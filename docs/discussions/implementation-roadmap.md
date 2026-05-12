# Implementation roadmap

Cross-cutting timing for decisions made in other documents in this folder. This is a checklist, not a decision record. Each item links back to the discussion doc where the decision was made.

## Conventions

- Development style is **TDD**: tests are written first; each checklist item implies "tests + code".
- Stages are logical groupings, not strict gates. They can overlap in practice but the recommended order is top-to-bottom.
- Items inside a stage are also ordered by recommended sequence (top first).
- A stage is "done" when all items are done **and** `make check` passes on the new code.

This document is updated whenever a discussion doc adds or changes a decision. Items move between stages or get checked off as PRs land.

---

## Stage 0 — Scaffold

The minimum to have `make check` working on an empty project.

- [x] `go.mod` with Go 1.24+
- [x] `Makefile` with targets: `check`, `test`, `lint`, `generate`, `run`
- [x] `.golangci.yml` with default linters (`errcheck`, `govet`, `gofmt`, `staticcheck`, `gosimple`, `unused`, `ineffassign`)
- [x] CI pipeline running `make check` on PR (GitHub Actions / GitLab CI)
- [x] Postgres migrations tool integrated (`golang-migrate` or equivalent)
- [x] Empty package skeleton: `cmd/`, `internal/`, `api/`
- [x] `.env.example` for local config

The `forbidigo` rule for `slog.*` is **not** enabled here — there is no `internal/obs` to redirect to yet. It lands in Stage 2.

---

## Stage 1 — Core domain

Queue, schemas, worker mechanics. No HTTP yet, no observability yet.

### Stage 1 amendments (pair-based pivot — C0/C1)

- [x] Plan change: pair-based currency model + apilayer-family provider (docs rewrite — C0)
- [x] Schema rework: `quote_jobs` and `quotes` tables to pair-based columns (C1)
- [x] `Job` struct rework: `Base, Quote string` fields replacing the old single-currency field (C1)
- [x] `pgQueue`, `memQueue`, contract tests updated to pair-based `Job` (C1)
- [x] `obs/helpers.go` log helpers: `currency string` → `base, quote string` (C1)

- [x] Migration: `quote_jobs` table — see `background-mechanism.md`
- [x] Migration: `quotes` table — see `background-mechanism.md`
- [x] Indexes: `quote_jobs_pending_idx`, `quote_jobs_dedup_key_uidx`
- [x] `JobQueue` interface — see `background-mechanism.md`
- [x] `Clock` and `IDGenerator` interfaces with real + fake implementations — see `testing-strategy.md`
- [x] `memQueue` implementation
- [x] `JobQueue` contract test suite (runs against `memQueue` first, will rerun against `pgQueue`)
- [x] `pgQueue.Enqueue` (`INSERT ON CONFLICT`, dedup)
- [x] `pgQueue.Reserve` + `Complete` + `Reschedule` + `Fail` (`FOR UPDATE SKIP LOCKED`, lease)
- [x] `pgQueue` cleaner (lease expiry recovery)
- [x] Schema-per-test isolation helper for integration tests — see `testing-strategy.md`
- [x] Backoff math (exponential + jitter, capped at 60s)
- [x] Worker loop skeleton (`Reserve` → process placeholder → `Complete`/`Reschedule`/`Fail`)

---

## Stage 2 — Observability foundation

Logging, metrics, health endpoints. Lands before HTTP handlers because handlers will use `obs.Logger(ctx)` from day one.

- [x] `internal/obs/log.go` — `Logger(ctx) *slog.Logger`
- [x] `internal/obs/events.go` — `Ev*` message constants — see `monitoring.md`
- [x] `internal/obs/helpers.go` — typed helper functions (`LogJobCompleted`, etc.)
- [x] `internal/obs/metrics.go` — Prometheus metric constants and registrations
- [x] `request_id` propagation middleware — inbound + echo (outbound deferred to Stage 3)
- [x] **Enable `forbidigo` rule** in `.golangci.yml`: forbid `slog.(Debug|Info|Warn|Error)` outside `internal/obs/`
- [x] Prometheus `/metrics` endpoint via `prometheus/client_golang`
- [x] `/healthz` (always 200)
- [x] `/readyz` with DB ping + scheduler-staleness check + worker heartbeat
  - [x] handler skeleton + `Checker` interface + body envelope
  - [x] `PostgresChecker` (hard, via `Ping(ctx)`)
  - [x] `WorkerChecker` (soft, via `Worker.LastIteration` heartbeat)
  - [x] mounted in `cmd/server` with real `pgxpool.Pool` + running `worker.Worker`
  - [x] `SchedulerChecker` — deferred to Stage 3 alongside the scheduler component itself
