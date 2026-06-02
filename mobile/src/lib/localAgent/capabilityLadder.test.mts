// capabilityLadder.test.mts — unit tests for the "what can I / should I do
// now?" walk. Run: npx tsx src/lib/localAgent/capabilityLadder.test.mts
// Pure logic, no RN — imports the .ts sources directly via tsx.

import test from "node:test";
import assert from "node:assert/strict";

import {
  capabilityLadder,
  spineReached,
  surfaceMenu,
  type LadderState,
  type DeviceFacts,
  type Goal,
} from "./capabilityLadder.ts";
import { dispositionFor } from "./catalog.ts";

// ── fixtures ────────────────────────────────────────────────────────
const connectedBlankBox: DeviceFacts = {
  deviceId: "box1",
  lifecycle: "connected",
  connected: true,
  runners: {},
  projects: [],
};

function state(over: Partial<LadderState> = {}): LadderState {
  return {
    online: true,
    hasAnyDevice: true,
    localTier: "router",
    ...over,
  };
}

function dev(over: Partial<DeviceFacts> = {}): DeviceFacts {
  return { ...connectedBlankBox, ...over };
}

const cap = (r: ReturnType<typeof capabilityLadder>, id: string) =>
  r.available.find((c) => c.id === id);

// ── spine: reached ──────────────────────────────────────────────────
test("spine: offline", () => {
  assert.equal(spineReached(state({ online: false })), "offline");
});

test("spine: no devices", () => {
  assert.equal(spineReached(state({ hasAnyDevice: false })), "no-device");
});

test("spine: device offline → unreachable", () => {
  assert.equal(spineReached(state({ device: dev({ lifecycle: "offline", connected: false }) })), "unreachable");
});

test("spine: auth expired / bootstrap → agent-unauthed", () => {
  assert.equal(spineReached(state({ device: dev({ lifecycle: "yaver-auth-expired", connected: false }) })), "agent-unauthed");
  assert.equal(spineReached(state({ device: dev({ lifecycle: "bootstrap", connected: false }) })), "agent-unauthed");
});

test("spine: reachable but not connected", () => {
  assert.equal(spineReached(state({ device: dev({ lifecycle: "ready-to-connect", connected: false }) })), "reachable");
});

test("spine: connected", () => {
  assert.equal(spineReached(state({ device: connectedBlankBox })), "connected");
});

// ── lazy / no-goal: nothing is introduced ───────────────────────────
test("no goal → nextStep is null (ask, don't wizard)", () => {
  const r = capabilityLadder(state({ device: connectedBlankBox }));
  assert.equal(r.nextStep, null);
});

test("connected blank box: menu is invitations, not prerequisites", () => {
  const r = capabilityLadder(state({ device: connectedBlankBox }));
  // ask is always ready; install-runner + start-project are invitations.
  assert.equal(cap(r, "ask")?.ready, true);
  assert.equal(cap(r, "install-runner")?.ready, false);
  // No coding actions surfaced (no runner) — not nagging about build/test.
  assert.equal(cap(r, "edit"), undefined);
});

// ── goal-pulled: connect ────────────────────────────────────────────
test("goal connect: gap is device.select when reachable-not-connected", () => {
  const r = capabilityLadder(
    state({ device: dev({ lifecycle: "ready-to-connect", connected: false }) }),
    { kind: "connect" },
  );
  assert.equal(r.nextStep?.action, "device.select");
});

test("goal connect: satisfied (null) once connected", () => {
  const r = capabilityLadder(state({ device: connectedBlankBox }), { kind: "connect" });
  assert.equal(r.nextStep, null);
});

// ── goal-pulled: code (existing repo) ───────────────────────────────
test("goal code: first gap is install a runner on a blank connected box", () => {
  const r = capabilityLadder(state({ device: connectedBlankBox }), { kind: "code" });
  assert.equal(r.nextStep?.action, "runner.install");
  assert.equal(r.nextStep?.provisions, "runner");
});

test("goal code: runner ready, no project, existing-repo wanted, git unauthed → connect git to clone", () => {
  const d = dev({ runners: { claude: { installed: true, authed: true } }, gitAuthed: false });
  const r = capabilityLadder(state({ device: d }), { kind: "code" });
  assert.equal(r.nextStep?.action, "git.connect");
  assert.equal(r.nextStep?.rung, "project-present");
});

test("goal code (fresh): runner ready, no project → scaffold fresh, no git nag", () => {
  const d = dev({ runners: { claude: { installed: true, authed: true } } });
  const r = capabilityLadder(state({ device: d }), { kind: "code", fresh: true });
  assert.equal(r.nextStep?.action, "project.new");
});

test("goal code: runner + multiple projects, none selected → ask which", () => {
  const d = dev({
    runners: { claude: { installed: true, authed: true } },
    projects: [{ slug: "api" }, { slug: "web" }],
  });
  const r = capabilityLadder(state({ device: d }), { kind: "code" });
  assert.equal(r.nextStep?.action, "project.select");
  assert.match(r.nextStep?.say ?? "", /api/);
});

test("goal code: fully ready → no gap", () => {
  const d = dev({
    runners: { claude: { installed: true, authed: true } },
    projects: [{ slug: "api" }],
    activeProjectSlug: "api",
  });
  const r = capabilityLadder(state({ device: d }), { kind: "code" });
  assert.equal(r.nextStep, null);
});

