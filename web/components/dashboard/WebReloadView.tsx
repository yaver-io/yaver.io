"use client";

// WebReloadView — left-nav tab dedicated to iframing web apps from
// the connected dev machine. Sibling of VibeCodingView; the two
// share agent-client but deliberately have different layouts.
//
// Layout:
//   ┌─ header: device badge + app picker + start/stop ─────────────┐
//   │ ┌─ body ───────────────────────────────────────────────────┐ │
//   │ │                                                          │ │
//   │ │       WebPreviewFrame (boxed browser chrome)            │ │
//   │ │                                                          │ │
//   │ │ ───────────────────────────────────────────────────────  │ │
//   │ │   logs (collapsible)                                     │ │
//   │ └──────────────────────────────────────────────────────────┘ │
//   └──────────────────────────────────────────────────────────────┘

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { agentClient, type WorkspaceAppView } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";
import { WebAppSelector } from "./WebAppSelector";
import { WebPreviewFrame, WEB_PREVIEW_VIEWPORTS, type ViewportId } from "./WebPreviewFrame";

interface ProjectRow {
  name: string;
  path: string;
  framework?: string;
  branch?: string;
  tags?: string[];
}

interface Props {
  connectedDevice: Device | null;
  connState: string;
  preferredProjectPath?: string | null;
  /** Hand the same repair handler the Hot Reload tab uses — POSTs
   *  /settings/repair-relay on Convex, re-syncs the user's password
   *  with the platform default. When present, WebReloadView auto-
   *  repairs on the first "invalid relay password" and exposes a
   *  manual button. When absent, the button is hidden. */
  onRepairRelay?: () => Promise<{ repaired: boolean; reason: string }>;
  onReconnect?: () => Promise<void>;
}

