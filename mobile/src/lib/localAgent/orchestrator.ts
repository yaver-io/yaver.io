// localAgent/orchestrator.ts — the one entry point that ties the local-agent
// pieces into a single plan. PURE planning (RN-free, tsx-tested); the native
// engine call + action dispatch are injected so this stays testable.
//
// Flow for a user utterance / agent message:
//   1. selectBrain() — remote-first; use the connected box's LLM if available,
//      else the on-device model, else scripted.
//   2. If a device is referenced, resolveDevice() (deterministic, tie-safe).
//   3. Produce a PLAN: which brain answers, what grammar constrains it, what
//      the safety disposition is for any proposed action.
// The caller executes the plan: run the chosen brain (remote MCP or local
// engine.complete with the grammar), parse with grammar.parseModelJson, then
// gate the action through catalog.dispositionFor before dispatching.
//
// This module decides STRUCTURE, not side effects — so "what runs where, and
// is it safe" is unit-tested in one place.

import { selectBrain, type Brain, type ConnectivitySnapshot } from "./brain";
import { resolveDevice, type DeviceRef, type ResolveOutcome } from "./resolver";
import { dispositionFor, voiceInvokableActions, type Disposition } from "./catalog";
import { buildToolCallGrammar } from "./grammar";

export type IntentKind = "command" | "troubleshoot" | "sandbox-code";

export interface PlanInput {
  /** What the user said/typed, or the agent message to interpret. */
  utterance: string;
  /** What kind of request this is (router sets command/troubleshoot; sandbox UI sets sandbox-code). */
  intent: IntentKind;
  /** Connectivity for brain selection. */
  connectivity: ConnectivitySnapshot;
  /** Devices for resolution (when the utterance names one). */
  devices: DeviceRef[];
  /** A device hint extracted from the utterance, if any (else we try the whole utterance). */
  deviceHint?: string;
}

export interface Plan {
  brain: Brain;
  /** Resolved device, ambiguity, or none — only when the intent targets a device. */
  device?: ResolveOutcome;
  /** GBNF grammar to constrain a tool-routing model (command/troubleshoot). */
  grammar?: string;
  /** Action ids the model may emit (BLOCKED already excluded). */
  allowedActionIds: string[];
  /** Human/TTS note describing what will happen. */
  note: string;
}

/**
 * Build a plan. Does NOT run the model or dispatch — returns the structure the
 * caller executes. Remote-first brain, deterministic device resolution,
 * grammar-constrained allowed actions.
 */
export function planRequest(input: PlanInput): Plan {
  const brain = selectBrain(input.connectivity);

  // Device resolution only when the request targets one. For sandbox-code the
  // "device" is the phone itself, so we skip device resolution.
  let device: ResolveOutcome | undefined;
  if (input.intent !== "sandbox-code") {
    const q = input.deviceHint ?? input.utterance;
    if (q && input.devices.length > 0) device = resolveDevice(q, input.devices);
  }

  const allowedActionIds = voiceInvokableActions().map((a) => a.id);
  const grammar =
    input.intent === "sandbox-code" ? undefined : buildToolCallGrammar(allowedActionIds);

  const note = describe(brain, device, input.intent);
  return { brain, device, grammar, allowedActionIds, note };
}

function describe(brain: Brain, device: ResolveOutcome | undefined, intent: IntentKind): string {
  const where =
    brain.kind === "remote"
      ? "using the connected machine's AI"
      : brain.kind === "local"
        ? `using the on-device model (${brain.tier})`
        : "using built-in guidance";
  if (intent === "sandbox-code") return `Coding in the Sandbox ${where}.`;
  if (device?.kind === "ambiguous") return "More than one device matches — I'll ask which one.";
  if (device?.kind === "resolved") return `Working on ${device.device.name} ${where}.`;
  return `Helping ${where}.`;
}

export interface ProposedAction {
  action: string;
  deviceRef?: string;
  args?: Record<string, unknown>;
}

export type GateResult =
  | { allow: true; needsConfirm: boolean; action: ProposedAction }
  | { allow: false; reason: "blocked" | "unknown"; action: ProposedAction };

/**
 * The final safety gate every proposed action (from a model, a chip, or voice)
 * passes through before dispatch. Single source of truth — mirrors
 * catalog.dispositionFor but returns an executable verdict.
 */
export function gateAction(p: ProposedAction): GateResult {
  const d: Disposition = dispositionFor(p.action);
  if (d === "auto") return { allow: true, needsConfirm: false, action: p };
  if (d === "confirm") return { allow: true, needsConfirm: true, action: p };
  return { allow: false, reason: d === "blocked" ? "blocked" : "unknown", action: p };
}
