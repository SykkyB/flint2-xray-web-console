# Перенос USB-SSD с flint2 на ryzen4700 — анализ и план

Дата инвентаризации: 2026-09-07. Статус: **план согласован (решения в §6), ждём кабели для окна Фазы 1**.

## 1. Что переносим

| | |
|---|---|
| Диск | WD Blue 3D NAND SATA 2.5" 1 TB (`WDS100T3B0A`) в боксе Realtek **RTL9201** (USB 3.2 Gen 2 → SATA) |
| ФС | ext4, label `usbssd`, UUID `cc7fc234-9d72-4c18-bd05-473289982a5a` |
| Объём | 930 GB, занято 226 GB |
| На flint2 | смонтирован дважды: `/mnt/sda1` (uci fstab по UUID) и `/tmp/mountd/disk1_part1` (GL mountd, туда смотрят samba-шары) |
| USB-линк | **480 Mbit/s (USB 2.0)**, SuperSpeed никогда не поднимался; 4 USB-ресета за лето |

### 1.1 Что лежит на диске и кто туда пишет

| Каталог | Размер | Кто пишет | Как | Судьба |
|---|---|---|---|---|
| `immich-backup/` | **205 GB** | ryzen `rsnapshot` (root cron 04:30 daily / пн 06:00 weekly / 1-го 07:30 monthly) | CIFS `//192.168.100.1/immich-backup` → `/backup` | **остаётся на SSD**, становится локальным `/backup` |
| `immich-backup/restic-media-srv/` | 1 GB | ryzen `/opt/media-srv/scripts/backup.sh` (root cron 23:00 UTC) | тот же CIFS, путь `/backup/restic-media-srv` | остаётся, путь не меняется |
| `downloads_ptp/` | 21.3 GB | transmission на flint2 (download_dir), minidlna (media_dir), samba-шара `downloads_ptp` (guest, ro) | локально на роутере | → **`/mnt/media/tv/`** для Jellyfin (решение 2026-09-07), потом удалить с SSD |
| `downloads_ptp_incomplete/` | 0 | transmission | | удалить |
| `xray-backup/` | 201 MB | flint2 `/usr/sbin/xray-panel-backup` (cron 03:30) + `deploy/install.sh` (снапшот перед деплоем); R2-копия есть (шифр., 180 д) | локально | → флеш flint2 `/root/backup/xray/` |
| `system-backup/` | 1.1 MB | flint2 `/usr/sbin/system-config-backup` (cron 04:30 local): снапшоты 30 д, `state.sha256`, лог; R2-копия 180 д | локально | → флеш `/root/backup/system/` |
| `watchdog/` | 40 KB | внешний watchdog Layer 2 (`watchdog.sh` + `config.env` + state + лог), cron каждую минуту | локально | → флеш `/etc/watchdog/` (закрывает слепую зону: сейчас умирает вместе с диском) |
| `watchdog-backup/` | 16 KB | разовый снапшот 2026-05-04 | | → `flint2-legacy/` |
| `rclone` | 69 MB | бинарь, которым пользуются оба flint2-бэкапа | | → `/usr/bin/rclone` |
| `butler-audit/` | 1.3 MB (95 файлов) | ryzen `~/butler-home-ai/ship-audit.sh` (cron 04:00) по ssh ключом butler; R2-копия есть | `ssh root@192.168.100.1 "cat > /mnt/sda1/butler-audit/…"` | → флеш `/root/offhost/butler-audit/` |
| `butler-secrets/` | 8 KB | `~/butler-home-ai/backup-secrets.sh` (вс 04:30), шифр.; R2-копия | так же | → `/root/offhost/butler-secrets/` |
| `domovoy-backup/` | 2 MB | `~/domovoy/backup.sh` (вс 04:45), шифр.; R2-копия | так же | → `/root/offhost/domovoy-backup/` |
| `flint2_etc_full_backup_27.03.2026.tar.gz` | 3.3 MB | разовый | | → `flint2-legacy/` |
| `.minidlna-db/`, `.gl_nas_sys_dir/` | 96 KB | minidlna, GL NAS | | удалить вместе с сервисами |

Свободного места на флеше flint2: **6.7 GB из 7.2 GB** (overlay). Всё, что остаётся на роутере, суммарно ~280 MB — влезает.

### 1.2 Все места, где зашит старый путь

