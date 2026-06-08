# Yaver Gateway

Captive-OpenRouter inference proxy for **Yaver Premium**. One
OpenAI-compatible endpoint; Yaver holds upstream model keys and meters
per-token usage into the user's prepaid wallet (with arbitrage markup).

> **The rule:** manage your own infra → free (BYO key, pay the provider
> directly). Want Yaver to manage it → metered through this gateway, and
> Yaver takes the spread.

Full design: [`docs/yaver-gateway-spec.md`](../docs/yaver-gateway-spec.md).
Product context: [`docs/yaver-premium-zero-to-hero.md`](../docs/yaver-premium-zero-to-hero.md).

## Status

Skeleton + Convex trust boundary. **Dormant / fail-closed** — `/gateway/meter`
500s until `GATEWAY_SHARED_SECRET` is set; metering stays `dryRun` until
`YAVER_MANAGED_METER_LIVE=true`. Not launched.

## Quick start

```bash
cd gateway
npm install
npm run typecheck

# secrets
npx wrangler secret put GATEWAY_SHARED_SECRET   # == Convex env of same name
npx wrangler secret put ZAI_API_KEY
npx wrangler secret put DEEPINFRA_API_KEY
# set CONVEX_URL (.convex.site) in wrangler.toml

npm run deploy
```

## Before going live

- **Verify `src/pricing.ts` rates** against live provider pricing.
- Add the Durable-Object per-hour cap + sub-cent carry (see spec §ceilings).
- Wire phone "managed" mode (`mobile/.../sandboxBinding.ts`).
