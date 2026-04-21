# flint2-xray-web-console

Lightweight web admin panel for an Xray/Reality VPN server running on a
GL.iNet Flint 2 (OpenWrt, aarch64). Single static Go binary, embedded UI,
basic-auth, LAN-bind by default.

See [HANDOFF.md](HANDOFF.md) for the full design and rationale.

## Status

Early scaffold. Not yet functional.

## Build

    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
        go build -trimpath -ldflags='-s -w' -o dist/xray-panel ./cmd/xray-panel

## Run locally (won't actually talk to xray unless paths exist)

    go run ./cmd/xray-panel -config ./deploy/panel.example.yaml

## Deploy to the router

    ./deploy/install.sh flint2
