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
import * as Linking from "expo-linking";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import {
  quicClient,
  type VaultCategory,
  type VaultEntrySummary,
} from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";

// Mobile UI for desktop/agent/vault.go. Values are never cached on
// device — each reveal re-fetches over the P2P channel and lives in
// React state only until the user hides or navigates away.

const CATEGORIES: VaultCategory[] = [
  "api-key",
  "signing-key",
  "ssh-key",
  "git-credential",
  "custom",
];

const OPENAI_API_KEYS_URL = "https://platform.openai.com/api-keys";

export default function VaultScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [entries, setEntries] = useState<VaultEntrySummary[]>([]);
  const [revealed, setRevealed] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [filter, setFilter] = useState<Set<VaultCategory>>(new Set(CATEGORIES));
  const [masks, setMasks] = useState<Record<string, string>>({});

  // Add form state
  const [draftName, setDraftName] = useState("");
  const [draftValue, setDraftValue] = useState("");
  const [draftNotes, setDraftNotes] = useState("");
  const [draftCategory, setDraftCategory] = useState<VaultCategory>("api-key");
  const [showForm, setShowForm] = useState(false);

  const load = useCallback(async () => {
    if (!connected) return;
    setErr(null);
    try {
      const rows = await quicClient.vaultList();
      setEntries(rows.sort((a, b) => a.name.localeCompare(b.name)));
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

  async function toggleReveal(name: string) {
    if (revealed[name]) {
      setRevealed((prev) => {
        const next = { ...prev };
        delete next[name];
        return next;
      });
      return;
    }
    try {
      const entry = await quicClient.vaultGet(name);
      setRevealed((prev) => ({ ...prev, [name]: entry.value }));
    } catch (e: any) {
      Alert.alert("Vault", e?.message ?? "failed to reveal");
    }
  }

  async function copy(value: string) {
    await Clipboard.setStringAsync(value);
    Alert.alert("Vault", "Copied to clipboard");
  }

  // Quick-copy: fetch + copy + throw away the plaintext (never rendered).
  async function quickCopy(name: string) {
    try {
      const entry = await quicClient.vaultGet(name);
      await Clipboard.setStringAsync(entry.value);
      Alert.alert("Vault", `${name} copied`);
    } catch (e: any) {
      Alert.alert("Vault", e?.message ?? "failed");
    }
  }

  async function toggleMask(name: string) {
    if (masks[name]) {
      setMasks((prev) => {
        const next = { ...prev };
        delete next[name];
        return next;
      });
      return;
    }
    try {
      const entry = await quicClient.vaultGet(name);
      const v = entry.value;
      const hint = v.length <= 10 ? `${v.slice(0, 1)}…${v.slice(-1)}` : `${v.slice(0, 4)}…${v.slice(-4)}`;
      setMasks((prev) => ({ ...prev, [name]: hint }));
    } catch (e: any) {
      Alert.alert("Vault", e?.message ?? "failed");
    }
  }

  async function remove(name: string) {
    Alert.alert("Delete?", `Remove vault entry "${name}"? This cannot be undone.`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Delete",
        style: "destructive",
        onPress: async () => {
          try {
            await quicClient.vaultDelete(name);
            await load();
          } catch (e: any) {
            Alert.alert("Vault", e?.message ?? "failed to delete");
          }
        },
      },
    ]);
  }

  async function save() {
    if (!draftName.trim() || !draftValue.trim()) {
      Alert.alert("Vault", "Name and value are required");
      return;
    }
    if (draftName.trim().toUpperCase() === "OPENAI_API_KEY") {
      const candidate = draftValue.trim();
      if (!(candidate.startsWith("sk-") || candidate.startsWith("sess-"))) {
        Alert.alert("Vault", "That does not look like an OpenAI API key. OpenAI keys usually start with sk-.");
        return;
      }
    }
    try {
      await quicClient.vaultSet({
        name: draftName.trim(),
        value: draftValue,
        category: draftCategory,
        notes: draftNotes.trim() || undefined,
      });
      setDraftName("");
      setDraftValue("");
      setDraftNotes("");
      setShowForm(false);
      await load();
    } catch (e: any) {
      Alert.alert("Vault", e?.message ?? "failed to save");
    }
  }

  const renderItem = ({ item }: { item: VaultEntrySummary }) => {
    const value = revealed[item.name];
    return (
      <View
        style={[
          s.row,
          { backgroundColor: c.bgCard, borderColor: c.border },
        ]}
      >
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          <Text
            style={{ color: c.textPrimary, fontFamily: "monospace", flexShrink: 1 }}
            numberOfLines={1}
          >
            {item.name}
          </Text>
          <Text
            style={[s.badge, { color: c.accent, borderColor: c.accent }]}
          >
            {item.category}
          </Text>
        </View>
        {item.notes ? (
          <Text style={{ color: c.textMuted, marginTop: 4 }}>{item.notes}</Text>
        ) : null}
        {!value && masks[item.name] ? (
          <Text
            style={{
              color: c.textMuted,
              fontFamily: "monospace",
              backgroundColor: c.bg,
              paddingHorizontal: 6,
              paddingVertical: 2,
              alignSelf: "flex-start",
              marginTop: 4,
              borderRadius: 2,
              fontSize: 11,
            }}
          >
            {masks[item.name]}
          </Text>
        ) : null}
        {value ? (
          <View style={[s.valueBox, { backgroundColor: c.bg }]}>
            <Text style={{ color: c.textPrimary, fontFamily: "monospace" }} selectable>
              {value}
            </Text>
          </View>
        ) : null}
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 8 }}>
          <Pressable
            style={[s.btn, { backgroundColor: c.bg, borderColor: c.border }]}
            onPress={() => quickCopy(item.name)}
          >
            <Text style={{ color: c.textPrimary }}>Copy</Text>
          </Pressable>
          <Pressable
            style={[s.btn, { backgroundColor: c.bg, borderColor: c.border }]}
            onPress={() => toggleMask(item.name)}
          >
            <Text style={{ color: c.textPrimary }}>{masks[item.name] ? "Hide mask" : "Preview"}</Text>
          </Pressable>
          <Pressable
            style={[s.btn, { backgroundColor: c.bg, borderColor: c.border }]}
            onPress={() => toggleReveal(item.name)}
          >
            <Text style={{ color: c.textPrimary }}>{value ? "Hide" : "Reveal"}</Text>
          </Pressable>
          {value ? (
            <Pressable
              style={[s.btn, { backgroundColor: c.bg, borderColor: c.border }]}
              onPress={() => copy(value)}
            >
              <Text style={{ color: c.textPrimary }}>Copy shown</Text>
            </Pressable>
          ) : null}
          <Pressable
            style={[s.btn, { backgroundColor: "#3f0a0a", borderColor: "#991b1b" }]}
            onPress={() => remove(item.name)}
          >
            <Text style={{ color: "#fecaca" }}>Delete</Text>
          </Pressable>
        </View>
      </View>
    );
  };

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}
    >
      <View style={[s.header, { borderColor: c.border }]}>
        <AppBackButton onPress={() => router.back()} />
        <Text style={[s.title, { color: c.textPrimary }]}>Vault</Text>
        <Pressable
          onPress={() => setShowForm((v) => !v)}
          hitSlop={12}
          style={{
            width: 32,
            height: 32,
            borderRadius: 16,
            alignItems: "center",
            justifyContent: "center",
            backgroundColor: showForm ? "transparent" : `${c.accent}22`,
            borderWidth: 1,
            borderColor: c.accent,
          }}
        >
          <Text style={{ color: c.accent, fontSize: 18, fontWeight: "600", lineHeight: 20 }}>
            {showForm ? "\u00D7" : "+"}
          </Text>
        </Pressable>
      </View>

      {err ? (
        <View style={[s.err, { borderColor: "#991b1b" }]}>
          <Text style={{ color: "#fecaca" }}>{err}</Text>
        </View>
      ) : null}

      <View style={[s.quickCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
        <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "600" }}>OpenAI quick add</Text>
        <Text style={{ color: c.textMuted, marginTop: 6 }}>
          Paste your OpenAI API key into the host vault. It stays on your machine and can be reused for speech and upcoming prompt-to-scaffold flows.
        </Text>
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 10 }}>
          <Pressable
            style={[s.btn, { backgroundColor: c.accent, borderColor: c.accent }]}
            onPress={() => {
              setDraftName("OPENAI_API_KEY");
              setDraftCategory("api-key");
              setDraftNotes("OpenAI API key for Yaver mobile-first scaffolding and speech");
              setShowForm(true);
            }}
          >
            <Text style={{ color: "#fff" }}>Use preset</Text>
          </Pressable>
          <Pressable
            style={[s.btn, { backgroundColor: c.bg, borderColor: c.border }]}
            onPress={() => Linking.openURL(OPENAI_API_KEYS_URL)}
          >
            <Text style={{ color: c.textPrimary }}>Open key page</Text>
          </Pressable>
        </View>
      </View>

      {showForm ? (
        <ScrollView
          style={{ maxHeight: 320 }}
          contentContainerStyle={[s.form, { backgroundColor: c.bgCard, borderColor: c.border }]}
        >
          <Text style={{ color: c.textMuted, fontSize: 11 }}>Name</Text>
          <TextInput
            value={draftName}
            onChangeText={setDraftName}
            placeholder="OPENAI_API_KEY"
            placeholderTextColor={c.textMuted}
            autoCapitalize="characters"
            autoCorrect={false}
            style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Category</Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, marginTop: 4 }}>
            {CATEGORIES.map((cat) => (
              <Pressable
                key={cat}
                onPress={() => setDraftCategory(cat)}
                style={{
                  borderWidth: 1,
                  borderColor: draftCategory === cat ? c.accent : c.border,
                  backgroundColor: draftCategory === cat ? `${c.accent}22` : "transparent",
                  paddingHorizontal: 8,
                  paddingVertical: 4,
                  borderRadius: 4,
                }}
              >
                <Text style={{ color: draftCategory === cat ? c.accent : c.textMuted, fontSize: 11 }}>{cat}</Text>
              </Pressable>
            ))}
          </View>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Value</Text>
          <TextInput
            value={draftValue}
            onChangeText={setDraftValue}
            placeholder="secret value"
            placeholderTextColor={c.textMuted}
            secureTextEntry
            autoCapitalize="none"
            autoCorrect={false}
            style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Notes (optional)</Text>
          <TextInput
            value={draftNotes}
            onChangeText={setDraftNotes}
            placeholder="what's this for?"
            placeholderTextColor={c.textMuted}
            style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <Pressable
            style={[s.saveBtn, { backgroundColor: c.accent }]}
            onPress={save}
          >
            <Text style={{ color: "#fff", fontWeight: "600" }}>Save</Text>
          </Pressable>
        </ScrollView>
      ) : null}

      {/* Category filter chips — fixed height row so the chips don't
       *  stretch vertically in the flex column. */}
      <View style={{ height: 36, marginTop: 4 }}>
        <ScrollView
          horizontal
          showsHorizontalScrollIndicator={false}
          contentContainerStyle={{
            paddingHorizontal: 12,
            alignItems: "center",
            gap: 6,
          }}
        >
          {CATEGORIES.map((cat) => {
            const active = filter.has(cat);
            return (
              <Pressable
                key={cat}
                onPress={() => {
                  setFilter((prev) => {
                    const next = new Set(prev);
                    if (next.has(cat)) next.delete(cat);
                    else next.add(cat);
                    if (next.size === 0) return new Set(CATEGORIES);
                    return next;
                  });
                }}
                style={{
                  borderWidth: 1,
                  borderColor: active ? c.accent : c.border,
                  backgroundColor: active ? `${c.accent}22` : "transparent",
                  paddingHorizontal: 10,
                  paddingVertical: 4,
                  borderRadius: 999,
                }}
              >
                <Text style={{ color: active ? c.accent : c.textMuted, fontSize: 11 }}>
                  {cat}
                </Text>
              </Pressable>
            );
          })}
        </ScrollView>
      </View>

      {loading ? (
        <ActivityIndicator style={{ marginTop: 24 }} color={c.accent} />
      ) : (
        <FlatList
          data={entries.filter((e) => filter.has(e.category))}
          keyExtractor={(i) => i.name}
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
            connected ? (
              <View style={{ alignItems: "center", paddingHorizontal: 24, paddingTop: 32 }}>
                <Text
                  style={{
                    color: c.textMuted,
                    textAlign: "center",
                    marginBottom: 14,
                    fontSize: 14,
                  }}
                >
                  No entries yet.
                </Text>
                <Pressable
                  onPress={() => setShowForm(true)}
                  style={{
                    backgroundColor: c.accent,
                    paddingHorizontal: 20,
                    paddingVertical: 12,
                    borderRadius: 8,
                    flexDirection: "row",
                    alignItems: "center",
                    gap: 8,
                  }}
                >
                  <Text style={{ color: "#fff", fontSize: 18, fontWeight: "600" }}>+</Text>
                  <Text style={{ color: "#fff", fontWeight: "600" }}>New entry</Text>
                </Pressable>
              </View>
            ) : (
              <Text style={{ color: c.textMuted, padding: 16, textAlign: "center" }}>
                Connect to a device to view vault.
              </Text>
            )
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
  err: { margin: 12, padding: 8, borderRadius: 6, borderWidth: 1, backgroundColor: "#3f0a0a22" },
  quickCard: { marginHorizontal: 12, marginBottom: 8, padding: 12, borderRadius: 6, borderWidth: 1 },
  form: { padding: 12, borderRadius: 6, borderWidth: 1, margin: 12 },
  input: {
    borderWidth: 1,
    borderRadius: 4,
    paddingHorizontal: 8,
    paddingVertical: 6,
    marginTop: 4,
    fontFamily: "monospace",
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
  badge: {
    fontSize: 10,
    borderWidth: 1,
    paddingHorizontal: 4,
    paddingVertical: 1,
    borderRadius: 2,
  },
  valueBox: {
    marginTop: 6,
    padding: 8,
    borderRadius: 4,
  },
  btn: {
    paddingHorizontal: 10,
    paddingVertical: 6,
    borderRadius: 4,
    borderWidth: 1,
  },
});
