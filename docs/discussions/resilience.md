# Resilience

## Status
Decided.

## Context

The service has multiple failure points: the external rates provider, the database, the network, and our own bugs. This document specifies how the service responds to each — what counts as transient, when it gives up, what recovery looks like.

This is **not** monitoring. `monitoring.md` covers visibility — what the service emits and what dashboards show. This document covers **what the service does** when something goes wrong: timeouts, retries, circuit breaker, fallback, panic recovery, shutdown ordering.

Some mechanisms are already described in other documents (lease + cleaner in `background-mechanism.md`, retry budget in worker lifecycle, graceful shutdown order). This document collects them in one place and adds the cross-cutting pieces (circuit breaker, error classification, timeouts).

## Failure inventory

The major failure modes the service must survive:

| # | Failure | Class |
|---|---|---|
| 1 | Upstream rates provider unreachable (DNS, connection refused, network partition) | transient |
| 2 | Upstream timeout | transient |
| 3 | Upstream returns 5xx | transient |
| 4 | Upstream returns 429 (rate limit) | transient |
| 5 | Upstream quota exhausted (monthly budget) | transient (resets) |
| 6 | Upstream returns 401/403 (auth) | permanent |
| 7 | Upstream returns 400 (bad request) | permanent |
| 8 | Upstream returns malformed JSON | permanent |
| 9 | Database unreachable | transient |
| 10 | Database query slow (timeout) | transient |
| 11 | Panic in handler / worker | transient (per-request) |
| 12 | Process killed (OOM, SIGKILL, hardware) | transient (orchestrator restarts) |
| 13 | Bad configuration on startup | permanent (refuse to start) |

For each, the response is described below. **Transient** means the service retries or routes traffic away and recovers automatically. **Permanent** means human intervention is needed — alert, fail-fast, do not retry.

## Timeouts everywhere

Every blocking operation has a timeout. Naked `context.Background()` is forbidden in long-running paths.

The **concrete numeric values** (defaults, ranges, tariff-plan-specific tuning) are in `capacity.md > Per-call timeouts`. Numbers there are the source of truth; this document covers the **rationale** and the **layering** rule.

### Layering rule

The outer timeout is always longer than the inner one; otherwise the inner timeout never fires.

```
HTTP server WriteTimeout
  └─ Handler context
       ├─ DB query timeout
       └─ Outbound HTTP timeout (with retry budget)
            ├─ Connect timeout
            └─ Read timeout
```

A nested operation must complete before its parent's deadline. If `WriteTimeout` is 30s and an outbound call alone could take 10s + retries, plus DB calls, the handler must orchestrate so total path stays under WriteTimeout.

### Why naked `context.Background()` is forbidden

`http.Get`, raw `db.Exec`, and similar calls without a deadline let goroutines block on TCP-level stalls until the OS closes the connection — typically two hours on Linux. One wedged goroutine ties up worker capacity, DB connections, FDs, and memory. Multiplied across worker iterations under failure conditions, this is a recipe for cascading failure.

The lint rule (Stage 2 of `implementation-roadmap.md`) blocks naked `context.Background()` outside `main.go` and tests.

## Upstream error classification

When `RatesProvider.FetchBatch` returns an error, the worker maps it to one of two actions in MVP: **reschedule** or **fail**. (Stage 6 adds a third action when the circuit breaker is in place — see the deferred section below.)

| Error | Class | Action |
|---|---|---|
| Connection refused / DNS error | transient | reschedule (backoff) |
| Read/write timeout | transient | reschedule (backoff) |
| `502/503/504` | transient | reschedule (backoff) |
| `429 Too Many Requests` | transient | reschedule (longer backoff, honour `Retry-After` if present) |
| Quota exceeded (provider-specific code) | transient | reschedule far ahead (`now + 1h`); alert ops |
| `401/403` | permanent | fail job; alert ops (auth issue) |
| `400` | permanent | fail job (request malformed — our bug or whitelist drift) |
| `200` with malformed body | permanent | fail job; alert (provider broke contract) |
| Unknown 4xx | permanent | fail job; log full response for diagnosis |

The classification is implemented in `RatesProvider`'s error type:

