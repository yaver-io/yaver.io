# Yaver Premium — overnight status (read me first)

Worked autonomously while you slept. Everything below is **committed +
pushed to `main`**, **dormant / fail-closed**, and **not launched**.
**No Hetzner box was ever started — €0 spent, nothing to close.**

## TL;DR

The thesis is built into a working spine: *manage your own infra → free;
want Yaver to manage it → metered, Yaver takes the arbitrage.* One prepaid
wallet, five meters (compute / inference / backend / web / publish). The
**inference gateway is code-complete and tested**; meters 3–5 are specced.
Nothing bills anyone until you flip env flags and deploy the Worker.

## My commits (newest first)

| Commit | What |
|---|---|
| `0995eccd` | gateway: reject malformed requests before any charge |
| `0ea56229` | meters 3–5 spec + limiter tests + gateway-spec update |
| `cd1bba37` | web: advertise `gatewayUrl` from `/api/mobile-config` |
| `0bba18b2` | gateway: per-user hourly cap + sub-cent carry (Durable Object) + tests |
| `f7817176` | mobile: managed-mode coding (route loop through gateway) |
| `41875ce5` | metering spine + inference gateway (Convex side) |

(Interleaved with another session's Android/social-graph commits — I
committed only my own files via pathspec, never `git add -A`, and stayed
out of `backend/convex/` after it started editing there.)

## What's LIVE right now (deployed, but fail-closed)

- **Convex prod** (`perceptive-minnow-557`): `managedUsage` table + indexes;
  `/gateway/authorize` + `/gateway/meter` routes. Verified fail-closed:
  `/gateway/meter` → `500 GATEWAY_SHARED_SECRET not set`, `/gateway/authorize`
  → `401`. Metering is `dryRun` until `YAVER_MANAGED_METER_LIVE=true`.

## What's CODE-COMPLETE (built + tested, not deployed)

- **Yaver Gateway** (`gateway/`) — captive-OpenRouter inference proxy:
  - `src/index.ts` — authorize → ceilings → cheapest-capable route
    (GLM-4.6 ▸ DeepSeek ▸ Qwen) → SSE stream w/ usage tee → carry-aware meter.
  - `src/limiter.ts` — `UserMeter` Durable Object: rolling 1h per-user cap
    (`MAX_CENTS_PER_HOUR`, 429 on exceed) + sub-cent carry (no ceil-overcharge).
    Graceful fallback if the DO isn't bound.
  - **15 tests pass** (`pricing.test.ts` 8 + `limiter.test.ts` 7); Worker
    typechecks clean against `@cloudflare/workers-types`.
  - Malformed requests rejected before any upstream call/charge.
- **Mobile managed mode** — agent loop routes through the gateway authed by
  the session token (no model key on device); off by default, BYO fallback.
- **Web** — `/api/mobile-config` advertises `gatewayUrl` so the phone
  discovers the Worker without a rebuild.

## What's SPECCED (design only)

- `docs/yaver-premium-meters-3-5.md` — backend (Convex proxy), web
  (Cloudflare proxy), publish (Mac-farm) meters. All report through the same
  `/gateway/meter` spine via `kind`; the wallet/markup/opt-in/privacy are
  done, so each is just provisioner + cron reporter + one-tap UX card.
  Recommended order: **backend first** (it kills the phone-only "can't
  deploy" wall).

## To go live (your call — needs secrets I don't have)

1. `cd gateway && npm i`; `wrangler secret put` for `GATEWAY_SHARED_SECRET`,
   `ZAI_API_KEY`, `DEEPINFRA_API_KEY`; set `CONVEX_URL` in `wrangler.toml`
   to `https://perceptive-minnow-557.eu-west-1.convex.site`.
2. **Verify `gateway/src/pricing.ts` rates** against live z.ai/DeepInfra
   pricing — they're PLACEHOLDERS; wrong numbers leak margin silently.
3. `wrangler deploy`.
4. Set Convex env `GATEWAY_SHARED_SECRET` (same value) + `YAVER_GATEWAY_URL`
   (so mobile auto-discovers it).
5. Test on a device: set `LOCAL_KEYS.gatewayUrl` override + `setManagedCodingEnabled(true)`,
   ensure wallet balance + cloud-access allowlist.
6. When happy: `YAVER_MANAGED_METER_LIVE=true` to leave dry-run.

## Deliberately NOT done

- **No `convex deploy` re-run** — the working tree holds another session's
  uncommitted `connections.ts`/`projectShares.ts`/`managedMeter` fair-metering
  edits; deploying would ship their unfinished work. My Convex part was
  already deployed earlier and is live.
- **No mobile TestFlight/Play build** — premature; the gateway isn't
  deployed yet, so there's nothing for the phone to talk to.
- **No Hetzner** — not needed for any of this.

## Doc map

- `docs/yaver-premium-zero-to-hero.md` — the product + the rule (§0).
- `docs/yaver-gateway-spec.md` — inference gateway, concrete.
- `docs/yaver-premium-meters-3-5.md` — the remaining three meters.
- `docs/yaver-premium-OVERNIGHT-STATUS.md` — this file.
