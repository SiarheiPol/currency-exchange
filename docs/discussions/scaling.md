# Scaling

## Status
Overview. Concrete sizing for MVP scale lives in `capacity.md`.

## Context

`capacity.md` covers per-instance tuning for given load. This document covers the **architectural shifts** required as load grows past what one instance, one database, or one region can handle. It is an overview — a forward-looking map, not an implementation plan. Most of what is here is **out of MVP scope**.

The MVP is Stage A (single instance). Stage B (multi-instance) is reachable without code changes. Stages C–F are deliberate extensions, each justified by a concrete bottleneck.

## Three load axes

The service has three independent scaling dimensions:

| Axis | What grows with it | First bottleneck |
|---|---|---|
| HTTP request rate | inbound `POST /quotes/refresh`, `GET /quotes/:id`, `GET /quotes/latest?base=BASE&quote=QUOTE` | service instances, DB connection pool |
| DB write throughput | `quote_jobs` inserts, `quotes` upserts | Postgres primary write capacity |
| DB read throughput | `GET /quotes/latest?base=BASE&quote=QUOTE` and `GET /quotes/:id` reads | Postgres primary → read replica |

**Upstream provider call rate is not a scaling axis.** Coalescing with bucket size `W` caps it at `1/W` calls per currency. With `apilayerProvider` grouping pairs by base and issuing one HTTP call per unique base, the absolute ceiling is **2 × len(whitelist) calls per minute when `W=30s`** (i.e., 6 HTTP calls per minute for [USD, EUR, MXN]), regardless of user count. Concrete numbers per tariff plan are in `capacity.md > Upstream call ceiling under coalescing` (Free plan: any source allowed empirically — the documented USD-source restriction does not apply in practice).

## Read pressure dominates at scale

Non-obvious observation: in this service, **read load grows faster than write load** as users multiply.

- A user does at most one `POST /quotes/refresh` per session. Many users only hit `/quotes/latest`.
- A user can hit `/quotes/latest` many times per session (UI ticker, mobile dashboard).
- The async pattern adds **`/quotes/:id` polling** RPS — each refresh produces several polls until the job goes terminal.

At 50M users (rough): refresh ~500–1500 RPS, `/quotes/:id` polling ~5 000–15 000 RPS, `/quotes/latest` ~5 000–40 000 RPS (highly dependent on UI cache discipline and CDN/BFF caching), upstream calls bounded at ~0.03/sec. The architecture prioritises read scalability because that is where pressure rises.

## Stages

Each stage is an architectural shift. Move to the next stage only when a concrete bottleneck appears.

### Stage A — single instance (MVP)

One service pod, one Postgres primary. Capacity: ~100–500 RPS depending on container size (see `capacity.md`).

### Stage B — multi-instance, single region

2–5 service pods behind a load balancer; single Postgres primary. **No code changes** are needed under stable upstream — `FOR UPDATE SKIP LOCKED` and the unique `dedup_key` index make the architecture multi-instance-ready from MVP. Each pod has its own scheduler tick; the dedup index collapses simultaneous ticks to one job.

Triggers for entry: HTTP `p99` rising under load, single-pod restart causing user-visible downtime, or Stage A pod fully utilised.

**Scheduler in multi-instance.** Each pod ticks the scheduler independently. This is a deliberate choice, not an oversight: the unique partial index on `dedup_key` collapses N simultaneous INSERT attempts into one job at the cost of N−1 unique-violation errors per tick. At the expected `N=2–5` and `T=60s` (or larger), this is microseconds of CPU and zero database pressure — cheaper than any coordination mechanism. Alternatives are leader election via `pg_try_advisory_lock` inside `scheduler.Tick`, or splitting roles into separate processes (`--role=api|worker|scheduler`) with `replicas=1` for the scheduler. Consider them only when: (a) pod count exceeds ~10 and dedup-key unique violations become visible in Postgres metrics, (b) api and worker need to be scaled very differently (e.g. upstream-bound load makes extra worker pods useless), or (c) failure-domain isolation between roles becomes a hard requirement. None of these triggers appear within the load envelopes projected in `capacity.md`.

**Upstream-stability caveat.** If multi-instance deployment coincides with recurring upstream failures, retry amplification across pods can hurt the upstream and our quota. In that case, jump to Stage 6 circuit breaker work (`resilience.md > Circuit breaker`) alongside Stage B. At 2–3 pods with stable upstream, the breaker is borderline; at more pods or with frequent outages, it becomes essential.

### Stage C — read replica for `/latest`

`GET /latest` reads from a Postgres read replica. `GET /quotes/:id` and writes stay on the primary (job state must be current; replica lag would show stale `pending` for an already-`done` job). Service config gets two DSNs.

Replica lag of <1s is invisible: `/latest` already serves data up to `W` seconds old by design. CDN/BFF caching of `/latest` (see below) reduces this need significantly.

Triggers: primary CPU saturated on reads, read latency rising, or read RPS so high that even a tuned primary cannot keep up.

### Stage D — table partitioning

