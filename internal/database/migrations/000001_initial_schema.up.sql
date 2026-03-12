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

CREATE TABLE agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_agents_account_id ON agents(account_id);

CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at TIMESTAMPTZ
);
CREATE INDEX idx_sessions_account_id ON sessions(account_id);
CREATE INDEX idx_sessions_agent_id ON sessions(agent_id);

CREATE TABLE events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    session_id UUID REFERENCES sessions(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_events_account_id ON events(account_id);
CREATE INDEX idx_events_session_id ON events(session_id);

-- Exactly one of content or bucket_path must be non-null.
CREATE TABLE sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('SYSTEM','USER','AGENT','TOOL','DOCUMENT','CODE')),
    content TEXT,
    content_type TEXT NOT NULL DEFAULT 'text/plain',
    bucket_path TEXT,
    size_bytes BIGINT CHECK (size_bytes IS NULL OR size_bytes >= 0),
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (content IS NOT NULL AND bucket_path IS NULL) OR
        (content IS NULL AND bucket_path IS NOT NULL)
    )
);
CREATE INDEX idx_sources_event_id ON sources(event_id);

CREATE TABLE facts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    agent_id UUID REFERENCES agents(id) ON DELETE CASCADE,
    session_id UUID REFERENCES sessions(id) ON DELETE SET NULL,
    source_id UUID NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('KNOWLEDGE','RULE','PREFERENCE')),
    text TEXT NOT NULL,
    embedding vector(1536),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_facts_account_id ON facts(account_id);
CREATE INDEX idx_facts_session_id ON facts(session_id);
CREATE INDEX idx_facts_source_id ON facts(source_id);
