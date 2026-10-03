# First steps

You have a running server and an owner account. This page covers what most people do next.
([Not there yet? Start with the quick start.](quickstart.md))

## 1. Add devices

| Device | How |
|---|---|
| Computer or phone | [Install the app](install-clients.md), enter your server address, sign in. Approve the sign-in in the browser if asked. |
| Server or router with no screen | Join with a [setup key](#servers-and-routers). |
| [Any WireGuard app](#any-wireguard-app) | In the console. |

### Servers and routers

Headless machines join with a *setup key* (**Setup keys → Create**). Give the key tags like `tag:server`:
devices joined with a tagged key belong to the tag instead of a person and never need to sign in again.

```sh
gorget up -server vpn.example.com -setup-key gsk_…
```

### Any WireGuard app

For phones, routers and NAS boxes that only run the official WireGuard app, open **WireGuard apps → Add**
in the console, then scan the QR code or download the config file. The keys are generated in your
browser; the server never sees the private key. For OpenWrt there is a ready-made setup script.
This needs the server's gateway (Linux or macOS server, UDP 51820 open).

## 2. Reach your devices

Every device gets a stable address (default `100.80.x.x`) and a name: `laptop.gorget.internal`.
Open one from another device with its name or address. `gorget status` lists them all.

## 3. Share a network or use an exit node

On a device that sits in a network you want to reach (a home router, an office server):

```sh
gorget set -advertise-routes=192.168.1.0/24
gorget set -advertise-exit-node
```

Then approve it under **Routes & exit nodes** in the console. Other devices pick the exit node in the
app (or `gorget exit-node set NAME`). See [Exit nodes and routes](routes.md).

## 4. Tighten access

New networks start with "everything can reach everything". Replace that with [access rules](access-rules.md):
groups, tags, ports, time limits. The editor has a **simulator** ("can alice reach the NAS on port 445?")
and **tests** that block saving a policy that breaks what you meant.

## 5. Add people

**People & groups → Add person**. With [email](email.md) set up they get an invitation link; otherwise you get
a temporary password to pass on.

## 6. Optional hardening

- Turn on [device health](posture.md) rules (OS version, disk encryption, firewall).
- Require two-factor sign-in under **Settings → Sign-in**.
- Connect your identity provider ([single sign-on and SCIM](../ops/sso.md)).
- Let people request [temporary access](temporary-access.md) instead of keeping permanent rules.
- Set up [notifications and sign-in alerts](notifications.md) (email, Gotify or ntfy).
- Set up [backups](../ops/backups.md) and keep a copy of `master.key` somewhere safe.
