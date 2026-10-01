# Install the server

You need a machine with a public address and a domain name that points at it (for HTTPS). A small VPS is plenty.

## Ports to open

| Port | Used for |
|---|---|
| TCP 80, 443 | Web console, control API, WebSocket relay, certificate issuance |
| UDP 443 | HTTP/3 |
| UDP 3478 | STUN (helps devices find a direct path) |
| UDP 3479 | UDP relay (faster than the WebSocket relay; optional) |
| UDP 51820 | WireGuard gateway (only if you use standard WireGuard apps) |

## Quick start (binary)

```sh
sudo gorget-server init      # asks 5 questions, writes /etc/gorget/config.yaml, checks the machine
sudo gorget-server install   # installs and starts the service (systemd, Windows service or launchd)
```

The service log then shows a box with a setup link, for example
`https://vpn.example.com/setup#token=…`. Open it, and the token is filled in for you; create the
owner account and you're done. Logs: `journalctl -u gorget-server` (Linux),
`/usr/local/var/log/gorget-server.err.log` (macOS), Event Viewer (Windows). The token is also saved in
`<data_dir>/setup-token` until setup is finished.

Something not working? `sudo gorget-server check` explains what's wrong (DNS record, a port already in
use, missing nftables, firewall) and how to fix it.

## Docker

```sh
cd deploy/docker
# edit GORGET_PUBLIC_URL and GORGET_TLS_EMAIL in docker-compose.yml, point DNS at the server
docker compose up -d
docker compose logs gorget     # shows the setup link with its token
```

Other layouts: `docker-compose.postgres.yml` (PostgreSQL), `docker-compose.caddy.yml` and `deploy/proxy/nginx.conf` (behind a reverse proxy with `tls.mode: off`).

## Building from source

```sh
make            # builds the web console and bin/gorget-server
sudo ./bin/gorget-server init
sudo ./bin/gorget-server install
```

Every setting can also be given as an environment variable (`GORGET_PUBLIC_URL`, `GORGET_DB_DRIVER`, …) or a command-line flag. `gorget-server config-example` prints every option with comments.

## Where things live

- **Database:** SQLite by default (`<data_dir>/gorget.db`), PostgreSQL for large or clustered setups.
- **Master key:** `<data_dir>/master.key` encrypts secrets in the database. Back it up with your database backups: you cannot restore without it.
- **Backups:** encrypted JSON written daily to `<data_dir>/backups` (see [backups](../ops/backups.md)).

## Platforms

The control plane, relay and STUN server run on Linux, Windows and macOS. The WireGuard gateway for *standard* WireGuard apps runs on Linux (nftables) and macOS (pf); Windows has no firewall that can enforce per-flow rules, so the gateway is off there. Gorget apps do not need the gateway.
