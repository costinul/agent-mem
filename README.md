# agent-mem

Minimal API service for memory operations with account and API-key management.

## CLI: create-api-key

The binary supports an ad hoc command to mint API keys:

`go run ./cmd/api create-api-key [flags]`

### Required environment

- `POSTGRES_DSN` must be set (same as normal server startup).

### Flags

- `--account-id <uuid>`: create a key for an existing account.
- `--account-name "<name>"`: create a new account and then create a key.
- `--label "<text>"`: optional API key label.
- `--expires-at "<RFC3339>"`: optional expiration timestamp.

Use either `--account-id` or `--account-name`.

### Examples

Create key for an existing account:

`go run ./cmd/api create-api-key --account-id 11111111-1111-1111-1111-111111111111 --label "local-dev"`

Create account and key in one command:

`go run ./cmd/api create-api-key --account-name "my-account" --expires-at 2026-12-31T23:59:59Z`

### Output

The command prints:

- `account_id`
- `api_key_id`
- `api_key_prefix`
- `api_key` (plaintext, shown once)
