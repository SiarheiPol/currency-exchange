# API contract

## Status
Decided.

## Context

The service exposes three endpoints with different roles:

- `POST /quotes/refresh` and `GET /quotes/:id` are an asynchronous pair: trigger work, then poll for the result.
- `GET /quotes/latest/:currency` is a synchronous read of the most recent successful quote. It is kept fresh by a background scheduler described in `background-mechanism.md`.

The HTTP layer here does not perform authentication. `X-Request-Id` is read for tracing if present; otherwise the service generates one.

## Endpoints

### `POST /quotes/refresh`

Triggers a background fetch of the current rate for a currency.

**Request:**
```http
POST /quotes/refresh HTTP/1.1
Content-Type: application/json
X-Request-Id: <optional>

{ "currency": "EUR" }
```

**Response (202 Accepted):**
```json
{ "id": "9b6e1f3a-..." }
```

Response headers:
- `Content-Type: application/json`
- `Location: /quotes/9b6e1f3a-...`
- `Cache-Control: no-store`

The handler returns as soon as the job is durably enqueued. The actual fetch runs in a worker. `202 Accepted` is used because the request was accepted but the result resource is not yet final.

Behavior on repeated requests for the same currency (request coalescing within a window) is described in `idempotency.md`. From a caller's point of view, repeated requests in the same coalescing window receive the same `update_id` and the same response shape — no special handling required.

### `GET /quotes/:id`

Returns the state of one refresh job.

The body always has the same shape, with `status` one of `pending`, `done`, `failed`. `status` reflects API-visible state, not internal worker state. A job that failed once but still has retry budget left is reported as `pending` — clients see at most two transitions: `pending → done` or `pending → failed`.

Reasoning behind "always 200 + `status` field" instead of `202` / `200` / `5xx`:

- `failed` is a business outcome (the upstream provider was unavailable after retries), not a service error. Mapping it to 5xx would corrupt service-level error metrics and trigger automatic retry loops in internal SDKs.
- One body shape means one parser on the client side. No branch on status code before reading JSON.
- Cache headers are explicit per status (see below) and do not depend on default proxy behavior for unusual codes.

**Common fields:**
```json
{
  "id":         "9b6e1f3a-...",
  "currency":   "EUR",
  "status":     "pending | done | failed",
  "created_at": "2026-04-29T08:00:00Z"
}
```

**`status = pending`:**
```json
{
  "id":         "9b6e1f3a-...",
  "currency":   "EUR",
  "status":     "pending",
  "created_at": "2026-04-29T08:00:00Z",
  "attempts":   1
}
```

**`status = done`:**
```json
{
  "id":           "9b6e1f3a-...",
  "currency":     "EUR",
  "status":       "done",
  "created_at":   "2026-04-29T08:00:00Z",
  "completed_at": "2026-04-29T08:00:01Z",
  "price":        1.0834,
  "updated_at":   "2026-04-29T08:00:01Z"
}
```

`updated_at` is the time the rates provider returned the value. Same field as in `GET /quotes/latest/:currency`.

**`status = failed`:**
```json
{
  "id":           "9b6e1f3a-...",
  "currency":     "EUR",
  "status":       "failed",
  "created_at":   "2026-04-29T08:00:00Z",
  "completed_at": "2026-04-29T08:01:30Z",
  "attempts":     5,
  "error":        { "code": "upstream_unavailable", "message": "rates provider returned 503" }
}
```

**HTTP codes used by this endpoint:**

| Situation | Code | Body |
|---|---|---|
| Job exists (any status) | 200 | one of the three shapes above |
| `id` is not a valid UUID | 400 | error envelope, code `invalid_request` |
| `id` is a valid UUID but unknown | 404 | error envelope, code `not_found` |
| Server bug | 500 | error envelope, code `internal` |

**Polling guidance.** The async pattern produces multiple `GET /quotes/:id` calls per `update_id`. To bound this load:

