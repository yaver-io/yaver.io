/**
 * AgentVoiceButton — floating mic affordance that drives the hands-free
 * agent loop end-to-end (record → backend STT → task creation → TTS
 * readback). Differs from the existing mic in tasks.tsx, which only
 * dictates text into the input field.
 *
 * Visual is intentionally simple (plain styled View). The Liquid Glass
 * orb upgrade lives in a later task — see [project_voice_glasses_revival_2026_05_27].
 */

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, Animated, Dimensions, Easing, Linking, Pressable, StyleSheet, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useColors } from "../context/ThemeContext";
import { AgentVoiceSession, pcmToTempWavURI } from "../lib/agentVoice";
import { YaverGlass } from "./YaverGlass";

/** Map the runtime device shape to the Go-side TaskViewport.Surface enum.
 *  Tablet bar is ~768pt — anything wider counts as tablet. */
function detectMobileSurface(): "mobile-phone" | "mobile-tablet" {
  const { width } = Dimensions.get("window");
  return width >= 768 ? "mobile-tablet" : "mobile-phone";
}

type Status = "idle" | "recording" | "uploading" | "thinking" | "speaking" | "error";

interface Props {
  project?: string;
  model?: string;
  runner?: string;
  /** Called when the agent finishes the task. Lets the host screen
   *  navigate to the task / refresh its list. */
  onTaskCreated?: (taskId: string) => void;
}

const COLOR_FOR_STATUS: Record<Status, string> = {
  idle: "#10b981",       // emerald
  recording: "#ef4444",  // red
  uploading: "#3b82f6",  // blue
  thinking: "#8b5cf6",   // violet
  speaking: "#f59e0b",   // amber
  error: "#6b7280",      // gray
};

const LABEL_FOR_STATUS: Record<Status, string> = {
  idle: "Tap to speak",
  recording: "Listening…",
  uploading: "Sending…",
  thinking: "Yaver is thinking…",
  speaking: "Reading back…",
  error: "Try again",
};

