#!/bin/sh
# Install AgentHub from a GitHub release: no Go needed.
#
#   curl -fsSL https://raw.githubusercontent.com/its-ammu/agenthub/main/get.sh | sh
#
# Options (pass after `sh -s --` when piping):
#   --version v0.1.0     install a specific release (default: latest)
#   --prefix DIR         where to put the binaries (default: ~/.local/bin)
#   --no-skills          binaries only; skip setting up agent instructions
set -eu

REPO="${AGENTHUB_REPO:-its-ammu/agenthub}"
VERSION="latest"
PREFIX="${HOME}/.local/bin"
NO_SKILLS=""

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift ;;
    --prefix) PREFIX="$2"; shift ;;
    --no-skills) NO_SKILLS=1 ;;
    -h|--help) sed -n 2,10p "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 1 ;;
  esac
  shift
done

case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux) OS=linux ;;
  *) echo "unsupported OS: $(uname -s). On Windows use get.ps1." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

ARCHIVE="agenthub_${OS}_${ARCH}.tar.gz"
if [ -n "${AGENTHUB_BASE_URL:-}" ]; then
  BASE="$AGENTHUB_BASE_URL" # mirror or local test server
elif [ "$VERSION" = "latest" ]; then
  BASE="https://github.com/${REPO}/releases/latest/download"
else
  BASE="https://github.com/${REPO}/releases/download/${VERSION}"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "downloading $ARCHIVE ($VERSION)"
curl -fsSL "$BASE/$ARCHIVE" -o "$TMP/$ARCHIVE" || { echo "download failed: $BASE/$ARCHIVE" >&2; exit 1; }
curl -fsSL "$BASE/checksums.txt" -o "$TMP/checksums.txt" || { echo "could not download checksums.txt" >&2; exit 1; }

want="$(grep " $ARCHIVE\$" "$TMP/checksums.txt" | awk '{print $1}')"
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$TMP/$ARCHIVE" | awk '{print $1}')"
else
  got="$(shasum -a 256 "$TMP/$ARCHIVE" | awk '{print $1}')"
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "checksum mismatch for $ARCHIVE, refusing to install" >&2
  exit 1
fi

tar -xzf "$TMP/$ARCHIVE" -C "$TMP"
mkdir -p "$PREFIX"
install -m 0755 "$TMP/ah" "$PREFIX/ah"
install -m 0755 "$TMP/agenthub-server" "$PREFIX/agenthub-server"
echo "installed ah and agenthub-server to $PREFIX"
case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *) echo "note: $PREFIX is not on your PATH. Add it, or run the binaries by full path." ;;
esac

if [ -z "$NO_SKILLS" ]; then
  "$PREFIX/ah" install --bin "$PREFIX/ah"
fi

cat <<MSG

Done. Next:
  1. Start the hub:   $PREFIX/agenthub-server        (dashboard at http://localhost:8080)
  2. Restart your coding agent so it loads the instructions, then ask it to "use the blackboard".
  See what is set up with: $PREFIX/ah tools
MSG
