import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import * as Clipboard from "expo-clipboard";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient, type APIKeyRecord } from "../src/lib/quic";

// Mobile UI over /apikeys (desktop/agent/apikeys.go). Creating a key
// goes through the agent → Convex (CreateSdkToken) → registry, and the
// raw secret is returned exactly once. After that only the hash is
// queryable — consistent with how the CLI and web UI handle it.

const SCOPES = ["feedback", "blackbox", "voice", "builds", "testapp", "health", "todolist"];

export default function APIKeysScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [keys, setKeys] = useState<APIKeyRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const [label, setLabel] = useState("");
  const [scopes, setScopes] = useState<string[]>(["feedback"]);
  const [expiresDays, setExpiresDays] = useState("365");
  const [showForm, setShowForm] = useState(false);
  const [fresh, setFresh] = useState<{ label: string; token: string } | null>(null);

  const load = useCallback(async () => {
    if (!connected) return;
    setErr(null);
    try {
      const rows = await quicClient.apiKeyList();
      rows.sort((a, b) => a.label.localeCompare(b.label));
      setKeys(rows);
    } catch (e: any) {
      setErr(e?.message ?? "failed to load");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [connected]);

  useEffect(() => {
    setLoading(true);
    void load();
  }, [load]);

  function toggleScope(s: string) {
    setScopes((prev) => (prev.includes(s) ? prev.filter((x) => x !== s) : [...prev, s]));
  }

  async function create() {
    if (!label.trim()) {
      Alert.alert("API Keys", "Label is required");
      return;
    }
    try {
      const days = Number.parseInt(expiresDays, 10);
      const expiresInMs = Number.isFinite(days) && days > 0 ? days * 24 * 60 * 60 * 1000 : undefined;
      const out = await quicClient.apiKeyCreate({
        label: label.trim(),
        scopes,
        expiresInMs,
      });
      setFresh({ label: out.label, token: out.token });
      setLabel("");
      setShowForm(false);
      await load();
    } catch (e: any) {
      Alert.alert("API Keys", e?.message ?? "failed to create");
    }
  }

  async function disable(rec: APIKeyRecord) {
    Alert.alert("Disable?", `Disable "${rec.label}"? The underlying token stays in Convex.`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Disable",
        style: "destructive",
        onPress: async () => {
          try {
            await quicClient.apiKeyDisable(rec.label || rec.tokenHash.slice(0, 8));
            await load();
          } catch (e: any) {
            Alert.alert("API Keys", e?.message ?? "failed to disable");
          }
        },
      },
    ]);
  }

  async function copy(value: string) {
    await Clipboard.setStringAsync(value);
    Alert.alert("API Keys", "Copied to clipboard");
  }

  const renderItem = ({ item }: { item: APIKeyRecord }) => (
    <View style={[s.row, { backgroundColor: c.bgCard, borderColor: c.border }]}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Text
          style={{ color: c.textPrimary, flexShrink: 1 }}
          numberOfLines={1}
        >
          {item.label || "(unlabeled)"}
        </Text>
        <Text
          style={{ color: c.textMuted, fontFamily: "monospace", fontSize: 10 }}
        >
          {item.tokenHash.slice(0, 8)}
        </Text>
        {item.disabled ? (
          <Text
            style={{
              color: "#fecaca",
              backgroundColor: "#3f0a0a88",
              fontSize: 10,
              paddingHorizontal: 4,
              borderRadius: 2,
            }}
          >
            disabled
          </Text>
        ) : null}
      </View>
      <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
        {item.scopes?.length ? `scopes: ${item.scopes.join(", ")}` : "no scopes"}
      </Text>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>
        usage: {item.usageCount ?? 0}
        {item.lastUsedAt ? ` · last ${item.lastUsedAt}` : ""}
      </Text>
      {!item.disabled ? (
        <Pressable
          style={[s.btn, { marginTop: 8, backgroundColor: "#3f0a0a", borderColor: "#991b1b" }]}
          onPress={() => disable(item)}
        >
          <Text style={{ color: "#fecaca" }}>Disable</Text>
        </Pressable>
      ) : null}
    </View>
  );

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}
    >
      <View style={[s.header, { borderColor: c.border }]}>
        <Pressable onPress={() => router.back()}>
          <Text style={{ color: c.textMuted, fontSize: 20 }}>{"\u2039"}</Text>
        </Pressable>
        <Text style={[s.title, { color: c.textPrimary }]}>API Keys</Text>
        <Pressable onPress={() => setShowForm((v) => !v)}>
          <Text style={{ color: c.accent, fontSize: 18 }}>{showForm ? "\u00D7" : "+"}</Text>
        </Pressable>
      </View>

      {err ? (
        <View style={[s.err, { borderColor: "#991b1b" }]}>
          <Text style={{ color: "#fecaca" }}>{err}</Text>
        </View>
      ) : null}

      {fresh ? (
        <View style={[s.fresh, { borderColor: "#d97706" }]}>
          <Text style={{ color: "#fcd34d", fontWeight: "600" }}>
            Copy {fresh.label} — it will not be shown again.
          </Text>
          <Text
            style={{
              color: "#fef3c7",
              fontFamily: "monospace",
              fontSize: 11,
              marginTop: 6,
              padding: 6,
              backgroundColor: "#00000088",
              borderRadius: 4,
            }}
            selectable
          >
            {fresh.token}
          </Text>
          <View style={{ flexDirection: "row", gap: 8, marginTop: 8 }}>
            <Pressable
              style={[s.btn, { backgroundColor: c.accent }]}
              onPress={() => copy(fresh.token)}
            >
              <Text style={{ color: "#fff" }}>Copy</Text>
            </Pressable>
            <Pressable
              style={[s.btn, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={() => setFresh(null)}
            >
              <Text style={{ color: c.textPrimary }}>Dismiss</Text>
            </Pressable>
          </View>
        </View>
      ) : null}

      {showForm ? (
        <ScrollView
          style={{ maxHeight: 320 }}
          contentContainerStyle={[s.form, { backgroundColor: c.bgCard, borderColor: c.border }]}
        >
          <Text style={{ color: c.textMuted, fontSize: 11 }}>Label</Text>
          <TextInput
            value={label}
            onChangeText={setLabel}
            placeholder="BentoApp prod"
            placeholderTextColor={c.textMuted}
            style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Scopes</Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, marginTop: 4 }}>
            {SCOPES.map((sc) => (
              <Pressable
                key={sc}
                onPress={() => toggleScope(sc)}
                style={{
                  borderWidth: 1,
                  borderColor: scopes.includes(sc) ? c.accent : c.border,
                  backgroundColor: scopes.includes(sc) ? `${c.accent}22` : "transparent",
                  paddingHorizontal: 8,
                  paddingVertical: 4,
                  borderRadius: 4,
                }}
              >
                <Text
                  style={{ color: scopes.includes(sc) ? c.accent : c.textMuted, fontSize: 11 }}
                >
                  {sc}
                </Text>
              </Pressable>
            ))}
          </View>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Expires (days)</Text>
          <TextInput
            value={expiresDays}
            onChangeText={setExpiresDays}
            keyboardType="numeric"
            placeholder="365"
            placeholderTextColor={c.textMuted}
            style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <Pressable style={[s.saveBtn, { backgroundColor: c.accent }]} onPress={create}>
            <Text style={{ color: "#fff", fontWeight: "600" }}>Create</Text>
          </Pressable>
        </ScrollView>
      ) : null}

      {loading ? (
        <ActivityIndicator style={{ marginTop: 24 }} color={c.accent} />
      ) : (
        <FlatList
          data={keys}
          keyExtractor={(i) => i.tokenHash}
          renderItem={renderItem}
          contentContainerStyle={{ padding: 12, paddingBottom: insets.bottom + 24 }}
          refreshControl={
            <RefreshControl
              refreshing={refreshing}
              onRefresh={() => {
                setRefreshing(true);
                void load();
              }}
              tintColor={c.accent}
            />
          }
          ListEmptyComponent={
            <Text style={{ color: c.textMuted, padding: 16, textAlign: "center" }}>
              {connected ? "No keys yet. Tap + to create one." : "Connect to a device to manage API keys."}
            </Text>
          }
        />
      )}
    </KeyboardAvoidingView>
  );
}

const s = StyleSheet.create({
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    padding: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  title: { fontSize: 17, fontWeight: "600" },
  err: { margin: 12, padding: 8, borderRadius: 6, borderWidth: 1 },
  fresh: { margin: 12, padding: 10, borderRadius: 6, borderWidth: 1, backgroundColor: "#78350f22" },
  form: { padding: 12, borderRadius: 6, borderWidth: 1, margin: 12 },
  input: {
    borderWidth: 1,
    borderRadius: 4,
    paddingHorizontal: 8,
    paddingVertical: 6,
    marginTop: 4,
  },
  saveBtn: {
    marginTop: 12,
    paddingVertical: 10,
    borderRadius: 6,
    alignItems: "center",
  },
  row: {
    padding: 12,
    borderRadius: 6,
    borderWidth: 1,
    marginBottom: 8,
  },
  btn: {
    paddingHorizontal: 10,
    paddingVertical: 6,
    borderRadius: 4,
    borderWidth: 1,
  },
});
