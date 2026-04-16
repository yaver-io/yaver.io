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
import AsyncStorage from "@react-native-async-storage/async-storage";
import { WebView } from "react-native-webview";
import { SafeAreaView, useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { Platform } from "react-native";
import { useDevice } from "../../src/context/DeviceContext";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient, type DevServerStatus } from "../../src/lib/quic";
import { loadApp } from "../../src/lib/bundleLoader";

// ── Types ──────────────────────────────────────────────────────────

interface ProjectItem {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  tags?: string[];
}

const FRAMEWORK_ICONS: Record<string, string> = {
  expo: "\uD83D\uDCF1",
  "react-native": "\u269B",
  react: "\u269B",
  flutter: "\uD83D\uDC26",
  nextjs: "\u25B2",
  vite: "\u26A1",
};

const MOBILE_FRAMEWORKS = ["expo", "react-native", "flutter"];
const WEB_FRAMEWORKS = ["nextjs", "vite", "react"];
const PREVIEW_TARGET_KEY = "@yaver/hotreload_preview_target";

function getProjectCategory(framework?: string): "mobile" | "web" | "other" {
  if (!framework) return "other";
  if (MOBILE_FRAMEWORKS.includes(framework)) return "mobile";
  if (WEB_FRAMEWORKS.includes(framework)) return "web";
  return "other";
}

// ── Projects Tab ──────────────────────────────────────────────────

