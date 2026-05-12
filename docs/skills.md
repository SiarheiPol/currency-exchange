# Skills

Reusable operations any agent or developer can run. Two flavours:

- **Shell scripts in `scripts/`** — agent/repo-level utilities. Run with `bash scripts/<name>.sh`. Claude Code also exposes them as slash commands via `.claude/commands/`.
- **Make targets** — code-level operations (build, test, lint, codegen). Run with `make <target>`. CI runs the same.

## MANDATORY: after any docs or skills change

```bash
bash scripts/sync-agents.sh
```

Regenerates Cursor's `.cursor/rules/project.mdc` (requires YAML frontmatter — cannot be a symlink) and verifies all other agent symlinks (`CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `.github/copilot-instructions.md`) are intact. Skipping this leaves agents out of sync.

## MANDATORY: before committing code

```bash
make check
```

The single quality gate. CI runs the same. Any agent or developer commits **only after** `make check` passes locally. Behavioural rule from `discussions/agent-development.md`: failures must be reported verbatim, not soften.

## Shell scripts

| Script | Slash command | When to run |
|---|---|---|
| `scripts/sync-agents.sh` | `/sync-agents` | after editing `docs/`, `AI_CONTEXT.md`, or adding a new shell skill |
| `scripts/using-github.sh` | `/using-github` | inspecting CI run status, PR status, repo metadata; read-only |
| `scripts/dev-run.sh` | — | dev-only: source `.env`, build, exec the server. Mirrors what Docker / k8s do in production; the binary itself stays 12-factor and does not read `.env` |

## Review prompts

External AI agents (codex, gemini, claude) are used to review the documentation independently. Prompts live in `docs/reviews/prompts/` and instruct the agent which docs to read, what review to perform, and where to save the output. No wrapper scripts are needed — the prompt itself contains the output instructions.

| Prompt | Run with |
|---|---|
| `docs/reviews/prompts/architecture-review.md` | `codex < docs/reviews/prompts/architecture-review.md` (and same with `gemini`, `claude`, etc.) |
| `docs/reviews/prompts/consistency-review.md` | same pattern |

Review files land in `docs/reviews/<date>-<model>-<type>.md`. See `docs/reviews/README.md` for the workflow.

## Make targets

| Target | What it does | When to run |
|---|---|---|
| `make check` | `go generate` + `git diff --exit-code` + `go test` + `golangci-lint run` | before every commit; CI gate |
| `make test` | unit tests only (fast — no integration build tag) | tight inner loop during TDD |
| `make test-integration` | unit + integration tests (`-tags integration`, brings up testcontainers) | before `make check`, or when changing pgQueue / migrations |
| `make lint` | `golangci-lint run` | when iterating on lint issues alone |
| `make generate` | `go generate ./...` (regenerates `oapi_gen.go` and similar) | after editing `api/openapi.yaml` |
| `make build` | `go build -o bin/server ./cmd/server` — produces a release binary in `bin/` (gitignored) | when you want a built artifact (smoke testing, profiling) |
| `make run` | `go run ./cmd/server` — assumes env vars are already exported (use `scripts/dev-run.sh` to source `.env` first) | local manual testing |
| `make demo` | bring up the full stack with business-like settings and run k6 profile 2 at 5000 RPS for 2 minutes (one command) | quick end-to-end demo / regression check |
| `make demo-real` | bring up the stack against the real apilayer upstream (requires `PROVIDER_API_KEY` in `.env`); no load test | smoke against real provider |
| `docker compose --profile loadtest run --rm k6 run /scripts/profileN.js` | ad-hoc k6 scenario against an already-running stack (N ∈ 1..5; override via `LOADTEST_RATE` / `LOADTEST_VUS` / `LOADTEST_DURATION`) | stress-testing the queue, refresh burst, failure injection |

## Adding a new shell skill

1. Create `scripts/new-skill.sh` and make it executable (`chmod +x`).
2. Add a row to the **Shell scripts** table above.
3. Create `.claude/commands/new-skill.md` with the content: `Run: bash scripts/new-skill.sh`.
4. Run `bash scripts/sync-agents.sh`.

## Adding a new make target

1. Add the target in `Makefile`. Keep targets idempotent and side-effect-aware (no destructive ops without confirmation).
2. Add a row to the **Make targets** table above.
3. If the target is part of `make check`, update CI to run it through `make check` (do not duplicate per-target CI steps).
4. Document any new env vars in `.env.example` and the `Configuration` section of `README.md`.
