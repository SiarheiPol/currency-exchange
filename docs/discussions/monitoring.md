# Monitoring

## Status
Decided.

## Context

The service has three sources of operational truth that need observation:

1. **HTTP layer** — request rate, latency, errors per endpoint.
2. **Background work** — scheduler ticks, worker iterations, queue depth, retry rate.
3. **Upstream calls** — rates-provider request rate, latency, errors, quota usage.

Each has natural metrics and natural log lines. This document fixes the conventions: what we emit, where, in what shape, and what humans (or automated systems) can rely on for diagnosis.

This document is **not** about resilience (circuit breakers, retries, fallback) — that lives in `resilience.md`. It is about visibility into a running system: would a person on call understand what is happening from the logs and dashboards alone?

In an AI-agent codebase, the same question applies in reverse: an agent reading bug reports cannot interactively debug a production incident; the logs and metrics it sees are all it has.

## Layers

| Layer | Purpose | Tooling |
|---|---|---|
| Structured logs | per-event narrative — what just happened, with context | `log/slog` (stdlib) with JSON handler |
| Metrics | aggregated rates, distributions, gauges over time | Prometheus exposition (`/metrics`) |
| Tracing | end-to-end request flow across service boundaries | request-id propagation in MVP; OpenTelemetry deferred |
| Health endpoints | binary signals for orchestrator and load balancer | `/healthz`, `/readyz` |

## Structured logs

### Format

JSON, one event per line. Stdlib `log/slog` with `slog.NewJSONHandler`. No external logger library.

Fields on every line:
- `time` — RFC3339Nano.
- `level` — `debug` / `info` / `warn` / `error`.
- `msg` — short, lower-case, stable across versions (used for grep and aggregation).
- `request_id` — propagated from `X-Request-Id` (or generated). Mandatory for HTTP-bound logic.
- `service` — fixed string, set at startup.
- `version` — git SHA or release tag, set at startup.

Domain-specific fields (`base`, `quote`, `job_id`, `bucket`, `provider`, `attempts`, `error`) are added by the caller. There are no free-form strings glued into `msg`; details go into structured fields.

Example:
```json
{"time":"2026-04-29T12:00:01.234Z","level":"info","msg":"job completed","request_id":"r-abc","job_id":"j-9b6e","base":"EUR","quote":"MXN","duration_ms":214}
```

### What we log

- **HTTP request boundaries.** One line per request, at `info`. Method, path, status, duration. Server-internal errors at `error`.
- **Job lifecycle.** Reserve, complete, reschedule, fail. One line per transition at `info`. Full payload of failures at `warn`/`error`. Structured fields `base` and `quote` replace the former single `currency` field.
- **Scheduler ticks.** One line per tick at `debug` (high cardinality if `T` is short).
- **Upstream calls.** One line per HTTP call at `info` with provider name, base currency, quote currency list for that base, duration, and status. One log line per base currency (not one per pair). Failures at `warn`.
- **Coalescing collapses.** One line at `debug` when an enqueue joined an existing job.

### What we never log

- API keys, JWTs, full request/response bodies.
- Full caller identity beyond `request_id` and (when present) `caller_key` hash.
- Stack traces at `info`. They go to `error` only.

### Event catalog

All `msg` strings live as constants in `internal/obs/events.go`. There are no string literals at the call site — only constants like `obs.EvJobCompleted`. Renaming or adding a message becomes a one-file change visible in PR review.

```go
package obs

const (
    EvHTTPRequestReceived  = "http request received"
    EvHTTPRequestCompleted = "http request completed"
    EvJobReserved          = "job reserved"
    EvJobCompleted         = "job completed"
    EvJobRescheduled       = "job rescheduled"
    EvJobFailed            = "job failed"
    EvSchedulerTick        = "scheduler tick"
    EvUpstreamCallStarted  = "upstream call started"
    EvUpstreamCallFinished = "upstream call finished"
    EvCoalescingCollapsed  = "coalescing collapsed"
)
```

Hot, recurring events also have **typed helper functions** that fix both the `msg` and the required field set:

