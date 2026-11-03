#!/usr/bin/env bash
# Runs the live PostgreSQL/Redis replication tests on Linux without root.
#
# Build the test binary on the host first (from the repo root):
#   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o dist/lintest/engines-live.test ./internal/engines
# Then:
#   bash scripts/run-live-tests.sh
set -euo pipefail
cd "$(dirname "$0")/.."
eval "$(bash scripts/portable-pg.sh)"
eval "$(bash scripts/portable-redis.sh)"
BIN=dist/lintest/engines-live.test
if [ ! -x "$BIN" ]; then
  echo "missing $BIN; build it with:" >&2
  echo "  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o $BIN ./internal/engines" >&2
  exit 1
fi
exec "$BIN" -test.run 'TestPostgresStreamingReplicationLive|TestRedisStandbyLive' -test.v -test.timeout 300s
