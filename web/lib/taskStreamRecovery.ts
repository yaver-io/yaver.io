// taskStreamRecovery.ts — the DECISION seam for a task-output stream that
// ended (failure-recovery audit 2026-07, connectivity+vibing pass).
//
// The defect this exists to kill: `streamTaskOutput` on BOTH surfaces used to
// swallow a stream drop in silence — mobile's `xhr.onerror` was an empty
// handler whose comment said "silent (matches the previous behavior)", web's
// reader loop ended in `catch {}` with "Silent best-effort stream". When the
// relay bounced, the box dropped, or the tunnel broke mid-render, the live
// transcript simply stopped growing. No surface detected it, none resubscribed,
// and the user watched a frozen last frame with a spinner over it — with the
// task still running perfectly well on the box.
//
// The agent has supported reattaching the whole time (`/tasks/{id}/output`
// replays on subscribe, and now takes `?since=<bytes>` so the reattach is
// lossless). The route existed; nobody took it. This module is the policy that
// takes it, plus the sentence to render while it happens — because a reattach
// the user cannot see is just a different silence.
//
// KEEP IN SYNC with mobile/src/lib/taskStreamRecovery.ts (same shapes, same
// wording — taskStreamRecovery.test.ts pins the parity).

/** How a task-output stream ended. */
export type StreamEndKind =
  /** A terminal `done` frame arrived — the task really finished. */
  | "done"
  /** The client tore the stream down itself (navigated away, switched task). */
  | "cancelled"
  /** The stream died without saying goodbye. This is not a task verdict. */
  | "interrupted";

export interface StreamEndInput {
  /** A `done` frame was seen before the stream closed. */
  sawDone: boolean;
  /** The teardown was initiated by this client. */
  cancelled: boolean;
  /** Transport error text, when the platform gave us one. */
  error?: string | null;
  /** HTTP response status when headers arrived. */
  httpStatus?: number;
}

/**
 * An end with neither a `done` frame nor a local cancel is an INTERRUPTION,
 * whether or not the platform bothered to report an error. A clean EOF on an
 * SSE stream that should never close is exactly what a dropped relay tunnel
 * looks like — treating "no error object" as "fine" is how the freeze shipped.
 */
export function classifyStreamEnd(input: StreamEndInput): StreamEndKind {
  if (input.sawDone) return "done";
  if (input.cancelled) return "cancelled";
  return "interrupted";
}

/** Reattach attempts before we stop and hand the user a button. */
export const MAX_REATTACH_ATTEMPTS = 5;

/**
 * Bounded backoff: 1s, 2s, 4s, 8s, then 15s. A relay bounce heals in seconds,
 * so the first rungs are fast; the cap keeps a genuinely-down box from being
 * hammered by every open tab.
 */
export function reattachDelayMs(attempt: number): number {
  const ladder = [1000, 2000, 4000, 8000, 15000];
  const idx = Math.max(0, Math.min(attempt, ladder.length - 1));
  return ladder[idx];
}

export type StreamRecoveryPlan =
  /** Nothing to do — the stream ended the way it was supposed to. */
  | { action: "idle" }
  | { action: "reattach"; attempt: number; delayMs: number; message: string }
  | { action: "give-up"; message: string };

function withCause(sentence: string, cause: string | null | undefined): string {
  const trimmed = String(cause || "").trim();
  return trimmed ? `${sentence} (${trimmed})` : sentence;
}

/**
 * What to do about a stream that ended, and what to SAY while doing it.
 *
 * A dead stream is not proof that either the task or the box died. Keep the
 * wording epistemically honest: the last task state remains visible, but only
 * a fresh task probe may claim that the runner is still working.
 */
export function planStreamRecovery(input: {
  end: StreamEndKind;
  attempt: number;
  maxAttempts?: number;
  cause?: string | null;
  httpStatus?: number;
}): StreamRecoveryPlan {
  if (input.end !== "interrupted") return { action: "idle" };

  const max = input.maxAttempts ?? MAX_REATTACH_ATTEMPTS;
  const status = input.httpStatus || 0;
  if (status >= 400 && status < 500 && status !== 408 && status !== 425 && status !== 429) {
    const message = status === 404
      ? "This machine does not have this task. Refresh Tasks to resync its owner."
      : status === 401 || status === 403
        ? "The task machine rejected stream authorization. Reconnect it, then try again."
        : `The task machine rejected the live stream (HTTP ${status}).`;
    return { action: "give-up", message };
  }

  if (input.attempt >= max) {
    return {
      action: "give-up",
      message: withCause(
        `Live output is still disconnected after ${max} attempts. The task status is unchanged.`,
        input.cause,
      ),
    };
  }

  return {
    action: "reattach",
    attempt: input.attempt,
    delayMs: reattachDelayMs(input.attempt),
    message: withCause(
      `Live output interrupted · reconnecting ${input.attempt + 1}/${max}… The task has not reported a failure.`,
      input.cause,
    ),
  };
}