```go
func LogJobCompleted(ctx context.Context, jobID, base, quote string, duration time.Duration) {
    Logger(ctx).LogAttrs(ctx, slog.LevelInfo, EvJobCompleted,
        slog.String("job_id", jobID),
        slog.String("base", base),
        slog.String("quote", quote),
        slog.Int64("duration_ms", duration.Milliseconds()),
    )
}
```

Note: this is a discussion doc change showing the intended signature. The actual Go code change is in C1/C2 scope, not C0.

Helpers exist for the most common events (HTTP boundaries, job lifecycle, upstream calls). Rare events (startup banners, panics) call `Logger(ctx).LogAttrs(ctx, level, EvX, ...)` directly with a constant.

### Enforcement

A small helper package (`internal/obs`) exposes `Logger(ctx) *slog.Logger`. The logger pulls `request_id` and other context fields from `context.Context`.

A lint rule (`forbidigo` in `golangci-lint`) rejects:
- Direct calls to `slog.Info`/`Warn`/`Error`/`Debug` outside `internal/obs`.
- String literals as the first argument of any logger method — only `obs.Ev*` constants are allowed.

The right path becomes the only path the linter accepts. AI agents and humans alike cannot accidentally drift into bare `slog.Info("debug stuff")` or one-off literal messages — both produce lint errors at CI gate. To add a new event, the agent must edit `events.go`, which is visible in PR review.

## Metrics

### Endpoint format

Prometheus exposition at `GET /metrics`. Standard `prometheus/client_golang`. No push-gateway, no alternative formats.

### RED metrics for HTTP

For each handler:

| Metric | Type | Labels |
|---|---|---|
| `http_requests_total` | counter | `method`, `path`, `status` |
| `http_request_duration_seconds` | histogram | `method`, `path` |
| `http_in_flight_requests` | gauge | — |

Path label is the **route template** (`/quotes/:id`), not the concrete URL — otherwise label cardinality explodes.

### Background work

| Metric | Type | What it tells us |
|---|---|---|
| `quote_jobs_pending_count` | gauge | Current queue depth. Alert if it grows. |
| `quote_jobs_total` | counter, label `status` | Cumulative by terminal status (`done`, `failed`). |
| `quote_jobs_attempts` | histogram | How many tries jobs needed to reach `done`. |
| `worker_iterations_total` | counter, label `outcome` (`work`, `idle`, `error`) | Worker activity. |
| `scheduler_ticks_total` | counter | Tick count. |
| `scheduler_last_tick_seconds_ago` | gauge | Updated on each tick. Alert if stale. |
| `coalescing_collapsed_total` | counter | Enqueues that joined an existing job. |

### Upstream

| Metric | Type | Labels |
|---|---|---|
| `rates_provider_requests_total` | counter | `provider`, `outcome` (`ok`, `transient`, `permanent`, `quota_exceeded`) |
| `rates_provider_request_duration_seconds` | histogram | `provider` |
| `rates_provider_quota_used` | gauge | `provider`, `period` (`month`) — populated by the rate-limit subsystem. Period `month` is the only billing cycle relevant to apilayer-family plans. |

The quota gauge gives ops an early warning before the upstream returns `quota_exceeded`.

### Naming conventions

- Metric names are constants in `internal/obs/metrics.go`. Never string-literals at call sites. Renaming a metric becomes a single-file change, not a search-and-replace.
- Counters end in `_total`. Histograms end in `_seconds` (durations) or `_bytes`. Gauges have no suffix.
- Labels live in a small set; avoid high-cardinality (no `currency` × `caller` × `path` matrices unless the dashboard needs them).

## Tracing

MVP: request-id propagation, end to end.

- Inbound: read `X-Request-Id`. If missing, generate a UUID. Place on `context.Context`.
- Logs: pulled from context, attached automatically via the helper logger.
- Outbound HTTP (rates provider): set `X-Request-Id` on the outgoing request.
- Response: `X-Request-Id` echoed back to the caller.

