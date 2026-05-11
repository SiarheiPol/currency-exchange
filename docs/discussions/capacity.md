# Capacity

## Status
Decided.

## Context

Other documents define **mechanism** (queue, scheduler, retries). This document defines **sizing** — concrete values for `T`, `W`, `K`, pool sizes, timeouts, retention. Numbers are the operational layer; the architectural decisions stay valid no matter what numbers we pick.

The dominant constraint is the rates-provider tariff plan. Every parameter in this document either flows from the chosen plan or from observed runtime characteristics (HTTP latency, DB query timing).

This document does **not** repeat the **why** of each parameter — that lives in the relevant decision document. It adds **concrete defaults** and **scaling triggers**: when a parameter should change and what signal indicates the change.

## Tariff matrix

Reference: apilayer-family plans (currencylayer, fixer, exchangeratesapi.io — all share similar tiers). Note: Free plan accepts any `source` currency (any source tested: USD, EUR, MXN all returned `success: true`). This contradicts the public documentation but was empirically confirmed.

| Plan | Monthly quota | Upstream cadence | Cost | Suitable for |
|---|---|---|---|---|
| Free | 100 | daily | $0 | demo only |
| Basic | 10 000 | daily | $14.99 | early prod, low traffic |
| Pro+ | 100 000 | 10 minutes | $59.99 | normal prod |
| Business | 500 000 | 60 seconds | $99.99 | high-frequency prod |

The cadence is the rate at which the provider itself refreshes data. Calling more often than the cadence returns identical data; quota burns for no reason.

## Recommended `T` and `W` per plan

`T` = scheduler tick (freshness floor). `W` = coalescing window (upstream call ceiling). Constraint: `W ≤ T`. Both formulae assume one HTTP call per unique base currency per scheduler tick (`apilayerProvider` groups pairs by base). For whitelist [USD, EUR, MXN], one scheduler tick triggers up to 3 HTTP calls (one per base), each returning 2 quote values. "Calls / month" in the table counts HTTP calls, not scheduler ticks.

| Plan | `T` | `W` | Calls / month | % quota |
|---|---|---|---|---|
| Free | 24h | 1h | ~90 | 90% |
| Basic | 4h | 1h | ~540 | 5.4% |
| Pro+ | 10min | 1min | ~12 960 | 13% |
| Business | 60s | 30s | ~129 600 | 25.9% |

**Headroom is intentional.** A scheduler running at exact provider cadence × number of currencies leaves zero margin for client-driven refreshes. Settings above keep the scheduler comfortably below the quota and allow client `POST /quotes/refresh` calls to consume the rest of the budget.

**Free plan headroom is now tighter** after the 3×-per-tick correction: ~90 calls/month for the scheduler alone, leaving only ~10 calls for client-driven refreshes. Use Free for proof-of-life only; Basic is the minimum for any meaningful testing. On the positive side, any `source` is accepted on Free plan (empirically confirmed), so no USD-source restriction applies.

**Setting `W > T` is forbidden by configuration validation.** A startup check refuses to run if `W > T` (degenerate case, see `idempotency.md`).

## Refresh latency SLA

`REFRESH_MAX_LATENCY_SECONDS` is the contractual upper bound on the p99 latency from accepted `POST /quotes/refresh` to `GET /quotes/:id` reporting `status=done`. Definition and exclusions live in `api-contract.md > POST /quotes/refresh > Latency contract`; SLI shape in `monitoring.md > SLO and SLI thinking > Job completion SLI`.

Default: **2s**. The SLA is not tariff-dependent at the HTTP-call level — apilayer-family p99 is ≈ 500ms across all plans — so the same baseline fits Pro+, Business, and Enterprise. Free and Basic share the same technical floor; their SLI carries less business weight because upstream data is daily anyway, and operators may relax the value if `pollInterval` headroom matters more.

| Plan | `REFRESH_MAX_LATENCY_SECONDS` | Notes |
|---|---|---|
| Free / Basic | 5s | technically same as Pro+; loosened only because upstream data is daily |
| Pro+ | 2s | |
| Business | 2s | |
| Enterprise | 1s or LISTEN/NOTIFY-driven | sub-second SLA crosses the polling-cost boundary; see `background-mechanism.md > Polling` |

### Budget decomposition

The SLA must accommodate every step from handler return to job completion:

```
REFRESH_MAX_LATENCY_SECONDS  ≥  pollInterval + upstream_p99 + db_p99 + margin
              2s             ≥      1s       +    500ms     +   100ms +  400ms
```

