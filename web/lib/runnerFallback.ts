/**
 * runnerFallback — "this failed; which runner can actually fix it, right now?"
 *
 * ── Why this exists ────────────────────────────────────────────────────────
 *
 * When a task fails, Yaver offers "Fix with <runner>". It offered the SAME
 * runner that just failed, including when the failure was that runner's own
 * sign-in. So a Codex OAuth expiry produced a Fix button that dispatched Codex,
 * which failed for the identical reason, which offered the button again.
 *
 * Measured on the owner's fleet (2026-08-02, read from the live device rows):
 *
 *   magara   claude  ready  authSource "claude.ai · max"
 *            codex   NOT ready — no credentials found at all
 *            opencode ready  authSource "GLM API key"
 *   ubuntu   claude  ready  "claude.ai · max"
 *            codex   ready  "codex login status"   ← token actually dead
 *            opencode ready
 *
 * Two things fall out of that table, and they are the whole design:
 *
 * 1. THE AUTH MECHANISM DECIDES THE FAILURE MODES. A runner backed by an API
 *    key (`GLM API key`, an env-var provider) has NO OAuth to expire. It cannot
 *    fail with token_expired, refresh_token_reused, or a revoked grant. A
 *    subscription-OAuth runner (`claude.ai · max`, `codex login status`) can —
 *    and worse, it can report a healthy-looking credential while the provider
 *    has already stopped accepting it, which is exactly what ubuntu's codex row
 *    did while every task 400'd.
 *
 *    So when the failure IS an OAuth failure, an API-key runner is not merely
 *    "another option" — it is structurally immune to the thing that just broke.
 *    That is a real ranking signal, not a preference.
 *
 * 2. READINESS IS PER BOX. Codex is fine on ubuntu and has no credentials at
 *    all on magara. A fallback chosen globally would send the fix to a runner
 *    that cannot start on the machine the task runs on.
 *
 * ── What this module refuses to do ─────────────────────────────────────────
 *
 * It does not claim a runner WILL succeed. `authVerified` is false on every
 * runner on every box in the fleet above — nothing has been proven by use — so
 * any confident "this one works" would be the same false green this codebase
 * keeps paying for. It ranks by what can be RULED OUT, and says which.
 */

import type { RunnerFailureKind } from "./runnerFailure";

/** How a runner proves itself to its provider. */
export type RunnerAuthMechanism =
  /** An API key (z.ai / GLM, OpenRouter, an env-var provider). No OAuth, so no
   *  OAuth expiry — the key is either accepted or it is not. */
  | "api-key"
  /** A consumer subscription OAuth grant (claude.ai, ChatGPT). Expires, can be
   *  revoked server-side, and can look valid locally while already dead. */
  | "subscription-oauth"
  /** Nothing reported — treat as unknown, never as either of the above. */
  | "unknown";

export interface RunnerState {
  id: string;
  installed?: boolean;
  ready?: boolean;
  authConfigured?: boolean;
  authPresent?: boolean;
  authVerified?: boolean;
  /** The agent's own label, e.g. "GLM API key", "claude.ai · max". */
  authSource?: string | null;
  warning?: string | null;
  error?: string | null;
}

/**
 * Infer the mechanism from the agent's `authSource` label.
 *
 * Deliberately conservative: an unrecognised label is `unknown`, never guessed
 * into one of the two real buckets. Mislabelling an OAuth runner as api-key
 * would make us recommend it precisely when OAuth is the thing that broke.
 */
export function runnerAuthMechanism(state: Pick<RunnerState, "authSource" | "id">): RunnerAuthMechanism {
  const s = String(state.authSource || "").toLowerCase();
  if (!s) return "unknown";
  if (/\bapi[- ]?key\b|apikey|env var|environment|\bkey\b/.test(s)) return "api-key";
  // Provider auth stores that hold a key rather than an OAuth grant.
  if (/auth\.json/.test(s) && /opencode/.test(s)) return "api-key";
  if (/claude\.ai|chatgpt|login status|oauth|subscription|\bmax\b|\bpro\b|\bplus\b/.test(s)) return "subscription-oauth";
  return "unknown";
}

/** Failure kinds that are specifically about an OAuth grant going bad. */
const OAUTH_FAILURE_KINDS: ReadonlySet<string> = new Set(["auth", "auth-revoked"]);

/** Failure kinds no change of runner can fix — they are about the MACHINE. */
const RUNNER_AGNOSTIC_KINDS: ReadonlySet<string> = new Set([
  "project-missing",
  "relay-presence",
  "relay-route",
  "relay-auth",
  "agent-verb-skew",
]);