// ── fork independence ───────────────────────────────────────────────
test("fork independence: git-unauthed but runner+project ready → edit ready, push is an invite (not a blocker)", () => {
  const d = dev({
    runners: { claude: { installed: true, authed: true } },
    projects: [{ slug: "api" }],
    activeProjectSlug: "api",
    gitAuthed: false,
  });
  const r = capabilityLadder(state({ device: d }), { kind: "code" });
  assert.equal(r.nextStep, null); // coding doesn't need git
  assert.equal(cap(r, "edit")?.ready, true);
  assert.equal(cap(r, "push")?.ready, false); // invite only
});

test("fork independence: hermes-unready does not block a coding goal", () => {
  const d = dev({
    runners: { claude: { installed: true, authed: true } },
    projects: [{ slug: "api" }],
    activeProjectSlug: "api",
    hermesReady: false,
  });
  const r = capabilityLadder(state({ device: d }), { kind: "code" });
  assert.equal(r.nextStep, null);
});

// ── goal-pulled: push pulls in the git plane ────────────────────────
test("goal push: runner+project ready, git unauthed → connect git", () => {
  const d = dev({
    runners: { claude: { installed: true, authed: true } },
    projects: [{ slug: "api" }],
    activeProjectSlug: "api",
    gitAuthed: false,
  });
  const r = capabilityLadder(state({ device: d }), { kind: "push" });
  assert.equal(r.nextStep?.action, "git.connect");
  assert.equal(r.nextStep?.rung, "git-authed");
});

// ── goal-pulled: preview is the Hermes fork (no runner needed) ──────
test("goal preview: needs Hermes stack, not a runner", () => {
  const d = dev({ runners: {}, hermesReady: false });
  const r = capabilityLadder(state({ device: d }), { kind: "preview" });
  assert.equal(r.nextStep?.rung, "hermes-stack");
  assert.equal(r.nextStep?.provisions, "hermes");
});

test("goal preview: hermes ready but no project → start one", () => {
  const d = dev({ hermesReady: true, projects: [] });
  const r = capabilityLadder(state({ device: d }), { kind: "preview" });
  assert.equal(r.nextStep?.rung, "dev-project");
});

// ── on-device branch (no spine) ─────────────────────────────────────
test("sandbox: coder tier + no device → ready, no machine needed", () => {
  const r = capabilityLadder(state({ hasAnyDevice: false, localTier: "coder", device: undefined }), { kind: "sandbox" });
  assert.equal(r.nextStep, null);
  assert.equal(cap(r, "sandbox")?.ready, true);
});

test("sandbox: router tier → can't run coder, suggest pairing", () => {
  const r = capabilityLadder(state({ localTier: "router" }), { kind: "sandbox" });
  assert.equal(r.nextStep?.rung, "coder-tier");
  // router phone never offers a sandbox capability in the menu
  assert.equal(cap(r, "sandbox"), undefined);
});

// ── guards ──────────────────────────────────────────────────────────
test("guard: a remote (via:ops) gap never carries a dispatchable action pre-connect", () => {
  // Unreachable box, code goal → spine gap fires first; if any ops action
  // leaked it'd be stripped. Spine gaps use context/shellHint, so action (if
  // present) must be a context action, never ops.
  const r = capabilityLadder(
    state({ device: dev({ lifecycle: "offline", connected: false }) }),
    { kind: "code" },
  );
  if (r.nextStep?.action) {
    assert.notEqual(dispositionFor(r.nextStep.action), "blocked");
  }
});

test("guard: every emitted nextStep.action is auto/confirm-dispatchable (never blocked)", () => {
  const goals: Goal[] = [
    { kind: "connect" }, { kind: "code" }, { kind: "code", fresh: true },
    { kind: "push" }, { kind: "deploy" }, { kind: "preview" },
  ];
  for (const g of goals) {
    const r = capabilityLadder(state({ device: connectedBlankBox }), g);
    if (r.nextStep?.action) {
      const disp = dispositionFor(r.nextStep.action);
      assert.ok(disp === "auto" || disp === "confirm", `${g.kind}: ${r.nextStep.action} → ${disp}`);
    }
  }
});

test("blocked: remote goal with no resolved device → blocked, don't guess", () => {
  const r = capabilityLadder(state({ hasAnyDevice: true, device: undefined }), { kind: "code" });
  assert.equal(r.blocked?.reason, "device-unresolved");
});

// ── idempotency ─────────────────────────────────────────────────────
test("idempotent: re-running with a later goal accretes (push gap appears only once git is the gap)", () => {
  const base = dev({
    runners: { claude: { installed: true, authed: true } },
    projects: [{ slug: "api" }],
    activeProjectSlug: "api",
    gitAuthed: false,
  });
  // code goal: satisfied. push goal on the SAME state: git gap. No setup reset.
  assert.equal(capabilityLadder(state({ device: base }), { kind: "code" }).nextStep, null);
  assert.equal(capabilityLadder(state({ device: base }), { kind: "push" }).nextStep?.action, "git.connect");
});

// ── menu monotonicity ───────────────────────────────────────────────
test("menu: offline still offers something (never a dead end)", () => {
  const r = capabilityLadder(state({ online: false }));
  assert.ok(r.available.length >= 1);
  assert.equal(cap(r, "ask")?.ready, true);
});
