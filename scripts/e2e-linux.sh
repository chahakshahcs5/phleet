#!/usr/bin/env bash
# End-to-end smoke test on a Linux host (or WSL): starts a control plane, runs a
# real agent, deploys a tiny Python project through the tunnel and verifies it.
#
#   scripts/e2e-linux.sh [repo-root]
#
# Requires: dist/bin/phleetd-linux-amd64 and dist/bin/phleet-agent-linux-amd64
set -euo pipefail

ROOT="${1:-$(pwd)}"
PORT="${PHLEET_E2E_PORT:-18081}"
BASE="http://127.0.0.1:$PORT"
WORK="${PHLEET_E2E_DIR:-/tmp/phleet-e2e}"
PHLEETD="$ROOT/dist/bin/phleetd-linux-amd64"
AGENTBIN="$ROOT/dist/bin/phleet-agent-linux-amd64"

fail() { echo "FAIL: $*" >&2; exit 1; }
say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }

[ -x "$PHLEETD" ] || fail "missing $PHLEETD"
[ -x "$AGENTBIN" ] || fail "missing $AGENTBIN"
command -v git >/dev/null || fail "git is required"
command -v python3 >/dev/null || fail "python3 is required"
command -v curl >/dev/null || fail "curl is required"

say "resetting $WORK"
if command -v fuser >/dev/null 2>&1; then
  fuser -k 4100/tcp >/dev/null 2>&1 || true
else
  pkill -f "python3 -m http.server 4100" 2>/dev/null || true
fi
rm -rf "$WORK"
mkdir -p "$WORK/repo"
cd "$WORK/repo"
cat > index.html <<'HTML'
<!doctype html><title>phleet e2e</title><h1>hello from phleet</h1>
HTML
git init -q
git config user.email e2e@phleet.local
git config user.name "phleet e2e"
git add -A
git commit -qm "initial"
BRANCH=$(git rev-parse --abbrev-ref HEAD)

CP_PID=""
AG_PID=""
cleanup() {
  [ -n "$AG_PID" ] && kill "$AG_PID" 2>/dev/null || true
  [ -n "$CP_PID" ] && kill "$CP_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  if command -v fuser >/dev/null 2>&1; then
    fuser -k 4100/tcp >/dev/null 2>&1 || true
  else
    pkill -f "python3 -m http.server 4100" 2>/dev/null || true
  fi
  pkill -f "rclone serve s3" 2>/dev/null || true
}
trap cleanup EXIT

say "starting control plane on :$PORT"
"$PHLEETD" -addr "127.0.0.1:$PORT" -data "$WORK/data" >"$WORK/phleetd.log" 2>&1 &
CP_PID=$!
for _ in $(seq 1 80); do curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "$BASE/healthz" >/dev/null || { cat "$WORK/phleetd.log"; fail "control plane did not start"; }

ADMIN=$(cat "$WORK/data/admin_token")
AGENT_TOKEN=$(cat "$WORK/data/agent_token")
AUTH=(-H "Authorization: Bearer $ADMIN")

say "starting agent"
"$AGENTBIN" -server "$BASE" -token "$AGENT_TOKEN" -name "wsl-phone" -state "$WORK/agent" -debug >"$WORK/agent.log" 2>&1 &
AG_PID=$!
online=""
for _ in $(seq 1 80); do
  if curl -fsS "${AUTH[@]}" "$BASE/api/servers" | grep -q '"online":true'; then online=1; break; fi
  sleep 0.1
done
[ -n "$online" ] || { cat "$WORK/agent.log"; fail "agent did not come online"; }

say "creating project"
PAYLOAD=$(python3 - "$WORK/repo" "$BRANCH" <<'PY'
import json, sys
print(json.dumps({
    "name": "E2E App",
    "repo": sys.argv[1],
    "branch": sys.argv[2],
    "startCmd": "python3 -m http.server $PORT",
    "healthPath": "/",
    "env": {"E2E": "1"},
}))
PY
)
CREATE=$(curl -fsS -X POST "$BASE/api/projects" "${AUTH[@]}" -H 'Content-Type: application/json' -d "$PAYLOAD")
PROJECT_ID=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["project"]["id"])' <<<"$CREATE")
HOSTNAME=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["project"]["hostname"])' <<<"$CREATE")
say "project $PROJECT_ID → $HOSTNAME"

