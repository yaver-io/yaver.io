"use client";

// RemoteRuntimeViewer — single React component that streams a remote
// device session into a <video> element and forwards pointer/text
// input back to the agent. Two media transports are supported and
// auto-negotiated:
//
//   1. webrtc-rtp-h264-v1 (preferred) — the agent attaches a Pion
//      H.264 video track. The browser's hardware decoder paints it
//      directly into <video srcObject>. Sub-200 ms latency on LAN.
//      Used whenever the agent reports it can encode for the target
//      (Android emulator with adb today; iOS once Phase 4 lands).
//
//   2. webrtc-datachannel-jpeg-v1 (fallback) — old behavior: the
//      agent ships JPEG bytes on a "frames" DataChannel and the
//      viewer renders them through an <img> blob URL. Kept alive so
//      iOS sessions keep working until the fragmented-MP4 parser
//      lands.
//
// The browser doesn't pick — the AGENT picks based on what's in our
// offer SDP and whether it can encode. We always offer m=video; the
// agent decides. The negotiated transport is reported back on the
// answer's session.frameTransport, and we render accordingly.

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { agentClient, type RemoteRuntimeSession } from "@/lib/agent-client";

type DeviceDims = {
  width: number;
  height: number;
  scale?: number;
  rotation?: "portrait" | "landscape";
};

