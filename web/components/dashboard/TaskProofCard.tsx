"use client";

/**
 * TaskProofCard — the web "task proof" card + overlay.
 *
 * One SHARED component mounted by BOTH web chat surfaces (VibeCodingView's
 * Vibing transcript and app/dashboard/page.tsx's chat tab) — audit B14: web
 * has two drifted task transcripts, and a proof card that exists on one of
 * them is a defect, not a feature.
 * Design: docs/audits/task-proof-showcase-audit-2026-07.md §9.4 (web).
 *
 * Wire contract (agent side, already shipped):
 *   - Task JSON carries proofStatus/proofUrl/commitSha/commitSubject/
 *     commitBranch/diffShortstat/feedbackId (+ the videoClipId/videoStatus
 *     fields that predate proofs; videoStatus may be "stale").
 *   - GET /tasks/{id}/proof → TaskProof (agentClient.getTaskProof).
 *
 * Media auth (audit B8 — never a bare unauthed URL for
 * /vibing/preview/clip/* routes):
 *   - Poster: authed fetch → blob via agentClient.vibeClipPosterRequest.
 *   - Hero video: prefers the same-origin cookie-authed proxy
 *     /d/<deviceId>/vibing/preview/clip/<clipId> (real Range/seek, no blob
 *     buffering); falls back to the fetch→blob shim when no deviceId is
 *     known or the proxy errors.
 *
 * Failure doctrine (C6): a failed or stale proof renders a NAMED cause and
 * keeps whatever evidence exists (commit, diff, summary) visible. This
 * component never renders nothing for a failed state — callers gate mounting
 * with taskProofVisible(), and past that gate every state draws something.
 */

import { memo, useEffect, useMemo, useState } from "react";
import type { AgentClient, Task, TaskProof } from "@/lib/agent-client";
import { AssistantMarkdown } from "@/components/dashboard/VibeCodingView";

/** Shared mount predicate so both chat surfaces show/hide the card on the
 *  same fact: task reached a renderable terminal state AND proof (or at
 *  least a demo clip) exists or was attempted. */
export function taskProofVisible(task: Task | null | undefined): boolean {
  if (!task) return false;
  if (task.verification) return true;
  if (task.status !== "completed" && task.status !== "review") return false;
  return Boolean(task.proofStatus || task.videoClipId);
}

type ProofPhase =
  | { kind: "capturing" }
  | { kind: "failed"; reason: string; route?: string }
  | { kind: "stale" }
  | { kind: "ready" };

function derivePhase(task: Task, proof: TaskProof | null): ProofPhase {
  if (task.verification) {
    if (task.verification.status === "failed") {
      return {
        kind: "failed",
        reason: task.verification.failureReason || "Browser verification failed. Open the checks below for the named cause.",
      };
    }
    if (task.verification.status === "running") return { kind: "capturing" };
    return { kind: "ready" };
  }
  if (task.proofStatus === "failed" || proof?.status === "failed" || task.videoStatus === "failed") {
    return {
      kind: "failed",
      // Named cause, never a bare "failed": the agent supplies failedReason;
      // the fallback sentence still separates "task finished" from "proof
      // capture did not" so a red card can't be misread as a failed task.
      reason:
        proof?.failedReason ||
        "Proof capture failed on the box. The task itself finished — only the demo recording did not.",
      route: proof?.failedRoute,
    };
  }
  if (task.proofStatus === "capturing" || task.videoStatus === "queued" || task.videoStatus === "recording") {
    return { kind: "capturing" };
  }
  if (task.videoStatus === "stale") return { kind: "stale" };
  return { kind: "ready" };
}

