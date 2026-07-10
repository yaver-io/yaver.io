/**
 * carReplyDispatch.ts — wire an Android Auto RemoteInput reply back into the
 * coding pipeline, with a risky-verb confirm gate.
 *
 * See docs/yaver-car-voice-coding.md §3 (Tier 1) + §4 (driving-safe rules).
 *
 * The Tier 1 surface (carMessagingNotification.ts) posts the coding agent's
 * status as an Android-Auto "message"; the car captures a spoken reply via
 * RemoteInput and the native receiver forwards it to JS as a `yaverCarReply`
 * event (subscribeCarReplies). THIS file turns that raw reply text into an
 * actual command on the chosen box.
 *
 * Two hard rules, both enforced here (not in carVoiceCoding.ts, which this
 * reuses):
 *
 *   1. CONFIRM GATE. A raw spoken reply must NOT auto-fire a destructive /
 *      irreversible action (deploy, push, force-push, rm -rf, drop database,
 *      reset --hard, …). When the reply looks risky we DON'T dispatch it: we
 *      stash it as "pending" for that conversation and return a prompt asking
 *      the driver to say "confirm" (or "yes"/"do it"). Only an explicit confirm
 *      reply releases the stashed command into dispatch. A "cancel"/"no" reply
 *      (or anything that isn't a confirm) discards it.
 *
 *   2. NEVER read code aloud. Inherited from carVoiceCoding.ts via
 *      dispatchAndSummarize — read-code asks are declined there.
 *
 * Pure + dependency-injected so it unit-tests under `npx tsx` with no RN.
 * The production wiring (subscribeCarReplies → handleCarReply → dispatch via
 * connectionManager.clientFor(box).sendTask(..., codeMode=true)) is assembled
 * by the caller; we only own the gate + dispatch glue.
 */

import {
  dispatchAndSummarize,
  type CarVoiceDeps,
  type CarVoiceConfig,
  type CarVoiceResult,
} from "./carVoiceCoding";
import {
  executeCarSurfaceIntent,
  type CarSurfaceOps,
} from "./carSurfaceIntent";
import {
  dispatchSessionTurn,
  type SessionTurnDep,
} from "./carSessionTurn";

// ── Risky-verb detection ─────────────────────────────────────────────

/**
 * True when a reply describes a destructive / irreversible / hard-to-undo
 * action that must not fire from a single unconfirmed spoken reply.
 *
 * Deliberately conservative-on-the-safe-side: it flags the well-known
 * dangerous verbs (deploy/publish/release, push/force-push, rm/delete,
 * drop/truncate a DB, git reset --hard / clean -fdx, terraform apply/destroy,
 * kubectl delete, shutdown/reboot, merge to main/prod). Normal coding commands
 * ("add a test", "fix the build", "rename the function") pass through ungated.
 */
export function isRiskyReply(text: string): boolean {
  const t = ` ${text.toLowerCase()} `;
  // Deploy / publish / release / ship to prod.
  if (/\b(deploy|publish|release|ship)\b/.test(t)) return true;
  // git push / force push.
  if (/\bpush(es|ed|ing)?\b/.test(t)) return true;
  if (/\bforce[- ]?push\b/.test(t)) return true;
  // Destructive file ops.
  if (/\brm\s+-?rf?\b/.test(t)) return true;
  if (/\b(delete|remove|wipe|erase|nuke)\b/.test(t)) return true;
  // git destructive.
  if (/\breset\s+--?hard\b/.test(t) || /\bhard reset\b/.test(t)) return true;
  if (/\bclean\s+-?fd/.test(t) || /\bgit clean\b/.test(t)) return true;
  if (/\bforce[- ]?merge\b/.test(t)) return true;
  // DB destructive.
  if (/\bdrop\s+(table|database|db|schema)\b/.test(t)) return true;
  if (/\btruncate\b/.test(t)) return true;
  // Infra.
  if (/\b(terraform|tf)\s+(apply|destroy)\b/.test(t)) return true;
  if (/\bkubectl\s+delete\b/.test(t)) return true;
  if (/\b(shutdown|reboot|poweroff|destroy)\b/.test(t)) return true;
  // Merge / promote to a protected branch.
  if (
    /\bmerge\b/.test(t) &&
    /\b(main|master|prod|production|release)\b/.test(t)
  )
    return true;
  return false;
}

