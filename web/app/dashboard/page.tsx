"use client";

import { useAuth } from "@/lib/use-auth";
import { useDevices, type Device } from "@/lib/use-devices";
import { agentClient, type Task, type ConnectionState, type Runner, type AgentInfo } from "@/lib/agent-client";
import { CONVEX_URL } from "@/lib/constants";
import { useState, useEffect, useRef } from "react";
import ProjectsView from "@/components/dashboard/ProjectsView";
import TodosView from "@/components/dashboard/TodosView";
import BuildsView from "@/components/dashboard/BuildsView";
import HealthView from "@/components/dashboard/HealthView";
import QualityView from "@/components/dashboard/QualityView";
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
  const [activeTab, setActiveTab] = useState<"chat" | "projects" | "todos" | "builds" | "preview" | "health" | "quality">("chat");
  const [todoCount, setTodoCount] = useState(0);

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const outputRef = useRef<HTMLDivElement>(null);

  const isConnected = connState === "connected";

  useEffect(() => {
    (async () => { try { const r = await fetch(`${CONVEX_URL}/config`); if (r.ok) { const d = await r.json(); if (d.relayServers) agentClient.setRelayServers(d.relayServers); } } catch {} })();
  }, []);

  useEffect(() => { const u = agentClient.on("connectionState", setConnState); return u; }, []);

  useEffect(() => {
    const u = agentClient.on("output", (tid, line) => { setActiveTask(at => { if (at && tid === at.id) setOutputLines(p => [...p, line]); return at; }); });
    return u;
  }, []);

  useEffect(() => { if (outputRef.current) outputRef.current.scrollTop = outputRef.current.scrollHeight; }, [outputLines]);

  useEffect(() => {
    if (!isConnected) return;
    const load = async () => { try { setTasks(await agentClient.listTasks()); } catch {} };
    load(); const iv = setInterval(load, 5000); return () => clearInterval(iv);
  }, [isConnected]);

  useEffect(() => {
    if (!isConnected) return;
    const poll = async () => { try { setTodoCount(await agentClient.todoCount()); } catch {} };
    poll(); const iv = setInterval(poll, 10000); return () => clearInterval(iv);
  }, [isConnected]);

  // ── Actions ─────────────────────────────────────────────────────

  const connectToDevice = async (device: Device) => {
    if (!token) return;
    setConnectedDevice(device);
    try {
      await agentClient.connect(device.host, device.port, token, device.id);
      try { setAgentInfo(await agentClient.getInfo()); } catch {}
      try { setRunners(await agentClient.getRunners()); } catch {}
    } catch {}
  };

  const disconnect = () => { agentClient.disconnect(); setConnectedDevice(null); setAgentInfo(null); setTasks([]); setActiveTask(null); setOutputLines([]); setRunners([]); };

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
    { id: "chat", label: "Chat", icon: "\uD83D\uDCAC" },
    { id: "projects", label: "Projects", icon: "\uD83D\uDCC1" },
    { id: "todos", label: "Todos", icon: "\u2611\uFE0F", badge: todoCount },
    { id: "builds", label: "Builds", icon: "\uD83D\uDE80" },
    { id: "preview", label: "Preview", icon: "\uD83C\uDFA8" },
    { id: "health", label: "Health", icon: "\uD83D\uDCCA" },
    { id: "quality", label: "Quality", icon: "\u2705" },
  ];

  return (
    <div className="flex h-screen overflow-hidden">
      {/* Sidebar */}
      <aside className="w-60 flex flex-col border-r border-surface-800 bg-surface-900/50 shrink-0 overflow-y-auto">
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
        <div className="mt-auto border-t border-surface-800 p-3">
          <button onClick={logout} className="text-[11px] text-red-400 hover:text-red-300">Sign Out</button>
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
                <p className="mb-6 text-sm text-surface-500">Connect to a device running <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver serve</code></p>
                {devices.filter(d => d.online).map(d => (
                  <button key={d.id} onClick={() => connectToDevice(d)} className="mx-auto flex items-center gap-3 rounded-lg border border-surface-700 bg-surface-900 px-5 py-3 hover:border-emerald-500/30">
                    <span className="h-2 w-2 rounded-full bg-emerald-400" /><span className="text-sm font-medium text-surface-200">{d.name}</span>
                  </button>
                ))}
              </div>
            </div>
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
