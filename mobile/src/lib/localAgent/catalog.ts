// localAgent/catalog.ts — the action catalog + safety tiers for the on-device
// voice helper. This is the security-critical contract: it decides which
// actions the local model may run automatically vs which require an explicit
// spoken confirmation, and which are flat-out blocked from voice.
//
// PURE + RN-free so it unit-tests under tsx. The mobile app maps each
// catalog entry to a concrete dispatcher (DeviceContext fn or /ops verb) in
// a thin adapter; the safety policy lives HERE so it's auditable in one place
// and can't drift per-call-site.
//
// Design: the model proposes {action, deviceRef?, args?}. We look the action
// up in the catalog, get its SafetyTier, and the runtime enforces:
//   READ_ONLY   → run immediately (status/info/list/audit)
//   SAFE_WRITE  → run immediately (reversible control-plane: pair, set-primary,
//                 switch-runner, reconnect, reload, refresh)
//   CONFIRM     → speak back the resolved device + action, require "yes"
//                 (anything that runs code or deploys)
//   BLOCKED     → never executable from voice (irreversible/destructive);
//                 the helper explains and points to the manual UI
//
// Rule of thumb: if getting it wrong on a misheard phrase is embarrassing →
// CONFIRM; if it's expensive/irreversible → BLOCKED.

export type SafetyTier = "READ_ONLY" | "SAFE_WRITE" | "CONFIRM" | "BLOCKED";

export interface ActionSpec {
  /** Stable id the model emits and the adapter dispatches on. */
  id: string;
  /** One-line description fed to the model as its tool doc. */
  description: string;
  tier: SafetyTier;
  /** Whether the action targets a specific device (needs resolution). */
  needsDevice: boolean;
  /** How the adapter executes it: an in-app DeviceContext fn, an /ops verb,
   *  or a direct MCP tool call (recovery-provider calls of the Go agent). */
  via: "context" | "ops" | "mcp";
  /** For ops actions, the verb name (machine routed separately). */
  opsVerb?: string;
  /** For mcp actions, the MCP tool name (e.g. recovery_target_start). */
  mcpTool?: string;
}

