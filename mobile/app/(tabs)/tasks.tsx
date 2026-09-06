import React, { startTransition, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Ionicons } from "@expo/vector-icons";
import { agentSignalFromTask, agentStateBg, agentStateColor } from "../../src/lib/agentStatus";
import { clipUrl } from "../../src/lib/vibePreview";
import { planFollowUp } from "../../src/lib/followUpPlan";
import { beginTaskTurn, mergeTaskSnapshot } from "../../src/lib/studioTaskState";
import { classifyStreamEnd, planStreamRecovery } from "../../src/lib/taskStreamRecovery";
import { isBundleLoaderAvailable } from "../../src/lib/bundleLoader";
import { AuthenticatedVideoPlayer } from "../../src/components/AuthenticatedVideoPlayer";
import {
  ActivityIndicator,
  Alert,
  Animated,
  AppState,
  Dimensions,
  FlatList,
  GestureResponderEvent,
  Image,
  Keyboard,
  KeyboardAvoidingView,
  Linking,
  Modal,
  Platform,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from "react-native";
import * as ImagePicker from "expo-image-picker";
import * as FileSystem from "expo-file-system/legacy";
import { SafeAreaView, useSafeAreaInsets } from "react-native-safe-area-context";
import Markdown from "react-native-markdown-display";
import { useDevice, type Device } from "../../src/context/DeviceContext";
import { useDogfoodOverlay } from "../../src/context/DogfoodOverlayContext";
import RemoteBoxBanner from "../../src/components/RemoteBoxBanner";
// Pure output-buffer derivations live in a plain module so they can be
// unit-tested in Node (see taskPreview.test.mts — it enforces that these
// stay BOUNDED; unbounded versions froze this screen while tasks streamed).
import {
  MAX_OUTPUT_LINES_PER_TASK,
  OUTPUT_TRUNCATED_MARKER,
  buildTaskPreviewText,
  capOutput,
  collapseAdjacentDuplicateLines,
  stripAnsi,
  stripMarkdownForPreview,
} from "../../src/lib/taskPreview";
// ONE definition of "what Yaver's prompt frame looks like", shared with
// FeedbackOverlay and parity-tested against the Go source.
import { SYSTEM_CONTEXT_END_MARKERS, containsYaverFraming } from "../../src/lib/promptFraming";
import EmptyState from "../../src/components/EmptyState";
import NoMachineEmpty from "../../src/components/NoMachineEmpty";
import TaskTargetWizard, { type TaskTarget } from "../../src/components/TaskTargetWizard";
import { useColors, useTheme } from "../../src/context/ThemeContext";
import type { ThemeColors } from "../../src/constants/colors";
import { AnsiConsoleText } from "../../src/components/AnsiConsoleText";
import { TaskSessionSummary } from "../../src/components/TaskSessionSummary";
import { assembleTrace } from "../../src/_core/trace";
import { summarizeRawConsole as _summarizeRawConsole } from "../../src/_core/ansi";
import {
  friendlyTaskPresentation,
  isTaskPresentationEvent,
  reduceTaskPresentation,
} from "../../src/_core/taskPresentation";
import { appTag } from "../../src/lib/appVersion";
import * as ExpoClipboard from "expo-clipboard";
import { getLogEntries, onLogsChanged, LogEntry } from "../../src/lib/logger";
import { rerenderActivePreviewSurface } from "../../src/lib/feedbackTrigger";
import { isAttachedDogfoodWebRuntime } from "../../src/lib/dogfoodRenderBridge";
import { publishAutoRenderVibing, subscribeAutoRenderVibing } from "../../src/lib/autoRenderVibing";
import { mustUseNativePreview } from "../../src/lib/devLane";
import { parseReloadIntent } from "../../src/lib/reloadIntent";
import { taskRunnerTransportLabel } from "../../src/lib/taskRunnerTransport";
import {
  AgentStatus,
  CloudWorkspaceRequiredError,
  ConnectionMode,
  ConnectionState,
  describeDevReloadResult,
  devReloadReachedTarget,
  ImageAttachment,
  ModelInfo,
  quicClient,
  RunnerInfo,
  Task,
  TaskRunnerControlCatalog,
  TaskStatus,
} from "../../src/lib/quic";
import { connectionManager } from "../../src/lib/connectionManager";
import { goalFromSlashCommand } from "../../src/lib/goalSlashCommand";
import { cacheTaskTurns, getCachedTaskTurns, cacheTaskList, getCachedTaskList } from "../../src/lib/storage";
import {
  activateTaskPlacement,
  getTaskPlacementStatus,
  listTaskDispatchIntents,
  rebindTaskPlacement,
  updateTaskDispatchIntent,
} from "../../src/lib/taskPlacement";
import { activationBlockReason } from "../../src/lib/taskPlacementCore";
import { listAgentTaskSnapshots, type AgentTaskSnapshot } from "../../src/lib/taskSnapshots";
import { reconcileTasksWithAgentSnapshots, scopedTaskIdentity } from "../../src/lib/taskSnapshotMerge";
import {
  listPendingCloudDispatches,
  mergePendingCloudDispatchIntents,
  mergePendingCloudPlacementStatus,
  pendingCloudDispatchNeedsUserAction,
  pendingCloudTaskPlaceholder,
  removePendingCloudDispatch,
  saveCloudWorkspaceRequiredDispatch,
  savePendingCloudDispatch,
  updatePendingCloudDispatch,
} from "../../src/lib/pendingCloudDispatch";
import { useAuth } from "../../src/context/AuthContext";
import { getUserSettings, getLocalSecret, LOCAL_KEYS, loadLocalSpeechConfig, type SpeechProvider, type TtsProvider } from "../../src/lib/auth";
import { transcribe, initWhisper, isWhisperReady, startRealtimeTranscribe, SPEECH_PROVIDERS, speakText as speakConfiguredText } from "../../src/lib/speech";
import { useRouter } from "expo-router";
import { DevPreview } from "../../src/components/DevPreview";
import { Badge } from "../../src/components/Badge";
import RunnerAuthModal from "../../src/components/RunnerAuthModal";
import { ParkedTurnError, parkedTurnNotice } from "../../src/lib/parkedTurn";
import { OpenCodeConfigModal } from "../../src/components/OpenCodeConfigModal";
import { taskFailureFixRoute } from "../../src/lib/taskFailureFixRoute";
import {
  runYaverAgent,
  loadYaverAgentLocalConfig,
  type YaverAgentHistoryTurn,
} from "../../src/lib/yaverAgentRunner";
import type { PhoneProject } from "../../src/lib/phoneProjects";
import { listLocalPhoneProjectsMeta } from "../../src/lib/phoneSandboxLocal";
import { gitContextForSlug, runAgenticCoding } from "../../src/lib/codingAgent/codingAgentRun";
import { gitNetForSlug, loadCodingConfig } from "../../src/lib/codingAgent/sandboxBinding";
import { repoSandboxForSlug } from "../../src/lib/codingAgent/repoSandbox";
import { isRepo } from "../../src/lib/codingAgent/sandboxGit";
import { makeYaverReadOnlyCodingTools } from "../../src/lib/codingAgent/yaverReadTools";
import { useRouteParamsCompat } from "../../src/lib/useRouteParamsCompat";
import { restoreTurnSnapshot, type TurnSnapshot } from "../../src/lib/codingAgent/turnTransaction";
import { redactProgressText, redactSecrets, redactValue } from "../../src/lib/codingAgent/secretRedaction";
import {
  endRemotelessTask,
  listRemotelessTasks,
  recoverInterruptedRemotelessTasks,
} from "../../src/lib/remotelessTaskLifecycle";
import type { YaverAgentToolContext } from "../../src/lib/yaverAgentTools";
import {
  loadKeepLastProjectEnabled,
  loadLastTaskProject,
  loadLastTaskProjectFromConvex,
  loadMCPServersFromConvex,
  loadUseLatestMCPEnabled,
  loadTaskVideoSummaryEnabled,
  loadTextCorrectionEnabled,
  saveKeepLastProjectEnabled,
  saveLastTaskProject,
  saveLastTaskProjectToConvex,
  saveMCPServersToConvex,
  saveTextCorrectionEnabled,
  saveUseLatestMCPEnabled,
} from "../../src/lib/taskComposerPrefs";
import { visibleProjectPickerRows } from "../../src/lib/projectPickerRows";
import { listMcpServers, type McpServer } from "../../src/lib/mcpServers";
import { mergeFetchedTasks } from "../../src/lib/taskListMerge";
import { withAlpha } from "../../src/lib/themeUtils";
import { layoutTokens, lightCardShadow, monoFamily, spacing, typography } from "../../src/theme/tokens";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";
import { CommandsPanel } from "../../src/components/CommandCard";
import {
  isCommandEvent,
  reduceCommandEvent,
  type CommandCardModel,
} from "../../src/lib/commandEvents";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import { taskHaptics } from "../../src/lib/taskHaptics";
import {
  isSandboxSupported,
  notifySandboxTaskFinished,
  setSandboxTaskStatus,
} from "../../src/lib/sandboxControl";
import { notifyTaskReply, shouldNotifyTaskReply } from "../../src/lib/taskReviewNotification";
import { MessageBubble } from "../../src/components/MessageBubble";
import { openTaskBus } from "../../src/lib/runningTasksBus";
import { ErrorMessage, detectSmartRetry } from "../../src/components/ErrorMessage";
import { AgentContextPanel, type AgentContextRow } from "../../src/components/AgentContextPanel";
import SandboxGitPanel from "../../src/components/SandboxGitPanel";
import { deriveRunnerBannerState, type RunnerFetchState } from "../../src/lib/runnerBannerState";
import { reconcileRunnerAuthStatus, runnerPollCadenceMs, sameAgentStatus, sameRunnerList, shouldPollRunnerState } from "../../src/lib/runnerPollPolicy";
import { resolveRemotelessPlacement, type ExecutionCandidate } from "../../src/_core/remoteless";
import {
  canComposeWithRemoteless,
  REMOTELESS_OWNER_ONLY_MESSAGE,
  remotelessAccessAllowed,
} from "../../src/lib/executionMode";
import { isPhoneLocalTask, phoneLocalTurnStatus } from "../../src/lib/phoneLocalTaskRoutingCore";
import { TaskHeader } from "../../src/components/TaskHeader";
import { firstClassTaskConversationTurns } from "../../src/_core/taskConversation";
import { taskRunnerControlForMessage } from "../../src/_core/taskRunnerControls";
import { buildTaskConsolePreview } from "../../src/lib/taskConsolePreview";
import { taskProjectExecutionSummary, workDirForTaskExecution } from "../../src/lib/taskProjectRouting";
import { SilentInputModal } from "../../src/components/SilentInputModal";
import { DEFAULT_SILENT_INPUT_CONFIG } from "../../src/lib/silentInput/types";
import { loadSilentInputConfig } from "../../src/lib/silentInput/config";
import {
  displayRunnerLabel,
  isModelCompatibleWithRunnerId,
  isTransportDeviceLabel,
  normalizeProjectChipName,
  normalizeTaskRunnerId,
  preferredDefaultModelForRunner,
  preferredSeededRunnerForDevice,
  runnerDispatchMismatch,
  resolveModelForRemoteSend,
  resolveRunnerForRemoteSend,
  resolveRunnerSelectionDeviceId,
} from "../../src/lib/remoteCodingSelection";

// Cap streaming output retained per task. A vibing session can produce
// 50k+ output lines (codex/claude tool runs spew bash stdout uncompressed),
// each ~80–120 chars. At ~100 char/line and 50k lines, that's 5MB per
// task held in JS heap as a string array — multiplied across multiple
// open tasks, this is what eventually OOMs the app on iOS. Cap at 8000
// lines and keep the tail (the head is rarely useful by line 8000).
// When we drop, prepend a marker so the user knows scrollback was
// truncated. The agent retains the full transcript on disk; the mobile
// is a window onto recent activity, not the source of truth.
// ── Constants ────────────────────────────────────────────────────────

// Status colour now comes from src/lib/agentStatus.ts — the one vocabulary every
// surface reads. The hardcoded map that lived here disagreed with the Home
// session strip's (running was blue here, emerald there; completed was green
// here, blue there), so the same task changed colour when you changed screens.
// Both bypassed the token layer. RUNNING is still statusInfo (blue) rather than
// indigo, for the original reason: the legacy #6366f1 sat in the same hue family
// as the brand purple used for user message bubbles, so two purples shadowed
// each other in the chat surface. That rule now lives in agentStateColor.

function runnerAuthIssue(
  runner: Pick<RunnerInfo, "id" | "installed" | "ready" | "warning" | "error" | "authConfigured"> | null | undefined,
): string | null {
  if (!runner || !runner.installed) return null;
  const detail = String(runner.error || runner.warning || "").trim();
  const lower = detail.toLowerCase();
  const isOpenCode = String(runner.id || "").trim().toLowerCase() === "opencode";
  if (isOpenCode && (
    runner.authConfigured === false ||
    lower.includes("401 unauthorized") ||
    lower.includes("unauthorized") ||
    lower.includes("api key") ||
    lower.includes("authentication")
  )) {
    return "OpenCode's selected provider needs a valid API key on this machine. Open OpenCode settings to update it; browser sign-in does not apply.";
  }
  if (
    lower.includes("token_expired") ||
    lower.includes("token has expired") ||
    lower.includes("refresh_token_reused") ||
    lower.includes("codex login --device-auth") ||
    lower.includes("please run `codex login`") ||
    lower.includes("no longer accepted") ||
    lower.includes("invalid_grant") ||
    lower.includes("401 unauthorized") ||
    lower.includes("unauthorized")
  ) {
    return `${displayRunnerLabel(runner.id)} reported that its sign-in is no longer accepted on this machine. Sign it in again on this machine.`;
  }
  if (runner.authConfigured === false) {
    return `${displayRunnerLabel(runner.id)} is installed but not signed in on this machine — tasks sent to it will wait forever.`;
  }
  if (runner.ready !== false) return null;
  if (
    lower.includes("auth") ||
    lower.includes("login") ||
    lower.includes("sign in") ||
    lower.includes("oauth") ||
    lower.includes("not authenticated")
  ) {
    return detail || `${displayRunnerLabel(runner.id)} is installed but not authenticated on this machine.`;
  }
  return null;
}

function runnerVerificationPending(
  runner: Pick<RunnerInfo, "authConfigured" | "authVerified" | "warning" | "ready"> | null | undefined,
): boolean {
  if (!runner || runner.ready !== false || runner.authConfigured !== true) return false;
  if (runner.authVerified === true) return false;
  return /provider operation|verification|provider probe/i.test(String(runner.warning || ""));
}

function runnerFetchAlertMessage(fetchState: RunnerFetchState): string | undefined {
  if (fetchState === "loading" || fetchState === "idle") {
    return "Still reading this machine's agents — the list may be incomplete.";
  }
  if (fetchState === "timed-out") {
    return "Agent status timed out — showing fallback choices while the machine retries.";
  }
  if (fetchState === "http-error") {
    return "Agent status unavailable — the machine returned an HTTP error, so the list may be incomplete.";
  }
  if (fetchState === "network-error") {
    return "Agent status unavailable — the machine could not be reached, so the list may be incomplete.";
  }
  return undefined;
}

function runnerPickerEmptyStateText(fetchState: RunnerFetchState): string {
  if (fetchState === "loading" || fetchState === "idle") {
    return "Loading agents… if this persists, make sure your dev machine has a coding agent installed (claude, codex, opencode).";
  }
  if (fetchState === "timed-out") {
    return "Agent status timed out. Retry and check the machine connection if this keeps happening.";
  }
  if (fetchState === "http-error") {
    return "Agent status unavailable because the machine returned an HTTP error. Retry, then check the logs if it keeps failing.";
  }
  if (fetchState === "network-error") {
    return "Agent status unavailable because the machine could not be reached. Retry once the connection is back.";
  }
  return "No coding agents available on this machine yet. Install claude, codex, or opencode and retry.";
}

// ── Typing indicator ─────────────────────────────────────────────────

function TypingIndicator({ color }: { color: string }) {
  const dot1 = useRef(new Animated.Value(0.3)).current;
  const dot2 = useRef(new Animated.Value(0.3)).current;
  const dot3 = useRef(new Animated.Value(0.3)).current;

  useEffect(() => {
    const animate = (dot: Animated.Value, delay: number) =>
      Animated.loop(
        Animated.sequence([
          Animated.delay(delay),
          Animated.timing(dot, { toValue: 1, duration: 400, useNativeDriver: true }),
          Animated.timing(dot, { toValue: 0.3, duration: 400, useNativeDriver: true }),
        ])
      );
    const a1 = animate(dot1, 0);
    const a2 = animate(dot2, 200);
    const a3 = animate(dot3, 400);
    a1.start(); a2.start(); a3.start();
    return () => { a1.stop(); a2.stop(); a3.stop(); };
  }, [dot1, dot2, dot3]);

  return (
    <View style={s.typingRow}>
      <View style={s.typingBubble}>
        {[dot1, dot2, dot3].map((dot, i) => (
          <Animated.View
            key={i}
            style={[s.typingDot, { backgroundColor: color, opacity: dot }]}
          />
        ))}
      </View>
    </View>
  );
}

// stripAnsi strips the most common ANSI / CSI / OSC escape sequences
// from runner stdout. Codex's `--full-auto` output is heavy on these
// — `[1mworkdir:[0m /root` etc. — and they leak into the rendered
// text on mobile because we don't have a terminal emulator in the
// chat view. Same regex shape as the agent's normalizeBrowserAuthLine
// (see desktop/agent/runner_auth_browser_http.go) and mobile's shell
// renderer (see mobile/app/shell.tsx) — kept here as a copy because
// the chat view doesn't import either.
function normalizePreviewLine(line: string): string {
  return stripMarkdownForPreview(line)
    .replace(/^\s*[-*]\s+/, "")
    .replace(/^\s*\d+\.\s+/, "")
    .replace(/\s+/g, " ")
    .trim();
}

function projectNameFromPath(path: string): string | undefined {
  const leaf = String(path || "").split(/[\\/]/).filter(Boolean).pop()?.trim();
  return leaf || undefined;
}

type ComposerProject = {
  name: string;
  path: string;
  /** Device that reported this absolute path. Paths never travel to another box. */
  deviceId?: string;
  branch?: string;
  framework?: string;
  gitRemote?: string;
};

// Top-level only — the composer contract (2026-08-09). A nested git clone
// inside another repo (e.g. <ws>/yaver.io/mobile inside <ws>/yaver.io) is NOT
// a pickable project: the outermost repo root wins, so the picker offers
// medici.ai / yaver.io / talos / sfmg — never "yaver mobile" or
// "<root> / <app>" sub-project rows. This is the client-side twin of the
// agent's collapseNestedRepos: if a box still reports a nested repo (older
// agent), the picker must not leak it.
function collapseNestedComposerProjects(projects: ComposerProject[]): ComposerProject[] {
  const sorted = [...projects].sort((a, b) => {
    const da = (a.path || "").split("/").length;
    const db = (b.path || "").split("/").length;
    if (da !== db) return da - db;
    return (a.path || "").localeCompare(b.path || "");
  });
  const kept: ComposerProject[] = [];
  for (const p of sorted) {
    const path = (p.path || "").replace(/\/+$/, "");
    let nested = false;
    for (const k of kept) {
      const root = (k.path || "").replace(/\/+$/, "");
      if (!root || root === path) continue;
      if (path.startsWith(root + "/")) {
        nested = true;
        break;
      }
    }
    if (!nested) kept.push(p);
  }
  return kept;
}

// Enrich the agent-discovered projects with the Convex runtime project
// catalog for the runner device (projectName/repoName/gitRemote/branch/
// framework — privacy-limited, never absolute paths). The catalog is the
// Convex-side memory of the same git projects; a row that matches an
// agent project by gitRemote or repoName fills in branch/framework the agent
// may not have reported. Catalog rows with NO agent match are only added when
// they carry a gitRemote we can still display — pathless rows cannot select a
// workDir, so they never become pickable entries.
function mergeConvexCatalogIntoProjects(
  projects: ComposerProject[],
  catalog?: { projectName?: string | null; repoName?: string | null; gitRemote?: string | null; branch?: string | null; framework?: string | null }[],
): ComposerProject[] {
  const rows = (catalog || []).filter((r) => r && (r.gitRemote || r.repoName));
  if (rows.length === 0) return projects;
  const norm = (v?: string | null) => String(v || "").trim().toLowerCase();
  const enriched = projects.map((p) => {
    const match = rows.find(
      (r) =>
        (p.gitRemote && r.gitRemote && norm(p.gitRemote) === norm(r.gitRemote)) ||
        (r.repoName && norm(r.repoName) === norm(projectNameFromPath(p.path))),
    );
    if (!match) return p;
    return {
      ...p,
      branch: p.branch || match.branch || undefined,
      framework: p.framework || match.framework || undefined,
      // The catalog name is the top-level repo identity (e.g. "talos"), never
      // a "<root> / <app>" monorepo-app label — those must never surface.
      name: match.projectName && !match.projectName.includes(" / ") ? match.projectName : p.name,
    };
  });
  return collapseNestedComposerProjects(enriched);
}

function extractAssistantActivity(text: string, maxItems = 4): string[] {
  const seen = new Set<string>();
  const lines = text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
  const items: string[] = [];

  for (const rawLine of lines) {
    let item = "";
    const command = rawLine.match(/^\*\*\$\s+(.+?)\*\*$/);
    if (command?.[1]) {
      item = `$ ${command[1].trim()}`;
    } else if (/^[-*]\s+/.test(rawLine) || /^\d+\.\s+/.test(rawLine)) {
      item = normalizePreviewLine(rawLine);
    }

    if (!item || item.length < 4 || seen.has(item)) continue;
    seen.add(item);
    items.push(item);
  }

  return items.slice(-maxItems);
}

// Markers that end one of the agent-injected system-context blocks.
// Keep in sync with desktop/agent/task_context.go: each entry is the
// last sentence of a `yaver*Context()` Go raw string. Codex's stream
// echoes those blocks back verbatim ahead of its actual answer; we
// slice from the LAST marker's end to recover just the assistant's
// real response. If task_context.go changes, update here.
// (moved to src/lib/promptFraming.ts — this list had drifted from the Go
// original: it never learned about the boundary sentinel, so chat-mode tasks,
// the per-turn screen-context block and [Attached images] all
// survived the strip and rendered in the bubble. There is now ONE list, and
// promptFramingParity.test.ts fails when it disagrees with the Go source.)

// Collapse codex's repeated/redundant blocks. codex 0.123.0 prints the
// same listing up to three times for a simple "Run ls":
//   (1) the raw exec output (rows after `succeeded in Xms:`)
//   (2) a final "Here is …" paragraph + ```text fenced block
//   (3) the same paragraph + fence emitted a second time (codex bug)
// We keep one structured copy. The exec announcement is reduced to a
// `$ <cmd>` header (mirroring Claude's stream_json `**$ <cmd>**`
// pattern) so users still see *what was run* without the raw output
// duplicating the fenced block below it.
// dedupeOpencodeEchoes strips bare bash-tool stdout that follows a
// `**$ <cmd>**` marker when the same rows are also re-rendered inside
// a fenced block elsewhere in the message. opencode + glm-4.7 routinely
// answer "run ls" by (a) printing the listing as the bash tool's raw
// output, then (b) re-rendering the same listing inside a ```text fence
// as the formatted answer — the bare rows in (a) are pure noise once
// (b) lands. Without this, the mobile collapsed view picks the first
// stdout row ("bootstrap.sh") as its summary and the bubble looks
// broken (image: bottom screenshot in the WhatsApp dump).
//
// Mirrors dedupeOpencodeEchoes in desktop/agent/result_cleanup.go —
// keep both in sync.
function dedupeOpencodeEchoes(s: string): string {
  const fenceContents: Set<string>[] = [];
  const fenceRE = /```[^\n]*\n([\s\S]*?)\n```/g;
  let fm: RegExpExecArray | null;
  while ((fm = fenceRE.exec(s)) !== null) {
    const set = new Set<string>();
    for (const line of fm[1].split("\n")) {
      const t = line.trim();
      if (t) set.add(t);
    }
    if (set.size > 0) fenceContents.push(set);
  }
  if (fenceContents.length === 0) return s;

  const markerRE = /\n\*\*\$\s+[^\n]+\*\*\n/g;
  let result = "";
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = markerRE.exec(s)) !== null) {
    const markerEnd = m.index + m[0].length;
    result += s.slice(last, markerEnd);
    last = markerEnd;

    const rest = s.slice(last);
    let end = rest.length;
    const blank = rest.indexOf("\n\n");
    if (blank >= 0 && blank < end) end = blank;
    const fenceStart = rest.indexOf("\n```");
    if (fenceStart >= 0 && fenceStart < end) end = fenceStart;
    if (end <= 0) continue;

    const rowLines = rest
      .slice(0, end)
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean);
    if (rowLines.length < 3) continue;

    const threshold = Math.max(3, Math.floor((rowLines.length * 7) / 10));
    let dropped = false;
    for (const fence of fenceContents) {
      let hit = 0;
      for (const row of rowLines) {
        if (fence.has(row)) hit++;
      }
      if (hit >= threshold) {
        dropped = true;
        break;
      }
    }
    if (dropped) {
      last += end;
    }
  }
  result += s.slice(last);
  return result;
}

function dedupeCodexEchoes(s: string): string {
  // (1) Replace `exec\n<cmd>\n succeeded in Xms:\n<rows>` blocks with
  // a `**$ <cmd>**` line, dropping the raw rows. The rows are almost
  // always echoed inside a fenced block by codex's final answer, and
  // when they aren't the Logs panel still has the full stream.
  s = s.replace(
    /\n?exec\n([^\n]+?)(?:\s+in\s+[^\n]+)?\n\s*succeeded in [\d.]+\s*m?s:\n[\s\S]*?(?=\n\n|\ncodex\n|$)/g,
    (_match, cmd: string) => `\n**$ ${String(cmd).trim()}**\n`,
  );
  // (2) Strip the lone `codex` section markers — they're left over
  // from ANSI-coloured `[codex]` headers and add no signal once the
  // body text follows.
  s = s.replace(/(^|\n)codex\n/g, "$1");
  // (3) Collapse two consecutive identical fenced code blocks
  // (codex's duplicate-message bug).
  s = s.replace(/(```[^\n]*\n[\s\S]*?\n```)\s*\n+\1/g, "$1");
  // (4) Collapse a "<lead-in>:\n\n```fenced```" pair that repeats
  // verbatim — e.g. "Here is the ls output … ```…``` Here is the ls
  // output … ```…```".
  s = s.replace(
    /([^\n]+:\s*\n+```[^\n]*\n[\s\S]*?\n```)\s*\n+\1/g,
    "$1",
  );
  return s;
}

// Some runner/relay combinations deliver the completed assistant payload twice
// inside ONE turn: once as the final streamed frame and once as the terminal
// result. Adjacent-line dedupe cannot see that shape because the repeated unit
// is a whole multi-line response. Collapse only an exact normalized repeat
// whose second copy begins with the same first meaningful line; this preserves
// intentional repeated lines inside an otherwise different answer.
function dedupeRepeatedAssistantResponse(s: string): string {
  let out = s.trim();
  for (let pass = 0; pass < 2; pass++) {
    const lines = out.replace(/\r/g, "").split("\n");
    const firstIndex = lines.findIndex((line) => stripAnsi(line).trim().length > 0);
    if (firstIndex < 0) return out;
    const firstLine = stripAnsi(lines[firstIndex]).trim();
    let collapsed = false;
    let candidates = 0;
    for (let i = firstIndex + 1; i < lines.length && candidates < 8; i++) {
      if (stripAnsi(lines[i]).trim() !== firstLine) continue;
      candidates += 1;
      const left = lines.slice(firstIndex, i).join("\n").trim();
      const right = lines.slice(i).join("\n").trim();
      const normalize = (value: string) => stripAnsi(value)
        .replace(/[ \t]+\n/g, "\n")
        .replace(/\n{3,}/g, "\n\n")
        .trim();
      if (left.length >= 24 && normalize(left) === normalize(right)) {
        out = left;
        collapsed = true;
        break;
      }
    }
    if (!collapsed) break;
  }
  return out;
}

// stripPromptEcho removes the noisy preamble that wraps a runner's
// actual answer when streaming. Three layers:
//   1. Our own injected system-context blocks (Codex echoes them) —
//      sliced off using SYSTEM_CONTEXT_END_MARKERS.
//   2. The Codex CLI's own banner + config dump ("Reading additional
//      input from stdin…", "OpenAI Codex v0.123.0", workdir/model/
//      provider/approval/sandbox lines).
//   3. Codex's redundant exec-output + duplicated fenced-block echoes
//      (see dedupeCodexEchoes above).
// Plus the trailing "tokens used N" footer Codex prints after the
// answer. Returns the bubble's MEANINGFUL content; the original raw
// stays available for the "Show details" expanded view.
function stripPromptEcho(content: string): string {
  if (!content) return content;
  let out = stripAnsi(content);

  // Slice after the last system-context end marker if any are present.
  let bestIdx = -1;
  for (const marker of SYSTEM_CONTEXT_END_MARKERS) {
    const idx = out.lastIndexOf(marker);
    if (idx >= 0 && idx + marker.length > bestIdx) {
      bestIdx = idx + marker.length;
    }
  }
  if (bestIdx > 0) {
    out = out.slice(bestIdx);
  }

  // Strip Codex CLI preamble (banner + config dump). Pattern: optional
  // "Reading additional input from stdin…" then "OpenAI Codex vX.Y.Z"
  // line then config keys until the first blank line.
  out = out.replace(/^[\s\S]*?OpenAI Codex v[^\n]*\n(?:[\s\S]*?\n)?\s*\n/, "");
  out = out.replace(/^Reading additional input from stdin[.…]*\s*\n?/, "");

  // Strip every "tokens used\n<number>" footer codex emits, not just
  // the trailing one. Codex 0.123.0 frequently prints its final answer
  // TWICE with this footer wedged between the two copies — leaving the
  // mid-stream footer in place breaks dedupeCodexEchoes (the two
  // identical blocks aren't adjacent), so the listing renders twice
  // on the phone. Drop them all; users don't read token counts on
  // mobile anyway.
  out = out.replace(/\n*\s*tokens used\s*\n?\s*[\d,]+\s*/gi, "\n\n");

  out = dedupeCodexEchoes(out);
  out = dedupeOpencodeEchoes(out);
  out = dedupeRepeatedAssistantResponse(out);

  return out.trim();
}

function buildAssistantPreview(content: string): {
  summary: string;
  cleaned: string;
  activity: string[];
  shouldCollapse: boolean;
  hasHiddenNoise: boolean;
} {
  const cleaned = stripPromptEcho(content);
  const plain = stripMarkdownForPreview(cleaned);
  const summaryLines = plain
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .filter((line) => !line.startsWith("$ "));
  // First non-empty line of the cleaned content as the summary, capped
  // at ~140 chars. Everything else (additional cleaned lines, activity
  // bullets, the raw uncleaned stream) goes behind "Show details".
  const firstLine = summaryLines[0] ?? "";
  const summary = firstLine.length > 140 ? firstLine.slice(0, 137) + "…" : firstLine;
  const activity = extractAssistantActivity(cleaned);
  const hasHiddenNoise = content.length > cleaned.length + 40;
  // shouldCollapse = the cleaned content is genuinely long and the summary
  // is a useful compression of it. For short, structured answers (e.g.
  // `ls` → `**$ ls**` + "18 items..." + fence) the fence IS the answer,
  // and collapsing to a one-line summary + activity bullet hides what the
  // user actually asked for. We only collapse when the cleaned content
  // exceeds ~30 non-empty lines OR ~2500 chars — past that, scrolling cost
  // outweighs the loss of seeing the full answer inline.
  //
  // (Previously this triggered on `cleaned.includes("```")` alone, which
  // forced every tool-output answer behind a "Show details" tap. Image #3
  // in the WhatsApp dump shows the failure mode: bare "bootstrap.sh" +
  // "$ ls" as the entire bubble.)
  const cleanedNonEmptyLines = cleaned
    .split("\n")
    .filter((line) => line.trim()).length;
  const hasMore =
    cleanedNonEmptyLines > 30 || cleaned.length > 2500;

  return {
    summary: summary || fallbackActivitySummary(activity),
    cleaned,
    activity,
    shouldCollapse: hasMore,
    hasHiddenNoise,
  };
}

function fallbackActivitySummary(activity: string[]): string {
  const last = activity[activity.length - 1]?.trim();
  return last ? `Working now: ${last}` : "Still working.";
}

function buildLiveAssistantMarkdown(content: string): string {
  const preview = buildAssistantPreview(content);
  const cleaned = preview.cleaned
    .replace(/```[\s\S]*?```/g, "\n_Code/details hidden while work continues._\n");
  const lines = cleaned
    .split("\n")
    .map((line) => line.trimEnd());

  const visible: string[] = [];
  let hidden = false;
  let chars = 0;

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line) {
      if (visible.length > 0 && visible[visible.length - 1] !== "") visible.push("");
      continue;
    }
    if (/^\*\*\$\s+.+\*\*$/.test(line)) {
      hidden = true;
      continue;
    }
    if (/^(workdir|model|provider|approval|sandbox|reasoning effort|session id):/i.test(line)) {
      hidden = true;
      continue;
    }
    if (/^(diff --git|index [0-9a-f]+\.\.[0-9a-f]+|@@ |--- |\+\+\+ )/.test(line)) {
      hidden = true;
      continue;
    }
    if (/^[{}[\];(),.=><:+\-/*\\|'"`_]+$/.test(line)) {
      hidden = true;
      continue;
    }
    visible.push(rawLine);
    chars += rawLine.length;
    if (visible.length >= 12 || chars >= 1400) {
      hidden = true;
      break;
    }
  }

  const body = visible.join("\n").replace(/\n{3,}/g, "\n\n").trim();
  if (!body) {
    if (!preview.activity.length) {
      return "Still working. I’ll post the next concrete step here.";
    }
    return `${fallbackActivitySummary(preview.activity)}\n\n${preview.activity.map((item) => `- ${item}`).join("\n")}`.trim();
  }
  if (!hidden && !preview.activity.length) return body;
  const activity = preview.activity.length > 0
    ? `\n\n${preview.activity.map((item) => `- ${item}`).join("\n")}`
    : "";
  return `${body}${activity}`.trim();
}

// ── Summarized console (mobile: same style, fewer words) ──────────────
// Mobile keeps the console STYLE (AnsiConsoleText) but a summarized
// payload — the full raw stream is megabytes of tool noise a phone screen
// shouldn't spend pixels on. The reducer lives in shared/client-core
// (src/_core/ansi.ts, sync'd) so web + mobile collapse the SAME grammar;
// this local wrapper only picks mobile-sized budgets.
function summarizeRawConsole(raw: string, running: boolean): string {
  return _summarizeRawConsole(raw, running);
}

// The preview is one line, capped at 120 chars — so it must never touch
// more than a bounded slice of the task. It used to run the whole output
// buffer (MAX_OUTPUT_LINES_PER_TASK = 8000 lines) through 12 chained
// regexes and then a per-line stripAnsi pass, on every render of every
// card, just to read the LAST line. With output streaming in, the list
// re-renders continuously and that pegged the JS thread: taps and scroll
// gestures need JS to negotiate the touch responder, so the whole Tasks
// screen went dead while tasks were running. Scan the tail only.
function normalizeTaskTitle(title: string): string {
  const trimmed = title.trim();
  if (!trimmed) return "Task";
  const replacements: Array<[RegExp, string]> = [
    [/^(expo|react native|rn|xcode|gradle|flutter)\s+build\b.*$/i, "Build"],
    [/^(expo|react native|rn|hermes)\s+bundle\b.*$/i, "Hot Reload"],
    [/^(expo|react native|rn|flutter)\s+hot\s*reload\b.*$/i, "Hot Reload"],
    [/^(ios|android)\s+build\b.*$/i, "Build"],
  ];
  for (const [pattern, replacement] of replacements) {
    if (pattern.test(trimmed)) return replacement;
  }
  return trimmed;
}

// A bare reload *command* typed (or dictated) into the composer —
// "reload", "hot reload", "hermes [reload]", "rebuild [bundle]",
// "push bundle" — optionally followed by a single project token
// ("reload sfmg"). These map straight to a dev-server reload on the
// connected machine rather than spinning up a whole agent task.
//
// Kept deliberately tight: the trailing capture allows at most one
// path-safe token (no spaces), so a genuine task phrased as a sentence —
// "reload the user list after delete" — falls through to a normal task
// because "the user list…" contains spaces and fails the `\s*$` anchor.
function taskStatusAllowsRuntimeRender(status?: TaskStatus | null): boolean {
  return status === "ready" || status === "completed" || status === "review";
}

function taskStatusMeansRunnerIsCoding(status?: TaskStatus | null): boolean {
  return status === "queued" || status === "running";
}

type TaskPhaseTone = "neutral" | "active" | "warm" | "success";

function deriveTaskPhases(task: Task): Array<{ label: string; tone: TaskPhaseTone }> {
  const latest = friendlyTaskPresentation(task.presentation)
    .filter((item) => item.kind !== "message" && item.text.trim())
    .at(-1);
  if (latest) {
    return [{
      label: latest.text,
      tone: latest.kind === "error" || latest.kind === "warning" || latest.kind === "action_required"
        ? "warm"
        : latest.state === "completed" || latest.state === "review" ? "success" : "active",
    }];
  }
  return [{ label: task.status === "queued" ? "Waiting to start" : "Working", tone: "active" }];
}

function PhaseChip({ task }: { task: Task }) {
  const c = useColors();
  const phases = useMemo(() => deriveTaskPhases(task), [task.id, task.presentation, task.status]);
  const [idx, setIdx] = useState(0);
  const fade = useRef(new Animated.Value(1)).current;

  useEffect(() => {
    setIdx(0);
  }, [phases.length, task.id]);

  useEffect(() => {
    if (task.status !== "running" && task.status !== "queued") return;
    if (phases.length <= 1) return;
    const interval = setInterval(() => {
      Animated.sequence([
        Animated.timing(fade, { toValue: 0.35, duration: 180, useNativeDriver: true }),
        Animated.timing(fade, { toValue: 1, duration: 220, useNativeDriver: true }),
      ]).start();
      setIdx((value) => (value + 1) % phases.length);
    }, 1800);
    return () => clearInterval(interval);
  }, [fade, phases.length, task.status]);

  const current = phases[idx] || phases[0];
  const palette =
    current?.tone === "success"
      ? { bg: "#22c55e16", border: "#22c55e33", fg: "#4ade80" }
      : current?.tone === "warm"
        ? { bg: "#f9731614", border: "#f9731633", fg: "#fb923c" }
        : current?.tone === "neutral"
          ? { bg: c.bgCardElevated, border: c.border, fg: c.textMuted }
          : { bg: "#6366f118", border: "#6366f133", fg: "#818cf8" };

  return (
    <Animated.View style={{ opacity: fade }}>
      <View style={[s.phaseChip, { backgroundColor: palette.bg, borderColor: palette.border }]}>
        <Text style={[s.phaseChipText, { color: palette.fg }]}>{current?.label || "working"}</Text>
      </View>
    </Animated.View>
  );
}

// Braille-spinner cycle. Same set Claude Code / Codex CLIs use for
// "in progress" indicators — feels native to anyone who's watched
// either CLI work, and stays visually quiet at small sizes (no big
// spinning circle to dominate the line).
const PHASE_SPINNER_FRAMES = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];

// Animated three-dot assistant bubble shown while the runner is
// spinning up but hasn't emitted any chat text yet. Without it the
// chat shows only the user turn for the 3–10s of a Codex/Claude
// cold start, which feels like Send did nothing.
function ThinkingBubble({ runner, deviceName }: { runner?: string; deviceName?: string }) {
  const c = useColors();
  const dotOpacity = useRef([new Animated.Value(0.25), new Animated.Value(0.25), new Animated.Value(0.25)]).current;
  useEffect(() => {
    const loops = dotOpacity.map((v, i) =>
      Animated.loop(
        Animated.sequence([
          Animated.delay(i * 180),
          Animated.timing(v, { toValue: 1, duration: 360, useNativeDriver: true }),
          Animated.timing(v, { toValue: 0.25, duration: 360, useNativeDriver: true }),
          Animated.delay(180),
        ]),
      ),
    );
    loops.forEach((l) => l.start());
    return () => loops.forEach((l) => l.stop());
  }, [dotOpacity]);
  const subtitle = runner && deviceName ? `${runner} · ${deviceName}` : runner || deviceName || "thinking";
  return (
    <View style={{ paddingHorizontal: 16, paddingVertical: 6 }}>
      <View style={{
        alignSelf: "flex-start",
        borderWidth: 1,
        borderColor: c.border,
        borderRadius: 12,
        paddingHorizontal: 14,
        paddingVertical: 10,
        flexDirection: "row",
        alignItems: "center",
        gap: 6,
      }}>
        {dotOpacity.map((v, i) => (
          <Animated.View
            key={i}
            style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: c.textMuted, opacity: v }}
          />
        ))}
        <Text style={{ color: c.textMuted, fontSize: 12, marginLeft: 8 }}>{subtitle}</Text>
      </View>
    </View>
  );
}

/// Single-line streaming status: morphing braille spinner + the
/// current derived phase ("searching", "compiling", …). Replaces
/// the prior two-block pattern (big TypingIndicator → "Working…"
/// label → activity-spinner → "Working…" label) at the bottom of
/// the task detail view, and the inline PhaseChip at the top.
/// Designed to overwrite ITSELF as the runner moves through phases
/// rather than stack a new line for each — the user's mental model
/// is "what is it doing right now", not "what did it do already".
function PhaseStatusLine({ task }: { task: Task }) {
  const c = useColors();
  const phases = useMemo(
    () => deriveTaskPhases(task),
    [task.id, task.presentation, task.status]
  );
  const isRunning = task.status === "running" || task.status === "queued";
  const [phaseIdx, setPhaseIdx] = useState(0);
  const [spinIdx, setSpinIdx] = useState(0);
  const [elapsedSec, setElapsedSec] = useState(() =>
    Math.max(0, Math.floor((Date.now() - task.createdAt) / 1000)),
  );
  const fade = useRef(new Animated.Value(1)).current;

  // Spinner: ~10 fps, cheap to keep alive — only mounts while the
  // task is running.
  useEffect(() => {
    if (!isRunning) return;
    const t = setInterval(() => {
      setSpinIdx((v) => (v + 1) % PHASE_SPINNER_FRAMES.length);
    }, 90);
    return () => clearInterval(t);
  }, [isRunning]);

  // Elapsed timer — ticks every 1s while running. Spec B3 fallback:
  // "Working · 4s", "Still working · 12s". Bumps a number, doesn't
  // touch the chat surface.
  useEffect(() => {
    if (!isRunning) return;
    const t = setInterval(() => {
      setElapsedSec(Math.max(0, Math.floor((Date.now() - task.createdAt) / 1000)));
    }, 1000);
    return () => clearInterval(t);
  }, [isRunning, task.createdAt]);

  // Phase rotation: same 1.8s cadence + fade-flip the inline pill
  // already used.
  useEffect(() => {
    if (!isRunning || phases.length <= 1) return;
    const t = setInterval(() => {
      Animated.sequence([
        Animated.timing(fade, { toValue: 0.35, duration: 180, useNativeDriver: true }),
        Animated.timing(fade, { toValue: 1, duration: 220, useNativeDriver: true }),
      ]).start();
      setPhaseIdx((v) => (v + 1) % phases.length);
    }, 1800);
    return () => clearInterval(t);
  }, [fade, isRunning, phases.length]);

  if (!isRunning) return null;
  const current = phases[phaseIdx] || phases[0];
  const tint =
    current?.tone === "success"
      ? "#4ade80"
      : current?.tone === "warm"
        ? "#fb923c"
        : current?.tone === "neutral"
          ? c.textMuted
          : "#818cf8";
  return (
    <Animated.View style={{ flexDirection: "row", alignItems: "center", paddingVertical: 6, opacity: fade }}>
      <Text style={{
        color: tint,
        fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
        fontSize: 14,
        width: 20,
        textAlign: "center",
      }}>
        {PHASE_SPINNER_FRAMES[spinIdx]}
      </Text>
      <Text style={{
        color: tint,
        fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
        fontSize: 13,
        marginLeft: 4,
      }}>
        {current?.label || "working"}…
      </Text>
      {/* Elapsed counter — switches to "still working" past 10s so
          the user knows the agent is alive and we're not stuck. */}
      <Text style={{
        color: c.textTertiary,
        fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
        fontSize: 12,
        marginLeft: 8,
      }}>
        · {elapsedSec >= 10 ? "still working " : ""}{elapsedSec}s
      </Text>
    </Animated.View>
  );
}

// An agent call that fails because Convex rejected the bearer (token
// expired / rotated-away / revoked) must NOT masquerade as a task
// failure. The agent surfaces these as 401/403 or a "token validation"
// message. Detect them so the UI says "sign in again" instead of the
// misleading "Task failed / Aborted".
function isAuthError(e: unknown): boolean {
  const msg = (e instanceof Error ? e.message : String(e || "")).toLowerCase();
  return (
    /\b401\b|\b403\b/.test(msg) ||
    msg.includes("unauthorized") ||
    msg.includes("token validation") ||
    msg.includes("validate token") ||
    msg.includes("session expired") ||
    msg.includes("not signed in")
  );
}

// ── Chat bubble ──────────────────────────────────────────────────────

type ChatBubbleProps = {
  turn: { role: string; content: string };
  c: ReturnType<typeof useColors>;
  /** When set, render a small "tokens used N" header above the assistant
   *  prose. Only meaningful for assistant bubbles, and only the LAST one
   *  (the runner reports usage as a single total on task completion). */
  tokens?: { input: number; output: number } | null;
};

// React.memo with a content-equality comparator. Without it, every streaming
// token append rebuilt chatMessages from scratch (new turn objects every
// time), which made the ScrollView .map() re-render every prior bubble on
// every token — O(n) work per token, and the markdown renderer is heavy.
// That stall on the JS thread is what made the keyboard feel dead while
// the agent was streaming. Comparing turn.content (string identity) lets
// only the bubble whose text actually changed re-render.
const ChatBubble = React.memo(ChatBubbleImpl, (prev, next) => {
  return (
    prev.turn.role === next.turn.role &&
    prev.turn.content === next.turn.content &&
    prev.c === next.c &&
    (prev.tokens?.input ?? 0) === (next.tokens?.input ?? 0) &&
    (prev.tokens?.output ?? 0) === (next.tokens?.output ?? 0)
  );
});

function ChatBubbleImpl({
  turn,
  c,
  tokens,
}: ChatBubbleProps) {
  const isUser = turn.role === "user";
  // Cap user bubble at 640pt on tablets — see MessageBubble.tsx for
  // the same reason. Phones never hit the cap.
  const winWidth = Dimensions.get("window").width;
  const userBubbleCap = { maxWidth: Math.min(winWidth * 0.8, 640) };

  // Assistant turns are already agent-owned presentation messages. This
  // surface renders them directly; it must not classify terminal text,
  // commands, diffs, or model prose on its own.
  const [copied, setCopied] = useState(false);
  const copiedTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const copyInFlightRef = useRef(false);
  useEffect(() => () => {
    if (copiedTimerRef.current) clearTimeout(copiedTimerRef.current);
  }, []);
  const copyMessage = useCallback(() => {
    // Pressable expects a synchronous gesture callback. Keep the clipboard
    // promise inside it so a native rejection cannot make a long press look
    // ignored, especially on the blue user bubbles.
    if (copyInFlightRef.current || !turn.content) return;
    copyInFlightRef.current = true;
    void ExpoClipboard.setStringAsync(turn.content)
      .then(() => {
        taskHaptics.suggestionAppeared();
        setCopied(true);
        if (copiedTimerRef.current) clearTimeout(copiedTimerRef.current);
        copiedTimerRef.current = setTimeout(() => setCopied(false), 1400);
      })
      .catch(() => {
        Alert.alert("Copy failed", "Yaver could not access the clipboard. Try again.");
      })
      .finally(() => {
        copyInFlightRef.current = false;
      });
  }, [turn.content]);
  if (isUser) {
    return (
      <View style={s.userRow}>
        <Pressable
          style={[s.userBubble, userBubbleCap, { backgroundColor: c.accent || "#6366f1" }]}
          onLongPress={copyMessage}
          delayLongPress={500}
          testID="task-user-message"
          accessibilityRole="button"
          accessibilityLabel={`Your message: ${turn.content}`}
          accessibilityHint="Long press to copy this message"
        >
          <Text style={s.userBubbleText}>{turn.content}</Text>
          {copied ? <Text style={s.userCopiedText}>Copied</Text> : null}
        </Pressable>
      </View>
    );
  }

  const totalTokens = tokens ? tokens.input + tokens.output : 0;
  return (
    <View style={s.assistantRow}>
      <Pressable
        style={[s.assistantFrame, { backgroundColor: c.bgCard, borderColor: c.border }]}
        onLongPress={copyMessage}
        delayLongPress={500}
        testID="task-assistant-message"
        accessibilityRole="button"
        accessibilityLabel={`Runner message: ${turn.content}`}
        accessibilityHint="Long press to copy this runner message"
      >
        {totalTokens > 0 ? (
          <Text style={[s.assistantTokens, { color: c.textMuted }]}>
            tokens used {totalTokens.toLocaleString()}
          </Text>
        ) : null}
        <Markdown style={markdownStyles(c)}>
          {turn.content || " "}
        </Markdown>
        <View style={s.assistantMetaRow}>
          {copied ? (
            <Text style={[s.assistantCopiedText, { color: c.textMuted }]}>Copied</Text>
          ) : null}
        </View>
      </Pressable>
    </View>
  );
}

// ── Debug section (foldable) ─────────────────────────────────────────

function DebugSection({
  task,
  connMode,
  c,
}: {
  task: Task;
  connMode: ConnectionMode;
  c: ReturnType<typeof useColors>;
}) {
  const [expanded, setExpanded] = useState(false);

  return (
    <View style={s.debugContainer}>
      <Pressable
        style={[s.debugToggle, { backgroundColor: c.bgCard, borderColor: c.border }]}
        onPress={() => setExpanded(!expanded)}
      >
        <Text style={[s.debugToggleText, { color: c.textMuted }]}>
          {expanded ? "\u25BC" : "\u25B6"} Debug
        </Text>
      </Pressable>
      {expanded && (
        <View style={[s.debugContent, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[s.debugLine, { color: c.textMuted }]}>Task ID: {task.id}</Text>
          <Text style={[s.debugLine, { color: c.textMuted }]}>Status: {task.status}</Text>
          <Text style={[s.debugLine, { color: c.textMuted }]}>Output lines: {task.output.length}</Text>
          <Text style={[s.debugLine, { color: c.textMuted }]}>Output chars: {task.output.join("").length}</Text>
          <Text style={[s.debugLine, { color: c.textMuted }]}>Mode: {connMode || "null"}</Text>
          <Text style={[s.debugLine, { color: c.textMuted }]}>Base URL: {quicClient.connectionMode === "relay" ? "relay" : "direct"}</Text>
          {task.resultText ? (
            <Text style={[s.debugLine, { color: c.textMuted }]}>Result: {task.resultText.length} chars</Text>
          ) : null}
          <Text style={[s.debugLine, { color: c.textMuted }]}>Created: {new Date(task.createdAt).toLocaleTimeString()}</Text>
        </View>
      )}
    </View>
  );
}

// ── Task card ────────────────────────────────────────────────────────

function TaskCardInner({
  item,
  onPress,
  onDelete,
  onComplete,
  onBlockedAction,
  selectionMode = false,
  selected = false,
  onToggleSelection,
  onEnterSelection,
}: {
  item: Task;
  onPress: () => void;
  onDelete: () => void;
  onComplete: () => void;
  onBlockedAction?: (task: Task) => void;
  selectionMode?: boolean;
  selected?: boolean;
  onToggleSelection?: () => void;
  onEnterSelection?: () => void;
}) {
  const c = useColors();
  const { isDark } = useTheme();
  const signal = agentSignalFromTask(item);
  const statusColor = agentStateColor(signal.state, c);
  const isRunning = item.status === "running" || item.status === "queued";
  const enter = useRef(new Animated.Value(0)).current;
  const pulse = useRef(new Animated.Value(isRunning ? 0.55 : 1)).current;

  useEffect(() => {
    Animated.spring(enter, {
      toValue: 1,
      useNativeDriver: true,
      damping: 18,
      stiffness: 180,
      mass: 0.7,
    }).start();
  }, [enter]);

  useEffect(() => {
    if (!isRunning) {
      pulse.stopAnimation();
      pulse.setValue(1);
      return;
    }
    const loop = Animated.loop(
      Animated.sequence([
        Animated.timing(pulse, { toValue: 1, duration: 900, useNativeDriver: true }),
        Animated.timing(pulse, { toValue: 0.45, duration: 900, useNativeDriver: true }),
      ])
    );
    loop.start();
    return () => loop.stop();
  }, [isRunning, pulse]);

  const handleActions = () => {
    // Long-press menu — manual control over auto-completion. Without
    // this, the only way to "finish" a task was to wait for the runner
    // to exit on its own. Now: running/review tasks expose a "Mark
    // complete" action that stops the runner and flips status to
    // completed; completed tasks expose delete only.
    const canMarkComplete =
      item.status === "running" ||
      item.status === "queued" ||
      item.status === "review";
    if (Platform.OS === "web") {
      const title = normalizeTaskTitle(item.title);
      if (canMarkComplete) {
        const complete = typeof window !== "undefined"
          ? window.confirm(`Mark "${title}" complete?\n\nPress Cancel to keep it, or use the next prompt to delete it.`)
          : false;
        if (complete) {
          onComplete();
          return;
        }
      }
      const remove = typeof window !== "undefined"
        ? window.confirm(`Remove "${title}" from Tasks?`)
        : false;
      if (remove) onDelete();
      return;
    }
    if (canMarkComplete) {
      Alert.alert("Task actions", normalizeTaskTitle(item.title), [
        { text: "Mark complete", onPress: onComplete },
        { text: "Delete", style: "destructive", onPress: onDelete },
        { text: "Cancel", style: "cancel" },
      ]);
    } else {
      Alert.alert("Delete Task", "Are you sure?", [
        { text: "Cancel", style: "cancel" },
        { text: "Delete", style: "destructive", onPress: onDelete },
      ]);
    }
  };

  const handleLongPress = () => {
    if (selectionMode) {
      onToggleSelection?.();
      return;
    }
    if (onEnterSelection) {
      onEnterSelection();
      return;
    }
    handleActions();
  };

  const handleActionPress = (event: GestureResponderEvent) => {
    event.stopPropagation();
    // RN-web nests this Pressable inside the card's TouchableOpacity. Stop the
    // DOM event too so the card does not open before the action can run.
    const nativeEvent = event.nativeEvent as unknown as {
      stopPropagation?: () => void;
      preventDefault?: () => void;
    };
    nativeEvent.stopPropagation?.();
    nativeEvent.preventDefault?.();
    handleActions();
  };

  // Last line is part of the key because capOutput() pins output.length
  // at the cap for long-running tasks — see chatMessages for the full
  // reasoning. The preview reads the tail, so the last line is what moves.
  const previewText = useMemo(
    () => buildTaskPreviewText(item),
    [item.id, item.status, item.resultText, item.output.length, item.presentation],
  );
  const blockedReason = String(item.pendingCloudBlockedReason || "").trim();
  const isPendingRemoteTask = item.id.startsWith("pending-cloud:");
  const expiresInHours =
    typeof item.pendingCloudExpiresAt === "number"
      ? Math.max(0, Math.ceil((item.pendingCloudExpiresAt - Date.now()) / 3_600_000))
      : null;
  const blockedActionLabel =
    item.pendingCloudBlockedAction === "runner_auth_required" ? `Sign in to ${displayRunnerLabel(item.runnerId || "runner")}` :
    item.pendingCloudBlockedAction === "resize_required" ||
    item.pendingCloudBlockedAction === "resize_failed" ||
    item.pendingCloudBlockedAction === "wake_failed" ? "Retry" :
    item.pendingCloudBlockedAction ? "Open Yaver web" :
    "";

  return (
    <Animated.View
      style={{
        opacity: enter,
        transform: [
          {
            translateY: enter.interpolate({
              inputRange: [0, 1],
              outputRange: [14, 0],
            }),
          },
          {
            scale: enter.interpolate({
              inputRange: [0, 1],
              outputRange: [0.98, 1],
            }),
          },
        ],
      }}
    >
      <TouchableOpacity
        style={[
          s.cardContainer,
          s.taskCard,
          { backgroundColor: c.bgCard, borderColor: c.borderSubtle },
          selected && { borderColor: c.accent, backgroundColor: c.accentSoft },
          !isDark && { shadowColor: c.shadowSm },
        ]}
        onPress={onPress}
        onLongPress={handleLongPress}
        activeOpacity={0.86}
      >
        <View style={s.taskHeader}>
          <View style={s.taskHeaderMain}>
            {selectionMode ? (
              <View
                style={[s.taskSelectionMark, { borderColor: selected ? c.accent : c.textMuted, backgroundColor: selected ? c.accent : "transparent" }]}
                accessibilityLabel={selected ? "Task selected" : "Task not selected"}
              >
                {selected ? <Ionicons name="checkmark" size={13} color="#000" /> : null}
              </View>
            ) : null}
            <View style={[s.statusBadge, { backgroundColor: agentStateBg(signal.state, c), borderColor: statusColor + "45" }]}>
              {signal.pulse ? (
                <Animated.View style={[s.statusPulseDot, { backgroundColor: statusColor, opacity: pulse }]} />
              ) : (
                <View
                  style={[
                    s.statusPulseDot,
                    // Hollow = we cannot confirm it: queued has been accepted but
                    // is not spending yet. Fill would claim more than we know.
                    signal.hollow
                      ? { borderWidth: 1.5, borderColor: statusColor, backgroundColor: "transparent" }
                      : { backgroundColor: statusColor },
                  ]}
                />
              )}
              <Text style={[s.statusText, { color: statusColor }]}>
                {signal.label.charAt(0).toUpperCase() + signal.label.slice(1)}
              </Text>
            </View>
            {/* Hosting details (tmux today, native desktop adapters later)
                belong behind Agent context. The overview is a Task list, not
                an inventory of implementation-level sessions. */}
            {item.chainId && (
              <View style={[s.metaPill, { backgroundColor: "#06b6d412", borderColor: "#06b6d433" }]}>
                <Text style={[s.metaPillText, { color: "#06b6d4" }]}>{`chain ${(item.chainOrder ?? 0) + 1}`}</Text>
              </View>
            )}
          </View>
          {/* Device + model on the right of the card header. Both come from
              the task's authoritative fields, so a task that ran on a
              non-focused box does not inherit the focused device/model. */}
          <View style={s.taskHeaderMeta}>
            {(() => {
              const dn = (item.deviceName || "").trim().replace(/\.local$/, "");
              if (!dn) return null;
              return (
                <View style={[s.ipPill, { backgroundColor: c.bgInput, borderColor: c.borderSubtle }]}>
                  <Text style={[s.ipPillText, { color: c.textMuted }]} numberOfLines={1}>
                    {dn}
                  </Text>
                </View>
              );
            })()}
            {(() => {
              // The runner is already implicit in the conversation. Show the
              // exact model id the agent recorded (including an OpenCode
              // provider prefix) plus Codex effort. Do not prettify it into a
              // different-looking id. Legacy tasks without a model fall back
              // to the runner instead of inventing one.
              const rawModel = String(item.model || "").trim();
              const effort = String(item.reasoningEffort || "").trim();
              const modelLabel = rawModel
                ? `${rawModel}${effort ? ` · ${effort}` : ""}`
                : item.runnerId ? displayRunnerLabel(item.runnerId) : "";
              if (!modelLabel) return null;
              return (
                <Text style={[s.taskRunnerLabel, { color: c.textMuted }]} numberOfLines={1}>
                  {modelLabel}
                </Text>
              );
            })()}
            {!selectionMode ? <Pressable
              hitSlop={12}
              onPress={handleActionPress}
              style={({ pressed }) => [
                s.taskActionButton,
                { backgroundColor: c.bgInput, borderColor: c.borderSubtle },
                pressed && { opacity: 0.7 },
              ]}
              accessibilityRole="button"
              accessibilityLabel="Task actions"
            >
              <Ionicons name="ellipsis-horizontal" size={18} color={c.textMuted} />
            </Pressable> : null}
          </View>
        </View>
        <Text style={[s.taskTitle, { color: c.textPrimary }]} numberOfLines={2}>{normalizeTaskTitle(item.title)}</Text>
        {isPendingRemoteTask && (blockedReason || item.pendingCloudBlockedAction || item.status === "stopped") ? (
          <View style={[s.pendingCloudBanner, { borderColor: "#f59e0b55", backgroundColor: isDark ? "#451a0322" : "#fff7ed" }]}>
            <View style={{ flex: 1, minWidth: 0 }}>
              <Text style={[s.pendingCloudTitle, { color: isDark ? "#fbbf24" : "#92400e" }]}>
                {item.status === "stopped" ? "Remote dispatch expired" : "Needs your action"}
              </Text>
              <Text style={[s.pendingCloudText, { color: isDark ? "#fcd34d" : "#9a3412" }]} numberOfLines={3}>
                {blockedReason || "This task is waiting for the selected remote machine."}
                {expiresInHours !== null && item.status !== "stopped" ? ` Expires in ~${expiresInHours}h.` : ""}
              </Text>
            </View>
            {blockedActionLabel && item.status !== "stopped" ? (
              <Pressable
                hitSlop={8}
                onPress={(event) => {
                  event.stopPropagation();
                  onBlockedAction?.(item);
                }}
                style={({ pressed }) => [
                  s.pendingCloudButton,
                  { borderColor: "#f59e0b77", backgroundColor: isDark ? "#78350f" : "#ffedd5", opacity: pressed ? 0.75 : 1 },
                ]}
                accessibilityRole="button"
                accessibilityLabel={blockedActionLabel}
              >
                <Text style={[s.pendingCloudButtonText, { color: isDark ? "#fffbeb" : "#7c2d12" }]} numberOfLines={1}>
                  {blockedActionLabel}
                </Text>
              </Pressable>
            ) : null}
          </View>
        ) : null}
        {isRunning ? (
          <View style={s.taskPhaseRow}>
            <PhaseChip task={item} />
          </View>
        ) : null}
        {previewText ? (
          <Text style={[s.taskOutputPreview, { color: c.textSecondary }]} numberOfLines={2}>
            {previewText}
            {previewText.length >= 120 ? "..." : ""}
          </Text>
        ) : null}
        <View style={s.taskFooter}>
          <Text style={[s.taskTimestamp, { color: c.textMuted }]}>{formatRelativeTime(item.updatedAt)}</Text>
          {item.autoRetry && item.autoRetryCount != null && item.autoRetryCount > 0 ? (
            <Text style={[s.taskFooterMeta, { color: "#f97316" }]}>{`retry ${item.autoRetryCount}/${item.autoRetryMax}`}</Text>
          ) : null}
        </View>
      </TouchableOpacity>
    </Animated.View>
  );
}

// Only re-render a card when its OWN task object changes. The streaming
// updates rebuild just the task they touch (setTasks(prev.map(...))), so
// every other card keeps its identity and is skipped entirely. Without
// this, one running task re-rendered every card in the list on every
// output chunk. The callbacks are intentionally excluded from the
// comparison: renderItem rebuilds them on each parent render, and they
// only close over the item (compared here) and stable state setters.
const TaskCard = React.memo(TaskCardInner, (prev, next) =>
  prev.item === next.item &&
  prev.selectionMode === next.selectionMode &&
  prev.selected === next.selected,
);

// ── Helpers ──────────────────────────────────────────────────────────

// Extract a usable error message from a failed task. Tasks don't
// have a structured error field — failures land in resultText (final
// summary the runner emitted) or the tail of the output stream. Pick
// the most informative thing we can find. ANSI is stripped because
// codex/opencode tend to colour stderr.
function extractTaskErrorMessage(task: Task): string {
  const structured = task.failure;
  if (structured?.title || structured?.reason || structured?.remedy) {
    return [
      structured.title,
      structured.reason,
      structured.remedy,
    ].map((v) => String(v || "").trim()).filter(Boolean).join("\n");
  }
  const stripAnsi = (s: string) => s.replace(/\x1B\[[0-9;]*[A-Za-z]/g, "");
  const result = task.resultText ? stripAnsi(task.resultText).trim() : "";
  if (result) return result;
  const out = (task.output || []).map(stripAnsi).map((l) => l.trim()).filter(Boolean);
  if (out.length === 0) return "Task failed without a clear reason.";
  // Keep the last ~6 lines so the user sees the immediate failure
  // context rather than just the final cryptic line.
  return out.slice(-6).join("\n");
}

// A recoverable tmux task stays in Review after a failed turn so its session
// cannot disappear from Active. The structured failure is therefore the
// authoritative failure signal; status alone is only the lifecycle bucket.
function taskHasUnresolvedFailure(task: Task): boolean {
  return task.status === "failed" || Boolean(task.failure);
}

// Build the rows shown in the AgentContextPanel below the chat. All
// fields are best-effort — we render whatever we have access to from
// the local state. Branch and full workDir aren't on the Task type
// today, so they're sourced from the screen's projectDir param when
// present. Runner / Model mirror the TaskHeader chip — same fallback
// chain so e.g. opencode tasks surface "glm-4.7" in both places.
interface AgentContextExtras {
  /** The model the RUNNER on that box is actually configured to use, read from
   *  its own config via GET /runner/opencode/config. Authoritative — this is
   *  what will execute, not what the app guessed. The endpoint already existed
   *  and quic.ts already called it; the panel simply never asked. */
  runnerConfiguredModel?: string | null;
  /** Currently picked model id from the in-screen picker. */
  selectedModelId?: string;
  /** Active device descriptor (full object, not just name) for the
   *  preferredDefaultModelForRunner fallback when Task lacks model. */
  activeDevice?: { id?: string; name?: string | null; hostName?: string | null; os?: string | null };
  /** Signed-in user email — feeds the kivanc-account fallback inside
   *  preferredDefaultModelForRunner. Honest pass-through: any user. */
  userEmail?: string | null;
  /** Per-device mode preference map (opencode build/plan, etc). */
  modeByDevice?: Record<string, string>;
  /** Per-device provider preference map (opencode provider routing). */
  providerByDevice?: Record<string, string>;
}

function buildAgentContextRows(
  task: Task,
  deviceName: string | undefined,
  connMode: ConnectionMode,
  models: ModelInfo[],
  extras: AgentContextExtras = {},
): AgentContextRow[] {
  const rows: AgentContextRow[] = [];
  const elapsedSec = Math.max(0, Math.round((Date.now() - task.createdAt) / 1000));
  const elapsedLabel = elapsedSec < 60
    ? `${elapsedSec}s`
    : elapsedSec < 3600
      ? `${Math.floor(elapsedSec / 60)}m ${elapsedSec % 60}s`
      : `${Math.floor(elapsedSec / 3600)}h ${Math.floor((elapsedSec % 3600) / 60)}m`;

  if (deviceName) {
    rows.push({ label: "Device", value: deviceName.replace(/\.local$/, ""), mono: false });
  }
  if (task.runnerId) {
    rows.push({
      label: "Runner",
      value: displayRunnerLabel(task.runnerId),
      mono: false,
    });

    // This is deliberately NOT the phone-to-agent connection below. A task
    // can reach its box over Relay while the runner itself used ACP, or reach
    // it directly while the runner fell back to CLI / PTY. Surface both facts
    // so a user can audit actual ACP use instead of inferring it from a green
    // connection badge.
    const runnerProtocol = taskRunnerTransportLabel(task.transport);
    if (runnerProtocol) {
      rows.push({ label: "Runner protocol", value: runnerProtocol, mono: false });
    }
    const protocolReason = String(task.transportReason || "").trim();
    if (protocolReason) {
      rows.push({ label: "Protocol detail", value: protocolReason, mono: false });
    }

    // Model: prefer the task's own `model` field (set by the agent at
    // task creation, plumbed via Task.model). Falls back to the
    // picker's selectedModelId only when the task doesn't carry one,
    // then to the runner's per-device default. Picker fallback is
    // wrong for cross-device tasks — was the source of "Claude Code
    // · GPT-5.4" mislabels users kept reporting.
    let modelLabel: string | undefined;
    const taskModelId = (task as any)?.model as string | undefined;
    if (taskModelId) {
      modelLabel = models.find((m) => m.id === taskModelId)?.name || taskModelId;
    }
    // What the box is really configured to run beats anything inferred here.
    // The mini reads `zai-coding-plan/glm-5.2` while this panel claimed
    // "Sonnet"; the real value was one already-implemented call away.
    if (!modelLabel && extras.runnerConfiguredModel) {
      modelLabel = extras.runnerConfiguredModel;
    }
    if (!modelLabel && extras.selectedModelId && isModelCompatibleWithRunnerId(extras.selectedModelId, task.runnerId)) {
      modelLabel = models.find((m) => m.id === extras.selectedModelId)?.name || extras.selectedModelId;
    }
    // NO FALLBACK GUESS HERE. This panel is titled "Agent context" and its only
    // job is to say what is ACTUALLY running; a per-runner default dressed as
    // fact is worse than an absent row.
    //
    // Measured 2026-07-26: the panel showed MODEL "Sonnet" for an OpenCode task
    // on the Mac mini, where ~/.config/opencode/opencode.json reads
    // `model: zai-coding-plan/glm-5.2`, provider `zai`. No --model flag was
    // passed, so OpenCode used its config and NOTHING in the chain ever said
    // Sonnet — preferredDefaultModelForRunner() invented it from the runner id.
    // The user spotted it immediately ("we don't use sonnet with opencode at
    // all"), which is the point: a fabricated field is only harmless until
    // somebody trusts it, and this one sits next to DEVICE, TRANSPORT and TASK
    // ID, which are all real.
    //
    // Same fabrication removed from the task header earlier the same day; this
    // was the second surface reading the same guess.
    if (modelLabel) {
      rows.push({ label: "Model", value: modelLabel, mono: false });
    }

    // Mode + provider are OpenCode routing metadata, not generic runner
    // metadata. The device keeps a provider preference (for example
    // DeepSeek) even when the user switches the runner chip to Codex or
    // Claude. Rendering that per-device preference here made a Codex task
    // claim it was using DeepSeek (the exact false signal reported on
    // 2026-08-23). A task's runner is authoritative; only OpenCode has a
    // provider/model route to show in this panel.
    const deviceId = extras.activeDevice?.id;
    if (deviceId && normalizeTaskRunnerId(task.runnerId) === "opencode") {
      const mode = extras.modeByDevice?.[deviceId];
      if (mode) rows.push({ label: "Mode", value: mode, mono: false });
      const provider = extras.providerByDevice?.[deviceId];
      if (provider) rows.push({ label: "Provider", value: provider, mono: false });
    }
  }
  const hostKind = task.hostKind || task.executionSession?.hostKind;
  if (hostKind) {
    rows.push({
      label: "Hosted by",
      value: hostKind === "terminal_tmux"
        ? "Terminal · tmux"
        : hostKind === "desktop_gui"
          ? "Desktop app"
          : "Runner process",
      mono: false,
    });
  }
  const yaverSession = task.executionSession;
  if (yaverSession?.yaverSessionId) {
    rows.push({ label: "Yaver session", value: yaverSession.yaverSessionId, mono: true });
    rows.push({
      label: "Session route",
      value: [
        yaverSession.remoteBoxId && `box ${yaverSession.remoteBoxId}`,
        yaverSession.runnerName || yaverSession.runnerId,
        yaverSession.startedFrom && `started in ${yaverSession.startedFrom}`,
        yaverSession.initialSurface && `${yaverSession.initialSurface} → ${yaverSession.lastSurface || yaverSession.initialSurface}`,
      ].filter(Boolean).join(" · "),
      mono: true,
    });
    rows.push({
      label: "Session activity",
      value: [
        yaverSession.sessionStartedAt && `started ${new Date(yaverSession.sessionStartedAt).toLocaleString()}`,
        yaverSession.lastActiveAt && `active ${new Date(yaverSession.lastActiveAt).toLocaleString()}`,
        yaverSession.firstUserMessageAt && `first message ${new Date(yaverSession.firstUserMessageAt).toLocaleString()}`,
        yaverSession.lastAgentResponseAt && `last response ${new Date(yaverSession.lastAgentResponseAt).toLocaleString()}`,
      ].filter(Boolean).join(" · "),
      mono: false,
    });
  }
  const runnerSessionId = task.executionSession?.runnerSessionId || task.sessionId;
  if (runnerSessionId) {
    rows.push({
      label: "Runner session",
      value: `${runnerSessionId}${task.executionSession?.resumable === false ? " · not resumable" : ""}`,
      mono: true,
    });
  }
  if (task.tmuxSession || task.tmuxSessionId || task.executionSession?.tmuxSession) {
    const tmux = task.executionSession;
    rows.push({
      label: "Yaver session",
      value: [
        tmux?.tmuxSession || task.tmuxSession,
        tmux?.tmuxSessionId || task.tmuxSessionId,
        (tmux?.tmuxWindowName || task.tmuxWindowName) && `window ${tmux?.tmuxWindowName || task.tmuxWindowName}`,
        (tmux?.tmuxPaneId || task.tmuxPaneId) && `pane ${tmux?.tmuxPaneId || task.tmuxPaneId}`,
      ].filter(Boolean).join(" · "),
      mono: true,
    });
  }
  if (connMode) {
    rows.push({ label: "Transport", value: connMode, mono: false });
  }
  rows.push({
    label: task.status === "failed" || task.status === "review" || task.status === "completed" || task.status === "stopped"
      ? "Ran for"
      : "Running for",
    value: elapsedLabel,
    mono: false,
  });
  if (task.id) {
    rows.push({ label: "Task ID", value: task.id, mono: true });
  }
  return rows;
}

function formatRelativeTime(ts: number): string {
  const diff = Date.now() - ts;
  if (diff < 60_000) return "just now";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return `${Math.floor(diff / 86_400_000)}d ago`;
}

/** Build chat messages from task turns + live streaming output. */
function buildChatMessages(task: Task): { role: string; content: string }[] {
  const messages: { role: string; content: string }[] = [];
  const pushMessage = (role: string, content: string) => {
    const normalizedContent = String(content ?? "");
    if (!normalizedContent.trim()) return;
    const last = messages[messages.length - 1];
    if (
      last &&
      last.role === role &&
      stripAnsi(last.content).trim() === stripAnsi(normalizedContent).trim()
    ) {
      return;
    }
    messages.push({ role, content: normalizedContent });
  };

  const projectedTurns = firstClassTaskConversationTurns(task.turns, task.presentation);
  if (!projectedTurns.some((turn) => turn.role === "user")) {
    pushMessage("user", normalizeTaskTitle(task.title));
  }
  for (const turn of projectedTurns) {
    pushMessage(turn.role, turn.content);
  }
  if (Array.isArray(task.pendingFollowUps)) {
    for (const followUp of task.pendingFollowUps) {
      pushMessage("user", String(followUp?.input ?? ""));
    }
  }

  return messages;
}

function TaskPresentationSummary({ task }: { task: Task }) {
  const c = useColors();
  const message = friendlyTaskPresentation(task.presentation)
    .filter((item) => item.kind !== "message" && item.text.trim())
    .at(-1);
  if (!message) return null;
  const attention = message.kind === "error" || message.kind === "warning" || message.kind === "action_required";
  const meta = [message.machine, message.platform, message.runner, message.project].filter(Boolean).join(" · ");
  return (
    <View style={{ marginTop: 10, borderWidth: 1, borderColor: attention ? (c.warn || "#f59e0b") : c.border, borderRadius: 12, backgroundColor: c.bgCard, padding: 12 }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <Ionicons name={attention ? "alert-circle-outline" : "pulse-outline"} size={18} color={attention ? (c.warn || "#f59e0b") : c.accent} />
        <Text style={{ flex: 1, color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>{message.text}</Text>
      </View>
      {meta ? <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 5 }} numberOfLines={2}>{meta}</Text> : null}
    </View>
  );
}

// ── Live console (raw lane) ─────────────────────────────────────────
// EVERY runner (opencode, codex, claude, …) streams its RAW runner
// stdout (ANSI + TUI intact) as `raw`/`raw_replay` SSE frames — see
// agent tasks.go emitRaw, which runs before any per-runner grooming.
// The primary lane renders only the agent-owned semantic messages. A foldable
// Live console renders the exact raw terminal tail via shared AnsiConsoleText
// (including green/red diffs) without asking the app to classify stdout. Raw
// evidence is folded by default, keeping readable activity and the final
// answer as the primary mobile experience.
const RAW_CONSOLE_RENDER_MS = 500;
// The agent retains a larger raw ring for replay. The phone only tokenizes the
// newest console tail on an explicit Details expansion, preserving coloured
// diffs without turning a long build into a JS-memory or render-time spike.
const MOBILE_CONSOLE_RENDER_CAP = 64 * 1024;

const LiveConsoleSection = React.memo(function LiveConsoleSection({
  status,
  rawText,
  live,
}: {
  status: TaskStatus;
  rawText: string;
  live: boolean;
}) {
  const c = useColors();
  const isRunning = status === "running" || status === "queued";
  const [expanded, setExpanded] = useState(false);
  const collapsedPreview = useMemo(
    () => expanded ? "" : buildTaskConsolePreview(rawText, isRunning),
    [expanded, isRunning, rawText],
  );
  const consoleText = useMemo(
    () => {
      if (!expanded) return "";
      if (rawText.length <= MOBILE_CONSOLE_RENDER_CAP) return rawText;
      return `… earlier terminal output is retained on the agent …\n${rawText.slice(-MOBILE_CONSOLE_RENDER_CAP)}`;
    },
    [expanded, rawText],
  );
  if (!rawText) return null;

  return (
    <View style={[s.liveConsoleWrap, { borderColor: c.border }]}>
      <Pressable
        onPress={() => setExpanded((v) => !v)}
        style={({ pressed }) => [
          s.liveConsoleToggle,
          { backgroundColor: c.surface },
          pressed && { opacity: 0.7 },
        ]}
        accessibilityRole="button"
        accessibilityLabel={expanded ? "Hide live console" : "Show live console"}
        accessibilityState={{ expanded }}
      >
        <View style={s.liveConsoleHeaderRow}>
          <Text style={[s.liveConsoleCaret, { color: c.textMuted }]}>
            {expanded ? "▼" : "▶"}
          </Text>
          <Text style={[s.liveConsoleTitle, { color: c.textSecondary }]}>
            Live console
          </Text>
          {live ? (
            <Text style={[s.liveConsoleDot, { color: "#4ade80" }]}>● live</Text>
          ) : (
            <Text style={[s.liveConsoleDot, { color: c.textTertiary }]}>○ idle</Text>
          )}
          <Text style={[s.liveConsoleCount, { color: c.textTertiary }]} numberOfLines={1}>
            {rawText.length > 1024
              ? `${Math.round(rawText.length / 1024)} KB`
              : rawText.length > 0
                ? `${rawText.length} B`
                : ""}
          </Text>
        </View>
        {!expanded && collapsedPreview ? (
          <Text
            style={[s.liveConsolePreview, { color: c.textMuted }]}
            numberOfLines={3}
          >
            {collapsedPreview}
          </Text>
        ) : null}
      </Pressable>
      {expanded && consoleText ? (
        <ScrollView
          style={[s.liveConsoleBody, { backgroundColor: c.bgCard, borderTopColor: c.border }]}
          nestedScrollEnabled
          showsVerticalScrollIndicator
        >
          <AnsiConsoleText text={consoleText} fontSize={11} />
        </ScrollView>
      ) : null}
    </View>
  );
});

function TaskDetailsSection({ children, projectSummary }: { children: React.ReactNode; projectSummary: string }) {
  const c = useColors();
  const [expanded, setExpanded] = useState(false);
  return (
    <View style={{ marginTop: 10 }}>
      <Pressable
        onPress={() => setExpanded((value) => !value)}
        accessibilityRole="button"
        accessibilityLabel={expanded ? "Hide runner details" : "Show runner details"}
        accessibilityState={{ expanded }}
        style={({ pressed }) => ({
          minHeight: 44,
          paddingHorizontal: 12,
          borderRadius: 12,
          borderWidth: 1,
          borderColor: c.border,
          backgroundColor: c.bgCard,
          flexDirection: "row",
          alignItems: "center",
          gap: 9,
          opacity: pressed ? 0.72 : 1,
        })}
      >
        <Ionicons name="terminal-outline" size={17} color={c.textMuted} />
        <View style={{ flex: 1, minWidth: 0 }}>
          <Text style={{ color: c.textSecondary, fontSize: 13, fontWeight: "700" }}>Runner details</Text>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 1 }} numberOfLines={1}>{projectSummary}</Text>
        </View>
        <Ionicons name={expanded ? "chevron-up" : "chevron-down"} size={16} color={c.textMuted} />
      </Pressable>
      {expanded ? <View style={{ marginTop: 8 }}>{children}</View> : null}
    </View>
  );
}

// ── Main screen ──────────────────────────────────────────────────────
/**
 * LogsPanelContent — the "Logs" sheet body, shared by two hosts:
 *
 *  1. the top-level native <Modal> (list screen, no task detail open), and
 *  2. an absolute overlay INSIDE the task-detail <Modal>.
 *
 * It MUST be a component and not a second native Modal for case 2: iOS
 * cannot present a second native <Modal> while another is on screen — the
 * newcomer mounts invisibly behind it, so tapping Logs from the chat did
 * nothing (2026-08-08). The chat path renders this panel over the chat
 * modal instead.
 */
function LogsPanelContent({
  c,
  selectedTask,
  taskLogLines,
  combinedLogText,
  logs,
  onClose,
}: {
  c: ThemeColors;
  selectedTask: Task | null;
  taskLogLines: string[];
  combinedLogText: string;
  logs: LogEntry[];
  onClose: () => void;
}) {
  return (
    <View style={s.logsModalOverlay}>
      <Pressable style={{ height: 80 }} onPress={onClose} />
      <View style={[s.logsModal, { backgroundColor: c.bg }]}>
        <View style={[s.logsHeader, { borderBottomColor: c.border }]}>
          <Text style={[s.logsTitle, { color: c.textPrimary }]}>{selectedTask ? "Live Logs" : "Connection Logs"}</Text>
          <View style={s.logsHeaderActions}>
            <Pressable onPress={() => {
              // Full paste-ready trace (2026-08-09) — same shape web copies.
              const trace = assembleTrace({
                surface: "mobile",
                task: selectedTask
                  ? {
                      id: selectedTask.id,
                      status: selectedTask.status,
                      runner: selectedTask.runnerId,
                      title: selectedTask.title,
                    }
                  : undefined,
                error: selectedTask ? extractTaskErrorMessage(selectedTask) : undefined,
                logTail: combinedLogText || "No logs yet.",
              });
              ExpoClipboard.setStringAsync(trace || "No logs yet.");
              Alert.alert("Copied", "Trace copied to clipboard.");
            }}>
              <Text style={[s.logsActionText, { color: c.accent }]}>Copy</Text>
            </Pressable>
            <Pressable onPress={onClose} style={{ marginLeft: 16 }}>
              <Text style={[s.logsActionText, { color: c.textMuted }]}>Close</Text>
            </Pressable>
          </View>
        </View>
        <ScrollView style={s.logsScroll} contentContainerStyle={s.logsScrollContent}>
          {selectedTask ? (
            <>
              <Text style={[s.logsSectionTitle, { color: c.textPrimary }]}>
                {normalizeTaskTitle(selectedTask.title)} · {selectedTask.status}
              </Text>
              {taskLogLines.length === 0 ? (
                <Text style={[s.logsEmpty, { color: c.textMuted }]}>No task output yet.</Text>
              ) : (
                taskLogLines.map((line, i) => (
                  <Text key={`task-${i}`} style={[s.logLine, { color: c.textPrimary }]}>
                    {line}
                  </Text>
                ))
              )}
              <View style={[s.logsSectionDivider, { backgroundColor: c.border }]} />
              <Text style={[s.logsSectionTitle, { color: c.textPrimary }]}>Connection</Text>
            </>
          ) : null}
          {logs.length === 0 ? (
            <Text style={[s.logsEmpty, { color: c.textMuted }]}>No logs yet.</Text>
          ) : (
            logs.slice().reverse().map((entry, i) => (
              <Text key={i} style={[s.logLine, {
                color: entry.level === "error" ? "#ef4444" : entry.level === "warn" ? "#eab308" : c.textSecondary,
              }]}>
                {new Date(entry.timestamp).toLocaleTimeString()} {entry.message}
              </Text>
            ))
          )}
        </ScrollView>
      </View>
    </View>
  );
}

function RunnerControlPanelContent({
  c,
  mode,
  catalog,
  step,
  selectedModel,
  selectedEffort,
  busy,
  error,
  onClose,
  onSelectModel,
  onSelectEffort,
  onBackToModels,
  onApplyModel,
  onConfirmExit,
}: {
  c: ThemeColors;
  mode: "model" | "exit";
  catalog: TaskRunnerControlCatalog | null;
  step: "models" | "effort";
  selectedModel: string;
  selectedEffort: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra";
  busy: boolean;
  error: string;
  onClose: () => void;
  onSelectModel: (model: ModelInfo) => void;
  onSelectEffort: (effort: "none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra") => void;
  onBackToModels: () => void;
  onApplyModel: () => void;
  onConfirmExit: () => void;
}) {
  const selected = catalog?.models.find((model) => model.id === selectedModel);
  const efforts: NonNullable<ModelInfo["supportedReasoningEfforts"]> = selected?.supportedReasoningEfforts ?? [];
  return (
    <View style={[StyleSheet.absoluteFillObject, { zIndex: 80, backgroundColor: "rgba(0,0,0,.64)", justifyContent: "flex-end" }]}>
      <Pressable style={{ flex: 1 }} onPress={onClose} accessibilityLabel="Close runner control" />
      <View style={{ maxHeight: "82%", backgroundColor: c.bgCard, borderTopLeftRadius: 24, borderTopRightRadius: 24, padding: 18, paddingBottom: 34 }}>
        <View style={{ flexDirection: "row", alignItems: "center", marginBottom: 6 }}>
          {mode === "model" && step === "effort" ? <Pressable onPress={onBackToModels} hitSlop={10}><Ionicons name="chevron-back" size={22} color={c.textPrimary} /></Pressable> : null}
          <View style={{ flex: 1 }}>
            <Text style={{ color: c.textPrimary, fontSize: 19, fontWeight: "800" }}>
              {mode === "exit" ? "Exit this session?" : step === "effort" ? selected?.name || selectedModel : "Choose a model"}
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 3 }}>
              {mode === "exit"
                ? "Yaver will ask the runner to exit, verify it stopped, and keep this conversation in history."
                : catalog ? `${catalog.runnerId} · live catalog from ${catalog.modelSource || "this machine"}` : "Loading the runner on this task's machine…"}
            </Text>
          </View>
          <Pressable onPress={onClose} hitSlop={10}><Ionicons name="close" size={23} color={c.textMuted} /></Pressable>
        </View>

        {busy && !catalog ? <ActivityIndicator color={c.accent} style={{ marginVertical: 28 }} /> : null}
        {error ? <Text style={{ color: c.error, fontSize: 13, marginVertical: 10 }}>{error}</Text> : null}

        {mode === "model" && catalog?.isAdopted ? (
          <View style={{ borderWidth: 1, borderColor: "#f59e0b", borderRadius: 12, padding: 10, marginVertical: 8 }}>
            <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>Live terminal session</Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 3 }}>Models are shown from the real runner, but Yaver will not guess at terminal menu positions. Change this adopted session from Runner details.</Text>
          </View>
        ) : null}

        {mode === "model" && catalog && step === "models" ? (
          <ScrollView style={{ marginTop: 8 }} contentContainerStyle={{ gap: 8 }}>
            {catalog.models.map((model) => {
              const active = model.id === selectedModel;
              return (
                <Pressable
                  key={model.id}
                  onPress={() => onSelectModel(model)}
                  accessibilityRole="radio"
                  accessibilityState={{ selected: active }}
                  style={{ minHeight: 52, borderWidth: 1, borderColor: active ? c.accent : c.border, backgroundColor: active ? c.accent + "18" : c.bgCardElevated, borderRadius: 13, paddingHorizontal: 13, paddingVertical: 9, flexDirection: "row", alignItems: "center" }}
                >
                  <View style={{ flex: 1 }}><Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }}>{model.name || model.id}</Text><Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>{model.id}{model.isDefault ? " · Recommended" : ""}</Text></View>
                  {active ? <Ionicons name="checkmark-circle" size={20} color={c.accent} /> : <Ionicons name="chevron-forward" size={17} color={c.textMuted} />}
                </Pressable>
              );
            })}
          </ScrollView>
        ) : null}

        {mode === "model" && catalog && step === "effort" ? (
          <View style={{ gap: 8, marginTop: 12 }}>
            <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "800", letterSpacing: 1 }}>REASONING LEVEL</Text>
            {efforts.map((option) => {
              const effort = option.reasoningEffort;
              const active = effort === selectedEffort;
              return <Pressable key={effort} onPress={() => onSelectEffort(effort)} style={{ minHeight: 50, borderWidth: 1, borderColor: active ? c.accent : c.border, backgroundColor: active ? c.accent + "18" : c.bgCardElevated, borderRadius: 13, padding: 11, flexDirection: "row", alignItems: "center" }}><View style={{ flex: 1 }}><Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }}>{effort === "xhigh" ? "Extra high" : effort === "max" ? "More reasoning" : effort[0].toUpperCase() + effort.slice(1)}</Text>{option.description ? <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>{option.description}</Text> : null}</View>{active ? <Ionicons name="checkmark-circle" size={20} color={c.accent} /> : null}</Pressable>;
            })}
            <Pressable disabled={busy || !!catalog.isAdopted} onPress={onApplyModel} style={{ marginTop: 6, minHeight: 48, borderRadius: 13, backgroundColor: c.accent, alignItems: "center", justifyContent: "center", opacity: busy || catalog.isAdopted ? .45 : 1 }}><Text style={{ color: "#fff", fontSize: 14, fontWeight: "800" }}>{busy ? "Applying…" : `Use ${selectedModel} · ${selectedEffort}`}</Text></Pressable>
          </View>
        ) : null}

        {mode === "model" && catalog && step === "models" && catalog.runnerId !== "codex" ? (
          <Pressable disabled={busy || !selectedModel || !!catalog.isAdopted} onPress={onApplyModel} style={{ marginTop: 12, minHeight: 48, borderRadius: 13, backgroundColor: c.accent, alignItems: "center", justifyContent: "center", opacity: busy || !selectedModel || catalog.isAdopted ? .45 : 1 }}><Text style={{ color: "#fff", fontSize: 14, fontWeight: "800" }}>{busy ? "Applying…" : `Use ${selected?.name || selectedModel}`}</Text></Pressable>
        ) : null}

        {mode === "exit" ? (
          <View style={{ flexDirection: "row", gap: 10, marginTop: 18 }}><Pressable disabled={busy} onPress={onClose} style={{ flex: 1, minHeight: 48, borderRadius: 13, borderWidth: 1, borderColor: c.border, alignItems: "center", justifyContent: "center" }}><Text style={{ color: c.textPrimary, fontWeight: "700" }}>Keep working</Text></Pressable><Pressable disabled={busy} onPress={onConfirmExit} style={{ flex: 1, minHeight: 48, borderRadius: 13, backgroundColor: c.error, alignItems: "center", justifyContent: "center", opacity: busy ? .55 : 1 }}><Text style={{ color: "#fff", fontWeight: "800" }}>{busy ? "Exiting…" : "Exit session"}</Text></Pressable></View>
        ) : null}
      </View>
    </View>
  );
}

export default function TasksScreen() {
  const c = useColors();
  const { isDark } = useTheme();
  const dogfoodRuntime = useDogfoodOverlay();
  const insets = useSafeAreaInsets();
  const taskRouter = useRouter();
  const layout = useResponsiveLayout();
  // Browser Dogfood is already the Yaver dev server rendered inside the
  // native host. Showing that same server as a DevPreview card here creates a
  // confusing Yaver-inside-Yaver control that can stop its own host surface.
  const attachedDogfoodRuntime = dogfoodRuntime.active ||
    (Platform.OS === "web" && isAttachedDogfoodWebRuntime());
  // Follow-up composer height cap. RN 0.81.5's Modal cannot compile
  // softwareKeyboardLayoutMode (see the note above the task-detail Modal),
  // so on Android behavior="height" shrank the sheet until the Send button
  // hid behind the keyboard. A bounded, scrollable sheet keeps the bottom
  // (Send) row reachable on every screen/keyboard and leaves part of the
  // streaming console visible while typing a follow-up.
  const winHeight = Dimensions.get("window").height;
  const followUpComposerMaxHeight = Math.round(winHeight * 0.62);
  // "wide" (960pt) over "regular" (720pt) on tablet. The DevPreview
  // serving banner + filter chip row + task list all read better at
  // wider clamp on a tablet — at 720pt the chips wrapped to 2 lines
  // and the serving CTA dominated. Phones unaffected — hook returns
  // {} when layoutClass === "phone".
  const tabletContent = useTabletContentStyle("wide");
  // Tablet landscape: render task detail as a persistent right-pane
  // panel instead of a slide-up sheet, so the task list stays
  // visible on the left. The Modal is still used (so keyboard +
  // focus management work) but its overlay/positioning are
  // overridden inline.
  const tabletDualPane = layout.layoutClass === "tablet-landscape";
  // Optional `?dir=/abs/path` scopes chat/tasks to a project directory.
  // When present, we pass it as workDir on new tasks so the runner executes
  // inside the project instead of the agent's global cwd. Used by the
  // unified project screen's [Chat] button.
  const taskParams = useRouteParamsCompat<{
    dir?: string;
    prompt?: string;
    title?: string;
    runner?: string;
    openNew?: string;
    autoSubmit?: string;
    hideInitialPrompt?: string;
    selectProject?: string;
    phoneCheckout?: string;
    /** Native review-notification target. Kept separate from the compose route. */
    taskId?: string;
    taskDeviceId?: string;
    taskNotificationNonce?: string;
    sessionStartedFrom?: "tasks" | "vibing" | "new-application" | "mobile-workspace";
  }>();
  const routeProjectDir = typeof taskParams.dir === "string" ? taskParams.dir : "";
  const initialPrompt = typeof taskParams.prompt === "string" ? taskParams.prompt : "";
  const initialTitle = typeof taskParams.title === "string" ? taskParams.title : "";
  const initialRunner = typeof taskParams.runner === "string" ? taskParams.runner : "";
  const initialPhoneCheckout = typeof taskParams.phoneCheckout === "string" ? taskParams.phoneCheckout : "";
  const notificationTaskId = typeof taskParams.taskId === "string" ? taskParams.taskId.trim() : "";
  const notificationTaskDeviceId = typeof taskParams.taskDeviceId === "string" ? taskParams.taskDeviceId.trim() : "";
  const notificationTaskNonce = typeof taskParams.taskNotificationNonce === "string" ? taskParams.taskNotificationNonce : "";
  const initialSessionStartedFrom = taskParams.sessionStartedFrom || "tasks";
  const shouldOpenNew =
    typeof taskParams.openNew === "string" &&
    (taskParams.openNew === "1" || taskParams.openNew === "true");
  const shouldAutoSubmit = taskParams.autoSubmit === "1" || taskParams.autoSubmit === "true";
  const shouldHideInitialPrompt = taskParams.hideInitialPrompt === "1" || taskParams.hideInitialPrompt === "true";
  const shouldSelectRouteProject = taskParams.selectProject === "1" || taskParams.selectProject === "true";
  const { connectionStatus, activeDevice, devices, userDisconnected, lastError, agentAuthExpired, recoverDeviceAuth, selectDevice, disconnect, isLoadingDevices, everHadDevices, refreshDevices, deviceListError, stopReconnectAndBounce, retryConnection, primaryDeviceId, secondaryDeviceId, codingMode, primaryRunnerByDevice, primaryModelByDevice, primaryReasoningEffortByDevice, primaryModeByDevice, primaryProviderByDevice, setPrimaryRunnerForDevice, multiTargetMode, connectedDeviceIds, machineRoles } = useDevice();
  // Use transport truth, not the optimistic focused-device status. This must
  // be declared before the route auto-submit effect below consumes it.
  const anyPoolConnected = connectedDeviceIds.length > 0;
  const activeLiveInPool = !!activeDevice && connectedDeviceIds.includes(activeDevice.id);
  const effectiveState: ConnectionState =
    activeLiveInPool ? "connected" :
    connectionStatus === "error" ? "connecting" :
    (!activeDevice && anyPoolConnected) ? "connected" :
    connectionStatus === "connected" ? "connecting" :
    connectionStatus;
  const isEffectivelyConnected = effectiveState === "connected";
  const [showLogs, setShowLogs] = useState(false);
  const [logs, setLogs] = useState<LogEntry[]>(getLogEntries());
  const [isRefreshingDevices, setIsRefreshingDevices] = useState(false);

  // Subscribe to log changes
  useEffect(() => {
    return onLogsChanged(() => setLogs(getLogEntries()));
  }, []);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [statusFilter, setStatusFilter] = useState<"running" | "review" | "completed" | "failed" | "all">("running");
  const [selectedTask, setSelectedTask] = useState<Task | null>(null);
  const [selectingTasks, setSelectingTasks] = useState(false);
  const [selectedBulkTaskKeys, setSelectedBulkTaskKeys] = useState<Set<string>>(() => new Set());
  const deviceForTask = useCallback((task?: Task | null) => {
    if (!task) return null;
    if (task.deviceId) {
      const byID = devices.find((d) => d.id === task.deviceId);
      if (byID) return byID;
    }
    const taskName = (task.deviceName || "").trim().replace(/\.local$/, "");
    if (!taskName) return null;
    return devices.find((d) => {
      const name = (d.name || "").trim().replace(/\.local$/, "");
      return name === taskName;
    }) || null;
  }, [devices]);
  const [showNewTask, setShowNewTask] = useState(false);
  // Task composition stays deliberately quiet. Project/MCP, runner and mode
  // controls are available behind the header ellipsis instead of competing
  // with the prompt and Send button when the keyboard is open.
  const [showTaskOptions, setShowTaskOptions] = useState(false);
  const [composerProjects, setComposerProjects] = useState<ComposerProject[]>([]);
  const [selectedProjectPath, setSelectedProjectPath] = useState<string>(routeProjectDir);
  const [phoneProjects, setPhoneProjects] = useState<PhoneProject[]>([]);
  const [selectedPhoneCheckout, setSelectedPhoneCheckout] = useState<string | null>(initialPhoneCheckout || null);
  // A blank project chosen by the user is intentional task context, not
  // "project discovery has not finished". Track it per runner device so the
  // restore effect cannot immediately overwrite No project with the old row.
  const explicitProjectChoiceRef = useRef<{ deviceId: string; path: string } | null>(null);
  const [availableMcpServers, setAvailableMcpServers] = useState<McpServer[]>([]);
  const [selectedMcpServers, setSelectedMcpServers] = useState<string[]>([]);
  // Yaver's own MCP doorway is task authority, so it starts OFF with every
  // other MCP unless the user selects it or enables Use latest.
  const [includeYaverMcp, setIncludeYaverMcp] = useState(false);
  const [showProjectPicker, setShowProjectPicker] = useState(false);
  const [projectPickerQuery, setProjectPickerQuery] = useState("");
  const [keepLastProject, setKeepLastProject] = useState(false);
  const [useLatestMCP, setUseLatestMCP] = useState(false);
  // Cross-machine surface catalogs (2026-08-13): which MCP servers / which
  // git projects live on which machine, from Convex userSettings
  // (mcpCatalogByDevice / runtimeProjectCatalogByDevice, seeded by each
  // agent's heartbeat). Lets the composer offer ANOTHER machine's MCPs as
  // selectable rows and browse other machines' repos — the mobile twin of
  // the web chat composer's cross-machine groups.
  const [mcpCatalogByDevice, setMcpCatalogByDevice] = useState<Record<string, Array<{ name: string; url: string; toolCount?: number }>>>({});
  const [projectCatalogByDevice, setProjectCatalogByDevice] = useState<Record<string, Array<{ projectName?: string | null; repoName?: string | null; gitRemote?: string | null; branch?: string | null }>>>({});
  // While a cross-machine pick is in flight, the MCP restore effect must not
  // overwrite the just-made selection with the previous device's Convex row.
  const suppressMcpRestoreRef = useRef(false);
  // Chat is prose-first, so correction defaults on. Users working with paths or
  // commands can disable it; the explicit choice persists per device.
  const [textCorrectionEnabled, setTextCorrectionEnabled] = useState(true);
  const selectedComposerProject = useMemo(
    () => composerProjects.find((project) => project.path === selectedProjectPath) || null,
    [composerProjects, selectedProjectPath],
  );
  const visibleComposerProjects = useMemo(
    () => visibleProjectPickerRows(composerProjects, selectedProjectPath, projectPickerQuery),
    [composerProjects, selectedProjectPath, projectPickerQuery],
  );
  const projectDir = selectedComposerProject?.path || selectedProjectPath || routeProjectDir;

  // Phone-local repositories are an explicit target. Loading their metadata is
  // advisory and must never block the remote project catalog or remote send.
  useEffect(() => {
    let cancelled = false;
    void listLocalPhoneProjectsMeta()
      .then(async (projects) => {
        const repos: PhoneProject[] = [];
        for (const project of projects) {
          try {
            if (await isRepo(gitContextForSlug(project.slug))) repos.push(project);
          } catch {
            // A metadata row without a usable local checkout is not selectable.
          }
        }
        if (!cancelled) {
          setPhoneProjects(repos);
          if (initialPhoneCheckout && repos.some((project) => project.slug === initialPhoneCheckout)) {
            setSelectedPhoneCheckout(initialPhoneCheckout);
            setSelectedProjectPath("");
          }
        }
      })
      .catch(() => {
        if (!cancelled) setPhoneProjects([]);
      });
    return () => { cancelled = true; };
  }, [initialPhoneCheckout]);

  const closeProjectPicker = useCallback(() => {
    Keyboard.dismiss();
    setProjectPickerQuery("");
    setShowProjectPicker(false);
  }, []);

  const openProjectPicker = useCallback(() => {
    // The task prompt auto-focuses, so the keyboard is normally still open
    // when this chip is tapped. A configuration sheet has no reason to inherit
    // that keyboard: it used to cover the bottom half of the project catalog
    // and left the selected project unreachable.
    Keyboard.dismiss();
    setProjectPickerQuery("");
    setShowProjectPicker(true);
  }, []);

  // ── Project / MCP picker sheet ───────────────────────────────────────
  // Rendered in THREE places by the same function so the three can never
  // drift: (a) as an absolute overlay INSIDE the New Task composer Modal,
  // (b) as an absolute overlay INSIDE the task-detail Modal (follow-up
  // composer), and (c) as a standalone Modal when neither is up. iOS cannot
  // present a second native Modal while another is on screen — the newcomer
  // mounts invisibly behind it, so tapping the project chip "did nothing"
  // (same class as the Logs sheet, 2026-08-08, and the Modal-handoff notes
  // in this file). Overlays inside the hosting Modal are the only form that
  // actually appears. (2026-08-09)
  // Cross-machine rows for the composer sheet (2026-08-13): every OTHER
  // owned device's MCP servers + repos from the Convex surface catalogs,
  // with the machine label each row carries. Picking one switches the task
  // to that machine via selectDevice — an MCP attaches by name on the task
  // machine, and repo paths are machine-local, so a selection on a machine
  // we are not about to run on would be a silent no-op.
  const remoteMcpRows = useMemo(() => {
    const rows: Array<{ device: Device; label: string; server: { name: string; url: string; toolCount?: number } }> = [];
    for (const d of devices || []) {
      if (d.id === activeDevice?.id) continue;
      for (const s of mcpCatalogByDevice[d.id] || []) {
        rows.push({ device: d, label: (d.name || d.id || "other machine").trim(), server: s });
      }
    }
    return rows.sort((a, b) => (a.label + a.server.name).localeCompare(b.label + b.server.name));
  }, [devices, activeDevice?.id, mcpCatalogByDevice]);

  const remoteProjectRows = useMemo(() => {
    const rows: Array<{ device: Device; label: string; name: string; gitRemote?: string }> = [];
    for (const d of devices || []) {
      if (d.id === activeDevice?.id) continue;
      for (const p of projectCatalogByDevice[d.id] || []) {
        rows.push({
          device: d,
          label: (d.name || d.id || "other machine").trim(),
          name: String(p.projectName || p.repoName || "Unnamed project"),
          gitRemote: p.gitRemote || undefined,
        });
      }
    }
    return rows.sort((a, b) => (a.label + a.name).localeCompare(b.label + b.name));
  }, [devices, activeDevice?.id, projectCatalogByDevice]);

  const renderProjectPickerSheet = () => (
    <KeyboardAvoidingView
      style={[s.modalOverlay, { justifyContent: "flex-end" }]}
      behavior={Platform.OS === "ios" ? "padding" : "height"}
    >
      <Pressable style={StyleSheet.absoluteFillObject} onPress={closeProjectPicker} />
      <View
        style={[
          s.agentPickerSheet,
          {
            backgroundColor: c.bgCard,
            maxHeight: "88%",
            paddingBottom: 0,
            overflow: "hidden",
          },
        ]}
        accessibilityViewIsModal
        testID="task-configuration-sheet"
      >
        <View
          style={[s.agentPickerHeader, { borderBottomColor: c.border }]}
        >
          <Text style={[s.agentPickerTitle, { color: c.textPrimary }]}>Task configuration</Text>
          <Pressable
            onPress={closeProjectPicker}
            accessibilityRole="button"
            accessibilityLabel="Close task configuration"
            testID="task-configuration-done"
          >
            <Text style={{ color: c.accent, fontSize: 16, fontWeight: "600" }}>Done</Text>
          </Pressable>
        </View>
        <ScrollView
          style={{ flexShrink: 1 }}
          contentContainerStyle={{
            paddingHorizontal: 16,
            paddingTop: 12,
            paddingBottom: Math.max(insets.bottom + 12, 28),
          }}
          keyboardShouldPersistTaps="handled"
          keyboardDismissMode="interactive"
          showsVerticalScrollIndicator
          testID="task-configuration-scroll"
        >
          <View style={s.keepLastRow}>
            <View style={{ flex: 1 }}>
              <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>Keep last project</Text>
              <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>Auto-select this runner box's last project.</Text>
            </View>
            <Switch
              value={keepLastProject}
              onValueChange={(value) => {
                setKeepLastProject(value);
                void saveKeepLastProjectEnabled(value);
                if (!value) {
                  setSelectedProjectPath("");
                  return;
                }
                const runnerDeviceId = connectionManager.roleDeviceId("runner") || activeDevice?.id || "default";
                void (async () => {
                  const last = (token ? await loadLastTaskProjectFromConvex(token, runnerDeviceId) : null)
                    ?? await loadLastTaskProject(runnerDeviceId);
                  if (!last) return;
                  const match = composerProjects.find((project) =>
                    (last.path && project.path === last.path) ||
                    project.name.toLowerCase() === last.name.toLowerCase() ||
                    projectNameFromPath(project.path)?.toLowerCase() === last.name.toLowerCase());
                  if (match) setSelectedProjectPath(match.path);
                })();
              }}
            />
          </View>
          <View style={s.keepLastRow}>
            <View style={{ flex: 1 }}>
              <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>Use latest MCP selection</Text>
              <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>Off means every task starts with No MCP, including Yaver MCP.</Text>
            </View>
            <Switch
              value={useLatestMCP}
              onValueChange={(value) => {
                setUseLatestMCP(value);
                void saveUseLatestMCPEnabled(value);
                if (!value) {
                  setSelectedMcpServers([]);
                  setIncludeYaverMcp(false);
                  return;
                }
                const runnerDeviceId = connectionManager.roleDeviceId("runner") || activeDevice?.id || "default";
                if (token) {
                  void loadMCPServersFromConvex(token, runnerDeviceId).then((pref) => {
                    if (!pref) return;
                    const known = new Set(availableMcpServers.map((server) => server.name));
                    setSelectedMcpServers((pref.mcpServers || []).filter((name) => known.has(name)));
                    setIncludeYaverMcp(pref.includeYaverMcp ?? false);
                  });
                }
              }}
            />
          </View>
          <View
            style={[s.projectSearchShell, { borderColor: c.border, backgroundColor: c.bg }]}
          >
            <Ionicons name="search" size={17} color={c.textMuted} />
            <TextInput
              value={projectPickerQuery}
              onChangeText={setProjectPickerQuery}
              placeholder="Search projects"
              placeholderTextColor={c.textMuted}
              style={[s.projectSearchInput, { color: c.textPrimary }]}
              autoCorrect={false}
              autoCapitalize="none"
              returnKeyType="search"
              testID="project-picker-search"
              accessibilityLabel="Search projects"
            />
            {projectPickerQuery ? (
              <Pressable
                onPress={() => setProjectPickerQuery("")}
                accessibilityRole="button"
                accessibilityLabel="Clear project search"
                hitSlop={8}
              >
                <Ionicons name="close-circle" size={19} color={c.textMuted} />
              </Pressable>
            ) : null}
          </View>
          <View>
            {ownerRemotelessEnabled && phoneProjects.length > 0 ? (
              <>
                <Text style={[s.agentPickerSection, { color: c.textMuted, marginLeft: 0, marginTop: 14 }]}>{codingMode === "local-only" ? "ON THIS PHONE" : "REMOTELESS FALLBACK"}</Text>
                <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 10 }}>
                  {codingMode === "local-only"
                    ? "You explicitly selected No remote box. DeepSeek can audit and edit a checkout here; builds, shells, tests, previews, and deploys remain unavailable."
                    : "Used only when no eligible primary or secondary runner is connected. Rendering, builds, shells, tests, and deploys remain unavailable here."}
                </Text>
                {phoneProjects.map((project) => {
                  const active = selectedPhoneCheckout === project.slug;
                  const remotePreferred = taskExecutionPlacement.lane === "remote";
                  return (
                    <Pressable
                      key={`phone:${project.slug}`}
                      onPress={() => {
                        if (remotePreferred) {
                          Alert.alert(
                            "Remote runner preferred",
                            `${taskExecutionPlacement.target.name} is available, so new work goes there. Remoteless activates automatically when your primary and secondary runners are unavailable.`,
                          );
                          return;
                        }
                        setSelectedPhoneCheckout(project.slug);
                        setSelectedProjectPath("");
                        explicitProjectChoiceRef.current = null;
                      }}
                      style={[s.projectPickerRow, { borderColor: active ? c.accent : c.border, backgroundColor: active ? withAlpha(c.accent, "1f") : c.bg, opacity: remotePreferred ? 0.55 : 1 }]}
                      accessibilityRole="button"
                      accessibilityLabel={`Use local checkout ${project.name}`}
                      accessibilityState={{ selected: active }}
                    >
                      <View style={{ flex: 1, minWidth: 0 }}>
                        <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }} numberOfLines={1}>{project.name}</Text>
                        <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }}>{remotePreferred ? `Fallback only · ${taskExecutionPlacement.target.name} available` : `This device · ${project.slug}`}</Text>
                      </View>
                      {active ? <Ionicons name="checkmark-circle" size={20} color={c.accent} /> : null}
                    </Pressable>
                  );
                })}
              </>
            ) : null}
            {codingMode === "local-only" ? (
              <Pressable
                onPress={() => {
                  closeProjectPicker();
                  taskRouter.navigate("/(tabs)/projects" as any);
                }}
                style={[s.projectPickerRow, { borderColor: c.accent, backgroundColor: c.accentSoft }]}
              >
                <View style={{ flex: 1, minWidth: 0 }}>
                  <Text style={{ color: c.accent, fontSize: 14, fontWeight: "700" }}>Browse GitHub & GitLab projects</Text>
                  <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }}>Clone a connected provider repository to this phone</Text>
                </View>
                <Ionicons name="arrow-forward" size={16} color={c.accent} />
              </Pressable>
            ) : null}
            {codingMode !== "local-only" ? (
            <Pressable
              onPress={() => {
                const runnerDeviceId = connectionManager.roleDeviceId("runner") || activeDevice?.id || "default";
                explicitProjectChoiceRef.current = { deviceId: runnerDeviceId, path: "" };
                setSelectedPhoneCheckout(null);
                setSelectedProjectPath("");
              }}
              style={[
                s.projectPickerRow,
                {
                  borderColor: !selectedProjectPath ? c.accent : c.border,
                  backgroundColor: !selectedProjectPath ? withAlpha(c.accent, "1f") : c.bg,
                },
              ]}
            >
              <View style={{ flex: 1, minWidth: 0 }}>
                <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }}>No project (optional)</Text>
                <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }}>
                  Send directly with this machine's default runner.
                </Text>
              </View>
              {!selectedProjectPath ? <Ionicons name="checkmark-circle" size={20} color={c.accent} /> : null}
            </Pressable>
            ) : null}
            {codingMode !== "local-only" && (composerProjects.length === 0 ? (
              <Text style={{ color: c.textMuted, fontSize: 13, paddingVertical: 18 }}>
                No projects reported by the runner machine yet.
              </Text>
            ) : visibleComposerProjects.length === 0 ? (
              <Text style={{ color: c.textMuted, fontSize: 13, paddingVertical: 18 }}>
                No projects match “{projectPickerQuery.trim()}”.
              </Text>
            ) : (
              visibleComposerProjects.map((project) => {
                const active = project.path === selectedProjectPath;
                return (
                  <Pressable
                    key={project.path}
                    onPress={() => {
                      const runnerDeviceId = connectionManager.roleDeviceId("runner") || activeDevice?.id || "default";
                      explicitProjectChoiceRef.current = { deviceId: runnerDeviceId, path: project.path };
                      setSelectedPhoneCheckout(null);
                      setSelectedProjectPath(project.path);
                      if (keepLastProject) {
                        void saveLastTaskProject({
                          deviceId: runnerDeviceId,
                          name: project.name,
                          path: project.path,
                          branch: project.branch,
                          gitRemote: project.gitRemote,
                        });
                      }
                    }}
                    style={[
                      s.projectPickerRow,
                      { borderColor: active ? c.accent : c.border, backgroundColor: active ? withAlpha(c.accent, "1f") : c.bg },
                    ]}
                    testID={`project-picker-row-${project.name}`}
                    accessibilityRole="button"
                    accessibilityLabel={`Select project ${project.name}`}
                    accessibilityState={{ selected: active }}
                  >
                    <View style={{ flex: 1, minWidth: 0 }}>
                      <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }} numberOfLines={1}>{project.name}</Text>
                      <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }} numberOfLines={1}>{project.path}</Text>
                      {[project.branch, project.framework].filter(Boolean).length ? (
                        <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }} numberOfLines={1}>
                          {[project.branch, project.framework].filter(Boolean).join(" · ")}
                        </Text>
                      ) : null}
                    </View>
                    {active ? <Ionicons name="checkmark-circle" size={20} color={c.accent} /> : null}
                  </Pressable>
                );
              })
            ))}
            {codingMode !== "local-only" && remoteProjectRows.length > 0 ? (
              <>
                <Text style={[s.agentPickerSection, { color: c.textMuted, marginLeft: 0, marginTop: 16 }]}>
                  ON OTHER MACHINES
                </Text>
                <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 10 }}>
                  These repos live on another machine — picking one switches the task to that machine.
                </Text>
                {remoteProjectRows.map((row) => (
                  <Pressable
                    key={`${row.device.id}:${row.name}`}
                    onPress={() => {
                      void (async () => {
                        try {
                          if (row.device.id !== activeDevice?.id) {
                            await selectDevice(row.device);
                          }
                          // The switch effect refetches the new machine's
                          // projects; the sheet stays open so the user taps
                          // the exact repo with its real path there.
                        } catch {}
                      })();
                    }}
                    style={[s.projectPickerRow, { borderColor: c.border, backgroundColor: c.bg }]}
                  >
                    <View style={{ flex: 1, minWidth: 0 }}>
                      <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }} numberOfLines={1}>{row.name}</Text>
                      <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }} numberOfLines={1}>
                        {row.gitRemote || "no remote"}
                      </Text>
                    </View>
                    <Ionicons name="arrow-forward" size={16} color={c.textMuted} />
                    <Text style={{ color: c.accent, fontSize: 11, fontWeight: "600", marginLeft: 4 }} numberOfLines={1}>{row.label}</Text>
                  </Pressable>
                ))}
              </>
            ) : null}
          </View>
          <Text style={[s.agentPickerSection, { color: c.textMuted, marginLeft: 0, marginTop: 18 }]}>MCP SERVERS</Text>
          <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 10 }}>
            No MCP is selected by default. Choose tools here or enable Use latest above.
          </Text>
          <Pressable
            key="yaver"
            onPress={() => {
              const next = !includeYaverMcp;
              setIncludeYaverMcp(next);
              persistMCPPrefs({ mcpServers: selectedMcpServers, includeYaverMcp: next });
            }}
            style={[
              s.projectPickerRow,
              { borderColor: includeYaverMcp ? c.accent : c.border, backgroundColor: includeYaverMcp ? withAlpha(c.accent, "1f") : c.bg },
            ]}
            accessibilityRole="button"
            accessibilityLabel="Toggle Yaver tools"
          >
            <View style={{ flex: 1, minWidth: 0 }}>
              <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }} numberOfLines={1}>
                yaver{includeYaverMcp ? "" : " (off)"}
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }} numberOfLines={1}>
                Yaver's own MCP doorway — agent state, tasks, projects, feedback.
              </Text>
            </View>
            {includeYaverMcp ? <Ionicons name="checkmark-circle" size={20} color={c.accent} /> : <Ionicons name="ellipse-outline" size={20} color={c.textMuted} />}
          </Pressable>
          {availableMcpServers.length === 0 ? (
            <Text style={{ color: c.textMuted, fontSize: 13, paddingVertical: 12 }}>
              No enabled external MCP servers registered on this runner.
            </Text>
          ) : (
            availableMcpServers.map((server) => {
              const active = selectedMcpServers.includes(server.name);
              return (
                <Pressable
                  key={server.name}
                  onPress={() => {
                    const next = selectedMcpServers.includes(server.name)
                      ? selectedMcpServers.filter((name) => name !== server.name)
                      : [...selectedMcpServers, server.name];
                    setSelectedMcpServers(next);
                    persistMCPPrefs({ mcpServers: next, includeYaverMcp });
                  }}
                  style={[
                    s.projectPickerRow,
                    { borderColor: active ? c.accent : c.border, backgroundColor: active ? withAlpha(c.accent, "1f") : c.bg },
                  ]}
                >
                  <View style={{ flex: 1, minWidth: 0 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }} numberOfLines={1}>{server.name}</Text>
                    <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }} numberOfLines={1}>
                      {server.toolCount ?? 0} tools{server.hasAuth ? " · auth" : ""}
                    </Text>
                  </View>
                  {active ? <Ionicons name="checkmark-circle" size={20} color={c.accent} /> : <Ionicons name="ellipse-outline" size={20} color={c.textMuted} />}
                </Pressable>
              );
            })
          )}
          {remoteMcpRows.length > 0 ? (
            <>
              <Text style={[s.agentPickerSection, { color: c.textMuted, marginLeft: 0, marginTop: 18 }]}>
                ON OTHER MACHINES
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 10 }}>
                These MCPs live on another machine — picking one switches the task to that machine.
              </Text>
              {remoteMcpRows.map((row) => {
                const active = selectedMcpServers.includes(row.server.name);
                return (
                  <Pressable
                    key={`${row.device.id}:${row.server.name}`}
                    onPress={() => {
                      suppressMcpRestoreRef.current = true;
                      setTimeout(() => { suppressMcpRestoreRef.current = false; }, 5000);
                      void (async () => {
                        try {
                          if (row.device.id !== activeDevice?.id) {
                            await selectDevice(row.device);
                          }
                          const deviceId = row.device.id || "default";
                          const next = [...new Set([...mcpStateRef.current.mcpServers, row.server.name])];
                          mcpStateRef.current = { mcpServers: next, includeYaverMcp };
                          setSelectedMcpServers(next);
                          if (token) {
                            void saveMCPServersToConvex(token, { deviceId, mcpServers: next, includeYaverMcp });
                          }
                        } catch {}
                      })();
                    }}
                    style={[
                      s.projectPickerRow,
                      { borderColor: active ? c.accent : c.border, backgroundColor: active ? withAlpha(c.accent, "1f") : c.bg },
                    ]}
                  >
                    <View style={{ flex: 1, minWidth: 0 }}>
                      <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }} numberOfLines={1}>{row.server.name}</Text>
                      <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }} numberOfLines={1}>
                        {row.server.toolCount ?? 0} tools · {row.label}
                      </Text>
                    </View>
                    {active ? <Ionicons name="checkmark-circle" size={20} color={c.accent} /> : <Ionicons name="ellipse-outline" size={20} color={c.textMuted} />}
                  </Pressable>
                );
              })}
            </>
          ) : null}
          <Text style={[s.agentPickerSection, { color: c.textMuted, marginLeft: 0, marginTop: 18 }]}>TEXT CORRECTION</Text>
          <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 10 }}>
            Autocorrect, spellcheck, and sentence capitalization for chat inputs.
          </Text>
          <Pressable
            onPress={() => setTextCorrectionEnabled((prev) => {
              const next = !prev;
              void saveTextCorrectionEnabled(next);
              return next;
            })}
            style={[
              s.projectPickerRow,
              { borderColor: textCorrectionEnabled ? c.accent : c.border, backgroundColor: textCorrectionEnabled ? withAlpha(c.accent, "1f") : c.bg },
            ]}
            accessibilityRole="button"
            accessibilityLabel="Toggle text correction"
          >
            <View style={{ flex: 1, minWidth: 0 }}>
              <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }}>
                Text correction{textCorrectionEnabled ? "" : " (off)"}
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }} numberOfLines={1}>
                autoCorrect + sentence auto-capitalization for prompts
              </Text>
            </View>
            {textCorrectionEnabled ? <Ionicons name="checkmark-circle" size={20} color={c.accent} /> : <Ionicons name="ellipse-outline" size={20} color={c.textMuted} />}
          </Pressable>
        </ScrollView>
      </View>
    </KeyboardAvoidingView>
  );
  // Multi-target wizard state. Only used when DeviceContext.multiTargetMode
  // is true: the FAB opens the wizard first, the wizard sets pendingTarget
  // (and switches the QUIC client to that device via selectDevice), then
  // the compose modal opens with the runner + model locked to pendingTarget.
  const [showTargetWizard, setShowTargetWizard] = useState(false);
  const [pendingTarget, setPendingTarget] = useState<TaskTarget | null>(null);
  // Runner choices belong to the machine that will execute the task. Keep
  // this one id aligned with the machine chip so probes, defaults, picker
  // persistence, and dispatch cannot quietly talk about different boxes.
  const runnerSelectionDeviceId = resolveRunnerSelectionDeviceId({
    taskTargetDeviceId: pendingTarget?.deviceId,
    runnerRoleDeviceId: machineRoles?.runnerDeviceId,
    activeDeviceId: activeDevice?.id,
  });
  const runnerSelectionDevice = devices.find((device) => device.id === runnerSelectionDeviceId)
    || activeDevice;
  const runnerSelectionConnected = shouldPollRunnerState({
    targetDeviceId: runnerSelectionDeviceId,
    connectedDeviceIds,
    focusedConnectionStatus: connectionStatus,
  });
  // Reconciliation must hit the exact machine named by the Tasks runner chip;
  // never whichever pooled client happened to connect most recently.
  const taskRunnerClient = useCallback(
    () => runnerSelectionDeviceId
      ? connectionManager.clientFor(runnerSelectionDeviceId)
      : connectionManager.runnerClient(),
    [runnerSelectionDeviceId],
  );
  const [newTaskText, setNewTaskText] = useState("");
  const [showSilentInput, setShowSilentInput] = useState(false);
  const [silentInputEnabled, setSilentInputEnabled] = useState(DEFAULT_SILENT_INPUT_CONFIG.enabled);
  const newTaskTextRef = useRef("");
  newTaskTextRef.current = newTaskText;

  useEffect(() => {
    let active = true;
    void loadSilentInputConfig().then((config) => {
      if (active) setSilentInputEnabled(config.enabled);
    });
    return () => { active = false; };
  }, []);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const submitInFlightRef = useRef(false);
  const [taskSubmitError, setTaskSubmitError] = useState<string | null>(null);
  const [selectedModel, setSelectedModel] = useState<string>("sonnet");
  const [selectedReasoningEffort, setSelectedReasoningEffort] = useState<"none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra">("medium");
  const [runnerControlMode, setRunnerControlMode] = useState<"model" | "exit" | null>(null);
  const [runnerControlCatalog, setRunnerControlCatalog] = useState<TaskRunnerControlCatalog | null>(null);
  const [runnerControlStep, setRunnerControlStep] = useState<"models" | "effort">("models");
  const [runnerControlModel, setRunnerControlModel] = useState("");
  const [runnerControlEffort, setRunnerControlEffort] = useState<"none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra">("medium");
  const [runnerControlBusy, setRunnerControlBusy] = useState(false);
  const [runnerControlError, setRunnerControlError] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [followUpText, setFollowUpText] = useState("");
  const followUpTextRef = useRef("");
  followUpTextRef.current = followUpText;
  const [isSendingFollowUp, setIsSendingFollowUp] = useState(false);
  const [followUpExpanded, setFollowUpExpanded] = useState(false);
  const [showFollowUpOptions, setShowFollowUpOptions] = useState(false);
  // A task detail is allowed to choose a different runner for its NEXT turn.
  // Keep that choice task-scoped: selectedRunner also drives the New Task
  // composer, so using it directly made an unrelated global picker value
  // switch (or block) an ordinary follow-up.
  const [followUpRunnerOverride, setFollowUpRunnerOverride] = useState<string | null>(null);
  // Pending agent_question pulled from the SSE stream. When non-null
  // the question sheet is open; the user types/picks an answer, the
  // sheet POSTs to /tasks/{id}/answer (via answerTaskQuestion), and
  // we clear this state. The daemon also broadcasts agent_answered
  // when another device on the same account answers first — we clear
  // on that event too so neither sheet stays orphaned.
  const [agentQuestion, setAgentQuestion] = useState<{
    id: string;
    taskId: string;
    prompt: string;
    header?: string;
    kind: "text" | "choice" | "secret";
    choices?: string[];
    multi?: boolean;
    vaultHint?: string;
    screenshot?: string; // F3 handoff: base64 PNG of the relevant page region
    step?: string;       // F3 handoff step type
  } | null>(null);
  const [agentAnswerText, setAgentAnswerText] = useState("");
  // Structured command-card models, keyed taskId → commandId. Fed by
  // command_* SSE events (see the onEvent handler); rendered as a
  // foldable "Commands" section in the chat footer. Per-task so
  // switching tasks doesn't bleed cards.
  const [cmdCardsByTask, setCmdCardsByTask] = useState<
    Record<string, Record<string, CommandCardModel>>
  >({});
  // Claude-Code-style choice state: which options are checked (multi)
  // and whether the free-text "Other…" row is expanded. Reset every
  // time a new question opens (see the stream consumer + late-join).
  const [agentMultiPicks, setAgentMultiPicks] = useState<string[]>([]);
  const [agentOtherOpen, setAgentOtherOpen] = useState(false);
  const [submittingAgentAnswer, setSubmittingAgentAnswer] = useState(false);
  const [attachedImages, setAttachedImages] = useState<ImageAttachment[]>([]);
  const [followUpImages, setFollowUpImages] = useState<ImageAttachment[]>([]);
  // OpenCode Build|Plan for the FOLLOW-UP composer (the in-chat mode switch,
  // 2026-08-13). Empty = agent default: no mode is sent until the user taps a
  // segment, so a conversation that never touches this keeps its behavior.
  // Selecting one sends mode on the next continue/fork, which is how a
  // plan-mode chat switches to build (and back) without leaving the thread.
  const [followUpOpenCodeMode, setFollowUpOpenCodeMode] = useState<string>("");
  const [isReconnecting, setIsReconnecting] = useState(false);
  const [recoveringDeviceId, setRecoveringDeviceId] = useState<string | null>(null);
  const [quicState, setQuicState] = useState<ConnectionState>(quicClient.connectionState);
  const [connMode, setConnMode] = useState<ConnectionMode>(quicClient.connectionMode);
  const [reconnectAttempt, setReconnectAttempt] = useState<number>(quicClient.reconnectAttempt);
  const [agentStatus, setAgentStatus] = useState<AgentStatus | null>(null);
  const [pingRtt, setPingRtt] = useState<number | null>(null);
  const [isPinging, setIsPinging] = useState(false);
  const [pingResult, setPingResult] = useState<{ ok: boolean; rttMs: number; hostname?: string; mode?: string } | null>(null);
  const [showPingResult, setShowPingResult] = useState(false);
  const [isRestartingRunner, setIsRestartingRunner] = useState(false);
  const [runnerInstallState, setRunnerInstallState] = useState<{
    runnerId: string;
    kind: "installing" | "failed";
    line: string;
  } | null>(null);
  const [availableRunners, setAvailableRunners] = useState<RunnerInfo[]>([]);
  const [runnersFetchState, setRunnersFetchState] = useState<RunnerFetchState>("idle");
  const [selectedRunner, setSelectedRunner] = useState<string>(""); // "" = default
  // Browser RN-web cannot present Alert.alert action choices reliably: the
  // title appears, but its native button array is discarded by window.alert.
  // Keep the chooser in the React tree so the exact same tappable chips work
  // on iPhone, iPad, and the browser automation lane.
  const [showComposerRunnerChoices, setShowComposerRunnerChoices] = useState(false);
  // Tasks overview reuses its existing runner-status text as the switch
  // affordance. The choices stay collapsed so the banner gains capability,
  // not another permanent status row.
  const [showBannerRunnerChoices, setShowBannerRunnerChoices] = useState(false);
  useEffect(() => {
    if (normalizeTaskRunnerId(selectedRunner) !== "remoteless") return;
    const preferred = availableRunners.find((runner) => runner.ready && normalizeTaskRunnerId(runner.id) !== "remoteless");
    if (preferred) setSelectedRunner(preferred.id);
  }, [availableRunners, selectedRunner]);

  useEffect(() => {
    setFollowUpRunnerOverride(null);
  }, [selectedTask?.id]);
  // OpenCode-only: which agent (build / plan / custom) drives the
  // task. Forwarded as `mode` on the task POST and turned into
  // `--agent <mode>` on `opencode run`. Empty = use the user's
  // defaultAgent from opencode.json. Other runners ignore it.
  const [selectedOpenCodeMode, setSelectedOpenCodeMode] = useState<string>("");
  // Custom agents the user has defined under `agent.<name>` in
  // opencode.json (review / chat / research / …). Loaded once when the
  // composer opens with selectedRunner=opencode, plus a refresh on
  // device switch — without this the picker would only ever show
  // build / plan even for users who already wired a custom agent up
  // through OpenCodeConfigModal or by hand. Empty array = "couldn't
  // fetch" or "no customs configured"; we fall back to the stock pair.
  const [opencodeAgents, setOpencodeAgents] = useState<string[]>([]);
  const [availableModels, setAvailableModels] = useState<ModelInfo[]>([]);
  const [customCommand, setCustomCommand] = useState("");
  const [showAgentPicker, setShowAgentPicker] = useState(false);
  // Tracks whether the user has explicitly picked a runner / model in this
  // session. Until they do, the Convex-stored per-device primary
  // (primaryRunnerByDevice / primaryModelByDevice) is the source of truth
  // and overrides any heuristic-seeded value. Without this, the runner-
  // seeding effect locks in "claude" before Convex finishes loading, then
  // the "preserve current" short-circuit refuses to switch to the user's
  // actual primary (Codex on yaver-test-ephemeral, etc.).
  const userPickedRunnerRef = useRef(false);
  const userPickedModelRef = useRef(false);
  // When the Agent & Model picker is opened from a FAILED task's "Switch
  // model & retry" CTA, closing it re-runs the original prompt with the
  // chosen runner/model (recovery from e.g. "gpt-5.4 not supported"). The
  // follow-up composer opens the same picker WITHOUT this flag, so its
  // Done just closes. Holds the task to re-run.
  const retryAfterPickRef = useRef<Task | null>(null);
  const pendingCloudDispatchRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    userPickedRunnerRef.current = false;
    userPickedModelRef.current = false;
    setShowComposerRunnerChoices(false);
    setShowBannerRunnerChoices(false);
    setRunnerInstallState(null);
  }, [runnerSelectionDeviceId]);

  useEffect(() => {
    if (routeProjectDir) {
      setSelectedProjectPath(routeProjectDir);
      if (shouldSelectRouteProject) {
        const runnerDeviceId = connectionManager.roleDeviceId("runner") || activeDevice?.id || "default";
        explicitProjectChoiceRef.current = { deviceId: runnerDeviceId, path: routeProjectDir };
      }
    }
  }, [activeDevice?.id, routeProjectDir, shouldSelectRouteProject]);

  useEffect(() => {
    if (!showNewTask) return;
    if (!keepLastProject && !routeProjectDir) setSelectedProjectPath("");
    if (!useLatestMCP) {
      setSelectedMcpServers([]);
      setIncludeYaverMcp(false);
    }
  }, [showNewTask]); // Scope defaults are evaluated once when the composer opens.

  useEffect(() => {
    if (!selectedTask?.id) return;
    if (!keepLastProject && !routeProjectDir) setSelectedProjectPath("");
    if (!useLatestMCP) {
      setSelectedMcpServers([]);
      setIncludeYaverMcp(false);
    }
  }, [selectedTask?.id]);

  useEffect(() => {
    let cancelled = false;
    void loadKeepLastProjectEnabled().then((enabled) => {
      if (!cancelled) setKeepLastProject(enabled);
    });
    void loadUseLatestMCPEnabled().then((enabled) => {
      if (!cancelled) setUseLatestMCP(enabled);
    });
    void loadTextCorrectionEnabled().then((enabled) => {
      if (!cancelled) setTextCorrectionEnabled(enabled);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const [runnerAuthModalRunner, setRunnerAuthModalRunner] = useState<string | null>(null);
  // Target device id for the runner-auth modal. When set, the modal routes
  // /runner-auth/browser/* through /peer/<id> so the OAuth flow runs on
  // the failing remote box, not on whichever agent is currently focused.
  const [runnerAuthModalTarget, setRunnerAuthModalTarget] = useState<string | null>(null);
  // OpenCode provider/model/key editor — opened from the composer banner's
  // "Configure" CTA when OpenCode reports a config gap (model's provider has
  // no key). startInAdd jumps straight to the add-provider+key sheet.
  const [showOpenCodeConfig, setShowOpenCodeConfig] = useState(false);
  const [openCodeConfigStartInAdd, setOpenCodeConfigStartInAdd] = useState(false);
  const [openCodeConfigTarget, setOpenCodeConfigTarget] = useState<string | null>(null);

  const chatScrollRef = useRef<FlatList>(null);
  // Follow live replies only while the reader is already at the bottom. A
  // semantic token stream can update many times per second; debounce it so we
  // do not queue hundreds of animated scrolls or yank somebody away from an
  // older message they deliberately opened.
  const chatShouldFollowRef = useRef(true);
  const chatFollowTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pendingOpenTaskRef = useRef<Task | null>(null);
  /** AbortController per in-flight yaver-agent run, keyed by synthetic
   *  task id. handleStopTask aborts the matching controller; the
   *  runner unwinds via AbortError and the task ends up "stopped". */
  const yaverAgentAbortersRef = useRef<Map<string, AbortController>>(new Map());
  /** Latest successful phone-local vibe turn per task. Contents stay in memory
   *  only; Undo restores the exact pre-turn bytes without touching Git. */
  const localTurnUndoRef = useRef<Map<string, { slug: string; snapshot: TurnSnapshot }>>(new Map());
  const [localTurnUndoEpoch, setLocalTurnUndoEpoch] = useState(0);
  const [localGitExpandedTaskId, setLocalGitExpandedTaskId] = useState<string | null>(null);
  const didApplyRouteSeedRef = useRef(false);
  const didAutoSubmitRoutePromptRef = useRef(false);

  // Project + Todo state
  const [projectName, setProjectName] = useState<string>("");
  const [projectBranch, setProjectBranch] = useState<string>("");
  const [todoCount, setTodoCount] = useState(0);
  const [todoTotal, setTodoTotal] = useState(0);
  const [todoDone, setTodoDone] = useState(0);

  // Speech state
  const { token, user, logout } = useAuth();
  const ownerRemotelessEnabled = remotelessAccessAllowed(user?.isOwner);
  const [autoRenderVibing, setAutoRenderVibing] = useState(false);
  useEffect(() => subscribeAutoRenderVibing(setAutoRenderVibing), []);
  useEffect(() => {
    if (!token) {
      setAutoRenderVibing(false);
      return;
    }
    let cancelled = false;
    void getUserSettings(token)
      .then((settings) => {
        if (!cancelled) publishAutoRenderVibing(settings.autoRenderVibing === true);
      })
      .catch(() => { if (!cancelled) publishAutoRenderVibing(false); });
    return () => { cancelled = true; };
  }, [token]);
  // Persist the MCP selection to Convex — same mcpServersByDevice row the web
  // chat + Vibing composers and tvOS write, so a selection made on the phone
  // is remembered on the web and vice versa (2026-08-10). Fire-and-forget:
  // a failed settings write never blocks the picker. Reads the current state
  // via a ref so callers can call it right after setState without waiting.
  const mcpStateRef = useRef({ mcpServers: [] as string[], includeYaverMcp: false });
  mcpStateRef.current = { mcpServers: selectedMcpServers, includeYaverMcp };
  const persistMCPPrefs = useCallback((override?: { mcpServers: string[]; includeYaverMcp: boolean }) => {
    if (!token) return;
    const deviceId = activeDevice?.id || "default";
    const snap = override ?? mcpStateRef.current;
    void saveMCPServersToConvex(token, {
      deviceId,
      mcpServers: snap.mcpServers,
      includeYaverMcp: snap.includeYaverMcp,
    });
  }, [activeDevice?.id, token]);
  const phoneLocalYaverToolContext = useMemo<YaverAgentToolContext>(() => ({
    devices: () => devices,
    primaryDeviceId: () => primaryDeviceId,
    secondaryDeviceId: () => secondaryDeviceId,
    selectDevice: async (deviceId) => {
      const d = devices.find((x) => x.id === deviceId);
      if (d) await selectDevice(d);
    },
  }), [devices, primaryDeviceId, secondaryDeviceId, selectDevice]);
  const phoneLocalAuditTools = useMemo(
    () => makeYaverReadOnlyCodingTools(phoneLocalYaverToolContext),
    [phoneLocalYaverToolContext],
  );
  const [isRecording, setIsRecording] = useState(false);
  const [isTranscribing, setIsTranscribing] = useState(false);
  // Transient inline status for the composer's ⚡ Hermes-reload action.
  const [reloadFlash, setReloadFlash] = useState<string | null>(null);
  const [speechProvider, setSpeechProvider] = useState<SpeechProvider | null>("on-device");
  const [speechApiKey, setSpeechApiKey] = useState<string | undefined>();
  const [sttModel, setSttModel] = useState<string | undefined>();
  const [ttsModel, setTtsModel] = useState<string | undefined>();

  const saveDeferredCloudWorkspaceTask = useCallback(async (
    err: CloudWorkspaceRequiredError,
    args: {
      title: string;
      description: string;
      model?: string;
      runner?: string;
      customCommand?: string;
      speechContext?: any;
      images?: ImageAttachment[];
      workDir?: string;
      projectName?: string;
      mode?: string;
      video?: { enabled?: boolean; source?: "browser" | "sim-ios" | "sim-android" | "phone" };
      codeMode?: boolean;
      allowLocalFallback?: boolean;
      mcpServers?: string[];
      goal?: string;
      includeYaverMcp?: boolean;
    },
  ): Promise<Task> => {
    const row = await saveCloudWorkspaceRequiredDispatch({
      err,
      params: args,
      sourceSurface: "mobile",
      requestedRunner: args.runner,
      projectSlug: args.workDir?.split(/[\\/]/).filter(Boolean).pop()?.slice(0, 80),
    });
    return pendingCloudTaskPlaceholder(row);
  }, []);
  const [ttsVoice, setTtsVoice] = useState<string | undefined>();
  const [ttsEnabled, setTtsEnabled] = useState(false);
  const [ttsProvider, setTtsProvider] = useState<TtsProvider>("device");
  const [inputFromSpeech, setInputFromSpeech] = useState(false);
  // Persisted task preference from Settings. When enabled, the agent
  // records a short MP4 demo after the task finishes and the task row
  // gets a "▶ Watch demo" button when the clip is ready.
  const [videoSummaryEnabled, setVideoSummaryEnabled] = useState(false);
  useEffect(() => {
    let cancelled = false;
    loadTaskVideoSummaryEnabled()
      .then((enabled) => {
        if (cancelled) return;
        setVideoSummaryEnabled(enabled);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);
  // Ask / deep-audit mode: when ON, the task runs as a grounded
  // explain-first question-answer (askModePreamble — file:line cites,
  // confirm gate) instead of a work run. Web has the Ask toggle /
  // auto-detect; this closes the mobile gap so a phone can trigger the
  // same deep-audit frame.
  const [askModeEnabled, setAskModeEnabled] = useState(false);
  // Deep-audit is a one-shot frame, not a sticky global mode. Leaving it on
  // after a successful send false-positives ordinary follow-ups into read-only
  // explain mode, so we consume it only once the send is accepted.
  const consumeAskMode = useCallback(() => {
    setAskModeEnabled(false);
  }, []);
  // Inline player state — set the clipId to open the modal that plays
  // the task's recorded demo MP4. Sourced from the agent at
  // /vibing/preview/clip/<id>.
  const [videoSummaryClipId, setVideoSummaryClipId] = useState<string | null>(null);
  const audioRecordingRef = useRef<any>(null);
  const realtimeRef = useRef<{ stop: () => Promise<string> } | null>(null);
  const recordingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [preRecordText, setPreRecordText] = useState(""); // text before recording started

  // Load speech settings from Convex (default: on-device whisper). We track
  // the whisper init error so the mic button can warn up-front instead of
  // failing with a cryptic message when the user actually taps it.
  const [whisperInitError, setWhisperInitError] = useState<string | null>(null);
  useEffect(() => {
    initWhisper()
      .then(() => setWhisperInitError(null))
      .catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : String(e);
        console.warn("[speech] whisper init failed:", msg);
        setWhisperInitError(msg);
      });
    // Speech config is LOCAL ONLY — provider / key / model / voice are
    // read from SecureStore via loadLocalSpeechConfig and are NEVER
    // fetched from or written to Convex. The one shared speech preference,
    // TTS enabled, still comes from getUserSettings.
    loadLocalSpeechConfig().then((sc) => {
      if (sc.sttProvider) setSpeechProvider(sc.sttProvider);
      if (sc.sttModel) setSttModel(sc.sttModel);
      if (sc.ttsProvider) setTtsProvider(sc.ttsProvider);
      if (sc.ttsModel) setTtsModel(sc.ttsModel);
      if (sc.ttsVoice) setTtsVoice(sc.ttsVoice);
      if (sc.apiKey) setSpeechApiKey(sc.apiKey);
    }).catch(() => {});
    if (!token) return;
    getUserSettings(token).then((s) => {
      if (s.ttsEnabled) setTtsEnabled(s.ttsEnabled);
    }).catch((e: unknown) => {
      const msg = e instanceof Error ? e.message : String(e);
      console.warn("[speech] getUserSettings failed:", msg);
    });
  }, [token]);

  // Track QUIC connection state and mode. The deps include
  // `activeDevice?.id` because `quicClient` is now a Proxy that
  // delegates to whichever pool client is currently focused — without
  // re-subscribing on focus change, the listener would stay bound to
  // the boot-time fallback client (which never connects), `quicState`
  // would freeze at "disconnected", and effectiveState's
  // connected-but-quicState-stale branch would silently render the
  // banner as "Disconnected" while the pool was actually live.
  useEffect(() => {
    setQuicState(quicClient.connectionState);
    setConnMode(quicClient.connectionMode);
    const unsub1 = quicClient.on("connectionState", setQuicState);
    const unsub2 = quicClient.on("connectionMode", setConnMode);
    const unsub3 = quicClient.on("reconnectAttempt", setReconnectAttempt);
    return () => { unsub1(); unsub2(); unsub3(); };
  }, [activeDevice?.id]);

  // Pull the connected device's opencode.json agent list whenever the
  // user has opencode picked. Falls back to [] (which means the
  // composer chip rail will use just the stock build/plan pair).
  // Refetch on device change so a context switch from machine A
  // (with `agent.review` defined) to machine B (without) doesn't
  // leave the picker showing review.
  useEffect(() => {
    if (connectionStatus !== "connected" || selectedRunner !== "opencode") {
      setOpencodeAgents([]);
      return;
    }
    let cancelled = false;
    const client = runnerSelectionDeviceId
      ? connectionManager.clientFor(runnerSelectionDeviceId)
      : quicClient;
    client.getOpenCodeConfig().then((cfg) => {
      if (cancelled) return;
      const names = (cfg?.agents || []).map((a) => a.name).filter((n): n is string => typeof n === "string" && n.length > 0);
      setOpencodeAgents(names);
    }).catch(() => {
      if (!cancelled) setOpencodeAgents([]);
    });
    return () => { cancelled = true; };
  }, [connectionStatus, selectedRunner, runnerSelectionDeviceId]);

  useEffect(() => {
    if (selectedRunner !== "opencode") return;
    if (userPickedRunnerRef.current) return;
    const preferredMode = runnerSelectionDeviceId ? primaryModeByDevice[runnerSelectionDeviceId] : "";
    if (preferredMode && selectedOpenCodeMode !== preferredMode) {
      setSelectedOpenCodeMode(preferredMode);
      return;
    }
    if (!preferredMode && selectedOpenCodeMode !== "") {
      setSelectedOpenCodeMode("");
    }
  }, [selectedRunner, runnerSelectionDeviceId, primaryModeByDevice, selectedOpenCodeMode]);

  // Seed selectedRunner when runners load or the active device / pin
  // changes. Uses a functional setState callback so we can read the
  // latest selectedRunner without listing it as a dep — that would
  // re-trigger the seeding loop on every chip tap and undo the user's
  // choice in the small race window before primaryRunnerByDevice
  // updates.
  useEffect(() => {
    if (availableRunners.length === 0) return;
    const RUNNER_WL = new Set(["claude", "codex", "opencode"]);
    const installed = availableRunners.filter((runner) => runner.installed && RUNNER_WL.has(runner.id));
    if (installed.length === 0) return;
    const ready = installed.filter((runner) => runner.ready !== false);
    const explicitRunner = runnerSelectionDeviceId ? primaryRunnerByDevice[runnerSelectionDeviceId] : "";
    setSelectedRunner((current) => {
      // Convex per-device primary is authoritative until the user picks
      // a chip in this session. Without this branch, the heuristic
      // fallback (which always returns "claude" when claude is
      // installed) gets seeded before Convex's userSettings load, then
      // the "preserve current" rule below refuses to switch to the
      // actual primary (e.g. Codex on yaver-test-ephemeral).
      if (
        !userPickedRunnerRef.current &&
        explicitRunner &&
        (RUNNER_WL.has(explicitRunner) || installed.some((r) => r.id === explicitRunner))
      ) {
        return explicitRunner;
      }
      // Preserve any explicit user pick — including the three first-class
      // agents that may not be installed YET on this box (codex/opencode
      // commonly need `yaver install` first). Reverting to claude here
      // silently swallowed chip taps on a fresh test box.
      if (current && (RUNNER_WL.has(current) || current === "custom")) return current;
      if (current && installed.some((r) => r.id === current)) return current;
      if (explicitRunner && (RUNNER_WL.has(explicitRunner) || installed.some((r) => r.id === explicitRunner))) return explicitRunner;
      const seededRunner = runnerSelectionDevice
        ? preferredSeededRunnerForDevice({
            device: runnerSelectionDevice,
            signedInEmail: user?.email,
            readyRunnerIds: ready.map((r) => r.id),
            installedRunnerIds: installed.map((r) => r.id),
          })
        : null;
      const seedPool = ready.length > 0 ? ready : installed;
      const preferred =
        seedPool.find((r) => r.id === seededRunner) ||
        ready.find((r) => r.isDefault) ||
        ready.find((r) => r.id === "claude") ||
        ready.find((r) => r.id === "codex") ||
        ready.find((r) => r.id === "opencode") ||
        installed.find((r) => r.isDefault) ||
        installed[0];
      return preferred ? preferred.id : current;
    });
  }, [availableRunners, primaryRunnerByDevice, runnerSelectionDevice, runnerSelectionDeviceId, user?.email]);

  // Update models when runner selection changes. Uses functional
  // setState so it doesn't need selectedModel as a dep — same fight-the-
  // user concern as the runner seeding above.
  useEffect(() => {
    const normalizedSelectedRunner = normalizeTaskRunnerId(selectedRunner);
    const runner = availableRunners.find((r) => normalizeTaskRunnerId(r.id) === normalizedSelectedRunner);
    if (!runner?.models?.length) {
      setAvailableModels([]);
      setSelectedModel("");
      return;
    }
    setAvailableModels(runner.models);
    const explicitModel = runnerSelectionDeviceId ? primaryModelByDevice[runnerSelectionDeviceId] : "";
    setSelectedModel((current) => {
      // Convex per-device primary model wins until the user explicitly
      // picks a chip in this session — same reasoning as the runner
      // seeding effect above. Otherwise the heuristic default beats the
      // stored primary on first render.
      if (
        !userPickedModelRef.current &&
        explicitModel &&
        runner.models!.some((m) => m.id === explicitModel)
      ) {
        return explicitModel;
      }
      // Preserve any explicit user pick — same fight-the-user concern as
      // the runner seeding above. Even if the model isn't in the current
      // runner.models list (e.g. fresh /agent/runners response dropped a
      // staged model the user just tapped), keep their choice; the send
      // path validates and surfaces a clear error if it's actually
      // invalid. Reverting silently to the default makes Sonnet-vs-Opus
      // chips look broken when they're tapped.
      // Keep `current` only if it's actually valid for THIS runner, or the
      // user explicitly tapped it this session (a staged model the latest
      // /agent/runners response may have dropped — the send path validates
      // and surfaces a clear error). Without the validity check the initial
      // default "sonnet" (a Claude model) survived a switch to Codex →
      // nonsensical "Codex · Sonnet" badge, then the agent fell back to its
      // own default and the task failed ("gpt-5.4 not supported with a
      // ChatGPT account"). A stale cross-runner default is NOT a user pick.
      if (current && (runner.models!.some((m) => m.id === current) || userPickedModelRef.current)) {
        return current;
      }
      if (explicitModel && runner.models!.some((m) => m.id === explicitModel)) return explicitModel;
      const seededModel = runnerSelectionDevice
        ? preferredDefaultModelForRunner(runner.id, runnerSelectionDevice, user?.email)
        : null;
      const preferredModel =
        runner.models!.find((m) => m.isDefault)?.id ||
        (seededModel && runner.models!.find((m) => m.id === seededModel)?.id) ||
        runner.models![0].id;
      return preferredModel || current;
    });
  }, [availableRunners, primaryModelByDevice, runnerSelectionDevice, runnerSelectionDeviceId, selectedRunner, user?.email]);

  const selectedRunnerRow = useMemo(
    () => availableRunners.find((runner) => normalizeTaskRunnerId(runner.id) === normalizeTaskRunnerId(selectedRunner)) || null,
    [availableRunners, selectedRunner],
  );
  const selectedModelRow = useMemo(
    () => availableModels.find((model) => model.id === selectedModel) || null,
    [availableModels, selectedModel],
  );
  const selectedModelReasoningEfforts = useMemo(() => {
    const advertised = selectedModelRow?.supportedReasoningEfforts || [];
    return advertised.map((item) => item.reasoningEffort);
  }, [selectedModelRow]);
  useEffect(() => {
    if (normalizeTaskRunnerId(selectedRunner) !== "codex") return;
    const saved = runnerSelectionDeviceId ? primaryReasoningEffortByDevice[runnerSelectionDeviceId] : "";
    const next = selectedModelReasoningEfforts.includes(saved as any)
      ? saved
      : selectedModelReasoningEfforts.length === 0
        ? (saved || selectedModelRow?.defaultReasoningEffort || "medium")
      : selectedModelReasoningEfforts.includes(selectedReasoningEffort as any)
        ? selectedReasoningEffort
        : (selectedModelRow?.defaultReasoningEffort || "medium");
    setSelectedReasoningEffort(next as typeof selectedReasoningEffort);
  }, [primaryReasoningEffortByDevice, runnerSelectionDeviceId, selectedModelReasoningEfforts, selectedModelRow?.defaultReasoningEffort, selectedRunner]);
  const selectedRunnerAuthIssue = useMemo(
    () => runnerAuthIssue(selectedRunnerRow),
    [selectedRunnerRow],
  );

  const resolveRunnerForSend = useCallback((fallbackRunner?: string | null, dispatchDeviceId?: string | null): string | undefined => {
    return resolveRunnerForRemoteSend({
      activeDeviceId: runnerSelectionDeviceId,
      // Runner/render split: defaults key off the box the task RUNS on.
      dispatchDeviceId: dispatchDeviceId || runnerSelectionDeviceId,
      primaryRunnerByDevice,
      selectedRunner,
      fallbackRunner,
      userPickedRunner: userPickedRunnerRef.current,
    });
  }, [primaryRunnerByDevice, runnerSelectionDeviceId, selectedRunner]);

  const resolveModelForSend = useCallback((runnerId: string | undefined, fallbackModel?: string | null, dispatchDeviceId?: string | null): string | undefined => {
    return resolveModelForRemoteSend({
      runnerId,
      activeDevice: runnerSelectionDevice,
      dispatchDeviceId: dispatchDeviceId || runnerSelectionDeviceId,
      primaryModelByDevice,
      selectedModel,
      fallbackModel,
      availableRunners,
      signedInEmail: user?.email,
      userPickedModel: userPickedModelRef.current,
    });
  }, [availableRunners, primaryModelByDevice, runnerSelectionDevice, runnerSelectionDeviceId, selectedModel, user?.email]);

  // Live mirror of the fetch state for the poller below. The poller must READ
  // this, never DEPEND on it — see the storm described above the effect.
  const runnersFetchStateRef = useRef<RunnerFetchState>("idle");
  runnersFetchStateRef.current = runnersFetchState;

  const refreshRunnerState = useCallback(async () => {
    if (!runnerSelectionConnected) return;
    // Task admission owns the short-request lane while it is awaiting an ACK.
    // Runner discovery invokes three independent probes and used to overlap a
    // POST /tasks on browser/low-memory boxes, turning a healthy direct route
    // into 20-30 seconds of contention. The next self-scheduled cycle catches
    // up after submit; runner inventory is advisory, task acknowledgement is not.
    if (submitInFlightRef.current) return;
    setRunnersFetchState((prev) => (prev === "ok" ? prev : "loading"));
    try {
      const client = runnerSelectionDeviceId
        ? connectionManager.clientFor(runnerSelectionDeviceId)
        : quicClient;
      if (!client.isConnected) throw new Error("Runner machine is not connected");
      const [probe, status, authRows] = await Promise.all([
        client.getRunnersProbe(),
        client.getAgentStatus(),
        // `/runner-auth/status` is the canonical machine-local auth audit.
        // Reconcile it with `/agent/runners` so Tasks cannot retain a stale
        // "needs sign-in" after the selected box's runner is authenticated.
        client.runnerAuthStatusOrNull(),
      ]);
      const runners = reconcileRunnerAuthStatus(probe.runners, authRows);
      // Hold the PREVIOUS objects when the box's answer is materially
      // unchanged. The probe parses fresh JSON every poll, so identity is
      // always new; handing that straight to setState re-ran every runner /
      // model useMemo and re-derived the banner text on a metronome even when
      // nothing about the box had moved. `sameRunnerList` / `sameAgentStatus`
      // compare exactly what the banner renders (see runnerPollPolicy.ts).
      setAvailableRunners((prev) => (sameRunnerList(prev, runners) ? prev : runners));
      setRunnersFetchState(probe.state);
      if (status) setAgentStatus((prev) => (sameAgentStatus(prev, status) ? prev : status));
    } catch {
      setRunnersFetchState("network-error");
    }
  }, [runnerSelectionConnected, runnerSelectionDeviceId]);

  // A missing runner is a deterministic capability gap, not something a
  // restart can repair. Install through the selected runner box's own pooled
  // client, stream progress in-place, then re-probe the real generation
  // capability. Keeping this state in the Tasks surface also keeps the typed
  // prompt mounted while the repair runs.
  const handleInstallRunner = useCallback(async (requestedRunnerId?: string | null) => {
    const runnerId = normalizeTaskRunnerId(requestedRunnerId);
    if (runnerId !== "claude" && runnerId !== "codex" && runnerId !== "opencode") return;
    if (runnerInstallState?.kind === "installing") return;

    setRunnerInstallState({ runnerId, kind: "installing", line: `Starting ${displayRunnerLabel(runnerId)} installer…` });
    try {
      const client = runnerSelectionDeviceId
        ? connectionManager.clientFor(runnerSelectionDeviceId)
        : quicClient;
      if (!client.isConnected) {
        throw new Error(`${runnerSelectionDevice?.name || "The selected machine"} is not connected.`);
      }
      const result = await client.installRunner(runnerId, {
        onProgress: (line) => {
          const trimmed = line.trim();
          if (!trimmed) return;
          setRunnerInstallState({ runnerId, kind: "installing", line: trimmed.slice(0, 160) });
        },
      });
      if (!result.ok) throw new Error(result.error || `${displayRunnerLabel(runnerId)} installation failed.`);
      await refreshRunnerState();
      setRunnerInstallState(null);
    } catch (error) {
      setRunnerInstallState({
        runnerId,
        kind: "failed",
        line: error instanceof Error ? error.message : String(error),
      });
    }
  }, [refreshRunnerState, runnerInstallState?.kind, runnerSelectionDevice, runnerSelectionDeviceId]);

  // Refresh runner + agent state on connect and keep retrying quickly until
  // the runner fetch is healthy. Once healthy, slow back down to background
  // polling so the banner stays honest without spamming the box.
  //
  // THE BANNER RE-RENDER STORM (user, 2026-07-26: "i really hate this opencode
  // etc super high frequency refresh at mobile ui at banner").
  //
  // This effect used to list `runnersFetchState` in its dependency array while
  // `refreshRunnerState` WROTE that same state. Every write tore the effect
  // down and re-ran it, which called refreshRunnerState again, which wrote
  // "loading" again… The `setInterval` never lived long enough to fire once, so
  // the intended "retry every 5s" became "retry as fast as the probe answers" —
  // and `getRunnersProbe()` answers `{state:"network-error"}` SYNCHRONOUSLY
  // when the transport is down while connectionStatus is still optimistically
  // "connected". No await, no throttle: the loop ran at render speed and the
  // banner alternated "OpenCode status loading" / "OpenCode status unavailable"
  // many times per second, taking the whole Tasks tree with it.
  //
  // Two changes, both load-bearing:
  //   1. `runnersFetchState` is GONE from the deps. The cadence is a policy we
  //      CALL (runnerPollCadenceMs, reading a ref) — not a subscription. A
  //      poller that restarts itself is not a poller.
  //   2. Self-scheduling timeout instead of setInterval: the next probe is
  //      queued only AFTER the previous one settles, so probes can never
  //      overlap and a synchronous failure still costs real wall-clock time.
  useEffect(() => {
    if (!runnerSelectionConnected && selectedTask?.runnerId !== "yaver-phone") {
      // A transport interruption says nothing about whether Codex/Claude/
      // OpenCode is still installed, authenticated, or addressable in its
      // persistent tmux seat. Keep the last-known inventory and explicit
      // runner/model choices mounted while reconnecting. Once this exact box
      // returns to the live pool, the effect re-probes it immediately.
      setRunnersFetchState("network-error");
      return;
    }
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const cycle = async () => {
      if (cancelled) return;
      await refreshRunnerState();
      if (cancelled) return;
      timer = setTimeout(cycle, runnerPollCadenceMs(runnersFetchStateRef.current));
    };
    void cycle();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [refreshRunnerState, runnerSelectionConnected, runnerSelectionDeviceId, selectedTask?.runnerId]);

  const openRunnerAuthModal = useCallback((runnerId: string, targetDeviceId?: string | null) => {
    const normalized = String(runnerId || "").trim().toLowerCase();
    if (normalized !== "claude" && normalized !== "codex") {
      Alert.alert("Sign-in unavailable", `${displayRunnerLabel(runnerId)} does not support browser sign-in from mobile yet.`);
      return;
    }
    // RunnerAuthModal is a sibling of the new-task wizard Modal and the
    // chat-detail Modal. React Native cannot reliably stack two visible
    // Modals on iOS — opening the auth modal while either is on screen
    // makes it render invisibly behind. Dismiss any open parent Modals
    // first, then open the auth modal on the next tick so RN has a frame
    // to play the dismiss animation.
    setShowNewTask(false);
    setSelectedTask(null);
    setTimeout(() => {
      setRunnerAuthModalRunner(normalized);
      setRunnerAuthModalTarget(targetDeviceId || null);
    }, 280);
  }, []);

  useEffect(() => {
    if (didApplyRouteSeedRef.current) return;
    if (!shouldOpenNew && !initialPrompt && !initialRunner) return;
    didApplyRouteSeedRef.current = true;
    if (shouldAutoSubmit) return;
    if (initialPrompt) setNewTaskText(initialPrompt);
    if (initialRunner) setSelectedRunner(initialRunner);
    setShowNewTask(true);
  }, [initialPrompt, initialRunner, shouldAutoSubmit, shouldOpenNew]);

  // Ping agent every 10s when connected
  useEffect(() => {
    if (connectionStatus !== "connected") {
      setPingRtt(null);
      return;
    }
    const doPing = async () => {
      const result = await quicClient.ping();
      if (result.ok) setPingRtt(result.rttMs);
      else setPingRtt(result.timedOut ? -1 : null);
    };
    doPing();
    const interval = setInterval(doPing, 10000);
    return () => clearInterval(interval);
  }, [connectionStatus]);

  // On-demand ping (like tailscale ping)
  const handlePing = async () => {
    setIsPinging(true);
    setShowPingResult(true);
    const result = await quicClient.ping();
    setPingResult({
      ok: result.ok,
      rttMs: result.rttMs,
      hostname: result.hostname,
      mode: connMode || undefined,
    });
    if (result.ok) setPingRtt(result.rttMs);
    setIsPinging(false);
  };

  // Restart runner from mobile
  const handleRestartRunner = async () => {
    setIsRestartingRunner(true);
    try {
      const ok = await quicClient.restartRunner();
      if (ok) {
        // Refresh status
        const s = await quicClient.getAgentStatus();
        if (s) setAgentStatus((prev) => (sameAgentStatus(prev, s) ? prev : s));
      } else {
        Alert.alert("Error", "Could not restart runner.");
      }
    } catch {
      Alert.alert("Error", "Failed to restart runner.");
    } finally {
      setIsRestartingRunner(false);
    }
  };

  // Fetch tasks
  const fetchTasks = useCallback(async () => {
    // Do not let the 3-second history poll compete with task admission. The
    // successful send path refreshes explicitly, and a failed send preserves
    // the prompt + renders its error in this composer.
    if (submitInFlightRef.current) return;
    try {
      const list = await connectionManager.runnerClient().listTasks();
      // Rows come from the runner box when a machine-role split is active —
      // stamp them with THAT box's identity, not the focused/render box's.
      const listDeviceId = connectionManager.roleDeviceId("runner") || quicClient.attachedDeviceId || activeDevice?.id || "";
      const focusedDeviceId = listDeviceId;
      const focusedDeviceName = devices.find((d) => d.id === focusedDeviceId)?.name || (connectionManager.roleDeviceId("runner") ? "" : activeDevice?.name) || "";
      // The owning agent is authoritative. Phone-local deletion tombstones
      // previously hid server rows even when Delete failed on another box.
      const filtered = list.filter((t) => t.source !== "vibing-cache");
      // Cap each task's output even on the initial fetch — a multi-day-old
      // task can come back from the agent with 100k+ lines of cached output,
      // which spikes JS heap on tab open.
      const capped = filtered.map((t) => {
        const output = t.output.length > MAX_OUTPUT_LINES_PER_TASK ? capOutput(t.output) : t.output;
        const deviceName = focusedDeviceName && (!t.deviceName || isTransportDeviceLabel(t.deviceName))
          ? focusedDeviceName
          : t.deviceName;
        const deviceId = t.deviceId || focusedDeviceId || undefined;
        return { ...t, output, deviceId, deviceName };
      });
      const pendingCloudTasks = (await listPendingCloudDispatches()).map(pendingCloudTaskPlaceholder);
      const nextTasks = [
        ...pendingCloudTasks,
        ...capped.filter((task) => !pendingCloudTasks.some((pending) => pending.id === task.id)),
      ];
      // The list endpoint STRIPS turns to bound its payload, so a fresh row
      // carries no history. Merging it verbatim onto an open task would wipe the
      // hydrated thread on every 3s poll (and hydration won't re-run — same id).
      // So preserve the richer in-memory turns whenever the fresh row lacks them.
      const keepTurns = (fresh: Task, old?: Task): Task =>
        old && (fresh.turns?.length ?? 0) === 0 && (old.turns?.length ?? 0) > 0
          ? { ...fresh, turns: old.turns, turnCount: old.turnCount ?? old.turns?.length }
          : fresh;
      startTransition(() => {
        setTasks((prev) => {
          const prevById = new Map(prev.map((t) => [t.id, t]));
          const reconciled = nextTasks.map((task) => {
            const current = prevById.get(task.id);
            const fresh = keepTurns(task, current);
            return current ? mergeTaskSnapshot(current, fresh) : fresh;
          });
          const merged = mergeFetchedTasks(
            prev,
            reconciled,
            listDeviceId,
          );
          void cacheTaskList(merged);
          return merged;
        });
        // Keep selected task in sync with latest data, but never let the stripped
        // list clobber the open thread's history.
        setSelectedTask((prev) => {
          if (!prev) return null;
          const fresh = nextTasks.find((t) => t.id === prev.id);
          return fresh ? mergeTaskSnapshot(prev, keepTurns(fresh, prev)) : prev;
        });
      });
    } catch {}
  }, [activeDevice?.id, activeDevice?.name, devices]);

  // Minimal cross-device lifecycle index. The Go agents publish only opaque
  // task/session ids + state; titles, prompts and output remain P2P. A five
  // minute foreground cadence is enough because local agent views continue to
  // use their direct task endpoint, and is 10x cheaper than the old 30s poll.
  const [agentTaskSnapshots, setAgentTaskSnapshots] = useState<AgentTaskSnapshot[]>([]);
  const refreshAgentTaskSnapshots = useCallback(async () => {
    if (!token) {
      setAgentTaskSnapshots([]);
      return;
    }
    try {
      setAgentTaskSnapshots(await listAgentTaskSnapshots());
    } catch {
      // Convex is optional. Keep direct/local task truth and do not turn a
      // cloud read failure into an empty authoritative snapshot.
    }
  }, [token]);
  useEffect(() => {
    void refreshAgentTaskSnapshots();
    const interval = setInterval(() => void refreshAgentTaskSnapshots(), 5 * 60_000);
    const appState = AppState.addEventListener("change", (state) => {
      if (state === "active") void refreshAgentTaskSnapshots();
    });
    return () => {
      clearInterval(interval);
      appState.remove();
    };
  }, [refreshAgentTaskSnapshots]);
  useEffect(() => {
    if (agentTaskSnapshots.length === 0) return;
    setTasks((previous) => {
      const next = reconcileTasksWithAgentSnapshots(previous, agentTaskSnapshots);
      void cacheTaskList(next);
      return next;
    });
  }, [agentTaskSnapshots]);

  // A task-review notification belongs to one runner conversation. The
  // notification handler supplies its stable task id (and, when known, the
  // runner device), then this screen resolves the row from the latest list or
  // asks that exact box. Do not use the current filter or a generic Review
  // list: either can land the user in somebody else's most-recent task.
  const openedNotificationTaskRef = useRef<string | null>(null);
  useEffect(() => {
    if (!notificationTaskId) return;
    const targetKey = `${notificationTaskId}:${notificationTaskNonce || "route"}`;
    if (openedNotificationTaskRef.current === targetKey) return;
    let cancelled = false;
    const open = async () => {
      const targetDevice = notificationTaskDeviceId
        ? devices.find((device) => device.id === notificationTaskDeviceId)
        : undefined;
      // Switch the focused box when the payload knows it. This makes follow-up,
      // hydration and runner controls stay attached to the task's own session.
      if (targetDevice && activeDevice?.id !== targetDevice.id) {
        try { await selectDevice(targetDevice); } catch {}
      }

      let task = tasks.find((candidate) => candidate.id === notificationTaskId) || null;
      if (!task) {
        try {
          const client = notificationTaskDeviceId
            ? connectionManager.clientFor(notificationTaskDeviceId)
            : connectionManager.runnerClient();
          task = await client.getTask(notificationTaskId);
          if (task && notificationTaskDeviceId && !task.deviceId) {
            task = { ...task, deviceId: notificationTaskDeviceId, deviceName: targetDevice?.name || task.deviceName };
          }
        } catch {
          // The normal poll will retry resolution as soon as the target box
          // reconnects. Crucially, we do not consume the notification as a
          // generic Tasks open while the exact target remains unavailable.
          return;
        }
      }
      if (cancelled || !task) return;
      openedNotificationTaskRef.current = targetKey;
      setTasks((previous) => previous.some((candidate) => candidate.id === task!.id)
        ? previous
        : [task!, ...previous]);
      setSelectedTask(task);
    };
    void open();
    return () => { cancelled = true; };
  }, [
    activeDevice?.id,
    devices,
    notificationTaskDeviceId,
    notificationTaskId,
    notificationTaskNonce,
    selectDevice,
    tasks,
  ]);

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    const run = async () => {
      const pending = await listPendingCloudDispatches();
      if (pending.length === 0) return;
      let rows = pending;
      try {
        rows = await mergePendingCloudDispatchIntents(await listTaskDispatchIntents({ limit: 80 }));
      } catch {
        rows = pending;
      }
      const placeholders = rows.map(pendingCloudTaskPlaceholder);
      setTasks((prev) => [
        ...placeholders,
        ...prev.filter((task) => !placeholders.some((pendingTask) => pendingTask.id === task.id)),
      ]);
      for (const row of rows) {
        if (cancelled || pendingCloudDispatchRef.current.has(row.localTaskId)) continue;
        let currentRow = row;
        if (currentRow.placementId) {
          try {
            currentRow = mergePendingCloudPlacementStatus(
              currentRow,
              await getTaskPlacementStatus({ placementId: currentRow.placementId }),
            );
            await updatePendingCloudDispatch(currentRow.localTaskId, currentRow);
            setTasks((prev) => prev.map((task) =>
              task.id === currentRow.localTaskId ? pendingCloudTaskPlaceholder(currentRow) : task,
            ));
          } catch {
            /* placement status is advisory; dispatch intents remain authoritative */
          }
        }
        if (pendingCloudDispatchNeedsUserAction(currentRow)) continue;
        const targetDeviceId = currentRow.targetDeviceId || undefined;
        if (!targetDeviceId || !connectedDeviceIds.includes(targetDeviceId)) continue;
        const targetClient = connectionManager.clientFor(targetDeviceId);
        if (!targetClient.isConnected) continue;
        pendingCloudDispatchRef.current.add(currentRow.localTaskId);
        try {
          await updateTaskDispatchIntent({
            intentId: currentRow.dispatchIntentId,
            localTaskId: currentRow.localTaskId,
            status: "dispatching",
            targetDeviceId,
            clearBlockedAction: currentRow.clearedBlockedAction === true,
          }).catch(() => undefined);
          const task = await targetClient.sendTask(
            currentRow.params.title,
            currentRow.params.description,
            currentRow.params.model,
            currentRow.params.runner,
            currentRow.params.customCommand,
            currentRow.params.speechContext,
            currentRow.params.images,
            currentRow.params.workDir,
            currentRow.params.mode,
            currentRow.params.video,
            currentRow.params.codeMode,
            true,
            currentRow.params.projectName,
            currentRow.params.mcpServers,
            currentRow.params.goal,
            currentRow.params.includeYaverMcp,
          );
          if (currentRow.placementId) {
            await rebindTaskPlacement(currentRow.placementId, task.id, "running").catch(() => undefined);
          }
          await updateTaskDispatchIntent({
            intentId: currentRow.dispatchIntentId,
            localTaskId: currentRow.localTaskId,
            status: "dispatched",
            taskId: task.id,
            targetDeviceId,
          }).catch(() => undefined);
          await removePendingCloudDispatch(currentRow.localTaskId);
          const nextTask = {
            ...task,
            deviceId: targetDeviceId,
            deviceName: devices.find((device) => device.id === targetDeviceId)?.name || task.deviceName,
            placementId: currentRow.placementId,
            placementLane: currentRow.placementLane,
            placementReason: currentRow.placementReason,
            placementCreditLabel: currentRow.placementCreditLabel,
          };
          setTasks((prev) => [
            nextTask,
            ...prev.filter((item) => item.id !== currentRow.localTaskId && item.id !== task.id),
          ]);
          setSelectedTask((current) => current?.id === currentRow.localTaskId ? nextTask : current);
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          await updateTaskDispatchIntent({
            intentId: currentRow.dispatchIntentId,
            localTaskId: currentRow.localTaskId,
            status: "failed",
            lastError: message,
            bumpAttempt: true,
          }).catch(() => undefined);
          await updatePendingCloudDispatch(currentRow.localTaskId, {
            attempts: currentRow.attempts + 1,
            lastError: message,
          });
        } finally {
          pendingCloudDispatchRef.current.delete(currentRow.localTaskId);
        }
      }
    };
    void run();
    const id = setInterval(() => void run(), 5000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [connectedDeviceIds, devices, token]);

  const handlePendingCloudBlockedAction = useCallback(async (task: Task) => {
    const action = task.pendingCloudBlockedAction;
    if (action === "runner_auth_required") {
      openRunnerAuthModal(task.runnerId || "codex", task.pendingCloudTargetDeviceId || task.deviceId || null);
      return;
    }
    if (action === "yaver_auth_required" || action === "billing_required") {
      await Linking.openURL("https://yaver.io").catch(() => {
        Alert.alert("Open Yaver web", "Go to https://yaver.io from a browser to finish account setup.");
      });
      return;
    }
    if (action === "resize_required" || action === "resize_failed" || action === "wake_failed") {
      if (!task.placementId) {
        Alert.alert("Retry unavailable", "This saved task does not have a placement id.");
        return;
      }
      try {
        const activation = await activateTaskPlacement({ placementId: task.placementId });
        const blockedReason = activationBlockReason(activation);
        await updatePendingCloudDispatch(task.id, {
          dispatchStatus: blockedReason ? "blocked" : "queued",
          blockedAction: blockedReason ? activation.action : undefined,
          blockedReason: blockedReason || undefined,
          clearedBlockedAction: !blockedReason,
          updatedAt: Date.now(),
        });
        await updateTaskDispatchIntent({
          localTaskId: task.id,
          status: blockedReason ? "blocked" : "dispatching",
          blockedAction: blockedReason ? activation.action : undefined,
          reason: blockedReason || undefined,
          clearBlockedAction: !blockedReason,
        }).catch(() => undefined);
        await fetchTasks();
      } catch (err: any) {
        Alert.alert("Retry failed", err?.message || "The remote machine is still not ready.");
      }
      return;
    }
    Alert.alert("Needs attention", task.pendingCloudBlockedReason || "This task is waiting for the selected remote machine.");
  }, [fetchTasks, openRunnerAuthModal]);

  const hasRunningTask = tasks.some(t => t.status === "running" || t.status === "queued");
  const effectiveFilter = statusFilter;
  const displayTasks = effectiveFilter === "all" ? tasks
    : effectiveFilter === "running" ? tasks.filter(t => t.status === "running" || t.status === "queued")
    : effectiveFilter === "review" ? tasks.filter(t => t.status === "review" || t.status === "ready")
    : effectiveFilter === "completed" ? tasks.filter(t => t.status === "completed")
    : tasks.filter(t => t.status === "failed" || t.status === "stopped");
  // Paint the last-known task list instantly from cache on cold start, so the
  // screen is never empty while the first network fetch is in flight. Only fills
  // when we have nothing yet — never stomps a live list.
  const cachePaintedRef = useRef(false);
  useEffect(() => {
    if (cachePaintedRef.current) return;
    cachePaintedRef.current = true;
    (async () => {
      try {
        await recoverInterruptedRemotelessTasks();
        const [cached, lifecycle] = await Promise.all([getCachedTaskList(), listRemotelessTasks()]);
        const reviewById = new Map(
          lifecycle
            .filter((record) => record.state === "review")
            .map((record) => [record.id, record.detail || "Review the working tree, then retry."]),
        );
        const reconciled = cached.map((task) => {
          const detail = reviewById.get(task.id);
          return detail && task.status === "running" && task.source === "phone-local"
            ? { ...task, status: "review" as TaskStatus, resultText: detail, output: [...task.output, detail], updatedAt: Date.now() }
            : task;
        });
        if (reconciled.length > 0) {
          void cacheTaskList(reconciled);
          setTasks((prev) => (prev.length === 0 ? reconciled : prev));
        }
      } catch { /* no cache — the fetch below fills it */ }
    })();
  }, []);
  useEffect(() => {
    fetchTasks();
    // Poll less frequently when a task is running (streaming handles live output)
    const interval = setInterval(fetchTasks, hasRunningTask ? 10000 : 3000);
    return () => clearInterval(interval);
  }, [fetchTasks, hasRunningTask]);

  const pendingRuntimeRenderRef = useRef<{ taskId: string; yaverSessionId?: string; source: string; workDir?: string; projectName?: string; explicit?: boolean } | null>(null);
  const namedReloadExecutorRef = useRef<((projectName: string) => Promise<void>) | null>(null);

  // ── raw-lane (live console) ─────────────────────────────────────────
  // Every runner (opencode, codex, claude, …) streams its RAW runner
  // stdout (ANSI + TUI intact) as `raw`/`raw_replay` SSE frames (agent
  // 1.99.406+, commit d671b7c02) — see agent tasks.go emitRaw. The app
  // is chat-first, but the raw bytes are consumed so the task detail
  // shows a LIVE console section (AnsiConsoleText) with the same
  // colours/status as the runner's console — summarized on mobile so
  // megabytes of tool noise don't eat the screen (summarizeRawConsole).
  // Buffer discipline (per selected task, survives status changes):
  //   rawBufRef     — retained tail of raw bytes (cap ~512KB, mirrors the
  //                   agent's rawOutputMaxBytes)
  //   rawCursorRef  — the agent's authoritative byte cursor, passed back as
  //                   `rawSince` on reattach to resume without gaps
  //   rawLive       — true for ~3s after the last LIVE raw frame (drives
  //                   the Live/Idle dot on the console section header)
  const RAW_CONSOLE_CAP = 512 * 1024;
  const rawBufRef = useRef("");
  const rawCursorRef = useRef(0);
  const [rawSnapshot, setRawSnapshot] = useState("");
  const [rawLastAt, setRawLastAt] = useState<number | null>(null);
  const [rawLive, setRawLive] = useState(false);
  const rawLiveRef = useRef(false);
  const rawLiveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const markRawLive = useCallback(() => {
    if (!rawLiveRef.current) {
      rawLiveRef.current = true;
      setRawLive(true);
    }
    if (rawLiveTimerRef.current) clearTimeout(rawLiveTimerRef.current);
    rawLiveTimerRef.current = setTimeout(() => {
      rawLiveRef.current = false;
      setRawLive(false);
    }, 3000);
  }, []);
  // Full-snapshot reset is keyed on the SELECTED TASK (not the raw lane):
  // a fresh `raw_replay` with full=true replaces the buffer; live `raw`
  // frames append. Tracked by task id so switching tasks never leaks one
  // task's console into another's.
  const rawTaskIdRef = useRef<string | null>(null);
  const rawRenderTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const publishRawSnapshot = useCallback((immediate: boolean) => {
    if (rawRenderTimerRef.current) {
      if (!immediate) return;
      clearTimeout(rawRenderTimerRef.current);
      rawRenderTimerRef.current = null;
    }
    const publish = () => {
      rawRenderTimerRef.current = null;
      const next = rawBufRef.current;
      startTransition(() => setRawSnapshot(next));
    };
    if (immediate) publish();
    else rawRenderTimerRef.current = setTimeout(publish, RAW_CONSOLE_RENDER_MS);
  }, []);
  const handleRawChunk = useCallback((text: string, offset: number, full: boolean) => {
    if (full) {
      rawBufRef.current = text;
    } else {
      rawBufRef.current = (rawBufRef.current + text).slice(-RAW_CONSOLE_CAP);
    }
    if (typeof offset === "number" && offset > 0) rawCursorRef.current = offset;
    if (!full) {
      markRawLive(); // live bytes → the console section's Live dot
      setRawLastAt(Date.now());
    }
    publishRawSnapshot(full);
  }, [RAW_CONSOLE_CAP, markRawLive, publishRawSnapshot]);
  useEffect(() => () => {
    if (rawRenderTimerRef.current) clearTimeout(rawRenderTimerRef.current);
    if (rawLiveTimerRef.current) clearTimeout(rawLiveTimerRef.current);
  }, []);

  // SSE stream for the selected running task (full live terminal stream)
  const sseAbortRef = useRef<(() => void) | null>(null);
  // Health of that stream, rendered above the composer. A stream that drops
  // mid-render used to end in an EMPTY `xhr.onerror` handler — the transcript
  // stopped growing and nothing told the user whether the task had finished,
  // the box had died, or the relay had bounced. See lib/taskStreamRecovery.ts.
  const [streamHealth, setStreamHealth] = useState<{
    kind: "reattaching" | "lost";
    message: string;
  } | null>(null);
  // Bumping this re-runs the stream effect — the "Reattach" route.
  const [streamReattachNonce, setStreamReattachNonce] = useState(0);
  // Why the preview did NOT refresh when the turn landed. The whole point is
  // that this is never empty-and-silent: a queued render that then doesn't
  // happen is indistinguishable from a broken product unless it says why.
  const [renderSkipNotice, setRenderSkipNotice] = useState<string | null>(null);
  const [renderReady, setRenderReady] = useState(false);
  useEffect(() => {
    // Cleanup previous SSE
    if (sseAbortRef.current) {
      sseAbortRef.current();
      sseAbortRef.current = null;
    }
    if (!selectedTask || !taskStatusMeansRunnerIsCoding(selectedTask.status)) {
      // The stream-health banner describes a live coding stream. Keeping it
      // after the task reaches review/completed is a false signal: the task is
      // no longer running and there is nothing left to reattach to.
      setStreamHealth(null);
      return;
    }
    if (!quicClient.isConnected) {
      // An unreachable box is a legitimate reason to have no live stream —
      // but it must not read as "the task went quiet". DeviceContext owns the
      // reconnect narration; we only make sure we are not ALSO claiming health.
      setStreamHealth(null);
      return;
    }

    // Bytes of transcript received from the STREAM. This is the offset the
    // agent resumes from (`?since=`), so a reattach after a dropped tunnel
    // replays only what we missed instead of duplicating the scrollback.
    let received = 0;
    let attempt = 0;
    let disposed = false;
    let taskFinished = false;
    let reattachTimer: ReturnType<typeof setTimeout> | undefined;
    setStreamHealth(null);

    const subscribe = (since: number, rawSince: number) => {
    if (disposed) return;
    const abort = connectionManager.runnerClient().streamTaskOutput(
      selectedTask.id,
      (text, offset) => {
        // Prefer the agent's authoritative byte cursor. Counting here means
        // counting UTF-16 code units, and `?since=` is sliced in BYTES — the
        // two agree only for ASCII, and a runner transcript is full of
        // box-drawing runes and "…". The local count stays as the fallback for
        // agents older than the `offset` field.
        if (typeof offset === "number") received = offset;
        else received += text.length;
        // Output is flowing again — drop any interruption banner and reset
        // the ladder so the next outage gets a full set of attempts.
        attempt = 0;
        setStreamHealth(null);
        // `output` is the compatibility/evidence lane. Human UI updates come
        // exclusively from the versioned presentation events below; raw bytes
        // use the separate folded console lane. Do not copy terminal text into
        // React state or infer its meaning on the phone.
      },
      (status) => {
        if (status === "ready" || status === "completed" || status === "review" || status === "failed" || status === "stopped") {
          taskFinished = true;
          setStreamHealth(null);
          setTasks((prev) => prev.map((t) => t.id === selectedTask.id ? { ...t, status: status as TaskStatus } : t));
          setSelectedTask((prev) => prev?.id === selectedTask.id ? { ...prev, status: status as TaskStatus } : prev);
        }
        // Task finished via SSE — refresh to get final state. Also
        // close any open agent_question sheet: a finished task
        // cannot consume an answer, and the daemon already cancelled
        // the registry entry on stop.
        setAgentQuestion(null);
        fetchTasks();
      },
      (evt) => {
        // Structured non-text events. The daemon emits agent_question
        // when the runner calls yaver_ask_user, agent_answered when
        // any device on the same account answers, and
        // agent_question_cancelled on timeout / task stop.
        if (!evt || typeof evt.type !== "string") return;
        // `resume.full` means the box's transcript is SHORTER than ours
        // (task re-created / output reset), so what follows is a snapshot
        // to replace with rather than an increment to append.
        if (evt.type === "resume") {
          if (evt.full === true) {
            received = 0;
            const tid = selectedTask.id;
            setTasks((prev) => prev.map((t) => (t.id === tid ? { ...t, output: [] } : t)));
            setSelectedTask((prev) => (prev?.id === tid ? { ...prev, output: [] } : prev));
          }
          return;
        }
        if (isTaskPresentationEvent(evt)) {
          const tid = selectedTask.id;
          const apply = (task: Task): Task => task.id === tid
            ? { ...task, presentation: reduceTaskPresentation(task.presentation ?? [], evt) }
            : task;
          setTasks((prev) => prev.map(apply));
          setSelectedTask((prev) => prev ? apply(prev) : prev);
          return;
        }
        // Structured shell-command events → fold into per-task card
        // models for the foldable Commands section. P2P only.
        if (isCommandEvent(evt)) {
          const tid = selectedTask.id;
          setCmdCardsByTask((prev) => ({
            ...prev,
            [tid]: reduceCommandEvent(prev[tid] || {}, evt),
          }));
          return;
        }
        if (evt.type === "runtime_render_requested") {
          pendingRuntimeRenderRef.current = {
            taskId: selectedTask.id,
            yaverSessionId: typeof evt.yaverSessionId === "string" ? evt.yaverSessionId : undefined,
            source: `mobile-task-finished-${String(evt.reason || "render")}`,
            workDir: typeof evt.workDir === "string" ? evt.workDir : undefined,
          };
          return;
        }
        if (evt.type === "agent_question" && evt.question) {
          const q = evt.question as {
            id: string;
            taskId: string;
            prompt: string;
            header?: string;
            kind: "text" | "choice" | "secret";
            choices?: string[];
            multi?: boolean;
            vaultHint?: string;
            screenshot?: string; // F3 handoff
            step?: string;       // F3 handoff
          };
          setAgentQuestion(q);
          setAgentAnswerText("");
          setAgentMultiPicks([]);
          setAgentOtherOpen(false);
        } else if (evt.type === "agent_answered" || evt.type === "agent_question_cancelled") {
          const qid = (evt as { questionId?: string }).questionId;
          setAgentQuestion((cur) => (cur && (!qid || cur.id === qid) ? null : cur));
        }
      },
      {
        since,
        // Resume the RAW stdout lane from the same cursor the last stream
        // reached, so the live console never repaints or gaps across a drop.
        // Per-task reset: switching the selected task reseeds from a full
        // `raw_replay` snapshot (rawSince=0); status changes keep the buffer.
        rawSince,
        onRaw: (text, offset, full) => {
          handleRawChunk(text, offset, full);
          // Raw bytes are output too — they prove the runner is alive.
          attempt = 0;
          setStreamHealth(null);
        },
        onEnd: (info) => {
          if (disposed || taskFinished) return;
          // The transport used to end here in silence (`xhr.onerror` was an
          // empty handler), so a relay bounce or a dropped tunnel simply
          // stopped the transcript and the screen sat on its last frame.
          const plan = planStreamRecovery({
            end: classifyStreamEnd(info),
            attempt,
            cause: info.error,
          });
          if (plan.action === "idle") {
            setStreamHealth(null);
            return;
          }
          if (plan.action === "give-up") {
            setStreamHealth({ kind: "lost", message: plan.message });
            return;
          }
          setStreamHealth({ kind: "reattaching", message: plan.message });
          attempt += 1;
          reattachTimer = setTimeout(() => subscribe(received, rawCursorRef.current), plan.delayMs);
        },
      },
    );
    sseAbortRef.current = abort;
    };

    // Per-task raw console reset: switching the selected task clears the
    // buffer so the console reseeds from a full `raw_replay` snapshot
    // (rawSince=0). Status changes (queued→running→completed) keep the
    // buffer so the console resumes with `rawSince=<cursor>` instead of
    // repainting.
    if (rawTaskIdRef.current !== selectedTask.id) {
      rawTaskIdRef.current = selectedTask.id;
      rawCursorRef.current = 0;
      rawBufRef.current = "";
      setRawSnapshot("");
      setRawLastAt(null);
      rawLiveRef.current = false;
      setRawLive(false);
    }

    subscribe(0, rawCursorRef.current);

    // Late-join replay: if the agent already asked while no client
    // was subscribed, the SSE writer will replay on connect. But the
    // streamTaskOutput callback fires asynchronously; for the
    // currently-selected task we also poll once so the sheet shows
    // immediately on tap-into-task without waiting for the next
    // server-buffered SSE flush.
    // Cancellation guard: this promise outlives the effect. Without it,
    // closing the task (or switching to another) before the fetch
    // resolves still mounts the question sheet — over the task LIST,
    // for a task that is no longer selected. Every path that clears
    // agentQuestion is keyed on selectedTask?.id, so once that's null
    // the sheet can never be cleared again: a permanently stuck sheet.
    let cancelled = false;
    void quicClient.getPendingTaskQuestion(selectedTask.id).then((q) => {
      if (cancelled) return;
      if (q && q.taskId === selectedTask.id) {
        setAgentQuestion(q);
        setAgentAnswerText("");
        setAgentMultiPicks([]);
        setAgentOtherOpen(false);
      }
    });

    return () => {
      cancelled = true;
      disposed = true;
      if (reattachTimer) clearTimeout(reattachTimer);
      sseAbortRef.current?.();
      sseAbortRef.current = null;
    };
  }, [selectedTask?.id, selectedTask?.status, streamReattachNonce, handleRawChunk]);

  // Raw console seed for tasks that never stream. A FINISHED opencode task
  // has no live SSE (the effect above only subscribes while the runner is
  // coding), so opening one subscribes once with `rawSince=0`, drains the
  // raw_replay snapshot into the buffer, then aborts. Re-fetches when the
  // task/status changes so the console tail is always authoritative.
  useEffect(() => {
    if (!selectedTask) return;
    if (taskStatusMeansRunnerIsCoding(selectedTask.status)) return; // live stream owns the raw lane
    if (normalizeTaskRunnerId(selectedTask.runnerId) !== "opencode") return;
    if (!quicClient.isConnected) return;
    if (rawBufRef.current && rawTaskIdRef.current === selectedTask.id) return; // already seeded
    let disposed = false;
    let seeded = false;
    let abort: () => void = () => {};
    rawTaskIdRef.current = selectedTask.id;
    rawCursorRef.current = 0;
    rawBufRef.current = "";
    setRawSnapshot("");
    setRawLastAt(null);
    rawLiveRef.current = false;
    setRawLive(false);
    abort = connectionManager.runnerClient().streamTaskOutput(
      selectedTask.id,
      () => { /* chat interest only — the live effect owns chat for coding tasks */ },
      undefined,
      undefined,
      {
        rawSince: 0,
        onRaw: (text, offset, full) => {
          if (disposed || seeded) return;
          seeded = true;
          handleRawChunk(text, offset, full);
          // One-shot: the finished task's raw_replay IS the whole retained
          // tail — close the stream, the console has everything.
          abort();
        },
      },
    );
    return () => {
      disposed = true;
      abort();
    };
  }, [selectedTask?.id, selectedTask?.status, handleRawChunk]);

  // The queued render intent lands here, once, when the turn reaches a
  // renderable terminal state.
  //
  // This used to call rerenderActiveRemoteRuntimeSurface() directly, so it
  // refreshed the WebRTC lane and ONLY the WebRTC lane — a browser-lane
  // preview (the Yaver-on-Yaver route) never updated, and the failure was a
  // bare `false` with no log and nothing on screen. Now the lane routing and
  // the "why not" live in planPostTaskRender(), and a skip is shown rather
  // than swallowed.
  useEffect(() => {
    if (!selectedTask || !taskStatusAllowsRuntimeRender(selectedTask.status)) return;
    if ((selectedTask.pendingFollowUps?.length ?? 0) > 0) return;
    const pending = pendingRuntimeRenderRef.current;
    if (!pending || pending.taskId !== selectedTask.id) return;
    if (pending.yaverSessionId && pending.yaverSessionId !== selectedTask.executionSession?.yaverSessionId) return;
    pendingRuntimeRenderRef.current = null;
    void (async () => {
      if (pending.projectName) {
        await namedReloadExecutorRef.current?.(pending.projectName);
        return;
      }
      const decision = await rerenderActivePreviewSurface({
        source: pending.source,
        workDir: pending.workDir,
        taskStatus: selectedTask.status,
        autoRenderEnabled: pending.explicit === true || autoRenderVibing,
      });
      // "no preview open" is the ordinary case for someone who never opened
      // one — not worth a banner. Everything else is a render the user had
      // reason to expect and did not get, so it must say so.
      if (decision.action === "offer") {
        setRenderReady(true);
        setRenderSkipNotice(decision.message);
      } else if (decision.action === "skip" && decision.reason !== "no-active-surface") {
        setRenderReady(false);
        setRenderSkipNotice(decision.message);
      } else {
        setRenderReady(false);
        setRenderSkipNotice(null);
      }
    })();
  }, [autoRenderVibing, selectedTask?.id, selectedTask?.pendingFollowUps?.length, selectedTask?.status]);

  // Second half of the same guard: a question that arrived over SSE for
  // a task you have since closed would otherwise linger with no owner
  // to clear it. Deselecting the task drops the sheet with it.
  useEffect(() => {
    if (!agentQuestion) return;
    if (selectedTask && agentQuestion.taskId === selectedTask.id) return;
    setAgentQuestion(null);
  }, [agentQuestion, selectedTask]);

  // Single submit path for the agent-question sheet — shared by the
  // per-choice tap, the multi-select "Send", the "Other…" free text,
  // and the text/secret kinds. Keeps the POST + error + close logic
  // in one place so the four entry points can't drift apart.
  const submitAgentAnswer = useCallback(
    async (answer: string) => {
      if (!agentQuestion || !answer.trim()) return;
      setSubmittingAgentAnswer(true);
      const res = await quicClient.answerTaskQuestion(agentQuestion.taskId, agentQuestion.id, answer);
      setSubmittingAgentAnswer(false);
      if (!res.ok) {
        Alert.alert("Could not deliver answer", res.error || "Unknown error");
        return;
      }
      setAgentQuestion(null);
      setAgentAnswerText("");
      setAgentMultiPicks([]);
      setAgentOtherOpen(false);
    },
    [agentQuestion],
  );

  // Idle detection: if task is "running" but no new output for 20s, re-fetch status.
  // This catches the case where the agent finishes but the status update was missed.
  const lastOutputTimeRef = useRef<number>(Date.now());
  useEffect(() => {
    lastOutputTimeRef.current = Date.now();
  }, [selectedTask?.output.length]);

  // Tracks which task id we've already hydrated full turns for, so neither the
  // running-poll refresh nor the open-hydration effect re-fetches on every tick.
  const hydratedTurnsForRef = useRef<string | null>(null);

  useEffect(() => {
    if (!selectedTask || selectedTask.status !== "running") return;
    const interval = setInterval(async () => {
      const idleMs = Date.now() - lastOutputTimeRef.current;
      if (idleMs > 20000) {
        // Agent has been silent for 20s — force refresh task status
        const fresh = await quicClient.getTask(selectedTask.id);
        if (fresh && fresh.status !== "running") {
          const capped = fresh.output.length > MAX_OUTPUT_LINES_PER_TASK
            ? { ...fresh, output: capOutput(fresh.output) }
            : fresh;
          setSelectedTask(capped);
          setTasks(prev => prev.map(t => t.id === capped.id ? capped : t));
          hydratedTurnsForRef.current = capped.id;
          if ((capped.turns?.length ?? 0) > 0) void cacheTaskTurns(capped.id, capped.turns as unknown[]);
        }
      }
    }, 5000);
    return () => clearInterval(interval);
  }, [selectedTask?.id, selectedTask?.status]);

  // Follow semantic conversation updates, not the deprecated transcript
  // buffer. Presentation is the human message lane now; watching only
  // output/result/status meant a live agent reply could appear above queued
  // follow-ups without moving the conversation at all.
  useEffect(() => {
    chatShouldFollowRef.current = true;
  }, [selectedTask?.id]);

  useEffect(() => {
    if (!selectedTask || !chatShouldFollowRef.current) return;
    if (chatFollowTimerRef.current) clearTimeout(chatFollowTimerRef.current);
    chatFollowTimerRef.current = setTimeout(() => {
      chatFollowTimerRef.current = null;
      chatScrollRef.current?.scrollToEnd({ animated: true });
    }, 120);
    return () => {
      if (chatFollowTimerRef.current) clearTimeout(chatFollowTimerRef.current);
      chatFollowTimerRef.current = null;
    };
  }, [selectedTask?.id, selectedTask?.presentation, selectedTask?.resultText, selectedTask?.status]);

  // Open-task intents from RunningTasksPill (rendered in the root
  // layout). The pill navigates to /tasks then publishes the id; we
  // resolve it against the current list and fall back to a one-shot
  // getTask fetch if the polling cycle hasn't caught it yet.
  useEffect(() => {
    return openTaskBus.subscribe(async (taskId) => {
      const found = tasks.find((t) => t.id === taskId);
      if (found) { setSelectedTask(found); return; }
      try {
        const fresh = await quicClient.getTask(taskId);
        if (fresh) {
          setTasks((prev) => prev.some((t) => t.id === fresh.id) ? prev : [fresh, ...prev]);
          setSelectedTask(fresh);
        }
      } catch { /* drop intent silently — pill will retry next tap */ }
    });
  }, [tasks]);

  // Hydrate the FULL conversation when a task is opened. The list endpoint
  // strips Turns to keep its payload small (agent httpserver.go nils Turns +
  // TurnCount), so a row tapped straight from the list arrives with NO history
  // and buildChatMessages falls back to "title + last result" — one exchange.
  // That is why the WhatsApp thread appeared right after a fork (turns carried
  // in memory) but vanished on re-entry from the list. Fix: on open, if the
  // selected task has no turns but the server says it has some (TurnCount > 0),
  // fetch the detail ONCE and cache it in selectedTask memory. Lightweight list
  // + lazy full-detail fetch is the optimal shape — we never re-ship history on
  // the 3s/10s list poll, only once per open.
  useEffect(() => {
    const t = selectedTask;
    if (!t) return;
    // Local yaver-agent tasks live only in memory — getTask would 404 and wipe
    // the live turns. They already carry their full turns, so never refetch.
    if (t.runnerId === "yaver-agent" || t.id.startsWith("yaver-agent-")) return;
    // Already have the thread in memory (fork-carried or previously hydrated).
    if ((t.turns?.length ?? 0) > 0) { hydratedTurnsForRef.current = t.id; void cacheTaskTurns(t.id, t.turns as unknown[]); return; }
    // Nothing to hydrate: the server itself has no prior turns for this task.
    if ((t.turnCount ?? 0) === 0) return;
    if (hydratedTurnsForRef.current === t.id) return;
    const taskId = t.id;
    let cancelled = false;
    (async () => {
      // 1) INSTANT: paint cached turns first so re-opening a thread never shows
      //    an empty/one-line chat while the detail fetch is in flight. The list
      //    strips turns, so without this the WhatsApp thread flickers on every
      //    open. Only applies if we haven't already filled turns from memory.
      try {
        const cached = await getCachedTaskTurns(taskId);
        if (!cancelled && cached && cached.length > 0) {
          setSelectedTask((prev) =>
            prev && prev.id === taskId && (prev.turns?.length ?? 0) === 0
              ? { ...prev, turns: cached as Task["turns"] }
              : prev,
          );
        }
      } catch { /* cache miss is fine — the fetch below is authoritative */ }
      // 2) AUTHORITATIVE: fetch the full detail and reconcile. Server wins over
      //    cache (no stale/missing data), and we refresh the cache for next time.
      try {
        // Detail hydration must go back to the runner that owns this task.
        // A notification can intentionally open a task from a non-focused box;
        // querying the focused singleton here made that exact review thread
        // look empty even after navigation succeeded.
        const detailClient = t.deviceId ? connectionManager.clientFor(t.deviceId) : quicClient;
        const full = await detailClient.getTask(taskId);
        if (cancelled || !full || (full.turns?.length ?? 0) === 0) return;
        hydratedTurnsForRef.current = taskId;
        const capped = full.output.length > MAX_OUTPUT_LINES_PER_TASK
          ? { ...full, output: capOutput(full.output) }
          : full;
        setSelectedTask((prev) => (prev && prev.id === taskId ? capped : prev));
        setTasks((prev) => prev.map((x) => (x.id === capped.id ? { ...x, turns: capped.turns, turnCount: capped.turns?.length ?? x.turnCount } : x)));
        void cacheTaskTurns(taskId, capped.turns as unknown[]);
      } catch { /* offline: keep the cached turns we painted in step 1 */ }
    })();
    return () => { cancelled = true; };
  }, [selectedTask?.id]);

  // TTS: speak the final result when task completes
  const lastSpokenTaskRef = useRef<string | null>(null);
  useEffect(() => {
    const spokenResult = friendlyTaskPresentation(selectedTask?.presentation)
      .filter((item) => item.kind === "message" && item.role === "assistant")
      .at(-1)?.text || selectedTask?.resultText;
    if (ttsEnabled && selectedTask?.status === "completed" && spokenResult && lastSpokenTaskRef.current !== selectedTask.id) {
      lastSpokenTaskRef.current = selectedTask.id;
      speakTaskResult(spokenResult);
    }
  }, [selectedTask?.status, selectedTask?.resultText, selectedTask?.presentation, ttsEnabled, ttsProvider, speechApiKey]);

  // Haptic notification on task transition: fire success on
  // completed, error on failed. Single ref tracks the last status
  // we already handled per task id so we don't re-fire on every
  // re-render. See spec X1.
  const lastHapticTaskStatusRef = useRef<{ id: string; status: TaskStatus } | null>(null);
  useEffect(() => {
    if (!selectedTask) return;
    const prev = lastHapticTaskStatusRef.current;
    const newKey = { id: selectedTask.id, status: selectedTask.status };
    if (prev?.id === newKey.id && prev.status === newKey.status) return;
    if (prev?.id === newKey.id) {
      // Same task, status changed — fire transition haptic. Skip on
      // queued/running (those don't need a haptic), only on terminal
      // states.
      if (newKey.status === "completed") taskHaptics.taskCompleted();
      else if (newKey.status === "failed") taskHaptics.taskFailed();
    }
    lastHapticTaskStatusRef.current = newKey;
  }, [selectedTask?.id, selectedTask?.status]);

  // Task-transition notifications are edge-triggered. Seed from the current
  // list on first load so reopening Tasks does not replay old review alerts.
  const taskTransitionStatusRef = useRef<Map<string, TaskStatus>>(new Map());
  useEffect(() => {
    const known = taskTransitionStatusRef.current;
    if (known.size === 0) {
      for (const t of tasks) known.set(scopedTaskIdentity(t.deviceId, t.id), t.status);
      return;
    }
    const seen = new Set<string>();
    for (const t of tasks) {
      const taskKey = scopedTaskIdentity(t.deviceId, t.id);
      seen.add(taskKey);
      const prev = known.get(taskKey);
      if (prev === t.status) continue;
      known.set(taskKey, t.status);
      const title = t.title || "Coding task";
      if (shouldNotifyTaskReply(prev, t.status)) {
        const assistantText = friendlyTaskPresentation(t.presentation)
          .filter((message) => message.kind === "message" && message.role === "assistant")
          .at(-1)?.text;
        if (isSandboxSupported() && isPhoneLocalTask(t)) {
          void notifySandboxTaskFinished(assistantText || title, t.status, t.id);
        } else {
          void notifyTaskReply(
            title,
            { taskId: t.id, deviceId: t.deviceId },
            { status: t.status, assistantText },
          );
        }
        continue;
      }
      if (isSandboxSupported() && isPhoneLocalTask(t)) {
        if (t.status === "running") {
          void setSandboxTaskStatus(`Running: ${t.title || "coding task"}`);
        } else if (t.status === "failed" || t.status === "stopped") {
          void notifySandboxTaskFinished(title, t.status, t.id);
        }
      }
    }
    for (const id of Array.from(known.keys())) {
      if (!seen.has(id)) known.delete(id);
    }
  }, [tasks]);

  // Auto-scroll to bottom when keyboard appears (prevents last message from being hidden)
  useEffect(() => {
    const sub = Keyboard.addListener("keyboardDidShow", () => {
      if (selectedTask) {
        setTimeout(() => chatScrollRef.current?.scrollToEnd({ animated: true }), 150);
      }
    });
    return () => sub.remove();
  }, [selectedTask]);

  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    // Refresh device list FIRST so a stale "agent session expired" banner
    // clears as soon as the agent's auth has actually been recovered (e.g.
    // by another client or the silent auto-recovery). Without this the
    // banner would persist until the next 30s heartbeat poll, masking
    // the real state.
    try { await refreshDevices(); } catch {}
    // Pull-to-refresh asks the owning Go agent to inspect real local runner
    // panes first. Any newly discovered Codex/Claude/OpenCode process becomes
    // a normal Task, persistence schedules the prompt-free Convex snapshot,
    // and this same gesture then reloads both direct and cross-device state.
    try {
      const runnerClient = taskRunnerClient();
      if (runnerClient.isConnected) await runnerClient.reconcileTasks();
    } catch {}
    await fetchTasks();
    await refreshAgentTaskSnapshots();
    setRefreshing(false);
  }, [fetchTasks, refreshAgentTaskSnapshots, refreshDevices, taskRunnerClient]);

  // ── Voice recording ─────────────────────────────────────────────────

  // Pre-init: request mic permission, configure iOS audio session, init whisper — all on mount
  // BEFORE any Modal opens (iOS blocks audio session activation from inside a <Modal> context).
  useEffect(() => {
    (async () => {
      try {
        // Request mic permission early so the OS prompt appears at app launch
        const { Audio } = require("expo-av");
        const perm = await Audio.requestPermissionsAsync();
        // Give OS time to finalize permission grant before configuring audio session
        if (perm.status === "granted") {
          await new Promise((r) => setTimeout(r, 500));
        }
      } catch (e) {
        console.warn("[audio] Failed to request mic permission:", e);
      }
      try {
        if (Platform.OS === "ios") {
          const { AudioSessionIos } = require("whisper.rn");
          await AudioSessionIos.setCategory("PlayAndRecord", ["DefaultToSpeaker", "AllowBluetooth"]);
          await AudioSessionIos.setActive(true);
        }
      } catch (e) {
        console.warn("[audio] Failed to pre-configure audio session:", e);
      }
      initWhisper().catch((e) => console.warn("[speech] Pre-init failed:", e));
    })();
  }, []);

  // Shared screenshots now route to ShareComposeModal (the WhatsApp-style
  // "comment + pick machines" sheet, mounted at app root) instead of the
  // generic new-task modal — see src/components/ShareComposeModal.tsx.

  // target: which text field to write into ("task" = new task, "followup" = follow-up input)
  const recordingTargetRef = useRef<"task" | "followup">("task");

  // Sticky input mode. The initial mode is keyboard text, and a follow-up defaults to
  // whatever method the user submitted the PRIOR message with. So this starts
  // text; every submit records how it was actually sent (inputFromSpeech),
  // and opening the follow-up composer re-arms dictation when the last send was
  // voice. A ref, not state — it must be read synchronously inside the open
  // handler without forcing a re-render.
  const lastSubmitModeRef = useRef<"voice" | "text">("text");

  // Open the follow-up composer, honouring the sticky mode: re-arm dictation
  // when the previous message went out by voice. startRecording is deferred a
  // tick so the expanded composer is mounted first; otherwise the recording
  // UI attaches to a view that is about to unmount.
  const openFollowUpComposer = () => {
    setFollowUpExpanded(true);
    if (lastSubmitModeRef.current === "voice") {
      setTimeout(() => { void startRecording("followup"); }, 250);
    }
  };

  const startRecording = async (target: "task" | "followup" = "task") => {
    try {
      if (!speechProvider) {
        Alert.alert("Voice Not Configured", "Set up a speech-to-text provider in Settings → Voice.");
        return;
      }
      // Refuse up front if on-device whisper failed to initialise — better
      // than failing deep inside startRealtimeTranscribe with a cryptic error.
      if (speechProvider === "on-device" && whisperInitError) {
        Alert.alert(
          "On-Device Voice Unavailable",
          `${whisperInitError}\n\nSwitch to a cloud provider in Settings → Voice, or reinstall Yaver from the App / Play Store to restore the on-device voice model.`,
        );
        return;
      }

      // Check mic permission — re-prompt or direct to Settings if denied
      const { Audio } = require("expo-av");
      const perm = await Audio.getPermissionsAsync();
      if (perm.status !== "granted") {
        if (perm.canAskAgain) {
          const requested = await Audio.requestPermissionsAsync();
          if (requested.status !== "granted") {
            Alert.alert("Microphone Access", "Microphone permission is required for voice input.");
            return;
          }
        } else {
          Alert.alert(
            "Microphone Access",
            "Microphone permission was denied. Please enable it in Settings > Yaver > Microphone.",
            [
              { text: "Cancel", style: "cancel" },
              { text: "Open Settings", onPress: () => Linking.openSettings() },
            ]
          );
          return;
        }
      }

      recordingTargetRef.current = target;
      const setText = target === "followup" ? setFollowUpText : setNewTaskText;
      const baseText = target === "followup" ? followUpText : newTaskText;

      if (speechProvider === "on-device") {
        // Use whisper.rn's built-in realtime transcription (streams text as you speak)
        setPreRecordText(baseText);
        const savedBase = baseText;
        const controller = await startRealtimeTranscribe((partialText) => {
          // Update text input with streaming partial results
          const next = savedBase ? savedBase + " " + partialText : partialText;
          if (target === "followup") followUpTextRef.current = next;
          else newTaskTextRef.current = next;
          setText(next);
        });
        realtimeRef.current = controller;
        setIsRecording(true);
        setInputFromSpeech(true);
      } else {
        // Cloud providers: record with expo-av, then send file
        await Audio.setAudioModeAsync({ allowsRecordingIOS: true, playsInSilentModeIOS: true });
        const { recording } = await Audio.Recording.createAsync(Audio.RecordingOptionsPresets.HIGH_QUALITY);
        audioRecordingRef.current = recording;
        setIsRecording(true);
      }
      // Auto-stop recording after 5 minutes for privacy
      if (recordingTimeoutRef.current) clearTimeout(recordingTimeoutRef.current);
      recordingTimeoutRef.current = setTimeout(() => {
        stopRecordingAndTranscribe();
      }, 5 * 60 * 1000);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      console.warn("[speech] Failed to start recording:", msg);
      Alert.alert("Recording Error", msg);
    }
  };

  const stopRecordingAndTranscribe = async () => {
    setIsRecording(false);
    if (recordingTimeoutRef.current) {
      clearTimeout(recordingTimeoutRef.current);
      recordingTimeoutRef.current = null;
    }
    const target = recordingTargetRef.current;
    const setText = target === "followup" ? setFollowUpText : setNewTaskText;
    const textRef = target === "followup" ? followUpTextRef : newTaskTextRef;
    const commitText = (text: string) => {
      textRef.current = text;
      setText(text);
    };

    if (speechProvider === "on-device" && realtimeRef.current) {
      // Realtime: stop and get final text (already streamed into input)
      try {
        const finalText = await realtimeRef.current.stop();
        realtimeRef.current = null;
        if (finalText) {
          const base = preRecordText;
          commitText(base ? base + " " + finalText : finalText);
          setInputFromSpeech(true);
        }
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        Alert.alert("Transcription failed", msg);
      }
      return textRef.current;
    }

    // Cloud providers: stop recording, upload file
    if (!audioRecordingRef.current) return textRef.current;
    setIsTranscribing(true);
    try {
      await audioRecordingRef.current.stopAndUnloadAsync();
      const uri = audioRecordingRef.current.getURI();
      audioRecordingRef.current = null;
      if (!uri) throw new Error("No recording URI");
      if (!speechProvider) throw new Error("No speech provider configured.");

      const result = await transcribe(uri, { provider: speechProvider, apiKey: speechApiKey, model: sttModel });
      if (result.text) {
        commitText(textRef.current ? textRef.current + " " + result.text : result.text);
        setInputFromSpeech(true);
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      Alert.alert("Transcription failed", msg);
    } finally {
      setIsTranscribing(false);
    }
    return textRef.current;
  };

  // ── Image picker ─────────────────────────────────────────────────

  const handlePickImage = async (target: "task" | "followup" = "task") => {
    const setImages = target === "followup" ? setFollowUpImages : setAttachedImages;
    const currentImages = target === "followup" ? followUpImages : attachedImages;

    const result = await ImagePicker.launchImageLibraryAsync({
      mediaTypes: ["images"],
      allowsMultipleSelection: true,
      selectionLimit: 5 - currentImages.length,
      quality: 0.7,
      // base64:true makes ImagePicker materialize the asset and return
      // base64 directly. Without it, asset.uri can be a ph:// (iOS Photos
      // framework) URI that expo-file-system's readAsStringAsync cannot
      // resolve — it throws synchronously, and the bare catch below used
      // to swallow it, leaving the user thinking the image attached.
      base64: true,
    });
    if (result.canceled) return;

    const newImages: ImageAttachment[] = [];
    const failures: { name: string; reason: string }[] = [];
    for (const asset of result.assets) {
      try {
        let base64 = asset.base64;
        if (!base64) {
          base64 = await FileSystem.readAsStringAsync(asset.uri, {
            encoding: FileSystem.EncodingType.Base64,
          });
        }
        if (!base64) throw new Error("empty base64");
        newImages.push({
          base64,
          mimeType: asset.mimeType ?? "image/jpeg",
          filename: asset.fileName ?? `image_${Date.now()}.jpg`,
        });
      } catch (err) {
        failures.push({
          name: asset.fileName || "image",
          reason: err instanceof Error ? err.message : String(err),
        });
      }
    }
    if (failures.length > 0) {
      const detail = failures
        .map((f) => `• ${f.name}: ${f.reason}`)
        .join("\n");
      Alert.alert(
        failures.length === result.assets.length
          ? "Couldn't attach image"
          : `${failures.length} of ${result.assets.length} images failed`,
        `${detail}\n\nIf you granted "Limited" Photos access, switch to "All Photos" in Settings.`,
      );
    }
    if (newImages.length > 0) {
      setImages((prev) => [...prev, ...newImages].slice(0, 5));
    }
  };

  // ── TTS ────────────────────────────────────────────────────────────

  const speakTaskResult = (text: string) => {
    if (!ttsEnabled) return;
    // Never read Yaver's own prompt frame aloud. The agent keeps it out of the
    // stream now, but this app talks to boxes that can be many versions behind,
    // and "You are running inside Yaver, not a generic terminal…" spoken into a
    // room is the worst shape this bug takes. Staying silent is the better
    // failure — the text is still on screen.
    if (containsYaverFraming(text)) {
      console.warn("[speech] refusing to read Yaver prompt framing aloud (stale agent?)");
      return;
    }
    speakConfiguredText(text, { provider: ttsProvider, apiKey: speechApiKey, model: ttsModel, voice: ttsVoice }).catch((err: unknown) => {
      console.warn("[speech] TTS failed:", err instanceof Error ? err.message : String(err));
    });
  };

  // Push a fresh Hermes bundle to THIS phone from the connected dev
  // machine. Reuses quic.ts `reloadDevServer` (dev → bundle fallback) and
  // the pool's per-device client so a multi-target pick reloads from the
  // box the user actually selected. Needs the native YaverBundleLoader
  // (iOS + Android); degrade visibly if this build lacks it rather than
  // firing a reload this phone can't consume.
  const triggerHermesReload = async (explicitProjectName?: string) => {
    if (Platform.OS !== "web" && !isBundleLoaderAvailable()) {
      Alert.alert(
        "Reload unavailable",
        "This build of Yaver can't mount project bundles. Update Yaver to the latest version, or use the Reload tab's dev-server controls.",
      );
      return;
    }
    // Runner/render split: the reload/build hop lands on the RENDER box —
    // it holds/serves the app (the runner's push reaches it via the render
    // box's pre-build-pull). Explicit wizard pick still wins.
    const renderRoleId = pendingTarget?.deviceId ? null : connectionManager.roleDeviceId("render");
    const client = pendingTarget?.deviceId
      ? connectionManager.clientFor(pendingTarget.deviceId)
      : renderRoleId
        ? connectionManager.clientFor(renderRoleId)
        : quicClient;
    const targetName = pendingTarget?.deviceName
      || (renderRoleId ? devices.find((d) => d.id === renderRoleId)?.name || "the render machine" : null)
      || activeDevice?.name
      || "the connected machine";
    setIsSubmitting(true);
    setReloadFlash(`Reloading on ${targetName}…`);
    try {
      if (Platform.OS === "web" && explicitProjectName) {
        const matchingSelectedProject = selectedComposerProject?.name.toLowerCase() === explicitProjectName.toLowerCase();
        const built = await client.buildWebJSBundle({
          projectName: explicitProjectName,
          projectPath: matchingSelectedProject ? projectDir || undefined : undefined,
          mode: "full",
        });
        if (!built.ok) {
          setReloadFlash(null);
          Alert.alert("Browser reload failed", built.error || "The selected project web bundle could not be built.");
          return;
        }
        const decision = await rerenderActivePreviewSurface({
          source: "mobile-web-named-project-render",
          taskStatus: "completed",
          autoRenderEnabled: true,
        });
        const message = decision.action === "render"
          ? `${explicitProjectName}: browser bundle rebuilt and preview refreshed.`
          : `${explicitProjectName}: browser bundle rebuilt. Open its preview to render it.`;
        setNewTaskText("");
        setInputFromSpeech(false);
        setReloadFlash(message);
        setTimeout(() => setReloadFlash((cur) => (cur === message ? null : cur)), 4500);
        return;
      }
      // Lane-aware: an RN project actively served on the BROWSER lane must get
      // a browser reload, never a Hermes bundle rebuild — this was the last
      // unconditional bundle caller of the leak class cb72c3e42 fixed. No
      // status (older agent, nothing running) keeps the old bundle behavior.
      const targetStatus = await client.getDevServerStatus().catch(() => null);
      // A named command is an explicit request to rebuild THAT phone project.
      // Never let an unrelated/older browser preview on the box redirect it
      // into /dev/reload. That was the live sfmg + ubuntu-4gb failure: the
      // agent truthfully refreshed its web bundle while the phone saw nothing.
      const nativeLane = !!explicitProjectName || !targetStatus || mustUseNativePreview(targetStatus);
      const result = await client.reloadDevServerDetailed(
        nativeLane
          ? {
              mode: "bundle",
              projectName: explicitProjectName || selectedComposerProject?.name || projectNameFromPath(projectDir),
              projectPath: !explicitProjectName || selectedComposerProject?.name.toLowerCase() === explicitProjectName.toLowerCase()
                ? projectDir || undefined
                : undefined,
              platform: Platform.OS === "android" ? "android" : "ios",
            }
          : { mode: "full", allowBundleFallback: false },
      );
      if (devReloadReachedTarget(result)) {
        taskHaptics.send();
        setNewTaskText("");
        setInputFromSpeech(false);
        const message = describeDevReloadResult(result);
        setReloadFlash(`${targetName}: ${message}`);
        setTimeout(() => setReloadFlash((cur) => (cur === `${targetName}: ${message}` ? null : cur)), 3500);
      } else {
        setReloadFlash(null);
        Alert.alert(
          "Reload failed",
          describeDevReloadResult(result) || `Couldn't reach a dev server on ${targetName}. Start one from the Reload tab (or have the agent run a dev server for the project), then try again.`,
        );
      }
    } catch (e) {
      setReloadFlash(null);
      Alert.alert("Reload failed", e instanceof Error ? e.message : String(e));
    } finally {
      setIsSubmitting(false);
    }
  };
  namedReloadExecutorRef.current = async (explicitProjectName: string) => {
    await triggerHermesReload(explicitProjectName);
  };

  const runPhoneLocalTask = useCallback(async (promptOverride?: string) => {
    const slug = selectedPhoneCheckout;
    const promptText = (promptOverride ?? newTaskTextRef.current).trim();
    if (!slug || !promptText) return;
    if (!remotelessAccessAllowed(user?.isOwner)) {
      Alert.alert("Owner preview", REMOTELESS_OWNER_ONLY_MESSAGE);
      return;
    }
    const config = await loadCodingConfig();
    if (!config) {
      Alert.alert(
        "Set up coding on this phone",
        "Add a DeepSeek or GLM API key, or enable managed coding, then retry. Keys stay in this device's keychain.",
        [
          { text: "Cancel", style: "cancel" },
          { text: "Open settings", onPress: () => taskRouter.push("/sandbox-ai") },
        ],
      );
      return;
    }

    Keyboard.dismiss();
    setTaskSubmitError(null);
    submitInFlightRef.current = true;
    setIsSubmitting(true);
    const taskId = `phone-local-${Date.now()}`;
    const startedAt = Date.now();
    const startedIso = new Date(startedAt).toISOString();
    const initialTask: Task = {
      id: taskId,
      title: promptText,
      description: promptText,
      status: "running" as TaskStatus,
      runnerId: "yaver-phone",
      model: config.model,
      source: "phone-local",
      localCheckoutId: slug,
      output: [],
      resultText: "",
      turns: [{ role: "user", content: promptText, timestamp: startedIso }],
      createdAt: startedAt,
      updatedAt: startedAt,
      deviceName: "This device",
    };
      setTasks((prev) => {
        const next = [initialTask, ...prev];
        void cacheTaskList(next);
        return next;
      });
      consumeAskMode();
      pendingOpenTaskRef.current = initialTask;
    setShowNewTask(false);
    setNewTaskText("");
    setAttachedImages([]);
    setInputFromSpeech(false);

    const updateTask = (mut: (task: Task) => Task) => {
      setTasks((prev) => {
        const next = prev.map((task) => (task.id === taskId ? mut(task) : task));
        void cacheTaskList(next);
        return next;
      });
      setSelectedTask((prev) => (prev && prev.id === taskId ? mut(prev) : prev));
    };
    const controller = new AbortController();
    yaverAgentAbortersRef.current.set(taskId, controller);
    try {
      const result = await runAgenticCoding({
        slug,
        prompt: promptText,
        config,
        mode: askModeEnabled ? "audit" : "vibe",
        extraTools: askModeEnabled ? phoneLocalAuditTools : undefined,
        net: (await gitNetForSlug(slug)) ?? undefined,
        sandbox: repoSandboxForSlug(slug),
        signal: controller.signal,
        lifecycleTaskId: taskId,
        lifecycleTitle: `Coding · ${slug}`,
        onProgress: (event) => {
          if (event.kind === "model_text") {
            const text = redactProgressText(event.text, [config.apiKey]);
            updateTask((task) => ({ ...task, resultText: text, output: [...task.output, text], updatedAt: Date.now() }));
          } else if (event.kind === "tool_call") {
            const safeCall = {
              ...event.call,
              args: redactValue(event.call.args, [config.apiKey]),
              result: redactValue(event.call.result, [config.apiKey]),
              error: event.call.error ? redactSecrets(event.call.error, [config.apiKey]) : undefined,
            };
            const summary = safeCall.error
              ? "failed: " + safeCall.name + " · " + safeCall.error
              : "completed: " + safeCall.name;
            updateTask((task) => ({ ...task, output: [...task.output, summary], updatedAt: Date.now() }));
          }
        },
      });
      if (result.snapshot.entries.length) {
        localTurnUndoRef.current.set(taskId, { slug, snapshot: result.snapshot });
        setLocalTurnUndoEpoch((value) => value + 1);
      }
      const reply = redactSecrets(result.result.finalText.trim() || "Done.", [config.apiKey]);
      const finishedAt = Date.now();
      updateTask((task) => ({
        ...task,
        status: phoneLocalTurnStatus(result.changed.length) as TaskStatus,
        resultText: reply,
        turns: [...(task.turns ?? []), { role: "assistant", content: reply, timestamp: new Date(finishedAt).toISOString() }],
        updatedAt: finishedAt,
      }));
    } catch (e) {
      const aborted = e instanceof Error && e.name === "AbortError";
      const message = aborted ? "Stopped." : redactSecrets(e instanceof Error ? e.message : String(e), [config.apiKey]);
      const finishedAt = Date.now();
      updateTask((task) => ({
        ...task,
        status: aborted ? ("stopped" as TaskStatus) : ("failed" as TaskStatus),
        resultText: message,
        turns: [...(task.turns ?? []), { role: "assistant", content: message, timestamp: new Date(finishedAt).toISOString() }],
        updatedAt: finishedAt,
      }));
    } finally {
      yaverAgentAbortersRef.current.delete(taskId);
      setIsSubmitting(false);
    }
  }, [askModeEnabled, consumeAskMode, selectedPhoneCheckout, taskRouter, user?.isOwner]);

  const handleCreateTask = async (promptOverride?: string, options?: { hideInitialPrompt?: boolean }) => {
    const submittedText = (promptOverride ?? newTaskTextRef.current).trim();
    if (!submittedText && attachedImages.length === 0) return;

    // Remember how this task went out so the follow-up composer defaults to the
    // same input mode (voice ↔ text).
    lastSubmitModeRef.current = inputFromSpeech ? "voice" : "text";

    // Hermes-reload fast-path: a bare "reload"/"hot reload"/"hermes"
    // command — typed or dictated into the composer — shouldn't spin up a
    // full agent task. Push a fresh bundle to this phone directly. Skipped
    // when images are attached (clearly a real task) or with no live host.
    const reloadIntent = attachedImages.length === 0 ? parseReloadIntent(submittedText) : null;
    if (isEffectivelyConnected && reloadIntent) {
      if (isRecording && promptOverride === undefined) { try { await stopRecordingAndTranscribe(); } catch {} }
      if (selectedTask && taskStatusMeansRunnerIsCoding(selectedTask.status)) {
        pendingRuntimeRenderRef.current = {
          taskId: selectedTask.id,
          source: "mobile-user-reload-after-task",
          projectName: reloadIntent.projectName,
          explicit: true,
        };
        setNewTaskText("");
        setInputFromSpeech(false);
        setReloadFlash("Reload queued until the current task finishes.");
        setTimeout(() => setReloadFlash((cur) => (cur === "Reload queued until the current task finishes." ? null : cur)), 3500);
        return;
      }
      // A named project is not an instruction to refresh whichever preview
      // happens to be active. It must traverse the stateless Hermes build path
      // with projectName pinned all the way to /dev/build-native.
      if (reloadIntent.projectName) {
        await triggerHermesReload(reloadIntent.projectName);
        return;
      }
      const decision = await rerenderActivePreviewSurface({
        source: "mobile-explicit-chat-render",
        taskStatus: "completed",
        autoRenderEnabled: true,
      });
      if (decision.action === "skip" && decision.reason === "no-active-surface") {
        await triggerHermesReload();
      }
      return;
    }

    if (codingMode === "local-only" && !selectedPhoneCheckout) {
      Alert.alert(
        "Choose a phone project",
        "No remote box is selected. Choose or clone a GitHub/GitLab project before starting a phone-local DeepSeek task.",
        [
          { text: "Cancel", style: "cancel" },
          { text: "Open Projects", onPress: () => taskRouter.push("/(tabs)/projects") },
        ],
      );
      return;
    }

    if (taskExecutionPlacement.lane === "blocked") {
      Alert.alert("Owner preview", taskExecutionPlacement.banner || REMOTELESS_OWNER_ONLY_MESSAGE);
      return;
    }

    if (taskExecutionPlacement.lane !== "remote" && attachedImages.length > 0) {
      Alert.alert(
        "Images need a remote box",
        "Phone-only execution currently accepts text-only prompts. Your prompt and images are still here; select a remote box to send them together.",
        [
          { text: "Keep editing", style: "cancel" },
          { text: "Choose remote box", onPress: () => taskRouter.navigate("/devices" as any) },
        ],
      );
      return;
    }

    // Explicit phone-local target: this is the repository-scoped DeepSeek
    // agent, not the control-plane Yaver agent and not a remote fallback.
    if (selectedPhoneCheckout && taskExecutionPlacement.lane !== "remote") {
      await runPhoneLocalTask(submittedText);
      return;
    }

    // Yaver-Agent fallback: when no host runner is connected, route the
    // prompt through the embedded control-plane LLM instead of failing
    // with "agent not ready". Streams the assistant's text + tool calls
    // into the task as they happen so users see progress before the
    // final reply lands. Cancellable via Stop on the task card.
    if (taskExecutionPlacement.lane !== "remote") {
      const localCfg = await loadYaverAgentLocalConfig();
      if (!localCfg) {
        Alert.alert(
          "Configure Yaver Agent first",
          "No host device is connected. To run control-plane prompts (auth, status, primary management) without a host, save a provider + API key in Settings → Yaver Agent.",
        );
        return;
      }
      const promptText = submittedText;
      if (!promptText) return;
      Keyboard.dismiss();
      setIsSubmitting(true);

      const taskId = `yaver-agent-${Date.now()}`;
      const startedAt = Date.now();
      const startedAtIso = new Date(startedAt).toISOString();
      const initialTask: Task = {
        id: taskId,
        title: promptText,
        description: promptText,
        status: "running" as TaskStatus,
        runnerId: "yaver-agent",
        output: [],
        resultText: "",
        turns: [{ role: "user", content: promptText, timestamp: startedAtIso }],
        createdAt: startedAt,
        updatedAt: startedAt,
        deviceName: "Yaver Agent",
      };
      setTasks((prev) => [initialTask, ...prev]);
      pendingOpenTaskRef.current = initialTask;
      setShowNewTask(false);
      setNewTaskText("");
      setAttachedImages([]);
      setInputFromSpeech(false);

      const updateTask = (mut: (t: Task) => Task) => {
        setTasks((prev) => prev.map((t) => (t.id === taskId ? mut(t) : t)));
        setSelectedTask((prev) => (prev && prev.id === taskId ? mut(prev) : prev));
      };

      const controller = new AbortController();
      yaverAgentAbortersRef.current.set(taskId, controller);

      try {
        const result = await runYaverAgent({
          prompt: promptText,
          ctx: phoneLocalYaverToolContext,
          maxSteps: 6,
          signal: controller.signal,
          onProgress: (event) => {
            updateTask((t) => {
              if (event.kind === "model_text") {
                return {
                  ...t,
                  resultText: event.text,
                  output: [...t.output, event.text],
                  updatedAt: Date.now(),
                };
              }
              if (event.kind === "tool_call") {
                const summary = event.call.error
                  ? `↳ ${event.call.name} failed: ${event.call.error}`
                  : `↳ ${event.call.name} ✓`;
                return { ...t, output: [...t.output, summary], updatedAt: Date.now() };
              }
              return t;
            });
          },
        });
        const replyText = result.finalText.trim() || "(no reply)";
        const finishedAt = Date.now();
        updateTask((t) => ({
          ...t,
          status: "completed" as TaskStatus,
          resultText: replyText,
          turns: [
            ...t.turns!.slice(0, 1),
            { role: "assistant", content: replyText, timestamp: new Date(finishedAt).toISOString() },
          ],
          updatedAt: finishedAt,
        }));
      } catch (e) {
        const aborted = e instanceof Error && e.name === "AbortError";
        const msg = aborted
          ? "Stopped."
          : e instanceof Error
          ? e.message
          : String(e);
        const finishedAt = Date.now();
        updateTask((t) => ({
          ...t,
          status: aborted ? ("stopped" as TaskStatus) : ("failed" as TaskStatus),
          resultText: msg,
          turns: [
            ...t.turns!.slice(0, 1),
            { role: "assistant", content: msg, timestamp: new Date(finishedAt).toISOString() },
          ],
          updatedAt: finishedAt,
        }));
      } finally {
        yaverAgentAbortersRef.current.delete(taskId);
        setIsSubmitting(false);
      }
      return;
    }

    // Defense in depth for voice / route auto-submit: the visible Send button
    // is disabled for this state, but non-tap entry points reach the same
    // function. Never POST a prompt to a runner the selected box explicitly
    // says is absent, and never dismiss or clear the composer here. The
    // in-composer Install action below is the route to repair.
    if (!pendingTarget && runnerBannerState?.action === "install") {
      return;
    }

    if (selectedRunnerRow?.ready === false && taskExecutionPlacement.lane === "remote" && taskExecutionPlacement.target.id === activeDevice?.id) {
      const detail =
        selectedRunnerAuthIssue ||
        selectedRunnerRow.error ||
        selectedRunnerRow.warning ||
        `${selectedRunnerRow.name} is installed but not ready on this machine.`;
      if (selectedRunnerAuthIssue && selectedRunnerRow.supportsBrowserAuth) {
        openRunnerAuthModal(selectedRunnerRow.id, runnerSelectionDeviceId || null);
      } else {
        Alert.alert("Agent not ready", detail);
      }
      return;
    }
    // Stop any active recording before sending
    if (isRecording && promptOverride === undefined) {
      try { await stopRecordingAndTranscribe(); } catch {}
    }
    Keyboard.dismiss();
    setIsSubmitting(true);
    let pendingCloudTaskParams: Parameters<typeof saveDeferredCloudWorkspaceTask>[1] | null = null;
    try {
      const speechCtx = speechProvider ? {
        inputFromSpeech,
        sttProvider: speechProvider ?? undefined,
        ttsEnabled,
        ttsProvider,
      } : undefined;
      const title = options?.hideInitialPrompt && initialTitle ? initialTitle : submittedText;
      // pendingTarget — set by TaskTargetWizard when multi-target mode
      // is on — overrides the in-modal runner/model picker for this
      // single submission. The wizard already switched the QUIC client
      // to pendingTarget.deviceId via selectDevice, so quicClient
      // baseUrl is correct without any per-call routing here.
      // Resolve both the catalog choice and the POST against the same box.
      // The placement decision can name a live secondary even when focus is
      // elsewhere, so derive it before runner/model defaults.
      const runnerRoleId = pendingTarget?.deviceId
        ? null
        : taskExecutionPlacement.lane === "remote"
          ? taskExecutionPlacement.target.id
          : null;
      const executionDeviceId = pendingTarget?.deviceId || runnerRoleId || runnerSelectionDeviceId;
      const effectiveRunner = pendingTarget?.runner
        ? normalizeTaskRunnerId(pendingTarget.runner)
        : resolveRunnerForSend(undefined, executionDeviceId);
      // Yaver goal-mode: `/goal <objective>` in the composer arms a
      // persistent goal on the opencode runner. The objective travels as
      // the structured `goal` field (NOT a raw runner command) so the
      // agent's <yaver_goal> wrapper fires. Only opencode honors it; other
      // runners get their native /goal passed through raw.
      const goalIntent = goalFromSlashCommand(title, effectiveRunner);
      const goalText = goalIntent?.goal ?? "";
      const effectiveModel = pendingTarget?.model && isModelCompatibleWithRunnerId(pendingTarget.model, effectiveRunner)
        ? pendingTarget.model
        : resolveModelForSend(effectiveRunner, undefined, executionDeviceId);
      // OpenCode mode comes from the wizard's remote opencode.json
      // probe when present; fall back to the in-modal selectedOpenCodeMode.
      const effectiveOpencodeMode = pendingTarget?.opencodeMode ?? selectedOpenCodeMode;
      // Route the sendTask through the EXACT pool client for the
      // wizard's chosen device. The legacy `quicClient` Proxy delegates
      // to whichever client is focused — but the focus shift in the
      // wizard was racing with React state propagation, so a task
      // sent right after picking Mobiles-Mac-mini sometimes ended up
      // on yaver-test-ephemeral (the previously-focused box) with
      // the wizard's runner/model attached. Going through clientFor
      // is deterministic: the URL + headers match the device we
      // genuinely picked.
      // Precedence: an explicit wizard pick wins; otherwise use the exact
      // target selected by the shared placement decision (assigned runner,
      // primary, secondary, then focused). This must not fall back to the
      // focused proxy: the placement banner may be naming a live secondary.
      const sendClient = pendingTarget?.deviceId
        ? connectionManager.clientFor(pendingTarget.deviceId)
        : runnerRoleId
          ? connectionManager.clientFor(runnerRoleId)
          : quicClient;
      // Make sure focus follows so any post-send streams (logs, output)
      // arrive on the same client the new task ran on.
      if (pendingTarget?.deviceId) {
        connectionManager.setFocused(pendingTarget.deviceId);
      }
      // Hard guard: if pendingTarget is set but the chosen sendClient
      // ended up with a baseUrl that doesn't match the picked device,
      // refuse to send and surface the discrepancy. This catches the
      // case the user keeps reproducing where a Mac-mini-targeted task
      // lands on yaver-test-ephemeral — better to fail loudly than
      // silently dispatch to the wrong agent.
      if (pendingTarget?.deviceId) {
        const targetDeviceId = pendingTarget.deviceId;
        const clientDeviceId = (sendClient as any).attachedDeviceId ?? null;
        const clientBaseUrl = (sendClient as any).baseUrl ?? "";
        if (!sendClient.isConnected) {
          throw new Error(
            `Picked ${pendingTarget.deviceName} but its client isn't connected. Re-tap it from the wizard.`,
          );
        }
        if (clientDeviceId && clientDeviceId !== targetDeviceId) {
          throw new Error(
            `Routing mismatch: wizard chose ${pendingTarget.deviceName} (${targetDeviceId.slice(0, 8)}…) but the pooled client is bound to ${clientDeviceId.slice(0, 8)}…. Reload the wizard.`,
          );
        }
        // Telemetry to ourselves — surfaces the URL the task POST is
        // actually using in the task description so post-mortem
        // screenshots tell us whether routing was correct without
        // having to read the agent logs.
        console.log(`[tasks] sendTask → ${pendingTarget.deviceName} via ${clientBaseUrl}`);
      }
      // Transport guard. The "Connected" badge is presence-based (relay /
      // heartbeat) and can show green while the QUIC client is still
      // mid-handshake ("Transport pending") or dropped — sending then
      // throws the raw "QuicClient is not connected. Call connect() first."
      // alert (assertConnected in quic.ts). The wizard path already guards
      // its pooled client above; this covers the MAIN composer (where
      // sendClient is the focused quicClient). Try once to (re)establish
      // via the active device, then fail with an actionable message.
      if (!sendClient.isConnected) {
        if (runnerRoleId) {
          // Role-routed dispatch: bring the runner box's pooled client up.
          // Refusal is NAMED — never silently fall back to the render box.
          const runnerRow = devices.find((d) => d.id === runnerRoleId);
          if (runnerRow && token) {
            try {
              await connectionManager.ensureConnected(runnerRoleId, {
                host: runnerRow.host,
                port: runnerRow.port,
                token,
                lanIps: runnerRow.lanIps,
                connectionPreferences: runnerRow.connectionPreferences,
              });
            } catch {}
          }
          if (!sendClient.isConnected) {
            const runnerName = devices.find((d) => d.id === runnerRoleId)?.name || `${runnerRoleId.slice(0, 8)}…`;
            throw new Error(
              `Your AI runner machine (${runnerName}) is not reachable right now. Nothing was sent to the wrong box — wake it, or change the machine roles in Settings.`,
            );
          }
        } else {
          if (activeDevice) {
            try { await selectDevice(activeDevice); } catch {}
          }
          if (!sendClient.isConnected) {
            throw new Error(
              `Still connecting to ${pendingTarget?.deviceName ?? activeDevice?.name ?? "the device"} — wait for the status dot to turn green, then send again (or tap Retry).`,
            );
          }
        }
      }
      const taskParams = {
        // In goal mode the objective IS the task — never show "/goal x"
        // as the title or send it to the runner wrapped in Yaver's
        // preamble. Use the bare objective everywhere.
        title: goalIntent ? goalText : title,
        description: goalIntent ? goalText : title,
        model: effectiveRunner === "custom" ? undefined : effectiveModel,
        reasoningEffort: effectiveRunner === "codex" ? (pendingTarget?.reasoningEffort || selectedReasoningEffort) : undefined,
        runner: effectiveRunner === "custom" ? "custom" : effectiveRunner,
        customCommand: effectiveRunner === "custom" ? customCommand.trim() || undefined : undefined,
        speechContext: speechCtx,
        images: attachedImages.length > 0 ? attachedImages : undefined,
        workDir: workDirForTaskExecution({
          workDir: projectDir,
          projectDeviceId: selectedComposerProject?.deviceId || runnerSelectionDeviceId,
          executionDeviceId,
        }),
        projectName: selectedComposerProject?.name || projectNameFromPath(projectDir),
        mode: effectiveRunner === "opencode" && effectiveOpencodeMode ? effectiveOpencodeMode : undefined,
        video: videoSummaryEnabled ? { enabled: true } : undefined,
        codeMode: true,
        allowLocalFallback: false,
        mcpServers: selectedMcpServers,
        goal: goalIntent ? goalText : undefined,
        includeYaverMcp,
        askMode: askModeEnabled,
      };
      pendingCloudTaskParams = taskParams;
      const rawTask = await sendClient.sendTask(
        taskParams.title,
        taskParams.description,
        taskParams.model,
        taskParams.runner,
        taskParams.customCommand,
        taskParams.speechContext,
        taskParams.images,
        taskParams.workDir,
        taskParams.mode,
        taskParams.video,
        taskParams.codeMode,
        taskParams.allowLocalFallback,
        taskParams.projectName,
        taskParams.mcpServers,
        taskParams.goal,
        taskParams.includeYaverMcp,
        taskParams.askMode,
        options?.hideInitialPrompt === true,
        initialSessionStartedFrom,
        undefined,
        undefined,
        taskParams.reasoningEffort,
      );
      // A response that names a different runner is proof the requested
      // operation did not happen. Never open a success-shaped OpenCode chat
      // after the user selected Codex: stop the unintended task immediately
      // and surface the exact contract breach.
      if (runnerDispatchMismatch(effectiveRunner, rawTask.runnerId)) {
        await sendClient.stopTask(rawTask.id).catch(() => {});
        throw new Error(
          `Agent mismatch: you selected ${displayRunnerLabel(effectiveRunner)}, but ${displayRunnerLabel(rawTask.runnerId)} started. The unintended task was stopped; refresh runner settings and retry.`,
        );
      }
      if (taskParams.projectName && keepLastProject) {
        const runnerDeviceId = executionDeviceId || "default";
        // Write BOTH stores: AsyncStorage (offline fallback) + Convex
        // defaultRuntimeProjectByDevice (canonical cross-surface memory —
        // the web dashboard reads the same row, so a project remembered on
        // the phone shows up on the web and vice versa).
        const lastProjectRow = {
          deviceId: runnerDeviceId,
          name: taskParams.projectName,
          path: taskParams.workDir,
          branch: selectedComposerProject?.branch,
          gitRemote: selectedComposerProject?.gitRemote,
        };
        void saveLastTaskProject(lastProjectRow);
        if (token) void saveLastTaskProjectToConvex(token, lastProjectRow);
      }
      // Stamp the task with the device + model we KNOW we sent it to
      // (sendTask response doesn't always echo deviceName; with the
      // pool the legitimate source is whichever client we picked).
      // Without this, the task card would later label itself with
      // activeDevice.name even though the work ran on a sibling box.
      const dispatchedDeviceId = executionDeviceId || rawTask.deviceId;
      const dispatchedDevice = dispatchedDeviceId ? devices.find((device) => device.id === dispatchedDeviceId) : null;
      const task: Task = {
        ...rawTask,
        deviceId: dispatchedDeviceId,
        deviceName: pendingTarget?.deviceName || dispatchedDevice?.name || rawTask.deviceName,
        model: rawTask.model || (effectiveRunner !== "custom" ? effectiveModel : undefined),
      };
      consumeAskMode();
      setNewTaskText("");
      setAttachedImages([]);
      setInputFromSpeech(false);
      setPendingTarget(null);
      setTasks((prev) => [task, ...prev]);
      // Stage the task; iOS onDismiss (line 3299) and Android effect
      // (line 2155) hand it to setSelectedTask once the compose
      // Modal's slide-down completes. We can't open the chat-detail
      // Modal in parallel — React Native's native <Modal> doesn't
      // reliably present a second one while the first is on screen,
      // which is why Send used to land you on the list instead of in
      // the chat.
      pendingOpenTaskRef.current = task;
      setShowNewTask(false);
      fetchTasks();
    } catch (e) {
      if (e instanceof CloudWorkspaceRequiredError && pendingCloudTaskParams) {
        const pendingTask = await saveDeferredCloudWorkspaceTask(e, pendingCloudTaskParams);
        consumeAskMode();
        setNewTaskText("");
        setAttachedImages([]);
        setInputFromSpeech(false);
        setPendingTarget(null);
        setTasks((prev) => [pendingTask, ...prev.filter((task) => task.id !== pendingTask.id)]);
        pendingOpenTaskRef.current = pendingTask;
        setShowNewTask(false);
        Alert.alert(
          "Remote machine is preparing",
          "Yaver kept this prompt on your phone and will dispatch it when the assigned machine is ready.",
        );
      } else if (isAuthError(e)) {
        setTaskSubmitError("Session expired. Sign in again to send this task; your prompt is still here.");
        Alert.alert(
          "Session expired",
          "Your sign-in is no longer valid, so the task could not be sent. Sign in again to continue — your work is safe.",
          [
            { text: "Not now", style: "cancel" },
            { text: "Sign in again", onPress: () => { void logout(); } },
          ],
        );
      } else {
        const msg = e instanceof Error ? e.message : String(e);
        const targetName = pendingTarget?.deviceName || runnerSelectionDevice?.name || activeDevice?.name || "the selected machine";
        setTaskSubmitError(`${targetName} did not accept this task: ${msg}`);
        Alert.alert("Task failed", msg);
      }
    } finally {
      submitInFlightRef.current = false;
      setIsSubmitting(false);
    }
  };

  // The initializer owns this kickoff. Submit it exactly once after the
  // generated directory is selected, without flashing the compose modal. The
  // hidden structured turn means the visible Developing chat starts with the
  // agent asking what the app should do.
  useEffect(() => {
    if (!shouldAutoSubmit || !initialPrompt || didAutoSubmitRoutePromptRef.current) return;
    if (!isEffectivelyConnected) return;
    if (routeProjectDir && selectedProjectPath !== routeProjectDir) return;
    didAutoSubmitRoutePromptRef.current = true;
    void handleCreateTask(initialPrompt, { hideInitialPrompt: shouldHideInitialPrompt });
  }, [
    initialPrompt,
    isEffectivelyConnected,
    routeProjectDir,
    selectedProjectPath,
    shouldAutoSubmit,
    shouldHideInitialPrompt,
  ]);

  // Modal handoff. iOS cannot present a second native <Modal> while
  // another one is still on screen — the newcomer mounts invisibly
  // behind it and the flow dead-ends (the same constraint that
  // openRunnerAuthModal works around with its 280ms delay). So every
  // "close A, then open B" transition stages B here and runs it only
  // once A is actually gone: onDismiss is the fast path, and the
  // effect below is the backstop for Android (where onDismiss never
  // fires) and for any sheet whose dismiss callback doesn't land.
  const pendingAfterDismissRef = useRef<(() => void) | null>(null);
  const flushAfterDismiss = useCallback(() => {
    const next = pendingAfterDismissRef.current;
    pendingAfterDismissRef.current = null;
    next?.();
  }, []);
  const handoffModal = useCallback((close: () => void, open: () => void) => {
    pendingAfterDismissRef.current = open;
    close();
  }, []);

  const handleNewTaskModalDismiss = () => {
    if (pendingOpenTaskRef.current) {
      const task = pendingOpenTaskRef.current;
      pendingOpenTaskRef.current = null;
      setSelectedTask(task);
    }
    flushAfterDismiss();
  };

  // Backstop for BOTH platforms — not Android-only.
  //
  // The compose sheet is a `transparent` Modal, and React Native does not fire
  // onDismiss for transparent modals on iOS. So the onDismiss wiring above
  // never ran on iPhone: pendingOpenTaskRef stayed set, setSelectedTask was
  // never called, and Send landed the user back on the LIST — precisely the
  // regression the staging comment in handleCreateTask says it fixed.
  //
  // The user-visible consequence is worse than "wrong screen". Back on the
  // list, the only composer is "New task", so their next message — a follow-up
  // in their head — creates a SECOND task and the first conversation scrolls
  // away. Reported as "I write a new message and can't see my message again,
  // then it shows a new task", identical on codex and claude because it is a
  // modal-lifecycle bug, not a runner one.
  //
  // Keying off `showNewTask` flipping false works on every platform because it
  // observes React state rather than a native callback. onDismiss stays as the
  // fast path where it does fire; handleNewTaskModalDismiss nulls the ref
  // before using it, so running twice is a no-op.
  useEffect(() => {
    if (!showNewTask && pendingOpenTaskRef.current) {
      const timer = setTimeout(handleNewTaskModalDismiss, 100);
      return () => clearTimeout(timer);
    }
  }, [showNewTask]);

  // Backstop for the staged opens above: once every sheet that can own
  // the screen is closed and something is still waiting to open, run
  // it. A stranded staged-open is exactly what makes a button feel
  // dead — you tap, the sheet closes, and nothing ever replaces it.
  useEffect(() => {
    if (showNewTask || showTargetWizard) return;
    if (!pendingAfterDismissRef.current) return;
    const timer = setTimeout(flushAfterDismiss, 350);
    return () => clearTimeout(timer);
  }, [showNewTask, showTargetWizard, flushAfterDismiss]);

  const handleUndoPhoneTurn = async (taskId: string) => {
    const undo = localTurnUndoRef.current.get(taskId);
    if (!undo) return;
    try {
      await restoreTurnSnapshot(repoSandboxForSlug(undo.slug), undo.snapshot);
      localTurnUndoRef.current.delete(taskId);
      setLocalTurnUndoEpoch((value) => value + 1);
      const note = "Undid the last phone-local vibe turn without changing Git history.";
      setTasks((prev) => prev.map((task) => task.id === taskId
        ? { ...task, output: [...task.output, note], updatedAt: Date.now() }
        : task));
      setSelectedTask((prev) => prev?.id === taskId
        ? { ...prev, output: [...prev.output, note], updatedAt: Date.now() }
        : prev);
    } catch (error) {
      Alert.alert("Undo failed", error instanceof Error ? error.message : String(error));
    }
  };

  const handleStopTask = async (taskId: string) => {
    // Yaver-agent tasks live entirely on the phone — no server to call,
    // just abort the local runner. The runner's finally block flips
    // the task status, so we don't optimistic-update here.
    const localAborter = yaverAgentAbortersRef.current.get(taskId);
    if (localAborter) {
      localAborter.abort();
      return;
    }
    const localTask = tasks.find((task) => task.id === taskId && isPhoneLocalTask(task));
    if (localTask) {
      const stoppedAt = Date.now();
      const stopLocal = (task: Task): Task => task.id === taskId
        ? { ...task, status: "stopped" as TaskStatus, resultText: "Stopped.", updatedAt: stoppedAt }
        : task;
      setTasks((prev) => {
        const next = prev.map(stopLocal);
        void cacheTaskList(next);
        return next;
      });
      setSelectedTask((prev) => prev ? stopLocal(prev) : prev);
      void endRemotelessTask(taskId, "stopped", "Stopped.").catch(() => {});
      return;
    }
    try {
      await quicClient.stopTask(taskId);
      // ACK received — immediately update UI
      setTasks(prev => prev.map(t => t.id === taskId ? { ...t, status: "stopped" as TaskStatus } : t));
      setSelectedTask(prev => prev?.id === taskId ? { ...prev, status: "stopped" as TaskStatus } : prev);
      await fetchTasks(); // Sync with server for final state
    } catch {
      // Stop not ACK'd — show error state
      Alert.alert("Stop Failed", "Could not reach the agent. The task may still be running.");
    }
  };

  const handleExitTask = async (taskId: string) => {
    const task = tasks.find((candidate) => candidate.id === taskId) ?? (selectedTask?.id === taskId ? selectedTask : null);
    if (isPhoneLocalTask(task)) {
      await handleStopTask(taskId);
      return;
    }
    try {
      await quicClient.exitTask(taskId);
      // ACK received — immediately update UI
      setTasks(prev => prev.map(t => t.id === taskId ? { ...t, status: "stopped" as TaskStatus } : t));
      setSelectedTask(prev => prev?.id === taskId ? { ...prev, status: "stopped" as TaskStatus } : prev);
      await fetchTasks();
    } catch {
      Alert.alert("Stop Failed", "Could not reach the agent. The task may still be running.");
    }
  };

  // Close the Agent & Model picker. If it was opened from a failed task's
  // "Switch model & retry" CTA (retryAfterPickRef set), re-run the original
  // prompt with the just-picked runner/model — the recovery path for model
  // errors like "gpt-5.4 not supported with a ChatGPT account", which a
  // plain same-model retry just reproduces.
  const closeAgentPicker = (retry = false) => {
    setShowAgentPicker(false);
    const task = retryAfterPickRef.current;
    retryAfterPickRef.current = null;
    if (!task || !retry) return;
    const retryRunner = resolveRunnerForSend(task.runnerId);
    const retryModel = resolveModelForSend(retryRunner, task.model);
    const taskDevice = deviceForTask(task);
    const retryClient = taskDevice?.id && connectionManager.clientFor(taskDevice.id).isConnected
      ? connectionManager.clientFor(taskDevice.id)
      : quicClient;
    taskHaptics.retry();
    void retryClient.sendTask(
      task.title, "", retryModel, retryRunner, undefined, undefined, undefined, projectDir || undefined,
      undefined, undefined, undefined, undefined, selectedComposerProject?.name || projectNameFromPath(projectDir), selectedMcpServers,
    ).then((retried) => {
      const deviceName = taskDevice?.name || task.deviceName || activeDevice?.name || retried.deviceName;
      const next = { ...retried, deviceId: taskDevice?.id || task.deviceId, deviceName, model: retried.model || retryModel };
      setTasks((prev) => [next, ...prev]);
      setSelectedTask(next);
    }).catch((err) => {
      Alert.alert("Retry failed", err instanceof Error ? err.message : String(err));
    });
  };

  const openFollowUpRunnerPicker = () => {
    if (!selectedTask) return;
    const parentRunner = normalizeTaskRunnerId(selectedTask.runnerId) || "claude";
    const byId = new Map(
      availableRunners.map((runner) => [normalizeTaskRunnerId(runner.id), runner]),
    );
    const choices = ["claude", "codex", "opencode"].map((id) => ({
      id,
      row: byId.get(id),
    }));
    Alert.alert(
      "Coding agent for next turn",
      `This task keeps running on ${displayRunnerLabel(parentRunner)}. Your next message can continue there or start a child chat on another agent.`,
      [
        ...choices.map(({ id, row }) => ({
          text:
            displayRunnerLabel(id) +
            ((followUpRunnerOverride || parentRunner) === id ? "  ✓" : "") +
            (row?.installed === false ? "  (not installed)" : ""),
          onPress: () => {
            setFollowUpRunnerOverride(id);
            // Mirror into the shared picker so model resolution and the next
            // New Task composer agree with the explicit choice.
            setSelectedRunner(id);
            userPickedRunnerRef.current = true;
            userPickedModelRef.current = false;
            const targetDeviceId = deviceForTask(selectedTask)?.id || selectedTask.deviceId || activeDevice?.id;
            if (targetDeviceId) {
              void setPrimaryRunnerForDevice(targetDeviceId, id, null).catch((error) => {
                Alert.alert("Couldn't save agent", error instanceof Error ? error.message : String(error));
              });
            }
          },
        })),
        { text: "Cancel", style: "cancel" as const },
      ],
    );
  };

  const clientForSelectedTaskControl = () => {
    if (!selectedTask) return quicClient;
    const taskDevice = deviceForTask(selectedTask);
    return taskDevice?.id
      ? connectionManager.clientFor(taskDevice.id)
      : quicClient;
  };

  const openRunnerControl = async (mode: "model" | "exit") => {
    if (!selectedTask) return;
    const taskDevice = deviceForTask(selectedTask);
    const client = clientForSelectedTaskControl();
    if (!client.isConnected) {
      Alert.alert("Machine not connected", `Reconnect ${taskDevice?.name || "the task machine"}, then try again.`);
      return;
    }
    setRunnerControlMode(mode);
    setRunnerControlCatalog(null);
    setRunnerControlStep("models");
    setRunnerControlError("");
    setRunnerControlBusy(true);
    try {
      const catalog = await client.getTaskRunnerControls(selectedTask.id);
      const initialModel = catalog.model || catalog.models.find((model) => model.isDefault)?.id || catalog.models[0]?.id || "";
      const selected = catalog.models.find((model) => model.id === initialModel);
      setRunnerControlCatalog(catalog);
      setRunnerControlModel(initialModel);
      setRunnerControlEffort(catalog.reasoningEffort || selected?.defaultReasoningEffort || "medium");
    } catch (error) {
      setRunnerControlError(error instanceof Error ? error.message : String(error));
    } finally {
      setRunnerControlBusy(false);
    }
  };

  const applyRunnerModelControl = async () => {
    if (!selectedTask || !runnerControlCatalog || !runnerControlModel) return;
    setRunnerControlBusy(true);
    setRunnerControlError("");
    try {
      const result = await clientForSelectedTaskControl().applyTaskRunnerControl(selectedTask.id, {
        control: "model",
        model: runnerControlModel,
        ...(runnerControlCatalog.runnerId === "codex" ? { reasoningEffort: runnerControlEffort } : {}),
      });
      const update = (task: Task): Task => task.id === selectedTask.id ? {
        ...task,
        model: result.model || runnerControlModel,
        reasoningEffort: result.reasoningEffort,
      } : task;
      setTasks((items) => items.map(update));
      setSelectedTask((task) => task ? update(task) : task);
      setRunnerControlMode(null);
    } catch (error) {
      setRunnerControlError(error instanceof Error ? error.message : String(error));
    } finally {
      setRunnerControlBusy(false);
    }
  };

  const applyRunnerExitControl = async () => {
    if (!selectedTask) return;
    setRunnerControlBusy(true);
    setRunnerControlError("");
    try {
      const result = await clientForSelectedTaskControl().applyTaskRunnerControl(selectedTask.id, { control: "exit", confirmed: true });
      const update = (task: Task): Task => task.id === selectedTask.id ? { ...task, status: result.status || "stopped", updatedAt: Date.now() } : task;
      setTasks((items) => items.map(update));
      setSelectedTask((task) => task ? update(task) : task);
      setRunnerControlMode(null);
    } catch (error) {
      setRunnerControlError(error instanceof Error ? error.message : String(error));
    } finally {
      setRunnerControlBusy(false);
    }
  };

  const handleFollowUp = async (promptOverride?: string) => {
    const submittedText = (promptOverride ?? followUpTextRef.current).trim();
    if (!selectedTask || (!submittedText && followUpImages.length === 0)) return;
    // Exact whole-message runner controls cross the typed JSON boundary. They
    // are UI interactions, so never add `/model` or `/exit` as a chat turn and
    // never wait for terminal pixels to infer whether anything happened.
    const runnerControl = taskRunnerControlForMessage(submittedText);
    if (followUpImages.length === 0 && runnerControl) {
      followUpTextRef.current = "";
      setFollowUpText("");
      await openRunnerControl(runnerControl);
      return;
    }
    // Gate on a LIVE connection before firing. On a flap the socket is gone but
    // host/port/token linger, so the send would silently hit a dead URL and the
    // message would vanish with zero feedback (the 2026-07-21 "second follow-up
    // never submitted" report). The main composer already guards this way; the
    // follow-up path did not. Return BEFORE clearing the input so the text is kept.
    const isLocalFollowUp = isPhoneLocalTask(selectedTask) || selectedTask.runnerId === "yaver-agent";
    if (!isLocalFollowUp && connectionStatus !== "connected") {
      Alert.alert(
        "Not connected",
        `Can't reach ${activeDevice?.name ?? "your machine"} right now — wait for the status dot to turn green, then tap Send again. Your message is kept.`,
      );
      return;
    }
    // Remember how this went out so the NEXT follow-up defaults to the same mode.
    lastSubmitModeRef.current = inputFromSpeech ? "voice" : "text";
    // Stop any active recording before sending
    if (isRecording && promptOverride === undefined) {
      try { await stopRecordingAndTranscribe(); } catch {}
    }
    Keyboard.dismiss();
    setIsSendingFollowUp(true);

    // Phone-local follow-up: continue against the same checkout without
    // touching the remote connection. The previous turns are compacted into
    // the prompt because the repository agent has no server-side session.
    if (isPhoneLocalTask(selectedTask)) {
      const promptText = submittedText;
      const slug = selectedTask.localCheckoutId;
      if (!promptText || !slug) {
        setIsSendingFollowUp(false);
        return;
      }
      if (!remotelessAccessAllowed(user?.isOwner)) {
        setIsSendingFollowUp(false);
        Alert.alert("Owner preview", REMOTELESS_OWNER_ONLY_MESSAGE);
        return;
      }
      if (followUpImages.length > 0) {
        setIsSendingFollowUp(false);
        Alert.alert(
          "Images need a remote box",
          "Remoteless follow-ups are text only for now. Your message and images are still here; switch to a remote task to send them.",
        );
        return;
      }
      const config = await loadCodingConfig();
      if (!config) {
        setIsSendingFollowUp(false);
        Alert.alert(
          "Set up coding on this phone",
          "Add a DeepSeek or GLM API key, or enable managed coding, then retry. Your message is kept.",
          [
            { text: "Cancel", style: "cancel" },
            { text: "Open settings", onPress: () => taskRouter.push("/sandbox-ai") },
          ],
        );
        return;
      }
      const taskId = selectedTask.id;
      const turnAt = Date.now();
      const turnIso = new Date(turnAt).toISOString();
      const prior = (selectedTask.turns ?? []).slice(-12).map((turn) => `${turn.role}: ${turn.content}`).join("\n\n");
      const updateTask = (mut: (task: Task) => Task) => {
        setTasks((prev) => {
          const next = prev.map((task) => (task.id === taskId ? mut(task) : task));
          void cacheTaskList(next);
          return next;
        });
        setSelectedTask((prev) => (prev && prev.id === taskId ? mut(prev) : prev));
      };
      updateTask((task) => ({
        ...task,
        status: "running" as TaskStatus,
        turns: [...(task.turns ?? []), { role: "user", content: promptText, timestamp: turnIso }],
        updatedAt: turnAt,
      }));
      setFollowUpText("");
      setFollowUpImages([]);
      consumeAskMode();
      const controller = new AbortController();
      yaverAgentAbortersRef.current.set(taskId, controller);
      try {
        const result = await runAgenticCoding({
          slug,
          prompt: `Previous conversation:\n${prior}\n\nNew request:\n${promptText}`,
          config,
          mode: askModeEnabled ? "audit" : "vibe",
          extraTools: askModeEnabled ? phoneLocalAuditTools : undefined,
          net: (await gitNetForSlug(slug)) ?? undefined,
          sandbox: repoSandboxForSlug(slug),
          signal: controller.signal,
          lifecycleTaskId: taskId,
          lifecycleTitle: `Coding · ${slug}`,
          onProgress: (event) => {
            if (event.kind === "model_text") {
              const text = redactProgressText(event.text, [config.apiKey]);
              updateTask((task) => ({ ...task, resultText: text, output: [...task.output, text], updatedAt: Date.now() }));
            } else if (event.kind === "tool_call") {
              const detail = event.call.error ? redactSecrets(event.call.error, [config.apiKey]) : "✓";
              updateTask((task) => ({ ...task, output: [...task.output, `↳ ${event.call.name} ${detail}`], updatedAt: Date.now() }));
            }
          },
        });
        if (result.snapshot.entries.length) {
          localTurnUndoRef.current.set(taskId, { slug, snapshot: result.snapshot });
          setLocalTurnUndoEpoch((value) => value + 1);
        }
        const reply = redactSecrets(result.result.finalText.trim() || "Done.", [config.apiKey]);
        const finishedAt = Date.now();
        updateTask((task) => ({
          ...task,
          status: phoneLocalTurnStatus(result.changed.length) as TaskStatus,
          resultText: reply,
          turns: [...(task.turns ?? []), { role: "assistant", content: reply, timestamp: new Date(finishedAt).toISOString() }],
          updatedAt: finishedAt,
        }));
      } catch (e) {
        const aborted = e instanceof Error && e.name === "AbortError";
        const message = aborted ? "Stopped." : redactSecrets(e instanceof Error ? e.message : String(e), [config.apiKey]);
        const finishedAt = Date.now();
        updateTask((task) => ({
          ...task,
          status: aborted ? ("stopped" as TaskStatus) : ("failed" as TaskStatus),
          resultText: message,
          turns: [...(task.turns ?? []), { role: "assistant", content: message, timestamp: new Date(finishedAt).toISOString() }],
          updatedAt: finishedAt,
        }));
      } finally {
        yaverAgentAbortersRef.current.delete(taskId);
        setIsSendingFollowUp(false);
      }
      return;
    }

    // Yaver-agent follow-up: continue the embedded LLM conversation
    // using prior turns as history. Same streaming + cancel rig as the
    // initial run.
    if (selectedTask.runnerId === "yaver-agent") {
      const promptText = submittedText;
      if (!promptText) {
        setIsSendingFollowUp(false);
        return;
      }
      if (followUpImages.length > 0) {
        setIsSendingFollowUp(false);
        Alert.alert(
          "Images need a remote box",
          "Yaver Agent follow-ups are text only for now. Your message and images are still here; switch to a remote task to send them.",
        );
        return;
      }
      const taskId = selectedTask.id;
      const turnAt = Date.now();
      const turnIso = new Date(turnAt).toISOString();
      const priorTurns = selectedTask.turns ?? [];
      const history: YaverAgentHistoryTurn[] = priorTurns
        .filter((t) => t.content?.trim())
        .map((t) => ({ role: t.role, text: t.content }));

      const updateTask = (mut: (t: Task) => Task) => {
        setTasks((prev) => prev.map((t) => (t.id === taskId ? mut(t) : t)));
        setSelectedTask((prev) => (prev && prev.id === taskId ? mut(prev) : prev));
      };

      // Append the user turn immediately so the chat detail reflects it.
      updateTask((t) => ({
        ...t,
        status: "running" as TaskStatus,
        turns: [...(t.turns ?? []), { role: "user", content: promptText, timestamp: turnIso }],
        updatedAt: turnAt,
      }));
      setFollowUpText("");
      setFollowUpImages([]);

      const controller = new AbortController();
      yaverAgentAbortersRef.current.set(taskId, controller);

      try {
        const result = await runYaverAgent({
          prompt: promptText,
          ctx: phoneLocalYaverToolContext,
          history,
          maxSteps: 6,
          signal: controller.signal,
          onProgress: (event) => {
            if (event.kind === "tool_call") {
              const summary = event.call.error
                ? `↳ ${event.call.name} failed: ${event.call.error}`
                : `↳ ${event.call.name} ✓`;
              updateTask((t) => ({ ...t, output: [...t.output, summary], updatedAt: Date.now() }));
            }
          },
        });
        const replyText = result.finalText.trim() || "(no reply)";
        const finishedAt = Date.now();
        updateTask((t) => ({
          ...t,
          status: "completed" as TaskStatus,
          resultText: replyText,
          turns: [
            ...(t.turns ?? []),
            { role: "assistant", content: replyText, timestamp: new Date(finishedAt).toISOString() },
          ],
          updatedAt: finishedAt,
        }));
      } catch (e) {
        const aborted = e instanceof Error && e.name === "AbortError";
        const msg = aborted ? "Stopped." : e instanceof Error ? e.message : String(e);
        const finishedAt = Date.now();
        updateTask((t) => ({
          ...t,
          status: aborted ? ("stopped" as TaskStatus) : ("failed" as TaskStatus),
          resultText: msg,
          turns: [
            ...(t.turns ?? []),
            { role: "assistant", content: msg, timestamp: new Date(finishedAt).toISOString() },
          ],
          updatedAt: finishedAt,
        }));
      } finally {
        yaverAgentAbortersRef.current.delete(taskId);
        setIsSendingFollowUp(false);
      }
      return;
    }

    // Show the user's own message IMMEDIATELY, before any network call.
    //
    // Only the yaver-agent branch above did this, so on every runner path
    // (codex, claude, opencode — what people actually use) the text vanished
    // from the input and appeared NOWHERE until fetchTasks() came back. When
    // the old terminal-state path also forked, the view swapped to a fresh
    // child task and made the failure worse. That is the "I wrote a message
    // and it created a new task" report.
    //
    // A follow-up never changes task identity. Completion ends the preceding
    // runner turn, not this conversation.
    const optimisticText = submittedText;
    // Capture the payload NOW, before we clear the composer — the send calls
    // below must use these consts, never the live followUpText/followUpImages
    // state (which we're about to empty).
    const optimisticImages = followUpImages;
    const optimisticTurn = {
      role: "user" as const,
      content: optimisticText,
      timestamp: new Date().toISOString(),
    };
    const optimisticParentId = selectedTask.id;
    if (optimisticText) {
      const withTurn = (t: Task): Task => ({
        ...t,
        turns: [...(t.turns ?? []), optimisticTurn],
        updatedAt: Date.now(),
      });
      setTasks((prev) => prev.map((t) => (t.id === optimisticParentId ? withTurn(t) : t)));
      setSelectedTask((prev) => (prev && prev.id === optimisticParentId ? withTurn(prev) : prev));
    }
    // WhatsApp/Claude-Code-style: the message is on screen, so clear the input
    // and RE-ENABLE the composer immediately — do NOT hold it disabled for the
    // network round-trip. On a slow relay path (measured 2932ms in the iOS
    // simulator) the old flow froze "Sending…" until the POST settled, which
    // read as a stuck UI. The send now runs in the background; a failure rolls
    // the turn back and alerts, and the typed text is restored so the user can
    // retry.
    setFollowUpText("");
    setFollowUpImages([]);
    setIsSendingFollowUp(false);

    // Undo the optimistic turn when the send does not happen. Leaving it would
    // show the user a message that was never sent — the same "UI states
    // something it does not know" failure this screen has been bitten by
    // before, just inverted.
    const rollbackOptimisticTurn = () => {
      // Restore the input we cleared optimistically so the user can retry —
      // only if they haven't already started typing the next message.
      setFollowUpText((cur) => (cur.trim() ? cur : optimisticText));
      setFollowUpImages((cur) => (cur.length ? cur : optimisticImages));
      if (!optimisticText) return;
      const dropTurn = (t: Task): Task => {
        const turns = t.turns ?? [];
        const last = turns[turns.length - 1];
        if (last && last.role === "user" && last.content === optimisticTurn.content && last.timestamp === optimisticTurn.timestamp) {
          return { ...t, turns: turns.slice(0, -1) };
        }
        return t;
      };
      setTasks((prev) => prev.map((t) => (t.id === optimisticParentId ? dropTurn(t) : t)));
      setSelectedTask((prev) => (prev && prev.id === optimisticParentId ? dropTurn(prev) : prev));
    };

    try {
      if (selectedTask.isAdopted) {
        // For adopted tmux sessions, send input to the task's own box. The
        // focused client may point at Ubuntu while this task is a Codex pane on
        // the MacBook; using the global proxy would send the task id to the
        // wrong agent and make ordinary input — including `/model` — fail.
        const taskDevice = deviceForTask(selectedTask);
        const taskClient = taskDevice?.id
          ? connectionManager.clientFor(taskDevice.id)
          : quicClient;
        await taskClient.sendTmuxInput(selectedTask.id, optimisticText);
        setTasks((prev) => prev.map((task) => task.id === selectedTask.id ? beginTaskTurn(task) : task));
        setSelectedTask((prev) => prev?.id === selectedTask.id ? beginTaskTurn(prev) : prev);
      } else {
        // A reply is always a continuation of this exact task + native runner
        // session. Finished/review means the previous TURN ended; it does not
        // authorize a child task. Runner changes are blocked here because
        // Claude/Codex/OpenCode session formats are not interchangeable.
        const parentRunner = (selectedTask.runnerId || "").trim();
        // Ordinary replies stay on the task's recorded runner. Only the
        // task-scoped picker above may override it; the global New Task picker
        // is deliberately ignored here so unrelated composer state cannot
        // switch a conversation by accident.
        const desiredRunner = (followUpRunnerOverride || parentRunner || selectedRunner || "").trim();
        // planFollowUp owns this invariant so it can be tested without React
        // Native — see mobile/src/lib/followUpPlan.test.ts.
        const plan = planFollowUp({
          isAdopted: selectedTask.isAdopted,
          parentRunner,
          desiredRunner,
          status: selectedTask.status,
        });
        if (plan.action === "runner-change-blocked") {
          rollbackOptimisticTurn();
          Alert.alert(
            "This conversation stays with its runner",
            `This task belongs to ${parentRunner || "its original runner"}. Start a new task if you want to use ${desiredRunner}; a follow-up will never create or switch sessions.`,
          );
          return;
        }
        // The agent accepts live follow-ups into this task's queue and resumes
        // completed turns only when it can address the exact native session.
        const executionSession = await connectionManager.runnerClient().continueTask(
          selectedTask.id,
          optimisticText,
          optimisticImages.length > 0 ? optimisticImages : undefined,
          followUpOpenCodeMode || undefined,
        );
        const applyIdentity = (task: Task): Task => ({
          ...task,
          sessionId: executionSession.runnerSessionId || task.sessionId,
          executionSession,
          tmuxSession: executionSession.tmuxSession || task.tmuxSession,
          tmuxSessionId: executionSession.tmuxSessionId || task.tmuxSessionId,
          tmuxWindowIndex: executionSession.tmuxWindowIndex || task.tmuxWindowIndex,
          tmuxWindowName: executionSession.tmuxWindowName || task.tmuxWindowName,
          tmuxPaneIndex: executionSession.tmuxPaneIndex || task.tmuxPaneIndex,
          tmuxPaneId: executionSession.tmuxPaneId || task.tmuxPaneId,
        });
        // The accepted message starts a new turn in this conversation. Review
        // described the previous turn; it must become active again without
        // waiting for the next list poll to notice the runner resumed.
        setTasks((prev) => prev.map((task) => task.id === selectedTask.id ? beginTaskTurn(applyIdentity(task)) : task));
        setSelectedTask((prev) => prev?.id === selectedTask.id ? beginTaskTurn(applyIdentity(prev)) : prev);
        consumeAskMode();
      }
      // Input already cleared optimistically above — just refresh.
      await fetchTasks();
    } catch (err) {
      // PARKED is not FAILED, and the difference is load-bearing.
      //
      // When the agent answers 409 `parked:true` it KEPT the user's words and
      // will replay them into this same session the moment the runner's
      // credential is restored. Doing the normal failure dance here — roll the
      // message back out of the UI and say "tap Send to try again" — would make
      // the user retype it, and then the prompt runs TWICE when the replay
      // fires. So: leave the optimistic turn on screen (it is real work that is
      // genuinely queued), and say so in one line, with the sign-in as the only
      // action when signing in is what unblocks it.
      if (err instanceof ParkedTurnError) {
        const notice = parkedTurnNotice(err);
        setIsSendingFollowUp(false);
        Alert.alert(
          "Message saved",
          notice.line,
          notice.action
            ? [
                { text: "Later", style: "cancel" },
                {
                  text: notice.action.label,
                  // The existing in-place sign-in modal, not a route push: it
                  // runs the device-auth flow against the machine that is
                  // actually blocked and returns the user to this task, which
                  // is the whole point of a route-to-fix.
                  onPress: () =>
                    openRunnerAuthModal(
                      err.runner || "codex",
                      // Same target resolution the rest of this screen uses, so
                      // the sign-in lands on the machine that is actually
                      // blocked rather than whichever device is merely focused.
                      (selectedTask ? deviceForTask(selectedTask)?.id || selectedTask.deviceId : null) || activeDevice?.id || null,
                    ),
                },
              ]
            : [{ text: "OK" }],
        );
        return;
      }
      // The send failed, so the message never reached the runner. Take the
      // optimistic turn back out rather than leaving a phantom message.
      rollbackOptimisticTurn();
      // SURFACE it. A silent rollback is exactly why the second follow-up
      // "vanished" with no explanation (2026-07-21): the user saw the message
      // disappear and had no idea it hadn't sent. The typed text is preserved
      // (only cleared on the success path), so re-opening the composer shows it
      // again — the alert tells them to retry.
      Alert.alert(
        "Couldn't send",
        err instanceof Error && err.message
          ? err.message
          : `The message didn't reach ${activeDevice?.name ?? "the machine"}. It's still in the box — tap Send to try again.`,
      );
    } finally {
      setIsSendingFollowUp(false);
    }
  };

  // One-tap voice send: stop Whisper first, then pass its returned transcript
  // directly into the normal submit path. React state updates do not refresh
  // this event handler's closure, so waiting a tick still submitted the old
  // empty value even though the final text was visible in the field.
  const finishVoiceAndSubmit = async (target: "task" | "followup") => {
    const transcript = await stopRecordingAndTranscribe();
    if (target === "task") await handleCreateTask(transcript);
    else await handleFollowUp(transcript);
  };

  const handleDeleteTask = async (task: Task, quiet = false): Promise<boolean> => {
    const taskId = task.id;
    if (isPhoneLocalTask(task)) {
      const next = tasks.filter((candidate) => candidate.id !== taskId);
      setTasks(next);
      if (selectedTask?.id === taskId) setSelectedTask(null);
      void cacheTaskList(next);
      return true;
    }
    try {
      const owner = deviceForTask(task);
      const client = owner?.id ? connectionManager.clientFor(owner.id) : quicClient;
      if (!client.isConnected) {
        throw new Error(`${owner?.name || task.deviceName || "The owning machine"} is offline. The session was not deleted.`);
      }
      // DELETE is the one lifecycle operation: the owning agent closes the
      // retained runner seat, verifies local state, persists the tombstone and
      // schedules the Convex snapshot. Never pre-close through another client.
      await client.deleteTask(taskId);
      const key = scopedTaskIdentity(task.deviceId || owner?.id, task.id);
      setTasks((previous) => {
        const next = previous.filter((candidate) => scopedTaskIdentity(candidate.deviceId, candidate.id) !== key);
        void cacheTaskList(next);
        return next;
      });
      if (selectedTask && scopedTaskIdentity(selectedTask.deviceId, selectedTask.id) === key) setSelectedTask(null);
      void refreshAgentTaskSnapshots();
      return true;
    } catch (e) {
      if (!quiet) Alert.alert("Delete failed", e instanceof Error ? e.message : String(e));
      return false;
    }
  };

  const handleCompleteTask = async (taskId: string) => {
    const localTask = tasks.find((task) => task.id === taskId && isPhoneLocalTask(task));
    if (localTask) {
      const completeLocal = (task: Task): Task => task.id === taskId
        ? { ...task, status: "completed" as TaskStatus, updatedAt: Date.now() }
        : task;
      setTasks((prev) => {
        const next = prev.map(completeLocal);
        void cacheTaskList(next);
        return next;
      });
      setSelectedTask((prev) => prev ? completeLocal(prev) : prev);
      return;
    }
    try {
      await quicClient.completeTask(taskId);
      setTasks(prev => prev.map(t => t.id === taskId ? { ...t, status: "completed" as TaskStatus } : t));
      setSelectedTask(prev => prev?.id === taskId ? { ...prev, status: "completed" as TaskStatus } : prev);
      await fetchTasks();
    } catch {
      Alert.alert("Complete Failed", "Could not mark this task complete.");
    }
  };

  const handleStopAll = async () => {
    try { await quicClient.stopAllTasks(); await fetchTasks(); } catch {}
  };

  // Active-chip bulk actions. Tapping the Active chip while it is already the
  // selected filter opens a popup to act on every active conversation
  // task at once — the "delete all active / remove actives" the user asked for.
  // Stop-and-clear stops the running ones first (so the agent actually tears
  // them down) then removes them, otherwise a deleted-but-running task reappears
  // on the next poll.
  const activeTasks = () =>
    tasks.filter((t) => t.status === "running" || t.status === "queued");
  const handleActiveBulkActions = () => {
    const active = activeTasks();
    if (active.length === 0) {
      Alert.alert("No active tasks", "There are no coding tasks running or queued.");
      return;
    }
    Alert.alert(
      `${active.length} active task${active.length === 1 ? "" : "s"}`,
      "Act on every active task at once.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Stop all",
          onPress: async () => {
            try { await quicClient.stopAllTasks(); } catch (e) { console.warn("[Tasks] Stop all active failed:", e); }
            await fetchTasks();
          },
        },
        {
          text: "Stop & remove all",
          style: "destructive",
          onPress: async () => {
            // The owning agent's idempotent Delete operation stops/closes and
            // persists each session. Reuse the same acknowledged path as a
            // single card; never hide a row that its owner did not delete.
            for (const task of active) await handleDeleteTask(task);
            await fetchTasks();
          },
        },
      ],
    );
  };

  const handleDeleteAll = async () => {
    const deletable = tasks.filter((t) => t.status !== "running" && t.status !== "queued");
    if (deletable.length === 0) return;
    for (const task of deletable) await handleDeleteTask(task);
    await fetchTasks();
  };

  const taskBulkKey = (task: Task) => scopedTaskIdentity(task.deviceId, task.id);
  const toggleTaskSelection = (task: Task) => {
    const key = taskBulkKey(task);
    setSelectedBulkTaskKeys((previous) => {
      const next = new Set(previous);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };
  const beginTaskSelection = (task?: Task) => {
    setSelectingTasks(true);
    setSelectedBulkTaskKeys(task ? new Set([taskBulkKey(task)]) : new Set());
  };
  const cancelTaskSelection = () => {
    setSelectingTasks(false);
    setSelectedBulkTaskKeys(new Set());
  };
  const toggleSelectAllVisibleTasks = () => {
    const visibleKeys = displayTasks.map(taskBulkKey);
    const allSelected = visibleKeys.length > 0 && visibleKeys.every((key) => selectedBulkTaskKeys.has(key));
    setSelectedBulkTaskKeys((previous) => {
      const next = new Set(previous);
      for (const key of visibleKeys) {
        if (allSelected) next.delete(key);
        else next.add(key);
      }
      return next;
    });
  };
  const handleDeleteSelectedTasks = async () => {
    const selected = tasks.filter((task) => selectedBulkTaskKeys.has(taskBulkKey(task)));
    if (selected.length === 0) return;
    const failed: Task[] = [];
    // Preserve the single-task guarantee for every row: each owning agent
    // closes/verifies its own runner seat before that row disappears locally.
    for (const task of selected) {
      if (!(await handleDeleteTask(task, true))) failed.push(task);
    }
    if (failed.length > 0) {
      setSelectedBulkTaskKeys(new Set(failed.map(taskBulkKey)));
      Alert.alert(
        "Some tasks were not deleted",
        `${failed.length} owning machine${failed.length === 1 ? " is" : "s are"} offline or did not acknowledge deletion. Those tasks remain selected.`,
      );
    } else {
      cancelTaskSelection();
    }
    await fetchTasks();
    await refreshAgentTaskSnapshots();
  };

  // Summary — last 24h activity digest
  const handleShowSummary = async () => {
    try {
      const { text } = await quicClient.getSummary(24);
      Alert.alert("Summary (24h)", text || "No activity in the last 24 hours.");
    } catch (e) {
      Alert.alert("Error", e instanceof Error ? e.message : String(e));
    }
  };

  // The header banner ALSO needs to reflect the connection-manager pool
  // — without this, a user with two live pooled clients but a
  // momentarily-stale focused client would see "Disconnected · <name>"
  // at the top of Tasks while Devices simultaneously rendered both
  // boxes as CONNECTED. Promote effectiveState to "connected" whenever
  // any pool client reports live, so the banner mirrors the source of
  // truth the Devices tab is already reading from.
  // Honest connection state. `connectionStatus` goes "connected" the
  // instant selectDevice's connect resolves — which is OPTIMISTIC: a
  // relay tunnel can come up while the agent behind it is unreachable,
  // leaving a green "Connected" for a box whose transport is pending and
  // whose ping fails (and, worse, gating OFF the reachability probe sweep
  // below so the dead box is never discovered). When a device is selected,
  // only trust it if that exact device is in the LIVE connected pool
  // (connectionManager's transport truth). The pool-any fallback is kept
  // ONLY for the no-device-focused case so a cold start with a warm pool
  // still reads connected.
  const taskExecutionCandidates = useMemo(() => {
    const rows: ExecutionCandidate[] = [];
    const seen = new Set<string>();
    const add = (id: string | null | undefined, role: ExecutionCandidate["role"]) => {
      if (!id || seen.has(id)) return;
      const device = devices.find((row) => row.id === id);
      if (!device) return;
      seen.add(id);
      rows.push({ id, name: device.name || id.slice(0, 8), role, connected: connectedDeviceIds.includes(id) });
    };
    // The Tasks banner's visible Remote Box is an explicit execution choice.
    // Keep placement aligned with the runner picker: a legacy account-wide
    // runner role must not make the header say Ubuntu while POST /tasks goes
    // to a Mac. Advanced split roles remain fallbacks when no visible box is
    // selected (for example before initial connection hydration).
    add(runnerSelectionDeviceId, "explicit");
    add(machineRoles?.runnerDeviceId, "primary");
    add(machineRoles?.secondaryRunnerDeviceId, "secondary");
    add(primaryDeviceId, "primary");
    add(secondaryDeviceId, "secondary");
    add(activeDevice?.id, "focused");
    return rows;
  }, [activeDevice?.id, connectedDeviceIds, devices, machineRoles, primaryDeviceId, runnerSelectionDeviceId, secondaryDeviceId]);
  const taskExecutionPlacement = useMemo(() => {
    const placement = resolveRemotelessPlacement({
      capability: "code-edit",
      surface: Platform.OS === "ios" ? "ios" : Platform.OS === "android" ? "android" : "web",
      candidates: taskExecutionCandidates,
      forceLocal: codingMode === "local-only",
    });
    // The core placement engine is account-agnostic. Apply the server-derived
    // owner entitlement at the UI boundary too, so stale local settings or a
    // route-seeded phone checkout cannot silently enter Remoteless.
    return placement.lane === "remoteless" && !ownerRemotelessEnabled
      ? { ...placement, lane: "blocked" as const, banner: REMOTELESS_OWNER_ONLY_MESSAGE }
      : placement;
  }, [codingMode, ownerRemotelessEnabled, taskExecutionCandidates]);
  const phoneFallbackInUse = taskExecutionPlacement.lane === "remoteless" && (
    !!selectedPhoneCheckout || tasks.some((task) => task.source === "phone-local" && (task.status === "running" || task.status === "queued"))
  );

  // A reconnected primary/secondary resumes precedence immediately. Keeping a
  // stale phone checkout selected made the next Send silently bypass the box.
  useEffect(() => {
    if (taskExecutionPlacement.lane === "remote" && selectedPhoneCheckout) {
      setSelectedPhoneCheckout(null);
    }
  }, [selectedPhoneCheckout, taskExecutionPlacement.lane]);

  useEffect(() => {
    if (!isEffectivelyConnected) return;
    let cancelled = false;
    const runnerDeviceId = connectionManager.roleDeviceId("runner") || activeDevice?.id || "default";
    void (async () => {
      const [projectRows, mcpRows, keep, useLatestMCPPref, settings] = await Promise.all([
        (runnerSelectionDeviceId
          ? connectionManager.clientFor(runnerSelectionDeviceId)
          : connectionManager.runnerClient()
        ).listProjects().catch(() => [] as ComposerProject[]),
        listMcpServers().catch(() => [] as McpServer[]),
        loadKeepLastProjectEnabled(),
        loadUseLatestMCPEnabled(),
        // Convex runtime project catalog for the runner device — the
        // Convex-seeded memory of the same git projects, merged with the
        // agent's live discovery below so both sources feed ONE list
        // (2026-08-09). Privacy-limited: names/remotes/branches, no paths.
        token ? getUserSettings(token).catch(() => null) : Promise.resolve(null),
      ]);
      if (cancelled) return;
      const catalogForDevice = (settings as any)?.runtimeProjectCatalogByDevice as
        | Array<{ deviceId: string; projects?: Array<{ projectName?: string | null; repoName?: string | null; gitRemote?: string | null; branch?: string | null; framework?: string | null }> }>
        | undefined;
      // Cross-machine catalogs (2026-08-13): the SAME rows the web chat
      // composer reads, so a box with MCP servers shows up on the phone as
      // selectable "on other machines" rows in the composer sheet.
      const mcpCatalogForDevice = (settings as any)?.mcpCatalogByDevice as
        | Array<{ deviceId: string; servers?: Array<{ name?: string; url?: string; enabled?: boolean; toolCount?: number }> }>
        | undefined;
      const mcpCatalogMap: Record<string, Array<{ name: string; url: string; toolCount?: number }>> = {};
      for (const row of mcpCatalogForDevice || []) {
        if (!row?.deviceId) continue;
        const servers = (Array.isArray(row.servers) ? row.servers : [])
          .filter((s) => s && s.name && s.enabled !== false)
          .map((s) => ({
            name: String(s.name).trim(),
            url: String(s.url || "").trim(),
            ...(typeof s.toolCount === "number" && s.toolCount >= 0 ? { toolCount: s.toolCount } : {}),
          }));
        if (servers.length > 0) mcpCatalogMap[row.deviceId] = servers;
      }
      const projectCatalogMap: Record<string, Array<{ projectName?: string | null; repoName?: string | null; gitRemote?: string | null; branch?: string | null }>> = {};
      for (const row of catalogForDevice || []) {
        if (!row?.deviceId || !Array.isArray(row.projects) || row.projects.length === 0) continue;
        projectCatalogMap[row.deviceId] = row.projects;
      }
      if (!cancelled) {
        setMcpCatalogByDevice(mcpCatalogMap);
        setProjectCatalogByDevice(projectCatalogMap);
      }
      const runnerDeviceId = runnerSelectionDeviceId || activeDevice?.id || "default";
      const catalogRow = (catalogForDevice || []).find((row) => row?.deviceId === runnerDeviceId);
      let normalizedProjects: ComposerProject[] = (projectRows || [])
        .filter((project: any) => project?.path)
        .map((project: any) => ({
          name: String(project.name || projectNameFromPath(project.path) || "Project"),
          path: String(project.path),
          deviceId: runnerDeviceId,
          branch: project.branch ? String(project.branch) : undefined,
          framework: project.framework ? String(project.framework) : undefined,
          gitRemote: project.gitRemote ? String(project.gitRemote) : undefined,
        }));
      // Top-level only + Convex/agent merge (Snowball, 2026-08-09): a box's
      // discovery can leak nested clones (yaver.io/mobile) and the picker
      // must fold them into their root; the Convex catalog enriches what the
      // agent reports. Both sources, one top-level list.
      normalizedProjects = mergeConvexCatalogIntoProjects(normalizedProjects, catalogRow?.projects);
      setComposerProjects(normalizedProjects);
      setAvailableMcpServers((mcpRows || []).filter((server) => server.enabled));
      setKeepLastProject(keep);
      setUseLatestMCP(useLatestMCPPref);

      // MCP selection restore — same mcpServersByDevice row the web chat +
      // Vibing composers and tvOS write, so an MCP set chosen on the web is
      // remembered on the phone and vice versa (2026-08-10). Convex-first,
      // no local fallback (MCP names are agent-scoped, not path-scoped).
      if (!useLatestMCPPref && !suppressMcpRestoreRef.current) {
        setSelectedMcpServers([]);
        setIncludeYaverMcp(false);
      } else if (token) {
        const mcpPref = await loadMCPServersFromConvex(token, runnerDeviceId).catch(() => null);
        if (mcpPref && !cancelled && !suppressMcpRestoreRef.current) {
          const known = new Set((mcpRows || []).filter((s) => s.enabled).map((s) => s.name));
          if (Array.isArray(mcpPref.mcpServers)) {
            setSelectedMcpServers(mcpPref.mcpServers.filter((name) => known.has(name)));
          }
          if (typeof mcpPref.includeYaverMcp === "boolean") {
            setIncludeYaverMcp(mcpPref.includeYaverMcp);
          }
        }
      }

      if (routeProjectDir) return;
      if (selectedProjectPath && normalizedProjects.some((project) => project.path === selectedProjectPath)) return;
      const explicit = explicitProjectChoiceRef.current;
      if (explicit?.deviceId === runnerDeviceId && explicit.path === "") return;
      if (keep) {
        // Convex-first, local-fallback (Snowball, 2026-08-09): the canonical
        // last-project memory is defaultRuntimeProjectByDevice — the SAME row
        // the web dashboard writes — so a project remembered on the web shows
        // up on the phone and vice versa. The Convex row has no absolute path,
        // so matching runs on name/remote; the local AsyncStorage row carries
        // the path and is used only when Convex has nothing for this device.
        const convexLast = await loadLastTaskProjectFromConvex(token, runnerDeviceId);
        const last = convexLast ?? (await loadLastTaskProject(runnerDeviceId));
        if (cancelled) return;
        const match = last
          ? normalizedProjects.find((project) =>
              (last.path && project.path === last.path) ||
              project.name.toLowerCase() === last.name.toLowerCase() ||
              projectNameFromPath(project.path)?.toLowerCase() === last.name.toLowerCase())
          : null;
        if (match) {
          setSelectedProjectPath(match.path);
          return;
        }
      }
      // No remembered project means no project. The first discovery row is
      // inventory order, not user intent; a prompt remains immediately
      // sendable and the machine applies its preferred runner.
    })();
    return () => {
      cancelled = true;
    };
  }, [activeDevice?.id, isEffectivelyConnected, routeProjectDir, runnerSelectionDeviceId, selectedProjectPath]);

  // Fetch agent info (project, todo stats) every 5s
  useEffect(() => {
    if (!isEffectivelyConnected) return;
    const fetchInfo = async () => {
      try {
        const info = await quicClient.agentInfo();
        setProjectName(normalizeProjectChipName(info.project?.name));
        setProjectBranch(info.project?.gitBranch ?? "");
        setTodoCount(info.todoCount ?? 0);
        setTodoTotal(info.todoTotal ?? 0);
        setTodoDone(info.todoDone ?? 0);
      } catch {}
    };
    fetchInfo();
    const interval = setInterval(fetchInfo, 5000);
    return () => clearInterval(interval);
  }, [isEffectivelyConnected]);
  // Show Retry on a normal drop AND on the terminal "Can't connect" state
  // the reachability auto-connect lands in when no device responds.
  const showRetryButton = (connectionStatus === "disconnected" || connectionStatus === "error") && !userDisconnected;
  // Show the attempt counter while we're actively retrying (attempt > 0 and
  // not yet connected). Clamp to max so the display never exceeds N/max.
  const showReconnectProgress =
    reconnectAttempt > 0 && !isEffectivelyConnected && !!activeDevice;
  const displayedAttempt = Math.min(reconnectAttempt, quicClient.maxReconnectAttempts);

  // anyPoolConnected is computed earlier next to effectiveState (kept there so
  // the banner promotion can reuse it). Aliased for readability: a live pooled
  // client means the user HAS a box to send a task to, even when this tab's
  // focused client has momentarily slipped to "disconnected".
  const hasAnyPooledConnection = anyPoolConnected;
  const canComposeTask = canComposeWithRemoteless(
    isEffectivelyConnected || hasAnyPooledConnection,
    user?.isOwner,
  );

  // The FAB's handler, hoisted so the "All Clear" empty state can offer the
  // same action — the old copy pointed at a + button that scrolls off-screen
  // on short viewports. Both call sites are gated on canComposeTask, so the
  // action can never be rendered in a state where it wouldn't work.
  const openCreateTask = useCallback(() => {
    // Defensive reset — guarantees the modal opens cleanly even if a previous
    // cancel/backdrop-dismiss left stale state around.
    setNewTaskText("");
    setAttachedImages([]);
    setInputFromSpeech(false);
    setShowTaskOptions(false);
    pendingOpenTaskRef.current = null;
    // multiTargetMode without an active connection falls through to the wizard
    // so the user can pick a target before they even see the composer.
    setPendingTarget(null);
    if (multiTargetMode && (!activeDevice || !isEffectivelyConnected)) {
      setShowTargetWizard(true);
    } else {
      setShowNewTask(true);
    }
  }, [multiTargetMode, activeDevice, isEffectivelyConnected]);

  // Transient zero-device state for a user who HAS had devices (VPN flap,
  // network drop, token drift). Kept OUT of NoMachineEmpty: with an empty
  // roster its "Choose machine" picker would open onto nothing, so the only
  // honest action here is re-fetching the list.
  const devicesDroppedOut = devices.length === 0 && everHadDevices && !isLoadingDevices;
  // Zero devices AND never had any → NoMachineEmpty runs the pairing flow.
  // Only then is "build on this phone" a meaningful escape hatch.
  const hasZeroDevices = devices.length === 0 && !isLoadingDevices;

  // Memoized for the same reason buildTaskPreviewText is bounded: this
  // walks every turn AND runs the whole live output buffer through the
  // markdown/ANSI pipeline. Unmemoized it re-ran on EVERY render of this
  // screen — including the constant re-renders a streaming task causes —
  // which pegs the JS thread and freezes the chat exactly like the list.
  // Cap-safe key: output is append-only but capOutput() trims from the
  // HEAD at MAX_OUTPUT_LINES_PER_TASK, so a long-running task pins
  // output.length at the cap while still streaming. Length alone would
  // freeze the chat exactly on the tasks that stream the most. First +
  // last line are O(1) and catch both the append and the head-drop.
  const chatMessages = useMemo(
    () => (selectedTask ? buildChatMessages(selectedTask) : []),
    [
      selectedTask?.id,
      selectedTask?.status,
      selectedTask?.resultText,
      selectedTask?.presentation,
      selectedTask?.output.length,
      selectedTask?.output[0],
      selectedTask?.output[(selectedTask?.output.length ?? 1) - 1],
      selectedTask?.turns?.length,
      selectedTask?.pendingFollowUps?.length,
      selectedTask?.pendingFollowUps?.[0]?.input,
      selectedTask?.pendingFollowUps?.[(selectedTask?.pendingFollowUps?.length ?? 1) - 1]?.input,
    ],
  );
  // Pre-compute the last-assistant index once per render (not per row) so
  // FlatList's renderItem can do an O(1) lookup. Token attribution is
  // "show on the LAST assistant bubble only" — recomputing inside
  // renderItem would be O(n) per row, defeating the FlatList win.
  const chatTokenInfo = useMemo(() => {
    let lastAssistantIdx = -1;
    for (let k = chatMessages.length - 1; k >= 0; k--) {
      if (chatMessages[k].role === "assistant") { lastAssistantIdx = k; break; }
    }
    const input = selectedTask?.inputTokens ?? 0;
    const output = selectedTask?.outputTokens ?? 0;
    return { lastAssistantIdx, input, output, showTokens: input + output > 0 };
  }, [chatMessages.length, selectedTask?.inputTokens, selectedTask?.outputTokens]);
  const isRunning = selectedTask?.status === "running" || selectedTask?.status === "queued";
  const taskLogLines = useMemo(() => {
    if (!selectedTask) return [] as string[];
    const lines = selectedTask.output.filter((line) => line.trim());
    if (selectedTask.resultText?.trim()) {
      lines.push(selectedTask.resultText.trim());
    }
    return lines;
  }, [selectedTask?.id, selectedTask?.output, selectedTask?.resultText]);
  const combinedLogText = useMemo(() => {
    const sections: string[] = [];
    if (selectedTask) {
      const taskSection = [
        `Task: ${normalizeTaskTitle(selectedTask.title)}`,
        `Status: ${selectedTask.status}`,
        "",
        ...taskLogLines,
      ].join("\n");
      sections.push(taskSection.trim());
    }
    if (logs.length > 0) {
      const connectionSection = [
        "Connection",
        ...logs.map((l) => `${new Date(l.timestamp).toLocaleTimeString()} [${l.level}] ${l.message}`),
      ].join("\n");
      sections.push(connectionSection);
    }
    return sections.filter(Boolean).join("\n\n");
  }, [logs, selectedTask, taskLogLines]);
  // The runner the banner describes = the runner a Send would actually
  // use: the composer chip pick when the user tapped one this session,
  // otherwise the per-device primary, otherwise the default. Same
  // resolver the send path uses (resolveRunnerForRemoteSend) so the
  // header can never disagree with what actually runs.
  const bannerRunnerId = useMemo(
    () =>
      resolveRunnerForRemoteSend({
        activeDeviceId: runnerSelectionDeviceId,
        dispatchDeviceId: runnerSelectionDeviceId,
        primaryRunnerByDevice,
        selectedRunner,
        userPickedRunner: userPickedRunnerRef.current,
      }),
    [primaryRunnerByDevice, runnerSelectionDeviceId, selectedRunner],
  );
  const runnerBannerState = useMemo(
    () => deriveRunnerBannerState(availableRunners, agentStatus, bannerRunnerId, runnersFetchState),
    [availableRunners, agentStatus, bannerRunnerId, runnersFetchState]
  );

  return (
    <SafeAreaView style={[s.safeArea, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <View style={s.container}>
        <RemoteBoxBanner
          extra={
            <>
              {taskExecutionPlacement.lane === "remote" && taskExecutionPlacement.banner ? (
                <View style={s.bannerActionRow}>
                  <Ionicons name="git-compare-outline" size={14} color={c.warn} />
                  <Text style={[s.bannerStatusCopy, { color: c.warn, flex: 1 }]} numberOfLines={2}>
                    {taskExecutionPlacement.banner}
                  </Text>
                </View>
              ) : phoneFallbackInUse ? (
                <View style={s.bannerActionRow}>
                  <Ionicons name="phone-portrait-outline" size={14} color={c.warn} />
                  <Text style={[s.bannerStatusCopy, { color: c.warn, flex: 1 }]} numberOfLines={2}>
                    {taskExecutionPlacement.banner}
                  </Text>
                  <Pressable
                    style={[s.bannerInlineBtn, { backgroundColor: c.accentSoft }]}
                    onPress={() => taskRouter.navigate("/devices" as any)}
                  >
                    <Text style={[s.bannerInlineBtnText, { color: c.accent }]}>Devices</Text>
                  </Pressable>
                </View>
              ) : null}
              {showReconnectProgress || showRetryButton ? (
                <View style={s.bannerActionRow}>
                  {showReconnectProgress ? (
                    <>
                      <Text style={[s.bannerStatusCopy, { color: c.textSecondary, fontFamily: monoFamily }]}>
                        reconnect {displayedAttempt}/{quicClient.maxReconnectAttempts}
                      </Text>
                      <Pressable
                        style={[s.bannerInlineBtn, { backgroundColor: c.errorBg }]}
                        onPress={() => { stopReconnectAndBounce().catch(() => {}); }}
                      >
                        <Text style={[s.bannerInlineBtnText, { color: c.error }]}>Stop</Text>
                      </Pressable>
                    </>
                  ) : null}
                  {!showReconnectProgress && showRetryButton ? (
                    <>
                      {connectionStatus === "error" && lastError ? (
                        <Text style={[s.bannerStatusCopy, { color: c.error, flexShrink: 1, marginRight: 8 }]} numberOfLines={2}>
                          {lastError}
                        </Text>
                      ) : null}
                      <Pressable
                        style={[s.bannerInlineBtn, { backgroundColor: c.accentSoft }]}
                        onPress={() => retryConnection()}
                      >
                        <Text style={[s.bannerInlineBtnText, { color: c.accent }]}>Retry</Text>
                      </Pressable>
                      {activeDevice && (activeDevice.needsAuth || !activeDevice.online) ? (
                        <Pressable
                          style={[s.bannerInlineBtn, { backgroundColor: c.warnBg }]}
                          onPress={() => { recoverDeviceAuth(activeDevice).catch(() => {}); }}
                        >
                          <Text style={[s.bannerInlineBtnText, { color: c.warn }]}>Re-auth</Text>
                        </Pressable>
                      ) : null}
                    </>
                  ) : null}
                  <Pressable
                    style={[s.bannerInlineBtn, { backgroundColor: c.surfaceMuted }]}
                    onPress={() => setShowLogs(true)}
                  >
                    <Text style={[s.bannerInlineBtnText, { color: c.textSecondary }]}>View Logs</Text>
                  </Pressable>
                </View>
              ) : null}
              {isEffectivelyConnected && agentAuthExpired ? (
                <View style={s.bannerActionRow}>
                  <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: c.warn }} />
                  <Text style={[s.bannerStatusCopy, { color: c.warn, flex: 1 }]}>
                    Machine is up but Yaver auth on it expired.
                  </Text>
                  {activeDevice ? (
                    <Pressable
                      onPress={async () => {
                        if (isReconnecting || recoveringDeviceId === activeDevice.id) return;
                        setRecoveringDeviceId(activeDevice.id);
                        setIsReconnecting(true);
                        try {
                          const result = await recoverDeviceAuth(activeDevice);
                          if (result?.ok) {
                            await selectDevice(activeDevice);
                            return;
                          }
                          if (result?.rateLimited) {
                            Alert.alert(
                              "Agent rate-limited",
                              `Agent's per-IP recovery cooldown is still active (5s window). Wait a few seconds and tap Re-auth again.\n\n${appTag()}`,
                            );
                            return;
                          }
                          Alert.alert(
                            "Re-auth Failed",
                            `${result?.error || `Could not recover ${activeDevice.name}.`}\n\n${appTag()}`,
                          );
                        } catch (e: any) {
                          Alert.alert("Re-auth Failed", `${e?.message || "Unexpected error."}\n\n${appTag()}`);
                        } finally {
                          setRecoveringDeviceId((cur) => (cur === activeDevice.id ? null : cur));
                          setIsReconnecting(false);
                        }
                      }}
                      disabled={isReconnecting || recoveringDeviceId === activeDevice.id}
                      style={[s.bannerInlineBtn, { backgroundColor: c.warnBg, opacity: isReconnecting || recoveringDeviceId === activeDevice.id ? 0.5 : 1 }]}
                    >
                      <Text style={[s.bannerInlineBtnText, { color: c.warn }]}>
                        {recoveringDeviceId === activeDevice.id ? "Re-authing…" : "Re-auth"}
                      </Text>
                    </Pressable>
                  ) : null}
                </View>
              ) : null}
              {activeDevice && isEffectivelyConnected && !agentAuthExpired ? (
                <View style={s.bannerMetaRow}>
                  <View style={s.bannerTransportRow}>
                    <Ionicons
                      name={connMode === "direct" ? "wifi-outline" : "radio-outline"}
                      size={16}
                      color={connMode === "direct" ? c.success : c.info}
                    />
                    <Text style={[s.bannerStatusCopy, { color: c.textSecondary }]}>
                      {/* This row only renders when isEffectivelyConnected, so
                          the box IS reachable. connMode can still be null when
                          native QUIC hasn't handshaked (e.g. the iOS simulator,
                          where UDP/QUIC is unreliable) while requests actually
                          flow over the HTTP relay proxy. That is a RELAY path,
                          not "pending" — showing "Transport pending" on a
                          working connection is the desync that read as stuck. */}
                      {connMode === "direct" ? "Direct" : connMode === "tunnel" ? "Tunnel" : "Relay"}
                    </Text>
                    {runnerBannerState ? (
                      <Pressable
                        onPress={(event) => {
                          event.stopPropagation();
                          setShowBannerRunnerChoices((shown) => !shown);
                        }}
                        hitSlop={6}
                        accessibilityRole="button"
                        accessibilityLabel={`Change coding agent on ${runnerSelectionDevice?.name || "selected machine"}`}
                        accessibilityState={{ expanded: showBannerRunnerChoices }}
                        style={{ minWidth: 0, flexShrink: 1 }}
                      >
                        <Text style={[s.bannerStatusCopy, { color: c.textSecondary }]} numberOfLines={1}>
                          · {runnerBannerState.text} ▾
                        </Text>
                      </Pressable>
                    ) : null}
                  </View>
                  <View style={s.bannerStatusRow}>
                    {pingRtt !== null ? (
                      <Pressable onPress={handlePing}>
                        <Badge
                          variant={pingRtt === -1 ? "warning" : "live"}
                          label={isPinging ? "..." : pingRtt === -1 ? "no response" : `${pingRtt}ms`}
                        />
                      </Pressable>
                    ) : (
                      // Un-pinged state: a proper tappable chip (icon + label)
                      // instead of bare muted text, so it reads as an action
                      // and matches the Retry / View Logs / latency-Badge pills.
                      <Pressable
                        onPress={handlePing}
                        disabled={isPinging}
                        hitSlop={6}
                        style={[s.bannerInlineBtn, { backgroundColor: c.surfaceMuted, flexDirection: "row", alignItems: "center", gap: 5 }]}
                      >
                        <Ionicons name="pulse-outline" size={13} color={c.textSecondary} />
                        <Text style={[s.bannerInlineBtnText, { color: c.textSecondary }]}>
                          {isPinging ? "Pinging…" : "Ping"}
                        </Text>
                      </Pressable>
                    )}
                    {runnerBannerState?.action === "configure" ? (
                      <Pressable
                        onPress={() => {
                          setOpenCodeConfigTarget(activeDevice?.id || null);
                          setOpenCodeConfigStartInAdd(true);
                          setShowOpenCodeConfig(true);
                        }}
                        style={[s.bannerInlineBtn, { backgroundColor: c.accentSoft, flexDirection: "row", alignItems: "center", gap: 5 }]}
                      >
                        <Ionicons name="settings-outline" size={13} color={c.accent} />
                        <Text style={[s.bannerInlineBtnText, { color: c.accent }]}>Configure</Text>
                      </Pressable>
                    ) : runnerBannerState?.action === "signIn" ? (
                      // "X needs sign-in" used to be the ONE banner state with
                      // no action — it named the problem and left the user to
                      // find the remote sign-in flow on their own, on a machine
                      // they may have no shell access to. Restart was correctly
                      // excluded here (restarting a signed-out runner just
                      // reproduces the same state); the mistake was excluding
                      // it without putting anything in its place.
                      <Pressable
                        onPress={() =>
                          openRunnerAuthModal(
                            runnerBannerState.runnerId || selectedRunnerRow?.id || "claude",
                            activeDevice?.id || null,
                          )
                        }
                        style={[s.bannerInlineBtn, { backgroundColor: c.accentSoft, flexDirection: "row", alignItems: "center", gap: 5 }]}
                      >
                        <Ionicons name="log-in-outline" size={13} color={c.accent} />
                        <Text style={[s.bannerInlineBtnText, { color: c.accent }]}>Sign in</Text>
                      </Pressable>
                    ) : runnerBannerState?.action === "install" ? (
                      <Pressable
                        onPress={() => void handleInstallRunner(runnerBannerState.runnerId || bannerRunnerId)}
                        disabled={runnerInstallState?.kind === "installing"}
                        style={[s.bannerInlineBtn, { backgroundColor: c.accentSoft }]}
                      >
                        <Text style={[s.bannerInlineBtnText, { color: c.accent }]}>
                          {runnerInstallState?.kind === "installing" ? "Installing…" : "Install"}
                        </Text>
                      </Pressable>
                    ) : runnerBannerState?.action === "restart" &&
                      (availableRunners.length > 0 || agentStatus) ? (
                      <Pressable
                        onPress={handleRestartRunner}
                        disabled={isRestartingRunner}
                        style={[s.bannerInlineBtn, { backgroundColor: c.accentSoft }]}
                      >
                        <Text style={[s.bannerInlineBtnText, { color: c.accent }]}>
                          {isRestartingRunner ? "Restarting..." : "Restart"}
                        </Text>
                      </Pressable>
                    ) : runnerBannerState?.action === "retry" ? (
                      <Pressable
                        onPress={() => {
                          void refreshRunnerState();
                        }}
                        style={[s.bannerInlineBtn, { backgroundColor: c.accentSoft }]}
                      >
                        <Text style={[s.bannerInlineBtnText, { color: c.accent }]}>Retry</Text>
                      </Pressable>
                    ) : null}
                  </View>
                </View>
              ) : null}
              {showBannerRunnerChoices && runnerSelectionDeviceId ? (
                <View
                  style={s.bannerActionRow}
                  accessibilityLabel="Tasks banner coding agent choices"
                >
                  {(["claude", "codex", "opencode"] as const).map((runnerId) => {
                    const selected = normalizeTaskRunnerId(bannerRunnerId) === runnerId;
                    const row = availableRunners.find(
                      (runner) => normalizeTaskRunnerId(runner.id) === runnerId,
                    );
                    return (
                      <Pressable
                        key={runnerId}
                        onPress={async (event) => {
                          event.stopPropagation();
                          const previousRunner = selectedRunner;
                          setSelectedRunner(runnerId);
                          userPickedRunnerRef.current = true;
                          userPickedModelRef.current = false;
                          setShowBannerRunnerChoices(false);
                          try {
                            await setPrimaryRunnerForDevice(runnerSelectionDeviceId, runnerId, null);
                          } catch (error) {
                            setSelectedRunner(previousRunner);
                            userPickedRunnerRef.current = false;
                            Alert.alert("Couldn't save coding agent", error instanceof Error ? error.message : String(error));
                          }
                        }}
                        accessibilityRole="button"
                        accessibilityLabel={`Use ${displayRunnerLabel(runnerId)} on ${runnerSelectionDevice?.name || "selected machine"}`}
                        style={[
                          s.bannerInlineBtn,
                          {
                            backgroundColor: selected ? c.accentSoft : c.surfaceMuted,
                            borderWidth: 1,
                            borderColor: selected ? c.accent : c.borderSubtle,
                          },
                        ]}
                      >
                        <Text style={[s.bannerInlineBtnText, { color: selected ? c.accent : c.textSecondary }]}>
                          {displayRunnerLabel(runnerId)}{row?.installed === false ? " · install" : ""}
                        </Text>
                      </Pressable>
                    );
                  })}
                </View>
              ) : null}
            </>
          }
        />

        {/* Dev server preview banner */}
        {isEffectivelyConnected && !attachedDogfoodRuntime ? (
          <View style={{ marginTop: 12 }}><DevPreview /></View>
        ) : null}

        {/* Project chip + Todo queue bar */}
        {isEffectivelyConnected && (projectName || todoTotal > 0) && (
          <View style={[s.projectBar, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            {projectName ? (
              <View style={s.projectChipMobile}>
                <Text style={[s.projectChipIcon, { color: c.accent }]}>{"\u25CF"}</Text>
                <Text style={[s.projectChipName, { color: c.textPrimary }]}>{projectName}</Text>
                {projectBranch ? (
                  <Text style={[s.projectChipBranch, { color: c.textMuted }]}>{projectBranch}</Text>
                ) : null}
              </View>
            ) : null}
            {todoTotal > 0 && (
              <View style={s.todoBarStats}>
                <Text style={[s.todoBarLabel, { color: "#f59e0b" }]}>
                  {"\u{1F4CB}"} {todoDone}/{todoTotal}
                </Text>
                {todoCount > 0 && (
                  <Text style={[s.todoBarPending, { color: c.textMuted }]}>
                    {todoCount} pending
                  </Text>
                )}
              </View>
            )}
          </View>
        )}

        {/* Ping result overlay */}
        {showPingResult && pingResult && (
          <Pressable
            style={[s.pingOverlay, { backgroundColor: c.bgCard, borderColor: c.border }]}
            onPress={() => setShowPingResult(false)}
          >
            <Text style={[s.pingTitle, { color: c.textPrimary }]}>
              {pingResult.ok ? "Pong!" : "Ping failed"}
            </Text>
            {pingResult.ok ? (
              <>
                <Text style={[s.pingDetail, { color: c.textSecondary }]}>
                  {pingResult.hostname || activeDevice?.name}
                </Text>
                <Text style={[s.pingDetail, { color: c.textSecondary }]}>
                  via {pingResult.mode || "unknown"} {"\u00b7"} {pingResult.rttMs}ms
                </Text>
                <View style={[s.pingBar, { backgroundColor: c.border }]}>
                  <View style={[s.pingBarFill, {
                    width: `${Math.min(100, Math.max(5, pingResult.rttMs / 5))}%`,
                    backgroundColor: pingResult.rttMs < 100 ? "#22c55e" : pingResult.rttMs < 300 ? "#eab308" : "#ef4444",
                  }]} />
                </View>
              </>
            ) : (
              <Text style={[s.pingDetail, { color: "#ef4444" }]}>Agent unreachable</Text>
            )}
            <Text style={[s.pingDismiss, { color: c.textMuted }]}>tap to dismiss</Text>
          </Pressable>
        )}

        {/* Filter chips + action bar */}
        {(isEffectivelyConnected || tasks.length > 0) && (
          <View style={[s.actionBar, { borderBottomColor: c.border }]}>
            <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 6, paddingLeft: 2, paddingRight: 8 }}>
              {selectingTasks ? (
                <>
                  <Pressable
                    style={[s.utilityButton, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }]}
                    onPress={cancelTaskSelection}
                    accessibilityRole="button"
                    accessibilityLabel="Cancel task selection"
                  >
                    <Text style={[s.actionButtonText, { color: c.textSecondary }]}>Cancel</Text>
                  </Pressable>
                  <Pressable
                    style={[s.utilityButton, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }]}
                    onPress={toggleSelectAllVisibleTasks}
                    accessibilityRole="button"
                    accessibilityLabel="Select all visible tasks"
                  >
                    <Text style={[s.actionButtonText, { color: c.accent }]}>Select all</Text>
                  </Pressable>
                  <Pressable
                    style={[s.utilityButton, { backgroundColor: selectedBulkTaskKeys.size > 0 ? c.errorBg : c.bgInput, borderColor: c.borderSubtle, opacity: selectedBulkTaskKeys.size > 0 ? 1 : 0.5 }]}
                    onPress={() => { void handleDeleteSelectedTasks(); }}
                    disabled={selectedBulkTaskKeys.size === 0}
                    accessibilityRole="button"
                    accessibilityLabel={`Delete ${selectedBulkTaskKeys.size} selected tasks`}
                  >
                    <Text style={[s.actionButtonText, { color: c.error }]}>Delete · {selectedBulkTaskKeys.size}</Text>
                  </Pressable>
                </>
              ) : <>
              {tasks.length > 0 ? (
                <Pressable
                  style={[s.utilityButton, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }]}
                  onPress={() => beginTaskSelection()}
                  accessibilityRole="button"
                  accessibilityLabel="Select tasks"
                >
                  <Text style={[s.actionButtonText, { color: c.accent }]}>Select</Text>
                </Pressable>
              ) : null}
              {([
                { key: "running" as const, label: "Active", color: c.accent, count: tasks.filter(t => t.status === "running" || t.status === "queued").length },
                { key: "review" as const, label: "Review", color: c.success, count: tasks.filter(t => t.status === "review" || t.status === "ready").length },
                { key: "completed" as const, label: "Completed", color: "#22c55e", count: tasks.filter(t => t.status === "completed").length },
                { key: "failed" as const, label: "Failed", color: "#ef4444", count: tasks.filter(t => t.status === "failed" || t.status === "stopped").length },
                { key: "all" as const, label: "All", color: c.textSecondary, count: tasks.length },
              ] as const).map(chip => (
                <Pressable
                  key={chip.key}
                  onPress={() => {
                    // Tapping the Active chip while it's already selected opens
                    // the bulk-action popup (stop / remove all active); the first
                    // tap just selects the filter.
                    if (chip.key === "running" && effectiveFilter === "running") {
                      handleActiveBulkActions();
                    } else {
                      setStatusFilter(chip.key);
                    }
                  }}
                  onLongPress={chip.key === "running" ? handleActiveBulkActions : undefined}
                  style={[s.actionButton, {
                    backgroundColor: (effectiveFilter === chip.key) ? withAlpha(chip.color, "1f") : c.bgInput,
                    borderWidth: 1,
                    borderColor: (effectiveFilter === chip.key) ? withAlpha(chip.color, "60") : "transparent",
                  }]}
                >
                  <Text style={[s.actionButtonText, { color: (effectiveFilter === chip.key) ? chip.color : c.textSecondary }]}>
                    {chip.label}
                    <Text style={{ color: c.textMuted }}>{` · ${chip.count}`}</Text>
                  </Text>
                </Pressable>
              ))}
              <View style={[s.actionDivider, { backgroundColor: c.borderSubtle }]} />
              {isEffectivelyConnected && tasks.some(t => t.status === "running") && (
                <Pressable style={[s.utilityButton, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }]} onPress={handleStopAll}>
                  <Text style={[s.actionButtonText, { color: "#ef4444" }]}>Stop All</Text>
                </Pressable>
              )}
              {tasks.some(t => t.status !== "running" && t.status !== "queued") && (
                <Pressable style={[s.utilityButton, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }]} onPress={handleDeleteAll}>
                  <Text style={[s.actionButtonText, { color: c.textMuted }]}>Clear</Text>
                </Pressable>
              )}
              <Pressable style={[s.utilityButton, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }]} onPress={() => setShowLogs(true)}>
                <Text style={[s.actionButtonText, { color: "#94a3b8" }]}>Logs</Text>
              </Pressable>
              {isEffectivelyConnected && (
                <Pressable style={[s.utilityButton, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }]} onPress={handleShowSummary}>
                  <Text style={[s.actionButtonText, { color: "#06b6d4" }]}>Summary</Text>
                </Pressable>
              )}
              </>}
            </ScrollView>
            <View pointerEvents="none" style={[s.actionBarFade, { backgroundColor: c.bg }]} />
          </View>
        )}

        <FlatList
          data={displayTasks}
          keyExtractor={(item) => scopedTaskIdentity(item.deviceId, item.id)}
          // Always bounce so pull-to-refresh (RefreshControl below) works even
          // in the empty / no-machine state — pulling down re-scans for devices.
          alwaysBounceVertical
          // Tablet portrait: 2-col grid for created tasks. Tablet
          // landscape: stays single column because the right pane
          // already shows the selected chat — a narrow 2-col grid
          // there would crush card content. Phone: single column.
          // numColumns can't change without remounting; key forces
          // remount on rotation.
          key={`tasks-cols-${tabletDualPane ? 1 : (layout.layoutClass === "tablet-portrait" ? 2 : 1)}`}
          numColumns={tabletDualPane ? 1 : (layout.layoutClass === "tablet-portrait" ? 2 : 1)}
          columnWrapperStyle={!tabletDualPane && layout.layoutClass === "tablet-portrait" ? { gap: 12 } : undefined}
          contentContainerStyle={[s.listContent, displayTasks.length === 0 && s.listContentEmpty, tabletDualPane ? null : tabletContent]}
          refreshControl={
            <RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={c.accent} colors={[c.accent]} progressBackgroundColor={c.bgCard} />
          }
          ListEmptyComponent={
            // Belt-and-suspenders: also consider raw pool state. If ANY pool
            // client is live, the user has a connected box to send tasks to,
            // so this is the "All Clear" empty state — not a no-machine one.
            // Without it a stale effectiveState (mid-transition) would briefly
            // surface "Pick a machine" while Devices shows green CONNECTED.
            canComposeTask ? (
              <EmptyState
                icon="file-tray-outline"
                title="All Clear"
                body="No tasks yet. Start one here or in a coding terminal on your machine."
                action={{ label: "New task", onPress: openCreateTask }}
              />
            ) : codingMode === "local-only" ? (
              <EmptyState
                icon="phone-portrait-outline"
                title="Code on this phone"
                body="Choose a phone checkout and use DeepSeek. Remote builds, shells, tests, previews, and deploys stay off."
                action={{ label: "New task", onPress: openCreateTask }}
              />
            ) : devices.length === 1 && connectionStatus === "connecting" ? (
              <EmptyState busy title="Connecting…" body={devices[0].name} />
            ) : devicesDroppedOut ? (
              <EmptyState
                icon="cloud-offline-outline"
                title="Reconnecting…"
                body="Your machines aren't answering. This is usually a VPN or network blip."
                action={{
                  label: "Refresh",
                  busy: isRefreshingDevices,
                  onPress: async () => {
                    if (isRefreshingDevices) return;
                    setIsRefreshingDevices(true);
                    try { await refreshDevices(); } finally { setIsRefreshingDevices(false); }
                  },
                }}
                link={{ label: "Build on this phone", onPress: () => taskRouter.navigate("/phone-projects" as any) }}
              />
            ) : (
              <View>
                {/* An auth error is a real error, not an empty state, so it
                    keeps its warn-tinted frame and sits ABOVE the empty state
                    rather than competing with it for the primary action. The
                    generic "connect a computer" copy below is misleading on its
                    own here — the user may already have machines that simply
                    failed to load behind a stale token. */}
                {deviceListError ? (
                  <View style={[s.discoverErrorCard, { borderColor: withAlpha(c.warn, "55"), backgroundColor: withAlpha(c.warn, "12") }]}>
                    <Text style={[s.discoverErrorText, { color: c.textPrimary }]}>
                      Couldn't load your devices. If you have machines paired, this is usually a stale sign-in on this phone.
                    </Text>
                    <Text style={[s.discoverHelper, { color: c.textMuted, marginTop: 4 }]} numberOfLines={2}>
                      {deviceListError}
                    </Text>
                    <Pressable
                      style={[s.discoverSecondaryBtn, { borderColor: c.border, marginTop: 10 }]}
                      onPress={async () => { try { await logout(); } catch {} }}
                    >
                      <Text style={[s.discoverBtnText, { color: c.textPrimary }]}>Sign in again</Text>
                    </Pressable>
                  </View>
                ) : null}

                <NoMachineEmpty
                  noun="tasks"
                  onDeviceChange={() => { void fetchTasks(); }}
                />

                {/* Escape hatches NoMachineEmpty can't own. Only shown with a
                    zero-device roster, where its action is the pairing flow:
                    the phone sandbox needs no machine, and a blank roster often
                    means the boxes live under a different sign-in. Both are
                    quiet links, never a second primary. */}
                {hasZeroDevices ? (
                  <View style={s.emptyEscapeHatches}>
                    <Pressable
                      hitSlop={8}
                      style={s.emptyEscapeLink}
                      onPress={() => taskRouter.navigate("/phone-projects" as any)}
                    >
                      <Text style={[s.emptyEscapeText, { color: c.accent }]}>
                        Or build on this phone
                      </Text>
                    </Pressable>
                    <Pressable
                      hitSlop={8}
                      style={s.emptyEscapeLink}
                      onPress={() => taskRouter.navigate("/(tabs)/settings?linkAccount=1" as any)}
                    >
                      <Text style={[s.emptyEscapeText, { color: c.textMuted }]}>
                        Already use Yaver with another sign-in? Link it
                      </Text>
                    </Pressable>
                  </View>
                ) : null}
              </View>
            )
          }
          renderItem={({ item }) => {
            const inGrid = !tabletDualPane && layout.layoutClass === "tablet-portrait";
            const card = (
              <TaskCard
                item={item}
                onPress={() => selectingTasks ? toggleTaskSelection(item) : setSelectedTask(item)}
                onDelete={() => handleDeleteTask(item)}
                onComplete={() => handleCompleteTask(item.id)}
                onBlockedAction={handlePendingCloudBlockedAction}
                selectionMode={selectingTasks}
                selected={selectedBulkTaskKeys.has(taskBulkKey(item))}
                onToggleSelection={() => toggleTaskSelection(item)}
                onEnterSelection={() => beginTaskSelection(item)}
              />
            );
            // Wrap in flex View when 2-col so each cell takes 50%.
            return inGrid ? (
              <View style={{ flex: 1, maxWidth: "50%" }}>{card}</View>
            ) : card;
          }}
        />

        {/* Single FAB: voice. Texting a coding agent from a phone is a poor
            vibing experience, so the mic is the one primary action here.

            It opens THIS screen's composer and starts dictating — it does not
            navigate to Vibe. Vibe is a hands-free conversation loop: it decides
            when you finished talking and dispatches on its own, so there is no
            moment where you read what you are about to send. Speaking into a
            coding agent without seeing the text first is not something anyone
            wants (2026-07-20): STT mangles paths, flags and identifiers, and
            the whole point is to fix it before it runs. The composer already
            streams whisper partials straight into the input, so you watch the
            words land and press send yourself. Vibe stays reachable from More.

            The compose "+" that used to sit below this was removed on the
            user's ask (2026-07-19). Typing is NOT a dead end: the "All Clear"
            empty state still offers "New task", and Vibe has a keyboard
            affordance that falls back to this same composer. If you ever
            remove one of those two, restore a text entry point here first.

            Rendered as a bare Pressable, not wrapped in a full-screen
            absoluteFillObject layer: that wrapper (even with
            pointerEvents="box-none") regressed the second-open path — after a
            Cancel/backdrop dismiss, taps would silently fall through on
            Android. Keep this simple. */}
        {canComposeTask && (
          <Pressable
            hitSlop={12}
            style={({ pressed }) => [
              s.fab,
              {
                backgroundColor: c.accent,
                bottom: Math.max(insets.bottom + 16, 24),
                shadowColor: c.shadowMd,
              },
              pressed && s.fabPressed,
            ]}
            accessibilityRole="button"
            accessibilityLabel="Start a new chat"
            testID="new-task-button"
            onPress={openCreateTask}
          >
            <Ionicons name="chatbubble-outline" size={26} color="#ffffff" />
          </Pressable>
        )}

        {/* Video summary player — opens when a task's "▶ Watch demo"
            chip is tapped. Plays the clip through the authenticated
            agent path, including relay/direct headers and Range seeks. */}
        <Modal
          visible={!!videoSummaryClipId}
          animationType="fade"
          transparent
          onRequestClose={() => setVideoSummaryClipId(null)}
        >
          <View style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.92)", alignItems: "center", justifyContent: "center" }}>
            <Pressable onPress={() => setVideoSummaryClipId(null)} style={{ position: "absolute", top: 56, right: 24, padding: 12 }}>
              <Text style={{ color: "#fff", fontSize: 18, fontWeight: "700" }}>×</Text>
            </Pressable>
            {videoSummaryClipId && clipUrl(videoSummaryClipId) ? (
              <AuthenticatedVideoPlayer
                key={videoSummaryClipId}
                uri={clipUrl(videoSummaryClipId)}
                headers={quicClient.getAuthHeaders()}
                style={{ width: "100%", height: "70%" }}
                onEnd={() => setVideoSummaryClipId(null)}
              />
            ) : (
              <Text style={{ color: "#888" }}>Loading…</Text>
            )}
          </View>
        </Modal>

        {/* Agent question sheet — opens when the runner calls the
            yaver_ask_user MCP tool while this task is selected. The
            user types/picks an answer, we POST to /tasks/{id}/answer
            (via answerTaskQuestion), the daemon resolves the parked
            /question handler, and the runner's tool call returns
            with the answer. agent_answered / agent_question_cancelled
            SSE events also clear agentQuestion so a second device
            answering doesn't leave this sheet orphaned. */}
        <Modal
          visible={!!agentQuestion}
          animationType="slide"
          transparent
          onRequestClose={() => setAgentQuestion(null)}
        >
          <View style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.55)", justifyContent: "flex-end" }}>
            <View style={{ backgroundColor: c.bg, borderTopLeftRadius: 20, borderTopRightRadius: 20, padding: 20, paddingBottom: 36 }}>
              <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "600", letterSpacing: 0.4, textTransform: "uppercase", marginBottom: 8 }}>
                Agent needs your input
              </Text>
              {agentQuestion?.header ? (
                <View style={{ alignSelf: "flex-start", backgroundColor: c.accent + "22", borderRadius: 6, paddingHorizontal: 8, paddingVertical: 3, marginBottom: 8 }}>
                  <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700", letterSpacing: 0.3 }}>
                    {agentQuestion.header}
                  </Text>
                </View>
              ) : null}
              {agentQuestion?.step ? (
                <View style={{ alignSelf: "flex-start", backgroundColor: c.textMuted + "22", borderRadius: 6, paddingHorizontal: 8, paddingVertical: 3, marginBottom: 8 }}>
                  <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "700", letterSpacing: 0.3 }}>
                    {"⛳ " + String(agentQuestion.step).replace(/_/g, " ")}
                  </Text>
                </View>
              ) : null}
              {agentQuestion?.screenshot ? (
                // F3 handoff: show the relevant page region so the human sees exactly what they're acting on
                <Image
                  source={{ uri: "data:image/png;base64," + agentQuestion.screenshot }}
                  style={{ width: "100%", height: 200, borderRadius: 10, marginBottom: 12, backgroundColor: "#000" }}
                  resizeMode="contain"
                />
              ) : null}
              <Text style={{ color: c.textPrimary, fontSize: 16, lineHeight: 22, marginBottom: 16 }}>
                {agentQuestion?.prompt}
              </Text>

              {agentQuestion?.kind === "choice" && (agentQuestion?.choices || []).length > 0 ? (
                <View style={{ gap: 8 }}>
                  {(agentQuestion?.choices || []).map((choice) => {
                    const picked = agentMultiPicks.includes(choice);
                    return (
                      <Pressable
                        key={choice}
                        disabled={submittingAgentAnswer}
                        onPress={() => {
                          if (!agentQuestion) return;
                          if (agentQuestion.multi) {
                            // Multi-select: toggle, don't submit — the
                            // footer "Send" commits the joined picks.
                            setAgentMultiPicks((prev) =>
                              prev.includes(choice) ? prev.filter((x) => x !== choice) : [...prev, choice],
                            );
                          } else {
                            // Single-select: tap commits immediately
                            // (Claude-Code behaviour).
                            void submitAgentAnswer(choice);
                          }
                        }}
                        style={{
                          flexDirection: "row",
                          alignItems: "center",
                          gap: 10,
                          backgroundColor: picked ? c.accent + "1A" : c.surface,
                          borderRadius: 12,
                          paddingVertical: 14,
                          paddingHorizontal: 16,
                          borderWidth: 1,
                          borderColor: picked ? c.accent : c.border,
                        }}
                      >
                        {agentQuestion?.multi ? (
                          <View
                            style={{
                              width: 20,
                              height: 20,
                              borderRadius: 5,
                              borderWidth: 2,
                              borderColor: picked ? c.accent : c.border,
                              backgroundColor: picked ? c.accent : "transparent",
                              alignItems: "center",
                              justifyContent: "center",
                            }}
                          >
                            {picked ? <Text style={{ color: "#fff", fontSize: 13, fontWeight: "800" }}>✓</Text> : null}
                          </View>
                        ) : null}
                        <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "500", flex: 1 }}>{choice}</Text>
                      </Pressable>
                    );
                  })}
                  {/* Claude-Code parity: a free-text "Other…" is ALWAYS
                      offered for choice questions, so the agent never
                      has to spell one out. Tapping expands an inline
                      text field; the footer "Send" commits it. */}
                  <Pressable
                    disabled={submittingAgentAnswer}
                    onPress={() => setAgentOtherOpen((v) => !v)}
                    style={{
                      backgroundColor: agentOtherOpen ? c.accent + "1A" : "transparent",
                      borderRadius: 12,
                      paddingVertical: 14,
                      paddingHorizontal: 16,
                      borderWidth: 1,
                      borderColor: agentOtherOpen ? c.accent : c.border,
                      borderStyle: "dashed",
                    }}
                  >
                    <Text style={{ color: agentOtherOpen ? c.accent : c.textMuted, fontSize: 15, fontWeight: "500" }}>
                      {agentOtherOpen ? "Other (typing below)" : "Other…"}
                    </Text>
                  </Pressable>
                  {agentOtherOpen ? (
                    <TextInput
                      value={agentAnswerText}
                      onChangeText={setAgentAnswerText}
                      placeholder="Type your own answer…"
                      placeholderTextColor={c.textMuted}
                      autoFocus
                      multiline
                      style={{
                        backgroundColor: c.surface,
                        color: c.textPrimary,
                        borderRadius: 12,
                        paddingHorizontal: 14,
                        paddingVertical: 12,
                        fontSize: 15,
                        borderWidth: 1,
                        borderColor: c.border,
                        minHeight: 64,
                        textAlignVertical: "top",
                      }}
                    />
                  ) : null}
                </View>
              ) : (
                <View>
                  <TextInput
                    value={agentAnswerText}
                    onChangeText={setAgentAnswerText}
                    placeholder={agentQuestion?.kind === "secret" ? "Secret value (not echoed to other devices)" : "Type your answer…"}
                    placeholderTextColor={c.textMuted}
                    secureTextEntry={agentQuestion?.kind === "secret"}
                    autoFocus
                    multiline={agentQuestion?.kind !== "secret"}
                    style={{
                      backgroundColor: c.surface,
                      color: c.textPrimary,
                      borderRadius: 12,
                      paddingHorizontal: 14,
                      paddingVertical: 12,
                      fontSize: 15,
                      borderWidth: 1,
                      borderColor: c.border,
                      minHeight: agentQuestion?.kind === "secret" ? 48 : 80,
                      textAlignVertical: "top",
                    }}
                  />
                  {agentQuestion?.vaultHint ? (
                    <Pressable
                      disabled={submittingAgentAnswer}
                      onPress={async () => {
                        if (!agentQuestion?.vaultHint) return;
                        // Resolve the vault entry server-side and submit
                        // its value as the answer in one round trip; the
                        // value never lives in JS memory beyond this
                        // function. quicClient.getVaultValue is the
                        // existing read endpoint; if it's missing on this
                        // build, fall back to telling the user to paste.
                        try {
                          const v = await (quicClient as unknown as { getVaultValue?: (n: string) => Promise<string | null> }).getVaultValue?.(
                            agentQuestion.vaultHint,
                          );
                          if (typeof v === "string" && v) {
                            setSubmittingAgentAnswer(true);
                            const res = await quicClient.answerTaskQuestion(agentQuestion.taskId, agentQuestion.id, v);
                            setSubmittingAgentAnswer(false);
                            if (!res.ok) {
                              Alert.alert("Could not deliver answer", res.error || "Unknown error");
                              return;
                            }
                            setAgentQuestion(null);
                            return;
                          }
                        } catch {
                          /* fall through to manual paste hint */
                        }
                        Alert.alert(
                          "Vault lookup unavailable",
                          `The agent suggested using the vault entry "${agentQuestion.vaultHint}". This client can't read the vault directly — paste the value manually.`,
                        );
                      }}
                      style={{ marginTop: 10, alignSelf: "flex-start" }}
                    >
                      <Text style={{ color: c.accent, fontSize: 13, fontWeight: "500" }}>
                        Use vault entry: {agentQuestion.vaultHint}
                      </Text>
                    </Pressable>
                  ) : null}
                </View>
              )}

              <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginTop: 18 }}>
                {/* Dismiss is NEVER disabled — on iOS this transparent Modal has
                    no hardware back, so gating Dismiss on submittingAgentAnswer
                    could trap the user in an undismissable sheet if the answer
                    POST stalls. Closing also lets the in-flight answer settle in
                    the background. */}
                <Pressable onPress={() => setAgentQuestion(null)} style={{ paddingVertical: 12, paddingHorizontal: 18 }}>
                  <Text style={{ color: c.textMuted, fontSize: 15 }}>Dismiss</Text>
                </Pressable>
                {(() => {
                  const isChoice = agentQuestion?.kind === "choice";
                  const multi = !!agentQuestion?.multi;
                  // Single-select choices commit on tap, so the footer
                  // Send only appears for text/secret, multi-select, or
                  // when the "Other…" free text is open.
                  const showSend = !isChoice || multi || agentOtherOpen;
                  if (!showSend) return null;
                  const otherText = agentAnswerText.trim();
                  const answer =
                    isChoice && multi
                      ? [...agentMultiPicks, ...(agentOtherOpen && otherText ? [otherText] : [])].join("; ")
                      : agentAnswerText;
                  const enabled = !submittingAgentAnswer && answer.trim().length > 0;
                  return (
                    <Pressable
                      disabled={!enabled}
                      onPress={() => void submitAgentAnswer(answer)}
                      style={{
                        backgroundColor: enabled ? c.accent : c.surface,
                        paddingVertical: 12,
                        paddingHorizontal: 22,
                        borderRadius: 10,
                      }}
                    >
                      <Text style={{ color: enabled ? "#fff" : c.textMuted, fontSize: 15, fontWeight: "600" }}>
                        {submittingAgentAnswer ? "Sending…" : multi ? `Send${agentMultiPicks.length ? ` (${agentMultiPicks.length})` : ""}` : "Send"}
                      </Text>
                    </Pressable>
                  );
                })()}
              </View>
            </View>
          </View>
        </Modal>

        {/* Multi-target wizard. Only mounted when the user opted into
            "Pick machine + agent per task" in Settings; the FAB opens
            this first, and the wizard's onConfirmed sets pendingTarget
            (which locks the runner + model in the compose modal) and
            opens the compose. The wizard's selectDevice already
            switches the QUIC client to the chosen device, so sendTask
            below targets the correct baseUrl without further work. */}
        <TaskTargetWizard
          visible={showTargetWizard}
          onDismiss={flushAfterDismiss}
          onCancel={() => setShowTargetWizard(false)}
          onConfirmed={(target) => {
            setPendingTarget(target);
            handoffModal(() => setShowTargetWizard(false), () => setShowNewTask(true));
          }}
        />

        {/* New Task Modal */}
        <Modal
          visible={showNewTask}
          animationType="slide"
          transparent
          onDismiss={handleNewTaskModalDismiss}
          onRequestClose={() => {
            Keyboard.dismiss();
            setShowTaskOptions(false);
            setShowNewTask(false);
            setNewTaskText("");
            setAttachedImages([]);
            setInputFromSpeech(false);
            setPendingTarget(null);
          }}
        >
          <KeyboardAvoidingView style={s.modalOverlay} behavior={Platform.OS === "ios" ? "padding" : "height"}>
            <Pressable style={s.modalDismiss} onPress={() => { Keyboard.dismiss(); setShowTaskOptions(false); setShowNewTask(false); setNewTaskText(""); setAttachedImages([]); setInputFromSpeech(false); setPendingTarget(null); }} />
            <View
              style={[
                s.modalContent,
                { backgroundColor: c.bgCard, maxHeight: "92%", flexShrink: 1, overflow: "hidden" },
              ]}
            >
              <ScrollView
                style={{ flexShrink: 1 }}
                contentContainerStyle={{ paddingBottom: Math.max(insets.bottom, 16) }}
                keyboardShouldPersistTaps="handled"
                keyboardDismissMode="interactive"
                stickyHeaderIndices={[0]}
                showsVerticalScrollIndicator
                testID="new-task-scroll"
              >
              {/* Two-row header: title + close on top, target chip below.
                  The chip lived inline with the title, but device names
                  like "Mobiles-Mac-mini.local · Claude" overflowed and
                  collided with the title text. Stacking lets the chip
                  use the full row width and show the full label without
                  truncation or layout pressure on the title. */}
              <View
                style={[s.modalHeaderStack, { backgroundColor: c.bgCard, zIndex: 2 }]}
              >
                <View style={s.modalHeaderRow}>
                  <Text style={[s.modalTitle, { color: c.textPrimary }]}>New task</Text>
                  <View style={s.modalHeaderActions}>
                    <Pressable
                      hitSlop={{ top: 12, bottom: 12, left: 12, right: 12 }}
                      onPress={() => setShowTaskOptions((visible) => !visible)}
                      style={({ pressed }) => [s.modalCloseButton, pressed && { opacity: 0.55 }]}
                      accessibilityRole="button"
                      accessibilityLabel={showTaskOptions ? "Hide task options" : "More task options"}
                      accessibilityState={{ expanded: showTaskOptions }}
                      testID="task-options-more"
                    >
                      <Ionicons name="ellipsis-horizontal" size={23} color={c.textSecondary} />
                    </Pressable>
                    <Pressable
                      hitSlop={{ top: 12, bottom: 12, left: 12, right: 12 }}
                      onPress={() => {
                        Keyboard.dismiss();
                        setShowTaskOptions(false);
                        setShowNewTask(false);
                        setNewTaskText("");
                        setAttachedImages([]);
                        setInputFromSpeech(false);
                        setPendingTarget(null);
                      }}
                      style={({ pressed }) => [s.modalCloseButton, pressed && { opacity: 0.55 }]}
                      accessibilityRole="button"
                      accessibilityLabel="Close new task"
                      testID="close-new-task"
                    >
                      <Ionicons name="close" size={24} color={c.textSecondary} />
                    </Pressable>
                  </View>
                </View>
                {/* Target chip row — runner+model pill mirrors the badge
                    in the follow-up bar so the user can pick the agent
                    at task creation, not only after the task starts. */}
                {showTaskOptions ? <>
                <View style={s.modalTargetRow}>
                  {codingMode === "local-only" ? (
                    // Remoteless is an explicit execution target, not an
                    // offline machine state. Never offer "Pick a machine"
                    // here: this turn runs on the phone with DeepSeek.
                    <View
                      style={[
                        s.agentBadge,
                        { backgroundColor: c.bgCardElevated, borderColor: c.accent, flexShrink: 1 },
                      ]}
                      accessibilityLabel="This phone, DeepSeek"
                    >
                      <Text style={[s.agentBadgeText, { color: c.textSecondary, flexShrink: 1 }]} numberOfLines={1}>
                        This phone · DeepSeek
                      </Text>
                    </View>
                  ) : pendingTarget ? (
                    // Locked target chip: when the wizard chose this
                    // device + runner, the picker is non-interactive so
                    // the user can't accidentally redirect a single task
                    // mid-compose. Re-open the wizard to change it.
                    <View
                      style={[
                        s.agentBadge,
                        { backgroundColor: c.bgCardElevated, borderColor: c.accent, flexShrink: 1 },
                      ]}
                    >
                      <Text style={[s.agentBadgeText, { color: c.textSecondary, flexShrink: 1 }]} numberOfLines={1}>
                        {pendingTarget.deviceName} · {
                          pendingTarget.runner === "codex" ? "Codex"
                            : pendingTarget.runner === "opencode" ? "OpenCode"
                              : "Claude"
                        }
                      </Text>
                    </View>
                  ) : (
                  <Pressable
                    hitSlop={{ top: 12, bottom: 12, left: 12, right: 12 }}
                    style={({ pressed }) => [
                      s.agentBadge,
                      { backgroundColor: c.bgCardElevated, borderColor: c.border, flexShrink: 1 },
                      pressed && { opacity: 0.55 },
                    ]}
                    // Opens the full TaskTargetWizard: machine selection,
                    // agent selection, and the per-runner model picker
                    // in one flow. Close compose first so the wizard owns
                    // the screen; on confirm, pendingTarget is set and
                    // the compose modal re-opens with the new target
                    // bound to the next send.
                    onPress={() => {
                      setPendingTarget(null);
                      handoffModal(() => setShowNewTask(false), () => setShowTargetWizard(true));
                    }}
                    accessibilityRole="button"
                    accessibilityLabel="Change device, coding agent, and model for this task"
                  >
                    {/* Pill shows ONLY the machine name — keeps the chip
                        compact; the full device + agent + model picker
                        is one tap away via the wizard launched on press. */}
                    <Text
                      style={[s.agentBadgeText, { color: c.textSecondary, flexShrink: 1 }]}
                      numberOfLines={1}
                    >
                      {/* With a machine-role split active the task runs on the
                          RUNNER box — name it, never imply the focused box. */}
                      {(machineRoles?.runnerDeviceId
                        ? devices.find((d) => d.id === machineRoles.runnerDeviceId)?.name
                        : null) || activeDevice?.name || "Pick a machine"}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 10, marginLeft: 4 }}>▾</Text>
                  </Pressable>
                  )}
                  {/* Coding-agent chip — a quick, inline way to pick Claude
                      Code / Codex / OpenCode without opening the full wizard.
                      Only the agents actually installed on this box are
                      offered, so the picker never lists something that can't
                      run. Hidden while a wizard-locked target is bound. */}
                  {codingMode !== "local-only" && !pendingTarget && (availableRunners.length > 0 || !!selectedRunner) && (
                    <Pressable
                      hitSlop={{ top: 12, bottom: 12, left: 12, right: 12 }}
                      style={({ pressed }) => [
                        s.agentBadge,
                        { backgroundColor: c.bgCardElevated, borderColor: c.border, flexShrink: 1, marginLeft: 8 },
                        pressed && { opacity: 0.55 },
                      ]}
                      onPress={() => setShowComposerRunnerChoices((shown) => !shown)}
                      accessibilityRole="button"
                      accessibilityLabel="Choose coding agent"
                    >
                      <Text style={[s.agentBadgeText, { color: c.textSecondary, flexShrink: 1 }]} numberOfLines={1}>
                        {selectedRunnerRow ? displayRunnerLabel(selectedRunnerRow.id) : "Agent"}
                      </Text>
                      <Text style={{ color: c.textMuted, fontSize: 10, marginLeft: 4 }}>▾</Text>
                    </Pressable>
                  )}
                </View>
                {showComposerRunnerChoices && !pendingTarget ? (
                  <View
                    style={{
                      flexDirection: "row",
                      flexWrap: "wrap",
                      gap: 8,
                      marginTop: 8,
                    }}
                    accessibilityLabel="Coding agent choices"
                  >
                    {["claude", "codex", "opencode"].map((runnerId) => {
                      const row = availableRunners.find(
                        (runner) => normalizeTaskRunnerId(runner.id) === runnerId,
                      );
                      const selected = normalizeTaskRunnerId(selectedRunner) === runnerId;
                      return (
                        <Pressable
                          key={runnerId}
                          onPress={() => {
                            setSelectedRunner(runnerId);
                            setShowComposerRunnerChoices(false);
                            userPickedRunnerRef.current = true;
                            userPickedModelRef.current = false;
                            if (runnerSelectionDeviceId) {
                              void setPrimaryRunnerForDevice(runnerSelectionDeviceId, runnerId, null).catch(() => {});
                            }
                          }}
                          style={({ pressed }) => [
                            s.agentBadge,
                            {
                              backgroundColor: selected ? c.accentSoft : c.bgCardElevated,
                              borderColor: selected ? c.accent : c.border,
                              opacity: pressed ? 0.65 : 1,
                            },
                          ]}
                          accessibilityRole="button"
                          accessibilityLabel={`Use ${displayRunnerLabel(runnerId)} on ${runnerSelectionDevice?.name || "selected machine"}`}
                        >
                          <Text style={[s.agentBadgeText, { color: selected ? c.accent : c.textSecondary }]}>
                            {displayRunnerLabel(runnerId)}
                            {row?.installed === false ? " · install" : ""}
                          </Text>
                          {selected ? <Text style={{ color: c.accent, marginLeft: 5 }}>✓</Text> : null}
                        </Pressable>
                      );
                    })}
                    {availableRunners.length === 0 ? (
                      <Text style={{ color: c.textMuted, fontSize: 11, alignSelf: "center" }}>
                        {runnerFetchAlertMessage(runnersFetchState)}
                      </Text>
                    ) : null}
                  </View>
                ) : null}
                {/* Machine-role split disclosure: two silent sources are two
                    unfalsifiable states — when a split is active, name which
                    box runs the AI and which box renders, right where the
                    user is composing. */}
                {!pendingTarget && machineRoles?.runnerDeviceId && machineRoles?.renderDeviceId && machineRoles.renderDeviceId !== machineRoles.runnerDeviceId ? (
                  <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }} numberOfLines={1}>
                    AI: {devices.find((d) => d.id === machineRoles.runnerDeviceId)?.name || machineRoles.runnerDeviceId.slice(0, 8)}
                    {"  ·  Render: "}
                    {devices.find((d) => d.id === machineRoles.renderDeviceId)?.name || machineRoles.renderDeviceId.slice(0, 8)}
                  </Text>
                ) : null}
                {!pendingTarget && runnerBannerState?.action === "install" ? (
                  <View
                    style={{
                      marginTop: 10,
                      borderRadius: 12,
                      borderWidth: 1,
                      borderColor: "rgba(248,113,113,0.30)",
                      backgroundColor: "rgba(248,113,113,0.10)",
                      padding: 12,
                    }}
                    accessibilityLabel={`${runnerBannerState.text} on ${runnerSelectionDevice?.name || "selected machine"}`}
                  >
                    <Text style={{ color: "#fecaca", fontSize: 12, lineHeight: 18 }}>
                      {runnerBannerState.text} on {runnerSelectionDevice?.name || "this machine"}. Install it here before sending; your prompt stays in the composer.
                    </Text>
                    {runnerInstallState?.runnerId === normalizeTaskRunnerId(runnerBannerState.runnerId || bannerRunnerId) ? (
                      <Text style={{ color: runnerInstallState.kind === "failed" ? c.error : c.textMuted, fontSize: 11, lineHeight: 16, marginTop: 7 }}>
                        {runnerInstallState.line}
                      </Text>
                    ) : null}
                    <Pressable
                      onPress={() => void handleInstallRunner(runnerBannerState.runnerId || bannerRunnerId)}
                      disabled={runnerInstallState?.kind === "installing"}
                      style={{
                        alignSelf: "flex-start",
                        marginTop: 10,
                        borderRadius: 999,
                        backgroundColor: c.accentSoft,
                        paddingHorizontal: 13,
                        paddingVertical: 8,
                        opacity: runnerInstallState?.kind === "installing" ? 0.65 : 1,
                      }}
                    >
                      <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>
                        {runnerInstallState?.kind === "installing" ? "Installing…" : runnerInstallState?.kind === "failed" ? "Retry install" : `Install ${displayRunnerLabel(runnerBannerState.runnerId || bannerRunnerId)}`}
                      </Text>
                    </Pressable>
                  </View>
                ) : null}
                </> : null}
              </View>
              {showTaskOptions ? (
              <View style={s.composerScopeRow}>
                <Pressable
                  style={({ pressed }) => [
                    s.scopeChip,
                    { backgroundColor: c.bgCardElevated, borderColor: selectedComposerProject ? c.accent : c.border },
                    pressed && { opacity: 0.65 },
                  ]}
                  onPress={openProjectPicker}
                  accessibilityRole="button"
                  accessibilityLabel="Configure project and MCPs for this task"
                  testID="composer-project-chip"
                >
                  <Ionicons name="options-outline" size={16} color={selectedComposerProject ? c.accent : c.textMuted} />
                  <Text style={[s.scopeChipText, { color: c.textSecondary }]} numberOfLines={1}>
                    {[
                      selectedComposerProject?.name || projectNameFromPath(projectDir) || "No project",
                      selectedMcpServers.length + (includeYaverMcp ? 1 : 0)
                        ? `${selectedMcpServers.length + (includeYaverMcp ? 1 : 0)} MCP`
                        : "No MCP",
                    ].join(" · ")}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 10 }}>▾</Text>
                </Pressable>
              </View>
              ) : null}
                <View style={[s.composerShell, { backgroundColor: "transparent" }]}>
                  <TextInput
                    style={[s.inputMultiline, s.composerInput, {
                      color: c.textPrimary,
                      backgroundColor: c.bg,
                      borderColor: c.border,
                      borderWidth: 1,
                    }]}
                    placeholder={tasks.length > 0 ? "Send another command…" : "What should the agent do?"}
                    placeholderTextColor={c.textMuted}
                    value={newTaskText}
                    onChangeText={(t) => { newTaskTextRef.current = t; setNewTaskText(t); setTaskSubmitError(null); setInputFromSpeech(false); }}
                    multiline numberOfLines={4} textAlignVertical="top" autoFocus
                    autoCorrect={textCorrectionEnabled}
                    spellCheck={textCorrectionEnabled}
                    autoCapitalize={textCorrectionEnabled ? "sentences" : "none"}
                  />
                  {isTranscribing && (
                    <View style={s.transcribingRow}>
                      <ActivityIndicator size="small" color={c.accent} />
                      <Text style={{ color: c.textMuted, fontSize: 12, marginLeft: 8 }}>Transcribing...</Text>
                    </View>
                  )}
                  {reloadFlash && (
                    <View style={s.transcribingRow}>
                      <Ionicons name="flash" size={14} color={c.accent} />
                      <Text style={{ color: c.textMuted, fontSize: 12, marginLeft: 8 }}>{reloadFlash}</Text>
                    </View>
                  )}
                  {taskSubmitError ? (
                    <View
                      style={{
                        marginTop: 10,
                        borderRadius: 12,
                        borderWidth: 1,
                        borderColor: withAlpha(c.error, "66"),
                        backgroundColor: withAlpha(c.error, "18"),
                        paddingHorizontal: 12,
                        paddingVertical: 10,
                      }}
                      accessibilityRole="alert"
                      accessibilityLabel={`Task not sent. ${taskSubmitError}`}
                      testID="task-submit-error"
                    >
                      <Text style={{ color: c.error, fontSize: 12, fontWeight: "700" }}>Task not sent</Text>
                      <Text style={{ color: c.textSecondary, fontSize: 12, lineHeight: 18, marginTop: 3 }}>
                        {taskSubmitError}
                      </Text>
                      <Text style={{ color: c.textMuted, fontSize: 11, lineHeight: 16, marginTop: 4 }}>
                        Your prompt is preserved. Tap Send to retry, or change the machine or coding agent above.
                      </Text>
                    </View>
                  ) : null}
                  {attachedImages.length > 0 && (
                    <ScrollView horizontal showsHorizontalScrollIndicator={false} style={s.attachmentStrip}>
                      {attachedImages.map((img, i) => (
                        <View key={i} style={s.attachmentPreviewWrap}>
                          <Image source={{ uri: `data:${img.mimeType};base64,${img.base64}` }} style={s.attachmentPreviewImage} />
                          <Pressable onPress={() => setAttachedImages((prev) => prev.filter((_, idx) => idx !== i))} style={[s.attachmentRemove, { backgroundColor: c.error }]}>
                            <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>×</Text>
                          </Pressable>
                        </View>
                      ))}
                    </ScrollView>
                  )}
                  {showTaskOptions ? <>
                  {/* OpenCode quick Build|Plan mode — the two common agents,
                      one tap, persisted to the device the task runs on. Custom
                      agents / Default stay in the task-configuration sheet's
                      OPENCODE AGENT rail. Mirrors the web dashboard's compact
                      segmented control (2026-08-09). Sends `--agent <mode>`
                      via taskParams.mode in handleCreateTask. */}
                  {normalizeTaskRunnerId(resolveRunnerForSend() ?? "") === "opencode" && (
                  <View style={[s.composerModeRow, { borderColor: withAlpha(c.border, "cc") }]}>
                    <Text style={[s.composerModeLabel, { color: c.textMuted }]}>Mode</Text>
                    <View style={s.composerModeSegmented}>
                      {(["build", "plan"] as const).map((mode) => {
                        const active = selectedOpenCodeMode === mode;
                        return (
                          <Pressable
                            key={mode}
                            onPress={() => {
                              taskHaptics.send();
                              setSelectedOpenCodeMode(mode);
                              if (runnerSelectionDeviceId) {
                                void setPrimaryRunnerForDevice(
                                  runnerSelectionDeviceId,
                                  "opencode",
                                  selectedModel || null,
                                  mode,
                                ).catch(() => {});
                              }
                            }}
                            accessibilityRole="radio"
                            accessibilityState={{ selected: active }}
                            accessibilityLabel={`Run opencode in ${mode} mode`}
                            style={[
                              s.composerModeButton,
                              { borderColor: active ? c.accent : c.border },
                              active && { backgroundColor: c.accent + "20" },
                            ]}
                          >
                            <Text style={[s.composerModeText, { color: active ? c.accent : c.textSecondary }]}>
                              {mode === "build" ? "Build" : "Plan"}
                            </Text>
                          </Pressable>
                        );
                      })}
                    </View>
                  </View>
                )}
                {/* Ask / deep-audit toggle — runs the task as a grounded
                    explain-first question-answer (askModePreamble: file:line
                    cites, confirm gate) instead of a work run. The web
                    dashboard has Ask via auto-detect; this is the explicit
                    phone twin so a deep audit can be triggered from the
                    hand (2026-08-12). Works for every runner. */}
                <Pressable
                  onPress={() => {
                    taskHaptics.send();
                    setAskModeEnabled((v) => !v);
                  }}
                  accessibilityRole="switch"
                  accessibilityState={{ checked: askModeEnabled }}
                  accessibilityLabel="Deep audit — answer with file:line evidence, change nothing without asking"
                  style={[s.composerModeRow, { borderColor: withAlpha(c.border, "cc") }]}
                >
                  <Text style={[s.composerModeLabel, { color: c.textMuted }]}>Ask</Text>
                  <View style={[s.composerModeButton, {
                    borderColor: askModeEnabled ? c.accent : c.border,
                    backgroundColor: askModeEnabled ? c.accent + "20" : "transparent",
                  }]}>
                    <Text style={[s.composerModeText, { color: askModeEnabled ? c.accent : c.textSecondary }]}>
                      {askModeEnabled ? "Deep audit on" : "Deep audit"}
                    </Text>
                  </View>
                  {askModeEnabled && (
                    <Text style={{ fontSize: 11, color: c.textMuted, marginLeft: 8, flexShrink: 1 }}>
                      answers with file:line evidence · changes nothing without asking
                    </Text>
                  )}
                </Pressable>
                </> : null}
                <View style={[s.composerFooter, { borderTopColor: withAlpha(c.border, "cc") }]}>
                  <Pressable
                    style={({ pressed }) => [
                      s.composerActionButton,
                      { backgroundColor: c.bgCard },
                      pressed && { opacity: 0.7 },
                    ]}
                    onPress={() => handlePickImage("task")}
                    disabled={attachedImages.length >= 5}
                  >
                    <Ionicons name="add" size={26} color={c.textPrimary} />
                  </Pressable>
                  <View style={s.composerFooterRight}>
                    {/* Mic — dictate the command (writes into the composer).
                        Saying "reload" / "reload <project>" trips the
                        Hermes-reload fast-path in handleCreateTask. The
                        composer mic was retired in the 2026-04-28 voice
                        cut and revived here now that the voice agent is
                        back; it reuses the same startRecording("task")
                        dictation path the follow-up composer already uses. */}
                    <Pressable
                      style={({ pressed }) => [
                        s.composerActionButton,
                        { backgroundColor: isRecording ? c.error : c.bgCard },
                        pressed && { opacity: 0.7 },
                      ]}
                      onPress={() => { if (isRecording) { stopRecordingAndTranscribe(); } else { startRecording("task"); } }}
                      disabled={isSubmitting || isTranscribing}
                    >
                      <Ionicons name={isRecording ? "stop" : "mic-outline"} size={22} color={isRecording ? "#fff" : c.textPrimary} />
                    </Pressable>
                    {silentInputEnabled && Platform.OS === "ios" ? (
                      <Pressable
                        style={({ pressed }) => [
                          s.composerActionButton,
                          { backgroundColor: c.bgCard },
                          pressed && { opacity: 0.7 },
                        ]}
                        onPress={() => {
                          Keyboard.dismiss();
                          // iOS cannot present a native Modal above the task
                          // composer Modal. Hand the surface off atomically;
                          // the transcription returns to this same draft.
                          setShowNewTask(false);
                          setShowSilentInput(true);
                        }}
                        disabled={isSubmitting || isTranscribing || !runnerSelectionDeviceId}
                        accessibilityRole="button"
                        accessibilityLabel="Silent lip-reading input"
                        testID="silent-input-button"
                      >
                        <Text style={{ color: c.textPrimary, fontSize: 20 }}>👄</Text>
                      </Pressable>
                    ) : null}
                    {(() => {
                      const isDisabled =
                        (!newTaskText.trim() && attachedImages.length === 0) ||
                        isSubmitting ||
                        isTranscribing ||
                        (!pendingTarget && runnerBannerState?.action === "install") ||
                        runnerInstallState?.kind === "installing" ||
                        !isEffectivelyConnected;
                      return (
                        <Pressable
                          style={({ pressed }) => [
                            s.sendButtonLarge,
                            isDisabled
                              ? { backgroundColor: c.surfaceMuted }
                              : {
                                  backgroundColor: c.brandPrimary,
                                  shadowColor: c.brandPrimary,
                                  shadowOffset: { width: 0, height: 2 },
                                  shadowOpacity: 0.24,
                                  shadowRadius: 8,
                                  elevation: 3,
                                },
                            !isDisabled && pressed && {
                              backgroundColor: c.accentDim,
                              transform: [{ scale: 0.96 }],
                            },
                          ]}
                          onPress={() => {
                            taskHaptics.send();
                            if (isRecording) void finishVoiceAndSubmit("task");
                            else void handleCreateTask();
                          }}
                          disabled={isDisabled}
                        >
                          <Text
                            style={[
                              s.submitButtonText,
                              isDisabled && { color: c.textTertiary },
                            ]}
                            numberOfLines={1}
                          >
                            {isSubmitting ? "Sending…" : "Send"}
                          </Text>
                        </Pressable>
                      );
                    })()}
                  </View>
                </View>
              </View>
              </ScrollView>
            </View>
          </KeyboardAvoidingView>
          {/* Project/MCP picker as an in-composer OVERLAY — never a second
              native Modal. iOS cannot present a second native Modal while
              another is on screen; the newcomer mounts invisibly behind it,
              so the project chip tap "did nothing" (same class as the Logs
              sheet, 2026-08-08). Rendered inside THIS Modal the sheet
              actually appears above the composer. (2026-08-09) */}
          {showProjectPicker && showNewTask ? (
            <View style={[StyleSheet.absoluteFillObject, { zIndex: 60 }]} pointerEvents="box-none">
              {renderProjectPickerSheet()}
            </View>
          ) : null}
        </Modal>

        {Platform.OS === "ios" && runnerSelectionDeviceId ? (
          <SilentInputModal
            visible={showSilentInput}
            colors={c}
            targetDeviceId={runnerSelectionDeviceId}
            projectName={selectedComposerProject?.name || projectNameFromPath(projectDir) || undefined}
            onCancel={() => {
              setShowSilentInput(false);
              setShowNewTask(true);
            }}
            onTranscription={(transcription) => {
              newTaskTextRef.current = transcription;
              setNewTaskText(transcription);
              setInputFromSpeech(false);
              setTaskSubmitError(null);
            }}
          />
        ) : null}

        {/* Standalone picker Modal — ONLY when neither the New Task composer
            nor the task-detail Modal is up. Over any other Modal a second
            native Modal is invisible on iOS, so this path must never
            overlap them; the follow-up composer's chip uses the in-detail
            overlay instead (see the task-detail Modal below). */}
        <Modal visible={showProjectPicker && !showNewTask && !selectedTask} animationType="slide" transparent onRequestClose={closeProjectPicker}>
          {renderProjectPickerSheet()}
        </Modal>


        {/* ── Agent / Model Picker Modal ─────────────────────────────── */}
        <Modal visible={showAgentPicker} animationType="slide" transparent onRequestClose={() => closeAgentPicker(false)}>
          {/* Scrim, not a bare transparent Pressable: an invisible
              full-screen touch target is indistinguishable from a frozen
              screen if this sheet ever gets stuck open. Every other modal
              here dims the same way. */}
          <Pressable style={[s.modalOverlay, { justifyContent: "flex-start" }]} onPress={() => closeAgentPicker(false)} />
          <View style={[s.agentPickerSheet, { backgroundColor: c.bgCard }]}>
            <View style={[s.agentPickerHeader, { borderBottomColor: c.border }]}>
              <Text style={[s.agentPickerTitle, { color: c.textPrimary }]}>
                {retryAfterPickRef.current ? "Switch Model & Retry" : "Agent & Model"}
              </Text>
              <Pressable onPress={() => closeAgentPicker(!!retryAfterPickRef.current)}>
                <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>
                  {retryAfterPickRef.current ? "Retry" : "Done"}
                </Text>
              </Pressable>
            </View>
            {availableRunners.length === 0 && availableModels.length === 0 && (
              <View style={{ paddingHorizontal: 16, paddingVertical: 20, gap: 12 }}>
                <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center" }}>
                  {runnerPickerEmptyStateText(runnersFetchState)}
                </Text>
                {runnersFetchState !== "loading" && runnersFetchState !== "idle" ? (
                  <Pressable
                    onPress={() => { void refreshRunnerState(); }}
                    style={[
                      s.bannerInlineBtn,
                      {
                        backgroundColor: c.accentSoft,
                        alignSelf: "center",
                        minWidth: 92,
                        justifyContent: "center",
                      },
                    ]}
                  >
                    <Text style={[s.bannerInlineBtnText, { color: c.accent, textAlign: "center" }]}>Retry</Text>
                  </Pressable>
                ) : null}
              </View>
            )}
            {availableRunners.length > 0 && (() => {
              // Always surface the three first-class coding agents — the
              // user should be able to pick claude-code / opencode even
              // when only codex is installed today, and we'll prompt
              // sign-in / install as needed when they tap. Previously
              // this filtered by `r.installed`, which silently hid two
              // chips on a fresh box and made it look like Codex was the
              // only option.
              const RUNNER_WL = new Set(["claude", "claude-code", "codex", "opencode"]);
              const byId = new Map(availableRunners.map((r) => [r.id, r]));
              const installed = (["claude-code", "codex", "opencode"] as const).map((id) => {
                const existing = byId.get(id) ?? (id === "claude-code" ? byId.get("claude") : undefined);
                if (existing) return { ...existing, id };
                // Synthesize a stub row for runners the agent didn't
                // report — same chip UX, "needs install" affordance
                // surfaces via runnerAuthIssue / ready=false.
                return {
                  id,
                  name: id === "claude-code" ? "Claude Code" : id === "codex" ? "OpenAI Codex" : "OpenCode",
                  installed: false,
                  ready: false,
                  // opencode authenticates via provider config, not browser OAuth.
                  supportsBrowserAuth: id !== "opencode",
                } as typeof availableRunners[number];
              });
              const verificationPending = runnerVerificationPending(selectedRunnerRow);
              // Keep the currently-selected runner visible even if it's
              // outside the whitelist (e.g. a custom command from a long-
              // lived task) so opening the picker doesn't silently drop
              // its chip.
              if (selectedRunner && !RUNNER_WL.has(selectedRunner) && selectedRunner !== "custom") {
                const cur = byId.get(selectedRunner);
                if (cur) installed.push(cur);
              }
              return (
                <>
                  <Text style={[s.agentPickerSection, { color: c.textMuted }]}>AGENT</Text>
                  <View style={s.agentPickerChips}>
                    {installed.map((r) => (
                      <Pressable
                        key={r.id}
                        style={[
                          s.modelChip,
                          { borderColor: selectedRunner === r.id ? "#f59e0b" : c.border },
                          selectedRunner === r.id && { backgroundColor: "#f59e0b20" },
                        ]}
                        onPress={() => {
                          setSelectedRunner(r.id);
                          // Lock the seeding effect to the user's pick
                          // for the rest of this session — without this
                          // the next render of the seeding effect would
                          // overwrite r.id with explicitRunner from
                          // Convex (or a heuristic default).
                          userPickedRunnerRef.current = true;
                          userPickedModelRef.current = false;
                          // Persist per-device so the seeding effect on
                          // re-render reads the user's choice instead of
                          // reverting to the previously-pinned default.
                          // Pass model=null to clear any stale model pin
                          // from the previously-selected runner — the
                          // model-seeding effect will pick a sensible
                          // default for the new runner on the next render.
                          if (runnerSelectionDeviceId) {
                            void setPrimaryRunnerForDevice(runnerSelectionDeviceId, r.id, null).catch(() => {});
                          }
                          if (r.id === "opencode" && runnerAuthIssue(r)) {
                            setOpenCodeConfigTarget(runnerSelectionDeviceId || null);
                            setOpenCodeConfigStartInAdd(true);
                            setShowOpenCodeConfig(true);
                            return;
                          }
                          if (runnerAuthIssue(r) && r.supportsBrowserAuth) {
                            openRunnerAuthModal(r.id, runnerSelectionDeviceId || null);
                          }
                        }}
                      >
                        <Text style={[s.modelChipText, { color: selectedRunner === r.id ? "#f59e0b" : c.textMuted }]}>
                          {r.name}
                        </Text>
                      </Pressable>
                    ))}
                  </View>
                  {selectedRunnerRow?.ready === false && selectedRunner !== "custom" && (
                    <View
                      style={{
                        marginHorizontal: 16,
                        marginBottom: 12,
                        borderRadius: 12,
                        borderWidth: 1,
                        borderColor: selectedRunnerAuthIssue ? "rgba(56,189,248,0.28)" : "rgba(251,191,36,0.24)",
                        backgroundColor: selectedRunnerAuthIssue ? "rgba(14,165,233,0.10)" : "rgba(251,191,36,0.10)",
                        padding: 12,
                      }}
                    >
                      <Text
                        style={{
                          color: selectedRunnerAuthIssue ? "#dbeafe" : "#fde68a",
                          fontSize: 12,
                          lineHeight: 18,
                        }}
                      >
                        {selectedRunnerAuthIssue ||
                          selectedRunnerRow.error ||
                          selectedRunnerRow.warning ||
                          `${selectedRunnerRow.name} is installed but not ready on this machine.`}
                      </Text>
                      {verificationPending ? (
                        <Pressable
                          onPress={async () => {
                            try {
                              const client = runnerSelectionDeviceId
                                ? connectionManager.clientFor(runnerSelectionDeviceId)
                                : quicClient;
                              await client.testRunner(selectedRunnerRow.id, { prompt: "Reply with exactly: YAVER_PROVIDER_CHECK" });
                              await refreshRunnerState();
                            } catch (error) {
                              Alert.alert("Runner verification failed", error instanceof Error ? error.message : String(error));
                            }
                          }}
                          style={{
                            alignSelf: "flex-start", marginTop: 10, borderRadius: 999, borderWidth: 1,
                            borderColor: "rgba(125,211,252,0.35)", backgroundColor: "rgba(125,211,252,0.12)",
                            paddingHorizontal: 12, paddingVertical: 8,
                          }}
                        >
                          <Text style={{ color: "#e0f2fe", fontSize: 12, fontWeight: "700" }}>
                            Test {selectedRunnerRow.name}
                          </Text>
                        </Pressable>
                      ) : selectedRunnerAuthIssue && selectedRunnerRow.id === "opencode" ? (
                        <Pressable
                          onPress={() => {
                            setOpenCodeConfigTarget(runnerSelectionDeviceId || null);
                            setOpenCodeConfigStartInAdd(true);
                            setShowOpenCodeConfig(true);
                          }}
                          style={{
                            alignSelf: "flex-start",
                            marginTop: 10,
                            borderRadius: 999,
                            borderWidth: 1,
                            borderColor: "rgba(125,211,252,0.35)",
                            backgroundColor: "rgba(125,211,252,0.12)",
                            paddingHorizontal: 12,
                            paddingVertical: 8,
                          }}
                        >
                          <Text style={{ color: "#e0f2fe", fontSize: 12, fontWeight: "700" }}>
                            OpenCode settings
                          </Text>
                        </Pressable>
                      ) : selectedRunnerAuthIssue && selectedRunnerRow.supportsBrowserAuth ? (
                        <Pressable
                          onPress={() => openRunnerAuthModal(selectedRunnerRow.id, runnerSelectionDeviceId || null)}
                          style={{
                            alignSelf: "flex-start",
                            marginTop: 10,
                            borderRadius: 999,
                            borderWidth: 1,
                            borderColor: "rgba(125,211,252,0.35)",
                            backgroundColor: "rgba(125,211,252,0.12)",
                            paddingHorizontal: 12,
                            paddingVertical: 8,
                          }}
                        >
                          <Text style={{ color: "#e0f2fe", fontSize: 12, fontWeight: "700" }}>
                            Sign in to {selectedRunnerRow.name}
                          </Text>
                        </Pressable>
                      ) : null}
                    </View>
                  )}
                </>
              );
            })()}
            {availableModels.length > 0 && (
              <>
                <Text style={[s.agentPickerSection, { color: c.textMuted }]}>MODEL</Text>
                <View style={s.agentPickerChips}>
                  {availableModels.map((m) => (
                    <Pressable
                      key={m.id}
                      style={[
                        s.modelChip,
                        { borderColor: selectedModel === m.id ? c.accent : c.border },
                        selectedModel === m.id && { backgroundColor: c.accent + "20" },
                      ]}
                      onPress={() => {
                        setSelectedModel(m.id);
                        userPickedModelRef.current = true;
                        // Persist alongside the runner so the seeding effect
                        // on re-render reads the user's pick instead of
                        // overwriting it from primaryModelByDevice.
                        if (runnerSelectionDeviceId && selectedRunner) {
                          const supported = m.supportedReasoningEfforts?.map((item) => item.reasoningEffort) || [];
                          const nextEffort = supported.length === 0 || supported.includes(selectedReasoningEffort)
                            ? selectedReasoningEffort
                            : (m.defaultReasoningEffort || "medium");
                          setSelectedReasoningEffort(nextEffort);
                          void setPrimaryRunnerForDevice(runnerSelectionDeviceId, selectedRunner, m.id, undefined, undefined, nextEffort).catch(() => {});
                        }
                      }}
                    >
                      <Text style={[s.modelChipText, { color: selectedModel === m.id ? c.accent : c.textMuted }]}>
                        {m.name}
                      </Text>
                    </Pressable>
                  ))}
                </View>
              </>
            )}
            {selectedRunner === "codex" && selectedModelReasoningEfforts.length > 0 ? (
              <>
                <Text style={[s.agentPickerSection, { color: c.textMuted }]}>REASONING</Text>
                <View style={s.agentPickerChips}>
                  {selectedModelReasoningEfforts.map((effort) => (
                    <Pressable key={effort} style={[s.modelChip, { borderColor: selectedReasoningEffort === effort ? c.accent : c.border }, selectedReasoningEffort === effort && { backgroundColor: c.accent + "20" }]} onPress={() => {
                      setSelectedReasoningEffort(effort);
                      if (runnerSelectionDeviceId && selectedModel) {
                        void setPrimaryRunnerForDevice(runnerSelectionDeviceId, "codex", selectedModel, undefined, undefined, effort).catch(() => {});
                      }
                    }}>
                      <Text style={[s.modelChipText, { color: selectedReasoningEffort === effort ? c.accent : c.textMuted }]}>{effort === "xhigh" ? "Extra high" : effort === "max" ? "More reasoning" : effort[0].toUpperCase() + effort.slice(1)}</Text>
                    </Pressable>
                  ))}
                </View>
              </>
            ) : null}
            {/* OpenCode-only: pick the agent. Maps to `--agent <mode>`
                on `opencode run`. Empty = use the machine's
                defaultAgent from opencode.json. The chip rail merges
                the two stock agents (build / plan) with whatever the
                user has defined under `agent.<name>` in their config —
                review / chat / research / etc. — so a custom agent
                isn't a hidden CLI-only feature. Names are
                title-cased for display; the value sent to the runner
                stays lowercase so it matches the on-disk config. */}
            {selectedRunner === "opencode" && (() => {
              const titleCase = (n: string) => n.length === 0 ? "Default" : n.charAt(0).toUpperCase() + n.slice(1);
              const seen = new Set<string>();
              const chips: Array<{ id: string; name: string }> = [{ id: "", name: "Default" }];
              for (const stock of ["build", "plan"]) {
                if (!seen.has(stock)) { chips.push({ id: stock, name: titleCase(stock) }); seen.add(stock); }
              }
              for (const a of opencodeAgents) {
                const id = a.toLowerCase();
                if (seen.has(id)) continue;
                seen.add(id);
                chips.push({ id, name: titleCase(a) });
              }
              return (
                <>
                  <Text style={[s.agentPickerSection, { color: c.textMuted }]}>OPENCODE AGENT</Text>
                  <View style={s.agentPickerChips}>
                    {chips.map((m) => (
                      <Pressable
                        key={m.id || "default"}
                        style={[
                          s.modelChip,
                          { borderColor: selectedOpenCodeMode === m.id ? c.accent : c.border },
                          selectedOpenCodeMode === m.id && { backgroundColor: c.accent + "20" },
                        ]}
                        onPress={() => {
                          setSelectedOpenCodeMode(m.id);
                          if (runnerSelectionDeviceId && selectedRunner === "opencode") {
                            void setPrimaryRunnerForDevice(
                              runnerSelectionDeviceId,
                              "opencode",
                              selectedModel || null,
                              m.id || null,
                            ).catch(() => {});
                          }
                        }}
                      >
                        <Text style={[s.modelChipText, { color: selectedOpenCodeMode === m.id ? c.accent : c.textMuted }]}>
                          {m.name}
                        </Text>
                      </Pressable>
                    ))}
                  </View>
                </>
              );
            })()}
          </View>
        </Modal>
        <RunnerAuthModal
          visible={!!runnerAuthModalRunner}
          runner={runnerAuthModalRunner || "claude"}
          deviceName={devices.find((d) => d.id === (runnerAuthModalTarget || runnerSelectionDeviceId))?.name || runnerSelectionDevice?.name || "this machine"}
          // Routes /runner-auth/browser/* via /peer/<id> when set, so
          // OAuth runs against the remote box where the runner actually
          // lives — not the device the phone happens to be focused on.
          target={runnerAuthModalTarget || runnerSelectionDeviceId || undefined}
          onClose={() => {
            setRunnerAuthModalRunner(null);
            setRunnerAuthModalTarget(null);
          }}
          onCompleted={() => {
            setRunnerAuthModalRunner(null);
            setRunnerAuthModalTarget(null);
            void refreshRunnerState();
          }}
        />
        <OpenCodeConfigModal
          visible={showOpenCodeConfig}
          startInAddProvider={openCodeConfigStartInAdd}
          target={openCodeConfigTarget || activeDevice?.id}
          onClose={() => {
            setShowOpenCodeConfig(false);
            setOpenCodeConfigStartInAdd(false);
            setOpenCodeConfigTarget(null);
            // A saved provider/key changes OpenCode readiness — re-poll so the
            // banner flips from "needs setup" to "ready" without a manual nudge.
            void refreshRunnerState();
          }}
        />
        {/* ── Chat Detail Modal ───────────────────────────────────── */}
        <Modal
          visible={!!selectedTask}
          animationType={tabletDualPane ? "fade" : "slide"}
          transparent
          // NOTE: softwareKeyboardLayoutMode ("resize") is NOT in RN 0.81.5's
          // Modal types — it cannot compile against this React Native. The
          // Android keyboard-panning issue it was meant to fix (follow-up
          // Send button hidden) must be revisited with the KeyboardAvoidingView
          // below once the RN version supports the prop.
          onRequestClose={() => setSelectedTask(null)}
        >
          <KeyboardAvoidingView
            style={[
              s.chatModalOverlay,
              tabletDualPane ? { backgroundColor: c.bg, flexDirection: "row" } : null,
            ]}
            behavior={Platform.OS === "ios" ? "padding" : "height"}
            keyboardVerticalOffset={0}
          >
            {/* Phone: tap outside (top strip) to dismiss. Tablet
                landscape: dismiss area becomes the LEFT half of the
                screen so the task list behind it can be tapped to
                pick a different task. */}
            {tabletDualPane ? (
              // Tablet landscape: a LIVE task list fills the left pane.
              // Tapping a card swaps the chat on the right WITHOUT
              // closing it — a true two-pane cockpit, replacing the old
              // "tap the empty strip to dismiss" half-measure. The +
              // opens the composer; the ‹ chevron collapses back to the
              // full-width single-pane list.
              <View style={[
                s.cockpitListPane,
                {
                  backgroundColor: c.bg,
                  borderRightColor: c.border,
                  paddingTop: insets.top + 8,
                  width: Math.min(
                    layoutTokens.pane.maxListWidth,
                    Math.max(layoutTokens.pane.minListWidth, layout.width * 0.38),
                  ),
                  // RN-web maps `flex: 0` to a ZERO flex-basis, which wins
                  // over width and collapsed this pane to a 1px strip while
                  // the detail covered the whole tablet. Pin both width and
                  // basis explicitly so native and web keep the same cockpit.
                  flexBasis: Math.min(
                    layoutTokens.pane.maxListWidth,
                    Math.max(layoutTokens.pane.minListWidth, layout.width * 0.38),
                  ),
                  flexGrow: 0,
                  flexShrink: 0,
                },
              ]}>
                <View style={s.cockpitListHeader}>
                  <Text style={[s.cockpitListTitle, { color: c.textPrimary }]}>Tasks</Text>
                  <View style={{ flex: 1 }} />
                  <Pressable
                    hitSlop={10}
                    style={[s.cockpitListBtn, { backgroundColor: c.accentSoft }]}
                    accessibilityRole="button"
                    accessibilityLabel="New task"
                    testID="new-task-button"
                    onPress={() => {
                      setNewTaskText("");
                      setAttachedImages([]);
                      setInputFromSpeech(false);
                      pendingOpenTaskRef.current = null;
                      if (multiTargetMode && (!activeDevice || !isEffectivelyConnected)) {
                        setPendingTarget(null);
                        setShowTargetWizard(true);
                      } else {
                        setPendingTarget(null);
                        setShowNewTask(true);
                      }
                    }}
                  >
                    <Ionicons name="add" size={20} color={c.accent} />
                  </Pressable>
                  <Pressable
                    hitSlop={10}
                    style={[s.cockpitListBtn, { backgroundColor: c.surfaceMuted }]}
                    onPress={() => setSelectedTask(null)}
                  >
                    <Ionicons name="chevron-back" size={20} color={c.textSecondary} />
                  </Pressable>
                </View>
                <FlatList
                  data={displayTasks}
                  keyExtractor={(item) => scopedTaskIdentity(item.deviceId, item.id)}
                  contentContainerStyle={s.cockpitListContent}
                  showsVerticalScrollIndicator={false}
                  renderItem={({ item }) => {
                    const active = !!selectedTask && scopedTaskIdentity(item.deviceId, item.id) === scopedTaskIdentity(selectedTask.deviceId, selectedTask.id);
                    return (
                      <View style={[s.cockpitSelWrap, active && { backgroundColor: c.accentSoft }]}>
                        <TaskCard
                          item={item}
                          onPress={() => selectingTasks ? toggleTaskSelection(item) : setSelectedTask(item)}
                          onDelete={() => handleDeleteTask(item)}
                          onComplete={() => handleCompleteTask(item.id)}
                          onBlockedAction={handlePendingCloudBlockedAction}
                          selectionMode={selectingTasks}
                          selected={selectedBulkTaskKeys.has(taskBulkKey(item))}
                          onToggleSelection={() => toggleTaskSelection(item)}
                          onEnterSelection={() => beginTaskSelection(item)}
                        />
                      </View>
                    );
                  }}
                  ListEmptyComponent={
                    <EmptyState
                      icon="file-tray-outline"
                      title="No tasks yet"
                      // The FAB sits under the chat pane in this layout, so the
                      // action is the only way to compose from here — and it's
                      // only offered when there's a box that can run the task.
                      action={canComposeTask ? { label: "New task", onPress: openCreateTask } : undefined}
                    />
                  }
                />
              </View>
            ) : (
              <Pressable style={s.chatModalDismissArea} onPress={() => setSelectedTask(null)} />
            )}
            {selectedTask && (
              <View
                style={[
                  s.chatModal,
                  { backgroundColor: c.bg },
                  tabletDualPane ? {
                    flex: 1,
                    minWidth: layoutTokens.pane.detailMinWidth,
                    borderTopLeftRadius: 24,
                    borderBottomLeftRadius: 24,
                    borderTopRightRadius: 0,
                    borderLeftWidth: 1,
                    borderLeftColor: c.border,
                  } : null,
                ]}
              >
                {/* TaskHeader collapses the legacy 3-row stack
                    (Back/title/Stop, status/Logs, device) into a
                    2-row design. Title slot is intentionally empty:
                    the user's first command becomes the chat bubble
                    below, so duplicating it in the title was visual
                    noise. See spec section B1. */}
                <TaskHeader
                  status={selectedTask.status}
                  // Prefer the task's recorded deviceName (set by the
                  // agent at task creation, plumbed via Task.deviceName).
                  // activeDevice.name was lying when a task ran on a
                  // pool-secondary box and the user later focused
                  // somewhere else.
                  deviceName={selectedTask.deviceName || activeDevice?.name}
                  // In an active conversation the useful control is the
                  // model, not a repeated runner brand. The task already owns
                  // its runner identity; show the actual model + Codex effort
                  // and make the chip the same typed `/model` interaction.
                  runnerLabel={selectedTask.model
                    ? `${selectedTask.model}${selectedTask.reasoningEffort ? ` · ${selectedTask.reasoningEffort}` : ""}`
                    : selectedTask.runnerId ? displayRunnerLabel(selectedTask.runnerId) : undefined}
                  onRunnerPress={() => { void openRunnerControl("model"); }}
                  runnerActionLabel="Change model for the next turn"
                  modelLabel={undefined}
                  onBack={() => { setSelectedTask(null); setFollowUpText(""); }}
                  onOpenLogs={() => setShowLogs(true)}
                  primaryAction={
                    taskHasUnresolvedFailure(selectedTask) ? "retry"
                      : selectedTask.status === "review" ? "complete"
                      : isRunning ? "stop"
                      : "none"
                  }
                  onComplete={() => handleCompleteTask(selectedTask.id)}
                  onStop={() => {
                    taskHaptics.stop();
                    Alert.alert(
                      "Stop Task",
                      "The AI agent will be stopped and this session will be terminated. You can send a follow-up to resume later.",
                      [
                        { text: "Cancel", style: "cancel" },
                        { text: "Stop", style: "destructive", onPress: () => handleExitTask(selectedTask.id) },
                      ]
                    );
                  }}
                  onForceKill={() => {
                    Alert.alert(
                      "Force Kill",
                      "The process will be killed immediately. Any unsaved progress will be lost.",
                      [
                        { text: "Cancel", style: "cancel" },
                        { text: "Kill", style: "destructive", onPress: () => handleStopTask(selectedTask.id) },
                      ]
                    );
                  }}
                  onRetry={() => {
                    taskHaptics.retry();
                    // Re-send the original title with the same runner.
                    // Model and workDir come from per-device defaults —
                    // same path as the New Task modal. Smart-retry
                    // with an extra flag is offered separately in the
                    // ErrorMessage card below.
                    const retryRunner = normalizeTaskRunnerId(selectedTask.runnerId) || resolveRunnerForSend();
                    const retryModel = resolveModelForSend(retryRunner, selectedTask.model);
                    const taskDevice = deviceForTask(selectedTask);
                    const retryClient = taskDevice?.id && connectionManager.clientFor(taskDevice.id).isConnected
                      ? connectionManager.clientFor(taskDevice.id)
                      : quicClient;
                    void retryClient.sendTask(
                      selectedTask.title,
                      "",
                      retryModel,
                      retryRunner,
                      undefined,
                      undefined,
                      undefined,
                      projectDir || undefined,
                      undefined,
                      undefined,
                      undefined,
                      undefined,
                      selectedComposerProject?.name || projectNameFromPath(projectDir),
                      selectedMcpServers,
                    ).then((retried) => {
                      const next = {
                        ...retried,
                        deviceId: taskDevice?.id || selectedTask.deviceId,
                        deviceName: taskDevice?.name || selectedTask.deviceName || activeDevice?.name || retried.deviceName,
                        model: retried.model || retryModel,
                      };
                      setTasks((prev) => [next, ...prev]);
                      setSelectedTask(next);
                    }).catch((err) => {
                      const msg = err instanceof Error ? err.message : String(err);
                      Alert.alert("Retry failed", msg);
                    });
                  }}
                />

                {/* Live-output stream health. Before this, a stream cut
                    mid-render (relay bounce, box drop, tunnel break) ended in
                    an EMPTY error handler: the transcript simply stopped and
                    the user could not tell a finished task from a severed
                    connection. Now the drop is named, the reattach ladder is
                    narrated, and give-up carries a Reattach button. The
                    wording distinguishes transport evidence from task state:
                    only a fresh task probe may claim the runner is alive. */}
                {streamHealth && isRunning ? (
                  <View style={{ paddingHorizontal: 16, paddingTop: 8 }}>
                    <View
                      style={{
                        borderWidth: 1,
                        borderRadius: 12,
                        paddingHorizontal: 12,
                        paddingVertical: 10,
                        borderColor: streamHealth.kind === "lost" ? c.errorBorder : c.border,
                        backgroundColor: streamHealth.kind === "lost" ? c.errorBg : c.bgCard,
                      }}
                    >
                      <Text style={{ color: c.textPrimary, fontSize: 13, lineHeight: 18 }}>
                        {streamHealth.message}
                      </Text>
                      {streamHealth.kind === "lost" ? (
                        <Pressable
                          onPress={() => {
                            setStreamHealth(null);
                            setStreamReattachNonce((n) => n + 1);
                          }}
                          style={{
                            marginTop: 8,
                            alignSelf: "flex-start",
                            borderWidth: 1,
                            borderColor: c.errorBorder,
                            borderRadius: 8,
                            paddingHorizontal: 12,
                            paddingVertical: 6,
                          }}
                        >
                          <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "600" }}>
                            Reattach
                          </Text>
                        </Pressable>
                      ) : null}
                    </View>
                  </View>
                ) : null}

                {/* Preview state is deliberately a quiet inline status, not a
                    warning card. A queued refresh is ordinary while a runner
                    is coding; the result transcript must remain the visual
                    focus. `renderReady` is the one case with a useful action. */}
                {renderSkipNotice ? (
                  <View style={{ paddingHorizontal: 16, paddingTop: 8 }} accessibilityLiveRegion="polite">
                    <View
                      style={{
                        alignItems: "center",
                        borderWidth: 1,
                        borderRadius: 10,
                        borderColor: c.border,
                        backgroundColor: c.bgCardElevated,
                        flexDirection: "row",
                        gap: 8,
                        minHeight: 38,
                        paddingHorizontal: 10,
                        paddingVertical: 7,
                      }}
                    >
                      <Ionicons name="refresh-outline" size={15} color={c.textMuted} />
                      <Text style={{ color: c.textMuted, flex: 1, fontSize: 12, lineHeight: 16 }}>
                        {renderSkipNotice}
                      </Text>
                      {renderReady ? (
                        <Pressable
                          onPress={() => {
                            setRenderReady(false);
                            setRenderSkipNotice(null);
                            void rerenderActivePreviewSurface({
                              source: "mobile-explicit-render",
                              workDir: selectedTask?.workDir || projectDir || undefined,
                              taskStatus: selectedTask?.status,
                              autoRenderEnabled: true,
                            });
                          }}
                          style={({ pressed }) => ({
                            backgroundColor: c.accent,
                            borderRadius: 7,
                            opacity: pressed ? 0.78 : 1,
                            paddingHorizontal: 9,
                            paddingVertical: 5,
                          })}
                          accessibilityRole="button"
                          accessibilityLabel="Render preview updates"
                        >
                          <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>
                            Render updates
                          </Text>
                        </Pressable>
                      ) : null}
                    </View>
                  </View>
                ) : null}

                {/* Failed-task recovery: a one-tap path to switch the
                    runner/model and re-run. The header's plain "retry"
                    re-sends with the SAME runner + default model, so a
                    model error (e.g. "gpt-5.4 not supported with a ChatGPT
                    account") just reproduces — this opens the Agent & Model
                    picker seeded to the task's runner and re-runs on close. */}
                {taskHasUnresolvedFailure(selectedTask) ? (
                  <View style={{ paddingHorizontal: 16, paddingTop: 8 }}>
                    <View style={{ flexDirection: "row", gap: 8 }}>
                      <Pressable
                        onPress={() => {
                          setSelectedRunner(selectedTask.runnerId || "");
                          userPickedModelRef.current = false;
                          retryAfterPickRef.current = selectedTask;
                          setShowAgentPicker(true);
                        }}
                        style={({ pressed }) => [
                          { flex: 1, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 8, paddingVertical: 11, borderRadius: 10, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCardElevated },
                          pressed && { opacity: 0.6 },
                        ]}
                        accessibilityRole="button"
                        accessibilityLabel="Switch model or agent and retry this task"
                      >
                        <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>⚙  Switch model &amp; retry</Text>
                      </Pressable>
                      {normalizeTaskRunnerId(selectedTask.runnerId) === "opencode" ? (
                        <Pressable
                          onPress={() => {
                            const target = deviceForTask(selectedTask)?.id || selectedTask.deviceId || activeDevice?.id || null;
                            setOpenCodeConfigTarget(target);
                            setOpenCodeConfigStartInAdd(true);
                            setSelectedTask(null);
                            setTimeout(() => setShowOpenCodeConfig(true), 280);
                          }}
                          style={({ pressed }) => [
                            { flex: 1, alignItems: "center", justifyContent: "center", paddingVertical: 11, borderRadius: 10, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCardElevated },
                            pressed && { opacity: 0.6 },
                          ]}
                          accessibilityRole="button"
                          accessibilityLabel="Open OpenCode settings for this task's machine"
                        >
                          <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600" }}>OpenCode settings</Text>
                        </Pressable>
                      ) : null}
                    </View>
                  </View>
                ) : null}

                {/* Video summary chip — kept out of the header so Row 1
                    stays clean (B1). Inline strip below the header. */}
                {selectedTask.videoStatus === "ready" && selectedTask.videoClipId ? (
                  <View style={{ paddingHorizontal: 16, paddingTop: 6 }}>
                    <Pressable
                      onPress={() => setVideoSummaryClipId(selectedTask.videoClipId!)}
                      style={{ alignSelf: "flex-start", paddingHorizontal: 8, paddingVertical: 3, borderRadius: 4, backgroundColor: "#22c55e22" }}
                    >
                      <Text style={{ color: "#22c55e", fontSize: 11, fontWeight: "600" }}>▶ Watch demo</Text>
                    </Pressable>
                  </View>
                ) : selectedTask.videoStatus === "recording" || selectedTask.videoStatus === "queued" ? (
                  <View style={{ paddingHorizontal: 16, paddingTop: 6 }}>
                    <View style={{ alignSelf: "flex-start", paddingHorizontal: 8, paddingVertical: 3, borderRadius: 4, backgroundColor: "#eab30822" }}>
                      <Text style={{ color: "#eab308", fontSize: 11, fontWeight: "600" }}>🎬 {selectedTask.videoStatus}…</Text>
                    </View>
                  </View>
                ) : null}

                {/* Dev server banner — shown inside task detail so user doesn't have to go back.
                    hostedInModal: this sits INSIDE the task-detail <Modal>, and iOS won't
                    reliably present a second native Modal on top (it mounts invisibly —
                    "Open in Yaver" looked like it did nothing). The prop makes DevPreview
                    render its preview as an in-modal overlay instead. */}
                {isEffectivelyConnected && !attachedDogfoodRuntime ? <DevPreview hostedInModal /> : null}

                {/* opencode Chat|Terminal toggle — opencode tasks stream raw
                    runner stdout (ANSI + TUI) that the chat bubbles flatten,
                    so offer a real terminal view. Other runners keep the chat
                    only (no toggle, zero surface change). */}
                {/* Chat messages */}
                {/* FlatList (not ScrollView+.map) so streaming a 60-message
                    chat doesn't re-render every prior bubble each token —
                    that O(n) work per token was what saturated the JS
                    thread and made the keyboard feel dead while the agent
                    was running. ChatBubble is React.memo'd with content
                    equality, so windowed rows skip re-render entirely.
                    PhaseStatusLine + DebugSection ride along as
                    ListFooterComponent.

                    NO Chat|Terminal toggle here (2026-08-09, user call):
                    the mobile app is chat-only. opencode's raw console
                    look renders inside the bubbles via AnsiConsoleText;
                    there is no terminal view to switch to. */}
                {/* Keep the current human update outside the transcript's
                    scroll container. Follow-ups may queue below the active
                    assistant turn, so an in-list summary can be pushed off
                    screen precisely when the user asks "what's the status?". */}
                <View style={{ paddingHorizontal: 14 }}>
                  <TaskSessionSummary
                    task={selectedTask}
                    commands={cmdCardsByTask[selectedTask.id]}
                    pendingQuestion={agentQuestion?.taskId === selectedTask.id ? agentQuestion.prompt : undefined}
                    rawBytes={rawSnapshot.length}
                    lastOutputAt={rawLastAt}
                    compact
                  />
                </View>
                <FlatList
                    ref={chatScrollRef as any}
                    data={chatMessages}
                    keyExtractor={(item, idx) => `${idx}-${item.role}`}
                    onScroll={({ nativeEvent }) => {
                      const distanceFromBottom = nativeEvent.contentSize.height -
                        nativeEvent.layoutMeasurement.height - nativeEvent.contentOffset.y;
                      chatShouldFollowRef.current = distanceFromBottom < 56;
                    }}
                    scrollEventThrottle={100}
                    renderItem={({ item, index }) => (
                      <ChatBubble
                        turn={item}
                        c={c}
                        tokens={chatTokenInfo.showTokens && index === chatTokenInfo.lastAssistantIdx
                          ? { input: chatTokenInfo.input, output: chatTokenInfo.output }
                          : null}
                      />
                    )}
                    style={s.chatScroll}
                    contentContainerStyle={s.chatScrollContent}
                    keyboardShouldPersistTaps="handled"
                    initialNumToRender={20}
                    maxToRenderPerBatch={10}
                    windowSize={10}
                    removeClippedSubviews
                    ListFooterComponent={
                      <>
                        {taskHasUnresolvedFailure(selectedTask) && (() => {
                          const errMsg = extractTaskErrorMessage(selectedTask);
                          const fix = selectedTask.failure?.fix;
                          const fixRoute = taskFailureFixRoute(fix, selectedTask.runnerId);
                          const structuredSuggestion = fixRoute?.kind === "runner-provider-config"
                            ? {
                                label: "Open OpenCode settings",
                                kind: "runner-provider-config" as const,
                                payload: "opencode",
                              }
                            : fixRoute?.kind === "runner-auth-needed"
                              ? {
                                  label: `Sign in to ${displayRunnerLabel(fixRoute.runnerId)}`,
                                  kind: "runner-auth-needed" as const,
                                  payload: fixRoute.runnerId,
                                }
                              : fixRoute?.kind === "runner-test"
                                ? {
                                    label: `Test ${displayRunnerLabel(fixRoute.runnerId)}`,
                                    kind: "runner-test" as const,
                                    payload: fixRoute.runnerId,
                                  }
                                : selectedTask.failure ? null : undefined;
                          return (
                            <ErrorMessage
                              message={errMsg}
                              title={selectedTask.failure?.title || "Task failed"}
                              suggestion={structuredSuggestion}
                              onSmartRetry={(suggestion) => {
                                taskHaptics.retry();
                                try {
                                  console.log("[yaver-analytics]", JSON.stringify({
                                    event: "task_smart_retry",
                                    suggestion: suggestion.kind,
                                    runner: selectedTask.runnerId || null,
                                    ts: Date.now(),
                                  }));
                                } catch { /* analytics is best-effort */ }
                                // chown-fix is a one-tap "copy the command"
                                // affordance, not a retry — the user has to
                                // run chown in their own shell on the host
                                // box before vibing again. We also surface
                                // a nudge so they know to retry once they're
                                // done. The agent's preflight error embedded
                                // the exact command in suggestion.payload.
                                if (suggestion.kind === "runner-provider-config") {
                                  const taskDevice = deviceForTask(selectedTask);
                                  const targetId = taskDevice?.id || selectedTask.deviceId || activeDevice?.id || null;
                                  setSelectedTask(null);
                                  setTimeout(() => {
                                    setOpenCodeConfigTarget(targetId);
                                    setOpenCodeConfigStartInAdd(true);
                                    setShowOpenCodeConfig(true);
                                  }, 280);
                                  return;
                                }
                                if (suggestion.kind === "runner-auth-needed") {
                                  // The runner on the failing task's
                                  // device hit a "Not logged in" /
                                  // expired-token state. Open the
                                  // browser-auth modal pre-filled with
                                  // that runner; the modal already
                                  // routes through /peer/<deviceId>/
                                  // when target is set.
                                  const runnerId = (suggestion.payload || selectedTask.runnerId || "claude").toLowerCase();
                                  const taskDevice = deviceForTask(selectedTask);
                                  const targetId = taskDevice?.id || selectedTask.deviceId || activeDevice?.id || null;
                                  // CRITICAL: dismiss the chat-detail Modal
                                  // before opening RunnerAuthModal. React
                                  // Native cannot stack two sibling Modals
                                  // reliably on iOS — the previous
                                  // implementation called setRunnerAuthModalRunner
                                  // while the chat detail was still on screen,
                                  // and the new modal silently rendered behind
                                  // it (button "did nothing"). Close first,
                                  // then open the auth modal on the next tick
                                  // so the dismiss animation has a frame to
                                  // play. The failed task is recoverable from
                                  // the task list after sign-in completes.
                                  setSelectedTask(null);
                                  setTimeout(() => {
                                    setRunnerAuthModalRunner(runnerId);
                                    setRunnerAuthModalTarget(targetId);
                                  }, 280);
                                  return;
                                }
                                if (suggestion.kind === "runner-test") {
                                  const runnerId = String(suggestion.payload || selectedTask.runnerId || "").trim();
                                  const taskDevice = deviceForTask(selectedTask);
                                  const client = taskDevice?.id
                                    ? connectionManager.clientFor(taskDevice.id)
                                    : quicClient;
                                  void client.testRunner(runnerId, { model: selectedTask.model }).then((result) => {
                                    Alert.alert(
                                      result.ok ? "Runner is ready" : "Runner still needs attention",
                                      result.ok
                                        ? `${displayRunnerLabel(runnerId)} completed a real generation test. You can retry this task.`
                                        : result.failure?.reason || result.error || result.output || "The runner test did not succeed.",
                                    );
                                  }).catch((error) => {
                                    Alert.alert("Runner test failed", error instanceof Error ? error.message : String(error));
                                  });
                                  return;
                                }
                                if (suggestion.kind === "chown-fix") {
                                  const cmd = suggestion.payload || "";
                                  if (cmd) {
                                    void ExpoClipboard.setStringAsync(cmd);
                                    Alert.alert(
                                      "Copied",
                                      `${cmd}\n\nRun this on the agent box, then retry the task.`,
                                    );
                                  } else {
                                    Alert.alert(
                                      "Permissions issue",
                                      "Codex's sandbox can't write into the project directory. Chown the project to the user running yaver and retry.",
                                    );
                                  }
                                  return;
                                }
                                // Append the suggested fix as a hint to the
                                // task title — the agent reads the title and
                                // can pick up the flag verbatim. Other
                                // suggestion kinds (api-key-missing,
                                // node-modules, permission) re-send unchanged
                                // and rely on the user to act on the hint.
                                const titleHint =
                                  suggestion.kind === "skip-git-repo-check"
                                    ? `${selectedTask.title} --skip-git-repo-check`
                                    : selectedTask.title;
                                const retryRunner = normalizeTaskRunnerId(selectedTask.runnerId) || resolveRunnerForSend();
                                const retryModel = resolveModelForSend(retryRunner, selectedTask.model);
                                const taskDevice = deviceForTask(selectedTask);
                                const retryClient = taskDevice?.id && connectionManager.clientFor(taskDevice.id).isConnected
                                  ? connectionManager.clientFor(taskDevice.id)
                                  : quicClient;
                                void retryClient.sendTask(
                                  titleHint,
                                  "",
                                  retryModel,
                                  retryRunner,
                                  undefined,
                                  undefined,
                                  undefined,
                                  projectDir || undefined,
                                  undefined,
                                  undefined,
                                  undefined,
                                  undefined,
                                  selectedComposerProject?.name || projectNameFromPath(projectDir),
                                  selectedMcpServers,
                                ).then((retried) => {
                                  const next = {
                                    ...retried,
                                    deviceId: taskDevice?.id || selectedTask.deviceId,
                                    deviceName: taskDevice?.name || selectedTask.deviceName || activeDevice?.name || retried.deviceName,
                                    model: retried.model || retryModel,
                                  };
                                  setTasks((prev) => [next, ...prev]);
                                  setSelectedTask(next);
                                }).catch((err) => {
                                  const msg = err instanceof Error ? err.message : String(err);
                                  Alert.alert("Retry failed", msg);
                                });
                              }}
                              onOpenInAgent={() => setShowLogs(true)}
                              onCopyError={() => {
                                // Full paste-ready trace (2026-08-09) — same
                                // shape web copies; names task + error.
                                const trace = assembleTrace({
                                  surface: "mobile",
                                  task: {
                                    id: selectedTask.id,
                                    status: selectedTask.status,
                                    runner: selectedTask.runnerId,
                                    title: selectedTask.title,
                                  },
                                  error: errMsg,
                                });
                                ExpoClipboard.setStringAsync(trace);
                                Alert.alert("Copied", "Trace copied to clipboard.");
                              }}
                            />
                          );
                        })()}
                        {isPhoneLocalTask(selectedTask) && selectedTask.localCheckoutId && !isRunning ? (
                          <View style={{ marginTop: 12 }}>
                            <Pressable
                              onPress={() => setLocalGitExpandedTaskId((current) => current === selectedTask.id ? null : selectedTask.id)}
                              accessibilityRole="button"
                              accessibilityLabel="Review and deliver phone-local changes"
                              accessibilityState={{ expanded: localGitExpandedTaskId === selectedTask.id }}
                              style={{
                                minHeight: 48,
                                borderWidth: 1,
                                borderColor: c.border,
                                borderRadius: 12,
                                backgroundColor: c.bgCard,
                                paddingHorizontal: 14,
                                flexDirection: "row",
                                alignItems: "center",
                                justifyContent: "space-between",
                              }}
                            >
                              <View style={{ flex: 1 }}>
                                <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Review &amp; deliver</Text>
                                <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>Diff, commit, branch, and push from this task</Text>
                              </View>
                              <Ionicons
                                name={localGitExpandedTaskId === selectedTask.id ? "chevron-up" : "chevron-down"}
                                size={18}
                                color={c.textMuted}
                              />
                            </Pressable>
                            {localGitExpandedTaskId === selectedTask.id ? (
                              <View style={{ marginTop: 8 }}>
                                <SandboxGitPanel
                                  slug={selectedTask.localCheckoutId}
                                  embedded
                                  onChanged={() => {
                                    setSelectedTask((current) => current?.id === selectedTask.id
                                      ? { ...current, status: "ready" as TaskStatus, updatedAt: Date.now() }
                                      : current);
                                  }}
                                />
                              </View>
                            ) : null}
                          </View>
                        ) : null}
                        <TaskDetailsSection
                          projectSummary={taskProjectExecutionSummary({
                            projectName: selectedTask.projectName,
                            workDir: selectedTask.workDir,
                            deviceName: selectedTask.deviceName || activeDevice?.name,
                          })}
                        >
                          <AgentContextPanel
                            rows={buildAgentContextRows(selectedTask, selectedTask.deviceName || activeDevice?.name, connMode, availableModels, {
                              selectedModelId: selectedModel,
                              activeDevice: activeDevice ?? undefined,
                              userEmail: user?.email,
                              modeByDevice: primaryModeByDevice,
                              providerByDevice: primaryProviderByDevice,
                            })}
                          />
                          <LiveConsoleSection
                            key={selectedTask.id}
                            status={selectedTask.status}
                            rawText={rawSnapshot}
                            live={rawLive}
                          />
                          <CommandsPanel key={`commands-${selectedTask.id}`} models={cmdCardsByTask[selectedTask.id]} />
                          <DebugSection task={selectedTask} connMode={connMode} c={c} />
                        </TaskDetailsSection>
                      </>
                    }
                  />

                {/* Follow-up input: compact bar, expands to full card on tap */}
                {followUpExpanded ? (
                  <View style={[s.modalContent, { backgroundColor: c.bgCard, borderTopWidth: 1, borderTopColor: c.border, paddingBottom: Math.max(insets.bottom + 28, 72), maxHeight: followUpComposerMaxHeight }]}>
                    <ScrollView keyboardShouldPersistTaps="handled" showsVerticalScrollIndicator={false}>
                    <View style={s.modalHeader}>
                      <Text style={[s.modalTitle, { color: c.textPrimary }]}>Follow Up</Text>
                      <Pressable
                        hitSlop={{ top: 12, bottom: 12, left: 12, right: 12 }}
                        style={({ pressed }) => [s.modalCloseButton, { marginLeft: "auto" }, pressed && { opacity: 0.55 }]}
                        onPress={() => setShowFollowUpOptions((visible) => !visible)}
                        accessibilityRole="button"
                        accessibilityLabel={showFollowUpOptions ? "Hide follow-up options" : "More follow-up options"}
                        accessibilityState={{ expanded: showFollowUpOptions }}
                        testID="followup-options-more"
                      >
                        <Ionicons name="ellipsis-horizontal" size={23} color={c.textSecondary} />
                      </Pressable>
                      {/* Runtime agent switch. Use an action sheet here rather
                          than mounting the New Task native Modal on top of the
                          task-detail native Modal: iOS mounts the second modal
                          invisibly, making the control look dead. */}
                      {showFollowUpOptions ? <Pressable
                        hitSlop={{ top: 12, bottom: 12, left: 12, right: 12 }}
                        style={({ pressed }) => [
                          s.agentBadge,
                          { backgroundColor: c.bgCardElevated, borderColor: c.border, marginLeft: "auto", marginRight: 10 },
                          pressed && { opacity: 0.55 },
                        ]}
                        onPress={openFollowUpRunnerPicker}
                        accessibilityRole="button"
                        accessibilityLabel="Change coding agent for the next turn"
                      >
                        <Text style={[s.agentBadgeText, { color: c.textSecondary }]}>
                          {(() => {
                            // Show the parent task's runner by default, but
                            // reflect a pending picker change if the user
                            // already tapped a different chip — handleFollowUp
                            // forks when these differ from selectedTask.runnerId.
                            const parentRunner = selectedTask?.runnerId || "";
                            const desiredRunner = (followUpRunnerOverride || parentRunner).trim();
                            const runner = availableRunners.find(r => normalizeTaskRunnerId(r.id) === normalizeTaskRunnerId(desiredRunner));
                            const model = availableModels.find(m => m.id === selectedModel && isModelCompatibleWithRunnerId(m.id, desiredRunner));
                            const runnerLabel = runner?.name || displayRunnerLabel(desiredRunner);
                            const modelLabel = model?.name || "";
                            const labelText = modelLabel ? `${runnerLabel} · ${modelLabel}` : runnerLabel;
                            // Hint when the picker is set to a different runner
                            // than the parent task's — the next Send forks.
                            const isPendingFork = parentRunner && desiredRunner && desiredRunner !== parentRunner;
                            return isPendingFork ? `→ ${labelText}` : labelText;
                          })()}
                        </Text>
                        <Text style={{ color: c.textMuted, fontSize: 10, marginLeft: 4 }}>▾</Text>
                      </Pressable> : null}
                      {/* NO running spinner here (2026-08-09, user call): the
                          runner is already named by the chip + the status
                          pill + the header Stop action + PhaseStatusLine. A pulsing
                          circle beside a usable composer reads as blocked. */}
                    </View>
                    {showFollowUpOptions ? <>
                    {/* Project/MCP scope chip — SAME affordance as the New
                        Task composer. No chat/console discrimination
                        (2026-08-09): a follow-up is a task, and the user
                        must be able to re-target the project + MCPs for it
                        exactly like a fresh task. Opens the same picker
                        sheet; it renders as an in-detail overlay (never a
                        second native Modal — iOS would mount it invisibly). */}
                    <View style={s.composerScopeRow}>
                      <Pressable
                        style={({ pressed }) => [
                          s.scopeChip,
                          { backgroundColor: c.bgCardElevated, borderColor: selectedComposerProject ? c.accent : c.border },
                          pressed && { opacity: 0.65 },
                        ]}
                        onPress={openProjectPicker}
                        accessibilityRole="button"
                        accessibilityLabel="Configure project and MCPs for this follow-up"
                        testID="followup-project-chip"
                      >
                        <Ionicons name="options-outline" size={16} color={selectedComposerProject ? c.accent : c.textMuted} />
                        <Text style={[s.scopeChipText, { color: c.textSecondary }]} numberOfLines={1}>
                          {[
                            selectedComposerProject?.name || projectNameFromPath(projectDir) || "No project",
                            selectedMcpServers.length + (includeYaverMcp ? 1 : 0)
                              ? `${selectedMcpServers.length + (includeYaverMcp ? 1 : 0)} MCP`
                              : "No MCP",
                          ].join(" · ")}
                        </Text>
                        <Text style={{ color: c.textMuted, fontSize: 10 }}>▾</Text>
                      </Pressable>
                    </View>
                    {/* OpenCode Build|Plan — the in-chat mode switch. Mirrors
                        the New Task composer's segmented control, but scoped to
                        THIS conversation's next turn: an empty selection sends
                        no mode (agent default); tapping Plan then Send forks or
                        continues that turn in plan mode. Gated on the parent
                        task's runner — mode only means something for opencode. */}
                    {normalizeTaskRunnerId((selectedTask?.runnerId || "").trim()) === "opencode" && (
                      <View style={[s.composerModeRow, { borderColor: withAlpha(c.border, "cc") }]}>
                        <Text style={[s.composerModeLabel, { color: c.textMuted }]}>Mode</Text>
                        <View style={s.composerModeSegmented}>
                          {(["build", "plan"] as const).map((mode) => {
                            const active = followUpOpenCodeMode === mode;
                            return (
                              <Pressable
                                key={mode}
                                onPress={() => {
                                  taskHaptics.send();
                                  setFollowUpOpenCodeMode(mode);
                                }}
                                accessibilityRole="radio"
                                accessibilityState={{ selected: active }}
                                accessibilityLabel={`Run this follow-up in ${mode} mode`}
                                style={[
                                  s.composerModeButton,
                                  { borderColor: active ? c.accent : c.border },
                                  active && { backgroundColor: c.accent + "20" },
                                ]}
                              >
                                <Text style={[s.composerModeText, { color: active ? c.accent : c.textSecondary }]}>
                                  {mode === "build" ? "Build" : "Plan"}
                                </Text>
                              </Pressable>
                            );
                          })}
                        </View>
                      </View>
                    )}
                    </> : null}
                    <TextInput
                      // testIDs on the composer exist so the follow-up loop can
                      // be driven by maestro (mobile/maestro/followup-visible.yaml).
                      // Without them the flow has to guess at text/index
                      // selectors, which break on every copy change.
                      testID="followup-input"
                      style={[s.input, s.inputMultiline, { backgroundColor: c.bg, borderColor: c.border, color: c.textPrimary }]}
                      placeholder={isRunning ? "Send follow-up while it works" : "Follow up — or send another command"}
                      placeholderTextColor={c.textMuted}
                      value={followUpText}
                      onChangeText={(t) => { followUpTextRef.current = t; setFollowUpText(t); setInputFromSpeech(false); }}
                      multiline numberOfLines={4} textAlignVertical="top" autoFocus
                      autoCorrect={textCorrectionEnabled}
                      spellCheck={textCorrectionEnabled}
                      autoCapitalize={textCorrectionEnabled ? "sentences" : "none"}
                    />
                    {isTranscribing && (
                      <View style={{ flexDirection: "row", alignItems: "center", paddingVertical: 6 }}>
                        <ActivityIndicator size="small" color={c.accent} />
                        <Text style={{ color: c.textMuted, fontSize: 12, marginLeft: 8 }}>Transcribing...</Text>
                      </View>
                    )}
                    {followUpImages.length > 0 && (
                      <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ marginBottom: 8 }}>
                        {followUpImages.map((img, i) => (
                          <View key={i} style={{ marginRight: 8, position: "relative" }}>
                            <Image source={{ uri: `data:${img.mimeType};base64,${img.base64}` }} style={{ width: 60, height: 60, borderRadius: 8 }} />
                            <Pressable onPress={() => setFollowUpImages((prev) => prev.filter((_, idx) => idx !== i))} style={{ position: "absolute", top: -6, right: -6, width: 20, height: 20, borderRadius: 10, backgroundColor: "#ef4444", alignItems: "center", justifyContent: "center" }}>
                              <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>×</Text>
                            </Pressable>
                          </View>
                        ))}
                      </ScrollView>
                    )}
                    <View style={s.modalButtons}>
                      <Pressable style={[s.cancelButton, { backgroundColor: c.bgCardElevated }]} onPress={() => { Keyboard.dismiss(); setShowFollowUpOptions(false); setFollowUpExpanded(false); }}>
                        <Text style={[s.cancelButtonText, { color: c.textSecondary }]}>Cancel</Text>
                      </Pressable>
                      <View style={{ flex: 1, flexDirection: "row", alignItems: "center", gap: 8 }}>
                        <Pressable
                          style={({ pressed }) => [
                            { width: 44, height: 44, borderRadius: 22, backgroundColor: c.bgCardElevated, alignItems: "center", justifyContent: "center", borderWidth: 1, borderColor: c.border },
                            pressed && { opacity: 0.7 },
                          ]}
                          onPress={() => handlePickImage("followup")}
                          disabled={followUpImages.length >= 5}
                        >
                          <Ionicons name="add" size={24} color={c.textPrimary} />
                        </Pressable>
                        <Pressable
                          style={({ pressed }) => [
                            {
                              width: 44, height: 44, borderRadius: 22,
                              backgroundColor: isRecording ? "#ef4444" : c.bgCardElevated,
                              alignItems: "center", justifyContent: "center",
                              borderWidth: 1, borderColor: isRecording ? "#ef4444" : c.border,
                            },
                            pressed && { opacity: 0.7 },
                          ]}
                          onPress={() => {
                            if (!speechProvider) {
                              Alert.alert("Voice Not Configured", "Set up a speech-to-text provider in Settings → Voice to use voice input.");
                              return;
                            }
                            if (isRecording) {
                              stopRecordingAndTranscribe();
                            } else {
                              startRecording("followup");
                            }
                          }}
                          disabled={isTranscribing}
                        >
                          <Ionicons name={isRecording ? "stop" : "mic-outline"} size={20} color={isRecording ? "#fff" : c.textPrimary} />
                        </Pressable>
                        <Pressable
                          testID="followup-send"
                          style={[s.submitButton, { backgroundColor: c.accent }, ((!followUpText.trim() && followUpImages.length === 0) || isSendingFollowUp || isTranscribing) && s.submitButtonDisabled]}
                          onPress={() => {
                            const submit = isRecording
                              ? finishVoiceAndSubmit("followup")
                              : handleFollowUp();
                            void submit;
                            setShowFollowUpOptions(false);
                            setFollowUpExpanded(false);
                          }}
                          disabled={(!followUpText.trim() && followUpImages.length === 0) || isSendingFollowUp || isTranscribing}
                        >
                          <Text style={s.submitButtonText}>{isSendingFollowUp ? "Sending..." : "Send"}</Text>
                        </Pressable>
                      </View>
                    </View>
                    </ScrollView>
                  </View>
                ) : (
                  <View
                    style={[
                      s.chatInputBar,
                      {
                        borderTopColor: c.border,
                        backgroundColor: c.bgCard,
                        flexDirection: "row",
                        alignItems: "center",
                        gap: 8,
                        paddingTop: layout.isTablet ? 10 : 8,
                        paddingBottom: Math.max(
                          insets.bottom + (layout.isTablet ? 10 : 6),
                          Platform.OS === "ios" ? 24 : 12,
                        ),
                      },
                    ]}
                  >
                    <Pressable
                      style={{ flex: 1 }}
                      onPress={openFollowUpComposer}
                    >
                      <View
                        style={[
                          s.chatInput,
                          s.chatPromptShell,
                          {
                            backgroundColor: c.bg,
                            borderColor: c.border,
                            justifyContent: "center",
                          },
                        ]}
                      >
                        <Text style={{ color: c.textMuted, fontSize: 15 }}>
                          {isRunning ? "Send follow-up while it works" : "Follow up — or send another command"}
                        </Text>
                      </View>
                    </Pressable>
                    {!isRunning &&
                    selectedTask.runnerId === "yaver-phone" &&
                    localTurnUndoRef.current.has(selectedTask.id) ? (
                      <Pressable
                        key={`local-turn-undo-${localTurnUndoEpoch}`}
                        hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
                        style={({ pressed }) => [
                          {
                            width: 44, height: 44, borderRadius: 12,
                            backgroundColor: c.bg,
                            alignItems: "center", justifyContent: "center",
                            borderWidth: 1, borderColor: c.border,
                          },
                          pressed && { opacity: 0.7 },
                        ]}
                        onPress={() => void handleUndoPhoneTurn(selectedTask.id)}
                        accessibilityRole="button"
                        accessibilityLabel="Undo last vibe turn"
                      >
                        <Ionicons name="arrow-undo-outline" size={20} color={c.textPrimary} />
                      </Pressable>
                    ) : null}
                    {!isRunning ? (
                      <Pressable
                        hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
                        style={({ pressed }) => [
                          {
                            width: 44, height: 44, borderRadius: 12,
                            backgroundColor: c.brandPrimary,
                            alignItems: "center", justifyContent: "center",
                          },
                          pressed && { opacity: 0.7, transform: [{ scale: 0.96 }] },
                        ]}
                        onPress={openFollowUpComposer}
                        accessibilityRole="button"
                        accessibilityLabel="Send command"
                      >
                        <Text style={{ color: "#fff", fontSize: 20, fontWeight: "700", lineHeight: 22 }}>↑</Text>
                      </Pressable>
                    ) : null}
                  </View>
                )}
              </View>
            )}
          </KeyboardAvoidingView>
          {/* In-chat Logs overlay. iOS cannot present a second native Modal
              while the task-detail Modal is up — the newcomer mounts
              invisibly behind it, so the TaskHeader Logs button did nothing.
              Render the same panel as an absolute overlay INSIDE this Modal
              instead (2026-08-08). */}
          {showLogs ? (
            <View style={[StyleSheet.absoluteFillObject, { zIndex: 60 }]} pointerEvents="box-none">
              <LogsPanelContent
                c={c}
                selectedTask={selectedTask}
                taskLogLines={taskLogLines}
                combinedLogText={combinedLogText}
                logs={logs}
                onClose={() => setShowLogs(false)}
              />
            </View>
          ) : null}
          {runnerControlMode ? (
            <RunnerControlPanelContent
              c={c}
              mode={runnerControlMode}
              catalog={runnerControlCatalog}
              step={runnerControlStep}
              selectedModel={runnerControlModel}
              selectedEffort={runnerControlEffort}
              busy={runnerControlBusy}
              error={runnerControlError}
              onClose={() => { if (!runnerControlBusy) setRunnerControlMode(null); }}
              onSelectModel={(model) => {
                setRunnerControlModel(model.id);
                setRunnerControlEffort(model.defaultReasoningEffort || runnerControlCatalog?.reasoningEffort || "medium");
                if (runnerControlCatalog?.runnerId === "codex") setRunnerControlStep("effort");
              }}
              onSelectEffort={setRunnerControlEffort}
              onBackToModels={() => setRunnerControlStep("models")}
              onApplyModel={() => { void applyRunnerModelControl(); }}
              onConfirmExit={() => { void applyRunnerExitControl(); }}
            />
          ) : null}
          {/* Project/MCP picker as an in-detail OVERLAY — the follow-up
              composer's project chip opens it while the task-detail Modal is
              up. A second native Modal would mount invisibly (same iOS rule
              as the Logs sheet above), so it renders here, above the
              composer, same zIndex ladder. (2026-08-09) */}
          {showProjectPicker && !!selectedTask ? (
            <View style={[StyleSheet.absoluteFillObject, { zIndex: 60 }]} pointerEvents="box-none">
              {renderProjectPickerSheet()}
            </View>
          ) : null}
        </Modal>
        {/* ── Logs Modal ─────────────────────────────────────────── */}
        {/* Gated on !selectedTask: with the task-detail <Modal> up, iOS
            refuses a second native Modal (it mounts invisibly behind) — the
            chat path renders LogsPanelContent as an overlay INSIDE the chat
            modal instead (see the overlay in the chat Modal below). */}
        <Modal visible={showLogs && !selectedTask} animationType="slide" transparent onRequestClose={() => setShowLogs(false)}>
          <LogsPanelContent
            c={c}
            selectedTask={selectedTask}
            taskLogLines={taskLogLines}
            combinedLogText={combinedLogText}
            logs={logs}
            onClose={() => setShowLogs(false)}
          />
        </Modal>
      </View>
    </SafeAreaView>
  );
}

// ── Styles ───────────────────────────────────────────────────────────

const s = StyleSheet.create({
  safeArea: { flex: 1 },
  container: { flex: 1 },

  bannerMetaRow: {
    marginTop: 6,
    marginLeft: 18,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 10,
  },
  bannerTransportRow: { flexDirection: "row", alignItems: "center", gap: 6, flex: 1, minWidth: 0 },
  bannerActionRow: { marginTop: 6, marginLeft: 18, flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" },
  bannerStatusRow: { flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" },
  bannerStatusCopy: { ...typography.caption },
  bannerInlineBtn: {
    paddingHorizontal: 10,
    paddingVertical: 5,
    borderRadius: 999,
  },
  bannerInlineBtnText: {
    fontSize: 11,
    fontWeight: "700",
  },

  // Ping overlay
  pingOverlay: { marginHorizontal: 16, marginTop: 8, padding: 14, borderRadius: 12, borderWidth: 1 },
  pingTitle: { fontSize: 15, fontWeight: "700", marginBottom: 4 },
  pingDetail: { fontSize: 12, marginBottom: 2 },
  pingBar: { height: 4, borderRadius: 2, marginTop: 8, overflow: "hidden" as const },
  pingBarFill: { height: 4, borderRadius: 2 },
  pingDismiss: { fontSize: 10, marginTop: 6, textAlign: "center" as const },

  // Project bar + Todo stats
  projectBar: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 18,
    paddingVertical: 10,
    marginHorizontal: 12,
    marginTop: 10,
    borderWidth: 1,
    borderRadius: 18,
  },
  projectChipMobile: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
  },
  projectChipIcon: { fontSize: 8 },
  projectChipName: { fontSize: 13, fontWeight: "600" },
  projectChipBranch: { fontSize: 11, fontStyle: "italic" as const },
  todoBarStats: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  todoBarLabel: { fontSize: 12, fontWeight: "600" },
  todoBarPending: { fontSize: 11 },

  // List
  listContent: { paddingHorizontal: 14, paddingTop: 14, paddingBottom: 120 },
  listContentEmpty: { flexGrow: 1 },

  // Escape hatches under NoMachineEmpty (zero-device roster only). Quiet
  // links, deliberately not buttons — EmptyState's own action is the primary.
  emptyEscapeHatches: { alignItems: "center", paddingHorizontal: 32 },
  emptyEscapeLink: { paddingVertical: 6 },
  emptyEscapeText: { fontSize: 13, fontWeight: "600", textAlign: "center" },

  // Discover card (no devices)
  discoverSecondaryBtn: { width: "100%", marginTop: 20, paddingVertical: 12, borderRadius: 10, borderWidth: 1, alignItems: "center", justifyContent: "center", minHeight: 44 },
  discoverHelper: { fontSize: 12, lineHeight: 18, marginTop: 12, textAlign: "center", paddingHorizontal: 8 },
  discoverErrorCard: { width: "100%", marginBottom: 20, padding: 14, borderRadius: 12, borderWidth: 1 },
  discoverErrorText: { fontSize: 13, lineHeight: 19, fontWeight: "500", textAlign: "center" },
  discoverSteps: { width: "100%", marginTop: 12, gap: 14 },
  discoverStep: { flexDirection: "row", alignItems: "center", gap: 12 },
  discoverStepDot: { width: 28, height: 28, borderRadius: 14, alignItems: "center", justifyContent: "center" },
  discoverStepNum: { color: "#fff", fontSize: 13, fontWeight: "700" },
  discoverStepContent: { flex: 1 },
  discoverStepTitle: { fontSize: 14, fontWeight: "600" },
  discoverStepDesc: { fontSize: 12, fontFamily: "monospace", marginTop: 2 },
  discoverBtnText: { color: "#ffffff", fontWeight: "600", fontSize: 15 },

  // Reconnect card (disconnected with prior session)
  reconnectCard: { width: "100%", borderRadius: 16, borderWidth: 1, padding: 24, alignItems: "center" },
  reconnectIcon: { fontSize: 40, marginBottom: 12 },
  reconnectDeviceCard: { width: "100%", borderWidth: 1, borderRadius: 12, padding: 14, marginTop: 16 },
  reconnectDeviceRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  reconnectDeviceInfo: { flex: 1 },
  reconnectDeviceName: { fontSize: 16, fontWeight: "600" },
  reconnectDeviceMeta: { fontSize: 12, marginTop: 2, fontFamily: Platform.OS === "ios" ? "SF Mono" : "monospace" },
  reconnectBtn: { marginTop: 16, paddingHorizontal: 28, paddingVertical: 12, borderRadius: 10 },
  reconnectBtnRow: { flexDirection: "row", alignItems: "center", gap: 8 },
  reconnectBtnText: { color: "#ffffff", fontWeight: "600", fontSize: 15 },

  // Logs modal
  logsModalOverlay: { flex: 1, backgroundColor: "rgba(0,0,0,0.4)" },
  logsModal: { flex: 1, borderTopLeftRadius: 24, borderTopRightRadius: 24, overflow: "hidden" },
  logsHeader: { flexDirection: "row", justifyContent: "space-between", alignItems: "center", paddingHorizontal: 16, paddingVertical: 14, borderBottomWidth: 1 },
  logsTitle: { fontSize: 16, fontWeight: "700" },
  logsHeaderActions: { flexDirection: "row", alignItems: "center" },
  logsActionText: { fontSize: 15, fontWeight: "600" },
  logsScroll: { flex: 1 },
  logsScrollContent: { padding: 12 },
  logsSectionTitle: { fontSize: 13, fontWeight: "700", marginBottom: 10 },
  logsSectionDivider: { height: 1, marginVertical: 14 },
  logsEmpty: { fontSize: 14, textAlign: "center", marginTop: 40 },
  logLine: { fontSize: 11, fontFamily: "monospace", lineHeight: 16, marginBottom: 2 },

  // Task card
  cardContainer: { marginBottom: 10 },
  taskCard: {
    borderRadius: 14,
    paddingHorizontal: 16,
    paddingVertical: 15,
    borderWidth: 1,
    shadowColor: "#000",
    shadowOffset: { width: 0, height: 8 },
    shadowOpacity: 0.12,
    shadowRadius: 16,
    elevation: 2,
  },
  taskCardPressed: { opacity: 0.7 },
  taskHeader: { flexDirection: "row", alignItems: "flex-start", justifyContent: "space-between", marginBottom: 10, gap: 10 },
  taskHeaderMain: { flexDirection: "row", alignItems: "center", gap: 7, flexWrap: "wrap", flex: 1 },
  taskSelectionMark: { width: 22, height: 22, borderRadius: 6, borderWidth: 1.5, alignItems: "center", justifyContent: "center" },
  statusBadge: { flexDirection: "row", alignItems: "center", gap: 7, paddingHorizontal: 10, paddingVertical: 5, borderRadius: 999, borderWidth: 1 },
  statusPulseDot: { width: 7, height: 7, borderRadius: 4 },
  statusText: { fontSize: 11, fontWeight: "700" },
  metaPill: { paddingHorizontal: 8, paddingVertical: 4, borderRadius: 999, borderWidth: 1 },
  metaPillText: { fontSize: 10, fontWeight: "700", textTransform: "uppercase", letterSpacing: 0.3 },
  taskHeaderMeta: { alignItems: "flex-end", gap: 6, maxWidth: 132, marginLeft: 8 },
  ipPill: { paddingHorizontal: 8, paddingVertical: 4, borderRadius: 999, borderWidth: 1, maxWidth: 132 },
  ipPillText: { fontSize: 11, fontWeight: "500" },
  taskRunnerLabel: { fontSize: 11, maxWidth: 132, textAlign: "right" },
  taskActionButton: { width: 32, height: 28, borderRadius: 14, borderWidth: 1, alignItems: "center", justifyContent: "center" },
  taskTitle: { fontSize: 16, fontWeight: "600", lineHeight: 22, letterSpacing: -0.2 },
  pendingCloudBanner: { marginTop: 10, borderWidth: 1, borderRadius: 10, padding: 10, flexDirection: "row", alignItems: "center", gap: 10 },
  pendingCloudTitle: { fontSize: 12, fontWeight: "800", marginBottom: 2 },
  pendingCloudText: { fontSize: 12, lineHeight: 16 },
  pendingCloudButton: { minHeight: 30, maxWidth: 132, borderRadius: 999, borderWidth: 1, paddingHorizontal: 10, alignItems: "center", justifyContent: "center" },
  pendingCloudButtonText: { fontSize: 11, fontWeight: "800" },
  taskPhaseRow: { marginBottom: 8 },
  phaseChip: { alignSelf: "flex-start", paddingHorizontal: 10, paddingVertical: 5, borderRadius: 999, borderWidth: 1 },
  phaseChipText: { fontSize: 11, fontWeight: "700", textTransform: "lowercase", letterSpacing: 0.25 },
  taskOutputPreview: { fontSize: 14, marginTop: 4, lineHeight: 20 },
  taskFooter: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginTop: 10 },
  taskTimestamp: { fontSize: 12 },
  taskFooterMeta: { fontSize: 12, fontWeight: "600" },

  // FAB
  fab: {
    position: "absolute",
    bottom: 24,
    right: 24,
    width: 56,
    height: 56,
    borderRadius: 28,
    alignItems: "center",
    justifyContent: "center",
    elevation: 12,
    zIndex: 41,
    backgroundColor: "#7C66FF",
    shadowColor: "#000",
    shadowOffset: { width: 0, height: 8 },
    shadowOpacity: 0.28,
    shadowRadius: 16,
  },
  fabPressed: { opacity: 0.92, transform: [{ scale: 0.96 }] },
  fabText: { fontSize: 28, color: "#ffffff", fontWeight: "300" },
  actionDivider: { width: 1, alignSelf: "stretch", marginVertical: 5, marginHorizontal: 6 },
  utilityButton: {
    minHeight: 34,
    paddingHorizontal: 14,
    borderRadius: 999,
    borderWidth: 1,
    justifyContent: "center",
  },
  liveSessionsBanner: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    marginHorizontal: 16,
    marginBottom: 8,
    paddingHorizontal: 14,
    paddingVertical: 10,
    borderRadius: 12,
    borderWidth: 1,
  },

  // New task modal
  modalOverlay: { flex: 1, backgroundColor: "rgba(0,0,0,0.6)", justifyContent: "flex-end" },
  modalDismiss: { flex: 1 },
  modalContent: { borderTopLeftRadius: 30, borderTopRightRadius: 30, padding: 20, paddingTop: 22, paddingBottom: 32 },
  modalHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginBottom: 24 },
  modalHeaderStack: { marginBottom: 12 },
  modalHeaderRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  modalHeaderActions: { flexDirection: "row", alignItems: "center", gap: 4 },
  modalTargetRow: { flexDirection: "row", alignItems: "center", marginTop: 12 },
  modalCloseButton: { width: 36, height: 36, borderRadius: 18, alignItems: "center", justifyContent: "center" },
  modalTitle: { fontSize: 20, fontWeight: "700" },
  agentBadge: { flexDirection: "row", alignItems: "center", paddingHorizontal: 14, paddingVertical: 10, borderRadius: 999, borderWidth: 1 },
  agentBadgeText: { fontSize: 12, fontWeight: "500" },
  agentPickerSheet: { borderTopLeftRadius: 20, borderTopRightRadius: 20, paddingBottom: 40 },
  agentPickerHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 20, paddingVertical: 16, borderBottomWidth: 1 },
  agentPickerTitle: { fontSize: 17, fontWeight: "700" },
  agentPickerSection: { fontSize: 11, fontWeight: "600", letterSpacing: 0.5, marginTop: 16, marginBottom: 8, marginLeft: 20 },
  composerScopeRow: { flexDirection: "row", alignItems: "center", marginBottom: 8 },
  scopeChip: { minHeight: 34, maxWidth: "100%", borderWidth: 1, borderRadius: 17, paddingHorizontal: 12, flexDirection: "row", alignItems: "center", gap: 7 },
  scopeChipText: { fontSize: 12, fontWeight: "600", flexShrink: 1 },
  keepLastRow: { minHeight: 52, flexDirection: "row", alignItems: "center", gap: 12, marginBottom: 10 },
  projectSearchShell: {
    minHeight: 44,
    borderWidth: 1,
    borderRadius: 12,
    paddingHorizontal: 12,
    marginBottom: 10,
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  projectSearchInput: { flex: 1, minWidth: 0, fontSize: 14, paddingVertical: 10 },
  projectPickerRow: { borderWidth: 1, borderRadius: 12, paddingHorizontal: 12, paddingVertical: 10, marginBottom: 8, flexDirection: "row", alignItems: "center", gap: 10 },
  agentPickerChips: { flexDirection: "row", flexWrap: "wrap", gap: 8, paddingHorizontal: 16, marginBottom: 4 },
  input: { borderWidth: 1, borderRadius: 12, padding: 16, fontSize: 16, marginBottom: 12 },
  inputMultiline: { minHeight: 132 },
  composerShell: {
    paddingTop: 0,
    paddingHorizontal: 0,
    paddingBottom: 0,
    marginBottom: 14,
  },
  composerInput: {
    borderRadius: 12,
    marginBottom: 0,
    paddingHorizontal: 16,
    paddingTop: 16,
    paddingBottom: 10,
    fontSize: 18,
    lineHeight: 24,
  },
  transcribingRow: { flexDirection: "row", alignItems: "center", paddingHorizontal: 16, paddingBottom: 10 },
  attachmentStrip: { marginTop: 6, marginBottom: 10, paddingLeft: 16 },
  attachmentPreviewWrap: { marginRight: 10, position: "relative" },
  attachmentPreviewImage: { width: 64, height: 64, borderRadius: 14 },
  attachmentRemove: { position: "absolute", top: -6, right: -6, width: 22, height: 22, borderRadius: 11, alignItems: "center", justifyContent: "center" },
  // OpenCode quick Build|Plan segmented control (composer banner, 2026-08-09).
  composerModeRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    borderTopWidth: 1,
    paddingTop: 10,
    paddingBottom: 12,
    paddingHorizontal: 8,
    marginTop: -2,
  },
  composerModeLabel: { fontSize: 11, fontWeight: "600", letterSpacing: 0.5, textTransform: "uppercase" },
  composerModeSegmented: { flexDirection: "row", alignItems: "center", gap: 6 },
  composerModeButton: {
    minHeight: 28,
    borderRadius: 14,
    borderWidth: 1,
    paddingHorizontal: 14,
    alignItems: "center",
    justifyContent: "center",
  },
  composerModeText: { fontSize: 12, fontWeight: "600" },
  composerFooter: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    borderTopWidth: 1,
    paddingTop: 12,
    paddingHorizontal: 8,
  },
  // flex:1 + minWidth:0 lets the right group take the space left after the add
  // button and shrink instead of pushing the Send pill past the composer edge —
  // the overflow seen on narrow iPhones with the keyboard open. justifyContent
  // keeps everything right-aligned as it shrinks.
  composerFooterRight: { flexDirection: "row", alignItems: "center", justifyContent: "flex-end", gap: 8, flex: 1, minWidth: 0 },
  composerActionButton: {
    width: 44,
    height: 44,
    borderRadius: 22,
    alignItems: "center",
    justifyContent: "center",
    flexShrink: 0,
  },
  composerIconButton: {
    width: 48,
    height: 48,
    borderRadius: 24,
    alignItems: "center",
    justifyContent: "center",
    borderWidth: 1,
  },
  sendButtonLarge: {
    // Was minWidth:120/paddingH:24 — too wide once the mic, voice-switch and
    // reload icons share the row. It now shrinks (flexShrink) with a sane floor
    // so it stays tappable but never pushes past the composer edge.
    minWidth: 88,
    flexShrink: 1,
    minHeight: 52,
    paddingHorizontal: 18,
    paddingVertical: 14,
    borderRadius: 20,
    alignItems: "center",
    justifyContent: "center",
  },
  optionRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingVertical: 12,
    paddingHorizontal: 12,
    borderRadius: 16,
  },
  modalButtons: { flexDirection: "row", gap: 12, marginTop: 8 },
  cancelButton: { flex: 1, paddingVertical: 14, borderRadius: 10, alignItems: "center" },
  cancelButtonText: { fontWeight: "600", fontSize: 15 },
  submitButton: { flex: 1, paddingVertical: 14, borderRadius: 10, alignItems: "center" },
  submitButtonDisabled: { opacity: 0.4 },
  submitButtonText: { color: "#ffffff", fontWeight: "600", fontSize: 15, flexShrink: 0 },

  // Action bar
  actionBar: { flexDirection: "row", paddingHorizontal: 14, paddingVertical: 8, gap: 8, position: "relative" },
  actionBarFade: { position: "absolute", right: 0, top: 0, bottom: 0, width: 24, opacity: 0.9 },
  actionButton: { paddingHorizontal: 14, paddingVertical: 7, borderRadius: 999 },
  actionButtonText: { ...typography.bodyStrong, fontSize: 14, letterSpacing: 0.1 },

  // ── Chat modal ─────────────────────────────────────────────────────
  chatModalOverlay: { flex: 1, backgroundColor: "rgba(0,0,0,0.3)" },
  // Tablet-landscape cockpit: live task list occupying the left pane
  // beside the chat detail. See the tabletDualPane branch in the chat
  // modal.
  cockpitListPane: { flex: 1, borderRightWidth: 1 },
  cockpitListHeader: { flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 16, paddingBottom: 12 },
  cockpitListTitle: { fontSize: 22, fontWeight: "700" },
  cockpitListBtn: { width: 34, height: 34, borderRadius: 17, alignItems: "center", justifyContent: "center" },
  cockpitListContent: { paddingHorizontal: 10, paddingBottom: 48 },
  cockpitSelWrap: { borderRadius: 17, paddingHorizontal: 3, paddingTop: 3 },
  chatModalDismissArea: { height: 50 },
  chatModal: { flex: 1, borderTopLeftRadius: 24, borderTopRightRadius: 24, overflow: "hidden" },

  // Chat header
  chatHeader: { flexDirection: "row", alignItems: "center", paddingHorizontal: 14, paddingVertical: 15, borderBottomWidth: 1 },
  chatHeaderDevice: { flexDirection: "row", alignItems: "center", gap: 4 },
  chatHeaderDeviceText: { fontSize: 10, fontWeight: "500" },
  chatHeaderTitle: { fontSize: 16, fontWeight: "700", letterSpacing: -0.2 },
  chatHeaderMeta: { flexDirection: "row", alignItems: "center", gap: 4 },
  statusDotSmall: { width: 6, height: 6, borderRadius: 3 },
  chatHeaderStatus: { fontSize: 11, fontWeight: "500", textTransform: "uppercase" },
  chatHeaderCost: { fontSize: 11, marginLeft: 6 },
  // chatStopBtn removed — now using chatHeaderRight
  chatStopText: { color: "#ef4444", fontSize: 14, fontWeight: "600" },

  // Chat messages
  chatScroll: { flex: 1 },
  chatScrollContent: { paddingHorizontal: 14, paddingTop: 16, paddingBottom: 96 },

  userRow: { flexDirection: "row", justifyContent: "flex-end", marginBottom: 12 },
  userBubble: { maxWidth: "80%", borderRadius: 20, borderBottomRightRadius: 6, paddingHorizontal: 16, paddingVertical: 11 },
  // User-bubble content is the user's command — terminal-shaped
  // text. Spec X2 typography: mono for "what a developer would see
  // in a terminal", sans for UI chrome.
  userBubbleText: { color: "#fff", fontSize: 14, lineHeight: 20, fontFamily: monoFamily },
  userCopiedText: { color: "rgba(255,255,255,0.78)", fontSize: 10, fontWeight: "600", marginTop: 4, textAlign: "right" },

  assistantRow: { width: "100%", flexDirection: "row", justifyContent: "flex-start", marginBottom: 12 },
  // assistantFrame is the assistant's chat bubble — WhatsApp/Claude-mobile
  // shaped: a subtle fill, rounded with a bottom-LEFT tail (mirror of the
  // user bubble's bottom-right tail). Give it an explicit readable width:
  // maxWidth alone lets React Native shrink the Pressable to the intrinsic
  // width of a markdown child, which produced the ~100pt column seen on a
  // real iPhone. Agent replies carry code/markdown, so they get 90% of the
  // row while the shorter user bubble remains content-sized. backgroundColor is
  // applied inline from the theme. Fenced code blocks keep their own inner
  // border so they still stand out against the bubble fill.
  assistantFrame: { width: "90%", maxWidth: 760, borderRadius: 20, borderBottomLeftRadius: 6, paddingHorizontal: 14, paddingVertical: 10 },
  assistantTokens: { fontSize: 12, marginBottom: 6, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" },
  assistantToggle: { fontSize: 12, fontWeight: "600" },
  assistantMetaRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginTop: 4 },
  assistantCopiedText: { fontSize: 10, fontWeight: "600" },

  // Typing indicator
  typingRow: { flexDirection: "row", justifyContent: "flex-start", marginBottom: 12 },
  typingBubble: { flexDirection: "row", gap: 5, backgroundColor: "#171b22", borderRadius: 20, borderBottomLeftRadius: 8, paddingHorizontal: 16, paddingVertical: 14 },
  typingDot: { width: 8, height: 8, borderRadius: 4 },

  // Streaming indicator
  streamingIndicator: { flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 8, paddingHorizontal: 4 },
  startingHint: { fontSize: 12, marginTop: 8, marginLeft: 4, marginBottom: 12 },
  modelChips: { flexDirection: "row", gap: 8, marginTop: 12, marginBottom: 4 },
  modelChip: { paddingHorizontal: 14, paddingVertical: 6, borderRadius: 16, borderWidth: 1 },
  modelChipText: { fontSize: 13, fontWeight: "500" },
  streamingText: { fontSize: 12, fontStyle: "italic" },

  // Chat input bar
  chatInputBar: { flexDirection: "row", alignItems: "flex-end", paddingHorizontal: 12, paddingVertical: 8, paddingBottom: Platform.OS === "ios" ? 24 : 8, borderTopWidth: 1, gap: 8 },
  chatInputBarRunning: { flex: 1, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 8, paddingVertical: 8 },
  chatRunningText: { fontSize: 14 },
  chatInput: { flex: 1, borderWidth: 1, borderRadius: 20, paddingHorizontal: 16, paddingVertical: 12, fontSize: 15, maxHeight: 200, minHeight: 190 },
  chatPromptShell: { minHeight: 48, maxHeight: 48, paddingVertical: 0, borderRadius: 18 },
  chatSendBtn: { width: 36, height: 36, borderRadius: 18, alignItems: "center", justifyContent: "center" },
  chatSendText: { color: "#fff", fontSize: 18, fontWeight: "700" },

  // Debug section
  debugContainer: { marginTop: 16, marginBottom: 8 },
  debugToggle: { paddingHorizontal: 12, paddingVertical: 8, borderRadius: 8, borderWidth: 1, alignSelf: "flex-start" },
  debugToggleText: { fontSize: 12, fontWeight: "600" },
  debugContent: { marginTop: 6, padding: 12, borderRadius: 8, borderWidth: 1 },
  debugLine: { fontSize: 11, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", lineHeight: 18 },

  // Live console (opencode raw lane)
  liveConsoleWrap: { marginHorizontal: spacing.lg, marginVertical: spacing.sm, borderWidth: 1, borderRadius: 10, overflow: "hidden" },
  liveConsoleToggle: { paddingHorizontal: 12, paddingVertical: 8, gap: 6 },
  liveConsoleHeaderRow: { flexDirection: "row", alignItems: "center", gap: 6 },
  liveConsoleCaret: { fontSize: 11 },
  liveConsoleTitle: { fontSize: 12, fontWeight: "600", letterSpacing: 0.2 },
  liveConsoleDot: { fontSize: 10, fontWeight: "600", textTransform: "uppercase" },
  liveConsoleCount: { fontSize: 10, marginLeft: "auto", fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" },
  liveConsolePreview: { fontSize: 12, lineHeight: 17, marginTop: 6 },
  liveConsoleBody: { borderTopWidth: 1, maxHeight: 320, padding: 12 },

});

// Markdown styles
function markdownStyles(c: ReturnType<typeof useColors>) {
  return {
    body: { color: c.textPrimary, fontSize: 13, lineHeight: 20 },
    heading1: { color: c.textPrimary, fontSize: 18, fontWeight: "700" as const, marginBottom: 6, marginTop: 10 },
    heading2: { color: c.textPrimary, fontSize: 16, fontWeight: "700" as const, marginBottom: 4, marginTop: 8 },
    heading3: { color: c.textPrimary, fontSize: 14, fontWeight: "600" as const, marginBottom: 4, marginTop: 6 },
    paragraph: { color: c.textPrimary, marginBottom: 6 },
    strong: { fontWeight: "700" as const, color: c.textPrimary },
    em: { fontStyle: "italic" as const },
    bullet_list: { marginBottom: 6 },
    ordered_list: { marginBottom: 6 },
    list_item: { flexDirection: "row" as const, marginBottom: 3 },
    // Code blocks always render terminal-style (dark slab, light text)
    // regardless of the active theme. In light mode the previous
    // `c.bg`-as-fence-background gave a near-white slab that, combined
    // with downstream text-color cascades from RN markdown, sometimes
    // surfaced white-on-near-white codex output. Hardcoding a dark
    // slab + explicit light text matches the conventional code-block
    // treatment (GitHub, VS Code) and removes the contrast-dependency
    // on theme tokens entirely.
    code_inline: { backgroundColor: "#1F1F26", color: "#E879F9", fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", fontSize: 13, paddingHorizontal: 5, paddingVertical: 1, borderRadius: 4 },
    fence: { backgroundColor: "#0F0F14", color: "#E6E6F0", borderColor: "#2A2A35", borderWidth: 1, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 10, marginVertical: 8, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", fontSize: 12, lineHeight: 18 },
    code_block: { backgroundColor: "#0F0F14", color: "#E6E6F0", fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", fontSize: 12, lineHeight: 18, padding: 10, borderRadius: 10, marginVertical: 8 },
    blockquote: { borderLeftWidth: 3, borderLeftColor: c.accent || "#6366f1", paddingLeft: 12, marginVertical: 6, opacity: 0.85 },
    link: { color: c.accent || "#6366f1" },
    hr: { backgroundColor: c.border || "#1e1e2e", height: 1, marginVertical: 10 },
    table: { borderColor: c.border || "#1e1e2e" },
    tr: { borderBottomColor: c.border || "#1e1e2e" },
    th: { color: c.textPrimary, fontWeight: "700" as const, padding: 6 },
    td: { color: c.textPrimary, padding: 6 },
  };
}
