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

- [ ] Migration: `quote_jobs` table — see `background-mechanism.md`
- [ ] Migration: `quotes` table — see `background-mechanism.md`
- [ ] Indexes: `quote_jobs_pending_idx`, `quote_jobs_dedup_key_uidx`
- [x] `JobQueue` interface — see `background-mechanism.md`
- [x] `Clock` and `IDGenerator` interfaces with real + fake implementations — see `testing-strategy.md`
- [x] `memQueue` implementation
- [x] `JobQueue` contract test suite (runs against `memQueue` first, will rerun against `pgQueue`)
- [ ] `pgQueue` implementation (`FOR UPDATE SKIP LOCKED`, lease, cleaner)
- [ ] Schema-per-test isolation helper for integration tests — see `testing-strategy.md`
- [x] Backoff math (exponential + jitter, capped at 60s)
- [ ] Worker loop skeleton (`Reserve` → process placeholder → `Complete`/`Reschedule`/`Fail`)

---

## Stage 2 — Observability foundation

Logging, metrics, health endpoints. Lands before HTTP handlers because handlers will use `obs.Logger(ctx)` from day one.

- [ ] `internal/obs/log.go` — `Logger(ctx) *slog.Logger`
- [ ] `internal/obs/events.go` — `Ev*` message constants — see `monitoring.md`
- [ ] `internal/obs/helpers.go` — typed helper functions (`LogJobCompleted`, etc.)
- [ ] `internal/obs/metrics.go` — Prometheus metric constants and registrations
- [ ] `request_id` propagation middleware (read header → context → outbound + echo)
- [ ] **Enable `forbidigo` rule** in `.golangci.yml`: forbid `slog.(Debug|Info|Warn|Error)` outside `internal/obs/`
- [ ] Prometheus `/metrics` endpoint via `prometheus/client_golang`
- [ ] `/healthz` (always 200)
- [ ] `/readyz` with DB ping + scheduler-staleness check + worker heartbeat
- [ ] Retrofit `pgQueue`, `memQueue`, contract tests with logger and metrics

---

## Stage 3 — Producers and rates provider

Refresh-path code, scheduler, real upstream client.

- [ ] `RatesProvider` interface (`FetchBatch` only) — see `background-mechanism.md`
- [ ] `fakeRatesProvider` for tests (configurable map + error injection)
- [ ] `exchangerateHostProvider` (real implementation; `httptest`-based unit tests)
- [ ] Worker loop calling `FetchBatch` and upserting `quotes` in one transaction
- [ ] Scheduler component + bootstrap-on-startup tick
- [ ] Coalescing: `dedup_key = sha256("<currency>:<bucket>")` on both producers
- [ ] Job lifecycle wiring: `pending` → `running` → `done` | `failed` (via `Reschedule` retry budget)
- [ ] All upstream call paths emit metrics + logs through `internal/obs`
- [ ] Coalescing-collapse counter incremented on `Enqueue` conflicts

---

## Stage 4 — API surface

HTTP handlers, OpenAPI spec, contract enforcement.

- [ ] `api/openapi.yaml` — full spec for 3 endpoints (request/response/errors/headers) — see `api-contract.md`
- [ ] `oapi-codegen` wired in `go generate` — see `openapi.md`
- [ ] `internal/api/oapi_gen.go` checked in
- [ ] `make check` includes `git diff --exit-code` after `go generate`
- [ ] Handler implementations satisfying generated `ServerInterface`
- [ ] Currency validation (format → whitelist, two-step) — see `api-contract.md`
- [ ] `Cache-Control` + `ETag` for `GET /quotes/:id` (per-status) and `GET /latest` (`max-age=W`)
- [ ] Conditional GET handling (`If-None-Match` → `304`)
- [ ] Error envelope with stable `code` field
- [ ] `kin-openapi` runtime validation middleware in dev/test profiles only
- [ ] Handler tests covering the full edge case matrix from `api-contract.md`
- [ ] Swagger UI at `/docs/`, raw spec at `/openapi.yaml` (both embedded)

---

## Stage 5 — Packaging and operability

Run-from-zero story for reviewers and operators.

### Service packaging

- [ ] `Dockerfile` (multi-stage, distroless or scratch final)
- [ ] Configuration via env vars (DSN, `T`, `W`, provider URL, provider key, `K`, etc.)
- [ ] Graceful shutdown wired in order: HTTP → scheduler → worker → exit
- [ ] `README.md` with build/run instructions, env vars, endpoints

### Fake rates provider

A standalone binary that imitates `exchangerate.host`. Lets reviewers run the service end-to-end without a paid API key, and lets us simulate plans we cannot buy (Enterprise). Reused in Stage 6 for load testing.

- [ ] `cmd/fakeprovider/main.go` — separate binary, listens on its own port
- [ ] HTTP-compatible with `exchangerate.host`: `/live` endpoint, query params (`access_key`, `currencies`, `source`), response shape (`{success, timestamp, source, quotes}`)
- [ ] Plan simulation via flags / env vars: monthly quota, update cadence, optional latency injection
- [ ] Random-walk rate generation with deterministic seed (reproducible runs)
- [ ] **In-memory state only** — restart resets quota counter and current rates
- [ ] Minimal contract tests (response shape, quota counting, cadence respected)

### Compose stack

- [ ] `docker-compose.yml` — service + Postgres + Prometheus + Grafana + fake provider
- [ ] Service wired against the fake provider **by default**; real upstream requires explicit env override
- [ ] Grafana dashboards auto-provisioned from `deploy/grafana/dashboards/*.json`:
  - `service-health.json` — RED metrics, runtime stats
  - `queue-health.json` — pending count, throughput, attempts, scheduler/worker liveness
  - `upstream-health.json` — provider rate, latency, error breakdown, quota usage
- [ ] Prometheus alert rules auto-provisioned from `deploy/prometheus/rules.yaml`, covering the alerts listed in `monitoring.md`

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
- **Load and stress tests** — uses the fake rates provider from Stage 5 to simulate Enterprise-tier quota and update cadence. See `load-testing.md`.

---

## Cross-cutting reminders

Things that are easy to forget because they sit between stages:

- Every PR runs `make check`. If `make check` is missing a step we need, fix it before adding the new code that exposes the gap.
- New log events go into `internal/obs/events.go` first; helper-function-or-not is a per-event call.
- New metrics names go into `internal/obs/metrics.go` first; never as string literals.
- New endpoints go into `api/openapi.yaml` first; handler signature follows from regeneration.
- New configurable parameters land in `.env.example` and the `README.md` config section in the same PR.
- Local development and `docker-compose up` run against the fake rates provider; the real upstream requires explicit env override (Stage 5).
