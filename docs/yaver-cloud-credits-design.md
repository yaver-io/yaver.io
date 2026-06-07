# Yaver Cloud — Prepaid Credits & Metered Compute (Deep Analysis)

**Status:** BUILT 2026-06-07 (uncommitted, NOT deployed to Convex prod).
Backend + mobile + web all typecheck clean; Go privacy test green. Live-spend
stays fail-closed until the owner flips the env flags (see §13 + the go-live
runbook). Code is source of truth; every claim below is anchored to a file:line.

## 13. What shipped (build log)

- **Configurable markup** — `cloudLifecycle.ts::markup(machineType)`, cpu 2× /
  gpu 3×, env-overridable (`YAVER_CLOUD_MARKUP_CPU|GPU`). Replaced the hardcoded
  `MARKUP_X` in meter + reserve + rate.
- **Prepaid top-up ledger** — new `creditTopups` table (`schema.ts`),
  idempotent `topUpForOrder` keyed on the payment order id (`cloudLifecycle.ts`).
- **Credit-pack front door** — catalog `CREDIT_PACKS` ($10/$25/$50/$100),
  `GET /billing/credits/packs`, `POST /billing/credits/checkout`, and the
  LemonSqueezy `order_created` webhook branch → `topUpForOrder` (`http.ts`).
- **Prepaid spin-up** — `POST /billing/yaver-cloud/provision` (balance-gated,
  no subscription) + a wallet fallback in `cloudMachines.provision`'s
  entitlement gate (funded wallet entitles provisioning).
- **Wallet read + ledger** — `GET /billing/yaver-cloud/balance` (now open to all
  cloud-access users, returns hourly rate + lowBalance) and
  `GET /billing/yaver-cloud/usage` (`getRecentUsage`).
- **Launch flags** — `cloudAccessAllowed` (owner OR `YAVER_CLOUD_PUBLIC`) gates
  balance/usage/checkout/provision/start/stop/decommission; `cloudAccess` added
  to `/subscription`. Meter live = `YAVER_CLOUD_METER_LIVE` env (was hardcoded).
- **Mobile** — `ManagedCloudCard` gains balance, add-credit packs (web checkout
  via `Linking`, no IAP), prepaid spin-up, and an activity ledger; new helpers
  in `mobile/src/lib/subscription.ts`.
- **Web** — `ManagedCloudPanel` gains a prepaid wallet block (balance, packs,
  spin-up); subscription-buy/adopt now owner-gated in the UI.
- **Tests/docs** — `e2e-managed-cloud-dryrun.sh` extended (packs, checkout,
  usage); `convex_privacy_test.go` pins `creditTopups` fields; go-live runbook
  updated with the env-flag table.

## 1. What the user asked for

> Users upload money (like Claude Code / Codex / OpenAI credit top-up). With
> that balance ("bakiye") they get **remote resource usage = Yaver Cloud**
> (Hetzner under the hood, **never branded as Hetzner**). From mobile they see
> their balance and can scale **up/down**. We charge **2×/3× the raw provider
> cost**. **Do not** route money through Apple IAP / Google Play Billing — do
> it the OpenAI way (web credit top-up).

Three hard requirements fall out of that:

1. **Prepaid credit model**, not subscription. Buy $X → spend down → top up.
2. **Mobile shows balance + can spin machines up/down.** (Today mobile is
   read-only by explicit decision — this reverses it.)
3. **Web-based payment**, no in-app purchase. (Already the architecture.)

## 2. Headline finding: ~70% of this is already built and dormant

The hard parts — an integer-cents wallet, an append-only usage ledger, a 2×
markup meter, a fail-closed minimum-reserve gate, an hourly metering cron, and
Hetzner provisioning that never says "Hetzner" to the user — **already exist in
the tree.** They are wired in `dryRun:true`, owner-gated, and **subscription-
shaped** instead of prepaid. This is not a greenfield build; it is:

- flip the model from *subscription-gates-machine* → *credit-funds-machine*,
- build a **real top-up front door** (the one missing piece),
- surface **balance + up/down on mobile**,
- make **markup configurable** (2× today, hardcoded),
- flip `dryRun` off when ready.

