# Idempotency

## Status

Classical `Idempotency-Key` support is **designed but not implemented in MVP**. It is part of Stage 6 in `implementation-roadmap.md`. The design is documented here so the MVP code does not paint us into a corner that blocks adding it later, and so `api-contract.md` has a coherent target to reference.

## Relationship to request coalescing

The MVP already has a **request coalescing** layer, described in `background-mechanism.md` (search for `dedup_key`, `INSERT ... ON CONFLICT`, and the bucket formula). Coalescing collapses concurrent enqueues for the same `(base, quote, bucket)` into a single job and protects upstream cost.

**Coalescing is not classical idempotency.** It works at the level of background work (one upstream fetch per bucket), not at the level of HTTP response replay. The two address different problems and live at different layers:

| | Request coalescing (MVP) | Classical idempotency (Stage 6) |
|---|---|---|
| Where the key comes from | server, derived from `(base, quote, bucket)` | client, sent in `Idempotency-Key` header |
| What is deduplicated | upstream **work** | client **response** (byte-for-byte replay) |
| Window | seconds (MVP `W = 30s`) | hours (24h is the de-facto standard) |
| Storage | `quote_jobs.dedup_key` | new table `idempotency_keys` |
| Goal | cost protection, multi-producer collapse | retry safety under unreliable networks |

**Coalescing formula (updated):** `dedup_key = sha256(UPPER(base) + ":" + UPPER(quote) + ":" + bucket_unix_seconds)`. Each pair has its own `update_id` per the PDF specification; per-base grouping would lose the 1:1 pair-to-job mapping.

A caller that retries a timed-out HTTP request cannot rely on coalescing — by the time the retry reaches the server, the original `W`-second bucket has usually moved on, and the retry creates a new job. Coalescing only fires when both requests land inside the same bucket, which is mostly true for concurrent requests, not for retries seconds later. The standard `Idempotency-Key` pattern fills that gap.

### Why shared `update_id` across callers is acceptable

Coalescing on `(base, quote, bucket)` (no caller component) means two different callers refreshing the `EUR/MXN` pair in the same bucket receive the **same** `update_id`. This is intentional and acceptable here for three reasons:

- The HTTP layer **does not authenticate**; auth is delegated to the gateway in front of the service. The service has no concept of "caller" beyond `X-Request-Id`.
- The data behind an `update_id` is a **public currency rate**, not personalised information. Sharing the id between callers does not leak anything caller-specific.
- The `quote_jobs` table stores **no caller fields**. There is nothing on a job that one caller could see about another.

The Cache-Control `private` on `GET /quotes/:id` (in `api-contract.md`) is a defensive default for shared HTTP caches at the BFF/gateway, not a statement that the response is caller-private. If the service ever grows caller-private fields on `quote_jobs`, this design must be revisited.

### Status-unaware dedup: trade-off

The dedup key is `(base, quote, bucket)`. It does not consider whether a previous job for the same pair is still `pending` or `running`. Consequence: at the bucket boundary, while the previous bucket's worker is still fetching, a refresh that lands in the next bucket will trigger a second concurrent upstream call.

This is intentional. Adding "skip if a pending or running job for this pair exists" makes the insert path stateful (must check job status under a lock or via a separate query), losing the simplicity of a single `INSERT ... ON CONFLICT` statement.

Mitigation that does **not** require code changes: choose `W` significantly larger than the upstream `p99` latency. Concretely, `W ≥ 2 × upstream_p99` keeps stampedes rare. The defaults in `capacity.md` are well above this threshold for all supported tariffs.

A status-aware dedup variant is recorded as a Stage 6 enhancement candidate, paired with the circuit-breaker work in `resilience.md`.

## Context

Classical idempotency protects callers from accidental double-effects on retry. Typical scenario:

1. Client sends `POST /quotes/refresh` with `Idempotency-Key: abc-123`.
2. Network glitch — client never receives the response, even though the server processed the request.
3. Client retries with the same `Idempotency-Key: abc-123`.
4. Server returns the **same response** as the first call. No new job, no second `update_id`, no duplicate audit row.

For our MVP this is not strictly required, because the only side-effect of `POST /quotes/refresh` is enqueuing a job — and that job is already coalesced for the bucket if both requests fall in it. But for any production-shape evolution (audit trail, spread/markup, hedging, customer commitments), classical idempotency is the canonical retry-safe contract.

## Decision

Add support for the standard `Idempotency-Key` HTTP header. Callers send a stable key per logical operation; the service stores `(caller_key, idempotency_key) → full HTTP response` for 24 hours. Repeated requests with the same key replay the original response byte-for-byte.

## Schema

```sql
CREATE TABLE idempotency_keys (
    caller_key       TEXT NOT NULL,
    idempotency_key  TEXT NOT NULL,
    request_hash     BYTEA NOT NULL,
    status           TEXT NOT NULL,        -- in_flight | completed
    response_status  INT,
    response_headers JSONB,
    response_body    BYTEA,
    job_id           UUID REFERENCES quote_jobs(id),
    created_at       TIMESTAMPTZ NOT NULL,
    completed_at     TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ NOT NULL,
    locked_until     TIMESTAMPTZ,
    PRIMARY KEY (caller_key, idempotency_key)
);

CREATE INDEX idempotency_keys_expires_idx
    ON idempotency_keys (expires_at);
```

