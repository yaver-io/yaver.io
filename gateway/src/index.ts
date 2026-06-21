// Yaver Gateway — a captive OpenRouter for Yaver Premium.
//
// One OpenAI-compatible endpoint (/v1/chat/completions). The phone's
// Hermes GLM loop and managed-box runners point their OPENAI_BASE_URL
// here with the user's Yaver SESSION TOKEN as the API key — there is no
// model API key on the device; the wallet IS the key.
//
// Flow per request:
//   1. authorize: session token → {userId, balanceCents} via Convex
//   2. ceilings:  clamp max_tokens, refuse if balance can't afford a floor
//   3. route:     cheapest-capable upstream (pricing.ts), fall through on 5xx
//   4. stream:    proxy SSE back to the client, tee to capture token usage
//   5. meter:     raw COGS → Convex /gateway/meter (markup + wallet debit)
//
// Privacy: the gateway SEES prompts but never persists them and never
// sends them to Convex — only {userId, model, tokens, cost} crosses to
// Convex. Keep Worker logging scrubbed.
//
// Host: Cloudflare Worker (global edge, native streaming). The relay (Go)
// is the alternative host if you'd rather keep it in-house.

import { resolveRoute, costCents, type Upstream } from "./pricing";
import { meterCheck, meterRecord, UserMeter } from "./limiter";

// Durable Object class must be re-exported from the Worker entry module.
export { UserMeter };

export interface Env {
  CONVEX_URL: string;             // e.g. https://<deployment>.convex.site
  GATEWAY_SHARED_SECRET: string;  // matches Convex GATEWAY_SHARED_SECRET
  MAX_TOKENS_PER_REQUEST: string; // hard cap on completion tokens
  MAX_CENTS_PER_REQUEST: string;  // refuse if worst-case > this
  MAX_CENTS_PER_HOUR?: string;    // rolling per-user hourly cap (0/unset = off)
  USER_METER?: DurableObjectNamespace; // per-user cap + sub-cent carry (optional)
  // Upstream provider keys (referenced by Upstream.keyEnv). Set whichever
  // you use; callUpstream skips any upstream whose key is unset, so the
  // set of configured keys IS the provider selection (pay-per-token first).
  ZAI_API_KEY: string;          // z.ai Coding Plan (flat) — free/beta tier
  ZAI_PAYG_API_KEY?: string;    // z.ai pay-as-you-go (/api/paas/v4)
  DEEPINFRA_API_KEY: string;    // pay-per-token primary (default)
  TOGETHER_API_KEY?: string;    // pay-per-token alternate
  [k: string]: unknown;
}

function num(v: string | undefined, d: number): number {
  const n = Number(v);
  return Number.isFinite(n) && n > 0 ? n : d;
}

// CORS: the gateway is called from browser clients (web dashboard + the mobile
// web preview) as well as native RN. Without these headers a browser fetch is
// blocked and the managed-inference call fails silently. Tokens are the auth, so
// "*" origin is acceptable (the bearer still gates every request).
const CORS = {
  "access-control-allow-origin": "*",
  "access-control-allow-methods": "POST, GET, OPTIONS",
  "access-control-allow-headers": "authorization, content-type",
  "access-control-max-age": "86400",
};

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "content-type": "application/json", ...CORS },
  });
}

// ── Convex trust-boundary calls ─────────────────────────────────────

// Per-user limits returned by Convex /gateway/authorize. Operator-set and
// user-immutable (gatewayPolicy table). 0 = "fall back to the env default".
type GwLimits = {
  maxTokensPerRequest: number;
  maxCentsPerRequest: number;
  hourlyCapCents: number;
  dailyCapCents: number;
  spentTodayCents: number;
};

async function authorize(
  env: Env,
  bearer: string,
): Promise<{
  userId: string;
  balanceCents: number;
  allow: boolean;
  reason?: string;
  limits?: GwLimits;
} | null> {
  const r = await fetch(`${env.CONVEX_URL}/gateway/authorize`, {
    method: "POST",
    headers: { authorization: `Bearer ${bearer}`, "content-type": "application/json" },
    body: "{}",
  });
  if (!r.ok) return null;
  return (await r.json()) as any;
}

