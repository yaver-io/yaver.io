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
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from 'react-native';
import type { P2PClient } from './P2PClient';
import { SDKVoiceSession, pcmToTempWavURI, isVoiceStreamSupported } from './voice';
import { startPcmRecording, stopPcmRecording, isVoiceCaptureSupported } from './capture';

export type VibeTurnRole = 'user' | 'assistant' | 'status';

type VoiceState = 'idle' | 'recording' | 'uploading' | 'thinking' | 'speaking';

export interface VibeTurn {
  id: string;
  role: VibeTurnRole;
  text: string;
  timestamp: number;
}

interface Props {
  client: P2PClient;
  initialTaskId: string;
  initialUserPrompt: string;
  onClose?: () => void;
  /** Called when the user taps Reload after a task completes — uses
   *  P2PClient.reloadApp() with the active project context. */
  onReload?: () => Promise<void>;
  /** Optional context forwarded to the voice stream so the agent runs
   *  the task against the right project / runner / model. */
  project?: string;
  model?: string;
  runner?: string;
}

export function VibeChatScreen({
  client,
  initialTaskId,
  initialUserPrompt,
  onClose,
  onReload,
  project,
  model,
  runner,
}: Props) {
  const [taskId, setTaskId] = useState(initialTaskId);
  const [turns, setTurns] = useState<VibeTurn[]>(() => [
    {
      id: `user-${Date.now()}`,
      role: 'user',
      text: initialUserPrompt,
      timestamp: Date.now(),
    },
    {
      id: `status-${Date.now()}`,
      role: 'status',
      text: 'starting…',
      timestamp: Date.now(),
    },
  ]);
  const [streamBuffer, setStreamBuffer] = useState('');
  const [status, setStatus] = useState<'running' | 'done' | 'failed'>('running');
  const [followUp, setFollowUp] = useState('');
  const [isResuming, setIsResuming] = useState(false);
  const [isReloading, setIsReloading] = useState(false);
  const scrollRef = useRef<ScrollView | null>(null);
  const abortRef = useRef<(() => void) | null>(null);

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
    let live = true;
    const acc: string[] = [];
    const close = client.streamTaskOutput(
      taskId,
      (line) => {
        if (!live) return;
        // Filter our internal error sentinel from the SSE helper.
        if (line.startsWith('__error__:')) {
          setStatus('failed');
          setStreamBuffer((prev) => prev + (prev ? '\n' : '') + line.slice('__error__:'.length).trim());
          return;
        }
        acc.push(line);
        // Throttle re-renders: flush every ~100ms.
        setStreamBuffer(acc.join('\n'));
      },
      (terminal) => {
        if (!live) return;
        setStatus(terminal === 'completed' ? 'done' : 'failed');
        // Move the buffered stream into a real assistant turn so the
        // user sees a stable render and can scroll back, then clear
        // the buffer for any follow-up.
        setTurns((prev) => {
          const collapsed = acc.join('\n').trim();
          if (!collapsed) return prev.filter((t) => t.role !== 'status');
          const next = prev.filter((t) => t.role !== 'status');
          next.push({
            id: `assistant-${taskId}-${Date.now()}`,
            role: 'assistant',
            text: collapsed,
            timestamp: Date.now(),
          });
          return next;
        });
        setStreamBuffer('');
      },
    );
    abortRef.current = close;
    return () => {
      live = false;
      try { close(); } catch { /* ignore */ }
    };
  }, [client, taskId]);

  // Auto-scroll the transcript when new content lands.
  useEffect(() => {
    const t = setTimeout(() => {
      scrollRef.current?.scrollToEnd({ animated: true });
    }, 50);
    return () => clearTimeout(t);
  }, [streamBuffer, turns]);

  const handleSendFollowUp = useCallback(async () => {
    const text = followUp.trim();
    if (!text || isResuming) return;
    setIsResuming(true);
    // Add user turn immediately for snappy UX.
    setTurns((prev) => [
      ...prev,
      { id: `user-${Date.now()}`, role: 'user', text, timestamp: Date.now() },
      { id: `status-${Date.now()}`, role: 'status', text: 'thinking…', timestamp: Date.now() },
    ]);
    setFollowUp('');
    setStatus('running');
    setStreamBuffer('');
    try {
      await client.resumeTask({ taskId, userPrompt: text });
      // resumeTask reuses the same taskId, so the SSE subscription
      // above will pick up the new output stream automatically. To
      // force a fresh subscription we momentarily flip taskId to a
      // sentinel and back; cleaner than tearing down + re-attaching
      // the SSE manually.
      const same = taskId;
      setTaskId(`${same}#`);
      setTimeout(() => setTaskId(same), 0);
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
  }, [client, followUp, isResuming, taskId]);

  const handleReload = useCallback(async () => {
    if (isReloading || !onReload) return;
    setIsReloading(true);
    try {
      await onReload();
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
  }, [isReloading, onReload]);

  // Probe whether voice is usable: deps present (expo-av + expo-file-
  // system + buffer) AND the agent reports STT/TTS ready. Hide the mic
  // entirely otherwise so users never tap a dead button.
  useEffect(() => {
    let cancelled = false;
    (async () => {
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
  }, [client]);

  useEffect(() => () => { voiceSessionRef.current?.close(); }, []);

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
      Speech.speak?.(headline);
    } catch { /* expo-speech not installed — text remains visible */ }
  }, []);

  const playTTS = useCallback(async (pcm: Uint8Array, sampleRate: number) => {
    try {
      const wavUri = await pcmToTempWavURI(pcm, sampleRate);
      // eslint-disable-next-line @typescript-eslint/no-var-requires
      const { Audio } = require('expo-av');
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

  return (
    <View style={styles.container}>
      <View style={styles.header}>
        <Text style={styles.title}>Vibe</Text>
        {onClose && (
          <TouchableOpacity onPress={onClose} accessibilityLabel="Close vibe chat">
            <Text style={styles.close}>✕</Text>
          </TouchableOpacity>
        )}
      </View>

      <ScrollView
        ref={scrollRef}
        style={styles.transcript}
        contentContainerStyle={styles.transcriptContent}
        keyboardShouldPersistTaps="handled"
      >
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
        {streamBuffer && status === 'running' && (
          <View style={[styles.turn, styles.turnAssistant]}>
            <Text style={styles.turnText}>{streamBuffer}</Text>
          </View>
        )}
        {status === 'running' && (
          <View style={styles.spinnerRow}>
            <ActivityIndicator size="small" color="#9ca3af" />
            <Text style={styles.spinnerText}>working…</Text>
          </View>
        )}
      </ScrollView>

      <View style={styles.footer}>
        {voiceAvailable && voiceState !== 'idle' && (
          <Text style={styles.engineCaption}>
            {voiceState === 'recording' ? 'listening' : voiceState === 'uploading' ? 'sending' : voiceState === 'thinking' ? 'agent working' : 'speaking'}
            {activeEngine ? ` · ${activeEngine}` : ` · ${voiceMode === 'flux' ? 'Flux (Deepgram)' : 'Local (whisper)'}`}
          </Text>
        )}
        <TextInput
          style={styles.input}
          value={followUp}
          onChangeText={setFollowUp}
          placeholder={status === 'running' ? 'wait for the agent…' : 'follow up…'}
          placeholderTextColor="#666"
          editable={status !== 'running' && !isResuming}
          multiline
        />
        <View style={styles.actions}>
          {voiceAvailable && (
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
                (isReloading || status === 'running') && styles.actionBtnDisabled,
              ]}
              onPress={handleReload}
              disabled={isReloading || status === 'running'}
            >
              <Text style={styles.actionText}>
                {isReloading ? 'reloading…' : '⟳ reload'}
              </Text>
            </TouchableOpacity>
          )}
          <TouchableOpacity
            style={[
              styles.actionBtn,
              styles.sendBtn,
              (isResuming || status === 'running' || !followUp.trim()) && styles.actionBtnDisabled,
            ]}
            onPress={handleSendFollowUp}
            disabled={isResuming || status === 'running' || !followUp.trim()}
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
  container: { flex: 1, backgroundColor: '#0a0a0a' },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 16,
    paddingTop: 14,
    paddingBottom: 8,
    borderBottomWidth: 1,
    borderBottomColor: 'rgba(255,255,255,0.08)',
  },
  title: { color: '#fff', fontSize: 17, fontWeight: '600' },
  close: { color: '#9ca3af', fontSize: 18 },
  transcript: { flex: 1 },
  transcriptContent: { padding: 12, paddingBottom: 24 },
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
    backgroundColor: 'rgba(255,255,255,0.06)',
    borderColor: 'rgba(255,255,255,0.10)',
    borderWidth: 1,
    alignSelf: 'flex-start',
  },
  turnStatus: {
    backgroundColor: 'transparent',
    alignSelf: 'flex-start',
    paddingHorizontal: 4,
  },
  turnText: { color: '#f1f5f9', fontSize: 14, lineHeight: 20 },
  spinnerRow: {
    flexDirection: 'row',
    alignItems: 'center',
    marginTop: 8,
    paddingHorizontal: 4,
  },
  spinnerText: { color: '#9ca3af', fontSize: 12, marginLeft: 8 },
  footer: {
    borderTopWidth: 1,
    borderTopColor: 'rgba(255,255,255,0.08)',
    padding: 10,
  },
  input: {
    minHeight: 40,
    maxHeight: 120,
    color: '#f1f5f9',
    fontSize: 14,
    backgroundColor: 'rgba(255,255,255,0.04)',
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
  reloadBtn: { backgroundColor: 'rgba(255,255,255,0.08)' },
  voiceBtn: { backgroundColor: 'rgba(16,185,129,0.18)' },
  voiceBtnActive: { backgroundColor: '#ef4444' },
  engineToggle: { backgroundColor: 'rgba(255,255,255,0.06)', marginRight: 'auto', marginLeft: 0 },
  engineToggleText: { color: '#cbd5e1', fontSize: 12, fontWeight: '600' },
  engineCaption: { color: '#9ca3af', fontSize: 11, marginBottom: 6, marginLeft: 4 },
  sendBtn: { backgroundColor: '#7582f5' },
  actionText: { color: '#fff', fontSize: 13, fontWeight: '600' },
});
