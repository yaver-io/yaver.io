// Box Monitor — htop for the box you're attached to, from your phone.
//
// A top-level route, NOT a tab: it's reached by tapping "All processes →" in a
// device's Activity fold, so it costs nothing on the main surfaces. Rides the
// active connection (LAN / Tailscale / tunnel / relay, whichever connect()
// negotiated), same as the Shell and Remote Desktop screens.
//
// Reads the structured table from /console/processes — typed rows, sorted
// server-side, so the phone pulls 60 rows instead of 800 to render 60.
//
// Kills are gated twice: the agent refuses to kill itself, its parent, or init
// (a kill of the agent from here would sever the very connection issuing it),
// and anything it WILL kill still needs an explicit destructive confirm.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";

type SortKey = "cpu" | "mem" | "pid" | "name";

interface HostMetrics {
  cpuPct: number;
  ramUsed: number;
  ramTotal: number;
  ramPct: number;
  diskUsed: number;
  diskTotal: number;
  diskPct: number;
  cores: number;
  hostname: string;
}

interface ProcRow {
  pid: number;
  ppid: number;
  name: string;
  cmd?: string;
  user?: string;
  cpuPct: number;
  rssMb: number;
  memPct: number;
  status?: string;
  protected?: boolean;
}

const POLL_MS = 3000;
const ROW_LIMIT = 60;

function pctColor(pct: number): string {
  if (pct >= 90) return "#f43f5e";
  if (pct >= 70) return "#fbbf24";
  return "#34d399";
}

