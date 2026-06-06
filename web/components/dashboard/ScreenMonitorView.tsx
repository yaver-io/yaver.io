"use client";

// ScreenMonitorView — dashboard panel for the screenlog "screen as a
// stream of images" black box on the connected device. Lists local
// sessions, shows the deterministic activity report ("what did this
// machine spend time on"), a frame grid (blob-loaded with auth), and the
// owner consent policy. Talks to the device agent's /screenlog/* surface
// via the relay (agentClient.agentFetch attaches the token + relay pass).
//
// Privacy: frames are served straight off the device's local disk and
// never touch Convex; this view just renders them through the encrypted
// relay tunnel.

import { useCallback, useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";

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
  display: number;
  file: string;
  activeApp?: string;
  activeWindow?: string;
}
interface CategoryStat { name: string; seconds: number; percent: number; samples: number }
interface Report {
  source: string;
  subject: string;
  activeSec: number;
  idleSec: number;
  byCategory: CategoryStat[];
  topLabels: CategoryStat[];
}
interface Policy {
  enabled: boolean;
  allowRemoteControl: boolean;
  requireMeshGrant: boolean;
  allowedPeers?: string[];
  notifyOnStart: boolean;
}

async function getJSON(path: string): Promise<any> {
  const res = await agentClient.agentFetch(path);
  if (!res.ok) throw new Error(`${path} → ${res.status}`);
  return res.json();
}
async function postJSON(path: string, body: any): Promise<any> {
  const res = await agentClient.agentFetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${path} → ${res.status}`);
  return res.json();
}

function fmtDur(sec: number): string {
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.round(sec / 60)}m`;
  return `${Math.floor(sec / 3600)}h${Math.round((sec % 3600) / 60)}m`;
}
function fmtTime(ms: number): string {
  try { return new Date(ms).toLocaleString(); } catch { return String(ms); }
}

const RecordIcon = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" aria-hidden className="w-4 h-4">
    <circle cx="12" cy="12" r="8" />
    <circle cx="12" cy="12" r="3" fill="currentColor" />
  </svg>
);
const StopIcon = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" aria-hidden className="w-4 h-4">
    <rect x="6" y="6" width="12" height="12" rx="2" />
  </svg>
);

