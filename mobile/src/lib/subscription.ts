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

export interface CreditPack {
  id: string;
  cents: number;
  label: string;
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

export async function getCreditPacks(token: string): Promise<CreditPack[]> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/billing/credits/packs`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return [];
    const data = (await res.json()) as { packs?: CreditPack[] };
    return Array.isArray(data.packs) ? data.packs : [];
  } catch {
    return [];
  }
}

// Create a web checkout for a prepaid credit pack and return its URL.
// The app NEVER charges in-app (no Apple/Google IAP) — it opens this
// URL in the system browser, OpenAI-style. On payment the order_created
// webhook credits the wallet server-side.
export function createCreditPackCheckout(token: string, packId: string) {
  return managedCloudPost<{ url: string; packId: string; cents: number; mode: string }>(
    token,
    "/billing/credits/checkout",
    { packId },
  );
}

// Prepaid spin-up: provision a new managed box funded by the wallet
// (no subscription). 402 if the balance can't cover the SKU reserve.
export function provisionManagedCloud(
  token: string,
  opts: { machineType?: "cpu" | "gpu"; region?: "eu" | "us" } = {},
) {
  return managedCloudPost<{ ok: boolean; machineId?: string; machineType?: string; region?: string }>(
    token,
    "/billing/yaver-cloud/provision",
    { machineType: opts.machineType ?? "cpu", region: opts.region ?? "eu" },
  );
}
