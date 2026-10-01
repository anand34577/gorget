// Helpers converting between the server's WebAuthn JSON (base64url) and the browser API.

function b64urlToBuf(s: string): ArrayBuffer {
  const pad = "=".repeat((4 - (s.length % 4)) % 4);
  const bin = atob((s + pad).replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
}

function bufToB64url(b: ArrayBuffer): string {
  const bytes = new Uint8Array(b);
  let s = "";
  for (const x of bytes) s += String.fromCharCode(x);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/* eslint-disable @typescript-eslint/no-explicit-any */
export async function createCredential(options: any): Promise<string> {
  if (!window.PublicKeyCredential) throw new Error("This browser does not support passkeys.");
  const pk = options.publicKey;
  pk.challenge = b64urlToBuf(pk.challenge);
  pk.user.id = b64urlToBuf(pk.user.id);
  pk.excludeCredentials = (pk.excludeCredentials ?? []).map((c: any) => ({ ...c, id: b64urlToBuf(c.id) }));
  const cred = (await navigator.credentials.create({ publicKey: pk })) as PublicKeyCredential | null;
  if (!cred) throw new Error("Passkey creation was cancelled.");
  const r = cred.response as AuthenticatorAttestationResponse;
  return JSON.stringify({
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    response: {
      attestationObject: bufToB64url(r.attestationObject),
      clientDataJSON: bufToB64url(r.clientDataJSON),
      transports: r.getTransports?.() ?? [],
    },
  });
}

export async function getAssertion(options: any): Promise<string> {
  if (!window.PublicKeyCredential) throw new Error("This browser does not support passkeys.");
  const pk = options.publicKey;
  pk.challenge = b64urlToBuf(pk.challenge);
  pk.allowCredentials = (pk.allowCredentials ?? []).map((c: any) => ({ ...c, id: b64urlToBuf(c.id) }));
  const cred = (await navigator.credentials.get({ publicKey: pk })) as PublicKeyCredential | null;
  if (!cred) throw new Error("Sign-in was cancelled.");
  const r = cred.response as AuthenticatorAssertionResponse;
  return JSON.stringify({
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    response: {
      authenticatorData: bufToB64url(r.authenticatorData),
      clientDataJSON: bufToB64url(r.clientDataJSON),
      signature: bufToB64url(r.signature),
      userHandle: r.userHandle ? bufToB64url(r.userHandle) : null,
    },
  });
}