- [x] Retrofit `pgQueue`, `memQueue`, contract tests with logger and metrics

---

## Stage 3 — Producers and rates provider

Refresh-path code, scheduler, real upstream client.

- [x] `RatesProvider` interface (`FetchPairs`, `Pair` type, `FetchResult` keyed by `Pair`, `ProviderError` with `APICode`) — amended in C2 to pair-based shape; amended in C3 to replace `Errors map` with `Missing []Pair`; see `background-mechanism.md`, `resilience.md`, and `fetchresult-missing-pairs.md`
- [x] `FetchResult.Missing` refactor: replace `Errors map[Pair]*ProviderError` with `Missing []Pair` in `provider.go`; update interface godoc — see `fetchresult-missing-pairs.md`
- [x] `fakeRatesProvider` for tests (three test patterns: success / batch-failure / partial-success with missing-pair detection)
- [x] `apilayerProvider` (real implementation; per-base HTTP grouping; `httptest`-based unit tests) — see `apilayer-spec.md` for endpoint, response shapes, and error-code mapping
- [x] Worker loop calling `FetchPairs` and upserting `quotes(base, quote)` in one transaction
- [x] Scheduler component + bootstrap-on-startup tick; iterates over all ordered pairs from whitelist
- [x] Coalescing: `dedup_key = sha256(UPPER(base) + ":" + UPPER(quote) + ":" + bucket_unix_seconds)` on both producers
- [x] Job lifecycle wiring: `pending` → `running` → `done` | `failed` (via `Reschedule` retry budget)
- [x] Startup probe: `FetchPairs([{USD,EUR}])` parses `success` boolean in response body
- [x] Missing-pair detection in `apilayerProvider`: pairs absent from upstream response populate `FetchResult.Missing`
- [x] All upstream call paths emit metrics + logs through `internal/obs`
- [x] Coalescing-collapse counter incremented on `Enqueue` conflicts

---

## Stage 4 — API surface

HTTP handlers, OpenAPI spec, contract enforcement.

- [x] `api/openapi.yaml` — full spec for 3 endpoints (request/response/errors/headers) — see `api-contract.md`
- [x] `oapi-codegen` wired in `go generate` — see `openapi.md`
- [x] `internal/api/oapi_gen.go` checked in
- [x] `make check` includes `git diff --exit-code` after `go generate`
- [x] `httpmw.PanicRecover` middleware — recovers panics in HTTP handlers and goroutine wrappers; returns 500 error envelope; logs `EvPanicRecovered` with request_id, panic value, and stack — see `api-contract.md` (row 23) and `monitoring.md`
- [x] `httpmw.Metrics` middleware — records `HTTPRequestsTotal`, `HTTPRequestDuration`, `HTTPRequestsInFlight` per handler — see `monitoring.md`
- [x] Handler implementations satisfying generated `ServerInterface`
- [x] Pair validation (format → whitelist → self-pair check, three-step) — see `api-contract.md`
- [x] Denormalize completed quote (`price`, `quote_updated_at`) into `quote_jobs`; `JobQueue.Complete` signature carries the snapshot so `GET /quotes/:id` for `done` jobs is genuinely immutable — see `background-mechanism.md > quote_jobs` and the `done_has_quote` invariant
- [x] Drop `attempts` field from `JobStatusPending` and `JobStatusFailed` response bodies — internal retry counter, not actionable for clients (see `api-contract.md > GET /quotes/:id`)
- [x] Trim `/quotes/:id` response bodies to ТЗ-aligned shape: drop `created_at` and `completed_at` from `pending`/`done`, drop `base`/`quote` from `pending`, flatten `failed.error` from `{code, message}` to a plain string — see `api-contract.md > GET /quotes/:id`
- [x] `Cache-Control` + `ETag` for `GET /quotes/:id` (per-status) and `GET /quotes/latest?base=BASE&quote=QUOTE` (`max-age=W`)
- [x] Conditional GET handling (`If-None-Match` → `304`)
- [x] Error envelope with stable `code` field
- [x] `kin-openapi` runtime validation middleware in dev/test profiles only
- [x] Handler tests covering the full edge case matrix from `api-contract.md` — rows 1-9 via `TestRefreshQuote_Validation`, rows 10-15 via `TestGetQuoteJob_RealHandler_*`, rows 16-22 via `TestGetLatestQuote_*` + `TestGetLatestQuote_Validation`, row 23 via `TestPanicRecover`, row 24 via `TestRequestID`. End-to-end manually verified across all 24 rows.
- [x] Swagger UI at `/docs/`, raw spec at `/openapi.json` (both embedded)
- [x] `cmd/server` cleanup: replace raw log-message literals with `Ev*` constants; use `context.WithoutCancel` in shutdown log calls; extract `gracefulShutdown` helper with uniform timeouts; fix stale package doc comment
- [x] Worker outcome relabel: replace `outcome="ok"` with `outcome="idle"/"work"` on poll branch per `capacity.md`

