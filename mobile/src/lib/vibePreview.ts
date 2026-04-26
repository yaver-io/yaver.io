// vibePreview.ts — mobile client for the Yaver vibe-preview feature.
// See docs/vibe-preview-streaming.md and desktop/agent/vibe_preview.go.
//
// One module, two surfaces:
//   • REST: start/stop/status/snapshot, clip start/stop, list clips, frame URLs
//   • SSE:  subscribeEvents() yields {type, hash, clipId, ...} live + replays history
//
// SSE in React Native: no native EventSource, so we tail fetch().body chunks
// manually — same approach DevPreview.tsx uses for /dev/events. Slow consumers
// don't drop frames here (we render text events, not pixels) so a bog-standard
// reader is enough.

import { quicClient } from "./quic";

export type VibePreviewMode = "live" | "change-only" | "summary-only";

export type VibePreviewProfile =
  | "live-direct"
  | "live-relay-wifi"
  | "live-relay-cell"
  | "change-only"
  | "summary-only";

export type VibeClipSource =
  | "browser"
  | "sim-ios"
  | "sim-android"
  | "phone";

export interface VibePreviewSession {
  id: string;
  project: string;
  targetUrl: string;
  browserId: string;
  mode: VibePreviewMode;
  profile: {
    name: string;
    fps: number;
    width: number;
    height: number;
    quality: number;
    maxFrameKB: number;
  };
  startedAt: string;
  lastFrame: string;
  frameCount: number;
  stableHits: number;
  errors: number;
}

export interface VibeClipRecord {
  id: string;
  project: string;
  source: VibeClipSource;
  startedAt: string;
  endedAt?: string;
  durationSec?: number;
  sizeBytes?: number;
  status: "recording" | "ready" | "failed";
  err?: string;
}

export interface VibePreviewEvent {
  type:
    | "frame"
    | "stable"
    | "throttle"
    | "capture_error"
    | "started"
    | "stopped"
    | "clip_started"
    | "clip_ready"
    | "summary"
    | "crash";
  project?: string;
  seq?: number;
  hash?: string;
  size?: number;
  width?: number;
  height?: number;
  fps?: number;
  mode?: string;
  clipId?: string;
  source?: VibeClipSource;
  durationSec?: number;
  message?: string;
  ts: string;
}

// ─── Connection helpers ──────────────────────────────────────────────────────

function baseUrl(): string | null {
  return quicClient.isConnected && quicClient.baseUrl ? quicClient.baseUrl : null;
}

function authHeaders(): Record<string, string> {
  return quicClient.getAuthHeaders();
}

/** Detect the developer's current network mode for an adaptive profile hint. */
export function getNetMode(): "direct" | "relay-wifi" | "relay-cell" {
  // quicClient.connectionMode = "direct" | "relay" + we know if cellular from
  // its internal NetInfo state. Conservative fallback when uncertain: relay-wifi.
  const mode = (quicClient as any).connectionMode as string | undefined;
  const isCellular = (quicClient as any).isCellular === true;
  if (mode === "direct") return "direct";
  if (isCellular) return "relay-cell";
  return "relay-wifi";
}

// ─── REST surface ────────────────────────────────────────────────────────────

export interface StartPreviewOpts {
  project: string;
  targetUrl: string;
  mode?: VibePreviewMode;
  profile?: VibePreviewProfile | "";
  netMode?: "" | "direct" | "relay-wifi" | "relay-cell";
}

export async function startPreview(opts: StartPreviewOpts): Promise<VibePreviewSession | null> {
  const base = baseUrl();
  if (!base) return null;
  const body = {
    project: opts.project,
    targetUrl: opts.targetUrl,
    mode: opts.mode ?? "live",
    profile: opts.profile ?? "",
    netMode: opts.netMode ?? getNetMode(),
  };
  try {
    const res = await fetch(`${base}/vibing/preview/start`, {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        "X-Yaver-NetMode": body.netMode,
      },
      body: JSON.stringify(body),
    });
    if (!res.ok) return null;
    const data = await res.json();
    return data?.session ?? null;
  } catch {
    return null;
  }
}

