"use client";

import { useAuth } from "@/lib/use-auth";
import { useDevices, type Device } from "@/lib/use-devices";
import {
  agentClient,
  type Task,
  type ConnectionState,
  type Runner,
  type AgentInfo,
} from "@/lib/agent-client";
import { CONVEX_URL } from "@/lib/constants";
import {
  useState,
  useEffect,
  useRef,
  useCallback,
  type FormEvent,
} from "react";
import Link from "next/link";

// ── Voice Recording Hook ──────────────────────────────────────────

function useVoiceRecorder() {
  const [recording, setRecording] = useState(false);
  const [processing, setProcessing] = useState(false);
  const mediaRecorder = useRef<MediaRecorder | null>(null);
  const chunks = useRef<Blob[]>([]);

  const start = useCallback(async () => {
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const mr = new MediaRecorder(stream, { mimeType: "audio/webm;codecs=opus" });
      chunks.current = [];
      mr.ondataavailable = (e) => {
        if (e.data.size > 0) chunks.current.push(e.data);
      };
      mediaRecorder.current = mr;
      mr.start();
      setRecording(true);
    } catch {
      // mic permission denied or unavailable
    }
  }, []);

  const stop = useCallback(async (): Promise<string> => {
    return new Promise((resolve) => {
      const mr = mediaRecorder.current;
      if (!mr || mr.state === "inactive") {
        resolve("");
        return;
      }
      mr.onstop = async () => {
        setRecording(false);
        setProcessing(true);
        const blob = new Blob(chunks.current, { type: "audio/webm" });
        try {
          const result = await agentClient.transcribeVoice(blob);
          resolve(result.text || "");
        } catch {
          resolve("");
        } finally {
          setProcessing(false);
          mr.stream.getTracks().forEach((t) => t.stop());
        }
      };
      mr.stop();
    });
  }, []);

  return { recording, processing, start, stop };
}

// ── Platform label helper ─────────────────────────────────────────

function platformLabel(p: string): string {
  switch (p.toLowerCase()) {
    case "darwin": return "macOS";
    case "linux": return "Linux";
    case "windows": return "Windows";
    default: return p;
  }
}

// ── Status badge colors ───────────────────────────────────────────

function connectionColor(s: ConnectionState): string {
  switch (s) {
    case "connected": return "bg-emerald-400";
    case "connecting": return "bg-amber-400 animate-pulse";
    case "error": return "bg-red-400";
    default: return "bg-surface-600";
  }
}

function statusColor(s: string): string {
  switch (s) {
    case "running": return "text-amber-400";
    case "completed": return "text-emerald-400";
    case "failed": case "stopped": return "text-red-400";
    default: return "text-surface-400";
  }
}

// ── Main Dashboard ────────────────────────────────────────────────

