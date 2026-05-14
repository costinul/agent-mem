CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email      TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL DEFAULT '',
    picture    TEXT NOT NULL DEFAULT '',
    google_sub TEXT NOT NULL UNIQUE,
    role       TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_users_email ON users (email);

CREATE TABLE sessions (
    token_hash  TEXT PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_user_id ON sessions (user_id);
CREATE INDEX idx_sessions_expires_at ON sessions (expires_at);

CREATE TABLE api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    prefix TEXT NOT NULL,
    key_hash TEXT NOT NULL,
    valid BOOLEAN NOT NULL DEFAULT true,
    label TEXT,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    debug BOOLEAN NOT NULL DEFAULT false
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

CREATE TABLE threads (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_threads_account_id ON threads(account_id);
CREATE INDEX idx_threads_agent_id ON threads(agent_id);

CREATE TABLE events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    thread_id UUID REFERENCES threads(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_events_account_id ON events(account_id);
CREATE INDEX idx_events_thread_id ON events(thread_id);

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
    author TEXT,
    event_date TIMESTAMPTZ NOT NULL,
    CHECK (
        (content IS NOT NULL AND bucket_path IS NULL) OR
        (content IS NULL AND bucket_path IS NOT NULL)
    )
);
CREATE INDEX idx_sources_event_id ON sources(event_id);
CREATE INDEX idx_sources_event_date ON sources(event_date);

CREATE TABLE facts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    agent_id UUID REFERENCES agents(id) ON DELETE CASCADE,
    thread_id UUID REFERENCES threads(id) ON DELETE CASCADE,
    source_id UUID NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('KNOWLEDGE','RULE','PREFERENCE')),
    text TEXT NOT NULL,
    embedding vector(768),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at TIMESTAMPTZ,
    superseded_by UUID REFERENCES facts(id) ON DELETE SET NULL,
    referenced_at TIMESTAMPTZ,
    text_search tsvector GENERATED ALWAYS AS (to_tsvector('english', text)) STORED,
    entities text[] NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_facts_account_id ON facts(account_id);
CREATE INDEX idx_facts_thread_id ON facts(thread_id);
CREATE INDEX idx_facts_source_id ON facts(source_id);
CREATE INDEX idx_facts_superseded_at ON facts(superseded_at);
CREATE INDEX idx_facts_referenced_at ON facts(referenced_at) WHERE referenced_at IS NOT NULL;
CREATE INDEX idx_facts_text_search ON facts USING GIN (text_search);
CREATE INDEX idx_facts_entities ON facts USING GIN (entities);