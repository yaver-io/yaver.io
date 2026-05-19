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

export interface ManagedSubscriptionSummary {
  // Owner-allowlist flag (server isCloudPreviewUser). Mobile hides
  // the managed-cloud card entirely for non-owners — cosmetic; the
  // server independently 403s every action.
  cloudPreviewOwner?: boolean;
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

export function startManagedCloudMachine(token: string, machineId: string) {
  return managedCloudPost<{ ok: boolean; machineId?: string }>(
    token,
    "/billing/yaver-cloud/start",
    { machineId },
  );
}

export function devTopUpManagedCloud(token: string, amountCents = 1000) {
  return managedCloudPost<{ ok: boolean; balanceCents?: number }>(
    token,
    "/billing/yaver-cloud/topup-dev",
    { amountCents },
  );
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
