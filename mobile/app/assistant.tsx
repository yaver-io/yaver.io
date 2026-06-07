// app/assistant.tsx — the on-device voice concierge: push-to-talk → local
// STT (whisper.rn) → the deterministic ladder/brain → safe device action → TTS.
// Wires useVoiceHelper + interpreter + AgentActionChips, which were built and
// tested but previously unmounted.
//
// Platform-aware: the model tier comes from the device's measured capability,
// so a low-RAM phone runs the helper in scripted mode (keyword goals + the
// deterministic ladder, which still onboards / troubleshoots / runs safe
// actions) instead of loading a model it can't hold. The free-form model path
// lights up only when llama.rn + a runnable router GGUF are present.

import React, { useCallback, useMemo, useState } from "react";
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useColors } from "../src/context/ThemeContext";
import { AppBackButton } from "../src/components/AppBackButton";
import AgentActionChips from "../src/components/AgentActionChips";
import { useVoiceHelper } from "../src/lib/localAgent/useVoiceHelper";
import { interpretMessage, type ActionChip } from "../src/lib/localAgent/interpreter";
import { useDeviceCapability } from "../src/lib/deviceCapability";
import { capabilityClassLabel } from "../src/lib/deviceCapabilityCore";

export default function AssistantScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { tier } = useDeviceCapability();

  const helper = useVoiceHelper({ localTier: tier });
  const [typed, setTyped] = useState("");
  const [busyChip, setBusyChip] = useState<string | null>(null);

  // Derive contextual action chips from whatever the helper last said.
  const interpreted = useMemo(
    () => (helper.lastSpoken ? interpretMessage(helper.lastSpoken) : { chips: [], needsLlm: false }),
    [helper.lastSpoken],
  );

  const onChip = useCallback(
    async (chip: ActionChip) => {
      setBusyChip(chip.actionId);
      try {
        await helper.say(chip.label);
      } finally {
        setBusyChip(null);
      }
    },
    [helper],
  );

  const sendTyped = useCallback(async () => {
    const t = typed.trim();
    if (!t) return;
    setTyped("");
    await helper.say(t);
  }, [typed, helper]);

  const micState = helper.listening ? "listening" : helper.thinking ? "thinking" : "idle";

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg }}
    >
      <View style={{ paddingTop: insets.top + 8 }}>
        <View style={styles.header}>
          <AppBackButton onPress={() => router.back()} />
          <View style={{ marginLeft: 8, flex: 1 }}>
            <Text style={[styles.h1, { color: c.textPrimary }]}>Assistant</Text>
            <Text style={{ color: c.textMuted, fontSize: 12 }}>
              Voice control · {capabilityClassLabel(tier).split(" — ")[0]}
            </Text>
          </View>
        </View>
      </View>

      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 24 }}>
        <View style={[styles.caption, { borderColor: c.border, backgroundColor: c.bgCard }]}>
          {helper.lastSpoken ? (
            <Text style={{ color: c.textPrimary, fontSize: 15, lineHeight: 21 }}>
              {helper.lastSpoken}
            </Text>
          ) : (
            <Text style={{ color: c.textMuted, fontSize: 14 }}>
              Hold the mic and ask me to connect a machine, reconnect, switch a coding agent,
              check status, or reload — I'll guide you and run the safe ones for you.
            </Text>
          )}
          {helper.awaiting ? (
            <Text style={{ color: c.accent, fontSize: 12, marginTop: 8 }}>
              Waiting for your confirmation — say “yes” or tap a chip.
            </Text>
          ) : null}
        </View>

        <AgentActionChips
          summary={interpreted.chips.length ? undefined : undefined}
          chips={interpreted.chips}
          busyActionId={busyChip}
          onChip={onChip}
        />

        {/* Typed fallback (and an accessibility path when the mic is unavailable). */}
        <View style={{ flexDirection: "row", marginTop: 16 }}>
          <TextInput
            value={typed}
            onChangeText={setTyped}
            placeholder="…or type a command"
            placeholderTextColor={c.textMuted}
            style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgCard }]}
            onSubmitEditing={sendTyped}
            returnKeyType="send"
          />
          <Pressable
            onPress={sendTyped}
            disabled={!typed.trim() || helper.thinking}
            style={[styles.sendBtn, { backgroundColor: typed.trim() ? c.accent : c.bgCard, borderColor: c.border }]}
          >
            <Text style={{ color: typed.trim() ? c.bg : c.textMuted, fontWeight: "600" }}>Send</Text>
          </Pressable>
        </View>
      </ScrollView>

      {/* Push-to-talk mic */}
      <View style={[styles.micBar, { paddingBottom: insets.bottom + 12, borderTopColor: c.border }]}>
        <Pressable
          onPressIn={() => void helper.startListening()}
          onPressOut={() => void helper.stopListening()}
          style={[
            styles.mic,
            {
              backgroundColor:
                micState === "listening" ? c.accent : micState === "thinking" ? c.bgCard : c.bgCard,
              borderColor: micState === "listening" ? c.accent : c.border,
            },
          ]}
        >
          {micState === "thinking" ? (
            <ActivityIndicator color={c.accent} />
          ) : (
            <Text style={{ color: micState === "listening" ? c.bg : c.textPrimary, fontWeight: "700" }}>
              {micState === "listening" ? "● Listening — release to send" : "🎙  Hold to talk"}
            </Text>
          )}
        </Pressable>
      </View>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  header: { flexDirection: "row", alignItems: "center", paddingHorizontal: 12, paddingBottom: 10 },
  h1: { fontSize: 18, fontWeight: "700" },
  caption: { borderWidth: 1, borderRadius: 12, padding: 14, minHeight: 80 },
  input: { flex: 1, borderWidth: 1, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 10, fontSize: 14 },
  sendBtn: { marginLeft: 8, borderWidth: 1, borderRadius: 10, paddingHorizontal: 18, alignItems: "center", justifyContent: "center" },
  micBar: { paddingHorizontal: 16, paddingTop: 12, borderTopWidth: 1 },
  mic: { borderWidth: 1.5, borderRadius: 14, paddingVertical: 18, alignItems: "center", justifyContent: "center" },
});
