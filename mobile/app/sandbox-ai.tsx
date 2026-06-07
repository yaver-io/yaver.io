// app/sandbox-ai.tsx — choose the AI backend the Mobile Sandbox uses to code.
// Every option is optional and user-controlled: on-device model, or a BYO-key
// cloud model (Claude / OpenAI / GLM), or "Auto" which picks on-device first
// then the strongest available key. Keys are stored in the device keychain
// (SecureStore), never synced.

import React, { useCallback, useState } from "react";
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
import { useFocusEffect, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useColors } from "../src/context/ThemeContext";
import { AppBackButton } from "../src/components/AppBackButton";
import {
  CODING_BACKENDS,
  backendUsable,
  resolveAutoBackend,
  type CodingBackendAvailability,
  type CodingBackendId,
  type CodingBackendPref,
} from "../src/lib/codingBackend";
import {
  loadCodingAvailability,
  loadCodingBackendPref,
  saveCodingBackendPref,
} from "../src/lib/codingBackendStore";
import { LOCAL_KEYS, getLocalSecret, saveLocalSecret, deleteLocalSecret } from "../src/lib/auth";
import { engineAvailable } from "../src/lib/localAgent/engine";

const KEY_SLOT: Record<"anthropic" | "openai" | "glm", string> = {
  anthropic: LOCAL_KEYS.anthropicApiKey,
  openai: LOCAL_KEYS.openAiApiKey,
  glm: LOCAL_KEYS.glmApiKey,
};

