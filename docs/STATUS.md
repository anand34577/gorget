# Project status

Companion to [REQUIREMENTS.md](REQUIREMENTS.md) (what Gorget should do) and
[ROADMAP.md](ROADMAP.md) (the order it was built in).

## Summary

| Area | State |
|---|---|
| Server (`gorget-server`) and web console | Feature complete |
| Shared Go client core and Android app | Feature complete |
| Desktop clients (Linux, Windows, macOS), tray app, installers | Feature complete |
| Advanced features (health rules, temporary access, post-quantum keys, clusters, SCIM, Terraform, Helm) | Feature complete, see "Known gaps" |
| Release engineering (fuzzing, reproducible builds, SBOM, signatures, F-Droid metadata, docs site) | Done; an external audit and OS code signing are still open |

**Testing so far:** `go vet` and `staticcheck` on Linux, Windows and macOS, the Go test suite
(unit tests plus in-process end-to-end tests of direct and relayed connections, access
rules, post-quantum key exchange and device health), TypeScript checks, and builds of the
console, the tray app and the Android app. **Not yet exercised on real machines and
networks:** see the last section.

## What exists

### Server

| Area | Where |
|---|---|
| Configuration (YAML, `GORGET_*` variables, flags), guided `init` and `check` commands | `internal/config`, `cmd/gorget-server` |
| SQLite or PostgreSQL with automatic migrations | `internal/store` |
| Secrets encrypted with a master key; Argon2id passwords; hashed tokens | `internal/secrets` |
| Sign-in: passwords with lockout, sessions, CSRF protection, TOTP and recovery codes, passkeys, OIDC single sign-on, API tokens, invitations and password resets by email | `internal/auth`, `internal/api` |
| Roles: owner, admin, network admin, auditor, member | `internal/api` |
| Customisable address range, pools, fixed addresses, re-addressing | `internal/ipam` |
| Access rules as code with tests, simulator, history and rollback | `internal/policy` |
| Per-device network maps streamed as changes; route failover; presence | `internal/core` |
| Subnet routes from Gorget apps and from routers using plain WireGuard (site routes) | `internal/core` |
| Gateway for standard WireGuard apps (Linux: kernel WireGuard + nftables; macOS: wireguard-go + pf) | `internal/gateway` |
| DNS: device names, custom records and wildcards (inside or outside the network domain), split DNS, DoH/DoT upstreams | `internal/dnsserver` |
| NAT traversal: STUN, WebSocket and UDP relays | `internal/stun`, `internal/relay` |
| Built-in HTTPS with Let's Encrypt (HTTP-01, TLS-ALPN-01, DNS-01), HTTP/3 | `internal/web` |
| Insights: devices online and traffic over time, breakdowns, connection history with public address and country | `internal/core/stats.go` |
| Country lookups from a local database (DB-IP Lite download or MaxMind file) | `internal/geoip` |
| Email (SMTP) notifications, invitations, password resets | `internal/mailer`, `internal/core/email.go` |
| Device health rules: app and OS versions, disk encryption, firewall, allowed operating systems, networks and countries | `internal/core/posture.go` |
| Temporary access, SCIM 2.0, OpenAPI document, webhooks, hash-chained activity log, backups, Prometheus metrics, clustering on PostgreSQL | `internal/*` |

### Clients

| Area | Where |
|---|---|
| Shared core: WireGuard engine with its own transport, encrypted path discovery, relay fallback, port mapping (PCP, NAT-PMP, UPnP), in-tunnel DNS, stateful firewall | `client/` |
| Android app (Kotlin, Compose): always-on, quick tile, widget, per-app routing, exit nodes | `android/`, `mobile/gorgetcore` |
| Desktop service and `gorget` CLI (Linux, Windows, macOS), local API protected by OS permissions | `cmd/gorget`, `client/localapi`, `client/osrouter` |
| Tray app (Wails) | `desktop/` |
| Packages: .deb, .rpm, AUR, MSI, macOS .pkg, Homebrew cask | `packaging/` |

## Known gaps

### Needs outside parties
- **External security audit.** [SECURITY.md](SECURITY.md) describes the scope.
- **Code signing** for Windows and **notarisation** for macOS need certificates. Releases
  carry checksums and cosign signatures; the Android APK is signed with the project key.
- **Wintun checksum** in `packaging/windows/build-msi.ps1` must be confirmed on the first
  build (the script refuses to build on a mismatch).

### Not built, by design or platform limits
- **Windows** can't be an exit node or run the gateway (no built-in NAT, no per-flow firewall).
- **Per-app routing** on desktop exists on Linux only (Windows needs a signed driver, macOS a
  network extension).
- **Routers with plain WireGuard** connect through the server's gateway, not directly to
  other devices.
- **One network per server.** Guests use temporary access instead of separate tenants.
- **UDP GSO/GRO** is not used (Linux batches packets with recvmmsg/sendmmsg).
- An identity-based SSH server: `gorget ssh` wraps the system's ssh.

### Behaviour worth knowing
- Network changes are noticed by polling every 5 seconds on desktop.
- Large DNS answers over TCP aren't intercepted by the in-tunnel resolver.
- Post-quantum keys are set up once per device pair and kept until a device key changes.
- Domain routes take effect just after the first lookup; programs with their own
  DNS-over-HTTPS bypass them.
- Health values are reported by the client: hygiene, not a boundary against a hostile device.
  Country and network rules are checked by the server and can't be faked by the client.
