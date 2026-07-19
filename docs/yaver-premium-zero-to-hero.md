# Yaver Premium — the normie's zero-to-hero managed cloud

> **Status: design + metering-spine only. NOT launched. NOT public.**
> Everything below is `dryRun`-defaulted and fail-closed, joining the
> existing dormant managed-cloud posture (`cloudLifecycle.ts`,
> `project_business_model`). Go-live for any meter is a single Convex env
> flip, never a code change. Free / BYO developers are unaffected.

## 0. The rule (the whole business in one line)

> **Manage your own infra → Yaver is free. Want Yaver to manage it → you
> pay metered, and Yaver takes the arbitrage.**

The free/paid line is **who operates the infra**, not which features you
get. Run your own box (physical or your own cloud) and BYO keys → free,
forever, full product. Hand the operating burden to Yaver — compute,
inference, backend, web, publishing — and each managed service is metered
into one prepaid wallet at a markup over Yaver's COGS. The normie always
chooses "Yaver manages it" because he can't operate any of it himself;
the developer chooses free because he can. Same product, two operating
models, one clean line between them.

## 1. The one sentence

A non-technical person installs the Yaver app, loads money once on the
web, and Yaver silently rents them the **entire** software stack —
compute, AI, backend, web hosting, and App-Store/Play publishing —
metered into a single prepaid balance, while Yaver earns the spread on
every layer. He never learns the words *GLM*, *Hermes*, *git*,
*Hetzner*, *Convex*, or *Cloudflare*.

**The ignorance is the product.** A developer can price-shop every
layer; the normie can't, because he doesn't have the vocabulary to route
around any of it. That opacity, wrapped in "it just works," is the moat
and the margin.

## 2. Who it's for (and who it isn't)

- **Premium (this doc):** someone who wants an app to exist and has money
  but no stack knowledge. Mobile-app-only feel. Web is only ever touched
  to top up the wallet.
- **Free / BYO (unchanged):** developers keep the existing free path —
  BYO subscription (Claude/Codex via the OAuth mirror), BYO API keys,
  BYO box. Premium is a *parallel lane*, not a paywall.

## 3. The five meters, one wallet

The wallet (`prepaidCredits`) already exists. The original **compute**
meter (`creditUsage` / `cloudLifecycle.recordUsageAndDeduct`) is
SKU-specific and predates everything. The other four meters all append
to the new generic ledger (`managedUsage`) and debit the **same wallet**
via `managedMeter.recordManagedUsage`. Adding a meter = a new `kind`
string + a markup default. No new table, no new wallet.

| # | Meter | `kind` | Upstream (COGS) | Markup default | Why the user can't shop around |
|---|---|---|---|---|---|
| 1 | **Compute** | `compute` | Hetzner cpx42 (~4.1¢/hr) | **2×** | Doesn't know what a VPS is |
| 2 | **Inference** | `inference` | GLM-4.6 / OpenRouter / DeepInfra tokens | **1.5×** | Reference price in his head is Claude/ChatGPT (7-10× pricier) |
| 3 | **Backend** | `backend` | Convex usage (reads/writes/storage) | **2×** | Doesn't know a backend exists |
| 4 | **Web** | `web` | Cloudflare (requests/bandwidth/builds) | **2×** | "My app has a website" is all he sees |
| 5 | **Publish** | `publish` | Mac-farm build-minutes + ASC/Play fees | **1.3×** | Apple/Google enrollment is a black box to him |

Markups are env-tunable per kind (`YAVER_MANAGED_MARKUP_<KIND>`). The
inference spread is deliberately the lightest because GLM-class tokens
are already ~7-10× cheaper than the user's mental anchor — even 1.5×
reads as "cheap" while still earning. The thicker compute/backend/web
markups ride on the user having no reference price at all.

### Why arbitrage, not flat subscription

A flat sub leaks margin on heavy users and overcharges light ones. A
metered wallet with per-layer markup:
- captures the spread on *every* unit consumed, on every layer;
- self-throttles abuse (wallet hits zero → `suspend` → cut off);
- makes the upsell a debit, not a new purchase (see §6).

## 4. What exists vs. what's the gap (grounded in code)

