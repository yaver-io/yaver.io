# Yaver Gateway — concrete spec

> The inference meter of [Yaver Premium](./yaver-premium-zero-to-hero.md).
> A captive OpenRouter: per-token AI resale into the prepaid wallet, with
> Yaver holding upstream keys and earning the spread. **Built as a
> skeleton + Convex trust boundary; dormant/fail-closed until launch.**

## The thesis it serves

> **Manage your own infra → free. Want Yaver to manage it → metered, and
> Yaver takes the arbitrage.** The gateway is how that rule applies to
> AI: a BYO-key developer pays the provider directly (free path,
> untouched); a Premium user lets Yaver hold the key and meters tokens
> through their wallet.

## Why a gateway (not the OAuth mirror)

Reselling a **subscription** token (Claude/Codex OAuth) violates provider
terms — and `subscriptionStore.ts` already rejects API keys on the mirror
path for exactly this reason. The legitimate arbitrage is **per-token API
resale** (z.ai / DeepInfra / OpenRouter all permit it). So Premium
inference flows through a gateway that holds per-token API keys
server-side, never the device.

**"Provision a key for the user" collapses to nothing:** there is no key
to mint, leak, or rotate. The user authenticates with their Yaver session
token; **the wallet is the key.**

## Topology

```
phone Hermes GLM loop            managed-box runner (codex/opencode)
  baseURL = gateway                 OPENAI_BASE_URL = gateway
  apiKey  = session token           OPENAI_API_KEY  = session token
        \                              /
         ▼                            ▼
        ┌──────────────────────────────────┐
        │  Yaver Gateway (Cloudflare Worker)│  gateway/src/index.ts
        │  • POST /v1/chat/completions      │
        │  • authorize → route → stream → meter
        └──────────────────────────────────┘
            │ authorize (user bearer)        │ meter (gateway secret)
            ▼                                 ▼
   POST /gateway/authorize            POST /gateway/meter        (http.ts)
   → cloudLifecycle.getWallet         → managedMeter.recordManagedUsage
                                         → markup(kind) → debit wallet
            ▲ upstream key (server-side only)
            └── z.ai (GLM) ▸ DeepInfra (DeepSeek/Qwen) ▸ …   pricing.ts
```

Only OpenAI-protocol runners route here (the phone loop, codex, opencode).
Claude-protocol subscription runners keep using the OAuth mirror — they're
free (user's own sub), not metered.

## Endpoints

### Gateway (Worker) — `POST /v1/chat/completions`
OpenAI-compatible. Auth: `Bearer <yaver-session-token>`. Supports
streaming (SSE) and non-streaming. The client sends `model: "auto"` (the
normie never picks); power users may pin a model id.

### Convex — `POST /gateway/authorize`  (user-bearer)
`{} ` + `Authorization: Bearer <session>` → `{ ok, userId, balanceCents,
allow }`. `allow` = balance > 0. No snapshot reserve (that's a compute-box
floor; tokens don't need one). Private-preview gated by
`cloudAccessAllowed`.

### Convex — `POST /gateway/meter`  (gateway-secret)
`Authorization: Bearer <GATEWAY_SHARED_SECRET>` +
`{ userId, kind, provider, model, unit, quantity, providerCostCents, ref? }`
→ `recordManagedUsage` → `{ ok, balanceCents, suspend, charged }`.
Asserts arbitrary userId+cost, so it MUST be gateway-secret authed, never
user-bearer. Fail-closed: 500 if `GATEWAY_SHARED_SECRET` unset.

## Cost & markup split

- **Worker** knows tokens (from the upstream `usage`) and per-model COGS
  (`pricing.ts`). It computes raw `providerCostCents` (fractional) and
  reports it.
- **Convex** owns the spread: `chargedCents = ceil(providerCostCents ×
  markup("inference"))`, default **1.5×**, env `YAVER_MANAGED_MARKUP_INFERENCE`.
- Pricing lives ONLY in the Worker → no drift; Convex never sees rates.

The light 1.5× rides on GLM being ~7-10× cheaper than the user's mental
anchor (Claude/ChatGPT): cheap to him, profitable to you.

## Routing & fallback

`pricing.ts ROUTES.auto` is a cheapest-capable chain: GLM-4.6 (z.ai) ▸
DeepSeek-V3 (DeepInfra) ▸ Qwen-Coder (DeepInfra). The gateway falls
through on upstream **5xx/timeout only** (a 4xx would fail identically on
every candidate). The chain is also the **hedge** against any single
upstream changing terms or pricing.

> ⚠️ The rates in `pricing.ts` are PLACEHOLDERS. Verify each against live
> provider pricing before flipping the meter live — wrong rates leak
> margin silently.

## Abuse ceilings (mandatory before go-live)

- `MAX_TOKENS_PER_REQUEST` — hard clamp on `max_tokens`.
- `MAX_CENTS_PER_REQUEST` — refuse if worst-case spend exceeds it.
- **Up-front affordability** — refuse (402) if the request's worst-case
  cost exceeds the wallet balance.
- **Wallet floor** — `recordManagedUsage` clamps balance at 0 and returns
  `suspend`; the gateway 402s the next request.
- **TODO (per-hour cap + sub-cent carry):** add a Durable Object per user
  to (a) rate-limit cents/hour and (b) carry fractional-cent remainders so
  many tiny requests aren't each ceil-rounded up. The current skeleton
  meters per-request (acceptable for MVP, slightly over-charges sub-cent
  calls — i.e. pro-margin, not a leak).

## Privacy

The gateway SEES prompts (it proxies them) but:
- **never persists them** and **never sends them to Convex** — only
  `{userId, model, tokens, cost}` crosses the boundary;
- Worker logs must stay scrubbed (no prompt/response bodies).

This keeps the `convex_privacy_test.go` contract intact: `managedUsage` is
counters + non-secret labels only.

## Files

| File | Role |
|---|---|
| `gateway/src/index.ts` | Worker: authorize → ceilings → route → stream/tee → meter |
| `gateway/src/pricing.ts` | routing chains + per-model COGS + cost calc |
| `gateway/wrangler.toml` | Worker config; `CONVEX_URL` + ceiling vars |
| `gateway/package.json` / `tsconfig.json` | build/typecheck |
| `backend/convex/gatewaySecret.ts` | `GATEWAY_SHARED_SECRET` verify (mirrors cronSecret) |
| `backend/convex/http.ts` | `/gateway/authorize` + `/gateway/meter` routes |
| `backend/convex/managedMeter.ts` | `recordManagedUsage` — markup + wallet debit |

## Deploy / go-live checklist

1. Set Convex env `GATEWAY_SHARED_SECRET` (`npx convex env set`).
2. `cd gateway && npm i`, then `wrangler secret put GATEWAY_SHARED_SECRET`
   (same value), `ZAI_API_KEY`, `DEEPINFRA_API_KEY`.
3. Set `CONVEX_URL` in `wrangler.toml` to the deployment's `.convex.site`.
4. **Verify `pricing.ts` rates against live provider pricing.**
5. `wrangler deploy`.
6. Phone: add "managed" mode in `sandboxBinding.ts` (baseURL=gateway,
   apiKey=session token).
7. Flip `YAVER_MANAGED_METER_LIVE=true` (Convex) to leave dry-run.
8. Add the Durable-Object per-hour cap + sub-cent carry before opening
   beyond private preview.