- The recommended client polling interval is **1 second**. Faster polling is not useful — worker pickup latency dominates.
- A `pending` response includes `Retry-After: 1` header. Clients that respect it reduce polling RPS automatically.
- A terminal response (`done` or `failed`) has long `max-age` (see Cache-Control below). Clients should stop polling and read the cached terminal response thereafter.

**Cache-Control:**

| Status | `Cache-Control` | `ETag` | Other headers |
|---|---|---|---|
| `pending` | `no-store` | — | `Retry-After: 1` |
| `done` | `private, max-age=3600, immutable` | `"<id>-done"` | — |
| `failed` | `private, max-age=3600, immutable` | `"<id>-failed"` | — |
| 400 / 404 | `no-store` | — | — |

`done` and `failed` are terminal: once set, the body for a given `id` never changes. `immutable` lets clients skip revalidation for the TTL.

**Why `private` and not caller-private semantics.** `private` means "do not store in shared caches" — a defensive default for HTTP caches at the BFF/gateway. It is **not** a statement that the response is tied to one caller. Coalescing collapses concurrent refreshes from different callers onto the same `update_id` (see `idempotency.md > Why shared update_id across callers is acceptable`); the response body contains only public rate data. If `quote_jobs` ever grows caller-specific fields, this design must be revisited.

### `GET /quotes/latest/:currency`

Returns the last successful quote for a currency. Operational state (pending or failed jobs) is not exposed here; that lives in `GET /quotes/:id`.

The reason: a caller of `latest` does not have an `update_id` and cannot do anything useful with `pending` or `failed` except re-poll. The freshness signal `updated_at` is enough — each caller applies its own staleness threshold.

**Response (200 OK):**
```json
{
  "currency":   "EUR",
  "price":      1.0834,
  "updated_at": "2026-04-29T08:00:00Z"
}
```

**HTTP codes:**

| Situation | Code | Body |
|---|---|---|
| Successful quote exists | 200 | as above |
| Currency is not supported | 400 | error envelope, code `unsupported_currency` |
| Currency is supported but no successful quote yet | 404 | error envelope, code `no_data` |

The 400 / 404 split is intentional. `400` means "do not retry, change the request". `404` means "no data right now, may appear later".

**Cache-Control:**

| Situation | `Cache-Control` | `ETag` |
|---|---|---|
| 200 | `public, max-age=W` | `"<currency>-<updated_at_unix>"` |
| 400 / 404 | `no-store` | — |

**What `W` means in this header.** `W` is the coalescing bucket size from `idempotency.md` — the **minimum interval between upstream-driven refresh work** per currency. It bounds how often `quotes.updated_at` can advance under active client traffic.

Note: `W` is **not** an absolute upper bound on data staleness. Three other factors influence actual freshness:

- The **scheduler tick `T`** sets the quiet-traffic refresh target — when no clients are calling refresh, `quotes` is updated at most once per `T`.
- The **upstream provider's own update cadence** is a hard ceiling: even if we call upstream every second, the provider may only refresh its data every 60s on Business or every 10min on Pro+.
- **Failures** (timeouts, retry exhaustion, circuit-open if Stage 6) can extend staleness beyond `W` and `T` until upstream recovers.

We use `W` for `max-age` because under active traffic it is the rate at which clients see new data. A `max-age` derived from `T` (which is ≥ `W`) would advertise stale data as fresh during active periods.

The `max-age` value must come from the same configuration variable as the coalescing window. With `W=30s` this gives `max-age=30`. After `max-age` expires, downstream caches use conditional GET with `If-None-Match: ETag`; the service returns `304 Not Modified` cheaply if the underlying row has not changed.

`public` is correct because the response is not tied to a caller. A shared cache at the BFF or gateway can serve it.

We do not use `stale-while-revalidate`. It would let caches serve stale responses for an extra window while async-revalidating, which extends the staleness boundary beyond `W`. For an MVP that is consumed mostly by internal services, the small latency win is not worth the added confusion.

A formal **freshness SLI** for `/latest` is in `monitoring.md > SLO and SLI thinking`.

## Common rules

### Currency validation

