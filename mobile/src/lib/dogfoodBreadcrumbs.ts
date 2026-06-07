/**
 * dogfoodBreadcrumbs — lightweight rolling context buffer for dogfood mode.
 *
 * NOT video. While dogfood mode is on, this keeps the last ~90s / 50 events of
 * what you did inside Yaver (route changes, coarse taps, recent errors) in
 * memory, so a caught screenshot arrives with "what led up to it." Only the
 * snapshot attached to a *sent* dogfood item is persisted/transmitted (P2P,
 * never Convex) — the live buffer is in-memory only.
 *
 * Yaver-context only: callers (dogfoodMode) gate pushes on host + foreground.
 */

export type BreadcrumbType = "route" | "tap" | "error" | "note";

export interface Breadcrumb {
  /** ms epoch */
  t: number;
  type: BreadcrumbType;
  label: string;
}

const MAX_ENTRIES = 50;
const WINDOW_MS = 90_000;

let buffer: Breadcrumb[] = [];

function prune(now: number): void {
  const cutoff = now - WINDOW_MS;
  if (buffer.length && buffer[0].t < cutoff) {
    buffer = buffer.filter((b) => b.t >= cutoff);
  }
  if (buffer.length > MAX_ENTRIES) {
    buffer = buffer.slice(buffer.length - MAX_ENTRIES);
  }
}

export function pushBreadcrumb(type: BreadcrumbType, label: string): void {
  const trimmed = (label ?? "").trim();
  if (!trimmed) return;
  const now = Date.now();
  const last = buffer[buffer.length - 1];
  // Collapse consecutive identical breadcrumbs (e.g. re-renders firing the
  // same route) into one — keep the latest timestamp.
  if (last && last.type === type && last.label === trimmed) {
    last.t = now;
    return;
  }
  buffer.push({ t: now, type, label: trimmed });
  prune(now);
}

/** Copy of the current buffer, pruned to the window. */
export function snapshotBreadcrumbs(): Breadcrumb[] {
  prune(Date.now());
  return buffer.map((b) => ({ ...b }));
}

export function clearBreadcrumbs(): void {
  buffer = [];
}

/**
 * Human-readable trail for the agent prompt, e.g.
 *   "Settings → tapped Devices → DeviceDetails (error: heartbeat 500) → here"
 * Relative to `now` so the agent sees recency.
 */
export function formatBreadcrumbs(crumbs?: Breadcrumb[]): string {
  const list = crumbs ?? snapshotBreadcrumbs();
  if (!list.length) return "";
  const parts = list.map((b) => {
    switch (b.type) {
      case "route":
        return b.label;
      case "tap":
        return `tapped ${b.label}`;
      case "error":
        return `(error: ${b.label})`;
      case "note":
        return `[${b.label}]`;
      default:
        return b.label;
    }
  });
  return parts.join(" → ");
}
