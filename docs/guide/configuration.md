# Configuration reference

Most people never edit the configuration: `gorget-server init` writes a small file with the answers
to five questions, and every other setting keeps a sensible default. Day-to-day settings (access
rules, DNS, email, device health) live in the web console, not here.

## How it is read

Settings come from three places. Later ones win:

1. **The configuration file** (YAML): `/etc/gorget/config.yaml` on Linux and macOS,
   `%ProgramData%\Gorget\config.yaml` on Windows. Use `-config path` or `GORGET_CONFIG` for another location.
2. **Environment variables** named `GORGET_` plus the name in the tables below. This is how Docker is configured.
3. **Command-line flags** such as `-public-url`, `-data-dir`, `-listen`, `-tls-mode`, `-db`, `-dsn`.

Useful commands:

```sh
gorget-server config-example     # prints every option with comments
gorget-server check              # validates the configuration and the machine
sudo gorget-server restart       # apply changes to the installed service
```

Lists (such as `trusted_proxies`) are comma-separated in environment variables:
`GORGET_TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12`. Durations look like `12h` or `30m`.

## Minimal examples

**Built-in HTTPS (the default):**

```yaml
public_url: https://vpn.example.com
tls:
  mode: acme
  email: you@example.com
```

**Behind a reverse proxy:**

```yaml
public_url: https://vpn.example.com
tls: { mode: "off" }
http:
  listen: 127.0.0.1:8080
  redirect_listen: ""
  http3: false
  trusted_proxies: ["127.0.0.1/32", "::1/128"]
```

**PostgreSQL instead of SQLite:**

```yaml
database:
  driver: postgres
  dsn: postgres://gorget:password@db.internal:5432/gorget?sslmode=require
```

**Wildcard certificate or a server not reachable on ports 80/443 (DNS-01 with Cloudflare):**

```yaml
tls:
  mode: acme-dns
  dns_provider: cloudflare
  dns_config:
    api_token: "…"
```

## Everything you can set

### General

| File key | Environment variable | Default | Meaning |
|---|---|---|---|
| `public_url` | `GORGET_PUBLIC_URL` | *(required)* | The address people and apps use, e.g. `https://vpn.example.com`. |
| `data_dir` | `GORGET_DATA_DIR` | `/var/lib/gorget` (Windows: `%ProgramData%\Gorget`) | Database, `master.key`, certificates, backups. |
| `log_level` | `GORGET_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `log_format` | `GORGET_LOG_FORMAT` | `text` | `text` or `json`. |

### Web server (`http`)

| File key | Environment variable | Default | Meaning |
|---|---|---|---|
| `http.listen` | `GORGET_HTTP_LISTEN` | `:443` | HTTPS address (plain HTTP when `tls.mode` is `off`). |
| `http.redirect_listen` | `GORGET_HTTP_REDIRECT_LISTEN` | `:80` | Redirects and Let's Encrypt checks. Empty disables it. |
| `http.http3` | `GORGET_HTTP3` | `true` | HTTP/3 on UDP 443. |
| `http.trusted_proxies` | `GORGET_TRUSTED_PROXIES` | none | Proxy addresses whose `X-Forwarded-For` is trusted. |
| `http.access_log` | `GORGET_ACCESS_LOG` | `false` | Log every request. |
| `http.admin_allow_cidrs` | `GORGET_ADMIN_ALLOW_CIDRS` | none | Only these networks may open the admin console. |

### HTTPS certificates (`tls`)

See [HTTPS certificates](../ops/tls.md) for when to use which mode.

| File key | Environment variable | Default | Meaning |
|---|---|---|---|
| `tls.mode` | `GORGET_TLS_MODE` | `acme` | `acme`, `acme-dns`, `custom`, `internal-ca` or `off`. |
| `tls.domains` | `GORGET_TLS_DOMAINS` | the host of `public_url` | Names on the certificate. |
| `tls.email` | `GORGET_TLS_EMAIL` | none | Let's Encrypt expiry notices. |
| `tls.staging` | `GORGET_TLS_STAGING` | `false` | Use Let's Encrypt's test servers while experimenting. |
| `tls.cert_file`, `tls.key_file` | `GORGET_TLS_CERT_FILE`, `GORGET_TLS_KEY_FILE` | none | Your own certificate (`custom`). |
| `tls.fallback_ca` | `GORGET_TLS_FALLBACK_CA` | `zerossl` | Backup issuer; empty disables it. |
| `tls.zerossl_api_key` | `GORGET_TLS_ZEROSSL_API_KEY` | none | Enables the ZeroSSL fallback. |
| `tls.acme_directory` | `GORGET_TLS_ACME_DIRECTORY` | Let's Encrypt | A private ACME CA such as step-ca. |
| `tls.eab_key_id`, `tls.eab_mac_key` | `GORGET_TLS_EAB_KEY_ID`, `GORGET_TLS_EAB_MAC_KEY` | none | External account binding for ACME CAs that need it. |
| `tls.dns_provider` | `GORGET_TLS_DNS_PROVIDER` | none | `cloudflare`, `digitalocean`, `hetzner`, `desec`, `route53`, `rfc2136` (`acme-dns`). |
| `tls.dns_config` | *(file only)* | none | The provider's credentials, e.g. `api_token`. |

### Database (`database`)

| File key | Environment variable | Default | Meaning |
|---|---|---|---|
| `database.driver` | `GORGET_DB_DRIVER` | `sqlite` | `sqlite` or `postgres`. |
| `database.dsn` | `GORGET_DB_DSN` | `<data_dir>/gorget.db` | SQLite file, or `postgres://user:password@host/db`. |
| `database.max_open_conns` | `GORGET_DB_MAX_OPEN_CONNS` | `20` | Connection pool size. |

