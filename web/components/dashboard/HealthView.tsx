"use client";

import { useState, useEffect } from "react";
import { agentClient } from "@/lib/agent-client";

interface HealthTarget { id: string; url: string; name?: string; status?: string; responseTime?: number; }

export default function HealthView() {
  const [targets, setTargets] = useState<HealthTarget[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => { loadTargets(); const i = setInterval(loadTargets, 15000); return () => clearInterval(i); }, []);

  async function loadTargets() {
    try { setTargets(await agentClient.listHealthTargets()); } catch {}
    setLoading(false);
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
    </div>
  );
}
