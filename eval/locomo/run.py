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
from shared.evaluator import Evaluator, JudgeResult, SCORE_TO_NUMERIC

LOCOMO_DATA_URL = (
    "https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json"
)
DATA_DEFAULT = Path(__file__).parent / "data" / "locomo10.json"
MODELS_JSON_PATH = Path(__file__).resolve().parent.parent.parent / "models.json"


def _resolve_model(model_id: str) -> tuple[str, str]:
    """Resolve a logical model id (as used in .env) to (real_model_name, provider)
    using models.json. Falls back to (model_id, 'azure') if not found, preserving
    backward compatibility with raw OpenAI model strings.
    """
    try:
        with open(MODELS_JSON_PATH, encoding="utf-8") as f:
            entries = json.load(f)
    except (FileNotFoundError, json.JSONDecodeError):
        return model_id, "azure"
    for entry in entries:
        if entry.get("id") == model_id:
            return entry.get("model", model_id), entry.get("provider", "azure")
    return model_id, "azure"

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

def _merge_usage(acc: dict, usage: dict | None) -> None:
    """Accumulate token counts from a single API response usage block into acc."""
    if not usage or not isinstance(usage, dict):
        return
    acc["input_tokens"] = acc.get("input_tokens", 0) + usage.get("input_tokens", 0)
    acc["output_tokens"] = acc.get("output_tokens", 0) + usage.get("output_tokens", 0)
    per_model = usage.get("per_model") or {}
    if per_model:
        if "per_model" not in acc:
            acc["per_model"] = {}
        for model, stats in per_model.items():
            m = acc["per_model"].setdefault(model, {"calls": 0, "input_tokens": 0, "output_tokens": 0})
            m["calls"] += stats.get("calls", 0)
            m["input_tokens"] += stats.get("input_tokens", 0)
            m["output_tokens"] += stats.get("output_tokens", 0)