**flint2:** `/etc/config/fstab` (uci mount по UUID), `/etc/config/samba4` (3 шары: `downloads_ptp`, `disk1_part1`, `immich-backup`), `/etc/config/minidlna`, `/etc/config/transmission`, `/etc/config/gl_nas/{disk,file_share}.db` (GL UI «Network Storage»), `/etc/crontabs/root` (строки `watchdog.sh` и `disk-watchdog`), `/usr/sbin/disk-watchdog` (весь смысл пропадает), `/usr/sbin/system-config-backup` (`BACKUP_ROOT`, `RCLONE`, две строки в `FLINT_PATHS`), `/usr/sbin/xray-panel-backup` (`BACKUP_ROOT`, `RCLONE`), `/root/HOME-INFRA.md`.

**ryzen:** `/etc/fstab` (CIFS-строка), `/root/.smbcred`, `/usr/local/bin/rsnapshot-safe` (проверка `findmnt -t cifs`), `/etc/rsnapshot.conf` (только опциональный тюнинг), `/opt/media-srv/scripts/backup.sh` (проверка `mountpoint -q /backup` — работает и с локальным диском), `~/butler-home-ai/ship-audit.sh`, `~/butler-home-ai/backup-secrets.sh`, `~/butler-home-ai/prompts/system_prompt.md`, `~/domovoy/backup.sh`, `~/watchdog/watchdog.sh` (добавить проверку маунта), `/usr/local/sbin/media-mount-guard` (расширить на `/backup`).

**Репо на Маке:** `butler-home-ai/{ship-audit.sh,backup-secrets.sh,prompts/system_prompt.md}`, `ryzen4700-homesrv/domovoy/backup.sh`, `ryzen4700-homesrv/media-srv/scripts/{backup,restore,restic-init}.sh` (комментарии про CIFS), `flint2-xray-web-console/deploy/install.sh` (каталог снапшотов), `flint2-xray-web-console/HOME-INFRA.md` (37 упоминаний), заметки в памяти Claude.

**beryl:** зависимости от диска нет (бэкапится напрямую в R2). **Mac:** SMB-маунтов сейчас нет.

## 2. Железо на ryzen

- Мини-ПК на Ryzen 7 4700U, **SATA-контроллера нет** (только M.2 NVMe, где стоит система). Значит SSD остаётся в USB-боксе.
- Два xHCI-контроллера AMD, у каждого 2 порта с SuperSpeed (10 Gbit/s) и 2 только USB 2.0. Итого **4 физических порта USB 3.x**. Один порт USB 2.0 занят внутренним Bluetooth.
- Медиа-HDD WD My Passport (`/mnt/media`) сидит на SS-способном порту, но **тоже на 480 Mbit/s** с дескриптором USB 2.10 — та же болезнь, что у SSD на flint2. Почти наверняка кабель USB 2.0 (micro-B 2.0 физически входит в гнездо micro-B 3.0). Заодно заменить.
- Autosuspend для HDD выключен (`power/control = on`), USB-ресетов HDD за 30 дней в журнале нет.

## 3. Целевая схема

- SSD в SS-порт ryzen новым кабелем USB-A → USB-C (10 Gbit/s, ≤0.5 м).
- Диск монтируется по UUID в **`/mnt/backup-ssd`** как ext4 (`nofail,x-systemd.device-timeout=10`), **структура каталогов на диске сохраняется как есть** (решение 2026-09-07).
- `/backup` становится **bind-маунтом** на `/mnt/backup-ssd/immich-backup` (вторая строка в fstab с `x-systemd.requires-mounts-for=/mnt/backup-ssd`). Тогда `/backup/daily.N`, `/backup/weekly.N`, `/backup/restic-media-srv` — ровно те же пути, что сегодня: rsnapshot, restic, watchdog и вся документация не трогаются. Каталог `/backup` на корне остаётся `chattr +i`: если диск не смонтирован, bind не поднимется, а писать в голый каталог нельзя.
- Цена по сравнению с выравниванием корня: одна дополнительная строка в fstab и то, что одни и те же данные видны по двум путям (`df` покажет оба). Умонтировать надо в обратном порядке: сначала `/backup`, потом `/mnt/backup-ssd`.
- Отвергнутый вариант: менять `snapshot_root`, `RESTIC_REPOSITORY` и проверки на `/mnt/backup-ssd/immich-backup/…` — четыре файла на ryzen плюс все доки ради того же результата.
- flint2 **оставляет себе**: внешний watchdog (флеш), `system-config-backup` и `xray-panel-backup` (локальные снапшоты на флеше, долговременно в R2), off-host копии butler/domovoy (флеш). flint2 **теряет**: samba, minidlna, transmission, disk-watchdog, GL «Network Storage».
- SMB для Мака переезжает на ryzen (Фаза 2b): шары на папки медиа-стека (`/mnt/media/downloads` rw, `/mnt/media` ro). DLNA не нужен (заменён Jellyfin/Jellyseerr).
- Пробел, который надо помнить: фото Immich и их бэкап окажутся на одном хосте (на разных дисках). Off-site копии фоток нет и сейчас. Следующий шаг после переезда: restic → R2 (~190 GB ≈ $3/мес).

