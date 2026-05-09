# Background update mechanism

## Status
Decided.

## Context

The service exposes `POST /quotes/refresh` and `GET /quotes/latest?base=BASE&quote=QUOTE`. The refresh handler must return an `update_id` right away. The real fetch from the rates provider happens later, in the background.

We keep two tables:
- `quote_jobs` — operational state of refresh jobs (`pending`, `running`, `done`, `failed`).
- `quotes` — successful rate values keyed by `(base, quote)` pair. Read by `GET /quotes/latest?base=BASE&quote=QUOTE`.

There are **two producers** of jobs and **one consumer**:
- Producer A: the refresh handler, when a client calls `POST /quotes/refresh`.
- Producer B: the scheduler, which fires on a tick to keep the cache warm even without client traffic.
- Consumer: the worker pool, which reserves jobs, calls the rates provider, and writes to `quotes`.

The worker is the **sole writer** of `quotes`. There are no parallel writers, no timestamp-guard logic, and no race conditions between the producers — both go through the same queue and the same dedup mechanism (see `idempotency.md`).

## Decision

The queue lives in Postgres, in the `quote_jobs` table. Workers poll with `FOR UPDATE SKIP LOCKED`. Both producers call `JobQueue.Enqueue` with a deduplication key. The unique index on `dedup_key` collapses concurrent enqueues for the same `(base, quote, bucket)` into a single job.

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
    Base      string    // ISO 4217 base currency code, e.g. "EUR"
    Quote     string    // ISO 4217 quote currency code, e.g. "MXN"
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

Two tables. `quote_jobs` is the queue. `quotes` is the rate cache that `GET /quotes/latest?base=BASE&quote=QUOTE` reads. The full data model (including any tables added later) will be summarized in `architecture.md`.

### `quote_jobs`

```sql
CREATE TABLE quote_jobs (
    id           UUID PRIMARY KEY,
    base         CHAR(3) NOT NULL,
    quote        CHAR(3) NOT NULL,
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
        CONSTRAINT last_error_length CHECK (last_error IS NULL OR length(last_error) <= 4096),
    CONSTRAINT no_self_pair CHECK (base <> quote)
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
    base       CHAR(3) NOT NULL,
    quote      CHAR(3) NOT NULL,
    price      NUMERIC NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (base, quote),
    CONSTRAINT no_self_pair CHECK (base <> quote)
);
```

One row per currency pair. The worker is the **sole writer**; producers (refresh handler, scheduler) never touch this table. `price` means "1 unit of `base` = `price` units of `quote`" — the forex base/quote convention. E.g. `(base='EUR', quote='MXN', price=20.255648)` means 1 EUR = 20.255648 MXN. Historical quotes are out of scope for MVP.

## Producers

### Refresh handler (`POST /quotes/refresh`)

The handler validates the body (both `base` and `quote` fields), computes `dedup_key = sha256(UPPER(base) + ":" + UPPER(quote) + ":" + bucket_unix_seconds)`, then calls `Enqueue`. The returned `id` is sent back to the client as the `update_id`.

Pseudocode:
```go
bucket := (now.Unix() / W) * W
key := fmt.Sprintf("%s:%s:%d", strings.ToUpper(base), strings.ToUpper(quote), bucket)
dedup := sha256Hex(key)

id, _, err := queue.Enqueue(ctx, Job{
    ID:        uuid.New(),
    Base:      base,
    Quote:     quote,
    DedupKey:  dedup,
    NextRunAt: now,
})
```

### Scheduler

The scheduler ticks every `T` seconds. On each tick it iterates over all autogenerated ordered pairs from the whitelist (6 pairs for `USD`, `EUR`, `MXN`: `USD/EUR`, `USD/MXN`, `EUR/USD`, `EUR/MXN`, `MXN/USD`, `MXN/EUR`) and calls `Enqueue` for each pair, with the same `dedup_key` rule:

