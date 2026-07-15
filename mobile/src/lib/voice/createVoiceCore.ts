/**
 * createVoiceCore.ts — the one factory every surface calls to get a fully wired
 * hands-free voice loop. Surfaces pass only what differs (which box, how to
 * reach it, their device list, their ops verbs, TTS prefs); the shared core +
 * adapters supply everything else.
 *
 * This is where "same mechanics on all interfaces" is realised: car, phone,
 * watch, TV, web, glass and VR all build their loop here and differ only in the
 * options below.
 */
import { VoiceConversationCore } from "./conversationCore";
import { realClock, realScheduler } from "./scheduler";
import { createWhisperCapture } from "./adapters/whisperCapture";
import { createTts, type DeviceTtsConfig } from "./adapters/deviceTts";
import { createRunnerChannel } from "./adapters/runnerChannel";
import { createLocalJudge } from "./adapters/localJudge";
import {
  machineSwitchInterceptor,
  surfaceIntentInterceptor,
  carRiskPolicy,
  type MachineOption,
} from "./adapters/interceptors";
import type {
  InstructionInterceptor,
  VoiceCoreListener,
  VoiceSurface,
} from "./types";
import type { SessionTurnDep } from "../carSessionTurn";

export interface CreateVoiceCoreOptions {
  surface: VoiceSurface;
  /** Drives one live runner turn (wraps quicClient.runnerSessionTurn). */
  sessionTurn: SessionTurnDep;
  /** Voice-addressable machines for "switch to <name>". Omit to disable. */
  machines?: () => MachineOption[];
  onSwitchMachine?: (deviceId: string) => void;
  /** Runtime ops for surface intents (mail/meeting/maps…). Omit to disable. */
  callOps?: (verb: string, payload: Record<string, unknown>) => Promise<unknown>;
  /** TTS provider config (defaults to on-device expo-speech). */
  tts?: DeviceTtsConfig;
  /** BCP-47 locale, e.g. "en-US" / "tr-TR". */
  locale?: string;
  listener?: VoiceCoreListener;
  /** Hard-gate deploy/push/delete/force behind a spoken confirm. Default true. */
  enableRisk?: boolean;
  /** Test seam: force the completeness judge's model completion fn. */
  judgeComplete?: NonNullable<Parameters<typeof createLocalJudge>[0]>["complete"];
}

export function createVoiceCore(o: CreateVoiceCoreOptions): VoiceConversationCore {
  const interceptors: InstructionInterceptor[] = [];
  // Machine-switch runs FIRST — it retargets, never executes, so it must win
  // over surface intents and coding dispatch.
  if (o.machines && o.onSwitchMachine) {
    interceptors.push(machineSwitchInterceptor(o.machines, o.onSwitchMachine));
  }
  if (o.callOps) {
    interceptors.push(surfaceIntentInterceptor(o.callOps));
  }

  return new VoiceConversationCore({
    surface: o.surface,
    capture: createWhisperCapture(),
    tts: createTts(o.tts),
    agent: createRunnerChannel({ sessionTurn: o.sessionTurn }),
    judge: createLocalJudge(
      o.judgeComplete !== undefined ? { complete: o.judgeComplete } : {},
    ),
    interceptors,
    risk: o.enableRisk === false ? undefined : carRiskPolicy(),
    locale: o.locale,
    listener: o.listener,
    clock: realClock,
    scheduler: realScheduler,
  });
}
