# Agent roles

Roles for the TDD iteration cycle defined in [rules/orchestration.md](rules/orchestration.md). Full prompts live in [.claude/agents/](../.claude/agents/) — Claude Code reads them as native subagents; other tools (Cursor, Copilot, Gemini) reuse the body of each file as a role-switch prompt.

## Roles

| Role | File | Reads | Writes | Refuses |
|---|---|---|---|---|
| **spec-author** | [.claude/agents/spec-author.md](../.claude/agents/spec-author.md) | roadmap, discussion docs, conventions | contract document (chat output) | code, tests |
| **test-writer** | [.claude/agents/test-writer.md](../.claude/agents/test-writer.md) | contract, existing code | test files only | production code |
| **implementer** | [.claude/agents/implementer.md](../.claude/agents/implementer.md) | contract, RED output, production code | production code only | tests |
| **reviewer** | [.claude/agents/reviewer.md](../.claude/agents/reviewer.md) | full diff, conventions, scope, roadmap | review report (chat output) | code, tests |

## Why role separation

`discussions/agent-development.md` requires that discipline be replaced by structure. Splitting the work across four roles is the structural enforcement of [rules/tdd.md](rules/tdd.md): a single agent doing both `test-writer` and `implementer` will, given enough iterations, adjust a test to make a stubborn implementation pass. Two roles with different tool sets and different prompts cannot — the implementer literally has no instruction to touch test files, and is told to abort and hand back instead.

## Adding or changing a role

1. Edit the role file under `.claude/agents/`.
2. Update the table above if the role's reads / writes / refusals changed.
3. If the role pulls in a new rule, add the rule under `docs/rules/` and link from `AI_CONTEXT.md` and from this file.
4. Run `bash scripts/sync-agents.sh`.

Removing a role is a structural change to the methodology — discuss before doing.
