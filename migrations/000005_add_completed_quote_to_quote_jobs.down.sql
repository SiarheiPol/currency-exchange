ALTER TABLE quote_jobs
    DROP CONSTRAINT done_has_quote,
    DROP COLUMN quote_updated_at,
    DROP COLUMN price;
