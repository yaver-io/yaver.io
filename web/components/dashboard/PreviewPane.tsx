"use client";

import { useState, useEffect, useRef, useMemo, useCallback } from "react";
import { agentClient, type MobileWorkerPreviewSession } from "@/lib/agent-client";

interface PreviewTarget {
  id: string;
  name: string;
}

type DeviceSkin = {
  id: string;
  label: string;
  // Logical viewport dimensions (portrait).
  width: number;
  height: number;
  radius: number;
  bezel: number;
  notch?: { width: number; height: number };
  punchHole?: { size: number; offsetTop: number };
  // Pure desktop / no-chrome fallback.
  plain?: boolean;
};

const DEVICES: DeviceSkin[] = [
  { id: "iphone-15", label: "iPhone 15", width: 393, height: 852, radius: 55, bezel: 11, notch: { width: 120, height: 30 } },
  { id: "iphone-se", label: "iPhone SE", width: 375, height: 667, radius: 20, bezel: 8 },
  { id: "pixel-8", label: "Pixel 8", width: 412, height: 915, radius: 30, bezel: 9, punchHole: { size: 22, offsetTop: 16 } },
  { id: "pixel-8-pro", label: "Pixel 8 Pro", width: 448, height: 998, radius: 32, bezel: 9, punchHole: { size: 22, offsetTop: 16 } },
  { id: "tablet", label: "Tablet", width: 820, height: 1180, radius: 24, bezel: 14 },
  { id: "desktop", label: "Desktop", width: 0, height: 0, radius: 0, bezel: 0, plain: true },
];

const SKIN_STORAGE_KEY = "yaver_preview_skin";
const ORIENTATION_STORAGE_KEY = "yaver_preview_orientation";

type Orientation = "portrait" | "landscape";

