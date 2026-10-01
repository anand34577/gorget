# HTTPS certificates

`tls.mode` chooses where certificates come from.

| Mode | Use when |
|---|---|
| `acme` | The server is reachable on ports 80/443 from the internet. Let's Encrypt via HTTP-01 and TLS-ALPN-01, with an optional ZeroSSL fallback (`tls.zerossl_api_key`). |
| `acme-dns` | The server is not reachable from the internet, or you want a wildcard. DNS-01 with Cloudflare, DigitalOcean, Hetzner, deSEC, Route 53 or RFC 2136 (`tls.dns_provider`, `tls.dns_config`). |
| `custom` | You bring a certificate (`tls.cert_file`, `tls.key_file`); it is reloaded when the files change. |
| `internal-ca` | A private network without a public name: Gorget runs a small CA (`/ca.crt`) that devices can trust. |
| `off` | A reverse proxy (Caddy, Nginx, a Kubernetes ingress) terminates TLS. Set `http.trusted_proxies` so client addresses are right. |

```yaml
tls:
  mode: acme
  email: admin@example.com
  # staging: true            # try without hitting rate limits
  # acme_directory: https://ca.example/acme/directory   # a private ACME CA such as step-ca
```

## In a cluster

With `cluster.enabled`, certificates, the ACME account and challenge state are kept in the shared database and issuance is guarded by a database lock, so all instances serve the same certificate and only one talks to the CA at a time. `internal-ca` is not available in clusters.

## Checking

**Settings → System → HTTPS certificate** shows the issuer, expiry and any error. Renewal starts 30 days before expiry.