## 4. Последовательность

### Фаза 0 — подготовка (без простоя, можно растянуть на несколько дней)

1. Купить кабель. Найти SS-порт на ryzen: воткнуть любую USB 3 флешку/бокс и смотреть `lsusb -t` — устройство должно оказаться на Bus 002 или 004 с 5000M/10000M.
2. Принять решения из §6.
3. flint2, перенос мелочей на флеш (диск ещё на месте, всё копируется, не переносится):
   ```sh
   mkdir -p /etc/watchdog /root/backup/system /root/backup/xray /root/offhost
   cp -a /mnt/sda1/watchdog/watchdog.sh /mnt/sda1/watchdog/config.env /etc/watchdog/
   chmod 700 /etc/watchdog/watchdog.sh; chmod 600 /etc/watchdog/config.env
   # в /etc/watchdog/watchdog.sh: BASE_DIR=/etc/watchdog, STATE_DIR=/tmp/watchdog-state, LOG=/etc/watchdog/watchdog.log
   # cron: /mnt/sda1/watchdog/watchdog.sh -> /etc/watchdog/watchdog.sh
   echo /etc/watchdog/ >> /etc/sysupgrade.conf
   cp /mnt/sda1/rclone /usr/bin/rclone && chmod 755 /usr/bin/rclone
   cp -a /mnt/sda1/system-backup/. /root/backup/system/
   cp -a /mnt/sda1/xray-backup/. /root/backup/xray/
   cp -a /mnt/sda1/butler-audit /mnt/sda1/butler-secrets /mnt/sda1/domovoy-backup /root/offhost/
   ```
   Затем в `system-config-backup`: `BACKUP_ROOT=/root/backup/system`, `RCLONE=/usr/bin/rclone`, в `FLINT_PATHS` заменить `/mnt/sda1/watchdog/…` на `/etc/watchdog/…`. В `xray-panel-backup`: `BACKUP_ROOT=/root/backup/xray`, `RCLONE=/usr/bin/rclone`. Проверить: `system-config-backup --force`, `xray-panel-backup`, глянуть R2.
4. ryzen, три скрипта-отправителя: дефолты `FLINT2_DEST`/`SECRETS_DEST`/`DEST` → `/root/offhost/butler-audit`, `/root/offhost/butler-secrets`, `/root/offhost/domovoy-backup`. Закоммитить в butler-home-ai и domovoy, задеплоить, прогнать вручную `ship-audit.sh`.
5. Репо: `flint2-xray-web-console/deploy/install.sh` — каталог снапшотов `/root/backup/xray/snapshots`. `system_prompt.md` butler — новые пути.
6. ryzen, подготовить (не активировать): новая строка fstab, новый `rsnapshot-safe`, расширение `media-mount-guard`, проверка `mount_backup` в `~/watchdog/watchdog.sh`. Тексты ниже.

### Фаза 1 — переключение (~30–40 минут; Immich, VPN, медиа не задеты, только бэкапы)

Окно: не пересекать 04:30 и 23:00 UTC (rsnapshot, restic). Удобно 10:00–20:00 UTC.