export async function stopPreview(project: string): Promise<boolean> {
  const base = baseUrl();
  if (!base) return false;
  try {
    const res = await fetch(`${base}/vibing/preview/stop`, {
      method: "POST",
      headers: { ...authHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify({ project }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

export async function listSessions(): Promise<VibePreviewSession[]> {
  const base = baseUrl();
  if (!base) return [];
  try {
    const res = await fetch(`${base}/vibing/preview/status`, { headers: authHeaders() });
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data?.sessions) ? data.sessions : [];
  } catch {
    return [];
  }
}

export async function snapshot(project: string): Promise<{ seq: number; hash: string } | null> {
  const base = baseUrl();
  if (!base) return null;
  try {
    const res = await fetch(`${base}/vibing/preview/snapshot`, {
      method: "POST",
      headers: { ...authHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify({ project }),
    });
    if (!res.ok) return null;
    const data = await res.json();
    return { seq: data.seq, hash: data.hash };
  } catch {
    return null;
  }
}

/** Returns the URL for a frame by content-hash. The Image cache dedupes
 *  identical hashes for free, so a stable scene = zero re-downloads. */
export function frameUrl(project: string, hash: string): string | null {
  const base = baseUrl();
  if (!base) return null;
  return `${base}/vibing/preview/frames/${encodeURIComponent(hash)}?project=${encodeURIComponent(project)}`;
}

// ─── Clips ──────────────────────────────────────────────────────────────────

export interface StartClipOpts {
  project: string;
  source?: VibeClipSource;
  durationMaxSec?: number;
  exerciseHint?: string;
}

export async function startClip(opts: StartClipOpts): Promise<VibeClipRecord | null> {
  const base = baseUrl();
  if (!base) return null;
  try {
    const res = await fetch(`${base}/vibing/preview/clip/start`, {
      method: "POST",
      headers: { ...authHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify(opts),
    });
    if (!res.ok) return null;
    const data = await res.json();
    return data?.clip ?? null;
  } catch {
    return null;
  }
}

export async function stopClip(clipId: string): Promise<boolean> {
  const base = baseUrl();
  if (!base) return false;
  try {
    const res = await fetch(`${base}/vibing/preview/clip/stop`, {
      method: "POST",
      headers: { ...authHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify({ clipId }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

export async function listClips(project: string): Promise<VibeClipRecord[]> {
  const base = baseUrl();
  if (!base) return [];
  try {
    const res = await fetch(`${base}/vibing/preview/clips?project=${encodeURIComponent(project)}`, {
      headers: authHeaders(),
    });
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data?.clips) ? data.clips : [];
  } catch {
    return [];
  }
}

/** Returns the URL for a clip MP4. Range requests work — expo-av <Video>
 *  uses them automatically for seek without a re-download. */
export function clipUrl(clipId: string): string | null {
  const base = baseUrl();
  if (!base) return null;
  return `${base}/vibing/preview/clip/${encodeURIComponent(clipId)}`;
}

export function clipPosterUrl(clipId: string): string | null {
  const base = baseUrl();
  if (!base) return null;
  return `${base}/vibing/preview/clip/${encodeURIComponent(clipId)}/poster`;
}

// ─── SSE ─────────────────────────────────────────────────────────────────────

export interface SubscribeOpts {
  signal?: AbortSignal;
  onEvent: (ev: VibePreviewEvent) => void;
  onError?: (err: unknown) => void;
}

/** Subscribe to /vibing/preview/events for a project. Returns an
 *  unsubscribe function. SSE is consumed via fetch().body so this works
 *  without a polyfill on RN — same approach DevPreview.tsx uses. */
export function subscribeEvents(project: string, opts: SubscribeOpts): () => void {
  const base = baseUrl();
  if (!base) {
    opts.onError?.(new Error("not connected"));
    return () => {};
  }
  const controller = new AbortController();
  const signal = opts.signal
    ? mergeAbortSignals(controller.signal, opts.signal)
    : controller.signal;

  void streamLoop(base, project, signal, opts);

  return () => controller.abort();
}

async function streamLoop(
  base: string,
  project: string,
  signal: AbortSignal,
  opts: SubscribeOpts,
) {
  const url = `${base}/vibing/preview/events?project=${encodeURIComponent(project)}`;
  try {
    const res = await fetch(url, {
      headers: { ...authHeaders(), Accept: "text/event-stream" },
      signal,
    });
    if (!res.ok || !res.body) {
      opts.onError?.(new Error(`events stream returned ${res.status}`));
      return;
    }
    const reader = (res.body as any).getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    while (!signal.aborted) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      // SSE messages are separated by blank lines; one or more "data:"
      // lines per message. Concatenate all data: lines, then JSON.parse.
      let idx;
      while ((idx = buffer.indexOf("\n\n")) >= 0) {
        const chunk = buffer.slice(0, idx);
        buffer = buffer.slice(idx + 2);
        const dataLines = chunk
          .split("\n")
          .filter((l) => l.startsWith("data:"))
          .map((l) => l.slice(5).trimStart());
        if (dataLines.length === 0) continue;
        const payload = dataLines.join("\n");
        try {
          const ev = JSON.parse(payload) as VibePreviewEvent;
          opts.onEvent(ev);
        } catch (err) {
          // Malformed payload — likely a keepalive ping. Skip silently.
        }
      }
    }
  } catch (err) {
    if (!signal.aborted) opts.onError?.(err);
  }
}

function mergeAbortSignals(a: AbortSignal, b: AbortSignal): AbortSignal {
  const ctrl = new AbortController();
  const onAbort = () => ctrl.abort();
  a.addEventListener("abort", onAbort);
  b.addEventListener("abort", onAbort);
  if (a.aborted || b.aborted) ctrl.abort();
  return ctrl.signal;
}
