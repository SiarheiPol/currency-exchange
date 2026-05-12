# Load Tests

k6 load test scripts for the Currency Quote Service.

## Precondition

The full compose stack must be running before executing any load test profile:

```bash
docker compose up -d
```

Wait for all services to become healthy (check with `docker compose ps`).

> **Tip:** `make demo` brings the stack up with business-like settings *and* runs profile 2 at 5000 RPS in one go. Use that for a quick all-in-one demo; the rest of this README is for running individual profiles manually.

## Running the tests

The k6 container is started on demand under the `loadtest` compose profile and removed automatically after the run (`--rm`). No k6 installation on the host is required.

```bash
# Profile 1 — sustained 50 RPS, 30s
docker compose --profile loadtest run --rm k6 run /scripts/profile1.js

# Profile 2 — read storm, 500 RPS default
docker compose --profile loadtest run --rm k6 run /scripts/profile2.js

# Profile 3 — refresh burst, 100 RPS default
docker compose --profile loadtest run --rm k6 run /scripts/profile3.js

# Profile 4 — coalescing stress (iteration-count based; ignores rate/duration overrides)
docker compose --profile loadtest run --rm k6 run /scripts/profile4.js

# Profile 5 — failure injection baseline (zero latency by default)
docker compose --profile loadtest run --rm k6 run /scripts/profile5.js
```

### Running with overrides

Three environment variables can be overridden — all profile targets (1, 2, 3, 5) honour them; profile 4 is iteration-count based and ignores them.

| Variable | Default per profile | Purpose |
|---|---|---|
| `LOADTEST_DURATION` | `30s` | Total run length. Use `5m`, `120s`, etc. |
| `LOADTEST_RATE` | profile-specific (50/500/100/25) | Target requests per second. |
| `LOADTEST_VUS` | profile-specific (50/100/50/50) | k6 virtual-user pool size. `maxVUs` is auto-derived as `LOADTEST_VUS × 2` with a floor at the profile default. |

Pass overrides via `-e` to the `run` invocation:

```bash
# High-rate extended run for profile 2
docker compose --profile loadtest run --rm \
  -e LOADTEST_RATE=5000 -e LOADTEST_VUS=1000 -e LOADTEST_DURATION=5m \
  k6 run /scripts/profile2.js
```

#### When to raise `LOADTEST_VUS`

If the k6 summary reports a non-zero `dropped_iterations` counter, k6 ran out of virtual users at the requested rate and the test is **not** an accurate capacity measurement — the service may be able to absorb much more than the reported `http_reqs/s`. Symptom from a sample 20 000 RPS run on `profile3`:

```
http_reqs.........: 1206682 10054.826599/s
dropped_iterations: 1193322  9943.502751/s   ← k6 starved
vus...............: 99      max=100         ← capped at maxVUs
```

Rule of thumb: `LOADTEST_VUS ≥ target_rate × expected_p95_seconds × 2`. For 20 000 RPS at a ~12 ms p95, set `LOADTEST_VUS=1000`. Each VU costs ~1 MB RAM, so pools up to a few thousand are fine.

```bash
LOADTEST_RATE=20000 LOADTEST_VUS=1000 LOADTEST_DURATION=120s make loadtest-burst
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
docker compose --profile loadtest run --rm k6 run /scripts/profile5.js
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
