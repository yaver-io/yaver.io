// Run: node --experimental-strip-types --test convex/wakeOnRequestPolicy.test.mts
// (from backend/). Wired into scripts/test-suite.sh.

import test from "node:test";
import assert from "node:assert/strict";

import {
  classifyWakeTarget,
  WAKE_IN_FLIGHT_WINDOW_MS,
  WAKE_RETRY_AFTER_SECONDS,
} from "./wakeOnRequestPolicy.ts";

const NOW = 1_700_000_000_000;

function base(over: Partial<Parameters<typeof classifyWakeTarget>[0]> = {}) {
  return classifyWakeTarget({
    hasTunnel: false,
    known: true,
    wakeable: true,
    callerMayWake: true,
    now: NOW,
    ...over,
  });
}

test("a live tunnel is none of this policy's business", () => {
  assert.equal(base({ hasTunnel: true }).verdict, "connected");
});

test("an unmanaged target keeps the existing 502 behavior", () => {
  const d = base({ known: false });
  assert.equal(d.verdict, "unknown");
  assert.equal(d.retryable, false);
});

test("a permitted caller wakes a parked, recreatable box", () => {
  const d = base();
  assert.equal(d.verdict, "wake");
  assert.equal(d.retryable, true);
  assert.equal(d.retryAfterSeconds, WAKE_RETRY_AFTER_SECONDS);
});

test("permission, not ownership, is what opens the wake", () => {
  // The whole point of the product: an app's USERS wake the box, because the
  // owner granted it. A rule keyed on ownership would make scale-to-zero mean
  // "offline" for everyone except the developer.
  assert.equal(base({ callerMayWake: true }).verdict, "wake");
  assert.equal(base({ callerMayWake: false }).verdict, "parked-no-permission");
});

test("an unpermitted caller is refused without spending the owner's money", () => {
  const d = base({ callerMayWake: false });
  assert.equal(d.verdict, "parked-no-permission");
  assert.equal(d.retryable, false, "retrying must not be suggested — it will never succeed");
  assert.match(d.message, /permission/i);
});

test("permission is checked before wakeability so refusals do not leak box state", () => {
  // Same refusal whether or not the box has a snapshot: an unpermitted caller
  // must not be able to probe someone else's backup posture.
  const noSnapshot = base({ callerMayWake: false, wakeable: false });
  const withSnapshot = base({ callerMayWake: false, wakeable: true });
  assert.equal(noSnapshot.verdict, withSnapshot.verdict);
  assert.equal(noSnapshot.message, withSnapshot.message);
});

test("a box with nothing to restore from is terminal, not retryable", () => {
  const d = base({ wakeable: false });
  assert.equal(d.verdict, "parked-not-wakeable");
  assert.equal(d.retryable, false);
  assert.equal(d.retryAfterSeconds, 0);
});

test("a wake already in flight does not start a second one", () => {
  const d = base({ lastWokeAt: NOW - 5_000 });
  assert.equal(d.verdict, "waking");
  assert.equal(d.retryable, true);
});

test("a stale wake stamp allows a fresh attempt", () => {
  const d = base({ lastWokeAt: NOW - WAKE_IN_FLIGHT_WINDOW_MS - 1 });
  assert.equal(d.verdict, "wake");
});

test("an in-flight wake outranks a spent budget", () => {
  // The spend already happened. Telling a user "budget spent" about a box that
  // is at that moment coming up for them would be both wrong and unhelpful.
  const d = base({ lastWokeAt: NOW - 1_000, wakeBudgetRemaining: 0 });
  assert.equal(d.verdict, "waking");
});

test("the owner's budget cap stops further wakes", () => {
  const d = base({ wakeBudgetRemaining: 0 });
  assert.equal(d.verdict, "parked-budget-spent");
  assert.equal(d.retryable, false);
});

test("no cap set means no cap enforced", () => {
  assert.equal(base({ wakeBudgetRemaining: undefined }).verdict, "wake");
  assert.equal(base({ wakeBudgetRemaining: 3 }).verdict, "wake");
});

test("every non-connected verdict carries an actionable message", () => {
  // The bug being fixed is a flat "502 device not connected to relay" that told
  // a small app's user nothing about whether to wait, reload, or call the dev.
  for (const d of [
    base({ known: false }),
    base({ callerMayWake: false }),
    base({ wakeable: false }),
    base({ lastWokeAt: NOW - 1_000 }),
    base({ wakeBudgetRemaining: 0 }),
    base(),
  ]) {
    assert.ok(d.message.length > 0, `verdict ${d.verdict} must explain itself`);
  }
});
