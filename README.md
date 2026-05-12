# Currency Quote Service

A REST service that returns current exchange rates between whitelisted currency pairs, refreshing in the background from an external rates provider with coalesced upstream calls.

## Requirements

- Go 1.25
- Docker (for building images, running Postgres locally)
- PostgreSQL 16 (provided via Docker for local development)

## API

The OpenAPI spec is at `api/openapi.yaml`. While the stack is running you can also browse it interactively:

- Swagger UI: `http://localhost:8080/docs/`
- Raw spec JSON: `http://localhost:8080/openapi.json`

Main endpoints:

- `GET /quotes/latest?base=USD&quote=EUR` — latest cached quote.
- `GET /quotes/{id}` — quote by ID.
- `POST /quotes/refresh` — request a fresh upstream fetch (async, coalesced).
- `GET /healthz` — liveness probe.
- `GET /readyz` — readiness probe (DB ping + scheduler + worker checks).
- `GET /metrics` — Prometheus metrics.

## Quick start

Two one-command modes are provided. Pick whichever fits.

### Demo mode (fake provider + load test)

```bash
make demo
```

Brings up the full stack against the local fake rates provider with business-like settings (`SCHEDULER_TICK_SECONDS=30`, `COALESCING_WINDOW_SECONDS=5`, injected upstream latency `100–500 ms`), waits for `/readyz`, and runs the read-storm load profile at **5000 RPS for 2 minutes** against it. The stack stays running after k6 exits so you can inspect Grafana.

### Real-upstream mode (apilayer, no load test)

```bash
# 1) Put your apilayer key into .env
cp .env.example .env
$EDITOR .env   # set PROVIDER_API_KEY=...

# 2) Bring the stack up against the real provider
make demo-real
```

`SCHEDULER_TICK_SECONDS=120` and `COALESCING_WINDOW_SECONDS=30` — ~30 upstream-fetch ticks per hour at quiet traffic. **Check your provider quota before leaving this running.** No load test is started.

### Endpoints

After either command:

- Service: `http://localhost:8080/healthz`
- Grafana: `http://localhost:3000` — login `admin/admin`
  - Dashboards: Service Health, Queue Health, Upstream Health, Database Health (auto-provisioned)
- Prometheus: `http://localhost:9090` — `Alerts` tab shows the configured rules

Stop the stack:

```bash
docker compose down       # keep DB volume
docker compose down -v    # also delete DB and Prometheus data
```

### Logs

The service writes structured JSON to stdout; nothing is persisted to a file inside the container. Stream logs via Compose:

```bash
docker compose logs -f server          # follow the server, Ctrl-C to stop
docker compose logs --tail=200 server  # last 200 lines, then exit
docker compose logs                    # all services, scrollable
docker compose logs -f server fakeprovider postgres   # follow several
```

Pipe through `jq` for readable output, e.g. `docker compose logs -f server | jq -c '{t:.time, lvl:.level, msg:.msg, req:.request_id}'`. Filter by request id with `docker compose logs server | grep '"request_id":"r-abc"'`.

### Plain stack (no preset)

If you want defaults without the demo presets:

```bash
docker compose up -d
```

### Configuration overrides

The stack defaults to the fake rates provider (`fakeprovider` service). To point at the real apilayer upstream, copy `.env.example` to `.env` and uncomment the relevant lines:

```
PROVIDER_BASE_URL=https://api.currencylayer.com
PROVIDER_API_KEY=your-real-key
GF_ADMIN_PASSWORD=changeme-for-non-local-deployments
```

Compose auto-loads `.env`. The file is `.gitignored`.

> **Note:** Docker Compose reads the repo-root `.env` for **every** invocation. If you set `PROVIDER_BASE_URL` and `PROVIDER_API_KEY` for `make run` (without Compose), those values will also override the fake-provider defaults the next time you run `docker compose up`. Symptom: the server fails to start with an apilayer `api_code=101` error. Either comment out those lines before `docker compose up`, or skip `.env` entirely via `docker compose --env-file /dev/null up -d`.

## Running without Compose (for IDE development)

If you prefer to run the binaries directly:

```bash
# Terminal 1: Postgres
docker run --rm -d --name currency-exchange-pg \
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
make compose-validate         # validate docker-compose.yml + Prometheus config
make docker-build-server      # build server image
make docker-build-fakeprovider
```

## Configuration

All configuration is via environment variables. See `.env.example` for the full list with defaults.

| Variable | Default | Purpose |
|---|---|---|
| `HTTP_ADDR` | `:8080` | HTTP listen address |
| `DB_DSN` | (required) | Postgres connection string |
| `DB_POOL_MAX_CONNS` | `25` | pgxpool max-connections ceiling. Must be >= 1. |
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

## Testing

```bash
make test                # unit + race
make test-integration    # requires DB_DSN pointing at a live Postgres
make test-fakeprovider   # fakeprovider-only
make coverage            # writes coverage.out
make coverage-html       # writes coverage.html
```

### Load tests (k6)

Smoke-tier load tests live in `loadtest/`. They run inside the Compose stack via the `loadtest` profile (no host k6 install required). `make demo` runs profile 2 at 5000 RPS as a one-command demo; for individual profiles and overrides see [`loadtest/README.md`](loadtest/README.md) and [`docs/discussions/load-testing.md`](docs/discussions/load-testing.md).

## Migrations

```bash
export DB_DSN=postgres://postgres:postgres@localhost:5432/quotes?sslmode=disable
make migrate-up          # apply all pending
make migrate-down        # roll back one step
```

Migrations live in `migrations/`. The server does NOT run them on startup in this MVP; the operator applies them out of band.

## Architecture

See `docs/architecture.md` for the design. The roadmap lives at `docs/discussions/implementation-roadmap.md`.
