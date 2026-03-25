import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  NativeModules,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useDevice } from "../../src/context/DeviceContext";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient, type DevServerStatus } from "../../src/lib/quic";
import type { BuildSummary, DownloadProgress } from "../../src/lib/builds";
import {
  downloadArtifact,
  formatSize,
  canInstallArtifact,
  installIPA,
} from "../../src/lib/builds";

// ── Status helpers ──────────────────────────────────────────────────

const STATUS_COLORS: Record<string, string> = {
  running: "#6366f1",
  completed: "#22c55e",
  failed: "#ef4444",
  cancelled: "#a1a1aa",
};

function StatusBadge({ status }: { status: string }) {
  const color = STATUS_COLORS[status] ?? "#a1a1aa";
  return (
    <View style={[styles.badge, { backgroundColor: color + "22" }]}>
      {status === "running" && (
        <ActivityIndicator size="small" color={color} style={{ marginRight: 4 }} />
      )}
      <Text style={[styles.badgeText, { color }]}>{status}</Text>
    </View>
  );
}

function PlatformBadge({ platform }: { platform: string }) {
  return (
    <View style={[styles.badge, { backgroundColor: "#3b82f622" }]}>
      <Text style={[styles.badgeText, { color: "#60a5fa" }]}>{platform}</Text>
    </View>
  );
}

// ── Build Item ──────────────────────────────────────────────────────

function BuildItem({ build, onRefresh }: { build: BuildSummary; onRefresh: () => void }) {
  const c = useColors();
  const [downloading, setDownloading] = useState(false);
  const [progress, setProgress] = useState<DownloadProgress | null>(null);
  const [localPath, setLocalPath] = useState<string | null>(null);

  const handleDownload = useCallback(async () => {
    if (!build.artifactName) return;
    setDownloading(true);
    setProgress(null);
    try {
      const path = await downloadArtifact(
        quicClient.baseUrl,
        quicClient.getAuthHeaders(),
        build.id,
        (p) => setProgress(p),
      );
      setLocalPath(path);
      Alert.alert("Downloaded", `Saved to ${path}`);
    } catch (e) {
      Alert.alert("Download failed", e instanceof Error ? e.message : String(e));
    } finally {
      setDownloading(false);
    }
  }, [build.id, build.artifactName]);

  const handleInstall = useCallback(async () => {
    if (!localPath && !build.artifactName) return;

    // iOS OTA install
    if (Platform.OS === "ios" && build.artifactName?.toLowerCase().endsWith(".ipa")) {
      try {
        const manifestUrl = `${quicClient.baseUrl}/builds/${build.id}/manifest`;
        await installIPA(manifestUrl);
      } catch (e) {
        Alert.alert("Install failed", e instanceof Error ? e.message : String(e));
      }
      return;
    }

    // Android APK install
    if (Platform.OS === "android" && localPath) {
      try {
        await NativeModules.ApkInstaller.install(localPath);
      } catch (e) {
        Alert.alert("Install failed", e instanceof Error ? e.message : String(e));
      }
    } else if (Platform.OS === "android" && !localPath) {
      Alert.alert("Download first", "Download the artifact before installing.");
    }
  }, [localPath, build.id, build.artifactName]);

  const showInstall = build.status === "completed" && build.artifactName && canInstallArtifact(build.artifactName);

  return (
    <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
      <View style={styles.cardHeader}>
        <Text style={[styles.buildId, { color: c.textMuted }]} numberOfLines={1}>
          {build.id.slice(0, 8)}
        </Text>
        <PlatformBadge platform={build.platform} />
        <StatusBadge status={build.status} />
      </View>

      {build.artifactName && (
        <View style={styles.artifactRow}>
          <Text style={[styles.artifactName, { color: c.textPrimary }]} numberOfLines={1}>
            {build.artifactName}
          </Text>
          {build.artifactSize != null && (
            <Text style={[styles.artifactSize, { color: c.textMuted }]}>
              {formatSize(build.artifactSize)}
            </Text>
          )}
        </View>
      )}

      {downloading && progress && (
        <View style={styles.progressRow}>
          <View style={[styles.progressBar, { backgroundColor: c.border }]}>
            <View
              style={[styles.progressFill, { width: `${progress.percent}%`, backgroundColor: "#6366f1" }]}
            />
          </View>
          <Text style={[styles.progressText, { color: c.textMuted }]}>{progress.percent}%</Text>
        </View>
      )}

      <View style={styles.actions}>
        {build.status === "completed" && build.artifactName && (
          <Pressable
            style={[styles.actionBtn, { backgroundColor: "#6366f122" }]}
            onPress={handleDownload}
            disabled={downloading}
          >
            {downloading ? (
              <ActivityIndicator size="small" color="#818cf8" />
            ) : (
              <Text style={[styles.actionText, { color: "#818cf8" }]}>Download</Text>
            )}
          </Pressable>
        )}
        {showInstall && (
          <Pressable
            style={[styles.actionBtn, { backgroundColor: "#22c55e22" }]}
            onPress={handleInstall}
          >
            <Text style={[styles.actionText, { color: "#4ade80" }]}>Install</Text>
          </Pressable>
        )}
      </View>
    </View>
  );
}

