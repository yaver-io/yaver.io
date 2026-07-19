import React, { useCallback, useEffect, useMemo, useState } from "react";
import { ActivityIndicator, Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from "react-native";
import { Stack, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AppBackButton } from "../src/components/AppBackButton";
import { ScreenScaffold } from "../src/components/layout/ScreenScaffold";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { DEFAULT_SLOT_COUNT, useAgentSlots } from "../src/lib/agentSlots";
import { agentSignalFromAutorun, agentStateBg, agentStateColor, slotKeyForAutorun, type AutorunSession } from "../src/lib/agentStatus";
import { quicClient, type AutorunSessionInfo } from "../src/lib/quic";

function taskLabel(taskPath: string): string {
  const trimmed = String(taskPath || "").trim();
  if (!trimmed) return "autorun";
  const parts = trimmed.split("/");
  return parts[parts.length - 1] || trimmed;
}

function timeAgo(ts?: number): string {
  if (!ts || !Number.isFinite(ts)) return "just now";
  const secs = Math.max(0, Math.round((Date.now() - ts) / 1000));
  if (secs < 60) return `${secs}s ago`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  return `${Math.floor(secs / 3600)}h ago`;
}

function finishedLabel(session: AutorunSessionInfo): string {
  if (session.finishReason) return session.finishReason;
  switch (session.status) {
    case "completed":
      return "completed";
    case "failed":
      return "failed";
    case "stopped":
      return "stopped";
    case "stopping":
      return "stopping";
    default:
      return "running";
  }
}

export default function AutorunsScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { activeDevice, connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [sessions, setSessions] = useState<AutorunSessionInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lastSeenMs, setLastSeenMs] = useState<number | undefined>(undefined);
  // Claims held on the box right now — what a slot_busy / area_owned refusal
  // refers to. Kept deliberately terse on a phone: the contended thing and who
  // has it, nothing else.
  const [holds, setHolds] = useState<
    { key?: { Class?: string; Name?: string }; slot?: string; holder?: string; phase?: string }[]
  >([]);

  const load = useCallback(async () => {
    if (!connected) {
      setSessions([]);
      setLoading(false);
      setRefreshing(false);
      return;
    }
    try {
      const next = await quicClient.listAutorunSessions();
      setSessions(next);
      setLastSeenMs(Date.now());
      setError(null);
    } catch (e: any) {
      setError(e?.message ?? "failed to load autoruns");
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
    // Claims are a separate, optional read. Failure is swallowed on purpose: an
    // agent too old to know the verb should show one less line, never break the
    // run list above it.
    try {
      const res = await quicClient.callOps("autorun_leases", {});
      setHolds(res?.ok && Array.isArray(res?.initial?.holds) ? res.initial.holds : []);
    } catch {
      setHolds([]);
    }
  }, [connected]);

  useEffect(() => {
    setLoading(true);
    void load();
    const id = setInterval(() => void load(), 4000);
    return () => clearInterval(id);
  }, [load]);

  const normalized = useMemo(
    () =>
      sessions.map((session) => ({
        ...session,
        finishReason: finishedLabel(session),
      })) as AutorunSession[],
    [sessions],
  );
  const { slots, overflow } = useAgentSlots(normalized, slotKeyForAutorun, DEFAULT_SLOT_COUNT);

  return (
    <ScreenScaffold unbounded>
      <Stack.Screen options={{ title: "Autoruns", headerShown: false }} />
      <View style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}>
        <View style={styles.headerRow}>
          <AppBackButton onPress={() => router.back()} />
          <View style={{ flex: 1 }}>
            <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700" }}>Autoruns</Text>
            <Text style={{ color: c.textMuted, fontSize: 11 }} numberOfLines={1}>
              {activeDevice?.name || "Current device"} · {connected ? "live" : "not connected"}
            </Text>
          </View>
          <Pressable onPress={() => { setRefreshing(true); void load(); }} hitSlop={8}>
            <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Refresh</Text>
          </Pressable>
        </View>

        {holds.length > 0 ? (
          <View style={{ paddingHorizontal: 14, paddingBottom: 8 }}>
            <Text style={{ color: c.textMuted, fontSize: 11 }} numberOfLines={2}>
              {/* One line, because this is a phone. The contended thing and who
                  has it is the whole answer to "why won't my run start?" — the
                  reasoning lives on web, which has room for it. */}
              Claimed: {holds
                .map((h) => `${h.key?.Name ?? "?"} (${h.slot || h.holder || "?"})`)
                .slice(0, 3)
                .join(" · ")}
              {holds.length > 3 ? ` +${holds.length - 3}` : ""}
            </Text>
            {holds.some((h) => h.phase === "build") && !holds.some((h) => h.key?.Class === "seat") ? (
              <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }} numberOfLines={2}>
                A build holds no seat — handed back so other work can run.
              </Text>
            ) : null}
          </View>
        ) : null}

        {!connected ? (
          <View style={styles.center}>
            <Text style={{ color: c.textMuted, fontSize: 13 }}>Connect to a device to read its autorun seats.</Text>
          </View>
        ) : loading ? (
          <View style={styles.center}>
            <ActivityIndicator color={c.accent} />
          </View>
        ) : (
          <ScrollView
            refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => { setRefreshing(true); void load(); }} tintColor={c.accent} />}
            contentContainerStyle={styles.scroll}
          >
            {error ? (
              <View style={[styles.banner, { borderColor: "#ef4444", backgroundColor: "#ef444410" }]}>
                <Text style={{ color: "#ef4444", fontSize: 12 }}>{error}</Text>
              </View>
            ) : null}

            {slots.map((slot) => {
              const session = slot.item;
              if (!session) {
                return (
                  <View key={slot.key} style={[styles.card, { borderColor: c.border, backgroundColor: c.bgCard, opacity: 0.55 }]}>
                    <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", textTransform: "uppercase" }}>
                      Slot {slot.ordinal}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 6 }}>Reserved for the next autorun.</Text>
                  </View>
                );
              }

              const signal = agentSignalFromAutorun(session, Date.now(), lastSeenMs);
              const accent = agentStateColor(signal.state, c);
              const soft = agentStateBg(signal.state, c);
              return (
                <View key={slot.key} style={[styles.card, { borderColor: c.border, backgroundColor: c.bgCard }]}>
                  <View style={styles.row}>
                    <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", textTransform: "uppercase" }}>
                      Slot {slot.ordinal}
                    </Text>
                    <View style={[styles.badge, { backgroundColor: soft, borderColor: accent }]}>
                      <View
                        style={[
                          styles.dot,
                          signal.hollow
                            ? { borderWidth: 1.5, borderColor: accent, backgroundColor: "transparent" }
                            : { backgroundColor: accent },
                        ]}
                      />
                      <Text style={{ color: accent, fontSize: 11, fontWeight: "600" }}>{signal.label}</Text>
                    </View>
                  </View>
                  <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "600", marginTop: 8 }} numberOfLines={1}>
                    {taskLabel(session.task)}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }} numberOfLines={1}>
                    {session.slot}
                  </Text>
                  {session.tmuxSession ? (
                    <Text
                      selectable
                      style={{ color: c.textMuted, fontSize: 11, marginTop: 4, fontFamily: "Menlo" }}
                      numberOfLines={1}
                    >
                      tmux: {session.tmuxSession}
                    </Text>
                  ) : null}
                  <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
                    {session.iterations ?? 0} iterations · {session.commits ?? 0} commits
                    {session.activeRunner ? ` · ${session.activeRunner}` : ""}
                    {session.master ? ` · ${session.master} -> ${session.activeRunner || "doer"}` : ""}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
                    {lastSeenMs ? `Seen ${timeAgo(lastSeenMs)}` : "Waiting for first poll"}
                  </Text>
                  {session.progressTail ? (
                    <Text style={{ color: c.textPrimary, fontSize: 11, marginTop: 8, fontFamily: "Menlo" }} numberOfLines={4}>
                      {session.progressTail.trim().split("\n").slice(-4).join("\n")}
                    </Text>
                  ) : null}
                </View>
              );
            })}

            {overflow.length > 0 ? (
              <View style={[styles.banner, { borderColor: c.border, backgroundColor: c.bgCard }]}>
                <Text style={{ color: c.textMuted, fontSize: 12 }}>
                  {overflow.length} more autorun session{overflow.length === 1 ? "" : "s"} are off deck.
                </Text>
              </View>
            ) : null}
          </ScrollView>
        )}
      </View>
    </ScreenScaffold>
  );
}

const styles = StyleSheet.create({
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    paddingHorizontal: 16,
    paddingBottom: 12,
  },
  center: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: 24,
  },
  scroll: {
    paddingHorizontal: 16,
    paddingBottom: 28,
    gap: 10,
  },
  card: {
    borderWidth: 1,
    borderRadius: 14,
    padding: 14,
  },
  row: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 12,
  },
  badge: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 10,
    paddingVertical: 5,
  },
  dot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  banner: {
    borderWidth: 1,
    borderRadius: 12,
    padding: 12,
  },
});
