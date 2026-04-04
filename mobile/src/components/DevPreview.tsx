import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Linking,
  Modal,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { WebView } from "react-native-webview";
import { quicClient, type DevServerStatus } from "../lib/quic";
import { useColors } from "../context/ThemeContext";
import { loadApp, onBundleEvent, buildNativeBundleUrl } from "../lib/bundleLoader";

/**
 * Dev Preview — banner + full-screen WebView for previewing apps
 * running through the agent's /dev/* reverse proxy.
 *
 * Flow:
 * 1. Agent starts a dev server (via POST /dev/start or Claude Code task)
 * 2. DevPreview polls /dev/status and detects it
 * 3. Shows a green banner: "expo dev server · Open App"
 * 4. User taps → full-screen WebView loads the app through relay
 * 5. Agent can trigger reload → WebView refreshes
 */
export function DevPreview() {
  const c = useColors();
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
      const isRunning = s?.running === true;
      setStatus(isRunning ? s : null);

      // Auto-show banner when dev server first starts
      if (isRunning && !wasRunning.current) {
        wasRunning.current = true;
      }
      if (!isRunning) {
        wasRunning.current = false;
        if (showPreview) setShowPreview(false);
      }
    };
    poll();
    const interval = setInterval(poll, 3000);
    return () => { mounted = false; clearInterval(interval); };
  }, [showPreview]);

  // Subscribe to SSE events for auto-reload
  useEffect(() => {
    if (!showPreview || !status?.running) return;

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
                if (event.type === "reload" || event.type === "ready") {
                  // Force WebView reload
                  setWebViewKey(k => k + 1);
                  setLoading(true);
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
  }, [showPreview, status?.running]);

  const isNativeMode = status?.devMode === "dev-client";
  const [nativeLoading, setNativeLoading] = useState(false);

  // Listen for bundle unload events (user pressed "Back to Yaver")
  useEffect(() => {
    const sub = onBundleEvent("onBundleUnloaded", () => {
      setNativeLoading(false);
    });
    return () => sub.remove();
  }, []);

  const handleOpen = useCallback(() => {
    if (isNativeMode) {
      // Dev-client mode: show controls panel (the native app runs separately)
      // Don't open WebView — Metro in dev-client mode redirects to exp:// which WebView can't handle
      setShowPreview(true);
      return;
    }
    // Web mode: open in WebView
    setShowPreview(true);
    setLoading(true);
    setWebViewKey(k => k + 1);
  }, [isNativeMode]);

  // Load the app inside Yaver via the secondary RCTBridge (super-host mode).
  // This gives full native module access (camera, BLE, GPS, etc.) without
  // needing a separate dev client app installed on the phone.
  const handleRunInYaver = useCallback(async () => {
    const baseUrl = (quicClient as any).baseUrl;
    if (!baseUrl) {
      Alert.alert("Error", "Not connected to agent");
      return;
    }
    setNativeLoading(true);
    try {
      const bundleUrl = buildNativeBundleUrl(baseUrl);
      await loadApp(bundleUrl, "main");
    } catch (err: any) {
      setNativeLoading(false);
      Alert.alert("Load Failed", err?.message || "Could not load bundle in Yaver");
    }
  }, []);

  const handleReload = useCallback(async () => {
    setLoading(true);
    const ok = await quicClient.reloadDevServer();
    if (!ok) {
      setLoading(false);
      Alert.alert("Reload Failed", "Could not reload — is the dev server still running?");
      return;
    }
    // SSE "reload" event will trigger WebView key increment.
    // Fallback: if SSE is not connected (e.g., relay mode), reload directly.
    if (!showPreview || !status?.running) {
      setWebViewKey(k => k + 1);
    } else {
      // Give SSE 500ms to deliver the reload event, then force reload as fallback
      setTimeout(() => setWebViewKey(k => k + 1), 500);
    }
  }, [showPreview, status?.running]);

  const handleStop = useCallback(async () => {
    Alert.alert("Stop Dev Server", "This will stop the dev server and close the preview.", [
      { text: "Cancel", style: "cancel" },
      {
        text: "Stop", style: "destructive", onPress: async () => {
          await quicClient.stopDevServer();
          setShowPreview(false);
          setStatus(null);
        }
      },
    ]);
  }, []);

  if (!status) return null;

  const bundleUrl = quicClient.getDevServerBundleUrl(status.bundleUrl || "/dev/");

  return (
    <>
      {/* Banner */}
      <Pressable
        style={[styles.banner, { backgroundColor: "#0f1a0f", borderColor: "#22c55e" }]}
        onPress={handleOpen}
      >
        <View style={styles.bannerLeft}>
          <View style={[styles.dot, { backgroundColor: "#22c55e" }]} />
          <View>
            <Text style={styles.bannerTitle}>
              {status.framework} dev server
            </Text>
            {status.workDir && (
              <Text style={styles.bannerSubtitle} numberOfLines={1}>
                {status.workDir.split("/").pop()}
              </Text>
            )}
          </View>
        </View>
        <View style={styles.bannerRight}>
          {nativeLoading ? (
            <ActivityIndicator size="small" color="#22c55e" />
          ) : (
            <>
              <Text style={styles.bannerAction}>{isNativeMode ? "Run Native" : "Open App"}</Text>
              <Text style={styles.bannerArrow}>{"\u203A"}</Text>
            </>
          )}
        </View>
      </Pressable>

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
                <Text style={styles.headerBtnStop}>Stop</Text>
              </Pressable>
            </View>
          </View>

          {isNativeMode ? (
            /* Native dev-client mode: Yaver is control plane, native app runs separately */
            <View style={styles.nativeControls}>
              <View style={styles.nativeStatus}>
                <View style={[styles.dot, { backgroundColor: "#22c55e", width: 14, height: 14, borderRadius: 7 }]} />
                <Text style={styles.nativeTitle}>Metro Running</Text>
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
                  <Text style={[styles.nativeBtnText, { color: "#22c55e" }]}>Run in Yaver</Text>
                )}
              </Pressable>
              <Text style={{ fontSize: 11, color: "#555", textAlign: "center", marginTop: 4 }}>
                Full native access — camera, BLE, GPS, etc.
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
                <Pressable onPress={handleReload} style={[styles.nativeBtn, { backgroundColor: "#1a2e1a" }]}>
                  <Text style={[styles.nativeBtnText, { color: "#22c55e" }]}>Reload</Text>
                </Pressable>
                <Pressable onPress={handleStop} style={[styles.nativeBtn, { backgroundColor: "#2e1a1a" }]}>
                  <Text style={[styles.nativeBtnText, { color: "#ef4444" }]}>Stop Server</Text>
                </Pressable>
              </View>
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
      if (mounted) setStatus(s?.running ? s : null);
    };
    poll();
    const interval = setInterval(poll, 5000);
    return () => { mounted = false; clearInterval(interval); };
  }, []);
  return status;
}

const styles = StyleSheet.create({
  banner: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    padding: 14,
    marginHorizontal: 16,
    marginBottom: 8,
    borderRadius: 14,
    borderWidth: 1,
  },
  bannerLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
  dot: { width: 10, height: 10, borderRadius: 5 },
  dotSmall: { width: 7, height: 7, borderRadius: 4 },
  bannerTitle: {
    fontSize: 15,
    fontWeight: "700",
    color: "#e4e4e7",
  },
  bannerSubtitle: {
    fontSize: 11,
    color: "#888",
    marginTop: 1,
  },
  bannerRight: {
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
  },
  bannerAction: {
    fontSize: 14,
    fontWeight: "700",
    color: "#22c55e",
  },
  bannerArrow: {
    fontSize: 20,
    color: "#22c55e",
    marginTop: -2,
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