### 2.1 What exists today (LIVE in code, dormant at runtime)

| Capability | Where | State |
|---|---|---|
| Wallet (`balanceCents`, lifetime added/used) | `backend/convex/schema.ts:1670` `prepaidCredits` | ✅ table + queries |
| Append-only usage ledger | `schema.ts:1687` `creditUsage` | ✅ table |
| `topUp(userId, amountCents)` primitive | `backend/convex/cloudLifecycle.ts:114` | ✅ live |
| Meter + deduct, 2× markup | `cloudLifecycle.ts:141` `recordUsageAndDeduct` | ✅ live, `dryRun` |
| Markup constant | `cloudLifecycle.ts:26` `MARKUP_X = 2` | ✅ hardcoded |
| Min-reserve fail-closed floor | `cloudLifecycle.ts:48` `minimumReserveCents` | ✅ live |
| Start gate ("can the wallet afford it?") | `cloudLifecycle.ts:202` `canStart` | ✅ live |
| Hourly metering cron (`cloudMeter`) | `backend/convex/crons.ts:19` | ✅ `dryRun:true` |
| Balance read endpoint | `http.ts:4065` `GET /billing/yaver-cloud/balance` | ⚠️ **owner-only** |
| Top-up endpoint | `http.ts:4084` `POST /billing/yaver-cloud/topup-dev` | ⚠️ **owner-dev stub** |
| Start / stop machine | `http.ts` `/billing/yaver-cloud/{start,stop}` | ✅ live |
| Hetzner provisioning (un-branded) | `backend/convex/cloudMachines.ts:1106` + `desktop/agent/cloud_provisioners.go:57` | ✅ live |
| Machine lifecycle state | `schema.ts:1119` `cloudMachines` (provisioning→active→paused→stopped) | ✅ live |
| Mandatory pre-delete snapshot | `desktop/agent/cloud_deploy.go:941` | ✅ live |
| Privacy: wallet fields whitelisted | `desktop/agent/convex_privacy_test.go` `TestPrepaidWalletFields_*` | ✅ pinned safe |
| No Apple/Google IAP in app | `mobile/package.json` (iap deps present but **0 imports**) | ✅ clean |

`cloudLifecycle.ts:9-16` literally documents the posture: *"chargedCents =
MARKUP_X \* hetznerCostCents in BOTH live and stopped states (100% margin).
dryRun (default true …) means simulate: ledger rows written, balance still
moves so the UX is real, but no real Hetzner spend is implied."*

### 2.2 The one structural mismatch: subscription vs prepaid

Today the **purchase front door is a LemonSqueezy *subscription***:
`http.ts:3877 /billing/yaver-cloud/checkout` creates a checkout for a fixed
recurring `YAVER_CLOUD_VARIANT_ID` (`http.ts:102`), and the webhook acts on
`subscription_created` (`http.ts:3737`) to provision one machine. The prepaid
wallet was added **alongside** it (`subscriptionId` is *optional* on
`prepaidCredits` and `cloudMachines`).

The user wants the **opposite primacy**: the wallet is the product, machines
are funded by the wallet, and there is **no subscription**. The good news —
`topUp()` and the whole meter already work with a standalone wallet and don't
require a subscription row. We mostly *delete a coupling*, not add one.

## 3. Target model — "OpenAI credits for compute"

```
                 ┌─────────────────────────────────────────────┐
   buy credits   │  web checkout (Stripe or LemonSqueezy MoR)   │
  (any browser)  │  pick a pack: $10 / $25 / $50 / custom       │
                 └───────────────┬─────────────────────────────┘
                                 │ webhook (order_created / one-time)
                                 ▼
                  internal.cloudLifecycle.topUp(userId, amountCents)
                                 │
                 ┌───────────────▼───────────────┐
                 │  prepaidCredits.balanceCents   │  ← single source of truth
                 └───────────────┬───────────────┘
            spin up │            │ meter hourly (markup × COGS)   │ low-balance
        (gate: canStart)        ▼                                  auto-stop
                 ┌──────────────────────────────────────────────┐
                 │  cloudMachines (Hetzner, shown as "Yaver Cloud")│
                 │  provisioning → active → paused → stopped       │
                 └──────────────────────────────────────────────┘
                                 ▲
            balance + up/down ───┘  mobile  +  web  +  CLI/MCP
```

