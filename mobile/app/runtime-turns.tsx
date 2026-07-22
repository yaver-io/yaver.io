// Runtime Turns — the phone-side view of everything spoken into a watch, car,
// TV, or headset. Those surfaces can only ack in one sentence; this is where
// the detail lands and where a captured idea becomes real work.
//
// Two things this screen refuses to lie about:
//
//   1. A `captured` item has NOT started. It shows a Run button, because
//      capture used to be a black hole — the watch said "I'll attach it to the
//      current app" and nothing ever moved the item again.
//   2. `ready_to_test` means the CODE finished, not that anything reloaded on a
//      device. Test on device attempts the real reload and reports the live
//      listener count back, so "nothing was listening" reads as a failure
//      instead of a green check.
import React, { useCallback, useEffect, useMemo, useState } from "react";
import { ActivityIndicator, Platform, Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { connectionManager } from "../src/lib/connectionManager";
import { openTaskBus } from "../src/lib/runningTasksBus";
import { runtimeSurfaceClient } from "../src/lib/runtimeSurfaceClient";
import type { RuntimeTurnQueueItem, RuntimeTurnState } from "../src/lib/runtimeSurfaceTypes";

const ACCENT = "#4f9cf9";
const POLL_MS = 5000;

function pickDeviceId(devices: any[], activeDevice: any | null): string {
  const focused = connectionManager.focusedDeviceId();
  if (focused) return focused;
  const activeId = activeDevice?.id || activeDevice?.deviceId;
  if (activeId) return activeId;
  const connected = connectionManager.connectedDeviceIds()[0];
  if (connected) return connected;
  const online = devices.find((d) => d?.online);
  return online?.id || online?.deviceId || devices[0]?.id || devices[0]?.deviceId || "";
}

const STATE_LABEL: Record<string, string> = {
  captured: "Captured",
  queued: "Queued",
  running: "Working",
  needs_input: "Needs you",
  ready_to_test: "Code done",
  ready_to_deploy: "Ready to ship",
  done: "Done",
  failed: "Failed",
  cancelled: "Cancelled",
};

function stateColor(state: RuntimeTurnState, c: any): string {
  switch (state) {
    case "failed":
      return "#e5534b";
    case "needs_input":
      return "#d29922";
    case "ready_to_test":
    case "ready_to_deploy":
    case "done":
      return "#3fb950";
    case "running":
    case "queued":
      return ACCENT;
    default:
      return c.textSecondary;
  }
}

function isTerminal(state?: RuntimeTurnState): boolean {
  return state === "done" || state === "failed" || state === "cancelled";
}

function relativeTime(iso?: string): string {
  if (!iso) return "";
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "";
  const secs = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (secs < 60) return `${secs}s ago`;
  if (secs < 3600) return `${Math.round(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.round(secs / 3600)}h ago`;
  return `${Math.round(secs / 86400)}d ago`;
}

/**
 * The honest one-liner about whether this is testable yet.
 *
 * Note the ladder: `delivered` means a phone ACCEPTED the reload command;
 * only `verified` means the device reported the bundle actually loaded. They
 * are not the same claim and this screen doesn't blur them.
 */
function testLine(item: RuntimeTurnQueueItem): { text: string; bad: boolean } | null {
  if (item.state !== "ready_to_test" && item.state !== "ready_to_deploy") return null;
  const tt = item.testTarget;
  if (!tt || tt.state === "unverified") {
    return { text: "Not on a device yet", bad: false };
  }
  if (tt.state === "verified") {
    return { text: "Running on your device", bad: false };
  }
  if (tt.state === "delivered") {
    const n = tt.listeners ?? 0;
    return { text: `Sent to ${n} device${n === 1 ? "" : "s"} — waiting for it to load`, bad: false };
  }
  if (tt.state === "unreachable") {
    return { text: tt.detail || "Nothing was listening", bad: true };
  }
  return { text: tt.detail || String(tt.state), bad: true };
}

export default function RuntimeTurnsScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = ((deviceCtx as any).devices as any[]) || [];
  const activeDevice = (deviceCtx as any).activeDevice || null;

  const deviceId = useMemo(() => pickDeviceId(devices, activeDevice), [devices, activeDevice]);

  const [items, setItems] = useState<RuntimeTurnQueueItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [shipPlan, setShipPlan] = useState<{ turnId: string; command: string; note: string } | null>(null);

  const load = useCallback(async () => {
    if (!deviceId) {
      setError("No Yaver device selected");
      setLoading(false);
      return;
    }
    try {
      const res = await runtimeSurfaceClient.runtimeTurns(deviceId, 25);
      setItems(res.items || []);
      setError(null);
    } catch (e: any) {
      setError(e?.message || "Could not reach the box");
    } finally {
      setLoading(false);
    }
  }, [deviceId]);

  useEffect(() => {
    void load();
  }, [load]);

  // Poll only while something is still moving. A queue of finished work does
  // not need a timer, and the agent skips refreshing terminal items anyway.
  const hasLiveWork = items.some((i) => !isTerminal(i.state) && i.state !== "captured");
  useEffect(() => {
    if (!hasLiveWork) return;
    const t = setInterval(() => void load(), POLL_MS);
    return () => clearInterval(t);
  }, [hasLiveWork, load]);

  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    await load();
    setRefreshing(false);
  }, [load]);

  const runItem = useCallback(
    async (item: RuntimeTurnQueueItem) => {
      setBusyId(item.itemId);
      setError(null);
      try {
        await runtimeSurfaceClient.runtimeTurnRun(deviceId, item.itemId);
        await load();
      } catch (e: any) {
        setError(e?.message || "Could not start that");
      } finally {
        setBusyId(null);
      }
    },
    [deviceId, load],
  );

  // Preflight only. Yaver does not deploy for you — it reports whether the
  // turn is shippable and hands back the command to run. A tap on a phone is
  // not consent to burn one of ~15 daily TestFlight slots that cannot be
  // rolled back.
  const preflight = useCallback(
    async (item: RuntimeTurnQueueItem) => {
      setBusyId(item.itemId);
      setError(null);
      setShipPlan(null);
      try {
        const res = await runtimeSurfaceClient.runtimeTurnDeployPreflight(deviceId, item.itemId);
        if (res.ready && res.command) {
          setShipPlan({ turnId: item.itemId, command: res.command, note: res.note });
        } else {
          setError(`Not ready to ship: ${(res.blockers || ["unknown reason"]).join("; ")}`);
        }
        await load();
      } catch (e: any) {
        setError(e?.message || "Preflight failed");
      } finally {
        setBusyId(null);
      }
    },
    [deviceId, load],
  );

  // The Tasks tab owns task detail; it subscribes to this bus and opens its
  // chat-detail modal. There is no standalone task-detail route.
  const openTask = useCallback(
    (taskId: string) => {
      openTaskBus.publish(taskId);
      router.push("/(tabs)/tasks" as any);
    },
    [router],
  );

  const verifyItem = useCallback(
    async (item: RuntimeTurnQueueItem) => {
      setBusyId(item.itemId);
      setError(null);
      try {
        const res = await runtimeSurfaceClient.runtimeTurnVerify(deviceId, item.itemId);
        // A zero-listener result comes back as a non-OK response on purpose.
        // Surface it instead of quietly showing a success state.
        if (!res.ok) setError(res.testTarget?.detail || res.error || "Nothing was listening");
        await load();
      } catch (e: any) {
        setError(e?.message || "Could not reach a device");
      } finally {
        setBusyId(null);
      }
    },
    [deviceId, load],
  );

  return (
    <View style={[styles.root, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="Runtime Turns" onBack={() => router.back()} />

      {error ? (
        <View style={[styles.banner, { backgroundColor: "#e5534b22", borderColor: "#e5534b" }]}>
          <Text style={[styles.bannerText, { color: c.textPrimary }]}>{error}</Text>
        </View>
      ) : null}

      {shipPlan ? (
        <View style={[styles.banner, { backgroundColor: "#3fb95022", borderColor: "#3fb950" }]}>
          <Text style={[styles.bannerText, { color: c.textPrimary, fontWeight: "700" }]}>
            Ready to ship — run this yourself:
          </Text>
          <Text selectable style={[styles.command, { color: c.textPrimary }]}>
            {shipPlan.command}
          </Text>
          <Text style={[styles.bannerText, { color: c.textSecondary }]}>{shipPlan.note}</Text>
          <Pressable onPress={() => setShipPlan(null)} style={{ paddingTop: 6 }}>
            <Text style={[styles.btnText, { color: ACCENT }]}>Dismiss</Text>
          </Pressable>
        </View>
      ) : null}

      {loading ? (
        <View style={styles.center}>
          <ActivityIndicator color={ACCENT} />
        </View>
      ) : items.length === 0 ? (
        <View style={styles.center}>
          <Text style={[styles.empty, { color: c.textSecondary }]}>
            Nothing yet. Speak an idea into your watch, car, or TV and it shows up here.
          </Text>
        </View>
      ) : (
        <ScrollView
          contentContainerStyle={styles.list}
          refreshControl={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={ACCENT} />}
        >
          {items.map((item) => {
            const busy = busyId === item.itemId;
            const test = testLine(item);
            return (
              <View key={item.itemId} style={[styles.card, { backgroundColor: c.card, borderColor: c.border }]}>
                <View style={styles.cardTop}>
                  <Text style={[styles.state, { color: stateColor(item.state, c) }]}>
                    {STATE_LABEL[item.state] || item.state}
                  </Text>
                  <Text style={[styles.time, { color: c.textSecondary }]}>{relativeTime(item.updatedAt)}</Text>
                </View>

                <Text style={[styles.utterance, { color: c.textPrimary }]}>{item.utterance}</Text>

                {item.surface?.class ? (
                  <Text style={[styles.meta, { color: c.textSecondary }]}>from {item.surface.class}</Text>
                ) : null}

                {item.spoken ? (
                  <Text style={[styles.spoken, { color: c.textSecondary }]}>{item.spoken}</Text>
                ) : null}

                {test ? (
                  <Text style={[styles.test, { color: test.bad ? "#e5534b" : c.textSecondary }]}>{test.text}</Text>
                ) : null}

                {item.error ? (
                  <Text style={[styles.error, { color: "#e5534b" }]} numberOfLines={4}>
                    {item.error}
                  </Text>
                ) : null}

                <View style={styles.actions}>
                  {item.state === "captured" ? (
                    <Pressable
                      disabled={busy}
                      onPress={() => void runItem(item)}
                      style={[styles.btn, { borderColor: ACCENT, opacity: busy ? 0.5 : 1 }]}
                    >
                      {busy ? (
                        <ActivityIndicator color={ACCENT} size="small" />
                      ) : (
                        <Text style={[styles.btnText, { color: ACCENT }]}>Run it</Text>
                      )}
                    </Pressable>
                  ) : null}

                  {item.state === "ready_to_test" ? (
                    <Pressable
                      disabled={busy}
                      onPress={() => void verifyItem(item)}
                      style={[styles.btn, { borderColor: ACCENT, opacity: busy ? 0.5 : 1 }]}
                    >
                      {busy ? (
                        <ActivityIndicator color={ACCENT} size="small" />
                      ) : (
                        <Text style={[styles.btnText, { color: ACCENT }]}>Test on device</Text>
                      )}
                    </Pressable>
                  ) : null}

                  {item.testTarget?.state === "verified" ||
                  item.state === "ready_to_deploy" ? (
                    <Pressable
                      disabled={busy}
                      onPress={() => void preflight(item)}
                      style={[styles.btn, { borderColor: "#3fb950", opacity: busy ? 0.5 : 1 }]}
                    >
                      <Text style={[styles.btnText, { color: "#3fb950" }]}>Ship it?</Text>
                    </Pressable>
                  ) : null}

                  {item.taskId ? (
                    <Pressable
                      onPress={() => openTask(item.taskId as string)}
                      style={[
                        styles.btn,
                        { borderColor: item.state === "needs_input" ? "#d29922" : c.border },
                      ]}
                    >
                      <Text
                        style={[
                          styles.btnText,
                          { color: item.state === "needs_input" ? "#d29922" : c.textSecondary },
                        ]}
                      >
                        {item.state === "needs_input" ? "Answer" : "Details"}
                      </Text>
                    </Pressable>
                  ) : null}
                </View>
              </View>
            );
          })}
        </ScrollView>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  center: { flex: 1, alignItems: "center", justifyContent: "center", padding: 32 },
  empty: { fontSize: 15, textAlign: "center", lineHeight: 22 },
  list: { padding: 16, gap: 12 },
  banner: { marginHorizontal: 16, marginTop: 8, padding: 10, borderRadius: 8, borderWidth: 1 },
  bannerText: { fontSize: 13 },
  command: {
    fontSize: 13,
    fontFamily: Platform.select({ ios: "Menlo", android: "monospace", default: "monospace" }),
    paddingVertical: 6,
  },
  card: { borderRadius: 12, borderWidth: 1, padding: 14, gap: 6 },
  cardTop: { flexDirection: "row", justifyContent: "space-between", alignItems: "center" },
  state: { fontSize: 12, fontWeight: "700", textTransform: "uppercase", letterSpacing: 0.5 },
  time: { fontSize: 12 },
  utterance: { fontSize: 16, fontWeight: "600", lineHeight: 22 },
  meta: { fontSize: 12 },
  spoken: { fontSize: 13, lineHeight: 18 },
  test: { fontSize: 13, fontWeight: "600" },
  error: { fontSize: 12, lineHeight: 16 },
  actions: { flexDirection: "row", gap: 8, marginTop: 6, flexWrap: "wrap" },
  btn: { paddingVertical: 8, paddingHorizontal: 14, borderRadius: 8, borderWidth: 1, minWidth: 76, alignItems: "center" },
  btnText: { fontSize: 13, fontWeight: "600" },
});
