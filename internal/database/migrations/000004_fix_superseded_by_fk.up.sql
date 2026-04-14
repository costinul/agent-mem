ALTER TABLE facts
    DROP CONSTRAINT facts_superseded_by_fkey,
    ADD CONSTRAINT facts_superseded_by_fkey
        FOREIGN KEY (superseded_by) REFERENCES facts(id) ON DELETE SET NULL;
