# Security model

This page states what Gorget protects, how, and what it does not. It is also the scope document for an external audit.

## Reporting a problem

Email the maintainers privately (see `SECURITY.md` in the repository root) with details and, if possible, a proof of concept. Please do not open a public issue for vulnerabilities. We acknowledge within 3 days and aim to fix and disclose within 90 days.

## What is protected, and from whom

| Asset | Protection |
|---|---|
| Traffic between devices | WireGuard (Noise IK, ChaCha20-Poly1305). Optional ML-KEM-768 pre-shared key per device pair ([post-quantum](guide/post-quantum.md)). The server and relays only ever see ciphertext. |
| Private keys of devices | Generated on the device, never sent. Standard WireGuard configs: generated in the browser; the server stores only the public key. |
| Secrets at rest (OIDC client secrets, webhook secrets, PSKs, gateway key) | XChaCha20-Poly1305 with a key derived from the master key. |
| Passwords | Argon2id; lockout after repeated failures; forced change for temporary passwords. |
| Console sessions | Random tokens stored hashed; CSRF token on writes; secure cookies behind HTTPS; TOTP, recovery codes and passkeys; re-confirmation for sensitive actions. |
| Device sessions | Ed25519 machine key; challenge-response (single use, 60 s) to authenticate; signed short-lived session token re-checked against the database on every call (disabling a device takes effect at once). |
| Access control | Default deny. Enforced by the server (peers only), by every device's inbound filter, and by the gateway firewall. A bad stored policy blocks everything. |
| Audit log | Hash chain: modifying or deleting stored entries is detectable. |
| Supply chain | Reproducible Go builds, SBOM, checksums and keyless cosign signatures for releases. |

## Trust boundaries and assumptions

- **The server is trusted for coordination.** It distributes public keys and decides who may talk to whom; a compromised server could add a malicious device to your network or hand out rules, but cannot read tunnel traffic between existing devices. Run it like you would a certificate authority.
- **Relays are untrusted for confidentiality.** They see who talks to whom and how much.
- **Local users:** the daemon runs as root/Administrator. The local API is a Unix socket or named pipe: everyone may *read* status; only root/Administrator or the configured operator may change anything (checked from the kernel-reported peer credentials).
- **Device health** reports are self-asserted by the client (hygiene, not a boundary against a hostile device).
- **File transfer** listens only on the overlay address and accepts only the same user's devices.
- **SCIM** has its own bearer token (stored hashed); rotate it to revoke.

## Known limitations

- No third-party audit yet. Phase 5 of the roadmap tracks it; the scope is the server (`internal/`), client core (`client/`) and the daemon's local API.
- Windows and macOS builds are not code-signed or notarised until certificates exist.
- macOS and Windows kill switches rely on the OS firewalls (pf, WFP): programs running as root on macOS are let through by the kill switch (the daemon runs as root); the Windows kill switch also blocks the local network.
- The Windows daemon cannot act as an exit node (no NAT).
- Android's kill switch is the system's *Block connections without VPN* setting.

## Hardening checklist

- Keep `master.key` out of backups that live next to the database backups, and out of version control.
- Put the console behind your VPN (`security.admin_only_from_vpn` or `http.admin_allow_cidrs`) once devices are enrolled.
- Require two-factor sign-in for administrators.
- Use tags and narrow rules instead of "everything can reach everything".
- Turn on [device health](guide/posture.md) rules and [post-quantum protection](guide/post-quantum.md) (on by default).
