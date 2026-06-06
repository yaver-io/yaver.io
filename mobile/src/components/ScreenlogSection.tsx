// ScreenlogSection — the Screen Monitor panel embedded INSIDE a device's
// details (mirrors CodingAgentsSection). Peer-aware: for a non-active
// device it routes through /peer/<id> so you can drive a remote box
// (e.g. dad's WSL machine) from the device list. Start/stop recording,
// pull + render the smart activity report ("what it spent time on"), and
// see recent frames — all from the phone.
//
// New file to keep the edit to the shared DeviceDetailsModal tiny
// (one import + one render line).

import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Image, Pressable, StyleSheet, Text, View } from "react-native";
import { useColors } from "../context/ThemeContext";
import { useDevice, type Device } from "../context/DeviceContext";
import { quicClient } from "../lib/quic";

interface Session { id: string; title?: string; host?: string; startedAt: number; stoppedAt?: number; frames: number }
interface Frame { idx: number; capturedAt: number; file: string; activeApp?: string }
interface CategoryStat { name: string; seconds: number; percent: number }
interface Report { activeSec: number; idleSec: number; byCategory: CategoryStat[] }

function fmtDur(sec: number): string {
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.round(sec / 60)}m`;
  return `${Math.floor(sec / 3600)}h${Math.round((sec % 3600) / 60)}m`;
}

export function ScreenlogSection({ device }: { device: Device }) {
  const c = useColors();
  const { activeDevice, connectionStatus } = useDevice();
  const isActive = Boolean(activeDevice && activeDevice.id === device.id && connectionStatus === "connected");
  const target = isActive ? undefined : device.id;
  const base = `${quicClient.baseUrl}${target ? `/peer/${target}` : ""}`;

  const [open, setOpen] = useState(false);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [status, setStatus] = useState<any>(null);
  const [drivers, setDrivers] = useState<any>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [report, setReport] = useState<Report | null>(null);
  const [frames, setFrames] = useState<Frame[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const get = useCallback(async (path: string) => {
    const res = await fetch(`${base}${path}`, { headers: quicClient.getAuthHeaders() });
    if (!res.ok) throw new Error(`${path} → ${res.status}`);
    return res.json();
  }, [base]);
  const post = useCallback(async (path: string, body: any) => {
    const res = await fetch(`${base}${path}`, {
      method: "POST",
      headers: { ...quicClient.getAuthHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) throw new Error(`${path} → ${res.status}`);
    return res.json();
  }, [base]);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const [list, st, drv] = await Promise.all([
        get("/screenlog/list"), get("/screenlog/status"), get("/screenlog/drivers"),
      ]);
      setSessions(list.sessions || []);
      setStatus(st.status || null);
      setDrivers(drv.drivers || null);
      setError(null);
    } catch {
      setError("Couldn't reach this device's agent.");
    } finally {
      setLoading(false);
    }
  }, [get]);

  useEffect(() => { if (open) void refresh(); }, [open, refresh]);

  const openSession = async (id: string) => {
    setSelected(id); setReport(null); setFrames([]);
    try {
      const [r, f] = await Promise.all([
        get(`/screenlog/analyze?id=${encodeURIComponent(id)}`),
        get(`/screenlog/${id}/frames.json`),
      ]);
      setReport(r.report);
      const all: Frame[] = f.session?.frames || [];
      const n = Math.min(6, all.length);
      const out: Frame[] = [];
      const step = n > 1 ? (all.length - 1) / (n - 1) : 0;
      for (let i = 0; i < n; i++) out.push(all[Math.round(i * step)]);
      setFrames(out);
    } catch (e: any) { setError(e.message); }
  };

  const record = async (start: boolean) => {
    setBusy(true);
    try {
      if (start) await post("/screenlog/start", { config: { displays: "all" } });
      else await post("/screenlog/stop", {});
      await refresh();
    } catch (e: any) { setError(e.message); }
    finally { setBusy(false); }
  };

  const running = status?.running;

  return (
    <View style={{ marginTop: 14 }}>
      <Pressable onPress={() => setOpen((v) => !v)} style={styles.header}>
        <Text style={[styles.title, { color: c.textPrimary }]}>🎥 Screen Monitor</Text>
        <Text style={{ color: c.textMuted }}>{open ? "▾" : "▸"}</Text>
      </Pressable>

      {open && (
        <View style={[styles.body, { borderColor: c.border, backgroundColor: c.bgCard }]}>
          {loading ? <ActivityIndicator size="small" /> : (
            <>
              <Text style={{ color: c.textSecondary, fontSize: 11 }}>
                {drivers?.driver || "?"}{drivers?.wsl ? " · WSL→Windows host" : ""}{drivers?.displays ? ` · ${drivers.displays} display(s)` : ""}
              </Text>
              <Pressable
                onPress={() => record(!running)}
                disabled={busy || drivers?.available === false}
                style={[styles.btn, { backgroundColor: running ? "#f43f5e22" : "#10b98122", borderColor: running ? "#f43f5e55" : "#10b98155", opacity: busy ? 0.5 : 1 }]}
              >
                {busy ? <ActivityIndicator size="small" /> : (
                  <Text style={{ color: running ? "#fb7185" : "#34d399", fontWeight: "600", fontSize: 12 }}>
                    {running ? `Stop · ${status.keptFrames} frames` : "Start recording"}
                  </Text>
                )}
              </Pressable>
              {error && <Text style={{ color: "#fbbf24", fontSize: 11, marginTop: 4 }}>{error}</Text>}

              {sessions.slice(0, 5).map((s) => (
                <Pressable key={s.id} onPress={() => openSession(s.id)}
                  style={[styles.row, { borderColor: selected === s.id ? "#10b98155" : c.border }]}>
                  <Text style={{ color: c.textPrimary, fontSize: 12, flex: 1 }} numberOfLines={1}>
                    {s.title || s.id}
                  </Text>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>{s.frames}</Text>
                </Pressable>
              ))}

              {report && (
                <View style={{ marginTop: 8 }}>
                  <Text style={{ color: c.textSecondary, fontSize: 11, marginBottom: 4 }}>
                    Active {fmtDur(report.activeSec)} · idle {fmtDur(report.idleSec)}
                  </Text>
                  {report.byCategory.slice(0, 5).map((cat) => (
                    <View key={cat.name} style={{ marginBottom: 5 }}>
                      <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
                        <Text style={{ color: c.textPrimary, fontSize: 11 }} numberOfLines={1}>{cat.name}</Text>
                        <Text style={{ color: c.textMuted, fontSize: 10 }}>{fmtDur(cat.seconds)} · {cat.percent}%</Text>
                      </View>
                      <View style={{ height: 4, borderRadius: 2, backgroundColor: c.border, marginTop: 2 }}>
                        <View style={{ height: 4, borderRadius: 2, backgroundColor: "#10b98199", width: `${Math.max(2, cat.percent)}%` }} />
                      </View>
                    </View>
                  ))}
                  {frames.length > 0 && (
                    <View style={styles.grid}>
                      {frames.map((f) => (
                        <Image
                          key={f.idx}
                          source={{ uri: `${base}/screenlog/${selected}/${f.file}`, headers: quicClient.getAuthHeaders() } as any}
                          style={styles.frame}
                          resizeMode="cover"
                        />
                      ))}
                    </View>
                  )}
                </View>
              )}
            </>
          )}
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingVertical: 6 },
  title: { fontSize: 14, fontWeight: "600" },
  body: { borderWidth: 1, borderRadius: 10, padding: 12, gap: 8 },
  btn: { marginTop: 8, borderWidth: 1, borderRadius: 8, paddingVertical: 8, alignItems: "center" },
  row: { flexDirection: "row", alignItems: "center", borderWidth: 1, borderRadius: 8, padding: 8, gap: 8, marginTop: 4 },
  grid: { flexDirection: "row", flexWrap: "wrap", gap: 4, marginTop: 6 },
  frame: { width: "31.5%", aspectRatio: 16 / 10, borderRadius: 4, backgroundColor: "#0002" },
});
