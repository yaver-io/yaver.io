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
// WebViewCompat, not react-native-webview directly: the real WebView has no web
// build and throws "React Native WebView does not support this platform.", which
// made the preview the ONE screen that could not render when Yaver's own app ran
// as RN-web. The web sibling implements the same surface with an <iframe>.
// Native resolves to the real WebView, unchanged. See WebViewCompat.web.tsx.
import { WebView, WEBVIEW_PROBE_UNSUPPORTED } from "../../src/components/WebViewCompat";
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
import { describeDevReloadResult, devReloadReachedTarget, quicClient, type CapabilitySnapshot, type DevCompatibilityStatus, type DevServerStatus, type MobileWorkerPreviewSession } from "../../src/lib/quic";
import { previewAgentHealthIsAuthoritative, previewHealthCanOfferProjectFix, previewLogsLookHealthy, previewPaintGateMode } from "../../src/lib/previewHealth";
import { getAvailableModules, isBundleLoaderAvailable, loadApp } from "../../src/lib/bundleLoader";
import { openAppBus } from "../../src/lib/openAppBus";
import { setActivePreviewLane, subscribeBrowserShake } from "../../src/lib/feedbackTrigger";
import LaneStartupStatus from "../../src/components/LaneStartupStatus";
import { PREVIEW_READY_SCRIPT, PREVIEW_LANE_SCRIPT, PREVIEW_RESOURCE_ERROR_SCRIPT } from "../../src/lib/previewReadyScript";
import { downloadArtifact } from "../../src/lib/builds";
import { describeConnectionStatus } from "../../src/lib/connection";
import { buildFailureHint, buildNativeBuildRequest, nativeBuildFailureMessage, nativeBuildFailureTitle } from "../../src/lib/nativeBuild";
import { isActiveDevServerStatus } from "../../src/lib/devServerState";
import { connectionManager } from "../../src/lib/connectionManager";
import { shouldPollDevStatus } from "../../src/lib/devStatusPolling";
import { detectCompileFailure } from "../../src/lib/compileFailure";
import { previewBundlePath } from "../../src/lib/previewBundlePath";
import { browserLaneProbeLine, doctorBrowserLane, probeBrowserResource, reconcileBrowserLaneProbe, shouldRetryBrowserResourceFailure, shouldRunBrowserLaneDoctor, type BrowserLaneProbeResult } from "../../src/lib/browserLaneDoctor";
import { previewPhaseTitle, previewTimeoutExplanation } from "../../src/lib/previewPhase";
import { previewWaitLine } from "../../src/lib/previewWait";
import { handlePreviewScreenMessage } from "../../src/lib/screenContextBridge";
import { BrowserVibeBubble } from "../../src/components/BrowserVibeBubble";
import {
  capabilityGapFromDevEvent,
  capabilityGapFromError,
  capabilityGapFromStatus,
  gapBody,
  gapConstraint,
  gapFixLabel,
  gapHeadroomLine,
  gapReclaimLabel,
  gapRetriesAfterFix,
  gapTitle,
  gapWarning,
  type CapabilityGap,
} from "../../src/lib/capabilityGap";
import { formatFixElapsed, runCapabilityGapFix } from "../../src/lib/capabilityGapFix";
import { subscribeSse, type SseSubscription } from "../../src/lib/sseClient";
import { isWebServedStatus, shouldUseNativePreview } from "../../src/lib/devLane";
import { applyPreviewCapabilities, guardYaverSelfDevelopmentActions, isHermesMobileFramework, workspaceAppLanes } from "../../src/lib/mobileProjectActions";
import { runtimeSurfaceClient } from "../../src/lib/runtimeSurfaceClient";
import { lightCardShadow, spacing, typography } from "../../src/theme/tokens";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import type { PhoneProject } from "../../src/lib/phoneProjects";
import { listLocalPhoneProjectsMeta } from "../../src/lib/phoneSandboxLocal";
import { discoverConnectedProviderProjects, type ProviderProject } from "../../src/lib/gitProviderProjects";
import { cloneGitRepoToPhone } from "../../src/lib/cloneToPhone";
import {
  canOpenPreviewBeforeRefresh,
  reconcilePreviewDevStatus,
  usablePreviewDevStatus,
} from "../../src/lib/previewDevStatus";
import { DevServerStopDialog, type DevServerStopPhase } from "../../src/components/DevServerStopDialog";
import BrowserShortcutExportModal from "../../src/components/BrowserShortcutExportModal";
import { startBrowserProjectLane, subscribeProjectPreviewOutput } from "../../src/lib/projectPreviewRuntime";
import {
  attachedDogfoodCheckout,
  dogfoodGuestProjectName,
  dogfoodProjectRootPath,
  isPathInsideAttachedDogfoodCheckout,
  normalizedDogfoodPath,
} from "../../src/lib/dogfoodRenderBridge";

// ── Types ──────────────────────────────────────────────────────────

