import { CONVEX_URL } from "@/lib/constants";

export interface ManagedCloudMachineSummary {
  id: string;
  machineType: string;
  status: string;
  hostname?: string;
  serverIp?: string;
  region?: string;
  errorMessage?: string;
  subscriptionId?: string | null;
}

export interface ManagedSubscriptionSummary {
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
}

export async function getManagedSubscription(token: string): Promise<ManagedSubscriptionSummary | null> {
  try {
    const res = await fetch(`${CONVEX_URL}/subscription`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return null;
    return (await res.json()) as ManagedSubscriptionSummary;
  } catch {
    return null;
  }
}
