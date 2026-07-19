import test from "node:test";
import assert from "node:assert/strict";

import {
  cloudMachineEligibleForPlacement,
  cloudMachineMeetsPlacement,
  cloudMachineTypeForPlacement,
  cloudWorkspaceProfileForPlacement,
  cloudWorkspaceProfileLabel,
  cloudWorkspaceProfilePolicy,
  includedHoursForCloudWorkspaceProfile,
  selectCloudMachineForPlacement,
  selectResizeSourceForPlacement,
} from "./cloudPlacementCapacity.js";

test("cloud placement capacity maps resource class to internal profile", () => {
  assert.equal(cloudMachineTypeForPlacement("standard"), "standard");
  assert.equal(cloudMachineTypeForPlacement("heavy"), "heavy");
  assert.equal(cloudMachineTypeForPlacement("build"), "build");
  assert.equal(cloudMachineTypeForPlacement("relay-source"), "standard");
  assert.equal(cloudWorkspaceProfileForPlacement("relay-source"), "standard");
  assert.equal(cloudWorkspaceProfileLabel("heavy"), "Heavy workspace");
});

test("cloud workspace flat plan uses weighted standard credits", () => {
  assert.deepEqual(cloudWorkspaceProfilePolicy("standard"), {
    profile: "standard",
    resourceClass: "standard",
    ramGb: 8,
    vcpu: 4,
    standardCreditWeight: 1,
    defaultIncludedStandardCredits: 120,
    minimumNetMargin: 0.4,
  });
  assert.equal(cloudWorkspaceProfilePolicy("heavy").standardCreditWeight, 2);
  assert.equal(cloudWorkspaceProfilePolicy("build").standardCreditWeight, 4);
  assert.equal(includedHoursForCloudWorkspaceProfile("standard"), 120);
  assert.equal(includedHoursForCloudWorkspaceProfile("heavy"), 60);
  assert.equal(includedHoursForCloudWorkspaceProfile("build"), 30);
  assert.equal(includedHoursForCloudWorkspaceProfile("build", 80), 20);
});

test("cloud placement capacity rejects underpowered machines", () => {
  const standard = { machineType: "standard", specs: { ramGb: 8 } };
  const heavy = { machineType: "heavy", specs: { ramGb: 16 } };
  const build = { machineType: "build", specs: { ramGb: 32 } };

  assert.equal(cloudMachineMeetsPlacement(standard, "standard"), true);
  assert.equal(cloudMachineMeetsPlacement(standard, "heavy"), false);
  assert.equal(cloudMachineMeetsPlacement(standard, "build"), false);
  assert.equal(cloudMachineMeetsPlacement(heavy, "heavy"), true);
  assert.equal(cloudMachineMeetsPlacement(heavy, "build"), false);
  assert.equal(cloudMachineMeetsPlacement(build, "heavy"), true);
  assert.equal(cloudMachineMeetsPlacement(build, "build"), true);
});

test("cloud placement eligibility allows only Yaver-managed usable Hetzner rows", () => {
  assert.equal(cloudMachineEligibleForPlacement({ status: "active" }), true);
  assert.equal(cloudMachineEligibleForPlacement({ status: "paused", origin: "managed", provider: "hetzner" }), true);
  assert.equal(cloudMachineEligibleForPlacement({ status: "grace", origin: "managed" }), true);
  assert.equal(cloudMachineEligibleForPlacement({ status: "stopped", origin: "managed" }), false);
  assert.equal(cloudMachineEligibleForPlacement({ status: "stopping", origin: "managed" }), false);
  assert.equal(cloudMachineEligibleForPlacement({ status: "error", origin: "managed" }), false);
  assert.equal(cloudMachineEligibleForPlacement({ status: "active", origin: "self-hosted" }), false);
  assert.equal(cloudMachineEligibleForPlacement({ status: "active", origin: "managed", provider: "aws" }), false);
});

