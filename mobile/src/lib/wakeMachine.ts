import { startManagedCloudMachine } from "./subscription";

// wakeMachine — shared helpers for resuming a managed cloud box that
// auto-off'd (self-park after idle). A paused box has snapshot+deleted its
// server, so it reports machineStatus "paused"/"stopped" and has no live
// endpoint — that's why the runner reads DISCONNECTED. Resuming recreates
// the server from the latest snapshot (~1-2 min) and it re-registers over
// the relay with its persisted token (no re-auth).
//
// Used by the shared RemoteBoxBanner (one-tap Wake on every tab) and the
// connection screen. Intentionally NOT auto-fired on passive app-open —
// that would defeat the whole point of auto-off (every glance would spin
// the box back up + bill). Wake is user-intent driven: tapping Wake, or
// acting on a sleeping box.

/** Shape we need off a Device to reason about sleep state. */
export interface WakeableDevice {
  managed?: boolean;
  machineId?: string;
  machineStatus?: string;
}

/**
 * isDeviceAsleep reports whether a device is a managed box that auto-off'd
 * (self-parked) — managed + a non-running lifecycle status. A self-hosted
 * box that's merely offline is NOT "asleep" (we can't wake it), so this is
 * gated on `managed`.
 */
export function isDeviceAsleep(d: WakeableDevice | null | undefined): boolean {
  if (!d?.managed) return false;
  const st = String(d.machineStatus ?? "").toLowerCase();
  return st === "paused" || st === "stopped" || st === "off";
}

export interface WakeResult {
  ok: boolean;
  error?: string;
}

/**
 * wakeManagedDevice asks the control plane to resume a paused managed box
 * from its latest snapshot. Resolves when the resume request is ACCEPTED —
 * the box then boots + re-registers over the relay asynchronously, so
 * callers should refresh the device list afterwards to pick up the new
 * status/IP. Safe to call again while a resume is already in flight.
 */
export async function wakeManagedDevice(
  token: string | null | undefined,
  machineId: string | null | undefined,
): Promise<WakeResult> {
  if (!token) return { ok: false, error: "Not signed in." };
  if (!machineId) return { ok: false, error: "No managed machine to wake." };
  try {
    await startManagedCloudMachine(token, machineId);
    return { ok: true };
  } catch (e: any) {
    return { ok: false, error: e?.message ? String(e.message) : "Wake request failed." };
  }
}
