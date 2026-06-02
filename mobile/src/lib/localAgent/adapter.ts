// localAgent/adapter.ts — the glue between the pure ladder core and the live
// app. Two halves:
//   (1) STATE-IN: map live DeviceContext + agent-audit/project shapes →
//       LadderState/DeviceFacts the ladder consumes.
//   (2) ACTION-OUT: dispatch a ladder NextStep.action through the catalog's
//       via-split (context fn / ops verb / mcp tool), enforcing the safety tier.
//   plus a deterministic keyword goal-extractor.
//
// PURE + RN-free by DEPENDENCY INJECTION: the side-effecting dispatch takes a
// `DispatchDeps` object, so this module unit-tests under tsx with fakes. The
// real wiring (quicClient.callOps, callMcpDirect, the DeviceContext fns) lives
// in adapterBindings.ts, which is NOT part of the pure barrel.

import {
  type LadderState,
  type DeviceFacts,
  type Goal,
} from "./capabilityLadder";
import { getAction, dispositionFor } from "./catalog";
import type { ModelTier } from "./tiers";
import type { RunnerId } from "./connectivity";

// ───────────────────────────────────────────────────────────────────────────
// (1) STATE-IN — structural subsets of the real shapes (avoids importing the
// whole DeviceContext into the pure core; mirrors context/DeviceContext.tsx).
// ───────────────────────────────────────────────────────────────────────────

export interface DeviceLike {
  id: string;
  name: string;
  alias?: string;
  os?: string;
  online?: boolean;
  lastSeen?: number;
  needsAuth?: boolean;
  peerState?: "online" | "stale" | "offline";
}

export interface DeviceStateLike {
  devices: DeviceLike[];
  activeDevice?: DeviceLike | null;
  connectionStatus: "disconnected" | "connecting" | "connected" | "error";
  lastError?: string | null;
  agentAuthExpired?: boolean;
  unreachableDeviceIds?: string[];
  manualAuthRequiredDeviceIds?: string[];
  connectedDeviceIds?: string[];
}

/** Shape of quicClient.yaverAgentAudit() we consume (YaverAgentDeviceAudit). */
export interface AuditLike {
  lifecycleState?: string; // "bootstrap" | "yaver-auth-expired" | "ready-to-connect"
  runners?: { id: string; installed?: boolean; ready?: boolean; authConfigured?: boolean }[];
}

/** Live facts a single device fetch couldn't include yet (see gaps below). */
export interface DeviceProbe {
  audit?: AuditLike;
  /** quicClient.listProjects() → name/branch only. */
  projects?: { name: string; branch?: string }[];
  /** No mobile "is git authed?" query exists yet → caller injects, default unknown. */
  gitAuthed?: boolean;
  /** From callMobileHermesDoctor(), when probed. */
  hermesReady?: boolean;
  /** The voice runtime tracks the selected project (no "active" marker on list). */
  activeProjectSlug?: string;
  deployTargetConfigured?: boolean;
}

const RUNNER_IDS: RunnerId[] = ["claude", "codex", "opencode"];

function isConnected(d: DeviceLike, s: DeviceStateLike): boolean {
  if (s.connectedDeviceIds?.includes(d.id)) return true;
  return s.activeDevice?.id === d.id && s.connectionStatus === "connected";
}

function deriveLifecycle(
  d: DeviceLike,
  s: DeviceStateLike,
  audit?: AuditLike,
): DeviceFacts["lifecycle"] {
  if (isConnected(d, s)) return "connected";
  // Prefer the agent's own lifecycle string when we audited the box.
  switch (audit?.lifecycleState) {
    case "bootstrap":
      return "bootstrap";
    case "yaver-auth-expired":
      return "yaver-auth-expired";
    case "ready-to-connect":
      return "ready-to-connect";
  }
  if (s.agentAuthExpired || d.needsAuth || s.manualAuthRequiredDeviceIds?.includes(d.id)) {
    return "yaver-auth-expired";
  }
  if (d.online === false || d.peerState === "offline" || s.unreachableDeviceIds?.includes(d.id)) {
    return "offline";
  }
  return "ready-to-connect";
}

/** Map a live Device (+ optional probe) → the ladder's DeviceFacts. Parts the
 *  mobile app can't query yet (gitAuthed, activeProjectSlug, hermesReady unless
 *  doctored) come from the probe and default to "not ready" — so the ladder
 *  gracefully *prompts the introduction* rather than asserting readiness. */
