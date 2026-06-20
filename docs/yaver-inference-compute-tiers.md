# Yaver inference / compute tiers + beta managed inference

> 2026-06-20. Pricing/tiering design + the concrete build gaps. Sibling of
> `beta-invisible-infra-share-design.md` (the beta soft-launch) — this doc is the
> commercial framing + the gaps to make it a real product.

## The model (confirmed)

Three things sell **independently**: **compute** (a remote box), **inference**
(GLM / metered tokens), **backend/host**.

| Tier | Compute (box) | Inference | When |
|---|---|---|---|
| **Phone-only + BYO GLM key** | none | user's own z.ai key | entry / free — $0 infra from us |
| **On-device model (GGUF)** | none | none (offline) | privacy / offline — needs native build |
| **Managed inference** | none | we resell metered GLM via the gateway | **sell first** — monetizes the build loop |
| **Remote box** | our box | runner (glm/codex/opencode…) | **sell at deploy** — phone can't host a live backend |

**Key insight:** the natural upgrade wall is **deploy/host**. A phone can build &
prototype (BYO or managed inference); to keep a backend running / host the app it
needs a box. So inference monetizes building; the box monetizes shipping.

**A user never *has* to buy a box** — phone-only + GLM key is complete.

## Beta managed inference (the "no key" path)

Beta users get **managed inference with no key entry**. Built on the existing
beta soft-launch (`beta-invisible-infra-share-design.md`):

- **Backend (exists):** `betaAccess.ts` (owner-gated `seedBetaUser`/`revokeBetaUser`)
  → `getBetaStatus` → `GET /subscription` `beta` field `{isBeta, plan,
  sharedProject, includedHours, usedHours, aiEnabled}`.
- **Inference (exists):** Cloudflare gateway Worker (`gateway/src/index.ts`,
  `ZAI_API_KEY` worker secret — "wallet IS the key") + per-user `gatewayTokens`
  (`ygw_…`) + `gatewayPolicy` caps (per-user grant ceiling + daily/hourly). The
  raw GLM key never reaches the phone.
- **Web mirror (exists):** `web/lib/subscription.ts` `BetaStatus`/`isBetaUser`,
  `useBetaStatus`, `BetaWorkspaceView`.
- **Mobile (built here):** `mobile/src/lib/subscription.ts` now has `BetaStatus`
  + `beta` field + `isBetaUser()`; `phone-projects.tsx` shows an **Inference
  radio** — **"Beta access"** (managed, no key) vs **"Use my own key"** (BYO
  OpenAI/GLM). Beta users default to managed; create() skips the key requirement
  for beta-managed.

## Gaps to close (build list)

1. **Quota enforcement** — `managedMeter.ts` *measures* (5 meters, dryRun); add
   **block-at-limit** for free tiers: token cap (gateway side), cpu/time cap (box
   side — `CPULimitPercent`/`RAMLimitMB` are plumbed; add wall-clock + monthly).
2. **Idle-shutdown** — NOT wired. The single biggest lever for free-compute
   economics: auto-stop an idle managed box. Needs an idle detector + stop on the
   box + `cloudLifecycle` hook.
3. **Mobile beta managed coding (data plane)** — the radio + detection are done;
   routing a beta user's *actual coding* to the owner's box (hidden infra grant +
   opencode + gateway) is the box-side tenant data plane from the beta design
   (partly built: `beta_broker.go`, `beta_tenant.go`, `beta_scrub.go` — TODO:
   wire broker→HTTP + run runner as tenant user).
4. **Free-tier accounting** — per-account included quota + reset window + the
   upgrade prompt when exhausted.

## Compliance (hard rule)

Resell **metered** keys only (GLM/z.ai apikey). **Never** resell subscription
tokens (Claude Max / ChatGPT Plus) — Anthropic/OpenAI ToS, detectable; already
enforced in `mobile/src/lib/codingBackend.ts`.