export function AgentVoiceButton({ project, model, runner, onTaskCreated }: Props): React.JSX.Element {
  const colors = useColors();
  const [status, setStatus] = useState<Status>("idle");
  const [transcript, setTranscript] = useState("");
  const [errorMsg, setErrorMsg] = useState("");

  const recordingRef = useRef<any>(null); // expo-av Audio.Recording
  const sessionRef = useRef<AgentVoiceSession | null>(null);
  const pulseAnim = useRef(new Animated.Value(1)).current;
  // Auto-hide when voice not enabled or keys not configured. Keyboard-
  // only trio users shouldn't see a non-functional mic orb.
  const [voiceReady, setVoiceReady] = useState<boolean | null>(null);
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const { quicClient } = require("../lib/quic");
        const res = await fetch(`${quicClient.baseUrl}/voice/status`, {
          headers: quicClient.getAuthHeaders(),
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const body = await res.json();
        if (cancelled) return;
        const ready = !!body.enabled && !!body.sttReady && !!body.ttsReady;
        setVoiceReady(ready);
      } catch {
        if (!cancelled) setVoiceReady(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  // Pulse the button while recording or speaking.
  useEffect(() => {
    if (status === "recording" || status === "speaking") {
      const loop = Animated.loop(
        Animated.sequence([
          Animated.timing(pulseAnim, { toValue: 1.15, duration: 480, easing: Easing.inOut(Easing.quad), useNativeDriver: true }),
          Animated.timing(pulseAnim, { toValue: 1.0,  duration: 480, easing: Easing.inOut(Easing.quad), useNativeDriver: true }),
        ]),
      );
      loop.start();
      return () => loop.stop();
    }
    pulseAnim.setValue(1);
  }, [status, pulseAnim]);

  const cleanupRecording = useCallback(async () => {
    const rec = recordingRef.current;
    if (!rec) return;
    try {
      const st = await rec.getStatusAsync();
      if (st.isRecording) {
        await rec.stopAndUnloadAsync();
      }
    } catch {
      // ignore — recorder may already be stopped
    }
    recordingRef.current = null;
  }, []);

  const reset = useCallback(async () => {
    await cleanupRecording();
    sessionRef.current?.close();
    sessionRef.current = null;
    setTranscript("");
    setErrorMsg("");
    setStatus("idle");
  }, [cleanupRecording]);

  const startRecording = useCallback(async () => {
    setErrorMsg("");
    setTranscript("");

    const { Audio } = require("expo-av");
    const perm = await Audio.getPermissionsAsync();
    if (perm.status !== "granted") {
      const req = perm.canAskAgain ? await Audio.requestPermissionsAsync() : perm;
      if (req.status !== "granted") {
        Alert.alert("Microphone Access", "Mic permission is required.", [
          { text: "Cancel", style: "cancel" },
          { text: "Open Settings", onPress: () => Linking.openSettings() },
        ]);
        return;
      }
    }
    await Audio.setAudioModeAsync({ allowsRecordingIOS: true, playsInSilentModeIOS: true });

    // Raw PCM 16-bit LE, 16kHz mono — what Deepgram Flux expects. We
    // strip the WAV header on the way to the backend.
    const recordingOptions: any = {
      android: {
        extension: ".wav",
        outputFormat: 2, // MPEG_4? not used — for LPCM we want raw WAV
        audioEncoder: 3,
        sampleRate: 16000,
        numberOfChannels: 1,
        bitRate: 256000,
      },
      ios: {
        extension: ".wav",
        outputFormat: "lpcm",
        audioQuality: 0x40, // LOW
        sampleRate: 16000,
        numberOfChannels: 1,
        bitRate: 256000,
        linearPCMBitDepth: 16,
        linearPCMIsBigEndian: false,
        linearPCMIsFloat: false,
      },
      web: {
        mimeType: "audio/wav",
        bitsPerSecond: 256000,
      },
    };

    try {
      const { recording } = await Audio.Recording.createAsync(recordingOptions);
      recordingRef.current = recording;
      setStatus("recording");
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setErrorMsg(msg);
      setStatus("error");
    }
  }, []);

  const stopAndProcess = useCallback(async () => {
    const rec = recordingRef.current;
    if (!rec) {
      setStatus("idle");
      return;
    }
    setStatus("uploading");
    let uri: string | undefined;
    try {
      await rec.stopAndUnloadAsync();
      uri = rec.getURI() ?? undefined;
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setErrorMsg(msg);
      setStatus("error");
      return;
    }
    recordingRef.current = null;
    if (!uri) {
      setErrorMsg("recording produced no file");
      setStatus("error");
      return;
    }

    const session = new AgentVoiceSession({
      onTranscriptPartial: (t) => setTranscript(t),
      onTranscriptFinal: (t) => {
        setTranscript(t);
        setStatus("thinking");
      },
      onTaskCreated: (id) => onTaskCreated?.(id),
      onTaskResult: (_id, _text, _status) => {
        setStatus("speaking");
      },
      onTTSReady: async (pcm, sampleRate) => {
        try {
          const wavURI = await pcmToTempWavURI(pcm, sampleRate);
          const { Audio } = require("expo-av");
          const { sound } = await Audio.Sound.createAsync({ uri: wavURI }, { shouldPlay: true });
          sound.setOnPlaybackStatusUpdate((st: any) => {
            if (st.didJustFinish) sound.unloadAsync().catch(() => {});
          });
        } catch (err) {
          const msg = err instanceof Error ? err.message : String(err);
          setErrorMsg(msg);
          setStatus("error");
        }
      },
      onDone: () => {
        // Reset after a short delay so the user sees the final state.
        setTimeout(() => {
          setStatus("idle");
          setTranscript("");
        }, 1500);
      },
      onError: (msg) => {
        setErrorMsg(msg);
        setStatus("error");
      },
    });
    sessionRef.current = session;
    try {
      await session.start({
        project,
        model,
        runner,
        surface: detectMobileSurface(),
        paneCount: 1,
        ttsBudget: 280,
      });
      await session.streamAudioFile(uri, { skipWavHeader: true });
      session.finalize();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setErrorMsg(msg);
      setStatus("error");
      session.close();
    }
  }, [project, model, runner, onTaskCreated]);

  const onPress = useCallback(() => {
    if (status === "idle" || status === "error") {
      void startRecording();
    } else if (status === "recording") {
      void stopAndProcess();
    } else {
      // mid-flow tap → cancel + reset
      void reset();
    }
  }, [status, startRecording, stopAndProcess, reset]);

  useEffect(() => {
    return () => {
      void cleanupRecording();
      sessionRef.current?.close();
    };
  }, [cleanupRecording]);

  const ringColor = COLOR_FOR_STATUS[status];
  const subtitleColor = status === "error" ? "#dc2626" : colors.textMuted ?? "#6b7280";

  // Keyboard-only trio mode: no orb at all when /voice/status reports
  // not ready. The user gets a clean UI without a button that would
  // just error on tap.
  if (voiceReady === false) return <View />;

  return (
    <View style={styles.wrap} pointerEvents="box-none">
      <Animated.View style={{ transform: [{ scale: pulseAnim }] }}>
        {/* Liquid Glass orb: iOS 26+ gets true refraction, iOS 18-25
            gets BlurView frosted glass, Android gets solid Material 3,
            Reduce Transparency drops to solid fill on all. */}
        <YaverGlass shape="circle" tint={withAlpha(ringColor, 0.65)} style={styles.glassOrb}>
          <Pressable
            onPress={onPress}
            style={({ pressed }) => [
              styles.orbInner,
              { borderColor: withAlpha(ringColor, 0.35), opacity: pressed ? 0.88 : 1 },
            ]}
            accessibilityRole="button"
            accessibilityLabel={LABEL_FOR_STATUS[status]}
          >
            <Ionicons name={status === "recording" ? "stop" : "mic"} size={28} color="#fff" />
          </Pressable>
        </YaverGlass>
      </Animated.View>
      <Text style={[styles.label, { color: subtitleColor }]} numberOfLines={2}>
        {errorMsg || (transcript ? `"${transcript}"` : LABEL_FOR_STATUS[status])}
      </Text>
    </View>
  );
}

function withAlpha(hex: string, alpha: number): string {
  // Lean helper — assumes #rrggbb input.
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  return `rgba(${r},${g},${b},${alpha})`;
}

const styles = StyleSheet.create({
  wrap: { alignItems: "center", justifyContent: "center" },
  glassOrb: {
    width: 72,
    height: 72,
    borderRadius: 36,
    // Shadow lives on the wrapping View since BlurView/GlassView
    // clip their own bounds — putting it here keeps it visible.
    shadowColor: "#000",
    shadowOpacity: 0.3,
    shadowRadius: 10,
    shadowOffset: { width: 0, height: 5 },
    elevation: 9,
  },
  orbInner: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    borderWidth: 1.5,
    borderRadius: 36,
  },
  label: {
    marginTop: 6,
    fontSize: 12,
    maxWidth: 180,
    textAlign: "center",
  },
});

export default AgentVoiceButton;
