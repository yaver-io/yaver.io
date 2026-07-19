/**
 * loadAppIntent.ts — "load me the app" by voice.
 *
 * The vibing loop's one phone-specific interceptor: while you talk to code on a
 * box, you can also say "load me the todo app with Hermes", "load sfmg over
 * webrtc", or just "load it" — and the Yaver container loads that app (with the
 * feedback overlay) so you keep vibing on the running thing instead of texting.
 *
 * Pure TS, no imports — classifies the spoken instruction into {app, mode}. The
 * effect (publish to openAppBus + open the Hot Reload tab) lives in the screen;
 * this file only decides "is this a load-app instruction, and for what". It must
 * be conservative: a false positive would steal a real coding instruction ("load
 * the config from disk") away from the runner, so the triggers below are tight.
 *
 * tsx-testable exactly like carMachineSwitch / carSurfaceIntent.
 */

/** How to bring the app up. "auto" lets the Hot Reload flow pick per framework
 *  (Hermes for RN/Expo, native-webrtc for native, dev-server for web). */
export type LoadMode = "hermes" | "webrtc" | "native" | "auto";

export interface LoadAppIntent {
  /** Project/app name to load, or "" when unspecified → open the picker. */
  app: string;
  /** Requested load path; "auto" defers to per-framework detection. */
  mode: LoadMode;
  /** The one short sentence to speak back. */
  spoken: string;
}

// Words that are never part of an app name — the verb, articles, the mode
// words, and the "load me the … app" scaffolding. Matched case-insensitively
// after punctuation is stripped, so "sfmg," still survives as "sfmg".
const STOP_WORDS = new Set([
  "load",
  "reload",
  "open",
  "run",
  "launch",
  "boot",
  "start",
  "render",
  "show",
  "fire",
  "bring",
  "pull",
  "here",
  "me",
  "the",
  "my",
  "a",
  "an",
  "this",
  "that",
  "it",
  "up",
  "again",
  "just",
  "please",
  "now",
  "and",
  "with",
  "using",
  "via",
  "through",
  "in",
  "on",
  "over",
  "for",
  "app",
  "apps",
  "application",
  "project",
  "hermes",
  "webrtc",
  "native",
  "webview",
  "build", // "…with a native build"
]);

/**
 * Classify a complete spoken instruction. Returns null (pass to the runner) for
 * anything that isn't clearly a "load an app into the container" request.
 */
export function classifyLoadApp(text: string): LoadAppIntent | null {
  const clean = text.trim();
  if (!clean) return null;
  // Normalise "web rtc" / "web-rtc" → "webrtc" before we tokenise.
  const norm = clean.replace(/web[\s-]?rtc/gi, "webrtc");
  const t = ` ${norm.toLowerCase()} `;

  // A load-ish verb must be present. "Strong" verbs (load/reload/render/launch)
  // can carry a pronoun ("render it"); "soft" verbs (show/open/run/…) are too
  // ambiguous with coding, so they only count next to an explicit "app". \bload\b
  // won't fire inside "download"/"upload" — no word boundary — which is intended.
  const strongLoad = /\b(load|reload|render|launch)\b/.test(t);
  const softLoad = /\b(show|open|run|boot|start)\b/.test(t);
  if (!strongLoad && !softLoad) return null;

  const mode = modeFromText(t);

  // Tight trigger: it only counts as a load-app intent when one of these holds,
  // so we don't hijack "load the config file" or "run the tests" into the
  // container.
  const looksLikeApp =
    /\bapps?\b/.test(t) || // "…the app"
    mode !== "auto" || // "…with hermes / webrtc / native"
    (strongLoad && /\b(load|reload|render|launch)\s+(it|this|that)\b/.test(t)) || // "render it"
    (strongLoad && /\b(here|in here)\b/.test(t)) || // "launch it here"
    /\bload\s+me\b/.test(t); // "load me …"
  if (!looksLikeApp) return null;

  const app = extractAppName(norm);
  return { app, mode, spoken: spokenForLoad(app, mode) };
}

function modeFromText(t: string): LoadMode {
  if (/\bhermes\b/.test(t)) return "hermes";
  if (/\bwebrtc\b/.test(t)) return "webrtc";
  if (/\bnative\b/.test(t)) return "native";
  return "auto";
}

/** Everything that isn't scaffolding or a mode word is the app name. */
function extractAppName(norm: string): string {
  const words = norm.split(/\s+/).filter(Boolean);
  const kept: string[] = [];
  for (const w of words) {
    const bare = w.replace(/[^a-z0-9-]/gi, "");
    if (!bare) continue;
    if (STOP_WORDS.has(bare.toLowerCase())) continue;
    kept.push(bare);
  }
  return kept.join(" ").trim();
}

function modeLabel(mode: LoadMode): string {
  switch (mode) {
    case "hermes":
      return "Hermes";
    case "webrtc":
      return "WebRTC";
    case "native":
      return "a native build";
    default:
      return "";
  }
}

export function spokenForLoad(app: string, mode: LoadMode): string {
  const via = mode === "auto" ? "" : ` with ${modeLabel(mode)}`;
  if (!app) return "Opening your apps.";
  return `Loading ${app}${via}.`;
}