1. ryzen (root): `systemctl stop backup.automount backup.mount`; закомментировать CIFS-строку в `/etc/fstab`; `systemctl daemon-reload`.
2. flint2: в GL UI Applications → Network Storage удалить все три шары (чтобы `file_share.db` не остался с мусором), затем:
   ```sh
   /etc/init.d/samba4 stop; /etc/init.d/samba4 disable
   /etc/init.d/minidlna stop; /etc/init.d/minidlna disable
   /etc/init.d/transmission stop; /etc/init.d/transmission disable
   sed -i '/disk-watchdog/d' /etc/crontabs/root; /etc/init.d/cron restart
   uci delete fstab.@mount[0]; uci commit fstab
   sync; umount /tmp/mountd/disk1_part1; umount /mnt/sda1
   ```
   Проверить, что uci samba4 больше не содержит `sambashare` секций (если GL UI их не убрал — `uci delete samba4.@sambashare[N]` три раза, `uci commit samba4`).
3. Физически: бокс + новый кабель в SS-порт ryzen. `lsusb -t` → 5000M; `lsblk` → новый `sdb1`.
4. ryzen (root), проверка ФС (диск не смонтирован, структура не меняется):
   ```sh
   e2fsck -f /dev/disk/by-uuid/cc7fc234-9d72-4c18-bd05-473289982a5a
   mkdir -p /mnt/backup-ssd
   ```
5. ryzen, fstab (две строки) и маунт:
   ```
   UUID=cc7fc234-9d72-4c18-bd05-473289982a5a  /mnt/backup-ssd  ext4  defaults,nofail,x-systemd.device-timeout=10  0  2
   /mnt/backup-ssd/immich-backup  /backup  none  bind,nofail,x-systemd.requires-mounts-for=/mnt/backup-ssd  0  0
   ```
   `systemctl daemon-reload && mount /mnt/backup-ssd && mount /backup`, затем проверить:
   `findmnt -o TARGET,SOURCE,FSTYPE,UUID /mnt/backup-ssd /backup` (у `/backup` SOURCE вида `/dev/sdX1[/immich-backup]`) и `ls /backup/daily.0 | head`.
6. Установить новый `rsnapshot-safe` (две проверки вместо CIFS: диск по UUID и bind на нужный подкаталог):
   ```sh
   SSD_UUID=cc7fc234-9d72-4c18-bd05-473289982a5a
   if [ "$(findmnt -n -o UUID -M /mnt/backup-ssd 2>/dev/null)" != "$SSD_UUID" ] \
      || ! findmnt -n -o SOURCE -M /backup 2>/dev/null | grep -q '\[/immich-backup\]'; then
       logger -t rsnapshot-safe "ABORT: /backup is not the backup SSD, backup skipped"
       echo "ERROR: /backup is not the backup SSD. Backup aborted." | mail -s "BACKUP FAILED: SSD not mounted" alexandr.rachok@gmail.com
       exit 1
   fi
   ```
7. Прогон: `rsnapshot-safe daily` (локальный `cp -al` на 31 тыс. файлов — секунды, rsync ~3 мин) и `restic -r /backup/restic-media-srv snapshots` с паролем из `/root/.restic-media-srv.pass`. Ожидаем `completed successfully` в `/var/log/rsnapshot.log`.
8. Защита от boot-race (на ryzen USB-диски уже не монтировались при буте, см. `/mnt/media` 2026-07-28): в `media-mount-guard` добавить `mountpoint -q /backup || systemctl start backup.mount` (systemd сам поднимет `mnt-backup\x2dssd.mount` через `requires-mounts-for`); в `~/watchdog/watchdog.sh` добавить `check "mount_backup" "mountpoint -q /backup && findmnt -n -o UUID -M /mnt/backup-ssd | grep -q cc7fc234"`.
9. Удалить `/root/.smbcred`. Пакет `cifs-utils` можно оставить.
10. Сериал из `downloads_ptp` → Jellyfin (локальное копирование 21 GB, минуты):
    ```sh
    ls -ln /mnt/media/tv | head -3     # запомнить uid:gid, которыми владеют каталоги медиа-стека
    rsync -a --info=progress2 "/mnt/backup-ssd/downloads_ptp/The.Dark.Crystal.Age.Of.Resistance.S01.1080p.TVShows/" \
          "/mnt/media/tv/The Dark Crystal - Age of Resistance/Season 01/"
    chown -R <uid>:<gid> "/mnt/media/tv/The Dark Crystal - Age of Resistance"
    ```
    В Jellyfin — обновить библиотеку. Если нужно, чтобы сериал вёл Sonarr: добавить сериал в Sonarr → Manual Import из этой папки. После проверки воспроизведения: `rm -rf /mnt/backup-ssd/downloads_ptp /mnt/backup-ssd/downloads_ptp_incomplete`.
