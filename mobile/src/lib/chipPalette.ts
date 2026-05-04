/**
 * Single source of truth for status / transport / lifecycle chip colors
 * across the mobile app. The previous pattern — hardcoded `#xxxxxx22`
 * backgrounds + `#xxxxxx66` borders + raw 500-shade text — was tuned
 * for dark mode and rendered as illegible pastel-on-pastel in light
 * mode (see Tasks tab READY, Devices tab CONNECTED / READY TO CONNECT
 * / YAVER PUBLIC RELAY / Public IP).
 *
 * Light-mode palette uses Tailwind 100 (bg) / 300 (border) / 700
 * (text) / 600 (dot) — tested for >= 4.5:1 contrast on `#ffffff` and
 * `#f5f5f5` (bgCard).
 *
 * Dark-mode palette preserves the existing look so we don't regress
 * dark-mode UX while fixing light mode.
 */

export type ChipTone =
  | "emerald"   // success / connected / yaver public relay
  | "blue"      // info / ready-to-connect / tailscale
  | "violet"    // bootstrap / self-hosted relay / accent
  | "indigo"    // primary / accent variant
  | "amber"     // warn / auth-expired / connecting / cloudflare tunnel / wsl
  | "rose"      // error / public IP
  | "slate";    // neutral / offline / private LAN / unknown

export interface ChipPalette {
  bg: string;
  border: string;
  text: string;
  dot: string;
}

const DARK: Record<ChipTone, ChipPalette> = {
  emerald: { bg: "rgba(34, 197, 94, 0.14)", border: "#22c55e66", text: "#22c55e", dot: "#22c55e" },
  blue:    { bg: "rgba(59, 130, 246, 0.14)", border: "#3b82f666", text: "#3b82f6", dot: "#3b82f6" },
  violet:  { bg: "rgba(124, 102, 255, 0.16)", border: "#7c66ff66", text: "#7c66ff", dot: "#7c66ff" },
  indigo:  { bg: "rgba(124, 102, 255, 0.16)", border: "#7c66ff", text: "#7c66ff", dot: "#7c66ff" },
  amber:   { bg: "rgba(245, 158, 11, 0.14)", border: "#f59e0b66", text: "#f59e0b", dot: "#f59e0b" },
  rose:    { bg: "rgba(239, 68, 68, 0.14)", border: "#ef444466", text: "#ef4444", dot: "#ef4444" },
  slate:   { bg: "rgba(168, 168, 176, 0.10)", border: "#2d2d35", text: "#a8a8b0", dot: "#a8a8b0" },
};

const LIGHT: Record<ChipTone, ChipPalette> = {
  emerald: { bg: "#dcfce7", border: "#86efac", text: "#15803d", dot: "#16a34a" },
  blue:    { bg: "#dbeafe", border: "#93c5fd", text: "#1d4ed8", dot: "#2563eb" },
  violet:  { bg: "#ede9fe", border: "#c4b5fd", text: "#6d28d9", dot: "#7c3aed" },
  indigo:  { bg: "#e0e7ff", border: "#a5b4fc", text: "#4338ca", dot: "#4f46e5" },
  amber:   { bg: "#fef3c7", border: "#fcd34d", text: "#b45309", dot: "#d97706" },
  rose:    { bg: "#fee2e2", border: "#fca5a5", text: "#b91c1c", dot: "#dc2626" },
  slate:   { bg: "#f1f5f9", border: "#cbd5e1", text: "#334155", dot: "#64748b" },
};

export function chipPalette(tone: ChipTone, isDark: boolean): ChipPalette {
  return (isDark ? DARK : LIGHT)[tone];
}