**Already built (dormant, verified):**
- Wallet + compute ledger + 2×/3× markup + min-reserve floor +
  hourly meter (`cloudLifecycle.ts`; cron wired via Hetzner
  systemd → `POST /crons/run {name:"cloudMeter"}`, `http.ts:6106`).
- Hetzner provision / snapshot-pause / resume (`HCLOUD_TOKEN`-gated,
  dry-run without).
- Credit-pack web checkout + idempotent LemonSqueezy webhook top-up.
- Zero-touch DPP provisioning (mint→claim→attest→auto-setup).
- On-phone GLM agentic coding (clone/edit/commit in Hermes).
- Runner provider env-injection (`provider_keys.go`) — can point any
  runner at a custom `OPENAI_BASE_URL`.
- Privacy contract + tests over all of it.

**Built THIS session (the unifying spine):**
- `managedUsage` table (`schema.ts`) — generic per-`kind` ledger.
- `managedMeter.recordManagedUsage` — single debit path for meters 2-5.
- Privacy-test pin extended to the new fields.

**Still to build (the real work, in order of leverage):**
1. **Yaver Gateway** (inference) — be a captive OpenRouter.
2. **Convex proxy** (backend) — resell managed backend.
3. **Cloudflare proxy** (web) — resell managed web.
4. **Mac-farm publish** (App Store / Play) — the zero-to-hero finale.
5. **Single-balance wallet UX** + web top-up + low-balance nudge.

## 5. The architecture of each new meter

### 5.1 Inference — the "Yaver Gateway"

The biggest, stickiest margin. **Do NOT reuse the OAuth mirror** — it is
subscription-token-only and `subscriptionStore.ts` actively rejects API
keys, because reselling a subscription CLI token violates Anthropic/
OpenAI terms. Legitimate arbitrage = per-token API resale (the
OpenRouter model), which z.ai / DeepInfra / OpenRouter all permit.

```
phone (Hermes GLM loop)  OR  managed box runner (OPENAI_BASE_URL=gateway)
        │  POST /v1/chat/completions  (Bearer = Yaver session token)
        ▼
Yaver Gateway   ← Cloudflare Worker or relay (Go). NOT a Convex httpAction
   • session token → userId
   • wallet balance > 0 ? else 402
   • route cheapest-capable: GLM-4.6 ▸ DeepSeek ▸ Qwen ▸ DeepInfra
   • stream tokens to client
   • on stream end → ctx.runMutation(managedMeter.recordManagedUsage,
        { kind:"inference", provider, model, unit:"token",
          quantity:tokens, providerCostCents:cost })
        ▲
   upstream provider key — server-side ONLY, never on device
```

- **Phone change is tiny:** `sandboxBinding.ts` already builds a GLM
  config from a device key; add a "managed" mode where `baseURL =
  gateway` and `apiKey = session token`. The Hermes loop is unchanged —
  it already speaks OpenAI-compatible.
- **"Provision a key for him"** collapses to nothing: there is no key to
  mint, leak, or rotate — *his wallet is his key*.
- **Privacy:** gateway sees prompts; Convex must NEVER. Only
  `{userId, model, tokens, cents}` is recorded — obeys
  `convex_privacy_test.go` (no `output`/`stdout`/prompt text).
- **Abuse guard:** per-request token ceiling + per-hour cap + wallet
  floor; a runaway agent loop can't drain the balance unbounded.
- **Multi-provider from day one** — it's also the hedge against any one
  upstream changing pricing or cutting you off.

### 5.2 Backend — Convex proxy

