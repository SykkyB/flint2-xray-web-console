# Home infrastructure — monitoring & backups

Документация всей домашней лаборатории: что мониторится, какими источниками,
куда шлются алёрты, что бэкапится, куда и как восстанавливаться.

---

## 1. Хосты

| Hostname | Role | Subnet | OS | SSH alias |
|----------|------|--------|----|-----------| 
| **flint2** | основной роутер, gateway, XRAY-сервер | 192.168.100.1 | OpenWrt 21.02 (GL-MT6000) | `flint2` |
| **ryzen4700** | домашний сервер, Immich + бот, cloudflared, media-srv (Jellyfin + *arr + Jellyseerr + Searcharr), **butler-home-ai** (Telegram→Claude агент по инфре), **domovoy** (голосовой помощник по хозяйству: списки/календарь/напоминалки) + **Mealie** (рецепты) | 192.168.100.5 | Ubuntu 24.04 | `ryzen4700` |
| **beryl** | travel-роутер, sing-box VPN-клиент | 192.168.200.1 | OpenWrt 21.02 (GL-MT3000) | `beryl` |

Доменные имена (через DNS sys-lab.xyz):
- `immich.sys-lab.xyz:8443` → ryzen4700, через XRAY на flint2
- `vpn.sys-lab.xyz:8443` → flint2, XRAY VLESS+Reality для VPN-клиентов
- `kitchen.sys-lab.xyz` → Mealie (рецепты domovoy) на ryzen, **через Cloudflare Tunnel** (cloudflared, published application route → `http://mealie:9000`, mealie в сети `proxy`) + **Cloudflare Access** (email-OTP, политика `household` = твой+женин email). TLS на edge Cloudflare, без проброса портов. API бота к Mealie идёт внутри docker (`http://mealie:9000`), мимо Access.
- Публичный IP: `176.221.192.204` (домашний провайдер на flint2)

---

## 2. Мониторинг — 4 независимых уровня

```
                    ┌───────────────────────────────────────────┐
                    │   Telegram chat 286584627 (Sykk_)        │
                    │   через @flint2_watchdog_bot              │
                    └─────────────┬─────────────────────────────┘
                                  │
         ┌────────────┬───────────┼───────────┬─────────────────┐
         │            │           │           │                 │
    🔴 INTERNAL   🔴 LAN      🔴 EXTERNAL   🔴 DEADMAN       🟢 RECOVERY
   ryzen4700    flint2     exit1.dev →    healthchecks.io   (любой из выше)
   watchdog     watchdog   CF Worker →     deadman switch
   (cron 1min)  (cron 1min) bot           (cron 2min)
         │            │           │           │
    docker        ping ryzen4700  HTTPS      ping flint2 →
    inspect       + Immich LAN    keyword     hc-ping.com
    7 контейнеров HTTP            check
```

### Layer 1 — INTERNAL (ryzen4700-watchdog)
- **Где:** `/home/sykkyb/watchdog/` на ryzen4700
- **Cron:** `* * * * *` (каждую минуту), пользовательский cron sykkyb
- **Что:** `docker inspect` для whitelist'а: immich_server, immich_machine_learning, immich_postgres, immich_redis, vanilla-sky-monitor, vanilla-sky-redirect, cloudflared, **domovoy**, **mealie**
- **Триггер:** контейнер не в state=running ИЛИ Health=unhealthy три раза подряд
- **Лог:** `/home/sykkyb/watchdog/watchdog.log` (только фейлы и переходы)

**Layer 1 add-on — media-srv watchdog**
- **Где:** `/opt/media-srv/scripts/watchdog-check.sh` (symlink в `/usr/local/bin/media-srv-watchdog`)
- **Cron:** `* * * * *` (пользовательский cron sykkyb)
- **Что:** `docker inspect` + HTTP probe для 8 контейнеров media-srv (jellyfin/qbittorrent/prowlarr/sonarr/radarr/bazarr/jellyseerr/searcharr) + `df` probe для `/mnt/media` (warn ≥90%)
- **Триггер:** state transition (ok→down / down→ok). Алерт только при смене статуса, не каждую минуту
- **State:** `/var/tmp/media-srv-watchdog/<service>.state` (refreshed every run — dir always reflects current state)
- **Telegram:** тот же `flint2_watchdog_bot`, креды из `~/watchdog/config.env` (`TG_TOKEN` / `TG_CHAT_ID` / `HOST_LABEL` — те же что у immich-watchdog)
- **Time:** карточки в Asia/Tbilisi даже когда host TZ=UTC (через `TZ_NAME` в скрипте)
- **Формат:** структурированные карточки в стиле Layer 1 (icon + title + From/Source/Service/Detail/Time)
- **Пример алёрта:**
  ```
  🔴 SERVICE DOWN — INTERNAL
  ━━━━━━━━━━━━━━━━
  🖥 From: ryzen4700 (docker probe)
  📦 Service: immich_server
  ℹ️ Detail: 3 consecutive failed checks · status=exited, health=unhealthy
  🕒 Time: 2026-05-04 21:30:00
  ```

### Layer 2 — LAN (flint2-watchdog)
- **Где:** `/mnt/sda1/watchdog/` на flint2
- **Cron:** `* * * * *` (root crontab)
- **Что:** ping 192.168.100.5 + curl `http://192.168.100.5:2283/api/server/ping`
- **Покрывает:** ryzen целиком мёртв ИЛИ Immich контейнер не отвечает по HTTP
- **Лог:** `/mnt/sda1/watchdog/watchdog.log`

### Layer 2b — DISK (flint2 disk-watchdog) — добавлено 2026-06-21
- **Где:** `/usr/sbin/disk-watchdog` на **внутренней флешке** (НЕ на `/mnt/sda1`!), TG-конфиг `/etc/disk-watchdog.env`. В git: `deploy/disk-watchdog`.
- **Cron:** `* * * * *` (root crontab)
- **Что:** проверяет (1) USB-бэкап-диск `/mnt/sda1` примонтирован + пишется; (2) Samba: smbd жив + порт 445 + пути шар.
- **Самолечение:** если диск присутствует, но не примонтирован в `/mnt/sda1` (частая гонка GL-mountd после reseat) — сам перемонтирует; если smbd упал (`reinit_after_fork`/tmpfs) — пересоздаёт `/var/lib/samba/private` и др. + рестартит samba.
- **Алерт:** 3 фейла подряд → 🔴 в `flint2-watchdog`-бот, 🟢 RECOVERED при возврате. Формат как у Layer 2.
- **Почему отдельно от Layer 2:** основной watchdog живёт на самом USB-диске и умирает вместе с ним; этот — на флешке, поэтому может детектить и алертить пропажу диска.
- **Лог:** `/tmp/disk-watchdog.log`, state `/tmp/disk-watchdog-state/`.

