"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { agentClient } from "@/lib/agent-client";

/**
 * AutorunsView — what the autorun loop is doing, on the surface the developer
 * actually watches.
 *
 * Web had NO autorun UI at all. The loop ran, failed, converged and re-ran with
 * the dashboard showing nothing, so "did my autorun finish?" was a question you
 * could only answer by reading git log and guessing — and git log cannot answer
 * it, because every run of a task emits the same commit subject.
 *
 * The design follows what actually went wrong on 2026-07-17, where 16 runs went
 * mostly-red for reasons the surfaces hid:
 *
 *  - A run is NOT done until it has a finalCommit, however quiet it looks. The
 *    agent marks the last commit of every run explicitly for exactly this reason;
 *    an empty finalCommit is the one reliable "still going".
 *  - status and finishReason can DISAGREE — a run converged (work done) and then
 *    lost a push race on the bookkeeping commit, landing status=failed with
 *    finishReason=converged. Both are shown, never collapsed into one verdict,
 *    because the honest answer is "the work succeeded, the landing didn't".
 *  - activeRunner may differ from the requested runner after a failover. That is
 *    a normal, healthy self-heal, so it is surfaced as information rather than
 *    an error — otherwise "why is claude driving my opencode run?" is unanswerable.
 *
 * Ordering is by SLOT, never by time or status. A slot (task:seat) is an agent's
 * stable address; sorting by StartedAt or status means a card moves whenever some
 * OTHER run changes, which defeats glanceability — you cannot build muscle memory
 * against a list that renumbers itself. Mirrors mobile's sortAutorunViewsBySlot.
 */

type HealEvent = { iteration: number; kind: string; detail: string };

type AutorunSession = {
  id: string;
  slot?: string;
  task?: string;
  runner?: string;
  activeRunner?: string;
  master?: string;
  workDir?: string;
  status?: string;
  startedAt?: string;
  finishedAt?: string;
  error?: string;
  /** Set when the work succeeded and only the final commit/push failed to land. */
  landingError?: string;
  progressTail?: string;
  iterations?: number;
  commits?: number;
  finishReason?: string;
  finalCommit?: string;
  finalCommitSubject?: string;
  heals?: HealEvent[];
};

/** A run has ended only when it says so. See the agent's finalCommit marker. */
function hasEnded(s: AutorunSession): boolean {
  const st = String(s.status || "").toLowerCase();
  return st !== "running" && st !== "";
}

/** The task's display name — never the absolute path (it leaks the home dir). */
function taskName(s: AutorunSession): string {
  const raw = String(s.task || "").trim();
  if (!raw) return "autorun";
  const base = raw.split("/").pop() || raw;
  return base.replace(/\.[^.]+$/, "");
}

function tone(s: AutorunSession): { label: string; cls: string } {
  const st = String(s.status || "").toLowerCase();
  if (st === "running") return { label: "Running", cls: "bg-sky-500/15 text-sky-700 dark:text-sky-300" };
  if (st === "completed") return { label: "Completed", cls: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300" };
  if (st === "stopped") return { label: "Stopped", cls: "bg-amber-500/15 text-amber-700 dark:text-amber-300" };
  if (st === "failed") return { label: "Failed", cls: "bg-red-500/15 text-red-700 dark:text-red-300" };
  return { label: st || "unknown", cls: "bg-surface-500/15 text-surface-400" };
}

/**
 * Did the work succeed and only the bookkeeping fail to land?
 *
 * The agent now answers this itself: a run whose loop converged and whose final
 * push lost a race is `completed` with `landingError` set, rather than `failed`.
 * Trust that field — the agent is the only thing that knows which half broke.
 *
 * The fallback below is for an OLDER agent, which had no landingError and marked
 * such a run `failed` with the push error in `error`. A dashboard talks to boxes
 * it did not ship, so recognising the old shape is what keeps those runs from
 * reading as failures here. Remove it once no agent that old can still report.
 */
function landingOnlyFailure(s: AutorunSession): boolean {
  if (String(s.landingError || "").trim() !== "") return true;
  if (String(s.status || "").toLowerCase() !== "failed") return false;
  const reason = String(s.finishReason || "").toLowerCase();
  const converged = reason.includes("converged") || reason.includes("done");
  const err = String(s.error || "").toLowerCase();
  const pushRace =
    err.includes("rejected") || err.includes("fetch first") || err.includes("push final commit");
  return converged && pushRace;
}

function fmtWhen(iso?: string): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (!Number.isFinite(t) || t <= 0) return "";
  const secs = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (secs < 60) return `${secs}s ago`;
  if (secs < 3600) return `${Math.round(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.round(secs / 3600)}h ago`;
  return `${Math.round(secs / 86400)}d ago`;
}

