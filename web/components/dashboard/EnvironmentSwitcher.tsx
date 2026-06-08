"use client";

import { useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

/** Renders the Local / Staging / Production toggle for a project.
 *  Sits at the top of project detail pages (Ops tab, Projects tab, etc).
 *  Switching writes .env.local from .yaver/envs/<env>.env on the agent. */
export default function EnvironmentSwitcher({ directory, onSwitch }: { directory?: string; onSwitch?: (env: string) => void }) {
  const [active, setActive] = useState<string>("local");
  const [envs, setEnvs] = useState<string[]>(["local"]);
  const [editing, setEditing] = useState<string | null>(null);
  const [editBody, setEditBody] = useState("");
  const [loading, setLoading] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  useEffect(() => { refresh(); }, [directory]);

  async function refresh() {
    try {
      const r = await agentClient.projectEnvList(directory);
      setActive(r.active);
      setEnvs(r.envs);
    } catch {}
  }

  async function switchTo(name: string) {
    setLoading(true);
    setMsg(null);
    try {
      const r = await agentClient.projectEnvSwitch(name, directory);
      if (r.error) {
        const e = String(r.error);
        setMsg(e.trim() && e.length <= 160 ? e : `Couldn't switch to ${name}. Please try again.`);
        return;
      }
      setActive(name);
      onSwitch?.(name);
    } catch {
      setMsg(`Couldn't switch to ${name} — the agent may be unreachable.`);
    } finally {
      setLoading(false);
    }
  }

  async function openEdit(name: string) {
    const r = await agentClient.projectEnvLoad(name, directory);
    setEditBody(r.body || "");
    setEditing(name);
  }

  async function saveEdit() {
    if (!editing) return;
    await agentClient.projectEnvSave(editing, editBody, directory);
    setEditing(null);
    refresh();
  }

  async function addEnv() {
    const name = prompt("New environment name (e.g. staging, production):");
    if (!name) return;
    await agentClient.projectEnvSave(name, "# " + name + " environment\n", directory);
    refresh();
  }

  const iconFor = (n: string) =>
    n === "local" ? "🟢" : n === "staging" ? "☁️" : n === "production" ? "🚀" : "📦";
  const colorFor = (n: string, isActive: boolean) => {
    if (!isActive) return "bg-surface-900/50 border-surface-800 text-surface-400 hover:border-surface-600";
    if (n === "local") return "bg-emerald-500/20 border-emerald-500/40 text-emerald-700 dark:text-emerald-300";
    if (n === "staging") return "bg-sky-500/20 border-sky-500/40 text-sky-700 dark:text-sky-300";
    if (n === "production") return "bg-red-500/20 border-red-500/40 text-red-700 dark:text-red-300";
    return "bg-indigo-500/20 border-indigo-500/40 text-indigo-700 dark:text-indigo-300";
  };

  return (
    <div className="rounded-lg border border-surface-800 bg-surface-900/30 p-3 space-y-2">
      <div className="flex items-center gap-2">
        <span className="text-[10px] uppercase font-semibold text-surface-500 flex-shrink-0">Environment</span>
        <div className="flex gap-1 flex-1 flex-wrap">
          {envs.map((n) => (
            <button key={n} disabled={loading} onClick={() => switchTo(n)}
              onDoubleClick={() => openEdit(n)}
              title="double-click to edit"
              className={`px-3 py-1.5 text-xs rounded-lg border font-semibold transition ${colorFor(n, active === n)}`}>
              <span className="mr-1">{iconFor(n)}</span>{n}
              {active === n && <span className="ml-1">✓</span>}
            </button>
          ))}
        </div>
        <button onClick={addEnv} className="text-xs text-indigo-400 hover:text-indigo-700 dark:hover:text-indigo-300">+ add</button>
      </div>
      <div className="text-[10px] text-surface-500">
        Switching replaces <code>.env.local</code> with <code>.yaver/envs/{active}.env</code>.
        Double-click a chip to edit its contents.
      </div>
      {msg && <div className="text-xs text-red-400">{msg}</div>}

      {editing && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4">
          <div className="bg-surface-950 border border-surface-700 rounded-xl p-5 w-full max-w-2xl space-y-3">
            <h3 className="text-sm font-semibold">Edit {editing}.env</h3>
            <textarea value={editBody} onChange={(e) => setEditBody(e.target.value)}
              rows={16}
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-mono text-surface-200 outline-none focus:border-indigo-500" />
            <div className="flex gap-2">
              <button onClick={saveEdit} className="px-4 py-2 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400">Save</button>
              <button onClick={() => setEditing(null)} className="px-4 py-2 text-sm rounded bg-surface-800 text-surface-200">Cancel</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
