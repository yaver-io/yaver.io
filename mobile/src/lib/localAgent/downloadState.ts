// localAgent/downloadState.ts — pure state machine for background model
// downloads. PURE + RN-free (tsx-tested).
//
// Modern-app behavior (kivanc): at onboarding we ASK before downloading a
// model; on yes we download in the BACKGROUND with a status pill and never
// block the UI — the user can keep pairing / skip / use the app while it runs.
// This module owns the *state* of those downloads (idle → asked → downloading
// → verifying → ready / failed / cancelled) so the UI and the native
// fetch/verify adapter stay thin and the transitions are unit-tested.
//
// The native side (expo-file-system resumable download + sha256 verify against
// models.ts) calls the reducer's transition helpers; the UI renders the
// snapshot. No model bytes flow through here — only status + progress.

export type DownloadPhase =
  | "idle" // not started, not asked
  | "prompted" // we asked the user; awaiting yes/no
  | "queued" // user said yes; waiting for a slot / network
  | "downloading" // bytes flowing
  | "verifying" // sha256 check after download
  | "ready" // verified + cached; usable
  | "failed" // download or verify error (retryable)
  | "cancelled"; // user cancelled

export interface DownloadItem {
  modelId: string;
  phase: DownloadPhase;
  /** 0..1 while downloading. */
  progress: number;
  /** bytes received / total, for a "120 / 800 MB" style label. */
  receivedBytes?: number;
  totalBytes?: number;
  error?: string;
  /** monotonic attempt count (retries). */
  attempts: number;
}

export type DownloadMap = Record<string, DownloadItem>;

function item(modelId: string, prev?: DownloadItem): DownloadItem {
  return prev ?? { modelId, phase: "idle", progress: 0, attempts: 0 };
}

// ── Transitions (pure: (map, ...) -> new map) ──────────────────────

/** Ask the user whether to download a model (shows the consent prompt). */
export function promptDownload(map: DownloadMap, modelId: string): DownloadMap {
  const it = item(modelId, map[modelId]);
  // Don't re-prompt something already in flight or done.
  if (["queued", "downloading", "verifying", "ready"].includes(it.phase)) return map;
  return { ...map, [modelId]: { ...it, phase: "prompted", error: undefined } };
}

/** User accepted → queue it for background download (UI stays unblocked). */
export function acceptDownload(map: DownloadMap, modelId: string): DownloadMap {
  const it = item(modelId, map[modelId]);
  return { ...map, [modelId]: { ...it, phase: "queued", progress: 0, error: undefined } };
}

/** User declined the prompt. */
export function declineDownload(map: DownloadMap, modelId: string): DownloadMap {
  const it = item(modelId, map[modelId]);
  return { ...map, [modelId]: { ...it, phase: "idle" } };
}

/** Adapter picked the queued item up and started fetching. */
export function startDownloading(map: DownloadMap, modelId: string): DownloadMap {
  const it = item(modelId, map[modelId]);
  return { ...map, [modelId]: { ...it, phase: "downloading", progress: 0, attempts: it.attempts + 1 } };
}

/** Progress tick from the resumable download. */
export function setProgress(
  map: DownloadMap,
  modelId: string,
  received: number,
  total: number,
): DownloadMap {
  const it = item(modelId, map[modelId]);
  if (it.phase !== "downloading") return map;
  const progress = total > 0 ? Math.min(1, received / total) : it.progress;
  return { ...map, [modelId]: { ...it, progress, receivedBytes: received, totalBytes: total } };
}

/** Bytes done → verifying sha256. */
export function startVerifying(map: DownloadMap, modelId: string): DownloadMap {
  const it = item(modelId, map[modelId]);
  return { ...map, [modelId]: { ...it, phase: "verifying", progress: 1 } };
}

/** Verified + cached → ready to use. */
export function markReady(map: DownloadMap, modelId: string): DownloadMap {
  const it = item(modelId, map[modelId]);
  return { ...map, [modelId]: { ...it, phase: "ready", progress: 1, error: undefined } };
}

/** Download or verify failed (retryable). */
export function markFailed(map: DownloadMap, modelId: string, error: string): DownloadMap {
  const it = item(modelId, map[modelId]);
  return { ...map, [modelId]: { ...it, phase: "failed", error } };
}

/** User cancelled an in-flight download. */
export function cancelDownload(map: DownloadMap, modelId: string): DownloadMap {
  const it = item(modelId, map[modelId]);
  return { ...map, [modelId]: { ...it, phase: "cancelled", progress: 0 } };
}

/** Retry a failed/cancelled download (back to queued). */
export function retryDownload(map: DownloadMap, modelId: string): DownloadMap {
  const it = item(modelId, map[modelId]);
  if (it.phase !== "failed" && it.phase !== "cancelled") return map;
  return { ...map, [modelId]: { ...it, phase: "queued", error: undefined } };
}

// ── Selectors ──────────────────────────────────────────────────────

export function isActive(it?: DownloadItem): boolean {
  return !!it && (it.phase === "queued" || it.phase === "downloading" || it.phase === "verifying");
}

export function ids(map: DownloadMap, phase: DownloadPhase): string[] {
  return Object.values(map).filter((i) => i.phase === phase).map((i) => i.modelId);
}

/** ids that finished + verified — feed into modelPicker(downloadedIds). */
export function readyIds(map: DownloadMap): string[] {
  return ids(map, "ready");
}

/** A short status line for a background pill: "Voice helper · 120/800 MB". */
export function statusLabel(it: DownloadItem | undefined, modelLabel: string): string | null {
  if (!it) return null;
  switch (it.phase) {
    case "queued":
      return `${modelLabel} · queued`;
    case "downloading": {
      const pct = Math.round(it.progress * 100);
      if (it.totalBytes) {
        const mb = (b: number) => Math.round(b / 1_000_000);
        return `${modelLabel} · ${mb(it.receivedBytes ?? 0)}/${mb(it.totalBytes)} MB`;
      }
      return `${modelLabel} · ${pct}%`;
    }
    case "verifying":
      return `${modelLabel} · verifying`;
    case "ready":
      return `${modelLabel} · ready`;
    case "failed":
      return `${modelLabel} · failed — tap to retry`;
    default:
      return null;
  }
}
