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

interface Props {
  connectedDevice: Device | null;
  connState: string;
}

export function WebReloadView({ connectedDevice, connState }: Props) {
  const [apps, setApps] = useState<WorkspaceAppView[]>([]);
  const [workspaceLoading, setWorkspaceLoading] = useState(false);
  const [workspaceError, setWorkspaceError] = useState<string | null>(null);
  const [selectedApp, setSelectedApp] = useState<string | null>(null);
  const [devStatus, setDevStatus] = useState<Awaited<ReturnType<typeof agentClient.getDevServerStatus>>>(null);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);
  const [logs, setLogs] = useState<string[]>([]);
  const [showLogs, setShowLogs] = useState(false);

  const isConnected = connState === "connected" && !!connectedDevice;

  // Load workspace apps on connect and whenever device changes.
  useEffect(() => {
    let cancelled = false;
    if (!isConnected) {
      setApps([]);
      setWorkspaceError(null);
      return;
    }
    setWorkspaceLoading(true);
    setWorkspaceError(null);
    agentClient
      .getWorkspaceApps("web,hybrid")
      .then((list) => {
        if (cancelled) return;
        setApps(list);
        if (list.length === 0) setWorkspaceError("No yaver.workspace.yaml found on the connected machine.");
      })
      .catch((err) => {
        if (cancelled) return;
        setWorkspaceError(String(err?.message ?? err));
      })
      .finally(() => {
        if (!cancelled) setWorkspaceLoading(false);
      });
    return () => { cancelled = true; };
  }, [isConnected, connectedDevice?.id]);

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
  useEffect(() => {
    if (!selectedApp && activeApp) setSelectedApp(activeApp);
  }, [activeApp, selectedApp]);

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
    if (!selectedApp) return;
    setStarting(true);
    setStartError(null);
    try {
      await agentClient.startDevServer({
        app: selectedApp,
        surface: "web-reload",
      });
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
          {isRunning ? (
            <>
              <button
                onClick={handleReload}
                className="rounded border border-surface-700 px-2.5 py-1 text-[11px] text-surface-200 hover:bg-surface-800"
              >
                Hard reload
              </button>
              <button
                onClick={handleStop}
                className="rounded border border-red-500/40 bg-red-500/10 px-2.5 py-1 text-[11px] text-red-200 hover:bg-red-500/20"
              >
                Stop
              </button>
            </>
          ) : (
            <button
              onClick={handleStart}
              disabled={!selectedApp || starting}
              className="rounded border border-emerald-500/40 bg-emerald-500/10 px-3 py-1 text-[11px] font-medium text-emerald-200 hover:bg-emerald-500/20 disabled:opacity-50"
            >
              {starting ? "Starting…" : selectedApp ? `Start ${selectedApp}` : "Pick an app"}
            </button>
          )}
        </div>
      </div>

      {startError && (
        <div className="rounded-md border border-red-500/40 bg-red-500/5 px-3 py-2 text-[11px] text-red-200">
          {startError}
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
              Web apps in workspace
            </p>
            <WebAppSelector
              apps={apps}
              selectedApp={selectedApp}
              activeApp={activeApp}
              onSelect={setSelectedApp}
              loading={workspaceLoading}
              error={workspaceError}
            />
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
