// orchestrator.test.mts — planRequest (brain+device+grammar) + gateAction.
// Run: npx tsx src/lib/localAgent/orchestrator.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { planRequest, gateAction } from "./orchestrator.ts";
import type { DeviceRef } from "./resolver.ts";

const DEVICES: DeviceRef[] = [
  { deviceId: "aaaa1111", name: "Hetzner box", alias: "hetzner", platform: "linux", online: true },
  { deviceId: "bbbb2222", name: "MacBook", alias: "mac", platform: "macos", online: true, isPrimary: true },
];

test("plan: remote-first brain when connected", () => {
  const p = planRequest({
    utterance: "switch the hetzner box to codex",
    intent: "command",
    connectivity: { connectedDeviceId: "bbbb2222", connectedRunnerReady: true, localTier: "router" },
    devices: DEVICES,
  });
  assert.equal(p.brain.kind, "remote");
  assert.equal(p.device?.kind, "resolved");
  assert.equal((p.device as any).device.deviceId, "aaaa1111");
  // grammar constrains to allowed actions, excludes blocked
  assert.ok(p.grammar && p.grammar.includes("runner.switch"));
  assert.ok(!p.grammar!.includes("cloud.destroy"));
});

test("plan: local brain when no remote", () => {
  const p = planRequest({
    utterance: "is my mac online",
    intent: "troubleshoot",
    connectivity: { localTier: "router" },
    devices: DEVICES,
  });
  assert.equal(p.brain.kind, "local");
  assert.equal(p.device?.kind, "resolved");
  assert.equal((p.device as any).device.deviceId, "bbbb2222");
});

test("plan: scripted brain when nothing available", () => {
  const p = planRequest({
    utterance: "help",
    intent: "troubleshoot",
    connectivity: { localTier: "none" },
    devices: [],
  });
  assert.equal(p.brain.kind, "scripted");
});

test("plan: sandbox-code skips device resolution + grammar", () => {
  const p = planRequest({
    utterance: "add a login form",
    intent: "sandbox-code",
    connectivity: { localTier: "coder" },
    devices: DEVICES,
  });
  assert.equal(p.device, undefined);
  assert.equal(p.grammar, undefined);
  assert.match(p.note, /Sandbox/);
});

test("plan: ambiguous device → note says it'll ask", () => {
  const twoMacs: DeviceRef[] = [
    { deviceId: "m1", name: "Work Mac", platform: "macos" },
    { deviceId: "m2", name: "Home Mac", platform: "macos" },
  ];
  const p = planRequest({
    utterance: "the mac",
    intent: "command",
    connectivity: { localTier: "router" },
    devices: twoMacs,
  });
  assert.equal(p.device?.kind, "ambiguous");
  assert.match(p.note, /ask which one/);
});

test("gate: auto / confirm / blocked / unknown", () => {
  assert.deepEqual(gateAction({ action: "status" }), { allow: true, needsConfirm: false, action: { action: "status" } });
  const dep = gateAction({ action: "deploy" });
  assert.equal(dep.allow, true);
  assert.equal((dep as any).needsConfirm, true);
  const blk = gateAction({ action: "cloud.destroy" });
  assert.equal(blk.allow, false);
  assert.equal((blk as any).reason, "blocked");
  const unk = gateAction({ action: "rm -rf /" });
  assert.equal(unk.allow, false);
  assert.equal((unk as any).reason, "unknown");
});

test("gate: recovery-provider calls are allowed (auto)", () => {
  for (const a of ["recovery.reauthStart", "recovery.targetStart", "recovery.transportStatus"]) {
    const g = gateAction({ action: a, deviceRef: "hetzner" });
    assert.equal(g.allow, true, `${a} allowed`);
    assert.equal((g as any).needsConfirm, false, `${a} auto`);
  }
});
