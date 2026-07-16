/**
 * Managed-cloud machine lifecycle from the web dashboard.
 *
 * Mirrors mobile/src/lib/subscription.ts — the same two Convex HTTP routes the
 * mobile Devices tab already uses, so a Yaver-hosted box can be Paused
 * (snapshot + delete → meter stops) or Resumed (recreate from snapshot) from
 * the web UI too, not just the phone.
 *
 * Pause is the scale-to-zero path: Hetzner bills a *stopped* server, so the
 * backend snapshots then DELETES the server. Resume recreates it from that
 * snapshot (~2-3 min).
 */
import { CONVEX_URL } from "./constants";

async function managedCloudPost<T>(
  token: string,
  path: string,
  body: Record<string, unknown>,
): Promise<T> {
  const res = await fetch(`${CONVEX_URL}${path}`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error((data as { error?: string })?.error || `HTTP ${res.status}`);
  }
  return data as T;
}

/** Pause a Yaver-hosted box: snapshot then delete the server (stops billing). */
export function stopManagedCloudMachine(token: string, machineId: string) {
  return managedCloudPost<{ ok: boolean; machineId?: string }>(
    token,
    "/billing/yaver-cloud/stop",
    { machineId },
  );
}

/** Resume a paused box: recreate it from its latest snapshot. */
export function startManagedCloudMachine(token: string, machineId: string) {
  return managedCloudPost<{ ok: boolean; machineId?: string }>(
    token,
    "/billing/yaver-cloud/start",
    { machineId },
  );
}

/**
 * Auto-close (auto-park): the box parks itself when idle so it stops billing.
 * ON by default — turning it OFF means it keeps running (and charging) until
 * you pause it by hand.
 */
export function setManagedCloudAutoPark(
  token: string,
  machineId: string,
  enabled: boolean,
  idleMinutes?: number,
) {
  return managedCloudPost<{ ok: boolean; autoParkEnabled?: boolean; autoParkMinutes?: number }>(
    token,
    "/billing/yaver-cloud/auto-park",
    { machineId, enabled, ...(idleMinutes ? { idleMinutes } : {}) },
  );
}

/** True when a device row is a Yaver-managed box we can pause/resume. */
export function isManagedCloudDevice(d: {
  managed?: boolean;
  hosting?: string;
  machineId?: string;
}): boolean {
  return Boolean(d.machineId) && (d.managed === true || d.hosting === "yaver-hosted");
}

/**
 * True when the box is currently parked (paused) rather than running.
 *
 * NOTE: this answers "is it parked", NOT "can it be woken" — a parked box with
 * no snapshot cannot come back, and only the backend knows that. For anything
 * that offers a wake action, read `device.machineWakeable` (the backend's own
 * isMachineWakeable verdict) instead of calling this.
 */
export function isMachinePaused(machineStatus?: string): boolean {
  const s = String(machineStatus ?? "").toLowerCase();
  return s === "paused" || s === "stopped" || s === "suspended" || s === "off";
}

/** True when the box is up and billing — the only state where Pause is meaningful. */
export function isMachineRunning(machineStatus?: string): boolean {
  return String(machineStatus ?? "").toLowerCase() === "active";
}

/**
 * describeMachineState turns a raw cloudMachines.status into what an operator
 * needs to know: is this box asleep (and wakeable), busy changing state, or gone?
 *
 * The dashboard previously showed every non-running managed box as plain
 * "Offline" with a ⏸ Pause button — so a parked box you could wake in two
 * minutes looked identical to one that had been deleted.
 */
export function describeMachineState(
  machineStatus: string | undefined,
  wakeable: boolean,
): { label: string; tone: "asleep" | "busy" | "gone" | "running"; hint: string } {
  const s = String(machineStatus ?? "").toLowerCase();
  if (s === "active") {
    return { label: "Running", tone: "running", hint: "The box is up and billing." };
  }
  if (s === "resuming" || s === "starting") {
    return { label: "Waking…", tone: "busy", hint: "Recreating from snapshot — usually 2-3 min." };
  }
  if (s === "stopping" || s === "pausing" || s === "snapshotting") {
    return { label: "Parking…", tone: "busy", hint: "Snapshotting, then deleting the server so billing stops." };
  }
  if (wakeable) {
    return {
      label: "Asleep",
      tone: "asleep",
      hint: "Parked to stop billing. Wake recreates it from its snapshot (~2-3 min).",
    };
  }
  if (s === "removed" || s === "deleted") {
    return {
      label: "Gone",
      tone: "gone",
      hint: "This box was deleted and has no snapshot to restore — it cannot be woken. Provision a new one.",
    };
  }
  if (s === "error") {
    return { label: "Error", tone: "gone", hint: "The box failed and cannot be woken. Check the machine log." };
  }
  return {
    label: s ? s[0].toUpperCase() + s.slice(1) : "Unknown",
    tone: "gone",
    hint: "No wake action is available from this state.",
  };
}