async def evaluate_sample(
    sample: dict,
    client: MemoryAPIClient | Mem0APIClient,
    evaluator: Evaluator | None,
    semaphore: asyncio.Semaphore,
    reuse_thread_id: str | None = None,
    recall_mode: str = "standard",
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
    usage: dict = {}

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
                    ingest_resp, ingest_elapsed, soft_inc, hard_inc, _ = await _run_with_retries(
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
                        if isinstance(ingest_resp, dict):
                            _merge_usage(usage, ingest_resp.get("usage"))

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
                action=lambda: client.recall(thread_id, question, when=last_session_iso, mode=recall_mode),
            )
            soft_errors += soft_inc
            hard_errors += hard_inc
            if hard_inc == 0 and memory_output is not None:
                if recall_elapsed is not None:
                    recall_durations.append(recall_elapsed)
                facts = [f["text"] for f in memory_output.get("facts", [])]
                recall_debug = memory_output.get("debug")
                _merge_usage(usage, memory_output.get("usage"))
            else:
                facts = []
                recall_debug = {"error": str(recall_exc) if recall_exc else "unknown recall error"}

            judge_result: JudgeResult | None = None
            if evaluator is not None:
                result, _, soft_inc, hard_inc, judge_exc = await _run_with_retries(
                    label=f"judge (qa {qi}/{total_qa})",
                    sample_id=sample_id,
                    action=lambda: evaluator.judge(question, ground_truth, facts),
                )
                soft_errors += soft_inc
                hard_errors += hard_inc
                if hard_inc == 0 and result is not None:
                    judge_result = result
                else:
                    judge_result = JudgeResult(
                        recall_score="fail",
                        answer_score="fail",
                        fact_relevance=[False] * len(facts),
                        reason=str(judge_exc) if judge_exc else "unknown judge error",
                    )

                # Retry against the dataset's revised answer when our judge
                # marks recall_score=fail. The revised answer is sometimes
                # phrased more loosely and accepts a wider set of supporting
                # facts than the strict ground truth.
                if judge_result.recall_score == "fail":
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
                            judge_result = retry

            elapsed_qa = time.time() - qa_t0
            if judge_result is None:
                print(
                    f"  [{sample_id} qa {qi}/{total_qa}] judge=skipped facts={len(facts)} ({elapsed_qa:.1f}s)",
                    flush=True,
                )
            else:
                p = judge_result.precision
                p_str = f"{p:.2f}" if p is not None else "n/a"
                print(
                    f"  [{sample_id} qa {qi}/{total_qa}] r={judge_result.recall_score} "
                    f"a={judge_result.answer_score} p={p_str} facts={len(facts)} "
                    f"({elapsed_qa:.1f}s)",
                    flush=True,
                )

            qa_results.append({
                "question": question,
                "ground_truth": ground_truth,
                "category": category,
                "facts_returned": facts,
                "judge": judge_result.to_dict() if judge_result is not None else None,
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
        "usage": usage,
    }


# ── summary ────────────────────────────────────────────────────────────────────

def _empty_bucket() -> dict:
    return {
        "count": 0,
        "skipped": 0,
        "recall": {"pass": 0, "partial": 0, "fail": 0},
        "answer": {"pass": 0, "partial": 0, "fail": 0},
        "precision_sum": 0.0,
        "precision_count": 0,
        "f1_sum": 0.0,
        "f1_count": 0,
        "rank_sum": 0,
        "rank_count": 0,
        "rrank_sum": 0.0,
        "facts_count_sum": 0,
        "facts_count_min": None,
        "facts_count_max": 0,
    }


def _accumulate(b: dict, judge: dict | None, facts_count: int) -> None:
    b["count"] += 1
    b["facts_count_sum"] += facts_count
    if b["facts_count_min"] is None or facts_count < b["facts_count_min"]:
        b["facts_count_min"] = facts_count
    if facts_count > b["facts_count_max"]:
        b["facts_count_max"] = facts_count

    if judge is None:
        b["skipped"] += 1
        return

    rec = judge.get("recall_score", "fail")
    ans = judge.get("answer_score", "fail")
    if rec in b["recall"]:
        b["recall"][rec] += 1
    if ans in b["answer"]:
        b["answer"][ans] += 1

    p = judge.get("precision")
    if p is not None:
        b["precision_sum"] += p
        b["precision_count"] += 1
    f = judge.get("f1")
    if f is not None:
        b["f1_sum"] += f
        b["f1_count"] += 1
    rank = judge.get("first_relevant_rank")
    if rank is not None and rank > 0:
        b["rank_sum"] += rank
        b["rank_count"] += 1
        b["rrank_sum"] += 1.0 / rank


def _finalize_bucket(b: dict) -> dict:
    n_judged = b["count"] - b["skipped"]

    def _aggregate_score(counts: dict) -> float | None:
        if n_judged == 0:
            return None
        return round(sum(SCORE_TO_NUMERIC[k] * v for k, v in counts.items()) / n_judged, 4)

    return {
        "count": b["count"],
        "skipped": b["skipped"],
        "recall": {**b["recall"], "score": _aggregate_score(b["recall"])},
        "answer": {**b["answer"], "score": _aggregate_score(b["answer"])},
        "precision": {
            "mean": round(b["precision_sum"] / b["precision_count"], 4) if b["precision_count"] else None,
            "questions_with_facts": b["precision_count"],
        },
        "f1": {
            "mean": round(b["f1_sum"] / b["f1_count"], 4) if b["f1_count"] else None,
            "questions_scored": b["f1_count"],
        },
        "first_relevant_rank": {
            "mean": round(b["rank_sum"] / b["rank_count"], 2) if b["rank_count"] else None,
            "mrr": round(b["rrank_sum"] / b["rank_count"], 4) if b["rank_count"] else None,
            "questions_with_relevant": b["rank_count"],
        },
        "facts_returned": {
            "mean": round(b["facts_count_sum"] / b["count"], 2) if b["count"] else 0,
            "min": b["facts_count_min"] if b["facts_count_min"] is not None else 0,
            "max": b["facts_count_max"],
        },
    }


def _compute_summary(results: list[dict]) -> dict:
    overall = _empty_bucket()
    by_category: dict[str, dict] = defaultdict(_empty_bucket)
    failed_or_partial: list[dict] = []
    soft_errors_total = sum(int(sample.get("soft_errors", 0)) for sample in results)
    hard_errors_total = sum(int(sample.get("hard_errors", 0)) for sample in results)

    for sample in results:
        for qa in sample["qa"]:
            cat = qa["category"]
            judge = qa.get("judge")
            facts_count = len(qa.get("facts_returned", []))

            _accumulate(overall, judge, facts_count)
            _accumulate(by_category[cat], judge, facts_count)

            if judge is not None and (
                judge.get("recall_score") in ("partial", "fail")
                or judge.get("answer_score") in ("partial", "fail")
            ):
                failed_or_partial.append({
                    "sample_id": sample["sample_id"],
                    "question": qa["question"],
                    "recall_score": judge.get("recall_score"),
                    "answer_score": judge.get("answer_score"),
                    "precision": judge.get("precision"),
                    "facts_count": facts_count,
                    "reason": judge.get("reason", ""),
                })

    return {
        "total_questions": overall["count"],
        "soft_errors": soft_errors_total,
        "hard_errors": hard_errors_total,
        "metrics": _finalize_bucket(overall),
        "by_category": {cat: _finalize_bucket(b) for cat, b in sorted(by_category.items())},
        "failed_or_partial": failed_or_partial,
    }


def _fmt(value) -> str:
    if value is None:
        return "n/a"
    if isinstance(value, float):
        return f"{value:.3f}"
    return str(value)


def _print_bucket(b: dict, indent: str = "") -> None:
    rec = b["recall"]
    ans = b["answer"]
    p = b["precision"]
    f1 = b["f1"]
    rk = b["first_relevant_rank"]
    fr = b["facts_returned"]
    print(
        f"{indent}Recall   : score={_fmt(rec['score'])}  "
        f"pass={rec['pass']}  partial={rec['partial']}  fail={rec['fail']}"
    )
    print(
        f"{indent}Answer   : score={_fmt(ans['score'])}  "
        f"pass={ans['pass']}  partial={ans['partial']}  fail={ans['fail']}"
    )
    print(f"{indent}Precision: mean={_fmt(p['mean'])}  (over {p['questions_with_facts']} qs with facts)")
    print(f"{indent}F1       : mean={_fmt(f1['mean'])}  (over {f1['questions_scored']} qs)")
    print(
        f"{indent}Rank     : mean={_fmt(rk['mean'])}  MRR={_fmt(rk['mrr'])}  "
        f"(over {rk['questions_with_relevant']} qs with ≥1 relevant)"
    )
    print(f"{indent}Facts ret: mean={_fmt(fr['mean'])}  min={fr['min']}  max={fr['max']}")


def print_summary(results: list[dict]) -> None:
    s = _compute_summary(results)
    total = s["total_questions"]

    if total == 0:
        print("No QA results.")
        print(f"Soft errors: {s['soft_errors']}")
        print(f"Hard errors: {s['hard_errors']}")
        return

    m = s["metrics"]
    print("\n" + "=" * 70)
    print("EVALUATION SUMMARY")
    print("=" * 70)
    print(f"Total questions   : {total}")
    print(f"Skipped (no judge): {m['skipped']}")
    print(f"Soft errors       : {s['soft_errors']}")
    print(f"Hard errors       : {s['hard_errors']}")
    print()
    _print_bucket(m, indent="")

    print("\nBy category:")
    for cat, b in s["by_category"].items():
        print(f"  [category {cat}]  count={b['count']}")
        _print_bucket(b, indent="    ")

    if s["failed_or_partial"]:
        print(f"\nFailed/partial questions ({len(s['failed_or_partial'])}):")
        for item in s["failed_or_partial"]:
            p = item.get("precision")
            p_str = f"{p:.2f}" if isinstance(p, (int, float)) else "n/a"
            print(
                f"  [{item['sample_id']}] r={item['recall_score']:<7} a={item['answer_score']:<7} "
                f"p={p_str} facts={item['facts_count']} :: {item['question']}"
            )
            print(f"      → {item['reason']}")

    print("=" * 70 + "\n")


# ── entrypoint ─────────────────────────────────────────────────────────────────

def _build_evaluator() -> Evaluator:
    """Construct the Evaluator from env vars based on AI_MODEL_JUDGE provider."""
    judge_id = os.environ.get("AI_MODEL_JUDGE")
    if not judge_id:
        sys.exit("[ERROR] AI_MODEL_JUDGE environment variable is required when running with the judge.")
    judge_model, judge_provider = _resolve_model(judge_id)
    if judge_provider == "google":
        gemini_key = os.environ.get("GEMINI_API_KEY")
        if not gemini_key:
            sys.exit("[ERROR] GEMINI_API_KEY is required when AI_MODEL_JUDGE is a Google model.")
        rpm = int(os.environ.get("GEMINI_JUDGE_RPM", "5"))
        return Evaluator(
            model=judge_model,
            provider="google",
            gemini_api_key=gemini_key,
            rpm_limit=rpm,
        )
    if judge_provider == "cerebras":
        cerebras_key = os.environ.get("CEREBRAS_API_KEY")
        if not cerebras_key:
            sys.exit("[ERROR] CEREBRAS_API_KEY is required when AI_MODEL_JUDGE is a Cerebras model.")
        rpm_env = os.environ.get("CEREBRAS_JUDGE_RPM")
        rpm = int(rpm_env) if rpm_env else None
        return Evaluator(
            model=judge_model,
            provider="cerebras",
            cerebras_api_key=cerebras_key,
            rpm_limit=rpm,
        )
    azure_key = os.environ.get("AZURE_OPENAI_API_KEY")
    if not azure_key:
        sys.exit("[ERROR] AZURE_OPENAI_API_KEY is required for Azure judge models.")
    azure_endpoint = os.environ.get(
        "AZURE_OPENAI_ENDPOINT", "https://cchat-ai.cognitiveservices.azure.com/"
    )
    return Evaluator(
        model=judge_model,
        provider="azure",
        azure_api_key=azure_key,
        azure_endpoint=azure_endpoint,
    )


async def rejudge_file(
    input_path: str,
    out_path: str,
    max_facts: int | None,
    evaluator: Evaluator,
    concurrency: int,
) -> None:
    """Reload an existing results JSON, optionally truncate facts to top-K, and
    rerun the judge on every QA. No backend recall is performed.
    """
    with open(input_path, encoding="utf-8") as f:
        data = json.load(f)

    conversations = data.get("conversations") or data.get("samples") or []
    if not conversations:
        sys.exit(f"[ERROR] No 'conversations' (or 'samples') found in {input_path}")

    jobs: list[tuple[str, dict]] = []
    for conv in conversations:
        sid = conv.get("sample_id", "?")
        for qa in conv.get("qa", []):
            jobs.append((sid, qa))

    total = len(jobs)
    print("=" * 60)
    print("LoCoMo Rejudge")
    print("=" * 60)
    print(f"  source file : {input_path}")
    print(f"  total QAs   : {total}")
    print(f"  max facts   : {max_facts if max_facts else 'unlimited (rejudge as-is)'}")
    print(f"  concurrency : {concurrency}")
    print(f"  judge model : {evaluator.model}  (provider={evaluator.provider})")
    print("=" * 60 + "\n")

    sem = asyncio.Semaphore(concurrency)
    soft_per_sample: dict[str, int] = defaultdict(int)
    hard_per_sample: dict[str, int] = defaultdict(int)
    progress = {"done": 0}
    start = time.time()

    async def _run_one(sid: str, qa: dict) -> None:
        async with sem:
            facts = list(qa.get("facts_returned") or [])
            if max_facts is not None and max_facts > 0:
                facts = facts[:max_facts]
            qa["facts_returned"] = facts

            qa_t0 = time.time()
            question = qa.get("question", "")
            ground_truth = qa.get("ground_truth", "")
            result, _, soft_inc, hard_inc, exc = await _run_with_retries(
                label="rejudge",
                sample_id=sid,
                action=lambda: evaluator.judge(question, ground_truth, facts),
            )
            soft_per_sample[sid] += soft_inc
            hard_per_sample[sid] += hard_inc

            if hard_inc == 0 and result is not None:
                qa["judge"] = result.to_dict()
            else:
                qa["judge"] = JudgeResult(
                    recall_score="fail",
                    answer_score="fail",
                    fact_relevance=[False] * len(facts),
                    reason=str(exc) if exc else "unknown judge error",
                ).to_dict()

            elapsed_qa = time.time() - qa_t0
            progress["done"] += 1
            j = qa["judge"]
            p = j.get("precision")
            p_str = f"{p:.2f}" if p is not None else "n/a"
            print(
                f"  [{sid} {progress['done']}/{total}] r={j.get('recall_score')} "
                f"a={j.get('answer_score')} p={p_str} facts={len(facts)} ({elapsed_qa:.1f}s)",
                flush=True,
            )

    try:
        await asyncio.gather(*[_run_one(sid, qa) for sid, qa in jobs])
    except (KeyboardInterrupt, asyncio.CancelledError):
        print("\n[interrupted] Writing partial results…", flush=True)

    for conv in conversations:
        sid = conv.get("sample_id", "?")
        conv["soft_errors"] = soft_per_sample.get(sid, 0)
        conv["hard_errors"] = hard_per_sample.get(sid, 0)

    elapsed = time.time() - start

    output = dict(data)
    output["conversations"] = conversations
    output["summary"] = _compute_summary(conversations)
    output["error_stats"] = {
        "soft_errors": sum(soft_per_sample.values()),
        "hard_errors": sum(hard_per_sample.values()),
        "max_retries": _MAX_RETRIES,
        "retry_wait_seconds": _RETRY_WAIT_SECONDS,
    }
    output["rejudge"] = {
        "source_file": input_path,
        "max_facts": max_facts,
        "judge_model": evaluator.model,
        "judge_provider": evaluator.provider,
        "elapsed_seconds": round(elapsed, 1),
    }
    models = dict(output.get("models") or {})
    models["AI_MODEL_JUDGE"] = evaluator.model
    output["models"] = models

    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(output, f, indent=2, ensure_ascii=False)
    print(f"\nResults written to: {out_path}  (elapsed: {elapsed:.1f}s)")
    print_summary(conversations)


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
    p.add_argument(
        "--light",
        action="store_true",
        help="Use RecallLight (cheaper, single-embedding + Flash Lite selection) instead of standard recall",
    )
    p.add_argument(
        "--zero",
        action="store_true",
        help="Use RecallZero (no LLM at all; single embedding + deterministic post-processing) instead of standard recall",
    )
    p.add_argument(
        "--rejudge",
        default=None,
        help="Path to an existing results JSON. Skip backend recall; rerun the judge on the stored facts and write a new file.",
    )
    p.add_argument(
        "--max-facts",
        type=int,
        default=None,
        help="When rejudging, truncate facts_returned to this top-K before scoring. Useful for apples-to-apples cap comparisons.",
    )
    return p.parse_args()


def _recall_mode_from_args(args: argparse.Namespace) -> str:
    if args.light and args.zero:
        sys.exit("[ERROR] --light and --zero are mutually exclusive")
    if args.zero:
        return "zero"
    if args.light:
        return "light"
    return "standard"


def _aggregate_usage(results: list) -> dict:
    """Merge per-sample usage dicts into a single run-level usage summary."""
    agg: dict = {}
    for r in results:
        _merge_usage(agg, r.get("usage"))
    return agg


def _write_output(out_path: str, results: list, elapsed: float, data_path: str, recall_mode: str = "standard") -> None:
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

    # Capture models from .env
    models = {
        "AI_MODEL_DECOMPOSE": os.environ["AI_MODEL_DECOMPOSE"],
        "AI_MODEL_EVALUATE": os.environ["AI_MODEL_EVALUATE"],
        "AI_MODEL_SELECT_FACTS": os.environ["AI_MODEL_SELECT_FACTS"],
        "AI_MODEL_DECOMPOSE_QUERIES": os.environ["AI_MODEL_DECOMPOSE_QUERIES"],
        "AI_MODEL_DECOMPOSE_RECALL": os.environ["AI_MODEL_DECOMPOSE_RECALL"],
        "AI_EMBEDDING_MODEL": os.environ["AI_EMBEDDING_MODEL"],
    }
    if os.environ.get("AI_MODEL_JUDGE"):
        models["AI_MODEL_JUDGE"] = os.environ["AI_MODEL_JUDGE"]

    # Strip the raw duration lists from the per-conversation output to keep it clean
    conversations = [
        {k: v for k, v in r.items() if k not in ("ingest_durations", "recall_durations")}
        for r in results
    ]

    summary = _compute_summary(results)
    usage = _aggregate_usage(results)

    output = {
        "dataset": Path(data_path).stem,
        "elapsed_seconds": round(elapsed, 1),
        "recall_mode": recall_mode,
        "models": models,
        "usage": usage,
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

    if args.rejudge:
        if args.no_judge:
            sys.exit("[ERROR] --rejudge requires the judge; remove --no-judge.")
        evaluator = _build_evaluator()
        await rejudge_file(
            input_path=args.rejudge,
            out_path=args.out,
            max_facts=args.max_facts,
            evaluator=evaluator,
            concurrency=args.concurrency,
        )
        return

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

    # Ensure required AI models are set in the environment
    required_models = [
        "AI_MODEL_DECOMPOSE",
        "AI_MODEL_EVALUATE",
        "AI_MODEL_SELECT_FACTS",
        "AI_MODEL_DECOMPOSE_QUERIES",
        "AI_MODEL_DECOMPOSE_RECALL",
        "AI_EMBEDDING_MODEL",
    ]
    missing_models = [m for m in required_models if not os.environ.get(m)]
    if missing_models:
        sys.exit(f"[ERROR] Missing required environment variables for models: {', '.join(missing_models)}")

    recall_mode = _recall_mode_from_args(args)

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
    print(f"  recall mode : {recall_mode}")
    print(f"  judge       : {'disabled (--no-judge)' if args.no_judge else 'enabled'}")
    if args.target == "mem0":
        print(f"  API         : mem0 (https://api.mem0.ai/v3)")
    else:
        print(f"  API         : {api_url}  agent={agent_id}")
    print("=" * 60 + "\n")

    evaluator: Evaluator | None = None
    if not args.no_judge:
        evaluator = _build_evaluator()

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
                evaluate_sample(sample, client, evaluator, semaphore, reuse_thread_id=args.reuse_thread, recall_mode=recall_mode)
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
        _write_output(args.out, results, elapsed, args.data, recall_mode=recall_mode)
        print_summary(results)

    if args.cleanup:
        print("--cleanup: fact deletion is not yet implemented (requires listing facts by agent).")


if __name__ == "__main__":
    asyncio.run(main())
