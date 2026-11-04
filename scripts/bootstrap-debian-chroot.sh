#!/system/bin/sh
# phleet agent bootstrap for a rooted Android phone running a Debian chroot.
#
# Run this on the ANDROID HOST as root (not inside the chroot):
#
#   ssh root@<phone> 'sh -s' < scripts/bootstrap-debian-chroot.sh -- \
#       --server https://fleet.example.com --token <agent-token> --name pixel-4a
#
# or, with adb:
#
#   adb push scripts/bootstrap-debian-chroot.sh /data/local/tmp/
#   adb shell "su -c 'sh /data/local/tmp/bootstrap-debian-chroot.sh --server ... --token ...'"
#
# It expects the chroot created by the Podroid/Linux-Deploy style setup:
#   /data/debian/rootfs        Debian root filesystem
#   /data/debian/rootfs/proc   bind-mounted /proc (metrics and supervision)
#
# The agent runs as a normal user inside the chroot, never as root: PostgreSQL
# and initdb refuse to run as root, so the phone behaves like a Termux phone.
set -eu

SERVER=""
TOKEN=""
NAME=""
ROOT="/data/debian/rootfs"
USER_NAME="phleet"
BINARY=""
WITH=""
AUTOSTART=1
FOREGROUND=0

usage() {
  cat <<EOF
usage: bootstrap-debian-chroot.sh --server URL --token TOKEN [options]

  --server URL    control plane, e.g. https://fleet.example.com
  --token TOKEN   agent token from the control plane
  --name NAME     display name (default: device model)
  --root DIR      Debian rootfs (default /data/debian/rootfs)
  --user NAME     unprivileged user that runs the agent (default phleet)
  --binary PATH   agent binary on the Android filesystem (default: download)
  --with LIST     extra apt packages: postgres,redis,sqlite,rclone,node (comma separated)
  --no-autostart  do not write a Magisk service.d hook
  --foreground    run the agent in the foreground after installing
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    --name) NAME="${2:-}"; shift 2 ;;
    --root) ROOT="${2:-}"; shift 2 ;;
    --user) USER_NAME="${2:-}"; shift 2 ;;
    --binary) BINARY="${2:-}"; shift 2 ;;
    --with) WITH="${2:-}"; shift 2 ;;
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
[ "$(id -u)" = "0" ] || die "run this as root on the Android host"
case "$SERVER" in
  http://*|https://*) ;;
  *) SERVER="https://$SERVER" ;;
esac
SERVER="${SERVER%/}"

[ -d "$ROOT" ] || die "$ROOT does not exist; start the Debian chroot first"
[ -x "$ROOT/bin/sh" ] || die "$ROOT does not look like a Debian rootfs"
chroot "$ROOT" /bin/sh -c 'exit 0' >/dev/null 2>&1 || die "chroot $ROOT does not run"
[ -r "$ROOT/proc/1/cmdline" ] || warn "$ROOT/proc is not mounted; metrics will be empty until the boot hook mounts it"

if [ -z "$NAME" ]; then
  NAME="$(getprop ro.product.model 2>/dev/null || true)"
  [ -n "$NAME" ] || NAME="android"
  NAME="$(printf '%s' "$NAME" | tr ' ' '-' | tr -cd 'A-Za-z0-9._-')"
  [ -n "$NAME" ] || NAME="android"
fi

# chroot starts services that have no systemd; 101 is the standard "do not".
if [ ! -e "$ROOT/usr/sbin/policy-rc.d" ]; then
  printf '#!/bin/sh\nexit 101\n' > "$ROOT/usr/sbin/policy-rc.d"
  chmod 755 "$ROOT/usr/sbin/policy-rc.d"
fi

say "checking the Debian userland"
for tool in git curl; do
  if ! chroot "$ROOT" /bin/sh -c "command -v $tool" >/dev/null 2>&1; then
    say "installing $tool inside Debian"
    chroot "$ROOT" /bin/sh -c "DEBIAN_FRONTEND=noninteractive apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y $tool ca-certificates" \
      || die "could not install $tool inside the chroot"
  fi
done

if ! chroot "$ROOT" id -u "$USER_NAME" >/dev/null 2>&1; then
  say "creating unprivileged user $USER_NAME (PostgreSQL will not run as root)"
  chroot "$ROOT" useradd -m -s /bin/bash "$USER_NAME" || die "could not create $USER_NAME"
fi

# Android's kernel only lets UIDs in AID_INET (3003) open sockets. A chroot user
# outside that group can read files but every outbound connection fails with
# "socket: permission denied", which looks like broken DNS.
if ! chroot "$ROOT" getent group 3003 >/dev/null 2>&1; then
  chroot "$ROOT" groupadd -g 3003 inet || warn "could not create the inet group"
