"use client";

/**
 * RemoteWindow3D — one floating browser-window quad inside the
 * immersive-vr scene. Streams JPEG frames from the agent's
 * /remote-runtime/sessions/<id>/webrtc/offer endpoint over a
 * WebRTC data channel, paints each frame into a CanvasTexture,
 * and forwards pointer + keyboard events back via /control.
 *
 * This is the spatial-side counterpart to the browser-window
 * runtime target in desktop/agent/remote_runtime_browser.go.
 * Drop into <VRScene> alongside TerminalPane3D — the existing
 * arc layout handles positioning.
 *
 * Per spatial constraints research: WebGL only, no HTML overlay
 * inside immersive-vr. JPEG frames decoded via off-thread
 * createImageBitmap so the render loop stays smooth.
 */

import { useEffect, useMemo, useRef, useState } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import type { BridgeConfig } from "../useAgentBridge";

interface Props {
  cfg: BridgeConfig;
  sessionId: string;
  deviceId: string;
  url?: string;
  title?: string;
  position: [number, number, number];
  rotationY: number;
  width: number;
  height: number;
  focused: boolean;
  onFocus: () => void;
}

interface FrameMeta {
  width: number;
  height: number;
}

export function RemoteWindow3D({
  cfg,
  sessionId,
  deviceId,
  url,
  title,
  position,
  rotationY,
  width,
  height,
  focused,
  onFocus,
}: Props) {
  const meshRef = useRef<THREE.Mesh>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const textureRef = useRef<THREE.CanvasTexture | null>(null);
  const pcRef = useRef<RTCPeerConnection | null>(null);
  const [status, setStatus] = useState<
    "idle" | "offering" | "streaming" | "failed" | "closed"
  >("idle");
  const [statusDetail, setStatusDetail] = useState("");
  const [meta, setMeta] = useState<FrameMeta>({ width: 1280, height: 800 });
  const { size } = useThree();

  // Bootstrap the WebRTC pipeline once per session id. We deliberately
  // negotiate WITHOUT requesting m=video so the agent picks the
  // jpegDataChannelStreamer path — that's the only one the browser
  // runtime target supports today.
  useEffect(() => {
    if (!cfg.agentUrl || !cfg.token || !sessionId) return;
    const canvas = document.createElement("canvas");
    canvas.width = 1280;
    canvas.height = 800;
    canvasRef.current = canvas;
    const tex = new THREE.CanvasTexture(canvas);
    tex.colorSpace = THREE.SRGBColorSpace;
    tex.minFilter = THREE.LinearFilter;
    tex.magFilter = THREE.LinearFilter;
    textureRef.current = tex;

    let cancelled = false;
    const pc = new RTCPeerConnection({
      iceServers: [{ urls: "stun:stun.l.google.com:19302" }],
    });
    pcRef.current = pc;

    // The agent creates `frames` (JPEG payloads) + `events` (typed
    // JSON metadata) on its side and waits for our offer. We mirror
    // the channel names so the SDP says "I expect these" — but the
    // ondatachannel handler receives them as remote-initiated.
    pc.ondatachannel = (ev) => {
      const dc = ev.channel;
      dc.binaryType = "arraybuffer";
      if (dc.label === "frames") {
        attachFramesDC(dc);
      } else if (dc.label === "events") {
        attachEventsDC(dc);
      }
    };

    pc.onconnectionstatechange = () => {
      switch (pc.connectionState) {
        case "connecting":
          setStatus("offering");
          break;
        case "connected":
          setStatus("streaming");
          break;
        case "failed":
          setStatus("failed");
          setStatusDetail("WebRTC connection failed");
          break;
        case "closed":
          setStatus("closed");
          break;
      }
    };

    const attachFramesDC = (dc: RTCDataChannel) => {
      dc.onmessage = async (msg) => {
        if (!(msg.data instanceof ArrayBuffer)) return;
        try {
          const blob = new Blob([msg.data], { type: "image/jpeg" });
          const bitmap = await createImageBitmap(blob);
          const c = canvasRef.current;
          if (!c) return;
          if (c.width !== bitmap.width || c.height !== bitmap.height) {
            c.width = bitmap.width;
            c.height = bitmap.height;
          }
          const ctx = c.getContext("2d");
          if (!ctx) return;
          ctx.drawImage(bitmap, 0, 0);
          bitmap.close?.();
          const t = textureRef.current;
          if (t) t.needsUpdate = true;
        } catch (err) {
          // A single bad frame should not kill the stream.
        }
      };
    };

    const attachEventsDC = (dc: RTCDataChannel) => {
      dc.onmessage = (msg) => {
        try {
          const payload = JSON.parse(String(msg.data));
          if (payload?.type === "dims" && payload?.width && payload?.height) {
            setMeta({
              width: Number(payload.width) || 1280,
              height: Number(payload.height) || 800,
            });
          }
        } catch {}
      };
    };

    (async () => {
      try {
        setStatus("offering");
        // We need at least one m-line in the SDP for the agent to
        // negotiate. The `events` channel covers that.
        pc.createDataChannel("init", { negotiated: false });
        const offer = await pc.createOffer({
          offerToReceiveAudio: false,
          offerToReceiveVideo: false,
        });
        await pc.setLocalDescription(offer);
        await iceGatheringComplete(pc);
        if (cancelled) return;
        const res = await fetch(
          `${cfg.agentUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/webrtc/offer`,
          {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              Authorization: `Bearer ${cfg.token}`,
            },
            body: JSON.stringify({
              sdp: pc.localDescription?.sdp,
              type: pc.localDescription?.type,
            }),
          },
        );
        if (!res.ok) {
          throw new Error(`offer ${res.status}: ${await res.text().catch(() => "")}`);
        }
        const answer = (await res.json()) as { sdp: string; type: string };
        if (cancelled) return;
        await pc.setRemoteDescription({
          type: answer.type as RTCSdpType,
          sdp: answer.sdp,
        });
      } catch (err: any) {
        if (cancelled) return;
        setStatus("failed");
        setStatusDetail(err?.message ?? "offer failed");
      }
    })();

    return () => {
      cancelled = true;
      try {
        pc.close();
      } catch {}
      try {
        textureRef.current?.dispose();
      } catch {}
      pcRef.current = null;
      canvasRef.current = null;
      textureRef.current = null;
    };
  }, [cfg.agentUrl, cfg.token, sessionId]);

  // Tick the texture once per frame so the CanvasTexture commits
  // its latest drawImage call. We don't fight VR frame pacing — the
  // RAF cadence here matches the headset's render loop.
  useFrame(() => {
    const t = textureRef.current;
    if (!t) return;
    if (t.needsUpdate) {
      // already flagged in onmessage; nothing else to do
    }
  });

  // Map a hit-test (uv-space, 0..1) to device-space pixels and POST
  // /control. The Pointer abstraction in @react-three/fiber bubbles
  // up uv coords on a Plane via `event.uv`.
  const dispatchControl = async (
    action: "tap" | "swipe" | "text" | "key" | "scroll",
    extras: Record<string, unknown>,
  ) => {
    if (!cfg.agentUrl || !cfg.token || !sessionId) return;
    try {
      await fetch(
        `${cfg.agentUrl}/remote-runtime/sessions/${encodeURIComponent(sessionId)}/control`,
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${cfg.token}`,
          },
          body: JSON.stringify({ action, ...extras }),
        },
      );
    } catch {}
  };

  // Forward keyboard events while this window has focus. Plain
  // chars become {action:"text"}; Arrows / Enter / Tab / Escape
  // become {action:"key"}.
  useEffect(() => {
    if (!focused) return;
    const onKey = (e: KeyboardEvent) => {
      const named: Record<string, string> = {
        ArrowLeft: "ArrowLeft",
        ArrowRight: "ArrowRight",
        ArrowUp: "ArrowUp",
        ArrowDown: "ArrowDown",
        Enter: "Enter",
        Tab: "Tab",
        Escape: "Escape",
        Backspace: "Backspace",
      };
      if (named[e.key]) {
        dispatchControl("key", { key: named[e.key] });
        e.preventDefault();
        return;
      }
      if (e.key.length === 1) {
        dispatchControl("text", { text: e.key });
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focused, sessionId]);

  const aspect = meta.width / Math.max(meta.height, 1);
  const planeHeight = useMemo(() => width / aspect, [width, aspect]);
  const renderHeight = Math.min(planeHeight, height);

  return (
    <group position={position} rotation={[0, rotationY, 0]}>
      <mesh
        ref={meshRef}
        onClick={(e) => {
          onFocus();
          // e.uv is in plane space (0..1, origin bottom-left in R3F).
          // Convert to device pixels (origin top-left).
          const uv = e.uv;
          if (!uv) return;
          const x = Math.round(uv.x * meta.width);
          const y = Math.round((1 - uv.y) * meta.height);
          dispatchControl("tap", { x, y });
        }}
        onWheel={(e) => {
          const uv = e.uv;
          if (!uv) return;
          const x = Math.round(uv.x * meta.width);
          const y = Math.round((1 - uv.y) * meta.height);
          dispatchControl("scroll", {
            x,
            y,
            x2: x,
            y2: y + Math.sign((e as any).deltaY ?? 0) * 60,
            durationMs: 0,
          });
        }}
      >
        <planeGeometry args={[width, renderHeight]} />
        {textureRef.current ? (
          <meshBasicMaterial
            map={textureRef.current}
            transparent
            toneMapped={false}
            side={THREE.DoubleSide}
          />
        ) : (
          <meshBasicMaterial color="#0a0e16" />
        )}
      </mesh>
      <StatusPill
        focused={focused}
        status={status}
        title={title}
        url={url}
        detail={statusDetail}
        sessionId={sessionId}
        deviceId={deviceId}
        position={[0, renderHeight / 2 + 0.04, 0]}
        width={width}
      />
    </group>
  );
}

function StatusPill({
  focused,
  status,
  title,
  url,
  detail,
  sessionId,
  deviceId,
  position,
  width,
}: {
  focused: boolean;
  status: string;
  title?: string;
  url?: string;
  detail: string;
  sessionId: string;
  deviceId: string;
  position: [number, number, number];
  width: number;
}) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const texRef = useRef<THREE.CanvasTexture | null>(null);
  useEffect(() => {
    const c = document.createElement("canvas");
    c.width = 1024;
    c.height = 64;
    canvasRef.current = c;
    const t = new THREE.CanvasTexture(c);
    t.colorSpace = THREE.SRGBColorSpace;
    texRef.current = t;
    return () => {
      t.dispose();
    };
  }, []);
  useEffect(() => {
    const c = canvasRef.current;
    if (!c) return;
    const ctx = c.getContext("2d");
    if (!ctx) return;
    ctx.clearRect(0, 0, c.width, c.height);
    ctx.fillStyle = focused
      ? "rgba(16,185,129,0.85)"
      : "rgba(8,12,20,0.85)";
    ctx.fillRect(0, 0, c.width, c.height);
    ctx.font = "20px ui-monospace, Menlo, monospace";
    ctx.textBaseline = "middle";
    ctx.fillStyle = "#e5e7eb";
    const label = title || url || sessionId;
    ctx.fillText(`[${shortStatus(status)}] ${label}`, 16, c.height / 2);
    const detailText = detail || `${deviceId}`;
    ctx.fillStyle = "#9ca3af";
    ctx.fillText(
      detailText,
      c.width - 16 - ctx.measureText(detailText).width,
      c.height / 2,
    );
    if (texRef.current) texRef.current.needsUpdate = true;
  }, [focused, status, title, url, detail, sessionId, deviceId]);

  return (
    <mesh position={position}>
      <planeGeometry args={[width, 0.06]} />
      {texRef.current ? (
        <meshBasicMaterial map={texRef.current} transparent toneMapped={false} />
      ) : (
        <meshBasicMaterial color="#0a0e16" />
      )}
    </mesh>
  );
}

function shortStatus(s: string): string {
  switch (s) {
    case "streaming":
      return "live";
    case "offering":
      return "→";
    case "failed":
      return "x";
    case "closed":
      return "—";
    default:
      return "·";
  }
}

function iceGatheringComplete(pc: RTCPeerConnection): Promise<void> {
  if (pc.iceGatheringState === "complete") return Promise.resolve();
  return new Promise((resolve) => {
    const check = () => {
      if (pc.iceGatheringState === "complete") {
        pc.removeEventListener("icegatheringstatechange", check);
        resolve();
      }
    };
    pc.addEventListener("icegatheringstatechange", check);
    // Hard cap so a flaky network can't stall the offer forever —
    // we send the offer with whatever candidates we've gathered.
    setTimeout(() => {
      pc.removeEventListener("icegatheringstatechange", check);
      resolve();
    }, 2500);
  });
}

export default RemoteWindow3D;
