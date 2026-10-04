// Mirrors web/lib/launchFlags.ts. Relay Pro is sold on the web; native apps may
// show entitlement/status but must never embed checkout. Hosted Cloud
// Workspace controls remain unavailable for this release.
export const ENABLE_RELAY_PRO_UI = true;
export const ENABLE_CLOUD_WORKSPACE_UI = false;
// Product-wide release gate. Keep the implementation available for later,
// but do not expose or select a phone/TV-local "boxless" runtime in shipped
// clients. Every coding task must name a real user-owned Yaver machine.
export const ENABLE_BOXLESS_UI = false;
export const HIDE_PAID_UI = !ENABLE_RELAY_PRO_UI;

type DeviceRegistryRow = {
  hosting?: unknown;
  cloudWorkspaceId?: unknown;
  deviceKind?: unknown;
  managed?: unknown;
  machineId?: unknown;
};

/**
 * Apple/mobile clients are relay + user-owned-machine surfaces for this
 * release. Suppress legacy hosted-compute rows at the registry boundary so a
 * forgotten card, picker, CarPlay handler, or deep route cannot revive them.
 * BYO and self-hosted VPS rows deliberately remain visible.
 */
export function isHostedCloudSurfaceDevice(row: DeviceRegistryRow): boolean {
  return row.hosting === "yaver-hosted"
    || (typeof row.cloudWorkspaceId === "string" && row.cloudWorkspaceId.trim() !== "")
    || row.deviceKind === "cloud-runner"
    || (row.managed === true && typeof row.machineId === "string" && row.machineId.trim() !== "");
}