export default function SandboxAiScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();

  const [pref, setPref] = useState<CodingBackendPref>("auto");
  const [av, setAv] = useState<CodingBackendAvailability | null>(null);
  const [loading, setLoading] = useState(true);
  const [keyDrafts, setKeyDrafts] = useState<Record<string, string>>({});
  const [savingKey, setSavingKey] = useState<string | null>(null);

  const reload = useCallback(async () => {
    const [p, a] = await Promise.all([loadCodingBackendPref(), loadCodingAvailability()]);
    setPref(p);
    setAv(a);
    setLoading(false);
  }, []);

  useFocusEffect(
    useCallback(() => {
      void reload();
    }, [reload]),
  );

  const choose = useCallback(async (next: CodingBackendPref) => {
    setPref(next);
    await saveCodingBackendPref(next);
  }, []);

  const saveKey = useCallback(
    async (id: "anthropic" | "openai" | "glm") => {
      const draft = (keyDrafts[id] ?? "").trim();
      setSavingKey(id);
      try {
        if (draft) await saveLocalSecret(KEY_SLOT[id], draft);
        else await deleteLocalSecret(KEY_SLOT[id]);
        setKeyDrafts((d) => ({ ...d, [id]: "" }));
        await reload();
      } finally {
        setSavingKey(null);
      }
    },
    [keyDrafts, reload],
  );

  const autoTarget = av ? resolveAutoBackend(av) : null;

  const renderRow = (
    id: CodingBackendPref,
    label: string,
    note: string,
    usable: boolean,
    selected: boolean,
  ) => (
    <Pressable
      key={id}
      onPress={() => choose(id)}
      style={[
        styles.row,
        { borderColor: selected ? c.accent : c.border, backgroundColor: c.bgCard },
      ]}
    >
      <View style={[styles.radio, { borderColor: selected ? c.accent : c.border }]}>
        {selected ? <View style={[styles.radioDot, { backgroundColor: c.accent }]} /> : null}
      </View>
      <View style={{ flex: 1 }}>
        <View style={{ flexDirection: "row", alignItems: "center" }}>
          <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{label}</Text>
          {!usable && id !== "auto" ? (
            <Text style={{ color: c.textMuted, fontSize: 11, marginLeft: 8 }}>· not set up</Text>
          ) : null}
        </View>
        <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>{note}</Text>
      </View>
    </Pressable>
  );

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg }}
    >
      <View style={{ paddingTop: insets.top + 8 }}>
        <View style={styles.header}>
          <AppBackButton onPress={() => router.back()} />
          <View style={{ marginLeft: 8 }}>
            <Text style={[styles.h1, { color: c.textPrimary }]}>Sandbox AI</Text>
            <Text style={{ color: c.textMuted, fontSize: 12 }}>
              How the Mobile Sandbox writes code
            </Text>
          </View>
        </View>
      </View>

      {loading || !av ? (
        <View style={styles.center}>
          <ActivityIndicator color={c.textMuted} />
        </View>
      ) : (
        <ScrollView
          contentContainerStyle={{ padding: 12, paddingBottom: insets.bottom + 40 }}
        >
          <Text style={[styles.section, { color: c.textSecondary }]}>BACKEND</Text>

          {renderRow(
            "auto",
            autoTarget ? `Auto · ${CODING_BACKENDS.find((b) => b.id === autoTarget)?.label}` : "Auto",
            autoTarget
              ? "On-device first, then your best cloud key. Recommended."
              : "Nothing is set up yet — add a key or download a model below.",
            true,
            pref === "auto",
          )}

          {CODING_BACKENDS.map((b) =>
            renderRow(b.id, b.label, b.note, backendUsable(b.id, av), pref === b.id),
          )}

          {/* On-device status */}
          <Text style={[styles.section, { color: c.textSecondary, marginTop: 18 }]}>
            ON-DEVICE MODEL
          </Text>
          <View style={[styles.card, { borderColor: c.border, backgroundColor: c.bgCard }]}>
            <Text style={{ color: c.textPrimary, fontSize: 13 }}>
              {av.localModelReady
                ? "A coder model is downloaded and ready."
                : engineAvailable()
                  ? "No coder model downloaded yet."
                  : "On-device inference isn't available in this build yet."}
            </Text>
            <Pressable
              onPress={() => router.push("/local-models")}
              style={[styles.linkBtn, { borderColor: c.border }]}
            >
              <Text style={{ color: c.accent, fontWeight: "600" }}>Manage on-device models →</Text>
            </Pressable>
          </View>

          {/* BYO keys */}
          <Text style={[styles.section, { color: c.textSecondary, marginTop: 18 }]}>
            BRING-YOUR-OWN KEYS
          </Text>
          {(["anthropic", "openai", "glm"] as const).map((id) => {
            const has =
              id === "anthropic" ? av.anthropicKey : id === "openai" ? av.openaiKey : av.glmKey;
            const meta = CODING_BACKENDS.find((b) => b.id === id)!;
            return (
              <View
                key={id}
                style={[styles.card, { borderColor: c.border, backgroundColor: c.bgCard }]}
              >
                <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                  <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{meta.label}</Text>
                  <Text style={{ color: has ? "#4caf50" : c.textMuted, fontSize: 12 }}>
                    {has ? "saved" : "no key"}
                  </Text>
                </View>
                <View style={{ flexDirection: "row", marginTop: 8 }}>
                  <TextInput
                    value={keyDrafts[id] ?? ""}
                    onChangeText={(t) => setKeyDrafts((d) => ({ ...d, [id]: t }))}
                    placeholder={has ? "Replace key (leave blank + Save to remove)" : "Paste API key"}
                    placeholderTextColor={c.textMuted}
                    autoCapitalize="none"
                    autoCorrect={false}
                    secureTextEntry
                    style={[styles.keyInput, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
                  />
                  <Pressable
                    onPress={() => saveKey(id)}
                    style={[styles.saveBtn, { backgroundColor: c.accent, marginLeft: 8 }]}
                  >
                    {savingKey === id ? (
                      <ActivityIndicator color={c.bg} />
                    ) : (
                      <Text style={{ color: c.bg, fontWeight: "600" }}>Save</Text>
                    )}
                  </Pressable>
                </View>
              </View>
            );
          })}

          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 14, lineHeight: 16 }}>
            Keys are stored only in this device's keychain and used directly from the phone — they
            are never sent to Yaver's servers. On-device models run fully offline.
          </Text>
        </ScrollView>
      )}
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  header: { flexDirection: "row", alignItems: "center", paddingHorizontal: 12, paddingBottom: 10 },
  h1: { fontSize: 18, fontWeight: "700" },
  center: { flex: 1, alignItems: "center", justifyContent: "center" },
  section: { fontSize: 11, fontWeight: "700", letterSpacing: 0.5, marginBottom: 8 },
  row: {
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 1,
    borderRadius: 10,
    padding: 12,
    marginBottom: 8,
  },
  radio: {
    width: 20,
    height: 20,
    borderRadius: 10,
    borderWidth: 2,
    marginRight: 12,
    alignItems: "center",
    justifyContent: "center",
  },
  radioDot: { width: 10, height: 10, borderRadius: 5 },
  card: { borderWidth: 1, borderRadius: 10, padding: 12, marginBottom: 8 },
  linkBtn: { marginTop: 10, borderWidth: 1, borderRadius: 8, paddingVertical: 8, alignItems: "center" },
  keyInput: { flex: 1, borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 8, fontSize: 13 },
  saveBtn: { borderRadius: 8, paddingHorizontal: 16, alignItems: "center", justifyContent: "center" },
});
