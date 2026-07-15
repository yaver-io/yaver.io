// Closed-loop wake ⇄ sleep test. Drives the REAL wake-state engine
// (deriveServerPhase — the shared driver behind the connected-device banner AND,
// via the same PHASE_META percents, the honest progress everywhere) through a
// full parked → waking → finishing → ready → parking → asleep cycle, asserting
// the invariants the wake-UX fixes depend on:
//   • progress is monotonic within a run (the bar never jumps backward),
//   • the run NEVER reports "ready"/100% before the box is genuinely usable
//     (reachable AND runnersAuthorized) — the "bar's already full but it isn't
//     actually" bug: an agent whose HTTP answers (status "active") but whose
//     runners aren't authorized yet stays at "online" (86%), not "ready" (100%),
//   • the park ladder is visible (snapshotting → powering-down → asleep).
// Headless: pure functions, no infra, no token, no cost.
//
// deriveWakeView (the picker's managed-cloud status) applies the identical
// runnersAuthorized gate and is type-checked; a dedicated test needs a pure-core
// split of parkedMachines.ts (it imports react) — tracked as a follow-up.

import test from "node:test";
import assert from "node:assert/strict";

import {
  deriveServerPhase,
  PHASE_META,
  PARK_STEPS,
  WAKE_STEPS,
} from "./wakeMachineCore.ts";

// A managed box coming up from a snapshot. deviceReachable flips true only once
// the agent's HTTP answers (status "active"); runnersAuthorized flips true last.
const WAKE: Array<{ status: string; provisionPhase: string | null; runnersAuthorized: boolean; reachable: boolean; usable: boolean }> = [
  { status: "stopped",      provisionPhase: null,                  runnersAuthorized: false, reachable: false, usable: false },
  { status: "provisioning", provisionPhase: "creating",           runnersAuthorized: false, reachable: false, usable: false },
  { status: "provisioning", provisionPhase: "booting",            runnersAuthorized: false, reachable: false, usable: false },
  { status: "provisioning", provisionPhase: "registering",        runnersAuthorized: false, reachable: false, usable: false },
  { status: "active",       provisionPhase: "authorizing-runners", runnersAuthorized: false, reachable: true,  usable: false }, // agent up, NOT usable
  { status: "active",       provisionPhase: "ready",              runnersAuthorized: true,  reachable: true,  usable: true },  // truly ready
];

test("closed-loop WAKE: progress is monotonic and never 'ready' before the box is usable", () => {
  let prevPct = -1;
  let reachedReady = false;
  for (const s of WAKE) {
    const phase = deriveServerPhase(
      { status: s.status, provisionPhase: s.provisionPhase, runnersAuthorized: s.runnersAuthorized },
      s.reachable,
    );
    const pct = PHASE_META[phase].percent;
    assert.ok(pct >= prevPct, `progress regressed at ${s.status}/${s.provisionPhase}: ${pct} < ${prevPct}`);
    prevPct = pct;

    if (!s.usable) {
      assert.notEqual(phase, "ready", `prematurely 'ready' at ${s.status}/${s.provisionPhase}`);
      assert.ok(pct < 100, `prematurely 100% at ${s.status}/${s.provisionPhase} (${pct})`);
    } else {
      assert.equal(phase, "ready");
      assert.equal(pct, 100);
      reachedReady = true;
    }
  }
  assert.ok(reachedReady, "wake never reached a usable/ready state");
  assert.deepEqual(WAKE_STEPS, ["resuming", "booting", "registering", "online", "ready"]);
});

test("closed-loop WAKE: agent-up-but-authorizing is 'online' (86%), not 'ready' (100%)", () => {
  const authorizing = deriveServerPhase({ status: "active", provisionPhase: "authorizing-runners", runnersAuthorized: false }, true);
  assert.equal(authorizing, "online");
  assert.equal(PHASE_META.online.percent, 86);
  const ready = deriveServerPhase({ status: "active", provisionPhase: "ready", runnersAuthorized: true }, true);
  assert.equal(ready, "ready");
  assert.equal(PHASE_META.ready.percent, 100);
});

test("closed-loop WAKE: a 'ready' provisionPhase but not-yet-reachable is NOT a false 100%", () => {
  // The "created record, still cold" window — backend says ready, box isn't up.
  const phase = deriveServerPhase({ status: "active", provisionPhase: "ready", runnersAuthorized: true }, false);
  assert.equal(phase, "registering");
  assert.ok(PHASE_META[phase].percent < 100);
});

test("closed-loop SLEEP: park walks snapshotting → powering-down → asleep, visibly", () => {
  assert.equal(deriveServerPhase({ status: "stopping", provisionPhase: "snapshotting", runnersAuthorized: true }, false), "snapshotting");
  assert.equal(deriveServerPhase({ status: "stopping", provisionPhase: "deleting", runnersAuthorized: true }, false), "powering-down");
  assert.equal(deriveServerPhase({ status: "stopped", provisionPhase: null, runnersAuthorized: false }, false), "asleep");
  assert.ok(PHASE_META["powering-down"].percent > PHASE_META.snapshotting.percent, "park progress must advance");
  assert.deepEqual(PARK_STEPS, ["snapshotting", "powering-down", "parked"]);
});

test("closed-loop SLEEP: park progress is monotonic", () => {
  const PARK: Array<{ status: string; provisionPhase: string }> = [
    { status: "active",   provisionPhase: "ready" },
    { status: "stopping", provisionPhase: "snapshotting" },
    { status: "stopping", provisionPhase: "deleting" },
    { status: "stopped",  provisionPhase: "" },
  ];
  // Ready (100) → snapshotting (35) → powering-down (78) → asleep (0). The bar
  // is a fresh run when parking, so we assert the PARK_STEPS ordering advances,
  // not that it continues from 100.
  const snap = PHASE_META[deriveServerPhase(PARK[1], false)].percent;
  const down = PHASE_META[deriveServerPhase(PARK[2], false)].percent;
  assert.ok(down > snap, `powering-down (${down}) must be past snapshotting (${snap})`);
});
