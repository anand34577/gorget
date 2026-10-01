package config

import "runtime"

func isWindows() bool { return runtime.GOOS == "windows" }

const exampleYAML = `# gorget-server configuration
# Every key can also be set via environment variables (GORGET_<NAME>), e.g. GORGET_PUBLIC_URL.

public_url: https://vpn.example.com
data_dir: /var/lib/gorget
log_level: info        # debug, info, warn, error
log_format: text       # text, json

http:
  listen: ":443"
  redirect_listen: ":80"   # ACME HTTP-01 + redirect to HTTPS; "" disables
  http3: true
  trusted_proxies: []      # e.g. ["10.0.0.0/8"] when behind a reverse proxy
  access_log: false
  admin_allow_cidrs: []    # restrict admin console, e.g. ["100.80.0.0/16"]

tls:
  mode: acme               # acme | acme-dns | custom | internal-ca | off
  email: admin@example.com
  staging: false
  fallback_ca: zerossl
  # dns_provider: cloudflare   # for acme-dns: cloudflare, digitalocean, hetzner, desec, rfc2136
  # dns_config:
  #   api_token: "..."
  # cert_file: /etc/gorget/cert.pem   # custom mode
  # key_file: /etc/gorget/key.pem

database:
  driver: sqlite           # sqlite | postgres
  # dsn: postgres://gorget:secret@localhost:5432/gorget?sslmode=disable

stun:
  enabled: true
  listen: ":3478"

relay:
  enabled: true
  region: default
  udp_listen: ":3479"      # faster UDP relay; clients fall back to WebSocket when UDP is blocked. "" disables
  # udp_advertise: vpn.example.com:3479

# Several servers on one PostgreSQL database (see docs/CLUSTER.md).
cluster:
  enabled: false
  # instance_id: eu-1                    # unique per instance; defaults to the host name
  # relay_url: wss://eu.vpn.example.com/relay
  # peer_listen: ":3480"                 # UDP: relay packets forwarded between instances (keep private)
  # peer_addr: 10.0.0.5:3480             # how the other instances reach this one

gateway:
  enabled: true            # WireGuard gateway for standard WireGuard clients (Linux, macOS)
  interface: gorget0
  listen_port: 51820
  dns: true
  mtu: 1420

metrics:
  enabled: false
  listen: 127.0.0.1:9090

security:
  session_ttl: 12h
  admin_only_from_vpn: false

backup:
  interval: 24h
  keep: 7
`
