// localAgent/connectivity.ts — first-level connectivity + runner-OAuth
// triage for the voice helper. PURE + RN-free (unit-tested under tsx).
//
// This encodes the "first-class support for connectivity and OAuth of the
// coding runners (claude-code / codex / opencode)" the helper must guide
// users through — driven by whichever brain selectBrain() chose, but the
// *diagnosis + next action* is deterministic so it's correct and auditable.
//
// Inputs come from existing app state the audit already mapped:
//   - DeviceContext: connectionStatus, lastError, needsAuth, agentAuthExpired,
//     manualAuthRequiredDeviceIds, online/lastSeen
//   - quicClient / device.audit: per-runner auth state
// The voice runtime maps those into the snapshots below, calls these pure
// functions, and speaks the `say` line + offers the `action`.

import { dispositionFor } from "./catalog";

export type RunnerId = "claude" | "codex" | "opencode";

export interface ConnDiagnosisInput {
  /** Is any device currently connected/usable? */
  hasConnectedDevice: boolean;
  /** Does the user have ANY registered device at all? */
  hasAnyDevice: boolean;
  /** Target device (if the user named one) — its derived lifecycle. */
  lifecycle?: "connected" | "ready-to-connect" | "bootstrap" | "yaver-auth-expired" | "offline";
  /** Target needs manual `yaver auth` (auto-pair exhausted). */
  manualAuthRequired?: boolean;
  /** Last transport error string, if any. */
  lastError?: string | null;
  /** Network present at all. */
  online?: boolean;
}

export interface ConnDiagnosis {
  /** Stable code for the runtime to branch on. */
  code:
    | "ok"
    | "offline"
    | "no-devices"
    | "device-offline"
    | "bootstrap"
    | "auth-expired"
    | "manual-auth"
    | "reachable-not-connected"
    | "unknown-error";
  /** Spoken explanation (concise, TTS-friendly). */
  say: string;
  /** The catalog action id to offer next (must be auto/confirm-safe). */
  action?: string;
  /** When the fix is on the user's computer, the command to run there. */
  shellHint?: string;
}

/**
 * Diagnose a connectivity problem and return the single best next action.
 * Order matters: most-blocking conditions first.
 */
export function diagnoseConnectivity(i: ConnDiagnosisInput): ConnDiagnosis {
  if (i.online === false) {
    return {
      code: "offline",
      say: "Your phone looks offline. Reconnect to Wi-Fi or cellular and I'll try again.",
    };
  }
  if (!i.hasAnyDevice) {
    return {
      code: "no-devices",
      say: "You haven't paired a computer yet. Install Yaver on your machine and sign in, then I'll find it.",
      shellHint: "npm install -g yaver-cli && yaver auth",
    };
  }
  if (i.hasConnectedDevice && i.lifecycle === "connected") {
    return { code: "ok", say: "You're connected. What would you like to do?" };
  }
  switch (i.lifecycle) {
    case "yaver-auth-expired":
      return {
        code: "auth-expired",
        say: "That machine is up but its Yaver session expired. I can reconnect it now.",
        action: "device.recoverAuth",
      };
    case "bootstrap":
      return {
        code: "bootstrap",
        say: "That machine is in setup mode. I can adopt it into your account and connect.",
        action: "device.recoverAuth",
      };
    case "offline":
      return {
        code: "device-offline",
        say: "That machine has no recent heartbeat. Power it on and run yaver serve, then say 'try again'.",
        shellHint: "yaver serve",
      };
    case "ready-to-connect":
      return {
        code: "reachable-not-connected",
        say: "That machine is reachable. Connecting now.",
        action: "device.select",
      };
  }
  if (i.manualAuthRequired) {
    return {
      code: "manual-auth",
      say: "I couldn't auto-pair that box after several tries. Run yaver auth on the machine, then say 'try again'.",
      shellHint: "yaver auth",
    };
  }
  if (i.lastError) {
    return {
      code: "unknown-error",
      say: `I hit a problem connecting: ${i.lastError}. I can retry, or you can check the machine is running yaver serve.`,
      action: "device.recoverAuth",
    };
  }
  return {
    code: "reachable-not-connected",
    say: "Let me try connecting to that machine.",
    action: "device.select",
  };
}

export interface RunnerAuthInput {
  /** Per-runner readiness as reported by device.audit. */
  runners: Partial<Record<RunnerId, { installed: boolean; authed: boolean }>>;
  /** The runner the user wants (or the device's default). */
  wanted?: RunnerId;
}

export interface RunnerAuthDiagnosis {
  code: "ok" | "needs-auth" | "needs-install" | "no-runners" | "unknown";
  runner?: RunnerId;
  say: string;
  /** Catalog action to offer (runner_auth via ops, browser OAuth on the box). */
  action?: string;
}

const RUNNER_LABEL: Record<RunnerId, string> = {
  claude: "Claude Code",
  codex: "Codex",
  opencode: "OpenCode",
};

/**
 * First-class runner-OAuth triage: decide whether the wanted coding runner on
 * the connected box is ready, needs a subscription OAuth, or needs install.
 * Subscription OAuth only (Max Pro / ChatGPT Plus) — never API keys
 * (feedback_no_api_keys_subscription_only).
 */
export function diagnoseRunnerAuth(i: RunnerAuthInput): RunnerAuthDiagnosis {
  const present = (Object.keys(i.runners) as RunnerId[]).filter((r) => i.runners[r]);
  if (present.length === 0) {
    return {
      code: "no-runners",
      say: "No coding agent is set up on that machine yet. I can install one — Claude Code, Codex, or OpenCode.",
      action: "runner.install",
    };
  }
  // Prefer the wanted runner, else the first installed-and-authed, else first present.
  const order: RunnerId[] = i.wanted ? [i.wanted, ...present.filter((r) => r !== i.wanted)] : present;
  for (const r of order) {
    const st = i.runners[r];
    if (!st) continue;
    if (st.installed && st.authed) {
      return { code: "ok", runner: r, say: `${RUNNER_LABEL[r]} is ready on that machine.` };
    }
    if (st.installed && !st.authed) {
      return {
        code: "needs-auth",
        runner: r,
        say: `${RUNNER_LABEL[r]} needs sign-in on that machine. I'll start the subscription sign-in — approve it in the browser that opens.`,
        action: "runner.install", // ops runner_auth op=browser_start, dispatched by adapter
      };
    }
    if (!st.installed) {
      return {
        code: "needs-install",
        runner: r,
        say: `${RUNNER_LABEL[r]} isn't installed on that machine. I can install it now.`,
        action: "runner.install",
      };
    }
  }
  return { code: "unknown", say: "I couldn't determine the coding agent's status on that machine." };
}

/** Guard: never let a connectivity/runner action that resolves to a BLOCKED
 *  catalog id slip through. (Belt-and-suspenders for the runtime.) */
export function actionIsDispatchable(actionId: string | undefined): boolean {
  if (!actionId) return false;
  const d = dispositionFor(actionId);
  return d === "auto" || d === "confirm";
}