- Kill switch: on Windows it also blocks the local network while an exit node is on; on
  macOS it lets every root process out.
- On Android, traffic to a shared home network goes through Gorget even when the phone is
  at home (it still works, and stays local when the home router device is reachable directly).

### Not yet verified on real infrastructure
- Desktop clients on real Windows, macOS and Linux machines: routing, DNS, kill switches,
  service installation, tray app, installers.
- Server: the gateway with real WireGuard clients and an OpenWrt router, Let's Encrypt on a
  public domain, PostgreSQL, a two-instance cluster, SSO and SCIM with a real identity
  provider, passkeys in a browser, SMTP with a real provider.
- Port mapping against real routers; Android on a phone.

## Design decisions

| # | Decision | Reason |
|---|---|---|
| D-1 | Name **Gorget**, default domain **`gorget.internal`** | The armour plate that guards the throat, a nod to WireGuard. `.internal` is reserved for private use, so it never collides with a real domain. |
| D-2 | **AGPL-3.0** | Keeps every fork, including hosted versions, open source |
| D-3 | **Kotlin + Jetpack Compose** for Android | Best fit with VpnService, battery use and smoothness |
| D-4 | **Wails v3** for the desktop app | Go-native, small (system web view), and v3 has a system tray |
| D-5 | No Windows/Apple code signing yet; Android signed with the project key | Cost. Checksums and cosign signatures cover releases meanwhile. |
| D-6 | **Built-in web server with automatic Let's Encrypt** | One binary to run; Caddy and Nginx remain optional |
| D-7 | **Customisable address range**, default `100.80.0.0/16` | Avoids clashes with home and office networks; can be changed later |
| D-8 | **ConnectRPC + Protobuf over TLS 1.3** between apps and server | One schema for Go and Kotlin; works through proxies |
| D-9 | **SQLite by default, PostgreSQL optional** | No setup for most people; PostgreSQL for scale and clusters |
| D-10 | **wireguard-go with a custom transport** in the apps | NAT traversal and relay fallback need control over every packet; the gateway still uses kernel WireGuard |
| D-11 | **One virtual endpoint per peer**, WireGuard roaming off | Switching between direct and relayed paths never interrupts a session |
| D-12 | **Encrypted peer-to-peer discovery**, hole-punch requests passed through the server | The server can't read path information |
| D-13 | **Rules enforced on every device and at the gateway**, and peers only learn about devices they may reach | Defence in depth |
| D-14 | **JSON over a Unix socket or named pipe** for the local service API | Only reachable from the same computer; OS permissions protect it |
| D-15 | **In-tunnel DNS at `100.100.100.100`** | Names resolve without per-OS resolver code |
| D-16 | **Linux policy routing** (table 52, fwmark) | Exit-node mode can't loop the service's own traffic; local networks keep working |
| D-17 | **One-time setup token** for the first-run wizard | Nobody can claim a fresh server first |
| D-18 | **Keys generated in the browser** for standard WireGuard apps | The server never sees their private keys |
| D-19 | **Hash-chained activity log** | Tampering with the stored log can be detected |
| D-20 | **Signed device tokens re-checked against the database** | Fast, works across servers, revocation is immediate |
| D-21 | **Backups as encrypted JSON of every table** | Restores into SQLite or PostgreSQL and drives database migration |
| D-22 | **No analytics, crash reporting or external fonts/CDNs** | Zero data collection; fonts are bundled |
| D-23 | **Four-step commit for post-quantum keys** | Both devices switch keys together; lost messages leave both on the old key |
| D-24 | **Clusters share one PostgreSQL database** (presence rows, LISTEN/NOTIFY, leader lease) | The database is already the source of truth |
| D-25 | **Guests via temporary access**, not multi-tenancy | One network per server stays simple |
| D-26 | **Countries from a local database file** | Lookups never leave the server; DB-IP Lite is free (CC BY 4.0), MaxMind files also work |
| D-27 | **Email through the administrator's own SMTP provider** | No third-party notification service; the password is encrypted at rest |
| D-28 | **Routers with plain WireGuard can carry subnet routes** | Lets an OpenWrt (or similar) router share a home network without installing Gorget on it |

## Building

| What | Command | Docs |
|---|---|---|
| Server and console | `make`, then `make run` for a local test on :8080 | `README.md` |
| Server in Docker | `cd deploy/docker && docker compose up -d` | `README.md` |
| Tests | `make test` | |
| Android | `make android` (gomobile, Android SDK and NDK, JDK 17+) | [ANDROID.md](ANDROID.md) |
| CLI and service | `make cli`, then `sudo bin/gorget daemon` and `gorget up -server URL` | |
| Tray app | `make desktop` | |
| Installers | `make packages-linux`, `packaging/windows/build-msi.ps1`, `packaging/macos/build-pkg.sh` | `packaging/` |
| Releases | push a tag `v*`; GitHub Actions builds, signs and publishes everything, including the signed Android APKs | `.github/workflows/release.yml` |
| Reproducible build check | `make repro` | [reproducible builds](ops/reproducible-builds.md) |
| Benchmarks | `make bench` | [BENCHMARKS.md](BENCHMARKS.md) |
| Documentation site | `pip install mkdocs-material`, then `make docs-site` | `mkdocs.yml` |
| Demo data | start a server, create a reusable setup key, `go run ./scripts/seed -setup-key gsk_…` | |
