# Benchmarks

Benchmarks live next to the code and run with the standard Go tooling:

```sh
make bench
# or one at a time
go test -run '^$' -bench . -benchmem ./internal/policy          # policy engine
go test -run '^$' -bench . -benchmem ./client/tunx              # per-packet firewall
go test -run '^$' -bench . -benchmem ./client/disco ./client/pq # discovery crypto, post-quantum exchange
go test -run '^$' -bench Throughput -benchtime 3x ./client      # tunnel throughput, direct vs relay
```

| Benchmark | Measures |
|---|---|
| `internal/policy` `BenchmarkCompile` | recomputing all access rules after a change, for 100 / 1000 / 5000 devices |
| `internal/policy` `BenchmarkPeers` | listing the peers of one device |
| `client/tunx` `BenchmarkFilter`, `BenchmarkParse` | cost of the inbound firewall per packet |
| `client/disco` `BenchmarkSealOpenPing`, `…PQInit` | one NAT-traversal probe, one post-quantum key-exchange message |
| `client/pq` `BenchmarkExchangeCrypto` | CPU for one ML-KEM-768 exchange (both sides) |
| `client` `BenchmarkTunnelThroughput` | bulk TCP between two clients through the real engine, over a direct path and over the relay |

The throughput benchmark runs both clients in one process on an in-memory network stack, so it measures Gorget's own overhead (WireGuard, path selection, firewall, relay) and not your network. On Linux the UDP socket layer batches up to 32 packets per system call (`recvmmsg`/`sendmmsg`); UDP GSO/GRO are not used yet.

## Publishing results

Run `make bench` on the hardware you want to describe and attach the output (`go test -bench` prints ns/op, B/op, allocs/op and MB/s) together with the CPU model, OS and Go version. Results on one machine say little about another: compare the *ratios* (direct vs relay, 100 vs 5000 devices) rather than the absolute numbers.
