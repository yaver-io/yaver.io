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
  width: number;
  height: number;
  radius: number;
  bezel: number;
  notch?: { width: number; height: number };
  punchHole?: { size: number; offsetTop: number };
  plain?: boolean;
};

const DEVICES: DeviceSkin[] = [
  { id: "iphone-15", label: "iPhone 15", width: 393, height: 852, radius: 55, bezel: 11, notch: { width: 120, height: 30 } },
  { id: "iphone-se", label: "iPhone SE", width: 375, height: 667, radius: 20, bezel: 8 },
  { id: "pixel-8", label: "Pixel 8", width: 412, height: 915, radius: 30, bezel: 9, punchHole: { size: 22, offsetTop: 16 } },
  { id: "pixel-8-pro", label: "Pixel 8 Pro", width: 448, height: 998, radius: 32, bezel: 9, punchHole: { size: 22, offsetTop: 16 } },
  { id: "tablet", label: "Tablet", width: 820, height: 1180, radius: 24, bezel: 14 },
  { id: "desktop", label: "Web", width: 0, height: 0, radius: 0, bezel: 0, plain: true },
];

const SKIN_STORAGE_KEY = "yaver_preview_skin";
const ORIENTATION_STORAGE_KEY = "yaver_preview_orientation";
const LOG_TAIL = 6;

type Orientation = "portrait" | "landscape";

type Project = {
  name: string;
  path: string;
  framework?: string;
  branch?: string;
  tags?: string[];
};

function frameworkIcon(fw?: string): string {
  const f = (fw || "").toLowerCase();
  if (f.includes("expo")) return "📱";
  if (f.includes("react-native") || f.includes("rn")) return "⚛";
  if (f.includes("flutter")) return "🦆";
  if (f.includes("next")) return "▲";
  if (f.includes("vite")) return "⚡";
  if (f === "react") return "⚛";
  return "💻";
}

function likelyFramework(project: Project): string {
  if (project.framework) return project.framework;
  const tags = (project.tags || []).map((t) => t.toLowerCase());
  if (tags.includes("expo")) return "expo";
  if (tags.includes("react-native")) return "react-native";
  if (tags.includes("flutter")) return "flutter";
  if (tags.includes("next") || tags.includes("nextjs")) return "nextjs";
  if (tags.includes("vite")) return "vite";
  return "vite";
}

function isWebPreviewFramework(framework?: string): boolean {
  const fw = (framework || "").toLowerCase();
  return (
    fw.includes("next") ||
    fw.includes("vite") ||
    fw === "react" ||
    fw.includes("expo") ||
    fw.includes("react-native")
  );
}

