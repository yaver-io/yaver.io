# Yaver for the Normie: Web Concierge + À-la-carte Fair Metering

**Status: design-only, 2026-06-08.** Grounded against code at `41875ce5`
("yaver-premium: managed-cloud metering spine + inference gateway (dormant)").
Builds directly on, and partly re-frames, three existing docs:
`yaver-premium-zero-to-hero.md`, `yaver-cloud-credits-design.md`,
`yaver-social-invite-to-code.md`.

Markdown drifts; the wallet/meter facts below were re-verified in the source.
If a constant here disagrees with `cloudLifecycle.ts` / `managedMeter.ts` /
`schema.ts`, the code wins — fix this doc in the same change.

---

## 0. The normie this doc is about (different from the others)

The Premium and Cloud-Credits docs assume a **phone-first** normie: installs the
mobile app, codes on-device with GLM in Hermes, never sees a terminal.

This doc is about a **prosumer normie** — the one in your prompt:

> has **Claude Code (or Codex) in a terminal** AND **Yaver in a browser tab**.

He can run a terminal and let an AI agent write code. He cannot assemble the
infrastructure that turns code into a running, installable, shippable app:
Xcode + signing + TestFlight, a Convex account + `convex deploy`, a Cloudflare
account + `wrangler`, env vars, the 15 MB bundle guard, provisioning profiles.

That boundary — **"the code is written; now what?"** — is where every normie
drowns, and it is exactly the boundary Yaver already automates via the agent,
the Mac-farm, the deploy-script generator, and the Hermes container. So the
product is clean:

> **Claude Code / Codex writes the code (his keys, ~free). Yaver sells the last
> mile — the operational toil that needs accounts, machines, signing identities,
> and toolchains he can't stand up himself.**

This is a *fairer* framing than "ignorance is the product" (Premium §1). We are
not charging him because he can't price-shop. We are charging him because we did
the work he genuinely cannot do. See §6.

---

## 1. Division of labor: terminal codes, web guides, phone previews

Three surfaces, three jobs. Don't collapse them.

| Surface | Job | Who acts |
|---|---|---|
| **Terminal (Claude Code / Codex + Yaver MCP)** | Write code, run agent actions (build, deploy, git) via the `ops` grand-tool | The AI agent, driven by him |
| **Web (Yaver cockpit/concierge)** | Guidance, money, the capability shelf, consent/approvals, live status | The human |
| **Phone (Yaver mobile container)** | See his app run, instantly, via Hermes reload | The human, passively |

The agent already has the Yaver MCP `ops` tool (84 verbs incl.
`cloud_provision`, deploy, git, build). So the *agent* can do the work; the
**web cockpit is the human's seat** — where he sees what the agent is about to
spend, taps "yes, do it," watches it happen, and tops up the wallet. The web UI
talks to the local agent (`localhost:18080`) and reads Convex for wallet/status.

**Why this split is the whole trick:** the normie never types an infra command.
He says "ship it" to Claude Code; the agent calls `ops deploy`; Yaver routes the
build to the Mac-farm; the web cockpit shows "Apple is reviewing your app ✅"
and "this used $0.42 of build time." The terminal is the engine, the browser is
the dashboard, the phone is the windshield.

---

## 2. Where the suffering actually is (ranked) → which meter is fair

