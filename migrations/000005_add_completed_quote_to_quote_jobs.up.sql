ALTER TABLE quote_jobs
    ADD COLUMN price            NUMERIC,
    ADD COLUMN quote_updated_at TIMESTAMPTZ,
    ADD CONSTRAINT done_has_quote CHECK (
        (status = 'done') = (price IS NOT NULL AND quote_updated_at IS NOT NULL)
    );
