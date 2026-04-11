// Monitor tab — production observability + OTA + feature flags.
//
// Single destination for the features that make yaver a one-stop
// replacement for Sentry / EAS Update / BetterStack / LaunchDarkly.
// Five sub-tabs share one screen so the tab bar stays small:
//
//   Errors    cross-device error aggregation (E1)
//   Releases  self-hosted OTA channels + rollout (R1)
//   Uptime    yaver monitor URL checks (U1)
//   Events    BlackBox track() event feed (A1)
//   Flags     feature flag list + evaluation (F1)
//
// Every section pulls from the existing P2P transport — no Convex,
// no central server, no SaaS roundtrip.

import React, { useCallback, useEffect, useState } from "react";
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
import { SafeAreaView } from "react-native-safe-area-context";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import {
  quicClient,
  type ErrorRecord,
  type ErrorsListResponse,
  type ReleaseManifest,
} from "../../src/lib/quic";

type Section = "errors" | "releases" | "uptime" | "events" | "flags";

export default function MonitorScreen() {
  const c = useColors();
  const { connectionStatus } = useDevice();
  const isConnected = connectionStatus === "connected";

  const [section, setSection] = useState<Section>("errors");

  return (
    <SafeAreaView style={[styles.root, { backgroundColor: c.bg }]} edges={["top"]}>
      <View style={[styles.header, { borderBottomColor: c.border }]}>
        <Text style={[styles.title, { color: c.textPrimary }]}>Monitor</Text>
        <Text style={[styles.subtitle, { color: c.textSecondary }]}>
          Errors · Releases · Uptime · Events · Flags
        </Text>
      </View>

      <View style={[styles.tabs, { borderBottomColor: c.border }]}>
        {(["errors", "releases", "uptime", "events", "flags"] as Section[]).map((s) => (
          <Pressable key={s} onPress={() => setSection(s)} style={styles.tabBtn}>
            <Text
              style={[
                styles.tabText,
                {
                  color: section === s ? c.textPrimary : c.textSecondary,
                  borderBottomColor: section === s ? c.tabActive : "transparent",
                },
              ]}
            >
              {s[0].toUpperCase() + s.slice(1)}
            </Text>
          </Pressable>
        ))}
      </View>

      {!isConnected ? (
        <View style={styles.empty}>
          <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
            Not connected
          </Text>
          <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
            Connect to an agent first. The Monitor tab talks to your local
            agent over P2P — no central server, no SaaS account.
          </Text>
        </View>
      ) : section === "errors" ? (
        <ErrorsPane />
      ) : section === "releases" ? (
        <ReleasesPane />
      ) : section === "uptime" ? (
        <UptimePane />
      ) : section === "events" ? (
        <EventsPane />
      ) : (
        <FlagsPane />
      )}
    </SafeAreaView>
  );
}

// ── Errors ─────────────────────────────────────────────────────────

function ErrorsPane() {
  const c = useColors();
  const [data, setData] = useState<ErrorsListResponse | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [includeResolved, setIncludeResolved] = useState(false);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      const res = await quicClient.errorsList(includeResolved);
      setData(res);
    } finally {
      setRefreshing(false);
    }
  }, [includeResolved]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const resolveError = useCallback(
    async (fp: string) => {
      const ok = await quicClient.errorResolve(fp);
      if (ok) {
        refresh();
      } else {
        Alert.alert("Resolve failed", "Could not mark error as resolved.");
      }
    },
    [refresh],
  );

  const reopenError = useCallback(
    async (fp: string) => {
      await quicClient.errorReopen(fp);
      refresh();
    },
    [refresh],
  );

  const records = data?.errors ?? [];
  const stats = data?.stats;

  return (
    <View style={{ flex: 1 }}>
      <View style={[styles.statsRow, { borderBottomColor: c.border }]}>
        <Stat label="Open" value={stats?.open ?? 0} color="#ef4444" c={c} />
        <Stat label="Last 24h" value={stats?.openLast24h ?? 0} color="#eab308" c={c} />
        <Stat label="Resolved" value={stats?.resolved ?? 0} color="#22c55e" c={c} />
        <Stat label="Total" value={stats?.totalDistinct ?? 0} color={c.textSecondary} c={c} />
      </View>
      <View style={styles.toggleRow}>
        <Pressable
          onPress={() => setIncludeResolved((v) => !v)}
          style={{
            paddingHorizontal: 12,
            paddingVertical: 6,
            borderRadius: 999,
            backgroundColor: includeResolved ? "#6366f1" : "rgba(255,255,255,0.08)",
          }}
        >
          <Text style={{ color: includeResolved ? "#fff" : c.textSecondary, fontSize: 12 }}>
            {includeResolved ? "Hiding nothing" : "Hiding resolved"}
          </Text>
        </Pressable>
      </View>
      <FlatList
        data={records}
        keyExtractor={(it) => it.fingerprint}
        refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
        contentContainerStyle={{ padding: 12 }}
        ListEmptyComponent={
          <View style={styles.empty}>
            <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
              No {includeResolved ? "" : "open "}errors
            </Text>
            <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
              Drop the Feedback SDK into your app and the agent aggregates
              errors across every device here — cross-session dedup,
              per-fingerprint history, one-tap resolve.
            </Text>
          </View>
        }
        renderItem={({ item }) => (
          <ErrorCard record={item} onResolve={resolveError} onReopen={reopenError} />
        )}
      />
    </View>
  );
}

