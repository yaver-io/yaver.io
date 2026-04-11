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
  type LogEntry,
  type ReleaseManifest,
  type TrackEvent,
  type YaverFlag,
  type YaverMonitor,
} from "../../src/lib/quic";

type Section = "errors" | "releases" | "uptime" | "events" | "flags" | "logs";

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
        {(["errors", "releases", "logs", "uptime", "events", "flags"] as Section[]).map((s) => (
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
      ) : section === "logs" ? (
        <LogsPane />
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

// ── Uptime monitors ────────────────────────────────────────────────

function UptimePane() {
  const c = useColors();
  const [monitors, setMonitors] = useState<YaverMonitor[]>([]);
  const [refreshing, setRefreshing] = useState(false);
  const [newUrl, setNewUrl] = useState("");

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      const list = await quicClient.monitorsList();
      setMonitors(list);
    } finally {
      setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, 15000);
    return () => clearInterval(id);
  }, [refresh]);

  const addMonitor = useCallback(async () => {
    if (!newUrl.trim()) return;
    const ok = await quicClient.monitorsAdd({ url: newUrl.trim() });
    if (ok) {
      setNewUrl("");
      refresh();
    } else {
      Alert.alert("Add failed");
    }
  }, [newUrl, refresh]);

  const removeMonitor = useCallback(
    async (id: string, name?: string) => {
      Alert.alert("Delete?", `Remove monitor ${name ?? id}?`, [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: async () => {
            await quicClient.monitorsRemove(id);
            refresh();
          },
        },
      ]);
    },
    [refresh],
  );

  const togglePause = useCallback(
    async (m: YaverMonitor) => {
      await quicClient.monitorsPause(m.id, !m.paused);
      refresh();
    },
    [refresh],
  );

  const checkNow = useCallback(
    async (id: string) => {
      await quicClient.monitorsCheck(id);
      refresh();
    },
    [refresh],
  );

  return (
    <ScrollView
      contentContainerStyle={{ padding: 12 }}
      refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
    >
      <Text style={[styles.sectionLabel, { color: c.textSecondary }]}>Add monitor</Text>
      <View style={styles.rolloutRow}>
        <TextInput
          value={newUrl}
          onChangeText={setNewUrl}
          placeholder="https://yaver.io"
          placeholderTextColor={c.textSecondary}
          autoCapitalize="none"
          autoCorrect={false}
          keyboardType="url"
          style={[
            styles.rolloutInput,
            { color: c.textPrimary, borderColor: c.border },
          ]}
        />
        <ActionButton label="Add" onPress={addMonitor} />
      </View>
      <Text style={[styles.cardMeta, { color: c.textSecondary, marginTop: 4 }]}>
        Checked every 60s. Alerts fire after three consecutive failures.
      </Text>

      {monitors.length === 0 ? (
        <View style={styles.empty}>
          <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
            No monitors yet. Drop a URL above, or run{"\n\n"}
            <Text style={styles.mono}>yaver monitor add https://yaver.io</Text>
            {"\n\n"}
            from the agent shell.
          </Text>
        </View>
      ) : (
        monitors.map((m) => {
          const stateColor =
            m.paused
              ? c.textSecondary
              : m.state === "up"
                ? "#22c55e"
                : m.state === "down"
                  ? "#ef4444"
                  : "#eab308";
          return (
            <View key={m.id} style={[styles.card, { borderColor: c.border }]}>
              <View style={styles.cardHeader}>
                <Text style={[styles.cardName, { color: c.textPrimary }]}>
                  {m.name ?? m.url}
                </Text>
                <Text style={[styles.cardStatus, { color: stateColor }]}>
                  {m.paused ? "paused" : m.state}
                </Text>
              </View>
              <Text style={[styles.cardMeta, { color: c.textSecondary }]} numberOfLines={1}>
                {m.url}
              </Text>
              <Text style={[styles.cardMeta, { color: c.textSecondary, marginTop: 2 }]}>
                every {m.interval}
                {m.lastCheckAt ? ` · last ${timeAgo(m.lastCheckAt)}` : ""}
                {m.state !== "unknown" ? ` · streak ${m.streak}` : ""}
              </Text>
              {m.checkSsl && m.sslDaysLeft != null ? (
                <Text
                  style={[
                    styles.cardMeta,
                    {
                      marginTop: 2,
                      color:
                        m.sslDaysLeft <= (m.sslWarnDays ?? 14)
                          ? "#ef4444"
                          : m.sslDaysLeft <= 30
                            ? "#eab308"
                            : "#22c55e",
                      fontWeight: "600",
                    },
                  ]}
                >
                  🔒 cert expires in {m.sslDaysLeft}d
                  {m.sslExpiresAt ? ` (${m.sslExpiresAt.slice(0, 10)})` : ""}
                </Text>
              ) : null}
              <View style={{ flexDirection: "row", marginTop: 8, flexWrap: "wrap" }}>
                <ActionButton label="Check now" onPress={() => checkNow(m.id)} />
                <ActionButton
                  label={m.paused ? "Resume" : "Pause"}
                  onPress={() => togglePause(m)}
                />
                <ActionButton
                  label="Delete"
                  onPress={() => removeMonitor(m.id, m.name)}
                />
              </View>
            </View>
          );
        })
      )}
    </ScrollView>
  );
}

