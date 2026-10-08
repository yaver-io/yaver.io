export const DEFAULT_FREE_OWNED_DEVICE_LIMIT = 2;
const MAX_CONFIGURED_DEVICE_LIMIT = 10_000;

export type RelayPlan = "free" | "relay-pro";

export function freeOwnedDeviceLimit(configured?: string): number {
  if (configured === undefined || !/^\d+$/.test(configured.trim())) {
    return DEFAULT_FREE_OWNED_DEVICE_LIMIT;
  }
  const parsed = Number(configured);
  if (!Number.isSafeInteger(parsed) || parsed > MAX_CONFIGURED_DEVICE_LIMIT) {
    return DEFAULT_FREE_OWNED_DEVICE_LIMIT;
  }
  return parsed;
}

export function ownedDeviceLimit(plan: RelayPlan, configuredFreeLimit?: string): number | null {
  return plan === "free" ? freeOwnedDeviceLimit(configuredFreeLimit) : null;
}

export function deviceEntitlement(plan: RelayPlan, configuredFreeLimit?: string) {
  return {
    plan,
    maxOwnedDevices: ownedDeviceLimit(plan, configuredFreeLimit),
  };
}