say "deploying"
DEPLOY=$(curl -fsS -X POST "$BASE/api/projects/$PROJECT_ID/deploy" "${AUTH[@]}")
DEPLOY_ID=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["deployments"][0]["id"])' <<<"$DEPLOY")

status=""
for _ in $(seq 1 90); do
  DETAIL=$(curl -fsS "${AUTH[@]}" "$BASE/api/deployments/$DEPLOY_ID")
  status=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["deployment"]["status"])' <<<"$DETAIL")
  case "$status" in active|failed|rolled_back) break ;; esac
  sleep 1
done

python3 - "$DETAIL" "$status" <<'PY'
import json, sys
d = json.loads(sys.argv[1])
print("  status:", d["deployment"]["status"])
for line in d["logs"]:
    print("  " + line["line"])
PY
[ "$status" = "active" ] || { tail -50 "$WORK/agent.log"; fail "deploy ended as $status"; }

say "requesting the app through the tunnel"
BODY=$(curl -fsS -H "Host: $HOSTNAME" "$BASE/")
[[ "$BODY" == *"hello from phleet"* ]] || fail "unexpected app response: $BODY"

say "checking placement state"
PLACEMENT=$(curl -fsS "${AUTH[@]}" "$BASE/api/projects/$PROJECT_ID" | python3 -c '
import json,sys
p=json.load(sys.stdin)["project"]
pl=p["placements"][0]
print(pl["status"], pl["port"])
')
echo "  $PLACEMENT"
[[ "$PLACEMENT" == running* ]] || fail "app is not running: $PLACEMENT"

say "checking app logs endpoint"
curl -fsS "${AUTH[@]}" "$BASE/api/projects/$PROJECT_ID/logs" | python3 -c '
import json,sys
d=json.load(sys.stdin)
print("  log bytes:", d["size"], "path:", d["path"])
'

say "deploying version two"
cd "$WORK/repo"
printf '<!doctype html><h1>version two</h1>\n' > index.html
git commit -qam "v2"
cd "$WORK"
DEPLOY2=$(curl -fsS -X POST "$BASE/api/projects/$PROJECT_ID/deploy" "${AUTH[@]}")
DEPLOY2_ID=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["deployments"][0]["id"])' <<<"$DEPLOY2")
status2=""
for _ in $(seq 1 60); do
  DETAIL2=$(curl -fsS "${AUTH[@]}" "$BASE/api/deployments/$DEPLOY2_ID")
  status2=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["deployment"]["status"])' <<<"$DETAIL2")
  case "$status2" in active|failed|rolled_back) break ;; esac
  sleep 0.5
done
[ "$status2" = "active" ] || { echo "$DETAIL2"; fail "second deploy ended as $status2"; }
BODY2=$(curl -fsS -H "Host: $HOSTNAME" "$BASE/")
[[ "$BODY2" == *"version two"* ]] || fail "v2 not served: $BODY2"

say "rolling back to the previous release"
curl -fsS -X POST "$BASE/api/projects/$PROJECT_ID/rollback" "${AUTH[@]}" >/dev/null
for _ in $(seq 1 40); do
  BODY3=$(curl -s -H "Host: $HOSTNAME" "$BASE/" || true)
  [[ "$BODY3" == *"hello from phleet"* ]] && break
  sleep 0.5
done
[[ "$BODY3" == *"hello from phleet"* ]] || fail "rollback did not restore the previous release: $BODY3"

say "restart, stop and start"
curl -fsS -X POST "$BASE/api/projects/$PROJECT_ID/restart" "${AUTH[@]}" >/dev/null
sleep 1
BODY4=$(curl -fsS -H "Host: $HOSTNAME" "$BASE/")
[[ "$BODY4" == *"hello from phleet"* ]] || fail "restart broke the app"

curl -fsS -X POST "$BASE/api/projects/$PROJECT_ID/stop" "${AUTH[@]}" >/dev/null
sleep 1
CODE=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: $HOSTNAME" "$BASE/")
[ "$CODE" = "502" ] || fail "expected 502 after stop, got $CODE"

curl -fsS -X POST "$BASE/api/projects/$PROJECT_ID/start" "${AUTH[@]}" >/dev/null
for _ in $(seq 1 40); do
  BODY5=$(curl -s -H "Host: $HOSTNAME" "$BASE/" || true)
  [[ "$BODY5" == *"hello from phleet"* ]] && break
  sleep 0.5
