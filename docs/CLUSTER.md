# High availability (cluster)

Run several `gorget-server` instances against **one PostgreSQL database**. They share all state, so the console and control API can sit behind a load balancer, an instance can fail or be upgraded without a gap, and every instance is also a relay, so devices use the nearest one.

## What is shared

| What | How |
|---|---|
| People, devices, rules, settings, audit log | the database (as always) |
| Which device is connected where | `presence` rows refreshed every 10 s |
| "Something changed, rebuild" | PostgreSQL `LISTEN/NOTIFY` between instances |
| Peer-to-peer signalling, notices, live console events | `NOTIFY` messages |
| Sign-in ceremonies and device login challenges | short-lived database rows (any instance can finish them) |
| TLS certificates, ACME account, challenge locks | database key/value storage and locks |
| Background jobs (cleanup, expiry) | run on one elected **leader** |
| Relay packets between devices on different instances | forwarded over UDP between the instances (authenticated with a key derived from the master key) |

## Set it up

All instances need the **same master key** and the same `database.dsn` (PostgreSQL).

```yaml
database: {driver: postgres, dsn: "postgres://gorget:…@db.internal/gorget"}
security: {master_key: "<same base64 key on every instance>"}
cluster:
  enabled: true
  instance_id: eu-1                          # unique per instance
  relay_url: wss://eu.vpn.example.com/relay  # how clients reach THIS instance's relay
  peer_listen: ":3480"                       # UDP; private network only
  peer_addr: 10.0.0.5:3480                   # how the other instances reach this one
relay: {region: eu, udp_listen: ":3479", udp_advertise: "eu.vpn.example.com:3479"}
```

- **Web console and control API** can use one name behind a load balancer: nothing is stored in memory, so any instance can answer any request.
- **Relays:** give each instance its own name (as above) so clients can pick the nearest. If you use only one shared name, it still works (packets are forwarded between instances), just not locality-aware.
- The **WireGuard gateway** has a fixed key and one public endpoint: enable it on a single instance (`gateway.enabled: true` there only).
- Keep `peer_listen` on a private network or firewall it to the other instances: it accepts only authenticated packets, but there is no reason to expose it.

## Behaviour

- Losing an instance: its devices reconnect to another within seconds; presence rows expire after 45 s and are repaired by heartbeats.
- Losing the database stops changes (and new devices) but running connections keep working.
- Leadership moves automatically (30 s lease); only one instance runs periodic cleanup.
- **Settings → System → Server cluster** lists the live instances, their relay URLs and the leader.

## Kubernetes

The Helm chart does this for you when `replicaCount > 1` and `database.driver=postgres` ([Kubernetes](ops/kubernetes.md)).
