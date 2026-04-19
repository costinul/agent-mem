# Memory Evaluation Framework

Tests the agent-mem memory API against real conversational datasets and scores how well the memory system retains and retrieves information.

## Structure

```
eval/
  requirements.txt       # Python dependencies
  shared/
    api_client.py        # Async HTTP wrapper for the memory API (Bearer auth)
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

## Bootstrap a test account, API key, and agent

The API requires a Bearer token. Create a dedicated test account + key + agent
once, then reuse them across runs.

1. Start the API server (`go run ./cmd/api`).
2. Create an account and API key:

```bash
go run ./cmd/api create-api-key --account-name eval-test
# prints: account_id=...  api_key=amk_...
```

3. Create an agent under that account (replace `$KEY`):

```bash
curl -s -X POST http://localhost:8080/agents \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"locomo-eval"}'
# prints JSON containing "id": "<agent-id>"
```

## Environment Variables

Add these variables to the `.env` file at the root of the repository:

| Variable | Description | Required |
|---|---|---|
| `MEMORY_API_URL` | Base URL of a running API instance | No (default: `http://localhost:8080`) |
| `MEMORY_API_KEY` | API key from `create-api-key` (Bearer token) | Yes |
| `MEMORY_AGENT_ID` | Agent ID created via `POST /agents` | Yes |
| `AZURE_OPENAI_API_KEY` | Azure OpenAI key for the LLM judge | Yes |

> Always use a dedicated test account/agent so you can cleanly inspect or delete results afterward.

## Running a Dataset

See each dataset's own `README.md` for details:

- [LoCoMo](locomo/README.md) — 10 long conversations, QA benchmark
