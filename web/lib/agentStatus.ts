/**
 * The one status vocabulary, web side. Mirror of mobile/src/lib/agentStatus.ts.
 *
 * WHY THIS EXISTS: the colour was defined four times across the product and no
 * two agreed on what `completed` meant — green on the mobile Tasks screen, blue
 * in the mobile Home strip, grey in the web spatial fleet. Same task, three
 * colours, depending on which surface you happened to be looking at. All of them
 * hardcoded hex, bypassing the token layers that already held the right values.
 *
 * That is not cosmetic. A colour-coded surface is only worth building if you can
 * trust the colour without reading the label — the value is a reflex, and a
 * reflex trained against four palettes is trained against noise.
 *
 * WHY A MIRROR AND NOT AN IMPORT: web/ and mobile/ are separate packages with no
 * shared build. The two files must be changed together; the tests on both sides
 * assert the same mapping so a one-sided edit fails rather than drifts quietly.
 *
 * Colours resolve through the CSS custom properties in app/globals.css, which
 * already carry the same semantic values as mobile's theme/tokens.ts and are
 * defined for both themes. Nothing here hardcodes a hex except the WebGL
 * fallbacks, which say so.
 */

export type AgentState = "idle" | "working" | "blocked" | "healing" | "verified" | "failed" | "unknown";

export interface AgentSignal {
  state: AgentState;
  /** Actively burning tokens right now. Drives pulse/breathing. */
  pulse: boolean;
  /** We cannot confirm this is true — render as an outline, not a fill. */
  hollow: boolean;
  label: string;
}

export type TaskStatus = "queued" | "running" | "review" | "completed" | "failed" | "stopped";

/**
 * Semantic CSS variable for a state. `blocked` and `healing` share the warning
 * hue — both mean "not nominal" — and are told apart by pulse: healing breathes
 * because it is working the problem itself; blocked is still because it waits on
 * you. Kept identical to mobile's agentStateColor.
 */
export function agentStateVar(state: AgentState): string {
  switch (state) {
    case "working":
      return "--info";
    case "verified":
      return "--success";
    case "blocked":
    case "healing":
      return "--warning";
    case "failed":
      return "--danger";
    case "idle":
    case "unknown":
      return "--surface-400";
  }
}

/** For DOM styling — follows the theme automatically. */
export function agentStateCssColor(state: AgentState): string {
  return `rgb(var(${agentStateVar(state)}))`;
}

/** Soft/pill background for a state. */
export function agentStateCssSoft(state: AgentState): string {
  const v = agentStateVar(state);
  // The neutral ramp has no -soft pair; a low-alpha fill reads the same.
  if (v === "--surface-400") return "rgb(var(--surface-400) / 0.14)";
  return `rgb(var(${v}-soft))`;
}

/**
 * Resolved hex for contexts that CANNOT read CSS variables — chiefly WebGL. The
 * VR scene composites into Three.js materials, which take colours, not vars.
 *
 * Reads the live variable when there's a document, so the value still comes from
 * globals.css and stays theme-correct; the literals are a server-render/no-DOM
 * fallback and are the ONLY hardcoded hexes in this file. They must match the
 * light ramp in globals.css.
 */
const FALLBACK_HEX: Record<AgentState, string> = {
  working: "#2563EB", // --info
  verified: "#16A34A", // --success
  blocked: "#D97706", // --warning
  healing: "#D97706", // --warning
  failed: "#DC2626", // --danger
  idle: "#888780", // --surface-400
  unknown: "#888780", // --surface-400
};

export function agentStateHex(state: AgentState): string {
  if (typeof document === "undefined") return FALLBACK_HEX[state];
  const raw = getComputedStyle(document.documentElement).getPropertyValue(agentStateVar(state)).trim();
  // globals.css stores triplets ("22 163 74") so they can be alpha-composited.
  const parts = raw.split(/[\s,]+/).map(Number);
  if (parts.length < 3 || parts.some(Number.isNaN)) return FALLBACK_HEX[state];
  return "#" + parts.slice(0, 3).map((n) => Math.round(n).toString(16).padStart(2, "0")).join("");
}

/** True when the agent is asking for a human. */
export function agentNeedsYou(state: AgentState): boolean {
  return state === "blocked" || state === "failed";
}

/** True when the agent is done and will not change on its own. */
export function agentIsTerminal(state: AgentState): boolean {
  return state === "verified" || state === "failed" || state === "idle";
}

/**
 * Task status -> signal. Identical mapping to mobile.
 *
 * `queued` is working-but-hollow rather than its own colour: from the user's
 * side the machine has the work either way, and the distinction that matters —
 * is it spending right now — is exactly what fill carries.
 */
export function agentSignalFromTaskStatus(status: TaskStatus): AgentSignal {
  switch (status) {
    case "queued":
      return { state: "working", pulse: false, hollow: true, label: "queued" };
    case "running":
      return { state: "working", pulse: true, hollow: false, label: "running" };
    case "review":
      return { state: "blocked", pulse: false, hollow: false, label: "needs you" };
    case "completed":
      return { state: "verified", pulse: false, hollow: false, label: "done" };
    case "failed":
      return { state: "failed", pulse: false, hollow: false, label: "failed" };
    case "stopped":
      return { state: "idle", pulse: false, hollow: false, label: "stopped" };
  }
}

/**
 * The stable address a UI pins a fixed slot to. Never key a slot on list
 * position, or on anything derived from time.
 */
export function slotKeyForTask(task: { id: string }): string {
  return `task:${task.id}`;
}