`upstream_p99` follows the rationale in `Per-call timeouts > Outbound HTTP`. `db_p99` covers the upsert + `Complete` transaction. `margin` absorbs scheduler jitter, Go runtime pauses, and rare missed-tick scenarios. Upstream spikes that blow this budget push the job into retry; by SLI design, retried jobs are excluded from the latency metric rather than counted as breaches.

### Derived worker parameters

The four business-level env vars — `WHITELIST_CURRENCIES`, `SCHEDULER_TICK_SECONDS`, `COALESCING_WINDOW_SECONDS`, `REFRESH_MAX_LATENCY_SECONDS` (+ `WORKER_COUNT`) — drive two derived worker parameters:

| Derived | Formula | Why |
|---|---|---|
| `pollInterval` | `REFRESH_MAX_LATENCY − upstream_p99 − db_p99 − margin` | tightest poll cadence that fits the SLA budget |
| `batchSize` | `ceil(len(pairs) / WORKER_COUNT)` (minimum 1) | drain a full scheduler tick in one `Reserve` per worker; matches the `N` already prescribed in `background-mechanism.md > Concurrency model` |

`leaseTime` stays sized by `Per-call timeouts > Worker job lease` (60s) — its constraint is "longer than the longest legitimate `FetchPairs` × retries", independent of the SLA budget.

### Startup validation

Refuse to start with a clear error if `REFRESH_MAX_LATENCY_SECONDS < upstream_p99 + db_p99 + margin` (the SLA is unachievable in the polling architecture). The operator must either relax the SLA, raise `WORKER_COUNT` if upstream is the bottleneck, or switch to LISTEN/NOTIFY (see `background-mechanism.md > Polling`).

## Worker pool `K`

Default `K = 1`. Reasoning in `background-mechanism.md`: with batched `FetchPairs`, one worker per tick is enough.

| Trigger | Action |
|---|---|
| `quote_jobs_pending_count > whitelist_size × 2` for > 5 min | bump `K` to 2 |
| Upstream `p99` latency > `T` | bump `K` (lag scenario) |
| `worker_iterations_total{outcome="idle"}` >> `{outcome="work"}` | check if `K` is too high |
| Many distinct refresh buckets pending (W << T scenario, frequent client refreshes) | bump `K` to 2–4 |

`K > 8` rarely makes sense at our scale. Past that, the constraint shifts to upstream provider rate limits, not worker concurrency.

## Per-call timeouts

Defaults from `resilience.md`, repeated here with the rationale:

| Timeout | Default | Why |
|---|---|---|
| Outbound HTTP (upstream) | 5s | apilayer-family typical p99 ~500ms; 10× margin covers congestion |
| DB per-query | 2s | typical query <10ms; 200× margin covers slow plans |
| DB pool acquisition | 1s | acquisition under healthy pool is sub-millisecond |
| HTTP server `ReadTimeout` | 10s | client upload should not exceed this for any request body |
| HTTP server `WriteTimeout` | 30s | leaves room for handler + DB + serialisation; not for upstream (handler returns fast) |
| HTTP server `IdleTimeout` | 60s | standard for keep-alive |
| `/readyz` total | 2s | sum of internal checks must fit |
| Worker job lease | 60s | longer than the longest legitimate `FetchPairs` × retries |
| Graceful shutdown | 30s | enough for in-flight DB tx and HTTP responses to drain |

**Tariff-aware tuning.** If the chosen provider has higher latency (some lower-tier APIs are noticeably slower), only the outbound HTTP timeout changes — the others stay.

## Database sizing

### `pgxpool`

Default size: **25 connections per service instance**.

| Trigger | Action |
|---|---|
| `db_connections_acquired_total` saturating, latency rising | bump pool size |
| Postgres `max_connections` near limit (default 100) | scale Postgres or reduce pool |
| Few active connections, but many queued waiters | check if connection acquisition timeout is too short |

For multi-instance deployment: total pool = pods × 25. Postgres `max_connections` should be set to `(pods × pool_size) × 1.5` for safety, with pgbouncer in front if total exceeds 200.

### Storage projections

| Table | Per row | Volume @ 1000 jobs/day | 30-day retention | Notes |
|---|---|---|---|---|
| `quote_jobs` | ~250 B | 30 000 rows / 7 MB | 7 MB × 1 = 7 MB | trivial; cleanup of `done`/`failed` after 7 days bounds growth |
| `quotes` | ~90 B | 6 rows total | 540 B | one row per currency pair (6 pairs for [USD, EUR, MXN]), never grows |
| `upstream_quota` | ~100 B | 1 row / month | <1 KB | one row per `(provider, period)` |
| `idempotency_keys` (Stage 6) | ~600 B | 70M rows/day @ 1000 RPS × 80% | TTL 24h: ~41 GB | partitioning needed at this scale |

