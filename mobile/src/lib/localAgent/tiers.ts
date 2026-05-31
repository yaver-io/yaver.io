// localAgent/tiers.ts — pure model-tier selection for the on-device LLM.
//
// One engine (llama.rn), three tiers (see SPIKE-local-voice-helper-...md):
//   router  — small 1B Q4, bundled, voice helper / onboarding / device control
//   coder   — 3B Q4, downloaded on-the-fly, Mobile Sandbox codegen (8GB+ only)
//   none    — fallback: keyword + scripted guidance (model load not viable)
//
// The selector reads the device's own capability — ideally the schema's
// edgeProfile { maxModelClass, memoryMb } the phone already reports, with a
// raw-RAM fallback. PURE + RN-free so it unit-tests under tsx; the mobile app
// gathers the capability (expo-device / edgeProfile) and passes it in.

export type ModelTier = "router" | "coder" | "none";

export interface DeviceCapability {
  /** Total physical RAM in MB (e.g. iPhone 14 ≈ 6144). */
  totalRamMb?: number;
  /** Apple chip generation if known ("A15", "A16", "A17", "A18", ...). */
  chip?: string;
  /** edgeProfile.maxModelClass when the device reported one. */
  maxModelClass?: "none" | "tiny" | "small" | "medium";
  /** True only if the runtime confirmed llama.rn loaded a model OK before. */
  inferenceKnownGood?: boolean;
  /** Current thermal state — refuse heavy models when hot. */
  thermalState?: "nominal" | "warm" | "hot";
}

// RAM thresholds (MB). Conservative against iOS jetsam (~50% of total).
//  router needs ~1GB resident for a 1B Q4 → safe from ~4GB total up.
//  coder needs ~2.5-3GB for a 3B Q4 → only safe from ~8GB total up.
const ROUTER_MIN_RAM_MB = 4000;
const CODER_MIN_RAM_MB = 7500; // 8GB devices report ~7.7-8GB

function chipGen(chip?: string): number | null {
  if (!chip) return null;
  const m = /a(\d{2})/i.exec(chip.trim());
  return m ? Number(m[1]) : null;
}

/**
 * Highest model tier this device can safely run RIGHT NOW. Conservative:
 * when signals are missing or the device is hot, fall back rather than risk
 * a jetsam kill.
 */
export function selectModelTier(cap: DeviceCapability): ModelTier {
  // Never run a heavy model while thermally throttled.
  if (cap.thermalState === "hot") return "none";

  const ram = cap.totalRamMb ?? 0;
  const gen = chipGen(cap.chip);
  const cls = cap.maxModelClass;

  // Coder (3B): needs real headroom. Require BOTH a RAM signal AND (chip or
  // edgeProfile) to agree — don't promote to coder on a single weak hint.
  const ramSaysCoder = ram >= CODER_MIN_RAM_MB;
  const classSaysCoder = cls === "medium";
  const chipSaysCoder = gen != null && gen >= 16; // A16+ (15 Pro / 16 / 17)
  if (ramSaysCoder && (classSaysCoder || chipSaysCoder)) return "coder";

  // Router (1B): the broad default. Any one credible capability signal is
  // enough — RAM ≥4GB, edgeProfile small/medium, A15+, or a prior good load.
  const ramSaysRouter = ram >= ROUTER_MIN_RAM_MB;
  const classSaysRouter = cls === "small" || cls === "medium";
  const chipSaysRouter = gen != null && gen >= 15; // A15+ → iPhone 13/14+
  if (cap.inferenceKnownGood || ramSaysRouter || classSaysRouter || chipSaysRouter) {
    return "router";
  }

  // Unknown / weak device → scripted fallback (still fully functional, just
  // keyword-driven instead of model-driven).
  return "none";
}

export interface ModelOption {
  tier: ModelTier;
  /** Canonical model id used in the GitHub-Releases manifest. */
  id: string;
  label: string;
  approxSizeMb: number;
  recommended: boolean;
}

/**
 * The model picker the mobile app shows. Marks ONE option "recommended" for
 * the given device so the UI can render "Recommended for your device".
 * Coder options only surface on devices that can run them.
 */
export function modelOptionsFor(cap: DeviceCapability): ModelOption[] {
  const tier = selectModelTier(cap);
  const opts: ModelOption[] = [
    { tier: "router", id: "qwen2.5-1.5b-instruct-q4", label: "Voice helper (1.5B)", approxSizeMb: 1100, recommended: false },
    { tier: "router", id: "llama-3.2-1b-instruct-q4", label: "Voice helper · lite (1B)", approxSizeMb: 800, recommended: false },
  ];
  // Coder options only when the device can actually run them.
  if (tier === "coder") {
    opts.push(
      { tier: "coder", id: "qwen2.5-coder-3b-q4", label: "Coder (3B)", approxSizeMb: 2200, recommended: true },
      { tier: "coder", id: "qwen2.5-coder-1.5b-q4", label: "Coder · lite (1.5B)", approxSizeMb: 1100, recommended: false },
    );
    return opts;
  }
  // Router-class device: recommend the 1.5B helper.
  if (tier === "router") {
    const pick = opts.find((o) => o.id === "qwen2.5-1.5b-instruct-q4");
    if (pick) pick.recommended = true;
  }
  // tier "none": still offer the lite router as an optional download (it may
  // run, just not guaranteed) — recommend nothing.
  return opts;
}
