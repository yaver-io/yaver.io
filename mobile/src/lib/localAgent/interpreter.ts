// localAgent/interpreter.ts — turn an agent/LLM response message into
// contextual ACTION CHIPS (the "Debug" button pattern in the chat).
//
// Two paths, both feeding the same chip output:
//   1. DETERMINISTIC fast-path: known message patterns (crash/restart, runner
//      needs OAuth, auth expired, device offline, build/test/deploy failed,
//      approval prompt, rate-limited) map straight to chips — no model, instant.
//   2. LLM-ASSISTED path: for free-form text the fast-path doesn't recognize,
//      buildInterpretPrompt() hands the message + the allowed action catalog to
//      whichever brain is selected (brain.ts: remote agent if connected, else
//      the on-device model). The model returns chips constrained to real
//      action ids; we re-validate every one through the safety layer.
//
// PURE + RN-free (tsx-tested). The chat UI renders the chips; tapping one
// dispatches through the same catalog/disposition gate as the voice runtime,
// so a chip can NEVER trigger a BLOCKED action and CONFIRM actions still
// prompt. This is the screenshot's "Debug" chip, generalized — and it works
// for yaver-level runner OAuth (codex/claude/opencode) too.

import { dispositionFor, getAction, type Disposition } from "./catalog";

export interface ActionChip {
  /** Button label shown in the chat. */
  label: string;
  /** Catalog action id the chip dispatches. */
  actionId: string;
  /** How the runtime must treat it (auto / confirm). BLOCKED chips are dropped. */
  disposition: Exclude<Disposition, "blocked" | "unknown">;
  /** Optional device the action targets (resolved deviceId or a hint). */
  deviceRef?: string;
  /** Optional args (e.g. {runner:"codex"} for runner.install browser OAuth). */
  args?: Record<string, unknown>;
  /** A non-action chip that just reveals detail in-app (e.g. "Logs", "Debug"
   *  expanders) — dispatched as a UI intent, not a catalog action. */
  ui?: "logs" | "debug" | "context" | "retry";
}

export interface InterpretContext {
  /** Device the message came from, if known (passed through to chips). */
  deviceRef?: string;
  /** The runner in use on that device, if known (claude/codex/opencode). */
  runner?: "claude" | "codex" | "opencode";
}

export interface InterpretResult {
  /** Short plain-language gloss of what the message means (optional, TTS-friendly). */
  summary?: string;
  /** Action/UI chips to render under the message. */
  chips: ActionChip[];
  /** True when the deterministic layer didn't recognize the message and the
   *  caller should ask the selected brain (LLM) to interpret it. */
  needsLlm: boolean;
}

// Keep only safe, dispatchable action chips; drop BLOCKED/unknown, tag CONFIRM.
function safeChip(c: Omit<ActionChip, "disposition">): ActionChip | null {
  // UI-only chips bypass the catalog (they don't run remote actions).
  if (c.ui && !getAction(c.actionId)) {
    return { ...c, disposition: "auto" };
  }
  const d = dispositionFor(c.actionId);
  if (d === "blocked" || d === "unknown") return null;
  return { ...c, disposition: d };
}

function chips(...cs: Array<Omit<ActionChip, "disposition"> | null>): ActionChip[] {
  return cs.map((c) => (c ? safeChip(c) : null)).filter((c): c is ActionChip => !!c);
}

// ── Deterministic matchers ──────────────────────────────────────────
// Each returns an InterpretResult or null. Ordered most-specific first.

type Matcher = (msg: string, ctx: InterpretContext) => InterpretResult | null;

