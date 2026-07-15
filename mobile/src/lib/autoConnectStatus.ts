/**
 * autoConnectStatus.ts — one source of truth for how the auto-connect state
 * reads, so every surface (mobile tabs, tablet, glass/AR, car, and — mirrored —
 * web/TV/watch) narrates it identically. Pure + trivially testable.
 *
 * Product intent: after login, silently connect to the primary if it's online,
 * else the secondary; narrate the attempt ("Primary (Mac mini) is online —
 * connecting…") and let the user cancel. Never dump them on a "No machine
 * selected" wall while a connect is in flight.
 */

export interface AutoConnectTarget {
  id: string;
  name: string;
  role: "primary" | "secondary" | "sticky";
}

export function roleWord(role: AutoConnectTarget["role"]): string {
  return role === "primary" ? "Primary" : role === "secondary" ? "Secondary" : "Your machine";
}

/** Compact two-part status for the connection banner (tight width). */
export function autoConnectBannerStatus(t: AutoConnectTarget | null): {
  label: string;
  detail: string;
} {
  if (!t) return { label: "Connecting", detail: "Reaching your machines…" };
  return { label: "Connecting", detail: `${roleWord(t.role)} · ${t.name}` };
}

/** Full sentence for the empty-state / large surfaces. */
export function autoConnectSentence(t: AutoConnectTarget | null): string {
  if (!t) return "Reaching your machines…";
  if (t.role === "sticky") return `Connecting to ${t.name}…`;
  return `${roleWord(t.role)} (${t.name}) is online — connecting…`;
}
