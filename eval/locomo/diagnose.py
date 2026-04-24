"""
Diagnose recall misses for a completed LoCoMo run.

For each failed/partial question this script:
  1. Searches the locally-dumped facts file for keyword matches (proxy for
     "is the fact stored?") — Stage A miss if nothing matches.
  2. Calls POST /memory/recall and reports how many facts came back — Stage C/E
     miss if store has hits but recall returns empty.
  3. Parses a server log file (optional) to extract per-query stage counts
     (phrases, retrieved, expanded, selected) logged by the instrumented
     Recall() function.
  4. Prints a classification table:
       MISS_A  — fact never extracted/stored (ingestion problem)
       MISS_C  — fact stored but not in top-25 vector candidates (embedding/search problem)
       MISS_E  — fact retrieved but SelectFacts dropped it (LLM filter too aggressive)
       OK      — recall returned ≥1 fact

Usage (from eval/ directory):
    # First dump facts:
    python locomo/diagnose.py --dump-facts
    # Then run diagnosis:
    python locomo/diagnose.py [--log SERVER_LOG] [--results results.json]
    # Or do both at once:
    python locomo/diagnose.py --dump-facts --log server.log

Environment variables (same as run.py):
    MEMORY_API_URL, MEMORY_API_KEY, MEMORY_AGENT_ID
"""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import re
import sys
import time
from collections import defaultdict
from pathlib import Path

import httpx
from dotenv import load_dotenv

load_dotenv(Path(__file__).resolve().parent.parent.parent / ".env")

THREAD_ID = "8fa46be9-eb4f-475d-b088-ea3d68c746da"  # conv-26 thread, set by default
FACTS_DUMP = Path(__file__).parent / "data" / "facts_dump.json"
RESULTS_DEFAULT = Path(__file__).parent.parent / "results.json"


# ── API helpers ────────────────────────────────────────────────────────────────

def _headers(api_key: str) -> dict:
    return {"Authorization": f"Bearer {api_key}"}


async def dump_facts(api_url: str, api_key: str, agent_id: str, thread_id: str) -> list[dict]:
    url = f"{api_url}/facts?agent_id={agent_id}&thread_id={thread_id}&limit=5000"
    async with httpx.AsyncClient(timeout=60) as client:
        resp = await client.get(url, headers=_headers(api_key))
        resp.raise_for_status()
        data = resp.json()
        facts = data.get("facts", [])
        print(f"Dumped {len(facts)} facts (total reported: {data.get('total')})", flush=True)
        FACTS_DUMP.parent.mkdir(parents=True, exist_ok=True)
        FACTS_DUMP.write_text(json.dumps(facts, indent=2, ensure_ascii=False))
        print(f"Written to {FACTS_DUMP}", flush=True)
        return facts


async def recall(api_url: str, api_key: str, agent_id: str, thread_id: str, question: str) -> list[str]:
    async with httpx.AsyncClient(timeout=60) as client:
        resp = await client.post(
            f"{api_url}/memory/recall",
            headers=_headers(api_key),
            json={"thread_id": thread_id, "agent_id": agent_id, "query": question},
        )
        resp.raise_for_status()
        return [f["text"] for f in resp.json().get("facts", [])]


# ── Log parsing ────────────────────────────────────────────────────────────────

def parse_server_log(log_path: str) -> dict[str, dict]:
    """
    Parse the Go server log for lines emitted by the instrumented Recall().
    Returns a dict keyed by the (truncated) query string with stage counts.

    Expected log format:
        recall q="..." phrases=[...]
        recall retrieved=N top_texts=[...]
        recall expanded=N
        recall selected=N ids=[...]
    """
    stages: dict[str, dict] = {}
    current_q = None

    q_re = re.compile(r'recall q="([^"]*)" phrases=(\[.*\])')
    ret_re = re.compile(r'recall retrieved=(\d+)')
    exp_re = re.compile(r'recall expanded=(\d+)')
    sel_re = re.compile(r'recall selected=(\d+)')

    with open(log_path, encoding="utf-8", errors="replace") as fh:
        for line in fh:
            m = q_re.search(line)
            if m:
                current_q = m.group(1)
                stages[current_q] = {"phrases": m.group(2), "retrieved": None, "expanded": None, "selected": None}
                continue
            if current_q is None:
                continue
            m = ret_re.search(line)
            if m:
                stages[current_q]["retrieved"] = int(m.group(1))
                continue
            m = exp_re.search(line)
            if m:
                stages[current_q]["expanded"] = int(m.group(1))
                continue
            m = sel_re.search(line)
            if m:
                stages[current_q]["selected"] = int(m.group(1))

    return stages


# ── Fact store search ──────────────────────────────────────────────────────────

def keyword_match(question: str, ground_truth: str, facts: list[dict]) -> list[str]:
    """
    Return texts of facts whose text contains any keyword from the ground-truth
    or key nouns from the question. Conservative proxy for "is it stored?".
    """
    # pull candidate keywords: all alpha tokens ≥4 chars from question+truth
    combined = (question + " " + ground_truth).lower()
    keywords = {w for w in re.findall(r"[a-z']{4,}", combined) if w not in _STOPWORDS}
    matches = []
    for f in facts:
        text_lower = f["text"].lower()
        if any(kw in text_lower for kw in keywords):
            matches.append(f["text"])
    return matches


_STOPWORDS = {
    "what", "when", "where", "have", "that", "this", "with", "from", "they",
    "been", "were", "also", "their", "about", "some", "many", "much",
    "would", "could", "should", "does", "once", "time", "last", "year",
    "month", "week", "into", "onto", "over", "after", "before", "which",
    "likely", "often", "then", "long",
}


