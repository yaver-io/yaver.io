"use client";

import { useEffect, useRef, useState } from "react";
import {
  PHASE_META,
  computeWakeView,
  etaLabel,
  formatClock,
  isPhaseSettled,
  type LifecyclePhase,
  type WakeMachineLike,
} from "@/lib/wakeProgress";

/**
 * WakeProgress — what a managed box is actually doing while it wakes (or parks).
 *
 * The dashboard used to render one static chip, "Resuming…", for the entire
 * multi-minute wake. That is indistinguishable from a hang, and for the one
 * case that IS terminal — a box whose Yaver session expired — it was worse
 * than useless: nothing would ever change, and the card said nothing about it.
 *
 * Web mirror of mobile's WakeProgress. Same ladder, same honesty rules:
 *   - the bar creeps inside a phase but never reaches the next rung until the
 *     control plane says so,
 *   - a phase that overruns explains itself instead of spinning,
 *   - `needs-auth` renders as an ACTION, not as progress, because no amount of
 *     waiting resolves it.
 */

const NETWORK_PHASES: LifecyclePhase[] = ["registering", "online", "ready"];

export interface WakeProgressProps {
  machine: WakeMachineLike | null | undefined;
  /** Live reachability (relay presence) — flips later than status=active. */
  deviceReachable: boolean;
  /** Compact = bar + one status line. Full adds the labelled step ladder. */
  compact?: boolean;
  /** Rendered under a `needs-auth` state, where waiting cannot help. */
  onSignIn?: () => void;
}

