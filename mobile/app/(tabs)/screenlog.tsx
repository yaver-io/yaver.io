// Screen Monitor tab — drive the screenlog "screen as a stream of images"
// black box on the connected device (e.g. a family member's WSL box you
// manage) from your phone. Lists local sessions, shows the deterministic
// activity report ("what did it spend time on"), a frame grid (loaded with
// auth headers straight off the device's disk over the relay), and the
// owner consent policy + remote start/stop.
//
// Privacy: frames live only on the recorded device and stream through the
// encrypted relay tunnel; nothing touches Convex. Remote start is gated by
// the device's ScreenlogPolicy and notifies the owner.
//
// Added as a parallel screen (reached from More) to keep navigation edits
// minimal.

import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Image,
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
import { quicClient } from "../../src/lib/quic";
import { spacing } from "../../src/theme/tokens";

interface Session {
  id: string;
  title?: string;
  host?: string;
  startedAt: number;
  stoppedAt?: number;
  frames: number;
}
interface Frame {
  idx: number;
  capturedAt: number;
  file: string;
  activeApp?: string;
  activeWindow?: string;
}
interface CategoryStat { name: string; seconds: number; percent: number }
interface Report { subject: string; activeSec: number; idleSec: number; byCategory: CategoryStat[] }
interface Policy { enabled: boolean; allowRemoteControl: boolean; requireMeshGrant: boolean; notifyOnStart: boolean }

function fmtDur(sec: number): string {
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.round(sec / 60)}m`;
  return `${Math.floor(sec / 3600)}h${Math.round((sec % 3600) / 60)}m`;
}

async function agentGet(path: string): Promise<any> {
  const res = await fetch(`${quicClient.baseUrl}${path}`, { headers: quicClient.getAuthHeaders() });
  if (!res.ok) throw new Error(`${path} → ${res.status}`);
  return res.json();
}
async function agentPost(path: string, body: any): Promise<any> {
  const res = await fetch(`${quicClient.baseUrl}${path}`, {
    method: "POST",
    headers: { ...quicClient.getAuthHeaders(), "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${path} → ${res.status}`);
  return res.json();
}

