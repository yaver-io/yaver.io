// VibeChatScreen — the converged chat UI for the standalone feedback
// SDK. Mirrors Yaver mobile's Tasks tab + the in-Yaver native pane:
//
//   1. User sees a live SSE transcript of agent stdout (PhaseStatusLine
//      style "searching… / compiling…" while running, full markdown
//      output once it lands).
//   2. User can keep vibing — type a follow-up after the first turn
//      lands and POST a /tasks/{id}/resume to multi-turn the same
//      coding session.
//   3. Reload button at the bottom hits client.reloadApp() so the user
//      can see the change without leaving the chat.
//
// State machine:
//   idle    — empty, waiting for first prompt (handled by parent screen)
//   running — task is live, transcript streams, follow-up disabled
//   done    — task finished, follow-up enabled, Reload prominent
//   failed  — same as done but error tinted

import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from 'react-native';
import type { ClientSessionSettings, P2PClient, TaskRunnerControlCatalog, VibeThreadSummary } from './P2PClient';
import type { DogfoodRenderBehavior } from './dogfoodPolicy';
import { SDKVoiceSession, pcmToTempWavURI, isVoiceStreamSupported } from './voice';
import { prepareNonInterruptingAudioPlayback, startPcmRecording, stopPcmRecording, isVoiceCaptureSupported } from './capture';
import {
  friendlyTaskPresentation,
  isTaskPresentationEvent,
  reduceTaskPresentation,
  type TaskPresentationMessage,
} from './_core/taskPresentation';
import { firstClassTaskConversationTurns, remoteAgentConversationView } from './_core/taskConversation';
import { taskRunnerControlForMessage, taskRunnerControlSuggestions } from './_core/taskRunnerControls';

export type VibeTurnRole = 'user' | 'assistant' | 'status';

type VoiceState = 'idle' | 'recording' | 'uploading' | 'thinking' | 'speaking';
type VibeTaskStatus = 'idle' | 'queued' | 'running' | 'ready' | 'review' | 'completed' | 'failed' | 'stopped';

type AgentQuestion = {
  id: string;
  taskId: string;
  prompt: string;
  header?: string;
  kind: 'text' | 'choice' | 'secret';
  choices?: string[];
};

function statusFromRunner(status?: string): VibeTaskStatus {
  switch (status) {
    case 'queued': case 'running': case 'ready': case 'review':
    case 'completed': case 'failed': case 'stopped': return status;
    default: return status ? 'failed' : 'running';
  }
}

function VibeConversationStatus({ status, presentation, pendingQuestion }: { status: VibeTaskStatus; presentation: TaskPresentationMessage[]; pendingQuestion?: string }) {
  if (status === 'idle') return null;
  const view = remoteAgentConversationView({ status, presentation }, { pendingQuestion });
  const color = view.tone === 'error' ? '#b42318' : view.tone === 'success' ? '#16803a' : view.tone === 'attention' ? '#a15c00' : '#6252e8';
  return (
    <View style={[styles.conversationStatus, { borderColor: `${color}33`, backgroundColor: `${color}0d` }]} accessibilityRole="summary">
      <Text style={[styles.conversationStatusEyebrow, { color }]}>{view.eyebrow}</Text>
      <Text style={styles.conversationStatusTitle}>{view.title}</Text>
      <Text style={styles.conversationStatusDetail}>{view.detail}</Text>
      {view.nextAction ? <Text style={[styles.conversationStatusNext, { color }]}>{view.nextAction}</Text> : null}
    </View>
  );
}

export interface VibeTurn {
  id: string;
  role: VibeTurnRole;
  text: string;
  timestamp: number;
}

interface Props {
  client: P2PClient;
  initialTaskId?: string;
  initialUserPrompt?: string;
  initialStatus?: string;
  initialTurns?: VibeTurn[];
  onClose?: () => void;
  /** Called when the user taps Reload after a task completes — uses
   *  P2PClient.reloadApp() with the active project context. */
  onReload?: () => Promise<void>;
  /** Optional context forwarded to the voice stream so the agent runs
   *  the task against the right project / runner / model. */
  project?: string;
  projectPath?: string;
  model?: string;
  runner?: string;
  /** Show the optional voice/STT controls. Defaults to keyboard-only. */
  voiceInputEnabled?: boolean;
  /** Standalone SDK hosts use this to fold back to the floating Y without
   * destroying the live task subscription or transcript state. */
  onMinimize?: () => void;
  codingMachine?: string;
  renderMachine?: string;
  /** Return to the SDK prompt composer to create a separate task/topic. */
  onNewTopic?: () => void;
  renderBehavior?: DogfoodRenderBehavior;
  onOpenSettings?: () => void;
  onSignOut?: () => Promise<void>;
  sessionSettings: ClientSessionSettings;
}