Key properties:
- **No subscription.** Credit is fungible; one wallet funds N machines.
- **Metered in both states.** Live ≈ 2× €29.99/mo COGS; stopped ≈ 2× snapshot
  storage (`cloudLifecycle.ts:36`). Stopping ≠ free — it's cheap, matching
  reality (snapshot parked).
- **Fail-closed.** A live box is force-stopped *before* the balance can't pay
  for its own snapshot + one parked month (`minimumReserveCents`,
  `recordUsageAndDeduct` `suspend` signal at `:187`).
- **Un-branded.** User-facing surfaces say "Yaver Cloud / cpu / gpu / eu / us"
  — never "Hetzner / cpx42". Provider strings stay server-side
  (`cloudMachines.ts` `MACHINE_SPECS`, `hetznerType`). This is already how the
  web/mobile copy reads; keep it.

## 4. Pricing & markup engine

### 4.1 Make markup configurable (today it's `MARKUP_X = 2`)

The user said "2x 3x **etc**" — markup must be a knob, ideally per-SKU and
overridable without a redeploy. Recommended shape (replaces the const at
`cloudLifecycle.ts:26`):

```ts
// markup resolved per machineType, env-overridable, default 2x.
const MARKUP_BY_TYPE: Record<string, number> = { cpu: 2, gpu: 3 };
function markup(machineType: string): number {
  const env = Number(process.env[`YAVER_CLOUD_MARKUP_${machineType.toUpperCase()}`]);
  return Number.isFinite(env) && env > 0 ? env : (MARKUP_BY_TYPE[machineType] ?? 2);
}
```

Then `recordUsageAndDeduct` (`:160`), `minimumReserveCents` (`:49-52`), and the
`ratePerHourCents` ledger field (`:171`) all read `markup(machineType)` instead
of `MARKUP_X`. The ledger already records both `hetznerCostCents` (raw COGS) and
`chargedCents` (marked-up) per tick — so **margin is auditable per machine per
day for free** (`creditUsage`, `schema.ts:1687`). Don't lose that: keep storing
raw COGS even though the user never sees it.

### 4.2 What the user sees vs what we store

| Stored (server, never shown) | Shown (user) |
|---|---|
| `hetznerType: "cpx42"`, `hetznerServerId` | "Yaver Cloud — CPU (16 GB)" |
| `hetznerCostCents` (raw COGS) | — |
| `chargedCents`, `ratePerHourCents` | "~$0.12/hr running, ~$0.002/hr paused" |
| provider = "hetzner" | region = "eu" / "us" |

This split is already enforced by the privacy contract — provider/cost internals
are agent-side and Convex stores only `serverIp`/`region`/`status` for the box
(`convex_privacy_test.go`; no absolute paths, no provider secrets). Adding the
credit fields is *already blessed* by `TestPrepaidWalletFields_AreNotConvexForbidden`.

### 4.3 Margin sanity (illustrative, cpu SKU)

- COGS live: €29.99/mo ≈ **4.1¢/hr** (`cloudLifecycle.ts:36`).
- 2× → user pays **8.2¢/hr** running ≈ ~$60/mo continuous.
- 3× → **12.3¢/hr** ≈ ~$90/mo continuous.
- Stopped: 2×–3× of ~0.07¢/hr → negligible; the floor (`minimumReserveCents`)
  reserves ~1 parked month so a forgotten box can't go negative.

FX caveat: the wallet is **USD cents** but COGS is **EUR**. `cloudLifecycle.ts:35`
punts with "treat €≈$ for the wallet." At 2–3× markup the FX swing is inside the
margin, so it's safe to launch — but **bake an FX buffer into the markup** (e.g.
treat €1 = $1.15 in the COGS basis) rather than tracking live rates.

## 5. Payment front door — the one genuinely missing piece

This is the only net-new external integration. Two viable paths; pick one.

### Option A — Stripe Checkout (best fit for "upload any amount")
- One-time payment, **custom/variable amount** is native (price `data:
  {currency, unit_amount}` or `customer-chosen amount`). Maps cleanly to
  "upload $37 of credit."
