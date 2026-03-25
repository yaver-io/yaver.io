import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
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
 * Dev Preview component — shows a banner when a dev server is running
 * on the connected agent. Tapping it opens a WebView that loads the
 * app through the agent's /dev/* reverse proxy (works through relay).
 *
 * Usage: <DevPreview /> — place anywhere in the connected device screen.
 */
export function DevPreview() {
  const c = useColors();
  const [status, setStatus] = useState<DevServerStatus | null>(null);
  const [showPreview, setShowPreview] = useState(false);
  const [loading, setLoading] = useState(false);

  // Poll dev server status every 5s
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

  const handleOpen = useCallback(() => {
    setShowPreview(true);
    setLoading(true);
  }, []);

  const handleReload = useCallback(async () => {
    setLoading(true);
    await quicClient.reloadDevServer();
    // WebView will reload via key change
    setShowPreview(false);
    setTimeout(() => setShowPreview(true), 200);
  }, []);

  const handleStop = useCallback(async () => {
    await quicClient.stopDevServer();
    setShowPreview(false);
    setStatus(null);
  }, []);

  if (!status) return null;

  const bundleUrl = quicClient.getDevServerBundleUrl(status.bundleUrl || "/dev/");

  return (
    <>
      {/* Banner */}
      <Pressable
        style={[styles.banner, { backgroundColor: "#1a1a2e", borderColor: "#6366f1" }]}
        onPress={handleOpen}
      >
        <View style={styles.bannerLeft}>
          <View style={[styles.dot, { backgroundColor: "#22c55e" }]} />
          <Text style={styles.bannerTitle}>
            {status.framework} dev server
          </Text>
        </View>
        <Text style={styles.bannerAction}>Preview</Text>
      </Pressable>

      {/* WebView Modal */}
      <Modal visible={showPreview} animationType="slide" onRequestClose={() => setShowPreview(false)}>
        <View style={[styles.container, { backgroundColor: c.bg }]}>
          {/* Header */}
          <View style={[styles.header, { backgroundColor: c.bgCard, borderBottomColor: c.border }]}>
            <Pressable onPress={() => setShowPreview(false)} style={styles.headerBtn}>
              <Text style={[styles.headerBtnText, { color: c.accent }]}>Close</Text>
            </Pressable>
            <View style={styles.headerCenter}>
              <Text style={[styles.headerTitle, { color: c.textPrimary }]}>
                {status.framework} Preview
              </Text>
              {status.workDir && (
                <Text style={[styles.headerSubtitle, { color: c.textMuted }]} numberOfLines={1}>
                  {status.workDir.split("/").slice(-2).join("/")}
                </Text>
              )}
            </View>
            <View style={styles.headerRight}>
              <Pressable onPress={handleReload} style={styles.headerBtn}>
                <Text style={[styles.headerBtnText, { color: c.accent }]}>Reload</Text>
              </Pressable>
              <Pressable onPress={handleStop} style={styles.headerBtn}>
                <Text style={[styles.headerBtnText, { color: "#ef4444" }]}>Stop</Text>
              </Pressable>
            </View>
          </View>

          {/* WebView */}
          <WebView
            source={{ uri: bundleUrl }}
            style={styles.webview}
            onLoadStart={() => setLoading(true)}
            onLoadEnd={() => setLoading(false)}
            javaScriptEnabled
            domStorageEnabled
            allowsInlineMediaPlayback
            originWhitelist={["*"]}
            startInLoadingState
            renderLoading={() => (
              <View style={styles.loadingContainer}>
                <ActivityIndicator size="large" color={c.accent} />
                <Text style={[styles.loadingText, { color: c.textMuted }]}>
                  Loading {status.framework} bundle...
                </Text>
              </View>
            )}
          />

          {loading && (
            <View style={styles.loadingOverlay}>
              <ActivityIndicator size="small" color={c.accent} />
            </View>
          )}
        </View>
      </Modal>
    </>
  );
}

/**
 * DevPreviewBanner — minimal version that just shows the banner.
 * The parent handles opening the preview.
 */
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
    padding: 12,
    marginHorizontal: 16,
    marginBottom: 8,
    borderRadius: 12,
    borderWidth: 1,
  },
  bannerLeft: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  dot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  bannerTitle: {
    fontSize: 14,
    fontWeight: "600",
    color: "#e4e4e7",
  },
  bannerAction: {
    fontSize: 13,
    fontWeight: "600",
    color: "#818cf8",
  },
  container: {
    flex: 1,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 12,
    paddingVertical: 10,
    paddingTop: 54, // safe area
    borderBottomWidth: 1,
  },
  headerBtn: {
    padding: 6,
  },
  headerBtnText: {
    fontSize: 14,
    fontWeight: "600",
  },
  headerCenter: {
    alignItems: "center",
    flex: 1,
  },
  headerTitle: {
    fontSize: 15,
    fontWeight: "700",
  },
  headerSubtitle: {
    fontSize: 11,
    marginTop: 2,
  },
  headerRight: {
    flexDirection: "row",
    gap: 8,
  },
  webview: {
    flex: 1,
  },
  loadingContainer: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    gap: 12,
  },
  loadingText: {
    fontSize: 13,
  },
  loadingOverlay: {
    position: "absolute",
    top: 100,
    right: 16,
  },
});
