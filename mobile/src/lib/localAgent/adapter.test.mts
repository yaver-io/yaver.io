// adapter.test.mts — unit tests for the state-in / action-out adapter.
// Run: npx tsx src/lib/localAgent/adapter.test.mts
// Pure logic via dependency injection — no RN.

import test from "node:test";
import assert from "node:assert/strict";

import {
  deviceFactsFrom,
  buildLadderState,
  extractGoal,
  dispatchAction,
  type DeviceStateLike,
  type DeviceLike,
  type DispatchDeps,
} from "./adapter.ts";
import { capabilityLadder } from "./capabilityLadder.ts";

const baseState = (over: Partial<DeviceStateLike> = {}): DeviceStateLike => ({
  devices: [],
  connectionStatus: "disconnected",
  ...over,
});

const mac: DeviceLike = { id: "mac1", name: "MacBook", alias: "mac", os: "macos", online: true };

// ── deviceFactsFrom ─────────────────────────────────────────────────
test("facts: connected via connectedDeviceIds", () => {
  const s = baseState({ devices: [mac], connectedDeviceIds: ["mac1"], connectionStatus: "connected" });
  const f = deviceFactsFrom(mac, s);
  assert.equal(f.connected, true);
  assert.equal(f.lifecycle, "connected");
});

test("facts: audit lifecycle string is honored when not connected", () => {
  const s = baseState({ devices: [mac] });
  const f = deviceFactsFrom(mac, s, { audit: { lifecycleState: "yaver-auth-expired" } });
  assert.equal(f.lifecycle, "yaver-auth-expired");
});

test("facts: runners mapped from audit (authed ⇔ authConfigured && ready)", () => {
  const s = baseState({ devices: [mac], connectedDeviceIds: ["mac1"], connectionStatus: "connected" });
  const f = deviceFactsFrom(mac, s, {
    audit: {
      runners: [
        { id: "claude", installed: true, ready: true, authConfigured: true },
        { id: "codex", installed: true, ready: true, authConfigured: false },
        { id: "bogus", installed: true, authConfigured: true }, // ignored (not a RunnerId)
      ],
    },
  });
  assert.deepEqual(f.runners.claude, { installed: true, authed: true });
  assert.deepEqual(f.runners.codex, { installed: true, authed: false });
  assert.equal((f.runners as any).bogus, undefined);
});

test("facts: projects map name→slug; unknown git/active default to undefined", () => {
  const s = baseState({ devices: [mac], connectedDeviceIds: ["mac1"], connectionStatus: "connected" });
  const f = deviceFactsFrom(mac, s, { projects: [{ name: "api", branch: "main" }] });
  assert.deepEqual(f.projects, [{ slug: "api", branch: "main" }]);
  assert.equal(f.gitAuthed, undefined);
  assert.equal(f.activeProjectSlug, undefined);
});

test("facts: offline derivation from peerState/online", () => {
  const s = baseState({ devices: [{ ...mac, online: false }] });
  assert.equal(deviceFactsFrom({ ...mac, online: false }, s).lifecycle, "offline");
});

// ── buildLadderState → ladder end-to-end ────────────────────────────
test("buildLadderState: connected box with a ready runner + project feeds a satisfied code goal", () => {
  const s = baseState({ devices: [mac], connectedDeviceIds: ["mac1"], connectionStatus: "connected" });
  const ls = buildLadderState(s, {
    online: true,
    localTier: "router",
    target: mac,
    probe: {
      audit: { runners: [{ id: "claude", installed: true, ready: true, authConfigured: true }] },
      projects: [{ name: "api" }],
      activeProjectSlug: "api",
    },
  });
  assert.equal(capabilityLadder(ls, { kind: "code" }).nextStep, null);
});

test("buildLadderState: hasAnyDevice + reachable derived from device list", () => {
  const s = baseState({ devices: [mac, { id: "x", name: "x", online: false }], unreachableDeviceIds: ["x"] });
  const ls = buildLadderState(s, { online: true, localTier: "none" });
  assert.equal(ls.hasAnyDevice, true);
  assert.deepEqual(ls.reachableDeviceIds, ["mac1"]);
});