export function WebReloadView({ connectedDevice, connState, preferredProjectPath, onRepairRelay, onReconnect }: Props) {
  const [apps, setApps] = useState<WorkspaceAppView[]>([]);
  const [projects, setProjects] = useState<ProjectRow[]>([]);
  const [workspaceLoading, setWorkspaceLoading] = useState(false);
  const [projectsLoading, setProjectsLoading] = useState(false);
  const [workspaceError, setWorkspaceError] = useState<string | null>(null);
  const [selectedApp, setSelectedApp] = useState<string | null>(null);
  const [selectedProjectPath, setSelectedProjectPath] = useState<string | null>(null);
  const [devStatus, setDevStatus] = useState<Awaited<ReturnType<typeof agentClient.getDevServerStatus>>>(null);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);
  const [logs, setLogs] = useState<string[]>([]);
  const [relayRepairState, setRelayRepairState] = useState<"idle" | "repairing" | "repaired" | "failed">("idle");
  const [relayRepairMsg, setRelayRepairMsg] = useState<string | null>(null);
  const [recovering, setRecovering] = useState(false);
  const [recoveryLog, setRecoveryLog] = useState<string[]>([]);
  const [recoveryProgress, setRecoveryProgress] = useState<{ pct: number; stage: string; active: boolean }>({ pct: 0, stage: "", active: false });
  // Stop UX state — drives the "Stopping…" / "Stopped ✓" feedback after
  // /dev/stop. agent 1.99.93+ returns verified + buildsCancelled so we
  // can show the user the agent really stopped, not just "the request
  // was acknowledged". Banner self-dismisses after 2.5s.
  const [stopState, setStopState] = useState<"idle" | "stopping" | "stopped" | "error">("idle");
  const [stopMessage, setStopMessage] = useState("");
  const [stopBuildsCancelled, setStopBuildsCancelled] = useState(0);
  // Sibling Expo Web preview — spawned on demand so RN/Expo projects
  // can render in the browser iframe without touching Metro (which
  // keeps serving Hermes bundles to the phone). Status.webPort is
  // authoritative; these flags drive the CTA state.
  const [webPreviewStarting, setWebPreviewStarting] = useState(false);
  const [webPreviewError, setWebPreviewError] = useState<string | null>(null);
  // Tick a counter while the Expo Web sibling is bundling so the
  // progress UI shows time elapsed. Reset on start, freeze on done.
  const [webPreviewElapsedSec, setWebPreviewElapsedSec] = useState(0);
  useEffect(() => {
    if (!webPreviewStarting) return;
    setWebPreviewElapsedSec(0);
    const id = setInterval(() => setWebPreviewElapsedSec((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [webPreviewStarting]);

  // Static web bundle (target=web-js-bundle) — alternative to the
  // live-HMR sibling. Compiles once via `expo export -p web` and serves
  // through /dev/web-bundle/. Doesn't depend on the long-running Expo
  // Web sibling process being alive — survives agent restarts and
  // re-clones cleanly. Tracks the post-compile transport pipeline via
  // SSE webview/transport events.
  const [staticBundleState, setStaticBundleState] = useState<"idle" | "building" | "ready" | "failed">("idle");
  const [staticBundleError, setStaticBundleError] = useState<string | null>(null);
  const [staticBundleInfo, setStaticBundleInfo] = useState<{ size: number; fileCount: number } | null>(null);
  const [staticBundleTransport, setStaticBundleTransport] = useState<{
    phase: string;
    pct: number;
    done: number;
    total: number;
    file?: string;
  } | null>(null);
  const staticBundleStartRef = useRef<number>(0);
  const [composer, setComposer] = useState("");
  const [sending, setSending] = useState(false);
  const [sendStatus, setSendStatus] = useState<string | null>(null);
  const [activeTaskStream, setActiveTaskStream] = useState<{
    id: string;
    title: string;
    status: "queued" | "running" | "completed" | "failed" | "stopped";
    lines: string[];
  } | null>(null);
  const taskStreamStopRef = useRef<(() => void) | null>(null);
  const taskPollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Right-column layout. The first thing a user does on this tab is
  // pick a project — so Projects starts expanded. Console + Vibing
  // are tools you reach for after the iframe is up, so they fold
  // away by default and the user expands them on demand. The whole
  // column is drag-resizable on xl screens.
  const [consoleExpanded, setConsoleExpanded] = useState(false);
  const [projectsExpanded, setProjectsExpanded] = useState(true);
  const [vibingExpanded, setVibingExpanded] = useState(false);
  // Viewport state lifted out of WebPreviewFrame so the picker can
  // render inline with the device-row header — saves ~40 px of
  // vertical space the iframe gets back. WebPreviewFrame is now in
  // controlled mode; its internal picker is suppressed.
  const [viewport, setViewport] = useState<ViewportId>("fluid");
  const activeViewport =
    WEB_PREVIEW_VIEWPORTS.find((v) => v.id === viewport) ?? WEB_PREVIEW_VIEWPORTS[0];
  const [rightColumnWidth, setRightColumnWidth] = useState(320);
  const dragRef = useRef<{ startX: number; startW: number } | null>(null);
  const handleDividerDragStart = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      dragRef.current = { startX: e.clientX, startW: rightColumnWidth };
      const onMove = (ev: MouseEvent) => {
        if (!dragRef.current) return;
        const dx = ev.clientX - dragRef.current.startX;
        // Aside is on the right — drag right shrinks it, drag left grows it.
        const next = Math.min(720, Math.max(240, dragRef.current.startW - dx));
        setRightColumnWidth(next);
      };
      const onUp = () => {
        dragRef.current = null;
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      };
      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
    },
    [rightColumnWidth],
  );

  const isConnected = connState === "connected" && !!connectedDevice;
  const needsAuth = !!connectedDevice?.needsAuth;
  const statusError = devStatus?.error || null;
  const statusHttp = devStatus?.httpStatus || 0;

  // Load workspace apps on connect and whenever device changes. This
  // also serves as an early transport/auth probe so we can surface
  // preview errors before the user starts a web app.
  useEffect(() => {
    let cancelled = false;
    if (!isConnected) {
      setApps([]);
      setProjects([]);
      setWorkspaceError(null);
      return;
    }
    setWorkspaceLoading(true);
    setProjectsLoading(true);
    setWorkspaceError(null);
    (async () => {
      try {
        const [list, scanned] = await Promise.all([
          agentClient.getWorkspaceApps("web,hybrid"),
          agentClient.listProjects(),
        ]);
        if (cancelled) return;
        setApps(list);
        setProjects(scanned.filter(isWebReloadProject));
        if (list.length === 0) {
          setWorkspaceError(
            scanned.filter(isWebReloadProject).length > 0
              ? "No yaver.workspace.yaml found on the connected machine. Showing discovered projects instead."
              : "No yaver.workspace.yaml found on the connected machine.",
          );
        }
      } catch (err) {
        if (cancelled) return;
        const msg = err instanceof Error ? err.message : String(err);
        setWorkspaceError(msg);
        try {
          const scanned = await agentClient.listProjects();
          if (!cancelled) setProjects(scanned.filter(isWebReloadProject));
        } catch {
          if (!cancelled) setProjects([]);
        }
        if (/invalid relay password/i.test(msg) && onRepairRelay) {
          void repairRelayThenReconnect("auto");
        }
      } finally {
        if (!cancelled) {
          setWorkspaceLoading(false);
          setProjectsLoading(false);
        }
      }
    })();
    return () => { cancelled = true; };
    // repairRelayThenReconnect is stable in this component.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isConnected, connectedDevice?.id]);

  const repairRelayThenReconnect = async (mode: "auto" | "manual") => {
    if (!onRepairRelay) return;
    setRelayRepairState("repairing");
    setRelayRepairMsg(mode === "auto" ? "Detected invalid relay password — auto-repairing…" : "Repairing relay password…");
    try {
      const r = await onRepairRelay();
      if (r.repaired) {
        setRelayRepairState("repaired");
        setRelayRepairMsg(r.reason || "repaired");
        if (onReconnect) {
          try { await onReconnect(); } catch { /* surfaced by device status */ }
        }
        // Retry the workspace load now that the password is fresh.
        try {
          const [list, scanned] = await Promise.all([
            agentClient.getWorkspaceApps("web,hybrid"),
            agentClient.listProjects(),
          ]);
          setApps(list);
          setProjects(scanned.filter(isWebReloadProject));
          setWorkspaceError(
            list.length === 0
              ? (scanned.filter(isWebReloadProject).length > 0
                ? "No yaver.workspace.yaml found on the connected machine. Showing discovered projects instead."
                : "No yaver.workspace.yaml found on the connected machine.")
              : null,
          );
        } catch { /* keep prior error state */ }
      } else {
        setRelayRepairState("failed");
        setRelayRepairMsg(r.reason || "repair reported no change");
      }
    } catch (e) {
      setRelayRepairState("failed");
      setRelayRepairMsg(e instanceof Error ? e.message : String(e));
    }
  };

  // Poll dev server status. Reuse the same 2s cadence PreviewPane uses.
  useEffect(() => {
    if (!isConnected) {
      setDevStatus(null);
      return;
    }
    let cancelled = false;
    const poll = async () => {
      const s = await agentClient.getDevServerStatus();
      if (!cancelled) setDevStatus(s);
    };
    poll();
    const t = setInterval(poll, 2000);
    return () => { cancelled = true; clearInterval(t); };
  }, [isConnected]);

  // Infer the active app from the running dev server's workDir. The
  // agent doesn't echo back the app name today, so we match workDir
  // against the manifest's abs paths. Good enough for the "● running"
  // badge.
  const activeApp = useMemo(() => {
    if (!devStatus?.workDir) return null;
    const match = apps.find(
      (a) => a.absPath && a.absPath === devStatus.workDir,
    );
    return match?.name ?? null;
  }, [apps, devStatus?.workDir]);

  // Keep selectedApp in sync with activeApp when the user hasn't picked
  // anything yet.

  const activeProject = useMemo(() => {
    if (!devStatus?.workDir) return null;
    return projects.find((project) => project.path === devStatus.workDir) ?? null;
  }, [projects, devStatus?.workDir]);

  const selectedProject = useMemo(() => {
    if (!selectedProjectPath) return null;
    return projects.find((project) => project.path === selectedProjectPath) ?? null;
  }, [projects, selectedProjectPath]);

  useEffect(() => {
    if (!preferredProjectPath) return;
    if (!projects.some((project) => project.path === preferredProjectPath)) return;
    setSelectedProjectPath(preferredProjectPath);
    setSelectedApp(null);
  }, [preferredProjectPath, projects]);

  const hasWorkspaceApps = apps.length > 0;
  const useProjectFallback = !hasWorkspaceApps;

  // SSE: live dev-server logs. Use fetch-based SSE so auth headers
  // survive relay/direct modes; browser EventSource drops them.
  useEffect(() => {
    if (!isConnected) return;
    const url = agentClient.devEventsUrl;
    if (!url) return;
    const controller = new AbortController();
    (async () => {
      try {
        const res = await fetch(url, {
          headers: { ...agentClient.getAuthHeaders(), Accept: "text/event-stream" },
          signal: controller.signal,
        });
        if (!res.ok || !res.body) return;
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        while (true) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          let idx: number;
          while ((idx = buffer.indexOf("\n\n")) >= 0) {
            const frame = buffer.slice(0, idx);
            buffer = buffer.slice(idx + 2);
            const dataLines = frame
              .split("\n")
              .filter((line) => line.startsWith("data:"))
              .map((line) => line.slice(5).trimStart());
            if (dataLines.length === 0) continue;
            try {
              const data = JSON.parse(dataLines.join("\n"));
              if (data?.logLine) {
                setLogs((prev) => [...prev.slice(-199), String(data.logLine)]);
              } else if (data?.message) {
                setLogs((prev) => [...prev.slice(-199), `[${data.type || "event"}] ${data.message}`]);
              }
            } catch {
              // ignore malformed SSE frames
            }
          }
        }
      } catch {
        // connection dropped or aborted; the effect re-subscribes on reconnect
      }
    })();
    return () => { controller.abort(); };
  }, [isConnected]);

  const stopActiveTaskStream = useCallback(() => {
    if (taskStreamStopRef.current) {
      taskStreamStopRef.current();
      taskStreamStopRef.current = null;
    }
    if (taskPollRef.current) {
      clearInterval(taskPollRef.current);
      taskPollRef.current = null;
    }
  }, []);

  useEffect(() => {
    return () => stopActiveTaskStream();
  }, [stopActiveTaskStream]);

  const handleStart = async () => {
    if (!selectedApp && !selectedProject) return;
    setStarting(true);
    setStartError(null);

    // Pre-flight: detect mobile-framework projects on the client. Two
    // reasons (1) older agents (≤1.99.79) reject /dev/start with a 400
    // "mobile-only" error before they understand the new caller=web-ui
    // routing, and (2) even on newer agents we'd rather skip the round
    // trip and go straight to the static-bundle build, since that's
    // the only Web Reload path that actually renders Expo / RN apps.
    const projectFw = (selectedProject?.framework || "").toLowerCase();
    const appRow = selectedApp ? apps.find((a) => a.name === selectedApp) ?? null : null;
    const appStack = (appRow?.stack || "").toLowerCase();
    const isMobileFramework =
      projectFw === "expo" ||
      projectFw === "react-native" ||
      projectFw === "metro" ||
      appStack === "react-native-expo" ||
      appStack === "react-native";
    if (isMobileFramework) {
      setStarting(false);
      await handleBuildStaticBundle();
      return;
    }

    try {
      // Web-only path: ask the agent to spin up the dev server normally.
      // The new caller="web-ui" tag (added by agentClient.startDevServer)
      // lets the agent's surface gate know who's calling — newer agents
      // (≥1.99.80) may answer with `mode: "static-bundle"` even for what
      // looks like a web framework if they detect the project is in fact
      // RN, in which case we route through the bundle path here too.
      let response: Awaited<ReturnType<typeof agentClient.startDevServer>> | null = null;
      if (useProjectFallback && selectedProject) {
        response = await agentClient.startDevServer({
          framework: selectedProject.framework,
          workDir: selectedProject.path,
          projectName: selectedProject.name,
          surface: "web-reload",
        });
      } else if (selectedApp) {
        response = await agentClient.startDevServer({
          app: selectedApp,
          surface: "web-reload",
        });
      }
      if (response?.mode === "static-bundle") {
        // Agent told us (web-ui caller) to render via the static bundle.
        // If a fresh build is already on disk we just flip iframe state;
        // otherwise kick off a build. Either way, no /dev/start dev
        // server is running — the iframe loads /dev/web-bundle/.
        if (response.bundleReady) {
          setStaticBundleState("ready");
        } else {
          await handleBuildStaticBundle();
        }
        return;
      }
      // Refresh status immediately; polling will pick up ongoing changes.
      const s = await agentClient.getDevServerStatus();
      setDevStatus(s);
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err));
    } finally {
      setStarting(false);
    }
  };

  const handleStop = async () => {
    setStopState("stopping");
    setStopMessage("");
    setStopBuildsCancelled(0);
    try {
      const res: any = await agentClient.stopDevServer();
      const s = await agentClient.getDevServerStatus();
      setDevStatus(s);
      if (!res || res.ok === false) {
        setStopState("error");
        setStopMessage(res?.error || res?.message || "Stop failed — check agent logs.");
        setTimeout(() => setStopState("idle"), 5000);
        return;
      }
      // verified=false means SIGINT+SIGKILL didn't confirm exit in 7s.
      if (res.verified === false) {
        setStopState("error");
        setStopMessage("Subprocess did not confirm exit within 7s. The agent issued SIGKILL.");
        setTimeout(() => setStopState("idle"), 5000);
        return;
      }
      setStopBuildsCancelled(res.buildsCancelled || 0);
      setStopState("stopped");
      setTimeout(() => {
        setStopState("idle");
        setStopMessage("");
        setStopBuildsCancelled(0);
      }, 2500);
    } catch (err) {
      setStopState("error");
      setStopMessage(err instanceof Error ? err.message : "Stop request failed.");
      setTimeout(() => setStopState("idle"), 5000);
    }
  };

  // Stop the active dev server and immediately start the currently
  // selected one. Used for the "Switch to <X>" affordance so the user
  // doesn't have to chase a two-step (stop, then start) flow.
  const handleSwitchProject = async () => {
    try {
      await agentClient.stopDevServer();
    } catch { /* keep going — start will surface its own error */ }
    setDevStatus({ running: false });
    await handleStart();
  };

  // True when the user has picked a different project than the one
  // currently running. Drives the Switch button label/visibility.
  // (Recomputed below after `isRunning` is in scope.)

  const handleReload = async () => {
    try {
      await agentClient.reloadDevServer({ mode: "dev" });
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err));
    }
  };

  // Spawn a sibling Expo Web process on the remote box. Only valid
  // when the active dev server is Expo in --dev-client mode (the
  // default). Metro keeps running; this adds a second subprocess on
  // a different port so the iframe has HTML to render.
  const handleStartWebPreview = async () => {
    setWebPreviewStarting(true);
    setWebPreviewError(null);
    try {
      await agentClient.startWebPreview();
      // Poll status until webPort flips positive; the preview needs a
      // few seconds to bundle before it responds with HTML.
      for (let i = 0; i < 30; i++) {
        await new Promise((r) => setTimeout(r, 1500));
        const s = await agentClient.getDevServerStatus();
        setDevStatus(s);
        if (s?.webPort && s.webPort > 0) break;
      }
    } catch (e) {
      setWebPreviewError(e instanceof Error ? e.message : String(e));
    } finally {
      setWebPreviewStarting(false);
    }
  };
  const handleStopWebPreview = async () => {
    try {
      await agentClient.stopWebPreview();
      const s = await agentClient.getDevServerStatus();
      setDevStatus(s);
    } catch (e) {
      setWebPreviewError(e instanceof Error ? e.message : String(e));
    }
  };

  // Compile a static web bundle on the agent (target=web-js-bundle) and
  // load it into the iframe via /dev/web-bundle/. Independent of the
  // sibling Expo Web process — works after agent restarts that orphan
  // the live-HMR flow. Transport progress is rendered via the
  // staticBundleTransport state which SSE listeners (below) drive.
  const handleBuildStaticBundle = useCallback(async () => {
    setStaticBundleState("building");
    setStaticBundleError(null);
    setStaticBundleTransport(null);
    setStaticBundleInfo(null);
    staticBundleStartRef.current = Date.now();
    const projectName = activeProject?.name || selectedApp || undefined;
    const projectPath = activeProject?.path || selectedProjectPath || undefined;
    try {
      const r = await agentClient.buildWebJSBundle({ projectName, projectPath });
      if (!r.ok) {
        setStaticBundleState("failed");
        setStaticBundleError(r.error || "build failed");
        return;
      }
      setStaticBundleInfo({ size: r.size, fileCount: r.fileCount });
      setStaticBundleState("ready");
    } catch (e) {
      setStaticBundleState("failed");
      setStaticBundleError(e instanceof Error ? e.message : String(e));
    }
  }, [activeProject?.name, activeProject?.path, selectedApp, selectedProjectPath]);

  // Auto-detect a pre-existing built bundle on mount + every 5s while
  // idle. Catches the case where the bundle is built out-of-band (curl,
  // MCP, recovery flow, agent server-side trigger) — the dashboard's
  // React state needs to flip to `ready` for the iframe URL to switch
  // to /dev/web-bundle/, and without periodic polling that flip never
  // happens since SSE doesn't fire a "bundle ready" notification on
  // out-of-band builds.
  useEffect(() => {
    if (!isConnected) return;
    let cancelled = false;
    const tick = async () => {
      const info = await agentClient.getWebBundleInfo();
      if (cancelled) return;
      if (info.built && (staticBundleState === "idle" || staticBundleState === "failed")) {
        setStaticBundleInfo({ size: info.size || 0, fileCount: info.fileCount || 0 });
        setStaticBundleState("ready");
      }
    };
    void tick();
    // Poll every 5s — only while idle/failed; once `ready` or `building`,
    // the existing flow drives the state and the timer becomes a no-op.
    const id = setInterval(() => {
      if (staticBundleState === "idle" || staticBundleState === "failed") {
        void tick();
      }
    }, 5000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [isConnected, staticBundleState]);

  // No auto-build on tab open — the user explicitly wants to pick a
  // project and click "Build & render static bundle" themselves.
  // (Earlier versions auto-fired on first connect with a project
  // selected; that surprised users who weren't ready to spend ~40s on
  // a full re-export. v1.1.84's auto-detect of a *pre-existing* bundle
  // still works above — that's a cheap GET, not a build kick-off.)

  // Subscribe to webview/transport SSE phase events so the dashboard
  // shows live "serving 23/142 files (16%)" progress between the
  // build-native response and the iframe rendering.
  useEffect(() => {
    if (staticBundleState !== "building" && staticBundleState !== "ready") return;
    const url = agentClient.devEventsUrl;
    if (!url) return;
    const es = new EventSource(url);
    const onMsg = (ev: MessageEvent) => {
      try {
        const data = JSON.parse(ev.data);
        if (data?.topic !== "webview/transport") return;
        // Mirror every transport event into the Console panel so the
        // user can see bytes flowing — without this the console only
        // shows the build phase and goes silent during delivery, which
        // looks like the dashboard hung when actually each iframe
        // asset is being streamed through the relay.
        //
        // Format mirrors Metro/Expo's compile bar so the eye reads
        // them as the same kind of thing: a 16-cell ▓░ bar followed
        // by percent + done/total + file name.
        const ts = new Date().toLocaleTimeString();
        const renderBar = (pct: number) => {
          const cells = 16;
          const filled = Math.max(0, Math.min(cells, Math.round((pct / 100) * cells)));
          return "▓".repeat(filled) + "░".repeat(cells - filled);
        };
        const fmtBytes = (n: number) => {
          if (n >= 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(2)} MB`;
          if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`;
          return `${n} B`;
        };
        if (data.type === "phase") {
          const file = typeof data.file === "string" ? ` · ${data.file}` : "";
          setLogs((prev) => [...prev.slice(-199), `[transport ${ts}] phase=${data.phase}${file}`]);
          setStaticBundleTransport((prev) => ({
            phase: data.phase,
            pct: prev?.pct ?? 0,
            done: prev?.done ?? 0,
            total: prev?.total ?? 0,
            file: prev?.file,
          }));
        } else if (data.type === "progress") {
          const rawPct = typeof data.pct === "number" ? data.pct : 0;
          const pct = rawPct.toFixed(1).padStart(5);
          const done = typeof data.done === "number" ? data.done : 0;
          const total = typeof data.total === "number" ? data.total : 0;
          const file = typeof data.currentFile === "string" ? data.currentFile : "?";
          // Only one column in the file/path so the bar stays visually
          // aligned even as filenames vary in length.
          setLogs((prev) => [
            ...prev.slice(-199),
            `[transport ${ts}] ${file}  ${renderBar(rawPct)} ${pct}% (${fmtBytes(done)}/${fmtBytes(total)})`,
          ]);
          setStaticBundleTransport({
            phase: data.phase || "streaming",
            pct: rawPct,
            done,
            total,
            file: typeof data.currentFile === "string" ? data.currentFile : undefined,
          });
        }
      } catch {
        /* ignore */
      }
    };
    es.addEventListener("message", onMsg as EventListener);
    return () => {
      es.removeEventListener("message", onMsg as EventListener);
      es.close();
    };
  }, [staticBundleState]);


  const handleSendPrompt = useCallback(async () => {
    const prompt = composer.trim();
    if (!prompt || sending) return;
    const appRow = selectedApp ? apps.find((app) => app.name === selectedApp) ?? null : null;
    const workDir = selectedProject?.path || appRow?.absPath || devStatus?.workDir;
    const projectName = selectedProject?.name || appRow?.name || workDir?.split("/").filter(Boolean).slice(-1)[0];
    setSending(true);
    setSendStatus(null);
    try {
      stopActiveTaskStream();
      const task = await agentClient.createTask({
        title: prompt.slice(0, 80),
        description: prompt,
        userPrompt: prompt,
        projectName,
        workDir,
      });
      setActiveTaskStream({
        id: task.id,
        title: task.title,
        status: task.status,
        lines: [],
      });
      taskStreamStopRef.current = agentClient.streamTaskOutput(task.id, (line) => {
        const trimmed = String(line || "").trimEnd();
        if (!trimmed) return;
        setActiveTaskStream((prev) => {
          if (!prev || prev.id !== task.id) return prev;
          const next = [...prev.lines, trimmed];
          return {
            ...prev,
            status: "running",
            lines: next.length > 200 ? next.slice(-200) : next,
          };
        });
      });
      taskPollRef.current = setInterval(() => {
        void agentClient.getTask(task.id)
          .then((fresh) => {
            setActiveTaskStream((prev) => {
              if (!prev || prev.id !== task.id) return prev;
              const nextLines = fresh.output && fresh.output.length > 0
                ? fresh.output
                : prev.lines.length === 0 && fresh.resultText
                  ? [fresh.resultText]
                  : prev.lines;
              return {
                ...prev,
                status: fresh.status,
                lines: nextLines.length > 200 ? nextLines.slice(-200) : nextLines,
              };
            });
            if (fresh.status !== "queued" && fresh.status !== "running") {
              stopActiveTaskStream();
            }
          })
          .catch(() => {});
      }, 2000);
      setComposer("");
      setSendStatus(`Started “${task.title}”.`);
    } catch (err) {
      setSendStatus(err instanceof Error ? err.message : String(err));
    } finally {
      setSending(false);
    }
  }, [apps, composer, devStatus?.workDir, selectedApp, selectedProject, sending, stopActiveTaskStream]);

  // Reconnect & Fix — same robust recovery the Hot Reload tab runs.
  // Ping → repair relay password (if relevant) → stop → git-pull →
  // pkill Metro/Expo stragglers → clear caches → restart → refresh.
  // Progress bar + streaming log mirror the Hot Reload UX so users
  // don't have to wonder if it's still working.
  const appendRecovery = (line: string) => {
    setRecoveryLog((prev) => {
      const next = [...prev, line];
      return next.length > 80 ? next.slice(-80) : next;
    });
  };
  const stageProgress = (pct: number, label: string) => {
    setRecoveryProgress((prev) => ({ pct: Math.max(prev.pct, pct), stage: label, active: true }));
  };

  const handleRecover = async () => {
    if (recovering) return;
    setRecovering(true);
    setRecoveryLog([]);
    setRecoveryProgress({ pct: 0.05, stage: "starting recovery…", active: true });
    const savedWorkDir = devStatus?.workDir;
    const savedFramework = devStatus?.framework;
    try {
      stageProgress(0.1, "checking agent reachability…");
      appendRecovery("→ checking agent reachability…");
      try {
        const info = await agentClient.getInfo();
        appendRecovery(`✓ agent ok (v${info?.version || "?"})`);
      } catch (e: any) {
        appendRecovery(`✗ agent not reachable: ${e?.message || e}`);
        if (onReconnect) {
          appendRecovery("→ reconnecting device…");
          try { await onReconnect(); appendRecovery("✓ device reconnect done"); }
          catch (err: any) { appendRecovery(`✗ device reconnect failed: ${err?.message || err}`); }
        }
      }
      if (onRepairRelay) {
        stageProgress(0.2, "repairing relay password…");
        appendRecovery("→ repairing user relay password in Convex…");
        try {
          const r = await onRepairRelay();
          appendRecovery(r.repaired ? `✓ repaired: ${r.reason}` : `· ${r.reason}`);
          if (r.repaired && onReconnect) {
            try { await onReconnect(); appendRecovery("✓ reconnected"); }
            catch (err: any) { appendRecovery(`✗ reconnect after repair failed: ${err?.message || err}`); }
          }
        } catch (e: any) { appendRecovery(`✗ repair failed: ${e?.message || e}`); }
      }
      if (savedWorkDir) {
        stageProgress(0.3, "stopping dev server…");
        appendRecovery("→ stopping dev server…");
        try { await agentClient.stopDevServer(); appendRecovery("✓ stopped"); }
        catch (e: any) { appendRecovery(`warn: stop failed: ${e?.message || e}`); }

        stageProgress(0.45, "git pull --ff-only…");
        appendRecovery("→ pulling latest commit (git pull --ff-only)…");
        try {
          await agentClient.startExec({
            command: "if [ -d .git ]; then if git diff --quiet && git diff --cached --quiet; then git fetch --depth=50 && git pull --ff-only; else echo 'skip: working tree has uncommitted changes' >&2; fi; else echo 'skip: not a git repo' >&2; fi",
            workDir: savedWorkDir,
            timeout: 60,
          });
          appendRecovery("✓ git pulled (or skipped on dirty/non-repo)");
        } catch (e: any) { appendRecovery(`warn: git pull failed: ${e?.message || e}`); }

        stageProgress(0.6, "killing stray Vite / Next / Metro…");
        appendRecovery("→ killing stray dev-server processes…");
        try {
          await agentClient.startExec({
            command: "pkill -f 'vite' 2>/dev/null; pkill -f 'next dev' 2>/dev/null; pkill -f 'expo start' 2>/dev/null; pkill -f 'metro' 2>/dev/null; sleep 1; echo procs-killed",
            workDir: savedWorkDir,
            timeout: 20,
          });
          appendRecovery("✓ procs killed");
        } catch (e: any) { appendRecovery(`warn: pkill failed: ${e?.message || e}`); }

        stageProgress(0.75, "clearing caches…");
        appendRecovery("→ clearing caches on remote…");
        try {
          await agentClient.startExec({
            command: "rm -rf node_modules/.cache .expo/web/cache .next/cache .vite /tmp/metro-* /tmp/haste-map-* 2>/dev/null || true; echo cache-cleared",
            workDir: savedWorkDir,
            timeout: 30,
          });
          appendRecovery("✓ caches cleared");
        } catch (e: any) { appendRecovery(`warn: cache clear failed: ${e?.message || e}`); }

        if (savedFramework) {
          stageProgress(0.9, `restarting dev server (${savedFramework})…`);
          appendRecovery(`→ restarting dev server (${savedFramework})…`);
          try {
            await agentClient.startDevServer({
              framework: savedFramework,
              workDir: savedWorkDir,
              platform: "web",
              surface: "web-reload",
            });
            appendRecovery("✓ dev server restarted");
          } catch (e: any) { appendRecovery(`✗ restart failed: ${e?.message || e}`); }
        }
      } else {
        appendRecovery("  (no dev server was running — skipping restart)");
      }
      appendRecovery("✓ done");
      setRecoveryProgress({ pct: 1, stage: "done", active: true });
      setTimeout(() => setRecoveryProgress({ pct: 0, stage: "", active: false }), 1500);
    } catch (e: any) {
      appendRecovery(`✗ recovery failed: ${e?.message || e}`);
      setRecoveryProgress((prev) => ({ ...prev, stage: `failed: ${e?.message || e}` }));
    }
    setRecovering(false);
  };

  const connectionLabel = connectedDevice
    ? (connectedDevice as any).local
      ? "DIRECT"
      : "RELAY"
    : undefined;

  if (!isConnected) {
    return (
      <div className="flex h-full items-center justify-center p-8 text-center">
        <div className="max-w-sm space-y-3">
          <div className="text-5xl">🌐</div>
          <p className="text-sm font-medium text-surface-200">Web Reload needs a connected device</p>
          <p className="text-[12px] text-surface-500">
            Pick a machine from the Devices tab to start previewing web apps
            (Next.js, Vite, Flutter Web) running on it.
          </p>
        </div>
      </div>
    );
  }

  const previewUrl = agentClient.devPreviewUrl;
  const isRunning = !!devStatus?.running;

  // Auto-start the Expo Web sibling whenever the user lands on the Web
  // App tab and Metro is already running for an Expo/RN project but no
  // sibling has been spawned yet. Skips if a start is already in flight,
  // already failed (don't loop on a broken setup), or if Metro is not
  // an Expo/RN dev server (vite/next/etc. render their own HTML).
  // Auto-start makes Web App mode "click and see" instead of "click,
  // wait, click another button, wait again".
  const autoStartedRef = useRef(false);
  useEffect(() => {
    if (autoStartedRef.current) return;
    if (!isRunning) return;
    if (webPreviewStarting) return;
    if (webPreviewError) return;
    if (devStatus?.webPort && devStatus.webPort > 0) return;
    const fw = (devStatus?.framework || "").toLowerCase();
    if (fw !== "expo" && fw !== "react-native" && fw !== "metro") return;
    autoStartedRef.current = true;
    void handleStartWebPreview();
  }, [isRunning, devStatus?.framework, devStatus?.webPort, webPreviewStarting, webPreviewError]);
  useEffect(() => {
    if (!isRunning) autoStartedRef.current = false;
  }, [isRunning]);
  const switchPending =
    isRunning &&
    !!devStatus?.workDir &&
    !!selectedProject &&
    selectedProject.path !== devStatus.workDir;

  // Preflight: fetch the preview URL from the parent page once so
  // transport/auth failures surface as a readable dashboard error
  // instead of a broken iframe.
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [autoRepairedOnce, setAutoRepairedOnce] = useState(false);
  const [notRenderable, setNotRenderable] = useState<{ title: string; body: string } | null>(null);
  useEffect(() => {
    if (!previewUrl || !isRunning) {
      setPreviewError(null);
      setNotRenderable(null);
      return;
    }
    let alive = true;
    const ctrl = new AbortController();
    (async () => {
      try {
        const res = await fetch(previewUrl, { method: "GET", signal: ctrl.signal, cache: "no-store", redirect: "manual" });
        if (!alive) return;
        if (res.status === 401 || res.status === 403) {
          const text = await res.text().catch(() => "");
          let msg = `HTTP ${res.status}`;
          try {
            const parsed = JSON.parse(text);
            if (parsed?.error) msg = parsed.error;
          } catch {
            if (text) msg = text.slice(0, 200);
          }
          setPreviewError(msg);
          setNotRenderable(null);
          // Auto-repair once when we see invalid-relay-password the first
          // time we mount the iframe. Avoids loops by flipping a flag.
          if (!autoRepairedOnce && /invalid relay password/i.test(msg) && onRepairRelay) {
            setAutoRepairedOnce(true);
            void repairRelayThenReconnect("auto");
          }
          return;
        }
        setPreviewError(null);
        // Non-HTML responses (Metro bundle JSON, plain text, octet-stream)
        // can't render in an iframe — show a CTA instead of a white box.
        const ct = res.headers.get("content-type") || "";
        const isHtml = /text\/html/i.test(ct);
        if (!isHtml) {
          const fw = (devStatus?.framework || "").toLowerCase();
          const isMobile = fw === "expo" || fw === "react-native" || fw === "metro";
          const hasWebSibling = !!devStatus?.webPort && devStatus.webPort > 0;
          setNotRenderable({
            title: isMobile
              ? (hasWebSibling
                ? "Expo Web preview is ready"
                : "This dev server is mobile-only — start an Expo Web preview to render it here")
              : "Dev server response isn't browser-renderable",
            body: isMobile
              ? (hasWebSibling
                ? "Click the preview URL to load the sibling Expo Web process (Metro keeps serving Hermes bundles to the phone)."
                : "Metro emits a JavaScript bundle, not HTML. Keep Metro running for the phone and spawn a sibling `expo --web` process here — the button below does that on the remote box without touching Metro.")
              : `The dev server returned ${ct || "no content-type"} instead of HTML. Check the dev server is configured to serve a web build.`,
          });
        } else {
          setNotRenderable(null);
        }
      } catch (e: any) {
        if (e?.name !== "AbortError") setPreviewError(null);
      }
    })();
    return () => { alive = false; ctrl.abort(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [previewUrl, isRunning, devStatus?.framework]);

  return (
    <div className="flex h-full flex-col gap-3 p-3 md:p-4">
      {/* Header — device, viewport picker, global actions, all on one
          row. Inline viewport saves ~40 px of vertical space the
          iframe gets back. */}
      <div className="flex flex-wrap items-center gap-3 rounded-md border border-surface-800 bg-surface-900/40 px-3 py-2">
        <div className="flex items-center gap-2 text-xs">
          <span className="h-2 w-2 rounded-full bg-emerald-400" />
          <span className="font-medium text-surface-100">{connectedDevice?.name}</span>
          <span className="text-[10px] uppercase tracking-widest text-surface-500">{connectionLabel}</span>
        </div>
        <div className="flex items-center gap-1.5">
          <span className="text-[9px] uppercase tracking-widest text-surface-500">View</span>
          <div className="flex rounded-md border border-surface-800 bg-surface-900">
            {WEB_PREVIEW_VIEWPORTS.map((v) => (
              <button
                key={v.id}
                onClick={() => setViewport(v.id)}
                className={`px-2 py-0.5 text-[10px] transition-colors first:rounded-l-md last:rounded-r-md ${
                  viewport === v.id
                    ? "bg-indigo-500/20 text-indigo-200"
                    : "text-surface-400 hover:bg-surface-800 hover:text-surface-200"
                }`}
                title={v.id === "fluid" ? "Fill container" : `${v.width}×${v.height}`}
              >
                {v.label}
              </button>
            ))}
          </div>
          {viewport !== "fluid" ? (
            <span className="text-[9px] text-surface-500">
              {activeViewport.width}×{activeViewport.height}
            </span>
          ) : null}
        </div>
        <div className="ml-auto flex items-center gap-2">
          {onRepairRelay && (
            <button
              onClick={() => void repairRelayThenReconnect("manual")}
              disabled={relayRepairState === "repairing"}
              className="rounded border border-amber-500/40 bg-amber-500/10 px-2.5 py-1 text-[11px] text-amber-200 hover:bg-amber-500/20 disabled:opacity-50"
              title="Re-sync userSettings.relayPassword with the platform default — fixes 'invalid relay password' iframe failures"
            >
              {relayRepairState === "repairing" ? "Repairing…" : "Repair relay"}
            </button>
          )}
          <button
            onClick={() => void handleRecover()}
            disabled={recovering}
            className={`rounded border px-2.5 py-1 text-[11px] ${
              recovering
                ? "border-amber-500/40 bg-amber-500/10 text-amber-300 cursor-wait"
                : "border-surface-700 text-surface-300 hover:border-emerald-500/40 hover:text-emerald-300"
            }`}
            title="Full recovery: ping agent, repair relay, stop, git pull, pkill, clear caches, restart, refresh"
          >
            {recovering ? "Recovering…" : "Reconnect & Fix"}
          </button>
          {isRunning ? (
            <>
              <button
                onClick={handleReload}
                className="rounded border border-surface-700 px-2.5 py-1 text-[11px] text-surface-200 hover:bg-surface-800"
              >
                Hard reload
              </button>
              {switchPending && selectedProject && (
                <button
                  onClick={() => void handleSwitchProject()}
                  disabled={starting}
                  className="rounded border border-emerald-500/40 bg-emerald-500/10 px-2.5 py-1 text-[11px] font-medium text-emerald-200 hover:bg-emerald-500/20 disabled:opacity-50"
                  title={`Stop ${devStatus?.workDir?.split("/").slice(-1)[0] || "current"} and start ${selectedProject.name}`}
                >
                  {starting ? "Switching…" : `Switch to ${selectedProject.name}`}
                </button>
              )}
              <button
                onClick={handleStop}
                disabled={stopState === "stopping"}
                className="rounded border border-red-500/40 bg-red-500/10 px-2.5 py-1 text-[11px] text-red-200 hover:bg-red-500/20 disabled:opacity-60 disabled:cursor-wait"
                title="Stop serving this preview, cancel any in-flight Hermes build, and clear stale incidents"
              >
                {stopState === "stopping" ? (
                  <span className="inline-flex items-center gap-1.5">
                    <span className="h-2.5 w-2.5 animate-spin rounded-full border border-red-200/40 border-t-red-200" />
                    Stopping…
                  </span>
                ) : (
                  devStatus?.stopActionLabel || "Stop & switch"
                )}
              </button>
            </>
          ) : (
            <>
              <button
                onClick={handleStart}
                disabled={(!selectedApp && !selectedProject) || starting || needsAuth}
                className="rounded border border-emerald-500/40 bg-emerald-500/10 px-3 py-1 text-[11px] font-medium text-emerald-200 hover:bg-emerald-500/20 disabled:opacity-50"
              >
                {starting ? "Starting…" : selectedApp ? `Start ${selectedApp}` : selectedProject ? `Start ${selectedProject.name}` : "Pick a project"}
              </button>
              {/* Force Stop — always reachable when something might still
                  be running on the agent (devStatus.workDir, or a previous
                  start that the status poll can no longer reach). Hidden
                  only when we're 100% sure nothing's there. */}
              {(devStatus?.workDir || statusError) && (
                <button
                  onClick={handleStop}
                  disabled={stopState === "stopping"}
                  className="rounded border border-red-500/40 bg-red-500/5 px-2.5 py-1 text-[11px] text-red-200 hover:bg-red-500/15 disabled:opacity-60 disabled:cursor-wait"
                  title="Force-stop any dev server the agent might still be running"
                >
                  {stopState === "stopping" ? "Stopping…" : "Force Stop"}
                </button>
              )}
            </>
          )}
        </div>
      </div>

      {/* Stop confirmation banner — gives the user explicit feedback that
          the agent really stopped (not just that the request was sent).
          Only renders for 2.5s on success, or until the next attempt /
          5s on error. agent 1.99.93+ provides verified + buildsCancelled
          fields; older agents fall back to a generic success message. */}
      {(stopState === "stopped" || stopState === "error") && (
        <div
          className={`flex items-start gap-2 rounded-md border px-3 py-2 text-[11px] ${
            stopState === "stopped"
              ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-200"
              : "border-red-500/50 bg-red-500/10 text-red-200"
          }`}
          role="status"
        >
          <span className="text-[14px] leading-none">{stopState === "stopped" ? "✓" : "⚠"}</span>
          <div className="flex-1">
            <div className="font-medium">
              {stopState === "stopped" ? "Dev server stopped" : "Stop incomplete"}
            </div>
            <div className="text-surface-300/90">
              {stopState === "stopped"
                ? stopBuildsCancelled > 0
                  ? `Subprocess confirmed exit. Cancelled ${stopBuildsCancelled} in-flight build${stopBuildsCancelled === 1 ? "" : "s"}. Stale build incidents cleared.`
                  : "Subprocess confirmed exit. Stale build incidents cleared."
                : stopMessage || "Stop did not complete. Try Force Stop, or check agent logs."}
            </div>
          </div>
        </div>
      )}

      {needsAuth && (
        <div className="flex items-center gap-2 rounded-md border border-amber-500/50 bg-amber-500/10 px-3 py-2 text-[11px] text-amber-200">
          <span className="flex-1">
            Agent session expired on this device — sign in on the host to restart Web Reload.
          </span>
          {onReconnect && (
            <button
              onClick={() => void onReconnect()}
              className="rounded border border-amber-500/40 bg-amber-500/15 px-2 py-0.5 text-[10px] hover:bg-amber-500/25"
            >
              Try reconnect
            </button>
          )}
        </div>
      )}

      {!needsAuth && statusError && (
        <div className="rounded-md border border-red-500/40 bg-red-500/5 px-3 py-2 text-[11px] text-red-200">
          {statusHttp ? `Dev status unreachable (HTTP ${statusHttp}): ` : "Dev status unreachable: "}
          {statusError}
        </div>
      )}

      {relayRepairMsg && (
        <div className={`rounded-md border px-3 py-2 text-[11px] ${
          relayRepairState === "failed"
            ? "border-red-500/40 bg-red-500/5 text-red-200"
            : relayRepairState === "repaired"
              ? "border-emerald-500/40 bg-emerald-500/5 text-emerald-200"
              : "border-amber-500/40 bg-amber-500/5 text-amber-200"
        }`}>
          {relayRepairMsg}
        </div>
      )}

      {startError && (
        <div className="rounded-md border border-red-500/40 bg-red-500/5 px-3 py-2 text-[11px] text-red-200">
          {startError}
        </div>
      )}

      {previewError && (
        <div className="flex items-center gap-2 rounded-md border border-red-500/40 bg-red-500/5 px-3 py-2 text-[11px] text-red-200">
          <span className="flex-1 truncate">{previewError}</span>
          {onRepairRelay && /invalid relay password/i.test(previewError) && (
            <button
              onClick={() => void repairRelayThenReconnect("manual")}
              className="rounded border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-[10px] text-amber-200 hover:bg-amber-500/20"
            >
              Repair now
            </button>
          )}
        </div>
      )}

      {/* Split body — flex on xl so the divider between iframe and
          panels is drag-resizable. Below xl it stacks vertically and
          the divider is hidden. */}
      <div className="flex min-h-0 flex-1 flex-col gap-3 xl:flex-row xl:gap-2">
        <div className="relative flex min-h-0 flex-col gap-2 xl:flex-1 xl:min-w-0">
          {/* Recovery overlay — progress bar + streaming log. Absolute
              so it doesn't reflow the preview frame when it mounts. */}
          {(recovering || recoveryLog.length > 0 || recoveryProgress.active) ? (
            <div className="pointer-events-auto absolute top-2 right-2 z-10 w-72 max-w-[40%] rounded border border-amber-500/30 bg-surface-950/95 shadow-lg backdrop-blur">
              <div className="flex items-center justify-between px-2 py-1 text-[10px] uppercase tracking-widest text-amber-400 border-b border-amber-500/20">
                <span>{recovering ? "Recovery · running" : "Recovery · last run"}</span>
                {!recovering && recoveryLog.length > 0 ? (
                  <button
                    onClick={() => setRecoveryLog([])}
                    className="text-surface-600 hover:text-surface-400"
                    title="Clear recovery log"
                  >
                    clear
                  </button>
                ) : null}
              </div>
              {recoveryProgress.active ? (
                <div className="px-2 pt-2">
                  <div className="h-1 w-full overflow-hidden rounded bg-emerald-500/15">
                    <div
                      className="h-full rounded bg-emerald-400 transition-[width] duration-300 ease-out"
                      style={{ width: `${Math.max(recoveryProgress.pct * 100, 5)}%` }}
                    />
                  </div>
                  {recoveryProgress.stage ? (
                    <p className="mt-1 truncate font-mono text-[10px] text-emerald-200/80" title={recoveryProgress.stage}>
                      {recoveryProgress.stage}
                    </p>
                  ) : null}
                </div>
              ) : null}
              {(recovering || recoveryLog.length > 0) ? (
                <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all px-2 py-1 font-mono text-[10px] leading-4 text-amber-200/80">
                  {recoveryLog.length === 0 ? (
                    <span className="text-surface-600">(starting…)</span>
                  ) : (
                    recoveryLog.join("\n")
                  )}
                </pre>
              ) : null}
            </div>
          ) : null}
          <WebPreviewFrame
            hideViewportSelector
            viewport={viewport}
            onViewportChange={setViewport}
            // URL priority: built static bundle (most stable, doesn't
            // depend on a long-running sibling process) → live Expo Web
            // sibling → primary dev server preview proxy.
            //
            // Critical: stay on the bundle URL while we're *rebuilding*
            // too, not just when state is "ready". Falling back to
            // previewUrl during rebuild shows the agent's `/dev/`
            // reverse-proxy catchall — which serves the running dev
            // server's response, or the agent's own dashboard if
            // nothing's up. Either way it's not what the user asked
            // for. The bundlingState overlay covers the iframe during
            // a rebuild, so showing a brief 404 underneath while the
            // rebuild completes is fine.
            url={
              staticBundleState === "ready" || staticBundleState === "building"
                ? agentClient.devWebBundleUrl
                : devStatus?.webPort && devStatus.webPort > 0
                ? agentClient.devWebPreviewUrl
                : previewUrl
            }
            onIframeLoad={
              staticBundleState === "ready"
                ? () => {
                    const ms = Date.now() - (staticBundleStartRef.current || Date.now());
                    void agentClient.ackWebBundleLoaded(ms);
                  }
                : undefined
            }
            // Static bundle is servable even when the dev server isn't
            // running — set running=true so the iframe mounts.
            running={staticBundleState === "ready" ? true : isRunning}
            // Open-in-new-tab / fullscreen URL must match the iframe's
            // src or the user gets a 503 "no dev server running" — the
            // /dev/ proxy 503s when there's no Metro running, which is
            // exactly the static-bundle mode. Match the URL priority
            // chain we use for the iframe itself.
            onOpenInNewTab={(() => {
              const url =
                staticBundleState === "ready" || staticBundleState === "building"
                  ? agentClient.devWebBundleUrl
                  : devStatus?.webPort && devStatus.webPort > 0
                  ? agentClient.devWebPreviewUrl
                  : previewUrl;
              return url ? () => window.open(url, "_blank") : undefined;
            })()}
            connectionLabel={connectionLabel}
            // Suppress the "mobile-only" notice when the static
            // bundle path has produced a renderable artifact — the
            // banner was written before that path existed and is now
            // misleading. RN+RN-Web projects ARE web-renderable via
            // /dev/web-bundle/.
            notRenderableNotice={
              webPreviewStarting ||
              staticBundleState === "ready" ||
              staticBundleState === "building"
                ? null
                : notRenderable
            }
            // Expo RN projects can opt in to a sibling `expo --web`
            // process that doesn't disturb Metro's Hermes push. The
            // button only shows when we actually surfaced the
            // mobile-only notice AND the sibling isn't up yet.
            notRenderableAction={
              notRenderable && !webPreviewStarting && (devStatus?.framework || "").toLowerCase() === "expo" && !devStatus?.webPort
                ? {
                    label: "Start Expo Web preview (sibling of Metro)",
                    onClick: () => void handleStartWebPreview(),
                    disabled: false,
                  }
                : null
            }
            // While the sibling Expo Web process is bundling we replace
            // the iframe area with a progress UI — without this the user
            // stares at the "mobile-only" notice or a blank iframe for
            // 20-40s wondering whether anything is happening.
            bundlingState={
              staticBundleState === "building"
                ? {
                    label: staticBundleTransport?.phase
                      ? `Static bundle: ${staticBundleTransport.phase}…`
                      : `Compiling static web bundle for ${activeProject?.name || selectedApp || "this project"}…`,
                    detail: staticBundleTransport?.file
                      ? `${staticBundleTransport.file} · ${staticBundleTransport.pct.toFixed(1)}% (${staticBundleTransport.done}/${staticBundleTransport.total} bytes)`
                      : "expo export -p web → /dev/web-bundle/",
                    elapsedSec: Math.floor((Date.now() - (staticBundleStartRef.current || Date.now())) / 1000),
                    expectedSec: 60,
                  }
                : webPreviewStarting
                ? {
                    label: `Bundling Expo Web for ${devStatus?.workDir?.split("/").slice(-1)[0] || "this project"}…`,
                    detail: `${(devStatus?.framework || "expo").toLowerCase()} · sibling of Metro :${devStatus?.port || "?"}`,
                    elapsedSec: webPreviewElapsedSec,
                    expectedSec: 30,
                  }
                : null
            }
          />
          {webPreviewError ? (
            <div className="rounded border border-red-500/40 bg-red-500/10 px-3 py-1.5 text-[11px] text-red-200">
              Expo Web preview failed: {webPreviewError}
            </div>
          ) : null}
          {/* Static-bundle target (web-js-bundle) — alternative to the
              live HMR sibling. Compiles once via `expo export -p web`,
              serves through /dev/web-bundle/ with a path-rewrite injected
              into index.html so absolute asset URLs resolve through the
              relay-prefixed origin. Survives agent restarts that kill
              the sibling expo --web process. */}
          {isConnected && (activeProject || selectedApp || selectedProjectPath) ? (
            <div className="flex flex-wrap items-center gap-2 rounded border border-surface-800 bg-surface-900/40 px-3 py-1.5 text-[11px]">
              <span className="font-mono text-surface-300">target=web-js-bundle</span>
              <span className="text-surface-500">·</span>
              {staticBundleState === "idle" ? (
                <button
                  onClick={() => void handleBuildStaticBundle()}
                  className="rounded border border-sky-500/40 bg-sky-500/10 px-2 py-0.5 text-[11px] text-sky-200 hover:bg-sky-500/20"
                  title="Compile a static web bundle (expo export -p web) and load it into the iframe via /dev/web-bundle/. Doesn't depend on the sibling Expo Web process being alive — survives agent restarts cleanly."
                >
                  Build & render static bundle
                </button>
              ) : staticBundleState === "building" ? (
                <span className="text-amber-200">
                  Building… {staticBundleTransport?.phase ? `· ${staticBundleTransport.phase}` : ""}
                  {staticBundleTransport && staticBundleTransport.total > 0
                    ? ` · ${staticBundleTransport.pct.toFixed(1)}%`
                    : ""}
                </span>
              ) : staticBundleState === "ready" ? (
                <>
                  <span className="text-emerald-200">
                    Ready · {((staticBundleInfo?.size || 0) / 1024).toFixed(0)} KB · {staticBundleInfo?.fileCount || 0} files
                    {staticBundleTransport?.phase === "delivered" ? " · delivered" : ""}
                  </span>
                  <button
                    onClick={() => {
                      setStaticBundleState("idle");
                      setStaticBundleTransport(null);
                      setStaticBundleInfo(null);
                    }}
                    className="ml-auto rounded border border-surface-700 px-2 py-0.5 text-[10px] text-surface-300 hover:border-surface-500"
                    title="Drop the static bundle and return to the live HMR / dev preview iframe"
                  >
                    Drop bundle
                  </button>
                  <button
                    onClick={() => void handleBuildStaticBundle()}
                    className="rounded border border-sky-500/40 bg-sky-500/10 px-2 py-0.5 text-[10px] text-sky-200 hover:bg-sky-500/20"
                    title="Re-export and re-render"
                  >
                    Rebuild
                  </button>
                </>
              ) : (
                <>
                  <span className="text-red-300">Build failed: {staticBundleError || "unknown"}</span>
                  <button
                    onClick={() => void handleBuildStaticBundle()}
                    className="ml-auto rounded border border-sky-500/40 bg-sky-500/10 px-2 py-0.5 text-[10px] text-sky-200 hover:bg-sky-500/20"
                  >
                    Retry
                  </button>
                </>
              )}
            </div>
          ) : null}
          {devStatus?.webPort ? (
            <div className="flex items-center gap-2 rounded border border-emerald-500/30 bg-emerald-500/5 px-3 py-1.5 text-[11px] text-emerald-200">
              <span className="font-mono">expo --web · :{devStatus.webPort}</span>
              <span className="text-surface-500">(Metro on :{devStatus.port} is untouched)</span>
              <button
                onClick={() => void handleStopWebPreview()}
                className="ml-auto rounded border border-red-500/40 bg-red-500/10 px-2 py-0.5 text-[10px] text-red-200 hover:bg-red-500/20"
                title="Stop the sibling Expo Web process. Metro keeps serving Hermes bundles to the phone."
              >
                Stop web preview
              </button>
            </div>
          ) : null}

        </div>

        {/* Drag divider — only on xl, where the layout is side-by-side.
            Drag left to give the panel column more room, drag right to
            give the iframe more room. */}
        <div
          onMouseDown={handleDividerDragStart}
          className="hidden xl:flex w-1.5 shrink-0 cursor-col-resize items-stretch justify-center bg-transparent hover:bg-indigo-500/40 active:bg-indigo-500/60 transition-colors group"
          title="Drag to resize"
        >
          <div className="my-auto h-12 w-0.5 rounded bg-surface-800 group-hover:bg-indigo-400 transition-colors" />
        </div>

        {/* Right column — Console (top, expanded) → Projects (folded)
            → Vibing (folded). Width is drag-controlled on xl. */}
        <aside
          style={{ "--right-w": `${rightColumnWidth}px` } as React.CSSProperties}
          className="flex min-h-0 flex-col gap-3 xl:w-[var(--right-w)] xl:flex-shrink-0"
        >
          {/* Console — primary debugging surface, starts expanded.
              Stretches to fill remaining vertical space when expanded so
              long log streams don't squish the rest of the column. */}
          <div
            className={`rounded-md border border-surface-800 bg-surface-900/40 ${
              consoleExpanded ? "flex min-h-0 flex-1 flex-col p-3" : "p-2.5"
            }`}
          >
            <button
              onClick={() => setConsoleExpanded((v) => !v)}
              className="flex w-full items-center justify-between gap-2 text-[10px] font-semibold uppercase tracking-widest text-surface-400 hover:text-surface-200"
              title={consoleExpanded ? "Collapse" : "Expand"}
            >
              <span className="flex items-center gap-2">
                <span>Console</span>
                <span className="rounded bg-surface-800 px-1.5 py-0.5 text-[9px] normal-case tracking-normal text-surface-400">
                  {logs.length}
                </span>
              </span>
              <span className="text-surface-500">{consoleExpanded ? "▾" : "▸"}</span>
            </button>
            {consoleExpanded ? (
              <pre className="mt-2 min-h-0 flex-1 overflow-auto rounded-md border border-surface-800 bg-surface-950 px-3 py-2 font-mono text-[10px] leading-4 text-surface-400">
                {logs.length === 0 ? "(waiting for events…)" : logs.join("\n")}
              </pre>
            ) : null}
          </div>

          {/* Projects / Web apps — folded by default. */}
          <div className="rounded-md border border-surface-800 bg-surface-900/40 p-2.5">
            <button
              onClick={() => setProjectsExpanded((v) => !v)}
              className="flex w-full items-center justify-between gap-2 text-[10px] font-semibold uppercase tracking-widest text-surface-400 hover:text-surface-200"
              title={projectsExpanded ? "Collapse" : "Expand"}
            >
              <span className="flex items-center gap-2">
                <span>{useProjectFallback ? "Projects" : "Web apps in workspace"}</span>
                <span className="rounded bg-surface-800 px-1.5 py-0.5 text-[9px] normal-case tracking-normal text-surface-400">
                  {(useProjectFallback ? projects.length : apps.length) || 0}
                </span>
                {(selectedProject?.name || selectedApp) ? (
                  <span className="truncate normal-case tracking-normal text-[10px] text-indigo-300/80">
                    · {selectedProject?.name || selectedApp}
                  </span>
                ) : null}
              </span>
              <span className="text-surface-500">{projectsExpanded ? "▾" : "▸"}</span>
            </button>
            {projectsExpanded ? (
              <div className="mt-2 max-h-[15.5rem] overflow-auto pr-1">
                {useProjectFallback ? (
                  <ScannedProjectSelector
                    projects={projects}
                    selectedProjectPath={selectedProjectPath}
                    activeProjectPath={activeProject?.path ?? null}
                    onSelect={setSelectedProjectPath}
                    loading={projectsLoading}
                    workspaceError={workspaceError}
                  />
                ) : (
                  <WebAppSelector
                    apps={apps}
                    selectedApp={selectedApp}
                    activeApp={activeApp}
                    onSelect={setSelectedApp}
                    loading={workspaceLoading}
                    error={workspaceError}
                  />
                )}
              </div>
            ) : null}
          </div>

          {/* Vibing — folded by default. */}
          <div
            className={`rounded-md border border-surface-800 bg-surface-900/40 ${
              vibingExpanded ? "flex min-h-0 flex-col p-3" : "p-2.5"
            }`}
          >
            <button
              onClick={() => setVibingExpanded((v) => !v)}
              className="flex w-full items-center justify-between gap-2 text-[10px] font-semibold uppercase tracking-widest text-surface-400 hover:text-surface-200"
              title={vibingExpanded ? "Collapse" : "Expand"}
            >
              <span className="flex items-center gap-2">
                <span>Vibing</span>
                {activeTaskStream ? (
                  <span className="rounded-full border border-surface-700 bg-surface-950 px-2 py-0.5 text-[9px] normal-case tracking-[0.16em] text-surface-300">
                    {activeTaskStream.status}
                  </span>
                ) : null}
              </span>
              <span className="text-surface-500">{vibingExpanded ? "▾" : "▸"}</span>
            </button>
            {vibingExpanded ? (
              <>
                <div className="mt-2 mb-3 text-[11px] text-surface-500">
                  Send a repo-scoped task for the selected web app and keep the live output here.
                </div>
                <div className="min-h-0 max-h-64 overflow-auto rounded-md border border-surface-800 bg-surface-950/70 p-3">
                  {activeTaskStream ? (
                    <div className="space-y-2">
                      <div className="text-[11px] font-semibold text-surface-100">{activeTaskStream.title}</div>
                      <pre className="whitespace-pre-wrap font-mono text-[10px] leading-5 text-surface-300">
                        {activeTaskStream.lines.length === 0 ? "(waiting for output…)" : activeTaskStream.lines.join("\n")}
                      </pre>
                    </div>
                  ) : (
                    <div className="text-[11px] text-surface-500">Start a task and the runner stream will stay here.</div>
                  )}
                </div>
                <textarea
                  value={composer}
                  onChange={(event) => setComposer(event.target.value)}
                  placeholder="Fix the selected app, reload it here, and tell me the remote dev URL."
                  className="mt-3 min-h-24 w-full rounded-2xl border border-surface-700 bg-surface-950 px-4 py-3 text-sm text-surface-100 outline-none focus:border-indigo-500"
                />
                <div className="mt-3 flex items-center justify-between gap-3">
                  <div className="text-[11px] text-surface-500">
                    {selectedProject?.name || selectedApp || activeApp || "Pick an app first"} on {connectedDevice?.name || "this machine"}
                  </div>
                  <button
                    onClick={() => void handleSendPrompt()}
                    disabled={!composer.trim() || sending || (!selectedProject && !selectedApp && !activeApp && !devStatus?.workDir)}
                    className="rounded-xl bg-indigo-500 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-400 disabled:opacity-40"
                  >
                    {sending ? "Sending…" : "Send"}
                  </button>
                </div>
                {sendStatus ? <div className="mt-2 text-[11px] text-surface-400">{sendStatus}</div> : null}
              </>
            ) : null}
          </div>

          {/* Running — kept as a small always-visible status footer. */}
          <div className="rounded-md border border-surface-800 bg-surface-900/40 p-2 text-[11px]">
            <p className="text-[10px] uppercase tracking-widest text-surface-500">Running</p>
            <p className="mt-1 font-medium text-surface-100">
              {devStatus?.running ? (
                <>
                  {devStatus.framework} <span className="text-surface-500">· :{devStatus.port}</span>
                </>
              ) : (
                "No app running"
              )}
            </p>
            {devStatus?.workDir && (
              <p className="mt-0.5 truncate text-[10px] text-surface-500" title={devStatus.workDir}>
                {devStatus.workDir}
              </p>
            )}
          </div>
        </aside>
      </div>
    </div>
  );
}

// Web Reload only previews things the iframe can actually render. We
// exclude *only* projects whose primary target is a phone (Metro will
// just serve a JS bundle, not HTML, and the iframe paints blank). Anything
// else — including projects with no detected framework — is offered to
// the user. If the iframe gets a non-HTML response we already render the
// "not browser-renderable" stub, so an unknown project that turns out to
// be mobile-only fails politely instead of being silently hidden.
const WEB_RELOAD_DENY_FRAMEWORKS = new Set([
  "expo",
  "react-native",
  "metro",
  "swift",
  "kotlin",
  "flutter-mobile",
]);
const WEB_RELOAD_DENY_TAGS = new Set([
  "expo",
  "react-native",
  "metro",
  "ios",
  "android",
  "swift",
  "kotlin",
]);

function isMobileOnlyProject(project: ProjectRow): boolean {
  const framework = (project.framework || "").toLowerCase().trim();
  if (WEB_RELOAD_DENY_FRAMEWORKS.has(framework)) return true;
  const tags = (project.tags || []).map((t) => String(t || "").toLowerCase());
  if (tags.some((t) => WEB_RELOAD_DENY_TAGS.has(t))) return true;
  return false;
}

// Now retained as a thin alias so existing call sites compile. We no
// longer hide mobile-only projects — the picker shows them with a
// "Hot Reload" badge and disables Start so the user knows to switch
// tabs rather than wondering why their project vanished.
function isWebReloadProject(_project: ProjectRow): boolean {
  return true;
}

function ScannedProjectSelector({
  projects,
  selectedProjectPath,
  activeProjectPath,
  onSelect,
  loading,
  workspaceError,
}: {
  projects: ProjectRow[];
  selectedProjectPath: string | null;
  activeProjectPath: string | null;
  onSelect: (path: string) => void;
  loading?: boolean;
  workspaceError?: string | null;
}) {
  if (loading) {
    return (
      <div className="rounded-md border border-surface-800 bg-surface-900/40 px-3 py-4 text-[11px] text-surface-500">
        Scanning projects…
      </div>
    );
  }

  if (projects.length === 0) {
    return (
      <div className="rounded-md border border-surface-800 bg-surface-900/40 px-3 py-3 text-[11px] text-surface-500">
        <p>No web-previewable projects detected on this machine.</p>
        {workspaceError ? (
          <p className="mt-2 text-[10px] text-surface-600">{workspaceError}</p>
        ) : null}
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {workspaceError ? (
        <div className="rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-[10px] text-amber-200/80">
          {workspaceError}
        </div>
      ) : null}
      <div className="space-y-1">
        {projects.map((project) => {
          const isSelected = selectedProjectPath === project.path;
          const isActive = activeProjectPath === project.path;
          return (
            <button
              key={project.path}
              onClick={() => onSelect(project.path)}
              className={`group flex w-full items-center justify-between gap-2 rounded-md border px-2.5 py-2 text-left transition-colors ${
                isSelected
                  ? "border-indigo-500/40 bg-indigo-500/10"
                  : "border-surface-800 bg-surface-900/40 hover:border-surface-700 hover:bg-surface-900"
              }`}
              title={project.path}
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className={`truncate text-xs font-medium ${isSelected ? "text-indigo-200" : "text-surface-100"}`}>
                    {project.name}
                  </span>
                  {isActive ? (
                    <span className="flex items-center gap-1 text-[10px] text-emerald-300">
                      <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                      running
                    </span>
                  ) : null}
                </div>
                <div className="mt-0.5 flex items-center gap-2 text-[10px] text-surface-500">
                  <span className="rounded bg-surface-800 px-1 py-px uppercase tracking-wide">
                    {project.framework || "?"}
                  </span>
                  <span className="truncate">{project.path}</span>
                </div>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}