# ── Main diagnosis ─────────────────────────────────────────────────────────────

async def diagnose(
    results_path: str,
    api_url: str,
    api_key: str,
    agent_id: str,
    thread_id: str,
    log_path: str | None,
    only_zero: bool,
) -> None:
    # Load facts dump
    if not FACTS_DUMP.exists():
        print(f"Facts dump not found at {FACTS_DUMP}. Run with --dump-facts first.", file=sys.stderr)
        sys.exit(1)
    stored_facts: list[dict] = json.loads(FACTS_DUMP.read_text())
    print(f"Loaded {len(stored_facts)} stored facts from dump.\n", flush=True)

    # Load results
    results = json.loads(Path(results_path).read_text())
    questions = []
    for conv in results.get("conversations", []):
        for qa in conv.get("qa", []):
            if only_zero:
                if qa.get("facts_returned") == [] and qa.get("score") == "fail":
                    questions.append(qa)
            else:
                if qa.get("score") in ("fail", "partial"):
                    questions.append(qa)
    print(f"Diagnosing {len(questions)} questions (only_zero={only_zero}).\n", flush=True)

    # Optional: parse server log
    log_stages: dict[str, dict] = {}
    if log_path:
        log_stages = parse_server_log(log_path)
        print(f"Parsed {len(log_stages)} recall entries from server log.\n", flush=True)

    # Run recalls
    rows = []
    for i, qa in enumerate(questions, 1):
        q = qa["question"]
        gt = qa.get("ground_truth", "")
        print(f"  [{i}/{len(questions)}] {q[:70]}…", flush=True)
        t0 = time.time()
        recalled = await recall(api_url, api_key, agent_id, thread_id, q)
        elapsed = time.time() - t0

        stored_hits = keyword_match(q, gt, stored_facts)

        # Determine stage miss
        if log_stages:
            entry = log_stages.get(q, {})
            retrieved = entry.get("retrieved") or 0
            expanded = entry.get("expanded") or retrieved
            selected = entry.get("selected") or len(recalled)
        else:
            retrieved = -1  # unknown without log
            expanded = -1
            selected = len(recalled)

        if len(recalled) > 0:
            miss_type = "OK"
        elif not stored_hits:
            miss_type = "MISS_A"  # ingestion miss
        elif retrieved == 0 or (retrieved == -1 and len(recalled) == 0 and stored_hits):
            # Store has it but we can't tell if retrieval or selection is the culprit
            # without the log; default to unknown, or C if retrieved==0
            miss_type = "MISS_C" if retrieved == 0 else "MISS_C/E"
        else:
            miss_type = "MISS_E"  # retrieved>0 but selected==0

        rows.append({
            "q": q,
            "gt": gt,
            "miss": miss_type,
            "stored": len(stored_hits),
            "retrieved": retrieved,
            "expanded": expanded,
            "selected": selected,
            "recalled": len(recalled),
            "elapsed": elapsed,
        })
        print(f"    => {miss_type}  stored_hits={len(stored_hits)}  retrieved={retrieved}  selected={selected}  recalled={len(recalled)}  ({elapsed:.1f}s)", flush=True)

    # Summary table
    print("\n" + "=" * 100)
    print("DIAGNOSIS TABLE")
    print("=" * 100)
    print(f"{'Question':<55} {'Miss':<10} {'Stored':>6} {'Retr':>5} {'Exp':>5} {'Sel':>5} {'Recl':>5}")
    print("-" * 100)
    for r in rows:
        q_trunc = r["q"][:54]
        retr = str(r["retrieved"]) if r["retrieved"] >= 0 else "?"
        exp = str(r["expanded"]) if r["expanded"] >= 0 else "?"
        print(f"{q_trunc:<55} {r['miss']:<10} {r['stored']:>6} {retr:>5} {exp:>5} {r['selected']:>5} {r['recalled']:>5}")

    print("\nSUMMARY")
    by_miss: dict[str, int] = defaultdict(int)
    for r in rows:
        by_miss[r["miss"]] += 1
    for k, v in sorted(by_miss.items()):
        pct = 100 * v // len(rows)
        print(f"  {k:<12}: {v}  ({pct}%)")

    print(f"\nTotal diagnosed: {len(rows)}")
    print("=" * 100)


# ── Entrypoint ─────────────────────────────────────────────────────────────────

def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Diagnose LoCoMo recall misses")
    p.add_argument("--results", default=str(RESULTS_DEFAULT), help="Path to results.json")
    p.add_argument("--log", default=None, help="Path to Go server log file (optional)")
    p.add_argument("--dump-facts", action="store_true", help="Dump all facts from the API first")
    p.add_argument("--thread-id", default=THREAD_ID, help="Thread ID to diagnose")
    p.add_argument("--all", dest="all_qs", action="store_true", help="Include partial scores, not just zero-retrieval fails")
    return p.parse_args()


async def main() -> None:
    args = parse_args()
    api_url = os.environ.get("MEMORY_API_URL", "http://localhost:8080")
    api_key = os.environ["MEMORY_API_KEY"]
    agent_id = os.environ["MEMORY_AGENT_ID"]

    if args.dump_facts:
        await dump_facts(api_url, api_key, agent_id, args.thread_id)

    await diagnose(
        results_path=args.results,
        api_url=api_url,
        api_key=api_key,
        agent_id=agent_id,
        thread_id=args.thread_id,
        log_path=args.log,
        only_zero=not args.all_qs,
    )


if __name__ == "__main__":
    asyncio.run(main())
