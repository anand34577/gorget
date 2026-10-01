# First steps

## 1. Add devices

- **Computers and phones:** install the app, enter your server address, sign in. Approve the sign-in in the browser if asked.
- **Any WireGuard app:** in the console open **WireGuard apps → Add**, scan the QR code or download the config. The keys are generated in your browser; the server never sees the private key.

### Servers and routers

Headless machines join with a *setup key* (**Setup keys → Create**). Give the key tags like `tag:server`: devices joined with a tagged key belong to the tag instead of a person and never need to sign in again.

```sh
gorget up -server vpn.example.com -setup-key gsk_…
```

## 2. Reach your devices

Every device gets a stable address (default `100.80.x.x`) and a name: `laptop.gorget.internal`. Open one from another device with its name or address.

## 3. Share a network or use an exit node

On a device that sits in a network you want to reach (a home router, an office server):

```sh
gorget set -advertise-routes=192.168.1.0/24
gorget set -advertise-exit-node
```

Then approve it under **Routes & exit nodes**. Other devices pick the exit node in the app (or `gorget exit-node set NAME`). See [Exit nodes and routes](routes.md).

## 4. Tighten access

New networks start with "everything can reach everything". Replace that with [access rules](access-rules.md): groups, tags, ports, time limits. The editor has a **simulator** ("can alice reach the NAS on port 445?") and **tests** that block saving a policy that breaks what you meant.

## 5. Optional hardening

- Turn on [device health](posture.md) rules (OS version, disk encryption, firewall).
- Require two-factor sign-in under **Settings → Sign-in**.
- Connect your identity provider ([single sign-on and SCIM](../ops/sso.md)).
- Let people request [temporary access](temporary-access.md) instead of keeping permanent rules.