async function meter(
  env: Env,
  body: {
    userId: string;
    model: string;
    provider: string;
    quantity: number;
    providerCostCents: number;
    ref?: string;
  },
): Promise<void> {
  // Best-effort: a metering failure must not corrupt the user's stream,
  // but it MUST be alarmed (unmetered usage = silent margin leak).
  try {
    const r = await fetch(`${env.CONVEX_URL}/gateway/meter`, {
      method: "POST",
      headers: {
        authorization: `Bearer ${env.GATEWAY_SHARED_SECRET}`,
        "content-type": "application/json",
      },
      body: JSON.stringify({ kind: "inference", unit: "token", ...body }),
    });
    if (!r.ok) console.error("gateway meter failed", r.status);
  } catch (e) {
    console.error("gateway meter threw", String(e));
  }
}

// ── Usage extraction from a streamed/non-streamed completion ─────────
// Returns {in,out} tokens. For streams we require the upstream to emit a
// final usage chunk (we inject stream_options.include_usage=true).

type Usage = { in: number; out: number };

function usageFromJson(obj: any): Usage | null {
  const u = obj?.usage;
  if (!u) return null;
  return { in: u.prompt_tokens ?? 0, out: u.completion_tokens ?? 0 };
}

// Carry-aware billing: accumulate raw fractional COGS in the user's DO and
// only forward WHOLE cents to Convex (markup applied there). A request whose
// COGS is still sub-cent after carry writes no ledger row this time — its cost
// rides forward into the next emission. With no DO bound, meterRecord ceils.
async function billUsage(
  env: Env,
  userId: string,
  upstream: Upstream,
  usage: { in: number; out: number },
): Promise<void> {
  const cost = costCents(upstream, usage.in, usage.out);
  const bill = await meterRecord(env, userId, cost);
  if (bill <= 0) return;
  await meter(env, {
    userId,
    model: upstream.model,
    provider: upstream.provider,
    quantity: usage.in + usage.out,
    providerCostCents: bill,
  });
}

// ── Upstream call with fallback ─────────────────────────────────────

