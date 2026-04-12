"use client";

import { useAuth } from "@/lib/use-auth";
import { useDevices, type Device } from "@/lib/use-devices";
import { agentClient, type Task, type ConnectionState, type Runner, type AgentInfo } from "@/lib/agent-client";
import { CONVEX_URL } from "@/lib/constants";
import { useState, useEffect, useRef } from "react";
import { useTheme } from "@/components/ThemeProvider";
import ProjectsView from "@/components/dashboard/ProjectsView";
import TodosView from "@/components/dashboard/TodosView";
import BuildsView from "@/components/dashboard/BuildsView";
import HealthView from "@/components/dashboard/HealthView";
import QualityView from "@/components/dashboard/QualityView";
import ConvexView from "@/components/dashboard/ConvexView";
import DataView from "@/components/dashboard/DataView";
import SwitchView from "@/components/dashboard/SwitchView";
import AccountsView from "@/components/dashboard/AccountsView";
import ConsoleView from "@/components/dashboard/ConsoleView";
import ObservabilityView from "@/components/dashboard/ObservabilityView";
import OpsView from "@/components/dashboard/OpsView";
import OverviewView from "@/components/dashboard/OverviewView";
import PreviewPane from "@/components/dashboard/PreviewPane";

function statusColor(s: string) {
  if (s === "running") return "text-amber-400";
  if (s === "completed") return "text-emerald-400";
  return "text-surface-400";
}

