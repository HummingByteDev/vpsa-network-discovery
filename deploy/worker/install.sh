#!/usr/bin/env bash
# VAPN worker bootstrap — the canonical
#   curl -fsSL https://raw.githubusercontent.com/HummingByteDev/vpsa-network-discovery/main/deploy/worker/install.sh | bash
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

# --- PATH -------------------------------------------------------------------
# When the CLI lands in a personal bin directory, `vapn` has to be callable by
# name in the operator's *next* shell, not just in this one. Two separate jobs:
# export it here so this script's hand-off works, and persist it so tomorrow's
# login still finds it.
#
# Idempotent by construction: the marker line is what we grep for, so running
# the installer ten times appends nothing the second time — and an operator who
# already put the directory on PATH by their own means gets nothing appended at
# all. Nothing existing is ever rewritten; we only append.
VAPN_PATH_MARKER="# added by vapn installer"

on_path() { case ":${PATH}:" in *":$1:"*) return 0 ;; *) return 1 ;; esac; }

persist_path() {
  local dir="$1" rc line
  line="export PATH=\"$dir:\$PATH\"  $VAPN_PATH_MARKER"

  # Write to the rc file of the shell the operator actually logs in with. bash
  # reads ~/.bashrc for interactive shells; zsh never reads it, so writing only
  # there would silently do nothing for a zsh user.
  case "$(basename "${SHELL:-/bin/bash}")" in
    zsh) rc="$HOME/.zshrc" ;;
    *)   rc="$HOME/.bashrc" ;;
  esac

  # Already handled — by us on a previous run, or by the operator themselves.
  if [[ -f "$rc" ]] && grep -qF "$VAPN_PATH_MARKER" "$rc"; then
    say "✓ $dir already on PATH via $rc"
    return 0
  fi
  if [[ -f "$rc" ]] && grep -qE "(^|[^#])PATH=.*(\\\$HOME/\.local/bin|$dir)" "$rc"; then
    say "✓ $dir already added to PATH by $rc"
    return 0
  fi

  # `touch` is not a writability test — the owner of a read-only file may still
  # update its timestamps — so ask about the file itself, or its directory when
  # the file does not exist yet.
  if { [[ -e "$rc" ]] && [[ ! -w "$rc" ]]; } || { [[ ! -e "$rc" ]] && [[ ! -w "$HOME" ]]; }; then
    say "note: $rc is not writable — add this line to your shell profile yourself:"
    say "        $line"
    return 0
  fi
  # A leading newline keeps this off the end of whatever line the file stopped
  # on, in the case of a profile with no trailing newline.
  if ! printf '\n%s\n' "$line" >>"$rc" 2>/dev/null; then
    say "note: could not append to $rc — add this line to your shell profile yourself:"
    say "        $line"
    return 0
  fi
  say "✓ Added $dir to your PATH in $rc"
  say "  This shell already has it. For other open terminals, run:  source $rc"
}

# --- fetch the CLI ----------------------------------------------------------
BIN_DIR="${VAPN_BIN_DIR:-/usr/local/bin}"
if [[ ! -w "$BIN_DIR" ]]; then
  if command -v sudo >/dev/null && sudo -n true 2>/dev/null; then
    SUDO="sudo"
  else
    BIN_DIR="$HOME/.local/bin"; mkdir -p "$BIN_DIR"; SUDO=""
  fi
else
  SUDO=""
fi

# Persist PATH for any personal bin directory, whether we fell back to it or
# the operator named it with VAPN_BIN_DIR. A system directory like
# /usr/local/bin is already on every PATH and needs nothing.
if [[ "$BIN_DIR" == "$HOME"/* ]] && ! on_path "$BIN_DIR"; then
  persist_path "$BIN_DIR"
  # Export for this process too, so the `exec` at the bottom and anything the
  # operator runs in this session resolve `vapn` without a full path. Sourcing
  # the rc file here would not help: this script is usually the right-hand side
  # of a `curl | bash`, whose environment dies with it.
  export PATH="$BIN_DIR:$PATH"
fi

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
say "Downloading vapn CLI (linux/$ARCH)..."
curl -fsSL "$BASE/vapn-linux-$ARCH" -o "$TMP/vapn" \
  || fail "download failed from $BASE/vapn-linux-$ARCH"
chmod +x "$TMP/vapn"
${SUDO:+$SUDO }install -m 0755 "$TMP/vapn" "$BIN_DIR/vapn"
say "✓ vapn installed to $BIN_DIR/vapn"

# Prove the promise this script just made: `vapn` resolves by name.
if command -v vapn >/dev/null 2>&1; then
  say "✓ vapn is on your PATH ($(command -v vapn))"
else
  say "note: $BIN_DIR is not on your PATH; run vapn as $BIN_DIR/vapn"
fi

# --- hand over --------------------------------------------------------------
say ""
exec "$BIN_DIR/vapn" install
