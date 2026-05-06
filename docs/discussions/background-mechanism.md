# Background update mechanism

## Status
Decided.

## Context

The service exposes `POST /quotes/refresh` and `GET /quotes/latest/:currency`. The refresh handler must return an `update_id` right away. The real fetch from the rates provider happens later, in the background.

We keep two tables:
- `quote_jobs` — operational state of refresh jobs (`pending`, `running`, `done`, `failed`).
- `quotes` — successful rate values keyed by currency. Read by `GET /quotes/latest/:currency`.

There are **two producers** of jobs and **one consumer**:
- Producer A: the refresh handler, when a client calls `POST /quotes/refresh`.
- Producer B: the scheduler, which fires on a tick to keep the cache warm even without client traffic.
- Consumer: the worker pool, which reserves jobs, calls the rates provider, and writes to `quotes`.

The worker is the **sole writer** of `quotes`. There are no parallel writers, no timestamp-guard logic, and no race conditions between the producers — both go through the same queue and the same dedup mechanism (see `idempotency.md`).

## Decision

The queue lives in Postgres, in the `quote_jobs` table. Workers poll with `FOR UPDATE SKIP LOCKED`. Both producers call `JobQueue.Enqueue` with a deduplication key. The unique index on `dedup_key` collapses concurrent enqueues for the same `(currency, bucket)` into a single job.

## Why

- The table is here anyway. Polling on top of it is a small step.
- It works with more than one instance out of the box. Two pods take different jobs because of `FOR UPDATE SKIP LOCKED`. No sticky routing is needed.
- A pod restart does not lose work. Pending rows stay until a worker picks them up.
- Visible from outside: `SELECT status, count(*) FROM quote_jobs GROUP BY 1` is the queue view for ops.
- Easy to test. Integration tests run against real Postgres (testcontainers). Unit tests use an in-memory fake of the same interface.
- Single writer of `quotes` (the worker). No two-writer race conditions.

A Go-channel design looks shorter, but it forces single-instance and sticky routing. Once the table is here, the saved code is small.

## Interface

All queue access goes through one interface. This is the seam for tests and for a future swap to a broker.

```go
type Job struct {
    ID        JobID
    Currency  string
    DedupKey  string    // empty means "no coalescing for this job"
    Attempts  int
    NextRunAt time.Time
}

type JobQueue interface {
    Enqueue(ctx context.Context, j Job) (JobID, bool, error) // returns (id, inserted, err); inserted=false on dedup conflict
    Reserve(ctx context.Context, n int, lease time.Duration) ([]Job, error)
    Complete(ctx context.Context, id JobID) error
    Reschedule(ctx context.Context, id JobID, reason string, after time.Duration) error
    Fail(ctx context.Context, id JobID, reason string) error
}
```

`Enqueue` returns `(id, inserted)`:
- `inserted=true` — a new job was created with the given `id`.
- `inserted=false` — `dedup_key` already existed; `id` is the existing job's id.

This lets the caller (handler or scheduler) react if it cares about which case happened. Most callers do not care and just return the id.

Two implementations ship:
- `pgQueue` — Postgres-backed. Used in integration tests and production.
- `memQueue` — in-memory fake. Used in unit tests of higher layers.

## Schema

Two tables. `quote_jobs` is the queue. `quotes` is the rate cache that `GET /latest/:currency` reads. The full data model (including any tables added later) will be summarized in `architecture.md`.

### `quote_jobs`

```sql
CREATE TABLE quote_jobs (
    id           UUID PRIMARY KEY,
    currency     CHAR(3) NOT NULL,
    status       TEXT NOT NULL,          -- pending | running | done | failed
    attempts     INT NOT NULL DEFAULT 0,
    next_run_at  TIMESTAMPTZ NOT NULL,
    lease_until  TIMESTAMPTZ,
    locked_by    TEXT,
    dedup_key    TEXT,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    last_error   TEXT
        CONSTRAINT last_error_length CHECK (last_error IS NULL OR length(last_error) <= 4096)
);

CREATE INDEX quote_jobs_pending_idx
    ON quote_jobs (next_run_at)
    WHERE status = 'pending';

CREATE UNIQUE INDEX quote_jobs_dedup_key_uidx
    ON quote_jobs (dedup_key)
    WHERE dedup_key IS NOT NULL;
```