Resolves the current phone-only limit ("`convex deploy` needs a
machine"). Yaver provisions the user's app backend on a Yaver-owned
Convex deployment (or a pooled one, namespaced) and proxies it.

- Yaver runs `convex deploy` on the user's behalf on the Mac-farm /
  managed box — the normie never sees the CLI.
- Meter Convex function calls / storage / bandwidth → `kind:"backend"`.
- `ref` = deployment id (non-secret).
- Reuses the existing managed-toggle seam (`managed.storage`,
  schema-level toggles already designed for "Yaver hosts it").

### 5.3 Web — Cloudflare proxy

Same shape, one layer up. Yaver deploys the user's web app to a
Yaver-owned Cloudflare account (or per-user Pages project), fronts it at
`<slug>.yaver.app`, and meters requests/bandwidth/build-minutes →
`kind:"web"`. The 15 MB deploy-size guard (CLAUDE.md) and
`@opennextjs/cloudflare` + `wrangler` path already exist in
`scripts/deploy-web.sh`.

### 5.4 Publish — the Mac-farm finale (App Store / Play)

iOS publishing **requires a Mac** and signing identities the normie will
never have. This is exactly the dormant Mac-farm work
(`project_publish_macfarm`: `/deploy/ship` spine + `publishJobs` async
queue + `yaver shots` for screenshots/submit). The normie taps
**"Publish my app"**; Yaver:

1. Builds on a Yaver-managed Mac (TestFlight) / Linux (Play AAB) — meter
   build-minutes → `kind:"publish"`.
2. Handles signing under Yaver's managed Apple/Google identities, OR
   guides the user through their own enrollment (see §7).
3. Generates screenshots + metadata (`yaver shots --submit`).
4. Uploads to ASC / Play, surfaces status back to the phone.

This is the literal "zero to hero" payoff: he started with an idea and a
credit card; he ends with an app in the store, never having opened a
terminal.

## 6. The zero-to-hero journey (deep onboarding)

The whole arc must *feel* like one mobile app. Every layer below is
invisible plumbing surfaced only as plain-language outcomes.

**Stage 0 — Install & sign in.** App Store install → native OAuth. No
survey friction; land straight in "What do you want to build?"

**Stage 1 — Describe the app (free, no wallet yet).** He types/voices an
idea. The on-phone GLM loop (or gateway in managed mode) scaffolds a
project on-device — clone/edit/commit in Hermes. He sees a live preview.
*Cost so far: ~nothing.* This is the hook before any money.

**Stage 2 — "Load credit to keep going."** First real compute/inference
need → a soft wall: *"Add $20 to keep building."* Tap → opens the **web**
top-up (Apple-safe; see §8) → wallet funded → returns to the app. One
balance, shown as a friendly number, never as line items.

**Stage 3 — Turn on the backend.** App needs data → *"Turn on your app's
brain ($X/mo from balance)."* One tap → Convex proxy provisions →
`kind:"backend"` meter starts. He never hears "Convex."

**Stage 4 — Put it on the web.** *"Give your app a website."* One tap →
Cloudflare proxy deploys → `<slug>.yaver.app` → `kind:"web"` meter. He
never hears "Cloudflare."

**Stage 5 — "This needs a computer."** For heavy builds / real CLIs →
*"Turn on your Yaver computer ($X/hr from balance)."* One tap →
DPP-style auto-provision of a managed box (step 0: Hetzner as-is) → box
appears, already claimed. The upsell is a **wallet debit, not a new
purchase.**

**Stage 6 — Ship to the stores.** *"Publish my app."* → Mac-farm build +
sign + screenshots + upload (§5.4, §7). Status streams back to the phone
("Apple is reviewing your app ✅").

**Stage 7 — Live.** App is in TestFlight / Play. He tops up the wallet
from the web whenever the balance runs low. Done. Hero.

Onboarding principles, throughout:
- **No vocabulary leaks.** Outcomes, never infra nouns.
- **Money is one number.** Never show "Hetzner + tokens + Convex +
  Cloudflare"; show "$14.20 left, ~6 days at your pace."
- **Every wall is a one-tap debit**, framed as enabling a capability.
- **Push notifications** carry status the phone can't poll (build done,
  app approved) — reuse the pairing-notification path (Phase 2 onboarding
  work).

## 7. App Store / Play onboarding — the deep part

Two enrollment models; offer both, default to whichever the user can
stomach:

- **Managed identity (lowest friction):** Yaver publishes under Yaver's
  Apple Developer + Google Play accounts (Yaver as the listed
  publisher, or a managed sub-account). The normie never enrolls. Meter
  the $99/yr Apple + $25 Play as amortized `kind:"publish"` line items.
  *Caveat:* store policy on third-party-managed listings is the gating
  risk — validate before promising.