A normie's pain is `severity × frequency × can't-DIY`. Ranking the boundary
crossings, with the meter each maps to and where Yaver's margin is *fair* (toil
removed, defensible markup, BYO exit always open):

| # | Suffering | Can he DIY? | Frequency | Maps to meter | Yaver COGS | Fair markup | Why fair |
|---|---|---|---|---|---|---|---|
| 1 | **iOS build + sign + TestFlight/App Store** | **No** (needs Mac + Xcode + $99 Apple acct + profiles) | Per release | `publish` | Mac-farm build-min + amortized $99/$25 | 1.3× + amortized fees | He literally cannot; this is pure toil-removal |
| 2 | **See app on his own phone (Hermes reload)** | iOS: no. Android: yes (self-compile) | **Many/day** | `compute` (build-native min) on a **pooled build box** | fractions of ¢/build | 2× | Tiny absolute cost, huge value, daily hook |
| 3 | **Backend (Convex) deploy + run** | No (acct, CLI, env, schema) | Per change + runtime | `backend` | Convex usage | 2× | Standard managed-hosting margin (Render/Railway tier) |
| 4 | **Web (Cloudflare) deploy + host** | No (acct, token, wrangler, 15 MB guard) | Per change + runtime | `web` | Cloudflare req/bw/build | 2× | Same; Vercel/Netlify mark up 2–5× |
| 5 | **Always-on agent box (run Claude Code/Codex remotely, drive from phone)** | Partially (could keep laptop on) | Continuous | `compute` (managed box) | Hetzner ~4.1¢/hr | 2× | He's renting convenience; BYO-Hetzner stays free |
| 6 | **Inference tokens** | **Yes — bring own key (default)** | Continuous | `inference` | GLM/DeepSeek/Qwen per-token | 1.5× | **Only** if he uses Yaver's gateway instead of his own key |

**Two conclusions fall straight out of this table:**

- **Start him at #2 (Hermes reload), monetize the hero at #1 (iOS publish).**
  Reload is the cheapest possible first debit (a pooled build farm bills
  build-minutes, not a whole box) and delivers the "I see my app on my phone!"
  moment that hooks him. iOS publish is the thing he'd otherwise pay a freelancer
  $100s for — that's where real money is fair.
- **Inference is the meter you should be *least* eager to push.** His own
  Claude Code/Codex key is cheap and already in his terminal. Charging him a
  1.5× markup on tokens when he already has a key is the *least* fair meter and
  the easiest to feel ripped off by. Keep BYO-key the default; offer the gateway
  only as "I don't have a key / run it on the box for me" convenience.

---

## 3. The à-la-carte principle (the core new design)

Your prompt is explicit: *"he can use one or more — Hermes reload only, or that
plus Convex + Cloudflare, or set Claude Code/Codex into Yaver managed cloud."*

So the meters must be **independently opt-in, composable, one tap each**, each
its own clear value proposition. The wallet is shared; the *capabilities* are à
la carte. He climbs the ladder only as he hits a wall — never pre-paying for a
layer he doesn't need yet.

### 3.1 The gap: per-user opt-in above the global env flags

Today live/dry is a **global** switch (`YAVER_CLOUD_METER_LIVE`,
`YAVER_MANAGED_METER_LIVE`). À-la-carte needs a **per-user** "which capabilities
has *this* user turned on" set. That's the one genuinely new piece of state.

Reuse the existing seam, don't invent a parallel one. `userSettings.managed`
(`schema.ts:790`) already holds per-user managed-subsystem booleans
(`relay/dns/storage/llm/...`). Add a sibling **`managedServices`** object keyed
by the meter `kind`:

```ts
// schema.ts — userSettings
managedServices: v.optional(v.object({
  reload:    v.optional(v.boolean()), // Hermes build-native on pooled farm  → compute
  backend:   v.optional(v.boolean()), // Convex proxy                        → backend
  web:       v.optional(v.boolean()), // Cloudflare proxy                    → web
  agentBox:  v.optional(v.boolean()), // always-on Claude Code/Codex box     → compute
  inference: v.optional(v.boolean()), // Yaver gateway (only if no own key)  → inference
  publish:   v.optional(v.boolean()), // Mac-farm App Store / Play           → publish
})),
```

Effective live-charge for a user/kind = `globalMeterLive(kind) AND
user.managedServices[kind] === true AND wallet.balance > 0`. This keeps the
fail-closed launch posture (global flag still gates everything) **and** gives
each normie an independent on-switch per capability. Meter routing in
`recordManagedUsage` / `recordUsageAndDeduct` reads this before charging; if a
kind is off for the user, it stays `dryRun` for him regardless of global flag.

### 3.2 The capability shelf (web cockpit)

The web cockpit renders one card per capability — the à-la-carte menu:

```
┌─────────────────────────────────────────────────────────────┐
│  Your app's capabilities                    Balance: $14.20  │
├─────────────────────────────────────────────────────────────┤
│  📱 See it on my phone (live reload)        ● ON   ~$0.01/build│
│     Compile + push to your phone instantly. No Xcode.         │
├─────────────────────────────────────────────────────────────┤
│  🧠 App backend (data, auth, APIs)          ○ OFF  ~$X/mo     │
│     Turn on your app's brain. We run + deploy it for you.     │
├─────────────────────────────────────────────────────────────┤
│  🌐 Website for my app                      ○ OFF  ~$X/mo     │
│     Give your app a public web address. We host it.          │
├─────────────────────────────────────────────────────────────┤
│  💻 Always-on coding computer               ○ OFF  ~$0.08/hr  │
│     Run Claude Code/Codex in the cloud; drive from anywhere. │
│     ↳ Use my own AI key (free) ▸ or Yaver's (metered)        │
├─────────────────────────────────────────────────────────────┤
│  🚀 Publish to the App Store / Play         ○ OFF  per release│
│     We build, sign, screenshot, and submit. The hero button. │
├─────────────────────────────────────────────────────────────┤
│  Each is independent. Turn on only what you need.            │
│  Prefer to run it yourself? Connect your own cloud → free.   │
└─────────────────────────────────────────────────────────────┘
```

Each toggle flips `managedServices[kind]` for him. Tapping ON when off-budget
shows the soft wall ("Add credit to enable"). The "Connect your own cloud →
free" link is the BYO exit, load-bearing for fairness (§6).

---

## 4. The time=0 → hero journey (terminal + web + phone)

Concrete, surface-by-surface. Free until the first capability wall, every wall a
one-tap debit.

**t=0 — Install & connect (free).**
`npm i -g yaver-cli && yaver auth`. Open the web cockpit. The cockpit detects:
agent online (localhost:18080), no project yet. It wires the Yaver MCP into his
Claude Code / Codex (writes the MCP server entry; Claude Code reads it from the
MCP registry). Now his terminal agent can call `ops` verbs.

**t=1 — Describe & scaffold (free).**
Cockpit: "What do you want to build?" → hands the prompt to Claude Code/Codex in
his terminal (or runs `yaver autoinit` / project_wizard). Agent scaffolds the
project **locally on his machine**. *Cost: $0 — his keys, his disk.* This is the
hook-before-money, same as Premium Stage 1 but terminal-driven.

> **Gap:** `project_wizard.go` / `autoinit` are CLI-only today (exploration
> confirmed). The cockpit needs a thin web trigger that POSTs the wizard answers
> to the local agent. Small build; the wizard engine exists.

**t=2 — See it on my phone (FIRST debit, tiny).**
He wants to see it. Cockpit: "See it on your phone →". Soft wall: *"Add $10 to
turn on live preview."* → web top-up (LemonSqueezy credit pack, Apple-safe,
already built). On enable: `managedServices.reload = true`. Agent runs
`/dev/build-native` on a **pooled build farm box** (not a dedicated box — keeps
the per-build cost at fractions of a cent), pushes the Hermes bundle to his phone
via the existing container path. **He sees his app on his phone in seconds, no
Xcode.** First debit ≈ $0.01. Trust established cheaply. *(Android can compile
on-device per `android-local-hermes-reload.md` — for Android users this stays
free; only iOS reload needs the farm. Don't charge for what costs nothing.)*

**t=3 — Turn on the brain (backend meter).**
App needs data. Cockpit: "App backend →". One tap → `managedServices.backend =
true` → Yaver provisions/deploys his Convex backend on Yaver-managed Convex and
proxies it; `backend` meter starts. He never types `convex deploy`, never makes
a Convex account.

**t=4 — Give it a website (web meter).**
"Website for my app →" → `managedServices.web = true` → Cloudflare proxy deploys
to `<slug>.yaver.app`; `web` meter starts. Never sees `wrangler`, never hits the
15 MB guard (the cockpit warns + compresses for him).

**t=5 — (optional) Move the agent to the cloud (compute meter).**
If he wants to close his laptop and drive from his phone: "Always-on coding
computer →" → `managedServices.agentBox = true` → DPP-provisions a managed
Hetzner box, installs Claude Code/Codex on it. Sub-choice: **use my own AI key
(free inference)** vs **Yaver's gateway (metered inference)**. Default to his own
key. Now he can attach from phone (`yaver code --attach`) or web terminal.

**t=6 — Ship to the stores (publish meter — the hero).**
"Publish to the App Store →" → `managedServices.publish = true` → Mac-farm
builds + signs (managed Apple identity, or guided own-account) + `yaver shots`
screenshots + uploads to ASC/Play. Status streams to the cockpit and a push
notification: "Apple is reviewing your app ✅." **This is the payoff he'd pay a
contractor hundreds for.** Fair money, gladly paid.

**t=7 — Live.** Top up from the web when low. The cockpit shows honest burn.

---

## 5. The inference / "managed Claude Code & Codex" question

Your prompt: *"may set claude code codex into yaver managed cloud as well."*
There are two distinct things here; keep them separate:

1. **Run the CLI on a managed box (compute).** Claude Code/Codex the *process*
   running on a Yaver Hetzner box instead of his laptop. Metered as `compute`
   (the box's hours). Straightforward, the meter exists.
2. **Supply the model tokens (inference).** Two modes:
   - **BYO key (default, fairest):** his Anthropic/OpenAI key lives in the box's
     vault (`provider_keys.go` env-injection already does this). Yaver charges
     **only** the box's compute hours. No inference markup. *This should be the
     loud default* — it's the honest path and the cheapest for him.
   - **Yaver gateway (convenience, metered):** he has no key / wants one bill.
     The runner points `OPENAI_BASE_URL` / `ANTHROPIC_BASE_URL` at the Yaver
     Gateway (built: CF Worker + `/gateway/authorize` + `/gateway/meter`).
     Gateway routes to GLM/DeepSeek/Qwen and meters `inference` at 1.5×.

**Fairness caveat that bites:** never resell *subscription* tokens (Claude
Pro/Max, ChatGPT Plus) through the gateway — terms-poisoned, already fenced in
`subscriptionStore.ts`. Gateway = per-token API resale only. And be honest that
the gateway uses GLM-class models, not Claude — if he expects Claude quality,
route his BYO Claude key, don't silently downgrade him to GLM and bill 1.5×.
That silent-swap would be the single most *unfair* thing in the whole system.

---

## 6. Fairness — answering "get his money fairly" head-on

Your word was **fairly**, and it forces a choice the existing docs dodge.
Premium §1 says *"the ignorance is the product... that opacity is the moat and
margin."* Cloud-Credits shows the rate, keeps integer-cent honesty, and always
offers BYO. **These are opposite ethics.** For the normie tier, adopt
Cloud-Credits' posture and explicitly reject Premium's opacity bet. Six rules:

1. **Charge for toil removed, not for ignorance.** The pitch is "you didn't have
   to learn Xcode/Convex/Cloudflare," never "you didn't know it was cheap." Every
   markup must survive him *finding out* the raw price. 2× managed-infra is
   industry-normal (Render/Railway/Vercel); a thin publish margin over real Mac
   build-minutes is obviously fair labor. Those survive disclosure. A 5× rent on
   "he can't price-shop VPS" does not.

2. **Free where it's near-zero or he brings his own.** Local scaffolding with his
   key = free. Android on-device Hermes reload = free. BYO inference key = no
   inference charge. Don't meter what costs Yaver nothing — metering it is the
   fastest way to feel like a scam.

3. **Cheapest-thing-first ladder.** First debit ≈ $0.01 (a single Hermes build on
   the pooled farm). Huge value, trivial cost. Trust compounds upward toward the
   publish meter where the real money is.

4. **Show the meter honestly — depart from Premium's "one number, never line
   items."** Headline number for calm ("$14.20 left, ~6 days at your pace"), with
   a per-capability breakdown one tap away ("reload $0.40, backend $2.10, web
   $1.00 this week"). The `managedUsage`/`creditUsage` ledgers already record
   per-kind charged cents — surfacing them costs nothing and *buys* trust.
   Opacity is not the moat; **the toil-removal is the moat.** Transparency makes
   it defensible.

5. **Always keep the BYO exit one tap away.** "Run it yourself → free" on every
   capability card. He can connect his own Hetzner/Apple/Convex/Cloudflare and
   pay Yaver nothing (BYO plane already shipped, Cloud-Credits §15/§16). Managed
   must win on *convenience*, never on lock-in. A captive normie churns and warns
   his friends; a fairly-served one invites them (→ `invite-to-code`).

6. **Pool resources so micro-meters stay honest.** A whole box per normie just to
   compile a Hermes bundle is wasteful and forces an unfair minimum. A shared
   build farm billed per build-minute keeps the entry meter genuinely cheap, so
   "see it on my phone" can cost a penny and still cover COGS.

**Net:** the margin is real but defensible — 2× on infra he'd never assemble,
thin margin on Mac builds he can't run, zero on the AI he already pays for. He
gets his app shipped; you get paid for the toil; both survive him reading the
receipt. That is "fairly."

---

## 7. Build gaps (what's actually missing for this)

Most of the spine exists. What this specific design adds, smallest-leverage
first. **Status updated 2026-06-08** — the keystone + cockpit shelf were built
this session (uncommitted, dry-run, verified by `tsc` + scoped `go test`):

| # | Gap | Where | Size | Status |
|---|---|---|---|---|
| 1 | **Per-user `managedServices` opt-in** above global env flags | `schema.ts` userSettings + gate in `managedMeter.ts` | S | **BUILT** |
| 2 | **Web cockpit: capability shelf** (toggle cards, balance, burn breakdown) | `web/components/dashboard/CapabilityShelf.tsx` + `Build` tab in `dashboard/page.tsx`; HTTP routes in `http.ts` | M | **BUILT** |
| 3 | **Web cockpit: guided journey** (detect state → offer next capability) | `web/` + reads local agent status | M | TODO (cards exist; auto-advance not wired) |
| 4 | **Web trigger for scaffold/autoinit** (wizard is CLI-only today) | thin web→agent POST; engine exists (`project_wizard.go`) | S | TODO |
| 5 | **Pooled build-farm for Hermes reload** (so the entry meter is cheap) | reuse Mac-farm/`publishJobs` queue shape for build-native | M | TODO |
| 6 | **Convex proxy** (backend meter routing) | Premium §5.2 — real work | L | TODO |
| 7 | **Cloudflare proxy** (web meter routing) | Premium §5.3 — real work | L | TODO |
| 8 | **Mac-farm publish** wired to publish meter | Premium §5.4 / `publish_macfarm` — real work | L | TODO |
| 9 | **Honest burn breakdown UI** (per-kind from ledgers) | `managedServices.burnBreakdownForUser` + cockpit "Where it went" panel | S | **BUILT** |
| 10 | **BYO-key-default toggle** on the agent-box card | `provider_keys.go` injection exists; UI choice | S | TODO (copy present on the card; toggle not wired) |

**What was built this session (uncommitted, dry-run, fail-closed):**
- `backend/convex/schema.ts` — `managedServices` optional object on `userSettings`
  (reload/backend/web/agentBox/inference/publish), additive + backward-compatible.
- `backend/convex/managedMeter.ts` — `userOptedIntoKind` gate; `recordManagedUsage`
  now forces `dryRun` unless the user has opted the capability in (per-user
  fail-closed on top of the global `YAVER_MANAGED_METER_LIVE` flag).
- `backend/convex/managedServices.ts` (new) — `getServicesForUser`,
  `setServiceForUser`, `burnBreakdownForUser`, `cockpitSummaryForUser`
  (internal, userId-keyed; the `cloudLifecycle.getWallet` pattern). Honest
  per-capability spend from `managedUsage` + `creditUsage`.
- `backend/convex/http.ts` — `GET/POST /managed/services`, `GET /managed/cockpit`,
  `GET /managed/burn` (bearer-authed, session-scoped, open to any authed user so
  the shelf renders from t=0).
- `web/components/dashboard/CapabilityShelf.tsx` (new) — the à-la-carte shelf:
  balance + honest runway header, six independent toggle cards in ladder order,
  "replaces <infra>" tags, "Where it went" per-capability breakdown, Apple-safe
  web top-up (reuses `/billing/credits/*`), BYO-exit link.
- `web/app/dashboard/page.tsx` — new **Build** tab (both navs + render branch + union).
- Verified: `npx tsc --noEmit` clean on backend (`convex/tsconfig.json`) and web;
  `convex codegen` clean; scoped `go test` privacy-pin tests pass. No deploy to
  prod, no Hetzner provisioning, no commit.

Already done and reusable (pre-session): wallet, both ledgers, configurable
markup, top-up webhook (signed, fail-closed, anti-tamper), inference gateway
(worker + authorize + meter), compute meter, Hetzner provisioning (un-branded),
BYO Hetzner full lifecycle on mobile, deploy-script generator + doctor, MCP
`ops` verbs, mobile onboarding/pairing/repo-coding, privacy contract + tests.

**Critical path to a shippable normie loop:** #1 ✅ → #4 → #5 → #2 ✅ → #3 gets
"scaffold locally, see it on your phone, pay a penny, see the honest meter" —
the cheapest end-to-end hook — without needing the heavy Convex/CF/publish
proxies. With #1/#2/#9 done, the remaining critical-path work is #4 (web scaffold
trigger) and #5 (pooled build-farm) to make "see it on my phone" real; the
backend/web/publish meters (#6–#8) are the upsell ladder that follows.

---

## 8. Anti-goals

- Don't silently route his "Claude Code" through GLM and bill 1.5× (§5). BYO
  Claude key is the honest default.
- Don't meter Android on-device reload or local scaffolding — they cost nothing.
- Don't remove the BYO exit to force the managed tier (rejects Premium §9 Step 5
  "remove BYO-Hetzner" *for the normie tier* — keep BYO visible; it's the
  fairness anchor).
- Don't hide the per-kind breakdown (rejects Premium §6 "never line items" —
  headline number yes, but breakdown one tap away).
- Don't put provider tokens, COGS internals, prompts, or paths in Convex
  (`convex_privacy_test.go`). Counters/labels only — already enforced.
- Don't wire Apple/Google IAP. Web top-up only; app spends, never sells.
- Don't push a tag and let CI deploy when a local deploy works (CLAUDE.md).