11. Порядок на диске (по желанию, после того как копии на флеше flint2 проверены): `cd /mnt/backup-ssd && mkdir flint2-legacy && mv xray-backup system-backup watchdog watchdog-backup rclone flint2_etc_full_backup_27.03.2026.tar.gz butler-audit butler-secrets domovoy-backup .minidlna-db .gl_nas_sys_dir flint2-legacy/`. `immich-backup/` не трогать.

### Фаза 2 — зачистка и документация

- flint2: `opkg remove transmission-daemon minidlna` (по желанию), unix-юзеры `sambausr`/`immichbckp` больше не нужны, `/usr/sbin/disk-watchdog` убрать (в git `deploy/disk-watchdog` пометить как legacy), `/etc/rc.local` — убрать `mkdir -p /var/lib/samba/...`.

### Фаза 2b — SMB для Мака на ryzen (отдельная задача, не блокирует переезд)

Нужны только папки медиа-стека, DLNA не нужен. Набросок:
- `apt install samba` на хосте (проще и надёжнее контейнера; Samba 4.19 в Ubuntu 24.04 дружит с macOS).
- `/etc/samba/smb.conf`: `[global]` с `server min protocol = SMB2`, `vfs objects = fruit streams_xattr`, `fruit:metadata = stream`, `fruit:model = MacSamba`, `interfaces = 192.168.100.5`, `bind interfaces only = yes`; шары `[downloads]` → `/mnt/media/downloads` (rw, `valid users = sykkyb`, `force user`/`force group` = uid:gid медиа-стека, чтобы qBittorrent и *arr видели права как свои) и `[media]` → `/mnt/media` (ro).
- `smbpasswd -a sykkyb`; на Маке `smb://192.168.100.5/downloads`.
- Если шара на `downloads` пишется с Мака, помнить про hardlink-схему *arr: класть файлы в подпапки, которые не разгребает Sonarr/Radarr автоматически.
- Внести в `~/watchdog/watchdog.sh` проверку `smbd` только если шарой пользуются постоянно.
- Обновить `HOME-INFRA.md` (Layer 2, Layer 2b, Pipeline 1/4/5/6/7, §5.1 п. с восстановлением watchdog, §5.2, §5.6, §6.3) и синхронизировать на `flint2:/root/`.
- Заметки памяти Claude: immich-backup-rsnapshot, flint2-usb-disk-reset, watchdog-setup, deployment-targets-flint2 (путь снапшотов), system-config-backup-script.
- Решить судьбу `flint2-legacy/` на диске (после проверки, что всё перенесено, — удалить).

### Фаза 3 — контроль на следующее утро

- `rsnapshot.log`: daily в 04:30 UTC завершился `completed successfully`; `daily.0` на диске.
- restic в 23:00 UTC: `/var/log/media-srv-backup.log` без ошибок.
- `system-config-backup` и `xray-panel-backup` на flint2 отработали в новые каталоги и залили в R2.
- Внешний watchdog на flint2 живёт с флеша: `/etc/watchdog/watchdog.log` обновляется на событиях, cron-строка на месте.
- В Telegram нет новых DOWN от `backup_disk`/`samba_shares` (их источник, disk-watchdog, снят с cron).

## 5. Откат

Диск остаётся ext4 с той же структурой, ничего не переименовано. Вернуть на flint2: воткнуть, восстановить uci `fstab`/`samba4` из снапшота `system-config-backup` (он бэкапит `/etc/config` ежедневно), шара `immich-backup` снова смотрит в `…/disk1_part1/immich-backup`; на ryzen вернуть CIFS-строку fstab, убрать две новые строки и поставить старый `rsnapshot-safe.bak-2026-09-07`.

## 6. Решения владельца (2026-09-07)

1. **`downloads_ptp`:** перенести в `/mnt/media/tv` для Jellyfin (Фаза 1, шаг 10), потом удалить с SSD.
2. **SMB/DLNA:** SMB к паре папок медиа-стека нужен → samba на ryzen (Фаза 2b). DLNA не нужен, заменён Jellyfin/Jellyseerr.
3. **Схема маунта:** сохранить структуру диска → `/mnt/backup-ssd` + bind `/backup` (§3).
4. **Окно Фазы 1:** после покупки новых кабелей (нужны два: USB-A→USB-C для SSD и micro-B 3.0 для HDD `/mnt/media`).
5. **Off-site для фоток:** решить позже, отдельно.

