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

// Cheapest-capable first; the gateway falls through on 5xx/timeout.
// "auto" is what the normie always sends — he never picks a model.
export const ROUTES: Record<string, Upstream[]> = {
  auto: [
    {
      provider: "zai",
      model: "glm-4.6",
      // GLM Coding Plan endpoint (subscription, flat-rate). NOT the
      // pay-as-you-go /api/paas/v4 (that bills a separate prepaid balance
      // and returns 1113 "insufficient balance" for coding-plan keys).
      // The coding plan is served from /api/coding/paas/v4 (OpenAI-compat).
      baseUrl: "https://api.z.ai/api/coding/paas/v4",
      keyEnv: "ZAI_API_KEY",
      // Our real cost is the flat YEARLY plan fee, so these per-token rates
      // are NOT money — they're the FAIR-USE BUDGET UNIT a free-tier tenant
      // spends from their grant. Nominal GLM-4.6-ish rates so a tenant's
      // small free grant ($1–2) depletes and throttles them. (PLACEHOLDER.)
      inCentsPerM: 60,
      outCentsPerM: 220,
    },
    {
      provider: "deepinfra",
      model: "deepseek-ai/DeepSeek-V3",
      baseUrl: "https://api.deepinfra.com/v1/openai",
      keyEnv: "DEEPINFRA_API_KEY",
      inCentsPerM: 27,   // PLACEHOLDER
      outCentsPerM: 110, // PLACEHOLDER
    },
    {
      provider: "deepinfra",
      model: "Qwen/Qwen2.5-Coder-32B-Instruct",
      baseUrl: "https://api.deepinfra.com/v1/openai",
      keyEnv: "DEEPINFRA_API_KEY",
      inCentsPerM: 8,    // PLACEHOLDER
      outCentsPerM: 18,  // PLACEHOLDER
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