Format constraints on the inbound value: `[A-Za-z0-9_-]{1,128}`. Out of bounds → ignore and generate. We never reject a request just for a malformed request-id.

OpenTelemetry tracing (`traceparent`, span hierarchy, exporter to Tempo/Jaeger) is a future extension. The current request-id approach is forward-compatible: when OTel arrives, `traceparent` is parsed alongside `X-Request-Id` and both live on the context.

## Health endpoints

| Endpoint | Status | Body | What it checks |
|---|---|---|---|
| `GET /healthz` | 200 always (if process is responsive) | `{"status":"ok"}` | Liveness — used by orchestrators to decide "kill this pod or not". |
| `GET /readyz` | 200 or 503 | `{"status":"...", "checks":{...}}` | Readiness — used by load balancers to decide "send traffic here or not". |

`/readyz` checks (severity-classified):

**Hard checks** (failure → `503`, pod removed from rotation):
- Postgres reachability (`SELECT 1`). Without a database, the service cannot serve any endpoint.

**Soft checks** (failure → `200` with `degraded` field set, plus an alert via metrics):
- Last scheduler tick within `2 × T`. A stuck scheduler degrades `/quotes/latest?base=BASE&quote=QUOTE` freshness, but the pod can still serve `/quotes/:id` reads, accept `POST /quotes/refresh` (which fills `quotes` itself when worker processes the job), and serve any not-yet-stale `/quotes/latest?base=BASE&quote=QUOTE` data. Removing it from rotation makes the situation worse, not better.
- Worker loop heartbeat (`worker_last_iteration_seconds_ago` < threshold). If the worker is stuck but the scheduler is healthy, `/quotes/latest?base=BASE&quote=QUOTE` continues to work via the scheduler-driven cache. A stuck worker means refresh-driven jobs queue up; that is visible via `quote_jobs_pending_count` and warrants an alert, not pod removal.

Body shape:

```json
{
  "status": "ok",
  "checks": {
    "postgres":  "ok",
    "scheduler": "degraded: last tick 75s ago",
    "worker":    "ok"
  }
}
```

`status` is `ok` (200) when no hard check failed; `fail` (503) when any hard check failed. `degraded` notes appear in the `checks` map regardless. The metric `readyz_degraded_total{check}` increments on each soft-check fail; the standard alert is "degraded for > N minutes".

`/readyz` is **not** authenticated. It exposes the names of internal checks but no values.

## Dashboards (outline)

Three dashboards, one per concern:

- **Service health.** Request rate, error rate, p50/p95/p99 latency per endpoint. In-flight requests. CPU / memory / GC pause via Go runtime metrics.
- **Queue health.** `quote_jobs_pending_count` over time. Job throughput by terminal status. Attempts histogram. Time-to-done.
- **Upstream health.** Provider call rate and latency. Error rate by `outcome`. Quota usage gauges, with budget thresholds.

Concrete Grafana JSON is not in this document — that lives in the deployment repo when we have one.

## Alerts (outline)

Wireframe, not exact thresholds:

- HTTP 5xx rate above `X` over `Y` minutes.
- p99 HTTP latency above `Z` ms over `Y` minutes.
- `scheduler_last_tick_seconds_ago` above `2 × T`.
- `quote_jobs_pending_count` above `whitelist_size × N` (sustained backlog).
- `rates_provider_quota_used` above 80% of plan limit.
- `/readyz` failing for more than `M` minutes.
- `quote_jobs_completion_seconds{source="refresh"}` p99 above `REFRESH_MAX_LATENCY_SECONDS` for 10 minutes (acute SLO breach). Multi-window burn-rate alerts on the same SLI live in the alerting repo — see `SLO and SLI thinking > Job completion SLI`.

Thresholds are operational decisions; they go into the alerting repo with the actual rules.

## SLO and SLI thinking

We define the **shape** of SLOs without committing to numbers (numbers come from production data):

