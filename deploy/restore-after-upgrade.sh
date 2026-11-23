#!/usr/bin/env bash
# restore-after-upgrade.sh — восстановление кастома flint2 (GL-MT6000) после
# sysupgrade GL.iNet. Идемпотентен: доливает только то, чего на роутере нет,
# ничего существующего не перезаписывает, /etc/config и gl_home.html не трогает.
#
# Источник правды — этот файл в репо flint2-xray-web-console (deploy/). В
# flint2-backups/ лежит только обёртка, которая вызывает его.
#
# Запуск с Mac (из корня репо):
#   deploy/restore-after-upgrade.sh --dry-run       # показать, что будет сделано
#   deploy/restore-after-upgrade.sh                 # выполнить
#   deploy/restore-after-upgrade.sh --no-opkg       # без opkg (нет доступа к репо GL)
#   deploy/restore-after-upgrade.sh --tarball FILE  # явный prefw tarball
#
# prefw-tarball (секреты, НЕ в git) ищется в $FLINT2_BACKUPS, по умолчанию
# ../flint2-backups рядом с репо. Собрать свежий: deploy/make-prefw.sh.
#
# После него: deploy/install.sh flint2 (пере-патч gl_home.html кнопкой панели).
# См. HOME-INFRA.md §5.1d и §5.1h.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
HERE="${FLINT2_BACKUPS:-$(cd "$REPO/.." && pwd)/flint2-backups}"
HOST=flint2
DRY=0
OPKG=1
TARBALL=""
PKGS="zoneinfo-asia transmission-daemon transmission-web htop nano"

while [ $# -gt 0 ]; do
    case "$1" in
        --dry-run) DRY=1 ;;
        --no-opkg) OPKG=0 ;;
        --tarball) TARBALL="$2"; shift ;;
        --host) HOST="$2"; shift ;;
        -h|--help) sed -n '2,14p' "$0"; exit 0 ;;
        *) echo "unknown arg: $1"; exit 2 ;;
    esac
    shift
done

# Файлы, которые доливаем из tarball, если их нет на роутере.
RESTORE_IF_MISSING="
usr/bin/xray
usr/bin/xray-panel
etc/init.d/xray
etc/init.d/xray-panel
etc/xray/config.json
etc/xray/config.json.bak
etc/xray/config.json.example
etc/xray-panel/panel.yaml
etc/xray-panel/disabled.json
etc/rclone/rclone.conf
etc/system-backup.pass
etc/disk-watchdog.env
etc/modules.d/99-port_forward
usr/sbin/system-config-backup
usr/sbin/xray-panel-backup
usr/sbin/xray-log-rotate
usr/sbin/wan-watchdog
usr/sbin/disk-watchdog
usr/sbin/port-forward-guard
etc/init.d/rtl9201-no-uas
usr/share/zoneinfo/Asia/Tbilisi
www/xray-panel-launcher.js
root/.ssh/id_ed25519
root/.ssh/id_ed25519.pub
root/.ssh/known_hosts
root/HOME-INFRA.md
"

# Пути, которые должны быть в /etc/sysupgrade.conf (чтобы следующий апгрейд сохранил их сам).
KEEP_PATHS="
/etc/xray/
/etc/xray-panel/
/etc/init.d/xray
/etc/init.d/xray-panel
/etc/init.d/rtl9201-no-uas
/etc/rc.d/S09rtl9201-no-uas
/usr/bin/xray
/usr/bin/xray-panel
/www/xray-panel-launcher.js
/etc/rclone/
/etc/system-backup.pass
/etc/disk-watchdog.env
/etc/modules.d/99-port_forward
/usr/sbin/system-config-backup
/usr/sbin/xray-panel-backup
/usr/sbin/xray-log-rotate
/usr/sbin/wan-watchdog
/usr/sbin/disk-watchdog
/usr/sbin/port-forward-guard
/usr/share/zoneinfo/Asia/Tbilisi
/root/.ssh/
/root/HOME-INFRA.md
/etc/firewall.user
"

say() { printf '\n=== %s\n' "$*"; }

# ---------- 0. tarball ----------
if [ -z "$TARBALL" ]; then
    TARBALL="$(ls -t "$HERE"/flint2-prefw-*.tar.gz 2>/dev/null | head -1 || true)"
fi
[ -n "$TARBALL" ] && [ -f "$TARBALL" ] || { echo "prefw tarball не найден (flint2-prefw-*.tar.gz в $HERE)"; exit 1; }
say "tarball: $TARBALL"
SUMFILE="${TARBALL%.tar.gz}.sha256"
if [ -f "$SUMFILE" ]; then
    (cd "$(dirname "$TARBALL")" && shasum -a 256 -c "$(basename "$SUMFILE")") || { echo "sha256 НЕ совпал, стоп"; exit 1; }
else
    echo "(файла .sha256 рядом нет, контроль пропущен)"
fi