export default function PreviewPane({
  selectedPreviewTarget,
  onSelectPreviewTarget,
  mobileWorkers,
}: {
  selectedPreviewTarget: PreviewTarget | null;
  onSelectPreviewTarget: (deviceId: string | null) => void;
  mobileWorkers: PreviewTarget[];
}) {
  const [devStatus, setDevStatus] = useState<{
    running: boolean;
    framework?: string;
    workDir?: string;
    targetDeviceName?: string;
  } | null>(null);
  const [workerSession, setWorkerSession] = useState<MobileWorkerPreviewSession | null>(null);
  const [iframeKey, setIframeKey] = useState(0);
  const [skinId, setSkinId] = useState<string>("iphone-15");
  const [orientation, setOrientation] = useState<Orientation>("portrait");
  const [stageSize, setStageSize] = useState<{ w: number; h: number }>({ w: 0, h: 0 });
  const [shotPulse, setShotPulse] = useState(false);
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const stageRef = useRef<HTMLDivElement>(null);

  // Restore persisted skin/orientation.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const s = window.localStorage.getItem(SKIN_STORAGE_KEY);
    if (s && DEVICES.some((d) => d.id === s)) setSkinId(s);
    const o = window.localStorage.getItem(ORIENTATION_STORAGE_KEY);
    if (o === "portrait" || o === "landscape") setOrientation(o);
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(SKIN_STORAGE_KEY, skinId);
  }, [skinId]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(ORIENTATION_STORAGE_KEY, orientation);
  }, [orientation]);

  useEffect(() => {
    const poll = async () => {
      try {
        const [status, session] = await Promise.all([
          agentClient.getDevServerStatus(),
          agentClient.getMobileWorkerPreviewSession(),
        ]);
        setDevStatus(status);
        setWorkerSession(session);
      } catch {}
    };
    poll();
    const interval = setInterval(poll, 3000);
    return () => clearInterval(interval);
  }, []);

  // SSE for live reload from dev server.
  useEffect(() => {
    if (!devStatus?.running) return;
    const previewUrl = agentClient.devPreviewUrl;
    if (!previewUrl) return;

    const controller = new AbortController();
    (async () => {
      try {
        const eventsUrl = agentClient.devEventsUrl;
        if (!eventsUrl) return;
        const res = await fetch(eventsUrl, {
          headers: agentClient.getAuthHeaders(),
          signal: controller.signal,
        });
        const reader = res.body?.getReader();
        if (!reader) return;
        const decoder = new TextDecoder();
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          for (const line of decoder.decode(value).split("\n")) {
            if (line.startsWith("data: ")) {
              try {
                const ev = JSON.parse(line.slice(6));
                if (ev.type === "reload" || ev.type === "ready") {
                  setIframeKey((k) => k + 1);
                }
              } catch {}
            }
          }
        }
      } catch {}
    })();
    return () => controller.abort();
  }, [devStatus?.running]);

  // Observe stage size for fit-scaling.
  useEffect(() => {
    const el = stageRef.current;
    if (!el) return;
    const measure = () => {
      const rect = el.getBoundingClientRect();
      setStageSize({ w: rect.width, h: rect.height });
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const skin = useMemo(() => DEVICES.find((d) => d.id === skinId) ?? DEVICES[0], [skinId]);
  const previewUrl = agentClient.devPreviewUrl;

  // Outer frame dimensions after orientation.
  const frame = useMemo(() => {
    if (skin.plain) return { width: 0, height: 0 };
    const w = orientation === "portrait" ? skin.width : skin.height;
    const h = orientation === "portrait" ? skin.height : skin.width;
    return {
      width: w + skin.bezel * 2,
      height: h + skin.bezel * 2,
      innerWidth: w,
      innerHeight: h,
    } as { width: number; height: number; innerWidth: number; innerHeight: number };
  }, [skin, orientation]);

  // Fit-scale the device frame inside the stage.
  const scale = useMemo(() => {
    if (skin.plain || !frame.width || !frame.height || !stageSize.w || !stageSize.h) return 1;
    const margin = 32; // breathing room
    const sx = (stageSize.w - margin) / frame.width;
    const sy = (stageSize.h - margin) / frame.height;
    return Math.min(sx, sy, 1);
  }, [skin.plain, frame.width, frame.height, stageSize.w, stageSize.h]);

  const handleReload = useCallback(() => {
    setIframeKey((k) => k + 1);
    agentClient.reloadDevServer();
  }, []);

  const handleStop = useCallback(async () => {
    await agentClient.stopDevServer();
    setDevStatus(null);
  }, []);

  const handleRequestScreenshot = useCallback(async () => {
    const ok = await agentClient.sendMobileWorkerPreviewCommand("capture_screenshot", {
      reason: "preview-control-plane",
    });
    if (ok) {
      setShotPulse(true);
      setTimeout(() => setShotPulse(false), 1200);
    }
  }, []);

  const innerDim = skin.plain
    ? { width: "100%", height: "100%" }
    : { width: `${(frame as { innerWidth: number }).innerWidth}px`, height: `${(frame as { innerHeight: number }).innerHeight}px` };

  // Render the inner screen area: iframe when running, empty-state chrome otherwise.
  const innerContent = devStatus?.running && previewUrl ? (
    <iframe
      key={iframeKey}
      ref={iframeRef}
      src={previewUrl}
      className="w-full h-full border-none bg-white"
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
    />
  ) : (
    <div className="w-full h-full flex flex-col items-center justify-center gap-2 bg-surface-950 text-surface-500">
      <div className="text-3xl opacity-30">&#x1F3A8;</div>
      <div className="text-xs">No dev server running</div>
      <div className="text-[10px] text-surface-600 max-w-[200px] text-center leading-relaxed px-4">
        Start one from Projects or ask the AI to build something
      </div>
    </div>
  );

  return (
    <div className="flex flex-col h-full">
      {/* Toolbar */}
      <div className="h-9 flex items-center px-3 gap-2 border-b border-surface-800 bg-surface-900/50 shrink-0">
        <span className="flex-1 text-[10px] text-surface-500 font-mono truncate">
          {previewUrl || "preview pane"}
        </span>
        {devStatus?.framework ? (
          <span className="text-[10px] text-emerald-400">{devStatus.framework}</span>
        ) : null}
        {devStatus?.running ? (
          <span className="text-[10px] text-sky-300">
            {devStatus.targetDeviceName || selectedPreviewTarget?.name || "current device"}
          </span>
        ) : null}
        {workerSession?.hasTarget ? (
          <span className={`text-[10px] ${workerSession.workerOnline ? "text-emerald-400" : "text-amber-400"}`}>
            {workerSession.workerOnline ? "worker online" : "worker offline"}
          </span>
        ) : null}
        {workerSession?.hasTarget && workerSession.workerOnline ? (
          <button
            onClick={handleRequestScreenshot}
            className={`text-xs ${shotPulse ? "text-emerald-400" : "text-surface-400 hover:text-surface-200"}`}
            title="Request screenshot from selected worker"
          >
            Shot
          </button>
        ) : null}
        {devStatus?.running ? (
          <>
            <button
              onClick={handleReload}
              className="text-surface-400 hover:text-surface-200 text-sm"
              title="Reload"
            >
              &#x21BB;
            </button>
            <button
              onClick={handleStop}
              className="text-red-400 hover:text-red-300 text-sm"
              title="Stop"
            >
              &#x25A0;
            </button>
          </>
        ) : null}
      </div>

      {/* Target picker (mobile workers) */}
      {mobileWorkers.length > 0 && (
        <div className="flex items-center gap-2 px-3 py-2 border-b border-surface-800 bg-surface-950/60 overflow-x-auto">
          <span className="text-[10px] uppercase tracking-widest text-surface-500 shrink-0">Target</span>
          <button
            onClick={() => onSelectPreviewTarget(null)}
            className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
              !selectedPreviewTarget
                ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
                : "border-surface-800 text-surface-500"
            }`}
          >
            Current device
          </button>
          {mobileWorkers.map((device) => (
            <button
              key={device.id}
              onClick={() => onSelectPreviewTarget(device.id)}
              className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
                selectedPreviewTarget?.id === device.id
                  ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
                  : "border-surface-800 text-surface-500"
              }`}
            >
              {device.name}
            </button>
          ))}
        </div>
      )}

      {/* Skin + orientation picker */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-surface-800 bg-surface-950/60 overflow-x-auto">
        <span className="text-[10px] uppercase tracking-widest text-surface-500 shrink-0">Device</span>
        {DEVICES.map((d) => (
          <button
            key={d.id}
            onClick={() => setSkinId(d.id)}
            className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
              skinId === d.id
                ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
                : "border-surface-800 text-surface-500 hover:text-surface-300"
            }`}
            title={d.plain ? "No chrome, full pane" : `${d.width}×${d.height}`}
          >
            {d.label}
          </button>
        ))}
        {!skin.plain ? (
          <>
            <span className="mx-1 text-surface-700">·</span>
            <button
              onClick={() => setOrientation("portrait")}
              className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
                orientation === "portrait"
                  ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
                  : "border-surface-800 text-surface-500 hover:text-surface-300"
              }`}
              title="Portrait"
            >
              &#x2B15;
            </button>
            <button
              onClick={() => setOrientation("landscape")}
              className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
                orientation === "landscape"
                  ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
                  : "border-surface-800 text-surface-500 hover:text-surface-300"
              }`}
              title="Landscape"
            >
              &#x25AD;
            </button>
            <span className="ml-2 text-[10px] text-surface-600 font-mono">
              {orientation === "portrait" ? `${skin.width}×${skin.height}` : `${skin.height}×${skin.width}`}
              {scale < 1 ? ` · ${Math.round(scale * 100)}%` : ""}
            </span>
          </>
        ) : null}
      </div>

      {/* Stage */}
      <div
        ref={stageRef}
        className="flex-1 min-h-0 flex items-center justify-center overflow-hidden bg-surface-950"
      >
        {skin.plain ? (
          <div style={innerDim}>{innerContent}</div>
        ) : (
          <div
            style={{
              width: frame.width,
              height: frame.height,
              transform: `scale(${scale})`,
              transformOrigin: "center center",
            }}
            className="relative"
          >
            {/* Bezel */}
            <div
              style={{
                width: frame.width,
                height: frame.height,
                borderRadius: skin.radius + skin.bezel,
                background:
                  "linear-gradient(140deg, #1a1a1a 0%, #0d0d0d 50%, #1a1a1a 100%)",
                boxShadow:
                  "inset 0 0 0 1px rgba(255,255,255,0.06), 0 30px 60px -20px rgba(0,0,0,0.7), 0 10px 30px -10px rgba(0,0,0,0.5)",
                padding: skin.bezel,
              }}
            >
              {/* Screen */}
              <div
                style={{
                  width: (frame as { innerWidth: number }).innerWidth,
                  height: (frame as { innerHeight: number }).innerHeight,
                  borderRadius: skin.radius,
                  overflow: "hidden",
                  position: "relative",
                  background: "#000",
                }}
              >
                {innerContent}
                {/* Notch (iPhone 15). Hide in landscape — notch shifts to side, too fiddly for v1. */}
                {skin.notch && orientation === "portrait" ? (
                  <div
                    style={{
                      position: "absolute",
                      top: 6,
                      left: "50%",
                      transform: "translateX(-50%)",
                      width: skin.notch.width,
                      height: skin.notch.height,
                      borderRadius: skin.notch.height,
                      background: "#000",
                      zIndex: 2,
                      pointerEvents: "none",
                    }}
                  />
                ) : null}
                {/* Punch hole (Pixel). */}
                {skin.punchHole && orientation === "portrait" ? (
                  <div
                    style={{
                      position: "absolute",
                      top: skin.punchHole.offsetTop,
                      left: "50%",
                      transform: "translateX(-50%)",
                      width: skin.punchHole.size,
                      height: skin.punchHole.size,
                      borderRadius: skin.punchHole.size,
                      background: "#000",
                      zIndex: 2,
                      pointerEvents: "none",
                    }}
                  />
                ) : null}
                {/* Home indicator (iOS) */}
                {skin.notch && orientation === "portrait" ? (
                  <div
                    style={{
                      position: "absolute",
                      bottom: 8,
                      left: "50%",
                      transform: "translateX(-50%)",
                      width: 134,
                      height: 4,
                      borderRadius: 2,
                      background: "rgba(255,255,255,0.5)",
                      zIndex: 2,
                      pointerEvents: "none",
                      mixBlendMode: "difference",
                    }}
                  />
                ) : null}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
