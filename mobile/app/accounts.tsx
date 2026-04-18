import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
  KeyboardAvoidingView,
  Modal,
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
  authType?: string;
  signupURL?: string;
  tokenURL?: string;
  notes?: string;
  fields?: Array<{ name: string; label?: string; secret?: boolean; placeholder?: string }>;
};

type Account = {
  provider: string;
  label?: string;
  connected?: boolean;
  connectedAt?: string;
  lastUsedAt?: string;
  hasSecret?: boolean;
  hint?: string;
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
      setProviders(Array.isArray(data.providers) ? data.providers.map(normalizeProvider) : []);
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

  const accountsByProvider = Object.fromEntries(accounts.map((account) => [account.provider, account]));
  const providerCards = providers.map((provider) => ({
    provider,
    account: accountsByProvider[provider.id] as Account | undefined,
  }));

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

      {loading ? (
        <ActivityIndicator style={{ marginTop: 24 }} color={c.accent} />
      ) : (
        <FlatList
          data={providerCards}
          keyExtractor={(item) => item.provider.id}
          renderItem={({ item }) => {
            const provider = item.provider;
            const account = item.account;
            const isConnected = !!account?.connected;
            const authLabel = provider.authType?.replace("+", " + ") || "token";
            return (
              <View
                style={[
                  s.row,
                  {
                    backgroundColor: c.bgCard,
                    borderColor: isConnected ? `${c.accent}55` : c.border,
                  },
                ]}
              >
                <View style={s.cardTop}>
                  <View style={{ flex: 1 }}>
                    <View style={s.titleRow}>
                      <View
                        style={[
                          s.statusDot,
                          { backgroundColor: isConnected ? "#22c55e" : c.textMuted },
                        ]}
                      />
                      <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 16 }}>
                        {provider.label ?? provider.id}
                      </Text>
                    </View>
                    <View style={s.metaRow}>
                      <Badge text={authLabel} tone="neutral" />
                      <Badge text={isConnected ? "Connected" : "Not connected"} tone={isConnected ? "success" : "muted"} />
                      {account?.hasSecret ? <Badge text="Secret stored" tone="neutral" /> : null}
                    </View>
                  </View>
                  <Pressable
                    style={[
                      s.inlineBtn,
                      {
                        backgroundColor: isConnected ? "#3f0a0a" : `${c.accent}22`,
                        borderColor: isConnected ? "#991b1b" : `${c.accent}66`,
                      },
                    ]}
                    onPress={() => {
                      if (isConnected && account) {
                        void disconnect(account);
                        return;
                      }
                      setActive(provider);
                      setFields({});
                      setLabel(account?.label ?? "");
                    }}
                  >
                    <Text style={{ color: isConnected ? "#fecaca" : c.accent, fontWeight: "600", fontSize: 12 }}>
                      {isConnected ? "Disconnect" : "Connect"}
                    </Text>
                  </Pressable>
                </View>

                <View style={s.infoBlock}>
                  {account?.label ? (
                    <InfoLine label="Label" value={account.label} muted={c.textMuted} primary={c.textPrimary} />
                  ) : null}
                  {account?.connectedAt ? (
                    <InfoLine label="Connected" value={formatDate(account.connectedAt)} muted={c.textMuted} primary={c.textPrimary} />
                  ) : null}
                  {account?.lastUsedAt ? (
                    <InfoLine label="Last used" value={formatDate(account.lastUsedAt)} muted={c.textMuted} primary={c.textPrimary} />
                  ) : null}
                  {!isConnected && (provider.notes || account?.hint || provider.tokenURL) ? (
                    <Text style={{ color: c.textMuted, fontSize: 11, lineHeight: 16 }}>
                      {provider.notes || account?.hint || provider.tokenURL}
                    </Text>
                  ) : null}
                </View>
              </View>
            );
          }}
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
              {connected ? "No providers available." : "Connect to a device to manage accounts."}
            </Text>
          }
        />
      )}

      <Modal visible={!!active} animationType="slide" transparent onRequestClose={() => setActive(null)}>
        <View style={s.modalOverlay}>
          <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined}>
            <View style={[s.modalCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <View style={s.modalHeader}>
                <View style={{ flex: 1 }}>
                  <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 16 }}>
                    Connect {active?.label ?? active?.id}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4, lineHeight: 16 }}>
                    Credentials are sent over P2P to your agent and stored encrypted on this machine.
                  </Text>
                </View>
                <Pressable
                  onPress={() => {
                    setActive(null);
                    setFields({});
                    setLabel("");
                  }}
                  style={s.closeBtn}
                >
                  <Text style={{ color: c.textMuted, fontSize: 18 }}>×</Text>
                </Pressable>
              </View>

              <ScrollView style={{ maxHeight: 420 }} contentContainerStyle={{ paddingTop: 4 }}>
                <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>Label</Text>
                <TextInput
                  value={label}
                  onChangeText={(v) => setLabel(v.slice(0, 80))}
                  placeholder="e.g. prod, personal"
                  placeholderTextColor={c.textMuted}
                  maxLength={80}
                  style={[s.input, { color: c.textPrimary, borderColor: c.border }]}
                />
                {(active?.fields ?? []).map((f) => (
                  <View key={f.name}>
                    <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 10 }}>
                      {f.label ?? prettifyFieldName(f.name)}
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
                      style={[
                        s.input,
                        {
                          color: c.textPrimary,
                          borderColor: c.border,
                          fontFamily: f.secret ? "monospace" : "System",
                        },
                      ]}
                    />
                  </View>
                ))}
                {active?.notes ? (
                  <Text style={{ color: c.textMuted, fontSize: 11, lineHeight: 16, marginTop: 12 }}>
                    {active.notes}
                  </Text>
                ) : null}
                {active?.tokenURL ? (
                  <Text style={{ color: c.textMuted, fontSize: 11, lineHeight: 16, marginTop: 8 }}>
                    {active.tokenURL}
                  </Text>
                ) : null}
              </ScrollView>

              <View style={s.modalActions}>
                <Pressable
                  style={[s.modalActionBtn, { backgroundColor: c.bg, borderColor: c.border }]}
                  onPress={() => {
                    setActive(null);
                    setFields({});
                    setLabel("");
                  }}
                >
                  <Text style={{ color: c.textPrimary }}>Cancel</Text>
                </Pressable>
                <Pressable
                  style={[s.modalActionBtn, s.primaryAction, { backgroundColor: c.accent, borderColor: c.accent }]}
                  onPress={connect}
                >
                  <Text style={{ color: "#fff", fontWeight: "700" }}>
                    {connecting ? "Connecting…" : "Connect"}
                  </Text>
                </Pressable>
              </View>
            </View>
          </KeyboardAvoidingView>
        </View>
      </Modal>
    </KeyboardAvoidingView>
  );
}

