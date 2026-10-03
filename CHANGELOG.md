# Changelog

## 0.4.2

### Fixed
- `gorget-server setup-link` (new in 0.4.1) didn't run; it printed the help text. The Docker installer and
  upgrades rely on it. Upgrade to 0.4.2.

## 0.4.1

### Easier install and clearer documentation
- The Windows installer (.msi) is attached to the release again.
- **One-command server install:** `install-server.sh` now asks only for your domain (or takes
  `--domain` and `--yes`), installs and starts the service, and prints the setup link. `--docker`
  runs the server in Docker instead; running it again upgrades; `--uninstall` removes it.
- **`gorget-server install` prints the setup link** when the service is up, and the new
  `gorget-server setup-link` prints it again at any time. No more digging through logs.
- **`gorget-server init` can run without questions:** `init -yes -domain vpn.example.com`
  (also `-tls`, `-email`, `-database-url`, `-gateway`, `-data-dir`).
- **Docker:** the compose files read a small `.env` file (`GORGET_DOMAIN`, `GORGET_EMAIL`,
  `GORGET_VERSION`) instead of needing YAML edits, and work without cloning the repository.
- **Documentation rewritten:** a [Quick start](docs/guide/quickstart.md), a step-by-step
  [Install the server](docs/guide/install-server.md) with a "which method" table, per-system
  [Install the apps](docs/guide/install-clients.md), a full [configuration
  reference](docs/guide/configuration.md), a longer [troubleshooting](docs/ops/troubleshooting.md)
  page, and upgrade and move-server steps in [Backups and upgrades](docs/ops/backups.md).

## 0.4.0

### New
- **Insights:** devices online and traffic over time, device status, operating systems, app
  versions, countries, the devices with the most traffic, and recent connections. Every chart
  has a table view. The overview shows the last 24 hours.
- **Connection history** per device, with public address, country and app version.
- **Block / Unblock** devices from the list, with a *Blocked* filter; the list shows where each
  device connects from.
- **Email (SMTP):** notifications for devices waiting for approval, new devices, connections
  from a new country, devices blocked by health rules, expiring sign-ins, access requests,
  shared networks and locked accounts. Presets for common providers and a test button.
- **Invitations and password resets by email**, with one-time links; "Forgot your password?"
  on the sign-in page.
- **Country rules** in device health (allow or block countries), using a local country
  database (free DB-IP Lite download with monthly updates, or a MaxMind file). Also
  **allowed operating systems**.
- **Routers with plain WireGuard can share networks:** add LAN and VLAN ranges to a
  WireGuard configuration (Routes & exit nodes > Add networks behind a router). WireGuard
  configurations can be downloaded as an **OpenWrt setup script**.
- **One-line install on Linux and macOS:** `curl -fsSL https://vpn.example.com/install.sh | sh`.
  The server serves the installer with its own address filled in; downloads are checked
  against the release checksums. Works with a setup key for headless servers, and
  `--uninstall` removes Gorget again.
- Guide: [your home network, from anywhere](docs/guide/homelab.md).

### Fixed
- Wildcard DNS records such as `*.apps` were never qualified with the network domain and so
  never matched; custom records outside the network domain (`jellyfin.home.lan`) weren't
  answered on Gorget apps.
- On Windows a shared route could override the network the computer is in; on macOS a route
  that clashed with the current network was never retried after moving elsewhere.
- Failed sign-ins now say whether the account was locked, for the activity log and alerts.

### Changed
- The Go module and links now point at `github.com/anand34577/gorget`.
- Release builds include signed Android APKs (needs the four `ANDROID_*` repository secrets).

## 0.3.1 (review pass)

