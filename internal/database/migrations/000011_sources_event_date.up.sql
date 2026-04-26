ALTER TABLE sources ADD COLUMN event_date TIMESTAMPTZ;
UPDATE sources SET event_date = created_at WHERE event_date IS NULL;
ALTER TABLE sources ALTER COLUMN event_date SET NOT NULL;
CREATE INDEX idx_sources_event_date ON sources(event_date);
