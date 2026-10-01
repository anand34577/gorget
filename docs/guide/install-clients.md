# Install the apps

All apps connect to your server's address (for example `vpn.example.com`) and sign in through your browser.

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

## Linux

```sh
sudo apt install ./gorget_0.3.0_amd64.deb            # the service and CLI (nftables is a dependency)
sudo apt install ./gorget-desktop_0.3.0_amd64.deb    # optional tray app (GTK, WebKitGTK)
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