```go
for _, pair := range pairs { // pairs = all ordered permutations of whitelist, self-pairs excluded
    bucket := (now.Unix() / W) * W
    key := fmt.Sprintf("%s:%s:%d", strings.ToUpper(pair.Base), strings.ToUpper(pair.Quote), bucket)
    dedup := sha256Hex(key)

    _, _, _ = queue.Enqueue(ctx, Job{
        ID:        uuid.New(),
        Base:      pair.Base,
        Quote:     pair.Quote,
        DedupKey:  dedup,
        NextRunAt: now,
    })
}
```

The scheduler ignores the `(id, inserted)` return values. Its goal is "ensure a job exists for this `(base, quote, bucket)`".

**Bulk upstream fetch.** The `RatesProvider` interface exposes a single method keyed by `Pair`:

```go
type Pair struct {
    Base  string // ISO 4217, e.g. "EUR"
    Quote string // ISO 4217, e.g. "MXN"
}

type FetchResult struct {
    Quotes map[Pair]Quote          // successfully fetched per pair
    Errors map[Pair]*ProviderError // per-pair errors (nil map if none)
}

type RatesProvider interface {
    FetchPairs(ctx context.Context, pairs []Pair) (FetchResult, error)
}
```

The return shape is **per-pair**, not all-or-nothing. Three result categories:

- `result.Quotes[pair]` populated → success for that pair.
- `result.Errors[pair]` populated → that one pair failed. The **primary error path** is a missing pair synthesised by our client: when `apilayerProvider` calls `source=EUR&currencies=MXN,USD`, a silent drop of `MXN` by the upstream is detected by comparing the requested pairs against the returned quote keys; missing pairs produce a `ProviderError` in `result.Errors`. This is the dominant failure mode (empirically confirmed: invalid codes are silently dropped, not rejected with code 202).
- `err` non-nil → the entire batch call failed (network error, malformed response, auth failure, quota exhaustion, etc.).

The worker handles each category:
- For each `pair` in `result.Quotes`: upsert to `quotes`, `Complete` the corresponding job.
- For each `pair` in `result.Errors`: classify per `ProviderError.IsTransient()` and `Reschedule` or `Fail` that job individually.
- If the whole batch returned `err`: classify and reschedule/fail all reserved jobs.

There is no cross-rate derivation. `EUR/MXN` is fetched directly from upstream (`source=EUR&currencies=MXN`) because reciprocal and cross-rate computation introduces measurable error (empirically: 5×10⁻⁷ to 6×10⁻⁶ divergence).

The `apilayerProvider` implementation groups the `pairs []Pair` slice by `Base`, then issues **one HTTP call per unique base** (`source=<base>&currencies=<comma-joined quotes>`). This is an implementation detail of the provider, not of the worker.

**Provider capability check at startup.** The service performs a synthetic pair call (e.g., `FetchPairs([{USD, EUR}])`) at startup. The implementation parses the `success` boolean in the upstream JSON response body — a `success: false` body returned with HTTP 200 is treated as a failure (empirically confirmed: auth failures, quota exhaustion, and other global errors use this shape). If startup fails for a clear reason (e.g., `success:false` with code 101 — invalid key), the service refuses to start with a descriptive error. Misconfiguration is caught before traffic.

