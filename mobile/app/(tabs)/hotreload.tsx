import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  NativeModules,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { SafeAreaView } from "react-native-safe-area-context";
import { useDevice } from "../../src/context/DeviceContext";
import { useColors } from "../../src/context/ThemeContext";
import {
  quicClient,
  type DevServerStatus,
  type IncidentEvent,
  type MobileWorkerPreviewSession,
  type OperationState,
} from "../../src/lib/quic";
import { loadApp } from "../../src/lib/bundleLoader";
import { formatGuestCrashReport, shouldShowGuestCrashReport, type GuestCrashReport } from "../../src/lib/guestCrash";
import { visibleReloadIncidents, visibleReloadOperations } from "../../src/lib/hotReloadState";
import { buildNativeBuildRequest, nativeBuildFailureMessage, nativeBuildFailureTitle } from "../../src/lib/nativeBuild";

// ── Types ──────────────────────────────────────────────────────────

interface ProjectItem {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  tags?: string[];
}

const DEV_FRAMEWORKS = ["expo", "flutter", "nextjs", "vite", "react-native", "react"];

const FRAMEWORK_ICONS: Record<string, string> = {
  expo: "\uD83D\uDCF1",
  "react-native": "\u269B",
  react: "\u269B",
  flutter: "\uD83D\uDC26",
  nextjs: "\u25B2",
  vite: "\u26A1",
};

const PREVIEW_TARGET_KEY = "@yaver/hotreload_preview_target";

function isHermesMobileFramework(framework?: string): boolean {
  return framework === "expo" || framework === "react-native";
}

function agentFlowGuidance(framework?: string): string | null {
  return null;
}

function currentYaverConsumerContract() {
  const info = (NativeModules as any)?.YaverInfo ?? {};
  return {
    consumerVersion: typeof info.version === "string" ? info.version : undefined,
    consumerBuild: typeof info.build === "string" ? info.build : undefined,
    consumerSdkVersion: typeof info.sdkVersion === "string" ? info.sdkVersion : undefined,
    consumerHermesBCVersion: typeof info.hermesBCVersion === "number" ? info.hermesBCVersion : undefined,
  };
}

function currentYaverGuestCrashReport(): GuestCrashReport | null {
  const info = (NativeModules as any)?.YaverInfo ?? {};
  const report = info?.lastGuestCrashReport;
  if (!report || typeof report !== "object") return null;
  return report as GuestCrashReport;
}

// ── Hot Reload Tab ────────────────────────────────────────────────

