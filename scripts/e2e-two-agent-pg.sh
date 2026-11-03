#!/usr/bin/env bash
# Two-"phone" PostgreSQL rehearsal with real servers.
#
# Two independent agents run on this Linux host with separate HOME and state
# directories, so the control plane schedules them as two phones. The standby
# clones and replicates through the phleet relay (agent B -> control plane ->
# agent A), exactly like two devices behind CGNAT. Then it is promoted and the
# old primary is fenced.
#
# Requires the Linux binaries and a PostgreSQL client/server on PATH. Easiest:
#
#   eval "$(bash scripts/portable-pg.sh)"
#   bash scripts/e2e-two-agent-pg.sh
#
# or use scripts/run-two-agent-pg.sh, which does both.
set -euo pipefail

ROOT="${1:-$(pwd)}"
PORT="${PHLEET_E2E_PG_PORT:-18083}"
BASE="http://127.0.0.1:$PORT"
WORK="${PHLEET_E2E_PG_DIR:-/tmp/phleet-e2e-pg}"
PHLEETD="$ROOT/dist/bin/phleetd-linux-amd64"
AGENTBIN="$ROOT/dist/bin/phleet-agent-linux-amd64"
DB="$WORK/data/phleet.db"

fail() { echo "FAIL: $*" >&2; exit 1; }
say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }

[ -x "$PHLEETD" ] || fail "missing $PHLEETD (run scripts/build.ps1)"
[ -x "$AGENTBIN" ] || fail "missing $AGENTBIN"
for t in initdb postgres pg_ctl pg_basebackup pg_isready psql sqlite3 python3 curl; do
  command -v "$t" >/dev/null 2>&1 || fail "$t is not on PATH; run: eval \"\$(bash scripts/portable-pg.sh)\""
done

CP_PID=""
A_PID=""
B_PID=""
ADDON_ID=""
ADMIN=""

cleanup() {
  if [ -n "$ADDON_ID" ] && [ -n "$CP_PID" ] && kill -0 "$CP_PID" 2>/dev/null; then
    curl -fsS -X DELETE "$BASE/api/addons/$ADDON_ID?force=1&purge=1" \
      -H "Authorization: Bearer $ADMIN" >/dev/null 2>&1 || true
    sleep 1
  fi
  for pid in "$A_PID" "$B_PID" "$CP_PID"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
  pkill -f "$WORK/home-" 2>/dev/null || true
}
trap cleanup EXIT

say "resetting $WORK"
rm -rf "$WORK"
mkdir -p "$WORK/home-a" "$WORK/home-b" "$WORK/data"

say "starting control plane on :$PORT"
"$PHLEETD" -addr "127.0.0.1:$PORT" -data "$WORK/data" >"$WORK/phleetd.log" 2>&1 &
CP_PID=$!
for _ in $(seq 1 100); do
  curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break
  sleep 0.1
done
curl -fsS "$BASE/healthz" >/dev/null || { cat "$WORK/phleetd.log"; fail "control plane did not start"; }
ADMIN=$(cat "$WORK/data/admin_token")
AGENT_TOKEN=$(cat "$WORK/data/agent_token")
AUTH=(-H "Authorization: Bearer $ADMIN")

say "starting two agents (separate HOME = two phones)"
HOME="$WORK/home-a" "$AGENTBIN" -server "$BASE" -token "$AGENT_TOKEN" \
  -name phone-a -state "$WORK/agent-a" -debug >"$WORK/agent-a.log" 2>&1 &
A_PID=$!
HOME="$WORK/home-b" "$AGENTBIN" -server "$BASE" -token "$AGENT_TOKEN" \
  -name phone-b -state "$WORK/agent-b" -debug >"$WORK/agent-b.log" 2>&1 &
B_PID=$!

server_id() {
  python3 - "$1" "$2" <<'PY'
import json, sys
want = sys.argv[1]
for s in json.loads(sys.argv[2])["servers"]:
    if s["name"] == want and s["online"]:
        print(s["id"])
        break
PY
}

IDS_OK=""
for _ in $(seq 1 200); do
  SERVERS=$(curl -fsS "${AUTH[@]}" "$BASE/api/servers" 2>/dev/null || true)
  ID_A=$(server_id phone-a "$SERVERS")
  ID_B=$(server_id phone-b "$SERVERS")
  if [ -n "$ID_A" ] && [ -n "$ID_B" ]; then IDS_OK=1; break; fi
  sleep 0.1
done
[ -n "$IDS_OK" ] || { tail -50 "$WORK/agent-a.log" "$WORK/agent-b.log"; fail "agents did not come online"; }
echo "  phone-a=$ID_A phone-b=$ID_B"