```go
type ProviderError struct {
    Code     string  // "transient" | "permanent" | "quota_exceeded"
    HTTPCode int
    Message  string
    Cause    error
}

func (e *ProviderError) IsTransient() bool { return e.Code == "transient" || e.Code == "quota_exceeded" }
```

The worker's `Reschedule` vs `Fail` decision is driven by this method, not by inspecting the error message string.

## Retry budget

Already specified in `background-mechanism.md`:
- `max_attempts = 5` per job.
- Exponential backoff with jitter, capped at 60s.
- After exhaustion → `status='failed'`.
- Permanent errors skip remaining attempts and fail immediately.

Concrete backoff schedule: `1s, 2s, 4s, 8s, 16s` (each ± up to 30% jitter). Total worst-case retry window for one job: ~31s.

## Circuit breaker (deferred — Stage 6)

Not implemented in MVP. MVP failure response relies on three layers:

1. **Per-call timeout** (5s) — caps wasted goroutine time on each upstream call.
2. **Per-job retry budget** (`max_attempts = 5` with exponential backoff) — prevents endless retries.
3. **Token bucket for quota** (described below) — caps total upstream calls per period.

This stack is sufficient for MVP scale: `K = 1` worker, single instance, async refresh pattern. Wasted-timeout overhead during a sustained outage is bounded (~25s per minute), and the user-facing impact is contained because clients see `pending` or `failed` via polling, not a hung HTTP response. Upstream load amplification at `K = 1` is `5×` per failure cycle — uncomfortable but not catastrophic.

A circuit breaker reduces wasted timeouts and prevents amplification on a struggling upstream. At our MVP scale that benefit is marginal.

**When to add it:**
- `K > 4` — concurrent workers multiply the amplification factor.
- Multi-instance deployment of more than 2–3 pods — cumulative amplification across pods. (At 2–3 pods the breaker is borderline; the operational incident pattern below is a better trigger.)
- Multi-provider fallback is being added — the breaker is the "switch to secondary" signal.
- "Stuck in timeouts" becomes a recurring operational incident pattern.

**Stage B caveat.** `scaling.md > Stage B` says no code changes are needed to go multi-instance. That is true under stable upstream. If multi-instance deployment coincides with recurring upstream outages, jump to Stage 6 breaker work alongside Stage B — the amplification of `K_per_pod × pod_count × max_attempts` retries on a struggling upstream becomes harmful.

**Design sketch for Stage 6:** standard three-state machine (closed / open / half-open) with `failure_threshold = 5`, `cooldown = 30s`, single half-open probe. Library `github.com/sony/gobreaker` or equivalent, wraps `RatesProvider.FetchBatch`. State is per-instance — no cross-instance coordination needed. Metric `rates_provider_breaker_state{provider,state}` is added when the breaker is wired in.

Subtle: **not all errors should trip the breaker.** `429` (rate limit), `quota_exceeded`, and `permanent` errors must be excluded — they are not symptoms of upstream sickness, and tripping the breaker on them only worsens the situation.

## Multi-provider fallback (deferred — Stage 6)

Not implemented in MVP. We support a single rates provider configured via env. Single-provider failure equals service degradation; the mitigation is alerting on `rates_provider_requests_total{outcome="error"}` and operator response.

The architecture supports extension: `RatesProvider` is an interface, and a chain wrapper would walk a list of providers, falling through to the next on transient errors. Sketch for Stage 6 reference:

```go
type chainProvider struct {
    providers []RatesProvider
}

func (c *chainProvider) FetchBatch(ctx context.Context, currencies []string) (map[string]Quote, error) {
    var lastErr error
    for _, p := range c.providers {
        result, err := p.FetchBatch(ctx, currencies)
        if err == nil {
            return result, nil
        }
        lastErr = err
        var pe *ProviderError
        if errors.As(err, &pe) && !pe.IsTransient() {
            return nil, err  // permanent → do not try fallback
        }
    }
    return nil, lastErr
}
```

Production-ready fallback needs more than the chain wrapper: per-provider quota tracking (each provider has a separate budget), latency-weighted ordering, primary/secondary distinction, breaker-driven switching. This belongs with the circuit-breaker work — both go in together at Stage 6.

## Quota protection (token bucket)