// ── Screen ──────────────────────────────────────────────────────────

export default function BuildsScreen() {
  const c = useColors();
  const { connectionStatus, activeDevice } = useDevice();
  const [builds, setBuilds] = useState<BuildSummary[]>([]);
  const [loading, setLoading] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const isConnected = connectionStatus === "connected";

  const fetchBuilds = useCallback(async () => {
    if (!isConnected) return;
    try {
      const list = await quicClient.listBuilds();
      setBuilds(list);
    } catch {
      // silent
    }
  }, [isConnected]);

  // Initial fetch + poll every 5s
  useEffect(() => {
    if (!isConnected) {
      setBuilds([]);
      return;
    }
    setLoading(true);
    fetchBuilds().finally(() => setLoading(false));

    pollRef.current = setInterval(fetchBuilds, 5000);
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [isConnected, fetchBuilds]);

  const renderItem = useCallback(
    ({ item }: { item: BuildSummary }) => <BuildItem build={item} onRefresh={fetchBuilds} />,
    [fetchBuilds],
  );

  // ── Repositories (discovered projects on the machine) ──
  const router = useRouter();
  const [projects, setProjects] = useState<{ name: string; path: string; branch?: string; framework?: string; gitRemote?: string }[]>([]);
  const [devStatus, setDevStatus] = useState<DevServerStatus | null>(null);
  const [discovering, setDiscovering] = useState(false);
  const [startingProject, setStartingProject] = useState<string | null>(null);
  const [repoSearch, setRepoSearch] = useState("");

  useEffect(() => {
    if (!isConnected) { setProjects([]); setDevStatus(null); return; }
    let mounted = true;
    const poll = async () => {
      try {
        const [list, ds] = await Promise.all([
          quicClient.listProjects(),
          quicClient.getDevServerStatus(),
        ]);
        if (mounted) {
          setProjects(list);
          setDevStatus(ds?.running ? ds : null);
        }
      } catch {}
    };
    poll();
    const interval = setInterval(poll, 5000);
    return () => { mounted = false; clearInterval(interval); };
  }, [isConnected]);

  const handleDiscover = useCallback(async () => {
    setDiscovering(true);
    try {
      // Trigger fresh project discovery via a task
      await quicClient.sendTask(
        "Discover all projects on this machine",
        "Run yaver discover to scan for git repositories and update the project list.",
      );
    } catch {}
    setTimeout(() => setDiscovering(false), 3000);
  }, []);

  const handleStartProject = useCallback(async (name: string, path: string) => {
    const isRunning = devStatus?.workDir === path;
    if (isRunning) {
      // Already running — reload with latest code
      try {
        await quicClient.reloadDevServer();
      } catch {}
      router.navigate("/(tabs)/apps");
      return;
    }
    setStartingProject(name);
    try {
      await quicClient.sendTask(
        `Run ${name} on my phone`,
        `Start the dev server for ${name} at ${path} and load it on the phone via the Yaver P2P channel.`,
      );
      router.navigate("/(tabs)/tasks");
    } catch (e) {
      Alert.alert("Failed", String(e));
    } finally {
      setStartingProject(null);
    }
  }, [devStatus, router]);

  return (
    <SafeAreaView style={[styles.container, { backgroundColor: c.bg }]} edges={["bottom"]}>
      {!isConnected ? (
        <View style={styles.center}>
          <Text style={[styles.emptyText, { color: c.textMuted }]}>
            Connect to a device to view builds
          </Text>
        </View>
      ) : (
        <ScrollView contentContainerStyle={styles.list}>
          {/* ── Machine + Discover ── */}
          {activeDevice && (
            <View style={[styles.machineCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <View style={[styles.machineDot, { backgroundColor: c.success || "#22c55e" }]} />
              <View style={{ flex: 1 }}>
                <Text style={[styles.machineName, { color: c.textPrimary }]}>
                  {activeDevice.name?.replace(/\.local$/, "")}
                </Text>
                <Text style={{ fontSize: 11, color: c.textMuted }}>
                  {projects.length} projects · {activeDevice.os || "unknown"}
                </Text>
              </View>
              <Pressable onPress={handleDiscover} disabled={discovering}>
                <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>
                  {discovering ? "Scanning..." : "Discover"}
                </Text>
              </Pressable>
            </View>
          )}

          {/* Search */}
          <View style={[styles.repoSearchRow, { borderColor: c.border }]}>
            <Text style={{ color: c.textMuted, fontSize: 14 }}>{"\u{1F50D}"}</Text>
            <TextInput
              style={[styles.repoSearchInput, { color: c.textPrimary }]}
              placeholder="Search repos..."
              placeholderTextColor={c.textMuted}
              value={repoSearch}
              onChangeText={setRepoSearch}
              autoCorrect={false}
              autoCapitalize="none"
            />
            {repoSearch.length > 0 && (
              <Pressable onPress={() => setRepoSearch("")}>
                <Text style={{ color: c.textMuted, fontSize: 14 }}>{"\u2715"}</Text>
              </Pressable>
            )}
          </View>

          {/* ── Project Cards (green = serving, gray = discovered) ── */}
          {projects.filter((p) => {
            if (!repoSearch.trim()) return true;
            const q = repoSearch.toLowerCase();
            return p.name.toLowerCase().includes(q) ||
              (p.branch?.toLowerCase().includes(q)) ||
              (p.framework?.toLowerCase().includes(q)) ||
              p.path.toLowerCase().includes(q);
          }).map((p) => {
            const isRunning = devStatus?.workDir === p.path;
            const isStarting = startingProject === p.name;
            return (
              <Pressable
                key={p.path}
                style={[styles.repoCard, {
                  backgroundColor: isRunning ? "#0f1a0f" : c.bgCard,
                  borderColor: isRunning ? "#22c55e44" : c.border,
                }]}
                onPress={() => handleStartProject(p.name, p.path)}
                disabled={isStarting}
              >
                <View style={styles.repoRow}>
                  <View style={[styles.repoDot, { backgroundColor: isRunning ? "#22c55e" : "#555" }]} />
                  <View style={{ flex: 1 }}>
                    <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                      <Text style={[styles.repoName, { color: isRunning ? "#fff" : c.textSecondary }]}>{p.name}</Text>
                      {p.framework && (
                        <View style={[styles.frameworkChip, isRunning && { backgroundColor: "#22c55e22", borderColor: "#22c55e44" }]}>
                          <Text style={[styles.frameworkChipText, isRunning && { color: "#22c55e" }]}>{p.framework}</Text>
                        </View>
                      )}
                    </View>
                    <Text style={{ fontSize: 11, color: isRunning ? "#22c55e88" : c.textMuted, marginTop: 2 }} numberOfLines={1}>
                      {p.branch ? `${p.branch} · ` : ""}{p.path}
                    </Text>
                  </View>
                  {isStarting ? (
                    <ActivityIndicator size="small" color={c.accent} />
                  ) : isRunning ? (
                    <View style={styles.repoRunningBadge}>
                      <Text style={styles.repoRunningText}>{"\u21BB"} Reload</Text>
                    </View>
                  ) : (
                    <Text style={{ color: "#888", fontSize: 12, fontWeight: "600" }}>{"\u25B6"}</Text>
                  )}
                </View>
              </Pressable>
            );
          })}

          {projects.length === 0 && !loading && (
            <View style={{ padding: 40, alignItems: "center" }}>
              <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center" }}>
                No projects found.{"\n"}Tap "Discover" to scan your machine.
              </Text>
            </View>
          )}

          {/* ── Build Artifacts ── */}
          {builds.length > 0 && (
            <>
              <View style={[styles.sectionHeader, { marginTop: 16 }]}>
                <Text style={[styles.sectionTitle, { color: c.textMuted }]}>Build Artifacts</Text>
              </View>
              {builds.map((build) => (
                <BuildItem key={build.id} build={build} onRefresh={fetchBuilds} />
              ))}
            </>
          )}

          {loading && builds.length === 0 && projects.length === 0 && (
            <View style={{ padding: 40, alignItems: "center" }}>
              <ActivityIndicator size="large" color={c.textMuted} />
            </View>
          )}
        </ScrollView>
      )}
    </SafeAreaView>
  );
}

// ── Styles ──────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    flex: 1,
  },
  center: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
  },
  emptyText: {
    fontSize: 15,
  },
  list: {
    padding: 12,
    gap: 10,
  },
  card: {
    borderRadius: 10,
    borderWidth: 1,
    padding: 12,
  },
  cardHeader: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    marginBottom: 6,
  },
  buildId: {
    fontSize: 13,
    fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
  },
  badge: {
    flexDirection: "row",
    alignItems: "center",
    borderRadius: 6,
    paddingHorizontal: 8,
    paddingVertical: 2,
  },
  badgeText: {
    fontSize: 12,
    fontWeight: "600",
  },
  artifactRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 8,
  },
  artifactName: {
    fontSize: 13,
    flex: 1,
    marginRight: 8,
  },
  artifactSize: {
    fontSize: 12,
  },
  progressRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    marginBottom: 8,
  },
  progressBar: {
    flex: 1,
    height: 4,
    borderRadius: 2,
    overflow: "hidden",
  },
  progressFill: {
    height: "100%",
    borderRadius: 2,
  },
  progressText: {
    fontSize: 12,
    width: 36,
    textAlign: "right",
  },
  actions: {
    flexDirection: "row",
    gap: 8,
  },
  actionBtn: {
    borderRadius: 8,
    paddingHorizontal: 14,
    paddingVertical: 6,
    alignItems: "center",
    justifyContent: "center",
    minWidth: 80,
  },
  actionText: {
    fontSize: 13,
    fontWeight: "600",
  },
  // Machine + Repo cards
  sectionHeader: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 8,
    marginTop: 4,
  },
  sectionTitle: {
    fontSize: 11,
    fontWeight: "600",
    textTransform: "uppercase",
    letterSpacing: 1,
  },
  machineCard: {
    flexDirection: "row",
    alignItems: "center",
    padding: 12,
    borderRadius: 10,
    borderWidth: 1,
    gap: 10,
    marginBottom: 12,
  },
  machineDot: { width: 8, height: 8, borderRadius: 4 },
  machineName: { fontSize: 14, fontWeight: "700" },
  repoCard: {
    borderRadius: 10,
    borderWidth: 1,
    padding: 12,
    marginBottom: 6,
  },
  repoRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
  repoDot: { width: 8, height: 8, borderRadius: 4 },
  repoName: { fontSize: 14, fontWeight: "600" },
  repoRunningBadge: {
    backgroundColor: "#22c55e22",
    borderRadius: 6,
    paddingHorizontal: 10,
    paddingVertical: 4,
  },
  repoRunningText: { color: "#22c55e", fontSize: 12, fontWeight: "600" },
  repoSearchRow: {
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 1,
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 8,
    marginBottom: 10,
    gap: 8,
  },
  repoSearchInput: { flex: 1, fontSize: 14, paddingVertical: 0 },
  frameworkChip: {
    backgroundColor: "#6366f115",
    borderWidth: 1,
    borderColor: "#6366f130",
    borderRadius: 4,
    paddingHorizontal: 5,
    paddingVertical: 1,
  },
  frameworkChipText: { color: "#818cf8", fontSize: 10, fontWeight: "600" },
});
