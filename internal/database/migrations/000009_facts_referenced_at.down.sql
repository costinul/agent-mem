DROP INDEX IF EXISTS idx_facts_referenced_at;
ALTER TABLE facts DROP COLUMN IF EXISTS referenced_at;
