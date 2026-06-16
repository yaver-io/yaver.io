// Car Voice Coding — Tier 0 "code from the car by voice".
//
// Speak a command → it's transcribed on-device (or via your STT key) →
// dispatched as a coding task to the box you pick → polled to done → a ONE
// sentence status is read back over the car's Bluetooth audio. We never read
// code/diffs aloud while driving (carVoiceCoding.ts::isReadCodeRequest) and we
// HARD-GATE risky commands (deploy / push / delete / force) behind an explicit
// on-screen + spoken confirm (carVoiceConfirm.ts) before anything dispatches.
//
// Tier 0 needs NO car SDK and NO entitlement: audio plays over whatever route
// is active — paired car speakers when you're in the car. The loop lib is
// makeRealCarVoiceDeps + runCarVoiceTurn; this screen owns recording state, the
// box picker, the turn history, and the confirmation modal.
//
// Big touch targets + high contrast for a glance-and-go context, but fully
// functional on the phone first. Hands-free entry (carVoiceEntry.ts) lets a
// native quick-action / Siri shortcut / deep-link autostart a turn with no
// navigation; until that native trigger ships, the big PTT button is the
// fallback.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Linking,
  Modal,
  Pressable,
  ScrollView,
  Text,
  View,
} from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { connectionManager } from "../src/lib/connectionManager";
import { loadLocalSpeechConfig } from "../src/lib/auth";
import { speakText } from "../src/lib/speech";
import {
  makeRealCarVoiceDeps,
  runCarVoiceTurn,
  type CarVoiceConfig,
  type CarVoiceStage,
  type CarVoiceTaskRef,
} from "../src/lib/carVoiceCoding";
import { assessRisk, interpretConfirmReply } from "../src/lib/carVoiceConfirm";
import { carVoiceEntryBus, shouldAutostart } from "../src/lib/carVoiceEntry";

// ── turn history model (UI only) ────────────────────────────────────
interface Turn {
  id: string;
  transcript: string;
  spoken: string;
  stage: CarVoiceStage | "queued" | "confirming";
  status?: string;
  declined?: boolean;
  at: number;
}

const STAGE_LABEL: Record<string, string> = {
  queued: "Queued",
  listening: "Listening…",
  transcribed: "Heard you",
  confirming: "Confirm to run",
  dispatched: "Sent to box",
  working: "Working…",
  spoken: "Done",
  declined: "Declined",
  error: "Error",
};

