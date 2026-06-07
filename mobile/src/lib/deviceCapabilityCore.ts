// deviceCapabilityCore.ts — PURE + RN-free device-capability math (tsx-tested).
// The expo-device reader lives in deviceCapability.ts; this is the part that
// decides "can this phone safely run that model?" so the gate is unit-tested.
//
// Why a hard gate matters: loading a 3B Q4 coder needs ~2.5-3GB resident. On a
// 6GB iPhone that risks an iOS jetsam kill (OS reaps the app). So we NEVER load
// a model whose minRamMb exceeds the device — we fall back to a cloud key or
// scripted mode instead. RAM is the honest, always-available signal (chip /
// thermal are best-effort), so RAM drives both the coarse class and the gate.

import type { DeviceCapability, ModelTier } from "./localAgent/tiers";

const MB = 1024 * 1024;

/** Bytes (expo-device totalMemory) → whole MB, or undefined when unknown. */
export function bytesToMb(bytes?: number | null): number | undefined {
  if (!bytes || bytes <= 0) return undefined;
  return Math.round(bytes / MB);
}

/**
 * Map measured RAM to the coarse maxModelClass tiers.ts expects, so an 8GB+
 * phone reports "medium" (→ coder eligible) without needing a chip lookup.
 * Thresholds mirror tiers.ts (ROUTER_MIN 4000, CODER_MIN 7500).
 */
export function ramToModelClass(ramMb?: number): DeviceCapability["maxModelClass"] {
  if (!ramMb) return undefined;
  if (ramMb >= 7500) return "medium";
  if (ramMb >= 4000) return "small";
  if (ramMb >= 2500) return "tiny";
  return "none";
}

/**
 * HARD gate: may we load a model needing `minRamMb` on this device right now?
 *  - thermally hot → never (avoid throttled, kill-prone loads)
 *  - unknown RAM → refuse (safe default; better to fall back than risk jetsam)
 *  - otherwise require total RAM ≥ the model's floor.
 */
export function canRunModel(minRamMb: number, cap: DeviceCapability): boolean {
  if (cap.thermalState === "hot") return false;
  const ram = cap.totalRamMb ?? 0;
  if (ram <= 0) return false;
  return ram >= minRamMb;
}

/** Human label for the device's on-device-AI class, for UI copy. */
export function capabilityClassLabel(tier: ModelTier): string {
  switch (tier) {
    case "coder":
      return "High — can run on-device coding models";
    case "router":
      return "Standard — can run the on-device voice helper";
    case "none":
      return "Limited — on-device models aren't recommended; use a cloud key";
  }
}
