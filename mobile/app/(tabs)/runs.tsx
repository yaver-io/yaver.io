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
  type TestkitFrameList,
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
  // Frame-sequence player (screencast playback on step failure).
  // Holds the directory path; the player fetches frames on mount.
  const [framesDir, setFramesDir] = useState<string | null>(null);
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
              {item.screenshot && (
                <Pressable
                  onPress={() => {
                    // testkit.FlushFrames writes `<label>-frames/`
                    // next to the FAIL screenshot, where label is
                    // `<phase>-<index>`. Convert
                    //   /artifacts/step-02-FAIL.png
                    // to
                    //   /artifacts/step-02-frames
                    const dir = item.screenshot!
                      .replace(/-FAIL\.png$/i, "-frames")
                      .replace(/\.png$/i, "-frames");
                    setFramesDir(dir);
                  }}
                >
                  <Text style={[styles.cardSub, { color: c.accent || "#6366f1", marginTop: 2 }]} numberOfLines={1}>
                    🎞  Play failure video
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
          /testkit/artifact endpoint over the existing P2P transport.
          Wrapped in ZoomableImage so the dev can pinch / pan a
          dense UI screenshot on the phone. */}
      <Modal
        visible={!!shotPath}
        animationType="fade"
        transparent
        onRequestClose={() => setShotPath(null)}
      >
        <View
          style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.92)", justifyContent: "center", alignItems: "center" }}
        >
          {shotPath && (
            <ZoomableImage
              uri={quicClient.testkitArtifactUrl(shotPath)}
              headers={quicClient.testkitArtifactHeaders}
            />
          )}
          <Pressable
            onPress={() => setShotPath(null)}
            style={{ position: "absolute", top: 50, right: 20, padding: 8 }}
          >
            <Text style={{ color: "#fff", fontSize: 18 }}>✕</Text>
          </Pressable>
          <Text style={{ position: "absolute", bottom: 32, color: "#fff", fontSize: 12 }}>
            Pinch to zoom · drag to pan · ✕ to close
          </Text>
        </View>
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
                <ZoomableImage
                  uri={quicClient.testkitArtifactUrl(
                    snapshotPane === "baseline"
                      ? snapshotBase
                      : snapshotPane === "current"
                        ? snapshotBase.replace(/\.png$/i, ".current.png")
                        : snapshotBase.replace(/\.png$/i, ".diff.png"),
                  )}
                  headers={quicClient.testkitArtifactHeaders}
                  heightPercent="85%"
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

      {/* Frame-sequence player — scrubs through the PNGs a failing
          spec's screencast left behind. Opens from the "Play failure
          video" pressable on each alert card. */}
      <Modal
        visible={!!framesDir}
        animationType="fade"
        transparent
        onRequestClose={() => setFramesDir(null)}
      >
        <View style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.96)" }}>
          {framesDir && <FrameSequencePlayer dir={framesDir} onClose={() => setFramesDir(null)} />}
        </View>
      </Modal>
    </SafeAreaView>
  );
}

// FrameSequencePlayer scrubs through a directory of screencast PNGs
// via the /testkit/frames listing + /testkit/artifact per-frame
// endpoints. Play/pause toggles a setInterval that advances the
// current index; the scrub bar is a PanResponder that maps the
// touch x-position to a frame index.
function FrameSequencePlayer({ dir, onClose }: { dir: string; onClose: () => void }) {
  const c = useColors();
  const [list, setList] = useState<TestkitFrameList | null>(null);
  const [idx, setIdx] = useState(0);
  const [playing, setPlaying] = useState(true);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const barWidthRef = useRef(1);

  useEffect(() => {
    let alive = true;
    (async () => {
      const res = await quicClient.testkitFrames(dir);
      if (!alive) return;
      if (!res || !res.frames || res.frames.length === 0) {
        setLoadErr("no frames captured for this step");
        return;
      }
      setList(res);
      setIdx(0);
      setPlaying(true);
    })();
    return () => {
      alive = false;
    };
  }, [dir]);

  useEffect(() => {
    if (!list || !playing) return;
    const interval = Math.max(16, Math.round(1000 / (list.fps || 15)));
    const id = setInterval(() => {
      setIdx((prev) => (prev + 1) % list.frames.length);
    }, interval);
    return () => clearInterval(id);
  }, [list, playing]);

  const pan = useRef(
    PanResponder.create({
      onStartShouldSetPanResponder: () => true,
      onMoveShouldSetPanResponder: () => true,
      onPanResponderGrant: (_, g) => {
        setPlaying(false);
        if (list) {
          const pct = Math.max(0, Math.min(1, g.x0 / barWidthRef.current));
          setIdx(Math.floor(pct * (list.frames.length - 1)));
        }
      },
      onPanResponderMove: (_, g) => {
        if (!list) return;
        const pct = Math.max(0, Math.min(1, (g.moveX || g.x0) / barWidthRef.current));
        setIdx(Math.floor(pct * (list.frames.length - 1)));
      },
    }),
  ).current;

  return (
    <>
      <View style={{ flex: 1, justifyContent: "center", alignItems: "center" }}>
        {loadErr ? (
          <Text style={{ color: "#fff", padding: 24, textAlign: "center" }}>{loadErr}</Text>
        ) : list ? (
          <Image
            source={{
              uri: quicClient.testkitArtifactUrl(list.frames[idx]),
              headers: quicClient.testkitArtifactHeaders,
            }}
            style={{ width: "100%", height: "80%", resizeMode: "contain" }}
          />
        ) : (
          <ActivityIndicator color="#fff" />
        )}
      </View>
      {list && (
        <View style={{ paddingBottom: 40, paddingTop: 12, paddingHorizontal: 16 }}>
          <Text style={{ color: "#fff", textAlign: "center", marginBottom: 6, fontSize: 12 }}>
            Frame {idx + 1} / {list.frames.length} · {list.fps} fps
          </Text>
          <View
            onLayout={(e) => {
              barWidthRef.current = e.nativeEvent.layout.width || 1;
            }}
            {...pan.panHandlers}
            style={{
              height: 28,
              justifyContent: "center",
            }}
          >
            <View style={{ height: 4, backgroundColor: "rgba(255,255,255,0.15)", borderRadius: 2 }} />
            <View
              style={{
                position: "absolute",
                left: 0,
                height: 4,
                width: `${((idx + 1) / list.frames.length) * 100}%`,
                backgroundColor: c.accent || "#6366f1",
                borderRadius: 2,
              }}
            />
          </View>
          <View style={{ flexDirection: "row", justifyContent: "center", marginTop: 14 }}>
            <Pressable
              onPress={() => setPlaying((p) => !p)}
              style={{
                paddingHorizontal: 22,
                paddingVertical: 8,
                borderRadius: 999,
                backgroundColor: "#6366f1",
              }}
            >
              <Text style={{ color: "#fff", fontWeight: "700" }}>{playing ? "Pause" : "Play"}</Text>
            </Pressable>
          </View>
        </View>
      )}
      <Pressable onPress={onClose} style={{ position: "absolute", top: 50, right: 20, padding: 8 }}>
        <Text style={{ color: "#fff", fontSize: 18 }}>✕</Text>
      </Pressable>
    </>
  );
}

