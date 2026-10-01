# Domain routing

Some services only accept connections from your office or server address. Domain routing sends traffic for *named* sites through one exit node while everything else keeps its normal path.

## Set it up

1. Make sure a device is an **approved exit node** (Routes & exit nodes).
2. **Settings → Routing → Add domain**: enter `example.com` and choose the exit node.
3. Allow your devices to use the exit node in the access rules (`autogroup:internet`).

`example.com` covers all its subdomains. Up to 200 domains can be listed.

## How it works

Devices send DNS for the listed domains to Gorget's resolver. Every address in an answer is added, for as long as the DNS record lives plus a few minutes, to the routes through the chosen exit node; the first lookup takes a fraction of a second to take effect. Nothing else changes: other sites, your LAN and your normal DNS are untouched.

## Good to know

- Devices need **Use Gorget DNS** (the default).
- If a device chooses a full exit node, that one carries everything and domain routes are not needed.
- Programs that cache addresses for a long time or use their own DNS-over-HTTPS bypass the lookup; point them at the system resolver.
- The exit node's access rules must allow the traffic, and its internet connection is what the sites see.
