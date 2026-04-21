# Handoff: Xray/Reality web console for GL.iNet Flint 2

Short brief to resume this task in a fresh Claude Code session opened from this directory.

## What the user wants

A lightweight web panel that runs on the router (GL.iNet Flint 2 / OpenWrt) next to the existing Xray + Reality VPN server. It must let the user:

- add new clients (UUIDs) and show their `vless://…` connection string + QR code
- enable / disable clients (without losing the key when disabled)
- restart / start / stop the xray service
- view error logs
- view active connections / traffic per client
- be reachable over a configurable port (LAN-only by default)

The user is running the panel on their own router; this is a personal admin tool, not a public service.

## Facts collected from the router (SSH alias: `flint2`, already configured on the Mac)

- Architecture: `aarch64` (MT7986, ARMv8)
- Xray binary: `/usr/bin/xray`, version **26.3.27** (fresh, supports Reality and the `xray x25519` / `xray api statsquery` subcommands) — installed from opkg package `xray-core 1.5.9-1` but the actual binary is upstream-current
- Xray config: `/etc/xray/config.json` (~1.3 KB)
- UCI wrapper: `/etc/config/xray` → `confdir=/etc/xray`, `conffiles=/etc/xray/config.json`, `datadir=/usr/share/xray`, `format=json`, `enabled=1`
- Init script: `/etc/init.d/xray` (procd, `USE_PROCD=1`). Restart: `/etc/init.d/xray restart`. Logs to syslog via `procd_set_param stdout/stderr 1`, but the config also writes to files.
- Xray logs (from current config): `/tmp/xray-error.log`, `/tmp/xray-access.log`, `loglevel: warning`
- Resources: 1013 MB RAM (~400 MB free), 7.2 GB overlayfs (~6.7 GB free), tmpfs 494 MB
- Tools present: `curl`. Missing: `jq`, `qrencode`, `python`/`python3`. Therefore the panel must be fully self-contained (single Go binary, QR generated in-process).
- **Stats API is NOT enabled** in the current config — no `api`, `stats`, or `policy` sections. The panel will offer an "Enable stats API" button that writes the blocks and restarts xray.

### Current config.json shape (redacted)

```json
{
  "log": { "access": "/tmp/xray-access.log", "error": "/tmp/xray-error.log", "loglevel": "warning" },
  "inbounds": [{
    "listen": "0.0.0.0",
    "port": 9443,
    "protocol": "vless",
    "settings": {
      "decryption": "none",
      "clients": [
        { "id": "<uuid-1>", "flow": "xtls-rprx-vision" },
        { "id": "<uuid-2>", "flow": "xtls-rprx-vision" },
        { "id": "<uuid-3>", "flow": "xtls-rprx-vision" }
      ]
    },
    "streamSettings": {
      "network": "tcp",
      "security": "reality",
      "realitySettings": {
        "show": false,
        "dest": "www.cloudflare.com:443",
        "xver": 0,
        "serverNames": ["www.cloudflare.com"],
        "privateKey": "<x25519-priv>",
        "shortIds": ["deadbeef"],
        "fingerprint": "chrome"
      },
      "tlsSettings": { "alpn": ["h2", "http/1.1"] }
    }
  }],
  "outbounds": [{ "protocol": "freedom", "tag": "direct" }]
}
```

Note: clients currently have no `email`/name. The panel will start attaching an `email` field (used by xray as the client identifier for stats and logs) as the human-readable name.

## Agreed design

### Stack

- **Go** (single static binary, cross-compiled `GOOS=linux GOARCH=arm64 CGO_ENABLED=0`, `-trimpath -ldflags='-s -w'`)
- Embedded web UI via `embed.FS` — vanilla HTML/JS/CSS, no framework
- Shell out to `xray x25519` and `xray api statsquery` instead of reimplementing crypto/gRPC
- QR generated server-side via `github.com/skip2/go-qrcode`
- UUID via `github.com/google/uuid`
- Password hashing via `golang.org/x/crypto/bcrypt`
- Config file parsing via `gopkg.in/yaml.v3`

### Project layout (to be created in this directory)

```
flint2-xray-web-console/
├── go.mod
├── README.md
├── cmd/xray-panel/main.go
├── internal/
│   ├── config/      # panel.yaml loader: listen, server_address, paths, bcrypt creds
│   ├── xray/        # read/write config.json, call `xray x25519`, `xray api statsquery`
│   ├── service/     # wraps `/etc/init.d/xray start|stop|restart|status`
│   ├── vless/       # builds vless://UUID@host:port?...#name URLs
│   ├── qr/          # PNG QR rendering
│   └── http/        # handlers, basic-auth middleware, LAN-bind guard
├── web/             # index.html, app.js, style.css (embedded)
└── deploy/
    ├── xray-panel.init        # procd init script for /etc/init.d/xray-panel
    ├── panel.example.yaml
    └── install.sh             # cross-compile + scp + enable + start (takes SSH host as arg)
```

