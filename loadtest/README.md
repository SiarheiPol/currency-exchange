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
LOADTEST_DURATION=10s make loadtest    # override duration
LOADTEST_DURATION=30m make loadtest    # full tier
```

The k6 container is started on demand under the `loadtest` compose profile and removed automatically after the run (`--rm`). No k6 installation on the host is required.

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

## Reference

See [docs/discussions/load-testing.md](../docs/discussions/load-testing.md) for the full load-testing strategy, profile descriptions, and CI integration plan.