// ZoomableImage wraps an <Image> with pinch-to-zoom and drag-to-pan
// gestures implemented directly with PanResponder + Animated. Two
// pointers → scale; one pointer → pan. Keeps the dep surface small
// (no react-native-gesture-handler / reanimated needed), which is
// the same philosophy as the rest of the mobile app.
function ZoomableImage({
  uri,
  headers,
  heightPercent,
}: {
  uri: string;
  headers: Record<string, string>;
  heightPercent?: `${number}%`;
}) {
  const scale = useRef(new Animated.Value(1)).current;
  const translateX = useRef(new Animated.Value(0)).current;
  const translateY = useRef(new Animated.Value(0)).current;

  // Static baselines we reset to at the start of each gesture so
  // the animated value deltas stay relative to the last committed
  // transform instead of compounding across gestures.
  const baseScale = useRef(1);
  const basePanX = useRef(0);
  const basePanY = useRef(0);
  const initialPinchDistance = useRef<number | null>(null);

  const pan = useRef(
    PanResponder.create({
      onStartShouldSetPanResponder: () => true,
      onMoveShouldSetPanResponder: () => true,
      onPanResponderGrant: () => {
        initialPinchDistance.current = null;
      },
      onPanResponderMove: (evt, gestureState) => {
        const touches = evt.nativeEvent.touches;
        if (touches.length >= 2) {
          // Pinch: two pointers → measure distance and scale
          // relative to the first recorded distance.
          const [a, b] = touches;
          const dx = a.pageX - b.pageX;
          const dy = a.pageY - b.pageY;
          const dist = Math.sqrt(dx * dx + dy * dy);
          if (initialPinchDistance.current == null) {
            initialPinchDistance.current = dist;
            return;
          }
          const next = Math.max(
            1,
            Math.min(5, (baseScale.current * dist) / initialPinchDistance.current),
          );
          scale.setValue(next);
        } else {
          // Single-pointer pan — only active when we're zoomed in.
          if (baseScale.current <= 1.01) return;
          translateX.setValue(basePanX.current + gestureState.dx);
          translateY.setValue(basePanY.current + gestureState.dy);
        }
      },
      onPanResponderRelease: () => {
        // Commit the current transform as the next baseline.
        // @ts-expect-error — Animated.Value exposes _value at runtime
        baseScale.current = scale._value;
        // @ts-expect-error
        basePanX.current = translateX._value;
        // @ts-expect-error
        basePanY.current = translateY._value;
        if (baseScale.current <= 1.01) {
          // Snap back to centered 1x when the user zooms out
          // below the threshold.
          Animated.parallel([
            Animated.spring(scale, { toValue: 1, useNativeDriver: true }),
            Animated.spring(translateX, { toValue: 0, useNativeDriver: true }),
            Animated.spring(translateY, { toValue: 0, useNativeDriver: true }),
          ]).start();
          baseScale.current = 1;
          basePanX.current = 0;
          basePanY.current = 0;
        }
        initialPinchDistance.current = null;
      },
    }),
  ).current;

  return (
    <View
      style={{ width: "100%" as const, height: heightPercent ?? ("100%" as const), overflow: "hidden" }}
      {...pan.panHandlers}
    >
      <Animated.Image
        source={{ uri, headers }}
        style={{
          width: "100%",
          height: "100%",
          resizeMode: "contain",
          transform: [{ translateX }, { translateY }, { scale }],
        }}
      />
    </View>
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
