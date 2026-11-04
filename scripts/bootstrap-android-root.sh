#!/system/bin/sh
# phleet agent bootstrap for a rooted Android phone WITHOUT Termux.
#
# This installs the static agent in /data/local/tmp (or --dir), runs it as
# root, and registers a Magisk service.d hook so it starts at boot. There is no
# package manager and no Linux userland in this setup: the phone reports
# metrics, battery, thermal, shell and files, but the panel will not offer
# deploys or addons on it. Install Termux (or a chroot with git + a runtime) if
# you want it to run projects.
#
# Typical use from a PC with adb:
#
#   adb push dist/bin/phleet-agent-linux-arm64 /data/local/tmp/phleet/bin/phleet-agent
#   adb push scripts/bootstrap-android-root.sh /data/local/tmp/phleet/
#   adb shell "su -c 'sh /data/local/tmp/phleet/bootstrap-android-root.sh \
#       --server https://fleet.example.com --token <agent-token> --name pixel-4a'"
#
# The agent dials out; no inbound port is opened. Run as root, not as shell.
set -eu

SERVER=""
TOKEN=""
NAME=""
DIR="/data/local/tmp/phleet"
BINARY=""
ROOTS=""
AUTOSTART=1
FOREGROUND=0

usage() {
  cat <<EOF
usage: bootstrap-android-root.sh --server URL --token TOKEN [options]

  --server URL    control plane, e.g. https://fleet.example.com
  --token TOKEN   agent token from the control plane
  --name NAME     display name (default: device model)
  --dir DIR       install directory (default /data/local/tmp/phleet)
  --binary PATH   agent binary to install (default DIR/bin/phleet-agent)
  --root DIR      extra directory the file browser and shell may access (repeatable)
  --no-autostart  do not write a Magisk service.d hook
  --foreground    run the agent in the foreground after installing
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    --name) NAME="${2:-}"; shift 2 ;;
    --dir) DIR="${2:-}"; shift 2 ;;
    --binary) BINARY="${2:-}"; shift 2 ;;
    --root) ROOTS="$ROOTS --root ${2:-}"; shift 2 ;;
    --no-autostart) AUTOSTART=0; shift ;;
    --foreground) FOREGROUND=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

die() { echo "error: $*" >&2; exit 1; }
say() { printf '\033[1;32m::\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*"; }

TOKEN="${TOKEN:-${PHLEET_BOOTSTRAP_TOKEN:-}}"
[ -n "$SERVER" ] || die "--server is required"
[ -n "$TOKEN" ] || die "--token is required"
[ "$(id -u)" = "0" ] || die "run this as root (adb shell su -c 'sh ...' or a root terminal)"
case "$SERVER" in
  http://*|https://*) ;;
  *) SERVER="https://$SERVER" ;;
esac
SERVER="${SERVER%/}"

if [ -z "$NAME" ]; then
  NAME="$(getprop ro.product.model 2>/dev/null || true)"
  [ -n "$NAME" ] || NAME="$(hostname 2>/dev/null || echo android)"
  NAME="$(printf '%s' "$NAME" | tr ' ' '-' | tr -cd 'A-Za-z0-9._-')"
  [ -n "$NAME" ] || NAME="android"
fi

say "installing into $DIR"
mkdir -p "$DIR/bin" "$DIR/state" "$DIR/apps" "$DIR/data" "$DIR/tmp"
chmod 700 "$DIR"

BIN="$DIR/bin/phleet-agent"
if [ -n "$BINARY" ] && [ "$BINARY" != "$BIN" ]; then
  cp "$BINARY" "$BIN" || die "could not copy $BINARY"
fi
if [ ! -f "$BIN" ]; then
  # No curl on stock Android, and toybox wget cannot send the agent token.
  # If curl happens to be present, fetch the binary from the control plane.
  if command -v curl >/dev/null 2>&1; then
    arch="$(uname -m)"
    case "$arch" in
      aarch64|arm64) ARCH=arm64 ;;
      armv7l|armv8l) ARCH=arm ;;
      x86_64|amd64) ARCH=amd64 ;;
      *) die "unsupported architecture: $arch" ;;
    esac
    say "downloading agent for linux/$ARCH"
    curl -fsSL -H "Authorization: Bearer $TOKEN" \
      "$SERVER/download/agent?arch=$ARCH" -o "$BIN.tmp" \
      || die "download failed; push the binary instead (see --binary)"
    mv "$BIN.tmp" "$BIN"
  else
    die "no agent binary at $BIN and no curl to download one
push it first, then re-run:
  adb push dist/bin/phleet-agent-linux-arm64 $BIN"
  fi
fi
chmod 755 "$BIN"

