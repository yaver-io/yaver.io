"use client";

import { useState, useEffect } from "react";
import { agentClient } from "@/lib/agent-client";

interface HealthTarget { id: string; url: string; name?: string; status?: string; responseTime?: number; }
type Machine = Awaited<ReturnType<typeof agentClient.machineHealth>>;
type Peer = Awaited<ReturnType<typeof agentClient.machinePeers>>[number];

export default function HealthView() {
  const [targets, setTargets] = useState<HealthTarget[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(true);
  const [machine, setMachine] = useState<Machine>(null);
  const [peers, setPeers] = useState<Peer[]>([]);

  useEffect(() => {
    loadTargets();
    loadMachine();
    const i = setInterval(() => {
      loadTargets();
      loadMachine();
    }, 15000);
    return () => clearInterval(i);
  }, []);

  async function loadTargets() {
    try { setTargets(await agentClient.listHealthTargets()); } catch {}
    setLoading(false);
  }

  async function loadMachine() {
    try {
      const [m, p] = await Promise.all([
        agentClient.machineHealth(),
        agentClient.machinePeers(),
      ]);
      setMachine(m);
      setPeers(p);
    } catch {}
  }

  async function addTarget() {
    if (!input.trim()) return;
    await agentClient.addHealthTarget({ url: input.trim(), name: input.trim() });
    setInput("");
    loadTargets();
  }

  async function deleteTarget(id: string) {
    await agentClient.deleteHealthTarget(id);
    loadTargets();
  }

  function statusColor(s?: string) {
    if (s === "up") return "bg-emerald-400";
    if (s === "warning") return "bg-amber-400";
    return "bg-red-400";
  }

  return (
    <div className="space-y-4">
      <div className="flex gap-2">
        <input value={input} onChange={(e) => setInput(e.target.value)} onKeyDown={(e) => e.key === "Enter" && addTarget()}
          placeholder="https://example.com" className="flex-1 rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 placeholder-surface-500 outline-none focus:border-indigo-500" />
        <button onClick={addTarget} className="px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400">Add</button>
      </div>

      {loading ? (
        <div className="text-center py-8 text-surface-500 text-sm">Loading...</div>
      ) : targets.length === 0 ? (
        <div className="text-center py-8 text-surface-500 text-sm">No health monitoring targets. Add a URL to start monitoring.</div>
      ) : (
        <div className="space-y-1">
          {targets.map((t) => (
            <div key={t.id} className="rounded-lg border border-surface-800 bg-surface-900/50 p-3 flex items-center gap-3">
              <div className={`w-2 h-2 rounded-full ${statusColor(t.status)}`} />
              <span className="flex-1 text-sm truncate">{t.url || t.name}</span>
              {t.responseTime != null && <span className="text-xs text-surface-500 font-mono">{t.responseTime}ms</span>}
              <span className="text-xs text-surface-500">{t.status || "unknown"}</span>
              <button onClick={() => deleteTarget(t.id)} className="text-surface-600 hover:text-red-400 text-xs">&#x2715;</button>
            </div>
          ))}
        </div>
      )}

      {machine && (
        <div className="mt-6">
          <h3 className="text-xs uppercase text-surface-500 font-semibold mb-2">
            Host: {machine.hostname} ({machine.os}) · last scan {machine.updatedAt?.slice(0, 19)}
          </h3>
          {machine.alerts && machine.alerts.length > 0 && (
            <div className="mb-3 rounded-lg border border-red-500/40 bg-red-900/20 p-3 space-y-1">
              {machine.alerts.map((a, i) => (
                <div key={i} className="text-xs text-red-400 font-semibold">⚠ {a}</div>
              ))}
            </div>
          )}

          <div className="space-y-1">
            {machine.filesystems.map((f) => {
              const tone =
                f.usedPct >= 95
                  ? "bg-red-400"
                  : f.usedPct >= 85
                    ? "bg-amber-400"
                    : "bg-emerald-400";
              return (
                <div key={f.mount} className="rounded-lg border border-surface-800 bg-surface-900/50 p-3">
                  <div className="flex items-center gap-3">
                    <span className="flex-1 text-sm truncate font-mono">{f.mount}</span>
                    <span className="text-xs text-surface-500">
                      {f.usedGb.toFixed(1)} / {f.totalGb.toFixed(1)} GB
                    </span>
                    <span className="text-xs font-mono">{Math.round(f.usedPct)}%</span>
                  </div>
                  <div className="mt-2 h-1 w-full bg-surface-800 rounded-full">
                    <div
                      className={`h-1 rounded-full ${tone}`}
                      style={{ width: `${Math.min(100, f.usedPct)}%` }}
                    />
                  </div>
                </div>
              );
            })}
          </div>

          {machine.drives.length > 0 && (
            <div className="mt-4 space-y-1">
              <h4 className="text-xs uppercase text-surface-500 font-semibold mb-1">SMART</h4>
              {machine.drives.map((d) => (
                <div key={d.device} className="rounded-lg border border-surface-800 bg-surface-900/50 p-3 flex items-center gap-3">
                  <div
                    className={`w-2 h-2 rounded-full ${d.health === "passed" ? "bg-emerald-400" : d.health === "failing" ? "bg-red-400" : "bg-surface-500"}`}
                  />
                  <span className="flex-1 text-sm truncate">
                    <span className="font-mono">{d.device}</span>
                    {d.model && <span className="text-surface-500"> · {d.model}</span>}
                  </span>
                  {d.temperatureC != null && d.temperatureC > 0 && (
                    <span className="text-xs text-surface-500">{d.temperatureC}°C</span>
                  )}
                  <span className="text-xs font-mono uppercase">{d.health}</span>
                </div>
              ))}
            </div>
          )}

          {peers.length > 0 && (
            <div className="mt-4 space-y-1">
              <h4 className="text-xs uppercase text-surface-500 font-semibold mb-1">Peer heartbeats</h4>
              {peers.map((p) => (
                <div key={p.deviceId} className="rounded-lg border border-surface-800 bg-surface-900/50 p-3 flex items-center gap-3">
                  <div
                    className={`w-2 h-2 rounded-full ${p.state === "online" ? "bg-emerald-400" : p.state === "offline" ? "bg-red-400" : "bg-amber-400"}`}
                  />
                  <span className="flex-1 text-sm truncate">{p.name || p.deviceId.slice(0, 8)}</span>
                  <span className="text-xs text-surface-500">{p.lastSeen?.slice(0, 19)}</span>
                  <span className="text-xs font-mono uppercase">{p.state}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
