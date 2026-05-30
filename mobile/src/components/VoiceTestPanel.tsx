/**
 * VoiceTestPanel — Settings test surface for STT + TTS, the mobile twin of
 * the terminal `yaver voice test`. Lets the user verify both legs with
 * either the FREE local engine (on-device whisper / OS speech) or an
 * API-key provider, before relying on them in the agent voice loop.
 *
 *   STT: pick provider → record a clip → see the transcript (+ latency).
 *        on-device uses whisper.rn realtime (live partials, no key);
 *        cloud providers record via expo-av then POST the clip.
 *   TTS: pick provider → type a phrase → hear it spoken.
 *        device uses expo-speech (free); OpenAI/OpenRouter use your key.
 *
 * Self-contained: reuses src/lib/speech.ts (same code paths the real voice
 * loop uses) and prefills the saved speech key from the device keychain.
 */

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  View,
  Text,
  Pressable,
  TextInput,
  ScrollView,
  ActivityIndicator,
  StyleSheet,
} from "react-native";
import { getLocalSecret, LOCAL_KEYS, type SpeechProvider, type TtsProvider } from "../lib/auth";
import {
  transcribe,
  initWhisper,
  startRealtimeTranscribe,
  speakText,
  SPEECH_PROVIDERS,
  TTS_PROVIDERS,
  TTS_VOICES,
  DEFAULT_TTS_VOICE,
} from "../lib/speech";

type Status = { kind: "idle" | "ok" | "error" | "busy"; msg?: string };

