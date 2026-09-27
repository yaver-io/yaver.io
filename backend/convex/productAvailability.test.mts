import assert from "node:assert/strict";
import test from "node:test";
import {
  cloudWorkspacePublicEnabled,
  CLOUD_WORKSPACE_UNAVAILABLE,
  relayProCheckoutEnabled,
  RELAY_PRO_CHECKOUT_UNAVAILABLE,
} from "./productAvailability.ts";

test("Cloud Workspace is fail-closed by default", () => {
  assert.equal(cloudWorkspacePublicEnabled({}), false);
});

test("Relay Pro checkout is a separate explicit payment launch gate", () => {
  assert.equal(relayProCheckoutEnabled({}), false);
  assert.equal(relayProCheckoutEnabled({ YAVER_RELAY_PRO_CHECKOUT_ENABLED: "false" }), false);
  assert.equal(relayProCheckoutEnabled({ YAVER_RELAY_PRO_CHECKOUT_ENABLED: "1" }), false);
  assert.equal(relayProCheckoutEnabled({ YAVER_RELAY_PRO_CHECKOUT_ENABLED: "true" }), true);
  assert.match(RELAY_PRO_CHECKOUT_UNAVAILABLE, /not open yet/i);
});

test("Cloud Workspace cannot be revived by stale deployment configuration", () => {
  assert.equal(cloudWorkspacePublicEnabled({ YAVER_CLOUD_WORKSPACE_ENABLED: "true" }), false);
  assert.equal(cloudWorkspacePublicEnabled({ YAVER_CLOUD_WORKSPACE_ENABLED: "1" }), false);
  assert.equal(cloudWorkspacePublicEnabled({ YAVER_CLOUD_WORKSPACE_ENABLED: "false" }), false);
  assert.match(CLOUD_WORKSPACE_UNAVAILABLE, /Relay Pro/);
});