## 7. Риски

- **Boot-race USB на ryzen** — закрыт `nofail` + guard + `chattr +i` + проверка UUID/bind в `rsnapshot-safe` + `no_create_root 1` у rsnapshot. Bind-маунт без диска не поднимается сам (нет исходного каталога), а голый `/backup` неизменяемый — записать на корень нечего.
- **Данные и бэкап на одном хосте** — разные диски, но общий корпус/ОС; off-site закрывается restic → R2.
- **USB-ресеты** — новый кабель и SS-порт; если повторятся на ryzen, смотреть в сторону хаба с внешним питанием и `usbcore.quirks`.
- **sysupgrade flint2** стирает `/root` и `/usr/bin`: локальные снапшоты и `rclone` восстанавливаются из R2 и по §6.3 HOME-INFRA; `/etc/watchdog/` защищён строкой в `/etc/sysupgrade.conf`.
- **Root на ryzen:** шаги Фазы 1 п. 1, 4–9 требуют sudo — выполняет владелец, Claude готовит файлы и команды. Все шаги на flint2 Claude может выполнить сам.

## 8. 2026-09-28 — замена кабелей и тест бокса на ryzen

- **HDD `/mnt/media`:** кабель micro-B 3.0 заменён (ryzen штатно выключался), линк 5000M на Bus 002 Port 1. Эта часть закрыта.
- **SSD, кабель A→C:** исправен — на ryzen бокс садится на 5000M (usb 3.20, драйвер `uas`). Но на MT6000 бокс RTL9201 и с новым кабелем встаёт на **480M (usb 2.10)** — SuperSpeed на роутере не поднимается ни с одним кабелем, дело в связке порт роутера + бокс. При первом подключении на роутере не энумерировался вовсе при горящем LED (посадка/ориентация USB-C), помогло переподключение.
- **Тест чтения на ryzen через uas** (`docker run --rm --device=/dev/sdb:/dev/sdb:r alpine dd … iflag=direct`): 0 ГБ 199 МБ/с, 97 ГБ 50, 195 ГБ 68, 390 ГБ 82, 781 ГБ (не занято) 360. Буферизованное чтение @300 ГБ: 8 команд не вернулись за 40 с → `uas_eh_abort_handler … inflight: CMD IN`, `Device offlined - not ready after error recovery`. Вернуть диск помог только физический replug.
- **SMART SSD** (`docker run --rm --privileged --device=/dev/sdb smartmon smartctl -d sat …`, работает поверх uas): health PASSED, short и extended self-test (10 мин) **без ошибок**; Reallocated 4, Pending 4, Offline_Uncorrectable 2, Reallocated_Event 2, Power-Off_Retract 24 при 56 включениях, CRC 0, 5216 ч. ext4 clean, FS Error count 0.
- **Вывод:** NAND исправен, виснет мост RTL9201 в режиме UAS под длинным чтением на 5 Gbit (на OpenWrt бокс идёт через usb-storage, там этой болячки нет). Летние USB-ресеты на flint2 кабелем не объясняются.
- **Перед Фазой 1 на ryzen (sudo):** `/etc/modprobe.d/rtl9201-no-uas.conf` → `options usb-storage quirks=0bda:9201:u`, затем `update-initramfs -u`. Рантайм-проверка без ребута: `echo 0bda:9201:u > /sys/module/usb_storage/parameters/quirks`, `echo 2-2 > /sys/bus/usb/drivers/usb/unbind`, `… /bind`, драйвер интерфейса должен стать `usb-storage`. После этого повторить тест чтения на тех же смещениях плюс 4 ГБ на 300 ГБ. Если виснет и через usb-storage — менять **бокс** (ASMedia ASM235CM/ASM1153E), SSD оставить. Ожидаемая цена квирка: 10–15 % пиковой последовательной скорости, на rsnapshot не заметно.
- **Порядок следующего захода** (SSD снова на flint2, Фаза 0 всё ещё не сделана): квирк + тест → Фаза 1 п.3–5 (маунт `/mnt/backup-ssd` + bind `/backup`) → Фаза 0 п.3–5, копируя мелочь с `/mnt/backup-ssd` на флеш flint2 через ssh-пайп (`ssh ryzen4700 cat … | ssh flint2 'cat > …'`, sftp на flint2 нет) → остальное Фазы 1 → Фаза 2.
- **Ограничение auto-mode:** классификатор не дал Claude остановить samba4/minidlna/transmission и размонтировать диск на flint2 («Interfere With Workloads»); старт сервисов, `mount`, правка cron прошли. Шаги «остановить/размонтировать» на flint2 вводит владелец.
- **После replug на flint2** uci-маунт `/mnt/sda1` не поднялся сам (только GL `/tmp/mountd/disk1_part1`, ext4 без recovery). Лечение: `mount -o rw,noatime /dev/sda1 /mnt/sda1` или дождаться disk-watchdog (если он в cron).
- Состояние на конец дня: SSD на flint2 новым кабелем (480M), samba/minidlna/transmission и disk-watchdog восстановлены, CIFS `/backup` на ryzen проверен, все 7 контейнеров с `/mnt/media` healthy.

