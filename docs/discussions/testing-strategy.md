# Testing strategy

## Status
Decided.

## Context

Development follows a TDD discipline: tests written first, then code, then refactor. For this to work, the codebase must have clear seams (interfaces) for the moving parts that tests need to control: queue, rates provider, clock, id generator.

The seams are already defined in other documents:
- `JobQueue` (in `background-mechanism.md`) — has both a real `pgQueue` and a fake `memQueue`.
- `RatesProvider` (in `background-mechanism.md`) — will have a real implementation per provider plus a test fake.
- `Clock` and `IDGenerator` — needed for deterministic tests of bucket math and job ids; tiny interfaces.

This document describes how we use these seams, which kinds of tests we write, and what we do not test.

## Test pyramid

```
            ┌────────┐
            │  e2e   │   few, slow, full stack
            └────────┘
          ┌────────────┐
          │ integration │  medium, real Postgres via testcontainers
          └────────────┘
        ┌────────────────┐
        │      unit       │  many, fast, fakes for all I/O
        └────────────────┘
```

### Unit tests

Cover handlers, service layer, queue logic against `memQueue`, scheduler with fake `Clock` and fake `RatesProvider`.

- Run in milliseconds.
- No network, no filesystem, no Postgres.
- Use `httptest` for handler tests.
- Use `memQueue` and other in-memory fakes for everything stateful.

### Integration tests

Cover `pgQueue` against a real Postgres instance, the full HTTP server with a real database, and the `RatesProvider` against a stub HTTP server (`httptest.NewServer`).

- Run in seconds (testcontainers spin-up plus real SQL).
- Behind a build tag (`//go:build integration`) so `go test ./...` stays fast by default.
- One Postgres container shared across the package via `TestMain`.
- **Data isolation: schema-per-test.** Each test creates its own Postgres schema (`CREATE SCHEMA test_<name>`), runs migrations into it, and drops it on teardown. The container stays warm between tests; only schemas come and go. Tx-rollback isolation is not enough — `pgQueue` relies on `FOR UPDATE SKIP LOCKED` semantics that exist only across separate transactions, which a single rollback transaction cannot exercise. `TRUNCATE` works but serialises the test package; schema-per-test stays parallel-safe (`t.Parallel()`) at the cost of a few ms of schema setup per test.

### End-to-end tests

Optional in MVP. The integration tests already cover the full HTTP-to-DB path. A separate e2e tier is justified only if we add docker-compose with the rates provider stubbed, multiple service replicas, and a load balancer — out of scope here.

## Stack

| Need | Choice |
|---|---|
| Assertions | `github.com/stretchr/testify/assert` and `require` |
| HTTP handler tests | stdlib `net/http/httptest` |
| Postgres in integration tests | `github.com/testcontainers/testcontainers-go` |
| HTTP stubs for outbound calls | stdlib `httptest.NewServer` |
| JSON comparison | `assert.JSONEq` |
| Time control | hand-written `Clock` interface with `realClock` and `fakeClock`. Migrate to `github.com/benbjohnson/clock` when our fake needs `Timer`/`Ticker`/sleep coordination beyond plain `Advance(d)` — duplicating that library's API is the trigger. |

We do **not** use:
- `gomock`, `mockery`, or other mock generators. A 30-line hand-written fake is simpler and easier to read in test failures.
- BDD frameworks (`ginkgo`). Plain `go test` with table-driven tests is idiomatic and enough.
- Property-based testing for MVP. Could be added for the bucket-math and dedup logic later.

## Mocks vs fakes

Default: **fakes**, not mocks.

A **fake** is a working, in-memory implementation of an interface. It maintains state and behaves like the real thing for the purposes of the test. Examples: `memQueue` stores jobs in a map and supports the full `JobQueue` contract.

A **mock** records calls and asserts on them. It does not behave like the real thing.

Reasons to prefer fakes:

- Tests describe **behavior**, not call sequences. `assert.Equal(t, "done", job.Status)` reads better than `mock.AssertCalled(t, "Complete", id)`.
- Fakes are reusable across many tests; mocks need per-test setup.
- A fake forces us to think through the contract once. After that, every test that uses it is consistent.
- Fakes can be shared with consumers as documentation of the contract.

We use mock-style assertions only when call ordering or absence-of-calls is the actual thing under test (rare).

## Test seams

Every external dependency goes through an interface. Each interface has a real implementation and a test fake.

| Interface | Real | Fake |
|---|---|---|
| `JobQueue` | `pgQueue` (Postgres) | `memQueue` (sync map) |
| `RatesProvider` | `apilayerProvider` | `fakeRatesProvider` (three test patterns: success / batch-failure / partial-success with missing-pair detection) |
| `Clock` | `realClock` (`time.Now`) | `fakeClock` (advanced manually in tests) |
| `IDGenerator` | `uuidGenerator` (`uuid.New`) | `seqIDGenerator` (deterministic counter) |
| `QuoteRepo` (read side for `/latest`) | `pgQuoteRepo` | `memQuoteRepo` |

`Clock` and `IDGenerator` look like overkill, but they make bucket-math and id-equality tests deterministic. Without them, every `time.Now()` and `uuid.New()` call sneaks non-determinism into tests.

**`fakeRatesProvider` test patterns:**

