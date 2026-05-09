ALTER TABLE facts ADD COLUMN text_search tsvector
  GENERATED ALWAYS AS (to_tsvector('english', text)) STORED;
CREATE INDEX idx_facts_text_search ON facts USING GIN (text_search);

ALTER TABLE facts ADD COLUMN entities text[] NOT NULL DEFAULT '{}';
CREATE INDEX idx_facts_entities ON facts USING GIN (entities);