const reCrash = /agent (process )?crashed|restarting \(attempt|runner (crashed|exited|died)|process exited/i;
const reRunnerExhausted = /restarting \(attempt\s*([4-9]|\d{2,})\s*\/\s*\d+\)|all retries exhausted|giving up|runner down/i;
const reNeedsOAuth = /\b(needs?|requires?) (sign[- ]?in|auth|authentication|login|to sign in)\b|not (signed in|authenticated|authorized)|unauthorized|please (sign in|authenticate|log ?in)|run .*auth/i;
const reCodex = /codex/i;
const reClaude = /claude/i;
const reOpencode = /opencode/i;
const reAuthExpired = /(session|token|auth).{0,20}expired|expired.{0,20}(session|token|auth)|re[- ]?auth/i;
const reOffline = /offline|no recent heartbeat|not reachable|unreachable|power on|host is down/i;
const reBuildFail = /build failed|compilation (error|failed)|tsc .*error|gradle.*fail|xcodebuild.*error/i;
const reTestFail = /tests? failed|\d+ failing|assertion (failed|error)|test suite failed/i;
const reDeployFail = /deploy(ment)? failed|push (rejected|failed)|publish failed|upload failed/i;
const reRateLimited = /rate[- ]?limit|429|too many requests|usage limit/i;
const reApproval = /\b(approve|permission|allow|confirm|y\/n|proceed\?|do you want)\b/i;

const MATCHERS: Matcher[] = [
  // Runner crashed and exhausted retries — recover/reconnect is the move.
  (msg, ctx) => {
    if (!reRunnerExhausted.test(msg)) return null;
    return {
      summary: "The coding agent kept crashing and gave up. Recovering its session usually fixes it.",
      needsLlm: false,
      chips: chips(
        { label: "Reconnect", actionId: "device.recoverAuth", deviceRef: ctx.deviceRef },
        { label: "Restart agent", actionId: "recycle", deviceRef: ctx.deviceRef },
        { label: "Logs", actionId: "logs", ui: "logs", deviceRef: ctx.deviceRef },
      ),
    };
  },
  // Transient crash/restart in progress — offer Debug + Logs (the screenshot).
  (msg, ctx) => {
    if (!reCrash.test(msg)) return null;
    return {
      summary: "The agent crashed and is auto-restarting. You can watch logs or debug if it doesn't recover.",
      needsLlm: false,
      chips: chips(
        { label: "Debug", actionId: "debug", ui: "debug", deviceRef: ctx.deviceRef },
        { label: "Logs", actionId: "logs", ui: "logs", deviceRef: ctx.deviceRef },
        { label: "Restart agent", actionId: "recycle", deviceRef: ctx.deviceRef },
      ),
    };
  },
  // Runner needs OAuth / sign-in — yaver-level subscription OAuth (codex etc).
  (msg, ctx) => {
    if (!reNeedsOAuth.test(msg)) return null;
    const runner =
      ctx.runner ??
      (reCodex.test(msg) ? "codex" : reClaude.test(msg) ? "claude" : reOpencode.test(msg) ? "opencode" : undefined);
    const label = runner ? `Sign in ${runner === "claude" ? "Claude Code" : runner === "codex" ? "Codex" : "OpenCode"}` : "Sign in runner";
    return {
      summary: `The coding agent needs to sign in${runner ? ` (${runner})` : ""}. Start the subscription sign-in and approve it in the browser.`,
      needsLlm: false,
      chips: chips(
        { label, actionId: "runner.install", deviceRef: ctx.deviceRef, args: runner ? { runner, op: "browser_start" } : { op: "browser_start" } },
        { label: "Switch agent", actionId: "runner.switch", deviceRef: ctx.deviceRef },
      ),
    };
  },
  // Yaver agent auth expired.
  (msg, ctx) => {
    if (!reAuthExpired.test(msg)) return null;
    return {
      summary: "The machine's Yaver session expired. Reconnect to refresh it.",
      needsLlm: false,
      chips: chips({ label: "Reconnect", actionId: "device.recoverAuth", deviceRef: ctx.deviceRef }),
    };
  },
  // Device offline.
  (msg, ctx) => {
    if (!reOffline.test(msg)) return null;
    return {
      summary: "That machine looks offline. Power it on and run yaver serve, then retry.",
      needsLlm: false,
      chips: chips(
        { label: "Try again", actionId: "device.select", ui: "retry", deviceRef: ctx.deviceRef },
        { label: "Check status", actionId: "status", deviceRef: ctx.deviceRef },
      ),
    };
  },
  // Build failed.
  (msg, ctx) => {
    if (!reBuildFail.test(msg)) return null;
    return {
      summary: "The build failed. Re-run it, or open logs to see the first error.",
      needsLlm: false,
      chips: chips(
        { label: "Rebuild", actionId: "build", deviceRef: ctx.deviceRef },
        { label: "Logs", actionId: "logs", ui: "logs", deviceRef: ctx.deviceRef },
      ),
    };
  },
  // Tests failed.
  (msg, ctx) => {
    if (!reTestFail.test(msg)) return null;
    return {
      summary: "Some tests failed. Re-run the suite or view logs.",
      needsLlm: false,
      chips: chips(
        { label: "Re-run tests", actionId: "test", deviceRef: ctx.deviceRef },
        { label: "Logs", actionId: "logs", ui: "logs", deviceRef: ctx.deviceRef },
      ),
    };
  },
  // Deploy failed.
  (msg, ctx) => {
    if (!reDeployFail.test(msg)) return null;
    return {
      summary: "The deploy failed. You can retry the deploy or check logs.",
      needsLlm: false,
      chips: chips(
        { label: "Retry deploy", actionId: "deploy", deviceRef: ctx.deviceRef },
        { label: "Logs", actionId: "logs", ui: "logs", deviceRef: ctx.deviceRef },
      ),
    };
  },
  // Rate limited — don't hammer; just surface status + logs.
  (msg, ctx) => {
    if (!reRateLimited.test(msg)) return null;
    return {
      summary: "Hit a rate/usage limit. Wait a bit before retrying.",
      needsLlm: false,
      chips: chips({ label: "Check status", actionId: "status", deviceRef: ctx.deviceRef }),
    };
  },
];

/**
 * Deterministic interpretation. Returns recognized chips with needsLlm=false,
 * or an empty result with needsLlm=true for the caller to escalate to the
 * selected brain (LLM) via buildInterpretPrompt().
 */
export function interpretMessage(message: string, ctx: InterpretContext = {}): InterpretResult {
  const msg = (message || "").trim();
  if (!msg) return { chips: [], needsLlm: false };
  for (const m of MATCHERS) {
    const r = m(msg, ctx);
    if (r) return r;
  }
  // Approval prompts are intentionally last + conservative: surface the raw
  // prompt to the user with a follow-up affordance rather than auto-answering.
  if (reApproval.test(msg)) {
    return { summary: "The agent is asking for your approval.", chips: [], needsLlm: false };
  }
  return { chips: [], needsLlm: true };
}

/**
 * Prompt fragment for the LLM-assisted path. Constrains the model to emit
 * chips referencing ONLY the given allowed action ids. The caller injects the
 * voiceInvokableActions() list and runs this through the selected brain; every
 * returned chip is re-validated by safeChip() before rendering.
 */
export function buildInterpretPrompt(message: string, allowedActionIds: string[]): string {
  return [
    "You are a UI assistant. Read the agent message below and propose up to 3",
    "action buttons that help the user respond. Output JSON:",
    '{"summary": string, "chips": [{"label": string, "actionId": string, "deviceRef"?: string, "args"?: object}]}',
    "Rules:",
    `- actionId MUST be one of: ${allowedActionIds.join(", ")}`,
    "- Never invent an actionId. If nothing fits, return an empty chips array.",
    "- Labels are 1-3 words, imperative (e.g. \"Reconnect\", \"Sign in Codex\").",
    "",
    "Agent message:",
    message,
  ].join("\n");
}