// ── extractGoal ─────────────────────────────────────────────────────
test("extractGoal: keyword intents", () => {
  assert.equal(extractGoal("connect to my mac")?.kind, "connect");
  assert.equal(extractGoal("push my changes")?.kind, "push");
  assert.equal(extractGoal("deploy it")?.kind, "deploy");
  assert.equal(extractGoal("run it on my phone")?.kind, "preview");
  assert.equal(extractGoal("build me a login screen")?.kind, "code");
  assert.equal(extractGoal("start a new app")?.kind, "code");
  assert.equal((extractGoal("start a new app") as any)?.fresh, true);
});

test("extractGoal: no clear intent → undefined (let the model/ask decide)", () => {
  assert.equal(extractGoal("hmm what's going on"), undefined);
  assert.equal(extractGoal(""), undefined);
});

// ── dispatchAction: tier enforcement ────────────────────────────────
function fakeDeps(over: Partial<DispatchDeps> = {}): DispatchDeps & { calls: string[] } {
  const calls: string[] = [];
  return {
    calls,
    context: {
      selectDevice: async () => { calls.push("selectDevice"); },
      recoverDeviceAuth: async () => { calls.push("recoverDeviceAuth"); },
    },
    ops: async (verb, payload) => { calls.push(`ops:${verb}:${JSON.stringify(payload)}`); return { ok: true }; },
    mcp: async (tool) => { calls.push(`mcp:${tool}`); return { ok: true }; },
    ...over,
  };
}

test("dispatch: BLOCKED action is refused, never runs", async () => {
  const deps = fakeDeps();
  const r = await dispatchAction("destroy", { device: mac }, deps);
  assert.equal(r.blocked, true);
  assert.equal(r.ran, false);
  assert.equal(deps.calls.length, 0);
});

test("dispatch: CONFIRM action withheld without approval, runs with it", async () => {
  const deps1 = fakeDeps();
  const held = await dispatchAction("git.push", { device: mac }, deps1);
  assert.equal(held.needsConfirm, true);
  assert.equal(deps1.calls.length, 0);

  const deps2 = fakeDeps({ confirmed: true });
  const ran = await dispatchAction("git.push", { device: mac }, deps2);
  assert.equal(ran.ran, true);
  assert.ok(deps2.calls.some((c) => c.startsWith("ops:run")));
});

test("dispatch: SAFE_WRITE context action runs immediately", async () => {
  const deps = fakeDeps();
  const r = await dispatchAction("device.select", { device: mac }, deps);
  assert.equal(r.ok, true);
  assert.deepEqual(deps.calls, ["selectDevice"]);
});

test("dispatch: ops action routes the right verb + default payload", async () => {
  const deps = fakeDeps();
  await dispatchAction("project.list", { device: mac }, deps);
  assert.ok(deps.calls.some((c) => c === 'ops:workspace:{"op":"list"}'));

  const deps2 = fakeDeps();
  await dispatchAction("git.connect", { device: mac }, deps2);
  assert.ok(deps2.calls.some((c) => c.startsWith('ops:git_connect:') && c.includes('"provider":"github"')));
});

test("dispatch: needsDevice action with no device errors cleanly", async () => {
  const deps = fakeDeps();
  const r = await dispatchAction("device.select", {}, deps);
  assert.equal(r.ok, false);
  assert.match(r.error ?? "", /needs a device/);
});

test("dispatch: a context fn that isn't wired returns 'not wired', not a crash", async () => {
  const deps = fakeDeps({ context: {} });
  const r = await dispatchAction("device.recoverAuth", { device: mac }, deps);
  assert.equal(r.ok, false);
  assert.match(r.error ?? "", /not wired/);
});

test("dispatch: unknown action id", async () => {
  const r = await dispatchAction("nope.nope", { device: mac }, fakeDeps());
  assert.equal(r.ok, false);
  assert.match(r.error ?? "", /unknown action/);
});
