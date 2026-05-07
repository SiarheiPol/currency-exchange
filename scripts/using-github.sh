#!/usr/bin/env bash
set -euo pipefail

# Self-validate: gh CLI presence + authentication
if ! command -v gh >/dev/null 2>&1; then
  echo "ERROR: gh CLI not installed." >&2
  echo "  Install: see https://github.com/cli/cli#installation" >&2
  exit 1
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "ERROR: gh CLI not authenticated." >&2
  echo "  Run: gh auth login" >&2
  exit 1
fi

# Resolve "latest" to the most recent run id on the current branch.
# gh CLI has no native "latest" alias for run-id arguments.
resolve_run_id() {
  local arg="$1"
  if [ "$arg" = "latest" ]; then
    gh run list -L 1 --json databaseId -q '.[0].databaseId'
  else
    echo "$arg"
  fi
}

usage() {
  cat <<EOF
Usage: bash scripts/using-github.sh <command> [args]

Read-only operations for inspecting CI and PR state.

Commands:
  runs [N]            List recent workflow runs (default: 10)
  run <id|latest>     Show details of a workflow run
  watch <id|latest>   Stream a workflow run in real time
  logs <id|latest>    Print full logs for a workflow run
  pr [num]            Show PR status (current branch if no num)
  prs                 List open PRs
  repo                Show repo metadata

Examples:
  bash scripts/using-github.sh runs
  bash scripts/using-github.sh watch latest
  bash scripts/using-github.sh logs 12345678
EOF
}

cmd="${1:-}"
case "$cmd" in
  runs)
    gh run list --limit "${2:-10}"
    ;;
  run)
    id=$(resolve_run_id "${2:?run id or 'latest' required}")
    gh run view "$id"
    ;;
  watch)
    id=$(resolve_run_id "${2:?run id or 'latest' required}")
    gh run watch "$id"
    ;;
  logs)
    id=$(resolve_run_id "${2:?run id or 'latest' required}")
    gh run view "$id" --log
    ;;
  pr)
    if [ -n "${2:-}" ]; then
      gh pr view "$2"
    else
      gh pr view
    fi
    ;;
  prs)
    gh pr list
    ;;
  repo)
    gh repo view
    ;;
  ""|-h|--help|help)
    usage
    ;;
  *)
    echo "ERROR: unknown command: $cmd" >&2
    usage
    exit 1
    ;;
esac
