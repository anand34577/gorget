# Product Requirements Specification
# Gorget
**Self-Hosted WireGuard Mesh VPN Platform**

| Item | Value |
|---|---|
| Product name | **Gorget**: the piece of armour that guards the throat, the most vulnerable point. It nods to Wire*Guard*, and it's short, distinctive, and unused by any existing VPN or networking project (checked October 2026). |
| Binaries | `gorget-server` (control plane, relay, gateway, web server) · `gorget` (client daemon + CLI) · *Gorget* desktop app · *Gorget* Android app (`app id: io.gorget.android`, final ID TBD) |
| Default network domain | `gorget.internal` (configurable). See §8. |
| Document status | Draft v0.3 (all decisions resolved except D6 macOS distribution timing) |
| Date | 2026-10-01 |
| Scope | Requirements only: no code, no implementation detail beyond what's needed to state a requirement precisely |

Keywords: **MUST** means mandatory for v1.0. **SHOULD** means strongly recommended (v1.0 if feasible, otherwise v1.x). **MAY** means optional or future.

---

## 1. Vision

Build a **fully open-source, self-hosted, zero-telemetry** VPN platform on **WireGuard**, comparable to NetBird and Tailscale/Headscale, that:

1. Creates a **secure peer-to-peer mesh** between your devices (servers, PCs, phones, home networks).
2. Supports **two traffic modes**:
   - **Mesh / split-tunnel mode** (like NetBird/Tailscale): only traffic for VPN peers and advertised private networks goes through the tunnel. Normal internet traffic stays on the local connection.
   - **Full-tunnel mode** (like a classic WireGuard VPN): *all* traffic, internet included, goes through a chosen **exit node**.
3. Works with **its own native clients** (full features) **and with standard WireGuard clients** (e.g. official WireGuard apps). Standard clients get reduced features, which is accepted.
4. Is controlled through a **modern web admin console** and **modern native client apps**.
5. Is **industry grade**: secure by default, fast, observable, easy to deploy, and easy to operate.

### 1.1 Core principles (non-negotiable)

