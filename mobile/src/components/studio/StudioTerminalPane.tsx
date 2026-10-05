import React, { useCallback, useEffect, useRef, useState } from "react";
import { Linking, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useRouter } from "expo-router";
import { useAuth } from "../../context/AuthContext";
import { useColors } from "../../context/ThemeContext";
import { useDevice } from "../../context/DeviceContext";
import { quicClient } from "../../lib/quic";
import type { StudioRunner } from "../../lib/studioWorkspace";
import XtermView, { type XtermHandle } from "../XtermView";
import { isTerminalMetaFrame, resizeFrame } from "../../lib/xtermBridge";
import { loadLocalSpeechConfig } from "../../lib/auth";
import {
  DEFAULT_TTS_MODEL,
  DEFAULT_TTS_VOICE,
  speakText,
  startRealtimeTranscribe,
  stopSpeaking,
  transcribe,
} from "../../lib/speech";
import {
  createMicrophoneRecording,
  discardMicrophoneRecording,
  stopMicrophoneRecording,
} from "../../lib/microphoneAudioSession";
import { appendTerminalSpeechText, terminalSpeechExcerpt } from "../../lib/terminalSpeech";

function encodeUtf8(value: string): Uint8Array {
  try { return new TextEncoder().encode(value); } catch {
    return Uint8Array.from(value, (character) => character.charCodeAt(0) & 0xff);
  }
}

// One-tap coding-agent launchers for the Studio SSH workspace. The session is
// already the persistent tmux workspace (`profile_tmux`), so each tool opens a
// NEW tmux window in the same session instead of nesting `tmux new-session`
// (which fails inside tmux). When tmux is missing the tool still runs in the
// current shell. Commands are fixed strings — never user text.
const STUDIO_TOOLS: ReadonlyArray<{ id: string; label: string; command: string }> = [
  { id: "claude", label: "Claude", command: "claude --dangerously-skip-permissions" },
  { id: "codex", label: "Codex", command: "codex --dangerously-bypass-approvals-and-sandbox" },
  { id: "opencode", label: "OpenCode", command: "opencode --auto" },
];

function studioToolCommand(command: string): string {
  // `#{pane_current_path}` is expanded by tmux, not the shell, so the new
  // window inherits the working directory the user is actually in.
  return `if command -v tmux >/dev/null 2>&1; then tmux new-window -c "#{pane_current_path}" '${command}'; else ${command}; fi`;
}

function terminalUrl(baseUrl: string, token: string, cwd: string | undefined, runner: StudioRunner, tmuxSession: string): string {
  const params = new URLSearchParams({ token, term: "xterm-256color" });
  if (cwd) params.set("cwd", cwd);
  if (runner !== "shell") params.set("launch", runner);
  else {
    params.set("profile_tmux", tmuxSession);
    params.set("profile_shell", "default");
  }
  return `${baseUrl.replace(/^http/, "ws").replace(/\/+$/, "")}/ws/terminal?${params.toString()}`;
}