export function deviceFactsFrom(
  d: DeviceLike,
  s: DeviceStateLike,
  probe: DeviceProbe = {},
): DeviceFacts {
  const runners: DeviceFacts["runners"] = {};
  for (const r of probe.audit?.runners ?? []) {
    if ((RUNNER_IDS as string[]).includes(r.id)) {
      runners[r.id as RunnerId] = {
        installed: !!r.installed,
        // authed ⇔ the agent reports the runner both auth-configured and ready.
        authed: !!r.authConfigured && r.ready !== false,
      };
    }
  }
  return {
    deviceId: d.id,
    lifecycle: deriveLifecycle(d, s, probe.audit),
    connected: isConnected(d, s),
    manualAuthRequired: s.manualAuthRequiredDeviceIds?.includes(d.id),
    runners,
    gitAuthed: probe.gitAuthed,
    hermesReady: probe.hermesReady,
    projects: (probe.projects ?? []).map((p) => ({ slug: p.name, branch: p.branch })),
    activeProjectSlug: probe.activeProjectSlug,
    deployTargetConfigured: probe.deployTargetConfigured,
  };
}

/** Assemble the full LadderState. `target` is the device the user named (run the
 *  resolver first); omit for spine-only / no-device situations. `localTier`
 *  comes from selectModelTier(this phone's capability). */
export function buildLadderState(
  s: DeviceStateLike,
  opts: { online: boolean; localTier: ModelTier; target?: DeviceLike | null; probe?: DeviceProbe },
): LadderState {
  return {
    online: opts.online,
    hasAnyDevice: s.devices.length > 0,
    reachableDeviceIds: s.devices
      .filter((d) => d.online !== false && !s.unreachableDeviceIds?.includes(d.id))
      .map((d) => d.id),
    device: opts.target ? deviceFactsFrom(opts.target, s, opts.probe ?? {}) : undefined,
    localTier: opts.localTier,
    lastError: s.lastError ?? null,
  };
}

// ───────────────────────────────────────────────────────────────────────────
// Goal extraction — deterministic keyword pre-pass. Free-form intent the
// keywords miss is left to the grammar-constrained model (returns undefined).
// ───────────────────────────────────────────────────────────────────────────

export function extractGoal(transcript: string): Goal | undefined {
  const t = (transcript || "").toLowerCase();
  if (!t.trim()) return undefined;

  // Order matters: most-specific intent first.
  if (/\b(sandbox|on (my|this) phone|on-device|locally on the phone)\b/.test(t) &&
      /\b(code|build|write|app|make)\b/.test(t)) {
    return { kind: "sandbox" };
  }
  if (/\b(deploy|ship it|publish|release|go live)\b/.test(t)) return { kind: "deploy" };
  if (/\b(push|commit|pull request|open a pr|raise a pr|merge)\b/.test(t)) return { kind: "push" };
  if (/\b(preview|hot ?reload|reload|run (it )?on (my|the) phone|test on (my|the) phone|see it on my phone)\b/.test(t)) {
    return { kind: "preview" };
  }
  if (/\b(connect|reconnect|pair|wake|hook up)\b/.test(t)) return { kind: "connect" };
  if (/\b(build|code|write|implement|fix|add|create|edit|refactor|make me|scaffold|new app|start a)\b/.test(t)) {
    const fresh = /\b(new|fresh|scaffold|start a|from scratch|brand new)\b/.test(t);
    return { kind: "code", fresh };
  }
  return undefined; // no clear goal → ask / let the model decide.
}

// ───────────────────────────────────────────────────────────────────────────
// (2) ACTION-OUT — dispatch a catalog action through its via-split, enforcing
// the safety tier. Side effects are injected so this stays testable.
// ───────────────────────────────────────────────────────────────────────────

/** The concrete in-app DeviceContext fns the adapter may call (via:"context").
 *  All optional — a missing fn yields a clean "not wired" error, never a crash. */
export interface DispatchContextFns {
  selectDevice?: (d: DeviceLike) => Promise<unknown>;
  recoverDeviceAuth?: (d: DeviceLike) => Promise<unknown>;
  setPrimaryDevice?: (id: string) => Promise<unknown>;
  setSecondaryDevice?: (id: string) => Promise<unknown>;
  setDeviceAlias?: (d: DeviceLike, alias: string) => Promise<unknown>;
  claimPendingDevice?: (id: string, name?: string) => Promise<unknown>;
  setPrimaryRunner?: (id: string, runnerId: string | null, model?: string | null) => Promise<unknown>;
  refreshDevices?: () => Promise<unknown>;
}

export interface DispatchDeps {
  context: DispatchContextFns;
  /** quicClient.callOps(verb, payload) → targets the active (connected) box. */
  ops: (verb: string, payload: Record<string, unknown>) => Promise<{ ok?: boolean; error?: string }>;
  /** callMcpDirect(tool, args). */
  mcp: (tool: string, args: Record<string, unknown>) => Promise<{ ok: boolean; error?: string }>;
  /** True only after the user explicitly approved a CONFIRM-tier action. */
  confirmed?: boolean;
}

export interface DispatchTarget {
  device?: DeviceLike;
  args?: Record<string, unknown>;
}

