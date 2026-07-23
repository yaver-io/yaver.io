import { describeLaneProgress, formatElapsed, LANE_STALL_SECONDS } from "./laneProgress.ts";

// The rule these pin: a user must be able to tell SLOW from STUCK. Reported
// from TestFlight 465 — "I'm not sure whether it's going to load or not".
const T0 = 1_700_000_000_000;

function expectEq(a: unknown, b: unknown, msg: string) {
  if (a !== b) throw new Error(`${msg}: got ${JSON.stringify(a)}, want ${JSON.stringify(b)}`);
}

expectEq(formatElapsed(0), "0:00", "zero");
expectEq(formatElapsed(9), "0:09", "pads seconds");
expectEq(formatElapsed(74), "1:14", "minutes");
expectEq(formatElapsed(-5), "0:00", "never negative");

expectEq(describeLaneProgress({ startedAt: null, lastOutputAt: null, now: T0 }), null,
  "no start time renders nothing");

// Nothing heard yet: say so plainly rather than implying output exists.
const waiting = describeLaneProgress({ startedAt: T0, lastOutputAt: null, now: T0 + 5000 })!;
expectEq(waiting.stalled, false, "waiting is not stalled");
if (!waiting.text.includes("waiting for the first output")) throw new Error(`waiting text: ${waiting.text}`);
if (!waiting.text.startsWith("0:05")) throw new Error(`elapsed missing: ${waiting.text}`);

// Output flowing = alive, however long it has been running.
const alive = describeLaneProgress({ startedAt: T0, lastOutputAt: T0 + 175_000, now: T0 + 180_000 })!;
expectEq(alive.stalled, false, "recent output is never stalled, even at 3min elapsed");
if (!alive.text.includes("last output 5s ago")) throw new Error(`alive text: ${alive.text}`);
if (!alive.text.startsWith("3:00")) throw new Error(`elapsed wrong: ${alive.text}`);

// Silence is called out — this is the whole point.
const quiet = describeLaneProgress({
  startedAt: T0, lastOutputAt: T0 + 1000, now: T0 + 1000 + LANE_STALL_SECONDS * 1000,
})!;
expectEq(quiet.stalled, true, "silence past the threshold is stalled");
if (!quiet.text.includes("no output for")) throw new Error(`stalled text: ${quiet.text}`);

// Just under the threshold must NOT alarm.
const almost = describeLaneProgress({
  startedAt: T0, lastOutputAt: T0 + 1000, now: T0 + 1000 + (LANE_STALL_SECONDS - 1) * 1000,
})!;
expectEq(almost.stalled, false, "one second under the threshold is not stalled");

console.log("LaneStartupStatus: all assertions passed");
