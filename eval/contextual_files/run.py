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

def _empty_bucket() -> dict:
    return {
        "count": 0, "skipped": 0,
        "recall": {"pass": 0, "partial": 0, "fail": 0},
        "answer": {"pass": 0, "partial": 0, "fail": 0},
        "precision_sum": 0.0, "precision_count": 0,
        "f1_sum": 0.0, "f1_count": 0,
        "rank_sum": 0, "rank_count": 0, "rrank_sum": 0.0,
        "facts_sum": 0, "facts_min": None, "facts_max": 0,
    }


def _accumulate(b: dict, j: dict | None, facts_count: int) -> None:
    b["count"] += 1
    b["facts_sum"] += facts_count
    if b["facts_min"] is None or facts_count < b["facts_min"]:
        b["facts_min"] = facts_count
    if facts_count > b["facts_max"]:
        b["facts_max"] = facts_count
    if j is None:
        b["skipped"] += 1
        return
    for key in ("pass", "partial", "fail"):
        if j.get("recall_score") == key:
            b["recall"][key] += 1
        if j.get("answer_score") == key:
            b["answer"][key] += 1
    p = j.get("precision")
    if p is not None:
        b["precision_sum"] += p
        b["precision_count"] += 1
    f = j.get("f1")
    if f is not None:
        b["f1_sum"] += f
        b["f1_count"] += 1
    rank = j.get("first_relevant_rank")
    if rank is not None and rank > 0:
        b["rank_sum"] += rank
        b["rank_count"] += 1
        b["rrank_sum"] += 1.0 / rank


def _finalize(b: dict) -> dict:
    n = b["count"] - b["skipped"]

    def _score(counts: dict):
        if n == 0:
            return None
        return round((counts["pass"] + 0.5 * counts["partial"]) / n, 4)

    return {
        "count": b["count"],
        "skipped": b["skipped"],
        "recall": {**b["recall"], "score": _score(b["recall"])},
        "answer": {**b["answer"], "score": _score(b["answer"])},
        "precision": {"mean": round(b["precision_sum"] / b["precision_count"], 4) if b["precision_count"] else None},
        "f1": {"mean": round(b["f1_sum"] / b["f1_count"], 4) if b["f1_count"] else None},
        "first_relevant_rank": {
            "mean": round(b["rank_sum"] / b["rank_count"], 2) if b["rank_count"] else None,
            "mrr": round(b["rrank_sum"] / b["rank_count"], 4) if b["rank_count"] else None,
        },
        "facts_returned": {
            "mean": round(b["facts_sum"] / b["count"], 2) if b["count"] else 0,
            "min": b["facts_min"] if b["facts_min"] is not None else 0,
            "max": b["facts_max"],
        },
    }


def _compute_summary(results: list[dict]) -> dict:
    total = _empty_bucket()
    by_cat: dict[str, dict] = defaultdict(_empty_bucket)
    failed_or_partial: list[dict] = []

    for sample in results:
        for qa in sample["qa"]:
            j = qa.get("judge")
            cat = qa.get("category", "unknown")
            facts_count = len(qa.get("facts_returned", []))
            _accumulate(total, j, facts_count)
            _accumulate(by_cat[cat], j, facts_count)
            if j and (j.get("recall_score") in ("partial", "fail") or j.get("answer_score") in ("partial", "fail")):
                failed_or_partial.append({
                    "category": cat,
                    "question": qa["question"],
                    "ground_truth": qa["ground_truth"],
                    "recall_score": j.get("recall_score"),
                    "answer_score": j.get("answer_score"),
                    "precision": j.get("precision"),
                    "facts_count": facts_count,
                    "reason": j.get("reason", ""),
                    "source_files": qa.get("source_files", []),
                })

    return {
        "total_questions": total["count"],
        "metrics": _finalize(total),
        "by_category": {cat: _finalize(b) for cat, b in sorted(by_cat.items())},
        "failed_or_partial": failed_or_partial,
    }


def _fmt(v) -> str:
    if v is None:
        return "n/a"
    if isinstance(v, float):
        return f"{v:.3f}"
    return str(v)


def _print_summary(results: list[dict]) -> None:
    s = _compute_summary(results)
    m = s["metrics"]
    rec = m["recall"]
    ans = m["answer"]
    print("\n" + "=" * 60)
    print("CONTEXTUAL FILE EVAL SUMMARY")
    print("=" * 60)
    print(f"  Questions : {s['total_questions']}  (skipped: {m['skipped']})")
    print(f"  Recall    : pass={rec['pass']}  partial={rec['partial']}  fail={rec['fail']}  score={_fmt(rec['score'])}")
    print(f"  Answer    : pass={ans['pass']}  partial={ans['partial']}  fail={ans['fail']}  score={_fmt(ans['score'])}")
    print(f"  Precision : {_fmt(m['precision']['mean'])}   F1: {_fmt(m['f1']['mean'])}   MRR: {_fmt(m['first_relevant_rank']['mrr'])}")
    print(f"  Facts ret : mean={_fmt(m['facts_returned']['mean'])}  min={m['facts_returned']['min']}  max={m['facts_returned']['max']}")
    print("\n  By category:")
    for cat, b in s["by_category"].items():
        r = b["recall"]
        print(f"    {cat:<12} count={b['count']}  pass={r['pass']}  partial={r['partial']}  fail={r['fail']}  score={_fmt(r['score'])}")
    if s["failed_or_partial"]:
        print(f"\n  Failed/partial ({len(s['failed_or_partial'])}):")
        for item in s["failed_or_partial"]:
            p = item.get("precision")
            p_str = f"{p:.2f}" if isinstance(p, float) else "n/a"
            print(f"    [{item['category']}] r={item['recall_score']:<7} a={item['answer_score']:<7} p={p_str} :: {item['question']}")
            print(f"      → {item['reason']}")
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
    models = {k: os.environ.get(k, "") for k in (
        "AI_MODEL_DECOMPOSE", "AI_MODEL_EVALUATE", "AI_MODEL_SELECT_FACTS",
        "AI_MODEL_DECOMPOSE_QUERIES", "AI_MODEL_DECOMPOSE_RECALL",
        "AI_EMBEDDING_MODEL", "AI_MODEL_JUDGE",
    )}
    output = {
        "dataset": "contextual_files",
        "elapsed_seconds": round(elapsed, 1),
        "recall_mode": recall_mode,
        "models": {k: v for k, v in models.items() if v},
        "summary": _compute_summary(results),
        "conversations": results,
    }
    out_path = Path(args.out)
    out_path.write_text(json.dumps(output, indent=2, ensure_ascii=False), encoding="utf-8")
    print(f"Results written to: {out_path}  (elapsed: {elapsed:.1f}s)")
    _print_summary(results)


if __name__ == "__main__":
    asyncio.run(main())
