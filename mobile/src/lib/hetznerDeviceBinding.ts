import type { Device } from "../context/DeviceContext";
import type { ClientCloudManagedResource } from "./clientCloudProvider";

function normalized(value: unknown): string {
  return String(value || "").trim().toLowerCase().replace(/^\[|\]$/g, "");
}

/**
 * Match a Yaver device to the single Hetzner server explicitly pinned in this
 * endpoint's secure storage. IP is authoritative; the user-owned Yaver alias
 * is the safe fallback because a daemon's generated device name is commonly a
 * hardware id rather than the provider hostname.
 */
export function isBoundHetznerDevice(
  device: Pick<Device, "name" | "alias" | "host" | "lanIps">,
  managed: ClientCloudManagedResource,
): boolean {
  const providerIp = normalized(managed.ip);
  const addresses = [device.host, ...(device.lanIps || [])].map(normalized).filter(Boolean);
  if (providerIp && addresses.includes(providerIp)) return true;

  const providerName = normalized(managed.name);
  return Boolean(providerName && [device.alias, device.name].map(normalized).includes(providerName));
}
