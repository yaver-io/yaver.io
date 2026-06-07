// codingBackend.ts — which LLM "backend" answers a Mobile-Sandbox coding
// request, and how we auto-pick one. PURE + RN-free (tsx-tested).
//
// The Mobile Sandbox edits a phone-local src/ tree (no paired machine). The
// user can code it with EITHER an on-device model OR a BYO-key cloud model —
// every option produces the same provider-agnostic EditPlan (see llmClient.ts)
// that we preview + apply against the local files. This module owns the
// *identity* of those backends and the deterministic "auto" selection so the
// UI and the RN factory (codingBackendStore.ts) stay thin and auditable.
//
// "remote runner on a connected box" is a DIFFERENT mode (the create-wizard's
// codingMode="runner" — quicClient.sendTask) that edits files on the remote
// machine, not the phone sandbox. It is intentionally NOT one of these
// backends: these are the engines that touch the local sandbox tree.

/** The concrete coding engines that produce an EditPlan for the local sandbox. */
export type CodingBackendId = "local" | "anthropic" | "openai" | "glm";

/** What the user has chosen. "auto" = let resolveAutoBackend pick per availability. */
export type CodingBackendPref = "auto" | CodingBackendId;

export type CodingBackendKind = "on-device" | "cloud";

export interface CodingBackendMeta {
  id: CodingBackendId;
  label: string;
  kind: CodingBackendKind;
  /** One-line note for the picker. */
  note: string;
  /** For cloud backends: which BYO key slot it needs (none for local). */
  requiresKey?: "openai" | "glm" | "anthropic";
}

export const CODING_BACKENDS: readonly CodingBackendMeta[] = [
  {
    id: "local",
    label: "On-device model",
    kind: "on-device",
    note: "Runs a downloaded coder model on this phone. Fully offline, no API key, no cloud.",
  },
  {
    id: "anthropic",
    label: "Claude (BYO key)",
    kind: "cloud",
    note: "Anthropic Claude via your own API key. Highest quality; needs network.",
    requiresKey: "anthropic",
  },
  {
    id: "openai",
    label: "OpenAI (BYO key)",
    kind: "cloud",
    note: "OpenAI GPT via your own API key.",
    requiresKey: "openai",
  },
  {
    id: "glm",
    label: "GLM (BYO key)",
    kind: "cloud",
    note: "Zhipu GLM via your own API key. Cheap, capable.",
    requiresKey: "glm",
  },
] as const;

export function backendMeta(id: CodingBackendId): CodingBackendMeta {
  const m = CODING_BACKENDS.find((b) => b.id === id);
  if (!m) throw new Error(`unknown coding backend: ${id}`);
  return m;
}

/** Live availability of each backend on this device, gathered by the RN layer. */
export interface CodingBackendAvailability {
  /** A coder model is downloaded AND the native engine is linked (engineAvailable). */
  localModelReady: boolean;
  /** BYO keys present in the keychain. */
  anthropicKey: boolean;
  openaiKey: boolean;
  glmKey: boolean;
}

export function backendUsable(id: CodingBackendId, av: CodingBackendAvailability): boolean {
  switch (id) {
    case "local":
      return av.localModelReady;
    case "anthropic":
      return av.anthropicKey;
    case "openai":
      return av.openaiKey;
    case "glm":
      return av.glmKey;
  }
}

/** Every backend the user could pick right now (usable === true). */
export function usableBackends(av: CodingBackendAvailability): CodingBackendId[] {
  return CODING_BACKENDS.filter((b) => backendUsable(b.id, av)).map((b) => b.id);
}

/**
 * Auto-pick a backend. Policy (mirrors the local-first privacy stance of
 * brain.ts but for the *sandbox* which has no remote project context):
 *   1. On-device model if it's ready — private, free, offline.
 *   2. Otherwise the strongest available cloud key: Anthropic → OpenAI → GLM.
 *   3. null when nothing is configured (UI prompts the user to set one up).
 */
export function resolveAutoBackend(av: CodingBackendAvailability): CodingBackendId | null {
  if (av.localModelReady) return "local";
  if (av.anthropicKey) return "anthropic";
  if (av.openaiKey) return "openai";
  if (av.glmKey) return "glm";
  return null;
}

export interface ResolvedBackend {
  /** The backend that will actually run, or null when none is usable. */
  id: CodingBackendId | null;
  /** True when the pref was "auto" (so the UI can show "Auto · Claude"). */
  auto: boolean;
  /** Set when the user's explicit pick isn't usable and we had to fall back / refuse. */
  fellBackFrom?: CodingBackendId;
}

/**
 * Resolve the user's preference against what's actually available.
 *  - "auto" → resolveAutoBackend.
 *  - an explicit pick that IS usable → that pick.
 *  - an explicit pick that is NOT usable → fall back to auto, recording the
 *    pick we couldn't honor so the UI can explain ("Claude key missing —
 *    using on-device model").
 */
export function resolveBackend(
  pref: CodingBackendPref,
  av: CodingBackendAvailability,
): ResolvedBackend {
  if (pref === "auto") {
    return { id: resolveAutoBackend(av), auto: true };
  }
  if (backendUsable(pref, av)) {
    return { id: pref, auto: false };
  }
  return { id: resolveAutoBackend(av), auto: true, fellBackFrom: pref };
}
