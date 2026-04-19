"use client";

// ToolsView — dashboard tab that shows the connected machine's specs
// (RAM / CPU / disk / GPU) alongside an install catalogue so the user
// can one-click install ollama / aider / codex / claude-code / etc.
// onto their dev machine (or any paired peer) without touching a
// terminal. Progress streams live from /streams/install:<tool>.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { agentClient, type InfraSummary } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

type InstallEntry = { name: string; installed: boolean; description: string };

type Props = {
  /** Paired devices — used to populate the peer picker. Optional;
   *  when absent the view simply installs onto the currently-connected
   *  machine. */
  devices?: Device[];
};

const TOOL_META: Record<string, { emoji: string; tagline: string }> = {
  ollama: { emoji: "🦙", tagline: "Local LLM runtime — pulls models, serves them to aider + claude-code." },
  aider: { emoji: "🧑‍🔧", tagline: "Terminal pair-programmer. Powers the hybrid planner's implementer tier." },
  opencode: { emoji: "🪄", tagline: "Open-source coding agent, Claude-style UX." },
  "claude-code": { emoji: "🤖", tagline: "Anthropic's CLI agent — frontier-quality runner." },
  codex: { emoji: "🧠", tagline: "OpenAI Codex CLI — token-efficient daily driver." },
  hybrid: { emoji: "🪢", tagline: "Meta-install: aider + ollama + qwen2.5-coder:14b." },
  docker: { emoji: "🐳", tagline: "Containerise tasks — required for guest isolation + sandbox mode." },
  node: { emoji: "🟢", tagline: "Node.js — required for Expo / Vite / Next.js." },
  python: { emoji: "🐍", tagline: "Python 3 — ML tooling and some CLIs." },
  go: { emoji: "🐹", tagline: "Go toolchain — rebuild the agent / relay from source." },
  rust: { emoji: "🦀", tagline: "Rust toolchain — some runners + Hermes compiler." },
  git: { emoji: "🔀", tagline: "Version control — every scaffold depends on it." },
};

