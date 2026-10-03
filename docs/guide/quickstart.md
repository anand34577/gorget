# Quick start

From nothing to two connected devices in about 15 minutes.

## What you need

| | |
|---|---|
| **A server** | Any Linux machine with a public IP address: a small cloud VPS (1 CPU, 1 GB RAM is plenty), or a machine at home with ports forwarded. |
| **A domain name** | For example `vpn.example.com`, with a DNS **A record** pointing at the server's public IP. Create it first; DNS can take a few minutes to spread. |
| **Open ports** | TCP 80 and 443, UDP 443, 3478, 3479 and 51820. In a cloud console this is the *security group* or *firewall* page of your VPS. |
| **Root access** | `sudo` on the server. |

!!! note "No domain name?"
    You can still try Gorget on a private network with its own certificate authority. Run the
    installer and choose option 3 ("private or test setup") when it asks about HTTPS. Browsers
    warn until you trust the certificate, so use a real domain for anything lasting.

## Step 1: install the server

On the server, run one command (replace the domain with yours):

```sh
curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/install-server.sh | sh -s -- --domain vpn.example.com
```

Leave out `--domain …` and it asks you for it instead. It downloads Gorget, checks the download,
writes the configuration, starts the service and prints a **setup link**:

```text
Gorget is running. Finish setting it up in your browser:

  https://vpn.example.com/setup#token=…
```

Prefer Docker? Add `--docker` to the command. More choices are in [Install the server](install-server.md).

## Step 2: create your owner account

Open the setup link in your browser. Choose an email and a password, then turn on two-factor
sign-in under **Account & security** straight away. You are now in the web console.

## Step 3: connect your first device

On a Linux or Mac computer:

```sh
curl -fsSL https://vpn.example.com/install.sh | sh
```

On Windows, open PowerShell:

```powershell
irm https://vpn.example.com/install.ps1 | iex
```

On Android, install the app and enter `vpn.example.com`. Every method opens a browser sign-in; approve
it. See [Install the apps](install-clients.md) for all platforms.

## Step 4: connect a second device and test

Do step 3 on another device, then on the first one:

```sh
gorget status                     # lists your devices and how they are connected
ping laptop.gorget.internal       # use the other device's name, or its 100.80.x.x address
```

If the ping answers, you are done. The two devices talk directly to each other whenever they can.

## Something went wrong?

Run `sudo gorget-server check` on the server: it tells you what is wrong (DNS record, a port that is
in use, a missing package) and how to fix it. The [troubleshooting page](../ops/troubleshooting.md)
covers the common cases.

## What next

- [First steps](first-steps.md): share a home network, use an exit node, set up servers and routers.
- [Access rules](access-rules.md): by default everything can reach everything. Tighten it.
- [Your home network, from anywhere](homelab.md): a complete worked example.
- [Backups and upgrades](../ops/backups.md): do this now, while it is quick.
