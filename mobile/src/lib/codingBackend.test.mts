// codingBackend.test.mts — backend metadata + auto/explicit resolution.
// Run: npx tsx src/lib/codingBackend.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  backendMeta,
  backendUsable,
  resolveAutoBackend,
  resolveBackend,
  usableBackends,
  type CodingBackendAvailability,
} from "./codingBackend.ts";

const NONE: CodingBackendAvailability = {
  localModelReady: false,
  claudeSubscription: false,
  anthropicKey: false,
  openaiKey: false,
  glmKey: false,
};

test("backendMeta resolves and rejects unknown", () => {
  assert.equal(backendMeta("local").kind, "on-device");
  assert.equal(backendMeta("anthropic").requiresKey, "anthropic");
  assert.throws(() => backendMeta("nope" as any));
});

test("auto prefers on-device, then subscription, then anthropic, then openai, then glm", () => {
  assert.equal(resolveAutoBackend({ ...NONE, localModelReady: true, anthropicKey: true }), "local");
  // subscription (free plan) OUTRANKS the metered BYO Claude key.
  assert.equal(resolveAutoBackend({ ...NONE, claudeSubscription: true, anthropicKey: true }), "subscription");
  assert.equal(resolveAutoBackend({ ...NONE, anthropicKey: true, openaiKey: true }), "anthropic");
  assert.equal(resolveAutoBackend({ ...NONE, openaiKey: true, glmKey: true }), "openai");
  assert.equal(resolveAutoBackend({ ...NONE, glmKey: true }), "glm");
  assert.equal(resolveAutoBackend(NONE), null);
});

test("subscription is usable when a mirrored plan token is present", () => {
  assert.equal(backendUsable("subscription", { ...NONE, claudeSubscription: true }), true);
  assert.equal(backendUsable("subscription", NONE), false);
  assert.equal(backendMeta("subscription").requiresKey, undefined); // no BYO key
});

test("backendUsable + usableBackends reflect availability", () => {
  const av = { ...NONE, openaiKey: true, glmKey: true };
  assert.equal(backendUsable("openai", av), true);
  assert.equal(backendUsable("local", av), false);
  assert.deepEqual(usableBackends(av), ["openai", "glm"]);
});

test("resolveBackend honors a usable explicit pick", () => {
  const av = { ...NONE, openaiKey: true, anthropicKey: true };
  const r = resolveBackend("openai", av);
  assert.equal(r.id, "openai");
  assert.equal(r.auto, false);
  assert.equal(r.fellBackFrom, undefined);
});

test("resolveBackend falls back when the explicit pick is unusable", () => {
  const av = { ...NONE, localModelReady: true }; // chose anthropic but no key
  const r = resolveBackend("anthropic", av);
  assert.equal(r.id, "local"); // auto fallback
  assert.equal(r.auto, true);
  assert.equal(r.fellBackFrom, "anthropic");
});

test("resolveBackend auto with nothing configured returns null id", () => {
  const r = resolveBackend("auto", NONE);
  assert.equal(r.id, null);
  assert.equal(r.auto, true);
});
