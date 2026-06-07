import React, { useCallback, useEffect, useRef, useState } from "react";
import { Ionicons } from "@expo/vector-icons";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  NativeModules,
  Platform,
  Pressable,
  RefreshControl,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useRouter } from "expo-router";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { SafeAreaView } from "react-native-safe-area-context";
import { useDevice } from "../../src/context/DeviceContext";
import { useColors, useTheme } from "../../src/context/ThemeContext";
import {
  quicClient,
  type DevServerStatus,
  type IncidentEvent,
  type MobileWorkerPreviewSession,
  type OperationState,
} from "../../src/lib/quic";
import { connectionManager } from "../../src/lib/connectionManager";
import { isBundleLoaded, loadAppIfChanged, onBundleEvent, setPhoneFrame } from "../../src/lib/bundleLoader";
import { FrameworkIcon } from "../../src/components/FrameworkIcon";
import RemoteBoxPickerModal from "../../src/components/RemoteBoxPickerModal";
import RemoteBoxBanner from "../../src/components/RemoteBoxBanner";
import DiscoveryDiagnosticsPanel from "../../src/components/DiscoveryDiagnosticsPanel";
import type { DiagnosticsProbe } from "../../src/lib/discoveryDiagnostics";
import { isEffectivelyConnected as computeEffectiveConnected } from "../../src/lib/connectionState";
import { lightCardShadow, monoFamily, spacing, typography } from "../../src/theme/tokens";
// Guest-crash helpers used to render an inline orange banner in the
// hot-reload card. Banner removed (see jsx below) but the data path is
// kept so a future DeviceDetailsModal section can surface it on tap.
// Once that lands, re-import shouldShowGuestCrashReport / formatGuestCrashReport.
import { shouldShowCurrentReloadIncident, visibleReloadIncidents, visibleReloadOperations } from "../../src/lib/hotReloadState";
import { buildNativeBuildRequest, nativeBuildFailureMessage, nativeBuildFailureTitle } from "../../src/lib/nativeBuild";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";

// ── Types ──────────────────────────────────────────────────────────

interface ProjectItem {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  executionMode?: string;
  primarySurface?: string;
  tags?: string[];
  monorepoRoot?: string;
  monorepoApp?: string;
}

interface RemoteAgentInfo {
  hostname: string;
  version: string;
  workDir: string;
}

const DEV_FRAMEWORKS = ["expo", "flutter", "nextjs", "vite", "react-native", "react"];

// Branded vector icons replace the prior emoji glyph map. Emoji read OK
// for "an apple" but didn't say "Swift on Apple platforms" at a glance,
// and \uD83D\uDFEA looked like a generic purple square instead of Kotlin. The
// FrameworkIcon component renders MaterialCommunityIcons with proper
// brand colors \u2014 see mobile/src/components/FrameworkIcon.tsx.

const PREVIEW_TARGET_KEY = "@yaver/hotreload_preview_target";

function isHermesMobileFramework(framework?: string): boolean {
  return framework === "expo" || framework === "react-native";
}

function isNativeRemoteRuntimeProject(project: ProjectItem): boolean {
  return project.executionMode === "native-webrtc" || project.framework === "swift" || project.framework === "kotlin";
}

function isMobileHotReloadProject(project: ProjectItem): boolean {
  const fw = String(project.framework || "").trim().toLowerCase();
  if (project.executionMode === "native-webrtc" || project.executionMode === "rn-hermes") return true;
  return fw === "expo" || fw === "react-native" || fw === "flutter" || fw === "swift" || fw === "kotlin";
}