function firstLine(text: string | undefined): string {
  if (!text) return "";
  const line = text
    .split("\n")
    .map((l) => l.replace(/^[#>\-*\s]+/, "").trim())
    .find((l) => l.length > 0);
  return line || "";
}

function StatusPill({ phase, label = "Proof" }: { phase: ProofPhase; label?: string }) {
  if (phase.kind === "ready") {
    return (
      <span className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-emerald-700 dark:text-emerald-300">
        {label} ✓
      </span>
    );
  }
  if (phase.kind === "capturing") {
    return (
      <span className="rounded-md border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-amber-700 dark:text-amber-300">
        Capturing…
      </span>
    );
  }
  if (phase.kind === "stale") {
    return (
      <span className="rounded-md border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-amber-700 dark:text-amber-300">
        Stale
      </span>
    );
  }
  return (
    <span className="rounded-md border border-rose-500/40 bg-rose-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-rose-700 dark:text-rose-300">
      {label} failed
    </span>
  );
}

function EvidenceRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline gap-3 border-t border-surface-800 py-1.5 first:border-t-0">
      <span className="w-20 shrink-0 text-[10px] font-semibold uppercase tracking-[0.14em] text-surface-500">
        {label}
      </span>
      <span className="min-w-0 text-[12px] leading-5 text-surface-200 break-words">{children}</span>
    </div>
  );
}

const TaskProofCard = memo(function TaskProofCard({
  task,
  agentClient,
}: {
  task: Task;
  agentClient: AgentClient;
}) {
  const [proof, setProof] = useState<TaskProof | null>(null);
  const [posterUrl, setPosterUrl] = useState<string | null>(null);
  const [overlayOpen, setOverlayOpen] = useState(false);
  const [proxyFailed, setProxyFailed] = useState(false);
  const [blobSrc, setBlobSrc] = useState<string | null>(null);

  // ── Proof fetch — re-runs when the agent flips proofStatus (SSE/poll) ──
  useEffect(() => {
    let cancelled = false;
    setProof(null);
    if (!task.proofStatus && !task.proofUrl) return;
    void (async () => {
      try {
        const p = await agentClient.getTaskProof(task.id);
        if (!cancelled) setProof(p);
      } catch {
        // Degrade to task-level fields — the card still renders evidence.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [task.id, task.proofStatus, task.proofUrl, agentClient]);

  const phase = derivePhase(task, proof);
  const clipId = task.verification?.videoClipId || proof?.clipId || task.videoClipId || null;

  // ── Poster thumbnail: authed fetch → blob (B8: the poster route is
  //    authSDK; a bare <img src> 401s over the relay). ──
  useEffect(() => {
    if (!clipId) {
      setPosterUrl(null);
      return;
    }
    const req = agentClient.vibeClipPosterRequest(clipId);
    if (!req) return;
    let cancelled = false;
    let obj: string | null = null;
    void (async () => {
      try {
        const res = await fetch(req.url, { headers: req.headers });
        if (!res.ok) return;
        const blob = await res.blob();
        obj = URL.createObjectURL(blob);
        if (!cancelled) setPosterUrl(obj);
        else URL.revokeObjectURL(obj);
      } catch {
        /* poster is decoration; the card works without it */
      }
    })();
    return () => {
      cancelled = true;
      if (obj) URL.revokeObjectURL(obj);
    };
  }, [clipId, agentClient]);

  // ── Hero video source: same-origin /d/<deviceId>/ proxy when possible
  //    (cookie-authed raw stream passthrough → real Range/seek); blob shim
  //    otherwise or when the proxy errors. ──
  const proxySrc = clipId ? agentClient.taskProofClipProxyUrl(clipId) : null;
  const wantBlob = overlayOpen && !!clipId && (!proxySrc || proxyFailed);
  useEffect(() => {
    if (!wantBlob || !clipId) {
      if (blobSrc) URL.revokeObjectURL(blobSrc);
      setBlobSrc(null);
      return;
    }
    const req = agentClient.vibeClipRequest(clipId);
    if (!req) return;
    let cancelled = false;
    let obj: string | null = null;
    void (async () => {
      try {
        const res = await fetch(req.url, { headers: req.headers });
        if (!res.ok) return;
        const blob = await res.blob();
        obj = URL.createObjectURL(blob);
        if (!cancelled) setBlobSrc(obj);
        else URL.revokeObjectURL(obj);
      } catch {
        /* the overlay shows "Loading clip…" until a source resolves */
      }
    })();
    return () => {
      cancelled = true;
      if (obj) URL.revokeObjectURL(obj);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wantBlob, clipId, agentClient]);

  // ESC closes the overlay (backdrop click handled on the element).
  useEffect(() => {
    if (!overlayOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOverlayOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [overlayOpen]);

  const commitSha = proof?.commitSha || task.commitSha || "";
  const commitSubject = proof?.commitSubject || task.commitSubject || "";
  const commitBranch = proof?.commitBranch || task.commitBranch || "";
  const diffShortstat = proof?.diffShortstat || task.diffShortstat || "";
  const durationSec = proof?.durationSec;
  const costUsd = proof?.costUsd ?? task.costUsd;
  const summaryLine = useMemo(
    () => firstLine(proof?.summaryMarkdown) || firstLine(task.resultText),
    [proof?.summaryMarkdown, task.resultText],
  );
  const caption = [
    durationSec ? `${Math.round(durationSec)}s` : "",
    costUsd != null ? `$${costUsd.toFixed(3)}` : "",
    proof?.lane ? proof.lane : "",
  ]
    .filter(Boolean)
    .join(" · ");
  const videoSrc = proxySrc && !proxyFailed ? proxySrc : blobSrc;

  // ── Capturing: a quiet one-line status, no giant spinner. ──
  if (phase.kind === "capturing") {
    return (
      <div className="flex items-center gap-2 text-[11px] text-amber-700 dark:text-amber-300">
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-400" />
        {task.verification?.status === "running" ? "Running browser verification…" : "Recording demo…"}
      </div>
    );
  }

  const failed = phase.kind === "failed";

  return (
    <>
      <div
        className={`max-w-[92%] rounded-2xl border px-4 py-3 ${
          failed ? "border-rose-500/30 bg-rose-500/5" : "border-emerald-500/30 bg-emerald-500/5"
        }`}
      >
        <div className="mb-2 flex items-center gap-2">
          <span
            className={`text-[10px] font-semibold uppercase tracking-[0.16em] ${
              failed ? "text-rose-700 dark:text-rose-300" : "text-emerald-700 dark:text-emerald-300"
            }`}
          >
            {task.verification ? "Browser verification" : "Task proof"}
          </span>
          <StatusPill phase={phase} label={task.verification ? "Browser" : "Proof"} />
          {caption ? <span className="ml-auto text-[10px] text-surface-500">{caption}</span> : null}
        </div>

        {failed ? (
          <div className="mb-2 text-[12px] leading-5 text-rose-700 dark:text-rose-200/90">
            {phase.reason}
            {phase.route ? (
              <span className="mt-0.5 block font-mono text-[11px] text-rose-700/80 dark:text-rose-300/70">
                {phase.route}
              </span>
            ) : null}
          </div>
        ) : null}
        {phase.kind === "stale" ? (
          <div className="mb-2 text-[12px] leading-5 text-amber-700 dark:text-amber-200/90">
            Demo video is stale — it was recorded before the latest changes to this task.
          </div>
        ) : null}

        <div className="flex items-start gap-3">
          {clipId ? (
            <button
              type="button"
              onClick={() => setOverlayOpen(true)}
              className="group relative w-40 shrink-0 overflow-hidden rounded-xl border border-surface-700 bg-surface-950"
              title="Watch the proof video"
            >
              {posterUrl ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img src={posterUrl} alt="Demo video poster" className="aspect-video w-full object-cover" />
              ) : (
                <div className="aspect-video w-full bg-surface-900" />
              )}
              <span className="absolute inset-0 flex items-center justify-center">
                <span className="rounded-full bg-black/60 px-3 py-1.5 text-sm text-white transition-transform group-hover:scale-110">
                  ▶
                </span>
              </span>
            </button>
          ) : null}

          <div className="min-w-0 flex-1">
            {summaryLine ? (
              <div className="text-[13px] leading-5 text-surface-100 break-words">{summaryLine}</div>
            ) : null}
            {commitSha ? (
              <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                <span className="rounded-md border border-surface-700 bg-surface-950 px-1.5 py-0.5 font-mono text-[10px] text-surface-300">
                  {commitSha.slice(0, 7)}
                </span>
                {commitSubject ? (
                  <span className="truncate text-[11px] text-surface-300">{commitSubject}</span>
                ) : null}
                {commitBranch ? (
                  <span className="text-[10px] text-surface-500">on {commitBranch}</span>
                ) : null}
              </div>
            ) : null}
            {diffShortstat ? (
              <div className="mt-1 text-[10px] text-surface-500">{diffShortstat}</div>
            ) : null}
            {task.verification ? (
              <div className="mt-2 text-[11px] text-surface-300">
                {task.verification.passed ?? 0}/{task.verification.total ?? 0} checks passed
                {task.verification.durationMs ? ` · ${(task.verification.durationMs / 1000).toFixed(1)}s` : ""}
              </div>
            ) : null}
          </div>
        </div>
      </div>

      {/* ── Modal overlay: no route change, ESC/backdrop close. ── */}
      {overlayOpen ? (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4"
          onClick={() => setOverlayOpen(false)}
        >
          <div
            className="flex max-h-full w-full max-w-4xl flex-col overflow-hidden rounded-2xl border border-surface-800 bg-surface-950 shadow-2xl"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center gap-2 border-b border-surface-800 px-4 py-3">
              <span className="min-w-0 flex-1 truncate text-sm font-semibold text-surface-100">
                {task.title || "Task proof"}
              </span>
              <StatusPill phase={phase} label={task.verification ? "Browser" : "Proof"} />
              <button
                type="button"
                onClick={() => setOverlayOpen(false)}
                className="rounded-full bg-white/10 px-3 py-1 text-[12px] text-surface-200 hover:bg-white/20"
              >
                ✕ Close
              </button>
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto">
              {clipId ? (
                videoSrc ? (
                  <video
                    key={videoSrc}
                    src={videoSrc}
                    controls
                    autoPlay
                    className="max-h-[55vh] w-full bg-black"
                    onError={() => {
                      // Proxy path unreachable (no cookie / relay not wired):
                      // degrade to the authed fetch→blob shim instead of a
                      // black rectangle.
                      if (proxySrc && !proxyFailed) setProxyFailed(true);
                    }}
                  />
                ) : (
                  <div className="flex aspect-video w-full items-center justify-center bg-black text-sm text-surface-400">
                    Loading clip…
                  </div>
                )
              ) : null}

              <div className="space-y-3 px-4 py-4">
                {failed ? (
                  <div className="rounded-2xl border border-rose-500/25 bg-rose-500/10 px-4 py-3 text-[13px] leading-5 text-rose-700 dark:text-rose-100">
                    <div className="mb-1 text-[10px] font-semibold uppercase tracking-[0.16em] text-rose-700 dark:text-rose-300">
                      Proof capture failed
                    </div>
                    {phase.reason}
                    {phase.route ? (
                      <div className="mt-1 font-mono text-[11px] opacity-80">{phase.route}</div>
                    ) : null}
                  </div>
                ) : null}
                {phase.kind === "stale" ? (
                  <div className="rounded-2xl border border-amber-500/25 bg-amber-500/10 px-4 py-3 text-[13px] leading-5 text-amber-800 dark:text-amber-100">
                    This recording predates the latest changes — re-run the task to refresh the proof.
                  </div>
                ) : null}

                {commitSha || diffShortstat || proof?.lane || durationSec || costUsd != null ? (
                  <div className="rounded-2xl border border-surface-800 bg-surface-950/60 px-4 py-2">
                    {commitSha ? (
                      <EvidenceRow label="Commit">
                        <span className="font-mono text-[11px] text-surface-300">{commitSha.slice(0, 7)}</span>
                        {commitSubject ? <span className="ml-2">{commitSubject}</span> : null}
                        {commitBranch ? (
                          <span className="ml-2 text-surface-500">on {commitBranch}</span>
                        ) : null}
                      </EvidenceRow>
                    ) : null}
                    {diffShortstat ? <EvidenceRow label="Changes">{diffShortstat}</EvidenceRow> : null}
                    {proof?.lane ? <EvidenceRow label="Lane">{proof.lane}</EvidenceRow> : null}
                    {durationSec ? <EvidenceRow label="Duration">{Math.round(durationSec)}s</EvidenceRow> : null}
                    {costUsd != null ? <EvidenceRow label="Cost">${costUsd.toFixed(3)}</EvidenceRow> : null}
                  </div>
                ) : null}

                {proof?.summaryMarkdown ? (
                  <div className="rounded-2xl border border-emerald-500/30 bg-emerald-500/5 px-4 py-3">
                    <div className="mb-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-emerald-700 dark:text-emerald-300">
                      What was proven
                    </div>
                    <div className="text-[13px] leading-6 text-surface-100 break-words [&_pre]:whitespace-pre-wrap">
                      <AssistantMarkdown text={proof.summaryMarkdown} />
                    </div>
                  </div>
                ) : null}
                {task.verification?.checks?.length ? (
                  <div className="rounded-2xl border border-surface-800 bg-surface-950/60 px-4 py-2">
                    {task.verification.checks.map((check, index) => (
                      <EvidenceRow key={`${check.name}:${index}`} label={check.status === "pass" ? "Passed" : "Failed"}>
                        {check.name}
                        {check.error ? <span className="mt-0.5 block text-rose-300">{check.error}</span> : null}
                      </EvidenceRow>
                    ))}
                  </div>
                ) : null}
              </div>
            </div>
          </div>
        </div>
      ) : null}
    </>
  );
});

export default TaskProofCard;
