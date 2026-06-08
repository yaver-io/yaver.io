"use client";

import { useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

type Target = {
  id: string;
  label: string;
  family: string;
  host: string;
  backend?: string;
  description: string;
  cost: string;
  requiresAccount: boolean;
};

type SwitchState = {
  id: string;
  fromBackend?: string;
  to: string;
  complexity: "trivial" | "easy" | "medium" | "hard";
  status: string;
  steps: { id: string; layer: string; title: string; status: string; error?: string; output?: string; description?: string }[];
  createdAt: string;
  finishedAt?: string;
  rollbackExpiresAt?: string;
  rewritePrompt?: string;
};

const complexityColors: Record<string, string> = {
  trivial: "bg-emerald-500/20 text-emerald-700 dark:text-emerald-300",
  easy: "bg-sky-500/20 text-sky-700 dark:text-sky-300",
  medium: "bg-amber-500/20 text-amber-700 dark:text-amber-300",
  hard: "bg-red-500/20 text-red-700 dark:text-red-300",
};

const statusColors: Record<string, string> = {
  pending: "text-surface-500",
  running: "text-amber-400 animate-pulse",
  done: "text-emerald-400",
  failed: "text-red-400",
  skipped: "text-surface-500",
  manual: "text-indigo-400",
  "rolled-back": "text-surface-400",
};

export default function SwitchView() {
  const [directory, setDirectory] = useState("");
  const [targets, setTargets] = useState<Target[]>([]);
  const [history, setHistory] = useState<SwitchState[]>([]);
  const [current, setCurrent] = useState<SwitchState | null>(null);
  const [running, setRunning] = useState(false);
  const [actionMsg, setActionMsg] = useState<string | null>(null);

  function cleanErr(e: unknown, fallback: string): string {
    const raw = typeof e === "string" ? e : e instanceof Error ? e.message : "";
    return raw.trim() && raw.trim().length <= 180 ? raw.trim() : fallback;
  }

  useEffect(() => { load(); }, [directory]);

  async function load() {
    try {
      const [t, h] = await Promise.all([
        agentClient.switchTargets(),
        agentClient.switchHistory(directory || undefined),
      ]);
      setTargets(t.targets || []);
      setHistory(h.switches || []);
    } catch {}
  }

  const [migrateModal, setMigrateModal] = useState<Target | null>(null);
  const [migrateData, setMigrateData] = useState(true);
  const [keepLocal, setKeepLocal] = useState(true);

  async function plan(target: string) {
    const t = targets.find(x => x.id === target);
    // For cloud migrations with Convex/Supabase, show the polished migrate modal first.
    if (t && (target === "convex-cloud" || target === "supabase-cloud" || target === "postgres-neon")) {
      setMigrateModal(t);
      return;
    }
    await doPlan(target);
  }

  async function doPlan(target: string) {
    setActionMsg(null);
    try {
      const s = await agentClient.switchPlan(target, { dryRun: false, directory: directory || undefined });
      if (s.error) { setActionMsg(cleanErr(s.error, "Couldn't build a switch plan for that target.")); return; }
      setCurrent(s);
      load();
    } catch (e) {
      setActionMsg(cleanErr(e, "Couldn't build a switch plan — the agent may be unreachable."));
    }
  }

  async function executeMigration() {
    if (!migrateModal) return;
    const target = migrateModal.id;
    setMigrateModal(null);
    await doPlan(target);
  }

  async function run(id: string) {
    setRunning(true);
    setActionMsg(null);
    try {
      const s = await agentClient.switchRun(id, directory || undefined);
      if (s.error) setActionMsg(cleanErr(s.error, "The switch run reported an error. Check the step details below."));
      setCurrent(s.state || s);
      load();
    } catch (e) {
      setActionMsg(cleanErr(e, "Couldn't run the switch — the agent may be unreachable."));
    } finally {
      setRunning(false);
    }
  }

  async function rollback(id: string) {
    if (!confirm("Roll back this switch? Git branch + env + data will be restored.")) return;
    setActionMsg(null);
    try {
      const s = await agentClient.switchRollback(id, directory || undefined);
      if (s.error) setActionMsg(cleanErr(s.error, "Rollback reported an error. Please check the history below."));
    } catch (e) {
      setActionMsg(cleanErr(e, "Couldn't roll back — the agent may be unreachable."));
    }
    load();
  }

  const grouped: Record<string, Target[]> = { postgres: [], convex: [], sqlite: [], app: [] };
  for (const t of targets) {
    (grouped[t.family] || (grouped[t.family] = [])).push(t);
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-2">
        <input
          value={directory}
          onChange={(e) => setDirectory(e.target.value)}
          placeholder="project directory (blank = agent cwd)"
          className="flex-1 min-w-[200px] rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200 placeholder-surface-500 outline-none focus:border-indigo-500"
        />
        <button onClick={load} className="px-3 py-2 text-sm rounded-lg bg-surface-800 text-surface-200 hover:bg-surface-700">Refresh</button>
      </div>

      {actionMsg && (
        <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-700 dark:text-red-300">{actionMsg}</div>
      )}

      {current && <CurrentPlan current={current} running={running} onRun={run} onClose={() => setCurrent(null)} />}

      {migrateModal && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4">
          <div className="bg-surface-950 border border-surface-700 rounded-xl p-5 max-w-lg w-full space-y-4">
            <div>
              <h3 className="text-lg font-semibold text-surface-100">
                Migrate to {migrateModal.label}
              </h3>
              <p className="text-xs text-surface-500 mt-1">
                Seamless switch — Yaver handles provisioning, credentials, and data migration.
              </p>
            </div>

            <div className="space-y-2 text-sm text-surface-300 bg-surface-900/50 border border-surface-800 rounded-lg p-3">
              <div className="font-semibold text-xs uppercase text-surface-500">This will:</div>
              {migrateModal.id === "convex-cloud" && <>
                <div>1. Run <code className="text-indigo-700 dark:text-indigo-300">npx convex deploy</code> to create/link your cloud project</div>
                <div>2. Capture <code>CONVEX_URL</code> + <code>CONVEX_DEPLOYMENT</code> into <code>.env.local</code></div>
                <div>3. Push functions + schema</div>
                {migrateData && <div>4. Stream-export local data → import to cloud</div>}
              </>}
              {migrateModal.id === "supabase-cloud" && <>
                <div>1. Create Supabase Cloud project via REST API (using your stored token)</div>
                <div>2. Wait for project to provision (ACTIVE_HEALTHY)</div>
                <div>3. Capture anon + service-role keys + DB password into <code>.env.local</code></div>
                {migrateData && <div>4. pg_dump local → pg_restore to cloud</div>}
              </>}
              {migrateModal.id === "postgres-neon" && <>
                <div>1. Create Neon project via REST API</div>
                <div>2. Capture <code>DATABASE_URL</code> into <code>.env.local</code></div>
                {migrateData && <div>3. pg_dump local → pg_restore to Neon</div>}
              </>}
              {keepLocal && <div className="text-emerald-400 text-xs pt-1">✓ Local backend stays intact — switch back anytime</div>}
            </div>

            <div className="space-y-2">
              <label className="flex items-center gap-2 text-sm cursor-pointer">
                <input type="checkbox" checked={migrateData} onChange={(e) => setMigrateData(e.target.checked)} />
                <span>Migrate existing data</span>
                <span className="text-xs text-surface-500">(pg_dump or streaming_export → target)</span>
              </label>
              <label className="flex items-center gap-2 text-sm cursor-pointer">
                <input type="checkbox" checked={keepLocal} onChange={(e) => setKeepLocal(e.target.checked)} />
                <span>Keep local backend as dev fallback</span>
                <span className="text-xs text-surface-500">(adds `local` env alongside `production`)</span>
              </label>
            </div>

            <div className="flex gap-2 pt-2">
              <button onClick={executeMigration}
                className="flex-1 px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400 font-semibold">
                Migrate now
              </button>
              <button onClick={() => setMigrateModal(null)}
                className="px-4 py-2 text-sm rounded-lg bg-surface-800 text-surface-200 hover:bg-surface-700">
                Cancel
              </button>
            </div>
            <div className="text-xs text-surface-500">
              ⓘ Migration is audited + reversible within 7 days via{" "}
              <code>yaver switch rollback</code>.
            </div>
          </div>
        </div>
      )}

      <section>
        <h2 className="text-xs uppercase text-surface-500 font-semibold mb-2">Switch to…</h2>
        {Object.entries(grouped).map(([family, list]) => (
          list.length === 0 ? null : (
            <div key={family} className="mb-4">
              <h3 className="text-xs uppercase text-indigo-400 font-semibold mb-2">{family}</h3>
              <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-2">
                {list.map((t) => (
                  <button
                    key={t.id}
                    onClick={() => plan(t.id)}
                    className="text-left bg-surface-900/50 border border-surface-800 hover:border-indigo-500 rounded-lg p-3 space-y-1 transition"
                  >
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-semibold text-surface-200 flex-1">{t.label}</span>
                      <span className="text-xs text-surface-500">{t.cost}</span>
                    </div>
                    <div className="text-xs text-surface-500 line-clamp-2">{t.description}</div>
                    {t.requiresAccount && <div className="text-[10px] text-amber-400">account required</div>}
                  </button>
                ))}
              </div>
            </div>
          )
        ))}
      </section>

      <section>
        <h2 className="text-xs uppercase text-surface-500 font-semibold mb-2">History</h2>
        {history.length === 0 && <div className="text-sm text-surface-500">No switches yet.</div>}
        <div className="space-y-1">
          {history.map((s) => (
            <div key={s.id} className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-3 text-sm">
              <span className={`px-2 py-0.5 rounded text-[10px] uppercase font-semibold ${complexityColors[s.complexity] || "bg-surface-800 text-surface-400"}`}>{s.complexity}</span>
              <span className="font-mono text-surface-300 truncate flex-1">{s.fromBackend || "?"} → {s.to}</span>
              <span className={`text-xs ${statusColors[s.status] || "text-surface-500"}`}>{s.status}</span>
              <span className="text-xs text-surface-500">{s.createdAt?.slice(0, 16)}</span>
              <button onClick={() => setCurrent(s)} className="text-xs text-indigo-400 hover:text-indigo-700 dark:hover:text-indigo-300">View</button>
              {s.status === "done" && (
                <button onClick={() => rollback(s.id)} className="text-xs text-red-400 hover:text-red-700 dark:hover:text-red-300">Rollback</button>
              )}
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}

function CurrentPlan({ current, running, onRun, onClose }: { current: SwitchState; running: boolean; onRun: (id: string) => void; onClose: () => void }) {
  const isDone = current.status === "done" || current.status === "failed" || current.status === "manual" || current.status === "rolled-back";
  return (
    <div className="rounded-lg border border-indigo-500/40 bg-indigo-900/10 p-4 space-y-3">
      <div className="flex items-center gap-3">
        <span className={`px-2 py-0.5 rounded text-[10px] uppercase font-semibold ${complexityColors[current.complexity] || "bg-surface-800"}`}>{current.complexity}</span>
        <span className="text-sm font-semibold text-surface-200 flex-1">{current.fromBackend || "?"} → {current.to}</span>
        <span className={`text-xs ${statusColors[current.status] || "text-surface-500"}`}>{current.status}</span>
        <button onClick={onClose} className="text-xs text-surface-500 hover:text-surface-300">Close</button>
      </div>

      {current.complexity === "hard" && current.rewritePrompt && (
        <div className="rounded-lg border border-amber-500/40 bg-amber-900/10 p-3 text-xs">
          <div className="font-semibold text-amber-700 dark:text-amber-300 mb-1">Paradigm switch — AI rewrite required</div>
          <div className="text-surface-400">
            Yaver can't auto-translate between these paradigms (SQL ↔ Convex, etc.). Running this switch emits a rewrite prompt to <code>.yaver/switches/{current.id}_rewrite.md</code> for your AI coding agent.
          </div>
        </div>
      )}

      <ol className="space-y-1">
        {current.steps.map((step, i) => (
          <li key={step.id} className="flex items-start gap-3 text-sm bg-surface-900/50 rounded-lg p-2">
            <span className="w-5 text-xs text-surface-600">{i + 1}.</span>
            <span className="px-1.5 py-0.5 rounded text-[9px] uppercase bg-surface-800 text-surface-400">{step.layer}</span>
            <div className="flex-1">
              <div className="font-mono text-surface-200">{step.title}</div>
              {step.description && <div className="text-xs text-surface-500 mt-1">{step.description}</div>}
              {step.output && <pre className="text-[10px] text-surface-500 mt-1 whitespace-pre-wrap">{step.output}</pre>}
              {step.error && <div className="text-xs text-red-400 mt-1">{step.error}</div>}
            </div>
            <span className={`text-xs ${statusColors[step.status] || "text-surface-500"}`}>{step.status}</span>
          </li>
        ))}
      </ol>

      {!isDone && (
        <div className="flex gap-2">
          <button onClick={() => onRun(current.id)} disabled={running} className="px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">
            {running ? "Running…" : "Execute switch"}
          </button>
        </div>
      )}
      {current.rollbackExpiresAt && (
        <div className="text-xs text-surface-500">Rollback available until {current.rollbackExpiresAt.slice(0, 10)}</div>
      )}
    </div>
  );
}
