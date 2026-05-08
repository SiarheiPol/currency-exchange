CREATE INDEX quote_jobs_pending_idx
    ON quote_jobs (next_run_at)
    WHERE status = 'pending';

CREATE UNIQUE INDEX quote_jobs_dedup_key_uidx
    ON quote_jobs (dedup_key)
    WHERE dedup_key IS NOT NULL;