if ! "$BIN" --version >/dev/null 2>&1; then
  die "$BIN is not a working agent binary (wrong architecture?)"
fi
say "agent $("$BIN" --version)"

# /sdcard is the useful shared root; keep /data opt-in with --root /data.
if [ -d /sdcard ]; then
  ROOTS="$ROOTS --root /sdcard"
fi

umask 077
sq() { printf '%s' "$1" | sed "s/'/'\\\\''/g"; }
cat > "$DIR/agent.env" <<EOF
PHLEET_URL='$(sq "$SERVER")'
PHLEET_NAME='$(sq "$NAME")'
PHLEET_ROOTS='$(sq "$ROOTS")'
EOF
chmod 600 "$DIR/agent.env"
printf '%s\n' "$TOKEN" > "$DIR/agent.token"
chmod 600 "$DIR/agent.token"
mkdir -p "$DIR/state"
if [ ! -s "$DIR/state/update.pub" ]; then
  curl -fsSL -H "Authorization: Bearer $TOKEN" \
    "$SERVER/download/update.pub" -o "$DIR/state/update.pub" 2>/dev/null || true
  [ -s "$DIR/state/update.pub" ] && chmod 644 "$DIR/state/update.pub"
fi

cat > "$DIR/run-agent.sh" <<EOF
#!/system/bin/sh
set -eu
DIR="$DIR"
. "\$DIR/agent.env"
unset PHLEET_TOKEN PHLEET_TOKEN_FILE
export HOME="\$DIR"
export TMPDIR="\$DIR/tmp"
export SSL_CERT_DIR="\${SSL_CERT_DIR:-/system/etc/security/cacerts}"
mkdir -p "\$TMPDIR"
# shellcheck disable=SC2086
exec "\$DIR/bin/phleet-agent" \\
  --server "\$PHLEET_URL" \\
  --token-file "\$DIR/agent.token" \\
  --name "\$PHLEET_NAME" \\
  --state "\$DIR/state" \\
  \$PHLEET_ROOTS
EOF
chmod 700 "$DIR/run-agent.sh"

if [ "$AUTOSTART" -eq 1 ]; then
  HOOK=""
  if [ -d /data/adb ]; then
    mkdir -p /data/adb/service.d 2>/dev/null || true
    HOOK=/data/adb/service.d/99-phleet.sh
  elif [ -d /system/etc/init.d ] && [ -w /system/etc/init.d ]; then
    HOOK=/system/etc/init.d/99-phleet
  fi
  if [ -n "$HOOK" ]; then
    cat > "$HOOK" <<EOF
#!/system/bin/sh
# phleet agent (installed by bootstrap-android-root.sh)
sleep 20
DIR="$DIR"
[ -x "\$DIR/run-agent.sh" ] || exit 0
cd "\$DIR" || exit 0
if command -v nohup >/dev/null 2>&1; then
  nohup "\$DIR/run-agent.sh" >> "\$DIR/agent.log" 2>&1 &
else
  "\$DIR/run-agent.sh" >> "\$DIR/agent.log" 2>&1 &
fi
EOF
    chmod 700 "$HOOK"
    say "autostart hook: $HOOK"
  else
    warn "no /data/adb or writable init.d; the agent will not start at boot"
    warn "re-run with --no-autostart to silence this"
  fi
fi

if command -v pkill >/dev/null 2>&1; then
  pkill -f "$BIN" 2>/dev/null || true
  sleep 1
fi
if [ "$FOREGROUND" -eq 0 ]; then
  if command -v nohup >/dev/null 2>&1; then
    nohup "$DIR/run-agent.sh" >> "$DIR/agent.log" 2>&1 &
  else
    "$DIR/run-agent.sh" >> "$DIR/agent.log" 2>&1 &
  fi
  sleep 1
  say "started (logs: $DIR/agent.log)"
else
  say "starting in the foreground (ctrl-c to stop)"
fi

cat <<EOF

$(printf '\033[1;32m::\033[0m') agent installed as "$NAME"

  binary   $BIN
  env      $DIR/agent.env
  logs     $DIR/agent.log
  state    $DIR/state

This phone has no Termux/userland, so the panel treats it as monitoring-only:
metrics, battery, thermal, shell and files work; deploys and data addons do not.

Android will still freeze background work sometimes. From a PC:
  adb shell dumpsys deviceidle whitelist +com.android.shell   # if it sleeps
  adb shell settings put global stay_on_while_plugged_in 3    # optional

Remove it:
  adb shell "su -c 'pkill -f $BIN; rm -f /data/adb/service.d/99-phleet.sh; rm -rf $DIR'"
EOF

if [ "$FOREGROUND" -eq 1 ]; then
  say "running in the foreground (ctrl-c to stop)"
  exec "$DIR/run-agent.sh"
fi
