/**
 * wakeProgress.test.ts — `npx tsx lib/wakeProgress.test.ts` from web/.
 * Plain node, same tiny assert harness agentStatus.test.ts and the mobile
 * libs use.
 *
 * THE PHASE TABLE below is the cross-package agreement between this file and
 * mobile/src/lib/wakeMachineCore.ts. web/ and mobile/ share no build, so the
 * only thing keeping the two ladders honest about the same control-plane
 * slugs is that both sides assert the same mapping.
 *
 * The bug that motivated all of this: a box whose Yaver session had expired
 * sat at status="resuming" server-side, so web rendered "Resuming…" forever
 * for a wake that had already stopped and would never resume on its own.
 * Several assertions here exist purely to keep that from coming back.
 */
import {
  PHASE_META,
  computeWakeView,
  creepPercent,
  deriveServerPhase,
  describeRest,
  expectedWakeMs,
  formatClock,
  formatDuration,
  isPhaseInFlight,
  isPhaseSettled,
  providerLine,
  stallHint,
  WAKE_STEPS,
  type LifecyclePhase,
} from "./wakeProgress";

let passed = 0;
let failed = 0;
function ok(cond: boolean, msg: string) {
  if (cond) {
    passed++;
  } else {
    failed++;
    console.error("  ✗ " + msg);
  }
}
function eq<T>(got: T, want: T, msg: string) {
  ok(got === want, `${msg} (got ${String(got)}, want ${String(want)})`);
}

/**
 * THE PHASE TABLE. provisionPhase slug (as written by the control plane in
 * backend/convex/cloudMachines.ts PROVISION_PHASES) → the rung we show while
 * the device is NOT yet reachable. Mirrored in mobile's deriveServerPhase.
 */
const PHASE_TABLE: Record<string, LifecyclePhase> = {
  // Wake-only steps — all before any server is booting.
  "checking-snapshot": "resuming",
  "preparing-volume": "resuming",
  "restoring-snapshot": "resuming",
  // Provider has a server; the OS is coming up.
  creating: "booting",
  booting: "booting",
  "installing-docker": "booting",
  "pulling-image": "booting",
  // The agent is alive and dialing out.
  "starting-agent": "registering",
  registering: "registering",
  "authorizing-runners": "registering",
  // Control plane says ready but we can't reach it yet — not a false 100%.
  ready: "registering",
  // Terminal: blocked on the user.
  "awaiting-yaver-auth": "needs-auth",
};

for (const [slug, want] of Object.entries(PHASE_TABLE)) {
  eq(
    deriveServerPhase({ status: "resuming", provisionPhase: slug }, false),
    want,
    `phase table: ${slug}`,
  );
}

// An unknown slug must degrade to a rung that exists, never to undefined.
eq(
  deriveServerPhase({ status: "resuming", provisionPhase: "who-knows" }, false),
  "booting",
  "unknown slug falls back to booting",
);

// Reachability outranks every phase claim: once the box answers, it is up.
eq(
  deriveServerPhase({ status: "resuming", provisionPhase: "booting" }, true),
  "ready",
  "reachable box is ready regardless of phase",
);
eq(
  deriveServerPhase({ status: "resuming", provisionPhase: "booting", runnersAuthorized: false }, true),
  "online",
  "reachable but runners unauthorized is online, not ready",
);

// Resting + parking directions.
eq(deriveServerPhase({ status: "paused" }, false), "asleep", "paused is asleep");
eq(deriveServerPhase({ status: "stopping" }, false), "snapshotting", "stopping snapshots first");
eq(
  deriveServerPhase({ status: "stopping", provisionPhase: "powering-down" }, false),
  "powering-down",
  "stopping + powering-down",
);
eq(deriveServerPhase({ status: "error" }, false), "error", "error is error");

