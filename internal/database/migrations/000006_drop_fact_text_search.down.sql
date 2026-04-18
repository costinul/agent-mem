ALTER TABLE facts ADD COLUMN text_search tsvector
  GENERATED ALWAYS AS (to_tsvector('english', text)) STORED;

CREATE INDEX idx_facts_text_search ON facts USING GIN (text_search);
