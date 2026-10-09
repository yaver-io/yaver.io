import assert from "node:assert/strict";
import test from "node:test";
import { parseSignal, type AccessClaims } from "./protocol.ts";

const now = 1_800_000_000_000;
const controller: AccessClaims = {
  role: "controller", tenant: "act_0123456789abcdef0123456789abcdef", broker: "acb_0123456789abcdef01234567",
  actor: "controller-1", sub: "controller-1", jti: "jti", exp: now / 1000 + 60,
};
const signal = {
  version: 1, kind: "requests.auth", requestId: "req-1", targetDeviceId: "mac-mini",
  issuerDeviceId: "controller-1", issuedAt: now, expiresAt: now + 60_000, nonce: "nonce-1", provider: "yaver",
};

test("accepts a bounded controller authorization intent", () => {
  assert.equal(parseSignal(JSON.stringify(signal), controller, now).kind, "requests.auth");
});

test("rejects private or arbitrary fields", () => {
  assert.throws(() => parseSignal(JSON.stringify({ ...signal, oauthToken: "secret" }), controller, now), /unknown_signal_field/);
});

test("binds the signal issuer and direction to the capability", () => {
  assert.throws(() => parseSignal(JSON.stringify({ ...signal, issuerDeviceId: "other" }), controller, now), /issuer_mismatch/);
  const device: AccessClaims = { ...controller, role: "device", actor: "mac-mini", device: "mac-mini" };
  assert.throws(() => parseSignal(JSON.stringify({ ...signal, issuerDeviceId: "mac-mini" }), device, now), /direction_denied/);
});