- **Availability SLI.** Successful HTTP responses (2xx, 3xx) over total HTTP responses, excluding 4xx caused by client errors.
- **Latency SLI.** p99 of `http_request_duration_seconds` for the read path (`GET /latest`).
- **Freshness SLI.** Age of the most recent successful row in `quotes` per currency pair, computed as `now() - max(quotes.updated_at)`. The SLO target is the **maximum acceptable staleness**, which depends on three independent factors:
  - `W` (coalescing window) — the floor of refresh-driven update cadence.
  - `T` (scheduler tick) — quiet-traffic cadence.
  - Provider cadence — the external freshness ceiling (Free/Basic = daily, Pro+ = 10min, Business = 60s).
  Realistic SLO target: `2 × T` (one missed tick is acceptable; two is degraded).
- **Job completion SLI.** p99 of the interval from accepted `POST /quotes/refresh` (handler returns `202`) to the job reaching `status=done`, measured per refresh-driven job. SLO target shape: `p99 ≤ REFRESH_MAX_LATENCY_SECONDS` (default 2s; see `capacity.md > Refresh latency SLA`). Scope:
  - Refresh-driven jobs only. Scheduler-driven cache freshness is covered by the freshness SLI above.
  - Jobs that enter retry/backoff because of transient upstream errors are excluded — they fall under the job-success-rate metric, not latency.
  - Coalesced duplicates are counted once, against the first accepted request in their dedup window (a second `POST` in the same bucket sees the existing job id and shares the same in-flight work).

  Underlying metric: `quote_jobs_completion_seconds` (histogram, labelled by `source={refresh,scheduler}`). Implementation contract:
  - Observation = `Complete_at − created_at`, recorded in the worker on the first successful `Complete`. Jobs with `attempts > 0` are excluded from this histogram (they belong to job-success metrics).
  - Histogram buckets must include `REFRESH_MAX_LATENCY_SECONDS` exactly, plus surrounding values for trend visibility. Suggested set: `[0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30]`.
  - The `source` label is set at `Enqueue` time by the producer (handler vs scheduler) and persisted on `quote_jobs` — see `background-mechanism.md > Distinguishing refresh-driven from scheduler-driven jobs`.

  PromQL for the acute SLO check (used by the alert listed in `Alerts (outline)`):

  ```promql
  histogram_quantile(0.99,
      sum by (le) (rate(quote_jobs_completion_seconds_bucket{source="refresh"}[5m])))
  ```

  A multi-window error-budget burn-rate alert (1h fast / 6h slow per SRE workbook) lives in the alerting repo, not the service repo: it is parameterised by the deployment's `REFRESH_MAX_LATENCY_SECONDS` and the chosen 30-day SLO compliance target, both of which are operator decisions.

When we have data, each SLI gets a target and an error budget. For MVP, the metrics behind these SLIs are emitted; the SLO numbers and burn-rate alerts are filled in later.

## AI-agent considerations

This document realises three structural principles from `agent-development.md`:

- **Single source of truth** — `internal/obs/events.go` for log message strings, `internal/obs/metrics.go` for metric names.
- **Wrapper-driven defaults** — `obs.Logger(ctx)` and helper functions (`LogJobCompleted`, etc.) instead of raw `slog.*` calls.
- **Mechanical enforcement** — `forbidigo` lint rule rejects direct `slog.Info`/`Warn`/`Error`/`Debug` outside `internal/obs`.

The general rationale (why these matter when code is written by AI agents and reviewed by humans) lives in `agent-development.md`. Behavioral principles (honest reporting of bad results, CI as source of truth) also live there.

## Not in scope here

- **Concrete Grafana dashboards and alert rules.** Live in the deployment repo.
- **Log aggregation backend** (Loki, ELK, hosted). Implementation choice; the service emits to stdout regardless.
- **OpenTelemetry distributed tracing.** Future extension on top of `X-Request-Id`.
- **Profiling endpoints** (`pprof`). Standard library; can be enabled behind a build tag for debugging.
- **Audit logging for compliance.** Different lifecycle (longer retention, immutable, structured for regulators). Out of scope for MVP.
- **SLO numbers and error-budget policy.** Deferred until production data is available.
