/**
 * deviceTts.ts — TtsAdapter over speech.ts. Defaults to on-device (expo-speech)
 * so the hands-free loop is free and offline; honours a configured cloud
 * provider (OpenAI/OpenRouter) when the user set one. `stop()` is the barge-in
 * hook — speech.ts::stopSpeaking cuts off expo-speech and any cloud playback.
 */
import type { TtsAdapter, TtsOptions } from "../types";
import { speakText, stopSpeaking, type TextToSpeechConfig } from "../../speech";

export interface DeviceTtsConfig {
  provider?: TextToSpeechConfig["provider"];
  apiKey?: string;
  voice?: string;
  model?: string;
}

export function createTts(cfg: DeviceTtsConfig = {}): TtsAdapter {
  const provider = cfg.provider || "device";
  return {
    async speak(text: string, opts?: TtsOptions): Promise<void> {
      try {
        await speakText(text, {
          provider,
          apiKey: cfg.apiKey,
          voice: cfg.voice,
          model: cfg.model,
          language: opts?.locale,
        });
      } catch {
        // A TTS failure must never crash the loop — the turn already happened.
      }
    },
    async stop(): Promise<void> {
      stopSpeaking();
    },
  };
}