export default function DashboardPage() {
  const { user, token, isLoading, isAuthenticated, logout } = useAuth();
  const { devices, refreshDevices } = useDevices(token);

  // Connection
  const [connState, setConnState] = useState<ConnectionState>("disconnected");
  const [connectedDevice, setConnectedDevice] = useState<Device | null>(null);
  const [agentInfo, setAgentInfo] = useState<AgentInfo | null>(null);

  // Tasks
  const [tasks, setTasks] = useState<Task[]>([]);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [outputLines, setOutputLines] = useState<string[]>([]);

  // Runners
  const [runners, setRunners] = useState<Runner[]>([]);
  const [activeRunner, setActiveRunner] = useState<string>("");

  // Input
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const outputRef = useRef<HTMLDivElement>(null);

  // Sidebar
  const [sidebarOpen, setSidebarOpen] = useState(true);

  // Voice
  const voice = useVoiceRecorder();

  // Relay servers for connection
  const [relayServers, setRelayServers] = useState<any[]>([]);

  // ── Auth guard ──────────────────────────────────────────────────

  if (!isLoading && !isAuthenticated) {
    if (typeof window !== "undefined") window.location.href = "/auth";
    return null;
  }

  if (isLoading) {
    return (
      <div className="flex min-h-[80vh] items-center justify-center">
        <div className="h-6 w-6 animate-spin rounded-full border-2 border-surface-600 border-t-emerald-400" />
      </div>
    );
  }

  // ── Fetch relay servers from platform config ────────────────────
  // eslint-disable-next-line react-hooks/rules-of-hooks
  useEffect(() => {
    async function fetchRelays() {
      try {
        const res = await fetch(`${CONVEX_URL}/config`);
        if (res.ok) {
          const data = await res.json();
          if (data.relayServers) {
            setRelayServers(data.relayServers);
            agentClient.setRelayServers(data.relayServers);
          }
        }
      } catch { /* ignore */ }
    }
    fetchRelays();
  }, []);

  // ── Connection state listener ───────────────────────────────────
  // eslint-disable-next-line react-hooks/rules-of-hooks
  useEffect(() => {
    const unsub = agentClient.on("connectionState", (s) => {
      setConnState(s);
    });
    return unsub;
  }, []);

  // ── Output listener ─────────────────────────────────────────────
  // eslint-disable-next-line react-hooks/rules-of-hooks
  useEffect(() => {
    const unsub = agentClient.on("output", (taskId, line) => {
      if (activeTask && taskId === activeTask.id) {
        setOutputLines((prev) => [...prev, line]);
      }
    });
    return unsub;
  }, [activeTask]);

  // ── Auto-scroll output ──────────────────────────────────────────
  // eslint-disable-next-line react-hooks/rules-of-hooks
  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }
  }, [outputLines]);

  // ── Poll tasks when connected ───────────────────────────────────
  // eslint-disable-next-line react-hooks/rules-of-hooks
  useEffect(() => {
    if (connState !== "connected") return;
    const load = async () => {
      try {
        const t = await agentClient.listTasks();
        setTasks(t);
      } catch { /* ignore */ }
    };
    load();
    const iv = setInterval(load, 5000);
    return () => clearInterval(iv);
  }, [connState]);

  // ── Connect to device ───────────────────────────────────────────

  const connectToDevice = async (device: Device) => {
    if (connectedDevice?.id === device.id && connState === "connected") {
      agentClient.disconnect();
      setConnectedDevice(null);
      setAgentInfo(null);
      setTasks([]);
      setActiveTask(null);
      setOutputLines([]);
      setRunners([]);
      return;
    }

    setConnectedDevice(device);
    setActiveTask(null);
    setOutputLines([]);

    try {
      await agentClient.connect(device.host, device.port, token!, device.id);

      // Fetch agent info + runners
      try {
        const info = await agentClient.getInfo();
        setAgentInfo(info);
      } catch { /* ignore */ }

      try {
        const r = await agentClient.getRunners();
        setRunners(r);
        const active = r.find((x) => x.active);
        if (active) setActiveRunner(active.id);
      } catch { /* ignore */ }

      // Load tasks
      try {
        const t = await agentClient.listTasks();
        setTasks(t);
      } catch { /* ignore */ }
    } catch {
      // Connection failed — state listener handles UI
    }
  };

  // ── Switch runner ───────────────────────────────────────────────

  const handleSwitchRunner = async (runnerId: string) => {
    try {
      await agentClient.switchRunner(runnerId);
      setActiveRunner(runnerId);
    } catch { /* ignore */ }
  };

  // ── Send task ───────────────────────────────────────────────────

  const handleSend = async (e?: FormEvent) => {
    e?.preventDefault();
    const text = input.trim();
    if (!text || connState !== "connected") return;

    setSending(true);
    setInput("");

    try {
      // If there's an active running task, continue it
      if (activeTask && activeTask.status === "running") {
        await agentClient.continueTask(activeTask.id, text);
        setOutputLines((prev) => [...prev, `> ${text}`]);
      } else {
        // Create new task
        const task = await agentClient.sendTask(text, text);
        setActiveTask(task);
        setOutputLines([]);
        setTasks((prev) => [task, ...prev]);
      }
    } catch { /* ignore */ }

    setSending(false);
    inputRef.current?.focus();
  };

  // ── Voice record toggle ─────────────────────────────────────────

  const handleVoice = async () => {
    if (voice.recording) {
      const text = await voice.stop();
      if (text) setInput((prev) => (prev ? prev + " " + text : text));
    } else {
      await voice.start();
    }
  };

  // ── Select task from history ────────────────────────────────────

  const selectTask = async (task: Task) => {
    setActiveTask(task);
    try {
      const fresh = await agentClient.getTask(task.id);
      setActiveTask(fresh);
      setOutputLines(fresh.output || []);
    } catch {
      setOutputLines(task.output || []);
    }
  };

  // ── Stop task ───────────────────────────────────────────────────

  const handleStop = async () => {
    if (!activeTask) return;
    try {
      await agentClient.stopTask(activeTask.id);
      setActiveTask((prev) => prev ? { ...prev, status: "stopped" } : null);
    } catch { /* ignore */ }
  };

  // ── Keyboard shortcut ──────────────────────────────────────────

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  // ── Render ──────────────────────────────────────────────────────

  const isConnected = connState === "connected";
  const runningTask = activeTask?.status === "running";

  return (
    <div className="flex h-[calc(100vh-73px)] overflow-hidden">
      {/* ── Sidebar ─────────────────────────────────────────────── */}
      <aside
        className={`flex flex-col border-r border-surface-800 bg-surface-900/50 transition-all duration-200 ${
          sidebarOpen ? "w-72" : "w-0 overflow-hidden"
        }`}
      >
        <div className="flex min-h-0 flex-1 flex-col overflow-y-auto p-4">
          {/* Account */}
          <div className="mb-5">
            <div className="mb-2 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
              <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 6a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.501 20.118a7.5 7.5 0 0114.998 0A17.933 17.933 0 0112 21.75c-2.676 0-5.216-.584-7.499-1.632z" />
              </svg>
              Account
            </div>
            <div className="flex items-center gap-3 rounded-lg border border-surface-800/50 bg-surface-850/50 px-3 py-2.5">
              {user?.avatarUrl ? (
                <img src={user.avatarUrl} alt="" className="h-8 w-8 rounded-full ring-1 ring-surface-700" referrerPolicy="no-referrer" />
              ) : (
                <div className="flex h-8 w-8 items-center justify-center rounded-full bg-surface-800 text-xs font-bold text-surface-300">
                  {user?.email?.charAt(0).toUpperCase() || "?"}
                </div>
              )}
              <div className="min-w-0 flex-1">
                {user?.name && (
                  <p className="truncate text-sm font-medium text-surface-100">{user.name}</p>
                )}
                <p className="truncate text-[11px] text-surface-500">{user?.email}</p>
              </div>
              {user?.provider && (
                <span className="shrink-0 rounded bg-surface-800 px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wider text-surface-400">
                  {user.provider}
                </span>
              )}
            </div>
          </div>

          {/* Connection Status */}
          <div className="mb-5">
            <div className="mb-2 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
              <span className={`inline-block h-1.5 w-1.5 rounded-full ${connectionColor(connState)}`} />
              {connState === "connected" && connectedDevice
                ? connectedDevice.name
                : connState === "connecting"
                  ? "Connecting..."
                  : connState === "error"
                    ? "Connection Error"
                    : "Not Connected"}
            </div>
            {agentInfo && isConnected && (
              <div className="rounded-lg border border-surface-800/50 bg-surface-850/50 px-3 py-2 font-mono text-[11px] text-surface-400">
                <div className="flex justify-between">
                  <span className="text-surface-500">agent</span>
                  <span className="text-surface-200">v{agentInfo.version}</span>
                </div>
                <div className="mt-1 flex justify-between">
                  <span className="text-surface-500">workdir</span>
                  <span className="max-w-[140px] truncate text-surface-300" title={agentInfo.workDir}>
                    {agentInfo.workDir.split("/").pop()}
                  </span>
                </div>
              </div>
            )}
          </div>

          {/* Devices */}
          <div className="mb-5">
            <div className="mb-2 flex items-center justify-between">
              <span className="text-[10px] font-semibold uppercase tracking-widest text-surface-500">
                Devices
              </span>
              <button
                onClick={() => refreshDevices()}
                className="text-[10px] text-surface-600 transition-colors hover:text-surface-300"
              >
                refresh
              </button>
            </div>
            {devices.length === 0 ? (
              <p className="rounded-lg border border-dashed border-surface-800 px-3 py-4 text-center text-xs text-surface-600">
                No devices. Run <code className="rounded bg-surface-800 px-1 text-surface-400">yaver auth</code> on your machine.
              </p>
            ) : (
              <div className="space-y-1">
                {devices.map((d) => {
                  const isActive = connectedDevice?.id === d.id && isConnected;
                  return (
                    <button
                      key={d.id}
                      onClick={() => connectToDevice(d)}
                      disabled={!d.online && !isActive}
                      className={`group flex w-full items-center gap-2.5 rounded-lg border px-3 py-2 text-left transition-all ${
                        isActive
                          ? "border-emerald-500/30 bg-emerald-500/5"
                          : d.online
                            ? "border-surface-800/50 bg-surface-850/30 hover:border-surface-700 hover:bg-surface-850"
                            : "border-surface-800/30 opacity-40"
                      }`}
                    >
                      <div className="flex h-7 w-7 items-center justify-center rounded-md bg-surface-800/80 text-surface-400">
                        {d.platform === "iOS" || d.platform === "Android" ? (
                          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                            <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 1.5H8.25A2.25 2.25 0 006 3.75v16.5a2.25 2.25 0 002.25 2.25h7.5A2.25 2.25 0 0018 20.25V3.75a2.25 2.25 0 00-2.25-2.25H13.5m-3 0V3h3V1.5m-3 0h3m-3 18.75h3" />
                          </svg>
                        ) : (
                          <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                            <path strokeLinecap="round" strokeLinejoin="round" d="M9 17.25v1.007a3 3 0 01-.879 2.122L7.5 21h9l-.621-.621A3 3 0 0115 18.257V17.25m6-12V15a2.25 2.25 0 01-2.25 2.25H5.25A2.25 2.25 0 013 15V5.25A2.25 2.25 0 015.25 3h13.5A2.25 2.25 0 0121 5.25z" />
                          </svg>
                        )}
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-1.5">
                          <span className={`h-1.5 w-1.5 rounded-full ${d.online ? "bg-emerald-400" : "bg-surface-600"}`} />
                          <span className="truncate text-xs font-medium text-surface-200">{d.name}</span>
                        </div>
                        <span className="text-[10px] text-surface-500">
                          {platformLabel(d.platform)}
                          {isActive && " -- connected"}
                        </span>
                      </div>
                      {isActive && (
                        <span className="text-[9px] font-medium uppercase text-emerald-400">live</span>
                      )}
                    </button>
                  );
                })}
              </div>
            )}
          </div>

          {/* AI Runner */}
          {isConnected && runners.length > 0 && (
            <div className="mb-5">
              <div className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
                AI Runner
              </div>
              <div className="space-y-1">
                {runners.filter((r) => r.installed).map((r) => (
                  <button
                    key={r.id}
                    onClick={() => handleSwitchRunner(r.id)}
                    className={`flex w-full items-center gap-2 rounded-lg border px-3 py-1.5 text-left text-xs transition-all ${
                      r.id === activeRunner
                        ? "border-surface-600 bg-surface-800 text-surface-100"
                        : "border-surface-800/30 text-surface-400 hover:border-surface-700 hover:text-surface-200"
                    }`}
                  >
                    <span className={`h-1 w-1 rounded-full ${r.id === activeRunner ? "bg-emerald-400" : "bg-surface-600"}`} />
                    {r.name}
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* Voice */}
          {isConnected && (
            <div className="mb-5">
              <div className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
                Voice Input
              </div>
              <button
                onClick={handleVoice}
                disabled={voice.processing}
                className={`flex w-full items-center justify-center gap-2 rounded-lg border px-3 py-2 text-xs transition-all ${
                  voice.recording
                    ? "border-red-500/40 bg-red-500/10 text-red-300"
                    : voice.processing
                      ? "border-amber-500/30 bg-amber-500/5 text-amber-300"
                      : "border-surface-800/50 text-surface-400 hover:border-surface-600 hover:text-surface-200"
                }`}
              >
                {voice.recording ? (
                  <>
                    <span className="h-2 w-2 animate-pulse rounded-full bg-red-400" />
                    Recording... tap to stop
                  </>
                ) : voice.processing ? (
                  <>
                    <span className="h-3 w-3 animate-spin rounded-full border border-amber-400 border-t-transparent" />
                    Transcribing...
                  </>
                ) : (
                  <>
                    <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" d="M12 18.75a6 6 0 006-6v-1.5m-6 7.5a6 6 0 01-6-6v-1.5m6 7.5v3.75m-3.75 0h7.5M12 15.75a3 3 0 01-3-3V4.5a3 3 0 116 0v8.25a3 3 0 01-3 3z" />
                    </svg>
                    Tap to speak
                  </>
                )}
              </button>
            </div>
          )}

          {/* Spacer */}
          <div className="flex-1" />

          {/* Bottom links */}
          <div className="space-y-1 border-t border-surface-800/50 pt-3">
            <Link
              href="/download"
              className="flex items-center gap-2 rounded-lg px-3 py-1.5 text-xs text-surface-500 transition-colors hover:bg-surface-850 hover:text-surface-200"
            >
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3" />
              </svg>
              Download CLI
            </Link>
            <Link
              href="/docs"
              className="flex items-center gap-2 rounded-lg px-3 py-1.5 text-xs text-surface-500 transition-colors hover:bg-surface-850 hover:text-surface-200"
            >
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
              </svg>
              Documentation
            </Link>
            <button
              onClick={logout}
              className="flex w-full items-center gap-2 rounded-lg px-3 py-1.5 text-xs text-surface-600 transition-colors hover:bg-surface-850 hover:text-red-400"
            >
              <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15m3 0l3-3m0 0l-3-3m3 3H9" />
              </svg>
              Sign out
            </button>
          </div>
        </div>
      </aside>

      {/* ── Main Workspace ──────────────────────────────────────── */}
      <div className="flex min-w-0 flex-1 flex-col">
        {/* Toolbar */}
        <div className="flex items-center gap-3 border-b border-surface-800 px-4 py-2">
          <button
            onClick={() => setSidebarOpen(!sidebarOpen)}
            className="rounded-md p-1.5 text-surface-500 transition-colors hover:bg-surface-900 hover:text-surface-200"
            title={sidebarOpen ? "Collapse sidebar" : "Expand sidebar"}
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
              {sidebarOpen ? (
                <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5M3.75 17.25h16.5" />
              ) : (
                <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5" />
              )}
            </svg>
          </button>

          {isConnected && connectedDevice ? (
            <div className="flex items-center gap-2 text-xs">
              <span className={`h-1.5 w-1.5 rounded-full ${connectionColor(connState)}`} />
              <span className="font-medium text-surface-200">{connectedDevice.name}</span>
              {activeRunner && (
                <>
                  <span className="text-surface-700">/</span>
                  <span className="text-surface-400">{activeRunner}</span>
                </>
              )}
              {agentInfo?.workDir && (
                <>
                  <span className="text-surface-700">/</span>
                  <span className="font-mono text-[11px] text-surface-500">{agentInfo.workDir}</span>
                </>
              )}
            </div>
          ) : (
            <span className="text-xs text-surface-500">
              {connState === "connecting" ? "Connecting..." : "Select a device to start"}
            </span>
          )}

          <div className="flex-1" />

          {/* Task count */}
          {tasks.length > 0 && (
            <span className="rounded-md bg-surface-850 px-2 py-0.5 text-[10px] font-medium text-surface-400">
              {tasks.filter((t) => t.status === "running").length} running / {tasks.length} total
            </span>
          )}

          {runningTask && (
            <button
              onClick={handleStop}
              className="rounded-md border border-red-500/30 bg-red-500/10 px-2.5 py-1 text-[11px] font-medium text-red-300 transition-colors hover:bg-red-500/20"
            >
              Stop
            </button>
          )}
        </div>

        {/* ── Content Area ─────────────────────────────────────── */}
        <div className="flex min-h-0 flex-1 flex-col">
          {!isConnected ? (
            /* ── Empty state ── */
            <div className="flex flex-1 items-center justify-center p-8">
              <div className="max-w-md text-center">
                <div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-2xl border border-surface-800 bg-surface-900">
                  <svg className="h-7 w-7 text-surface-500" fill="none" viewBox="0 0 24 24" strokeWidth={1.2} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M6.75 7.5l3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0021 18V6a2.25 2.25 0 00-2.25-2.25H5.25A2.25 2.25 0 003 6v12a2.25 2.25 0 002.25 2.25z" />
                  </svg>
                </div>
                <h2 className="mb-2 text-lg font-semibold text-surface-100">Vibe Coding Workspace</h2>
                <p className="mb-6 text-sm text-surface-500">
                  Connect to a device running <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver serve</code> to start sending tasks to your AI agent.
                </p>
                {devices.filter((d) => d.online).length > 0 ? (
                  <div className="space-y-2">
                    <p className="text-xs font-medium text-surface-400">Available devices:</p>
                    {devices.filter((d) => d.online).map((d) => (
                      <button
                        key={d.id}
                        onClick={() => connectToDevice(d)}
                        className="mx-auto flex items-center gap-3 rounded-lg border border-surface-700 bg-surface-900 px-5 py-3 transition-colors hover:border-emerald-500/30 hover:bg-emerald-500/5"
                      >
                        <span className="h-2 w-2 rounded-full bg-emerald-400" />
                        <span className="text-sm font-medium text-surface-200">{d.name}</span>
                        <span className="text-xs text-surface-500">{platformLabel(d.platform)}</span>
                      </button>
                    ))}
                  </div>
                ) : (
                  <div className="space-y-3">
                    <p className="text-xs text-surface-500">No devices online.</p>
                    <Link href="/download" className="btn-secondary inline-flex px-4 py-2 text-xs">
                      Download Yaver CLI
                    </Link>
                  </div>
                )}
              </div>
            </div>
          ) : (
            <>
              {/* ── Task History + Output ── */}
              <div className="flex min-h-0 flex-1">
                {/* Task list sidebar (narrow) */}
                <div className="hidden w-56 shrink-0 flex-col border-r border-surface-800 lg:flex">
                  <div className="border-b border-surface-800 px-3 py-2 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
                    Tasks
                  </div>
                  <div className="flex-1 overflow-y-auto">
                    {tasks.length === 0 ? (
                      <p className="p-4 text-center text-[11px] text-surface-600">No tasks yet</p>
                    ) : (
                      tasks.map((t) => (
                        <button
                          key={t.id}
                          onClick={() => selectTask(t)}
                          className={`flex w-full items-start gap-2 border-b border-surface-800/50 px-3 py-2.5 text-left transition-colors hover:bg-surface-850 ${
                            activeTask?.id === t.id ? "bg-surface-850" : ""
                          }`}
                        >
                          <span className={`mt-1 h-1.5 w-1.5 shrink-0 rounded-full ${
                            t.status === "running" ? "animate-pulse bg-amber-400"
                              : t.status === "completed" ? "bg-emerald-400"
                              : t.status === "failed" || t.status === "stopped" ? "bg-red-400"
                              : "bg-surface-600"
                          }`} />
                          <div className="min-w-0">
                            <p className="truncate text-xs font-medium text-surface-200">{t.title}</p>
                            <p className="mt-0.5 text-[10px] text-surface-500">
                              <span className={statusColor(t.status)}>{t.status}</span>
                              {t.costUsd != null && (
                                <span className="ml-2 text-surface-600">${t.costUsd.toFixed(3)}</span>
                              )}
                            </p>
                          </div>
                        </button>
                      ))
                    )}
                  </div>
                </div>

                {/* Output area */}
                <div className="flex min-w-0 flex-1 flex-col">
                  {activeTask ? (
                    <>
                      {/* Task header */}
                      <div className="flex items-center gap-3 border-b border-surface-800 px-4 py-2">
                        <span className={`h-1.5 w-1.5 rounded-full ${
                          activeTask.status === "running" ? "animate-pulse bg-amber-400"
                            : activeTask.status === "completed" ? "bg-emerald-400"
                            : "bg-surface-600"
                        }`} />
                        <span className="truncate text-sm font-medium text-surface-200">{activeTask.title}</span>
                        <span className={`text-[10px] font-medium ${statusColor(activeTask.status)}`}>
                          {activeTask.status}
                        </span>
                        {activeTask.costUsd != null && (
                          <span className="text-[10px] text-surface-600">${activeTask.costUsd.toFixed(3)}</span>
                        )}
                      </div>

                      {/* Output stream */}
                      <div
                        ref={outputRef}
                        className="flex-1 overflow-y-auto bg-surface-950 p-4 font-mono text-[12px] leading-5 text-surface-300"
                      >
                        {outputLines.length === 0 ? (
                          <div className="flex items-center gap-2 text-surface-600">
                            {activeTask.status === "running" && (
                              <span className="h-3 w-3 animate-spin rounded-full border border-surface-500 border-t-transparent" />
                            )}
                            {activeTask.status === "running" ? "Waiting for output..." : "No output"}
                          </div>
                        ) : (
                          outputLines.map((line, i) => (
                            <div key={i} className="whitespace-pre-wrap break-all">
                              {line.startsWith("> ") ? (
                                <span className="text-emerald-400">{line}</span>
                              ) : (
                                line
                              )}
                            </div>
                          ))
                        )}
                      </div>
                    </>
                  ) : (
                    <div className="flex flex-1 items-center justify-center text-sm text-surface-600">
                      Type a message below to start a new task
                    </div>
                  )}
                </div>
              </div>

              {/* ── Input Area ─────────────────────────────────── */}
              <div className="border-t border-surface-800 bg-surface-900/50 p-3">
                <form
                  onSubmit={handleSend}
                  className="mx-auto flex max-w-4xl items-end gap-2"
                >
                  {/* Voice button */}
                  <button
                    type="button"
                    onClick={handleVoice}
                    disabled={voice.processing}
                    className={`shrink-0 rounded-lg border p-2.5 transition-all ${
                      voice.recording
                        ? "border-red-500/40 bg-red-500/10 text-red-300"
                        : "border-surface-700 text-surface-500 hover:border-surface-500 hover:text-surface-200"
                    }`}
                    title={voice.recording ? "Stop recording" : "Voice input"}
                  >
                    {voice.processing ? (
                      <span className="block h-4 w-4 animate-spin rounded-full border border-amber-400 border-t-transparent" />
                    ) : (
                      <svg className="h-4 w-4" fill={voice.recording ? "currentColor" : "none"} viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" d="M12 18.75a6 6 0 006-6v-1.5m-6 7.5a6 6 0 01-6-6v-1.5m6 7.5v3.75m-3.75 0h7.5M12 15.75a3 3 0 01-3-3V4.5a3 3 0 116 0v8.25a3 3 0 01-3 3z" />
                      </svg>
                    )}
                  </button>

                  {/* Text input */}
                  <div className="relative min-w-0 flex-1">
                    <textarea
                      ref={inputRef}
                      value={input}
                      onChange={(e) => setInput(e.target.value)}
                      onKeyDown={handleKeyDown}
                      placeholder={
                        runningTask
                          ? "Send follow-up to running task..."
                          : "Describe what you want to build..."
                      }
                      rows={1}
                      className="max-h-32 w-full resize-none rounded-lg border border-surface-700 bg-surface-850 px-4 py-2.5 pr-20 text-sm text-surface-100 placeholder-surface-600 outline-none transition-colors focus:border-surface-500"
                      style={{ minHeight: "42px" }}
                    />
                    <div className="absolute bottom-1.5 right-2 flex items-center gap-1">
                      <kbd className="hidden text-[9px] text-surface-700 sm:inline">
                        {runningTask ? "follow-up" : "enter"}
                      </kbd>
                    </div>
                  </div>

                  {/* Send button */}
                  <button
                    type="submit"
                    disabled={!input.trim() || sending}
                    className="shrink-0 rounded-lg bg-surface-100 px-4 py-2.5 text-sm font-medium text-surface-900 transition-colors hover:bg-surface-50 disabled:opacity-30 dark:bg-surface-200 dark:text-surface-950 dark:hover:bg-surface-100"
                  >
                    {sending ? (
                      <span className="block h-4 w-4 animate-spin rounded-full border-2 border-surface-600 border-t-surface-950" />
                    ) : runningTask ? (
                      "Send"
                    ) : (
                      "Run"
                    )}
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