// --- needs-auth must never look like progress -----------------------------
ok(isPhaseSettled("needs-auth"), "needs-auth is settled so polling can stop");
ok(!isPhaseInFlight("needs-auth"), "needs-auth is not in flight");
eq(creepPercent("needs-auth", 10 * 60_000, WAKE_STEPS), 0, "needs-auth never creeps");
eq(stallHint("needs-auth", 30 * 60_000), null, "needs-auth has no stall hint — it is not stalled, it is waiting");

// A wake blocked on sign-in reports the control plane's own sentence, which
// web declared, rendered, and then never mapped off /subscription.
{
  const view = computeWakeView(
    {
      status: "resuming",
      provisionPhase: "awaiting-yaver-auth",
      errorMessage: "The box is awake but its Yaver agent session expired.",
      provisionPhaseAt: Date.now() - 60_000,
    },
    false,
    Date.now(),
  );
  eq(view.phase, "needs-auth", "blocked wake derives needs-auth");
  eq(view.direction, null, "needs-auth has no direction, so no bar renders");
  ok(view.error !== null, "needs-auth surfaces the control plane sentence");
}

// --- the bar is honest ----------------------------------------------------
{
  // Creep moves inside a phase but must never reach the next rung.
  const gapEnd = PHASE_META.booting.percent;
  const crept = PHASE_META.resuming.percent + creepPercent("resuming", 60_000, WAKE_STEPS);
  ok(crept > PHASE_META.resuming.percent, "creep advances within the phase");
  ok(crept < gapEnd, "creep never reaches the next rung");
}
eq(creepPercent("ready", 60_000, WAKE_STEPS), 0, "terminal phases do not creep");

{
  // A one-hour "booting" is stalled and must say something real.
  ok(stallHint("booting", 60 * 60_000) !== null, "an overrunning boot explains itself");
  eq(stallHint("booting", 5_000), null, "a fresh boot is not stalled");
}

{
  // The in-phase clock anchors on provisionPhaseAt, not the whole wake —
  // that is what separates "booting for 20s" from "booting for 9 minutes".
  const now = 1_000_000;
  const view = computeWakeView(
    { status: "resuming", provisionPhase: "booting", lastWokeAt: now - 600_000, provisionPhaseAt: now - 20_000 },
    false,
    now,
  );
  eq(view.elapsedInPhaseMs, 20_000, "in-phase clock uses provisionPhaseAt");
  eq(view.elapsedTotalMs, 600_000, "total clock uses lastWokeAt");
  eq(view.stallHint, null, "a 20s boot is not stalled even in a 10min wake");
}

// --- provider line --------------------------------------------------------
{
  const now = 2_000_000;
  eq(
    providerLine({ providerStatus: "initializing", providerStatusAt: now - 5_000 }, "booting", now),
    "Provider: the provider is still initializing the server.",
    "fresh provider status is shown while booting",
  );
  eq(
    providerLine({ providerStatus: "initializing", providerStatusAt: now - 600_000 }, "booting", now),
    null,
    "stale provider status is dropped — worse than none, it reads as current",
  );
  eq(
    providerLine({ providerStatus: "running", providerStatusAt: now }, "registering", now),
    null,
    "provider line goes quiet once our own signal is better",
  );
  eq(
    providerLine({ providerStatus: "some-new-enum", providerStatusAt: now }, "booting", now),
    null,
    "an unmapped provider enum is never printed raw",
  );
}

// --- measured ETA beats the constants -------------------------------------
{
  // A volume-backed box genuinely wakes in ~1-2 min. Promising it the
  // snapshot-era default (~11 min) was wrong for every box but the one the
  // constants were measured on.
  const fast = expectedWakeMs({ hasVolume: true });
  const dflt = expectedWakeMs({});
  ok(fast < dflt, "volume-backed box gets a shorter default than a snapshot box");

  eq(expectedWakeMs({ lastWakeDurationMs: 120_000 }), 120_000, "measured duration wins");
  // Implausible measurements must not poison future wakes.
  eq(expectedWakeMs({ lastWakeDurationMs: 900 }), dflt, "a 0.9s 'wake' is ignored");
  eq(expectedWakeMs({ lastWakeDurationMs: 3 * 3600_000 }), dflt, "a 3h 'wake' is ignored");
}

