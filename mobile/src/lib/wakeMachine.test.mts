import test from "node:test";
import assert from "node:assert/strict";

import {
  creepPercent,
  describeRest,
  deriveServerPhase,
  expectedWakeMs,
  formatDuration,
  isDeviceAsleep,
  isPhaseInFlight,
  isPhaseSettled,
  isPhaseStalled,
  PHASE_META,
  phaseTypicalMs,
  stallHint,
  wakeKindFor,
  wakeStepsFor,
  type LifecyclePhase,
} from "./wakeMachineCore.ts";

function phase(
  status: string,
  provisionPhase: string | null,
  deviceReachable: boolean,
  runnersAuthorized?: boolean,
): LifecyclePhase {
  return deriveServerPhase(
    { status, provisionPhase, runnersAuthorized },
    deviceReachable,
  );
}

test("deriveServerPhase keeps active-but-unreachable machines at registering, not ready", () => {
  assert.equal(phase("active", "ready", false), "registering");
  assert.equal(phase("active", "authorizing-runners", false, false), "registering");
  assert.equal(PHASE_META.registering.percent, 65);
});

test("deriveServerPhase only reports ready when the device is reachable and runners are authorized", () => {
  assert.equal(phase("active", "ready", true), "ready");
  assert.equal(phase("active", "authorizing-runners", true, false), "online");
});

test("deriveServerPhase maps resume and stop lifecycle signals to visible progress", () => {
  assert.equal(phase("resuming", "ready", false), "registering");
  assert.equal(phase("resuming", "booting", false), "booting");
  assert.equal(phase("provisioning", "pulling-image", false), "booting");
  assert.equal(phase("stopping", "deleting", false), "powering-down");
  assert.equal(phase("stopped", "ready", false), "asleep");
});

// ── LAN wake (Wake-on-LAN) ────────────────────────────────────────────────
// A physical box asleep on a LAN is a different animal from a managed cloud
// box: no snapshot, no control-plane record, and a wake that completes in
// seconds rather than minutes.

test("isDeviceAsleep: managed box parked is asleep", () => {
  assert.equal(isDeviceAsleep({ managed: true, machineStatus: "stopped" }), true);
  assert.equal(isDeviceAsleep({ managed: true, machineStatus: "active" }), false);
});

test("isDeviceAsleep: offline LAN box with a waker is asleep", () => {
  assert.equal(
    isDeviceAsleep({
      isOnline: false,
      wakeOnLan: { mac: "AA:BB:CC:DD:EE:FF", viaDeviceId: "peer-1" },
    }),
    true,
  );
});

test("isDeviceAsleep: offline box with no way to wake it is NOT asleep", () => {
  // Showing a Wake button that cannot possibly work is worse than none.
  assert.equal(isDeviceAsleep({ isOnline: false }), false);
  // Knowing the MAC is useless without a peer awake on that wire to shout it.
  assert.equal(isDeviceAsleep({ isOnline: false, wakeOnLan: { mac: "AA:BB:CC:DD:EE:FF" } }), false);
  // ...and vice versa.
  assert.equal(isDeviceAsleep({ isOnline: false, wakeOnLan: { viaDeviceId: "peer-1" } }), false);
});

test("isDeviceAsleep: an online LAN box is not asleep", () => {
  assert.equal(
    isDeviceAsleep({ isOnline: true, wakeOnLan: { mac: "AA:BB:CC:DD:EE:FF", viaDeviceId: "peer-1" } }),
    false,
  );
});

test("wakeKindFor distinguishes the two mechanisms", () => {
  assert.equal(wakeKindFor({ managed: true, machineStatus: "stopped" }), "cloud");
  assert.equal(wakeKindFor({ isOnline: false, wakeOnLan: { mac: "A", viaDeviceId: "p" } }), "lan");
  assert.equal(wakeKindFor({ isOnline: false }), null);
  assert.equal(wakeKindFor(null), null);
});

test("LAN wake skips the snapshot-restore step", () => {
  // There is no snapshot to recreate from; a "Restoring" step would strand
  // the stepper on a phase the box never enters.
  assert.ok(!wakeStepsFor("lan").includes("resuming"));
  assert.ok(wakeStepsFor("cloud").includes("resuming"));
  assert.deepEqual(wakeStepsFor("lan"), ["booting", "registering", "online", "ready"]);
});

test("LAN timings are seconds, not minutes", () => {
  // Reusing the cloud numbers would crawl the bar at 40% for eight minutes
  // while the box has actually been up the whole time.
  const lanBoot = phaseTypicalMs("booting", "lan")!;
  const cloudBoot = phaseTypicalMs("booting", "cloud")!;
  assert.ok(lanBoot < cloudBoot / 10, `lan boot ${lanBoot}ms should be far under cloud ${cloudBoot}ms`);
  assert.equal(phaseTypicalMs("resuming", "lan"), undefined);
});