For MVP scope: total storage <100 MB easily. Stage 6 idempotency table is the only thing that grows materially; it gets its own retention and partitioning policy.

### Retention policy for `quote_jobs`

Old jobs serve audit/diagnostic purposes only. Retention rules:

- `done` and `failed` jobs older than **7 days** are deleted by a periodic cleanup task.
- `pending` and `running` jobs are not subject to time-based deletion (they should be terminal within minutes; if they aren't, that's a bug, not retention).
- The unique partial index on `dedup_key` (where `status` ⊆ `{pending, running}` would have been ideal, but the index is `WHERE dedup_key IS NOT NULL` — terminal jobs still occupy index slots until deletion) means the cleanup keeps the index slim.

Cleanup runs in-process every hour by default. Deletion is batched (`LIMIT 10000`) to avoid long-running locks. Retention horizon is configurable via `QUOTE_JOBS_RETENTION_DAYS`.

Stage 6 idempotency keys have their own 24h TTL discussed in `idempotency.md > Cleanup and TTL`.

### Queries to watch

- `SELECT ... FROM quote_jobs WHERE status='pending' AND next_run_at <= now() FOR UPDATE SKIP LOCKED LIMIT $n` — partial index `quote_jobs_pending_idx` keeps this fast.
- `INSERT INTO quotes ... ON CONFLICT (base, quote) DO UPDATE` — primary-key upsert, sub-millisecond.
- `SELECT 1` (readiness) — should never appear in slow-query logs.

`pg_stat_statements` enabled at deployment. Any query exceeding p95 of 50ms is investigated.

## Per-instance resources

Typical Go service profile in our shape:

| Resource | At idle | Active (1000 RPS) | Notes |
|---|---|---|---|
| RSS | 50–100 MB | +10 MB per 1000 in-flight requests | small Go binaries |
| CPU | <0.05 cores | ~0.5 cores per 500 RPS | dominated by JSON marshal + DB round-trip |
| FDs | ~50 | +1 per concurrent upstream call | Go HTTP client reuses connections |
| Goroutines | ~30 | +1 per in-flight HTTP + workers + scheduler | `go_goroutines` metric |

Container limits to start with:
- **CPU request**: 100m, **limit**: 500m.
- **Memory request**: 128 MB, **limit**: 256 MB.

Adjust based on observed `container_memory_working_set_bytes` and `container_cpu_usage_seconds_total`.

## Log volume

From the discussion in `monitoring.md`: ~250 B per JSON log line, ~5 lines per request.

| Load | Bytes/sec | Per day | Per month |
|---|---|---|---|
| Demo (1 req/min) | ~30 B | ~2.6 MB | ~80 MB |
| 10 RPS | ~15 KB | ~1.3 GB | ~40 GB |
| 100 RPS | ~150 KB | ~13 GB | ~400 GB |
| 1000 RPS | ~1.5 MB | ~130 GB | ~3.9 TB |

After typical aggregator compression (5–10×), storage is divided by ~7. At 1000 RPS, ~600 GB/month after compression. This is when log retention policies start mattering.

For MVP scope: irrelevant — we run at <10 RPS, stdout to local file or `docker compose logs`.

## Growth scenarios

Honest forecasts for three audience-size points. Coalescing makes the upstream call rate **independent** of user count once the user base is large enough that every bucket has at least one refresher; the constraint moves to HTTP and DB layers.

### Upstream call ceiling under coalescing

With `W = 30s` and 3 whitelist currencies fetched in one batched call, the **maximum** upstream call rate per currency is `1 call / W = 0.033/s`. With batching, that maps to **2 batched calls per minute** at the absolute ceiling — when every single `W` bucket has at least one refresh-driven job for at least one currency.

In month: `2 × 60 × 24 × 30 = 86 400 batched calls/month` worst-case.

This is the **ceiling**, not the typical case:

- Most buckets have no client-driven activity (refresh is bursty). The scheduler tick `T` then dominates.
- With `T = 60s` (Business default), scheduler alone produces `60 × 24 × 30 = 43 200 calls/month`.
- Adding sparse client traffic that fills empty buckets: typical realistic load lands between 43 200 and 86 400/month for the Business plan tariff.

