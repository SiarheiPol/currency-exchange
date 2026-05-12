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
| 1 | Upstream unreachable (DNS, connection refused, network partition) | transient |
| 2 | Upstream timeout | transient |
| 3 | Upstream returns 5xx | transient |
| 4 | API code 104 (usage limit reached — monthly quota) | transient (resets at period boundary) |
| 5 | API code 101 (invalid access key) | permanent |
| 6 | API code 102 (inactive account) | permanent |
| 7 | API code 103 (no access to endpoint) | permanent |
| 8 | API code 105 (function access restricted — rare on Free plan; any source empirically allowed) | permanent |
| 9 | API code 106 (no results for query) | permanent |
| 10 | API code 201 (invalid base currency — rare; provider may silently drop instead) | permanent |
| 11 | API code 202 (invalid currency code — rare; provider silently drops invalid codes instead of rejecting, empirically confirmed) | permanent |
| 12 | API code 404 (unknown endpoint) | permanent |
| 13 | Upstream returns `success: false` with unrecognised code | permanent (unknown — fail safe) |
| 14 | Upstream returns malformed JSON (`success` field absent or unparseable) | transient |
| 15 | Database unreachable | transient |
| 16 | Database query slow (timeout) | transient |
| 17 | Panic in handler / worker | transient (per-request) |
| 18 | Process killed (OOM, SIGKILL, hardware) | transient (orchestrator restarts) |
| 19 | Bad configuration on startup | permanent (refuse to start) |

For each, the response is described below. **Transient** means the service retries or routes traffic away and recovers automatically. **Permanent** means human intervention is needed — alert, fail-fast, do not retry.

> Row 14 (malformed JSON) is reclassified **transient** (not permanent as in the original draft). Rationale: a malformed response is more likely a transient upstream glitch (partial response, proxy injection) than a permanent API contract break. If it recurs every retry it is caught by the retry budget. Alert on sustained occurrence.
>
> Row 11 (code 202 — invalid currency code) is listed as "rare" because empirical testing shows that `currencies=ZZZ,EUR` returns `{success:true, quotes:{USDEUR}}` — the invalid code is **silently dropped** rather than triggering code 202. Code 202 is retained in the table for completeness (it can still occur in documented scenarios) but is not the primary error path.

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

When `RatesProvider.FetchPairs` returns an error, the worker maps it to one of two actions in MVP: **reschedule** or **fail**. (Stage 6 adds a third action when the circuit breaker is in place — see the deferred section below.)

| Error | Class | Action |
|---|---|---|
| Connection refused / DNS error | transient | reschedule (backoff) |
| Read/write timeout | transient | reschedule (backoff) |
| HTTP `502/503/504` | transient | reschedule (backoff) |
| `success: false`, API code 104 (quota) | transient | reschedule far ahead (`now + 1h`); alert ops |
| `success: false`, API codes 101/102/103/105/106 (auth, access) | permanent | fail job; alert ops |
| `success: false`, API codes 201/202/404 (invalid query) | permanent | fail job; log full response |
| `success: false`, unrecognised code | permanent | fail job; log for diagnosis |
| `success: true`, requested pair absent from response (silent drop) | pair appears in `FetchResult.Missing` | treat as permanent for that pair; fail job |
| Malformed JSON or `success` field absent | transient | reschedule (backoff) |
| Unknown 4xx at HTTP level | permanent | fail job; log full response for diagnosis |

> **Detection of `success: false`**: the upstream returns `success: false` with HTTP 200, not with a 4xx/5xx status. The client must inspect the body, not the HTTP status code, to detect these failures. `ProviderError.APICode` carries the numeric API error code for diagnosis and classification.

The classification is implemented in `RatesProvider`'s error type:

```go
type ProviderError struct {
    Code     string  // "transient" | "permanent" | "quota_exceeded"
    HTTPCode int     // HTTP status code, or zero if failure was not HTTP-level
    APICode  int     // upstream API error code (e.g. 101, 104, 202), or zero
    Message  string
    Cause    error
}

func (e *ProviderError) Error() string {
    s := fmt.Sprintf("provider error [%s]", e.Code)
    if e.HTTPCode != 0 {
        s += fmt.Sprintf(" http=%d", e.HTTPCode)
    }
    if e.APICode != 0 {
        s += fmt.Sprintf(" api_code=%d", e.APICode)
    }
    s += ": " + e.Message
    if e.Cause != nil {
        s += ": " + e.Cause.Error()
    }
    return s
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

**Design sketch for Stage 6:** standard three-state machine (closed / open / half-open) with `failure_threshold = 5`, `cooldown = 30s`, single half-open probe. Library `github.com/sony/gobreaker` or equivalent, wraps `RatesProvider.FetchPairs`. State is per-instance — no cross-instance coordination needed. Metric `rates_provider_breaker_state{provider,state}` is added when the breaker is wired in.

Subtle: **not all errors should trip the breaker.** `429` (rate limit), `quota_exceeded`, and `permanent` errors must be excluded — they are not symptoms of upstream sickness, and tripping the breaker on them only worsens the situation.

## Multi-provider fallback (deferred — Stage 6)

Not implemented in MVP. We support a single rates provider configured via env. Single-provider failure equals service degradation; the mitigation is alerting on `rates_provider_requests_total{outcome="transient"}` and operator response.

The architecture supports extension: `RatesProvider` is an interface, and a chain wrapper would walk a list of providers, falling through to the next on transient errors. Sketch for Stage 6 reference:

```go
func (c *chainProvider) FetchPairs(ctx context.Context, pairs []Pair) (FetchResult, error) {
    var lastErr error
    for _, p := range c.providers {
        result, err := p.FetchPairs(ctx, pairs)
        if err == nil {
            return result, nil
        }
        lastErr = err
        var pe *ProviderError
        if errors.As(err, &pe) && !pe.IsTransient() {
            return FetchResult{}, err  // permanent → do not try fallback
        }
    }
    return FetchResult{}, lastErr
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

The quota gauge `rates_provider_quota_used` is updated from this table on each call (planned — Stage 6).

### What counts a token

The token is consumed by **`RatesProvider.FetchPairs` actually making an HTTP call**. Specifically:

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
| Upstream unreachable | metric `rates_provider_requests_total{outcome="transient"}` rising | retries continue with backoff; alert if sustained > N minutes |
| Upstream quota exhausted | `rates_provider_quota_used` ≥ 1.0 (planned — Stage 6); `rates_provider_requests_total{outcome="quota_exceeded"}` rising | alert ops; consider tariff upgrade; service returns stale data until next period |
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
