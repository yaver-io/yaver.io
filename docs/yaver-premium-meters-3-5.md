# Yaver Premium — meters 3–5 (backend, web, publish)

> The remaining managed-PaaS meters after compute (1) and inference (2).
> All three report through the SAME spine: `recordManagedUsage` via the
> secret-guarded `POST /gateway/meter` route, discriminated by `kind`.
> No new tables, no new wallet. Design only — none built yet.

## The shared shape (already in place)

The metering spine built for inference is generic. Any managed service
becomes a meter by POSTing to `/gateway/meter` (gateway-secret authed)
with its `kind`:

```
reporter ──POST /gateway/meter (Bearer GATEWAY_SHARED_SECRET)──▶ Convex
  { userId, kind, provider, unit, quantity, providerCostCents, ref }
        └─▶ recordManagedUsage → chargedCents = ceil(cost × markup(kind))
            → append managedUsage row → debit prepaidCredits wallet
```

So building meters 3–5 means building three **reporters** (things that
compute usage and call the route) + the **provisioning/proxy** each
service needs + the **UX**. The debit path is done.

Markups (env-tunable `YAVER_MANAGED_MARKUP_<KIND>`), defaults from
`managedMeter.ts`: `backend` 2×, `web` 2×, `publish` 1.3×.

Per-user opt-in: `managedMeter` gates real (non-dryRun) charges on
`userSettings.managedServices[kind] === true` — a user is only billed for
real on a meter kind they explicitly enabled (defense-in-depth on top of
the global `YAVER_MANAGED_METER_LIVE` flag).

---

## Meter 3 — managed backend (Convex proxy) · `kind:"backend"`

**Goal:** the normie's app gets a backend without him knowing what Convex
is, and **this is what kills the phone-only "can't `convex deploy`" wall**
from the zero-to-hero flow.

**Provisioning.** Yaver owns the Convex org; each user app maps to a
Yaver-managed Convex deployment (or a namespaced pooled one). Yaver runs
`convex deploy` on the user's behalf from a managed box / Mac-farm runner
— the user never sees the CLI. Store the deployment id in `managedUsage.ref`
(non-secret) and the user→deployment mapping in a lifecycle table
(counter/id/timestamp only, like `cloudMachines`).

**Metering.** Convex exposes usage (function calls, action compute,
database bandwidth, storage GB-months) via its team/usage API. A reporter
(cron on the Hetzner cron box, same pattern as `cloudMeter`) pulls
per-deployment usage daily and calls `/gateway/meter`:

```
kind: "backend", provider: "convex", unit: "function-call" | "storage-gb-mo" | "bandwidth-gb",
quantity: <delta since last tick>, providerCostCents: <Convex COGS for that delta>, ref: <deploymentId>
```

Emit one row per unit type per tick so the wallet ledger is auditable.
Carry sub-cent deltas the same way the gateway does (accumulate, emit
whole cents) — or simply meter daily where deltas are already ≥1¢.

**Pricing.** Map Convex's published per-unit pricing → `providerCostCents`.
Keep the rate table in the reporter (like `gateway/src/pricing.ts`), not
in Convex.

**Privacy.** Never copy app data, schema, or function names into
`managedUsage` — only counts + deploymentId. The privacy contract already
forbids the dangerous fields.

**Open risks.** Convex multi-tenant billing under one org; usage-API
granularity per deployment; whether pooled vs per-user deployments. These
are provisioning decisions, not metering ones — the meter is trivial.

---

## Meter 4 — managed web (Cloudflare proxy) · `kind:"web"`

**Goal:** "give my app a website," one tap, no mention of Cloudflare.

**Provisioning.** Deploy the user's web build to a Yaver-owned Cloudflare
account — a per-user Pages project (or Workers route) fronted at
`<slug>.yaver.app`. Reuse the existing `@opennextjs/cloudflare` + wrangler
path from `scripts/deploy-web.sh` and the 15 MB size guard. Build runs on
the Mac-farm / managed runner; `ref` = Pages project name.

**Metering.** Cloudflare exposes per-zone/per-project analytics (requests,
bandwidth, build minutes) via the GraphQL Analytics API. A reporter cron
pulls deltas and calls `/gateway/meter`:

```
kind: "web", provider: "cloudflare", unit: "request" | "bandwidth-gb" | "build-min",
quantity: <delta>, providerCostCents: <CF COGS>, ref: <pagesProject>
```

**Pricing.** Cloudflare Pages/Workers free tiers are generous; meter the
paid overage + build minutes. The markup (2×) rides on the user having no
reference price for "a website."

**Open risks.** Per-user project isolation vs one zone with subdomains;
custom domains (user brings a domain → DNS automation, already partly in
`userDomains`); DDoS/abuse on a shared account (Cloudflare WAF / Attack
Mode). Provisioning concerns; metering is trivial.

---

## Meter 5 — publish to App Store / Play · `kind:"publish"`

**Goal:** the zero-to-hero finale — "Publish my app" → it lands in
TestFlight / Play, the normie never opening Xcode or a terminal.

**This reuses the dormant Mac-farm work** (`project_publish_macfarm`:
`/deploy/ship` spine + `publishJobs` async queue + `yaver shots` for
screenshots/submit). Don't build a third publish engine.

**Flow.**
1. Build on a Yaver-managed Mac (iOS archive → TestFlight) / Linux (Play
   AAB). Meter wall-clock build-minutes → `kind:"publish"`,
   `unit:"build-min"`.
2. Sign under a managed Apple/Google identity OR the user's own (guided
   enrollment; Yaver holds the ASC `.p8` / Play service-account JSON in
   the user's vault and uploads on their behalf). See zero-to-hero §7.
3. Screenshots + metadata via `yaver shots --submit`.
4. Upload to ASC / Play; stream status back to the phone via push.

**Metering.**
```
kind: "publish", provider: "macfarm", unit: "build-min",
quantity: <minutes>, providerCostCents: <Mac-farm COGS for those minutes>, ref: <publishJobId>
```
Optionally amortize the $99/yr Apple + $25 Play fees as separate
`unit:"membership"` rows when using a managed identity.

**Pricing.** Mac-farm COGS = (machine $/hr ÷ 60) × build-minutes. Markup
1.3× — thin, because the value here is the *convenience and the
unblock*, not the compute.

**Open risks (the real ones).**
- **Store policy on third-party-managed listings.** Apple/Google may
  reject Yaver publishing under its own identity for users at scale.
  Validate the managed-identity model before promising it; the
  guided-own-account path is the safe fallback.
- **TestFlight upload rate limit** (~15–20/app/day) — already a known
  constraint (CLAUDE.md). A shared managed identity multiplies this
  pressure; per-user own-accounts sidestep it.
- iOS builds are local-only by design (UDID/keychain) — the Mac-farm is
  the answer, but it's real hardware to operate.

---

## Build order recommendation

1. **Backend (meter 3) first** — it unblocks the phone-only deploy wall,
   the most-felt limitation in the normie flow, and Convex is the easiest
   to provision (Yaver already lives there).
2. **Web (meter 4)** — reuses `deploy-web.sh`; mostly provisioning glue.
3. **Publish (meter 5)** — highest payoff but highest external risk
   (store policy + Mac-farm ops); do it last, lead with guided-own-account.

Each is: provisioner + a cron reporter (clone `cloudMeter`'s
external-timer pattern) + a one-tap UX card (clone `ManagedCloudCard`).
The wallet, markup, opt-in gate, and privacy contract are already done.