## 9. 2026-10-10 — третий кабель: SuperSpeed на flint2 поднялся, диск на usb-storage

- **Диагностика до замены:** debugfs xHCI (`/sys/kernel/debug/usb/xhci/11200000.xhci/ports/port01/portsc`) показывал SS-порт в `Link:RxDetect Not-connected` при воткнутом боксе, даже при повторной энумерации не доходил до Polling → SS-пары электрически не доходили. Софт исключён: GL-опция `glconfig.general.usb3_disable` пуста, `usb2-port1/disable`=0, u3-phy в DT включён. Пробуждение SS-хаба из runtime-suspend не помогло.
- **Новый кабель A→C:** бокс сразу встал как `2-1`, **5000M, usb 3.20**, SS-порт `U0 PortSpeed:4`. Кабель от 28.09 (5000M на ryzen) на MT6000 SS не давал — порт роутера, видимо, чувствительнее к кабелю, чем десктопный xHCI.
- **UAS:** на SS ядро взяло `uas` и сразу написало `xhci-mtk: ERROR Transfer event for unknown stream ring slot 1 ep 2`. Применён квирк `0bda:9201:u` → `usb-storage` (`Quirks match … 800000`), повторная энумерация чистая. usb-storage/uas вкомпилены в ядро GL → сделан `/etc/init.d/rtl9201-no-uas` (START=09, до fstab/gl_nas_diskmanager S40): ставит квирк в sysfs и перебиндивает интерфейс с uas на usb-storage. Проверен функционально (сброс квирка → GPIO-цикл → uas → скрипт → usb-storage). Добавлен в `/etc/sysupgrade.conf`, `system-config-backup` FLINT_PATHS, `restore-after-upgrade.sh` (только enable). После ребута проверить `readlink /sys/bus/usb/devices/2-1/2-1:1.0/driver` = usb-storage.
- **Тест чтения на flint2 через usb-storage @5 Гбит** (`dd bs=4M iflag=direct`): 1 ГиБ @0 за 6 с (~170 МБ/с), 512 МиБ @100 ГиБ за 10 с (~51 МБ/с — старые занятые области, как на ryzen). Ошибок xHCI/SCSI нет.
- **VBUS управляется:** `/sys/class/gpio/usb_power/value` (gpio-export в DT, GL сам дёргает его в `usb3_disable`). `echo 0; sleep 1; echo 1` = полноценный replug, диск переэнумерируется за ~2 с. Заметка §8/4-й случай про «только физически» устарела.
- **Что это меняет для миграции:** главный аргумент «flint2 даёт 480M и флапает» ослаб — на роутере теперь 5 Гбит и usb-storage (без UAS-болячки RTL9201). Решение переносить SSD на ryzen или оставить на flint2 — за владельцем; минимум стоит понаблюдать 2–4 недели, повторяются ли USB-ресеты на новом кабеле (все 5 были на старых). Если оставлять на flint2: Фаза 0 (копия мелочи на флеш) всё равно полезна. Если переносить: квирк для ryzen из §8 остаётся в силе.
- Состояние на конец окна: оба маунта на месте, samba/minidlna/transmission и cron disk-watchdog работают, CIFS `/backup` на ryzen смонтирован, сессия `immichbckp` активна. Монитор `/tmp/ss-mon.sh` (tmpfs) исчезнет при ребуте.
