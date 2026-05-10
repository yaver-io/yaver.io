// connectionState.ts — single source of truth for "is the user
// effectively connected?" across every tab.
//
// Why this exists: the focused-device `connectionStatus` from
// DeviceContext only reflects ONE QuicClient — the one the user has
// actively selected. The connection manager keeps a pool of secondary
// clients warm (background-attached devices, the user's other boxes
// signed in to the same account). When the focused box is mid-retry
// but a pool client is live, three different tabs were each making up
// their own answer:
//
//   Devices  → reads `connectedDeviceIds` (pool truth) — shows "2 Connected"
//   Tasks    → promotes to "connected" via locally-computed anyPoolConnected
//   Reload   → focused-only — shows "Not connected" full screen
//   Projects → focused-only — same misleading "Not connected" view
//
// Lifting the same derivation here so every consumer reads the same
// truth fixes the screenshot inconsistency the user complained about.

import type { ConnectionState } from "./quic";

export type EffectiveConnectionState = "connected" | "connecting" | "disconnected" | "error";

/** Compute the user-visible connection state from the focused-device
 *  status + the live pool. Pool fallback wins over a transient focused
 *  retry — because if SOME box is live, the app is functionally
 *  connected (mobile can route tasks to a peer, the user has at least
 *  one healthy primary). The "error" state collapses to "connecting"
 *  so we don't surface raw transport errors as "the product is broken"
 *  while a single retry would fix it. */
export function deriveEffectiveConnectionState(
  connectionStatus: ConnectionState,
  connectedDeviceIds: readonly string[],
): EffectiveConnectionState {
  if (connectionStatus === "connected") return "connected";
  if (connectedDeviceIds.length > 0) return "connected";
  if (connectionStatus === "error") return "connecting";
  return connectionStatus;
}

/** True when at least one pool client (focused or background) is
 *  currently reachable. Cheap shorthand for tabs that just need a
 *  yes/no rather than the full state machine. */
export function isEffectivelyConnected(
  connectionStatus: ConnectionState,
  connectedDeviceIds: readonly string[],
): boolean {
  return deriveEffectiveConnectionState(connectionStatus, connectedDeviceIds) === "connected";
}