async function callUpstream(
  env: Env,
  chain: Upstream[],
  payload: any,
): Promise<{ res: Response; upstream: Upstream } | null> {
  for (const u of chain) {
    const key = env[u.keyEnv] as string | undefined;
    if (!key) continue;
    try {
      const res = await fetch(`${u.baseUrl}/chat/completions`, {
        method: "POST",
        headers: { authorization: `Bearer ${key}`, "content-type": "application/json" },
        body: JSON.stringify({ ...payload, model: u.model }),
      });
      // Fall through on upstream-side failures: 5xx, AND 429 (rate-limit/
      // quota — the coding-plan "staleness": a throttled upstream must
      // fail over to the next provider, never be returned to the user) and
      // 408 (upstream timeout). Other 4xx (400/401/403) are not retried —
      // a bad request / bad key fails identically on the next candidate.
      if (res.status >= 500 || res.status === 429 || res.status === 408) continue;
      return { res, upstream: u };
    } catch {
      continue; // network/timeout → next candidate
    }
  }
  return null;
}

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(request.url);
    if (request.method === "OPTIONS") {
      return new Response(null, { status: 204, headers: CORS });
    }
    if (request.method === "GET" && url.pathname === "/healthz") {
      return json({ ok: true });
    }
    if (request.method !== "POST" || !url.pathname.endsWith("/chat/completions")) {
      return json({ error: "not found" }, 404);
    }

    const auth = request.headers.get("authorization") ?? "";
    const bearer = auth.startsWith("Bearer ") ? auth.slice(7) : "";
    if (!bearer) return json({ error: "missing bearer" }, 401);

    const session = await authorize(env, bearer);
    if (!session) return json({ error: "unauthorized" }, 401);
    if (!session.allow || session.balanceCents <= 0) {
      return json({ error: "insufficient_balance", balanceCents: session.balanceCents }, 402);
    }

    let payload: any;
    try {
      payload = await request.json();
    } catch {
      return json({ error: "bad json" }, 400);
    }
    // Reject malformed requests BEFORE any upstream call or charge — a junk
    // request must never cost the user a token.
    if (!payload || !Array.isArray(payload.messages) || payload.messages.length === 0) {
      return json({ error: "messages required" }, 400);
    }

    // ── Ceilings ────────────────────────────────────────────────────
    // Per-user limits (operator-set, user-immutable) win over the env
    // defaults; 0/unset → fall back to env. The user cannot raise these.
    const lim = session.limits;
    const maxTok =
      lim && lim.maxTokensPerRequest > 0
        ? lim.maxTokensPerRequest
        : num(env.MAX_TOKENS_PER_REQUEST, 4096);
    const maxCents =
      lim && lim.maxCentsPerRequest > 0
        ? lim.maxCentsPerRequest
        : num(env.MAX_CENTS_PER_REQUEST, 50);
    payload.max_tokens = Math.min(payload.max_tokens ?? maxTok, maxTok);

    const chain = resolveRoute(payload.model);
    const primary = chain[0];
    // Worst-case spend for this request at the primary route. If the
    // wallet can't cover it, refuse rather than risk an overdraft. (We
    // clamp at 0 balance anyway, but refusing up-front is honest UX.)
    const worst = costCents(primary, payload.max_tokens, payload.max_tokens);
    if (Math.min(worst, maxCents) > session.balanceCents) {
      return json({ error: "insufficient_balance", balanceCents: session.balanceCents }, 402);
    }

    // Rolling per-user hourly cap (Durable Object; no-op if unbound). Bounds a
    // runaway loop even when the wallet is flush. Estimate ~ worst-case cents.
    const estCents = Math.ceil(Math.min(worst, maxCents));
    const userHourlyCap = lim && lim.hourlyCapCents > 0 ? lim.hourlyCapCents : 0;
    const cap = await meterCheck(env, session.userId, estCents, userHourlyCap);
    if (!cap.allow) {
      return json({ error: "rate_limited", remainingCents: cap.remaining }, 429);
    }

    const stream = payload.stream === true;
    if (stream) {
      // Force a final usage chunk so we can meter exact tokens.
      payload.stream_options = { ...(payload.stream_options ?? {}), include_usage: true };
    }

    const up = await callUpstream(env, chain, payload);
    if (!up) return json({ error: "no upstream available" }, 502);

    // ── Non-streaming: read usage, meter, return ────────────────────
    if (!stream) {
      const obj = await up.res.json();
      const usage = usageFromJson(obj) ?? { in: 0, out: 0 };
      ctx.waitUntil(billUsage(env, session.userId, up.upstream, usage));
      return json(obj, up.res.status);
    }

    // ── Streaming: tee SSE to the client AND parse for the usage chunk ─
    if (!up.res.body) return json({ error: "empty upstream stream" }, 502);
    const [toClient, toMeter] = up.res.body.tee();

    ctx.waitUntil(
      (async () => {
        let usage: Usage = { in: 0, out: 0 };
        const reader = toMeter.getReader();
        const dec = new TextDecoder();
        let buf = "";
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += dec.decode(value, { stream: true });
          let nl: number;
          while ((nl = buf.indexOf("\n")) >= 0) {
            const line = buf.slice(0, nl).trim();
            buf = buf.slice(nl + 1);
            if (!line.startsWith("data:")) continue;
            const data = line.slice(5).trim();
            if (data === "[DONE]") continue;
            try {
              const u = usageFromJson(JSON.parse(data));
              if (u) usage = u;
            } catch {
              /* partial/non-usage chunk */
            }
          }
        }
        await billUsage(env, session.userId, up.upstream, usage);
      })(),
    );

    return new Response(toClient, {
      status: up.res.status,
      headers: {
        "content-type": "text/event-stream",
        "cache-control": "no-cache",
        connection: "keep-alive",
        ...CORS,
      },
    });
  },
};
