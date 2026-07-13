import { getConvexSiteUrl } from "./auth";

export interface ManagedCloudMachineSummary {
  id: string;
  machineType: string;
  status: string;
  hostname?: string;
  serverIp?: string;
  region?: string;
  errorMessage?: string;
  subscriptionId?: string | null;
  hetznerServerId?: string;
  // First-class onboarding parity with web
  // (project_managed_cloud_onboarding_gap).
  provisionPhase?: string | null;
  provisionProgress?: number | null;
  /** "golden" = booted from a prebuilt snapshot (fast); "vanilla" =
   *  ubuntu-24.04 first-boot build (~3-5 min). */
  bootImageSource?: string | null;
  runnersAuthorized?: boolean;
  stoppedAt?: number | null;
  /** True when a persistent volume holds the box's data, so wake skips the fat
   *  disk restore (~1-2 min instead of ~10). Surfaced by /subscription. */
  hasVolume?: boolean;
  /** Concrete provider server type the box was created on (e.g. "cx43"). */
  serverType?: string | null;
  /** Hardware summary — surfaced on the Parked card ("8 vCPU · 16 GB · 160 GB").
   *  Populated once the /subscription machine mapping carries it. */
  specs?: {
    vcpu?: number;
    ramGb?: number;
    diskGb?: number;
    arch?: string;
    gpu?: string | null;
  } | null;
  /** When the box last transitioned to a parked state — "slept 3h ago". */
  lastParkedAt?: number | null;
  /** When the last wake was requested — "woke 2m ago" once active again. */
  lastWokeAt?: number | null;
  prepaidBalanceCents?: number | null;
  estimatedHourlyCents?: number | null;
  /** Auto-park (auto-close) when idle. Undefined === ON (the default), so a
   *  forgotten box always stops its own meter. Only an explicit false keeps it
   *  running while idle. */
  autoParkEnabled?: boolean | null;
  autoParkMinutes?: number | null;
}

export interface ManagedCloudBalanceSummary {
  ok?: boolean;
  balanceCents?: number;
  prepaidBalanceCents?: number;
  currency?: string;
  estimatedHourlyCents?: number;
  lowBalance?: boolean;
  reservedCents?: number;
}

export interface ManagedCloudUsageEntry {
  machineId?: string | null;
  date: string;
  state: string;
  seconds: number;
  chargedCents: number;
  ratePerHourCents: number;
  dryRun: boolean;
  createdAt: number;
}

export interface ManagedCloudTopupEntry {
  orderId: string;
  source: string;
  packId?: string | null;
  amountCents: number;
  createdAt: number;
}

export interface ManagedCloudUsageSummary {
  ok?: boolean;
  usage: ManagedCloudUsageEntry[];
  topups: ManagedCloudTopupEntry[];
}

export interface ManagedSubscriptionSummary {
  // Owner-allowlist flag (server isCloudPreviewUser). Mobile hides
  // the managed-cloud card entirely for non-owners — cosmetic; the
  // server independently 403s every action.
  cloudPreviewOwner?: boolean;
  // True when this account may use the prepaid-cloud surfaces (owner
  // allowlist OR the YAVER_CLOUD_PUBLIC launch flag). Mobile shows the
  // wallet + controls when EITHER this or cloudPreviewOwner is true.
  cloudAccess?: boolean;
  subscription: {
    plan: string;
    status: string;
    currentPeriodEnd?: number;
    cancelledAt?: number;
  } | null;
  relay: {
    status: string;
    domain?: string;
    region?: string;
    quicPort?: number;
    httpPort?: number;
  } | null;
  machines: ManagedCloudMachineSummary[];
  prepaidBalanceCents?: number | null;
  currency?: string;
  balance?: ManagedCloudBalanceSummary | null;
}

export async function getManagedSubscription(token: string): Promise<ManagedSubscriptionSummary | null> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/subscription`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return null;
    return (await res.json()) as ManagedSubscriptionSummary;
  } catch {
    return null;
  }
}

async function managedCloudPost<T>(
  token: string,
  path: string,
  body: Record<string, unknown>,
): Promise<T> {
  const res = await fetch(`${getConvexSiteUrl()}${path}`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data?.error || `HTTP ${res.status}`);
  }
  return data as T;
}

export function stopManagedCloudMachine(token: string, machineId: string) {
  return managedCloudPost<{ ok: boolean; machineId?: string }>(
    token,
    "/billing/yaver-cloud/stop",
    { machineId },
  );
}

/**
 * Auto-park (auto-close): the box parks itself when idle so it stops billing.
 * ON by default — turning it OFF means the box keeps running (and charging)
 * until you park it by hand.
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

export function startManagedCloudMachine(token: string, machineId: string) {
  return managedCloudPost<{ ok: boolean; machineId?: string }>(
    token,
    "/billing/yaver-cloud/start",
    { machineId },
  );
}

export interface ByoMachine {
  id: string;
  provider: string;
  serverId: string;
  deviceId?: string | null;
  name: string;
  region?: string | null;
  plan?: string | null;
  serverIp?: string | null;
  imageId?: string | null;
  snapshotImageId?: string | null;
  state: "active" | "stopped" | "deleted";
  createdAt: number;
  lastUpAt?: number | null;
  stoppedAt?: number | null;
  deletedAt?: number | null;
  updatedAt: number;
}

// BYO cloud boxes' lifecycle state from Convex (alive/sleeping/deleted +
// timestamps). Convex holds id/state/timestamps only — never the token.
export async function getByoMachines(token: string): Promise<ByoMachine[]> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/byo/machines`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return [];
    const data = (await res.json()) as { machines?: ByoMachine[] };
    return Array.isArray(data.machines) ? data.machines : [];
  } catch {
    return [];
  }
}

export async function getManagedCloudBalance(token: string): Promise<ManagedCloudBalanceSummary | null> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/billing/yaver-cloud/balance`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return null;
    return (await res.json()) as ManagedCloudBalanceSummary;
  } catch {
    return null;
  }
}

export async function getManagedCloudUsage(token: string): Promise<ManagedCloudUsageSummary | null> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/billing/yaver-cloud/usage`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return null;
    return (await res.json()) as ManagedCloudUsageSummary;
  } catch {
    return null;
  }
}
