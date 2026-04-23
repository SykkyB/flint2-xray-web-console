#!/usr/bin/env bash
# Cross-compile xray-panel and install it on an OpenWrt router via SSH.
#
# Usage:   deploy/install.sh flint2
# (expects an ssh alias in ~/.ssh/config; a bare root@host works too)
#
# The script is idempotent: it copies the binary and init script, and
# writes /etc/xray-panel/panel.yaml from the example on the first run
# only. It never overwrites an existing config, so credentials and
# server_address survive reinstalls.

set -euo pipefail

if [ $# -lt 1 ]; then
	echo "usage: $0 <ssh-target>   (e.g. flint2 or root@192.168.100.1)" >&2
	exit 2
fi

TARGET="$1"
SSH_OPTS="${SSH_OPTS:--o StrictHostKeyChecking=accept-new}"
# -O forces legacy scp1 transfer: OpenWrt's dropbear has no sftp-server,
# and macOS's OpenSSH 9+ picks sftp by default.
SCP_OPTS="${SCP_OPTS:--O}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR="$(mktemp -d)"
BIN="$BUILD_DIR/xray-panel"

cleanup() { rm -rf "$BUILD_DIR"; }
trap cleanup EXIT

echo ">>> building xray-panel for linux/arm64"
(
	cd "$REPO_ROOT"
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		go build -trimpath -ldflags='-s -w' -o "$BIN" ./cmd/xray-panel
)
ls -la "$BIN"

echo ">>> copying artifacts to $TARGET"
# shellcheck disable=SC2086
scp $SSH_OPTS $SCP_OPTS \
	"$BIN" \
	"$REPO_ROOT/deploy/xray-panel.init" \
	"$REPO_ROOT/deploy/panel.example.yaml" \
	"$REPO_ROOT/deploy/xray-panel-backup" \
	"$TARGET:/tmp/"

echo ">>> installing on $TARGET"
# shellcheck disable=SC2087,SC2086
ssh $SSH_OPTS "$TARGET" /bin/sh <<'REMOTE'
set -eu

# OpenWrt's busybox lacks coreutils' `install`, so use cp + chmod.
cp /tmp/xray-panel      /usr/bin/xray-panel
chmod 0755              /usr/bin/xray-panel
cp /tmp/xray-panel.init /etc/init.d/xray-panel
chmod 0755              /etc/init.d/xray-panel

mkdir -p /etc/xray-panel
if [ ! -f /etc/xray-panel/panel.yaml ]; then
	cp /tmp/panel.example.yaml /etc/xray-panel/panel.yaml
	chmod 0600                 /etc/xray-panel/panel.yaml
	echo ">>> wrote /etc/xray-panel/panel.yaml from example — edit listen, server_address, auth.password_bcrypt before first start"
	FIRST_INSTALL=1
else
	echo ">>> /etc/xray-panel/panel.yaml already present, leaving untouched"
	FIRST_INSTALL=0
fi

# Backup tool: refreshes the script + ensures the cron entry exists.
# Backups go to /mnt/sda1/xray-backup and are content-hash gated, so
# running the cron daily is a no-op when nothing changed.
cp /tmp/xray-panel-backup /usr/sbin/xray-panel-backup
chmod 0755                /usr/sbin/xray-panel-backup
mkdir -p /mnt/sda1/xray-backup
CRON_LINE='30 3 * * * /usr/sbin/xray-panel-backup'
CRON_FILE=/etc/crontabs/root
touch "$CRON_FILE"
if ! grep -Fxq "$CRON_LINE" "$CRON_FILE"; then
	echo "$CRON_LINE" >> "$CRON_FILE"
	echo ">>> added daily backup to $CRON_FILE"
	/etc/init.d/cron enable  >/dev/null 2>&1 || true
	/etc/init.d/cron restart >/dev/null 2>&1 || true
else
	echo ">>> backup cron entry already present"
fi

rm -f /tmp/xray-panel /tmp/xray-panel.init /tmp/panel.example.yaml /tmp/xray-panel-backup

/etc/init.d/xray-panel enable

if [ "$FIRST_INSTALL" = "1" ]; then
	echo ">>> not starting: edit /etc/xray-panel/panel.yaml first, then run '/etc/init.d/xray-panel start'"
else
	/etc/init.d/xray-panel restart
	echo ">>> xray-panel restarted"
fi
REMOTE

echo ">>> done"
