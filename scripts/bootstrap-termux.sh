#!/data/data/com.termux/files/usr/bin/bash
# phleet agent bootstrap for Termux.
#
#   curl -fsSL https://fleet.example.com/download/bootstrap.sh \
#     | bash -s -- --server https://fleet.example.com --token <agent-token>
#
# The agent dials out to the control plane; no inbound ports are opened.
set -eu

SERVER=""
TOKEN=""
NAME=""
ROOT_ARGS=""
NO_WAKE_LOCK=0
FOREGROUND=0
WITH=""

usage() {
  cat <<EOF
usage: bootstrap.sh --server URL --token TOKEN [options]

  --server URL     control plane, e.g. https://fleet.example.com
  --token TOKEN    agent token from the control plane
  --name NAME      display name (default: device model)
  --root DIR       extra directory the file browser may access (repeatable)
  --with LIST      preinstall data engines: postgres, redis, sqlite, rclone (comma separated)
  --no-wake-lock   do not acquire a Termux wake lock
  --foreground     run the agent in the foreground after installing
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    --name) NAME="${2:-}"; shift 2 ;;
    --root) ROOT_ARGS="$ROOT_ARGS --root ${2:-}"; shift 2 ;;
    --with) WITH="${2:-}"; shift 2 ;;
    --no-wake-lock) NO_WAKE_LOCK=1; shift ;;
    --foreground) FOREGROUND=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

die() { echo "error: $*" >&2; exit 1; }
say() { printf '\033[1;32m::\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*"; }

[ -n "$SERVER" ] || die "--server is required"
[ -n "$TOKEN" ] || die "--token is required"
case "$SERVER" in
  http://*|https://*) ;;
  *) SERVER="https://$SERVER" ;;
esac
SERVER="${SERVER%/}"

if ! command -v pkg >/dev/null 2>&1; then
  die "this script must run inside Termux (pkg not found)"
fi

if [ -z "$NAME" ]; then
  NAME="$(getprop ro.product.model 2>/dev/null || true)"
  [ -n "$NAME" ] || NAME="$(hostname 2>/dev/null || echo android)"
  # keep it readable in the UI
  NAME="$(printf '%s' "$NAME" | tr ' ' '-' | tr -cd 'A-Za-z0-9._-')"
  [ -n "$NAME" ] || NAME="android"
fi

say "installing into $HOME/.phleet"
mkdir -p "$HOME/.phleet/bin"
chmod 700 "$HOME/.phleet"

if ! command -v curl >/dev/null 2>&1; then
  say "installing curl"
  pkg install -y curl >/dev/null || die "could not install curl"
fi

# git is required to deploy projects; node/python are app-specific.
if ! command -v git >/dev/null 2>&1; then
  say "installing git (needed for deploys)"
  pkg install -y git >/dev/null || warn "could not install git; deploys will fail until it is present"
fi

# Data engines are optional and can also be installed on demand from the panel.
if [ -n "$WITH" ]; then
  for engine in $(printf '%s' "$WITH" | tr ',' ' '); do
    case "$engine" in
      postgres|postgresql)
        if ! command -v postgres >/dev/null 2>&1; then
          say "installing PostgreSQL"
          pkg install -y postgresql >/dev/null || warn "could not install postgresql"
        fi
        ;;
      redis)
        if ! command -v redis-server >/dev/null 2>&1; then
          say "installing Redis"
          pkg install -y redis >/dev/null || warn "could not install redis"
        fi
        ;;
      sqlite|sqlite3)
        if ! command -v sqlite3 >/dev/null 2>&1; then
          say "installing SQLite"
          pkg install -y sqlite >/dev/null || warn "could not install sqlite"
        fi
        ;;
      rclone|s3)
        if ! command -v rclone >/dev/null 2>&1; then
          say "installing rclone (S3 storage)"
          pkg install -y rclone >/dev/null || warn "could not install rclone"
        fi
        ;;
      "" ) ;;
      *) warn "unknown engine '$engine' (expected postgres, redis or sqlite)" ;;
    esac
  done
fi

mkdir -p "$HOME/phleet/data"
chmod 700 "$HOME/phleet" 2>/dev/null || true

arch="$(uname -m)"
case "$arch" in
  aarch64|arm64) ARCH=arm64 ;;
  armv7l|armv8l) ARCH=arm ;;
  x86_64|amd64) ARCH=amd64 ;;
  *) die "unsupported architecture: $arch" ;;
esac

BIN="$HOME/.phleet/bin/phleet-agent"
say "downloading agent for linux/$ARCH"
curl -fsSL -H "Authorization: Bearer $TOKEN" \
  "$SERVER/download/agent?arch=$ARCH" -o "$BIN.tmp" \
  || die "download failed; check the URL and agent token"
mv "$BIN.tmp" "$BIN"
chmod 755 "$BIN"

if ! "$BIN" --version >/dev/null 2>&1; then
  die "downloaded file is not a working agent binary (is the control plane built with its agent?)"
fi
say "agent $("$BIN" --version)"

