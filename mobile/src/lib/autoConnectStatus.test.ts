/**
 * autoConnectStatus.test.ts — `npx tsx src/lib/autoConnectStatus.test.ts`.
 * No RN, no jest — the tiny assert harness the voice libs use.
 *
 * Pins the sweep-outcome decision behind the 2026-07-18 report: the phone
 * flapped between "Connecting" and "No machine selected" for 23 seconds and the
 * connection log showed ZERO connect attempts in that window, because the sweep
 * burned its retry token on entry and effect cleanup then cancelled it.
 */
import { resolveSweepOutcome, autoConnectSentence, autoConnectBannerStatus } from "./autoConnectStatus";

let failures = 0;
function check(name: string, cond: boolean) {
  if (cond) {
    console.log(`  ok  ${name}`);
  } else {
    failures++;
    console.error(`FAIL  ${name}`);
  }
}

console.log("resolveSweepOutcome");

check(
  "completed sweep burns the token (no pointless re-run)",
  resolveSweepOutcome({ interrupted: false, userCancelled: false }) === "burn",
);

// THE regression. Dep churn cancels the sweep; nobody asked to stop; the app
// must try again rather than present "No machine selected" having never tried.
check(
  "interrupted by effect cleanup → re-runs",
  resolveSweepOutcome({ interrupted: true, userCancelled: false }) === "rerun",
);

// The other half: re-running a sweep the user explicitly dismissed would yank
// them out of the picker they just asked for.
check(
  "user cancelled → burns the token, does NOT re-run",
  resolveSweepOutcome({ interrupted: true, userCancelled: true }) === "burn",
);

check(
  "user cancel wins even if cleanup never fired",
  resolveSweepOutcome({ interrupted: false, userCancelled: true }) === "burn",
);

console.log("narration (unchanged behaviour, guarded against drift)");
check(
  "primary sentence names the role and the box",
  autoConnectSentence({ id: "d1", name: "Mac mini", role: "primary" }) ===
    "Primary (Mac mini) is online — connecting…",
);
check(
  "sticky pick is narrated without a role word",
  autoConnectSentence({ id: "d1", name: "Mac mini", role: "sticky" }) === "Connecting to Mac mini…",
);
check("null target still says something useful", autoConnectSentence(null) === "Reaching your machines…");
check(
  "banner status is a short label, not the full sentence",
  autoConnectBannerStatus({ id: "d1", name: "Mac mini", role: "primary" }).label === "Connecting",
);

console.log(failures === 0 ? "\nAll autoConnectStatus tests passed." : `\n${failures} FAILED`);
process.exit(failures === 0 ? 0 : 1);
