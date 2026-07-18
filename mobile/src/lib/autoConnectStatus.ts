/**
 * autoConnectStatus.ts — one source of truth for how the auto-connect state
 * reads, so every surface (mobile tabs, tablet, glass/AR, car, and — mirrored —
 * web/TV/watch) narrates it identically. Pure + trivially testable.
 *
 * Product intent: after login, silently connect to the primary if it's online,
 * else the secondary; narrate the attempt ("Primary (Mac mini) is online —
 * connecting…") and let the user cancel. Never dump them on a "No machine
 * selected" wall while a connect is in flight.
 */

export interface AutoConnectTarget {
  id: string;
  name: string;
  role: "primary" | "secondary" | "sticky";
}

export function roleWord(role: AutoConnectTarget["role"]): string {
  return role === "primary" ? "Primary" : role === "secondary" ? "Secondary" : "Your machine";
}

/** Compact two-part status for the connection banner (tight width). */
export function autoConnectBannerStatus(t: AutoConnectTarget | null): {
  label: string;
  detail: string;
} {
  if (!t) return { label: "Connecting", detail: "Reaching your machines…" };
  return { label: "Connecting", detail: `${roleWord(t.role)} · ${t.name}` };
}

/** Full sentence for the empty-state / large surfaces. */
export function autoConnectSentence(t: AutoConnectTarget | null): string {
  if (!t) return "Reaching your machines…";
  if (t.role === "sticky") return `Connecting to ${t.name}…`;
  return `${roleWord(t.role)} (${t.name}) is online — connecting…`;
}

/**
 * What a finished auto-connect sweep should do with its retry token.
 *
 * This is the decision that caused the 2026-07-18 "23 seconds of flapping with
 * zero connect attempts" report. The sweep marked its nonce attempted on ENTRY,
 * so a sweep cancelled mid-probe by React effect cleanup (fired by churny deps
 * — `devices` re-identifies every 30s poll, `connectedDeviceIds` mutates when
 * the pool-warmer opens a background connection) left the app believing an
 * attempt had been made when none had. The banner sat on "No machine selected"
 * until the user picked a box by hand.
 *
 * Kept pure and separate from the effect so the three outcomes stay honest:
 *   - "burn": the sweep ran to completion (or the USER cancelled it). Don't
 *     re-run — re-running a sweep the user just dismissed is a UI trap.
 *   - "rerun": cleanup interrupted us and nobody asked to stop. Re-arm AND
 *     schedule, because the re-entrant effect already bailed on the in-flight
 *     guard while this sweep was unwinding; re-arming alone triggers nothing.
 */
export type SweepOutcome = "burn" | "rerun";

export function resolveSweepOutcome(args: {
  /** Effect cleanup ran (a dependency changed). */
  interrupted: boolean;
  /** The user explicitly asked to stop ("Choose a machine myself"). */
  userCancelled: boolean;
}): SweepOutcome {
  if (args.userCancelled) return "burn";
  return args.interrupted ? "rerun" : "burn";
}
