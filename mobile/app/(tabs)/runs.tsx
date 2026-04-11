// Yaver-test-sdk "Runs" screen — local CI orchestrator UI for the
// solo developer. Lists the agent's spec files, kicks off a run with
// one tap, and shows the live result + history. Everything goes over
// the existing P2P transport: no Convex, no central server, no leak.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Animated,
  Dimensions,
  FlatList,
  Image,
  Modal,
  PanResponder,
  Pressable,
  RefreshControl,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import {
  quicClient,
  TestkitAutoFix,
  TestkitFlakeStats,
  TestkitHistoryEntry,
  TestkitIntegration,
  TestkitNotification,
  TestkitPassMarker,
  TestkitRunStatus,
  TestkitSpec,
  TestkitUSBDevice,
} from "../../src/lib/quic";

type Tab = "specs" | "history" | "flake" | "alerts" | "devices" | "fixes" | "setup";

export default function RunsScreen() {
  const c = useColors();
  const { connectionStatus } = useDevice();
  const isConnected = connectionStatus === "connected";

  const [tab, setTab] = useState<Tab>("specs");
  const [specs, setSpecs] = useState<TestkitSpec[]>([]);
  const [status, setStatus] = useState<TestkitRunStatus | null>(null);
  const [history, setHistory] = useState<TestkitHistoryEntry[]>([]);
  const [flake, setFlake] = useState<TestkitFlakeStats[]>([]);
  const [alerts, setAlerts] = useState<TestkitNotification[]>([]);
  const [markers, setMarkers] = useState<TestkitPassMarker[]>([]);
  const [devices, setDevices] = useState<TestkitUSBDevice[]>([]);
  const [integrations, setIntegrations] = useState<TestkitIntegration[]>([]);
  const [autofixes, setAutofixes] = useState<TestkitAutoFix[]>([]);
  const [shotPath, setShotPath] = useState<string | null>(null);
  // Snapshot diff viewer — pass a base path ending in `.png` (before
  // we suffix `.current.png` / `.diff.png`) to open the three-pane
  // comparator. null closes the modal.
  const [snapshotBase, setSnapshotBase] = useState<string | null>(null);
  const [snapshotPane, setSnapshotPane] = useState<"baseline" | "current" | "diff">("baseline");
  const [refreshing, setRefreshing] = useState(false);
  const [starting, setStarting] = useState(false);
  const [headful, setHeadful] = useState(false);
  const [retries, setRetries] = useState(0);
  const [acOnly, setAcOnly] = useState(true);

  const refresh = useCallback(async () => {
    if (!isConnected) return;
    setRefreshing(true);
    try {
      const [s, st, h, f, a, m, d, i, af] = await Promise.all([
        quicClient.testkitListSpecs(),
        quicClient.testkitRunStatus(),
        quicClient.testkitHistory(),
        quicClient.testkitFlakeReport(),
        quicClient.testkitNotifications(),
        quicClient.testkitMarkers(),
        quicClient.testkitDevices(),
        quicClient.testkitIntegrations(),
        quicClient.testkitAutoFix(),
      ]);
      setSpecs(s);
      setStatus(st);
      setHistory(h);
      setFlake(f);
      setAlerts(a);
      setMarkers(m);
      setDevices(d);
      setIntegrations(i);
      setAutofixes(af);
    } finally {
      setRefreshing(false);
    }
  }, [isConnected]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Poll status while a run is in progress so the spinner updates live.
  useEffect(() => {
    if (!isConnected || !status?.running) return;
    const t = setInterval(async () => {
      const st = await quicClient.testkitRunStatus();
      setStatus(st);
      if (st && !st.running) {
        // Run just finished — refresh history.
        const h = await quicClient.testkitHistory();
        setHistory(h);
      }
    }, 1500);
    return () => clearInterval(t);
  }, [isConnected, status?.running]);

  const startRun = async () => {
    setStarting(true);
    const result = await quicClient.testkitStartRun({
      headful,
      retries,
      ac_power_only: acOnly,
      concurrency: 2,
    });
    setStarting(false);
    if (!result.ok) {
      // Show inline; mobile alerts on every error get noisy.
      setStatus({
        running: false,
        root: status?.root ?? "",
        last_suite: undefined,
      } as any);
      return;
    }
    // Optimistic flip to "running" so the spinner shows immediately.
    setStatus((prev) => (prev ? { ...prev, running: true } : prev));
    // Refresh after a tick.
    setTimeout(refresh, 300);
  };

  if (!isConnected) {
    return (
      <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <View style={styles.center}>
          <Text style={[styles.muted, { color: c.textMuted }]}>
            Connect to a device to use the local CI runner.
          </Text>
        </View>
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      {/* Tabs — horizontally scrollable since we now have 7 */}
      <View style={[styles.tabBar, { borderColor: c.border }]}>
        <FlatList
          horizontal
          showsHorizontalScrollIndicator={false}
          data={["specs", "history", "alerts", "fixes", "devices", "flake", "setup"] as Tab[]}
          keyExtractor={(t) => t}
          renderItem={({ item: t }) => {
            const label =
              t === "specs" ? "Specs" :
              t === "history" ? "Runs" :
              t === "flake" ? "Flake" :
              t === "alerts" ? "Alerts" :
              t === "devices" ? "Devices" :
              t === "fixes" ? "Auto-fixes" :
              "Setup";
            let badge = "";
            if (t === "alerts" && alerts.length > 0) badge = ` (${alerts.length})`;
            if (t === "devices" && devices.length > 0) badge = ` (${devices.length})`;
            if (t === "fixes" && autofixes.filter(f => f.state === "applied").length > 0) {
              badge = ` (${autofixes.filter(f => f.state === "applied").length})`;
            }
            if (t === "setup") {
              const missing = integrations.filter(i => !i.installed).length;
              if (missing > 0) badge = ` (${missing})`;
            }
            return (
              <Pressable
                onPress={() => setTab(t)}
                style={[
                  styles.tabButton,
                  tab === t && { borderBottomColor: c.accent, borderBottomWidth: 2 },
                  { paddingHorizontal: 16 },
                ]}
              >
                <Text style={{ color: tab === t ? c.textPrimary : c.textMuted, fontWeight: "600" }}>
                  {label}{badge}
                </Text>
              </Pressable>
            );
          }}
        />
      </View>

      {/* Run controls */}
      {tab === "specs" && (
        <View style={[styles.controls, { borderColor: c.border, backgroundColor: c.bgCard }]}>
          <View style={styles.row}>
            <Text style={[styles.controlLabel, { color: c.textPrimary }]}>Headful</Text>
            <Switch value={headful} onValueChange={setHeadful} />
          </View>
          <View style={styles.row}>
            <Text style={[styles.controlLabel, { color: c.textPrimary }]}>AC power only</Text>
            <Switch value={acOnly} onValueChange={setAcOnly} />
          </View>
          <View style={styles.row}>
            <Text style={[styles.controlLabel, { color: c.textPrimary }]}>Retries: {retries}</Text>
            <View style={{ flexDirection: "row", gap: 8 }}>
              <Pressable onPress={() => setRetries(Math.max(0, retries - 1))} style={[styles.btnSmall, { backgroundColor: c.bg }]}>
                <Text style={{ color: c.textPrimary }}>−</Text>
              </Pressable>
              <Pressable onPress={() => setRetries(Math.min(5, retries + 1))} style={[styles.btnSmall, { backgroundColor: c.bg }]}>
                <Text style={{ color: c.textPrimary }}>+</Text>
              </Pressable>
            </View>
          </View>
          <Pressable
            onPress={startRun}
            disabled={starting || status?.running}
            style={[
              styles.runBtn,
              { backgroundColor: c.accent || "#6366f1" },
              (starting || status?.running) && { opacity: 0.5 },
            ]}
          >
            {status?.running ? (
              <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                <ActivityIndicator color="#fff" />
                <Text style={styles.runBtnText}>Running…</Text>
              </View>
            ) : (
              <Text style={styles.runBtnText}>Run all specs</Text>
            )}
          </Pressable>
          {status?.last_suite && !status.running && (
            <Text style={[styles.muted, { color: c.textMuted, marginTop: 6, textAlign: "center" }]}>
              Last: {status.last_suite.passed}/{status.last_suite.total} passed in {Math.round(status.last_suite.duration_ms / 100) / 10}s
            </Text>
          )}
        </View>
      )}

      {/* Body */}
      {tab === "specs" && (
        <FlatList
          data={specs}
          keyExtractor={(it) => it.path}
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} tintColor={c.textPrimary} />}
          contentContainerStyle={{ padding: 12 }}
          ListEmptyComponent={
            <Text style={[styles.muted, { color: c.textMuted, textAlign: "center", marginTop: 32 }]}>
              No specs found. Create yaver-tests/example.test.yaml in your repo.
            </Text>
          }
          renderItem={({ item }) => (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.cardTitle, { color: c.textPrimary }]}>{item.name}</Text>
              <Text style={[styles.cardSub, { color: c.textMuted }]}>
                {item.target} · {item.step_count} step{item.step_count === 1 ? "" : "s"}
                {item.url ? ` · ${item.url}` : ""}
              </Text>
            </View>
          )}
        />
      )}

      {tab === "history" && (
        <FlatList
          data={history.slice().reverse()}
          keyExtractor={(it) => it.started_at}
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} tintColor={c.textPrimary} />}
          contentContainerStyle={{ padding: 12 }}
          ListEmptyComponent={
            <Text style={[styles.muted, { color: c.textMuted, textAlign: "center", marginTop: 32 }]}>
              No runs yet. Tap "Run all specs" on the Specs tab.
            </Text>
          }
          renderItem={({ item }) => (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
                <Text style={[styles.cardTitle, { color: item.failed > 0 ? "#f87171" : "#4ade80" }]}>
                  {item.failed > 0 ? "✗" : "✓"} {item.passed}/{item.total} passed
                </Text>
                <Text style={[styles.cardSub, { color: c.textMuted }]}>
                  {Math.round(item.duration_ms / 100) / 10}s
                </Text>
              </View>
              <Text style={[styles.cardSub, { color: c.textMuted }]}>
                {new Date(item.started_at).toLocaleString()}
                {item.git_branch ? ` · ${item.git_branch}` : ""}
                {item.flaky_count > 0 ? ` · ${item.flaky_count} flaky` : ""}
              </Text>
              {item.specs.filter((s) => !s.passed).map((s) => {
                const err = s.error || "failed";
                // If the error mentions a snapshot, extract the
                // baseline path and let the dev open the three-pane
                // viewer.
                const snapMatch = err.match(/diff at (.+\.diff\.png)/);
                const baseline = snapMatch ? snapMatch[1].replace(/\.diff\.png$/, ".png") : null;
                return (
                  <Pressable
                    key={s.name}
                    onPress={() => baseline && setSnapshotBase(baseline)}
                  >
                    <Text style={[styles.failLine, { color: "#f87171" }]} numberOfLines={2}>
                      ✗ {s.name}: {err}
                    </Text>
                    {baseline && (
                      <Text style={[styles.cardSub, { color: c.accent || "#6366f1", marginTop: 2 }]}>
                        Tap to compare baseline / current / diff →
                      </Text>
                    )}
                  </Pressable>
                );
              })}
            </View>
          )}
        />
      )}

      {tab === "alerts" && (
        <FlatList
          data={alerts}
          keyExtractor={(it) => it.id}
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} tintColor={c.textPrimary} />}
          contentContainerStyle={{ padding: 12 }}
          ListHeaderComponent={
            markers.length > 0 ? (
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, marginBottom: 12 }]}>
                <Text style={[styles.cardTitle, { color: "#4ade80" }]}>
                  ✓ {markers.length} SHA{markers.length === 1 ? "" : "s"} already passed locally
                </Text>
                {markers.slice(0, 3).map((m) => (
                  <Text key={m.sha} style={[styles.cardSub, { color: c.textMuted }]}>
                    {m.sha.slice(0, 7)} {m.branch ? `· ${m.branch}` : ""} · {m.total} specs
                  </Text>
                ))}
                <Text style={[styles.cardSub, { color: c.textMuted, marginTop: 4, fontStyle: "italic" }]}>
                  GH Actions can short-circuit these via `yaver test sync --check`
                </Text>
              </View>
            ) : null
          }
          ListEmptyComponent={
            <Text style={[styles.muted, { color: c.textMuted, textAlign: "center", marginTop: 32 }]}>
              No failure alerts. The agent posts here whenever a spec breaks.
            </Text>
          }
          renderItem={({ item }) => (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.cardTitle, { color: "#f87171" }]}>
                ✗ {item.spec_name}
              </Text>
              <Text style={[styles.cardSub, { color: c.textMuted }]}>
                {new Date(item.created_at).toLocaleString()}
                {item.git_branch ? ` · ${item.git_branch}` : ""}
              </Text>
              {item.error && (
                <Text style={[styles.failLine, { color: c.textPrimary }]} numberOfLines={4}>
                  {item.error}
                </Text>
              )}
              {item.screenshot && (
                <Pressable onPress={() => setShotPath(item.screenshot!)}>
                  <Text style={[styles.cardSub, { color: c.accent || "#6366f1", marginTop: 4 }]} numberOfLines={1}>
                    📷 View screenshot
                  </Text>
                </Pressable>
              )}
            </View>
          )}
        />
      )}

      {tab === "flake" && (
        <FlatList
          data={flake}
          keyExtractor={(it) => it.path}
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} tintColor={c.textPrimary} />}
          contentContainerStyle={{ padding: 12 }}
          ListEmptyComponent={
            <Text style={[styles.muted, { color: c.textMuted, textAlign: "center", marginTop: 32 }]}>
              No history yet — run a few times to see flake stats.
            </Text>
          }
          renderItem={({ item }) => {
            const ratio = item.total > 0 ? item.failed / item.total : 0;
            const tint = ratio > 0.2 ? "#f87171" : ratio > 0 ? "#facc15" : "#4ade80";
            return (
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <Text style={[styles.cardTitle, { color: tint }]}>{item.name}</Text>
                <Text style={[styles.cardSub, { color: c.textMuted }]}>
                  {item.passed}/{item.total} passed · {item.flaky} flaky · {Math.round(ratio * 100)}% failure rate
                </Text>
              </View>
            );
          }}
        />
      )}

      {tab === "devices" && (
        <FlatList
          data={devices}
          keyExtractor={(it) => it.UDID}
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} tintColor={c.textPrimary} />}
          contentContainerStyle={{ padding: 12 }}
          ListEmptyComponent={
            <Text style={[styles.muted, { color: c.textMuted, textAlign: "center", marginTop: 32 }]}>
              No USB devices connected. Plug your iPhone or Android in and pull to refresh.
            </Text>
          }
          renderItem={({ item }) => (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.cardTitle, { color: c.textPrimary }]}>
                {item.Platform === "ios" ? "🍎" : "🤖"} {item.Name}
              </Text>
              <Text style={[styles.cardSub, { color: c.textMuted }]}>
                {item.OS} · {item.UDID}
              </Text>
            </View>
          )}
        />
      )}

      {tab === "fixes" && (
        <FlatList
          data={autofixes}
          keyExtractor={(it) => it.id}
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} tintColor={c.textPrimary} />}
          contentContainerStyle={{ padding: 12 }}
          ListEmptyComponent={
            <Text style={[styles.muted, { color: c.textMuted, textAlign: "center", marginTop: 32 }]}>
              No autonomous fixes yet. The agent records here whenever it patches a broken spec.
            </Text>
          }
          renderItem={({ item }) => {
            const tint = item.state === "rolled_back" ? "#a1a1aa" : "#4ade80";
            return (
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <Text style={[styles.cardTitle, { color: tint }]}>
                  {item.state === "rolled_back" ? "↩" : "✓"} {item.spec_name}
                </Text>
                <Text style={[styles.cardSub, { color: c.textMuted }]}>
                  {item.strategy} · {new Date(item.created_at).toLocaleString()}
                </Text>
                {item.description && (
                  <Text style={[styles.cardSub, { color: c.textPrimary }]}>{item.description}</Text>
                )}
                {item.notes && (
                  <Text style={[styles.cardSub, { color: c.textMuted }]} numberOfLines={3}>
                    {item.notes}
                  </Text>
                )}
                {item.state === "applied" && (
                  <Pressable
                    style={{
                      marginTop: 8,
                      paddingHorizontal: 12,
                      paddingVertical: 6,
                      borderRadius: 6,
                      backgroundColor: "#ef444422",
                      alignSelf: "flex-start",
                    }}
                    onPress={async () => {
                      const ok = await quicClient.testkitAutoFixUndo(item.id);
                      if (ok) refresh();
                      else Alert.alert("Undo failed");
                    }}
                  >
                    <Text style={{ color: "#f87171", fontSize: 12, fontWeight: "600" }}>Undo</Text>
                  </Pressable>
                )}
              </View>
            );
          }}
        />
      )}

      {tab === "setup" && (
        <FlatList
          data={integrations}
          keyExtractor={(it) => it.name}
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} tintColor={c.textPrimary} />}
          contentContainerStyle={{ padding: 12 }}
          ListHeaderComponent={
            <Text style={[styles.muted, { color: c.textMuted, marginBottom: 12 }]}>
              {"Run `yaver install <name>` from your terminal to install missing pieces."}
            </Text>
          }
          renderItem={({ item }) => (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.cardTitle, { color: item.installed ? "#4ade80" : c.textMuted }]}>
                {item.installed ? "✓" : "—"} {item.name}
              </Text>
              <Text style={[styles.cardSub, { color: c.textMuted }]}>{item.description}</Text>
              {!item.installed && (
                <Text style={[styles.cardSub, { color: c.textPrimary, marginTop: 4 }]}>
                  $ {item.hint}
                </Text>
              )}
            </View>
          )}
        />
      )}

      {/* Screenshot viewer modal — opens when a failure card's
          screenshot path is tapped. Pulls the PNG via the new
          /testkit/artifact endpoint over the existing P2P transport. */}
      <Modal
        visible={!!shotPath}
        animationType="fade"
        transparent
        onRequestClose={() => setShotPath(null)}
      >
        <Pressable
          style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.92)", justifyContent: "center", alignItems: "center" }}
          onPress={() => setShotPath(null)}
        >
          {shotPath && (
            <Image
              source={{
                uri: quicClient.testkitArtifactUrl(shotPath),
                headers: quicClient.testkitArtifactHeaders,
              }}
              style={{ width: "100%", height: "100%", resizeMode: "contain" }}
            />
          )}
          <Text style={{ position: "absolute", bottom: 32, color: "#fff", fontSize: 12 }}>Tap to close</Text>
        </Pressable>
      </Modal>

      {/* Snapshot diff viewer — three panes (baseline / current /
          diff) with tap-swipe between them. Used when a snapshot: step
          fails and the runner wrote current.png + diff.png next to the
          baseline. The base path is the original snapshot PNG (e.g.
          yaver-tests/snapshots/home.png); we suffix it to get the
          other two panes. */}
      <Modal
        visible={!!snapshotBase}
        animationType="fade"
        transparent
        onRequestClose={() => setSnapshotBase(null)}
      >
        <View style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.96)" }}>
          {snapshotBase && (
            <>
              <View style={{ flex: 1, justifyContent: "center", alignItems: "center" }}>
                <Image
                  source={{
                    uri: quicClient.testkitArtifactUrl(
                      snapshotPane === "baseline"
                        ? snapshotBase
                        : snapshotPane === "current"
                          ? snapshotBase.replace(/\.png$/i, ".current.png")
                          : snapshotBase.replace(/\.png$/i, ".diff.png"),
                    ),
                    headers: quicClient.testkitArtifactHeaders,
                  }}
                  style={{ width: "100%", height: "85%", resizeMode: "contain" }}
                />
              </View>
              <View style={{ flexDirection: "row", justifyContent: "space-around", paddingBottom: 40, paddingTop: 16 }}>
                {(["baseline", "current", "diff"] as const).map((p) => (
                  <Pressable
                    key={p}
                    onPress={() => setSnapshotPane(p)}
                    style={{
                      paddingHorizontal: 18,
                      paddingVertical: 8,
                      borderRadius: 999,
                      backgroundColor: snapshotPane === p ? "#6366f1" : "rgba(255,255,255,0.12)",
                    }}
                  >
                    <Text style={{ color: "#fff", fontWeight: "600" }}>
                      {p === "baseline" ? "Baseline" : p === "current" ? "Current" : "Diff"}
                    </Text>
                  </Pressable>
                ))}
              </View>
              <Pressable
                onPress={() => setSnapshotBase(null)}
                style={{ position: "absolute", top: 50, right: 20, padding: 8 }}
              >
                <Text style={{ color: "#fff", fontSize: 18 }}>✕</Text>
              </Pressable>
            </>
          )}
        </View>
      </Modal>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  center: { flex: 1, alignItems: "center", justifyContent: "center", padding: 24 },
  tabBar: {
    flexDirection: "row",
    borderBottomWidth: 1,
  },
  tabButton: {
    flex: 1,
    paddingVertical: 12,
    alignItems: "center",
  },
  controls: {
    margin: 12,
    padding: 12,
    borderWidth: 1,
    borderRadius: 12,
    gap: 8,
  },
  row: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
  },
  controlLabel: { fontSize: 13, fontWeight: "600" },
  runBtn: {
    marginTop: 8,
    paddingVertical: 12,
    borderRadius: 10,
    alignItems: "center",
  },
  runBtnText: { color: "#fff", fontWeight: "700", fontSize: 15 },
  btnSmall: {
    paddingHorizontal: 12,
    paddingVertical: 4,
    borderRadius: 6,
  },
  card: {
    borderWidth: 1,
    borderRadius: 12,
    padding: 12,
    marginBottom: 8,
  },
  cardTitle: { fontSize: 15, fontWeight: "700" },
  cardSub: { fontSize: 12, marginTop: 2 },
  failLine: { fontSize: 12, marginTop: 4 },
  muted: { fontSize: 13 },
});
