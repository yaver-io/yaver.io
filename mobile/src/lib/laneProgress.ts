// laneProgress.ts — the "slow vs stuck" rule, with no React imports.
//
// Split out of LaneStartupStatus.tsx so it can actually be executed by a test:
// a module that pulls in react-native cannot be run under plain node, and an
// untested rule is how the original spinner shipped.
//
// Reported from TestFlight 465 on a Flutter compile: "taking too much time,
// feels stuck", "I'm not sure whether it's going to load or not, I don't feel
// secure at all". A first web compile legitimately runs for minutes, so the
// answer is not a faster lane — it is telling the user the two things that
// separate SLOW from STUCK:
//
//   elapsed      — time is passing, and how much
//   last output  — the BOX is still talking
//
// Silence is stated outright rather than left to be inferred.

/** Seconds of silence after which a lane is called out as possibly stalled. */
export const LANE_STALL_SECONDS = 45;

export function formatElapsed(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

export function describeLaneProgress(opts: {
  startedAt: number | null;
  lastOutputAt: number | null;
  now: number;
  stallSeconds?: number;
}): { text: string; stalled: boolean } | null {
  if (!opts.startedAt) return null;
  const stallAfter = opts.stallSeconds ?? LANE_STALL_SECONDS;
  const elapsed = formatElapsed((opts.now - opts.startedAt) / 1000);
  if (!opts.lastOutputAt) {
    return { text: `${elapsed} elapsed · waiting for the first output from the box`, stalled: false };
  }
  const quiet = Math.floor((opts.now - opts.lastOutputAt) / 1000);
  if (quiet >= stallAfter) {
    return { text: `${elapsed} elapsed · no output for ${quiet}s — this may be stalled`, stalled: true };
  }
  return { text: `${elapsed} elapsed · last output ${quiet}s ago`, stalled: false };
}