interface ProjectItem {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  frameworks?: string[];
  stack?: string;
  stacks?: string[];
  surfaces?: string[];
  testSurfaces?: string[];
  backend?: string;
  services?: string[];
  hosting?: string[];
  role?: string;
  executionMode?: string;
  primarySurface?: string;
  gitRemote?: string;
  tags?: string[];
  isRepoRoot?: boolean;
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

type MobileDiscoveryState = {
  status?: "idle" | "discovering" | "partial" | "ready";
  discovering?: boolean;
  partiallyReady?: boolean;
  lastCompletedAt?: string;
  scanMs?: number;
  timedOut?: boolean;
  permDenied?: number;
  scanError?: string;
};

type PreviewProbeState = {
  href?: string;
  reason?: string;
  mountId?: string;
  mountChildren?: number;
  bodyChildren?: number;
  bodyTextLen?: number;
  visibleBoxCount?: number;
  mediaCount?: number;
};

type VectorIconName = keyof typeof Ionicons.glyphMap;

const TEXT_ICON_TO_IONICON: Record<string, VectorIconName> = {
  "\u{1F310}": "globe-outline",
  "\u{1F4F1}": "phone-portrait-outline",
  "\u{1F4FA}": "tv-outline",
  "\u{1F50C}": "flash-outline",
  "\u{1F527}": "construct-outline",
  "\u{1F680}": "rocket-outline",
  "\u{1F4E6}": "cube-outline",
  "\u{1F5C4}": "folder-open-outline",
  "\u2699": "settings-outline",
  "\u2699\uFE0F": "settings-outline",
  "\u25B6": "play-outline",
};

function iconNameForGlyph(icon?: string | null): VectorIconName | null {
  if (!icon) return null;
  if (icon in Ionicons.glyphMap) return icon as VectorIconName;
  return TEXT_ICON_TO_IONICON[icon] ?? null;
}

function GlyphIcon({
  icon,
  size,
  color,
  textStyle,
}: {
  icon?: string | null;
  size: number;
  color: string;
  textStyle?: any;
}) {
  const name = iconNameForGlyph(icon);
  if (name) {
    return <Ionicons name={name} size={size} color={color} />;
  }
  return <Text style={textStyle}>{icon || "\u2022"}</Text>;
}

// Branded vector icons via mobile/src/components/FrameworkIcon.tsx \u2014 see
// that file for the per-framework MaterialCommunityIcon + brand-color
// mapping. Kept in sync with hotreload.tsx so the two surfaces render
// identical icons for the same framework.

const MOBILE_FRAMEWORKS = ["expo", "react-native", "flutter"];
const SECOND_CLASS_MOBILE_FRAMEWORKS = ["flutter", "swift", "kotlin"];
const WEB_FRAMEWORKS = ["nextjs", "vite", "react"];
const PREVIEW_TARGET_KEY = "@yaver/hotreload_preview_target";
const MAX_WEB_PREVIEW_LOGS = 80;

const WEBVIEW_DIAGNOSTICS_SCRIPT = `(function(){
  if (window.__yaverPreviewDiagnosticsInstalled) return true;
  window.__yaverPreviewDiagnosticsInstalled = true;
  var MAX = 1200;
  function clean(value) {
    try {
      if (value instanceof Error) value = value.stack || value.message || String(value);
      else if (typeof value === 'object') value = JSON.stringify(value);
      else value = String(value);
    } catch (e) {
      try { value = String(value); } catch (_) { value = '[unserializable]'; }
    }
    value = String(value || '')
      .replace(/Bearer\\s+[A-Za-z0-9._~+\\/=:-]+/gi, 'Bearer [redacted]')
      .replace(/([?&](?:token|access_token|refresh_token|password|secret|key)=)[^\\s&]+/gi, '$1[redacted]')
      .replace(/((?:token|password|secret|api[_-]?key)\\s*[:=]\\s*)[^\\s,;}]+/gi, '$1[redacted]');
    return value.length > MAX ? value.slice(0, MAX) + ' ...' : value;
  }
  function post(level, parts) {
    try {
      var text = Array.prototype.slice.call(parts || []).map(clean).filter(Boolean).join(' ');
      if (!text) return;
      window.ReactNativeWebView && window.ReactNativeWebView.postMessage(JSON.stringify({
        t: 'yaver-preview-log',
        level: level || 'log',
        text: text,
        ts: Date.now()
      }));
    } catch (e) {}
  }
  ['log','warn','error'].forEach(function(level) {
    var original = console[level];
    if (!original || original.__yaverWrapped) return;
    var wrapped = function() {
      post(level, arguments);
      return original.apply(console, arguments);
    };
    wrapped.__yaverWrapped = true;
    console[level] = wrapped;
  });
  window.addEventListener('error', function(event) {
    var target = event && event.target;
    if (target && target !== window && (target.src || target.href)) {
      // PREVIEW_RESOURCE_ERROR_SCRIPT owns this structured signal for BOTH
      // preview implementations. Posting it here too produced duplicate logs.
      return;
    }
    post('error', [
      event && event.message ? event.message : 'window error',
      event && event.filename ? ('@ ' + event.filename + ':' + (event.lineno || 0) + ':' + (event.colno || 0)) : '',
      event && event.error ? (event.error.stack || event.error.message || event.error) : ''
    ]);
  }, true);
  window.addEventListener('unhandledrejection', function(event) {
    var reason = event && event.reason;
    post('error', ['unhandled rejection', reason && (reason.stack || reason.message) ? (reason.stack || reason.message) : reason]);
  });
  return true;
})(); true;`;

const WEBVIEW_INJECTED_SCRIPT = `${PREVIEW_RESOURCE_ERROR_SCRIPT}\n${WEBVIEW_DIAGNOSTICS_SCRIPT}\n${PREVIEW_READY_SCRIPT}`;
// Runs BEFORE the guest's scripts: stamp the lane so a lane-aware
// yaver-feedback SDK self-hosts its draggable icon (feedback-sdk-lanes audit).
const WEBVIEW_BEFORE_CONTENT_SCRIPT = `${PREVIEW_LANE_SCRIPT}\n${PREVIEW_RESOURCE_ERROR_SCRIPT}\n${WEBVIEW_DIAGNOSTICS_SCRIPT}`;

function pathLeaf(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() || path;
}

function sameProjectPath(a?: string | null, b?: string | null): boolean {
  const left = normalizedDogfoodPath(a);
  const right = normalizedDogfoodPath(b);
  return !!left && left === right;
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

function previewLogColor(
  line: string,
  colors: { error: string; warn: string; success: string; info: string; textMuted: string; accent: string },
): string {
  const l = line.toLowerCase();
  if (/^\s*(queued|starting|building|running|listening|ready)\s*$/i.test(line) ||
      /^\s*\$\s+(flutter|npm|npx|yarn|pnpm|bun|expo|vite|next)\b/i.test(line)) {
    return colors.info || colors.accent;
  }
  if (/\b(error|failed|failure|exception|fatal|crash|cannot|unable|denied|rejected|timed out|timeout)\b/.test(l) || /\bhttp\s*[45]\d\d\b/.test(l)) {
    return colors.error;
  }
  if (/\b(warn|warning|deprecated|mismatch|expected version|recrawled|retry|stale)\b/.test(l)) {
    return colors.warn;
  }
  if (/\b(ready|success|succeeded|compiled|done|listening|serving on|running|connected)\b/.test(l)) {
    return colors.success;
  }
  if (/\b(queued|starting|building|waiting|scanning|probe|progress|installing)\b/.test(l)) {
    return colors.info || colors.accent;
  }
  return colors.textMuted;
}

function previewLogsNeedProjectFix(lines: readonly string[], statusError?: string | null): boolean {
  const err = String(statusError || "").toLowerCase();
  const tail = lines.slice(-80).join("\n").toLowerCase();
  const text = `${err}\n${tail}`;
  if (!text.trim()) return false;
  if (/\b(render probe timed out|server is listening but the webview did not render|no render probe message received)\b/.test(text)) {
    return false;
  }
  if (/\b(disconnected|not connected|connection dropped|relay disconnected|status polling is paused)\b/.test(text)) {
    return false;
  }
  const hasRealFailure = /\b(failed to compile|compilation failed|module build failed|bundling failed|unable to resolve module|syntaxerror|error ts\d+|no file or variants found for asset|cannot find module|undefined name|isn't defined|runtime error|uncaught|exception|crash)\b/.test(text);
  if (!hasRealFailure) return false;
  return true;
}

// Shared gate (parity rule): both browser-preview implementations consume
// src/lib/previewHealth — see previewHealth.test.mts for the drift guard.
const previewCanOfferProjectFix = (status: DevServerStatus | null | undefined, lines: readonly string[]): boolean =>
  previewHealthCanOfferProjectFix(status, lines, previewLogsNeedProjectFix);

function appendPreviewLogLine(prev: string[], line: string, limit = MAX_WEB_PREVIEW_LOGS): string[] {
  const trimmed = line.replace(/\x1B\[[0-?]*[ -/]*[@-~]/g, "").trim();
  if (!trimmed) return prev;
  return (prev[prev.length - 1] === trimmed ? prev : [...prev, trimmed]).slice(-limit);
}

function isPreviewRuntimeIssueLevel(level: string): boolean {
  return level === "error" || level === "warn";
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

function repoToProject(repo: RepoItem): ProjectItem {
  const frameworks = Array.from(new Set([repo.framework, ...(repo.subframeworks ?? [])].filter(Boolean) as string[]));
  return {
    name: repo.name,
    path: repo.path,
    branch: repo.branch,
    framework: repo.framework || (repo.isMonorepo ? "monorepo" : undefined),
    frameworks,
    stack: repo.isMonorepo ? "monorepo" : repo.framework,
    stacks: repo.isMonorepo ? ["monorepo", ...frameworks] : frameworks,
    surfaces: repo.tags?.filter((t) => ["web", "mobile", "backend", "ios", "android"].includes(t.toLowerCase())),
    gitRemote: repo.gitRemote,
    tags: Array.from(new Set([...(repo.tags ?? []), ...(repo.isMonorepo ? ["repo", "monorepo"] : ["repo"])])),
    isRepoRoot: true,
  };
}

function mergeProjectRows(projectRows: ProjectItem[], repoRows: RepoItem[]): ProjectItem[] {
  const byPath = new Map<string, ProjectItem>();
  for (const row of repoRows.map(repoToProject)) {
    byPath.set(row.path, row);
  }
  for (const row of projectRows) {
    byPath.set(row.path, { ...byPath.get(row.path), ...row, isRepoRoot: byPath.get(row.path)?.isRepoRoot });
  }
  return Array.from(byPath.values()).sort((a, b) => a.name.localeCompare(b.name));
}

function projectTerms(project: ProjectItem): string[] {
  return [
    project.name,
    project.path,
    project.branch,
    project.framework,
    ...(project.frameworks ?? []),
    project.stack,
    ...(project.stacks ?? []),
    ...(project.surfaces ?? []),
    ...(project.testSurfaces ?? []),
    project.backend,
    ...(project.services ?? []),
    ...(project.hosting ?? []),
    project.role,
    project.primarySurface,
    project.executionMode,
    ...(project.tags ?? []),
  ].filter(Boolean) as string[];
}

function getProjectCategory(projectOrFramework?: ProjectItem | string): "mobile" | "web" | "other" {
  const project = typeof projectOrFramework === "string" ? undefined : projectOrFramework;
  const framework = (typeof projectOrFramework === "string" ? projectOrFramework : projectOrFramework?.framework || "").toLowerCase();
  const surfaces = (project?.surfaces ?? []).map((s) => s.toLowerCase());
  const frameworks = (project?.frameworks ?? []).map((s) => s.toLowerCase());
  if (surfaces.some((s) => s.includes("mobile") || s.includes("ios") || s.includes("android")) || frameworks.some((fw) => MOBILE_FRAMEWORKS.includes(fw) || SECOND_CLASS_MOBILE_FRAMEWORKS.includes(fw))) {
    return "mobile";
  }
  if (surfaces.some((s) => s.includes("web")) || frameworks.some((fw) => WEB_FRAMEWORKS.includes(fw))) {
    return "web";
  }
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
  const { activeDevice, connectionStatus, devices, connectedDeviceIds, refreshDevices, retryConnection, codingMode } = useDevice();
  const isConnected = connectionStatus === "connected" && !!activeDevice;
  // Use the selected box's pooled client for the whole preview lifecycle.
  // Mixing this with the focus-bound singleton produced a green status card
  // from the pool followed by `Failed to fetch` (and an empty WebView URL)
  // from a singleton that was still reconnecting.
  const previewClient = connectionManager.renderClient();
  const codingClient = connectionManager.runnerClient();
  // Effective state — focused box OR any pool client live. See
  // lib/connectionState; aligns this tab with Devices/Tasks/Reload so
  // we no longer disagree about "connected" when the focused box is
  // mid-retry but a peer is still up.
  const effectivelyConnected = computeEffectiveConnected(connectionStatus, connectedDeviceIds);
  const [selectedTargetId, setSelectedTargetId] = useState<string | null>(null);
  const mobileWorkers = devices.filter((d) => d.deviceClass === "edge-mobile");
  const selectedTarget = mobileWorkers.find((d) => d.id === selectedTargetId) || null;
  const isDirectConnection = previewClient.connectionMode === "direct";
  const router = useRouter();
  // The outer Dogfood host injects the checkout path that the agent already
  // verified as Yaver. Hide only that directory tree from the inner Projects
  // inventory; other projects remain launchable while Yaver is dogfooding.
  const dogfoodCheckout = attachedDogfoodCheckout();
  const [remotelessPhoneProjects, setRemotelessPhoneProjects] = useState<PhoneProject[]>([]);
  const [remotelessProviderProjects, setRemotelessProviderProjects] = useState<ProviderProject[]>([]);
  const [remotelessLoading, setRemotelessLoading] = useState(false);
  const [remotelessError, setRemotelessError] = useState<string | null>(null);
  const [remotelessCloningID, setRemotelessCloningID] = useState<string | null>(null);

  const loadRemotelessProjects = useCallback(async () => {
    if (codingMode !== "local-only") return;
    setRemotelessLoading(true);
    setRemotelessError(null);
    try {
      const [local, discovery] = await Promise.all([
        listLocalPhoneProjectsMeta().catch(() => [] as PhoneProject[]),
        discoverConnectedProviderProjects(),
      ]);
      setRemotelessPhoneProjects(local);
      setRemotelessProviderProjects(discovery.projects);
      setRemotelessError(discovery.errors.join("\n") || null);
    } catch (error) {
      setRemotelessError(error instanceof Error ? error.message : "Could not list projects on this phone.");
    } finally {
      setRemotelessLoading(false);
    }
  }, [codingMode]);

  useEffect(() => {
    void loadRemotelessProjects();
  }, [loadRemotelessProjects]);

  const cloneRemotelessProject = useCallback(async (project: ProviderProject) => {
    setRemotelessCloningID(project.id);
    try {
      const cloned = await cloneGitRepoToPhone(project.cloneUrl, { ref: project.defaultBranch });
      await loadRemotelessProjects();
      router.push({ pathname: "/(tabs)/tasks", params: { openNew: "1", phoneCheckout: cloned.slug, sessionStartedFrom: "mobile-workspace" } } as any);
    } catch (error) {
      Alert.alert("Clone failed", error instanceof Error ? error.message : "Could not clone this repository to the phone.");
    } finally {
      setRemotelessCloningID(null);
    }
  }, [loadRemotelessProjects, router]);

  // Build + task status hoisted to the top of the component so the shared
  // helpers below (sendTaskOrWarn / offerAgentFix) can surface status from
  // any callback without forward-reference TDZ errors.
  const [nativeLoading, setNativeLoading] = useState(false);
  // Set when the readiness probe is IMPOSSIBLE (cross-origin iframe on RN-web),
  // as opposed to merely slow — one is worth waiting for, the other never ends.
  const [probeUnavailable, setProbeUnavailable] = useState<string | null>(null);
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
    previewClient.capabilitySnapshot()
      .then(setCapabilitySnapshot)
      .catch(() => setCapabilitySnapshot(null));
    // Fire-and-forget; older agents 404 and we just fall through to
    // the snapshot-based blocker below.
    previewClient.deployCapabilities()
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
      await codingClient.sendTask(title, description);
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
              void codingClient.sendTask(title, description)
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
            const r = await previewClient.recover(ctx);
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
  const [showStopConfirm, setShowStopConfirm] = useState(false);
  const [stopPhase, setStopPhase] = useState<DevServerStopPhase>("confirm");
  const [workerSession, setWorkerSession] = useState<MobileWorkerPreviewSession | null>(null);
  const [projects, setProjects] = useState<ProjectItem[]>([]);
  const [shortcutProject, setShortcutProject] = useState<ProjectItem | null>(null);
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
        previewClient.refreshMobileProjects(),
        refreshDevices(),
      ]);
    } finally {
      setPullRefreshing(false);
    }
  }, [refreshDevices]);
  const [startingProject, setStartingProject] = useState<string | null>(null);
  const [showWebView, setShowWebView] = useState(false);
  const showWebViewRef = useRef(false);
  showWebViewRef.current = showWebView;
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
  const [mobileDiscovery, setMobileDiscovery] = useState<MobileDiscoveryState | null>(null);
  const webViewRef = useRef<WebView>(null);
  // Browser-preview cold-start retry budget (see the WebView onError/onHttpError
  // below). A web dev server can take up to a minute to compile on first open.
  const webPreviewRetryRef = useRef(0);
  const webPreviewErroredRef = useRef(false);
  const webPreviewRetryTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const webPreviewRenderWatchdogFiredRef = useRef(false);
  // True once the WebView paints REAL content (a flutter-view / non-empty DOM),
  // not just a 200 on index.html — keeps the progress overlay up so a Flutter
  // page that renders black while CanvasKit boots never shows as a blank void.
  const [webPreviewContentLoaded, setWebPreviewContentLoaded] = useState(false);
  const [webPreviewFailed, setWebPreviewFailed] = useState(false);
  const [webPreviewLogs, setWebPreviewLogs] = useState<string[]>([]);
  const [webPreviewProbe, setWebPreviewProbe] = useState<PreviewProbeState | null>(null);
  const webPreviewProbeRef = useRef<PreviewProbeState | null>(null);
  const [browserLaneProbe, setBrowserLaneProbe] = useState<BrowserLaneProbeResult | null>(null);
  const browserLaneDoctorRunningRef = useRef(false);
  const browserLaneDoctorRanForKeyRef = useRef("");
  const browserResourceProbeRanRef = useRef("");
  useEffect(() => () => { if (webPreviewRetryTimer.current) clearTimeout(webPreviewRetryTimer.current); }, []);
  // Elapsed + last-output heartbeat.
  //
  // A spinner that never changes reads as HUNG, and a first web compile can
  // legitimately run for minutes. Users reported not knowing "whether it's
  // going to load or not" — which is a trust problem, not a cosmetic one. The
  // fix is to distinguish SLOW from STUCK, and the only honest signal for that
  // is when the agent last said anything: output flowing = alive, output
  // silent for a while = worth telling the user plainly.
  const [webPreviewStartedAt, setWebPreviewStartedAt] = useState<number | null>(null);
  const [webPreviewLastLogAt, setWebPreviewLastLogAt] = useState<number | null>(null);
  const [previewNowTick, setPreviewNowTick] = useState(Date.now());
  // The named capability gap behind a failed preview, when the failure is one
  // (missing Flutter/toolchain). Produced by the agent (capability_gap.go),
  // carried on the 412 body, the /dev/events error frame AND /dev/status —
  // whichever arrives first wins, because the async spawn failure can only ever
  // reach us on the stream. `gapFixRunning` holds the install's live output so
  // the download narrates itself instead of hiding behind a spinner.
  const [previewGap, setPreviewGap] = useState<CapabilityGap | null>(null);
  const [gapFixRunning, setGapFixRunning] = useState(false);
  const [gapFixStartedAt, setGapFixStartedAt] = useState<number | null>(null);
  const gapFixCancelRef = useRef<(() => void) | null>(null);
  // What to re-run when the fix reports ok — "return them to what they were
  // doing" is half the contract; an install that leaves the user on the same
  // error panel has only done half the job.
  const gapRetryRef = useRef<(() => Promise<void>) | null>(null);
  const resetWebPreview = useCallback(() => {
    setPreviewGap(null);
    webPreviewRetryRef.current = 0;
    webPreviewErroredRef.current = false;
    setWebPreviewContentLoaded(false);
    setWebPreviewFailed(false);
    setWebPreviewLogs([]);
    setWebPreviewProbe(null);
    webPreviewProbeRef.current = null;
    setBrowserLaneProbe(null);
    browserLaneDoctorRunningRef.current = false;
    browserLaneDoctorRanForKeyRef.current = "";
    browserResourceProbeRanRef.current = "";
    webPreviewRenderWatchdogFiredRef.current = false;
    setWebPreviewStartedAt(Date.now());
    setWebPreviewLastLogAt(null);
  }, []);
  // 1s tick only while the overlay is actually up — never a background timer.
  useEffect(() => {
    if (!showWebView || webPreviewContentLoaded) return;
    const id = setInterval(() => setPreviewNowTick(Date.now()), 1000);
    return () => clearInterval(id);
  }, [showWebView, webPreviewContentLoaded]);
  // Keep the elapsed counter honest while a fix downloads — a 1.2 GB SDK with
  // no clock on it is indistinguishable from a hang.
  useEffect(() => {
    if (!gapFixRunning) return;
    const id = setInterval(() => setPreviewNowTick(Date.now()), 1000);
    return () => clearInterval(id);
  }, [gapFixRunning]);
  useEffect(() => () => gapFixCancelRef.current?.(), []);