Each upstream provider has a monthly quota (Free 100, Basic 10k, Pro 100k, Business 500k). Burning through it causes hard failure for the rest of the period.

Protection: in-process token bucket sized for the configured plan.

```
bucket_size = monthly_quota
refill_rate = monthly_quota / (30 * 24 * 3600)   // tokens per second
```

`RatesProvider` acquires one token before each call. If the bucket is empty, it returns `ProviderError{Code: "quota_exceeded"}` immediately, without making the call.

The bucket lives in **Postgres** (table `upstream_quota`), not in process memory, so:
- Multiple service instances share one bucket.
- Restarts do not reset the count.

Implementation:
```sql
UPDATE upstream_quota
   SET used = used + 1
 WHERE provider = $1
   AND period   = date_trunc('month', now())
   AND used     < quota_limit
RETURNING used;
```

Zero rows returned → `quota_exceeded`. Atomic via Postgres row locking.

A separate periodic task resets the row at the start of each month (or it gets a new row keyed by `(provider, period)`).

The quota gauge `rates_provider_quota_used` is updated from this table on each call.

### What counts a token

The token is consumed by **`RatesProvider.FetchBatch` actually making an HTTP call**. Specifically:

- A successful call (any status, including 4xx/5xx — provider charges for the request itself, not for "useful" responses): **consumes one token**.
- A call short-circuited by the bucket being empty: **does not consume a token** (no HTTP made).
- A call short-circuited by future Stage 6 circuit breaker: **does not consume a token** (no HTTP made).
- A timeout where the request was sent but no response received: **counted conservatively as consumed** — we cannot distinguish whether the provider counted it or not.
- An auth failure (`401/403`): **counted as consumed** — the provider already received and processed the request before responding.

This biases toward over-counting, which is the safe direction for quota protection.

### Stage 6: row-contention concern

At very high call rates, a single Postgres row becomes a contention point — every upstream attempt acquires a row-level lock on `upstream_quota`. For our MVP scale (tens of thousands of calls per month), this is invisible. At Enterprise scale (millions per month) and high parallelism, a Stage 6 enhancement would introduce a local in-memory pre-filter that lazily syncs with Postgres, reducing contention. Not implemented in MVP.

## DB resilience

- **Connection pool** — `pgxpool` with size from config (MVP default 25). Acquisition timeout 1s.
- **Per-query timeout** — every query gets a `context.WithTimeout(2s)`. No naked `context.Background()`.
- **Reconnect** — pgx handles dropped connections automatically; pool replaces them.
- **Failed migrations on startup** — service refuses to start, log fatal error. Permanent failure.
- **Slow queries** — query timeout caps the impact. `pg_stat_statements` (enabled at deployment) helps post-hoc.

DB unavailable → `/readyz` returns 503 → load balancer routes traffic away → no requests served on this pod until DB returns.

## Worker resilience

Already covered in `background-mechanism.md`:
- Lease + cleaner for stuck workers.
- Backoff on consecutive errors.

Added here:

**Panic recovery in the worker loop.** A panic processing one job must not kill the worker goroutine.

```go
func (w *Worker) processJob(job Job) {
    defer func() {
        if r := recover(); r != nil {
            obs.Logger(w.ctx).Error(obs.EvWorkerPanic,
                "job_id", job.ID,
                "panic", fmt.Sprint(r),
                "stack", string(debug.Stack()))
            obs.WorkerPanicTotal.Inc()
            // Job lease will expire; cleaner returns it to pending.
            // We do not call Fail here — the panic might be a transient
            // bug that will not recur on retry.
        }
    }()
    // ... actual processing ...
}
```

Panics are logged at `error` level with the full stack and counted as `worker_panic_total`. The job is **not** marked failed; it goes back to `pending` via the cleaner and gets retried. A panic that recurs every time is caught by the per-job retry budget.

## HTTP server resilience

- **Timeouts** as listed above.
- **Body size limit** — `http.MaxBytesReader(w, r.Body, 1<<20)` (1 MiB) on every endpoint that reads bodies.
- **Panic recovery middleware** — catches panics in handlers, logs with stack, returns `500` with the standard error envelope.
- **Concurrency limit** — Go's default `net/http` allows unbounded goroutines; under attack this is a problem. For MVP we accept it; production deploys add a connection limiter (`netutil.LimitListener`) or rely on the gateway in front.