export function VibeChatScreen({
  client,
  initialTaskId,
  initialUserPrompt,
  initialStatus,
  initialTurns,
  onClose,
  onReload,
  project,
  projectPath,
  model,
  runner,
  voiceInputEnabled = false,
  onMinimize,
  codingMachine,
  renderMachine,
  onNewTopic,
  renderBehavior = 'manual',
  onOpenSettings,
  onSignOut,
  sessionSettings,
}: Props) {
  const [activeTab, setActiveTab] = useState<'chat' | 'settings'>('chat');
  const [taskId, setTaskId] = useState<string | null>(initialTaskId || null);
  const [turns, setTurns] = useState<VibeTurn[]>(() => initialTurns?.length ? initialTurns : initialTaskId ? [
    {
      id: `user-${Date.now()}`,
      role: 'user',
      text: initialUserPrompt || '',
      timestamp: Date.now(),
    },
  ] : []);
  const [streamBuffer, setStreamBuffer] = useState('');
  const [runnerDetails, setRunnerDetails] = useState('');
  const [runnerDetailsOpen, setRunnerDetailsOpen] = useState(false);
  const [streamEpoch, setStreamEpoch] = useState(0);
  const [status, setStatus] = useState<VibeTaskStatus>(() => !initialTaskId ? 'idle' : statusFromRunner(initialStatus));
  const [presentation, setPresentation] = useState<TaskPresentationMessage[]>([]);
  const [pendingQuestion, setPendingQuestion] = useState<AgentQuestion | null>(null);
  const [questionAnswer, setQuestionAnswer] = useState('');
  const [questionError, setQuestionError] = useState('');
  const [streamNotice, setStreamNotice] = useState('');
  const [followUp, setFollowUp] = useState('');
  const [isResuming, setIsResuming] = useState(false);
  const [isReloading, setIsReloading] = useState(false);
  const [reloadQueued, setReloadQueued] = useState(false);
  const [activeModel, setActiveModel] = useState(model || '');
  const [activeReasoningEffort, setActiveReasoningEffort] = useState('');
  const [runnerControlMode, setRunnerControlMode] = useState<'model' | 'exit' | null>(null);
  const [runnerControlCatalog, setRunnerControlCatalog] = useState<TaskRunnerControlCatalog | null>(null);
  const [runnerControlStep, setRunnerControlStep] = useState<'model' | 'effort'>('model');
  const [runnerControlModel, setRunnerControlModel] = useState('');
  const [runnerControlEffort, setRunnerControlEffort] = useState<'low' | 'medium' | 'high' | 'xhigh' | 'max' | 'ultra'>('medium');
  const [runnerControlBusy, setRunnerControlBusy] = useState(false);
  const [runnerControlError, setRunnerControlError] = useState('');

  useEffect(() => {
    if (!taskId) return;
    void client.updateVibeTaskSessionSettings(taskId, sessionSettings).catch(() => {});
  }, [
    client,
    sessionSettings.appName,
    sessionSettings.appVersion,
    sessionSettings.buildNumber,
    sessionSettings.chatEnabled,
    sessionSettings.clientSurface,
    sessionSettings.deviceClass,
    sessionSettings.dogfood,
    sessionSettings.lane,
    sessionSettings.platform,
    sessionSettings.renderEnabled,
    sessionSettings.runtimeMode,
    sessionSettings.surface,
    sessionSettings.usageMode,
    taskId,
  ]);
  const [renderRequested, setRenderRequested] = useState(false);
  const [threads, setThreads] = useState<VibeThreadSummary[]>(() => initialTaskId ? [{
    id: initialTaskId,
    title: initialUserPrompt || 'New topic',
    status: initialStatus || 'running',
  }] : []);
  const scrollRef = useRef<ScrollView | null>(null);
  const abortRef = useRef<(() => void) | null>(null);

  const refreshThreads = useCallback(async () => {
    try {
      const list = await client.listVibeThreads({ projectName: project, projectPath });
      setThreads(list.slice(0, 12));
    } catch { /* history is advisory; keep the active conversation usable */ }
  }, [client, project, projectPath]);

  useEffect(() => { void refreshThreads(); }, [refreshThreads]);

  // ── Voice vibe coding ──────────────────────────────────────────────
  const [voiceState, setVoiceState] = useState<VoiceState>('idle');
  const [voiceAvailable, setVoiceAvailable] = useState(false);
  // "local" = whisper.cpp on the host (free, private); "flux" = Deepgram
  // nova-3 streaming. fluxAvailable gates the toggle on whether the agent
  // has a Deepgram key. activeEngine is echoed back by the agent.
  const [voiceMode, setVoiceMode] = useState<'local' | 'flux'>('local');
  const [fluxAvailable, setFluxAvailable] = useState(false);
  const [activeEngine, setActiveEngine] = useState<string>('');
  const voiceSessionRef = useRef<SDKVoiceSession | null>(null);

  // Subscribe to the current task's SSE stream. Re-runs whenever the
  // taskId changes (resumeTask reuses the same id, so this only fires
  // once per task — which is fine).
  useEffect(() => {
    if (!taskId) return;
    let live = true;
    const acc: string[] = [];
    let semantic: TaskPresentationMessage[] = [];
    const close = client.streamTaskOutput(
      taskId,
      (line) => {
        if (!live) return;
        if (line.includes('YAVER_THREAD_TITLE')) return;
        // Filter our internal error sentinel from the SSE helper.
        if (line.startsWith('__error__:')) {
          setStatus('failed');
          setStreamBuffer((prev) => prev + (prev ? '\n' : '') + line.slice('__error__:'.length).trim());
          return;
        }
        // Transport stdout is evidence, not the assistant's prose. Keep it in
        // the folded details disclosure rather than rebuilding a fake chat turn.
        acc.push(line);
        setRunnerDetails((previous) => (previous + line + '\n').slice(-64 * 1024));
      },
      (terminal) => {
        if (!live) return;
        setStatus(statusFromRunner(terminal));
        // Only a semantic runner message may become an assistant turn. Raw
        // stdout remains under Runner details, including on older agents.
        setTurns((prev) => {
          const assistant = [...friendlyTaskPresentation(semantic)].reverse().find((item) => item.kind === 'message' && item.role === 'assistant');
          const collapsed = assistant?.text.trim();
          if (!collapsed) return prev.filter((t) => t.role !== 'status');
          const next = prev.filter((t) => t.role !== 'status');
          const previousAssistant = next[next.length - 1];
          if (previousAssistant?.role === 'assistant' && previousAssistant.text.trim() === collapsed) return next;
          next.push({
            id: `assistant-${taskId}-${Date.now()}`,
            role: 'assistant',
            text: collapsed,
            timestamp: Date.now(),
          });
          return next;
        });
        setStreamBuffer('');
        setThreads((prev) => prev.map((thread) => thread.id === taskId ? { ...thread, status: terminal } : thread));
        void refreshThreads();
      },
      { onEvent: (event) => {
        if (event.type !== 'runtime_render_requested' && isTaskPresentationEvent(event)) {
          semantic = reduceTaskPresentation(semantic, event);
          setPresentation(semantic);
          const friendly = friendlyTaskPresentation(semantic);
          const assistant = [...friendly].reverse().find((item) => item.kind === 'message' && item.role === 'assistant');
          setStreamBuffer(assistant?.text || '');
          return;
        }
        if (event.type === 'raw' && typeof event.text === 'string') {
          setRunnerDetails((previous) => (previous + event.text).slice(-64 * 1024));
          return;
        }
            if (event.type === 'runtime_render_requested') {
          setRenderRequested(true);
              if (renderBehavior === 'auto-on-request') setReloadQueued(true);
              return;
            }
            if (event.type === 'agent_question' && event.question) {
              setPendingQuestion(event.question as AgentQuestion);
              setQuestionAnswer('');
              setQuestionError('');
              return;
            }
            if (event.type === 'agent_answered' || event.type === 'agent_question_cancelled') {
              setPendingQuestion(null);
              setQuestionAnswer('');
              return;
            }
            if (event.type === 'task_stream_interrupted') {
              setStreamNotice(String(event.message || 'Live updates paused. Reconnecting…'));
              return;
            }
            if (event.type === 'task_stream_restored') {
              setStreamNotice('');
            }
          } },
        );
        abortRef.current = close;
        void client.getPendingTaskQuestion(taskId).then((question) => {
          if (live && question) setPendingQuestion(question);
        });
    return () => {
      live = false;
      try { close(); } catch { /* ignore */ }
    };
  }, [client, refreshThreads, renderBehavior, streamEpoch, taskId]);

  const selectThread = useCallback(async (thread: VibeThreadSummary) => {
    try {
      const task = await client.getVibeThread(thread.id);
      setTaskId(task.id);
      setStatus(statusFromRunner(task.status));
      setPresentation(task.presentation || []);
      setActiveModel(task.model || thread.model || '');
      setActiveReasoningEffort(task.reasoningEffort || thread.reasoningEffort || '');
      setStreamBuffer('');
      setRunnerDetails('');
      setTurns(firstClassTaskConversationTurns(task.turns, task.presentation).map((turn, index) => ({
        id: `${task.id}-${index}`,
        role: turn.role as VibeTurnRole,
        text: turn.content,
        timestamp: Date.now() + index,
      })));
      setPendingQuestion(await client.getPendingTaskQuestion(task.id));
    } catch (error) {
      setTurns((prev) => [...prev, { id: `thread-error-${Date.now()}`, role: 'status', text: error instanceof Error ? error.message : 'Could not open topic', timestamp: Date.now() }]);
    }
  }, [client]);

  const answerQuestion = useCallback(async (answer: string) => {
    if (!taskId || !pendingQuestion || !answer.trim()) return;
    setQuestionError('');
    const result = await client.answerTaskQuestion(taskId, pendingQuestion.id, answer.trim());
    if (!result.ok) {
      setQuestionError(result.error || 'That question is no longer waiting for an answer.');
      return;
    }
    setPendingQuestion(null);
    setQuestionAnswer('');
    setStatus('running');
    setStreamEpoch((value) => value + 1);
  }, [client, pendingQuestion, taskId]);

  const startNewTopic = useCallback(() => {
    abortRef.current?.();
    abortRef.current = null;
    setTaskId(null);
    setTurns([]);
    setStreamBuffer('');
    setRunnerDetails('');
    setRunnerDetailsOpen(false);
    setStatus('idle');
    setPresentation([]);
    setRunnerControlMode(null);
    setRunnerControlCatalog(null);
    setFollowUp('');
    setReloadQueued(false);
    setRenderRequested(false);
    onNewTopic?.();
  }, [onNewTopic]);

  const removeThread = useCallback((thread: VibeThreadSummary) => {
    const message = thread.status === 'running' || thread.status === 'queued'
      ? 'This also stops the coding turn that is still running.'
      : 'This removes the conversation from your history.';
    Alert.alert('Remove topic?', message, [
      { text: 'Cancel', style: 'cancel' },
      { text: 'Remove', style: 'destructive', onPress: () => {
        void client.deleteVibeThread(thread.id).then(() => {
          setThreads((prev) => prev.filter((item) => item.id !== thread.id));
          if (thread.id === taskId) {
            setTaskId(null);
            setTurns([]);
            setStatus('idle');
            startNewTopic();
          }
        }).catch((error) => {
          setTurns((prev) => [...prev, { id: `delete-error-${Date.now()}`, role: 'status', text: error instanceof Error ? error.message : 'Could not remove topic', timestamp: Date.now() }]);
        });
      } },
    ]);
  }, [client, startNewTopic, taskId]);

  // Each topic owns its own runner/tmux seat. A different live topic must not
  // lock this composer or prevent the user from starting another session.
  const codingLocked = status === 'running' || status === 'queued';

  // Auto-scroll the transcript when new content lands.
  useEffect(() => {
    const t = setTimeout(() => {
      scrollRef.current?.scrollToEnd({ animated: true });
    }, 50);
    return () => clearTimeout(t);
  }, [streamBuffer, turns]);

  const openRunnerControl = useCallback(async (mode: 'model' | 'exit') => {
    if (!taskId) {
      setStreamNotice(`Start a task before using /${mode}.`);
      return;
    }
    setRunnerControlMode(mode);
    setRunnerControlCatalog(null);
    setRunnerControlStep('model');
    setRunnerControlError('');
    setRunnerControlBusy(true);
    try {
      const catalog = await client.getTaskRunnerControls(taskId);
      const selected = catalog.model || catalog.models.find((item) => item.isDefault)?.id || catalog.models[0]?.id || '';
      const selectedInfo = catalog.models.find((item) => item.id === selected);
      const defaultEffort = catalog.reasoningEffort || selectedInfo?.defaultReasoningEffort || 'medium';
      setRunnerControlCatalog(catalog);
      setRunnerControlModel(selected);
      setRunnerControlEffort(defaultEffort);
    } catch (error) {
      setRunnerControlError(error instanceof Error ? error.message : String(error));
    } finally {
      setRunnerControlBusy(false);
    }
  }, [client, taskId]);

  const chooseRunnerModel = useCallback(async (nextModel: string) => {
    if (!taskId || !runnerControlCatalog) return;
    setRunnerControlModel(nextModel);
    const selected = runnerControlCatalog.models.find((item) => item.id === nextModel);
    if (runnerControlCatalog.runnerId === 'codex' && (selected?.supportedReasoningEfforts?.length || 0) > 0) {
      setRunnerControlEffort(selected?.defaultReasoningEffort || runnerControlCatalog.reasoningEffort || 'medium');
      setRunnerControlStep('effort');
      return;
    }
    setRunnerControlBusy(true);
    setRunnerControlError('');
    try {
      const result = await client.applyTaskRunnerControl(taskId, { control: 'model', model: nextModel });
      setActiveModel(result.model || nextModel);
      setActiveReasoningEffort(result.reasoningEffort || '');
      setThreads((prev) => prev.map((thread) => thread.id === taskId ? { ...thread, model: result.model || nextModel, reasoningEffort: result.reasoningEffort } : thread));
      setRunnerControlMode(null);
      setStreamNotice(`Model set to ${result.model || nextModel} for the next turn.`);
    } catch (error) {
      setRunnerControlError(error instanceof Error ? error.message : String(error));
    } finally {
      setRunnerControlBusy(false);
    }
  }, [client, runnerControlCatalog, taskId]);

  const chooseRunnerEffort = useCallback(async (effort: 'low' | 'medium' | 'high' | 'xhigh' | 'max' | 'ultra') => {
    if (!taskId || !runnerControlModel) return;
    setRunnerControlBusy(true);
    setRunnerControlError('');
    try {
      const result = await client.applyTaskRunnerControl(taskId, { control: 'model', model: runnerControlModel, reasoningEffort: effort });
      setActiveModel(result.model || runnerControlModel);
      setActiveReasoningEffort(result.reasoningEffort || effort);
      setThreads((prev) => prev.map((thread) => thread.id === taskId ? { ...thread, model: result.model || runnerControlModel, reasoningEffort: result.reasoningEffort || effort } : thread));
      setRunnerControlMode(null);
      setStreamNotice(`Model set to ${result.model || runnerControlModel} · ${result.reasoningEffort || effort} for the next turn.`);
    } catch (error) {
      setRunnerControlError(error instanceof Error ? error.message : String(error));
    } finally {
      setRunnerControlBusy(false);
    }
  }, [client, runnerControlModel, taskId]);

  const confirmRunnerExit = useCallback(async () => {
    if (!taskId) return;
    setRunnerControlBusy(true);
    setRunnerControlError('');
    try {
      const result = await client.applyTaskRunnerControl(taskId, { control: 'exit', confirmed: true });
      setStatus('stopped');
      setThreads((prev) => prev.map((thread) => thread.id === taskId ? { ...thread, status: 'stopped' } : thread));
      setRunnerControlMode(null);
      setStreamNotice(result.status === 'stopped' ? 'Runner session exited.' : 'Runner session was already exited.');
    } catch (error) {
      setRunnerControlError(error instanceof Error ? error.message : String(error));
    } finally {
      setRunnerControlBusy(false);
    }
  }, [client, taskId]);

  const handleSendFollowUp = useCallback(async () => {
    const text = followUp.trim();
    if (!text || isResuming) return;
    const runnerControl = taskRunnerControlForMessage(text);
    if (runnerControl) {
      setFollowUp('');
      await openRunnerControl(runnerControl);
      return;
    }
    if (codingLocked) return;
    setIsResuming(true);
    // Add user turn immediately for snappy UX.
    setTurns((prev) => [
      ...prev,
      { id: `user-${Date.now()}`, role: 'user', text, timestamp: Date.now() },
    ]);
    setFollowUp('');
    setStatus('running');
    setPresentation([]);
    setThreads((prev) => prev.map((thread) => thread.id === taskId ? { ...thread, status: 'running' } : thread));
    setStreamBuffer('');
    try {
      if (taskId) {
        await client.resumeTask({ taskId, userPrompt: text, sessionSettings });
        // resumeTask reuses the same task id; advance the subscription epoch so
        // the existing SSE path reconnects without inventing an invalid id.
        setStreamEpoch((value) => value + 1);
      } else {
        const created = await client.createFeedbackTask({
          userPrompt: text,
          projectName: project,
          projectPath,
          runner,
          model,
          sessionSettings,
        });
        setTaskId(created.taskId);
        setThreads((prev) => [{ id: created.taskId, title: text, status: 'running', runnerId: runner, model }, ...prev]);
      }
    } catch (e) {
      setStatus('failed');
      setTurns((prev) => [
        ...prev.filter((t) => t.role !== 'status'),
        {
          id: `assistant-err-${Date.now()}`,
          role: 'assistant',
          text: `Failed to send follow-up: ${e instanceof Error ? e.message : String(e)}`,
          timestamp: Date.now(),
        },
      ]);
    } finally {
      setIsResuming(false);
    }
  }, [client, codingLocked, followUp, isResuming, model, openRunnerControl, project, projectPath, runner, sessionSettings, taskId]);

  const handleReload = useCallback(async () => {
    if (isReloading || !onReload) return;
    if (codingLocked) {
      setReloadQueued(true);
      return;
    }
    setIsReloading(true);
    try {
      await onReload();
      setRenderRequested(false);
    } catch (e) {
      setTurns((prev) => [
        ...prev,
        {
          id: `assistant-reload-err-${Date.now()}`,
          role: 'assistant',
          text: `Reload failed: ${e instanceof Error ? e.message : String(e)}`,
          timestamp: Date.now(),
        },
      ]);
    } finally {
      setIsReloading(false);
    }
  }, [codingLocked, isReloading, onReload]);

  useEffect(() => {
    if (!reloadQueued || codingLocked || isReloading) return;
    setReloadQueued(false);
    void handleReload();
  }, [codingLocked, handleReload, isReloading, reloadQueued]);

  // Probe whether voice is usable: deps present (expo-av + expo-file-
  // system + buffer) AND the agent reports STT/TTS ready. Hide the mic
  // entirely otherwise so users never tap a dead button.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      if (!voiceInputEnabled) return;
      if (!isVoiceCaptureSupported() || !isVoiceStreamSupported()) return;
      try {
        const res = await fetch(`${client.agentBaseUrl}/voice/status`, { headers: client.voiceAuthHeaders() });
        if (!res.ok) return;
        const body = await res.json();
        if (cancelled) return;
        // Local whisper is always usable when voice is enabled; Flux needs
        // a Deepgram key on the agent. Show the mic if either path works.
        const localOk = !!body?.enabled;
        const fluxOk = !!body?.enabled && !!body?.deepgramSet;
        if (localOk || fluxOk) setVoiceAvailable(true);
        setFluxAvailable(fluxOk);
        if (!localOk && fluxOk) setVoiceMode('flux');
      } catch { /* leave hidden */ }
    })();
    return () => { cancelled = true; };
  }, [client, voiceInputEnabled]);

  useEffect(() => () => {
    voiceSessionRef.current?.close();
    void stopPcmRecording().catch(() => {});
  }, []);

  // Local TTS: the agent streams no audio for "local"/"device" engines,
  // so the client speaks the result text with the device synthesizer.
  // expo-speech is optional — if absent, the text is still shown.
  const speakLocalText = useCallback((text: string) => {
    if (!text) return;
    try {
      // eslint-disable-next-line @typescript-eslint/no-var-requires
      const Speech = require('expo-speech');
      const headline = text.length > 280 ? `${text.slice(0, 280)} — see screen for the rest.` : text;
      Speech.stop?.();
      void prepareNonInterruptingAudioPlayback()
        .catch(() => {})
        .then(() => Speech.speak?.(headline));
    } catch { /* expo-speech not installed — text remains visible */ }
  }, []);

  const playTTS = useCallback(async (pcm: Uint8Array, sampleRate: number) => {
    try {
      const wavUri = await pcmToTempWavURI(pcm, sampleRate);
      // eslint-disable-next-line @typescript-eslint/no-var-requires
      const { Audio } = require('expo-av');
      await prepareNonInterruptingAudioPlayback();
      const { sound } = await Audio.Sound.createAsync({ uri: wavUri }, { shouldPlay: true });
      sound.setOnPlaybackStatusUpdate((st: any) => {
        if (st.didJustFinish) sound.unloadAsync().catch(() => {});
      });
    } catch { /* playback best-effort */ }
  }, []);

  const stopVoiceAndProcess = useCallback(async () => {
    setVoiceState('uploading');
    let uri: string | null = null;
    try {
      uri = await stopPcmRecording();
    } catch (e) {
      setVoiceState('idle');
      setTurns((prev) => [...prev, { id: `status-verr-${Date.now()}`, role: 'status', text: `voice: ${e instanceof Error ? e.message : String(e)}`, timestamp: Date.now() }]);
      return;
    }
    if (!uri) { setVoiceState('idle'); return; }

    const useFlux = voiceMode === 'flux' && fluxAvailable;
    const session = new SDKVoiceSession({
      onProviders: (stt, tts) => setActiveEngine(stt === 'deepgram' ? 'Flux (Deepgram)' : stt === 'local' ? 'Local (whisper)' : stt),
      onTranscriptPartial: (t) => {
        setTurns((prev) => {
          const next = prev.filter((x) => x.id !== 'voice-partial');
          next.push({ id: 'voice-partial', role: 'status', text: `🎙 ${t}`, timestamp: Date.now() });
          return next;
        });
      },
      onTranscriptFinal: (t) => {
        setVoiceState('thinking');
        setTurns((prev) => [
          ...prev.filter((x) => x.id !== 'voice-partial'),
          { id: `user-voice-${Date.now()}`, role: 'user', text: t, timestamp: Date.now() },
          { id: `status-${Date.now()}`, role: 'status', text: 'thinking…', timestamp: Date.now() },
        ]);
      },
      onTaskCreated: (id) => {
        // Hand the chat's SSE subscription the new task so its agent
        // output streams into the transcript exactly like a typed turn.
        if (id) { setStatus('running'); setStreamBuffer(''); setTaskId(id); }
      },
      onTaskResult: (_id, text) => {
        setVoiceState('speaking');
        // Local TTS path: agent sends no audio frames, so speak here.
        if (!useFlux) speakLocalText(text);
      },
      onTTSReady: (pcm, sr) => { void playTTS(pcm, sr); },
      onDone: () => setTimeout(() => setVoiceState('idle'), 1200),
      onError: (msg) => {
        setVoiceState('idle');
        setTurns((prev) => [...prev.filter((x) => x.id !== 'voice-partial'), { id: `status-verr-${Date.now()}`, role: 'status', text: `voice: ${msg}`, timestamp: Date.now() }]);
      },
    });
    voiceSessionRef.current = session;
    try {
      await session.start({
        wsUrl: client.voiceStreamUrl(),
        headers: client.voiceAuthHeaders(),
        project,
        model,
        runner,
        surface: 'feedback-sdk',
        ttsBudget: 280,
        // Local: whisper.cpp on the host + device synth. Flux: Deepgram
        // nova-3 STT + Aura TTS streamed back as PCM.
        sttProvider: useFlux ? 'deepgram' : 'local',
        ttsProvider: useFlux ? 'deepgram' : 'local',
      });
      await session.streamAudioFile(uri, { skipWavHeader: true });
      session.finalize();
    } catch (e) {
      setVoiceState('idle');
      session.close();
      setTurns((prev) => [...prev, { id: `status-verr-${Date.now()}`, role: 'status', text: `voice: ${e instanceof Error ? e.message : String(e)}`, timestamp: Date.now() }]);
    }
  }, [client, project, model, runner, playTTS, voiceMode, fluxAvailable, speakLocalText]);

  const handleVoicePress = useCallback(async () => {
    if (voiceState === 'recording') {
      void stopVoiceAndProcess();
      return;
    }
    if (voiceState !== 'idle') {
      // Mid-flow tap cancels.
      voiceSessionRef.current?.close();
      voiceSessionRef.current = null;
      setVoiceState('idle');
      return;
    }
    try {
      await startPcmRecording();
      setVoiceState('recording');
    } catch (e) {
      setTurns((prev) => [...prev, { id: `status-verr-${Date.now()}`, role: 'status', text: `voice: ${e instanceof Error ? e.message : String(e)}`, timestamp: Date.now() }]);
    }
  }, [voiceState, stopVoiceAndProcess]);

  const voiceLabel: Record<VoiceState, string> = {
    idle: '🎙 speak',
    recording: '■ stop',
    uploading: 'sending…',
    thinking: 'thinking…',
    speaking: 'speaking…',
  };

  const confirmSignOut = useCallback(() => {
    if (!onSignOut) return;
    Alert.alert('Sign out of Yaver?', 'Dogfood will require Yaver sign-in before Chat or Reload can be used again.', [
      { text: 'Cancel', style: 'cancel' },
      { text: 'Sign out', style: 'destructive', onPress: () => { void onSignOut(); } },
    ]);
  }, [onSignOut]);

  return (
    <View style={styles.container}>
      <View style={styles.header}>
        <View>
          <Text style={styles.title}>Chat</Text>
          <TouchableOpacity disabled={!taskId} onPress={() => { void openRunnerControl('model'); }} accessibilityLabel="Change task model">
            <Text style={styles.routeCaption} numberOfLines={1}>
              {[activeModel || model || runner || 'Automatic model', activeReasoningEffort].filter(Boolean).join(' · ')}
            </Text>
          </TouchableOpacity>
        </View>
        <View style={styles.headerActions}>
          {onMinimize ? <TouchableOpacity onPress={onMinimize} accessibilityLabel="Minimize Vibing"><Text style={styles.close}>−</Text></TouchableOpacity> : null}
          {onClose ? <TouchableOpacity onPress={onClose} accessibilityLabel="Close Dogfood chat"><Text style={styles.close}>✕</Text></TouchableOpacity> : null}
        </View>
      </View>

      <View style={styles.tabs} accessibilityRole="tablist">
        {(['chat', 'settings'] as const).map((tab) => (
          <TouchableOpacity key={tab} onPress={() => setActiveTab(tab)} style={[styles.tab, activeTab === tab && styles.tabSelected]} accessibilityRole="tab" accessibilityState={{ selected: activeTab === tab }}>
            <Text style={[styles.tabText, activeTab === tab && styles.tabTextSelected]}>{tab === 'chat' ? 'Chat' : 'Settings'}</Text>
          </TouchableOpacity>
        ))}
      </View>

      {activeTab === 'chat' && threads.length > 0 ? <View style={styles.topicRailWrap}>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.topicRail}>
          <TouchableOpacity style={styles.newTopicCard} onPress={startNewTopic} accessibilityLabel="Start a new topic">
            <Text style={styles.newTopicPlus}>＋</Text>
            <Text style={styles.newTopicText}>New</Text>
          </TouchableOpacity>
          {threads.map((thread) => (
            <TouchableOpacity key={thread.id} style={[styles.topicCard, thread.id === taskId && styles.topicCardSelected]} onPress={() => { void selectThread(thread); }} accessibilityLabel={`Open ${thread.title}`}>
              <View style={styles.topicCardTopline}>
                <View style={[styles.topicDot, (thread.status === 'running' || thread.status === 'queued') && styles.topicDotLive]} />
                <Text style={styles.topicStatus}>{thread.status === 'completed' ? 'done' : thread.status}</Text>
                <TouchableOpacity onPress={() => removeThread(thread)} hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }} accessibilityLabel={`Remove ${thread.title}`}><Text style={styles.topicRemove}>×</Text></TouchableOpacity>
              </View>
              <Text style={styles.topicTitle} numberOfLines={2}>{thread.title}</Text>
              <Text style={styles.topicRoute} numberOfLines={1}>{[thread.model ? [thread.model, thread.reasoningEffort].filter(Boolean).join(' · ') : thread.runnerId, thread.tmuxSession ? 'tmux' : null].filter(Boolean).join(' · ')}</Text>
            </TouchableOpacity>
          ))}
        </ScrollView>
      </View> : null}

      <ScrollView
        ref={scrollRef}
        style={[styles.transcript, activeTab !== 'chat' && styles.hidden]}
        contentContainerStyle={styles.transcriptContent}
        keyboardShouldPersistTaps="handled"
      >
        {turns.length === 0 && status === 'idle' ? (
          <View style={styles.emptyChat}>
            <Text style={styles.emptyChatTitle}>What would you like to change?</Text>
            <Text style={styles.emptyChatText}>Describe it naturally. Yaver will keep this conversation here.</Text>
          </View>
        ) : null}
        <VibeConversationStatus status={status} presentation={presentation} pendingQuestion={pendingQuestion?.prompt} />
        {streamNotice ? (
          <View style={styles.streamNotice} accessibilityRole="alert">
            <Text style={styles.streamNoticeText}>{streamNotice}</Text>
          </View>
        ) : null}
        {runnerControlMode ? (
          <View style={styles.runnerControlCard} accessibilityRole="summary">
            <View style={styles.runnerControlHeader}>
              <View style={{ flex: 1 }}>
                <Text style={styles.runnerControlEyebrow}>{runnerControlMode === 'model' ? '/MODEL' : '/EXIT'}</Text>
                <Text style={styles.runnerControlTitle}>
                  {runnerControlMode === 'model'
                    ? runnerControlStep === 'effort' ? `Reasoning for ${runnerControlModel}` : 'Choose this conversation’s model'
                    : 'Exit this runner session?'}
                </Text>
                {runnerControlCatalog ? <Text style={styles.runnerControlMeta}>{runnerControlCatalog.runnerId} · {runnerControlCatalog.modelSource || 'this machine'}</Text> : null}
              </View>
              <TouchableOpacity onPress={() => setRunnerControlMode(null)} accessibilityLabel="Close runner control"><Text style={styles.runnerControlClose}>×</Text></TouchableOpacity>
            </View>
            {runnerControlBusy ? <View style={styles.runnerControlLoading}><ActivityIndicator size="small" color="#6252e8" /><Text style={styles.runnerControlMeta}>Checking the task’s machine…</Text></View> : null}
            {runnerControlError ? <Text style={styles.runnerControlError}>{runnerControlError}</Text> : null}
            {!runnerControlBusy && runnerControlMode === 'exit' ? (
              <>
                <Text style={styles.runnerControlBody}>This stops the live runner seat for this conversation. Your readable chat history stays here.</Text>
                <View style={styles.runnerControlActions}>
                  <TouchableOpacity style={styles.runnerControlSecondary} onPress={() => setRunnerControlMode(null)}><Text style={styles.runnerControlSecondaryText}>Keep session</Text></TouchableOpacity>
                  <TouchableOpacity style={styles.runnerControlDanger} onPress={() => { void confirmRunnerExit(); }}><Text style={styles.runnerControlDangerText}>Exit session</Text></TouchableOpacity>
                </View>
              </>
            ) : null}
            {!runnerControlBusy && runnerControlMode === 'model' && runnerControlCatalog && runnerControlStep === 'model' ? (
              <>
                {runnerControlCatalog.models.map((item) => (
                  <TouchableOpacity key={item.id} disabled={runnerControlCatalog.isAdopted} style={[styles.runnerControlOption, item.id === runnerControlModel && styles.runnerControlOptionSelected, runnerControlCatalog.isAdopted && { opacity: 0.45 }]} onPress={() => { void chooseRunnerModel(item.id); }} accessibilityLabel={`Use ${item.name}`}>
                    <View style={{ flex: 1 }}>
                      <Text style={styles.runnerControlOptionTitle}>{item.name || item.id}</Text>
                      <Text style={styles.runnerControlMeta}>{item.id}{item.description ? ` · ${item.description}` : ''}</Text>
                    </View>
                    {item.id === runnerControlCatalog.model ? <Text style={styles.runnerControlCurrent}>current</Text> : null}
                  </TouchableOpacity>
                ))}
                {runnerControlCatalog.models.length === 0 ? <Text style={styles.runnerControlError}>This runner did not return a model catalog.</Text> : null}
              </>
            ) : null}
            {!runnerControlBusy && runnerControlMode === 'model' && runnerControlCatalog && runnerControlStep === 'effort' ? (
              <>
                {(runnerControlCatalog.models.find((item) => item.id === runnerControlModel)?.supportedReasoningEfforts || []).map((item) => (
                  <TouchableOpacity key={item.reasoningEffort} disabled={runnerControlCatalog.isAdopted} style={[styles.runnerControlOption, item.reasoningEffort === runnerControlEffort && styles.runnerControlOptionSelected, runnerControlCatalog.isAdopted && { opacity: 0.45 }]} onPress={() => { void chooseRunnerEffort(item.reasoningEffort); }} accessibilityLabel={`Use ${item.reasoningEffort} reasoning`}>
                    <View style={{ flex: 1 }}>
                      <Text style={styles.runnerControlOptionTitle}>{item.reasoningEffort === 'xhigh' ? 'Extra high' : item.reasoningEffort[0].toUpperCase() + item.reasoningEffort.slice(1)}</Text>
                      {item.description ? <Text style={styles.runnerControlMeta}>{item.description}</Text> : null}
                    </View>
                  </TouchableOpacity>
                ))}
                <TouchableOpacity style={styles.runnerControlBack} onPress={() => setRunnerControlStep('model')}><Text style={styles.runnerControlSecondaryText}>← Models</Text></TouchableOpacity>
              </>
            ) : null}
          </View>
        ) : null}
        {pendingQuestion ? (
          <View style={styles.questionCard} accessibilityRole="summary">
            <Text style={styles.questionEyebrow}>{pendingQuestion.header || 'NEEDS YOUR ANSWER'}</Text>
            <Text style={styles.questionPrompt}>{pendingQuestion.prompt}</Text>
            {(pendingQuestion.choices || []).map((choice) => (
              <TouchableOpacity key={choice} style={styles.questionChoice} onPress={() => { void answerQuestion(choice); }} accessibilityLabel={`Answer ${choice}`}>
                <Text style={styles.questionChoiceText}>{choice}</Text>
              </TouchableOpacity>
            ))}
            <TextInput
              style={styles.questionInput}
              value={questionAnswer}
              onChangeText={setQuestionAnswer}
              placeholder={pendingQuestion.kind === 'secret' ? 'Enter securely…' : 'Type an answer…'}
              secureTextEntry={pendingQuestion.kind === 'secret'}
              placeholderTextColor="#888"
            />
            <TouchableOpacity style={styles.questionSubmit} onPress={() => { void answerQuestion(questionAnswer); }} disabled={!questionAnswer.trim()}>
              <Text style={styles.questionSubmitText}>Answer and continue</Text>
            </TouchableOpacity>
            {questionError ? <Text style={styles.questionError}>{questionError}</Text> : null}
          </View>
        ) : null}
        {turns.map((turn) => (
          <View
            key={turn.id}
            style={[
              styles.turn,
              turn.role === 'user' && styles.turnUser,
              turn.role === 'assistant' && styles.turnAssistant,
              turn.role === 'status' && styles.turnStatus,
            ]}
          >
            <Text style={styles.turnText}>{turn.text}</Text>
          </View>
        ))}
        {/* Live streaming buffer rendered as a single trailing
            assistant block while the task is running. Once the task
            terminates the stream is moved into a real turn (above)
            and this block clears. */}
        {streamBuffer && codingLocked && (
          <View style={[styles.turn, styles.turnAssistant]}>
            <Text style={styles.turnText}>{streamBuffer}</Text>
          </View>
        )}
        {codingLocked && (
          <View style={styles.spinnerRow}>
            <ActivityIndicator size="small" color="#9ca3af" />
            <Text style={styles.spinnerText}>working…</Text>
          </View>
        )}
        {runnerDetails ? (
          <View style={styles.runnerDetailsWrap}>
            <TouchableOpacity onPress={() => setRunnerDetailsOpen((value) => !value)} accessibilityRole="button">
              <Text style={styles.runnerDetailsToggle}>{runnerDetailsOpen ? 'Hide runner details' : 'Runner details'}</Text>
            </TouchableOpacity>
            {runnerDetailsOpen ? <Text style={styles.runnerDetailsText}>{runnerDetails}</Text> : null}
          </View>
        ) : null}
      </ScrollView>

      <ScrollView style={[styles.settings, activeTab !== 'settings' && styles.hidden]} contentContainerStyle={styles.settingsContent}>
        <Text style={styles.settingsLabel}>Setup</Text>
        <View style={styles.settingsCard}>
          <Text style={styles.settingsTitle}>{runner || 'Automatic runner'}</Text>
          <Text style={styles.settingsMeta}>{codingMachine || 'Selected remote box'}</Text>
          {project ? <Text style={styles.settingsMeta}>{project}</Text> : null}
          {onOpenSettings ? <TouchableOpacity style={styles.secondarySettingsButton} onPress={onOpenSettings} accessibilityLabel="Change Dogfood setup">
            <Text style={styles.secondarySettingsButtonText}>Change box, runner, or project</Text>
          </TouchableOpacity> : null}
        </View>
        {onReload ? <><Text style={styles.settingsLabel}>Preview</Text>
        <View style={styles.settingsCard}>
          <Text style={styles.settingsTitle}>Reload rendered app</Text>
          <Text style={styles.settingsMeta}>{reloadQueued ? 'Render queued. It will run when coding finishes.' : renderRequested ? 'UI updates are ready. Render when you want to see them.' : codingLocked ? 'Rendering stays separate while this coding turn runs.' : 'Apply the latest output without ending this chat.'}</Text>
          <TouchableOpacity style={[styles.settingsButton, isReloading && styles.actionBtnDisabled]} onPress={handleReload} disabled={isReloading}>
            <Text style={styles.settingsButtonText}>{isReloading ? 'Rendering…' : reloadQueued ? 'Render Queued' : codingLocked ? 'Queue Render' : renderRequested ? 'Render updates' : 'Render'}</Text>
          </TouchableOpacity>
        </View></> : null}
        {onMinimize ? <TouchableOpacity style={styles.returnButton} onPress={onMinimize} accessibilityLabel="Return to app and keep Vibing running"><Text style={styles.returnButtonText}>Return to App</Text></TouchableOpacity> : null}
        {onSignOut ? <TouchableOpacity style={styles.signOutButton} onPress={confirmSignOut} accessibilityLabel="Sign out of Yaver"><Text style={styles.signOutButtonText}>Sign out of Yaver</Text></TouchableOpacity> : null}
      </ScrollView>

      <View style={[styles.footer, activeTab !== 'chat' && styles.hidden]}>
        {voiceInputEnabled && voiceAvailable && voiceState !== 'idle' && (
          <Text style={styles.engineCaption}>
            {voiceState === 'recording' ? 'listening' : voiceState === 'uploading' ? 'sending' : voiceState === 'thinking' ? 'agent working' : 'speaking'}
            {activeEngine ? ` · ${activeEngine}` : ` · ${voiceMode === 'flux' ? 'Flux (Deepgram)' : 'Local (whisper)'}`}
          </Text>
        )}
        {taskRunnerControlSuggestions(followUp).length ? (
          <View style={styles.runnerCommandMenu} accessibilityRole="menu" accessibilityLabel="Task commands">
            {taskRunnerControlSuggestions(followUp).map((item) => (
              <TouchableOpacity
                key={item.command}
                style={[styles.runnerCommandItem, item.destructive && styles.runnerCommandItemDanger]}
                accessibilityRole="button"
                accessibilityLabel={`${item.command}, ${item.label}`}
                onPress={() => {
                  setFollowUp('');
                  void openRunnerControl(item.control);
                }}
              >
                <Text style={[styles.runnerCommandCode, item.destructive && styles.runnerCommandCodeDanger]}>{item.command}</Text>
                <View style={styles.runnerCommandCopy}>
                  <Text style={styles.runnerCommandLabel}>{item.label}</Text>
                  <Text style={styles.runnerCommandDescription}>{item.description}</Text>
                </View>
              </TouchableOpacity>
            ))}
          </View>
        ) : null}
        <TextInput
          style={styles.input}
          value={followUp}
          onChangeText={setFollowUp}
          placeholder={codingLocked ? 'Yaver is working…' : taskId ? 'Follow up…' : 'What would you like to change?'}
          placeholderTextColor="#666"
          editable={!codingLocked && !isResuming}
          multiline
        />
        <View style={styles.actions}>
          {voiceInputEnabled && voiceAvailable && (
            <>
              {/* Local ↔ Flux engine toggle. Only shows Flux when the
                  agent has a Deepgram key; otherwise the label just
                  states "Local" so the active engine is always clear. */}
              {fluxAvailable ? (
                <TouchableOpacity
                  style={[styles.actionBtn, styles.engineToggle]}
                  onPress={() => setVoiceMode((m) => (m === 'local' ? 'flux' : 'local'))}
                  disabled={voiceState !== 'idle'}
                  accessibilityLabel="Toggle voice engine"
                >
                  <Text style={styles.engineToggleText}>{voiceMode === 'flux' ? '⚡ Flux' : '🔒 Local'}</Text>
                </TouchableOpacity>
              ) : (
                <View style={[styles.actionBtn, styles.engineToggle]}>
                  <Text style={styles.engineToggleText}>🔒 Local</Text>
                </View>
              )}
              <TouchableOpacity
                style={[
                  styles.actionBtn,
                  styles.voiceBtn,
                  voiceState === 'recording' && styles.voiceBtnActive,
                  (voiceState === 'uploading' || voiceState === 'thinking' || voiceState === 'speaking') && styles.actionBtnDisabled,
                ]}
                onPress={handleVoicePress}
                disabled={voiceState === 'uploading' || voiceState === 'thinking' || voiceState === 'speaking'}
                accessibilityLabel="Vibe code by voice"
              >
                <Text style={styles.actionText}>{voiceLabel[voiceState]}</Text>
              </TouchableOpacity>
            </>
          )}
          {onReload && (
            <TouchableOpacity
              style={[
                styles.actionBtn,
                styles.reloadBtn,
                isReloading && styles.actionBtnDisabled,
              ]}
              onPress={handleReload}
              disabled={isReloading}
            >
              <Text style={styles.actionText}>
                {isReloading ? 'rendering…' : codingLocked ? 'Queue render' : renderRequested ? 'Render updates' : '⟳ render'}
              </Text>
            </TouchableOpacity>
          )}
          <TouchableOpacity
            style={[
              styles.actionBtn,
              styles.sendBtn,
              (isResuming || codingLocked || !followUp.trim()) && styles.actionBtnDisabled,
            ]}
            onPress={handleSendFollowUp}
            disabled={isResuming || codingLocked || !followUp.trim()}
          >
            <Text style={styles.actionText}>
              {isResuming ? '…' : '↑ send'}
            </Text>
          </TouchableOpacity>
        </View>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#f8f8fb' },
  runnerCommandMenu: { gap: 7, marginBottom: 8 },
  runnerCommandItem: { minHeight: 48, borderWidth: 1, borderColor: '#dedde8', backgroundColor: '#fff', borderRadius: 12, paddingHorizontal: 12, paddingVertical: 8, flexDirection: 'row', alignItems: 'center', gap: 10 },
  runnerCommandItemDanger: { borderColor: '#f2b8b5' },
  runnerCommandCode: { color: '#6252e8', fontFamily: 'monospace', fontWeight: '800' },
  runnerCommandCodeDanger: { color: '#b42318' },
  runnerCommandCopy: { flex: 1 },
  runnerCommandLabel: { color: '#23212d', fontSize: 13, fontWeight: '700' },
  runnerCommandDescription: { color: '#716f7a', fontSize: 11, marginTop: 1 },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 16,
    paddingTop: 14,
    paddingBottom: 8,
    borderBottomWidth: 1,
    borderBottomColor: '#e5e5ec',
  },
  title: { color: '#17171d', fontSize: 17, fontWeight: '700' },
  routeCaption: { color: '#858590', fontSize: 10, marginTop: 2, maxWidth: 250 },
  headerActions: { flexDirection: 'row', alignItems: 'center', gap: 22 },
  close: { color: '#656570', fontSize: 20, fontWeight: '700' },
  tabs: { flexDirection: 'row', gap: 6, marginHorizontal: 12, marginTop: 8, padding: 3, borderRadius: 12, backgroundColor: '#ededf3' },
  tab: { flex: 1, minHeight: 34, borderRadius: 9, alignItems: 'center', justifyContent: 'center' },
  tabSelected: { backgroundColor: '#fff' },
  tabText: { color: '#858590', fontSize: 12, fontWeight: '700' },
  tabTextSelected: { color: '#6252e8' },
  topicRailWrap: { minHeight: 102, borderBottomWidth: 1, borderBottomColor: '#e5e5ec' },
  topicRail: { paddingHorizontal: 12, paddingVertical: 10, gap: 9 },
  newTopicCard: { width: 72, minHeight: 80, borderRadius: 14, alignItems: 'center', justifyContent: 'center', backgroundColor: '#ebe8ff', borderWidth: 1, borderColor: '#d9d3ff' },
  newTopicPlus: { color: '#6252e8', fontSize: 22, lineHeight: 24 },
  newTopicText: { color: '#6252e8', fontSize: 12, fontWeight: '800' },
  topicCard: { width: 172, minHeight: 80, borderRadius: 14, padding: 10, gap: 7, backgroundColor: '#fff', borderWidth: 1.5, borderColor: '#e1e1e8' },
  topicCardSelected: { borderColor: '#7568f8', backgroundColor: '#faf9ff' },
  topicCardTopline: { flexDirection: 'row', alignItems: 'center', gap: 6 },
  topicDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: '#9ca3af' },
  topicDotLive: { backgroundColor: '#f59e0b' },
  topicStatus: { flex: 1, color: '#858590', fontSize: 10, fontWeight: '700', textTransform: 'uppercase' },
  topicRemove: { color: '#9a9aa5', fontSize: 18, lineHeight: 18 },
  topicTitle: { color: '#24242b', fontSize: 13, lineHeight: 17, fontWeight: '700' },
  topicRoute: { color: '#858590', fontSize: 9, lineHeight: 12 },
  transcript: { flex: 1 },
  transcriptContent: { padding: 12, paddingBottom: 24 },
  conversationStatus: { borderWidth: 1, borderRadius: 14, padding: 12, marginBottom: 10 },
  conversationStatusEyebrow: { fontSize: 10, fontWeight: '900', letterSpacing: 0.7 },
  conversationStatusTitle: { color: '#24242b', fontSize: 15, lineHeight: 20, fontWeight: '800', marginTop: 3 },
  conversationStatusDetail: { color: '#656570', fontSize: 13, lineHeight: 18, marginTop: 5 },
  conversationStatusNext: { fontSize: 12, lineHeight: 17, fontWeight: '700', marginTop: 8 },
  streamNotice: { borderRadius: 12, borderWidth: 1, borderColor: '#d69e2e55', backgroundColor: '#fff8e6', padding: 10, marginBottom: 10 },
  streamNoticeText: { color: '#7a4d00', fontSize: 12, lineHeight: 17 },
  questionCard: { borderRadius: 14, borderWidth: 1, borderColor: '#d69e2e66', backgroundColor: '#fffaf0', padding: 12, marginBottom: 10 },
  questionEyebrow: { color: '#a15c00', fontSize: 10, fontWeight: '900', letterSpacing: 0.7 },
  questionPrompt: { color: '#24242b', fontSize: 14, lineHeight: 20, fontWeight: '700', marginTop: 5, marginBottom: 8 },
  questionChoice: { minHeight: 42, justifyContent: 'center', borderRadius: 10, borderWidth: 1, borderColor: '#e2c27a', backgroundColor: '#fff', paddingHorizontal: 11, marginTop: 6 },
  questionChoiceText: { color: '#513500', fontSize: 13, fontWeight: '700' },
  questionInput: { minHeight: 42, borderRadius: 10, borderWidth: 1, borderColor: '#ddd', backgroundColor: '#fff', color: '#24242b', paddingHorizontal: 11, marginTop: 8 },
  questionSubmit: { minHeight: 42, alignItems: 'center', justifyContent: 'center', borderRadius: 10, backgroundColor: '#a15c00', marginTop: 8 },
  questionSubmitText: { color: '#fff', fontSize: 13, fontWeight: '800' },
  questionError: { color: '#b42318', fontSize: 12, lineHeight: 17, marginTop: 7 },
  runnerControlCard: { borderRadius: 14, borderWidth: 1, borderColor: '#6252e855', backgroundColor: '#fff', padding: 12, marginBottom: 10, gap: 8 },
  runnerControlHeader: { flexDirection: 'row', alignItems: 'flex-start', gap: 10 },
  runnerControlEyebrow: { color: '#6252e8', fontSize: 10, fontWeight: '900', letterSpacing: 0.7 },
  runnerControlTitle: { color: '#24242b', fontSize: 15, lineHeight: 20, fontWeight: '800', marginTop: 3 },
  runnerControlMeta: { color: '#747480', fontSize: 11, lineHeight: 16, marginTop: 2 },
  runnerControlClose: { color: '#747480', fontSize: 22, lineHeight: 24, fontWeight: '700' },
  runnerControlLoading: { minHeight: 42, flexDirection: 'row', alignItems: 'center', gap: 8 },
  runnerControlError: { color: '#b42318', fontSize: 12, lineHeight: 17 },
  runnerControlBody: { color: '#4f4f59', fontSize: 13, lineHeight: 19 },
  runnerControlActions: { flexDirection: 'row', gap: 8, marginTop: 4 },
  runnerControlSecondary: { flex: 1, minHeight: 42, alignItems: 'center', justifyContent: 'center', borderRadius: 10, borderWidth: 1, borderColor: '#d8d8df', backgroundColor: '#fff' },
  runnerControlSecondaryText: { color: '#555560', fontSize: 13, fontWeight: '800' },
  runnerControlDanger: { flex: 1, minHeight: 42, alignItems: 'center', justifyContent: 'center', borderRadius: 10, backgroundColor: '#b42318' },
  runnerControlDangerText: { color: '#fff', fontSize: 13, fontWeight: '800' },
  runnerControlOption: { minHeight: 52, flexDirection: 'row', alignItems: 'center', gap: 8, borderRadius: 11, borderWidth: 1, borderColor: '#e3e3e9', backgroundColor: '#fafafd', paddingHorizontal: 11, paddingVertical: 8 },
  runnerControlOptionSelected: { borderColor: '#6252e899', backgroundColor: '#6252e80d' },
  runnerControlOptionTitle: { color: '#24242b', fontSize: 13, fontWeight: '800' },
  runnerControlCurrent: { color: '#6252e8', fontSize: 10, fontWeight: '900', textTransform: 'uppercase' },
  runnerControlBack: { minHeight: 40, justifyContent: 'center', alignSelf: 'flex-start', paddingHorizontal: 5 },
  emptyChat: { flex: 1, minHeight: 180, alignItems: 'center', justifyContent: 'center', paddingHorizontal: 30 },
  emptyChatTitle: { color: '#24242b', fontSize: 18, fontWeight: '800', textAlign: 'center' },
  emptyChatText: { color: '#858590', fontSize: 13, lineHeight: 19, textAlign: 'center', marginTop: 7 },
  turn: {
    marginVertical: 4,
    padding: 10,
    borderRadius: 12,
    maxWidth: '92%',
  },
  turnUser: {
    backgroundColor: '#7582f5',
    alignSelf: 'flex-end',
  },
  turnAssistant: {
    backgroundColor: '#fff',
    borderColor: '#e4e4ea',
    borderWidth: 1,
    alignSelf: 'flex-start',
  },
  turnStatus: {
    backgroundColor: 'transparent',
    alignSelf: 'flex-start',
    paddingHorizontal: 4,
  },
  turnText: { color: '#28282f', fontSize: 14, lineHeight: 20 },
  spinnerRow: {
    flexDirection: 'row',
    alignItems: 'center',
    marginTop: 8,
    paddingHorizontal: 4,
  },
  spinnerText: { color: '#9ca3af', fontSize: 12, marginLeft: 8 },
  runnerDetailsWrap: { marginTop: 12, borderTopWidth: 1, borderTopColor: '#e5e5ec', paddingTop: 10 },
  runnerDetailsToggle: { color: '#656570', fontSize: 12, fontWeight: '700' },
  runnerDetailsText: { marginTop: 8, color: '#5f5f69', backgroundColor: '#f0f0f4', borderRadius: 10, padding: 10, fontSize: 11, lineHeight: 16, fontFamily: 'monospace' },
  footer: {
    borderTopWidth: 1,
    borderTopColor: '#e5e5ec',
    padding: 10,
  },
  input: {
    minHeight: 40,
    maxHeight: 120,
    color: '#28282f',
    fontSize: 14,
    backgroundColor: '#fff',
    borderWidth: 1,
    borderColor: '#e2e2e9',
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  actions: {
    flexDirection: 'row',
    justifyContent: 'flex-end',
    marginTop: 8,
  },
  actionBtn: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 10,
    marginLeft: 8,
  },
  actionBtnDisabled: { opacity: 0.5 },
  reloadBtn: { backgroundColor: '#e9e9ef' },
  voiceBtn: { backgroundColor: 'rgba(16,185,129,0.18)' },
  voiceBtnActive: { backgroundColor: '#ef4444' },
  engineToggle: { backgroundColor: '#e9e9ef', marginRight: 'auto', marginLeft: 0 },
  engineToggleText: { color: '#555561', fontSize: 12, fontWeight: '600' },
  engineCaption: { color: '#9ca3af', fontSize: 11, marginBottom: 6, marginLeft: 4 },
  sendBtn: { backgroundColor: '#7582f5' },
  actionText: { color: '#fff', fontSize: 13, fontWeight: '600' },
  settings: { flex: 1 },
  settingsContent: { padding: 14, gap: 10, paddingBottom: 30 },
  settingsLabel: { color: '#777782', fontSize: 11, fontWeight: '800', textTransform: 'uppercase', letterSpacing: 0.5, marginTop: 4 },
  settingsCard: { borderRadius: 14, padding: 14, gap: 5, backgroundColor: '#fff', borderWidth: 1, borderColor: '#e3e3e9' },
  settingsTitle: { color: '#222229', fontSize: 14, fontWeight: '800' },
  settingsMeta: { color: '#777782', fontSize: 12, lineHeight: 17 },
  settingsButton: { alignSelf: 'flex-start', marginTop: 8, borderRadius: 10, backgroundColor: '#6f58f5', paddingHorizontal: 14, paddingVertical: 9 },
  settingsButtonText: { color: '#fff', fontSize: 12, fontWeight: '800' },
  secondarySettingsButton: { alignSelf: 'flex-start', marginTop: 8, borderRadius: 10, borderWidth: 1, borderColor: '#d9d6f8', backgroundColor: '#f7f5ff', paddingHorizontal: 12, paddingVertical: 9 },
  secondarySettingsButtonText: { color: '#6252e8', fontSize: 12, fontWeight: '800' },
  returnButton: { minHeight: 46, borderRadius: 13, alignItems: 'center', justifyContent: 'center', backgroundColor: '#6f58f5', marginTop: 4 },
  returnButtonText: { color: '#fff', fontWeight: '800' },
  signOutButton: { minHeight: 44, alignItems: 'center', justifyContent: 'center', marginTop: 2 },
  signOutButtonText: { color: '#b42318', fontSize: 13, fontWeight: '700' },
  hidden: { display: 'none' },
});
