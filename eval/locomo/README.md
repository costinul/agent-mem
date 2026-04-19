# LoCoMo Evaluation

Tests memory recall against long conversations. Run them in order, smallest first.

## Runs (small → full)

| # | Command | Time | Threads | Turns | QAs |
|---|---|---|---|---|---|
| 1 | `python locomo/run.py --data locomo/data/warmup_small.json --no-judge` | ~10s | 1 | 6 | 3 (no judge) |
| 2 | `python locomo/run.py --data locomo/data/warmup_small.json` | ~1 min | 1 | 6 | 3 |
| 3 | `python locomo/run.py --data locomo/data/warmup_medium.json` | ~5 min | 1 | 48 | 10 |
| 4 | `python locomo/run.py --limit 1` | ~15 min | 1 | ~600 | ~30 |
| 5 | `python locomo/run.py` | 30–60 min | 10 | ~6000 | ~300 |

Step 1 = smoke test, no LLM judge tokens spent.
Step 5 = full real LoCoMo benchmark.

Same three runs are also available as VS Code debug configs:
**Eval: LoCoMo (warmup small)**, **Eval: LoCoMo (warmup medium)**, **Eval: LoCoMo (1 conversation)**.

## All flags

| Flag | Default | What it does |
|---|---|---|
| `--data FILE` | `locomo/data/locomo10.json` | Dataset to load |
| `--limit N` | all | Only run the first N conversations |
| `--concurrency N` | `1` | How many conversations to process in parallel |
| `--no-judge` | off | Skip the LLM judge; record recalled facts only |
| `--out FILE` | `results.json` | Where to write per-question results |
| `--cleanup` | off | _(not implemented)_ Delete ingested facts after the run |

Press **Ctrl+C** any time — partial results are written to `--out` before exiting.

## Setup (one-time)

1. `.env` at the repo root must contain:
   ```env
   MEMORY_API_URL=http://localhost:8080
   MEMORY_API_KEY=amk_...
   MEMORY_AGENT_ID=<your-test-agent-id>
   AZURE_OPENAI_API_KEY=sk-...
   ```
2. Start the Go API server.
3. `cd eval && pip install -r requirements.txt`

See [eval/README.md](../README.md) for how to create the test account/key/agent.

## Datasets

- `locomo/data/warmup_small.json` — synthetic, committed to repo.
- `locomo/data/warmup_medium.json` — synthetic, committed to repo.
- `locomo/data/locomo10.json` — real LoCoMo, **auto-downloaded** on first run.

## Output

`results.json` contains the per-question score (`pass` / `partial` / `fail` / `skipped`), the recalled facts, and the judge's reasoning. A summary table is also printed to the terminal at the end.
