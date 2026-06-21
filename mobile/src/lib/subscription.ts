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
  prepaidBalanceCents?: number | null;
  estimatedHourlyCents?: number | null;
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

// Beta soft-launch status (mirrors web/lib/subscription.ts). When isBeta, the
// user gets managed inference (owner's GLM via the gateway) + the owner's box —
// no key entry. Cosmetic flag; access is enforced server-side (gateway caps +
// hidden infra grant). See project_beta_invisible_infra_share.
export interface BetaStatus {
  isBeta: boolean;
  plan: string | null;
  sharedProject: string | null;
  includedHours: number;
  usedHours: number;
  aiEnabled: boolean;
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
  beta?: BetaStatus | null;
}

/** Render the beta surface? Cosmetic only — access is enforced server-side. */
export function isBetaUser(s: ManagedSubscriptionSummary | null | undefined): boolean {
  return s?.beta?.isBeta === true;
}

/** Exchange a beta user's session for a scoped managed-inference token + gateway
 * URL (keyless GLM). Returns null for non-beta / errors. The raw token is only
 * returned here; store it locally for the sandbox generation's managed lane. */
export async function fetchBetaInferenceToken(
  token: string,
): Promise<{ token: string; gatewayUrl: string } | null> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/beta/inference-token`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return null;
    const data = (await res.json()) as { token?: string; gatewayUrl?: string };
    if (!data.token || !data.gatewayUrl) return null;
    return { token: data.token, gatewayUrl: data.gatewayUrl };
  } catch {
    return null;
  }
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
