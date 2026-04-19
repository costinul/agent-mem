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
    --concurrency N     Max parallel conversations  (default: 3)
    --limit N           Only evaluate first N conversations
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

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from shared.api_client import MemoryAPIClient
from shared.evaluator import Evaluator

LOCOMO_DATA_URL = (
    "https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json"
)
DATA_DEFAULT = Path(__file__).parent / "data" / "locomo10.json"


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


# ── evaluation logic ───────────────────────────────────────────────────────────

async def evaluate_sample(
    sample: dict,
    client: MemoryAPIClient,
    evaluator: Evaluator,
    semaphore: asyncio.Semaphore,
) -> dict:
    sample_id = str(sample["sample_id"])
    thread = await client.create_thread()
    thread_id = thread["id"]
    conversation = sample["conversation"]
    qa_pairs = sample.get("qa", [])

    async with semaphore:
        # 1. Ingest all turns in chronological order (sequential within a sample)
        for _session_key, turns in _sessions_in_order(conversation):
            for turn in turns:
                text = turn.get("text", "").strip()
                if not text:
                    continue
                role = _speaker_to_role(turn.get("speaker", ""), conversation)
                try:
                    await client.ingest(thread_id, role, text)
                except Exception as exc:
                    print(f"  [WARN] ingest error in sample {sample_id}: {exc}")

        # 2. Query + judge each QA pair
        qa_results = []
        for qa in qa_pairs:
            question = qa.get("question", "").strip()
            ground_truth = qa.get("answer", "").strip()
            category = qa.get("category", "unknown")

            if not question or not ground_truth:
                continue

            try:
                memory_output = await client.recall(thread_id, question)
                facts = [f["text"] for f in memory_output.get("facts", [])]
            except Exception as exc:
                print(f"  [WARN] query error in sample {sample_id}: {exc}")
                facts = []

            try:
                result = await evaluator.judge(question, ground_truth, facts)
                score = result.score
                reason = result.reason
            except Exception as exc:
                print(f"  [WARN] judge error in sample {sample_id}: {exc}")
                score = "fail"
                reason = str(exc)

            qa_results.append({
                "question": question,
                "ground_truth": ground_truth,
                "category": category,
                "facts_returned": facts,
                "score": score,
                "reason": reason,
            })

    return {"sample_id": sample_id, "thread_id": thread_id, "qa": qa_results}


# ── summary ────────────────────────────────────────────────────────────────────

def print_summary(results: list[dict]) -> None:
    totals: dict[str, int] = defaultdict(int)
    by_category: dict[str, dict[str, int]] = defaultdict(lambda: defaultdict(int))

    for sample in results:
        for qa in sample["qa"]:
            score = qa["score"]
            cat = qa["category"]
            totals[score] += 1
            by_category[cat][score] += 1

    total = sum(totals.values())
    if total == 0:
        print("No QA results.")
        return

    print("\n" + "=" * 50)
    print("EVALUATION SUMMARY")
    print("=" * 50)
    print(f"Total questions : {total}")
    print(f"  pass          : {totals['pass']}  ({100*totals['pass']//total}%)")
    print(f"  partial       : {totals['partial']}  ({100*totals['partial']//total}%)")
    print(f"  fail          : {totals['fail']}  ({100*totals['fail']//total}%)")

    print("\nBy category:")
    for cat, scores in sorted(by_category.items()):
        cat_total = sum(scores.values())
        pct = 100 * scores["pass"] // cat_total if cat_total else 0
        print(f"  {cat:<30} pass={scores['pass']}/{cat_total} ({pct}%)")
    print("=" * 50 + "\n")


# ── entrypoint ─────────────────────────────────────────────────────────────────

def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="LoCoMo memory evaluation")
    p.add_argument("--concurrency", type=int, default=3, help="Max parallel conversations")
    p.add_argument("--limit", type=int, default=None, help="Limit number of conversations")
    p.add_argument("--cleanup", action="store_true", help="Delete ingested facts after run")
    p.add_argument("--out", default="results.json", help="Output JSON file")
    p.add_argument("--data", default=str(DATA_DEFAULT), help="Path to locomo10.json")
    return p.parse_args()


async def main() -> None:
    args = parse_args()

    api_url = os.environ.get("MEMORY_API_URL", "http://localhost:8080")
    api_key = os.environ["MEMORY_API_KEY"]
    agent_id = os.environ["MEMORY_AGENT_ID"]
    openai_key = os.environ["OPENAI_API_KEY"]

    samples = load_dataset(Path(args.data))
    if args.limit:
        samples = samples[: args.limit]

    print(f"Running LoCoMo eval: {len(samples)} conversations, concurrency={args.concurrency}")
    print(f"  API: {api_url}  agent={agent_id}\n")

    semaphore = asyncio.Semaphore(args.concurrency)
    evaluator = Evaluator(api_key=openai_key)

    start = time.time()
    async with MemoryAPIClient(api_url, api_key, agent_id) as client:
        tasks = [
            evaluate_sample(sample, client, evaluator, semaphore) for sample in samples
        ]
        results = await asyncio.gather(*tasks)

    elapsed = time.time() - start

    output = {
        "dataset": "locomo",
        "elapsed_seconds": round(elapsed, 1),
        "conversations": list(results),
    }
    with open(args.out, "w", encoding="utf-8") as f:
        json.dump(output, f, indent=2, ensure_ascii=False)

    print_summary(list(results))
    print(f"Results written to: {args.out}  (elapsed: {elapsed:.1f}s)")

    if args.cleanup:
        print("--cleanup: fact deletion is not yet implemented (requires listing facts by agent).")


if __name__ == "__main__":
    asyncio.run(main())