export default function RemoteRuntimeViewer({
  session,
  onSessionChange,
}: {
  session: RemoteRuntimeSession;
  onSessionChange: (session: RemoteRuntimeSession) => void;
}) {
  // --- transport-agnostic UI state ---------------------------------------
  const [viewerNote, setViewerNote] = useState("Negotiating WebRTC...");
  const [textInput, setTextInput] = useState("");
  const [connected, setConnected] = useState(false);
  // Tracks which transport actually got picked (so the UI can show
  // "RTP H.264" or "JPEG-DC fallback" rather than guess).
  const [transport, setTransport] = useState<string>(session.frameTransport ?? "");

  // --- device dims ------------------------------------------------------
  // Initial value comes from the session payload (set on Attach). The
  // agent also broadcasts a `dims` event on the events channel right
  // after PC connects — that's what keeps rotation in sync. We trust
  // whichever arrived more recently.
  const [dims, setDims] = useState<DeviceDims | null>(session.deviceDims ?? null);

  // --- refs for the two transports + the events channel ------------------
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const imgRef = useRef<HTMLImageElement | null>(null);
  const pcRef = useRef<RTCPeerConnection | null>(null);
  const eventsRef = useRef<RTCDataChannel | null>(null);
  const heartbeatRef = useRef<number | null>(null);
  // For the legacy JPEG-DC path — track the current blob URL so we
  // can revoke it before swapping in the next frame. Without this
  // we'd leak ~80 KB per frame at 1.4 FPS = a slow but real OOM.
  const jpegUrlRef = useRef<string | null>(null);

  const revokeJpeg = useCallback(() => {
    if (jpegUrlRef.current) {
      URL.revokeObjectURL(jpegUrlRef.current);
      jpegUrlRef.current = null;
    }
  }, []);

  // --- pointer drag detection -------------------------------------------
  // Press-and-release at the same spot → tap. Press, drag, release →
  // swipe. We coalesce intermediate pointermove events into the swipe
  // path's start/end so we only send one `swipe` action per gesture.
  const dragStartRef = useRef<{ x: number; y: number; t: number } | null>(null);

  // --- helpers -----------------------------------------------------------
  const sendEvent = useCallback((payload: unknown) => {
    const ch = eventsRef.current;
    if (!ch || ch.readyState !== "open") return;
    try {
      ch.send(typeof payload === "string" ? payload : JSON.stringify(payload));
    } catch {
      // Channel may have closed between the readyState check and
      // send — don't escalate, the viewer will reconnect on the
      // next state change.
    }
  }, []);

  // Convert a viewer-pixel coordinate (relative to the rendered
  // surface) into device space using the dims event. Falls back to
  // the natural width/height of the underlying media element when
  // dims are missing — keeps clicks roughly working even if the
  // dims event was dropped.
  const toDeviceCoord = useCallback(
    (clientX: number, clientY: number): { x: number; y: number } | null => {
      const surface = videoRef.current ?? imgRef.current;
      if (!surface) return null;
      const rect = surface.getBoundingClientRect();
      if (rect.width === 0 || rect.height === 0) return null;
      let dWidth = dims?.width;
      let dHeight = dims?.height;
      if (!dWidth || !dHeight) {
        if (videoRef.current) {
          dWidth = videoRef.current.videoWidth;
          dHeight = videoRef.current.videoHeight;
        } else if (imgRef.current) {
          dWidth = imgRef.current.naturalWidth;
          dHeight = imgRef.current.naturalHeight;
        }
      }
      if (!dWidth || !dHeight) return null;
      const x = Math.round(((clientX - rect.left) / rect.width) * dWidth);
      const y = Math.round(((clientY - rect.top) / rect.height) * dHeight);
      return { x, y };
    },
    [dims],
  );

  const sendControl = useCallback(
    async (body: Parameters<typeof agentClient.sendRemoteRuntimeControl>[1]) => {
      try {
        const next = await agentClient.sendRemoteRuntimeControl(session.id, body);
        onSessionChange(next);
        if (next.note) setViewerNote(next.note);
      } catch (err) {
        setViewerNote(err instanceof Error ? err.message : String(err));
      }
    },
    [session.id, onSessionChange],
  );

  // --- pointer handlers --------------------------------------------------
  const onPointerDown = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>) => {
      const c = toDeviceCoord(e.clientX, e.clientY);
      if (!c) return;
      dragStartRef.current = { x: c.x, y: c.y, t: Date.now() };
      // Capture the pointer so we still see move/up if the user
      // drags outside the video element.
      (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
    },
    [toDeviceCoord],
  );

  const onPointerUp = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>) => {
      const start = dragStartRef.current;
      dragStartRef.current = null;
      const end = toDeviceCoord(e.clientX, e.clientY);
      if (!start || !end) return;
      const dx = end.x - start.x;
      const dy = end.y - start.y;
      const distSq = dx * dx + dy * dy;
      // Threshold: 8 device pixels is roughly the smallest gesture a
      // user means as "drag" rather than "I tapped sloppily".
      const dragThreshold = 8 * 8;
      if (distSq < dragThreshold) {
        void sendControl({ action: "tap", x: start.x, y: start.y });
        return;
      }
      const durationMs = Date.now() - start.t;
      void sendControl({
        action: "swipe",
        x: start.x,
        y: start.y,
        x2: end.x,
        y2: end.y,
        durationMs: Math.max(80, durationMs),
      });
    },
    [sendControl, toDeviceCoord],
  );

  // --- core lifecycle ----------------------------------------------------
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      revokeJpeg();
      setConnected(false);
      setTransport(session.frameTransport ?? "");

      // Relay-jpeg-poll mode bypasses WebRTC entirely: the viewer
      // GETs a JPEG every ~900 ms. Keeps working as a last resort
      // when ICE can't punch through (corp WiFi blocking UDP, etc.).
      if (session.transportMode === "relay-jpeg-poll") {
        setViewerNote("Starting relay frame polling...");
        setConnected(true);
        const pump = async () => {
          if (cancelled) return;
          try {
            const blob = await agentClient.fetchRemoteRuntimeFrame(session.id);
            if (cancelled) return;
            revokeJpeg();
            const url = URL.createObjectURL(blob);
            jpegUrlRef.current = url;
            if (imgRef.current) imgRef.current.src = url;
            setTransport("relay-jpeg-poll-v1");
            setViewerNote("Relay frame polling active.");
          } catch (err) {
            if (!cancelled) setViewerNote(err instanceof Error ? err.message : String(err));
          } finally {
            if (!cancelled) window.setTimeout(pump, 900);
          }
        };
        void pump();
        return;
      }

      setViewerNote("Negotiating WebRTC...");
      // Pull ICE servers (STUN + optional TURN) from the agent.
      // Agent always returns at least one STUN entry; TURN is added
      // when the operator has set YAVER_TURN_URL on the agent host
      // and the relay was started with --turn-port. If the fetch
      // fails we fall back to a hardcoded public STUN so the session
      // doesn't get stuck in offer-creation forever.
      let iceServers: RTCIceServer[] = [{ urls: "stun:stun.l.google.com:19302" }];
      try {
        const creds = await agentClient.fetchRemoteRuntimeTurnCredentials();
        if (creds.iceServers.length > 0) iceServers = creds.iceServers;
      } catch {
        /* fall through to default STUN */
      }
      if (cancelled) return;
      const pc = new RTCPeerConnection({ iceServers });
      pcRef.current = pc;

      // Ensure the offer has an SCTP m-line. The agent creates the
      // "frames" and "events" channels from its side; without a local
      // channel here, video-only offers cannot negotiate those channels
      // when the agent falls back to JPEG data-channel streaming.
      pc.createDataChannel("primer");

      // Always offer a video transceiver. The agent inspects the
      // SDP for m=video and decides whether to attach an H.264
      // track. If the agent can't encode (or it's an old binary),
      // it falls back to JPEG-DC and we just won't get an ontrack
      // — the framesDC path picks up the slack.
      pc.addTransceiver("video", { direction: "recvonly" });

      pc.ontrack = (event) => {
        if (cancelled) return;
        const stream = event.streams[0];
        if (videoRef.current && stream) {
          videoRef.current.srcObject = stream;
          // Autoplay policies: must be muted on Chrome to start
          // automatically. The viewer never plays audio anyway.
          videoRef.current.muted = true;
          void videoRef.current.play().catch(() => {
            /* user gesture required — handled by tap on the surface */
          });
        }
      };

      pc.onconnectionstatechange = () => {
        if (cancelled) return;
        const state = pc.connectionState;
        setConnected(state === "connected");
        setViewerNote(`Peer state: ${state}`);
        if (state === "connected") {
          // Heartbeat — agent uses this to know the viewer is alive
          // for the TeamViewer-style takeover semantics.
          if (heartbeatRef.current === null) {
            heartbeatRef.current = window.setInterval(() => {
              sendEvent({ type: "ping", ts: Date.now() });
            }, 5000) as unknown as number;
          }
        }
      };

      pc.ondatachannel = (event) => {
        const ch = event.channel;
        if (ch.label === "events") {
          eventsRef.current = ch;
          ch.onmessage = (msg) => {
            try {
              const payload = JSON.parse(String(msg.data));
              if (payload?.type === "dims" || payload?.type === "rotation") {
                setDims({
                  width: payload.width,
                  height: payload.height,
                  scale: payload.scale,
                  rotation: payload.rotation,
                });
              }
              if (payload?.type === "ready" && payload.transport) {
                setTransport(String(payload.transport));
              }
              if (payload?.type === "throttle") {
                setViewerNote(`throttled · ${payload.reason ?? "rtp"}`);
              }
              if (payload?.type === "taken-over") {
                setViewerNote("Session taken over by another viewer.");
                pc.close();
              }
              if (payload?.session) {
                onSessionChange(payload.session as RemoteRuntimeSession);
              }
              if (typeof payload?.error === "string") setViewerNote(payload.error);
            } catch {
              /* ignore malformed event payloads */
            }
          };
        }
        if (ch.label === "frames") {
          // Legacy JPEG-DC transport. Each message is one full JPEG.
          ch.binaryType = "arraybuffer";
          ch.onmessage = (msg) => {
            if (cancelled) return;
            revokeJpeg();
            const blob = new Blob([msg.data as ArrayBuffer], { type: "image/jpeg" });
            const url = URL.createObjectURL(blob);
            jpegUrlRef.current = url;
            if (imgRef.current) imgRef.current.src = url;
          };
        }
      };

      // Build + send the offer.
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      const local = pc.localDescription;
      if (!local) throw new Error("Missing local WebRTC offer.");
      const result = await agentClient.createRemoteRuntimeWebRTCAnswer(session.id, {
        type: local.type,
        sdp: local.sdp,
      });
      if (cancelled) return;
      onSessionChange(result.session);
      if (result.transport) setTransport(result.transport);
      if (result.note) setViewerNote(result.note);
      // Pick up the dims that the agent stamped on the session
      // payload immediately — the events-channel `dims` will follow
      // shortly but the user shouldn't see a brief mis-scaled tap
      // window in between.
      if (result.session.deviceDims) setDims(result.session.deviceDims);
      await pc.setRemoteDescription(
        new RTCSessionDescription({
          type: (result.answer.type || "answer") as RTCSdpType,
          sdp: result.answer.sdp || "",
        }),
      );
    })().catch((err) => {
      if (!cancelled) setViewerNote(err instanceof Error ? err.message : String(err));
    });

    return () => {
      cancelled = true;
      if (heartbeatRef.current !== null) {
        window.clearInterval(heartbeatRef.current);
        heartbeatRef.current = null;
      }
      revokeJpeg();
      if (videoRef.current) {
        try {
          (videoRef.current.srcObject as MediaStream | null)?.getTracks?.().forEach((t) => t.stop());
        } catch {
          /* defensive */
        }
        videoRef.current.srcObject = null;
      }
      pcRef.current?.close();
      pcRef.current = null;
      eventsRef.current = null;
    };
  }, [session.id, session.transportMode, session.frameTransport, session.deviceDims, onSessionChange, revokeJpeg, sendEvent]);

  // --- derived state for the UI ------------------------------------------
  const transportLabel = useMemo(() => {
    if (!transport) return "negotiating";
    if (transport.startsWith("webrtc-rtp-h264")) return "RTP H.264";
    if (transport.startsWith("webrtc-datachannel-jpeg")) return "JPEG-DC";
    if (transport.startsWith("relay-jpeg-poll")) return "Relay JPEG";
    return transport;
  }, [transport]);

  const isRTP = transport.startsWith("webrtc-rtp-h264");

  const aspectRatio = dims
    ? `${dims.width} / ${dims.height}`
    : session.platform === "android"
    ? "9 / 19.5"
    : "9 / 19.5";

  return (
    <div className="rounded-lg border border-surface-800 bg-surface-950/80 p-3 space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div className="text-xs text-surface-400">
          {session.targetLabel} · {session.deviceId || "attaching"} ·{" "}
          {dims ? `${dims.width}×${dims.height}` : "—"} · {transportLabel}
        </div>
        <div className={`text-xs ${connected ? "text-emerald-700 dark:text-emerald-300" : "text-amber-700 dark:text-amber-300"}`}>
          {connected ? "Connected" : "Connecting"}
        </div>
      </div>

      {/* Surface — wraps both <video> and <img> so the pointer
          handlers work the same regardless of which transport
          painted the picture. We use display:contents-equivalent
          stacking so only one is visible at a time. */}
      <div
        className="relative rounded-lg border border-surface-800 bg-black overflow-hidden mx-auto select-none touch-none"
        style={{ aspectRatio, maxHeight: "70vh", maxWidth: "100%" }}
        onPointerDown={onPointerDown}
        onPointerUp={onPointerUp}
        onPointerCancel={() => {
          dragStartRef.current = null;
        }}
      >
        {/* RTP path: <video srcObject> filled by ontrack. Hidden
            (display:none) when JPEG-DC is the active transport so the
            stale srcObject doesn't bleed through. */}
        <video
          ref={videoRef}
          className={`absolute inset-0 w-full h-full object-contain ${
            isRTP ? "block" : "hidden"
          }`}
          autoPlay
          playsInline
          muted
        />
        {/* JPEG path: <img> filled by frames DataChannel or relay
            poll. Hidden when RTP wins. */}
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img
          ref={imgRef}
          alt=""
          className={`absolute inset-0 w-full h-full object-contain ${
            isRTP ? "hidden" : "block"
          }`}
        />
        {!connected && (
          <div className="absolute inset-0 flex items-center justify-center text-xs text-surface-500 pointer-events-none">
            {viewerNote}
          </div>
        )}
      </div>

      <div className="flex items-center gap-2">
        <input
          value={textInput}
          onChange={(e) => setTextInput(e.target.value)}
          placeholder="Send text to focused field"
          className="flex-1 rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-xs text-surface-100 outline-none"
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              const payload = textInput.trim();
              if (!payload) return;
              void sendControl({ action: "text", text: payload });
              setTextInput("");
            }
          }}
        />
        <button
          onClick={() => {
            const payload = textInput.trim();
            if (!payload) return;
            void sendControl({ action: "text", text: payload });
            setTextInput("");
          }}
          className="px-3 py-2 rounded-md bg-sky-500/15 text-sky-700 dark:text-sky-200 text-xs hover:bg-sky-500/25"
        >
          Type
        </button>
      </div>

      <div className="flex items-center gap-2 flex-wrap">
        {session.targetId === "android-emulator" ? (
          <>
            <button
              onClick={() => void sendControl({ action: "back" })}
              className="px-3 py-2 rounded-md bg-surface-800 text-surface-200 text-xs hover:bg-surface-700"
              title="KEYCODE_BACK (4)"
            >
              ◁ Back
            </button>
            <button
              onClick={() => void sendControl({ action: "home" })}
              className="px-3 py-2 rounded-md bg-surface-800 text-surface-200 text-xs hover:bg-surface-700"
              title="KEYCODE_HOME (3)"
            >
              ⌂ Home
            </button>
            <button
              onClick={() => void sendControl({ action: "key", key: "recents" })}
              className="px-3 py-2 rounded-md bg-surface-800 text-surface-200 text-xs hover:bg-surface-700"
              title="KEYCODE_APP_SWITCH (187)"
            >
              ▣ Recents
            </button>
            <button
              onClick={() => void sendControl({ action: "key", key: "menu" })}
              className="px-3 py-2 rounded-md bg-surface-800 text-surface-200 text-xs hover:bg-surface-700"
              title="KEYCODE_MENU (82)"
            >
              ☰ Menu
            </button>
            <span className="mx-1 text-surface-700">·</span>
            <button
              onClick={() => void sendControl({ action: "key", key: "volume_down" })}
              className="px-2 py-2 rounded-md bg-surface-800 text-surface-200 text-xs hover:bg-surface-700"
              title="KEYCODE_VOLUME_DOWN (25)"
            >
              ▾ Vol-
            </button>
            <button
              onClick={() => void sendControl({ action: "key", key: "volume_up" })}
              className="px-2 py-2 rounded-md bg-surface-800 text-surface-200 text-xs hover:bg-surface-700"
              title="KEYCODE_VOLUME_UP (24)"
            >
              ▴ Vol+
            </button>
            <button
              onClick={() => void sendControl({ action: "key", key: "power" })}
              className="px-3 py-2 rounded-md bg-surface-800 text-surface-200 text-xs hover:bg-surface-700"
              title="KEYCODE_POWER (26) — toggles screen on/off"
            >
              ⏻ Power
            </button>
          </>
        ) : (
          <div className="text-xs text-surface-500">
            iOS Simulator hardware keys (home / sleep / volume) land in a follow-up phase — tap, swipe, and text already work.
          </div>
        )}
      </div>

      <div className="text-xs text-surface-500">{viewerNote}</div>
    </div>
  );
}
