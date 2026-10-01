# Gorget

A self-hosted, open-source WireGuard® mesh VPN. You run the server; nothing ever leaves it.

- **Mesh or full tunnel.** Devices talk to each other directly (only private traffic uses the VPN), or route all internet traffic through an exit node.
- **Works with any WireGuard app.** The built-in gateway gives the official WireGuard apps, routers and NAS boxes a QR code or config file.
- **Default-deny access rules** with groups, tags, ports, time limits, tests and a simulator, plus [temporary access](guide/temporary-access.md) people can request.
- **Device health.** Require an up-to-date, encrypted, firewalled device before it joins ([device health](guide/posture.md)).
- **Quantum-resistant.** Connections between Gorget devices also use an ML-KEM key exchange ([post-quantum protection](guide/post-quantum.md)).
- **DNS** for every device (`laptop.gorget.internal`), custom records, split DNS, DNS-over-HTTPS/TLS upstreams, and [domain routing](guide/domain-routing.md).
- **Built-in HTTPS** with automatic Let's Encrypt certificates, HTTP/2 and HTTP/3.
- **Scales out.** Several servers can share one PostgreSQL database ([high availability](CLUSTER.md)); every one is also a relay.
- **Zero telemetry.** No analytics, no phone-home, no external fonts or CDNs.

## Where to start

1. [Install the server](guide/install-server.md): Docker, a binary, or [Kubernetes](ops/kubernetes.md).
2. [Install the apps](guide/install-clients.md) on your computers and phones.
3. Take the [first steps](guide/first-steps.md): add devices, then tighten the [access rules](guide/access-rules.md).

## Apps

| Platform | App | Notes |
|---|---|---|
| Android | Gorget for Android (Kotlin, Compose) | Always-on, quick tile, widget, per-app routing |
| Windows | `gorget` service + tray app | Wintun, Windows Filtering Platform kill switch |
| macOS | `gorget` service + tray app | utun, pf kill switch |
| Linux | `gorget` service + CLI + tray app | nftables kill switch, per-app bypass |
| Anything else | The official WireGuard app | Through the server's gateway |

Gorget is licensed under the AGPL-3.0: every fork, including hosted ones, stays open source.