### Fixed
- **Windows client:** every daemon connection failed (the socket-exclusion hook ran before the socket had an address). DNS now only takes over all lookups when override is on; split mode uses NRPT rules alone. Sockets are reopened after a network change (Windows and macOS).
- **Security:** server-supplied DNS names are validated before they reach `/etc/resolver` paths, `resolv.conf` or PowerShell (path traversal / command injection as root or SYSTEM). The CLI and tray app refuse a local API pipe or socket that isn't owned by the service. The UDP relay re-checks the token on repeated hellos and authenticates off the packet loop. SCIM can't deactivate owners. UPnP only talks to the router that answered, without redirects. Operator matching no longer accepts a same-named account from another domain.
- **Crash recovery:** a daemon that died while connected no longer leaves the kill switch blocking traffic or DNS pointing at a dead resolver (Linux, macOS, Windows).
- **DNS server:** the "fall back to system DNS" setting now works; the most specific split-DNS domain wins; wildcard records (`*.apps`) can be saved as the console offered.
- macOS gateway: an internet rule's exclusion no longer blocks traffic that other rules allow.
- Linux client unit no longer blocks per-app routing (`ProtectControlGroups`).
- File transfer: Windows device names (CON, NUL…) and simultaneous same-name uploads are handled.

### Faster
- Access rules compile about 230x faster with 5000 devices (11 ms instead of 2.6 s, 10 MB instead of 470 MB).
- Discovery messages about 160x cheaper (cached key agreement).
- Per-packet firewall checks against large groups: about 48 ns instead of tens of microseconds; the connection table no longer rescans on every packet when full.
- DNS: identical concurrent lookups share one upstream query.
- macOS: default-interface lookup cached instead of two processes per socket; routers skip re-running `nft`, `resolvectl`, `pfctl` and PowerShell when nothing changed.
- Web console: initial bundle 712 KB to 333 KB; pages load on demand (with a one-time reload after upgrades).

### Easier
- `gorget-server init` (guided configuration) and `gorget-server check` (DNS, ports, prerequisites, with fixes). `install` now starts the service.
- The server prints a setup link with the token in it; the setup page fills it in.
- Console overview: a "Getting started" checklist for administrators.
- Tray app: step-by-step sign-in, OS-specific instructions when the service is down, busy states, file-send progress, clearer errors.
- CLI: setup keys from standard input or `GORGET_SETUP_KEY`; requests time out instead of hanging; clearer messages.

## 0.3.0

### New
- **Desktop clients** for Linux, Windows and macOS: `gorget` service and CLI, tray app, installers (.deb, .rpm, AUR, MSI, .pkg, Homebrew cask).
- **Device health** rules (client and OS version, disk encryption, firewall, allowed networks), enforce or report-only.
- **Post-quantum protection** between Gorget devices (ML-KEM-768 pre-shared keys).
- **Temporary access**: requests, approvals and direct guest grants that expire by themselves.
- **High availability**: several servers on one PostgreSQL database; shared certificates; relays forwarded between instances.
- **UDP relay** with WebSocket fallback; devices pick the nearest relay and reach peers homed elsewhere.
- **Port mapping** (PCP, NAT-PMP, UPnP) for more direct connections.
- **Domain routing**: send named sites through an exit node.
- **File transfer** between your own devices and `gorget ssh`.
- **Per-app bypass** on Linux (`gorget run -bypass`).
- **SCIM 2.0** provisioning, **Terraform provider**, **Helm chart**, **OpenAPI** document and API reference page.
- **WireGuard gateway on macOS** (wireguard-go and pf).
- Linux UDP batching (recvmmsg/sendmmsg).
- Documentation site, benchmarks, reproducible build check, SBOM and cosign-signed releases, F-Droid metadata.

### Changed
- Redesigned tray app; Android shows blocked devices, post-quantum status and relay transport.
- Login challenges and sign-in ceremonies are stored in the database (needed for clusters).
- Client protocol additions are backwards compatible (older apps keep working without the new features).

### Known gaps
See [docs/STATUS.md](docs/STATUS.md).

## 0.2.0
Shared Go client core and the Android app.

## 0.1.0
Server and web console.
