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

test("auto prefers on-device, then GLM, then anthropic, then openai (NO subscription)", () => {
  assert.equal(resolveAutoBackend({ ...NONE, localModelReady: true, anthropicKey: true }), "local");
  // GLM is the cheap compliant cloud default.
  assert.equal(resolveAutoBackend({ ...NONE, glmKey: true, anthropicKey: true }), "glm");
  assert.equal(resolveAutoBackend({ ...NONE, anthropicKey: true, openaiKey: true }), "anthropic");
  assert.equal(resolveAutoBackend({ ...NONE, openaiKey: true }), "openai");
  // A mirrored plan token alone does NOT enable the in-app loop (compliance).
  assert.equal(resolveAutoBackend({ ...NONE, claudeSubscription: true }), null);
  assert.equal(resolveAutoBackend(NONE), null);
});

test("subscription is NEVER usable in the in-app loop (CLI-only, compliance)", () => {
  // Even with a mirrored plan token present, the in-app loop can't use it.
  assert.equal(backendUsable("subscription", { ...NONE, claudeSubscription: true }), false);
  assert.equal(backendUsable("subscription", NONE), false);
  // It's also never auto-selected and never appears in usableBackends.
  assert.equal(usableBackends({ ...NONE, claudeSubscription: true }).includes("subscription"), false);
  assert.equal(resolveBackend("subscription", { ...NONE, claudeSubscription: true, glmKey: true }).id, "glm");
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