// ── Events (track() ingest) ────────────────────────────────────────

function EventsPane() {
  const c = useColors();
  const [events, setEvents] = useState<TrackEvent[]>([]);
  const [refreshing, setRefreshing] = useState(false);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      const list = await quicClient.analyticsEvents(undefined, 200);
      setEvents(list);
    } finally {
      setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, 10000);
    return () => clearInterval(id);
  }, [refresh]);

  return (
    <ScrollView
      contentContainerStyle={{ padding: 12 }}
      refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
    >
      <Text style={[styles.cardMeta, { color: c.textSecondary }]}>
        {events.length} recent event{events.length === 1 ? "" : "s"} · ring-bounded
      </Text>
      {events.length === 0 ? (
        <View style={styles.empty}>
          <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
            No events yet
          </Text>
          <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
            Wire your SDK to call{" "}
            <Text style={styles.mono}>yaver.track("purchase_completed", {`{ amount: 9.99 }`})</Text>
            {"\n\n"}
            Events stream into{" "}
            <Text style={styles.mono}>~/.yaver/analytics/events.jsonl</Text> and
            can be exported via{" "}
            <Text style={styles.mono}>GET /analytics/events.csv</Text>.
          </Text>
        </View>
      ) : (
        events.map((ev, i) => (
          <View
            key={`${ev.timestamp}-${i}`}
            style={[styles.card, { borderColor: c.border }]}
          >
            <Text style={[styles.cardName, { color: c.textPrimary }]}>{ev.name}</Text>
            <Text style={[styles.cardMeta, { color: c.textSecondary }]}>
              {new Date(ev.timestamp).toLocaleString()}
              {ev.route ? ` · ${ev.route}` : ""}
              {ev.deviceId ? ` · ${ev.deviceId.slice(0, 8)}` : ""}
            </Text>
            {ev.props && Object.keys(ev.props).length > 0 ? (
              <Text style={[styles.cardMeta, styles.mono, { color: c.textSecondary, marginTop: 4 }]} numberOfLines={3}>
                {JSON.stringify(ev.props)}
              </Text>
            ) : null}
          </View>
        ))
      )}
    </ScrollView>
  );
}

// ── Logs (cross-device grep) ───────────────────────────────────────

function LogsPane() {
  const c = useColors();
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [refreshing, setRefreshing] = useState(false);
  const [query, setQuery] = useState("");
  const [level, setLevel] = useState<"" | "info" | "warn" | "error">("");

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      const data = await quicClient.logsSearch({
        q: query || undefined,
        level: level || undefined,
        limit: 200,
      });
      setEntries(data);
    } finally {
      setRefreshing(false);
    }
  }, [query, level]);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, 8000);
    return () => clearInterval(id);
  }, [refresh]);

  const levelColor = (lvl: string) =>
    lvl === "error" ? "#ef4444" : lvl === "warn" ? "#eab308" : c.textSecondary;

  return (
    <View style={{ flex: 1 }}>
      <View style={{ padding: 12 }}>
        <TextInput
          value={query}
          onChangeText={setQuery}
          onSubmitEditing={refresh}
          placeholder="grep message…"
          placeholderTextColor={c.textSecondary}
          autoCapitalize="none"
          autoCorrect={false}
          style={[
            styles.rolloutInput,
            { color: c.textPrimary, borderColor: c.border, marginRight: 0 },
          ]}
        />
        <View style={{ flexDirection: "row", marginTop: 8 }}>
          {(["", "info", "warn", "error"] as const).map((lvl) => (
            <Pressable
              key={lvl || "all"}
              onPress={() => setLevel(lvl)}
              style={[
                styles.pill,
                {
                  backgroundColor:
                    level === lvl ? "#6366f1" : "rgba(255,255,255,0.08)",
                },
              ]}
            >
              <Text
                style={{
                  color: level === lvl ? "#fff" : c.textSecondary,
                  fontSize: 12,
                }}
              >
                {lvl || "all"}
              </Text>
            </Pressable>
          ))}
        </View>
      </View>
      <FlatList
        data={entries}
        keyExtractor={(_, i) => String(i)}
        refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
        contentContainerStyle={{ padding: 12, paddingTop: 0 }}
        ListEmptyComponent={
          <View style={styles.empty}>
            <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
              No log entries match
            </Text>
            <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
              Call <Text style={styles.mono}>BlackBox.log/warn/error</Text> from
              your app or opt into{" "}
              <Text style={styles.mono}>BlackBox.wrapConsole()</Text> to
              capture console calls. Cross-device ring, searchable here.
            </Text>
          </View>
        }
        renderItem={({ item }) => (
          <View style={[styles.card, { borderColor: c.border }]}>
            <Text style={[styles.cardMeta, { color: levelColor(item.level) }]}>
              [{item.level}] {new Date(item.timestamp).toLocaleTimeString()}
              {item.deviceId ? ` · ${item.deviceId.slice(0, 8)}` : ""}
              {item.source ? ` · ${item.source}` : ""}
              {item.route ? ` · ${item.route}` : ""}
            </Text>
            <Text style={[styles.mono, { color: c.textPrimary, marginTop: 2, fontSize: 12 }]}>
              {item.message}
            </Text>
          </View>
        )}
      />
    </View>
  );
}

