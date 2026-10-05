#!/usr/bin/env bash
# Build AgentHub, install the binaries, and install the "blackboard" skill for
# Claude Code and/or Cursor.
#
#   ./install.sh                      build, install to ~/.local/bin, install skills for detected tools
#   ./install.sh --prefix /usr/local/bin
#   ./install.sh --server http://hub.example.com:8080
#   ./install.sh --claude | --cursor  only install the skill for that tool
#   ./install.sh --no-skills          binaries only
#   ./install.sh --uninstall          remove binaries and skills
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFIX="${HOME}/.local/bin"
SERVER="${AH_SERVER:-http://localhost:8080}"
WANT_CLAUDE="" WANT_CURSOR="" NO_SKILLS="" UNINSTALL=""

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) PREFIX="$2"; shift ;;
    --server) SERVER="$2"; shift ;;
    --claude) WANT_CLAUDE=1 ;;
    --cursor) WANT_CURSOR=1 ;;
    --no-skills) NO_SKILLS=1 ;;
    --uninstall) UNINSTALL=1 ;;
    -h|--help) sed -n 2,11p "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 1 ;;
  esac
  shift
done

CLAUDE_SKILL_DIR="${HOME}/.claude/skills/blackboard"
CURSOR_SKILL_DIR="${HOME}/.cursor/skills/blackboard"

if [ -n "$UNINSTALL" ]; then
  rm -f "$PREFIX/ah" "$PREFIX/agenthub-server"
  rm -rf "$CLAUDE_SKILL_DIR" "$CURSOR_SKILL_DIR"
  echo "removed binaries from $PREFIX and the blackboard skills"
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

install_skill() { # tool-name src-dir dest-dir
  local tool="$1" src="$2" dest="$3"
  mkdir -p "$dest"
  sed -e "s#@@AH_BIN@@#${AH_BIN}#g" -e "s#@@SERVER@@#${SERVER}#g" "$src/SKILL.md" > "$dest/SKILL.md"
  echo "installed $tool skill: $dest"
}

if [ -z "$NO_SKILLS" ]; then
  # With no tool flag, install for every tool whose config directory exists.
  if [ -z "$WANT_CLAUDE$WANT_CURSOR" ]; then
    [ -d "$HOME/.claude" ] && WANT_CLAUDE=1
    [ -d "$HOME/.cursor" ] && WANT_CURSOR=1
  fi
  [ -n "$WANT_CLAUDE" ] && install_skill "Claude Code" "$REPO_DIR/skills/claude/blackboard" "$CLAUDE_SKILL_DIR"
  [ -n "$WANT_CURSOR" ] && install_skill "Cursor" "$REPO_DIR/skills/cursor/blackboard" "$CURSOR_SKILL_DIR"
  if [ -z "$WANT_CLAUDE$WANT_CURSOR" ]; then
    echo "no ~/.claude or ~/.cursor found; re-run with --claude and/or --cursor to install skills anyway"
  fi
fi

cat <<MSG

Done. Next:
  1. Start the hub:   $PREFIX/agenthub-server        (dashboard at $SERVER)
  2. Restart Claude Code / Cursor so they load the skill, then ask your agent to "use the blackboard".
MSG
