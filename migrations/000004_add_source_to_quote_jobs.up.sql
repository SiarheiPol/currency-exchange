ALTER TABLE quote_jobs
    ADD COLUMN source TEXT NOT NULL DEFAULT 'scheduler'
    CONSTRAINT quote_jobs_source_check CHECK (source IN ('refresh', 'scheduler'));
