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
// NOTE: "runner" coding mode (the create-wizard's codingMode="runner" —
// quicClient.sendTask) edits files in a repo ON the remote machine and is a
// different feature. The "remote" backend below is distinct: it round-trips the
// PHONE SANDBOX's own files to a connected box, runs the GLM runner there, and
// applies the resulting EditPlan back to the local sandbox tree — so the engine
// runs remotely but the edited tree stays the phone's.

/** The concrete coding engines that produce an EditPlan for the local sandbox.
 *  "subscription" = Claude on the user's Max/Pro PLAN via the mirrored OAuth
 *  token (claudeSubscription.ts). It is NOT a BYO API key: it draws from the
 *  flat-rate subscription at zero marginal cost, which is why iOS prefers it
 *  over the metered "anthropic" key.
 *  "remote" = ship the sandbox to a connected box and let its GLM runner edit
 *  it (the box holds the z.ai credential, not the phone). */
export type CodingBackendId = "local" | "subscription" | "anthropic" | "openai" | "glm" | "remote";

/** What the user has chosen. "auto" = let resolveAutoBackend pick per availability. */
export type CodingBackendPref = "auto" | CodingBackendId;

export type CodingBackendKind = "on-device" | "cloud";

export interface CodingBackendMeta {
  id: CodingBackendId;
  label: string;
  kind: CodingBackendKind;
  /** One-line note for the picker. */
  note: string;
  /** For cloud fallback backends: which local provider credential slot it needs (none for local). */
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
    id: "subscription",
    label: "Claude (your plan) — real CLI only",
    kind: "cloud",
    note: "Your Max/Pro plan token. Allowed ONLY via the genuine Claude Code CLI (Android proot or a paired box), never the in-app agent — using a plan token from a re-implemented client breaks Anthropic's terms. For standalone in-app coding, use a configured fallback backend.",
  },
  {
    id: "anthropic",
    label: "Claude API (metered fallback)",
    kind: "cloud",
    note: "Anthropic Claude via a local provider credential. Metered — separate from any Claude plan.",
    requiresKey: "anthropic",
  },
  {
    id: "openai",
    label: "OpenAI API (metered fallback)",
    kind: "cloud",
    note: "OpenAI GPT via a local provider credential. Separate from ChatGPT Plus/Pro plan OAuth.",
    requiresKey: "openai",
  },
  {
    id: "glm",
    label: "GLM API (metered fallback)",
    kind: "cloud",
    note: "Zhipu GLM via a local provider credential.",
    requiresKey: "glm",
  },
  {
    id: "remote",
    label: "Remote runner (GLM)",
    kind: "cloud",
    note: "Runs the GLM coding agent on a connected box and applies its edits back to this sandbox. The box holds the key — your phone doesn't. Requires a connected device.",
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
  /** A Claude subscription OAuth token (mirrored from desktop) is present. */
  claudeSubscription: boolean;
  /** Local provider credentials present in the keychain. */
  anthropicKey: boolean;
  openaiKey: boolean;
  glmKey: boolean;
  /** A box is connected that can run the remote GLM runner. */
  remoteRunner: boolean;
}

// COMPLIANCE — the subscription token is CLI-ONLY.
// These backends power the IN-APP Hermes agent loop (they make LLM calls from
// inside the app and produce an EditPlan). Using a Claude Max/Pro (or ChatGPT
// Plus) SUBSCRIPTION token from a re-implemented client is against Anthropic's /
// OpenAI's consumer terms and is detectable — it must NEVER drive the in-app
// loop. The mirrored plan token is legitimate ONLY for the GENUINE CLI: the
// Android proot `claude`/`codex`, or the real CLI on a paired box (Codex even
// supports official "Sign in with ChatGPT" there). Those paths don't go through
// codingBackend at all. So `subscription` is permanently NOT usable here; the
// no-real-CLI case (iOS always, Android without proot) falls to configured
// fallback backends instead. See docs/phone-dev-environment-audit.md.
export function backendUsable(id: CodingBackendId, av: CodingBackendAvailability): boolean {
  switch (id) {
    case "local":
      return av.localModelReady;
    case "subscription":
      // Hard policy gate: the in-app loop can't use the subscription mimic.
      return false;
    case "anthropic":
      return av.anthropicKey;
    case "openai":
      return av.openaiKey;
    case "glm":
      return av.glmKey;
    case "remote":
      return av.remoteRunner;
  }
}

/** Every backend the user could pick right now (usable === true). */
export function usableBackends(av: CodingBackendAvailability): CodingBackendId[] {
  return CODING_BACKENDS.filter((b) => backendUsable(b.id, av)).map((b) => b.id);
}

/**
 * Auto-pick a backend for the IN-APP loop. NO subscription here (see the
 * compliance note on backendUsable). Policy:
 *   1. On-device model if it's ready — private, free, offline, fully compliant.
 *   2. GLM — the cheap, compliant BYO cloud default (the "$0-ish standalone"
 *      path; z.ai coding endpoint). Preferred cloud choice.
 *   3. Other BYO metered keys the user configured: Anthropic → OpenAI.
 *   4. null when nothing is configured (UI prompts the user to add a key, or to
 *      pair a box / enable Android proot to use the real CLI on their plan).
 * "remote" is intentionally NEVER auto-picked — routing a sandbox edit to a box
 * (uses the user's machine + network) must be an explicit choice, not a silent
 * fallback. The user selects it deliberately in the backend chooser.
 */
export function resolveAutoBackend(av: CodingBackendAvailability): CodingBackendId | null {
  if (av.localModelReady) return "local";
  if (av.glmKey) return "glm";
  if (av.anthropicKey) return "anthropic";
  if (av.openaiKey) return "openai";
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
