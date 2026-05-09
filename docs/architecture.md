# Architecture

Top-level summary. Detailed reasoning lives in `docs/discussions/`.

## Package Layout

```
cmd/
  server/main.go              # service entry point
  fakeprovider/main.go        # fake rates provider for dev/load testing (Stage 5)
internal/
  api/                        # HTTP handlers, OpenAPI-generated types, middleware
  domain/                     # core types: Quote, Job, Currency
  queue/
    pgqueue/                  # Postgres implementation of JobQueue
    memqueue/                 # in-memory fake for unit tests
    contract_test.go          # shared contract suite, runs against both
  worker/                     # worker loop: Reserve → FetchPairs → Complete
  scheduler/                  # tick-driven producer that enqueues jobs
  ratesprovider/
    apilayer/                 # real upstream client (apilayer-family: currencylayer, fixer, exchangeratesapi.io)
    fake/                     # in-process fake for unit tests
  obs/                        # logging, metrics, request-id propagation
  config/                     # env-var loading
api/
  openapi.yaml                # API contract (source of truth)
  oapi-codegen.yaml           # codegen config
deploy/
  grafana/dashboards/         # auto-provisioned dashboards
  prometheus/rules.yaml       # auto-provisioned alert rules
loadtest/                     # k6 scenarios
migrations/                   # SQL migrations (golang-migrate compatible)
testdata/                     # static test fixtures
docker-compose.yml            # service + Postgres + Prometheus + Grafana + fake provider
.env.example                  # documented env var defaults
Dockerfile
Makefile
.golangci.yml
README.md
```

`internal/` enforces the boundary — nothing in `internal/` is importable from outside the module. The `api/openapi.yaml` and migration files are the only artefacts intentionally shared with operators/clients.

## Key Design Decisions

Each decision is summarised here in one paragraph and pointed at its source document.

**Asynchronous refresh pattern.** `POST /quotes/refresh` returns an `update_id` immediately; the actual upstream fetch happens in a worker. Clients poll `GET /quotes/:id` for completion. See `discussions/api-contract.md`.

**Postgres-backed queue with `FOR UPDATE SKIP LOCKED`.** `quote_jobs` table holds operational state. Workers poll the table; concurrent workers across instances do not collide. The queue is multi-instance-ready from MVP. See `discussions/background-mechanism.md`.

**Worker is the sole writer of `quotes`.** All upstream fetches go through the worker; no other component writes to `quotes`. Eliminates two-writer race conditions. See `discussions/background-mechanism.md`.

**Coalescing on `(base, quote, bucket)`.** Both producers (refresh handler and scheduler) compute the same `dedup_key = sha256(UPPER(base) + ":" + UPPER(quote) + ":" + bucket_unix_seconds)`. The unique partial index collapses concurrent enqueues into one job. Bucket size `W` caps the rate of upstream-driven work per pair; actual data freshness is also bounded by provider cadence and failure recovery, see `discussions/idempotency.md`.

**Two independent dials: `T` and `W`.** `T` (scheduler tick) sets the quiet-traffic refresh target; `W` (coalescing window) sets the upstream cost ceiling. Constraint `W ≤ T`. Concrete defaults per tariff plan in `discussions/capacity.md`.

**Spec-first API with `oapi-codegen`.** `api/openapi.yaml` is the source of truth. Go handler interfaces are generated from it; drift is impossible because the compiler enforces match. See `discussions/openapi.md`.

**Spec-first + lint-driven observability.** `internal/obs` exposes `Logger(ctx)` and metric constants. `forbidigo` rejects raw `slog.*` calls outside the package. Log message strings live as constants in `events.go`. See `discussions/monitoring.md`.

**TDD with seam interfaces.** `JobQueue`, `RatesProvider`, `Clock`, `IDGenerator`, `QuoteRepo` all have real and fake implementations. Unit tests use fakes; integration tests use real with `testcontainers`. See `discussions/testing-strategy.md`.

**Schema-per-test isolation.** Integration tests create their own Postgres schema and drop it on teardown. Tx-rollback is insufficient because we test `FOR UPDATE SKIP LOCKED` semantics across transactions. See `discussions/testing-strategy.md`.

**Single rates provider, multi-provider as Stage 6.** MVP supports one provider via `RatesProvider` interface. Chain-of-providers wrapper is designed but deferred. Circuit breaker also Stage 6 — MVP relies on timeouts + retry budget + token bucket. See `discussions/resilience.md`. The real provider implementation (`apilayerProvider`) issues one HTTP call per unique base currency within a `FetchPairs` batch.

**Token bucket for upstream quota in Postgres, not in memory.** Multi-instance deployments share the bucket via atomic SQL updates. Restarts do not reset the count. See `discussions/resilience.md`.

**AI-agent-friendly structural enforcement.** `make check` is the single quality gate (codegen + diff + tests + lint). Every "remember to do X" is replaced by tooling, helper, or single-source-of-truth file. See `discussions/agent-development.md`.

## Data Model

Three tables in MVP:

| Table | Purpose | Writer |
|---|---|---|
| `quote_jobs` | operational state of refresh jobs (`pending` / `running` / `done` / `failed`) | refresh handler, scheduler, worker |
| `quotes` | latest successful rate per currency pair | worker only |
| `upstream_quota` | monthly quota counter per provider | rates provider on each call |

A fourth table, `idempotency_keys`, is designed in `discussions/idempotency.md` for Stage 6 (classical `Idempotency-Key` header support) but not implemented in MVP.

Full schemas in `discussions/background-mechanism.md` and `discussions/idempotency.md`.

## What to Look At First

For a new contributor (human or AI agent), this is the recommended reading order:

1. **`discussions/agent-development.md`** — development methodology and principles. Sets expectations for everything else.
2. **`docs/discussions/implementation-roadmap.md`** — checklist of what has been built and what is next.
3. **`api/openapi.yaml`** — the API contract.
4. **`docs/discussions/api-contract.md`** — explanations behind the contract.
5. **`docs/discussions/background-mechanism.md`** — queue, scheduler, worker.
6. **`docs/discussions/monitoring.md`** — observability conventions.

Other discussion docs are reference material, read on demand.

## Cross-cutting principles

- **Discipline → automation.** Anything that requires "remember to do X" is enforced by tooling. See `discussions/agent-development.md`.
- **Single source of truth.** Names, contracts, configurations have one definition site. Drift is impossible.
- **`make check` is the gate.** Codegen + git-diff-exit-code + tests + lint. CI runs the same.
- **Local development matches docs.** `docker-compose up` runs the full stack against the fake rates provider; no real API key required.
