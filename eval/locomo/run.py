"""
LoCoMo evaluation runner.

Downloads locomo10.json from GitHub (or reads from ./data/locomo10.json),
ingests all conversation turns into the memory API, then queries memory for
each QA pair and scores results using an LLM judge.

Usage:
    python run.py [options]

Environment variables:
    MEMORY_API_URL      Base URL of the memory API  (default: http://localhost:8080)
    MEMORY_API_KEY      API key for the account     (required for --target our_api)
    MEMORY_AGENT_ID     Agent ID for test runs      (required for --target our_api)
    MEM0_API_KEY        API key for mem0            (required for --target mem0)
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
    --target TARGET     Target API: 'our_api' (default) or 'mem0'

Debug workflow (fast iteration without re-ingesting):
    1. Flag your dev API key in Postgres:
           UPDATE api_keys SET debug=true WHERE prefix='<prefix>';
       This makes every /memory/recall response include a "debug" block with
       candidate lists, counts, query_date, and per-fact window flags.

    2. Ingest once and note the thread_id printed at the start:
           python run.py --limit 1 --out baseline.json

    3. Iterate quickly (seconds instead of 45 min) using --reuse-thread:
           python run.py --limit 1 --reuse-thread <thread_id> --out v3f.json
       The ingest step is skipped entirely; only recall + judge runs.

    The "recall_debug" key is captured per QA entry in the output JSON
    whenever the API key has debug=true.
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
from datetime import timezone
from typing import Awaitable, Callable, TypeVar
from dotenv import load_dotenv

try:
    from dateutil import parser as _dateutil_parser
    _HAS_DATEUTIL = True
except ImportError:
    _HAS_DATEUTIL = False

# Load .env from the repository root
env_path = Path(__file__).resolve().parent.parent.parent / ".env"
load_dotenv(dotenv_path=env_path)

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from shared.api_client import MemoryAPIClient, Mem0APIClient
from shared.evaluator import Evaluator

LOCOMO_DATA_URL = (
    "https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json"
)
DATA_DEFAULT = Path(__file__).parent / "data" / "locomo10.json"

_PREVIEW_LEN = 80
_MAX_RETRIES = 3
_RETRY_WAIT_SECONDS = 5.0
_T = TypeVar("_T")


def _parse_session_datetime(raw: str | None) -> str | None:
    """Parse a locomo session date string (e.g. '1:56 pm on 8 May, 2023') to ISO 8601."""
    if not raw:
        return None
    if _HAS_DATEUTIL:
        try:
            dt = _dateutil_parser.parse(raw, dayfirst=True)
            return dt.replace(tzinfo=timezone.utc).isoformat()
        except Exception:
            return None
    return None


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

async def _run_with_retries(
    label: str,
    sample_id: str,
    action: Callable[[], Awaitable[_T]],
    retries: int = _MAX_RETRIES,
    wait_seconds: float = _RETRY_WAIT_SECONDS,
) -> tuple[_T | None, float | None, int, int, Exception | None]:
    attempts = retries + 1
    transient_errors = 0
    for attempt in range(1, attempts + 1):
        try:
            started = time.time()
            result = await action()
            return result, time.time() - started, transient_errors, 0, None
        except Exception as exc:
            if attempt < attempts:
                transient_errors += 1
                print(
                    f"  [WARN] {label} error in sample {sample_id} (attempt {attempt}/{attempts}): {exc}. "
                    f"Retrying in {wait_seconds:.0f}s...",
                    flush=True,
                )
                await asyncio.sleep(wait_seconds)
                continue
            print(
                f"  [WARN] {label} error in sample {sample_id} (attempt {attempt}/{attempts}): {exc}",
                flush=True,
            )
            return None, None, 0, 1, exc
    return None, None, 0, 1, RuntimeError("unreachable retry state")

async def evaluate_sample(
    sample: dict,
    client: MemoryAPIClient | Mem0APIClient,
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

    ingest_durations: list[float] = []
    recall_durations: list[float] = []
    soft_errors = 0
    hard_errors = 0

    async with semaphore:
        if not reuse_thread_id:
            # 1. Ingest all turns in chronological order (sequential within a sample)
            ingest_start = time.time()
            sessions = _sessions_in_order(conversation)
            total_turns = sum(
                1 for _k, turns in sessions for t in turns if t.get("text", "").strip()
            )
            ingested = 0
            last_session_iso: str | None = None
            for session_key, turns in sessions:
                session_iso = _parse_session_datetime(conversation.get(f"{session_key}_date_time"))
                if session_iso:
                    last_session_iso = session_iso
                for turn in turns:
                    text = turn.get("text", "").strip()
                    if not text:
                        continue
                    ingested += 1
                    role = _speaker_to_role(turn.get("speaker", ""), conversation)
                    blip = (turn.get("blip_caption") or "").strip() or None
                    suffix = f"  +img: \"{_preview(blip)}\"" if blip else ""
                    print(
                        f"  [{sample_id} {short_tid} {session_key} turn {ingested}/{total_turns}]"
                        f" {role}: \"{_preview(text)}\"{suffix}",
                        flush=True,
                    )
                    _, ingest_elapsed, soft_inc, hard_inc, _ = await _run_with_retries(
                        label=f"ingest ({session_key} turn {ingested}/{total_turns})",
                        sample_id=sample_id,
                        action=lambda: client.ingest(
                            thread_id,
                            role,
                            text,
                            author=turn.get("speaker") or None,
                            when=session_iso,
                            image_caption=blip,
                        ),
                    )
                    soft_errors += soft_inc
                    hard_errors += hard_inc
                    if hard_inc == 0 and ingest_elapsed is not None:
                        ingest_durations.append(ingest_elapsed)

            ingest_elapsed = time.time() - ingest_start
            print(
                f"[sample={sample_id}] ingest done: {ingested} turns in {ingest_elapsed:.1f}s",
                flush=True,
            )
        else:
            # When reusing a thread, use the last session date from the conversation as event_date for recall.
            sessions = _sessions_in_order(conversation)
            last_session_iso = None
            for session_key, _turns in sessions:
                iso = _parse_session_datetime(conversation.get(f"{session_key}_date_time"))
                if iso:
                    last_session_iso = iso

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

            memory_output, recall_elapsed, soft_inc, hard_inc, recall_exc = await _run_with_retries(
                label=f"recall (qa {qi}/{total_qa})",
                sample_id=sample_id,
                action=lambda: client.recall(thread_id, question, when=last_session_iso),
            )
            soft_errors += soft_inc
            hard_errors += hard_inc
            if hard_inc == 0 and memory_output is not None:
                if recall_elapsed is not None:
                    recall_durations.append(recall_elapsed)
                facts = [f["text"] for f in memory_output.get("facts", [])]
                recall_debug = memory_output.get("debug")
            else:
                facts = []
                recall_debug = {"error": str(recall_exc) if recall_exc else "unknown recall error"}

            if evaluator is None:
                score = "skipped"
                reason = "--no-judge"
            else:
                result, _, soft_inc, hard_inc, judge_exc = await _run_with_retries(
                    label=f"judge (qa {qi}/{total_qa})",
                    sample_id=sample_id,
                    action=lambda: evaluator.judge(question, ground_truth, facts),
                )
                soft_errors += soft_inc
                hard_errors += hard_inc
                if hard_inc == 0 and result is not None:
                    score = result.score
                    reason = result.reason
                else:
                    score = "fail"
                    reason = str(judge_exc) if judge_exc else "unknown judge error"

            if score == "fail" and evaluator is not None:
                revised = qa.get("answer_revised")
                if revised:
                    retry, _, soft_inc, hard_inc, _ = await _run_with_retries(
                        label=f"judge-revised (qa {qi}/{total_qa})",
                        sample_id=sample_id,
                        action=lambda: evaluator.judge(question, str(revised), facts),
                    )
                    soft_errors += soft_inc
                    hard_errors += hard_inc
                    if hard_inc == 0 and retry is not None:
                        score = retry.score
                        reason = retry.reason

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
                "recall_debug": recall_debug,
            })

        judge_elapsed = time.time() - judge_start
        print(
            f"[sample={sample_id}] recall+judge done: {len(qa_results)} QAs in {judge_elapsed:.1f}s",
            flush=True,
        )

    return {
        "sample_id": sample_id,
        "thread_id": thread_id,
        "qa": qa_results,
        "ingest_durations": ingest_durations,
        "recall_durations": recall_durations,
        "soft_errors": soft_errors,
        "hard_errors": hard_errors,
    }


# ── summary ────────────────────────────────────────────────────────────────────

def _compute_summary(results: list[dict]) -> dict:
    totals: dict[str, int] = defaultdict(int)
    by_category: dict[str, dict[str, int]] = defaultdict(lambda: defaultdict(int))
    failed_or_partial: list[dict] = []
    soft_errors_total = sum(int(sample.get("soft_errors", 0)) for sample in results)
    hard_errors_total = sum(int(sample.get("hard_errors", 0)) for sample in results)

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
                    "reason": qa["reason"],
                })

    total = sum(totals.values())
    by_category_out = {}
    for cat, scores in sorted(by_category.items()):
        cat_total = sum(scores.values())
        by_category_out[cat] = {
            "total": cat_total,
            **{k: scores[k] for k in ("pass", "partial", "fail", "skipped") if scores[k]},
        }

    return {
        "total_questions": total,
        "soft_errors": soft_errors_total,
        "hard_errors": hard_errors_total,
        "scores": {k: totals[k] for k in ("pass", "partial", "fail", "skipped") if totals[k]},
        "by_category": by_category_out,
        "failed_or_partial": failed_or_partial,
    }


def print_summary(results: list[dict]) -> None:
    s = _compute_summary(results)
    total = s["total_questions"]

    if total == 0:
        print("No QA results.")
        print(f"Soft errors: {s['soft_errors']}")
        print(f"Hard errors: {s['hard_errors']}")
        return

    print("\n" + "=" * 50)
    print("EVALUATION SUMMARY")
    print("=" * 50)
    print(f"Total questions : {total}")
    print(f"Soft errors     : {s['soft_errors']}")
    print(f"Hard errors     : {s['hard_errors']}")
    for score_key in ("pass", "partial", "fail", "skipped"):
        count = s["scores"].get(score_key, 0)
        if count:
            pct = 100 * count // total
            print(f"  {score_key:<10}: {count}  ({pct}%)")

    print("\nBy category:")
    for cat, scores in s["by_category"].items():
        cat_total = scores["total"]
        pct = 100 * scores.get("pass", 0) // cat_total if cat_total else 0
        print(f"  {cat:<30} pass={scores.get('pass', 0)}/{cat_total} ({pct}%)")

    if s["failed_or_partial"]:
        print("\nFailed or Partial Reasons:")
        for item in s["failed_or_partial"]:
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
    p.add_argument(
        "--target",
        choices=["our_api", "mem0"],
        default="our_api",
        help="Target API to test against: 'our_api' (default) or 'mem0'",
    )
    return p.parse_args()


def _write_output(out_path: str, results: list, elapsed: float, data_path: str) -> None:
    all_ingest = [d for r in results for d in r.get("ingest_durations", [])]
    all_recall = [d for r in results for d in r.get("recall_durations", [])]
    soft_errors_total = sum(int(sample.get("soft_errors", 0)) for sample in results)
    hard_errors_total = sum(int(sample.get("hard_errors", 0)) for sample in results)

    timing = {
        "total_ingest_duration_seconds": round(sum(all_ingest), 3),
        "avg_ingest_duration_seconds": round(sum(all_ingest) / len(all_ingest), 3) if all_ingest else 0,
        "total_recall_duration_seconds": round(sum(all_recall), 3),
        "avg_recall_duration_seconds": round(sum(all_recall) / len(all_recall), 3) if all_recall else 0,
    }

    # Strip the raw duration lists from the per-conversation output to keep it clean
    conversations = [
        {k: v for k, v in r.items() if k not in ("ingest_durations", "recall_durations")}
        for r in results
    ]

    summary = _compute_summary(results)

    output = {
        "dataset": Path(data_path).stem,
        "elapsed_seconds": round(elapsed, 1),
        "summary": summary,
        "timing": timing,
        "error_stats": {
            "soft_errors": soft_errors_total,
            "hard_errors": hard_errors_total,
            "max_retries": _MAX_RETRIES,
            "retry_wait_seconds": _RETRY_WAIT_SECONDS,
        },
        "conversations": conversations,
    }
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(output, f, indent=2, ensure_ascii=False)
    print(f"Results written to: {out_path}  (elapsed: {elapsed:.1f}s)")


async def main() -> None:
    args = parse_args()

    if args.target == "mem0":
        mem0_api_key = os.environ["MEM0_API_KEY"]
        api_url = ""
        api_key = ""
        agent_id = ""
    else:
        api_url = os.environ.get("MEMORY_API_URL", "http://localhost:8080")
        api_key = os.environ["MEMORY_API_KEY"]
        agent_id = os.environ["MEMORY_AGENT_ID"]
        mem0_api_key = ""

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
    if args.target == "mem0":
        print(f"  API         : mem0 (https://api.mem0.ai/v3)")
    else:
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

    client_ctx = (
        Mem0APIClient(mem0_api_key)
        if args.target == "mem0"
        else MemoryAPIClient(api_url, api_key, agent_id)
    )

    try:
        async with client_ctx as client:
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
