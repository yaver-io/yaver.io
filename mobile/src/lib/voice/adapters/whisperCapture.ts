/**
 * whisperCapture.ts — AudioCaptureAdapter backed by on-device whisper.rn
 * realtime STT (free, offline, local-first — the default per the product owner).
 *
 * Two things this adapter gets right that the old clip-record path did not:
 *  1. STREAMING partials — whisper.rn's realtime mic emits its best transcript
 *     ~1×/slice, which is exactly what the endpointer needs to detect an
 *     end-of-utterance without a submit button.
 *  2. A CarPlay-correct AVAudioSession — .playAndRecord + .voiceChat mode +
 *     Bluetooth input. The barge-in audit found the original recorder set none
 *     of these, so the car mic (BT-HFP route) captured silence → "I didn't
 *     catch that". .voiceChat also enables the OS voice-processing path
 *     (hardware echo cancellation), which is the groundwork for real barge-in.
 *
 * A smaller slice (1s) is used so silence is detected within a beat instead of
 * whisper.rn's 5s default.
 */
import type { AudioCaptureAdapter, CaptureSession } from "../types";
import { startRealtimeTranscribe } from "../../speech";

export function createWhisperCapture(): AudioCaptureAdapter {
  return {
    async start(onPartial, opts): Promise<CaptureSession> {
      const surface = opts?.surface ?? "phone";
      const startedAt = Date.now();
      const ctrl = await startRealtimeTranscribe(
        (text) => onPartial(text, Date.now() - startedAt),
        {
          language: whisperLang(opts?.locale),
          sliceSec: 1,
          // startRealtimeTranscribe owns the shared iOS voice-chat session and
          // restores it on stop. Car is the sole background-capture opt-in.
          keepRecordingInBackground: surface === "car",
        },
      );
      let stopped = false;
      return {
        async stop(): Promise<string> {
          if (stopped) return "";
          stopped = true;
          try {
            return await ctrl.stop();
          } catch {
            return "";
          }
        },
        active: () => !stopped,
      };
    },
  };
}

/** whisper wants a bare language code ("en", "tr"), not a BCP-47 tag. */
function whisperLang(locale?: string): string {
  if (!locale) return "en";
  return locale.split(/[-_]/)[0].toLowerCase() || "en";
}