test("a LAN wake stalls long before a cloud wake would", () => {
  // 60s into a boot: the Mac is clearly not coming back, but a cloud box
  // restoring a 160GB snapshot is perfectly healthy.
  assert.equal(isPhaseStalled("booting", 60_000, "lan"), true);
  assert.equal(isPhaseStalled("booting", 60_000, "cloud"), false);
});

test("stall hints explain LAN failures, not snapshot restores", () => {
  const hint = stallHint("booting", 60_000, "lan")!;
  assert.ok(hint, "a stalled LAN boot must say something");
  // The cloud copy talks about snapshots; none of that is happening here.
  assert.ok(!/snapshot/i.test(hint), `LAN hint must not mention snapshots: ${hint}`);
  // The two real-world causes are worth naming outright.
  assert.ok(/firmware|Wi-Fi|Ethernet/i.test(hint), `LAN hint should name the usual causes: ${hint}`);
  // The cloud copy still talks snapshots — but only once a cloud boot has
  // actually overrun, which takes 16 minutes, not one.
  assert.match(stallHint("booting", 1_000_000, "cloud") ?? "", /snapshot/i);
});

test("deriveServerPhase: a LAN box is judged purely on reachability", () => {
  // No control plane exists to ask — it is either back or it isn't.
  assert.equal(deriveServerPhase(null, false, "lan"), "asleep");
  assert.equal(deriveServerPhase(null, true, "lan"), "ready");
  assert.equal(deriveServerPhase({ runnersAuthorized: false }, true, "lan"), "online");
});

test("creep respects the wake kind", () => {
  // Same elapsed time, wildly different meaning.
  const lan = creepPercent("booting", 20_000, wakeStepsFor("lan"), "lan");
  const cloud = creepPercent("booting", 20_000, wakeStepsFor("cloud"), "cloud");
  assert.ok(lan > cloud, `20s into a LAN boot (${lan}) should be further along than a cloud boot (${cloud})`);
});

// ── awaiting-yaver-auth ───────────────────────────────────────────────────
// Regression: a real managed Hetzner box (mn777j15, 88.198.131.204) sat at
// "Connecting over the free relay… 65%" forever in the mobile app. The box
// was UP the whole time — its agent answered /health with
// {"authExpired":true,"lifecycle":{"state":"yaver-auth-expired",
// "recoveryMode":"reauth"}} — but its session had expired, so it could never
// register and no amount of waiting would change that. The control plane even
// said so in machine.errorMessage; the UI mapped the phase to `registering`
// and threw the sentence away.

test("awaiting-yaver-auth is not progress — it is blocked on the user", () => {
  // The exact payload GET /subscription returned for the stuck box.
  assert.equal(phase("resuming", "awaiting-yaver-auth", false), "needs-auth");
  // Same when the control plane has already flipped status to active.
  assert.equal(phase("active", "awaiting-yaver-auth", false), "needs-auth");
});

test("needs-auth must not be mistaken for a relay connection in progress", () => {
  const p = phase("resuming", "awaiting-yaver-auth", false);
  assert.notEqual(p, "registering");
  assert.match(PHASE_META[p].label, /sign/i);
  assert.equal(PHASE_META[p].tone, "warn");
});

test("needs-auth is settled so we stop polling for a flip that can't come", () => {
  // The box cannot register itself; polling forever only burns battery and
  // keeps a lying progress bar alive.
  assert.equal(isPhaseSettled("needs-auth"), true);
  assert.equal(isPhaseInFlight("needs-auth"), false);
});

test("needs-auth never creeps or stalls — there is no work happening", () => {
  assert.equal(creepPercent("needs-auth", 60_000, wakeStepsFor("cloud")), 0);
  assert.equal(stallHint("needs-auth", 600_000), null);
});

test("the genuine relay-connection phases are untouched", () => {
  assert.equal(phase("resuming", "registering", false), "registering");
  assert.equal(phase("resuming", "authorizing-runners", false), "registering");
  assert.equal(phase("active", "starting-agent", false), "registering");
  // A box that IS reachable is still ready/online regardless.
  assert.equal(phase("active", "ready", true), "ready");
});

