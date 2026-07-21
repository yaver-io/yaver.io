import type { MachineProfile, ProviderId, RequiredCapability } from "./cloudProviders/types";

export type PoolKind =
  | "warm-builder"
  | "parked-builder"
  | "serverless-preview"
  | "serverless-always-on"
  | "inference-budget";

export type PoolKey = {
  provider: ProviderId;
  region: string;
  profile: MachineProfile;
  sku: string;
  kind: PoolKind;
};

export type PoolEntry = {
  key: PoolKey;
  machineId?: string;
  state: "warm" | "leased" | "parking" | "parked" | "waking" | "active" | "draining" | "error";
  capabilities: RequiredCapability[];
  leaseId?: string;
  reservedForUserId?: string;
  expiresAt?: number;
  lastProbeAt?: number;
  lastWakeDurationMs?: number;
  estimatedMonthlyUsd?: number;
};

export type PoolRequirement = {
  profile: MachineProfile;
  kind: PoolKind;
  requiredCapabilities: RequiredCapability[];
  userId?: string;
  now: number;
  maxWakeMs?: number;
  allowWarmSharedPool: boolean;
};

export type PoolDecision = {
  ok: true;
  entry: PoolEntry;
  score: number;
  reasons: string[];
} | {
  ok: false;
  code: "no_pool_entry";
  reasons: string[];
};

export function selectPoolEntry(requirement: PoolRequirement, entries: PoolEntry[]): PoolDecision {
  const candidates = entries.filter((entry) => {
    if (entry.key.profile !== requirement.profile) return false;
    if (entry.key.kind !== requirement.kind) return false;
    if (entry.state === "error" || entry.state === "draining" || entry.state === "leased") return false;
    if (entry.expiresAt !== undefined && entry.expiresAt <= requirement.now) return false;
    if (!requirement.allowWarmSharedPool && entry.key.kind === "warm-builder" && !entry.reservedForUserId) return false;
    if (entry.reservedForUserId && requirement.userId && entry.reservedForUserId !== requirement.userId) return false;
    return requirement.requiredCapabilities.every((cap) => entry.capabilities.includes(cap));
  });
  if (candidates.length === 0) {
    return { ok: false, code: "no_pool_entry", reasons: ["no pool entry satisfied profile/capability/state requirements"] };
  }
  const scored = candidates.map((entry) => {
    const stateScore = entry.state === "warm" || entry.state === "active" ? 40 : entry.state === "parked" ? 18 : 0;
    const wakePenalty = Math.min(30, (entry.lastWakeDurationMs || 180_000) / 20_000);
    const costPenalty = Math.min(20, entry.estimatedMonthlyUsd || 0);
    const dedicatedScore = entry.reservedForUserId === requirement.userId ? 12 : 0;
    return {
      entry,
      score: stateScore + dedicatedScore - wakePenalty - costPenalty,
    };
  });
  scored.sort((a, b) => b.score - a.score);
  const best = scored[0];
  return {
    ok: true,
    entry: best.entry,
    score: best.score,
    reasons: [`selected ${best.entry.key.kind}/${best.entry.key.provider}/${best.entry.key.region}`],
  };
}

export type PlacementLease = {
  leaseId: string;
  userId: string;
  provider: ProviderId;
  kind: "compute-wake" | "compute-hour" | "serverless-host" | "inference-budget";
  estimatedUsd: number;
  expiresAt: number;
  status: "reserved" | "consumed" | "released" | "expired";
};

export function leaseWouldExceedBudget(args: {
  existingLeases: PlacementLease[];
  provider: ProviderId;
  additionalEstimatedUsd: number;
  hardStopUsd: number;
  now: number;
}): boolean {
  const reserved = args.existingLeases
    .filter((lease) => lease.provider === args.provider)
    .filter((lease) => lease.status === "reserved" && lease.expiresAt > args.now)
    .reduce((sum, lease) => sum + lease.estimatedUsd, 0);
  return reserved + args.additionalEstimatedUsd > args.hardStopUsd;
}
