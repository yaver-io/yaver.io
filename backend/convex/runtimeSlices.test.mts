import test from "node:test";
import assert from "node:assert/strict";

import { buildWorkspaceRuntimePlan } from "./runtimeSlices.js";

test("cloud workspace includes Relay Pro even when compute is parked", () => {
  const plan = buildWorkspaceRuntimePlan({
    plan: "cloud-workspace",
    computeProvider: "hetzner",
    computeState: "parked",
  });

  assert.equal(plan.relay.kind, "relay-pro");
  assert.equal(plan.relay.persistsWhenComputeParked, true);
  assert.equal(plan.relay.authorizationBoundary, "device-keys");
  assert.equal(plan.compute?.provider, "hetzner");
  assert.equal(plan.compute?.state, "parked");
});

test("workspace relay sidecar is active-only and falls back to Relay Pro", () => {
  const plan = buildWorkspaceRuntimePlan({
    plan: "cloud-workspace",
    computeProvider: "hetzner",
    computeState: "active",
    preferWorkspaceSidecarRelay: true,
  });

  assert.equal(plan.relay.kind, "workspace-sidecar");
  assert.equal(plan.relay.persistsWhenComputeParked, false);
  assert.deepEqual(plan.relay.fallbackKinds, ["relay-pro", "public-free"]);
});

test("compute provider does not imply inference provider", () => {
  const plan = buildWorkspaceRuntimePlan({
    plan: "cloud-workspace",
    computeProvider: "azure",
    computeState: "active",
    inferenceMode: "byo",
  });

  assert.equal(plan.compute?.provider, "azure");
  assert.equal(plan.inference.mode, "byo");
  assert.equal(plan.inference.provider, "byo");
  assert.equal(plan.inference.keyPolicy, "user-vault");
});

test("trial inference chooses provider credits and requires a hard budget", () => {
  const plan = buildWorkspaceRuntimePlan({
    plan: "cloud-workspace",
    computeProvider: "hetzner",
    computeState: "active",
    inferenceMode: "trial",
  });

  assert.equal(plan.inference.mode, "trial");
  assert.equal(plan.inference.usesProviderCredits, true);
  assert.equal(plan.inference.hardBudgetRequired, true);
});