Pro+ tariff's recommended `T = 10min` produces `~4 320/month` from the scheduler alone; client-driven calls in the gaps can push toward Pro+'s 100 000 ceiling under aggressive load (10×–20× headroom in practice).

### 3M users (current scale)

- Active concurrent ~1–3%: 30 000–90 000 active.
- Refresh rate per active user ~0.001 Hz: 30–90 refresh/sec.
- After coalescing (W=30s, 3 currencies): worst-case ~86 400/month, realistic 50 000–70 000/month under bursty load → fits Business comfortably; Pro+ leaves narrow margin under burst.
- `/quotes/:id` polling at 1 RPS per active refresh × 30s window: 900–2 700 RPS read load on `quote_jobs` (single-row PK lookups) — small for Postgres.
- `/latest` RPS depends on UI patterns; assume 10× `/refresh` rate ≈ 300–900 RPS.
- HTTP: single instance suffices for write path; consider 2 instances for redundancy.
- DB: trivial.

### 10M users

- 100–300 active refresh/sec.
- Upstream: same ceiling (W=30s capped at ~86 400/month) — Business comfortable.
- HTTP: 100–300 RPS write + 1 000–3 000 RPS read → 2–3 service instances behind LB.
- DB: single primary still adequate; consider read replica for `/latest` if reads dominate.

### 50M users

- 500–1500 active refresh/sec → polling `/quotes/:id` at 5 000–15 000 RPS.
- `/latest` traffic: 5 000–40 000 RPS (depends on UI cache discipline; CDN/BFF caching of `/latest` (with `max-age=W`) drops this by 90%+).
- Upstream: still bounded by `W` — Business plan, occasionally consider Enterprise / custom for headroom.
- HTTP: 500–1500 RPS write path + sustained read load → 5–10 service instances.
- DB: read replica for `/latest`; primary still handles writes; possibly partition `quote_jobs` by month if Stage 6 idempotency keys make any table large.
- Multi-region active-passive becomes worth considering — see `scaling.md`.

The non-obvious property of this architecture: **upstream cost is bounded by `W`, not user count**. Adding 10× users does not multiply provider quota use. Read load on the service layer (`/quotes/:id` polling and `/latest`) does scale with users, however; that is the dimension that drives instance and replica counts.

## Token bucket sizing

Bucket size = monthly quota of the chosen plan (full budget available at start of month).

Refill rate = `quota / (30 × 86400)` tokens per second.

Example for Business 500 000:
```
bucket_size = 500 000
refill_rate = 0.193 tokens/sec ≈ 11.6/min ≈ 695/hour
```

Refill happens implicitly via the SQL update — no in-memory token timer. The Postgres row tracks `used`; comparisons are `used < quota_limit`. New month creates a new row, naturally refilling the budget.

## Test and dev defaults

For `docker compose up` (running against the fake rates provider):

```
T = 30s
W = 30s
REFRESH_MAX_LATENCY_SECONDS = 2s
K = 1
DB pool size = 10
Worker poll interval = 1s     # derived from REFRESH_MAX_LATENCY; shown for readability
Worker batch size = N         # derived: ceil(len(pairs) / K), 6 for [USD, EUR, MXN]
Lease = 60s
Log level = debug
Plan simulation = "business"   # fake provider config
```

These are the values in `.env.example`. The four "business" env vars are authoritative; `Worker poll interval` and `Worker batch size` are derived and logged at startup. Test environments override only what they need to test (e.g., setting `T = 5s` to validate scheduler behaviour faster, or `REFRESH_MAX_LATENCY = 10s` to relax the SLA when running against a deliberately slow fake provider).

## AI-agent considerations

This document realises the structural principles from `agent-development.md`:

- **Single source of truth** — every capacity number has one defined place: `.env.example`. Application code reads it; documentation references it.
- **Reproducibility** — defaults are committed; local development matches the documented matrix.
- **No silent rescoping** — when an agent observes a metric crossing one of the trigger thresholds in this document, it must surface that fact in the PR description, not silently adjust defaults.

The general rationale lives in `agent-development.md`.

## Not in scope here

- **Failure modes and retry logic.** Lives in `resilience.md`.
- **Scaling beyond a single Postgres primary** — read replicas, partitioning, sharding. Lives in `scaling.md`.
- **Load testing methodology.** Lives in `load-testing.md`.
- **SLO numbers.** Numbers depend on production data; this document has the *capacity* shape, `monitoring.md` has the *target* shape, real numbers come from observation.
- **Cost modelling beyond tariff plans.** Cloud spend, Postgres sizing in dollars — out of scope.
