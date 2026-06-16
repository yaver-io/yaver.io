"use client";

// AppleTVCellView — control an Apple TV + watch the home capture card from the
// web dashboard over the relay (web is relay-only by design). Mirrors
// ArmCellView's transport (ensureClient + callOps) and RemoteDesktopView's
// MJPEG-in-<img> streaming. Control + now-playing are always-legal; the capture
// view streams whatever the card provides, as-is (Yaver is a neutral tool — like
// OBS; what you stream and the right to it is your responsibility).
import { useCallback, useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

type NowPlaying = {
  title?: string | null;
  artist?: string | null;
  app?: string | null;
  state?: string;
  position?: number | null;
  total?: number | null;
  artwork_b64?: string;
  mimetype?: string;
};
type CaptureStatus = { running?: boolean; device?: string; fps?: number; hasFrame?: boolean; blackHint?: string; ffmpeg?: boolean; error?: string };
type PairedATV = { identifier: string; name: string; address: string; default?: boolean; protocols?: string[] };

type RemoteKey =
  | "up" | "down" | "left" | "right" | "select" | "menu" | "home"
  | "play" | "pause" | "stop" | "next" | "previous" | "play_pause";

const APP_SHORTCUTS = [
  { label: "TV", bundle: "com.apple.TVAppLive" },
  { label: "Music", bundle: "com.apple.TVMusic" },
  { label: "Podcasts", bundle: "com.apple.podcasts" },
  { label: "Settings", bundle: "com.apple.TVSettings" },
];

export default function AppleTVCellView({ devices, token }: { devices: Device[]; token: string | null }) {
  const [deviceId, setDeviceId] = useState("");
  const [paired, setPaired] = useState<PairedATV[]>([]);
  const [np, setNp] = useState<NowPlaying | null>(null);
  const [cap, setCap] = useState<CaptureStatus | null>(null);
  const [captureUrl, setCaptureUrl] = useState<string | null>(null);
  const [watchUrl, setWatchUrl] = useState("");
  const [shareUrl, setShareUrl] = useState<string | null>(null);
  const [rtcOn, setRtcOn] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const videoElRef = useRef<HTMLVideoElement | null>(null);
  const pcRef = useRef<RTCPeerConnection | null>(null);

  const clientRef = useRef<AgentClient | null>(null);
  const connectedTo = useRef("");
  const liveRef = useRef(true);

  const ensureClient = useCallback(
    async (id: string): Promise<AgentClient | null> => {
      const device = devices.find((d) => d.id === id);
      if (!device || !token) return null;
      if (clientRef.current && connectedTo.current === id) return clientRef.current;
      try {
        clientRef.current?.disconnect();
      } catch {}
      clientRef.current = null;
      connectedTo.current = "";
      const client = new AgentClient();
      client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
      const tunnelUrls = Array.from(new Set([...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []), ...(device.tunnelUrl ? [device.tunnelUrl] : [])]));
      await client.connect(device.host, device.port, token, device.id, { tunnelUrls });
      clientRef.current = client;
      connectedTo.current = id;
      return client;
    },
    [devices, token],
  );

  const callOps = useCallback(
    async (verb: string, payload: Record<string, unknown> = {}): Promise<any> => {
      try {
        const client = await ensureClient(deviceId);
        if (!client) return { ok: false, error: "not connected" };
        const res = await client.callOps(verb, payload);
        if (res?.ok === false) return { ok: false, code: res.code, error: res.error };
        return (res as any)?.initial ?? res;
      } catch (e: any) {
        setMsg(e?.message || "connection failed");
        return { ok: false, error: e?.message || "failed" };
      }
    },
    [deviceId, ensureClient],
  );

  const refreshNowPlaying = useCallback(async () => {
    const r = await callOps("appletv_now_playing");
    if (liveRef.current && r && !r.code) setNp(r);
  }, [callOps]);

  const refreshCapture = useCallback(async () => {
    const r = await callOps("capture_status");
    if (liveRef.current) setCap(r);
  }, [callOps]);

  // load paired list + start polling on device pick
  useEffect(() => {
    if (!deviceId) return;
    liveRef.current = true;
    (async () => {
      const l = await callOps("appletv_list");
      if (l?.devices) setPaired(l.devices);
      refreshNowPlaying();
      refreshCapture();
    })();
    const id = setInterval(refreshNowPlaying, 2500);
    return () => {
      liveRef.current = false;
      clearInterval(id);
    };
  }, [deviceId]); // eslint-disable-line

  // W4: live now-playing via SSE (EventSource), on top of the poll fallback.
  useEffect(() => {
    if (!deviceId) return;
    let es: EventSource | null = null;
    let cancelled = false;
    (async () => {
      try {
        const client = await ensureClient(deviceId);
        if (!client || cancelled) return;
        const url = await client.nowPlayingStreamUrl();
        if (cancelled) return;
        es = new EventSource(url);
        es.onmessage = (ev) => {
          try {
            const data = JSON.parse(ev.data);
            if (data && typeof data === "object") setNp((prev) => ({ ...prev, ...data }));
          } catch {}
        };
        es.onerror = () => { es?.close(); }; // poll keeps it fresh
      } catch {
        /* SSE optional — poll covers it */
      }
    })();
    return () => {
      cancelled = true;
      es?.close();
    };
  }, [deviceId, ensureClient]);

  const run = useCallback(
    async (fn: () => Promise<any>) => {
      setBusy(true);
      setMsg(null);
      const r = await fn();
      if (r?.ok === false) setMsg(r.error || r.code || "failed");
      await refreshNowPlaying();
      setBusy(false);
      return r;
    },
    [refreshNowPlaying],
  );

  const key = (k: RemoteKey) => () => run(() => callOps("appletv_remote_key", { key: k }));

  const toggleCapture = useCallback(async () => {
    setBusy(true);
    try {
      if (cap?.running) {
        await callOps("capture_stop");
        setCaptureUrl(null);
      } else {
        await callOps("capture_start", { fps: 6 });
        const client = await ensureClient(deviceId);
        if (client) setCaptureUrl(await client.captureStreamUrl());
      }
      await refreshCapture();
    } catch (e: any) {
      setMsg(e?.message || "capture failed");
    } finally {
      setBusy(false);
    }
  }, [cap, callOps, ensureClient, deviceId, refreshCapture]);

  // Mint a VIEW-ONLY watch link: a stream-scoped token (server-side, reaches
  // only stream_* verbs) packed with this device's connection info + relay
  // config into the URL hash, so a friend can watch with no login. Snapshot-poll
  // only — the token can't reach controls.
  const createShareLink = useCallback(async () => {
    setBusy(true);
    setMsg(null);
    try {
      const device = devices.find((d) => d.id === deviceId);
      if (!device) return;
      const r = await callOps("stream_share", { ttlHours: 24 });
      if (!r?.token) {
        setMsg(r?.error || "couldn't mint share token");
        return;
      }
      const blob = {
        d: {
          id: device.id,
          host: device.host,
          port: device.port,
          publicEndpoints: (device as any).publicEndpoints,
          tunnelUrl: (device as any).tunnelUrl,
        },
        r: agentClient.configuredRelayServers.map((s) => ({ ...s })),
        t: r.token,
      };
      const packed = btoa(encodeURIComponent(JSON.stringify(blob)));
      const url = `${window.location.origin}/watch#${packed}`;
      setShareUrl(url);
      try {
        await navigator.clipboard.writeText(url);
      } catch {}
    } finally {
      setBusy(false);
    }
  }, [devices, deviceId, callOps]);

  // Real-time WebRTC viewer (M15): offer/answer via the agent's
  // /stream/webrtc/offer; the agent answers with an H264 track fed by `source`.
  // Sub-second vs. the snapshot/MJPEG paths. (Same-network/relay-with-TURN.)
  const stopWebRTC = useCallback(() => {
    try { pcRef.current?.close(); } catch {}
    pcRef.current = null;
    if (videoElRef.current) videoElRef.current.srcObject = null;
    setRtcOn(false);
  }, []);

  const startWebRTC = useCallback(async (source: string) => {
    setMsg(null);
    stopWebRTC();
    try {
      const client = await ensureClient(deviceId);
      if (!client) return;
      const pc = new RTCPeerConnection();
      pcRef.current = pc;
      pc.addTransceiver("video", { direction: "recvonly" });
      pc.ontrack = (e) => {
        if (videoElRef.current) videoElRef.current.srcObject = e.streams[0];
      };
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      // non-trickle: wait for our ICE gathering to finish before signaling
      await new Promise<void>((resolve) => {
        if (pc.iceGatheringState === "complete") return resolve();
        const check = () => { if (pc.iceGatheringState === "complete") { pc.removeEventListener("icegatheringstatechange", check); resolve(); } };
        pc.addEventListener("icegatheringstatechange", check);
        setTimeout(resolve, 2000); // fallback
      });
      const res = await client.agentFetch("/stream/webrtc/offer", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ source, sdp: pc.localDescription?.sdp }),
      });
      if (!res.ok) throw new Error(`offer ${res.status}`);
      const ans = await res.json();
      await pc.setRemoteDescription({ type: "answer", sdp: ans.sdp });
      setRtcOn(true);
    } catch (e: any) {
      setMsg(e?.message || "WebRTC failed");
      stopWebRTC();
    }
  }, [deviceId, ensureClient, stopWebRTC]);

  useEffect(() => () => stopWebRTC(), [stopWebRTC]);

  const btn = "rounded-md px-3 py-1.5 text-sm border border-neutral-700 bg-neutral-800 text-neutral-100 hover:bg-neutral-700 disabled:opacity-40";
  const btnAccent = "rounded-md px-3 py-1.5 text-sm bg-indigo-600 text-white hover:bg-indigo-500 disabled:opacity-40";
  const pad = "flex h-14 w-14 items-center justify-center rounded-xl border border-neutral-700 bg-neutral-800 text-lg text-neutral-100 hover:bg-neutral-700 disabled:opacity-40";

  if (!deviceId) {
    return (
      <div className="space-y-3">
        <h2 className="text-lg font-semibold text-neutral-100">Apple TV — pick the device</h2>
        <p className="text-sm text-neutral-400">Pick the box running the Apple TV engine (your home Pi).</p>
        {devices.map((d) => (
          <button key={d.id} onClick={() => setDeviceId(d.id)} className="flex w-full items-center justify-between rounded-lg border border-neutral-800 bg-neutral-900 px-4 py-3 text-left hover:border-neutral-600">
            <span className="font-medium text-neutral-100">{d.name || d.id}</span>
            <span className="text-xs text-neutral-500">{(d as any).online ? "online" : "offline"}</span>
          </button>
        ))}
        {devices.length === 0 && <p className="text-sm text-neutral-500">No devices yet.</p>}
      </div>
    );
  }

  const artworkUri = np?.artwork_b64 ? `data:${np.mimetype || "image/jpeg"};base64,${np.artwork_b64}` : null;

  return (
    <div className="space-y-4">
      {/* header */}
      <div className="flex items-center justify-between rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <div>
          <div className="font-semibold text-neutral-100">Apple TV</div>
          <div className="text-xs text-neutral-400">{paired.length ? `${paired.length} paired` : "none paired — run `yaver appletv pair` on the box"}</div>
        </div>
        <button className={btn} onClick={() => setDeviceId("")}>Switch</button>
      </div>

      {/* now playing */}
      <div className="flex items-center gap-3 rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        {artworkUri ? (
          <img src={artworkUri} alt="artwork" className="h-16 w-16 rounded-md object-cover" />
        ) : (
          <div className="h-16 w-16 rounded-md bg-neutral-800" />
        )}
        <div className="min-w-0 flex-1">
          <div className="truncate font-semibold text-neutral-100">{np?.title || "Nothing playing"}</div>
          <div className="truncate text-xs text-neutral-400">{[np?.artist, np?.app].filter(Boolean).join(" · ") || "—"}</div>
          {!!np?.state && <div className="text-[11px] text-neutral-500">{np.state}</div>}
        </div>
      </div>

      {/* remote */}
      <div className="rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <div className="mb-3 text-sm font-semibold text-neutral-200">Remote</div>
        <div className="flex flex-col items-center gap-2">
          <button className={pad} disabled={busy} onClick={key("up")}>▲</button>
          <div className="flex items-center gap-2">
            <button className={pad} disabled={busy} onClick={key("left")}>◀</button>
            <button className="flex h-14 w-14 items-center justify-center rounded-full bg-indigo-600 text-sm font-bold text-white hover:bg-indigo-500 disabled:opacity-40" disabled={busy} onClick={key("select")}>OK</button>
            <button className={pad} disabled={busy} onClick={key("right")}>▶</button>
          </div>
          <button className={pad} disabled={busy} onClick={key("down")}>▼</button>
        </div>
        <div className="mt-3 flex flex-wrap justify-center gap-2">
          <button className={btn} disabled={busy} onClick={key("menu")}>Menu</button>
          <button className={btn} disabled={busy} onClick={key("home")}>Home</button>
          <button className={btn} disabled={busy} onClick={() => run(() => callOps("appletv_power", { state: "off" }))}>Power</button>
        </div>
        <div className="mt-3 flex justify-center gap-2">
          <button className={btn} disabled={busy} onClick={key("previous")}>⏮</button>
          <button className={btnAccent} disabled={busy} onClick={key("play_pause")}>⏯</button>
          <button className={btn} disabled={busy} onClick={key("next")}>⏭</button>
        </div>
      </div>

      {/* apps */}
      <div className="rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <div className="mb-3 text-sm font-semibold text-neutral-200">Apps</div>
        <div className="flex flex-wrap gap-2">
          {APP_SHORTCUTS.map((a) => (
            <button key={a.bundle} className={btn} disabled={busy} onClick={() => run(() => callOps("appletv_launch_app", { bundle_id: a.bundle }))}>{a.label}</button>
          ))}
        </div>
      </div>

      {/* capture card */}
      <div className="rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <div className="mb-3 flex items-center justify-between">
          <div className="text-sm font-semibold text-neutral-200">Home camera / capture</div>
          <button className={cap?.running ? btn : btnAccent} disabled={busy} onClick={toggleCapture}>{cap?.running ? "Stop" : "Start"}</button>
        </div>
        {cap?.running && captureUrl ? (
          <div className="flex aspect-video items-center justify-center overflow-hidden rounded-lg border border-neutral-800 bg-black">
            <img src={captureUrl} alt="capture" className="max-h-full max-w-full object-contain" />
          </div>
        ) : (
          <p className="text-sm text-neutral-500">{cap?.ffmpeg === false ? "ffmpeg not installed on this box." : "Stopped. Start to stream a capture card (satellite box, console, camera, PC…)."}</p>
        )}
        {cap?.blackHint && <p className="mt-2 text-xs text-neutral-500">{cap.blackHint}</p>}
      </div>

      {/* real-time WebRTC viewer (low latency) */}
      <div className="rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <div className="mb-2 flex items-center justify-between">
          <div className="text-sm font-semibold text-neutral-200">Live (WebRTC, low-latency)</div>
          <div className="flex gap-2">
            {rtcOn ? (
              <button className={btn} onClick={stopWebRTC}>Stop</button>
            ) : (
              <>
                <button className={btnAccent} onClick={() => startWebRTC("capture")}>Capture</button>
                <button className={btn} onClick={() => startWebRTC("scene")}>Scene</button>
                <button className={btn} onClick={() => startWebRTC("screen")}>Screen</button>
              </>
            )}
          </div>
        </div>
        <div className="flex aspect-video items-center justify-center overflow-hidden rounded-md border border-neutral-800 bg-black">
          {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
          <video ref={videoElRef} autoPlay playsInline muted className="max-h-full max-w-full" />
        </div>
        <p className="mt-2 text-xs text-neutral-500">Sub-second vs. snapshot/MJPEG. Same-network now; remote needs TURN. Needs ffmpeg on the box.</p>
      </div>

      {/* watch a URL on the box (magara) */}
      <div className="rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <div className="mb-2 text-sm font-semibold text-neutral-200">Open a video on this box</div>
        <div className="flex gap-2">
          <input
            className="flex-1 rounded-md border border-neutral-700 bg-neutral-950 px-3 py-2 text-sm text-neutral-100"
            value={watchUrl}
            onChange={(e) => setWatchUrl(e.target.value)}
            placeholder="https://youtube.com/watch?v=…"
          />
          <button
            className={btnAccent}
            disabled={busy || !watchUrl.trim()}
            onClick={() => run(() => callOps("screen_watch", { url: watchUrl.trim() }))}
          >
            Open
          </button>
        </div>
        <p className="mt-2 text-xs text-neutral-500">Opens in the box's browser; watch via Remote Desktop. Streams the screen as-is.</p>
      </div>

      {/* share a view-only watch link (guest / friend, no login) */}
      <div className="rounded-lg border border-neutral-800 bg-neutral-900 p-4">
        <div className="mb-2 flex items-center justify-between">
          <div className="text-sm font-semibold text-neutral-200">Share a view-only link</div>
          <button className={btnAccent} disabled={busy} onClick={createShareLink}>Create link</button>
        </div>
        <p className="text-xs text-neutral-500">A 24-hour, view-only link (live frames + now-playing, no controls). The recipient needs no account.</p>
        {shareUrl && (
          <input
            readOnly
            value={shareUrl}
            onFocus={(e) => e.currentTarget.select()}
            className="mt-2 w-full rounded-md border border-neutral-700 bg-neutral-950 px-3 py-2 text-xs text-neutral-300"
          />
        )}
      </div>

      {busy && <p className="text-xs text-neutral-500">working…</p>}
      {!!msg && <p className="text-sm text-rose-400">{msg}</p>}
    </div>
  );
}
