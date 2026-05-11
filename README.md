# Currency Quote Service

A REST service that returns current exchange rates between whitelisted currency pairs, refreshing in the background from an external rates provider with coalesced upstream calls.

## Requirements

- Go 1.25
- Docker (for building images, running Postgres locally)
- PostgreSQL 16 (provided via Docker for local development)

## Quick start (local, without Compose)

Compose stack is coming in a follow-up iteration. For now, run the pieces directly:

```bash
# Terminal 1: Postgres
docker run --rm -d --name plata-pg \
  -p 5432:5432 \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=quotes \
  postgres:16

# Apply schema (export DB_DSN first)
export DB_DSN=postgres://postgres:postgres@localhost:5432/quotes?sslmode=disable
make migrate-up

# Terminal 2: fake rates provider (no API key required)
make run-fakeprovider

# Terminal 3: service (point at fake provider)
export PROVIDER_BASE_URL=http://localhost:9090
export PROVIDER_API_KEY=test
make run
```

Visit `http://localhost:8080/healthz` to confirm.

## Build

```bash
make build                    # both binaries to ./bin/
make check                    # generate + go test -race + golangci-lint
make docker-build-server      # build server image
make docker-build-fakeprovider
```

## Configuration

All configuration is via environment variables. See `.env.example` for the full list with defaults.

| Variable | Default | Purpose |
|---|---|---|
| `HTTP_ADDR` | `:8080` | HTTP listen address |
| `DB_DSN` | (required) | Postgres connection string |
| `PROVIDER_BASE_URL` | `https://api.currencylayer.com` | Upstream provider URL |
| `PROVIDER_API_KEY` | (required) | Upstream provider API key |
| `WHITELIST_CURRENCIES` | `USD,EUR,MXN` | Comma-separated currency whitelist |
| `SCHEDULER_TICK_SECONDS` | `30` | Quiet-traffic refresh cadence |
| `COALESCING_WINDOW_SECONDS` | `30` | Refresh coalescing bucket size |
| `WORKER_COUNT` | `1` | Background worker pool size |
| `REFRESH_MAX_LATENCY_MS` | `2000` | P99 SLA budget for refresh; `pollInterval` and `batchSize` are derived |
| `APP_ENV` | `development` | `production` disables OpenAPI runtime validation |
| `LOG_LEVEL` | `info` | One of: debug, info, warn, error |

### Fake rates provider (development only)

| Variable | Default | Purpose |
|---|---|---|
| `FAKE_ADDR` | `:9090` | Fake provider listen address |
| `FAKE_SEED` | `42` | RNG seed for deterministic runs |
| `FAKE_MONTHLY_QUOTA` | `100` | Simulated monthly quota |
| `FAKE_ACCESS_KEY` | (empty = any key) | Optional strict-mode access key |
| `FAKE_UPSTREAM_CADENCE_SECONDS` | `0` | Cache rates per window (0 = advance every call) |
| `FAKE_LATENCY_MIN_MS` | `0` | Lower bound for injected latency |
| `FAKE_LATENCY_MAX_MS` | `0` | Upper bound; must be >= MIN |

## API

The OpenAPI spec is at `api/openapi.yaml`. Main endpoints:

- `GET /quotes/latest?base=USD&quote=EUR` — latest cached quote.
- `GET /quotes/{id}` — quote by ID.
- `POST /quotes/refresh` — request a fresh upstream fetch (async, coalesced).
- `GET /healthz` — liveness probe.
- `GET /readyz` — readiness probe (DB ping + scheduler + worker checks).
- `GET /metrics` — Prometheus metrics.

## Testing

```bash
make test                # unit + race
make test-integration    # requires DB_DSN pointing at a live Postgres
make test-fakeprovider   # fakeprovider-only
make coverage            # writes coverage.out
make coverage-html       # writes coverage.html
```

## Migrations

```bash
export DB_DSN=postgres://postgres:postgres@localhost:5432/quotes?sslmode=disable
make migrate-up          # apply all pending
make migrate-down        # roll back one step
```

Migrations live in `migrations/`. The server does NOT run them on startup in this MVP; the operator applies them out of band.

## Architecture

See `docs/architecture.md` for the design. The roadmap lives at `docs/discussions/implementation-roadmap.md`.
