#!/usr/bin/env bash
# Pull a full config snapshot from the Xiaomi Redmi Router AC2100 (Kwrt) that
# serves as a dumb AP / switch on the home LAN (192.168.100.2, behind flint2).
#
# Why pull-based from the Mac and not a router-side cron job like
# beryl-config-backup: this box is a LAN-only leaf device with no WAN, no rclone
# and no R2 credentials — and no reason to ever hold any. A snapshot taken
# before/after changes is enough, and nothing on it is reachable from outside.
#
# Output: ../xiaomi-backups/xiaomi-ac2100-<UTC>.tar.gz (+ .sha256).
# That directory sits OUTSIDE the repo on purpose: the snapshot carries the
# WireGuard private key, dropbear host keys and /etc/shadow. Same rule as
# flint2-backups/ and beryl's gitignored backups/ — secrets never reach git.
#
# Usage:
#   deploy/backup-xiaomi.sh [ssh-target]      # default: xiaomi
#
# Restore onto a freshly flashed Kwrt/OpenWrt of the same build:
#   scp -O ../xiaomi-backups/xiaomi-ac2100-<ts>.tar.gz xiaomi:/tmp/
#   ssh xiaomi 'tar xzf /tmp/xiaomi-ac2100-<ts>.tar.gz -C /'
#   ssh xiaomi 'reboot'                       # apply UCI
# Note: the Kwrt feeds for this build are 404 on dl.openwrt.ai (snapshot pruned),
# so packages cannot be reinstalled via opkg — restore onto the same image.

set -euo pipefail

TARGET="${1:-xiaomi}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$ROOT/../xiaomi-backups"
DEST_DIR="$(cd "$ROOT/../xiaomi-backups" && pwd)"

TS="$(date -u +%Y%m%d-%H%M%SZ)"
DEST="$DEST_DIR/xiaomi-ac2100-$TS.tar.gz"

# Paths captured, relative to /. Everything that a clean reflash would wipe
# and that we would not want to reconstruct by hand.
PATHS=(
    etc/config          # network (incl. wg0 private key), wireless, firewall,
                        # dhcp, dropbear, nginx, upnpd, passwall, system
    etc/dropbear        # ssh host keys + authorized_keys
    etc/passwd
    etc/shadow
    etc/group
    etc/crontabs
    etc/firewall.user
    etc/sysupgrade.conf
    etc/opkg
    root
)

collect() { ssh "$TARGET" "$1" 2>/dev/null || echo "?"; }

echo ">>> Backing up '$TARGET' -> $DEST"

R_HOST="$(collect 'cat /proc/sys/kernel/hostname')"
R_MODEL="$(collect 'cat /tmp/sysinfo/model')"
R_RELEASE="$(collect 'grep DISTRIB_DESCRIPTION /etc/openwrt_release | cut -d= -f2 | tr -d "'"'"'"')"
R_KERNEL="$(collect 'uname -srm')"
R_UPTIME="$(collect 'uptime')"
R_IP="$(collect 'ip -br addr')"
R_LINK="$(collect 'for p in wan lan1 lan2 lan3; do printf "%s=%s " "$p" "$(cat /sys/class/net/$p/speed 2>/dev/null || echo down)"; done; echo')"
R_LISTEN="$(collect 'netstat -lntu')"
R_UCI_SYSTEM="$(collect 'uci export system')"
R_KEEP="$(collect 'sysupgrade -l')"
R_SERVICES="$(collect 'ls /etc/rc.d/S* 2>/dev/null | sed "s#.*/##"')"
R_PKGS="$(collect 'opkg list-installed')"

GIT_REV="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"

LOCAL_MANIFEST="$(mktemp)"
trap 'rm -f "$LOCAL_MANIFEST"' EXIT
cat >"$LOCAL_MANIFEST" <<EOF
=== xiaomi (Redmi Router AC2100 / Kwrt) backup manifest ===
taken:    $TS (UTC)
from:     $TARGET
repo rev: $GIT_REV

host:     $R_HOST
model:    $R_MODEL
release:  $R_RELEASE
kernel:   $R_KERNEL
uptime:   $R_UPTIME

--- addresses ---
$R_IP

--- port link speed (Mb/s, "down" = no carrier) ---
$R_LINK

--- listening sockets ---
$R_LISTEN

--- uci export system ---
$R_UCI_SYSTEM

--- sysupgrade keep-list ---
$R_KEEP

--- enabled services ---
$R_SERVICES

--- installed packages ---
$R_PKGS
EOF

# No sftp-server on dropbear, so ship the manifest over a plain ssh pipe.
ssh "$TARGET" 'cat > /tmp/xiaomi-backup-manifest.txt' < "$LOCAL_MANIFEST"

# One -C only: busybox tar permutes options, so a second -C would silently
# re-root every preceding path. The manifest lives under /tmp, which is under /,
# so it rides along as tmp/xiaomi-backup-manifest.txt.
ssh "$TARGET" "tar czf - -C / ${PATHS[*]} tmp/xiaomi-backup-manifest.txt" > "$DEST"
ssh "$TARGET" 'rm -f /tmp/xiaomi-backup-manifest.txt'

shasum -a 256 "$DEST" > "$DEST.sha256"
echo ">>> $(ls -lh "$DEST" | awk '{print $5}')  $DEST"
echo ">>> sha256 $(cut -c1-16 < "$DEST.sha256")"