done
[[ "$BODY5" == *"hello from phleet"* ]] || fail "start did not bring the app back"

say "creating a SQLite data service for the project"
SERVER_ID=$(curl -fsS "${AUTH[@]}" "$BASE/api/servers" | python3 -c '
import json,sys
print(json.load(sys.stdin)["servers"][0]["id"])
')
DB_PAYLOAD=$(python3 - "$SERVER_ID" "$PROJECT_ID" <<'PY'
import json, sys
print(json.dumps({"name": "E2E DB", "engine": "sqlite", "serverId": sys.argv[1], "projectId": sys.argv[2]}))
PY
)
ADDON=$(curl -fsS -X POST "$BASE/api/addons" "${AUTH[@]}" -H 'Content-Type: application/json' -d "$DB_PAYLOAD")
ADDON_ID=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["id"])' <<<"$ADDON")
echo "  addon $ADDON_ID"

addon_status=""
for _ in $(seq 1 120); do
  ADDON_JSON=$(curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID")
  addon_status=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["status"])' <<<"$ADDON_JSON")
  case "$addon_status" in running|failed) break ;; esac
  sleep 0.5
done
if [ "$addon_status" != "running" ]; then
  curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID/logs" || true
  fail "addon ended as $addon_status"
fi

say "running SQL through the panel console"
QUERY=$(curl -fsS -X POST "$BASE/api/addons/$ADDON_ID/query" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"input":"create table t(x); insert into t values (42); select x from t;"}')
echo "$QUERY" | python3 -c '
import json,sys
d=json.load(sys.stdin)
assert "42" in d["output"], d
print("  console returned 42")
'

say "taking and restoring a backup"
BACKUP=$(curl -fsS -X POST "$BASE/api/addons/$ADDON_ID/backup" "${AUTH[@]}")
BACKUP_ID=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["backup"]["id"])' <<<"$BACKUP")
backup_status=""
for _ in $(seq 1 120); do
  BACKUPS=$(curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID/backups")
  backup_status=$(python3 - "$BACKUP_ID" "$BACKUPS" <<'PY'
import json, sys
want = sys.argv[1]
for b in json.loads(sys.argv[2])["backups"]:
    if b["id"] == want:
        print(b["status"])
        break
PY
)
  case "$backup_status" in complete|failed) break ;; esac
  sleep 0.5
done
[ "$backup_status" = "complete" ] || fail "backup ended as $backup_status"
curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID/backups/$BACKUP_ID/download" -o "$WORK/backup.sqlite"
[ -s "$WORK/backup.sqlite" ] || fail "backup download is empty"

curl -fsS -X POST "$BASE/api/addons/$ADDON_ID/query" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"input":"delete from t;"}' >/dev/null
curl -fsS -X POST "$BASE/api/addons/$ADDON_ID/restore" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"backupId\":\"$BACKUP_ID\"}" >/dev/null
restated=""
for _ in $(seq 1 120); do
  restated=$(curl -fsS "${AUTH[@]}" "$BASE/api/addons/$ADDON_ID" | python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["status"])')
  [ "$restated" = "running" ] && break
  sleep 0.5
done
[ "$restated" = "running" ] || fail "restore did not finish: $restated"
RESTORED=$(curl -fsS -X POST "$BASE/api/addons/$ADDON_ID/query" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"input":"select x from t;"}')
echo "$RESTORED" | python3 -c '
import json,sys
d=json.load(sys.stdin)
assert "42" in d["output"], d
print("  restored row is back")
'

say "verifying the database env is injected on deploy"
DEPLOY3=$(curl -fsS -X POST "$BASE/api/projects/$PROJECT_ID/deploy" "${AUTH[@]}")
DEPLOY3_ID=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["deployments"][0]["id"])' <<<"$DEPLOY3")
status3=""
for _ in $(seq 1 120); do
  DETAIL3=$(curl -fsS "${AUTH[@]}" "$BASE/api/deployments/$DEPLOY3_ID")
  status3=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["deployment"]["status"])' <<<"$DETAIL3")
  case "$status3" in active|failed|rolled_back) break ;; esac
  sleep 0.5
done
[ "$status3" = "active" ] || { echo "$DETAIL3"; fail "redeploy ended as $status3"; }
grep -q '^DATABASE_URL=file:' "$HOME/apps/e2e-app/shared/.env" \
  || fail "DATABASE_URL was not injected: $(cat "$HOME/apps/e2e-app/shared/.env")"
