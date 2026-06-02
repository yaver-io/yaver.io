// localAgent/capabilityLadder.ts — the deterministic "what can I / should I do
// now?" walk for the voice helper. PURE + RN-free (unit-tested under tsx).
//
// "What should I do now?" is NOT an LLM question — it's a deterministic walk
// over a precondition graph. This module *computes* the answer (state + next
// action + menu); the on-device/remote model only *narrates* it. Same pattern
// as connectivity.ts ("diagnosis is deterministic so it's auditable").
//
// See DESIGN-capability-ladder-2026-06-01.md for the full spec. The graph:
//
//   SPINE (linear, monotonic, gates all remote work):
//     online → device-exists → reachable → agent-authed → connected
//   then FORKS (independent — a missing one never blocks the others):
//     CODING : runner-installed → runner-authed → project-present → selected
//              (+ git plane bookends it: clone-left, push-right)
//     HERMES : hermes-stack → dev-project   (live RN preview on THIS phone)
//     DEPLOY : = coding fork + deploy target
//   plus an ON-DEVICE branch (no spine): coder-tier → sandbox codegen.
//
// Lazy + goal-pulled: with no goal, nextStep is null ("what do you want to
// do?") and `available` is an invitation menu, never a setup checklist. With a
// goal, nextStep is the FIRST unmet rung on THAT goal's path only — off-path
// planes are never introduced (anti-nag). "Nothing set up yet" is a valid
// resting state, not a 7-step wizard.

import {
  diagnoseConnectivity,
  diagnoseRunnerAuth,
  actionIsDispatchable,
  type ConnDiagnosis,
  type RunnerId,
} from "./connectivity";
import { getAction } from "./catalog";
import type { ModelTier } from "./tiers";

export type Plane = "agent" | "runner" | "git";

/** Highest satisfied SPINE rung (forks live beyond "connected"). */
export type SpineRung =
  | "offline"
  | "no-device"
  | "unreachable"
  | "agent-unauthed"
  | "reachable" // reachable + agent-authed, but no live session yet
  | "connected";

/** What the user wants. Drives lazy introduction. Absent = no goal yet. */
export type Goal =
  | { kind: "ask" }
  | { kind: "connect"; deviceRef?: string }
  | { kind: "code"; deviceRef?: string; projectRef?: string; fresh?: boolean }
  | { kind: "push"; deviceRef?: string; projectRef?: string }
  | { kind: "preview"; deviceRef?: string }
  | { kind: "deploy"; deviceRef?: string; projectRef?: string }
  | { kind: "sandbox" };

/** Per-device facts the caller assembles from DeviceContext + device.audit. */
export interface DeviceFacts {
  deviceId: string;
  lifecycle:
    | "connected"
    | "ready-to-connect"
    | "bootstrap"
    | "yaver-auth-expired"
    | "offline";
  connected: boolean;
  manualAuthRequired?: boolean;
  /** Per-runner readiness (plane 2). */
  runners: Partial<Record<RunnerId, { installed: boolean; authed: boolean }>>;
  /** Git provider authed on the box (plane 3). v1 collapses providers → bool. */
  gitAuthed?: boolean;
  /** Mobile dev stack provisioned (Hermes fork). */
  hermesReady?: boolean;
  /** Projects registered on the box (userProjects; no paths). */
  projects: { slug: string; branch?: string }[];
  /** Selected project / active work dir. */
  activeProjectSlug?: string;
  /** Branch hygiene — optional; the `status` verb doesn't surface it yet. */
  activeProjectClean?: boolean;
  /** A deploy target is configured for the active project. */
  deployTargetConfigured?: boolean;
}

export interface LadderState {
  online: boolean;
  hasAnyDevice: boolean;
  reachableDeviceIds?: string[];
  /** The resolved target device (caller runs the resolver first). */
  device?: DeviceFacts;
  /** Highest local model tier (tiers.ts) → on-device sandbox availability. */
  localTier: ModelTier;
  lastError?: string | null;
}