### panel.yaml (example)

```yaml
listen: "192.168.1.1:8080"          # LAN-only bind; configurable port
server_address: "your.domain.or.wanip"   # what goes into vless:// URLs
xray_config: /etc/xray/config.json
xray_bin: /usr/bin/xray
xray_init: /etc/init.d/xray
log_error: /tmp/xray-error.log
log_access: /tmp/xray-access.log
stats_api: "127.0.0.1:10085"        # empty string = stats disabled
disabled_store: /etc/xray-panel/disabled.json
auth:
  username: admin
  password_bcrypt: "$2a$..."
```

### HTTP API (all JSON, basic auth)

- `GET  /api/state` — current config (with `privateKey` redacted) + derived public key + service status
- `POST /api/clients` — body `{name, flow}` — generates UUID, appends to inbound `clients`, writes config, restarts xray
- `PATCH /api/clients/:id` — rename / change flow
- `POST /api/clients/:id/disable` — move to `disabled.json`, rewrite config, restart
- `POST /api/clients/:id/enable` — move back from `disabled.json`, rewrite config, restart
- `DELETE /api/clients/:id` — remove permanently (confirm on UI)
- `GET  /api/clients/:id/link` — `vless://…` string
- `GET  /api/clients/:id/qr.png` — PNG QR for the same link
- `PATCH /api/server/reality` — update dest / serverNames / shortIds / fingerprint
- `POST /api/server/regenerate-keys` — new X25519 pair via `xray x25519`, updates config, restarts
- `POST /api/server/enable-stats` — inserts `api`, `stats`, `policy.levels.0.statsUserUplink/Downlink`, `routing` inbound tag wiring; restarts
- `GET  /api/service/status` — procd status
- `POST /api/service/{start,stop,restart}`
- `GET  /api/logs/{error,access}?tail=200` — tail of log file
- `GET  /api/activity` — parses `xray api statsquery -server 127.0.0.1:10085 -pattern "user>>>"`; falls back to `ss -tnp | grep :<port>`

### UI tabs

1. **Clients** — table of UUID/name/flow/status + Add/Rename/Enable/Disable/Delete; click row → modal with `vless://` + QR + copy button.
2. **Server** — edit dest, serverNames, shortIds (add/remove, auto-gen 8-hex), fingerprint; buttons "Regenerate X25519 keypair" and "Enable stats API".
3. **Logs** — last N lines of error / access with auto-refresh toggle.
4. **Activity** — per-client uplink/downlink, simple online indicator by counter delta.
5. **Service** — running/stopped + Start/Stop/Restart.

### Security

- Basic auth (bcrypt); first-run helper prints a temporary password if none set.
- On startup, panel enumerates local interfaces; if `listen` address is on the WAN interface, refuse to start with a clear error. Default bind is the LAN IP (`192.168.1.1`).
- Atomic config writes: write `config.json.tmp`, `rename`; keep a `config.json.bak` of the previous version. Never call xray restart before verifying the new JSON parses.
- Disabled clients kept in `/etc/xray-panel/disabled.json` (same shape as inbound `clients[]`), not in the live config — xray never sees the UUID while disabled, but re-enabling restores the same key.

### Deploy flow

1. `deploy/install.sh flint2` on the Mac:
   - cross-compile `xray-panel`
   - scp binary → `/usr/bin/xray-panel`
   - scp init → `/etc/init.d/xray-panel`, `chmod +x`
   - if `/etc/xray-panel/panel.yaml` absent: scp example, prompt user to edit
   - `/etc/init.d/xray-panel enable && /etc/init.d/xray-panel restart`
2. User opens `http://<lan-ip>:<port>` from LAN, logs in with basic auth.

## Where we stopped

Plan is fully agreed with the user. User moved the work into this dedicated repo folder (`flint2-xray-web-console/`) and will continue in a new session opened from here, to keep the unrelated Joblio backend context clean.

**Next step when resumed:** `git init` (if not done), create the layout above, start with `cmd/xray-panel/main.go`, `internal/config`, and the `internal/xray` config-reader/writer. Keep commits small and scoped per subsystem.

## User preferences observed in this session

- Communicates in Russian; wants responses in Russian.
- Wants the port (and, by extension, other paths) configurable from a config file, not hardcoded.
- Concerned about exposing private keys — keep them out of user-facing output; store only on the router.
- Has SSH alias `flint2` set up on the Mac — use it directly for deploy and introspection.