export default function ScreenMonitorView() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [status, setStatus] = useState<any>(null);
  const [drivers, setDrivers] = useState<any>(null);
  const [policy, setPolicy] = useState<Policy | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [report, setReport] = useState<Report | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [list, st, drv, pol] = await Promise.all([
        getJSON("/screenlog/list"),
        getJSON("/screenlog/status"),
        getJSON("/screenlog/drivers"),
        getJSON("/screenlog/policy"),
      ]);
      setSessions(list.sessions || []);
      setStatus(st.status || null);
      setDrivers(drv.drivers || null);
      setPolicy(pol.policy || null);
      setError(null);
    } catch (e: any) {
      setError("Couldn't reach the device agent — connect to a device first.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
    const i = setInterval(refresh, 5000);
    return () => clearInterval(i);
  }, [refresh]);

  const start = async () => {
    setBusy(true);
    try { await postJSON("/screenlog/start", { config: { displays: "all" } }); await refresh(); }
    catch (e: any) { setError(e.message); }
    finally { setBusy(false); }
  };
  const stop = async () => {
    setBusy(true);
    try { await postJSON("/screenlog/stop", {}); await refresh(); }
    catch (e: any) { setError(e.message); }
    finally { setBusy(false); }
  };
  const analyze = async (id: string) => {
    setSelected(id);
    setReport(null);
    try { const r = await getJSON(`/screenlog/analyze?id=${encodeURIComponent(id)}`); setReport(r.report); }
    catch (e: any) { setError(e.message); }
  };
  const togglePolicy = async (patch: Partial<Policy>) => {
    try { const r = await postJSON("/screenlog/policy", patch); setPolicy(r.policy); }
    catch (e: any) { setError(e.message); }
  };

  const running = status?.running;

  if (loading) return <div className="text-center py-8 text-surface-500 text-sm">Loading screen monitor…</div>;

  return (
    <div className="space-y-5">
      {/* Header / controls */}
      <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-4">
        <div className="flex items-center justify-between gap-3 flex-wrap">
          <div>
            <h2 className="text-sm font-medium text-surface-100">Screen Monitor</h2>
            <p className="text-xs text-surface-500 mt-0.5">
              Local-only screen black box · {drivers?.driver || "?"}
              {drivers?.wsl ? " · WSL" : ""}{drivers?.displays ? ` · ${drivers.displays} display(s)` : ""}
            </p>
          </div>
          <div className="flex items-center gap-2">
            {running ? (
              <button onClick={stop} disabled={busy} className="inline-flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md bg-rose-500/15 text-rose-300 hover:bg-rose-500/25 disabled:opacity-50">
                <StopIcon /> Stop
              </button>
            ) : (
              <button onClick={start} disabled={busy || drivers?.available === false} className="inline-flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md bg-emerald-500/15 text-emerald-300 hover:bg-emerald-500/25 disabled:opacity-50">
                <RecordIcon /> Start recording
              </button>
            )}
          </div>
        </div>
        {running && (
          <div className="mt-3 text-xs text-surface-400 flex gap-4">
            <span className="inline-flex items-center gap-1.5"><span className="w-2 h-2 rounded-full bg-rose-400 animate-pulse" /> recording</span>
            <span>{status.keptFrames} frames</span>
            <span>{status.dropped} dup-skipped</span>
            <span>{Math.round((status.bytes || 0) / 1024 / 1024)} MB</span>
            <span>{fmtDur(status.elapsedSec || 0)}</span>
          </div>
        )}
        {drivers?.available === false && (
          <p className="mt-2 text-xs text-amber-400">{drivers?.error || "Capture not available on this host."}</p>
        )}
      </div>

      {error && <div className="text-xs text-amber-400">{error}</div>}

      {/* Sessions + selected report */}
      <div className="grid md:grid-cols-2 gap-4">
        <div>
          <h3 className="text-xs font-medium text-surface-400 mb-2">Sessions</h3>
          {sessions.length === 0 ? (
            <div className="text-xs text-surface-500 py-6 text-center rounded-lg border border-dashed border-surface-800">No recordings yet.</div>
          ) : (
            <div className="space-y-1">
              {sessions.map((s) => (
                <button key={s.id} onClick={() => analyze(s.id)}
                  className={`w-full text-left rounded-lg border p-3 transition ${selected === s.id ? "border-emerald-500/40 bg-emerald-500/5" : "border-surface-800 bg-surface-900/40 hover:bg-surface-900/70"}`}>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-surface-200 font-medium">{s.title || s.id}</span>
                    <span className="text-[11px] text-surface-500">{s.frames} frames</span>
                  </div>
                  <div className="text-[11px] text-surface-500 mt-0.5">{s.host} · {fmtTime(s.startedAt)}</div>
                </button>
              ))}
            </div>
          )}
        </div>

        <div>
          <h3 className="text-xs font-medium text-surface-400 mb-2">
            {selected ? "What it spent time on" : "Select a session"}
          </h3>
          {selected && !report && <div className="text-xs text-surface-500 py-6 text-center">Analyzing…</div>}
          {report && (
            <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-3 space-y-3">
              <div className="text-[11px] text-surface-500">
                Active {fmtDur(report.activeSec)} · idle {fmtDur(report.idleSec)} · {report.subject}
              </div>
              <div className="space-y-1.5">
                {report.byCategory.slice(0, 8).map((c) => (
                  <div key={c.name}>
                    <div className="flex justify-between text-[11px] text-surface-300">
                      <span className="truncate pr-2">{c.name}</span>
                      <span className="text-surface-500">{fmtDur(c.seconds)} · {c.percent}%</span>
                    </div>
                    <div className="h-1.5 rounded-full bg-surface-800 mt-0.5 overflow-hidden">
                      <div className="h-full bg-emerald-500/60" style={{ width: `${Math.max(2, c.percent)}%` }} />
                    </div>
                  </div>
                ))}
              </div>
              {selected && <SecurityCamPlayer sessionId={selected} />}
            </div>
          )}
        </div>
      </div>

      {/* Policy / consent */}
      {policy && (
        <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-4">
          <h3 className="text-xs font-medium text-surface-400 mb-3">Consent policy (this device)</h3>
          <div className="space-y-2">
            <PolicyToggle label="Recording enabled (master switch)" on={policy.enabled} onChange={(v) => togglePolicy({ enabled: v })} />
            <PolicyToggle label="Allow remote start/stop" on={policy.allowRemoteControl} onChange={(v) => togglePolicy({ allowRemoteControl: v })} />
            <PolicyToggle label="Require explicit grant for mesh peers" on={policy.requireMeshGrant} onChange={(v) => togglePolicy({ requireMeshGrant: v })} />
            <PolicyToggle label="Notify owner on remote start" on={policy.notifyOnStart} onChange={(v) => togglePolicy({ notifyOnStart: v })} />
          </div>
          {policy.allowedPeers && policy.allowedPeers.length > 0 && (
            <p className="text-[11px] text-surface-500 mt-2">Granted peers: {policy.allowedPeers.join(", ")}</p>
          )}
        </div>
      )}
    </div>
  );
}