say "creating a PostgreSQL addon on phone-a"
ADDON=$(curl -fsS -X POST "$BASE/api/addons" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "$(python3 -c 'import json,sys; print(json.dumps({"name":"Rehearsal DB","engine":"postgres","serverId":sys.argv[1]}))' "$ID_A")")
ADDON_ID=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["id"])' <<<"$ADDON")

addon() { curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID"; }

wait_status() {
  local want=$1
  local json=""
  for _ in $(seq 1 600); do
    json=$(addon 2>/dev/null || true)
    local st
    st=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["status"])' <<<"$json" 2>/dev/null || true)
    [ "$st" = "$want" ] && { printf '%s' "$json"; return 0; }
    if [ "$st" = "failed" ]; then
      printf '%s' "$json"
      return 1
    fi
    sleep 0.5
  done
  return 1
}

if ! CURRENT=$(wait_status running); then
  curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID/logs" || true
  tail -40 "$WORK/agent-a.log"
  fail "primary did not reach running"
fi
PRIMARY_PORT=$(python3 -c 'import json,sys; d=json.load(sys.stdin)["addon"]; print(d["placements"][0]["port"])' <<<"$CURRENT")
echo "  primary port $PRIMARY_PORT"

console() {
  curl -fsS -X POST "$BASE/api/addons/$ADDON_ID/query" "${AUTH[@]}" -H 'Content-Type: application/json' \
    -d "$(python3 -c 'import json,sys; print(json.dumps({"input":sys.argv[1]}))' "$1")"
}
# psql's aligned output ends with "(N row[s])"; ignore that line when reading a
# scalar result.
console_num() {
  python3 -c '
import json, re, sys
out = json.load(sys.stdin)["output"]
lines = [l for l in out.splitlines() if not l.strip().startswith("(")]
nums = re.findall(r"-?[0-9]+", "\n".join(lines))
print(nums[-1] if nums else "")
'
}
console "create table t(x int); insert into t values (1);" >/dev/null
COUNT=$(console "select count(*) from t;" | console_num)
[ "$COUNT" = "1" ] || fail "primary console returned $COUNT"
CONN=$(curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID/connection")
DB_USER=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["user"])' <<<"$CONN")
DB_PASS=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["password"])' <<<"$CONN")
DB_NAME=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["database"])' <<<"$CONN")

# Both agents share this host, so make phone-b allocate a different port than
# the primary. On real phones the same port on two devices is fine.
say "reserving port $PRIMARY_PORT on phone-b (simulation only)"
sqlite3 -cmd ".timeout 5000" "$DB" \
  "insert or ignore into placements (project_id,server_id,status,desired,port,pid,restarts,release,last_error,updated_at) values ('rehearsal-reserve','$ID_B','stopped','stopped',$PRIMARY_PORT,0,0,'','',0);"

say "adding a standby on phone-b (pg_basebackup over the relay)"
curl -fsS -X POST "$BASE/api/addons/$ADDON_ID/standby" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "$(python3 -c 'import json,sys; print(json.dumps({"serverId":sys.argv[1]}))' "$ID_B")" >/dev/null

