# Orchestration

How a single iteration moves from a roadmap item to a merged commit. Defines the roles, the artefacts each role produces, and how to run the cycle in different AI tools.

## The cycle

```
roadmap item
    │
    ▼
spec-author ──► contract (what, tests, invariants)        ── user approves
    │
    ▼
test-writer ──► failing tests + verbatim RED output       ── user reviews tests
    │
    ▼
implementer ──► production code, all tests GREEN
    │
    ▼
make check  ──► verbatim PASS output
    │
    ▼
reviewer    ──► report (diff vs. conventions/scope/contract, checkbox check)
    │
    ▼
user approves ──► commit (code + roadmap checkbox in one commit)
```

Each arrow is a handoff. Each handoff carries a concrete artefact. Skipping an artefact (e.g. "tests pass, trust me, no log attached") breaks the cycle.

## Roles

Full prompts live in [.claude/agents/](../../.claude/agents/). Summary:

| Role | Reads | Writes | Refuses |
|---|---|---|---|
| **spec-author** | roadmap, discussion docs, conventions | contract document (chat output) | code, tests |
| **test-writer** | contract, existing code | test files only | production code |
| **implementer** | contract, RED output, production code | production code only | tests |
| **reviewer** | full diff, conventions, scope, roadmap | review report (chat output) | code, tests |

The role separation is the structural enforcement of [tdd.md](tdd.md). A single agent doing two roles defeats the point.

## Running the cycle

### Mode A — Claude Code (native subagents)

The main conversation is the orchestrator. It does not write code itself. It calls each role via the `Task` tool with `subagent_type` matching one of `spec-author`, `test-writer`, `implementer`, `reviewer`. Between handoffs it returns control to the user for approval.

The user types: "next item from Stage N". The orchestrator runs the full cycle, pausing at every approval gate.

### Mode B — Cursor / Copilot / Gemini (single-chat role switching)

No native subagents. The user (or the agent itself, by quoting the role file) explicitly switches roles inside one chat:

> Now act as `test-writer`. Rules: <paste body of `.claude/agents/test-writer.md`>. Contract from previous step: <paste contract>.

Each phase is a clean prompt that includes the role description and the artefact from the previous phase. The role file is the same across tools — only the activation method changes.

### Mode C — Separate sessions (maximum isolation)

Four independent chats, one per role. Handoff artefacts (contract, RED log, code diff, review report) are copied between sessions by the user. Slower, but each role has a clean context window. Use when an iteration is large or sensitive enough that context bleed between roles is a real risk.

## Artefacts and where they live

- **Contract** — in chat. Not committed; it lives in the iteration's transcript. If the contract turns out to deserve preservation, promote it to a `docs/discussions/*.md` entry as a separate plan-change commit (see [roadmap-driven.md](roadmap-driven.md)).
- **RED output** — verbatim `go test` output, attached in the handoff to the implementer.
- **GREEN / `make check` output** — verbatim, attached in the handoff to the reviewer and in the final commit message body if non-trivial.
- **Review report** — in chat. Reviewer states pass / fail per category (conventions, scope, contract, roadmap checkbox) explicitly.

## When the cycle aborts

- **Contract is wrong.** Test-writer or implementer surfaces it, hands back to spec-author. No silent fixes.
- **`make check` fails.** Implementer fixes it without modifying tests. If the test is wrong, hand back to spec-author for a contract revision.
- **Reviewer rejects.** Back to whichever role caused the issue (test-writer if test is missing, implementer if convention violation, spec-author if scope creep). Never "fix in review".
- **User rejects at any approval gate.** Iteration restarts from the affected role. No partial commits.

The only way out is back through the cycle. Shortcuts ("I'll just patch this and re-run") destroy the audit trail and the role separation.
