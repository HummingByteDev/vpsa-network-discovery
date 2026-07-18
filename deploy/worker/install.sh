#!/usr/bin/env bash
# VAPN worker bootstrap — the `curl -fsSL https://install.vpsadvisor.com | bash`
# entrypoint. Installs the `vapn` CLI for this machine's architecture, then
# hands over to `vapn install` (interactive: coordinator URL, enrollment
# token, system checks, start, verify).
#
# Environment overrides:
#   VAPN_DOWNLOAD_BASE  release download base
#                       (default: latest GitHub release of the project)
#   VAPN_BIN_DIR        install target (default /usr/local/bin, ~/.local/bin
#                       when not writable)
set -euo pipefail

REPO="HummingByteDev/vpsa-network-discovery"
BASE="${VAPN_DOWNLOAD_BASE:-https://github.com/$REPO/releases/latest/download}"

say() { printf '%s\n' "$*"; }
fail() { printf 'vapn installer: %s\n' "$*" >&2; exit 1; }

# --- platform ---------------------------------------------------------------
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
[[ "$OS" == "linux" ]] || fail "workers run on Linux (got: $OS)"
case "$(uname -m)" in
  x86_64|amd64)  ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

# --- docker -----------------------------------------------------------------
if ! command -v docker >/dev/null; then
  say "Docker is required. Install it first:"
  say "  curl -fsSL https://get.docker.com | sh"
  fail "docker not found"
fi
docker info >/dev/null 2>&1 || fail "docker daemon not reachable (is your user in the docker group?)"
say "✓ Docker detected"

# --- fetch the CLI ----------------------------------------------------------
BIN_DIR="${VAPN_BIN_DIR:-/usr/local/bin}"
if [[ ! -w "$BIN_DIR" ]]; then
  if command -v sudo >/dev/null && sudo -n true 2>/dev/null; then
    SUDO="sudo"
  else
    BIN_DIR="$HOME/.local/bin"; mkdir -p "$BIN_DIR"; SUDO=""
    case ":$PATH:" in *":$BIN_DIR:"*) ;; *) say "note: add $BIN_DIR to your PATH" ;; esac
  fi
else
  SUDO=""
fi

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
say "Downloading vapn CLI (linux/$ARCH)..."
curl -fsSL "$BASE/vapn-linux-$ARCH" -o "$TMP/vapn" \
  || fail "download failed from $BASE/vapn-linux-$ARCH"
chmod +x "$TMP/vapn"
${SUDO:+$SUDO }install -m 0755 "$TMP/vapn" "$BIN_DIR/vapn"
say "✓ vapn installed to $BIN_DIR/vapn"

# --- hand over --------------------------------------------------------------
say ""
exec "$BIN_DIR/vapn" install
