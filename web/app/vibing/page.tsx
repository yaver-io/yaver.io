"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useAuth } from "@/lib/use-auth";
import { useDevices, type Device } from "@/lib/use-devices";
import { CONVEX_URL } from "@/lib/constants";

type Project = { name: string; path: string; branch?: string };
type DevStatus = {
  framework?: string;
  kind?: string;
  running: boolean;
  serving: boolean;
  servingLabel?: string;
  port?: number;
  vibeSessionId?: string;
  previewHealth?: { state?: string; reason?: string };
};

type Conn = { base: string; password: string };

export default function VibingPage() {
  const { token, isLoading, isAuthenticated } = useAuth();
  const { devices } = useDevices(token);
  const [deviceId, setDeviceId] = useState("");
  const [conn, setConn] = useState<Conn | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [project, setProject] = useState("");
  const [status, setStatus] = useState<DevStatus | null>(null);
  const [frameUri, setFrameUri] = useState("");
  const [frameError, setFrameError] = useState("");
  const [frameOverride, setFrameOverride] = useState("");
  const [msg, setMsg] = useState("");
  const [transport, setTransport] = useState("auto");
  const [relayTier, setRelayTier] = useState<"free" | "pro">("free");
  const [relayIce, setRelayIce] = useState<{ stun?: string; turn?: string }>({});
  const [webrtcMsg, setWebrtcMsg] = useState("");
  const [webrtcStream, setWebrtcStream] = useState<MediaStream | null>(null);
  const timers = useRef<ReturnType<typeof setInterval>[]>([]);
  const pcRef = useRef<RTCPeerConnection | null>(null);

  const selectedDevice = useMemo(() => devices.find((d) => d.id === deviceId), [devices, deviceId]);

  const h = (conn: Conn, json = false) => ({
    Authorization: `Bearer ${token}`,
    "X-Relay-Password": conn.password,
    ...(json ? { "Content-Type": "application/json" } : {}),
  });

  const connect = useCallback(async () => {
    if (!token || !selectedDevice) return;
    setMsg(`Connecting to ${selectedDevice.name}…`);
    try {
      const [cfgRes, setRes] = await Promise.all([
        fetch(`${CONVEX_URL}/config`),
        fetch(`${CONVEX_URL}/settings`, { headers: { Authorization: `Bearer ${token}` } }),
      ]);
      const cfg = (await cfgRes.json()) as { relayServers?: Array<{ httpUrl: string }>; relayIce?: { stun?: string; turn?: string } };
      const set = (await setRes.json()) as { settings?: { relayPassword?: string; relayUrl?: string; vibingTransport?: string; relayTier?: string } };
      const relayUrl = set.settings?.relayUrl || cfg.relayServers?.[0]?.httpUrl || "";
      const c: Conn = { base: `${relayUrl}/d/${selectedDevice.id}`, password: set.settings?.relayPassword || "" };
      setConn(c);
      if (set.settings?.vibingTransport) setTransport(set.settings.vibingTransport);
      if (set.settings?.relayTier === "pro" || set.settings?.relayTier === "free") setRelayTier(set.settings.relayTier);
      if (cfg.relayIce) setRelayIce(cfg.relayIce);

      const pr = await fetch(`${c.base}/projects?refresh=1`, { headers: h(c) });
      const pd = (await pr.json()) as { projects?: Project[] };
      setProjects(pd.projects || []);
      setMsg(`Connected to ${selectedDevice.name}. Pick a project to start vibing.`);
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    }
  }, [token, selectedDevice]);

  useEffect(() => {
    if (selectedDevice) connect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedDevice]);

  useEffect(() => {
    if (!deviceId && devices.length) setDeviceId((devices.find((d) => d.online) ?? devices[0]).id);
  }, [deviceId, devices]);

  const refreshStatus = useCallback(async () => {
    if (!conn) return;
    try {
      const r = await fetch(`${conn.base}/dev/status`, { headers: h(conn) });
      if (r.ok) setStatus(await r.json());
    } catch {}
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conn]);

  useEffect(() => {
    if (!conn) return;
    refreshStatus();
    const iv = setInterval(refreshStatus, 4000);
    timers.current.push(iv);
    return () => clearInterval(iv);
  }, [conn, refreshStatus]);

  const startProject = async (path: string) => {
    if (!conn) return;
    setProject(path);
    setFrameUri("");
    setFrameError("");
    setMsg(`Starting ${path}…`);
    try {
      const r = await fetch(`${conn.base}/dev/start`, { method: "POST", headers: h(conn, true), body: JSON.stringify({ workDir: path }) });
      if (r.ok) setStatus(await r.json());
      setTimeout(refreshStatus, 3000);
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    }
  };

  const stopPreview = async () => {
    if (!conn) return;
    try {
      await fetch(`${conn.base}/dev/stop`, { method: "POST", headers: h(conn) });
      setStatus(null);
      setFrameUri("");
      refreshStatus();
    } catch {}
  };

  const saveTransport = async (v: string) => {
    setTransport(v);
    if (token && conn) {
      await fetch(`${CONVEX_URL}/settings`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
        body: JSON.stringify({ vibingTransport: v }),
      }).catch(() => {});
    }
  };

  // WebRTC (Relay Pro) attempt: free STUN from the relay + TURN when pro.
  const startWebRTC = async () => {
    if (!conn || !token) return;
    setWebrtcMsg("Requesting WebRTC session…");
    try {
      const pc = new RTCPeerConnection({
        iceServers: [
          ...(relayIce.stun ? [{ urls: relayIce.stun }] : []),
          ...(relayTier === "pro" && relayIce.turn ? [{ urls: relayIce.turn, username: "yaver-pro", credential: "yaver-pro" }] : []),
        ],
      });
      pcRef.current = pc;
      pc.ontrack = (e) => {
        setWebrtcStream(e.streams[0] || null);
        setWebrtcMsg("");
      };
      pc.oniceconnectionstatechange = () => {
        if (pc.iceConnectionState === "failed") setWebrtcMsg("WebRTC failed — box broadcaster not reachable. Falling back to SSE.");
      };

      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      // Signaling: box agent's WebRTC broadcaster endpoint.
      const res = await fetch(`${conn.base}/rtc/offer`, {
        method: "POST",
        headers: h(conn, true),
        body: JSON.stringify({ sdp: offer.sdp, type: offer.type }),
      });
      if (!res.ok) {
        setWebrtcMsg(`Box has no WebRTC broadcaster yet (${res.status}) — using SSE.`);
        pc.close();
        pcRef.current = null;
        return;
      }
      const answer = await res.json();
      await pc.setRemoteDescription(new RTCSessionDescription({ sdp: answer.sdp, type: answer.type }));
    } catch (e) {
      setWebrtcMsg(`WebRTC unavailable: ${e instanceof Error ? e.message : String(e)} — using SSE.`);
    }
  };

  useEffect(() => () => {
    timers.current.forEach(clearInterval);
    pcRef.current?.close();
  }, []);

  const fetchFrame = useCallback(async () => {
    if (!conn || !status?.serving || !status?.port) return;
    try {
      const o = frameOverride.replace(/\/$/, "");
      const url = o ? `${o}/frame` : `${conn.base}/vibing/frame?url=${encodeURIComponent(`http://localhost:${status.port}/`)}`;
      const r = await fetch(url, { headers: { Authorization: `Bearer ${token}` } });
      if (r.status === 404) {
        setFrameError(o ? "Local frame server not found" : "Frame endpoint not available on this box");
        return;
      }
      if (!r.ok) return;
      const blob = await r.blob();
      setFrameUri(URL.createObjectURL(blob));
      setFrameError("");
    } catch {}
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conn, status, frameOverride, token]);

  useEffect(() => {
    if (!status?.serving) {
      setFrameUri("");
      return;
    }
    fetchFrame();
    const iv = setInterval(fetchFrame, 2500);
    timers.current.push(iv);
    return () => clearInterval(iv);
  }, [status?.serving, fetchFrame]);

  // WebRTC attempt when the user picks it and the preview is serving
  useEffect(() => {
    if (status?.serving && transport === "webrtc" && conn) {
      startWebRTC();
    }
    return () => {
      pcRef.current?.close();
      pcRef.current = null;
      setWebrtcStream(null);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status?.serving, transport, conn]);

  if (!isLoading && !isAuthenticated) {
    if (typeof window !== "undefined") window.location.href = "/auth";
    return null;
  }
  if (isLoading) return <main className="flex min-h-screen items-center justify-center bg-surface-950 text-surface-400">Loading account…</main>;

  const serving = !!status?.serving;

  return (
    <main className="min-h-screen bg-surface-950 px-6 py-10 text-surface-200">
      <div className="mx-auto max-w-5xl">
        <div className="mb-8 flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-bold text-surface-50">Studio</h1>
            <p className="mt-1 text-sm text-surface-400">
              Live preview of a project running on {selectedDevice ? selectedDevice.name : "your device"}
            </p>
          </div>
          <span className={`rounded-lg px-3 py-1.5 text-xs font-semibold uppercase ${serving ? "bg-green-500/15 text-green-400" : "bg-surface-800 text-surface-400"}`}>
            {serving ? "Serving" : "Idle"}
          </span>
        </div>

        {msg && <div className="mb-4 rounded-lg border border-surface-800 bg-surface-900 px-4 py-2 text-sm text-surface-300">{msg}</div>}

        <div className="mb-6 grid gap-4 sm:grid-cols-2">
          <div className="rounded-xl border border-surface-800 bg-surface-900 p-4">
            <label className="mb-1 block text-xs font-medium text-surface-400">Device</label>
            <select value={deviceId} onChange={(e) => setDeviceId(e.target.value)} className="w-full rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm">
              {devices.map((d) => (
                <option key={d.id} value={d.id}>{d.name} ({d.online ? "online" : "offline"})</option>
              ))}
            </select>
          </div>
          <div className="rounded-xl border border-surface-800 bg-surface-900 p-4">
            <label className="mb-1 block text-xs font-medium text-surface-400">Transport</label>
            <select value={transport} onChange={(e) => saveTransport(e.target.value)} className="w-full rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm capitalize">
              <option value="auto">Auto (SSE → WebRTC)</option>
              <option value="sse">SSE (free)</option>
              <option value="webrtc">WebRTC (Relay Pro)</option>
            </select>
            <p className="mt-1 text-[11px] text-surface-500">
              Tier: <span className={relayTier === "pro" ? "text-green-400" : "text-surface-400"}>{relayTier === "pro" ? "Relay Pro" : "Free"}</span>
              {relayTier === "pro"
                ? " · WebRTC/TURN low-latency (STUN via free relay)"
                : " · SSE frames + STUN; WebRTC/TURN needs Relay Pro"}
            </p>
          </div>
        </div>

        <h2 className="mb-2 text-sm font-semibold text-surface-300">Projects on device</h2>
        {projects.length === 0 ? (
          <p className="text-sm text-surface-500">No projects discovered. Connect a device first.</p>
        ) : (
          <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {projects.map((p) => (
              <button
                key={p.path}
                onClick={() => startProject(p.path)}
                className={`rounded-xl border p-4 text-left transition hover:border-surface-500 ${project === p.path ? "border-surface-50 bg-surface-50/10" : "border-surface-800 bg-surface-900"}`}
              >
                <div className="text-sm font-semibold text-surface-100">📁 {p.name}</div>
                <div className="mt-1 truncate text-xs text-surface-500">{p.path}</div>
                {p.branch && <div className="mt-1 text-xs text-surface-400">{p.branch}</div>}
              </button>
            ))}
          </div>
        )}

        {status && (
          <div className="mb-6 rounded-xl border border-surface-800 bg-surface-900 p-4 text-sm">
            <div className="flex items-center justify-between">
              <div>
                <div className="font-semibold text-surface-100">{status.servingLabel || "Not serving"}</div>
                <div className="mt-0.5 text-xs text-surface-400">
                  {status.framework || "-"} · port {status.port || "-"}
                  {status.vibeSessionId ? ` · ${status.vibeSessionId}` : ""}
                </div>
              </div>
              <div className="flex gap-2">
                <button onClick={() => project && startProject(project)} className="rounded-lg bg-surface-50 px-3 py-1.5 text-xs font-semibold text-surface-950 hover:bg-surface-100">
                  {serving ? "Restart" : "Start"}
                </button>
                <button onClick={stopPreview} className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-1.5 text-xs font-semibold text-red-400 hover:bg-red-500/20">
                  Stop
                </button>
              </div>
            </div>
            {status.previewHealth?.reason && (
              <div className="mt-2 rounded-lg border border-amber-500/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">{status.previewHealth.reason}</div>
            )}
          </div>
        )}

        <div className="mb-6 rounded-xl border border-surface-800 bg-surface-900 p-4">
          <label className="mb-1 block text-xs font-medium text-surface-400">Frame source (dev)</label>
          <input
            value={frameOverride}
            onChange={(e) => setFrameOverride(e.target.value)}
            placeholder="e.g. http://localhost:8787 (empty = box)"
            className="w-full rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-sm"
          />
        </div>

        {serving && webrtcStream && (
          <div className="rounded-xl border border-surface-800 bg-surface-900 p-4">
            <div className="mb-2 text-sm font-semibold text-surface-100">Live preview · WebRTC ✓</div>
            <video autoPlay playsInline ref={(el) => { if (el && webrtcStream && !el.srcObject) el.srcObject = webrtcStream; }} className="w-full rounded-lg border border-surface-800" />
          </div>
        )}
        {serving && webrtcMsg && !webrtcStream && (
          <div className="mb-4 rounded-lg border border-amber-500/20 bg-amber-500/10 px-4 py-2 text-sm text-amber-300">{webrtcMsg}</div>
        )}
        {serving && !webrtcStream && frameUri && (
          <div className="rounded-xl border border-surface-800 bg-surface-900 p-4">
            <div className="mb-2 text-sm font-semibold text-surface-100">Live preview {frameError ? "" : "· frames"} ✓</div>
            <img src={frameUri} alt="live preview" className="w-full rounded-lg border border-surface-800" />
          </div>
        )}
        {serving && !webrtcStream && !frameUri && (
          <div className="rounded-xl border border-surface-800 bg-surface-900 p-4 text-sm text-surface-400">
            {frameError || "Loading frames…"}
          </div>
        )}
      </div>
    </main>
  );
}
