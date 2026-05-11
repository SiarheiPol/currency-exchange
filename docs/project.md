# Project Overview

Go HTTP service providing currency exchange rate quotes via an asynchronous refresh pattern. Full requirements: `Requirements.pdf` at project root (gitignored).

## API Flow

The service exposes three endpoints with two roles:

- `POST /quotes/refresh` and `GET /quotes/:id` form an **asynchronous pair**. The client triggers a fetch, receives an `update_id`, and polls for the result.
- `GET /quotes/latest?base=BASE&quote=QUOTE` is a **synchronous read** of the most recent successful quote for a currency pair.

Refresh flow:

```
1. Client → POST /quotes/refresh {"base":"EUR","quote":"MXN"}
            ↓
   Service responds 202 Accepted {"id":"abc-..."}
            (job is enqueued; handler does not perform the fetch)

2. Worker → reserves the job, calls the rates provider, writes to quotes
            (background; client is not waiting)

3. Client → GET /quotes/:id (polls)
            Service responds 200 with one of:
              {"status":"pending", ...}   while not yet processed or retrying
              {"status":"done", "price":..., "updated_at":...}  on success
              {"status":"failed", "error":...}  after retries exhausted

4. Client → GET /quotes/latest?base=EUR&quote=MXN  (any time, independent of refresh)
            Service responds 200 with the most recent successful quote.
```

`/latest` is kept fresh by a background scheduler that ticks every `T` seconds, plus by any client-driven refresh that lands in a fresh bucket. Both go through the same queue and dedup mechanism.

## Endpoints

| Endpoint | Method | Body / Path | Response |
|---|---|---|---|
| `/quotes/refresh` | POST | `{"base":"EUR","quote":"MXN"}` | `202 Accepted {"id":"..."}` |
| `/quotes/:id` | GET | path: UUID | `200 OK` with `status`, `price`, `updated_at`, etc. |
| `/quotes/latest` | GET | query: `base=EUR&quote=MXN` | `200 OK {"base","quote","price","updated_at"}` or `404` |

Operational endpoints:
| Endpoint | Purpose |
|---|---|
| `/healthz` | liveness — always 200 if process responsive |
| `/readyz` | readiness — DB ping + scheduler-tick freshness + worker heartbeat |
| `/metrics` | Prometheus exposition |
| `/openapi.json` | the API spec |
| `/docs/` | Swagger UI |

Detailed contract (request/response shapes, headers, error envelope, edge case matrix) lives in `discussions/api-contract.md`.

## Supported Currencies

MVP whitelist: **`USD`, `EUR`, `MXN`**. The service autogenerates all 6 ordered pairs at startup, excluding self-pairs. Configured via env var; enforced at the validation layer with three-step check (format → whitelist → self-pair) producing distinct error codes (`invalid_request` vs `unsupported_currency`).

Adding a currency is a configuration change, not a code change. The architecture imposes no hard limit on whitelist size; capacity numbers in `discussions/capacity.md` assume three.

## Consumer profile

The service is consumed by other internal services and BFF (mobile/web). Authentication is performed at the gateway in front of the service; the HTTP layer here does not validate tokens. `X-Request-Id` is read for tracing; an empty middleware slot for `authn` is reserved in the router for future zero-trust or mTLS work.

## Where to look

- API contract details — `discussions/api-contract.md` and `api/openapi.yaml`.
- Background mechanism (queue, scheduler, worker) — `discussions/background-mechanism.md`.
- Build order and roadmap — `discussions/implementation-roadmap.md`.
- Architecture overview — `architecture.md`.