---

## Stage 4.5 — Refresh latency SLA enforcement

Closes the implementation gap exposed when documenting `REFRESH_MAX_LATENCY_MS` (see `api-contract.md > POST /quotes/refresh > Latency contract` and `capacity.md > Refresh latency SLA`). The current `worker.go` defaults (`batchSize=1`, `pollInterval=5s`) cannot meet a 2s SLA by construction, and the per-job loop in `dispatch` negates the per-base batching that `apilayerProvider.FetchPairs` already implements.

- [x] `cmd/server/config.go` — read `REFRESH_MAX_LATENCY_MS` (integer milliseconds; default `2000`); refuse to start when `REFRESH_MAX_LATENCY_MS < 1000` (the sum `upstream_p99 + db_p99 + margin`) per `capacity.md > Refresh latency SLA > Startup validation`
- [x] `cmd/server/config.go` — derive `pollInterval` and `batchSize` from the SLA + whitelist + `WORKER_COUNT`; log the effective values at startup (`derived worker.poll_interval=…, batch_size=…`)
- [x] `internal/worker/worker.go` — in the `Reserve` loop, **group reserved jobs by `Base`** before calling `FetchPairs`, then dispatch per-pair results from the batched response. Replaces the current per-job slice-of-one call. Matches the design in `background-mechanism.md > Lifecycle` step 3.
- [x] `cmd/server/main.go` — pass derived options to `worker.New(...)` via `WithPollInterval` / `WithBatchSize`; remove hardcoded defaults from `New` once env is the single source of truth
- [x] **Job completion SLI plumbing** — covers schema, producer, worker, and metric in one consistent change:
  - `migrations/` — add column `source TEXT NOT NULL` to `quote_jobs` (allowed values: `refresh`, `scheduler`); enforce via `CHECK`
  - `internal/queue/` — `Job.Source` field on the type and on `Enqueue`; producer sets it (`refresh` in the `POST` handler, `scheduler` in `Tick`)
  - `internal/obs/metrics.go` — register `quote_jobs_completion_seconds` histogram with label `source`; buckets `[0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30]` (must include `REFRESH_MAX_LATENCY_MS / 1000` exactly as a bucket boundary; the default `2000ms → 2s` is already in the suggested set)
  - `internal/worker/worker.go` — observe `Complete_at − created_at` on first successful `Complete` only; jobs with `attempts > 0` are NOT observed (they belong to job-success metrics)
  - Acute alert `quote_jobs_completion_seconds{source="refresh"}` p99 > `REFRESH_MAX_LATENCY_MS / 1000` for 10m lives in the alerting repo; multi-window burn-rate alerts also there — see `monitoring.md > Alerts (outline)` and `monitoring.md > SLO and SLI thinking > Job completion SLI`
- [x] `.env.example` and `README.md` — document `REFRESH_MAX_LATENCY_MS` (integer milliseconds; default `2000`), note `pollInterval`/`batchSize` are derived

---

## Stage 5 — Packaging and operability

Run-from-zero story for reviewers and operators.

### Service packaging

- [x] `Dockerfile` (multi-stage, distroless or scratch final)
- [x] Configuration via env vars (DSN, `T`, `W`, provider URL, provider key, `K`, etc.)
- [x] Graceful shutdown wired in order: HTTP → scheduler → worker → exit
- [x] `README.md` with build/run instructions, env vars, endpoints

### Fake rates provider

A standalone binary that imitates the apilayer-family (currencylayer). Lets reviewers run the service end-to-end without a paid API key, and lets us simulate plans we cannot buy (Enterprise). Reused in Stage 6 for load testing.

- [x] `cmd/fakeprovider/main.go` — separate binary, listens on its own port
- [x] HTTP-compatible with apilayer-family (currencylayer): `/live` endpoint, query params (`access_key`, `currencies`, `source`), response shape (`{success, timestamp, source, quotes}` with `success: false` error shape `{success: false, error: {code, info}}`)
- [x] Plan simulation via flags / env vars: monthly quota, update cadence, optional latency injection
- [x] Random-walk rate generation with deterministic seed (reproducible runs)
- [x] **In-memory state only** — restart resets quota counter and current rates
- [x] Minimal contract tests (response shape, quota counting, cadence respected)