- Webhook `checkout.session.completed` → `topUp(userId, amountCents)`.
- **Downside:** *you* are merchant of record → you handle VAT/sales-tax
  (Stripe Tax helps but it's your liability). For a Turkey-based solo founder
  selling worldwide compute, that's real overhead.
- Stripe is currently **only a dev tool** in the repo (`desktop/agent/stripe_dev.go`
  wraps `stripe listen` for *users'* apps) — no first-party Stripe billing yet.

### Option B — LemonSqueezy (Merchant of Record) — already integrated
- LemonSqueezy is **MoR**: it remits global VAT/sales tax for you. Huge for a
  solo founder. Already wired: `desktop/agent/lemonsqueezy.go` (1196 lines),
  webhook at `http.ts:3692`, checkout at `http.ts:3877`.
- **Downside:** LS is subscription/fixed-product shaped. "Upload any amount" is
  awkward — you model **credit packs** as one-time products ($10/$25/$50/$100
  variants) and act on the `order_created` event (not `subscription_created`).
  Custom arbitrary amounts aren't first-class.

**Recommendation:** **LemonSqueezy with fixed credit packs.** Tax-as-MoR is
worth more to a solo founder than arbitrary-cent precision, the integration is
already there, and OpenAI-style "pick a pack" ($10/$25/$50/$100) is a perfectly
familiar UX. Add a one-time-product variant per pack, switch the webhook to
handle `order_created` → `topUp`, and keep the subscription path dormant (don't
delete — `project_business_model`).

Net change for the front door:
1. New LS one-time products → `YAVER_CREDIT_PACK_*` variant IDs (env, like the
   existing `YAVER_CLOUD_VARIANT_ID` at `http.ts:102`).
2. `POST /billing/credits/checkout {packId}` → LS checkout URL (clone of
   `http.ts:3877`, one-time variant).
3. Webhook `order_created` branch (next to `http.ts:3737`) → resolve user →
   `internal.cloudLifecycle.topUp(userId, packCents)`.
4. Promote `GET /billing/yaver-cloud/balance` (`http.ts:4065`) from **owner-only
   to any authed user**, and retire `topup-dev` (`http.ts:4084`) for production.

## 6. Apple / Google policy — why web top-up is the right call

The user's instinct is correct and the repo already complies.

- **No IAP in the binary today.** `react-native-iap` / `react-native-purchases`
  are in `mobile/package.json` but have **zero imports** — only a comment
  (`mobile/app/(tabs)/console.tsx:112`). No StoreKit entitlement in `app.json`.
- **Why we can sell compute via the web:** Apple's IAP requirement (App Store
  Review Guideline 3.1.1) covers *digital content/services consumed inside the
  app*. **Yaver Cloud is remote IaaS** — real servers running outside the app,
  the same category as the AWS Console app, Hetzner's own app, or a remote-
  desktop client, none of which use IAP. Compute time on a VM is not "in-app
  digital content"; it's an external service. Google Play Billing has the
  analogous carve-out for "physical goods and services rendered outside the app."
- **The safe posture on iOS:** mobile **displays balance** and **controls
  machines** (both fine), and for adding credit it **does not present an in-app
  buy button or an out-link to checkout** (3.1.1 also restricts *linking* to
  external purchase). Instead: show balance + "Manage credits at yaver.io" as
  plain text, or rely on the user topping up on web. Android is more permissive
  and can link out directly. (If you later want an in-app "Add credit" button on
  iOS, use Apple's External Purchase Link entitlement or the multiplatform-
  service exception — but **not required** for launch.)
- **Bottom line:** keep purchase on web, keep mobile to *show balance + drive
  machines*. This is exactly the existing architecture
  (`console.tsx:122` "Provisioning / teardown / billing all live on the web
  dashboard"), with **one deliberate reversal**: we now allow **up/down** (start/
  stop/spin) from mobile, because that's a compute control, not a payment.

## 7. Mobile — balance + up/down (the visible deliverable)

Today mobile never even calls `/subscription` or `/balance` — balance is
invisible on phone. The user explicitly wants it visible and wants up/down from
the phone. Plan:

1. **New `Wallet` surface** (a `more.tsx` entry or a small tab). Data from a
   promoted `GET /billing/yaver-cloud/balance`:
   - Big number: balance in user's currency (cents → display).
   - "~$X/hr running · ~$Y/hr paused · ~Zh left at current burn."
   - "Add credit →" (opens `yaver.io` web on Android; plain pointer on iOS).
   - Recent ledger (from `creditUsage`, `by_user_date` index) — last N charges.
2. **Yaver Cloud machines list** (extend `devices.tsx` or a `cloud` tab):
   - Show managed boxes (`cloudMachines` by user) with status + live cost.
   - **Spin up** button → preflight `canStart` (`cloudLifecycle.ts:202`); if ok,
     `POST /billing/yaver-cloud/start` (or a new `/provision`); if not, "Add
     credit to start (need $N)."
   - **Pause / Stop** → `POST /billing/yaver-cloud/stop`.
   - This is the reversal of the "read-only on mobile" rule — scoped to *compute
     controls only*, money stays on web.
3. **Data pattern:** mobile already auths with a bearer token via
   `mobile/src/lib/auth.ts`; just add fetches to the billing endpoints. No new
   transport.

## 8. Up / down to "Hetzner" (un-branded) — provisioning path

The provisioning spine exists and is already un-branded to the user:

- **Provision:** `cloudMachines.ts:1106` builds a `yaver-{type}-{shortId}`
  server via the agent's Hetzner provisioner (`cloud_provisioners.go:57`,
  vault-token, never from payload). Status machine
  provisioning→booting→…→active (`schema.ts` `provisionPhase`).
