// StudioChatPane.tsx — the RIGHT pane of the tablet Vibe Studio.
//
// A lean chat/composer/live-console pane for tablet vibe sessions. It
// deliberately reuses the app's shared task and console primitives
// instead of re-deriving them:
//   - streamTaskOutput (quic.ts) for the raw runner stdout SSE lane
//   - summarizeRawConsole + AnsiConsoleText for the foldable live console
//   - MessageBubble for user/assistant rows
//   - existing task history as switchable topic cards; continueTask keeps each
//     card in one runner conversation
// The pane talks to the connected box exactly like the Tasks screen does;
// finished consoles fold quietly while live output remains one tap away.

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { summarizeRawConsole } from "../../_core/ansi";
import { AnsiConsoleText } from "../AnsiConsoleText";
import { MessageBubble } from "../MessageBubble";
import { TaskSessionSummary } from "../TaskSessionSummary";
import { quicClient, type QuicClient, type Task } from "../../lib/quic";
import { classifyStreamEnd, planStreamRecovery } from "../../lib/taskStreamRecovery";
import { beginTaskTurn, mergeTaskSnapshot, taskStatusIsTerminal, withObservedTaskStatus } from "../../lib/studioTaskState";
import { groomRunnerTranscript } from "../../lib/runnerTranscript";
import { isTaskPresentationEvent, reduceTaskPresentation } from "../../_core/taskPresentation";
import { useColors } from "../../context/ThemeContext";
import { useDevice } from "../../context/DeviceContext";
import type { ClientSessionSettings } from "../../lib/appVersion";
import { tombstoneAgentTask } from "../../lib/taskSnapshots";
import { markTaskDeleted } from "../../lib/storage";

interface StudioChatPaneProps {
  /** Selected project the vibe prompt runs against (box-side workDir). */
  projectPath?: string;
  projectName?: string;
  onRequestProject?: () => void;
  previewLogs?: readonly string[];
  previewLogsLive?: boolean;
  /** Bubble/sheet host: conversation only, without project/task inventory. */
  compact?: boolean;
  /** Feedback-style host chrome used over a running guest preview. */
  feedbackStyle?: boolean;
  /** Explicit runner/model selected in the preview card. */
  runner?: string;
  model?: string;
  /** Exact coding-machine client. Browser/Hermes previews may render from a
   * different box, so task traffic must never fall through to focused/render
   * singleton state. */
  client?: QuicClient;
  clientConnected?: boolean;
  codingMachineName?: string;
  /** Lets the preview host queue reloads and lock routing while coding. */
  onTaskStateChange?: (task: Task | null) => void;
  onAnyTaskCodingChange?: (coding: boolean) => void;
  onRenderRequested?: () => void;
  initialSessionBehavior?: "resume-last" | "new-session";
  sessionSettings?: ClientSessionSettings;
}

type ChatRow =
  | { kind: "user"; text: string }
  | { kind: "assistant"; text: string }
  | { kind: "system"; text: string };

const visibleVibeText = (text: string) => text.replace(/<!--\s*YAVER_THREAD_TITLE:[\s\S]*?(?:-->|$)/gi, "").trimEnd();
const humanVibeText = (text: string) => visibleVibeText(groomRunnerTranscript(text).body || text);

