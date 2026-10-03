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

## Get started

You need a Linux machine with a public IP address (a small VPS is plenty), a domain name pointing at it (an **A record**, e.g. `vpn.example.com`), and these ports open: **TCP 80, 443** and **UDP 443, 3478, 3479, 51820**.

**1. Install the server** (on that machine):

```sh
curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/install-server.sh | sh
```

It asks for your domain, installs and starts the service, and prints a **setup link**. Add `--docker` to run it in Docker instead. Open the link and create the owner account.

**2. Connect your devices**, with the address of your server:

| Device | |
|---|---|
| Linux, macOS | `curl -fsSL https://vpn.example.com/install.sh \| sh` |
| Windows (PowerShell) | `irm https://vpn.example.com/install.ps1 \| iex` |
| Android | install the APK, enter `vpn.example.com` |
| Router, NAS, any WireGuard app | **WireGuard apps → Add** in the console |

**3. Check it:** `gorget status` on a device lists the others; `sudo gorget-server check` on the server explains anything that is wrong.

The full guides are in the **[wiki](https://github.com/anand34577/gorget/wiki)** (generated from [docs/](docs/), so edit the files there):

| I want to… | Read |
|---|---|
| Be up in 15 minutes | [Quick start](docs/guide/quickstart.md) |
| Choose between script, Docker, reverse proxy, Kubernetes | [Install the server](docs/guide/install-server.md) |
| Put apps on laptops, phones, servers | [Install the apps](docs/guide/install-clients.md) |
| Reach my home network and VLANs from anywhere | [Your home network, from anywhere](docs/guide/homelab.md) |
| Change a setting | [Configuration reference](docs/guide/configuration.md) |
| Fix something | [Troubleshooting](docs/ops/troubleshooting.md) |
| Upgrade, back up or move the server | [Backups and upgrades](docs/ops/backups.md) |

### Other ways to run the server

- **Docker:** `curl -fsSL …/install-server.sh | sh -s -- --docker`, or by hand with the files in [`deploy/docker`](deploy/docker) (copy `.env.example` to `.env`, set `GORGET_DOMAIN`, `docker compose up -d`). Variants for [PostgreSQL](deploy/docker/docker-compose.postgres.yml) and [Caddy](deploy/docker/docker-compose.caddy.yml); an [Nginx](deploy/proxy/nginx.conf) example.
- **Binary:** download `gorget-server` from the [releases](https://github.com/anand34577/gorget/releases/latest), then `sudo gorget-server init` and `sudo gorget-server install` (Linux, macOS, Windows).
- **Kubernetes:** the [Helm chart](docs/ops/kubernetes.md).
- **From source:** `make`, then as above. For local development without TLS: `make run`, open http://localhost:8080 (the setup token is printed in the log).

Every setting is a YAML key *and* an environment variable (`GORGET_PUBLIC_URL`, `GORGET_DB_DRIVER`, …): see the [configuration reference](docs/guide/configuration.md).

### HTTPS modes

| `tls.mode` | Use when |
|---|---|
| `acme` (default) | Public domain pointing at the server. Let's Encrypt, auto-renewed. |
| `acme-dns` | Wildcards or servers not reachable on 80/443 (DNS-01 with Cloudflare, DigitalOcean, Hetzner, deSEC, RFC 2136, Route 53). |
| `custom` | Your own certificate files (hot-reloaded). |
| `internal-ca` | LAN, air-gapped or IP-only installs: Gorget runs its own CA (`/ca.crt`). |
| `off` | Behind a reverse proxy that handles HTTPS. Set `http.trusted_proxies`. |

## Operations

| Command | |
|---|---|
| `gorget-server check` | Finds what's wrong: DNS record, ports, missing packages |
| `gorget-server setup-link` | Print the first-run setup link again |
| `gorget-server backup -out file.gbk` | Encrypted, database-independent backup (scheduled daily by default) |
| `gorget-server restore -in file.gbk` | Restore into an empty database |
| `gorget-server migrate-db -to postgres://…` | Move from SQLite to PostgreSQL |
| `gorget-server reset-password -email you@x` | Recover a locked-out account |

To upgrade, run the install command again. **Back up `master.key`** (in the data directory, or set `GORGET_MASTER_KEY`): it encrypts secrets at rest and backups, and without it they cannot be restored.

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
