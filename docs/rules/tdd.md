# TDD

Code in this project is written test-first. The cycle is **RED → GREEN → (refactor)**, and the phases are kept structurally separate so that a test never gets adjusted to match the code that was written for it.

## Phases

### RED — write a failing test

- A test exists and fails for the right reason (asserts the new behaviour, not a typo or missing import).
- Failure is confirmed by actually running `go test` (or `make test`) against the new test.
- The verbatim failure output is the artefact that closes this phase. No paraphrasing.

### GREEN — minimal code to pass

- Only enough production code to make the failing test pass. No speculative additions.
- All previously-green tests stay green; new test moves from RED to PASS.
- Verbatim `make check` (or `go test ./...`) output is the artefact that closes this phase.

### Refactor — optional

- Tests stay green throughout. If a refactor breaks a test, the refactor is wrong, not the test.
- Skipped when not needed. Refactoring "just because" is a scope change.

## Hard rules

- **No production code without a failing test.** If a behaviour is worth writing, it is worth pinning with a test first.
- **Test files and production files are edited in separate phases.** During RED, only test files change. During GREEN, only production files change. Mixed edits in one phase are a process violation — they hide whether the test was actually written first.
- **The implementer never modifies tests.** If a test needs to change (the contract was wrong), the implementer stops and hands back to the spec-author. See [orchestration.md](orchestration.md).
- **The test-writer never modifies production code.** If the test cannot be expressed against the current API, the contract from the spec-author is incomplete — surface it, do not paper over with a helper in production code.

## Test scope

- **Unit-level seams are preferred** (`memQueue`, `fakeRatesProvider`, `fakeClock` — see `discussions/testing-strategy.md`). They run fast and keep the RED→GREEN loop tight.
- **Integration tests** (`-tags integration`, real Postgres via testcontainers) are required when the behaviour lives at the boundary — `pgQueue`, migrations, real upstream client.
- A behaviour covered by an integration test does not also need a duplicating unit test. Pick the level where the behaviour actually exists.

## Test ROI — when not to write a test

A test earns its place by catching realistic regressions that the **compiler, type system, and code review would miss**. Before writing a test, ask: *if I deleted this test, could a real bug slip through undetected?*

Skip a test when it only mirrors the implementation's structure:
- Asserting that a constant equals the string literal written right next to it.
- Asserting that a field name or function parameter matches a string literal in the implementation.
- Asserting behaviour that is trivially derived from the type signature (e.g. a getter returns what was passed to a constructor with no transformation).

Write a test when it pins **logic**:
- Conditional behaviour (`err != nil` → different level/output).
- State transitions (job moves from pending → running → done).
- Error paths and fallbacks (nil context value → falls back to default).
- Non-obvious transformations (duration → milliseconds, dedup key derivation).
- Concurrency invariants (only one inserter wins the dedup race).

The spec-author must apply this filter when designing the test plan. The test-writer must apply it before writing each case.

## What counts as a violation

- Pushing an implementation commit without a corresponding test in the same change set.
- Modifying both a test and the code-under-test in the same phase to "fix" a failure.
- Marking a task done while any test is skipped, commented out, or `t.Skip`-gated without an explicit reference to a follow-up roadmap item.
- Reporting "tests pass" without the verbatim command output. Honest reporting from `discussions/agent-development.md` applies.

## Why this is a rule, not a guideline

`discussions/agent-development.md` requires that discipline be replaced by structure wherever possible. TDD here is enforced through **role separation** (test-writer vs. implementer — see [orchestration.md](orchestration.md)) and through **artefact requirements** (verbatim RED and GREEN output in the handoff). Neither is enforceable by lint, so the cycle is documented here and surfaced in every iteration's handoff.