export function StudioTerminalPane({ cwd, runner, tmuxSession, expanded, onToggleFullscreen }: {
  cwd?: string;
  runner: StudioRunner;
  tmuxSession: string;
  expanded?: boolean;
  onToggleFullscreen?: () => void;
}) {
  const router = useRouter();
  const colors = useColors();
  const { token } = useAuth();
  const { activeDevice, connectionStatus } = useDevice();
  const terminalRef = useRef<XtermHandle | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState<"connecting" | "open" | "closed" | "error">("connecting");
  const [failure, setFailure] = useState<string | null>(null);
  const [voiceFailure, setVoiceFailure] = useState<string | null>(null);
  const [listening, setListening] = useState(false);
  const [speaking, setSpeaking] = useState(false);
  const [fontSize, setFontSize] = useState(13);
  const realtimeRef = useRef<{ stop: () => Promise<string> } | null>(null);
  const recordingRef = useRef<any>(null);
  const speechTailRef = useRef("");
  const decoderRef = useRef(typeof TextDecoder !== "undefined" ? new TextDecoder() : null);
  const fontKey = `yaver:studio:terminal-font:${cwd || "default"}`;

  useEffect(() => {
    AsyncStorage.getItem(fontKey).then((value) => {
      const parsed = Number(value);
      if (Number.isFinite(parsed)) setFontSize(Math.max(9, Math.min(28, parsed)));
    }).catch(() => {});
  }, [fontKey]);

  useEffect(() => {
    terminalRef.current?.setFontSize(fontSize);
  }, [fontSize]);

  const changeFontSize = useCallback((delta: number) => {
    setFontSize((current) => {
      const next = delta === 0 ? 13 : Math.max(9, Math.min(28, current + delta));
      AsyncStorage.setItem(fontKey, String(next)).catch(() => {});
      return next;
    });
  }, [fontKey]);

  useEffect(() => {
    if (!activeDevice || !token || connectionStatus !== "connected") {
      setStatus("closed");
      setFailure("SSH route unavailable. Connect the selected machine, then retry.");
      return;
    }
    let disposed = false;
    setStatus("connecting");
    setFailure(null);
    const socket = new WebSocket(terminalUrl(quicClient.baseUrl, token, cwd, runner, tmuxSession));
    socketRef.current = socket;
    try { (socket as any).binaryType = "arraybuffer"; } catch {}
    socket.onopen = () => {
      if (disposed) return;
      setStatus("open");
      socket.send(resizeFrame(100, 32));
    };
    socket.onmessage = (event: WebSocketMessageEvent) => {
      if (disposed) return;
      if (event.data instanceof ArrayBuffer) {
        const bytes = new Uint8Array(event.data);
        terminalRef.current?.write(bytes);
        const decoded = decoderRef.current?.decode(bytes, { stream: true }) || "";
        speechTailRef.current = appendTerminalSpeechText(speechTailRef.current, decoded);
      }
      else if (typeof event.data === "string" && !isTerminalMetaFrame(event.data)) {
        terminalRef.current?.write(encodeUtf8(event.data));
        speechTailRef.current = appendTerminalSpeechText(speechTailRef.current, event.data);
      }
      else if (typeof event.data === "string") {
        try {
          const frame = JSON.parse(event.data);
          if (typeof frame?.error === "string") setFailure(frame.error);
        } catch {}
      }
    };
    socket.onerror = () => {
      if (disposed) return;
      setStatus("error");
      setFailure("SSH terminal could not reach the selected machine.");
    };
    socket.onclose = (event: WebSocketCloseEvent) => {
      if (disposed) return;
      setStatus("closed");
      if (event.code !== 1000) setFailure(event.reason || `SSH session closed (code ${event.code}).`);
    };
    return () => {
      disposed = true;
      try { socket.close(); } catch {}
      socketRef.current = null;
    };
  }, [activeDevice, attempt, connectionStatus, cwd, runner, tmuxSession, token]);

  const send = useCallback((bytes: Uint8Array) => {
    if (socketRef.current?.readyState === WebSocket.OPEN) socketRef.current.send(bytes);
  }, []);

  const stopListening = useCallback(async () => {
    try {
      let transcript = "";
      if (realtimeRef.current) {
        const controller = realtimeRef.current;
        realtimeRef.current = null;
        transcript = await controller.stop();
      } else if (recordingRef.current) {
        const recording = recordingRef.current;
        recordingRef.current = null;
        const uri = await stopMicrophoneRecording(recording);
        if (!uri) throw new Error("The microphone did not produce an audio clip.");
        const cfg = await loadLocalSpeechConfig();
        transcript = (await transcribe(uri, {
          provider: cfg.sttProvider || "on-device",
          apiKey: cfg.apiKey,
          model: cfg.sttModel,
        })).text;
      }
      const text = transcript.trim();
      if (text) send(encodeUtf8(text));
      else setVoiceFailure("Nothing was heard. Tap the microphone and try again.");
    } catch (error) {
      setVoiceFailure(error instanceof Error ? error.message : "Speech input failed.");
    } finally {
      setListening(false);
    }
  }, [send]);

  const toggleListening = useCallback(async () => {
    setVoiceFailure(null);
    if (listening) {
      await stopListening();
      return;
    }
    if (status !== "open") {
      setVoiceFailure("Connect the SSH terminal before starting voice input.");
      return;
    }
    stopSpeaking();
    try {
      const cfg = await loadLocalSpeechConfig();
      if ((cfg.sttProvider || "on-device") === "on-device") {
        realtimeRef.current = await startRealtimeTranscribe(() => {});
      } else {
        const { Audio } = require("expo-av");
        recordingRef.current = await createMicrophoneRecording(Audio.RecordingOptionsPresets.HIGH_QUALITY);
      }
      setListening(true);
    } catch (error) {
      setListening(false);
      setVoiceFailure(error instanceof Error ? error.message : "Microphone unavailable.");
    }
  }, [listening, status, stopListening]);

  const speakLatest = useCallback(async () => {
    if (speaking) {
      stopSpeaking();
      setSpeaking(false);
      return;
    }
    const excerpt = terminalSpeechExcerpt(speechTailRef.current);
    if (!excerpt) {
      setVoiceFailure("There is no terminal output to read yet.");
      return;
    }
    setVoiceFailure(null);
    setSpeaking(true);
    try {
      const cfg = await loadLocalSpeechConfig();
      await speakText(excerpt, {
        provider: cfg.ttsProvider || "device",
        apiKey: cfg.apiKey,
        model: cfg.ttsModel || DEFAULT_TTS_MODEL,
        voice: cfg.ttsVoice || DEFAULT_TTS_VOICE,
      });
    } catch (error) {
      setVoiceFailure(error instanceof Error ? error.message : "Spoken output failed.");
    } finally {
      setSpeaking(false);
    }
  }, [speaking]);

  useEffect(() => () => {
    stopSpeaking();
    const realtime = realtimeRef.current;
    realtimeRef.current = null;
    if (realtime) void realtime.stop().catch(() => {});
    const recording = recordingRef.current;
    recordingRef.current = null;
    if (recording) void discardMicrophoneRecording(recording);
  }, []);

  return (
    <View style={[styles.root, { backgroundColor: "#0b0e14" }]}>
      <View style={[styles.bar, { borderBottomColor: colors.border }]}>
        <Text style={[styles.title, { color: colors.textSecondary }]} numberOfLines={1}>
          SSH · {activeDevice?.alias ? `@${activeDevice.alias}` : activeDevice?.name || "machine"} · {runner === "shell" ? tmuxSession : runner}
        </Text>
        <Text style={{ color: status === "open" ? colors.success : colors.textMuted, fontSize: 10 }}>{status}</Text>
        <Pressable accessibilityLabel="Zoom terminal out" onPress={() => changeFontSize(-1)} style={styles.tool}><Text style={styles.toolText}>A−</Text></Pressable>
        <Pressable accessibilityLabel="Reset terminal zoom" onPress={() => changeFontSize(0)} style={styles.tool}><Text style={styles.toolText}>{fontSize}</Text></Pressable>
        <Pressable accessibilityLabel="Zoom terminal in" onPress={() => changeFontSize(1)} style={styles.tool}><Text style={styles.toolText}>A+</Text></Pressable>
        <Pressable accessibilityLabel={listening ? "Stop voice input" : "Start voice input"} onPress={() => void toggleListening()} style={[styles.tool, listening && styles.toolActive]}><Text style={styles.toolText}>{listening ? "●" : "🎙"}</Text></Pressable>
        <Pressable accessibilityLabel={speaking ? "Stop spoken output" : "Read latest terminal output"} onPress={() => void speakLatest()} style={[styles.tool, speaking && styles.toolActive]}><Text style={styles.toolText}>{speaking ? "■" : "🔊"}</Text></Pressable>
        {onToggleFullscreen ? (
          <Pressable
            accessibilityLabel={expanded ? "Show the preview pane" : "Full screen SSH"}
            onPress={onToggleFullscreen}
            style={[styles.tool, expanded && styles.toolActive]}
          >
            <Text style={styles.toolText}>{expanded ? "⤡" : "⤢"}</Text>
          </Pressable>
        ) : null}
      </View>
      {runner === "shell" ? (
        <ScrollView
          horizontal
          showsHorizontalScrollIndicator={false}
          keyboardShouldPersistTaps="handled"
          style={[styles.toolsRow, { borderBottomColor: colors.border }]}
          contentContainerStyle={styles.toolsRowContent}
        >
          <Text style={[styles.toolsLabel, { color: colors.textMuted }]}>TOOLS</Text>
          {STUDIO_TOOLS.map((tool) => (
            <Pressable
              key={tool.id}
              disabled={status !== "open"}
              accessibilityLabel={`Open ${tool.label} in a new tmux window`}
              onPress={() => send(encodeUtf8(studioToolCommand(tool.command) + "\n"))}
              style={[styles.toolChip, status !== "open" && styles.toolDisabled]}
            >
              <Text style={styles.toolChipText}>{tool.label}</Text>
            </Pressable>
          ))}
          <Pressable
            disabled={status !== "open"}
            accessibilityLabel="Split a new tmux window"
            onPress={() => send(encodeUtf8("tmux new-window\n"))}
            style={[styles.toolChip, status !== "open" && styles.toolDisabled]}
          >
            <Text style={styles.toolChipText}>+ window</Text>
          </Pressable>
          <Pressable
            disabled={status !== "open"}
            accessibilityLabel="Split the current tmux pane vertically"
            onPress={() => send(encodeUtf8("tmux split-window -v -c \"#{pane_current_path}\"\n"))}
            style={[styles.toolChip, status !== "open" && styles.toolDisabled]}
          >
            <Text style={styles.toolChipText}>split</Text>
          </Pressable>
        </ScrollView>
      ) : null}
      <XtermView
        ref={terminalRef}
        onData={send}
        onResize={(cols, rows) => {
          if (socketRef.current?.readyState === WebSocket.OPEN) socketRef.current.send(resizeFrame(cols, rows));
        }}
        onReady={() => {
          terminalRef.current?.setFontSize(fontSize);
          terminalRef.current?.fit();
        }}
        style={styles.terminal}
      />
      {voiceFailure ? (
        <View style={[styles.voiceFailure, { backgroundColor: colors.bgCard, borderColor: colors.border }]}>
          <Text style={{ color: colors.error, fontSize: 11, flex: 1 }}>{voiceFailure}</Text>
          <Pressable onPress={() => router.push("/(tabs)/settings" as any)} style={styles.voiceAction}><Text style={{ color: colors.accent, fontSize: 11, fontWeight: "800" }}>Voice settings</Text></Pressable>
          {/permission|microphone/i.test(voiceFailure) ? <Pressable onPress={() => void Linking.openSettings()} style={styles.voiceAction}><Text style={{ color: colors.accent, fontSize: 11, fontWeight: "800" }}>OS settings</Text></Pressable> : null}
          <Pressable onPress={() => setVoiceFailure(null)} style={styles.voiceAction}><Text style={{ color: colors.textSecondary, fontSize: 11 }}>Dismiss</Text></Pressable>
        </View>
      ) : null}
      {failure ? (
        <View style={[styles.failure, { backgroundColor: colors.bgCard, borderColor: colors.border }]}>
          <Text style={{ color: colors.error, fontSize: 12, flex: 1 }}>{failure}</Text>
          <Pressable onPress={() => setAttempt((value) => value + 1)} style={[styles.retry, { backgroundColor: colors.accent }]}>
            <Text style={styles.retryText}>Retry</Text>
          </Pressable>
        </View>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, minHeight: 0 },
  bar: { height: 34, paddingHorizontal: 10, borderBottomWidth: StyleSheet.hairlineWidth, flexDirection: "row", alignItems: "center", gap: 8 },
  title: { flex: 1, fontSize: 11, fontWeight: "700" },
  toolsRow: { flexGrow: 0, borderBottomWidth: StyleSheet.hairlineWidth },
  toolsRowContent: { alignItems: "center", gap: 6, paddingHorizontal: 10, paddingVertical: 6 },
  toolsLabel: { fontSize: 9, fontWeight: "900", letterSpacing: 1, marginRight: 2 },
  toolChip: { minHeight: 24, borderWidth: 1, borderColor: "#2a3240", borderRadius: 6, paddingHorizontal: 9, justifyContent: "center", backgroundColor: "#171c26" },
  toolChipText: { color: "#d7dce5", fontSize: 11, fontWeight: "700" },
  toolDisabled: { opacity: 0.4 },
  terminal: { flex: 1 },
  failure: { position: "absolute", left: 12, right: 12, bottom: 12, borderWidth: 1, borderRadius: 10, padding: 10, flexDirection: "row", alignItems: "center", gap: 10 },
  retry: { borderRadius: 8, paddingHorizontal: 12, paddingVertical: 7 },
  retryText: { color: "#fff", fontSize: 11, fontWeight: "800" },
  tool: { minWidth: 30, height: 26, paddingHorizontal: 6, borderRadius: 6, backgroundColor: "#171c26", alignItems: "center", justifyContent: "center" },
  toolActive: { backgroundColor: "#5b21b6" },
  toolText: { color: "#d7dce5", fontSize: 10, fontWeight: "800" },
  voiceFailure: { position: "absolute", left: 12, right: 12, top: 44, borderWidth: 1, borderRadius: 10, padding: 9, flexDirection: "row", alignItems: "center", gap: 8 },
  voiceAction: { paddingHorizontal: 4, paddingVertical: 4 },
});
