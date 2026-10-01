# Post-quantum protection

WireGuard's handshake uses Curve25519. A future quantum computer could break it, and traffic recorded today could be decrypted later. WireGuard can mix in a 32-byte *pre-shared key*; if that key comes from a quantum-safe exchange, the connection stays safe either way.

Gorget devices do this for each other automatically:

1. Two devices run an **ML-KEM-768** key exchange (FIPS 203) through the server's signal channel. The messages are sealed between the two devices, so the server only relays them.
2. Both sides derive the pre-shared key with HKDF, bound to both WireGuard public keys, and apply it together using a short commit protocol (a lost message leaves both on the old key, never half-switched).
3. The key is stored on each device and used for every later handshake. It is redone when either device's key changes.

You can see it: **🔒** next to a device in the app and `post-quantum` in `gorget status`.

## Details and limits

- Only between Gorget devices. Standard WireGuard apps behind the gateway use their own optional pre-shared key (**WireGuard apps → rotate PSK**).
- Older apps simply do not answer; the connection works as before without the extra protection.
- Turn it off per device with `gorget set -post-quantum=false` or in the app's settings (existing keys stay until you sign out).
- It protects against recorded traffic; it does not change who may talk to whom, which stays under your access rules.
- The exchange is repeated only when keys change, so there is no ongoing cost: one small exchange per device pair.
