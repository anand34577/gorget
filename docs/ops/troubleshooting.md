# Troubleshooting

| Symptom | Check |
|---|---|
| App says the service isn't running | Windows: *Gorget* service started? Linux: `systemctl status gorget`. Run `gorget status`. |
| "access denied" from `gorget up` | Ask an administrator for `gorget operator set YOUR_USER`, or run with sudo/as Administrator. |
| Device shows **Blocked** | The message lists the [device health](../guide/posture.md) rules it breaks; fix them and it connects by itself. |
| Device waits for approval | An administrator approves it under **Devices** (or use a setup key with auto-approve). |
| Devices only connect through the relay | `gorget netcheck`. UDP may be blocked on one side; relay works but is slower. Open UDP 3478/3479 on the server; hard NATs on both sides need the relay. |
| Can reach a device by IP but not by name | **Use Gorget DNS** is on? On Linux without systemd-resolved only DNS override works. |
| A device can't reach another | **Devices → (device) → Access** shows exactly what the rules allow. Use the simulator under **Access rules**. |
| Can't reach a subnet | Route approved? A rule allowing the subnet's addresses? The router device must forward (Linux/macOS). |
| Exit node: no internet | The exit node must be approved, online, and allowed by `autogroup:internet`; on Windows it cannot NAT. |
| Certificate errors | **Settings → System**. Ports 80/443 reachable for `acme`? Check the error shown there. |
| WireGuard apps can't connect | **Settings → System → gateway**: Linux or macOS server, `NET_ADMIN`, UDP 51820 open. |
| "challenge expired or unknown" at registration | Clock skew over a minute between device and server, or a load balancer in front of a non-clustered second server. |
| Forgot the owner password | `gorget-server reset-password -email …` |

Turn on debug logs with `log_level: debug` on the server or `gorget daemon -debug` on a device; `GORGET_WG_DEBUG=1` adds WireGuard handshake details (very chatty).
