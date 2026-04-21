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
scp $SSH_OPTS \
	"$BIN" \
	"$REPO_ROOT/deploy/xray-panel.init" \
	"$REPO_ROOT/deploy/panel.example.yaml" \
	"$TARGET:/tmp/"

echo ">>> installing on $TARGET"
# shellcheck disable=SC2087,SC2086
ssh $SSH_OPTS "$TARGET" /bin/sh <<'REMOTE'
set -eu

install -m 0755 /tmp/xray-panel     /usr/bin/xray-panel
install -m 0755 /tmp/xray-panel.init /etc/init.d/xray-panel

mkdir -p /etc/xray-panel
if [ ! -f /etc/xray-panel/panel.yaml ]; then
	install -m 0600 /tmp/panel.example.yaml /etc/xray-panel/panel.yaml
	echo ">>> wrote /etc/xray-panel/panel.yaml from example — edit listen, server_address, auth.password_bcrypt before first start"
	FIRST_INSTALL=1
else
	echo ">>> /etc/xray-panel/panel.yaml already present, leaving untouched"
	FIRST_INSTALL=0
fi

rm -f /tmp/xray-panel /tmp/xray-panel.init /tmp/panel.example.yaml

/etc/init.d/xray-panel enable

if [ "$FIRST_INSTALL" = "1" ]; then
	echo ">>> not starting: edit /etc/xray-panel/panel.yaml first, then run '/etc/init.d/xray-panel start'"
else
	/etc/init.d/xray-panel restart
	echo ">>> xray-panel restarted"
fi
REMOTE

echo ">>> done"