function previewPlatformForProject(project: Project): "web" | undefined {
  const fw = likelyFramework(project).toLowerCase();
  if (
    fw.includes("next") ||
    fw.includes("vite") ||
    fw === "react" ||
    fw.includes("expo") ||
    fw.includes("react-native")
  ) {
    return "web";
  }
  return undefined;
}

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
    port?: number;
    targetDeviceName?: string;
  } | null>(null);
  const [workerSession, setWorkerSession] = useState<MobileWorkerPreviewSession | null>(null);
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [iframeKey, setIframeKey] = useState(0);
  const [reloadNonce, setReloadNonce] = useState(0);
  const [skinId, setSkinId] = useState<string>("iphone-15");
  const [orientation, setOrientation] = useState<Orientation>("portrait");
  const [stageSize, setStageSize] = useState<{ w: number; h: number }>({ w: 0, h: 0 });
  const [shotPulse, setShotPulse] = useState(false);
  const [logLines, setLogLines] = useState<string[]>([]);
  const [showLogs, setShowLogs] = useState(true);
  const [startingPath, setStartingPath] = useState<string | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const stageRef = useRef<HTMLDivElement>(null);

  const [userPickedSkin, setUserPickedSkin] = useState(false);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const s = window.localStorage.getItem(SKIN_STORAGE_KEY);
    if (s && DEVICES.some((d) => d.id === s)) {
      setSkinId(s);
      setUserPickedSkin(true);
    }
    const o = window.localStorage.getItem(ORIENTATION_STORAGE_KEY);
    if (o === "portrait" || o === "landscape") setOrientation(o);
  }, []);

  useEffect(() => {
    if (userPickedSkin) return;
    const fw = (devStatus?.framework || "").toLowerCase();
    if (!fw) return;
    const isWeb = fw.includes("next") || fw.includes("vite") || fw === "react";
    const isMobile = fw.includes("expo") || fw.includes("react-native") || fw.includes("flutter");
    if (isWeb) setSkinId("desktop");
    else if (isMobile) setSkinId("iphone-15");
  }, [devStatus?.framework, userPickedSkin]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(SKIN_STORAGE_KEY, skinId);
  }, [skinId]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(ORIENTATION_STORAGE_KEY, orientation);
  }, [orientation]);

  // Poll dev server + worker-session status.
  useEffect(() => {
    let alive = true;
    const poll = async () => {
      try {
        const [status, session] = await Promise.all([
          agentClient.getDevServerStatus(),
          agentClient.getMobileWorkerPreviewSession(),
        ]);
        if (!alive) return;
        setDevStatus(status);
        setWorkerSession(session);
      } catch {}
    };
    poll();
    const interval = setInterval(poll, 3000);
    return () => {
      alive = false;
      clearInterval(interval);
    };
  }, []);

  // Fetch project list for the empty-state picker.
  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const rows = await agentClient.listProjects();
        if (alive) setProjects(rows as Project[]);
      } catch {
        if (alive) setProjects([]);
      }
    })();
    return () => {
      alive = false;
    };
  }, [devStatus?.running]);

  // SSE: reload on ready/reload events, also capture `line` events for the tail.
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
        let buffer = "";
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split("\n");
          buffer = lines.pop() || "";
          for (const line of lines) {
            if (!line.startsWith("data: ")) continue;
            try {
              const ev = JSON.parse(line.slice(6));
              if (ev.type === "reload" || ev.type === "ready") {
                setIframeKey((k) => k + 1);
                setReloadNonce((n) => n + 1);
              } else if (ev.type === "line" && typeof ev.text === "string") {
                setLogLines((prev) => {
                  const next = [...prev, ev.text];
                  return next.length > 200 ? next.slice(-200) : next;
                });
              } else if (ev.type === "error" && typeof ev.text === "string") {
                setLogLines((prev) => [...prev.slice(-200), `[error] ${ev.text}`]);
              }
            } catch {}
          }
        }
      } catch {}
    })();
    return () => controller.abort();
  }, [devStatus?.running]);

  // Reset logs when dev server transitions stopped → running.
  useEffect(() => {
    if (devStatus?.running) {
      setStartError(null);
    } else {
      setLogLines([]);
    }
  }, [devStatus?.running]);

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
  const previewFrameUrl = useMemo(() => {
    if (!previewUrl) return null;
    try {
      const url = new URL(previewUrl);
      url.searchParams.set("__preview_reload", String(reloadNonce));
      return url.toString();
    } catch {
      const join = previewUrl.includes("?") ? "&" : "?";
      return `${previewUrl}${join}__preview_reload=${encodeURIComponent(String(reloadNonce))}`;
    }
  }, [previewUrl, reloadNonce]);

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

  const scale = useMemo(() => {
    if (skin.plain || !frame.width || !frame.height || !stageSize.w || !stageSize.h) return 1;
    const margin = 32;
    const sx = (stageSize.w - margin) / frame.width;
    const sy = (stageSize.h - margin) / frame.height;
    return Math.min(sx, sy, 1);
  }, [skin.plain, frame.width, frame.height, stageSize.w, stageSize.h]);

  const handleReload = useCallback(async () => {
    const framework = (devStatus?.framework || "").toLowerCase();
    setIframeKey((k) => k + 1);
    setReloadNonce((n) => n + 1);
    if (isWebPreviewFramework(framework)) {
      try {
        await agentClient.reloadDevServer();
      } catch {
        // Browser preview already got a hard refresh above.
      }
      return;
    }
    await agentClient.reloadDevServer();
  }, [devStatus?.framework]);

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

  const handleStartProject = useCallback(
    async (project: Project) => {
      setStartingPath(project.path);
      setStartError(null);
      setLogLines([]);
      try {
        await agentClient.startDevServer({
          framework: likelyFramework(project),
          workDir: project.path,
          platform: previewPlatformForProject(project),
          targetDeviceId: selectedPreviewTarget?.id,
          targetDeviceName: selectedPreviewTarget?.name,
        });
        // status poll will pick up running=true shortly
      } catch (e: any) {
        setStartError(e?.message || "Failed to start dev server");
      }
      setStartingPath(null);
    },
    [selectedPreviewTarget],
  );

  const mobileProjects = useMemo(() => {
    if (!projects) return [];
    return projects.filter((p) => {
      const fw = (p.framework || "").toLowerCase();
      const tags = (p.tags || []).map((t) => t.toLowerCase());
      return (
        fw.includes("expo") ||
        fw.includes("react-native") ||
        fw.includes("flutter") ||
        tags.includes("expo") ||
        tags.includes("react-native") ||
        tags.includes("flutter")
      );
    });
  }, [projects]);

  const webProjects = useMemo(() => {
    if (!projects) return [];
    return projects.filter((p) => {
      const fw = (p.framework || "").toLowerCase();
      const tags = (p.tags || []).map((t) => t.toLowerCase());
      return (
        fw.includes("next") ||
        fw.includes("vite") ||
        fw === "react" ||
        tags.includes("next") ||
        tags.includes("nextjs") ||
        tags.includes("vite") ||
        (tags.includes("react") && !tags.includes("react-native"))
      );
    });
  }, [projects]);

  const innerDim = skin.plain
    ? { width: "100%", height: "100%" }
    : { width: `${(frame as { innerWidth: number }).innerWidth}px`, height: `${(frame as { innerHeight: number }).innerHeight}px` };

  const innerContent = devStatus?.running && previewFrameUrl ? (
    <iframe
      key={iframeKey}
      ref={iframeRef}
      src={previewFrameUrl}
      className="w-full h-full border-none bg-white"
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
    />
  ) : (
    <EmptyPhoneState
      projects={mobileProjects.length > 0 ? mobileProjects : webProjects}
      projectsAll={projects}
      onStart={handleStartProject}
      startingPath={startingPath}
      startError={startError}
    />
  );

  const tail = logLines.slice(-LOG_TAIL);

  return (
    <div className="flex flex-col h-full">
      {/* Toolbar */}
      <div className="h-9 flex items-center px-3 gap-2 border-b border-surface-800 bg-surface-900/50 shrink-0">
        <span
          className={`text-[10px] ${
            devStatus?.running ? "text-emerald-400" : "text-surface-500"
          }`}
        >
          {devStatus?.running
            ? `${frameworkIcon(devStatus.framework)} ${devStatus.framework || "dev"}${devStatus.port ? ` :${devStatus.port}` : ""}`
            : "live preview"}
        </span>
        <span className="flex-1 text-[10px] text-surface-600 font-mono truncate">
          {devStatus?.running ? devStatus.workDir || previewFrameUrl : "no dev server running"}
        </span>
        {devStatus?.running ? (
          <span className="text-[10px] text-sky-300">
            {devStatus.targetDeviceName || selectedPreviewTarget?.name || "current"}
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
              onClick={() => void handleReload()}
              className="text-surface-400 hover:text-surface-200 text-sm"
              title={isWebPreviewFramework(devStatus?.framework) ? "Refresh preview" : "Reload"}
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
            onClick={() => {
              setSkinId(d.id);
              setUserPickedSkin(true);
            }}
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

      {/* Log tail */}
      {devStatus?.running ? (
        <div className="border-t border-surface-800 bg-surface-950/80 shrink-0">
          <button
            onClick={() => setShowLogs((v) => !v)}
            className="flex w-full items-center justify-between px-3 py-1 text-[10px] uppercase tracking-widest text-surface-500 hover:text-surface-300"
          >
            <span>Dev log ({logLines.length})</span>
            <span>{showLogs ? "–" : "+"}</span>
          </button>
          {showLogs ? (
            <pre className="max-h-32 overflow-auto whitespace-pre-wrap break-all border-t border-surface-800 bg-surface-950 px-3 py-1 font-mono text-[10px] text-surface-400">
              {tail.length === 0 ? (
                <span className="text-surface-600">(waiting for output…)</span>
              ) : (
                tail.join("\n")
              )}
            </pre>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function EmptyPhoneState({
  projects,
  projectsAll,
  onStart,
  startingPath,
  startError,
}: {
  projects: Project[];
  projectsAll: Project[] | null;
  onStart: (p: Project) => void;
  startingPath: string | null;
  startError: string | null;
}) {
  return (
    <div className="w-full h-full flex flex-col gap-3 bg-surface-950 text-surface-400 p-4 overflow-auto">
      <div className="text-center mt-2">
        <div className="text-3xl opacity-30">📱</div>
        <div className="mt-1 text-xs font-medium text-surface-300">Hot reload</div>
        <div className="text-[10px] text-surface-600">
          Start a dev server to preview it live in this phone frame.
        </div>
      </div>
      {startError ? (
        <div className="rounded border border-red-500/30 bg-red-500/5 px-2 py-1 text-[10px] text-red-300">
          {startError}
        </div>
      ) : null}
      {projectsAll === null ? (
        <div className="text-center text-[10px] text-surface-600">Scanning projects…</div>
      ) : projects.length === 0 ? (
        <div className="text-center text-[10px] text-surface-600 px-4">
          No RN / Expo / Flutter / Next.js / Vite projects detected on this machine.
          <br />
          Start one manually from a shell with <code className="rounded bg-surface-900 px-1">yaver dev start</code>.
        </div>
      ) : (
        <div className="flex flex-col gap-1.5">
          {projects.slice(0, 6).map((p) => (
            <button
              key={p.path}
              onClick={() => onStart(p)}
              disabled={startingPath === p.path}
              className={`flex items-center gap-2 rounded border px-2 py-1.5 text-left transition-colors ${
                startingPath === p.path
                  ? "cursor-wait border-amber-500/30 bg-amber-500/5 text-amber-200"
                  : "border-surface-800 bg-surface-900/60 hover:border-emerald-500/30 hover:bg-emerald-500/5"
              }`}
            >
              <span className="text-sm">{frameworkIcon(p.framework)}</span>
              <div className="min-w-0 flex-1">
                <div className="truncate text-[11px] font-medium text-surface-200">{p.name}</div>
                <div className="truncate text-[9px] text-surface-600 font-mono">{p.path}</div>
              </div>
              <span className="shrink-0 text-[9px] uppercase tracking-wider text-surface-500">
                {startingPath === p.path ? "starting…" : "start"}
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