### Layer 3 — EXTERNAL (exit1.dev + Cloudflare Worker)
- **Сервис мониторинга:** [exit1.dev](https://app.exit1.dev/) (free, 10 мониторов, 5-мин интервал)
- **Чек:** Web Keyword `https://immich.sys-lab.xyz:8443/api/server/ping` ищет `pong`. Регион Frankfurt.
- **Webhook:** `r2:exit1.dev → Cloudflare Worker exit1-telegram-relay → Telegram bot`
  - Worker URL: `https://exit1-telegram-relay.alexandr-rachok.workers.dev`
  - Worker делает: парсит JSON от exit1.dev → форматирует HTML-карточку → POST на api.telegram.org
  - Worker secrets: `TG_TOKEN`, `TG_CHAT_ID`, `TZ_NAME=Asia/Tbilisi`
- **Покрывает:** реальный путь снаружи (cloudflared/XRAY/cert/DNS/интернет провайдера). Самый «дорогой» детект (10–20 мин), но единственный честный external probe.

### Layer 4 — DEADMAN (healthchecks.io)
- **Сервис:** [healthchecks.io](https://healthchecks.io/) (free, неограниченное число чеков)
- **Period:** 5 мин, **Grace:** 5 мин → молчание 10+ мин = алёрт
- **Heartbeat:** `*/2 * * * * curl https://hc-ping.com/<UUID>` в crontab flint2
- **Webhook integration:** custom JSON body POST на api.telegram.org/bot.../sendMessage напрямую (без Worker)
- **Покрывает:** flint2 / интернет провайдера упали целиком. Единственный сигнал когда даже Layer 3 не сработает (потому что кричать снаружи некому если флинт мёртв — но healthchecks увидит «пинги перестали приходить»).

### Тестирование
```bash
# Layer 1 + 2 (контейнер сдох)
ssh ryzen4700 'docker stop immich_server'
# через ~3 мин прилетит ryzen-watchdog, потом flint-watchdog
ssh ryzen4700 'docker start immich_server'

# Layer 3 (внешний probe)
# exit1.dev сам поймает в течение 10–15 мин при том же docker stop

# Layer 4 (deadman)
# Закомментировать строку с hc-ping в crontab flint2:
ssh flint2 'crontab -l | sed "s|^\(.*hc-ping.com.*\)|#\1|" | crontab -'
# через 10 мин прилетит "🔴 HEARTBEAT LOST"
# раскомментировать обратно:
ssh flint2 'crontab -l | sed "s|^#\(.*hc-ping.com.*\)|\1|" | crontab -'
```

---

## 3. Бэкапы — 5 независимых pipeline

```
            ┌─────────────────────────────────────────────────┐
            │  Cloudflare R2 bucket: sys-lab-home-backups     │
            │  Endpoint: <account>.r2.cloudflarestorage.com   │
            │  Token: scoped Read+Write only on this bucket   │
            └─────────────────────────────────────────────────┘
                  ▲             ▲                ▲
                  │             │                │
        ┌─────────┴───────┐ ┌──┴────────┐ ┌─────┴─────────┐
        │ system-snapshots│ │beryl-    │ │ snapshots/    │
        │ (daily)         │ │snapshots │ │ (one-off)    │
        └─────────────────┘ │(weekly)  │ │              │
                  ▲         └──┬───────┘ └──────────────┘
                  │            │                ▲
                  │            │                │
       /usr/sbin/system    /usr/sbin/beryl   manual rclone
       -config-backup      -config-backup    upload from Mac
       on flint2 (cron     on beryl (cron    (historical)
       04:30 daily)        Sun 04:00 UTC)
                  │
        ┌─────────┴─────────────┐
        │ flint2 local +        │
        │ ssh-pull from ryzen   │
        └───────────────────────┘
```

### Pipeline 1 — flint2 + ryzen daily (`system-config-backup`)

**Расположение:**
- Скрипт: `/usr/sbin/system-config-backup` на flint2
- Локальные снапшоты: `/mnt/sda1/system-backup/snapshots/`
- Состояние: `/mnt/sda1/system-backup/state.sha256`
- Лог: `/mnt/sda1/system-backup/system-backup.log`
- Ретеншн: 30 дней локально, 180 дней в R2

**Cron на flint2:**
```
30 4 * * * /usr/sbin/system-config-backup
```

**Что бэкапится с flint2 (локально):**
- `/etc/config/` (UCI), `/etc/dropbear/` (SSH-ключи), `/etc/{passwd,shadow,group,crontabs}`, `/etc/firewall.user`, `/etc/rc.local`
- `/etc/AdGuardHome/` (DNS rewrites, client-исключения — добавлено 2026-06-21 после потери при чистой перешивке)
- `/etc/samba/` (smbpasswd — пароли samba-юзеров; добавлено 2026-06-21)
- `/root/`
- `/usr/sbin/xray-panel-backup`, `/usr/sbin/system-config-backup`, `/usr/sbin/disk-watchdog`
- `/etc/disk-watchdog.env` (TG-конфиг flash-watchdog)
- `/mnt/sda1/watchdog/{watchdog.sh,config.env}`
- crontab snapshot

**Что бэкапится с ryzen (через SSH ключ flint→ryzen):**
- Compose-стеки: `/srv/{immich,cloudflared,vanilla-sky}/docker-compose.yml` + `.env` + `config.yml`
- `/etc/caddy/Caddyfile`, `/etc/rsnapshot.conf`, `/etc/cron.d/`, `/etc/crontab`, `/etc/ssh/sshd_config`
- `/home/sykkyb/.bashrc`, `.profile`, `.msmtprc`, `.ssh/`, `watchdog/`
- `/usr/local/bin/{rsnapshot-safe,system-config-readroot}`
- crontab snapshot, `docker ps -a`, `docker volume ls`, `docker network ls`
- SQLite базы (`RYZEN_SQLITE_DBS` в скрипте): `/srv/vanilla-sky/data/state.db` —
  снимается через `sqlite3 .backup` чтобы получить консистентный снапшот
  на живом WAL. Без этого после потери ryzen бот перезапустится с пустой
  историей и при первом цикле залпом перешлёт «newly released» по каждому
  bookable рейсу.

**Что бэкапится с ryzen через root (NOPASSWD sudo на specific helper):**
- `/etc/netplan/`, `/etc/ufw/`, `/etc/systemd/system/caddy.service.d/`, `/root/.ssh/authorized_keys`
- Помощник: `/usr/local/bin/system-config-readroot` (хардкод-paths, only this script in sudoers)
- sudoers entry: `/etc/sudoers.d/system-config-backup`

**Что НЕ бэкапится (намеренно):**
- `/srv/immich/library/` — фотки, уже под rsnapshot в `/mnt/sda1/immich-backup/`
- `/var/lib/docker/` — данные контейнеров (восстанавливаются compose+env+rsnapshot)

**Hash-skip:** скрипт SHA256-хэширует все исходники + сравнивает с `state.sha256`. Если ничего не менялось, выходит за миллисекунды без архивации/upload.

**Шифрование (с 2026-06-03):** снапшот шифруется `openssl AES-256-cbc -pbkdf2` ПЕРЕД сохранением локально и заливкой в R2 (файлы `*.tar.gz.enc`). Причина — в архиве лежит `/home/sykkyb/.ssh` и др. чувствительное, нельзя хранить в облаке открытым. Пароль: `flint2:/etc/system-backup.pass` (chmod 600) + **дубль в зашифрованных заметках**. Старые plaintext-снапшоты вычищены из flint2 и R2. Расшифровка: `openssl enc -d -aes-256-cbc -pbkdf2 -pass file:<pass> -in <файл>.enc | tar xzf -`.

### Pipeline 2 — beryl weekly self-backup (`beryl-config-backup`)

**Расположение:**
- Скрипт: `/usr/sbin/beryl-config-backup` на beryl
- Состояние: `/etc/beryl-backup/{state.sha256,beryl-backup.log}`
- rclone: эфемерный в `/tmp/rclone`, удаляется trap'ом после каждого запуска (только когда нужен upload)
- Постоянный disk-footprint: ~5 KB

**Cron на beryl:**
```
0 4 * * 0 /usr/sbin/beryl-config-backup
```

**Что бэкапится (скоуп расширен 2026-06-22 после инцидента с MT6000 — чтобы чистая перешивка beryl была полностью восстановима, а не только наш деплой):**
- **`/etc/config`** целиком (network, wireless, dhcp, firewall, **wireguard_server** с ключами, adguardhome, samba4, sing-box, switch-button, …)
- `/etc/AdGuardHome/config.yaml` (кастом DNS rewrites/clients), `/etc/xray/` (config.json)
- `/etc/dropbear/` (ssh host-ключи + authorized_keys), `/etc/{passwd,shadow,group}`, `/etc/crontabs`
- `/etc/rclone` (R2-креды), `/etc/openvpn`, `/etc/wireguard` (AmneziaWG-параметры если включат), `/root`
- `/usr/bin/{sing-box,xray-panel-cli}`, `/etc/sing-box/`, `/etc/xray-panel-cli/` (вкл. `sources.json` для VPN Scout + `scans/`)
- `/etc/init.d/{sing-box,xray-panel-cli}`, `/etc/hotplug.d/button/50-sing-box-switch`, `/etc/sysctl.d/99-disable-mptcp.conf`
- `/www/xray-panel-launcher.js`, `/www/gl_home.html`, `/www/gl_home.html.bak`

**Шифрование (с 2026-06-22):** скоуп теперь содержит секреты (WG-приватключи, ssh, shadow), поэтому снапшот **AES-256 шифруется** перед заливкой в R2 (fail-closed — при сбое шифрования upload отменяется). Пароль: `beryl:/etc/beryl-backup.pass` (= `flint2:/etc/system-backup.pass`, дубль в 1Password). Файлы в R2: `beryl-self-*.tar.gz.enc`. Хеш-детектор использует `find -exec sha256sum` (устойчив к кавычкам в именах).

**Поведение:**
| Состояние beryl | Что произойдёт |
|-----------------|----------------|
| дома, без изменений | exit за <2 сек, нулевой трафик |
| в дороге, есть интернет, есть изменения | wget rclone (25 MB) → tar (~16 MB) → upload → trap чистит /tmp |
| в дороге, нет интернета | wget fail, лог "rclone download failed", state не обновляется → retry в следующее воскресенье |
| выключен | cron не сработает, отработает в следующий запуск |

### Pipeline 3 — beryl manual from Mac (`scripts/backup.sh`)

**Расположение:** `~/Documents/projects/home-lab/beryl-xray-web-console/scripts/backup.sh`

**Когда использовать:** beryl дома + хочется guarantee'ать снапшот прямо сейчас (например после изменений в xray-panel-cli или конфигах). Или если cron на beryl был отключён.

**Запуск:**
```bash
cd ~/Documents/projects/home-lab/beryl-xray-web-console
scripts/backup.sh           # цель beryl
scripts/backup.sh other     # цель custom SSH alias
```

Делает то же что Pipeline 2, но запускается с Mac → SCP файлов → tar.gz в `<repo>/backups/` (gitignored). Дополнительно мирорит в R2 (`r2:sys-lab-home-backups/beryl-snapshots/`).

### Pipeline 4 — media-srv appdata (restic, local-only)

Отдельный pipeline для configs стека Jellyfin + *arr на ryzen. **Не в R2** — слишком большие SQLite БД (~сотни MB у Sonarr/Radarr/Jellyfin), CIFS-том на flint2 (`/mnt/sda1/immich-backup/`, mount как `/backup` на ryzen) уже есть и доступен.

**Расположение:**
- Скрипт: `/opt/media-srv/scripts/backup.sh` (из репо `github.com/SykkyB/media-srv`)
- Restic репо: `/backup/restic-media-srv/` (это CIFS-mount на flint2:/mnt/sda1/immich-backup)
- Пароль: `/root/.restic-media-srv.pass` (chmod 600, продублирован в зашифрованных заметках)
- Лог: `/var/log/media-srv-backup.log`

**Cron на ryzen (root):**
```
30 6 * * * /opt/media-srv/scripts/backup.sh >> /var/log/media-srv-backup.log 2>&1
```
(хост TZ=UTC, так что 06:30 UTC = 10:30 по Tbilisi)

**Что бэкапится:**
- `/opt/appdata/` целиком (configs + SQLite всех сервисов)
- Исключения: `*/log/*`, `*/logs/*`, `*/Logs/*`, `*/Cache/*`, `*/cache/*`

**Что НЕ бэкапится (намеренно):**
- `/mnt/media/` — сам медиа-контент (фильмы/сериалы/торренты). Можно перекачать.
- `/var/lib/docker/` — overlay + volumes. `jellyfin_cache` — derived, не нужен.

**Поведение:**
- Скрипт делает `docker compose stop` → restic snapshot → `up -d` (через trap, чтобы стек поднялся даже при ошибке снапшота). Это нужно чтобы SQLite (Sonarr/Radarr/Jellyfin) не повредились на живом WAL. (С 2026-06-03: `start`→`up -d`, чтобы соблюдался `depends_on` — иначе searcharr поднимался раньше Sonarr/Radarr и зависал; trap также чинит снятие `.paused` watchdog.)
- Retention: 7 daily + 4 weekly + 6 monthly. На 2-3 GB начального снапшота с dedup занимает ~5-10 GB в `/backup` через год.

**Restore:** `/opt/media-srv/scripts/restore.sh` — интерактивный (выбор snapshot ID + target dir). Подробнее в разделе 5.6.

### Pipeline 5 — butler-home-ai (аудит + секреты)

Бэкап агента butler-home-ai (репо `github.com/SykkyB/butler-home-ai`, деплой `ryzen4700:~/butler-home-ai`). Сам код — на GitHub; здесь бэкапятся рантайм-аудит и секреты.

**Скрипты (в репо butler, cron sykkyb на ryzen):**
- `ship-audit.sh` — cron `0 4 * * *` (ежедневно). Аудит-лог (`data/audit/`) → tar → `flint2:/mnt/sda1/butler-audit/` **и** → R2 `r2:butler-audit/` (через rclone в `~/bin/rclone`, креды в `~/butler-home-ai/.audit-r2.env`).
- `backup-secrets.sh` — `30 4 * * 0` (еженедельно, вс). Секреты (`.env` = токены бота/Claude, `secrets/ssh/` = приватный ключ агента, `.audit-r2.env`) → **зашифрованный** `openssl AES-256` блоб `butler-secrets.tgz.enc` → flint2:/mnt/sda1/butler-secrets/ **и** → R2. Пароль: `~/butler-home-ai/.backup-pass` (chmod 600) + **дубль в зашифрованных заметках**.

**Почему отдельный R2-бакет `butler-audit`, а не `sys-lab-home-backups`:** у butler свой scoped-токен, и на бакете включён **Object Lock** (WORM) — залитые аудит/секреты нельзя стереть даже с валидным токеном.

**Restore:** см. 5.7.

### Pipeline 6 — domovoy (секреты + данные бота + книга рецептов Mealie)

Бэкап голосового помощника domovoy (репо `github.com/SykkyB/domovoy`, деплой `ryzen4700:~/domovoy`) и связанного Mealie (`ryzen4700:~/domovoy/mealie`). Код — на GitHub; здесь бэкапятся секреты и пользовательские данные, которых нет в git.

**Скрипт (в репо domovoy, cron sykkyb на ryzen):**
- `backup.sh` — `45 4 * * 0` (еженедельно, вс). Собирает в один **зашифрованный** (`openssl AES-256`) блоб `domovoy-backup.tgz.enc`:
  - `.env` — токены (бота, `CLAUDE_CODE_OAUTH_TOKEN`, `MEALIE_TOKEN`, Google calendar id, whitelist)
  - `data/` бота — `domovoy.db` (списки/напоминания), `google_token.json` (OAuth Calendar)
  - `mealie/data/` — рецепты + картинки + `mealie.db`
  - **исключено:** модели whisper (`data/whisper-models`, перекачиваются сами)
- **Root-владелые тома** (data/ и mealie/data владеет root) читаются через `docker exec tar` / `docker cp` — на ryzen нет passwordless sudo.
- **Две точки:** `flint2:/mnt/sda1/domovoy-backup/` (одна перезаписываемая копия по butler-ssh-ключу) **и** R2 `domovoy-snapshots/` (таймстемп-имена, ротация 180д).
- **Общие с butler** пароль шифрования (`~/butler-home-ai/.backup-pass`) и ssh-ключ к flint2 (`~/butler-home-ai/secrets/ssh/id_ed25519`). Креды R2 (основной бакет) — `~/domovoy/.r2.env` (`RCLONE_CONFIG_R2_*`, chmod 600, gitignored).

**Restore:** см. 5.8.

---

## 4. Cloudflare R2 — структура хранилища

**Bucket:** `sys-lab-home-backups`
**Регион:** Europe (eur)
**Storage class:** Standard
**Public access:** Disabled
**Token:** scoped Read+Write only, без права DeleteBucket/CreateBucket. См. файл `~/.r2-creds.env` на Mac.

```
sys-lab-home-backups/
├── system-snapshots/                                     ← Pipeline 1 (daily)
│   └── system-config-2026-06-03_2357.tar.gz.enc         (ШИФРОВАНО, ~50 KB)
│
├── xray-snapshots/                                       ← xray-panel-backup (с 2026-06-21)
│   └── 2026-06-21_1509.tar.gz.enc                        (ШИФРОВАНО, ~15 MB: xray+panel+reality)
│
├── flint2-prefw/                                         ← разовый pre-firmware снапшот
│   └── flint2-prefw-2026-06-21_0126.tar.gz.enc          (перед апгрейдом 4.9.0)
│
├── beryl-snapshots/                                      ← Pipeline 2+3
│   ├── beryl-self-20260504-194152Z.tar.gz               (~16 MB, beryl-self)
│   └── beryl-20260504-193555Z.tar.gz                    (~16 MB, Mac-side)
│
├── domovoy-snapshots/                                    ← Pipeline 6 (weekly)
│   └── domovoy-backup-20260628-194825Z.tgz.enc          (ШИФРОВАНО, ~210 KB: .env+db+google+рецепты)
│
└── snapshots/                                            ← разовый ad-hoc
    └── watchdog-config-2026-05-04_2215.tar.gz           (исторический)

butler-audit/                                             ← Pipeline 5, Object Lock ON
├── audit-2026-06-03.tgz                                  (ежедневный аудит butler)
└── butler-secrets.tgz.enc                                (ШИФРОВАНО: .env/ssh-ключ/r2-creds)
```

**Bucket `butler-audit`:** отдельный scoped-токен (Object Read&Write только на него), **Object Lock включён** (WORM — объекты нельзя удалить/перезаписать до конца retention, даже с валидным токеном). Креды: `ryzen4700:~/butler-home-ai/.audit-r2.env`.

> **Важно:** `system-snapshots/` теперь содержит `*.tar.gz.enc` (Pipeline 1 шифрует). Старые plaintext-объекты удалены. На `sys-lab-home-backups` Object Lock тоже **включён**.

**Free-tier лимиты Cloudflare R2:**
- Storage: 10 GB (используем ~32 MB)
- Class A operations (PUT/POST): 1M/мес — мы делаем ~40/мес
- Class B operations (GET/HEAD): 10M/мес
- Egress: бесплатный

При текущем расходе уложимся в free навсегда.

**Доступ с Mac:**
```bash
rclone ls r2:sys-lab-home-backups            # листинг всего
rclone copy file r2:sys-lab-home-backups/<prefix>/   # upload
rclone copy r2:sys-lab-home-backups/<prefix>/file ./ # download
rclone delete --min-age 180d r2:sys-lab-home-backups/<prefix>/  # ротация
```

rclone profile живёт в `~/.config/rclone/rclone.conf` (создан на основе `~/.r2-creds.env`).

---

## 5. Восстановление

### 5.1 Восстановление из R2 → новый/чистый flint2

**Предположение:** прошивка GL.iNet установлена, базовая настройка сети сделана (LAN, WAN, USB-disk). Нужно восстановить кастомные конфиги.

```bash
# 1. С Mac скачиваем последний снапшот (теперь .enc — ШИФРОВАН)
rclone copy r2:sys-lab-home-backups/system-snapshots/ /tmp/restore/ --max-age 7d

# 1b. Расшифровываем (пароль из зашифрованных заметок: "system-backup pass")
openssl enc -d -aes-256-cbc -pbkdf2 -pass pass:'<ПАРОЛЬ_ИЗ_ЗАМЕТОК>' \
  -in /tmp/restore/system-config-*.tar.gz.enc -out /tmp/restore/system-config.tar.gz

# 2. Заливаем на флинт
scp -O /tmp/restore/system-config.tar.gz flint2:/tmp/system-config-restore.tar.gz

# 3. На флинте распаковываем (только flint2-часть)
ssh flint2 'cd /tmp && tar -xzf system-config-restore.tar.gz; \
  cp -a flint2/etc/config/* /etc/config/; \
  cp -a flint2/etc/dropbear/* /etc/dropbear/; \
  cp -a flint2/root/* /root/ 2>/dev/null; \
  cp flint2/usr/sbin/* /usr/sbin/ && chmod 755 /usr/sbin/xray-panel-backup /usr/sbin/system-config-backup; \
  mkdir -p /mnt/sda1/watchdog && cp -a flint2/mnt/sda1/watchdog/* /mnt/sda1/watchdog/; \
  chmod 700 /mnt/sda1/watchdog/watchdog.sh && chmod 600 /mnt/sda1/watchdog/config.env'

# 4. Восстанавливаем crontab
ssh flint2 'cat /tmp/flint2/crontab.txt | crontab -'

# 5. Перезагрузка для применения UCI
ssh flint2 'reboot'

# 6. После загрузки — установить rclone (для будущих бэкапов)
# (см. шаг "Установка rclone на флинт" в разделе 6)

# 7. Восстановить SSH key flint→ryzen
# (он уже в /root/.ssh из бэкапа, но может надо перепринять host key ryzen)
ssh flint2 'ssh -i /root/.ssh/id_ed25519 -o StrictHostKeyChecking=accept-new sykkyb@192.168.100.5 "echo OK"'
```

**Нет в бэкапе** (надо восстановить вручную или с другого источника):
- Прошивка GL.iNet — переустановить с офсайта
- Базовый WAN/LAN setup — через GL.iNet UI
- xray-panel + xray-binary — отдельный pipeline `xray-panel-backup`. Локальные snapshots в `/mnt/sda1/xray-backup/snapshots/` (plaintext), **с 2026-06-21 ШИФРУЕТ и льёт в R2** `xray-snapshots/` (retention 180д, ключ тот же `/etc/system-backup.pass`). Раньше offsite не уходил — из-за этого reality-ключи чуть не потерялись при смерти USB-диска.

### 5.1c Апгрейд прошивки flint2 + U-Boot recovery (урок 2026-06-21)

**Главное правило: апгрейд прошивки делать ТОЛЬКО при наличии свежего offsite-бэкапа (R2).** Даже режим «Keep settings» сносит наши бинари/скрипты.

**Что переживает sysupgrade (keep settings):** только `sysupgrade -l` keep-list — `/etc/config/*` (вкл. xray, wireguard_server, dhcp, firewall, samba4), `/etc/xray/config.json`, `/etc/crontabs/root`, dropbear-ключи, passwd/shadow.
**Что НЕ переживает (восстанавливать вручную):** `/usr/bin/{xray,xray-panel}`, `/etc/init.d/{xray,xray-panel}`, `/etc/xray-panel/`, `/www/xray-panel-launcher.js` + патч `gl_home.html`, `/usr/sbin/*backup` + `disk-watchdog`, `/etc/rclone/`, `/etc/system-backup.pass`, `/etc/AdGuardHome/config.yaml`, `/etc/samba/smbpasswd`, `/root/.ssh/id_ed25519`, openssh-client.

**U-Boot recovery (если роутер не грузится / нужна чистая перешивка):**
1. Ноут статиком `192.168.1.2/24`, кабель в LAN-порт.
2. Выключить; зажать RESET, подать питание, держать ~8 сек пока LED не замигает часто.
3. Браузер → `http://192.168.1.1` → залить `.bin` (хост прошивок: `https://fw.gl-inet.com/firmware/mt6000/release/`).
4. Чистая прошивка → заводские (LAN `192.168.8.1`), мастер задаёт admin-пароль (= root SSH пароль).

**После чистой перешивки — восстановление по этапам:**
1. Почистить known_hosts (`ssh-keygen -R 192.168.100.1` — host-ключи сменились), вернуть свой+butler ssh-ключ в `/etc/dropbear/authorized_keys`.
2. `/etc/config/*` (network/wireless/dhcp/firewall/wireguard_server/samba4) — из R2-снапшота (cherry-pick, не всё подряд — схемы 4.9.0 могут отличаться).
3. xray-core + panel + scripts + rclone.conf + pass — из prefw-tarball; `deploy/install.sh flint2` для пере-патча `gl_home.html`.
4. WG-сервер: положить `/etc/config/wireguard_server` с ключами → включить тумблер в UI (ключи сохранятся, клиенты не отвалятся).
5. **Samba:** GL NAS UI НЕ создаёт юзеров из старой восстановленной БД → завести unix-юзеров вручную (`/etc/passwd`+`/etc/group`, shell `/bin/false`) + `smbpasswd -a -s`, упростить `samba4.@sambashare[].users` на прямые имена. Пароли samba нигде не бэкапятся восстановимо — задаются заново (и обновить на ryzen `/root/.smbcred`).
6. **smbd падает `reinit_after_fork: NT_STATUS_DISK_FULL`** если нет рантайм-каталогов: `mkdir -p /var/lib/samba/private /var/run/samba/msg.lock /var/lock /var/cache/samba` (вписано в `/etc/rc.local`, т.к. /var→/tmp tmpfs; disk-watchdog тоже самолечит).
7. Transmission — отдельный пакет (`opkg install transmission-daemon transmission-web`), в чистой прошивке не стоит.
8. **minidlna/DLNA**: чистая прошивка сбрасывает на заводское — `db_dir=/var/run/minidlna` (tmpfs) и `media_dir`=ВЕСЬ диск → индекс на сотни МБ строится в `/tmp`, забивает tmpfs до 100% и валит бэкапы/samba (индексирует и immich-backup-фото). **Правильная (наша) конфигурация:** `db_dir=/mnt/sda1/.minidlna-db` (БД на диске, не в памяти) + `media_dir=/mnt/sda1/downloads_ptp` (только загрузки, immich-backup исключён) + `enabled=1`:
   ```
   uci set minidlna.config.enabled=1
   uci set minidlna.config.db_dir=/mnt/sda1/.minidlna-db
   uci delete minidlna.config.media_dir; uci add_list minidlna.config.media_dir=/mnt/sda1/downloads_ptp
   uci commit minidlna; mkdir -p /mnt/sda1/.minidlna-db; /etc/init.d/minidlna enable; /etc/init.d/minidlna restart
   ```
9. **immich port-forward (WAN 8443 → ryzen 192.168.100.5:8443)** — создаётся в UI (Security → Port Forwarding → +Add: TCP, ext 8443, IP 192.168.100.5, int 8443). Хранится в `/etc/config/port_forward` (НЕ в firewall!). В 4.9.0 проброс работает через **kernel-модуль `port_forward`** (`/proc/port_forward`, write-only) — **в iptables его НЕ видно, это норма**. ВАЖНО: модуль не всегда грузится на буте сам → нужен `/etc/modules.d/99-port_forward` (содержит `port_forward`), иначе UI-запись висит, но не пробрасывает. Проверка (с любого хоста): `curl -k --resolve immich.sys-lab.xyz:8443:<WAN_IP> https://immich.sys-lab.xyz:8443/api/server/ping` → 200. На него завязан external-монитор Layer 3 (exit1.dev). Порт 8443 при этом занят и админкой роутера (uhttpd) — модуль перехватывает раньше, конфликта нет.

> ⚠️ USB-SSD (label `usbssd`) АППАРАТНО флакал в этот день (отваливался от шины 3+ раз) — при reseat монтируется в `/tmp/mountd/disk1_part1`, в `/mnt/sda1` возвращает disk-watchdog (Layer 2b) или вручную `mount /dev/sda1 /mnt/sda1`. Кандидат на замену.

### 5.2 Восстановление ryzen4700

```bash
# 1. С Mac скачиваем последний снапшот
rclone copy r2:sys-lab-home-backups/system-snapshots/ /tmp/restore/ --max-age 7d
# снапшот зашифрован (.enc) — расшифровать паролем из зашифрованных заметок:
cd /tmp/restore && openssl enc -d -aes-256-cbc -pbkdf2 -pass pass:'<ПАРОЛЬ_ИЗ_ЗАМЕТОК>' \
  -in system-config-*.tar.gz.enc -out system-config.tar.gz && tar -xzf system-config.tar.gz

# 2. Compose-стеки → /srv (нужен root)
scp -r ryzen4700/srv/* ryzen4700:/tmp/srv-restore/
ssh ryzen4700 'sudo cp -a /tmp/srv-restore/* /srv/ && \
  sudo chown -R sykkyb:docker /srv/immich/ && \
  sudo chown -R sykkyb:sykkyb /srv/vanilla-sky/data/'
# state.db поднимется автоматически (sqlite-backup в архиве — обычный файл).
# Важно: chown под user сделать ДО старта vanilla-sky-monitor, иначе контейнер
# (UID 1000) не сможет писать в state.db и упадёт.

# 3. Caddy config + systemd
scp -r ryzen4700/etc/caddy ryzen4700:/tmp/
ssh ryzen4700 'sudo cp /tmp/caddy/Caddyfile /etc/caddy/'

# 4. Distroот настройки (netplan, ufw) из вложенного root-archive
scp ryzen4700/_root-only-configs.tar.gz ryzen4700:/tmp/
ssh ryzen4700 'sudo tar -xzf /tmp/_root-only-configs.tar.gz -C /'
ssh ryzen4700 'sudo netplan apply; sudo ufw reload'

# 5. Watchdog
scp -r ryzen4700/home/sykkyb/watchdog/ ryzen4700:~/
ssh ryzen4700 'chmod 700 ~/watchdog/watchdog.sh && chmod 600 ~/watchdog/config.env'

# 6. SSH-ключ
scp -r ryzen4700/home/sykkyb/.ssh/* ryzen4700:~/.ssh/
ssh ryzen4700 'chmod 600 ~/.ssh/* && chmod 700 ~/.ssh'

# 7. Crontab
scp ryzen4700/crontab.txt ryzen4700:/tmp/
ssh ryzen4700 'crontab /tmp/crontab.txt'

# 8. Sudoers entry для system-config-readroot
ssh ryzen4700 'sudo install -m 755 ryzen4700/usr/local/bin/system-config-readroot /usr/local/bin/system-config-readroot && \
  echo "sykkyb ALL=(root) NOPASSWD: /usr/local/bin/system-config-readroot" | sudo tee /etc/sudoers.d/system-config-backup && \
  sudo chmod 440 /etc/sudoers.d/system-config-backup'

# 9. Запустить контейнеры
ssh ryzen4700 'cd /srv/immich && docker compose up -d'
ssh ryzen4700 'cd /srv/cloudflared && docker compose up -d'
ssh ryzen4700 'cd /srv/vanilla-sky && docker compose up -d'

# 10. rsnapshot восстановит фотки Immich из /mnt/sda1/immich-backup/daily.X
# Это отдельный путь (нерсе R2-бэкап).
```

### 5.3 Восстановление beryl

См. также `~/Documents/projects/beryl-xray-web-console/scripts/backup.sh` — там есть restore-инструкции в шапке.

```bash
# 1. С Mac берём свежайший beryl-snapshot
rclone copy r2:sys-lab-home-backups/beryl-snapshots/ /tmp/beryl-restore/ --max-age 30d
cd /tmp/beryl-restore

# Выбираем самый свежий (либо beryl-self-* либо beryl-*)
LATEST=$(ls -t beryl*.tar.gz | head -1)

# 2. Заливаем на свежий beryl (после прошивки GL.iNet)
scp -O "$LATEST" beryl:/tmp/

# 3. Распаковка прямо в корень — все пути абсолютные внутри tar
ssh beryl "tar -xzf /tmp/$(basename $LATEST) -C /"

# 4. Включаем сервисы (init-скрипты восстановлены, но не enabled)
ssh beryl '/etc/init.d/sing-box enable; /etc/init.d/sing-box start'
ssh beryl '/etc/init.d/xray-panel-cli enable; /etc/init.d/xray-panel-cli start'

# 5. Если нужен self-backup — установить скрипт + cron
scp -O /usr/sbin/beryl-config-backup beryl:/usr/sbin/
ssh beryl 'chmod 755 /usr/sbin/beryl-config-backup; \
  crontab -l > /tmp/c; echo "0 4 * * 0 /usr/sbin/beryl-config-backup" >> /tmp/c; crontab /tmp/c'

# 6. rclone.conf для beryl (R2 creds)
# Скопировать вручную из ~/.r2-creds.env через ssh, см. секцию 6.
```

### 5.4 Восстановление Cloudflare Worker (exit1-telegram-relay)

Worker-код хранится в R2: `snapshots/watchdog-config-2026-05-04_2215.tar.gz` → `cloudflare-worker/worker.js`.

```bash
# 1. Скачать архив
rclone copy r2:sys-lab-home-backups/snapshots/watchdog-config-2026-05-04_2215.tar.gz /tmp/
cd /tmp && tar -xzf watchdog-config-2026-05-04_2215.tar.gz
cat watchdog-config-backup-2026-05-04_2215/cloudflare-worker/worker.js

# 2. В Cloudflare dashboard:
#    Workers & Pages → Create application → Create Worker
#    Имя: exit1-telegram-relay
#    Edit code → вставить содержимое worker.js → Deploy
#    Settings → Variables and Secrets → добавить (как Secret):
#      TG_TOKEN, TG_CHAT_ID, TZ_NAME (опц.)
#    См. cloudflare-worker/secrets.txt в архиве для значений
```

### 5.5 Восстановление SaaS-конфигурации (exit1.dev, healthchecks.io)

Описаны в архиве `snapshots/watchdog-config-2026-05-04_2215.tar.gz` в файлах:
- `saas/exit1.dev.md` — webhook URL, чек URL+keyword, regions, settings
- `saas/healthchecks.io.md` — period, grace, body templates для Down/Up

Эти сервисы пересоздаются вручную через UI (нет API-export'а на free плане).

### 5.6 Восстановление media-srv (restic из /backup)

Сценарий: ryzen переустановлен / SSD заменён / configs повреждены. Сам медиа-контент (`/mnt/media/`) на USB-диске WD My Passport не теряется при переустановке системы, поэтому восстанавливаем только `/opt/appdata/`.

```bash
# 1. Базовый host prep (см. SETUP.md в репо media-srv)
#    - ext4 на /dev/sda1 (если диск новый), монтаж в /mnt/media (fstab)
#    - hd-idle, render group, VA-API libs
#    - CIFS mount /backup ← flint2:/mnt/sda1/immich-backup
#
# 2. Клонируем репо
sudo git clone git@github.com:SykkyB/media-srv.git /opt/media-srv
sudo chown -R sykkyb:sykkyb /opt/media-srv
cd /opt/media-srv
cp .env.example .env
$EDITOR .env

# 3. Кладём пароль restic
sudo install -m 600 /dev/null /root/.restic-media-srv.pass
echo '<пароль из зашифрованных заметок>' | sudo tee /root/.restic-media-srv.pass

# 4. Восстанавливаем appdata
sudo restic -r /backup/restic-media-srv \
  --password-file /root/.restic-media-srv.pass \
  restore latest --target /

# 5. Запускаем стек
./scripts/deploy.sh

# 6. Подключаем watchdog (cron sykkyb)
sudo ln -sf /opt/media-srv/scripts/watchdog-check.sh /usr/local/bin/media-srv-watchdog
(crontab -l 2>/dev/null; echo "* * * * * /usr/local/bin/media-srv-watchdog") | crontab -

# 7. Подключаем daily backup (root crontab)
sudo crontab -e
# 30 3 * * * /opt/media-srv/scripts/backup.sh >> /var/log/media-srv-backup.log 2>&1
```

Sonarr/Radarr/Jellyfin поднимутся со всей библиотекой и историей загрузок. Если торренты были в активной раздаче — qBittorrent попробует переподключиться к peers и продолжит с того же места (state.fastresume в configs).

**Searcharr** — `/opt/appdata/searcharr/settings.py` тоже восстановится из restic, включая bot token и passwords. Если ты сменил host или потерял bot — пересоздай через @BotFather и впиши новый `tgram_token`. Авторизованные user_id хранятся в `searcharr.db` рядом, тоже восстанавливаются.

### 5.7 Восстановление butler-home-ai

```bash
# 1. Клонируем репо (код — на GitHub)
git clone git@github.com:SykkyB/butler-home-ai.git ~/butler-home-ai
cd ~/butler-home-ai

# 2. Берём зашифрованный блоб секретов (с flint2 или R2) и расшифровываем
#    (пароль из зашифрованных заметок: "butler-home-ai backup pass")
scp flint2:/mnt/sda1/butler-secrets/butler-secrets.tgz.enc .
#    либо: rclone copy r2:butler-audit/butler-secrets.tgz.enc .   (нужны R2-креды из зашифрованных заметок)
openssl enc -d -aes-256-cbc -pbkdf2 -pass pass:'<ПАРОЛЬ_ИЗ_ЗАМЕТОК>' \
  -in butler-secrets.tgz.enc | tar xzf -        # вернёт .env, .audit-r2.env, secrets/

# 3. Поставить rclone (для бэкап-джоба) и поднять контейнер
mkdir -p ~/bin && curl ... # см. установку rclone (или из репо)
docker compose up -d --build

# 4. Если SSH-ключ агента менялся — заново раздать pubkey на хосты:
#    ryzen ~/.ssh/authorized_keys (from="192.168.100.5,127.0.0.1"), flint2 dropbear
# 5. Вернуть cron: 0 4 * * * ship-audit.sh ; 30 4 * * 0 backup-secrets.sh
#    + .backup-pass (из зашифрованных заметок) в ~/butler-home-ai/.backup-pass (chmod 600)
```

> Зависимости: Telegram 2FA на аккаунте, токен бота (@BotFather), OAuth-токен Claude (`claude setup-token`), R2-токен `butler-audit`. Все перевыпускаемы; либо в расшифрованном `.env`/`.audit-r2.env`.

### 5.8 Восстановление domovoy (+ Mealie)

```bash
# 1. Клонируем репо (код — на GitHub) и общую docker-сеть
git clone git@github.com:SykkyB/domovoy.git ~/domovoy
docker network create domovoy-net          # если ещё нет

# 2. Берём зашифрованный блоб (с flint2 или R2) и расшифровываем
#    (пароль — общий с butler: "butler-home-ai backup pass" из зашифрованных заметок)
scp flint2:/mnt/sda1/domovoy-backup/domovoy-backup.tgz.enc .
#    либо: rclone copy r2:sys-lab-home-backups/domovoy-snapshots/<свежий>.tgz.enc .
openssl enc -d -aes-256-cbc -pbkdf2 -pass pass:'<ПАРОЛЬ_ИЗ_ЗАМЕТОК>' \
  -in domovoy-backup.tgz.enc | tar xzf -      # вернёт env, domovoy-data/, mealie-data/

# 3. Раскладываем секреты и данные
cp env ~/domovoy/.env
mkdir -p ~/domovoy/data ~/domovoy/mealie/data

# 4. Поднимаем Mealie (его данные кладём в том) и бота
cp -a mealie-data/. ~/domovoy/mealie/data/
cd ~/domovoy/mealie && docker compose up -d
cd ~/domovoy && docker compose up -d --build
#    domovoy.db и google_token.json кладутся в /data контейнера через docker cp:
docker cp domovoy-data/domovoy.db        domovoy:/data/domovoy.db
docker cp domovoy-data/google_token.json domovoy:/data/google_token.json
docker compose restart domovoy

# 5. Вернуть R2-креды для бэкапа и cron
#    ~/domovoy/.r2.env (из ~/.r2-creds.env на Mac, формат RCLONE_CONFIG_R2_*), chmod 600
#    cron: 45 4 * * 0 /home/sykkyb/domovoy/backup.sh
#    watchdog: добавить domovoy, mealie в ~/watchdog/watchdog.sh (массив CONTAINERS)
```

> Зависимости: токен бота (@BotFather), OAuth Claude (общий с butler, `claude setup-token`), Google OAuth (`tools/google_auth.py` заново, если token.json потерян — нужен `client_secret.json` из Google Cloud), Mealie API-токен (web → API Tokens, если потерян). Модели whisper скачаются сами при первом голосовом. Whisper-кэш и БД списков — не критичны (списки эфемерны), главное — `.env`, `google_token.json` и рецепты Mealie.

---

## 6. Routine ops

### 6.1 Просмотр логов
```bash
# Watchdog logs (только фейлы и переходы)
ssh ryzen4700 'tail -f ~/watchdog/watchdog.log'
ssh flint2 'tail -f /mnt/sda1/watchdog/watchdog.log'
ssh beryl 'tail -f /etc/beryl-backup/beryl-backup.log'

# System-config-backup лог
ssh flint2 'tail -f /mnt/sda1/system-backup/system-backup.log'

# Cloudflare Worker лог (incoming payloads)
# https://dash.cloudflare.com → Workers → exit1-telegram-relay → Observability → Logs
```

### 6.2 Ручной запуск бэкапа
```bash
ssh flint2 '/usr/sbin/system-config-backup'           # обычный (skip if no changes)
ssh flint2 '/usr/sbin/system-config-backup --force'   # форс-снапшот
ssh beryl  '/usr/sbin/beryl-config-backup --force'

# Mac-side beryl backup
cd ~/Documents/projects/home-lab/beryl-xray-web-console && scripts/backup.sh
```

### 6.3 Установка rclone на флинт (после reset/restore)
```bash
ssh flint2 'cd /tmp && wget -q -O rclone.zip "https://downloads.rclone.org/rclone-current-linux-arm64.zip" && \
  unzip -o rclone.zip > /dev/null && \
  mv rclone-*-linux-arm64/rclone /mnt/sda1/rclone && \
  chmod 755 /mnt/sda1/rclone && \
  rm -rf rclone.zip rclone-*-linux-arm64/'
```

### 6.4 Установка rclone.conf (флинт или beryl)
```bash
set -a; . ~/.r2-creds.env; set +a
ssh <host> "mkdir -p /etc/rclone && cat > /etc/rclone/rclone.conf <<EOF
[r2]
type = s3
provider = Cloudflare
access_key_id = ${R2_ACCESS_KEY_ID}
secret_access_key = ${R2_SECRET_ACCESS_KEY}
endpoint = ${R2_ENDPOINT}
no_check_bucket = true
EOF
chmod 600 /etc/rclone/rclone.conf"
```

### 6.5 Тест Telegram-доставки от любого источника
```bash
# Прямая отправка от @flint2_watchdog_bot
curl -X POST "https://api.telegram.org/bot$(grep TG_TOKEN ~/.r2-creds.env || ssh flint2 'grep TG_TOKEN /mnt/sda1/watchdog/config.env')/sendMessage" \
  -d chat_id=286584627 -d text="ping from $(hostname)"

# Через CF Worker (как ходит exit1.dev)
curl -X POST https://exit1-telegram-relay.alexandr-rachok.workers.dev \
  -H 'Content-Type: application/json' \
  -d '{"event":"website_down","website":{"name":"Test","url":"https://example.com"},"timestamp":1777914797814}'
```

### 6.6 Docker hygiene — resource limits, healthchecks, log rotation

Базовая операционная гигиена применена ко **всем** Docker-стекам на ryzen4700, чтобы один runaway-контейнер не положил остальные. Подход — [Otus best-practices](https://habr.com/ru/companies/otus/articles/1034390/).

**Log rotation — host-wide (один раз):**
```bash
# /etc/docker/daemon.json
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" }
}
```
После записи: `sudo systemctl restart docker` (рестартанёт все контейнеры). Применяется ко **всем** Docker-стекам на хосте без правок в compose.

**Resource limits + healthchecks по стекам:**

| Стек | Где конфиг | Лимиты заданы как | Healthchecks |
|------|------------|-------------------|--------------|
| media-srv (8 контейнеров) | `/opt/media-srv/docker-compose.yml` (git) | `deploy.resources.limits` + `reservations` | Все 7 с HTTP — кастомные; Jellyfin — встроенный; Searcharr — без (нет HTTP) |
| immich (4 контейнера) | `/srv/immich/docker-compose.override.yml` (НЕ в git, не трогает upstream) | legacy `mem_limit` + `cpus` (потому что upstream уже задаёт `cpus:` для machine-learning) | Встроенные в образы |
| vanilla-sky (2 контейнера) | `~/Documents/projects/home-lab/ryzen4700-homesrv/vanilla-sky/docker-compose.yml` (git) | `deploy.resources.limits` | Кастомные (heartbeat-файл / HTTP /health) |
| cloudflared (1 контейнер) | `/srv/cloudflared/docker-compose.override.yml` (НЕ в git, не трогает upstream) | `deploy.resources.limits` | Нет (cloudflared не имеет встроенного, добавление curl/wget не оправдано) |

**Бюджет памяти на ryzen4700 (32 GiB total):**

| Стек | Сумма limits | Доля от 32 GiB |
|------|--------------|---------------|
| media-srv | ~10 GiB | 31% |
| immich | ~7.25 GiB | 23% |
| vanilla-sky | 256 MiB | <1% |
| cloudflared | 128 MiB | <1% |
| **итого compose-лимиты** | **~17.6 GiB** | **55%** |
| host + buffers + page cache | ~14 GiB | 45% |

Лимиты — потолок, не reservation. Фактическое потребление в idle ~3-4 GiB суммарно (immich-ml ест больше всех когда индексирует, jellyfin — когда транскодит).

**Проверка состояния:**
```bash
# Кратко: все контейнеры и health
docker ps --format 'table {{.Names}}\t{{.Status}}'

# Память / CPU по факту против лимитов
docker stats --no-stream

# Размер логов на хосте
sudo du -sh /var/lib/docker/containers/*/*-json.log | sort -h | tail
```

**Override files — где смотреть:**
- `/srv/immich/docker-compose.override.yml` — лимиты только, остальное в upstream
- `/srv/cloudflared/docker-compose.override.yml` — лимит cloudflared
- Override-файлы автоматически merge'атся compose'ом при `up -d` в той же папке.
- **Бэкап**: `system-config-backup` теперь захватывает `docker-compose.override.yml` через `RYZEN_PATHS` (обновлено вместе с `/opt/media-srv/.env`).

### 6.7 Pretty URLs — Caddy reverse proxy + AdGuard DNS rewrites

Все 7 media-srv-сервисов плюс immich доступны по красивым subdomain'ам внутри LAN/WG с настоящим Let's Encrypt TLS. Цепочка:

```
client
  └─ DNS query "jellyfin.media.sys-lab.xyz"
     ↓
  AdGuard Home on flint2 (192.168.100.1:53 → 3053)
     └─ DNS Rewrite: *.media.sys-lab.xyz → 192.168.100.5
     ↓
  Caddy on ryzen4700:443
     ├─ wildcard cert *.media.sys-lab.xyz (Let's Encrypt DNS-01 via CF API token)
     └─ reverse_proxy localhost:<port>
     ↓
  service
```

**Subdomain → upstream port:**

| Subdomain                          | Port |
|------------------------------------|------|
| `jellyfin.media.sys-lab.xyz`       | 8096 |
| `qbit.media.sys-lab.xyz`           | 8080 |
| `prowlarr.media.sys-lab.xyz`       | 9696 |
| `sonarr.media.sys-lab.xyz`         | 8989 |
| `radarr.media.sys-lab.xyz`         | 7878 |
| `bazarr.media.sys-lab.xyz`         | 6767 |
| `jellyseerr.media.sys-lab.xyz`     | 5055 |
| `immich.sys-lab.xyz:8443`          | 2283 (publicly exposed via cloudflared, separate block in same Caddyfile) |

**Config:**
- Caddyfile: `/etc/caddy/Caddyfile` (один блок `*.media.sys-lab.xyz` с матчерами по host)
- CF API token: `/etc/systemd/system/caddy.service.d/env.conf` (env `CF_API_TOKEN`)
- AdGuard DNS rewrites: в AdGuard Home UI → http://192.168.100.1:3000 → Filters → DNS rewrites

**Не публичный доступ.** ISP блокирует входящие 80/443, и AdGuard отдаёт `*.media.sys-lab.xyz` только в LAN-IP. То есть в интернете эти имена «висят», но никто к ним не подключится. WireGuard-клиенты получают AdGuard как DNS, поэтому те же URL работают с телефона/ноута через WG.

**Восстановление после потери ryzen** (см. 5.2): Caddyfile входит в system-config-backup. CF_API_TOKEN тоже сохранится (через `/etc/systemd/system/caddy.service.d/env.conf`). После восстановления — `sudo systemctl reload caddy` и Caddy сам перевыпишет cert через DNS-01.

---

## 7. Где живут креды

| Что | Где |
|-----|-----|
| Telegram bot token (8643682083:...) | `flint2:/mnt/sda1/watchdog/config.env`, `ryzen4700:~/watchdog/config.env`, Cloudflare Worker secret `TG_TOKEN`, healthchecks.io webhook URL |
| Telegram chat_id (286584627) | те же файлы + Worker secret `TG_CHAT_ID` |
| R2 access keys | Mac: `~/.r2-creds.env` (chmod 600); flint2: `/etc/rclone/rclone.conf`; beryl: `/etc/rclone/rclone.conf` |
| SSH ключ flint2→ryzen | `flint2:/root/.ssh/id_ed25519` (приватный), `ryzen4700:~/.ssh/authorized_keys` (публичный) |
| sing-box VLESS UUID (beryl) | `beryl:/etc/sing-box/config.json` |
| xray-panel bcrypt hash | `beryl:/etc/xray-panel-cli/panel.yaml` |
| sykkyb's GitHub SSH key | `ryzen4700:~/.ssh/sykkyb@github` (приватный) |
| restic репо media-srv (пароль) | `ryzen4700:/root/.restic-media-srv.pass` (chmod 600), дубль в зашифрованных заметках |
| Searcharr TG bot token | `ryzen4700:/opt/appdata/searcharr/settings.py` (отдельный bot от `flint2_watchdog_bot`, создан через @BotFather специально для запросов) |
| Searcharr user/admin passwords | те же `searcharr_password` / `searcharr_admin_password` в `settings.py` — клиенты вводят при `/start <password>` |
| **butler** TG bot token + Claude OAuth token | `ryzen4700:~/butler-home-ai/.env` (отдельный bot от watchdog/searcharr) |
| **butler** SSH-ключ агента (root на flint2, `from=` на ryzen) | `ryzen4700:~/butler-home-ai/secrets/ssh/id_ed25519`; pub — в authorized_keys на ryzen/flint2 |
| **butler** R2-креды (бакет `butler-audit`) | `ryzen4700:~/butler-home-ai/.audit-r2.env` |
| **butler** пароль шифрования секрет-бэкапа | `ryzen4700:~/butler-home-ai/.backup-pass` (chmod 600) + **зашифрованные заметки** |
| **domovoy** TG bot token + Claude OAuth + Google calendar id + whitelist | `ryzen4700:~/domovoy/.env` (отдельный bot; Claude OAuth — тот же что у butler) |
| **domovoy** Google Calendar OAuth token | `ryzen4700:~/domovoy/data/google_token.json` (+ `client_secret.json`); GCP-проект `domovoy-ai` |
| **domovoy** Mealie API token | `ryzen4700:~/domovoy/.env` (`MEALIE_TOKEN`); Mealie web `http://192.168.100.5:9925`, дефолт-логин `changeme@example.com` |
| **domovoy** R2-креды (основной бакет) | `ryzen4700:~/domovoy/.r2.env` (`RCLONE_CONFIG_R2_*`, chmod 600) = `~/.r2-creds.env` на Mac |
| **domovoy** пароль шифрования бэкапа | = butler (`~/butler-home-ai/.backup-pass`) + **зашифрованные заметки** |
| **system-config-backup** пароль шифрования снапшота | `flint2:/etc/system-backup.pass` (chmod 600) + **зашифрованные заметки** |

**Ничего из этого не должно попасть в git.** Все паттерны секретов ловятся .gitignore'ами в соответствующих репах.

---

## 8. Что НЕ покрыто (известные пробелы)

1. **Сам флинт упал, но healthchecks при этом не алёртит** — только если интернет провайдера лёг **одновременно с флинтом**. Маловероятно но возможно.
2. **R2 token revocation** — если случайно удалю токен в CF dashboard, backup pipeline молча начнут падать на R2 upload (локальные снапшоты остаются). Алёрта на это нет. Workaround: проверять `r2:sys-lab-home-backups/system-snapshots/` раз в месяц на наличие свежего файла.
3. **Beryl постоянно офлайн >180 дней** — снапшот в R2 будет ротейтнут (R2_RETENTION_DAYS=180). Локального снапшота на самом beryl нет (всё в эфемерном /tmp). Так что если beryl исчез на год и потом вернулся — настройки скриптами не восстановишь, надо иметь оффлайн-копию (например в этом репо). (beryl — травел-роутер, ведётся вручную.)
4. **Прошивка GL.iNet (flint2 + beryl) не бэкапится** — если флэшка испортится и прошивка слетит, надо ставить с офсайта GL.iNet, потом восстанавливать конфиги.
5. **butler-home-ai после ребута ryzen** — `depends_on` соблюдается только при `docker compose up`, а не при авто-рестарте контейнеров по policy после ребута хоста. Теоретически возможны гонки старта (как было у searcharr). Редко; лечится ручным `docker compose up -d` или `/lockdown` на время.
6. **Шифр-пароли бэкапов** (`/etc/system-backup.pass`, `~/butler-home-ai/.backup-pass`) — если потерять И хост, И зашифрованную заметку, R2-копии не расшифровать. Поэтому пароли обязательно дублируются в зашифрованных заметках.
