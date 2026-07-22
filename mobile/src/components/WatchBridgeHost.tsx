import React, { useEffect, useMemo } from "react";
import { NativeEventEmitter, NativeModules, Platform } from "react-native";
import { useDevice } from "../context/DeviceContext";
import { useAuth } from "../context/AuthContext";
import { makeRealCarVoiceDeps, type CarVoiceConfig, type CarVoiceTaskRef } from "../lib/carVoiceCoding";
import { connectionManager } from "../lib/connectionManager";
import { appLog } from "../lib/logger";
import { runtimeSurfaceClient } from "../lib/runtimeSurfaceClient";
import { watchBridgeBus } from "../lib/watchEntry";
import { isDeviceAsleep, wakeManagedDevice } from "../lib/wakeMachine";

type NativeWatchBridge = {
  sendToWatch?: (json: string) => void;
  addListener?: (eventName: string) => void;
  removeListeners?: (count: number) => void;
  consumePendingTurns?: () => Promise<string[] | undefined>;
};

function nativeBridge(): NativeWatchBridge | null {
  const mod = (NativeModules as { YaverWatchBridge?: NativeWatchBridge }).YaverWatchBridge;
  return mod && typeof mod.sendToWatch === "function" ? mod : null;
}

/** Resolve which managed machine the wrist's Wake should target: an explicit
 *  machineId from the message wins; else the focused/active box if it's a
 *  managed box that's asleep; else the first asleep managed box we know. */
function resolveWakeMachineId(devices: any[], activeDevice: any | null, explicit?: string): string | undefined {
  if (explicit) return explicit;
  if (activeDevice?.managed && activeDevice?.machineId) return activeDevice.machineId;
  const sleeping = devices.find((d) => isDeviceAsleep(d) && d?.machineId);
  if (sleeping?.machineId) return sleeping.machineId;
  const anyManaged = devices.find((d) => d?.managed && d?.machineId);
  return anyManaged?.machineId;
}

function pickDeviceId(devices: any[], activeDevice: any | null): string {
  const focused = connectionManager.focusedDeviceId();
  if (focused) return focused;
  const activeId = activeDevice?.id || activeDevice?.deviceId;
  if (activeId) return activeId;
  const connected = connectionManager.connectedDeviceIds()[0];
  if (connected) return connected;
  const online = devices.find((d) => d?.online);
  return online?.id || online?.deviceId || devices[0]?.id || devices[0]?.deviceId || "";
}

function makeWatchDeps(deviceId: string) {
  const config: CarVoiceConfig = {
    pollIntervalMs: 4000,
    maxWaitMs: 15 * 60 * 1000,
    speakAcknowledgement: false,
  };
  const deps = makeRealCarVoiceDeps({
    config,
    dispatchTask: async (title, prompt) => {
      if (!deviceId) throw new Error("No Yaver device selected");
      const client = connectionManager.clientFor(deviceId);
      const t = await client.sendTask(
        title,
        prompt,
        undefined,
        undefined,
        undefined,
        undefined,
        undefined,
        undefined,
        undefined,
        undefined,
        true,
      );
      return { id: t.id };
    },
    getTask: async (taskId): Promise<CarVoiceTaskRef> => {
      if (!deviceId) throw new Error("No Yaver device selected");
      const t = await connectionManager.clientFor(deviceId).getTask(taskId);
      return { id: t.id, status: t.status, resultText: t.resultText, output: t.output };
    },
  });
  return { deps, config };
}

export function WatchBridgeHost() {
  const deviceCtx = useDevice();
  const { token } = useAuth();
  const devices = (deviceCtx.devices as any[]) || [];
  const activeDevice = deviceCtx.activeDevice as any | null;
  const targetDeviceId = useMemo(
    () => pickDeviceId(devices, activeDevice),
    [devices, activeDevice],
  );

  useEffect(() => {
    const bridge = nativeBridge();
    if (!bridge) {
      watchBridgeBus.reset();
      return;
    }
    watchBridgeBus.configure({
      makeDeps: () => makeWatchDeps(targetDeviceId).deps,
      config: () => makeWatchDeps(targetDeviceId).config,
      ops: (verb, payload) => {
        if (verb === "meeting_next") return runtimeSurfaceClient.meetingNext(targetDeviceId, payload as any);
        if (verb === "meeting_join_next") return runtimeSurfaceClient.meetingJoinNext(targetDeviceId, payload as any);
        if (verb === "mail_unread") return runtimeSurfaceClient.mailUnread(targetDeviceId, payload as any);
        if (verb === "mail_send") return runtimeSurfaceClient.mailSend(targetDeviceId, payload as any);
        if (verb === "git_prs") return runtimeSurfaceClient.gitPRs(targetDeviceId, payload as any);
        if (verb === "git_issues") return runtimeSurfaceClient.gitIssues(targetDeviceId, payload as any);
        if (verb === "git_ci_status") return runtimeSurfaceClient.gitCIStatus(targetDeviceId, payload as any);
        if (verb === "git_connect") return runtimeSurfaceClient.gitConnect(targetDeviceId, payload as any);
        if (verb === "media_open") return runtimeSurfaceClient.mediaOpen(targetDeviceId, payload as any);
        if (verb === "maps_open") return runtimeSurfaceClient.mapsOpen(targetDeviceId, payload as any);
        throw new Error(`unsupported watch ops verb ${verb}`);
      },
      // The wrist can ask the phone to wake a parked managed box — the phone
      // holds the control-plane token; the watch then polls /health and drives
      // its own wake ladder to Ready.
      wakeBox: async (machineId) => {
        const mid = resolveWakeMachineId(devices, activeDevice, machineId);
        if (!mid) return { ok: false, error: "No parked managed box to wake." };
        return wakeManagedDevice(token, mid);
      },
      runtimeTurn: (request) => runtimeSurfaceClient.runtimeTurn(targetDeviceId, {
        ...request,
        target: {
          ...(request.target ?? {}),
          deviceId: targetDeviceId || request.target?.deviceId,
        },
      }),
      sender: (json) => bridge.sendToWatch?.(json),
    });
    return () => watchBridgeBus.reset();
    // token in deps so the wakeBox closure re-binds once auth is available.
  }, [targetDeviceId, token]);

  useEffect(() => {
    const bridge = nativeBridge();
    if (!bridge) return;
    if (Platform.OS !== "android" && Platform.OS !== "ios") return;
    const emitter = new NativeEventEmitter(bridge as any);
    const sub = emitter.addListener("yaverWatchMessage", (json: unknown) => {
      if (typeof json !== "string") return;
      void watchBridgeBus.deliver(json).catch((e) => {
        appLog("warn", `watch bridge delivery failed: ${e instanceof Error ? e.message : String(e)}`);
      });
    });

    // Drain any turns that arrived while the JS bridge was dead (app cold or
    // before this host mounted). The Wear listener service persists them in
    // SharedPreferences; consumePendingTurns pops + returns them. Mirrors the
    // car surface's consumePendingReplies pattern.
    if (typeof bridge.consumePendingTurns === "function") {
      void bridge.consumePendingTurns().then((turns) => {
        if (!Array.isArray(turns)) return;
        turns.forEach((json) => {
          if (typeof json === "string" && json) {
            void watchBridgeBus.deliver(json).catch(() => {});
          }
        });
      }).catch(() => {});
    }

    return () => sub.remove();
  }, []);

  return null;
}