function ErrorCard({
  record,
  onResolve,
  onReopen,
}: {
  record: ErrorRecord;
  onResolve: (fp: string) => void;
  onReopen: (fp: string) => void;
}) {
  const c = useColors();
  return (
    <View style={[styles.card, { borderColor: c.border }]}>
      <View style={styles.cardHeader}>
        <Text
          style={[styles.cardName, { color: record.fatal ? "#ef4444" : c.textPrimary, flex: 1 }]}
          numberOfLines={2}
        >
          {record.fatal ? "💥 " : ""}
          {record.message}
        </Text>
        <Text style={[styles.cardStatus, { color: record.resolved ? "#22c55e" : "#eab308" }]}>
          ×{record.count}
        </Text>
      </View>
      {record.firstFrame ? (
        <Text style={[styles.mono, { color: c.textSecondary }]} numberOfLines={1}>
          {record.firstFrame}
        </Text>
      ) : null}
      <Text style={[styles.cardMeta, { color: c.textSecondary, marginTop: 4 }]}>
        {record.deviceIds.length} device{record.deviceIds.length === 1 ? "" : "s"} · last{" "}
        {timeAgo(record.lastSeenAt)}
        {record.resolved ? " · resolved" : ""}
      </Text>
      {record.resolvedNote ? (
        <Text style={[styles.cardMeta, { color: c.textSecondary, marginTop: 2 }]}>
          note: {record.resolvedNote}
        </Text>
      ) : null}
      <View style={{ flexDirection: "row", marginTop: 8 }}>
        {record.resolved ? (
          <ActionButton label="Reopen" onPress={() => onReopen(record.fingerprint)} />
        ) : (
          <ActionButton label="Resolve" onPress={() => onResolve(record.fingerprint)} />
        )}
      </View>
    </View>
  );
}

// ── Releases ───────────────────────────────────────────────────────