- **Down/stop:** stop endpoint → agent snapshots (mandatory,
  `cloud_deploy.go:941`) then deletes the server; row → `stopped`, meter drops
  to stopped-rate. Snapshot is parked (~€0.50/mo) so resume is fast.
- **Resume/up:** start endpoint → `canStart` gate → re-create from snapshot.
- **Scale (size up/down):** `cloud_scale` exists (`mcp_workspace_handlers.go:776`)
  as resize. For credits, expose it as "change plan" and re-derive the per-hour
  rate from the new SKU's COGS. The SKU→Hetzner map is server-side
  (`MACHINE_SPECS`, env override `YAVER_CLOUD_CPU_TYPE` at `cloudMachines.ts:1211`).

The **GPU rental engine** (`desktop/agent/gpu_autoscaler.go`, `gpu_rental.go`,
`gpuRentals` table) is a second, finer-grained meter (Salad/DeepInfra, hourly/
per-token) with a clean provider interface (`GPUBurstBackend`,
`gpu_autoscaler.go:26`). It's the template for *bursty* GPU credit-spend later;
for v1 keep credits on the **cpu/gpu VM** path above and leave GPU-rental as a
follow-on that writes into the same `creditUsage` ledger.

## 9. Money correctness (already sound, keep it that way)

- Convex mutations are **ACID**; `recordUsageAndDeduct` does *insert ledger +
  patch balance in one mutation* (`cloudLifecycle.ts:163-183`) — no torn writes,
  no lost-update race (snapshot isolation).
- **Integer cents end-to-end** (`cloudLifecycle.ts:10`) — no float drift.
- **Ledger is append-only** → every balance move is explainable; never patch a
  `creditUsage` row.
- **Clamp at zero** (`Math.max(0, …)` `:177`) + fail-closed auto-stop means a
  box can't drive the wallet negative.
- Top-up is idempotent-able: key the LS webhook on `order_id` and ignore
  duplicates before calling `topUp` (LS re-delivers webhooks).

## 10. Phased build plan

**P0 — Flip to prepaid (backend, no UI)**
- Markup → `markup(machineType)` env-overridable (§4.1).
- `GET /balance` → any authed user (drop owner gate, `http.ts:4065`).
- New `POST /billing/credits/checkout {packId}` (LS one-time variants).
- Webhook `order_created` → `topUp` (`http.ts` near `:3737`).
- Keep subscription path dormant; keep `dryRun:true`.

**P1 — Mobile balance (read-only)**
- Wallet surface: balance + burn + ledger (§7.1). Pure display; no money in app.

