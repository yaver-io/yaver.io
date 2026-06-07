/**
 * Voice provider picker — the first-class Settings screen for the
 * Yaver trio that wants voice enabled.
 *
 * Deep-link from anywhere: router.push("/voice-config")
 *
 * Flow:
 *   1. Fetch /voice/status to know current state (provider, keys-set,
 *      availableProviders).
 *   2. Pick OpenAI (default, simplest) / Deepgram+Cartesia (faster) /
 *      Mix (one of each).
 *   3. Paste API keys. Already-set keys show as "•••••• (configured)" —
 *      the user replaces only if they want to rotate.
 *   4. Save → POST /voice/config → agent writes each key to the
 *      encrypted vault (project="voice"), P2P-syncs it to your other
 *      devices, and confirms via the sanitized status response.
 *   5. Skip → keyboard-only mode. No keys saved. Voice orb hides itself
 *      across all surfaces.
 *
 * Privacy: keys travel ONCE over the owner-auth transport to the agent,
 * land in the local vault (NaCl secretbox at rest), sync only to your
 * own devices, and NEVER touch Convex (convex_privacy_test forbidden-
 * keys fence). The agent reads them at request time — no restart.
 */

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { AppBackButton } from "../src/components/AppBackButton";
import { SafeAreaView } from "react-native-safe-area-context";
import { useColors } from "../src/context/ThemeContext";
import { quicClient } from "../src/lib/quic";
import { YaverGlass } from "../src/components/YaverGlass";

type SttProvider = "openai" | "deepgram" | "assemblyai" | "on-device";
type TtsProvider = "openai" | "cartesia" | "deepgram" | "elevenlabs" | "device";

interface VoiceStatus {
  enabled?: boolean;
  sttProvider?: SttProvider;
  ttsProvider?: TtsProvider;
  sttReady?: boolean;
  ttsReady?: boolean;
  defaultProject?: string;
  openaiSet?: boolean;
  deepgramSet?: boolean;
  cartesiaSet?: boolean;
  assemblyaiSet?: boolean;
  elevenlabsSet?: boolean;
  availableProviders?: { stt?: string[]; tts?: string[] };
}

