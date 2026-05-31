// brain.test.mts — tests for brain selection (remote-first → local tiered →
// scripted) + connectivity / runner-OAuth triage.
// Run: npx tsx src/lib/localAgent/brain.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { selectBrain, localHandlesGeneralTroubleshooting } from "./brain.ts";
import { diagnoseConnectivity, diagnoseRunnerAuth, actionIsDispatchable } from "./connectivity.ts";

// ── brain selection ───────────────────────────────────────────────
test("brain: connected remote with ready runner wins over local", () => {
  const b = selectBrain({ connectedDeviceId: "mac1", connectedRunnerReady: true, localTier: "coder" });
  assert.equal(b.kind, "remote");
  assert.equal((b as any).deviceId, "mac1");
});

test("brain: reachable (not yet connected) remote still beats local", () => {
  const b = selectBrain({ reachableDeviceIds: ["box1"], localTier: "coder" });
  assert.equal(b.kind, "remote");
});

test("brain: no remote → local tier used", () => {
  assert.equal(selectBrain({ localTier: "router" }).kind, "local");
  assert.equal((selectBrain({ localTier: "coder" }) as any).tier, "coder");
});

test("brain: offline ignores remote, falls to local", () => {
  const b = selectBrain({ online: false, connectedDeviceId: "mac1", connectedRunnerReady: true, localTier: "router" });
  assert.equal(b.kind, "local");
});

test("brain: nothing reachable, no model → scripted", () => {
  assert.equal(selectBrain({ localTier: "none" }).kind, "scripted");
});

test("brain: local handles general troubleshooting only at coder tier", () => {
  assert.equal(localHandlesGeneralTroubleshooting({ localTier: "coder" }), true);
  assert.equal(localHandlesGeneralTroubleshooting({ localTier: "router" }), false);
  // remote present → not local's job
  assert.equal(localHandlesGeneralTroubleshooting({ connectedDeviceId: "x", connectedRunnerReady: true, localTier: "coder" }), false);
});

// ── connectivity triage ───────────────────────────────────────────
test("conn: offline phone", () => {
  assert.equal(diagnoseConnectivity({ online: false, hasAnyDevice: true, hasConnectedDevice: false }).code, "offline");
});

test("conn: no devices → onboarding install hint", () => {
  const d = diagnoseConnectivity({ hasAnyDevice: false, hasConnectedDevice: false });
  assert.equal(d.code, "no-devices");
  assert.match(d.shellHint ?? "", /npm install -g yaver-cli/);
});

test("conn: auth expired → recoverAuth", () => {
  const d = diagnoseConnectivity({ hasAnyDevice: true, hasConnectedDevice: false, lifecycle: "yaver-auth-expired" });
  assert.equal(d.code, "auth-expired");
  assert.equal(d.action, "device.recoverAuth");
});

test("conn: offline device → power on + yaver serve", () => {
  const d = diagnoseConnectivity({ hasAnyDevice: true, hasConnectedDevice: false, lifecycle: "offline" });
  assert.equal(d.code, "device-offline");
  assert.match(d.shellHint ?? "", /yaver serve/);
});

test("conn: manual auth exhausted", () => {
  const d = diagnoseConnectivity({ hasAnyDevice: true, hasConnectedDevice: false, manualAuthRequired: true });
  assert.equal(d.code, "manual-auth");
});

test("conn: connected → ok", () => {
  const d = diagnoseConnectivity({ hasAnyDevice: true, hasConnectedDevice: true, lifecycle: "connected" });
  assert.equal(d.code, "ok");
});

test("conn: every offered action is dispatchable (never BLOCKED)", () => {
  const inputs = [
    { hasAnyDevice: true, hasConnectedDevice: false, lifecycle: "yaver-auth-expired" as const },
    { hasAnyDevice: true, hasConnectedDevice: false, lifecycle: "bootstrap" as const },
    { hasAnyDevice: true, hasConnectedDevice: false, lifecycle: "ready-to-connect" as const },
    { hasAnyDevice: true, hasConnectedDevice: false, lastError: "boom" },
  ];
  for (const i of inputs) {
    const d = diagnoseConnectivity(i);
    if (d.action) assert.ok(actionIsDispatchable(d.action), `action ${d.action} must be dispatchable`);
  }
});

// ── runner OAuth triage ───────────────────────────────────────────
test("runner: ready", () => {
  const d = diagnoseRunnerAuth({ runners: { claude: { installed: true, authed: true } } });
  assert.equal(d.code, "ok");
  assert.equal(d.runner, "claude");
});

test("runner: installed but not authed → needs-auth (subscription OAuth)", () => {
  const d = diagnoseRunnerAuth({ runners: { codex: { installed: true, authed: false } }, wanted: "codex" });
  assert.equal(d.code, "needs-auth");
  assert.equal(d.runner, "codex");
  assert.match(d.say, /sign-in/i);
});

test("runner: not installed → needs-install", () => {
  const d = diagnoseRunnerAuth({ runners: { opencode: { installed: false, authed: false } }, wanted: "opencode" });
  assert.equal(d.code, "needs-install");
});

test("runner: none present → no-runners", () => {
  assert.equal(diagnoseRunnerAuth({ runners: {} }).code, "no-runners");
});

test("runner: prefers wanted over an already-authed other", () => {
  const d = diagnoseRunnerAuth({
    runners: { claude: { installed: true, authed: true }, codex: { installed: true, authed: false } },
    wanted: "codex",
  });
  assert.equal(d.runner, "codex");
  assert.equal(d.code, "needs-auth");
});
