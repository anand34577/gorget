# Exit nodes and routes

## Subnet routes

A device can share the network it sits in, so others reach printers, NAS boxes or servers that cannot run Gorget themselves.

```sh
gorget set -advertise-routes=192.168.1.0/24,10.20.0.0/16
```

An administrator approves each route (**Routes & exit nodes**), or an `autoApprovers` rule in the access policy does. Several devices may share the same route: the lowest priority number among *online* routers carries the traffic, and another takes over when it goes offline.

Receiving devices install shared routes by default (`gorget set -accept-routes=false` to opt out). Access rules still decide who may use them: a route is reachable only if a rule allows its addresses.

On Linux a router needs IP forwarding and NAT; Gorget turns both on while the device serves routes. On computers, a shared route never overrides the network the computer is connected to, so at home LAN traffic stays local.

### A router with plain WireGuard (OpenWrt and others)

A router that can't run Gorget can still carry your home networks, and the Gorget apps on your
laptop and phone reach them with no second VPN (the apps install a route for each network
automatically). The console's **Connect a router or network** wizard does all of this for you.
Manually:  create a WireGuard
configuration for it (the OpenWrt format sets up the interface and firewall for you), then
**Routes & exit nodes > Add networks behind a router**. Traffic to those networks then goes
through the server's gateway to the router. Step-by-step: [your home network, from anywhere](homelab.md). Windows can forward between routed networks but cannot translate addresses, so use Linux or macOS for exit nodes.

## Exit nodes

An exit node carries a device's *internet* traffic. Offer one:

```sh
gorget set -advertise-exit-node
```

Approve it in the console. Devices choose it in the app, or `gorget exit-node set NAME` (administrators can also force one for everyone under **Settings → Gorget apps**). While an exit node is on:

- local networks stay reachable (`allow-lan`, on by default), so printers keep working;
- the **kill switch** (optional, or enforced by the administrator) blocks traffic outside the tunnel if it drops;
- all DNS goes through Gorget.

## Only some traffic: domain routing and apps

- [Domain routing](domain-routing.md) sends named sites through an exit node and leaves the rest alone.
- On Linux `gorget run -bypass -- COMMAND` keeps one program outside the tunnel while an exit node carries everything else.
