/**
 * The one status vocabulary. Every surface reads agent state from here.
 *
 * WHY THIS EXISTS: the colour was defined three times and the definitions
 * disagreed. On the Tasks screen `running` was blue and `completed` green; in
 * the Home session strip `running` was emerald and `completed` blue; the web
 * spatial bridge had a third copy. The same task was green on one screen and
 * blue on the next, and all three bypassed the token layer with hardcoded hex.
 *
 * That is not cosmetic. The whole value of a colour-coded surface is that you
 * perceive state without reading it — you glance and you know. A reflex trained
 * against three palettes is trained against noise, and every ambient surface we
 * build on top (watch complication, VR arc, TV wall, tab favicon) inherits the
 * lie and multiplies it. Centralising is the prerequisite for all of them, and
 * it is what makes retuning a hue a one-line change instead of a hunt.
 *
 * Colours come from ThemeColors, which reads theme/tokens.ts, which defines both
 * light and dark. Nothing here hardcodes a hex.
 */

import type { ThemeColors } from "../constants/colors";
import type { Task, TaskStatus } from "./quic";

/**
 * What an agent is doing, in the vocabulary a GLANCE needs — not the vocabulary
 * the backend happens to use. The question a user asks a status light is "does
 * this need me?", so states that answer differently must look different, and
 * states that answer the same may share.
 *
 * Six, not four. The Codex Micro ships white/blue/green/red because a macropad
 * is wired to a local app: it is never degraded-but-alive, and it always knows.
 * Yaver is neither. `healing` and `unknown` are the two states our own code
 * forced, and the list stops there on purpose — every extra colour costs the
 * user a lookup, which is the one thing this is meant to eliminate.
 */
export type AgentState =
  /** Seat exists, nothing running. */
  | "idle"
  /** A turn is in flight. Nothing is required of you. */
  | "working"
  /** Stopped and waiting on YOU. The only state that is a request. */
  | "blocked"
  /** Alive but degraded — self-healing. Look when convenient; don't act. */
  | "healing"
  /** Finished AND verified. Not merely quiet. */
  | "verified"
  /** Ended badly. Needs you. */
  | "failed"
  /** We lost contact. We are not claiming anything. */
  | "unknown";

/**
 * A state plus the two modifiers that carry nuance in FORM rather than hue.
 * Encoding "actively spending" and "unconfirmed" as motion and fill keeps the
 * palette at six while still distinguishing, e.g., queued from running.
 */
export interface AgentSignal {
  state: AgentState;
  /** Actively burning tokens right now. Drives the breathing animation. */
  pulse: boolean;
  /** We cannot confirm this is true — render as an outline, not a fill. */
  hollow: boolean;
  /** Short human label. Lowercase; callers case it as their surface needs. */
  label: string;
}

/**
 * Semantic colour for a state. Takes ThemeColors so light/dark and any future
 * theme come for free.
 *
 * `blocked` and `healing` deliberately share the warning hue: both mean "not
 * nominal". They are told apart by pulse — healing breathes because it is
 * working the problem itself, blocked is still because it is waiting on you. If
 * that proves too subtle in real use, blocked should take its own hue; being in
 * one module is what makes that a one-line change.
 */
export function agentStateColor(state: AgentState, colors: ThemeColors): string {
  switch (state) {
    case "working":
      return colors.info;
    case "verified":
      return colors.success;
    case "blocked":
    case "healing":
      return colors.warn;
    case "failed":
      return colors.error;
    case "idle":
    case "unknown":
      return colors.neutral;
  }
}

/** Soft/background variant, for chips and fills. */
export function agentStateBg(state: AgentState, colors: ThemeColors): string {
  switch (state) {
    case "working":
      return colors.infoBg;
    case "verified":
      return colors.successBg;
    case "blocked":
    case "healing":
      return colors.warnBg;
    case "failed":
      return colors.errorBg;
    case "idle":
    case "unknown":
      return colors.neutralBg;
  }
}

/** True when the agent is asking for a human. Drives badges, haptics, sorting. */
export function agentNeedsYou(state: AgentState): boolean {
  return state === "blocked" || state === "failed";
}

/** True when the agent is done and will not change on its own. */
export function agentIsTerminal(state: AgentState): boolean {
  return state === "verified" || state === "failed" || state === "idle";
}

