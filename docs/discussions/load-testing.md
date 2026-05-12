# Load testing

## Status
Overview. Load testing itself is Stage 6 in `implementation-roadmap.md`; it depends on the fake rates provider that lands in Stage 5.

## Context

Load testing for this service has two purposes:

1. **Validate capacity claims.** `capacity.md` has estimated numbers (1000 RPS per core, etc.). Load testing confirms or replaces these with measured values.
2. **Find break points before users do.** Sustained load reveals queue lag, GC pauses, connection-pool exhaustion, slow-query plans — issues that unit and integration tests miss.

This is not a substitute for unit/integration tests, which validate **correctness**. Load tests validate **behaviour under stress**.

The core dependency is the fake rates provider (Stage 5 in `implementation-roadmap.md`). Without it, every load test burns real upstream quota — economically and ethically unacceptable. The fake provider lets us simulate any tariff plan, including Enterprise-level quotas we cannot purchase.

## Tooling

| Tool | When to use |
|---|---|
| **k6** (recommended) | full scenarios with JS-style scripting, CI-friendly output, good docs |
| **vegeta** | simple constant-rate attacks for fast smoke tests |
| **hey** / **wrk** | ad-hoc benchmarks during development |

Default choice: **k6**. JavaScript scenarios are readable; checks and thresholds are first-class; output integrates with Prometheus and Grafana directly.

Test files live in `loadtest/` directory at the repository root, not in `internal/`.

## Profiles

Five distinct profiles, each answering a different question.

### 1. Sustained baseline

Hold a constant load (e.g., 100 RPS mixed `/latest` and `/refresh`) for 30 minutes. Confirms steady-state behaviour: no leaks, no queue growth, GC stable.

**Question answered:** does the service hold up under realistic continuous load?

### 2. Read storm

Hammer `/latest` at 5 000+ RPS. `/refresh` minimal or zero. Confirms read-path scalability and Cache-Control behaviour.

**Question answered:** how does single-instance read capacity scale with CPU?

### 3. Refresh burst

Sustained burst of `POST /refresh` for many distinct currencies (or repeated for the same currency to verify coalescing). Validates queue throughput, dedup-index contention, worker drain rate.

**Question answered:** how many refresh requests per second can the queue absorb before pending count grows uncontrolled?

### 4. Coalescing stress

Many concurrent refresh calls for the **same currency** within one bucket. Should produce exactly one upstream call regardless of caller volume.

**Question answered:** does coalescing actually save upstream calls under realistic concurrency?

### 5. Failure injection

Use the fake provider's plan-simulation features to inject:
- High latency (500ms–5s response).
- Random `5xx` responses.
- `success: false` with API code 104 (quota exceeded).
- `success: false` with API code 101 (invalid key — simulates auth failure).
- `success: false` with API code 202 (if provider supports it; otherwise silent currency drop).
- Partial response (some pairs returned, others silently dropped — primary error path).
- Total provider unavailability (connection refused).
- Malformed JSON response.

Validates retry budget, backoff behaviour, and graceful degradation. Pairs naturally with `resilience.md` decisions.

**Question answered:** does the service degrade gracefully when upstream misbehaves?

## What we measure

Each profile reports the same set of metrics:

| Metric | Source | Target |
|---|---|---|
| HTTP `p50` / `p95` / `p99` latency | k6 + Prometheus | TBD per profile |
| HTTP error rate | k6 + Prometheus | < 1% under normal profiles |
| `quote_jobs_pending_count` peak | Prometheus | < `whitelist × N` for sustained |
| Worker iteration rate | Prometheus | matches expected throughput |
| `rates_provider_requests_total` | Prometheus | profile-dependent (coalescing should keep it low) |
| Service CPU usage | Prometheus | < container limit |
| Service memory peak | Prometheus | within expected envelope |
| Postgres connections in use | Prometheus | < pool size |
| Postgres slow queries | `pg_stat_statements` | none > 50ms in normal profiles |

The three dashboards from `monitoring.md` (Service / Queue / Upstream health) are the primary view during a test.

## Thresholds (pass/fail)

Each profile defines thresholds in the k6 script. Examples:

```javascript
export const options = {
    thresholds: {
        'http_req_duration{name:GET /latest}': ['p(99)<200'],
        'http_req_failed': ['rate<0.01'],
        'checks': ['rate>0.99'],
    },
};
```

CI runs profiles 1 (sustained) and 4 (coalescing stress) on every PR — fast checks. Profiles 2, 3, 5 run nightly or on demand because they take longer and need more headroom.

## Output: capacity numbers

The deliverable from running these profiles is a **measured capacity table** that replaces estimates in `capacity.md`. Concretely:

- Realistic RPS per pod size, with measured `p99` latency at each level.
- Worker throughput (jobs/sec per worker) under typical and stressed conditions.
- Coalescing collapse ratio under refresh-burst (validates the design assumption).
- Time-to-recover after upstream outage (validates retry budget).

These numbers go into `capacity.md` to replace the current "1000 RPS per core" estimate.

## CI integration

Two layers:

**Smoke tier** — runs on every PR, takes <2 minutes:
- Profile 1 at 50 RPS for 30 seconds.
- Profile 4 (10 concurrent refreshes for same currency, verify single upstream call).

**Full tier** — runs nightly or on a `loadtest` label, takes 10–30 minutes:
- Profiles 1–5, full duration, full thresholds.

Smoke tier catches obvious regressions (a PR that breaks dedup will fail the coalescing check). Full tier catches subtler issues (memory leak only visible after 20 minutes).

## Local development

Developers run load tests locally via the `loadtest` Compose profile. There is no per-profile `make` target — the canonical entry points are:

```bash
# Canned demo: bring up the stack with business-like settings and run
# profile 2 at 5000 RPS for 2 minutes (one command).
make demo

# Ad-hoc per-profile runs against an already-running stack:
docker compose --profile loadtest run --rm k6 run /scripts/profile1.js  # baseline
docker compose --profile loadtest run --rm k6 run /scripts/profile3.js  # refresh burst
docker compose --profile loadtest run --rm k6 run /scripts/profile5.js  # failure injection

# Override rate / VUs / duration via shell env (compose interpolates):
LOADTEST_RATE=20000 LOADTEST_VUS=1500 LOADTEST_DURATION=120s \
  docker compose --profile loadtest run --rm k6 run /scripts/profile3.js
```

The k6 container shares the Compose default network so it reaches the server at `http://server:8080` without host port mapping. Results print to stdout; Grafana dashboards are available at `localhost:3000`.

## AI-agent considerations

This document realises the structural principles from `agent-development.md`:

- **Reproducibility** — load tests run against the fake provider; results are deterministic enough to compare across runs (same seed in fake provider).
- **Honest reporting** — when load tests reveal a regression, the agent must report the **specific** profile and threshold that failed, not a generic "load test failed".
- **CI as source of truth** — the threshold values in k6 scripts are the contract; passing locally but failing in CI is a CI-truth issue.

## Not in scope here

- **Specific RPS targets and latency budgets.** Filled in once profiles are run; for now, placeholders.
- **Performance optimisation.** This document is about **measuring**, not improving.
- **Stress beyond normal operating range.** Chaos engineering, region failure, full DB outage — separate exercise; lives with the resilience playbook in the deployment repo.
- **Production load testing.** Running profiles against production traffic patterns is a separate operational discipline; this document covers staging/local only.
