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

// Beta entitlement (invisible owner-infra share). When isBeta, the
// dashboard renders the Beta workspace view (project + vibe box) + a
// "Beta" badge, and hides the infra/wallet/device panels — the beta user
// never sees the shared device/guest/owner details (those stay hidden
// server-side via listVisibleInfraGrantsForGuest).
export interface BetaStatus {
  isBeta: boolean;
  plan: string | null;          // "beta" | null
  sharedProject: string | null; // "sfmg" | "carrotbet" | null
  includedHours: number;
  usedHours: number;
  aiEnabled: boolean;
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
  beta?: BetaStatus | null;
}

// Single source of truth for "render the beta surface". Cosmetic only —
// the actual access is enforced server-side (gateway caps + hidden grant).
export function isBetaUser(s: ManagedSubscriptionSummary | null | undefined): boolean {
  return s?.beta?.isBeta === true;
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
