import type { MachineProfile, ProviderCapabilities, ProviderId, RequiredCapability } from "./cloudProviders/types";

export type ProviderCreditState = {
  provider: ProviderId;
  creditUsdRemaining?: number;
  creditExpiresAt?: number;
  monthlyBudgetUsd?: number;
  monthToDateSpendUsd?: number;
  hardStopAtUsd?: number;
  supportsCompute: boolean;
  supportsInference: boolean;
  lastSyncedAt: number;
};

export type PlacementCandidate = {
  provider: ProviderId;
  profile: MachineProfile;
  region: string;
  sku: string;
  estimatedMonthlyUsd: number;
  expectedWakeMs?: number;
  historicalFailureRate?: number;
  capabilities: RequiredCapability[];
  productionEligible: boolean;
  kind: "builder" | "serverless" | "inference";
};

export type PlacementDecision = {
  ok: true;
  candidate: PlacementCandidate;
  score: number;
  reasons: string[];
} | {
  ok: false;
  code: "no_capable_provider" | "budget_blocked";
  reasons: string[];
};

export type PlacementRequirement = {
  profile: MachineProfile;
  requiredCapabilities: RequiredCapability[];
  kind: "builder" | "serverless" | "inference";
  intent: "trial" | "paid" | "owner-dev";
  now: number;
};

export function requiredCapabilitiesForProfile(profile: MachineProfile): RequiredCapability[] {
  const base: RequiredCapability[] = [
    "cloud-init",
    "docker",
    "systemd",
    "durable-volume",
    "image-boot",
    "delete-stops-compute-spend",
    "provider-status",
    "tagged-cleanup",
    "outbound-relay",
    "stable-endpoint",
  ];
  if (profile === "linux-runner") return base;
  if (profile === "linux-runner-webrtc") return [...base, "udp-ingress", "webrtc-probe"];
  if (profile === "linux-runner-redroid") return [...base, "redroid-probe"];
  if (profile === "linux-runner-gpu") return base;
  if (profile === "yaver-serverless-host") {
    return [
      ...base,
      "serverless-runtime",
      "custom-domain-tls",
    ];
  }
  if (profile === "inference-only") return ["first-party-inference", "budget-telemetry"];
  return base;
}

export function providerSupportsProfile(
  provider: ProviderCapabilities,
  profile: MachineProfile,
  requiredCapabilities = requiredCapabilitiesForProfile(profile),
): boolean {
  if (!provider.productionEligible) return false;
  if (!provider.profiles.includes(profile)) return false;
  return requiredCapabilities.every((cap) => provider.capabilities.includes(cap));
}

export function candidateSupportsRequirement(
  candidate: PlacementCandidate,
  requirement: PlacementRequirement,
): boolean {
  if (!candidate.productionEligible) return false;
  if (candidate.profile !== requirement.profile) return false;
  if (candidate.kind !== requirement.kind) return false;
  return requirement.requiredCapabilities.every((cap) => candidate.capabilities.includes(cap));
}

export function creditScore(
  candidate: PlacementCandidate,
  credit: ProviderCreditState | undefined,
  now: number,
): number {
  if (!credit) return 0;
  if (credit.hardStopAtUsd !== undefined && (credit.monthToDateSpendUsd || 0) >= credit.hardStopAtUsd) {
    return Number.NEGATIVE_INFINITY;
  }
  const remaining = credit.creditUsdRemaining || 0;
  if (remaining <= 0) return 0;
  const daysToExpiry = credit.creditExpiresAt
    ? Math.max(1, (credit.creditExpiresAt - now) / 86_400_000)
    : 90;
  const expiryPressure = Math.min(3, 90 / daysToExpiry);
  const coverage = candidate.estimatedMonthlyUsd > 0
    ? Math.min(1, remaining / candidate.estimatedMonthlyUsd)
    : 1;
  return 25 * coverage * expiryPressure;
}

export function selectPlacementCandidate(
  requirement: PlacementRequirement,
  candidates: PlacementCandidate[],
  credits: ProviderCreditState[],
): PlacementDecision {
  const capable = candidates.filter((candidate) => candidateSupportsRequirement(candidate, requirement));
  if (capable.length === 0) {
    return {
      ok: false,
      code: "no_capable_provider",
      reasons: ["no provider candidate satisfied the required capability profile"],
    };
  }

  const creditByProvider = new Map(credits.map((credit) => [credit.provider, credit]));
  const scored = capable
    .map((candidate) => {
      const providerCredit = creditByProvider.get(candidate.provider);
      const cScore = creditScore(candidate, providerCredit, requirement.now);
      const paidCostPenalty = requirement.intent === "paid" ? candidate.estimatedMonthlyUsd : candidate.estimatedMonthlyUsd * 0.35;
      const wakePenalty = Math.min(20, (candidate.expectedWakeMs || 180_000) / 30_000);
      const failurePenalty = Math.min(30, (candidate.historicalFailureRate || 0) * 100);
      const hetznerPaidBaseline = requirement.intent === "paid" && candidate.provider === "hetzner" ? 12 : 0;
      return {
        candidate,
        score: cScore + hetznerPaidBaseline - paidCostPenalty - wakePenalty - failurePenalty,
        creditBlocked: cScore === Number.NEGATIVE_INFINITY,
      };
    })
    .filter((entry) => !entry.creditBlocked);

  if (scored.length === 0) {
    return {
      ok: false,
      code: "budget_blocked",
      reasons: ["all capable providers are over their hard budget stop"],
    };
  }

  scored.sort((a, b) => b.score - a.score);
  const best = scored[0];
  const credit = creditByProvider.get(best.candidate.provider);
  const reasons = [
    `selected ${best.candidate.provider}/${best.candidate.region}/${best.candidate.sku}`,
    `profile ${best.candidate.profile}`,
  ];
  if ((credit?.creditUsdRemaining || 0) > 0) {
    reasons.push(`credit-aware: ${best.candidate.provider} has remaining credits`);
  }
  if (requirement.intent === "paid" && best.candidate.provider === "hetzner") {
    reasons.push("paid baseline: Hetzner preferred when capability fits and credits do not dominate");
  }
  return { ok: true, candidate: best.candidate, score: best.score, reasons };
}