`dedup_key` is `NULL` when coalescing is disabled. The partial unique index lets many `NULL` rows coexist while uniqueness is enforced for real keys. The full coalescing rules live in `idempotency.md`.

### `quotes`

```sql
CREATE TABLE quotes (
    currency   CHAR(3) PRIMARY KEY,
    price      NUMERIC NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
```

One row per currency. The worker is the **sole writer**; producers (refresh handler, scheduler) never touch this table. The schema is intentionally minimal: a single current row per currency. Historical quotes (if ever needed) live elsewhere — out of scope for MVP.

## Producers

### Refresh handler (`POST /quotes/refresh`)

The handler validates the body, computes `dedup_key = sha256("<currency>:<bucket>")`, then calls `Enqueue`. The returned `id` is sent back to the client as the `update_id`.

Pseudocode:
```go
bucket := (now.Unix() / W) * W
dedup := sha256Hex(fmt.Sprintf("%s:%d", currency, bucket))

id, _, err := queue.Enqueue(ctx, Job{
    ID:        uuid.New(),
    Currency:  currency,
    DedupKey:  dedup,
    NextRunAt: now,
})
```

### Scheduler

The scheduler ticks every `T` seconds. On each tick it iterates over the whitelist of currencies (`USD`, `EUR`, `MXN`) and calls `Enqueue` for each, with the same `dedup_key` rule:

```go
for _, currency := range whitelist {
    bucket := (now.Unix() / W) * W
    dedup := sha256Hex(fmt.Sprintf("%s:%d", currency, bucket))

    _, _, _ = queue.Enqueue(ctx, Job{
        ID:        uuid.New(),
        Currency:  currency,
        DedupKey:  dedup,
        NextRunAt: now,
    })
}
```

The scheduler ignores the `(id, inserted)` return values — it does not own the id, and it does not care whether it created a new job or joined an existing one. Its goal is "ensure a job exists for this `(currency, bucket)`".

**Bulk upstream fetch.** All major rates providers we surveyed (exchangerate.host, exchangeratesapi.io, openexchangerates.org, currencylayer.com, fixer.io) accept multiple currencies in one HTTP call. The architecture assumes batch support and the `RatesProvider` interface exposes a single method:

```go
type FetchResult struct {
    Quotes map[string]Quote          // successfully fetched per currency
    Errors map[string]*ProviderError // per-currency errors (nil map if none)
}

type RatesProvider interface {
    FetchBatch(ctx context.Context, currencies []string) (FetchResult, error)
}
```

The return shape is **per-currency**, not all-or-nothing. Three result categories:

- `result.Quotes[currency]` populated → success for that currency.
- `result.Errors[currency]` populated → that one currency failed (e.g., upstream returned a rate for `USD` and `EUR` but a per-currency error for `MXN`).
- `err` non-nil → the entire batch call failed (network error, malformed response, all currencies failed).

The worker handles each category:
- For each `currency` in `result.Quotes`: upsert to `quotes`, `Complete` the corresponding job.
- For each `currency` in `result.Errors`: classify per `ProviderError.IsTransient()` and `Reschedule` or `Fail` that job individually (see `resilience.md > Upstream error classification`).
- If the whole batch returned `err`: classify and reschedule/fail all reserved jobs.

There is no separate single-currency `Fetch` and no fallback to sequential single calls. If a future provider does not support batch natively, that implementation may parallelize internal HTTP calls and assemble the same `FetchResult` shape — but this is the provider's concern, not the worker's. The worker contract is uniform.

**Provider capability check at startup.** The service performs a synthetic batch call (e.g., `FetchBatch([USD, EUR])`) at startup; if it returns a single-currency response or fails for an obvious reason (e.g., 401 with the configured key), startup fails fast with a clear error. Misconfiguration is caught before traffic.

The flow per scheduler tick:
1. Scheduler enqueues N independent jobs (one per whitelist currency).
2. A worker calls `Reserve(N, lease)` and gets up to N jobs back.
3. Worker collects currencies and calls `FetchBatch(currencies)` — one upstream call.
4. Per-currency results are dispatched: successes upsert and `Complete`; failures `Reschedule` or `Fail` per the error class.

This collapses N upstream calls into 1 per scheduler tick, which matters for paid tariff plans.

`T` and `W` are independent parameters with the constraint `W ≤ T`:
- `W` (coalescing bucket size) — minimum interval between upstream calls per currency. Caps cost.
- `T` (scheduler tick) — maximum interval between cache refreshes when no client traffic. Sets the freshness floor.

