#!/usr/bin/env bash
# Portable Redis server + client for Linux hosts without root (WSL, CI).
# Prints `export` lines on stdout; use:
#
#   eval "$(bash scripts/portable-redis.sh)"
set -euo pipefail

BASE="${PHLEET_TEST_BASE:-$HOME/.phleet-test}/redis"

say() { printf '\033[1;36m::\033[0m %s\n' "$*" >&2; }

if [ ! -x "$BASE/usr/bin/redis-server" ]; then
  say "downloading Redis server/client packages"
  tmp="$(mktemp -d)"
  ( cd "$tmp" && apt-get download redis-server redis-tools liblzf1 libjemalloc2 >/dev/null 2>&1 ) || {
    echo "could not download redis packages (apt required)" >&2
    exit 1
  }
  mkdir -p "$BASE"
  for d in "$tmp"/*.deb; do
    [ -f "$d" ] && dpkg-deb -x "$d" "$BASE"
  done
  rm -rf "$tmp"
fi

BIN="$BASE/usr/bin"
LIBDIR="$BASE/usr/lib/x86_64-linux-gnu"
export LD_LIBRARY_PATH="$LIBDIR${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
if ! "$BIN/redis-server" --version >/dev/null 2>&1; then
  echo "portable redis-server failed to start" >&2
  exit 1
fi
say "Redis $( "$BIN/redis-server" --version | awk '{print $3}' ) ready"

printf 'export PATH="%s:$PATH"\n' "$BIN"
printf 'export LD_LIBRARY_PATH="%s${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"\n' "$LIBDIR"
