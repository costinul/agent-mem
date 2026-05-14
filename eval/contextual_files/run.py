"""
Contextual file memory evaluation runner.

Ingests 10 synthetic company documents into a thread, then queries memory for
each QA pair in validation.json and scores results using an LLM judge.

Usage:
    python run.py [options]

Environment variables:
    MEMORY_API_URL      Base URL of the memory API  (default: http://localhost:8080)
    MEMORY_API_KEY      API key                     (required)
    MEMORY_AGENT_ID     Agent ID for test runs      (required)
    AI_MODEL_JUDGE      Judge model ID              (required unless --no-judge)

Options:
    --no-judge          Skip LLM judge; record returned facts only
    --reuse-thread ID   Skip ingest; run QA only against an existing thread
    --out FILE          Output file path            (default: results_contextual.json)
    --data DIR          Directory with files + validation.json  (default: ./data)
    --light             Use RecallLight instead of standard recall
    --zero              Use RecallZero (no LLM) instead of standard recall
"""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import sys
import time
from collections import defaultdict
from pathlib import Path

from dotenv import load_dotenv

env_path = Path(__file__).resolve().parent.parent.parent / ".env"
load_dotenv(dotenv_path=env_path)

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from shared.api_client import MemoryAPIClient
from shared.evaluator import Evaluator, JudgeResult

MODELS_JSON_PATH = Path(__file__).resolve().parent.parent.parent / "models.json"
DATA_DEFAULT = Path(__file__).parent / "data"

_MAX_RETRIES = 3
_RETRY_WAIT = 5.0
_PREVIEW_LEN = 80


def _preview(text: str) -> str:
    text = text.replace("\n", " ")
    return text[:_PREVIEW_LEN] + "…" if len(text) > _PREVIEW_LEN else text


def _resolve_model(model_id: str) -> tuple[str, str]:
    try:
        with open(MODELS_JSON_PATH, encoding="utf-8") as f:
            entries = json.load(f)
    except (FileNotFoundError, json.JSONDecodeError):
        return model_id, "azure"
    for entry in entries:
        if entry.get("id") == model_id:
            return entry.get("model", model_id), entry.get("provider", "azure")
    return model_id, "azure"


async def _with_retries(label: str, action):
    for attempt in range(1, _MAX_RETRIES + 2):
        try:
            t0 = time.time()
            result = await action()
            return result, time.time() - t0, None
        except Exception as exc:
            if attempt <= _MAX_RETRIES:
                print(f"  [WARN] {label} attempt {attempt}: {exc}. Retrying…", flush=True)
                await asyncio.sleep(_RETRY_WAIT)
            else:
                return None, None, exc
    return None, None, RuntimeError("unreachable")


def _build_evaluator() -> Evaluator:
    judge_id = os.environ.get("AI_MODEL_JUDGE")
    if not judge_id:
        sys.exit("[ERROR] AI_MODEL_JUDGE is required.")
    model, provider = _resolve_model(judge_id)

    if provider == "google":
        key = os.environ.get("GEMINI_API_KEY")
        if not key:
            sys.exit("[ERROR] GEMINI_API_KEY required for Google judge.")
        return Evaluator(model=model, provider="google", gemini_api_key=key)
    if provider == "cerebras":
        key = os.environ.get("CEREBRAS_API_KEY")
        if not key:
            sys.exit("[ERROR] CEREBRAS_API_KEY required for Cerebras judge.")
        return Evaluator(model=model, provider="cerebras", cerebras_api_key=key)
    key = os.environ.get("AZURE_OPENAI_API_KEY")
    if not key:
        sys.exit("[ERROR] AZURE_OPENAI_API_KEY required.")
    endpoint = os.environ.get("AZURE_OPENAI_ENDPOINT", "https://cchat-ai.cognitiveservices.azure.com/")
    return Evaluator(model=model, provider="azure", azure_api_key=key, azure_endpoint=endpoint)


async def run_eval(
    data_dir: Path,
    client: MemoryAPIClient,
    evaluator: Evaluator | None,
    reuse_thread_id: str | None,
    recall_mode: str,
) -> dict:
    val = json.loads((data_dir / "validation.json").read_text(encoding="utf-8"))
    qa_pairs = val["qa"]
    files = sorted(data_dir.glob("file_*.txt"))

    if not files:
        sys.exit(f"[ERROR] No file_*.txt found in {data_dir}. Run generate.py first.")

    # ── ingest ────────────────────────────────────────────────────────────
    if reuse_thread_id:
        thread_id = reuse_thread_id
        print(f"Reusing thread={thread_id} (skipping ingest)", flush=True)
    else:
        thread = await client.create_thread()
        thread_id = thread["id"]
        print(f"Created thread={thread_id}", flush=True)

        for i, fpath in enumerate(files, 1):
            content = fpath.read_text(encoding="utf-8")
            print(f"  [{i}/{len(files)}] ingesting {fpath.name} ({len(content):,} chars)…", flush=True)
            _, elapsed, err = await _with_retries(
                f"ingest {fpath.name}",
                lambda c=content, n=fpath.name: client.ingest(thread_id, "document", c, author=n),
            )
            if err:
                print(f"  [ERROR] failed to ingest {fpath.name}: {err}", flush=True)
            else:
                print(f"  [{i}/{len(files)}] done ({elapsed:.1f}s)", flush=True)

    # ── recall + judge ────────────────────────────────────────────────────
    qa_results = []
    for i, qa in enumerate(qa_pairs, 1):
        question = qa["question"]
        answer = qa["answer"]
        category = qa.get("category", "unknown")

        print(f"  [qa {i}/{len(qa_pairs)}] {category}: \"{_preview(question)}\"", flush=True)

        memory_output, _, err = await _with_retries(
            f"recall qa {i}",
            lambda q=question: client.recall(thread_id, q, mode=recall_mode),
        )
        if err or memory_output is None:
            facts = []
            recall_debug = {"error": str(err)}
        else:
            facts = [f["text"] for f in memory_output.get("facts", [])]
            recall_debug = memory_output.get("debug")

        judge_result: JudgeResult | None = None
        if evaluator is not None:
            result, _, err = await _with_retries(
                f"judge qa {i}",
                lambda q=question, a=answer, fs=facts: evaluator.judge(q, a, fs),
            )
            if err or result is None:
                judge_result = JudgeResult(
                    recall_score="fail", answer_score="fail",
                    fact_relevance=[], reason=str(err),
                )
            else:
                judge_result = result

            p = judge_result.precision
            p_str = f"{p:.2f}" if p is not None else "n/a"
            print(
                f"    r={judge_result.recall_score} a={judge_result.answer_score} "
                f"p={p_str} facts={len(facts)}",
                flush=True,
            )
        else:
            print(f"    facts={len(facts)} (judge skipped)", flush=True)

        qa_results.append({
            "question": question,
            "ground_truth": answer,
            "category": category,
            "source_files": qa.get("source_files", []),
            "facts_returned": facts,
            "judge": judge_result.to_dict() if judge_result else None,
            "recall_debug": recall_debug,
        })

    return {
        "sample_id": "contextual_files",
        "thread_id": thread_id,
        "qa": qa_results,
    }


