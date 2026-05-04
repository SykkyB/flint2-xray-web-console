# Home infrastructure — monitoring & backups

Документация всей домашней лаборатории: что мониторится, какими источниками,
куда шлются алёрты, что бэкапится, куда и как восстанавливаться.

---

## 1. Хосты

| Hostname | Role | Subnet | OS | SSH alias |
|----------|------|--------|----|-----------| 
| **flint2** | основной роутер, gateway, XRAY-сервер | 192.168.100.1 | OpenWrt 21.02 (GL-MT6000) | `flint2` |
| **ryzen4700** | домашний сервер, Immich + бот, cloudflared | 192.168.100.5 | Ubuntu 24.04 | `ryzen4700` |
| **beryl** | travel-роутер, sing-box VPN-клиент | 192.168.200.1 | OpenWrt 21.02 (GL-MT3000) | `beryl` |

Доменные имена (через DNS sys-lab.xyz):
- `immich.sys-lab.xyz:8443` → ryzen4700, через XRAY на flint2
- `vpn.sys-lab.xyz:8443` → flint2, XRAY VLESS+Reality для VPN-клиентов
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
- **Что:** `docker inspect` для whitelist'а: immich_server, immich_machine_learning, immich_postgres, immich_redis, vanilla-sky-monitor, vanilla-sky-redirect, cloudflared
- **Триггер:** контейнер не в state=running ИЛИ Health=unhealthy три раза подряд
- **Лог:** `/home/sykkyb/watchdog/watchdog.log` (только фейлы и переходы)
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

## 3. Бэкапы — 3 независимых pipeline

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
- `/root/`
- `/usr/sbin/xray-panel-backup`, `/usr/sbin/system-config-backup`
- `/mnt/sda1/watchdog/{watchdog.sh,config.env}`
- crontab snapshot

**Что бэкапится с ryzen (через SSH ключ flint→ryzen):**
- Compose-стеки: `/srv/{immich,cloudflared,vanilla-sky}/docker-compose.yml` + `.env` + `config.yml`
- `/etc/caddy/Caddyfile`, `/etc/rsnapshot.conf`, `/etc/cron.d/`, `/etc/crontab`, `/etc/ssh/sshd_config`
- `/home/sykkyb/.bashrc`, `.profile`, `.msmtprc`, `.ssh/`, `watchdog/`
- `/usr/local/bin/{rsnapshot-safe,system-config-readroot}`
- crontab snapshot, `docker ps -a`, `docker volume ls`, `docker network ls`

**Что бэкапится с ryzen через root (NOPASSWD sudo на specific helper):**
- `/etc/netplan/`, `/etc/ufw/`, `/etc/systemd/system/caddy.service.d/`, `/root/.ssh/authorized_keys`
- Помощник: `/usr/local/bin/system-config-readroot` (хардкод-paths, only this script in sudoers)
- sudoers entry: `/etc/sudoers.d/system-config-backup`

**Что НЕ бэкапится (намеренно):**
- `/srv/immich/library/` — фотки, уже под rsnapshot в `/mnt/sda1/immich-backup/`
- `/var/lib/docker/` — данные контейнеров (восстанавливаются compose+env+rsnapshot)

**Hash-skip:** скрипт SHA256-хэширует все исходники + сравнивает с `state.sha256`. Если ничего не менялось, выходит за миллисекунды без архивации/upload.

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

**Что бэкапится:**
- `/usr/bin/sing-box`, `/usr/bin/xray-panel-cli`
- `/etc/sing-box/`, `/etc/config/sing-box`, `/etc/xray-panel-cli/`
- `/etc/init.d/sing-box`, `/etc/init.d/xray-panel-cli`
- `/etc/hotplug.d/button/50-sing-box-switch`
- `/etc/sysctl.d/99-disable-mptcp.conf`
- `/www/xray-panel-launcher.js`, `/www/gl_home.html`, `/www/gl_home.html.bak` (GL.iNet UI launcher injection)

