// localAgent/brain.ts — which "brain" answers a troubleshooting / command
// request, in priority order. PURE + RN-free (unit-tested under tsx).
//
// Locked policy (kivanc, 2026-06-01):
//   1. REMOTE-FIRST. If the app is connected to a remote dev box (or any
//      reachable device with a runner), use THAT machine's full LLM for
//      troubleshooting + command-taking. Never burn the on-device model when
//      a real remote brain is available — the remote one is bigger/smarter
//      and already has the project context.
//   2. LOCAL fallback, tiered by phone power. Only when NO remote is
//      reachable, use the on-device model. A beefier phone (coder tier) is
//      allowed to handle general troubleshooting too, not just routing.
//   3. SCRIPTED fallback. If the device can't run any model and nothing is
//      reachable, fall back to keyword shortcuts + scripted guidance so the
//      helper is never fully dead (esp. during first-run onboarding).
//
// Important nuance: even in REMOTE mode, the local model is still useful as a
// pre-parser for the keyword fast-paths and as the connectivity/onboarding
// guide WHEN the remote is unreachable. Brain selection is per-request, so a
// dropped connection transparently demotes remote → local.

import type { ModelTier } from "./tiers";

export type Brain =
  | { kind: "remote"; deviceId: string; reason: string }
  | { kind: "local"; tier: Exclude<ModelTier, "none">; reason: string }
  | { kind: "scripted"; reason: string };

export interface ConnectivitySnapshot {
  /** A remote device the app currently has a live session/transport to. */
  connectedDeviceId?: string | null;
  /** That device actually has a usable coding runner / agent LLM. */
  connectedRunnerReady?: boolean;
  /** Any other device that is online + reachable even if not the active one. */
  reachableDeviceIds?: string[];
  /** Highest local model tier this phone can run right now (from tiers.ts). */
  localTier: ModelTier;
  /** Network present at all (offline → can't reach remote). */
  online?: boolean;
}

/**
 * Pick the brain for a troubleshooting / command request. Remote-first,
 * then local by tier, then scripted.
 */
export function selectBrain(s: ConnectivitySnapshot): Brain {
  // 1. Remote-first: an actively-connected box with a ready runner wins.
  if (s.online !== false && s.connectedDeviceId && s.connectedRunnerReady) {
    return {
      kind: "remote",
      deviceId: s.connectedDeviceId,
      reason: "connected to a remote dev box with a ready runner",
    };
  }

  // 1b. Remote-first (weaker): a reachable box we can attach to on demand.
  // We still prefer this over local because it's the full project brain.
  if (s.online !== false && (s.reachableDeviceIds?.length ?? 0) > 0) {
    return {
      kind: "remote",
      deviceId: s.reachableDeviceIds![0],
      reason: "a remote device is reachable; attach and use its runner",
    };
  }

  // 2. Local fallback, tiered.
  if (s.localTier === "coder" || s.localTier === "router") {
    return {
      kind: "local",
      tier: s.localTier,
      reason:
        s.localTier === "coder"
          ? "no remote reachable; phone can run the larger local model for general troubleshooting"
          : "no remote reachable; using the on-device voice/router model",
    };
  }

  // 3. Scripted.
  return {
    kind: "scripted",
    reason: "no remote reachable and no local model available; keyword + scripted guidance",
  };
}

/**
 * Should the local model be used for GENERAL troubleshooting (not just
 * device-command routing)? Only when there's no remote AND the phone is
 * beefy enough (coder tier). Router-tier phones still do routing + scripted
 * troubleshooting, but defer rich troubleshooting to a remote brain.
 */
export function localHandlesGeneralTroubleshooting(s: ConnectivitySnapshot): boolean {
  const b = selectBrain(s);
  return b.kind === "local" && b.tier === "coder";
}
