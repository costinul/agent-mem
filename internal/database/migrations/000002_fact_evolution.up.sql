ALTER TABLE facts
  ADD COLUMN superseded_at TIMESTAMPTZ,
  ADD COLUMN superseded_by UUID REFERENCES facts(id);

CREATE INDEX idx_facts_superseded_at ON facts(superseded_at);