echo "  DATABASE_URL injected"

say "installing rclone for the object-storage test"
mkdir -p "$WORK/agent/bin"
RCLONE_BIN="$WORK/agent/bin/rclone"
if [ ! -x "$RCLONE_BIN" ]; then
  python3 - "$RCLONE_BIN" <<'PY'
import io, os, sys, urllib.request, zipfile
url = "https://downloads.rclone.org/rclone-current-linux-amd64.zip"
req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0 (phleet e2e)"})
data = urllib.request.urlopen(req, timeout=120).read()
z = zipfile.ZipFile(io.BytesIO(data))
member = next(n for n in z.namelist() if n.endswith("/rclone") and not n.endswith("/"))
dest = sys.argv[1]
with open(dest, "wb") as f:
    f.write(z.read(member))
os.chmod(dest, 0o755)
PY
fi
[ -x "$RCLONE_BIN" ] || fail "could not install rclone for the object-storage test"
"$RCLONE_BIN" version | head -1

say "creating an S3 bucket service"
S3_PAYLOAD=$(python3 - "$SERVER_ID" <<'PY'
import json, sys
print(json.dumps({"name": "E2E S3", "engine": "rclone", "serverId": sys.argv[1]}))
PY
)
S3_ADDON=$(curl -fsS -X POST "$BASE/api/addons" "${AUTH[@]}" -H 'Content-Type: application/json' -d "$S3_PAYLOAD")
S3_ID=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["id"])' <<<"$S3_ADDON")
s3_status=""
for _ in $(seq 1 180); do
  S3_JSON=$(curl -fsS "${AUTH[@]}" "$BASE/api/addons/$S3_ID")
  s3_status=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["status"])' <<<"$S3_JSON")
  case "$s3_status" in running|failed) break ;; esac
  sleep 0.5
done
if [ "$s3_status" != "running" ]; then
  curl -fsS "${AUTH[@]}" "$BASE/api/addons/$S3_ID/logs" || true
  fail "S3 addon ended as $s3_status"
fi
S3_HOST=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["hostname"])' <<<"$S3_JSON")
CONN=$(curl -fsS "${AUTH[@]}" "$BASE/api/addons/$S3_ID/connection")
S3_ENDPOINT=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["env"]["S3_ENDPOINT"])' <<<"$CONN")
S3_BUCKET=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["env"]["S3_BUCKET"])' <<<"$CONN")
S3_KEY=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["env"]["S3_ACCESS_KEY_ID"])' <<<"$CONN")
S3_SECRET=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["env"]["S3_SECRET_ACCESS_KEY"])' <<<"$CONN")

rc() {
  : > "$WORK/rclone.conf"
  RCLONE_CONFIG="$WORK/rclone.conf" \
  RCLONE_CONFIG_PHELEET_TYPE=s3 \
  RCLONE_CONFIG_PHELEET_PROVIDER=Other \
  RCLONE_CONFIG_PHELEET_ENDPOINT="$S3_ENDPOINT" \
  RCLONE_CONFIG_PHELEET_ACCESS_KEY_ID="$S3_KEY" \
  RCLONE_CONFIG_PHELEET_SECRET_ACCESS_KEY="$S3_SECRET" \
  RCLONE_CONFIG_PHELEET_FORCE_PATH_STYLE=true \
  RCLONE_CONFIG_PHELEET_NO_CHECK_BUCKET=true \
  "$RCLONE_BIN" "$@"
}

printf 'hello object' > "$WORK/object.txt"
rc copyto "$WORK/object.txt" "pheleet:$S3_BUCKET/objects/hello.txt"
BODY=$(rc cat "pheleet:$S3_BUCKET/objects/hello.txt")
[ "$BODY" = "hello object" ] || fail "S3 round-trip returned $BODY"

say "routing the storage hostname through the control plane"
RESP=$(curl -s -H "Host: $S3_HOST" "$BASE/")
echo "$RESP" | grep -q '<Error>' \
  || fail "storage hostname did not reach rclone: $RESP"

