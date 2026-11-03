#!/usr/bin/env bash
# Prepares the portable PostgreSQL and runs the two-agent rehearsal.
set -euo pipefail
cd "$(dirname "$0")/.."
eval "$(bash scripts/portable-pg.sh)"
exec bash scripts/e2e-two-agent-pg.sh "$(pwd)"
