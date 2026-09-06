import { NativeModules, Platform } from "react-native";
import * as FileSystem from "expo-file-system/legacy";
import type { MouthFrame, VisualSpeechSource } from "./types";

type NativeMouthCropper = {
  processVideo(path: string, options: { width: number; height: number; fps: number; maxDurationMs: number }): Promise<{ frames: MouthFrame[]; durationMs: number }>;
};

export function nativeMouthCropper(): NativeMouthCropper {
  const module = NativeModules.YaverMouthCropper as NativeMouthCropper | undefined;
  if (!module || Platform.OS !== "ios") {
    throw new Error("Silent Input mouth tracking is currently available in the iOS experimental build.");
  }
  return module;
}

/** iOS MVP source. The camera view owns capture; this source owns conversion
 * and guaranteed disposal of the temporary full-frame recording. Backends see
 * only the standardized MouthFrame iterator. */
export class RecordedMouthFrameSource implements VisualSpeechSource {
  private started = false;
  private stopped = false;
  cropDurationMs = 0;

  constructor(private readonly path: string) {}

  async start(): Promise<void> { this.started = true; }

  async *frames(): AsyncIterable<MouthFrame> {
    if (!this.started || this.stopped) throw new Error("Visual speech source is not active.");
    const result = await nativeMouthCropper().processVideo(this.path, { width: 96, height: 96, fps: 25, maxDurationMs: 8_000 });
    this.cropDurationMs = result.durationMs;
    for (const frame of result.frames) yield frame;
  }

  async stop(): Promise<void> { this.stopped = true; }

  async dispose(): Promise<void> {
    this.stopped = true;
    const uri = this.path.startsWith("file://") ? this.path : `file://${this.path}`;
    await FileSystem.deleteAsync(uri, { idempotent: true }).catch(() => {});
  }
}
