import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { WebView } from "react-native-webview";
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
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

// ── Apps Tab ───────────────────────────────────────────────────────

export default function AppsScreen() {
  const c = useColors();
  const { activeDevice, connectionStatus } = useDevice();
  const isConnected = connectionStatus === "connected" && !!activeDevice;

  const [devStatus, setDevStatus] = useState<DevServerStatus | null>(null);
  const [projects, setProjects] = useState<ProjectItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [startingProject, setStartingProject] = useState<string | null>(null);
  const [showWebView, setShowWebView] = useState(false);
  const [webViewKey, setWebViewKey] = useState(0);
  const [webViewLoading, setWebViewLoading] = useState(false);
  const [search, setSearch] = useState("");
  const [activeFilter, setActiveFilter] = useState<string | null>(null);
  const webViewRef = useRef<WebView>(null);

  // Poll dev server status + projects
  useEffect(() => {
    if (!isConnected) return;
    let mounted = true;

    const poll = async () => {
      try {
        const status = await quicClient.getDevServerStatus();
        if (mounted) setDevStatus(status?.running ? status : null);
      } catch {
        if (mounted) setDevStatus(null);
      }

      try {
        const list = await quicClient.listProjects();
        if (mounted) setProjects(list);
      } catch {}
    };

    poll();
    const interval = setInterval(poll, 3000);
    return () => { mounted = false; clearInterval(interval); };
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

  const router = useRouter();

  const handleStartProject = useCallback(async (projectName: string) => {
    setStartingProject(projectName);
    try {
      // Signal CLI directly: switch project + start dev server
      const result = await quicClient.switchProject(projectName, true);
      if (result.devServer?.running) {
        // Dev server started directly — stay on Apps, it'll show the green card
        setStartingProject(null);
        return;
      }
      // Dev server didn't start instantly (needs npm install, etc.) — create a task
      await quicClient.sendTask(
        `Run ${projectName} on my phone`,
        `Start the dev server for ${projectName} and load it on the phone via the Yaver P2P channel.`,
      );
      router.navigate("/(tabs)/tasks");
    } catch (e) {
      // Fallback: create task for the agent to handle
      try {
        await quicClient.sendTask(
          `Run ${projectName} on my phone`,
          `Start the dev server for ${projectName} and load it on the phone via the Yaver P2P channel.`,
        );
        router.navigate("/(tabs)/tasks");
      } catch {
        Alert.alert("Failed", String(e));
      }
    } finally {
      setStartingProject(null);
    }
  }, [router]);

  const handleOpen = useCallback(() => {
    setShowWebView(true);
    setWebViewLoading(true);
    setWebViewKey(k => k + 1);
  }, []);

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

  const bundleUrl = devStatus ? quicClient.getDevServerBundleUrl(devStatus.bundleUrl || "/dev/") : "";

  if (!isConnected) {
    return (
      <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <View style={s.emptyContainer}>
          <Text style={[s.emptyIcon, { color: c.textMuted }]}>{"\u{1F4F1}"}</Text>
          <Text style={[s.emptyTitle, { color: c.textPrimary }]}>Not connected</Text>
          <Text style={[s.emptySubtitle, { color: c.textSecondary }]}>
            Connect to a device to see your apps
          </Text>
        </View>
      </SafeAreaView>
    );
  }

  const runningProject = devStatus?.workDir?.split("/").pop() ?? devStatus?.framework ?? "App";

  return (
    <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <View style={s.container}>

        {/* Running app — green card */}
        {devStatus && (
          <View style={[s.card, s.activeCard]}>
            <View style={s.cardHeader}>
              <View style={[s.statusDot, { backgroundColor: "#22c55e" }]} />
              <View style={s.cardTitleContainer}>
                <Text style={s.cardTitle}>{runningProject}</Text>
                <Text style={s.cardMeta}>
                  {devStatus.framework} · port {devStatus.port} · hot reload {devStatus.hotReload ? "on" : "off"}
                </Text>
              </View>
            </View>
            <View style={s.cardActions}>
              <Pressable style={[s.actionBtn, s.openBtn]} onPress={handleOpen}>
                <Text style={s.openBtnText}>Open App</Text>
              </Pressable>
              <Pressable style={[s.actionBtn, s.reloadBtn]} onPress={handleReload}>
                <Text style={s.reloadBtnText}>{"\u21BB"} Reload</Text>
              </Pressable>
              <Pressable style={[s.actionBtn, s.stopBtn]} onPress={handleStop}>
                <Text style={s.stopBtnText}>Stop</Text>
              </Pressable>
            </View>
          </View>
        )}

        {/* Search + Projects list */}
        <View style={[s.searchRow, { borderColor: c.border }]}>
          <Text style={{ color: c.textMuted, fontSize: 14 }}>{"\u{1F50D}"}</Text>
          <TextInput
            style={[s.searchInput, { color: c.textPrimary }]}
            placeholder="Search projects..."
            placeholderTextColor={c.textMuted}
            value={search}
            onChangeText={setSearch}
            autoCorrect={false}
            autoCapitalize="none"
          />
          {search.length > 0 && (
            <Pressable onPress={() => setSearch("")}>
              <Text style={{ color: c.textMuted, fontSize: 14 }}>{"\u2715"}</Text>
            </Pressable>
          )}
        </View>

        {/* Tag filter chips */}
        {(() => {
          const allTags = new Set<string>();
          projects.forEach((p) => {
            if (p.framework) allTags.add(p.framework);
            (p.tags ?? []).forEach((t: string) => allTags.add(t));
          });
          const tags = Array.from(allTags).sort();
          if (tags.length === 0) return null;
          return (
            <ScrollView horizontal showsHorizontalScrollIndicator={false} style={s.filterRow} contentContainerStyle={s.filterRowContent}>
              <Pressable
                style={[s.filterChip, !activeFilter && s.filterChipActive]}
                onPress={() => setActiveFilter(null)}
              >
                <Text style={[s.filterChipText, !activeFilter && s.filterChipTextActive]}>All</Text>
              </Pressable>
              {tags.map((tag) => (
                <Pressable
                  key={tag}
                  style={[s.filterChip, activeFilter === tag && s.filterChipActive]}
                  onPress={() => setActiveFilter(activeFilter === tag ? null : tag)}
                >
                  <Text style={[s.filterChipText, activeFilter === tag && s.filterChipTextActive]}>{tag}</Text>
                </Pressable>
              ))}
            </ScrollView>
          );
        })()}

        <FlatList
          data={projects.filter((p) => {
            // Fuzzy search
            if (search.trim()) {
              const q = search.toLowerCase();
              const match = p.name.toLowerCase().includes(q) ||
                (p.branch?.toLowerCase().includes(q)) ||
                p.path.toLowerCase().includes(q) ||
                (p.framework?.toLowerCase().includes(q)) ||
                (p.tags ?? []).some((t: string) => t.toLowerCase().includes(q));
              if (!match) return false;
            }
            // Tag filter
            if (activeFilter) {
              return p.framework === activeFilter || (p.tags ?? []).includes(activeFilter);
            }
            return true;
          })}
          keyExtractor={(item) => item.path}
          contentContainerStyle={s.listContent}
          renderItem={({ item }) => {
            const isRunning = devStatus?.workDir === item.path;
            const isStarting = startingProject === item.name;

            return (
              <Pressable
                style={[s.card, s.projectCard, { backgroundColor: c.bgCard, borderColor: c.border },
                  isRunning && { borderColor: "#22c55e44" }]}
                onPress={() => {
                  if (isRunning) {
                    handleOpen();
                  } else {
                    handleStartProject(item.name);
                  }
                }}
                disabled={isStarting}
              >
                <View style={s.cardHeader}>
                  <View style={[s.statusDot, { backgroundColor: isRunning ? "#22c55e" : c.textMuted }]} />
                  <View style={s.cardTitleContainer}>
                    <Text style={[s.projectName, { color: c.textPrimary }]}>{item.name}</Text>
                    {((item.framework ? [item.framework] : []).concat(item.tags ?? [])).length > 0 && (
                      <View style={s.tagRow}>
                        {[...(item.framework ? [item.framework] : []), ...(item.tags ?? [])].filter((v, i, a) => a.indexOf(v) === i).map((tag) => (
                          <View key={tag} style={s.tag}>
                            <Text style={s.tagText}>{tag}</Text>
                          </View>
                        ))}
                      </View>
                    )}
                    <Text style={[s.projectMeta, { color: c.textMuted }]} numberOfLines={1}>
                      {item.branch ? `${item.branch} · ` : ""}{item.path}
                    </Text>
                  </View>
                  {isStarting ? (
                    <ActivityIndicator size="small" color={c.accent} />
                  ) : isRunning ? (
                    <Text style={{ color: "#22c55e", fontSize: 12, fontWeight: "600" }}>Running</Text>
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
                No projects discovered yet.{"\n"}The agent scans your home directory automatically.
              </Text>
            </View>
          }
        />
      </View>

      {/* Full-screen WebView */}
      <Modal visible={showWebView} animationType="slide" presentationStyle="fullScreen">
        <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["top"]}>
          <View style={[s.webViewHeader, { borderBottomColor: c.border }]}>
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
        </SafeAreaView>
      </Modal>
    </SafeAreaView>
  );
}

// ── Styles ─────────────────────────────────────────────────────────

const s = StyleSheet.create({
  safe: { flex: 1 },
  container: { flex: 1 },

  // Search
  searchRow: {
    flexDirection: "row",
    alignItems: "center",
    marginHorizontal: 16,
    marginTop: 12,
    marginBottom: 8,
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 10,
    borderWidth: 1,
    gap: 8,
  },
  searchInput: { flex: 1, fontSize: 14, paddingVertical: 0 },

  // Filter chips
  filterRow: { marginHorizontal: 16, marginBottom: 8 },
  filterRowContent: { gap: 6 },
  filterChip: {
    paddingHorizontal: 10,
    paddingVertical: 5,
    borderRadius: 6,
    backgroundColor: "#111",
    borderWidth: 1,
    borderColor: "#222",
  },
  filterChipActive: { backgroundColor: "#6366f122", borderColor: "#6366f1" },
  filterChipText: { fontSize: 11, fontWeight: "600", color: "#888" },
  filterChipTextActive: { color: "#818cf8" },

  // Tag chips on cards
  tagRow: { flexDirection: "row", flexWrap: "wrap", gap: 4, marginTop: 3 },
  tag: {
    backgroundColor: "#6366f115",
    borderRadius: 4,
    paddingHorizontal: 5,
    paddingVertical: 1,
  },
  tagText: { color: "#818cf8", fontSize: 9, fontWeight: "600" },

  // Active app card
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

  // Section
  sectionTitle: { fontSize: 11, fontWeight: "600", textTransform: "uppercase", letterSpacing: 1, marginHorizontal: 16, marginTop: 20, marginBottom: 8 },

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
  webViewHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingVertical: 10, borderBottomWidth: 1 },
  webViewHeaderCenter: { flexDirection: "row", alignItems: "center", gap: 6 },
  webViewTitle: { fontSize: 15, fontWeight: "700" },
  webViewHeaderActions: { flexDirection: "row", alignItems: "center" },
  loadingBar: { height: 2, opacity: 0.6 },
});