export const ACTION_CATALOG: ActionSpec[] = [
  // ── READ_ONLY ────────────────────────────────────────────────────
  { id: "device.list", description: "List all devices and their online/auth state.", tier: "READ_ONLY", needsDevice: false, via: "context" },
  { id: "device.audit", description: "Diagnose one device: lifecycle + runner auth + recommendations.", tier: "READ_ONLY", needsDevice: true, via: "context" },
  { id: "status", description: "Rollup of the agent's state on a device (tasks, dev server, tunnels).", tier: "READ_ONLY", needsDevice: true, via: "ops", opsVerb: "status" },
  { id: "info", description: "Hardware/OS snapshot of a device.", tier: "READ_ONLY", needsDevice: true, via: "ops", opsVerb: "info" },

  // ── SAFE_WRITE (reversible control-plane; auto-exec) ──────────────
  { id: "device.refresh", description: "Re-fetch the device list from the backend.", tier: "SAFE_WRITE", needsDevice: false, via: "context" },
  { id: "device.select", description: "Connect to / make a device the active one.", tier: "SAFE_WRITE", needsDevice: true, via: "context" },
  { id: "device.recoverAuth", description: "Reconnect a device or fix its expired agent auth.", tier: "SAFE_WRITE", needsDevice: true, via: "context" },
  { id: "device.setPrimary", description: "Set a device as the primary for auto-connect.", tier: "SAFE_WRITE", needsDevice: true, via: "context" },
  { id: "device.setSecondary", description: "Set a device as the secondary fallback.", tier: "SAFE_WRITE", needsDevice: true, via: "context" },
  { id: "device.setAlias", description: "Set a nickname/alias for a device.", tier: "SAFE_WRITE", needsDevice: true, via: "context" },
  { id: "device.claimPending", description: "Claim a freshly-bootstrapped box into the account.", tier: "SAFE_WRITE", needsDevice: true, via: "context" },
  { id: "runner.switch", description: "Switch the coding agent (claude/codex/opencode) on a device.", tier: "SAFE_WRITE", needsDevice: true, via: "context" },
  { id: "reload", description: "Hot-reload the dev server / Hermes bundle on a device.", tier: "SAFE_WRITE", needsDevice: true, via: "ops", opsVerb: "reload" },

  // ── Recovery-provider calls (yaver Go agent, mcp_recovery_tools.go) ──
  // Read-or-recover only, hardware-id-gated server-side — safe for the local
  // LLM to drive autonomously so it can fix a wedged box without SSH.
  { id: "recovery.reauthStart", description: "Start re-auth/re-pair for a device that lost its token.", tier: "SAFE_WRITE", needsDevice: true, via: "mcp", mcpTool: "device_reauth_start" },
  { id: "recovery.reauthStatus", description: "Check progress of a device re-auth.", tier: "READ_ONLY", needsDevice: true, via: "mcp", mcpTool: "device_reauth_status" },
  { id: "recovery.reauthWait", description: "Wait for a device re-auth to complete.", tier: "READ_ONLY", needsDevice: true, via: "mcp", mcpTool: "device_reauth_wait" },
  { id: "recovery.targetStart", description: "Start a full recovery session against a wedged box.", tier: "SAFE_WRITE", needsDevice: true, via: "mcp", mcpTool: "recovery_target_start" },
  { id: "recovery.targetStatus", description: "Check a recovery session's status.", tier: "READ_ONLY", needsDevice: true, via: "mcp", mcpTool: "recovery_target_status" },
  { id: "recovery.targetWait", description: "Wait for a recovery session / box to come back.", tier: "READ_ONLY", needsDevice: true, via: "mcp", mcpTool: "recovery_target_wait" },
  { id: "recovery.transportStatus", description: "Inspect which transports (direct/relay/tunnel) can reach a box.", tier: "READ_ONLY", needsDevice: true, via: "mcp", mcpTool: "recovery_transport_status" },

  // ── CONFIRM (runs code / deploys; speak-back + 'yes') ─────────────
  { id: "run", description: "Run a shell command on a device.", tier: "CONFIRM", needsDevice: true, via: "ops", opsVerb: "run" },
  { id: "build", description: "Build the project on a device.", tier: "CONFIRM", needsDevice: true, via: "ops", opsVerb: "build" },
  { id: "test", description: "Run the project test suite on a device.", tier: "CONFIRM", needsDevice: true, via: "ops", opsVerb: "test" },
  { id: "deploy", description: "Deploy the project to a hosting target.", tier: "CONFIRM", needsDevice: true, via: "ops", opsVerb: "deploy" },
  { id: "runner.install", description: "Install a coding runner on a device.", tier: "CONFIRM", needsDevice: true, via: "ops", opsVerb: "runner" },
  { id: "recycle", description: "Restart the agent daemon on a device.", tier: "CONFIRM", needsDevice: true, via: "ops", opsVerb: "recycle" },

  // ── BLOCKED (never from voice; irreversible / costly) ─────────────
  { id: "device.remove", description: "Permanently remove a device from the account.", tier: "BLOCKED", needsDevice: true, via: "context" },
  { id: "cloud.destroy", description: "Destroy a managed cloud machine.", tier: "BLOCKED", needsDevice: true, via: "ops", opsVerb: "cloud_destroy" },
  { id: "destroy", description: "Destroy remote infrastructure.", tier: "BLOCKED", needsDevice: true, via: "ops", opsVerb: "destroy" },
  { id: "provision", description: "Provision/buy a new managed cloud machine.", tier: "BLOCKED", needsDevice: false, via: "ops", opsVerb: "provision" },
  { id: "scale", description: "Scale a deployment (add replicas/resources).", tier: "BLOCKED", needsDevice: true, via: "ops", opsVerb: "scale" },
  { id: "secrets.write", description: "Write a vault/1Password secret.", tier: "BLOCKED", needsDevice: true, via: "ops", opsVerb: "secrets" },
];

const BY_ID = new Map(ACTION_CATALOG.map((a) => [a.id, a]));

export function getAction(id: string): ActionSpec | undefined {
  return BY_ID.get(id);
}

export type Disposition = "auto" | "confirm" | "blocked" | "unknown";

/**
 * The single decision point the voice runtime calls before doing anything.
 * Returns how to treat a model-proposed action id.
 */
export function dispositionFor(actionId: string): Disposition {
  const a = BY_ID.get(actionId);
  if (!a) return "unknown";
  switch (a.tier) {
    case "READ_ONLY":
    case "SAFE_WRITE":
      return "auto";
    case "CONFIRM":
      return "confirm";
    case "BLOCKED":
      return "blocked";
  }
}

/** Actions the model is allowed to even consider from voice (everything
 *  except BLOCKED). BLOCKED ids are still listed so the model can explain
 *  "I can't do that by voice — open the X screen", but are never dispatched. */
export function voiceInvokableActions(): ActionSpec[] {
  return ACTION_CATALOG.filter((a) => a.tier !== "BLOCKED");
}
