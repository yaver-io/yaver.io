"use client";

// SchedulesView — UI over desktop/agent/scheduler.go. A schedule runs
// a task on the agent at a future time (runAt), on a cron expression
// (cron), or every N minutes (repeatInterval). Everything executes
// locally on the user's machine — Convex never sees it.

import { useCallback, useEffect, useState } from "react";
import { agentClient, type ScheduledTask } from "@/lib/agent-client";

type ScheduleMode = "once" | "cron" | "interval";

function statusColor(s: ScheduledTask["status"]) {
  switch (s) {
    case "running":
      return "bg-amber-900/40 text-amber-700 dark:text-amber-200";
    case "completed":
      return "bg-emerald-900/40 text-emerald-700 dark:text-emerald-200";
    case "failed":
      return "bg-red-900/40 text-red-700 dark:text-red-200";
    case "paused":
      return "bg-surface-800 text-surface-400";
    default:
      return "bg-indigo-900/40 text-indigo-700 dark:text-indigo-200";
  }
}

export default function SchedulesView() {
  const [schedules, setSchedules] = useState<ScheduledTask[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);

  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [mode, setMode] = useState<ScheduleMode>("cron");
  const [cron, setCron] = useState("0 9 * * 1-5");
  const [runAt, setRunAt] = useState("");
  const [interval, setInterval] = useState("60");
  const [runner, setRunner] = useState("");
  const [creating, setCreating] = useState(false);

  const load = useCallback(async () => {
    setErr(null);
    try {
      const rows = await agentClient.listSchedules();
      rows.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
      setSchedules(rows);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    const iv = window.setInterval(load, 15_000);
    return () => window.clearInterval(iv);
  }, [load]);

  async function create() {
    if (!title.trim()) return;
    setCreating(true);
    try {
      const spec: Partial<ScheduledTask> & { title: string } = {
        title: title.trim(),
        description: description.trim() || undefined,
        runner: runner.trim() || undefined,
      };
      if (mode === "cron") {
        spec.cron = cron.trim();
      } else if (mode === "interval") {
        const n = Number.parseInt(interval, 10);
        if (Number.isFinite(n) && n > 0) spec.repeatInterval = n;
      } else if (mode === "once") {
        if (runAt) spec.runAt = new Date(runAt).toISOString();
      }
      await agentClient.createSchedule(spec);
      setTitle("");
      setDescription("");
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setCreating(false);
    }
  }

  async function remove(id: string, title: string) {
    if (!window.confirm(`Delete "${title}"?`)) return;
    try {
      await agentClient.deleteSchedule(id);
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function toggle(s: ScheduledTask) {
    try {
      if (s.status === "paused") await agentClient.resumeSchedule(s.id);
      else await agentClient.pauseSchedule(s.id);
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function runNow(s: ScheduledTask) {
    try {
      await agentClient.runScheduleNow(s.id);
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4 text-surface-100">
      <header>
        <h2 className="text-lg font-semibold">Schedules</h2>
        <p className="text-xs text-surface-400">
          Tasks that fire on a cron, a specific time, or a fixed interval — all scheduled and executed on the agent. Nothing is pushed to Convex.
        </p>
      </header>

      {err && (
        <div className="rounded border border-red-500/40 bg-red-950/30 px-3 py-2 text-sm text-red-700 dark:text-red-200" role="alert">
          {err}
        </div>
      )}

      <section className="rounded border border-surface-700 bg-surface-950/30 p-3">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-surface-400">New schedule</h3>
        <div className="mt-2 grid gap-2 md:grid-cols-2">
          <input
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 text-sm"
            placeholder="title (e.g. Daily deploy check)"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
          />
          <input
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 text-sm"
            placeholder="runner (optional — e.g. claude-code, codex, opencode)"
            value={runner}
            onChange={(e) => setRunner(e.target.value)}
          />
          <textarea
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 font-mono text-sm md:col-span-2"
            placeholder="description / prompt"
            rows={3}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-2 text-xs">
          {(["cron", "once", "interval"] as ScheduleMode[]).map((m) => (
            <button
              key={m}
              type="button"
              onClick={() => setMode(m)}
              className={`rounded px-2 py-1 ${
                mode === m ? "bg-indigo-900/60 text-indigo-800 dark:text-indigo-100" : "bg-surface-900 text-surface-400 hover:text-surface-100"
              }`}
            >
              {m === "cron" ? "Cron" : m === "once" ? "Run at" : "Every N min"}
            </button>
          ))}
          {mode === "cron" && (
            <input
              className="flex-1 rounded border border-surface-700 bg-surface-900 px-2 py-1 font-mono text-sm"
              placeholder="0 9 * * 1-5"
              value={cron}
              onChange={(e) => setCron(e.target.value)}
            />
          )}
          {mode === "once" && (
            <input
              className="flex-1 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm"
              type="datetime-local"
              value={runAt}
              onChange={(e) => setRunAt(e.target.value)}
            />
          )}
          {mode === "interval" && (
            <input
              className="w-32 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm"
              type="number"
              min="1"
              value={interval}
              onChange={(e) => setInterval(e.target.value)}
              placeholder="minutes"
            />
          )}
          <button
            type="button"
            className="ml-auto rounded bg-indigo-600 px-4 py-1.5 text-sm font-semibold disabled:opacity-40"
            disabled={creating || !title.trim()}
            onClick={() => void create()}
          >
            {creating ? "Creating…" : "Create"}
          </button>
        </div>
      </section>

      <section className="rounded border border-surface-700">
        <div className="flex items-center justify-between border-b border-surface-700 px-3 py-2">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-surface-400">
            Schedules {schedules.length > 0 && `(${schedules.length})`}
          </h3>
          <button type="button" className="text-xs text-surface-400 hover:text-surface-100" onClick={() => void load()}>
            Refresh
          </button>
        </div>
        {loading && <p className="p-3 text-sm text-surface-400">Loading…</p>}
        {!loading && schedules.length === 0 && <p className="p-3 text-sm text-surface-500">None yet.</p>}
        <ul className="divide-y divide-surface-800">
          {schedules.map((s) => (
            <li key={s.id} className="flex flex-col gap-1 px-3 py-2">
              <div className="flex items-center gap-2">
                <span className="font-medium">{s.title}</span>
                <span className={`rounded px-1.5 py-0.5 text-[10px] ${statusColor(s.status)}`}>
                  {s.status}
                </span>
                <span className="ml-auto flex gap-1 text-xs">
                  <button
                    type="button"
                    className="rounded bg-emerald-900/40 px-2 py-0.5 text-emerald-700 dark:text-emerald-200 hover:bg-emerald-900/70"
                    onClick={() => void runNow(s)}
                    title="Fire now without altering cadence"
                  >
                    Run now
                  </button>
                  <button
                    type="button"
                    className="rounded bg-surface-800 px-2 py-0.5 hover:bg-surface-700"
                    onClick={() => void toggle(s)}
                  >
                    {s.status === "paused" ? "Resume" : "Pause"}
                  </button>
                  <button
                    type="button"
                    className="rounded bg-red-900/40 px-2 py-0.5 text-red-700 dark:text-red-200 hover:bg-red-900/70"
                    onClick={() => void remove(s.id, s.title)}
                  >
                    Delete
                  </button>
                </span>
              </div>
              <div className="flex flex-wrap gap-3 text-[11px] text-surface-400">
                {s.cron && (
                  <span>
                    cron <code className="rounded bg-surface-900 px-1">{s.cron}</code>
                  </span>
                )}
                {s.runAt && <span>runAt {s.runAt}</span>}
                {s.repeatInterval && <span>every {s.repeatInterval} min</span>}
                {s.runner && <span>runner {s.runner}</span>}
                <span>runs {s.runCount}</span>
                {s.nextRunAt && <span>next {s.nextRunAt}</span>}
                {s.lastRunAt && <span>last {s.lastRunAt}</span>}
              </div>
              {s.description && <p className="truncate text-xs text-surface-500">{s.description}</p>}
              {s.history && s.history.length > 0 && (
                <>
                  <button
                    type="button"
                    className="mt-1 self-start text-[11px] text-surface-400 hover:text-surface-100"
                    onClick={() =>
                      setExpanded((prev) => ({ ...prev, [s.id]: !prev[s.id] }))
                    }
                  >
                    {expanded[s.id] ? "Hide" : "Show"} history ({s.history.length})
                  </button>
                  {expanded[s.id] && (
                    <ul className="ml-2 mt-1 flex flex-col gap-0.5 border-l border-surface-800 pl-2 text-[11px]">
                      {[...s.history].reverse().slice(0, 20).map((h) => {
                        const statusCls =
                          h.status === "completed" || h.status === "finished"
                            ? "text-emerald-700 dark:text-emerald-300"
                            : h.status === "failed"
                              ? "text-red-700 dark:text-red-300"
                              : "text-surface-400";
                        const dur = h.durationMs
                          ? h.durationMs > 1000
                            ? `${(h.durationMs / 1000).toFixed(1)}s`
                            : `${h.durationMs}ms`
                          : "";
                        return (
                          <li key={`${s.id}-${h.taskId}`} className="flex flex-wrap gap-2">
                            <span className={statusCls}>●</span>
                            <span className="text-surface-400">{h.startedAt}</span>
                            {dur && <span className="text-surface-500">{dur}</span>}
                            {typeof h.costUsd === "number" && h.costUsd > 0 && (
                              <span className="text-surface-500">
                                ${h.costUsd.toFixed(3)}
                              </span>
                            )}
                            <code className="truncate text-surface-500">{h.taskId.slice(0, 8)}</code>
                          </li>
                        );
                      })}
                    </ul>
                  )}
                </>
              )}
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
