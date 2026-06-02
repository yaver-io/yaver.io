// localAgent/voiceSession.ts — the orchestrator that ties the whole voice
// helper together: ONE spoken turn → understand → act → speak. PURE + RN-free
// by dependency injection (tsx-tested); the React component injects real
// speech.ts (STT/TTS), engine.ts (model), and adapterBindings (dispatch).
//
// Per-turn flow:
//   transcript
//     → (pending confirmation? resolve yes/no first)
//     → resolveDevice(transcript)                       [tie-safe]
//     → extractGoal(transcript)                         [keyword pre-pass]
//     → no goal + model present? model proposes a direct {action,...}
//         (grammar-constrained to the catalog, re-validated)            ── COMMAND path
//     → else capabilityLadder(state, goal) → nextStep / menu            ── LADDER path
//     → speak; CONFIRM-tier actions are held for an explicit spoken "yes".
//
// Works with NO model (scripted mode): keyword goals + the deterministic ladder
// already answer "what should I do" and run safe actions. The model only adds
// the free-form direct-command path. So this ships before llama.rn lands.

import { resolveDevice, type DeviceRef } from "./resolver";
import { extractGoal, type DispatchResult } from "./adapter";
import {
  capabilityLadder,
  isRemoteGoal,
  type LadderState,
  type NextStep,
  type SpineRung,
  type Capability,
} from "./capabilityLadder";
import { voiceInvokableActions, getAction, dispositionFor } from "./catalog";
import { buildToolCallGrammar, parseModelJson } from "./grammar";

/** Matches engine.ts LoadedModel.complete (only the bits we use). */
export type CompleteFn = (opts: {
  prompt: string;
  grammar?: string;
  maxTokens?: number;
}) => Promise<{ text: string }>;

export interface VoiceSessionDeps {
  /** Live device list (resolver shape) for spoken-ref resolution. */
  devices: () => DeviceRef[];
  /** Build the ladder state for an optionally-resolved target device. */
  ladderState: (targetDeviceId?: string) => LadderState | Promise<LadderState>;
  /** Run a catalog action (wraps adapter.dispatchAction in the component). */
  dispatch: (
    actionId: string,
    opts: { deviceId?: string; args?: Record<string, unknown> },
    confirmed: boolean,
  ) => Promise<DispatchResult>;
  /** Text-to-speech. */
  speak: (text: string) => void | Promise<void>;
  /** Grammar-constrained model completion (engine.complete). null → scripted. */
  complete?: CompleteFn | null;
}

export interface VoiceTurnResult {
  spoken: string;
  awaiting?: "confirm" | "device" | "goal";
  dispatched?: { actionId: string; ok: boolean };
  reached?: SpineRung;
}

interface Pending {
  actionId: string;
  deviceId?: string;
  args?: Record<string, unknown>;
  label: string;
}

