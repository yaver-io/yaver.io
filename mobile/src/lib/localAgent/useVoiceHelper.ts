// localAgent/useVoiceHelper.ts — the RN hook that drives the voice helper end
// to end: push-to-talk STT (whisper.rn) → the pure voiceSession orchestrator →
// safe dispatch (adapter) → TTS. RN-bound (React + speech.ts + DeviceContext +
// quicClient) so it is NOT in the pure barrel and NOT tsx-tested; all the logic
// it drives is tested in voiceSession/adapter/capabilityLadder.
//
// Model: `complete` is null today — the helper runs in SCRIPTED mode (keyword
// goals + the deterministic ladder), which already onboards, troubleshoots, and
// runs safe device actions. When llama.rn lands, wire engine.loadModel(...).
// complete into the session for the free-form direct-command path.

import { useCallback, useRef, useState } from "react";

import { useDevice, type Device, type DeviceState } from "../../context/DeviceContext";
import { startRealtimeTranscribe, speakText } from "../speech";
import {
  buildLadderState,
  dispatchAction,
  type DeviceLike,
  type DeviceStateLike,
} from "./adapter";
import { makeDispatchDeps, probeDevice } from "./adapterBindings";
import { createVoiceSession, type VoiceTurnResult } from "./voiceSession";
import type { DeviceRef } from "./resolver";
import type { ModelTier } from "./tiers";

// ── mappers: live Device/DeviceState → the adapter/resolver shapes ──────────
function toDeviceLike(d: Device): DeviceLike {
  return {
    id: d.id,
    name: d.name,
    alias: d.alias,
    os: d.os,
    online: d.online,
    lastSeen: d.lastSeen,
    needsAuth: d.needsAuth,
    peerState: d.peerState,
  };
}

function toDeviceRef(d: Device, ds: DeviceState): DeviceRef {
  return {
    deviceId: d.id,
    name: d.name,
    alias: d.alias,
    platform: d.os,
    online: d.online,
    isPrimary: ds.primaryDeviceId === d.id,
    isSecondary: ds.secondaryDeviceId === d.id,
    isPhone: d.deviceClass === "edge-mobile",
  };
}

function toDeviceStateLike(ds: DeviceState): DeviceStateLike {
  return {
    devices: ds.devices.map(toDeviceLike),
    activeDevice: ds.activeDevice ? toDeviceLike(ds.activeDevice) : null,
    connectionStatus: ds.connectionStatus,
    lastError: ds.lastError,
    agentAuthExpired: ds.agentAuthExpired,
    unreachableDeviceIds: ds.unreachableDeviceIds,
    manualAuthRequiredDeviceIds: ds.manualAuthRequiredDeviceIds,
    connectedDeviceIds: ds.connectedDeviceIds,
  };
}

export interface UseVoiceHelper {
  listening: boolean;
  /** The last line the helper spoke (for an on-screen caption). */
  lastSpoken: string;
  /** What the helper is waiting on, if anything. */
  awaiting?: VoiceTurnResult["awaiting"];
  thinking: boolean;
  startListening: () => Promise<void>;
  /** Stop capture, run the turn, speak the result. */
  stopListening: () => Promise<void>;
  /** Feed a typed/explicit utterance (e.g. a tapped suggestion) without STT. */
  say: (text: string) => Promise<VoiceTurnResult>;
  reset: () => void;
}

export function useVoiceHelper(opts: { localTier?: ModelTier } = {}): UseVoiceHelper {
  const ds = useDevice();
  // Keep the session stable but always reading the LATEST device state.
  const dsRef = useRef<DeviceState>(ds);
  dsRef.current = ds;

  const [listening, setListening] = useState(false);
  const [thinking, setThinking] = useState(false);
  const [lastSpoken, setLastSpoken] = useState("");
  const [awaiting, setAwaiting] = useState<VoiceTurnResult["awaiting"]>(undefined);

  const recRef = useRef<{ stop: () => Promise<string> } | null>(null);
  const sessionRef = useRef<ReturnType<typeof createVoiceSession> | null>(null);

  if (!sessionRef.current) {
    sessionRef.current = createVoiceSession({
      devices: () => dsRef.current.devices.map((d) => toDeviceRef(d, dsRef.current)),

      ladderState: async (targetId) => {
        const cur = dsRef.current;
        const target = targetId
          ? cur.devices.find((d) => d.id === targetId)
          : cur.activeDevice ?? undefined;
        // Probe only a connected target (audit/projects need a live agent).
        const connected = target ? cur.connectedDeviceIds?.includes(target.id) : false;
        const probe = connected ? await probeDevice().catch(() => undefined) : undefined;
        return buildLadderState(toDeviceStateLike(cur), {
          online: true, // RN NetInfo could refine this; the spine handles offline anyway.
          localTier: opts.localTier ?? "router",
          target: target ? toDeviceLike(target) : null,
          probe,
        });
      },

      dispatch: async (actionId, o, confirmed) => {
        const cur = dsRef.current;
        const device = o.deviceId
          ? cur.devices.find((d) => d.id === o.deviceId)
          : cur.activeDevice ?? undefined;
        const deps = makeDispatchDeps(cur, { confirmed });
        return dispatchAction(
          actionId,
          { device: device ? toDeviceLike(device) : undefined, args: o.args },
          deps,
        );
      },

      speak: (t) => {
        setLastSpoken(t);
        void speakText(t, { provider: "device" }).catch(() => {});
      },

      // null → scripted mode. Wire engine.loadModel(path).complete here when
      // the bundled router GGUF + llama.rn are present on the build.
      complete: null,
    });
  }

  const runTurn = useCallback(async (text: string): Promise<VoiceTurnResult> => {
    setThinking(true);
    try {
      const result = await sessionRef.current!.handle(text);
      setAwaiting(result.awaiting);
      return result;
    } finally {
      setThinking(false);
    }
  }, []);

  const startListening = useCallback(async () => {
    if (recRef.current) return;
    setListening(true);
    try {
      recRef.current = await startRealtimeTranscribe(() => {});
    } catch (e: any) {
      setListening(false);
      await runTurn(""); // surface a graceful "I didn't catch that"
    }
  }, [runTurn]);

  const stopListening = useCallback(async () => {
    const rec = recRef.current;
    recRef.current = null;
    setListening(false);
    if (!rec) return;
    let finalText = "";
    try {
      finalText = await rec.stop();
    } catch {
      // fall through with empty → graceful handling
    }
    await runTurn(finalText);
  }, [runTurn]);

  const reset = useCallback(() => {
    sessionRef.current?.reset();
    setAwaiting(undefined);
  }, []);

  return {
    listening,
    lastSpoken,
    awaiting,
    thinking,
    startListening,
    stopListening,
    say: runTurn,
    reset,
  };
}
