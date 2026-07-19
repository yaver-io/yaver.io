/**
 * quicCloudWorkspaceRequired.test.mts — `npx tsx src/lib/quicCloudWorkspaceRequired.test.mts`.
 * Pins the mobile transport boundary for agent-side Cloud Workspace deferrals.
 */
import assert from "node:assert/strict";
import test from "node:test";

import {
  CloudWorkspaceRequiredError,
  decodeCloudWorkspaceRequiredError,
} from "./cloudWorkspaceRequired.ts";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

test("mobile quic decoder preserves structured cloud workspace defer metadata", async () => {
  const err = await decodeCloudWorkspaceRequiredError(jsonResponse(409, {
    action: "cloud_workspace_required",
    pendingTaskId: "pending-cloud:mobile",
    reason: "Cloud Workspace is waking.",
    placement: {
      id: "placement-1",
      lane: "cloud_build",
      resourceClass: "build",
      targetDeviceId: "cloud-device",
      cloudMachineId: "machine-1",
      reason: "native build",
      creditEstimate: { display: "~4 standard credits" },
    },
    activation: {
      ok: false,
      action: "runner_auth_required",
      targetDeviceId: "cloud-device",
      wakeRunId: "wake-1",
      reason: "Codex needs sign-in.",
    },
  }));

  assert.ok(err instanceof CloudWorkspaceRequiredError);
  assert.equal(err.pendingTaskId, "pending-cloud:mobile");
  assert.equal(err.placement?.id, "placement-1");
  assert.equal(err.placement?.targetDeviceId, "cloud-device");
  assert.equal(err.activation?.action, "runner_auth_required");
  assert.equal(err.activation?.wakeRunId, "wake-1");
  assert.match(err.message, /Cloud Workspace is waking/);
});

test("mobile quic decoder ignores unrelated conflicts and malformed deferrals", async () => {
  assert.equal(await decodeCloudWorkspaceRequiredError(jsonResponse(400, {
    action: "cloud_workspace_required",
    pendingTaskId: "pending-cloud:mobile",
  })), null);
  assert.equal(await decodeCloudWorkspaceRequiredError(jsonResponse(409, {
    error: "busy",
  })), null);
  assert.equal(await decodeCloudWorkspaceRequiredError(jsonResponse(409, {
    action: "cloud_workspace_required",
  })), null);
});