// --- wake-only phase slugs map onto the Restoring rung -----------------------
// Mirrored in web/lib/wakeProgress.test.ts — the slugs come from the control
// plane (backend/convex/cloudMachines.ts PROVISION_PHASES), so a new one needs
// a home in BOTH mappers or it silently falls through to the default.
test("wake-only slugs sit on Restoring, not Booting", () => {
  // These all run BEFORE any server is booting; the default rung would have
  // claimed "Booting the machine…" for a machine that does not exist yet.
  assert.equal(phase("resuming", "checking-snapshot", false), "resuming");
  assert.equal(phase("resuming", "preparing-volume", false), "resuming");
  assert.equal(phase("resuming", "restoring-snapshot", false), "resuming");
});

// --- a measured wake beats the built-in constants ---------------------------
test("expectedWakeMs prefers this box's own measurement", () => {
  assert.equal(expectedWakeMs({ lastWakeDurationMs: 120_000 }), 120_000);
  // A volume-backed box has no fat disk to restore, so the snapshot-era
  // default would overstate its wake by minutes.
  assert.ok(expectedWakeMs({ hasVolume: true }) < expectedWakeMs({}));
  // One freak run must not poison every future wake.
  assert.equal(expectedWakeMs({ lastWakeDurationMs: 900 }), expectedWakeMs({}));
  assert.equal(expectedWakeMs({ lastWakeDurationMs: 3 * 3600_000 }), expectedWakeMs({}));
});

test("phase estimates scale to the box's measured pace", () => {
  const base = phaseTypicalMs("booting", "cloud") ?? 0;
  assert.ok((phaseTypicalMs("booting", "cloud", 0.25) ?? 0) < base);
  assert.ok((phaseTypicalMs("booting", "cloud", 2) ?? 0) > base);
  // A fast box must stop claiming a stall on the SLOW box's schedule.
  assert.equal(isPhaseStalled("booting", 200_000, "cloud", 1), false);
  assert.equal(isPhaseStalled("booting", 200_000, "cloud", 0.2), true);
});

// --- a parked box explains its last wake ------------------------------------
test("describeRest reports the last wake outcome", () => {
  const now = 9_000_000;
  assert.equal(describeRest({ snapshotSizeGb: 42 }, now).warning, null);
  assert.ok(describeRest({ snapshotSizeGb: 42 }, now).storage?.includes("42 GB"));

  const blocked = describeRest({ lastWakeOutcome: "needs-auth" }, now);
  assert.ok(blocked.warning && blocked.warning.includes("signed in"));

  const abandoned = describeRest({ lastWakeOutcome: "abandoned" }, now);
  assert.ok(abandoned.warning && abandoned.warning !== blocked.warning);

  // Volume-backed: nothing was snapshotted, so never claim a snapshot.
  const vol = describeRest({ hasVolume: true, snapshotSizeGb: 0 }, now);
  assert.ok(vol.storage?.includes("volume"));
  assert.ok(!vol.storage?.includes("Snapshot"));
});

test("formatDuration reads naturally at both scales", () => {
  assert.equal(formatDuration(45_000), "45s");
  assert.equal(formatDuration(240_000), "4 min");
  assert.equal(formatDuration(0), "0s");
});

// `runnersAuthorized: null` means UNKNOWN — nobody has reported either way yet.
// It is not "unauthorized", and conflating the two is what made a successful
// wake unable to render as finished: the backend sent `?? false`, coercing
// unknown into a hard false, while every readiness gate tests strict `=== false`.
// A box that had genuinely finished waking therefore sat at "online" forever.
// The backend now sends `?? null`; these pin the client half of that contract.
test("unknown runner authorization is not the same as unauthorized", () => {
  // Explicitly false — the runners really are not authorized yet.
  assert.equal(deriveServerPhase({ runnersAuthorized: false }, true, "lan"), "online");

  // null / undefined — nobody has said. A reachable box is ready; holding it at
  // "online" on the strength of a missing field is the bug this guards.
  assert.equal(deriveServerPhase({ runnersAuthorized: null }, true, "lan"), "ready");
  assert.equal(deriveServerPhase({ runnersAuthorized: undefined }, true, "lan"), "ready");
  assert.equal(deriveServerPhase({}, true, "lan"), "ready");

  // Authorized is unambiguous.
  assert.equal(deriveServerPhase({ runnersAuthorized: true }, true, "lan"), "ready");
});

// Reachability still decides the asleep end regardless of what the field says —
// an unreachable box is asleep whether or not its runners were authorized.
test("an unreachable box is asleep whatever runnersAuthorized holds", () => {
  for (const runnersAuthorized of [true, false, null, undefined]) {
    assert.equal(
      deriveServerPhase({ runnersAuthorized }, false, "lan"),
      "asleep",
      `runnersAuthorized=${String(runnersAuthorized)}`,
    );
  }
});
