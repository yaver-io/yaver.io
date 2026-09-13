"use client";

// SilentInputPanel — the macOS/web front-camera test surface for VSR.
//
// WHY IT EXISTS. Silent Input shipped iOS-only, so the only way to exercise
// lip reading was a physical iPhone, and a missing model surfaced as a bare
// "VSR request failed (404)" with no route to a fix. This panel makes the
// capture surface the browser the Electron macOS app already runs, targets any
// Yaver device (local or remote) as the recognizer, and renders the agent's
// typed capability gap — so "no licensed model configured" is a named state
// with its constraint, not a dead end.

import { useCallback, useEffect, useRef, useState } from "react";
import { agentClientPool } from "@/lib/agent-client";
import { gapBody, gapConstraint, gapFixLabel, gapTitle, type CapabilityGap } from "@/lib/capabilityGap";
import type { Device } from "@/lib/use-devices";
import { cameraSupported, captureMouthFrames, VSR_MAX_DURATION_MS } from "@/lib/silentInput/capture";
import { probeVSR, recognizeMouthFrames, type VSRCapabilities } from "@/lib/silentInput/recognizer";

const MUTED = "#8b949e";
const BORDER = "rgba(139,148,158,0.3)";

export function SilentInputPanel({ device, token }: { device: Device; token: string | null }) {
  const [capability, setCapability] = useState<VSRCapabilities | null>(null);
  const [checking, setChecking] = useState(false);
  const [recording, setRecording] = useState(false);
  const [transcript, setTranscript] = useState<string | null>(null);
  const [frameCount, setFrameCount] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  const clientForDevice = useCallback(async () => {
    const client = agentClientPool.get(device.id);
    if (!client.isConnected) {
      if (!token) throw new Error("Sign in again to reach this machine.");
      await client.connect(device.host, device.port, token, device.id, { tunnelUrls: device.publicEndpoints });
    }
    return client;
  }, [device.id, device.host, device.port, device.publicEndpoints, token]);

  const check = useCallback(async () => {
    setChecking(true);
    setError(null);
    try {
      const client = await clientForDevice();
      setCapability(await probeVSR(client));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setChecking(false);
    }
  }, [clientForDevice]);

  useEffect(() => {
    void check();
    // Re-probe only when the target device changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [device.id]);

  const runCameraTest = useCallback(async () => {
    setError(null);
    setTranscript(null);
    setFrameCount(null);
    try {
      const client = await clientForDevice();
      const controller = new AbortController();
      abortRef.current = controller;
      setRecording(true);
      const frames = await captureMouthFrames({ durationMs: VSR_MAX_DURATION_MS, signal: controller.signal });
      setFrameCount(frames.length);
      const result = await recognizeMouthFrames(client, frames);
      setTranscript(result.text || "(empty transcript)");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setRecording(false);
      abortRef.current = null;
    }
  }, [clientForDevice]);

  const stopRecording = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  const gap: CapabilityGap | null = capability?.capabilityGap ?? null;
  const available = capability?.available === true;

  return (
    <div style={{ borderTop: `1px solid ${BORDER}`, marginTop: 14, paddingTop: 14 }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
        <div style={{ fontWeight: 700, fontSize: 13 }}>Silent Input · lip reading</div>
        <button
          type="button"
          onClick={() => void check()}
          disabled={checking}
          style={{
            background: "transparent",
            color: MUTED,
            border: `1px solid ${BORDER}`,
            borderRadius: 8,
            padding: "4px 10px",
            fontSize: 12,
            cursor: checking ? "default" : "pointer",
            opacity: checking ? 0.5 : 1,
          }}
        >
          {checking ? "Checking…" : "Check again"}
        </button>
      </div>

      <div style={{ color: MUTED, fontSize: 12, marginTop: 6, lineHeight: 1.5 }}>
        {available
          ? "Ready. Test silently mouths a short phrase using this browser's front camera; no audio is captured and only temporary 96×96 grayscale mouth crops cross your encrypted Yaver connection."
          : "Type without speaking. Turn lip reading on for this machine, then test with the front camera."}
      </div>

      {/* The typed capability gap is rendered generically: named cause, body,
          constraint, and the agent's own fix label. No prose reinvention. */}
      {gap ? (
        <div style={{ marginTop: 10, padding: 10, border: `1px solid ${BORDER}`, borderRadius: 8 }}>
          <div style={{ fontSize: 12, fontWeight: 700 }}>{gapTitle(gap)}</div>
          {gapBody(gap) ? <div style={{ color: MUTED, fontSize: 12, marginTop: 4 }}>{gapBody(gap)}</div> : null}
          {gapConstraint(gap) ? <div style={{ color: MUTED, fontSize: 12, marginTop: 4 }}>{gapConstraint(gap)}</div> : null}
                    {gap.fix ? (
            <button
              type="button"
              onClick={() =>
                void clientForDevice()
                  .then((client) => client.invokeGapFix(gap.fix!.method || "POST", gap.fix!.path, gap.fix!.body ?? undefined))
                  .then(check)
                  .catch((e) => setError(e instanceof Error ? e.message : String(e)))
              }
              style={{
                marginTop: 8,
                background: "transparent",
                color: "inherit",
                border: `1px solid ${BORDER}`,
                borderRadius: 8,
                padding: "5px 12px",
                fontSize: 12,
                fontWeight: 700,
                cursor: "pointer",
              }}
            >
              {gapFixLabel(gap)}
            </button>
          ) : null}
        </div>
      ) : null}

      <div style={{ display: "flex", gap: 8, marginTop: 10, flexWrap: "wrap" }}>
        <button
          type="button"
          onClick={() => (recording ? stopRecording() : void runCameraTest())}
          disabled={!cameraSupported()}
          style={{
            background: recording ? "rgba(248,81,73,0.15)" : "transparent",
            color: recording ? "#f85149" : "inherit",
            border: `1px solid ${BORDER}`,
            borderRadius: 8,
            padding: "6px 14px",
            fontSize: 12,
            fontWeight: 700,
            cursor: cameraSupported() ? "pointer" : "not-allowed",
            opacity: cameraSupported() ? 1 : 0.5,
          }}
        >
          {recording ? "Stop" : "Test with front camera"}
        </button>
      </div>

      {!cameraSupported() ? (
        <div style={{ color: MUTED, fontSize: 12, marginTop: 8 }}>
          This browser exposes no camera. Open the dashboard in a real browser on a camera-equipped machine.
        </div>
      ) : null}
      {frameCount !== null ? (
        <div style={{ color: MUTED, fontSize: 12, marginTop: 8 }}>{frameCount} mouth frames captured.</div>
      ) : null}
      {transcript !== null ? (
        <div style={{ marginTop: 8, padding: 10, border: `1px solid ${BORDER}`, borderRadius: 8, fontSize: 13 }}>{transcript}</div>
      ) : null}
      {error ? <div style={{ color: "#f85149", fontSize: 12, marginTop: 8 }}>{error}</div> : null}
    </div>
  );
}