**Поведение:**
| Состояние beryl | Что произойдёт |
|-----------------|----------------|
| дома, без изменений | exit за <2 сек, нулевой трафик |
| в дороге, есть интернет, есть изменения | wget rclone (25 MB) → tar (~16 MB) → upload → trap чистит /tmp |
| в дороге, нет интернета | wget fail, лог "rclone download failed", state не обновляется → retry в следующее воскресенье |
| выключен | cron не сработает, отработает в следующий запуск |

### Pipeline 3 — beryl manual from Mac (`scripts/backup.sh`)

**Расположение:** `~/Documents/projects/beryl-xray-web-console/scripts/backup.sh`

**Когда использовать:** beryl дома + хочется guarantee'ать снапшот прямо сейчас (например после изменений в xray-panel-cli или конфигах). Или если cron на beryl был отключён.

**Запуск:**
```bash
cd ~/Documents/projects/beryl-xray-web-console
scripts/backup.sh           # цель beryl
scripts/backup.sh other     # цель custom SSH alias
```

Делает то же что Pipeline 2, но запускается с Mac → SCP файлов → tar.gz в `<repo>/backups/` (gitignored). Дополнительно мирорит в R2 (`r2:sys-lab-home-backups/beryl-snapshots/`).

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
│   ├── system-config-2026-05-04_2320.tar.gz             (~50 KB each)
│   └── system-config-2026-05-05_0430.tar.gz
│
├── beryl-snapshots/                                      ← Pipeline 2+3
│   ├── beryl-self-20260504-194152Z.tar.gz               (~16 MB, beryl-self)
│   └── beryl-20260504-193555Z.tar.gz                    (~16 MB, Mac-side)
│
└── snapshots/                                            ← разовый ad-hoc
    └── watchdog-config-2026-05-04_2215.tar.gz           (исторический)
```

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
# 1. С Mac скачиваем последний снапшот
rclone copy r2:sys-lab-home-backups/system-snapshots/ /tmp/restore/ --max-age 7d

# 2. Заливаем на флинт
scp -O /tmp/restore/system-config-*.tar.gz flint2:/tmp/

# 3. На флинте распаковываем (только flint2-часть)
ssh flint2 'cd /tmp && tar -xzf system-config-*.tar.gz; \
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
- xray-panel + xray-binary — отдельный pipeline `xray-panel-backup` (snapshots в `/mnt/sda1/xray-backup/snapshots/`, тоже в R2 не уходит — отдельная история)

### 5.2 Восстановление ryzen4700

```bash
# 1. С Mac скачиваем последний снапшот
rclone copy r2:sys-lab-home-backups/system-snapshots/ /tmp/restore/ --max-age 7d
cd /tmp/restore && tar -xzf system-config-*.tar.gz

# 2. Compose-стеки → /srv (нужен root)
scp -r ryzen4700/srv/* ryzen4700:/tmp/srv-restore/
ssh ryzen4700 'sudo cp -a /tmp/srv-restore/* /srv/ && sudo chown -R sykkyb:docker /srv/immich/'

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
cd ~/Documents/projects/beryl-xray-web-console && scripts/backup.sh
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

**Ничего из этого не должно попасть в git.** Все паттерны секретов ловятся .gitignore'ами в соответствующих репах.

---

## 8. Что НЕ покрыто (известные пробелы)

1. **Сам флинт упал, но healthchecks при этом не алёртит** — только если интернет провайдера лёг **одновременно с флинтом**. Маловероятно но возможно.
2. **R2 token revocation** — если случайно удалю токен в CF dashboard, все три backup pipeline молча начнут падать на R2 upload (локальные снапшоты остаются). Алёрта на это нет. Workaround: проверять `r2:sys-lab-home-backups/system-snapshots/` раз в месяц на наличие свежего файла.
3. **Beryl постоянно офлайн >180 дней** — снапшот в R2 будет ротейтнут (R2_RETENTION_DAYS=180). Локального снапшота на самом beryl нет (всё в эфемерном /tmp). Так что если beryl исчез на год и потом вернулся — настройки скриптами не восстановишь, надо иметь оффлайн-копию (например в этом репо).
4. **Прошивка GL.iNet (flint2 + beryl) не бэкапится** — если флэшка испортится и прошивка слетит, надо ставить с офсайта GL.iNet, потом восстанавливать конфиги.