# ---------- 1. ssh ----------
say "ssh $HOST"
if ! OUT=$(ssh -o BatchMode=yes -o ConnectTimeout=8 "$HOST" 'cat /etc/glversion 2>/dev/null; uptime' 2>&1); then
    echo "$OUT" | head -5
    if echo "$OUT" | grep -q 'REMOTE HOST IDENTIFICATION HAS CHANGED'; then
        echo "host-ключи сменились (чистая прошивка?). Выполни: ssh-keygen -R 192.168.100.1 && ssh-keygen -R flint2, затем повтори."
    fi
    exit 1
fi
echo "$OUT"

# ---------- 2. что отсутствует ----------
say "проверка отсутствующих файлов на роутере"
MISSING=$(ssh "$HOST" "for f in $(echo $RESTORE_IF_MISSING); do [ -e \"/\$f\" ] || echo \"\$f\"; done")
if [ -z "$MISSING" ]; then
    echo "все файлы на месте, доливать нечего"
else
    echo "$MISSING" | sed 's/^/  missing: /'
fi

TMP=$(mktemp -d)
trap 'rm -rf "${TMP:?}"' EXIT

# манифест crontab из tarball (для доливки строк)
tar -xzf "$TARBALL" -C "$TMP" prefw-work/manifest/crontab.txt 2>/dev/null || true
CRON_MANIFEST="$TMP/prefw-work/manifest/crontab.txt"
[ -f "$CRON_MANIFEST" ] || { echo "в tarball нет manifest/crontab.txt, cron-доливка пропускается"; : > "$CRON_MANIFEST"; }

if [ -n "$MISSING" ]; then
    # берём из tarball только те missing, которые в нём есть
    INTAR=$(tar -tzf "$TARBALL")
    TAKE=""
    for f in $MISSING; do
        if echo "$INTAR" | grep -qx "$f"; then TAKE="$TAKE $f"; else echo "  (в tarball нет: $f)"; fi
    done
    if [ -n "$TAKE" ]; then
        mkdir -p "$TMP/stage"
        # shellcheck disable=SC2086
        tar -xzf "$TARBALL" -C "$TMP/stage" $TAKE
        say "будет залито на роутер:"; (cd "$TMP/stage" && find . -type f | sed 's|^\./|  /|')
        if [ "$DRY" = 0 ]; then
            tar -czf - -C "$TMP/stage" . | ssh "$HOST" 'cat > /tmp/flint2-restore.tar.gz'
            ssh "$HOST" 'cd / && tar -xzpf /tmp/flint2-restore.tar.gz && rm -f /tmp/flint2-restore.tar.gz && echo "залито"'
        fi
    fi
fi

# ---------- 3. удалённая фаза ----------
say "удалённая фаза (DRY=$DRY OPKG=$OPKG)"
if [ "$DRY" = 0 ]; then
    ssh "$HOST" 'cat > /tmp/flint2-restore-crontab.txt' < "$CRON_MANIFEST"
else
    ssh "$HOST" 'cat > /tmp/flint2-restore-crontab.txt' < "$CRON_MANIFEST"   # только чтение при DRY, файл во временной памяти
fi
# shellcheck disable=SC2087
ssh "$HOST" "DRY=$DRY OPKG=$OPKG PKGS='$PKGS' KEEP_PATHS='$(echo $KEEP_PATHS)' sh -s" <<'REMOTE'
run() { echo "+ $*"; [ "$DRY" = 1 ] || eval "$*"; }
ex() { [ -e "$1" ]; }

echo "--- права"
for f in /usr/bin/xray /usr/bin/xray-panel /etc/init.d/xray /etc/init.d/xray-panel /usr/sbin/system-config-backup /usr/sbin/xray-panel-backup /usr/sbin/xray-log-rotate /usr/sbin/wan-watchdog /usr/sbin/disk-watchdog /usr/sbin/port-forward-guard /etc/init.d/rtl9201-no-uas; do ex "$f" && run chmod 755 "$f"; done
for f in /etc/xray-panel/panel.yaml /etc/system-backup.pass /etc/rclone/rclone.conf /etc/disk-watchdog.env /root/.ssh/id_ed25519 /root/.ssh/known_hosts; do ex "$f" && run chmod 600 "$f"; done
ex /root/.ssh && run chmod 700 /root/.ssh

echo "--- /etc/sysupgrade.conf"
for p in $KEEP_PATHS; do grep -qx "$p" /etc/sysupgrade.conf 2>/dev/null || run "echo '$p' >> /etc/sysupgrade.conf"; done

echo "--- каталоги логов на eMMC (не в keep-list; xray пишет access/error сюда, logd — syslog)"
run mkdir -p /opt/xray/log /opt/log
[ "$(uci -q get system.@system[0].log_file)" = /opt/log/syslog ] && run /etc/init.d/log restart

