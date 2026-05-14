# Contextual File Eval

Tests large-file contextual memory: 10 synthetic company documents with updates,
deletions, and additions spread across files. Designed to stress chunking and
overlapping, and to validate whether the system correctly tracks fact changes
over time.

## Setup

Generate the test data first:

```bash
cd eval/contextual_files
python generate.py
```

This writes `data/file_01.txt` through `data/file_10.txt` and `data/validation.json`.

## Run

```bash
python run.py                                # ingest all files, run 25 QA pairs, judge
python run.py --no-judge                     # skip LLM judge, just record returned facts
python run.py --reuse-thread <thread_id>     # skip re-ingest, rerun QA only
python run.py --light                        # use RecallLight instead of standard
python run.py --zero                         # use RecallZero (no LLM)
python run.py --out my_results.json          # custom output path
```

Output: `results_contextual.json`

## Environment variables

Same `.env` as the locomo eval (loaded from repo root):

```
MEMORY_API_URL      (default: http://localhost:8080)
MEMORY_API_KEY      required
MEMORY_AGENT_ID     required
AI_MODEL_JUDGE      required unless --no-judge
```

## What it tests

| Category    | Count | Description                                           |
|-------------|-------|-------------------------------------------------------|
| `retrieval` | 8     | Simple fact lookup from a single file                 |
| `update`    | 6     | Fact that changed across files — latest should win    |
| `deletion`  | 3     | Fact negated or removed in a later file               |
| `addition`  | 2     | Fact that only appears in a later file                |
| `multi_hop` | 4     | Requires combining facts from 2+ files                |
| `conflict`  | 2     | Two competing facts — latest file should override     |

## Document map

| File         | Date       | Key tracked event                                    |
|--------------|------------|------------------------------------------------------|
| file_01.txt  | 2025-01-15 | Baseline: team directory, Project Atlas, leave policy |
| file_02.txt  | 2025-02-21 | Atlas milestone 1 done; Bob assigned as tech lead    |
| file_03.txt  | 2025-03-01 | Remote work policy v1: 2 days/week                   |
| file_04.txt  | 2025-04-02 | Bob promoted: Senior Developer → Lead Engineer       |
| file_05.txt  | 2025-04-28 | Project Nova announced: £180k, Diana Ross lead, Q1 2026 |
| file_06.txt  | 2025-06-01 | Remote work policy v2: updated to 3 days/week        |
| file_07.txt  | 2025-07-14 | Alice Chen leaves; Bob becomes acting Eng Manager    |
| file_08.txt  | 2025-08-20 | Project Atlas cancelled; resources to Nova           |
| file_09.txt  | 2025-09-08 | Diana Ross → VP of Product; Nova budget → £250k      |
| file_10.txt  | 2025-10-01 | Sarah Kim joins; Nova deadline → Q3 2026; Bob confirmed Eng Manager |
