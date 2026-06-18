import { quicClient } from "./quic";
import {
  carVoiceViewport,
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
  watchVoiceViewport,
  tvDpadViewport,

  gatewayIntent: (target: OpsTarget, utterance: string) =>
    callSurfaceOps<GatewayIntentResult>(target, "gateway_intent", { utterance }, 60000),

  gatewayQuery: (target: OpsTarget, connector: string, capability: string, params: Record<string, string> = {}) =>
    callSurfaceOps<unknown>(target, "gateway_query", { connector, capability, params }, 60000),

  gatewayActDryRun: (target: OpsTarget, connector: string, capability: string, params: Record<string, string> = {}) =>
    callSurfaceOps<unknown>(target, "gateway_act", { connector, capability, params, execute: false }, 60000),

  gatewayActConfirm: (target: OpsTarget, actId: string, answer: string) =>
    callSurfaceOps<unknown>(target, "gateway_act_confirm", { act_id: actId, answer }, 120000),

  dpadInput: (
    target: OpsTarget,
    args: { target: DpadTarget; key: DpadKey; repeat?: number; device?: string; host?: string; app?: string },
  ) => callSurfaceOps<{ target: DpadTarget; key: DpadKey; repeat: number; last?: unknown }>(target, "dpad_input", args as Record<string, unknown>, 20000),
};

