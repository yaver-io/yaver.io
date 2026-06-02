// voiceSession.test.mts — unit tests for the per-turn voice orchestrator.
// Run: npx tsx src/lib/localAgent/voiceSession.test.mts
// Pure via dependency injection — no RN, no model required.

import test from "node:test";
import assert from "node:assert/strict";

import { createVoiceSession, buildVoicePrompt, type VoiceSessionDeps } from "./voiceSession.ts";
import type { DeviceRef } from "./resolver.ts";
import type { LadderState, DeviceFacts } from "./capabilityLadder.ts";
import type { DispatchResult } from "./adapter.ts";

const DEVS: DeviceRef[] = [
  { deviceId: "mac1", name: "MacBook", alias: "mac", platform: "macos", online: true, isPrimary: true },
  { deviceId: "hetz1", name: "Hetzner box", alias: "hetzner", platform: "linux", online: true },
];

const connected = (over: Partial<DeviceFacts> = {}): DeviceFacts => ({
  deviceId: "mac1",
  lifecycle: "connected",
  connected: true,
  runners: {},
  projects: [],
  ...over,
});

function makeDeps(over: Partial<VoiceSessionDeps> = {}) {
  const spoken: string[] = [];
  const dispatched: { actionId: string; confirmed: boolean }[] = [];
  const deps: VoiceSessionDeps = {
    devices: () => DEVS,
    ladderState: (): LadderState => ({
      online: true,
      hasAnyDevice: true,
      localTier: "router",
      device: connected(),
    }),
    dispatch: async (actionId, _opts, confirmed): Promise<DispatchResult> => {
      dispatched.push({ actionId, confirmed });
      return { ok: true, ran: true };
    },
    speak: (t) => { spoken.push(t); },
    complete: null,
    ...over,
  };
  return { deps, spoken, dispatched };
}

// ── scripted mode (no model): ladder narration ──────────────────────
test("no goal → speaks the menu, runs nothing", async () => {
  const { deps, dispatched } = makeDeps();
  const s = createVoiceSession(deps);
  const r = await s.handle("hey there");
  assert.equal(r.dispatched, undefined);
  assert.equal(dispatched.length, 0);
  assert.match(r.spoken, /you can|what would you like/i);
});

test("goal connect when reachable-not-connected → dispatches device.select (auto)", async () => {
  const { deps, dispatched } = makeDeps({
    ladderState: () => ({
      online: true, hasAnyDevice: true, localTier: "router",
      device: connected({ lifecycle: "ready-to-connect", connected: false }),
    }),
  });
  const s = createVoiceSession(deps);
  const r = await s.handle("connect to my mac");
  assert.deepEqual(dispatched, [{ actionId: "device.select", confirmed: false }]);
  assert.equal(r.dispatched?.actionId, "device.select");
});

test("goal code on blank connected box → narrates install-runner (auto, no confirm)", async () => {
  const { deps, dispatched } = makeDeps();
  const s = createVoiceSession(deps);
  const r = await s.handle("build me a login screen");
  // runner.install is CONFIRM-tier → should be held, not auto-dispatched.
  assert.equal(r.awaiting, "confirm");
  assert.equal(dispatched.length, 0);
});

// ── CONFIRM gating across turns ─────────────────────────────────────
test("CONFIRM action is held, then runs on 'yes'", async () => {
  const { deps, dispatched } = makeDeps({
    // code goal where the first gap is a CONFIRM action (runner.install).
  });
  const s = createVoiceSession(deps);
  const held = await s.handle("write some code");
  assert.equal(held.awaiting, "confirm");
  assert.equal(s.isAwaitingConfirm(), true);
  assert.equal(dispatched.length, 0);

  const done = await s.handle("yes go ahead");
  assert.equal(s.isAwaitingConfirm(), false);
  assert.equal(dispatched.length, 1);
  assert.equal(dispatched[0].confirmed, true);
  assert.equal(done.dispatched?.ok, true);
});

test("CONFIRM action cancelled on 'no'", async () => {
  const { deps, dispatched } = makeDeps();
  const s = createVoiceSession(deps);
  await s.handle("write some code");
  const r = await s.handle("no never mind");
  assert.equal(s.isAwaitingConfirm(), false);
  assert.equal(dispatched.length, 0);
  assert.match(r.spoken, /cancel/i);
});

test("ambiguous yes/no while pending → re-asks", async () => {
  const { deps } = makeDeps();
  const s = createVoiceSession(deps);
  await s.handle("write some code");
  const r = await s.handle("hmm I'm not sure");
  assert.equal(r.awaiting, "confirm");
  assert.equal(s.isAwaitingConfirm(), true);
});

// ── device ambiguity ────────────────────────────────────────────────
test("remote goal with no named device + unresolved target → asks which machine", async () => {
  const { deps } = makeDeps({
    ladderState: () => ({ online: true, hasAnyDevice: true, localTier: "router", device: undefined }),
  });
  const s = createVoiceSession(deps);
  const r = await s.handle("deploy it"); // no device named
  assert.equal(r.awaiting, "device");
  assert.match(r.spoken, /which machine/i);
});

// ── COMMAND path via a fake model ───────────────────────────────────
test("model proposes a direct action (runner.switch) → CONFIRM-gated", async () => {
  const { deps } = makeDeps({
    complete: async () => ({ text: '{"action":"runner.switch","deviceRef":"mac"}' }),
  });
  const s = createVoiceSession(deps);
  // "switch the agent" has no goal keyword → falls to the model command path.
  const r = await s.handle("change the coding agent on my mac");
  // runner.switch is SAFE_WRITE → auto-dispatched (no confirm).
  assert.equal(r.dispatched?.actionId, "runner.switch");
});

test("model proposing a BLOCKED action is refused, never dispatched", async () => {
  const { deps, dispatched } = makeDeps({
    complete: async () => ({ text: '{"action":"destroy","deviceRef":"hetzner"}' }),
  });
  const s = createVoiceSession(deps);
  const r = await s.handle("nuke the hetzner box completely");
  assert.equal(dispatched.length, 0);
  assert.match(r.spoken, /can't do that by voice/i);
});

test("model returning garbage → falls through to the ladder, no crash", async () => {
  const { deps } = makeDeps({
    complete: async () => ({ text: "uhh I think maybe" }),
  });
  const s = createVoiceSession(deps);
  const r = await s.handle("do the thing"); // no goal, model junk → ladder menu
  assert.ok(r.spoken.length > 0);
});

// ── empty input ─────────────────────────────────────────────────────
test("empty transcript is handled gracefully", async () => {
  const { deps } = makeDeps();
  const s = createVoiceSession(deps);
  const r = await s.handle("   ");
  assert.match(r.spoken, /didn't catch/i);
});

// ── prompt builder ──────────────────────────────────────────────────
test("buildVoicePrompt lists devices + a non-BLOCKED action, excludes destroy", () => {
  const p = buildVoicePrompt("switch to codex", ["mac", "hetzner"]);
  assert.match(p, /mac, hetzner/);
  assert.match(p, /runner\.switch/);
  assert.ok(!/\bdestroy —/.test(p)); // BLOCKED actions aren't offered
});
