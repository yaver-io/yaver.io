"use client";

// VibePreviewView.tsx — dashboard tab for the vibe-preview live frame +
// MP4 clip stream. See docs/vibe-preview-streaming.md.
//
// Authed binary fetch shim: <img src> / <video src> can't carry bearer
// headers, so we fetch as blob and feed the object URL to the element.
// Object URLs are revoked on swap to keep memory bounded.

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

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-2 px-4 py-3 border-b border-white/10">
        <input
          className="px-3 py-1.5 rounded bg-white/5 border border-white/10 text-sm w-44"
          placeholder="project name"
          value={project}
          onChange={(e) => setProject(e.target.value)}
          disabled={active}
        />
        <input
          className="px-3 py-1.5 rounded bg-white/5 border border-white/10 text-sm flex-1"
          placeholder="http://127.0.0.1:3000"
          value={targetUrl}
          onChange={(e) => setTargetUrl(e.target.value)}
          disabled={active}
        />
        {active ? (
          <button
            onClick={handleStop}
            className="px-3 py-1.5 rounded bg-zinc-700 hover:bg-zinc-600 text-sm"
          >
            Stop
          </button>
        ) : (
          <button
            onClick={handleStart}
            className="px-3 py-1.5 rounded bg-blue-600 hover:bg-blue-500 text-sm"
          >
            Start preview
          </button>
        )}
      </div>

      {error && (
        <div className="px-4 py-2 bg-red-950/40 border-b border-red-800/40 text-red-700 dark:text-red-200 text-sm">
          {error}
        </div>
      )}

      <div className="flex-1 flex bg-black">
        <div className="flex-1 flex items-center justify-center">
          {activeClipUrl ? (
            <video
              key={activeClipId ?? "clip"}
              src={activeClipUrl}
              controls
              autoPlay
              className="max-w-full max-h-full"
              onEnded={() => setActiveClipId(null)}
            />
          ) : frameUrl ? (
            // Content-addressed, but blob-backed → ok to render plain img.
            // eslint-disable-next-line @next/next/no-img-element
            <img src={frameUrl} alt="vibe preview frame" className="max-w-full max-h-full" />
          ) : (
            <span className="text-zinc-500 text-sm">
              {active ? "Waiting for first frame…" : "Press Start to begin a session."}
            </span>
          )}
        </div>

        <aside className="w-80 border-l border-white/10 flex flex-col">
          <div className="px-3 py-2 border-b border-white/10 text-xs uppercase tracking-wider text-zinc-400">
            Clips · {clips.length}
          </div>
          <div className="p-3 flex flex-wrap gap-2 max-h-44 overflow-y-auto">
            {clips.length === 0 && (
              <span className="text-zinc-500 text-xs">No clips yet. Hit Record below.</span>
            )}
            {clips.map((c) => (
              <button
                key={c.id}
                onClick={() => handlePlayClip(c)}
                className={`px-2 py-1 rounded text-xs border ${
                  c.status === "ready"
                    ? "border-emerald-500/50 hover:bg-emerald-500/10"
                    : c.status === "recording"
                    ? "border-red-500/50 animate-pulse"
                    : "border-zinc-700"
                }`}
              >
                {c.source} · {c.durationSec ? `${c.durationSec.toFixed(0)}s` : c.status}
              </button>
            ))}
          </div>

          <div className="p-3 border-t border-white/10 flex gap-2 flex-wrap">
            <button
              onClick={() => handleRecord()}
              disabled={!project}
              className="flex-1 px-3 py-1.5 rounded bg-red-600 hover:bg-red-500 text-sm disabled:opacity-30"
            >
              ● Record
            </button>
            <button
              onClick={() => handleRecord("sim-android")}
              disabled={!project}
              className="px-2 py-1.5 rounded bg-zinc-700 hover:bg-zinc-600 text-xs disabled:opacity-30"
            >
              Android
            </button>
            <button
              onClick={() => handleRecord("phone")}
              disabled={!project}
              className="px-2 py-1.5 rounded bg-zinc-700 hover:bg-zinc-600 text-xs disabled:opacity-30"
            >
              Phone
            </button>
          </div>

          <div className="px-3 py-2 border-t border-white/10 text-xs uppercase tracking-wider text-zinc-400">
            Events
          </div>
          <div className="flex-1 overflow-y-auto px-3 py-2 text-[11px] font-mono">
            {recentEvents.map((ev, i) => (
              <div key={i} className="text-emerald-700 dark:text-emerald-300/80 truncate">
                {ev.ts.slice(11, 19)} · {ev.type}
                {ev.hash ? ` · ${ev.hash.slice(0, 6)}` : ""}
                {ev.clipId ? ` · ${ev.clipId}` : ""}
                {ev.message ? ` · ${ev.message}` : ""}
              </div>
            ))}
          </div>
        </aside>
      </div>
    </div>
  );
}
