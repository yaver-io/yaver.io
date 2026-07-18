import test from "node:test";
import assert from "node:assert/strict";

import { deriveWakeView } from "./wakeMachineCore";

// managed-cloud-wake-broken.md §7 tabulated three ladders giving three different
// answers for the same machine at the same instant, and named the picker's the
// LEAST correct. deriveWakeView now derives from wakeMachineCore instead of
// deciding for itself; these pin each row of that table so the two cannot drift
// apart again.
const m = (over: Record<string, unknown> = {}) => ({ id: "m1", ...over }) as any;

test("an active box nobody can reach is not 100% online", () => {
  // The headline row. This used to return stage 3 / 100% / "Online" without
  // ever being told whether the box answers — the "bar's already full but it
  // isn't" complaint, straight from the user.
  const unreachable = deriveWakeView(m({ status: "active", provisionPhase: "ready" }), false, false);
  assert.notEqual(unreachable.percent, 100, "a box we cannot reach must not read as 100%");
  assert.notEqual(unreachable.tone, "online");

  // Same machine, reachable: now it may finish.
  const reachable = deriveWakeView(m({ status: "active", provisionPhase: "ready" }), false, true);
  assert.equal(reachable.percent, 100);
  assert.equal(reachable.tone, "online");
});

test("awaiting-yaver-auth is a terminal fact, not a wake failure", () => {
  // Previously: tone error, 0%, inFlight false — as if the wake had failed. The
  // box is UP; it needs a human. 65% is how far it honestly got.
  const v = deriveWakeView(m({ status: "resuming", provisionPhase: "awaiting-yaver-auth" }), false, false);
  assert.equal(v.percent, 65, "needs-auth sits at the percent it actually reached");
  assert.equal(v.inFlight, false, "nothing will change until a human acts — do not creep a bar");
  assert.ok(v.error, "the user needs to be told what to do");
});

test("the snapshot-restore long pole is not 'Creating server' at 10%", () => {
  // These three phases had NO case, so the longest part of a wake displayed as
  // stage 0 / 10% — the bar looked stuck at the exact moment most work happens.
  for (const provisionPhase of ["restoring-snapshot", "checking-snapshot", "preparing-volume"]) {
    const v = deriveWakeView(m({ status: "resuming", provisionPhase }), false, false);
    assert.ok(v.percent > 10, `${provisionPhase} reported ${v.percent}% — still the stage-0 default`);
    assert.equal(v.inFlight, true, `${provisionPhase} is a wake in progress`);
  }
});

test("a real provisioning error still reads as an error", () => {
  const v = deriveWakeView(m({ status: "error", errorMessage: "boom" }), false, false);
  assert.equal(v.tone, "error");
  assert.equal(v.inFlight, false);
  assert.ok(v.error);
});

test("a parked box stays parked until it is actually woken", () => {
  const parked = deriveWakeView(m({ status: "paused" }), false, false);
  assert.equal(parked.tone, "parked");
  assert.equal(parked.inFlight, false);

  // Optimistic tap: the user pressed Wake, so show motion before the server
  // catches up — otherwise the button appears to do nothing.
  const tapped = deriveWakeView(m({ status: "paused" }), true, false);
  assert.equal(tapped.inFlight, true);
});

test("runners still authorizing reads as finishing up, not as done", () => {
  const v = deriveWakeView(m({ status: "active", provisionPhase: "ready", runnersAuthorized: false }), false, true);
  assert.notEqual(v.percent, 100, "runner auth is still landing — 100% would be a lie");
  assert.equal(v.inFlight, true);
});
