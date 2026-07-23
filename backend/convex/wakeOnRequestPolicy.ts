// wakeOnRequestPolicy.ts — should an inbound request wake a parked box?
//
// This is the core primitive of "a cloud for small software": software with
// three users is idle ~99% of the time, so it must cost nothing while idle and
// still answer when someone knocks. Hetzner bills metered and bills STOPPED
// servers too, so parked means DELETED-and-recreatable, not suspended — which
// is why waking is a real operation with a real latency and a real cost, not a
// flag flip.
//
// That cost is why waking is PERMISSIONED rather than owner-hardcoded.
//
// The tempting rule is "only the owner may wake". It is wrong twice. It bakes
// one policy into code nobody can change without a deploy, and it defeats the
// entire product: if only the owner can wake the box, then a small app's actual
// users can never reach it, and "scale to zero" just means "offline". The owner
// must be able to say "yes, my five users' requests may wake this" — and to say
// it for one app, one guest, or one grant at a time, and take it back.
//
// So this file decides on a PERMISSION (`callerMayWake`), never on identity.
// Where that permission comes from is resolved by the caller:
//
//   * the machine's owner holds it implicitly;
//   * a guest / host-share / project-share holds it only if the owner set
//     `allowWake` on the grant — same family as the existing owner-controlled
//     allowDesktopControl / allowTunnelForward / allowBrowserControl flags,
//     and default OFF like them;
//   * a published small app's scoped token carries it when the owner published
//     the app as wake-on-request.
//
// Anonymous traffic never reaches this decision: the relay authenticates every
// proxy request (device signature or relay password) before a tunnel is looked
// up, so `callerMayWake` is always evaluated against a known principal.
//
// Dependency-free on purpose: the rule must be unit-testable, and a module with
// no imports can be executed by `node --experimental-strip-types --test`.

/**
 * What the relay should do when it has no tunnel for a target device.
 *
 *   "connected"            — a tunnel exists; not this file's business.
 *   "wake"                 — parked, recreatable, caller is permitted. Wake it
 *                            and hold the caller with a retry hint.
 *   "waking"               — a wake is already in flight; do NOT start a second
 *                            one, just tell the caller to retry.
 *   "parked-no-permission" — asleep, and this caller was not granted wake.
 *                            Honest "asleep, ask the owner" — never a silent
 *                            spend of someone else's money.
 *   "parked-not-wakeable"  — asleep with nothing to restore from. Terminal; say
 *                            so rather than offering a button that cannot work.
 *   "parked-budget-spent"  — permitted, but the owner's wake budget for this
 *                            window is used up. The owner's cap, not ours.
 *   "unknown"              — not a managed box we can reason about. Existing
 *                            502 behavior, unchanged.
 */
export type WakeVerdict =
  | "connected"
  | "wake"
  | "waking"
  | "parked-no-permission"
  | "parked-not-wakeable"
  | "parked-budget-spent"
  | "unknown";

export type WakeDecision = {
  verdict: WakeVerdict;
  /** Whether the caller should retry the same request shortly. */
  retryable: boolean;
  /** Seconds to put in Retry-After. 0 when retrying will not help. */
  retryAfterSeconds: number;
  /**
   * Message shown to the caller. Names the actual state and the actual remedy —
   * a small app's user hitting a flat "502 device not connected" has no idea
   * whether to wait, reload, or tell the developer.
   */
  message: string;
};

/**
 * How long a wake may be considered "already in flight" before another request
 * is allowed to trigger a fresh one. A cold recreate-from-volume is tens of
 * seconds; a snapshot restore is longer. Deliberately generous: re-triggering
 * costs money and can thrash the provider, while waiting one extra cycle only
 * costs latency on a request that is already slow.
 */
export const WAKE_IN_FLIGHT_WINDOW_MS = 180_000;

/** Retry hint while a box boots. Short enough to feel alive, long enough not
 *  to turn one user's page-load into a retry storm against the provider. */
export const WAKE_RETRY_AFTER_SECONDS = 15;

export function classifyWakeTarget(opts: {
  /** A live tunnel is registered for this device on the relay. */
  hasTunnel: boolean;
  /** We found a managed machine row for this target. */
  known: boolean;
  /** Result of isMachineWakeable() on that row. */
  wakeable: boolean;
  /**
   * Whether THIS caller holds wake permission on THIS machine. Resolved by the
   * caller from ownership or an owner-granted allowWake. Never inferred here —
   * see the header for why identity must not be the rule.
   */
  callerMayWake: boolean;
  /**
   * Wakes the owner still allows in the current window. undefined = the owner
   * set no cap. 0 = cap reached. The cap is the owner's spend control; this
   * file only reads it.
   */
  wakeBudgetRemaining?: number;
  /** Epoch millis of the last wake we started, if any. */
  lastWokeAt?: number;
  /** Epoch millis now. Passed in so this stays pure and testable. */
  now: number;
}): WakeDecision {
  if (opts.hasTunnel) {
    return { verdict: "connected", retryable: false, retryAfterSeconds: 0, message: "" };
  }
  if (!opts.known) {
    return {
      verdict: "unknown",
      retryable: false,
      retryAfterSeconds: 0,
      message: "device not connected to relay",
    };
  }

  // Permission is checked BEFORE wakeability on purpose: a caller without
  // permission must not be able to learn whether someone else's box has a
  // snapshot by reading which refusal comes back.
  if (!opts.callerMayWake) {
    return {
      verdict: "parked-no-permission",
      retryable: false,
      retryAfterSeconds: 0,
      message:
        "this machine is parked (asleep) and you do not have permission to wake it — ask its owner to bring it up, or to grant wake access",
    };
  }

  if (!opts.wakeable) {
    return {
      verdict: "parked-not-wakeable",
      retryable: false,
      retryAfterSeconds: 0,
      message:
        "this machine is parked with no snapshot or volume-backed image to restore from — it cannot be woken automatically",
    };
  }

  // An in-flight wake wins over the budget check: the spend already happened,
  // and refusing here would tell a waiting user "budget spent" about a box that
  // is at that moment coming up for them.
  const last = typeof opts.lastWokeAt === "number" ? opts.lastWokeAt : 0;
  if (last > 0 && opts.now - last < WAKE_IN_FLIGHT_WINDOW_MS) {
    return {
      verdict: "waking",
      retryable: true,
      retryAfterSeconds: WAKE_RETRY_AFTER_SECONDS,
      message: "machine is waking up — retry shortly",
    };
  }

  if (typeof opts.wakeBudgetRemaining === "number" && opts.wakeBudgetRemaining <= 0) {
    return {
      verdict: "parked-budget-spent",
      retryable: false,
      retryAfterSeconds: 0,
      message:
        "this machine is parked and its owner's automatic-wake budget for this period is used up — ask them to wake it or raise the limit",
    };
  }

  return {
    verdict: "wake",
    retryable: true,
    retryAfterSeconds: WAKE_RETRY_AFTER_SECONDS,
    message: "machine was parked and is now waking up — retry shortly",
  };
}