export default function HotReloadScreen() {
  const c = useColors();
  const { activeDevice, connectionStatus, devices } = useDevice();
  const isConnected = connectionStatus === "connected" && !!activeDevice;

  const [devStatus, setDevStatus] = useState<DevServerStatus | null>(null);
  const [workerSession, setWorkerSession] = useState<MobileWorkerPreviewSession | null>(null);
  const [projects, setProjects] = useState<ProjectItem[]>([]);
  const [projectsScanning, setProjectsScanning] = useState(false);
  const [scanStopping, setScanStopping] = useState(false);
  const [startingProject, setStartingProject] = useState<string | null>(null);
  // Tail of log lines streamed from /dev/events SSE. Bounded to 40
  // so a chatty Metro bundle doesn't blow up state; the card shows
  // the last ~6. Cleared when a new dev server starts.
  const [devLog, setDevLog] = useState<string[]>([]);
  const [reloadIncidents, setReloadIncidents] = useState<IncidentEvent[]>([]);
  const [reloadOperations, setReloadOperations] = useState<OperationState[]>([]);
  const [selectedTargetId, setSelectedTargetId] = useState<string | null>(null);
  // Stop UX state: "idle" → "stopping" (after Stop tapped) → "stopped" (post
  // /dev/stop with verified=true). Snaps back to "idle" after a 2s success
  // banner so the user gets a clear "agent really stopped" confirmation
  // without a permanent extra UI element. "error" surfaces when the agent
  // returns ok=false (e.g. subprocess didn't die within 7s).
  const [stopState, setStopState] = useState<"idle" | "stopping" | "stopped" | "error">("idle");
  const [stopMessage, setStopMessage] = useState<string>("");
  const [stopBuildsCancelled, setStopBuildsCancelled] = useState<number>(0);
  const mobileWorkers = devices.filter((d) => d.deviceClass === "edge-mobile");
  const selectedTarget = mobileWorkers.find((d) => d.id === selectedTargetId) || null;

  useEffect(() => {
    AsyncStorage.getItem(PREVIEW_TARGET_KEY)
      .then((value) => {
        if (value) setSelectedTargetId(value);
      })
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (!isConnected) return;
    let mounted = true;
    quicClient.getDevServerTarget()
      .then((target) => {
        if (!mounted) return;
        setSelectedTargetId(target?.targetDeviceId || null);
      })
      .catch(() => {});
    return () => { mounted = false; };
  }, [isConnected, activeDevice?.id]);

  useEffect(() => {
    if (selectedTargetId) {
      AsyncStorage.setItem(PREVIEW_TARGET_KEY, selectedTargetId).catch(() => {});
      return;
    }
    AsyncStorage.removeItem(PREVIEW_TARGET_KEY).catch(() => {});
  }, [selectedTargetId]);

  useEffect(() => {
    if (devStatus?.targetDeviceId) {
      setSelectedTargetId(devStatus.targetDeviceId);
      return;
    }
    if (!devStatus?.running && !devStatus?.building) {
      return;
    }
  }, [devStatus?.targetDeviceId, devStatus?.running, devStatus?.building]);

  // Poll dev server status + mobile projects
  useEffect(() => {
    if (!isConnected) return;
    let mounted = true;

    const poll = async () => {
      try {
        const [status, session] = await Promise.all([
          quicClient.getDevServerStatus(),
          quicClient.getMobileWorkerPreviewSession(),
        ]);
        if (mounted) setDevStatus(status?.running || status?.framework ? status : null);
        if (mounted) setWorkerSession(session);
      } catch {
        if (mounted) setDevStatus(null);
        if (mounted) setWorkerSession(null);
      }
      try {
        const [operations, incidents] = await Promise.all([
          quicClient.operations({ limit: 12 }),
          quicClient.incidents({ limit: 12 }),
        ]);
        if (!mounted) return;
        setReloadOperations(
          operations.filter((op) => op.kind === "reload" || op.kind === "reload_app" || op.kind === "build_native"),
        );
        setReloadIncidents(
          incidents.filter((incident) => incident.category === "reload" || incident.category === "build"),
        );
      } catch {
        if (!mounted) return;
        setReloadOperations([]);
        setReloadIncidents([]);
      }
    };

    // Fetch mobile projects from dedicated scanner (cached, refreshes in background)
    const fetchMobileProjects = async () => {
      try {
        const baseUrl = (quicClient as any).baseUrl;
        const headers = (quicClient as any).authHeaders;
        const res = await fetch(`${baseUrl}/projects/mobile`, { headers });
        const data = await res.json();
        if (mounted && data.ok && data.projects) {
          setProjectsScanning(!!data.scanning);
          setProjects(data.projects.map((p: any) => ({
            name: p.name,
            path: p.path,
            branch: p.branch,
            framework: p.framework,
            tags: [p.framework],
          })));
        }
      } catch {}
    };

    poll();
    fetchMobileProjects();
    const interval = setInterval(poll, 3000);
    const projectInterval = setInterval(fetchMobileProjects, projectsScanning ? 2500 : 15000);
    return () => { mounted = false; clearInterval(interval); clearInterval(projectInterval); };
  }, [isConnected, projectsScanning]);

  // Subscribe to /dev/events SSE so the user sees Metro / Expo /
  // Flutter output live while the dev server is coming up. Without
  // this, a slow-starting Metro looks like "expo · starting…" with
  // a spinner indefinitely — no signal of progress, no signal of
  // failure. Cap the tail at 40 lines to keep state bounded.
  useEffect(() => {
    if (!isConnected) return;
    setDevLog([]);
    const unsub = quicClient.subscribeDevEvents((ev) => {
      if (ev.type === "log" && ev.logLine) {
        setDevLog((prev) => {
          const next = [...prev, String(ev.logLine)];
          return next.length > 40 ? next.slice(next.length - 40) : next;
        });
      } else if (ev.type === "ready" || ev.type === "stopped") {
        setDevLog([]);
      } else if (ev.type === "error" && ev.message) {
        setDevLog((prev) => {
          const next = [...prev, `[error] ${ev.message}`];
          return next.length > 40 ? next.slice(next.length - 40) : next;
        });
      }
    });
    return () => unsub();
  }, [isConnected, activeDevice?.id]);


  const [nativeLoading, setNativeLoading] = useState(false);
  const runningGuidance = agentFlowGuidance(devStatus?.framework);

  const handleOpen = useCallback(async () => {
    const baseUrl = (quicClient as any).baseUrl as string;
    if (!baseUrl) {
      Alert.alert("Error", "Not connected to agent");
      return;
    }

    setNativeLoading(true);
    setLoadingStatus("Building HBC bundle...");
    // Agent caps Metro bundle at 8 min and hermesc at 3 min, so the worst
    // case is ~11 min. Give the client 12 min before aborting — anything
    // longer means the agent is unreachable, not slow. Without an abort
    // the fetch can hang forever on a crashed agent or dead relay, leaving
    // the Hot Reload UI stuck on "Building HBC bundle...".
    const buildAbort = new AbortController();
    const buildAbortTimer = setTimeout(() => buildAbort.abort(), 12 * 60 * 1000);
    try {
      const headers = {
        ...(quicClient as any).authHeaders,
        "Content-Type": "application/json",
      };

      // Step 1: Build production Hermes bytecode bundle (embedded hermesc BC96)
      const platform = Platform.OS;
      const buildRes = await fetch(`${baseUrl}/dev/build-native`, {
        method: "POST",
        headers,
        body: JSON.stringify(buildNativeBuildRequest(platform, currentYaverConsumerContract())),
        signal: buildAbort.signal,
      });
      clearTimeout(buildAbortTimer);
      const buildResult = await buildRes.json();

      if (buildResult.status !== "ok") {
        const error = new Error(nativeBuildFailureMessage(buildResult));
        (error as any).buildResult = buildResult;
        throw error;
      }

      const sizeKB = Math.round((buildResult.size || 0) / 1024);
      setLoadingStatus(`Built ${sizeKB}KB BC${buildResult.bcVersion || "?"}`);

      // Step 2: Download assets if available
      if (buildResult.hasAssets && buildResult.assetsUrl) {
        setLoadingStatus("Downloading assets...");
        try {
          const assetsRes = await fetch(`${baseUrl}${buildResult.assetsUrl}`, { headers });
          if (assetsRes.ok) {
            const assetsBlob = await assetsRes.blob();
            await fetch(`http://localhost:8347/assets`, {
              method: "POST",
              body: assetsBlob,
              headers: { "Content-Type": "application/zip" },
            });
          }
        } catch {
          // Non-fatal
        }
      }

      // Step 3: Download + validate HBC bundle (integrity checked via X-Yaver-Bundle-Metadata)
      setLoadingStatus(`Downloading ${sizeKB}KB bundle...`);
      const bundleUrl = `${baseUrl}${buildResult.bundleUrl}`;
      const moduleName = buildResult.moduleName || "main";
      await loadApp(bundleUrl, moduleName, (quicClient as any).authHeaders);

      // If we get here, bundle was validated (MD5 + BC version match)
      setLoadingStatus(`Loaded! MD5: ${(buildResult.md5 || "").slice(0, 8)}...`);
    } catch (err: any) {
      clearTimeout(buildAbortTimer);
      setLoadingStatus("");
      const aborted = err?.name === "AbortError" || buildAbort.signal.aborted;
      const message = aborted
        ? "Build did not respond in 12 minutes. The agent may be stuck or unreachable — check the project's node_modules and retry."
        : err?.message || "Could not load bundle in Yaver";
      const title = aborted
        ? "Build Timed Out"
        : err?.buildResult ? nativeBuildFailureTitle(err.buildResult) : "Load Failed";
      Alert.alert(title, message);
    } finally {
      setNativeLoading(false);
    }
  }, [devStatus]);

  const handleReload = useCallback(async () => {
    await quicClient.reloadDevServer();
  }, []);

  const handleStop = useCallback(() => {
    Alert.alert("Stop Dev Server", "Stop the running dev server?", [
      { text: "Cancel", style: "cancel" },
      {
        text: "Stop", style: "destructive", onPress: async () => {
          // Optimistic "Stopping…" pill so the user sees the agent is
          // working immediately. The card stays visible during this
          // window — no flicker into the project list and back out.
          setStopState("stopping");
          setStopMessage("");
          setStopBuildsCancelled(0);
          const res = await quicClient.stopDevServer();
          setDevStatus(null);
          if (!res || res.ok === false) {
            setStopState("error");
            setStopMessage(res?.error || res?.message || "Stop failed — check agent logs.");
            // Auto-dismiss the error banner after 5s so it doesn't pin
            // forever and confuse the next interaction.
            setTimeout(() => setStopState("idle"), 5000);
            return;
          }
          // Successful stop. agent 1.99.93+ returns verified + buildsCancelled.
          // Surface buildsCancelled when >0 so the user knows the in-flight
          // Hermes build was actually aborted (not just the dev server).
          setStopState("stopped");
          setStopBuildsCancelled(res.buildsCancelled || 0);
          if (res.verified === false) {
            setStopMessage("Sent SIGINT + SIGKILL but subprocess didn't confirm exit in 7s.");
            setStopState("error");
            setTimeout(() => setStopState("idle"), 5000);
            return;
          }
          // 2s success banner, then back to idle.
          setTimeout(() => {
            setStopState("idle");
            setStopMessage("");
            setStopBuildsCancelled(0);
          }, 2000);
        }
      },
    ]);
  }, []);

  const handleStopDiscovery = useCallback(async () => {
    try {
      setScanStopping(true);
      const baseUrl = (quicClient as any).baseUrl;
      const headers = (quicClient as any).authHeaders;
      await fetch(`${baseUrl}/projects/mobile`, { method: "DELETE", headers });
      setProjectsScanning(false);
    } catch {
      Alert.alert("Stop failed", "Could not stop project discovery right now.");
    } finally {
      setScanStopping(false);
    }
  }, []);

  const handleSelectTarget = useCallback(async (deviceId: string | null) => {
    const target = deviceId ? mobileWorkers.find((d) => d.id === deviceId) || null : null;
    setSelectedTargetId(deviceId);
    try {
      await quicClient.setDevServerTarget({
        targetDeviceId: target?.id,
        targetDeviceName: target?.name,
        targetDeviceClass: target?.deviceClass,
      });
    } catch {}
  }, [mobileWorkers]);

  const handleRequestScreenshot = useCallback(async () => {
    await quicClient.sendMobileWorkerPreviewCommand("capture_screenshot", {
      reason: "hotreload-control-plane",
    });
  }, []);

  // Tap project → start dev server directly using path + framework from scanner
  const handleStartProject = useCallback(async (project: ProjectItem) => {
    const isRunning = devStatus?.workDir === project.path;
    if (isRunning) {
      handleOpen();
      return;
    }

    setStartingProject(project.name);
    try {
      // Use the exact path from the mobile scanner — no name-based lookup needed
      await quicClient.startDevServer({
        framework: project.framework || "",
        workDir: project.path,
        targetDeviceId: selectedTarget?.id,
        targetDeviceName: selectedTarget?.name,
        targetDeviceClass: selectedTarget?.deviceClass,
      });
    } catch {
      Alert.alert("Failed", `Could not start dev server for ${project.name}`);
    } finally {
      setStartingProject(null);
    }
  }, [devStatus, handleOpen]);

  const [loadingStatus, setLoadingStatus] = useState("");
  const activeProjectPath = devStatus?.workDir ? String(devStatus.workDir).trim() : "";
  const visibleOperations = visibleReloadOperations(reloadOperations, activeProjectPath);
  const currentOperation = visibleOperations[0] || null;
  const visibleIncidents = visibleReloadIncidents(reloadIncidents, currentOperation, activeProjectPath);
  const currentIncident = visibleIncidents[0] || null;
  const lastGuestCrash = currentYaverGuestCrashReport();
  const showGuestCrashCard =
    shouldShowGuestCrashReport(lastGuestCrash) &&
    (!currentOperation || currentOperation.status !== "running") &&
    !currentIncident;
  // Match running workDir to project list to get the real app name (not directory name)
  const runningProject = (() => {
    if (!devStatus?.workDir) return devStatus?.framework ?? "App";
    const match = projects.find(p => p.path === devStatus.workDir);
    if (match) return match.name;
    return devStatus.workDir.split("/").pop() ?? "App";
  })();

  // All projects from /projects/mobile are already mobile-only
  const devProjects = projects;

  if (!isConnected) {
    return (
      <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <View style={s.emptyContainer}>
          <Text style={[s.emptyIcon, { color: c.textMuted }]}>{"\uD83D\uDD25"}</Text>
          <Text style={[s.emptyTitle, { color: c.textPrimary }]}>Not connected</Text>
          <Text style={[s.emptySubtitle, { color: c.textSecondary }]}>
            Connect to a device to hot reload your apps
          </Text>
        </View>
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <View style={s.container}>
        {mobileWorkers.length > 0 && (
          <>
            <Text style={[s.sectionTitle, { color: c.textMuted }]}>Preview Target</Text>
            <View style={[s.card, s.projectCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[s.projectName, { color: c.textPrimary }]}>Choose real-device Hermes target</Text>
              <Text style={[s.projectMeta, { color: c.textMuted, marginTop: 4 }]}>
                Default stays on this phone. Pick a spare mobile worker only when you want Hermes preview to target that device.
              </Text>
              <View style={s.targetChipRow}>
                <Pressable
                  onPress={() => { void handleSelectTarget(null); }}
                  style={[
                    s.targetChip,
                    {
                      borderColor: !selectedTarget ? c.accent : c.border,
                      backgroundColor: !selectedTarget ? c.accent + "22" : c.bg,
                    },
                  ]}
                >
                  <Text style={{ color: !selectedTarget ? c.accent : c.textSecondary, fontWeight: "600" }}>
                    This device
                  </Text>
                </Pressable>
                {mobileWorkers.map((worker) => (
                  <Pressable
                    key={worker.id}
                    onPress={() => { void handleSelectTarget(worker.id); }}
                    style={[
                      s.targetChip,
                      {
                        borderColor: selectedTarget?.id === worker.id ? c.accent : c.border,
                        backgroundColor: selectedTarget?.id === worker.id ? c.accent + "22" : c.bg,
                      },
                    ]}
                  >
                    <Text style={{ color: selectedTarget?.id === worker.id ? c.accent : c.textSecondary, fontWeight: "600" }}>
                      {worker.name}
                    </Text>
                  </Pressable>
                ))}
              </View>
            </View>
          </>
        )}

        {/* Running / starting dev server card */}
        {devStatus && (
          <View style={[
            s.card,
            s.activeCard,
            !devStatus.running && !devStatus.error && { borderColor: "#f59e0b44", backgroundColor: "#1a150f" },
            !!devStatus.error && { borderColor: "#ef4444", backgroundColor: "#1a0a0a" },
          ]}>
            <View style={s.cardHeader}>
              <View style={[s.statusDot, { backgroundColor: devStatus.error ? "#ef4444" : devStatus.running ? "#22c55e" : "#f59e0b" }]} />
              <View style={s.cardTitleContainer}>
                <Text style={s.cardTitle}>{runningProject}</Text>
                <Text style={s.cardMeta}>
                  {devStatus.error
                    ? `${devStatus.framework} · failed to start`
                    : devStatus.running
                      ? `${devStatus.framework} · port ${devStatus.port} · hot reload ${devStatus.hotReload ? "on" : "off"}`
                      : `${devStatus.framework} · starting...`}
                </Text>
                <Text style={[s.cardMeta, { color: "#86efac" }]}>
                  mode · {devStatus.iosInstallMethod === "native" ? "native install" : "Hermes bytecode"}
                </Text>
                {devStatus.iosInstallReason && devStatus.iosInstallMethod === "native" ? (
                  <Text style={[s.cardMeta, { color: "#d1d5db" }]}>
                    {devStatus.iosInstallReason}
                  </Text>
                ) : null}
                {runningGuidance ? (
                  <Text style={[s.cardMeta, { color: "#cbd5e1" }]}>
                    {runningGuidance}
                  </Text>
                ) : null}
                {(devStatus.targetDeviceName || selectedTarget?.name) && (
                  <Text style={[s.cardMeta, { color: "#7dd3fc" }]}>
                    target · {devStatus.targetDeviceName || selectedTarget?.name}
                  </Text>
                )}
                {workerSession?.hasTarget && (
                  <Text style={[s.cardMeta, { color: workerSession.workerOnline ? "#86efac" : "#fbbf24" }]}>
                    worker · {workerSession.workerOnline ? "online" : "offline"}
                  </Text>
                )}
                {!devStatus.targetDeviceName && !selectedTarget && (
                  <Text style={[s.cardMeta, { color: "#7dd3fc" }]}>
                    target · this device
                  </Text>
                )}
              </View>
              {!devStatus.running && !devStatus.error && <ActivityIndicator size="small" color="#f59e0b" />}
            </View>
            {loadingStatus ? (
              <Text style={{ color: "#9ca3af", fontSize: 11, marginTop: 4 }}>{loadingStatus}</Text>
            ) : null}
            {currentOperation ? (
              <View style={{ marginTop: 8, padding: 8, borderRadius: 6, backgroundColor: "#0b1220", borderWidth: 1, borderColor: "#1d4ed8" }}>
                <Text style={{ color: "#93c5fd", fontSize: 12, fontWeight: "600", marginBottom: 4 }}>
                  agent operation
                </Text>
                <Text style={{ color: "#dbeafe", fontSize: 11 }}>
                  {currentOperation.kind} · {currentOperation.status}
                  {currentOperation.phase ? ` · ${currentOperation.phase}` : ""}
                </Text>
                {currentOperation.message ? (
                  <Text style={{ color: "#bfdbfe", fontSize: 11, marginTop: 4 }}>
                    {currentOperation.message}
                  </Text>
                ) : null}
              </View>
            ) : null}
            {currentIncident ? (
              <View style={{ marginTop: 8, padding: 8, borderRadius: 6, backgroundColor: "#2a0a0a", borderWidth: 1, borderColor: "#ef444466" }}>
                <Text style={{ color: "#fca5a5", fontSize: 12, fontWeight: "600", marginBottom: 4 }}>
                  current blocker
                </Text>
                <Text style={{ color: "#fecaca", fontSize: 11, fontWeight: "600" }}>
                  {currentIncident.title || currentIncident.code}
                </Text>
                <Text style={{ color: "#fecaca", fontSize: 11, marginTop: 4 }}>
                  {currentIncident.userMessage}
                </Text>
                {currentIncident.suggestedAction ? (
                  <Text style={{ color: "#fda4af", fontSize: 10, marginTop: 4 }}>
                    Next: {currentIncident.suggestedAction}
                  </Text>
                ) : null}
              </View>
            ) : null}
            {showGuestCrashCard ? (
              <View style={{ marginTop: 8, padding: 8, borderRadius: 6, backgroundColor: "#1a1208", borderWidth: 1, borderColor: "#f59e0b66" }}>
                <Text style={{ color: "#fbbf24", fontSize: 12, fontWeight: "600", marginBottom: 4 }}>
                  last guest crash
                </Text>
                {formatGuestCrashReport(lastGuestCrash).map((line, index) => (
                  <Text key={`${index}-${line}`} style={{ color: "#fde68a", fontSize: 11, marginTop: index === 0 ? 0 : 4 }}>
                    {line}
                  </Text>
                ))}
              </View>
            ) : null}
            {/* Failure banner: shows the server-captured reason (stderr tail, missing tool, etc.) */}
            {devStatus.error ? (
              <View style={{ marginTop: 8, padding: 8, borderRadius: 6, backgroundColor: "#2a0a0a", borderWidth: 1, borderColor: "#ef444466" }}>
                <Text style={{ color: "#f87171", fontSize: 12, fontWeight: "600", marginBottom: 4 }}>
                  Start failed
                </Text>
                <Text style={{ color: "#fecaca", fontSize: 11, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" }} numberOfLines={10}>
                  {String(devStatus.error).trim()}
                </Text>
              </View>
            ) : null}
            {/* Live agent activity: Metro/Expo/Flutter stdout streamed over /dev/events SSE.
                Shows the last ~6 lines so the user sees progress during "starting…" and
                the actual log tail on failure. */}
            {!devStatus.running && devLog.length > 0 ? (
              <View style={{ marginTop: 8, padding: 8, borderRadius: 6, backgroundColor: "#0b0b0b", borderWidth: 1, borderColor: "#333" }}>
                <Text style={{ color: "#9ca3af", fontSize: 10, fontWeight: "600", marginBottom: 4 }}>
                  agent activity
                </Text>
                {devLog.slice(-6).map((line, i) => (
                  <Text
                    key={`${i}-${line.length}`}
                    style={{ color: "#cbd5e1", fontSize: 10, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" }}
                    numberOfLines={1}
                  >
                    {line}
                  </Text>
                ))}
              </View>
            ) : null}
            <View style={s.cardActions}>
              {devStatus.running && (
                <>
                  <Pressable style={[s.actionBtn, s.openBtn]} onPress={handleOpen} disabled={nativeLoading}>
                    {nativeLoading ? (
                      <ActivityIndicator size="small" color="#000" />
                    ) : (
                      <Text style={s.openBtnText}>Open in Yaver</Text>
                    )}
                  </Pressable>
                  <Pressable style={[s.actionBtn, s.reloadBtn]} onPress={handleReload}>
                    <Text style={s.reloadBtnText}>{"\u21BB"} Reload</Text>
                  </Pressable>
                  {workerSession?.hasTarget && workerSession.workerOnline && (
                    <Pressable style={[s.actionBtn, s.reloadBtn]} onPress={handleRequestScreenshot}>
                      <Text style={s.reloadBtnText}>Shot</Text>
                    </Pressable>
                  )}
                </>
              )}
              {devStatus.error && devStatus.workDir && devStatus.framework && (
                <Pressable
                  style={[s.actionBtn, s.reloadBtn]}
                  onPress={() => {
                    const framework = devStatus.framework || "";
                    const workDir = devStatus.workDir || "";
                    quicClient.startDevServer({
                      framework,
                      workDir,
                      targetDeviceId: selectedTarget?.id,
                      targetDeviceName: selectedTarget?.name,
                      targetDeviceClass: selectedTarget?.deviceClass,
                    }).catch(() => Alert.alert("Retry failed", "Could not restart the dev server"));
                  }}
                >
                  <Text style={s.reloadBtnText}>Retry</Text>
                </Pressable>
              )}
              <Pressable
                style={[s.actionBtn, s.stopBtn, stopState === "stopping" && { opacity: 0.6 }]}
                onPress={handleStop}
                disabled={stopState === "stopping"}
              >
                {stopState === "stopping" ? (
                  <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                    <ActivityIndicator size="small" color="#fff" />
                    <Text style={s.stopBtnText}>Stopping…</Text>
                  </View>
                ) : (
                  <Text style={s.stopBtnText}>Stop</Text>
                )}
              </Pressable>
            </View>
          </View>
        )}

        {/* Stop confirmation banner — surfaces "agent really stopped"
            after /dev/stop returns verified=true. Stays visible 2s, then
            self-dismisses. The card above this is gone (devStatus=null
            optimistic), so this banner is the only post-stop signal —
            without it the user has no idea whether the agent honored
            the request. */}
        {(stopState === "stopped" || stopState === "error") && (
          <View
            style={{
              marginHorizontal: 16,
              marginTop: 12,
              padding: 12,
              borderRadius: 10,
              borderWidth: StyleSheet.hairlineWidth,
              backgroundColor: stopState === "stopped" ? "#0f3a1f" : "#3a1f1f",
              borderColor: stopState === "stopped" ? "#16a34a" : "#dc2626",
              flexDirection: "row",
              alignItems: "center",
              gap: 10,
            }}
          >
            <Text style={{ fontSize: 18 }}>{stopState === "stopped" ? "✓" : "⚠"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={{ color: "#fff", fontSize: 13, fontWeight: "600" }}>
                {stopState === "stopped" ? "Dev server stopped" : "Stop incomplete"}
              </Text>
              <Text style={{ color: "#cbd5e1", fontSize: 11, marginTop: 2 }}>
                {stopState === "stopped"
                  ? stopBuildsCancelled > 0
                    ? `Subprocess confirmed exit. Cancelled ${stopBuildsCancelled} in-flight build${stopBuildsCancelled === 1 ? "" : "s"}.`
                    : "Subprocess confirmed exit. Tap a project to start again."
                  : stopMessage}
              </Text>
            </View>
          </View>
        )}

        {/* Available apps list */}
        <Text style={[s.sectionTitle, { color: c.textMuted }]}>
          {devStatus ? "Other Apps" : "Available Apps"}
        </Text>

        <FlatList
          data={devProjects.filter((p) => devStatus?.workDir !== p.path)}
          keyExtractor={(item) => item.path}
          contentContainerStyle={s.listContent}
          renderItem={({ item }) => {
            const isStarting = startingProject === item.name;
            const fwIcon = FRAMEWORK_ICONS[item.framework || ""] || "\u25B6";

            return (
              <Pressable
                style={[s.card, s.projectCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
                onPress={() => handleStartProject(item)}
                disabled={isStarting}
              >
                <View style={s.cardHeader}>
                  <Text style={s.frameworkIcon}>{fwIcon}</Text>
                  <View style={s.cardTitleContainer}>
                    <Text style={[s.projectName, { color: c.textPrimary }]}>{item.name}</Text>
                    <View style={s.tagRow}>
                      <View style={s.tag}>
                        <Text style={s.tagText}>{item.framework}</Text>
                      </View>
                    </View>
                    <Text style={[s.projectMeta, { color: c.textMuted }]} numberOfLines={1}>
                      {item.path}
                    </Text>
                  </View>
                  {isStarting ? (
                    <ActivityIndicator size="small" color={c.accent} />
                  ) : (
                    <Text style={{ color: c.textMuted, fontSize: 18, fontWeight: "300" }}>{"\u203A"}</Text>
                  )}
                </View>
              </Pressable>
            );
          }}
          ListEmptyComponent={
            <View style={s.emptyList}>
              <Text style={[s.emptySubtitle, { color: c.textMuted }]}>
                {projectsScanning
                  ? "Discovering mobile projects on your machine…"
                  : projects.length > 0
                  ? "No hot-reloadable projects found.\nLooking for Expo, Flutter, Next.js, or Vite projects."
                  : "No projects discovered yet.\nThe agent scans your home directory automatically."}
              </Text>
              <Pressable
                style={[s.rediscoverBtn, { borderColor: c.border, backgroundColor: c.bgCard }]}
                onPress={async () => {
                  try {
                    setProjectsScanning(true);
                    const baseUrl = (quicClient as any).baseUrl;
                    const headers = (quicClient as any).authHeaders;
                    await fetch(`${baseUrl}/projects/mobile`, { method: "POST", headers });
                  } catch {}
                }}
              >
                <Text style={[s.rediscoverBtnText, { color: c.textPrimary }]}>
                  {projectsScanning ? "Discovering..." : "Rediscover"}
                </Text>
              </Pressable>
              {projectsScanning ? (
                <Pressable
                  style={[s.rediscoverBtn, { borderColor: "#ef444466", backgroundColor: "#ef444411" }]}
                  onPress={() => { void handleStopDiscovery(); }}
                  disabled={scanStopping}
                >
                  <Text style={[s.rediscoverBtnText, { color: "#f87171" }]}>
                    {scanStopping ? "Stopping..." : "Stop Discovery"}
                  </Text>
                </Pressable>
              ) : null}
            </View>
          }
        />
      </View>

    </SafeAreaView>
  );
}

// ── Styles ─────────────────────────────────────────────────────────

const s = StyleSheet.create({
  safe: { flex: 1 },
  container: { flex: 1 },

  // Section
  sectionTitle: {
    fontSize: 11,
    fontWeight: "600",
    textTransform: "uppercase",
    letterSpacing: 1,
    marginHorizontal: 16,
    marginTop: 20,
    marginBottom: 8,
  },

  // Cards
  card: { marginHorizontal: 16, borderRadius: 12, padding: 14, marginBottom: 8 },
  activeCard: {
    backgroundColor: "#0f1a0f",
    borderWidth: 1,
    borderColor: "#22c55e44",
    marginTop: 12,
  },
  cardHeader: { flexDirection: "row", alignItems: "center", gap: 10 },
  cardTitleContainer: { flex: 1 },
  cardTitle: { fontSize: 16, fontWeight: "700", color: "#fff" },
  cardMeta: { fontSize: 11, color: "#666", marginTop: 2 },
  statusDot: { width: 8, height: 8, borderRadius: 4 },

  cardActions: { flexDirection: "row", gap: 8, marginTop: 12 },
  actionBtn: { paddingVertical: 8, paddingHorizontal: 14, borderRadius: 8 },
  openBtn: { backgroundColor: "#22c55e", flex: 1, alignItems: "center" },
  openBtnText: { color: "#000", fontSize: 13, fontWeight: "700" },
  reloadBtn: { backgroundColor: "#22c55e22", flex: 1, alignItems: "center" },
  reloadBtnText: { color: "#22c55e", fontSize: 13, fontWeight: "600" },
  stopBtn: { backgroundColor: "#ef444422", paddingHorizontal: 16, alignItems: "center" },
  stopBtnText: { color: "#ef4444", fontSize: 13, fontWeight: "600" },

  // Framework icon
  frameworkIcon: { fontSize: 20 },

  // Tag chips
  tagRow: { flexDirection: "row", flexWrap: "wrap", gap: 4, marginTop: 3 },
  tag: {
    backgroundColor: "#6366f115",
    borderRadius: 4,
    paddingHorizontal: 5,
    paddingVertical: 1,
  },
  tagText: { color: "#818cf8", fontSize: 9, fontWeight: "600" },

  // Project cards
  projectCard: { borderWidth: 1 },
  projectName: { fontSize: 14, fontWeight: "600" },
  projectMeta: { fontSize: 11, marginTop: 1 },
  listContent: { paddingBottom: 40 },
  targetChipRow: { flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 12 },
  targetChip: {
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 10,
    paddingVertical: 6,
  },

  // Empty
  emptyContainer: { flex: 1, alignItems: "center", justifyContent: "center", padding: 40 },
  emptyIcon: { fontSize: 40, marginBottom: 12 },
  emptyTitle: { fontSize: 18, fontWeight: "700", marginBottom: 4 },
  emptySubtitle: { fontSize: 13, textAlign: "center", lineHeight: 20 },
  emptyList: { padding: 40, alignItems: "center" },
  rediscoverBtn: { marginTop: 14, borderWidth: 1, borderRadius: 10, paddingHorizontal: 16, paddingVertical: 10 },
  rediscoverBtnText: { fontSize: 14, fontWeight: "600" },

});