umask 077
sq() { printf '%s' "$1" | sed "s/'/'\\\\''/g"; }
cat > "$HOME/.phleet/agent.env" <<EOF
PHLEET_URL='$(sq "$SERVER")'
PHLEET_NAME='$(sq "$NAME")'
PHLEET_EXTRA_ARGS='$(sq "$ROOT_ARGS")'
SSL_CERT_FILE='$(sq "$PREFIX/etc/tls/cert.pem")'
EOF
chmod 600 "$HOME/.phleet/agent.env"
# Keep the token in a file, not in the agent's argv. Deployed apps share this
# Unix user, so this is defence in depth rather than perfect isolation.
printf '%s\n' "$TOKEN" > "$HOME/.phleet/agent.token"
chmod 600 "$HOME/.phleet/agent.token"

cat > "$HOME/.phleet/run-agent.sh" <<'EOF'
#!/data/data/com.termux/files/usr/bin/sh
set -eu
. "$HOME/.phleet/agent.env"
# Old installs kept the token in agent.env; do not leak it to child processes.
unset PHLEET_TOKEN PHLEET_TOKEN_FILE
# shellcheck disable=SC2086
exec "$HOME/.phleet/bin/phleet-agent" \
  --server "$PHLEET_URL" \
  --token-file "$HOME/.phleet/agent.token" \
  --name "$PHLEET_NAME" \
  $PHLEET_EXTRA_ARGS
EOF
chmod 700 "$HOME/.phleet/run-agent.sh"

# termux-services (runit) keeps the agent alive across shell exits.
if ! command -v sv >/dev/null 2>&1; then
  say "installing termux-services (runit supervisor)"
  pkg install -y termux-services >/dev/null 2>&1 || warn "could not install termux-services; falling back to a background process"
fi

if command -v sv >/dev/null 2>&1 && command -v runsvdir >/dev/null 2>&1; then
  # Current termux-services uses $PREFIX/var/service (older builds used
  # ~/.termux/service). Keep SVDIR explicit so this also works from a
  # non-interactive SSH shell.
  SVDIR="${SVDIR:-$PREFIX/var/service}"
  export SVDIR
  mkdir -p "$SVDIR/phleet-agent"
  cat > "$SVDIR/phleet-agent/run" <<'EOF'
#!/data/data/com.termux/files/usr/bin/sh
: "${HOME:=/data/data/com.termux/files/home}"
exec "$HOME/.phleet/run-agent.sh" >> "$HOME/.phleet/agent.log" 2>&1
EOF
  chmod 700 "$SVDIR/phleet-agent/run"

  # The supervisor itself is normally started by termux-services' profile
  # hook on login; start it here too so provisioning works immediately.
  if ! pgrep -x runsvdir >/dev/null 2>&1; then
    nohup runsvdir "$SVDIR" >> "$HOME/.phleet/runsvdir.log" 2>&1 &
    sleep 2
  fi
  pkill -f "$HOME/.phleet/bin/phleet-agent" 2>/dev/null || true
  sleep 1
  if sv up phleet-agent 2>/dev/null; then
    say "started as a runit service (sv status phleet-agent)"
  else
    warn "could not start the service now; open a new Termux session and run: sv up phleet-agent"
  fi
else
  warn "termux-services is not available; falling back to a background process"
  warn "for a supervised service: pkg install termux-services, restart Termux, re-run this script"
  pkill -f "$HOME/.phleet/bin/phleet-agent" 2>/dev/null || true
  nohup "$HOME/.phleet/run-agent.sh" >> "$HOME/.phleet/agent.log" 2>&1 &
  echo $! > "$HOME/.phleet/agent.pid"
  say "started in the background (logs: ~/.phleet/agent.log)"
fi

if [ "$NO_WAKE_LOCK" -eq 0 ] && command -v termux-wake-lock >/dev/null 2>&1; then
  termux-wake-lock 2>/dev/null && say "wake lock acquired"
fi

# Termux:Boot — survive a reboot when the addon is installed.
mkdir -p "$HOME/.termux/boot"
cat > "$HOME/.termux/boot/50-phleet" <<'EOF'
#!/data/data/com.termux/files/usr/bin/sh
termux-wake-lock 2>/dev/null || true
PREFIX="${PREFIX:-/data/data/com.termux/files/usr}"
SVDIR="${SVDIR:-$PREFIX/var/service}"
export SVDIR
if command -v runsvdir >/dev/null 2>&1; then
  pgrep -x runsvdir >/dev/null 2>&1 || {
    nohup runsvdir "$SVDIR" >> "$HOME/.phleet/runsvdir.log" 2>&1 &
    sleep 2
  }
  sv up phleet-agent 2>/dev/null || true
else
  sleep 5
  "$HOME/.phleet/run-agent.sh" >/dev/null 2>&1 &
fi
EOF
chmod 700 "$HOME/.termux/boot/50-phleet"

cat <<EOF

$(printf '\033[1;32m::\033[0m') agent installed as "$NAME"

  env      ~/.phleet/agent.env
  binary   ~/.phleet/bin/phleet-agent
  logs     ~/.phleet/agent.log  (or: sv log / logcat)

Android will freeze background work unless you:
  1. disable battery optimisation for Termux (Settings > Apps > Termux > Battery > Unrestricted)
  2. keep the wake lock (already requested)
  3. optionally stop the phantom process killer over ADB:
       adb shell device_config put activity_manager max_phantom_processes 2147483647

The phone should appear in the panel within a few seconds.
EOF

if [ "$FOREGROUND" -eq 1 ]; then
  say "running in the foreground (ctrl-c to stop)"
  exec "$HOME/.phleet/run-agent.sh"
fi