/** True when a reply is an explicit confirmation of a pending risky command. */
export function isConfirmReply(text: string): boolean {
  const t = text
    .trim()
    .toLowerCase()
    .replace(/[.!]+$/, "");
  return /^(confirm|confirmed|yes|yep|yeah|do it|go ahead|proceed|approved?|send it)$/.test(
    t,
  );
}

/** True when a reply explicitly cancels a pending risky command. */
export function isCancelReply(text: string): boolean {
  const t = text
    .trim()
    .toLowerCase()
    .replace(/[.!]+$/, "");
  return /^(cancel|no|nope|stop|abort|never ?mind|forget it)$/.test(t);
}

// ── Pending-confirm store (per conversation) ─────────────────────────

interface PendingCommand {
  command: string;
  /** epoch ms the pending command was stashed (for staleness, if needed). */
  at: number;
}

/**
 * Holds the per-conversation "awaiting confirm" command. Kept tiny + explicit
 * so the gate is testable without globals; the production binding owns one
 * instance for the app's car surface.
 */
export class CarReplyGate {
  private pending = new Map<string, PendingCommand>();

  /** Stash a risky command awaiting confirmation. */
  setPending(conversationId: string, command: string, now = Date.now()): void {
    this.pending.set(conversationId, { command, at: now });
  }

  /** Pop (and clear) the pending command for a conversation, if any. */
  takePending(conversationId: string): string | undefined {
    const p = this.pending.get(conversationId);
    if (p) this.pending.delete(conversationId);
    return p?.command;
  }

  hasPending(conversationId: string): boolean {
    return this.pending.has(conversationId);
  }

  clear(conversationId: string): void {
    this.pending.delete(conversationId);
  }
}

// ── Session-choice gate (per conversation) ───────────────────────────

/**
 * Tracks whether the last session turn left the pane on a menu
 * (awaitingChoice). When the driver speaks a number/yes/no after a menu,
 * `handleCarReply` uses this to route the reply as {choice} instead of {text}.
 *
 * Separate from [CarReplyGate] (which gates risky verbs) because session choices
 * are NOT risky — they're the normal interaction model for a coding session
 * that's asking "1. Yes, I trust this folder / 2. No, exit". The risk gate
 * still applies to the initial transcript; the choice gate handles the
 * follow-up menu answer.
 */
export class SessionChoiceGate {
  private pending = new Map<string, boolean>();

  setAwaiting(conversationId: string): void {
    this.pending.set(conversationId, true);
  }

  takeAwaiting(conversationId: string): boolean {
    const v = this.pending.get(conversationId) ?? false;
    this.pending.delete(conversationId);
    return v;
  }

  clear(conversationId: string): void {
    this.pending.delete(conversationId);
  }
}

// ── Reply handling ───────────────────────────────────────────────────

export type CarReplyOutcome =
  | "dispatched" // command was sent to the box (task-spawning path)
  | "session-prompt" // prompt sent to the live session (/runner/session/turn)
  | "session-choice" // a menu choice was sent to the live session
  | "surface" // handled by a car-safe ops verb (meetings/mail/etc.)
  | "needs-confirm" // risky command stashed; awaiting confirm
  | "confirmed" // a previously-stashed risky command was released + dispatched
  | "cancelled" // a pending risky command was discarded
  | "ignored"; // empty / nothing actionable

export interface CarReplyDecision {
  outcome: CarReplyOutcome;
  /** The (possibly summarized) line to surface back to the driver. */
  reply: string;
  /** The command that was/would be dispatched, when applicable. */
  command?: string;
  /** Present when outcome is "dispatched" or "confirmed". */
  result?: CarVoiceResult;
  /** True when the session is showing a menu (the driver needs to answer). */
  awaitingChoice?: boolean;
}

export interface HandleCarReplyOpts {
  conversationId: string;
  text: string;
  gate: CarReplyGate;
  deps: CarVoiceDeps;
  config?: CarVoiceConfig;
  ops?: CarSurfaceOps;
  now?: () => number;
  /**
   * Optional: the /runner/session/turn transport. When present, `handleCarReply`
   * drives the LIVE coding session instead of spawning a new task. This is the
   * preferred path (docs/yaver-car-surface.md §7 build order #2).
   */
  sessionTurn?: SessionTurnDep;
  /**
   * Optional: tracks whether the last session turn left a menu on screen.
   * Required when `sessionTurn` is provided. The caller owns one instance for
   * the app's car surface.
   */
  sessionChoiceGate?: SessionChoiceGate;
}

