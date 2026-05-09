DROP INDEX IF EXISTS idx_facts_entities;
ALTER TABLE facts DROP COLUMN IF EXISTS entities;

DROP INDEX IF EXISTS idx_facts_text_search;
ALTER TABLE facts DROP COLUMN IF EXISTS text_search;