STANDBY_OK=""
STANDBY_PORT=""
for _ in $(seq 1 500); do
  CURRENT=$(addon)
  STANDBY_PORT=$(python3 -c '
import json,sys
d=json.load(sys.stdin)["addon"]
for p in d["placements"]:
    if p["role"]=="standby" and p["status"]=="running":
        print(p["port"]); break
' <<<"$CURRENT")
  if [ -n "$STANDBY_PORT" ]; then STANDBY_OK=1; break; fi
  if python3 -c 'import json,sys; d=json.load(sys.stdin)["addon"]; raise SystemExit(0 if any(p["status"]=="failed" for p in d["placements"]) else 1)' <<<"$CURRENT"; then
    curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID/logs" || true
    tail -40 "$WORK/agent-b.log"
    fail "standby failed to provision"
  fi
  sleep 0.5
done
[ -n "$STANDBY_OK" ] || { curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID/logs" || true; fail "standby did not start"; }
echo "  standby port $STANDBY_PORT"

say "waiting for replication to phone-b (direct loopback query)"
REPL_OK=""
for _ in $(seq 1 120); do
  N=$(PGPASSWORD="$DB_PASS" psql -h 127.0.0.1 -p "$STANDBY_PORT" -U "$DB_USER" -d "$DB_NAME" \
    -tAc "select count(*) from t" 2>/dev/null | tr -d '[:space:]' || true)
  if [ "$N" = "1" ]; then REPL_OK=1; break; fi
  sleep 0.5
done
[ -n "$REPL_OK" ] || fail "standby never replicated the row"

say "promoting phone-b"
curl -fsS -X POST "$BASE/api/addons/$ADDON_ID/promote" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "$(python3 -c 'import json,sys; print(json.dumps({"serverId":sys.argv[1]}))' "$ID_B")" >/dev/null

PROMOTED=""
for _ in $(seq 1 120); do
  CURRENT=$(addon)
  if python3 - "$ID_B" "$CURRENT" <<'PY'
import json, sys
d = json.loads(sys.argv[2])["addon"]
raise SystemExit(0 if d["pinnedServer"] == sys.argv[1] and d["epoch"] >= 2 else 1)
PY
  then PROMOTED=1; break; fi
  sleep 0.5
done
[ -n "$PROMOTED" ] || { curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID/logs" || true; fail "promotion did not repoint the addon"; }

EPOCH=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["epoch"])' <<<"$CURRENT")
ROLE_A=$(python3 - "$ID_A" "$CURRENT" <<'PY'
import json, sys
d = json.loads(sys.argv[2])["addon"]
for p in d["placements"]:
    if p["serverId"] == sys.argv[1]:
        print(p["role"]); break
PY
)
ROLE_B=$(python3 - "$ID_B" "$CURRENT" <<'PY'
import json, sys
d = json.loads(sys.argv[2])["addon"]
for p in d["placements"]:
    if p["serverId"] == sys.argv[1]:
        print(p["role"]); break
PY
)
echo "  epoch=$EPOCH roles: phone-a=$ROLE_A phone-b=$ROLE_B"
[ "$ROLE_A" = "demoted" ] || fail "old primary is not demoted (got $ROLE_A)"
[ "$ROLE_B" = "primary" ] || fail "standby is not primary (got $ROLE_B)"

say "writing through the new primary (panel console)"
for _ in $(seq 1 120); do
  RECOVERY=$(PGPASSWORD="$DB_PASS" psql -h 127.0.0.1 -p "$STANDBY_PORT" -U "$DB_USER" -d "$DB_NAME" \
    -tAc "select pg_is_in_recovery()" 2>/dev/null | tr -d '[:space:]' || true)
  [ "$RECOVERY" = "f" ] && break
  sleep 0.5
done
[ "$RECOVERY" = "f" ] || fail "promoted server is still in recovery"
INSERT=$(console "insert into t values (2);")
echo "  insert output: $(printf '%s' "$INSERT" | tr '\n' ' ')"
PORT_NOW=$(python3 -c 'import json,sys; d=json.load(sys.stdin)["addon"]; print(d["port"])' <<<"$CURRENT")
PIN_NOW=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["pinnedServer"])' <<<"$CURRENT")
SERVER_PORT=$(console "select inet_server_port();" | console_num)
echo "  addon port=$PORT_NOW pinned=$PIN_NOW console-server-port=$SERVER_PORT"
[ "$SERVER_PORT" = "$STANDBY_PORT" ] || fail "console hit port $SERVER_PORT, want $STANDBY_PORT"
COUNT=$(console "select count(*) from t;" | console_num)
DIRECT=$(PGPASSWORD="$DB_PASS" psql -h 127.0.0.1 -p "$STANDBY_PORT" -U "$DB_USER" -d "$DB_NAME" -tAc "select count(*) from t" | tr -d '[:space:]')
echo "  console count=$COUNT direct=$DIRECT"
[ "$COUNT" = "2" ] || fail "promoted primary returned $COUNT rows"

say "writing through the control-plane bridge"
BRIDGE_PORT=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"].get("bridgePort") or 0)' <<<"$CURRENT")
[ "$BRIDGE_PORT" != "0" ] || fail "addon has no bridge port"
BRIDGE_COUNT=""
for _ in $(seq 1 80); do
  BRIDGE_COUNT=$(PGPASSWORD="$DB_PASS" psql -h 127.0.0.1 -p "$BRIDGE_PORT" -U "$DB_USER" -d "$DB_NAME" \
    -tAc "select count(*) from t" 2>/dev/null | tr -d '[:space:]' || true)
  [ "$BRIDGE_COUNT" = "2" ] && break
  sleep 0.25
done
[ "$BRIDGE_COUNT" = "2" ] || fail "bridge returned $BRIDGE_COUNT rows"

say "checking the old primary is fenced"
if pg_isready -h 127.0.0.1 -p "$PRIMARY_PORT" -q 2>/dev/null; then
  fail "the old primary is still accepting connections on $PRIMARY_PORT"
fi

say "re-admitting phone-a and enabling automatic failover"
curl -fsS -X DELETE "$BASE/api/addons/$ADDON_ID/placement/$ID_A" "${AUTH[@]}" >/dev/null
curl -fsS -X POST "$BASE/api/addons/$ADDON_ID/standby" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "$(python3 -c 'import json,sys; print(json.dumps({"serverId":sys.argv[1]}))' "$ID_A")" >/dev/null

A_STANDBY_PORT=""
for _ in $(seq 1 500); do
  CURRENT=$(addon)
  A_STANDBY_PORT=$(python3 - "$ID_A" "$CURRENT" <<'PY'
import json, sys
d = json.loads(sys.argv[2])["addon"]
for p in d["placements"]:
    if p["serverId"] == sys.argv[1] and p["role"] == "standby" and p["status"] == "running":
        print(p["port"]); break
PY
)
  [ -n "$A_STANDBY_PORT" ] && break
  sleep 0.5
done
[ -n "$A_STANDBY_PORT" ] || { curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID/logs" || true; fail "phone-a standby did not start"; }
REPL_OK=""
for _ in $(seq 1 120); do
  N=$(PGPASSWORD="$DB_PASS" psql -h 127.0.0.1 -p "$A_STANDBY_PORT" -U "$DB_USER" -d "$DB_NAME" \
    -tAc "select count(*) from t" 2>/dev/null | tr -d '[:space:]' || true)
  [ "$N" = "2" ] && { REPL_OK=1; break; }
  sleep 0.5
done
[ -n "$REPL_OK" ] || fail "phone-a standby did not replicate"

curl -fsS -X PATCH "$BASE/api/addons/$ADDON_ID" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"autoFailover":true,"failoverAfterSec":10,"maxLagMs":0,"leaseSec":30}' >/dev/null

say "killing the control plane; phone-b must self-fence"
kill "$CP_PID" 2>/dev/null || true
wait "$CP_PID" 2>/dev/null || true
CP_PID=""
FENCED=""
for _ in $(seq 1 90); do
  if ! pg_isready -h 127.0.0.1 -p "$STANDBY_PORT" -q 2>/dev/null; then FENCED=1; break; fi
  sleep 1
done
[ -n "$FENCED" ] || fail "the primary did not self-fence after the lease expired"

say "restarting the control plane; phone-a should be promoted automatically"
"$PHLEETD" -addr "127.0.0.1:$PORT" -data "$WORK/data" >>"$WORK/phleetd.log" 2>&1 &
CP_PID=$!
for _ in $(seq 1 100); do
  curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break
  sleep 0.1
done
curl -fsS "$BASE/healthz" >/dev/null || fail "control plane did not restart"

IDS_OK=""
for _ in $(seq 1 300); do
  SERVERS=$(curl -fsS "${AUTH[@]}" "$BASE/api/servers" 2>/dev/null || true)
  ID_A2=$(server_id phone-a "$SERVERS")
  ID_B2=$(server_id phone-b "$SERVERS")
  if [ -n "$ID_A2" ] && [ -n "$ID_B2" ]; then IDS_OK=1; break; fi
  sleep 0.2
done
[ -n "$IDS_OK" ] || fail "agents did not reconnect after the restart"
curl -fsS -X POST "$BASE/api/rebalance" "${AUTH[@]}" >/dev/null

AUTO_OK=""
for _ in $(seq 1 200); do
  CURRENT=$(addon)
  if python3 - "$ID_A" "$CURRENT" <<'PY'
import json, sys
d = json.loads(sys.argv[2])["addon"]
raise SystemExit(0 if d["pinnedServer"] == sys.argv[1] and d["epoch"] >= 3 else 1)
PY
  then AUTO_OK=1; break; fi
  sleep 0.5
done
[ -n "$AUTO_OK" ] || { curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID/logs" || true; fail "automatic promotion did not happen"; }

AUTO_EPOCH=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["epoch"])' <<<"$CURRENT")
echo "  auto-promoted epoch=$AUTO_EPOCH"
console "insert into t values (3);" >/dev/null
COUNT=$(console "select count(*) from t;" | console_num)
[ "$COUNT" = "3" ] || fail "auto-promoted primary returned $COUNT rows"
if pg_isready -h 127.0.0.1 -p "$STANDBY_PORT" -q 2>/dev/null; then
  fail "the self-fenced old primary came back on $STANDBY_PORT"
fi

echo
echo "PASS — two-agent PostgreSQL provisioning, relay replication, manual promotion, epoch fencing, the bridge, and lease-based auto-failover with self-fencing work"
