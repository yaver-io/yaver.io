/**
 * carVoiceConfirm.ts — the SCREEN-layer safety gate for the car voice
 * coding loop.
 *
 * `carVoiceCoding.ts` already refuses to *read code aloud* while driving
 * (isReadCodeRequest) but it does NOT gate *risky writes*: a raw transcript
 * like "deploy the web app" or "force push and delete the branch" would
 * otherwise dispatch straight to the box. That's exactly the class of command
 * a driver must NOT be able to fire by accident from a half-heard utterance.
 *
 * This module is the gate. It is deliberately a tiny, dependency-free pure
 * function so it can be unit-tested with `npx tsx` and reused from both the
 * spoken loop and any future hands-free entry point. The SCREEN decides how
 * to surface the confirmation (an on-screen modal + a spoken "Say confirm to
 * …" prompt); this file only decides *whether* a confirmation is required and
 * supplies the prompt text.
 *
 * We never rewrite carVoiceCoding.ts's core: the screen runs `needsConfirm()`
 * BEFORE calling runCarVoiceTurn/dispatchAndSummarize, and only proceeds once
 * the user has explicitly confirmed (tap + spoken "confirm"/"yes"/"do it").
 */

/** Categories of risk we gate. Kept coarse on purpose — the point is "stop and
 *  ask", not a fine-grained policy engine. */
export type RiskKind =
  | "deploy"
  | "push"
  | "delete"
  | "force"
  | "reset"
  | "prod"
  | "storage"
  | "kill";

export interface RiskAssessment {
  /** True when the command must be explicitly confirmed before dispatch. */
  risky: boolean;
  /** Which risk patterns matched (for the prompt + audit). */
  kinds: RiskKind[];
  /** One short sentence to show on-screen AND speak before dispatch. */
  prompt: string;
}

// Each pattern is intentionally conservative — it should fire on the obvious
// destructive/irreversible verbs and NOT on routine coding ("add a test",
// "fix the build", "rename the handler"). Word-boundaried so "redeploy" and
// "deployment" still match deploy, but "deltas" doesn't match "delete".
const RISK_PATTERNS: { kind: RiskKind; re: RegExp }[] = [
  { kind: "force", re: /\bforce[ -]?push(es|ed|ing)?\b|\b--force\b|\bforce\b/ },
  { kind: "push", re: /\b(git )?push(es|ed|ing)?\b/ },
  { kind: "deploy", re: /\b(re)?deploy(s|ed|ing|ment)?\b|\bship (it|to)\b|\brelease\b|\brollout\b/ },
  { kind: "delete", re: /\b(delete|remove|destroy|drop|wipe|rm)\b|\brm -rf\b/ },
  { kind: "reset", re: /\b(reset|revert|rollback|roll back|hard reset)\b/ },
  { kind: "prod", re: /\b(prod|production|live|mainnet)\b/ },
  // storage_reclaim deletes files off the box. The plain "delete" pattern
  // above misses every natural way a driver phrases it ("clean up my disk",
  // "free up some space"), so match the reclaim verbs directly, plus the
  // soft verbs when they're aimed at a storage noun.
  {
    kind: "storage",
    re: /\b(reclaim|purge|prune)\b|\b(clean|clear|free|empty)\b[^.!?]{0,24}\b(disk|storage|space|cache|caches|trash|junk|build artifacts?)\b/,
  },
  // proc_kill terminates a live process. "kill" is not in any pattern above.
  {
    kind: "kill",
    re: /\b(kill|terminate|force[ -]?quit|sigkill|pkill)\b/,
  },
];

/**
 * Assess a transcript for risk. Returns which categories matched and a single
 * spoken/visible confirmation prompt. Pure + deterministic.
 */
export function assessRisk(transcript: string): RiskAssessment {
  const t = (transcript || "").toLowerCase();
  const kinds: RiskKind[] = [];
  for (const { kind, re } of RISK_PATTERNS) {
    if (re.test(t) && !kinds.includes(kind)) kinds.push(kind);
  }
  const risky = kinds.length > 0;
  return { risky, kinds, prompt: confirmPrompt(transcript, kinds) };
}

/** Convenience boolean for call sites that don't need the detail. */
export function needsConfirm(transcript: string): boolean {
  return assessRisk(transcript).risky;
}

/**
 * The one-sentence prompt shown on-screen and spoken before a risky dispatch.
 * Names the action so the driver knows exactly what they're authorizing, and
 * tells them how to confirm by voice.
 */
export function confirmPrompt(transcript: string, kinds: RiskKind[]): string {
  if (kinds.length === 0) return "";
  const label = describeKinds(kinds);
  return `That looks like a ${label} command — say "confirm" or tap Confirm to run it.`;
}

function describeKinds(kinds: RiskKind[]): string {
  const human: Record<RiskKind, string> = {
    deploy: "deploy",
    push: "push",
    delete: "delete",
    force: "force-push",
    reset: "reset",
    prod: "production",
    storage: "disk-cleanup",
    kill: "process-kill",
  };
  const labels = kinds.map((k) => human[k]);
  if (labels.length === 1) return labels[0];
  return labels.slice(0, -1).join(", ") + " / " + labels[labels.length - 1];
}

// Spoken-confirmation matcher. When the user answers a confirm prompt by
// voice, the screen transcribes their reply and runs it through this. We keep
// it strict-ish: a clear affirmative confirms; anything ambiguous does NOT.
const AFFIRM = /\b(confirm|confirmed|yes|yeah|yep|do it|go ahead|proceed|send it|affirmative)\b/;
const NEGATE = /\b(no|nope|cancel|stop|don'?t|abort|never ?mind|negative)\b/;

/**
 * Interpret a spoken reply to a confirmation prompt.
 *   - "confirmed"        → "confirm"
 *   - "no, cancel that"  → "cancel"  (negation wins over any stray affirmative)
 *   - "uh, maybe"        → "unclear" (caller must re-ask or default to cancel)
 */
export function interpretConfirmReply(reply: string): "confirm" | "cancel" | "unclear" {
  const t = (reply || "").toLowerCase();
  if (NEGATE.test(t)) return "cancel";
  if (AFFIRM.test(t)) return "confirm";
  return "unclear";
}