function fmtBytes(n?: number) {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let v = n, i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${u[i]}`;
}

function fmtUptime(s?: number) {
  if (!s) return "0m";
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  return `${m}m`;
}

export default function ToolsView({ devices = [] }: Props) {
  const [summary, setSummary] = useState<InfraSummary | null>(null);
  const [catalogue, setCatalogue] = useState<InstallEntry[]>([]);
  const [target, setTarget] = useState<string | undefined>(undefined);
  const [installing, setInstalling] = useState<string | null>(null);
  const [log, setLog] = useState<string[]>([]);
  const [result, setResult] = useState<{ tool: string; status: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const cancelStreamRef = useRef<(() => void) | null>(null);

  const peers = useMemo(
    () =>
      devices
        .filter((d) => d.online && d.deviceClass !== "edge-mobile")
        .map((d) => ({ id: d.id, name: d.name })),
    [devices],
  );

  const loadSummary = useCallback(async () => {
    try {
      setSummary(await agentClient.infraSummary(target));
    } catch {
      /* soft-fail */
    }
  }, [target]);

  const loadCatalogue = useCallback(async () => {
    try {
      setCatalogue(await agentClient.listInstallables(target));
    } catch {
      /* soft-fail */
    }
  }, [target]);

  useEffect(() => {
    loadSummary();
    loadCatalogue();
    const i = setInterval(() => {
      loadSummary();
      loadCatalogue();
    }, 15_000);
    return () => {
      clearInterval(i);
      cancelStreamRef.current?.();
    };
  }, [loadSummary, loadCatalogue]);

  async function runInstall(tool: string) {
    if (installing) return;
    setInstalling(tool);
    setLog([]);
    setResult(null);
    setError(null);
    const res = await agentClient.installTool(tool, target);
    if (!res.ok) {
      setError(res.error || "Install failed to start");
      setInstalling(null);
      return;
    }
    cancelStreamRef.current?.();
    cancelStreamRef.current = agentClient.streamLog(res.stream, (ev: any) => {
      if (ev.type === "line" && typeof ev.text === "string") {
        setLog((prev) => [...prev.slice(-299), ev.text]);
      } else if (ev.type === "result") {
        setResult({ tool, status: ev.status || "" });
        setInstalling(null);
        if (ev.status !== "ok" && ev.error) setError(ev.error);
        void loadCatalogue();
        void loadSummary();
      }
    });
  }

  const metrics = summary?.metrics;

  const sortedCatalogue = useMemo(
    () => [...catalogue].sort((a, b) => {
      // Missing tools first — the whole point of this view.
      if (a.installed !== b.installed) return a.installed ? 1 : -1;
      return a.name.localeCompare(b.name);
    }),
    [catalogue],
  );

  return (
    <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-surface-50">Tools &amp; Machine</h2>
        <p className="text-sm text-surface-400 mt-1">
          See what this dev machine is running on, then one-click install coding agents and local
          model runtimes without opening a terminal.
        </p>
      </div>

      {peers.length > 0 && (
        <div className="flex flex-wrap gap-2">
          <button
            onClick={() => setTarget(undefined)}
            className={`rounded-full px-3 py-1.5 text-xs font-semibold border ${
              !target
                ? "bg-indigo-500/15 text-indigo-300 border-indigo-500/40"
                : "bg-surface-900 text-surface-300 border-surface-800 hover:border-surface-700"
            }`}
          >
            This machine
          </button>
          {peers.map((p) => (
            <button
              key={p.id}
              onClick={() => setTarget(p.id)}
              className={`rounded-full px-3 py-1.5 text-xs font-semibold border ${
                target === p.id
                  ? "bg-indigo-500/15 text-indigo-300 border-indigo-500/40"
                  : "bg-surface-900 text-surface-300 border-surface-800 hover:border-surface-700"
              }`}
            >
              {p.name}
            </button>
          ))}
        </div>
      )}

      {summary && (
        <section className="rounded-2xl border border-surface-800 bg-surface-900/50 p-5">
          <div className="flex items-center gap-3 mb-4">
            <div
              className={`w-2.5 h-2.5 rounded-full ${
                summary.machine.isOnline ? "bg-emerald-400" : "bg-red-400"
              }`}
            />
            <h3 className="text-lg font-semibold text-surface-50">{summary.machine.name}</h3>
            <span className="text-xs text-surface-400">
              {summary.machine.platform}
              {summary.machine.arch ? ` · ${summary.machine.arch}` : ""}
            </span>
          </div>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            <Metric label="CPU" value={`${(metrics?.cpuPct || 0).toFixed(1)}%`} sub={`${metrics?.cores || 0} cores`} />
            <Metric
              label="RAM"
              value={`${(metrics?.ramPct || 0).toFixed(0)}%`}
              sub={`${fmtBytes(metrics?.ramUsed)} / ${fmtBytes(metrics?.ramTotal)}`}
            />
            <Metric
              label="Disk"
              value={`${(metrics?.diskPct || 0).toFixed(0)}%`}
              sub={`${fmtBytes(metrics?.diskUsed)} / ${fmtBytes(metrics?.diskTotal)}`}
            />
            <Metric label="Uptime" value={fmtUptime(metrics?.uptime)} sub={metrics?.hostname || summary.machine.deviceId} />
          </div>
        </section>
      )}

      <section>
        <h3 className="text-sm font-semibold text-surface-300 mb-3">Install catalogue</h3>
        {catalogue.length === 0 ? (
          <p className="text-sm text-surface-500">
            No install targets advertised. The connected agent may be below v1.98.0.
          </p>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2">
            {sortedCatalogue.map((entry) => {
              const meta = TOOL_META[entry.name] ?? { emoji: "⚙️", tagline: entry.description || "" };
              const isBusy = installing === entry.name;
              return (
                <div
                  key={entry.name}
                  className="rounded-2xl border border-surface-800 bg-surface-900/40 p-4 flex flex-col gap-3"
                >
                  <div className="flex gap-3">
                    <div className="text-2xl leading-none">{meta.emoji}</div>
                    <div className="flex-1">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-surface-50">{entry.name}</span>
                        <span
                          className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-bold ${
                            entry.installed
                              ? "bg-emerald-500/15 text-emerald-300"
                              : "bg-surface-800 text-surface-400"
                          }`}
                        >
                          {entry.installed ? "INSTALLED" : "NOT INSTALLED"}
                        </span>
                      </div>
                      <p className="text-xs text-surface-400 mt-1 leading-relaxed">
                        {meta.tagline || entry.description}
                      </p>
                    </div>
                  </div>
                  <button
                    onClick={() => void runInstall(entry.name)}
                    disabled={!!installing}
                    className={`rounded-lg px-3 py-2 text-xs font-semibold transition ${
                      entry.installed
                        ? "border border-surface-700 text-surface-300 hover:border-surface-600"
                        : "bg-indigo-500 text-white hover:bg-indigo-400"
                    } ${installing && !isBusy ? "opacity-50 cursor-not-allowed" : ""}`}
                  >
                    {isBusy ? "Installing…" : entry.installed ? "Reinstall / update" : "Install"}
                  </button>
                </div>
              );
            })}
          </div>
        )}
      </section>

      {(log.length > 0 || error) && (
        <section className="rounded-2xl border border-surface-800 bg-black p-4">
          <div className="text-[10px] font-bold text-surface-400 mb-2">
            {installing
              ? `INSTALLING · ${installing}`
              : result
                ? `LAST RUN · ${result.tool} · ${result.status}`
                : error
                  ? "ERROR"
                  : "LOG"}
          </div>
          {error && <div className="text-xs text-red-400 mb-2">{error}</div>}
          <div className="font-mono text-[11px] text-surface-200 leading-5 max-h-64 overflow-y-auto whitespace-pre-wrap">
            {log.slice(-300).map((line, i) => (
              <div key={i}>{line}</div>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function Metric({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="rounded-xl border border-surface-800 bg-surface-900/60 p-3">
      <div className="text-[10px] font-bold uppercase tracking-wider text-surface-500">{label}</div>
      <div className="text-2xl font-semibold text-surface-50 mt-1">{value}</div>
      {sub && <div className="text-[11px] text-surface-500 mt-1 truncate">{sub}</div>}
    </div>
  );
}
