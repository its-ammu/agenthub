#!/usr/bin/env bash
# Build AgentHub, install the binaries, and install the blackboard instructions
# for the coding agents found on this machine (see `ah tools`).
#
#   ./install.sh                      build, install to ~/.local/bin, set up every tool found
#   ./install.sh --prefix /usr/local/bin
#   ./install.sh --server http://hub.example.com:8080
#   ./install.sh --tool codex         only that tool (repeatable; --claude and --cursor also work)
#   ./install.sh --no-skills          binaries only
#   ./install.sh --uninstall          remove binaries and instructions
#
# Project-scoped tools (copilot, aider, agents) are written into the current
# directory, so install them with `ah install --tool copilot` from inside a repo.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFIX="${HOME}/.local/bin"
SERVER="${AH_SERVER:-http://localhost:8080}"
TOOL_ARGS=() NO_SKILLS="" UNINSTALL=""

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) PREFIX="$2"; shift ;;
    --server) SERVER="$2"; shift ;;
    --tool) TOOL_ARGS+=(--tool "$2"); shift ;;
    --claude) TOOL_ARGS+=(--tool claude) ;;
    --cursor) TOOL_ARGS+=(--tool cursor) ;;
    --no-skills) NO_SKILLS=1 ;;
    --uninstall) UNINSTALL=1 ;;
    -h|--help) sed -n 2,13p "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 1 ;;
  esac
  shift
done

if [ -n "$UNINSTALL" ]; then
  # Remove the instructions first, while the binary that knows where they are still exists.
  if [ -x "$PREFIX/ah" ]; then
    "$PREFIX/ah" uninstall ${TOOL_ARGS[@]+"${TOOL_ARGS[@]}"} || true
  fi
  rm -f "$PREFIX/ah" "$PREFIX/agenthub-server"
  echo "removed binaries from $PREFIX"
  echo "per-session credentials remain in ~/.agenthub (delete it to forget them)"
  exit 0
fi

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required to build AgentHub (https://go.dev/dl/). On macOS: brew install go" >&2
  exit 1
fi

mkdir -p "$PREFIX"
echo "building into $PREFIX"
(cd "$REPO_DIR" && go build -o "$PREFIX/agenthub-server" ./cmd/agenthub-server && go build -o "$PREFIX/ah" ./cmd/ah)
AH_BIN="$PREFIX/ah"
case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *) echo "note: $PREFIX is not on your PATH. Add it, or run the binaries by full path." ;;
esac

if [ "$SERVER" != "http://localhost:8080" ]; then
  mkdir -p "$HOME/.agenthub"
  printf '%s\n' "$SERVER" > "$HOME/.agenthub/server"
  echo "saved hub URL $SERVER to ~/.agenthub/server"
fi

if [ -z "$NO_SKILLS" ]; then
  "$AH_BIN" install --bin "$AH_BIN" --server "$SERVER" ${TOOL_ARGS[@]+"${TOOL_ARGS[@]}"}
fi

cat <<MSG

Done. Next:
  1. Start the hub:   $PREFIX/agenthub-server        (dashboard at $SERVER)
  2. Restart your coding agent so it loads the instructions, then ask it to "use the blackboard".
  See what is set up with: $AH_BIN tools
MSG