/**
 * Process one captured car reply for a conversation.
 *
 * Two dispatch paths:
 *
 *   1. SESSION path (preferred, docs/yaver-car-surface.md §7): when
 *      `opts.sessionTurn` is provided, drive the LIVE coding session via
 *      POST /runner/session/turn. The `sessionChoiceGate` tracks whether the
 *      last turn left a menu on screen — if so, a spoken number/yes/no is
 *      routed as {choice} instead of {text}. The risk gate still applies to
 *      the initial transcript; the choice gate handles the follow-up.
 *
 *   2. TASK path (fallback): spawn a new task via dispatchAndSummarize. The
 *      never-read-code rule + one-sentence summary readback are inherited.
 *
 * Flow (shared risk gate, then path-specific dispatch):
 *   - empty                          → "ignored"
 *   - pending risk exists + confirm  → release + dispatch    → "confirmed"
 *   - pending risk exists + cancel   → discard                → "cancelled"
 *   - pending risk + anything else   → discard old, fall through
 *   - new risky command              → stash, ask to confirm  → "needs-confirm"
 *   - new safe command + sessionTurn → drive live session     → "session-prompt" / "session-choice"
 *   - new safe command, no sessionTurn → dispatch new task    → "dispatched"
 */
export async function handleCarReply(
  opts: HandleCarReplyOpts,
): Promise<CarReplyDecision> {
  const { conversationId, gate, deps, config } = opts;
  const text = (opts.text || "").trim();
  const now = opts.now ?? (() => Date.now());

  if (!text) return { outcome: "ignored", reply: "I didn't catch that." };

  // Resolve any pending risky command first (applies to both paths).
  if (gate.hasPending(conversationId)) {
    if (isConfirmReply(text)) {
      const command = gate.takePending(conversationId)!;
      const result = await dispatchAndSummarize(command, deps, config);
      return { outcome: "confirmed", reply: result.spoken, command, result };
    }
    if (isCancelReply(text)) {
      gate.clear(conversationId);
      return { outcome: "cancelled", reply: "Cancelled. Nothing was run." };
    }
    // Neither confirm nor cancel → treat the new utterance as a replacement
    // command (drop the stale pending one) and re-evaluate below.
    gate.clear(conversationId);
  }

  // Fresh command: gate risky verbs before dispatch (applies to both paths).
  if (isRiskyReply(text)) {
    gate.setPending(conversationId, text, now());
    return {
      outcome: "needs-confirm",
      command: text,
      reply: `That's a risky one — say "confirm" to run "${clampForSpeech(text)}", or "cancel".`,
    };
  }

  if (opts.ops) {
    const surface = await executeCarSurfaceIntent(text, opts.ops);
    if (surface.handled) {
      return { outcome: "surface", reply: surface.spoken, command: text };
    }
  }

  // SESSION path (preferred): drive the live coding session.
  if (opts.sessionTurn && opts.sessionChoiceGate) {
    const pendingChoice = opts.sessionChoiceGate.takeAwaiting(conversationId);
    const result = await dispatchSessionTurn(text, opts.sessionTurn, pendingChoice);
    if (result.awaitingChoice) {
      opts.sessionChoiceGate.setAwaiting(conversationId);
    }
    return {
      outcome: pendingChoice ? "session-choice" : "session-prompt",
      reply: result.spoken,
      command: text,
      awaitingChoice: result.awaitingChoice,
    };
  }

  // TASK path (fallback): spawn a new task.
  const result = await dispatchAndSummarize(text, deps, config);
  return { outcome: "dispatched", reply: result.spoken, command: text, result };
}

/** Trim a command to a short, glanceable-by-ear phrase for the confirm prompt. */
function clampForSpeech(s: string): string {
  const t = s.replace(/\s+/g, " ").trim();
  return t.length <= 80 ? t : t.slice(0, 79).trimEnd() + "…";
}
