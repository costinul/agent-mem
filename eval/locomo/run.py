"""
LoCoMo evaluation runner.

Downloads locomo10.json from GitHub (or reads from ./data/locomo10.json),
ingests all conversation turns into the memory API, then queries memory for
each QA pair and scores results using an LLM judge.

Usage:
    python run.py [options]

Environment variables:
    MEMORY_API_URL      Base URL of the memory API  (default: http://localhost:8080)
    MEMORY_API_KEY      API key for the account     (required)
    MEMORY_AGENT_ID     Agent ID for test runs      (required)
    OPENAI_API_KEY      OpenAI key for the judge    (required)

Options:
    --concurrency N     Max parallel conversations  (default: 1)
    --start N           Skip the first N conversations (for resuming a partial run)
    --limit N           Only evaluate first N conversations (applied after --start)
    --reuse-thread ID   Skip ingest, run QA only against an existing thread (requires single sample in scope)
    --no-judge          Skip LLM judge; record facts only (score="skipped")
    --cleanup           Delete ingested facts after the run
    --out FILE          Output file path            (default: results.json)
    --data FILE         Path to locomo10.json       (default: ./data/locomo10.json,
                                                     auto-downloaded if missing)
"""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import sys
import time
import urllib.request
from collections import defaultdict
from pathlib import Path
from dotenv import load_dotenv

# Load .env from the repository root
env_path = Path(__file__).resolve().parent.parent.parent / ".env"
load_dotenv(dotenv_path=env_path)

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from shared.api_client import MemoryAPIClient
from shared.evaluator import Evaluator

LOCOMO_DATA_URL = (
    "https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json"
)
DATA_DEFAULT = Path(__file__).parent / "data" / "locomo10.json"

_PREVIEW_LEN = 80


# ── dataset helpers ────────────────────────────────────────────────────────────

def load_dataset(path: Path) -> list[dict]:
    if not path.exists():
        print(f"Dataset not found at {path}, downloading from GitHub...")
        path.parent.mkdir(parents=True, exist_ok=True)
        urllib.request.urlretrieve(LOCOMO_DATA_URL, path)
        print("Download complete.")
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def _sessions_in_order(conversation: dict) -> list[tuple[str, list[dict]]]:
    """Return [(session_key, [turns...]), ...] sorted numerically."""
    sessions = []
    i = 1
    while True:
        key = f"session_{i}"
        if key not in conversation:
            break
        sessions.append((key, conversation[key]))
        i += 1
    return sessions


def _speaker_to_role(speaker_name: str, conversation: dict) -> str:
    """Map speaker name to API role kind.  speaker_a -> user, speaker_b -> agent."""
    if speaker_name == conversation.get("speaker_a"):
        return "user"
    return "agent"


def _count_turns(samples: list[dict]) -> int:
    total = 0
    for s in samples:
        for _key, turns in _sessions_in_order(s["conversation"]):
            total += sum(1 for t in turns if t.get("text", "").strip())
    return total


def _count_qa(samples: list[dict]) -> int:
    return sum(len(s.get("qa", [])) for s in samples)


def _preview(text: str) -> str:
    text = text.replace("\n", " ")
    return text[:_PREVIEW_LEN] + "…" if len(text) > _PREVIEW_LEN else text


# ── evaluation logic ───────────────────────────────────────────────────────────

