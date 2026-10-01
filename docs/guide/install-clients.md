# Install the apps

All apps connect to your server's address (for example `vpn.example.com`) and sign in through your browser.

## Linux and macOS: one command

```sh
curl -fsSL https://vpn.example.com/install.sh | sh
```

Your server serves this installer with its own address filled in. It detects the system,
downloads the latest release from GitHub, checks it against the release checksums, installs
it (a `.deb` or `.rpm` where it can, the macOS package on a Mac, otherwise a plain binary
plus a system service), makes you the operator so you don't need `sudo` later, and opens
the sign-in page.

| Variation | Command |
|---|---|
| A server without a browser (setup key) | `curl -fsSL https://vpn.example.com/install.sh \| GORGET_SETUP_KEY=gsk_… sh` |
| Install only, connect later | `curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/install.sh \| sh` |
| A specific version | `… \| GORGET_VERSION=v0.4.0 sh` |
| Remove Gorget | `curl -fsSL https://vpn.example.com/install.sh \| sh -s -- --uninstall` |

Prefer reading a script before running it? Download it first:
`curl -fsSLo install.sh https://vpn.example.com/install.sh`, look at it, then `sh install.sh`.

## Android

Install the APK from the releases page (verify its checksum and signature) or from F-Droid. On first start choose **Connect**, allow the VPN permission, and sign in. See [Gorget for Android](../ANDROID.md) for building from source.

Handy settings: *Always-on VPN* (Android settings), the quick-settings tile and the widget; *Block connections without VPN* gives you a kill switch.

## Windows

Run the MSI installer (as an administrator). It installs the **Gorget** service (which creates the network adapter) and the tray app, and makes the installing user the *operator* so the tray app works without elevation.

```powershell
gorget up -server vpn.example.com
```

## macOS

Open the `.pkg`. It installs the service (a launchd daemon), the command-line tool and the **Gorget** menu-bar app. macOS may ask you to allow the installer in *System Settings → Privacy & Security* because the package is not notarised yet.

## Linux, by hand

```sh
sudo apt install ./gorget_0.4.0_amd64.deb            # the service and CLI (nftables is a dependency)
sudo apt install ./gorget-desktop_0.4.0_amd64.deb    # optional tray app (GTK, WebKitGTK)
gorget up -server vpn.example.com
```

RPM and an AUR package are available too. For servers without a browser, create a [setup key](first-steps.md#servers-and-routers) and run `gorget up -server … -setup-key gsk_…`.

## Who may control the service

The service runs as root/Administrator. Anyone on the computer may *read* the status. Changing things needs root/Administrator or the **operator**:

```sh
sudo gorget operator set alice      # alice may use gorget and the tray app without sudo
gorget operator                     # who is the operator, and may I control?
```

## The command line

```text
gorget up | down | status | login | logout        connect, disconnect, show devices
gorget exit-node [set NAME | none]                use another device for internet traffic
gorget set -use-dns=false -kill-switch=true …     change settings
gorget netcheck                                   how this device reaches the network
gorget file cp FILE… DEVICE | list | get          send and receive files between your devices
gorget ssh [USER@]DEVICE                          ssh by device name
gorget run -bypass -- COMMAND                     keep one program outside the tunnel (Linux)
```
