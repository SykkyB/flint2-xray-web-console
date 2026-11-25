#!/usr/bin/env bash
# make-prefw.sh — полный снимок flint2 перед прошивкой («prefw tarball»).
# Собирается НА РОУТЕРЕ (busybox tar от root — без граблей с uid 501 от bsdtar):
# всё из `sysupgrade -l` + то, что в keep-list не входит, но нужно для разбора
# и ручного восстановления (geo-базы xray, .dat региона Wi-Fi, gl_home.html,
# init samba4/chronyd/transmission, nginx.conf, opkg status) + манифесты в
# prefw-work/manifest/ (crontab, uci export, списки пакетов, keep-list,
# iptables-save, rc.d, версии, md5 бинарей).
#
# Результат в трёх местах: USB роутера /mnt/sda1/flint2-prefw/ (plaintext),
# Mac $FLINT2_BACKUPS (plaintext, НЕ в git — внутри ключи), R2
# flint2-prefw/*.tar.gz.enc (AES-256, пароль /etc/system-backup.pass).
#
#   deploy/make-prefw.sh            # host = flint2
#   deploy/make-prefw.sh --no-r2    # без заливки в R2
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
DEST="${FLINT2_BACKUPS:-$(cd "$REPO/.." && pwd)/flint2-backups}"
HOST=flint2
R2=1
while [ $# -gt 0 ]; do
    case "$1" in
        --no-r2) R2=0 ;;
        --host) HOST="$2"; shift ;;
        *) echo "unknown arg: $1"; exit 2 ;;
    esac
    shift
done
[ -d "$DEST" ] || { echo "нет каталога $DEST (задай FLINT2_BACKUPS)"; exit 1; }

NAME=$(ssh "$HOST" 'sh -s' <<'REMOTE'
set -e
TS=$(date +%Y-%m-%d_%H%M)
D=/mnt/sda1/flint2-prefw
mkdir -p "$D"
OUT=$D/flint2-prefw-$TS.tar.gz
M=/prefw-work/manifest
rm -rf /prefw-work; mkdir -p $M
crontab -l > $M/crontab.txt
uci export > $M/uci-export.txt 2>/dev/null || true
opkg list-installed > $M/opkg-installed.txt
ls /overlay/upper/usr/lib/opkg/info/*.control 2>/dev/null | sed 's#.*/##; s#\.control##' > $M/opkg-user-installed.txt
sysupgrade -l > $M/keep-list.txt
iptables-save > $M/iptables-save.txt 2>/dev/null || true
netstat -lntup > $M/netstat.txt 2>/dev/null || true
ls /etc/rc.d > $M/rc.d.txt
{ cat /etc/glversion; cat /etc/openwrt_release; } > $M/glversion.txt
ps w > $M/ps.txt
md5sum /usr/bin/xray /usr/bin/xray-panel /www/gl_home.html /www/xray-panel-launcher.js > $M/md5.txt 2>/dev/null || true
L=/tmp/.prefw.list
# Список собираем без `set -e`: под ним группа в пайпе обрывалась после первой команды,
# и в архив попадал только keep-list (без манифестов, geo-баз и gl_home.html) — так вышло 23 и 25.11.2026.
set +e
{ cat $M/keep-list.txt
  find /usr/share/xray /etc/wireless/mediatek /etc/sysctl.d /etc/AdGuardHome -path /etc/AdGuardHome/data -prune -o -type f -print 2>/dev/null
  for f in /www/gl_home.html /www/gl_home.html.bak /www/index.html /etc/init.d/samba4 /etc/init.d/chronyd /etc/init.d/transmission /etc/nginx/nginx.conf /usr/lib/opkg/status /etc/modules.d/usb-storage; do [ -e $f ] && echo $f; done
  find /prefw-work -type f
} | sed 's#^/##' | sort -u > $L
cd / && tar -czf "$OUT" -T $L 2>/dev/null || true
rm -rf /prefw-work $L
cd "$D" && sha256sum "$(basename "$OUT")" > "${OUT%.tar.gz}.sha256"
for must in usr/bin/xray prefw-work/manifest/crontab.txt www/gl_home.html etc/wireless/mediatek/DBDC_card0.dat; do
    tar -tzf "$OUT" | grep -q -x "$must" || { echo "ERR: в архиве нет $must" >&2; exit 1; }
done
basename "${OUT%.tar.gz}"
REMOTE
)
echo "=== собран на роутере: $NAME"
ssh "$HOST" "cat /mnt/sda1/flint2-prefw/$NAME.tar.gz" > "$DEST/$NAME.tar.gz"
ssh "$HOST" "cat /mnt/sda1/flint2-prefw/$NAME.sha256" > "$DEST/$NAME.sha256"
chmod 600 "$DEST/$NAME.tar.gz"
(cd "$DEST" && shasum -a 256 -c "$NAME.sha256")
echo "=== Mac: $DEST/$NAME.tar.gz ($(wc -c < "$DEST/$NAME.tar.gz" | tr -d ' ') байт, $(tar -tzf "$DEST/$NAME.tar.gz" | wc -l | tr -d ' ') записей)"
if [ "$R2" = 1 ]; then
    ssh "$HOST" "openssl enc -aes-256-cbc -pbkdf2 -salt -pass file:/etc/system-backup.pass -in /mnt/sda1/flint2-prefw/$NAME.tar.gz -out /tmp/$NAME.tar.gz.enc && /mnt/sda1/rclone --config /etc/rclone/rclone.conf copy /tmp/$NAME.tar.gz.enc r2:sys-lab-home-backups/flint2-prefw/; rm -f /tmp/$NAME.tar.gz.enc; /mnt/sda1/rclone --config /etc/rclone/rclone.conf lsf r2:sys-lab-home-backups/flint2-prefw/ | grep $NAME"
fi
