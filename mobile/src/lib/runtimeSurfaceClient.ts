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
  type RuntimeTurnRequest,
  type RuntimeTurnListResponse,
  type RuntimeTurnResponse,
  type RuntimeTurnState,
  type RuntimeTurnEvidence,
  type RuntimeDeployPreflight,
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
  RuntimeTurnEvidence,
  RuntimeTurnQueueItem,
  RuntimeTurnRequest,
  RuntimeTurnListResponse,
  RuntimeTurnResponse,
  RuntimeTurnState,
  RuntimeTurnSurface,
  RuntimeTurnTarget,
  RuntimeTurnTestTarget,
  RuntimeDeployPreflight,
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

const RUNTIME_TURN_TERMINAL = new Set([
  "captured",
  "needs_input",
  "ready_to_test",
  "ready_to_deploy",
  "done",
  "failed",
  "cancelled",
]);

function isRuntimeTurnTerminal(state?: RuntimeTurnState): boolean {
  return RUNTIME_TURN_TERMINAL.has(String(state || "").toLowerCase());
}

async function waitForRuntimeTurnDone(
  target: OpsTarget,
  initial: RuntimeTurnResponse,
  opts: {
    pollIntervalMs?: number;
    maxWaitMs?: number;
    sleep?: (ms: number) => Promise<void>;
    now?: () => number;
  } = {},
): Promise<RuntimeTurnResponse> {
  const itemId = initial.turnId || initial.queue?.itemId;
  if (!itemId || isRuntimeTurnTerminal(initial.state)) return initial;

  const pollMs = opts.pollIntervalMs ?? 4000;
  const maxWaitMs = opts.maxWaitMs ?? 15 * 60 * 1000;
  const sleep = opts.sleep ?? ((ms: number) => new Promise((r) => setTimeout(r, ms)));
  const now = opts.now ?? (() => Date.now());
  const deadline = now() + maxWaitMs;
  let last = initial;

  while (now() < deadline) {
    await sleep(pollMs);
    last = await runtimeSurfaceClient.runtimeTurnStatus(target, itemId);
    if (isRuntimeTurnTerminal(last.state)) return last;
  }
  return {
    ...last,
    spoken: last.spoken || "Still working. I'll let you know on your phone.",
  };
}

export const runtimeSurfaceClient = {
  viewportHeaders,
  carVoiceViewport,
  headsetViewport,
  watchVoiceViewport,
  tvDpadViewport,
  isRuntimeTurnTerminal,
  waitForRuntimeTurnDone,

  runtimeTurn: (
    target: OpsTarget,
    request: RuntimeTurnRequest,
  ) =>
    callSurfaceOps<RuntimeTurnResponse>(
      target,
      "runtime_turn",
      request as unknown as Record<string, unknown>,
      120000,
    ),

  turn: (
    target: OpsTarget,
    text: string,
    opts: Partial<RuntimeTurnRequest> = {},
  ) =>
    callSurfaceOps<RuntimeTurnResponse>(
      target,
      "runtime_turn",
      {
        ...opts,
        text,
        queue: opts.queue ?? true,
        surface: opts.surface ?? {
          id: "mobile",
          class: "mobile-phone",
          interaction: "touch",
          visualBudget: "full",
          riskPolicy: "normal",
          replyTo: "mobile",
        },
      } as unknown as Record<string, unknown>,
      120000,
    ),

  runtimeTurnStatus: (target: OpsTarget, itemId: string) =>
    callSurfaceOps<RuntimeTurnResponse>(
      target,
      "runtime_turn_status",
      { itemId },
      30000,
    ),

  runtimeTurns: (target: OpsTarget, limit = 25) =>
    callSurfaceOps<RuntimeTurnListResponse>(
      target,
      "runtime_turns",
      { limit },
      30000,
    ),

  /** Promote a captured idea into real work, keeping its original turnId. */
  runtimeTurnRun: (target: OpsTarget, itemId: string) =>
    callSurfaceOps<RuntimeTurnResponse>(
      target,
      "runtime_turn_run",
      { itemId },
      120000,
    ),

  /**
   * Attempt the device reload and report what actually happened. Resolves with
   * `testTarget.state === "unreachable"` when nothing was listening — surface
   * that to the user rather than treating it as a successful test push.
   */
  runtimeTurnVerify: (target: OpsTarget, itemId: string) =>
    callSurfaceOps<RuntimeTurnResponse>(
      target,
      "runtime_turn_verify",
      { itemId },
      60000,
    ),

  /** Attach evidence refs (screenshot, clip, console, route) to a turn. */
  runtimeTurnEvidence: (
    target: OpsTarget,
    itemId: string,
    evidence: RuntimeTurnEvidence[],
  ) =>
    callSurfaceOps<RuntimeTurnResponse>(
      target,
      "runtime_turn_evidence",
      { itemId, evidence },
      30000,
    ),

  /**
   * Check whether a turn is shippable and get the exact command to run.
   * This NEVER deploys — deploy stays a human action on a full-visual surface.
   */
  runtimeTurnDeployPreflight: (
    target: OpsTarget,
    itemId: string,
    deployTarget?: string,
  ) =>
    callSurfaceOps<RuntimeDeployPreflight>(
      target,
      "runtime_turn_deploy_preflight",
      { itemId, target: deployTarget },
      60000,
    ),

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

  gitConnect: (
    target: OpsTarget,
    args: {
      provider?: string;
      host?: string;
      surface?: string;
    } = {},
  ) =>
    callSurfaceOps<unknown>(
      target,
      "git_connect",
      args as Record<string, unknown>,
      60000,
    ),

  gitConnectStatus: (
    target: OpsTarget,
    args: {
      sessionId?: string;
      session_id?: string;
      provider?: string;
      surface?: string;
    },
  ) =>
    callSurfaceOps<unknown>(
      target,
      "git_connect_status",
      args as Record<string, unknown>,
      30000,
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
