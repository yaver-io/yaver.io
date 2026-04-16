"use client";

import { useCallback, useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";
import type { MorningRunSummary, MorningTaskHighlight } from "@/lib/agent-client";

/**
 * MorningView — the "good morning, here's what shipped" match-report
 * tab in the web dashboard. Renders every run's task cards with an
 * inline `<video>` (byte-range streamed from the agent through the
 * relay), a rollback button, and a short git-stats row.
 *
 * All data goes through the existing agentClient, which means this
 * view works the same way from a yaver-to-yaver viewer (one Mac
 * looking at another's overnight rig) as it does on the owner's own
 * machine — no special transport.
 */
export default function MorningView() {
  const [runs, setRuns] = useState<MorningRunSummary[]>([]);
  const [selected, setSelected] = useState<MorningRunSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [rolling, setRolling] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    const list = await agentClient.listMorningRuns(20);
    setRuns(list);
    if (list.length > 0) {
      const latest = await agentClient.getMorningRun(list[0].runId);
      setSelected(latest);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const selectRun = async (runId: string) => {
    const full = await agentClient.getMorningRun(runId);
    setSelected(full);
  };

  const rollback = async (run: MorningRunSummary, task: MorningTaskHighlight) => {
    if (!confirm(`Revert "${task.title}"? This creates a new revert commit.`)) return;
    setRolling(task.taskId);
    const res = await agentClient.rollbackMorningTask(run.runId, task.taskId);
    setRolling(null);
    if (!res.ok) {
      alert(`Rollback failed: ${res.error ?? "unknown"}`);
      return;
    }
    await selectRun(run.runId);
  };

  return (
    <div className="flex h-full flex-1 gap-4 overflow-hidden p-4">
      <aside className="w-64 shrink-0 overflow-y-auto rounded-lg border border-surface-800 bg-surface-900/40 p-2">
        <div className="mb-2 flex items-center justify-between px-1">
          <h2 className="text-xs font-semibold uppercase tracking-widest text-surface-400">Overnight runs</h2>
          <button onClick={refresh} className="text-[10px] text-surface-500 hover:text-surface-200">refresh</button>
        </div>
        {loading && runs.length === 0 ? (
          <p className="px-2 py-4 text-xs text-surface-500">Loading…</p>
        ) : runs.length === 0 ? (
          <div className="px-2 py-4">
            <p className="text-xs text-surface-500">No runs yet.</p>
            <p className="mt-1 text-[11px] text-surface-600">
              Start <code className="rounded bg-surface-800 px-1">yaver autodev --morning</code> before bed to get a match report in the morning.
            </p>
          </div>
        ) : (
          <ul className="space-y-1">
            {runs.map(r => (
              <li key={r.runId}>
                <button
                  onClick={() => selectRun(r.runId)}
                  className={`w-full rounded-md px-2 py-1.5 text-left text-xs transition-colors ${
                    selected?.runId === r.runId
                      ? "bg-indigo-500/15 text-indigo-200"
                      : "text-surface-300 hover:bg-surface-800"
                  }`}
                >
                  <div className="truncate font-medium">{r.project || r.runId}</div>
                  <div className="text-[10px] text-surface-500">
                    {r.stats.tasksShipped} shipped · {r.stats.tasksFailed} failed · {formatCost(r.stats.totalCostUsd)}
                  </div>
                  <div className="text-[10px] text-surface-600">{formatStarted(r.startedAt)}</div>
                </button>
              </li>
            ))}
          </ul>
        )}
      </aside>

      <section className="flex-1 overflow-y-auto">
        {selected ? (
          <RunDetail run={selected} rolling={rolling} onRollback={(task) => rollback(selected, task)} />
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-surface-500">
            Select a run on the left.
          </div>
        )}
      </section>
    </div>
  );
}

function RunDetail({
  run,
  rolling,
  onRollback,
}: {
  run: MorningRunSummary;
  rolling: string | null;
  onRollback: (task: MorningTaskHighlight) => void;
}) {
  return (
    <div className="max-w-4xl">
      <header className="mb-4 border-b border-surface-800 pb-3">
        <h1 className="text-lg font-semibold text-surface-100">☀ {run.project}</h1>
        <p className="text-xs text-surface-500">
          {formatStarted(run.startedAt)} · {run.stats.totalMinutes}m · {run.stats.tasksShipped} shipped · {run.stats.tasksFailed} failed · {run.stats.tasksRolledBack} rolled back · {formatCost(run.stats.totalCostUsd)}
        </p>
      </header>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        {run.tasks.map(task => (
          <TaskCard
            key={task.taskId}
            task={task}
            videoUrl={task.hasVideo ? agentClient.morningVideoUrl(run.runId, task.taskId) : null}
            rollingState={rolling === task.taskId}
            onRollback={() => onRollback(task)}
          />
        ))}
      </div>
    </div>
  );
}

function TaskCard({
  task,
  videoUrl,
  rollingState,
  onRollback,
}: {
  task: MorningTaskHighlight;
  videoUrl: string | null;
  rollingState: boolean;
  onRollback: () => void;
}) {
  const statusChip = chipColor(task.status);
  return (
    <article className="overflow-hidden rounded-xl border border-surface-800 bg-surface-900/50">
      <header className="flex items-center justify-between border-b border-surface-800 px-3 py-2 text-[10px]">
        <span className={`rounded px-1.5 py-[2px] font-semibold uppercase tracking-wider ${statusChip}`}>
          {task.status}
        </span>
        <span className="text-surface-500">
          {task.costUsd ? `$${task.costUsd.toFixed(3)}` : ""}
        </span>
      </header>

      {videoUrl ? (
        <video
          src={videoUrl}
          controls
          muted
          playsInline
          className="aspect-video w-full bg-black"
        />
      ) : (
        <div className="flex aspect-video w-full items-center justify-center border-b border-surface-800 bg-surface-950 text-xs text-surface-600">
          no video
        </div>
      )}

      <div className="space-y-2 p-3 text-sm">
        <p className="font-medium text-surface-100">{task.title}</p>
        {task.oneLineSummary && <p className="text-xs text-surface-400">{task.oneLineSummary}</p>}
        <p className="text-[11px] text-surface-500">
          {(task.filesChanged ?? 0)} files · +{task.linesAdded ?? 0} / -{task.linesRemoved ?? 0}
          {task.headSha ? ` · ${task.headSha.slice(0, 8)}` : ""}
        </p>
        {task.rolledBackAt && (
          <p className="text-[11px] text-amber-400">
            rolled back · revert {task.revertSha?.slice(0, 8) ?? ""}
          </p>
        )}
        {task.failureNote && <p className="text-[11px] text-red-400">{task.failureNote}</p>}
      </div>

      <footer className="flex items-center justify-end gap-2 border-t border-surface-800 px-3 py-2">
        <button
          disabled={task.status === "rolled-back" || !task.commitShas?.length || rollingState}
          onClick={onRollback}
          className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-1 text-[11px] text-red-300 hover:bg-red-500/20 disabled:cursor-not-allowed disabled:opacity-40"
        >
          {rollingState ? "Rolling back…" : "Rollback"}
        </button>
      </footer>
    </article>
  );
}

function chipColor(status: string): string {
  switch (status) {
    case "shipped":
      return "border border-emerald-500/30 bg-emerald-500/10 text-emerald-300";
    case "failed":
      return "border border-red-500/30 bg-red-500/10 text-red-300";
    case "rolled-back":
      return "border border-amber-500/30 bg-amber-500/10 text-amber-300";
    default:
      return "border border-surface-700 bg-surface-800/60 text-surface-400";
  }
}

function formatStarted(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.valueOf())) return iso;
  return d.toLocaleString(undefined, { dateStyle: "short", timeStyle: "short" });
}

function formatCost(n?: number): string {
  if (!n || n <= 0) return "$0";
  return `$${n.toFixed(2)}`;
}
