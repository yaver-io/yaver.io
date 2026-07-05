import { quicClient } from "./quic";
import {
  carVoiceViewport,
  headsetViewport,
  normalizeOpsInitial,
  tvDpadViewport,
  viewportHeaders,
  watchVoiceViewport,
  type DpadKey,
  type DpadTarget,
  type GatewayIntentResult,
  type OpsTarget,
} from "./runtimeSurfaceTypes";

export {
  carVoiceViewport,
  headsetViewport,
  normalizeOpsInitial,
  tvDpadViewport,
  viewportHeaders,
  watchVoiceViewport,
};
export type {
  DpadKey,
  DpadTarget,
  GatewayIntentResult,
  RuntimeSurface,
  SurfaceInteraction,
  SurfaceRiskPolicy,
  SurfaceVisualBudget,
  TaskViewportInput,
} from "./runtimeSurfaceTypes";

async function callSurfaceOps<T = unknown>(
  target: OpsTarget,
  verb: string,
  payload: Record<string, unknown> = {},
  timeoutMs = 30000,
): Promise<T> {
  const deviceId = typeof target === "string" ? target : target?.id;
  const data = deviceId
    ? await quicClient.callOpsOnDevice(deviceId, verb, payload, timeoutMs)
    : await quicClient.callOps(verb, payload);
  return normalizeOpsInitial<T>(data);
}

export const runtimeSurfaceClient = {
  viewportHeaders,
  carVoiceViewport,
  headsetViewport,
  watchVoiceViewport,
  tvDpadViewport,

  gatewayIntent: (target: OpsTarget, utterance: string) =>
    callSurfaceOps<GatewayIntentResult>(
      target,
      "gateway_intent",
      { utterance },
      60000,
    ),

  gatewayQuery: (
    target: OpsTarget,
    connector: string,
    capability: string,
    params: Record<string, string> = {},
  ) =>
    callSurfaceOps<unknown>(
      target,
      "gateway_query",
      { connector, capability, params },
      60000,
    ),

  gatewayActDryRun: (
    target: OpsTarget,
    connector: string,
    capability: string,
    params: Record<string, string> = {},
  ) =>
    callSurfaceOps<unknown>(
      target,
      "gateway_act",
      { connector, capability, params, execute: false },
      60000,
    ),

  gatewayActConfirm: (target: OpsTarget, actId: string, answer: string) =>
    callSurfaceOps<unknown>(
      target,
      "gateway_act_confirm",
      { act_id: actId, answer },
      120000,
    ),

  meetingNext: (
    target: OpsTarget,
    args: {
      provider?: string;
      withinHours?: number;
      includePastMin?: number;
    } = {},
  ) =>
    callSurfaceOps<unknown>(
      target,
      "meeting_next",
      args as Record<string, unknown>,
      60000,
    ),

  meetingJoinNext: (
    target: OpsTarget,
    args: {
      provider?: string;
      open?: boolean;
      openMode?: string;
      surface?: string;
      withinHours?: number;
      includePastMin?: number;
    } = {},
  ) =>
    callSurfaceOps<unknown>(
      target,
      "meeting_join_next",
      args as Record<string, unknown>,
      60000,
    ),

  meetingOpenUrl: (
    target: OpsTarget,
    args: { url: string; open?: boolean; openMode?: string; surface?: string },
  ) =>
    callSurfaceOps<unknown>(
      target,
      "meeting_open_url",
      args as Record<string, unknown>,
      30000,
    ),

  mailSearch: (
    target: OpsTarget,
    args: {
      provider?: string;
      folder?: string;
      query?: string;
      limit?: number;
      onlyPersonal?: boolean;
    } = {},
  ) =>
    callSurfaceOps<unknown>(
      target,
      "mail_search",
      args as Record<string, unknown>,
      60000,
    ),

  mailUnread: (
    target: OpsTarget,
    args: { provider?: string; limit?: number; onlyPersonal?: boolean } = {},
  ) =>
    callSurfaceOps<unknown>(
      target,
      "mail_unread",
      args as Record<string, unknown>,
      60000,
    ),

  mailSend: (
    target: OpsTarget,
    args: {
      to: string[];
      subject: string;
      body: string;
      html?: string;
      cc?: string[];
      bcc?: string[];
      execute?: boolean;
      confirm?: string;
      surface?: string;
    },
  ) =>
    callSurfaceOps<unknown>(
      target,
      "mail_send",
      args as Record<string, unknown>,
      60000,
    ),

  gitPRs: (
    target: OpsTarget,
    args: {
      provider?: string;
      directory?: string;
      state?: string;
      limit?: number;
    } = {},
  ) =>
    callSurfaceOps<unknown>(
      target,
      "git_prs",
      args as Record<string, unknown>,
      60000,
    ),

  gitIssues: (
    target: OpsTarget,
    args: {
      provider?: string;
      directory?: string;
      state?: string;
      limit?: number;
    } = {},
  ) =>
    callSurfaceOps<unknown>(
      target,
      "git_issues",
      args as Record<string, unknown>,
      60000,
    ),

  gitCIStatus: (
    target: OpsTarget,
    args: { provider?: string; directory?: string } = {},
  ) =>
    callSurfaceOps<unknown>(
      target,
      "git_ci_status",
      args as Record<string, unknown>,
      60000,
    ),

  mediaOpen: (
    target: OpsTarget,
    args: {
      provider?: string;
      query?: string;
      url?: string;
      live?: boolean;
      open?: boolean;
      openMode?: string;
      surface?: string;
    },
  ) =>
    callSurfaceOps<unknown>(
      target,
      "media_open",
      args as Record<string, unknown>,
      30000,
    ),

  mapsOpen: (
    target: OpsTarget,
    args: {
      provider?: string;
      query?: string;
      origin?: string;
      destination?: string;
      traffic?: boolean;
      open?: boolean;
      openMode?: string;
      surface?: string;
    },
  ) =>
    callSurfaceOps<unknown>(
      target,
      "maps_open",
      args as Record<string, unknown>,
      30000,
    ),

  dpadInput: (
    target: OpsTarget,
    args: {
      target: DpadTarget;
      key: DpadKey;
      repeat?: number;
      device?: string;
      host?: string;
      app?: string;
    },
  ) =>
    callSurfaceOps<{
      target: DpadTarget;
      key: DpadKey;
      repeat: number;
      last?: unknown;
    }>(target, "dpad_input", args as Record<string, unknown>, 20000),
};
