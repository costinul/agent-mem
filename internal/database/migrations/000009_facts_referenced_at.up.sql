ALTER TABLE facts ADD COLUMN referenced_at TIMESTAMPTZ;
CREATE INDEX idx_facts_referenced_at ON facts(referenced_at) WHERE referenced_at IS NOT NULL;
