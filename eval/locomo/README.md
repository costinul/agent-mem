# LoCoMo Evaluation

Evaluates the memory API against the [LoCoMo](https://github.com/snap-research/locomo) dataset — 10 very long-term conversations with annotated QA pairs.

## How It Works

1. Creates one thread per conversation and ingests all turns **sequentially** into that thread.
2. For each QA pair, queries memory with the question and collects the returned facts.
3. Sends `(question, ground_truth, returned facts)` to GPT-4o-mini to score as `pass / partial / fail`.
4. Writes `results.json` and prints a summary.

## Dataset

The dataset file `locomo10.json` is **auto-downloaded** from GitHub on first run.
If you want to pre-download it manually:

```bash
curl -o data/locomo10.json \
  https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json
```

## Run

See [eval/README.md](../README.md) for one-time setup (create account, API key,
and agent).

1. Ensure your `.env` file at the root of the repository contains the required variables:
   ```env
   MEMORY_API_KEY=amk_...
   MEMORY_AGENT_ID=<your-test-agent-id>
   AZURE_OPENAI_API_KEY=sk-...
   MEMORY_API_URL=http://localhost:8080
   ```

2. Run the evaluation script:
   ```bash
   cd eval
   python locomo/run.py
   ```

## Options

| Flag | Default | Description |
|---|---|---|
| `--concurrency N` | `3` | Max conversations processed in parallel |
| `--limit N` | all | Evaluate only first N conversations (useful for quick smoke tests) |
| `--out FILE` | `results.json` | Output file path |
| `--data FILE` | `locomo/data/locomo10.json` | Path to dataset file |
| `--cleanup` | off | Print a reminder to delete ingested facts after the run |

### Quick smoke test (1 conversation)

```bash
python locomo/run.py --limit 1 --out smoke_results.json
```

## Debug in VS Code (with breakpoints)

A debug configuration is provided in [.vscode/launch.json](../../.vscode/launch.json):
**Eval: LoCoMo (1 conversation)**.

1. Install the **Python** extension and `pip install debugpy` (usually already
   bundled with the extension).
2. Ensure your `.env` file at the root of the repository contains `MEMORY_API_KEY`,
   `MEMORY_AGENT_ID`, and `AZURE_OPENAI_API_KEY`. The launch config automatically
   loads these variables using `"envFile": "${workspaceFolder}/.env"`.
3. Set breakpoints anywhere in `locomo/run.py`, `shared/api_client.py`, or
   `shared/evaluator.py`.
4. Open the **Run and Debug** panel and start **Eval: LoCoMo (1 conversation)**.
   Execution will pause at your breakpoints; you can inspect `facts`,
   `result.score`, `qa_results`, etc. in the Variables panel.

The launch config runs with `--limit 1 --out smoke_results.json` and
`justMyCode: false` so you can also step into `httpx` / `openai` if needed.

## Output

`results.json` contains per-question detail:

```json
{
  "dataset": "locomo",
  "elapsed_seconds": 120.4,
  "conversations": [
    {
      "sample_id": "1",
      "thread_id": "55555555-5555-5555-5555-555555555555",
      "qa": [
        {
          "question": "What did Angela buy on her birthday?",
          "ground_truth": "A red scarf",
          "category": "single-hop",
          "facts_returned": ["Angela bought a red scarf for her birthday on March 3rd."],
          "score": "pass",
          "reason": "The returned fact directly states the answer."
        }
      ]
    }
  ]
}
```

Terminal summary example:

```
==================================================
EVALUATION SUMMARY
==================================================
Total questions : 120
  pass          : 84  (70%)
  partial       : 18  (15%)
  fail          : 18  (15%)

By category:
  multi-hop                      pass=12/20 (60%)
  single-hop                     pass=48/60 (80%)
  temporal                       pass=24/40 (60%)
==================================================
```
