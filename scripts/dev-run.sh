#!/usr/bin/env bash
# dev-run.sh — load .env into the environment, build, exec the server.
#
# The binary itself does NOT read .env (12-factor: production env comes
# from Docker / k8s / systemd, not from a file on disk). This script
# replicates what those orchestrators do: it puts variables into the
# process environment before exec'ing the binary.
#
# Variable already set in the parent shell takes precedence over .env
# (matches godotenv.Load() and the 12-factor rule that orchestrator-
# provided env wins over file env). One-shot overrides:
#
#   HTTP_ADDR=:18080 bash scripts/dev-run.sh   # :18080 wins, .env loses
#
# Pass --fake to also build and run cmd/fakeprovider in the background and
# point the server at it via PROVIDER_BASE_URL. Useful for local dev
# without a real upstream key. Fakeprovider is killed on script exit.
#
# Build is incremental — go build is a no-op when nothing changed.
set -euo pipefail

USE_FAKE=0
if [ "${1:-}" = "--fake" ]; then
    USE_FAKE=1
    shift
fi

cd "$(dirname "$0")/.."

if [ -f .env ]; then
    while IFS='=' read -r key value || [ -n "${key:-}" ]; do
        # Skip blanks and comments.
        case "$key" in
            ''|\#*) continue ;;
        esac
        # Strip a single trailing CR (CRLF line endings) and any surrounding
        # double quotes from the value.
        value="${value%$'\r'}"
        value="${value%\"}"
        value="${value#\"}"
        # Only set if unset in the parent environment — preserves shell /
        # orchestrator overrides.
        if [ -z "${!key+x}" ]; then
            export "$key=$value"
        fi
    done < .env
fi

go build -o bin/server ./cmd/server

if [ "$USE_FAKE" = "1" ]; then
    go build -o bin/fakeprovider ./cmd/fakeprovider

    # Force server to talk to the local fakeprovider, regardless of .env.
    export PROVIDER_BASE_URL="http://localhost${FAKE_ADDR:-:9090}"
    # fakeprovider accepts any non-empty key by default.
    : "${PROVIDER_API_KEY:=dev-key}"
    export PROVIDER_API_KEY

    ./bin/fakeprovider &
    FAKE_PID=$!
    trap 'kill -TERM "$FAKE_PID" 2>/dev/null || true; wait "$FAKE_PID" 2>/dev/null || true' EXIT

    # Brief wait so fakeprovider binds its port before the server's startup
    # probe races against it.
    sleep 0.5

    # Not using exec: the trap above must fire on script exit so the
    # background fakeprovider is reaped.
    ./bin/server "$@"
    exit $?
fi

# exec replaces this shell with the server, so signals (SIGINT, SIGTERM)
# go straight to the Go process and graceful shutdown works as designed.
exec ./bin/server "$@"