Validated the same way in the request body of `POST /quotes/refresh` and in the path of `GET /quotes/latest/:currency`:

1. **Format check first.** Exactly three ASCII uppercase letters. Otherwise `400 invalid_request`.
2. **Whitelist check second.** Must be one of `USD`, `EUR`, `MXN`. Otherwise `400 unsupported_currency`.

The order matters: `jpy` fails on format, `JPY` fails on whitelist. Two distinct error codes for two distinct mistakes.

### Headers we read

- `X-Request-Id` — copied to logs and to outbound calls. If absent, the service generates a UUID and uses it for logs.
- `Content-Type: application/json` — required on `POST`.

`Idempotency-Key` is **not** read in MVP. Classical idempotency is a Stage 6 extension; design lives in `idempotency.md`.

### Headers we emit

- `Content-Type: application/json` on every response with a body.
- `Cache-Control` on every response.
- `ETag` on terminal `GET /quotes/:id` (done, failed) and on `200` of `GET /quotes/latest/:currency`.
- `X-Request-Id` echoed back (the value the caller sent, or the one the service generated).

### Error envelope

All non-2xx responses use the same shape:

```json
{
  "error": {
    "code":    "unsupported_currency",
    "message": "currency 'JPY' is not supported"
  }
}
```

Codes used so far: `invalid_request`, `unsupported_currency`, `no_data`, `not_found`, `upstream_unavailable`, `internal`. New codes are added per endpoint as needed. `message` is for humans; clients should branch on `code`.

## Edge case matrix (test seeds)

One row, one handler test case.

| # | Endpoint | Input | Expected | Notes |
|---|---|---|---|---|
|  1 | POST /quotes/refresh | `{"currency":"EUR"}` | 202 | `id` is a fresh UUID, `Location` set |
|  2 | POST /quotes/refresh | `{"currency":"JPY"}` | 400 | `unsupported_currency` |
|  3 | POST /quotes/refresh | `{"currency":"jpy"}` | 400 | `invalid_request` (format) |
|  4 | POST /quotes/refresh | `{"currency":"EU"}` | 400 | `invalid_request` (format) |
|  5 | POST /quotes/refresh | empty body | 400 | `invalid_request` |
|  6 | POST /quotes/refresh | non-JSON body | 400 | `invalid_request` |
|  7 | POST /quotes/refresh | missing `Content-Type` | 400 | `invalid_request` |
|  8 | GET /quotes/:id | unknown valid UUID | 404 | `not_found` |
|  9 | GET /quotes/:id | malformed UUID | 400 | `invalid_request` |
| 10 | GET /quotes/:id | pending job | 200 + `status=pending` | `Cache-Control: no-store` |
| 11 | GET /quotes/:id | done job | 200 + `status=done` | `ETag` set, `immutable` |
| 12 | GET /quotes/:id | failed job | 200 + `status=failed` | `ETag` set |
| 13 | GET /quotes/:id | done job, `If-None-Match` matches | 304 | no body |
| 14 | GET /quotes/latest/:currency | `EUR` with quote | 200 | `public`, `ETag` set |
| 15 | GET /quotes/latest/:currency | `EUR` without quote | 404 | `no_data` |
| 16 | GET /quotes/latest/:currency | `JPY` | 400 | `unsupported_currency` |
| 17 | GET /quotes/latest/:currency | `eur` | 400 | `invalid_request` |
| 18 | GET /quotes/latest/:currency | `EUR`, `If-None-Match` matches | 304 | no body |
| 19 | any | server bug (panic, DB down) | 500 | `internal`, no leaked details |
| 20 | any | request without `X-Request-Id` | normal status | service-generated id in logs and echo |

## Not in scope here

- Coalescing rules for `POST /quotes/refresh` (window `W`, dedup key, behavior on duplicates). See `idempotency.md`.
- Classical idempotency via `Idempotency-Key` header (Stage 6 extension). See `idempotency.md`.
- OpenAPI / Swagger source of truth. See `openapi.md`.
- Per-endpoint rate limits and authentication concerns. Out of scope for this service.