export interface NextStep {
  /** Which rung is the gap (stable code for the runtime to branch on). */
  rung: string;
  /** Catalog action id to offer — guaranteed auto/confirm-dispatchable, or
   *  absent when the fix is guidance / handled by a dedicated UI flow. */
  action?: string;
  /** Spoken explanation (concise, TTS-friendly). */
  say: string;
  /** A command the user runs on their own computer/box, when that's the fix. */
  shellHint?: string;
  /** What introducing this rung sets up (telemetry / UI hinting). */
  provisions?: Plane | "project" | "hermes" | "runner";
}

export interface Capability {
  id: string;
  label: string;
  /** Unlocked NOW (true) vs an invitation to introduce it (false). */
  ready: boolean;
}

export interface LadderResult {
  reached: SpineRung;
  /** First gap on the goal's path; null with no goal or when satisfied. */
  nextStep: NextStep | null;
  /** Affirmative menu — ready items + ≤1-line invitations. */
  available: Capability[];
  /** Set when the walk can't proceed safely (e.g. device unresolved). */
  blocked?: { reason: string };
}

// ───────────────────────────────────────────────────────────────────────────
// Public entry point.
// ───────────────────────────────────────────────────────────────────────────

export function capabilityLadder(state: LadderState, goal?: Goal): LadderResult {
  const reached = spineReached(state);
  const available = surfaceMenu(reached, state);

  let nextStep: NextStep | null = null;
  let blocked: { reason: string } | undefined;

  if (goal) {
    if (isRemoteGoal(goal) && !state.device && state.hasAnyDevice) {
      // The goal needs a device but the caller hasn't resolved one. Don't guess.
      blocked = { reason: "device-unresolved" };
    } else {
      nextStep = guard(firstGapForGoal(goal, state), reached);
    }
  }

  return { reached, nextStep, available, blocked };
}

// ───────────────────────────────────────────────────────────────────────────
// Spine — single source of truth via diagnoseConnectivity, so we never drift.
// ───────────────────────────────────────────────────────────────────────────

function toConnInput(state: LadderState) {
  return {
    hasConnectedDevice: state.device?.connected ?? false,
    hasAnyDevice: state.hasAnyDevice,
    lifecycle: state.device?.lifecycle,
    manualAuthRequired: state.device?.manualAuthRequired,
    lastError: state.lastError,
    online: state.online,
  };
}

/** Highest satisfied spine rung, derived from the connectivity diagnosis. */
export function spineReached(state: LadderState): SpineRung {
  const code = diagnoseConnectivity(toConnInput(state)).code;
  switch (code) {
    case "offline":
      return "offline";
    case "no-devices":
      return "no-device";
    case "device-offline":
      return "unreachable";
    case "bootstrap":
    case "auth-expired":
    case "manual-auth":
      return "agent-unauthed";
    case "reachable-not-connected":
    case "unknown-error":
      return "reachable";
    case "ok":
      return "connected";
  }
}

function connToNextStep(conn: ConnDiagnosis): NextStep {
  return {
    rung: conn.code,
    action: conn.action,
    say: conn.say,
    shellHint: conn.shellHint,
    provisions: conn.action === "device.recoverAuth" ? "agent" : undefined,
  };
}

// ───────────────────────────────────────────────────────────────────────────
// Goal-pulled walk — first unmet rung on the goal's path only.
// ───────────────────────────────────────────────────────────────────────────

export function isRemoteGoal(goal: Goal): boolean {
  return goal.kind !== "ask" && goal.kind !== "sandbox";
}