fi
chroot "$ROOT" usermod -aG inet "$USER_NAME" || warn "could not add $USER_NAME to the inet group"

# Older phleet bootstraps granted the agent user passwordless sudo for
# apt-get/apt/dpkg. That made every project running as $USER_NAME root-equivalent
# (a .deb maintainer script runs as root). Remove that legacy rule. Packaged
# engines can still be installed once as root from the host, or preinstalled
# with --with; engines with static installers do not need root.
LEGACY_SUDOERS="$ROOT/etc/sudoers.d/phleet"
if [ -f "$LEGACY_SUDOERS" ] && grep -q 'package management only' "$LEGACY_SUDOERS" 2>/dev/null; then
  rm -f "$LEGACY_SUDOERS"
  say "removed the legacy phleet sudoers rule (agent stays unprivileged)"
fi

# apt must never stop on a config prompt in an unattended chroot.
mkdir -p "$ROOT/etc/apt/apt.conf.d"
cat > "$ROOT/etc/apt/apt.conf.d/99phleet" <<'EOF'
Dpkg::Options { "--force-confdef"; "--force-confold"; };
EOF
grep -q '^DEBIAN_FRONTEND=' "$ROOT/etc/environment" 2>/dev/null \
  || echo 'DEBIAN_FRONTEND=noninteractive' >> "$ROOT/etc/environment"

if [ -n "$WITH" ]; then
  pkgs=""
  for item in $(printf '%s' "$WITH" | tr ',' ' '); do
    case "$item" in
      postgres|postgresql) pkgs="$pkgs postgresql" ;;
      redis) pkgs="$pkgs redis-server" ;;
      sqlite|sqlite3) pkgs="$pkgs sqlite3" ;;
      rclone|s3) pkgs="$pkgs rclone" ;;
      node|nodejs) pkgs="$pkgs nodejs npm" ;;
      git|curl) pkgs="$pkgs $item" ;;
      "") ;;
      *) warn "unknown package '$item' (expected postgres, redis, sqlite, rclone, node)" ;;
    esac
  done
  if [ -n "$pkgs" ]; then
    say "installing$pkgs inside Debian (this can take a few minutes)"
    chroot "$ROOT" /bin/sh -c "DEBIAN_FRONTEND=noninteractive apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y$pkgs" \
      || warn "apt install failed; the panel can retry per addon later"
  fi
fi

INNER="/home/$USER_NAME/.phleet"
HOST_DIR="$ROOT$INNER"
mkdir -p "$HOST_DIR/bin" "$HOST_DIR/state" "$HOST_DIR/data"
chroot "$ROOT" chown -R "$USER_NAME:$USER_NAME" "$INNER"
chmod 700 "$HOST_DIR"

BIN="$HOST_DIR/bin/phleet-agent"
# Stop the old agent before replacing the binary: a running executable cannot
# be overwritten (ETXTBSY).
if command -v pkill >/dev/null 2>&1; then
  pkill -f "$INNER/bin/phleet-agent" 2>/dev/null || true
  sleep 1
fi
if [ -n "$BINARY" ]; then
  cp "$BINARY" "$BIN" || die "could not copy $BINARY"
fi
if [ ! -f "$BIN" ]; then
  case "$(uname -m)" in
    aarch64|arm64) ARCH=arm64 ;;
    armv7l|armv8l) ARCH=arm ;;
    x86_64|amd64) ARCH=amd64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac
  say "downloading agent for linux/$ARCH inside Debian"
  chroot "$ROOT" /bin/sh -c "curl -fsSL -H 'Authorization: Bearer $TOKEN' '$SERVER/download/agent?arch=$ARCH' -o '$INNER/bin/phleet-agent.tmp'" \
    || die "download failed; pass --binary with a pushed phleet-agent-linux-$ARCH"
  mv "$BIN.tmp" "$BIN"
fi
chmod 755 "$BIN"
chroot "$ROOT" chown "$USER_NAME:$USER_NAME" "$INNER/bin/phleet-agent"

if ! chroot "$ROOT" /bin/sh -c "$INNER/bin/phleet-agent --version" >/dev/null 2>&1; then
  die "$BIN is not a working agent binary (wrong architecture?)"
fi
say "agent $(chroot "$ROOT" /bin/sh -c "$INNER/bin/phleet-agent --version")"

