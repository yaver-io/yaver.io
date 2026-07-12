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

/** True when a device row is a Yaver-managed box we can pause/resume. */
export function isManagedCloudDevice(d: {
  managed?: boolean;
  hosting?: string;
  machineId?: string;
}): boolean {
  return Boolean(d.machineId) && (d.managed === true || d.hosting === "yaver-hosted");
}

/** True when the box is currently parked (paused) rather than running. */
export function isMachinePaused(machineStatus?: string): boolean {
  const s = String(machineStatus ?? "").toLowerCase();
  return s === "paused" || s === "stopped" || s === "suspended" || s === "off";
}