export default function VoiceTestPanel() {
  const [savedKey, setSavedKey] = useState<string>("");

  // ── STT state ──────────────────────────────────────────────────────
  const [sttProvider, setSttProvider] = useState<SpeechProvider>("on-device");
  const [sttKey, setSttKey] = useState<string>("");
  const [recording, setRecording] = useState(false);
  const [transcribing, setTranscribing] = useState(false);
  const [transcript, setTranscript] = useState<string>("");
  const [sttStatus, setSttStatus] = useState<Status>({ kind: "idle" });
  const cloudRecRef = useRef<any>(null);
  const realtimeRef = useRef<{ stop: () => Promise<string> } | null>(null);

  // ── TTS state ──────────────────────────────────────────────────────
  const [ttsProvider, setTtsProvider] = useState<TtsProvider>("device");
  const [ttsKey, setTtsKey] = useState<string>("");
  const [ttsText, setTtsText] = useState<string>("Hello from Yaver. Voice is working.");
  const [ttsVoice, setTtsVoice] = useState<string>(DEFAULT_TTS_VOICE);
  const [ttsStatus, setTtsStatus] = useState<Status>({ kind: "idle" });

  useEffect(() => {
    getLocalSecret(LOCAL_KEYS.speechApiKey).then((k) => {
      if (k) {
        setSavedKey(k);
        setSttKey(k);
        setTtsKey(k);
      }
    });
  }, []);

  const sttInfo = SPEECH_PROVIDERS.find((p) => p.id === sttProvider);
  const ttsInfo = TTS_PROVIDERS.find((p) => p.id === ttsProvider);
  const sttNeedsKey = !!sttInfo?.requiresKey;
  const ttsNeedsKey = !!ttsInfo?.requiresKey;

  // ── STT actions ────────────────────────────────────────────────────
  const startStt = useCallback(async () => {
    setTranscript("");
    setSttStatus({ kind: "busy", msg: "listening…" });
    try {
      if (sttProvider === "on-device") {
        await initWhisper();
        realtimeRef.current = await startRealtimeTranscribe((partial) =>
          setTranscript(partial),
        );
        setRecording(true);
      } else {
        if (sttNeedsKey && !sttKey.trim()) throw new Error("API key required for this provider");
        const { Audio } = require("expo-av");
        await Audio.requestPermissionsAsync();
        await Audio.setAudioModeAsync({ allowsRecordingIOS: true, playsInSilentModeIOS: true });
        const { recording: rec } = await Audio.Recording.createAsync(
          Audio.RecordingOptionsPresets.HIGH_QUALITY,
        );
        cloudRecRef.current = rec;
        setRecording(true);
        setSttStatus({ kind: "busy", msg: "recording — tap Stop when done" });
      }
    } catch (e: any) {
      setRecording(false);
      setSttStatus({ kind: "error", msg: e?.message || String(e) });
    }
  }, [sttProvider, sttNeedsKey, sttKey]);

  const stopStt = useCallback(async () => {
    setRecording(false);
    setTranscribing(true);
    setSttStatus({ kind: "busy", msg: "transcribing…" });
    try {
      if (sttProvider === "on-device") {
        const final = await realtimeRef.current!.stop();
        realtimeRef.current = null;
        setTranscript(final);
        setSttStatus({ kind: "ok", msg: "on-device transcript ready" });
      } else {
        const rec = cloudRecRef.current;
        cloudRecRef.current = null;
        await rec.stopAndUnloadAsync();
        const uri = rec.getURI();
        if (!uri) throw new Error("no recording URI");
        const res = await transcribe(uri, { provider: sttProvider, apiKey: sttKey.trim() });
        setTranscript(res.text);
        setSttStatus({ kind: "ok", msg: `transcribed in ${res.durationMs} ms` });
      }
    } catch (e: any) {
      setSttStatus({ kind: "error", msg: e?.message || String(e) });
    } finally {
      setTranscribing(false);
    }
  }, [sttProvider, sttKey]);

  // ── TTS action ─────────────────────────────────────────────────────
  const testTts = useCallback(async () => {
    setTtsStatus({ kind: "busy", msg: "speaking…" });
    try {
      if (ttsNeedsKey && !ttsKey.trim()) throw new Error("API key required for this provider");
      await speakText(ttsText, {
        provider: ttsProvider,
        apiKey: ttsKey.trim() || undefined,
        voice: ttsVoice,
      });
      setTtsStatus({ kind: "ok", msg: "played" });
    } catch (e: any) {
      setTtsStatus({ kind: "error", msg: e?.message || String(e) });
    }
  }, [ttsProvider, ttsNeedsKey, ttsKey, ttsText, ttsVoice]);

  return (
    <ScrollView style={styles.root} contentContainerStyle={styles.content}>
      <Text style={styles.title}>Voice test</Text>
      <Text style={styles.subtitle}>
        Verify speech-to-text and text-to-speech with the free local engine or your own API key.
      </Text>

      {/* ── STT ── */}
      <Text style={styles.section}>Speech → Text</Text>
      <ChipRow
        options={SPEECH_PROVIDERS.map((p) => ({ id: p.id, label: p.name }))}
        selected={sttProvider}
        onSelect={(id) => setSttProvider(id as SpeechProvider)}
        disabled={recording || transcribing}
      />
      <Text style={styles.hint}>{sttInfo?.description}</Text>
      {sttNeedsKey && (
        <TextInput
          style={styles.input}
          value={sttKey}
          onChangeText={setSttKey}
          placeholder={sttInfo?.keyPlaceholder || "API key"}
          placeholderTextColor="#666"
          autoCapitalize="none"
          autoCorrect={false}
          secureTextEntry
        />
      )}
      <Pressable
        style={({ pressed }) => [
          styles.btn,
          { backgroundColor: recording ? "#ef4444" : "#2563eb", opacity: pressed ? 0.85 : 1 },
        ]}
        onPress={recording ? stopStt : startStt}
        disabled={transcribing}
      >
        <Text style={styles.btnText}>
          {transcribing ? "Transcribing…" : recording ? "Stop & transcribe" : "Record"}
        </Text>
      </Pressable>
      {transcript ? (
        <View style={styles.resultBox}>
          <Text style={styles.resultLabel}>Transcript</Text>
          <Text style={styles.resultText}>{transcript}</Text>
        </View>
      ) : null}
      <StatusLine status={sttStatus} />

      {/* ── TTS ── */}
      <Text style={[styles.section, { marginTop: 28 }]}>Text → Speech</Text>
      <ChipRow
        options={TTS_PROVIDERS.map((p) => ({ id: p.id, label: p.name }))}
        selected={ttsProvider}
        onSelect={(id) => setTtsProvider(id as TtsProvider)}
        disabled={ttsStatus.kind === "busy"}
      />
      <Text style={styles.hint}>{ttsInfo?.description}</Text>
      {ttsNeedsKey && (
        <TextInput
          style={styles.input}
          value={ttsKey}
          onChangeText={setTtsKey}
          placeholder="API key (sk-...)"
          placeholderTextColor="#666"
          autoCapitalize="none"
          autoCorrect={false}
          secureTextEntry
        />
      )}
      <TextInput
        style={[styles.input, styles.multiline]}
        value={ttsText}
        onChangeText={setTtsText}
        placeholder="Text to speak"
        placeholderTextColor="#666"
        multiline
      />
      {(ttsProvider === "openai" || ttsProvider === "openrouter") && (
        <ChipRow
          options={TTS_VOICES.map((v) => ({ id: v, label: v }))}
          selected={ttsVoice}
          onSelect={setTtsVoice}
          disabled={ttsStatus.kind === "busy"}
        />
      )}
      <Pressable
        style={({ pressed }) => [
          styles.btn,
          { backgroundColor: "#2563eb", opacity: ttsStatus.kind === "busy" ? 0.5 : pressed ? 0.85 : 1 },
        ]}
        onPress={testTts}
        disabled={ttsStatus.kind === "busy"}
      >
        <Text style={styles.btnText}>{ttsStatus.kind === "busy" ? "Speaking…" : "Speak it"}</Text>
      </Pressable>
      <StatusLine status={ttsStatus} />
    </ScrollView>
  );
}

