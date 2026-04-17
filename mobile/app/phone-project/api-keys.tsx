import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Clipboard,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import {
  PhoneProjectTokenMint,
  PhoneProjectTokenSummary,
  listPhoneProjectTokens,
  mintPhoneProjectToken,
  revokePhoneProjectToken,
} from "../../src/lib/phoneProjects";

// "API keys" screen — the surface the developer uses to mint scoped tokens
// for the third-party RN / web app they're shipping to end users. Raw
// plaintext is shown ONCE per mint; afterward we only know the label +
// timestamps. Revoke is instant-effect (server re-reads tokens.yaml on
// every data-API request).

export default function PhoneAPIKeysScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const slugStr = String(slug ?? "");

  const [tokens, setTokens] = useState<PhoneProjectTokenSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);

  const [label, setLabel] = useState("web app");
  const [minting, setMinting] = useState(false);
  // Plaintext of the just-minted token — survives one render cycle so the
  // user can copy it. After they dismiss, we drop it from memory.
  const [justMinted, setJustMinted] = useState<PhoneProjectTokenMint | null>(null);

  const load = useCallback(async () => {
    if (!slugStr) return;
    setLoading(true);
    setErr(null);
    try {
      const list = await listPhoneProjectTokens(slugStr);
      setTokens(list);
    } catch (e: any) {
      setErr(e?.message ?? "failed to load");
    } finally {
      setLoading(false);
    }
  }, [slugStr]);

  useEffect(() => {
    void load();
  }, [load]);

  async function mint() {
    if (!label.trim()) {
      Alert.alert("Label required", "Give this key a short human name (e.g. 'iOS app', 'web staging').");
      return;
    }
    setMinting(true);
    try {
      const r = await mintPhoneProjectToken(slugStr, label.trim());
      if (!r) throw new Error("agent returned nothing");
      setJustMinted(r);
      setLabel("");
      await load();
    } catch (e: any) {
      Alert.alert("Mint failed", e?.message ?? String(e));
    } finally {
      setMinting(false);
    }
  }

  async function revoke(t: PhoneProjectTokenSummary) {
    Alert.alert(
      `Revoke ${t.label}?`,
      "Apps using this key will stop working immediately. Mint a new one if they need to come back.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Revoke",
          style: "destructive",
          onPress: async () => {
            const ok = await revokePhoneProjectToken(slugStr, t.id);
            if (!ok) Alert.alert("Revoke failed");
            await load();
          },
        },
      ],
    );
  }

  function copyRaw() {
    if (!justMinted) return;
    // Clipboard API is deprecated-but-alive in RN; the non-deprecated
    // replacement is @react-native-clipboard/clipboard, but we stay with
    // the built-in to avoid adding a dep just for this screen.
    Clipboard.setString(justMinted.raw);
    Alert.alert("Copied", "API key copied to clipboard. Paste it into your app's env / config.");
  }

  return (
    <ScrollView
      style={{ backgroundColor: c.bg }}
      contentContainerStyle={{ paddingTop: insets.top + 8, paddingBottom: 60 + insets.bottom }}
    >
      <View style={{ paddingHorizontal: 16 }}>
        <Pressable onPress={() => router.back()}>
          <Text style={{ color: c.accent, marginBottom: 8 }}>‹ Back</Text>
        </Pressable>
        <Text style={[styles.h1, { color: c.textPrimary }]}>API keys</Text>
        <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 4 }}>
          Scoped tokens for the apps you ship to your users. Each key only unlocks{" "}
          <Text style={{ color: c.textPrimary }}>{slugStr}</Text> — revoking a key
          stops those apps immediately. Use the <Text style={{ color: c.textPrimary }}>yaver-sdk</Text> npm
          package to consume from React Native / web / Node.
        </Text>

        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginTop: 16 }]}>
          <Text style={[styles.label, { color: c.textMuted }]}>Label</Text>
          <TextInput
            value={label}
            onChangeText={setLabel}
            placeholder="web app / iOS app / staging / ..."
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <Pressable
            onPress={mint}
            disabled={minting}
            style={[styles.btn, { backgroundColor: c.accent, marginTop: 10, opacity: minting ? 0.6 : 1 }]}
          >
            {minting ? (
              <ActivityIndicator color={c.bg} />
            ) : (
              <Text style={{ color: c.bg, fontWeight: "600" }}>Mint new API key</Text>
            )}
          </Pressable>
        </View>

        {justMinted ? (
          <View
            style={[
              styles.card,
              {
                backgroundColor: c.bgCard,
                borderColor: c.success ?? "#22c55e",
                marginTop: 12,
              },
            ]}
          >
            <Text style={{ color: c.success ?? "#22c55e", fontWeight: "700", fontSize: 12 }}>
              ✓ Minted — copy NOW, it's shown once
            </Text>
            <Text
              selectable
              style={{
                color: c.textPrimary,
                fontFamily: "Menlo",
                fontSize: 11,
                marginTop: 8,
                backgroundColor: c.bg,
                padding: 8,
                borderRadius: 6,
              }}
            >
              {justMinted.raw}
            </Text>
            <View style={{ flexDirection: "row", gap: 8, marginTop: 10 }}>
              <Pressable
                onPress={copyRaw}
                style={[styles.btn, { backgroundColor: c.accent, flex: 1 }]}
              >
                <Text style={{ color: c.bg, fontWeight: "600" }}>Copy</Text>
              </Pressable>
              <Pressable
                onPress={() => setJustMinted(null)}
                style={[styles.btnSecondary, { borderColor: c.border, flex: 1 }]}
              >
                <Text style={{ color: c.textPrimary, fontWeight: "500" }}>Dismiss</Text>
              </Pressable>
            </View>
          </View>
        ) : null}

        <Text style={[styles.section, { color: c.textPrimary }]}>Active keys</Text>
        {loading ? (
          <ActivityIndicator color={c.textMuted} />
        ) : tokens.length === 0 ? (
          <Text style={{ color: c.textMuted, fontSize: 12 }}>
            No keys yet. Mint one above so your app can talk to this backend.
          </Text>
        ) : (
          tokens.map((t) => (
            <View
              key={t.id}
              style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginBottom: 8 }]}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{t.label}</Text>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>
                created {new Date(t.createdAt).toLocaleString()}
                {t.lastUsed ? ` · last used ${new Date(t.lastUsed).toLocaleString()}` : " · never used"}
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }}>id {t.id}</Text>
              <Pressable
                onPress={() => revoke(t)}
                style={[styles.btnSecondary, { borderColor: "#ff6b6b", marginTop: 8, alignSelf: "flex-start" }]}
              >
                <Text style={{ color: "#ff6b6b", fontWeight: "500", fontSize: 12 }}>Revoke</Text>
              </Pressable>
            </View>
          ))
        )}
        {err ? <Text style={{ color: "#ff6b6b", fontSize: 12, marginTop: 8 }}>{err}</Text> : null}

        <Text style={[styles.section, { color: c.textPrimary }]}>Consume from your app</Text>
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text
            selectable
            style={{
              color: c.textPrimary,
              fontFamily: "Menlo",
              fontSize: 11,
              lineHeight: 16,
            }}
          >
{`npm install yaver-sdk

import { createYaverBackendClient } from "yaver-sdk";

const yaver = createYaverBackendClient({
  baseUrl: "https://cloud.yaver.io",
  slug:    "${slugStr}",
  apiKey:  "pp_${slugStr}_...",
});

const todos = await yaver.collection("todos").list();
await yaver.collection("todos").insert({ id: "42", title: "hi" });`}
          </Text>
        </View>
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  h1: { fontSize: 24, fontWeight: "700" },
  label: { fontSize: 11, fontWeight: "500", marginBottom: 4, textTransform: "uppercase", letterSpacing: 0.5 },
  section: { fontSize: 13, fontWeight: "600", marginTop: 20, marginBottom: 8, textTransform: "uppercase", letterSpacing: 0.5 },
  card: { borderWidth: 1, borderRadius: 10, padding: 12 },
  input: { borderWidth: 1, borderRadius: 8, padding: 10, fontSize: 14 },
  btn: { paddingVertical: 12, borderRadius: 8, alignItems: "center" },
  btnSecondary: { paddingVertical: 8, paddingHorizontal: 12, borderRadius: 6, borderWidth: 1, alignItems: "center" },
});
