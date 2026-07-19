import test from "node:test";
import assert from "node:assert/strict";

import { cloudWorkspaceUpgradeBillingGate, planEntitlements } from "./plans.js";

test("byok Cloud Workspace grants weighted standard-credit hours", () => {
  const entitlements = planEntitlements("byok");
  assert.equal(entitlements.includedHoursStandard, 120);
  assert.equal(entitlements.includedHoursHeavy, 60);
  assert.equal(entitlements.includedHoursBuild, 30);
  assert.equal(entitlements.gateway.enabled, false);
});

test("hosted legacy plan remains managed-inference gated", () => {
  const entitlements = planEntitlements("hosted");
  assert.equal(entitlements.gateway.enabled, true);
  assert.ok(entitlements.gateway.dailyCapCents > 0);
});

test("cloud workspace upgrade billing gate fails closed without LemonSqueezy subscription", () => {
  assert.deepEqual(cloudWorkspaceUpgradeBillingGate({}), {
    ok: false,
    reason: "missing-lemonsqueezy-subscription",
  });
});

test("cloud workspace upgrade billing gate preserves LemonSqueezy sync failure reason", () => {
  assert.deepEqual(
    cloudWorkspaceUpgradeBillingGate({
      lemonSqueezyId: "123",
      billing: { ok: false, reason: "variant-unconfigured" },
    }),
    { ok: false, reason: "variant-unconfigured" },
  );
});

test("cloud workspace upgrade billing gate passes only after billing sync succeeds", () => {
  assert.deepEqual(
    cloudWorkspaceUpgradeBillingGate({
      lemonSqueezyId: "123",
      billing: { ok: true },
    }),
    { ok: true },
  );
});