1. **Success.** Returns pre-configured `FetchResult.Quotes` covering every requested pair; `Missing` is empty. Used in happy-path worker and scheduler tests.
2. **Batch-failure.** Returns a typed `*ProviderError` as the Go `error` (simulates network failure, auth error, quota exhaustion). All reserved jobs will be rescheduled or failed by the worker according to `ProviderError.IsTransient()`.
3. **Partial-success with missing-pair detection.** Returns `FetchResult` where some pairs are in `Quotes` and the rest appear in `Missing` (simulating silent drop of a requested pair by the upstream). Tests the worker's permanent-fail dispatch path for missing pairs — this is the **primary error path** given empirically confirmed silent-drop behaviour.

The fake is configured per-test, not globally. Each test sets exactly the pattern it needs.

## What we test

### Handler-level tests (unit + integration)

For each endpoint, the edge-case matrix from `api-contract.md` is the test seed. One row, one test case. Unit-level tests cover validation, error envelopes, status codes, and `Cache-Control` headers. Integration-level tests cover end-to-end flow including DB writes.

### Queue contract tests

`JobQueue` has a contract test suite that runs against both `memQueue` (unit, fast) and `pgQueue` (integration, real Postgres). Same tests, different backend. This is the most reliable way to ensure the in-memory fake stays faithful to the real implementation.

### Worker lifecycle tests

Reserve/Complete/Reschedule/Fail transitions. Crash recovery via lease expiration. Backoff math. All testable against `memQueue` with `fakeClock` advancing manually.

### Scheduler tests

With `fakeClock` and `fakeRatesProvider`, we test:
- Bootstrap tick fires at startup.
- Subsequent ticks fire at the configured `T` interval.
- Each tick enqueues exactly one job per whitelist pair (6 jobs for [USD, EUR, MXN]).
- Same-bucket dedup returns existing ids for the same `(base, quote, bucket)`.

### Coalescing tests

Bucket math (`floor(now/W)*W`) and `INSERT ON CONFLICT` race resolution. Unit-level with `fakeClock`; integration-level for the real partial unique index behavior.

### `/latest` cache-headers tests

Each branch of the Cache-Control table from `api-contract.md` becomes a test. Cold-start 404 path included.

## What we do not test

- **`main.go`.** It is dependency-injection wiring. Tested implicitly by integration and e2e tests; explicit unit tests would be brittle and add no value.
- **Config loading.** Trivial env-var reading. If it gets non-trivial later (validation, defaults, profile-based selection), add tests then.
- **Migrations.** SQL is run by an external tool (`golang-migrate` or similar). Running migrations is part of the integration test setup, but the SQL itself is not "code under test".
- **Generated code (`oapi_gen.go`).** We trust `oapi-codegen`. Its output is data, not logic.
- **Third-party libraries.** Standard hygiene: do not test stdlib or vendored code.

## Test layout

```
internal/
  api/
    server.go
    server_test.go              // unit: handler tests with fakes
    server_integration_test.go  // integration: full stack, build tag
  queue/
    pgqueue/
      pgqueue.go
      pgqueue_test.go           // integration: against testcontainers
    memqueue/
      memqueue.go
      memqueue_test.go          // unit: pure in-memory
    contract_test.go            // shared contract suite, run by both backends
  worker/
    worker.go
    worker_test.go              // unit: with memQueue + fakes
  scheduler/
    scheduler.go
    scheduler_test.go           // unit: with fakeClock + memQueue
  ratesprovider/
    apilayer/
      apilayer.go
      apilayer_test.go          // unit: with httptest stub server
    fake/
      fake.go
      fake_test.go              // unit: three-mode fake behaviour tests
```

Tests sit next to the code they cover. Black-box tests use `package <name>_test` when imports would otherwise be circular or when we want to test only the public API.

## Build tags

Integration tests are gated by a build tag at the top of the file:

```go
//go:build integration
```

Default `go test ./...` runs only unit tests (fast). CI runs both with `go test -tags integration ./...`.

This keeps the inner-loop of TDD fast: a developer writing handler logic does not wait 5 seconds per test for testcontainers to spin up.

## Coverage policy

- **No hard line-coverage target.** Coverage numbers are a poor proxy for test quality.
- **Behavior coverage.** Every public path through every endpoint, every queue state transition, every error branch must have at least one test.
- **The api-contract edge case matrix** is a checklist. If a row has no corresponding test, that is a gap.
- **Coverage report** generated by CI for visibility, not as a gate.

## Test naming and organization

- Test functions: `Test<Subject>_<Scenario>`. Example: `TestRefreshHandler_UnsupportedCurrency`.
- Table-driven tests for variations of the same logic. Each row has a `name` field used as the subtest name (`t.Run(tc.name, ...)`).
- Helpers in `*_test.go` files; shared helpers in `internal/testutil` if cross-package.
- `t.Parallel()` enabled by default unless the test mutates a shared resource (e.g., clock manipulation, package-level state).

## Resolved questions

**`updated_at` testing in `/latest` responses.** Closed. Convention: **exact match with controlled time**. The `fakeRatesProvider` returns a deterministic `timestamp` value; the `fakeClock` produces a known `now`. Tests assert the exact JSON including `updated_at` via `assert.JSONEq`. Shape-and-skip is not used — the timestamp wiring is behaviorally meaningful and must be verified.

Trigger for revisiting: an integration test that hits a real (but stubbed-at-HTTP-level) upstream where the timestamp originates from the stub, not from `fakeClock`. In that case, use a known stub timestamp constant.

## Not in scope here

- **Property-based testing.** Useful candidate later for bucket math and dedup edge cases.
- **Mutation testing.** Out of scope.
- **Load and stress tests.** Lives in `load-testing.md`.
- **Chaos / fault-injection tests.** Lives in `resilience.md`.
- **Contract tests against the real upstream provider.** We use `httptest` stubs; a smoke test against the real upstream can be added under a separate build tag if we want it in CI.
