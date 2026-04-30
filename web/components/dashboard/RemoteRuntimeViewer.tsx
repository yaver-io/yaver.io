"use client";

import { useCallback, useEffect, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";
import { agentClient, type RemoteRuntimeSession } from "@/lib/agent-client";

export default function RemoteRuntimeViewer({
  session,
  onSessionChange,
}: {
  session: RemoteRuntimeSession;
  onSessionChange: (session: RemoteRuntimeSession) => void;
}) {
  const [frameUrl, setFrameUrl] = useState<string | null>(null);
  const [viewerNote, setViewerNote] = useState<string>("Negotiating WebRTC...");
  const [text, setText] = useState("");
  const [connected, setConnected] = useState(false);
  const imgRef = useRef<HTMLImageElement | null>(null);
  const pcRef = useRef<RTCPeerConnection | null>(null);
  const objectUrlRef = useRef<string | null>(null);

  const revokeFrame = useCallback(() => {
    if (objectUrlRef.current) {
      URL.revokeObjectURL(objectUrlRef.current);
      objectUrlRef.current = null;
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    async function start() {
      revokeFrame();
      setConnected(false);
      if (session.transportMode === "relay-jpeg-poll") {
        setViewerNote("Starting relay frame polling...");
        setConnected(true);
        const pump = async () => {
          if (cancelled) return;
          try {
            const blob = await agentClient.fetchRemoteRuntimeFrame(session.id);
            if (cancelled) return;
            revokeFrame();
            const url = URL.createObjectURL(blob);
            objectUrlRef.current = url;
            setFrameUrl(url);
            setViewerNote("Relay frame polling active.");
          } catch (error) {
            if (!cancelled) setViewerNote(error instanceof Error ? error.message : String(error));
          } finally {
            if (!cancelled) window.setTimeout(pump, 900);
          }
        };
        void pump();
        return;
      }
      setViewerNote("Negotiating WebRTC...");
      const pc = new RTCPeerConnection();
      pcRef.current = pc;
      pc.onconnectionstatechange = () => {
        if (cancelled) return;
        const state = pc.connectionState;
        setConnected(state === "connected");
        setViewerNote(`Peer state: ${state}`);
      };
      pc.ondatachannel = (event) => {
        if (event.channel.label === "frames") {
          event.channel.binaryType = "arraybuffer";
          event.channel.onmessage = (msg) => {
            if (cancelled) return;
            revokeFrame();
            const blob = new Blob([msg.data], { type: "image/jpeg" });
            const url = URL.createObjectURL(blob);
            objectUrlRef.current = url;
            setFrameUrl(url);
          };
        }
        if (event.channel.label === "events") {
          event.channel.onmessage = (msg) => {
            try {
              const payload = JSON.parse(String(msg.data));
              if (payload?.session) onSessionChange(payload.session as RemoteRuntimeSession);
              if (typeof payload?.error === "string") setViewerNote(payload.error);
            } catch {
              // ignore malformed event payloads
            }
          };
        }
      };
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
      if (result.note) setViewerNote(result.note);
      await pc.setRemoteDescription(new RTCSessionDescription({
        type: (result.answer.type || "answer") as RTCSdpType,
        sdp: result.answer.sdp || "",
      }));
    }
    start().catch((error) => {
      if (!cancelled) {
        setViewerNote(error instanceof Error ? error.message : String(error));
      }
    });
    return () => {
      cancelled = true;
      revokeFrame();
      pcRef.current?.close();
      pcRef.current = null;
    };
  }, [session.id, session.transportMode, revokeFrame, onSessionChange]);

  const sendControl = useCallback(async (body: { action: "tap" | "text" | "back" | "home"; x?: number; y?: number; text?: string }) => {
    const next = await agentClient.sendRemoteRuntimeControl(session.id, body);
    onSessionChange(next);
    if (next.note) setViewerNote(next.note);
  }, [session.id, onSessionChange]);

  const handleViewerClick = useCallback(async (event: ReactMouseEvent<HTMLImageElement>) => {
    const img = imgRef.current;
    if (!img || !img.naturalWidth || !img.naturalHeight) return;
    const rect = img.getBoundingClientRect();
    const x = Math.round(((event.clientX - rect.left) / rect.width) * img.naturalWidth);
    const y = Math.round(((event.clientY - rect.top) / rect.height) * img.naturalHeight);
    await sendControl({ action: "tap", x, y });
  }, [sendControl]);

  return (
    <div className="rounded-lg border border-surface-800 bg-surface-950/80 p-3 space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div className="text-xs text-surface-400">
          {session.targetLabel} · {session.deviceId || "attaching"} · {session.transportMode || "direct-webrtc"} · {session.frameTransport || "webrtc"}
        </div>
        <div className={`text-xs ${connected ? "text-emerald-300" : "text-amber-300"}`}>{connected ? "Connected" : "Connecting"}</div>
      </div>
      <div className="rounded-lg border border-surface-800 bg-black aspect-[9/16] overflow-hidden flex items-center justify-center">
        {frameUrl ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img ref={imgRef} src={frameUrl} alt="Remote runtime frame" className="w-full h-full object-contain cursor-crosshair" onClick={(e) => void handleViewerClick(e)} />
        ) : (
          <div className="text-xs text-surface-500">{viewerNote}</div>
        )}
      </div>
      <div className="flex items-center gap-2">
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="Send text to focused field"
          className="flex-1 rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-xs text-surface-100 outline-none"
        />
        <button
          onClick={() => {
            const payload = text.trim();
            if (!payload) return;
            void sendControl({ action: "text", text: payload });
            setText("");
          }}
          className="px-3 py-2 rounded-md bg-sky-500/15 text-sky-200 text-xs hover:bg-sky-500/25"
        >
          Type
        </button>
      </div>
      <div className="flex items-center gap-2">
        {session.targetId === "android-emulator" ? (
          <>
            <button onClick={() => void sendControl({ action: "back" })} className="px-3 py-2 rounded-md bg-surface-800 text-surface-200 text-xs hover:bg-surface-700">Back</button>
            <button onClick={() => void sendControl({ action: "home" })} className="px-3 py-2 rounded-md bg-surface-800 text-surface-200 text-xs hover:bg-surface-700">Home</button>
          </>
        ) : (
          <div className="text-xs text-surface-500">Tap and text are available on iOS Simulator in this phase.</div>
        )}
      </div>
      <div className="text-xs text-surface-500">{viewerNote}</div>
    </div>
  );
}
