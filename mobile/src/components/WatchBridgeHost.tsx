import React, { useEffect, useMemo } from "react";
import { NativeEventEmitter, NativeModules, Platform } from "react-native";
import { useDevice } from "../context/DeviceContext";
import { makeRealCarVoiceDeps, type CarVoiceConfig, type CarVoiceTaskRef } from "../lib/carVoiceCoding";
import { connectionManager } from "../lib/connectionManager";
import { appLog } from "../lib/logger";
import { runtimeSurfaceClient } from "../lib/runtimeSurfaceClient";
import { watchBridgeBus } from "../lib/watchEntry";

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
        if (verb === "media_open") return runtimeSurfaceClient.mediaOpen(targetDeviceId, payload as any);
        if (verb === "maps_open") return runtimeSurfaceClient.mapsOpen(targetDeviceId, payload as any);
        throw new Error(`unsupported watch ops verb ${verb}`);
      },
      sender: (json) => bridge.sendToWatch?.(json),
    });
    return () => watchBridgeBus.reset();
  }, [targetDeviceId]);

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