export default function AppsScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const { activeDevice, connectionStatus, devices } = useDevice();
  const isConnected = connectionStatus === "connected" && !!activeDevice;
  const [selectedTargetId, setSelectedTargetId] = useState<string | null>(null);
  const mobileWorkers = devices.filter((d) => d.deviceClass === "edge-mobile");
  const selectedTarget = mobileWorkers.find((d) => d.id === selectedTargetId) || null;

  const [devStatus, setDevStatus] = useState<DevServerStatus | null>(null);
  const [projects, setProjects] = useState<ProjectItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [startingProject, setStartingProject] = useState<string | null>(null);
  const [showWebView, setShowWebView] = useState(false);
  const [webViewKey, setWebViewKey] = useState(0);
  const [webViewLoading, setWebViewLoading] = useState(false);
  const [search, setSearch] = useState("");
  const [activeFilter, setActiveFilter] = useState<string | null>(null);
  const [actionSheet, setActionSheet] = useState<{
    project: string;
    path: string;
    actions: { label: string; target: string; type: string; framework?: string; platform?: string; command?: string; icon?: string; supported?: boolean; reason?: string }[];
  } | null>(null);
  const [loadingActions, setLoadingActions] = useState(false);

  // Vibing
  const [vibingState, setVibingState] = useState<{
    project: string; path: string;
    suggestions: { id: string; icon: string; label: string; desc: string; category: string; prompt: string; reasoning?: string }[];
    quickActions: { id: string; icon: string; label: string; desc: string; category: string; prompt: string }[];
    history: string[];
  } | null>(null);
  const [customTask, setCustomTask] = useState("");
  const [vibingTaskId, setVibingTaskId] = useState<string | null>(null);
  const [vibingTaskStatus, setVibingTaskStatus] = useState<string>("");
  const [deepShuffleActive, setDeepShuffleActive] = useState(false);
  const [deepShuffleText, setDeepShuffleText] = useState("");
  const [deepShuffleStep, setDeepShuffleStep] = useState("");
  const webViewRef = useRef<WebView>(null);

  // Poll dev server status + all projects
  useEffect(() => {
    if (!isConnected) return;
    let mounted = true;

    const pollStatus = async () => {
      try {
        const status = await quicClient.getDevServerStatus();
        if (mounted) setDevStatus(status?.running ? status : null);
      } catch {
        if (mounted) setDevStatus(null);
      }
    };

    const fetchProjects = async () => {
      try {
        const list = await quicClient.listProjects();
        if (mounted) setProjects(list);
      } catch {}
    };

    pollStatus();
    fetchProjects();
    const statusInterval = setInterval(pollStatus, 3000);
    const projectInterval = setInterval(fetchProjects, 30000);
    return () => { mounted = false; clearInterval(statusInterval); clearInterval(projectInterval); };
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

  // Tap project → if dev server running, always use Hermes push (fast, ~10s).
  // Xcode native build is available via "Install Native" action in the sheet.
  const handleTapProject = useCallback(async (projectName: string) => {
    const isRunning = devStatus?.workDir?.endsWith(projectName);
    if (isRunning) {
      handleOpenNative(devStatus!.workDir!);
      return;
    }

    setLoadingActions(true);
    try {
      const result = await quicClient.getProjectActions(projectName);
      // Always prepend "Vibing" + orchestration surfaces as the first options.
      const vibingAction = { label: "Vibing", target: ".", type: "vibing", icon: "\u{1F3B5}", framework: "", platform: "", command: "" };
      const agentAction = { label: "Agent Mode", target: ".", type: "agent", icon: "\u{1F9E0}", framework: "", platform: "", command: "" };
      const autoDevAction = { label: "Auto Dev", target: ".", type: "autodev", icon: "\u{1F916}", framework: "", platform: "", command: "" };
      const autoTestAction = { label: "Auto Test", target: ".", type: "autotest", icon: "\u{1F9EA}", framework: "", platform: "", command: "" };
      const gitSyncAction = { label: "Git Sync", target: ".", type: "git-sync", icon: "\u{1F504}", framework: "", platform: "", command: "" };
      result.actions = [vibingAction, agentAction, autoDevAction, autoTestAction, gitSyncAction, ...result.actions];
      setActionSheet(result);
    } catch {
      // Fallback
      await quicClient.sendTask(`Run ${projectName} on my phone`, "").catch(() => {});
      router.navigate("/(tabs)/tasks");
    } finally {
      setLoadingActions(false);
    }
  }, [devStatus, router]);

  // Execute a specific action from the action sheet
  const handleExecuteAction = useCallback(async (action: { label: string; target: string; type: string; framework?: string; platform?: string; command?: string; supported?: boolean; reason?: string }) => {
    // Block unsupported actions
    if (action.supported === false) {
      Alert.alert("Not Supported Yet", action.reason || `${action.label} for ${action.framework} is coming soon.`);
      return;
    }

    const project = actionSheet?.project ?? "";
    const path = actionSheet?.path ?? "";
    setActionSheet(null);

    if (action.type === "git-sync") {
      await quicClient.sendTask(
        `Git Sync — ${project}`,
        `cd ${path} && Sync this repository with its remote. Pull the latest changes. If there are merge conflicts, resolve them intelligently. If the local branch is behind, rebase or merge as appropriate. If there are uncommitted local changes, stash them first, pull, then re-apply. Show me a summary of what changed.`,
      ).catch(() => {});
      router.navigate("/(tabs)/tasks");
      return;
    }

    if (action.type === "vibing") {
      // Open vibing mode — delay to let action sheet modal fully close first
      setTimeout(async () => {
        try {
          const state = await quicClient.getVibingState(project);
          if (state) {
            setVibingState(state);
          } else {
            Alert.alert("No data", "Vibing returned empty state");
          }
        } catch (e) {
          Alert.alert("Failed", String(e));
        }
      }, 400);
      return;
    }

    if (action.type === "agent") {
      router.navigate({ pathname: "/(tabs)/agent", params: { project, path } } as any);
      return;
    }

    if (action.type === "autodev") {
      // Jump to the Auto Dev screen (manages develop-mode loops). The
      // screen shows all loops across projects; the user picks or starts
      // one targeted at this path from there.
      router.navigate({ pathname: "/(tabs)/autodev", params: { project, path } } as any);
      return;
    }

    if (action.type === "autotest") {
      // Jump to Local CI / runs screen (yaver-test-sdk), which is where
      // auto-test loops live. Same pattern as Auto Dev.
      router.navigate({ pathname: "/(tabs)/runs", params: { project, path } } as any);
      return;
    }

    if (action.type === "dev-server") {
      // Direct dev server start — use the exact target path (handles monorepos like talos/mobile)
      setStartingProject(project);
      try {
        const targetPath = action.target === "." ? path : `${path}/${action.target}`.replace(/\/+$/, "");
        await quicClient.startDevServer({
          framework: action.framework || "",
          workDir: targetPath,
          platform: action.platform || "",
          targetDeviceId: selectedTarget?.id,
          targetDeviceName: selectedTarget?.name,
          targetDeviceClass: selectedTarget?.deviceClass,
        });
        // Check if it started
        const status = await quicClient.getDevServerStatus();
        if (!status?.running) {
          await quicClient.sendTask(
            `Hot reload ${project} (${action.framework}) on my phone`,
            `Call POST /dev/start with workDir=${targetPath} to start Metro. DO NOT run 'expo run:ios', 'expo run:android', 'xcodebuild', 'gradlew', or any native build — the mobile app loads the JS bundle via Hermes push (/dev/build-native). Only Metro is needed.`,
          );
          router.navigate("/(tabs)/tasks");
        }
      } catch {
        const targetPath = action.target === "." ? path : `${path}/${action.target}`.replace(/\/+$/, "");
        await quicClient.sendTask(
          `Hot reload ${project} on my phone`,
          `Call POST /dev/start with workDir=${targetPath}. Metro only — no expo run:ios, no xcodebuild. Mobile loads via Hermes push.`,
        ).catch(() => {});
        router.navigate("/(tabs)/tasks");
      } finally {
        setStartingProject(null);
      }
    } else if (action.command) {
      // Direct command
      await quicClient.sendTask(
        `${action.label} — ${project}`,
        `cd ${path}/${action.target} && ${action.command}`,
      ).catch(() => {});
      router.navigate("/(tabs)/tasks");
    } else {
      // AI handles it
      await quicClient.sendTask(
        `${action.label} for ${project}`,
        `Project: ${path}/${action.target}. Platform: ${action.platform || action.framework || "auto"}. Do it.`,
      ).catch(() => {});
      router.navigate("/(tabs)/tasks");
    }
  }, [actionSheet, router, selectedTarget]);

  const [nativeLoading, setNativeLoading] = useState(false);
  const [loadingStatus, setLoadingStatus] = useState("");
  const [buildProgress, setBuildProgress] = useState(0);

  // Direct device install state
  const [buildStatus, setBuildStatus] = useState<string | null>(null);
  const [quickActionStatus, setQuickActionStatus] = useState<string | null>(null);

  useEffect(() => {
    AsyncStorage.getItem(PREVIEW_TARGET_KEY)
      .then((value) => {
        if (value) setSelectedTargetId(value);
      })
      .catch(() => {});
  }, [selectedTarget]);

  useEffect(() => {
    if (devStatus?.targetDeviceId) {
      setSelectedTargetId(devStatus.targetDeviceId);
    }
  }, [devStatus?.targetDeviceId]);

  // Direct device install: build with Xcode and install on device via xcrun devicectl
  const handleDirectBuild = useCallback(async () => {
    if (!devStatus?.workDir) return;
    setBuildStatus("queued");
    try {
      const build = await quicClient.startBuild("xcode-device-install", devStatus.workDir, true);
      setBuildStatus("running");

      // Poll build status every 3s
      const poll = setInterval(async () => {
        try {
          const baseUrl = (quicClient as any).baseUrl;
          const headers = (quicClient as any).authHeaders;
          const res = await fetch(`${baseUrl}/builds/${build.id}`, { headers });
          if (!res.ok) return;
          const b = await res.json();

          if (b.installStatus === "installed") {
            setBuildStatus("installed");
            clearInterval(poll);
            setTimeout(() => setBuildStatus(null), 5000);
          } else if (b.installStatus === "install_failed") {
            setBuildStatus("install_failed");
            clearInterval(poll);
            Alert.alert("Install Failed", b.installError || "Could not install on device");
            setTimeout(() => setBuildStatus(null), 5000);
          } else if (b.installStatus === "installing") {
            setBuildStatus("installing");
          } else if (b.status === "failed") {
            setBuildStatus("failed");
            clearInterval(poll);
            Alert.alert("Build Failed", b.error || "xcodebuild failed");
            setTimeout(() => setBuildStatus(null), 5000);
          }
          // else still running
        } catch {}
      }, 3000);
    } catch (e) {
      setBuildStatus("failed");
      Alert.alert("Build Error", String(e));
      setTimeout(() => setBuildStatus(null), 3000);
    }
  }, [devStatus?.workDir]);

  // Open app natively: Go agent builds Hermes bytecode → phone loads into RCTBridge
  const handleOpenNative = useCallback(async (workDir: string) => {
    const baseUrl = (quicClient as any).baseUrl;
    if (!baseUrl) {
      Alert.alert("Error", "Not connected to agent");
      return;
    }

    setNativeLoading(true);
    setBuildProgress(0);
    const headers = {
      ...(quicClient as any).authHeaders,
      "Content-Type": "application/json",
    };

    // Listen to SSE for real-time build progress from Go agent
    const sseController = new AbortController();
    const listenSSE = async () => {
      try {
        const res = await fetch(`${baseUrl}/dev/events`, {
          headers: (quicClient as any).authHeaders,
          signal: sseController.signal,
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
                if (event.type === "log" && event.message) {
                  const msg = event.message;
                  setLoadingStatus(msg);
                  // Map known build stages to progress percentage
                  if (msg.includes("Installing dependencies")) setBuildProgress(0.1);
                  else if (msg.includes("Bundling")) setBuildProgress(0.3);
                  else if (msg.includes("Compiling Hermes")) setBuildProgress(0.7);
                  else if (msg.includes("Bundle ready")) setBuildProgress(0.95);
                }
              } catch {}
            }
          }
        }
      } catch {}
    };
    listenSSE();

    try {
      // 1. Ensure dev server is running for this project
      const currentStatus = await quicClient.getDevServerStatus();
      if (!currentStatus?.running || !currentStatus.workDir?.includes(workDir.split("/").pop() || "")) {
        setLoadingStatus("Starting dev server...");
        setBuildProgress(0.05);
        await quicClient.startDevServer({
          framework: "expo",
          workDir: workDir,
          targetDeviceId: selectedTarget?.id,
          targetDeviceName: selectedTarget?.name,
          targetDeviceClass: selectedTarget?.deviceClass,
        });
        // Wait for ready
        for (let i = 0; i < 30; i++) {
          await new Promise(r => setTimeout(r, 1000));
          const s = await quicClient.getDevServerStatus();
          setLoadingStatus(s?.running ? "Dev server ready" : "Starting dev server...");
          if (s?.running) break;
        }
      }

      // 2. Build native Hermes bytecode bundle via Go agent
      setLoadingStatus("Building native bundle...");
      setBuildProgress(0.15);
      const buildRes = await fetch(`${baseUrl}/dev/build-native`, {
        method: "POST",
        headers,
        body: JSON.stringify({ platform: Platform.OS }),
      });
      const buildResult = await buildRes.json();

      if (buildResult.status !== "ok") {
        throw new Error(buildResult.error || "Build failed");
      }

      // 3. Load compiled bytecode into native RCTBridge (NOT WebView)
      const sizeKB = Math.round((buildResult.size || 0) / 1024);
      setLoadingStatus(`Downloading ${sizeKB}KB bundle...`);
      setBuildProgress(0.95);
      const bundleUrl = `${baseUrl}${buildResult.bundleUrl}`;
      const moduleName = buildResult.moduleName || "main";
      await loadApp(bundleUrl, moduleName, (quicClient as any).authHeaders);
      setBuildProgress(1);
      setLoadingStatus("Loaded!");
    } catch (err: any) {
      Alert.alert("Load Failed", err?.message || "Could not load app in Yaver");
    } finally {
      sseController.abort();
      setNativeLoading(false);
      setBuildProgress(0);
      setTimeout(() => setLoadingStatus(""), 2000);
    }
  }, []);

  const handleOpen = useCallback(() => {
    if (!devStatus?.workDir) return;
    // Always Hermes push — fast (~10s), works on LAN and relay equally.
    // Xcode native device install is available as a separate "Install Native" action.
    handleOpenNative(devStatus.workDir);
  }, [devStatus, handleOpenNative]);

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
            Connect to a device to see your projects
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
                <Text style={[s.cardMeta, { color: "#7dd3fc" }]}>
                  target · {devStatus.targetDeviceName || selectedTarget?.name || "this device"}
                </Text>
              </View>
            </View>
            <View style={s.cardActions}>
              <Pressable style={[s.actionBtn, s.openBtn]} onPress={handleOpen} disabled={nativeLoading}>
                {nativeLoading ? (
                  <>
                    <ActivityIndicator size="small" color="#000" />
                    <Text style={[s.openBtnText, { fontSize: 11, marginLeft: 6 }]}>Building...</Text>
                  </>
                ) : (
                  <Text style={s.openBtnText}>Open App</Text>
                )}
              </Pressable>
              <Pressable style={[s.actionBtn, s.reloadBtn]} onPress={handleReload}>
                <Text style={s.reloadBtnText}>{"\u21BB"} Reload</Text>
              </Pressable>
              <Pressable style={[s.actionBtn, s.stopBtn]} onPress={handleStop}>
                <Text style={s.stopBtnText}>Stop</Text>
              </Pressable>
            </View>

            {/* Build progress bar — shows during HBC compilation */}
            {nativeLoading && (
              <View style={s.progressContainer}>
                <View style={s.progressTrack}>
                  <View style={[s.progressFill, { width: `${Math.max(buildProgress * 100, 5)}%` }]} />
                </View>
                {loadingStatus ? (
                  <Text style={s.progressText} numberOfLines={1}>{loadingStatus}</Text>
                ) : null}
              </View>
            )}

            {/* Build status — shows during direct device install */}
            {buildStatus && (
              <View style={s.progressContainer}>
                <View style={s.progressTrack}>
                  <View style={[s.progressFill, { width: buildStatus === "installed" ? "100%" : buildStatus === "installing" ? "90%" : "50%" }]} />
                </View>
                <Text style={s.progressText} numberOfLines={1}>
                  {buildStatus === "running" ? "Building with Xcode..." :
                   buildStatus === "installing" ? "Installing on device..." :
                   buildStatus === "installed" ? "Installed! App is ready." :
                   buildStatus === "install_failed" ? "Install failed" :
                   buildStatus === "failed" ? "Build failed" :
                   buildStatus === "queued" ? "Starting build..." : buildStatus}
                </Text>
              </View>
            )}

            {/* Quick actions */}
            <View style={s.quickActions}>
              {[
                { icon: "\u{1F680}", label: "Ship It", prompt: `Ship ${runningProject}: bump version, build iOS + Android, upload to TestFlight and Google Play, generate changelog from recent git commits. Report progress.` },
                { icon: "\u{1F3A8}", label: "Polish UI", prompt: `Do a design pass on ${runningProject}: fix inconsistent spacing, typography, colors. Make it look polished and professional. Don't redesign — just polish what's there. Hot reload when done.` },
                { icon: "\u{1F4F1}", label: "Screenshots", prompt: `Generate App Store and Google Play screenshots for ${runningProject}: capture all key screens at iPhone 6.7", iPhone 6.1", iPad 12.9", and Android phone/tablet sizes. Save to a screenshots/ folder.` },
                { icon: "\u{1F41B}", label: "Fix All Bugs", prompt: `Run the test suite for ${runningProject}, find all failing tests and runtime errors, fix them all. Hot reload after each fix so I can verify on my phone.` },
              ].map((action) => (
                <Pressable
                  key={action.label}
                  style={s.quickBtn}
                  onPress={() => {
                    if (action.label === "Ship It" && Platform.OS === "ios" && quicClient.connectionMode === "direct") {
                      // Direct device install — build with Xcode and install on device
                      handleDirectBuild();
                    } else {
                      // Send as task but stay on this page
                      quicClient.sendTask(action.prompt, `[Quick Action] ${action.label} for ${runningProject}`).catch(() => {});
                      setQuickActionStatus(`${action.label} task sent`);
                      setTimeout(() => setQuickActionStatus(null), 3000);
                    }
                  }}
                >
                  <Text style={s.quickIcon}>{action.icon}</Text>
                  <Text style={s.quickLabel}>{action.label}</Text>
                </Pressable>
              ))}
            </View>
            {quickActionStatus && (
              <Text style={{ color: "#22c55e", fontSize: 11, textAlign: "center", marginTop: 4 }}>{quickActionStatus}</Text>
            )}
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

        {/* Category + framework filter chips */}
        {(() => {
          const categories = new Map<string, number>();
          projects.forEach((p) => {
            const cat = getProjectCategory(p.framework);
            categories.set(cat, (categories.get(cat) || 0) + 1);
          });
          const categoryOrder = ["mobile", "web", "other"] as const;
          const categoryLabels: Record<string, string> = { mobile: "Mobile", web: "Web", other: "Other" };
          const visibleCategories = categoryOrder.filter((c) => categories.has(c));
          if (visibleCategories.length <= 1 && projects.length > 0) return null;
          return (
            <ScrollView horizontal showsHorizontalScrollIndicator={false} style={s.filterRow} contentContainerStyle={s.filterRowContent}>
              <Pressable
                style={[s.filterChip, !activeFilter && s.filterChipActive]}
                onPress={() => setActiveFilter(null)}
              >
                <Text style={[s.filterChipText, !activeFilter && s.filterChipTextActive]}>All ({projects.length})</Text>
              </Pressable>
              {visibleCategories.map((cat) => (
                <Pressable
                  key={cat}
                  style={[s.filterChip, activeFilter === cat && s.filterChipActive]}
                  onPress={() => setActiveFilter(activeFilter === cat ? null : cat)}
                >
                  <Text style={[s.filterChipText, activeFilter === cat && s.filterChipTextActive]}>
                    {categoryLabels[cat]} ({categories.get(cat)})
                  </Text>
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
            // Category filter
            if (activeFilter) {
              return getProjectCategory(p.framework) === activeFilter;
            }
            return true;
          })}
          keyExtractor={(item) => item.path}
          contentContainerStyle={s.listContent}
          renderItem={({ item }) => {
            const isRunning = devStatus?.workDir === item.path;
            const isStarting = startingProject === item.name;
            const category = getProjectCategory(item.framework);
            const fwIcon = FRAMEWORK_ICONS[item.framework || ""] || (category === "mobile" ? "\uD83D\uDCF1" : category === "web" ? "\u{1F310}" : "\u{1F4C2}");

            return (
              <Pressable
                style={[s.card, s.projectCard, { backgroundColor: c.bgCard, borderColor: c.border },
                  isRunning && { borderColor: "#22c55e44" }]}
                onPress={() => handleTapProject(item.name)}
                disabled={isStarting || loadingActions}
              >
                <View style={s.cardHeader}>
                  <Text style={s.frameworkIcon}>{fwIcon}</Text>
                  <View style={s.cardTitleContainer}>
                    <Text style={[s.projectName, { color: c.textPrimary }]}>{item.name}</Text>
                    {item.framework && (
                      <View style={s.tagRow}>
                        <View style={s.tag}>
                          <Text style={s.tagText}>{item.framework}</Text>
                        </View>
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
                    <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600" }}>{"\u25B6"}</Text>
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

      {/* Action sheet — shows available actions for a project */}
      <Modal visible={!!actionSheet} animationType="slide" transparent>
        <Pressable style={s.actionSheetOverlay} onPress={() => setActionSheet(null)}>
          <Pressable style={[s.actionSheetContainer, { backgroundColor: c.bgCard }]} onPress={(e) => e.stopPropagation()}>
            <View style={s.actionSheetHandle} />
            <Text style={[s.actionSheetTitle, { color: c.textPrimary }]}>
              {actionSheet?.project}
            </Text>
            <Text style={[s.actionSheetSubtitle, { color: c.textMuted }]}>
              What do you want to do?
            </Text>
            <ScrollView style={s.actionSheetScroll}>
              {actionSheet?.actions.map((action, i) => {
                const disabled = action.supported === false;
                return (
                  <Pressable
                    key={`${action.label}-${i}`}
                    style={[s.actionSheetItem, { borderColor: c.border }, disabled && { opacity: 0.4 }]}
                    onPress={() => handleExecuteAction(action)}
                  >
                    <Text style={s.actionSheetIcon}>{action.icon || "\u25B6"}</Text>
                    <View style={{ flex: 1 }}>
                      <Text style={[s.actionSheetLabel, { color: disabled ? c.textMuted : c.textPrimary }]}>
                        {action.label}{disabled ? " (coming soon)" : ""}
                      </Text>
                      <Text style={[s.actionSheetMeta, { color: c.textMuted }]}>
                        {disabled && action.reason ? action.reason : `${action.target}${action.framework ? ` · ${action.framework}` : ""}${action.platform ? ` → ${action.platform}` : ""}`}
                      </Text>
                    </View>
                  </Pressable>
                );
              })}
            </ScrollView>
          </Pressable>
        </Pressable>
      </Modal>

      {/* Vibing modal — AI pair programming widget */}
      <Modal visible={!!vibingState} animationType="slide">
        <View style={[s.safe, { backgroundColor: c.bg }]}>
          <View style={[s.vibingHeader, { borderBottomColor: c.border, paddingTop: insets.top + 8 }]}>
            <Pressable onPress={() => { setVibingState(null); setCustomTask(""); setVibingTaskStatus(""); setVibingTaskId(null); }} style={{ paddingVertical: 8 }}>
              <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
            </Pressable>
            <View style={{ alignItems: "center" }}>
              <Text style={[s.vibingTitle, { color: c.textPrimary }]}>{"\u{1F3B5}"} Vibing</Text>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>{vibingState?.project}</Text>
            </View>
            <View style={{ width: 50 }} />
          </View>

          <ScrollView contentContainerStyle={s.vibingContent}>

            {/* Running task indicator */}
            {vibingTaskStatus ? (
              <View style={[s.vibingStatus, { backgroundColor: c.accent + "11", borderColor: c.accent + "33" }]}>
                <ActivityIndicator size="small" color={c.accent} style={{ marginTop: 2 }} />
                <Text
                  style={{ color: c.textSecondary, fontSize: 13, flex: 1, lineHeight: 18 }}
                  numberOfLines={3}
                >
                  {vibingTaskStatus}
                </Text>
                {vibingTaskId && (
                  <Pressable onPress={() => { setVibingState(null); router.navigate("/(tabs)/tasks"); }}>
                    <Text style={{ color: c.accent, fontSize: 11, fontWeight: "600" }}>Details {"\u203A"}</Text>
                  </Pressable>
                )}
              </View>
            ) : null}

            {/* ── Deep Shuffle ── */}
            <Pressable
              style={[s.vibingDiceBtn, deepShuffleActive && { backgroundColor: "#1a1a2e", borderColor: c.accent + "44", borderWidth: 1 }]}
              disabled={deepShuffleActive}
              onPress={async () => {
                if (!vibingState) return;
                setDeepShuffleActive(true);
                setDeepShuffleText("Analyzing project...");
                setDeepShuffleStep("1/5");

                try {
                  // Start Deep Shuffle as a task — poll for output (SSE broken in RN)
                  const baseUrl = (quicClient as any).baseUrl;
                  const headers = { ...(quicClient as any).authHeaders, "Content-Type": "application/json" };

                  const res = await fetch(`${baseUrl}/vibing/surprise`, {
                    method: "POST",
                    headers,
                    body: JSON.stringify({ projectPath: vibingState.path }),
                  });

                  // The endpoint blocks until done (SSE), but we read the final response
                  // In the meantime, poll the vibing cache for intermediate results
                  const pollInterval = setInterval(async () => {
                    try {
                      const stateRes = await fetch(`${baseUrl}/vibing?path=${encodeURIComponent(vibingState.path)}`, { headers: (quicClient as any).authHeaders });
                      const stateData = await stateRes.json();
                      if (stateData?.suggestions?.length > 0) {
                        setVibingState((prev: any) => {
                          if (!prev) return prev;
                          return { ...prev, suggestions: stateData.suggestions };
                        });
                      }
                    } catch {}
                  }, 3000);

                  // Animate the status text while waiting
                  const steps = [
                    { step: "1/5", text: "Reading codebase and architecture..." },
                    { step: "2/5", text: "Brainstorming wild ideas..." },
                    { step: "3/5", text: "Finding practical magic..." },
                    { step: "4/5", text: "Dreaming up moonshots..." },
                    { step: "5/5", text: "Crafting final suggestions..." },
                  ];
                  let stepIdx = 0;
                  const stepInterval = setInterval(() => {
                    if (stepIdx < steps.length) {
                      setDeepShuffleStep(steps[stepIdx].step);
                      setDeepShuffleText(steps[stepIdx].text);
                      stepIdx++;
                    }
                  }, 15000); // advance step every 15s

                  // Wait for the response (blocks during analysis)
                  const text = await res.text();
                  clearInterval(pollInterval);
                  clearInterval(stepInterval);

                  // Final: refresh vibing state from cache (server updated it)
                  try {
                    const finalRes = await fetch(`${baseUrl}/vibing?path=${encodeURIComponent(vibingState.path)}`, { headers: (quicClient as any).authHeaders });
                    const finalData = await finalRes.json();
                    if (finalData?.suggestions?.length > 0) {
                      setVibingState((prev: any) => prev ? { ...prev, suggestions: finalData.suggestions } : prev);
                    }
                  } catch {}
                } catch {} finally {
                  setDeepShuffleActive(false);
                  setDeepShuffleText("");
                  setDeepShuffleStep("");
                }
              }}
            >
              <Text style={s.vibingDiceBtnIcon}>{deepShuffleActive ? "\u2728" : "\u{1F3B2}"}</Text>
              <Text style={s.vibingDiceBtnText}>{deepShuffleActive ? "Analyzing..." : "Deep Shuffle"}</Text>
            </Pressable>

            {/* ── Deep Shuffle streaming card ── */}
            {deepShuffleActive && (
              <View style={[s.deepShuffleCard, { backgroundColor: c.bgCard, borderColor: c.accent + "33" }]}>
                <View style={s.deepShuffleHeader}>
                  <ActivityIndicator size="small" color={c.accent} />
                  <Text style={[s.deepShuffleStepText, { color: c.accent }]}>{deepShuffleStep}</Text>
                </View>
                <Text style={[s.deepShuffleStreamText, { color: c.textSecondary }]} numberOfLines={4}>
                  {deepShuffleText}
                </Text>
              </View>
            )}

            {/* ── Deep Shuffle results ── */}
            {(vibingState?.suggestions ?? []).length > 0 && (
              <>
                {vibingState!.suggestions.map((sg: any) => (
                  <Pressable
                    key={sg.id}
                    style={[s.vibingFeatureCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
                    onPress={async () => {
                      try {
                        const result = await quicClient.executeVibingSuggestion(sg.prompt, vibingState!.path);
                        setVibingTaskId(result.taskId);
                        setVibingTaskStatus(`Running: ${sg.label}`);
                      } catch {}
                    }}
                    onLongPress={() => {
                      Alert.alert(
                        sg.icon + " " + sg.label,
                        sg.desc + (sg.reasoning ? `\n\n${sg.reasoning}` : ""),
                        [
                          { text: "Cancel", style: "cancel" },
                          { text: "Add to Todo", onPress: async () => {
                            try {
                              await quicClient.sendTask(sg.label, sg.prompt + (sg.reasoning ? `\n\nContext: ${sg.reasoning}` : ""));
                            } catch {}
                          }},
                          { text: "Delete", style: "destructive", onPress: () => {
                            setVibingState((prev: any) => {
                              if (!prev) return prev;
                              return { ...prev, suggestions: prev.suggestions.filter((s: any) => s.id !== sg.id) };
                            });
                          }},
                        ]
                      );
                    }}
                  >
                    <Text style={s.vibingFeatureIcon}>{sg.icon}</Text>
                    <View style={{ flex: 1 }}>
                      <Text style={[s.vibingFeatureLabel, { color: c.textPrimary }]}>{sg.label}</Text>
                      <Text style={[s.vibingFeatureDesc, { color: c.textMuted }]} numberOfLines={2}>{sg.desc}</Text>
                    </View>
                    <View style={[s.vibingCategoryChip, {
                      backgroundColor: sg.category === "bugfix" ? "#ef444422" : sg.category === "feature" ? "#6366f122" : "#22c55e22"
                    }]}>
                      <Text style={[s.vibingCategoryText, {
                        color: sg.category === "bugfix" ? "#ef4444" : sg.category === "feature" ? "#818cf8" : "#22c55e"
                      }]}>{sg.category}</Text>
                    </View>
                  </Pressable>
                ))}
              </>
            )}

            {/* ── Grid: Dev actions (2 columns) ── */}
            <Text style={[s.vibingSectionTitle, { color: c.textMuted, marginTop: 12 }]}>Dev Actions</Text>
            <View style={s.vibingGrid}>
              {(vibingState?.quickActions ?? []).filter(qa => qa.id !== "custom").map((qa) => (
                <Pressable
                  key={qa.id}
                  style={[s.vibingGridItem, { backgroundColor: c.bgCard, borderColor: c.border }]}
                  onPress={async () => {
                    try {
                      const result = await quicClient.executeVibingSuggestion(qa.prompt, vibingState!.path);
                      setVibingTaskId(result.taskId);
                      setVibingTaskStatus(`Running: ${qa.label}`);
                    } catch {}
                  }}
                >
                  <Text style={s.vibingGridIcon}>{qa.icon}</Text>
                  <Text style={[s.vibingGridLabel, { color: c.textPrimary }]}>{qa.label}</Text>
                </Pressable>
              ))}
            </View>

            {/* ── Custom input ── */}
            <Text style={[s.vibingSectionTitle, { color: c.textMuted, marginTop: 16 }]}>Chat</Text>
            <View style={[s.vibingCustomRow, { borderColor: c.border }]}>
              <TextInput
                style={[s.vibingCustomInput, { color: c.textPrimary }]}
                placeholder="What should we work on?"
                placeholderTextColor={c.textMuted}
                value={customTask}
                onChangeText={setCustomTask}
                multiline
              />
              <Pressable
                style={[s.vibingCustomSend, { backgroundColor: c.accent }, !customTask.trim() && { opacity: 0.3 }]}
                disabled={!customTask.trim()}
                onPress={async () => {
                  if (!customTask.trim() || !vibingState) return;
                  try {
                    const result = await quicClient.executeVibingSuggestion(customTask, vibingState.path);
                    setVibingTaskId(result.taskId);
                    setVibingTaskStatus(`Running: ${customTask.slice(0, 40)}`);
                    setCustomTask("");
                  } catch {}
                }}
              >
                <Text style={{ color: "#fff", fontWeight: "700", fontSize: 13 }}>Go</Text>
              </Pressable>
            </View>

            {/* ── Recent history ── */}
            {(vibingState?.history ?? []).length > 0 && (
              <>
                <Text style={[s.vibingSectionTitle, { color: c.textMuted, marginTop: 16 }]}>Recent</Text>
                {vibingState!.history.slice(0, 5).map((h, i) => (
                  <Text key={i} style={[s.vibingHistoryItem, { color: c.textMuted }]} numberOfLines={1}>
                    {"\u2022"} {h}
                  </Text>
                ))}
              </>
            )}
          </ScrollView>
        </View>
      </Modal>

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
  filterRow: { marginHorizontal: 16, marginBottom: 8, height: 30, flexGrow: 0 },
  filterRowContent: { gap: 4, alignItems: "center" as const, paddingRight: 16 },
  filterChip: {
    height: 26,
    paddingHorizontal: 8,
    borderRadius: 6,
    backgroundColor: "#111",
    borderWidth: 1,
    borderColor: "#222",
    justifyContent: "center" as const,
  },
  filterChipActive: { backgroundColor: "#6366f122", borderColor: "#6366f1" },
  filterChipText: { fontSize: 10, fontWeight: "600", color: "#888" },
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

  // Quick actions
  quickActions: { flexDirection: "row", gap: 6, marginTop: 10 },
  quickBtn: {
    flex: 1,
    backgroundColor: "#111",
    borderRadius: 8,
    paddingVertical: 10,
    alignItems: "center",
    borderWidth: 1,
    borderColor: "#1a1a1a",
  },
  quickIcon: { fontSize: 18, marginBottom: 2 },
  quickLabel: { fontSize: 9, color: "#999", fontWeight: "600" },

  // Action sheet
  actionSheetOverlay: { flex: 1, backgroundColor: "rgba(0,0,0,0.5)", justifyContent: "flex-end" },
  actionSheetContainer: { borderTopLeftRadius: 20, borderTopRightRadius: 20, padding: 20, paddingBottom: 40, maxHeight: "70%" },
  actionSheetHandle: { width: 36, height: 4, backgroundColor: "#333", borderRadius: 2, alignSelf: "center", marginBottom: 16 },
  actionSheetTitle: { fontSize: 20, fontWeight: "700", marginBottom: 2 },
  actionSheetSubtitle: { fontSize: 13, marginBottom: 16 },
  actionSheetScroll: {},
  actionSheetItem: { flexDirection: "row", alignItems: "center", paddingVertical: 14, borderBottomWidth: 1, gap: 12 },
  actionSheetIcon: { fontSize: 22 },
  actionSheetLabel: { fontSize: 15, fontWeight: "600" },
  actionSheetMeta: { fontSize: 11, marginTop: 1 },

  // Vibing
  vibingHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingTop: 12, paddingBottom: 10, borderBottomWidth: 1 },
  vibingTitle: { fontSize: 17, fontWeight: "700" },
  vibingContent: { padding: 16, paddingBottom: 40 },
  vibingSectionTitle: { fontSize: 11, fontWeight: "600", textTransform: "uppercase" as const, letterSpacing: 1, marginBottom: 8 },
  vibingFeatureCard: { flexDirection: "row", alignItems: "center", borderRadius: 12, borderWidth: 1, padding: 14, marginBottom: 8, gap: 12 },
  vibingFeatureIcon: { fontSize: 24 },
  vibingFeatureLabel: { fontSize: 15, fontWeight: "700" },
  vibingFeatureDesc: { fontSize: 11, marginTop: 2, lineHeight: 16 },
  vibingGrid: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  vibingGridItem: { width: "48%", borderRadius: 10, borderWidth: 1, padding: 14, alignItems: "center", gap: 6 },
  vibingGridIcon: { fontSize: 22 },
  vibingGridLabel: { fontSize: 12, fontWeight: "600", textAlign: "center" as const },
  vibingCategoryChip: { borderRadius: 4, paddingHorizontal: 6, paddingVertical: 2 },
  vibingCategoryText: { fontSize: 9, fontWeight: "600" },
  vibingCustomRow: { flexDirection: "row", alignItems: "flex-end", borderWidth: 1, borderRadius: 10, marginTop: 16, padding: 8, gap: 8 },
  vibingCustomInput: { flex: 1, fontSize: 14, minHeight: 40, paddingVertical: 4 },
  vibingCustomSend: { borderRadius: 8, paddingHorizontal: 16, paddingVertical: 10 },
  vibingHistoryItem: { fontSize: 12, paddingVertical: 4 },
  vibingDiceBtn: { alignSelf: "center", flexDirection: "row", alignItems: "center", gap: 6, backgroundColor: "#1a1a2e", borderRadius: 20, paddingHorizontal: 20, paddingVertical: 10, marginBottom: 12, marginTop: 4 },
  vibingDiceBtnIcon: { fontSize: 18 },
  vibingDiceBtnText: { color: "#818cf8", fontSize: 13, fontWeight: "700" },
  vibingStatus: { flexDirection: "row", alignItems: "center", borderWidth: 1, borderRadius: 10, padding: 10, marginBottom: 12, gap: 8 },
  deepShuffleCard: { borderWidth: 1, borderRadius: 12, padding: 14, marginBottom: 12 },
  deepShuffleHeader: { flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 8 },
  deepShuffleStepText: { fontSize: 11, fontWeight: "700", letterSpacing: 0.5 },
  deepShuffleStreamText: { fontSize: 13, lineHeight: 19 },

  // Build progress
  progressContainer: { marginTop: 10 },
  progressTrack: {
    height: 4,
    backgroundColor: "#22c55e22",
    borderRadius: 2,
    overflow: "hidden" as const,
  },
  progressFill: {
    height: 4,
    backgroundColor: "#22c55e",
    borderRadius: 2,
  },
  progressText: {
    fontSize: 11,
    color: "#9ca3af",
    marginTop: 4,
  },

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
  frameworkIcon: { fontSize: 20 },

  cardActions: { flexDirection: "row", gap: 8, marginTop: 12 },
  actionBtn: { paddingVertical: 8, paddingHorizontal: 14, borderRadius: 8 },
  openBtn: { backgroundColor: "#22c55e", flex: 1, alignItems: "center", flexDirection: "row" as const, justifyContent: "center", gap: 4 },
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
  webViewHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingTop: 12, paddingBottom: 10, borderBottomWidth: 1 },
  webViewHeaderCenter: { flexDirection: "row", alignItems: "center", gap: 6 },
  webViewTitle: { fontSize: 15, fontWeight: "700" },
  webViewHeaderActions: { flexDirection: "row", alignItems: "center" },
  loadingBar: { height: 2, opacity: 0.6 },
});