export default function CarVoiceCodingScreen() {
  const c = useColors();
  const router = useRouter();
  const params = useLocalSearchParams<{ surface?: string; autostart?: string }>();
  const glass = params.surface === "glass";
  const deviceCtx = useDevice();
  const devices = ((deviceCtx as any).devices as any[]) || [];

  const [deviceId, setDeviceId] = useState("");
  const [status, setStatus] = useState<"idle" | "recording" | "thinking" | "speaking" | "error">("idle");
  const [errorMsg, setErrorMsg] = useState("");
  const [turns, setTurns] = useState<Turn[]>([]);

  // Confirmation gate state for a risky command awaiting an explicit OK.
  const [confirm, setConfirm] = useState<{ transcript: string; prompt: string } | null>(null);

  const recordingRef = useRef<any>(null); // expo-av Audio.Recording
  const liveRef = useRef(true);
  useEffect(() => () => { liveRef.current = false; }, []);

  const pickedDevice = devices.find((d) => (d.id || d.deviceId) === deviceId);

  // ── deps factory: dispatch + getTask go through THE picked box ────
  const buildDeps = useCallback(async () => {
    const cfg = await loadLocalSpeechConfig();
    const config: CarVoiceConfig = {
      stt: { provider: cfg.sttProvider || "on-device", apiKey: cfg.apiKey, model: cfg.sttModel },
      tts: { provider: cfg.ttsProvider || "device", apiKey: cfg.apiKey, voice: cfg.ttsVoice },
      speakAcknowledgement: true,
    };
    const client = connectionManager.clientFor(deviceId);
    const deps = makeRealCarVoiceDeps({
      config,
      // codeMode=true → terminal-style ("yaver code") prompt wrapping.
      dispatchTask: async (title, prompt) => {
        const t = await client.sendTask(title, prompt, undefined, undefined, undefined, undefined, undefined, undefined, undefined, undefined, true);
        return { id: t.id };
      },
      getTask: async (taskId): Promise<CarVoiceTaskRef> => {
        const t = await client.getTask(taskId);
        return { id: t.id, status: t.status, resultText: t.resultText, output: t.output };
      },
    });
    return { deps, config };
  }, [deviceId]);

  // ── recording (mirrors AgentVoiceButton's expo-av path) ───────────
  const startRecording = useCallback(async () => {
    setErrorMsg("");
    const { Audio } = require("expo-av");
    const perm = await Audio.getPermissionsAsync();
    if (perm.status !== "granted") {
      const req = perm.canAskAgain ? await Audio.requestPermissionsAsync() : perm;
      if (req.status !== "granted") {
        Alert.alert("Microphone Access", "Mic permission is required to speak commands.", [
          { text: "Cancel", style: "cancel" },
          { text: "Open Settings", onPress: () => Linking.openSettings() },
        ]);
        return;
      }
    }
    await Audio.setAudioModeAsync({ allowsRecordingIOS: true, playsInSilentModeIOS: true });
    try {
      const { recording } = await Audio.Recording.createAsync(
        Audio.RecordingOptionsPresets.HIGH_QUALITY,
      );
      recordingRef.current = recording;
      setStatus("recording");
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : String(err));
      setStatus("error");
    }
  }, []);

  const stopRecordingToUri = useCallback(async (): Promise<string | null> => {
    const rec = recordingRef.current;
    if (!rec) return null;
    let uri: string | null = null;
    try {
      await rec.stopAndUnloadAsync();
      uri = rec.getURI() ?? null;
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : String(err));
      setStatus("error");
    }
    recordingRef.current = null;
    return uri;
  }, []);

  // Run a full turn from a recorded clip. Inserts a live turn row, transcribes,
  // GATES risky commands, then runs the lib loop (dispatch→poll→summarize→speak).
  const runTurnFromUri = useCallback(
    async (uri: string) => {
      const turnId = `${Date.now()}`;
      const seed: Turn = { id: turnId, transcript: "", spoken: "", stage: "listening", at: Date.now() };
      setTurns((prev) => [seed, ...prev].slice(0, 50));
      setStatus("thinking");

      const { deps, config } = await buildDeps();

      // 1) Transcribe first so we can run the risk gate BEFORE any dispatch.
      let transcript = "";
      try {
        transcript = (await deps.transcribe(uri)).trim();
      } catch (e) {
        const spoken = "I couldn't understand that.";
        patchTurn(turnId, { stage: "error", spoken });
        await safeSpeak(deps, spoken);
        setStatus("idle");
        return;
      }
      patchTurn(turnId, { transcript, stage: "transcribed" });
      if (!transcript) {
        const spoken = "I didn't catch that.";
        patchTurn(turnId, { stage: "error", spoken });
        await safeSpeak(deps, spoken);
        setStatus("idle");
        return;
      }

      // 2) SAFETY GATE — risky commands stop here for explicit confirm.
      const risk = assessRisk(transcript);
      if (risk.risky) {
        patchTurn(turnId, { stage: "confirming", spoken: risk.prompt });
        setConfirm({ transcript, prompt: risk.prompt });
        setStatus("speaking");
        await safeSpeak(deps, risk.prompt);
        setStatus("idle");
        return; // dispatch only happens after confirmTurn()
      }

      await dispatchTurn(turnId, transcript, deps, config);
    },
    [buildDeps],
  );

  // Shared dispatch path (used after transcribe for safe commands AND after a
  // confirm for risky ones). We pass the already-known transcript by faking a
  // single-shot deps.transcribe so the lib's read-code guard + summarizer +
  // speak path all run unchanged.
  const dispatchTurn = useCallback(
    async (turnId: string, transcript: string, deps: ReturnType<typeof makeRealCarVoiceDeps> | any, config: CarVoiceConfig) => {
      setStatus("thinking");
      const fixedDeps = { ...deps, transcribe: async () => transcript };
      const r = await runCarVoiceTurn("preset://" + turnId, fixedDeps, config, (step) => {
        if (!liveRef.current) return;
        if (step.stage === "dispatched") setStatus("thinking");
        if (step.stage === "spoken") setStatus("speaking");
        patchTurn(turnId, {
          stage: step.stage,
          status: step.status,
          ...(step.text ? { spoken: step.text } : {}),
        });
      });
      patchTurn(turnId, {
        stage: r.declined ? "declined" : "spoken",
        spoken: r.spoken,
        status: r.status,
        declined: r.declined,
      });
      setStatus("idle");
    },
    [],
  );

  function patchTurn(id: string, patch: Partial<Turn>) {
    if (!liveRef.current) return;
    setTurns((prev) => prev.map((t) => (t.id === id ? { ...t, ...patch } : t)));
  }

  // ── push-to-talk gesture ──────────────────────────────────────────
  const onPressTalk = useCallback(async () => {
    if (!deviceId) {
      Alert.alert("Pick a box first", "Choose the machine that should run your commands.");
      return;
    }
    if (status === "recording") {
      const uri = await stopRecordingToUri();
      if (uri) void runTurnFromUri(uri);
      else setStatus("idle");
      return;
    }
    if (status === "idle" || status === "error") {
      void startRecording();
    }
  }, [deviceId, status, startRecording, stopRecordingToUri, runTurnFromUri]);

  // ── confirmation actions ──────────────────────────────────────────
  const confirmTurn = useCallback(async () => {
    if (!confirm) return;
    const { transcript } = confirm;
    setConfirm(null);
    const turnId = `${Date.now()}`;
    const seed: Turn = { id: turnId, transcript, spoken: "On it.", stage: "dispatched", at: Date.now() };
    setTurns((prev) => [seed, ...prev].slice(0, 50));
    const { deps, config } = await buildDeps();
    await dispatchTurn(turnId, transcript, deps, config);
  }, [confirm, buildDeps, dispatchTurn]);

  const cancelTurn = useCallback(async () => {
    setConfirm(null);
    setStatus("idle");
    try { await speakText("Cancelled.", { provider: "device" }); } catch { /* ignore */ }
  }, []);

  // Spoken confirm: record a short yes/no, transcribe, interpret. Lets the
  // driver confirm hands-free without reaching for the screen.
  const confirmBySpeech = useCallback(async () => {
    if (!confirm) return;
    await startRecording();
    // The user taps again (or we could VAD); for simplicity we reuse the PTT:
    // stop is wired through onPressTalk when status==="recording". Here we just
    // arm recording; the reply is handled by replyToConfirm below.
  }, [confirm, startRecording]);

  const replyToConfirm = useCallback(async () => {
    const uri = await stopRecordingToUri();
    setStatus("idle");
    if (!uri || !confirm) return;
    const { deps } = await buildDeps();
    let reply = "";
    try { reply = (await deps.transcribe(uri)).trim(); } catch { /* ignore */ }
    const verdict = interpretConfirmReply(reply);
    if (verdict === "confirm") {
      await confirmTurn();
    } else if (verdict === "cancel") {
      await cancelTurn();
    } else {
      try { await speakText("I didn't catch a yes or no — tap Confirm or Cancel.", { provider: "device" }); } catch { /* ignore */ }
    }
  }, [confirm, buildDeps, confirmTurn, cancelTurn, stopRecordingToUri]);

  // ── hands-free entry: autostart on deep link, and live trigger bus ─
  useEffect(() => {
    const unsub = carVoiceEntryBus.subscribe(() => {
      if (!deviceId) return; // need a box; the user picks once, then it's hands-free
      if (status === "idle" || status === "error") void startRecording();
    });
    return unsub;
  }, [deviceId, status, startRecording]);

  useEffect(() => {
    if (shouldAutostart(params.autostart) && deviceId && status === "idle") {
      void startRecording();
    }
    // run once per device selection / autostart param
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId]);

  // ── styles ────────────────────────────────────────────────────────
  const card = { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 12, padding: 14, marginBottom: 12 } as const;
  const TALK_COLOR = status === "recording" ? "#ef4444" : status === "thinking" ? "#8b5cf6" : status === "speaking" ? "#f59e0b" : c.accent;
  const talkLabel =
    status === "recording" ? "Listening… tap to send" :
    status === "thinking" ? "Working…" :
    status === "speaking" ? "Reading back…" :
    confirm ? "Confirm needed" : "Hold the road — tap to speak";

  // ── device picker ─────────────────────────────────────────────────
  if (!deviceId) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader title="Car Voice Coding" onBack={() => router.back()} />
        <ScrollView contentContainerStyle={{ padding: 16 }}>
          <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700", marginBottom: 6 }}>
            Pick the box that runs your commands
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 14 }}>
            Speak a coding task; it runs on this machine and reads the result back over your car audio.
          </Text>
          {devices.map((d) => {
            const id = d.id || d.deviceId;
            return (
              <Pressable
                key={id}
                onPress={() => setDeviceId(id)}
                style={[card, { flexDirection: "row", justifyContent: "space-between", alignItems: "center" }]}
              >
                <Text style={{ color: c.textPrimary, fontWeight: "600", fontSize: 16 }}>{d.name || d.alias || id}</Text>
                <Text style={{ color: d.online ? "#22c55e" : c.textMuted }}>{d.online ? "online" : "offline"}</Text>
              </Pressable>
            );
          })}
          {devices.length === 0 && <Text style={{ color: c.textMuted }}>No devices yet. Sign a box in first.</Text>}
        </ScrollView>
      </View>
    );
  }

  // ── glass HUD (compact: just the PTT + last status) ───────────────
  if (glass) {
    const last = turns[0];
    return (
      <View style={{ flex: 1, backgroundColor: c.bg, alignItems: "center", justifyContent: "center", padding: 16 }}>
        <Pressable
          onPress={onPressTalk}
          style={{ width: 160, height: 160, borderRadius: 80, backgroundColor: TALK_COLOR, alignItems: "center", justifyContent: "center" }}
          accessibilityRole="button"
          accessibilityLabel={talkLabel}
        >
          <Text style={{ color: "#fff", fontSize: 20, fontWeight: "800" }}>{status === "recording" ? "■" : "🎤"}</Text>
        </Pressable>
        <Text style={{ color: c.textPrimary, marginTop: 16, fontSize: 16, textAlign: "center" }} numberOfLines={3}>
          {last?.spoken || talkLabel}
        </Text>
      </View>
    );
  }

  // ── full surface (phone / head unit) ──────────────────────────────
  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Car Voice Coding" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16 }}>
        {/* Active box */}
        <View style={[card, { flexDirection: "row", justifyContent: "space-between", alignItems: "center" }]}>
          <View style={{ flex: 1 }}>
            <Text style={{ color: c.textMuted, fontSize: 11 }}>Running on</Text>
            <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 16 }} numberOfLines={1}>
              {pickedDevice?.name || pickedDevice?.alias || deviceId}
            </Text>
          </View>
          <Pressable onPress={() => setDeviceId("")} style={{ paddingVertical: 8, paddingHorizontal: 12, backgroundColor: c.bgCard, borderRadius: 10, borderWidth: 1, borderColor: c.border }}>
            <Text style={{ color: c.textMuted, fontSize: 13 }}>Switch</Text>
          </Pressable>
        </View>

        {/* Big push-to-talk */}
        <View style={{ alignItems: "center", marginVertical: 14 }}>
          <Pressable
            onPress={onPressTalk}
            style={{ width: 200, height: 200, borderRadius: 100, backgroundColor: TALK_COLOR, alignItems: "center", justifyContent: "center", opacity: status === "thinking" ? 0.85 : 1 }}
            accessibilityRole="button"
            accessibilityLabel={talkLabel}
          >
            {status === "thinking" ? (
              <ActivityIndicator color="#fff" size="large" />
            ) : (
              <Text style={{ color: "#fff", fontSize: 56 }}>{status === "recording" ? "■" : "🎤"}</Text>
            )}
          </Pressable>
          <Text style={{ color: c.textPrimary, fontSize: 18, fontWeight: "700", marginTop: 14, textAlign: "center" }}>
            {talkLabel}
          </Text>
          {!!errorMsg && <Text style={{ color: c.error || "#f55", fontSize: 13, marginTop: 6 }}>{errorMsg}</Text>}
        </View>

        {/* Turn history */}
        {turns.length > 0 && (
          <View style={card}>
            <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700", marginBottom: 10 }}>History</Text>
            {turns.map((t) => {
              const stageColor =
                t.stage === "error" ? (c.error || "#ef4444") :
                t.stage === "declined" ? "#f59e0b" :
                t.stage === "spoken" ? "#22c55e" :
                t.stage === "confirming" ? "#f59e0b" : c.textMuted;
              return (
                <View key={t.id} style={{ paddingVertical: 8, borderBottomWidth: 1, borderBottomColor: c.border }}>
                  <Text style={{ color: c.textPrimary, fontSize: 15 }} numberOfLines={2}>
                    {t.transcript ? `“${t.transcript}”` : "…"}
                  </Text>
                  <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginTop: 4 }}>
                    <Text style={{ color: stageColor, fontSize: 12, fontWeight: "600" }}>
                      {STAGE_LABEL[t.stage] || t.stage}
                    </Text>
                    {!!t.spoken && (
                      <Text style={{ color: c.textMuted, fontSize: 13, flex: 1 }} numberOfLines={2}>
                        {t.spoken}
                      </Text>
                    )}
                  </View>
                </View>
              );
            })}
          </View>
        )}

        <Text style={{ color: c.textMuted, fontSize: 11, textAlign: "center", marginTop: 4 }}>
          Risky commands (deploy / push / delete / force) always ask before running. Code is never read aloud while you drive.
        </Text>
      </ScrollView>

      {/* Confirmation gate modal */}
      <Modal visible={!!confirm} transparent animationType="fade" onRequestClose={cancelTurn}>
        <View style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.6)", justifyContent: "center", padding: 24 }}>
          <View style={{ backgroundColor: c.bgCard, borderRadius: 16, padding: 20, borderWidth: 1, borderColor: c.border }}>
            <Text style={{ color: c.textPrimary, fontSize: 18, fontWeight: "800", marginBottom: 8 }}>Confirm before running</Text>
            <Text style={{ color: c.textPrimary, fontSize: 16, marginBottom: 6 }} numberOfLines={3}>
              “{confirm?.transcript}”
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 18 }}>{confirm?.prompt}</Text>
            <View style={{ flexDirection: "row", gap: 12 }}>
              <Pressable
                onPress={cancelTurn}
                style={{ flex: 1, paddingVertical: 16, borderRadius: 12, backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, alignItems: "center" }}
                accessibilityRole="button"
                accessibilityLabel="Cancel command"
              >
                <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700" }}>Cancel</Text>
              </Pressable>
              <Pressable
                onPress={confirmTurn}
                style={{ flex: 1, paddingVertical: 16, borderRadius: 12, backgroundColor: "#ef4444", alignItems: "center" }}
                accessibilityRole="button"
                accessibilityLabel="Confirm and run command"
              >
                <Text style={{ color: "#fff", fontSize: 16, fontWeight: "800" }}>Confirm</Text>
              </Pressable>
            </View>
            {/* Hands-free confirm: arm a short spoken yes/no */}
            <Pressable
              onPress={status === "recording" ? replyToConfirm : confirmBySpeech}
              style={{ marginTop: 12, paddingVertical: 12, borderRadius: 12, backgroundColor: c.bg, borderWidth: 1, borderColor: c.border, alignItems: "center" }}
              accessibilityRole="button"
              accessibilityLabel="Answer by voice"
            >
              <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>
                {status === "recording" ? "Tap when done speaking" : "Answer by voice (say “confirm” or “cancel”)"}
              </Text>
            </Pressable>
          </View>
        </View>
      </Modal>
    </View>
  );
}

// Never let a TTS failure crash the loop.
async function safeSpeak(deps: { speak: (s: string) => Promise<void> }, text: string): Promise<void> {
  try { await deps.speak(text); } catch { /* ignore */ }
}