function ChipRow({
  options,
  selected,
  onSelect,
  disabled,
}: {
  options: { id: string; label: string }[];
  selected: string;
  onSelect: (id: string) => void;
  disabled?: boolean;
}) {
  return (
    <View style={styles.chipRow}>
      {options.map((o) => {
        const active = o.id === selected;
        return (
          <Pressable
            key={o.id}
            onPress={() => !disabled && onSelect(o.id)}
            style={[
              styles.chip,
              { borderColor: active ? "#2563eb" : "#333", backgroundColor: active ? "#1e293b" : "transparent", opacity: disabled ? 0.5 : 1 },
            ]}
          >
            <Text style={{ color: active ? "#60a5fa" : "#cbd5e1", fontSize: 13 }}>{o.label}</Text>
          </Pressable>
        );
      })}
    </View>
  );
}

function StatusLine({ status }: { status: Status }) {
  if (status.kind === "idle") return null;
  const color =
    status.kind === "ok" ? "#22c55e" : status.kind === "error" ? "#ef4444" : "#94a3b8";
  return (
    <View style={styles.statusRow}>
      {status.kind === "busy" && <ActivityIndicator size="small" color="#94a3b8" />}
      <Text style={[styles.statusText, { color }]}>{status.msg}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: "#0a0a0a" },
  content: { padding: 16, paddingBottom: 48 },
  title: { color: "#fff", fontSize: 22, fontWeight: "700" },
  subtitle: { color: "#94a3b8", fontSize: 13, marginTop: 4, marginBottom: 8 },
  section: { color: "#fff", fontSize: 16, fontWeight: "600", marginTop: 16, marginBottom: 8 },
  hint: { color: "#64748b", fontSize: 12, marginTop: 6, marginBottom: 4 },
  chipRow: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  chip: { borderWidth: 1, borderRadius: 16, paddingHorizontal: 12, paddingVertical: 6 },
  input: {
    borderWidth: 1,
    borderColor: "#333",
    borderRadius: 8,
    color: "#fff",
    paddingHorizontal: 12,
    paddingVertical: 10,
    marginTop: 10,
    fontSize: 14,
  },
  multiline: { minHeight: 64, textAlignVertical: "top" },
  btn: { borderRadius: 8, paddingVertical: 12, alignItems: "center", marginTop: 12 },
  btnText: { color: "#fff", fontWeight: "600", fontSize: 15 },
  resultBox: { borderWidth: 1, borderColor: "#1e293b", borderRadius: 8, padding: 12, marginTop: 12 },
  resultLabel: { color: "#64748b", fontSize: 11, textTransform: "uppercase", marginBottom: 4 },
  resultText: { color: "#e2e8f0", fontSize: 15 },
  statusRow: { flexDirection: "row", alignItems: "center", gap: 8, marginTop: 8 },
  statusText: { fontSize: 13 },
});