export default function VoiceConfigScreen(): React.JSX.Element {
  const c = useColors();
  const router = useRouter();

  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState("");
  const [saving, setSaving] = useState(false);

  // Hydrated from /voice/status on mount
  const [enabled, setEnabled] = useState(false);
  const [sttProvider, setSttProvider] = useState<SttProvider>("openai");
  const [ttsProvider, setTtsProvider] = useState<TtsProvider>("openai");
  const [openaiSet, setOpenaiSet] = useState(false);
  const [deepgramSet, setDeepgramSet] = useState(false);
  const [cartesiaSet, setCartesiaSet] = useState(false);
  const [assemblyaiSet, setAssemblyaiSet] = useState(false);
  const [elevenlabsSet, setElevenlabsSet] = useState(false);
  const [defaultProject, setDefaultProject] = useState("");

  // Local-only — typed by user, sent on Save. Empty = "leave alone".
  const [openaiKey, setOpenaiKey] = useState("");
  const [deepgramKey, setDeepgramKey] = useState("");
  const [cartesiaKey, setCartesiaKey] = useState("");
  const [assemblyaiKey, setAssemblyaiKey] = useState("");
  const [elevenlabsKey, setElevenlabsKey] = useState("");

  // Initial fetch
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await fetch(`${quicClient.baseUrl}/voice/status`, {
          headers: quicClient.getAuthHeaders(),
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const body: VoiceStatus = await res.json();
        if (cancelled) return;
        setEnabled(!!body.enabled);
        setSttProvider((body.sttProvider as SttProvider) ?? "openai");
        setTtsProvider((body.ttsProvider as TtsProvider) ?? "openai");
        // /voice/status returns per-provider booleans (agent v1.99.220+).
        // Older agents only return sttReady/ttsReady — fall back to
        // inferring from the currently-selected provider.
        setOpenaiSet(
          body.openaiSet ??
          (body.sttProvider === "openai" ? !!body.sttReady : body.ttsProvider === "openai" ? !!body.ttsReady : false),
        );
        setDeepgramSet(
          body.deepgramSet ??
          ((body.sttProvider === "deepgram" && !!body.sttReady) || (body.ttsProvider === "deepgram" && !!body.ttsReady)),
        );
        setCartesiaSet(body.cartesiaSet ?? (body.ttsProvider === "cartesia" ? !!body.ttsReady : false));
        setAssemblyaiSet(body.assemblyaiSet ?? (body.sttProvider === "assemblyai" ? !!body.sttReady : false));
        setElevenlabsSet(body.elevenlabsSet ?? (body.ttsProvider === "elevenlabs" ? !!body.ttsReady : false));
        setDefaultProject(body.defaultProject ?? "");
      } catch (e: any) {
        if (!cancelled) setErrorMsg(e?.message ?? String(e));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  const save = useCallback(async () => {
    setSaving(true);
    setErrorMsg("");
    const body: Record<string, unknown> = {
      enabled: true,
      sttProvider,
      ttsProvider,
      defaultProject,
    };
    if (openaiKey.trim()) body.openaiApiKey = openaiKey.trim();
    if (deepgramKey.trim()) body.deepgramApiKey = deepgramKey.trim();
    if (cartesiaKey.trim()) body.cartesiaApiKey = cartesiaKey.trim();
    if (assemblyaiKey.trim()) body.assemblyaiApiKey = assemblyaiKey.trim();
    if (elevenlabsKey.trim()) body.elevenlabsApiKey = elevenlabsKey.trim();
    try {
      const res = await fetch(`${quicClient.baseUrl}/voice/config`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...quicClient.getAuthHeaders() },
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        const t = await res.text().catch(() => "");
        throw new Error(`HTTP ${res.status}: ${t.slice(0, 200)}`);
      }
      const result = await res.json();
      setEnabled(!!result.enabled);
      setOpenaiSet(!!result.openaiSet);
      setDeepgramSet(!!result.deepgramSet);
      setCartesiaSet(!!result.cartesiaSet);
      setAssemblyaiSet(!!result.assemblyaiSet);
      setElevenlabsSet(!!result.elevenlabsSet);
      setOpenaiKey("");
      setDeepgramKey("");
      setCartesiaKey("");
      setAssemblyaiKey("");
      setElevenlabsKey("");
      Alert.alert("Saved", "Voice config updated. Keys are in your agent's vault and take effect on the next voice turn — no restart needed.");
      router.back();
    } catch (e: any) {
      setErrorMsg(e?.message ?? String(e));
    } finally {
      setSaving(false);
    }
  }, [sttProvider, ttsProvider, openaiKey, deepgramKey, cartesiaKey, assemblyaiKey, elevenlabsKey, defaultProject, router]);

  const disableVoice = useCallback(async () => {
    setSaving(true);
    try {
      const res = await fetch(`${quicClient.baseUrl}/voice/config`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...quicClient.getAuthHeaders() },
        body: JSON.stringify({ enabled: false }),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      router.back();
    } catch (e: any) {
      setErrorMsg(e?.message ?? String(e));
    } finally {
      setSaving(false);
    }
  }, [router]);

  if (loading) {
    return (
      <SafeAreaView style={{ flex: 1, backgroundColor: c.bg, alignItems: "center", justifyContent: "center" }}>
        <ActivityIndicator color={c.accent} />
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
      <View style={styles.headerRow}>
        <AppBackButton variant="icon" color={c.textPrimary} onPress={() => router.back()} />
        <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "600" }}>Voice</Text>
        <Pressable onPress={() => router.push("/voice-test")} hitSlop={12}>
          <Ionicons name="mic-circle-outline" size={26} color={c.textPrimary} />
        </Pressable>
      </View>

      <ScrollView contentContainerStyle={{ padding: 16, gap: 16, paddingBottom: 40 }}>
        <YaverGlass tint={c.bgCard} style={{ borderRadius: 12, overflow: "hidden" }}>
          <View style={{ padding: 16, gap: 8 }}>
            <Text style={{ color: c.textPrimary, fontWeight: 600, fontSize: 14 }}>
              How voice works
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 18 }}>
              <Text style={{ color: c.textPrimary }}>STT</Text> (speech‑to‑text) turns
              your voice into prompts. <Text style={{ color: c.textPrimary }}>TTS</Text>{" "}
              (text‑to‑speech) reads replies back. Use either, both, or neither.
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 18 }}>
              <Text style={{ color: c.textPrimary }}>Local</Text> (on‑device Whisper +
              the system voice) is free and works offline — no key needed, and it&apos;s
              the default. <Text style={{ color: c.textPrimary }}>Cloud</Text> engines
              below are optional and faster; they need your own key.
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 18 }}>
              When voice is on, the agent keeps the spoken headline short and puts the
              detail on screen — so answers read well out loud.
            </Text>
          </View>
        </YaverGlass>

        <YaverGlass tint={c.bgCard} style={{ borderRadius: 12, overflow: "hidden" }}>
          <View style={{ padding: 16, gap: 10 }}>
            <Text style={{ color: c.textPrimary, fontWeight: 600, fontSize: 14 }}>
              Subscription OAuth only · never API keys for the runners
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 12, lineHeight: 17 }}>
              Pick STT + TTS providers. Yaver doesn't ship default keys — you own
              the billing relationship. Keys are saved to the <Text style={{ fontFamily: "Menlo" }}>voice</Text> vault
              on your agent — encrypted at rest, P2P-synced to your own devices, never on Convex.
            </Text>
          </View>
        </YaverGlass>

        <Section title="Speech-to-text">
          <ProviderRow
            current={sttProvider}
            choice="openai"
            label="OpenAI (default · whisper)"
            sub="One key for STT + TTS. Easiest if you already have an OpenAI account."
            keySet={openaiSet}
            onSelect={() => setSttProvider("openai")}
            c={c}
          />
          <ProviderRow
            current={sttProvider}
            choice="deepgram"
            label="Deepgram Flux"
            sub="Faster (<300ms first-word) + model-integrated end-of-turn detection."
            keySet={deepgramSet}
            onSelect={() => setSttProvider("deepgram")}
            c={c}
          />
          <ProviderRow
            current={sttProvider}
            choice="assemblyai"
            label="AssemblyAI Universal-Streaming"
            sub="99+ languages, cheapest of the lot (~$0.0025/min). Server-detected end-of-turn."
            keySet={assemblyaiSet}
            onSelect={() => setSttProvider("assemblyai")}
            c={c}
          />
          <ProviderRow
            current={sttProvider}
            choice="on-device"
            label="On-device (whisper.rn)"
            sub="$0, works offline. Tiny model bundled in the app. Phone-only — agent rejects /voice/stream for this pick."
            keySet={true}
            onSelect={() => setSttProvider("on-device")}
            c={c}
          />
        </Section>

        <Section title="Text-to-speech">
          <ProviderRow
            current={ttsProvider}
            choice="openai"
            label="OpenAI gpt-4o-mini-tts"
            sub="Same key as STT. 10 voices, 300-600ms TTFA."
            keySet={openaiSet}
            onSelect={() => setTtsProvider("openai")}
            c={c}
          />
          <ProviderRow
            current={ttsProvider}
            choice="deepgram"
            label="Deepgram Aura-2"
            sub="Same key as Deepgram STT — one signup covers the whole loop. ~$30 / M chars."
            keySet={deepgramSet}
            onSelect={() => setTtsProvider("deepgram")}
            c={c}
          />
          <ProviderRow
            current={ttsProvider}
            choice="cartesia"
            label="Cartesia Sonic-3"
            sub="40ms TTFA, premium voice quality, $35 / M chars."
            keySet={cartesiaSet}
            onSelect={() => setTtsProvider("cartesia")}
            c={c}
          />
          <ProviderRow
            current={ttsProvider}
            choice="elevenlabs"
            label="ElevenLabs Flash v2.5"
            sub="~75ms TTFA, 32 languages, top voice character. Rachel default voice; rotate via cfg."
            keySet={elevenlabsSet}
            onSelect={() => setTtsProvider("elevenlabs")}
            c={c}
          />
          <ProviderRow
            current={ttsProvider}
            choice="device"
            label="On-device (Apple AVSpeech / Android TTS)"
            sub="$0, no network. System voice — quality varies. Mobile plays locally; agent skips the TTS leg."
            keySet={true}
            onSelect={() => setTtsProvider("device")}
            c={c}
          />
        </Section>

        <Section title="API keys">
          <KeyField
            label="OpenAI"
            placeholder={openaiSet ? "•••••• configured · paste to rotate" : "sk-..."}
            value={openaiKey}
            onChange={setOpenaiKey}
            url="https://platform.openai.com/api-keys"
            c={c}
            needed={sttProvider === "openai" || ttsProvider === "openai"}
          />
          <KeyField
            label="Deepgram"
            placeholder={deepgramSet ? "•••••• configured · paste to rotate" : "da-..."}
            value={deepgramKey}
            onChange={setDeepgramKey}
            url="https://console.deepgram.com"
            c={c}
            needed={sttProvider === "deepgram" || ttsProvider === "deepgram"}
          />
          <KeyField
            label="Cartesia"
            placeholder={cartesiaSet ? "•••••• configured · paste to rotate" : "ck_..."}
            value={cartesiaKey}
            onChange={setCartesiaKey}
            url="https://play.cartesia.ai/keys"
            c={c}
            needed={ttsProvider === "cartesia"}
          />
          <KeyField
            label="AssemblyAI"
            placeholder={assemblyaiSet ? "•••••• configured · paste to rotate" : "..."}
            value={assemblyaiKey}
            onChange={setAssemblyaiKey}
            url="https://www.assemblyai.com/app/account"
            c={c}
            needed={sttProvider === "assemblyai"}
          />
          <KeyField
            label="ElevenLabs"
            placeholder={elevenlabsSet ? "•••••• configured · paste to rotate" : "sk_..."}
            value={elevenlabsKey}
            onChange={setElevenlabsKey}
            url="https://elevenlabs.io/app/settings/api-keys"
            c={c}
            needed={ttsProvider === "elevenlabs"}
          />
        </Section>

        {errorMsg ? (
          <View style={{ padding: 12, backgroundColor: "rgba(239,68,68,0.12)", borderRadius: 8, borderWidth: 1, borderColor: "rgba(239,68,68,0.4)" }}>
            <Text style={{ color: "#ef4444", fontSize: 12 }}>{errorMsg}</Text>
          </View>
        ) : null}

        <Pressable
          onPress={save}
          disabled={saving}
          style={[styles.primaryBtn, { backgroundColor: c.accent, opacity: saving ? 0.7 : 1 }]}
        >
          <Text style={{ color: "#fff", fontSize: 15, fontWeight: 600 }}>
            {saving ? "Saving…" : enabled ? "Update voice settings" : "Enable voice"}
          </Text>
        </Pressable>

        {enabled && (
          <Pressable onPress={disableVoice} style={styles.secondaryBtn}>
            <Text style={{ color: c.textMuted, fontSize: 13 }}>Disable voice (keyboard-only mode)</Text>
          </Pressable>
        )}

        <Text style={{ color: c.textMuted, fontSize: 11, textAlign: "center", marginTop: 8, lineHeight: 16 }}>
          Voice is optional. The Yaver trio (phone + glasses + BT keyboard) works
          fully without voice keys — just skip this screen and use the keyboard.
        </Text>
      </ScrollView>
    </SafeAreaView>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  const c = useColors();
  return (
    <View style={{ gap: 8 }}>
      <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: 700, textTransform: "uppercase", letterSpacing: 0.5 }}>
        {title}
      </Text>
      <View style={{ gap: 8 }}>{children}</View>
    </View>
  );
}

