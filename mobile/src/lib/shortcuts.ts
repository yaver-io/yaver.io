// Mobile Shortcuts — Convex-synced, one-tap action chains.
//
// Storage lives in Convex (`userShortcuts` table) so shortcuts roam
// across a user's phones and survive reinstall. Privacy contract: a
// shortcut step carries ONLY a deviceId, a project slug, small robot
// action parameters, and flags/labels — never an absolute path,
// task-prompt text, logs, or captured media (see backend/convex/schema.ts
// + shortcuts.ts). The chain itself runs client-side on the phone via
// runShortcut.ts.

import { getConvexSiteUrl } from "./auth";

export type ShortcutStepKind =
  | "select-device" // connect this phone's client to a dev machine
  | "open-project" // load a project onto the phone (Hermes) via the Projects tab
  | "start-dev" // switch the dev box to a project + start its dev server
  | "hermes-reload" // push a fresh Hermes bundle to the phone
  | "open-robot" // select a robot cell and open the Robot tab
  | "robot-action"; // drive a robot cell over the mesh

export type RobotShortcutAction =
  | "status"
  | "home"
  | "jog"
  | "tool"
  | "screw"
  | "program-run"
  | "estop"
  | "reset";

export interface ShortcutStep {
  kind: ShortcutStepKind;
  /** Target device uuid. Device-dependent steps stamp this so the chain
   *  is deterministic even before focus has propagated. */
  deviceId?: string;
  /** Display label for the device (the resolved deviceId can roam). */
  deviceName?: string;
  /** Project filesystem basename — never a path. */
  projectSlug?: string;
  /** hermes-reload mode. Always "bundle" (full) now — the Metro/dev
   *  fast-path was dropped from the editor; kept optional for old rows. */
  mode?: "dev" | "bundle";
  /** start-dev framework hint (expo | vite | nextjs | flutter | …). */
  framework?: string;
  /** Agent runner id (claude / codex / opencode) to preset on the target
   *  device when the chain runs. Empty/undefined = off (leave the device's
   *  current agent alone). Flags only — privacy-safe, no prompt text. */
  runner?: string;
  /** Model id for the chosen runner (e.g. gpt-5.5, sonnet). */
  model?: string;
  /** Display label for the chosen agent+model, e.g. "Codex · GPT-5.5". */
  runnerLabel?: string;
  /** Optional human label shown in the step list. */
  label?: string;
  /** Robot shortcut action. Params below are privacy-safe machine flags
   *  only: no paths, no prompts, no raw camera frames. */
  robotAction?: RobotShortcutAction;
  verify?: "agent" | "frames" | "off";
  axis?: "X" | "Y" | "Z";
  distanceMm?: number;
  feed?: number;
  toolOn?: boolean;
  programName?: string;
  targetTorqueNmm?: number;
  x?: number;
  y?: number;
}

export interface Shortcut {
  _id: string;
  name: string;
  icon?: string;
  color?: string;
  order: number;
  steps: ShortcutStep[];
  updatedAt: number;
}

export interface ShortcutInput {
  id?: string;
  name: string;
  icon?: string;
  color?: string;
  order?: number;
  steps: ShortcutStep[];
}

/** Fetch the caller's shortcuts (ordered for the grid). Returns [] on any
 *  failure so the screen can render an empty state rather than throw. */
export async function listShortcuts(token: string): Promise<Shortcut[]> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/shortcuts`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return [];
    const data = await res.json();
    return (data.shortcuts || []) as Shortcut[];
  } catch {
    return [];
  }
}

/** Create (no id) or update (with id) a shortcut. Returns the row id.
 *  Throws on failure so the editor can surface a visible error. */
export async function saveShortcut(token: string, input: ShortcutInput): Promise<string | null> {
  const res = await fetch(`${getConvexSiteUrl()}/shortcuts`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    const text = await res.text().catch(() => `HTTP ${res.status}`);
    throw new Error(text || `HTTP ${res.status}`);
  }
  const data = await res.json();
  return data.id ?? null;
}

/** Delete a shortcut. Best-effort — the backend no-ops if it isn't yours. */
export async function deleteShortcut(token: string, id: string): Promise<void> {
  await fetch(`${getConvexSiteUrl()}/shortcuts/delete`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ id }),
  }).catch(() => {});
}

/** One-line human summary of a step, for the card + step list. */
export function describeStep(step: ShortcutStep): string {
  switch (step.kind) {
    case "select-device":
      return `Connect to ${step.deviceName || "device"}`;
    case "open-project":
      return `Open ${step.projectSlug || "project"} on phone`;
    case "start-dev":
      return `Start dev server for ${step.projectSlug || "project"}`;
    case "hermes-reload": {
      const base = step.projectSlug ? `Reload ${step.projectSlug}` : "Hermes bundle reload";
      return step.runnerLabel ? `${base} · ${step.runnerLabel}` : base;
    }
    case "open-robot":
      return `Open ${step.deviceName || "robot"} controls`;
    case "robot-action":
      return describeRobotStep(step);
    default:
      return step.label || (step as ShortcutStep).kind;
  }
}

function describeRobotStep(step: ShortcutStep): string {
  const robot = step.deviceName || "robot";
  switch (step.robotAction) {
    case "status":
      return `Check ${robot} status`;
    case "home":
      return `Home ${robot}`;
    case "jog":
      return `Jog ${robot} ${step.axis || "X"} ${signed(step.distanceMm ?? 10)}mm`;
    case "tool":
      return `${step.toolOn === false ? "Turn off" : "Turn on"} ${robot} tool`;
    case "screw":
      return `Drive screw${step.targetTorqueNmm ? ` to ${step.targetTorqueNmm} Nmm` : ""}`;
    case "program-run":
      return `Run ${step.programName || "robot program"}`;
    case "estop":
      return `E-stop ${robot}`;
    case "reset":
      return `Reset ${robot}`;
    default:
      return step.label || "Robot action";
  }
}

function signed(n: number): string {
  return n > 0 ? `+${n}` : `${n}`;
}