Partition large tables by time:
- `idempotency_keys` (when Stage 6 classical idempotency lands) by day — daily partition `DROP` instead of vacuum cycles.
- `quote_jobs` by month — archival and retention via partition swap.

Triggers: tables in tens of millions of rows, vacuum/maintenance becoming a problem.

### Stage E — multi-region active-passive

Two regions: one active, one hot standby. Postgres replication keeps the passive region warm. DNS or anycast failover on regional outage. RTO: minutes. RPO: seconds (async replication is fine because `quotes` is rebuildable).

Triggers: regional outage tolerance becomes a requirement; users in other geographies start complaining about latency.

### Stage F — multi-region active-active

Both regions serve traffic. Recommended approach for our service: **region-independent state**. Each region has its own `quotes`, `quote_jobs`, `upstream_quota`. Each region's scheduler hits the upstream independently, doubling upstream cost. This is dramatically simpler than multi-master replication of write-heavy `quote_jobs`.

Multi-master replication (CockroachDB, BDR) is also possible but wins only for very large global scale (100M+ users) and adds significant complexity.

Triggers: 100M+ users globally, p99 latency targets that cross-region cannot satisfy, or data-residency regulation.

## Stage transitions summary

| From → To | Trigger | Code changes |
|---|---|---|
| A → B | latency rising, single-point-of-failure unacceptable | none — already multi-instance ready |
| B → C | Postgres CPU saturated on reads | dual DSN config, route reads through replica |
| C → D | tables in millions of rows, vacuum slow | partitioned table migration, partition-creation cron |
| D → E | regional outage tolerance required | DNS failover, replica config in passive region |
| E → F | global low-latency, very high scale | per-region tariff plans, scheduler-per-region, deployment complexity |

Skip stages only with deliberate justification — each one solves a real problem and adds operational cost.

## Levers that defer the next stage

Before jumping to the next stage, consider whether these reduce the pressure:

**CDN/BFF caching of `/latest`.** Our `Cache-Control: public, max-age=W` directive lets any HTTP cache layer (BFF, gateway, CDN) absorb most read traffic. At 50M users with `W=30s`, a single BFF cache instance can drop our `/latest` RPS by 90%+. This is the cheapest read-scaling lever and requires zero changes in the service.

**Tuning before scaling.** Bigger DB connection pool, bigger pod CPU/memory limit, bigger Postgres instance — these are configuration changes, not architectural shifts. Try them first.

**Aggressive cleanup.** Old `quote_jobs` rows do nothing useful after a day; deleting them keeps the index slim and the table fast. A scheduled `DELETE` is cheaper than partitioning until it isn't.

## When to introduce a message broker

**Not in MVP.** Coalescing into the Postgres queue handles MVP and most production scales.

### Concrete trigger

The signal: **Postgres `INSERT ... ON CONFLICT` on `quote_jobs` p95 latency exceeds 50ms** under steady load, and the queue depth (`quote_jobs_pending_count`) sustains above the alerting threshold.

Why this is the right signal: coalescing protects the upstream provider, but every `POST /quotes/refresh` still attempts an insert against the small set of hot `dedup_key` values. At 1000+ RPS with `W=30s` (~5 hot keys at any moment for 3 currencies × ~couple of buckets), the unique-index contention can become the bottleneck even though only one job is created per bucket.

### Softer mitigations to try first

Before committing to a broker:

- **In-process singleflight** before the DB call. The handler hashes `dedup_key`; if another goroutine on the same pod is already inserting that key, wait for its result. Reduces DB attempts but only within the pod.
- **Gateway coalescing.** The BFF/gateway in front of the service collapses simultaneous refreshes into one downstream call. Removes the contention from the service entirely.
- **Larger `W`.** If freshness allows, doubling `W` halves the hot-key insert pressure.

### When the broker is the right answer

After the softer mitigations are exhausted and the trigger persists, a broker (Kafka, NATS, RabbitMQ) replaces the `INSERT` contention with append-mostly publish. Consumers (workers) read from the broker; the existing `quote_jobs` table becomes pure audit / lifecycle state.

A broker is a significant architectural change — new dependency, new failure modes, idempotent publish, at-least-once consume, monitoring lag. Defer until measured.

## AI-agent considerations

This document realises the structural principles from `agent-development.md`:
- **Reproducibility** — local development is always Stage A. Stage transitions are explicit deployment changes, not silent code drift.
- **No silent rescoping** — if an agent observes load nearing a stage transition, it must surface the recommendation explicitly, not preemptively introduce read replicas without alignment.

## Not in scope here

- **Per-instance sizing** (CPU, memory, pool limits) — `capacity.md`.
- **Failure recovery semantics** — `resilience.md`.
- **Load testing methodology** — `load-testing.md`.
- **Deployment automation** (Terraform, Helm, CI) — out of scope; lives in deployment repo.
- **Cost optimisation** (reserved instances, spot pricing) — operational concern.
- **Detailed CDN configuration** (Vary headers, surrogate keys) — deployment concern when CDN is added.
