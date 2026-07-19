import test from "node:test";
import assert from "node:assert/strict";

import {
  creditEstimateFor,
  selectLatestPlacementStatusWakeRun,
  selectTaskPlacementCloudMachine,
  subscriptionProductForPlacement,
  subscriptionStatusAllowsPlacement,
} from "./taskPlacement.js";

test("task placement treats active and past_due subscriptions as usable product access", () => {
  assert.equal(subscriptionStatusAllowsPlacement("active"), true);
  assert.equal(subscriptionStatusAllowsPlacement("past_due"), true);
  assert.equal(subscriptionStatusAllowsPlacement("cancelled"), false);
  assert.equal(subscriptionProductForPlacement("cloud-workspace", "active"), "cloud-workspace");
  assert.equal(subscriptionProductForPlacement("cloud-workspace", "past_due"), "cloud-workspace");
  assert.equal(subscriptionProductForPlacement("relay-pro", "past_due"), "relay-pro");
  assert.equal(subscriptionProductForPlacement("cloud-workspace", "cancelled"), "free");
});

test("cloud placement estimates expose standard-credit allowance metadata", () => {
  const standard = creditEstimateFor({
    kind: "vibe",
    lane: "cloud_standard",
    resourceClass: "standard",
    plan: "cloud-workspace",
  });
  assert.equal(standard.standardCredits, 0.5);
  assert.equal(standard.creditWeight, 1);
  assert.equal(standard.includedStandardCreditsBucket, 120);
  assert.match(standard.display, /0\.5 standard credits/);

  const heavy = creditEstimateFor({
    kind: "test",
    lane: "cloud_heavy",
    resourceClass: "heavy",
    plan: "cloud-workspace",
  });
  assert.equal(heavy.standardCredits, 1.5);
  assert.equal(heavy.creditWeight, 2);
  assert.equal(heavy.includedStandardCreditsBucket, 120);

  const build = creditEstimateFor({
    kind: "build",
    lane: "cloud_build",
    resourceClass: "build",
    plan: "cloud-workspace",
  });
  assert.equal(build.standardCredits, 4);
  assert.equal(build.creditWeight, 4);
  assert.equal(build.includedHoursBucket, 30);
  assert.equal(build.includedStandardCreditsBucket, 120);
});

test("relay and owned-machine placement estimates avoid cloud allowance copy", () => {
  const relay = creditEstimateFor({
    kind: "source",
    lane: "relay_source",
    resourceClass: "relay-source",
    plan: "relay-pro",
  });
  assert.equal(relay.display, "Included in Relay");
  assert.equal(relay.standardCredits, undefined);

  const owned = creditEstimateFor({
    kind: "build",
    lane: "owned_machine",
    resourceClass: "build",
    plan: "cloud-workspace",
  });
  assert.equal(owned.display, "No Yaver compute charge");
});

test("task placement cloud candidate refuses underpowered existing workspaces", () => {
  const machines = [
    {
      _id: "standard-active",
      status: "active",
      machineType: "standard",
      specs: { ramGb: 8 },
      updatedAt: 30,
    },
    {
      _id: "standard-paused",
      status: "paused",
      machineType: "standard",
      specs: { ramGb: 8 },
      updatedAt: 20,
    },
  ];

  assert.equal(selectTaskPlacementCloudMachine(machines, "standard")?._id, "standard-active");
  assert.equal(selectTaskPlacementCloudMachine(machines, "heavy"), null);
  assert.equal(selectTaskPlacementCloudMachine(machines, "build"), null);
});

test("task placement cloud candidate can pick an older machine that actually fits", () => {
  const machines = [
    {
      _id: "standard-new",
      status: "active",
      machineType: "standard",
      specs: { ramGb: 8 },
      updatedAt: 30,
    },
    {
      _id: "build-old",
      status: "paused",
      machineType: "build",
      specs: { ramGb: 32 },
      updatedAt: 10,
    },
  ];

  assert.equal(selectTaskPlacementCloudMachine(machines, "build")?._id, "build-old");
  assert.equal(selectTaskPlacementCloudMachine(machines, "heavy")?._id, "build-old");
});

