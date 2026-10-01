# Access rules

Gorget is **default deny**: a device can reach another only when a rule allows it. Rules are a document (JSON with comments) in **Access rules**, with a visual editor, a file editor, tests, a simulator and a version history you can roll back.

```jsonc
{
  "tagOwners": { "tag:server": ["group:admins"] },
  "hosts":     { "nas": "192.168.1.10" },
  "acls": [
    { "id": "ssh", "action": "accept", "src": ["group:eng"], "dst": ["tag:server:22,443", "nas:445"] },
    { "id": "self", "action": "accept", "src": ["autogroup:member"], "dst": ["autogroup:self:*"] },
    { "id": "inet", "action": "accept", "src": ["autogroup:member"], "dst": ["autogroup:internet:*"] },
    { "id": "contractor", "action": "accept", "src": ["sam@example.com"], "dst": ["device:build-server:22"],
      "expires": "2026-12-31T00:00:00Z" }
  ],
  "autoApprovers": { "routes": { "192.168.0.0/16": ["tag:router"] }, "exitNode": ["tag:exit"] },
  "tests": [
    { "src": "alice@example.com", "accept": ["tag:server:22"], "deny": ["tag:server:80"] }
  ]
}
```

## Selectors

| Selector | Means |
|---|---|
| `*` | everything |
| `alice@example.com` or `user:…` | that person's devices |
| `group:eng` | members of the group |
| `tag:server` | devices with the tag |
| `device:build-server` | one device by name |
| `nas`, `host:nas` | an alias from `hosts` |
| `192.168.1.0/24` | an address or range (reachable through a subnet router) |
| `autogroup:member` | all devices owned by people |
| `autogroup:self` | (destination) the source person's own devices |
| `autogroup:internet` | (destination) the internet, through exit nodes |

Destinations carry ports: `tag:server:22,443`, `nas:8000-9000`, `host:*`.

## Safety nets

- **Tests** run every time you save; a failing test blocks the save.
- **Simulator** answers "may A reach B on port P?" for the saved or unsaved policy.
- **History** keeps every version with who changed it and why; restoring is one click.
- A broken stored policy never opens things up: all traffic is blocked until it is fixed.
- Rules from [temporary access](temporary-access.md) are added on top and expire by themselves; they do not appear in the document.

Enforcement happens twice: the server gives each device only the peers it may reach, and every device (and the gateway) also filters inbound traffic itself.