export default function DashboardPage() {
  // ── ALL hooks unconditionally at the top ────────────────────────
  const { user, token, isLoading, isAuthenticated, logout } = useAuth();
  const { devices, refreshDevices } = useDevices(token);
  const { theme, toggle: toggleTheme } = useTheme();

  const [connState, setConnState] = useState<ConnectionState>("disconnected");
  const [connectedDevice, setConnectedDevice] = useState<Device | null>(null);
  const [agentInfo, setAgentInfo] = useState<AgentInfo | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [outputLines, setOutputLines] = useState<string[]>([]);
  const [runners, setRunners] = useState<Runner[]>([]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [guestCode, setGuestCode] = useState("");
  const [activeTab, setActiveTab] = useState<"home" | "chat" | "projects" | "todos" | "builds" | "preview" | "health" | "quality" | "convex" | "data" | "switch" | "accounts" | "console" | "observ" | "ops">("home");
  const [todoCount, setTodoCount] = useState(0);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [relayReady, setRelayReady] = useState(false);

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const outputRef = useRef<HTMLDivElement>(null);
  const relayReadyPromiseRef = useRef<Promise<void> | null>(null);

  const isConnected = connState === "connected";

  useEffect(() => {
    let cancelled = false;
    let resolve: () => void;
    relayReadyPromiseRef.current = new Promise<void>(r => { resolve = r; });

    (async () => {
      try {
        // Fetch platform relay servers (already includes password)
        const r = await fetch(`${CONVEX_URL}/config`);
        let relays: any[] = [];
        if (r.ok) { const d = await r.json(); relays = d.relayServers || []; }

        // Fetch user settings to get relay password override
        if (token) {
          try {
            const sr = await fetch(`${CONVEX_URL}/settings`, { headers: { Authorization: `Bearer ${token}` } });
            if (sr.ok) {
              const sd = await sr.json();
              const pw = sd.settings?.relayPassword || sd.relayPassword;
              if (pw) { relays = relays.map((r: any) => ({ ...r, password: pw })); }
            }
          } catch {}
        }

        if (!cancelled && relays.length > 0) agentClient.setRelayServers(relays);
      } catch {}
      if (!cancelled) setRelayReady(true);
      resolve!();
    })();
    return () => { cancelled = true; };
  }, [token]);

  useEffect(() => { const u = agentClient.on("connectionState", setConnState); return u; }, []);

  useEffect(() => {
    const u = agentClient.on("output", (tid, line) => { setActiveTask(at => { if (at && tid === at.id) setOutputLines(p => [...p, line]); return at; }); });
    return u;
  }, []);

  useEffect(() => { if (outputRef.current) outputRef.current.scrollTop = outputRef.current.scrollHeight; }, [outputLines]);

  useEffect(() => {
    if (!isConnected) return;
    const load = async () => { try { setTasks(await agentClient.listTasks(20)); } catch {} };
    load(); const iv = setInterval(load, 10000); return () => clearInterval(iv);
  }, [isConnected]);

  useEffect(() => {
    if (!isConnected) return;
    const poll = async () => { try { setTodoCount(await agentClient.todoCount()); } catch {} };
    poll(); const iv = setInterval(poll, 30000); return () => clearInterval(iv);
  }, [isConnected]);

  // ── Actions ─────────────────────────────────────────────────────

  const connectToDevice = async (device: Device) => {
    if (!token) return;
    setConnectedDevice(device);
    setConnectError(null);

    // Wait for relay config to be loaded (web dashboard MUST use relay)
    if (relayReadyPromiseRef.current) {
      await relayReadyPromiseRef.current;
    }

    try {
      await agentClient.connect(device.host, device.port, token, device.id);
      try { setAgentInfo(await agentClient.getInfo()); } catch {}
      try { setRunners(await agentClient.getRunners()); } catch {}
    } catch (err: any) {
      setConnectError(err?.message || "Could not connect to device");
    }
  };

  const disconnect = () => { agentClient.disconnect(); setConnectedDevice(null); setAgentInfo(null); setTasks([]); setActiveTask(null); setOutputLines([]); setRunners([]); setConnectError(null); };

  const handleSend = async (e?: React.FormEvent) => {
    e?.preventDefault();
    const text = input.trim(); if (!text || sending) return;
    setInput(""); setSending(true);
    try {
      if (activeTask?.status === "running") { await agentClient.continueTask(activeTask.id, text); }
      else { const t = await agentClient.sendTask(text.slice(0, 80), text); setActiveTask(t); setOutputLines([]); setTasks(p => [t, ...p]); }
    } catch {} setSending(false);
  };

  const selectTask = (t: Task) => { setActiveTask(t); setOutputLines(t.output || []); setActiveTab("chat"); };
  const onTaskCreated = () => { setActiveTab("chat"); agentClient.listTasks().then(setTasks).catch(() => {}); };

  // ── Conditional renders (NO hooks below this point) ─────────────

  if (isLoading) return <div className="flex min-h-[80vh] items-center justify-center"><div className="h-6 w-6 animate-spin rounded-full border-2 border-surface-600 border-t-emerald-400" /></div>;

  if (!isAuthenticated) return (
    <div className="flex min-h-[80vh] items-center justify-center">
      <div className="text-center">
        <h2 className="text-lg font-semibold text-surface-100 mb-4">Sign in to continue</h2>
        <a href="/auth?return=/dashboard" className="rounded-lg bg-surface-100 px-6 py-3 text-sm font-medium text-surface-900 hover:bg-surface-50">Sign In</a>
      </div>
    </div>
  );

  const runningTask = tasks.find(t => t.status === "running");
  const tabs: { id: typeof activeTab; label: string; icon: string; badge?: number }[] = [
    { id: "home", label: "Home", icon: "\uD83C\uDFE0" },
    { id: "chat", label: "Chat", icon: "\uD83D\uDCAC" },
    { id: "projects", label: "Projects", icon: "\uD83D\uDCC1" },
    { id: "todos", label: "Todos", icon: "\u2611\uFE0F", badge: todoCount },
    { id: "builds", label: "Builds", icon: "\uD83D\uDE80" },
    { id: "preview", label: "Preview", icon: "\uD83C\uDFA8" },
    { id: "health", label: "Health", icon: "\uD83D\uDCCA" },
    { id: "quality", label: "Quality", icon: "\u2705" },
    { id: "data", label: "Data", icon: "\uD83D\uDDC4\uFE0F" },
    { id: "switch", label: "Switch", icon: "\uD83D\uDD04" },
    { id: "accounts", label: "Accounts", icon: "\uD83D\uDD11" },
    { id: "console", label: "Console", icon: "\uD83D\uDCBB" },
    { id: "observ", label: "Observ", icon: "\uD83D\uDCCA" },
    { id: "ops", label: "Ops", icon: "\uD83D\uDE80" },
    { id: "convex", label: "Convex", icon: "\u26A1" },
  ];

  return (
    <div className="flex flex-col md:flex-row h-screen overflow-hidden">
      {/* Mobile top bar — visible only below md */}
      <div className="md:hidden border-b border-surface-800 bg-surface-900/50">
        <div className="flex items-center gap-2 px-3 py-2">
          <div className="flex h-6 w-6 items-center justify-center rounded-full bg-surface-800 text-[10px] font-bold text-surface-300">{user?.email?.charAt(0).toUpperCase()}</div>
          <span className="text-xs font-medium text-surface-200 flex-1 truncate">{connectedDevice?.name || "No device"}</span>
          <span className={`h-1.5 w-1.5 rounded-full ${isConnected ? "bg-emerald-400" : "bg-surface-600"}`} />
        </div>
        <div className="flex overflow-x-auto no-scrollbar border-t border-surface-800">
          {tabs.map((t) => (
            <button key={t.id} onClick={() => setActiveTab(t.id)}
              className={`flex items-center gap-1 px-3 py-2 text-[11px] whitespace-nowrap ${activeTab === t.id ? "text-indigo-400 border-b-2 border-indigo-400" : "text-surface-400"}`}>
              <span>{t.icon}</span>{t.label}
              {t.badge != null && t.badge > 0 && <span className="ml-1 text-[9px] bg-indigo-500 text-white rounded-full px-1">{t.badge}</span>}
            </button>
          ))}
        </div>
      </div>

      {/* Sidebar — hidden on mobile */}
      <aside className="hidden md:flex w-60 flex-col border-r border-surface-800 bg-surface-900/50 shrink-0 overflow-y-auto">
        <div className="p-3 space-y-4">
          {/* User */}
          <div className="flex items-center gap-2 rounded-lg border border-surface-800/50 px-3 py-2">
            <div className="flex h-6 w-6 items-center justify-center rounded-full bg-surface-800 text-[10px] font-bold text-surface-300">{user?.email?.charAt(0).toUpperCase()}</div>
            <p className="truncate text-xs text-surface-300">{user?.name || user?.email}</p>
          </div>

          {/* Devices */}
          <div>
            <p className="text-[10px] font-semibold uppercase tracking-widest text-surface-500 mb-1">Device</p>
            {isConnected && connectedDevice ? (
              <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/5 px-3 py-2">
                <div className="flex items-center gap-1.5"><span className="h-1.5 w-1.5 rounded-full bg-emerald-400" /><span className="text-xs font-medium text-surface-200 truncate">{connectedDevice.name}</span></div>
                {agentInfo && <p className="text-[10px] text-surface-500 mt-0.5">v{agentInfo.version}</p>}
                <button onClick={disconnect} className="text-[10px] text-red-400 hover:text-red-300 mt-1">Disconnect</button>
              </div>
            ) : connState === "connecting" && connectedDevice ? (
              <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-3 py-2">
                <div className="flex items-center gap-1.5">
                  <span className="h-3 w-3 animate-spin rounded-full border border-amber-400 border-t-transparent" />
                  <span className="text-xs font-medium text-surface-300 truncate">{connectedDevice.name}</span>
                </div>
                <p className="text-[10px] text-amber-400/70 mt-1">Connecting via relay...</p>
                <button onClick={disconnect} className="text-[10px] text-surface-500 hover:text-surface-300 mt-1">Cancel</button>
              </div>
            ) : connState === "error" && connectedDevice ? (
              <div className="rounded-lg border border-red-500/20 bg-red-500/5 px-3 py-2">
                <div className="flex items-center gap-1.5"><span className="h-1.5 w-1.5 rounded-full bg-red-400" /><span className="text-xs font-medium text-surface-300 truncate">{connectedDevice.name}</span></div>
                <p className="text-[10px] text-red-400/70 mt-1">{connectError || "Connection failed"}</p>
                <div className="flex gap-2 mt-1">
                  <button onClick={() => connectToDevice(connectedDevice)} className="text-[10px] text-amber-400 hover:text-amber-300">Retry</button>
                  <button onClick={disconnect} className="text-[10px] text-surface-500 hover:text-surface-300">Cancel</button>
                </div>
              </div>
            ) : (
              <div className="space-y-1">
                {devices.filter(d => d.online).map(d => (
                  <button key={d.id} onClick={() => connectToDevice(d)} className="w-full flex items-center gap-2 rounded-md border border-surface-800 px-2 py-1.5 text-left hover:border-emerald-500/30 text-xs text-surface-300">
                    <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />{d.name}
                  </button>
                ))}
                {devices.filter(d => d.online).length === 0 && <p className="text-[10px] text-surface-600">No devices online</p>}
              </div>
            )}
          </div>

          {/* Guest code */}
          <div className="mb-4">
            <p className="text-[10px] font-semibold uppercase tracking-widest text-surface-500 mb-1">Join as Guest</p>
            <div className="flex gap-1.5">
              <input value={guestCode} onChange={e => setGuestCode(e.target.value.toUpperCase())} maxLength={6}
                placeholder="CODE" className="flex-1 rounded-md border border-surface-800 bg-surface-900 px-2 py-1.5 text-xs text-center font-mono tracking-widest text-surface-200 placeholder-surface-600 outline-none focus:border-indigo-500" />
              <button onClick={async () => {
                if (guestCode.trim().length < 4) return;
                try {
                  const res = await fetch(`${CONVEX_URL}/guests/accept-code`, { method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` }, body: JSON.stringify({ inviteCode: guestCode.trim() }) });
                  const data = await res.json();
                  if (data.ok || data.hostName) { alert(`Joined ${data.hostName || "host"}'s machine!`); setGuestCode(""); refreshDevices(); }
                  else alert(data.error || "Invalid code");
                } catch (e: any) { alert(e.message); }
              }} disabled={guestCode.trim().length < 4}
                className="rounded-md bg-indigo-500 px-3 py-1.5 text-[11px] font-medium text-white hover:bg-indigo-400 disabled:opacity-30">Join</button>
            </div>
          </div>

          {/* Tasks */}
          {tasks.length > 0 && (
            <div>
              <p className="text-[10px] font-semibold uppercase tracking-widest text-surface-500 mb-1">Tasks</p>
              {tasks.slice(0, 12).map(t => (
                <button key={t.id} onClick={() => selectTask(t)} className={`w-full text-left rounded-md px-2 py-1 text-[11px] truncate mb-0.5 ${activeTask?.id === t.id ? "bg-indigo-500/10 text-indigo-400" : "text-surface-400 hover:bg-surface-800"}`}>
                  <span className={`inline-block h-1 w-1 rounded-full mr-1 ${t.status === "running" ? "bg-amber-400" : t.status === "completed" ? "bg-emerald-400" : "bg-surface-600"}`} />{t.title}
                </button>
              ))}
            </div>
          )}
        </div>
        <div className="mt-auto border-t border-surface-800 p-3 flex items-center justify-between">
          <button onClick={logout} className="text-[11px] text-red-400 hover:text-red-300">Sign Out</button>
          <button onClick={toggleTheme} className="rounded-md p-1.5 text-surface-500 hover:text-surface-300 hover:bg-surface-800" title={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}>
            {theme === "dark" ? (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>
            ) : (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
            )}
          </button>
        </div>
      </aside>

      {/* Main */}
      <div className="flex flex-1 min-w-0 flex-col">
        {isConnected && (
          <div className="flex items-center gap-1 border-b border-surface-800 px-3 py-1 bg-surface-900/30 overflow-x-auto">
            {tabs.map(tab => (
              <button key={tab.id} onClick={() => setActiveTab(tab.id)} className={`shrink-0 rounded-md px-2.5 py-1 text-xs font-medium flex items-center gap-1 ${activeTab === tab.id ? "bg-indigo-500/10 text-indigo-400" : "text-surface-500 hover:text-surface-300 hover:bg-surface-800"}`}>
                <span>{tab.icon}</span><span className="hidden sm:inline">{tab.label}</span>
                {tab.badge ? <span className="rounded-full bg-indigo-500/20 px-1 text-[9px] text-indigo-400">{tab.badge}</span> : null}
              </button>
            ))}
            <div className="flex-1" />
            {runningTask && <button onClick={() => agentClient.stopTask(runningTask.id)} className="shrink-0 rounded-md border border-red-500/30 bg-red-500/10 px-2 py-0.5 text-[10px] text-red-300 hover:bg-red-500/20">Stop</button>}
          </div>
        )}

        <div className="flex flex-1 min-h-0 flex-col">
          {!isConnected ? (
            <div className="flex flex-1 items-center justify-center p-8">
              <div className="max-w-md text-center">
                <h2 className="mb-2 text-lg font-semibold text-surface-100">Workspace</h2>
                {connState === "connecting" ? (
                  <div className="flex flex-col items-center gap-3">
                    <div className="h-6 w-6 animate-spin rounded-full border-2 border-surface-600 border-t-amber-400" />
                    <p className="text-sm text-surface-400">Connecting to {connectedDevice?.name}...</p>
                    <p className="text-xs text-surface-600">Trying relay servers</p>
                  </div>
                ) : connState === "error" ? (
                  <div className="flex flex-col items-center gap-3">
                    <div className="rounded-lg border border-red-500/20 bg-red-500/5 px-4 py-3 text-left">
                      <p className="text-sm text-red-400 font-medium mb-1">Connection failed</p>
                      <p className="text-xs text-surface-500">{connectError || "Could not reach agent (direct or via relay)"}</p>
                      <p className="text-xs text-surface-600 mt-2">Make sure <code className="rounded bg-surface-800 px-1 py-0.5 text-surface-400">yaver serve</code> is running on your machine</p>
                    </div>
                    <div className="flex gap-2">
                      {connectedDevice && <button onClick={() => connectToDevice(connectedDevice)} className="rounded-md bg-amber-500/10 border border-amber-500/20 px-4 py-2 text-xs text-amber-400 hover:bg-amber-500/20">Retry</button>}
                      <button onClick={disconnect} className="rounded-md border border-surface-700 px-4 py-2 text-xs text-surface-400 hover:text-surface-300">Back</button>
                    </div>
                  </div>
                ) : (
                  <>
                    <p className="mb-6 text-sm text-surface-500">Connect to a device running <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver serve</code></p>
                    <div className="space-y-2">
                      {devices.filter(d => d.online).map(d => (
                        <button key={d.id} onClick={() => connectToDevice(d)} className="mx-auto flex items-center gap-3 rounded-lg border border-surface-700 bg-surface-900 px-5 py-3 hover:border-emerald-500/30 transition-colors">
                          <span className="h-2 w-2 rounded-full bg-emerald-400" /><span className="text-sm font-medium text-surface-200">{d.name}</span>
                        </button>
                      ))}
                    </div>
                    {devices.filter(d => d.online).length === 0 && <p className="text-sm text-surface-600 mt-4">No devices online</p>}
                  </>
                )}
              </div>
            </div>
          ) : activeTab === "home" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><OverviewView user={user ?? undefined} /></div>
          ) : activeTab === "projects" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><ProjectsView onTaskCreated={onTaskCreated} /></div>
          ) : activeTab === "todos" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><TodosView onTaskCreated={onTaskCreated} /></div>
          ) : activeTab === "builds" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><BuildsView onTaskCreated={onTaskCreated} /></div>
          ) : activeTab === "preview" ? (
            <div className="flex-1 min-h-0"><PreviewPane /></div>
          ) : activeTab === "health" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><HealthView /></div>
          ) : activeTab === "quality" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><QualityView /></div>
          ) : activeTab === "data" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><DataView /></div>
          ) : activeTab === "switch" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><SwitchView /></div>
          ) : activeTab === "accounts" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><AccountsView /></div>
          ) : activeTab === "console" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><ConsoleView /></div>
          ) : activeTab === "observ" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><ObservabilityView /></div>
          ) : activeTab === "ops" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><OpsView /></div>
          ) : activeTab === "convex" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><ConvexView /></div>
          ) : (
            <>
              <div className="flex flex-1 min-h-0">
                <div className="flex flex-1 min-w-0 flex-col">
                  {activeTask ? (
                    <>
                      <div className="flex items-center gap-3 border-b border-surface-800 px-4 py-2">
                        <span className={`h-1.5 w-1.5 rounded-full ${activeTask.status === "running" ? "animate-pulse bg-amber-400" : activeTask.status === "completed" ? "bg-emerald-400" : "bg-surface-600"}`} />
                        <span className="truncate text-sm font-medium text-surface-200">{activeTask.title}</span>
                        <span className={`text-[10px] ${statusColor(activeTask.status)}`}>{activeTask.status}</span>
                        {activeTask.costUsd != null && <span className="text-[10px] text-surface-600">${activeTask.costUsd.toFixed(3)}</span>}
                      </div>
                      <div ref={outputRef} className="flex-1 overflow-y-auto bg-surface-950 p-4 font-mono text-[12px] leading-5 text-surface-300">
                        {outputLines.length === 0 ? (
                          <div className="flex items-center gap-2 text-surface-600">
                            {activeTask.status === "running" && <span className="h-3 w-3 animate-spin rounded-full border border-surface-500 border-t-transparent" />}
                            {activeTask.status === "running" ? "Working..." : "No output"}
                          </div>
                        ) : outputLines.map((line, i) => (
                          <div key={i} className="whitespace-pre-wrap break-all">{line.startsWith("> ") ? <span className="text-emerald-400">{line}</span> : line}</div>
                        ))}
                      </div>
                    </>
                  ) : (
                    <div className="flex flex-1 items-center justify-center text-sm text-surface-600">Describe what you want to build</div>
                  )}
                </div>
              </div>
              <div className="border-t border-surface-800 bg-surface-900/50 p-3">
                <form onSubmit={handleSend} className="mx-auto flex max-w-4xl items-end gap-2">
                  <textarea ref={inputRef} value={input} onChange={e => setInput(e.target.value)}
                    onKeyDown={e => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); handleSend(); } }}
                    placeholder={runningTask ? "Follow up..." : "Build me a todo app..."} rows={1}
                    className="max-h-32 w-full resize-none rounded-lg border border-surface-700 bg-surface-850 px-4 py-2.5 text-sm text-surface-100 placeholder-surface-600 outline-none focus:border-surface-500" style={{ minHeight: "42px" }} />
                  <button type="submit" disabled={!input.trim() || sending}
                    className="shrink-0 rounded-lg bg-surface-100 px-4 py-2.5 text-sm font-medium text-surface-900 hover:bg-surface-50 disabled:opacity-30">
                    {sending ? "..." : runningTask ? "Send" : "Run"}
                  </button>
                </form>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
