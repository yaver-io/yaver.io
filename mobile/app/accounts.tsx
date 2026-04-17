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
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";

// Mobile screen for /accounts — cloud-provider credential vault on
// the host. Credentials are stored encrypted under ~/.yaver; the agent
// uses them to run cloud commands on your behalf. Convex never holds
// credential values — the mobile UI hits the agent directly over P2P.

type Provider = {
  id: string;
  label: string;
  fields?: { name: string; label?: string; secret?: boolean; placeholder?: string }[];
};

type Account = {
  provider: string;
  label?: string;
  connectedAt?: string;
  status?: string;
};

export default function AccountsScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [accounts, setAccounts] = useState<Account[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const [active, setActive] = useState<Provider | null>(null);
  const [label, setLabel] = useState("");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [connecting, setConnecting] = useState(false);

  const load = useCallback(async () => {
    if (!connected) return;
    setErr(null);
    try {
      const data = await quicClient.accountsList();
      setAccounts(Array.isArray(data.accounts) ? data.accounts : []);
      setProviders(Array.isArray(data.providers) ? data.providers : []);
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

  async function connect() {
    if (!active) return;
    if (!label.trim()) {
      Alert.alert("Accounts", "Label is required");
      return;
    }
    setConnecting(true);
    try {
      await quicClient.accountConnect(active.id, label.trim(), fields);
      setActive(null);
      setLabel("");
      setFields({});
      await load();
    } catch (e: any) {
      Alert.alert("Accounts", e?.message ?? "failed to connect");
    } finally {
      setConnecting(false);
    }
  }

  async function disconnect(a: Account) {
    Alert.alert("Disconnect?", `Disconnect ${a.provider} (${a.label ?? "unlabeled"})?`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Disconnect",
        style: "destructive",
        onPress: async () => {
          try {
            await quicClient.accountDisconnect(a.provider);
            await load();
          } catch (e: any) {
            Alert.alert("Accounts", e?.message ?? "failed to disconnect");
          }
        },
      },
    ]);
  }

  const renderItem = ({ item }: { item: Account }) => (
    <View style={[s.row, { backgroundColor: c.bgCard, borderColor: c.border }]}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{item.provider}</Text>
        {item.label ? <Text style={{ color: c.textMuted, fontSize: 11 }}>{item.label}</Text> : null}
        {item.status ? (
          <Text
            style={{
              color: c.accent,
              fontSize: 10,
              borderWidth: 1,
              borderColor: c.border,
              paddingHorizontal: 4,
              paddingVertical: 1,
              borderRadius: 2,
            }}
          >
            {item.status}
          </Text>
        ) : null}
      </View>
      {item.connectedAt ? (
        <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
          connected {item.connectedAt}
        </Text>
      ) : null}
      <Pressable
        style={[s.btn, { marginTop: 8, backgroundColor: "#3f0a0a", borderColor: "#991b1b" }]}
        onPress={() => disconnect(item)}
      >
        <Text style={{ color: "#fecaca" }}>Disconnect</Text>
      </Pressable>
    </View>
  );

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}
    >
      <View style={[s.header, { borderColor: c.border }]}>
        <AppBackButton onPress={() => router.back()} />
        <Text style={[s.title, { color: c.textPrimary }]}>Accounts</Text>
        <View style={{ width: 20 }} />
      </View>

      {err ? (
        <View style={[s.err, { borderColor: "#991b1b" }]}>
          <Text style={{ color: "#fecaca" }}>{err}</Text>
        </View>
      ) : null}

      {/* Provider picker */}
      <View style={{ padding: 12 }}>
        <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 6 }}>
          Available providers (credentials stay on this machine)
        </Text>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 6 }}>
          {providers.map((p) => (
            <Pressable
              key={p.id}
              onPress={() => {
                setActive(p);
                setFields({});
                setLabel("");
              }}
              style={{
                borderWidth: 1,
                borderColor: active?.id === p.id ? c.accent : c.border,
                backgroundColor: active?.id === p.id ? `${c.accent}22` : "transparent",
                paddingHorizontal: 10,
                paddingVertical: 6,
                borderRadius: 4,
              }}
            >
              <Text style={{ color: active?.id === p.id ? c.accent : c.textPrimary, fontSize: 12 }}>
                {p.label ?? p.id}
              </Text>
            </Pressable>
          ))}
        </ScrollView>
      </View>

      {/* Connect form */}
      {active ? (
        <ScrollView
          style={{ maxHeight: 380 }}
          contentContainerStyle={[s.form, { backgroundColor: c.bgCard, borderColor: c.border }]}
        >
          <Text style={{ color: c.textPrimary, fontWeight: "600" }}>Connect {active.label ?? active.id}</Text>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>
            Fields are POSTed over P2P to your agent and stored encrypted locally. They never reach Convex.
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Label (no secrets)</Text>
          <TextInput
            value={label}
            onChangeText={(v) => setLabel(v.slice(0, 80))}
            placeholder="e.g. prod, personal"
            placeholderTextColor={c.textMuted}
            maxLength={80}
            style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          {(active.fields ?? []).map((f) => (
            <View key={f.name}>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>
                {f.label ?? f.name}
                {f.secret ? " (secret)" : ""}
              </Text>
              <TextInput
                value={fields[f.name] ?? ""}
                onChangeText={(v) => setFields((prev) => ({ ...prev, [f.name]: v }))}
                placeholder={f.placeholder ?? ""}
                placeholderTextColor={c.textMuted}
                secureTextEntry={f.secret}
                autoCapitalize="none"
                autoCorrect={false}
                style={[s.input, { color: c.textPrimary, borderColor: c.border, fontFamily: f.secret ? "monospace" : "System" }]}
              />
            </View>
          ))}
          <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
            <Pressable
              style={[s.btn, { backgroundColor: c.bgCard, borderColor: c.border }]}
              onPress={() => {
                setActive(null);
                setFields({});
                setLabel("");
              }}
            >
              <Text style={{ color: c.textPrimary }}>Cancel</Text>
            </Pressable>
            <Pressable style={[s.saveBtn, { backgroundColor: c.accent, flex: 1 }]} onPress={connect}>
              <Text style={{ color: "#fff", fontWeight: "600" }}>
                {connecting ? "Connecting…" : "Connect"}
              </Text>
            </Pressable>
          </View>
        </ScrollView>
      ) : null}

      {loading ? (
        <ActivityIndicator style={{ marginTop: 24 }} color={c.accent} />
      ) : (
        <FlatList
          data={accounts}
          keyExtractor={(i, idx) => `${i.provider}-${i.label ?? idx}`}
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
              {connected ? "No connected accounts yet. Pick a provider above." : "Connect to a device to manage accounts."}
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
  form: { padding: 12, borderRadius: 6, borderWidth: 1, margin: 12 },
  input: {
    borderWidth: 1,
    borderRadius: 4,
    paddingHorizontal: 8,
    paddingVertical: 6,
    marginTop: 4,
  },
  saveBtn: {
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
