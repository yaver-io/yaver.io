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
  const [relayRepairState, setRelayRepairState] = useState<"idle" | "repairing" | "repaired" | "failed">("idle");
  const [relayRepairMsg, setRelayRepairMsg] = useState<string | null>(null);
  const [recovering, setRecovering] = useState(false);
  const [recoveryLog, setRecoveryLog] = useState<string[]>([]);
  const [recoveryProgress, setRecoveryProgress] = useState<{ pct: number; stage: string; active: boolean }>({ pct: 0, stage: "", active: false });
  // Sibling Expo Web preview — spawned on demand so RN/Expo projects
  // can render in the browser iframe without touching Metro (which
  // keeps serving Hermes bundles to the phone). Status.webPort is
  // authoritative; these flags drive the CTA state.
  const [webPreviewStarting, setWebPreviewStarting] = useState(false);
  const [webPreviewError, setWebPreviewError] = useState<string | null>(null);
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
    try {
      // Always start in the framework's default mode (Expo gets
      // --dev-client, not --web) so Hot Reload (Hermes) keeps working
      // for a parallel mobile user. The web preview itself is a
      // sibling process the agent spawns when surface=web-reload.
      if (useProjectFallback && selectedProject) {
        await agentClient.startDevServer({
          framework: selectedProject.framework,
          workDir: selectedProject.path,
          projectName: selectedProject.name,
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
      <div className="grid min-h-0 flex-1 grid-cols-1 gap-3 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div className="relative flex min-h-0 flex-col gap-2">
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
            // Route the iframe at the Expo Web sibling when it's running,
            // otherwise fall back to the primary dev server preview.
            url={devStatus?.webPort && devStatus.webPort > 0 ? agentClient.devWebPreviewUrl : previewUrl}
            running={isRunning}
            onOpenInNewTab={previewUrl ? () => window.open(previewUrl, "_blank") : undefined}
            connectionLabel={connectionLabel}
            notRenderableNotice={notRenderable}
            // Expo RN projects can opt in to a sibling `expo --web`
            // process that doesn't disturb Metro's Hermes push. The
            // button only shows when we actually surfaced the
            // mobile-only notice AND the sibling isn't up yet.
            notRenderableAction={
              notRenderable && (devStatus?.framework || "").toLowerCase() === "expo" && !devStatus?.webPort
                ? {
                    label: webPreviewStarting ? "Starting Expo Web…" : "Start Expo Web preview (sibling of Metro)",
                    onClick: () => void handleStartWebPreview(),
                    disabled: webPreviewStarting,
                  }
                : null
            }
          />
          {webPreviewError ? (
            <div className="rounded border border-red-500/40 bg-red-500/10 px-3 py-1.5 text-[11px] text-red-200">
              Expo Web preview failed: {webPreviewError}
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

        {/* Right column — app selector + meta */}
        <aside className="flex min-h-0 flex-col gap-3">
          <div className="rounded-md border border-surface-800 bg-surface-900/40 p-3">
            <div className="mb-2 flex items-center justify-between gap-2">
              <p className="text-[10px] font-semibold uppercase tracking-widest text-surface-500">
                {useProjectFallback ? "Projects" : "Web apps in workspace"}
              </p>
              <span className="text-[10px] text-surface-600">
                {(useProjectFallback ? projects.length : apps.length) || 0} total
              </span>
            </div>
            <div className="max-h-[15.5rem] overflow-auto pr-1">
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
          </div>

          <div className="flex min-h-0 flex-1 flex-col rounded-md border border-surface-800 bg-surface-900/40 p-3">
            <div className="mb-2 flex items-center justify-between gap-2">
              <p className="text-[10px] font-semibold uppercase tracking-widest text-surface-500">Vibing</p>
              {activeTaskStream ? (
                <span className="rounded-full border border-surface-700 bg-surface-950 px-2 py-0.5 text-[9px] uppercase tracking-[0.16em] text-surface-300">
                  {activeTaskStream.status}
                </span>
              ) : null}
            </div>
            <div className="mb-3 text-[11px] text-surface-500">
              Send a repo-scoped task for the selected web app and keep the live output here.
            </div>
            <div className="min-h-0 flex-1 overflow-auto rounded-md border border-surface-800 bg-surface-950/70 p-3">
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
              className="mt-3 min-h-28 w-full rounded-2xl border border-surface-700 bg-surface-950 px-4 py-3 text-sm text-surface-100 outline-none focus:border-indigo-500"
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
          </div>

          <div className="rounded-md border border-surface-800 bg-surface-900/40 p-3">
            <p className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-surface-500">Console</p>
            <pre className="max-h-52 overflow-auto rounded-md border border-surface-800 bg-surface-950 px-3 py-2 font-mono text-[10px] leading-4 text-surface-400">
              {logs.length === 0 ? "(waiting for events…)" : logs.join("\n")}
            </pre>
          </div>

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
