// localAgent/models.ts — the single source of truth for every on-device LLM
// the app can run. PURE + RN-free (tsx-tested).
//
// Compatibility guarantee: EVERY model here runs on the SAME engine
// (llama.rn / llama.cpp, GGUF Q4) — the bundled one and all downloadables.
// There is exactly one `engine` value; the registry asserts it. So "is this
// model compatible with the app?" is answered structurally: if it's in this
// registry it loads through the one compiled-in engine. No second runtime is
// ever introduced (would violate App Store 2.5.2 anyway — see SPIKE).
//
// Hosting: the bundled router ships inside the binary (no URL). Everything
// else downloads on-the-fly from GitHub Releases (kivanccakmak/yaver-models)
// with a sha256 the app verifies. This module just declares them; the native
// download/verify adapter consumes `downloadUrl` + `sha256`.

import type { ModelTier } from "./tiers";

/** The one and only inference engine. Asserted across the registry. */
export const ENGINE = "llama.rn" as const;
export type Engine = typeof ENGINE;

export interface ModelEntry {
  /** Stable id used by the picker, manifest, and on-disk cache filename. */
  id: string;
  label: string;
  tier: ModelTier; // "router" | "coder"
  engine: Engine; // always ENGINE — the compatibility guarantee
  quant: string; // e.g. "Q4_K_M"
  approxSizeMb: number;
  /** Minimum total RAM (MB) this model needs to load without jetsam risk. */
  minRamMb: number;
  /** true → ships in the app binary (no download). Exactly one router is bundled. */
  bundled: boolean;
  /** GitHub-Releases asset URL for downloadable models (absent when bundled). */
  downloadUrl?: string;
  /** Hex sha256 of the GGUF, verified after download (absent when bundled). */
  sha256?: string;
  /** One-line capability note for the picker. */
  note: string;
}

// The GitHub Releases base the downloadable GGUFs live under. Tag/asset names
// are appended per entry. (Repo: kivanccakmak/yaver-models — Releases only,
// NOT in git history; up to 2GB/asset, free CDN — see SPIKE hosting section.)
const REL = "https://github.com/kivanccakmak/yaver-models/releases/download";

export const MODEL_REGISTRY: ModelEntry[] = [
  // ── Router tier ──────────────────────────────────────────────────
  {
    id: "llama-3.2-1b-instruct-q4",
    label: "Voice helper · lite (1B)",
    tier: "router",
    engine: ENGINE,
    quant: "Q4_K_M",
    approxSizeMb: 800,
    minRamMb: 3500,
    bundled: true, // ← the included model: ships with the app, instant + offline
    note: "Bundled. Powers voice onboarding, troubleshooting, and device control offline.",
  },
  {
    id: "qwen2.5-1.5b-instruct-q4",
    label: "Voice helper (1.5B)",
    tier: "router",
    engine: ENGINE,
    quant: "Q4_K_M",
    approxSizeMb: 1100,
    minRamMb: 4000,
    bundled: false,
    downloadUrl: `${REL}/router-v1/qwen2.5-1.5b-instruct-q4_k_m.gguf`,
    sha256: "", // filled when the release asset is published
    note: "Sharper routing + tool-calling than the bundled lite model.",
  },
  // ── Coder tier (downloadable; high-RAM only) ─────────────────────
  {
    id: "qwen2.5-coder-1.5b-q4",
    label: "Coder · lite (1.5B)",
    tier: "coder",
    engine: ENGINE,
    quant: "Q4_K_M",
    approxSizeMb: 1100,
    minRamMb: 4500,
    bundled: false,
    downloadUrl: `${REL}/coder-v1/qwen2.5-coder-1.5b-q4_k_m.gguf`,
    sha256: "",
    note: "On-device coding for the Mobile Sandbox on mid devices.",
  },
  {
    id: "qwen2.5-coder-3b-q4",
    label: "Coder (3B)",
    tier: "coder",
    engine: ENGINE,
    quant: "Q4_K_M",
    approxSizeMb: 2200,
    minRamMb: 7500,
    bundled: false,
    downloadUrl: `${REL}/coder-v1/qwen2.5-coder-3b-q4_k_m.gguf`,
    sha256: "",
    note: "Full-stack Sandbox codegen. Needs an 8GB+ device.",
  },
];

// Compatibility assertion: every registered model uses the one engine.
// (Run at import; a mismatched entry is a build-time bug, not a runtime one.)
for (const m of MODEL_REGISTRY) {
  if (m.engine !== ENGINE) {
    throw new Error(`model ${m.id} declares engine ${m.engine}, but the app only ships ${ENGINE}`);
  }
  if (!m.bundled && (!m.downloadUrl || m.sha256 === undefined)) {
    throw new Error(`downloadable model ${m.id} must declare downloadUrl + sha256`);
  }
}

export function getModel(id: string): ModelEntry | undefined {
  return MODEL_REGISTRY.find((m) => m.id === id);
}

/** The single bundled model (ships in the binary). */
export function bundledModel(): ModelEntry {
  const b = MODEL_REGISTRY.find((m) => m.bundled);
  if (!b) throw new Error("registry must declare exactly one bundled model");
  return b;
}

export interface ModelAvailability extends ModelEntry {
  /** Can this device's RAM run it without jetsam risk? */
  runnable: boolean;
  /** Already on the binary (bundled) or downloaded+verified to cache. */
  installed: boolean;
  /** The single best pick for this device (UI: "Recommended for your device"). */
  recommended: boolean;
}

export interface ModelPickerState {
  totalRamMb?: number;
  /** ids the download adapter has verified into the cache. */
  downloadedIds?: string[];
}

/**
 * Build the model-picker list for a device: every model, flagged runnable /
 * installed, with exactly one `recommended`. The bundled model is always
 * installed+runnable. Coder models only become `recommended` on devices that
 * can run them; otherwise the best runnable router is recommended.
 */
export function modelPicker(state: ModelPickerState): ModelAvailability[] {
  const ram = state.totalRamMb ?? 0;
  const downloaded = new Set(state.downloadedIds ?? []);
  const rows: ModelAvailability[] = MODEL_REGISTRY.map((m) => ({
    ...m,
    runnable: m.bundled || ram === 0 ? true : ram >= m.minRamMb,
    installed: m.bundled || downloaded.has(m.id),
    recommended: false,
  }));

  // Recommend a CODER only on a genuinely coder-class device (8GB+), matching
  // tiers.ts CODER_MIN_RAM_MB. A small coder can technically load on a 6GB
  // phone, but the intended on-device coding experience is 8GB+ — below that we
  // recommend a router and let the user opt into a coder manually if they want.
  const CODER_CLASS_RAM_MB = 7500;
  if (ram >= CODER_CLASS_RAM_MB) {
    const runnableCoders = rows
      .filter((r) => r.tier === "coder" && ram >= r.minRamMb)
      .sort((a, b) => b.approxSizeMb - a.approxSizeMb);
    if (runnableCoders.length > 0) {
      runnableCoders[0].recommended = true;
      return rows;
    }
  }
  const runnableRouters = rows
    .filter((r) => r.tier === "router" && r.runnable)
    .sort((a, b) => b.approxSizeMb - a.approxSizeMb);
  if (runnableRouters.length > 0) runnableRouters[0].recommended = true;
  return rows;
}
