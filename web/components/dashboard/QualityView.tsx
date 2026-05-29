"use client";

import { useState, useEffect } from "react";
import { agentClient } from "@/lib/agent-client";

interface QualityCheck { id?: string; type?: string; name?: string; status?: string; duration?: number; }

export default function QualityView() {
  const [checks, setChecks] = useState<QualityCheck[]>([]);
  const [loading, setLoading] = useState(true);
  const [running, setRunning] = useState(false);
  const [loadError, setLoadError] = useState(false);

  useEffect(() => { loadChecks(); }, []);

  async function loadChecks() {
    try {
      setChecks(await agentClient.listQualityGates());
      setLoadError(false);
    } catch {
      setLoadError(true);
    }
    setLoading(false);
  }

  async function runCheck(id: string) {
    await agentClient.runQualityGate(id);
    setTimeout(loadChecks, 1500);
  }

  async function runAll() {
    setRunning(true);
    await agentClient.runAllQualityGates();
    setTimeout(() => { loadChecks(); setRunning(false); }, 3000);
  }

  function statusColor(s?: string) {
    if (s === "passed") return "bg-emerald-500/10 text-emerald-400";
    if (s === "failed") return "bg-red-500/10 text-red-400";
    if (s === "warning") return "bg-amber-500/10 text-amber-400";
    return "bg-surface-800 text-surface-400";
  }

  return (
    <div className="space-y-4">
      <button onClick={runAll} disabled={running}
        className="px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">
        {running ? "Running..." : "Run All Checks"}
      </button>

      {loading ? (
        <div className="text-center py-8 text-surface-500 text-sm">Loading...</div>
      ) : loadError ? (
        <div className="text-center py-8 text-sm space-y-2">
          <div className="text-surface-400">Couldn't load quality checks — the agent may be unreachable.</div>
          <button onClick={loadChecks} className="text-xs px-3 py-1 rounded-md bg-surface-800 text-surface-300 hover:bg-surface-700">Retry</button>
        </div>
      ) : checks.length === 0 ? (
        <div className="text-center py-8 text-surface-500 text-sm">No quality checks detected for the current project.</div>
      ) : (
        <div className="space-y-1">
          {checks.map((c, i) => (
            <div key={c.id || i} className="rounded-lg border border-surface-800 bg-surface-900/50 p-3 flex items-center gap-3">
              <span className={`text-xs px-2 py-0.5 rounded-full ${statusColor(c.status)}`}>{c.status || "not run"}</span>
              <span className="flex-1 text-sm">{c.name || c.type || "check"}</span>
              {c.duration != null && <span className="text-xs text-surface-500">{(c.duration / 1000).toFixed(1)}s</span>}
              <button onClick={() => runCheck(c.id || c.type || "")} className="px-3 py-1 text-xs rounded-md bg-surface-800 text-surface-300 hover:bg-surface-700">Run</button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
