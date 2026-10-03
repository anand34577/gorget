# Your home network, from anywhere

This guide sets up a common case: the Gorget server on a cloud VPS, servers at home on
your LAN and VLANs, and laptops (Windows, Linux) and Android phones that should reach the
home machines by their usual addresses, as if they were at home. Several people use it,
and rules decide who reaches what.

```
   laptop (Windows/Linux) ─┐                          ┌─ home LAN 192.168.1.0/24
   phone (Android) ────────┼── encrypted ── VPS ──────┤   (NAS, Proxmox, Home Assistant…)
   family devices ─────────┘   (Gorget server)        └─ VLANs 192.168.20.0/24, 192.168.30.0/24
                                                         through one always-on device at home
```

Devices talk to each other directly whenever they can; the VPS only coordinates them and
relays traffic when a direct path is impossible.

## 1. The server on your VPS

Point a DNS name at the VPS (for example `vpn.example.com`) and open TCP 80 and 443 and
UDP 443, 3478, 3479 and 51820 in the VPS firewall. Then, on the VPS:

```sh
curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/install-server.sh | sh -s -- --domain vpn.example.com
```

It prints a setup link. Open it, create your owner account, and turn on two-factor sign-in
under **Account & security** straight away. (Other ways to install, such as Docker, are in
[Install the server](install-server.md).)

## 2. Let one machine at home share the home networks

Pick something at home that is always on: a Linux server, a Proxmox host, a Raspberry Pi,
or your OpenWrt router (see option B).

### Option A: a Linux machine (recommended)

Create a setup key first (**Setup keys > New**, tag `tag:home-router`), then on the machine:

```sh
curl -fsSL https://vpn.example.com/install.sh | GORGET_SETUP_KEY=gsk_… sh
sudo gorget set -advertise-routes 192.168.1.0/24,192.168.20.0/24,192.168.30.0/24
```

In the console, open **Routes & exit nodes** and approve the three networks. That machine
now forwards traffic into your LAN and VLANs. Your home devices need no changes: replies
come back through the same machine automatically.

The machine must be able to reach every VLAN you list (it needs an address or a route in
each one, which is normal when the router routes between your VLANs).

The tag makes the device belong to the network rather than to a person, and its sign-in
doesn't expire.

### Option B: your OpenWrt router

If your router runs OpenWrt with WireGuard:

1. **WireGuard apps > New configuration**, name it `home-router`, choose
   **Private network only**.
2. Show the configuration, switch the format to **OpenWrt router** and download the
   script. Run it on the router (`ssh root@192.168.1.1 'sh -s' < home-router-openwrt.sh`)
   after `opkg update && opkg install wireguard-tools luci-proto-wireguard`.
3. **Routes & exit nodes > Add networks behind a router**: pick `home-router` and add
   `192.168.1.0/24`, `192.168.20.0/24`, `192.168.30.0/24`.

The script puts the tunnel in its own firewall zone and allows forwarding to and from
`lan`. VLANs in other zones (say `iot`) need the same two forwarding entries for that zone.
Traffic passes through the VPS in this mode, because a router with plain WireGuard can't
make direct connections.

## 3. Laptops and phones

- **Windows:** install the MSI, open the Gorget tray app, enter `vpn.example.com`, sign in.
- **Linux and Mac:** `curl -fsSL https://vpn.example.com/install.sh | sh` installs Gorget
  and opens the sign-in page.
- **Android:** install the APK, enter the server address and sign in. Turn on *Always-on
  VPN* in Android's settings if you want it connected all the time.

Now `ssh 192.168.1.10`, `https://192.168.20.5:8006` and so on work from anywhere. At home
on the same LAN, computers keep sending LAN traffic the normal way: the network they are
connected to takes priority over a shared route. Android sends it through Gorget even at home;
it still works, and stays local because the phone reaches your home router device directly.

!!! note "Overlapping addresses"
    If the network you are visiting uses the same range as home (many hotels and cafés use
    `192.168.1.0/24`), the local network wins. Giving your home LAN a less common range,
    such as `10.37.12.0/24`, avoids this.

## 4. Add the people at home

**People & groups > Add person**. With email set up (**Settings > Email**) they get an
invitation link to choose their own password; otherwise you get a temporary password to
pass on. Put people into groups, for example `group:family` and `group:admins`.

## 5. Decide who can reach what

New networks start with "everyone reaches everything". Replace it in **Access rules**, for
example:

```jsonc
{
  "hosts": {
    "home-lan":  "192.168.1.0/24",
    "lab-vlan":  "192.168.20.0/24",
    "nas":       "192.168.1.10",
    "jellyfin":  "192.168.1.20"
  },
  "acls": [
    // Admins reach everything at home and every Gorget device.
    { "id": "admins", "action": "accept", "src": ["group:admins"], "dst": ["*:*"] },
    // The family gets the media server and file shares only.
    { "id": "family-media", "action": "accept", "src": ["group:family"], "dst": ["jellyfin:8096", "nas:445"] },
    // Everyone reaches their own devices.
    { "id": "own-devices", "action": "accept", "src": ["autogroup:member"], "dst": ["autogroup:self:*"] }
  ],
  "tests": [
    { "src": "group:family", "accept": ["jellyfin:8096"], "deny": ["lab-vlan:22"] }
  ]
}
```

The tests run on every save, and the simulator answers "may this person reach that
address on this port?" before you commit to a change.

## 6. Names instead of addresses

**DNS** in the console:

- Every Gorget device gets a name: `laptop.gorget.internal`.
- **Custom records** add your own: `nas` (becomes `nas.gorget.internal`), `jellyfin.home.lan`
  pointing at `192.168.1.20`, or a wildcard `*.apps.home.lan` pointing at your reverse proxy
  so `grafana.apps.home.lan`, `wiki.apps.home.lan`… all work.
- **Split DNS** sends a whole domain to your home DNS server (Pi-hole, AdGuard, the
  router): domain `home.lan`, nameserver `192.168.1.1`.
- **Upstream resolvers** decide where everything else goes, including encrypted DNS
  (`https://…` or `tls://…`).

## 7. Keep an eye on it

- **Insights** shows who is online over time, traffic, operating systems and app versions,
  and every connection with its public address and country.
- **Devices** shows each device's status, last-seen time and where it connects from;
  **Block** cuts a device off instantly (Unblock restores it).
- **Settings > Email** sends you mail when a device waits for approval, connects from a new
  country, gets blocked by health rules, or when an account is locked after wrong passwords.
- **Settings > Device health** can require a minimum app or OS version, disk encryption, a
  firewall, certain operating systems, or connections only from allowed countries.

## Security checklist

- Turn on two-factor sign-in for every administrator (**Settings > Sign-in** can require it).
- Approve new devices manually (**Settings > Devices > Require approval**).
- Use setup keys that expire and are single-use, except for servers.
- Replace the allow-all rule with specific rules as above.
- Keep the VPS firewall to the ports in step 1; the console can be limited to the VPN
  itself with `security.admin_only_from_vpn: true` once you are connected.
- Back up `master.key` together with the database backups.