function normalizeProvider(raw: any): Provider {
  const rawFields = Array.isArray(raw?.fields) ? raw.fields : [];
  return {
    id: raw?.id,
    label: raw?.label,
    authType: raw?.authType,
    signupURL: raw?.signupURL,
    tokenURL: raw?.tokenURL,
    notes: raw?.notes,
    fields: rawFields.map((field: any) => {
      if (typeof field === "string") {
        return {
          name: field,
          label: prettifyFieldName(field),
          secret: /token|secret|key/i.test(field),
          placeholder: field === "region" ? "e.g. eu-central-1" : "",
        };
      }
      return {
        name: field?.name,
        label: field?.label ?? prettifyFieldName(field?.name || ""),
        secret: !!field?.secret,
        placeholder: field?.placeholder ?? "",
      };
    }),
  };
}

function prettifyFieldName(value: string): string {
  return value
    .replace(/([A-Z])/g, " $1")
    .replace(/[_-]/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/^./, (match) => match.toUpperCase());
}

function InfoLine({
  label,
  value,
  muted,
  primary,
}: {
  label: string;
  value: string;
  muted: string;
  primary: string;
}) {
  return (
    <View style={s.infoRow}>
      <Text style={{ color: muted, fontSize: 11, width: 74 }}>{label}</Text>
      <Text style={{ color: primary, fontSize: 11, flex: 1 }} numberOfLines={2}>
        {value}
      </Text>
    </View>
  );
}

function Badge({ text, tone }: { text: string; tone: "success" | "muted" | "neutral" }) {
  const palette =
    tone === "success"
      ? { bg: "#14532d", border: "#22c55e66", text: "#86efac" }
      : tone === "muted"
        ? { bg: "#111111", border: "#2a2a2a", text: "#a1a1aa" }
        : { bg: "#111111", border: "#303030", text: "#d4d4d8" };
  return (
    <View style={[s.badge, { backgroundColor: palette.bg, borderColor: palette.border }]}>
      <Text style={{ color: palette.text, fontSize: 10, fontWeight: "600" }}>{text}</Text>
    </View>
  );
}

function formatDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
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
  input: {
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 10,
    paddingVertical: 9,
    marginTop: 4,
  },
  row: {
    padding: 12,
    borderRadius: 10,
    borderWidth: 1,
    marginBottom: 10,
  },
  cardTop: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: 10,
  },
  titleRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  statusDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    marginTop: 1,
  },
  metaRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 6,
    marginTop: 8,
  },
  badge: {
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 8,
    paddingVertical: 3,
  },
  infoBlock: {
    marginTop: 10,
    gap: 6,
  },
  infoRow: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: 8,
  },
  inlineBtn: {
    paddingHorizontal: 10,
    paddingVertical: 8,
    borderRadius: 8,
    borderWidth: 1,
  },
  modalOverlay: {
    flex: 1,
    backgroundColor: "rgba(0,0,0,0.55)",
    justifyContent: "center",
    padding: 16,
  },
  modalCard: {
    borderWidth: 1,
    borderRadius: 16,
    padding: 16,
  },
  modalHeader: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: 12,
    marginBottom: 4,
  },
  closeBtn: {
    width: 28,
    height: 28,
    alignItems: "center",
    justifyContent: "center",
  },
  modalActions: {
    flexDirection: "row",
    gap: 10,
    marginTop: 14,
  },
  modalActionBtn: {
    flex: 1,
    paddingVertical: 11,
    borderRadius: 10,
    borderWidth: 1,
    alignItems: "center",
  },
  primaryAction: {},
});
