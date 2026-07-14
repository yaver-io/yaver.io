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
  type StyleProp,
  type ViewStyle,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useDevice } from "../../src/context/DeviceContext";
import { useColors } from "../../src/context/ThemeContext";
import { AppBackButton } from "../../src/components/AppBackButton";
import EmptyState from "../../src/components/EmptyState";
import NoMachineEmpty from "../../src/components/NoMachineEmpty";
import { quicClient, type DevServerStatus } from "../../src/lib/quic";
import type { BuildSummary, DownloadProgress } from "../../src/lib/builds";
import {
  downloadArtifact,
  formatSize,
  canInstallArtifact,
  installIPA,
} from "../../src/lib/builds";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";

type PublishConfigView = {
  config?: {
    targets?: Array<{ id: string; label?: string; kind: string }>;
    fallback?: { githubAllowed?: boolean };
  };
  exists: boolean;
  path: string;
};

type UnityRunSummary = {
  ok: boolean;
  status?: string;
  stage?: string;
  projectPath?: string;
  mode?: string;
  buildTarget?: string;
  executeMethod?: string;
  outputPath?: string;
  executablePath?: string;
  logPath?: string;
  resultsPath?: string;
  summary?: string;
  artifacts?: string[];
  nextAction?: string;
  command?: string[];
};

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

function unityRunLabel(run: UnityRunSummary) {
  if (run.stage === "test") return `Tests${run.mode ? ` · ${run.mode}` : ""}`;
  if (run.stage === "build") return `Build${run.buildTarget ? ` · ${run.buildTarget}` : ""}`;
  if (run.stage === "relaunch") return "Relaunch";
  return run.stage || "Unity";
}

function unityRunPath(run: UnityRunSummary) {
  return run.executablePath || run.outputPath || run.resultsPath || run.logPath || run.projectPath || "";
}

// ── Build Item ──────────────────────────────────────────────────────

