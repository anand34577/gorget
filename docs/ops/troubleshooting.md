# Troubleshooting

Start with the tool that finds most problems for you:

```sh
sudo gorget-server check     # on the server: configuration, DNS record, free ports, prerequisites
gorget status                # on a device: signed in? connected? which devices, direct or relayed?
gorget netcheck              # on a device: how it reaches the network
```

## Installing the server

| Symptom | Cause and fix |
|---|---|
| `check` says **"… doesn't resolve"** | The domain has no DNS record yet. Add an **A record** (and AAAA for IPv6) pointing at the server's public IP, wait a few minutes, run `check` again. |
| `check` says a **port is in use** | Another program (often nginx, Apache or Caddy) owns port 80 or 443. Stop it, or [put Gorget behind it](../guide/install-server.md#c-behind-a-reverse-proxy). Find it with `sudo ss -ltnp 'sport = :443'`. |
| The page doesn't load, or the browser says **connection timed out** | The firewall blocks the ports. Open them on the machine (`ufw`, `firewalld`) **and** in the cloud provider's security group. See [ports](../guide/install-server.md#before-you-start). |
| Browser says **certificate error** right after install | The Let's Encrypt certificate takes a few seconds on first visit. If it persists, ports 80 and 443 aren't reachable from the internet, or the DNS record points elsewhere. Read the error in the log (`journalctl -u gorget-server`, or `docker compose logs gorget`). Try `GORGET_TLS_STAGING=true` while testing: Let's Encrypt limits repeated failures. |
| **Lost the setup link** | `sudo gorget-server setup-link` (Docker: `docker compose exec gorget gorget-server setup-link`). If it says no setup is pending, the owner account exists: sign in, or reset the password below. |
| **Forgot the owner password** | `sudo gorget-server reset-password -email you@example.com` prints a temporary one. |
| The installer says **"this release has no gorget-server for linux/…"** | Your processor type has no build. amd64, arm64 and arm are published. |
| **"docker: permission denied"** | Add yourself to the `docker` group, or run the installer with `sudo`. The installer retries with `sudo` on its own. |
| **"service is already installed"** | That's fine: use `sudo gorget-server restart`. To change the service's flags, `uninstall` and `install` again. |
| The service starts and stops immediately | `journalctl -u gorget-server -n 50` shows why: usually a configuration mistake (`gorget-server check` explains it) or a port in use. |
| Server is behind a router at home | Forward TCP 80, 443 and UDP 443, 3478, 3479, 51820 to the machine, and point your domain at your home's public IP (a dynamic DNS name works). |

## Installing and connecting devices

| Symptom | Check |
|---|---|
| The install command says **"couldn't download the release list"** | No internet from that machine, or GitHub is unreachable. Try `curl -I https://github.com`. |
| `curl … /install.sh` fails with a **certificate error** | The server's HTTPS isn't working yet; see above. With `internal-ca`, trust the server's CA first (download it from `https://SERVER/ca.crt`). |
| The Windows installer says **"this release has no Windows installer"** | The release you are installing has no `.msi` attached. Use a newer release (`$env:GORGET_VERSION='v…'`) or check the [Releases page](https://github.com/anand34577/gorget/releases). |
| App says the service isn't running | Windows: is the *Gorget* service started? Linux: `systemctl status gorget`. Then `gorget status`. |
| **"access denied"** from `gorget up` | You aren't the *operator*. Run with `sudo`, or have an administrator run `sudo gorget operator set YOUR_USER`. |
| Device shows **Blocked** | The message lists the [device health](../guide/posture.md) rules it breaks; fix them and it connects by itself. |
| Device waits for approval | An administrator approves it under **Devices** (or use a setup key with auto-approve). |
| **"challenge expired or unknown"** at registration | The device clock is off by more than a minute, or a load balancer sits in front of a non-clustered second server. |

## Once connected

| Symptom | Check |
|---|---|
| Devices only connect through the relay | `gorget netcheck`. UDP may be blocked on one side; the relay works but is slower. Open UDP 3478/3479 on the server; hard NATs on both sides need the relay. |
| Can reach a device by IP but not by name | Is **Use Gorget DNS** on? On Linux without systemd-resolved only the DNS override works. |
| A device can't reach another | **Devices → (device) → Access** shows exactly what the rules allow. Use the simulator under **Access rules**. |
| Can't reach a subnet | Is the route approved? Is there a rule allowing the subnet's addresses? The router device must forward (Linux/macOS). |
| Exit node: no internet | The exit node must be approved, online, and allowed by `autogroup:internet`; on Windows it cannot NAT. |
| Certificate errors in the console | **Settings → System** shows the certificate and its error. Ports 80/443 reachable for `acme`? |
| WireGuard apps can't connect | **Settings → System → gateway**: Linux or macOS server, `NET_ADMIN`, UDP 51820 open. |

## More detail

Turn on debug logs with `log_level: debug` on the server or `gorget daemon -debug` on a device;
`GORGET_WG_DEBUG=1` adds WireGuard handshake details (very chatty). Still stuck? Open an
[issue](https://github.com/anand34577/gorget/issues) with the output of `gorget-server check` or
`gorget status` and the relevant log lines (remove secrets first).
