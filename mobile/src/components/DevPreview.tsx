import React, { useCallback, useEffect, useRef, useState } from "react";
import { Ionicons } from "@expo/vector-icons";
import {
  ActivityIndicator,
  Alert,
  Linking,
  Modal,
  NativeModules,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { WebView } from "react-native-webview";
import { quicClient, type DevServerStatus } from "../lib/quic";
import { useColors } from "../context/ThemeContext";
import { isBundleLoaded, loadAppIfChanged, onBundleEvent } from "../lib/bundleLoader";
import { buildNativeBuildRequest, nativeBuildFailureMessage, nativeBuildFailureTitle } from "../lib/nativeBuild";
import { isActiveDevServerStatus } from "../lib/devServerState";
import { useResponsiveLayout } from "../hooks/useResponsiveLayout";
import { monoFamily } from "../theme/tokens";

/**
 * Dev Preview.
 *
 * Expo / React Native in the Yaver mobile app must stay on the native
 * Hermes bridge path. WebView is only for browser-style web projects.
 *
 * Flow:
 * 1. Agent starts a dev server (via POST /dev/start or Claude Code task)
 * 2. DevPreview polls /dev/status and detects it
 * 3. Native mobile projects use "Open in Yaver" + Hermes reload
 * 4. Web projects open a full-screen WebView through relay
 */
function isHermesNativeFramework(status: DevServerStatus | null): boolean {
  const framework = String(status?.framework || "").toLowerCase();
  return framework.includes("expo") || framework.includes("react-native");
}

function currentYaverConsumerContract() {
  const info = (NativeModules as any)?.YaverInfo ?? {};
  return {
    consumerVersion: typeof info.version === "string" ? info.version : undefined,
    consumerBuild: typeof info.build === "string" ? info.build : undefined,
    consumerSdkVersion: typeof info.sdkVersion === "string" ? info.sdkVersion : undefined,
    consumerHermesBCVersion: typeof info.hermesBCVersion === "number" ? info.hermesBCVersion : undefined,
    consumerCurrentRuntimeFamilyId: typeof info.currentRuntimeFamilyId === "string" ? info.currentRuntimeFamilyId : undefined,
    consumerDefaultRuntimeFamilyId: typeof info.defaultRuntimeFamilyId === "string" ? info.defaultRuntimeFamilyId : undefined,
    consumerRuntimeFamilies: Array.isArray(info.runtimeFamilies) ? info.runtimeFamilies : undefined,
  };
}

function projectLabelFromStatus(status: DevServerStatus | null): string {
  const workDir = String(status?.workDir || "").trim();
  if (workDir) {
    const parts = workDir.split("/").filter(Boolean);
    const tail = parts[parts.length - 1];
    if (tail) return tail;
  }
  const framework = String(status?.framework || "").trim();
  return framework || "App";
}

export function DevPreview() {
  const c = useColors();
  const layout = useResponsiveLayout();
  const [status, setStatus] = useState<DevServerStatus | null>(null);
  const [showPreview, setShowPreview] = useState(false);
  const [loading, setLoading] = useState(false);
  const [webViewKey, setWebViewKey] = useState(0);
  const wasRunning = useRef(false);
  const webViewRef = useRef<WebView>(null);

  // Poll dev server status every 3s
  useEffect(() => {
    let mounted = true;
    const poll = async () => {
      const s = await quicClient.getDevServerStatus();
      if (!mounted) return;
      const isActive = isActiveDevServerStatus(s);
      setStatus(isActive ? s : null);

      // Auto-show banner when dev server first starts
      if (isActive && !wasRunning.current) {
        wasRunning.current = true;
      }
      if (!isActive) {
        wasRunning.current = false;
        if (showPreview) setShowPreview(false);
      }
    };
    poll();
    const interval = setInterval(poll, 3000);
    return () => { mounted = false; clearInterval(interval); };
  }, [showPreview]);

  // Subscribe to SSE events for auto-reload + log streaming
  useEffect(() => {
    if (!status?.running && !status?.building) return;

    const controller = new AbortController();
    const baseUrl = (quicClient as any).baseUrl;
    if (!baseUrl) return;

    const listenSSE = async () => {
      try {
        const res = await fetch(`${baseUrl}/dev/events`, {
          headers: (quicClient as any).authHeaders,
          signal: controller.signal,
        });
        const reader = res.body?.getReader();
        if (!reader) return;
        const decoder = new TextDecoder();
        let incomplete = "";

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          // Accumulate across chunks to handle SSE frames split across TCP packets
          const text = incomplete + decoder.decode(value, { stream: true });
          const lines = text.split("\n");
          // Last element may be an incomplete line — carry it over
          incomplete = lines.pop() || "";

          for (const line of lines) {
            if (line.startsWith("data: ")) {
              try {
                const event = JSON.parse(line.slice(6));
                // Yaver Protocol v1: structured progress + snapshots
                // for Hermes/Metro/Expo Web. The mobile DevPreview
                // banner shows a real percentage + currentFile while
                // a bundle compiles instead of a vague "Building…".
                if (event.type === "progress" && typeof event.topic === "string") {
                  const pct = typeof event.pct === "number" ? Math.round(event.pct) : 0;
                  const cf = typeof event.currentFile === "string" ? event.currentFile.split("/").slice(-2).join("/") : "";
                  const phaseStr = typeof event.phase === "string" ? event.phase.replace(/_/g, " ") : "compiling";
                  setProgressState({
                    topic: event.topic,
                    phase: phaseStr,
                    pct,
                    done: typeof event.done === "number" ? event.done : 0,
                    total: typeof event.total === "number" ? event.total : 0,
                    unit: typeof event.unit === "string" ? event.unit : "",
                    currentFile: cf,
                    src: event.progressSrc === "exact" ? "exact" : "unknown",
                    etaMs: typeof event.etaMs === "number" ? event.etaMs : 0,
                    updatedAt: Date.now(),
                  });
                  setLastByteAt(Date.now());
                  continue;
                }
                if (event.type === "phase" && typeof event.topic === "string") {
                  setProgressState((prev) => {
                    const same = prev && prev.topic === event.topic;
                    return {
                      topic: event.topic,
                      phase: typeof event.phase === "string" ? event.phase.replace(/_/g, " ") : "",
                      pct: same && prev ? prev.pct : 0,
                      done: same && prev ? prev.done : 0,
                      total: same && prev ? prev.total : 0,
                      unit: same && prev ? prev.unit : "",
                      currentFile: "",
                      src: same && prev ? prev.src : "unknown",
                      etaMs: 0,
                      updatedAt: Date.now(),
                    };
                  });
                  setLastByteAt(Date.now());
                  continue;
                }
                if (event.type === "snapshot") {
                  setLastByteAt(Date.now());
                  // Render fully from the snapshot's recent_log + progress
                  // so a reconnected client never feels behind.
                  if (event.snapshot?.progress) {
                    const p = event.snapshot.progress;
                    setProgressState({
                      topic: typeof p.topic === "string" ? p.topic : "dev/start",
                      phase: typeof p.phase === "string" ? p.phase.replace(/_/g, " ") : "",
                      pct: typeof p.pct === "number" ? Math.round(p.pct) : 0,
                      done: typeof p.done === "number" ? p.done : 0,
                      total: typeof p.total === "number" ? p.total : 0,
                      unit: typeof p.unit === "string" ? p.unit : "",
                      currentFile: typeof p.currentFile === "string" ? p.currentFile.split("/").slice(-2).join("/") : "",
                      src: p.progressSrc === "exact" ? "exact" : "unknown",
                      etaMs: typeof p.etaMs === "number" ? p.etaMs : 0,
                      updatedAt: Date.now(),
                    });
                  }
                  continue;
                }
                if (event.type === "heartbeat") {
                  setLastByteAt(Date.now());
                  continue;
                }
                if (event.type === "reload" || event.type === "ready") {
                  if (!mustUseNativePreview) {
                    setWebViewKey(k => k + 1);
                    setLoading(true);
                  }
                  setLastLogLine("");
                  setProgressState(null);
                  setLastByteAt(Date.now());
                } else if (event.type === "building") {
                  setLastLogLine(event.message || "Building...");
                  setLastByteAt(Date.now());
                } else if (event.type === "log" && event.logLine) {
                  setLastLogLine(event.logLine);
                  setLastByteAt(Date.now());
                } else if (event.type === "error") {
                  setLastLogLine(event.message || "Build failed");
                  setLastByteAt(Date.now());
                }
              } catch {}
            }
          }
        }
      } catch {
        // SSE disconnected — OK, we still have polling
      }
    };
    listenSSE();

    return () => controller.abort();
  }, [status?.running, status?.building, status?.framework, status?.devMode]);

  const [nativeLoading, setNativeLoading] = useState(false);
  const [reloadLoading, setReloadLoading] = useState(false);
  const [bundleMounted, setBundleMounted] = useState(false);
  const [lastLogLine, setLastLogLine] = useState<string>("");

  // Yaver Protocol v1: per-topic structured progress + transport
  // liveness. The DevPreview banner reads progressState to render
  // a real percentage + currentFile while a bundle compiles. The
  // user never sees a vague "Building…" again. lastByteAt drives
  // a "channel: live | syncing | reconnecting | lost" indicator
  // so even when compile is silent, the user knows transport is
  // alive — the agent guarantees a snapshot every 5s so >6s of
  // silence is the only "real" disconnect.
  const [progressState, setProgressState] = useState<{
    topic: string;
    phase: string;
    pct: number;
    done: number;
    total: number;
    unit: string;
    currentFile: string;
    src: "exact" | "unknown";
    etaMs: number;
    updatedAt: number;
  } | null>(null);
  const [lastByteAt, setLastByteAt] = useState<number>(Date.now());
  // Tick once per second so the relative-time labels refresh
  // without waiting for new bytes.
  const [, setNowTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setNowTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, []);
  const mustUseNativePreview =
    isHermesNativeFramework(status) ||
    status?.devMode === "dev-client" ||
    !!status?.building;

  // Listen for bundle unload events (user pressed "Back to Yaver")
  useEffect(() => {
    let mounted = true;
    void isBundleLoaded()
      .then((loaded) => {
        if (mounted) setBundleMounted(loaded);
      })
      .catch(() => {
        if (mounted) setBundleMounted(false);
      });
    const sub = onBundleEvent("onBundleUnloaded", () => {
      setNativeLoading(false);
      setBundleMounted(false);
    });
    const loadedSub = onBundleEvent("onBundleLoaded", () => setBundleMounted(true));
    return () => {
      mounted = false;
      sub.remove();
      loadedSub.remove();
    };
  }, []);

  // Load the app inside Yaver via the secondary RCTBridge (super-host mode).
  // This gives full native module access (camera, BLE, GPS, etc.) without
  // needing a separate dev client app installed on the phone.
  // Declared before handleOpen so the latter can close over it without
  // tripping the TS "used before declaration" rule.
  const handleRunInYaver = useCallback(async () => {
    const baseUrl = (quicClient as any).baseUrl;
    if (!baseUrl) {
      Alert.alert("Error", "Not connected to agent");
      return;
    }
    setNativeLoading(true);
    setLastLogLine("Building native bundle...");
    // The Go agent caps Metro bundle at 8 min and hermesc at 3 min, so
    // /dev/build-native worst-case duration is ~11 min. Give the client a
    // hair more so the agent's structured "timedOut" response surfaces
    // first; client abort is a backstop for a dead network or crashed
    // agent. Without this the fetch hangs forever — setNativeLoading stays
    // true, the UI is stuck on "Building..." and the user has to kill the
    // app to recover.
    const buildAbort = new AbortController();
    const buildAbortTimer = setTimeout(() => buildAbort.abort(), 12 * 60 * 1000);
    try {
      // Ask the Go agent to build a production Hermes bytecode bundle
      const platform = require("react-native").Platform.OS;
      const headers = {
        ...(quicClient as any).authHeaders,
        "Content-Type": "application/json",
      };
      const buildRes = await fetch(`${baseUrl}/dev/build-native`, {
        method: "POST",
        headers,
        body: JSON.stringify(
          buildNativeBuildRequest(
            platform,
            currentYaverConsumerContract(),
            status?.workDir ? { projectPath: status.workDir } : undefined,
          ),
        ),
        signal: buildAbort.signal,
      });
      clearTimeout(buildAbortTimer);
      const buildResult = await buildRes.json();

      if (buildResult.status !== "ok") {
        const error = new Error(nativeBuildFailureMessage(buildResult));
        (error as any).buildResult = buildResult;
        throw error;
      }
      const familySelection = buildResult.runtimeFamilySelection;
      const familyLabel = familySelection?.selected?.label || familySelection?.selected?.id || "";
      if (familyLabel) {
        setLastLogLine(
          familySelection?.exactMatch
            ? `Runtime family matched: ${familyLabel}`
            : `Runtime family closest host: ${familyLabel}`,
        );
      }

      // Download assets first (if any) so images/fonts are available when JS runs
      if (buildResult.hasAssets && buildResult.assetsUrl) {
        setLastLogLine("Downloading assets...");
        try {
          const assetsRes = await fetch(`${baseUrl}${buildResult.assetsUrl}`, { headers });
          if (assetsRes.ok) {
            const assetsBlob = await assetsRes.blob();
            // Push assets to the on-device HTTP server for extraction
            const devicePort = 8347;
            await fetch(`http://localhost:${devicePort}/assets`, {
              method: "POST",
              body: assetsBlob,
              headers: { "Content-Type": "application/zip" },
            });
          }
        } catch (assetErr) {
          // Non-fatal — images may be broken but app should still render
          console.warn("[DevPreview] asset download failed:", assetErr);
        }
      }

      // Load the compiled native bundle. loadAppIfChanged short-circuits
      // when md5 matches what's already running — saves the bridge reload.
      setLastLogLine("Loading bundle on device...");
      const bundleUrl = `${baseUrl}${buildResult.bundleUrl}`;
      const moduleName = buildResult.moduleName || "main";
      const loadResult = await loadAppIfChanged(
        bundleUrl,
        moduleName,
        buildResult.md5,
        (quicClient as any).authHeaders,
      );
      if (loadResult.skipped) {
        setLastLogLine("Already up to date");
      }
      setBundleMounted(true);
    } catch (err: any) {
      clearTimeout(buildAbortTimer);
      setNativeLoading(false);
      setLastLogLine("");
      const aborted = err?.name === "AbortError" || buildAbort.signal.aborted;
      const message = aborted
        ? "Build did not respond in 12 minutes. The agent may be stuck or unreachable — check the project's node_modules and retry."
        : err?.message || "Could not load bundle in Yaver";
      const title = aborted
        ? "Build Timed Out"
        : err?.buildResult ? nativeBuildFailureTitle(err.buildResult) : "Load Failed";
      Alert.alert(title, message);
    }
    // status.workDir MUST be in the deps. Agent 1.99.187+ requires
    // every /dev/build-native to pin the project via projectName,
    // projectPath, or bundleId — with `[]` deps the closure froze
    // `status` to its first-render value (undefined), so the tap
    // sent an unpinned request and the agent rejected with
    // PROJECT_REQUIRED ("build-native requires projectName,
    // projectPath, or bundleId..."). Including workDir means the
    // callback refreshes whenever the dev server's project changes.
  }, [status?.workDir]);

  const handleOpen = useCallback(() => {
    if (mustUseNativePreview) {
      // Expo / React Native on the phone must never degrade to WebView.
      handleRunInYaver();
      return;
    }
    // Web mode: open in WebView
    setShowPreview(true);
    setLoading(true);
    setWebViewKey(k => k + 1);
  }, [mustUseNativePreview, handleRunInYaver]);

  const handleReload = useCallback(async () => {
    if (reloadLoading || nativeLoading) return;
    if (mustUseNativePreview && !bundleMounted) {
      await handleRunInYaver();
      return;
    }
    setReloadLoading(true);
    if (!mustUseNativePreview) {
      setLoading(true);
    }
    try {
      const ok = await quicClient.reloadDevServer({ mode: mustUseNativePreview ? "bundle" : "dev" });
      if (!ok) {
        setLoading(false);
        Alert.alert("Reload Failed", "Could not reload — is the dev server still running?");
        return;
      }
      if (!mustUseNativePreview) {
        if (!showPreview || !status?.running) {
          setWebViewKey(k => k + 1);
        } else {
          setTimeout(() => setWebViewKey(k => k + 1), 500);
        }
      }
    } finally {
      setReloadLoading(false);
    }
  }, [bundleMounted, handleRunInYaver, mustUseNativePreview, nativeLoading, reloadLoading, showPreview, status?.running]);

  const handleStop = useCallback(async () => {
    Alert.alert("Stop Serving Preview", "This will stop serving the current preview and close it on this device.", [
      { text: "Cancel", style: "cancel" },
      {
        text: "Stop Serving", style: "destructive", onPress: async () => {
          await quicClient.stopDevServer();
          setShowPreview(false);
          setStatus(null);
        }
      },
    ]);
  }, []);

  if (!status) return null;

  const bundleUrl = quicClient.getDevServerBundleUrl(status.bundleUrl || "/dev/");
  const projectLabel = projectLabelFromStatus(status);
  const frameworkLabel = status.framework || "app";
  const portLabel = status.port ? `port ${status.port}` : null;
  const hotReloadLabel = status.hotReload === false ? "hot reload off" : "hot reload on";
  const metaLine = [frameworkLabel, portLabel, hotReloadLabel].filter(Boolean).join(" · ");
  const modeLine = status.building
    ? "build in progress"
    : (status.iosInstallMethod === "native"
        ? "native install"
        : frameworkLabel.toLowerCase() === "flutter"
          ? "LAN app reload"
          : "Hermes bundle in Yaver");
  const targetLabel = status.targetDeviceName || "this device";
  const isBusy = !!status.building || nativeLoading;
  const openLabel = status.building
    ? "Compiling…"
    : nativeLoading
      ? "Building…"
      : "Open in Yaver";

  return (
    <>
      <View style={[styles.card, styles.activeCard]}>
        {/* Mirror the Projects-tab card layout so the Tasks tab's
            dev-server section reads as the same component. The previous
            banner squeezed the project name + meta into a small block
            beside a heavy two-line green button; this layout puts the
            project name at the visual centre and lets the action row
            (Open in Yaver / Reload / Stop) sit underneath with the
            same three-button rhythm Apps uses. */}
        <View style={styles.cardHeader}>
          {status.building ? (
            <ActivityIndicator size="small" color="#eab308" />
          ) : (
            <View style={[styles.statusDot, { backgroundColor: "#22c55e" }]} />
          )}
          <View style={styles.cardTitleContainer}>
            <Text style={styles.cardTitle} numberOfLines={1}>{projectLabel}</Text>
            <Text style={styles.cardMeta} numberOfLines={1}>{metaLine}</Text>
            <Text style={[styles.cardMeta, { color: "#86efac" }]} numberOfLines={1}>
              mode · {modeLine}
            </Text>
            <Text style={[styles.cardMeta, { color: "#7dd3fc" }]} numberOfLines={1}>
              target · {targetLabel}
            </Text>
            {lastLogLine ? (
              <Text style={[styles.cardMeta, { color: "#94a3b8" }]} numberOfLines={1}>
                {lastLogLine}
              </Text>
            ) : null}
          </View>
        </View>
        <View style={styles.cardActions}>
          <Pressable
            style={({ pressed }) => [
              styles.actionBtn,
              styles.openBtn,
              (isBusy || pressed) && { opacity: pressed ? 0.85 : 0.55 },
            ]}
            onPress={handleOpen}
            disabled={isBusy}
          >
            {isBusy ? (
              <>
                <ActivityIndicator size="small" color="#000" />
                <Text style={[styles.openBtnText, { marginLeft: 6 }]}>{openLabel}</Text>
              </>
            ) : (
              <Text style={styles.openBtnText}>{openLabel}</Text>
            )}
          </Pressable>
          <Pressable
            style={({ pressed }) => [
              styles.actionBtn,
              styles.reloadBtn,
              (isBusy || reloadLoading || pressed) && { opacity: pressed ? 0.85 : 0.55 },
            ]}
            onPress={handleReload}
            disabled={isBusy || reloadLoading}
          >
            {reloadLoading ? (
              <ActivityIndicator size="small" color="#22c55e" />
            ) : (
              <Text style={styles.reloadBtnText}>{"↻"} Reload</Text>
            )}
          </Pressable>
          <Pressable
            style={({ pressed }) => [
              styles.actionBtn,
              styles.stopBtn,
              pressed && { opacity: 0.85 },
            ]}
            onPress={handleStop}
          >
            <Text style={styles.stopBtnText}>Stop</Text>
          </Pressable>
        </View>
      </View>
      {/* Full-screen WebView Modal */}
      <Modal visible={showPreview} animationType="slide" onRequestClose={() => setShowPreview(false)}>
        <View style={[styles.container, { backgroundColor: c.bg }]}>
          {/* Header */}
          <View style={[styles.header, { backgroundColor: "#111", borderBottomColor: "#333" }]}>
            <Pressable onPress={() => setShowPreview(false)} style={styles.headerBtn}>
              <Text style={styles.headerBtnClose}>Back</Text>
            </Pressable>
            <View style={styles.headerCenter}>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                <View style={[styles.dotSmall, { backgroundColor: "#22c55e" }]} />
                <Text style={styles.headerTitle}>
                  {status.workDir?.split("/").pop() || status.framework}
                </Text>
              </View>
            </View>
            <View style={styles.headerRight}>
              <Pressable onPress={handleReload} style={styles.headerBtn}>
                <Text style={styles.headerBtnReload}>Reload</Text>
              </Pressable>
              <Pressable onPress={handleStop} style={styles.headerBtn}>
                <Text style={styles.headerBtnStop}>{status.stopActionLabel || "Stop Serving"}</Text>
              </Pressable>
            </View>
          </View>

          {mustUseNativePreview ? (
            /* Native dev-client mode or building: show controls / build logs */
            <View style={styles.nativeControls}>
              {status.building ? (
                /* ── Building state: show compilation progress ── */
                <>
                  <View style={styles.nativeStatus}>
                    <ActivityIndicator size="small" color="#eab308" />
                    <Text style={[styles.nativeTitle, { color: "#eab308" }]}>Building Native App</Text>
                  </View>
                  <Text style={styles.nativeSubtext}>
                    {status.workDir?.split("/").pop() || "app"} — compiling for device
                  </Text>
                  <Text style={{ fontSize: 12, color: "#666", textAlign: "center", marginTop: 4 }}>
                    This takes 3-5 min for the first build, ~30s for incremental
                  </Text>

                  {/* Build log output */}
                  {lastLogLine ? (
                    <View style={{
                      marginTop: 16,
                      padding: 12,
                      borderRadius: 10,
                      backgroundColor: "#111",
                      borderWidth: 1,
                      borderColor: "#333",
                      width: "100%",
                    }}>
                      <Text style={{
                        fontFamily: "monospace",
                        fontSize: 11,
                        color: "#eab308",
                        lineHeight: 16,
                      }} numberOfLines={3}>
                        {lastLogLine}
                      </Text>
                    </View>
                  ) : null}

                  <View style={styles.nativeButtons}>
                    <Pressable onPress={handleStop} style={[styles.nativeBtn, { backgroundColor: "#2e1a1a" }]}>
                      <Text style={[styles.nativeBtnText, { color: "#ef4444" }]}>Cancel Build</Text>
                    </Pressable>
                  </View>
                </>
              ) : (
                /* ── Running state: Metro is up, show controls ── */
                <>
                  <View style={styles.nativeStatus}>
                    <View style={[styles.dot, { backgroundColor: "#22c55e", width: 14, height: 14, borderRadius: 7 }]} />
                    <Text style={styles.nativeTitle}>Dev Server Ready</Text>
                  </View>
                  <Text style={styles.nativeSubtext}>
                    {status.workDir?.split("/").pop() || "app"} — {status.framework} — port {status.port}
                  </Text>

                  {/* Metro URL — tap to copy */}
                  {status.deepLink && (
                    <Pressable
                      onPress={() => {
                        const url = status.deepLink!;
                        import("expo-clipboard").then(({ setStringAsync }) => {
                          setStringAsync(url);
                          Alert.alert("Copied", url);
                        }).catch(() => {});
                      }}
                      style={{ marginTop: 12, paddingVertical: 10, paddingHorizontal: 20, borderRadius: 10, backgroundColor: "#111", borderWidth: 1, borderColor: "#333" }}
                    >
                      <Text style={{ fontFamily: "monospace", fontSize: 14, color: "#22c55e", textAlign: "center" }}>
                        {status.deepLink}
                      </Text>
                      <Text style={{ fontSize: 11, color: "#666", textAlign: "center", marginTop: 4 }}>
                        Tap to copy — paste in dev client if Bonjour fails
                      </Text>
                    </Pressable>
                  )}

                  {/* Run in Yaver (super-host: load bundle inside Yaver's RCTBridge) */}
                  <Pressable
                    onPress={handleRunInYaver}
                    disabled={nativeLoading}
                    style={[styles.nativeBtn, { backgroundColor: "#1a2e1a", paddingHorizontal: 40, marginTop: 12 }]}
                  >
                    {nativeLoading ? (
                      <ActivityIndicator size="small" color="#22c55e" />
                    ) : (
                      <Text style={[styles.nativeBtnText, { color: "#22c55e" }]}>Open in Yaver</Text>
                    )}
                  </Pressable>
                  <Text style={{ fontSize: 11, color: "#555", textAlign: "center", marginTop: 4 }}>
                    Hermes bundle on this iPhone. Ideal for Linux, WSL, and remote-host workflows.
                  </Text>

                  {/* Open in separate dev client (if installed) */}
                  {status.deepLink && (
                    <Pressable
                      onPress={() => {
                        Linking.openURL(status.deepLink!).catch(() =>
                          Alert.alert("Open App", "Open the app from your home screen.")
                        );
                      }}
                      style={[styles.nativeBtn, { backgroundColor: "#1a1a2e", paddingHorizontal: 40, marginTop: 8 }]}
                    >
                      <Text style={[styles.nativeBtnText, { color: "#818cf8" }]}>Open Dev Client</Text>
                    </Pressable>
                  )}

                  <View style={styles.nativeButtons}>
                    <Pressable
                      onPress={handleReload}
                      disabled={reloadLoading || nativeLoading}
                      style={[styles.nativeBtn, { backgroundColor: "#1a2e1a", opacity: reloadLoading || nativeLoading ? 0.75 : 1 }]}
                    >
                      <Text style={[styles.nativeBtnText, { color: "#22c55e" }]}>
                        {reloadLoading ? "Reloading…" : bundleMounted ? "Reload" : "Open first"}
                      </Text>
                    </Pressable>
                    <Pressable onPress={handleStop} style={[styles.nativeBtn, { backgroundColor: "#2e1a1a" }]}>
                      <Text style={[styles.nativeBtnText, { color: "#ef4444" }]}>{status.stopActionLabel || "Stop Serving"}</Text>
                    </Pressable>
                  </View>
                </>
              )}
            </View>
          ) : (
            /* Web mode: load app in WebView */
            <>
              <WebView
                ref={webViewRef}
                key={webViewKey}
                source={{ uri: bundleUrl }}
                style={styles.webview}
                onLoadStart={() => setLoading(true)}
                onLoadEnd={() => setLoading(false)}
                onError={(e) => {
                  setLoading(false);
                  Alert.alert("Load Error", e.nativeEvent.description || "Could not load the app");
                }}
                javaScriptEnabled
                domStorageEnabled
                allowsInlineMediaPlayback
                originWhitelist={["*"]}
                startInLoadingState
                renderLoading={() => (
                  <View style={styles.loadingContainer}>
                    <ActivityIndicator size="large" color="#818cf8" />
                    <Text style={styles.loadingText}>
                      Loading {status.workDir?.split("/").pop() || "app"}...
                    </Text>
                    <Text style={styles.loadingSubtext}>
                      Through {(quicClient as any)._connectionMode === "relay" ? "relay" : "direct"} connection
                    </Text>
                  </View>
                )}
              />
              {loading && (
                <View style={styles.loadingBar}>
                  <View style={styles.loadingBarFill} />
                </View>
              )}
            </>
          )}
        </View>
      </Modal>
    </>
  );
}