test("task placement cloud candidate ignores dead or deleting managed rows", () => {
  const machines = [
    {
      _id: "stopped-build",
      status: "stopped",
      origin: "managed",
      machineType: "build",
      specs: { ramGb: 32 },
      updatedAt: 80,
    },
    {
      _id: "stopping-build",
      status: "stopping",
      origin: "managed",
      machineType: "build",
      specs: { ramGb: 32 },
      updatedAt: 70,
    },
    {
      _id: "error-build",
      status: "error",
      origin: "managed",
      machineType: "build",
      specs: { ramGb: 32 },
      updatedAt: 60,
    },
    {
      _id: "paused-build",
      status: "paused",
      origin: "managed",
      machineType: "build",
      specs: { ramGb: 32 },
      updatedAt: 10,
    },
  ];

  assert.equal(selectTaskPlacementCloudMachine(machines, "build")?._id, "paused-build");
});

test("task placement cloud candidate ignores explicit self-hosted rows", () => {
  const machines = [
    {
      _id: "self-hosted-build",
      status: "active",
      origin: "self-hosted",
      machineType: "build",
      specs: { ramGb: 32 },
      updatedAt: 40,
    },
    {
      _id: "managed-standard",
      status: "active",
      origin: "managed",
      machineType: "standard",
      specs: { ramGb: 8 },
      updatedAt: 30,
    },
  ];

  assert.equal(selectTaskPlacementCloudMachine(machines, "standard")?._id, "managed-standard");
  assert.equal(selectTaskPlacementCloudMachine(machines, "build"), null);
});

test("task placement cloud candidate keeps legacy managed rows with no origin", () => {
  const machines = [
    {
      _id: "legacy-build",
      status: "suspended",
      machineType: "build",
      specs: { ramGb: 32 },
      updatedAt: 10,
    },
  ];

  assert.equal(selectTaskPlacementCloudMachine(machines, "build")?._id, "legacy-build");
});

test("placement status wake selection prefers directly linked wake runs", () => {
  const linked = [
    { _id: "linked-old", kind: "wake", startedAt: 10, updatedAt: 20 },
    { _id: "linked-new", kind: "wake", startedAt: 10, updatedAt: 30 },
  ];
  const machine = [
    { _id: "machine-newer", kind: "provision", startedAt: 10, updatedAt: 50 },
  ];

  assert.equal(selectLatestPlacementStatusWakeRun(linked, machine)?._id, "linked-new");
});

test("placement status wake selection ignores linked park runs", () => {
  const linked = [
    { _id: "park-newest", kind: "park", startedAt: 10, updatedAt: 90 },
    { _id: "linked-wake", kind: "wake", startedAt: 10, updatedAt: 30 },
  ];
  const machine = [
    { _id: "machine-provision", kind: "provision", startedAt: 10, updatedAt: 70 },
  ];

  assert.equal(selectLatestPlacementStatusWakeRun(linked, machine)?._id, "linked-wake");
});

test("placement status wake selection falls back when linked runs are only park runs", () => {
  const linked = [
    { _id: "linked-park", kind: "park", startedAt: 10, updatedAt: 90 },
  ];
  const machine = [
    { _id: "machine-wake", kind: "wake", startedAt: 10, updatedAt: 40 },
  ];

  assert.equal(selectLatestPlacementStatusWakeRun(linked, machine)?._id, "machine-wake");
});

test("placement status wake selection falls back to latest machine wake or provision", () => {
  const machine = [
    { _id: "park-newest", kind: "park", startedAt: 10, updatedAt: 90 },
    { _id: "wake-old", kind: "wake", startedAt: 10, updatedAt: 20 },
    { _id: "provision-new", kind: "provision", startedAt: 10, updatedAt: 60 },
  ];

  assert.equal(selectLatestPlacementStatusWakeRun([], machine)?._id, "provision-new");
});
