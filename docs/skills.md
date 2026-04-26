# Skills

Skills are shell scripts in `scripts/`. Any agent can run them directly with `bash scripts/<name>.sh`. Claude Code also exposes them as slash commands via `.claude/commands/`.

## MANDATORY: after any docs or skills change

```bash
bash scripts/sync-agents.sh
```

This regenerates Cursor's `.cursor/rules/project.mdc` (requires YAML frontmatter — cannot be a symlink) and verifies all other agent symlinks are intact. Skipping this leaves agents out of sync.

## Available scripts

| Script | Slash command | When to run |
|--------|---------------|-------------|
| `scripts/sync-agents.sh` | `/sync-agents` | After editing `docs/`, `AI_CONTEXT.md`, or adding a new script |

## Adding a new skill

1. Create `scripts/new-skill.sh` and make it executable (`chmod +x`)
2. Add a row to the table above in this file
3. Create `.claude/commands/new-skill.md` with the content: `Run: bash scripts/new-skill.sh`
4. Run `bash scripts/sync-agents.sh`