umask 077
sq() { printf '%s' "$1" | sed "s/'/'\\\\''/g"; }
cat > "$HOST_DIR/agent.env" <<EOF
PHLEET_URL='$(sq "$SERVER")'
PHLEET_NAME='$(sq "$NAME")'
EOF
chmod 600 "$HOST_DIR/agent.env"
printf '%s\n' "$TOKEN" > "$HOST_DIR/agent.token"
chmod 600 "$HOST_DIR/agent.token"
if [ ! -s "$HOST_DIR/state/update.pub" ]; then
  chroot "$ROOT" /bin/sh -c "mkdir -p '$INNER/state' && curl -fsSL -H 'Authorization: Bearer $TOKEN' '$SERVER/download/update.pub' -o '$INNER/state/update.pub'" 2>/dev/null || true
  chroot "$ROOT" chown "$USER_NAME:$USER_NAME" "$INNER/state/update.pub" 2>/dev/null || true
  chmod 644 "$HOST_DIR/state/update.pub" 2>/dev/null || true
fi

cat > "$HOST_DIR/run-agent.sh" <<EOF
#!/bin/sh
set -eu
DIR="$INNER"
. "\$DIR/agent.env"
unset PHLEET_TOKEN PHLEET_TOKEN_FILE
export HOME="/home/$USER_NAME"
export TMPDIR=/tmp
# runuser starts with a minimal PATH; npm global binaries live in /usr/local/bin.
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
exec "\$DIR/bin/phleet-agent" \\
  --server "\$PHLEET_URL" \\
  --token-file "\$DIR/agent.token" \\
  --name "\$PHLEET_NAME" \\
  --state "\$DIR/state"
EOF
chmod 700 "$HOST_DIR/run-agent.sh"
chroot "$ROOT" chown -R "$USER_NAME:$USER_NAME" "$INNER"

# runuser ships with util-linux in Debian; fall back to su if it is missing.
RUN_AS=""
if [ -x "$ROOT/usr/sbin/runuser" ] || [ -x "$ROOT/usr/bin/runuser" ]; then
  RUN_AS="runuser -u $USER_NAME -- $INNER/run-agent.sh"
elif [ -x "$ROOT/bin/su" ]; then
  RUN_AS="su -s /bin/sh $USER_NAME -c $INNER/run-agent.sh"
else
  die "neither runuser nor su exists inside the chroot"
fi

if [ "$AUTOSTART" -eq 1 ]; then
  if [ ! -d /data/adb ]; then
    warn "no /data/adb (Magisk); the agent will not start at boot"
  else
    mkdir -p /data/adb/service.d
    cat > /data/adb/service.d/98-phleet-debian.sh <<EOF
#!/system/bin/sh
# phleet agent inside the Debian chroot (installed by bootstrap-debian-chroot.sh)
ROOT="$ROOT"
LOG="/data/debian/phleet-agent.log"
i=0
while [ \$i -lt 60 ]; do
  [ -r "\$ROOT/proc/1/cmdline" ] && break
  sleep 2
  i=\$((i+1))
done
sleep 5
[ -x "\$ROOT$INNER/run-agent.sh" ] || exit 0
if command -v pkill >/dev/null 2>&1; then
  pkill -f "$INNER/bin/phleet-agent" 2>/dev/null || true
fi
chroot "\$ROOT" $RUN_AS >> "\$LOG" 2>&1 &
EOF
    chmod 700 /data/adb/service.d/98-phleet-debian.sh
    say "autostart hook: /data/adb/service.d/98-phleet-debian.sh"
  fi
fi

if command -v pkill >/dev/null 2>&1; then
  pkill -f "$INNER/bin/phleet-agent" 2>/dev/null || true
  sleep 1
fi
if [ "$FOREGROUND" -eq 0 ]; then
  chroot "$ROOT" $RUN_AS >> /data/debian/phleet-agent.log 2>&1 &
  sleep 3
  if pgrep -f "$INNER/bin/phleet-agent" >/dev/null 2>&1; then
    say "started (logs: /data/debian/phleet-agent.log)"
  else
    warn "the agent exited; last lines:"
    tail -5 /data/debian/phleet-agent.log 2>/dev/null || true
  fi
else
  say "starting in the foreground (ctrl-c to stop)"
fi

cat <<EOF

$(printf '\033[1;32m::\033[0m') agent installed as "$NAME" inside the Debian chroot

  user     $USER_NAME
  binary   $INNER/bin/phleet-agent
  env      $INNER/agent.env
  logs     /data/debian/phleet-agent.log

The phone is a full node: projects and data addons work, installed with apt.
PostgreSQL's binaries live in /usr/lib/postgresql/<version>/bin; the agent adds
that directory to PATH at startup, so nothing else needs configuring.

Remove it:
  pkill -f $INNER/bin/phleet-agent
  rm -f /data/adb/service.d/98-phleet-debian.sh
  rm -rf $HOST_DIR
EOF

if [ "$FOREGROUND" -eq 1 ]; then
  exec chroot "$ROOT" $RUN_AS
fi