**P2 — Mobile up/down**
- Cloud machines list + spin-up/pause/stop with `canStart` preflight (§7.2).

**P3 — Flip the meter live**
- `dryRun:false` for opted-in users; real Hetzner start/stop; watch margins via
  `creditUsage` (raw vs charged). Add low-balance push notification.

**P4 — Polish**
- FX buffer in COGS basis; per-SKU markup tuning; "scale plan" UI; GPU-rental →
  same ledger.

## 11. Decisions (locked 2026-06-07)

1. **Payment provider: LemonSqueezy (MoR) + fixed credit packs.** LS remits
   global VAT/sales tax; already integrated. Model packs as one-time products
   ($10/$25/$50/$100), act on `order_created` → `topUp`. Keep the subscription
   path dormant, don't delete (`project_business_model`).
2. **Markup: cpu = 2×, gpu = 3×.** Per-SKU, env-overridable (§4.1). GPU's
   fatter multiplier covers its lumpier, pricier COGS.
3. **Mobile up/down: YES.** Reverse the "provisioning is web-only" rule
   (`console.tsx:122`) — scoped to *compute controls* (start/stop/spin/scale).
   **Payment stays web-only** (no IAP, §6).

### Still a business call (not engineering)
- **Launch gate:** repo posture is *free at launch, billing dormant until
  post-YC* (`project_business_model`). Flipping the meter live (`dryRun:false`)
  is that business decision — the P0–P2 build can land fully while still dry-run,
  and P3 flips it when you're ready to charge.

## 14. Security review (public repo — buyers must not be able to cheat)

The repo is open source, so the threat model assumes the attacker has read
every line and can call any HTTP route / public Convex function. Money paths
audited + hardened:

1. **Forged webhook → free credit/boxes (CRITICAL, fixed).**
   `verifyLemonSqueezySignature` used to *fail open* (accept) when
   `LEMONSQUEEZY_WEBHOOK_SECRET` was unset — anyone knowing the public Convex
   URL could POST a forged `order_created` to mint credit, or
   `subscription_created` for a free box. Now **fail-closed**: unsigned
   webhooks are rejected unless `LEMONSQUEEZY_ALLOW_UNSIGNED=true` is explicitly
   set (local dev only). HMAC is constant-time compared.
2. **Pack-amount tampering (fixed).** `order_created` no longer trusts the
   buyer-set `custom_data.pack_id`/amount. It resolves the pack from the
   **signed** purchased variant id (`first_order_item.variant_id`) and refuses
   to credit if the **amount actually paid** (signed `price`/`subtotal`) is less
   than the catalog price. You can never get more credit than you paid.