# ── summary ────────────────────────────────────────────────────────────────

def _compute_summary(results: list[dict]) -> dict:
    def _empty():
        return {"count": 0, "recall_pass": 0, "recall_partial": 0, "recall_fail": 0, "skipped": 0}

    total = _empty()
    by_cat: dict[str, dict] = defaultdict(_empty)

    for sample in results:
        for qa in sample["qa"]:
            j = qa.get("judge")
            cat = qa.get("category", "unknown")
            for d in (total, by_cat[cat]):
                d["count"] += 1
                if j is None:
                    d["skipped"] += 1
                else:
                    score = j.get("recall_score", "fail")
                    d[f"recall_{score}"] = d.get(f"recall_{score}", 0) + 1

    def _score(d: dict):
        n = d["count"] - d["skipped"]
        if n == 0:
            return None
        return round((d["recall_pass"] + 0.5 * d["recall_partial"]) / n, 3)

    return {
        "total": {**total, "score": _score(total)},
        "by_category": {cat: {**b, "score": _score(b)} for cat, b in sorted(by_cat.items())},
    }


def _print_summary(results: list[dict]) -> None:
    s = _compute_summary(results)
    t = s["total"]
    print("\n" + "=" * 60)
    print("CONTEXTUAL FILE EVAL SUMMARY")
    print("=" * 60)
    print(f"  Questions : {t['count']}  (skipped: {t['skipped']})")
    print(
        f"  Recall    : pass={t['recall_pass']}  partial={t['recall_partial']}  "
        f"fail={t['recall_fail']}  score={t['score']}"
    )
    print("\n  By category:")
    for cat, b in s["by_category"].items():
        print(
            f"    {cat:<12} count={b['count']}  pass={b['recall_pass']}  "
            f"partial={b['recall_partial']}  fail={b['recall_fail']}  score={b['score']}"
        )
    print("=" * 60 + "\n")


# ── entrypoint ─────────────────────────────────────────────────────────────

def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Contextual file memory evaluation")
    p.add_argument("--no-judge", action="store_true", help="Skip LLM judge")
    p.add_argument("--reuse-thread", default=None, help="Reuse existing thread_id, skip ingest")
    p.add_argument("--out", default="results_contextual.json", help="Output JSON file")
    p.add_argument("--data", default=str(DATA_DEFAULT), help="Directory with data files")
    p.add_argument("--light", action="store_true", help="Use RecallLight")
    p.add_argument("--zero", action="store_true", help="Use RecallZero (no LLM)")
    return p.parse_args()


async def main() -> None:
    args = parse_args()

    if args.light and args.zero:
        sys.exit("[ERROR] --light and --zero are mutually exclusive")
    recall_mode = "zero" if args.zero else "light" if args.light else "standard"

    api_url = os.environ.get("MEMORY_API_URL", "http://localhost:8080")
    api_key = os.environ.get("MEMORY_API_KEY") or sys.exit("[ERROR] MEMORY_API_KEY required.")
    agent_id = os.environ.get("MEMORY_AGENT_ID") or sys.exit("[ERROR] MEMORY_AGENT_ID required.")

    evaluator = None if args.no_judge else _build_evaluator()

    print("=" * 60)
    print("Contextual File Evaluation")
    print("=" * 60)
    print(f"  data dir    : {args.data}")
    print(f"  API         : {api_url}  agent={agent_id}")
    print(f"  recall mode : {recall_mode}")
    print(f"  judge       : {'disabled' if args.no_judge else 'enabled'}")
    print("=" * 60 + "\n")

    t0 = time.time()
    async with MemoryAPIClient(api_url, api_key, agent_id) as client:
        result = await run_eval(
            Path(args.data), client, evaluator, args.reuse_thread, recall_mode
        )
    elapsed = time.time() - t0

    results = [result]
    output = {
        "dataset": "contextual_files",
        "elapsed_seconds": round(elapsed, 1),
        "recall_mode": recall_mode,
        "summary": _compute_summary(results),
        "conversations": results,
    }
    out_path = Path(args.out)
    out_path.write_text(json.dumps(output, indent=2, ensure_ascii=False), encoding="utf-8")
    print(f"Results written to: {out_path}  (elapsed: {elapsed:.1f}s)")
    _print_summary(results)


if __name__ == "__main__":
    asyncio.run(main())