function duration(s: AutorunSession): string {
  const a = Date.parse(s.startedAt || "");
  if (!Number.isFinite(a)) return "";
  const bRaw = Date.parse(s.finishedAt || "");
  // A zero-value Go time marshals to year 1 — treat anything absurd as "still going".
  const b = Number.isFinite(bRaw) && bRaw > 0 && bRaw >= a ? bRaw : Date.now();
  const mins = Math.round((b - a) / 60000);
  if (mins < 1) return "<1m";
  if (mins < 60) return `${mins}m`;
  return `${Math.floor(mins / 60)}h${mins % 60 ? ` ${mins % 60}m` : ""}`;
}

export default function AutorunsView() {
  const [sessions, setSessions] = useState<AutorunSession[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const res = await agentClient.callOps("autorun_status", {});
      if (!res.ok) throw new Error(res.error || "autorun_status failed");
      const list = Array.isArray(res.initial?.sessions) ? res.initial.sessions : [];
      setSessions(list as AutorunSession[]);
      setErr(null);
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
    // A running loop changes on the order of iterations, not frames — polling
    // this often enough to feel live and rarely enough to stay free.
    const t = setInterval(() => void refresh(), 10_000);
    return () => clearInterval(t);
  }, [refresh]);

  async function stop(id: string) {
    setBusy(id);
    try {
      const res = await agentClient.callOps("autorun_stop", { id });
      if (!res.ok) throw new Error(res.error || "autorun_stop failed");
      await refresh();
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setBusy(null);
    }
  }

  // Slot-stable order: a card must not move because a sibling changed status.
  const ordered = useMemo(() => {
    return [...sessions].sort((a, b) => {
      const sa = String(a.slot || a.id);
      const sb = String(b.slot || b.id);
      return sa.localeCompare(sb);
    });
  }, [sessions]);

  const running = ordered.filter((s) => !hasEnded(s));

  if (loading) {
    return <div className="card p-6 text-sm text-surface-400">Loading autoruns…</div>;
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold text-surface-100">Autoruns</h2>
          <p className="text-xs text-surface-500">
            {running.length > 0
              ? `${running.length} running · ${ordered.length} total on this machine`
              : `${ordered.length} on this machine — none running`}
          </p>
        </div>
        <button
          onClick={() => void refresh()}
          className="rounded border border-surface-700 px-2 py-1 text-xs text-surface-300 hover:bg-surface-800"
        >
          Refresh
        </button>
      </div>

      {err ? (
        <div className="card border border-red-500/30 p-3 text-xs text-red-700 dark:text-red-300">{err}</div>
      ) : null}

      {ordered.length === 0 ? (
        <div className="card p-8 text-center">
          <p className="text-sm text-surface-400">No autoruns on this machine.</p>
          <p className="mt-1 text-xs text-surface-500">
            Autoruns started on another box report there — open that device to see its runs.
          </p>
        </div>
      ) : null}

      {ordered.map((s) => {
        const t = tone(s);
        const failedOnlyToLand = landingOnlyFailure(s);
        const failedOver =
          s.activeRunner && s.runner && s.activeRunner !== s.runner ? s.activeRunner : null;
        const open = expanded === s.id;
        return (
          <div key={s.id} className="card p-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex flex-wrap items-center gap-2">
                <span className={`rounded px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${t.cls}`}>
                  {t.label}
                </span>
                <span className="text-sm font-medium text-surface-100">{taskName(s)}</span>
                {s.slot ? <span className="text-xs text-surface-500">{s.slot}</span> : null}
              </div>
              <div className="flex items-center gap-2">
                {!hasEnded(s) ? (
                  <button
                    onClick={() => void stop(s.id)}
                    disabled={busy === s.id}
                    className="rounded border border-amber-500/40 bg-amber-500/10 px-2 py-1 text-xs font-medium text-amber-700 hover:bg-amber-500/20 disabled:opacity-60 dark:text-amber-300"
                  >
                    {busy === s.id ? "Stopping…" : "Stop"}
                  </button>
                ) : null}
                <button
                  onClick={() => setExpanded(open ? null : s.id)}
                  className="rounded border border-surface-700 px-2 py-1 text-xs text-surface-300 hover:bg-surface-800"
                >
                  {open ? "Hide" : "Details"}
                </button>
              </div>
            </div>

            <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-surface-500">
              <span>
                runner <span className="text-surface-300">{s.activeRunner || s.runner || "—"}</span>
                {s.master ? <> · master <span className="text-surface-300">{s.master}</span></> : null}
              </span>
              <span>iteration {s.iterations ?? 0}</span>
              <span>{s.commits ?? 0} verified commit{(s.commits ?? 0) === 1 ? "" : "s"}</span>
              <span>{duration(s)}{hasEnded(s) ? "" : " so far"}</span>
              {s.startedAt ? <span>started {fmtWhen(s.startedAt)}</span> : null}
            </div>

            {failedOver ? (
              <p className="mt-2 text-[11px] text-sky-700 dark:text-sky-300">
                Requested <span className="font-medium">{s.runner}</span> was not ready — this run
                self-healed onto <span className="font-medium">{failedOver}</span>.
              </p>
            ) : null}

            {/* The honesty gap: converged work reported as a failed run. */}
            {failedOnlyToLand ? (
              <div className="mt-2 rounded border border-amber-500/30 bg-amber-500/5 p-2">
                <p className="text-[11px] text-amber-700 dark:text-amber-300">
                  The work finished ({s.finishReason}) — it was the final commit that
                  failed to land, not the run. The iterations are not lost.
                </p>
                {s.landingError ? (
                  <pre className="mt-1 max-h-24 overflow-auto whitespace-pre-wrap text-[10px] text-surface-500">
                    {s.landingError}
                  </pre>
                ) : null}
              </div>
            ) : null}

            {!hasEnded(s) ? (
              <p className="mt-2 text-[11px] text-surface-500">
                Still running. A run is only finished once it records its final autorun
                commit — a quiet loop is not a finished one.
              </p>
            ) : (
              <div className="mt-2 text-[11px] text-surface-500">
                {s.finishReason ? (
                  <span>
                    finished: <span className="text-surface-300">{s.finishReason}</span>
                  </span>
                ) : null}
                {s.finalCommit ? (
                  <span className="ml-2">
                    final commit <code className="rounded bg-surface-800 px-1">{s.finalCommit.slice(0, 9)}</code>
                  </span>
                ) : (
                  <span className="ml-2 text-amber-700 dark:text-amber-300">
                    no final commit recorded — this run did not land its bookkeeping
                  </span>
                )}
              </div>
            )}

            {open ? (
              <div className="mt-3 space-y-2 border-t border-surface-800 pt-3">
                <div className="text-[11px] text-surface-500">
                  <span className="text-surface-400">run id</span>{" "}
                  <code className="rounded bg-surface-800 px-1">{s.id}</code>
                </div>
                {s.heals && s.heals.length > 0 ? (
                  <div>
                    <p className="mb-1 text-[11px] font-medium text-surface-400">Self-heals</p>
                    {s.heals.map((h, i) => (
                      <p key={i} className="text-[11px] text-surface-500">
                        iteration {h.iteration} · <span className="text-surface-300">{h.kind}</span> — {h.detail}
                      </p>
                    ))}
                  </div>
                ) : null}
                {s.error ? (
                  <div>
                    <p className="mb-1 text-[11px] font-medium text-surface-400">Error</p>
                    <pre className="max-h-40 overflow-auto whitespace-pre-wrap rounded bg-surface-900 p-2 text-[10px] text-red-700 dark:text-red-300">
                      {s.error}
                    </pre>
                  </div>
                ) : null}
                {s.progressTail ? (
                  <div>
                    <p className="mb-1 text-[11px] font-medium text-surface-400">Progress</p>
                    <pre className="max-h-56 overflow-auto whitespace-pre-wrap rounded bg-surface-900 p-2 text-[10px] text-surface-400">
                      {s.progressTail}
                    </pre>
                  </div>
                ) : null}
              </div>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}