### Connecting devices (`stun`, `relay`)

| File key | Environment variable | Default | Meaning |
|---|---|---|---|
| `stun.enabled` | `GORGET_STUN_ENABLED` | `true` | Run the STUN server. |
| `stun.listen` | `GORGET_STUN_LISTEN` | `:3478` | STUN address (UDP). |
| `stun.advertise` | `GORGET_STUN_ADVERTISE` | `<host>:3478` | Public `host:port` told to devices. |
| `relay.enabled` | `GORGET_RELAY_ENABLED` | `true` | Relay for devices that can't connect directly. |
| `relay.region` | `GORGET_RELAY_REGION` | `default` | Name shown for this relay. |
| `relay.udp_listen` | `GORGET_RELAY_UDP_LISTEN` | `:3479` | UDP relay address. Empty disables it (the WebSocket relay still works). |
| `relay.udp_advertise` | `GORGET_RELAY_UDP_ADVERTISE` | `<host>:3479` | Public `host:port` of the UDP relay. |
| `relay.rate_limit_bytes` | `GORGET_RELAY_RATE_LIMIT_BYTES` | `0` | Per-connection speed limit in bytes/s (0 = none). |

### Gateway for standard WireGuard apps (`gateway`)

Linux and macOS servers only. See [Install the server](install-server.md#platform-notes).

| File key | Environment variable | Default | Meaning |
|---|---|---|---|
| `gateway.enabled` | `GORGET_GATEWAY_ENABLED` | `true` | Turn the gateway on or off. |
| `gateway.interface` | `GORGET_GATEWAY_INTERFACE` | `gorget0` | WireGuard interface name. |
| `gateway.listen_port` | `GORGET_GATEWAY_LISTEN_PORT` | `51820` | UDP port. |
| `gateway.endpoint` | `GORGET_GATEWAY_ENDPOINT` | `<host>:51820` | Public `host:port` written into client configs. |
| `gateway.egress_interface` | `GORGET_GATEWAY_EGRESS_INTERFACE` | detected | Interface used for internet traffic (exit node). |
| `gateway.userspace` | `GORGET_GATEWAY_USERSPACE` | `false` | Force wireguard-go even when the kernel module exists. |
| `gateway.dns` | `GORGET_GATEWAY_DNS` | `true` | Run the DNS resolver on the gateway's address. |
| `gateway.mtu` | `GORGET_GATEWAY_MTU` | `1420` | Tunnel MTU. |

### Security and backups

| File key | Environment variable | Default | Meaning |
|---|---|---|---|
| `security.master_key` | `GORGET_MASTER_KEY` | generated | Base64 32-byte key (`gorget-server gen-master-key`). Overrides the key file. |
| `security.master_key_file` | `GORGET_MASTER_KEY_FILE` | `<data_dir>/master.key` | Where the key is stored. **Back it up.** |
| `security.session_ttl` | `GORGET_SESSION_TTL` | `12h` | How long a console sign-in lasts. |
| `security.admin_only_from_vpn` | `GORGET_ADMIN_ONLY_FROM_VPN` | `false` | Only allow the console from inside the VPN. |
| `backup.interval` | `GORGET_BACKUP_INTERVAL` | `24h` | Automatic backups (`0` disables). |
| `backup.keep` | `GORGET_BACKUP_KEEP` | `7` | How many to keep. |
| `backup.dir` | `GORGET_BACKUP_DIR` | `<data_dir>/backups` | Where they go. |

### Monitoring and clustering

| File key | Environment variable | Default | Meaning |
|---|---|---|---|
| `metrics.enabled` | `GORGET_METRICS_ENABLED` | `false` | Prometheus metrics, see [monitoring](../ops/monitoring.md). |
| `metrics.listen` | `GORGET_METRICS_LISTEN` | `127.0.0.1:9090` | Metrics address. |
| `metrics.token` | `GORGET_METRICS_TOKEN` | none | Required when listening on a non-loopback address. |
| `cluster.enabled` | `GORGET_CLUSTER_ENABLED` | `false` | Several servers on one PostgreSQL, see [high availability](../CLUSTER.md). |
| `cluster.instance_id` | `GORGET_CLUSTER_INSTANCE_ID` | host name | Unique per server. |
| `cluster.relay_url` | `GORGET_CLUSTER_RELAY_URL` | `<public_url>/relay` | This server's relay address. |
| `cluster.peer_listen`, `cluster.peer_addr` | `GORGET_CLUSTER_PEER_LISTEN`, `GORGET_CLUSTER_PEER_ADDR` | `:3480`, detected | Private address for forwarding relay packets between servers. |

## Server commands

| Command | What it does |
|---|---|
| `init` | Guided setup. `init -yes -domain vpn.example.com` asks nothing. Other flags: `-tls acme\|proxy\|private`, `-email`, `-database-url`, `-gateway yes\|no`, `-data-dir`. |
| `check` | Checks the configuration, DNS, ports and prerequisites, and says how to fix problems. |
| `install`, `uninstall` | Add or remove the system service. `install` starts it and prints the setup link. |
| `start`, `stop`, `restart` | Control the installed service. |
| `setup-link` | Prints the first-run setup link again. |
| `backup`, `restore` | [Encrypted backups](../ops/backups.md). |
| `migrate-db` | Copy everything from SQLite to PostgreSQL. |
| `reset-password -email …` | Set a temporary password for a locked-out account. |
| `config-example` | Print an annotated configuration file. |
| `gen-master-key` | Print a new random master key. |
| `healthcheck` | Exit 0 if the local server answers (used by Docker). |
| `version` | Print the version. |
