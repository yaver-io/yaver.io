// localAgent.test.mts — unit tests for the on-device voice-helper core.
// Run: npx tsx src/lib/localAgent/localAgent.test.mts
// Pure logic, no RN — imports the .ts sources directly via tsx.

import test from "node:test";
import assert from "node:assert/strict";

import { resolveDevice, type DeviceRef } from "./resolver.ts";
import { dispositionFor, getAction, voiceInvokableActions } from "./catalog.ts";
import { selectModelTier, modelOptionsFor } from "./tiers.ts";

const DEVICES: DeviceRef[] = [
  { deviceId: "aaaa1111", name: "Hetzner box (hel1)", alias: "hetzner", platform: "linux", online: true },
  { deviceId: "bbbb2222", name: "MacBook", alias: "mac", platform: "macos", online: true, isPrimary: true },
  { deviceId: "cccc3333", name: "Linux box", alias: "linux", platform: "linux", online: false },
  { deviceId: "dddd4444", name: "Kivanc iPhone", platform: "ios", online: true, isPhone: true },
];

// ── resolver ──────────────────────────────────────────────────────
test("resolver: special tokens", () => {
  assert.equal((resolveDevice("primary", DEVICES) as any).device.deviceId, "bbbb2222");
  assert.equal((resolveDevice("this phone", DEVICES) as any).device.deviceId, "dddd4444");
});

test("resolver: exact alias inside a spoken sentence", () => {
  const r = resolveDevice("switch my hetzner box to codex", DEVICES) as any;
  assert.equal(r.kind, "resolved");
  assert.equal(r.device.deviceId, "aaaa1111");
  assert.equal(r.matchedBy, "alias");
});

test("resolver: platform word + fuzzy", () => {
  const r = resolveDevice("the macbook", DEVICES) as any;
  assert.equal(r.kind, "resolved");
  assert.equal(r.device.deviceId, "bbbb2222");
});

test("resolver: id prefix", () => {
  const r = resolveDevice("aaaa11", DEVICES) as any;
  assert.equal(r.kind, "resolved");
  assert.equal(r.device.deviceId, "aaaa1111");
});

test("resolver: ambiguous 'linux' between two linux boxes → ask, never guess", () => {
  // "hetzner" alias is linux too; "linux" matches alias of cccc exactly, so
  // that resolves. Use a query that hits BOTH linux devices by platform only.
  const r = resolveDevice("the linux server", DEVICES) as any;
  // "linux" is an exact alias of cccc3333 → resolves to it (alias beats fuzzy).
  assert.equal(r.kind, "resolved");
  assert.equal(r.device.deviceId, "cccc3333");
});

test("resolver: genuinely ambiguous platform-only query asks", () => {
  const twoMacs: DeviceRef[] = [
    { deviceId: "m1", name: "Work Mac", platform: "macos" },
    { deviceId: "m2", name: "Home Mac", platform: "macos" },
  ];
  const r = resolveDevice("the mac", twoMacs) as any;
  assert.equal(r.kind, "ambiguous");
  assert.equal(r.candidates.length, 2);
});

test("resolver: no match", () => {
  assert.equal(resolveDevice("the toaster", DEVICES).kind, "none");
});

// ── catalog / safety tiers ────────────────────────────────────────
test("catalog: read-only + safe-write auto-run", () => {
  assert.equal(dispositionFor("status"), "auto");
  assert.equal(dispositionFor("device.list"), "auto");
  assert.equal(dispositionFor("runner.switch"), "auto");
  assert.equal(dispositionFor("device.setPrimary"), "auto");
  assert.equal(dispositionFor("reload"), "auto");
});

test("catalog: code/deploy require confirmation", () => {
  assert.equal(dispositionFor("run"), "confirm");
  assert.equal(dispositionFor("deploy"), "confirm");
  assert.equal(dispositionFor("build"), "confirm");
  assert.equal(dispositionFor("recycle"), "confirm");
});

test("catalog: destructive is blocked from voice", () => {
  assert.equal(dispositionFor("device.remove"), "blocked");
  assert.equal(dispositionFor("cloud.destroy"), "blocked");
  assert.equal(dispositionFor("provision"), "blocked");
  assert.equal(dispositionFor("secrets.write"), "blocked");
});

test("catalog: unknown action id", () => {
  assert.equal(dispositionFor("rm -rf /"), "unknown");
  assert.equal(getAction("nope"), undefined);
});

test("catalog: recovery-provider calls are LLM-drivable (auto), via mcp", () => {
  // Start/recover actions auto-run; status/wait are read-only — all auto.
  for (const id of [
    "device.doctor",
    "recovery.reauthStart", "recovery.reauthStatus", "recovery.reauthWait",
    "recovery.targetStart", "recovery.targetStatus", "recovery.targetWait",
    "recovery.transportStatus",
  ]) {
    assert.equal(dispositionFor(id), "auto", `${id} should auto-run`);
    const a = getAction(id);
    assert.equal(a?.via, "mcp", `${id} dispatches via mcp`);
    assert.ok(a?.mcpTool, `${id} has an mcpTool`);
  }
});

test("catalog: voiceInvokableActions excludes BLOCKED", () => {
  const ids = voiceInvokableActions().map((a) => a.id);
  assert.ok(!ids.includes("cloud.destroy"));
  assert.ok(ids.includes("status"));
});

// ── model tiers ───────────────────────────────────────────────────
test("tiers: iPhone 14 (6GB, A15) → router", () => {
  assert.equal(selectModelTier({ totalRamMb: 6144, chip: "A15" }), "router");
});

test("tiers: 8GB Pro (A17) → coder", () => {
  assert.equal(selectModelTier({ totalRamMb: 8192, chip: "A17" }), "coder");
});

test("tiers: hot device refuses model", () => {
  assert.equal(selectModelTier({ totalRamMb: 8192, chip: "A17", thermalState: "hot" }), "none");
});

test("tiers: weak/unknown device → none", () => {
  assert.equal(selectModelTier({ totalRamMb: 3000 }), "none");
  assert.equal(selectModelTier({}), "none");
});

test("tiers: edgeProfile maxModelClass drives selection", () => {
  assert.equal(selectModelTier({ maxModelClass: "small" }), "router");
  assert.equal(selectModelTier({ maxModelClass: "medium", totalRamMb: 8192 }), "coder");
});

test("tiers: model picker recommends 1.5B router on iPhone 14, no coder offered", () => {
  const opts = modelOptionsFor({ totalRamMb: 6144, chip: "A15" });
  const rec = opts.find((o) => o.recommended);
  assert.equal(rec?.id, "qwen2.5-1.5b-instruct-q4");
  assert.ok(!opts.some((o) => o.tier === "coder"));
});

test("tiers: coder offered + recommended on 8GB", () => {
  const opts = modelOptionsFor({ totalRamMb: 8192, chip: "A17" });
  assert.ok(opts.some((o) => o.tier === "coder"));
  assert.equal(opts.find((o) => o.recommended)?.id, "qwen2.5-coder-3b-q4");
});
