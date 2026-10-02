# Gorget

A self-hosted, open-source WireGuard® mesh VPN. You run the server; nothing ever leaves it.

- **Mesh or full tunnel.** Devices talk to each other directly (only private traffic uses the VPN), or route all internet traffic through an exit node.
- **Your home or office network, from anywhere.** One device there shares its LAN and VLANs, and everyone allowed reaches those machines by their usual IP addresses. A router running plain WireGuard (OpenWrt and others) can do the job too.
- **Works with any WireGuard app.** The built-in gateway gives the official WireGuard apps, routers and NAS boxes a QR code, a config file or an OpenWrt setup script.
- **Default-deny access rules** with groups, tags, ports, time limits, tests and a simulator.
- **DNS** for every device (`laptop.gorget.internal`), custom records and wildcards (`*.apps.home.lan`), split DNS, DNS-over-HTTPS/TLS upstreams.
- **See what's happening.** Charts of devices online and traffic, a connection history with public addresses and countries, and one-click blocking.
- **Email** for new devices waiting for approval, logins from a new country, blocked devices and locked accounts, plus invitations and password resets through your own SMTP provider.
- **Device health rules:** minimum app and OS versions, disk encryption, firewall, allowed operating systems, allowed or blocked countries.
- **Built-in HTTPS** with automatic Let's Encrypt certificates (HTTP-01, TLS-ALPN-01, DNS-01), HTTP/2 and HTTP/3.
- **Temporary access, post-quantum keys, SCIM, Terraform, Helm and high availability** for teams that need them.
- **Zero telemetry.** No analytics, no phone-home, no external fonts or CDNs.

