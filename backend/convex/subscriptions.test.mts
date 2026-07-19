import test from "node:test";
import assert from "node:assert/strict";

import { isCloudWorkspaceSubscriptionPlan } from "./subscriptions.js";

test("cloud workspace plan classifier excludes free and Relay Pro", () => {
  assert.equal(isCloudWorkspaceSubscriptionPlan("cloud-workspace"), true);
  assert.equal(isCloudWorkspaceSubscriptionPlan("cloud-agent"), true);
  assert.equal(isCloudWorkspaceSubscriptionPlan("yaver-cloud-byok"), true);

  assert.equal(isCloudWorkspaceSubscriptionPlan("relay-pro"), false);
  assert.equal(isCloudWorkspaceSubscriptionPlan("relay-monthly"), false);
  assert.equal(isCloudWorkspaceSubscriptionPlan("free"), false);
  assert.equal(isCloudWorkspaceSubscriptionPlan(""), false);
  assert.equal(isCloudWorkspaceSubscriptionPlan(null), false);
});
