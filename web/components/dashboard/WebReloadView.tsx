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

import { useEffect, useMemo, useRef, useState } from "react";
import { agentClient, type WorkspaceAppView } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";
import { WebAppSelector } from "./WebAppSelector";
import { WebPreviewFrame } from "./WebPreviewFrame";

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
  const [showLogs, setShowLogs] = useState(false);
  const [relayRepairState, setRelayRepairState] = useState<"idle" | "repairing" | "repaired" | "failed">("idle");
  const [relayRepairMsg, setRelayRepairMsg] = useState<string | null>(null);

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

  // SSE: live dev-server logs. Use EventSource directly since the
  // events endpoint lives on the agent through the relay.
  const esRef = useRef<EventSource | null>(null);
  useEffect(() => {
    if (!isConnected) return;
    const url = agentClient.devEventsUrl;
    if (!url) return;
    const es = new EventSource(url);
    esRef.current = es;
    es.onmessage = (ev) => {
      try {
        const data = JSON.parse(ev.data);
        if (data?.logLine) {
          setLogs((prev) => [...prev.slice(-199), String(data.logLine)]);
        } else if (data?.message) {
          setLogs((prev) => [...prev.slice(-199), `[${data.type || "event"}] ${data.message}`]);
        }
      } catch { /* ignore parse errors */ }
    };
    es.onerror = () => { /* EventSource auto-reconnects */ };
    return () => { es.close(); esRef.current = null; };
  }, [isConnected]);

  const handleStart = async () => {
    if (!selectedApp && !selectedProject) return;
    setStarting(true);
    setStartError(null);
    try {
      if (useProjectFallback && selectedProject) {
        await agentClient.startDevServer({
          framework: selectedProject.framework,
          workDir: selectedProject.path,
          projectName: selectedProject.name,
          platform: "web",
          surface: "web-reload",
        });
      } else if (selectedApp) {
        await agentClient.startDevServer({
          app: selectedApp,
          surface: "web-reload",
        });
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
    try {
      await agentClient.stopDevServer();
      const s = await agentClient.getDevServerStatus();
      setDevStatus(s);
    } catch { /* surface via polling */ }
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
      await agentClient.reloadDevServer();
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err));
    }
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
          setNotRenderable({
            title: isMobile
              ? "This dev server is mobile-only — no browser preview available"
              : "Dev server response isn't browser-renderable",
            body: isMobile
              ? "Metro / Expo emit a JavaScript bundle, not HTML. Open the project on the Yaver mobile app to use Hot Reload, or switch the project to expo --web."
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
      {/* Header — device, app selector trigger, global actions */}
      <div className="flex flex-wrap items-center gap-3 rounded-md border border-surface-800 bg-surface-900/40 px-3 py-2">
        <div className="flex items-center gap-2 text-xs">
          <span className="h-2 w-2 rounded-full bg-emerald-400" />
          <span className="font-medium text-surface-100">{connectedDevice?.name}</span>
          <span className="text-[10px] uppercase tracking-widest text-surface-500">{connectionLabel}</span>
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
                className="rounded border border-red-500/40 bg-red-500/10 px-2.5 py-1 text-[11px] text-red-200 hover:bg-red-500/20"
                title="Stop serving this preview and return to the project picker"
              >
                {devStatus?.stopActionLabel || "Stop & switch"}
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
                  className="rounded border border-red-500/40 bg-red-500/5 px-2.5 py-1 text-[11px] text-red-200 hover:bg-red-500/15"
                  title="Force-stop any dev server the agent might still be running"
                >
                  Force Stop
                </button>
              )}
            </>
          )}
        </div>
      </div>

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

      {/* Split body */}
      <div className="grid min-h-0 flex-1 grid-cols-1 gap-3 md:grid-cols-[1fr_280px]">
        <div className="flex min-h-0 flex-col gap-2">
          <WebPreviewFrame
            url={previewUrl}
            running={isRunning}
            onOpenInNewTab={previewUrl ? () => window.open(previewUrl, "_blank") : undefined}
            connectionLabel={connectionLabel}
            notRenderableNotice={notRenderable}
          />

          {/* Logs strip */}
          <div className="rounded-md border border-surface-800 bg-surface-950/60">
            <button
              onClick={() => setShowLogs((v) => !v)}
              className="flex w-full items-center justify-between px-3 py-1.5 text-[10px] uppercase tracking-widest text-surface-400 hover:text-surface-200"
            >
              <span>Dev server logs ({logs.length})</span>
              <span>{showLogs ? "▾" : "▸"}</span>
            </button>
            {showLogs && (
              <pre className="max-h-40 overflow-auto border-t border-surface-800 bg-surface-950 px-3 py-2 font-mono text-[10px] leading-4 text-surface-400">
                {logs.length === 0 ? "(waiting for events…)" : logs.join("\n")}
              </pre>
            )}
          </div>
        </div>

        {/* Right column — app selector + meta */}
        <aside className="flex min-h-0 flex-col gap-3">
          <div>
            <p className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
              {useProjectFallback ? "Projects" : "Web apps in workspace"}
            </p>
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

          {devStatus?.running && (
            <div className="rounded-md border border-surface-800 bg-surface-900/40 p-2 text-[11px]">
              <p className="text-[10px] uppercase tracking-widest text-surface-500">Running</p>
              <p className="mt-1 font-medium text-surface-100">
                {devStatus.framework} <span className="text-surface-500">· :{devStatus.port}</span>
              </p>
              {devStatus.workDir && (
                <p className="mt-0.5 truncate text-[10px] text-surface-500" title={devStatus.workDir}>
                  {devStatus.workDir}
                </p>
              )}
            </div>
          )}
        </aside>
      </div>
    </div>
  );
}

// Web Reload only previews things the iframe can actually render. The
// match has to agree with the agent's FrameworkToDevServerKind so the
// picker never offers a project that /dev/start would then reject.
//
// Mobile-only frameworks (react-native, vanilla expo without a web
// build, kotlin, swift) are deliberately excluded — they have no HTML
// surface for the iframe and used to render as a blank Metro page.
const WEB_RELOAD_FRAMEWORK_HINTS = ["next", "nextjs", "vite", "astro", "remix"] as const;
const WEB_RELOAD_FRAMEWORK_EXACT = new Set([
  "next",
  "nextjs",
  "vite",
  "astro",
  "remix",
  "react",       // generic React (CRA / RSC) projects
  "flutter-web", // Flutter compiled to web, served on a port
]);

function isWebReloadProject(project: ProjectRow): boolean {
  const framework = (project.framework || "").toLowerCase().trim();
  if (!framework) return false;
  if (WEB_RELOAD_FRAMEWORK_EXACT.has(framework)) return true;
  for (const hint of WEB_RELOAD_FRAMEWORK_HINTS) {
    if (framework.includes(hint)) return true;
  }
  // Tags are advisory metadata — only honour them when the framework
  // field is empty-ish ("?") AND a tag explicitly asserts a web stack.
  // Loose tag-only matches let mobile projects sneak in.
  if (framework === "?" || framework === "unknown") {
    const tags = (project.tags || []).map((tag) => tag.toLowerCase());
    return WEB_RELOAD_FRAMEWORK_HINTS.some((hint) => tags.includes(hint));
  }
  return false;
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