// ── Feature flags ──────────────────────────────────────────────────

function FlagsPane() {
  const c = useColors();
  const [flags, setFlags] = useState<YaverFlag[]>([]);
  const [refreshing, setRefreshing] = useState(false);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      const list = await quicClient.flagsList();
      setFlags(list);
    } finally {
      setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const toggleFlag = useCallback(
    async (f: YaverFlag) => {
      if (f.type !== "bool") return;
      const next: YaverFlag = { ...f, defaultBool: !f.defaultBool };
      await quicClient.flagsSet(next);
      refresh();
    },
    [refresh],
  );

  const setRollout = useCallback(
    (f: YaverFlag) => {
      Alert.prompt(
        "Rollout percent",
        `0..100 for ${f.key}`,
        async (text) => {
          const pct = parseInt(text, 10);
          if (isNaN(pct) || pct < 0 || pct > 100) return;
          const next: YaverFlag = { ...f, rolloutPercent: pct };
          await quicClient.flagsSet(next);
          refresh();
        },
        "plain-text",
        String(f.rolloutPercent),
      );
    },
    [refresh],
  );

  const deleteFlag = useCallback(
    (f: YaverFlag) => {
      Alert.alert("Delete flag?", f.key, [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: async () => {
            await quicClient.flagsDelete(f.key);
            refresh();
          },
        },
      ]);
    },
    [refresh],
  );

  return (
    <ScrollView
      contentContainerStyle={{ padding: 12 }}
      refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
    >
      {flags.length === 0 ? (
        <View style={styles.empty}>
          <Text style={[styles.emptyTitle, { color: c.textPrimary }]}>
            No flags yet
          </Text>
          <Text style={[styles.emptyBody, { color: c.textSecondary }]}>
            From the dev's machine:{"\n\n"}
            <Text style={styles.mono}>
              yaver flags set checkout_v2 false --rollout 20
            </Text>
            {"\n\n"}
            Then your SDK polls{" "}
            <Text style={styles.mono}>/flags/eval?userId=x</Text> and caches
            the result for 30s.
          </Text>
        </View>
      ) : (
        flags.map((f) => {
          const isOn = f.type === "bool" ? f.defaultBool : !!f.defaultString;
          return (
            <View key={f.key} style={[styles.card, { borderColor: c.border }]}>
              <View style={styles.cardHeader}>
                <Text style={[styles.cardName, { color: c.textPrimary }]}>{f.key}</Text>
                <Text style={[styles.cardStatus, { color: isOn ? "#22c55e" : c.textSecondary }]}>
                  {f.type === "bool" ? (isOn ? "ON" : "OFF") : f.defaultString}
                </Text>
              </View>
              {f.description ? (
                <Text style={[styles.cardMeta, { color: c.textSecondary }]}>
                  {f.description}
                </Text>
              ) : null}
              <Text style={[styles.cardMeta, { color: c.textSecondary, marginTop: 2 }]}>
                rollout {f.rolloutPercent}%
                {f.overrides && Object.keys(f.overrides).length > 0
                  ? ` · ${Object.keys(f.overrides).length} override(s)`
                  : ""}
              </Text>
              <View style={{ flexDirection: "row", marginTop: 8, flexWrap: "wrap" }}>
                {f.type === "bool" ? (
                  <ActionButton label={isOn ? "Flip OFF" : "Flip ON"} onPress={() => toggleFlag(f)} />
                ) : null}
                <ActionButton label="Rollout" onPress={() => setRollout(f)} />
                <ActionButton label="Delete" onPress={() => deleteFlag(f)} />
              </View>
            </View>
          );
        })
      )}
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
