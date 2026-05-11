# Load Tests

k6 load test scripts for the Currency Quote Service.

## Precondition

The full compose stack must be running before executing any load test profile:

```bash
docker compose up -d
```

Wait for all services to become healthy (check with `docker compose ps`).

## Running the tests

```bash
make loadtest             # profile 1, 30s default
make loadtest-coalesce    # profile 4
make loadtest-read        # profile 2
make loadtest-burst       # profile 3
make loadtest-fail        # profile 5 (baseline; see below for latency injection)
LOADTEST_DURATION=10s make loadtest    # override duration
LOADTEST_DURATION=30m make loadtest    # full tier
```

The k6 container is started on demand under the `loadtest` compose profile and removed automatically after the run (`--rm`). No k6 installation on the host is required.

### Running with overrides

Both `LOADTEST_DURATION` and `LOADTEST_RATE` can be overridden via environment variables. All profile targets (1, 2, 3, 5) honour both. Example showing a high-rate extended run for profile 2:

```bash
LOADTEST_RATE=5000 LOADTEST_DURATION=5m make loadtest-read
```

## Profiles

### Profile 1 — Sustained baseline smoke

Sends 50 requests per second for 30 seconds (overridable via `LOADTEST_DURATION`). Each iteration picks a random whitelisted pair and rolls an 80/20 split: 80 % are `GET /quotes/latest` calls, 20 % are `POST /quotes/refresh` calls.

Thresholds that must pass:
- `http_req_failed` rate < 1 %
- `GET /quotes/latest` p(99) < 200 ms
- `POST /quotes/refresh` p(99) < 300 ms
- `checks` pass rate > 99 %

This profile answers: *does the service hold up under realistic continuous load against the fake rates provider?*

### Profile 4 — Coalescing stress smoke

Fires 100 virtual users, each sending exactly one `POST /quotes/refresh` for the fixed pair `USD/EUR`, all within a 10-second window. Before and after the burst, the script reads `/metrics` and parses the `rates_provider_requests_total{outcome="ok"}` counter. The delta must be ≤ 2, confirming that coalescing collapses the 100 requests into at most two upstream calls (one for any partial bucket overlap).

Thresholds that must pass:
- `checks` pass rate > 99 % (covers both the 202 status check and the delta assertion)

This profile answers: *does coalescing actually save upstream calls under realistic concurrency?*

**Implicit coupling:** the `delta ≤ 2` threshold assumes `COALESCING_WINDOW_SECONDS ≥ 10` in the running server. The 100-request burst takes ~5 s; with smaller windows the requests would span multiple coalescing buckets and produce more than two upstream calls. `docker-compose.yml` hardcodes the server env to `COALESCING_WINDOW_SECONDS=30`, so the assumption holds out of the box. Keep this floor in mind if you ever override the value.

### Profile 2 — Read storm smoke

Sends `GET /quotes/latest` at high RPS (default 500 req/s) across random whitelisted pairs for 30 seconds. Validates that caching headers are present on every response and that read scalability holds under sustained pressure.

Thresholds that must pass:
- `http_req_failed` rate < 1 %
- `GET /quotes/latest` p(99) < 200 ms
- `checks` pass rate > 99 % (covers status 200, `Cache-Control: public`, non-empty `ETag`)

This profile answers: *does the service scale reads efficiently, with correct caching headers, under a sustained high-RPS read load?*

### Profile 3 — Refresh burst smoke

Sends `POST /quotes/refresh` at sustained RPS (default 100 req/s) distributed across all whitelisted pairs for 30 seconds. After the run, `teardown` reads `/metrics` and asserts that the `quote_jobs_pending_count` gauge is below 50, confirming the worker is draining the queue in time.

Thresholds that must pass:
- `http_req_failed` rate < 1 %
- `POST /quotes/refresh` p(99) < 300 ms
- `checks` pass rate > 99 % (covers both 202 status checks and the pending-count assertion)

The teardown logs the actual pending count (`teardown: quote_jobs_pending_count = N`) for diagnosis. On slow hardware the count may approach the 50-job threshold; a re-run after the queue catches up is acceptable.

This profile answers: *does the worker keep up with a sustained refresh ingest rate, and does the queue drain cleanly after the burst ends?*

### Profile 5 — Failure injection

Sends a 80/20 mix of `GET /quotes/latest` and `POST /quotes/refresh` at 25 req/s (overridable). By default (zero latency) this runs as a lightweight baseline smoke test. The load-bearing assertion is that the same thresholds pass even when the upstream fake provider is configured with injected latency.

**Operator precondition for latency injection:**

```bash
FAKE_LATENCY_MIN_MS=500 FAKE_LATENCY_MAX_MS=2000 docker compose up -d
make loadtest-fail
```

Thresholds that must pass (both with and without injection):
- `http_req_failed` rate < 1 %
- `GET /quotes/latest` p(99) < 200 ms
- `POST /quotes/refresh` p(99) < 300 ms
- `checks` pass rate > 99 %

**Scope:** latency injection only in this iteration. Full chaos modes (5xx, 401 auth, silent drop, partial response, connection refused, malformed JSON) require fakeprovider features not yet implemented.

This profile answers: *does the service's caching layer insulate read latency from slow upstream calls, and does `POST /quotes/refresh` meet its SLA despite injected provider latency?*

## Reference

See [docs/discussions/load-testing.md](../docs/discussions/load-testing.md) for the full load-testing strategy, profile descriptions, and CI integration plan.
