import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Ionicons } from "@expo/vector-icons";
import { agentSignalFromTask, agentStateBg, agentStateColor } from "../../src/lib/agentStatus";
import { clipUrl } from "../../src/lib/vibePreview";
import { planFollowUp } from "../../src/lib/followUpPlan";
import { isBundleLoaderAvailable } from "../../src/lib/bundleLoader";
import { AuthenticatedVideoPlayer } from "../../src/components/AuthenticatedVideoPlayer";
import {
  ActivityIndicator,
  Alert,
  Animated,
  Dimensions,
  FlatList,
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
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from "react-native";
import * as ImagePicker from "expo-image-picker";
import * as FileSystem from "expo-file-system/legacy";
import { SafeAreaView, useSafeAreaInsets } from "react-native-safe-area-context";
import Markdown from "react-native-markdown-display";
import { useDevice } from "../../src/context/DeviceContext";
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
import EmptyState from "../../src/components/EmptyState";
import NoMachineEmpty from "../../src/components/NoMachineEmpty";
import TaskTargetWizard, { type TaskTarget } from "../../src/components/TaskTargetWizard";
import { useColors, useTheme } from "../../src/context/ThemeContext";
import { appTag } from "../../src/lib/appVersion";
import * as ExpoClipboard from "expo-clipboard";
import { getLogEntries, onLogsChanged, LogEntry } from "../../src/lib/logger";
import {
  AgentStatus,
  CloudWorkspaceRequiredError,
  ConnectionMode,
  ConnectionState,
  ImageAttachment,
  ModelInfo,
  quicClient,
  RunnerInfo,
  Task,
  TaskStatus,
  TmuxSession,
} from "../../src/lib/quic";
import { connectionManager } from "../../src/lib/connectionManager";
import { markTaskDeleted, getDeletedTaskIds, cacheTaskTurns, getCachedTaskTurns, cacheTaskList, getCachedTaskList } from "../../src/lib/storage";
import {
  getTaskPlacementStatus,
  listTaskDispatchIntents,
  rebindTaskPlacement,
  updateTaskDispatchIntent,
} from "../../src/lib/taskPlacement";
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
import { useLocalSearchParams, useRouter } from "expo-router";
import { DevPreview } from "../../src/components/DevPreview";
import { Badge } from "../../src/components/Badge";
import RunnerAuthModal from "../../src/components/RunnerAuthModal";
import { OpenCodeConfigModal } from "../../src/components/OpenCodeConfigModal";
import {
  runYaverAgent,
  loadYaverAgentLocalConfig,
  type YaverAgentHistoryTurn,
} from "../../src/lib/yaverAgentRunner";
import type { YaverAgentToolContext } from "../../src/lib/yaverAgentTools";
import { loadTaskVideoSummaryEnabled } from "../../src/lib/taskComposerPrefs";
import { withAlpha } from "../../src/lib/themeUtils";
import { lightCardShadow, monoFamily, spacing, typography } from "../../src/theme/tokens";
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
import { MessageBubble } from "../../src/components/MessageBubble";
import { openTaskBus } from "../../src/lib/runningTasksBus";
import { ErrorMessage, detectSmartRetry } from "../../src/components/ErrorMessage";
import { AgentContextPanel, type AgentContextRow } from "../../src/components/AgentContextPanel";
import { deriveRunnerBannerState, type RunnerFetchState } from "../../src/lib/runnerBannerState";
import { TaskHeader } from "../../src/components/TaskHeader";
import {
  displayRunnerLabel,
  isModelCompatibleWithRunnerId,
  isTransportDeviceLabel,
  normalizeProjectChipName,
  normalizeTaskRunnerId,
  preferredDefaultModelForRunner,
  preferredDefaultRunnerForDevice,
  resolveModelForRemoteSend,
  resolveRunnerForRemoteSend,
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
  runner: Pick<RunnerInfo, "id" | "installed" | "ready" | "warning" | "error"> | null | undefined,
): string | null {
  if (!runner || !runner.installed || runner.ready !== false) return null;
  const detail = String(runner.error || runner.warning || "").trim();
  const lower = detail.toLowerCase();
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
const SYSTEM_CONTEXT_END_MARKERS = [
  "Kill any stale expo/metro processes before retrying.",
  "or related Yaver preview tools instead of asking them to guess.",
  "pick up where you left off.",
];

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
    summary: summary || "Working...",
    cleaned,
    activity,
    shouldCollapse: hasMore,
    hasHiddenNoise,
  };
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
    return "_Working… implementation details hidden while the task runs._";
  }
  if (!hidden && !preview.activity.length) return body;
  const activity = preview.activity.length > 0
    ? `\n\n${preview.activity.map((item) => `- ${item}`).join("\n")}`
    : "";
  return `${body}${activity}\n\n_Working through implementation details…_`.trim();
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
const RELOAD_INTENT =
  /^\s*(hot\s*reload|reload|hermes(\s+reload)?|rebuild(\s+bundle)?|push\s+bundle)(\s+[a-z0-9._-]{1,40})?\s*$/i;
function isReloadIntent(text: string): boolean {
  return RELOAD_INTENT.test(text.trim());
}

type TaskPhaseTone = "neutral" | "active" | "warm" | "success";

function deriveTaskPhases(task: Task): Array<{ label: string; tone: TaskPhaseTone }> {
  const tail = task.output.length > 120 ? task.output.slice(-120) : task.output;
  const signalLines = tail
    .map((line) => stripAnsi(line).trim())
    .filter(Boolean)
    // OpenCode's banner (`> build · glm-4.7`) is transport metadata,
    // not task activity. If we keep it, trivial commands like `ls`
    // get mislabeled as "compiling…" purely because the selected
    // OpenCode agent is named "build".
    .filter((line) => !/^>\s+[A-Za-z0-9._-]+\s+·\s+[A-Za-z0-9_./:-]+$/.test(line))
    // Shell markers tell us a command ran, but not which phase the
    // task is in. The command text itself is enough.
    .map((line) => line.replace(/^\*\*\$\s+/, "").replace(/\*\*$/, ""));
  const haystack = `${task.title}\n${signalLines.join("\n")}\n${task.resultText || ""}`.toLowerCase();
  const phases: Array<{ label: string; tone: TaskPhaseTone }> = [];
  const push = (label: string, tone: TaskPhaseTone) => {
    if (!phases.some((phase) => phase.label === label)) phases.push({ label, tone });
  };

  if (/(search|find|grep|rg |ripgrep|scan|inspect|trace|ls |cat )/.test(haystack)) push("searching", "neutral");
  if (/(plan|reason|thinking|analyz|investigat|review)/.test(haystack)) push("mapping", "neutral");
  if (/(edit|patch|write|refactor|implement|apply_patch|create file)/.test(haystack)) push("cooking", "warm");
  if (/(build|compile|tsc|xcodebuild|gradle|go build|cargo build|bundle|hermes)/.test(haystack)) push("compiling", "active");
  if (/(test|jest|vitest|pytest|go test|cargo test|unit test)/.test(haystack)) push("checking", "active");
  if (/(publish|deploy|upload|ship|release|testflight|play store|pypi|npm publish)/.test(haystack)) push("shipping", "success");
  if (phases.length === 0) push("working", "active");
  return phases.slice(0, 3);
}

function PhaseChip({ task }: { task: Task }) {
  const c = useColors();
  const phases = useMemo(() => deriveTaskPhases(task), [task.id, task.title, task.output, task.resultText, task.status]);
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
    [task.id, task.title, task.output, task.resultText, task.status]
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

// When a runner (claude-code / codex) surfaces a structured payload it
// prints the WHOLE response as JSON — most visibly API failures, e.g.
// `ERROR: {"type":"error","error":{"message":"…"}}`. Rendering that raw
// through Markdown looks broken. If the entire content parses as JSON
// (tolerating one leading `LABEL:` prefix like ERROR:), surface a clean
// view: the human-readable message when the shape is a known error, plus
// the pretty-printed JSON. Anything that isn't fully JSON returns null →
// the caller falls back to the normal markdown/raw render.
function detectJsonResponse(raw: string | undefined): { message: string; pretty: string } | null {
  if (!raw) return null;
  const text = raw.trim();
  if (!text) return null;
  const labelStripped = text.replace(/^[A-Za-z][\w-]*:\s*/, "");
  let parsed: unknown;
  for (const candidate of text === labelStripped ? [text] : [text, labelStripped]) {
    const head = candidate[0];
    if (head !== "{" && head !== "[") continue;
    try { parsed = JSON.parse(candidate); break; } catch { /* not pure JSON */ }
  }
  if (parsed === undefined || parsed === null || typeof parsed !== "object") return null;
  const p = parsed as Record<string, any>;
  const rawMsg = p?.error?.message ?? p?.message ?? p?.error ?? null;
  const message = typeof rawMsg === "string" && rawMsg.trim() ? rawMsg.trim() : "";
  return { message, pretty: JSON.stringify(parsed, null, 2) };
}

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

  // RULES OF HOOKS: every hook MUST run on every render, BEFORE any early
  // return. This block used to sit AFTER the `if (isUser) return` below, so a
  // user bubble ran 0 hooks while an assistant bubble ran 5 — and the moment a
  // list slot flipped role (or React re-rendered the same slot), it crashed the
  // whole app with "Rendered fewer hooks than expected. This may be caused by an
  // accidental early return statement." Hooks now run unconditionally; the user
  // branch simply ignores these assistant-only values (the extra work is a cheap
  // memoized string transform).
  //
  // preview: cleaned markdown so the bubble looks like real claude-code / codex
  // output. jsonResponse: whole-response-is-JSON detection (errors / structured
  // payloads) → clean message + pretty block instead of raw JSON through
  // Markdown. showRaw: long-press toggles the verbatim stream. collapsedMarkdown:
  // summary + activity bullets when the response is long.
  const preview = useMemo(() => buildAssistantPreview(turn.content), [turn.content]);
  const jsonResponse = useMemo(() => detectJsonResponse(turn.content), [turn.content]);
  const [showRaw, setShowRaw] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const collapsedMarkdown = useMemo(() => {
    if (preview.activity.length === 0) return preview.summary;
    return `${preview.summary}\n\n${preview.activity.map((item) => `- ${item}`).join("\n")}`;
  }, [preview]);

  if (isUser) {
    return (
      <View style={s.userRow}>
        <View style={[s.userBubble, userBubbleCap, { backgroundColor: c.accent || "#6366f1" }]}>
          <Text style={s.userBubbleText}>{turn.content}</Text>
        </View>
      </View>
    );
  }

  const totalTokens = tokens ? tokens.input + tokens.output : 0;
  const renderedMarkdown = showRaw
    ? turn.content
    : (expanded || !preview.shouldCollapse ? preview.cleaned : collapsedMarkdown);

  return (
    <View style={s.assistantRow}>
      <Pressable
        style={[s.assistantFrame, { backgroundColor: c.bgCard, borderColor: c.border }]}
        onLongPress={() => setShowRaw((v) => !v)}
        delayLongPress={500}
      >
        {totalTokens > 0 ? (
          <Text style={[s.assistantTokens, { color: c.textMuted }]}>
            tokens used {totalTokens.toLocaleString()}
          </Text>
        ) : null}
        {jsonResponse && !showRaw ? (
          <View>
            {jsonResponse.message ? (
              <Text selectable style={{ color: c.textPrimary, fontSize: 15, lineHeight: 21, marginBottom: jsonResponse.pretty ? 10 : 0 }}>
                {jsonResponse.message}
              </Text>
            ) : null}
            <View style={{ borderWidth: 1, borderColor: c.border, borderRadius: 8, padding: 10, backgroundColor: c.bg }}>
              <Text
                selectable
                style={{ color: c.textMuted, fontSize: 12, lineHeight: 17, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" }}
              >
                {jsonResponse.pretty}
              </Text>
            </View>
          </View>
        ) : (
          <Markdown style={markdownStyles(c)}>
            {renderedMarkdown || " "}
          </Markdown>
        )}
        {!showRaw && preview.shouldCollapse ? (
          <Pressable onPress={() => setExpanded((value) => !value)} style={{ marginTop: 6 }}>
            <Text style={[s.assistantToggle, { color: c.accent }]}>
              {expanded ? "Hide details" : "Show details"}
            </Text>
          </Pressable>
        ) : null}
        {showRaw ? (
          <Text style={[s.assistantToggle, { color: c.textMuted, marginTop: 4, fontSize: 10 }]}>
            (raw stream — long-press to hide)
          </Text>
        ) : null}
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
}: {
  item: Task;
  onPress: () => void;
  onDelete: () => void;
  onComplete: () => void;
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

  const handleLongPress = () => {
    // Long-press menu — manual control over auto-completion. Without
    // this, the only way to "finish" a task was to wait for the runner
    // to exit on its own. Now: running/review tasks expose a "Mark
    // complete" action that stops the runner and flips status to
    // completed; completed tasks expose delete only.
    const canMarkComplete =
      item.status === "running" ||
      item.status === "queued" ||
      item.status === "review";
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

  // Last line is part of the key because capOutput() pins output.length
  // at the cap for long-running tasks — see chatMessages for the full
  // reasoning. The preview reads the tail, so the last line is what moves.
  const previewText = useMemo(
    () => buildTaskPreviewText(item),
    [item.id, item.status, item.resultText, item.output.length, item.output[item.output.length - 1]],
  );

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
          !isDark && { shadowColor: c.shadowSm },
        ]}
        onPress={onPress}
        onLongPress={handleLongPress}
        activeOpacity={0.86}
      >
        <View style={s.taskHeader}>
          <View style={s.taskHeaderMain}>
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
            {(item.tmuxSession || item.tmuxSessionId) && (
              <View style={[s.metaPill, { backgroundColor: "#8b5cf614", borderColor: "#8b5cf633" }]}>
                <Text style={[s.metaPillText, { color: "#8b5cf6" }]} numberOfLines={1}>
                  {`tmux ${[item.tmuxSession, item.tmuxSessionId].filter(Boolean).join(" · ")}`.trim()}
                </Text>
              </View>
            )}
            {item.chainId && (
              <View style={[s.metaPill, { backgroundColor: "#06b6d412", borderColor: "#06b6d433" }]}>
                <Text style={[s.metaPillText, { color: "#06b6d4" }]}>{`chain ${(item.chainOrder ?? 0) + 1}`}</Text>
              </View>
            )}
          </View>
          {/* Device + runner label on the right of the card header.
              User asked for the remote device + agent shown gracefully
              on each task card. Pulls from the task's authoritative
              fields (Task.deviceName + Task.runnerId), so a task that
              ran on a non-focused box doesn't get mislabelled with
              the focused device name. Trims `.local` and the trailing
              `-ephemeral` for compactness. */}
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
              const rid = item.runnerId;
              const runnerLabel =
                rid === "claude" || rid === "claude-code" ? "Claude"
                : rid === "codex" ? "Codex"
                : rid === "opencode" ? "OpenCode"
                : rid;
              if (!runnerLabel) return null;
              return (
                <Text style={[s.taskRunnerLabel, { color: c.textMuted }]} numberOfLines={1}>
                  {runnerLabel}
                </Text>
              );
            })()}
            <Pressable
              hitSlop={12}
              onPress={(event) => {
                event.stopPropagation();
                handleLongPress();
              }}
              style={({ pressed }) => [
                s.taskActionButton,
                { backgroundColor: c.bgInput, borderColor: c.borderSubtle },
                pressed && { opacity: 0.7 },
              ]}
              accessibilityRole="button"
              accessibilityLabel="Task actions"
            >
              <Ionicons name="ellipsis-horizontal" size={18} color={c.textMuted} />
            </Pressable>
          </View>
        </View>
        <Text style={[s.taskTitle, { color: c.textPrimary }]} numberOfLines={2}>{normalizeTaskTitle(item.title)}</Text>
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
const TaskCard = React.memo(TaskCardInner, (prev, next) => prev.item === next.item);

