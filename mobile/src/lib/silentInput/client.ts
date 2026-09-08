import { parseCapabilityGap, type CapabilityGap } from "../capabilityGap";
import { connectionManager } from "../connectionManager";
import type { MouthFrame, VSRRecognitionOptions, VSRResult, VisualSpeechRecognizer } from "./types";

const BATCH_SIZE = 8;

async function responseJSON<T>(response: Response): Promise<T> {
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error((body as { error?: string }).error || `VSR request failed (${response.status})`);
  return body as T;
}

export type VSRCapabilities = {
  available: boolean;
  backend: "user-machine";
  language: "en";
  mouthCropOnly: boolean;
  reason?: string;
  capabilityGap: CapabilityGap | null;
};

/** Ask the exact target machine whether remote VSR can execute. The response
 * carries the same typed capability gap used by preview/toolchain recovery, so
 * mobile can invoke and stream /install/vsr instead of flattening it to prose. */
export async function probeVSRCapabilities(targetDeviceId: string): Promise<VSRCapabilities> {
  const client = connectionManager.clientFor(targetDeviceId);
  const response = await client.agentRequest(targetDeviceId, "/vsr/capabilities", { method: "GET" }, 5_000);
  const raw = await responseJSON<Record<string, unknown>>(response);
  return {
    available: raw.available === true,
    backend: "user-machine",
    language: "en",
    mouthCropOnly: raw.mouthCropOnly !== false,
    reason: typeof raw.reason === "string" && raw.reason.trim() ? raw.reason : undefined,
    capabilityGap: parseCapabilityGap(raw.capabilityGap),
  };
}

export class UserMachineVSRRecognizer implements VisualSpeechRecognizer {
  constructor(private readonly targetDeviceId: string) {}

  async initialize(): Promise<void> {
    const capability = await probeVSRCapabilities(this.targetDeviceId);
    if (!capability.available) throw new Error(capability.reason || "Visual speech recognition is not installed on this machine.");
  }

  async recognize(frames: MouthFrame[], options: VSRRecognitionOptions = {}): Promise<VSRResult> {
    const started = Date.now();
    const client = connectionManager.clientFor(this.targetDeviceId);
    const startResponse = await client.agentRequest(this.targetDeviceId, "/vsr/session/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        language: options.language || "en",
        contextualTerms: options.contextualTerms || [],
        maxAlternatives: options.maxAlternatives || 3,
        width: 96,
        height: 96,
        format: "gray8",
      }),
    });
    const { sessionId } = await responseJSON<{ sessionId: string }>(startResponse);
    try {
      const transferStarted = Date.now();
      for (let offset = 0, sequence = 0; offset < frames.length; offset += BATCH_SIZE, sequence += 1) {
        const response = await client.agentRequest(this.targetDeviceId, `/vsr/session/${encodeURIComponent(sessionId)}/frames`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ sessionId, sequence, frames: frames.slice(offset, offset + BATCH_SIZE) }),
        }, 15_000);
        await responseJSON(response);
      }
      const transferMs = Date.now() - transferStarted;
      const response = await client.agentRequest(this.targetDeviceId, `/vsr/session/${encodeURIComponent(sessionId)}/stop`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      }, 120_000);
      const result = await responseJSON<VSRResult>(response);
      return { ...result, metrics: { ...result.metrics, transferMs, totalMs: Date.now() - started } };
    } catch (error) {
      // Best-effort privacy cleanup if a batch or inference request fails.
      await client.agentRequest(this.targetDeviceId, `/vsr/session/${encodeURIComponent(sessionId)}`, { method: "DELETE" }, 5_000).catch(() => {});
      throw error;
    }
  }

  async dispose(): Promise<void> {}
}
