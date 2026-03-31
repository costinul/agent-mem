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

```bash
# From the eval/ directory:
cd eval

# Required environment variables
export MEMORY_ACCOUNT_ID=<your-test-account-id>
export MEMORY_AGENT_ID=<your-test-agent-id>
export OPENAI_API_KEY=<your-openai-key>

# Optional
export MEMORY_API_URL=http://localhost:8080   # default

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
