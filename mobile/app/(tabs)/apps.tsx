import React, { useCallback, useEffect, useRef, useState } from "react";
import { Ionicons } from "@expo/vector-icons";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  Modal,
  NativeModules,
  Pressable,
  RefreshControl,
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
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { FrameworkIcon } from "../../src/components/FrameworkIcon";
import { useDevice } from "../../src/context/DeviceContext";
import RemoteBoxBanner from "../../src/components/RemoteBoxBanner";
import EmptyState from "../../src/components/EmptyState";
import NoMachineEmpty from "../../src/components/NoMachineEmpty";
import { isEffectivelyConnected as computeEffectiveConnected } from "../../src/lib/connectionState";
import { useColors, useTheme } from "../../src/context/ThemeContext";
import { quicClient, type CapabilitySnapshot, type DevCompatibilityStatus, type DevServerStatus, type MobileWorkerPreviewSession } from "../../src/lib/quic";
import { getAvailableModules, isBundleLoaderAvailable, loadApp } from "../../src/lib/bundleLoader";
import { openAppBus } from "../../src/lib/openAppBus";
import { downloadArtifact } from "../../src/lib/builds";
import { describeConnectionStatus } from "../../src/lib/connection";
import { buildNativeBuildRequest, nativeBuildFailureMessage, nativeBuildFailureTitle } from "../../src/lib/nativeBuild";
import { isActiveDevServerStatus } from "../../src/lib/devServerState";
import { isWebServedStatus } from "../../src/lib/devLane";
import { applyPreviewCapabilities, guardYaverSelfDevelopmentActions, isHermesMobileFramework } from "../../src/lib/mobileProjectActions";
import { runtimeSurfaceClient } from "../../src/lib/runtimeSurfaceClient";
import { lightCardShadow, spacing, typography } from "../../src/theme/tokens";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";

// ── Types ──────────────────────────────────────────────────────────

interface ProjectItem {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  executionMode?: string;
  primarySurface?: string;
  tags?: string[];
}

// Repo-level entry — monorepo root or standalone repo. Surfaced in the
// "Repos" row above the per-framework apps list so vibe-coding can be
// scoped to the whole repo (Go agent + web + mobile + cli) instead of
// a single mobile/ subdir of a monorepo.
interface RepoItem {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  gitRemote?: string;
  tags?: string[];
  isMonorepo?: boolean;
  subframeworks?: string[];
}

// Branded vector icons via mobile/src/components/FrameworkIcon.tsx \u2014 see
// that file for the per-framework MaterialCommunityIcon + brand-color
// mapping. Kept in sync with hotreload.tsx so the two surfaces render
// identical icons for the same framework.

const MOBILE_FRAMEWORKS = ["expo", "react-native", "flutter"];
const SECOND_CLASS_MOBILE_FRAMEWORKS = ["flutter", "swift", "kotlin"];
const WEB_FRAMEWORKS = ["nextjs", "vite", "react"];
const PREVIEW_TARGET_KEY = "@yaver/hotreload_preview_target";

function pathLeaf(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() || path;
}

function findProjectMatch(projects: ProjectItem[], query: string): ProjectItem | null {
  const target = query.trim().toLowerCase();
  if (!target) return null;
  const exact = projects.find((p) =>
    p.name.trim().toLowerCase() === target ||
    pathLeaf(p.path).trim().toLowerCase() === target,
  );
  if (exact) return exact;
  return projects.find((p) =>
    p.name.toLowerCase().includes(target) ||
    pathLeaf(p.path).toLowerCase().includes(target) ||
    p.path.toLowerCase().includes(target),
  ) || null;
}

function isSecondClassMobileFramework(framework?: string): boolean {
  return framework === "flutter" || framework === "swift" || framework === "kotlin";
}

// "carrotbet / mobile" → ["carrotbet", "mobile"]. The trailing "/ <subdir>"
// reads as a clumsy path fragment in a title; we split it so the subdir can
// render as a chip next to the framework tag. No " / " → [name, ""].
function splitProjectName(name?: string): [string, string] {
  const n = (name || "").trim();
  const idx = n.lastIndexOf(" / ");
  if (idx < 0) return [n, ""];
  return [n.slice(0, idx).trim(), n.slice(idx + 3).trim()];
}

