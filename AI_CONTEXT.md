# AI_CONTEXT — Currency Quote Service

> Agent hub. Read this file, then follow links for details.
> Symlinked as: CLAUDE.md · AGENTS.md · GEMINI.md · .github/copilot-instructions.md
> Cursor uses `.cursor/rules/project.mdc` (generated) — run `bash scripts/sync-agents.sh` after any edits.

## Rules
- [Confidentiality](docs/rules/confidentiality.md) — do not mention the company name
- [Agent sync](docs/rules/agent-sync.md) — how to add rules and skills; when to run the sync script
- [Agent development principles](docs/discussions/agent-development.md) — spec-first TDD with AI agents: structural enforcement, honest reporting, reproducibility

## Workflow
- [TDD](docs/rules/tdd.md) — RED → GREEN → refactor; role separation between test-writer and implementer
- [Roadmap-driven](docs/rules/roadmap-driven.md) — every iteration ties to one checkbox; plan changes are separate commits
- [Orchestration](docs/rules/orchestration.md) — the spec-author → test-writer → implementer → reviewer cycle and how to run it in different AI tools
- [Agent roles](docs/agents.md) — index of role files (`spec-author`, `test-writer`, `implementer`, `reviewer`)

## Project
- [Overview & API](docs/project.md)
- [Architecture](docs/architecture.md)

## Development
- [Go conventions](docs/conventions.md)
- [Out of scope](docs/scope.md)

## Skills
- [Available scripts](docs/skills.md) — reusable operations any agent can run; mandatory sync workflow
