import type { ProviderId } from "./cloudProviders/types";

export type InferenceProviderId = ProviderId | "byo" | "external";

export type InferenceCandidate = {
  provider: InferenceProviderId;
  model: string;
  label?: string;
  contextTokens?: number;
  estimatedUsdPerMillionInputTokens?: number;
  estimatedUsdPerMillionOutputTokens?: number;
  creditUsdRemaining?: number;
  creditExpiresAt?: number;
  latencyMs?: number;
  qualityScore?: number;
  productionEligible: boolean;
};

export type InferenceRequirement = {
  intent: "trial" | "paid" | "owner-dev";
  requestedModel?: string;
  minContextTokens?: number;
  allowManaged: boolean;
  allowByo: boolean;
  now: number;
};

export type InferenceDecision = {
  ok: true;
  candidate: InferenceCandidate;
  score: number;
  reasons: string[];
} | {
  ok: false;
  code: "no_inference_provider";
  reasons: string[];
};

export function selectInferenceCandidate(
  requirement: InferenceRequirement,
  candidates: InferenceCandidate[],
): InferenceDecision {
  const eligible = candidates.filter((candidate) => {
    if (!candidate.productionEligible && candidate.provider !== "byo") return false;
    if (candidate.provider === "byo" && !requirement.allowByo) return false;
    if (candidate.provider !== "byo" && !requirement.allowManaged) return false;
    if (requirement.requestedModel && candidate.model !== requirement.requestedModel) return false;
    if (requirement.minContextTokens && (candidate.contextTokens || 0) < requirement.minContextTokens) return false;
    return true;
  });
  if (eligible.length === 0) {
    return { ok: false, code: "no_inference_provider", reasons: ["no inference candidate satisfied the request"] };
  }
  const scored = eligible.map((candidate) => {
    const creditScore = managedCreditScore(candidate, requirement.now);
    const byoScore = candidate.provider === "byo" ? 20 : 0;
    const qualityScore = candidate.qualityScore || 0;
    const latencyPenalty = Math.min(20, (candidate.latencyMs || 500) / 100);
    const costPenalty =
      ((candidate.estimatedUsdPerMillionInputTokens || 0) + (candidate.estimatedUsdPerMillionOutputTokens || 0)) / 4;
    return {
      candidate,
      score: creditScore + byoScore + qualityScore - latencyPenalty - costPenalty,
    };
  });
  scored.sort((a, b) => b.score - a.score);
  const best = scored[0];
  const reasons = [`selected ${best.candidate.provider}/${best.candidate.model}`];
  if ((best.candidate.creditUsdRemaining || 0) > 0) reasons.push("credit-aware: provider has remaining inference credits");
  if (best.candidate.provider === "byo") reasons.push("BYO inference avoids Yaver model spend");
  return { ok: true, candidate: best.candidate, score: best.score, reasons };
}

function managedCreditScore(candidate: InferenceCandidate, now: number): number {
  const remaining = candidate.creditUsdRemaining || 0;
  if (remaining <= 0) return 0;
  const daysToExpiry = candidate.creditExpiresAt
    ? Math.max(1, (candidate.creditExpiresAt - now) / 86_400_000)
    : 90;
  return Math.min(50, 20 * Math.min(3, 90 / daysToExpiry));
}
