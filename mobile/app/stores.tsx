// Publish — the store-onboarding concierge on mobile. Renders the catalogue
// the agent serves at /stores (single source of truth in Go's setup_guide.go)
// and, for every step only a human can do (identity, payment, store review),
// routes you straight to the official Apple/Google page. Status is best-effort
// from your device's vault. Transport: YAVER mesh by device + your bearer.
import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Linking, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";

type StoreTask = {
  id: string;
  platform: "apple" | "google" | "both";
  title: string;
  summary: string;
  automation: "auto" | "assisted" | "manual";
  routeUrl?: string;
  steps?: string[];
  needsSecret?: string[];
  dependsOn?: string[];
  yaverCmd?: string;
  status: "done" | "todo" | "action" | "blocked" | "unknown";
};

const STATUS_GLYPH: Record<StoreTask["status"], string> = {
  done: "✓", todo: "○", action: "◆", blocked: "⋯", unknown: "·",
};
const AUTOMATION_LABEL: Record<StoreTask["automation"], string> = {
  auto: "Yaver does it", assisted: "guided", manual: "you (legal/payment)",
};
const GROUPS: { key: StoreTask["platform"]; label: string }[] = [
  { key: "apple", label: "Apple" },
  { key: "google", label: "Google" },
  { key: "both", label: "Cross-platform" },
];

export default function StoresScreen() {
  const router = useRouter();
  const c = useColors();
  const { activeDevice, primaryDeviceId } = useDevice();
  const deviceId = activeDevice?.id || primaryDeviceId || "";
  const [tasks, setTasks] = useState<StoreTask[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  const load = useCallback(async () => {
    setErr(null);
    if (!deviceId) {
      setErr("No device connected. Connect a device to see your status.");
      setTasks([]);
      return;
    }
    try {
      const res = await quicClient.agentRequest(deviceId, "/stores", undefined, 15000);
      const data = await res.json().catch(() => ({}));
      setTasks(Array.isArray(data?.tasks) ? (data.tasks as StoreTask[]) : []);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setTasks([]);
    }
  }, [deviceId]);

  useEffect(() => {
    void load();
  }, [load]);

  const statusColor = (s: StoreTask["status"]) =>
    s === "done" ? c.success : s === "action" ? c.warn : s === "todo" ? c.accent : c.textMuted;

  return (
    <View style={[styles.root, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="Publish to the stores" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={styles.scroll}>
        <Text style={[styles.intro, { color: c.textMuted }]}>
          Everything to get your app onto the App Store &amp; Google Play. Yaver does what it can; for
          the parts only you can do, it opens the exact official page.
        </Text>

        {err ? (
          <View style={[styles.banner, { backgroundColor: c.warnBg, borderColor: c.warnBorder }]}>
            <Text style={{ color: c.warn, fontSize: 12 }}>{err}</Text>
          </View>
        ) : null}

        {tasks === null ? (
          <ActivityIndicator color={c.accent} style={{ marginTop: 24 }} />
        ) : (
          GROUPS.map((g) => {
            const rows = (tasks || []).filter((t) => t.platform === g.key);
            if (rows.length === 0) return null;
            return (
              <View key={g.key} style={styles.group}>
                <Text style={[styles.groupLabel, { color: c.textMuted }]}>{g.label.toUpperCase()}</Text>
                {rows.map((t) => {
                  const open = !!expanded[t.id];
                  return (
                    <View key={t.id} style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                      <Pressable
                        onPress={() => setExpanded((m) => ({ ...m, [t.id]: !open }))}
                        style={styles.cardHead}
                      >
                        <Text style={[styles.glyph, { color: statusColor(t.status) }]}>
                          {STATUS_GLYPH[t.status] ?? "·"}
                        </Text>
                        <View style={{ flex: 1 }}>
                          <Text style={[styles.title, { color: c.textPrimary }]}>{t.title}</Text>
                          <Text style={[styles.summary, { color: c.textMuted }]} numberOfLines={open ? undefined : 2}>
                            {t.summary}
                          </Text>
                        </View>
                        <Text style={[styles.autoTag, { color: c.textSecondary, borderColor: c.border }]}>
                          {AUTOMATION_LABEL[t.automation]}
                        </Text>
                      </Pressable>

                      {open ? (
                        <View style={[styles.detail, { borderTopColor: c.border }]}>
                          {t.dependsOn && t.dependsOn.length > 0 ? (
                            <Text style={[styles.meta, { color: c.textMuted }]}>
                              Needs first: {t.dependsOn.join(", ")}
                            </Text>
                          ) : null}
                          {(t.steps || []).map((s, i) => (
                            <Text key={i} style={[styles.step, { color: c.textSecondary }]}>
                              {i + 1}. {s}
                            </Text>
                          ))}
                          {t.yaverCmd ? (
                            <Text style={[styles.cmd, { color: c.textPrimary, backgroundColor: c.bgInput }]}>
                              {t.yaverCmd}
                            </Text>
                          ) : null}
                          {t.needsSecret && t.needsSecret.length > 0 ? (
                            <Text style={[styles.meta, { color: c.textMuted }]}>
                              Done when in your vault: {t.needsSecret.join(", ")}
                            </Text>
                          ) : null}
                          {t.routeUrl ? (
                            <Pressable
                              onPress={() => Linking.openURL(t.routeUrl!)}
                              style={[styles.openBtn, { backgroundColor: c.accentSoft, borderColor: c.accent }]}
                            >
                              <Text style={{ color: c.accent, fontWeight: "600", fontSize: 13 }}>
                                Open the {t.platform === "apple" ? "Apple" : t.platform === "google" ? "Google" : "official"} page ↗
                              </Text>
                            </Pressable>
                          ) : null}
                        </View>
                      ) : null}
                    </View>
                  );
                })}
              </View>
            );
          })
        )}
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  scroll: { padding: 16, paddingBottom: 48 },
  intro: { fontSize: 12, lineHeight: 17, marginBottom: 14 },
  banner: { borderWidth: 1, borderRadius: 8, padding: 10, marginBottom: 14 },
  group: { marginBottom: 18 },
  groupLabel: { fontSize: 11, fontWeight: "700", letterSpacing: 1, marginBottom: 8 },
  card: { borderWidth: 1, borderRadius: 12, marginBottom: 8, overflow: "hidden" },
  cardHead: { flexDirection: "row", alignItems: "flex-start", gap: 10, padding: 12 },
  glyph: { fontSize: 16, width: 18, textAlign: "center", marginTop: 1 },
  title: { fontSize: 14, fontWeight: "600" },
  summary: { fontSize: 12, lineHeight: 16, marginTop: 2 },
  autoTag: { fontSize: 9, borderWidth: 1, borderRadius: 6, paddingHorizontal: 5, paddingVertical: 2, overflow: "hidden" },
  detail: { borderTopWidth: 1, padding: 12, gap: 6 },
  meta: { fontSize: 11 },
  step: { fontSize: 12, lineHeight: 17 },
  cmd: { fontSize: 11, fontFamily: "Menlo", padding: 8, borderRadius: 6, marginTop: 4 },
  openBtn: { borderWidth: 1, borderRadius: 8, paddingVertical: 9, paddingHorizontal: 12, alignItems: "center", marginTop: 6 },
});
