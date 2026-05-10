/**
 * yaverAgentTools — compiled-in tool registry for the mobile-embedded
 * yaver-agent (the small LLM that handles control-plane tasks).
 *
 * The registry is intentionally NOT discovered over MCP. The mobile app
 * ships these tool definitions and their dispatchers as code so that:
 *
 *  1. Tool surface is fixed and trusted (no schema injection).
 *  2. Token bytes never travel through tool inputs/outputs — anything
 *     sensitive flows on a side-channel (vault P2P, native fs APIs).
 *  3. The same dispatcher backs both the LLM tool-loop AND quick UI
 *     buttons in the empty-state Tasks screen.
 *
 * Out of scope here: the LLM-provider adapter (Anthropic / GLM /
 * OpenAI / OpenRouter). That sits one layer up and consumes this
 * registry. See task #9.
 */

import type { Device } from "../context/DeviceContext";
import {
  quicClient,
  type YaverAgentDeviceAudit,
  type YaverAgentRecommendation,
} from "./quic";

// ── Tool argument + result types ────────────────────────────────────

export interface DeviceSummary {
  deviceId: string;
  name: string;
  alias?: string;
  online: boolean;
  needsAuth: boolean;
  os?: string;
  lastSeen?: number;
  isPrimary: boolean;
  isSecondary: boolean;
  isGuest?: boolean;
}

export interface ResolveDeviceArgs {
  /** Free-form: "primary" | "secondary" | alias (case-insensitive) | deviceId. */
  target: string;
}

export interface ResolveDeviceResult {
  deviceId: string;
  name: string;
  alias?: string;
  matchedBy: "primary" | "secondary" | "alias" | "id" | "name";
}

export interface AuditDeviceArgs {
  /** Same shape as ResolveDeviceArgs.target. */
  target: string;
  /** Optional workDir for runner readiness probes. */
  workDir?: string;
}

export interface AuditDeviceResult {
  device: DeviceSummary;
  audit?: YaverAgentDeviceAudit;
  /** Set when audit could not be retrieved (offline, transport error). */
  unreachable?: { reason: string };
}

export interface NextStepResult {
  device: DeviceSummary;
  recommendation: YaverAgentRecommendation;
  /** Other recommendations that the agent might queue after this one. */
  followUps: YaverAgentRecommendation[];
}

// ── Context the dispatcher needs from the mobile app ────────────────
//
// This indirection keeps the tool layer testable and free of React
// hooks. The mobile app builds a YaverAgentToolContext from its
// DeviceContext + Convex client and passes it in.

export interface YaverAgentToolContext {
  /** Snapshot of devices visible to the current Convex user. */
  devices: () => Device[];
  /** ID of the device currently set as primary (null when unset). */
  primaryDeviceId: () => string | null;
  /** ID of the device currently set as secondary. */
  secondaryDeviceId: () => string | null;
  /**
   * Switch the QUIC client to talk to a specific device. The dispatcher
   * does this before any agent-HTTP call (audit, /vault/*, etc) because
   * quicClient is single-baseUrl. The host app already wraps this — we
   * just need a hook into it.
   */
  selectDevice: (deviceId: string) => Promise<void>;
}

// ── Tool definition shape (provider-agnostic) ───────────────────────
//
// The fields mirror what every modern function-calling LLM expects:
// a name, a one-paragraph description for the model, and a JSON Schema
// for the parameters. The provider adapter (next turn) translates this
// into the vendor-specific request format.

export interface YaverAgentTool<Args = unknown, Result = unknown> {
  name: string;
  description: string;
  parameters: Record<string, unknown>; // JSON Schema
  /** Invoked by the dispatcher with parsed Args; returns a serializable Result. */
  invoke: (args: Args, ctx: YaverAgentToolContext) => Promise<Result>;
}

// ── Helpers ─────────────────────────────────────────────────────────

