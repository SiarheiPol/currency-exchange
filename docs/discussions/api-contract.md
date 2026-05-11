# API contract

## Status
Decided.

## Context

The service exposes three endpoints with different roles:

- `POST /quotes/refresh` and `GET /quotes/:id` are an asynchronous pair: trigger work, then poll for the result. The refresh target is a currency pair `(base, quote)` — both sides are required.
- `GET /quotes/latest?base=BASE&quote=QUOTE` is a synchronous read of the most recent successful quote for a currency pair. It is kept fresh by a background scheduler described in `background-mechanism.md`.

The HTTP layer here does not perform authentication. `X-Request-Id` is read for tracing if present; otherwise the service generates one.

## Endpoints

### `POST /quotes/refresh`

Triggers a background fetch of the current rate for a currency.

**Request:**
```http
POST /quotes/refresh HTTP/1.1
Content-Type: application/json
X-Request-Id: <optional>

{ "base": "EUR", "quote": "MXN" }
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

**Latency contract.** For refresh-driven jobs on a healthy upstream, the p99 of the interval from accepted `POST /quotes/refresh` (the moment this handler returns `202`) to `GET /quotes/:id` reporting `status=done` is bounded by `REFRESH_MAX_LATENCY_MS` (an integer count of milliseconds; default `2000`, which produces a 2s SLA). Per-deployment overrides and per-tariff guidance live in `capacity.md > Refresh latency SLA`. The SLI shape (target as p99, exclusions) is defined in `monitoring.md > SLO and SLI thinking`.

Scope and exclusions:

- Applies **only** to the refresh-driven path. Scheduler-driven cache freshness (when no client refreshes occur) has a separate target of `2 × T`, documented in `monitoring.md > SLO and SLI thinking > Freshness SLI`.
- Jobs that enter retry/backoff because of transient upstream errors are excluded from this SLI — their behaviour is the concern of the job-success-rate metric, not job-latency.
- Coalesced duplicates (a second `POST` arriving inside the same window as a first one) are counted **once**, against the first accepted request in their dedup window. The second response is effectively immediate — `Enqueue` returns the existing job id, and the underlying work is the same job already in flight.

Behavior on repeated requests for the same `(base, quote)` pair (request coalescing within a window) is described in `idempotency.md`. From a caller's point of view, repeated requests in the same coalescing window receive the same `update_id` — no special handling required.

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
  "base":       "EUR",
  "quote":      "MXN",
  "status":     "pending | done | failed",
  "created_at": "2026-04-29T08:00:00Z"
}
```

**`status = pending`:**
```json
{
  "id":         "9b6e1f3a-...",
  "base":       "EUR",
  "quote":      "MXN",
  "status":     "pending",
  "created_at": "2026-04-29T08:00:00Z",
  "attempts":   1
}
```

**`status = done`:**
```json
{
  "id":           "9b6e1f3a-...",
  "base":         "EUR",
  "quote":        "MXN",
  "status":       "done",
  "created_at":   "2026-04-29T08:00:00Z",
  "completed_at": "2026-04-29T08:00:01Z",
  "price":        20.255648,
  "updated_at":   "2026-04-29T08:00:01Z"
}
```

`price` is in forex convention: `price = 20.255648` means "1 EUR = 20.255648 MXN". `updated_at` is derived from the upstream response `timestamp` field (Unix seconds), not from the local clock.

