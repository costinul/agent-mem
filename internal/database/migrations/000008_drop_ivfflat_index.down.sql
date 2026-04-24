CREATE INDEX IF NOT EXISTS idx_facts_embedding_cosine
ON facts
USING ivfflat (embedding vector_cosine_ops)
WITH (lists = 100);
