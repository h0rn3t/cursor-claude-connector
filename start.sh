#!/usr/bin/env bash
# Builds and runs the cursor-claude-connector (Go) server.
# - Reads .env from the working directory if present.
# - The compiled binary is placed at ./cursor-claude-connector and reused on
#   subsequent runs unless -f/--force is passed.

set -euo pipefail

PORT="${PORT:-9095}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

FORCE=0
for arg in "$@"; do
  case "$arg" in
    -f|--force) FORCE=1 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

# Ensure Go is available.
if ! command -v go >/dev/null 2>&1; then
  echo "❌ Go is not installed. Install Go 1.26+ and try again." >&2
  exit 1
fi

GO_VERSION="$(go version | awk '{print $3}')"
GO_MAJOR="${GO_VERSION#go}"
GO_MAJOR="${GO_MAJOR%%.*}"
if [ "$GO_MAJOR" -lt 26 ]; then
  echo "❌ Go 1.26+ is required (found $GO_VERSION)." >&2
  exit 1
fi

BINARY="./cursor-claude-connector"

if [ "$FORCE" = 1 ] || [ ! -x "$BINARY" ]; then
  echo "🔨 Building..."
  go build -o "$BINARY" ./cmd/cursor-claude-connector
fi

echo ""
echo "🌐 Server starting on http://localhost:$PORT"
echo "📚 Web UI:        http://localhost:$PORT/"
echo "🔐 OAuth start:   http://localhost:$PORT/auth/oauth/start"
echo "📋 List models:   http://localhost:$PORT/v1/models"
echo ""
echo "Press Ctrl+C to stop the server"
echo ""

exec "$BINARY"