When client traffic is active, refresh-driven jobs run at the W cadence. When traffic is quiet, the scheduler tick drives the rate. Defaults and tariff-plan-specific values are the concern of `capacity.md`.

The scheduler is one goroutine per service instance. Multi-instance deployments do not need leader election: even if all instances tick at the same time, the unique constraint on `dedup_key` collapses their enqueues into one job.

### Bootstrap on startup

On service startup, the scheduler fires its first tick immediately (not after waiting `T`). This warms up `quotes` so that `GET /latest` does not return 404 for long after a fresh deploy.

### Whitelist reload

The whitelist of supported currencies (`USD,EUR,MXN`) is loaded from the `WHITELIST_CURRENCIES` env var at process startup. **Changes require a restart** in MVP. We do not hot-reload because the whitelist also affects HTTP validation (input rejection) and the OpenAPI schema (enum); diverging at runtime would create cross-cutting bugs.

Adding a currency without restart is recorded as a Stage 6 enhancement candidate.

## Lifecycle

1. A producer calls `Enqueue`. Postgres `INSERT ... ON CONFLICT (dedup_key) DO NOTHING` either creates a new row (`status='pending'`, `next_run_at=now()`) or returns no row.
2. A worker reserves a batch:
   ```sql
   UPDATE quote_jobs
      SET status      = 'running',
          lease_until = now() + interval '60 seconds',
          locked_by   = $worker_id,
          updated_at  = now()
   WHERE id IN (
       SELECT id FROM quote_jobs
        WHERE status = 'pending' AND next_run_at <= now()
        ORDER BY next_run_at
        FOR UPDATE SKIP LOCKED
        LIMIT $n
   )
   RETURNING id, currency, attempts;
   ```
3. The worker collects currencies from the reserved jobs and calls `RatesProvider.FetchBatch(currencies)`. With one job, the list has one currency; with the full whitelist reserved in one tick, all of them.
4. **On success.** In one transaction:
   - Upsert the row in `quotes`:
     ```sql
     INSERT INTO quotes (currency, price, updated_at)
     VALUES ($1, $2, $3)
     ON CONFLICT (currency) DO UPDATE
        SET price      = EXCLUDED.price,
            updated_at = EXCLUDED.updated_at;
     ```
     This is Postgres "upsert" syntax: insert a new row, and if a row with the same `currency` (the primary key) already exists, update it with the new values. `EXCLUDED` refers to the row that the `INSERT` tried to add. Result: exactly one row per currency, always reflecting the latest successful fetch.
   - `Complete(id)` — `status='done'`, `completed_at=now()`.

   The two writes are wrapped in a single transaction, so either both succeed or both roll back. We never leave `quotes` updated without the corresponding `quote_jobs` audit, or vice versa.
5. **On error.**
   - If `attempts + 1 < max_attempts`: `Reschedule(id, reason, after=backoff(attempts))`. The job returns to `status='pending'`, `next_run_at = now() + after`, `attempts += 1`, `last_error = reason`, `lease_until = NULL`. The API still shows the job as `pending` while retries are in flight; this mapping is the concern of `api-contract.md`.
   - Else: `Fail(id, reason)`. Sets `status='failed'`, `completed_at=now()`, `last_error = reason`.

Backoff is exponential with jitter, capped at 60 seconds. `max_attempts` defaults to 5.

## Concurrency model

The default model is **a single worker goroutine** (`K = 1`). Its loop:

1. Call `Reserve(N, lease)` where `N = len(whitelist)` (3 in MVP).
2. If 1 to N jobs are returned, call `RatesProvider.FetchBatch(currencies)`, then upsert each result into `quotes` and `Complete` each job in one transaction. On error, `Reschedule` or `Fail`.
3. If nothing is returned, sleep one poll interval.

`Reserve(N)` returns **up to N** rows (`LIMIT $n` in SQL). Worker handles partial batches without waiting for a full set — a single pending job goes through `FetchBatch([currency])` immediately, no batching delay.

### Why K = 1 by default

In steady state, one scheduler tick produces N pending jobs in one bucket. One worker grabs all of them with `Reserve(N)` and processes them with one batched upstream call. Other workers in a pool would find an empty queue and sleep.