function summarizeDevice(d: Device, primaryId: string | null, secondaryId: string | null): DeviceSummary {
  return {
    deviceId: d.id,
    name: d.name,
    alias: d.alias || undefined,
    online: !!d.online,
    needsAuth: !!d.needsAuth,
    os: d.os || undefined,
    lastSeen: d.lastSeen,
    isPrimary: d.id === primaryId,
    isSecondary: d.id === secondaryId,
    isGuest: !!d.isGuest,
  };
}

function resolveTarget(target: string, ctx: YaverAgentToolContext): ResolveDeviceResult | null {
  const t = (target || "").trim();
  if (!t) return null;
  const lower = t.toLowerCase();
  const list = ctx.devices();
  const primary = ctx.primaryDeviceId();
  const secondary = ctx.secondaryDeviceId();

  if (lower === "primary") {
    const d = list.find((x) => x.id === primary);
    if (d) return { deviceId: d.id, name: d.name, alias: d.alias, matchedBy: "primary" };
  }
  if (lower === "secondary") {
    const d = list.find((x) => x.id === secondary);
    if (d) return { deviceId: d.id, name: d.name, alias: d.alias, matchedBy: "secondary" };
  }
  // exact deviceId match
  const byId = list.find((x) => x.id === t);
  if (byId) return { deviceId: byId.id, name: byId.name, alias: byId.alias, matchedBy: "id" };
  // alias match (case-insensitive)
  const byAlias = list.find((x) => (x.alias || "").toLowerCase() === lower);
  if (byAlias) return { deviceId: byAlias.id, name: byAlias.name, alias: byAlias.alias, matchedBy: "alias" };
  // name match (case-insensitive substring — last resort, only if unique)
  const byName = list.filter((x) => x.name.toLowerCase().includes(lower));
  if (byName.length === 1) {
    return { deviceId: byName[0].id, name: byName[0].name, alias: byName[0].alias, matchedBy: "name" };
  }
  return null;
}

// ── Tools ───────────────────────────────────────────────────────────

const deviceListTool: YaverAgentTool<Record<string, never>, { devices: DeviceSummary[] }> = {
  name: "device.list",
  description:
    "List every Yaver device registered to the current user, with online state, " +
    "auth state, and which one is primary/secondary. Always call this first when " +
    "the user references a device by name (e.g. \"mac mini\", \"hetzner\") — it " +
    "gives you the deviceId and aliases needed by other tools.",
  parameters: { type: "object", properties: {}, additionalProperties: false },
  async invoke(_args, ctx) {
    const primary = ctx.primaryDeviceId();
    const secondary = ctx.secondaryDeviceId();
    return { devices: ctx.devices().map((d) => summarizeDevice(d, primary, secondary)) };
  },
};

const deviceResolveTool: YaverAgentTool<ResolveDeviceArgs, ResolveDeviceResult | { error: string }> = {
  name: "device.resolve",
  description:
    "Resolve a free-form device reference (\"primary\", \"secondary\", an alias, " +
    "a deviceId, or a unique name substring) to a concrete deviceId. Returns the " +
    "resolved id + which kind of match it was. Returns {error} when ambiguous or " +
    "no match.",
  parameters: {
    type: "object",
    properties: {
      target: {
        type: "string",
        description:
          "Device hint: \"primary\", \"secondary\", an alias, a deviceId, or a unique name substring.",
      },
    },
    required: ["target"],
    additionalProperties: false,
  },
  async invoke(args, ctx) {
    const r = resolveTarget(args.target, ctx);
    if (!r) return { error: `no device matches ${JSON.stringify(args.target)}` };
    return r;
  },
};

