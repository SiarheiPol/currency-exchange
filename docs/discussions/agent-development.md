# Agent development principles

## Status
Active. Loaded by all agent entry points (CLAUDE.md, AGENTS.md, GEMINI.md, copilot-instructions, Cursor rules).

## Context

Code in this project is written by AI agents and reviewed by humans. The methodology is **spec-first TDD with AI agents**: a yaml or schema is the contract, tests are written first, implementation follows. The principles in this document make that methodology work — primarily by replacing human discipline with structural and behavioral safeguards.

This document is the **index and the rationale**, not the implementation. Each principle below has a concrete realisation in a domain document; the table at the end maps principles to implementations.

## Core principle: discipline → automation

Anything that requires "remember to do X" from a developer is unreliable when the developer is an AI agent that does not retain context between PRs. Such rules must be:

- **Enforced by tooling** (lint, compiler, codegen, CI), or
- **Eliminated by wrapping** the action in a helper, or
- **Surfaced in the PR diff** through constants files, schemas, or generated code.

If none of those apply, the rule is **behavioral**. Behavioral rules require explicit instruction to the agent and post-hoc verification by the reviewer. They are weaker than structural rules and used only when structural enforcement is impossible.

---

## Structural principles

Structural principles are enforced by tooling. Violation is impossible without breaking the build, lint, or CI.

### 1. Single source of truth

Names, contracts, and configurations have one defined location. Drift is impossible because changing them anywhere else is impossible — either it does not compile, or it is auto-generated from the source location.

Examples in this codebase:
- HTTP API contract: `api/openapi.yaml`.
- Log message strings: `internal/obs/events.go`.
- Metric names: `internal/obs/metrics.go`.
- Database schema: SQL migrations.

When the agent needs to add a new endpoint, message, metric, or column, it edits the single source location. The change is visible in PR review.

### 2. Wrapper-driven defaults

Helpers do the right thing automatically. Direct API access is blocked by lint or convention.

Examples:
- Logger access: `obs.Logger(ctx)` only; raw `slog.Info` rejected by `forbidigo`.
- Outbound HTTP: `RatesProvider` interface, not `http.Client.Do` directly.
- Tests use seams (`memQueue`, `fakeRatesProvider`, `fakeClock`), not direct dependencies.

The agent does not need to remember "always log duration around upstream calls". A helper does it.

### 3. Mechanical enforcement

`make check` is the single command that verifies a PR. It runs:
- Code generation (`go generate ./...`).
- Diff check (`git diff --exit-code`) — catches forgotten regeneration.
- Tests (`go test ./...`).
- Linters (`golangci-lint run`).

CI runs `make check` and fails the PR on any error. This is the **primary** safety net for agent-written code, not a backup. The agent runs `make check` before committing; the human reviewer trusts that it passed.

### 4. Reproducibility

- `go.mod` and `go.sum` committed.
- Generated code (`internal/api/oapi_gen.go`, etc.) committed and verified by `git diff --exit-code` in CI.
- Test fixtures live in `testdata/` directories or in code, not in external resources.
- Container images pinned by digest, not floating tags.
- Local development runs against a fake rates provider (Stage 5 of `implementation-roadmap.md`); the real upstream requires explicit env override.

The agent can reproduce any failure deterministically. There is no "works on my machine" gap.

---

## Behavioral principles

These cannot be enforced by tooling alone. They require explicit instructions to the agent and verification by the reviewer.

### 1. Honest reporting

**Agent must report negative results verbatim, not soften them.**

Specifically:
- If tests fail, agent must report the exact failing test name and the assertion message. Not "mostly passing" or "minor issues".
- If a build emits warnings, they must be reported in full, not labelled "non-blocking" without explicit justification.
- If acceptance criteria are partially met, agent must say "task partial" with an explicit list of incomplete items, not "done" with caveats buried at the end.
- If agent self-corrects a mistake mid-task, the correction must be reported, not silently overwritten.
- Agent must not skip running tests to avoid known failures.
- Agent must not cherry-pick logs to omit unfavorable lines.

### 2. CI is the source of truth, not agent narrative

The CI pipeline output is authoritative. Agent text is a summary; the human reviewer trusts CI logs over agent claims. Agent must structure PR descriptions so the explicit pass/fail status is visible at the top, not buried in detail at the end.

### 3. Test failures as feedback signal

Test failures, panicked handlers, and structured logs are the agent's primary feedback channel for runtime behavior. Agent must read this feedback **fully**, not skim, and respond to all signals — including those that suggest "the work is not done" even when other parts pass.

### 4. No silent scope changes

If the task as written cannot be completed (missing context, contradiction, ambiguity), agent must surface this explicitly and request clarification, not silently rescope or invent assumptions.

If agent decides during the work that a different approach is better, this decision must be reported and accepted before continuing — not made silently.

---

## Mapping principles to implementations

| Principle | Implemented in |
|---|---|
| Single source of truth — API contract | `openapi.md`, `api-contract.md` |
| Single source of truth — observability | `monitoring.md` |
| Wrapper-driven defaults — logging | `monitoring.md` |
| Wrapper-driven defaults — testing seams | `testing-strategy.md` |
| Wrapper-driven defaults — outbound HTTP | `background-mechanism.md` |
| Mechanical enforcement — `make check` | `openapi.md` |
| Mechanical enforcement — lint rules | `monitoring.md` |
| Reproducibility — generated code | `openapi.md` |
| Reproducibility — fake provider | `implementation-roadmap.md` (Stage 5) |
| Honest reporting | this document |
| CI as source of truth | this document |
| Test failures as signal | this document, `testing-strategy.md` |
| No silent scope changes | this document |

When a principle is updated here, downstream implementations may need updating. Conversely, when an implementation changes, this document remains valid as long as the principle still holds.

---

## Risks of this approach

This methodology has known weaknesses, recorded for honesty:

- **Cargo cult.** "We follow the principles" can become a slogan without practice. Mitigated by tying every principle to a concrete implementation in a domain document.
- **Out of context.** Agents do not always load this document into working context. Mitigated by linking from `AI_CONTEXT.md` (auto-loaded by Claude Code, copied to other agent entry points by `scripts/sync-agents.sh`).
- **Behavioral rules cannot be lint-checked.** Honest reporting depends on agent following instructions. Mitigated by reviewer skepticism, CI-as-truth, and explicit PR templates.
- **Doc maintenance.** Yet another file to keep in sync. Mitigated by keeping this document focused on principles only, with implementations elsewhere.

---

## Not in scope here

- **Specific tooling configurations** (`.golangci.yml`, `Makefile`) — live in implementation docs.
- **General Go conventions** (formatting, naming, package layout) — live in `../conventions.md`.
- **Project-specific rules** (do not mention the company name, agent-sync workflow) — live in `docs/rules/`.
