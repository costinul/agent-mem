DROP INDEX IF EXISTS idx_facts_superseded_at;

ALTER TABLE facts
  DROP COLUMN IF EXISTS superseded_by,
  DROP COLUMN IF EXISTS superseded_at;
