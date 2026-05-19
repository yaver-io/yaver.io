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