The flow per scheduler tick:
1. Scheduler enqueues N independent jobs (one per whitelist pair).
2. A worker calls `Reserve(N, lease)` where `N = len(whitelist) * (len(whitelist) - 1)` (6 for [USD, EUR, MXN]) and gets up to N jobs back.
3. Worker collects the `(Base, Quote)` pairs from reserved jobs and calls `FetchPairs(pairs)`. The `apilayerProvider` groups these by `Base` internally and issues one HTTP call per unique base — up to `len(whitelist)` HTTP calls for a full scheduler batch.
4. Per-pair results are dispatched: successes upsert to `quotes` and `Complete` the job; failures `Reschedule` or `Fail` per the error class.

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
   RETURNING id, base, quote, attempts;
   ```
3. The worker collects the `(Base, Quote)` pairs from reserved jobs and calls `RatesProvider.FetchPairs(pairs)`. With one job, the list has one pair; with the full whitelist reserved in one tick, all pairs.
4. **On success.** In one transaction:
   - Upsert the row in `quotes`:
     ```sql
     INSERT INTO quotes (base, quote, price, updated_at)
     VALUES ($1, $2, $3, $4)
     ON CONFLICT (base, quote) DO UPDATE
        SET price      = EXCLUDED.price,
            updated_at = EXCLUDED.updated_at;
     ```
     This is Postgres "upsert" syntax: insert a new row, and if a row with the same `(base, quote)` pair (the primary key) already exists, update it with the new values. `EXCLUDED` refers to the row that the `INSERT` tried to add. Result: exactly one row per currency pair, always reflecting the latest successful fetch.
   - `Complete(id)` — `status='done'`, `completed_at=now()`.

   The two writes are wrapped in a single transaction, so either both succeed or both roll back. We never leave `quotes` updated without the corresponding `quote_jobs` audit, or vice versa.
5. **On error.**
   - If `attempts + 1 < max_attempts`: `Reschedule(id, reason, after=backoff(attempts))`. The job returns to `status='pending'`, `next_run_at = now() + after`, `attempts += 1`, `last_error = reason`, `lease_until = NULL`. The API still shows the job as `pending` while retries are in flight; this mapping is the concern of `api-contract.md`.
   - Else: `Fail(id, reason)`. Sets `status='failed'`, `completed_at=now()`, `last_error = reason`.

Backoff is exponential with jitter, capped at 60 seconds. `max_attempts` defaults to 5.

## Concurrency model

The default model is **a single worker goroutine** (`K = 1`). Its loop:

1. Call `Reserve(N, lease)` where `N = len(whitelist) * (len(whitelist) - 1)` (6 for [USD, EUR, MXN]).
2. If 1 to N jobs are returned, call `RatesProvider.FetchPairs(pairs)`, then upsert each successful result into `quotes` and `Complete` each job in one transaction. On error, `Reschedule` or `Fail`.
3. If nothing is returned, sleep one poll interval.

`Reserve(N)` returns **up to N** rows (`LIMIT $n` in SQL). Worker handles partial batches without waiting for a full set — a single pending job goes through `FetchPairs([pair])` immediately, no batching delay.

### Why K = 1 by default

In steady state, one scheduler tick produces N = `len(whitelist) * (len(whitelist) - 1)` pending jobs in one bucket. One worker grabs all of them with `Reserve(N)` and processes them with up to `len(whitelist)` batched upstream calls (one per unique base currency). Other workers in a pool would find an empty queue and sleep.

A pool of K > 1 was the right model when each worker did `Reserve(1)` and made its own `Fetch` for one currency, parallelizing upstream calls. With batched upstream, that parallelism collapses into one HTTP call per base, and extra workers add no throughput.

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
| Worker pool (`K`) | Parallel upstream fetches | 1 (with batched `FetchPairs`) | Increase only for the scenarios listed above |

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

- Coalescing rules and `dedup_key` derivation. See `idempotency.md`. (updated formula: `sha256(UPPER(base) + ":" + UPPER(quote) + ":" + bucket_unix_seconds)`).
- Classical idempotency via `Idempotency-Key` header (Stage 6 extension). See `idempotency.md`.
- Message broker (Kafka, NATS, RabbitMQ, Redis Streams). Useful only if Postgres write throughput becomes the bottleneck. Not now.
- `LISTEN/NOTIFY` for push-style pickup. Optional later.
- Tariff-plan-specific values for `T`, `W`, `K`, lease, poll interval. See `capacity.md`.
- Upstream rate limiting and circuit breaker. See `resilience.md`.
