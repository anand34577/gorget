# Install the apps

Every app needs two things: **your server's address** (for example `vpn.example.com`) and a
browser to sign in with. You do not need to type a password into the app itself.

| Your device | Easiest way |
|---|---|
| [Linux or Mac](#linux-and-macos) | `curl -fsSL https://vpn.example.com/install.sh \| sh` |
| [Windows](#windows) | `irm https://vpn.example.com/install.ps1 \| iex` (PowerShell) |
| [Android](#android) | Install the APK, enter the address |
| [A server or router with no browser](#servers-and-routers-without-a-browser) | A setup key |
| [Anything else, or a router running WireGuard](first-steps.md#any-wireguard-app) | The official WireGuard app, from the console |

Replace `vpn.example.com` with your server. Your server serves these installers with its own address
filled in, so there is nothing else to type.

## Linux and macOS

```sh
curl -fsSL https://vpn.example.com/install.sh | sh
```

What it does: detects your system, downloads the latest release from GitHub, **checks it against
the release checksums**, installs it (a `.deb` or `.rpm` where it can, the macOS package on a Mac,
otherwise a plain binary plus a system service), lets you control Gorget without `sudo`, and opens
the sign-in page in your browser. On a Linux desktop it also installs the tray app.

Check that it worked:

```sh
gorget status
```

| Variation | Command |
|---|---|
| A machine without a browser (setup key) | `curl -fsSL https://vpn.example.com/install.sh \| GORGET_SETUP_KEY=gsk_… sh` |
| Install now, connect later | `curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/install.sh \| sh` |
| A specific version | `… \| GORGET_VERSION=v0.4.0 sh` |
| No tray app on a desktop | `… \| GORGET_NO_TRAY=1 sh` |
| Remove Gorget | `curl -fsSL https://vpn.example.com/install.sh \| sh -s -- --uninstall` |

Prefer reading a script before running it? Download it first:
`curl -fsSLo install.sh https://vpn.example.com/install.sh`, read it, then run `sh install.sh`.

**By hand on Linux:**

```sh
sudo apt install ./gorget_<version>_amd64.deb            # the service and CLI (nftables is a dependency)
sudo apt install ./gorget-desktop_<version>_amd64.deb    # optional tray app
gorget up -server vpn.example.com
```

RPM packages and an AUR package are available too.

**By hand on a Mac:** open `gorget_<version>_macos.pkg`. It installs the service, the command-line
tool and the menu-bar app. macOS may ask you to allow the installer in *System Settings → Privacy &
Security* because the package is not notarised yet. Then connect with the menu-bar app or
`gorget up -server vpn.example.com`.

## Windows

Open **PowerShell** (no need for Administrator; Windows asks once for approval) and run:

```powershell
irm https://vpn.example.com/install.ps1 | iex
```

It downloads the installer, checks it against the release checksums, installs the **Gorget** service
and the tray app, and opens the browser sign-in. Open a new terminal afterwards to use the `gorget`
command.

**By hand:** download `gorget_<version>_windows_amd64.msi` from the
[Releases page](https://github.com/anand34577/gorget/releases/latest), run it, then open **Gorget** from
the Start menu, enter your server address and sign in. Or from a terminal:

```powershell
gorget up -server vpn.example.com
```

The installing user is made the *operator*, so the tray app works without elevation.

## Android

1. Download the APK from the [Releases page](https://github.com/anand34577/gorget/releases/latest)
   (`gorget-android-<version>-universal.apk` works on every phone) or get it from F-Droid, and open it.
   Android asks to allow installs from your browser or file manager the first time.
2. Open **Gorget**, enter your server address, tap **Connect**, allow the VPN permission, and sign in
   in the browser that opens.

Useful settings: *Always-on VPN* (Android settings) keeps it connected; the quick-settings tile and the
widget switch it on and off; *Block connections without VPN* adds a kill switch. See
[Gorget for Android](../ANDROID.md) for building it from source.

## Servers and routers without a browser

Machines with no screen join with a **setup key**:

1. In the console open **Setup keys → Create**. Give it a tag such as `tag:server`, so the device
   belongs to the network rather than to a person and never needs to sign in again.
2. On the machine:

```sh
curl -fsSL https://vpn.example.com/install.sh | GORGET_SETUP_KEY=gsk_… sh
```

On Windows: `$env:GORGET_SETUP_KEY='gsk_…'; irm https://vpn.example.com/install.ps1 | iex`.

## Who may control the service

The service runs as root/Administrator. Anyone on the computer may *read* the status. Changing things
needs root/Administrator or the **operator**, which the installer sets to you:

```sh
sudo gorget operator set alice      # alice may use gorget and the tray app without sudo
gorget operator                     # who is the operator, and may I control?
```

If you see "access denied", that is why: run the command with `sudo`, or ask for `operator set`.

## The command line

```text
gorget up -server HOST                            connect (first time: also sign in)
gorget up | down | status | login | logout        connect, disconnect, show devices
gorget exit-node [set NAME | none]                use another device for internet traffic
gorget set -use-dns=false -kill-switch=true …     change settings
gorget netcheck                                   how this device reaches the network
gorget file cp FILE… DEVICE | list | get          send and receive files between your devices
gorget ssh [USER@]DEVICE                          ssh by device name
gorget run -bypass -- COMMAND                     keep one program outside the tunnel (Linux)
```

Not connecting? See [troubleshooting](../ops/troubleshooting.md).
