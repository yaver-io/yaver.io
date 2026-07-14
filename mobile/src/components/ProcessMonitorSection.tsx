// ProcessMonitorSection — live CPU/RAM/disk vitals for one box, plus the top
// few processes. The compact half of "htop on my phone"; the sortable, killable
// table lives in app/box-monitor.tsx, one tap away.
//
// Same containment rule as StorageSection: its own file, one render line in
// DeviceDetailsModal, nothing on the main tabs.
//
// Polls only while OPEN. A device-details modal left on screen must not sit
// there hammering a remote box over the relay forever.

import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { router } from "expo-router";
import { useColors } from "../context/ThemeContext";
import { useDevice, type Device } from "../context/DeviceContext";
import { quicClient } from "../lib/quic";

interface HostMetrics {
  cpuPct: number;
  ramUsed: number;
  ramTotal: number;
  ramPct: number;
  diskUsed: number;
  diskTotal: number;
  diskPct: number;
  cores: number;
  uptime: number;
}
interface ProcRow {
  pid: number;
  name: string;
  cpuPct: number;
  rssMb: number;
}

const POLL_MS = 3000;

function pctColor(pct: number): string {
  if (pct >= 90) return "#f43f5e";
  if (pct >= 70) return "#fbbf24";
  return "#34d399";
}

function Gauge({ label, pct, detail }: { label: string; pct: number; detail: string }) {
  const c = useColors();
  return (
    <View style={{ flex: 1 }}>
      <View style={styles.row}>
        <Text style={{ color: c.textSecondary, fontSize: 10, fontWeight: "700" }}>{label}</Text>
        <Text style={{ color: pctColor(pct), fontSize: 10, fontWeight: "700" }}>
          {pct.toFixed(0)}%
        </Text>
      </View>
      <View style={[styles.barTrack, { backgroundColor: c.border }]}>
        <View
          style={{
            width: `${Math.min(100, Math.max(0, pct))}%`,
            height: 5,
            borderRadius: 3,
            backgroundColor: pctColor(pct),
          }}
        />
      </View>
      <Text style={{ color: c.textMuted, fontSize: 9, marginTop: 2 }}>{detail}</Text>
    </View>
  );
}

export function ProcessMonitorSection({ device }: { device: Device }) {
  const c = useColors();
  const { activeDevice, connectionStatus, selectDevice } = useDevice();
  const isActive = Boolean(
    activeDevice && activeDevice.id === device.id && connectionStatus === "connected"
  );
  const target = isActive ? undefined : device.id;
  const base = `${quicClient.baseUrl}${target ? `/peer/${target}` : ""}`;

  const [open, setOpen] = useState(false);
  const [metrics, setMetrics] = useState<HostMetrics | null>(null);
  const [procs, setProcs] = useState<ProcRow[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [unsupported, setUnsupported] = useState(false);
  const [loading, setLoading] = useState(false);
  const openRef = useRef(open);
  openRef.current = open;

  const poll = useCallback(async () => {
    try {
      const [mRes, pRes] = await Promise.all([
        fetch(`${base}/console/metrics`, { headers: quicClient.getAuthHeaders() }),
        fetch(`${base}/console/processes?sort=cpu&limit=5`, { headers: quicClient.getAuthHeaders() }),
      ]);
      if (pRes.status === 404) {
        setUnsupported(true);
        return;
      }
      if (mRes.ok) setMetrics(await mRes.json());
      if (pRes.ok) {
        const json = await pRes.json();
        setProcs(json.table?.processes ?? []);
      }
      setError(null);
    } catch (e: any) {
      setError(e?.message || "failed to read vitals");
    }
  }, [base]);

  // Poll only while open — see the file header.
  useEffect(() => {
    if (!open || unsupported) return;
    let cancelled = false;
    setLoading(true);
    poll().finally(() => !cancelled && setLoading(false));
    const id = setInterval(() => {
      if (openRef.current) poll();
    }, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [open, unsupported, poll]);

  const openFullMonitor = () => {
    // The full monitor rides the active connection, so make this device active
    // first (same idiom the Shell / Remote Desktop rows use).
    if (!isActive) selectDevice(device).catch(() => {});
    setTimeout(() => router.push("/box-monitor"), 200);
  };

  return (
    <View style={{ marginTop: 14 }}>
      <Pressable onPress={() => setOpen((v) => !v)} style={styles.header}>
        <Text style={[styles.title, { color: c.textPrimary }]}>📊 Activity</Text>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          {metrics && (
            <Text style={{ color: pctColor(metrics.cpuPct), fontSize: 11, fontWeight: "700" }}>
              {metrics.cpuPct.toFixed(0)}% CPU
            </Text>
          )}
          <Text style={{ color: c.textMuted }}>{open ? "▾" : "▸"}</Text>
        </View>
      </Pressable>

      {open && (
        <View style={[styles.body, { borderColor: c.border, backgroundColor: c.bgCard }]}>
          {unsupported ? (
            <Text style={{ color: c.textMuted, fontSize: 11 }}>
              This agent is too old for the activity monitor — update it to enable this panel.
            </Text>
          ) : loading && !metrics ? (
            <ActivityIndicator size="small" />
          ) : (
            <>
              {metrics && (
                <View style={{ flexDirection: "row", gap: 12, marginBottom: 10 }}>
                  <Gauge
                    label="CPU"
                    pct={metrics.cpuPct}
                    detail={`${metrics.cores} cores`}
                  />
                  <Gauge
                    label="RAM"
                    pct={metrics.ramPct}
                    detail={`${(metrics.ramUsed / 1e9).toFixed(1)} / ${(metrics.ramTotal / 1e9).toFixed(0)} GB`}
                  />
                  <Gauge
                    label="DISK"
                    pct={metrics.diskPct}
                    detail={`${((metrics.diskTotal - metrics.diskUsed) / 1e9).toFixed(0)} GB free`}
                  />
                </View>
              )}

              {procs.map((p) => (
                <View key={p.pid} style={styles.procRow}>
                  <Text style={{ color: c.textSecondary, fontSize: 11, flex: 1 }} numberOfLines={1}>
                    {p.name}
                  </Text>
                  <Text style={{ color: pctColor(p.cpuPct), fontSize: 11, width: 52, textAlign: "right" }}>
                    {p.cpuPct.toFixed(0)}%
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 11, width: 68, textAlign: "right" }}>
                    {p.rssMb.toFixed(0)} MB
                  </Text>
                </View>
              ))}

              <Pressable
                onPress={openFullMonitor}
                style={[styles.btn, { backgroundColor: "#3b82f622", borderColor: "#3b82f655" }]}
              >
                <Text style={{ color: "#60a5fa", fontWeight: "600", fontSize: 12 }}>
                  All processes →
                </Text>
              </Pressable>

              {error && <Text style={{ color: "#fbbf24", fontSize: 11, marginTop: 6 }}>{error}</Text>}
            </>
          )}
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingVertical: 6,
  },
  title: { fontSize: 13, fontWeight: "700" },
  body: { borderWidth: 1, borderRadius: 8, padding: 12, marginTop: 4 },
  row: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  barTrack: { height: 5, borderRadius: 3, marginTop: 3, overflow: "hidden" },
  procRow: { flexDirection: "row", alignItems: "center", paddingVertical: 3 },
  btn: {
    borderWidth: 1,
    borderRadius: 6,
    paddingVertical: 8,
    alignItems: "center",
    marginTop: 8,
  },
});