export interface DispatchResult {
  ok: boolean;
  ran: boolean;
  /** Set when a CONFIRM-tier action was withheld pending spoken approval. */
  needsConfirm?: boolean;
  /** Set when a BLOCKED action was refused outright. */
  blocked?: boolean;
  error?: string;
}

/** Default ops payloads for the verbs whose op-routing the id implies. Anything
 *  else passes the caller's args straight through. */
function opsPayloadFor(id: string, t: DispatchTarget): Record<string, unknown> {
  const args = t.args ?? {};
  switch (id) {
    case "project.list":
      return { op: "list", ...args };
    case "project.new":
      return { op: "scaffold", ...args };
    case "git.connect":
      return { provider: args.provider ?? "github", ...args };
    case "git.pushCreds":
      return { deviceId: t.device?.id, provider: args.provider ?? "all", ...args };
    case "git.push":
      return { command: args.command ?? "git push", ...args };
    default:
      return { ...args };
  }
}

/**
 * Dispatch a catalog action. Enforces, in order: known action → not BLOCKED →
 * CONFIRM requires `deps.confirmed` → device present when needed → via-split.
 */
export async function dispatchAction(
  actionId: string,
  target: DispatchTarget,
  deps: DispatchDeps,
): Promise<DispatchResult> {
  const spec = getAction(actionId);
  if (!spec) return { ok: false, ran: false, error: `unknown action: ${actionId}` };

  const disp = dispositionFor(actionId);
  if (disp === "blocked") return { ok: false, ran: false, blocked: true, error: "blocked from voice" };
  if (disp === "confirm" && !deps.confirmed) return { ok: false, ran: false, needsConfirm: true };

  if (spec.needsDevice && !target.device) {
    return { ok: false, ran: false, error: "this action needs a device, none resolved" };
  }

  try {
    if (spec.via === "context") return await dispatchContext(spec.id, target, deps.context);
    if (spec.via === "ops") {
      const r = await deps.ops(spec.opsVerb!, opsPayloadFor(spec.id, target));
      return { ok: r.ok !== false && !r.error, ran: true, error: r.error };
    }
    // via === "mcp"
    const args = { ...(target.args ?? {}), ...(target.device ? { deviceId: target.device.id } : {}) };
    const r = await deps.mcp(spec.mcpTool!, args);
    return { ok: r.ok, ran: true, error: r.error };
  } catch (e: any) {
    return { ok: false, ran: true, error: e?.message || String(e) };
  }
}

async function dispatchContext(
  id: string,
  t: DispatchTarget,
  fns: DispatchContextFns,
): Promise<DispatchResult> {
  const d = t.device;
  const args = t.args ?? {};
  const need = <T>(fn: T | undefined, name: string): DispatchResult | T =>
    fn ?? ({ ok: false, ran: false, error: `${name} not wired` } as DispatchResult);

  switch (id) {
    case "device.list":
    case "device.audit":
      // Read-only — the runtime reads DeviceContext directly; nothing to run.
      return { ok: true, ran: false };
    case "device.refresh": {
      const fn = need(fns.refreshDevices, "refreshDevices");
      if (typeof fn !== "function") return fn;
      await fn();
      return { ok: true, ran: true };
    }
    case "device.select": {
      const fn = need(fns.selectDevice, "selectDevice");
      if (typeof fn !== "function") return fn;
      await fn(d!);
      return { ok: true, ran: true };
    }
    case "device.recoverAuth": {
      const fn = need(fns.recoverDeviceAuth, "recoverDeviceAuth");
      if (typeof fn !== "function") return fn;
      await fn(d!);
      return { ok: true, ran: true };
    }
    case "device.setPrimary": {
      const fn = need(fns.setPrimaryDevice, "setPrimaryDevice");
      if (typeof fn !== "function") return fn;
      await fn(d!.id);
      return { ok: true, ran: true };
    }
    case "device.setSecondary": {
      const fn = need(fns.setSecondaryDevice, "setSecondaryDevice");
      if (typeof fn !== "function") return fn;
      await fn(d!.id);
      return { ok: true, ran: true };
    }
    case "device.setAlias": {
      const fn = need(fns.setDeviceAlias, "setDeviceAlias");
      if (typeof fn !== "function") return fn;
      await fn(d!, String(args.alias ?? ""));
      return { ok: true, ran: true };
    }
    case "device.claimPending": {
      const fn = need(fns.claimPendingDevice, "claimPendingDevice");
      if (typeof fn !== "function") return fn;
      await fn(d!.id, args.name as string | undefined);
      return { ok: true, ran: true };
    }
    case "runner.switch": {
      const fn = need(fns.setPrimaryRunner, "setPrimaryRunner");
      if (typeof fn !== "function") return fn;
      await fn(d!.id, (args.runnerId as string) ?? null, (args.model as string) ?? null);
      return { ok: true, ran: true };
    }
    default:
      return { ok: false, ran: false, error: `no context dispatch for ${id}` };
  }
}