function firstGapForGoal(goal: Goal, state: LadderState): NextStep | null {
  switch (goal.kind) {
    case "ask":
      return null; // needs nothing — already answerable.

    case "sandbox":
      return sandboxGap(state);

    case "connect":
      return spineGap(state); // satisfied iff connected.

    case "code": {
      return (
        spineGap(state) ??
        runnerGap(state) ??
        projectPresentGap(state, goal) ??
        projectSelectedGap(state)
      );
    }

    case "push": {
      return (
        spineGap(state) ??
        runnerGap(state) ??
        projectPresentGap(state, goal) ??
        projectSelectedGap(state) ??
        gitGap(state)
      );
    }

    case "deploy": {
      return (
        spineGap(state) ??
        runnerGap(state) ??
        projectPresentGap(state, goal) ??
        projectSelectedGap(state) ??
        gitGap(state) ??
        deployTargetGap(state)
      );
    }

    case "preview": {
      return spineGap(state) ?? hermesGap(state) ?? devProjectGap(state);
    }
  }
}

/** The spine, as a gap: null when connected, else the blocking connectivity step. */
function spineGap(state: LadderState): NextStep | null {
  const conn = diagnoseConnectivity(toConnInput(state));
  if (conn.code === "ok") return null;
  return connToNextStep(conn);
}

function runnerGap(state: LadderState): NextStep | null {
  const d = state.device;
  if (!d) return null;
  const rd = diagnoseRunnerAuth({ runners: d.runners });
  if (rd.code === "ok") return null;
  return {
    rung: `runner:${rd.code}`,
    action: rd.action,
    say: rd.say,
    provisions: "runner",
  };
}

function projectPresentGap(state: LadderState, goal: Goal): NextStep | null {
  const d = state.device;
  if (!d) return null;
  if (d.projects.length > 0 || d.activeProjectSlug) return null; // present

  const wantsFresh = goal.kind === "code" && goal.fresh === true;
  if (wantsFresh) {
    return {
      rung: "project-present",
      action: "project.new",
      say: "No project here yet — I'll scaffold a fresh one.",
      provisions: "project",
    };
  }
  // An existing repo is wanted but none is here → clone. A private clone needs
  // git auth first; surface that as the actionable step.
  if (!d.gitAuthed) {
    return {
      rung: "project-present",
      action: "git.connect",
      say: "No project on that machine yet. I'll connect your GitHub so we can clone one — open the link and enter the code.",
      provisions: "git",
    };
  }
  return {
    rung: "project-present",
    action: "project.new",
    say: "No project on that machine yet — I can clone a repo or start a fresh one.",
    provisions: "project",
  };
}

function projectSelectedGap(state: LadderState): NextStep | null {
  const d = state.device;
  if (!d) return null;
  if (d.activeProjectSlug) return null; // selected
  if (d.projects.length === 0) return null; // handled by projectPresentGap

  const names = d.projects
    .slice(0, 3)
    .map((p) => p.slug)
    .join(", ");
  return {
    rung: "project-selected",
    action: "project.select",
    say: `Which project should I use? You have ${names}.`,
    provisions: "project",
  };
}

function gitGap(state: LadderState): NextStep | null {
  const d = state.device;
  if (!d || d.gitAuthed) return null;
  return {
    rung: "git-authed",
    action: "git.connect",
    say: "To push, that machine needs to sign in to GitHub. I'll start it — open the link and enter the code.",
    provisions: "git",
  };
}

function deployTargetGap(state: LadderState): NextStep | null {
  const d = state.device;
  if (!d || d.deployTargetConfigured) return null;
  return {
    rung: "deploy-target",
    // No single dispatchable verb — target selection lives in the Deploy flow.
    say: "No deploy target is set for that project yet — open Deploy to pick one.",
  };
}

function hermesGap(state: LadderState): NextStep | null {
  const d = state.device;
  if (!d || d.hermesReady) return null;
  return {
    rung: "hermes-stack",
    // Provisioned via the remote "Fix this machine" Hermes flow (no single ops
    // verb) — the adapter wires it; we just narrate + hint.
    say: "That machine isn't set up to preview React Native apps on your phone yet. I can provision the Hermes stack.",
    provisions: "hermes",
  };
}

