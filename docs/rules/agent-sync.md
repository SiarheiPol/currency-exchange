# Agent Sync Rules

`AI_CONTEXT.md` is the single source of truth for all agent guidance. All agent entry points read from it — directly or via generated files. **Never create separate per-agent instruction files.**

## Mandatory workflow

**After adding a new rule or convention:**
1. Add it to `AI_CONTEXT.md` (index) and the relevant `docs/` file
2. Run `bash scripts/sync-agents.sh`

**After adding a new skill:**
1. Create `scripts/new-skill.sh` (executable)
2. Document it in `docs/skills.md`
3. Create `.claude/commands/new-skill.md` for Claude Code slash command
4. Run `bash scripts/sync-agents.sh`

Running `sync-agents.sh` is required — it regenerates Cursor's `.mdc` file (which needs YAML frontmatter) and verifies all symlinks. Skipping it leaves agents out of sync.