  /**
   * Run the gap's fix: POST its path, stream the output into the panel the user
   * is already staring at, then re-run what they were trying to do.
   *
   * Everything here is driven off the typed route the agent shipped — no tool
   * name is hardcoded, no endpoint is guessed. Teaching the agent a new gap
   * lights this button up with no change on this screen.
   */
  const startGapFix = useCallback((gap: CapabilityGap) => {
    if (gapFixRunning) return;
    setGapFixRunning(true);
    setGapFixStartedAt(Date.now());
    setWebPreviewFailed(true);
    gapFixCancelRef.current = runCapabilityGapFix(quicClient as any, gap, {
      onLine: (line) => {
        setWebPreviewLastLogAt(Date.now());
        const ln = String(line).trimEnd();
        if (ln) setWebPreviewLogs((p) => appendPreviewLogLine(p, ln));
      },
      onDone: (ok, error) => {
        gapFixCancelRef.current = null;
        setGapFixRunning(false);
        if (!ok) {
          setWebPreviewLogs((p) => appendPreviewLogLine(p, `[install] failed: ${error || "unknown error"}`));
          return;
        }
        setPreviewGap(null);
        setWebPreviewLogs((p) => appendPreviewLogLine(p, "[install] done — restarting the preview…"));
        if (gapRetriesAfterFix(gap) && gapRetryRef.current) {
          setWebPreviewFailed(false);
          void gapRetryRef.current().catch((e) => {
            setWebPreviewFailed(true);
            setWebPreviewLogs((p) => appendPreviewLogLine(p, `[retry] ${e instanceof Error ? e.message : String(e)}`));
          });
        }
      },
    });
  }, [gapFixRunning]);
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
    setMobileDiscovery(null);
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
    void previewClient.refreshMobileProjects().catch(() => {});
  }, [activeDevice?.id, isConnected]);

  // Poll dev server status + all projects.
  //
  // Keyed to the DEVICE, not the focused client's mood: the old gate was
  // `isConnected` (connectionStatus === "connected"), which is fed by a
  // focus-bound listener and can sit at "connecting" after a relay restart
  // while the box's own pooled client is back up. That froze this poll and
  // stranded the preview modal on "Waiting for the dev server to report its
  // address…" while /dev/status on the box reported running+serving. Gate on
  // the pool (the header's transport truth) and talk to the active device's
  // OWN pooled client so the poll can never be answered by a stale or
  // fallback focus.
  const devStatusPollEnabled = shouldPollDevStatus({
    activeDeviceId: activeDevice?.id,
    connectedDeviceIds,
  });
  useEffect(() => {
    const deviceId = activeDevice?.id;
    if (!devStatusPollEnabled || !deviceId) return;
    const client = connectionManager.clientFor(deviceId);
    let mounted = true;

    const pollStatus = async () => {
      try {
        const [status, session] = await Promise.all([
          client.getDevServerStatus(),
          client.getMobileWorkerPreviewSession(),
        ]);
        if (mounted) {
          setDevStatus((previous) => reconcilePreviewDevStatus(previous, status, showWebViewRef.current));
        }
        if (mounted) setWorkerSession(session);
      } catch {
        if (mounted) {
          setDevStatus((previous) => reconcilePreviewDevStatus(previous, null, showWebViewRef.current));
        }
        if (mounted) setWorkerSession(null);
      }
    };

    const fetchProjects = async () => {
      try {
        const [projectsData, reposData] = await Promise.all([
          client.listMobileProjectsDetailed(),
          client.listWorkspaceRepos().catch(() => ({ repos: [] })),
        ]);
        if (!mounted) return;
        let projectRows = projectsData.projects;
        // If the mobile-specific scanner is still walking or came back empty,
        // fall back to the general project index. This is deliberately additive:
        // /projects/mobile remains the best source for launch metadata, but the
        // user must never stare at "No projects yet" when the agent can already
        // list repos/projects through the generic index (observed in the
        // Selenium/RN-web path on 2026-07-25).
        if (projectRows.length === 0 || projectsData.discovery?.discovering) {
          try {
            const fallback = await client.listProjectsDetailed();
            projectRows = mergeProjectRows([...projectRows, ...(fallback.projects as ProjectItem[])], []);
          } catch {}
        }
        const merged = mergeProjectRows(projectRows, reposData.repos);
        const launchableProjects = dogfoodCheckout
          ? merged.filter((project) => !isPathInsideAttachedDogfoodCheckout(project.path, dogfoodCheckout))
          : merged;
        setProjects(launchableProjects);
        setProjectsDiscovering(!!projectsData.discovery?.discovering && launchableProjects.length === 0);
        setMobileDiscovery(projectsData.discovery ?? null);
        setRepos([]);
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
  }, [devStatusPollEnabled, dogfoodCheckout, projectsDiscovering, activeDevice?.id]);

  // SSE auto-reload
  // Shake -> feedback SDK, for the BROWSER lane.
  //
  // The native container's shake is gated to Hermes guests, so a shake while a
  // Flutter/web app is previewed in a WebView did nothing. DevPreview.tsx had
  // this bridge; this screen — the one the Projects tab actually opens — never
  // did, so shake was dead here. Same drift as the startup heartbeat.
  //
  // Forward it INTO the WebView and dispatch every signal a guest SDK might be
  // listening for: the web SDK (sdk/feedback/web) and the Flutter SDK's web
  // build both watch for the CustomEvent / postMessage; __yaverFeedbackLaunch
  // covers an app that registered a direct hook.
  useEffect(() => {
    if (!showWebView) return;
    setActivePreviewLane("browser");
    const unsub = subscribeBrowserShake(() => {
      webViewRef.current?.injectJavaScript(`(function(){try{
        var d={source:'shake',ts:Date.now()};
        window.dispatchEvent(new CustomEvent('yaver-feedback:launch',{detail:d}));
        window.postMessage({type:'yaver-feedback:launch',source:'shake'}, '*');
        if(typeof window.__yaverFeedbackLaunch==='function'){window.__yaverFeedbackLaunch('shake');}
      }catch(e){}})(); true;`);
    });
    return () => { unsub(); setActivePreviewLane(null); };
  }, [showWebView]);

  // Stream the agent's dev-server output into the preview overlay.
  //
  // This used to require devStatus.running, which is precisely backwards: while
  // the overlay says "Starting flutter dev server…" the status is BUILDING, not
  // running, so the stream never opened during the one phase where the user has
  // nothing to look at but a spinner. The logs only ever appeared after the
  // server was already up — or in the failure panel, after it was too late.
  // The overlay below already renders webPreviewLogs; it was simply never fed.
  // Gate on active (running OR building), the same predicate the status poll
  // uses, so a first web compile narrates itself.
  useEffect(() => {
    // Open the stream whenever the preview is open — do NOT gate it on
    // devStatus. The status poll can lag, return null between agent restarts, or
    // report a launching server as inactive; gating the stream on it meant the
    // overlay had no information during precisely the phase that needs it. The
    // stream itself is the better signal: its first frame proves the box is
    // talking.
    if (!showWebView) return;

    // Use the client's shared reconnecting SSE lane. A direct one-shot XHR
    // made a relay hiccup look like the whole preview had failed, even though
    // /dev/status and the browser route were healthy. Status polling remains
    // authoritative; logs are useful narration, never a prerequisite to paint.
    const unsubscribe = subscribeProjectPreviewOutput(previewClient, (sharedLines, event: any) => {
                setWebPreviewLastLogAt((prev) => prev ?? Date.now());
                if (sharedLines.length) {
                  setWebPreviewLastLogAt(Date.now());
                  setWebPreviewLogs((previous) => {
                    if (event.type === "snapshot") {
                      const fresh = sharedLines.filter((line) => !previous.includes(line));
                      return fresh.length ? [...previous, ...fresh].slice(-MAX_WEB_PREVIEW_LOGS) : previous;
                    }
                    return sharedLines.reduce((next, line) => appendPreviewLogLine(next, line), previous);
                  });
                }
                if (event.type === "reload" || event.type === "ready") {
                  setWebViewKey(k => k + 1);
                  setWebViewLoading(true);
                }
                if (event.type === "snapshot") {
                  if (event.snapshot?.previewHealth) {
                    setDevStatus((prev) => prev ? { ...prev, previewHealth: event.snapshot.previewHealth } : prev);
                  }
                }
                if (event.type === "heartbeat") {
                  // Proof of life even when nothing new is printed. It keeps
                  // "last output Ns ago" honest instead of letting a healthy but
                  // quiet compile read as a stall.
                  setWebPreviewLastLogAt((prev) => prev ?? Date.now());
                }
                if (event.type === "error") {
                  setWebPreviewFailed(true);
                  // THE ROUTE. mgr.Start returns before the process is spawned,
                  // so a missing toolchain ("exec flutter: executable file not
                  // found") can ONLY arrive here — no 412 can catch it. Before
                  // this branch the phone rendered that fact as a log line under
                  // an alert icon with no button, while POST /install/flutter
                  // worked the whole time.
                  const gap = capabilityGapFromDevEvent(event);
                  if (gap) setPreviewGap(gap);
                }
      }, (health) => {
          if (!health) {
            setWebPreviewLastLogAt(Date.now());
            return;
          }
          if (health.kind === "lost") {
            setWebPreviewLogs((p) => appendPreviewLogLine(
              p,
              "[logs] Live output is unavailable; preview startup continues via status checks.",
            ));
          }
        });
    return unsubscribe;
    // Re-subscribe only when the preview opens or its selected box changes.
    // Depending on devStatus would still tear this stream down every poll.
  }, [showWebView, previewClient]);

  async function openRunningPreview() {
    // The running card already represents a measured active route. Open from
    // that state immediately; a fresh status request is advisory and can hang
    // behind a reconnecting relay/SSE lane. Before this split, the tap looked
    // like a no-op until that request happened to finish.
    let usable = canOpenPreviewBeforeRefresh(devStatus) ? devStatus : null;
    if (usable) {
      void previewClient.getDevServerStatus().then((fresh) => {
        const refreshed = usablePreviewDevStatus(fresh, devStatus);
        if (refreshed) setDevStatus(refreshed);
      }).catch(() => {});
    } else {
      const fresh = await previewClient.getDevServerStatus();
      usable = usablePreviewDevStatus(fresh, devStatus);
    }
    if (usable) setDevStatus(usable);
    // The tablet studio is also the loading surface. Navigate immediately so
    // the device frame and vibe console remain visible while the box starts
    // or recovers its browser lane.
    if (layout.isTablet) {
      router.push({
        pathname: "/vibe-studio",
        // The Studio resolves projects from their real box-side identity. A
        // display label such as "sfmg / mobile" is not a path and previously
        // produced a false "project isn't on the connected box" dead end.
        params: { project: usable?.workDir || currentProject?.path || runningProject || "" },
      });
      return;
    }
    if (!usable || !isActiveDevServerStatus(usable)) {
      const known = usable as DevServerStatus | null;
      Alert.alert(
        "Preview needs attention",
        known?.error ||
          known?.servingLabel ||
          "The remote box does not currently have a browser preview ready. Start it again from Projects.",
      );
      return;
    }
    if (shouldUseNativePreview(usable, isBundleLoaderAvailable())) {
      handleOpenNative(usable.workDir!, usable.framework);
      return;
    }
    resetWebPreview();
    setWebViewLoading(true);
    setWebViewKey((k) => k + 1);
    setShowWebView(true);
  }

  // Tap project → if dev server running, always use Hermes push (fast, ~10s).
  // This keeps iPhone testing working from Linux, WSL, and remote hosts.
  // Xcode native build is available via "Install Native" action in the sheet.
  const handleTapProject = useCallback(async (projectOrQuery: ProjectItem | string) => {
    const selectedProject = typeof projectOrQuery === "string"
      ? findProjectMatch(projects, projectOrQuery)
      : projectOrQuery;
    const projectName = selectedProject?.name || (typeof projectOrQuery === "string" ? projectOrQuery : projectOrQuery.name);
    const projectPath = selectedProject?.path || "";
    const isRunning = !!projectPath && sameProjectPath(devStatus?.workDir, projectPath);
    if (isRunning) {
      await openRunningPreview();
      return;
    }

    setLoadingActions(true);
    try {
      const result = projectPath
        ? await previewClient.getProjectActionsByPath(projectPath)
        : await previewClient.getProjectActions(projectName);
      let compatibility: DevCompatibilityStatus | null = null;
      const hermesFramework = result.actions.find((a: any) => isHermesMobileFramework(a.framework))?.framework;
      const secondClassFramework = result.actions.find((a: any) => isSecondClassMobileFramework(a.framework))?.framework;
      if (isHermesMobileFramework(hermesFramework)) {
        try {
          const availableModules = await getAvailableModules();
          compatibility = await previewClient.getDevCompatibility(result.path, availableModules);
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
      //   Browser Reload — first-class browser/WebView lane; no Hermes.
      //   Hermes Reload  — RN/Expo only (loads the real bundle into the Yaver
      //                    container on THIS phone).
      //   WebRTC Reload  — universal: RN, Flutter, Swift/iOS, Kotlin/Android
      //                    (streams the app from the dev box).
      const fw = hermesFramework || secondClassFramework ||
        result.actions.find((a: any) => a.framework)?.framework || "";
      const isRN = isHermesMobileFramework(hermesFramework);

      // Exactly three lanes, no Flush:
      //   Browser — first for RN/Expo too. It serves the web target in a
      //             WebView and must never compile/push a Hermes bundle.
      //   Hermes  — RN/Expo ONLY (loads the JS bundle into the container).
      //   WebRTC  — ALL stacks (streams the app from the box).
      const reloadLanes: any[] = [];
      reloadLanes.push({
        label: "Browser Reload", target: ".", type: "dev-server", icon: "\u{1F310}",
        framework: fw, platform: Platform.OS, supported: true,
      });
      if (isRN) {
        reloadLanes.push({
          label: "Hermes Reload", target: ".", type: "open-native", icon: "\u{1F4F1}",
          framework: hermesFramework, platform: Platform.OS,
          supported: compatibility?.compatible !== false, reason: compatibility?.errors?.[0],
        });
      }
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
          { workDir: result.path || projectPath, projectName, platform: Platform.OS, hasPairedDevice: true },
        );
        composed = applyPreviewCapabilities(composed, caps);
      } catch {
        /* older agent — keep the locally composed lanes */
      }
      // Monorepo "pick a sub-app" step: a workspace with several apps
      // (yaver.io → mobile · expo / web · next) offers each one as its own
      // browser lane, exactly like web's target discovery. Best-effort —
      // an older agent without /workspace/apps changes nothing.
      try {
        const apps = await previewClient.getWorkspaceApps(undefined, result.path || projectPath);
        composed = [...composed, ...workspaceAppLanes(apps as any)];
      } catch {
        /* no workspace manifest or older agent — no sub-app step */
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
    return openAppBus.subscribe((intent) => {
      handleTapProject(intent.projectPath || intent.app).catch(() => {
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
      // Tablet has one canonical workspace: preview + conversation in
      // landscape, chat + preview peek in portrait. Every tablet entry point
      // must land on the same stateful surface.
      if (layout.isTablet) {
        router.push({
          pathname: "/vibe-studio",
          params: { project: path || project },
        });
        return;
      }
      // Open vibing mode — delay to let action sheet modal fully close first
      setTimeout(async () => {
        try {
          const state = await codingClient.getVibingState(project);
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

    if (action.type === "wire-push") {
      // Agent-offered lane (project_preview_options): a phone/tablet is
      // USB-attached to the BOX, so the box can native-build + install the
      // app on it. The box does the work; dispatch it as a task so the
      // build streams into Tasks like any other box-side operation.
      await sendTaskOrWarn(
        `Install ${project} on the USB-connected device`,
        `Run \`yaver wire push\` in ${path}: build ${project} for the USB-attached device and install it. ` +
          "Stream the build output and report the installed bundle id — or the failure verbatim, never a summary.",
        "Install via USB",
      );
      return;
    }

    if (action.type === "dev-server") {
      // Direct dev server start — use the exact target path (handles monorepos like talos/mobile)
      setStartingProject(project);
      const targetPath = action.target === "." ? path : `${path}/${action.target}`.replace(/\/+$/, "");
      let deferStartingClear = false;
      // One closure for "start this preview", so the capability-gap fix can
      // re-issue the EXACT request that was refused instead of approximating it.
      const startThisPreview = () => startBrowserProjectLane(previewClient, {
        framework: action.framework || "",
        workDir: targetPath,
        // Browser Reload = the browser lane. Serve the web target, never a
        // Hermes native bundle (Hermes needs the guest's native modules to
        // match the container — sfmg dies on expo-gl — has no meaning for
        // Flutter, and is blocked for Yaver-self-dev).
        targetDeviceId: selectedTarget?.id,
        targetDeviceName: selectedTarget?.name,
        targetDeviceClass: selectedTarget?.deviceClass,
      });
      gapRetryRef.current = async () => {
        await startThisPreview();
        setWebViewKey((k) => k + 1);
        setWebViewLoading(true);
      };
      try {
        await startThisPreview();
        // Rendering is NOT a task. Do NOT spawn a coding task or navigate to
        // Tasks — the agent starts the dev server directly (it handles flutter
        // web / expo web itself). Open the full-screen browser preview RIGHT HERE
        // so the user sees the remote runtime come up (WebView + loading bar +
        // Reload), instead of the action sheet closing to a blank Projects list
        // with the progress hidden on the Tasks tab. The WebView auto-retries
        // while the web server is still compiling (onError below).
        setActionSheet(null);
        resetWebPreview();

        // Pull status BEFORE opening. bundleUrl is derived from devStatus, and
        // devStatus otherwise only arrives on an independent 3s poll — so the
        // WebView would mount with uri:"" and issue no request at all. That is
        // silent: no request means no onError/onHttpError, so the retry counter
        // never increments and the failure panel is unreachable. The user gets
        // an overlay that never lifts. Cheap call, removes a whole dead end.
        try {
          const st = await previewClient.getDevServerStatus();
          if (isActiveDevServerStatus(st)) setDevStatus(st);
        } catch { /* the poll will catch up; the empty-url guard below covers us */ }

        setWebViewKey((k) => k + 1);
        setWebViewLoading(true);
        setShowWebView(true);
      } catch (e) {
        // The agent's structured refusal. `capabilityGap` carries the whole
        // route — the sentence, the endpoint, the stream and the estimate — so
        // this branch no longer hardcodes a tool name or a copy line. It used to
        // say "Install Node LTS into ~/.yaver/runtimes/node" for bun, pnpm AND
        // (had it ever been reachable) Flutter: a route that worked wrapped in a
        // sentence that lied.
        const gap = capabilityGapFromError(e);
        if (gap) {
          deferStartingClear = true;
          setActionSheet(null);
          resetWebPreview();
          setPreviewGap(gap);
          setWebPreviewFailed(true);
          setWebPreviewLogs([gapTitle(gap), gapBody(gap)].filter(Boolean));
          // Open the preview overlay: the card belongs where the user was
          // heading, not in an Alert that dismisses to an unchanged Projects
          // list. The overlay renders the named cause + the Install button.
          setShowWebView(true);
          setStartingProject(null);
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
  }, [actionSheet, selectedTarget, isConnected, connectionStatus, sendTaskOrWarn, layout.isTablet, router]);

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
    previewClient.getDevServerTarget()
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
      const build = await previewClient.startBuild("xcode-device-install", devStatus.workDir, true);
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
                await codingClient.sendTask(deploy.prompt(projectName, workDir), `[Store Deploy] ${projectName} · ${framework}`);
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
        const currentStatus = await previewClient.getDevServerStatus();
        if (currentStatus?.running && sameProjectPath(currentStatus.workDir, workDir) && currentStatus.framework === "flutter") {
          await previewClient.reloadDevServer();
          setQuickActionStatus("Flutter reload sent");
          Alert.alert("Flutter Flushed", "A Flutter reload was sent over LAN.");
        } else {
          await previewClient.startDevServer({
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
        const build = await previewClient.startBuild("xcode-device-install", workDir, true);
        setBuildStatus("running");
        let consecutivePollFailures = 0;
        for (let i = 0; i < 120; i++) {
          await new Promise((resolve) => setTimeout(resolve, 2000));
          let b: Awaited<ReturnType<typeof previewClient.getBuild>> | null = null;
          try {
            b = await previewClient.getBuild(build.id);
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
        const build = await previewClient.startBuild("gradle-apk", workDir);
        setBuildStatus("running");
        let consecutivePollFailures = 0;
        for (let i = 0; i < 180; i++) {
          await new Promise((resolve) => setTimeout(resolve, 2000));
          let b: Awaited<ReturnType<typeof previewClient.getBuild>> | null = null;
          try {
            b = await previewClient.getBuild(build.id);
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
                previewClient.baseUrl,
                previewClient.getAuthHeaders(),
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
    const currentStatus = await previewClient.getDevServerStatus();
    // `running` alone is NOT "the lane I need is up".
    //
    // A dev server for this workDir can be serving the WEB target (caller
    // "web-ui") or the Hermes/native one (caller "mobile"). This guard used to
    // return early on running + matching workDir, so asking for one lane while
    // the OTHER was already up silently did nothing: the caller then waited on
    // a surface nobody had started.
    //
    // Observed 2026-07-25 on a real iPhone — Metro up for sfmg, browser preview
    // tapped, and the agent answered `running: true, servingLabel: "Serving
    // expo preview", webPort: null` while /dev-web/ returned
    //   503 {"error":"no Expo Web preview running — POST /dev/web-preview/start"}
    // The phone sat on "Starting expo dev server… 1:04 elapsed" forever. Both
    // sides were telling the truth about DIFFERENT lanes.
    //
    // So compare the lane, not just the flag. Wrong lane ⇒ fall through and
    // start the right one.
    if (currentStatus?.running && sameProjectPath(currentStatus.workDir, workDir) && !isWebServedStatus(currentStatus)) {
      return;
    }

    setLoadingStatus("Starting dev server...");
    setBuildProgress(0.05);
    await previewClient.startDevServer({
      framework: framework || "expo",
      workDir,
      targetDeviceId: selectedTarget?.id,
      targetDeviceName: selectedTarget?.name,
      targetDeviceClass: selectedTarget?.deviceClass,
    });

    for (let i = 0; i < 30; i++) {
      await new Promise((resolve) => setTimeout(resolve, 1000));
      const status = await previewClient.getDevServerStatus();
      setLoadingStatus(status?.running ? "Dev server ready" : "Starting dev server...");
      if (status?.running && sameProjectPath(status.workDir, workDir)) return;
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
        "This build of Yaver can't mount project bundles. Update Yaver to the latest version — or run the app directly on the dev machine.",
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

    // Hermes build progress over the shared XHR SSE client. This used to be a
    // fetch().body.getReader() reader too — undefined in React Native — so the
    // Hermes lane ALSO never showed a progress line: the bar moved on local
    // guesses while the live tail under it stayed empty for every build.
    // Held in a box, not a `let`: TS narrows a closure-assigned local back to
    // `null` at the call sites below.
    const buildSse: { current: SseSubscription | null } = { current: null };
    const listenSSE = () => {
      buildSse.current = subscribeSse({
        url: `${baseUrl}/dev/events`,
        headers: (quicClient as any).authHeaders,
        onError: (reason) => setBundlerLine(`build log stream unavailable: ${reason}`),
        onEvent: (event: any) => {
            {
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
            }
        },
      });
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
      buildSse.current?.close();
      setNativeLoading(false);
      setBuildProgress(0);
      setLoadingStatus("");
      const raw = err?.message || "Could not build Hermes bundle in Yaver";
      const lower = raw.toLowerCase();
      const buildResult = err?.buildResult;
      // One classifier, in a file a test can import. See buildFailureHint for
      // the substring trap this replaced and the phone screenshot that proved it.
      let hint = buildFailureHint(buildResult, raw);
      if (!hint && (lower.includes("network") || lower.includes("fetch") || lower.includes("timeout"))) {
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
      // ROUTE-TO-FIX, not a sentence describing one.
      //
      // The agent sends `remedy` on this 409 — a machine-readable name for the
      // lane that DOES work. Until now the phone rendered the prose (which even
      // says "use the browser/WebRTC preview") next to a single OK button,
      // while both of those lanes exist as real, working buttons one screen
      // back. That is the failure CLAUDE.md's worked example is about: a remedy
      // string naming a control the user cannot reach from where they are.
      //
      // Measured on TestFlight build 500, 2026-08-03 — tapping `mobile` gave
      // "Load Failed" + "Tamam", and the way forward was two taps away behind
      // a dismissal.
      const remedyLane =
        buildResult?.remedy === "stream-over-webrtc" ? "remote-runtime" :
        buildResult?.remedy === "browser-preview" ? "dev-server" : "";
      if (remedyLane) {
        Alert.alert(title, `${raw}${hint}`, [
          { text: "Not now", style: "cancel" },
          {
            text: remedyLane === "remote-runtime" ? "Stream over WebRTC" : "Open browser preview",
            // Dispatched through handleExecuteAction — the SAME function the
            // real "WebRTC Reload" / "Browser Reload" buttons call. A second
            // implementation here would be one more copy to drift, which is
            // how this repo shipped three different relay-auth matchers.
            onPress: () => {
              void handleExecuteAction({
                label: remedyLane === "remote-runtime" ? "WebRTC Reload" : "Browser Reload",
                target: ".",
                type: remedyLane,
                framework: devStatus?.framework || "",
                platform: Platform.OS,
                supported: true,
              });
            },
          },
        ]);
        return;
      }
      Alert.alert(title, `${raw}${hint}`);
      return;
    }
    buildSse.current?.close();
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

  /** Fast/full reload split (agent 1.99.374+). Mirrors DevPreview.tsx —
   *  the app's OTHER browser-preview implementation; a fix in one is not
   *  a fix (cross-surface parity rule).
   *  fast — cheapest refresh: Metro/Expo reload, Flutter "r", fresh web
   *         bundle re-served. Sub-second.
   *  full — Flutter "R" hot restart / forced web-bundle re-export (warm
   *         cache) on the browser lane; Hermes rebuild + push on the
   *         native lane. */
  const handleReload = useCallback(async (kind: "fast" | "full" = "fast") => {
    const nativeHermes = shouldUseNativePreview(devStatus || {}, isBundleLoaderAvailable());
    if (!nativeHermes) {
      setWebViewLoading(true);
    }
    const mode = nativeHermes ? (kind === "full" ? "bundle" : "fast") : kind;
    const result = await previewClient.reloadDevServerDetailed({
      mode,
      allowBundleFallback: nativeHermes,
    });
    if (!devReloadReachedTarget(result)) {
      setWebViewLoading(false);
      Alert.alert("Reload failed", describeDevReloadResult(result));
      return false;
    }
    setQuickActionStatus(describeDevReloadResult(result));
    setTimeout(() => setQuickActionStatus(null), 3000);
    if (!nativeHermes) {
      setWebViewKey(k => k + 1);
    }
    return true;
  }, [devStatus?.framework, previewClient]);

  const handleRequestScreenshot = useCallback(async () => {
    await previewClient.sendMobileWorkerPreviewCommand("capture_screenshot", {
      reason: "apps-control-plane",
    });
  }, [previewClient]);

  const handleStop = useCallback(() => {
    if (stopPhase === "stopping") return;
    setShowStopConfirm(true);
  }, [stopPhase]);

  // WHICH url the preview loads — previewBundlePath (shared with
  // DevPreview.tsx) applies the agent-is-authority rule, the single legacy
  // "/dev/"+webPort override, and the empty-url guard.
  const reportedBundlePath = previewBundlePath(devStatus as any);
  const bundleUrl =
    devStatus && reportedBundlePath
      ? previewClient.getDevServerBundleUrl(reportedBundlePath)
      : "";
  const webPreviewServerLooksReady =
    (devStatus ? isWebServedStatus(devStatus) : false) ||
    webPreviewLogs.some((line) => /\b(listening|serving on|compiled|ready|running)\b/i.test(line));
  // Keep the Projects/browser implementation on the same narration contract
  // as DevPreview.tsx. The two screens are separate React trees; sharing the
  // pure helper prevents SFMG from saying "loading page" on one surface while
  // the other reports the actual compile line and heartbeat.
  const narratedPreviewWait = previewWaitLine({
    contentLoaded: webPreviewContentLoaded,
    startedAt: webPreviewStartedAt,
    lastOutputAt: webPreviewLastLogAt,
    now: previewNowTick,
    logs: webPreviewLogs,
    workDir: devStatus?.workDir,
  });

  const runBrowserLaneDoctor = useCallback((reason: string) => {
    if (!showWebView || !bundleUrl || webPreviewContentLoaded) return;
    const key = `${bundleUrl}|${reason}`;
    if (browserLaneDoctorRunningRef.current || browserLaneDoctorRanForKeyRef.current === key) return;
    browserLaneDoctorRunningRef.current = true;
    browserLaneDoctorRanForKeyRef.current = key;
    setWebPreviewLogs((prev) => appendPreviewLogLine(prev, `[doctor] probing browser lane after ${reason}…`));
    void doctorBrowserLane(previewClient, 45).then((probe) => {
      if (!probe) {
        setWebPreviewLogs((prev) => appendPreviewLogLine(prev, "[doctor] browser lane probe unavailable"));
        return;
      }
      const verifiedProbe = reconcileBrowserLaneProbe(probe, webPreviewProbeRef.current);
      setBrowserLaneProbe(verifiedProbe);
      setWebPreviewLogs((prev) => appendPreviewLogLine(prev, browserLaneProbeLine(verifiedProbe)));
      if (!verifiedProbe.ok) {
        setWebPreviewFailed(true);
      }
    }).catch((err) => {
      setWebPreviewLogs((prev) => appendPreviewLogLine(prev, `[doctor] browser lane probe failed: ${err instanceof Error ? err.message : String(err)}`));
    }).finally(() => {
      browserLaneDoctorRunningRef.current = false;
    });
  }, [bundleUrl, previewClient, showWebView, webPreviewContentLoaded]);

  // A later phone probe is stronger than an earlier box-local doctor result.
  // This also closes the race where the doctor completes just before the
  // WebView reports its still-empty #root.
  useEffect(() => {
    webPreviewProbeRef.current = webPreviewProbe;
    if (!browserLaneProbe || !webPreviewProbe) return;
    const verifiedProbe = reconcileBrowserLaneProbe(browserLaneProbe, webPreviewProbe);
    if (verifiedProbe === browserLaneProbe) return;
    setBrowserLaneProbe(verifiedProbe);
    setWebPreviewFailed(true);
    setWebPreviewLogs((prev) => appendPreviewLogLine(prev, browserLaneProbeLine(verifiedProbe)));
  }, [browserLaneProbe, webPreviewProbe]);

  // ── Capability gap card ────────────────────────────────────────────────
  // The gap can reach us on three carriers and we take whichever we have: the
  // 412 body (synchronous refusal), the /dev/events error frame (the async
  // spawn failure the 412 can never catch), or /dev/status (a poll that
  // outlives a torn-down stream). One object, one card, one button.
  const activeGap = previewGap || capabilityGapFromStatus(devStatus);
  const paintGateMode = previewPaintGateMode(devStatus, {
    contentLoaded: webPreviewContentLoaded,
    failed: webPreviewFailed,
    probeUnavailable,
  });
  const activeGapFixLabel = gapFixLabel(activeGap);
  const gapCard = activeGap ? (
    <View style={s.gapCard}>
      <Ionicons name="construct-outline" size={34} color={c.warn} />
      <Text style={[s.previewFailTitle, { color: c.textPrimary }]}>{gapTitle(activeGap)}</Text>
      {gapBody(activeGap) ? (
        <Text style={[s.previewSubtle, { color: c.textMuted, textAlign: "left" }]} selectable>
          {gapBody(activeGap)}
        </Text>
      ) : null}
      {/* The headroom on the surface where the decision is made — "3.2 GB free
          · needs 3.0 GB" BEFORE a ten-minute download, not after it fails. */}
      {gapHeadroomLine(activeGap) ? (
        <Text style={[s.previewSubtle, { color: c.textMuted, textAlign: "left", fontSize: 11 }]} selectable>
          {gapHeadroomLine(activeGap)}
        </Text>
      ) : null}
      {/* A WARNING is not a refusal: it renders above a button that STAYS. */}
      {gapWarning(activeGap) ? (
        <Text style={[s.previewSubtle, { color: c.warn, textAlign: "left" }]} selectable>
          {gapWarning(activeGap)}
        </Text>
      ) : null}
      {activeGapFixLabel ? (
        <Pressable
          onPress={() => startGapFix(activeGap)}
          disabled={gapFixRunning}
          accessibilityRole="button"
          accessibilityLabel={activeGapFixLabel}
          style={[s.previewBtn, s.gapFixBtn, { backgroundColor: gapFixRunning ? "#1f2937" : "#1a2e1a" }]}
        >
          <Text style={[s.previewBtnText, { color: gapFixRunning ? c.textMuted : "#22c55e" }]}>
            {gapFixRunning
              ? `Installing… ${gapFixStartedAt ? formatFixElapsed(gapFixStartedAt, previewNowTick) : ""}`.trim()
              : activeGapFixLabel}
          </Text>
        </Pressable>
      ) : (
        // No fixer on THIS machine — say so specifically. A gap with no route
        // must still name its constraint, never render an inert button.
        <Text style={[s.previewSubtle, { color: c.warn, textAlign: "left" }]} selectable>
          {gapConstraint(activeGap) || "Yaver has no installer for this on this machine."}
        </Text>
      )}
      {/* Space is the blocker, or nearly is: ship the route that frees it. The
          Storage screen lists every path with its size and its rebuild cost and
          deletes nothing without an explicit tick. */}
      {gapReclaimLabel(activeGap) ? (
        <Pressable
          onPress={() => router.push("/storage")}
          accessibilityRole="button"
          accessibilityLabel={gapReclaimLabel(activeGap) || "Free up space"}
          style={[s.previewBtn, s.gapFixBtn, { backgroundColor: "#2a1f0a" }]}
        >
          <Text style={[s.previewBtnText, { color: c.warn }]}>{gapReclaimLabel(activeGap)}</Text>
        </Pressable>
      ) : null}
    </View>
  ) : null;

  useEffect(() => {
    if (!showWebView || !bundleUrl || paintGateMode !== "blocking") return;
    if (!webPreviewServerLooksReady || webPreviewRenderWatchdogFiredRef.current) return;
    const id = setTimeout(() => {
      if (webPreviewContentLoaded || webPreviewFailed || webPreviewRenderWatchdogFiredRef.current) return;
      webPreviewRenderWatchdogFiredRef.current = true;
      const probe = webPreviewProbe
        ? `${webPreviewProbe.reason || "waiting"} · ${webPreviewProbe.mountId ? `#${webPreviewProbe.mountId} children ${webPreviewProbe.mountChildren ?? 0}` : `body children ${webPreviewProbe.bodyChildren ?? 0}`}`
        : "no render probe message received";
      let path = bundleUrl;
      try { path = new URL(bundleUrl).pathname; } catch {}
      setWebPreviewLogs((prev) => appendPreviewLogLine(prev, `[preview] server is listening but the WebView did not render after 20s (${path}; ${probe})`));
      setWebPreviewFailed(true);
      runBrowserLaneDoctor("ready-without-render");
    }, 20000);
    return () => clearTimeout(id);
  }, [showWebView, bundleUrl, paintGateMode, webPreviewContentLoaded, webPreviewFailed, webPreviewServerLooksReady, webPreviewProbe, runBrowserLaneDoctor]);

  const visibleProjects = projects.filter((p) => {
    if (search.trim()) {
      const q = search.toLowerCase();
      const match = projectTerms(p).some((term) => term.toLowerCase().includes(q));
      if (!match) return false;
    }
    if (activeFilter) {
      return getProjectCategory(p) === activeFilter;
    }
    return true;
  });
  const activeFilterLabel = activeFilter
    ? activeFilter[0].toUpperCase() + activeFilter.slice(1)
    : "All";
  const scanDiagnosticLine = mobileDiscovery
    ? [
        typeof mobileDiscovery.scanMs === "number" ? `scan ${Math.round(mobileDiscovery.scanMs / 100) / 10}s` : "",
        mobileDiscovery.timedOut ? "timed out" : "",
        mobileDiscovery.permDenied ? `${mobileDiscovery.permDenied} permission-denied dirs` : "",
        mobileDiscovery.scanError || "",
      ].filter(Boolean).join(" · ")
    : "";

  if (codingMode === "local-only") {
    const localSlugs = new Set(remotelessPhoneProjects.map((project) => project.slug.toLowerCase()));
    const providerProjects = remotelessProviderProjects.filter((project) => !localSlugs.has(project.name.toLowerCase()));
    return (
      <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <RemoteBoxBanner />
        <ScrollView
          contentContainerStyle={[tabletContent, { padding: 24, paddingBottom: 100 }]}
          refreshControl={<RefreshControl refreshing={remotelessLoading} onRefresh={loadRemotelessProjects} tintColor={c.accent} />}
        >
          <Text style={{ color: c.textSecondary, fontSize: 15, marginBottom: 20 }}>
            No remote box · checkouts and connected Git providers on this phone
          </Text>
          <View style={{ backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 16, padding: 16, marginBottom: 18 }}>
            <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "700" }}>Phone-local workspace</Text>
            <Text style={{ color: c.textSecondary, fontSize: 13, marginTop: 8 }}>
              DeepSeek can audit and edit cloned repositories. Git status, diff, commit, and push stay available; builds, tests, shells, previews, and deploys require a remote box.
            </Text>
          </View>

          <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "700", marginTop: 8, marginBottom: 12 }}>On this phone</Text>
          {remotelessPhoneProjects.length === 0 ? (
            <Text style={{ color: c.textMuted, fontSize: 14 }}>No checkout yet. Clone one from GitHub or GitLab below.</Text>
          ) : remotelessPhoneProjects.map((project) => (
            <Pressable
              key={`phone:${project.slug}`}
              accessibilityRole="button"
              accessibilityLabel={`Open phone checkout ${project.name}`}
              onPress={() => router.push({ pathname: "/(tabs)/tasks", params: { openNew: "1", phoneCheckout: project.slug, sessionStartedFrom: "mobile-workspace" } } as any)}
              style={{ flexDirection: "row", alignItems: "center", gap: 14, backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 16, padding: 16, marginBottom: 12 }}
            >
              <Text style={{ fontSize: 24 }}>📱</Text>
              <View style={{ flex: 1, minWidth: 0 }}>
                <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "700" }} numberOfLines={1}>{project.name}</Text>
                <Text style={{ color: c.textSecondary, fontSize: 12, marginTop: 4 }}>{project.slug}</Text>
              </View>
              <Text style={{ color: c.accent, fontSize: 13, fontWeight: "700" }}>Vibe</Text>
            </Pressable>
          ))}

          <Text style={{ color: c.textPrimary, fontSize: 20, fontWeight: "700", marginTop: 24, marginBottom: 12 }}>GitHub &amp; GitLab</Text>
          {remotelessLoading && providerProjects.length === 0 ? (
            <ActivityIndicator style={{ marginTop: 20 }} color={c.accent} />
          ) : providerProjects.length === 0 ? (
            <View>
              <Text style={{ color: c.textMuted, fontSize: 14 }}>No connected provider projects found.</Text>
              <Pressable onPress={() => router.push("/(tabs)/settings" as any)} style={{ paddingVertical: 12 }} accessibilityRole="button">
                <Text style={{ color: c.accent, fontWeight: "700" }}>Connect GitHub or GitLab in Settings →</Text>
              </Pressable>
            </View>
          ) : providerProjects.map((project) => (
            <Pressable
              key={project.id}
              disabled={remotelessCloningID !== null}
              onPress={() => { void cloneRemotelessProject(project); }}
              accessibilityRole="button"
              accessibilityLabel={`Clone ${project.fullName} from ${project.provider}`}
              style={{ flexDirection: "row", alignItems: "center", gap: 14, backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 16, padding: 16, marginBottom: 12, opacity: remotelessCloningID && remotelessCloningID !== project.id ? 0.55 : 1 }}
            >
              <Text style={{ fontSize: 24 }}>{project.provider === "github" ? "◉" : "◆"}</Text>
              <View style={{ flex: 1, minWidth: 0 }}>
                <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "700" }} numberOfLines={1}>{project.fullName}</Text>
                <Text style={{ color: c.textSecondary, fontSize: 12, marginTop: 4 }}>{project.provider === "github" ? "GitHub" : "GitLab"} · {project.defaultBranch} · {project.isPrivate ? "private" : "public"}</Text>
              </View>
              {remotelessCloningID === project.id ? <ActivityIndicator color={c.accent} /> : <Text style={{ color: c.accent, fontWeight: "700" }}>Clone</Text>}
            </Pressable>
          ))}
          {remotelessError ? <Text style={{ color: c.error, fontSize: 13, marginTop: 14 }}>{remotelessError}</Text> : null}
        </ScrollView>
      </SafeAreaView>
    );
  }

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

  const devServerBelongsToAttachedDogfoodCheckout = !!devStatus?.workDir &&
    isPathInsideAttachedDogfoodCheckout(devStatus.workDir, dogfoodCheckout);
  const activeProjectPath = dogfoodProjectRootPath(devStatus?.workDir, devServerBelongsToAttachedDogfoodCheckout ? dogfoodCheckout : null);
  const currentProject = projects.find((project) => sameProjectPath(project.path, activeProjectPath || devStatus?.workDir)) ?? null;
  const runningProject = currentProject?.name ?? (pathLeaf(activeProjectPath || devStatus?.workDir || "") || devStatus?.framework || "App");
  const guestProjectName = dogfoodGuestProjectName(activeProjectPath || devStatus?.workDir, currentProject?.name || runningProject, devStatus?.framework || "Preview");
  const runningSecondClassGuidance = secondClassGuidance(devStatus?.framework, isDirectConnection);
  const devServerBuilding = devStatus?.building === true;
  const devServerBusy = nativeLoading || devServerBuilding;

  return (
    <SafeAreaView style={[s.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <RemoteBoxBanner />
      <View style={s.container}>
        {/* Running app — green card */}
        {devStatus && !devServerBelongsToAttachedDogfoodCheckout && (
          <View
            style={[
              s.card,
              s.activeCard,
              {
                backgroundColor: isDark ? c.successBg : c.surfaceMuted,
                borderColor: c.successBorder,
              },
            ]}
          >
            <View style={s.cardHeader}>
              <View style={[s.statusDot, { backgroundColor: devServerBuilding ? c.warn : c.success }]} />
              <View style={s.cardTitleContainer}>
                <Text style={[s.cardTitle, { color: c.textPrimary }]}>{runningProject}</Text>
                <Text style={[s.cardMeta, { color: c.textMuted }]}>
                  {devServerBuilding
                    ? `${devStatus.framework} · starting…`
                    : `${devStatus.framework} · browser preview`}
                </Text>
                {workerSession?.hasTarget && (
                  <Text style={[s.cardMeta, { color: workerSession.workerOnline ? c.success : c.warn }]}>
                    worker · {workerSession.workerOnline ? "online" : "offline"}
                  </Text>
                )}
              </View>
            </View>
            {/* Vibing = SEE the app. Exactly two actions: "Open" opens
                the browser preview (works for every stack — RN, Flutter, web —
                it serves the web target, not a LAN flush), and "Stop". Flush /
                Reload / Screenshots / Ship It were removed on purpose. */}
            <View style={s.cardActions}>
              <Pressable
                style={[s.actionBtn, s.openBtn, devServerBusy && { opacity: 0.5 }]}
                onPress={() => { openRunningPreview().catch((e) => Alert.alert("Open in Yaver failed", e instanceof Error ? e.message : String(e))); }}
                disabled={devServerBusy}
                accessibilityRole="button"
                accessibilityLabel={`Open ${(runningProject || "preview").split(" / ")[0]} in Yaver`}
                testID="projects-open-in-yaver"
              >
                {devServerBusy ? (
                  <>
                    <ActivityIndicator size="small" color="#000" />
                    <Text style={[s.openBtnText, { fontSize: 12, marginLeft: 6 }]}>
                      {devServerBuilding && !nativeLoading ? "Starting…" : "Building…"}
                    </Text>
                  </>
                ) : (
                  <Text style={s.openBtnText}>Open</Text>
                )}
              </Pressable>
              <Pressable style={[s.actionBtn, s.stopBtn]} onPress={handleStop} disabled={stopPhase === "stopping"}>
                <Text style={s.stopBtnText}>{stopPhase === "stopping" ? "Stopping…" : "Stop"}</Text>
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
              testID="projects-search-input"
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
            const cat = getProjectCategory(p);
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
                testID="projects-filter-all"
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
                      testID={`projects-filter-${cat}`}
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
          testID="projects-list"
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
          data={visibleProjects}
          keyExtractor={(item) => item.path}
          contentContainerStyle={[s.listContent, layout.gridCols("repos") > 1 ? null : tabletContent]}
          renderItem={({ item }) => {
            const isRunning = sameProjectPath(devStatus?.workDir, item.path) || sameProjectPath(activeProjectPath, item.path);
            const isStarting = startingProject === item.name;
            const cols = layout.gridCols("projects");

            return (
              <Pressable
                testID={`project-card-${item.name || item.path}`}
                accessibilityLabel={`Project ${item.name || item.path}`}
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
                  ) : (
                    <View style={{ alignItems: "flex-end", gap: 8 }}>
                      {isRunning ? <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Running</Text> : null}
                      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                        <Pressable
                          accessibilityRole="button"
                          accessibilityLabel={`Export ${item.name} as browser shortcut`}
                          onPress={(event) => {
                            event.stopPropagation();
                            setShortcutProject(item);
                          }}
                          hitSlop={8}
                          style={({ pressed }) => ({ width: 32, height: 32, borderRadius: 10, alignItems: "center", justifyContent: "center", backgroundColor: c.accentSoft, opacity: pressed ? 0.7 : 1 })}
                        >
                          <Ionicons name="phone-portrait-outline" size={17} color={c.accent} />
                        </Pressable>
                        {!isRunning ? <Ionicons name="chevron-forward" size={16} color={c.textMuted} /> : null}
                      </View>
                    </View>
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
            ) : projects.length > 0 ? (
              <EmptyState
                icon="filter-outline"
                title={`No ${activeFilterLabel.toLowerCase()} projects`}
                body={`${projects.length} project${projects.length === 1 ? "" : "s"} found on ${activeDevice.name || "this machine"}, but none match the ${activeFilterLabel} filter.`}
                action={{ label: "Show all", onPress: () => setActiveFilter(null) }}
              />
            ) : (
              <EmptyState
                icon="folder-open-outline"
                busy={projectsDiscovering}
                title={projectsDiscovering ? "Scanning…" : "No projects yet"}
                body={
                  projectsDiscovering
                    ? `Looking through the home directory on ${activeDevice.name || "your machine"}.${scanDiagnosticLine ? ` ${scanDiagnosticLine}.` : ""}`
                    : `Yaver found nothing to build on ${activeDevice.name || "this machine"}.${scanDiagnosticLine ? ` ${scanDiagnosticLine}.` : ""}`
                }
                action={
                  projectsDiscovering
                    ? {
                        label: "Restart scan",
                        onPress: async () => {
                          try {
                            await previewClient.stopMobileProjectsScan().catch(() => undefined);
                            await previewClient.refreshMobileProjects();
                          } catch {}
                        },
                      }
                    : {
                        label: "Scan again",
                        onPress: async () => {
                          try {
                            setProjectsDiscovering(true);
                            await previewClient.refreshMobileProjects();
                          } catch {}
                        },
                      }
                }
              />
            )
          }
        />
      </View>

      <BrowserShortcutExportModal
        visible={!!shortcutProject}
        onClose={() => setShortcutProject(null)}
        deviceId={activeDevice?.id}
        connected={!!activeDevice && (isConnected || connectedDeviceIds.includes(activeDevice.id))}
        project={shortcutProject}
        c={c}
      />

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
              {/* THE ROUTE RENDERS FIRST — advisory never buries it. Measured
                  2026-07-28 (build-482 regression, live on the real app as
                  RN-web): with the advisory block ABOVE this scroll region in
                  a maxHeight:70% sheet, an 8,212-char guidance wall pushed
                  "WebRTC Reload"/"Browser Reload" 280–340px below a fold the
                  user could not scroll past (body.scrollHeight === viewport).
                  Chosen shape: the lanes are the FIRST children of the scroll
                  region and the whole advisory block lives BELOW them — so
                  "lanes below the fold with no scroll" is structurally
                  impossible: the sheet opens showing lanes, and advisory
                  length only adds scrollable content underneath. Every
                  advisory <Text> also carries numberOfLines, belt-and-braces
                  with the producer-side cap (devserver_http.go
                  compatGuidanceMaxChars). Guard:
                  mobile/src/lib/appsAdvisoryLayout.test.ts */}
              {/* Tapping a project is only about rendering it — the reload lanes.
                  Tests (and build/deploy) are driven by vibing text to the agent. */}
              {actionSheet?.actions.map((action, i) => {
                const disabled = action.supported === false;
                const isHermes = action.type === "open-native" || action.label.toLowerCase().includes("hermes");
                const meta = isHermes
                  ? (disabled && action.reason
                      ? `Hermes builds a full native bundle and takes longer. ${action.reason}`
                      : "Full native bundle build — slower than Browser or WebRTC Reload.")
                  : (disabled && action.reason
                      ? action.reason
                      : `${action.target}${action.framework ? ` · ${action.framework}` : ""}${action.platform ? ` → ${action.platform}` : ""}`);
                return (
                  <Pressable
                    key={`${action.label}-${i}`}
                    style={[s.actionSheetItem, { borderColor: c.border }, disabled && { opacity: 0.4 }]}
                    onPress={() => handleExecuteAction(action)}
                  >
                    <GlyphIcon
                      icon={action.icon || "\u25B6"}
                      size={22}
                      color={action.supported === false ? c.textMuted : c.accent}
                      textStyle={[s.actionSheetIcon, { color: action.supported === false ? c.textMuted : c.accent }]}
                    />
                    <View style={{ flex: 1 }}>
                      <Text style={[s.actionSheetLabel, { color: disabled ? c.textMuted : c.textPrimary }]}>
                        {action.label}
                      </Text>
                      <Text style={[s.actionSheetMeta, { color: c.textMuted }]}>
                        {meta}
                      </Text>
                    </View>
                  </Pressable>
                );
              })}
              {/* Advisory / diagnostics — below the route, scrollable, every
                  line capped. Detail beyond the caps belongs on a dedicated
                  diagnostics surface, not in front of the lanes. */}
            {actionSheet?.compatibility?.guidance ? (
              /* numberOfLines: guidance is producer-capped (280 chars) since
                 2026-07-28, but this surface must not trust that alone — the
                 identical content once flowed here unbounded as an 8KB wall
                 while the errors[0] Text below was already capped. Same
                 channel, same cap. */
              <Text numberOfLines={4} style={[s.actionSheetSubtitle, { color: actionSheet.compatibility.compatible ? "#cbd5e1" : "#fbbf24", marginTop: 8 }]}>
                {actionSheet.compatibility.guidance}
              </Text>
            ) : secondClassGuidance(
              actionSheet?.actions.find((a) => isSecondClassMobileFramework(a.framework))?.framework,
              isDirectConnection,
            ) ? (
              <Text numberOfLines={4} style={[s.actionSheetSubtitle, { color: "#cbd5e1", marginTop: 8 }]}>
                {secondClassGuidance(
                  actionSheet?.actions.find((a) => isSecondClassMobileFramework(a.framework))?.framework,
                  isDirectConnection,
                )}
              </Text>
            ) : agentFlowGuidance(
              actionSheet?.actions.find((a) => a.framework === "expo" || a.framework === "react-native")?.framework
            ) ? (
              <Text numberOfLines={4} style={[s.actionSheetSubtitle, { color: "#cbd5e1", marginTop: 8 }]}>
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
              /* numberOfLines: this can be a multi-KB per-module wall (seen
                 live in build 482 for yaver/mobile) — unbounded, it consumed
                 the WHOLE sheet and squeezed the action lanes to zero height,
                 so "Browser Reload" existed but could never be seen. The
                 diagnostics summarize; the lanes are the point of the sheet. */
              <Text numberOfLines={4} style={[s.actionSheetSubtitle, { color: "#fca5a5", marginTop: -8 }]}>
                {actionSheet.compatibility.errors[0]}
              </Text>
            )}
            {!!actionSheet?.compatibility?.warnings?.length && !actionSheet?.compatibility?.errors?.length && (
              <Text numberOfLines={4} style={[s.actionSheetSubtitle, { color: "#fcd34d", marginTop: -8 }]}>
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
              <Text numberOfLines={4} style={[s.actionSheetSubtitle, { color: "#fca5a5", marginTop: -8 }]}>
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
              <Text numberOfLines={4} style={[s.actionSheetSubtitle, { color: "#fca5a5", marginTop: -8 }]}>
                Missing on machine: {actionSheet.compatibility.missingLocalTools.join(", ")}
              </Text>
            )}
            {actionSheet?.compatibility?.hermesCompilerError ? (
              <Text numberOfLines={4} style={[s.actionSheetSubtitle, { color: "#fca5a5", marginTop: -8 }]}>
                {actionSheet.compatibility.hermesCompilerError}
              </Text>
            ) : null}
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
                        const result = await codingClient.executeVibingSuggestion(sg.prompt, vibingState!.path);
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
                              await codingClient.sendTask(sg.label, sg.prompt + (sg.reasoning ? `\n\nContext: ${sg.reasoning}` : ""));
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
                    <GlyphIcon
                      icon={sg.icon}
                      size={24}
                      color={c.accent}
                      textStyle={[s.vibingFeatureIcon, { color: c.accent }]}
                    />
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
                      const result = await codingClient.executeVibingSuggestion(qa.prompt, vibingState!.path);
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
                  <GlyphIcon
                    icon={qa.icon}
                    size={22}
                    color={c.accent}
                    textStyle={[s.vibingGridIcon, { color: c.accent }]}
                  />
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
                    const result = await codingClient.executeVibingSuggestion(customTask, vibingState.path);
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
          {/* The browser guest owns every pixel. Host navigation, reload,
              stop, DOM and diagnostics chrome stay off this surface; the one
              host affordance is the Y Vibing bubble below. */}
          {webViewLoading && !webPreviewContentLoaded && (
            <View style={[s.loadingBar, { backgroundColor: c.accent }]} />
          )}

          <View style={{ flex: 1 }}>
            {/* An empty bundleUrl means devStatus has not reported yet. Mounting
                a WebView on uri:"" issues no request, so nothing can ever fail
                or retry — it just sits blank. Render an explicit waiting state
                instead; treat an empty url as a bug, never as a value. */}
            {!bundleUrl ? (
              <View style={s.previewOverlay}>
                {activeGap ? (
                  gapCard
                ) : devStatus?.error ? (
                  <>
                    <Ionicons name="alert-circle-outline" size={36} color={c.error} />
                    <Text style={[s.previewFailTitle, { color: c.error }]}>Preview route is unavailable</Text>
                    <Text style={[s.previewSubtle, { color: c.textMuted }]}>
                      {devStatus.error}
                    </Text>
                  </>
                ) :(previewNowTick - (webPreviewStartedAt ?? previewNowTick)) >= 10000 ? (
                  /* 10s with no address is no longer "waiting", it's a state
                     that needs a NAMED reason and an action — an unbounded
                     spinner over a missing address is the unfalsifiable
                     silence this screen exists to avoid. */
                  <>
                    <Ionicons name="time-outline" size={36} color={c.warn} />
                    <Text style={[s.previewFailTitle, { color: c.textPrimary }]}>
                      Still no address from the dev server
                    </Text>
                    <Text style={[s.previewSubtle, { color: c.textMuted }]}>
                      {devStatusPollEnabled
                        ? "The box is reachable, but /dev/status has not reported an active server yet. It may have stopped — retry, or start the preview again from Projects."
                        : `Yaver ${describeConnectionStatus(connectionStatus)} — status polling is paused until the box is reachable again.`}
                    </Text>
                    <Pressable
                      onPress={() => {
                        retryConnection();
                        const id = activeDevice?.id;
                        if (id) {
                          void connectionManager
                            .clientFor(id)
                            .getDevServerStatus()
                            .then((st) => { if (isActiveDevServerStatus(st)) setDevStatus(st); })
                            .catch(() => {});
                        }
                      }}
                      style={[s.previewBtn, { backgroundColor: "#1a2e1a", marginTop: 12 }]}
                    >
                      <Text style={[s.previewBtnText, { color: "#22c55e" }]}>Retry</Text>
                    </Pressable>
                  </>
                ) : (
                <Text style={[s.previewSubtle, { color: c.textMuted }]}>
                  Waiting for the dev server to report its address…
                </Text>
                )}
              </View>
            ) : (
              <WebView
                ref={webViewRef}
                key={webViewKey}
                source={{ uri: bundleUrl, headers: previewClient.getAuthHeaders() }}
                sharedCookiesEnabled
                thirdPartyCookiesEnabled
              style={{ flex: 1, backgroundColor: c.bg }}
              onLoadStart={() => { webPreviewErroredRef.current = false; }}
              onLoadEnd={() => {
                setWebViewLoading(false);
                if (!webPreviewErroredRef.current) webPreviewRetryRef.current = 0;
              }}
              // Cold start: a Flutter/expo/vite web server takes 10-60s to compile
              // and bind. Until then the agent's /dev/ proxy returns 503 or refuses
              // the connection. Auto-retry (~30×2.5s ≈ 75s) instead of a dead page.
              // Every non-2xx is a signal. This used to retry only on >=500,
              // which meant 401/403/404 did nothing at all: no retry, no
              // failure panel, overlay forever. The initial navigation now
              // carries auth headers and the relay mints a scoped HttpOnly
              // cookie for subresources; credentials never enter this URL.
              onHttpError={(e) => {
                const code = e.nativeEvent.statusCode;
                if (code === 401 || code === 403) {
                  // Terminal: retrying cannot fix credentials. Name the cause.
                  setWebPreviewLogs((prev) => appendPreviewLogLine(prev, `HTTP ${code} — the preview URL was rejected. The relay credential is missing or stale; reconnect to this box to refetch it.`));
                  setWebPreviewFailed(true);
                  setWebViewLoading(false);
                  return;
                }
                if (code === 404) {
                  setWebPreviewLogs((prev) => appendPreviewLogLine(prev, "HTTP 404 — the dev server is up but is not serving a web target at this path. Confirm the project has a web build (react-native-web + react-dom)."));
                  setWebPreviewFailed(true);
                  setWebViewLoading(false);
                  return;
                }
                if (code >= 400) scheduleWebPreviewRetry();
              }}
              onError={() => scheduleWebPreviewRetry()}
              // Confirm real paint before hiding the overlay. Lives in
              // src/lib/previewReadyScript.ts so the single most failure-prone
              // piece of this lane is testable — e2e/rn-browser-loop.mjs runs
              // the exact string below against a real Chromium.
              //
              // The inline version this replaced accepted `body.children > 1`,
              // which every Expo Web page satisfies at document-end (noscript +
              // div#root + script = 3 children) BEFORE react mounts. It lifted
              // the overlay onto an empty #root — the RN browser-lane blank
              // screen — and latched, so a slow or failed 7 MB bundle stayed
              // blank with no error and no retry. Verified against sfmg and
              // talos/mobile exports: t0 #root=0, old probe said "rendered".
              injectedJavaScriptBeforeContentLoaded={WEBVIEW_BEFORE_CONTENT_SCRIPT}
              injectedJavaScript={WEBVIEW_INJECTED_SCRIPT}
              onMessage={(e) => {
                try {
                  const m = JSON.parse(e.nativeEvent.data);
                  // Screen context FIRST — the agent's injected probe has always
                  // had an RN branch (window.ReactNativeWebView.postMessage)
                  // written for exactly this handler, and until this line
                  // existed the message fell into the catch below. The phone
                  // paid for the probe and web got the feature. Forwarded over
                  // the authed quicClient, never straight from the page (/dev/
                  // is unauthenticated by design).
                  if (handlePreviewScreenMessage(m, devStatus?.workDir)) return;
                  // DOM mode SECOND: the clicked element (and the
                  // interactive-items inventory) from the dom probe, over the
                  // same authed channel.
                  if (m && m.t === "yaver-preview-resource-error") {
                    const tag = String(m.tag || "resource").toUpperCase();
                    const url = String(m.url || "");
                    const resourcePath = String(m.path || "");
                    const line = `[web:error] resource failed ${tag}${url ? ` ${url}` : ""}`.slice(0, 1400);
                    setWebPreviewLogs((prev) => appendPreviewLogLine(prev, line));
                    // Cold Expo/Metro can return index.html before the entry
                    // bundle is ready. The document stays 200, so WebView's
                    // main-frame callbacks never retry it after compilation.
                    if (shouldRetryBrowserResourceFailure({ tag, contentLoaded: webPreviewContentLoaded })) {
                      scheduleWebPreviewRetry();
                    }
                    if (resourcePath && browserResourceProbeRanRef.current !== resourcePath) {
                      browserResourceProbeRanRef.current = resourcePath;
                      void probeBrowserResource(previewClient, bundleUrl, resourcePath).then((probe) => {
                        setBrowserLaneProbe(probe);
                        setWebPreviewFailed(true);
                        setWebPreviewLogs((prev) => appendPreviewLogLine(prev, browserLaneProbeLine(probe)));
                        // A successful HEAD crossed the exact relay + agent
                        // route and refreshed the scoped auth cookie in the
                        // shared jar. Retry once so the WebView consumes it.
                        if (probe.stage === "resource-delivery" && probe.httpStatus === 200) scheduleWebPreviewRetry();
                      });
                    }
                    if (shouldRunBrowserLaneDoctor({
                      showWebView,
                      bundleUrl,
                      contentLoaded: webPreviewContentLoaded,
                      failed: webPreviewFailed,
                      serverLooksReady: webPreviewServerLooksReady,
                      logLine: line,
                    })) runBrowserLaneDoctor("resource-error");
                    return;
                  }
                  // The probe cannot run in a cross-origin RN-web iframe. That
                  // is a limitation, not evidence of paint: sfmg produced a
                  // green box doctor while this phone surface stayed black.
                  if (m && m.type === WEBVIEW_PROBE_UNSUPPORTED) {
                    setProbeUnavailable(String(m.detail || m.reason || "the ready-probe cannot run on this frame"));
                    return;
                  }
                  if (m && (m.t === "yaver-preview-probe" || m.t === "yaver-preview-timeout")) {
                    setWebPreviewProbe((m.state || null) as PreviewProbeState | null);
                    if (m.t === "yaver-preview-timeout") {
                      const reason = String(m.state?.reason || "preview probe timed out");
                      setWebPreviewLogs((prev) => appendPreviewLogLine(prev, `[preview] render probe timed out: ${reason}`));
                      // Name the likely CAUSE for the terminal reason (e.g.
                      // flutter_booting → asset/CanvasKit fetch failure), not
                      // just the fact of the timeout.
                      setWebPreviewLogs((prev) => appendPreviewLogLine(prev, previewTimeoutExplanation(m.state?.reason, devStatus?.framework)));
                      setWebPreviewFailed(true);
                    }
                  } else if (m && m.t === "yaver-rendered") {
                    setWebPreviewProbe((m.state || null) as PreviewProbeState | null);
                    setWebPreviewContentLoaded(true);
                    setWebPreviewFailed(false);
                    webPreviewRetryRef.current = 0;
                  } else if (m && m.t === "yaver-preview-log") {
                    const level = String(m.level || "log").toLowerCase();
                    const text = String(m.text || "").trim();
                    if (!text) return;
                    const line = `[web:${level}] ${text}`.slice(0, 1400);
                    setWebPreviewLogs((prev) => appendPreviewLogLine(prev, line));
                    if (isPreviewRuntimeIssueLevel(level)) {
                      if (shouldRunBrowserLaneDoctor({
                        showWebView,
                        bundleUrl,
                        contentLoaded: webPreviewContentLoaded,
                        failed: webPreviewFailed,
                        serverLooksReady: webPreviewServerLooksReady,
                        logLine: line,
                      })) {
                        runBrowserLaneDoctor(level === "error" ? "webview-error" : "webview-warning");
                      }
                      // Console evidence is client-only — the agent cannot see
                      // inside the WebView, so a page crash may escalate even
                      // when agent health says the SERVER is healthy.
                      // Keep failures visible as a compact issue count. Logs
                      // open only when the user asks; an error must not cover
                      // the app with a second, automatic diagnostics surface.
                    }
                  }
                } catch { /* not ours */ }
              }}
              javaScriptEnabled
              domStorageEnabled
              allowsInlineMediaPlayback
            />
            )}
            {/* Routine diagnostics never compete with the guest or Vibing.
                Failures still replace the guest below with their named cause
                and route-to-fix; healthy runtime logs remain internal. */}
            {/* A box-local doctor or host-probe failure cannot satisfy phone
                 paint. Current agents advertise an in-frame signal and remain
                 strict; older agents expose the frame as visibly unverified so
                 a missing channel cannot become a permanent opaque wall. */}
              {bundleUrl && paintGateMode === "blocking" && (
              <View style={s.previewOverlay}>
                {webPreviewFailed ? (
                  (() => {
                    /* Compile failures lead with a COMPACT card (remained.md
                       P1): the agent already persisted the offending lines +
                       remedy into status.error, or they sit in the tail —
                       either way the user must read "your app failed to
                       compile: <reason>", never a raw log dump with the
                       truth buried in purple. Full output stays below. */
                    const compileCard = detectCompileFailure(devStatus?.error, webPreviewLogs);
                    const healthyLogs = previewLogsLookHealthy(webPreviewLogs, devStatus?.error);
                    const connectionDropped = !effectivelyConnected;
                    const canOfferProjectFix = !activeGap && (
                      previewAgentHealthIsAuthoritative(devStatus)
                        ? previewCanOfferProjectFix(devStatus, webPreviewLogs)
                        : (compileCard || previewCanOfferProjectFix(devStatus, webPreviewLogs))
                    );
                    const fallbackTitle = connectionDropped
                      ? "Connection dropped while preview was ready"
                      : healthyLogs
                        ? "Preview is ready, waiting for a rendered frame"
                        : "Dev server didn't come up";
                    return (
                  <>
                    {/* A NAMED capability gap outranks every other diagnosis
                        here: it has a deterministic one-tap fix, so offering
                        "Fix in Yaver" (an LLM run) first would be the most
                        expensive possible answer to the cheapest possible
                        question. */}
                    {activeGap ? gapCard : null}
                    <Ionicons name="alert-circle-outline" size={40} color={c.error} />
                    <Text style={[s.previewFailTitle, { color: c.error }]}>
                      {browserLaneProbe && !browserLaneProbe.ok
                        ? `Browser lane stopped at ${browserLaneProbe.stage}`
                        : compileCard ? compileCard.title : fallbackTitle}
                    </Text>
                    {browserLaneProbe && !browserLaneProbe.ok ? (
                      <Text style={[s.previewSubtle, { color: c.textPrimary, textAlign: "left" }]} selectable>
                        {[
                          browserLaneProbe.detail || "The agent probed the same browser lane and it did not render.",
                          browserLaneProbe.remedy ? `Remedy: ${browserLaneProbe.remedy}` : "",
                        ].filter(Boolean).join("\n")}
                      </Text>
                    ) : compileCard ? (
                      <Text style={[s.previewSubtle, { color: c.textPrimary, textAlign: "left" }]} selectable>
                        {compileCard.detail}
                      </Text>
                    ) : connectionDropped ? (
                      <Text style={s.previewStepCmd}>{`Yaver ${describeConnectionStatus(connectionStatus)}. Reconnect, then reload the preview.`}</Text>
                    ) : healthyLogs ? (
                      <Text style={s.previewStepCmd}>{probeUnavailable
                        ? `The dev server is ready, but this phone frame has not confirmed paint — ${probeUnavailable}`
                        : "The dev server reported ready. The WebView has not confirmed the first rendered frame yet."}</Text>
                    ) : (
                      <Text style={s.previewStepCmd}>{devServerStepsFor(devStatus?.framework)}</Text>
                    )}
                    <View style={s.previewFailBtns}>
                      <Pressable
                        onPress={() => setShowWebView(false)}
                        accessibilityRole="button"
                        accessibilityLabel="Back from failed preview"
                        style={[s.previewBtn, { backgroundColor: "#27272a" }]}
                      >
                        <Text style={[s.previewBtnText, { color: "#f4f4f5" }]}>Back</Text>
                      </Pressable>
                      <Pressable onPress={() => { resetWebPreview(); setWebViewLoading(true); setWebViewKey((k) => k + 1); }} style={[s.previewBtn, { backgroundColor: "#1a2e1a" }]}>
                        <Text style={[s.previewBtnText, { color: "#22c55e" }]}>Retry</Text>
                      </Pressable>
                      {canOfferProjectFix ? (
                        <Pressable
                          onPress={() => {
                            const proj = (runningProject || devStatus?.framework || "the app").split(" / ")[0];
                            const logs = webPreviewLogs.slice(-40).join("\n");
                            void sendTaskOrWarn(
                              `Fix ${proj} preview (${devStatus?.framework || "app"})`,
                              `The ${devStatus?.framework || "app"} dev server / browser preview for ${proj} (workDir: ${devStatus?.workDir || "?"}) failed to build or render. Diagnose the ROOT cause from the structured browser-lane result and output below, then fix it so the app builds and serves through Browser Reload. Common causes: a missing asset declared in config, a missing dependency, or a bad import.\n\n--- browser-lane probe ---\n${JSON.stringify(browserLaneProbe || {})}\n\n--- dev server output ---\n${logs}`,
                              "Fix browser preview",
                            ).then((sent) => { if (sent) setShowWebView(false); });
                          }}
                          style={[s.previewBtn, { backgroundColor: c.accentSoft, borderColor: c.accent }]}
                        >
                          <Text style={[s.previewBtnText, { color: c.accent }]}>Fix with AI</Text>
                        </Pressable>
                      ) : null}
                      <Pressable onPress={() => void handleReload("full")} style={[s.previewBtn, { backgroundColor: c.infoBg, borderColor: c.info }]}>
                        <Text style={[s.previewBtnText, { color: c.info }]}>Restart</Text>
                      </Pressable>
                    </View>
                  </>
                    );
                  })()
                ) : (
                  <>
                    <ActivityIndicator size="large" color={c.accent} />
                    {/* Phase-accurate: "Starting flutter dev server…" over a
                        server that is already serving (probe reason
                        flutter_booting) sent users debugging the wrong layer.
                        previewPhase.ts maps status+probe to the honest line —
                        shared with DevPreview.tsx. */}
                    <Text style={[s.previewStartTitle, { color: c.textPrimary }]}>
                      {narratedPreviewWait?.title || previewPhaseTitle(devStatus, webPreviewProbe)}
                    </Text>
                    <Text style={s.previewStepCmd}>{devServerStepsFor(devStatus?.framework)}</Text>
                    <Text style={[s.previewSubtle, { color: c.textMuted }]}>
                      {loadingStatus || "First web compile can take up to a minute — retrying automatically."}
                    </Text>
                    <LaneStartupStatus
                      startedAt={webPreviewStartedAt}
                      lastOutputAt={webPreviewLastLogAt}
                      now={previewNowTick}
                      // The browser lane already receives the agent's live
                      // dev-server stream above. Keep a small rolling tail on
                      // the first-glance card so a cold SFMG compile feels
                      // alive instead of looking like an opaque spinner. Full
                      // history remains available in the diagnostic/failure
                      // path; this is deliberately only the newest few lines.
                      lines={webPreviewLogs}
                      maxLines={4}
                      emptyText="waiting for the first line from the box…"
                      mutedColor={c.textMuted}
                      warnColor={c.warn}
                      lineColorFor={(line) => previewLogColor(line, c)}
                      stallHint="Stop and retry if this persists"
                    />
                  </>
                )}
              </View>
              )}
              <BrowserVibeBubble
                projectPath={activeProjectPath || devStatus?.workDir}
                projectName={guestProjectName}
                exitLabel="Go to Projects"
                onExitPreview={() => setShowWebView(false)}
                onReload={handleReload}
              />
          </View>
        </View>
      </Modal>
      <DevServerStopDialog
        visible={showStopConfirm}
        project={guestProjectName.split(" / ")[0]}
        port={devStatus?.port}
        client={previewClient}
        onCancel={() => setShowStopConfirm(false)}
        onPhaseChange={setStopPhase}
        onStopped={() => {
          setShowStopConfirm(false);
          setShowWebView(false);
          setDevStatus(null);
        }}
      />
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
  previewUnverifiedNotice: {
    position: "absolute", left: 12, right: 12, bottom: 58, zIndex: 68,
    flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 8,
    paddingHorizontal: 10, paddingVertical: 7, borderRadius: 8,
    backgroundColor: "rgba(14,14,18,0.86)", borderWidth: 1, borderColor: "#3f3f46",
  },
  previewUnverifiedText: { flex: 1, color: "#d4d4d8", fontSize: 10 },
  previewUnverifiedAction: { color: "#818cf8", fontSize: 10, fontWeight: "700" },
  previewStartTitle: { fontSize: 16, fontWeight: "700", textAlign: "center" },
  previewFailTitle: { fontSize: 17, fontWeight: "700", textAlign: "center" },
  previewStepCmd: {
    fontFamily: "Menlo", fontSize: 12, color: "#22c55e",
    backgroundColor: "#0f1a0f", borderColor: "#22c55e33", borderWidth: 1,
    borderRadius: 8, paddingHorizontal: 10, paddingVertical: 6, overflow: "hidden",
  },
  previewSubtle: { fontSize: 12, textAlign: "center", lineHeight: 17 },
  previewEscapeBar: {
    position: "absolute",
    left: 12,
    right: 12,
    zIndex: 80,
    minHeight: 44,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
  },
  previewEscapeBtn: {
    minHeight: 44,
    paddingHorizontal: 12,
    borderRadius: 22,
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
    backgroundColor: "rgba(0,0,0,0.62)",
  },
  previewEscapeText: { color: "#fff", fontSize: 15, fontWeight: "700" },
  previewEscapeActions: { flexDirection: "row", alignItems: "center", gap: 8 },
  previewEscapeIconBtn: {
    width: 44,
    height: 44,
    borderRadius: 22,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "rgba(0,0,0,0.62)",
  },
  previewLogBox: {
    maxHeight: 180, width: "100%", marginTop: 6, borderRadius: 10,
    backgroundColor: "#0a0a0f", borderWidth: 1, borderColor: "#333",
  },
  previewLogLine: { fontFamily: "Menlo", fontSize: 10.5, color: "#9ca3af", lineHeight: 15 },
  previewFailBtns: { flexDirection: "row", gap: 12, marginTop: 8 },
  // The gap card is the ROUTE, so it gets a floor and its own breathing room —
  // advisory diagnostics must never squeeze it out (the 2026-07-26 action-sheet
  // defect, where the one offered lane sat at zero height under a diagnostics
  // wall).
  gapCard: { alignItems: "center", gap: 8, paddingHorizontal: 8, paddingVertical: 12, marginBottom: 10, width: "100%" },
  gapFixBtn: { marginTop: 4, minWidth: 200, alignItems: "center" },
  previewBtn: { paddingHorizontal: 22, paddingVertical: 11, borderRadius: 10 },
  previewBtnText: { fontSize: 14, fontWeight: "700" },
  previewRuntimeLogFab: {
    position: "absolute",
    right: 14,
    zIndex: 70,
    minHeight: 42,
    paddingHorizontal: 12,
    borderRadius: 21,
    borderWidth: 1,
    flexDirection: "row",
    alignItems: "center",
    gap: 7,
  },
  previewRuntimeLogFabText: { fontSize: 13, fontWeight: "800" },
  previewRuntimeLogPanel: {
    position: "absolute",
    left: 12,
    right: 12,
    zIndex: 69,
    maxHeight: 260,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: "#ffffff1f",
    backgroundColor: "#08080d",
    overflow: "hidden",
  },
  previewRuntimeLogHeader: {
    minHeight: 42,
    paddingHorizontal: 12,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    borderBottomWidth: 1,
    borderBottomColor: "#ffffff14",
  },
  previewRuntimeLogTitleRow: { flexDirection: "row", alignItems: "center", gap: 8 },
  previewRuntimeLogTitle: { color: "#fff", fontSize: 14, fontWeight: "800" },
  // The vibing overlay sits ON the live preview — see the JSX for why it is an
  // overlay and not a page. Backdrop is deliberately light: the app behind it
  // is the thing the user is looking at, and dimming it to unreadable would
  // recreate the problem of not being able to see your own app.
  vibeOverlayBackdrop: { ...StyleSheet.absoluteFillObject, justifyContent: "flex-end", backgroundColor: "rgba(0,0,0,0.35)" },
  vibeOverlaySheet: { backgroundColor: "#0d0d12", borderTopLeftRadius: 18, borderTopRightRadius: 18, borderWidth: 1, borderColor: "#23232c", borderBottomWidth: 0 },
  vibeOverlayInputRow: { flexDirection: "row", alignItems: "flex-end", gap: 8, paddingHorizontal: 12, paddingTop: 10 },
  vibeOverlayInput: { flex: 1, minHeight: 44, maxHeight: 120, color: "#fff", fontSize: 15, backgroundColor: "#16161c", borderRadius: 12, borderWidth: 1, borderColor: "#26262f", paddingHorizontal: 12, paddingVertical: 10 },
  vibeOverlayGo: { paddingHorizontal: 18, height: 44, borderRadius: 12, alignItems: "center", justifyContent: "center", backgroundColor: "#312e81" },
  vibeOverlayGoText: { color: "#fff", fontSize: 15, fontWeight: "800" },
  previewRuntimeLogClose: { width: 34, height: 34, borderRadius: 17, alignItems: "center", justifyContent: "center" },
  previewRuntimeLogScroll: { maxHeight: 142 },
  previewRuntimeLogActions: {
    flexDirection: "row",
    gap: 10,
    paddingHorizontal: 10,
    paddingTop: 8,
    paddingBottom: 10,
    borderTopWidth: 1,
    borderTopColor: "#ffffff14",
  },
  previewRuntimeActionBtn: { flex: 1, alignItems: "center", paddingHorizontal: 12 },

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
  // ScrollView clips to its own bounds on iOS and RN-web. This used to be
  // 30px around 34px chips, shaving the selected outline off both edges.
  filterRow: { height: 38, flexGrow: 0 },
  filterFade: { position: "absolute", right: 0, top: 0, bottom: 0, width: 24, opacity: 0.9 },
  filterRowContent: { gap: 8, alignItems: "center" as const, paddingVertical: 2, paddingRight: 8 },
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

  // Action sheet
  actionSheetOverlay: { flex: 1, backgroundColor: "rgba(0,0,0,0.5)", justifyContent: "flex-end" },
  actionSheetContainer: { borderTopLeftRadius: 20, borderTopRightRadius: 20, padding: 20, paddingBottom: 40, maxHeight: "70%" },
  actionSheetHandle: { width: 36, height: 4, backgroundColor: "#333", borderRadius: 2, alignSelf: "center", marginBottom: 16 },
  actionSheetTitle: { fontSize: 20, fontWeight: "700", marginBottom: 2 },
  actionSheetSubtitle: { fontSize: 13, marginBottom: 16 },
  // minHeight: the lanes are the sheet's reason to exist — whatever the
  // diagnostics above do, the action list keeps enough room to show and
  // scroll its entries (the 482 regression rendered ZERO visible lanes).
  actionSheetScroll: { minHeight: 200 },
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
    borderWidth: 1,
    marginTop: 12,
  },
  catalogCard: {
    borderWidth: 1,
    marginTop: 12,
  },
  cardHeader: { flexDirection: "row", alignItems: "center", gap: 10 },
  cardTitleContainer: { flex: 1 },
  cardTitle: { fontSize: 16, fontWeight: "700" },
  cardMeta: { fontSize: 11, marginTop: 2 },
  guidanceText: { lineHeight: 15, marginTop: 4 },
  statusDot: { width: 8, height: 8, borderRadius: 4 },
  frameworkIcon: {},

  cardActions: { flexDirection: "row", gap: 8, marginTop: 12, alignItems: "center", justifyContent: "flex-start" },
  actionBtn: { minHeight: 36, paddingVertical: 8, paddingHorizontal: 14, borderRadius: 8, alignItems: "center", justifyContent: "center" },
  openBtn: { backgroundColor: "#22c55e", flex: 0, minWidth: 72, flexDirection: "row" as const, gap: 4 },
  openBtnText: { color: "#000", fontSize: 13, fontWeight: "700" },
  reloadBtn: { backgroundColor: "#22c55e22", flex: 1, alignItems: "center" },
  reloadBtnText: { color: "#22c55e", fontSize: 13, fontWeight: "600" },
  stopBtn: { backgroundColor: "#ef444422", minWidth: 72, paddingHorizontal: 16, alignItems: "center" },
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
  webViewHeaderActions: { flexDirection: "row", alignItems: "center", gap: 4 },
  // 32pt box + hitSlop 10 ⇒ a 52pt touch target, comfortably past the 44pt
  // minimum, while the visual footprint stays small enough for the title.
  webViewHeaderBtn: { width: 32, height: 32, alignItems: "center", justifyContent: "center" },
  loadingBar: { height: 2, opacity: 0.6 },
});