function ProviderRow({ current, choice, label, sub, keySet, onSelect, c }: {
  current: string; choice: string; label: string; sub: string; keySet: boolean;
  onSelect: () => void; c: ReturnType<typeof useColors>;
}) {
  const selected = current === choice;
  return (
    <Pressable onPress={onSelect}>
      <YaverGlass tint={selected ? c.accent + "22" : c.bgCard} style={{
        borderRadius: 10, overflow: "hidden",
        borderWidth: 1, borderColor: selected ? c.accent + "88" : c.border,
      }}>
        <View style={{ padding: 12, flexDirection: "row", alignItems: "flex-start", gap: 10 }}>
          <Ionicons
            name={selected ? "radio-button-on" : "radio-button-off"}
            size={20}
            color={selected ? c.accent : c.textMuted}
            style={{ marginTop: 1 }}
          />
          <View style={{ flex: 1 }}>
            <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: 600 }}>
              {label}{" "}
              {keySet ? (
                <Text style={{ color: "#10b981", fontSize: 10, fontWeight: 500 }}>· key set ✓</Text>
              ) : null}
            </Text>
            <Text style={{ color: c.textMuted, fontSize: 11, lineHeight: 16, marginTop: 2 }}>{sub}</Text>
          </View>
        </View>
      </YaverGlass>
    </Pressable>
  );
}

