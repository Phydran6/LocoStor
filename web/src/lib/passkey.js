// WebAuthn helpers: the server speaks base64url, the browser ArrayBuffers.
function decode(s) {
  const b = atob(s.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((s.length + 3) % 4));
  return Uint8Array.from(b, (c) => c.charCodeAt(0));
}
function encode(buf) {
  let s = '';
  for (const b of new Uint8Array(buf)) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

// Why passkeys may not work on this page, or '' if they can.
export function passkeyProblem() {
  if (!window.PublicKeyCredential || !navigator.credentials) return 'This browser does not support passkeys.';
  if (!window.isSecureContext) return 'Passkeys need HTTPS. Open LocoStor via https:// with a trusted certificate.';
  if (/^[\d.]+$|^\[?[0-9a-f:]+\]?$/i.test(location.hostname)) return 'Passkeys need a host name, not an IP address (e.g. https://nas.home.arpa).';
  return '';
}

export async function createPasskey(publicKey) {
  const opts = {
    ...publicKey,
    challenge: decode(publicKey.challenge),
    user: { ...publicKey.user, id: decode(publicKey.user.id) },
    excludeCredentials: (publicKey.excludeCredentials ?? []).map((c) => ({ ...c, id: decode(c.id) })),
  };
  const cred = await navigator.credentials.create({ publicKey: opts });
  return {
    id: cred.id,
    client_data_json: encode(cred.response.clientDataJSON),
    attestation_object: encode(cred.response.attestationObject),
  };
}

export async function getPasskey(publicKey) {
  const opts = {
    ...publicKey,
    challenge: decode(publicKey.challenge),
    allowCredentials: (publicKey.allowCredentials ?? []).map((c) => ({ ...c, id: decode(c.id) })),
  };
  const cred = await navigator.credentials.get({ publicKey: opts });
  return {
    id: cred.id,
    client_data_json: encode(cred.response.clientDataJSON),
    authenticator_data: encode(cred.response.authenticatorData),
    signature: encode(cred.response.signature),
    user_handle: cred.response.userHandle ? encode(cred.response.userHandle) : '',
  };
}