```go
func RecoveryMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if rec := recover(); rec != nil {
                obs.Logger(r.Context()).Error(obs.EvHTTPPanic,
                    "panic", fmt.Sprint(rec),
                    "stack", string(debug.Stack()))
                http.Error(w, `{"error":{"code":"internal","message":"internal error"}}`, 500)
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

The `recovery → request_id → auth → rate_limit → idempotency → handler` order from `monitoring.md` ensures recovery is outermost — even auth/middleware panics are caught.

## Graceful shutdown

Already specified in `background-mechanism.md`. Order on `SIGTERM`:

1. Stop accepting new HTTP requests (close listener, but let in-flight finish).
2. Stop scheduler tick.
3. Stop worker `Reserve` loop (workers finish current jobs).
4. Wait up to 30s for everything to drain.
5. Exit.

A pre-shutdown signal: `/readyz` returns 503 a few seconds **before** the actual shutdown begins, so the load balancer notices and stops sending traffic. Implementation: a `draining` flag flipped early in shutdown; `/readyz` checks it.

```go
func readyHandler(draining *atomic.Bool, ...) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if draining.Load() {
            writeUnready(w, "draining")
            return
        }
        // ... other checks ...
    }
}
```

## Backpressure

Already mentioned in `background-mechanism.md`: `quote_jobs_pending_count` is the operational signal. We do not actively reject `Enqueue` in MVP. If the queue grows uncontrollably, alerting and operator response handle it (scale workers, scale upstream tier, throttle at the gateway).

## Outage playbook (high-level)

Wireframe — concrete runbooks live in the deployment repo when there is one.

| Failure | Detection | First response |
|---|---|---|
| Upstream unreachable | metric `rates_provider_requests_total{outcome="error"}` rising | retries continue with backoff; alert if sustained > N minutes |
| Upstream quota exhausted | `rates_provider_quota_used` ≥ 1.0; `rates_provider_requests_total{outcome="quota_exceeded"}` rising | alert ops; consider tariff upgrade; service returns stale data until next period |
| DB unreachable | `/readyz` fails | LB removes pod from rotation; ops investigates DB |
| Workers falling behind | `quote_jobs_pending_count` rising; throughput unchanged | scale `K`; check for upstream slowness |
| Pod OOM | k8s restarts pod; `up == 0` briefly | investigate memory leak; raise resource limits temporarily |
| All instances stuck | `up == 0` for all | full incident — page on-call |

## RTO and RPO (shape, not numbers)

- **RTO** (Recovery Time Objective) — time to restore service after total outage. Goal: minutes, after Postgres and at least one service instance are reachable. The `quotes` table is rebuildable from the next scheduler tick; no manual intervention needed.
- **RPO** (Recovery Point Objective) — tolerable data loss. The `quotes` table is a cache populated by the upstream; effective RPO is `T` (one scheduler tick of cache age is acceptable). The `quote_jobs` table may lose in-flight rows on disaster restore from backup; clients can retry refresh manually.

Concrete RTO/RPO targets are part of an SLA discussion. Not in MVP.

## AI-agent considerations

This document realises the structural principles from `agent-development.md`:

- **Wrapper-driven defaults** — error classification through `ProviderError.IsTransient()`, not string matching; recovery middleware wraps every handler.
- **Single source of truth** — error class lives in the error type, not in the call site.
- **Mechanical enforcement** — naked `context.Background()` in long-running paths is forbidden by lint rule.

The general rationale lives in `agent-development.md`.

## Not in scope here

- **Capacity sizing.** Concrete values for pool sizes, `K`, lease, timeouts — `capacity.md`.
- **Load and stress testing.** Failure modes are tested via fault injection — see `load-testing.md`.
- **Multi-region failover.** Active-passive across regions — `scaling.md`.
- **Compliance and audit logs.** Different lifecycle and retention; out of MVP scope.
- **Backup and restore strategy.** Deployment concern — lives with infra docs.
- **Concrete runbooks.** Operational artefacts in the deployment repo.