function agentFlowGuidance(_framework?: string, _feedbackSDKInstalled?: boolean): string | null {
  // Yaver's native overlay (shake → reload / back) already handles the
  // in-app feedback loop, so no extra banner text is needed.
  return null;
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

function secondClassGuidance(_framework?: string, _isDirectConnection?: boolean): string | null {
  // No LAN-only / Hermes-only guidance. None of the reload lanes are LAN-only —
  // the browser lane works over relay (proven by the relay-auth fix), and Hermes
  // is a React Native concept that never applied to Flutter/Swift/Kotlin. The
  // sheet now shows only the reload lanes, which are self-explanatory.
  return null;
}

function describeRuntimeDeployResult(result: any): string {
  const pushes = Array.isArray(result?.runtimeDeploy?.pushes) ? result.runtimeDeploy.pushes.length : 0;
  const runtime = result?.runtimeDeploy?.runtime;
  const switches = Array.isArray(runtime?.phoneSwitches) ? runtime.phoneSwitches.length : 0;
  if (pushes > 0 || switches > 0) {
    return `Runtime deploy finished: ${switches} promotion${switches === 1 ? "" : "s"}, ${pushes} push${pushes === 1 ? "" : "es"}.`;
  }
  return result?.message || "Runtime deploy finished.";
}

function secondClassFlushLabel(framework?: string): string {
  return framework === "flutter" ? "Flush to App (LAN)" : "Flush Build to Phone (LAN)";
}

type StoreDeploy = {
  label: string;
  target: "testflight" | "playstore";
  prompt: (project: string, workDir: string) => string;
};

function storeDeployDescriptor(framework: string): StoreDeploy | null {
  switch (framework) {
    case "flutter":
      return Platform.OS === "android"
        ? {
            label: "Ship to Play Store (internal)",
            target: "playstore",
            prompt: (project, workDir) => `Build ${project} (Flutter) for Android as a release AAB at ${workDir} and upload to Google Play internal testing. Auto-increment versionCode. Report progress.`,
          }
        : {
            label: "Ship to TestFlight",
            target: "testflight",
            prompt: (project, workDir) => `Build ${project} (Flutter) for iOS at ${workDir}, archive, and upload to TestFlight. Auto-increment build number. Report progress.`,
          };
    case "swift":
      return {
        label: "Ship to TestFlight",
        target: "testflight",
        prompt: (project, workDir) => `Build ${project} (native Swift/iOS) at ${workDir}, archive with Xcode, and upload to TestFlight. Auto-increment CFBundleVersion. Report progress.`,
      };
    case "kotlin":
      return {
        label: "Ship to Play Store (internal)",
        target: "playstore",
        prompt: (project, workDir) => `Build ${project} (native Kotlin/Android) at ${workDir} as a release AAB and upload to Google Play internal testing. Auto-increment versionCode. Report progress.`,
      };
    default:
      return null;
  }
}

// Check whether the currently-connected dev machine can actually produce the
// requested build. TestFlight archives require macOS + Xcode; without macOS the
// task will silently fail after minutes. Better to refuse up front.
function devMachineDeployBlocker(target: "testflight" | "playstore", machineOs?: string): string | null {
  const os = (machineOs || "").toLowerCase();
  if (target === "testflight") {
    if (!os) return "This dev machine hasn't reported its OS yet. TestFlight archives need macOS + Xcode.";
    if (!os.startsWith("darwin") && !os.includes("mac")) {
      return `TestFlight archives need macOS + Xcode, but this dev machine is ${machineOs}. Switch to a Mac dev machine or run a CI job instead.`;
    }
  }
  if (target === "playstore") {
    if (!os) return "This dev machine hasn't reported its OS yet. Play Store AABs need Java 17 + the Android SDK.";
    // Any desktop OS can build Android, but warn on something clearly non-desktop.
    if (os.startsWith("ios") || os.startsWith("android")) {
      return `Play Store AABs need a desktop dev machine with Java 17 + Android SDK, but this dev machine is ${machineOs}.`;
    }
  }
  return null;
}


function buildStateLabel(compatibility?: DevCompatibilityStatus | null): string | null {
  if (!compatibility?.buildState) return null;
  switch (compatibility.buildState) {
    case "ready":
      return compatibility.lastBuildAt ? `Hermes ready · ${formatBuildTimestamp(compatibility.lastBuildAt)}` : "Hermes ready";
    case "building":
      return "Hermes build running";
    case "build_failed":
      return compatibility.lastBuildFailedAt ? `Last build failed · ${formatBuildTimestamp(compatibility.lastBuildFailedAt)}` : "Last build failed";
    default:
      return "Source only · compile Hermes first";
  }
}

function buildStateTone(compatibility?: DevCompatibilityStatus | null): string {
  switch (compatibility?.buildState) {
    case "ready":
      return "#86efac";
    case "build_failed":
      return "#fca5a5";
    case "building":
      return "#7dd3fc";
    default:
      return "#fcd34d";
  }
}

function compileActionLabel(compatibility?: DevCompatibilityStatus | null): string {
  return compatibility?.buildState === "ready" ? "Rebuild Hermes" : "Compile Hermes";
}

function formatBuildTimestamp(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

// The exact command Yaver runs to serve the web target, per framework — shown
// in the preview's "starting" panel so the user sees what's happening.
function devServerStepsFor(framework?: string): string {
  const fw = (framework || "").toLowerCase();
  if (fw === "flutter") return "flutter run -d web-server";
  if (fw.includes("expo") || fw.includes("react-native")) return "expo start --web";
  if (fw.includes("next")) return "next dev";
  if (fw.includes("vite")) return "vite";
  return "starting the web dev server";
}

function getProjectCategory(framework?: string): "mobile" | "web" | "other" {
  if (!framework) return "other";
  if (MOBILE_FRAMEWORKS.includes(framework) || SECOND_CLASS_MOBILE_FRAMEWORKS.includes(framework)) return "mobile";
  if (WEB_FRAMEWORKS.includes(framework)) return "web";
  return "other";
}

// ── Projects Tab ──────────────────────────────────────────────────

export default function AppsScreen() {
  const c = useColors();
  const { isDark } = useTheme();
  const insets = useSafeAreaInsets();
  const layout = useResponsiveLayout();
  const tabletContent = useTabletContentStyle("wide");
  const { activeDevice, connectionStatus, devices, connectedDeviceIds, refreshDevices } = useDevice();
  const isConnected = connectionStatus === "connected" && !!activeDevice;
  // Effective state — focused box OR any pool client live. See
  // lib/connectionState; aligns this tab with Devices/Tasks/Reload so
  // we no longer disagree about "connected" when the focused box is
  // mid-retry but a peer is still up.
  const effectivelyConnected = computeEffectiveConnected(connectionStatus, connectedDeviceIds);
  const [selectedTargetId, setSelectedTargetId] = useState<string | null>(null);
  const mobileWorkers = devices.filter((d) => d.deviceClass === "edge-mobile");
  const selectedTarget = mobileWorkers.find((d) => d.id === selectedTargetId) || null;
  const isDirectConnection = quicClient.connectionMode === "direct";
  const router = useRouter();

  // Build + task status hoisted to the top of the component so the shared
  // helpers below (sendTaskOrWarn / offerAgentFix) can surface status from
  // any callback without forward-reference TDZ errors.
  const [nativeLoading, setNativeLoading] = useState(false);
  // Live tail of the latest bundler stdout line. Updated from /dev/events
  // SSE on every event.logLine push. Rendered below the progress bar so
  // the user can see Metro is actively chewing through modules and not
  // just hung. Cleared when the build finishes.
  const [bundlerLine, setBundlerLine] = useState("");
  const [loadingStatus, setLoadingStatus] = useState("");
  const [buildProgress, setBuildProgress] = useState(0);
  const [buildStatus, setBuildStatus] = useState<string | null>(null);
  const [quickActionStatus, setQuickActionStatus] = useState<string | null>(null);
  const [capabilitySnapshot, setCapabilitySnapshot] = useState<CapabilitySnapshot | null>(null);
  // New per-target live-probe map keyed by target id. Sourced from
  // /deploy/capabilities so we surface the real "wrong OS / missing
  // tools / missing secrets / file-not-found-at-path" reason instead
  // of the cached + stale CapabilitySnapshot booleans. Reasons can
  // include path-validity warnings the snapshot endpoint never had.
  const [liveDeployCaps, setLiveDeployCaps] = useState<
    Record<string, { canDeploy: boolean; reason?: string; platformLock?: string }>
  >({});

  useEffect(() => {
    if (!isConnected) {
      setCapabilitySnapshot(null);
      setLiveDeployCaps({});
      return;
    }
    quicClient.capabilitySnapshot()
      .then(setCapabilitySnapshot)
      .catch(() => setCapabilitySnapshot(null));
    // Fire-and-forget; older agents 404 and we just fall through to
    // the snapshot-based blocker below.
    quicClient.deployCapabilities()
      .then((report) => {
        const map: Record<string, { canDeploy: boolean; reason?: string; platformLock?: string }> = {};
        report.targets.forEach((t) => {
          map[t.target] = {
            canDeploy: t.canDeploy,
            reason: t.reason,
            platformLock: t.platformLock,
          };
        });
        setLiveDeployCaps(map);
      })
      .catch(() => setLiveDeployCaps({}));
  }, [isConnected, activeDevice?.id]);

  const deployBlocker = useCallback((target: "testflight" | "playstore", machineOs?: string): string | null => {
    // Live probe wins when it's available — it's freshly computed
    // from the host's tools+vault state. Fall back to the stale
    // snapshot, then to the static OS-based heuristic so older
    // agents (pre-/deploy/capabilities) still gate something.
    const live = liveDeployCaps[target];
    if (live && !live.canDeploy) {
      return live.reason || devMachineDeployBlocker(target, machineOs);
    }
    const readiness = capabilitySnapshot?.targets?.[target];
    if (readiness && readiness.enabled === false) {
      return readiness.reason || readiness.suggestedAction || devMachineDeployBlocker(target, machineOs);
    }
    return devMachineDeployBlocker(target, machineOs);
  }, [capabilitySnapshot, liveDeployCaps]);

  // sendTaskOrWarn replaces the old `.catch(() => {})` pattern on user-
  // initiated taps. Every call either succeeds and navigates the user to the
  // Tasks tab, or shows them a real error with connection context so they
  // can fix it — never silently.
  const sendTaskOrWarn = useCallback(async (
    title: string,
    description: string,
    labelForUser: string,
  ): Promise<boolean> => {
    try {
      await quicClient.sendTask(title, description);
      setQuickActionStatus(`${labelForUser} sent`);
      setTimeout(() => setQuickActionStatus(null), 3000);
      router.navigate("/(tabs)/tasks");
      return true;
    } catch (e) {
      const err = e instanceof Error ? e.message : String(e);
      Alert.alert(
        `Couldn't Send "${labelForUser}"`,
        `${err}\n\nYaver ${describeConnectionStatus(connectionStatus)}.`,
        [
          { text: "Close", style: "cancel" },
          {
            text: "Retry",
            onPress: () => {
              void quicClient.sendTask(title, description)
                .then(() => {
                  setQuickActionStatus(`${labelForUser} sent`);
                  setTimeout(() => setQuickActionStatus(null), 3000);
                  router.navigate("/(tabs)/tasks");
                })
                .catch((retryErr) => Alert.alert(
                  "Still Couldn't Send",
                  `${retryErr instanceof Error ? retryErr.message : String(retryErr)}\n\nYaver ${describeConnectionStatus(connectionStatus)}.`,
                ));
            },
          },
        ],
      );
      return false;
    }
  }, [router, connectionStatus]);

  // offerAgentFix shows a 2-button alert whose second action queues a
  // recovery task on the wrapped AI (Claude Code / Codex / Aider / …). The
  // prompt is crafted server-side by the Go agent — the mobile app only
  // ships a recovery kind + context. Keeps the "vibe coder" loop tight:
  // a failure becomes a fix task without the user composing a prompt.
  const offerAgentFix = useCallback((
    title: string,
    body: string,
    ctx: Parameters<typeof quicClient.recover>[0],
    actionLabel?: string,
  ) => {
    Alert.alert(title, body, [
      { text: "Close", style: "cancel" },
      {
        text: actionLabel || "Ask AI to Fix",
        onPress: async () => {
          try {
            const r = await quicClient.recover(ctx);
            setQuickActionStatus(`Fix task queued: ${r.title}`);
            setTimeout(() => setQuickActionStatus(null), 5000);
            router.navigate("/(tabs)/tasks");
          } catch (e) {
            Alert.alert(
              "Could not queue fix task",
              `${e instanceof Error ? e.message : String(e)}\n\nYaver ${describeConnectionStatus(connectionStatus)}.`,
            );
          }
        },
      },
    ]);
  }, [router, connectionStatus]);

  const [devStatus, setDevStatus] = useState<DevServerStatus | null>(null);
  const [workerSession, setWorkerSession] = useState<MobileWorkerPreviewSession | null>(null);
  const [projects, setProjects] = useState<ProjectItem[]>([]);
  const [repos, setRepos] = useState<RepoItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [pullRefreshing, setPullRefreshing] = useState(false);

  // Pull-to-refresh on the Projects list: re-scan projects on the box + re-poll
  // devices. The list updates from the existing poll loop; show the spinner
  // briefly while the fresh scan is kicked off.
  const onPullRefresh = useCallback(async () => {
    setPullRefreshing(true);
    try {
      await Promise.allSettled([
        quicClient.refreshMobileProjects(),
        refreshDevices(),
      ]);
    } finally {
      setPullRefreshing(false);
    }
  }, [refreshDevices]);
  const [startingProject, setStartingProject] = useState<string | null>(null);
  const [showWebView, setShowWebView] = useState(false);
  const [webViewKey, setWebViewKey] = useState(0);
  const [webViewLoading, setWebViewLoading] = useState(false);
  const [search, setSearch] = useState("");
  // Default to the mobile view: Yaver is overwhelmingly used for mobile app
  // development, and a repo tree usually holds far more non-mobile projects
  // than mobile ones — so an unfiltered list buries the thing the user came
  // for. "All" is one tap away.
  const [activeFilter, setActiveFilter] = useState<string | null>("mobile");
  const [actionSheet, setActionSheet] = useState<{
    project: string;
    path: string;
    actions: { label: string; target: string; type: string; framework?: string; platform?: string; command?: string; icon?: string; supported?: boolean; reason?: string }[];
    compatibility?: DevCompatibilityStatus | null;
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
  const [projectsDiscovering, setProjectsDiscovering] = useState(false);
  const webViewRef = useRef<WebView>(null);
  // Browser-preview cold-start retry budget (see the WebView onError/onHttpError
  // below). A web dev server can take up to a minute to compile on first open.
  const webPreviewRetryRef = useRef(0);
  const webPreviewErroredRef = useRef(false);
  const webPreviewRetryTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // True once the WebView paints REAL content (a flutter-view / non-empty DOM),
  // not just a 200 on index.html — keeps the progress overlay up so a Flutter
  // page that renders black while CanvasKit boots never shows as a blank void.
  const [webPreviewContentLoaded, setWebPreviewContentLoaded] = useState(false);
  const [webPreviewFailed, setWebPreviewFailed] = useState(false);
  const [webPreviewLogs, setWebPreviewLogs] = useState<string[]>([]);
  useEffect(() => () => { if (webPreviewRetryTimer.current) clearTimeout(webPreviewRetryTimer.current); }, []);
  const resetWebPreview = useCallback(() => {
    webPreviewRetryRef.current = 0;
    webPreviewErroredRef.current = false;
    setWebPreviewContentLoaded(false);
    setWebPreviewFailed(false);
    setWebPreviewLogs([]);
  }, []);
  const scheduleWebPreviewRetry = useCallback(() => {
    webPreviewErroredRef.current = true;
    setWebViewLoading(false);
    if (webPreviewRetryRef.current >= 30) {
      // Fall into the failure panel (logs + Retry), not an Alert that dismisses
      // the preview to nothing.
      setWebPreviewFailed(true);
      return;
    }
    webPreviewRetryRef.current += 1;
    if (webPreviewRetryTimer.current) clearTimeout(webPreviewRetryTimer.current);
    webPreviewRetryTimer.current = setTimeout(() => setWebViewKey((k) => k + 1), 2500);
  }, []);

  // Remote Box switch — clear stale per-box state immediately, then
  // kick a fresh scan ONCE the new device's QuicClient is actually
  // connected. The previous one-effect version captured
  // effectivelyConnected at switch-time — switching to a not-yet-
  // -connected box meant the kick fired against the OLD client (or
  // threw assertConnected and was swallowed), so the user saw a
  // permanent spinner with no scan ever happening on the new box.
  // Splitting into two effects + a ref tracker fixes both directions.
  const lastScanKickedDeviceIdRef = useRef<string | null>(null);
  useEffect(() => {
    if (!activeDevice?.id) return;
    setProjects([]);
    setProjectsDiscovering(false);
    setDevStatus(null);
    setWorkerSession(null);
    setRepos([]);
    setStartingProject(null);
    setActionSheet(null);
    setQuickActionStatus(null);
    setBuildStatus(null);
    setBundlerLine("");
    // Reset the kick tracker so the next isConnected→true on this
    // deviceId fires a fresh scan kick.
    lastScanKickedDeviceIdRef.current = null;
  }, [activeDevice?.id]);
  useEffect(() => {
    if (!activeDevice?.id || !isConnected) return;
    if (lastScanKickedDeviceIdRef.current === activeDevice.id) return;
    lastScanKickedDeviceIdRef.current = activeDevice.id;
    void quicClient.refreshMobileProjects().catch(() => {});
  }, [activeDevice?.id, isConnected]);

  // Poll dev server status + all projects
  useEffect(() => {
    if (!isConnected) return;
    let mounted = true;

    const pollStatus = async () => {
      try {
        const [status, session] = await Promise.all([
          quicClient.getDevServerStatus(),
          quicClient.getMobileWorkerPreviewSession(),
        ]);
        if (mounted) setDevStatus(isActiveDevServerStatus(status) ? status : null);
        if (mounted) setWorkerSession(session);
      } catch {
        if (mounted) setDevStatus(null);
        if (mounted) setWorkerSession(null);
      }
    };

    const fetchProjects = async () => {
      try {
        const [projectsData, reposData] = await Promise.all([
          quicClient.listMobileProjectsDetailed(),
          quicClient.listWorkspaceRepos().catch(() => ({ repos: [] })),
        ]);
        if (!mounted) return;
        setProjects(projectsData.projects);
        setProjectsDiscovering(!!projectsData.discovery?.discovering);
        setRepos(reposData.repos);
      } catch {}
    };

    pollStatus();
    fetchProjects();
    const statusInterval = setInterval(pollStatus, 3000);
    const projectInterval = setInterval(fetchProjects, projectsDiscovering ? 2500 : 15000);
    return () => { mounted = false; clearInterval(statusInterval); clearInterval(projectInterval); };
    // activeDevice?.id in deps so the poll loop tears down + restarts
    // on a Remote Box switch — without it the closure keeps using the
    // same `mounted` flag and the user has to wait up to 15s for the
    // next interval tick to fetch the new box's data.
  }, [isConnected, projectsDiscovering, activeDevice?.id]);

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
                } else if (event.type === "log" && event.logLine) {
                  const ln = String(event.logLine).trim();
                  if (ln) setWebPreviewLogs((p) => (p[p.length - 1] === ln ? p : [...p, ln].slice(-40)));
                } else if (event.type === "building" && event.message) {
                  const ln = String(event.message).trim();
                  if (ln) setWebPreviewLogs((p) => (p[p.length - 1] === ln ? p : [...p, ln].slice(-40)));
                } else if (event.type === "error") {
                  const em = String(event.message || "Dev server failed to start").trim();
                  setWebPreviewLogs((p) => [...p, `ERROR: ${em}`].slice(-40));
                  setWebPreviewFailed(true);
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

  // Tap project → if dev server running, always use Hermes push (fast, ~10s).
  // This keeps iPhone testing working from Linux, WSL, and remote hosts.
  // Xcode native build is available via "Install Native" action in the sheet.
  const handleTapProject = useCallback(async (projectOrQuery: ProjectItem | string) => {
    const selectedProject = typeof projectOrQuery === "string"
      ? findProjectMatch(projects, projectOrQuery)
      : projectOrQuery;
    const projectName = selectedProject?.name || (typeof projectOrQuery === "string" ? projectOrQuery : projectOrQuery.name);
    const projectPath = selectedProject?.path || "";
    const isRunning = !!projectPath && devStatus?.workDir === projectPath;
    if (isRunning) {
      // Tapping the RUNNING project = SEE it. Open the browser preview for every
      // stack (web-served or not) — except a Hermes RN bundle that loads natively
      // into the container. No LAN flush: that path was removed entirely.
      if (isHermesMobileFramework(devStatus?.framework)
          && !isWebServedStatus({ platform: devStatus?.platform, devMode: devStatus?.devMode })) {
        handleOpenNative(devStatus!.workDir!, devStatus?.framework);
        return;
      }
      resetWebPreview();
      setWebViewLoading(true);
      setWebViewKey((k) => k + 1);
      setShowWebView(true);
      return;
    }

    setLoadingActions(true);
    try {
      const result = projectPath
        ? await quicClient.getProjectActionsByPath(projectPath)
        : await quicClient.getProjectActions(projectName);
      let compatibility: DevCompatibilityStatus | null = null;
      const hermesFramework = result.actions.find((a: any) => isHermesMobileFramework(a.framework))?.framework;
      const secondClassFramework = result.actions.find((a: any) => isSecondClassMobileFramework(a.framework))?.framework;
      if (isHermesMobileFramework(hermesFramework)) {
        try {
          const availableModules = await getAvailableModules();
          compatibility = await quicClient.getDevCompatibility(result.path, availableModules);
        } catch {
          compatibility = null;
        }
      }
      // ── Only VIBING / reload lanes ──────────────────────────────────────
      // Build, deploy, tests, git-sync, project-overview and preview-manifest
      // are all removed ON PURPOSE. Tapping a project is "how do you want to
      // SEE it run", nothing else — the user drives building, deploying and
      // testing by vibing text to the agent, so those never need a button here.
      //
      // Three reload lanes, framework-gated:
      //   Hermes Reload  — RN/Expo only (loads the real bundle into the Yaver
      //                    container on THIS phone).
      //   Browser Reload — RN web target, Flutter web, and plain web stacks.
      //   WebRTC Reload  — universal: RN, Flutter, Swift/iOS, Kotlin/Android
      //                    (streams the app from the dev box).
      const fw = hermesFramework || secondClassFramework ||
        result.actions.find((a: any) => a.framework)?.framework || "";
      const isRN = isHermesMobileFramework(hermesFramework);

      // Exactly three lanes, no Flush:
      //   Hermes  — RN/Expo ONLY (loads the JS bundle into the container).
      //   Browser — ALL stacks (serves the web target in a WebView). This is the
      //             primary, universal vibing lane.
      //   WebRTC  — ALL stacks (streams the app from the box).
      const reloadLanes: any[] = [];
      if (isRN) {
        reloadLanes.push({
          label: "Hermes Reload", target: ".", type: "open-native", icon: "\u{1F4F1}",
          framework: hermesFramework, platform: Platform.OS,
          supported: compatibility?.compatible !== false, reason: compatibility?.errors?.[0],
        });
      }
      reloadLanes.push({
        label: "Browser Reload", target: ".", type: "dev-server", icon: "\u{1F310}",
        framework: fw, platform: Platform.OS, supported: true,
      });
      reloadLanes.push({
        label: "WebRTC Reload", target: ".", type: "remote-runtime", icon: "\u{1F4FA}",
        framework: fw, platform: Platform.OS, supported: true,
      });

      let composed = guardYaverSelfDevelopmentActions(
        reloadLanes, projectName, result.path || projectPath,
      );
      // The AGENT owns which lanes actually apply — it can see the project on
      // disk. This strips Hermes for a Flutter/Kotlin/Swift project (no RN
      // runtime to load a bundle into) and orders by the fastest lane. An older
      // box that doesn't know the verb leaves the composed lanes untouched.
      try {
        const caps: any = await runtimeSurfaceClient.projectPreviewOptions(
          activeDevice?.id,
          { workDir: result.path || projectPath, projectName, hasPairedDevice: true },
        );
        composed = applyPreviewCapabilities(composed, caps);
      } catch {
        /* older agent — keep the locally composed lanes */
      }
      result.actions = composed;
      setActionSheet({ ...result, compatibility });
    } catch (e) {
      // Don't silently send a vague task — the user just tapped a project and
      // deserves to know the dev machine couldn't answer. sendTask as a vague
      // "run on my phone" string is almost never what they meant.
      const msg = e instanceof Error ? e.message : String(e);
      Alert.alert(
        "Couldn't Load Project",
        `Yaver ${describeConnectionStatus(connectionStatus)}.\n\n${msg}`,
      );
    } finally {
      setLoadingActions(false);
    }
  }, [devStatus, isDirectConnection, connectionStatus, router, projects]);

  // `yaver insert <app>` from the dev machine ends up here:
  // _layout.tsx receives the open_app command, navigates to this
  // tab, and publishes the app name on openAppBus. We replay the
  // exact same handleTapProject() flow a manual tap would run.
  useEffect(() => {
    return openAppBus.subscribe((app) => {
      handleTapProject(app).catch(() => {
        // handleTapProject already surfaces its own errors via Alert.
      });
    });
  }, [handleTapProject]);

  // Execute a specific action from the action sheet
  const handleExecuteAction = useCallback(async (action: { label: string; target: string; type: string; framework?: string; platform?: string; command?: string; supported?: boolean; reason?: string }) => {
    // Guard: nothing to do if we're not connected to a dev machine.
    if (!isConnected) {
      Alert.alert(
        "Dev Machine Offline",
        `Yaver ${describeConnectionStatus(connectionStatus)}. Nothing can run until the dev machine is reachable again.`,
      );
      return;
    }

    // Block unsupported actions — but flush-mobile has a richer fallback
    // (store deploy, platform mismatch, LAN-missing explanation) that we want
    // the user to see instead of a generic "coming soon" toast.
    if (action.supported === false && action.type !== "flush-mobile") {
      Alert.alert("Not Supported", action.reason || `${action.label} for ${action.framework || "this project"} is not available right now.`);
      return;
    }

    const project = actionSheet?.project ?? "";
    const path = actionSheet?.path ?? "";
    const compatibility = actionSheet?.compatibility ?? null;
    setActionSheet(null);

    if (action.type === "git-sync") {
      await sendTaskOrWarn(
        `Git Sync — ${project}`,
        `cd ${path} && Sync this repository with its remote. Pull the latest changes. If there are merge conflicts, resolve them intelligently. If the local branch is behind, rebase or merge as appropriate. If there are uncommitted local changes, stash them first, pull, then re-apply. Show me a summary of what changed.`,
        `Git Sync for ${project}`,
      );
      return;
    }

    if (action.type === "project") {
      router.navigate({ pathname: "/(tabs)/project", params: { dir: path } } as any);
      return;
    }

    if (action.type === "preview-manifest") {
      router.navigate({ pathname: "/preview-manifest", params: { project, path, framework: action.framework || "" } } as any);
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
            Alert.alert("Nothing To Show Yet", "No suggestions available for this project yet.");
          }
        } catch (e) {
          Alert.alert(
            "Couldn't Load Suggestions",
            `Yaver couldn't load suggestions for this project. Check your connection and try again.\n\n${e instanceof Error ? e.message : String(e)}`,
          );
        }
      }, 400);
      return;
    }

    if (action.type === "agent") {
      router.navigate({ pathname: "/(tabs)/agent", params: { project, path } } as any);
      return;
    }

    if (action.type === "autotest") {
      // Jump to Local CI / runs screen (yaver-test-sdk), which is where
      // auto-test loops live. Same pattern as Auto Dev.
      router.navigate({ pathname: "/(tabs)/runs", params: { project, path } } as any);
      return;
    }

    if (action.type === "open-native") {
      await handleOpenNative(path, action.framework);
      return;
    }

    if (action.type === "compile-hermes") {
      if (compatibility?.errors?.length) {
        Alert.alert("Compatibility Blocked", compatibility.errors[0]);
        return;
      }
      await handleCompileHermes(path, action.framework);
      return;
    }

    if (action.type === "flush-mobile") {
      await handleFlushMobile(path, action.framework);
      return;
    }

    if (action.type === "remote-runtime") {
      router.navigate({ pathname: "/remote-runtime", params: { project, path, framework: action.framework || "" } } as any);
      return;
    }

    if (action.type === "dev-server") {
      // Direct dev server start — use the exact target path (handles monorepos like talos/mobile)
      setStartingProject(project);
      const targetPath = action.target === "." ? path : `${path}/${action.target}`.replace(/\/+$/, "");
      let deferStartingClear = false;
      try {
        await quicClient.startDevServer({
          framework: action.framework || "",
          workDir: targetPath,
          // Browser Reload = the browser lane. Serve the web target, never a
          // Hermes native bundle (Hermes needs the guest's native modules to
          // match the container — sfmg dies on expo-gl — has no meaning for
          // Flutter, and is blocked for Yaver-self-dev).
          web: true,
          targetDeviceId: selectedTarget?.id,
          targetDeviceName: selectedTarget?.name,
          targetDeviceClass: selectedTarget?.deviceClass,
        });
        // Rendering is NOT a task. Do NOT spawn a coding task or navigate to
        // Tasks — the agent starts the dev server directly (it handles flutter
        // web / expo web itself). Open the full-screen browser preview RIGHT HERE
        // so the user sees the remote runtime come up (WebView + loading bar +
        // Reload), instead of the action sheet closing to a blank Projects list
        // with the progress hidden on the Tasks tab. The WebView auto-retries
        // while the web server is still compiling (onError below).
        setActionSheet(null);
        resetWebPreview();

        setWebViewKey((k) => k + 1);
        setWebViewLoading(true);
        setShowWebView(true);
      } catch (e) {
        const err = e as Error & {
          kind?: "missing-runtime";
          missingTools?: string[];
          installEndpoint?: string;
          installable?: boolean;
          helpHint?: string;
        };
        if (err?.kind === "missing-runtime" && err.installable && err.installEndpoint) {
          // Phone-driven sudo-free install path: fire /install/node and
          // stream the agent's output as an alert until the result event
          // lands, then auto-retry the dev server start. The async work
          // outlives this try/catch so we hold the spinner until the
          // result handler clears it.
          deferStartingClear = true;
          const tool = err.installEndpoint.replace(/^\/install\//, "") || "node";
          const missingLabel = (err.missingTools || []).join(", ") || tool;
          Alert.alert(
            "Install required",
            `${missingLabel} missing on dev box. Install Node LTS into ~/.yaver/runtimes/node (no sudo)?`,
            [
              { text: "Cancel", style: "cancel", onPress: () => setStartingProject(null) },
              {
                text: "Install",
                onPress: async () => {
                  let lastLine = "Starting…";
                  setQuickActionStatus(`Installing ${tool}: ${lastLine}`);
                  const res = await quicClient.installTool(tool);
                  if (!res.ok) {
                    Alert.alert("Install failed", res.error || "Unknown error");
                    setStartingProject(null);
                    setQuickActionStatus(null);
                    return;
                  }
                  const cancel = quicClient.subscribeStream(
                    res.stream,
                    (line) => {
                      lastLine = line;
                      setQuickActionStatus(`Installing ${tool}: ${line.slice(0, 80)}`);
                    },
                    async (status, error) => {
                      cancel();
                      if (status === "ok") {
                        setQuickActionStatus("Install complete — retrying dev server…");
                        try {
                          await quicClient.startDevServer({
                            framework: action.framework || "",
                            workDir: targetPath,
                            web: true,
                            targetDeviceId: selectedTarget?.id,
                            targetDeviceName: selectedTarget?.name,
                            targetDeviceClass: selectedTarget?.deviceClass,
                          });
                        } catch (retryErr) {
                          Alert.alert(
                            "Dev server failed after install",
                            retryErr instanceof Error ? retryErr.message : String(retryErr),
                          );
                        }
                        setQuickActionStatus(null);
                      } else {
                        Alert.alert("Install failed", error || lastLine);
                        setQuickActionStatus(null);
                      }
                      setStartingProject(null);
                    },
                  );
                },
              },
            ],
          );
          return;
        }
        // Genuine failure to start the preview — surface it inline, do NOT
        // spawn a task. Rendering stays out of the task system.
        Alert.alert(
          "Couldn't start the preview",
          e instanceof Error ? e.message : "The dev server didn't start. Check the machine is reachable.",
        );
      } finally {
        if (!deferStartingClear) setStartingProject(null);
      }
    } else if (action.command) {
      // Direct command
      await sendTaskOrWarn(
        `${action.label} — ${project}`,
        `cd ${path}/${action.target} && ${action.command}`,
        action.label,
      );
    } else {
      // AI handles it
      await sendTaskOrWarn(
        `${action.label} for ${project}`,
        `Project: ${path}/${action.target}. Platform: ${action.platform || action.framework || "auto"}. Do it.`,
        `${action.label} for ${project}`,
      );
    }
  }, [actionSheet, selectedTarget, isConnected, connectionStatus, sendTaskOrWarn]);

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
      .catch((e) => {
        // Not user-triggered — don't alert. But keep a breadcrumb so if the
        // UI sticks to a stale target we can tell why from the console/devtools.
        console.warn("[apps] getDevServerTarget failed — keeping last known target:", e);
      });
    return () => { mounted = false; };
  }, [isConnected, activeDevice?.id]);

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
      Alert.alert(
        "Couldn't Start Build",
        `Yaver couldn't start the device build. Check your connection and try again.\n\n${e instanceof Error ? e.message : String(e)}`,
      );
      setTimeout(() => setBuildStatus(null), 3000);
    }
  }, [devStatus?.workDir]);


  const handleFlushMobile = useCallback(async (workDir: string, framework?: string) => {
    if (!framework || !isSecondClassMobileFramework(framework)) return;

    const platformMismatch =
      (framework === "swift" && Platform.OS !== "ios") ||
      (framework === "kotlin" && Platform.OS !== "android");
    const canDirectInstall = isDirectConnection && !platformMismatch;

    if (!canDirectInstall) {
      const deploy = storeDeployDescriptor(framework);
      const projectName = workDir.split("/").filter(Boolean).pop() || "app";
      const frameworkLabel = framework === "flutter" ? "Flutter" : framework === "swift" ? "native iOS (Swift)" : "native Android (Kotlin)";
      const reason = !isDirectConnection
        ? `Running ${frameworkLabel} directly on your phone needs both your machine and phone on the same Wi-Fi. Right now you're on relay / 4G, so the direct install is not possible.`
        : `This ${frameworkLabel} build needs to run on a ${framework === "swift" ? "iPhone" : "Android phone"}, but you're controlling Yaver from ${Platform.OS === "ios" ? "iPhone" : "Android"}. A direct install from this phone is not possible.`;
      const blocker = deploy ? deployBlocker(deploy.target, activeDevice?.os) : null;
      const alternative = !deploy
        ? ""
        : blocker
          ? `\n\nWe also can't ship it to ${deploy.label.replace(/^Ship to /, "")} from this dev machine — ${blocker}`
          : `\n\nWe can still build it on your machine (${activeDevice?.os || "dev machine"}) and ship it to ${deploy.label.replace(/^Ship to /, "")} so your phone picks it up from the store.`;
      Alert.alert(
        !isDirectConnection ? "LAN Required" : "Wrong Phone Class",
        `${reason}\n\nHermes (Expo / React Native) is the only first-class path that works over LAN, relay, and 4G.${alternative}`,
        deploy && !blocker ? [
          { text: "Cancel", style: "cancel" },
          {
            text: deploy.label,
            onPress: async () => {
              try {
                await quicClient.sendTask(deploy.prompt(projectName, workDir), `[Store Deploy] ${projectName} · ${framework}`);
                setQuickActionStatus(`${deploy.label} task sent`);
                setTimeout(() => setQuickActionStatus(null), 4000);
                router.navigate("/(tabs)/tasks");
              } catch (e) {
                Alert.alert("Could not queue deploy task", e instanceof Error ? e.message : String(e));
              }
            },
          },
        ] : undefined,
      );
      return;
    }

    if (framework === "flutter") {
      setNativeLoading(true);
      setLoadingStatus("Flushing Flutter app...");
      setQuickActionStatus("Starting Flutter flush...");
      try {
        const currentStatus = await quicClient.getDevServerStatus();
        if (currentStatus?.running && currentStatus.workDir === workDir && currentStatus.framework === "flutter") {
          await quicClient.reloadDevServer();
          setQuickActionStatus("Flutter reload sent");
          Alert.alert("Flutter Flushed", "A Flutter reload was sent over LAN.");
        } else {
          await quicClient.startDevServer({
            framework: "flutter",
            workDir,
            targetDeviceId: selectedTarget?.id,
            targetDeviceName: selectedTarget?.name,
            targetDeviceClass: selectedTarget?.deviceClass,
          });
          setQuickActionStatus("Flutter launch started on LAN");
          Alert.alert("Flutter Flush Started", "Yaver asked the Go agent to start the Flutter app on your phone over LAN.");
        }
      } catch (e) {
        const err = e instanceof Error ? e.message : String(e);
        setNativeLoading(false);
        setLoadingStatus("");
        setTimeout(() => setQuickActionStatus(null), 4000);
        const missingDevice = /no device|device.*(not found|missing|offline)/i.test(err);
        offerAgentFix(
          "Flutter Flush Failed",
          `${err}\n\nYaver can hand this to the AI agent so it can diagnose and fix on the dev machine.`,
          {
            kind: missingDevice ? "flutter-device-missing" : "flutter-flush-failed",
            framework: "flutter",
            workDir,
            platform: Platform.OS,
            error: err,
          },
        );
        return;
      } finally {
        setNativeLoading(false);
        setLoadingStatus("");
        setTimeout(() => setQuickActionStatus(null), 4000);
      }
      return;
    }

    if (framework === "swift") {
      setBuildStatus("queued");
      setQuickActionStatus("Starting native iOS flush...");
      try {
        const build = await quicClient.startBuild("xcode-device-install", workDir, true);
        setBuildStatus("running");
        let consecutivePollFailures = 0;
        for (let i = 0; i < 120; i++) {
          await new Promise((resolve) => setTimeout(resolve, 2000));
          let b: Awaited<ReturnType<typeof quicClient.getBuild>> | null = null;
          try {
            b = await quicClient.getBuild(build.id);
          } catch (pollErr) {
            consecutivePollFailures += 1;
            if (consecutivePollFailures >= 5) {
              throw new Error(`Lost contact with build server after ${consecutivePollFailures} consecutive failures: ${pollErr instanceof Error ? pollErr.message : String(pollErr)}`);
            }
            continue;
          }
          consecutivePollFailures = 0;
          if (!b) continue;
          if (b.installStatus === "installed") {
            setBuildStatus("installed");
            setQuickActionStatus("Native iOS app installed on phone");
            Alert.alert("Installed", "The native iOS app was installed on your phone.");
            setTimeout(() => { setBuildStatus(null); setQuickActionStatus(null); }, 5000);
            return;
          }
          if (b.installStatus === "install_failed") {
            setBuildStatus("install_failed");
            setTimeout(() => { setBuildStatus(null); setQuickActionStatus(null); }, 5000);
            offerAgentFix(
              "iOS Install Failed",
              `${b.installError || "Could not install on device."}\n\nMost common causes: provisioning profile doesn't include this iPhone UDID, device isn't trusted, or Xcode can't see the phone. Yaver can ask the AI agent to fix the signing / provisioning.`,
              { kind: "swift-install-failed", framework: "swift", workDir, platform: Platform.OS, error: b.installError || "install failed" },
            );
            return;
          }
          if (b.installStatus === "installing") {
            setBuildStatus("installing");
            setQuickActionStatus("Installing native iOS app...");
          } else if (b.status === "failed") {
            setBuildStatus("failed");
            setTimeout(() => { setBuildStatus(null); setQuickActionStatus(null); }, 5000);
            offerAgentFix(
              "Xcode Build Failed",
              `${b.error || "xcodebuild failed."}\n\nLikely a signing, pods, or SDK issue on the dev machine. The AI agent can diagnose and fix end-to-end.`,
              { kind: "swift-build-failed", framework: "swift", workDir, platform: Platform.OS, error: b.error || "xcodebuild failed" },
            );
            return;
          }
        }
        throw new Error("Build timed out after 4 minutes. Run it again or check xcodebuild logs on the dev machine.");
      } catch (e) {
        const err = e instanceof Error ? e.message : String(e);
        setBuildStatus("failed");
        setTimeout(() => { setBuildStatus(null); setQuickActionStatus(null); }, 5000);
        offerAgentFix(
          "Native iOS Flush Failed",
          `${err}\n\nThe AI agent can investigate the Xcode + signing setup and fix it.`,
          { kind: "swift-build-failed", framework: "swift", workDir, platform: Platform.OS, error: err },
        );
      }
      return;
    }

    if (framework === "kotlin") {
      setBuildStatus("queued");
      setQuickActionStatus("Building Android APK...");
      try {
        const build = await quicClient.startBuild("gradle-apk", workDir);
        setBuildStatus("running");
        let consecutivePollFailures = 0;
        for (let i = 0; i < 180; i++) {
          await new Promise((resolve) => setTimeout(resolve, 2000));
          let b: Awaited<ReturnType<typeof quicClient.getBuild>> | null = null;
          try {
            b = await quicClient.getBuild(build.id);
          } catch (pollErr) {
            consecutivePollFailures += 1;
            if (consecutivePollFailures >= 5) {
              throw new Error(`Lost contact with build server after ${consecutivePollFailures} consecutive failures: ${pollErr instanceof Error ? pollErr.message : String(pollErr)}`);
            }
            continue;
          }
          consecutivePollFailures = 0;
          if (!b) continue;
          if (b.status === "failed") {
            setBuildStatus("failed");
            setTimeout(() => { setBuildStatus(null); setQuickActionStatus(null); }, 5000);
            offerAgentFix(
              "Gradle Build Failed",
              `${b.error || "Gradle build failed."}\n\nCheck the dev machine has Java 17 + Android SDK, or let the AI agent diagnose and fix.`,
              { kind: "kotlin-build-failed", framework: "kotlin", workDir, platform: Platform.OS, error: b.error || "Gradle build failed" },
            );
            return;
          }
          if (b.status === "completed" && b.artifactName) {
            setBuildStatus("installing");
            setQuickActionStatus("Downloading APK to phone...");
            let localPath: string;
            try {
              localPath = await downloadArtifact(
                quicClient.baseUrl,
                quicClient.getAuthHeaders(),
                build.id,
              );
            } catch (dlErr) {
              const err = dlErr instanceof Error ? dlErr.message : String(dlErr);
              setBuildStatus("install_failed");
              setTimeout(() => { setBuildStatus(null); setQuickActionStatus(null); }, 5000);
              offerAgentFix(
                "APK Download Failed",
                `Could not pull the APK from the dev machine: ${err}\n\nThe AI agent can inspect the artifact endpoint or re-run the build.`,
                { kind: "apk-download-failed", framework: "kotlin", workDir, platform: Platform.OS, error: err },
              );
              return;
            }
            try {
              const installer = NativeModules.ApkInstaller;
              if (!installer || typeof installer.install !== "function") {
                throw new Error("ApkInstaller native module is not registered in this build of Yaver. Reinstall Yaver from the Play Store.");
              }
              await installer.install(localPath);
            } catch (instErr) {
              const err = instErr instanceof Error ? instErr.message : String(instErr);
              setBuildStatus("install_failed");
              setTimeout(() => { setBuildStatus(null); setQuickActionStatus(null); }, 5000);
              // APK install on Android is an OS-level dialog, not AI-fixable,
              // so we keep this as a plain alert explaining the system setting.
              Alert.alert(
                "APK Install Failed",
                `${err}\n\nIf Android blocked the install, enable "Install unknown apps" for Yaver in system settings and retry. If a previous debug-signed copy is conflicting, uninstall it first.`,
              );
              return;
            }
            setBuildStatus("installed");
            setQuickActionStatus("Android app ready to open");
            Alert.alert("APK Ready", "The Android build was downloaded to your phone and the install flow was started.");
            setTimeout(() => { setBuildStatus(null); setQuickActionStatus(null); }, 5000);
            return;
          }
        }
        throw new Error("Build timed out after 6 minutes. Run it again or check gradlew logs on the dev machine.");
      } catch (e) {
        setBuildStatus("failed");
        Alert.alert("Native Android Flush Failed", e instanceof Error ? e.message : String(e));
        setTimeout(() => { setBuildStatus(null); setQuickActionStatus(null); }, 5000);
      }
    }
  }, [isDirectConnection, selectedTarget, activeDevice?.os, router]);

  const ensureHermesDevServer = useCallback(async (workDir: string, framework?: string) => {
    const currentStatus = await quicClient.getDevServerStatus();
    if (currentStatus?.running && currentStatus.workDir === workDir) {
      return;
    }

    setLoadingStatus("Starting dev server...");
    setBuildProgress(0.05);
    await quicClient.startDevServer({
      framework: framework || "expo",
      workDir,
      targetDeviceId: selectedTarget?.id,
      targetDeviceName: selectedTarget?.name,
      targetDeviceClass: selectedTarget?.deviceClass,
    });

    for (let i = 0; i < 30; i++) {
      await new Promise((resolve) => setTimeout(resolve, 1000));
      const status = await quicClient.getDevServerStatus();
      setLoadingStatus(status?.running ? "Dev server ready" : "Starting dev server...");
      if (status?.running && status.workDir === workDir) return;
    }

    throw new Error("Dev server did not become ready in time");
  }, [selectedTarget]);

  const buildHermesBundle = useCallback(async ({ workDir, framework, loadAfterBuild }: {
    workDir: string;
    framework?: string;
    loadAfterBuild: boolean;
  }) => {
    // Guard against callers accidentally routing a second-class project through
    // the Hermes path. Without this, the dev server start below fails with an
    // opaque "could not detect framework" far from the real mistake.
    if (framework && !isHermesMobileFramework(framework)) {
      Alert.alert(
        "Wrong Action For This Project",
        `"${framework}" projects can't be loaded inside Yaver — Hermes is React Native / Expo only. Use Flush to App for Flutter or Flush Build to Phone for Swift / Kotlin.`,
      );
      return;
    }
    // Loading a guest app inside the Yaver container needs the native
    // YaverBundleLoader module (iOS + Android). Guard on the capability so an
    // old build / web preview without the module stops here instead of
    // building a bundle it can't load (a confusing "native module" error).
    if (!isBundleLoaderAvailable()) {
      Alert.alert(
        "Bundle Loader Unavailable",
        "This build of Yaver can't mount guest bundles. Update Yaver to the latest version — or run the app directly on the dev machine.",
      );
      return;
    }
    const baseUrl = (quicClient as any).baseUrl;
    if (!baseUrl) {
      Alert.alert(
        "Dev Machine Not Connected",
        `Yaver ${describeConnectionStatus(connectionStatus)}. Reconnect on the Devices tab before building.`,
      );
      return;
    }

    setNativeLoading(true);
    setBuildProgress(0);
    const headers = {
      ...(quicClient as any).authHeaders,
      "Content-Type": "application/json",
    };

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
            if (!line.startsWith("data: ")) continue;
            try {
              const event = JSON.parse(line.slice(6));
              // Two SSE shapes carry useful text:
              //  - event.message  → high-level phase from emitBuildProgress
              //                     ("Bundling with Expo for ios...")
              //  - event.logLine  → individual stdout line from Metro/hermesc
              //                     ("iOS node_modules/expo-router/entry.js …62.8% (953/1217)")
              // The phase line drives loadingStatus + the progress bar
              // bucket; the logLine drives the live tail under the bar.
              if (event.type === "log") {
                if (event.logLine) {
                  // Trim noisy prefixes the agent's devLogWriter prepends
                  // ([super-host], [super-host:hermesc]) so the mobile tail
                  // stays readable in 1 line.
                  const clean = String(event.logLine)
                    .replace(/^\[super-host(?::hermesc)?\]\s*/, "")
                    .trimEnd();
                  if (clean) setBundlerLine(clean);
                }
                if (event.message) {
                  const msg = event.message;
                  setLoadingStatus(msg);
                  if (msg.includes("Installing dependencies")) setBuildProgress(0.1);
                  else if (msg.includes("Bundling")) setBuildProgress(0.3);
                  else if (msg.includes("Compiling Hermes")) setBuildProgress(0.7);
                  else if (msg.includes("Bundle ready")) setBuildProgress(0.95);
                }
              }
            } catch {}
          }
        }
      } catch {}
    };
    listenSSE();

    try {
      await ensureHermesDevServer(workDir, framework);

      setLoadingStatus("Building Hermes bundle...");
      setBuildProgress(0.15);
      const platform = (Platform.OS as string) === "android" ? "android" : "ios";
      const buildRes = await fetch(`${baseUrl}/dev/build-native`, {
        method: "POST",
        headers,
        body: JSON.stringify(
          buildNativeBuildRequest(
            platform,
            currentYaverConsumerContract(),
            { projectPath: workDir },
          ),
        ),
      });
      const buildResult = await buildRes.json();

      if (buildResult.status !== "ok") {
        const error = new Error(nativeBuildFailureMessage(buildResult));
        (error as any).buildResult = buildResult;
        throw error;
      }
      const familySelection = buildResult.runtimeFamilySelection;
      const familyLabel = familySelection?.selected?.label || familySelection?.selected?.id || "";

      if (loadAfterBuild) {
        const sizeKB = Math.round((buildResult.size || 0) / 1024);
        setLoadingStatus(
          familySelection?.exactMatch && familyLabel
            ? `Downloading ${sizeKB}KB bundle · matched ${familyLabel}...`
            : `Downloading ${sizeKB}KB bundle${familyLabel ? ` · closest ${familyLabel}` : ""}...`,
        );
        setBuildProgress(0.95);
        const bundleUrl = `${baseUrl}${buildResult.bundleUrl}`;
        const moduleName = buildResult.moduleName || "main";
        await loadApp(bundleUrl, moduleName, (quicClient as any).authHeaders);
        setBuildProgress(1);
        setLoadingStatus(`Loaded${familyLabel ? ` · ${familyLabel}` : ""}!`);
      } else {
        setBuildProgress(1);
        setLoadingStatus(`Hermes bundle ready${familyLabel ? ` · ${familyLabel}` : ""}`);
      }
    } catch (err: any) {
      // Reset loading state BEFORE the alert so a fast dismissal can't leave
      // the UI stuck in a half-built state and trigger a double-build.
      sseController.abort();
      setNativeLoading(false);
      setBuildProgress(0);
      setLoadingStatus("");
      const raw = err?.message || "Could not build Hermes bundle in Yaver";
      const lower = raw.toLowerCase();
      const buildResult = err?.buildResult;
      let hint = "";
      if (lower.includes("did not become ready") || lower.includes("dev server")) {
        hint = "\n\nMetro didn't start on the dev machine. Check Node.js is installed and the project has a valid package.json.";
      } else if (buildResult?.code === "RUNTIME_FAMILY_MISMATCH" || buildResult?.code === "FRAMEWORK_VERSION_MISMATCH") {
        hint = "\n\nYaver picked the nearest supported runtime family, but the guest app still does not match it exactly. Align the guest app to one of Yaver's supported families or switch to a native build fallback.";
      } else if (lower.includes("hbc") || lower.includes("bytecode") || lower.includes("hermes")) {
        hint = "\n\nHermes bytecode version mismatch between the guest app and the selected Yaver host family. Align the guest runtime to a supported family and retry.";
      } else if (lower.includes("yaverbundleloader") || lower.includes("native module")) {
        hint = "\n\nYaver's native bundle loader is missing from this build — update Yaver to the latest version, or run the app directly on the dev machine.";
      } else if (lower.includes("network") || lower.includes("fetch") || lower.includes("timeout")) {
        hint = `\n\nYaver ${describeConnectionStatus(connectionStatus)}.`;
      }
      const title = buildResult
        ? nativeBuildFailureTitle(buildResult)
        : (loadAfterBuild ? "Open in Yaver Failed" : "Hermes Build Failed");

      // Compatibility blocks are the one failure a remote runner can actually
      // repair (guard an unguarded require, align a version down to the host).
      // Every other framework failure already routes through offerAgentFix; the
      // compat dialog used to dead-end in a bare alert. Wire it to the same
      // self-heal path, threading the STRUCTURED report through so the fix task
      // names the exact modules and versions — the agent builds the prompt from
      // ctx.compat (RecoveryHermesCompatBlocked), the phone does not.
      const compatCodes = [
        "NATIVE_MODULE_INCOMPATIBLE",
        "NATIVE_MODULE_VERSION_MISMATCH",
        "REACT_VERSION_MISMATCH",
        "FRAMEWORK_VERSION_MISMATCH",
        "RUNTIME_FAMILY_MISMATCH",
        "BC_VERSION_MISMATCH",
      ];
      if (buildResult && compatCodes.includes(buildResult.code)) {
        offerAgentFix(title, `${raw}${hint}`, {
          kind: "hermes-compat-blocked",
          framework: devStatus?.framework || undefined,
          workDir: buildResult.workDir || devStatus?.workDir || undefined,
          platform: Platform.OS,
          project: buildResult.projectName || undefined,
          error: raw,
          // Forward the whole 409 payload as the compat report — its top-level
          // keys (incompatibleNativeModules, nativeModuleVersionMismatches,
          // guestRuntime, runtimeFamilySelection, …) match the agent's
          // CompatReport JSON, so the agent decodes what it needs and ignores
          // the rest.
          compat: buildResult,
        }, "Try to Fix");
        return;
      }
      Alert.alert(title, `${raw}${hint}`);
      return;
    }
    sseController.abort();
    setNativeLoading(false);
    setBuildProgress(0);
    setTimeout(() => setLoadingStatus(""), 2000);
  }, [ensureHermesDevServer, connectionStatus]);

  // Open app natively: Go agent builds Hermes bytecode → phone loads into RCTBridge
  const handleOpenNative = useCallback(async (workDir: string, framework?: string) => {
    await buildHermesBundle({ workDir, framework, loadAfterBuild: true });
  }, [buildHermesBundle]);

  const handleCompileHermes = useCallback(async (workDir: string, framework?: string) => {
    await buildHermesBundle({ workDir, framework, loadAfterBuild: false });
  }, [buildHermesBundle]);

  const handleOpen = useCallback(() => {
    if (!devStatus?.workDir) return;
    if (isHermesMobileFramework(devStatus.framework)) {
      // Always Hermes push — fast (~10s), works on LAN and relay equally.
      // This is the default iPhone path for Linux / WSL / remote dev.
      // Xcode native device install is available as a separate "Install Native" action.
      handleOpenNative(devStatus.workDir, devStatus.framework);
      return;
    }
    if (devStatus.framework === "flutter") {
      handleFlushMobile(devStatus.workDir, devStatus.framework);
    }
  }, [devStatus, handleFlushMobile, handleOpenNative]);

  const handleReload = useCallback(async () => {
    const nativeHermes = isHermesMobileFramework(devStatus?.framework);
    if (!nativeHermes) {
      setWebViewLoading(true);
    }
    await quicClient.reloadDevServer({ mode: nativeHermes ? "bundle" : "dev" });
    if (!nativeHermes) {
      setWebViewKey(k => k + 1);
    }
  }, [devStatus?.framework]);

  const handleRequestScreenshot = useCallback(async () => {
    await quicClient.sendMobileWorkerPreviewCommand("capture_screenshot", {
      reason: "apps-control-plane",
    });
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

  if (!effectivelyConnected) {
    // Banner first (always actionable) so the user can tap Switch ›
    // and pick a device — even from the empty state. Below, the same
    // hint text the screen used to render full-bleed.
    return (
      <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <RemoteBoxBanner />
        <View style={s.emptyContainer}>
          <Ionicons name="phone-portrait-outline" size={56} color={c.textTertiary} style={{ opacity: 0.5, marginBottom: 12 }} />
          <Text style={[s.emptyTitle, { color: c.textPrimary }]}>Not connected</Text>
          <Text style={[s.emptySubtitle, { color: c.textSecondary }]}>
            Connect to a device to see your projects
          </Text>
          {/* Explicit Refresh — pull-to-refresh isn't discoverable when the
              list is empty. Re-polls the device list + projects so a box that
              just came back online (or woke from auto-off) reconnects without
              leaving the tab. */}
          <Pressable
            onPress={onPullRefresh}
            disabled={pullRefreshing}
            style={{
              marginTop: 18,
              flexDirection: "row",
              alignItems: "center",
              gap: 8,
              paddingHorizontal: 18,
              paddingVertical: 10,
              borderRadius: 10,
              borderWidth: 1,
              borderColor: c.accent,
              backgroundColor: c.accent + "18",
              opacity: pullRefreshing ? 0.6 : 1,
            }}
          >
            {pullRefreshing ? (
              <ActivityIndicator size="small" color={c.accent} />
            ) : (
              <Ionicons name="refresh" size={16} color={c.accent} />
            )}
            <Text style={{ color: c.accent, fontWeight: "700", fontSize: 14 }}>
              {pullRefreshing ? "Refreshing…" : "Refresh"}
            </Text>
          </Pressable>
        </View>
      </SafeAreaView>
    );
  }

  const currentProject = projects.find((project) => project.path === devStatus?.workDir) ?? null;
  const runningProject = currentProject?.name ?? devStatus?.workDir?.split("/").pop() ?? devStatus?.framework ?? "App";
  const runningSecondClassGuidance = secondClassGuidance(devStatus?.framework, isDirectConnection);
  const devServerBuilding = devStatus?.building === true;
  const devServerBusy = nativeLoading || devServerBuilding;

  return (
    <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <RemoteBoxBanner />
      <View style={s.container}>
        {/* Running app — green card */}
        {devStatus && (
          <View style={[s.card, s.activeCard]}>
            <View style={s.cardHeader}>
              <View style={[s.statusDot, { backgroundColor: devServerBuilding ? "#eab308" : "#22c55e" }]} />
              <View style={s.cardTitleContainer}>
                <Text style={s.cardTitle}>{runningProject}</Text>
                <Text style={s.cardMeta}>
                  {devServerBuilding
                    ? `${devStatus.framework} · starting…`
                    : `${devStatus.framework} · browser preview`}
                </Text>
                {workerSession?.hasTarget && (
                  <Text style={[s.cardMeta, { color: workerSession.workerOnline ? "#86efac" : "#fbbf24" }]}>
                    worker · {workerSession.workerOnline ? "online" : "offline"}
                  </Text>
                )}
              </View>
            </View>
            {/* Vibing = SEE the app. Exactly two actions: "Open in Yaver" opens
                the browser preview (works for every stack — RN, Flutter, web —
                it serves the web target, not a LAN flush), and "Stop". Flush /
                Reload / Screenshots / Ship It were removed on purpose. */}
            <View style={s.cardActions}>
              <Pressable
                style={[s.actionBtn, s.openBtn, { flex: 1 }, devServerBusy && { opacity: 0.5 }]}
                onPress={() => { resetWebPreview(); setWebViewLoading(true); setWebViewKey((k) => k + 1); setShowWebView(true); }}
                disabled={devServerBusy}
              >
                {devServerBusy ? (
                  <>
                    <ActivityIndicator size="small" color="#000" />
                    <Text style={[s.openBtnText, { fontSize: 12, marginLeft: 6 }]}>
                      {devServerBuilding && !nativeLoading ? "Starting…" : "Building…"}
                    </Text>
                  </>
                ) : (
                  <Text style={s.openBtnText}>Open in Yaver</Text>
                )}
              </Pressable>
              <Pressable style={[s.actionBtn, s.stopBtn]} onPress={handleStop}>
                <Text style={s.stopBtnText}>Stop</Text>
              </Pressable>
            </View>

            {/* Build progress — two-line layout while HBC bundle is compiling.
                Line 1 (loadingStatus): the high-level phase, e.g. "Building
                Hermes bundle...". Line 2 (bundlerLine): the latest stdout
                line from Metro/expo-export, updated live as Metro emits
                progress (e.g. "iOS node_modules/expo-router/entry.js
                62.8% (953/1217)"). The second line is what the user actually
                wants to see when a build seems "stuck" — it confirms whether
                the agent is doing useful work or genuinely hung. */}
            {devServerBusy && (
              <View style={s.progressContainer}>
                <View style={s.progressTrack}>
                  <View style={[s.progressFill, { width: `${Math.max(buildProgress * 100, 5)}%` }]} />
                </View>
                {loadingStatus ? (
                  <Text style={s.progressText} numberOfLines={1}>{loadingStatus}</Text>
                ) : devServerBuilding ? (
                  <Text style={s.progressText} numberOfLines={1}>Build is still running on your machine...</Text>
                ) : null}
                {bundlerLine ? (
                  <Text
                    style={[s.progressText, { fontSize: 10, opacity: 0.65, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" }]}
                    numberOfLines={1}
                    ellipsizeMode="middle"
                  >
                    {bundlerLine}
                  </Text>
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
                  {buildStatus === "running" ? "Building on your machine..." :
                   buildStatus === "installing" ? "Flushing to phone..." :
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
                { icon: "\u{1F4F1}", label: "Screenshots", prompt: `Generate App Store and Google Play screenshots for ${runningProject}: capture all key screens at iPhone 6.7", iPhone 6.1", iPad 12.9", and Android phone/tablet sizes. Save to a screenshots/ folder.` },
              ].map((action) => (
                <Pressable
                  key={action.label}
                  style={s.quickBtn}
                  onPress={() => {
                    if (!isConnected) {
                      Alert.alert(
                        "Dev Machine Offline",
                        `Yaver ${describeConnectionStatus(connectionStatus)}. Reconnect before running "${action.label}".`,
                      );
                      return;
                    }
                    if (action.label === "Ship It") {
                      const iosBlocker = deployBlocker("testflight", activeDevice?.os);
                      if (iosBlocker) {
                        Alert.alert(
                          "Can't Ship From This Dev Machine",
                          `${iosBlocker}\n\nAndroid can still be built here; run Deploy to Play Store from Vibing if that's what you want.`,
                        );
                        return;
                      }
                      if (Platform.OS === "ios" && quicClient.connectionMode === "direct" && isHermesMobileFramework(devStatus?.framework)) {
                        // Direct device install — build with Xcode and install on device
                        handleDirectBuild();
                        return;
                      }
                    }
                    // Send as task but stay on this page — surface failures
                    // inline instead of silently swallowing them.
                    setQuickActionStatus(`${action.label}…`);
                    quicClient.sendTask(action.prompt, `[Quick Action] ${action.label} for ${runningProject}`)
                      .then(() => {
                        setQuickActionStatus(`${action.label} sent`);
                        setTimeout(() => setQuickActionStatus(null), 3000);
                      })
                      .catch((e) => {
                        setQuickActionStatus(null);
                        Alert.alert(
                          `Couldn't Send "${action.label}"`,
                          `${e instanceof Error ? e.message : String(e)}\n\nYaver ${describeConnectionStatus(connectionStatus)}.`,
                        );
                      });
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

        {/* Repos — monorepo roots and standalone repos. Tapping one
            opens the project screen scoped to the repo root, where
            Chat → tasks tab inherits workDir=repo-root so codex/claude
            can edit the WHOLE repo (Go agent + web + mobile + cli),
            not just a per-framework subdir. */}
        {/* Repos are hidden in the Mobile view. The sliding strip was the
            first thing on the screen and mostly showed non-mobile repos —
            the user is here for the mobile app. It returns under "All",
            where browsing the whole tree is the point. */}
        {repos.length > 0 && !activeFilter && (
          <View style={s.reposSection}>
            <Text style={[s.reposHeader, { color: c.textMuted }]}>
              Repos · {repos.length}
            </Text>
            {/* Phone keeps the horizontal scroller (one row, swipe to
                see more). Tablets switch to a wrapping grid so the
                repos fan out across the wide canvas instead of
                producing one stretched row. */}
            {layout.isTablet ? (
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                {repos.map((repo) => (
                  <Pressable
                    key={repo.path}
                    style={[
                      s.repoCard,
                      { backgroundColor: c.bgCard, borderColor: c.border, flexBasis: layout.layoutClass === "tablet-landscape" ? "23%" : "31%", flexGrow: 1 },
                    ]}
                    onPress={() => router.navigate({ pathname: "/(tabs)/project", params: { dir: repo.path } } as any)}
                  >
                    <View style={s.repoCardRow}>
                      <Ionicons name="git-branch-outline" size={16} color={c.accent} />
                      <Text style={[s.repoCardName, { color: c.textPrimary }]} numberOfLines={1}>
                        {repo.name}
                      </Text>
                    </View>
                    {repo.isMonorepo ? (
                      <Text style={[s.repoCardBranch, { color: c.textMuted }]} numberOfLines={1}>
                        monorepo
                      </Text>
                    ) : null}
                  </Pressable>
                ))}
              </View>
            ) : (
              <ScrollView
                horizontal
                showsHorizontalScrollIndicator={false}
                contentContainerStyle={s.reposRow}
              >
                {repos.map((repo) => (
                  <Pressable
                    key={repo.path}
                    style={[s.repoCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
                    onPress={() => router.navigate({ pathname: "/(tabs)/project", params: { dir: repo.path } } as any)}
                  >
                    <View style={s.repoCardRow}>
                      <Ionicons name="git-branch-outline" size={16} color={c.accent} />
                      <Text style={[s.repoCardName, { color: c.textPrimary }]} numberOfLines={1}>
                        {repo.name}
                      </Text>
                    </View>
                    {repo.isMonorepo ? (
                      <Text style={[s.repoCardBranch, { color: c.textMuted }]} numberOfLines={1}>
                        monorepo
                      </Text>
                    ) : null}
                  </Pressable>
                ))}
              </ScrollView>
            )}
          </View>
        )}

        {/* Search + Projects list. A search field over an empty list is dead
            chrome — it can only ever return nothing. Show it once there's
            something to filter (or a query still in the box to clear). */}
        {projects.length > 0 || search.length > 0 ? (
          <View style={[s.searchRow, { backgroundColor: c.bgInput, borderColor: isDark ? "transparent" : c.borderSubtle, borderWidth: isDark ? 0 : 1 }]}>
            <Ionicons name="search" size={16} color={c.textMuted} />
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
                <Ionicons name="close" size={16} color={c.textMuted} />
              </Pressable>
            )}
          </View>
        ) : null}

        {/* Category + framework filter chips */}
        {(() => {
          const categories = new Map<string, number>();
          projects.forEach((p) => {
            const cat = getProjectCategory(p.framework);
            categories.set(cat, (categories.get(cat) || 0) + 1);
          });
          const categoryOrder = ["mobile", "web", "other"] as const;
          const categoryLabels: Record<string, string> = { mobile: "Mobile", web: "Web", other: "Other" };
          // Always show the three category filters (Mobile / Web / Other) so the
          // user can pivot even when the current box only has one kind — the
          // labels are a persistent segmented control, defaulting to Mobile. The
          // count suffix (0 included) doubles as "you have N of these here", so a
          // zero is informative, not dead UI. Only the fully-empty pre-discovery
          // state hides the row.
          const visibleCategories = categoryOrder;
          if (!search.trim() && projects.length === 0) return null;
          return (
            <View style={s.filterWrap}>
            <ScrollView horizontal showsHorizontalScrollIndicator={false} style={s.filterRow} contentContainerStyle={s.filterRowContent}>
              <Pressable
                style={[
                  s.filterChip,
                  {
                    backgroundColor: !activeFilter ? c.accent + "1f" : c.bgInput,
                    borderColor: !activeFilter ? c.accent + "60" : isDark ? "transparent" : c.borderSubtle,
                  },
                ]}
                onPress={() => setActiveFilter(null)}
              >
                <Text style={[s.filterChipText, { color: !activeFilter ? c.accent : c.textSecondary }]}>
                  All
                  <Text style={{ color: c.textMuted }}>{` · ${projects.length}`}</Text>
                </Text>
              </Pressable>
              {visibleCategories.map((cat) => (
                (() => {
                  const chipColor = cat === "mobile" ? c.accent : cat === "web" ? c.success : c.textSecondary;
                  return (
                    <Pressable
                      key={cat}
                      style={[
                        s.filterChip,
                        {
                          backgroundColor: activeFilter === cat ? chipColor + "1f" : c.bgInput,
                          borderColor: activeFilter === cat ? chipColor + "60" : isDark ? "transparent" : c.borderSubtle,
                        },
                      ]}
                      onPress={() => setActiveFilter(activeFilter === cat ? null : cat)}
                    >
                      <Text style={[s.filterChipText, { color: activeFilter === cat ? chipColor : c.textSecondary }]}>
                        {categoryLabels[cat]}
                        <Text style={{ color: c.textMuted }}>{` · ${categories.get(cat) ?? 0}`}</Text>
                      </Text>
                    </Pressable>
                  );
                })()
              ))}
            </ScrollView>
            <View pointerEvents="none" style={[s.filterFade, { backgroundColor: c.bg }]} />
            </View>
          );
        })()}

        <FlatList
          // Tablets get a 2-col project grid (per the `projects` token);
          // phone stays single column. Repos list above keeps its own
          // 3/4-col `repos` token — they were sharing it before, which
          // crowded long monorepo names against the chevron edge.
          // Re-mount when column count changes — FlatList rejects mid-flight changes.
          key={`projects-cols-${layout.gridCols("projects")}`}
          numColumns={layout.gridCols("projects")}
          columnWrapperStyle={layout.gridCols("projects") > 1 ? { gap: 10 } : undefined}
          refreshControl={
            <RefreshControl refreshing={pullRefreshing} onRefresh={onPullRefresh} tintColor={c.accent} colors={[c.accent]} progressBackgroundColor={c.bgCard} />
          }
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
          contentContainerStyle={[s.listContent, layout.gridCols("repos") > 1 ? null : tabletContent]}
          renderItem={({ item }) => {
            const isRunning = devStatus?.workDir === item.path;
            const isStarting = startingProject === item.name;
            const cols = layout.gridCols("repos");

            return (
              <Pressable
                style={[s.card, s.projectCard, { backgroundColor: c.bgCard, borderColor: c.borderSubtle },
                  !isDark && { shadowColor: c.shadowSm },
                  cols > 1 ? { flex: 1, maxWidth: `${100 / cols}%` } : null,
                  isRunning && { borderColor: c.accent, borderWidth: 1.5 }]}
                onPress={() => handleTapProject(item)}
                disabled={isStarting || loadingActions}
              >
                <View style={s.cardHeader}>
                  <View style={s.frameworkIcon}>
                    <FrameworkIcon framework={item.framework} size={22} />
                  </View>
                  <View style={s.cardTitleContainer}>
                    {(() => {
                      // "carrotbet / mobile" reads as a clumsy path fragment in
                      // the title. Split the trailing "/ <subdir>" out and show
                      // it as a chip next to the framework — same visual weight
                      // as the "expo"/"flutter" tag.
                      const [repoTitle, subdir] = splitProjectName(item.name);
                      return (
                        <>
                          <Text style={[s.projectName, { color: c.textPrimary }]}>{repoTitle}</Text>
                          {(subdir || item.framework) && (
                            <View style={s.tagRow}>
                              {subdir ? (
                                <View style={[s.tag, { backgroundColor: c.bgInput, borderColor: isDark ? "transparent" : c.borderSubtle }]}>
                                  <Text style={[s.tagText, { color: c.textSecondary }]}>{subdir}</Text>
                                </View>
                              ) : null}
                              {item.framework ? (
                                <View style={[s.tag, { backgroundColor: c.bgInput, borderColor: isDark ? "transparent" : c.borderSubtle }]}>
                                  <Text style={[s.tagText, { color: c.textSecondary }]}>{item.framework}</Text>
                                </View>
                              ) : null}
                            </View>
                          )}
                        </>
                      );
                    })()}
                    {/* No branch line. A card is name + framework + path;
                        the branch is the same on nearly every row, so it read
                        as noise rather than information. */}
                    <Text
                      style={[
                        s.projectPath,
                        { color: c.textTertiary, fontFamily: Platform.OS === "ios" ? "SF Mono" : "monospace" },
                      ]}
                      numberOfLines={1}
                    >
                      {item.path}
                    </Text>
                  </View>
                  {isStarting ? (
                    <ActivityIndicator size="small" color={c.accent} />
                  ) : isRunning ? (
                    <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Running</Text>
                  ) : (
                    <Ionicons name="chevron-forward" size={16} color={c.textMuted} />
                  )}
                </View>
              </Pressable>
            );
          }}
          ListEmptyComponent={
            // Three genuinely different dead-ends, not one card with mutable
            // text: no machine to scan / a search that matched nothing / a
            // machine with no projects on it. Only the last one can honestly
            // offer "Rediscover".
            !activeDevice ? (
              <NoMachineEmpty noun="projects" />
            ) : search.trim() ? (
              <EmptyState
                icon="search-outline"
                title="No matches"
                body={`Nothing named “${search.trim()}” on ${activeDevice.name || "this machine"}.`}
                action={{ label: "Clear search", onPress: () => setSearch("") }}
              />
            ) : (
              <EmptyState
                icon="folder-open-outline"
                busy={projectsDiscovering}
                title={projectsDiscovering ? "Scanning…" : "No projects yet"}
                body={
                  projectsDiscovering
                    ? `Looking through the home directory on ${activeDevice.name || "your machine"}.`
                    : `Yaver found nothing to build on ${activeDevice.name || "this machine"}.`
                }
                action={
                  projectsDiscovering
                    ? undefined
                    : {
                        label: "Scan again",
                        onPress: async () => {
                          try {
                            setProjectsDiscovering(true);
                            await quicClient.refreshMobileProjects();
                          } catch {}
                        },
                      }
                }
              />
            )
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
            {actionSheet?.compatibility?.guidance ? (
              <Text style={[s.actionSheetSubtitle, { color: actionSheet.compatibility.compatible ? "#cbd5e1" : "#fbbf24", marginTop: -8 }]}>
                {actionSheet.compatibility.guidance}
              </Text>
            ) : secondClassGuidance(
              actionSheet?.actions.find((a) => isSecondClassMobileFramework(a.framework))?.framework,
              isDirectConnection,
            ) ? (
              <Text style={[s.actionSheetSubtitle, { color: "#cbd5e1", marginTop: -8 }]}>
                {secondClassGuidance(
                  actionSheet?.actions.find((a) => isSecondClassMobileFramework(a.framework))?.framework,
                  isDirectConnection,
                )}
              </Text>
            ) : agentFlowGuidance(
              actionSheet?.actions.find((a) => a.framework === "expo" || a.framework === "react-native")?.framework
            ) ? (
              <Text style={[s.actionSheetSubtitle, { color: "#cbd5e1", marginTop: -8 }]}>
                {agentFlowGuidance(
                  actionSheet?.actions.find((a) => a.framework === "expo" || a.framework === "react-native")?.framework
                )}
              </Text>
            ) : null}
            {!!actionSheet?.compatibility?.missingModules?.length && (
              <Text style={[s.actionSheetSubtitle, { color: "#fca5a5", marginTop: -8 }]}>
                Missing in Yaver: {actionSheet.compatibility.missingModules.slice(0, 4).join(", ")}
                {actionSheet.compatibility.missingModules.length > 4 ? ` +${actionSheet.compatibility.missingModules.length - 4} more` : ""}
              </Text>
            )}
            {!!actionSheet?.compatibility?.errors?.length && (
              <Text style={[s.actionSheetSubtitle, { color: "#fca5a5", marginTop: -8 }]}>
                {actionSheet.compatibility.errors[0]}
              </Text>
            )}
            {!!actionSheet?.compatibility?.warnings?.length && !actionSheet?.compatibility?.errors?.length && (
              <Text style={[s.actionSheetSubtitle, { color: "#fcd34d", marginTop: -8 }]}>
                {actionSheet.compatibility.warnings[0]}
              </Text>
            )}
            {actionSheet?.compatibility?.projectReactNative && actionSheet?.compatibility?.sdkReactNative && (
              <Text style={[s.actionSheetSubtitle, { color: "#94a3b8", marginTop: -8 }]}>
                RN {actionSheet.compatibility.projectReactNative} · Yaver RN {actionSheet.compatibility.sdkReactNative}
              </Text>
            )}
            {buildStateLabel(actionSheet?.compatibility) ? (
              <Text style={[s.actionSheetSubtitle, { color: buildStateTone(actionSheet?.compatibility), marginTop: -8 }]}>
                {buildStateLabel(actionSheet?.compatibility)}
                {actionSheet?.compatibility?.compiledBundleSize
                  ? ` · ${Math.round(actionSheet.compatibility.compiledBundleSize / 1024)} KB`
                  : ""}
                {actionSheet?.compatibility?.compiledModuleName
                  ? ` · ${actionSheet.compatibility.compiledModuleName}`
                  : ""}
              </Text>
            ) : null}
            {actionSheet?.compatibility?.lastBuildError && actionSheet.compatibility.buildState === "build_failed" ? (
              <Text style={[s.actionSheetSubtitle, { color: "#fca5a5", marginTop: -8 }]}>
                {actionSheet.compatibility.lastBuildError}
              </Text>
            ) : null}
            {actionSheet?.compatibility?.packageManager ? (
              <Text style={[s.actionSheetSubtitle, { color: "#94a3b8", marginTop: -8 }]}>
                {actionSheet.compatibility.packageManager}
                {actionSheet.compatibility.needsDependencyInstall
                  ? actionSheet.compatibility.canAutoInstallDependencies
                    ? " · deps will auto-install on first build"
                    : " · deps missing"
                  : " · deps ready"}
                {actionSheet.compatibility.hermesCompiler ? ` · hermesc ${actionSheet.compatibility.hermesCompiler}` : ""}
              </Text>
            ) : null}
            {!!actionSheet?.compatibility?.missingLocalTools?.length && (
              <Text style={[s.actionSheetSubtitle, { color: "#fca5a5", marginTop: -8 }]}>
                Missing on machine: {actionSheet.compatibility.missingLocalTools.join(", ")}
              </Text>
            )}
            {actionSheet?.compatibility?.hermesCompilerError ? (
              <Text style={[s.actionSheetSubtitle, { color: "#fca5a5", marginTop: -8 }]}>
                {actionSheet.compatibility.hermesCompilerError}
              </Text>
            ) : null}
            <ScrollView style={s.actionSheetScroll}>
              {/* Tapping a project is only about rendering it — the reload lanes.
                  Tests (and build/deploy) are driven by vibing text to the agent. */}
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
          <AppScreenHeader
            title="Vibing"
            onBack={() => { setVibingState(null); setCustomTask(""); setVibingTaskStatus(""); setVibingTaskId(null); }}
            style={{ paddingTop: insets.top + 8 }}
          />
          {vibingState?.project ? (
            <View style={{ alignItems: "center", paddingTop: 8 }}>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>{vibingState.project}</Text>
            </View>
          ) : null}

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
                        if (result.taskId) {
                          setVibingTaskId(result.taskId);
                          setVibingTaskStatus(`Running: ${sg.label}`);
                        } else if (result.runtimeDeploy) {
                          setVibingTaskId(null);
                          setVibingTaskStatus(describeRuntimeDeployResult(result));
                        }
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
                  style={[
                    s.vibingGridItem,
                    { backgroundColor: c.bgCard, borderColor: c.border },
                    layout.layoutClass === "tablet-portrait" ? { width: "31%" } : null,
                    layout.layoutClass === "tablet-landscape" ? { width: "23%" } : null,
                  ]}
                  onPress={async () => {
                    try {
                      const result = await quicClient.executeVibingSuggestion(qa.prompt, vibingState!.path);
                      if (result.taskId) {
                        setVibingTaskId(result.taskId);
                        setVibingTaskStatus(`Running: ${qa.label}`);
                      } else if (result.runtimeDeploy) {
                        setVibingTaskId(null);
                        setVibingTaskStatus(describeRuntimeDeployResult(result));
                      }
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
                    if (result.taskId) {
                      setVibingTaskId(result.taskId);
                      setVibingTaskStatus(`Running: ${customTask.slice(0, 40)}`);
                    } else if (result.runtimeDeploy) {
                      setVibingTaskId(null);
                      setVibingTaskStatus(describeRuntimeDeployResult(result));
                    }
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
          <AppScreenHeader
            title={(runningProject || "Preview").split(" / ")[0]}
            onBack={() => setShowWebView(false)}
            style={{ paddingTop: insets.top + 8 }}
            right={
              <View style={s.webViewHeaderActions}>
                <Pressable onPress={handleReload} hitSlop={8}>
                  <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>Reload</Text>
                </Pressable>
                <Pressable onPress={handleStop} hitSlop={8}>
                  <Text style={{ color: c.error, fontSize: 14, fontWeight: "600", marginLeft: 16 }}>Stop</Text>
                </Pressable>
              </View>
            }
          />
          {webViewLoading && !webPreviewContentLoaded && (
            <View style={[s.loadingBar, { backgroundColor: c.accent }]} />
          )}
          <View style={{ flex: 1 }}>
            <WebView
              ref={webViewRef}
              key={webViewKey}
              source={{ uri: bundleUrl }}
              style={{ flex: 1, backgroundColor: c.bg }}
              onLoadStart={() => { webPreviewErroredRef.current = false; }}
              onLoadEnd={() => {
                setWebViewLoading(false);
                if (!webPreviewErroredRef.current) webPreviewRetryRef.current = 0;
              }}
              // Cold start: a Flutter/expo/vite web server takes 10-60s to compile
              // and bind. Until then the agent's /dev/ proxy returns 503 or refuses
              // the connection. Auto-retry (~30×2.5s ≈ 75s) instead of a dead page.
              onHttpError={(e) => {
                if (e.nativeEvent.statusCode >= 500) scheduleWebPreviewRetry();
              }}
              onError={() => scheduleWebPreviewRetry()}
              // Confirm real paint before hiding the overlay (Flutter index.html
              // 200s then renders black while CanvasKit boots / assets 404).
              injectedJavaScript={`(function(){try{var s=false;function ok(){if(s)return true;var b=document.body;var bt=(b&&b.innerText||'').trim();if(bt.indexOf('"status":"starting"')>=0||bt.indexOf('did not become ready')>=0){return false;}var f=document.querySelector('flutter-view,flt-glass-pane,flt-scene-host');var d=b&&(b.children.length>1||bt.length>0);if(f||d){s=true;if(window.ReactNativeWebView)window.ReactNativeWebView.postMessage(JSON.stringify({t:'yaver-rendered'}));return true;}return false;}if(!ok()){var n=0,iv=setInterval(function(){n++;if(ok()||n>120)clearInterval(iv);},500);}}catch(e){}return true;})();`}
              onMessage={(e) => {
                try {
                  const m = JSON.parse(e.nativeEvent.data);
                  if (m && m.t === "yaver-rendered") {
                    setWebPreviewContentLoaded(true);
                    setWebPreviewFailed(false);
                    webPreviewRetryRef.current = 0;
                  }
                } catch { /* not ours */ }
              }}
              javaScriptEnabled
              domStorageEnabled
              allowsInlineMediaPlayback
            />
            {!webPreviewContentLoaded && (
              <View style={s.previewOverlay}>
                {webPreviewFailed ? (
                  <>
                    <Ionicons name="alert-circle-outline" size={40} color={c.error} />
                    <Text style={[s.previewFailTitle, { color: c.error }]}>Dev server didn't come up</Text>
                    <Text style={s.previewStepCmd}>{devServerStepsFor(devStatus?.framework)}</Text>
                    <Text style={[s.previewSubtle, { color: c.textMuted }]}>
                      The {devStatus?.framework || "web"} server never served content. Recent output:
                    </Text>
                    <ScrollView style={s.previewLogBox} contentContainerStyle={{ padding: 10 }}>
                      {(webPreviewLogs.length ? webPreviewLogs : ["No output captured — the server may have exited immediately, or was never started."]).slice(-40).map((ln, i) => (
                        <Text key={i} style={s.previewLogLine}>{ln}</Text>
                      ))}
                    </ScrollView>
                    <View style={s.previewFailBtns}>
                      <Pressable onPress={() => { resetWebPreview(); setWebViewLoading(true); setWebViewKey((k) => k + 1); }} style={[s.previewBtn, { backgroundColor: "#1a2e1a" }]}>
                        <Text style={[s.previewBtnText, { color: "#22c55e" }]}>Retry</Text>
                      </Pressable>
                      <Pressable onPress={handleReload} style={[s.previewBtn, { backgroundColor: "#1a1a2e" }]}>
                        <Text style={[s.previewBtnText, { color: "#818cf8" }]}>Restart server</Text>
                      </Pressable>
                    </View>
                  </>
                ) : (
                  <>
                    <ActivityIndicator size="large" color={c.accent} />
                    <Text style={[s.previewStartTitle, { color: c.textPrimary }]}>
                      Starting {devStatus?.framework || "web"} dev server…
                    </Text>
                    <Text style={s.previewStepCmd}>{devServerStepsFor(devStatus?.framework)}</Text>
                    <Text style={[s.previewSubtle, { color: c.textMuted }]}>
                      {loadingStatus || "First web compile can take up to a minute — retrying automatically."}
                    </Text>
                    {webPreviewLogs.length > 0 ? (
                      <ScrollView style={s.previewLogBox} contentContainerStyle={{ padding: 10 }}>
                        {webPreviewLogs.slice(-40).map((ln, i) => (
                          <Text key={i} style={s.previewLogLine}>{ln}</Text>
                        ))}
                      </ScrollView>
                    ) : bundlerLine ? (
                      <Text style={[s.previewSubtle, { color: c.textMuted }]} numberOfLines={2}>{bundlerLine}</Text>
                    ) : null}
                  </>
                )}
              </View>
            )}
          </View>
        </View>
      </Modal>
    </SafeAreaView>
  );
}

// ── Styles ─────────────────────────────────────────────────────────

const s = StyleSheet.create({
  safe: { flex: 1 },
  container: { flex: 1 },
  webPreviewStarting: { flex: 1, justifyContent: "center", alignItems: "center", gap: 10, padding: 24 },
  webPreviewStartingText: { fontSize: 15, fontWeight: "600", textAlign: "center" },
  webPreviewStartingSub: { fontSize: 12, textAlign: "center", lineHeight: 17 },
  previewOverlay: {
    position: "absolute", top: 0, left: 0, right: 0, bottom: 0,
    backgroundColor: "#050508",
    alignItems: "center", justifyContent: "center", gap: 10, padding: 24,
  },
  previewStartTitle: { fontSize: 16, fontWeight: "700", textAlign: "center" },
  previewFailTitle: { fontSize: 17, fontWeight: "700", textAlign: "center" },
  previewStepCmd: {
    fontFamily: "Menlo", fontSize: 12, color: "#22c55e",
    backgroundColor: "#0f1a0f", borderColor: "#22c55e33", borderWidth: 1,
    borderRadius: 8, paddingHorizontal: 10, paddingVertical: 6, overflow: "hidden",
  },
  previewSubtle: { fontSize: 12, textAlign: "center", lineHeight: 17 },
  previewLogBox: {
    maxHeight: 180, width: "100%", marginTop: 6, borderRadius: 10,
    backgroundColor: "#0a0a0f", borderWidth: 1, borderColor: "#333",
  },
  previewLogLine: { fontFamily: "Menlo", fontSize: 10.5, color: "#9ca3af", lineHeight: 15 },
  previewFailBtns: { flexDirection: "row", gap: 12, marginTop: 8 },
  previewBtn: { paddingHorizontal: 22, paddingVertical: 11, borderRadius: 10 },
  previewBtnText: { fontSize: 14, fontWeight: "700" },

  // Repos row (monorepo roots + standalone repos)
  reposSection: { marginTop: 12, marginBottom: 4 },
  reposHeader: {
    fontSize: 11,
    fontWeight: "700",
    letterSpacing: 0.5,
    textTransform: "uppercase",
    marginHorizontal: 16,
    marginBottom: 6,
  },
  reposRow: { paddingHorizontal: 16, paddingBottom: 4, gap: 8 },
  repoCard: {
    minWidth: 140,
    maxWidth: 220,
    paddingHorizontal: 12,
    paddingVertical: 10,
    borderRadius: 10,
    borderWidth: 1,
    gap: 4,
  },
  repoCardRow: { flexDirection: "row", alignItems: "center", gap: 6 },
  repoCardName: { fontSize: 13, fontWeight: "600", flex: 1 },
  repoCardBranch: { fontSize: 11, fontFamily: "Menlo" },

  // Search
  searchRow: {
    flexDirection: "row",
    alignItems: "center",
    marginHorizontal: 16,
    marginTop: 12,
    marginBottom: 8,
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 12,
    borderWidth: 1,
    gap: 8,
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.12,
    shadowRadius: 10,
    elevation: 1,
  },
  searchInput: { ...typography.body, flex: 1, paddingVertical: 0 },

  // Filter chips
  filterWrap: { marginHorizontal: 16, marginBottom: 8, position: "relative" },
  filterRow: { height: 30, flexGrow: 0 },
  filterFade: { position: "absolute", right: 0, top: 0, bottom: 0, width: 24, opacity: 0.9 },
  filterRowContent: { gap: 8, alignItems: "center" as const, paddingRight: 8 },
  filterChip: {
    minHeight: 34,
    paddingHorizontal: 14,
    borderRadius: 999,
    borderWidth: 1,
    borderColor: "transparent",
    justifyContent: "center" as const,
  },
  filterChipActive: { borderColor: "#7C66FF" },
  filterChipText: { ...typography.bodyStrong, fontSize: 14, color: "#A8A8B0" },
  filterChipTextActive: { color: "#7C66FF" },

  // Tag chips on cards
  tagRow: { flexDirection: "row", flexWrap: "wrap", gap: 4, marginTop: 3 },
  tag: {
    backgroundColor: "#6366f115",
    borderRadius: 6,
    paddingHorizontal: 8,
    paddingVertical: 3,
    borderWidth: 1,
    borderColor: "transparent",
  },
  tagText: { color: "#818cf8", fontSize: 11, fontWeight: "600" },

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
  card: {
    marginHorizontal: spacing.lg,
    borderRadius: 16,
    paddingHorizontal: spacing.lg,
    paddingVertical: 14,
    marginBottom: spacing.md,
    borderWidth: 0.5,
    ...lightCardShadow,
  },
  activeCard: {
    backgroundColor: "#0f1a0f",
    borderWidth: 1,
    borderColor: "#22c55e44",
    marginTop: 12,
  },
  catalogCard: {
    borderWidth: 1,
    marginTop: 12,
  },
  cardHeader: { flexDirection: "row", alignItems: "center", gap: 10 },
  cardTitleContainer: { flex: 1 },
  cardTitle: { fontSize: 16, fontWeight: "700", color: "#fff" },
  cardMeta: { fontSize: 11, color: "#666", marginTop: 2 },
  guidanceText: { lineHeight: 15, marginTop: 4 },
  statusDot: { width: 8, height: 8, borderRadius: 4 },
  frameworkIcon: {},

  cardActions: { flexDirection: "row", gap: 8, marginTop: 12 },
  actionBtn: { paddingVertical: 8, paddingHorizontal: 14, borderRadius: 8 },
  openBtn: { backgroundColor: "#22c55e", flex: 1, alignItems: "center", flexDirection: "row" as const, justifyContent: "center", gap: 4 },
  openBtnText: { color: "#000", fontSize: 13, fontWeight: "700" },
  reloadBtn: { backgroundColor: "#22c55e22", flex: 1, alignItems: "center" },
  reloadBtnText: { color: "#22c55e", fontSize: 13, fontWeight: "600" },
  stopBtn: { backgroundColor: "#ef444422", paddingHorizontal: 16, alignItems: "center" },
  stopBtnText: { color: "#ef4444", fontSize: 13, fontWeight: "600" },

  // Section
  sectionTitle: { ...typography.badge, textTransform: "uppercase", letterSpacing: 1.2, marginHorizontal: 16, marginTop: 24, marginBottom: 12 },

  // Project cards
  projectCard: { borderWidth: 1 },
  projectName: { ...typography.cardTitle, fontSize: 15, fontWeight: "600" },
  projectMeta: { ...typography.caption, marginTop: 3 },
  projectPath: { ...typography.path, marginTop: 3 },
  listContent: { paddingBottom: 40 },

  // Empty
  emptyContainer: { flex: 1, alignItems: "center", justifyContent: "center", padding: 40 },
  emptyIcon: { fontSize: 40, marginBottom: 12 },
  emptyTitle: { fontSize: 18, fontWeight: "700", marginBottom: 4 },
  emptySubtitle: { fontSize: 13, textAlign: "center", lineHeight: 20 },
  // Empty-list card + CTA now live in the shared, chromeless <EmptyState>.

  // WebView header
  webViewHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingTop: 12, paddingBottom: 10, borderBottomWidth: 1 },
  webViewHeaderCenter: { flexDirection: "row", alignItems: "center", gap: 6 },
  webViewTitle: { fontSize: 15, fontWeight: "700" },
  webViewHeaderActions: { flexDirection: "row", alignItems: "center" },
  loadingBar: { height: 2, opacity: 0.6 },
});
