#!/usr/bin/env bash
# Portable PostgreSQL 16 server + client for Linux hosts without root (WSL,
# CI). Prints `export` lines on stdout; progress goes to stderr, so use:
#
#   eval "$(bash scripts/portable-pg.sh)"
#
# The server bundle is the relocatable zonky/embedded-postgres build; the
# client is the Ubuntu 16 package (psql, pg_basebackup, pg_isready).
set -euo pipefail

BASE="${PHLEET_TEST_BASE:-$HOME/.phleet-test}"
SERVER="$BASE/pg"
CLIENT="$BASE/pgclient"
PG_VERSION="${PG_VERSION:-16.15.0}"

say() { printf '\033[1;36m::\033[0m %s\n' "$*" >&2; }

if [ ! -x "$SERVER/bin/initdb" ]; then
  say "downloading portable PostgreSQL $PG_VERSION"
  tmp="$(mktemp -d)"
  url="https://repo1.maven.org/maven2/io/zonky/test/postgres/embedded-postgres-binaries-linux-amd64/$PG_VERSION/embedded-postgres-binaries-linux-amd64-$PG_VERSION.jar"
  curl -fsSL "$url" -o "$tmp/pg.jar"
  mkdir -p "$SERVER"
  ( cd "$tmp" && unzip -o -q pg.jar && tar -xJf postgres-linux-x86_64.txz -C "$SERVER" )
  rm -rf "$tmp"
fi

if [ ! -x "$CLIENT/usr/lib/postgresql/16/bin/psql" ]; then
  say "downloading PostgreSQL 16 client package"
  tmp="$(mktemp -d)"
  ( cd "$tmp" && apt-get download postgresql-client-16 libpq5 >/dev/null 2>&1 ) || {
    echo "could not download postgresql-client-16/libpq5 (apt required)" >&2
    exit 1
  }
  mkdir -p "$CLIENT"
  for d in "$tmp"/*.deb; do
    [ -f "$d" ] && dpkg-deb -x "$d" "$CLIENT"
  done
  rm -rf "$tmp"
fi

BIN="$CLIENT/usr/lib/postgresql/16/bin"
LIBDIR="$CLIENT/usr/lib/x86_64-linux-gnu"
export LD_LIBRARY_PATH="$LIBDIR${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
if ! "$BIN/psql" --version >/dev/null 2>&1; then
  echo "portable PostgreSQL client failed to start" >&2
  exit 1
fi
say "PostgreSQL $( "$SERVER/bin/initdb" --version | awk '{print $NF}' ) ready"

printf 'export PATH="%s:%s:$PATH"\n' "$SERVER/bin" "$BIN"
printf 'export LD_LIBRARY_PATH="%s${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"\n' "$LIBDIR"