export default function ScreenlogScreen() {
  const c = useColors();
  const { connectionStatus, activeDevice } = useDevice();
  const connected = connectionStatus === "connected" && !!activeDevice;

  const [sessions, setSessions] = useState<Session[]>([]);
  const [status, setStatus] = useState<any>(null);
  const [drivers, setDrivers] = useState<any>(null);
  const [policy, setPolicy] = useState<Policy | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [report, setReport] = useState<Report | null>(null);
  const [frames, setFrames] = useState<Frame[]>([]);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    if (!connected) return;
    setRefreshing(true);
    try {
      const [list, st, drv, pol] = await Promise.all([
        agentGet("/screenlog/list"),
        agentGet("/screenlog/status"),
        agentGet("/screenlog/drivers"),
        agentGet("/screenlog/policy"),
      ]);
      setSessions(list.sessions || []);
      setStatus(st.status || null);
      setDrivers(drv.drivers || null);
      setPolicy(pol.policy || null);
      setError(null);
    } catch (e: any) {
      setError("Couldn't reach the device agent.");
    } finally {
      setRefreshing(false);
    }
  }, [connected]);

  useEffect(() => { refresh(); }, [refresh]);

  const openSession = async (id: string) => {
    setSelected(id);
    setReport(null);
    setFrames([]);
    try {
      const [r, f] = await Promise.all([
        agentGet(`/screenlog/analyze?id=${encodeURIComponent(id)}`),
        agentGet(`/screenlog/${id}/frames.json`),
      ]);
      setReport(r.report);
      const all: Frame[] = f.session?.frames || [];
      // Sample up to 9 evenly-spaced frames for the grid.
      const n = Math.min(9, all.length);
      const out: Frame[] = [];
      const step = n > 1 ? (all.length - 1) / (n - 1) : 0;
      for (let i = 0; i < n; i++) out.push(all[Math.round(i * step)]);
      setFrames(out);
    } catch (e: any) {
      setError(e.message);
    }
  };

  const start = async () => {
    setBusy(true);
    try { await agentPost("/screenlog/start", { config: { displays: "all" } }); await refresh(); }
    catch (e: any) { setError(e.message); }
    finally { setBusy(false); }
  };
  const stop = async () => {
    setBusy(true);
    try { await agentPost("/screenlog/stop", {}); await refresh(); }
    catch (e: any) { setError(e.message); }
    finally { setBusy(false); }
  };
  const setPol = async (patch: Partial<Policy>) => {
    try { const r = await agentPost("/screenlog/policy", patch); setPolicy(r.policy); }
    catch (e: any) { setError(e.message); }
  };

  if (!connected) {
    return (
      <SafeAreaView style={[styles.root, { backgroundColor: c.bg }]} edges={["bottom"]}>
        <View style={styles.center}><Text style={{ color: c.textSecondary }}>Connect to a device to monitor its screen.</Text></View>
      </SafeAreaView>
    );
  }

  const running = status?.running;

  return (
    <SafeAreaView style={[styles.root, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <ScrollView
        contentContainerStyle={{ padding: spacing.md, gap: spacing.md }}
        refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} tintColor={c.textSecondary} />}
      >
        {/* Status + controls */}
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.cardTitle, { color: c.textPrimary }]}>{activeDevice?.name || "Device"}</Text>
          <Text style={{ color: c.textSecondary, fontSize: 12, marginTop: 2 }}>
            {drivers?.driver || "?"}{drivers?.wsl ? " · WSL" : ""}{drivers?.displays ? ` · ${drivers.displays} display(s)` : ""}
          </Text>
          {running && (
            <Text style={{ color: c.textSecondary, fontSize: 12, marginTop: 6 }}>
              ● recording · {status.keptFrames} frames · {Math.round((status.bytes || 0) / 1048576)} MB · {fmtDur(status.elapsedSec || 0)}
            </Text>
          )}
          <Pressable
            onPress={running ? stop : start}
            disabled={busy || drivers?.available === false}
            style={[styles.btn, { backgroundColor: running ? "#f43f5e22" : "#10b98122", borderColor: running ? "#f43f5e55" : "#10b98155", opacity: busy ? 0.5 : 1 }]}
          >
            {busy ? <ActivityIndicator size="small" /> : (
              <Text style={{ color: running ? "#fb7185" : "#34d399", fontWeight: "600", fontSize: 13 }}>
                {running ? "Stop recording" : "Start recording"}
              </Text>
            )}
          </Pressable>
          {drivers?.available === false && <Text style={{ color: "#fbbf24", fontSize: 12, marginTop: 6 }}>{drivers?.error}</Text>}
        </View>

        {error && <Text style={{ color: "#fbbf24", fontSize: 12 }}>{error}</Text>}

        {/* Sessions */}
        <Text style={[styles.section, { color: c.textSecondary }]}>Sessions</Text>
        {sessions.length === 0 ? (
          <Text style={{ color: c.textSecondary, fontSize: 12 }}>No recordings yet.</Text>
        ) : sessions.map((s) => (
          <Pressable key={s.id} onPress={() => openSession(s.id)}
            style={[styles.row, { backgroundColor: c.bgCard, borderColor: selected === s.id ? "#10b98155" : c.border }]}>
            <View style={{ flex: 1 }}>
              <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "500" }}>{s.title || s.id}</Text>
              <Text style={{ color: c.textSecondary, fontSize: 11, marginTop: 2 }}>
                {s.host} · {new Date(s.startedAt).toLocaleString()}
              </Text>
            </View>
            <Text style={{ color: c.textSecondary, fontSize: 11 }}>{s.frames} frames</Text>
          </Pressable>
        ))}

        {/* Selected report + frames */}
        {selected && (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary }]}>What it spent time on</Text>
            {!report ? <ActivityIndicator size="small" style={{ marginTop: 8 }} /> : (
              <>
                <Text style={{ color: c.textSecondary, fontSize: 11, marginVertical: 6 }}>
                  Active {fmtDur(report.activeSec)} · idle {fmtDur(report.idleSec)}
                </Text>
                {report.byCategory.slice(0, 6).map((cat) => (
                  <View key={cat.name} style={{ marginBottom: 6 }}>
                    <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
                      <Text style={{ color: c.textPrimary, fontSize: 12 }} numberOfLines={1}>{cat.name}</Text>
                      <Text style={{ color: c.textSecondary, fontSize: 11 }}>{fmtDur(cat.seconds)} · {cat.percent}%</Text>
                    </View>
                    <View style={{ height: 5, borderRadius: 3, backgroundColor: c.border, marginTop: 2 }}>
                      <View style={{ height: 5, borderRadius: 3, backgroundColor: "#10b98199", width: `${Math.max(2, cat.percent)}%` }} />
                    </View>
                  </View>
                ))}
              </>
            )}
            {frames.length > 0 && (
              <View style={styles.grid}>
                {frames.map((f) => (
                  <Image
                    key={f.idx}
                    source={{ uri: `${quicClient.baseUrl}/screenlog/${selected}/${f.file}`, headers: quicClient.getAuthHeaders() } as any}
                    style={styles.frame}
                    resizeMode="cover"
                  />
                ))}
              </View>
            )}
          </View>
        )}

        {/* Consent policy */}
        {policy && (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Consent policy</Text>
            <PolicyRow c={c} label="Recording enabled" on={policy.enabled} onChange={(v) => setPol({ enabled: v })} />
            <PolicyRow c={c} label="Allow remote start/stop" on={policy.allowRemoteControl} onChange={(v) => setPol({ allowRemoteControl: v })} />
            <PolicyRow c={c} label="Require grant for mesh peers" on={policy.requireMeshGrant} onChange={(v) => setPol({ requireMeshGrant: v })} />
            <PolicyRow c={c} label="Notify owner on remote start" on={policy.notifyOnStart} onChange={(v) => setPol({ notifyOnStart: v })} />
          </View>
        )}
      </ScrollView>
    </SafeAreaView>
  );
}

function PolicyRow({ c, label, on, onChange }: { c: any; label: string; on: boolean; onChange: (v: boolean) => void }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingVertical: 6 }}>
      <Text style={{ color: c.textPrimary, fontSize: 13, flex: 1 }}>{label}</Text>
      <Switch value={on} onValueChange={onChange} />
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  center: { flex: 1, alignItems: "center", justifyContent: "center", padding: spacing.lg },
  card: { borderWidth: 1, borderRadius: 12, padding: spacing.md },
  cardTitle: { fontSize: 14, fontWeight: "600" },
  section: { fontSize: 12, fontWeight: "600", marginTop: spacing.xs },
  row: { flexDirection: "row", alignItems: "center", borderWidth: 1, borderRadius: 10, padding: spacing.md, gap: spacing.sm },
  btn: { marginTop: spacing.md, borderWidth: 1, borderRadius: 10, paddingVertical: 10, alignItems: "center" },
  grid: { flexDirection: "row", flexWrap: "wrap", gap: 6, marginTop: spacing.sm },
  frame: { width: "31.5%", aspectRatio: 16 / 10, borderRadius: 6, backgroundColor: "#0002" },
});
