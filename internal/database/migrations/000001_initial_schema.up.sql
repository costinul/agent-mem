CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    prefix TEXT NOT NULL,
    key_hash TEXT NOT NULL,
    valid BOOLEAN NOT NULL DEFAULT true,
    label TEXT,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_api_keys_prefix ON api_keys(prefix);
CREATE INDEX idx_api_keys_account_id ON api_keys(account_id);

CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at TIMESTAMPTZ
);
CREATE INDEX idx_sessions_account_id ON sessions(account_id);

CREATE TABLE facts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    session_id UUID REFERENCES sessions(id) ON DELETE SET NULL,
    event_id UUID NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('SYSTEM','USER','AGENT','TOOL','DOCUMENT','CODE')),
    kind TEXT NOT NULL CHECK (kind IN ('KNOWLEDGE','RULE','PREFERENCE')),
    text TEXT NOT NULL,
    embedding vector(1536),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_facts_account_id ON facts(account_id);
CREATE INDEX idx_facts_session_id ON facts(session_id);
CREATE INDEX idx_facts_event_id ON facts(event_id);

CREATE TABLE fact_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fact_id UUID NOT NULL REFERENCES facts(id) ON DELETE CASCADE,
    event_id UUID NOT NULL,
    input_hash TEXT NOT NULL,
    bucket_path TEXT
);
CREATE INDEX idx_fact_links_fact_id ON fact_links(fact_id);

CREATE TABLE raw_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    event_id UUID NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('SYSTEM','USER','AGENT','TOOL','DOCUMENT','CODE')),
    content TEXT NOT NULL,
    sequence INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_raw_messages_session_id ON raw_messages(session_id);
CREATE INDEX idx_raw_messages_session_sequence ON raw_messages(session_id, sequence);
