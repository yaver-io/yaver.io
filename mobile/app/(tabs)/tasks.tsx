import React, { useCallback, useEffect, useRef, useState } from "react";
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
import * as FileSystem from "expo-file-system";
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

  return (
    <View style={s.assistantRow}>
      <View style={[s.assistantBubble, { backgroundColor: c.bgCardElevated || "#1a1a2e" }]}>
        <Markdown style={markdownStyles(c)}>{turn.content || " "}</Markdown>
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

  const previewText = item.resultText
    ? item.resultText.substring(0, 120) + (item.resultText.length > 120 ? "..." : "")
    : item.status === "running"
      ? "Running..."
      : null;

  return (
    <TouchableOpacity
      style={[s.cardContainer, s.taskCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
      onPress={onPress}
      onLongPress={handleLongPress}
      activeOpacity={0.7}
    >
      <View style={s.taskHeader}>
        <View style={[s.statusBadge, { backgroundColor: STATUS_COLORS[item.status] + "22" }]}>
          <Text style={[s.statusText, { color: STATUS_COLORS[item.status] }]}>{item.status}</Text>
        </View>
        {item.isAdopted && (
          <View style={[s.statusBadge, { backgroundColor: "#8b5cf622" }]}>
            <Text style={[s.statusText, { color: "#8b5cf6" }]}>tmux</Text>
          </View>
        )}
        {item.runnerId && item.runnerId !== "claude" && item.runnerId !== "unknown" && (
          <View style={[s.statusBadge, { backgroundColor: "#f59e0b22" }]}>
            <Text style={[s.statusText, { color: "#f59e0b" }]}>{item.runnerId}</Text>
          </View>
        )}
        {item.status === "running" && <ActivityIndicator size="small" color="#6366f1" />}
      </View>
      <Text style={[s.taskTitle, { color: c.textPrimary }]} numberOfLines={2}>{item.title}</Text>
      {previewText ? (
        <Text style={[s.taskOutputPreview, { color: c.accent }]} numberOfLines={1}>{previewText}</Text>
      ) : null}
      <Text style={[s.taskTimestamp, { color: c.textMuted }]}>{formatRelativeTime(item.updatedAt)}</Text>
    </TouchableOpacity>
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
    messages.push({ role: "user", content: task.title });
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
  const { connectionStatus, activeDevice, devices, userDisconnected, lastError, selectDevice, isLoadingDevices, refreshDevices } = useDevice();
  const [showLogs, setShowLogs] = useState(false);
  const [logs, setLogs] = useState<LogEntry[]>(getLogEntries());

  // Subscribe to log changes
  useEffect(() => {
    return onLogsChanged(() => setLogs(getLogEntries()));
  }, []);
  const [tasks, setTasks] = useState<Task[]>([]);
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
  const [agentStatus, setAgentStatus] = useState<AgentStatus | null>(null);
  const [pingRtt, setPingRtt] = useState<number | null>(null);
  const [isPinging, setIsPinging] = useState(false);
  const [pingResult, setPingResult] = useState<{ ok: boolean; rttMs: number; hostname?: string; mode?: string } | null>(null);
  const [showPingResult, setShowPingResult] = useState(false);
  const [isRestartingRunner, setIsRestartingRunner] = useState(false);
  const [availableRunners, setAvailableRunners] = useState<RunnerInfo[]>([]);
  const [selectedRunner, setSelectedRunner] = useState<string>(""); // "" = default
  const [availableModels, setAvailableModels] = useState<ModelInfo[]>([]);
  const [customCommand, setCustomCommand] = useState("");
  const [showAgentPicker, setShowAgentPicker] = useState(false);
  const [showTmuxSessions, setShowTmuxSessions] = useState(false);
  const [tmuxSessions, setTmuxSessions] = useState<TmuxSession[]>([]);
  const [isLoadingTmux, setIsLoadingTmux] = useState(false);
  const [isAdopting, setIsAdopting] = useState<string | null>(null); // session name being adopted
  const chatScrollRef = useRef<ScrollView>(null);
  const pendingOpenTaskRef = useRef<Task | null>(null);

  // Speech state
  const { token } = useAuth();
  const [isRecording, setIsRecording] = useState(false);
  const [isTranscribing, setIsTranscribing] = useState(false);
  const [speechProvider, setSpeechProvider] = useState<SpeechProvider | null>("on-device");
  const [speechApiKey, setSpeechApiKey] = useState<string | undefined>();
  const [ttsEnabled, setTtsEnabled] = useState(false);
  const [verbosity, setVerbosity] = useState(10);
  const [inputFromSpeech, setInputFromSpeech] = useState(false);
  const audioRecordingRef = useRef<any>(null);
  const realtimeRef = useRef<{ stop: () => Promise<string> } | null>(null);
  const recordingTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [preRecordText, setPreRecordText] = useState(""); // text before recording started

  // Load speech settings from Convex (default: on-device whisper)
  useEffect(() => {
    // Pre-init whisper for default on-device provider
    initWhisper().catch(() => {});
    if (!token) return;
    getUserSettings(token).then(async (s) => {
      if (s.speechProvider) setSpeechProvider(s.speechProvider);
      if (s.ttsEnabled) setTtsEnabled(s.ttsEnabled);
      if (s.verbosity !== undefined) setVerbosity(s.verbosity);
      // Load speech API key — prefer local Keychain, fall back to cloud
      const localKey = await getLocalSecret(LOCAL_KEYS.speechApiKey);
      if (localKey) setSpeechApiKey(localKey);
      else if (s.speechApiKey) setSpeechApiKey(s.speechApiKey);
    }).catch(() => {});
  }, [token]);

  // Track QUIC connection state and mode
  useEffect(() => {
    const unsub1 = quicClient.on("connectionState", setQuicState);
    const unsub2 = quicClient.on("connectionMode", setConnMode);
    return () => { unsub1(); unsub2(); };
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
        // Set default runner selection and its models
        const def = r.find(x => x.isDefault);
        if (def) {
          setSelectedRunner(def.id);
          if (def.models?.length > 0) {
            setAvailableModels(def.models);
            const defModel = def.models.find(m => m.isDefault);
            if (defModel) setSelectedModel(defModel.id);
          }
        }
      }
    });
  }, [connectionStatus]);

  // Update models when runner selection changes
  useEffect(() => {
    const runner = availableRunners.find(r => r.id === selectedRunner);
    if (runner?.models?.length) {
      setAvailableModels(runner.models);
      const defModel = runner.models.find(m => m.isDefault);
      if (defModel) setSelectedModel(defModel.id);
      else setSelectedModel(runner.models[0].id);
    } else {
      setAvailableModels([]);
      setSelectedModel("");
    }
  }, [selectedRunner, availableRunners]);

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
      // Filter out locally-deleted tasks so they don't reappear
      const deletedIds = await getDeletedTaskIds();
      const filtered = deletedIds.size > 0 ? list.filter((t) => !deletedIds.has(t.id)) : list;
      setTasks(filtered);
      // Keep selected task in sync with latest data
      setSelectedTask((prev) => {
        if (!prev) return null;
        return filtered.find((t) => t.id === prev.id) || prev;
      });
    } catch {}
  }, []);

  const hasRunningTask = tasks.some(t => t.status === "running" || t.status === "queued");
  useEffect(() => {
    fetchTasks();
    // Poll less frequently when a task is running (streaming handles live output)
    const interval = setInterval(fetchTasks, hasRunningTask ? 10000 : 3000);
    return () => clearInterval(interval);
  }, [fetchTasks, hasRunningTask]);

  // Listen for streaming output — buffer updates to avoid UI freezing
  const outputBufferRef = useRef<Record<string, string[]>>({});
  const flushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
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

    const unsub = quicClient.on("output", (taskId, line) => {
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
      const task = await quicClient.sendTask(
        newTaskText.trim(), "",
        selectedRunner === "custom" ? undefined : (selectedModel || undefined),
        selectedRunner === "custom" ? "custom" : (selectedRunner || undefined),
        selectedRunner === "custom" ? customCommand.trim() || undefined : undefined,
        speechCtx,
        attachedImages.length > 0 ? attachedImages : undefined,
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
    try { await quicClient.stopTask(taskId); await fetchTasks(); } catch {}
  };

  const handleExitTask = async (taskId: string) => {
    try { await quicClient.exitTask(taskId); await fetchTasks(); } catch {}
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
  const showRetryButton = connectionStatus === "disconnected" && activeDevice && !userDisconnected;

  const chatMessages = selectedTask ? buildChatMessages(selectedTask) : [];
  const isRunning = selectedTask?.status === "running" || selectedTask?.status === "queued";

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
              {lastError && connectionStatus === "error" ? lastError : banner.label}
              {isEffectivelyConnected ? modeLabel : ""}
              {activeDevice ? ` \u00b7 ${activeDevice.name}` : ""}
            </Text>
            {showRetryButton && (
              <Pressable
                style={{ marginLeft: 8, paddingHorizontal: 10, paddingVertical: 3, borderRadius: 6, backgroundColor: "#6366f133" }}
                onPress={() => activeDevice && selectDevice(activeDevice)}
              >
                <Text style={{ fontSize: 12, color: "#818cf8", fontWeight: "600" }}>Retry</Text>
              </Pressable>
            )}
          </View>
          {isEffectivelyConnected && (
            <View style={{ flexDirection: "row", alignItems: "center", marginTop: 4, marginLeft: 18 }}>
              {agentStatus && (
                <>
                  <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: agentStatus.runner.installed ? "#22c55e" : "#ef4444" }} />
                  <Text style={{ color: agentStatus.runner.installed ? "#4ade80" : "#f87171", fontSize: 11, marginLeft: 6 }}>
                    {agentStatus.runner.name} {agentStatus.runner.installed ? "ready" : "not found"}
                    {agentStatus.runningTasks > 0 ? ` \u00b7 ${agentStatus.runningTasks} running` : ""}
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
          {agentStatus && isEffectivelyConnected && !agentStatus.runner.installed && (
            <View style={{ flexDirection: "row", alignItems: "center", marginTop: 2, marginLeft: 18 }}>
              {agentStatus.runner.installed === false && (
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
        {isEffectivelyConnected && <DevPreview />}

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

        {/* Action bar */}
        {isEffectivelyConnected && (
          <View style={[s.actionBar, { borderBottomColor: c.border }]}>
            {tasks.some(t => t.status === "running") && (
              <Pressable style={[s.actionButton, { backgroundColor: "#ef444418" }]} onPress={handleStopAll}>
                <Text style={[s.actionButtonText, { color: "#ef4444" }]}>Stop All</Text>
              </Pressable>
            )}
            {tasks.some(t => t.status !== "running" && t.status !== "queued") && (
              <Pressable style={[s.actionButton, { backgroundColor: c.bgCardElevated }]} onPress={handleDeleteAll}>
                <Text style={[s.actionButtonText, { color: c.textMuted }]}>Clear History</Text>
              </Pressable>
            )}
            <Pressable style={[s.actionButton, { backgroundColor: "#8b5cf618" }]} onPress={handleOpenTmuxSessions}>
              <Text style={[s.actionButtonText, { color: "#8b5cf6" }]}>Tmux</Text>
            </Pressable>
          </View>
        )}

        {/* Task list */}
        <FlatList
          data={tasks}
          keyExtractor={(item) => item.id}
          alwaysBounceVertical
          contentContainerStyle={[s.listContent, tasks.length === 0 && s.listContentEmpty]}
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
                <View style={[s.discoverCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.discoverIcon, { color: c.textMuted }]}>{"\u2318"}</Text>
                  <Text style={[s.emptyTitle, { color: c.textPrimary }]}>Set Up Your Dev Machine</Text>
                  <Text style={[s.emptySubtitle, { color: c.textSecondary, marginTop: 8 }]}>
                    Install the Yaver agent on your computer to start sending tasks from your phone.
                  </Text>
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
                        <Text style={[s.discoverStepTitle, { color: c.textPrimary }]}>Sign in & start</Text>
                        <Text style={[s.discoverStepDesc, { color: c.textMuted }]}>yaver auth</Text>
                      </View>
                    </View>
                  </View>
                  <Pressable
                    style={[s.discoverBtn, { backgroundColor: c.accent }]}
                    onPress={() => refreshDevices()}
                  >
                    <Text style={s.discoverBtnText}>Refresh Devices</Text>
                  </Pressable>
                </View>
              </View>
            ) : userDisconnected && devices.length >= 1 ? (
              <View style={s.emptyList}>
                <View style={[s.reconnectCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.reconnectIcon, { color: c.textMuted }]}>{"\u23FB"}</Text>
                  <Text style={[s.emptyTitle, { color: c.textPrimary }]}>Disconnected</Text>
                  <Text style={[s.emptySubtitle, { color: c.textSecondary, marginTop: 8 }]}>
                    Your last session
                  </Text>

                  <Pressable
                    style={[s.reconnectDeviceCard, { backgroundColor: c.bg, borderColor: c.border }]}
                    onPress={() => !isReconnecting && handleReconnect(devices[0])}
                    disabled={isReconnecting}
                  >
                    <View style={s.reconnectDeviceRow}>
                      <View style={s.reconnectDeviceInfo}>
                        <Text style={[s.reconnectDeviceName, { color: c.textPrimary }]}>{devices[0].name}</Text>
                        <Text style={[s.reconnectDeviceMeta, { color: c.textMuted }]}>
                          {devices[0].os} · {devices[0].host}
                        </Text>
                      </View>
                      <View style={[s.reconnectDeviceStatus, { backgroundColor: devices[0].online ? "#22c55e22" : "#a1a1aa22" }]}>
                        <View style={[s.reconnectStatusDot, { backgroundColor: devices[0].online ? "#22c55e" : "#a1a1aa" }]} />
                        <Text style={[s.reconnectStatusText, { color: devices[0].online ? "#22c55e" : "#a1a1aa" }]}>
                          {devices[0].online ? "Online" : "Offline"}
                        </Text>
                      </View>
                    </View>
                  </Pressable>

                  {reconnectError && (
                    <Text style={[s.reconnectError, { color: "#ef4444" }]}>{reconnectError}</Text>
                  )}

                  <Pressable
                    style={[s.reconnectBtn, { backgroundColor: c.accent }, isReconnecting && s.submitButtonDisabled]}
                    onPress={() => handleReconnect(devices[0])}
                    disabled={isReconnecting}
                  >
                    {isReconnecting ? (
                      <View style={s.reconnectBtnRow}>
                        <ActivityIndicator size="small" color="#fff" />
                        <Text style={s.reconnectBtnText}>Reconnecting...</Text>
                      </View>
                    ) : (
                      <Text style={s.reconnectBtnText}>Reconnect</Text>
                    )}
                  </Pressable>
                </View>
              </View>
            ) : connectionStatus === "error" && lastError ? (
              <View style={s.emptyList}>
                <Text style={[s.emptyIcon, { color: "#ef4444" }]}>!</Text>
                <Text style={[s.emptyTitle, { color: c.textPrimary }]}>Connection Failed</Text>
                <Text style={[s.emptySubtitle, { color: c.textSecondary }]}>
                  {lastError}
                </Text>
                <View style={s.errorActions}>
                  <Pressable
                    style={[s.inlineConnectBtn, { backgroundColor: c.accent }]}
                    onPress={() => { if (devices.length === 1) selectDevice(devices[0]); }}
                  >
                    <Text style={s.inlineConnectText}>Retry</Text>
                  </Pressable>
                  <Pressable
                    style={[s.inlineConnectBtn, { backgroundColor: c.bgCardElevated || "#222", marginLeft: 10 }]}
                    onPress={() => setShowLogs(true)}
                  >
                    <Text style={[s.inlineConnectText, { color: c.textSecondary }]}>View Logs</Text>
                  </Pressable>
                </View>
              </View>
            ) : devices.length === 1 && connectionStatus === "connecting" ? (
              <View style={s.emptyList}>
                <ActivityIndicator size="large" color={c.accent} />
                <Text style={[s.emptyTitle, { color: c.textPrimary, marginTop: 16 }]}>Connecting...</Text>
                <Text style={[s.emptySubtitle, { color: c.textSecondary }]}>
                  {devices[0].name}
                </Text>
              </View>
            ) : devices.length >= 1 && !activeDevice ? (
              <View style={s.emptyList}>
                <Text style={[s.emptyIcon, { color: c.textMuted }]}>{"\u2630"}</Text>
                <Text style={[s.emptyTitle, { color: c.textPrimary }]}>
                  {devices.length} {devices.length === 1 ? "Device" : "Devices"} Available
                </Text>
                <Text style={[s.emptySubtitle, { color: c.textSecondary, marginBottom: 16 }]}>
                  Which device do you want to connect to?
                </Text>
                {devices.map((d) => (
                  <Pressable
                    key={d.id}
                    style={[s.devicePickerCard, { backgroundColor: c.bgCard, borderColor: c.border }]}
                    onPress={() => selectDevice(d)}
                  >
                    <View style={s.devicePickerRow}>
                      <View>
                        <Text style={[s.devicePickerName, { color: c.textPrimary }]}>{d.name}</Text>
                        <Text style={[s.devicePickerMeta, { color: c.textMuted }]}>{d.os} · {d.host}</Text>
                      </View>
                      <View style={[s.devicePickerDot, { backgroundColor: d.online ? c.success || "#22c55e" : c.textMuted }]} />
                    </View>
                  </Pressable>
                ))}
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
          <Pressable style={({ pressed }) => [s.fab, pressed && s.fabPressed]} onPress={() => setShowNewTask(true)}>
            <Text style={s.fabText}>+</Text>
          </Pressable>
        )}

        {/* New Task Modal */}
        <Modal visible={showNewTask} animationType="slide" transparent onDismiss={handleNewTaskModalDismiss}>
          <KeyboardAvoidingView style={s.modalOverlay} behavior={Platform.OS === "ios" ? "padding" : "height"}>
            <Pressable style={s.modalDismiss} onPress={() => { Keyboard.dismiss(); setShowNewTask(false); setNewTaskText(""); }} />
            <View style={[s.modalContent, { backgroundColor: c.bgCard }]}>
              <View style={s.modalHeader}>
                <Text style={[s.modalTitle, { color: c.textPrimary }]}>New Task</Text>
                {(availableRunners.length > 0 || availableModels.length > 0) && (
                  <Pressable
                    style={[s.agentBadge, { backgroundColor: c.bgCardElevated, borderColor: c.border }]}
                    onPress={() => setShowAgentPicker(true)}
                  >
                    <Text style={[s.agentBadgeText, { color: c.textSecondary }]}>
                      {(() => {
                        const runner = availableRunners.find(r => r.id === selectedRunner);
                        const model = availableModels.find(m => m.id === selectedModel);
                        const runnerLabel = selectedRunner === "custom" ? "Custom" : (runner?.name || "Claude");
                        const modelLabel = model?.name || selectedModel || "";
                        return modelLabel ? `${runnerLabel} · ${modelLabel}` : runnerLabel;
                      })()}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 10, marginLeft: 4 }}>▾</Text>
                  </Pressable>
                )}
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
                    style={[s.submitButton, { backgroundColor: c.accent }, ((!newTaskText.trim() && attachedImages.length === 0) || isSubmitting || isTranscribing) && s.submitButtonDisabled]}
                    onPress={handleCreateTask}
                    disabled={(!newTaskText.trim() && attachedImages.length === 0) || isSubmitting || isTranscribing}
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
            {availableRunners.length > 0 && (
              <>
                <Text style={[s.agentPickerSection, { color: c.textMuted }]}>AGENT</Text>
                <View style={s.agentPickerChips}>
                  {availableRunners.map((r) => (
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
            )}
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
                      {selectedTask.title}
                    </Text>
                    <View style={[s.chatHeaderMeta, { marginTop: 3 }]}>
                      <View style={[s.statusDotSmall, { backgroundColor: STATUS_COLORS[selectedTask.status] }]} />
                      <Text style={[s.chatHeaderStatus, { color: STATUS_COLORS[selectedTask.status] }]}>
                        {selectedTask.status}
                      </Text>
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
                <Text style={[s.logsTitle, { color: c.textPrimary }]}>Connection Logs</Text>
                <View style={s.logsHeaderActions}>
                  <Pressable onPress={() => {
                    const text = logs.map(l => `${new Date(l.timestamp).toLocaleTimeString()} [${l.level}] ${l.message}`).join("\n");
                    ExpoClipboard.setStringAsync(text);
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
  banner: { flexDirection: "row", alignItems: "center", paddingHorizontal: 16, paddingVertical: 10, borderBottomWidth: 1 },
  dot: { width: 8, height: 8, borderRadius: 4, marginRight: 8 },
  bannerText: { fontSize: 13, fontWeight: "500" },

  // Ping overlay
  pingOverlay: { marginHorizontal: 16, marginTop: 8, padding: 14, borderRadius: 12, borderWidth: 1 },
  pingTitle: { fontSize: 15, fontWeight: "700", marginBottom: 4 },
  pingDetail: { fontSize: 12, marginBottom: 2 },
  pingBar: { height: 4, borderRadius: 2, marginTop: 8, overflow: "hidden" as const },
  pingBarFill: { height: 4, borderRadius: 2 },
  pingDismiss: { fontSize: 10, marginTop: 6, textAlign: "center" as const },

  // List
  listContent: { padding: 16, paddingBottom: 100 },
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
  discoverCard: { width: "100%", borderRadius: 16, borderWidth: 1, padding: 24, alignItems: "center" },
  discoverIcon: { fontSize: 40, marginBottom: 12 },
  discoverSteps: { width: "100%", marginTop: 20, gap: 14 },
  discoverStep: { flexDirection: "row", alignItems: "center", gap: 12 },
  discoverStepDot: { width: 28, height: 28, borderRadius: 14, alignItems: "center", justifyContent: "center" },
  discoverStepNum: { color: "#fff", fontSize: 13, fontWeight: "700" },
  discoverStepContent: { flex: 1 },
  discoverStepTitle: { fontSize: 14, fontWeight: "600" },
  discoverStepDesc: { fontSize: 12, fontFamily: "monospace", marginTop: 2 },
  discoverBtn: { marginTop: 24, paddingHorizontal: 28, paddingVertical: 12, borderRadius: 10 },
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
  logsModal: { flex: 1, borderTopLeftRadius: 20, borderTopRightRadius: 20, overflow: "hidden" },
  logsHeader: { flexDirection: "row", justifyContent: "space-between", alignItems: "center", paddingHorizontal: 16, paddingVertical: 14, borderBottomWidth: 1 },
  logsTitle: { fontSize: 16, fontWeight: "700" },
  logsHeaderActions: { flexDirection: "row", alignItems: "center" },
  logsActionText: { fontSize: 15, fontWeight: "600" },
  logsScroll: { flex: 1 },
  logsScrollContent: { padding: 12 },
  logsEmpty: { fontSize: 14, textAlign: "center", marginTop: 40 },
  logLine: { fontSize: 11, fontFamily: "monospace", lineHeight: 16, marginBottom: 2 },

  // Task card
  cardContainer: { marginBottom: 12 },
  taskCard: { borderRadius: 12, padding: 16, borderWidth: 1 },
  taskCardPressed: { opacity: 0.7 },
  taskHeader: { flexDirection: "row", alignItems: "center", marginBottom: 8, gap: 8 },
  statusBadge: { paddingHorizontal: 10, paddingVertical: 4, borderRadius: 6 },
  statusText: { fontSize: 12, fontWeight: "600", textTransform: "uppercase" },
  taskTitle: { fontSize: 16, fontWeight: "600" },
  taskOutputPreview: { fontSize: 12, marginTop: 6, fontFamily: "monospace" },
  taskTimestamp: { fontSize: 11, marginTop: 8 },

  // FAB
  fab: { position: "absolute", bottom: 24, right: 24, width: 56, height: 56, borderRadius: 28, backgroundColor: "#6366f1", alignItems: "center", justifyContent: "center", elevation: 4, shadowColor: "#6366f1", shadowOffset: { width: 0, height: 4 }, shadowOpacity: 0.3, shadowRadius: 8 },
  fabPressed: { opacity: 0.8, transform: [{ scale: 0.95 }] },
  fabText: { fontSize: 28, color: "#ffffff", fontWeight: "300" },

  // New task modal
  modalOverlay: { flex: 1, backgroundColor: "rgba(0,0,0,0.6)", justifyContent: "flex-end" },
  modalDismiss: { flex: 1 },
  modalContent: { borderTopLeftRadius: 20, borderTopRightRadius: 20, padding: 24, paddingTop: 28, paddingBottom: 40 },
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
  actionBar: { flexDirection: "row", paddingHorizontal: 16, paddingVertical: 8, gap: 8, borderBottomWidth: 1 },
  actionButton: { paddingHorizontal: 14, paddingVertical: 6, borderRadius: 8 },
  actionButtonText: { fontSize: 12, fontWeight: "600" },

  // ── Chat modal ─────────────────────────────────────────────────────
  chatModalOverlay: { flex: 1, backgroundColor: "rgba(0,0,0,0.3)" },
  chatModalDismissArea: { height: 50 },
  chatModal: { flex: 1, borderTopLeftRadius: 20, borderTopRightRadius: 20, overflow: "hidden" },

  // Chat header
  chatHeader: { flexDirection: "row", alignItems: "center", paddingHorizontal: 14, paddingVertical: 14, borderBottomWidth: 1 },
  chatHeaderDevice: { flexDirection: "row", alignItems: "center", gap: 4 },
  chatHeaderDeviceText: { fontSize: 10, fontWeight: "500" },
  chatHeaderTitle: { fontSize: 15, fontWeight: "600" },
  chatHeaderMeta: { flexDirection: "row", alignItems: "center", gap: 4 },
  statusDotSmall: { width: 6, height: 6, borderRadius: 3 },
  chatHeaderStatus: { fontSize: 11, fontWeight: "500", textTransform: "uppercase" },
  chatHeaderCost: { fontSize: 11, marginLeft: 6 },
  // chatStopBtn removed — now using chatHeaderRight
  chatStopText: { color: "#ef4444", fontSize: 14, fontWeight: "600" },

  // Chat messages
  chatScroll: { flex: 1 },
  chatScrollContent: { padding: 16, paddingBottom: 80 },

  userRow: { flexDirection: "row", justifyContent: "flex-end", marginBottom: 12 },
  userBubble: { maxWidth: "80%", borderRadius: 18, borderBottomRightRadius: 4, paddingHorizontal: 16, paddingVertical: 10 },
  userBubbleText: { color: "#fff", fontSize: 15, lineHeight: 21 },

  assistantRow: { flexDirection: "row", justifyContent: "flex-start", marginBottom: 12 },
  assistantBubble: { maxWidth: "90%", borderRadius: 18, borderBottomLeftRadius: 4, paddingHorizontal: 14, paddingVertical: 10 },

  // Typing indicator
  typingRow: { flexDirection: "row", justifyContent: "flex-start", marginBottom: 12 },
  typingBubble: { flexDirection: "row", gap: 5, backgroundColor: "#1a1a2e", borderRadius: 18, borderBottomLeftRadius: 4, paddingHorizontal: 16, paddingVertical: 14 },
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
    body: { color: c.textPrimary, fontSize: 14, lineHeight: 22 },
    heading1: { color: c.textPrimary, fontSize: 20, fontWeight: "700" as const, marginBottom: 6, marginTop: 12 },
    heading2: { color: c.textPrimary, fontSize: 17, fontWeight: "700" as const, marginBottom: 4, marginTop: 10 },
    heading3: { color: c.textPrimary, fontSize: 15, fontWeight: "600" as const, marginBottom: 4, marginTop: 8 },
    paragraph: { color: c.textPrimary, marginBottom: 8 },
    strong: { fontWeight: "700" as const, color: c.textPrimary },
    em: { fontStyle: "italic" as const },
    bullet_list: { marginBottom: 6 },
    ordered_list: { marginBottom: 6 },
    list_item: { flexDirection: "row" as const, marginBottom: 3 },
    code_inline: { backgroundColor: c.bgCardElevated || "#1e1e2e", color: "#e879f9", fontFamily: "monospace", fontSize: 13, paddingHorizontal: 5, paddingVertical: 1, borderRadius: 4 },
    fence: { backgroundColor: c.bgCardElevated || "#0a0a14", borderRadius: 8, padding: 12, marginVertical: 6 },
    code_block: { color: "#a5f3fc", fontFamily: "monospace", fontSize: 12, lineHeight: 18 },
    blockquote: { borderLeftWidth: 3, borderLeftColor: c.accent || "#6366f1", paddingLeft: 12, marginVertical: 6, opacity: 0.85 },
    link: { color: c.accent || "#6366f1" },
    hr: { backgroundColor: c.border || "#1e1e2e", height: 1, marginVertical: 10 },
    table: { borderColor: c.border || "#1e1e2e" },
    tr: { borderBottomColor: c.border || "#1e1e2e" },
    th: { color: c.textPrimary, fontWeight: "700" as const, padding: 6 },
    td: { color: c.textPrimary, padding: 6 },
  };
}