A pool of K > 1 was the right model when each worker did `Reserve(1)` and made its own `Fetch` for one currency, parallelizing upstream calls. With batched upstream, that parallelism collapses into one HTTP call, and extra workers add no throughput.

### When K > 1 helps

There are scenarios where additional workers do useful work in parallel:

- **Multi-bucket lag.** If upstream is slow and a fetch takes longer than the scheduler tick, jobs from a new bucket pile up while the previous one is still in flight. A second worker can drain the new bucket in parallel.
- **Refresh activity with `W < T`.** When clients refresh in buckets that the scheduler has not yet reached, multiple distinct buckets can have pending jobs at the same time. Each worker takes one bucket.
- **Failed-job retries during fresh activity.** Rescheduled jobs become pending again with a future `next_run_at`. When that time arrives, they may coexist with newly enqueued jobs.
- **In-process redundancy** for a stuck worker. Lease-plus-cleaner already covers this at the cluster level via multi-instance deployment, so adding workers in-process is a weak form of the same defense.

`K` is configurable (>= 1). MVP default 1. Production defaults will be tuned in `capacity.md` based on observed lag and load.

### Heavy refresh traffic does not need more workers

A burst of `POST /quotes/refresh` requests for the same currency in one bucket creates exactly one job, regardless of request count. Coalescing collapses them all into the same `dedup_key`. The worker pool sees one job per bucket per currency. Heavy refresh traffic stresses the HTTP layer and the database write path — see "Three scaling dials" below — not the worker pool.

## Three scaling dials

The service has three independent scaling parameters that are easy to confuse:

| Dial | Concern | Default | How to scale |
|---|---|---|---|
| HTTP server concurrency | Inbound request handling | Go default — one goroutine per incoming request, no explicit pool | Add instances behind a load balancer |
| DB connection pool | Concurrent Postgres operations | pgx pool, ~25 connections | Tune pool size, scale Postgres (vertical, then read replica) |
| Worker pool (`K`) | Parallel upstream fetches | 1 (with batched `FetchBatch`) | Increase only for the scenarios listed above |

These three dials respond to different bottlenecks:
- **High refresh request rate** → HTTP server and DB pool. Coalescing protects the worker.
- **Slow upstream causing lag** → worker pool size.
- **Many distinct buckets pending** → worker pool size.
- **Multi-instance redundancy** → deploy more replicas; each carries the full triple.

Concrete tuning per tariff plan and load profile lives in `capacity.md`.

## Lease and crash recovery

A reserved job has a `lease_until`. If a worker crashes, the job stays `running` until the lease ends. A small cleaner runs every minute:

```sql
UPDATE quote_jobs
   SET status      = 'pending',
       lease_until = NULL,
       locked_by   = NULL,
       updated_at  = now()
 WHERE status = 'running' AND lease_until < now();
```

This is the only restart-recovery we need. No special startup scan.

## Polling

Default poll interval is 1 second. Good enough for our load. To lower pickup latency later, we can add `LISTEN/NOTIFY`: the producer runs `NOTIFY quote_jobs` after insert and workers wait on `LISTEN` instead of sleeping. The `JobQueue` interface does not change.

## Graceful shutdown

On `SIGTERM`:

1. Stop accepting HTTP requests.
2. Stop the scheduler tick.
3. Stop the worker `Reserve` loop.
4. Wait for current jobs to finish, up to a deadline (default 30s).
5. Exit. Unfinished jobs keep their lease and are picked up by another instance, or by the cleaner after the lease ends.

We never mark in-flight jobs as `failed` on shutdown. Lease plus cleaner is enough.

## Backpressure

`Enqueue` does not block. Postgres inserts are cheap. If the queue grows, ops see the metric `quote_jobs_pending_count` and scale workers (or rate-limit upstream at the gateway).

## Not in scope here

- Coalescing rules and `dedup_key` derivation. See `idempotency.md`.
- Classical idempotency via `Idempotency-Key` header (Stage 6 extension). See `idempotency.md`.
- Message broker (Kafka, NATS, RabbitMQ, Redis Streams). Useful only if Postgres write throughput becomes the bottleneck. Not now.
- `LISTEN/NOTIFY` for push-style pickup. Optional later.
- Tariff-plan-specific values for `T`, `W`, `K`, lease, poll interval. See `capacity.md`.
- Upstream rate limiting and circuit breaker. See `resilience.md`.