const deviceAuditTool: YaverAgentTool<AuditDeviceArgs, AuditDeviceResult> = {
  name: "device.audit",
  description:
    "Audit one device end-to-end: returns Yaver-level lifecycle " +
    "(bootstrap / yaver-auth-expired / ready-to-connect), each runner's " +
    "auth state (claude / codex / opencode), and a recommendations list " +
    "the agent should walk through in order. If the device is offline, " +
    "returns {unreachable} so the agent reports that to the user instead " +
    "of guessing.",
  parameters: {
    type: "object",
    properties: {
      target: { type: "string", description: "Device hint (see device.resolve)." },
      workDir: { type: "string", description: "Optional working directory for runner readiness probes." },
    },
    required: ["target"],
    additionalProperties: false,
  },
  async invoke(args, ctx) {
    const resolved = resolveTarget(args.target, ctx);
    if (!resolved) {
      // Build a synthetic summary so the model still gets a structured failure.
      const fake: DeviceSummary = {
        deviceId: "",
        name: args.target,
        online: false,
        needsAuth: true,
        isPrimary: false,
        isSecondary: false,
      };
      return { device: fake, unreachable: { reason: `no device matches ${JSON.stringify(args.target)}` } };
    }
    const list = ctx.devices();
    const fullDevice = list.find((x) => x.id === resolved.deviceId);
    const summary = summarizeDevice(
      fullDevice ?? ({ id: resolved.deviceId, name: resolved.name, online: false, lastSeen: 0, runners: [], host: "", port: 0, os: "" } as unknown as Device),
      ctx.primaryDeviceId(),
      ctx.secondaryDeviceId(),
    );
    if (!summary.online) {
      return {
        device: summary,
        unreachable: {
          reason: `device "${summary.name}" is offline (no recent heartbeat).`,
        },
      };
    }
    try {
      await ctx.selectDevice(summary.deviceId);
      const audit = await quicClient.yaverAgentAudit(args.workDir ? { workDir: args.workDir } : undefined);
      return { device: summary, audit };
    } catch (e) {
      return {
        device: summary,
        unreachable: {
          reason: e instanceof Error ? e.message : String(e),
        },
      };
    }
  },
};

const deviceNextStepTool: YaverAgentTool<AuditDeviceArgs, NextStepResult | { error: string }> = {
  name: "device.next_step",
  description:
    "Return the single most important next action the user should take on a " +
    "device, plus any follow-ups. Use this after device.audit when you need to " +
    "tell the user one thing instead of dumping the whole audit. The recommendation's " +
    "`action` field names a tool you can offer to invoke (e.g. yaver.start_auth).",
  parameters: deviceAuditTool.parameters,
  async invoke(args, ctx) {
    const auditResult = await deviceAuditTool.invoke(args, ctx);
    if (auditResult.unreachable) {
      return { error: auditResult.unreachable.reason };
    }
    const recs = auditResult.audit?.recommendations ?? [];
    if (recs.length === 0) {
      return { error: "audit returned no recommendations" };
    }
    return {
      device: auditResult.device,
      recommendation: recs[0],
      followUps: recs.slice(1),
    };
  },
};

// ── Registry + dispatcher ───────────────────────────────────────────

export const YAVER_AGENT_TOOLS: YaverAgentTool[] = [
  deviceListTool as YaverAgentTool,
  deviceResolveTool as YaverAgentTool,
  deviceAuditTool as YaverAgentTool,
  deviceNextStepTool as YaverAgentTool,
];

/**
 * Dispatcher: invokes a tool by name with the parsed arguments. Returns
 * whatever the tool returned. Throws if the tool name is unknown so the
 * LLM gets a clear "no such tool" signal instead of a silent null.
 *
 * Provider adapters (next turn) call this per tool-use block.
 */
export async function dispatchYaverAgentTool(
  name: string,
  args: unknown,
  ctx: YaverAgentToolContext,
): Promise<unknown> {
  const tool = YAVER_AGENT_TOOLS.find((t) => t.name === name);
  if (!tool) throw new Error(`unknown yaver-agent tool: ${name}`);
  return tool.invoke(args as never, ctx);
}
