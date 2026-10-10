import nacl from "./secureNacl.ts";
import util from "tweetnacl-util";

const { decodeBase64, encodeBase64, decodeUTF8, encodeUTF8 } = util;
const BACKUP_PREFIX = "yaver-hcloud-recovery-v1";
const KEY_PREFIX = "yrk1_";

const b64url = (bytes: Uint8Array) => encodeBase64(bytes)
  .replace(/\+/g, "-")
  .replace(/\//g, "_")
  .replace(/=+$/g, "");

const fromB64url = (value: string): Uint8Array => {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  return decodeBase64(normalized + "=".repeat((4 - normalized.length % 4) % 4));
};

export type HetznerRecoveryExport = {
  encryptedBackup: string;
  recoveryKey: string;
};

export function encryptHetznerRecovery(token: string): HetznerRecoveryExport {
  const value = token.trim();
  if (!value) throw new Error("No Hetzner token is connected on this phone.");
  const key = nacl.randomBytes(nacl.secretbox.keyLength);
  const nonce = nacl.randomBytes(nacl.secretbox.nonceLength);
  const plaintext = decodeUTF8(JSON.stringify({ v: 1, provider: "hetzner", token: value }));
  const ciphertext = nacl.secretbox(plaintext, nonce, key);
  plaintext.fill(0);
  return {
    encryptedBackup: `${BACKUP_PREFIX}.${b64url(nonce)}.${b64url(ciphertext)}`,
    recoveryKey: `${KEY_PREFIX}${b64url(key)}`,
  };
}

export function decryptHetznerRecovery(encryptedBackup: string, recoveryKey: string): string {
  const parts = encryptedBackup.trim().split(".");
  if (parts.length !== 3 || parts[0] !== BACKUP_PREFIX || !recoveryKey.trim().startsWith(KEY_PREFIX)) {
    throw new Error("Invalid Hetzner recovery backup.");
  }
  try {
    const key = fromB64url(recoveryKey.trim().slice(KEY_PREFIX.length));
    const nonce = fromB64url(parts[1]);
    const ciphertext = fromB64url(parts[2]);
    if (key.length !== nacl.secretbox.keyLength || nonce.length !== nacl.secretbox.nonceLength) {
      throw new Error("invalid lengths");
    }
    const plaintext = nacl.secretbox.open(ciphertext, nonce, key);
    if (!plaintext) throw new Error("authentication failed");
    const parsed = JSON.parse(encodeUTF8(plaintext));
    plaintext.fill(0);
    if (parsed?.v !== 1 || parsed?.provider !== "hetzner" || typeof parsed?.token !== "string" || !parsed.token.trim()) {
      throw new Error("invalid payload");
    }
    return parsed.token.trim();
  } catch {
    throw new Error("Hetzner recovery key or backup is incorrect.");
  }
}
