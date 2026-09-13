"use client";

// Silent Input web recognizer — the browser counterpart to mobile's
// `UserMachineVSRRecognizer`. It speaks the agent's /vsr session protocol over
// the SAME authenticated AgentClient every other dashboard panel uses, so the
// remote path is a device-id choice rather than a second transport.

import type { AgentClient } from "@/lib/agent-client";
import { parseCapabilityGap, type CapabilityGap } from "@/lib/capabilityGap";
import type { WebMouthFrame } from "./capture";

const BATCH_SIZE = 8;

export type VSRCapabilities = {
  available: boolean;
  reason?: string;
  capabilityGap: CapabilityGap | null;
  frame?: { width: number; height: number; format: string; fps: number; maxFrames: number };
};

export type VSRRecognitionResult = {
  text: string;
  durationMs: number;
  metrics?: { transferMs?: number; inferenceMs?: number; totalMs?: number };
};

/** Ask the exact target machine whether VSR can execute, carrying the typed
 * capability gap so the panel can offer the real route instead of prose. */
export async function probeVSR(client: AgentClient): Promise<VSRCapabilities> {
  const response = await client.agentFetch("/vsr/capabilities", { method: "GET" });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(`/vsr/capabilities answered ${response.status}`);
  const raw = body as Record<string, unknown>;
  return {
    available: raw.available === true,
    reason: typeof raw.reason === "string" && raw.reason.trim() ? raw.reason : undefined,
    capabilityGap: parseCapabilityGap(raw.capabilityGap),
    frame: (raw.frame as VSRCapabilities["frame"]) ?? undefined,
  };
}

/** Run one inference: open a session, ship frame batches in order, stop, and
 * read the transcript. A failed batch or stop best-effort DELETEs the session
 * so mouth frames never outlive the request. */
export async function recognizeMouthFrames(
  client: AgentClient,
  frames: WebMouthFrame[],
): Promise<VSRRecognitionResult> {
  if (frames.length < BATCH_SIZE) {
    throw new Error(`Need at least ${BATCH_SIZE} mouth frames; captured ${frames.length}. Hold the recording a moment longer.`);
  }
  const started = performance.now();
  const startResponse = await client.agentFetch("/vsr/session/start", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      language: "en",
      contextualTerms: [],
      maxAlternatives: 3,
      width: 96,
      height: 96,
      format: "gray8",
    }),
  });
  const startBody = await startResponse.json().catch(() => ({}));
  if (!startResponse.ok) {
    throw new Error((startBody as { error?: string }).error || `/vsr/session/start answered ${startResponse.status}`);
  }
  const sessionId = (startBody as { sessionId?: string }).sessionId;
  if (!sessionId) throw new Error("/vsr/session/start returned no sessionId");

  try {
    const transferStarted = performance.now();
    for (let offset = 0, sequence = 0; offset < frames.length; offset += BATCH_SIZE, sequence += 1) {
      const response = await client.agentFetch(`/vsr/session/${encodeURIComponent(sessionId)}/frames`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sessionId, sequence, frames: frames.slice(offset, offset + BATCH_SIZE) }),
      });
      if (!response.ok) {
        const body = await response.json().catch(() => ({}));
        throw new Error((body as { error?: string }).error || `/vsr/session/${sessionId}/frames answered ${response.status}`);
      }
    }
    const transferMs = Math.round(performance.now() - transferStarted);

    const stopResponse = await client.agentFetch(`/vsr/session/${encodeURIComponent(sessionId)}/stop`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    });
    const stopBody = await stopResponse.json().catch(() => ({}));
    if (!stopResponse.ok) {
      throw new Error((stopBody as { error?: string }).error || `/vsr/session/${sessionId}/stop answered ${stopResponse.status}`);
    }
    const result = stopBody as { text?: string; durationMs?: number; metrics?: VSRRecognitionResult["metrics"] };
    return {
      text: String(result.text ?? ""),
      durationMs: result.durationMs ?? Math.round(performance.now() - started),
      metrics: { ...result.metrics, transferMs, totalMs: Math.round(performance.now() - started) },
    };
  } catch (error) {
    await client.agentFetch(`/vsr/session/${encodeURIComponent(sessionId)}`, { method: "DELETE" }).catch(() => {});
    throw error;
  }
}
