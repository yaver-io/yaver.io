// Screen Monitor tab — drive the screenlog "screen as a stream of images"
// black box on ANY of your machines (e.g. a family member's PC you manage)
// from your phone. Lists local sessions, shows the deterministic activity
// report ("what did it spend time on"), a frame grid (loaded with auth headers
// straight off the device's disk over the relay), and the owner consent policy
// + remote start/stop.
//
// Device targeting: a picker at the top lets you point the monitor at any
// OWN/online or SHARED device WITHOUT changing your app-wide active box. Each
// target gets its own connectionManager.clientFor(deviceId) client (direct LAN
// or relay), so watching dad's PC doesn't drop your primary coding box.
//
// Privacy: frames live only on the recorded device and stream through the
// encrypted relay tunnel; nothing touches Convex. Remote start is gated by the
// device's ScreenlogPolicy and notifies the owner.

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
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
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice, type Device } from "../../src/context/DeviceContext";
import { useAuth } from "../../src/context/AuthContext";
import { connectionManager } from "../../src/lib/connectionManager";
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

export default function ScreenlogScreen() {
  const c = useColors();
  const router = useRouter();
  const { activeDevice, devices } = useDevice();
  const { token } = useAuth();

  // Which machine the monitor is pointed at. Defaults to the app-wide active
  // box but the picker can retarget it independently.
  const [targetId, setTargetId] = useState<string | null>(activeDevice?.id ?? null);
  useEffect(() => {
    if (!targetId && activeDevice) setTargetId(activeDevice.id);
  }, [activeDevice, targetId]);

  const target = useMemo<Device | null>(
    () => devices.find((d) => d.id === targetId) ?? activeDevice ?? null,
    [devices, targetId, activeDevice]
  );
  // Per-device client — NOT the global quicClient, so retargeting the monitor
  // never changes the user's primary box.
  const client = useMemo(() => (target ? connectionManager.clientFor(target.id) : null), [target]);

  const [connecting, setConnecting] = useState(false);
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

  const agentGet = useCallback(
    async (path: string): Promise<any> => {
      if (!client) throw new Error("no device selected");
      const res = await fetch(`${client.baseUrl}${path}`, { headers: client.getAuthHeaders() });
      if (!res.ok) throw new Error(`${path} → ${res.status}`);
      return res.json();
    },
    [client]
  );
  const agentPost = useCallback(
    async (path: string, body: any): Promise<any> => {
      if (!client) throw new Error("no device selected");
      const res = await fetch(`${client.baseUrl}${path}`, {
        method: "POST",
        headers: { ...client.getAuthHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) throw new Error(`${path} → ${res.status}`);
      return res.json();
    },
    [client]
  );

  const refresh = useCallback(async () => {
    if (!client || !target?.online) return;
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
      setError(`Couldn't reach ${target?.name ?? "the device"} — is its Yaver agent online?`);
    } finally {
      setRefreshing(false);
    }
  }, [client, target, agentGet]);

  // Connect the per-device client when the target changes, then load. The
  // active box is usually already connected (clientFor returns it); a freshly
  // picked remote box connects here (direct LAN or relay).
  useEffect(() => {
    let cancelled = false;
    setSessions([]); setStatus(null); setDrivers(null); setPolicy(null);
    setSelected(null); setReport(null); setFrames([]); setError(null);
    if (!target || !token) return;
    if (!target.online) return;
    (async () => {
      try {
        if (!client?.isConnected) {
          setConnecting(true);
          await connectionManager.ensureConnected(target.id, {
            host: target.host,
            port: target.port,
            token,
            lanIps: target.lanIps,
            connectionPreferences: target.connectionPreferences,
          });
        }
        if (!cancelled) await refresh();
      } catch {
        if (!cancelled) setError(`Couldn't connect to ${target.name}.`);
      } finally {
        if (!cancelled) setConnecting(false);
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target?.id, token]);

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

  // Sorted machine list for the picker — online first, then by name.
  const pickable = useMemo(
    () =>
      [...devices].sort((a, b) =>
        a.online === b.online ? (a.name || "").localeCompare(b.name || "") : a.online ? -1 : 1
      ),
    [devices]
  );

  const running = status?.running;

  return (
    <SafeAreaView style={[styles.root, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <AppScreenHeader title="Screen Monitor" onBack={() => router.navigate("/(tabs)/more" as any)} />
      <ScrollView
        contentContainerStyle={{ padding: spacing.md, gap: spacing.md }}
        refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} tintColor={c.textSecondary} />}
      >
        {/* Machine picker — point the monitor at any of your devices */}
        <View style={{ gap: 6 }}>
          <Text style={[styles.section, { color: c.textSecondary }]}>Machine</Text>
          {pickable.length === 0 ? (
            <Text style={{ color: c.textSecondary, fontSize: 12 }}>
              No machines registered. Install Yaver and run <Text style={{ fontFamily: "Menlo" }}>yaver serve</Text> on the PC you want to watch.
            </Text>
          ) : (
            <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 8, paddingVertical: 2 }}>
              {pickable.map((d) => {
                const on = d.id === target?.id;
                return (
                  <Pressable
                    key={d.id}
                    onPress={() => setTargetId(d.id)}
                    style={{
                      flexDirection: "row",
                      alignItems: "center",
                      gap: 6,
                      borderRadius: 999,
                      paddingHorizontal: 12,
                      paddingVertical: 7,
                      borderWidth: 1,
                      borderColor: on ? "#10b98199" : c.border,
                      backgroundColor: on ? "#10b98114" : c.bgCard,
                    }}
                  >
                    <View style={{ width: 7, height: 7, borderRadius: 4, backgroundColor: d.online ? "#34d399" : c.textMuted }} />
                    <Text style={{ color: on ? c.textPrimary : c.textSecondary, fontSize: 13, fontWeight: on ? "700" : "500" }}>
                      {d.alias || d.name}
                    </Text>
                  </Pressable>
                );
              })}
            </ScrollView>
          )}
        </View>

        {!target ? (
          <View style={styles.center}>
            <Text style={{ color: c.textSecondary }}>Pick a machine to monitor.</Text>
          </View>
        ) : !target.online ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, gap: 6 }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary }]}>{target.alias || target.name} is offline</Text>
            <Text style={{ color: c.textSecondary, fontSize: 12, lineHeight: 17 }}>
              Screen Monitor needs the machine online. Make sure Yaver is running there
              (<Text style={{ fontFamily: "Menlo" }}>yaver serve</Text>) and it's signed into this account.
              If it's a family member's PC on their own account, share it to you first.
            </Text>
          </View>
        ) : connecting ? (
          <View style={styles.center}><ActivityIndicator color={c.textSecondary} /></View>
        ) : (
          <>
            {/* Status + controls */}
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.cardTitle, { color: c.textPrimary }]}>{target.alias || target.name}</Text>
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
              <Text style={{ color: c.textSecondary, fontSize: 12 }}>
                No recordings yet. Tap “Start recording” above to begin capturing this machine’s screen.
              </Text>
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
                {frames.length > 0 && client && (
                  <View style={styles.grid}>
                    {frames.map((f) => (
                      <Image
                        key={f.idx}
                        source={{ uri: `${client.baseUrl}/screenlog/${selected}/${f.file}`, headers: client.getAuthHeaders() } as any}
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
          </>
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
  center: { alignItems: "center", justifyContent: "center", padding: spacing.lg },
  card: { borderWidth: 1, borderRadius: 12, padding: spacing.md },
  cardTitle: { fontSize: 14, fontWeight: "600" },
  section: { fontSize: 12, fontWeight: "600", marginTop: spacing.xs },
  row: { flexDirection: "row", alignItems: "center", borderWidth: 1, borderRadius: 10, padding: spacing.md, gap: spacing.sm },
  btn: { marginTop: spacing.md, borderWidth: 1, borderRadius: 10, paddingVertical: 10, alignItems: "center" },
  grid: { flexDirection: "row", flexWrap: "wrap", gap: 6, marginTop: spacing.sm },
  frame: { width: "31.5%", aspectRatio: 16 / 10, borderRadius: 6, backgroundColor: "#0002" },
});