echo "--- samba runtime dirs + rc.local"
run mkdir -p /var/lib/samba/private /var/run/samba/msg.lock /var/lock /var/cache/samba
if ! grep -q 'mkdir -p /var/lib/samba/private' /etc/rc.local 2>/dev/null; then
    run "sed -i 's|^exit 0|mkdir -p /var/lib/samba/private /var/run/samba/msg.lock /var/lock /var/cache/samba\nexit 0|' /etc/rc.local"
fi

if [ "$OPKG" = 1 ]; then
    echo "--- opkg"
    NEED=""
    for pkg in $PKGS; do opkg list-installed 2>/dev/null | grep -q "^$pkg " || NEED="$NEED $pkg"; done
    if [ -n "$NEED" ]; then
        run opkg update
        for pkg in $NEED; do run opkg install "$pkg" || echo "  (не поставился: $pkg)"; done
    else
        echo "все пакеты уже стоят: $PKGS"
    fi
fi

echo "--- localtime для Go-бинарей"
ex /usr/share/zoneinfo/Asia/Tbilisi && run /etc/init.d/system restart

echo "--- cron"
if [ -s /tmp/flint2-restore-crontab.txt ]; then
    ADDED=0
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        case "$line" in \#*) continue ;; esac
        grep -qF -- "$line" /etc/crontabs/root 2>/dev/null || { run "echo '$line' >> /etc/crontabs/root"; ADDED=1; }
    done < /tmp/flint2-restore-crontab.txt
    [ "$ADDED" = 1 ] && run /etc/init.d/cron restart || echo "cron полный"
fi
rm -f /tmp/flint2-restore-crontab.txt

echo "--- сервисы"
for s in xray xray-panel samba4 minidlna transmission; do
    if [ -x "/etc/init.d/$s" ]; then
        /etc/init.d/$s enabled 2>/dev/null || run "/etc/init.d/$s enable"
        run "/etc/init.d/$s restart"
    else
        echo "  нет /etc/init.d/$s (пакет не стоит?)"
    fi
done
ex /etc/init.d/port_forward && run /etc/init.d/port_forward restart
ex /etc/init.d/cron && run /etc/init.d/cron restart

echo "--- rtl9201-no-uas (USB-SSD через usb-storage, не uas; START=09)"
if ex /etc/init.d/rtl9201-no-uas; then
    /etc/init.d/rtl9201-no-uas enabled 2>/dev/null || run /etc/init.d/rtl9201-no-uas enable
    # НЕ делать restart: если диск уже примонтирован через uas, перебинд выдернет его из-под маунта.
    if ls /sys/bus/usb/drivers/uas/ 2>/dev/null | grep -q ':'; then
        echo "  ВНИМАНИЕ: диск сейчас на uas — применится после ребута (или вручную: stop samba4/minidlna/transmission, umount /mnt/sda1 /tmp/mountd/disk1_part1, /etc/init.d/rtl9201-no-uas start)"
    else
        echo "  диск на usb-storage, ок"
    fi
fi

echo "--- smoke"
sleep 3
echo "glversion:      $(cat /etc/glversion 2>/dev/null)"
pgrep -f /usr/bin/xray >/dev/null && echo "xray:           up" || echo "xray:           DOWN"
netstat -ltn 2>/dev/null | grep -q ':9443 ' && echo "xray :9443:     listening" || echo "xray :9443:     NOT LISTENING"
echo "panel 9092:     HTTP $(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://192.168.100.1:9092/ 2>/dev/null) (ожидаем 401)"
netstat -ltn 2>/dev/null | grep -q ':3053 ' && echo "adguard :3053:  up" || echo "adguard :3053:  DOWN"
echo "wg peers:       $(uci show wireguard_server 2>/dev/null | grep -c =peers)"
echo "sda1 mounts:    $(mount | grep -c sda1) (ожидаем 2)"
echo "smbd:           $(ps | grep -c '[s]mbd')"
echo "cron lines:     $(crontab -l 2>/dev/null | wc -l)"
lsmod 2>/dev/null | grep -q port_forward && echo "kmod pf:        loaded" || echo "kmod pf:        NOT LOADED"
for f in /usr/share/zoneinfo/Asia/Tbilisi /etc/rclone/rclone.conf /etc/system-backup.pass /etc/disk-watchdog.env /www/xray-panel-launcher.js; do ex "$f" && echo "ok:             $f" || echo "MISSING:        $f"; done
grep -q xray-panel-launcher /www/gl_home.html 2>/dev/null && echo "gl_home patch:  present" || echo "gl_home patch:  ABSENT -> deploy/install.sh flint2"
echo "sysupgrade -l:  $(sysupgrade -l 2>/dev/null | grep -c xray) путей с xray (ожидаем >=5)"
REMOTE

say "готово. Дальше: cd $REPO && deploy/install.sh $HOST  (пере-патч gl_home.html), затем проверки UI из flint2-backups/UPGRADE-PLAN-4.11.0.md §6.4 и внешний тест 8443/9443 (HOME-INFRA §5.1h)."
