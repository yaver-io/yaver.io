/**
 * voice/ — the shared, surface-agnostic hands-free voice conversation engine.
 *
 * One core (conversationCore.ts) runs the full "Claude-app voice mode" loop —
 * streaming STT → semantic endpointing → runner dispatch → spoken reply →
 * auto-resume → barge-in — for every surface (car/phone/watch/TV/web/glass/VR)
 * on both iOS and Android. Surfaces build a loop via createVoiceCore() and plug
 * in only what differs.
 *
 * See docs/architecture/VOICE_CONVERSATION.md for the architecture.
 */
export { VoiceConversationCore, type VoiceCoreDeps } from "./conversationCore";
export { createVoiceCore, type CreateVoiceCoreOptions } from "./createVoiceCore";
export {
  UtteranceEndpointer,
  DEFAULT_ENDPOINT_CONFIG,
  type EndpointConfig,
  type EndpointDecision,
} from "./endpointer";
export {
  createCompletenessJudge,
  heuristicVerdict,
  type ModelComplete,
} from "./completenessJudge";
export { realClock, realScheduler, FakeTime } from "./scheduler";
export {
  machineSwitchInterceptor,
  surfaceIntentInterceptor,
  carRiskPolicy,
  type MachineOption,
} from "./adapters/interceptors";
export type {
  VoiceSurface,
  VoiceState,
  VoiceCoreEvent,
  VoiceCoreListener,
  AudioCaptureAdapter,
  TtsAdapter,
  AgentChannelAdapter,
  CompletenessJudge,
  InstructionInterceptor,
  RiskPolicy,
} from "./types";