async def evaluate_sample(
    sample: dict,
    client: MemoryAPIClient,
    evaluator: Evaluator | None,
    semaphore: asyncio.Semaphore,
    reuse_thread_id: str | None = None,
) -> dict:
    sample_id = str(sample["sample_id"])
    if reuse_thread_id:
        thread_id = reuse_thread_id
        print(f"[sample={sample_id}] reusing thread={thread_id} (skipping ingest)", flush=True)
    else:
        thread = await client.create_thread()
        thread_id = thread["id"]
        print(f"[sample={sample_id}] created thread={thread_id}", flush=True)
    short_tid = thread_id[:8]

    conversation = sample["conversation"]
    qa_pairs = sample.get("qa", [])

    async with semaphore:
        if not reuse_thread_id:
            # 1. Ingest all turns in chronological order (sequential within a sample)
            ingest_start = time.time()
            sessions = _sessions_in_order(conversation)
            total_turns = sum(
                1 for _k, turns in sessions for t in turns if t.get("text", "").strip()
            )
            ingested = 0
            for session_key, turns in sessions:
                for turn in turns:
                    text = turn.get("text", "").strip()
                    if not text:
                        continue
                    ingested += 1
                    role = _speaker_to_role(turn.get("speaker", ""), conversation)
                    print(
                        f"  [{sample_id} {short_tid} {session_key} turn {ingested}/{total_turns}]"
                        f" {role}: \"{_preview(text)}\"",
                        flush=True,
                    )
                    try:
                        await client.ingest(thread_id, role, text, author=turn.get("speaker") or None)
                    except Exception as exc:
                        print(f"  [WARN] ingest error in sample {sample_id}: {exc}", flush=True)

            ingest_elapsed = time.time() - ingest_start
            print(
                f"[sample={sample_id}] ingest done: {ingested} turns in {ingest_elapsed:.1f}s",
                flush=True,
            )

        # 2. Query + judge each QA pair
        qa_results = []
        total_qa = len(qa_pairs)
        judge_start = time.time()

        for qi, qa in enumerate(qa_pairs, 1):
            question = str(qa.get("question", "")).strip()
            ground_truth = str(qa.get("answer", "")).strip()
            category = qa.get("category", "unknown")

            if not question or not ground_truth:
                continue

            print(
                f"  [{sample_id} qa {qi}/{total_qa}] Q: \"{_preview(question)}\" → recalling…",
                flush=True,
            )
            qa_t0 = time.time()

            try:
                memory_output = await client.recall(thread_id, question)
                facts = [f["text"] for f in memory_output.get("facts", [])]
            except Exception as exc:
                print(f"  [WARN] query error in sample {sample_id}: {exc}", flush=True)
                facts = []

            if evaluator is None:
                score = "skipped"
                reason = "--no-judge"
            else:
                try:
                    result = await evaluator.judge(question, ground_truth, facts)
                    score = result.score
                    reason = result.reason
                except Exception as exc:
                    print(f"  [WARN] judge error in sample {sample_id}: {exc}", flush=True)
                    score = "fail"
                    reason = str(exc)

            elapsed_qa = time.time() - qa_t0
            print(
                f"  [{sample_id} qa {qi}/{total_qa}] score={score} facts={len(facts)} ({elapsed_qa:.1f}s)",
                flush=True,
            )

            qa_results.append({
                "question": question,
                "ground_truth": ground_truth,
                "category": category,
                "facts_returned": facts,
                "score": score,
                "reason": reason,
            })

        judge_elapsed = time.time() - judge_start
        print(
            f"[sample={sample_id}] recall+judge done: {len(qa_results)} QAs in {judge_elapsed:.1f}s",
            flush=True,
        )

    return {"sample_id": sample_id, "thread_id": thread_id, "qa": qa_results}


# ── summary ────────────────────────────────────────────────────────────────────

def print_summary(results: list[dict]) -> None:
    totals: dict[str, int] = defaultdict(int)
    by_category: dict[str, dict[str, int]] = defaultdict(lambda: defaultdict(int))
    failed_or_partial: list[dict] = []

    for sample in results:
        for qa in sample["qa"]:
            score = qa["score"]
            cat = qa["category"]
            totals[score] += 1
            by_category[cat][score] += 1
            if score in ("partial", "fail"):
                failed_or_partial.append({
                    "sample_id": sample["sample_id"],
                    "question": qa["question"],
                    "score": score,
                    "reason": qa["reason"]
                })

    total = sum(totals.values())
    if total == 0:
        print("No QA results.")
        return

    print("\n" + "=" * 50)
    print("EVALUATION SUMMARY")
    print("=" * 50)
    print(f"Total questions : {total}")
    for score_key in ("pass", "partial", "fail", "skipped"):
        if totals[score_key]:
            pct = 100 * totals[score_key] // total
            print(f"  {score_key:<10}: {totals[score_key]}  ({pct}%)")

    print("\nBy category:")
    for cat, scores in sorted(by_category.items()):
        cat_total = sum(scores.values())
        pct = 100 * scores["pass"] // cat_total if cat_total else 0
        print(f"  {cat:<30} pass={scores['pass']}/{cat_total} ({pct}%)")

    if failed_or_partial:
        print("\nFailed or Partial Reasons:")
        for item in failed_or_partial:
            print(f"  [{item['sample_id']}] Q: {item['question']}")
            print(f"    Score: {item['score']}")
            print(f"    Reason: {item['reason']}")

    print("=" * 50 + "\n")