function devProjectGap(state: LadderState): NextStep | null {
  const d = state.device;
  if (!d) return null;
  if (d.activeProjectSlug) return null;
  if (d.projects.length === 0) {
    return {
      rung: "dev-project",
      action: "project.new",
      say: "No app to preview yet — I can start one or clone a repo.",
      provisions: "project",
    };
  }
  return projectSelectedGap(state);
}

function sandboxGap(state: LadderState): NextStep | null {
  if (state.localTier === "coder") return null; // ready, no machine needed
  return {
    rung: "coder-tier",
    say: "This phone can't run the on-device coder model. Pair a machine and I'll use its full power for coding.",
  };
}

// ───────────────────────────────────────────────────────────────────────────
// Guards — belt-and-suspenders before we hand an action to the runtime.
// ───────────────────────────────────────────────────────────────────────────

function guard(ns: NextStep | null, reached: SpineRung): NextStep | null {
  if (!ns || !ns.action) return ns;
  // 1. Never surface a non-dispatchable (BLOCKED/unknown) action.
  if (!actionIsDispatchable(ns.action)) return { ...ns, action: undefined };
  // 2. Reachability gate: a via:ops / via:mcp action can't run against a box we
  //    aren't connected to. Keep the guidance, drop the un-runnable action.
  const a = getAction(ns.action);
  if (a && (a.via === "ops" || a.via === "mcp") && reached !== "connected") {
    return { ...ns, action: undefined };
  }
  return ns;
}

// ───────────────────────────────────────────────────────────────────────────
// Affirmative menu — "what can I do now?". Monotonic in `reached`; framed as
// invitations (ready:false), never as missing prerequisites. Anti-nag: an
// off-path plane appears at most once.
// ───────────────────────────────────────────────────────────────────────────

export function surfaceMenu(reached: SpineRung, state: LadderState): Capability[] {
  const caps: Capability[] = [
    { id: "ask", label: "Ask me anything / troubleshoot", ready: true },
  ];

  // On-device branch is independent of the spine — only offered when runnable.
  if (state.localTier === "coder") {
    caps.push({ id: "sandbox", label: "Vibe-code on this phone (no machine)", ready: true });
  }

  if (reached === "offline") return caps;

  if (reached === "no-device" || reached === "unreachable") {
    caps.push({ id: "connect", label: "Pair / wake a computer", ready: false });
    return caps;
  }

  if (reached === "agent-unauthed" || reached === "reachable") {
    caps.push({ id: "connect", label: "Connect to that machine", ready: false });
    return caps;
  }

  // reached === "connected" — derive readiness from device facts.
  const d = state.device;
  const runnerReady = !!d && Object.values(d.runners).some((r) => r?.installed && r?.authed);
  const projectReady = !!d && !!d.activeProjectSlug;
  const gitReady = !!d && !!d.gitAuthed;
  const hermesReady = !!d && !!d.hermesReady;
  const deployReady = projectReady && gitReady && !!d?.deployTargetConfigured;

  if (!runnerReady) {
    caps.push({ id: "install-runner", label: "Install a coding agent", ready: false });
  }
  if (runnerReady && !projectReady) {
    caps.push({ id: "start-project", label: "Pick or start a project", ready: false });
  }
  if (runnerReady && projectReady) {
    caps.push(
      { id: "edit", label: "Edit / build / run / test", ready: true },
      { id: "push", label: gitReady ? "Commit & push" : "Connect GitHub to push", ready: gitReady },
      { id: "deploy", label: "Deploy", ready: deployReady },
    );
  }
  // Live RN preview is its own fork — ready when the Hermes stack exists, else
  // a single invitation line.
  caps.push({ id: "preview", label: "Preview an app live on this phone", ready: hermesReady });

  return caps;
}
