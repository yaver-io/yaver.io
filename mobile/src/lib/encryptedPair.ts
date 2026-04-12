import nacl from "tweetnacl";
import util from "tweetnacl-util";
const encodeBase64 = util.encodeBase64;
const decodeBase64 = util.decodeBase64;

let phoneKeyPair: nacl.BoxKeyPair | null = null;

function getPhoneKeyPair(): nacl.BoxKeyPair {
  if (!phoneKeyPair) {
    phoneKeyPair = nacl.box.keyPair();
  }
  return phoneKeyPair;
}

export function getPhonePublicKeyBase64(): string {
  return encodeBase64(getPhoneKeyPair().publicKey);
}

export function encryptTokenForDevice(
  token: string,
  devicePublicKeyBase64: string
): { encrypted: string; senderPublicKey: string } {
  const kp = getPhoneKeyPair();
  const devicePub = decodeBase64(devicePublicKeyBase64);
  const nonce = nacl.randomBytes(24);
  const msgBytes = new TextEncoder().encode(token);
  const ciphertext = nacl.box(msgBytes, nonce, devicePub, kp.secretKey);

  const combined = new Uint8Array(24 + ciphertext.length);
  combined.set(nonce);
  combined.set(ciphertext, 24);

  return {
    encrypted: encodeBase64(combined),
    senderPublicKey: encodeBase64(kp.publicKey),
  };
}

export async function submitEncryptedPair(
  targetUrl: string,
  token: string,
  devicePublicKeyBase64: string
): Promise<{ ok: boolean; host?: string; error?: string }> {
  const { encrypted, senderPublicKey } = encryptTokenForDevice(
    token,
    devicePublicKeyBase64
  );
  try {
    const res = await fetch(`${targetUrl}/auth/pair/encrypted`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ encrypted, senderPublicKey }),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      return { ok: false, error: text || `HTTP ${res.status}` };
    }
    const data = await res.json();
    return { ok: true, host: data.host };
  } catch (e: any) {
    return { ok: false, error: e?.message ?? "network error" };
  }
}
