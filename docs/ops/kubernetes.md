# Kubernetes (Helm)

```sh
helm install gorget ./deploy/helm/gorget \
  --set publicUrl=https://vpn.example.com \
  --set masterKey.value=$(gorget-server gen-master-key) \
  --set ingress.enabled=true --set ingress.host=vpn.example.com
kubectl exec deploy/gorget-gorget -- gorget-server setup-link
```

The default is one replica with SQLite on a volume and TLS terminated by the ingress. The chart:

- exposes the web console and API through an `Ingress` (long timeouts for the relay and streams);
- exposes STUN (UDP 3478), the UDP relay (3479) and, if you enable it, the WireGuard gateway (51820) through a separate `LoadBalancer` Service (`gorget-gorget-udp`). Set `stun.advertise` and `relay.udp.advertise` to that address;
- keeps the master key in a Secret (`masterKey.existingSecret` for one you manage: back it up).

## More than one replica

```sh
helm upgrade gorget ./deploy/helm/gorget --reuse-values \
  --set database.driver=postgres --set database.dsn='postgres://…' --set replicaCount=3
```

Replicas become a [cluster](../CLUSTER.md): each pod advertises its pod IP for forwarding relay packets between pods (UDP 3480, pod-to-pod only). With one shared ingress name the relay works through forwarding; give pods individual relay names (`extraEnv` `GORGET_CLUSTER_RELAY_URL`) if you want clients to choose by distance.

## Values worth knowing

| Value | Meaning |
|---|---|
| `tls.mode` | `off` behind an ingress, `acme` to let Gorget fetch certificates (use a `LoadBalancer` service, no ingress) |
| `database.existingSecret` | Secret with the key `dsn` |
| `gateway.enabled` | WireGuard gateway; needs `NET_ADMIN` (the chart adds it) |
| `metrics.enabled`, `metrics.token` | Prometheus endpoint on :9090 (a token is required) |
| `extraEnv` | any other `GORGET_*` variable |