3. **Top-up/meter primitives are `internalMutation`** (`topUp`, `topUpForOrder`,
   `recordUsageAndDeduct`) — not in the public `api`, so no client can call them
   to self-credit or zero a charge. Idempotent on `orderId` (re-delivered
   webhook can't double-credit).
4. **Wallet/usage reads are `internalQuery`** (`getWallet`, `getRecentUsage`) —
   a public query taking a `userId` arg would let any client read anyone's
   balance/ledger by id. Reads go only through bearer-authed HTTP endpoints that
   derive `userId` from the session.
5. **Provision without paying (fixed/defended).** Real Hetzner create needs the
   platform `HCLOUD_TOKEN` (server secret) AND passes `cloudMachines.provision`'s
   entitlement gate (active subscription OR funded wallet OR owner). No
   client-reachable path provisions a real box for free.
6. **Concurrent over-provision / TOCTOU (fixed).** `canStart` only checks a
   single-box reserve, so concurrent `/provision` calls could fan out N boxes on
   credit for one. The endpoint now requires balance ≥ reserve × (live boxes +
   1) and caps boxes per user (`YAVER_CLOUD_MAX_MACHINES_PER_USER`, default 10).
7. **Cross-tenant actions.** start/stop/provision/decommission all re-check
   `machine.userId === session.userDocId`; balance/usage are session-scoped.
8. **Integer cents end-to-end, clamp at 0** — no float drift, balance can't go
   negative; fail-closed auto-suspend before the floor.

Residual / accepted: `api.cloudMachines.create` is a public mutation taking a
`userId` (pre-existing, parallel-session-owned) — callable directly, but it
cannot provision a real box without the entitlement gate + platform token, so it
yields at most an error-status row (griefing, not free compute). Tracked, not a
money cheat. Convex-client public-query enumeration follows the codebase-wide
pattern for non-money tables (machines/subscriptions); money tables are tightened
above.

## 15. Bring-your-own Hetzner (use without paying us)

Two fully separate planes — no code shared between billing and BYO:

- **Managed plane (paid):** `cloudMachines` rows, platform `HCLOUD_TOKEN`,
  prepaid wallet + meter. This is everything in §1–§14.
- **BYO plane (free to us):** the user stores *their own* provider token in the
  encrypted on-device vault and provisions on *their* account. Already shipped
  at the agent level:
  ```bash
  yaver account connect hetzner            # or MCP account_connect {provider:"hetzner", fields:{"token":"..."}}
  yaver ops cloud_provision host=hetzner … # builds on THEIR Hetzner account
  # or remote_provision / remote_destroy / remote_cost (desktop/agent/remote.go)
  ```
  Token via `accountField(ProviderHetzner,"token")` — **never** the platform
  token, **never** the wallet. BYO boxes live in the agent's local store, not
  `cloudMachines`, so the meter never sees them; and as a belt-and-suspenders
  guard `listMeterableMachines` bills only `origin === "managed"` rows.

So a user can run entirely on their own Hetzner (or DigitalOcean) and pay us
nothing. The prepaid wallet is purely for the *managed* convenience tier.
Follow-up (not in this build): a first-class in-app "Connect your Hetzner"
button; today it's CLI/MCP.

## 16. BYO Hetzner full lifecycle from mobile (BUILT 2026-06-07)

First-class in Settings → "Bring your own cloud" (`CloudProvidersSection.tsx`):

- **Connect** Hetzner / DigitalOcean — paste API token, stored AES-256-GCM on
  the agent vault, never synced to Convex, never rendered back (leak-safe input:
  secureTextEntry + autofill/autocorrect/spellcheck off; token wiped from state
  right after the authed POST).
- **Spin up** a box on the user's OWN account (`cloud_provision` ops verb,
  `cloud_byo_provision.go`): plan/region picker, ~€/hr shown, optional **prebuilt
  image id** (fast boot) and optional **git repo** to shallow-clone on first boot
  (`git clone --depth 1`, URL validated + shell-quoted). They pay Hetzner
  directly — the wallet/meter is NOT involved.
- **Stop to save cost / Start** (`cloud_stop` / `cloud_start`, `cloud_stopstart.go`):
  stop = snapshot (recover-safe) then delete the server so hourly billing halts;
  the stopped box shows up under "Stopped boxes (snapshots)" and Start recreates
  it from the snapshot. List/Delete running servers too (`cloud_list`/`cloud_destroy`).

**Safety switch:** all real BYO mutations (provision/stop/start) fire only when
`confirm=true` AND `YAVER_CLOUD_STOPSTART_LIVE=1` on the agent — one flag enables
the whole lifecycle together, so a user can never create a real billing box they
can't then stop. Without it: safe dry-run previews everywhere. All BYO verbs use
the caller's OWN vault token (`accountField(ProviderHetzner)`) — never the
platform token, never the payload; a self-destruct guard blocks deleting the box
the agent runs on. Private-repo clones need creds pushed via `git_push_creds`.

Follow-up: make the MANAGED provision path boot from the prebuilt Yaver image for
fast first-boot (touches the parallel-owned `cloudMachines.ts`); in-app streaming
bring-up progress.

## 12. Anti-goals / guardrails

- Don't delete the dormant subscription / `managedRelays` paths
  (`project_business_model`).
- Don't ship a hardcoded user/device/provider name into user-facing copy
  (`feedback_yaver_is_for_everyone`) — "Yaver Cloud," never "Hetzner."
- Don't put provider tokens, server IPs, or COGS internals into Convex payloads
  (`convex_privacy_test.go`); credit *counters* are fine and already whitelisted.
- Don't add Apple/Google IAP SDK wiring — web top-up only.