{
  // The bar's pace follows the measurement, so a fast box isn't told
  // "~8 min left in this step".
  const now = 5_000_000;
  const fast = computeWakeView(
    { status: "resuming", provisionPhase: "booting", provisionPhaseAt: now - 10_000, lastWakeDurationMs: 90_000 },
    false, now,
  );
  const slow = computeWakeView(
    { status: "resuming", provisionPhase: "booting", provisionPhaseAt: now - 10_000, lastWakeDurationMs: 900_000 },
    false, now,
  );
  ok(fast.scale < slow.scale, "a faster box gets a smaller scale");
  ok(fast.percent > slow.percent, "a faster box's bar is further along at the same elapsed time");
}

// --- a parked box explains its last wake ----------------------------------
{
  const now = 9_000_000;
  const peaceful = describeRest({ status: "paused", snapshotSizeGb: 42, snapshotCreatedAt: now - 3600_000 }, now);
  eq(peaceful.warning, null, "a clean park carries no warning");
  ok(peaceful.storage?.includes("42 GB") === true, "snapshot size is stated");

  const blocked = describeRest({ status: "paused", lastWakeOutcome: "needs-auth" }, now);
  ok(blocked.warning !== null, "a needs-auth park explains itself");
  ok(blocked.warning!.includes("signed in"), "and says what to do about it");

  const abandoned = describeRest({ status: "paused", lastWakeOutcome: "abandoned" }, now);
  ok(abandoned.warning !== null, "an abandoned wake explains itself");
  ok(abandoned.warning !== blocked.warning, "the two failure modes read differently");

  // Volume-backed: nothing was snapshotted, so don't claim a snapshot.
  const vol = describeRest({ status: "paused", hasVolume: true, snapshotSizeGb: 0 }, now);
  ok(vol.storage?.includes("volume") === true, "volume-backed says volume, not snapshot");
  ok(vol.storage?.includes("Snapshot") !== true, "and never claims a snapshot it doesn't have");
}

// --- optimistic wake feedback ---------------------------------------------
{
  const now = 7_000_000;
  const parked = { status: "paused" };
  eq(computeWakeView(parked, false, now).phase, "asleep", "parked with no request is asleep");
  eq(
    computeWakeView(parked, false, now, { kind: "wake", at: now - 1_000 }).phase,
    "requested",
    "a just-pressed Wake shows immediately, before the server catches up",
  );
  // A request the control plane never accepted must not creep forever.
  eq(
    computeWakeView(parked, false, now, { kind: "wake", at: now - 120_000 }).phase,
    "asleep",
    "a stale optimistic request falls back to the truth",
  );
  // Once the server HAS moved, the real phase wins over the optimistic one.
  eq(
    computeWakeView(
      { status: "resuming", provisionPhase: "booting" }, false, now, { kind: "wake", at: now - 1_000 },
    ).phase,
    "booting",
    "server truth outranks the optimistic rung",
  );
  // The optimistic clock times from the click, not a stale phase stamp.
  const v = computeWakeView(
    { status: "paused", provisionPhaseAt: now - 999_000 }, false, now, { kind: "wake", at: now - 3_000 },
  );
  eq(v.elapsedInPhaseMs, 3_000, "optimistic elapsed times from the click");
}

// --- misc -----------------------------------------------------------------
eq(formatDuration(45_000), "45s", "sub-90s durations read in seconds");
eq(formatDuration(240_000), "4 min", "longer durations read in minutes");
eq(formatDuration(0), "0s", "zero duration");
eq(formatClock(0), "0:00", "clock at zero");
eq(formatClock(65_000), "1:05", "clock pads seconds");
eq(formatClock(-5), "0:00", "clock never goes negative");

// Every rung the stepper renders must have metadata, or the ladder throws.
for (const sp of WAKE_STEPS) ok(!!PHASE_META[sp], `wake step ${sp} has metadata`);

console.log(`${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
