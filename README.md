# Currency Quote Service

Go HTTP service providing currency exchange rate quotes via an asynchronous refresh pattern. Clients trigger a refresh, receive an `update_id`, and poll for the result. Latest successful quote is also available synchronously per currency.

## API

| Endpoint | Method | Purpose |
|---|---|---|
| `/quotes/refresh` | POST | trigger a background fetch for a currency; returns `update_id` |
| `/quotes/:id` | GET | poll the status of a refresh job |
| `/quotes/latest/:currency` | GET | read the latest successful quote |
| `/healthz` | GET | liveness probe |
| `/readyz` | GET | readiness probe (DB ping + scheduler + worker checks) |
| `/metrics` | GET | Prometheus metrics |
| `/openapi.yaml` | GET | API specification (source of truth) |
| `/docs/` | GET | Swagger UI |

Full contract in [docs/discussions/api-contract.md](docs/discussions/api-contract.md). Spec: [api/openapi.yaml](api/openapi.yaml).

Supported currencies (whitelist): `USD`, `EUR`, `MXN`. Configurable via env.

## Requirements

- Go 1.24+
- Docker and Docker Compose (for running the full stack)
- `make` (Makefile is the entry point for build / test / lint)

External dependencies (run via Docker Compose): Postgres, Prometheus, Grafana, fake rates provider.

## Quick Start

```bash
git clone <repo-url>
cd <repo>
cp .env.example .env
docker compose up
```

The stack starts:
- The service on `:8080`.
- Postgres on `:5432`.
- A fake rates provider on `:8081` (no external API key needed).
- Prometheus on `:9090`.
- Grafana on `:3000` with auto-provisioned dashboards.

Smoke-check:

```bash
curl -X POST http://localhost:8080/quotes/refresh \
     -H 'Content-Type: application/json' \
     -d '{"currency":"EUR"}'
# → 202 Accepted {"id":"abc-..."}

curl http://localhost:8080/quotes/latest/EUR
# → 200 OK {"currency":"EUR","price":...,"updated_at":"..."}
```

## Configuration

All configuration via environment variables. See [.env.example](.env.example) for the full list with descriptions and recommended ranges.

Key variables:

| Variable | Default | Purpose |
|---|---|---|
| `DB_DSN` | `postgres://...` | Postgres connection string |
| `PROVIDER_API_KEY` | (required) | API key for the upstream rates provider |
| `PROVIDER_BASE_URL` | `https://api.currencylayer.com` | upstream rates base URL |
| `SCHEDULER_TICK_SECONDS` | 30 | scheduler interval `T` |
| `COALESCING_WINDOW_SECONDS` | 30 | dedup bucket size `W` (constraint: `W ≤ T`) |
| `WORKER_COUNT` | 1 | number of worker goroutines `K` |
| `WHITELIST_CURRENCIES` | `USD,EUR,MXN` | comma-separated supported currencies |
| `REFRESH_MAX_LATENCY_MS` | 2000 | SLA upper bound on refresh p99 (integer ms; floor 1000) |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

The worker's `pollInterval` and `batchSize` are derived from
`REFRESH_MAX_LATENCY_MS`, `WHITELIST_CURRENCIES`, and `WORKER_COUNT` — not
separate env vars. Effective values are logged at startup as `derived worker
config`. Tariff-plan-specific tuning is in [docs/discussions/capacity.md](docs/discussions/capacity.md).

## Development

```bash
make check         # codegen + git diff check + tests + lint (the CI gate)
make test          # unit tests only (fast)
make test-integration  # integration tests with build tag (testcontainers)
make lint          # golangci-lint
make generate      # regenerate code from api/openapi.yaml
make run           # run the service locally against docker-compose dependencies
make loadtest      # k6 baseline scenario (Stage 6)
```

Development methodology and conventions are in [docs/conventions.md](docs/conventions.md). The full set of design decisions is in [docs/discussions/](docs/discussions/).

## Project Structure

```
cmd/                 service binaries (server, fakeprovider)
internal/            service-private packages (api, queue, worker, scheduler, obs, ratesprovider, ...)
api/                 openapi.yaml + codegen config
deploy/              docker-compose, Grafana dashboards, Prometheus rules
loadtest/            k6 scenarios
migrations/          SQL migrations
docs/                top-level docs + discussions/ (decision records)
testdata/            static test fixtures
```

Full layout and rationale in [docs/architecture.md](docs/architecture.md).

## Documentation

- **For agents (and humans):** start with [AI_CONTEXT.md](AI_CONTEXT.md) — the agent hub.
- **For reviewers:** start with [docs/architecture.md](docs/architecture.md) and [docs/project.md](docs/project.md).
- **For implementers:** [docs/discussions/implementation-roadmap.md](docs/discussions/implementation-roadmap.md) is the build-order checklist.
- **For operators:** [docs/discussions/monitoring.md](docs/discussions/monitoring.md), [docs/discussions/resilience.md](docs/discussions/resilience.md), [docs/discussions/capacity.md](docs/discussions/capacity.md).

## Status

This repository is in the documentation phase. Implementation follows the stages in [docs/discussions/implementation-roadmap.md](docs/discussions/implementation-roadmap.md). See that document for current progress.