**`status = failed`:**
```json
{
  "id":           "9b6e1f3a-...",
  "base":         "EUR",
  "quote":        "MXN",
  "status":       "failed",
  "created_at":   "2026-04-29T08:00:00Z",
  "completed_at": "2026-04-29T08:01:30Z",
  "attempts":     5,
  "error":        { "code": "upstream_unavailable", "message": "rates provider returned success:false, api_code=104" }
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

### `GET /quotes/latest?base=BASE&quote=QUOTE`

Returns the last successful quote for a currency pair. Operational state (pending or failed jobs) is not exposed here; that lives in `GET /quotes/:id`.

The reason: a caller of `latest` does not have an `update_id` and cannot do anything useful with `pending` or `failed` except re-poll. The freshness signal `updated_at` is enough — each caller applies its own staleness threshold.

**Response (200 OK):**
```json
{
  "base":       "EUR",
  "quote":      "MXN",
  "price":      20.255648,
  "updated_at": "2026-04-29T08:00:00Z"
}
```

`price` means "1 `base` = `price` `quote`".

**HTTP codes:**

| Situation | Code | Body |
|---|---|---|
| Successful quote exists | 200 | as above |
| Either `base` or `quote` is not a supported currency | 400 | error envelope, code `unsupported_currency` |
| `base == quote` | 400 | error envelope, code `invalid_request` |
| Either field is missing or malformed | 400 | error envelope, code `invalid_request` |
| Pair is supported but no successful quote yet | 404 | error envelope, code `no_data` |

The 400 / 404 split is intentional. `400` means "do not retry, change the request". `404` means "no data right now, may appear later".

**Cache-Control:**

| Situation | `Cache-Control` | `ETag` |
|---|---|---|
| 200 | `public, max-age=W` | `"<base>-<quote>-<updated_at_unix>"` |
| 400 / 404 | `no-store` | — |

`public` is correct because the response is not tied to a caller. A shared cache at the BFF or gateway can serve it.

**What `W` means in this header.** `W` is the coalescing bucket size from `idempotency.md` — the **minimum interval between upstream-driven refresh work** per pair. It bounds how often `quotes.updated_at` can advance under active client traffic.

Note: `W` is **not** an absolute upper bound on data staleness. Three other factors influence actual freshness:

- The **scheduler tick `T`** sets the quiet-traffic refresh target — when no clients are calling refresh, `quotes` is updated at most once per `T`.
- The **upstream provider's own update cadence** is a hard ceiling: even if we call upstream every second, the provider may only refresh its data every 60s on Business or every 10min on Pro+.
- **Failures** (timeouts, retry exhaustion, circuit-open if Stage 6) can extend staleness beyond `W` and `T` until upstream recovers.

We use `W` for `max-age` because under active traffic it is the rate at which clients see new data. A `max-age` derived from `T` (which is ≥ `W`) would advertise stale data as fresh during active periods.

The `max-age` value must come from the same configuration variable as the coalescing window. With `W=30s` this gives `max-age=30`. After `max-age` expires, downstream caches use conditional GET with `If-None-Match: ETag`; the service returns `304 Not Modified` cheaply if the underlying row has not changed.

We do not use `stale-while-revalidate`. It would let caches serve stale responses for an extra window while async-revalidating, which extends the staleness boundary beyond `W`. For an MVP that is consumed mostly by internal services, the small latency win is not worth the added confusion.

A formal **freshness SLI** for `/latest` is in `monitoring.md > SLO and SLI thinking`.

## Common rules

### Pair validation

Validated the same way in the request body of `POST /quotes/refresh` and in the query parameters of `GET /quotes/latest?base=BASE&quote=QUOTE`:

1. **Format check first.** Each field (`base` and `quote`) must be exactly three ASCII uppercase letters. Otherwise `400 invalid_request`.
2. **Whitelist check second.** Both must be one of `USD`, `EUR`, `MXN`. Otherwise `400 unsupported_currency`.
3. **Self-pair check third.** `base` must not equal `quote` (e.g., `base=EUR&quote=EUR` is rejected). Error code `invalid_request`, message "base and quote must differ".

The order matters: `eur` fails on format, `JPY` fails on whitelist, `EUR/EUR` fails on self-pair check. Three distinct error codes / messages for three distinct mistakes.

### Headers we read

- `X-Request-Id` — copied to logs and to outbound calls. If absent, the service generates a UUID and uses it for logs.
- `Content-Type: application/json` — required on `POST`.

`Idempotency-Key` is **not** read in MVP. Classical idempotency is a Stage 6 extension; design lives in `idempotency.md`.

### Headers we emit

- `Content-Type: application/json` on every response with a body.
- `Cache-Control` on every response.
- `ETag` on terminal `GET /quotes/:id` (done, failed) and on `200` of `GET /quotes/latest?base=BASE&quote=QUOTE`.
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

Codes used so far: `invalid_request`, `unsupported_currency`, `no_data`, `not_found`, `upstream_unavailable`, `internal`. `unsupported_currency` is returned when either side of the requested pair is outside the whitelist. New codes are added per endpoint as needed. `message` is for humans; clients should branch on `code`.

## Edge case matrix (test seeds)

One row, one handler test case.

| # | Endpoint | Input | Expected | Notes |
|---|---|---|---|---|
| 1 | POST /quotes/refresh | `{"base":"EUR","quote":"MXN"}` | 202 | `id` is a fresh UUID, `Location` set |
| 2 | POST /quotes/refresh | `{"base":"JPY","quote":"MXN"}` | 400 | `unsupported_currency` (JPY not in whitelist) |
| 3 | POST /quotes/refresh | `{"base":"eur","quote":"MXN"}` | 400 | `invalid_request` (format: lowercase) |
| 4 | POST /quotes/refresh | `{"base":"EU","quote":"MXN"}` | 400 | `invalid_request` (format: length) |
| 5 | POST /quotes/refresh | `{"base":"EUR","quote":"EUR"}` | 400 | `invalid_request` (self-pair) |
| 6 | POST /quotes/refresh | `{"base":"EUR"}` (missing quote) | 400 | `invalid_request` |
| 7 | POST /quotes/refresh | empty body | 400 | `invalid_request` |
| 8 | POST /quotes/refresh | non-JSON body | 400 | `invalid_request` |
| 9 | POST /quotes/refresh | missing `Content-Type` | 400 | `invalid_request` |
| 10 | GET /quotes/:id | unknown valid UUID | 404 | `not_found` |
| 11 | GET /quotes/:id | malformed UUID | 400 | `invalid_request` |
| 12 | GET /quotes/:id | pending job | 200 + `status=pending` | `Cache-Control: no-store` |
| 13 | GET /quotes/:id | done job | 200 + `status=done` | `ETag` set, `immutable` |
| 14 | GET /quotes/:id | failed job | 200 + `status=failed` | `ETag` set |
| 15 | GET /quotes/:id | done job, `If-None-Match` matches | 304 | no body |
| 16 | GET /quotes/latest | `base=EUR&quote=MXN` with quote | 200 | `public`, `ETag` set |
| 17 | GET /quotes/latest | `base=EUR&quote=MXN`, no data yet | 404 | `no_data` |
| 18 | GET /quotes/latest | `base=JPY&quote=MXN` | 400 | `unsupported_currency` |
| 19 | GET /quotes/latest | `base=eur&quote=MXN` | 400 | `invalid_request` |
| 20 | GET /quotes/latest | `base=EUR&quote=EUR` | 400 | `invalid_request` (self-pair) |
| 21 | GET /quotes/latest | `base=EUR` (missing quote) | 400 | `invalid_request` |
| 22 | GET /quotes/latest | `base=EUR&quote=MXN`, `If-None-Match` matches | 304 | no body |
| 23 | any | server bug (panic, DB down) | 500 | `internal`, no leaked details |
| 24 | any | request without `X-Request-Id` | normal status | service-generated id in logs and echo |

## Not in scope here

- Coalescing rules for `POST /quotes/refresh` (window `W`, dedup key derivation, behavior on duplicates). See `idempotency.md`.
- Classical idempotency via `Idempotency-Key` header (Stage 6 extension). See `idempotency.md`.
- OpenAPI / Swagger source of truth. See `openapi.md`.
- Per-endpoint rate limits and authentication concerns. Out of scope for this service.
