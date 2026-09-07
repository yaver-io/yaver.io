// A Task is owned by the agent that created it. Focus and account-level
// runner/render roles are mutable UI preferences; neither is a valid routing
// key once a task exists. Cross-device lifecycle rows from Convex deliberately
// contain deviceId, so the phone can locate the owner without storing prompts
// or output in Convex.

export interface TaskOwnerRef {
  deviceId?: string;
  deviceName?: string;
}

export interface TaskOwnerDevice {
  id: string;
  name?: string;
}

function normalizedDeviceName(value: string | undefined): string {
  return String(value || "").trim().replace(/\.local$/i, "").toLowerCase();
}

/** Resolve the immutable owner of an existing task.
 *
 * deviceId always wins. The name fallback only supports older cached task rows
 * that predate deviceId stamping; it must never override an explicit id merely
 * because that device is temporarily absent from the latest Convex list.
 */
export function taskOwnerDeviceId(
  task: TaskOwnerRef | null | undefined,
  devices: readonly TaskOwnerDevice[],
): string | null {
  const explicit = String(task?.deviceId || "").trim();
  if (explicit) return explicit;

  const wantedName = normalizedDeviceName(task?.deviceName);
  if (!wantedName) return null;
  return devices.find((device) => normalizedDeviceName(device.name) === wantedName)?.id || null;
}

/** Whether opening this task must first establish its owner connection. */
export function taskOwnerNeedsConnection(
  ownerDeviceId: string | null,
  connectedDeviceIds: readonly string[],
): boolean {
  return !!ownerDeviceId && !connectedDeviceIds.includes(ownerDeviceId);
}
