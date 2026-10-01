# Gorget build phases

Each phase ends with something usable on its own. Requirement IDs refer to `REQUIREMENTS.md`.

## Phase 1: server and web console
Deliverable: a working self-hosted VPN server that standard WireGuard clients can use today
(full-tunnel and split-tunnel), with the complete control plane that native clients will use in Phases 2–3.

Backend (`gorget-server`, Go, single binary):
- Config (YAML + env + flags), SQLite (default) / PostgreSQL, embedded migrations, SQLite→Postgres migration tool
- Secrets envelope encryption (master key), Argon2id passwords
- Auth: local accounts, sessions, CSRF, rate-limit + lockout, TOTP MFA, OIDC SSO (multiple providers), API tokens, RBAC roles
- Users, groups, devices, setup keys, device approval, key expiry, ephemeral devices
- IPAM with customisable IPv4 range + per-install ULA IPv6, static IPs, collision checks
- ACL engine: policy-as-code (JSON), groups/tags/users/hosts/ports, default deny, policy tests, simulator, version history + rollback
- Routes: subnet routes, exit nodes, approval, HA priority
- DNS: `gorget.internal` names, custom records, nameservers, split DNS, DoH/DoT upstreams (DNS server on gateway)
- WireGuard Gateway for standard clients: config + QR, full/split/custom modes, PSK, expiry/revoke, ACL enforcement (nftables), NAT for exit (Linux)
- Client control API (ConnectRPC + Protobuf): registration, interactive/browser login, challenge auth, network-map streaming with deltas, signalling, status
- Relay (WebSocket over 443) and STUN server
- Built-in web server: HTTP/2, HTTP/3, compression, security headers, rate limiting, automatic Let's Encrypt (HTTP-01/TLS-ALPN-01/DNS-01) with ZeroSSL fallback, custom cert, internal CA, behind-proxy mode
- Audit log (hash-chained), webhooks (HMAC), Prometheus metrics, health endpoints, backups
- Linux systemd / Windows service install, Dockerfile + compose (SQLite, Postgres, Caddy, Nginx examples)

Frontend (React + TS + Tailwind + Radix):
- Setup wizard, login (password, TOTP, SSO), device-login approval page
- Dashboard, Devices, Users, Groups, Access Control (visual + code editor + tests + simulator + history),
  Network map, Routes, DNS, WireGuard configs (browser-side key generation + QR), Setup keys,
  Audit log, Settings (network, auth/SSO, TLS status, API tokens, webhooks), self-service "My devices"
- Dark/light theme, command palette, real-time updates, responsive, accessible

### Phase 1 status: complete, with these known gaps
- **Tested:** unit tests (policy engine, IPAM, secrets, STUN, DNS, nftables ruleset), an end-to-end native-client protocol test
  (register → challenge auth → full map → deltas → signalling → revocation, race detector clean), API smoke tests and a
  headless-browser pass over every console page (light, dark, mobile).
- **Not yet verified on real infrastructure:** the gateway data plane (needs a Linux host with a WireGuard client),
  automatic Let's Encrypt issuance (needs a public domain), PostgreSQL (portable schema, not run against a live server),
  OIDC against a real identity provider, passkeys in a real browser.
- **Deferred:** gateway on Windows/macOS servers (control plane, relay and STUN already run there), UDP relay
  (WebSocket relay over 443 is in place), OpenAPI document for the REST API, SCIM, Helm chart.

## Phase 2: Go client core and Android app
- `libgorget` shared core: control client, WireGuard engine (wireguard-go), ICE/NAT traversal, relay client,
  embedded DNS resolver, peer firewall, roaming
- Android (Kotlin + Compose, `VpnService` via gomobile): login, peers, exit nodes, per-app split tunnel,
  Always-On, quick tile, widget; APK signed with project key

### Phase 2 status: complete
- `client/`: shared Go core.
  - `magicsock`: disco NAT traversal, STUN, relay fallback.
  - `tunx`: stateful firewall, in-tunnel DNS, swappable TUN.
  - Control client, engine, state and preferences.
- `mobile/gorgetcore`: gomobile binding.
- `android/`: Kotlin/Compose app (see docs/ANDROID.md).

## Phase 3: desktop clients (Linux, Windows, macOS)
- `gorget` daemon + CLI, kernel WG (Linux), WireGuardNT (Windows), utun (macOS)
- Kill switch, leak protection, LAN access, exit-node use and serving, subnet router, trusted networks
- Wails desktop/tray app, installers (.deb/.rpm/MSI/.pkg/Homebrew)

### Phase 3 status: complete. See [STATUS.md](STATUS.md).

## Phase 4: advanced features

### Phase 4 status: complete. Remaining gaps are listed in [STATUS.md](STATUS.md).
- Posture checks, post-quantum PSKs, UDP relay + multi-region relays, HA control plane
- App split tunnelling on desktop, domain routing, SSH over mesh, file transfer, JIT access, SCIM, Terraform, Helm

## Phase 5: hardening and release

### Phase 5 status: everything that does not need outside parties is done (fuzz tests, reproducible builds, SBOM and cosign workflow, F-Droid metadata, docs site, benchmarks). The external audit and OS code signing remain.
- Fuzzing, external audit, reproducible builds, SBOM, cosign, F-Droid, docs site, benchmarks, OS signing when available