say "using the control-plane TCP bridge for S3"
BRIDGE_JSON=$(curl -fsS "${AUTH[@]}" "$BASE/api/addons/$S3_ID")
BRIDGE_PORT=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"].get("bridgePort") or 0)' <<<"$BRIDGE_JSON")
[ "$BRIDGE_PORT" != "0" ] || fail "S3 addon has no bridge port"
bridge_active=false
for _ in $(seq 1 40); do
  bridge_active=$(curl -fsS "${AUTH[@]}" "$BASE/api/addons/$S3_ID" | python3 -c 'import json,sys; print(str(json.load(sys.stdin)["addon"].get("bridgeActive")).lower())')
  [ "$bridge_active" = "true" ] && break
  sleep 0.25
done
[ "$bridge_active" = "true" ] || fail "TCP bridge is not active"
rcb() {
  : > "$WORK/rclone-bridge.conf"
  RCLONE_CONFIG="$WORK/rclone-bridge.conf" \
  RCLONE_CONFIG_PHELEET_TYPE=s3 \
  RCLONE_CONFIG_PHELEET_PROVIDER=Other \
  RCLONE_CONFIG_PHELEET_ENDPOINT="http://127.0.0.1:$BRIDGE_PORT" \
  RCLONE_CONFIG_PHELEET_ACCESS_KEY_ID="$S3_KEY" \
  RCLONE_CONFIG_PHELEET_SECRET_ACCESS_KEY="$S3_SECRET" \
  RCLONE_CONFIG_PHELEET_FORCE_PATH_STYLE=true \
  RCLONE_CONFIG_PHELEET_NO_CHECK_BUCKET=true \
  "$RCLONE_BIN" "$@"
}
printf 'bridge object' > "$WORK/bridge.txt"
rcb copyto "$WORK/bridge.txt" "pheleet:$S3_BUCKET/objects/bridge.txt"
BODY=$(rcb cat "pheleet:$S3_BUCKET/objects/bridge.txt")
[ "$BODY" = "bridge object" ] || fail "bridge round-trip returned $BODY"
echo "  bridge port $BRIDGE_PORT works"

say "backing up and restoring the bucket"
S3_BACKUP=$(curl -fsS -X POST "$BASE/api/addons/$S3_ID/backup" "${AUTH[@]}")
S3_BACKUP_ID=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["backup"]["id"])' <<<"$S3_BACKUP")
s3_backup_status=""
for _ in $(seq 1 180); do
  S3_BACKUPS=$(curl -fsS "${AUTH[@]}" "$BASE/api/addons/$S3_ID/backups")
  s3_backup_status=$(python3 - "$S3_BACKUP_ID" "$S3_BACKUPS" <<'PY'
import json, sys
want = sys.argv[1]
for b in json.loads(sys.argv[2])["backups"]:
    if b["id"] == want:
        print(b["status"])
        break
PY
)
  case "$s3_backup_status" in complete|failed) break ;; esac
  sleep 0.5
done
[ "$s3_backup_status" = "complete" ] || fail "S3 backup ended as $s3_backup_status"
rc deletefile "pheleet:$S3_BUCKET/objects/hello.txt"
if rc lsf "pheleet:$S3_BUCKET/objects/" | grep -qx 'hello.txt'; then
  fail "S3 object was not deleted before restore"
fi
curl -fsS -X POST "$BASE/api/addons/$S3_ID/restore" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"backupId\":\"$S3_BACKUP_ID\"}" >/dev/null
s3_restored=""
for _ in $(seq 1 180); do
  s3_restored=$(curl -fsS "${AUTH[@]}" "$BASE/api/addons/$S3_ID" | python3 -c 'import json,sys; print(json.load(sys.stdin)["addon"]["status"])')
  [ "$s3_restored" = "running" ] && break
  sleep 0.5
done
[ "$s3_restored" = "running" ] || fail "S3 restore did not finish: $s3_restored"
BODY2=$(rc cat "pheleet:$S3_BUCKET/objects/hello.txt")
[ "$BODY2" = "hello object" ] || fail "S3 restore lost the object: $BODY2"
echo "  object restored"

# Leave the WSL home clean for the next run.
curl -fsS -X DELETE "$BASE/api/addons/$S3_ID?force=1&purge=1" "${AUTH[@]}" >/dev/null
curl -fsS -X DELETE "$BASE/api/addons/$ADDON_ID?force=1&purge=1" "${AUTH[@]}" >/dev/null

echo
echo "PASS — deploy, rollback, supervision, tunnel routing, data services, TCP bridge and S3 all work"