function ReleasesPane() {
  const c = useColors();
  const [channel, setChannel] = useState("production");
  const [manifest, setManifest] = useState<ReleaseManifest | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [rolloutInput, setRolloutInput] = useState("");

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      const m = await quicClient.releasesList(channel);
      setManifest(m);
      if (m) setRolloutInput(String(m.rolloutPercent));
    } finally {
      setRefreshing(false);
    }
  }, [channel]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const rollback = useCallback(
    async (semver: string) => {
      Alert.alert(
        "Roll back?",
        `Flip channel "${channel}" latest to ${semver}?`,
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Roll back",
            style: "destructive",
            onPress: async () => {
              const ok = await quicClient.releasesRollback(channel, semver);
              if (ok) refresh();
              else Alert.alert("Rollback failed");
            },
          },
        ],
      );
    },
    [channel, refresh],
  );

  const saveRollout = useCallback(async () => {
    const pct = parseInt(rolloutInput, 10);
    if (isNaN(pct) || pct < 0 || pct > 100) {
      Alert.alert("Invalid rollout", "Enter an integer 0..100");
      return;
    }
    const ok = await quicClient.releasesRollout(channel, pct);
    if (ok) refresh();
    else Alert.alert("Rollout failed");
  }, [channel, rolloutInput, refresh]);

  return (
    <ScrollView
      contentContainerStyle={{ padding: 12 }}
      refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
    >
      <Text style={[styles.sectionLabel, { color: c.textSecondary }]}>Channel</Text>
      <View style={styles.channelRow}>
        {(["production", "staging", "canary"] as const).map((ch) => (
          <Pressable
            key={ch}
            onPress={() => setChannel(ch)}
            style={[
              styles.pill,
              {
                backgroundColor: channel === ch ? "#6366f1" : "rgba(255,255,255,0.08)",
              },
            ]}
          >
            <Text style={{ color: channel === ch ? "#fff" : c.textSecondary, fontSize: 12 }}>
              {ch}
            </Text>
          </Pressable>
        ))}
      </View>

      {!manifest || manifest.releases.length === 0 ? (
        <View style={styles.empty}>
          <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
            No releases in {channel}
          </Text>
          <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
            On the dev's machine:{"\n\n"}
            <Text style={styles.mono}>yaver release publish --channel {channel}</Text>
            {"\n\n"}
            Compiles your RN project with the embedded hermesc, stashes the
            bundle under <Text style={styles.mono}>~/.yaver/releases/</Text>,
            and serves it through the P2P relay.
          </Text>
        </View>
      ) : (
        <>
          <Text style={[styles.sectionLabel, { color: c.textSecondary, marginTop: 16 }]}>
            Latest
          </Text>
          <View style={[styles.card, { borderColor: c.border }]}>
            <Text style={[styles.cardName, { color: c.textPrimary }]}>
              {manifest.latest ?? "(none)"}
            </Text>
            <Text style={[styles.cardMeta, { color: c.textSecondary }]}>
              {manifest.releases.length} release{manifest.releases.length === 1 ? "" : "s"}
              {" · updated "}
              {timeAgo(manifest.updatedAt)}
            </Text>
          </View>

          <Text style={[styles.sectionLabel, { color: c.textSecondary, marginTop: 16 }]}>
            Rollout
          </Text>
          <View style={styles.rolloutRow}>
            <TextInput
              value={rolloutInput}
              onChangeText={setRolloutInput}
              keyboardType="number-pad"
              placeholder="100"
              placeholderTextColor={c.textSecondary}
              style={[
                styles.rolloutInput,
                { color: c.textPrimary, borderColor: c.border },
              ]}
            />
            <Text style={[styles.cardMeta, { color: c.textSecondary, marginRight: 8 }]}>%</Text>
            <ActionButton label="Save" onPress={saveRollout} />
          </View>
          <Text style={[styles.cardMeta, { color: c.textSecondary, marginTop: 4 }]}>
            0 = everyone runs the previous bundle · 100 = everyone on latest · 50
            = half the devices (stable per-device hash bucket)
          </Text>

          <Text style={[styles.sectionLabel, { color: c.textSecondary, marginTop: 16 }]}>
            History
          </Text>
          {manifest.releases.map((r) => {
            const isLatest = r.semver === manifest.latest;
            return (
              <View key={r.semver} style={[styles.card, { borderColor: c.border }]}>
                <View style={styles.cardHeader}>
                  <Text style={[styles.cardName, { color: c.textPrimary }]}>
                    {isLatest ? "→ " : "   "}
                    {r.semver}
                  </Text>
                  <Text style={[styles.cardStatus, { color: c.textSecondary }]}>
                    {Math.round(r.size / 1024)}kb · bc{r.hermesBcVersion}
                  </Text>
                </View>
                <Text style={[styles.cardMeta, { color: c.textSecondary }]}>
                  {timeAgo(r.publishedAt)}
                  {r.commit ? ` · ${r.commit}` : ""}
                </Text>
                {r.notes ? (
                  <Text style={[styles.cardMeta, { color: c.textSecondary, marginTop: 2 }]}>
                    {r.notes}
                  </Text>
                ) : null}
                {!isLatest && (
                  <View style={{ flexDirection: "row", marginTop: 8 }}>
                    <ActionButton label="Roll back to this" onPress={() => rollback(r.semver)} />
                  </View>
                )}
              </View>
            );
          })}
        </>
      )}
    </ScrollView>
  );
}

// ── Stubs for U1 / A1 / F1 — the server side lands next ────────────

function UptimePane() {
  const c = useColors();
  return (
    <ScrollView contentContainerStyle={{ padding: 12 }}>
      <View style={styles.empty}>
        <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>Uptime checks</Text>
        <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
          Coming in U1. From the dev's machine:{"\n\n"}
          <Text style={styles.mono}>yaver monitor add https://yaver.io</Text>
          {"\n\n"}
          Schedules 30s checks through the agent's cron, alerts your phone on
          three consecutive failures. No vendor account.
        </Text>
      </View>
    </ScrollView>
  );
}

