#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ── 1. Generate .cursor/rules/project.mdc ────────────────────────────────────
# Cursor requires YAML frontmatter — a plain-markdown symlink does not work.
CURSOR_DIR="$ROOT/.cursor/rules"
CURSOR_FILE="$CURSOR_DIR/project.mdc"

mkdir -p "$CURSOR_DIR"

# Remove symlink or stale file
rm -f "$CURSOR_FILE"

{
  cat <<'FRONTMATTER'
---
description: Project rules, architecture and conventions for this codebase
globs: "**/*"
alwaysApply: true
---
FRONTMATTER

  cat "$ROOT/AI_CONTEXT.md"
  echo ""

  # Append all docs in a stable order
  for f in \
    "$ROOT/docs/project.md" \
    "$ROOT/docs/architecture.md" \
    "$ROOT/docs/conventions.md" \
    "$ROOT/docs/scope.md" \
    "$ROOT/docs/skills.md" \
    "$ROOT/docs/rules/confidentiality.md" \
    "$ROOT/docs/rules/agent-sync.md" \
    "$ROOT/docs/rules/tdd.md" \
    "$ROOT/docs/rules/roadmap-driven.md" \
    "$ROOT/docs/rules/orchestration.md" \
    "$ROOT/docs/agents.md"
  do
    [ -f "$f" ] || continue
    echo ""
    echo "---"
    echo ""
    cat "$f"
  done
} > "$CURSOR_FILE"

echo "✓ Generated $CURSOR_FILE"

# ── 2. Verify / recreate symlinks for plain-markdown agents ──────────────────
ensure_symlink() {
  local link="$1" target="$2" dir
  dir="$(dirname "$link")"
  mkdir -p "$dir"
  if [ -L "$link" ] && [ "$(readlink "$link")" = "$target" ]; then
    echo "✓ Symlink OK: $link -> $target"
  else
    rm -f "$link"
    ln -s "$target" "$link"
    echo "✓ Created symlink: $link -> $target"
  fi
}

ensure_symlink "$ROOT/CLAUDE.md"                          "AI_CONTEXT.md"
ensure_symlink "$ROOT/AGENTS.md"                          "AI_CONTEXT.md"
ensure_symlink "$ROOT/GEMINI.md"                          "AI_CONTEXT.md"
ensure_symlink "$ROOT/.github/copilot-instructions.md"    "../AI_CONTEXT.md"

echo ""
echo "All agent entry points are up to date."
