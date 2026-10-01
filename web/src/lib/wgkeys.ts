import { x25519 } from "@noble/curves/ed25519.js";

function b64(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s);
}

/** Generates a WireGuard key pair in the browser; the private key never reaches the server. */
export function generateKeyPair(): { privateKey: string; publicKey: string } {
  const priv = x25519.utils.randomSecretKey();
  // WireGuard clamps the scalar; clamp explicitly so the stored key matches what wg uses.
  priv[0] &= 248;
  priv[31] &= 127;
  priv[31] |= 64;
  const pub = x25519.getPublicKey(priv);
  return { privateKey: b64(priv), publicKey: b64(pub) };
}

/** Inserts the private key into a config rendered by the server. */
export function withPrivateKey(conf: string, privateKey: string): string {
  return conf.replace("PrivateKey = <REPLACE_WITH_YOUR_PRIVATE_KEY>", `PrivateKey = ${privateKey}`);
}
