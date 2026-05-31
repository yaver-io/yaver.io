// Mobile Shortcuts — Convex-synced, one-tap action chains.
//
// Storage lives in Convex (`userShortcuts` table) so shortcuts roam
// across a user's phones and survive reinstall. Privacy contract: a
// shortcut step carries ONLY a deviceId, a project slug, and flags/labels
// — never an absolute path or task-prompt text (see backend/convex/
// schema.ts + shortcuts.ts). The chain itself runs client-side on the
// phone via runShortcut.ts.

import { getConvexSiteUrl } from "./auth";

export type ShortcutStepKind =
  | "select-device" // connect this phone's client to a dev machine
  | "open-project" // load a project onto the phone (Hermes) via the Projects tab
  | "start-dev" // switch the dev box to a project + start its dev server
  | "hermes-reload"; // push a fresh Hermes bundle to the phone

export interface ShortcutStep {
  kind: ShortcutStepKind;
  /** Target device uuid. Device-dependent steps stamp this so the chain
   *  is deterministic even before focus has propagated. */
  deviceId?: string;
  /** Display label for the device (the resolved deviceId can roam). */
  deviceName?: string;
  /** Project filesystem basename — never a path. */
  projectSlug?: string;
  /** hermes-reload mode. */
  mode?: "dev" | "bundle";
  /** start-dev framework hint (expo | vite | nextjs | flutter | …). */
  framework?: string;
  /** Optional human label shown in the step list. */
  label?: string;
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
    case "hermes-reload":
      return step.mode === "dev" ? "Hot reload (Metro)" : "Hermes bundle reload";
    default:
      return step.label || (step as ShortcutStep).kind;
  }
}
