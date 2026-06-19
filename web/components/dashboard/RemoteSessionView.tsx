"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

type RemoteSessionStatus = {
  running?: boolean;
  url?: string;
  title?: string;
  lastError?: string;
  screen?: { running?: boolean; hasFrame?: boolean; fps?: number };
};

type InputEvent = {
  type: "move" | "click" | "double" | "drag" | "scroll" | "text" | "key";
  nx?: number;
  ny?: number;
  tonx?: number;
  tony?: number;
  button?: "left" | "right" | "middle";
  dx?: number;
  dy?: number;
  text?: string;
  keys?: string[];
};

const NAMED_KEYS: Record<string, string> = {
  Enter: "enter",
  Backspace: "backspace",
  Tab: "tab",
  " ": "space",
  Escape: "esc",
  Delete: "del",
  ArrowUp: "up",
  ArrowDown: "down",
  ArrowLeft: "left",
  ArrowRight: "right",
  Home: "home",
  End: "end",
  PageUp: "pageup",
  PageDown: "pagedown",
};

export default function RemoteSessionView({ devices, token }: { devices: Device[]; token: string | null }) {
  const [deviceId, setDeviceId] = useState("");
  const [url, setUrl] = useState("");
  const [status, setStatus] = useState<RemoteSessionStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [rtcOn, setRtcOn] = useState(false);
  const [rtcAudio, setRtcAudio] = useState(true);
  const [rtcQuality, setRtcQuality] = useState<"auto" | "high" | "balanced" | "saver">("auto");
  const [controlEnabled, setControlEnabled] = useState(false);
  const [linkState, setLinkState] = useState<string | null>(null);

  const clientRef = useRef<AgentClient | null>(null);
  const connectedTo = useRef("");
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const pcRef = useRef<RTCPeerConnection | null>(null);
  const overlayRef = useRef<HTMLDivElement | null>(null);
  const queue = useRef<InputEvent[]>([]);
  const flushTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const dragStart = useRef<{ nx: number; ny: number; button: "left" | "right" | "middle" } | null>(null);
  const dragged = useRef(false);
  const lastMove = useRef(0);
  const audioDeviceRef = useRef("");

  const ensureClient = useCallback(async (id: string): Promise<AgentClient | null> => {
    const device = devices.find((d) => d.id === id);
    if (!device || !token) return null;
    if (clientRef.current && connectedTo.current === id) return clientRef.current;
    try { clientRef.current?.disconnect(); } catch {}
    clientRef.current = null;
    connectedTo.current = "";
    const client = new AgentClient();
    client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
    const tunnelUrls = Array.from(new Set([...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []), ...(device.tunnelUrl ? [device.tunnelUrl] : [])]));
    await client.connect(device.host, device.port, token, device.id, { tunnelUrls });
    clientRef.current = client;
    connectedTo.current = id;
    return client;
  }, [devices, token]);

  const callOps = useCallback(async (verb: string, payload: Record<string, unknown> = {}) => {
    const client = await ensureClient(deviceId);
    if (!client) return { ok: false, error: "not connected" };
    const res = await client.callOps(verb, payload);
    if (res?.ok === false) return { ok: false, error: res.error || res.code || "failed" };
    return res.initial ?? res;
  }, [deviceId, ensureClient]);

  const refreshStatus = useCallback(async () => {
    if (!deviceId) return;
    const r = await callOps("remote_session_status");
    if (r?.ok === false) {
      setMsg(r.error || "status failed");
      return;
    }
    setStatus(r);
  }, [callOps, deviceId]);

  const stopWebRTC = useCallback(() => {
    try { pcRef.current?.close(); } catch {}
    pcRef.current = null;
    if (videoRef.current) videoRef.current.srcObject = null;
    setRtcOn(false);
    setLinkState(null);
  }, []);

  const startWebRTC = useCallback(async () => {
    stopWebRTC();
    setMsg(null);
    try {
      const client = await ensureClient(deviceId);
      if (!client) return;
      let iceServers: RTCIceServer[] = [];
      try {
        const iceRes = await client.agentFetch("/stream/webrtc/ice");
        if (iceRes.ok) iceServers = (await iceRes.json())?.iceServers || [];
      } catch {}
      const pc = new RTCPeerConnection({ iceServers });
      pcRef.current = pc;
      pc.addTransceiver("video", { direction: "recvonly" });
      if (rtcAudio) {
        if (!audioDeviceRef.current) {
          try {
            const devices = await callOps("audio_devices");
            audioDeviceRef.current = devices?.devices?.[0]?.alsaDevice || "default";
          } catch {
            audioDeviceRef.current = "default";
          }
        }
        pc.addTransceiver("audio", { direction: "recvonly" });
      }
      pc.onconnectionstatechange = () => setLinkState(pc.connectionState);
      pc.ontrack = (ev) => {
        if (videoRef.current) videoRef.current.srcObject = ev.streams[0];
      };
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      await waitForIce(pc);
      const vw = videoRef.current?.clientWidth || window.innerWidth;
      const vh = videoRef.current?.clientHeight || Math.round((vw * 9) / 16);
      const net = (navigator as any)?.connection?.effectiveType || "";
      const res = await client.agentFetch("/stream/webrtc/offer", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          source: "screen",
          sdp: pc.localDescription?.sdp,
          deviceClass: "web",
          w: Math.round(vw),
          h: Math.round(vh),
          net,
          profile: rtcQuality === "auto" ? "balanced" : rtcQuality,
          audioDevice: rtcAudio ? audioDeviceRef.current : "",
        }),
      });
      if (!res.ok) throw new Error(`offer ${res.status}`);
      const ans = await res.json();
      await pc.setRemoteDescription({ type: "answer", sdp: ans.sdp });
      setRtcOn(true);
    } catch (e: any) {
      setMsg(e?.message || "WebRTC failed");
      stopWebRTC();
    }
  }, [callOps, deviceId, ensureClient, rtcAudio, rtcQuality, stopWebRTC]);

  useEffect(() => () => stopWebRTC(), [stopWebRTC]);

  useEffect(() => {
    if (!deviceId) return;
    void refreshStatus();
    const id = setInterval(refreshStatus, 4000);
    return () => clearInterval(id);
  }, [deviceId, refreshStatus]);

  const startSession = useCallback(async () => {
    setBusy(true);
    setMsg(null);
    try {
      const r = await callOps("remote_session_start", { url: url.trim(), fps: 8 });
      if (r?.ok === false) throw new Error(r.error || "start failed");
      setStatus(r);
      await startWebRTC();
    } catch (e: any) {
      setMsg(e?.message || "start failed");
    } finally {
      setBusy(false);
    }
  }, [callOps, startWebRTC, url]);

  const stopSession = useCallback(async () => {
    setBusy(true);
    setMsg(null);
    try {
      stopWebRTC();
      const r = await callOps("remote_session_stop", { stopStream: true });
      if (r?.ok === false) throw new Error(r.error || "stop failed");
      setStatus(r);
    } catch (e: any) {
      setMsg(e?.message || "stop failed");
    } finally {
      setBusy(false);
    }
  }, [callOps, stopWebRTC]);

  const toggleControl = useCallback(async () => {
    setBusy(true);
    setMsg(null);
    try {
      const client = await ensureClient(deviceId);
      if (!client) return;
      const next = !controlEnabled;
      const res = await client.agentFetch("/rd/policy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ controlEnabled: next }),
      });
      if (!res.ok) throw new Error(`policy ${res.status}`);
      setControlEnabled(next);
      if (next) overlayRef.current?.focus();
    } catch (e: any) {
      setMsg(e?.message || "control failed");
    } finally {
      setBusy(false);
    }
  }, [controlEnabled, deviceId, ensureClient]);

  const flush = useCallback(async () => {
    flushTimer.current = null;
    if (queue.current.length === 0) return;
    const events = queue.current;
    queue.current = [];
    try {
      const client = await ensureClient(deviceId);
      if (!client) return;
      const res = await client.agentFetch("/rd/input", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ events }),
      });
      if (res.status === 403) setControlEnabled(false);
    } catch {}
  }, [deviceId, ensureClient]);

  const enqueue = useCallback((ev: InputEvent, immediate = false) => {
    queue.current.push(ev);
    if (immediate) {
      if (flushTimer.current) clearTimeout(flushTimer.current);
      flushTimer.current = null;
      void flush();
      return;
    }
    if (!flushTimer.current) flushTimer.current = setTimeout(() => void flush(), 40);
  }, [flush]);

  const norm = useCallback((e: { clientX: number; clientY: number }) => {
    const el = overlayRef.current;
    if (!el) return { nx: 0, ny: 0 };
    const r = el.getBoundingClientRect();
    return {
      nx: Math.min(1, Math.max(0, (e.clientX - r.left) / Math.max(1, r.width))),
      ny: Math.min(1, Math.max(0, (e.clientY - r.top) / Math.max(1, r.height))),
    };
  }, []);

  const onPointerMove = useCallback((e: React.PointerEvent) => {
    if (!controlEnabled) return;
    const now = Date.now();
    if (now - lastMove.current < 45) return;
    lastMove.current = now;
    const { nx, ny } = norm(e);
    if (dragStart.current) dragged.current = true;
    enqueue({ type: "move", nx, ny });
  }, [controlEnabled, enqueue, norm]);

  const onPointerDown = useCallback((e: React.PointerEvent) => {
    if (!controlEnabled) return;
    overlayRef.current?.focus();
    const { nx, ny } = norm(e);
    const button = e.button === 2 ? "right" : e.button === 1 ? "middle" : "left";
    dragStart.current = { nx, ny, button };
    dragged.current = false;
    try { (e.target as HTMLElement).setPointerCapture(e.pointerId); } catch {}
  }, [controlEnabled, norm]);

  const onPointerUp = useCallback((e: React.PointerEvent) => {
    if (!controlEnabled) return;
    const { nx, ny } = norm(e);
    const start = dragStart.current;
    dragStart.current = null;
    if (start && dragged.current) {
      enqueue({ type: "drag", nx: start.nx, ny: start.ny, tonx: nx, tony: ny, button: start.button }, true);
    } else {
      const button = e.button === 2 ? "right" : e.button === 1 ? "middle" : "left";
      enqueue({ type: "click", nx, ny, button }, true);
    }
    dragged.current = false;
  }, [controlEnabled, enqueue, norm]);

  const onKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (!controlEnabled) return;
    const mods: string[] = [];
    if (e.ctrlKey) mods.push("ctrl");
    if (e.altKey) mods.push("alt");
    if (e.metaKey) mods.push("cmd");
    const named = NAMED_KEYS[e.key];
    if (named) {
      e.preventDefault();
      if (e.shiftKey) mods.push("shift");
      enqueue({ type: "key", keys: [...mods, named] }, true);
      return;
    }
    if (e.key.length === 1) {
      e.preventDefault();
      if (mods.length > 0) {
        if (e.shiftKey) mods.push("shift");
        enqueue({ type: "key", keys: [...mods, e.key.toLowerCase()] }, true);
      } else {
        enqueue({ type: "text", text: e.key }, true);
      }
    }
  }, [controlEnabled, enqueue]);

  const btn = "rounded-md border border-surface-700 bg-surface-800 px-3 py-2 text-sm text-surface-100 hover:bg-surface-700 disabled:opacity-40";
  const accent = "rounded-md bg-brand px-3 py-2 text-sm font-semibold text-surface-950 hover:bg-brand/90 disabled:opacity-40";

  if (!deviceId) {
    return (
      <div className="mx-auto max-w-4xl space-y-4 p-4">
        <div>
          <h2 className="text-lg font-semibold text-surface-100">Remote Session</h2>
          <p className="text-sm text-surface-500">Pick the Yaver device that will run the browser.</p>
        </div>
        <div className="grid gap-2 md:grid-cols-2">
          {devices.map((d) => (
            <button key={d.id} onClick={() => setDeviceId(d.id)} className="rounded-lg border border-surface-800 bg-surface-900 px-4 py-3 text-left hover:border-surface-600">
              <div className="font-medium text-surface-100">{d.name || d.id}</div>
              <div className="mt-1 text-xs text-surface-500">{(d as any).online ? "online" : "last seen device"}</div>
            </button>
          ))}
        </div>
        {devices.length === 0 ? <p className="text-sm text-surface-500">No devices found.</p> : null}
      </div>
    );
  }

  const selected = devices.find((d) => d.id === deviceId);

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold text-surface-100">Remote Session</h2>
          <p className="text-xs text-surface-500">{selected?.name || deviceId}</p>
        </div>
        <button className={btn} onClick={() => setDeviceId("")}>Switch device</button>
      </div>

      <div className="flex flex-wrap gap-2 rounded-lg border border-surface-800 bg-surface-900 p-3">
        <input
          className="min-w-[260px] flex-1 rounded-md border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="https://teams.microsoft.com/..."
        />
        <button className={accent} disabled={busy || !url.trim()} onClick={startSession}>Start</button>
        <button className={btn} disabled={busy} onClick={() => void startWebRTC()}>{rtcOn ? "Reconnect" : "View"}</button>
        <button className={btn} disabled={busy} onClick={toggleControl}>{controlEnabled ? "Control on" : "Control off"}</button>
        <button className={btn} disabled={busy} onClick={stopSession}>Stop</button>
      </div>

      <div className="flex flex-wrap items-center gap-2 text-xs">
        <button onClick={() => setRtcAudio((v) => !v)} className={`rounded px-2 py-1 ${rtcAudio ? "bg-emerald-600 text-white" : "border border-surface-700 text-surface-300"}`}>Audio {rtcAudio ? "on" : "off"}</button>
        {(["auto", "high", "balanced", "saver"] as const).map((q) => (
          <button key={q} onClick={() => setRtcQuality(q)} className={`rounded px-2 py-1 ${rtcQuality === q ? "bg-brand text-surface-950" : "border border-surface-700 text-surface-300"}`}>{q}</button>
        ))}
        {linkState ? <span className="text-surface-500">link {linkState}</span> : null}
        {status?.running ? <span className="text-emerald-400">running</span> : <span className="text-surface-500">idle</span>}
        {status?.screen?.hasFrame ? <span className="text-surface-500">frames ready</span> : null}
      </div>

      {msg || status?.lastError ? (
        <div className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-200">{msg || status?.lastError}</div>
      ) : null}

      <div className="relative min-h-[260px] flex-1 overflow-hidden rounded-lg border border-surface-800 bg-black">
        {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
        <video ref={videoRef} autoPlay playsInline muted={!rtcAudio} className="h-full w-full object-contain" />
        <div
          ref={overlayRef}
          tabIndex={0}
          onPointerMove={onPointerMove}
          onPointerDown={onPointerDown}
          onPointerUp={onPointerUp}
          onDoubleClick={(e) => { if (controlEnabled) { const p = norm(e); enqueue({ type: "double", ...p, button: "left" }, true); } }}
          onWheel={(e) => {
            if (!controlEnabled) return;
            const dy = e.deltaY > 0 ? -1 : e.deltaY < 0 ? 1 : 0;
            const dx = e.deltaX > 0 ? -1 : e.deltaX < 0 ? 1 : 0;
            if (dx || dy) enqueue({ type: "scroll", dx, dy }, true);
          }}
          onKeyDown={onKeyDown}
          className={`absolute inset-0 outline-none ${controlEnabled ? "cursor-crosshair" : "pointer-events-none"}`}
          onContextMenu={(e) => e.preventDefault()}
        />
      </div>

      <div className="truncate text-xs text-surface-500">
        {status?.title || status?.url || "Start a session to open a managed browser on the selected device."}
      </div>
    </div>
  );
}

function waitForIce(pc: RTCPeerConnection): Promise<void> {
  return new Promise((resolve) => {
    if (pc.iceGatheringState === "complete") return resolve();
    const check = () => {
      if (pc.iceGatheringState === "complete") {
        pc.removeEventListener("icegatheringstatechange", check);
        resolve();
      }
    };
    pc.addEventListener("icegatheringstatechange", check);
    setTimeout(resolve, 2000);
  });
}