function PolicyToggle({ label, on, onChange }: { label: string; on: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-center justify-between gap-3 cursor-pointer">
      <span className="text-xs text-surface-300">{label}</span>
      <button type="button" onClick={() => onChange(!on)}
        className={`w-9 h-5 rounded-full transition relative ${on ? "bg-emerald-500/70" : "bg-surface-700"}`}>
        <span className={`absolute top-0.5 w-4 h-4 rounded-full bg-white transition ${on ? "left-[18px]" : "left-0.5"}`} />
      </button>
    </label>
  );
}

// SecurityCamPlayer — a DVR-style scrubber over a session's frames. Scrub the
// timeline forward/back, step frame-by-frame, or hit play to watch his screen
// like security-cam footage. Frames are blob-loaded on demand through the
// auth'd relay (a plain <img src> can't carry the bearer/relay headers) and
// cached, so scrubbing is smooth without pulling every frame up front.
function SecurityCamPlayer({ sessionId }: { sessionId: string }) {
  const [frames, setFrames] = useState<Frame[]>([]);
  const [idx, setIdx] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [curUrl, setCurUrl] = useState<string | null>(null);
  const cache = useRef<Map<number, string>>(new Map());

  // Load the frame index (only kept frames with an image), oldest→newest.
  useEffect(() => {
    let cancelled = false;
    const c = cache.current;
    (async () => {
      try {
        const data = await getJSON(`/screenlog/${sessionId}/frames.json`);
        const fr: Frame[] = ((data.session?.frames || []) as Frame[])
          .filter((f) => f.file)
          .sort((a, b) => a.capturedAt - b.capturedAt);
        if (!cancelled) { setFrames(fr); setIdx(fr.length ? fr.length - 1 : 0); }
      } catch { /* ignore */ }
    })();
    return () => { cancelled = true; c.forEach((u) => URL.revokeObjectURL(u)); c.clear(); };
  }, [sessionId]);

  // Load + cache the current frame's blob.
  useEffect(() => {
    const f = frames[idx];
    if (!f) return;
    const cached = cache.current.get(idx);
    if (cached) { setCurUrl(cached); return; }
    let cancelled = false;
    (async () => {
      const res = await agentClient.agentFetch(`/screenlog/${sessionId}/${f.file}`);
      if (!res.ok || cancelled) return;
      const url = URL.createObjectURL(await res.blob());
      if (cancelled) { URL.revokeObjectURL(url); return; }
      cache.current.set(idx, url);
      setCurUrl(url);
    })();
    return () => { cancelled = true; };
  }, [idx, frames, sessionId]);

  // Playback — advance ~1.4 fps, stop at the end.
  useEffect(() => {
    if (!playing || frames.length === 0) return;
    const t = setInterval(() => setIdx((i) => (i + 1 >= frames.length ? (setPlaying(false), i) : i + 1)), 700);
    return () => clearInterval(t);
  }, [playing, frames.length]);

  if (frames.length === 0) return <div className="text-xs text-surface-500 pt-2">No frames captured yet.</div>;
  const f = frames[idx];
  const btn = "w-8 h-8 inline-flex items-center justify-center rounded-md bg-surface-800 hover:bg-surface-700 text-surface-200 text-sm";
  return (
    <div className="pt-2 space-y-2">
      <div className="relative bg-black rounded-md overflow-hidden border border-surface-800">
        {curUrl ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={curUrl} alt="" className="w-full block" />
        ) : (
          <div className="aspect-video flex items-center justify-center text-surface-600 text-xs">loading…</div>
        )}
        <div className="absolute top-1 left-2 text-[11px] text-white/85 bg-black/50 px-1.5 py-0.5 rounded">
          {new Date(f.capturedAt).toLocaleString()}{f.activeApp ? " · " + f.activeApp : ""}
        </div>
        <div className="absolute top-1 right-2 text-[11px] text-white/70 bg-black/50 px-1.5 py-0.5 rounded">
          {idx + 1}/{frames.length}
        </div>
      </div>
      <input
        type="range" min={0} max={frames.length - 1} value={idx}
        onChange={(e) => { setPlaying(false); setIdx(Number(e.target.value)); }}
        className="w-full accent-emerald-500"
      />
      <div className="flex items-center justify-center gap-2">
        <button className={btn} title="First" onClick={() => { setPlaying(false); setIdx(0); }}>⏮</button>
        <button className={btn} title="Back" onClick={() => { setPlaying(false); setIdx((i) => Math.max(0, i - 1)); }}>◀</button>
        <button className={btn} title={playing ? "Pause" : "Play"} onClick={() => setPlaying((p) => !p)}>{playing ? "⏸" : "▶"}</button>
        <button className={btn} title="Forward" onClick={() => { setPlaying(false); setIdx((i) => Math.min(frames.length - 1, i + 1)); }}>▶</button>
        <button className={btn} title="Latest" onClick={() => { setPlaying(false); setIdx(frames.length - 1); }}>⏭</button>
      </div>
    </div>
  );
}
