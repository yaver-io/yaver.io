import React, { useEffect, useMemo } from "react";
import { NativeEventEmitter, NativeModules, Platform } from "react-native";
import { useDevice } from "../context/DeviceContext";
import { useAuth } from "../context/AuthContext";
import { getUserSettings, saveUserSettings } from "../lib/auth";
import { makeRealCarVoiceDeps, type CarVoiceConfig, type CarVoiceTaskRef } from "../lib/carVoiceCoding";
import { connectionManager } from "../lib/connectionManager";
import { goalFromSlashCommand } from "../lib/goalSlashCommand";
import { appLog } from "../lib/logger";
import { runtimeSurfaceClient } from "../lib/runtimeSurfaceClient";
import { loadKeepLastProjectEnabled, loadLastTaskProject, loadLastTaskProjectFromConvex, loadMCPServersFromConvex, loadUseLatestMCPEnabled } from "../lib/taskComposerPrefs";
import { watchBridgeBus } from "../lib/watchEntry";
import { mobileSessionSettings } from "../lib/appVersion";

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
  // Runner/render split: watch-dictated tasks run on the configured AI
  // runner box first — same precedence every other dispatch surface uses.
  const roleRunner = connectionManager.roleDeviceId("runner");
  if (roleRunner) return roleRunner;
  const focused = connectionManager.focusedDeviceId();
  if (focused) return focused;
  const activeId = activeDevice?.id || activeDevice?.deviceId;
  if (activeId) return activeId;
  const connected = connectionManager.connectedDeviceIds()[0];
  if (connected) return connected;
  const online = devices.find((d) => d?.online);
  return online?.id || online?.deviceId || devices[0]?.id || devices[0]?.deviceId || "";
}

function makeWatchDeps(
  deviceId: string,
  token: string | null | undefined,
  preference: { runner?: string; model?: string; reasoningEffort?: string; mode?: string },
) {
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
      // Convex-first: telefon/web'te seçilen son proje bilekte de geri gelir
      // (defaultRuntimeProjectByDevice); AsyncStorage offline fallback.
      const convexLast = token ? await loadLastTaskProjectFromConvex(token, deviceId) : null;
      const lastProject = (await loadKeepLastProjectEnabled()) ? (convexLast ?? (await loadLastTaskProject(deviceId))) : null;
      // Remembered MCP scope is opt-in. Wrist-created tasks otherwise start
      // with No MCP, matching phone/TV/web rather than silently reusing tools.
      const useLatestMCP = await loadUseLatestMCPEnabled();
      const mcpPref = useLatestMCP && token ? await loadMCPServersFromConvex(token, deviceId) : null;
      const mcpServers = mcpPref?.mcpServers ?? [];
      const includeYaverMcp = mcpPref?.includeYaverMcp ?? false;
      // The wrist has no model-inventory wall. It silently uses the same
      // Convex-backed per-machine favorite selected on phone/web/TV.
      const goalIntent = goalFromSlashCommand(prompt, preference.runner || "opencode");
      const goalText = goalIntent?.goal ?? "";
      const t = await client.sendTask(
        goalIntent ? goalText : title,
        goalIntent ? goalText : prompt,
        preference.model,
        preference.runner,
        undefined,
        undefined,
        undefined,
        lastProject?.path,
        preference.mode,
        undefined,
        true,
        undefined,
        lastProject?.name,
        mcpServers,
        goalIntent ? goalText : undefined,
        includeYaverMcp,
        undefined,
        undefined,
        "tasks",
        Platform.OS === "ios" ? "watchos" : "wearos",
        mobileSessionSettings({
          surface: Platform.OS === "ios" ? "apple-watch" : "wear-os",
          platform: Platform.OS === "ios" ? "watchos" : "wearos",
          deviceClass: "watch",
        }),
        preference.reasoningEffort as "none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra" | undefined,
      );
      return { id: t.id };
    },
    getTask: async (taskId): Promise<CarVoiceTaskRef> => {
      if (!deviceId) throw new Error("No Yaver device selected");
      const t = await connectionManager.clientFor(deviceId).getTask(taskId);
      return { id: t.id, status: t.status, resultText: t.resultText, presentation: t.presentation, output: t.output };
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
  const codingPreference = useMemo(() => ({
    runner: targetDeviceId ? deviceCtx.primaryRunnerByDevice[targetDeviceId] : undefined,
    model: targetDeviceId ? deviceCtx.primaryModelByDevice[targetDeviceId] : undefined,
    reasoningEffort: targetDeviceId ? deviceCtx.primaryReasoningEffortByDevice[targetDeviceId] : undefined,
    mode: targetDeviceId ? deviceCtx.primaryModeByDevice[targetDeviceId] : undefined,
  }), [
    deviceCtx.primaryModeByDevice,
    deviceCtx.primaryModelByDevice,
    deviceCtx.primaryReasoningEffortByDevice,
    deviceCtx.primaryRunnerByDevice,
    targetDeviceId,
  ]);

  useEffect(() => {
    const bridge = nativeBridge();
    if (!bridge) {
      watchBridgeBus.reset();
      return;
    }
    watchBridgeBus.configure({
      makeDeps: () => makeWatchDeps(targetDeviceId, token, codingPreference).deps,
      config: () => makeWatchDeps(targetDeviceId, token, codingPreference).config,
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
      appearance: async (theme) => {
        if (!token) return { ok: false, error: "Sign in to Yaver on your phone first." };
        const surface = Platform.OS === "ios" ? "watchos" : "wearos";
        if (theme) {
          await saveUserSettings(token, { appearanceThemeForSurface: { surface, theme } });
          return { ok: true, theme };
        }
        const settings = await getUserSettings(token);
        const saved = settings.appearanceThemeBySurface?.find((row) => row.surface === surface)?.theme;
        return { ok: true, theme: saved === "light" ? "light" : "dark" };
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
  }, [codingPreference, targetDeviceId, token]);

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