function BuildItem({ build, onRefresh, style }: { build: BuildSummary; onRefresh: () => void; style?: StyleProp<ViewStyle> }) {
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
    <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }, style]}>
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
  const layout = useResponsiveLayout();
  const tabletContent = useTabletContentStyle("wide");
  const { connectionStatus, activeDevice } = useDevice();
  const [builds, setBuilds] = useState<BuildSummary[]>([]);
  const [publishRuns, setPublishRuns] = useState<Array<{ id: string; targetId: string; status: string; provider: string }>>([]);
  const [publishConfig, setPublishConfig] = useState<PublishConfigView | null>(null);
  const [selectedPublishProject, setSelectedPublishProject] = useState<string>("");
  const [publishBusy, setPublishBusy] = useState<string | null>(null);
  const [allowGitHubFallback, setAllowGitHubFallback] = useState(false);
  const [unityRuns, setUnityRuns] = useState<UnityRunSummary[]>([]);
  const [loading, setLoading] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const isConnected = connectionStatus === "connected";

  const fetchBuilds = useCallback(async () => {
    if (!isConnected) return;
    try {
      const [list, runs] = await Promise.all([
        quicClient.listBuilds(),
        quicClient.listUnityRuns(),
      ]);
      setBuilds(list);
      setUnityRuns(runs);
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
  const projectCols = layout.layoutClass === "phone" ? 1 : layout.layoutClass === "tablet-portrait" ? 2 : 3;
  const cardGridItemStyle = projectCols > 1
    ? { width: `${(100 - (projectCols - 1) * 1.5) / projectCols}%` as const }
    : null;

  useEffect(() => {
    if (!isConnected) { setProjects([]); setDevStatus(null); return; }
    let mounted = true;
    const poll = async () => {
      try {
        const [projectData, ds] = await Promise.all([
          quicClient.listProjectsDetailed(),
          quicClient.getDevServerStatus(),
        ]);
        if (mounted) {
          setProjects(projectData.projects);
          if (!selectedPublishProject && projectData.projects[0]?.path) {
            setSelectedPublishProject(projectData.projects[0].path);
          }
          setDiscovering(!!projectData.discovery?.discovering);
          setDevStatus(ds?.running ? ds : null);
        }
      } catch {}
    };
    poll();
    const interval = setInterval(poll, 5000);
    return () => { mounted = false; clearInterval(interval); };
  }, [isConnected, selectedPublishProject]);

  useEffect(() => {
    if (!isConnected || !selectedPublishProject) {
      setPublishConfig(null);
      setPublishRuns([]);
      return;
    }
    let mounted = true;
    const poll = async () => {
      try {
        const [cfgRaw, runs] = await Promise.all([
          quicClient.getPublishConfig(selectedPublishProject),
          quicClient.listPublishes(),
        ]);
        if (!mounted) return;
        const cfg = cfgRaw as PublishConfigView | null;
        setPublishConfig(cfg);
        setPublishRuns(runs);
        setAllowGitHubFallback(Boolean(cfg?.config?.fallback?.githubAllowed));
      } catch {}
    };
    void poll();
    const interval = setInterval(poll, 5000);
    return () => { mounted = false; clearInterval(interval); };
  }, [isConnected, selectedPublishProject]);

  const handlePublish = useCallback(async (targetId: string) => {
    if (!selectedPublishProject) return;
    setPublishBusy(targetId);
    try {
      const run = await quicClient.startPublish(selectedPublishProject, targetId, allowGitHubFallback);
      if (!run) throw new Error("Publish did not start");
      Alert.alert("Publish started", `${run.targetId} via ${run.provider}`);
      const runs = await quicClient.listPublishes();
      setPublishRuns(runs);
    } catch (e) {
      Alert.alert("Publish failed", e instanceof Error ? e.message : String(e));
    } finally {
      setPublishBusy(null);
    }
  }, [allowGitHubFallback, selectedPublishProject]);

  const handleDiscover = useCallback(async () => {
    setDiscovering(true);
    try {
      await quicClient.refreshProjects();
    } catch {}
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
      Alert.alert(
        "Couldn't Start Project",
        `Yaver couldn't start ${name} on your phone. Check your connection and try again.\n\n${e instanceof Error ? e.message : String(e)}`,
      );
    } finally {
      setStartingProject(null);
    }
  }, [devStatus, router]);

  return (
    <SafeAreaView style={[styles.container, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <View style={[styles.header, { borderBottomColor: c.border }]}>
        <AppBackButton onPress={() => router.navigate("/(tabs)/more" as any)} />
        <Text style={[styles.headerTitle, { color: c.textPrimary }]}>Builds</Text>
        <View style={styles.headerSpacer} />
      </View>
      {!isConnected ? (
        // "Connect to a device to view builds" named the move but shipped no
        // way to make it. NoMachineEmpty picks the right one for the state:
        // pair a computer if there are none, otherwise open the picker.
        <NoMachineEmpty noun="builds" />
      ) : (
        <ScrollView contentContainerStyle={[styles.list, tabletContent]}>
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
          <View style={[styles.cardGrid, projectCols > 1 && styles.cardGridTablet]}>
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
                }, cardGridItemStyle]}
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
          </View>

          {/* The old copy pointed at a "Discover" button that lives in the
              machine card above — and vanishes entirely when activeDevice is
              null. Carry the scan itself here instead of a pointer to it.
              busy tracks `discovering` (the real scan flag handleDiscover
              sets), not `loading` — this block only renders when !loading. */}
          {projects.length === 0 && !loading && (
            <EmptyState
              icon="folder-open-outline"
              title="No projects found"
              body="Scan your machine for repos Yaver can build, publish, and hot-reload."
              action={{
                label: discovering ? "Scanning…" : "Discover",
                onPress: () => { void handleDiscover(); },
                busy: discovering,
              }}
            />
          )}

          {projects.length > 0 && (
            <>
              <View style={[styles.sectionHeader, { marginTop: 16 }]}>
                <Text style={[styles.sectionTitle, { color: c.textMuted }]}>Publish</Text>
              </View>
              <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.publishProjectRow}>
                {projects.map((project) => {
                  const active = project.path === selectedPublishProject;
                  return (
                    <Pressable
                      key={`pub-${project.path}`}
                      onPress={() => setSelectedPublishProject(project.path)}
                      style={[
                        styles.publishProjectChip,
                        {
                          backgroundColor: active ? "#6366f122" : c.bgCard,
                          borderColor: active ? "#818cf8" : c.border,
                        },
                      ]}
                    >
                      <Text style={{ color: active ? "#818cf8" : c.textSecondary, fontSize: 12, fontWeight: "600" }}>
                        {project.name}
                      </Text>
                    </Pressable>
                  );
                })}
              </ScrollView>

              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <View style={styles.publishHeaderRow}>
                  <Text style={[styles.publishHelper, { color: c.textMuted }]}>
                    Local/self-hosted first. GitHub fallback only when enabled in the project config and requested here.
                  </Text>
                </View>
                <Pressable style={styles.publishToggleRow} onPress={() => setAllowGitHubFallback((v) => !v)}>
                  <View style={[styles.publishCheckbox, { borderColor: c.border, backgroundColor: allowGitHubFallback ? "#6366f1" : "transparent" }]}>
                    {allowGitHubFallback ? <Text style={{ color: "#fff", fontSize: 10, fontWeight: "700" }}>✓</Text> : null}
                  </View>
                  <Text style={{ color: c.textSecondary, fontSize: 13 }}>Allow GitHub fallback for this run</Text>
                </Pressable>
                {publishConfig?.config?.targets?.length ? (
                  <View style={styles.publishTargetsWrap}>
                    {publishConfig.config.targets.map((target) => (
                      <Pressable
                        key={target.id}
                        onPress={() => handlePublish(target.id)}
                        disabled={publishBusy === target.id}
                        style={[styles.actionBtn, { backgroundColor: "#6366f122" }]}
                      >
                        {publishBusy === target.id ? (
                          <ActivityIndicator size="small" color="#818cf8" />
                        ) : (
                          <Text style={[styles.actionText, { color: "#818cf8" }]}>{target.label || target.id}</Text>
                        )}
                      </Pressable>
                    ))}
                  </View>
                ) : (
                  <Text style={{ color: c.textMuted, fontSize: 13 }}>
                    No publish targets yet. Run `yaver publish init` in this repo.
                  </Text>
                )}
              </View>

              {publishRuns.length > 0 && (
                <View style={[styles.publishRunsList, projectCols > 1 && styles.cardGridTablet]}>
                  {publishRuns.slice(0, 8).map((run) => (
                    <View key={run.id} style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }, cardGridItemStyle]}>
                      <View style={styles.cardHeader}>
                        <Text style={[styles.buildId, { color: c.textMuted }]}>{run.id}</Text>
                        <PlatformBadge platform={run.targetId} />
                        <StatusBadge status={run.status} />
                      </View>
                      <Text style={{ color: c.textMuted, fontSize: 12 }}>{run.provider}</Text>
                    </View>
                  ))}
                </View>
              )}
            </>
          )}

          {unityRuns.length > 0 && (
            <>
              <View style={[styles.sectionHeader, { marginTop: 16 }]}>
                <Text style={[styles.sectionTitle, { color: c.textMuted }]}>Unity Runs</Text>
              </View>
              <View style={[styles.publishRunsList, projectCols > 1 && styles.cardGridTablet]}>
                {unityRuns.slice(0, 8).map((run, index) => {
                  const pathHint = unityRunPath(run);
                  const status = run.status || (run.ok ? "completed" : "failed");
                  return (
                    <View
                      key={`${run.projectPath || "unity"}-${run.stage || "run"}-${index}`}
                      style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }, cardGridItemStyle]}
                    >
                      <View style={styles.cardHeader}>
                        <Text style={[styles.buildId, { color: c.textMuted }]} numberOfLines={1}>
                          {unityRunLabel(run)}
                        </Text>
                        <StatusBadge status={status} />
                      </View>
                      {run.summary ? (
                        <Text style={[styles.unitySummaryText, { color: c.textSecondary }]}>
                          {run.summary}
                        </Text>
                      ) : null}
                      {run.nextAction ? (
                        <Text style={[styles.unityMetaText, { color: c.textMuted }]}>
                          Next: {run.nextAction}
                        </Text>
                      ) : null}
                      {pathHint ? (
                        <Text style={[styles.unityMetaText, { color: c.textMuted }]} numberOfLines={1}>
                          {pathHint}
                        </Text>
                      ) : null}
                    </View>
                  );
                })}
              </View>
            </>
          )}

          {/* ── Build Artifacts ── */}
          {builds.length > 0 && (
            <>
              <View style={[styles.sectionHeader, { marginTop: 16 }]}>
                <Text style={[styles.sectionTitle, { color: c.textMuted }]}>Build Artifacts</Text>
              </View>
              <View style={[styles.publishRunsList, projectCols > 1 && styles.cardGridTablet]}>
                {builds.map((build) => (
                  <BuildItem key={build.id} build={build} onRefresh={fetchBuilds} style={cardGridItemStyle} />
                ))}
              </View>
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
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: 1,
  },
  headerTitle: {
    fontSize: 17,
    fontWeight: "700",
  },
  headerSpacer: {
    width: 40,
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
  cardGrid: {
    gap: 10,
  },
  cardGridTablet: {
    flexDirection: "row",
    flexWrap: "wrap",
    alignItems: "stretch",
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
  publishProjectRow: {
    gap: 8,
    paddingBottom: 4,
  },
  publishProjectChip: {
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  publishHeaderRow: {
    marginBottom: 10,
  },
  publishHelper: {
    fontSize: 12,
    lineHeight: 18,
  },
  publishToggleRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    marginBottom: 12,
  },
  publishCheckbox: {
    width: 18,
    height: 18,
    borderWidth: 1,
    borderRadius: 4,
    alignItems: "center",
    justifyContent: "center",
  },
  publishTargetsWrap: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
  },
  publishRunsList: {
    gap: 8,
  },
  unitySummaryText: {
    fontSize: 13,
    lineHeight: 18,
    marginBottom: 4,
  },
  unityMetaText: {
    fontSize: 12,
    lineHeight: 16,
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