| # | Principle |
|---|---|
| P1 | **No data collection.** No telemetry, analytics, crash uploads, "phone home", update pings, or third-party SDKs in any component. All data stays on the operator's own server. |
| P2 | **Fully open source.** All components (server, clients, UI, apps) are public and buildable from source. No closed "enterprise" features. |
| P3 | **Self-hosted first.** No dependency on any vendor cloud. Works fully air-gapped or on a LAN. |
| P4 | **Secure by default.** Default-deny ACLs, encrypted secrets, TLS everywhere, least privilege, signed releases. |
| P5 | **The control plane never sees traffic.** The management server only coordinates keys, configuration, and policy. Data flows peer-to-peer (or through an encrypted relay that can't decrypt it). The only exception is gateway mode for standard WireGuard clients (see §6). |
| P6 | **Performance first.** Use kernel WireGuard wherever it's available, and keep the userspace path optimised. |

---

## 2. Technology stack (as specified)

| Layer | Choice |
|---|---|
| Backend / control plane | **Go** |
| Client daemon (agent) | **Go**, a single shared core for all platforms |
| Data plane | WireGuard: Linux kernel module, **WireGuardNT** on Windows, **wireguard-go** (optimised userspace) on macOS/Android or as a fallback |
| Web admin UI | **React + TypeScript**, Vite, Tailwind CSS, shadcn/ui (Radix), TanStack Query/Table, Framer Motion, Recharts |
| Desktop client UI | React UI in a **Wails** shell (Go-native, small binaries, uses the OS webview, no bundled Chromium) |
| Android app | Native **Kotlin + Jetpack Compose** UI (Material 3), with the Go core embedded through gomobile in Android `VpnService` |
| macOS app | Go core running as a privileged **launchd daemon using `utun`** (works without Apple signing) + React UI in a Wails menu-bar app. Network Extension variant deferred until an Apple Developer ID is available (see §16, D7). |
| Web server / TLS | **Built-in web server** in the Go server binary, with **automatic Let's Encrypt certificates** (CertMagic, the ACME engine behind Caddy). Optional external Caddy/Nginx reverse proxy. See §13.7. |
| License | **AGPL-3.0** for all components |
| Database | **SQLite (default)**, **PostgreSQL** (optional, required for HA) |
| API | REST (OpenAPI 3 spec) for the admin/public API. **ConnectRPC + Protobuf** over TLS 1.3 (HTTP/2, HTTP/1.1 fallback) with Ed25519 device-key authentication for client ↔ server (§16.4) |
| Platforms | **Linux** (amd64, arm64, armv7), **Windows** (amd64, arm64), **macOS** (Intel + Apple Silicon), **Android**. **iOS is out of scope.** |

---

## 3. System components

| ID | Component | Responsibility |
|---|---|---|
| C1 | **Management Server** | Users, devices, keys, ACLs, routes, DNS config, and the admin API. Serves the web UI. Pushes network maps to agents in real time. |
| C2 | **Signal / Coordination service** | Exchanges connection candidates (endpoints) between peers for NAT traversal. Sees only encrypted, opaque messages. Can be embedded in C1. |
| C3 | **Relay service** | Forwards *already-encrypted* WireGuard packets when a direct P2P connection is impossible (symmetric NAT, strict firewall). Works over UDP, with TCP/TLS/WebSocket fallback on port 443 for restrictive networks. Can be embedded in C1 or deployed in several regions. |
| C4 | **STUN service** | Discovers public endpoints. Embedded. |
| C5 | **WireGuard Gateway** | A WireGuard interface on the server (or on any designated node) that standard WireGuard clients connect to. Bridges them into the mesh and enforces ACLs for them. |
| C6 | **Embedded DNS resolver** | Runs in every native agent. Resolves internal names and applies split DNS and custom upstreams. |
| C7 | **Client Agent (daemon/service)** | Runs on each device. Manages the WireGuard interface, NAT traversal, routes, DNS, firewall, and kill switch. Exposes a local IPC API to the UI and CLI. |
| C8 | **Client UI apps** | Desktop (Windows/macOS/Linux) tray/menu-bar apps and the Android app |
| C9 | **CLI** | `gorget` (client) and `gorget-server` (admin) for the server admin and the client (connect, status, ping, routes, debug) |
| C10 | **Web Admin Console** | React SPA for administrators and a self-service portal for users |
| C11 | **Edge Web Server** | Built-in HTTPS server. Terminates TLS, obtains and renews Let's Encrypt certificates automatically, serves the web UI and client downloads, routes API/gRPC/signal/relay traffic on port 443, applies compression, security headers, and rate limits (§13.7) |

**Deployment simplicity requirement:** C1–C5 and C11 **MUST** ship as **one single Go binary** (all-in-one mode), with an option to run relays separately for scaling and geography.

---

## 4. Networking and traffic modes

### 4.1 Overlay network
- **N-1** MUST assign each device a stable overlay IPv4 **and** IPv6 address from a **fully customisable address plan** (see §4.0).

### 4.0 VPN address plan (customisable IP series)
- **IP-1** The overlay IPv4 range is **fully configurable** by the admin: any RFC 1918 range (`10.x`, `172.16–31.x`, `192.168.x`) or the CGNAT range (`100.64.0.0/10`), with any prefix length from `/8` to `/28`.
- **IP-2** **Default:** IPv4 `100.80.0.0/16` (a sub-range of CGNAT that is unlikely to clash with home or office LANs; it's narrower than the full `/10`, which avoids clashes with carrier CGNAT on mobile networks). IPv6: a randomly generated ULA `/48` per installation (`fdXX:XXXX:XXXX::/48`, per RFC 4193), so two Gorget installations never collide.
- **IP-3** The range is chosen in the **setup wizard**, which shows the default and warns if it overlaps the server's own LAN or any advertised subnet route.
- **IP-4** **Per-network ranges**: each network/tenant (ID-7) has its own address plan.
- **IP-5** **Static IP assignment**: admins can pin a specific address to a device or a standard WireGuard peer. Other devices get the next free address automatically, and the address stays stable across reconnects and key rotations.
- **IP-6** **Reserved addresses / sub-pools**: admins can reserve ranges (e.g. `.1–.20` for servers) and assign sub-pools per group or tag (e.g. servers in `100.80.1.0/24`, phones in `100.80.10.0/24`). This makes firewall rules outside Gorget easier.
- **IP-7** **Collision detection**: clients detect when the overlay range overlaps their current local network, warn the user, and report it to the admin console. Routes are installed so that the more specific route wins, without breaking local access.
- **IP-8** **Re-addressing**: changing the range later is supported through a guided migration (preview of the new address per device, then apply). Native clients update automatically. Standard WireGuard peers are flagged for config re-download.
- **IP-9** IPv6 in the overlay can be disabled per network if not wanted.
- **IP-10** Validation prevents ranges that are too small for the current device count, public (non-private) ranges, and overlaps with gateway/relay addresses.
- **N-2** MUST build a **full mesh** of direct peer-to-peer WireGuard tunnels between native clients, limited to peers that ACLs allow to talk to each other (peers not allowed by ACL aren't even configured).
- **N-3** MUST perform **NAT traversal**: STUN, UDP hole punching, ICE-style candidate exchange (host, server-reflexive, relay), port-mapping protocols (UPnP-IGD, NAT-PMP, PCP), and IPv6 direct connections when available.
- **N-4** MUST fall back automatically to the **relay** when a direct connection fails, and MUST keep trying to upgrade back to a direct connection in the background.
- **N-5** MUST support **relay over TCP/443 (TLS/WebSocket)** so the client works on networks that block UDP (hotels, corporate Wi-Fi, captive networks).
- **N-6** SHOULD support multiple relay regions, with each client picking the lowest-latency one.
- **N-7** MUST show per-peer connection type (direct / relayed), latency, last handshake, and transfer bytes.

### 4.2 Mode A: mesh / split tunnel (default for native clients)
- **N-10** Only traffic to (a) overlay IPs of peers and (b) private subnets advertised by subnet routers (§4.4) is routed into the tunnel.
- **N-11** All other internet traffic uses the device's normal connection.
- **N-12** MUST NOT break the device's local LAN access.

### 4.3 Mode B: full tunnel / exit node
- **N-20** Any node (Linux, Windows, macOS, and Docker containers) MAY be designated an **exit node** by an admin, subject to approval.
- **N-21** A native client MUST be able to select an exit node from the UI or CLI. All traffic (`0.0.0.0/0`, `::/0`) then goes through it.
- **N-22** The exit node MUST perform NAT/masquerade and IP forwarding automatically, with clean rollback on shutdown.
- **N-23** **Allow LAN access** toggle: while on full tunnel, the user can still reach their local network (printer, NAS, router).
- **N-24** **Kill switch**: if the tunnel drops while in full-tunnel mode, block all non-tunnel traffic (configurable, policy-enforceable by the admin).
- **N-25** MUST prevent **DNS leaks** and **IPv6 leaks** in full-tunnel mode.
- **N-26** Admins MAY set a **default exit node** per group, or **force** an exit node (e.g. "all contractor devices always use exit node X").
- **N-27** SHOULD support **suggest best exit node** based on latency.

### 4.4 Local network access (subnet routing)
- **N-30** Any node MUST be able to **advertise local subnets** (e.g. `192.168.1.0/24`) so other authorised peers can reach devices that have no agent installed (printers, NAS, cameras, servers).
- **N-31** Advertised routes MUST require **admin approval**. Auto-approval rules MAY exist per tag.
- **N-32** **High-availability subnet routers**: two or more routers for the same subnet with automatic failover.
- **N-33** **Overlapping subnet handling**: SHOULD support 1:1 NAT / address mapping (e.g. two sites both using `192.168.1.0/24`).
- **N-34** Access to subnet resources MUST be governed by ACLs, down to host and port.
- **N-35** SHOULD support **site-to-site** setups (whole LAN ↔ whole LAN through routers).

### 4.5 Split tunneling controls (native clients)
- **N-40** **Route-based**: include/exclude specific CIDRs.
- **N-41** **App-based split tunneling** MUST be available on Android and SHOULD be available on Windows/macOS where the OS allows it ("only these apps use the VPN" / "these apps bypass the VPN").
- **N-42** **Domain-based routing** SHOULD be available (route traffic for `*.example.com` through the exit node).

### 4.6 Protocol details
- **N-50** Full IPv4 and IPv6 support inside the tunnel and for the underlay.
- **N-51** Automatic **MTU** handling (path MTU detection, sensible default of 1280/1420), overridable per device.
- **N-52** Configurable WireGuard listen port (default random, or fixed for firewalled servers).
- **N-53** Keepalive tuned automatically (only when behind NAT, to save mobile battery).

---

## 5. Native client features (Windows, macOS, Linux, Android)

### 5.1 Connection experience
- **CL-1** One-click / one-tap **Connect / Disconnect**.
- **CL-2** **Auto-connect on startup / always-on VPN** (Android Always-On + "Block connections without VPN" support).
- **CL-3** **Seamless roaming**: switching Wi-Fi ↔ mobile data ↔ Ethernet, or sleep/wake, MUST NOT require reconnecting. Endpoints are re-discovered automatically and tunnels recover within a few seconds.
- **CL-4** **Trusted networks**: automatically disconnect (or switch to mesh-only mode) on trusted Wi-Fi SSIDs. Auto-connect / enforce full tunnel on untrusted Wi-Fi.
- **CL-5** **Captive portal detection**: temporarily allow the login page, then protect again.
- **CL-6** **Multiple profiles / networks**: connect to more than one management server (e.g. "Home" and "Office") and switch between them quickly.
- **CL-7** **Fast connect**: the cached network map lets the tunnel come up immediately even if the management server is temporarily unreachable (existing peers keep working; P2P links don't depend on the server).

### 5.2 Information and control
- **CL-10** Status view: connected state, own overlay IP, current exit node, connection quality.
- **CL-11** Peer list: name, OS, online status, IP, direct/relayed, latency, with copy IP/hostname, search, and filter.
- **CL-12** Exit node picker with latency and location labels.
- **CL-13** Toggles: allow LAN access, kill switch, use custom DNS, accept routes, run as exit node, advertise routes (where permitted by the admin).
- **CL-14** Built-in **diagnostics**: ping a peer, NAT type check, relay reachability, DNS test, and a **"Generate debug bundle"** button that produces a local file (redacted, never auto-uploaded).
- **CL-15** Live throughput graph (local only).
- **CL-16** System tray / menu-bar icon with quick actions and status colour.
- **CL-17** Local notifications (disconnected, new device approved, key expiring), all individually switchable.

### 5.3 Authentication on clients
- **CL-20** Login via browser (SSO/OIDC or local account) using a device-code or loopback redirect flow.
- **CL-21** Headless login for servers using **setup keys / auth keys** (one-off, reusable, ephemeral, pre-tagged, expiring).
- **CL-22** QR-code login and enrolment for Android.

### 5.4 Platform specifics
| Platform | Requirements |
|---|---|
| **Linux** | Kernel WireGuard when present, wireguard-go fallback. systemd service. nftables (iptables fallback) for firewall/kill switch. systemd-resolved / resolvconf / direct `/etc/resolv.conf` DNS integration. Packages: `.deb`, `.rpm`, Arch (AUR), static tarball, Docker image. GUI (tray) is optional, with a CLI-only / headless mode. Runs on Raspberry Pi and other ARM. |
| **Windows** | WireGuardNT kernel driver (Wintun fallback; both drivers are already signed by their upstream authors, so no own driver signing is needed). Runs as a Windows Service. WFP (Windows Filtering Platform) for kill switch and leak protection. NRPT for split DNS. MSI installer (+ winget). Windows 10/11, amd64 and arm64. **Initially unsigned**: SmartScreen will warn on first install, documented with SHA-256 checksums and cosign signatures for verification. Authenticode signing added later. |
| **macOS** | **v1: root launchd daemon with `utun` interface** (wireguard-go), `pf` firewall for the kill switch, `scutil`/resolver files for DNS. Menu-bar app (Wails). Intel + Apple Silicon universal binary. macOS 12+. Distributed as `.pkg` + Homebrew cask. **Initially unsigned/not notarised**: users must approve in *System Settings → Privacy & Security* (documented). **Later**, once an Apple Developer ID is available: signed + notarised build with a Network Extension. |
| **Android** | `VpnService`, Always-On VPN support, per-app split tunnel, quick-settings tile, home-screen widget, battery-optimised keepalive, Material 3 UI with dark mode. **Signed with the project's own release key** (key kept offline, with a published certificate fingerprint). Distributed via GitHub releases (APK), **F-Droid** (fits the no-tracking principle), and optionally Google Play. Android 8+. Android TV support SHOULD be considered. |

---

## 6. Standard WireGuard client support (compatibility mode)

Plain WireGuard clients (official apps, `wg-quick`, routers like OpenWrt/MikroTik/pfSense) cannot do NAT traversal, SSO, or dynamic config. They therefore connect in **hub mode** to the **WireGuard Gateway (C5)**.

- **WG-1** Admins (and users, if allowed) MUST be able to create a "WireGuard config" peer from the UI.
- **WG-2** The server generates a standard `.conf` file and a **QR code** for download/scan. The private key SHOULD be generated in the user's browser (client-side), so the server never stores it. A server-generated option is allowed if explicitly chosen, with a one-time download.
- **WG-3** A per-config **traffic mode selector**:
  - **Full tunnel**: `AllowedIPs = 0.0.0.0/0, ::/0`. All traffic exits through the gateway (the gateway acts as the exit node).
  - **Split tunnel**: `AllowedIPs` = overlay range + selected subnets only.
  - **Custom**: an admin-defined list of CIDRs.
- **WG-4** Standard clients MUST be able to reach native-client peers and subnet routes through the gateway, **subject to the same ACLs** (enforced at the gateway with a firewall).
- **WG-5** DNS for standard clients is pushed in the config (`DNS =` pointing to the gateway's internal resolver), so internal names work.
- **WG-6** Optional **pre-shared key** per peer (MUST default to on).
- **WG-7** Config **expiry** and **revocation**: revoking immediately removes the peer from the gateway.
- **WG-8** Usage stats per standard peer: last handshake, endpoint, bytes.
- **WG-9** **Multiple gateways** SHOULD be supported (e.g. gateway in the EU, gateway at home). Any native Linux node MAY act as a gateway.
- **WG-10** Admins can import existing WireGuard peers (migration from a plain `wg` setup).

**Accepted limitations of standard clients** (documented in the UI): no P2P mesh (all traffic is hubbed via the gateway), no automatic roaming optimisation, no SSO login, no posture checks, no auto-config updates (a config change requires re-download), no kill switch beyond what the client itself offers.

---

## 7. Access control (ACL)

- **ACL-1** **Default deny**: a new network allows nothing until a policy permits it (a "default allow-all" starter policy MAY be offered during setup wizard, clearly labelled).
- **ACL-2** Policy subjects: **users, groups, device tags, individual devices, standard WG peers, IP/CIDR**.
- **ACL-3** Policy objects: devices, tags, groups, subnet routes, exit nodes, **ports and protocols** (TCP/UDP/ICMP/any, port ranges).
- **ACL-4** Directional policies (A → B) with automatic stateful return traffic. Bidirectional when explicitly chosen.
- **ACL-5** **Enforced at every peer** (each native agent filters incoming traffic in its own firewall), **and** peers only receive configs for peers they're allowed to reach (defence in depth). Enforced at the gateway for standard WG clients and at subnet routers for subnet traffic.
- **ACL-6** **Two editing modes**: (a) a visual rule builder in the UI, and (b) **policy-as-code** (a JSON/HuJSON/YAML file) with syntax validation, diff preview, and version history.
- **ACL-7** **Policy tests**: admins can define assertions ("user X MUST reach host Y:22", "group Z MUST NOT reach DB") that are checked before a policy is saved.
- **ACL-8** **Policy simulator / "who can access what"** view: select a source and destination to see the resulting decision and the matching rule.
- **ACL-9** Time-limited access rules SHOULD be supported (e.g. contractor access for 7 days).
- **ACL-10** Policy changes propagate to all online agents within **≤ 5 seconds**.
- **ACL-11** Tag ownership: only defined owners can assign a given tag to devices.
- **ACL-12** Policy-as-code SHOULD be syncable from a Git repository (GitOps).

---

## 8. DNS

- **DNS-1** **Internal name resolution (MagicDNS-style)**: every device is reachable as `<device-name>.<network-domain>` (e.g. `nas.gorget.internal`). The default domain is **`gorget.internal`**. `.internal` is the TLD that ICANN reserved in 2024 for private use, so it never resolves on the public internet and can't collide with a real domain. Admins can change it to any domain they control (e.g. `vpn.example.com`) or to another private suffix, per network. Short names MUST work through search domains.
- **DNS-2** **Custom nameservers**: global upstream resolvers set by the admin (e.g. Cloudflare, Quad9, self-hosted Pi-hole/AdGuard).
- **DNS-3** **Split DNS**: route specific domains to specific resolvers (e.g. `corp.local` → `10.0.0.53` behind a subnet router).
- **DNS-4** **Encrypted upstream DNS**: DNS-over-HTTPS and DNS-over-TLS upstreams MUST be supported.
- **DNS-5** **Custom DNS records**: admins can define A/AAAA/CNAME records within the network domain.
- **DNS-6** Per-group DNS settings (different groups get different resolvers).
- **DNS-7** Users can override with "use my local DNS" if the admin permits it.
- **DNS-8** Leak protection in full-tunnel mode (all DNS forced through the tunnel resolver).
- **DNS-9** Optional **DNS-based ad/tracker/malware blocking** using admin-chosen blocklists, resolved locally on the agent/gateway with no external service required. (SHOULD)
- **DNS-10** Fallback behaviour if the upstream is down (configurable: fail open to system DNS or fail closed).

---

## 9. Identity, authentication, and device management

### 9.1 Users and identity
- **ID-1** **Local accounts** with Argon2id-hashed passwords.
- **ID-2** **SSO via OIDC** (any provider: Keycloak, Authentik, Zitadel, Google, Microsoft Entra ID, GitHub, Okta, …). Multiple providers at once. **SAML** MAY be added later.
- **ID-3** **MFA**: TOTP and **WebAuthn/Passkeys** (hardware keys). Admins can enforce MFA.
- **ID-4** **Roles (RBAC)**: Owner, Admin, Network Admin, Auditor (read-only), User. Custom roles SHOULD be supported later.
- **ID-5** **User/group sync** from the IdP (OIDC group claims; SCIM SHOULD be supported).
- **ID-6** User invitations by link/email (SMTP optional; links can be shared manually if no SMTP is configured).
- **ID-7** **Multi-tenancy / multiple networks** in one server installation (e.g. "Home", "Office", "Customer A"), with strict isolation. (SHOULD)

### 9.2 Device lifecycle
- **DV-1** **Device approval**: optionally require admin approval before a new device joins.
- **DV-2** **Key expiry**: devices re-authenticate after a configurable period (e.g. 90/180 days). Expiry can be disabled per device (for servers).
- **DV-3** **Automatic WireGuard key rotation** without connectivity loss.
- **DV-4** **Ephemeral devices** (CI runners, containers): auto-removed after going offline.
- **DV-5** Device rename, tag, disable, remove, and remote **log out / revoke instantly**.
- **DV-6** Device details: OS, version, hostname, last seen, endpoints, routes, connection type.
- **DV-7** **Device posture checks** (SHOULD): minimum client version, OS version, geo/country of public IP, disk encryption / firewall enabled (where detectable), running process. Failing devices are blocked or restricted by policy.
- **DV-8** **Lost device**: one-click revoke from the web portal, by the user or an admin.
- **DV-9** Inactive device cleanup policy.

---

## 10. Web admin console (React)

### 10.1 Pages / modules
1. **Setup wizard** (first run): create owner account, network domain, IP range, default policy, TLS mode, optional SSO.
2. **Dashboard**: online/offline devices, direct vs relayed ratio, relay health, recent events, pending approvals, expiring keys.
3. **Devices**: table with search/filter/sort and bulk actions, plus a detail drawer.
4. **Users & Groups**.
5. **Access Control**: visual builder + code editor (Monaco) + tests + simulator + history/rollback.
6. **Network Map**: interactive topology graph (peers, routers, exit nodes, relays, live link state).
7. **Routes**: subnet routes and exit nodes, approval queue, HA status.
8. **DNS**: nameservers, split DNS, records, blocklists.
9. **WireGuard Configs** (standard clients): create, QR code, download, revoke, stats.
10. **Setup Keys**: create/revoke, usage counts, expiry, tags.
11. **Gateways & Relays**: status, load, regions.
12. **Audit Log**: who changed what and when, filterable and exportable.
13. **Settings**: authentication/SSO, MFA policy, key expiry, posture checks, SMTP, backup, API tokens, TLS.
14. **User self-service portal**: my devices, download clients, create my own WG config (if permitted), revoke a lost device.

### 10.2 UI/UX requirements
- **UX-1** Modern, clean design: shadcn/ui + Tailwind, consistent design tokens, **dark and light themes** (system default), smooth subtle animations.
- **UX-2** Fully **responsive** (usable on mobile browsers).
- **UX-3** **Accessibility**: WCAG 2.2 AA, keyboard navigation, screen-reader labels.
- **UX-4** **Command palette** (Ctrl/Cmd+K) for quick navigation and actions.
- **UX-5** Real-time updates (WebSocket/SSE) of device status, with no manual refresh.
- **UX-6** Empty states, skeleton loaders, inline validation, undo for destructive actions where possible, and confirmations for dangerous ones.
- **UX-7** **i18n-ready** (English first).
- **UX-8** No external fonts, CDNs, or analytics loaded at runtime: everything is bundled and served by the server (supports P1 and air-gapped use).
- **UX-9** Client apps share the same visual language as the web console.

---

## 11. Security requirements

### 11.1 Cryptography and keys
- **SEC-1** The data plane uses standard WireGuard (Noise IK, Curve25519, ChaCha20-Poly1305, BLAKE2s). No custom crypto.
- **SEC-2** Device private keys are **generated on the device and never leave it**. The server stores only public keys. (Exception: the opt-in server-generated WG configs in WG-2, which are never stored after download.)
- **SEC-3** **Post-quantum protection** (SHOULD): rotating pre-shared keys derived from a hybrid PQ key exchange (ML-KEM / Rosenpass-style) between native peers.
- **SEC-4** Agent ↔ server channel is authenticated and encrypted (TLS 1.3 and/or Noise with a device-key-bound identity). Certificate pinning SHOULD be optional.
- **SEC-5** Secrets at rest (setup keys, OIDC client secrets, API tokens, gateway keys) MUST be **encrypted** in the database (envelope encryption with a master key from file/env/KMS). Tokens are stored hashed where possible.

### 11.2 Server hardening
- **SEC-10** **Automatic TLS** via ACME (Let's Encrypt / ZeroSSL / custom CA), or user-provided certificates. HTTP → HTTPS redirect, HSTS. Full requirements in §13.7.
- **SEC-11** Strict security headers (CSP, frame-ancestors, etc.), CSRF protection, secure, HttpOnly, SameSite cookies.
- **SEC-12** **Rate limiting** and brute-force protection on login and API. Account lockout with backoff.
- **SEC-13** All admin actions recorded in a **tamper-evident audit log** (hash-chained).
- **SEC-14** Runs as an unprivileged user wherever possible. Only required capabilities (e.g. `CAP_NET_ADMIN`). Docker image is distroless/minimal, non-root where feasible, read-only rootfs.
- **SEC-15** Input validation on all endpoints. Parameterised SQL only.
- **SEC-16** Short-lived sessions with refresh, session list with remote logout, and admin re-authentication for sensitive actions.
- **SEC-17** Optional restriction of the admin console to the VPN itself (admin UI reachable only from the overlay network).

### 11.3 Supply chain and transparency
- **SEC-20** **Reproducible builds**, **signed releases** (Sigstore/cosign + checksums), **SBOM** for every release.
- **SEC-21** **Android APK signing with the project's own key** (MUST, from day one; APK Signature Scheme v2/v3, key stored offline, certificate fingerprint published). Windows Authenticode signing and macOS Developer ID signing + notarisation are **deferred** (no certificates yet). Until then, every artefact is verifiable through SHA-256 checksums and cosign/Sigstore signatures (free, keyless), and the build pipeline MUST be ready to add OS signing later without restructuring.
- **SEC-22** Dependency scanning (govulncheck, npm audit), SAST, and fuzzing of parsers and protocol handlers in CI.
- **SEC-23** Public **SECURITY.md**, a responsible disclosure process, and an independent security audit before v1.0 GA (recommended).
- **SEC-24** Clients verify update signatures. Updates are **never automatic without consent**, and update checks are opt-in, direct to the operator's own server or GitHub only (P1).

### 11.4 Privacy (zero data collection)
- **PRV-1** No telemetry, analytics, or crash reporting in any component.
- **PRV-2** No traffic logging, no DNS query logging, and no flow logs by default. Flow logs MAY be enabled explicitly by the operator for their own network, stored locally only, with a retention limit.
- **PRV-3** Connection metadata (last seen, endpoint IP) is kept only as needed for operation, with configurable retention/purge.
- **PRV-4** Data export and full account/device deletion are available to users.
- **PRV-5** Debug bundles are redacted (keys, tokens, public IPs optional) and only ever saved locally.

---

## 12. Performance requirements

| ID | Requirement |
|---|---|
| PERF-1 | Use **kernel WireGuard** on Linux and **WireGuardNT** on Windows for near line-rate throughput |
| PERF-2 | Optimised userspace path: UDP **GSO/GRO**, batched send/receive (`sendmmsg`/`recvmmsg`), checksum offload, zero-copy where possible |
| PERF-3 | Target **≥ 90% of raw kernel WireGuard throughput** for direct P2P links on the same hardware |
| PERF-4 | Direct connection established in **< 2 s** typical on common NATs. Relay fallback in **< 5 s** |
| PERF-5 | Network change / roaming recovery in **< 3 s** |
| PERF-6 | Policy/config propagation to online agents in **≤ 5 s** |
| PERF-7 | Agent idle footprint: **< 30 MB RAM**, ~0% CPU idle. Mobile battery impact minimised (no unnecessary keepalives, push-style updates) |
| PERF-8 | A single server (4 vCPU / 8 GB) SHOULD handle **≥ 5,000 devices** on PostgreSQL and **≥ 500** on SQLite |
| PERF-9 | **Incremental network map updates** (send diffs, not the full map) to scale |
| PERF-10 | Relay can forward **multi-Gbps** on modest hardware and is horizontally scalable |
| PERF-11 | Web UI: initial load < 2 s on broadband, with code-split bundles |

A public, reproducible **benchmark suite** SHOULD be part of the repository.

---

## 13. Server deployment and operations

### 13.1 Deployment options
- **OPS-1** **Single static binary** for Linux (amd64/arm64/armv7) and Windows (amd64/arm64).
- **OPS-2** **Linux service**: systemd unit with hardening (`ProtectSystem`, `NoNewPrivileges`, etc.).
- **OPS-3** **Windows service**: installs/uninstalls via the binary or MSI, with Event Log integration.
- **OPS-4** **Docker**: official multi-arch image + **docker-compose** examples (SQLite simple setup; PostgreSQL + reverse proxy setup).
- **OPS-5** SHOULD provide a **Helm chart** for Kubernetes.
- **OPS-6** One-line install script (optional, readable, verifiable).
- **OPS-7** Works **standalone with its built-in web server and automatic TLS (default)**, or behind an external reverse proxy (Caddy, Nginx, Traefik). See §13.7.
- **OPS-8** Minimal ports: **443/TCP** (UI, API, signal, relay fallback), **3478/UDP** (STUN), **one UDP port** for relay/gateway (e.g. 51820). All configurable.

### 13.2 Database
- **DB-1** **SQLite is the default**, with no extra setup (WAL mode, auto-migrations).
- **DB-2** **PostgreSQL** is supported for larger and HA deployments.
- **DB-3** Versioned, automatic, **reversible schema migrations**.
- **DB-4** Built-in **migration tool SQLite → PostgreSQL**.
- **DB-5** **Backup and restore**: scheduled built-in backups (encrypted), plus a CLI backup/restore command.

### 13.3 Configuration
- **CFG-1** Config file (YAML/TOML), environment variables, and CLI flags, with documented precedence.
- **CFG-2** Most settings manageable from the web UI after initial setup.
- **CFG-3** Configuration validation with clear error messages on startup.

### 13.4 Observability (local only)
- **OBS-1** Structured logs (JSON or text), configurable levels, log rotation.
- **OBS-2** **Prometheus metrics** endpoint (opt-in, local, protected), plus an example Grafana dashboard.
- **OBS-3** Health/readiness endpoints for Docker/Kubernetes.
- **OBS-4** OpenTelemetry tracing MAY be supported (opt-in, operator's own collector only).

### 13.5 High availability and scale (SHOULD, v1.x)
- **HA-1** Multiple management server instances behind a load balancer, sharing PostgreSQL.
- **HA-2** Multiple relays in different regions, plus HA gateways.
- **HA-3** Clients keep working on the cached network map during control-plane outages (see CL-7).

### 13.6 Upgrades
- **UPG-1** Zero-downtime or minimal-downtime upgrades. Clients tolerate server version skew (N-1 compatibility at minimum).
- **UPG-2** Admin UI shows the client version distribution and outdated devices.

### 13.7 Built-in web server and automatic SSL
The server ships with its **own lightweight, production-grade web server**, so no separate Nginx/Caddy is needed. It is built in Go on the same TLS/ACME engine that Caddy uses (CertMagic), so it behaves like Caddy without being a separate process to manage.

**Serving**
- **WEB-1** Serves the React admin console (embedded in the binary), the REST API, the agent gRPC/Connect API, signal, WebSocket relay fallback, and the **client download page** (installers/APKs hosted on the operator's server). Everything runs on a single port 443.
- **WEB-2** **HTTP/2** MUST be supported. **HTTP/3 (QUIC)** SHOULD be supported.
- **WEB-3** Compression (gzip + Brotli/zstd) for static assets, long-term cache headers for hashed assets, and SPA fallback routing.
- **WEB-4** Security headers on by default: HSTS, CSP, X-Content-Type-Options, Referrer-Policy, Permissions-Policy, and frame-ancestors `none`.
- **WEB-5** Per-IP **rate limiting**, request size limits, sensible timeouts (slowloris protection), and connection limits.
- **WEB-6** Access logs: off or minimal by default (P1), optional, local only, with IP anonymisation and a retention limit.
- **WEB-7** Optional **IP allow-list** for the admin console (in addition to SEC-17).
- **WEB-8** Port 80 is used only for ACME HTTP-01 challenges and redirecting to HTTPS.
- **WEB-9** Graceful reload of certificates and configuration with **no dropped connections**.

**Automatic SSL (ACME)**
- **TLS-1** **Fully automatic certificates from Let's Encrypt**: the operator enters only the domain (and optionally an email). Certificates are issued on first start and **renewed automatically** (about 30 days before expiry) with no restarts.
- **TLS-2** Challenge types: **HTTP-01**, **TLS-ALPN-01**, and **DNS-01**. DNS-01 enables wildcard certificates and servers that aren't reachable from the internet on 80/443. It needs built-in providers for common DNS hosts (Cloudflare, Route 53, DigitalOcean, Hetzner, Google Cloud DNS, deSEC, RFC 2136), with credentials encrypted at rest.
- **TLS-3** **Fallback CA**: automatically try **ZeroSSL** (or another configured ACME CA) if Let's Encrypt is unavailable or rate-limited.
- **TLS-4** Support for the **Let's Encrypt staging** environment for testing, plus clear errors when issuance fails (DNS not pointing to the server, port blocked, rate limit).
- **TLS-5** **OCSP stapling** and modern TLS only: TLS 1.2 minimum, TLS 1.3 preferred, strong cipher suites only.
- **TLS-6** **Multiple domains/SANs** (e.g. `vpn.example.com`, plus separate relay hostnames like `relay-eu.example.com`).
- **TLS-7** Alternative modes for setups without a public domain:
  - **Custom certificate**: upload or point to cert/key files, with auto-reload on change.
  - **Internal CA**: the server generates its own local CA for LAN, air-gapped, or IP-only installs. Clients pin the CA during enrolment.
  - **Behind a reverse proxy**: TLS off, with trusted-proxy headers (`X-Forwarded-*`, PROXY protocol) correctly honoured.
- **TLS-8** The certificate store lives in the server's data directory (or in the database for HA), so all HA instances share certificates with a distributed lock to avoid duplicate issuance.
- **TLS-9** The admin UI shows certificate status: issuer, domains, expiry, last renewal, and errors, with a warning notification before expiry if renewal keeps failing.
- **TLS-10** The setup wizard configures TLS: choose *Let's Encrypt (automatic)* / *DNS challenge* / *Own certificate* / *Internal CA* / *Behind proxy*.

**Optional external proxy**
- **WEB-20** Official **docker-compose examples** with **Caddy** (recommended) and **Nginx** for operators who prefer a separate proxy, including correct WebSocket, gRPC/HTTP2, and long-lived stream settings.
- **WEB-21** The documentation lists the exact proxy requirements (headers, timeouts, h2c/gRPC passthrough, UDP ports that cannot be proxied).

---

## 14. APIs and automation
- **API-1** Full **REST API** covering everything the UI can do, documented with **OpenAPI 3**. The UI uses only the public API.
- **API-2** **API tokens** (personal + service accounts) with scopes and expiry.
- **API-3** **Webhooks** for events (device added, approval needed, key expiring, policy changed), signed with HMAC.
- **API-4** **CLI** covering all admin operations (scriptable, JSON output).
- **API-5** SHOULD provide a **Terraform provider** and an official Go SDK later.
- **API-6** Local client API (Unix socket / named pipe) for the client UI and third-party integrations, access-restricted to the local user/admin.

---

## 15. Additional recommended features

These are suggested additions, in priority order (to be confirmed):

| Priority | Feature | Description |
|---|---|---|
| High | **Exit node selection by location** | Labels/regions for exit nodes, with auto-select of the fastest |
| High | **Network map visualisation** | Live topology graph in the admin UI |
| High | **Docker / container sidecar mode** | Join containers to the mesh (userspace networking, no `NET_ADMIN` needed) |
| High | **Router / NAS packages** | OpenWrt, pfSense/OPNsense, Synology/QNAP: as standard WG peers or with the native agent where possible |
| Medium | **Built-in SSH over the mesh** | Identity-based SSH access controlled by ACLs, with no keys to distribute |
| Medium | **Peer-to-peer file transfer** | Send files directly between your devices over the mesh (like Taildrop) |
| Medium | **Expose service to internet (Funnel-style)** | Optionally publish an internal service through the server with automatic TLS. Off by default |
| Medium | **Network sharing** | Share a single device with a user from another network/tenant without full membership |
| Medium | **Just-in-time access requests** | A user requests temporary access, and an admin approves it in the UI |
| Medium | **Bandwidth limits / QoS** | Per-user or per-device rate limits on exit nodes and gateways |
| Low | **Wake-on-LAN** through a subnet router | Wake home machines remotely |
| Low | **Obfuscation transport** | Disguise relay traffic as normal HTTPS for censorship-heavy networks |
| Low | **Desktop "Lock-down" mode** | Admin-enforced settings (can't disconnect, forced exit node) via MDM/GPO profiles |

---

## 16. Decisions

### 16.1 Resolved
| # | Decision | Outcome |
|---|---|---|
| D1 | Android UI tech | **Kotlin + Jetpack Compose** |
| D2 | Desktop shell | **Wails** |
| D3 | License | **AGPL-3.0** for all components |
| D7 | Code signing | **Android: own release key (now).** Windows Authenticode and Apple Developer ID: **deferred**. Releases are verified through checksums + cosign until then. |
| D8 | Web server / TLS | **Built-in web server with automatic Let's Encrypt** (Caddy's CertMagic engine). Caddy/Nginx are optional external proxies. |
| D4 | Client ↔ server protocol | **ConnectRPC (gRPC-compatible) with Protobuf over TLS 1.3 + device-key authentication.** Full specification in §16.4. |
| D5 | Product name / domain | **Gorget**, with default network domain **`gorget.internal`** |
| D9 | VPN IP range | **Customisable**. Default `100.80.0.0/16` + random ULA `/48` (§4.0) |

### 16.2 Still open
| # | Decision | Recommendation |
|---|---|---|
| D6 | macOS distribution | v1: unsigned `.pkg` + Homebrew with a `utun` daemon. Move to a signed + notarised Network Extension once an Apple Developer ID exists. |

### 16.3 Consequences of deferred code signing
- Windows shows a **SmartScreen warning** on first install ("More info → Run anyway"). This is documented in the install guide.
- macOS **Gatekeeper blocks** the first launch until the user allows it in Privacy & Security. Apple's Network Extension / system extension APIs **require** a Developer ID, which is why macOS v1 uses the `utun` daemon approach instead.
- Enterprise environments with strict policies (AppLocker, MDM) may refuse unsigned binaries. Signing should be added before targeting business users.
- Android is unaffected: self-signed APKs are standard. The **release key MUST be backed up securely**, because losing it makes updates to existing installs impossible.

### 16.4 Client ↔ server protocol (D4 specification)
**Choice: ConnectRPC + Protobuf over TLS 1.3, with device-key authentication.**

Why this choice:
- **Go-native and fast.** Protobuf is compact and quick to encode, which matters for battery life on Android.
- **One schema for every client.** The same `.proto` definitions generate code for Go (server, daemon, desktop), Kotlin (Android), and TypeScript (web UI), so there is no hand-written API drift.
- **Works through proxies.** ConnectRPC speaks gRPC, gRPC-Web, and the Connect protocol, over **HTTP/2 or plain HTTP/1.1**. Clients behind corporate proxies or Nginx setups that break gRPC still work, all on port 443 through the built-in web server.
- **Real-time streaming.** Server-streaming RPCs push changes instantly, with no polling.

Requirements:
- **PROTO-1** **Transport:** TLS 1.3 only for client connections (§13.7), HTTP/2 preferred with automatic fallback to HTTP/1.1 streaming. Optional pinning of the server certificate or internal CA, set at enrolment.
- **PROTO-2** **Device authentication (two layers):**
  1. TLS authenticates the **server** to the client.
  2. Each device has a dedicated **Ed25519 machine key**, separate from its WireGuard key and generated on the device. It authenticates itself by **signing a server challenge** at session start, and receives a short-lived session token bound to that key. Bearer tokens alone are never sufficient.
- **PROTO-3** **Enrolment:** the first registration uses an SSO/browser login or a setup key. The server binds the machine key and the WireGuard public key to the device record. Private keys never leave the device.
- **PROTO-4** **Network map streaming:** a long-lived server-stream delivers the initial full map, then **incremental diffs** (peers, ACL rules, routes, DNS, settings) with sequence numbers. On reconnect, the client resumes from its last sequence, with a full resync only when needed.
- **PROTO-5** **Signal messages** (NAT traversal candidates exchanged between peers) are **end-to-end encrypted between the two peers** (sealed with their keys), so the server only relays opaque blobs (P5).
- **PROTO-6** **Keepalive and reconnection:** application-level heartbeats, exponential backoff with jitter, and fast reconnect on network change. Mobile clients use longer heartbeats and rely on the stream instead of polling, to save battery.
- **PROTO-7** **Versioning:** protobuf schemas are versioned (`gorget.v1`) and linted with breaking-change detection in CI. The server supports at least the current and previous client major versions (UPG-1). Clients announce their version and capabilities, so features can be negotiated.
- **PROTO-8** **Separation of APIs:**
  - *Client API* (ConnectRPC): daemon ↔ server.
  - *Admin/public API*: REST/JSON with an OpenAPI spec for operators, scripts, and the web UI (API-1). The web UI MAY also use Connect's JSON mode for real-time views.
  - *Local API*: daemon ↔ desktop UI/CLI over a Unix socket or Windows named pipe, also Connect/Protobuf, with OS-level permission checks (API-6).
- **PROTO-9** **Abuse protection:** per-device rate limits, maximum stream count, message size limits, and revocation that immediately terminates the device's active streams.

---

## 17. Out of scope (v1)
- iOS / iPadOS clients.
- Any vendor-hosted / SaaS offering or cloud dependency.
- Non-WireGuard VPN protocols (OpenVPN, IPsec).
- Telemetry or usage analytics of any kind (permanently out of scope).

---

## 18. Suggested release roadmap

| Phase | Content |
|---|---|
| **M1: Foundation** | Management server (Go, SQLite), local auth + MFA, device registration with setup keys, Linux agent + CLI, direct P2P mesh with STUN/hole punching, relay fallback, basic ACL, internal DNS, web UI basics, **built-in web server with automatic Let's Encrypt TLS**, Docker + systemd deployment |
| **M2: Compatibility & routing** | WireGuard Gateway for standard clients (QR/config, full/split), exit nodes, subnet routers, kill switch, LAN access toggle, custom/split DNS, Windows agent + desktop UI |
| **M3: Platforms & identity** | macOS app, Android app, OIDC SSO, device approval, key expiry/rotation, policy-as-code + tests + simulator, audit log, PostgreSQL, Windows service |
| **M4: Polish & hardening** | Network map, posture checks, app-based split tunneling, trusted networks, captive portal handling, metrics, backups, performance tuning and benchmarks, external security audit → **v1.0** |
| **M5+** | HA, multi-region relays, PQ pre-shared keys, SSH, file transfer, Terraform, Helm, router packages, the rest of §15 |

---

## 19. Acceptance criteria (v1.0 summary)
1. Fresh install via Docker or a single binary reaches a working network in **< 10 minutes**.
2. Windows, macOS, Linux, and Android native clients connect P2P across different NATs, with relay fallback on UDP-blocked networks.
3. A standard WireGuard client (official app) imports a QR/config and works in both full-tunnel and split-tunnel modes, with ACLs enforced.
4. A native client can switch between mesh-only and full-tunnel (exit node) without reconnecting, with no DNS/IPv6 leaks (verified by leak tests).
5. Subnet routing gives access to non-agent LAN devices, under ACL control.
6. An ACL change propagates in ≤ 5 s, and denied traffic is verifiably blocked.
7. Network inspection shows **zero outbound connections** from any component other than to the operator's own server(s), relays, STUN, and configured DNS upstreams.
8. All release artefacts come with SHA-256 checksums, cosign signatures, and an SBOM. Android APKs are signed with the project release key.
9. With only a domain name configured, the server obtains a valid Let's Encrypt certificate on first start, renews it automatically, and scores **A+ on SSL Labs**, all without any external web server.