export function StudioChatPane({
  projectPath,
  projectName,
  onRequestProject,
  previewLogs = [],
  previewLogsLive = false,
  compact = false,
  feedbackStyle = false,
  runner,
  model,
  client = quicClient,
  clientConnected,
  codingMachineName,
  onTaskStateChange,
  onAnyTaskCodingChange,
  onRenderRequested,
  initialSessionBehavior = "resume-last",
  sessionSettings,
}: StudioChatPaneProps) {
  const theme = useColors();
  // Browser-preview Vibing is the React-Native twin of the standalone feedback
  // card. It deliberately uses the same calm white surface regardless of the
  // guest theme; the overlay belongs to Yaver, not to the rendered app.
  const c = feedbackStyle ? {
    ...theme,
    bg: "#f8f8fb",
    bgCard: "#ffffff",
    bgInput: "#f0f0f5",
    surface: "#ffffff",
    surfaceMuted: "#f2f2f7",
    border: "#dedee7",
    borderSubtle: "#e8e8ef",
    textPrimary: "#16161d",
    textSecondary: "#555561",
    textMuted: "#777782",
    textTertiary: "#a0a0aa",
    accent: "#6f58f5",
    accentSoft: "#ebe8ff",
    brandPrimary: "#7568f8",
  } : theme;
  const { activeDevice, connectionStatus } = useDevice();
  const connected = clientConnected ?? (connectionStatus === "connected" && !!activeDevice);
  const taskClient = client;

  const [composerText, setComposerText] = useState("");
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState<string | null>(null);
  const [rows, setRows] = useState<ChatRow[]>([]);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [rawText, setRawText] = useState("");
  const [rawLive, setRawLive] = useState(false);
  const [consoleExpanded, setConsoleExpanded] = useState(false);
  const [previewLogsExpanded, setPreviewLogsExpanded] = useState(false);
  const [lastRawVersion, setLastRawVersion] = useState(0);
  const [loadingTasks, setLoadingTasks] = useState(false);
  const [streamHealth, setStreamHealth] = useState<{ kind: "reattaching" | "lost"; message: string } | null>(null);

  const streamAbortRef = useRef<null | (() => void)>(null);
  const streamRetryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const streamAttemptRef = useRef(0);
  const rawCursorRef = useRef(0);
  const streamGenerationRef = useRef(0);
  const subscribeTaskRef = useRef<(taskId: string, status?: Task["status"], resume?: boolean) => void>(() => {});
  const listScrollRef = useRef<ScrollView>(null);
  const consolePreferenceRef = useRef<boolean | null>(null);
  const projectPathRef = useRef(projectPath);
  const draftingNewTopicRef = useRef(false);

  // Refresh the recent-task list when the pane mounts and after each send.
  const refreshTasks = useCallback(async () => {
    setLoadingTasks(true);
    try {
      const list = await taskClient.listVibeThreads({ projectName, projectPath });
      const recent = list.slice(0, 12);
      setTasks((current) => recent.map((snapshot) => {
        const existing = current.find((task) => task.id === snapshot.id);
        return existing ? mergeTaskSnapshot(existing, snapshot) : snapshot;
      }));
      // The list endpoint is an independent authoritative status probe. This
      // closes the task even if the terminal SSE frame was lost while the
      // browser/relay lane reconnected.
      setActiveTask((current) => {
        if (!current) return current;
        const snapshot = recent.find((task) => task.id === current.id);
        return snapshot ? mergeTaskSnapshot(current, snapshot) : current;
      });
    } catch {
      // keep the last list; the surface is advisory
    } finally {
      setLoadingTasks(false);
    }
  }, [projectName, projectPath, taskClient]);

  useEffect(() => {
    void refreshTasks();
  }, [refreshTasks]);

  // Tear down any live stream when the pane unmounts.
  useEffect(() => {
    return () => {
      streamAbortRef.current?.();
      if (streamRetryTimerRef.current) clearTimeout(streamRetryTimerRef.current);
    };
  }, []);

  const subscribeTask = useCallback((taskId: string, status?: Task["status"], resume = false) => {
    if (streamRetryTimerRef.current) {
      clearTimeout(streamRetryTimerRef.current);
      streamRetryTimerRef.current = null;
    }
    streamAbortRef.current?.();
    if (!resume) {
      streamGenerationRef.current += 1;
      streamAttemptRef.current = 0;
      rawCursorRef.current = 0;
      setRawText("");
      setRawLive(false);
      setLastRawVersion((v) => v + 1);
      setStreamHealth(null);
      consolePreferenceRef.current = null;
      // Console output is supporting evidence, never the primary chat view.
      setConsoleExpanded(false);
    }
    const generation = streamGenerationRef.current;
    // Seed with the retained tail (rawSince=0 → raw_replay full=true) so a
    // just-finished task paints its console immediately.
    streamAbortRef.current = taskClient.streamTaskOutput(
      taskId,
      () => {
        // groomed transcript already lives in the task; we show raw only
      },
      (status) => {
        if (generation !== streamGenerationRef.current) return;
        setRawLive(false);
        setStreamHealth(null);
        setActiveTask((prev) => prev?.id === taskId ? withObservedTaskStatus(prev, status as Task["status"]) : prev);
        setTasks((prev) => prev.map((task) => task.id === taskId ? withObservedTaskStatus(task, status as Task["status"]) : task));
        if (consolePreferenceRef.current === null) setConsoleExpanded(false);
        setLastRawVersion((v) => v + 1);
        void taskClient.getTask(taskId).then((task) => {
          if (generation !== streamGenerationRef.current) return;
          setActiveTask((current) => current?.id === task.id ? mergeTaskSnapshot(current, task) : task);
          setRows([]);
        }).catch(() => {});
        void refreshTasks();
      },
      (evt) => {
        if (!evt || typeof evt.type !== "string") return;
        if (isTaskPresentationEvent(evt)) {
          const apply = (task: Task): Task => task.id === taskId
            ? { ...task, presentation: reduceTaskPresentation(task.presentation ?? [], evt) }
            : task;
          setActiveTask((current) => current ? apply(current) : current);
          setTasks((current) => current.map(apply));
          return;
        }
        if (evt.type === "runtime_render_requested") {
          onRenderRequested?.();
          return;
        }
      },
      {
        rawSince: resume ? rawCursorRef.current : 0,
        onRaw: (text, _offset, full) => {
          if (generation !== streamGenerationRef.current) return;
          if (_offset > 0) rawCursorRef.current = _offset;
          setRawText((prev) => {
            const next = full ? text : prev + text;
            return next.length > 512 * 1024 ? next.slice(next.length - 512 * 1024) : next;
          });
          // `raw_replay` is retained history, not proof that the runner is
          // currently coding. It used to resurrect completed SFMG topics as
          // running every time Vibing reopened. Only a live raw frame may
          // advance queued→running, and terminal state still wins.
          if (!full) {
            streamAttemptRef.current = 0;
            setStreamHealth(null);
            setRawLive(true);
            setActiveTask((prev) => prev?.id === taskId ? withObservedTaskStatus(prev, "running") : prev);
            setTasks((prev) => prev.map((task) => task.id === taskId ? withObservedTaskStatus(task, "running") : task));
            // Do not steal the chat surface when an active runner writes.
          }
          setLastRawVersion((v) => v + 1);
        },
        onEnd: (info) => {
          if (generation !== streamGenerationRef.current || info.cancelled) return;
          setRawLive(false);
          void taskClient.getTask(taskId).then((snapshot) => {
            if (generation !== streamGenerationRef.current) return;
            setActiveTask((current) => current?.id === snapshot.id ? mergeTaskSnapshot(current, snapshot) : snapshot);
            setTasks((current) => current.map((task) => task.id === snapshot.id ? mergeTaskSnapshot(task, snapshot) : task));
            if (taskStatusIsTerminal(snapshot.status)) {
              setStreamHealth(null);
              return;
            }
            const plan = planStreamRecovery({
              end: classifyStreamEnd(info),
              attempt: streamAttemptRef.current,
              cause: info.error,
            });
            if (plan.action === "idle") return;
            if (plan.action === "give-up") {
              setStreamHealth({ kind: "lost", message: plan.message });
              return;
            }
            setStreamHealth({ kind: "reattaching", message: plan.message });
            streamAttemptRef.current += 1;
            streamRetryTimerRef.current = setTimeout(
              () => subscribeTaskRef.current(taskId, snapshot.status, true),
              plan.delayMs,
            );
          }).catch(() => {
            if (generation !== streamGenerationRef.current) return;
            const plan = planStreamRecovery({
              end: classifyStreamEnd(info),
              attempt: streamAttemptRef.current,
              cause: info.error,
            });
            if (plan.action === "reattach") {
              setStreamHealth({ kind: "reattaching", message: plan.message });
              streamAttemptRef.current += 1;
              streamRetryTimerRef.current = setTimeout(
                () => subscribeTaskRef.current(taskId, status, true),
                plan.delayMs,
              );
            } else if (plan.action === "give-up") {
              setStreamHealth({ kind: "lost", message: plan.message });
            }
          });
        },
      },
    );
  }, [onRenderRequested, refreshTasks, taskClient]);
  subscribeTaskRef.current = subscribeTask;

  const runningTask = tasks.find((task) => task.status === "running" || task.status === "queued")
    || (activeTask && (activeTask.status === "running" || activeTask.status === "queued") ? activeTask : undefined);
  // Each topic owns its own runner/tmux seat. Keep polling every live topic for
  // render safety, but only the selected topic locks its own composer.
  const activeTaskRunning = activeTask?.status === "running" || activeTask?.status === "queued";

  // Status reconciliation is deliberately independent from the SSE stream.
  // A stream can stay half-open and never deliver `done`; the cheap list probe
  // keeps the UI from claiming the runner is coding forever.
  useEffect(() => {
    if (!runningTask) return;
    const timer = setInterval(() => void refreshTasks(), 3000);
    return () => clearInterval(timer);
  }, [refreshTasks, runningTask?.id]);

  const handleSend = useCallback(async () => {
    const text = composerText.trim();
    if (!text || sending || !connected || activeTaskRunning) return;
    setComposerText("");
    setSending(true);
    setSendError(null);
    setRows((prev) => [...prev, { kind: "user", text }]);
    try {
      if (activeTask) {
        // A chat stays one task. Creating a fresh /vibing/execute task for every
        // message made the Studio look conversational while discarding context.
        await taskClient.continueTask(activeTask.id, text, undefined, undefined, sessionSettings);
        setActiveTask((prev) => prev ? beginTaskTurn(prev) : prev);
        setTasks((prev) => prev.map((task) => task.id === activeTask.id ? beginTaskTurn(task) : task));
        subscribeTask(activeTask.id, "running");
      } else {
        const result = await taskClient.executeVibingSuggestion(text, projectPath || "", {
          projectName,
          runner,
          model,
          sessionSettings,
        });
        const taskId = (result as any)?.taskId;
        if (taskId) {
          draftingNewTopicRef.current = false;
          const now = Date.now();
          setActiveTask({ id: String(taskId), title: text, description: "", status: "queued", output: [], createdAt: now, updatedAt: now });
          subscribeTask(String(taskId), "queued");
          void taskClient.getTask(String(taskId)).then((task) => {
            setActiveTask(task);
            if (task.turns?.length) setRows([]);
          }).catch(() => {});
        } else {
          setRows((prev) => [...prev, { kind: "system", text: result?.message || "Sent — no task id returned." }]);
        }
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : "Could not send";
      // A rejected prompt was never part of the conversation. Restore it to
      // the composer so retrying does not require retyping it.
      setRows((prev) => prev.slice(0, -1));
      setComposerText(text);
      setSendError(msg);
    } finally {
      setSending(false);
      void refreshTasks();
    }
  }, [activeTask, activeTaskRunning, composerText, connected, model, projectName, projectPath, refreshTasks, runner, sending, sessionSettings, subscribeTask, taskClient]);

  useEffect(() => {
    if (!activeTask?.id || !sessionSettings) return;
    void taskClient.updateTaskSessionSettings(activeTask.id, sessionSettings).catch(() => {});
  }, [activeTask?.id, sessionSettings, taskClient]);

  const resetConversation = useCallback((draftNewTopic = false) => {
    draftingNewTopicRef.current = draftNewTopic;
    streamAbortRef.current?.();
    streamAbortRef.current = null;
    setActiveTask(null);
    setRows([]);
    setRawText("");
    setRawLive(false);
    setSendError(null);
    consolePreferenceRef.current = null;
    setConsoleExpanded(false);
  }, []);

  const handleTaskTap = useCallback(
    (task: Task) => {
      draftingNewTopicRef.current = false;
      setActiveTask(task);
      setRows([]);
      void taskClient.getTask(task.id).then((hydrated) => setActiveTask(hydrated)).catch(() => {});
      subscribeTask(task.id, task.status);
    },
    [subscribeTask, taskClient],
  );

  const removeTask = useCallback(async (task: Task) => {
    const deviceId = task.deviceId || activeDevice?.id;
    if (activeTask?.id === task.id) resetConversation();
    setTasks((prev) => prev.filter((item) => item.id !== task.id));
    void markTaskDeleted(task.id);
    if (!deviceId) return;
    try {
      await tombstoneAgentTask(deviceId, task.id);
      if (taskClient.isConnected) void taskClient.deleteTask(task.id).catch(() => undefined);
    } catch {}
  }, [activeDevice?.id, activeTask?.id, resetConversation, taskClient]);

  const confirmRemoveTask = useCallback((task: Task) => {
    const message = task.status === "running" || task.status === "queued"
      ? "This also stops the coding turn that is still running."
      : "This removes the conversation from your history.";
    if (Platform.OS === "web" && typeof globalThis.confirm === "function") {
      if (globalThis.confirm(`Remove “${task.title || "this topic"}”?\n\n${message}`)) void removeTask(task);
      return;
    }
    Alert.alert("Remove topic?", message, [
      { text: "Cancel", style: "cancel" },
      { text: "Remove", style: "destructive", onPress: () => { void removeTask(task); } },
    ]);
  }, [removeTask]);

  const completeTask = useCallback(async (task: Task) => {
    try {
      await taskClient.completeTask(task.id);
      setTasks((current) => current.map((item) => item.id === task.id ? withObservedTaskStatus(item, "completed") : item));
      setActiveTask((current) => current?.id === task.id ? withObservedTaskStatus(current, "completed") : current);
      void refreshTasks();
    } catch (error) {
      setSendError(error instanceof Error ? error.message : "Could not complete topic");
    }
  }, [refreshTasks, taskClient]);

  const confirmCompleteTask = useCallback((task: Task) => {
    const message = "This explicitly closes the runner/tmux seat and keeps the conversation in history.";
    if (Platform.OS === "web" && typeof globalThis.confirm === "function") {
      if (globalThis.confirm(`Complete “${task.title || "this topic"}”?\n\n${message}`)) void completeTask(task);
      return;
    }
    Alert.alert("Complete topic?", message, [
      { text: "Cancel", style: "cancel" },
      { text: "Complete", onPress: () => { void completeTask(task); } },
    ]);
  }, [completeTask]);

  const isRunning = !!activeTaskRunning;
  const isRenderable = activeTask?.status === "completed" || activeTask?.status === "review";
  const consoleStatus = activeTask?.status === "failed" || activeTask?.status === "stopped"
    ? `○ ${activeTask.status}`
    : isRenderable
      ? "○ done"
      : "○ idle";
  const conversationRows = useMemo<ChatRow[]>(() => {
    const hydrated: ChatRow[] = (activeTask?.turns || []).map((turn) => ({
      kind: turn.role === "user" ? "user" : "assistant",
      text: turn.role === "user" ? turn.content : humanVibeText(turn.content),
    }));
    return [...hydrated, ...rows.map((row) => row.kind === "assistant" ? { ...row, text: humanVibeText(row.text) } : row)];
  }, [activeTask?.turns, rows]);

  // With one saved topic there is no picker: restore it directly as the chat.
  // An explicit New action suppresses this auto-restore until the new prompt
  // creates its task.
  useEffect(() => {
    if (initialSessionBehavior === "resume-last" && tasks.length > 0 && !activeTask && !draftingNewTopicRef.current) {
      handleTaskTap(tasks[0]);
    }
  }, [activeTask, handleTaskTap, initialSessionBehavior, tasks]);
  useEffect(() => {
    if (projectPathRef.current === projectPath) return;
    projectPathRef.current = projectPath;
    resetConversation();
  }, [projectPath, resetConversation]);

  useEffect(() => {
    onTaskStateChange?.(activeTask);
    onAnyTaskCodingChange?.(!!runningTask);
  }, [activeTask, onAnyTaskCodingChange, onTaskStateChange, runningTask]);

  return (
    <View style={[styles.wrap, { backgroundColor: c.bg }]}>
      {/* Header strip: route context. Topics live as cards below on every size. */}
      {!compact ? <View style={[styles.header, { borderBottomColor: c.borderSubtle }]}>
        <View style={styles.headerChips}>
          <View style={[styles.chip, { backgroundColor: connected ? c.successBg : c.surfaceMuted, borderColor: connected ? c.successBorder : c.borderSubtle }]}>
            <View style={[styles.dot, { backgroundColor: connected ? c.success : c.textTertiary }]} />
            <Text style={[styles.chipText, { color: connected ? c.success : c.textMuted }]} numberOfLines={1}>
              {connected ? codingMachineName || activeDevice?.name || "box" : "disconnected"}
            </Text>
          </View>
          {projectName ? (
            <Pressable onPress={onRequestProject} style={[styles.chip, { backgroundColor: c.accentSoft, borderColor: c.borderSubtle }]}>
              <Ionicons name="folder-open" size={12} color={c.accent} />
              <Text style={[styles.chipText, { color: c.accent }]} numberOfLines={1}>
                {projectName}
              </Text>
            </Pressable>
          ) : null}
        </View>
      </View> : null}

      {/* One topic is just a chat. Topic cards earn their height only when
          there is something to switch between. */}
      {tasks.length > 1 ? <View style={[styles.topicRailWrap, { borderBottomColor: c.borderSubtle }]}>
        <ScrollView
          horizontal
          contentContainerStyle={styles.topicRail}
          showsHorizontalScrollIndicator={false}
        >
          <Pressable
            onPress={() => resetConversation(true)}
            style={[styles.newTopicCard, { backgroundColor: c.accentSoft, borderColor: c.borderSubtle }]}
            accessibilityRole="button"
            accessibilityLabel="Start a new topic"
          >
            <Ionicons name="add" size={21} color={c.accent} />
            <Text style={[styles.newTopicText, { color: c.accent }]}>New</Text>
          </Pressable>
          {loadingTasks && tasks.length === 0 ? <ActivityIndicator size="small" color={c.textMuted} style={{ width: 48 }} /> : null}
          {tasks.map((t) => (
            <Pressable
              key={t.id}
              onPress={() => handleTaskTap(t)}
              style={[styles.topicCard, { backgroundColor: c.surface, borderColor: activeTask?.id === t.id ? c.accent : c.borderSubtle }]}
            >
              <View style={styles.topicTopline}>
                <View style={[styles.taskDot, { backgroundColor: t.status === "running" || t.status === "queued" ? c.warn : t.status === "completed" ? c.success : c.textTertiary }]} />
                <Text style={[styles.taskStatus, { color: c.textMuted }]}>{t.status === "completed" ? "done" : t.status}</Text>
                {t.status !== "completed" ? <Pressable onPress={(event) => { event.stopPropagation(); confirmCompleteTask(t); }} hitSlop={8} accessibilityRole="button" accessibilityLabel={`Complete ${t.title || "topic"}`}>
                  <Ionicons name="checkmark" size={15} color={c.textTertiary} />
                </Pressable> : null}
                <Pressable onPress={(event) => { event.stopPropagation(); confirmRemoveTask(t); }} hitSlop={8} accessibilityRole="button" accessibilityLabel={`Remove ${t.title || "topic"}`}>
                  <Ionicons name="close" size={15} color={c.textTertiary} />
                </Pressable>
              </View>
              <Text style={[styles.topicTitle, { color: c.textPrimary }]} numberOfLines={2}>{t.title || "New topic"}</Text>
              <Text style={[styles.topicRoute, { color: c.textMuted }]} numberOfLines={1}>
                {[
                  t.model ? `${t.model}${t.reasoningEffort ? ` · ${t.reasoningEffort}` : ""}` : t.runnerId,
                ].filter(Boolean).join(" · ")}
              </Text>
            </Pressable>
          ))}
        </ScrollView>
      </View> : tasks.length === 1 ? <View style={[styles.singleTopicBar, { borderBottomColor: c.borderSubtle }]}>
        <Pressable style={styles.singleTopicTitleButton} onPress={() => handleTaskTap(tasks[0])} accessibilityRole="button" accessibilityLabel={`Open ${tasks[0].title || "topic"}`}>
          <Text style={[styles.singleTopicTitle, { color: c.textSecondary }]} numberOfLines={1}>{tasks[0].title || "Current topic"}</Text>
        </Pressable>
        <Pressable onPress={() => resetConversation(true)} style={styles.singleTopicAction} accessibilityRole="button" accessibilityLabel="Start a new topic">
          <Ionicons name="add" size={18} color={c.accent} />
        </Pressable>
        {tasks[0].status !== "completed" ? <Pressable onPress={() => confirmCompleteTask(tasks[0])} style={styles.singleTopicAction} accessibilityRole="button" accessibilityLabel={`Complete ${tasks[0].title || "topic"}`}>
          <Ionicons name="checkmark" size={17} color={c.textTertiary} />
        </Pressable> : null}
        <Pressable onPress={() => confirmRemoveTask(tasks[0])} style={styles.singleTopicAction} accessibilityRole="button" accessibilityLabel={`Remove ${tasks[0].title || "topic"}`}>
          <Ionicons name="trash-outline" size={16} color={c.textTertiary} />
        </Pressable>
      </View> : null}

      {/* Conversation / live console */}
      <ScrollView
        ref={listScrollRef}
        style={styles.conversation}
        contentContainerStyle={styles.conversationContent}
        onContentSizeChange={() => listScrollRef.current?.scrollToEnd({ animated: true })}
      >
        {/* Dev-server output belongs beside the preview, not hidden behind its
            loading layer. Keep it folded by default like web UI sections; the
            latest line remains readable without covering the app. */}
        {previewLogsLive || previewLogs.length > 0 ? (
          <View style={[styles.consoleWrap, { borderColor: c.border }]}>
            <Pressable
              onPress={() => setPreviewLogsExpanded((value) => !value)}
              style={({ pressed }) => [styles.consoleToggle, { backgroundColor: c.surface }, pressed && { opacity: 0.7 }]}
              accessibilityRole="button"
              accessibilityLabel={previewLogsExpanded ? "Hide preview logs" : "Show preview logs"}
              accessibilityState={{ expanded: previewLogsExpanded }}
            >
              <Text style={[styles.consoleCaret, { color: c.textMuted }]}>{previewLogsExpanded ? "▼" : "▶"}</Text>
              <Text style={[styles.consoleTitle, { color: c.textSecondary }]}>Logs</Text>
              {!previewLogsExpanded && previewLogs.length > 0 ? (
                <Text style={[styles.previewLogLatest, { color: c.textTertiary }]} numberOfLines={1}>
                  {previewLogs[previewLogs.length - 1]}
                </Text>
              ) : null}
              <Text style={[styles.consoleDot, { color: previewLogsLive ? "#4ade80" : c.textTertiary }]}>
                {previewLogsLive ? "● live" : "○ idle"}
              </Text>
            </Pressable>
            {previewLogsExpanded ? (
              <ScrollView
                style={[styles.previewLogBody, { backgroundColor: c.bgCard, borderTopColor: c.border }]}
                nestedScrollEnabled
                showsVerticalScrollIndicator
              >
                {previewLogs.length > 0 ? (
                  <AnsiConsoleText text={previewLogs.join("\n")} fontSize={11} />
                ) : (
                  <Text style={{ color: c.textTertiary, fontSize: 12 }}>Waiting for dev-server output…</Text>
                )}
              </ScrollView>
            ) : null}
          </View>
        ) : null}

        {conversationRows.length === 0 && !rawText.trim() && !connected ? (
          <Text style={[styles.emptyHint, { color: c.textTertiary }]}>
            Connect a box to start vibing.
          </Text>
        ) : null}
        {activeTask ? <TaskSessionSummary task={activeTask} compact /> : null}
        {conversationRows.map((row, i) => {
          if (row.kind === "user") return <MessageBubble key={i} variant="user" content={row.text} mono />;
          if (row.kind === "assistant") return <MessageBubble key={i} variant="tool" content={row.text} />;
          return <MessageBubble key={i} variant="system" content={row.text} />;
        })}

        {/* Foldable live console — same grammar as the opencode console */}
        {activeTask || rawText.trim() ? (
          <View style={[styles.consoleWrap, { borderColor: c.border }]}>
            <Pressable
              onPress={() => setConsoleExpanded((v) => {
                consolePreferenceRef.current = !v;
                return !v;
              })}
              style={({ pressed }) => [styles.consoleToggle, { backgroundColor: c.surface }, pressed && { opacity: 0.7 }]}
              accessibilityRole="button"
              accessibilityState={{ expanded: consoleExpanded }}
            >
              <Text style={[styles.consoleCaret, { color: c.textMuted }]}>{consoleExpanded ? "▼" : "▶"}</Text>
              <Text style={[styles.consoleTitle, { color: c.textSecondary }]}>Console</Text>
              {rawLive && isRunning ? (
                <Text style={[styles.consoleDot, { color: "#4ade80" }]}>● live</Text>
              ) : (
                <Text style={[styles.consoleDot, { color: activeTask?.status === "failed" ? c.error : c.textTertiary }]}>{consoleStatus}</Text>
              )}
              <Text style={[styles.consoleCount, { color: c.textTertiary }]}>
                {rawText.length > 1024 ? `${Math.round(rawText.length / 1024)} KB` : `${rawText.length} B`}
              </Text>
            </Pressable>
            {consoleExpanded ? (
              <ScrollView
                style={[styles.consoleBody, { backgroundColor: c.bgCard, borderTopColor: c.border }]}
                nestedScrollEnabled
                showsVerticalScrollIndicator
              >
                {/* re-render on rawVersion so streaming frames repaint */}
                {rawText.trim() ? (
                  <AnsiConsoleText key={lastRawVersion} text={visibleVibeText(summarizeRawConsole(rawText, isRunning))} fontSize={11} />
                ) : (
                  <Text style={{ color: c.textTertiary, fontSize: 12 }}>
                    {isRunning ? "Waiting for runner output…" : "No console output was retained for this task."}
                  </Text>
                )}
              </ScrollView>
            ) : null}
          </View>
        ) : null}

        {sending ? (
          <View style={styles.sendingRow}>
            <ActivityIndicator size="small" color={c.accent} />
            <Text style={{ color: c.textMuted, fontSize: 13 }}>Sending…</Text>
          </View>
        ) : null}
        {streamHealth ? (
          <View style={[styles.streamHealth, { borderColor: streamHealth.kind === "lost" ? c.error : c.border, backgroundColor: c.surfaceMuted }]}>
            <Text style={{ color: streamHealth.kind === "lost" ? c.error : c.textSecondary, fontSize: 12, lineHeight: 17, flex: 1 }}>{streamHealth.message}</Text>
            {streamHealth.kind === "lost" && activeTask ? (
              <Pressable
                onPress={() => {
                  streamAttemptRef.current = 0;
                  setStreamHealth(null);
                  subscribeTask(activeTask.id, activeTask.status, true);
                }}
                accessibilityRole="button"
                accessibilityLabel="Reattach live output"
              >
                <Text style={{ color: c.accent, fontWeight: "800", fontSize: 12 }}>Reattach</Text>
              </Pressable>
            ) : null}
          </View>
        ) : null}
        {sendError ? <MessageBubble variant="error" content={sendError} /> : null}
      </ScrollView>

      {/* Composer */}
      <View style={[styles.composer, { borderTopColor: c.borderSubtle }]}>
        <TextInput
          style={[styles.input, { backgroundColor: c.bgInput, color: c.textPrimary, borderColor: c.borderSubtle }]}
          value={composerText}
          onChangeText={setComposerText}
          placeholder={!connected ? "Connect a box first" : isRunning ? "This runner is coding…" : activeTask ? "Continue this topic…" : "Start a new topic…"}
          placeholderTextColor={c.textTertiary}
          multiline
          editable={connected && !isRunning}
          onSubmitEditing={handleSend}
          blurOnSubmit={false}
        />
        <Pressable
          onPress={handleSend}
          disabled={!connected || isRunning || sending || !composerText.trim()}
          style={[styles.sendBtn, { backgroundColor: c.brandPrimary }, (!connected || isRunning || sending || !composerText.trim()) && { opacity: 0.4 }]}
          accessibilityRole="button"
          accessibilityLabel="Send vibe prompt"
        >
          <Ionicons name="arrow-up" size={18} color="#fff" />
        </Pressable>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: { flex: 1, minWidth: 0 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderBottomWidth: StyleSheet.hairlineWidth,
    gap: 8,
  },
  headerChips: { flexDirection: "row", gap: 6, flex: 1, minWidth: 0 },
  chip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 5,
    borderWidth: StyleSheet.hairlineWidth,
    borderRadius: 20,
    paddingHorizontal: 10,
    paddingVertical: 4,
    maxWidth: "48%",
  },
  dot: { width: 6, height: 6, borderRadius: 3 },
  chipText: { fontSize: 12, fontWeight: "700", flexShrink: 1 },
  topicRailWrap: {
    borderBottomWidth: StyleSheet.hairlineWidth,
    minHeight: 100,
  },
  topicRail: { paddingHorizontal: 12, paddingVertical: 10, gap: 9 },
  singleTopicBar: { minHeight: 40, paddingLeft: 12, paddingRight: 8, flexDirection: "row", alignItems: "center", borderBottomWidth: StyleSheet.hairlineWidth },
  singleTopicTitleButton: { flex: 1, minWidth: 0, justifyContent: "center", minHeight: 38 },
  singleTopicTitle: { fontSize: 11, fontWeight: "700" },
  singleTopicAction: { width: 34, height: 34, alignItems: "center", justifyContent: "center", borderRadius: 10 },
  newTopicCard: {
    width: 72,
    minHeight: 78,
    borderRadius: 14,
    borderWidth: 1,
    alignItems: "center",
    justifyContent: "center",
    gap: 4,
  },
  newTopicText: { fontSize: 12, fontWeight: "800" },
  topicCard: {
    width: 168,
    minHeight: 78,
    borderRadius: 14,
    borderWidth: 1.5,
    padding: 10,
    gap: 7,
  },
  topicTopline: { flexDirection: "row", alignItems: "center", gap: 6 },
  taskDot: { width: 7, height: 7, borderRadius: 4 },
  taskStatus: { flex: 1, fontSize: 10, fontWeight: "700", textTransform: "uppercase" },
  topicTitle: { fontSize: 13, lineHeight: 17, fontWeight: "700" },
  topicRoute: { fontSize: 9, lineHeight: 12 },
  conversation: { flex: 1 },
  conversationContent: { padding: 12, gap: 8 },
  emptyHint: { fontSize: 13, textAlign: "center", paddingTop: 24, lineHeight: 20 },
  consoleWrap: { borderWidth: 1, borderRadius: 10, overflow: "hidden", marginTop: 4 },
  consoleToggle: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 12,
    paddingVertical: 9,
  },
  consoleCaret: { fontSize: 10 },
  consoleTitle: { fontSize: 13, fontWeight: "700" },
  previewLogLatest: { flex: 1, minWidth: 0, fontSize: 11, fontFamily: "monospace" },
  consoleDot: { fontSize: 11, fontWeight: "700", marginLeft: "auto" },
  consoleCount: { fontSize: 11 },
  consoleBody: { maxHeight: 280, padding: 10 },
  previewLogBody: { maxHeight: 220, padding: 10, borderTopWidth: StyleSheet.hairlineWidth },
  sendingRow: { flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 6 },
  streamHealth: { flexDirection: "row", alignItems: "center", gap: 10, borderWidth: 1, borderRadius: 10, padding: 10 },
  composer: {
    flexDirection: "row",
    alignItems: "flex-end",
    gap: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
    borderTopWidth: StyleSheet.hairlineWidth,
  },
  input: {
    flex: 1,
    minHeight: 40,
    maxHeight: 120,
    borderRadius: 12,
    borderWidth: 1,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 14,
    lineHeight: 19,
  },
  sendBtn: {
    width: 40,
    height: 40,
    borderRadius: 20,
    alignItems: "center",
    justifyContent: "center",
  },
});