test("cloud placement selection does not fall back to smaller machine", () => {
  const machines = [
    { _id: "standard-new", status: "active", machineType: "standard", specs: { ramGb: 8 }, updatedAt: 30 },
    { _id: "standard-old", status: "active", machineType: "standard", specs: { ramGb: 8 }, updatedAt: 20 },
  ];

  assert.equal(selectCloudMachineForPlacement(machines, "build"), null);
  assert.equal(selectCloudMachineForPlacement(machines, "heavy"), null);
  assert.equal(selectCloudMachineForPlacement(machines, "standard")?._id, "standard-new");
});

test("cloud placement selection ignores ineligible rows even when newest and powerful", () => {
  const machines = [
    { _id: "stopped-build", status: "stopped", origin: "managed", machineType: "build", specs: { ramGb: 32 }, updatedAt: 50 },
    { _id: "self-hosted-build", status: "active", origin: "self-hosted", machineType: "build", specs: { ramGb: 32 }, updatedAt: 40 },
    { _id: "wrong-provider-build", status: "active", origin: "managed", provider: "aws", machineType: "build", specs: { ramGb: 32 }, updatedAt: 30 },
    { _id: "paused-build", status: "paused", origin: "managed", provider: "hetzner", machineType: "build", specs: { ramGb: 32 }, updatedAt: 10 },
  ];

  assert.equal(selectCloudMachineForPlacement(machines, "build")?._id, "paused-build");
});

test("cloud placement selection ignores underpowered pinned placement machine", () => {
  const machines = [
    { _id: "standard-pinned", status: "active", machineType: "standard", specs: { ramGb: 8 }, updatedAt: 40 },
    { _id: "build-ok", status: "active", machineType: "build", specs: { ramGb: 32 }, updatedAt: 10 },
  ];

  assert.equal(selectCloudMachineForPlacement(machines, "build", "standard-pinned")?._id, "build-ok");
});

test("cloud resize source selection requires a persisted recovery source", () => {
  const machines = [
    {
      _id: "ephemeral-standard",
      status: "paused",
      machineType: "standard",
      specs: { ramGb: 8 },
      baseImageId: "img-standard",
      updatedAt: 40,
    },
    {
      _id: "volume-standard",
      status: "paused",
      machineType: "standard",
      specs: { ramGb: 8 },
      volumeId: "vol-standard",
      baseImageId: "img-standard",
      updatedAt: 10,
    },
  ];

  assert.equal(selectResizeSourceForPlacement(machines, "build")?._id, "volume-standard");
  assert.equal(selectResizeSourceForPlacement(machines, "standard"), null);
});

test("cloud resize source selection ignores ineligible persisted rows", () => {
  const machines = [
    {
      _id: "self-hosted-standard",
      status: "active",
      origin: "self-hosted",
      machineType: "standard",
      specs: { ramGb: 8 },
      volumeId: "vol-self",
      baseImageId: "img-self",
      updatedAt: 60,
    },
    {
      _id: "stopped-standard",
      status: "stopped",
      origin: "managed",
      machineType: "standard",
      specs: { ramGb: 8 },
      volumeId: "vol-stopped",
      baseImageId: "img-stopped",
      updatedAt: 50,
    },
    {
      _id: "managed-standard",
      status: "paused",
      origin: "managed",
      provider: "hetzner",
      machineType: "standard",
      specs: { ramGb: 8 },
      volumeId: "vol-managed",
      baseImageId: "img-managed",
      updatedAt: 10,
    },
  ];

  assert.equal(selectResizeSourceForPlacement(machines, "heavy")?._id, "managed-standard");
});

test("cloud resize source selection prefers the pinned underpowered machine", () => {
  const machines = [
    {
      _id: "newer-standard",
      status: "paused",
      machineType: "standard",
      specs: { ramGb: 8 },
      volumeId: "vol-new",
      baseImageId: "img-new",
      updatedAt: 40,
    },
    {
      _id: "pinned-standard",
      status: "paused",
      machineType: "standard",
      specs: { ramGb: 8 },
      volumeId: "vol-pinned",
      baseImageId: "img-pinned",
      updatedAt: 10,
    },
  ];

  assert.equal(selectResizeSourceForPlacement(machines, "heavy", "pinned-standard")?._id, "pinned-standard");
});