export default function WakeProgress({
  machine,
  deviceReachable,
  compact,
  onSignIn,
}: WakeProgressProps) {
  // A local clock, not a data fetch: the elapsed timer and the in-phase creep
  // both have to advance between the 10s /subscription polls, or the bar would
  // visibly jump once every ten seconds and sit frozen in between.
  const [now, setNow] = useState(() => Date.now());
  const view = computeWakeView(machine, deviceReachable, now);
  const settled = isPhaseSettled(view.phase);

  useEffect(() => {
    if (settled) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [settled]);

  // Monotonic: the bar may only ever fill. A phase can legitimately move
  // backwards in the data (a retry re-enters `booting`), and a bar that
  // retreats reads as the wake losing ground when it has not.
  const peak = useRef(0);
  const machineKey = `${machine?.status ?? ""}:${machine?.lastWokeAt ?? ""}`;
  const lastKey = useRef(machineKey);
  if (lastKey.current !== machineKey) {
    lastKey.current = machineKey;
    peak.current = 0;
  }
  peak.current = Math.max(peak.current, view.percent);
  const percent = view.phase === "error" ? view.percent : peak.current;

  // Nothing in flight and nothing to explain — the card's own resting UI says
  // it better than an empty progress block would.
  if (!view.direction && view.phase !== "error" && view.phase !== "needs-auth") return null;

  // needs-auth is not progress. It is a task for the user, and it will stay
  // that way until they do it, so it gets no bar.
  if (view.phase === "needs-auth") {
    return (
      <div className="mt-2 rounded border border-amber-300 bg-amber-50 p-2.5 dark:border-amber-500/40 dark:bg-amber-500/10">
        <p className="text-xs font-semibold text-amber-800 dark:text-amber-200">
          This box is awake — it just needs signing in
        </p>
        <p className="mt-1 text-[11px] text-amber-700 dark:text-amber-300">
          {view.error ??
            "The machine booted fine, but its Yaver agent session expired, so it can't finish connecting on its own."}
        </p>
        <p className="mt-1 text-[11px] text-surface-500">
          It will not connect by itself, and it parks again once the wake window
          closes{view.elapsedTotalMs !== null ? ` (awake ${formatClock(view.elapsedTotalMs)})` : ""}.
        </p>
        {onSignIn ? (
          <button
            type="button"
            onClick={onSignIn}
            className="mt-2 rounded border border-amber-400 bg-amber-100 px-2 py-1 text-[11px] font-semibold text-amber-900 hover:bg-amber-200 dark:border-amber-500/50 dark:bg-amber-500/20 dark:text-amber-100 dark:hover:bg-amber-500/30"
          >
            Sign this machine in
          </button>
        ) : (
          // Web has no re-auth flow of its own yet, so it must name the one
          // that exists rather than offer a button that cannot work.
          <p className="mt-1 text-[11px] text-surface-500">
            Sign it in from the Yaver app on your phone — open the remote-box
            picker and use “Sign this machine in”.
          </p>
        )}
      </div>
    );
  }

  const isPark = view.meta.kind === "park";
  const barTone =
    view.phase === "error"
      ? "bg-red-500"
      : NETWORK_PHASES.includes(view.phase)
        ? "bg-emerald-500"
        : "bg-sky-500";
  const eta = etaLabel(view.phase, view.elapsedInPhaseMs);

  return (
    <div className="mt-2">
      <div className="flex items-center gap-2">
        <span
          className={`h-2 w-2 shrink-0 rounded-full ${barTone} ${view.phase === "error" ? "" : "animate-pulse"}`}
          aria-hidden
        />
        <span className="min-w-0 flex-1 truncate text-xs font-semibold text-slate-700 dark:text-surface-200">
          {view.phase === "error" ? (view.error ?? view.meta.label) : view.meta.label}
        </span>
        <span className="text-[11px] font-bold tabular-nums text-surface-500">
          {Math.round(percent)}%
        </span>
      </div>

      <p className="mt-0.5 text-[11px] tabular-nums text-surface-500">
        {isPark ? "Closing" : "Waking"} · {formatClock(view.elapsedInPhaseMs)}
        {eta ? ` · ${eta} left in this step` : ""}
      </p>

      <div
        className="mt-1.5 h-1.5 w-full overflow-hidden rounded bg-surface-200 dark:bg-surface-800"
        role="progressbar"
        aria-valuenow={Math.round(percent)}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={view.meta.label}
      >
        <div
          className={`h-full rounded ${barTone} transition-all duration-700`}
          style={{ width: `${Math.max(3, Math.min(100, percent))}%` }}
        />
      </div>

      {!compact ? (
        <>
          <div className="mt-2 flex items-start justify-between gap-1">
            {view.steps.map((sp) => {
              const done = percent >= PHASE_META[sp].percent && view.phase !== sp;
              const current = view.phase === sp;
              return (
                <div key={sp} className="flex min-w-0 flex-1 flex-col items-center gap-1">
                  <span
                    className={`flex h-4 w-4 items-center justify-center rounded-full border-2 text-[8px] font-black leading-none ${
                      done
                        ? "border-emerald-500 bg-emerald-500 text-white"
                        : current
                          ? "animate-pulse border-sky-500 text-sky-500"
                          : "border-surface-300 text-transparent dark:border-surface-600"
                    }`}
                  >
                    {done ? "✓" : ""}
                  </span>
                  <span
                    className={`w-full truncate text-center text-[9px] leading-tight ${
                      current
                        ? "font-bold text-slate-700 dark:text-surface-200"
                        : done
                          ? "text-surface-500"
                          : "text-surface-400 dark:text-surface-600"
                    }`}
                  >
                    {PHASE_META[sp].short}
                  </span>
                </div>
              );
            })}
          </div>

          {/* What the provider sees — the only visibility that exists between
              "server created" and "agent answered". */}
          {view.provider ? (
            <p className="mt-1.5 text-[11px] text-surface-500">{view.provider}</p>
          ) : null}

          {NETWORK_PHASES.includes(view.phase) ? (
            <p className="mt-1.5 flex items-center gap-1.5 text-[11px] text-emerald-700 dark:text-emerald-400">
              <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" aria-hidden />
              {view.phase === "ready"
                ? "Connected over the free relay"
                : "Relay link coming up — no re-auth needed"}
            </p>
          ) : view.phase === "error" ? null : (
            <p className="mt-1.5 text-[11px] text-surface-500">
              {isPark
                ? "Snapshot is kept — the server is only removed once it's safely stored."
                : "This becomes a full device card (Shell, SSH, Coding Agents) on its own once the box connects."}
            </p>
          )}

          {/* Honest explanation when a step overruns — replaces the silently
              frozen bar this component exists to kill. */}
          {view.stallHint ? (
            <p className="mt-1 text-[11px] text-amber-700 dark:text-amber-400">{view.stallHint}</p>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
