# DNS

Every device is reachable by name (`laptop.gorget.internal`; the domain is yours to choose and defaults to `gorget.internal`, which is reserved for private use so it never clashes with a real domain).

## How it works

- Gorget apps answer DNS **inside the tunnel** at `100.100.100.100`. On desktops only your network's domains go there, unless **DNS override** or an exit node is on; then every query does, so nothing leaks around the tunnel.
- The server's gateway answers on its own overlay address for standard WireGuard apps.

## Settings (Console → DNS)

| Setting | Use |
|---|---|
| Device names | Turn names on or off |
| Upstream resolvers | `1.1.1.1`, `1.1.1.1:53`, `https://dns.example/dns-query` (DoH), `tls://9.9.9.9` (DoT) |
| Split DNS | Send `corp.example.com` to your internal resolver only |
| Custom records | `A`, `AAAA` and `CNAME` names inside your domain; wildcards such as `*.apps` or `*.apps.home.lan` answer every name below them (exact names win) |
| Search domains | Short names like `nas` |
| Override local DNS | Send every query to Gorget (also used while an exit node is on) |
| Fail open | Fall back to system DNS if all upstreams fail (otherwise fail closed) |

Replies are cached, reverse lookups work for device addresses, and answers are only given to devices on your network.

## Domain routing

Names can also decide *which path* traffic takes: see [domain routing](domain-routing.md).