function KeyField({ label, placeholder, value, onChange, url, c, needed }: {
  label: string; placeholder: string; value: string; onChange: (v: string) => void;
  url: string; c: ReturnType<typeof useColors>; needed: boolean;
}) {
  return (
    <View style={{ gap: 4, opacity: needed ? 1 : 0.55 }}>
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: 600 }}>
          {label}{" "}
          {needed ? null : <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: 400 }}>· not needed for current selection</Text>}
        </Text>
        <Text style={{ color: c.textMuted, fontSize: 9, fontFamily: "Menlo" }} numberOfLines={1}>
          {url.replace(/^https?:\/\//, "")}
        </Text>
      </View>
      <TextInput
        value={value}
        onChangeText={onChange}
        placeholder={placeholder}
        placeholderTextColor={c.textMuted}
        autoCapitalize="none"
        autoCorrect={false}
        secureTextEntry
        style={{
          color: c.textPrimary,
          fontFamily: "Menlo",
          fontSize: 12,
          backgroundColor: c.bg,
          borderWidth: 1,
          borderColor: c.border,
          borderRadius: 8,
          padding: 10,
        }}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 10,
  },
  primaryBtn: {
    paddingVertical: 13,
    borderRadius: 10,
    alignItems: "center",
    marginTop: 8,
  },
  secondaryBtn: {
    paddingVertical: 12,
    alignItems: "center",
  },
});
