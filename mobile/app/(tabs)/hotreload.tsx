import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Linking,
  Modal,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { WebView } from "react-native-webview";
import { SafeAreaView, useSafeAreaInsets } from "react-native-safe-area-context";
import { useDevice } from "../../src/context/DeviceContext";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient, type DevServerStatus } from "../../src/lib/quic";

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

// ── Hot Reload Tab ────────────────────────────────────────────────

export default function HotReloadScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const { activeDevice, connectionStatus } = useDevice();
  const isConnected = connectionStatus === "connected" && !!activeDevice;

  const [devStatus, setDevStatus] = useState<DevServerStatus | null>(null);
  const [projects, setProjects] = useState<ProjectItem[]>([]);
  const [startingProject, setStartingProject] = useState<string | null>(null);
  const [showWebView, setShowWebView] = useState(false);
  const [webViewKey, setWebViewKey] = useState(0);
  const [webViewLoading, setWebViewLoading] = useState(false);
  const webViewRef = useRef<WebView>(null);

  // Poll dev server status + mobile projects
  useEffect(() => {
    if (!isConnected) return;
    let mounted = true;

    const poll = async () => {
      try {
        const status = await quicClient.getDevServerStatus();
        if (mounted) setDevStatus(status?.running || status?.framework ? status : null);
      } catch {
        if (mounted) setDevStatus(null);
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
    // Refresh mobile project list every 30s (cache is 10min on server)
    const projectInterval = setInterval(fetchMobileProjects, 30000);
    return () => { mounted = false; clearInterval(interval); clearInterval(projectInterval); };
  }, [isConnected]);

  // SSE auto-reload
  useEffect(() => {
    if (!showWebView || !devStatus?.running) return;
    const controller = new AbortController();
    const baseUrl = (quicClient as any).baseUrl;
    if (!baseUrl) return;

    const listen = async () => {
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
          for (const line of text.split("\n")) {
            if (line.startsWith("data: ")) {
              try {
                const event = JSON.parse(line.slice(6));
                if (event.type === "reload" || event.type === "ready") {
                  setWebViewKey(k => k + 1);
                  setWebViewLoading(true);
                }
              } catch {}
            }
          }
        }
      } catch {}
    };
    listen();
    return () => controller.abort();
  }, [showWebView, devStatus?.running]);

  const handleOpen = useCallback(() => {
    // For Expo/RN: open Expo Go with deep link for full native experience (camera, BLE, etc.)
    // For Flutter/Vite/Next: open WebView with web build
    const isExpo = devStatus?.framework === "expo" || devStatus?.framework === "react-native";
    if (isExpo && devStatus?.deepLink) {
      Alert.alert(
        "Open App",
        "Choose how to load your app:",
        [
          {
            text: "Expo Go (Native)",
            onPress: () => Linking.openURL(devStatus.deepLink!).catch(() => {
              Alert.alert("Expo Go not installed", "Install Expo Go from the App Store to get full native hot reload with camera, BLE, QR, etc.");
            }),
          },
          {
            text: "WebView (Preview)",
            onPress: () => { setShowWebView(true); setWebViewLoading(true); setWebViewKey(k => k + 1); },
          },
          { text: "Cancel", style: "cancel" },
        ],
      );
    } else {
      setShowWebView(true);
      setWebViewLoading(true);
      setWebViewKey(k => k + 1);
    }
  }, [devStatus]);

  const handleReload = useCallback(async () => {
    setWebViewLoading(true);
    await quicClient.reloadDevServer();
    setWebViewKey(k => k + 1);
  }, []);

  const handleStop = useCallback(() => {
    Alert.alert("Stop Dev Server", "Stop the running dev server?", [
      { text: "Cancel", style: "cancel" },
      {
        text: "Stop", style: "destructive", onPress: async () => {
          await quicClient.stopDevServer();
          setShowWebView(false);
          setDevStatus(null);
        }
      },
    ]);
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
      });
    } catch {
      Alert.alert("Failed", `Could not start dev server for ${project.name}`);
    } finally {
      setStartingProject(null);
    }
  }, [devStatus, handleOpen]);

  const bundleUrl = devStatus ? quicClient.getDevServerBundleUrl(devStatus.bundleUrl || "/dev/") : "";
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

        {/* Running / starting dev server card */}
        {devStatus && (
          <View style={[s.card, s.activeCard, !devStatus.running && { borderColor: "#f59e0b44", backgroundColor: "#1a150f" }]}>
            <View style={s.cardHeader}>
              <View style={[s.statusDot, { backgroundColor: devStatus.running ? "#22c55e" : "#f59e0b" }]} />
              <View style={s.cardTitleContainer}>
                <Text style={s.cardTitle}>{runningProject}</Text>
                <Text style={s.cardMeta}>
                  {devStatus.running
                    ? `${devStatus.framework} · port ${devStatus.port} · hot reload ${devStatus.hotReload ? "on" : "off"}`
                    : `${devStatus.framework} · starting...`}
                </Text>
              </View>
              {!devStatus.running && <ActivityIndicator size="small" color="#f59e0b" />}
            </View>
            <View style={s.cardActions}>
              {devStatus.running && (
                <>
                  <Pressable style={[s.actionBtn, s.openBtn]} onPress={handleOpen}>
                    <Text style={s.openBtnText}>Open App</Text>
                  </Pressable>
                  <Pressable style={[s.actionBtn, s.reloadBtn]} onPress={handleReload}>
                    <Text style={s.reloadBtnText}>{"\u21BB"} Reload</Text>
                  </Pressable>
                </>
              )}
              <Pressable style={[s.actionBtn, s.stopBtn]} onPress={handleStop}>
                <Text style={s.stopBtnText}>Stop</Text>
              </Pressable>
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
                    <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Start</Text>
                  )}
                </View>
              </Pressable>
            );
          }}
          ListEmptyComponent={
            <View style={s.emptyList}>
              <Text style={[s.emptySubtitle, { color: c.textMuted }]}>
                {projects.length > 0
                  ? "No hot-reloadable projects found.\nLooking for Expo, Flutter, Next.js, or Vite projects."
                  : "No projects discovered yet.\nThe agent scans your home directory automatically."}
              </Text>
            </View>
          }
        />
      </View>

      {/* Full-screen WebView */}
      <Modal visible={showWebView} animationType="slide" presentationStyle="fullScreen">
        <View style={[s.safe, { backgroundColor: c.bg }]}>
          <View style={[s.webViewHeader, { borderBottomColor: c.border, paddingTop: insets.top + 8 }]}>
            <Pressable onPress={() => setShowWebView(false)}>
              <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Back</Text>
            </Pressable>
            <View style={s.webViewHeaderCenter}>
              <View style={[s.statusDot, { backgroundColor: "#22c55e" }]} />
              <Text style={[s.webViewTitle, { color: c.textPrimary }]}>{runningProject}</Text>
            </View>
            <View style={s.webViewHeaderActions}>
              <Pressable onPress={handleReload}>
                <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>Reload</Text>
              </Pressable>
              <Pressable onPress={handleStop}>
                <Text style={{ color: c.error, fontSize: 14, fontWeight: "600", marginLeft: 16 }}>Stop</Text>
              </Pressable>
            </View>
          </View>
          {webViewLoading && (
            <View style={[s.loadingBar, { backgroundColor: c.accent }]} />
          )}
          <WebView
            ref={webViewRef}
            key={webViewKey}
            source={{ uri: bundleUrl }}
            style={{ flex: 1, backgroundColor: c.bg }}
            onLoadEnd={() => setWebViewLoading(false)}
            onError={() => setWebViewLoading(false)}
            javaScriptEnabled
            domStorageEnabled
            allowsInlineMediaPlayback
          />
        </View>
      </Modal>
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

  // Empty
  emptyContainer: { flex: 1, alignItems: "center", justifyContent: "center", padding: 40 },
  emptyIcon: { fontSize: 40, marginBottom: 12 },
  emptyTitle: { fontSize: 18, fontWeight: "700", marginBottom: 4 },
  emptySubtitle: { fontSize: 13, textAlign: "center", lineHeight: 20 },
  emptyList: { padding: 40, alignItems: "center" },

  // WebView header
  webViewHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingTop: 12, paddingBottom: 10, borderBottomWidth: 1 },
  webViewHeaderCenter: { flexDirection: "row", alignItems: "center", gap: 6 },
  webViewTitle: { fontSize: 15, fontWeight: "700" },
  webViewHeaderActions: { flexDirection: "row", alignItems: "center" },
  loadingBar: { height: 2, opacity: 0.6 },
});
