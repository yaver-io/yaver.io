"use client";

// VibePreviewView.tsx — dashboard tab for the vibe-preview live frame +
// MP4 clip stream. See docs/vibe-preview-streaming.md.
//
// Authed binary fetch shim: <img src> / <video src> can't carry bearer
// headers, so we fetch as blob and feed the object URL to the element.
// Object URLs are revoked on swap to keep memory bounded.
//
// Styling: consumes the surface/semantic token ramp (globals.css) so the
// view re-themes for light/dark like the rest of the dashboard. It also
// walks the user through the flow — name → start → record → play — with an
// always-visible "next step" so an empty session never looks broken.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";

interface VibeEvent {
  type: string;
  project?: string;
  seq?: number;
  hash?: string;
  size?: number;
  clipId?: string;
  source?: string;
  durationSec?: number;
  message?: string;
  ts: string;
}

interface VibeClip {
  id: string;
  source: string;
  status: "recording" | "ready" | "failed";
  durationSec?: number;
  sizeBytes?: number;
  err?: string;
}

export default function VibePreviewView() {
  const [project, setProject] = useState("");
  const [targetUrl, setTargetUrl] = useState("http://127.0.0.1:3000");
  const [active, setActive] = useState(false);
  const [events, setEvents] = useState<VibeEvent[]>([]);
  const [latestHash, setLatestHash] = useState<string | null>(null);
  const [frameUrl, setFrameUrl] = useState<string | null>(null);
  const [clips, setClips] = useState<VibeClip[]>([]);
  const [activeClipId, setActiveClipId] = useState<string | null>(null);
  const [activeClipUrl, setActiveClipUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const unsubRef = useRef<null | (() => void)>(null);
  const frameUrlRef = useRef<string | null>(null);

  // Fetch a frame as blob and turn it into an object URL that <img> can
  // load without auth headers in the URL string. Revokes the previous URL
  // so we don't leak blob references on every frame.
  const refreshFrame = useCallback(
    async (project: string, hash: string) => {
      const req = agentClient.vibeFrameRequest(project, hash);
      if (!req) return;
      try {
        const res = await fetch(req.url, { headers: req.headers });
        if (!res.ok) return;
        const blob = await res.blob();
        const url = URL.createObjectURL(blob);
        if (frameUrlRef.current) URL.revokeObjectURL(frameUrlRef.current);
        frameUrlRef.current = url;
        setFrameUrl(url);
      } catch {
        /* relay flapped — drop this frame */
      }
    },
    [],
  );

  // Same blob shim for clip MP4. <video> handles Range, but the URL must
  // still carry auth → fetch + objectURL is the simplest correct path
  // (the player loses Range optimization but a 12 s clip is cheap).
  const loadClipPlayback = useCallback(async (clipId: string) => {
    const req = agentClient.vibeClipRequest(clipId);
    if (!req) return;
    try {
      const res = await fetch(req.url, { headers: req.headers });
      if (!res.ok) return;
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      setActiveClipUrl((prev) => {
        if (prev) URL.revokeObjectURL(prev);
        return url;
      });
    } catch (e) {
      setError(`clip load: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, []);

  const refreshClips = useCallback(async () => {
    if (!project) return;
    setClips(await agentClient.listVibeClips(project) as VibeClip[]);
  }, [project]);

  const handleStart = useCallback(async () => {
    setError(null);
    if (!project || !targetUrl) {
      setError("project and target URL required");
      return;
    }
    const sess = await agentClient.startVibePreview({ project, targetUrl, mode: "live" });
    if (!sess) {
      setError("could not start preview — is Chrome/Chromium installed on the agent?");
      return;
    }
    setActive(true);

    // Subscribe to events
    if (unsubRef.current) unsubRef.current();
    unsubRef.current = agentClient.subscribeVibePreviewEvents(
      project,
      (ev: VibeEvent) => {
        setEvents((prev) => {
          const next = [...prev, ev];
          return next.length > 200 ? next.slice(next.length - 200) : next;
        });
        if (ev.type === "frame" && ev.hash) {
          setLatestHash(ev.hash);
          void refreshFrame(project, ev.hash);
        } else if (ev.type === "clip_ready") {
          void refreshClips();
        } else if (ev.type === "stopped") {
          setActive(false);
        }
      },
      (err) => setError(String(err)),
    );

    void refreshClips();
  }, [project, targetUrl, refreshFrame, refreshClips]);

  const handleStop = useCallback(async () => {
    if (project) await agentClient.stopVibePreview(project);
    if (unsubRef.current) {
      unsubRef.current();
      unsubRef.current = null;
    }
    setActive(false);
  }, [project]);

  const handleRecord = useCallback(
    async (source?: "sim-ios" | "sim-android" | "phone") => {
      if (!project) return;
      const rec = await agentClient.startVibeClip({ project, source, durationMaxSec: 12 });
      if (!rec) {
        setError("recording failed — install Xcode (sim-ios) or platform-tools/adb (sim-android)");
        return;
      }
      void refreshClips();
    },
    [project, refreshClips],
  );

  const handlePlayClip = useCallback(
    async (clip: VibeClip) => {
      if (clip.status !== "ready") return;
      setActiveClipId(clip.id);
      await loadClipPlayback(clip.id);
    },
    [loadClipPlayback],
  );

  // Cleanup on unmount.
  useEffect(() => {
    return () => {
      if (unsubRef.current) unsubRef.current();
      if (frameUrlRef.current) URL.revokeObjectURL(frameUrlRef.current);
    };
  }, []);

  const recentEvents = useMemo(() => events.slice().reverse().slice(0, 20), [events]);

  // ── Guided flow ──────────────────────────────────────────────────────
  // One source of truth for "where is the user in the flow" so the header
  // stepper, the empty-state CTA, and the disabled-button hints all agree.
  const hasName = project.trim().length > 0;
  const hasMedia = Boolean(frameUrl || activeClipUrl);
  const step: 1 | 2 | 3 | 4 = !hasName
    ? 1
    : !active
    ? 2
    : !hasMedia
    ? 3
    : 4;

  const recordDisabledReason = !hasName
    ? "Name your project first"
    : !active
    ? "Start the preview first"
    : null;
  const canRecord = !recordDisabledReason;

  const steps = [
    { n: 1, label: "Name the project" },
    { n: 2, label: "Start the preview" },
    { n: 3, label: "Record clips" },
  ];

  return (
    <div className="flex h-full flex-col bg-surface-950 text-surface-200">
      {/* Header — title, one-liner, and a live stepper so the user always
          knows what to do next. */}
      <div className="border-b border-surface-800 px-5 py-4">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 className="text-base font-semibold text-surface-50">Vibe Preview</h2>
            <p className="mt-0.5 text-xs text-surface-400">
              Launch a live browser preview of your running app, then capture
              short clips to share or feed back to the agent.
            </p>
          </div>
          <ol className="flex items-center gap-1.5 text-[11px]">
            {steps.map((s, i) => {
              const state =
                step > s.n ? "done" : step === s.n ? "current" : "todo";
              return (
                <li key={s.n} className="flex items-center gap-1.5">
                  <span
                    className={`inline-flex h-5 items-center gap-1.5 rounded-full border px-2 font-medium transition-colors ${
                      state === "current"
                        ? "border-brand/40 bg-brand-soft text-brand-soft-fg"
                        : state === "done"
                        ? "border-success/30 bg-success-soft text-success-soft-fg"
                        : "border-surface-700 bg-surface-900 text-surface-400"
                    }`}
                  >
                    <span className="tabular-nums">
                      {state === "done" ? "✓" : s.n}
                    </span>
                    {s.label}
                  </span>
                  {i < steps.length - 1 && (
                    <span className="text-surface-600">→</span>
                  )}
                </li>
              );
            })}
          </ol>
        </div>

        {/* Inputs — now labeled, themed, and visibly bounded in both modes. */}
        <div className="mt-3 flex flex-wrap items-end gap-3">
          <label className="flex flex-col gap-1">
            <span className="text-[11px] font-medium text-surface-400">
              Project name
            </span>
            <input
              className="w-48 rounded-lg border border-surface-700 bg-surface-900 px-3 py-1.5 text-sm text-surface-100 placeholder:text-surface-400 focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand/30 disabled:opacity-50"
              placeholder="my-app"
              value={project}
              onChange={(e) => setProject(e.target.value)}
              disabled={active}
              autoFocus
            />
          </label>
          <label className="flex min-w-[14rem] flex-1 flex-col gap-1">
            <span className="text-[11px] font-medium text-surface-400">
              Target URL
            </span>
            <input
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-1.5 text-sm text-surface-100 placeholder:text-surface-400 focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand/30 disabled:opacity-50"
              placeholder="http://127.0.0.1:3000"
              value={targetUrl}
              onChange={(e) => setTargetUrl(e.target.value)}
              disabled={active}
            />
          </label>
          {active ? (
            <button
              onClick={handleStop}
              className="inline-flex items-center gap-2 rounded-lg border border-surface-700 bg-surface-800 px-4 py-1.5 text-sm font-medium text-surface-100 transition-colors hover:bg-surface-700"
            >
              <span className="h-2 w-2 rounded-full bg-success animate-pulse" />
              Stop
            </button>
          ) : (
            <button
              onClick={handleStart}
              disabled={!hasName}
              title={!hasName ? "Name your project first" : undefined}
              className="rounded-lg bg-brand px-4 py-1.5 text-sm font-medium text-brand-fg transition-colors hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
            >
              Start preview
            </button>
          )}
        </div>
      </div>

      {error && (
        <div className="border-b border-danger/30 bg-danger-soft px-5 py-2 text-sm text-danger-soft-fg">
          {error}
        </div>
      )}

      <div className="flex min-h-0 flex-1">
        {/* Stage */}
        <div className="flex flex-1 items-center justify-center p-4">
          <div className="flex h-full w-full items-center justify-center overflow-hidden rounded-xl border border-surface-800 bg-surface-900">
            {activeClipUrl ? (
              <video
                key={activeClipId ?? "clip"}
                src={activeClipUrl}
                controls
                autoPlay
                className="max-h-full max-w-full"
                onEnded={() => setActiveClipId(null)}
              />
            ) : frameUrl ? (
              // Content-addressed, but blob-backed → ok to render plain img.
              // eslint-disable-next-line @next/next/no-img-element
              <img src={frameUrl} alt="vibe preview frame" className="max-h-full max-w-full" />
            ) : (
              <EmptyStage step={step} targetUrl={targetUrl} active={active} />
            )}
          </div>
        </div>

        {/* Right rail */}
        <aside className="flex w-80 flex-col border-l border-surface-800 bg-surface-950">
          <div className="border-b border-surface-800 px-4 py-2.5 text-[11px] font-semibold uppercase tracking-wider text-surface-400">
            Clips · {clips.length}
          </div>
          <div className="flex max-h-44 flex-wrap gap-2 overflow-y-auto p-3">
            {clips.length === 0 && (
              <p className="text-xs text-surface-400">
                No clips yet.{" "}
                {canRecord
                  ? "Hit Record below to capture a 12s clip."
                  : `${recordDisabledReason} to record.`}
              </p>
            )}
            {clips.map((c) => (
              <button
                key={c.id}
                onClick={() => handlePlayClip(c)}
                title={c.status === "ready" ? "Play clip" : c.status}
                className={`inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-xs transition-colors ${
                  c.status === "ready"
                    ? "border-success/40 bg-success-soft text-success-soft-fg hover:opacity-80"
                    : c.status === "recording"
                    ? "border-danger/40 bg-danger-soft text-danger-soft-fg animate-pulse"
                    : "border-surface-700 bg-surface-900 text-surface-400"
                }`}
              >
                {c.status === "ready" && <span aria-hidden>▶</span>}
                {c.source} · {c.durationSec ? `${c.durationSec.toFixed(0)}s` : c.status}
              </button>
            ))}
          </div>

          <div className="border-t border-surface-800 p-3">
            <div className="flex flex-wrap gap-2">
              <button
                onClick={() => handleRecord()}
                disabled={!canRecord}
                title={recordDisabledReason ?? "Record a 12s clip"}
                className="inline-flex flex-1 items-center justify-center gap-2 rounded-lg bg-danger px-3 py-1.5 text-sm font-medium text-danger-fg transition-colors hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
              >
                <span className="h-2 w-2 rounded-full bg-danger-fg" />
                Record
              </button>
              <button
                onClick={() => handleRecord("sim-android")}
                disabled={!canRecord}
                title={recordDisabledReason ?? "Record from an Android emulator"}
                className="rounded-lg border border-surface-700 bg-surface-900 px-2.5 py-1.5 text-xs font-medium text-surface-200 transition-colors hover:bg-surface-800 disabled:cursor-not-allowed disabled:opacity-40"
              >
                Android
              </button>
              <button
                onClick={() => handleRecord("phone")}
                disabled={!canRecord}
                title={recordDisabledReason ?? "Record from a paired phone"}
                className="rounded-lg border border-surface-700 bg-surface-900 px-2.5 py-1.5 text-xs font-medium text-surface-200 transition-colors hover:bg-surface-800 disabled:cursor-not-allowed disabled:opacity-40"
              >
                Phone
              </button>
            </div>
            {recordDisabledReason && (
              <p className="mt-2 text-[11px] text-surface-400">
                {recordDisabledReason} to enable recording.
              </p>
            )}
          </div>

          <div className="border-t border-surface-800 px-4 py-2.5 text-[11px] font-semibold uppercase tracking-wider text-surface-400">
            Events
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-4 py-2 font-mono text-[11px]">
            {recentEvents.length === 0 ? (
              <p className="font-sans text-surface-500">
                Session activity (frames, clips, errors) will stream here.
              </p>
            ) : (
              recentEvents.map((ev, i) => (
                <div key={i} className="truncate text-success-soft-fg">
                  {ev.ts.slice(11, 19)} · {ev.type}
                  {ev.hash ? ` · ${ev.hash.slice(0, 6)}` : ""}
                  {ev.clipId ? ` · ${ev.clipId}` : ""}
                  {ev.message ? ` · ${ev.message}` : ""}
                </div>
              ))
            )}
          </div>
        </aside>
      </div>
    </div>
  );
}

// EmptyStage — the guided "what do I do now" panel shown before any frame
// or clip exists. It points at the single next action for the current step
// instead of a bare "Press Start".
function EmptyStage({
  step,
  targetUrl,
  active,
}: {
  step: 1 | 2 | 3 | 4;
  targetUrl: string;
  active: boolean;
}) {
  const items = [
    {
      n: 1,
      title: "Name your project",
      body: "Type a project name above — it labels this preview session and its clips.",
    },
    {
      n: 2,
      title: "Start the preview",
      body: (
        <>
          Make sure your app is serving at{" "}
          <code className="rounded bg-surface-800 px-1 py-0.5 text-surface-200">
            {targetUrl || "http://127.0.0.1:3000"}
          </code>
          , then hit <span className="font-medium text-surface-200">Start preview</span>.
        </>
      ),
    },
    {
      n: 3,
      title: "Record & share",
      body: "Once frames arrive, use Record to capture a 12s clip from the browser, an Android emulator, or a paired phone.",
    },
  ];

  return (
    <div className="mx-auto max-w-md px-6 py-8 text-center">
      {active && step === 3 ? (
        <>
          <div className="mx-auto mb-3 flex h-10 w-10 items-center justify-center">
            <span className="h-3 w-3 rounded-full bg-brand animate-pulse" />
          </div>
          <p className="text-sm font-medium text-surface-200">
            Session live — waiting for the first frame…
          </p>
          <p className="mt-1 text-xs text-surface-400">
            Connecting to{" "}
            <code className="rounded bg-surface-800 px-1 py-0.5 text-surface-300">
              {targetUrl}
            </code>
            . If nothing appears, confirm the app is running there.
          </p>
        </>
      ) : (
        <>
          <p className="text-sm font-medium text-surface-200">
            Get started in three steps
          </p>
          <ol className="mt-4 space-y-3 text-left">
            {items.map((it) => {
              const state =
                step > it.n ? "done" : step === it.n ? "current" : "todo";
              return (
                <li
                  key={it.n}
                  className={`flex gap-3 rounded-lg border p-3 transition-colors ${
                    state === "current"
                      ? "border-brand/40 bg-brand-soft/40"
                      : "border-surface-800 bg-surface-950/50"
                  }`}
                >
                  <span
                    className={`mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold ${
                      state === "done"
                        ? "bg-success text-success-fg"
                        : state === "current"
                        ? "bg-brand text-brand-fg"
                        : "bg-surface-800 text-surface-400"
                    }`}
                  >
                    {state === "done" ? "✓" : it.n}
                  </span>
                  <div>
                    <p
                      className={`text-xs font-medium ${
                        state === "todo" ? "text-surface-400" : "text-surface-100"
                      }`}
                    >
                      {it.title}
                    </p>
                    <p className="mt-0.5 text-[11px] leading-relaxed text-surface-400">
                      {it.body}
                    </p>
                  </div>
                </li>
              );
            })}
          </ol>
        </>
      )}
    </div>
  );
}