export default function BoxMonitorScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { activeDevice, connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [metrics, setMetrics] = useState<HostMetrics | null>(null);
  const [procs, setProcs] = useState<ProcRow[]>([]);
  const [count, setCount] = useState(0);
  const [sortBy, setSortBy] = useState<SortKey>("cpu");
  const [error, setError] = useState<string | null>(null);
  const [unsupported, setUnsupported] = useState(false);
  const [loading, setLoading] = useState(true);
  const [paused, setPaused] = useState(false);

  // Keep the sort key in a ref so the poll interval doesn't tear down and
  // rebuild every time the user changes sort.
  const sortRef = useRef(sortBy);
  sortRef.current = sortBy;
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  const poll = useCallback(async () => {
    if (!connected) return;
    try {
      const [mRes, pRes] = await Promise.all([
        fetch(`${quicClient.baseUrl}/console/metrics`, { headers: quicClient.getAuthHeaders() }),
        fetch(
          `${quicClient.baseUrl}/console/processes?sort=${sortRef.current}&limit=${ROW_LIMIT}`,
          { headers: quicClient.getAuthHeaders() }
        ),
      ]);
      if (pRes.status === 404) {
        setUnsupported(true);
        return;
      }
      if (mRes.ok) setMetrics(await mRes.json());
      if (pRes.ok) {
        const json = await pRes.json();
        setProcs(json.table?.processes ?? []);
        setCount(json.table?.count ?? 0);
      }
      setError(null);
    } catch (e: any) {
      setError(e?.message || "failed to read the box");
    } finally {
      setLoading(false);
    }
  }, [connected]);

  useEffect(() => {
    poll();
    const id = setInterval(() => {
      if (!pausedRef.current) poll();
    }, POLL_MS);
    return () => clearInterval(id);
  }, [poll]);

  // Re-poll immediately on a sort change rather than waiting out the interval.
  useEffect(() => {
    poll();
  }, [sortBy, poll]);

  const confirmKill = (p: ProcRow) => {
    if (p.protected) {
      Alert.alert(
        "Protected process",
        `${p.name} (pid ${p.pid}) is the Yaver agent, its parent, or init. Killing it from here would cut the connection you're using to kill it.`
      );
      return;
    }
    Alert.alert(
      `Kill ${p.name}?`,
      `pid ${p.pid}${p.user ? ` · ${p.user}` : ""}\n${p.cmd || ""}\n\nThe process is sent SIGTERM. Unsaved work in it is lost.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Kill",
          style: "destructive",
          onPress: async () => {
            try {
              const res = await fetch(`${quicClient.baseUrl}/console/processes/kill`, {
                method: "POST",
                headers: {
                  ...quicClient.getAuthHeaders(),
                  "Content-Type": "application/json",
                },
                body: JSON.stringify({ pid: p.pid }),
              });
              if (!res.ok) {
                const body = await res.json().catch(() => ({}));
                throw new Error(body?.error || `kill → ${res.status}`);
              }
              await poll();
            } catch (e: any) {
              setError(e?.message || "kill failed");
            }
          },
        },
      ]
    );
  };

  if (!activeDevice || !connected) {
    return (
      <View style={[styles.center, { backgroundColor: c.bg, paddingTop: insets.top }]}>
        <AppBackButton onPress={() => router.back()} />
        <Text style={{ color: c.textMuted, fontSize: 13 }}>
          Connect to a device to see its activity.
        </Text>
      </View>
    );
  }

  return (
    <View style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top }}>
      <View style={styles.headerRow}>
        <AppBackButton onPress={() => router.back()} />
        <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700" }} numberOfLines={1}>
          {metrics?.hostname || activeDevice.name}
        </Text>
        <Pressable onPress={() => setPaused((v) => !v)} hitSlop={8}>
          <Text style={{ color: paused ? "#fbbf24" : c.textMuted, fontSize: 12, fontWeight: "600" }}>
            {paused ? "▶ Resume" : "❚❚ Pause"}
          </Text>
        </Pressable>
      </View>

      {unsupported ? (
        <View style={styles.center}>
          <Text style={{ color: c.textMuted, fontSize: 12, textAlign: "center", paddingHorizontal: 32 }}>
            This agent is too old for the activity monitor. Update it to enable this screen.
          </Text>
        </View>
      ) : loading ? (
        <View style={styles.center}>
          <ActivityIndicator />
        </View>
      ) : (
        <>
          {metrics && (
            <View style={[styles.metricsBar, { borderColor: c.border, backgroundColor: c.bgCard }]}>
              {[
                { label: "CPU", pct: metrics.cpuPct, sub: `${metrics.cores} cores` },
                {
                  label: "RAM",
                  pct: metrics.ramPct,
                  sub: `${(metrics.ramUsed / 1e9).toFixed(1)}/${(metrics.ramTotal / 1e9).toFixed(0)} GB`,
                },
                {
                  label: "DISK",
                  pct: metrics.diskPct,
                  sub: `${((metrics.diskTotal - metrics.diskUsed) / 1e9).toFixed(0)} GB free`,
                },
              ].map((m) => (
                <View key={m.label} style={{ flex: 1, alignItems: "center" }}>
                  <Text style={{ color: c.textMuted, fontSize: 9, fontWeight: "700" }}>{m.label}</Text>
                  <Text style={{ color: pctColor(m.pct), fontSize: 18, fontWeight: "700" }}>
                    {m.pct.toFixed(0)}%
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 9 }}>{m.sub}</Text>
                </View>
              ))}
            </View>
          )}

          <View style={styles.sortRow}>
            {(["cpu", "mem", "pid", "name"] as SortKey[]).map((k) => (
              <Pressable
                key={k}
                onPress={() => setSortBy(k)}
                style={[
                  styles.sortChip,
                  {
                    borderColor: sortBy === k ? "#60a5fa" : c.border,
                    backgroundColor: sortBy === k ? "#3b82f622" : "transparent",
                  },
                ]}
              >
                <Text
                  style={{
                    color: sortBy === k ? "#60a5fa" : c.textMuted,
                    fontSize: 11,
                    fontWeight: "600",
                  }}
                >
                  {k.toUpperCase()}
                </Text>
              </Pressable>
            ))}
            <Text style={{ color: c.textMuted, fontSize: 10, marginLeft: "auto" }}>
              {procs.length} of {count}
            </Text>
          </View>

          {error && (
            <Text style={{ color: "#fbbf24", fontSize: 11, paddingHorizontal: 16, paddingBottom: 4 }}>
              {error}
            </Text>
          )}

          <ScrollView contentContainerStyle={{ paddingBottom: insets.bottom + 24 }}>
            {procs.map((p) => (
              <Pressable
                key={p.pid}
                onLongPress={() => confirmKill(p)}
                style={[styles.procRow, { borderColor: c.border }]}
              >
                <Text style={{ color: c.textMuted, fontSize: 10, width: 52 }}>{p.pid}</Text>
                <View style={{ flex: 1, paddingRight: 6 }}>
                  <Text style={{ color: c.textPrimary, fontSize: 12 }} numberOfLines={1}>
                    {p.name}
                    {p.protected ? " 🔒" : ""}
                  </Text>
                  {p.cmd ? (
                    <Text style={{ color: c.textMuted, fontSize: 9 }} numberOfLines={1}>
                      {p.cmd}
                    </Text>
                  ) : null}
                </View>
                <Text
                  style={{
                    color: pctColor(p.cpuPct),
                    fontSize: 12,
                    width: 50,
                    textAlign: "right",
                    fontWeight: "600",
                  }}
                >
                  {p.cpuPct.toFixed(0)}%
                </Text>
                <Text style={{ color: c.textSecondary, fontSize: 11, width: 66, textAlign: "right" }}>
                  {p.rssMb.toFixed(0)} MB
                </Text>
              </Pressable>
            ))}
            <Text style={{ color: c.textMuted, fontSize: 10, textAlign: "center", marginTop: 12 }}>
              Long-press a process to kill it.
            </Text>
          </ScrollView>
        </>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  center: { flex: 1, alignItems: "center", justifyContent: "center" },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 10,
    gap: 12,
  },
  metricsBar: {
    flexDirection: "row",
    borderWidth: 1,
    borderRadius: 10,
    marginHorizontal: 16,
    paddingVertical: 10,
  },
  sortRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    paddingHorizontal: 16,
    paddingVertical: 10,
  },
  sortChip: { borderWidth: 1, borderRadius: 6, paddingHorizontal: 10, paddingVertical: 4 },
  procRow: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 16,
    paddingVertical: 7,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
});
