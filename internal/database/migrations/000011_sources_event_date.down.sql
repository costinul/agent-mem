DROP INDEX IF EXISTS idx_sources_event_date;
ALTER TABLE sources DROP COLUMN IF EXISTS event_date;
