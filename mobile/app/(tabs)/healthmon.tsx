import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  FlatList,
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
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";

// ── Types ────────────────────────────────────────────────────────────

interface HealthTarget {
  id: string;
  url: string;
  label?: string;
  status?: string; // "up" | "warning" | "down"
  statusCode?: number;
  responseMs?: number;
  uptimePercent?: number;
  lastChecked?: string;
  history?: { status: string; responseMs: number; time: string }[];
}

const STATUS_COLORS: Record<string, string> = {
  up: "#22c55e",
  warning: "#f59e0b",
  down: "#ef4444",
  unknown: "#a1a1aa",
};

function formatTime(time: string) {
  try {
    const diff = Date.now() - new Date(time).getTime();
    if (diff < 60_000) return "just now";
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
    return `${Math.floor(diff / 86_400_000)}d ago`;
  } catch {
    return time;
  }
}

// ── Screen ───────────────────────────────────────────────────────────

export default function HealthMonitorScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [targets, setTargets] = useState<HealthTarget[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [addingUrl, setAddingUrl] = useState(false);
  const [newUrl, setNewUrl] = useState("");
  const [newLabel, setNewLabel] = useState("");
  const [expandedTarget, setExpandedTarget] = useState<string | null>(null);
  const [checkingId, setCheckingId] = useState<string | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const loadTargets = useCallback(async () => {
    try {
      const t = await quicClient.getHealthTargets();
      setTargets(t);
    } catch {
      // silent
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!connected) return;
    loadTargets();
    pollRef.current = setInterval(loadTargets, 30000);
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [connected, loadTargets]);

  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    await loadTargets();
    setRefreshing(false);
  }, [loadTargets]);

  const handleAdd = useCallback(async () => {
    if (!newUrl.trim()) return;
    try {
      await quicClient.addHealthTarget(newUrl.trim(), newLabel.trim() || undefined);
      setNewUrl("");
      setNewLabel("");
      setAddingUrl(false);
      loadTargets();
    } catch (e) {
      Alert.alert("Error", e instanceof Error ? e.message : "Failed to add target");
    }
  }, [newUrl, newLabel, loadTargets]);

  const handleRemove = useCallback(
    (id: string, label?: string) => {
      Alert.alert("Remove Target", `Remove ${label || "this target"}?`, [
        { text: "Cancel", style: "cancel" },
        {
          text: "Remove",
          style: "destructive",
          onPress: async () => {
            try {
              await quicClient.removeHealthTarget(id);
              loadTargets();
            } catch {}
          },
        },
      ]);
    },
    [loadTargets]
  );

  const handleCheck = useCallback(
    async (id: string) => {
      setCheckingId(id);
      try {
        await quicClient.checkHealthTarget(id);
        await loadTargets();
      } catch {}
      setCheckingId(null);
    },
    [loadTargets]
  );

  const resolveStatus = (t: HealthTarget) => {
    if (t.status === "warning") return "warning";
    if (t.status === "up" || t.statusCode === 200) return "up";
    if (t.status === "down") return "down";
    if (t.status) return t.status;
    return "unknown";
  };

  const renderTarget = ({ item: t }: { item: HealthTarget }) => {
    const statusKey = resolveStatus(t);
    const statusColor = STATUS_COLORS[statusKey] || STATUS_COLORS.unknown;
    const isExpanded = expandedTarget === t.id;
    const isChecking = checkingId === t.id;

    return (
      <Pressable
        style={[st.card, { backgroundColor: c.bgCard, borderColor: c.border }]}
        onPress={() => setExpandedTarget(isExpanded ? null : t.id)}
        onLongPress={() => handleRemove(t.id, t.label || t.url)}
      >
        {/* Status badges */}
        <View style={st.badgeRow}>
          <View style={[st.badge, { backgroundColor: statusColor + "22" }]}>
            <Text style={[st.badgeText, { color: statusColor }]}>{statusKey}</Text>
          </View>
          {t.statusCode != null && (
            <View style={[st.badge, { backgroundColor: statusColor + "22" }]}>
              <Text style={[st.badgeText, { color: statusColor }]}>{t.statusCode}</Text>
            </View>
          )}
          {t.responseMs != null && (
            <View style={[st.badge, { backgroundColor: "#6366f122" }]}>
              <Text style={[st.badgeText, { color: "#6366f1" }]}>{t.responseMs}ms</Text>
            </View>
          )}
          {isChecking && <ActivityIndicator size="small" color={c.accent} />}
        </View>

        {/* Title */}
        <Text style={[st.title, { color: c.textPrimary }]} numberOfLines={1}>
          {t.label || t.url}
        </Text>
        {t.label && (
          <Text style={[st.url, { color: c.textMuted }]} numberOfLines={1}>
            {t.url}
          </Text>
        )}

        {/* Uptime bar */}
        {t.uptimePercent != null && (
          <View style={st.uptimeRow}>
            <View style={[st.uptimeBarBg, { backgroundColor: c.border }]}>
              <View
                style={[
                  st.uptimeBarFill,
                  {
                    width: `${Math.min(t.uptimePercent, 100)}%`,
                    backgroundColor:
                      t.uptimePercent >= 99
                        ? "#22c55e"
                        : t.uptimePercent >= 95
                        ? "#f59e0b"
                        : "#ef4444",
                  },
                ]}
              />
            </View>
            <Text style={[st.uptimeText, { color: c.textMuted }]}>
              {t.uptimePercent.toFixed(1)}%
            </Text>
          </View>
        )}

        {/* Timestamp */}
        {t.lastChecked && (
          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>
            checked {formatTime(t.lastChecked)}
          </Text>
        )}

        {/* Expanded detail */}
        {isExpanded && (
          <View style={[st.expandedSection, { borderTopColor: c.border }]}>
            <View style={{ flexDirection: "row", gap: 8, marginBottom: 8 }}>
              <Pressable
                style={[st.btn, { backgroundColor: c.accent, flex: 1 }]}
                onPress={() => handleCheck(t.id)}
                disabled={isChecking}
              >
                {isChecking ? (
                  <ActivityIndicator size="small" color="#fff" />
                ) : (
                  <Text style={[st.btnText, { color: "#fff" }]}>Check Now</Text>
                )}
              </Pressable>
              <Pressable
                style={[st.btn, { backgroundColor: "#ef444422", flex: 1 }]}
                onPress={() => handleRemove(t.id, t.label || t.url)}
              >
                <Text style={[st.btnText, { color: "#ef4444" }]}>Remove</Text>
              </Pressable>
            </View>

            {t.history && t.history.length > 0 && (
              <View style={{ gap: 2 }}>
                <Text style={{ color: c.textMuted, fontSize: 11, fontWeight: "600", marginBottom: 4 }}>
                  Recent Checks
                </Text>
                {t.history.slice(0, 10).map((h, i) => {
                  const hColor =
                    h.status === "warning"
                      ? "#f59e0b"
                      : h.status === "up"
                      ? "#22c55e"
                      : "#ef4444";
                  return (
                    <View key={i} style={st.historyRow}>
                      <View style={[st.historyDot, { backgroundColor: hColor }]} />
                      <Text style={{ color: c.textPrimary, fontSize: 12, flex: 1 }}>
                        {h.responseMs}ms
                      </Text>
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>
                        {formatTime(h.time)}
                      </Text>
                    </View>
                  );
                })}
              </View>
            )}
          </View>
        )}
      </Pressable>
    );
  };

  const ListHeader = () => (
    <View style={{ gap: 10, marginBottom: 8 }}>
      {addingUrl ? (
        <View style={[st.addForm, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <TextInput
            style={[st.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="https://example.com/health"
            placeholderTextColor={c.textMuted}
            value={newUrl}
            onChangeText={setNewUrl}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="url"
            autoFocus
          />
          <TextInput
            style={[st.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
            placeholder="Label (optional)"
            placeholderTextColor={c.textMuted}
            value={newLabel}
            onChangeText={setNewLabel}
          />
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Pressable style={[st.btn, { backgroundColor: c.accent, flex: 1 }]} onPress={handleAdd}>
              <Text style={[st.btnText, { color: "#fff" }]}>Add</Text>
            </Pressable>
            <Pressable
              style={[st.btn, { backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.border, flex: 1 }]}
              onPress={() => {
                setAddingUrl(false);
                setNewUrl("");
                setNewLabel("");
              }}
            >
              <Text style={[st.btnText, { color: c.textPrimary }]}>Cancel</Text>
            </Pressable>
          </View>
        </View>
      ) : (
        <Pressable
          style={[st.addBtn, { backgroundColor: c.bgCard, borderColor: c.border }]}
          onPress={() => setAddingUrl(true)}
        >
          <Text style={{ color: c.accent, fontSize: 18, fontWeight: "300" }}>+</Text>
          <Text style={{ color: c.textMuted, fontSize: 13 }}>Add URL to monitor</Text>
        </Pressable>
      )}
    </View>
  );

  const ListEmpty = () =>
    !addingUrl && !loading ? (
      <View style={{ paddingVertical: 40, alignItems: "center" }}>
        <Text style={{ color: c.textMuted, fontSize: 40, marginBottom: 12 }}>{"\u2661"}</Text>
        <Text style={{ color: c.textMuted, fontSize: 14, textAlign: "center" }}>
          No health targets yet.{"\n"}Add a URL to start monitoring.
        </Text>
      </View>
    ) : null;

  return (
    <View style={[st.container, { backgroundColor: c.bg, paddingTop: insets.top }]}>
      {/* Header */}
      <View style={[st.header, { borderBottomColor: c.border }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)}>
          <Text style={{ color: c.accent, fontSize: 16 }}>Back</Text>
        </Pressable>
        <Text style={[st.headerTitle, { color: c.textPrimary }]}>Health Monitor</Text>
        <View style={{ width: 40 }} />
      </View>

      {!connected ? (
        <View style={{ flex: 1, justifyContent: "center", alignItems: "center", padding: 20 }}>
          <Text style={{ color: c.textMuted, fontSize: 14, textAlign: "center" }}>
            Connect to a device to use Health Monitor.
          </Text>
        </View>
      ) : loading ? (
        <View style={{ flex: 1, justifyContent: "center", alignItems: "center" }}>
          <ActivityIndicator color={c.accent} />
        </View>
      ) : (
        <FlatList
          data={targets}
          keyExtractor={(item) => item.id}
          renderItem={renderTarget}
          ListHeaderComponent={ListHeader}
          ListEmptyComponent={ListEmpty}
          contentContainerStyle={{ padding: 16, gap: 10 }}
          refreshControl={
            <RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={c.accent} />
          }
        />
      )}
    </View>
  );
}

// ── Styles ───────────────────────────────────────────────────────────

const st = StyleSheet.create({
  container: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  headerTitle: { fontSize: 17, fontWeight: "700" },
  card: {
    borderRadius: 12,
    padding: 16,
    borderWidth: 1,
  },
  badgeRow: {
    flexDirection: "row",
    alignItems: "center",
    marginBottom: 8,
    gap: 8,
  },
  badge: {
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 6,
  },
  badgeText: {
    fontSize: 12,
    fontWeight: "600",
    textTransform: "uppercase",
  },
  title: { fontSize: 16, fontWeight: "600" },
  url: { fontSize: 12, marginTop: 2, fontFamily: "monospace" },
  uptimeRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    marginTop: 10,
  },
  uptimeBarBg: {
    flex: 1,
    height: 4,
    borderRadius: 2,
    overflow: "hidden",
  },
  uptimeBarFill: { height: "100%", borderRadius: 2 },
  uptimeText: { fontSize: 11, fontWeight: "500", minWidth: 44, textAlign: "right" },
  expandedSection: {
    marginTop: 12,
    paddingTop: 12,
    borderTopWidth: StyleSheet.hairlineWidth,
  },
  historyRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingVertical: 3,
  },
  historyDot: { width: 6, height: 6, borderRadius: 3 },
  btn: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
    alignItems: "center",
    justifyContent: "center",
    minWidth: 60,
  },
  btnText: { fontSize: 13, fontWeight: "600" },
  addForm: {
    borderRadius: 12,
    borderWidth: 1,
    padding: 14,
    gap: 8,
  },
  addBtn: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    borderRadius: 12,
    borderWidth: 1,
    borderStyle: "dashed",
    padding: 14,
    justifyContent: "center",
  },
  input: {
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 14,
  },
});
