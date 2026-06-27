# Buy & manage a Yaver plan from MCP (buyer-side billing)

Status: DESIGN — awaiting sign-off before implementation (2026-06-27). Source
is truth; this doc points at code, fix the code reference here if they drift.

Goal: a Yaver user, from their terminal coding agent (Claude Code / Codex /
opencode), can **check their plan, buy Workspace ($9) or Agent ($19), pay,
change tier, and cancel** — all via Yaver MCP tools that return LemonSqueezy
(LS) links. The agent never handles card data; LS hosts checkout + the customer
portal.

## Scope: buyer-side, NOT the existing seller-side tools

`lemonsqueezy_*` MCP tools (`desktop/agent/lemonsqueezy.go`) are **seller-side**
— they manage *the user's own* LS store (a Yaver feature for *their* products).
This work is the opposite: a Yaver user buying *Yaver's own* subscription. No
such tool exists today.

## What already exists (reuse, don't rebuild)

| Stage | Exists | Where |
|---|---|---|
| Checkout URL builder | ✅ `createLemonSqueezyCheckout({email, custom, variantId})` → `POST /v1/checkouts` | `backend/convex/http.ts:121` |
| Buy endpoint (authed) | ✅ `POST /billing/yaver-cloud/checkout` (uses `session.email`) | `http.ts:4290` |
| Webhook → activation | ✅ `POST /webhooks/lemonsqueezy`, HMAC-verified; `subscription_created/updated/resumed/cancelled/expired` | `http.ts:4009` |
| Entitlements | ✅ `applyPlanEntitlements` byok($9)/hosted($19): 40h + wallet + gateway flag | `plans.ts:61` |
| Tier swap (in-app) | ✅ `updateLemonSqueezyVariant` | `http.ts:298` |
| Status data | ✅ `getAllowance` (hours), `prepaidCredits` (wallet), `subscriptions` table, `/billing/yaver-cloud/balance` | `cloudLifecycle.ts:329` |
| Credit top-up | ✅ `/billing/credits/checkout` + `/billing/credits/packs` | `http.ts:4360` |

## Attribution (the make-or-break detail)

LS subscriptions map to a Yaver user **by email**, not userId:
```ts
const userEmail = payload.meta?.custom_data?.user_email || data.user_email; // http.ts:4041
const user = await getUserByEmail({ email: userEmail });
```
The MCP tool MUST seed `custom_data.user_email = <authed user's email>` from
`authStatusSnapshot().UserEmail`. Tool output must warn: **"pay with the email
you signed into Yaver with"** — a mismatched payer email orphans the
subscription.

## Tier → variant mapping (CONFIG GAP — verify before launch)

The webhook picks the tier from `custom_data.tier` (default `"byok"`,
`http.ts:4087`); the variant to charge comes from `lsEnv(tier === "hosted" ?
"YAVER_CLOUD_HOSTED_VARIANT_ID" : "YAVER_CLOUD_BYOK_VARIANT_ID")` (`http.ts:298`).

**⚠️ Prod env currently has ONLY `LEMONSQUEEZY_YAVER_CLOUD_VARIANT_ID`** (one
default variant). `…HOSTED_VARIANT_ID` and `…BYOK_VARIANT_ID` are **not set on
prod** (verified via `npx convex env list --prod`). Until both exist:
- a "buy Agent ($19)" checkout would charge the single default variant and
  (gap #1 below) the webhook would grant **byok** entitlements — silent
  mis-bill.

**Launch prerequisite:** create the $9 and $19 products/variants in the LS
dashboard and set BOTH `LEMONSQUEEZY_YAVER_CLOUD_BYOK_VARIANT_ID` and
`LEMONSQUEEZY_YAVER_CLOUD_HOSTED_VARIANT_ID` on the **prod** deployment.

## Three code gaps to close

1. **Tier not set on initial checkout.** `/billing/yaver-cloud/checkout`
   (`http.ts:4290`) passes `plan_id` but **not `tier`**, and always the default
   variant — so the webhook defaults to byok regardless of what the buyer
   picked. Fix: accept `tier`, pass `custom_data.tier` + the tier-specific
   variant. (Also fixes the *web* buy flow, same bug.)
2. **No status read for MCP.** Data exists (`subscriptions`,
   `includedAllowance`, `prepaidCredits`, `gatewayPolicy`) but no single
   endpoint answers "subscribed? which tier? active/cancelled? hours/wallet
   left?" — needed for "aware if already purchased". Add `GET /billing/status`.
3. **No self-service cancel/manage.** The webhook *reconciles*
   cancelled/expired/resumed, but the LS **customer-portal URL**
   (`urls.customer_portal`, `urls.update_payment_method`) is never stored. Add:
   capture both into the `subscriptions` row in the webhook, + `GET
   /billing/portal` returning the latest. This is the single answer for
   "cancel" and "fix a failed payment".

## MCP tool surface (idempotent, status-aware)

Thin wrappers over the `/billing/*` endpoints, authed as the user. Names use the
`yaver_billing_*` prefix (kept OUT of the owner-gate prefixes, so they're public
to every user).

- **`yaver_billing_status`** → `{ subscribed, tier, status, periodEnd,
  includedHoursLeft, walletCents, managedInference }`. The always-first read;
  satisfies "aware if already purchased".
- **`yaver_billing_checkout { plan: "workspace" | "agent" }`** → LS URL with the
  right variant + `custom_data={user_email, tier}`.
  - Idempotency: if `status` already shows that tier **active** → return "You're
    already on <plan>, renews <date>" + the manage link, NOT a new checkout.
  - If on the **other** tier → route to **change-plan** (variant swap via
    `updateLemonSqueezyVariant`), not a second subscription.
- **`yaver_billing_manage`** → customer-portal URL (update payment / change /
  cancel). Single answer for cancel + failed-payment.
- **`yaver_billing_topup { amount }`** → credit-pack checkout for prepaid
  overage (reuses `/billing/credits/checkout`).

Lifecycle coverage: **create** (checkout) · **pay / fix payment** (checkout +
portal) · **change tier** (checkout detects existing → swap) · **cancel /
resume** (portal; webhook reconciles) · **status** (always-first read).

## Implementation order (backend first, then the thin MCP layer)

1. `http.ts` `/billing/yaver-cloud/checkout`: honor `tier` → tier variant +
   `custom_data.tier` (gap #1).
2. `http.ts` add `GET /billing/status` (gap #2) backed by a `cloudLifecycle`
   query joining `subscriptions` + `includedAllowance` + `prepaidCredits` +
   `gatewayPolicy`.
3. webhook: store `urls.customer_portal` / `urls.update_payment_method` on the
   `subscriptions` row; add `GET /billing/portal` (gap #3).
4. `desktop/agent/mcp_billing.go`: the four tools above → call the endpoints via
   the local agent's authed Convex client; register in `getMCPToolsList` +
   dispatch; each returns structured JSON + a spoken `next_action`.

No deploy until: the two tier variants exist in LS + their prod env vars are
set + a sandbox end-to-end test (checkout → webhook → entitlement) passes.

## Launch checklist

- [ ] Create $9 Workspace + $19 Agent products/variants in LS dashboard.
- [ ] Set `LEMONSQUEEZY_YAVER_CLOUD_{BYOK,HOSTED}_VARIANT_ID` on **prod**.
- [ ] Confirm `LEMONSQUEEZY_WEBHOOK_SECRET` matches the LS webhook config.
- [ ] Sandbox test each tool: status (none → active), checkout (byok + agent),
      change tier, manage/cancel, topup.
- [ ] Verify the webhook attributes by `user_email` for a real terminal buy.