# ── entrypoint ─────────────────────────────────────────────────────────────────

def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="LoCoMo memory evaluation")
    p.add_argument("--concurrency", type=int, default=1, help="Max parallel conversations (default: 1)")
    p.add_argument("--start", type=int, default=0, help="Skip the first N conversations (resume helper)")
    p.add_argument("--limit", type=int, default=None, help="Limit number of conversations (applied after --start)")
    p.add_argument("--reuse-thread", default=None, help="Reuse an existing thread_id, skip ingest, run only QA. Requires --limit 1 (or one sample in scope).")
    p.add_argument("--no-judge", action="store_true", help="Skip LLM judge; record facts only")
    p.add_argument("--cleanup", action="store_true", help="Delete ingested facts after run")
    p.add_argument("--out", default="results.json", help="Output JSON file")
    p.add_argument("--data", default=str(DATA_DEFAULT), help="Path to locomo10.json")
    return p.parse_args()


def _write_output(out_path: str, results: list, elapsed: float, data_path: str) -> None:
    output = {
        "dataset": Path(data_path).stem,
        "elapsed_seconds": round(elapsed, 1),
        "conversations": list(results),
    }
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(output, f, indent=2, ensure_ascii=False)
    print(f"Results written to: {out_path}  (elapsed: {elapsed:.1f}s)")


async def main() -> None:
    args = parse_args()

    api_url = os.environ.get("MEMORY_API_URL", "http://localhost:8080")
    api_key = os.environ["MEMORY_API_KEY"]
    agent_id = os.environ["MEMORY_AGENT_ID"]

    samples = load_dataset(Path(args.data))
    if args.start:
        samples = samples[args.start:]
    if args.limit:
        samples = samples[: args.limit]

    if args.reuse_thread and len(samples) != 1:
        sys.exit(f"--reuse-thread requires exactly one sample in scope (got {len(samples)}). Use --start/--limit to narrow down.")

    total_turns = _count_turns(samples)
    total_qa = _count_qa(samples)

    print("=" * 60)
    print("LoCoMo Evaluation")
    print("=" * 60)
    print(f"  data file   : {args.data}")
    print(f"  samples     : {len(samples)}{f' (skipped first {args.start})' if args.start else ''}")
    print(f"  total turns : {total_turns}")
    print(f"  total QAs   : {total_qa}")
    print(f"  concurrency : {args.concurrency}")
    print(f"  judge       : {'disabled (--no-judge)' if args.no_judge else 'enabled'}")
    print(f"  API         : {api_url}  agent={agent_id}")
    print("=" * 60 + "\n")

    evaluator: Evaluator | None = None
    if not args.no_judge:
        openai_key = os.environ["AZURE_OPENAI_API_KEY"]
        openai_endpoint = os.environ.get(
            "AZURE_OPENAI_ENDPOINT", "https://cchat-ai.cognitiveservices.azure.com/"
        )
        evaluator = Evaluator(api_key=openai_key, endpoint=openai_endpoint)

    semaphore = asyncio.Semaphore(args.concurrency)
    start = time.time()
    results: list[dict] = []

    try:
        async with MemoryAPIClient(api_url, api_key, agent_id) as client:
            tasks = [
                evaluate_sample(sample, client, evaluator, semaphore, reuse_thread_id=args.reuse_thread)
                for sample in samples
            ]
            raw = await asyncio.gather(*tasks, return_exceptions=True)
            for sample, item in zip(samples, raw):
                if isinstance(item, BaseException):
                    print(f"[ERROR] sample {sample.get('sample_id')} crashed: {item!r}", flush=True)
                    continue
                results.append(item)
    except (KeyboardInterrupt, asyncio.CancelledError):
        print("\n[interrupted] Writing partial results…", flush=True)
    except Exception as exc:
        print(f"\n[ERROR] run aborted: {exc!r}. Writing partial results…", flush=True)
    finally:
        elapsed = time.time() - start
        _write_output(args.out, results, elapsed, args.data)
        print_summary(results)

    if args.cleanup:
        print("--cleanup: fact deletion is not yet implemented (requires listing facts by agent).")


if __name__ == "__main__":
    asyncio.run(main())
