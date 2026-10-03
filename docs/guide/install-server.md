# Install the server

The server is one program, `gorget-server`. It runs the web console, the control API, the
relay, the STUN server and (on Linux and macOS) the gateway for standard WireGuard apps. Pick
**one** method below; all of them end the same way, with a setup link you open in a browser.

## Before you start

1. **A machine** with a public IP address: a small VPS is plenty (1 CPU, 1 GB RAM).
2. **A domain name** with an **A record** (and AAAA, if the machine has IPv6) pointing at that
   machine, for example `vpn.example.com`. Check it with `nslookup vpn.example.com`.
3. **Open ports** in the machine's firewall *and* in your cloud provider's security group:

| Port | Used for | Needed? |
|---|---|---|
| TCP 80 | Free HTTPS certificate (Let's Encrypt) and redirects | Yes, with the built-in HTTPS |
| TCP 443 | Web console, apps, WebSocket relay | Yes |
| UDP 443 | HTTP/3 | Optional |
| UDP 3478 | STUN: helps devices find a direct path | Recommended |
| UDP 3479 | UDP relay: faster than the WebSocket relay | Recommended |
| UDP 51820 | WireGuard gateway | Only if you use standard WireGuard apps or routers |

## Which method?

| You have… | Use |
|---|---|
| A Linux machine and want the fewest steps | [**A. Installer script**](#a-installer-script-linux-recommended) |
| Docker already | [**B. Docker**](#b-docker) |
| Caddy, Nginx or Traefik already serving HTTPS | [**C. Behind a reverse proxy**](#c-behind-a-reverse-proxy) |
| Windows or macOS as the server | [**D. Manual binary**](#d-manual-binary-any-system) |
| Kubernetes | [Helm chart](../ops/kubernetes.md) |
| Several servers sharing one database | [High availability](../CLUSTER.md) |

## A. Installer script (Linux, recommended)

```sh
curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/install-server.sh | sh
```

It asks for your domain name (and an optional email for certificate notices), then downloads the
latest release, **checks it against the release checksums**, installs it as a systemd service,
starts it and prints the setup link. To skip the questions:

```sh
curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/install-server.sh \
  | sh -s -- --domain vpn.example.com --email you@example.com --yes
```

| Option | Meaning |
|---|---|
| `--domain NAME` | The server's domain name |
| `--email ADDRESS` | Email for Let's Encrypt expiry notices |
| `--version vX.Y.Z` | A specific release instead of the latest |
| `--docker` | Run in Docker instead (method B) |
| `--yes` | Never ask; use the defaults |
| `--uninstall` | Remove the service (data stays) |

**Upgrade:** run the same command again. It keeps your configuration and data, replaces the
program and restarts it. Read the script first if you prefer:
`curl -fsSLo install-server.sh https://raw.githubusercontent.com/anand34577/gorget/main/install-server.sh`.

## B. Docker

You need Docker with the Compose plugin (`docker compose version` works).

**The short way:** the installer does everything below for you:

```sh
curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/install-server.sh | sh -s -- --docker
```

**By hand:**

```sh
mkdir gorget && cd gorget
curl -fsSLO https://raw.githubusercontent.com/anand34577/gorget/main/deploy/docker/docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/deploy/docker/.env.example -o .env
nano .env                                   # set GORGET_DOMAIN=vpn.example.com (and GORGET_EMAIL)
docker compose up -d
docker compose exec gorget gorget-server setup-link     # prints the link to open
```

All data lives in the `gorget-data` volume (database, `master.key`, backups). To upgrade:
`docker compose pull && docker compose up -d`. To use PostgreSQL instead of SQLite, download
`docker-compose.postgres.yml` as well, set `POSTGRES_PASSWORD` in `.env`, and add
`-f docker-compose.postgres.yml` to each command.

## C. Behind a reverse proxy

Use this when something else already owns ports 80 and 443. Gorget listens on plain HTTP at
`127.0.0.1:8080` and the proxy handles HTTPS.

- **Caddy with Docker:** from a clone of the repository, `cd deploy/docker`, copy `.env.example`
  to `.env`, set `GORGET_DOMAIN`, and run `docker compose -f docker-compose.caddy.yml up -d`.
- **Any proxy:** run `gorget-server init`, answer **2** to "How should HTTPS work?", then point
  your proxy at `http://127.0.0.1:8080`. Ready-made [Caddyfile](https://github.com/anand34577/gorget/blob/main/deploy/proxy/Caddyfile)
  and [nginx.conf](https://github.com/anand34577/gorget/blob/main/deploy/proxy/nginx.conf) are in `deploy/proxy`. The proxy must
  allow long-lived connections (streaming and WebSockets) and pass `X-Forwarded-For`.

UDP services (STUN, relay, WireGuard) cannot pass through an HTTP proxy: open UDP 3478, 3479 and
51820 on the machine itself.

## D. Manual binary (any system)

Download `gorget-server` for your system from the [latest release](https://github.com/anand34577/gorget/releases/latest):
`gorget-server_<version>_linux_amd64`, `_linux_arm64`, `_darwin_arm64`, `_windows_amd64.exe` and so on.
Then, as root (Linux, macOS) or in an **Administrator** terminal (Windows):

```sh
sudo install -m 755 gorget-server_*_linux_amd64 /usr/local/bin/gorget-server    # Linux / macOS
sudo gorget-server init       # five questions: domain, HTTPS, database, gateway, data folder
sudo gorget-server install    # installs the service, starts it, prints the setup link
```

```powershell
# Windows (Administrator PowerShell, in the folder with the download)
.\gorget-server_<version>_windows_amd64.exe init
.\gorget-server_<version>_windows_amd64.exe install
```

`init` also checks the machine (DNS record, free ports, nftables) and tells you what to fix.
To skip the questions: `gorget-server init -yes -domain vpn.example.com -email you@example.com`.

**From source:** `make` builds the web console and `bin/gorget-server` (needs Go and Node.js),
then continue with `init` as above.

## After the install

1. **Open the setup link** the installer printed and create the owner account. Lost it? Run
   `sudo gorget-server setup-link` (Docker: `docker compose exec gorget gorget-server setup-link`).
2. **Turn on two-factor sign-in** under *Account & security*.
3. **Back up `master.key`** (in the data folder, see below). Without it a backup cannot be restored.
4. **Check the server** any time with `sudo gorget-server check`.
5. **Add devices**: [Install the apps](install-clients.md).

## Where things live

| | Linux | Windows | macOS | Docker |
|---|---|---|---|---|
| Configuration | `/etc/gorget/config.yaml` | `%ProgramData%\Gorget\config.yaml` | `/etc/gorget/config.yaml` | environment in `docker-compose.yml` and `.env` |
| Data (database, `master.key`, backups) | `/var/lib/gorget` | `%ProgramData%\Gorget` | `/var/lib/gorget` | volume `gorget-data` |
| Logs | `journalctl -u gorget-server` | Event Viewer, *Application* | `/usr/local/var/log/gorget-server.err.log` | `docker compose logs gorget` |
| Service | `systemctl status gorget-server` | `Gorget VPN Server` in Services | `launchctl list \| grep gorget` | `docker compose ps` |

Everything you can set is listed in the [configuration reference](configuration.md).

## Uninstall

- **Installer script:** run it with `--uninstall` (add `--docker` for Docker). Your configuration and data stay until you delete them.
- **Manual:** `sudo gorget-server uninstall`, then delete the binary.

## Platform notes

The control plane, relay and STUN server run on Linux, Windows and macOS. The WireGuard gateway
for *standard* WireGuard apps runs on Linux (nftables) and macOS (pf). Windows has no firewall that
can enforce per-flow rules, so the gateway is off there; Gorget's own apps do not need it.
