/**
 * runtimeTurnAnnouncer.ts — tell the user how the thing they said turned out.
 *
 * A watch or car can only ack in one sentence: "Added to the queue." Then it
 * goes quiet. The work finishes minutes later on a box the user isn't looking
 * at, and nothing ever says so. That silence is what made the surface-neutral
 * contract feel like a write-only hole — you spoke an idea into your wrist and
 * never heard from it again.
 *
 * This module watches the queue and produces announcements for the transitions
 * a human actually cares about. It is pure and dependency-injected so it unit
 * -tests under `npx tsx` with no RN runtime.
 *
 * Rules baked in here rather than left to each surface:
 *
 *   1. ONLY announce transitions. A state that hasn't changed is not news;
 *      re-announcing it on every poll is how a notification channel gets
 *      muted, and a muted channel announces nothing at all.
 *   2. NEVER announce the first observation. The first poll after launch sees
 *      every historical turn "for the first time" — announcing those would
 *      replay a week of finished work into the user's ear.
 *   3. Car readback stays ONE short sentence and never carries code, diffs,
 *      logs, stack traces, or file paths. A driver cannot act on a stack trace
 *      and shouldn't be read one. The detail goes to the phone.
 *   4. `ready_to_test` is not "done" — it is "code done". Only a device-
 *      verified turn may be announced as live.
 */

import type { RuntimeTurnQueueItem, RuntimeTurnState } from "./runtimeSurfaceTypes";

/** States worth interrupting someone for. */
const ANNOUNCEABLE: ReadonlySet<string> = new Set([
  "needs_input",
  "ready_to_test",
  "ready_to_deploy",
  "done",
  "failed",
]);

export interface RuntimeTurnAnnouncement {
  itemId: string;
  state: RuntimeTurnState;
  /** One sentence, safe for a car or a watch. */
  spoken: string;
  /** Longer context for the phone. Never sent to car/watch TTS. */
  detail?: string;
  urgent: boolean;
}

/**
 * Strip anything a voice surface must not read aloud. Errors routinely carry
 * stack frames and absolute paths — the path also leaks the user's home-dir
 * username, which is the same reason absolute paths are forbidden in Convex.
 */
export function carSafeLine(text: string, maxLen = 120): string {
  const firstLine = (text || "").split("\n")[0] ?? "";
  const cleaned = firstLine
    .replace(/\/(?:Users|home|root)\/[^\s]*/g, "the project")
    .replace(/[A-Za-z]:\\[^\s]*/g, "the project")
    .replace(/\s+/g, " ")
    .trim();
  if (cleaned.length <= maxLen) return cleaned;
  return cleaned.slice(0, maxLen - 1).trimEnd() + "…";
}

function shortLabel(item: RuntimeTurnQueueItem): string {
  const u = (item.utterance || "").trim();
  if (!u) return "your last request";
  const words = u.split(/\s+/).slice(0, 6).join(" ");
  return words.length < u.length ? `${words}…` : words;
}

/** The one sentence a watch or car should hear for this state. */
export function announcementFor(item: RuntimeTurnQueueItem): RuntimeTurnAnnouncement | null {
  if (!ANNOUNCEABLE.has(item.state)) return null;
  const label = shortLabel(item);

  switch (item.state) {
    case "needs_input":
      return {
        itemId: item.itemId,
        state: item.state,
        spoken: `${label} needs your answer.`,
        urgent: true,
      };
    case "failed":
      return {
        itemId: item.itemId,
        state: item.state,
        // The reason goes to the phone; the car hears that it failed.
        spoken: `${label} failed. Details are on your phone.`,
        detail: item.error ? carSafeLine(item.error, 400) : undefined,
        urgent: true,
      };
    case "ready_to_test": {
      const verified = item.testTarget?.state === "verified";
      return {
        itemId: item.itemId,
        state: item.state,
        spoken: verified
          ? `${label} is live on your phone.`
          : `${label} is coded. Say test it to push it to your phone.`,
        urgent: false,
      };
    }
    case "ready_to_deploy":
      return {
        itemId: item.itemId,
        state: item.state,
        spoken: `${label} is ready to ship when you are.`,
        urgent: false,
      };
    case "done":
      return { itemId: item.itemId, state: item.state, spoken: `${label} is done.`, urgent: false };
    default:
      return null;
  }
}

/**
 * Tracks what each turn was last seen doing, and emits an announcement only
 * when a turn ENTERS an announceable state.
 */
export class RuntimeTurnAnnouncer {
  private seen = new Map<string, RuntimeTurnState>();
  private primed = false;

  /**
   * Feed the latest queue listing. Returns the announcements to deliver.
   *
   * The first call only primes the baseline (rule 2) — it never announces, so
   * opening the app doesn't replay every finished turn.
   */
  observe(items: RuntimeTurnQueueItem[]): RuntimeTurnAnnouncement[] {
    const out: RuntimeTurnAnnouncement[] = [];
    for (const item of items) {
      const prev = this.seen.get(item.itemId);
      this.seen.set(item.itemId, item.state);
      if (!this.primed) continue;
      if (prev === item.state) continue;
      // A turn seen for the first time AFTER priming is genuinely new work
      // (e.g. spoken from another surface), so it may announce.
      const a = announcementFor(item);
      if (a) out.push(a);
    }
    this.primed = true;
    return out;
  }

  /** Forget a turn (e.g. evicted from the queue) so it can announce again. */
  forget(itemId: string): void {
    this.seen.delete(itemId);
  }

  reset(): void {
    this.seen.clear();
    this.primed = false;
  }
}