function agentFlowGuidance(framework?: string): string | null {
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

// currentYaverGuestCrashReport: removed alongside the inline crash card
// in this view. Native still exposes lastGuestCrashReport via YaverInfo;
// re-add this reader (and the GuestCrashReport import) when surfacing
// the data inside DeviceDetailsModal's tap-to-expand section.

function displayProjectTitle(project: ProjectItem): string {
  return project.name;
}

function describeRuntimeFamilySelection(metadata?: Record<string, unknown> | null): string | null {
  const selection = metadata?.runtimeFamilySelection;
  if (!selection || typeof selection !== "object") return null;
  const selected = (selection as any).selected;
  if (!selected || typeof selected !== "object") return null;
  const label = typeof selected.label === "string" && selected.label.trim()
    ? selected.label.trim()
    : typeof selected.id === "string" && selected.id.trim()
      ? selected.id.trim()
      : "";
  if (!label) return null;
  const exact = (selection as any).exactMatch === true;
  const reason = typeof (selection as any).reason === "string" ? (selection as any).reason.trim() : "";
  if (exact) return `host runtime · ${label}`;
  return reason ? `host runtime · ${label} · ${reason}` : `host runtime · ${label}`;
}

// ── Hot Reload Tab ────────────────────────────────────────────────

// AsyncStorage key for the user's last-picked tablet view mode. Read
// at picker open so the default answer is sticky; written on confirm.
// Values: "phone" (frame in iPhone shell + vibe dock), "tablet"
// (full-width native tablet layout), "" (never picked — show prompt).
const VIEW_MODE_KEY = "@yaver/tablet/view_mode";

export default function HotReloadScreen() {
  const c = useColors();
  const { isDark } = useTheme();
  const router = useRouter();
  const layout = useResponsiveLayout();
  const tabletContent = useTabletContentStyle("wide");
  const projectCols = layout.layoutClass === "phone" ? 1 : layout.layoutClass === "tablet-portrait" ? 2 : 3;
  // Phone-sized type (13-17pt) reads as postage-stamp text on a
  // 1340pt-wide tablet, especially against the larger "Other apps"
  // grid where the eye expects the active card to dominate.
  // Bump type + padding on tablet so the running-project card
  // actually anchors the screen. Untouched on phones.
  const tabletCard = layout.isTablet
    ? {
        cardPadding: { paddingHorizontal: 22, paddingVertical: 20 } as const,
        title: { fontSize: 22, lineHeight: 28 } as const,
        meta: { fontSize: 15, lineHeight: 21 } as const,
        path: { fontSize: 13 } as const,
        metaPillText: { fontSize: 12, letterSpacing: 0.4 } as const,
        statusPillText: { fontSize: 13 } as const,
        projectName: { fontSize: 19, lineHeight: 25 } as const,
        caret: 22,
        frameworkIconSize: 28,
        listGap: 14,
      }
    : null;
  const { activeDevice, connectionStatus, devices, connectedDeviceIds, refreshDevices } = useDevice();
  // Effective connected = focused-device alive OR any pool client live.
  // Used for the in-tab gating logic (showing project list vs. CTA)
  // so this tab no longer disagrees with Devices/Tasks about whether
  // the user is "connected". See lib/connectionState for the rule.
  const effectivelyConnected = computeEffectiveConnected(connectionStatus, connectedDeviceIds);

  // Promise wrapper around Alert.alert so handleOpen / handleStart
  // can `await` the user's pick. Returns `true` if cancelled (caller
  // should bail out), `false` if the user picked phone or tablet
  // (native flag has been written; mount path can proceed). Sticky
  // default uses the last-picked mode so power users don't re-pick
  // every reload.
  const promptViewMode = useCallback((): Promise<boolean> => {
    return new Promise((resolve) => {
      (async () => {
        let last = "";
        try { last = (await AsyncStorage.getItem(VIEW_MODE_KEY)) || ""; } catch { /* ignore */ }
        const apply = async (mode: "phone" | "tablet") => {
          try { await AsyncStorage.setItem(VIEW_MODE_KEY, mode); } catch { /* ignore */ }
          try { await setPhoneFrame(mode === "phone"); } catch { /* native module unavailable on Android */ }
          resolve(false);
        };
        Alert.alert(
          "Open as…",
          last
            ? `Last time you picked "${last === "phone" ? "Phone view" : "Tablet view"}". Pick again or keep it.`
            : "Run this guest as a phone-shaped frame with a vibing dock beside it, or full-width for tablet UI testing?",
          [
            { text: "Cancel", style: "cancel", onPress: () => resolve(true) },
            { text: "Tablet view", onPress: () => { void apply("tablet"); } },
            { text: "Phone view", onPress: () => { void apply("phone"); } },
          ],
          { cancelable: true, onDismiss: () => resolve(true) },
        );
      })();
    });
  }, []);
  const isConnected = connectionStatus === "connected" && !!activeDevice;

  const [devStatus, setDevStatus] = useState<DevServerStatus | null>(null);
  const [workerSession, setWorkerSession] = useState<MobileWorkerPreviewSession | null>(null);
  const [agentInfo, setAgentInfo] = useState<RemoteAgentInfo | null>(null);
  const [projects, setProjects] = useState<ProjectItem[]>([]);
  const [projectsScanning, setProjectsScanning] = useState(false);
  const [pullRefreshing, setPullRefreshing] = useState(false);

  // Pull-to-refresh: re-scan projects on the box + re-poll devices, like the
  // Tasks tab. The list itself updates from the existing poll loop; we just
  // kick a fresh scan and show the spinner briefly.
  const onPullRefresh = useCallback(async () => {
    setPullRefreshing(true);
    try {
      await Promise.allSettled([
        quicClient.refreshMobileProjects(),
        refreshDevices(),
      ]);
      if (connectionStatus === "connected") setProjectsScanning(true);
    } finally {
      setPullRefreshing(false);
    }
  }, [refreshDevices, connectionStatus]);
  const [scanStopping, setScanStopping] = useState(false);
  // Stalled scan detection: a healthy /projects/mobile scan settles in
  // 1-3s. If we're still "discovering" past ~10s, the remote agent is
  // most likely unreachable (port-conflict, daemon crash, network
  // partition) — surface a concrete "check Devices" hint instead of
  // letting the spinner imply progress that isn't happening.
  const scanStartedAtRef = useRef<number | null>(null);
  const [scanStalled, setScanStalled] = useState(false);
  useEffect(() => {
    if (projectsScanning) {
      if (scanStartedAtRef.current == null) scanStartedAtRef.current = Date.now();
      const t = setTimeout(() => setScanStalled(true), 10000);
      return () => clearTimeout(t);
    }
    scanStartedAtRef.current = null;
    setScanStalled(false);
  }, [projectsScanning]);
  const [startingProject, setStartingProject] = useState<string | null>(null);
  // Tail of log lines streamed from /dev/events SSE. Bounded to 40
  // so a chatty Metro bundle doesn't blow up state; the card shows
  // the last ~6. Cleared when a new dev server starts.
  const [devLog, setDevLog] = useState<string[]>([]);
  const [reloadIncidents, setReloadIncidents] = useState<IncidentEvent[]>([]);
  const [reloadOperations, setReloadOperations] = useState<OperationState[]>([]);
  const [showRemoteBoxPicker, setShowRemoteBoxPicker] = useState(false);
  const [showDiagnostics, setShowDiagnostics] = useState(false);
  const [remoteHermesReady, setRemoteHermesReady] = useState<{
    enabled: boolean;
    reason?: string;
    notes?: string[];
  } | null>(null);
  const [selectedTargetId, setSelectedTargetId] = useState<string | null>(null);
  // Stop UX state: "idle" → "stopping" (after Stop tapped) → "stopped" (post
  // /dev/stop with verified=true). Snaps back to "idle" after a 2s success
  // banner so the user gets a clear "agent really stopped" confirmation
  // without a permanent extra UI element. "error" surfaces when the agent
  // returns ok=false (e.g. subprocess didn't die within 7s).
  const [stopState, setStopState] = useState<"idle" | "stopping" | "stopped" | "error">("idle");
  const [stopMessage, setStopMessage] = useState<string>("");
  const [stopBuildsCancelled, setStopBuildsCancelled] = useState<number>(0);
  const mobileWorkers = devices.filter((d) => d.deviceClass === "edge-mobile");
  const selectedTarget = mobileWorkers.find((d) => d.id === selectedTargetId) || null;

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
      .catch(() => {});
    return () => { mounted = false; };
  }, [isConnected, activeDevice?.id]);

  useEffect(() => {
    if (selectedTargetId) {
      AsyncStorage.setItem(PREVIEW_TARGET_KEY, selectedTargetId).catch(() => {});
      return;
    }
    AsyncStorage.removeItem(PREVIEW_TARGET_KEY).catch(() => {});
  }, [selectedTargetId]);

  useEffect(() => {
    if (devStatus?.targetDeviceId) {
      setSelectedTargetId(devStatus.targetDeviceId);
      return;
    }
    if (!devStatus?.running && !devStatus?.building) {
      return;
    }
  }, [devStatus?.targetDeviceId, devStatus?.running, devStatus?.building]);

  // Remote Box switch — clear every piece of state bound to the
  // previous box AND kick a fresh project scan on the new box. Without
  // this, switching from yaver-test-ephemeral (paths like /root/...)
  // to a Mac mini (paths like /Users/...) leaves the previous box's
  // project list visible until the 15-second poll tick happens to fire,
  // which makes the Reload tab look broken even though it's just stale.
  // Triggered on every activeDevice.id transition (and on first attach).
  useEffect(() => {
    if (!activeDevice?.id) return;
    // Snapshot fields read from the previous box's agent — drop them
    // so the user doesn't see a Mac mini's hostname next to a Linux
    // box's project paths during the gap before the new poll lands.
    setProjects([]);
    setScanStopping(false);
    setDevStatus(null);
    setWorkerSession(null);
    setAgentInfo(null);
    setRemoteHermesReady(null);
    setReloadIncidents([]);
    setReloadOperations([]);
    setDevLog([]);
    // Action state from the previous box — a "Building HBC..." spinner
    // for a build that's now happening on a different machine, or a
    // stop-action mid-flight against the prior agent.
    setStartingProject(null);
    setNativeLoading(false);
    setLoadingStatus("");
    setStopState("idle");
    setStopMessage("");
    setStopBuildsCancelled(0);
    // Best-effort scan kick on the new box so the next GET poll
    // returns fresh data within seconds instead of falling back to
    // the cached scan from the agent's last full sweep. Failure is
    // intentionally swallowed — the regular 15s poll catches up.
    //
    // Also flip projectsScanning ON optimistically so the UI shows
    // "Discovering..." immediately AND the polling interval rebuilds
    // at 2.5s (vs 15s for the idle cadence). Without this the user
    // stares at a "No projects discovered — Rediscover" empty-state
    // for up to 15s after switching, which makes the auto-scan look
    // like it didn't fire. The next poll reconciles scanning back to
    // false once the agent reports the scan finished.
    if (isConnected) {
      setProjectsScanning(true);
      void quicClient.refreshMobileProjects().catch(() => {
        setProjectsScanning(false);
      });
    } else {
      setProjectsScanning(false);
    }
  }, [activeDevice?.id]);

  // Poll dev server status + mobile projects
  useEffect(() => {
    if (!isConnected) return;
    let mounted = true;

    const poll = async () => {
      try {
        const [status, session, info, snapshot] = await Promise.all([
          quicClient.getDevServerStatus(),
          quicClient.getMobileWorkerPreviewSession(),
          quicClient.getInfo(),
          quicClient.capabilitySnapshot(),
        ]);
        if (mounted) setDevStatus(status?.running || status?.framework ? status : null);
        if (mounted) setWorkerSession(session);
        if (mounted) setAgentInfo(info);
        if (mounted) {
          const target = snapshot?.targets?.["mobile-hermes"];
          setRemoteHermesReady(
            target
              ? {
                  enabled: !!target.enabled,
                  reason: target.reason,
                  notes: Array.isArray(target.notes) ? target.notes : undefined,
                }
              : null,
          );
        }
      } catch {
        if (mounted) setDevStatus(null);
        if (mounted) setWorkerSession(null);
        if (mounted) setAgentInfo(null);
        if (mounted) setRemoteHermesReady(null);
      }
      try {
        const [operations, incidents] = await Promise.all([
          quicClient.operations({ limit: 12 }),
          quicClient.incidents({ limit: 12 }),
        ]);
        if (!mounted) return;
        setReloadOperations(
          operations.filter((op) => op.kind === "reload" || op.kind === "reload_app" || op.kind === "build_native"),
        );
        setReloadIncidents(
          incidents.filter((incident) => incident.category === "reload" || incident.category === "build"),
        );
      } catch {
        if (!mounted) return;
        setReloadOperations([]);
        setReloadIncidents([]);
      }
    };

    // Fetch mobile projects from dedicated scanner (cached, refreshes in background).
    // Read baseUrl + headers from the device-specific client looked up
    // by activeDevice.id, NOT from the global `quicClient` Proxy. When a
    // user switches device in Devices and the previous device was in a
    // "connecting" state, the Proxy can briefly publish the stale baseUrl
    // (the focused pointer flips but the closure captured baseUrl at
    // effect creation time). Pinning the lookup to activeDevice.id —
    // which IS in this effect's deps — guarantees the fetch hits the
    // agent the user actually picked. If the picked device has no
    // pool client yet, abort the fetch rather than fall through to the
    // Proxy's last-known URL.
    const fetchMobileProjects = async () => {
      try {
        const targetId = activeDevice?.id;
        if (!targetId) return;
        const client = (connectionManager.clientFor(targetId) as any);
        const baseUrl = client?.baseUrl;
        const headers = client?.authHeaders;
        if (!baseUrl || !headers) return;
        const res = await fetch(`${baseUrl}/projects/mobile`, { headers });
        const data = await res.json();
        if (mounted && data.ok && data.projects) {
          setProjectsScanning(!!data.scanning);
          setProjects(data.projects.map((p: any) => ({
            name: p.name,
            path: p.path,
            branch: p.branch,
            framework: p.framework,
            executionMode: p.executionMode,
            primarySurface: p.primarySurface,
            monorepoRoot: p.monorepoRoot,
            monorepoApp: p.monorepoApp,
            tags: [p.framework, p.primarySurface].filter(Boolean),
          })));
        }
      } catch {}
    };

    poll();
    fetchMobileProjects();
    const interval = setInterval(poll, 3000);
    const projectInterval = setInterval(fetchMobileProjects, projectsScanning ? 2500 : 15000);
    return () => { mounted = false; clearInterval(interval); clearInterval(projectInterval); };
    // activeDevice?.id is in the deps so the poll loop tears down +
    // restarts on a Remote Box switch — without it the closure keeps
    // running with the same `mounted` flag and the user has to wait
    // up to 15s for the next interval tick to fetch the new box's data.
  }, [isConnected, projectsScanning, activeDevice?.id]);

  // Subscribe to /dev/events SSE so the user sees Metro / Expo /
  // Flutter output live while the dev server is coming up. Without
  // this, a slow-starting Metro looks like "expo · starting…" with
  // a spinner indefinitely — no signal of progress, no signal of
  // failure. Cap the tail at 40 lines to keep state bounded.
  useEffect(() => {
    if (!isConnected) return;
    setDevLog([]);
    const unsub = quicClient.subscribeDevEvents((ev) => {
      if (ev.type === "log" && ev.logLine) {
        setDevLog((prev) => {
          const next = [...prev, String(ev.logLine)];
          return next.length > 40 ? next.slice(next.length - 40) : next;
        });
      } else if (ev.type === "ready" || ev.type === "stopped") {
        setDevLog([]);
      } else if (ev.type === "error" && ev.message) {
        setDevLog((prev) => {
          const next = [...prev, `[error] ${ev.message}`];
          return next.length > 40 ? next.slice(next.length - 40) : next;
        });
      }
    });
    return () => unsub();
  }, [isConnected, activeDevice?.id]);


  const [nativeLoading, setNativeLoading] = useState(false);
  const [reloadLoading, setReloadLoading] = useState(false);
  const [bundleMounted, setBundleMounted] = useState(false);
  const runningGuidance = agentFlowGuidance(devStatus?.framework);

  useEffect(() => {
    let mounted = true;
    void isBundleLoaded()
      .then((loaded) => {
        if (mounted) setBundleMounted(loaded);
      })
      .catch(() => {
        if (mounted) setBundleMounted(false);
      });
    const loadSub = onBundleEvent("onBundleLoaded", () => setBundleMounted(true));
    const unloadSub = onBundleEvent("onBundleUnloaded", () => setBundleMounted(false));
    return () => {
      mounted = false;
      loadSub.remove();
      unloadSub.remove();
    };
  }, []);

  const handleOpen = useCallback(async () => {
    // Loading a guest app inside the Yaver container needs the native
    // YaverBundleLoader module, which only ships on iOS today. Stop here on
    // Android with a clear explanation rather than building a bundle the
    // phone can't mount.
    if (Platform.OS === "android") {
      Alert.alert(
        "iOS-Only For Now",
        "Loading apps inside Yaver is iOS-only today. Use an iPhone or iPad to open this app in Yaver. Android support is in progress.",
      );
      return;
    }
    const baseUrl = (quicClient as any).baseUrl as string;
    if (!baseUrl) {
      Alert.alert(
        "Dev Machine Not Connected",
        "Yaver isn't connected to your dev machine. Check your connection on the Devices tab and try again.",
      );
      return;
    }

    // Tablet: ask whether to mount in a phone-shaped frame (with vibe
    // dock alongside) or render the guest at full tablet width (for
    // testing the guest's own tablet layout). The choice persists via
    // AsyncStorage so the next reload skips the prompt unless the user
    // wants to switch — see VIEW_MODE_KEY below. Phones bypass entirely.
    if (layout.isTablet) {
      const cancelled = await promptViewMode();
      if (cancelled) return;
    }

    setNativeLoading(true);
    setLoadingStatus("Building HBC bundle...");
    // Agent caps Metro bundle at 8 min and hermesc at 3 min, so the worst
    // case is ~11 min. Give the client 12 min before aborting — anything
    // longer means the agent is unreachable, not slow. Without an abort
    // the fetch can hang forever on a crashed agent or dead relay, leaving
    // the Hot Reload UI stuck on "Building HBC bundle...".
    const buildAbort = new AbortController();
    const buildAbortTimer = setTimeout(() => buildAbort.abort(), 12 * 60 * 1000);
    try {
      const headers = {
        ...(quicClient as any).authHeaders,
        "Content-Type": "application/json",
      };

      // Step 1: Build production Hermes bytecode bundle (embedded hermesc BC96)
      const platform = (Platform.OS as string) === "android" ? "android" : "ios";
      // Pin the request to the dev server's active project so the agent
      // (≥ 1.99.187) doesn't reject with PROJECT_REQUIRED. devStatus.workDir
      // is the path of whatever Metro is serving — same project the user
      // is viewing in this tab. Without this pin the agent refuses the
      // build to avoid letting an unrelated dev server (e.g. a Vite worker
      // started by another caller) dictate which project the Hermes bundle
      // gets built from.
      const projectPath = devStatus?.workDir ? String(devStatus.workDir).trim() : "";
      const buildRes = await fetch(`${baseUrl}/dev/build-native`, {
        method: "POST",
        headers,
        body: JSON.stringify(
          buildNativeBuildRequest(
            platform,
            currentYaverConsumerContract(),
            projectPath ? { projectPath } : undefined,
          ),
        ),
        signal: buildAbort.signal,
      });
      clearTimeout(buildAbortTimer);

      // Two failure modes share this branch:
      //   (1) HTTP 5xx + application/json — the agent's structured build
      //       failure. The body is {error, phase, command, workDir,
      //       output (last 120 lines stderr), helpHint, ...}. Parse it,
      //       attach to the error so the outer Alert can call
      //       nativeBuildFailureTitle/Message and surface the real reason.
      //       Truncating it to 240 chars used to hide everything except
      //       the command line — the user saw "HTTP 500 — {command:[...]}"
      //       and had no clue what actually failed.
      //   (2) Non-JSON body (text/plain or HTML) — relay or proxy error.
      //       Most common: 502 "tunnel read error" when the relay's HTTP
      //       wait window expires while the bundler is still running.
      const ct = String(buildRes.headers.get("content-type") || "");
      if (!buildRes.ok) {
        if (ct.includes("application/json")) {
          let parsed: any = null;
          try { parsed = await buildRes.json(); } catch {}
          if (parsed) {
            const error = new Error(nativeBuildFailureMessage(parsed));
            (error as any).buildResult = parsed;
            throw error;
          }
        }
        const text = await buildRes.text();
        const trimmed = text.length > 240 ? text.slice(0, 240) + "…" : text;
        const reason = trimmed || `HTTP ${buildRes.status}`;
        if (buildRes.status === 502 && /tunnel read error/i.test(text)) {
          throw new Error(`Bundle build is still running on the agent — the relay closed our HTTP wait window before the build finished. The build may have completed; check the agent logs and re-tap to load. Detail: ${reason}`);
        }
        throw new Error(`Bundle build failed: HTTP ${buildRes.status} ${ct ? `(${ct})` : ""} — ${reason}`);
      }
      if (!ct.includes("application/json")) {
        const text = await buildRes.text();
        const trimmed = text.length > 240 ? text.slice(0, 240) + "…" : text;
        throw new Error(`Bundle build returned non-JSON response — ${ct || "unknown content-type"} — ${trimmed || "(empty)"}`);
      }
      const buildResult = await buildRes.json();

      if (buildResult.status !== "ok") {
        const error = new Error(nativeBuildFailureMessage(buildResult));
        (error as any).buildResult = buildResult;
        throw error;
      }

      const sizeKB = Math.round((buildResult.size || 0) / 1024);
      const familySelection = buildResult.runtimeFamilySelection;
      const familyLabel = familySelection?.selected?.label || familySelection?.selected?.id || "";
      setLoadingStatus(
        familySelection?.exactMatch && familyLabel
          ? `Built ${sizeKB}KB BC${buildResult.bcVersion || "?"} · matched ${familyLabel}`
          : `Built ${sizeKB}KB BC${buildResult.bcVersion || "?"}${familyLabel ? ` · closest ${familyLabel}` : ""}`,
      );

      // Step 2: Download assets if available
      if (buildResult.hasAssets && buildResult.assetsUrl) {
        setLoadingStatus("Downloading assets...");
        try {
          const assetsRes = await fetch(`${baseUrl}${buildResult.assetsUrl}`, { headers });
          if (assetsRes.ok) {
            const assetsBlob = await assetsRes.blob();
            await fetch(`http://localhost:8347/assets`, {
              method: "POST",
              body: assetsBlob,
              headers: { "Content-Type": "application/zip" },
            });
          }
        } catch {
          // Non-fatal
        }
      }

      // Step 3: Download + validate HBC bundle (integrity checked via X-Yaver-Bundle-Metadata).
      // loadAppIfChanged short-circuits when buildResult.md5 matches the
      // bundle already running on the bridge — no download, no bridge
      // teardown, ~50ms instead of ~1-3s.
      setLoadingStatus(`Downloading ${sizeKB}KB bundle...`);
      const bundleUrl = `${baseUrl}${buildResult.bundleUrl}`;
      const moduleName = buildResult.moduleName || "main";
      const loadResult = await loadAppIfChanged(
        bundleUrl,
        moduleName,
        buildResult.md5,
        (quicClient as any).authHeaders,
      );

      // If we get here, bundle was validated (MD5 + BC version match)
      const loadedFamilyLabel = familySelection?.selected?.label || familySelection?.selected?.id || "";
      const md5Short = (buildResult.md5 || "").slice(0, 8);
      setLoadingStatus(
        loadResult.skipped
          ? `Already up to date${loadedFamilyLabel ? ` · ${loadedFamilyLabel}` : ""} · MD5: ${md5Short}...`
          : `Loaded${loadedFamilyLabel ? ` · ${loadedFamilyLabel}` : ""}! MD5: ${md5Short}...`,
      );
      setBundleMounted(true);
    } catch (err: any) {
      clearTimeout(buildAbortTimer);
      setLoadingStatus("");
      const aborted = err?.name === "AbortError" || buildAbort.signal.aborted;
      const message = aborted
        ? "Build did not respond in 12 minutes. The agent may be stuck or unreachable — check the project's node_modules and retry."
        : err?.message || "Could not load bundle in Yaver";
      const title = aborted
        ? "Build Timed Out"
        : err?.buildResult ? nativeBuildFailureTitle(err.buildResult) : "Load Failed";
      Alert.alert(title, message);
    } finally {
      setNativeLoading(false);
    }
  }, [devStatus]);

  const handleReload = useCallback(async () => {
    if (reloadLoading || nativeLoading) return;
    const framework = String(devStatus?.framework || "").trim().toLowerCase();
    const isHermesFramework = framework === "expo" || framework === "react-native";
    if (isHermesFramework && !bundleMounted) {
      await handleOpen();
      return;
    }
    setReloadLoading(true);
    setLoadingStatus(isHermesFramework ? "Preparing fresh bundle..." : "Sending reload command...");
    try {
      const ok = await quicClient.reloadDevServer({ mode: isHermesFramework ? "bundle" : "dev" });
      if (!ok) {
        setLoadingStatus("");
        Alert.alert("Reload failed", "Could not send reload to the running app.");
        return;
      }
      if (!isHermesFramework) {
        setLoadingStatus("Reload sent.");
      }
    } finally {
      setReloadLoading(false);
    }
  }, [bundleMounted, devStatus?.framework, handleOpen, nativeLoading, reloadLoading]);

  const handleStop = useCallback(() => {
    Alert.alert("Stop Dev Server", "Stop the running dev server?", [
      { text: "Cancel", style: "cancel" },
      {
        text: "Stop", style: "destructive", onPress: async () => {
          // Optimistic "Stopping…" pill so the user sees the agent is
          // working immediately. The card stays visible during this
          // window — no flicker into the project list and back out.
          setStopState("stopping");
          setStopMessage("");
          setStopBuildsCancelled(0);
          const res = await quicClient.stopDevServer();
          setDevStatus(null);
          if (!res || res.ok === false) {
            setStopState("error");
            setStopMessage(res?.error || res?.message || "Stop failed — check agent logs.");
            // Auto-dismiss the error banner after 5s so it doesn't pin
            // forever and confuse the next interaction.
            setTimeout(() => setStopState("idle"), 5000);
            return;
          }
          // Successful stop. agent 1.99.93+ returns verified + buildsCancelled.
          // Surface buildsCancelled when >0 so the user knows the in-flight
          // Hermes build was actually aborted (not just the dev server).
          setStopState("stopped");
          setStopBuildsCancelled(res.buildsCancelled || 0);
          if (res.verified === false) {
            setStopMessage("Sent SIGINT + SIGKILL but subprocess didn't confirm exit in 7s.");
            setStopState("error");
            setTimeout(() => setStopState("idle"), 5000);
            return;
          }
          // 2s success banner, then back to idle.
          setTimeout(() => {
            setStopState("idle");
            setStopMessage("");
            setStopBuildsCancelled(0);
          }, 2000);
        }
      },
    ]);
  }, []);

  const handleStopDiscovery = useCallback(async () => {
    try {
      setScanStopping(true);
      const baseUrl = (quicClient as any).baseUrl;
      const headers = (quicClient as any).authHeaders;
      await fetch(`${baseUrl}/projects/mobile`, { method: "DELETE", headers });
      setProjectsScanning(false);
    } catch {
      Alert.alert("Stop failed", "Could not stop project discovery right now.");
    } finally {
      setScanStopping(false);
    }
  }, []);

  // Build a device-pinned probe for the diagnostics panel. Read baseUrl +
  // headers from the active device's pool client (same rule as
  // fetchMobileProjects) so the preflight hits the box the user actually
  // picked, not the global proxy's last-known URL.
  const buildDiagnosticsProbe = useCallback((): DiagnosticsProbe | null => {
    const targetId = activeDevice?.id;
    if (!targetId) return null;
    const client = connectionManager.clientFor(targetId) as any;
    const baseUrl = client?.baseUrl;
    const authHeaders = client?.authHeaders;
    if (!baseUrl || !authHeaders) return null;
    return {
      baseUrl,
      authHeaders,
      host: agentInfo?.hostname || activeDevice?.name || "this device",
    };
  }, [activeDevice?.id, activeDevice?.name, agentInfo?.hostname]);

  const kickRescan = useCallback(async () => {
    try {
      setProjectsScanning(true);
      const probe = buildDiagnosticsProbe();
      if (!probe) return;
      await fetch(`${probe.baseUrl}/projects/mobile`, { method: "POST", headers: probe.authHeaders });
    } catch {}
  }, [buildDiagnosticsProbe]);

  const handleSelectTarget = useCallback(async (deviceId: string | null) => {
    const target = deviceId ? mobileWorkers.find((d) => d.id === deviceId) || null : null;
    setSelectedTargetId(deviceId);
    try {
      await quicClient.setDevServerTarget({
        targetDeviceId: target?.id,
        targetDeviceName: target?.name,
        targetDeviceClass: target?.deviceClass,
      });
    } catch {}
  }, [mobileWorkers]);

  const handleRequestScreenshot = useCallback(async () => {
    await quicClient.sendMobileWorkerPreviewCommand("capture_screenshot", {
      reason: "hotreload-control-plane",
    });
  }, []);

  // Tap project → start dev server directly using path + framework from scanner.
  // On tablet, ask the user "phone view or tablet view?" first so the
  // freshly-loaded guest mounts at the right frame. Picker remembers
  // the last answer, so a second tap on the same/different project
  // doesn't keep prompting — only fires when no choice has been made
  // OR when the user explicitly opens "Change view" from the dock.
  const handleStartProject = useCallback(async (project: ProjectItem) => {
    if (layout.isTablet && !isNativeRemoteRuntimeProject(project)) {
      const cancelled = await promptViewMode();
      if (cancelled) {
        return;
      }
    }
    // Push project context into native UserDefaults so YaverFeedbackPane
    // can prepend "Project: <name> (<path>)" to feedback prompts and
    // pin task.workDir on the agent. The runner-side vibingify pipeline
    // resolves the project from workDir → projectName, so the agent
    // can apply changes against the right working tree without the
    // user repeating "this is for the Todo RN project" every time.
    try {
      NativeModules.YaverInfo?.setInheritedGuestProject?.(
        project.name || "",
        project.path || "",
      );
    } catch {
      // Native module unavailable — non-iOS / unit-test path.
    }

    if (isNativeRemoteRuntimeProject(project)) {
      router.navigate({
        pathname: "/remote-runtime",
        params: {
          project: project.name,
          path: project.path,
          framework: project.framework || "",
        },
      } as any);
      return;
    }

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
        targetDeviceId: selectedTarget?.id,
        targetDeviceName: selectedTarget?.name,
        targetDeviceClass: selectedTarget?.deviceClass,
      });
    } catch {
      Alert.alert("Failed", `Could not start dev server for ${project.name}`);
    } finally {
      setStartingProject(null);
    }
  }, [devStatus, handleOpen, router, selectedTarget?.deviceClass, selectedTarget?.id, selectedTarget?.name]);

  const [loadingStatus, setLoadingStatus] = useState("");
  const activeProjectPath = devStatus?.workDir ? String(devStatus.workDir).trim() : "";
  const visibleOperations = visibleReloadOperations(reloadOperations, activeProjectPath);
  const currentOperation = visibleOperations[0] || null;
  const visibleIncidents = visibleReloadIncidents(reloadIncidents, currentOperation, activeProjectPath);
  const currentIncident = visibleIncidents[0] || null;
  const runtimeFamilyLine = describeRuntimeFamilySelection(currentOperation?.metadata);
  const showCurrentIncident = shouldShowCurrentReloadIncident(currentIncident, currentOperation);
  // lastGuestCrash / showGuestCrashCard: removed with the inline banner.
  // Will return when the data is folded into DeviceDetailsModal.
  // Match running workDir to project list to get the real app name (not directory name)
  const runningProject = (() => {
    if (!devStatus?.workDir) return devStatus?.framework ?? "App";
    const match = projects.find(p => p.path === devStatus.workDir);
    if (match) return match.name;
    return devStatus.workDir.split("/").pop() ?? "App";
  })();

  // All projects from /projects/mobile are already mobile-only
  const devProjects = projects.filter(isMobileHotReloadProject);

  if (!effectivelyConnected) {
    // Shared banner first so the user can immediately tap Switch › and
    // pick a device. Below it: the same minimal "connect a device"
    // hint the screen used to render full-bleed. Crucially, this no
    // longer hides the device-picker affordance behind a dead-end
    // "Not connected" message — the banner is always actionable.
    return (
      <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <RemoteBoxBanner />
        <View style={s.emptyContainer}>
          <Ionicons name="flame-outline" size={48} color={c.textTertiary} style={{ opacity: 0.5, marginBottom: 12 }} />
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
      <RemoteBoxBanner
        extra={
          <View style={s.bannerExtra}>
            <View style={[s.bannerPill, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
              <Text style={[s.bannerChipText, { color: c.textSecondary }]}>
                Go agent {agentInfo?.version || activeDevice?.agentVersion || "unknown"}
              </Text>
            </View>
            {remoteHermesReady ? (
              <View
                style={[
                  s.bannerPill,
                  {
                    backgroundColor: remoteHermesReady.enabled ? c.successBg : c.warnBg,
                    borderColor: remoteHermesReady.enabled ? c.successBorder : c.warnBorder,
                  },
                ]}
              >
                <Text style={[s.bannerChipText, { color: remoteHermesReady.enabled ? c.success : c.warn }]}>
                  {remoteHermesReady.enabled ? "Hermes reload ready" : remoteHermesReady.reason || "Hermes reload prerequisites missing"}
                </Text>
              </View>
            ) : null}
            {agentInfo?.workDir ? (
              <Text
                style={[
                  s.bannerPath,
                  { color: c.textTertiary, fontFamily: monoFamily },
                ]}
                numberOfLines={1}
              >
                {agentInfo.workDir}
              </Text>
            ) : null}
            {!remoteHermesReady?.enabled && remoteHermesReady?.notes?.[0] ? (
              <Text style={[s.bannerNote, { color: c.textMuted }]} numberOfLines={2}>
                {remoteHermesReady.notes[0]}
              </Text>
            ) : null}
          </View>
        }
      />
      <View style={s.container}>

        {mobileWorkers.length > 0 && (
          <>
            <Text style={[s.sectionTitle, { color: c.textMuted }]}>Preview Target</Text>
            <View style={[s.card, s.projectCard, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }, !isDark && { shadowColor: c.shadowSm }]}>
              <View style={s.targetHeaderRow}>
                <Text style={[s.projectName, { color: c.textPrimary }]}>Choose real-device Hermes target</Text>
                <View style={[s.targetStatePill, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
                  <Text style={[s.targetStateText, { color: c.textSecondary }]}>
                    {selectedTarget?.name || "This device"}
                  </Text>
                </View>
              </View>
              <Text style={[s.projectMeta, { color: c.textMuted, marginTop: 4 }]}>
                Default stays on this phone. Pick a spare mobile worker only when you want Hermes preview to target that device.
              </Text>
              <View style={s.targetChipRow}>
                <Pressable
                  onPress={() => { void handleSelectTarget(null); }}
                  style={[
                    s.targetChip,
                    {
                      borderColor: !selectedTarget ? c.accent : "transparent",
                      backgroundColor: !selectedTarget ? c.accentSoft : c.bgInput,
                    },
                  ]}
                >
                  <Text style={{ color: !selectedTarget ? c.accent : c.textSecondary, fontWeight: "600" }}>
                    This device
                  </Text>
                </Pressable>
                {mobileWorkers.map((worker) => (
                  <Pressable
                    key={worker.id}
                    onPress={() => { void handleSelectTarget(worker.id); }}
                    style={[
                      s.targetChip,
                      {
                        borderColor: selectedTarget?.id === worker.id ? c.accent : "transparent",
                        backgroundColor: selectedTarget?.id === worker.id ? c.accentSoft : c.bgInput,
                      },
                    ]}
                  >
                    <Text style={{ color: selectedTarget?.id === worker.id ? c.accent : c.textSecondary, fontWeight: "600" }}>
                      {worker.name}
                    </Text>
                  </Pressable>
                ))}
              </View>
            </View>
          </>
        )}

        {/* Running / starting dev server card */}
        {devStatus && (
          <View style={[
            s.card,
            s.activeCard,
            {
              backgroundColor: c.bgCardElevated,
              borderColor: isDark ? c.accent + "55" : c.borderSubtle,
            },
            !isDark && { shadowColor: c.shadowSm },
            tabletCard?.cardPadding,
          ]}>
            <View style={s.cardHeader}>
              <View style={[s.activeFrameworkBadge, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
                <FrameworkIcon framework={devStatus.framework} size={tabletCard?.frameworkIconSize ?? 24} />
              </View>
              <View style={s.cardTitleContainer}>
                <View style={s.activeTitleRow}>
                  <Text style={[s.cardTitle, tabletCard?.title, { color: c.textPrimary }]}>{runningProject}</Text>
                  <View
                    style={[
                      s.statusPill,
                      {
                        backgroundColor: devStatus.error ? c.errorBg : devStatus.running ? c.successBg : c.warnBg,
                        borderColor: devStatus.error ? c.errorBorder : devStatus.running ? c.successBorder : c.warnBorder,
                      },
                    ]}
                  >
                    <Text
                      style={[
                        s.statusPillText,
                        tabletCard?.statusPillText,
                        { color: devStatus.error ? c.error : devStatus.running ? c.success : c.warn },
                      ]}
                    >
                      {devStatus.error ? "Failed" : devStatus.running ? "Running" : "Building"}
                    </Text>
                  </View>
                </View>
                <Text style={[s.cardMeta, tabletCard?.meta, { color: c.textSecondary }]}>
                  {devStatus.error
                    ? `${devStatus.framework} · failed to start`
                    : devStatus.running
                      ? `${devStatus.framework} · port ${devStatus.port} · hot reload ${devStatus.hotReload ? "on" : "off"}`
                      : `${devStatus.framework} · starting...`}
                </Text>
                <View style={s.activeMetaChips}>
                  <View style={[s.activeMetaPill, { backgroundColor: c.accentSoft, borderColor: c.accent + "44" }]}>
                    <Text style={[s.activeMetaPillText, tabletCard?.metaPillText, { color: c.accent }]}>{String(devStatus.framework || "app").toUpperCase()}</Text>
                  </View>
                  {devStatus.port ? (
                    <View style={[s.activeMetaPill, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
                      <Text style={[s.activeMetaPillText, tabletCard?.metaPillText, { color: c.textSecondary }]}>PORT {devStatus.port}</Text>
                    </View>
                  ) : null}
                  <View
                    style={[
                      s.activeMetaPill,
                      {
                        backgroundColor: devStatus.hotReload ? c.successBg : c.warnBg,
                        borderColor: devStatus.hotReload ? c.successBorder : c.warnBorder,
                      },
                    ]}
                  >
                    <Text style={[s.activeMetaPillText, tabletCard?.metaPillText, { color: devStatus.hotReload ? c.success : c.warn }]}>
                      {devStatus.hotReload ? "HOT RELOAD ON" : "HOT RELOAD OFF"}
                    </Text>
                  </View>
                  <View style={[s.activeMetaPill, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
                    <Text style={[s.activeMetaPillText, tabletCard?.metaPillText, { color: c.textSecondary }]}>
                      TARGET {devStatus.targetDeviceName || selectedTarget?.name || "THIS DEVICE"}
                    </Text>
                  </View>
                </View>
                {devStatus.workDir ? (
                  <Text style={[s.activePath, tabletCard?.path, { color: c.textTertiary, fontFamily: monoFamily }]} numberOfLines={1}>
                    {devStatus.workDir}
                  </Text>
                ) : null}
              </View>
              {!devStatus.running && !devStatus.error && <ActivityIndicator size="small" color={c.warn} />}
            </View>
            {loadingStatus ? (
              <Text style={[s.inlineMeta, { color: c.textMuted }]}>{loadingStatus}</Text>
            ) : null}
            {currentOperation ? (
              <View style={[s.insetCard, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
                <Text style={[s.insetLabel, { color: c.textSecondary }]}>
                  {"\u25CF"} agent operation
                </Text>
                <Text style={[s.insetMeta, { color: c.textSecondary }]}>
                  {currentOperation.kind} · {currentOperation.status}
                  {currentOperation.phase ? ` · ${currentOperation.phase}` : ""}
                </Text>
                {currentOperation.message ? (
                  <Text style={[s.insetBody, { color: c.textPrimary }]}>
                    {currentOperation.message}
                  </Text>
                ) : null}
                {runtimeFamilyLine ? (
                  <Text style={[s.insetCaption, { color: c.textMuted }]}>
                    {runtimeFamilyLine}
                  </Text>
                ) : null}
              </View>
            ) : null}
            {showCurrentIncident && currentIncident ? (
              <View style={[s.insetCard, { backgroundColor: c.errorBg, borderColor: c.errorBorder }]}>
                <Text style={[s.insetLabel, { color: c.error }]}>
                  current blocker
                </Text>
                <Text style={[s.insetMeta, { color: c.error }]}>
                  {currentIncident.title || currentIncident.code}
                </Text>
                <Text style={[s.insetBody, { color: c.error }]}>
                  {currentIncident.userMessage}
                </Text>
                {currentIncident.suggestedAction ? (
                  <Text style={[s.insetCaption, { color: c.error }]}>
                    Next: {currentIncident.suggestedAction}
                  </Text>
                ) : null}
              </View>
            ) : null}
            {/* "last guest crash" card removed from the main hot-reload
                view — kept the data fetching + parsing intact (see the
                lastGuestCrash hook above) so DeviceDetailsModal can
                surface it on tap-to-expand later. The inline orange
                banner was visually loud and cluttered the active
                project card while the user was typically focused on
                running/reloading rather than reading old crash dumps. */}
            {/* Failure banner: shows the server-captured reason (stderr tail, missing tool, etc.) */}
            {devStatus.error ? (
              <View style={[s.insetCard, { backgroundColor: c.errorBg, borderColor: c.errorBorder }]}>
                <Text style={[s.insetLabel, { color: c.error }]}>
                  Start failed
                </Text>
                <Text style={[s.errorMono, { color: c.error, fontFamily: monoFamily }]} numberOfLines={10}>
                  {String(devStatus.error).trim()}
                </Text>
              </View>
            ) : null}
            {/* Live agent activity: Metro/Expo/Flutter stdout streamed over /dev/events SSE.
                Shows the last ~6 lines so the user sees progress during "starting…" and
                the actual log tail on failure. */}
            {devLog.length > 0 ? (
              <View style={[s.insetCard, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
                <Text style={[s.insetLabel, { color: c.textSecondary }]}>
                  agent activity
                </Text>
                {devLog.slice(-6).map((line, i) => (
                  <Text
                    key={`${i}-${line.length}`}
                    style={{ color: isDark ? c.textSecondary : c.textTertiary, fontSize: 11, fontFamily: monoFamily }}
                    numberOfLines={1}
                  >
                    {line}
                  </Text>
                ))}
              </View>
            ) : null}
            <View style={[s.cardActions, layout.isTablet ? s.cardActionsTablet : null]}>
              {devStatus.running && (
                <>
                  <Pressable
                    style={[s.actionBtn, s.openBtn, layout.isTablet ? s.openBtnTablet : null]}
                    onPress={handleOpen}
                    disabled={nativeLoading}
                  >
                    {nativeLoading ? (
                      <View style={s.primaryActionContent}>
                        <ActivityIndicator size="small" color="#fff" />
                        <View style={s.primaryActionTextWrap}>
                          <Text style={s.openBtnText}>Opening…</Text>
                          <Text style={s.openBtnSubtext}>Building and loading on this device</Text>
                        </View>
                      </View>
                    ) : (
                      <View style={s.primaryActionContent}>
                        <View style={s.primaryActionIcon}>
                          <Ionicons name="phone-portrait-outline" size={16} color="#fff" />
                        </View>
                        <View style={s.primaryActionTextWrap}>
                          <Text style={s.openBtnText}>Open in Yaver</Text>
                          <Text style={s.openBtnSubtext}>
                            {bundleMounted ? "Tap to bring it back on this device" : "Tap to load it on this device"}
                          </Text>
                        </View>
                        <Ionicons name="chevron-forward" size={18} color="#fff" />
                      </View>
                    )}
                  </Pressable>
                  <Pressable
                    style={[
                      s.actionBtn,
                      s.reloadBtn,
                      layout.isTablet ? s.secondaryBtnTablet : null,
                      { backgroundColor: c.accentSoft, borderColor: c.accent + "55" },
                      (reloadLoading || nativeLoading) && { opacity: 0.7 },
                    ]}
                    onPress={handleReload}
                    disabled={reloadLoading || nativeLoading}
                  >
                    {reloadLoading ? (
                      <View style={s.actionBtnRow}>
                        <ActivityIndicator size="small" color={c.accent} />
                        <Text style={[s.reloadBtnText, { color: c.accent }]}>Reloading…</Text>
                      </View>
                    ) : (
                      <Text style={[s.reloadBtnText, { color: c.accent }]}>
                        {bundleMounted ? "\u21BB Reload" : "Open first"}
                      </Text>
                    )}
                  </Pressable>
                  {workerSession?.hasTarget && workerSession.workerOnline && (
                    <Pressable
                      style={[s.actionBtn, s.reloadBtn, layout.isTablet ? s.secondaryBtnTablet : null, { backgroundColor: c.accentSoft, borderColor: c.accent + "55" }]}
                      onPress={handleRequestScreenshot}
                    >
                      <Text style={[s.reloadBtnText, { color: c.accent }]}>Shot</Text>
                    </Pressable>
                  )}
                </>
              )}
              {devStatus.error && devStatus.workDir && devStatus.framework && (
                <Pressable
                  style={[s.actionBtn, s.reloadBtn, layout.isTablet ? s.secondaryBtnTablet : null, { backgroundColor: c.accentSoft, borderColor: c.accent + "55" }]}
                  onPress={() => {
                    const framework = devStatus.framework || "";
                    const workDir = devStatus.workDir || "";
                    quicClient.startDevServer({
                      framework,
                      workDir,
                      targetDeviceId: selectedTarget?.id,
                      targetDeviceName: selectedTarget?.name,
                      targetDeviceClass: selectedTarget?.deviceClass,
                    }).catch(() => Alert.alert("Retry failed", "Could not restart the dev server"));
                  }}
                >
                  <Text style={[s.reloadBtnText, { color: c.accent }]}>Retry</Text>
                </Pressable>
              )}
              <Pressable
                style={[s.actionBtn, s.stopBtn, layout.isTablet ? s.secondaryBtnTablet : null, { backgroundColor: c.errorBg, borderColor: c.errorBorder }, stopState === "stopping" && { opacity: 0.6 }]}
                onPress={handleStop}
                disabled={stopState === "stopping"}
              >
                {stopState === "stopping" ? (
                  <View style={s.actionBtnRow}>
                    <ActivityIndicator size="small" color="#fff" />
                    <Text style={s.stopBtnText}>Stopping…</Text>
                  </View>
                ) : (
                  <Text style={s.stopBtnText}>Stop</Text>
                )}
              </Pressable>
            </View>
          </View>
        )}

        {/* Stop confirmation banner — surfaces "agent really stopped"
            after /dev/stop returns verified=true. Stays visible 2s, then
            self-dismisses. The card above this is gone (devStatus=null
            optimistic), so this banner is the only post-stop signal —
            without it the user has no idea whether the agent honored
            the request. */}
        {(stopState === "stopped" || stopState === "error") && (
          <View
            style={[
              s.stopBanner,
              {
                backgroundColor: stopState === "stopped" ? c.successBg : c.errorBg,
                borderColor: stopState === "stopped" ? c.successBorder : c.errorBorder,
              },
            ]}
          >
            <Text style={{ fontSize: 18 }}>{stopState === "stopped" ? "✓" : "⚠"}</Text>
            <View style={{ flex: 1 }}>
              <Text style={{ color: stopState === "stopped" ? c.success : c.error, fontSize: 13, fontWeight: "600" }}>
                {stopState === "stopped" ? "Dev server stopped" : "Stop incomplete"}
              </Text>
              <Text style={{ color: c.textSecondary, fontSize: 11, marginTop: 2 }}>
                {stopState === "stopped"
                  ? stopBuildsCancelled > 0
                    ? `Subprocess confirmed exit. Cancelled ${stopBuildsCancelled} in-flight build${stopBuildsCancelled === 1 ? "" : "s"}.`
                    : "Subprocess confirmed exit. Tap a project to start again."
                  : stopMessage}
              </Text>
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
          // Tablets fan project cards into 2/3-col grids so the wide
          // canvas isn't a single stretched row of repos. Re-mount on
          // column-count change.
          key={`hotreload-cols-${projectCols}`}
          numColumns={projectCols}
          columnWrapperStyle={projectCols > 1 ? { gap: tabletCard?.listGap ?? 10 } : undefined}
          contentContainerStyle={[s.listContent, tabletContent]}
          refreshControl={
            <RefreshControl refreshing={pullRefreshing} onRefresh={onPullRefresh} tintColor={c.accent} colors={[c.accent]} progressBackgroundColor={c.bgCard} />
          }
          renderItem={({ item }) => {
            const isStarting = startingProject === item.name;

            return (
              <Pressable
                style={[
                  s.card,
                  s.projectCard,
                  { backgroundColor: c.bgCard, borderColor: c.borderSubtle },
                  !isDark && { shadowColor: c.shadowSm },
                  projectCols > 1 ? { flex: 1, maxWidth: `${100 / projectCols}%` } : null,
                  tabletCard?.cardPadding,
                ]}
                onPress={() => handleStartProject(item)}
                disabled={isStarting}
              >
                <View style={s.cardHeader}>
                  <View style={s.frameworkIcon}>
                    <FrameworkIcon framework={item.framework} size={tabletCard?.frameworkIconSize ?? 22} />
                  </View>
                  <View style={s.cardTitleContainer}>
                    <Text style={[s.projectName, tabletCard?.projectName, { color: c.textPrimary }]}>{displayProjectTitle(item)}</Text>
                    <View style={s.tagRow}>
                      {item.tags?.map((tag) => (
                        <View key={tag} style={[s.tag, { backgroundColor: c.accentSoft, borderColor: "transparent" }]}>
                          <Text style={[s.tagText, { color: c.accent }]}>{tag}</Text>
                        </View>
                      ))}
                    </View>
                    <Text
                      style={[
                        s.projectPath,
                        tabletCard?.path,
                        { color: c.textTertiary, fontFamily: monoFamily },
                      ]}
                      numberOfLines={1}
                    >
                      {item.path}
                    </Text>
                    {isNativeRemoteRuntimeProject(item) ? (
                      <Text style={[s.projectMeta, tabletCard?.meta, { color: c.textMuted, marginTop: 4 }]}>
                        Opens via Remote Runtime — drives the simulator on the dev box; phone stays paired.
                      </Text>
                    ) : null}
                  </View>
                  {isStarting ? (
                    <ActivityIndicator size="small" color={c.accent} />
                  ) : (
                    <Text style={{ color: c.textMuted, fontSize: 18, fontWeight: "300" }}>{"\u203A"}</Text>
                  )}
                </View>
              </Pressable>
            );
          }}
          ListEmptyComponent={
            <View style={s.emptyList}>
              <View
                style={[
                  s.emptyStateCard,
                  { backgroundColor: c.bgCardElevated, borderColor: c.borderSubtle },
                  !isDark && { shadowColor: c.shadowSm },
                ]}
              >
                <Ionicons name="folder-open-outline" size={32} color={c.textMuted} style={{ marginBottom: 12 }} />
                <Text style={[s.emptyStateTitle, { color: c.textPrimary }]}>
                  {projectsScanning ? "Discovering apps" : "No apps yet"}
                </Text>
                <Text style={[s.emptyStateBody, { color: c.textSecondary }]}>
                  {projectsScanning
                    ? `Discovering mobile projects on ${agentInfo?.hostname || activeDevice?.name || "remote box"}…`
                    : projects.length > 0
                    ? `No mobile projects found on ${agentInfo?.hostname || activeDevice?.name || "this box"}. Looking for Hermes apps and native Swift/Kotlin projects.`
                    : `No projects discovered on ${agentInfo?.hostname || activeDevice?.name || "this box"}. The agent scans your home directory automatically.`}
                </Text>
              </View>
              {projectsScanning && scanStalled ? (
                <Pressable
                  onPress={() => setShowDiagnostics(true)}
                  style={[s.warnCallout, { backgroundColor: c.warnBg, borderColor: c.warnBorder }]}
                  hitSlop={8}
                >
                  <Text style={{ color: c.warn, fontSize: 12, textAlign: "center", lineHeight: 18 }}>
                    Taking longer than usual. Let's find out why — we'll check the
                    connection, sign-in, and file access.{"\n"}
                    <Text style={{ color: c.accent, fontWeight: "700" }}>Diagnose discovery ›</Text>
                  </Text>
                </Pressable>
              ) : null}
              <Pressable
                style={[s.rediscoverBtn, { borderColor: c.accent, backgroundColor: c.accent }]}
                onPress={async () => {
                  try {
                    setProjectsScanning(true);
                    const baseUrl = (quicClient as any).baseUrl;
                    const headers = (quicClient as any).authHeaders;
                    await fetch(`${baseUrl}/projects/mobile`, { method: "POST", headers });
                  } catch {}
                }}
              >
                <Text style={[s.rediscoverBtnText, { color: c.textInverse }]}>
                  {projectsScanning ? "Discovering..." : "Rediscover"}
                </Text>
              </Pressable>
              {projectsScanning ? (
                <Pressable
                  style={[s.rediscoverBtn, { borderColor: c.errorBorder, backgroundColor: c.errorBg }]}
                  onPress={() => { void handleStopDiscovery(); }}
                  disabled={scanStopping}
                >
                  <Text style={[s.rediscoverBtnText, { color: c.error }]}>
                    {scanStopping ? "Stopping..." : "Stop Discovery"}
                  </Text>
                </Pressable>
              ) : null}
              {/* Always-available escape hatch: run the layered preflight
                  (connection → sign-in → file access) and get numbered
                  fix-it steps instead of guessing. */}
              <Pressable onPress={() => setShowDiagnostics(true)} hitSlop={8} style={s.diagnoseLink}>
                <Text style={{ color: c.accent, fontSize: 13, fontWeight: "700" }}>
                  Diagnose discovery ›
                </Text>
              </Pressable>
            </View>
          }
        />
      </View>

      <RemoteBoxPickerModal
        visible={showRemoteBoxPicker}
        onClose={() => setShowRemoteBoxPicker(false)}
      />

      <DiscoveryDiagnosticsPanel
        visible={showDiagnostics}
        onClose={() => setShowDiagnostics(false)}
        probe={showDiagnostics ? buildDiagnosticsProbe() : null}
        onOpenDevices={() => router.push("/(tabs)/devices")}
        onRetryScan={() => { void kickRescan(); }}
        onReauth={() => router.push("/(tabs)/settings")}
        onRunnerAuth={() => router.push("/(tabs)/settings")}
      />

    </SafeAreaView>
  );
}

// ── Styles ─────────────────────────────────────────────────────────

const s = StyleSheet.create({
  safe: { flex: 1 },
  container: { flex: 1 },

  // Section
  sectionTitle: { ...typography.badge, textTransform: "uppercase", letterSpacing: 0.5, marginHorizontal: 16, marginTop: 24, marginBottom: 8 },

  // Cards
  card: {
    marginHorizontal: spacing.lg,
    borderRadius: 14,
    paddingHorizontal: spacing.lg,
    paddingVertical: 16,
    marginBottom: spacing.md,
    borderWidth: 1,
    ...lightCardShadow,
  },
  activeCard: {
    borderWidth: 1,
    marginTop: 12,
  },
  cardHeader: { flexDirection: "row", alignItems: "center", gap: 10 },
  activeFrameworkBadge: {
    width: 42,
    height: 42,
    borderRadius: 14,
    borderWidth: 1,
    alignItems: "center",
    justifyContent: "center",
  },
  cardTitleContainer: { flex: 1 },
  cardTitle: { ...typography.cardTitle, fontSize: 16, fontWeight: "600" },
  cardMeta: { ...typography.caption, fontSize: 13, marginTop: 4 },
  activeMetaChips: { flexDirection: "row", flexWrap: "wrap", gap: 6, marginTop: 10 },
  activeMetaPill: {
    paddingHorizontal: 10,
    paddingVertical: 6,
    borderRadius: 999,
    borderWidth: 1,
  },
  activeMetaPillText: { fontSize: 10, fontWeight: "700", letterSpacing: 0.35 },
  activePath: { ...typography.monoCaption, marginTop: 10 },
  statusDot: { width: 8, height: 8, borderRadius: 4 },
  activeTitleRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 10 },
  statusPill: { paddingHorizontal: 10, paddingVertical: 5, borderRadius: 999, borderWidth: 1 },
  statusPillText: { fontSize: 11, fontWeight: "700" },
  inlineMeta: { fontSize: 11, marginTop: 4 },
  insetCard: { marginTop: 10, padding: 12, borderRadius: 10, borderWidth: 1 },
  insetLabel: { fontSize: 12, fontWeight: "600", marginBottom: 4 },
  insetMeta: { fontSize: 12, marginBottom: 4 },
  insetBody: { fontSize: 13, lineHeight: 18 },
  insetCaption: { fontSize: 11, marginTop: 4 },
  errorMono: { fontSize: 11, lineHeight: 16, marginTop: 2 },

  cardActions: { flexDirection: "row", gap: 8, marginTop: 12 },
  cardActionsTablet: { flexWrap: "wrap", alignItems: "stretch" },
  actionBtn: { paddingVertical: 11, paddingHorizontal: 14, borderRadius: 10, alignItems: "center", justifyContent: "center" },
  actionBtnRow: { flexDirection: "row", alignItems: "center", gap: 6 },
  openBtn: { flex: 2, alignItems: "center" },
  openBtnTablet: { flexBasis: "100%" },
  secondaryBtnTablet: { flex: 1, minWidth: 140 },
  primaryActionContent: { flexDirection: "row", alignItems: "center", gap: 10, width: "100%" },
  primaryActionIcon: {
    width: 28,
    height: 28,
    borderRadius: 999,
    backgroundColor: "rgba(255,255,255,0.16)",
    alignItems: "center",
    justifyContent: "center",
  },
  primaryActionTextWrap: { flex: 1, minWidth: 0, alignItems: "flex-start" },
  openBtnText: { color: "#fff", fontSize: 14, fontWeight: "800" },
  openBtnSubtext: { color: "rgba(255,255,255,0.84)", fontSize: 11, marginTop: 2 },
  reloadBtn: { borderWidth: 1, flex: 1, alignItems: "center" },
  reloadBtnText: { color: "#6E56F6", fontSize: 13, fontWeight: "600" },
  stopBtn: { borderWidth: 1, paddingHorizontal: 16, alignItems: "center" },
  stopBtnText: { color: "#ef4444", fontSize: 13, fontWeight: "600" },

  // Framework icon
  frameworkIcon: {},

  // Tag chips
  tagRow: { flexDirection: "row", flexWrap: "wrap", gap: 4, marginTop: 3 },
  tag: {
    backgroundColor: "#6366f115",
    borderRadius: 6,
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderWidth: 1,
    borderColor: "transparent",
  },
  tagText: { color: "#818cf8", fontSize: 11, fontWeight: "600" },

  // Project cards
  projectCard: { borderWidth: 1 },
  projectName: { ...typography.cardTitle, fontSize: 17 },
  projectMeta: { ...typography.caption, marginTop: 3 },
  projectPath: { ...typography.path, marginTop: 3 },
  listContent: { paddingBottom: 40 },
  targetChipRow: { flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 12 },
  targetHeaderRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 12 },
  targetStatePill: {
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 10,
    paddingVertical: 6,
    flexShrink: 1,
  },
  targetStateText: { fontSize: 11, fontWeight: "700" },
  targetChip: {
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 12,
    paddingVertical: 6,
  },
  bannerExtra: { flexDirection: "row", alignItems: "center", gap: 6, flexWrap: "wrap", backgroundColor: "transparent" },
  bannerPill: {
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 10,
    paddingVertical: 6,
    backgroundColor: "transparent",
  },
  bannerChipText: { fontSize: 12, fontWeight: "600", backgroundColor: "transparent" },
  bannerPath: { ...typography.monoCaption, width: "100%" },
  bannerNote: { fontSize: 11, lineHeight: 16, width: "100%" },

  // Empty
  emptyContainer: { flex: 1, alignItems: "center", justifyContent: "center", padding: 40 },
  emptyIcon: { fontSize: 40, marginBottom: 12 },
  emptyTitle: { fontSize: 18, fontWeight: "700", marginBottom: 4 },
  emptySubtitle: { fontSize: 13, textAlign: "center", lineHeight: 20 },
  emptyList: { padding: 40, alignItems: "center" },
  emptyStateCard: {
    width: "100%",
    maxWidth: 360,
    paddingHorizontal: 24,
    paddingVertical: 24,
    borderRadius: 16,
    borderWidth: 1,
    alignItems: "center",
    marginBottom: 14,
  },
  emptyStateTitle: { fontSize: 16, fontWeight: "600", marginBottom: 8 },
  emptyStateBody: { fontSize: 13, lineHeight: 19, textAlign: "center" },
  warnCallout: { marginTop: 8, paddingHorizontal: 14, paddingVertical: 10, borderRadius: 10, borderWidth: 1 },
  rediscoverBtn: { marginTop: 14, borderWidth: 1, borderRadius: 10, paddingHorizontal: 16, paddingVertical: 10 },
  rediscoverBtnText: { fontSize: 14, fontWeight: "600" },
  diagnoseLink: { marginTop: 14, paddingVertical: 6, paddingHorizontal: 12 },
  stopBanner: {
    marginHorizontal: 16,
    marginTop: 12,
    padding: 12,
    borderRadius: 10,
    borderWidth: StyleSheet.hairlineWidth,
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },

});
