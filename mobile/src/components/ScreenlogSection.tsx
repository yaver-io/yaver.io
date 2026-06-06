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
  const [frameIdx, setFrameIdx] = useState(0);
  const [playing, setPlaying] = useState(false);
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

  // Security-cam playback — advance through frames while playing.
  useEffect(() => {
    if (!playing || frames.length === 0) return;
    const t = setInterval(() => {
      setFrameIdx((i) => (i + 1 >= frames.length ? (setPlaying(false), i) : i + 1));
    }, 700);
    return () => clearInterval(t);
  }, [playing, frames.length]);

  const openSession = async (id: string) => {
    setSelected(id); setReport(null); setFrames([]);
    try {
      const [r, f] = await Promise.all([
        get(`/screenlog/analyze?id=${encodeURIComponent(id)}`),
        get(`/screenlog/${id}/frames.json`),
      ]);
      setReport(r.report);
      const all: Frame[] = (f.session?.frames || [])
        .filter((fr: Frame) => fr.file)
        .sort((a: Frame, b: Frame) => a.capturedAt - b.capturedAt);
      setFrames(all);
      setFrameIdx(all.length ? all.length - 1 : 0);
      setPlaying(false);
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
                    <View style={{ marginTop: 8 }}>
                      <Image
                        source={{ uri: `${base}/screenlog/${selected}/${frames[Math.min(frameIdx, frames.length - 1)].file}`, headers: quicClient.getAuthHeaders() } as any}
                        style={styles.player}
                        resizeMode="contain"
                      />
                      <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }}>
                        {new Date(frames[Math.min(frameIdx, frames.length - 1)].capturedAt).toLocaleString()}
                        {frames[Math.min(frameIdx, frames.length - 1)].activeApp ? " · " + frames[Math.min(frameIdx, frames.length - 1)].activeApp : ""}
                        {"  ·  " + (frameIdx + 1) + "/" + frames.length}
                      </Text>
                      <View style={styles.controls}>
                        <Pressable onPress={() => { setPlaying(false); setFrameIdx(0); }} style={styles.ctlBtn}><Text style={styles.ctl}>⏮</Text></Pressable>
                        <Pressable onPress={() => { setPlaying(false); setFrameIdx((i) => Math.max(0, i - 1)); }} style={styles.ctlBtn}><Text style={styles.ctl}>◀</Text></Pressable>
                        <Pressable onPress={() => setPlaying((p) => !p)} style={styles.ctlBtn}><Text style={styles.ctl}>{playing ? "⏸" : "▶"}</Text></Pressable>
                        <Pressable onPress={() => { setPlaying(false); setFrameIdx((i) => Math.min(frames.length - 1, i + 1)); }} style={styles.ctlBtn}><Text style={styles.ctl}>▶</Text></Pressable>
                        <Pressable onPress={() => { setPlaying(false); setFrameIdx(frames.length - 1); }} style={styles.ctlBtn}><Text style={styles.ctl}>⏭</Text></Pressable>
                      </View>
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
  player: { width: "100%", aspectRatio: 16 / 10, borderRadius: 6, backgroundColor: "#000" },
  controls: { flexDirection: "row", justifyContent: "center", gap: 8, marginTop: 6 },
  ctlBtn: { width: 34, height: 30, borderRadius: 6, backgroundColor: "#ffffff14", alignItems: "center", justifyContent: "center" },
  ctl: { color: "#cbd5e1", fontSize: 14 },
});
