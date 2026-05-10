CREATE TABLE quote_jobs (
    id           UUID PRIMARY KEY,
    base         CHAR(3) NOT NULL,
    quote        CHAR(3) NOT NULL,
    status       TEXT NOT NULL,
    attempts     INT NOT NULL DEFAULT 0,
    next_run_at  TIMESTAMPTZ NOT NULL,
    lease_until  TIMESTAMPTZ,
    locked_by    TEXT,
    dedup_key    TEXT,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    last_error   TEXT
        CONSTRAINT last_error_length CHECK (last_error IS NULL OR length(last_error) <= 4096),
    CONSTRAINT no_self_pair CHECK (base <> quote)
);