- **Guided own-account (cleanest ownership):** a wizard walks him through
  Apple Developer enrollment + Play Console signup on the web, then
  Yaver holds the API keys (ASC `.p8`, Play service-account JSON) in his
  vault and does all uploads. He owns the listing; Yaver does the work.

Either way the **build + sign + screenshot + upload** machinery is the
existing Mac-farm / `deploy-testflight.sh` / `upload-playstore.py` /
`yaver shots` stack, run on Yaver hardware. The normie's entire
contribution is tapping "Publish" and answering a few plain-language
prompts (app name, icon, one-line description).

## 8. Billing — Apple-safe by design

**Decision (current): sell flat infrastructure subscriptions on the web.**

- The app is positioned as a **developer / infrastructure tool**, not a
  consumer content app. Money buys **cloud infrastructure** (compute,
  hosting, build minutes) — a real metered service.
- Subscription purchase, cancellation, and payment updates happen on the web,
  outside the app. Mobile may show entitlement/status and route users to web,
  but it must not call checkout, portal, cancel, or plan-change APIs.
- This avoids Apple/Google IAP for remote infrastructure while keeping the app
  free of purchase flows.
- Public credit-pack checkout and prepaid workspace provisioning are retired.

> Risk flag: Apple's line between "infra tool" and "digital content
> consumed in-app" is fuzzy and enforced unevenly. Keep the in-app copy
> infra-framed, keep top-up strictly web-side, and don't deep-link to
> checkout from inside the app for the spend flow.

## 9. Roadmap

- **Step 0 (now, testing):** keep BYO-Hetzner as-is; exercise the
  managed-cloud compute meter end-to-end on real Hetzner with the
  owner-bypass. Nothing public.
- **Step 1:** Yaver Gateway (inference) → flip `inference` meter live in
  private preview.
- **Step 2:** Convex proxy (backend meter).
- **Step 3:** Cloudflare proxy (web meter).
- **Step 4:** Mac-farm publish flow + store onboarding.
- **Step 5 (post-testing):** **remove BYO-Hetzner setting** — Yaver
  Managed Cloud becomes the only box path for Premium. Single managed
  path = full arbitrage capture, zero config surface.
- **Launch:** flip `YAVER_CLOUD_METER_LIVE` + `YAVER_MANAGED_METER_LIVE`,
  set LemonSqueezy variant IDs, open `YAVER_CLOUD_PUBLIC`. One env flip
  per meter; no code change.

## 10. Risks (the ones that actually bite)

1. **Apple IAP framing** (§8) — mitigated by infra positioning + web-only
   top-up, but enforcement is uneven. The single biggest external risk.
2. **Never resell subscription tokens** — terms-poisoned and already
   fenced in code (`subscriptionStore.ts`). Arbitrage = per-token API
   only. Keep the fence.
3. **Gateway runaway spend** — per-request + per-hour + wallet-floor
   ceilings are mandatory before any meter goes live.
4. **Privacy contract** — proxies/gateway see prompts, code, traffic;
   Convex stores only counters. One stray log into a synced table breaks
   `convex_privacy_test.go`. The only Convex call from the edge is the
   debit mutation.
5. **Upstream concentration** — if GLM/z.ai is the whole inference
   margin and they change terms, it evaporates. Multi-provider routing is
   both the margin lever and the hedge.
6. **Managed store identity** — third-party-managed App Store/Play
   listings may violate store policy; validate the managed-identity model
   before promising it, fall back to guided-own-account.

## 11. What changed in code this session

- `backend/convex/schema.ts` — new `managedUsage` table (generic
  per-`kind` reseller ledger, counter/label-only).
- `backend/convex/managedMeter.ts` — `recordManagedUsage` internal
  mutation: the single debit path for the inference/backend/web/publish
  meters; markup-per-kind; `dryRun`-defaulted; same wallet as compute.
- `desktop/agent/convex_privacy_test.go` — pinned the new field names as
  counter/label-only (test passes).

All additive, all dormant, all fail-closed. Nothing deployed, nothing
committed. The metering *spine* now exists; the gateway and proxies plug
into it.