> Status: the server, web console, Android app and desktop clients (Linux, Windows, macOS: `gorget` service/CLI plus a tray app) are written; real-device testing is in progress. See [docs/STATUS.md](docs/STATUS.md).
>
> Documentation: the [wiki](https://github.com/anand34577/gorget/wiki) has every guide. It is generated from [docs/](docs/), so edit the files there.

> New here? [Your home network, from anywhere](docs/guide/homelab.md) walks through a VPS server, a home LAN with VLANs, laptops, phones and family members step by step.

## Quick start (Docker)

```sh
cd deploy/docker
# edit GORGET_PUBLIC_URL and GORGET_TLS_EMAIL in docker-compose.yml, point DNS at the server
docker compose up -d
docker compose logs gorget     # prints a setup link with the token in it
```

Open the setup link and create the owner account. Then, on any Linux or Mac device:

```sh
curl -fsSL https://vpn.example.com/install.sh | sh
```
 Open these ports: TCP 80 + 443, UDP 443 (HTTP/3), UDP 3478 (STUN), UDP 3479 (UDP relay), UDP 51820 (WireGuard gateway).

Other layouts: [`docker-compose.postgres.yml`](deploy/docker/docker-compose.postgres.yml), behind [Caddy](deploy/docker/docker-compose.caddy.yml) or [Nginx](deploy/proxy/nginx.conf).

## Quick start (binary / service)

```sh
make                               # builds the web console and bin/gorget-server (or download a release)
sudo ./bin/gorget-server init      # five questions, writes /etc/gorget/config.yaml, checks DNS and ports
sudo ./bin/gorget-server install   # installs and starts the service (systemd, Windows service or launchd)
```

`gorget-server check` explains what's wrong when something doesn't work (DNS record, a port in use, missing nftables).

Local development without TLS: `make run`, then open http://localhost:8080 (the setup token is printed in the log).

Every setting is also available as an environment variable, e.g. `GORGET_PUBLIC_URL`, `GORGET_DB_DRIVER=postgres`, `GORGET_DB_DSN=postgres://…`, `GORGET_TLS_MODE=acme-dns`, `GORGET_TLS_DNS_PROVIDER=cloudflare`.

### TLS modes

| `tls.mode` | Use when |
|---|---|
| `acme` (default) | Public domain pointing at the server. Let's Encrypt via HTTP-01 / TLS-ALPN-01, auto-renewed. Optional ZeroSSL fallback (`tls.zerossl_api_key`). |
| `acme-dns` | Wildcards or servers not reachable on 80/443. DNS-01 via `cloudflare`, `digitalocean`, `hetzner`, `desec`, `rfc2136`, `route53` (`tls.dns_config`). |
| `custom` | Your own certificate files (hot-reloaded on change). |
| `internal-ca` | LAN / air-gapped / IP-only installs. Gorget runs its own CA; download it from `/ca.crt`. |
| `off` | Behind a TLS-terminating reverse proxy. Set `http.trusted_proxies`. |

## Operations

| Command | |
|---|---|
| `gorget-server backup -out file.gbk` | Encrypted, database-independent backup (scheduled daily by default) |
| `gorget-server restore -in file.gbk` | Restore into an empty database |
| `gorget-server migrate-db -to postgres://…` | Move from SQLite to PostgreSQL |
| `gorget-server reset-password -email you@x` | Recover a locked-out account |
| `gorget-server healthcheck` | Container health probe |

**Back up `master.key`** (in the data directory, or set `GORGET_MASTER_KEY`). It encrypts secrets at rest and backups; without it they cannot be restored.

Prometheus metrics are opt-in on a separate listener (`metrics.enabled`, default `127.0.0.1:9090`). `/healthz` and `/readyz` are always available.

## Architecture

```
                    ┌───────────────── gorget-server (single Go binary) ─────────────────┐
 browser ──HTTPS──▶ │ web server (TLS/ACME, HTTP/2+3) ─┬─ /            React console     │
                    │                                  ├─ /api/v1      REST admin API     │
 Gorget app ──────▶ │                                  ├─ /gorget.v1.ControlService       │
                    │                                  │               ConnectRPC client API│
                    │                                  └─ /relay       WebSocket relay    │
                    │ coordinator: DB + policy ─▶ per-device network maps (streamed diffs)│
 WireGuard app ─UDP▶│ gateway: WireGuard + nftables ACL + NAT + DNS resolver (Linux)      │
 any client ───UDP▶ │ STUN :3478                                                          │
                    │ SQLite (default) / PostgreSQL                                       │
                    └─────────────────────────────────────────────────────────────────────┘
```

| Path | |
|---|---|
| `proto/gorget/v1` | Client ↔ server protocol (Protobuf, ConnectRPC) |
| `internal/core` | Business logic, coordinator, network-map builder |
| `internal/policy` | Access-rule engine (HuJSON policies, tests, simulator) |
| `internal/ipam` | Customisable address plan, pools, re-addressing |
| `internal/gateway` | WireGuard gateway: kernel or wireguard-go, nftables |
| `internal/auth` | Passwords (Argon2id), sessions, TOTP, passkeys, OIDC, API tokens |
| `internal/rpcserver` | Device registration, challenge auth, map streaming, signalling |
| `internal/relay`, `internal/stun`, `internal/dnsserver` | NAT traversal and DNS |
| `internal/web` | Built-in web server and TLS manager |
| `web/` | React + TypeScript + Tailwind console (embedded into the binary) |

## Security model

- Device private keys are generated on the device; WireGuard app keys are generated in the browser. The server never stores private keys.
- Devices prove possession of an Ed25519 machine key (challenge/response) and get short-lived session tokens.
- Secrets at rest (PSKs, OIDC secrets, webhook secrets, the SMTP password, gateway key) are encrypted with XChaCha20-Poly1305 under the master key.
- Admin actions are recorded in a hash-chained audit log; the console verifies the chain.
- First-run setup requires a one-time token from the server log, so nobody can claim a fresh server before you.

## License

[AGPL-3.0](LICENSE). WireGuard is a registered trademark of Jason A. Donenfeld.
