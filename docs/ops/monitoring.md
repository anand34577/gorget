# Monitoring

## Health

- `GET /healthz`: the process is up. `GET /readyz`: the database answers.
- `gorget-server healthcheck` exits 0/1 for Docker health checks.

## Metrics

Prometheus metrics are served on a separate listener (`metrics.listen`, default `127.0.0.1:9090`; a bearer token is required if it listens beyond loopback).

```yaml
metrics: {enabled: true, listen: "10.0.0.5:9090", token: "…"}
```

They cover devices (total, online, pending), relay sessions and bytes, STUN requests, gateway state and policy version. Scrape with `Authorization: Bearer <token>`.

## Audit and activity

**Activity log** records every administrative action with a hash chain (`Verify` in the console, `GET /api/v1/audit/verify`) so tampering with stored history is detectable. Export it as CSV. Webhooks (signed with HMAC-SHA256) deliver events such as `device.created`, `device.online`, `policy.updated`, `access.requested` to your own systems.

## Client diagnostics

On a device: `gorget status`, `gorget netcheck` (relay and direct paths, public addresses). The daemon log is `gorget.log` in its data directory (`journalctl -u gorget` on Linux).
