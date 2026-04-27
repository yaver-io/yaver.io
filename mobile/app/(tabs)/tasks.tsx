import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Video, ResizeMode } from "expo-av";
import { clipUrl } from "../../src/lib/vibePreview";
import {
  ActivityIndicator,
  Alert,
  Animated,
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
import { SafeAreaView } from "react-native-safe-area-context";
import Markdown from "react-native-markdown-display";
import { useDevice } from "../../src/context/DeviceContext";
import { useColors } from "../../src/context/ThemeContext";
import * as ExpoClipboard from "expo-clipboard";
import { getLogEntries, onLogsChanged, LogEntry } from "../../src/lib/logger";
import {
  AgentStatus,
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
import { markTaskDeleted, getDeletedTaskIds } from "../../src/lib/storage";
import { useAuth } from "../../src/context/AuthContext";
import { getUserSettings, getLocalSecret, LOCAL_KEYS, type SpeechProvider } from "../../src/lib/auth";
import { transcribe, initWhisper, isWhisperReady, startRealtimeTranscribe, SPEECH_PROVIDERS } from "../../src/lib/speech";
import { shareIntentEmitter } from "../../src/lib/shareIntent";
import { useLocalSearchParams, useRouter } from "expo-router";
import { DevPreview } from "../../src/components/DevPreview";

// ── Constants ────────────────────────────────────────────────────────

const STATUS_COLORS: Record<TaskStatus, string> = {
  queued: "#eab308",
  running: "#6366f1",
  completed: "#22c55e",
  failed: "#ef4444",
  stopped: "#a1a1aa",
};

const BANNER_CONFIG: Record<
  ConnectionState,
  { bg: string; border: string; dot: string; text: string; label: string }
> = {
  connected: {
    bg: "#0d1a0d",
    border: "#1a2e1a",
    dot: "#22c55e",
    text: "#4ade80",
    label: "Connected",
  },
  connecting: {
    bg: "#1a1a0d",
    border: "#2e2e1a",
    dot: "#eab308",
    text: "#facc15",
    label: "Reconnecting",
  },
  error: {
    bg: "#1a0d0d",
    border: "#2e1a1a",
    dot: "#ef4444",
    text: "#f87171",
    label: "Reconnecting",
  },
  disconnected: {
    bg: "#111",
    border: "#222",
    dot: "#666",
    text: "#666",
    label: "Disconnected",
  },
};

function isKivancAccount(email: string | null | undefined): boolean {
  return String(email || "").trim().toLowerCase() === "kivanc.cakmak@icloud.com";
}

function isKivancMacBook(device: { name?: string | null; hostName?: string | null; os?: string | null }): boolean {
  const haystack = `${device.name || ""} ${device.hostName || ""}`.toLowerCase();
  const isMac = ["darwin", "macos"].includes(String(device.os || "").trim().toLowerCase());
  if (!isMac) return false;
  return haystack.includes("kivanc") || haystack.includes("cakmak") || haystack.includes("macbook");
}

function preferredDefaultRunnerForDevice(
  device: { name?: string | null; hostName?: string | null; os?: string | null },
  signedInEmail: string | null | undefined,
  availableRunnerIds: string[],
): string | null {
  if (availableRunnerIds.length === 0) return null;
  const unique = Array.from(new Set(availableRunnerIds.filter(Boolean)));
  if (isKivancAccount(signedInEmail)) {
    if (isKivancMacBook(device) && unique.includes("claude")) return "claude";
    if (!isKivancMacBook(device) && unique.includes("codex")) return "codex";
  }
  if (unique.includes("claude")) return "claude";
  if (unique.includes("codex")) return "codex";
  return unique[0] || null;
}

function preferredDefaultModelForRunner(
  runnerId: string | null | undefined,
  device: { name?: string | null; hostName?: string | null; os?: string | null },
  signedInEmail: string | null | undefined,
): string | null {
  const normalized = String(runnerId || "").trim().toLowerCase();
  if (!normalized) return null;
  if (isKivancAccount(signedInEmail)) {
    if (normalized === "claude" && isKivancMacBook(device)) return "claude-opus-4-7";
    if (normalized === "codex" && !isKivancMacBook(device)) return "gpt-5.4";
  }
  if (normalized === "claude") return "claude-opus-4-7";
  if (normalized === "codex") return "gpt-5.4";
  if (normalized === "aider-ollama") return "qwen2.5-coder:14b";
  return null;
}

type DeviceProbeState = {
  reachable: boolean;
  needsAuth: boolean;
  checkedAt: number;
};

async function probeDeviceInfo(device: { host: string; port: number }): Promise<DeviceProbeState | null> {
  try {
    const res = await fetch(`http://${device.host}:${device.port || 18080}/info`, {
      signal: AbortSignal.timeout(2500),
    });
    if (!res.ok) {
      return { reachable: false, needsAuth: false, checkedAt: Date.now() };
    }
    const info = await res.json().catch(() => ({}));
    const needsAuth = info?.needsAuth === true || info?.mode === "bootstrap";
    return { reachable: true, needsAuth, checkedAt: Date.now() };
  } catch {
    return { reachable: false, needsAuth: false, checkedAt: Date.now() };
  }
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

function stripMarkdownForPreview(text: string): string {
  return text
    .replace(/```[\s\S]*?```/g, " code block ")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, "$1")
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/^\s*>\s?/gm, "")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/\*([^*]+)\*/g, "$1")
    .replace(/_/g, "")
    .replace(/\r/g, "")
    .replace(/[ \t]+\n/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

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

function buildAssistantPreview(content: string): {
  summary: string;
  activity: string[];
  shouldCollapse: boolean;
} {
  const plain = stripMarkdownForPreview(content);
  const summaryLines = plain
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .filter((line) => !line.startsWith("$ "));
  const summary = summaryLines.slice(0, 4).join("\n").slice(0, 320).trim();
  const activity = extractAssistantActivity(content);
  const shouldCollapse =
    content.length > Math.max(summary.length + 80, 320) ||
    activity.length > 0 ||
    content.includes("```") ||
    content.includes("|");

  return {
    summary: summary || "Working...",
    activity,
    shouldCollapse,
  };
}

function buildTaskPreviewText(task: Task): string | null {
  if (task.resultText) {
    return stripMarkdownForPreview(task.resultText).slice(0, 120);
  }
  if (task.status === "running" || task.status === "queued") {
    const live = stripMarkdownForPreview(task.output.join("\n")).split("\n").map((line) => line.trim()).filter(Boolean);
    if (live.length > 0) return live.slice(-1)[0].slice(0, 120);
    return "Working...";
  }
  return null;
}

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

type TaskPhaseTone = "neutral" | "active" | "warm" | "success";

function deriveTaskPhases(task: Task): Array<{ label: string; tone: TaskPhaseTone }> {
  const tail = task.output.length > 120 ? task.output.slice(-120) : task.output;
  const haystack = `${task.title}\n${tail.join("\n")}\n${task.resultText || ""}`.toLowerCase();
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

type RunnerBannerKind =
  | "ok"
  | "authNeeded"
  | "notRunnable"
  | "notInstalled"
  | "blocked";

const RUNNER_BANNER_TONES: Record<RunnerBannerKind, string> = {
  ok: "#4ade80",
  authNeeded: "#fbbf24",
  notRunnable: "#fbbf24",
  notInstalled: "#f87171",
  blocked: "#fbbf24",
};

function deriveRunnerBannerState(
  runners: RunnerInfo[],
  agentStatus: AgentStatus | null,
): { text: string; tone: string; kind: RunnerBannerKind } | null {
  if (runners.length === 0 && !agentStatus) return null;

  const installed = runners.filter((runner) => runner.installed);
  const runnable = installed.filter((runner) => !runner.error);
  const authed = installed.filter((runner) => runner.authConfigured);
  const current = agentStatus?.runner;
  const make = (kind: RunnerBannerKind, text: string) => ({
    text,
    tone: RUNNER_BANNER_TONES[kind],
    kind,
  });

  if (installed.length === 0) {
    return make("notInstalled", "No agents available");
  }
  if (runnable.length === 0 && authed.length === 0) {
    return make("authNeeded", "Agents available, none authenticated");
  }
  if (runnable.length === 0) {
    return make("notRunnable", "Agents available, none runnable");
  }
  if (current?.installed === false) {
    return make("notInstalled", `${current.name} not installed`);
  }
  if (current?.error && !current?.authConfigured) {
    return make("authNeeded", `${current.name} needs sign-in`);
  }
  if (current?.error) {
    return make("blocked", `${current.name} blocked`);
  }
  if (current?.name) {
    return make(
      "ok",
      `${current.name} ready${agentStatus?.runningTasks ? ` · ${agentStatus.runningTasks} running` : ""}`,
    );
  }
  return make(
    "ok",
    `${runnable.length} agent${runnable.length > 1 ? "s" : ""} ready`,
  );
}

// ── Chat bubble ──────────────────────────────────────────────────────

function ChatBubble({
  turn,
  c,
}: {
  turn: { role: string; content: string };
  c: ReturnType<typeof useColors>;
}) {
  const isUser = turn.role === "user";

  if (isUser) {
    return (
      <View style={s.userRow}>
        <View style={[s.userBubble, { backgroundColor: c.accent || "#6366f1" }]}>
          <Text style={s.userBubbleText}>{turn.content}</Text>
        </View>
      </View>
    );
  }

  const preview = useMemo(() => buildAssistantPreview(turn.content), [turn.content]);
  const [expanded, setExpanded] = useState(false);

  return (
    <View style={s.assistantRow}>
      <View style={[s.assistantBubble, { backgroundColor: c.bgCardElevated || "#1a1a2e" }]}>
        <View style={s.assistantHeaderRow}>
          <Text style={[s.assistantLabel, { color: c.textMuted }]}>Update</Text>
          {preview.shouldCollapse ? (
            <Pressable onPress={() => setExpanded((value) => !value)}>
              <Text style={[s.assistantToggle, { color: c.accent || "#6366f1" }]}>
                {expanded ? "Hide raw" : "Show raw"}
              </Text>
            </Pressable>
          ) : null}
        </View>
        <Text style={[s.assistantSummary, { color: c.textPrimary }]}>{preview.summary}</Text>
        {preview.activity.length > 0 ? (
          <View style={s.assistantActivityList}>
            {preview.activity.map((item, index) => (
              <View key={`${item}-${index}`} style={[s.assistantActivityRow, { borderColor: c.border, backgroundColor: c.bg + "55" }]}>
                <Text
                  style={[
                    s.assistantActivityText,
                    { color: item.startsWith("$ ") ? c.accent || "#6366f1" : c.textSecondary },
                  ]}
                  numberOfLines={2}
                >
                  {item}
                </Text>
              </View>
            ))}
          </View>
        ) : null}
        {expanded ? (
          <View style={[s.assistantRawWrap, { borderTopColor: c.border }]}>
            <Markdown style={markdownStyles(c)}>{turn.content || " "}</Markdown>
          </View>
        ) : null}
      </View>
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

function TaskCard({
  item,
  onPress,
  onDelete,
}: {
  item: Task;
  onPress: () => void;
  onDelete: () => void;
}) {
  const c = useColors();
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
    if (isRunning) {
      Alert.alert("Stop & Delete Task", "This will kill the running process and remove the task.", [
        { text: "Cancel", style: "cancel" },
        { text: "Stop & Delete", style: "destructive", onPress: onDelete },
      ]);
    } else {
      Alert.alert("Delete Task", "Are you sure?", [
        { text: "Cancel", style: "cancel" },
        { text: "Delete", style: "destructive", onPress: onDelete },
      ]);
    }
  };

  const previewText = buildTaskPreviewText(item);

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
        style={[s.cardContainer, s.taskCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
        onPress={onPress}
        onLongPress={handleLongPress}
        activeOpacity={0.86}
      >
        <View style={s.taskHeader}>
          <View style={s.taskHeaderMain}>
            <View style={[s.statusBadge, { backgroundColor: STATUS_COLORS[item.status] + "1f" }]}>
              {isRunning ? (
                <Animated.View style={[s.statusPulseDot, { backgroundColor: STATUS_COLORS[item.status], opacity: pulse }]} />
              ) : (
                <View style={[s.statusPulseDot, { backgroundColor: STATUS_COLORS[item.status] }]} />
              )}
              <Text style={[s.statusText, { color: STATUS_COLORS[item.status] }]}>{item.status}</Text>
            </View>
            {item.isAdopted && (
              <View style={[s.metaPill, { backgroundColor: "#8b5cf614", borderColor: "#8b5cf633" }]}>
                <Text style={[s.metaPillText, { color: "#8b5cf6" }]}>tmux</Text>
              </View>
            )}
            {item.chainId && (
              <View style={[s.metaPill, { backgroundColor: "#06b6d412", borderColor: "#06b6d433" }]}>
                <Text style={[s.metaPillText, { color: "#06b6d4" }]}>{`chain ${(item.chainOrder ?? 0) + 1}`}</Text>
              </View>
            )}
          </View>
          {item.runnerId && item.runnerId !== "claude" && item.runnerId !== "unknown" ? (
            <Text style={[s.taskRunnerLabel, { color: c.textMuted }]} numberOfLines={1}>
              {item.runnerId}
            </Text>
          ) : null}
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

// ── Helpers ──────────────────────────────────────────────────────────

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

  if (task.turns && task.turns.length > 0) {
    for (const turn of task.turns) {
      messages.push({ role: turn.role, content: turn.content });
    }
  } else {
    messages.push({ role: "user", content: normalizeTaskTitle(task.title) });
    if (task.resultText) {
      messages.push({ role: "assistant", content: task.resultText });
    }
  }

  // If running and we have streaming output, replace the last assistant message
  // with the live stream (which is more up-to-date than the polled turn data)
  if (task.status === "running" && task.output.length > 0) {
    const streamText = task.output.join("\n");
    if (streamText.trim()) {
      // Remove the last assistant message if present — streaming output supersedes it
      const lastIdx = messages.length - 1;
      if (lastIdx >= 0 && messages[lastIdx].role === "assistant") {
        messages[lastIdx].content = streamText;
      } else {
        messages.push({ role: "assistant", content: streamText });
      }
    }
  }

  return messages;
}

// ── Main screen ──────────────────────────────────────────────────────

export default function TasksScreen() {
  const c = useColors();
  const taskRouter = useRouter();
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
  const { connectionStatus, activeDevice, devices, userDisconnected, lastError, agentAuthExpired, recoverDeviceAuth, selectDevice, disconnect, isLoadingDevices, refreshDevices, unreachableDeviceIds, stopReconnectAndBounce, primaryRunnerByDevice, primaryModelByDevice } = useDevice();
  const unreachableSet = useMemo(() => new Set(unreachableDeviceIds), [unreachableDeviceIds]);
  const [deviceProbeMap, setDeviceProbeMap] = useState<Record<string, DeviceProbeState>>({});
  const [showLogs, setShowLogs] = useState(false);
  const [logs, setLogs] = useState<LogEntry[]>(getLogEntries());
  const [isRefreshingDevices, setIsRefreshingDevices] = useState(false);

  // Subscribe to log changes
  useEffect(() => {
    return onLogsChanged(() => setLogs(getLogEntries()));
  }, []);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [statusFilter, setStatusFilter] = useState<"running" | "completed" | "failed" | "all">("running");
  const [selectedTask, setSelectedTask] = useState<Task | null>(null);
  const [showNewTask, setShowNewTask] = useState(false);
  const [newTaskText, setNewTaskText] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [selectedModel, setSelectedModel] = useState<string>("sonnet");
  const [refreshing, setRefreshing] = useState(false);
  const [followUpText, setFollowUpText] = useState("");
  const [isSendingFollowUp, setIsSendingFollowUp] = useState(false);
  const [followUpExpanded, setFollowUpExpanded] = useState(false);
  const [attachedImages, setAttachedImages] = useState<ImageAttachment[]>([]);
  const [followUpImages, setFollowUpImages] = useState<ImageAttachment[]>([]);
  const [isReconnecting, setIsReconnecting] = useState(false);
  const [reconnectError, setReconnectError] = useState<string | null>(null);
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
  const [selectedRunner, setSelectedRunner] = useState<string>(""); // "" = default
  // OpenCode-only: which agent (build / plan / custom) drives the
  // task. Forwarded as `mode` on the task POST and turned into
  // `--agent <mode>` on `opencode run`. Empty = use the user's
  // defaultAgent from opencode.json. Other runners ignore it.
  const [selectedOpenCodeMode, setSelectedOpenCodeMode] = useState<string>("");
  const [availableModels, setAvailableModels] = useState<ModelInfo[]>([]);
  const [customCommand, setCustomCommand] = useState("");
  const [showAgentPicker, setShowAgentPicker] = useState(false);
  const [showTmuxSessions, setShowTmuxSessions] = useState(false);
  const [tmuxSessions, setTmuxSessions] = useState<TmuxSession[]>([]);
  const [isLoadingTmux, setIsLoadingTmux] = useState(false);
  const [isAdopting, setIsAdopting] = useState<string | null>(null); // session name being adopted
  const chatScrollRef = useRef<ScrollView>(null);
  const pendingOpenTaskRef = useRef<Task | null>(null);
  const didApplyRouteSeedRef = useRef(false);

  // Project + Todo state
  const [projectName, setProjectName] = useState<string>("");
  const [projectBranch, setProjectBranch] = useState<string>("");
  const [todoCount, setTodoCount] = useState(0);
  const [todoTotal, setTodoTotal] = useState(0);
  const [todoDone, setTodoDone] = useState(0);

  // Speech state
  const { token, user } = useAuth();
  const [isRecording, setIsRecording] = useState(false);
  const [isTranscribing, setIsTranscribing] = useState(false);
  const [speechProvider, setSpeechProvider] = useState<SpeechProvider | null>("on-device");
  const [speechApiKey, setSpeechApiKey] = useState<string | undefined>();
  const [ttsEnabled, setTtsEnabled] = useState(false);
  const [verbosity, setVerbosity] = useState(10);
  const [inputFromSpeech, setInputFromSpeech] = useState(false);
  // Video summary toggle for the new task. When on, the agent records
  // a short MP4 demo after the task finishes (vibe-preview pipeline);
  // the task row gets a "▶ Watch demo" button when ready.
  const [videoSummaryEnabled, setVideoSummaryEnabled] = useState(false);
  // codeMode toggle = "yaver code mode". When ON, the task is sent
  // with source="mobile-code" so the agent applies the same prompt
  // wrapping the `yaver code` CLI uses (terminal-style, no markdown
  // headings by default) instead of the mobile dev-server / Hermes
  // wrapping. Same /tasks endpoint, same TaskManager — only the
  // prompt prefix differs. See mobile/src/lib/quic.ts::sendTask doc
  // for the wrapping contract.
  const [codeModeEnabled, setCodeModeEnabled] = useState(false);
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
    if (!token) return;
    getUserSettings(token).then(async (s) => {
      if (s.speechProvider) setSpeechProvider(s.speechProvider);
      if (s.ttsEnabled) setTtsEnabled(s.ttsEnabled);
      if (s.verbosity !== undefined) setVerbosity(s.verbosity);
      // Load speech API key — prefer local Keychain, fall back to cloud
      const localKey = await getLocalSecret(LOCAL_KEYS.speechApiKey);
      if (localKey) setSpeechApiKey(localKey);
      else if (s.speechApiKey) setSpeechApiKey(s.speechApiKey);
    }).catch((e: unknown) => {
      const msg = e instanceof Error ? e.message : String(e);
      console.warn("[speech] getUserSettings failed:", msg);
    });
  }, [token]);

  // Track QUIC connection state and mode
  useEffect(() => {
    const unsub1 = quicClient.on("connectionState", setQuicState);
    const unsub2 = quicClient.on("connectionMode", setConnMode);
    const unsub3 = quicClient.on("reconnectAttempt", setReconnectAttempt);
    return () => { unsub1(); unsub2(); unsub3(); };
  }, []);

  // Fetch agent status when connected
  useEffect(() => {
    if (connectionStatus !== "connected") {
      setAgentStatus(null);
      return;
    }
    const fetchStatus = () => {
      quicClient.getAgentStatus().then(s => { if (s) setAgentStatus(s); });
    };
    fetchStatus();
    const interval = setInterval(fetchStatus, 30000);
    return () => clearInterval(interval);
  }, [connectionStatus]);

  // Fetch available runners + models when connected
  useEffect(() => {
    if (connectionStatus !== "connected") {
      setAvailableRunners([]);
      setAvailableModels([]);
      return;
    }
    quicClient.getRunners().then(r => {
      if (r.length > 0) {
        setAvailableRunners(r);
        const installed = r.filter((runner) => runner.installed);
        const ready = installed.filter((runner) => runner.ready !== false);
        const explicitRunner = activeDevice ? primaryRunnerByDevice[activeDevice.id] : "";
        if (explicitRunner && installed.some((runner) => runner.id === explicitRunner) && selectedRunner !== explicitRunner) {
          setSelectedRunner(explicitRunner);
          return;
        }
        const seededRunner = activeDevice
          ? preferredDefaultRunnerForDevice(activeDevice, user?.email, ready.map((runner) => runner.id).concat(installed.map((runner) => runner.id)))
          : null;
        const preferred =
          ready.find((runner) => runner.id === seededRunner) ||
          installed.find((runner) => runner.id === seededRunner) ||
          ready.find((runner) => runner.isDefault) ||
          ready.find((runner) => runner.id === "claude") ||
          ready.find((runner) => runner.id === "codex") ||
          installed.find((runner) => runner.isDefault) ||
          installed[0];
        if (!selectedRunner && preferred) setSelectedRunner(preferred.id);
      }
    });
  }, [activeDevice, connectionStatus, primaryRunnerByDevice, selectedRunner, user?.email]);

  // Update models when runner selection changes
  useEffect(() => {
    const runner = availableRunners.find(r => r.id === selectedRunner);
    if (runner?.models?.length) {
      setAvailableModels(runner.models);
      const explicitModel = activeDevice ? primaryModelByDevice[activeDevice.id] : "";
      if (explicitModel && runner.models.find((model) => model.id === explicitModel)?.id && selectedModel !== explicitModel) {
        setSelectedModel(explicitModel);
        return;
      }
      if (selectedModel && runner.models.some((model) => model.id === selectedModel)) {
        return;
      }
      const seededModel = activeDevice
        ? preferredDefaultModelForRunner(runner.id, activeDevice, user?.email)
        : null;
      const preferredModel =
        (explicitModel && runner.models.find((model) => model.id === explicitModel)?.id) ||
        (seededModel && runner.models.find((model) => model.id === seededModel)?.id) ||
        runner.models.find(m => m.isDefault)?.id ||
        runner.models[0].id;
      setSelectedModel(preferredModel);
    } else {
      setAvailableModels([]);
      setSelectedModel("");
    }
  }, [activeDevice, availableRunners, primaryModelByDevice, selectedModel, selectedRunner, user?.email]);

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
      // Filter out locally-deleted tasks and internal vibing-cache tasks
      const deletedIds = await getDeletedTaskIds();
      const filtered = list.filter((t) => !deletedIds.has(t.id) && t.source !== "vibing-cache");
      setTasks(filtered);
      // Keep selected task in sync with latest data
      setSelectedTask((prev) => {
        if (!prev) return null;
        return filtered.find((t) => t.id === prev.id) || prev;
      });
    } catch {}
  }, []);

  const hasRunningTask = tasks.some(t => t.status === "running" || t.status === "queued");
  const effectiveFilter = statusFilter;
  const displayTasks = effectiveFilter === "all" ? tasks
    : effectiveFilter === "running" ? tasks.filter(t => t.status === "running" || t.status === "queued")
    : effectiveFilter === "completed" ? tasks.filter(t => t.status === "completed")
    : tasks.filter(t => t.status === "failed" || t.status === "stopped");
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
        // Task finished via SSE — refresh to get final state
        fetchTasks();
      }
    );
    sseAbortRef.current = abort;
    return () => abort();
  }, [selectedTask?.id, selectedTask?.status]);

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
        return { ...t, output: [...t.output, ...newLines] };
      })
    );
    setSelectedTask((prev) => {
      if (!prev || !buffer[prev.id]) return prev;
      return { ...prev, output: [...prev.output, ...buffer[prev.id]] };
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

  useEffect(() => {
    if (!selectedTask || selectedTask.status !== "running") return;
    const interval = setInterval(async () => {
      const idleMs = Date.now() - lastOutputTimeRef.current;
      if (idleMs > 20000) {
        // Agent has been silent for 20s — force refresh task status
        const fresh = await quicClient.getTask(selectedTask.id);
        if (fresh && fresh.status !== "running") {
          setSelectedTask(fresh);
          setTasks(prev => prev.map(t => t.id === fresh.id ? fresh : t));
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

  // TTS: speak the final result when task completes
  const lastSpokenTaskRef = useRef<string | null>(null);
  useEffect(() => {
    if (ttsEnabled && selectedTask?.status === "completed" && selectedTask?.resultText && lastSpokenTaskRef.current !== selectedTask.id) {
      lastSpokenTaskRef.current = selectedTask.id;
      speakText(selectedTask.resultText);
    }
  }, [selectedTask?.status, selectedTask?.resultText, ttsEnabled]);

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
    await fetchTasks();
    setRefreshing(false);
  }, [fetchTasks]);

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

  // Listen for shared images from iOS Share Extension
  useEffect(() => {
    return shareIntentEmitter.on((images) => {
      setAttachedImages(images.slice(0, 5));
      setShowNewTask(true);
    });
  }, []);

  // target: which text field to write into ("task" = new task, "followup" = follow-up input)
  const recordingTargetRef = useRef<"task" | "followup">("task");

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

      const result = await transcribe(uri, { provider: speechProvider, apiKey: speechApiKey });
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
    });
    if (result.canceled) return;

    const newImages: ImageAttachment[] = [];
    for (const asset of result.assets) {
      try {
        const base64 = await FileSystem.readAsStringAsync(asset.uri, {
          encoding: FileSystem.EncodingType.Base64,
        });
        newImages.push({
          base64,
          mimeType: asset.mimeType ?? "image/jpeg",
          filename: asset.fileName ?? `image_${Date.now()}.jpg`,
        });
      } catch {}
    }
    setImages((prev) => [...prev, ...newImages].slice(0, 5));
  };

  // ── TTS ────────────────────────────────────────────────────────────

  const speakText = (text: string) => {
    if (!ttsEnabled) return;
    try {
      const Speech = require("expo-speech");
      // Strip markdown for cleaner speech
      const plain = text.replace(/[#*`_~\[\]()>|\\-]/g, "").replace(/\n+/g, ". ");
      Speech.speak(plain, { language: "en" });
    } catch {}
  };

  const handleCreateTask = async () => {
    if (!newTaskText.trim() && attachedImages.length === 0) return;
    // Stop any active recording before sending
    if (isRecording) {
      try { await stopRecordingAndTranscribe(); } catch {}
    }
    Keyboard.dismiss();
    setIsSubmitting(true);
    try {
      const speechCtx = (speechProvider || verbosity < 10) ? {
        inputFromSpeech,
        sttProvider: speechProvider ?? undefined,
        ttsEnabled,
        ttsProvider: "device",
        verbosity,
      } : undefined;
      const title = initialTitle || newTaskText.trim();
      const task = await quicClient.sendTask(
        title, "",
        selectedRunner === "custom" ? undefined : (selectedModel || undefined),
        selectedRunner === "custom" ? "custom" : (selectedRunner || undefined),
        selectedRunner === "custom" ? customCommand.trim() || undefined : undefined,
        speechCtx,
        attachedImages.length > 0 ? attachedImages : undefined,
        projectDir || undefined,
        selectedRunner === "opencode" && selectedOpenCodeMode ? selectedOpenCodeMode : undefined,
        videoSummaryEnabled ? { enabled: true } : undefined,
        codeModeEnabled,
      );
      setNewTaskText("");
      setAttachedImages([]);
      setInputFromSpeech(false);
      // Add task to list immediately
      setTasks((prev) => [task, ...prev]);
      // Store task to open after modal closes (onDismiss will pick it up)
      pendingOpenTaskRef.current = task;
      setShowNewTask(false);
      // Refresh from server in background
      fetchTasks();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      Alert.alert("Task failed", msg);
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleNewTaskModalDismiss = () => {
    if (pendingOpenTaskRef.current) {
      const task = pendingOpenTaskRef.current;
      pendingOpenTaskRef.current = null;
      setSelectedTask(task);
    }
  };

  // Android fallback: onDismiss is iOS-only, so use effect to detect modal close
  useEffect(() => {
    if (!showNewTask && pendingOpenTaskRef.current && Platform.OS === "android") {
      const timer = setTimeout(handleNewTaskModalDismiss, 100);
      return () => clearTimeout(timer);
    }
  }, [showNewTask]);

  const handleStopTask = async (taskId: string) => {
    try {
      await quicClient.stopTask(taskId);
      // ACK received — immediately update UI
      setTasks(prev => prev.map(t => t.id === taskId ? { ...t, status: "completed" as TaskStatus } : t));
      setSelectedTask(prev => prev?.id === taskId ? { ...prev, status: "completed" as TaskStatus } : prev);
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
      setTasks(prev => prev.map(t => t.id === taskId ? { ...t, status: "completed" as TaskStatus } : t));
      setSelectedTask(prev => prev?.id === taskId ? { ...prev, status: "completed" as TaskStatus } : prev);
      await fetchTasks();
    } catch {
      Alert.alert("Stop Failed", "Could not reach the agent. The task may still be running.");
    }
  };

  const handleFollowUp = async () => {
    if (!selectedTask || (!followUpText.trim() && followUpImages.length === 0)) return;
    // Stop any active recording before sending
    if (isRecording) {
      try { await stopRecordingAndTranscribe(); } catch {}
    }
    Keyboard.dismiss();
    setIsSendingFollowUp(true);
    try {
      if (selectedTask.isAdopted) {
        // For adopted tmux sessions, send input directly via tmux send-keys
        await quicClient.sendTmuxInput(selectedTask.id, followUpText.trim());
      } else {
        // For regular tasks, stop then resume with new input
        const isTaskRunning = selectedTask.status === "running" || selectedTask.status === "queued";
        if (isTaskRunning) {
          await quicClient.stopTask(selectedTask.id);
          // Wait briefly for task to fully stop
          await new Promise((r) => setTimeout(r, 500));
        }
        await quicClient.continueTask(selectedTask.id, followUpText.trim(), followUpImages.length > 0 ? followUpImages : undefined);
      }
      setFollowUpText("");
      setFollowUpImages([]);
      await fetchTasks();
    } catch {
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

  const handleStopAll = async () => {
    try { await quicClient.stopAllTasks(); await fetchTasks(); } catch {}
  };

  const handleDeleteAll = async () => {
    try { await quicClient.deleteAllTasks(); setTasks([]); await fetchTasks(); } catch {}
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

  // Summary — show morning digest
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

  const handleAdoptTmuxSession = async (sessionName: string) => {
    setIsAdopting(sessionName);
    try {
      const result = await quicClient.adoptTmuxSession(sessionName);
      // Refresh both lists
      const [sessions] = await Promise.all([quicClient.listTmuxSessions(), fetchTasks()]);
      setTmuxSessions(sessions);
      // Close modal and open the new task
      setShowTmuxSessions(false);
      const updatedTasks = await quicClient.listTasks();
      const newTask = updatedTasks.find(t => t.id === result.taskId);
      if (newTask) setSelectedTask(newTask);
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

  const handleReconnect = async (device: typeof devices[0]) => {
    setIsReconnecting(true);
    setReconnectError(null);
    try {
      if (!device.isGuest && unreachableSet.has(device.id) && device.online) {
        const recovery = await recoverDeviceAuth(device);
        if (recovery && !recovery.ok && recovery.error) {
          console.log(`[tasks] auth recovery before reconnect failed for ${device.name}: ${recovery.error}`);
        }
      }
      await selectDevice(device);
      // Give it a moment to establish connection
      await new Promise(resolve => setTimeout(resolve, 3000));
      if (quicClient.connectionState !== "connected") {
        setReconnectError(`Could not reach ${device.name}. Make sure yaver is running.`);
      }
    } catch (e: any) {
      setReconnectError(e?.message || `Could not reach ${device.name}`);
    } finally {
      setIsReconnecting(false);
    }
  };

  const effectiveState: ConnectionState =
    connectionStatus === "connected" ? quicState :
    // Show yellow "Reconnecting" for error state (active retries)
    connectionStatus === "error" ? "connecting" :
    connectionStatus;
  const banner = BANNER_CONFIG[effectiveState];
  const isEffectivelyConnected = effectiveState === "connected";
  const modeLabel = connMode === "relay" ? " via Relay" : connMode === "direct" ? " Direct" : "";

  useEffect(() => {
    if (isEffectivelyConnected || devices.length === 0) return;
    let cancelled = false;
    const targets = devices.filter((device) => !device.isGuest && (device.online || unreachableSet.has(device.id)));
    if (targets.length === 0) return;

    const run = async () => {
      const updates = await Promise.all(
        targets.map(async (device) => ({ id: device.id, result: await probeDeviceInfo(device) }))
      );
      if (cancelled) return;
      setDeviceProbeMap((prev) => {
        const next = { ...prev };
        for (const update of updates) {
          if (update.result) next[update.id] = update.result;
        }
        return next;
      });
    };

    void run();
    const iv = setInterval(run, 8000);
    return () => {
      cancelled = true;
      clearInterval(iv);
    };
  }, [devices, isEffectivelyConnected, unreachableSet]);

  // Fetch agent info (project, todo stats) every 5s
  useEffect(() => {
    if (!isEffectivelyConnected) return;
    const fetchInfo = async () => {
      try {
        const info = await quicClient.agentInfo();
        setProjectName(info.project?.name ?? "");
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
  const showRetryButton = connectionStatus === "disconnected" && activeDevice && !userDisconnected;
  // Show the attempt counter while we're actively retrying (attempt > 0 and
  // not yet connected). Clamp to max so the display never exceeds N/max.
  const showReconnectProgress =
    reconnectAttempt > 0 && !isEffectivelyConnected && !!activeDevice;
  const displayedAttempt = Math.min(reconnectAttempt, quicClient.maxReconnectAttempts);

  const chatMessages = selectedTask ? buildChatMessages(selectedTask) : [];
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
  const runnerBannerState = useMemo(
    () => deriveRunnerBannerState(availableRunners, agentStatus),
    [availableRunners, agentStatus]
  );

  return (
    <SafeAreaView style={[s.safeArea, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <View style={s.container}>
        {/* Connection banner */}
        <Pressable
          style={[s.banner, { backgroundColor: banner.bg, borderBottomColor: banner.border, flexDirection: "column", alignItems: "flex-start", paddingVertical: 12 }]}
          onPress={() => {
            if (!isEffectivelyConnected && activeDevice) {
              selectDevice(activeDevice);
            }
          }}
        >
          <View style={{ flexDirection: "row", alignItems: "center", flexWrap: "wrap" }}>
            <View style={[s.dot, { backgroundColor: banner.dot }]} />
            <Text style={[s.bannerText, { color: banner.text, flexShrink: 1 }]} numberOfLines={1}>
              {/* Never surface raw transport errors here — they read
                  as "the product is broken" even when a single retry
                  would fix it. The banner stays at "Disconnected"
                  level and the unified Not-connected list below
                  shows the per-device options. */}
              {banner.label}
              {isEffectivelyConnected ? modeLabel : ""}
              {activeDevice ? ` \u00b7 ${activeDevice.name}` : ""}
            </Text>
            {showReconnectProgress && (
              <Text style={{ color: banner.text, fontSize: 11, marginLeft: 6, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" }}>
                {displayedAttempt}/{quicClient.maxReconnectAttempts}
              </Text>
            )}
            {showReconnectProgress && (
              <Pressable
                style={{ marginLeft: 8, paddingHorizontal: 10, paddingVertical: 3, borderRadius: 6, backgroundColor: "#ef444433" }}
                onPress={() => { stopReconnectAndBounce().catch(() => {}); }}
              >
                <Text style={{ fontSize: 12, color: "#f87171", fontWeight: "600" }}>Stop</Text>
              </Pressable>
            )}
            {!showReconnectProgress && showRetryButton && (
              <Pressable
                style={{ marginLeft: 8, paddingHorizontal: 10, paddingVertical: 3, borderRadius: 6, backgroundColor: "#6366f133" }}
                onPress={() => activeDevice && selectDevice(activeDevice)}
              >
                <Text style={{ fontSize: 12, color: "#818cf8", fontWeight: "600" }}>Retry</Text>
              </Pressable>
            )}
            {(showReconnectProgress || showRetryButton) && (
              <Pressable
                style={{ marginLeft: 8, paddingHorizontal: 10, paddingVertical: 3, borderRadius: 6, backgroundColor: "#64748b33" }}
                onPress={() => setShowLogs(true)}
              >
                <Text style={{ fontSize: 12, color: "#94a3b8", fontWeight: "600" }}>View Logs</Text>
              </Pressable>
            )}
          </View>
          {isEffectivelyConnected && agentAuthExpired && (
            <View style={{ flexDirection: "row", alignItems: "center", marginTop: 4, marginLeft: 18 }}>
              <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: "#f59e0b" }} />
              <Text style={{ color: "#fbbf24", fontSize: 11, marginLeft: 6 }}>
                Agent session expired — open Devices and tap Recover Auth
              </Text>
            </View>
          )}
          {isEffectivelyConnected && !agentAuthExpired && (
            <View style={{ flexDirection: "row", alignItems: "center", marginTop: 4, marginLeft: 18 }}>
              {runnerBannerState && (
                <>
                  <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: runnerBannerState.tone }} />
                  <Text style={{ color: runnerBannerState.tone, fontSize: 11, marginLeft: 6 }}>
                    {runnerBannerState.text}
                  </Text>
                </>
              )}
              {pingRtt !== null && (
                <Pressable onPress={handlePing} style={{ marginLeft: 8, paddingHorizontal: 6, paddingVertical: 1, borderRadius: 4, backgroundColor: (pingRtt === -1 ? "#ef4444" : pingRtt < 100 ? "#22c55e" : pingRtt < 300 ? "#eab308" : "#ef4444") + "18" }}>
                  <Text style={{ color: pingRtt === -1 ? "#f87171" : pingRtt < 100 ? "#4ade80" : pingRtt < 300 ? "#facc15" : "#f87171", fontSize: 11, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" }}>
                    {isPinging ? "..." : pingRtt === -1 ? "no response" : `${pingRtt}ms`}
                  </Text>
                </Pressable>
              )}
              {pingRtt === null && (
                <Pressable onPress={handlePing} style={{ marginLeft: 8 }}>
                  <Text style={{ color: banner.text, fontSize: 11 }}>{isPinging ? "pinging..." : "ping"}</Text>
                </Pressable>
              )}
            </View>
          )}
          {isEffectivelyConnected &&
            runnerBannerState &&
            runnerBannerState.kind !== "ok" &&
            runnerBannerState.kind !== "authNeeded" && (
              <View style={{ flexDirection: "row", alignItems: "center", marginTop: 2, marginLeft: 18 }}>
                {(availableRunners.length > 0 || agentStatus) && (
                  <Pressable
                    onPress={handleRestartRunner}
                    disabled={isRestartingRunner}
                    style={{ marginLeft: 8, paddingHorizontal: 8, paddingVertical: 2, borderRadius: 4, backgroundColor: "#6366f122" }}
                  >
                    <Text style={{ color: "#818cf8", fontSize: 11 }}>
                      {isRestartingRunner ? "Restarting..." : "Restart"}
                    </Text>
                  </Pressable>
                )}
              </View>
            )}
        </Pressable>

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
        {isEffectivelyConnected && (
          <View style={[s.actionBar, { borderBottomColor: c.border }]}>
            <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 6, paddingHorizontal: 2 }}>
              {([
                { key: "running" as const, label: "Running", color: "#6366f1", count: tasks.filter(t => t.status === "running" || t.status === "queued").length },
                { key: "completed" as const, label: "Completed", color: "#22c55e", count: tasks.filter(t => t.status === "completed").length },
                { key: "failed" as const, label: "Failed", color: "#ef4444", count: tasks.filter(t => t.status === "failed" || t.status === "stopped").length },
                { key: "all" as const, label: "All", color: c.textMuted, count: tasks.length },
              ] as const).map(chip => (
                <Pressable
                  key={chip.key}
                  onPress={() => setStatusFilter(chip.key)}
                  style={[s.actionButton, {
                    backgroundColor: (effectiveFilter === chip.key) ? `${chip.color}22` : c.bgCardElevated,
                    borderWidth: 1,
                    borderColor: (effectiveFilter === chip.key) ? `${chip.color}66` : "transparent",
                  }]}
                >
                  <Text style={[s.actionButtonText, { color: (effectiveFilter === chip.key) ? chip.color : c.textMuted }]}>
                    {chip.label}{chip.count > 0 ? ` ${chip.count}` : ""}
                  </Text>
                </Pressable>
              ))}
              {tasks.some(t => t.status === "running") && (
                <Pressable style={[s.actionButton, { backgroundColor: "#ef444418" }]} onPress={handleStopAll}>
                  <Text style={[s.actionButtonText, { color: "#ef4444" }]}>Stop All</Text>
                </Pressable>
              )}
              {tasks.some(t => t.status !== "running" && t.status !== "queued") && (
                <Pressable style={[s.actionButton, { backgroundColor: c.bgCardElevated }]} onPress={handleDeleteAll}>
                  <Text style={[s.actionButtonText, { color: c.textMuted }]}>Clear</Text>
                </Pressable>
              )}
              <Pressable style={[s.actionButton, { backgroundColor: "#8b5cf618" }]} onPress={handleOpenTmuxSessions}>
                <Text style={[s.actionButtonText, { color: "#8b5cf6" }]}>Tmux</Text>
              </Pressable>
              <Pressable style={[s.actionButton, { backgroundColor: "#64748b22" }]} onPress={() => setShowLogs(true)}>
                <Text style={[s.actionButtonText, { color: "#94a3b8" }]}>Logs</Text>
              </Pressable>
              <Pressable style={[s.actionButton, { backgroundColor: "#f9731618" }]} onPress={handleShipIt}>
                <Text style={[s.actionButtonText, { color: "#f97316" }]}>Ship It</Text>
              </Pressable>
              <Pressable style={[s.actionButton, { backgroundColor: "#06b6d418" }]} onPress={handleShowSummary}>
                <Text style={[s.actionButtonText, { color: "#06b6d4" }]}>Summary</Text>
              </Pressable>
            </ScrollView>
          </View>
        )}

        {/* Task list */}
        <FlatList
          data={displayTasks}
          keyExtractor={(item) => item.id}
          alwaysBounceVertical
          contentContainerStyle={[s.listContent, displayTasks.length === 0 && s.listContentEmpty]}
          refreshControl={
            <RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={c.accent} colors={[c.accent]} progressBackgroundColor={c.bgCard} />
          }
          ListEmptyComponent={
            isEffectivelyConnected ? (
              <View style={s.emptyList}>
                <Text style={[s.emptyIcon, { color: c.textMuted }]}>{"[ ]"}</Text>
                <Text style={[s.emptyTitle, { color: c.textPrimary }]}>All Clear</Text>
                <Text style={[s.emptySubtitle, { color: c.textSecondary }]}>
                  No tasks yet. Tap the + button to create your first task.
                </Text>
              </View>
            ) : isLoadingDevices ? (
              <View style={s.emptyList}>
                <ActivityIndicator size="large" color={c.accent} />
                <Text style={[s.emptySubtitle, { color: c.textSecondary, marginTop: 16 }]}>
                  Looking for devices...
                </Text>
              </View>
            ) : devices.length === 0 ? (
              <View style={s.emptyList}>
                <Text style={[s.discoverIcon, { color: c.textMuted }]}>{"\u2318"}</Text>
                <Text style={[s.emptyTitle, { color: c.textPrimary }]}>Start Coding</Text>
                <Text style={[s.emptySubtitle, { color: c.textSecondary, marginTop: 8, marginBottom: 20 }]}>
                  Build from this phone, or pair a dev machine.
                </Text>

                <Pressable
                  style={[s.discoverPrimaryBtn, { backgroundColor: c.accent }]}
                  onPress={() => taskRouter.navigate("/phone-projects" as any)}
                >
                  <Text style={s.discoverBtnText}>Open Mobile Sandbox</Text>
                </Pressable>
                <Text style={[s.discoverHelper, { color: c.textMuted }]}>
                  Local SQLite-backed project. No machine required. Git is optional; if used, Yaver expects a monorepo workspace.
                </Text>

                <View style={[s.discoverDivider, { backgroundColor: c.border }]} />
                <Text style={[s.discoverSectionLabel, { color: c.textMuted }]}>Or pair your computer</Text>

                <View style={s.discoverSteps}>
                  <View style={s.discoverStep}>
                    <View style={[s.discoverStepDot, { backgroundColor: c.accent }]}>
                      <Text style={s.discoverStepNum}>1</Text>
                    </View>
                    <View style={s.discoverStepContent}>
                      <Text style={[s.discoverStepTitle, { color: c.textPrimary }]}>Install</Text>
                      <Text style={[s.discoverStepDesc, { color: c.textMuted }]}>brew install kivanccakmak/yaver/yaver</Text>
                    </View>
                  </View>
                  <View style={s.discoverStep}>
                    <View style={[s.discoverStepDot, { backgroundColor: c.accent }]}>
                      <Text style={s.discoverStepNum}>2</Text>
                    </View>
                    <View style={s.discoverStepContent}>
                      <Text style={[s.discoverStepTitle, { color: c.textPrimary }]}>Sign in &amp; start</Text>
                      <Text style={[s.discoverStepDesc, { color: c.textMuted }]}>yaver auth</Text>
                    </View>
                  </View>
                </View>

                <Pressable
                  style={[s.discoverSecondaryBtn, { borderColor: c.border, opacity: isRefreshingDevices ? 0.6 : 1 }]}
                  onPress={async () => {
                    if (isRefreshingDevices) return;
                    setIsRefreshingDevices(true);
                    try { await refreshDevices(); } finally { setIsRefreshingDevices(false); }
                  }}
                  disabled={isRefreshingDevices}
                >
                  {isRefreshingDevices ? (
                    <ActivityIndicator size="small" color={c.textPrimary} />
                  ) : (
                    <Text style={[s.discoverBtnText, { color: c.textPrimary }]}>Refresh Devices</Text>
                  )}
                </Pressable>
              </View>
            ) : devices.length === 1 && connectionStatus === "connecting" ? (
              // Single-device fast path: show a calm spinner instead
              // of the device picker we'd otherwise render. Still no
              // "Failed" surface — if the connect dies we fall
              // through to the unified Not-connected list below.
              <View style={s.emptyList}>
                <ActivityIndicator size="large" color={c.accent} />
                <Text style={[s.emptyTitle, { color: c.textPrimary, marginTop: 16 }]}>Connecting...</Text>
                <Text style={[s.emptySubtitle, { color: c.textSecondary }]}>
                  {devices[0].name}
                </Text>
              </View>
            ) : devices.length >= 1 ? (
              // Unified "Not connected" view. Used in three cases:
              //   (a) user disconnected explicitly (was: "Disconnected /
              //       Your last session" card with the first device only)
              //   (b) connect attempt failed (was: red "Connection
              //       Failed" panel with raw error message)
              //   (c) plain "no active device" with multiple options
              // We never surface raw errors here — the user said the
              // product reads as "failing/buggy" when we do. Instead
              // every known device gets a row with an explicit status
              // pill (online / stale / offline) and tap-to-retry.
              <View style={s.emptyList}>
                <Text style={[s.emptyIcon, { color: c.textMuted }]}>{"\u23FB"}</Text>
                <Text style={[s.emptyTitle, { color: c.textPrimary }]}>Not connected</Text>
                <Text style={[s.emptySubtitle, { color: c.textSecondary, marginBottom: 16 }]}>
                  {devices.length === 1
                    ? "Tap the device below to connect."
                    : `Pick one of your ${devices.length} devices.`}
                </Text>
                {devices.map((d) => {
                  const unreachable = unreachableSet.has(d.id);
                  const probe = deviceProbeMap[d.id];
                  const hasReachableProbe = probe?.reachable === true;
                  const needsAuth = d.needsAuth === true || probe?.needsAuth === true;
                  const statusText =
                    needsAuth && hasReachableProbe
                      ? "Needs Auth"
                      : d.online && !unreachable
                        ? "Online"
                        : hasReachableProbe
                          ? "Reachable"
                          : unreachable && d.online
                            ? "Stale"
                            : "Offline";
                  const statusColor =
                    needsAuth && hasReachableProbe
                      ? "#f59e0b"
                      : d.online && !unreachable
                        ? "#22c55e"
                        : hasReachableProbe
                          ? "#38bdf8"
                          : unreachable && d.online
                            ? "#eab308"
                            : "#a1a1aa";
                  const isRetrying = isReconnecting && activeDevice?.id === d.id;
                  return (
                    <Pressable
                      key={d.id}
                      style={[s.devicePickerCard, {
                        backgroundColor: c.bgCard,
                        borderColor: unreachable && d.online ? "#eab30866" : c.border,
                        // Wider cards per user feedback on the
                        // disconnected screen — the old single-line
                        // "last session" card didn't give enough room
                        // for status + meta + action affordance.
                        paddingVertical: 14,
                      }]}
                      onPress={() => !isRetrying && handleReconnect(d)}
                      disabled={isRetrying}
                    >
                      <View style={s.devicePickerRow}>
                        <View style={{ flex: 1 }}>
                          <Text style={[s.devicePickerName, { color: c.textPrimary }]}>{d.name}</Text>
                          <Text style={[s.devicePickerMeta, { color: c.textMuted }]}>
                            {d.os} · {d.host}
                            {d.deviceClass === "edge-mobile" ? " · mobile worker" : ""}
                          </Text>
                          {needsAuth && hasReachableProbe ? (
                            <Text style={[s.devicePickerMeta, { color: "#f59e0b", marginTop: 2 }]}>
                              Machine is up, but Yaver auth expired. Tap to recover from this phone.
                            </Text>
                          ) : hasReachableProbe ? (
                            <Text style={[s.devicePickerMeta, { color: "#38bdf8", marginTop: 2 }]}>
                              Machine answered an unauthenticated probe. Tap to attach again.
                            </Text>
                          ) : unreachable && d.online ? (
                            <Text style={[s.devicePickerMeta, { color: "#eab308", marginTop: 2 }]}>
                              Last attach failed. We will keep probing before calling it offline.
                            </Text>
                          ) : null}
                          {!hasReachableProbe && !d.online && (
                            <Text style={[s.devicePickerMeta, { color: c.textMuted, marginTop: 2 }]}>
                              No recent heartbeat. Power on and run yaver serve.
                            </Text>
                          )}
                        </View>
                        <View style={{ alignItems: "flex-end" }}>
                          <View style={[s.reconnectDeviceStatus, { backgroundColor: statusColor + "22" }]}>
                            <View style={[s.reconnectStatusDot, { backgroundColor: statusColor }]} />
                            <Text style={[s.reconnectStatusText, { color: statusColor }]}>{statusText}</Text>
                          </View>
                          {isRetrying ? (
                            <ActivityIndicator size="small" color={c.accent} style={{ marginTop: 8 }} />
                          ) : null}
                        </View>
                      </View>
                    </Pressable>
                  );
                })}
              </View>
            ) : null
          }
          renderItem={({ item }) => (
            <TaskCard
              item={item}
              onPress={() => setSelectedTask(item)}
              onDelete={() => handleDeleteTask(item.id)}
            />
          )}
        />

        {/* FAB */}
        {isEffectivelyConnected && (
          <Pressable
            style={({ pressed }) => [s.fab, pressed && s.fabPressed]}
            onPress={() => {
              // Defensive reset — guarantees the modal opens cleanly even if
              // a previous cancel/backdrop-dismiss left stale state around.
              setNewTaskText("");
              setAttachedImages([]);
              setInputFromSpeech(false);
              pendingOpenTaskRef.current = null;
              setShowNewTask(true);
            }}
          >
            <Text style={s.fabText}>+</Text>
          </Pressable>
        )}

        {/* Video summary player — opens when a task's "▶ Watch demo"
            chip is tapped. Plays the clip at /vibing/preview/clip/<id>
            via expo-av's Video. The clip URL helper attaches the auth
            headers under the hood (expo-av accepts headers through the
            source object). */}
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
              <Video
                key={videoSummaryClipId}
                source={{ uri: clipUrl(videoSummaryClipId)!, headers: quicClient.getAuthHeaders() } as any}
                style={{ width: "100%", height: "70%" }}
                useNativeControls
                resizeMode={ResizeMode.CONTAIN}
                shouldPlay
                onPlaybackStatusUpdate={(st: any) => {
                  if (st?.didJustFinish) setVideoSummaryClipId(null);
                }}
              />
            ) : (
              <Text style={{ color: "#888" }}>Loading…</Text>
            )}
          </View>
        </Modal>

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
          }}
        >
          <KeyboardAvoidingView style={s.modalOverlay} behavior={Platform.OS === "ios" ? "padding" : "height"}>
            <Pressable style={s.modalDismiss} onPress={() => { Keyboard.dismiss(); setShowNewTask(false); setNewTaskText(""); setAttachedImages([]); setInputFromSpeech(false); }} />
            <View style={[s.modalContent, { backgroundColor: c.bgCard }]}>
              <View style={s.modalHeader}>
                <Text style={[s.modalTitle, { color: c.textPrimary }]}>New Task</Text>
                <Pressable
                  style={[s.agentBadge, { backgroundColor: c.bgCardElevated, borderColor: c.border }]}
                  onPress={() => setShowAgentPicker(true)}
                >
                  <Text style={[s.agentBadgeText, { color: c.textSecondary }]}>
                    {(() => {
                      const runner = availableRunners.find(r => r.id === selectedRunner);
                      const model = availableModels.find(m => m.id === selectedModel);
                      const runnerLabel = selectedRunner === "custom"
                        ? "Custom"
                        : (runner?.name || (selectedRunner ? selectedRunner : "Claude"));
                      const modelLabel = model?.name || selectedModel || "";
                      return modelLabel ? `${runnerLabel} · ${modelLabel}` : runnerLabel;
                    })()}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 10, marginLeft: 4 }}>▾</Text>
                </Pressable>
              </View>
              <TextInput
                style={[s.input, s.inputMultiline, { backgroundColor: c.bg, borderColor: c.border, color: c.textPrimary }]}
                placeholder={`What would you like ${selectedRunner === "codex" ? "Codex" : selectedRunner === "aider" ? "Aider" : "Claude"} to do?`}
                placeholderTextColor={c.textMuted}
                value={newTaskText}
                onChangeText={(t) => { setNewTaskText(t); setInputFromSpeech(false); }}
                multiline numberOfLines={4} textAlignVertical="top" autoFocus
              />
              {isTranscribing && (
                <View style={{ flexDirection: "row", alignItems: "center", paddingVertical: 6 }}>
                  <ActivityIndicator size="small" color={c.accent} />
                  <Text style={{ color: c.textMuted, fontSize: 12, marginLeft: 8 }}>Transcribing...</Text>
                </View>
              )}
              {attachedImages.length > 0 && (
                <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{ marginBottom: 8 }}>
                  {attachedImages.map((img, i) => (
                    <View key={i} style={{ marginRight: 8, position: "relative" }}>
                      <Image source={{ uri: `data:${img.mimeType};base64,${img.base64}` }} style={{ width: 60, height: 60, borderRadius: 8 }} />
                      <Pressable onPress={() => setAttachedImages((prev) => prev.filter((_, idx) => idx !== i))} style={{ position: "absolute", top: -6, right: -6, width: 20, height: 20, borderRadius: 10, backgroundColor: "#ef4444", alignItems: "center", justifyContent: "center" }}>
                        <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>×</Text>
                      </Pressable>
                    </View>
                  ))}
                </ScrollView>
              )}
              <Pressable
                onPress={() => setVideoSummaryEnabled((v) => !v)}
                style={({ pressed }) => [
                  {
                    flexDirection: "row", alignItems: "center", gap: 8,
                    paddingVertical: 8, paddingHorizontal: 4,
                    opacity: pressed ? 0.6 : 1,
                  },
                ]}
              >
                <View
                  style={{
                    width: 18, height: 18, borderRadius: 4,
                    borderWidth: 1.5, borderColor: videoSummaryEnabled ? c.accent : c.border,
                    backgroundColor: videoSummaryEnabled ? c.accent : "transparent",
                    alignItems: "center", justifyContent: "center",
                  }}
                >
                  {videoSummaryEnabled ? <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>✓</Text> : null}
                </View>
                <Text style={{ color: c.textSecondary, fontSize: 13 }}>
                  🎬 Record demo video when this task finishes
                </Text>
              </Pressable>
              {/*
                yaver code mode toggle — flips the agent's prompt
                wrapping. Both modes use the same /tasks endpoint and
                same TaskManager; they differ only in the prompt
                prefix the agent injects:
                  • OFF (default): mobile-style. Agent layers in the
                    Hermes / Metro / dev-server hot-reload context
                    so an Expo project can call /dev/start. Markdown
                    answers, bullet framing, the works. (yaver go)
                  • ON: terminal-style. Agent skips the dev-server
                    prefix and instead injects the same wrapper
                    capability context the `yaver code` CLI uses —
                    plain terminal output, no canned formatting.
                Pick ON when you want the runner to behave like a CLI
                coding session; OFF for "build me an Expo screen".
              */}
              <Pressable
                onPress={() => setCodeModeEnabled((v) => !v)}
                style={({ pressed }) => [
                  {
                    flexDirection: "row", alignItems: "center", gap: 8,
                    paddingVertical: 8, paddingHorizontal: 4,
                    opacity: pressed ? 0.6 : 1,
                  },
                ]}
              >
                <View
                  style={{
                    width: 18, height: 18, borderRadius: 4,
                    borderWidth: 1.5, borderColor: codeModeEnabled ? c.accent : c.border,
                    backgroundColor: codeModeEnabled ? c.accent : "transparent",
                    alignItems: "center", justifyContent: "center",
                  }}
                >
                  {codeModeEnabled ? <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>✓</Text> : null}
                </View>
                <Text style={{ color: c.textSecondary, fontSize: 13 }}>
                  ⌨️ yaver code mode (terminal-style wrapping)
                </Text>
              </Pressable>
              <View style={s.modalButtons}>
                <Pressable style={[s.cancelButton, { backgroundColor: c.bgCardElevated }]} onPress={() => { Keyboard.dismiss(); setShowNewTask(false); setNewTaskText(""); setAttachedImages([]); setInputFromSpeech(false); }}>
                  <Text style={[s.cancelButtonText, { color: c.textSecondary }]}>Cancel</Text>
                </Pressable>
                <View style={{ flex: 1, flexDirection: "row", alignItems: "center", gap: 8 }}>
                  <Pressable
                    style={({ pressed }) => [
                      { width: 44, height: 44, borderRadius: 22, backgroundColor: c.bgCardElevated, alignItems: "center", justifyContent: "center", borderWidth: 1, borderColor: c.border },
                      pressed && { opacity: 0.7 },
                    ]}
                    onPress={() => handlePickImage("task")}
                    disabled={attachedImages.length >= 5}
                  >
                    <Text style={{ fontSize: 20, color: c.textSecondary }}>📷</Text>
                  </Pressable>
                  <Pressable
                    style={({ pressed }) => [
                      {
                        width: 44, height: 44, borderRadius: 22,
                        backgroundColor: isRecording ? "#ef4444" : c.bgCardElevated,
                        alignItems: "center", justifyContent: "center",
                        borderWidth: 1, borderColor: isRecording ? "#ef4444" : c.border,
                        opacity: 1,
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
                        startRecording();
                      }
                    }}
                    disabled={isTranscribing}
                  >
                    <Text style={{ fontSize: 20, color: isRecording ? "#fff" : c.textSecondary }}>
                      {isRecording ? "\u25A0" : "\uD83C\uDFA4"}
                    </Text>
                  </Pressable>
                  <Pressable
                    style={[s.submitButton, { backgroundColor: c.accent }, ((!newTaskText.trim() && attachedImages.length === 0) || isSubmitting || isTranscribing || !isEffectivelyConnected) && s.submitButtonDisabled]}
                    onPress={handleCreateTask}
                    disabled={(!newTaskText.trim() && attachedImages.length === 0) || isSubmitting || isTranscribing || !isEffectivelyConnected}
                  >
                    <Text style={s.submitButtonText}>{isSubmitting ? "Sending..." : "Send"}</Text>
                  </Pressable>
                </View>
              </View>
            </View>
          </KeyboardAvoidingView>
        </Modal>


        {/* ── Agent / Model Picker Modal ─────────────────────────────── */}
        <Modal visible={showAgentPicker} animationType="slide" transparent onRequestClose={() => setShowAgentPicker(false)}>
          <Pressable style={{ flex: 1 }} onPress={() => setShowAgentPicker(false)} />
          <View style={[s.agentPickerSheet, { backgroundColor: c.bgCard }]}>
            <View style={[s.agentPickerHeader, { borderBottomColor: c.border }]}>
              <Text style={[s.agentPickerTitle, { color: c.textPrimary }]}>Agent & Model</Text>
              <Pressable onPress={() => setShowAgentPicker(false)}>
                <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Done</Text>
              </Pressable>
            </View>
            {availableRunners.length === 0 && availableModels.length === 0 && (
              <Text style={{ color: c.textMuted, fontSize: 13, paddingHorizontal: 16, paddingVertical: 20, textAlign: "center" }}>
                Loading agents… if this persists, make sure your dev machine has a coding agent installed (claude-code, codex, aider, opencode).
              </Text>
            )}
            {availableRunners.length > 0 && (() => {
              // Only surface runners the connected dev machine actually has
              // on PATH. Keep the currently-selected runner even if it's not
              // installed (so the chip doesn't silently disappear).
              const installed = availableRunners.filter(
                (r) => r.installed || r.id === selectedRunner,
              );
              if (installed.length === 0) {
                return (
                  <>
                    <Text style={[s.agentPickerSection, { color: c.textMuted }]}>AGENT</Text>
                    <Text style={{ color: c.textMuted, fontSize: 12, paddingHorizontal: 16, paddingBottom: 12 }}>
                      No coding agents installed on this dev machine. Install one with{"\n"}
                      <Text style={{ fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", color: c.textSecondary }}>
                        yaver install claude | codex | aider | opencode
                      </Text>
                    </Text>
                  </>
                );
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
                        onPress={() => setSelectedRunner(r.id)}
                      >
                        <Text style={[s.modelChipText, { color: selectedRunner === r.id ? "#f59e0b" : c.textMuted }]}>
                          {r.name}
                        </Text>
                      </Pressable>
                    ))}
                    <Pressable
                      style={[
                        s.modelChip,
                        { borderColor: selectedRunner === "custom" ? "#f59e0b" : c.border },
                        selectedRunner === "custom" && { backgroundColor: "#f59e0b20" },
                      ]}
                      onPress={() => setSelectedRunner("custom")}
                    >
                      <Text style={[s.modelChipText, { color: selectedRunner === "custom" ? "#f59e0b" : c.textMuted }]}>
                        Custom
                      </Text>
                    </Pressable>
                  </View>
                  {selectedRunner === "custom" && (
                    <TextInput
                      style={[s.input, { backgroundColor: c.bg, borderColor: c.border, color: c.textPrimary, marginHorizontal: 16, marginBottom: 8, fontSize: 13, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" }]}
                      placeholder="Command, e.g. my-tool --auto {prompt}"
                      placeholderTextColor={c.textMuted}
                      value={customCommand}
                      onChangeText={setCustomCommand}
                      autoCapitalize="none"
                      autoCorrect={false}
                    />
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
                      onPress={() => setSelectedModel(m.id)}
                    >
                      <Text style={[s.modelChipText, { color: selectedModel === m.id ? c.accent : c.textMuted }]}>
                        {m.name}
                      </Text>
                    </Pressable>
                  ))}
                </View>
              </>
            )}
            {/* OpenCode-only: Build vs Plan agent. Maps to
                `--agent <mode>` on `opencode run`. Empty = use the
                machine's defaultAgent from opencode.json. Custom
                agents the user has defined are reachable via the
                Tools view; this chip rail covers the two builtin
                agents most users need on the chat surface. */}
            {selectedRunner === "opencode" && (
              <>
                <Text style={[s.agentPickerSection, { color: c.textMuted }]}>OPENCODE AGENT</Text>
                <View style={s.agentPickerChips}>
                  {[
                    { id: "", name: "Default" },
                    { id: "build", name: "Build" },
                    { id: "plan", name: "Plan" },
                  ].map((m) => (
                    <Pressable
                      key={m.id || "default"}
                      style={[
                        s.modelChip,
                        { borderColor: selectedOpenCodeMode === m.id ? c.accent : c.border },
                        selectedOpenCodeMode === m.id && { backgroundColor: c.accent + "20" },
                      ]}
                      onPress={() => setSelectedOpenCodeMode(m.id)}
                    >
                      <Text style={[s.modelChipText, { color: selectedOpenCodeMode === m.id ? c.accent : c.textMuted }]}>
                        {m.name}
                      </Text>
                    </Pressable>
                  ))}
                </View>
              </>
            )}
          </View>
        </Modal>
        {/* ── Chat Detail Modal ───────────────────────────────────── */}
        <Modal visible={!!selectedTask} animationType="slide" transparent onRequestClose={() => setSelectedTask(null)}>
          <KeyboardAvoidingView
            style={s.chatModalOverlay}
            behavior={Platform.OS === "ios" ? "padding" : "height"}
            keyboardVerticalOffset={0}
          >
            {/* Tap outside to dismiss */}
            <Pressable style={s.chatModalDismissArea} onPress={() => setSelectedTask(null)} />
            {selectedTask && (
              <View style={[s.chatModal, { backgroundColor: c.bg }]}>
                {/* Header — Back (left) | Title+Status+Device (center) | Stop (right) */}
                <View style={[s.chatHeader, { borderBottomColor: c.border }]}>
                  {/* Left: Back button */}
                  <Pressable
                    style={({ pressed }) => [
                      { flexDirection: "row", alignItems: "center", gap: 4, paddingVertical: 6, paddingHorizontal: 10, paddingRight: 14, borderRadius: 8, backgroundColor: c.accent + "15" },
                      pressed && { opacity: 0.6 },
                    ]}
                    onPress={() => { setSelectedTask(null); setFollowUpText(""); }}
                  >
                    <Text style={{ fontSize: 18, color: c.accent, fontWeight: "600" }}>{"\u2039"}</Text>
                    <Text style={{ fontSize: 13, color: c.accent, fontWeight: "600" }}>Back</Text>
                  </Pressable>

                  {/* Center: title + status + device (3 lines) */}
                  <View style={{ flex: 1, alignItems: "center" }}>
                    <Text style={[s.chatHeaderTitle, { color: c.textPrimary }]} numberOfLines={1}>
                      {normalizeTaskTitle(selectedTask.title)}
                    </Text>
                    <View style={[s.chatHeaderMeta, { marginTop: 3 }]}>
                      <View style={[s.statusDotSmall, { backgroundColor: STATUS_COLORS[selectedTask.status] }]} />
                      <Text style={[s.chatHeaderStatus, { color: STATUS_COLORS[selectedTask.status] }]}>
                        {selectedTask.status}
                      </Text>
                      {isRunning ? <PhaseChip task={selectedTask} /> : null}
                      <Pressable
                        onPress={() => setShowLogs(true)}
                        style={{ marginLeft: 8, paddingHorizontal: 8, paddingVertical: 2, borderRadius: 4, backgroundColor: "#64748b22" }}
                      >
                        <Text style={{ color: "#94a3b8", fontSize: 11, fontWeight: "600" }}>Logs</Text>
                      </Pressable>
                      {/* Video summary chip — visible whenever the task has a
                          clip in any state. Tapping a "ready" clip plays it
                          via the existing VibePreviewModal (clip strip).
                          For "recording" / "queued" we show an indicator
                          pill. */}
                      {selectedTask.videoStatus === "ready" && selectedTask.videoClipId ? (
                        <Pressable
                          onPress={() => {
                            // Open the VibePreviewModal anchored to this
                            // task's project so its clip strip pre-loads
                            // the just-recorded MP4. ProjectName comes
                            // from the agent's videoProjectForTask helper.
                            // For now, just open the device dev banner's
                            // modal — the existing VibePreviewModal scrubber
                            // will surface the clip.
                            setVideoSummaryClipId(selectedTask.videoClipId!);
                          }}
                          style={{ marginLeft: 8, paddingHorizontal: 8, paddingVertical: 2, borderRadius: 4, backgroundColor: "#22c55e22" }}
                        >
                          <Text style={{ color: "#22c55e", fontSize: 11, fontWeight: "600" }}>▶ Watch demo</Text>
                        </Pressable>
                      ) : selectedTask.videoStatus === "recording" || selectedTask.videoStatus === "queued" ? (
                        <View style={{ marginLeft: 8, paddingHorizontal: 8, paddingVertical: 2, borderRadius: 4, backgroundColor: "#eab30822" }}>
                          <Text style={{ color: "#eab308", fontSize: 11, fontWeight: "600" }}>🎬 {selectedTask.videoStatus}…</Text>
                        </View>
                      ) : null}
                      {/* Cost hidden — Yaver is positioned as part of the free/open-source AI tool stack */}
                    </View>
                    {activeDevice && (
                      <Text style={{ fontSize: 10, color: c.textMuted, marginTop: 2 }} numberOfLines={1}>
                        {activeDevice.name.replace(/\.local$/, "")}
                      </Text>
                    )}
                  </View>

                  {/* Right: Stop button (only when running) */}
                  {isRunning ? (
                    <Pressable
                      style={({ pressed }) => [
                        { flexDirection: "row", alignItems: "center", gap: 5, paddingVertical: 6, paddingHorizontal: 10, borderRadius: 8, backgroundColor: selectedTask.isAdopted ? "#8b5cf618" : "#ef444418" },
                        pressed && { opacity: 0.6 },
                      ]}
                      onPress={() => {
                        if (selectedTask.isAdopted) {
                          Alert.alert(
                            "Detach Session",
                            `Stop monitoring "${selectedTask.tmuxSession || "tmux session"}"? The session will keep running.`,
                            [
                              { text: "Cancel", style: "cancel" },
                              { text: "Detach", onPress: () => handleDetachTmuxSession(selectedTask.id) },
                            ]
                          );
                        } else {
                          Alert.alert(
                            "Stop Task",
                            "The AI agent will be stopped and this session will be terminated. You can send a follow-up to resume later.",
                            [
                              { text: "Cancel", style: "cancel" },
                              { text: "Stop", style: "destructive", onPress: () => handleExitTask(selectedTask.id) },
                            ]
                          );
                        }
                      }}
                      onLongPress={() => {
                        if (!selectedTask.isAdopted) {
                          Alert.alert(
                            "Force Kill",
                            "The process will be killed immediately. Any unsaved progress will be lost.",
                            [
                              { text: "Cancel", style: "cancel" },
                              { text: "Kill", style: "destructive", onPress: () => handleStopTask(selectedTask.id) },
                            ]
                          );
                        }
                      }}
                    >
                      <Text style={{ fontSize: 14, color: selectedTask.isAdopted ? "#8b5cf6" : "#ef4444" }}>{selectedTask.isAdopted ? "\u23CF" : "\u25A0"}</Text>
                      <Text style={{ fontSize: 13, color: selectedTask.isAdopted ? "#8b5cf6" : "#ef4444", fontWeight: "600" }}>{selectedTask.isAdopted ? "Detach" : "Stop"}</Text>
                    </Pressable>
                  ) : (
                    <View style={{ width: 60 }} />
                  )}
                </View>

                {/* Dev server banner — shown inside task detail so user doesn't have to go back */}
                {isEffectivelyConnected && <DevPreview />}

                {/* Chat messages */}
                <ScrollView
                  ref={chatScrollRef}
                  style={s.chatScroll}
                  contentContainerStyle={s.chatScrollContent}
                  keyboardShouldPersistTaps="handled"
                >
                  {chatMessages.map((msg, i) => (
                    <ChatBubble key={`${i}-${msg.role}`} turn={msg} c={c} />
                  ))}
                  {isRunning && chatMessages[chatMessages.length - 1]?.role !== "assistant" && (
                    <View>
                      <TypingIndicator color={c.accent || "#6366f1"} />
                      <Text style={[s.startingHint, { color: c.textMuted }]}>
                        {(selectedTask?.turns?.length ?? 0) > 2 ? "Thinking..." : "Starting..."}
                      </Text>
                    </View>
                  )}
                  {isRunning && chatMessages[chatMessages.length - 1]?.role === "assistant" && (
                    <View style={s.streamingIndicator}>
                      <ActivityIndicator size="small" color={c.accent} />
                      <Text style={[s.streamingText, { color: c.textMuted }]}>Working...</Text>
                    </View>
                  )}

                  {/* Debug info (foldable) */}
                  <DebugSection task={selectedTask} connMode={connMode} c={c} />
                </ScrollView>

                {/* Follow-up input: compact bar, expands to full card on tap */}
                {followUpExpanded ? (
                  <View style={[s.modalContent, { backgroundColor: c.bgCard, borderTopWidth: 1, borderTopColor: c.border }]}>
                    <View style={s.modalHeader}>
                      <Text style={[s.modalTitle, { color: c.textPrimary }]}>Follow Up</Text>
                      {isRunning && <ActivityIndicator size="small" color={c.accent} />}
                    </View>
                    <TextInput
                      style={[s.input, s.inputMultiline, { backgroundColor: c.bg, borderColor: c.border, color: c.textPrimary }]}
                      placeholder={isRunning ? "Send a command..." : "Follow up..."}
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
                          <Text style={{ fontSize: 20, color: c.textSecondary }}>📷</Text>
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
                          <Text style={{ fontSize: 20, color: isRecording ? "#fff" : c.textSecondary }}>
                            {isRecording ? "\u25A0" : "\uD83C\uDFA4"}
                          </Text>
                        </Pressable>
                        <Pressable
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
                  <Pressable
                    style={[s.chatInputBar, { borderTopColor: c.border, backgroundColor: c.bgCard }]}
                    onPress={() => setFollowUpExpanded(true)}
                  >
                    <View style={[s.chatInput, { backgroundColor: c.bg, borderColor: c.border, justifyContent: "center", minHeight: 44, maxHeight: 44 }]}>
                      <Text style={{ color: c.textMuted, fontSize: 15 }}>{isRunning ? "Send a command..." : "Follow up..."}</Text>
                    </View>
                  </Pressable>
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
        <Modal visible={showTmuxSessions} animationType="slide" transparent onRequestClose={() => setShowTmuxSessions(false)}>
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

  // Banner
  banner: { flexDirection: "row", alignItems: "center", paddingHorizontal: 18, paddingVertical: 12, borderBottomWidth: 1 },
  dot: { width: 8, height: 8, borderRadius: 4, marginRight: 8 },
  bannerText: { fontSize: 13, fontWeight: "600", letterSpacing: 0.1 },

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
  listContentEmpty: { flex: 1 },
  emptyList: { flex: 1, justifyContent: "center", alignItems: "center", paddingHorizontal: 32 },
  emptyIcon: { fontSize: 48, marginBottom: 16 },
  emptyTitle: { fontSize: 20, fontWeight: "700", marginBottom: 8 },
  emptySubtitle: { fontSize: 14, textAlign: "center", lineHeight: 20 },

  // Inline connect button (reconnect after user disconnect)
  inlineConnectBtn: { marginTop: 20, paddingHorizontal: 28, paddingVertical: 12, borderRadius: 10 },
  inlineConnectText: { color: "#ffffff", fontWeight: "600", fontSize: 15 },

  // Device picker cards (multi-device selection)
  devicePickerCard: { width: "100%", borderWidth: 1, borderRadius: 12, padding: 14, marginBottom: 10 },
  devicePickerRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  devicePickerName: { fontSize: 16, fontWeight: "600" },
  devicePickerMeta: { fontSize: 12, marginTop: 2 },
  devicePickerDot: { width: 10, height: 10, borderRadius: 5 },

  // Error actions row
  errorActions: { flexDirection: "row", marginTop: 20 },

  // Discover card (no devices)
  discoverIcon: { fontSize: 40, marginBottom: 12 },
  discoverPrimaryBtn: { width: "100%", paddingVertical: 14, borderRadius: 12, alignItems: "center", justifyContent: "center" },
  discoverSecondaryBtn: { width: "100%", marginTop: 20, paddingVertical: 12, borderRadius: 10, borderWidth: 1, alignItems: "center", justifyContent: "center", minHeight: 44 },
  discoverHelper: { fontSize: 12, lineHeight: 18, marginTop: 12, textAlign: "center", paddingHorizontal: 8 },
  discoverDivider: { height: 1, width: "100%", marginTop: 28, marginBottom: 14, opacity: 0.5 },
  discoverSectionLabel: { fontSize: 11, fontWeight: "600", letterSpacing: 1, textTransform: "uppercase", marginBottom: 8 },
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
  reconnectDeviceMeta: { fontSize: 12, marginTop: 2, fontFamily: "monospace" },
  reconnectDeviceStatus: { flexDirection: "row", alignItems: "center", gap: 6, paddingHorizontal: 10, paddingVertical: 4, borderRadius: 8 },
  reconnectStatusDot: { width: 8, height: 8, borderRadius: 4 },
  reconnectStatusText: { fontSize: 11, fontWeight: "600", textTransform: "uppercase" },
  reconnectError: { fontSize: 13, textAlign: "center", marginTop: 12, lineHeight: 18 },
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
    borderRadius: 20,
    paddingHorizontal: 16,
    paddingVertical: 15,
    borderWidth: 1,
    shadowColor: "#000",
    shadowOffset: { width: 0, height: 10 },
    shadowOpacity: 0.14,
    shadowRadius: 18,
    elevation: 2,
  },
  taskCardPressed: { opacity: 0.7 },
  taskHeader: { flexDirection: "row", alignItems: "flex-start", justifyContent: "space-between", marginBottom: 10, gap: 10 },
  taskHeaderMain: { flexDirection: "row", alignItems: "center", gap: 7, flexWrap: "wrap", flex: 1 },
  statusBadge: { flexDirection: "row", alignItems: "center", gap: 7, paddingHorizontal: 10, paddingVertical: 5, borderRadius: 999 },
  statusPulseDot: { width: 7, height: 7, borderRadius: 4 },
  statusText: { fontSize: 11, fontWeight: "700", textTransform: "uppercase", letterSpacing: 0.45 },
  metaPill: { paddingHorizontal: 8, paddingVertical: 4, borderRadius: 999, borderWidth: 1 },
  metaPillText: { fontSize: 10, fontWeight: "700", textTransform: "uppercase", letterSpacing: 0.3 },
  taskRunnerLabel: { fontSize: 11, marginTop: 4, marginLeft: 8, maxWidth: 90, textAlign: "right" },
  taskTitle: { fontSize: 17, fontWeight: "600", lineHeight: 22, letterSpacing: -0.2 },
  taskPhaseRow: { marginBottom: 8 },
  phaseChip: { alignSelf: "flex-start", paddingHorizontal: 10, paddingVertical: 5, borderRadius: 999, borderWidth: 1 },
  phaseChipText: { fontSize: 11, fontWeight: "700", textTransform: "lowercase", letterSpacing: 0.25 },
  taskOutputPreview: { fontSize: 13, marginTop: 4, lineHeight: 18 },
  taskFooter: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginTop: 10 },
  taskTimestamp: { fontSize: 11 },
  taskFooterMeta: { fontSize: 11, fontWeight: "600" },

  // FAB
  fab: { position: "absolute", bottom: 24, right: 20, width: 58, height: 58, borderRadius: 29, backgroundColor: "#111827", alignItems: "center", justifyContent: "center", elevation: 4, shadowColor: "#000", shadowOffset: { width: 0, height: 8 }, shadowOpacity: 0.28, shadowRadius: 12 },
  fabPressed: { opacity: 0.8, transform: [{ scale: 0.95 }] },
  fabText: { fontSize: 28, color: "#ffffff", fontWeight: "300" },

  // New task modal
  modalOverlay: { flex: 1, backgroundColor: "rgba(0,0,0,0.6)", justifyContent: "flex-end" },
  modalDismiss: { flex: 1 },
  modalContent: { borderTopLeftRadius: 24, borderTopRightRadius: 24, padding: 24, paddingTop: 28, paddingBottom: 40 },
  modalHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginBottom: 24 },
  modalTitle: { fontSize: 20, fontWeight: "700" },
  agentBadge: { flexDirection: "row", alignItems: "center", paddingHorizontal: 10, paddingVertical: 5, borderRadius: 10, borderWidth: 1 },
  agentBadgeText: { fontSize: 12, fontWeight: "500" },
  agentPickerSheet: { borderTopLeftRadius: 20, borderTopRightRadius: 20, paddingBottom: 40 },
  agentPickerHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 20, paddingVertical: 16, borderBottomWidth: 1 },
  agentPickerTitle: { fontSize: 17, fontWeight: "700" },
  agentPickerSection: { fontSize: 11, fontWeight: "600", letterSpacing: 0.5, marginTop: 16, marginBottom: 8, marginLeft: 20 },
  agentPickerChips: { flexDirection: "row", flexWrap: "wrap", gap: 8, paddingHorizontal: 16, marginBottom: 4 },
  input: { borderWidth: 1, borderRadius: 12, padding: 16, fontSize: 16, marginBottom: 12 },
  inputMultiline: { minHeight: 200 },
  modalButtons: { flexDirection: "row", gap: 12, marginTop: 16 },
  cancelButton: { flex: 1, paddingVertical: 14, borderRadius: 10, alignItems: "center" },
  cancelButtonText: { fontWeight: "600", fontSize: 15 },
  submitButton: { flex: 1, paddingVertical: 14, borderRadius: 10, alignItems: "center" },
  submitButtonDisabled: { opacity: 0.4 },
  submitButtonText: { color: "#ffffff", fontWeight: "600", fontSize: 15 },

  // Action bar
  actionBar: { flexDirection: "row", paddingHorizontal: 14, paddingVertical: 8, gap: 8 },
  actionButton: { paddingHorizontal: 14, paddingVertical: 7, borderRadius: 999 },
  actionButtonText: { fontSize: 12, fontWeight: "700", letterSpacing: 0.1 },

  // ── Chat modal ─────────────────────────────────────────────────────
  chatModalOverlay: { flex: 1, backgroundColor: "rgba(0,0,0,0.3)" },
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
  userBubbleText: { color: "#fff", fontSize: 15, lineHeight: 21 },

  assistantRow: { flexDirection: "row", justifyContent: "flex-start", marginBottom: 12 },
  assistantBubble: { maxWidth: "92%", borderRadius: 22, borderBottomLeftRadius: 8, paddingHorizontal: 14, paddingVertical: 13, borderWidth: 1, borderColor: "rgba(255,255,255,0.04)" },
  assistantHeaderRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginBottom: 8 },
  assistantLabel: { fontSize: 11, fontWeight: "700", textTransform: "uppercase", letterSpacing: 0.6 },
  assistantToggle: { fontSize: 12, fontWeight: "600" },
  assistantSummary: { fontSize: 14, lineHeight: 20 },
  assistantActivityList: { gap: 8, marginTop: 10 },
  assistantActivityRow: { borderWidth: 1, borderRadius: 10, paddingHorizontal: 10, paddingVertical: 8 },
  assistantActivityText: { fontSize: 12, lineHeight: 17, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" },
  assistantRawWrap: { marginTop: 12, paddingTop: 12, borderTopWidth: 1 },

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
    code_inline: { backgroundColor: c.bgCardElevated || "#1e1e2e", color: "#e879f9", fontFamily: "monospace", fontSize: 13, paddingHorizontal: 5, paddingVertical: 1, borderRadius: 4 },
    fence: { backgroundColor: c.bgCardElevated || "#0a0a14", borderRadius: 8, padding: 10, marginVertical: 6 },
    code_block: { color: "#a5f3fc", fontFamily: "monospace", fontSize: 11, lineHeight: 17 },
    blockquote: { borderLeftWidth: 3, borderLeftColor: c.accent || "#6366f1", paddingLeft: 12, marginVertical: 6, opacity: 0.85 },
    link: { color: c.accent || "#6366f1" },
    hr: { backgroundColor: c.border || "#1e1e2e", height: 1, marginVertical: 10 },
    table: { borderColor: c.border || "#1e1e2e" },
    tr: { borderBottomColor: c.border || "#1e1e2e" },
    th: { color: c.textPrimary, fontWeight: "700" as const, padding: 6 },
    td: { color: c.textPrimary, padding: 6 },
  };
}
