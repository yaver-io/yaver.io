import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Modal,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { WebView } from "react-native-webview";
import { quicClient, type DevServerStatus } from "../lib/quic";
import { useColors } from "../context/ThemeContext";

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

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          const text = decoder.decode(value);
          // SSE format: "data: {...}\n\n"
          const lines = text.split("\n");
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

  const handleOpen = useCallback(() => {
    setShowPreview(true);
    setLoading(true);
    setWebViewKey(k => k + 1);
  }, []);

  const handleReload = useCallback(async () => {
    setLoading(true);
    await quicClient.reloadDevServer();
    setWebViewKey(k => k + 1);
  }, []);

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
          <Text style={styles.bannerAction}>Open App</Text>
          <Text style={styles.bannerArrow}>{"\u203A"}</Text>
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

          {/* WebView */}
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
});