/** Hook to check dev server status from other components. */
export function useDevServerStatus() {
  const [status, setStatus] = useState<DevServerStatus | null>(null);
  useEffect(() => {
    let mounted = true;
    const poll = async () => {
      const s = await quicClient.getDevServerStatus();
      if (mounted) setStatus(isActiveDevServerStatus(s) ? s : null);
    };
    poll();
    const interval = setInterval(poll, 5000);
    return () => { mounted = false; clearInterval(interval); };
  }, []);
  return status;
}

const styles = StyleSheet.create({
  // Replicates the Projects-tab "running app" card (apps.tsx ::
  // card + activeCard) so the Tasks-tab dev banner reads as the same
  // component on both screens. Dark-green background, subtle green
  // border, generous internal padding, no shadow (RN-iOS handles depth
  // via the bordered card itself).
  card: {
    marginHorizontal: 16,
    marginBottom: 8,
    borderRadius: 16,
    paddingHorizontal: 16,
    paddingVertical: 14,
    borderWidth: 0.5,
  },
  activeCard: {
    backgroundColor: "#0f1a0f",
    borderWidth: 1,
    borderColor: "#22c55e44",
    marginTop: 12,
  },
  cardHeader: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
  cardTitleContainer: { flex: 1 },
  cardTitle: { fontSize: 16, fontWeight: "700", color: "#fff" },
  cardMeta: { fontSize: 11, color: "#666", marginTop: 2 },
  statusDot: { width: 8, height: 8, borderRadius: 4 },
  cardActions: { flexDirection: "row", gap: 8, marginTop: 12 },
  actionBtn: { paddingVertical: 8, paddingHorizontal: 14, borderRadius: 8 },
  openBtn: {
    backgroundColor: "#22c55e",
    flex: 1,
    alignItems: "center",
    flexDirection: "row",
    justifyContent: "center",
    gap: 4,
  },
  openBtnText: { color: "#000", fontSize: 13, fontWeight: "700" },
  reloadBtn: {
    backgroundColor: "#22c55e22",
    flex: 1,
    alignItems: "center",
  },
  reloadBtnText: { color: "#22c55e", fontSize: 13, fontWeight: "600" },
  stopBtn: { backgroundColor: "#ef444422", paddingHorizontal: 16, alignItems: "center" },
  stopBtnText: { color: "#ef4444", fontSize: 13, fontWeight: "600" },

  banner: {
    padding: 14,
    marginHorizontal: 16,
    marginBottom: 8,
    borderRadius: 14,
    borderWidth: 1,
    gap: 12,
  },
  // Header row owns the project name + state pill on top. Replaces the
  // old horizontal bannerLeft/bannerRight split where the project name
  // was buried in a small block next to the action buttons.
  bannerHeaderRow: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: 10,
  },
  // Single-row pair: prominent green "Open in Yaver" + flat "Stop"
  // pill. Mirrors the Projects-tab action row styling.
  bannerActionRow: {
    flexDirection: "row",
    alignItems: "stretch",
    gap: 10,
  },
  bannerPrimaryBtnSimple: {
    flex: 1,
    minHeight: 48,
    borderRadius: 12,
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: 16,
  },
  bannerPrimaryTextSimple: {
    fontSize: 15,
    fontWeight: "800",
    letterSpacing: 0.2,
  },
  bannerStopBtnSimple: {
    minWidth: 88,
    minHeight: 48,
    paddingHorizontal: 16,
    borderRadius: 12,
    borderWidth: 1,
    alignItems: "center",
    justifyContent: "center",
  },
  bannerMain: {
    gap: 12,
  },
  bannerMainWide: {
    flexDirection: "row",
    alignItems: "center",
  },
  bannerLeft: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: 10,
    flex: 1,
  },
  bannerTextWrap: {
    flex: 1,
    minWidth: 0,
  },
  bannerTitleRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    flexWrap: "wrap",
  },
  dot: { width: 10, height: 10, borderRadius: 5 },
  dotSmall: { width: 7, height: 7, borderRadius: 4 },
  bannerTitle: {
    fontSize: 16,
    fontWeight: "700",
    color: "#e4e4e7",
    flexShrink: 1,
  },
  // Larger, more prominent project name for the redesigned banner.
  // Replaces the cramped layout where the green button text was the
  // visual anchor — now the app name is.
  bannerTitleLarge: {
    fontSize: 18,
    fontWeight: "800",
    color: "#e4e4e7",
    flexShrink: 1,
    letterSpacing: -0.2,
  },
  bannerStatePill: {
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderRadius: 999,
    borderWidth: 1,
  },
  bannerStateText: {
    fontSize: 10,
    fontWeight: "800",
    letterSpacing: 0.5,
  },
  bannerSubtitle: {
    fontSize: 12,
    color: "#888",
    marginTop: 3,
  },
  bannerPath: {
    fontSize: 10,
    marginTop: 4,
    fontFamily: monoFamily,
  },
  bannerLogLine: {
    fontSize: 10,
    marginTop: 4,
    fontFamily: monoFamily,
  },
  bannerRight: {
    flexDirection: "row",
    alignItems: "stretch",
    gap: 10,
  },
  bannerRightInline: {
    width: 220,
    flexDirection: "column",
    // Center the button stack vertically within the row so it
    // doesn't visually crash into the card border when the
    // bannerLeft text column is taller than the buttons' natural
    // size. Without this + with flex:1 on the buttons, each button
    // stretched to half the row height — on a tablet where path +
    // meta + log line + title made the left column 90+pt, the
    // green/red buttons grew to ~45pt each AND sat flush against
    // the card's borderWidth:1 frame, reading as "overlapping the
    // boundary lines".
    justifyContent: "center",
  },
  bannerRightStacked: {
    flexDirection: "row",
  },
  bannerPrimaryBtn: {
    // No flex here — bannerRightInline (tablet column) keeps the
    // button at its natural minHeight so it doesn't stretch into
    // the card padding. bannerRightStacked (phone row) re-applies
    // flex:1 inline at render time via `bannerActionRowGrow` so the
    // phone side-by-side layout still splits width evenly.
    minHeight: 44,
    borderRadius: 12,
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: 16,
    alignSelf: "stretch",
  },
  bannerPrimaryContent: {
    width: "100%",
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
  bannerPrimaryIcon: {
    width: 28,
    height: 28,
    borderRadius: 999,
    backgroundColor: "rgba(255,255,255,0.18)",
    alignItems: "center",
    justifyContent: "center",
  },
  bannerPrimaryTextWrap: {
    flex: 1,
    minWidth: 0,
  },
  bannerPrimaryText: {
    fontSize: 14,
    fontWeight: "800",
  },
  bannerPrimarySubtext: {
    fontSize: 11,
    fontWeight: "600",
    opacity: 0.82,
    marginTop: 2,
  },
  bannerStopBtn: {
    minHeight: 44,
    paddingHorizontal: 16,
    paddingVertical: 10,
    borderRadius: 12,
    borderWidth: 1,
    alignItems: "center",
    justifyContent: "center",
    alignSelf: "stretch",
  },
  // Phone row layout splits the two buttons evenly via flex:1;
  // injected only when bannerRightStacked is in use.
  bannerActionRowGrow: { flex: 1 },
  bannerStopText: {
    fontSize: 12,
    fontWeight: "700",
  },
  container: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 12,
    paddingBottom: 10,
    paddingTop: 54,
    borderBottomWidth: 1,
  },
  headerBtn: { padding: 6 },
  headerBtnClose: { fontSize: 15, fontWeight: "600", color: "#818cf8" },
  headerBtnReload: { fontSize: 13, fontWeight: "600", color: "#22c55e" },
  headerBtnStop: { fontSize: 13, fontWeight: "600", color: "#ef4444" },
  headerCenter: { alignItems: "center", flex: 1 },
  headerTitle: { fontSize: 15, fontWeight: "700", color: "#fff" },
  headerRight: { flexDirection: "row", gap: 12 },
  webview: { flex: 1 },
  loadingContainer: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    gap: 10,
    backgroundColor: "#050508",
  },
  loadingText: { fontSize: 14, color: "#e4e4e7", fontWeight: "600" },
  loadingSubtext: { fontSize: 12, color: "#666" },
  loadingBar: {
    position: "absolute",
    top: 94,
    left: 0,
    right: 0,
    height: 2,
    backgroundColor: "#333",
  },
  loadingBarFill: {
    height: "100%",
    width: "60%",
    backgroundColor: "#22c55e",
  },
  nativeControls: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: 32,
    gap: 20,
    backgroundColor: "#050508",
  },
  nativeStatus: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
  nativeTitle: {
    fontSize: 20,
    fontWeight: "700",
    color: "#e4e4e7",
  },
  nativeSubtext: {
    fontSize: 14,
    color: "#888",
    textAlign: "center",
    lineHeight: 20,
  },
  nativeButtons: {
    flexDirection: "row",
    gap: 16,
    marginTop: 20,
  },
  nativeBtn: {
    paddingHorizontal: 28,
    paddingVertical: 14,
    borderRadius: 12,
  },
  nativeBtnText: {
    fontSize: 16,
    fontWeight: "700",
  },
});
