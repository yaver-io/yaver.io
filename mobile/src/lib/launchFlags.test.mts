import assert from "node:assert/strict";
import test from "node:test";
import {
  ENABLE_CLOUD_WORKSPACE_UI,
  ENABLE_RELAY_PRO_UI,
  isHostedCloudSurfaceDevice,
} from "./launchFlags.ts";

test("Apple/mobile release exposes Relay Pro but no Cloud Workspace UI", () => {
  assert.equal(ENABLE_RELAY_PRO_UI, true);
  assert.equal(ENABLE_CLOUD_WORKSPACE_UI, false);
});

test("registry boundary removes hosted compute and preserves user-owned VPS", () => {
  assert.equal(isHostedCloudSurfaceDevice({ hosting: "yaver-hosted" }), true);
  assert.equal(isHostedCloudSurfaceDevice({ cloudWorkspaceId: "workspace-1" }), true);
  assert.equal(isHostedCloudSurfaceDevice({ deviceKind: "cloud-runner" }), true);
  assert.equal(isHostedCloudSurfaceDevice({ managed: true, machineId: "legacy-machine-1" }), true);
  assert.equal(isHostedCloudSurfaceDevice({ hosting: "byo" }), false);
  assert.equal(isHostedCloudSurfaceDevice({ hosting: "self-hosted" }), false);
  assert.equal(isHostedCloudSurfaceDevice({ managed: true }), false);
  assert.equal(isHostedCloudSurfaceDevice({}), false);
});
