/**
 * Device truth — the claims a surface is allowed to make.
 *
 * These are the regression tests for docs/architecture/DEVICE_TRUTH.md. The
 * fixtures are real device shapes we have shipped bugs on; `magara` in
 * particular (fresh heartbeat, every transport dead) is the one that survived
 * three fix attempts.
 *
 * Run: npx tsx --test web/lib/device-lifecycle.test.ts
 */

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  BROWSER_FAILURE_WINDOW_MS,
  deriveBrowserReach,
  deriveDeviceLifecycleState,
  deviceStatusLabel,
  deviceCtaLabel,
  canBrowserActOnDevice,
} from "./device-lifecycle";
import { MAX_DELAY_MS } from "./probe-backoff";

const NOW = 1_800_000_000_000;

// Minimal shape; the derivations only read these fields.
function device(over: Record<string, unknown> = {}): any {
  return {
    online: false,
    needsAuth: false,
    peerState: undefined,
    workspaceLive: false,
    probeState: undefined,
    probeError: undefined,
    lastTunnelEvent: undefined,
    probeInfo: undefined,
    ...over,
  };
}

function failure(at: number, label = "Unauthorized", reason = "unauthorized") {
  return { reason, label, detail: "The agent refused our token.", at };
}

// --- The constant coupling that F3 was ------------------------------------

test("failure evidence outlives the probe backoff cap", () => {
  // If evidence expires before we re-probe, a failing device flaps back to
  // looking healthy. This is the exact arithmetic that broke magara: a 90s
  // window against a 120s cap.
  assert.ok(
    BROWSER_FAILURE_WINDOW_MS > MAX_DELAY_MS,
    `evidence window (${BROWSER_FAILURE_WINDOW_MS}ms) must exceed max backoff (${MAX_DELAY_MS}ms)`,
  );
});

test("a device failing at max backoff never flaps back to reachable", () => {
  const d = device({ online: true });
  // Worst case: we only get to re-probe once every MAX_DELAY_MS.
  const reach = deriveBrowserReach(d, failure(NOW - MAX_DELAY_MS), NOW);
  assert.equal(reach.state, "unreachable");
  assert.equal(canBrowserActOnDevice(deriveDeviceLifecycleState(d), reach), false);
});

// --- claimed vs reachable (F2) --------------------------------------------

test("an unprobed heartbeating device is claimed, not reachable", () => {
  const d = device({ online: true });
  const reach = deriveBrowserReach(d, null, NOW);
  assert.equal(reach.state, "claimed");
  assert.equal(reach.verified, false);
  assert.equal(reach.unreachable, false);
});

test("claimed devices do not get a confident CTA", () => {
  const d = device({ online: true });
  const lc = deriveDeviceLifecycleState(d);
  const cta = deviceCtaLabel(lc, deriveBrowserReach(d, null, NOW));
  assert.equal(cta.label, "Connect");
  assert.equal(cta.confident, false, "unverified device must not get the primary style");
});

test("claimed devices do not claim readiness in the status line", () => {
  const d = device({ online: true });
  const lc = deriveDeviceLifecycleState(d);
  assert.equal(deviceStatusLabel(lc, deriveBrowserReach(d, null, NOW)), "Reporting in · not verified");
});

test("a successful probe earns 'reachable' and the confident CTA", () => {
  const d = device({ online: true, probeState: "ok" });
  const lc = deriveDeviceLifecycleState(d);
  const reach = deriveBrowserReach(d, null, NOW);
  assert.equal(reach.state, "reachable");
  assert.equal(reach.verified, true);
  assert.equal(deviceStatusLabel(lc, reach), "Ready to Connect");
  assert.deepEqual(
    { label: deviceCtaLabel(lc, reach).label, confident: deviceCtaLabel(lc, reach).confident },
    { label: "Open Workspace", confident: true },
  );
});

test("a live workspace counts as proof", () => {
  const d = device({ online: true, workspaceLive: true });
  assert.equal(deriveBrowserReach(d, null, NOW).state, "reachable");
});

// --- magara: the case this whole model exists for -------------------------

test("magara — fresh heartbeat, probe 401 — never claims ready", () => {
  const d = device({ online: true });
  const lc = deriveDeviceLifecycleState(d);
  const reach = deriveBrowserReach(d, failure(NOW - 5_000), NOW);

  assert.equal(lc, "ready-to-connect", "the agent IS alive; lifecycle should say so");
  assert.equal(reach.state, "unreachable", "but we proved we can't get there");
  assert.equal(deviceStatusLabel(lc, reach), "Alive · can't reach (Unauthorized)");
  assert.equal(deviceCtaLabel(lc, reach).label, "Try Connect");
  assert.equal(canBrowserActOnDevice(lc, reach), false);
});

test("evidence older than the window decays back to claimed, not reachable", () => {
  const d = device({ online: true });
  const reach = deriveBrowserReach(d, failure(NOW - BROWSER_FAILURE_WINDOW_MS - 1), NOW);
  assert.equal(reach.state, "claimed", "stale evidence must not promote to verified");
  assert.equal(reach.verified, false);
});

// --- lifecycle ------------------------------------------------------------

test("a stale peerState is not readiness", () => {
  // "the bus saw this machine but no transport is healthy" — the opposite of ready.
  assert.equal(deriveDeviceLifecycleState(device({ peerState: "stale" })), "offline");
});

test("offline devices are offline everywhere", () => {
  const d = device();
  const lc = deriveDeviceLifecycleState(d);
  const reach = deriveBrowserReach(d, null, NOW);
  assert.equal(lc, "offline");
  assert.equal(reach.state, "offline");
  assert.equal(deviceStatusLabel(lc, reach), "Offline");
  assert.equal(deviceCtaLabel(lc, reach).confident, false);
  assert.equal(canBrowserActOnDevice(lc, reach), false);
});

test("needsAuth outranks a fresh heartbeat", () => {
  const d = device({ online: true, needsAuth: true });
  const lc = deriveDeviceLifecycleState(d);
  assert.equal(lc, "bootstrap");
  assert.equal(deviceStatusLabel(lc, deriveBrowserReach(d, null, NOW)), "Needs pairing");
  assert.equal(deviceCtaLabel(lc, deriveBrowserReach(d, null, NOW)).confident, false);
});

test("no surface offers a confident CTA without proof", () => {
  // The invariant, stated once: confident ⇒ verified.
  const cases = [
    device({ online: true }),
    device({ online: true, needsAuth: true }),
    device({ peerState: "stale" }),
    device(),
    device({ online: true, probeState: "unreachable" }),
    device({ online: true, probeState: "ok" }),
    device({ workspaceLive: true }),
  ];
  for (const d of cases) {
    const lc = deriveDeviceLifecycleState(d);
    const reach = deriveBrowserReach(d, null, NOW);
    const cta = deviceCtaLabel(lc, reach);
    if (cta.confident) {
      assert.equal(reach.verified, true, `confident CTA without proof for ${JSON.stringify(d)}`);
    }
  }
});
