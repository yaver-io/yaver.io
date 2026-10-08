import assert from "node:assert/strict";
import test from "node:test";
import nacl from "tweetnacl";

import {
  CREDENTIAL_HANDOFF_MAX_TTL_MS,
  CredentialHandoffError,
  createCredentialHandoffRequest,
  credentialAccountFingerprint,
  credentialHandoffVerificationCode,
  encodeCredentialHandoffQr,
  openCredentialHandoff,
  parseCredentialHandoffQr,
  sealCredentialForHandoff,
} from "./credentialHandoff.ts";

const NOW = 1_800_000_000_000;
const account = credentialAccountFingerprint("account-owner-1");

function fixture() {
  const recipient = nacl.box.keyPair();
  const request = createCredentialHandoffRequest({
    targetDeviceId: "samsung-tablet-1",
    targetPublicKey: recipient.publicKey,
    accountFingerprint: account,
    now: NOW,
    randomBytes: (length) => new Uint8Array(length).fill(7),
  });
  const envelope = sealCredentialForHandoff({
    request,
    expectedAccountFingerprint: account,
    kind: "deepseek-api-key",
    value: "sk-deepseek-test-only",
    now: NOW + 1_000,
    senderKeyPair: nacl.box.keyPair.fromSecretKey(new Uint8Array(32).fill(9)),
  });
  return { recipient, request, envelope };
}

function expectCode(code: string, fn: () => unknown): void {
  assert.throws(fn, (error) => error instanceof CredentialHandoffError && error.code === code);
}

test("round trips a credential only for the intended account and device", () => {
  const { recipient, envelope } = fixture();
  const opened = openCredentialHandoff(envelope, {
    expectedDeviceId: "samsung-tablet-1",
    expectedAccountFingerprint: account,
    recipientSecretKey: recipient.secretKey,
    now: NOW + 2_000,
  });
  assert.deepEqual(opened, {
    handoffId: envelope.handoffId,
    kind: "deepseek-api-key",
    value: "sk-deepseek-test-only",
    expiresAt: NOW + 120_000,
  });
  assert.doesNotMatch(JSON.stringify(envelope), /sk-deepseek/);
});

test("machine access bundle uses the same device-and-account-bound ciphertext", () => {
  const recipient = nacl.box.keyPair();
  const request = createCredentialHandoffRequest({ targetDeviceId: "phone-1", targetPublicKey: recipient.publicKey, accountFingerprint: account, now: NOW });
  const value = JSON.stringify({ version: 1, deviceId: "pc-1", hosts: ["100.64.0.7"], user: "operator", port: 22 });
  const envelope = sealCredentialForHandoff({ request, expectedAccountFingerprint: account, kind: "machine-access-bundle", value, now: NOW + 1 });
  const opened = openCredentialHandoff(envelope, { expectedDeviceId: "phone-1", expectedAccountFingerprint: account, recipientSecretKey: recipient.secretKey, now: NOW + 2 });
  assert.equal(opened.kind, "machine-access-bundle");
  assert.equal(opened.value, value);
  assert.doesNotMatch(JSON.stringify(envelope), /100\.64\.0\.7|operator/);
});

test("rejects wrong-account, wrong-device, replay, and expiry", () => {
  const { recipient, envelope } = fixture();
  const base = {
    expectedDeviceId: "samsung-tablet-1",
    expectedAccountFingerprint: account,
    recipientSecretKey: recipient.secretKey,
    now: NOW + 2_000,
  };
  expectCode("HANDOFF_WRONG_ACCOUNT", () => openCredentialHandoff(envelope, {
    ...base,
    expectedAccountFingerprint: credentialAccountFingerprint("account-owner-2"),
  }));
  expectCode("HANDOFF_WRONG_DEVICE", () => openCredentialHandoff(envelope, { ...base, expectedDeviceId: "other-tablet" }));
  expectCode("HANDOFF_REPLAYED", () => openCredentialHandoff(envelope, {
    ...base,
    consumedHandoffIds: new Set([envelope.handoffId]),
  }));
  expectCode("HANDOFF_EXPIRED", () => openCredentialHandoff(envelope, { ...base, now: NOW + 120_000 }));
});

test("authenticated ciphertext fails closed after tampering", () => {
  const { recipient, envelope } = fixture();
  const tampered = { ...envelope, ciphertext: `${envelope.ciphertext.slice(0, -4)}AAAA` };
  expectCode("HANDOFF_DECRYPT_FAILED", () => openCredentialHandoff(tampered, {
    expectedDeviceId: "samsung-tablet-1",
    expectedAccountFingerprint: account,
    recipientSecretKey: recipient.secretKey,
    now: NOW + 2_000,
  }));
});

test("request lifetime is bounded and the verification code matches the transcript", () => {
  const { request, envelope } = fixture();
  assert.match(credentialHandoffVerificationCode(request, envelope), /^\d{6}$/);
  expectCode("HANDOFF_MALFORMED", () => createCredentialHandoffRequest({
    targetDeviceId: "tablet",
    targetPublicKey: nacl.box.keyPair().publicKey,
    accountFingerprint: account,
    now: NOW,
    ttlMs: CREDENTIAL_HANDOFF_MAX_TTL_MS + 1,
  }));
});

test("sender refuses a request claiming another signed-in account", () => {
  const { request } = fixture();
  expectCode("HANDOFF_WRONG_ACCOUNT", () => sealCredentialForHandoff({
    request,
    expectedAccountFingerprint: credentialAccountFingerprint("account-owner-2"),
    kind: "deepseek-api-key",
    value: "never-sent",
    now: NOW + 1_000,
  }));
});

test("QR encoding is structured, round-trips, and rejects unrelated data", () => {
  const { request, envelope } = fixture();
  assert.deepEqual(parseCredentialHandoffQr(encodeCredentialHandoffQr(request)), request);
  assert.deepEqual(parseCredentialHandoffQr(encodeCredentialHandoffQr(envelope)), envelope);
  assert.equal(parseCredentialHandoffQr("https://example.com/not-yaver"), null);
  assert.doesNotMatch(encodeCredentialHandoffQr(envelope), /sk-deepseek/);
});