export interface FixCandidate {
  runner: string;
  /** Why this one, in words a user can check against the screen. */
  why: string;
  /** True when the candidate is structurally immune to the failure that just
   *  happened (not merely "different"). */
  immune: boolean;
}

export interface FixPlan {
  /** The runner to attempt the fix with, or null when none can. */
  candidate: FixCandidate | null;
  /** Rendered when candidate is null: what the user must do instead. */
  blocked: string | null;
  /** True when the failing thing is not a runner problem at all. */
  runnerAgnostic: boolean;
}

function usable(r: RunnerState): boolean {
  if (r.installed === false) return false;
  if (r.ready === false) return false;
  // authConfigured:false is the agent stating there is no usable credential.
  if (r.authConfigured === false) return false;
  return true;
}

function label(id: string): string {
  const n = id.toLowerCase();
  if (n === "claude") return "Claude Code";
  if (n === "codex") return "OpenAI Codex";
  if (n === "opencode") return "OpenCode";
  return id;
}

/**
 * Choose the runner to offer as "Fix with …".
 *
 * @param failedRunner the runner that just failed (never re-offered for a
 *        failure that is about that runner's own credential or entitlement)
 * @param kind        the classified failure
 * @param runners     the runner rows for THE MACHINE the task will run on
 */
export function planRunnerFix(
  failedRunner: string | null | undefined,
  kind: RunnerFailureKind | string | null | undefined,
  runners: readonly RunnerState[],
): FixPlan {
  const k = String(kind || "");
  if (RUNNER_AGNOSTIC_KINDS.has(k)) {
    return {
      candidate: null,
      runnerAgnostic: true,
      blocked:
        "This is a machine problem, not a coding problem — no runner can fix it. Use the route offered above.",
    };
  }

  const failed = String(failedRunner || "").toLowerCase();
  const oauthBroke = OAUTH_FAILURE_KINDS.has(k);
  // A model-entitlement refusal is specific to the failing runner's ACCOUNT, so
  // another runner is a legitimate escape from it too.
  const accountBound = oauthBroke || k === "model-not-supported" || k === "billing";

  const pool = runners.filter((r) => {
    if (!usable(r)) return false;
    // Never re-offer the runner that just failed for a reason that is about
    // that runner. For a plain build error it is fine — and correct — to retry
    // with the same one.
    if (accountBound && String(r.id).toLowerCase() === failed) return false;
    return true;
  });

  if (!pool.length) {
    const failedRow = runners.find((r) => String(r.id).toLowerCase() === failed);
    const mech = failedRow ? runnerAuthMechanism(failedRow) : "unknown";
    return {
      candidate: null,
      runnerAgnostic: false,
      blocked: accountBound
        ? mech === "subscription-oauth"
          ? `No other runner is ready on this machine, so ${label(failed)}'s sign-in has to be repaired before anything can run here. Open an encrypted terminal to this box and use the runner's native sign-in command.`
          : `No other runner is ready on this machine. Fix ${label(failed) || "the runner"}'s credential, or install another runner here.`
        : "No runner is ready on this machine — install or sign one in before retrying.",
    };
  }

  // Rank. An API-key runner is FIRST when OAuth is what broke, because it
  // cannot have the failure mode at all. Otherwise prefer a proven credential,
  // then a present one, then anything usable.
  const scored = pool
    .map((r) => {
      const mech = runnerAuthMechanism(r);
      let score = 0;
      let why = "";
      let immune = false;
      if (accountBound && mech === "api-key") {
        score += 100;
        immune = true;
        why = `${label(r.id)} on this machine authenticates with an API key${r.authSource ? ` (${r.authSource})` : ""}, so it cannot hit the ${oauthBroke ? "sign-in" : "account"} problem that just stopped ${label(failed) || "the other runner"}.`;
      } else if (mech === "api-key") {
        score += 20;
        why = `${label(r.id)} is ready on this machine and uses an API key, which has no sign-in to expire.`;
      }
      if (r.authVerified === true) {
        score += 40;
        if (!why) why = `${label(r.id)} is the only runner here whose credential has actually been exercised successfully.`;
      } else if (r.authPresent === true) {
        score += 10;
      }
      if (!why) why = `${label(r.id)} is installed and ready on this machine.`;
      return { runner: r.id, why, immune, score };
    })
    .sort((a, b) => b.score - a.score);

  const best = scored[0];
  return {
    candidate: { runner: best.runner, why: best.why, immune: best.immune },
    runnerAgnostic: false,
    blocked: null,
  };
}

/**
 * The button label. Never "Fix with <the thing that just failed>".
 */
export function fixButtonLabel(plan: FixPlan): string | null {
  if (!plan.candidate) return null;
  return `Fix with ${label(plan.candidate.runner)}`;
}
