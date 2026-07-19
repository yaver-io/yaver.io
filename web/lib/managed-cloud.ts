/**
 * Managed-cloud machine lifecycle from the web dashboard.
 *
 * Mirrors mobile/src/lib/subscription.ts — the same Convex HTTP routes the
 * mobile Devices tab uses, so a Yaver-hosted box can be Paused (server deleted
 * while state stays durable) or Resumed (server recreated from its recovery
 * source) from the web UI too, not just the phone.
 *
 * Pause is the scale-to-zero path: Hetzner bills a *stopped* server, so the
 * backend DELETES the server. New workspaces keep state on a persistent volume
 * and wake from a slim base image; legacy rows may still use full snapshots.
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

/** Pause a Yaver-hosted box: preserve state, then delete the server so billing stops. */
export function stopManagedCloudMachine(token: string, machineId: string) {
  return managedCloudPost<{ ok: boolean; machineId?: string; wakeRunId?: string | null }>(
    token,
    "/billing/yaver-cloud/stop",
    { machineId },
  );
}

/** Resume a paused box from its recorded recovery source. */
export function startManagedCloudMachine(token: string, machineId: string) {
  return managedCloudPost<{ ok: boolean; machineId?: string; wakeRunId?: string | null }>(
    token,
    "/billing/yaver-cloud/start",
    { machineId },
  );
}

/**
 * Auto-close (auto-park): the box parks itself when idle so it stops billing.
 * ON by default and required for customer-facing Cloud Workspace traffic; the
 * product API accepts enable/tune requests but rejects disabling cost protection.
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
 * no recovery source cannot come back, and only the backend knows that. For
 * anything that offers a wake action, read `device.machineWakeable` (the
 * backend's own isMachineWakeable verdict) instead of calling this.
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
    return { label: "Waking…", tone: "busy", hint: "Starting the workspace from its saved state." };
  }
  if (s === "stopping" || s === "pausing" || s === "snapshotting") {
    return { label: "Parking…", tone: "busy", hint: "Preserving state, then deleting compute so billing stops." };
  }
  if (wakeable) {
    return {
      label: "Asleep",
      tone: "asleep",
      hint: "Parked to stop billing. Wake recreates compute from saved state.",
    };
  }
  if (s === "removed" || s === "deleted") {
    return {
      label: "Gone",
      tone: "gone",
      hint: "This box was deleted and has no recovery source — it cannot be woken. Provision a new one.",
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