### Compose stack

- [x] `docker-compose.yml` — service + Postgres + Prometheus + Grafana + fake provider
- [x] Service wired against the fake provider **by default**; real upstream requires explicit env override
- [x] Grafana dashboards auto-provisioned from `deploy/grafana/dashboards/*.json`:
  - `service-health.json` — RED metrics, runtime stats
  - `queue-health.json` — pending count, throughput, attempts, scheduler/worker liveness
  - `upstream-health.json` — provider rate, latency, error breakdown, quota usage
- [x] Prometheus alert rules auto-provisioned from `deploy/prometheus/rules.yaml`, covering the alerts listed in `monitoring.md`

### Latency SLA empirical validation

With the fake provider's latency injection in place, the SLA from Stage 4.5 becomes testable end-to-end:

- [ ] End-to-end timing tests covering the four cases: SLA on healthy upstream, SLA under injected jitter, SLA under transient errors (rescheduled jobs must be excluded from the SLI per `monitoring.md`), SLA under coalesced bursts (multiple `POST /quotes/refresh` in one bucket)
- [ ] **Revisit response headers** for `POST /quotes/refresh` and `GET /quotes/:id`: based on the measured p99 from the previous item, decide whether `Retry-After`, `X-Refresh-Eta-Seconds`, or other latency hints add real value for clients. If yes, update `api/openapi.yaml` and `api-contract.md` in the same PR.
- [ ] Replace the proposed per-tariff defaults in `capacity.md > Refresh latency SLA` with measured values, and remove the "default 5s" relaxation for Free/Basic if HTTP latency in practice does not differ from Pro+/Business

### Optional

- [ ] Smoke test against real upstream behind `//go:build smoke`

---

## Stage 6 (post-MVP)

These are designed but explicitly deferred. Each becomes a stage on its own when prioritised.

- **Classical idempotency** (`Idempotency-Key` header, `idempotency_keys` table, replay middleware) — see `idempotency.md`.
- **Production-on `kin-openapi` runtime validation** — see `openapi.md`.
- **Tariff-plan-specific tuning** of `T`, `W`, `K`, lease, poll interval — see `capacity.md`.
- **Circuit breaker + multi-provider fallback** — see `resilience.md`.
- **OpenTelemetry distributed tracing** alongside `X-Request-Id` — see `monitoring.md`.
- **Property-based testing** for bucket math and dedup logic — see `testing-strategy.md`.
- **Load and stress tests** — uses the fake rates provider from Stage 5 to simulate Enterprise-tier quota and update cadence. See `load-testing.md`. Tracked sub-items:
  - [x] Load-test infrastructure: k6 service in `docker-compose.yml` under the `loadtest` profile, `loadtest/` scripts directory with shared `common.js`, Makefile targets
  - [x] Profile 1 smoke (sustained baseline): 50 RPS mixed `GET /quotes/latest` + `POST /quotes/refresh`, 80/20 ratio, 30 s default with override env, threshold pass/fail
  - [x] Profile 4 smoke (coalescing stress): 100 VU burst on a single pair, assert `rates_provider_requests_total` delta ≤ 2 by parsing `/metrics`
  - [x] Profile 2 smoke (read storm): `GET /quotes/latest` at high RPS, scalability check
  - [x] Profile 3 smoke (refresh burst): sustained `POST /quotes/refresh` across distinct pairs, queue drain
  - [x] Profile 5 (failure injection): fake-provider latency + error modes, resilience check
  - [ ] `DB_POOL_MAX_CONNS` env var: expose pgxpool max-conns as an operator tunable (default 25, matching `capacity.md`); wire via `pgxpool.ParseConfig` + `NewWithConfig`; log effective value at startup alongside `EvPostgresConnected`; document in `.env.example` and README
  - [ ] Dashboard + alert validation pass: verify `monitoring.md` panels render correctly under load, tune alert thresholds against measured baselines
  - [ ] Capacity measurements: replace `capacity.md` estimates with measured p99 values

---

## Cross-cutting reminders

Things that are easy to forget because they sit between stages:

- Every PR runs `make check`. If `make check` is missing a step we need, fix it before adding the new code that exposes the gap.
- New log events go into `internal/obs/events.go` first; helper-function-or-not is a per-event call.
- New metrics names go into `internal/obs/metrics.go` first; never as string literals.
- New endpoints go into `api/openapi.yaml` first; handler signature follows from regeneration.
- New configurable parameters land in `.env.example` and the `README.md` config section in the same PR.
- Local development and `docker-compose up` run against the fake rates provider; the real upstream requires explicit env override (Stage 5).
