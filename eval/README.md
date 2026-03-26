# Memory Evaluation Framework

Tests the agent-mem memory API against real conversational datasets and scores how well the memory system retains and retrieves information.

## Structure

```
eval/
  requirements.txt       # Python dependencies
  shared/
    api_client.py        # Async HTTP wrapper for the memory API
    evaluator.py         # LLM-as-judge (gpt-4o-mini)
  locomo/
    run.py               # LoCoMo dataset runner
    data/                # Put locomo10.json here (git-ignored)
```

## Setup

```bash
cd eval
pip install -r requirements.txt
```

## Environment Variables

| Variable | Description | Required |
|---|---|---|
| `MEMORY_API_URL` | Base URL of a running API instance | No (default: `http://localhost:8080`) |
| `MEMORY_ACCOUNT_ID` | Dedicated test account ID | Yes |
| `MEMORY_AGENT_ID` | Dedicated test agent ID | Yes |
| `OPENAI_API_KEY` | OpenAI key (same as launch.json) | Yes |

> Always use a dedicated test account/agent so you can cleanly inspect or delete results afterward.

## Running a Dataset

See each dataset's own `README.md` for details:

- [LoCoMo](locomo/README.md) — 10 long conversations, QA benchmark
