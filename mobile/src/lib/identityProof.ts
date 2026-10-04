import nacl from "./secureNacl";
import { decodeBase64, encodeBase64 } from "tweetnacl-util";

/**
 * identityProof.ts — client half of POST /identity/prove.
 *
 * Mirrors desktop/agent/identity_proof.go. Keep the two in step: they are one
 * protocol implemented twice, which is exactly the drift shape that has bitten
 * this codebase before, so the shared shape is asserted by tests on both sides.
 *
 * WHY THIS EXISTS
 * ---------------
 * LAN discovery is unauthenticated by construction. The UDP beacon is unsigned
 * and its `th` tag is a short unsalted hash any listener can replay, so a beacon
 * tells you an ADDRESS and never tells you WHO is at it. Attaching a bearer
 * token to a beacon-supplied address — which is what the heartbeat upgrade used
 * to do — hands the session to anyone willing to broadcast a packet.
 *
 * THE HANDSHAKE
 * -------------
 *  1. Generate a fresh 32-byte nonce and an ephemeral X25519 keypair.
 *  2. Seal the nonce to the device public key CONVEX records for the device we
 *     mean to reach (NaCl box — same construction as encrypted pairing).
 *  3. POST it with NO credentials. An unproven host must never see a token.
 *  4. Only the holder of that private key can open the box and echo the nonce.
 *  5. Compare. Match ⇒ this really is that machine ⇒ safe to attach a credential.
 *
 * Replay-safe (fresh nonce per attempt, and we only accept the value we just
 * generated). An impostor gains nothing from reading the beacon, replaying `th`,
 * or answering /health.
 */

/** Constant-time compare — a short-circuit here leaks the nonce byte by byte. */
function timingSafeEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length || a.length === 0) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a[i] ^ b[i];
  return diff === 0;
}

/**
 * Ask the host at `baseUrl` to prove it holds the private key matching
 * `expectedPublicKeyB64` (the device's public key as recorded in Convex).
 *
 * Returns true ONLY on a cryptographically valid proof. Every other outcome —
 * network error, timeout, malformed reply, wrong nonce, mismatched key — is
 * false. Fail closed: the caller's fallback is to keep using the connection it
 * already has, which costs an optimisation, not a session.
 */
export async function verifyHostIdentity(
  baseUrl: string,
  expectedPublicKeyB64: string,
  timeoutMs = 2000
): Promise<boolean> {
  try {
    const recipient = decodeBase64(expectedPublicKeyB64);
    if (recipient.length !== 32) return false;

    const nonce = nacl.randomBytes(32);
    const eph = nacl.box.keyPair();
    const boxNonce = nacl.randomBytes(nacl.box.nonceLength);
    const ct = nacl.box(nonce, boxNonce, recipient, eph.secretKey);
    if (!ct) return false;

    // Wire format matches the Go side exactly: 24-byte box nonce || ciphertext.
    const sealed = new Uint8Array(boxNonce.length + ct.length);
    sealed.set(boxNonce, 0);
    sealed.set(ct, boxNonce.length);

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    let res: Response;
    try {
      res = await fetch(`${baseUrl}/identity/prove`, {
        method: "POST",
        // NO Authorization header. That is the entire point of this call.
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          sealed: encodeBase64(sealed),
          senderPublicKey: encodeBase64(eph.publicKey),
        }),
        signal: controller.signal,
      });
    } finally {
      clearTimeout(timer);
    }
    if (!res.ok) return false;

    const body = (await res.json()) as { nonce?: string; publicKey?: string };
    if (!body?.nonce) return false;

    // The echoed public key must also be the one we sealed to, so a host cannot
    // answer with a key of its own choosing.
    if (body.publicKey && body.publicKey !== expectedPublicKeyB64) return false;

    return timingSafeEqual(decodeBase64(body.nonce), nonce);
  } catch {
    return false;
  }
}

/**
 * Convex-sourced device public keys, keyed by deviceId.
 *
 * CRITICAL: the expected key must come from CONVEX, never from the beacon. The
 * beacon carries a `dpk` field, and using it here would be circular — an
 * attacker broadcasting a forged beacon would simply supply their own key and
 * pass their own challenge. The whole handshake rests on comparing against an
 * identity the attacker does not control.
 *
 * DeviceContext populates this whenever the Convex device list refreshes.
 */
const knownDevicePublicKeys = new Map<string, string>();

export function setKnownDevicePublicKeys(
  devices: Array<{ id?: string; deviceId?: string; publicKey?: string }>
): void {
  knownDevicePublicKeys.clear();
  for (const d of devices) {
    const id = (d.deviceId || d.id || "").trim();
    const key = (d.publicKey || "").trim();
    if (id && key) knownDevicePublicKeys.set(id, key);
  }
}

/**
 * Look up a device's Convex-recorded public key. Prefix-tolerant because
 * beacons advertise a short device id while Convex stores the full one.
 * Returns null when we have no recorded key — the caller must then refuse to
 * upgrade rather than fall back to trusting the beacon.
 */
export function getKnownDevicePublicKey(deviceId: string): string | null {
  const id = (deviceId || "").trim();
  if (!id) return null;
  const exact = knownDevicePublicKeys.get(id);
  if (exact) return exact;
  for (const [k, v] of knownDevicePublicKeys) {
    if (k.startsWith(id) || id.startsWith(k)) return v;
  }
  return null;
}