/**
 * Task status -> signal.
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

export function agentSignalFromTask(task: Pick<Task, "status">): AgentSignal {
  return agentSignalFromTaskStatus(task.status);
}

/**
 * One autorun loop, as the agent reports it (ops verb `autorun_status`).
 * Mirrors autorunSessionView in desktop/agent/autorun_ops.go.
 */
export interface AutorunSession {
  id: string;
  /** Stable address, `task:seat` — see autorunSlotKey in autorun.go. */
  slot: string;
  task: string;
  status: "running" | "completed" | "failed" | "stopped" | "stopping";
  activeRunner?: string;
  /** Planning seat; absent on a single-runner loop. */
  master?: string;
  iterations?: number;
  commits?: number;
  finishReason?: string;
  /** Empty while the run has not finished, however quiet it looks. */
  finalCommit?: string;
  heals?: { iteration: number; kind: string; detail: string }[];
  progressTail?: string;
}

/**
 * How long a "running" loop may go without evidence before we stop claiming it
 * is running. Autorun kicks a runner for up to 30 minutes per turn, so silence
 * far shorter than that is normal; this is about a loop whose machine died, not
 * one that is thinking.
 */
const AUTORUN_STALE_MS = 45 * 60 * 1000;

/**
 * Autorun session -> signal.
 *
 * The `unknown` branch is the one that earns its place. A run that goes quiet
 * without recording a final commit has NOT converged — the agent's own contract
 * is that an empty finalCommit means the run did not finish, however quiet it
 * looks, and Yaver is distributed over relay/QUIC/LAN where online != reachable.
 * Showing green there would be a light that lies, which is worse than no light.
 */
export function agentSignalFromAutorun(session: AutorunSession, nowMs: number, lastSeenMs?: number): AgentSignal {
  switch (session.status) {
    case "failed":
      return { state: "failed", pulse: false, hollow: false, label: session.finishReason || "failed" };
    case "stopped":
      return { state: "idle", pulse: false, hollow: false, label: "stopped" };
    case "stopping":
      return { state: "idle", pulse: true, hollow: true, label: "stopping" };
    case "completed":
      // A finished loop with no final commit did not finish cleanly — the
      // marker is the whole way to tell a real ending from a quiet death.
      if (!session.finalCommit) {
        return { state: "unknown", pulse: false, hollow: true, label: "ended without a final commit" };
      }
      return { state: "verified", pulse: false, hollow: false, label: session.finishReason || "done" };
    case "running": {
      if (lastSeenMs !== undefined && nowMs - lastSeenMs > AUTORUN_STALE_MS) {
        return { state: "unknown", pulse: false, hollow: true, label: "no contact" };
      }
      if (healIsActive(session)) {
        const heal = session.heals![session.heals!.length - 1];
        return { state: "healing", pulse: true, hollow: false, label: healLabel(heal.kind) };
      }
      return { state: "working", pulse: true, hollow: false, label: autorunWorkingLabel(session) };
    }
  }
}

/**
 * A heal counts as current only while the iteration that raised it is the one
 * still running. Heals accumulate for the life of the run — treating the array
 * as "is healing" would leave a loop amber forever after one disk reclaim.
 */
function healIsActive(session: AutorunSession): boolean {
  if (!session.heals?.length || !session.iterations) return false;
  return session.heals[session.heals.length - 1].iteration >= session.iterations;
}

function healLabel(kind: string): string {
  switch (kind) {
    case "runner_failover":
      return "switching runner";
    case "disk_reclaim":
      return "reclaiming disk";
    case "cpu_backoff":
      return "waiting on cpu";
    default:
      return "self-healing";
  }
}

/** Names the seat that is working, which is the whole point of the split. */
function autorunWorkingLabel(session: AutorunSession): string {
  const iter = session.iterations ? `iteration ${session.iterations}` : "working";
  if (session.master && session.activeRunner) return `${iter} · ${session.master} → ${session.activeRunner}`;
  if (session.activeRunner) return `${iter} · ${session.activeRunner}`;
  return iter;
}

/**
 * The stable address a UI pins a fixed slot to.
 *
 * Autorun reports its own (`session.slot`). A plain task has no task file, so
 * its id is the stable thing — server-assigned and constant for that task's
 * life. Never key a slot on list position or on anything derived from time.
 */
export function slotKeyForAutorun(session: AutorunSession): string {
  return session.slot || `${session.task}:${session.activeRunner ?? "auto"}`;
}

export function slotKeyForTask(task: Pick<Task, "id">): string {
  return `task:${task.id}`;
}