// ── Helpers ──────────────────────────────────────────────────────────

// Extract a usable error message from a failed task. Tasks don't
// have a structured error field — failures land in resultText (final
// summary the runner emitted) or the tail of the output stream. Pick
// the most informative thing we can find. ANSI is stripped because
// codex/opencode tend to colour stderr.
function extractTaskErrorMessage(task: Task): string {
  const stripAnsi = (s: string) => s.replace(/\x1B\[[0-9;]*[A-Za-z]/g, "");
  const result = task.resultText ? stripAnsi(task.resultText).trim() : "";
  if (result) return result;
  const out = (task.output || []).map(stripAnsi).map((l) => l.trim()).filter(Boolean);
  if (out.length === 0) return "Task failed without a clear reason.";
  // Keep the last ~6 lines so the user sees the immediate failure
  // context rather than just the final cryptic line.
  return out.slice(-6).join("\n");
}

// Build the rows shown in the AgentContextPanel below the chat. All
// fields are best-effort — we render whatever we have access to from
// the local state. Branch and full workDir aren't on the Task type
// today, so they're sourced from the screen's projectDir param when
// present. Runner / Model mirror the TaskHeader chip — same fallback
// chain so e.g. opencode tasks surface "glm-4.7" in both places.
interface AgentContextExtras {
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
    if (!modelLabel && extras.selectedModelId && isModelCompatibleWithRunnerId(extras.selectedModelId, task.runnerId)) {
      modelLabel = models.find((m) => m.id === extras.selectedModelId)?.name || extras.selectedModelId;
    }
    if (!modelLabel) {
      const fallbackId = preferredDefaultModelForRunner(
        task.runnerId,
        extras.activeDevice ?? {},
        extras.userEmail,
      );
      if (fallbackId) {
        modelLabel = models.find((m) => m.id === fallbackId)?.name || fallbackId;
      }
    }
    if (modelLabel) {
      rows.push({ label: "Model", value: modelLabel, mono: false });
    }

    // Mode + provider: opencode-flavoured details that the picker
    // sets per-device. Codex / Claude usually don't write these so
    // the rows stay hidden when empty — non-opencode tasks render
    // the same compact panel as before.
    const deviceId = extras.activeDevice?.id;
    if (deviceId) {
      const mode = extras.modeByDevice?.[deviceId];
      if (mode) rows.push({ label: "Mode", value: mode, mono: false });
      const provider = extras.providerByDevice?.[deviceId];
      if (provider) rows.push({ label: "Provider", value: provider, mono: false });
    }
  }
  if (task.tmuxSession || task.tmuxSessionId) {
    rows.push({
      label: "Tmux",
      value: [task.tmuxSession, task.tmuxSessionId].filter(Boolean).join(" · "),
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
    const normalizedContent = collapseAdjacentDuplicateLines(String(content || ""));
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

  if (task.turns && task.turns.length > 0) {
    for (const turn of task.turns) {
      pushMessage(turn.role, turn.content);
    }
  } else {
    pushMessage("user", normalizeTaskTitle(task.title));
    if (task.resultText) {
      pushMessage("assistant", task.resultText);
    }
  }

  // If running and we have streaming output, replace the last assistant message
  // with the live stream (which is more up-to-date than the polled turn data).
  // stripAnsi here so codex's `--full-auto` ANSI-coloured config dump
  // (`[1mworkdir:[0m /root` etc.) renders as plain text rather than
  // leaking control codes into the chat bubble.
  if (task.status === "running" && task.output.length > 0) {
    const streamText = buildLiveAssistantMarkdown(task.output.join("\n"));
    if (streamText.trim()) {
      // Remove the last assistant message if present — streaming output supersedes it
      const lastIdx = messages.length - 1;
      if (lastIdx >= 0 && messages[lastIdx].role === "assistant") {
        messages[lastIdx].content = streamText;
      } else {
        pushMessage("assistant", streamText);
      }
    }
  }

  return messages;
}

// ── Main screen ──────────────────────────────────────────────────────

export default function TasksScreen() {
  const c = useColors();
  const { isDark } = useTheme();
  const insets = useSafeAreaInsets();
  const taskRouter = useRouter();
  const layout = useResponsiveLayout();
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
  const taskParams = useLocalSearchParams<{
    dir?: string;
    prompt?: string;
    title?: string;
    runner?: string;
    openNew?: string;
  }>();
  const projectDir = typeof taskParams.dir === "string" ? taskParams.dir : "";
  const initialPrompt = typeof taskParams.prompt === "string" ? taskParams.prompt : "";
  const initialTitle = typeof taskParams.title === "string" ? taskParams.title : "";
  const initialRunner = typeof taskParams.runner === "string" ? taskParams.runner : "";
  const shouldOpenNew =
    typeof taskParams.openNew === "string" &&
    (taskParams.openNew === "1" || taskParams.openNew === "true");
  const { connectionStatus, activeDevice, devices, userDisconnected, lastError, agentAuthExpired, recoverDeviceAuth, selectDevice, disconnect, isLoadingDevices, everHadDevices, refreshDevices, deviceListError, stopReconnectAndBounce, retryConnection, primaryDeviceId, primaryRunnerByDevice, primaryModelByDevice, primaryModeByDevice, primaryProviderByDevice, setPrimaryRunnerForDevice, multiTargetMode, connectedDeviceIds } = useDevice();
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
  // Multi-target wizard state. Only used when DeviceContext.multiTargetMode
  // is true: the FAB opens the wizard first, the wizard sets pendingTarget
  // (and switches the QUIC client to that device via selectDevice), then
  // the compose modal opens with the runner + model locked to pendingTarget.
  const [showTargetWizard, setShowTargetWizard] = useState(false);
  const [pendingTarget, setPendingTarget] = useState<TaskTarget | null>(null);
  const [newTaskText, setNewTaskText] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [selectedModel, setSelectedModel] = useState<string>("sonnet");
  const [refreshing, setRefreshing] = useState(false);
  const [followUpText, setFollowUpText] = useState("");
  const [isSendingFollowUp, setIsSendingFollowUp] = useState(false);
  const [followUpExpanded, setFollowUpExpanded] = useState(false);
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
  const [availableRunners, setAvailableRunners] = useState<RunnerInfo[]>([]);
  const [runnersFetchState, setRunnersFetchState] = useState<RunnerFetchState>("idle");
  const [selectedRunner, setSelectedRunner] = useState<string>(""); // "" = default
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
  }, [activeDevice?.id]);
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
  const [showTmuxSessions, setShowTmuxSessions] = useState(false);
  const [tmuxSessions, setTmuxSessions] = useState<TmuxSession[]>([]);
  const [isLoadingTmux, setIsLoadingTmux] = useState(false);
  const [isAdopting, setIsAdopting] = useState<string | null>(null); // session name being adopted
  const chatScrollRef = useRef<FlatList>(null);
  const pendingOpenTaskRef = useRef<Task | null>(null);
  /** AbortController per in-flight yaver-agent run, keyed by synthetic
   *  task id. handleStopTask aborts the matching controller; the
   *  runner unwinds via AbortError and the task ends up "stopped". */
  const yaverAgentAbortersRef = useRef<Map<string, AbortController>>(new Map());
  const didApplyRouteSeedRef = useRef(false);

  // Project + Todo state
  const [projectName, setProjectName] = useState<string>("");
  const [projectBranch, setProjectBranch] = useState<string>("");
  const [todoCount, setTodoCount] = useState(0);
  const [todoTotal, setTodoTotal] = useState(0);
  const [todoDone, setTodoDone] = useState(0);

  // Speech state
  const { token, user, logout } = useAuth();
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
      mode?: string;
      video?: { enabled?: boolean; source?: "browser" | "sim-ios" | "sim-android" | "phone" };
      codeMode?: boolean;
      allowLocalFallback?: boolean;
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
  const [verbosity, setVerbosity] = useState(10);
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
    // fetched from or written to Convex. Non-speech prefs (ttsEnabled,
    // verbosity) still come from getUserSettings.
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
      if (s.verbosity !== undefined) setVerbosity(s.verbosity);
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
    quicClient.getOpenCodeConfig().then((cfg) => {
      if (cancelled) return;
      const names = (cfg?.agents || []).map((a) => a.name).filter((n): n is string => typeof n === "string" && n.length > 0);
      setOpencodeAgents(names);
    }).catch(() => {
      if (!cancelled) setOpencodeAgents([]);
    });
    return () => { cancelled = true; };
  }, [connectionStatus, selectedRunner, activeDevice?.id]);

  useEffect(() => {
    if (selectedRunner !== "opencode") return;
    if (userPickedRunnerRef.current) return;
    const preferredMode = activeDevice ? primaryModeByDevice[activeDevice.id] : "";
    if (preferredMode && selectedOpenCodeMode !== preferredMode) {
      setSelectedOpenCodeMode(preferredMode);
      return;
    }
    if (!preferredMode && selectedOpenCodeMode !== "") {
      setSelectedOpenCodeMode("");
    }
  }, [selectedRunner, activeDevice?.id, primaryModeByDevice, selectedOpenCodeMode]);

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
    const explicitRunner = activeDevice ? primaryRunnerByDevice[activeDevice.id] : "";
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
      const seededRunner = activeDevice
        ? preferredDefaultRunnerForDevice(activeDevice, user?.email, ready.map((r) => r.id).concat(installed.map((r) => r.id)))
        : null;
      const preferred =
        ready.find((r) => r.id === seededRunner) ||
        installed.find((r) => r.id === seededRunner) ||
        ready.find((r) => r.isDefault) ||
        ready.find((r) => r.id === "claude") ||
        ready.find((r) => r.id === "codex") ||
        ready.find((r) => r.id === "opencode") ||
        installed.find((r) => r.isDefault) ||
        installed[0];
      return preferred ? preferred.id : current;
    });
  }, [availableRunners, activeDevice, primaryRunnerByDevice, user?.email]);

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
    const explicitModel = activeDevice ? primaryModelByDevice[activeDevice.id] : "";
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
      const seededModel = activeDevice
        ? preferredDefaultModelForRunner(runner.id, activeDevice, user?.email)
        : null;
      const preferredModel =
        (seededModel && runner.models!.find((m) => m.id === seededModel)?.id) ||
        runner.models!.find((m) => m.isDefault)?.id ||
        runner.models![0].id;
      return preferredModel || current;
    });
  }, [availableRunners, activeDevice, primaryModelByDevice, selectedRunner, user?.email]);

  const selectedRunnerRow = useMemo(
    () => availableRunners.find((runner) => normalizeTaskRunnerId(runner.id) === normalizeTaskRunnerId(selectedRunner)) || null,
    [availableRunners, selectedRunner],
  );
  const selectedRunnerAuthIssue = useMemo(
    () => runnerAuthIssue(selectedRunnerRow),
    [selectedRunnerRow],
  );

  const resolveRunnerForSend = useCallback((fallbackRunner?: string | null): string | undefined => {
    return resolveRunnerForRemoteSend({
      activeDeviceId: activeDevice?.id,
      primaryRunnerByDevice,
      selectedRunner,
      fallbackRunner,
      userPickedRunner: userPickedRunnerRef.current,
    });
  }, [activeDevice?.id, primaryRunnerByDevice, selectedRunner]);

  const resolveModelForSend = useCallback((runnerId: string | undefined, fallbackModel?: string | null): string | undefined => {
    return resolveModelForRemoteSend({
      runnerId,
      activeDevice,
      primaryModelByDevice,
      selectedModel,
      fallbackModel,
      availableRunners,
      signedInEmail: user?.email,
      userPickedModel: userPickedModelRef.current,
    });
  }, [activeDevice, availableRunners, primaryModelByDevice, selectedModel, user?.email]);

  const refreshRunnerState = useCallback(async () => {
    if (connectionStatus !== "connected") return;
    setRunnersFetchState((prev) => (prev === "ok" ? prev : "loading"));
    try {
      const [probe, status] = await Promise.all([
        quicClient.getRunnersProbe(),
        quicClient.getAgentStatus(),
      ]);
      setAvailableRunners(probe.runners);
      setRunnersFetchState(probe.state);
      if (status) setAgentStatus(status);
    } catch {
      setRunnersFetchState("network-error");
    }
  }, [connectionStatus]);

  // Refresh runner + agent state on connect and keep retrying quickly until
  // the runner fetch is healthy. Once healthy, slow back down to background
  // polling so the banner stays honest without spamming the box.
  useEffect(() => {
    if (connectionStatus !== "connected") {
      setAgentStatus(null);
      setAvailableRunners([]);
      setAvailableModels([]);
      setRunnersFetchState("idle");
      return;
    }
    void refreshRunnerState();
    const cadenceMs = runnersFetchState === "ok" ? 30000 : 5000;
    const interval = setInterval(() => {
      void refreshRunnerState();
    }, cadenceMs);
    return () => clearInterval(interval);
  }, [activeDevice?.id, connectionStatus, refreshRunnerState, runnersFetchState]);

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
    if (initialPrompt) setNewTaskText(initialPrompt);
    if (initialRunner) setSelectedRunner(initialRunner);
    setShowNewTask(true);
  }, [initialPrompt, initialRunner, shouldOpenNew]);

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
        if (s) setAgentStatus(s);
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
    try {
      const list = await quicClient.listTasks();
      const focusedDeviceId = quicClient.attachedDeviceId || activeDevice?.id || "";
      const focusedDeviceName = devices.find((d) => d.id === focusedDeviceId)?.name || activeDevice?.name || "";
      // Filter out locally-deleted tasks and internal vibing-cache tasks
      const deletedIds = await getDeletedTaskIds();
      const filtered = list.filter((t) => !deletedIds.has(t.id) && t.source !== "vibing-cache");
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
      // Persist the (turns-stripped, small) list so a cold start paints instantly.
      void cacheTaskList(nextTasks);
      // The list endpoint STRIPS turns to bound its payload, so a fresh row
      // carries no history. Merging it verbatim onto an open task would wipe the
      // hydrated thread on every 3s poll (and hydration won't re-run — same id).
      // So preserve the richer in-memory turns whenever the fresh row lacks them.
      const keepTurns = (fresh: Task, old?: Task): Task =>
        old && (fresh.turns?.length ?? 0) === 0 && (old.turns?.length ?? 0) > 0
          ? { ...fresh, turns: old.turns, turnCount: old.turnCount ?? old.turns?.length }
          : fresh;
      setTasks((prev) => {
        const prevById = new Map(prev.map((t) => [t.id, t]));
        return nextTasks.map((t) => keepTurns(t, prevById.get(t.id)));
      });
      // Keep selected task in sync with latest data, but never let the stripped
      // list clobber the open thread's history.
      setSelectedTask((prev) => {
        if (!prev) return null;
        const fresh = nextTasks.find((t) => t.id === prev.id);
        return fresh ? keepTurns(fresh, prev) : prev;
      });
    } catch {}
  }, [activeDevice?.id, activeDevice?.name, devices]);

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

  const hasRunningTask = tasks.some(t => t.status === "running" || t.status === "queued");
  const effectiveFilter = statusFilter;
  const displayTasks = effectiveFilter === "all" ? tasks
    : effectiveFilter === "running" ? tasks.filter(t => t.status === "running" || t.status === "queued" || t.status === "review")
    : effectiveFilter === "review" ? tasks.filter(t => t.status === "review")
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
        const cached = await getCachedTaskList();
        if (cached.length > 0) {
          setTasks((prev) => (prev.length === 0 ? cached : prev));
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

  // Listen for streaming output — buffer updates to avoid UI freezing
  const outputBufferRef = useRef<Record<string, string[]>>({});
  const flushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // SSE stream for the selected running task (full live terminal stream)
  const sseAbortRef = useRef<(() => void) | null>(null);
  useEffect(() => {
    // Cleanup previous SSE
    if (sseAbortRef.current) {
      sseAbortRef.current();
      sseAbortRef.current = null;
    }
    if (!selectedTask || (selectedTask.status !== "running" && selectedTask.status !== "queued")) return;
    if (!quicClient.isConnected) return;

    const abort = quicClient.streamTaskOutput(
      selectedTask.id,
      (text) => {
        // Push SSE output into the same buffer system
        const lines = text.split("\n").filter(l => l);
        for (const line of lines) {
          if (!outputBufferRef.current[selectedTask.id]) {
            outputBufferRef.current[selectedTask.id] = [];
          }
          outputBufferRef.current[selectedTask.id].push(line);
        }
        if (!flushTimerRef.current) {
          flushTimerRef.current = setTimeout(flushOutputBuffer, 150);
        }
      },
      (status) => {
        if (status === "completed" || status === "review" || status === "failed" || status === "stopped") {
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
    );
    sseAbortRef.current = abort;

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
      abort();
    };
  }, [selectedTask?.id, selectedTask?.status]);

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

  const flushOutputBuffer = () => {
    const buffer = outputBufferRef.current;
    outputBufferRef.current = {};
    flushTimerRef.current = null;

    const taskIds = Object.keys(buffer);
    if (taskIds.length === 0) return;

    setTasks((prev) =>
      prev.map((t) => {
        const newLines = buffer[t.id];
        if (!newLines) return t;
        return { ...t, output: capOutput([...t.output, ...newLines]) };
      })
    );
    setSelectedTask((prev) => {
      if (!prev || !buffer[prev.id]) return prev;
      return { ...prev, output: capOutput([...prev.output, ...buffer[prev.id]]) };
    });
  };

  useEffect(() => {
    const unsub = quicClient.on("output", (taskId, line) => {
      // Check for Yaver control signals (auto-route)
      if (line.includes('"yaver_control"')) {
        try {
          const ctrl = JSON.parse(line);
          if (ctrl.yaver_control === "dev_server_ready") {
            // Dev server is ready — auto-route to Apps tab
            setSelectedTask(null);
            taskRouter.navigate("/(tabs)/apps");
          }
        } catch {}
      }

      if (!outputBufferRef.current[taskId]) {
        outputBufferRef.current[taskId] = [];
      }
      outputBufferRef.current[taskId].push(line);

      // Flush every 250ms to keep UI responsive while still showing progress
      if (!flushTimerRef.current) {
        flushTimerRef.current = setTimeout(flushOutputBuffer, 250);
      }
    });

    return () => {
      unsub();
      if (flushTimerRef.current) clearTimeout(flushTimerRef.current);
    };
  }, []);

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

  // Auto-scroll chat when output changes
  useEffect(() => {
    if (selectedTask) {
      setTimeout(() => chatScrollRef.current?.scrollToEnd({ animated: true }), 100);
    }
  }, [selectedTask?.output.length, selectedTask?.resultText, selectedTask?.status]);

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
        const full = await quicClient.getTask(taskId);
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
    if (ttsEnabled && selectedTask?.status === "completed" && selectedTask?.resultText && lastSpokenTaskRef.current !== selectedTask.id) {
      lastSpokenTaskRef.current = selectedTask.id;
      speakTaskResult(selectedTask.resultText);
    }
  }, [selectedTask?.status, selectedTask?.resultText, ttsEnabled, ttsProvider, speechApiKey]);

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

  // On-device sandbox notifications: when a task running on THIS phone's
  // sandbox transitions, reflect it in the ongoing foreground notification and
  // post a dismissible "task finished" notification on completion. This is the
  // user-facing payoff that justifies FOREGROUND_SERVICE_SPECIAL_USE — the work
  // keeps running and notifies even while the app is backgrounded. The native
  // side self-scopes (only fires when this device hosts the sandbox).
  const sandboxNotifRef = useRef<Map<string, TaskStatus>>(new Map());
  useEffect(() => {
    if (!isSandboxSupported()) return;
    for (const t of tasks) {
      const prev = sandboxNotifRef.current.get(t.id);
      if (prev === t.status) continue;
      sandboxNotifRef.current.set(t.id, t.status);
      if (t.status === "running") {
        void setSandboxTaskStatus(`Running: ${t.title || "coding task"}`);
      } else if (
        t.status === "completed" ||
        t.status === "review" ||
        t.status === "failed" ||
        t.status === "stopped"
      ) {
        void notifySandboxTaskFinished(t.title || "Coding task", t.status);
      }
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
    await fetchTasks();
    setRefreshing(false);
  }, [fetchTasks, refreshDevices]);

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

  // Sticky input mode. Rule (from the user, 2026-07-20): the initial mode is
  // voice/STT unless the user changes it, and a follow-up should default to
  // whatever method the user submitted the PRIOR message with. So this starts
  // at "voice", every submit records how it was actually sent (inputFromSpeech),
  // and opening the follow-up composer re-arms dictation when the last send was
  // voice. A ref, not state — it must be read synchronously inside the open
  // handler without forcing a re-render.
  const lastSubmitModeRef = useRef<"voice" | "text">("voice");

  // Open the follow-up composer, honouring the sticky mode: re-arm dictation
  // when the previous message went out by voice. startRecording is deferred a
  // tick so the expanded composer is mounted first (mirrors
  // openCreateTaskDictating), otherwise the recording UI attaches to a view
  // that is about to unmount.
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
          setText(savedBase ? savedBase + " " + partialText : partialText);
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
    const setText = recordingTargetRef.current === "followup" ? setFollowUpText : setNewTaskText;

    if (speechProvider === "on-device" && realtimeRef.current) {
      // Realtime: stop and get final text (already streamed into input)
      try {
        const finalText = await realtimeRef.current.stop();
        realtimeRef.current = null;
        if (finalText) {
          const base = preRecordText;
          setText(base ? base + " " + finalText : finalText);
          setInputFromSpeech(true);
        }
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        Alert.alert("Transcription failed", msg);
      }
      return;
    }

    // Cloud providers: stop recording, upload file
    if (!audioRecordingRef.current) return;
    setIsTranscribing(true);
    try {
      await audioRecordingRef.current.stopAndUnloadAsync();
      const uri = audioRecordingRef.current.getURI();
      audioRecordingRef.current = null;
      if (!uri) throw new Error("No recording URI");
      if (!speechProvider) throw new Error("No speech provider configured.");

      const result = await transcribe(uri, { provider: speechProvider, apiKey: speechApiKey, model: sttModel });
      if (result.text) {
        setText((prev) => (prev ? prev + " " + result.text : result.text));
        setInputFromSpeech(true);
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      Alert.alert("Transcription failed", msg);
    } finally {
      setIsTranscribing(false);
    }
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
  const triggerHermesReload = async () => {
    if (!isBundleLoaderAvailable()) {
      Alert.alert(
        "Reload unavailable",
        "This build of Yaver can't mount guest bundles. Update Yaver to the latest version, or use the Reload tab's dev-server controls.",
      );
      return;
    }
    const client = pendingTarget?.deviceId
      ? connectionManager.clientFor(pendingTarget.deviceId)
      : quicClient;
    const targetName = pendingTarget?.deviceName || activeDevice?.name || "the connected machine";
    setIsSubmitting(true);
    setReloadFlash(`Reloading on ${targetName}…`);
    try {
      const ok = await client.reloadDevServer({ mode: "bundle" });
      if (ok) {
        taskHaptics.send();
        setNewTaskText("");
        setInputFromSpeech(false);
        setReloadFlash(`Hermes reload pushed to ${targetName}`);
        setTimeout(() => setReloadFlash((cur) => (cur?.startsWith("Hermes reload pushed") ? null : cur)), 3500);
      } else {
        setReloadFlash(null);
        Alert.alert(
          "Reload failed",
          `Couldn't reach a dev server on ${targetName}. Start one from the Reload tab (or have the agent run a dev server for the project), then try again.`,
        );
      }
    } catch (e) {
      setReloadFlash(null);
      Alert.alert("Reload failed", e instanceof Error ? e.message : String(e));
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleCreateTask = async () => {
    if (!newTaskText.trim() && attachedImages.length === 0) return;

    // Remember how this task went out so the follow-up composer defaults to the
    // same input mode (voice ↔ text).
    lastSubmitModeRef.current = inputFromSpeech ? "voice" : "text";

    // Hermes-reload fast-path: a bare "reload"/"hot reload"/"hermes"
    // command — typed or dictated into the composer — shouldn't spin up a
    // full agent task. Push a fresh bundle to this phone directly. Skipped
    // when images are attached (clearly a real task) or with no live host.
    if (attachedImages.length === 0 && isEffectivelyConnected && isReloadIntent(newTaskText)) {
      if (isRecording) { try { await stopRecordingAndTranscribe(); } catch {} }
      await triggerHermesReload();
      return;
    }

    // Yaver-Agent fallback: when no host runner is connected, route the
    // prompt through the embedded control-plane LLM instead of failing
    // with "agent not ready". Streams the assistant's text + tool calls
    // into the task as they happen so users see progress before the
    // final reply lands. Cancellable via Stop on the task card.
    if (!isEffectivelyConnected) {
      const localCfg = await loadYaverAgentLocalConfig();
      if (!localCfg) {
        Alert.alert(
          "Configure Yaver Agent first",
          "No host device is connected. To run control-plane prompts (auth, status, primary management) without a host, save a provider + API key in Settings → Yaver Agent.",
        );
        return;
      }
      const promptText = newTaskText.trim();
      if (!promptText) return;
      Keyboard.dismiss();
      setIsSubmitting(true);

      const taskId = `yaver-agent-${Date.now()}`;
      const startedAt = Date.now();
      const startedAtIso = new Date(startedAt).toISOString();
      const initialTask: Task = {
        id: taskId,
        title: promptText,
        description: "",
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
        const ctx: YaverAgentToolContext = {
          devices: () => devices,
          primaryDeviceId: () => primaryDeviceId,
          secondaryDeviceId: () => null,
          selectDevice: async (deviceId) => {
            const d = devices.find((x) => x.id === deviceId);
            if (d) await selectDevice(d);
          },
        };
        const result = await runYaverAgent({
          prompt: promptText,
          ctx,
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

    if (selectedRunnerRow?.ready === false) {
      const detail =
        selectedRunnerAuthIssue ||
        selectedRunnerRow.error ||
        selectedRunnerRow.warning ||
        `${selectedRunnerRow.name} is installed but not ready on this machine.`;
      if (selectedRunnerAuthIssue && selectedRunnerRow.supportsBrowserAuth) {
        openRunnerAuthModal(selectedRunnerRow.id);
      } else {
        Alert.alert("Agent not ready", detail);
      }
      return;
    }
    // Stop any active recording before sending
    if (isRecording) {
      try { await stopRecordingAndTranscribe(); } catch {}
    }
    Keyboard.dismiss();
    setIsSubmitting(true);
    let pendingCloudTaskParams: Parameters<typeof saveDeferredCloudWorkspaceTask>[1] | null = null;
    try {
      const speechCtx = (speechProvider || verbosity < 10) ? {
        inputFromSpeech,
        sttProvider: speechProvider ?? undefined,
        ttsEnabled,
        ttsProvider,
        verbosity,
      } : undefined;
      const title = initialTitle || newTaskText.trim();
      // pendingTarget — set by TaskTargetWizard when multi-target mode
      // is on — overrides the in-modal runner/model picker for this
      // single submission. The wizard already switched the QUIC client
      // to pendingTarget.deviceId via selectDevice, so quicClient
      // baseUrl is correct without any per-call routing here.
      const effectiveRunner = pendingTarget?.runner
        ? normalizeTaskRunnerId(pendingTarget.runner)
        : resolveRunnerForSend();
      const effectiveModel = pendingTarget?.model && isModelCompatibleWithRunnerId(pendingTarget.model, effectiveRunner)
        ? pendingTarget.model
        : resolveModelForSend(effectiveRunner);
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
      const sendClient = pendingTarget?.deviceId
        ? connectionManager.clientFor(pendingTarget.deviceId)
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
        if (activeDevice) {
          try { await selectDevice(activeDevice); } catch {}
        }
        if (!sendClient.isConnected) {
          throw new Error(
            `Still connecting to ${pendingTarget?.deviceName ?? activeDevice?.name ?? "the device"} — wait for the status dot to turn green, then send again (or tap Retry).`,
          );
        }
      }
      const taskParams = {
        title,
        description: "",
        model: effectiveRunner === "custom" ? undefined : effectiveModel,
        runner: effectiveRunner === "custom" ? "custom" : effectiveRunner,
        customCommand: effectiveRunner === "custom" ? customCommand.trim() || undefined : undefined,
        speechContext: speechCtx,
        images: attachedImages.length > 0 ? attachedImages : undefined,
        workDir: projectDir || undefined,
        mode: effectiveRunner === "opencode" && effectiveOpencodeMode ? effectiveOpencodeMode : undefined,
        video: videoSummaryEnabled ? { enabled: true } : undefined,
        codeMode: true,
        allowLocalFallback: false,
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
      );
      // Stamp the task with the device + model we KNOW we sent it to
      // (sendTask response doesn't always echo deviceName; with the
      // pool the legitimate source is whichever client we picked).
      // Without this, the task card would later label itself with
      // activeDevice.name even though the work ran on a sibling box.
      const task: Task = {
        ...rawTask,
        deviceId: pendingTarget?.deviceId || activeDevice?.id || rawTask.deviceId,
        deviceName: pendingTarget?.deviceName || activeDevice?.name || rawTask.deviceName,
        model: rawTask.model || (effectiveRunner !== "custom" ? effectiveModel : undefined),
      };
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
        setNewTaskText("");
        setAttachedImages([]);
        setInputFromSpeech(false);
        setPendingTarget(null);
        setTasks((prev) => [pendingTask, ...prev.filter((task) => task.id !== pendingTask.id)]);
        pendingOpenTaskRef.current = pendingTask;
        setShowNewTask(false);
        Alert.alert(
          "Cloud Workspace is preparing",
          "Yaver kept this prompt on your phone and will dispatch it when the assigned workspace is ready.",
        );
      } else if (isAuthError(e)) {
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
        Alert.alert("Task failed", msg);
      }
    } finally {
      setIsSubmitting(false);
    }
  };

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
    if (showNewTask || showTargetWizard || showTmuxSessions) return;
    if (!pendingAfterDismissRef.current) return;
    const timer = setTimeout(flushAfterDismiss, 350);
    return () => clearTimeout(timer);
  }, [showNewTask, showTargetWizard, showTmuxSessions, flushAfterDismiss]);

  const handleStopTask = async (taskId: string) => {
    // Yaver-agent tasks live entirely on the phone — no server to call,
    // just abort the local runner. The runner's finally block flips
    // the task status, so we don't optimistic-update here.
    const localAborter = yaverAgentAbortersRef.current.get(taskId);
    if (localAborter) {
      localAborter.abort();
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
    ).then((retried) => {
      const deviceName = taskDevice?.name || task.deviceName || activeDevice?.name || retried.deviceName;
      const next = { ...retried, deviceId: taskDevice?.id || task.deviceId, deviceName, model: retried.model || retryModel };
      setTasks((prev) => [next, ...prev]);
      setSelectedTask(next);
    }).catch((err) => {
      Alert.alert("Retry failed", err instanceof Error ? err.message : String(err));
    });
  };

  const handleFollowUp = async () => {
    if (!selectedTask || (!followUpText.trim() && followUpImages.length === 0)) return;
    // Gate on a LIVE connection before firing. On a flap the socket is gone but
    // host/port/token linger, so the send would silently hit a dead URL and the
    // message would vanish with zero feedback (the 2026-07-21 "second follow-up
    // never submitted" report). The main composer already guards this way; the
    // follow-up path did not. Return BEFORE clearing the input so the text is kept.
    if (connectionStatus !== "connected") {
      Alert.alert(
        "Not connected",
        `Can't reach ${activeDevice?.name ?? "your machine"} right now — wait for the status dot to turn green, then tap Send again. Your message is kept.`,
      );
      return;
    }
    // Remember how this went out so the NEXT follow-up defaults to the same mode.
    lastSubmitModeRef.current = inputFromSpeech ? "voice" : "text";
    // Stop any active recording before sending
    if (isRecording) {
      try { await stopRecordingAndTranscribe(); } catch {}
    }
    Keyboard.dismiss();
    setIsSendingFollowUp(true);

    // Yaver-agent follow-up: continue the embedded LLM conversation
    // using prior turns as history. Same streaming + cancel rig as the
    // initial run.
    if (selectedTask.runnerId === "yaver-agent") {
      const promptText = followUpText.trim();
      if (!promptText) {
        setIsSendingFollowUp(false);
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
        const ctx: YaverAgentToolContext = {
          devices: () => devices,
          primaryDeviceId: () => primaryDeviceId,
          secondaryDeviceId: () => null,
          selectDevice: async (deviceId) => {
            const d = devices.find((x) => x.id === deviceId);
            if (d) await selectDevice(d);
          },
        };
        const result = await runYaverAgent({
          prompt: promptText,
          ctx,
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
    // the follow-up also forks (see parentFinished below) the view swaps to a
    // fresh child task, so the message was invisible in a chat that had itself
    // just been replaced by an empty one. That is the "I wrote a message and
    // cannot see it at all, then it shows a new task" report.
    //
    // The optimistic turn is carried across the fork so the conversation reads
    // continuously, the way a chat app is expected to behave.
    const optimisticText = followUpText.trim();
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

    // Undo the optimistic turn when the send does not happen. Leaving it would
    // show the user a message that was never sent — the same "UI states
    // something it does not know" failure this screen has been bitten by
    // before, just inverted.
    const rollbackOptimisticTurn = () => {
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
        // For adopted tmux sessions, send input directly via tmux send-keys
        await quicClient.sendTmuxInput(selectedTask.id, followUpText.trim());
      } else {
        // Decide between continue (resume in place) vs. fork (spawn a
        // child task). We fork when:
        //   - the user changed the runner picker since this task started, OR
        //   - the parent task already finished (completed/review/failed/
        //     stopped). Continuing a finished task in place tries to
        //     --resume the runner's old session; forking is cleaner and
        //     matches Codex/Claude Code "continue into a new session"
        //     semantics. See task_fork.go on the agent side.
        const parentRunner = (selectedTask.runnerId || "").trim();
        // A task detail follow-up is a chat reply, not a runner-picker action.
        // Keep it on the task's recorded runner so stale global picker state
        // cannot pop "Switch to Claude?" and break the WhatsApp-like thread.
        const desiredRunner = parentRunner || (selectedRunner || "").trim();
        // planFollowUp owns this decision so it can be tested without React
        // Native — see mobile/src/lib/followUpPlan.test.ts. It is the reason
        // follow-ups appeared to "create a new task": a finished parent always
        // forks, and finished is the normal state by the time a reply is typed.
        const plan = planFollowUp({
          isAdopted: selectedTask.isAdopted,
          parentRunner,
          desiredRunner,
          status: selectedTask.status,
        });
        const runnerChanged = plan.action === "fork-confirm";
        const switching = plan.action === "fork-confirm" || plan.action === "fork-silent";

        if (switching) {
          // Two flavors of fork:
          //  - runnerChanged: confirm before switching agents (different
          //    chat formats, picker explicitly changed by user).
          //  - parentFinished only: silent fork to a child task, same
          //    runner. This is the "continue a completed task" path —
          //    no extra dialog because the user just typed and tapped
          //    send.
          if (runnerChanged) {
            const niceName = desiredRunner.charAt(0).toUpperCase() + desiredRunner.slice(1);
            const confirmed = await new Promise<boolean>((resolve) => {
              Alert.alert(
                `Switch to ${niceName}?`,
                `Switching to ${niceName} will start a new child chat. ` +
                  `Yaver will include the most recent part of this conversation as context ` +
                  `so the new agent can pick up where you left off.\n\n` +
                  `For speed and token safety, Yaver sends roughly the last ~1200 words plus ` +
                  `the latest task summary, not the entire chat history.`,
                [
                  { text: "Cancel", style: "cancel", onPress: () => resolve(false) },
                  { text: `Switch to ${niceName}`, style: "default", onPress: () => resolve(true) },
                ],
              );
            });
            if (!confirmed) {
              // user backed out — drop the throw so the catch below
              // doesn't double-handle, then leave the input in place.
              rollbackOptimisticTurn();
              return;
            }
            try {
              console.log("[yaver-analytics]", JSON.stringify({
                event: "agent_switch_requested",
                source: "mobile",
                from: parentRunner,
                to: desiredRunner,
                ts: Date.now(),
              }));
            } catch { /* analytics is best-effort */ }
          }
          // Fork is non-destructive — no need to stop the parent.
          // Image attachments don't carry over (the child receives
          // text-only conversation context instead). When the parent
          // is finished but the runner is unchanged, send the parent's
          // runner so the fork uses the same one. Fork requires a
          // non-empty runner; legacy tasks without a recorded runnerId
          // fall back to claude.
          const forkRunner = plan.forkRunner;
          const result = await quicClient.forkTask(selectedTask.id, {
            runner: forkRunner,
            input: followUpText.trim(),
          });
          // Switch the chat to the new child so subsequent follow-ups
          // continue against the forked task.
          // Carry the conversation (including the turn we just optimistically
          // appended) into the child. Without this the fork presents as an
          // empty chat and the message the user just sent is gone from view.
          setSelectedTask((prev) => prev && prev.id === selectedTask.id
            ? {
                ...prev,
                id: result.taskId,
                runnerId: result.runnerId,
                status: "queued" as TaskStatus,
                turns: prev.turns ?? [],
              }
            : prev);
          if (runnerChanged) {
            try {
              console.log("[yaver-analytics]", JSON.stringify({
                event: "agent_switch_completed",
                source: "mobile",
                from: parentRunner,
                to: desiredRunner,
                contextWords: result.contextWordsUsed,
                ts: Date.now(),
              }));
            } catch { /* analytics is best-effort */ }
          }
        } else {
          // Same runner: regular continue. The agent now accepts
          // follow-ups while a task is still streaming and queues them
          // onto the same session instead of requiring a stop first.
          await quicClient.continueTask(selectedTask.id, followUpText.trim(), followUpImages.length > 0 ? followUpImages : undefined);
        }
      }
      setFollowUpText("");
      setFollowUpImages([]);
      await fetchTasks();
    } catch (err) {
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
      // Best-effort analytics for runtime-switch failures. Other
      // continue-task failures don't have analytics yet; if we add
      // them later, gate this on an explicit "was a switch" flag.
      try {
        const desiredRunner = (selectedRunner || "").trim();
        if (desiredRunner && selectedTask?.runnerId && desiredRunner !== selectedTask.runnerId) {
          console.log("[yaver-analytics]", JSON.stringify({
            event: "agent_switch_failed",
            source: "mobile",
            from: selectedTask.runnerId,
            to: desiredRunner,
            error: err instanceof Error ? err.message : String(err),
            ts: Date.now(),
          }));
        }
      } catch { /* analytics is best-effort */ }
    } finally {
      setIsSendingFollowUp(false);
    }
  };

  const handleDeleteTask = async (taskId: string) => {
    // Close detail modal if this task is open
    if (selectedTask?.id === taskId) setSelectedTask(null);
    setTasks((prev) => prev.filter((t) => t.id !== taskId));
    // Remember deletion so it won't reappear after refresh/re-login
    markTaskDeleted(taskId);
    try {
      await quicClient.deleteTask(taskId);
    } catch (e) {
      // Ignore errors — task is already removed locally and marked as deleted
      console.warn("[Tasks] Delete failed (kept local deletion):", e);
    }
  };

  const handleCompleteTask = async (taskId: string) => {
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
  // selected filter opens a popup to act on every active (running/queued/review)
  // task at once — the "delete all active / remove actives" the user asked for.
  // Stop-and-clear stops the running ones first (so the agent actually tears
  // them down) then removes them, otherwise a deleted-but-running task reappears
  // on the next poll.
  const activeTasks = () =>
    tasks.filter((t) => t.status === "running" || t.status === "queued" || t.status === "review");
  const handleActiveBulkActions = () => {
    const active = activeTasks();
    if (active.length === 0) {
      Alert.alert("No active tasks", "There are no running, queued, or review tasks to act on.");
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
            const ids = active.map((t) => t.id);
            // Optimistic: drop them locally so the list clears immediately.
            setTasks((prev) => prev.filter((t) => !ids.includes(t.id)));
            try { await quicClient.stopAllTasks(); } catch (e) { console.warn("[Tasks] Stop (for remove) failed:", e); }
            await Promise.all(ids.map((id) => markTaskDeleted(id).catch(() => {})));
            try {
              await Promise.all(ids.map((id) => quicClient.deleteTask(id).catch(() => {})));
            } finally {
              await fetchTasks();
            }
          },
        },
      ],
    );
  };

  const handleDeleteAll = async () => {
    const deletable = tasks.filter((t) => t.status !== "running" && t.status !== "queued");
    if (deletable.length === 0) return;
    setTasks((prev) => prev.filter((t) => t.status === "running" || t.status === "queued"));
    await Promise.all(deletable.map((t) => markTaskDeleted(t.id)));
    try {
      await quicClient.deleteAllTasks();
      await fetchTasks();
    } catch (e) {
      console.warn("[Tasks] Clear failed (kept local deletions):", e);
    }
  };

  // Ship It — one-tap deploy
  const handleShipIt = async () => {
    try {
      const { targets } = await quicClient.getDeployTargets();
      if (targets.length === 0) {
        Alert.alert("No Deploy Targets", "Could not detect any deploy targets for this project.");
        return;
      }
      if (targets.length === 1) {
        // Single target — deploy directly
        Alert.alert(
          "Ship It",
          `Deploy to ${targets[0].name}?`,
          [
            { text: "Cancel", style: "cancel" },
            { text: "Ship It", onPress: async () => {
              try {
                const result = await quicClient.deploy(targets[0].id);
                Alert.alert("Deploying", `Deploying to ${result.target}...`);
                await fetchTasks();
              } catch (e) {
                Alert.alert("Deploy Failed", e instanceof Error ? e.message : String(e));
              }
            }},
          ]
        );
      } else {
        // Multiple targets — let user pick
        const buttons: { text: string; onPress?: () => void; style?: "cancel" | "destructive" | "default" }[] = targets.map(t => ({
          text: t.name,
          onPress: () => {
            quicClient.deploy(t.id).then((result) => {
              Alert.alert("Deploying", `Deploying to ${result.target}...`);
              fetchTasks();
            }).catch((e) => {
              Alert.alert("Deploy Failed", e instanceof Error ? e.message : String(e));
            });
          },
        }));
        buttons.push({ text: "Cancel", style: "cancel" });
        Alert.alert("Pick Deploy Target", "Where do you want to ship?", buttons);
      }
    } catch (e) {
      Alert.alert("Error", e instanceof Error ? e.message : String(e));
    }
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

  // Tmux session management
  const handleOpenTmuxSessions = async () => {
    setShowTmuxSessions(true);
    setIsLoadingTmux(true);
    try {
      const sessions = await quicClient.listTmuxSessions();
      setTmuxSessions(sessions);
    } catch {
      setTmuxSessions([]);
    } finally {
      setIsLoadingTmux(false);
    }
  };

  // paneId picks WHICH agent when a session is split across panes; without it
  // the agent adopts the session's active pane, which on a split window is not
  // necessarily the one the user tapped.
  const handleAdoptTmuxSession = async (sessionName: string, paneId?: string) => {
    setIsAdopting(paneId ? `${sessionName}#${paneId}` : sessionName);
    try {
      const result = await quicClient.adoptTmuxSession(sessionName, paneId);
      // Refresh both lists
      const [sessions] = await Promise.all([quicClient.listTmuxSessions(), fetchTasks()]);
      setTmuxSessions(sessions);
      // Resolve the task BEFORE closing, then hand the chat-detail
      // Modal off to the tmux Modal's dismiss — opening it in the same
      // tick makes it present invisibly behind the sheet on iOS.
      const updatedTasks = await quicClient.listTasks();
      const newTask = updatedTasks.find(t => t.id === result.taskId);
      handoffModal(
        () => setShowTmuxSessions(false),
        () => { if (newTask) setSelectedTask(newTask); },
      );
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      Alert.alert("Adopt Failed", msg);
    } finally {
      setIsAdopting(null);
    }
  };

  const handleDetachTmuxSession = async (taskId: string) => {
    try {
      await quicClient.detachTmuxSession(taskId);
      await fetchTasks();
      // If we're viewing this task, close the detail modal
      if (selectedTask?.id === taskId) setSelectedTask(null);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      Alert.alert("Detach Failed", msg);
    }
  };

  // The header banner ALSO needs to reflect the connection-manager pool
  // — without this, a user with two live pooled clients but a
  // momentarily-stale focused client would see "Disconnected · <name>"
  // at the top of Tasks while Devices simultaneously rendered both
  // boxes as CONNECTED. Promote effectiveState to "connected" whenever
  // any pool client reports live, so the banner mirrors the source of
  // truth the Devices tab is already reading from.
  const anyPoolConnected = connectedDeviceIds.length > 0;
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
  const activeLiveInPool = !!activeDevice && connectedDeviceIds.includes(activeDevice.id);
  const effectiveState: ConnectionState =
    activeLiveInPool ? "connected" :
    connectionStatus === "error" ? "connecting" :
    (!activeDevice && anyPoolConnected) ? "connected" :
    // Active device selected but not actually live (incl. an optimistic
    // connectionStatus==="connected") → still connecting, not green.
    connectionStatus === "connected" ? "connecting" :
    connectionStatus;
  const isEffectivelyConnected = effectiveState === "connected";

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
  const canComposeTask = isEffectivelyConnected || hasAnyPooledConnection;

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

  // The mic FAB: open the composer AND start dictating in one tap.
  //
  // Deliberately reuses startRecording("task") rather than inventing a second
  // capture path — that function already streams whisper partials into the
  // very input the user is about to send, which IS the requested behaviour:
  // watch the words land, correct them, press send. A parallel implementation
  // would drift from it.
  //
  // Recording starts only once the composer is actually up. When
  // multiTargetMode has no connection, openCreateTask opens the target wizard
  // instead, and a mic that was already hot behind a modal would record the
  // user picking a machine — five minutes of audio they never asked for.
  const openCreateTaskDictating = useCallback(() => {
    openCreateTask();
    const composerWillOpen = !(multiTargetMode && (!activeDevice || !isEffectivelyConnected));
    if (!composerWillOpen) return;
    // One frame, so the modal is mounted before the mic opens; otherwise the
    // first partials land in an input nobody is looking at yet.
    requestAnimationFrame(() => {
      void startRecording("task");
    });
  }, [openCreateTask, multiTargetMode, activeDevice, isEffectivelyConnected]);

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
      selectedTask?.output.length,
      selectedTask?.output[0],
      selectedTask?.output[(selectedTask?.output.length ?? 1) - 1],
      selectedTask?.turns?.length,
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
        activeDeviceId: activeDevice?.id,
        primaryRunnerByDevice,
        selectedRunner,
        userPickedRunner: userPickedRunnerRef.current,
      }),
    [activeDevice?.id, primaryRunnerByDevice, selectedRunner],
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
                      {connMode === "direct" ? "Direct" : connMode === "relay" ? "Relay" : "Transport pending"}
                    </Text>
                    {runnerBannerState ? (
                      <Text style={[s.bannerStatusCopy, { color: c.textSecondary, flexShrink: 1 }]} numberOfLines={1}>
                        · {runnerBannerState.text}
                      </Text>
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
                    {runnerBannerState?.kind === "needsConfig" ? (
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
                    ) : runnerBannerState?.kind === "authNeeded" ? (
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
                    ) : runnerBannerState &&
                      runnerBannerState.kind !== "ok" &&
                      runnerBannerState.kind !== "loading" &&
                      runnerBannerState.kind !== "failed" &&
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
                    ) : runnerBannerState?.kind === "failed" ? (
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
            </>
          }
        />

        {/* Dev server preview banner */}
        {isEffectivelyConnected && <View style={{ marginTop: 12 }}><DevPreview /></View>}

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
              {([
                { key: "running" as const, label: "Active", color: c.accent, count: tasks.filter(t => t.status === "running" || t.status === "queued" || t.status === "review").length },
                { key: "review" as const, label: "Review", color: "#8b5cf6", count: tasks.filter(t => t.status === "review").length },
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
              {isEffectivelyConnected && (
                <Pressable style={[s.utilityButton, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }]} onPress={handleOpenTmuxSessions}>
                  <Text style={[s.actionButtonText, { color: "#8b5cf6" }]}>Tmux</Text>
                </Pressable>
              )}
              <Pressable style={[s.utilityButton, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }]} onPress={() => setShowLogs(true)}>
                <Text style={[s.actionButtonText, { color: "#94a3b8" }]}>Logs</Text>
              </Pressable>
              {isEffectivelyConnected && (
                <Pressable style={[s.utilityButton, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }]} onPress={handleShipIt}>
                  <Text style={[s.actionButtonText, { color: "#f97316" }]}>Ship It</Text>
                </Pressable>
              )}
              {isEffectivelyConnected && (
                <Pressable style={[s.utilityButton, { backgroundColor: c.bgCard, borderColor: c.borderSubtle }]} onPress={handleShowSummary}>
                  <Text style={[s.actionButtonText, { color: "#06b6d4" }]}>Summary</Text>
                </Pressable>
              )}
            </ScrollView>
            <View pointerEvents="none" style={[s.actionBarFade, { backgroundColor: c.bg }]} />
          </View>
        )}

        <FlatList
          data={displayTasks}
          keyExtractor={(item) => item.id}
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
                body="No tasks yet. Start one and it runs on your machine."
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
                onPress={() => setSelectedTask(item)}
                onDelete={() => handleDeleteTask(item.id)}
                onComplete={() => handleCompleteTask(item.id)}
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
            accessibilityLabel="Dictate a new task"
            onPress={openCreateTaskDictating}
          >
            <Ionicons name="mic" size={26} color="#ffffff" />
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
                <Pressable disabled={submittingAgentAnswer} onPress={() => setAgentQuestion(null)} style={{ paddingVertical: 12, paddingHorizontal: 18 }}>
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
            setShowNewTask(false);
            setNewTaskText("");
            setAttachedImages([]);
            setInputFromSpeech(false);
            setPendingTarget(null);
          }}
        >
          <KeyboardAvoidingView style={s.modalOverlay} behavior={Platform.OS === "ios" ? "padding" : "height"}>
            <Pressable style={s.modalDismiss} onPress={() => { Keyboard.dismiss(); setShowNewTask(false); setNewTaskText(""); setAttachedImages([]); setInputFromSpeech(false); setPendingTarget(null); }} />
            <View style={[s.modalContent, { backgroundColor: c.bgCard }]}>
              {/* Two-row header: title + close on top, target chip below.
                  The chip lived inline with the title, but device names
                  like "Mobiles-Mac-mini.local · Claude" overflowed and
                  collided with the title text. Stacking lets the chip
                  use the full row width and show the full label without
                  truncation or layout pressure on the title. */}
              <View style={s.modalHeaderStack}>
                <View style={s.modalHeaderRow}>
                  <Text style={[s.modalTitle, { color: c.textPrimary }]}>New task</Text>
                  <Pressable
                    hitSlop={{ top: 12, bottom: 12, left: 12, right: 12 }}
                    onPress={() => {
                      Keyboard.dismiss();
                      setShowNewTask(false);
                      setNewTaskText("");
                      setAttachedImages([]);
                      setInputFromSpeech(false);
                      setPendingTarget(null);
                    }}
                    style={({ pressed }) => [s.modalCloseButton, pressed && { opacity: 0.55 }]}
                    accessibilityRole="button"
                    accessibilityLabel="Close new task"
                  >
                    <Ionicons name="close" size={24} color={c.textSecondary} />
                  </Pressable>
                </View>
                {/* Target chip row — runner+model pill mirrors the badge
                    in the follow-up bar so the user can pick the agent
                    at task creation, not only after the task starts. */}
                <View style={s.modalTargetRow}>
                  {pendingTarget ? (
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
                      {activeDevice?.name || "Pick a machine"}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 10, marginLeft: 4 }}>▾</Text>
                  </Pressable>
                  )}
                  {/* Coding-agent chip — a quick, inline way to pick Claude
                      Code / Codex / OpenCode without opening the full wizard.
                      Only the agents actually installed on this box are
                      offered, so the picker never lists something that can't
                      run. Hidden while a wizard-locked target is bound. */}
                  {!pendingTarget && (availableRunners.length > 0 || !!selectedRunner) && (
                    <Pressable
                      hitSlop={{ top: 12, bottom: 12, left: 12, right: 12 }}
                      style={({ pressed }) => [
                        s.agentBadge,
                        { backgroundColor: c.bgCardElevated, borderColor: c.border, flexShrink: 1, marginLeft: 8 },
                        pressed && { opacity: 0.55 },
                      ]}
                      onPress={() => {
                        // Prefer the agents actually installed on this box, but
                        // never leave the user stuck: if the runner list hasn't
                        // (re)loaded — e.g. right after a task failed — fall back
                        // to any known runner so they can always switch away from
                        // a failed agent. Un-installed picks are marked so the
                        // choice stays honest.
                        const installed = availableRunners.filter((r) => r.installed);
                        // The fallback list used to synthesise `installed: false` for any
                        // runner it had no row for, turning MISSING DATA into a stated
                        // fact: with availableRunners empty, every agent rendered
                        // "(not installed)" — including ones the box reports installed
                        // AND ready. Seen against a mini whose /runner-auth/status said
                        // codex/opencode/glm were installed=true ready=true while the
                        // picker declared all three absent and put a checkmark on one:
                        // selected and non-existent in the same row.
                        const fallback = ["claude", "codex", "opencode"].map(
                          (id) => availableRunners.find((r) => normalizeTaskRunnerId(r.id) === id) || ({ id } as RunnerInfo),
                        );
                        const haveRunnerData = availableRunners.length > 0;
                        const choices = installed.length > 0 ? installed : fallback;
                        Alert.alert(
                          "Coding agent",
                          haveRunnerData ? undefined : runnerFetchAlertMessage(runnersFetchState),
                          [
                            ...choices.map((r) => ({
                              text:
                                displayRunnerLabel(r.id) +
                                (normalizeTaskRunnerId(r.id) === normalizeTaskRunnerId(selectedRunner) ? "  ✓" : "") +
                                (r.installed === false ? "  (not installed)" : ""),
                              onPress: () => {
                                setSelectedRunner(normalizeTaskRunnerId(r.id));
                                userPickedRunnerRef.current = true;
                              },
                            })),
                            { text: "Cancel", style: "cancel" as const },
                          ],
                        );
                      }}
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
              </View>
              <View
                style={[
                  s.composerShell,
                  {
                    backgroundColor: c.bg,
                    borderColor: c.border,
                  },
                ]}
              >
                <TextInput
                  style={[s.input, s.inputMultiline, s.composerInput, { color: c.textPrimary }]}
                  placeholder={tasks.length > 0 ? "Send another command…" : "What should the agent do?"}
                  placeholderTextColor={c.textMuted}
                  value={newTaskText}
                  onChangeText={(t) => { setNewTaskText(t); setInputFromSpeech(false); }}
                  multiline numberOfLines={4} textAlignVertical="top" autoFocus
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
                    {/* Symmetric mic↔text switch (audit §4.2, 2026-07-19).
                        Vibe has a "Prefer to type?" pill; the composer needs
                        the mirror. Tapping this closes the composer, seeds
                        Vibe with whatever the user just typed (preserved as
                        lastHeardRef on the Vibe side — the same channel
                        that carries speech going the other direction), and
                        navigates to /vibe so the loop reopens. Quiet by
                        design — voice is the primary path, this pill is
                        the escape hatch back to it. */}
                    <Pressable
                      style={({ pressed }) => [
                        s.composerActionButton,
                        { backgroundColor: c.bgCard },
                        pressed && { opacity: 0.7 },
                      ]}
                      onPress={() => {
                        const seed = newTaskText.trim();
                        Keyboard.dismiss();
                        setShowNewTask(false);
                        setNewTaskText("");
                        setAttachedImages([]);
                        setInputFromSpeech(false);
                        setPendingTarget(null);
                        taskRouter.push({
                          pathname: "/vibe",
                          params: seed ? { prompt: seed } : {},
                        } as any);
                      }}
                      accessibilityRole="button"
                      accessibilityLabel="Switch to voice — reopens Vibe with your typed text preserved"
                    >
                      <Ionicons name="chatbubble-ellipses-outline" size={20} color={c.textMuted} />
                    </Pressable>
                    {/* ⚡ Reload — one-tap Hermes bundle push to this phone
                        from the selected machine. Needs the native bundle
                        loader (iOS + Android); hidden on builds without it. */}
                    {isEffectivelyConnected && isBundleLoaderAvailable() && (
                      <Pressable
                        style={({ pressed }) => [
                          s.composerActionButton,
                          { backgroundColor: c.bgCard },
                          pressed && { opacity: 0.7 },
                        ]}
                        onPress={() => { taskHaptics.send(); triggerHermesReload(); }}
                        disabled={isSubmitting || isTranscribing}
                      >
                        <Ionicons name="flash-outline" size={22} color={c.textPrimary} />
                      </Pressable>
                    )}
                    {(() => {
                      const isDisabled =
                        (!newTaskText.trim() && attachedImages.length === 0) ||
                        isSubmitting ||
                        isTranscribing ||
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
                            handleCreateTask();
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
            </View>
          </KeyboardAvoidingView>
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
                          if (activeDevice?.id) {
                            void setPrimaryRunnerForDevice(activeDevice.id, r.id, null).catch(() => {});
                          }
                          if (r.id === "opencode" && runnerAuthIssue(r)) {
                            setOpenCodeConfigTarget(activeDevice?.id || null);
                            setOpenCodeConfigStartInAdd(true);
                            setShowOpenCodeConfig(true);
                            return;
                          }
                          if (runnerAuthIssue(r) && r.supportsBrowserAuth) {
                            openRunnerAuthModal(r.id);
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
                      {selectedRunnerAuthIssue && selectedRunnerRow.id === "opencode" ? (
                        <Pressable
                          onPress={() => {
                            setOpenCodeConfigTarget(activeDevice?.id || null);
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
                          onPress={() => openRunnerAuthModal(selectedRunnerRow.id)}
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
                        if (activeDevice?.id && selectedRunner) {
                          void setPrimaryRunnerForDevice(activeDevice.id, selectedRunner, m.id).catch(() => {});
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
                          if (activeDevice?.id && selectedRunner === "opencode") {
                            void setPrimaryRunnerForDevice(
                              activeDevice.id,
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
          deviceName={devices.find((d) => d.id === (runnerAuthModalTarget || activeDevice?.id))?.name || activeDevice?.name || "this machine"}
          // Routes /runner-auth/browser/* via /peer/<id> when set, so
          // OAuth runs against the remote box where the runner actually
          // lives — not the device the phone happens to be focused on.
          target={runnerAuthModalTarget || activeDevice?.id || undefined}
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
              <View style={[s.cockpitListPane, { backgroundColor: c.bg, borderRightColor: c.border, paddingTop: insets.top + 8 }]}>
                <View style={s.cockpitListHeader}>
                  <Text style={[s.cockpitListTitle, { color: c.textPrimary }]}>Tasks</Text>
                  <View style={{ flex: 1 }} />
                  <Pressable
                    hitSlop={10}
                    style={[s.cockpitListBtn, { backgroundColor: c.accentSoft }]}
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
                  keyExtractor={(item) => item.id}
                  contentContainerStyle={s.cockpitListContent}
                  showsVerticalScrollIndicator={false}
                  renderItem={({ item }) => {
                    const active = item.id === selectedTask?.id;
                    return (
                      <View style={[s.cockpitSelWrap, active && { backgroundColor: c.accentSoft }]}>
                        <TaskCard
                          item={item}
                          onPress={() => setSelectedTask(item)}
                          onDelete={() => handleDeleteTask(item.id)}
                          onComplete={() => handleCompleteTask(item.id)}
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
                    width: Math.max(560, layout.width * 0.58),
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
                  runnerLabel={selectedTask.runnerId ? displayRunnerLabel(selectedTask.runnerId) : undefined}
                  tmuxSession={selectedTask.tmuxSession}
                  tmuxSessionId={selectedTask.tmuxSessionId}
                  modelLabel={(() => {
                    // Authoritative source: Task.model from the agent
                    // (now plumbed through quic.ts). Picker fallback
                    // only kicks in for legacy tasks that don't carry
                    // the field — without this priority order the
                    // header would label cross-device tasks with the
                    // currently-focused box's picker, producing the
                    // "Claude Code · GPT-5.4" mislabel.
                    const taskModelId = (selectedTask as any)?.model as string | undefined;
                    if (taskModelId) {
                      return availableModels.find((m) => m.id === taskModelId)?.name || taskModelId;
                    }
                    const explicit = isModelCompatibleWithRunnerId(selectedModel, selectedTask.runnerId)
                      ? availableModels.find((m) => m.id === selectedModel)?.name
                      : undefined;
                    if (explicit) return explicit;
                    const fallbackId = preferredDefaultModelForRunner(
                      selectedTask.runnerId,
                      activeDevice ?? {},
                      user?.email,
                    );
                    if (!fallbackId) return undefined;
                    return availableModels.find((m) => m.id === fallbackId)?.name || fallbackId;
                  })()}
                  onBack={() => { setSelectedTask(null); setFollowUpText(""); }}
                  onOpenLogs={() => setShowLogs(true)}
                  primaryAction={
                    selectedTask.status === "failed" ? "retry"
                      : selectedTask.status === "review" ? "complete"
                      : isRunning && selectedTask.isAdopted ? "detach"
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
                  onDetach={() => {
                    Alert.alert(
                      "Detach Session",
                      `Stop monitoring "${selectedTask.tmuxSession || "tmux session"}"? The session will keep running.`,
                      [
                        { text: "Cancel", style: "cancel" },
                        { text: "Detach", onPress: () => handleDetachTmuxSession(selectedTask.id) },
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

                {/* Failed-task recovery: a one-tap path to switch the
                    runner/model and re-run. The header's plain "retry"
                    re-sends with the SAME runner + default model, so a
                    model error (e.g. "gpt-5.4 not supported with a ChatGPT
                    account") just reproduces — this opens the Agent & Model
                    picker seeded to the task's runner and re-runs on close. */}
                {selectedTask.status === "failed" ? (
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

                {/* Dev server banner — shown inside task detail so user doesn't have to go back */}
                {isEffectivelyConnected && <DevPreview />}

                {/* Chat messages */}
                {/* FlatList (not ScrollView+.map) so streaming a 60-message
                    chat doesn't re-render every prior bubble each token —
                    that O(n) work per token was what saturated the JS
                    thread and made the keyboard feel dead while the agent
                    was running. ChatBubble is React.memo'd with content
                    equality, so windowed rows skip re-render entirely.
                    PhaseStatusLine + DebugSection ride along as
                    ListFooterComponent. */}
                  <FlatList
                    ref={chatScrollRef as any}
                    data={chatMessages}
                    keyExtractor={(item, idx) => `${idx}-${item.role}`}
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
                        {/* ThinkingBubble used to render here next to
                            PhaseStatusLine; the two pulsing effects
                            stacked on top of each other made the
                            screen feel busy. The runner+model info it
                            carried is now surfaced as a chip in the
                            TaskHeader, so we only keep the one
                            spinner-with-elapsed line below. */}
                        {isRunning && <PhaseStatusLine task={selectedTask} />}
                        {selectedTask.status === "failed" && (() => {
                          const errMsg = extractTaskErrorMessage(selectedTask);
                          return (
                            <ErrorMessage
                              message={errMsg}
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
                                ExpoClipboard.setStringAsync(errMsg);
                                Alert.alert("Copied", "Error copied to clipboard.");
                              }}
                            />
                          );
                        })()}
                        <AgentContextPanel
                          rows={buildAgentContextRows(selectedTask, selectedTask.deviceName || activeDevice?.name, connMode, availableModels, {
                            selectedModelId: selectedModel,
                            activeDevice: activeDevice ?? undefined,
                            userEmail: user?.email,
                            modeByDevice: primaryModeByDevice,
                            providerByDevice: primaryProviderByDevice,
                          })}
                          defaultExpanded={selectedTask.status === "failed"}
                        />
                        <CommandsPanel models={cmdCardsByTask[selectedTask.id]} />
                        <DebugSection task={selectedTask} connMode={connMode} c={c} />
                      </>
                    }
                  />

                {/* Follow-up input: compact bar, expands to full card on tap */}
                {followUpExpanded ? (
                  <View style={[s.modalContent, { backgroundColor: c.bgCard, borderTopWidth: 1, borderTopColor: c.border }]}>
                    <View style={s.modalHeader}>
                      <Text style={[s.modalTitle, { color: c.textPrimary }]}>Follow Up</Text>
                      {/* Runtime agent switch — tap to open the same picker
                          that's on the New Task screen, but here a different
                          selection forks the chat to a child task with the
                          new runner instead of continuing in place. See
                          handleFollowUp's `switching` branch + task_fork.go. */}
                      <Pressable
                        hitSlop={{ top: 12, bottom: 12, left: 12, right: 12 }}
                        style={({ pressed }) => [
                          s.agentBadge,
                          { backgroundColor: c.bgCardElevated, borderColor: c.border, marginLeft: "auto", marginRight: 10 },
                          pressed && { opacity: 0.55 },
                        ]}
                        onPress={() => setShowAgentPicker(true)}
                        accessibilityRole="button"
                        accessibilityLabel="Change coding agent and model for this chat"
                      >
                        <Text style={[s.agentBadgeText, { color: c.textSecondary }]}>
                          {(() => {
                            // Show the parent task's runner by default, but
                            // reflect a pending picker change if the user
                            // already tapped a different chip — handleFollowUp
                            // forks when these differ from selectedTask.runnerId.
                            const parentRunner = selectedTask?.runnerId || "";
                            const desiredRunner = (selectedRunner || parentRunner).trim();
                            const runner = availableRunners.find(r => r.id === desiredRunner);
                            const model = availableModels.find(m => m.id === selectedModel);
                            const runnerLabel = runner?.name || (desiredRunner ? desiredRunner : "Claude");
                            const modelLabel = model?.name || selectedModel || "";
                            const labelText = modelLabel ? `${runnerLabel} · ${modelLabel}` : runnerLabel;
                            // Hint when the picker is set to a different runner
                            // than the parent task's — the next Send forks.
                            const isPendingFork = parentRunner && desiredRunner && desiredRunner !== parentRunner;
                            return isPendingFork ? `→ ${labelText}` : labelText;
                          })()}
                        </Text>
                        <Text style={{ color: c.textMuted, fontSize: 10, marginLeft: 4 }}>▾</Text>
                      </Pressable>
                      {isRunning && <ActivityIndicator size="small" color={c.accent} />}
                    </View>
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
                      onChangeText={(t) => { setFollowUpText(t); setInputFromSpeech(false); }}
                      multiline numberOfLines={4} textAlignVertical="top" autoFocus
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
                      <Pressable style={[s.cancelButton, { backgroundColor: c.bgCardElevated }]} onPress={() => { Keyboard.dismiss(); setFollowUpExpanded(false); }}>
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
                          onPress={() => { handleFollowUp(); setFollowUpExpanded(false); }}
                          disabled={(!followUpText.trim() && followUpImages.length === 0) || isSendingFollowUp || isTranscribing}
                        >
                          <Text style={s.submitButtonText}>{isSendingFollowUp ? "Sending..." : "Send"}</Text>
                        </Pressable>
                      </View>
                    </View>
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
                    {isRunning ? (
                      <Pressable
                        hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
                        style={({ pressed }) => [
                          {
                            width: 44, height: 44, borderRadius: 12,
                            backgroundColor: c.errorBg,
                            alignItems: "center", justifyContent: "center",
                            borderWidth: 1, borderColor: c.error,
                          },
                          pressed && { opacity: 0.7 },
                        ]}
                        onPress={() => {
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
                        accessibilityRole="button"
                        accessibilityLabel="Stop task"
                      >
                        <Text style={{ color: c.error, fontSize: 16, fontWeight: "700", lineHeight: 18 }}>{"■"}</Text>
                      </Pressable>
                    ) : (
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
                    )}
                  </View>
                )}
              </View>
            )}
          </KeyboardAvoidingView>
        </Modal>
        {/* ── Logs Modal ─────────────────────────────────────────── */}
        <Modal visible={showLogs} animationType="slide" transparent onRequestClose={() => setShowLogs(false)}>
          <View style={s.logsModalOverlay}>
            <Pressable style={{ height: 80 }} onPress={() => setShowLogs(false)} />
            <View style={[s.logsModal, { backgroundColor: c.bg }]}>
              <View style={[s.logsHeader, { borderBottomColor: c.border }]}>
                <Text style={[s.logsTitle, { color: c.textPrimary }]}>{selectedTask ? "Live Logs" : "Connection Logs"}</Text>
                <View style={s.logsHeaderActions}>
                  <Pressable onPress={() => {
                    ExpoClipboard.setStringAsync(combinedLogText || "No logs yet.");
                    Alert.alert("Copied", "Logs copied to clipboard.");
                  }}>
                    <Text style={[s.logsActionText, { color: c.accent }]}>Copy</Text>
                  </Pressable>
                  <Pressable onPress={() => setShowLogs(false)} style={{ marginLeft: 16 }}>
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
        </Modal>
        {/* ── Tmux Sessions Modal ────────────────────────────────── */}
        <Modal visible={showTmuxSessions} animationType="slide" transparent onDismiss={flushAfterDismiss} onRequestClose={() => setShowTmuxSessions(false)}>
          <View style={s.logsModalOverlay}>
            <Pressable style={{ height: 80 }} onPress={() => setShowTmuxSessions(false)} />
            <View style={[s.logsModal, { backgroundColor: c.bg }]}>
              <View style={[s.logsHeader, { borderBottomColor: c.border }]}>
                <Text style={[s.logsTitle, { color: c.textPrimary }]}>Tmux Sessions</Text>
                <View style={s.logsHeaderActions}>
                  <Pressable onPress={handleOpenTmuxSessions}>
                    <Text style={[s.logsActionText, { color: c.accent }]}>Refresh</Text>
                  </Pressable>
                  <Pressable onPress={() => setShowTmuxSessions(false)} style={{ marginLeft: 16 }}>
                    <Text style={[s.logsActionText, { color: c.textMuted }]}>Close</Text>
                  </Pressable>
                </View>
              </View>
              <ScrollView style={s.logsScroll} contentContainerStyle={{ padding: 12 }}>
                {isLoadingTmux ? (
                  <View style={{ alignItems: "center", paddingTop: 40 }}>
                    <ActivityIndicator size="large" color={c.accent} />
                    <Text style={{ color: c.textMuted, marginTop: 12, fontSize: 14 }}>Scanning sessions...</Text>
                  </View>
                ) : tmuxSessions.length === 0 ? (
                  <View style={{ alignItems: "center", paddingTop: 40 }}>
                    <Text style={{ color: c.textMuted, fontSize: 16, marginBottom: 8 }}>No tmux sessions</Text>
                    <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center", lineHeight: 20, paddingHorizontal: 20 }}>
                      Start a tmux session on your dev machine to see it here.{"\n"}
                      e.g. tmux new -s claude
                    </Text>
                  </View>
                ) : (
                  tmuxSessions.map((session) => {
                    const isBeingAdopted = isAdopting === session.name;
                    const alreadyAdopted = session.relationship === "adopted";
                    const agent = session.agentType || "shell";

                    return (
                      <View
                        key={session.name}
                        style={[s.tmuxCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
                      >
                        <View style={s.tmuxCardHeader}>
                          <View style={{ flex: 1 }}>
                            <Text style={[s.tmuxName, { color: c.textPrimary }]}>{session.name}</Text>
                            <View style={{ flexDirection: "row", alignItems: "center", gap: 6, marginTop: 4 }}>
                              <View style={[s.statusBadge, { backgroundColor: agent !== "shell" ? "#22c55e22" : "#a1a1aa22" }]}>
                                <Text style={[s.statusText, { color: agent !== "shell" ? "#22c55e" : "#a1a1aa" }]}>{agent}</Text>
                              </View>
                              <Text style={{ color: c.textMuted, fontSize: 11 }}>
                                {session.windows} window{session.windows !== 1 ? "s" : ""}
                                {session.attached ? " · attached" : ""}
                              </Text>
                            </View>
                            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }} numberOfLines={1}>
                              {[session.id, session.windowIndex !== undefined ? `w${session.windowIndex}` : "", session.paneId || (session.paneIndex !== undefined ? `p${session.paneIndex}` : "")]
                                .filter(Boolean)
                                .join(" · ")}
                              {session.windowName ? ` · ${session.windowName}` : ""}
                            </Text>
                          </View>
                          {alreadyAdopted ? (
                            <View style={[s.statusBadge, { backgroundColor: "#8b5cf622" }]}>
                              <Text style={[s.statusText, { color: "#8b5cf6" }]}>adopted</Text>
                            </View>
                          ) : session.relationship === "forked-by-yaver" ? (
                            <View style={[s.statusBadge, { backgroundColor: "#6366f122" }]}>
                              <Text style={[s.statusText, { color: "#6366f1" }]}>yaver</Text>
                            </View>
                          ) : null}
                        </View>

                        {/* Pane preview */}
                        {session.panePreview ? (
                          <View style={[s.tmuxPreview, { backgroundColor: c.bg, borderColor: c.border }]}>
                            <Text style={[s.tmuxPreviewText, { color: c.textSecondary }]} numberOfLines={5}>
                              {session.panePreview}
                            </Text>
                          </View>
                        ) : null}

                        {/* Action button */}
                        {alreadyAdopted ? (
                          <View style={{ flexDirection: "row", gap: 8, marginTop: 10 }}>
                            <Pressable
                              style={[s.tmuxActionBtn, { backgroundColor: c.accent + "18", flex: 1 }]}
                              onPress={() => {
                                // Open the task detail
                                setShowTmuxSessions(false);
                                const task = tasks.find(t => t.id === session.taskId);
                                if (task) setSelectedTask(task);
                              }}
                            >
                              <Text style={[s.tmuxActionText, { color: c.accent }]}>View Task</Text>
                            </Pressable>
                            <Pressable
                              style={[s.tmuxActionBtn, { backgroundColor: "#ef444418" }]}
                              onPress={() => {
                                Alert.alert(
                                  "Detach Session",
                                  `Stop monitoring "${session.name}"? The tmux session will keep running.`,
                                  [
                                    { text: "Cancel", style: "cancel" },
                                    { text: "Detach", style: "destructive", onPress: () => {
                                      if (session.taskId) handleDetachTmuxSession(session.taskId);
                                      // Refresh list
                                      handleOpenTmuxSessions();
                                    }},
                                  ]
                                );
                              }}
                            >
                              <Text style={[s.tmuxActionText, { color: "#ef4444" }]}>Detach</Text>
                            </Pressable>
                          </View>
                        ) : session.relationship !== "forked-by-yaver" && (session.panes?.length ?? 0) > 1 ? (
                          // A split window is several agents, so offer one row
                          // each rather than a single "Adopt Session" that
                          // silently picks whichever pane is active.
                          <View style={{ marginTop: 10, gap: 6 }}>
                            {session.panes!.map((pane) => {
                              const paneKey = `${session.name}#${pane.paneId}`;
                              const busy = isAdopting === paneKey;
                              const tone = pane.status === "awaiting-input" ? "#f59e0b"
                                : pane.status === "working" ? "#22c55e"
                                : pane.status === "no-agent" ? "#a1a1aa" : c.textMuted;
                              return (
                                <Pressable
                                  key={pane.paneId}
                                  style={[s.tmuxActionBtn, { backgroundColor: "#8b5cf618" }, busy && s.submitButtonDisabled]}
                                  onPress={() => handleAdoptTmuxSession(session.name, pane.paneId)}
                                  disabled={busy || !!pane.taskId}
                                >
                                  <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                                    {busy ? <ActivityIndicator size="small" color="#8b5cf6" /> : null}
                                    <Text style={[s.tmuxActionText, { color: "#8b5cf6" }]} numberOfLines={1}>
                                      {pane.taskId ? "Adopted" : "Adopt"} {pane.agent || "shell"} · {pane.paneId}
                                    </Text>
                                    <Text style={{ color: tone, fontSize: 11 }}>{pane.status}</Text>
                                  </View>
                                </Pressable>
                              );
                            })}
                          </View>
                        ) : session.relationship !== "forked-by-yaver" ? (
                          <Pressable
                            style={[s.tmuxActionBtn, { backgroundColor: "#8b5cf618", marginTop: 10 }, isBeingAdopted && s.submitButtonDisabled]}
                            onPress={() => handleAdoptTmuxSession(session.name)}
                            disabled={isBeingAdopted}
                          >
                            {isBeingAdopted ? (
                              <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                                <ActivityIndicator size="small" color="#8b5cf6" />
                                <Text style={[s.tmuxActionText, { color: "#8b5cf6" }]}>Adopting...</Text>
                              </View>
                            ) : (
                              <Text style={[s.tmuxActionText, { color: "#8b5cf6" }]}>Adopt Session</Text>
                            )}
                          </Pressable>
                        ) : null}
                      </View>
                    );
                  })
                )}
              </ScrollView>
            </View>
          </View>
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

  // New task modal
  modalOverlay: { flex: 1, backgroundColor: "rgba(0,0,0,0.6)", justifyContent: "flex-end" },
  modalDismiss: { flex: 1 },
  modalContent: { borderTopLeftRadius: 30, borderTopRightRadius: 30, padding: 24, paddingTop: 28, paddingBottom: 40 },
  modalHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginBottom: 24 },
  modalHeaderStack: { marginBottom: 20 },
  modalHeaderRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  modalTargetRow: { flexDirection: "row", alignItems: "center", marginTop: 12 },
  modalCloseButton: { width: 36, height: 36, borderRadius: 18, alignItems: "center", justifyContent: "center" },
  modalTitle: { fontSize: 20, fontWeight: "700" },
  agentBadge: { flexDirection: "row", alignItems: "center", paddingHorizontal: 14, paddingVertical: 10, borderRadius: 999, borderWidth: 1 },
  agentBadgeText: { fontSize: 12, fontWeight: "500" },
  agentPickerSheet: { borderTopLeftRadius: 20, borderTopRightRadius: 20, paddingBottom: 40 },
  agentPickerHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 20, paddingVertical: 16, borderBottomWidth: 1 },
  agentPickerTitle: { fontSize: 17, fontWeight: "700" },
  agentPickerSection: { fontSize: 11, fontWeight: "600", letterSpacing: 0.5, marginTop: 16, marginBottom: 8, marginLeft: 20 },
  agentPickerChips: { flexDirection: "row", flexWrap: "wrap", gap: 8, paddingHorizontal: 16, marginBottom: 4 },
  input: { borderWidth: 1, borderRadius: 12, padding: 16, fontSize: 16, marginBottom: 12 },
  inputMultiline: { minHeight: 160 },
  composerShell: {
    borderWidth: 1,
    borderRadius: 28,
    paddingTop: 8,
    paddingHorizontal: 8,
    paddingBottom: 8,
    marginBottom: 14,
    ...lightCardShadow,
  },
  composerInput: {
    borderWidth: 0,
    borderRadius: 22,
    backgroundColor: "transparent",
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

  assistantRow: { flexDirection: "row", justifyContent: "flex-start", marginBottom: 12 },
  // assistantFrame is the assistant's chat bubble — WhatsApp/Claude-mobile
  // shaped: a subtle fill, rounded with a bottom-LEFT tail (mirror of the
  // user bubble's bottom-right tail), snug to its content rather than
  // full-width. maxWidth 90% (a touch wider than the user's 80% because
  // agent replies carry code/markdown that needs room); backgroundColor is
  // applied inline from the theme. Fenced code blocks keep their own inner
  // border so they still stand out against the bubble fill.
  assistantFrame: { maxWidth: "90%", borderRadius: 20, borderBottomLeftRadius: 6, paddingHorizontal: 14, paddingVertical: 10 },
  assistantTokens: { fontSize: 12, marginBottom: 6, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" },
  assistantToggle: { fontSize: 12, fontWeight: "600" },

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

  // Tmux sessions
  tmuxCard: { borderRadius: 12, padding: 14, borderWidth: 1, marginBottom: 10 },
  tmuxCardHeader: { flexDirection: "row", alignItems: "flex-start", justifyContent: "space-between" },
  tmuxName: { fontSize: 15, fontWeight: "600", fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" },
  tmuxPreview: { marginTop: 10, padding: 10, borderRadius: 8, borderWidth: 1 },
  tmuxPreviewText: { fontSize: 11, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", lineHeight: 16 },
  tmuxActionBtn: { paddingVertical: 8, paddingHorizontal: 14, borderRadius: 8, alignItems: "center" },
  tmuxActionText: { fontSize: 13, fontWeight: "600" },
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
