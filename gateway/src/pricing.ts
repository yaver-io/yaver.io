// Yaver Gateway — routing table + upstream token pricing.
//
// SOURCE OF TRUTH for which upstream model serves a request and what it
// costs Yaver (COGS). The gateway computes providerCostCents here; Convex
// (managedMeter.recordManagedUsage) applies the arbitrage markup and
// debits the wallet. Keeping pricing ONLY in the Worker (not Convex)
// avoids drift — Convex never needs to know upstream rates.
//
// ⚠️ RATES ARE PLACEHOLDERS — verify against each provider's live pricing
// before flipping YAVER_MANAGED_METER_LIVE. Wrong rates here = margin or
// loss, silently. Cents per 1,000,000 tokens.

export type Upstream = {
  provider: string;       // managedUsage.provider label (non-secret)
  model: string;          // upstream model id sent on the wire
  baseUrl: string;        // OpenAI-compatible /v1 base
  keyEnv: string;         // Worker secret holding this provider's key
  inCentsPerM: number;    // input token COGS, cents / 1e6 tokens
  outCentsPerM: number;   // output token COGS
};

// PAY-PER-TOKEN FIRST. The "auto" chain is ordered so a metered,
// no-quota-wall endpoint is the PRIMARY and the flat z.ai *coding plan*
// is the LAST-RESORT overflow. This is the P2 staleness fix: a shared
// coding-plan seat throttles (429s) and is a per-seat ToS grey area when
// resold to many paying users — pay-per-token has neither problem.
//
// PROVIDER SELECTION = WHICH KEY YOU CONFIGURE. callUpstream (index.ts)
// skips any upstream whose keyEnv is unset and falls through on 429/5xx,
// so the chain can safely list every provider: set DEEPINFRA_API_KEY (or
// TOGETHER_API_KEY / ZAI_PAYG_API_KEY) and it becomes primary; leave only
// ZAI_API_KEY set and you degrade to the old coding-plan behaviour. No
// code change to switch providers — just set/unset a Worker secret.
//
// ⚠️ PAID vs FREE: for PAID users set a pay-per-token key as primary. The
// z.ai coding plan (flat) is acceptable ONLY for the free/beta grant tier
// — never the primary for paid seats (ToS + staleness).
//
// ⚠️ RATES ARE PLACEHOLDERS — verify each provider's live pricing before
// flipping YAVER_MANAGED_METER_LIVE. Cents per 1,000,000 tokens.
export const ROUTES: Record<string, Upstream[]> = {
  auto: [
    // 1) Pay-per-token primary — strong agentic coder, no quota wall.
    {
      provider: "deepinfra",
      model: "deepseek-ai/DeepSeek-V3",
      baseUrl: "https://api.deepinfra.com/v1/openai",
      keyEnv: "DEEPINFRA_API_KEY",
      inCentsPerM: 27,   // PLACEHOLDER ~$0.27/M in
      outCentsPerM: 110, // PLACEHOLDER ~$1.10/M out
    },
    // 2) Cheaper pay-per-token coder for the bulk of mechanical edits.
    {
      provider: "deepinfra",
      model: "Qwen/Qwen2.5-Coder-32B-Instruct",
      baseUrl: "https://api.deepinfra.com/v1/openai",
      keyEnv: "DEEPINFRA_API_KEY",
      inCentsPerM: 8,    // PLACEHOLDER ~$0.08/M in
      outCentsPerM: 18,  // PLACEHOLDER ~$0.18/M out
    },
    // 3) Together — alternate pay-per-token provider (set TOGETHER_API_KEY
    //    to diversify/prefer; unset ⇒ skipped).
    {
      provider: "together",
      model: "deepseek-ai/DeepSeek-V3",
      baseUrl: "https://api.together.xyz/v1",
      keyEnv: "TOGETHER_API_KEY",
      inCentsPerM: 60,   // PLACEHOLDER
      outCentsPerM: 170, // PLACEHOLDER
    },
    // 4) z.ai PAY-AS-YOU-GO GLM-4.6 (/api/paas/v4, separate prepaid
    //    balance — NOT the coding plan). Set ZAI_PAYG_API_KEY to use it.
    {
      provider: "zai-payg",
      model: "glm-4.6",
      baseUrl: "https://api.z.ai/api/paas/v4",
      keyEnv: "ZAI_PAYG_API_KEY",
      inCentsPerM: 43,   // PLACEHOLDER ~$0.43/M in
      outCentsPerM: 174, // PLACEHOLDER ~$1.74/M out
    },
    // 5) LAST RESORT — z.ai Coding Plan (flat, oversubscribed). Free/beta
    //    grant tier only. Its per-token rate is a FAIR-USE BUDGET UNIT,
    //    not real money (our cost is the flat plan fee). Demoted from
    //    primary in P2; kept so a coding-plan-only deployment still works.
    {
      provider: "zai",
      model: "glm-4.6",
      baseUrl: "https://api.z.ai/api/coding/paas/v4",
      keyEnv: "ZAI_API_KEY",
      inCentsPerM: 60,   // PLACEHOLDER (fair-use budget unit)
      outCentsPerM: 220, // PLACEHOLDER
    },
  ],
};

// Map a client-requested model string to a candidate chain. The normie
// sends "auto" (or nothing); a power user could send "glm-4.6" to pin.
export function resolveRoute(requested?: string): Upstream[] {
  const key = (requested || "auto").toLowerCase();
  if (ROUTES[key]) return ROUTES[key];
  // Pin-by-model: find the exact upstream across all chains.
  for (const chain of Object.values(ROUTES)) {
    const hit = chain.find((u) => u.model.toLowerCase() === key);
    if (hit) return [hit];
  }
  return ROUTES.auto;
}

// Raw upstream COGS in cents (fractional — Convex ceils at the debit).
export function costCents(u: Upstream, inTok: number, outTok: number): number {
  return (inTok * u.inCentsPerM + outTok * u.outCentsPerM) / 1_000_000;
}