// Order of evaluation matters: UNSURE (incl. "not sure", which contains the
// bare "sure") is checked BEFORE yes/no so it re-asks instead of mis-firing.
const UNSURE = /\b(not sure|unsure|maybe|dunno|don'?t know|hold on|wait)\b/i;
const NO = /\b(no|nope|nah|cancel|stop|don'?t|never ?mind|abort)\b/i;
const YES = /\b(yes|yeah|yep|yup|sure|ok|okay|go ahead|do it|please do|confirm)\b/i;

const refName = (d: DeviceRef) => d.alias || d.name;

/** Create a stateful voice session. The component calls handle() per utterance;
 *  the session holds the pending-confirmation state across turns. */
export function createVoiceSession(deps: VoiceSessionDeps) {
  let pending: Pending | null = null;

  const say = (spoken: string, extra: Partial<VoiceTurnResult> = {}): VoiceTurnResult => {
    void deps.speak(spoken);
    return { spoken, ...extra };
  };

  async function runAction(
    actionId: string,
    deviceId: string | undefined,
    args: Record<string, unknown> | undefined,
    narration: string,
  ): Promise<VoiceTurnResult> {
    const disp = dispositionFor(actionId);
    if (disp === "blocked" || disp === "unknown") {
      return say(`I can't do that by voice — open the app to ${describe(actionId)}.`);
    }
    if (disp === "confirm") {
      pending = { actionId, deviceId, args, label: narration || describe(actionId) };
      return say(`${narration || `I'll ${describe(actionId)}`}. Shall I go ahead?`, { awaiting: "confirm" });
    }
    // auto (READ_ONLY / SAFE_WRITE)
    const r = await deps.dispatch(actionId, { deviceId, args }, false);
    const tail = r.ok ? "" : ` (that didn't work: ${r.error ?? "unknown error"})`;
    return say((narration || "Done.") + tail, { dispatched: { actionId, ok: r.ok } });
  }

  async function narrateNext(ns: NextStep, deviceId?: string): Promise<VoiceTurnResult> {
    if (!ns.action) {
      const hint = ns.shellHint ? ` Run: ${ns.shellHint}.` : "";
      return say(ns.say + hint);
    }
    return runAction(ns.action, deviceId, undefined, ns.say);
  }

  async function handle(transcript: string): Promise<VoiceTurnResult> {
    const t = (transcript || "").trim();
    if (!t) return say("I didn't catch that.");

    // 1. Pending confirmation gates everything until resolved.
    if (pending) {
      if (!UNSURE.test(t) && NO.test(t)) {
        pending = null;
        return say("Okay, cancelled.");
      }
      if (!UNSURE.test(t) && YES.test(t)) {
        const p = pending;
        pending = null;
        const r = await deps.dispatch(p.actionId, { deviceId: p.deviceId, args: p.args }, true);
        return say(r.ok ? "Done." : `That didn't work: ${r.error ?? "unknown error"}.`, {
          dispatched: { actionId: p.actionId, ok: r.ok },
        });
      }
      return say(`Should I ${pending.label}? Please say yes or no.`, { awaiting: "confirm" });
    }

    // 2. Resolve a named device (if any).
    const devs = deps.devices();
    const res = resolveDevice(t, devs);
    const targetId = res.kind === "resolved" ? res.device.deviceId : undefined;

    // 3. COMMAND path: no clear goal + a model → let it propose a direct action.
    const goal = extractGoal(t);
    if (!goal && deps.complete) {
      const cmd = await proposeCommand(t, devs, deps.complete);
      if (cmd) {
        const did = targetId ?? resolveRef(cmd.deviceRef, devs);
        return runAction(cmd.action, did, cmd.args, "");
      }
    }

    // 4. LADDER path.
    const state = await deps.ladderState(targetId);
    const ladder = capabilityLadder(state, goal);

    if (goal && isRemoteGoal(goal) && !targetId && res.kind === "ambiguous") {
      return say(`Which one — ${res.candidates.map(refName).join(", ")}?`, { awaiting: "device" });
    }
    if (ladder.blocked?.reason === "device-unresolved") {
      return say("Which machine should I use?", { awaiting: "device" });
    }
    if (!ladder.nextStep) {
      if (!goal) return say(menuLine(ladder.available), { reached: ladder.reached });
      return say("You're all set. What should I do next?", { reached: ladder.reached });
    }
    return narrateNext(ladder.nextStep, targetId);
  }

  return {
    handle,
    /** Drop any pending confirmation (e.g. when the helper sheet closes). */
    reset() {
      pending = null;
    },
    /** Test/inspection: is a CONFIRM action awaiting approval? */
    isAwaitingConfirm() {
      return pending !== null;
    },
  };
}

// ── helpers (pure) ──────────────────────────────────────────────────

interface ProposedCommand {
  action: string;
  deviceRef?: string;
  args?: Record<string, unknown>;
}

async function proposeCommand(
  transcript: string,
  devs: DeviceRef[],
  complete: CompleteFn,
): Promise<ProposedCommand | null> {
  const actions = voiceInvokableActions();
  const grammar = buildToolCallGrammar(actions.map((a) => a.id));
  const prompt = buildVoicePrompt(transcript, devs.map(refName), actions);
  const out = await complete({ prompt, grammar, maxTokens: 128 }).catch(() => null);
  if (!out) return null;
  const parsed = parseModelJson<{ action?: string; deviceRef?: string; args?: unknown }>(out.text);
  if (!parsed || typeof parsed.action !== "string" || !getAction(parsed.action)) return null;
  return {
    action: parsed.action,
    deviceRef: typeof parsed.deviceRef === "string" ? parsed.deviceRef : undefined,
    args: parsed.args && typeof parsed.args === "object" && !Array.isArray(parsed.args)
      ? (parsed.args as Record<string, unknown>)
      : undefined,
  };
}

/** Instruction prompt for the direct-command path (the GBNF grammar enforces
 *  the JSON shape; this gives the model the catalog + device context). */
export function buildVoicePrompt(
  transcript: string,
  deviceRefs: string[],
  actions = voiceInvokableActions(),
): string {
  const catalog = actions.map((a) => `  ${a.id} — ${a.description}`).join("\n");
  return [
    "Translate one spoken command into a single structured action for a",
    'developer\'s device fleet. Output ONLY JSON: {"action": string, "deviceRef"?: string, "args"?: object}.',
    "",
    "Allowed actions (use the id verbatim):",
    catalog,
    "",
    deviceRefs.length
      ? `Known devices (deviceRef = one of these or a nickname): ${deviceRefs.join(", ")}`
      : "No devices known yet.",
    "",
    'If nothing fits, output {"action": "device.list"}.',
    "",
    `Command: ${transcript}`,
  ].join("\n");
}

function resolveRef(ref: string | undefined, devs: DeviceRef[]): string | undefined {
  if (!ref) return undefined;
  const r = resolveDevice(ref, devs);
  return r.kind === "resolved" ? r.device.deviceId : undefined;
}

/** A short imperative gloss of an action id for spoken confirmations. */
function describe(actionId: string): string {
  const a = getAction(actionId);
  if (!a) return actionId;
  // First clause of the catalog description, lowercased.
  return a.description.replace(/\.$/, "").split(/[.(]/)[0].trim().replace(/^[A-Z]/, (c) => c.toLowerCase());
}

function menuLine(available: Capability[]): string {
  const ready = available.filter((c) => c.ready).map((c) => c.label.toLowerCase());
  const invites = available.filter((c) => !c.ready).map((c) => c.label.toLowerCase());
  const parts: string[] = [];
  if (ready.length) parts.push(`You can ${joinList(ready)}.`);
  if (invites.length) parts.push(`I can also help you ${joinList(invites)}.`);
  return parts.join(" ") || "What would you like to do?";
}

function joinList(xs: string[]): string {
  if (xs.length <= 1) return xs[0] ?? "";
  if (xs.length === 2) return `${xs[0]} or ${xs[1]}`;
  return `${xs.slice(0, -1).join(", ")}, or ${xs[xs.length - 1]}`;
}
