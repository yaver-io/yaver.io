import { createHmac } from "crypto";

/**
 * Minimal RFC 6238 TOTP generator for E2E tests.
 *
 * The Yaver backend (`backend/convex/totp.ts`) issues a base32 secret with
 * SHA-1 / 6 digits / 30s period and a ±1 verification window. We mirror those
 * exact parameters here so a test can derive a code the backend will accept —
 * no external `otplib`/`speakeasy` dependency, just node's crypto.
 *
 * IMPORTANT: this is test-only. The secret is created per run via
 * `/auth/totp/setup` and the account is deleted in teardown, so no real
 * 2FA secret ever lives in the repo.
 */

const DIGITS = 6;
const PERIOD = 30; // seconds

/** Decode an RFC 4648 base32 string (no padding required) into bytes. */
function base32Decode(input: string): Buffer {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  const clean = input.replace(/=+$/, "").toUpperCase().replace(/\s+/g, "");
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const ch of clean) {
    const idx = alphabet.indexOf(ch);
    if (idx === -1) throw new Error(`invalid base32 char: ${ch}`);
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      out.push((value >>> bits) & 0xff);
    }
  }
  return Buffer.from(out);
}

/** HOTP for an explicit counter. */
function hotp(secret: Buffer, counter: number): string {
  const buf = Buffer.alloc(8);
  // 64-bit big-endian counter. JS bitwise is 32-bit, so split hi/lo.
  buf.writeUInt32BE(Math.floor(counter / 2 ** 32), 0);
  buf.writeUInt32BE(counter >>> 0, 4);

  const hmac = createHmac("sha1", secret).update(buf).digest();
  const offset = hmac[hmac.length - 1] & 0x0f;
  const code =
    ((hmac[offset] & 0x7f) << 24) |
    ((hmac[offset + 1] & 0xff) << 16) |
    ((hmac[offset + 2] & 0xff) << 8) |
    (hmac[offset + 3] & 0xff);
  return (code % 10 ** DIGITS).toString().padStart(DIGITS, "0");
}

/**
 * Generate the current TOTP code for a base32 secret.
 *
 * `offsetPeriods` lets a test produce a deliberately stale (or future) code
 * outside the verification window — e.g. `generateTotp(secret, -2)` to assert
 * the backend rejects an old code.
 */
export function generateTotp(secret: string, offsetPeriods = 0): string {
  const counter = Math.floor(Date.now() / 1000 / PERIOD) + offsetPeriods;
  return hotp(base32Decode(secret), counter);
}