function EventsPane() {
  const c = useColors();
  return (
    <ScrollView contentContainerStyle={{ padding: 12 }}>
      <View style={styles.empty}>
        <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>Events stream</Text>
        <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
          Coming in A1. The Feedback SDK adds a{" "}
          <Text style={styles.mono}>yaver.track("purchase_completed", {`{ amount: 9.99 }`})</Text>{" "}
          channel that funnels through BlackBox into a local ring buffer. No
          dashboards — CSV export + optional PostHog webhook bridge.
        </Text>
      </View>
    </ScrollView>
  );
}

function FlagsPane() {
  const c = useColors();
  return (
    <ScrollView contentContainerStyle={{ padding: 12 }}>
      <View style={styles.empty}>
        <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>Feature flags</Text>
        <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
          Coming in F1. The agent serves{" "}
          <Text style={styles.mono}>/flags/eval</Text> with hashed-bucket
          percentage rollouts and stable per-user on/off. Flip flags from your
          phone; the SDK polls through the existing P2P channel.
        </Text>
      </View>
    </ScrollView>
  );
}

// ── Shared UI bits ─────────────────────────────────────────────────

function Stat({
  label,
  value,
  color,
  c,
}: {
  label: string;
  value: number;
  color: string;
  c: ReturnType<typeof useColors>;
}) {
  return (
    <View style={styles.statCell}>
      <Text style={[styles.statValue, { color }]}>{value}</Text>
      <Text style={[styles.statLabel, { color: c.textSecondary }]}>{label}</Text>
    </View>
  );
}

function ActionButton({ label, onPress }: { label: string; onPress: () => void }) {
  return (
    <Pressable
      onPress={onPress}
      style={{
        paddingHorizontal: 14,
        paddingVertical: 6,
        borderRadius: 999,
        backgroundColor: "#6366f1",
        marginRight: 8,
      }}
    >
      <Text style={{ color: "#fff", fontSize: 12, fontWeight: "600" }}>{label}</Text>
    </Pressable>
  );
}

function timeAgo(isoStr: string): string {
  if (!isoStr) return "";
  const now = Date.now();
  const then = Date.parse(isoStr);
  if (isNaN(then)) return isoStr;
  const diff = Math.max(0, Math.floor((now - then) / 1000));
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  header: { paddingHorizontal: 16, paddingVertical: 12, borderBottomWidth: 1 },
  title: { fontSize: 20, fontWeight: "700" },
  subtitle: { fontSize: 12, marginTop: 2 },
  tabs: { flexDirection: "row", borderBottomWidth: 1 },
  tabBtn: { paddingHorizontal: 14, paddingVertical: 12 },
  tabText: {
    fontSize: 13,
    fontWeight: "600",
    paddingBottom: 8,
    borderBottomWidth: 2,
  },
  empty: { padding: 24 },
  emptyTitle: { fontSize: 16, fontWeight: "700", marginBottom: 8 },
  emptyBody: { fontSize: 13, lineHeight: 20 },
  statsRow: {
    flexDirection: "row",
    paddingVertical: 10,
    borderBottomWidth: 1,
    justifyContent: "space-around",
  },
  statCell: { alignItems: "center" },
  statValue: { fontSize: 20, fontWeight: "700" },
  statLabel: { fontSize: 11, marginTop: 2 },
  toggleRow: {
    flexDirection: "row",
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  card: {
    marginTop: 10,
    padding: 12,
    borderRadius: 10,
    borderWidth: 1,
  },
  cardHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
  },
  cardName: { fontSize: 15, fontWeight: "700" },
  cardStatus: { fontSize: 12, fontWeight: "700", marginLeft: 8 },
  cardMeta: { fontSize: 12 },
  mono: { fontFamily: "Courier" },
  sectionLabel: { fontSize: 11, fontWeight: "600", textTransform: "uppercase", marginTop: 4 },
  channelRow: { flexDirection: "row", marginTop: 6 },
  pill: {
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 999,
    marginRight: 8,
  },
  rolloutRow: { flexDirection: "row", alignItems: "center", marginTop: 6 },
  rolloutInput: {
    flex: 1,
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 10,
    paddingVertical: 6,
    marginRight: 8,
    fontFamily: "Courier",
    fontSize: 14,
  },
});