Column notes:

- `caller_key` — server-derived caller identity (from JWT, mTLS subject, or an internal header set by the gateway). The composite primary key `(caller_key, idempotency_key)` isolates namespaces — a client cannot replay another client's response by guessing keys.
- `request_hash` — `sha256(method + path + body)`. Used to detect "same key, different body" — a client bug that returns `409 Conflict`.
- `status` — `in_flight` while the first request is processing, `completed` once the response is recorded.
- `response_status`, `response_headers`, `response_body` — full HTTP response. Replayed verbatim on repeat.
- `job_id` — link to the `quote_jobs` row created by the original request. Useful for diagnostics.
- `expires_at` — TTL marker. Default 24h.
- `locked_until` — guard for the in-flight window so that a stuck or dead handler does not block all retries forever.

## Middleware

Idempotency runs as middleware between auth and the handler:

```
recovery → request_id → auth → rate_limit → idempotency → handler
```

The middleware:

1. Reads the `Idempotency-Key` header. If absent, passes through unchanged — handler runs normally, no row stored.
2. Reads the body, computes `request_hash`. Limits body size (1 MiB; otherwise `413 Payload Too Large`).
3. Looks up `(caller_key, idempotency_key)`:
   - **Hit, completed.** Replay stored response.
   - **Hit, in-flight, lock not expired.** Return `409 Conflict` with `Retry-After`.
   - **Hit, hash mismatch.** Return `409 Conflict` with `code: idempotency_key_reused`.
   - **Miss.** `INSERT ... ON CONFLICT DO NOTHING` an `in_flight` row. If insert fails (race), re-read and branch as above. If insert succeeds, call the handler, capture its response via a `ResponseWriter` wrapper, write the captured response back to the row with `status=completed`.

## Interaction with the coalescing layer

Both layers are active when this stage is enabled. They compose:

| Request | Idempotency layer | Coalescing layer | Result |
|---|---|---|---|
| With `Idempotency-Key`, key already completed | hit | — | Replay stored response. Coalescing not consulted. |
| With `Idempotency-Key`, first time | miss → in-flight | hit (existing job) | Handler runs; coalescing returns existing `update_id`; idempotency stores response with that id. |
| With `Idempotency-Key`, first time | miss → in-flight | miss | New job created; new response stored. |
| Without `Idempotency-Key` | bypass | hit / miss | MVP behavior, coalescing only. |

The idempotency layer can be added without changing any coalescing code.

## Errors are idempotent too

If the first request resulted in a `5xx` error, the stored response is that `5xx`. Subsequent retries with the same key receive the same `5xx`. This is the standard Stripe-style behavior and prevents "client retried, got success on retry, and sent a duplicate downstream operation" problems.

Exception: errors that occur **before** the `in_flight` marker is committed (body parse, auth failure, rate-limit reject) are not stored. They are not yet covered by the idempotency contract.

## Edge cases

| # | Situation | Behavior |
|---|---|---|
| 1 | Same key, same body | Replay stored response |
| 2 | Same key, different body | `409 Conflict` (`code: idempotency_key_reused`) |
| 3 | Same key, first request still in-flight | `409 Conflict` with `Retry-After` |
| 4 | Same key sent by different caller (different `caller_key`) | Not found in this caller's namespace; treated as new request |
| 5 | No `Idempotency-Key` header | Middleware passes through; behavior reverts to MVP (coalescing only) |
| 6 | Key TTL (24h) expired | Treated as new request; new response stored |
| 7 | Body exceeds 1 MiB | `413 Payload Too Large`; key not stored |
| 8 | First request errored with 5xx | 5xx stored and replayed on subsequent calls |
| 9 | Handler crashed mid-flight (`in_flight` lock expired) | Next retry is treated as new request; `INSERT ON CONFLICT` resolves the race |

## Cleanup and TTL

A periodic cleanup task deletes expired rows:

```sql
DELETE FROM idempotency_keys WHERE expires_at < now();
```

Default schedule: every 5 minutes. Implementation choice (in-process goroutine, `pg_cron`, k8s `CronJob`, or daily partitioning for very high volume) is deferred until this stage is in scope.

## Why deferred

- The MVP scope is "the main async pattern works correctly". Coalescing protects upstream cost; that is sufficient for the primary use case.
- This stage adds a non-trivial amount of code: middleware, response-capture wrapper, new table and migrations, TTL cleanup, race handling.
- The schema and integration are designed; nothing about the MVP blocks adding this stage later.

## Not in scope here

- **Per-request force-fresh override.** A header that says "ignore the coalescing window and fetch now". Belongs to the coalescing layer; could be added separately.
- **TTL cleanup mechanics.** The implementation details (in-process cron, `pg_cron`, partitioning) are deferred until this stage is in scope.
- **Public API authentication concerns** that come with `Idempotency-Key` (rate-limiting per key, key length validation, etc.). To be revisited when the public API surface is defined.
