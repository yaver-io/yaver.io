# Frictionless MCP-first managed-machine onboarding — design

> **Goal:** a customer-friendly, low-friction path where a user who *already has
> Claude Code / Codex* adds the Yaver MCP with one command, says "create me a
> machine" in Claude/ChatGPT, pays via a LemonSqueezy link, and gets a remote box
> that is **immediately usable** — runners pre-installed *and* authorized — reachable
> from CLI, mobile app, watch, car, TV, or the Claude/ChatGPT mobile app itself.
>
> **North star: never screw the customer.** Prepaid + scale-to-zero (pay for use, not
> for existing), transparent quota/cost, instant usability, no lock-in (BYO escape
> hatch), honest failures, data stays on the user's own box.
>
> Companion to `docs/security/arbitrage-resale-threat-model.md` — every billing/quota
> decision here is **control-plane authoritative** (enforced in Convex, never trusted
> from the MCP verb, which a user calls however they want).

## 1. Friction audit

Naive managed-box path = ~11 steps, **three** logins (yaver + claude-code + codex):
`install cli → yaver auth → subscribe → machine create → wait → ssh → install
runner → auth claude-code → auth codex → alias/primary → run`.

Target = **two user actions + natural language**:
1. `claude mcp add yaver …` (one command; or connect the hosted MCP + OAuth on mobile).
2. "create me a machine" → (unpaid?) LemonSqueezy link → pay → box provisions from the
   golden image → runners auto-authorized → "ready".
3. "code X on my machine" → runs, from any surface.

The golden image kills the install steps (runners baked in). The two remaining
friction points: **runner authorization** (§3) and **reaching the box from a
phone-side AI** (§4).

## 2. One high-level verb: `machine_onboard`

Don't make the user's AI orchestrate 6 low-level verbs. Expose **one** resumable verb
that chains internally and returns structured next-actions the AI narrates:

```
machine_onboard(plan?) →
  1. entitlement check (Convex, authoritative)
       not paid    → { status:"payment_required", checkoutUrl }   ← a link, not an error
       over quota   → { status:"quota_exceeded", used, limit, upgradeUrl }
  2. provision from golden image (origin:"managed", ~2–4 min)
  3. runner authorization (§3) → { status:"runner_auth", mode, code?, url? }
  4. register device over relay (persisted token, no OAuth prompt)
  5. → { status:"ready", machine, alias, runners:["claude-code","codex"] }
```

## 3. The crux — authorizing the box's runners

The box's runners must run under the **user's own subscription** (laws: sub-login, never
API key, CLI-only — `[[feedback_no_api_keys_subscription_only]]`,
`[[feedback_subscription_cli_only_compliance]]`). Both auth paths exist as MCP verbs;
**the default is surface-dependent:**

| Onboarding surface | Default | Verb |
|---|---|---|
| Local PC (has Claude Code creds) | **Import** the already-authed creds → zero-step | `runner_auth_credentials_import` |
| **Claude/ChatGPT mobile** (no local creds) | **Browser device-code** (sign in from any browser) | `runner_auth_browser_start`/`submit_code` |

Import is legitimate: *your* subscription on *your* single-user box (matches
`[[feedback_yaver_single_user_wrapper]]` "copy token"), not sharing. **Transport rule:**
runner creds move local→box over the encrypted relay/direct channel (QUIC/TLS), reusing
`/vault/peer-sync` — **never through Convex, never through the MCP tool result, never
logged.**

### 3.5 OAuth-sync broker — shipped core (2026-07-11)

The "box born authenticated" path is now real (no interactive OAuth on the box):
- **Convex `deviceCode.createAuthorizedDeviceCode` + `POST /auth/device-code/broker`**
  (deployed prod). An authenticated caller mints a PRE-authorized device code for a
  new box in one call; gated on the caller's own session so the box binds to the
  SAME user. Returns only the 15-min `deviceCode` handle — the real 256-bit token
  is fetched once by the box via the existing `GET /auth/device-code/poll`
  (`pendingToken` cleared on first read), so the injected handle is spent + worthless
  after first boot even though cloud metadata is rooted-readable.
- **Agent `cloud_broker.go`**: `brokerNewMachineDeviceCode()` (daemon calls the broker
  route with its own session) + `machineSelfRegisterScript()` (cloud-init snippet: poll
  once → write `~/.yaver/config.json` → `yaver serve` → registers as the user's device).
- **Remaining**: wire these into `machine create`'s `user_data` (bootstrap +
  self-register) so `yaver machine create` = zero-code OAuth end to end.

## 4. Multi-surface access + the one new architectural piece

- **CLI, Yaver mobile/watch/car/TV** — already work (relay/direct + persisted device
  token, runner PTY over WS). Nothing new.
- **Claude/ChatGPT mobile with Yaver MCP** — the **new build**. A phone-side AI calls
  MCP from Anthropic/OpenAI's cloud over **HTTPS + OAuth**; the local stdio
  `yaver-cli mcp` can't serve that. Need a **hosted remote MCP endpoint** that is:
  1. **OAuth'd** — user connects their Yaver account once.
  2. **A thin relay proxy** — authenticates the user, forwards verbs to *their own* box
     over the relay; compute never happens in the hosted MCP (broker, not executor —
     same shape as the arbitrage control plane).
  3. **Quota/billing-aware** — entitlement enforced before proxying.
  Inherits the **relay cross-tenant scoping** P0 (a session reaches only its own boxes).

## 5. Billing & quota — authoritative, friendly

- **Payment**: reuse LemonSqueezy checkout (`backend/convex/http.ts:196` → checkout URL).
  Unpaid → `{ payment_required, checkoutUrl }` (a link, never a bare refusal).
- **Quota** (NEW): per-plan machine cap enforced **in the Convex managed-provision
  action** (not the MCP verb). Expose `machine_quota → { used, limit, plan }` so the AI
  warns *before* the wall: "you're at 1 of 1 — upgrade for more." Silent "create failed"
  is exactly the un-friendly failure to avoid.
- Count awareness reuses the device hosting tag (managed list) shipped 2026-07-11.

## 6. Customer-friendly guardrails (the honesty contract)

- **No surprise charges** — prepaid + explicit link + **scale-to-zero** (idle ≈ €0.50/mo
  snapshot vs €14 running). Pay for use, not for existing.
- **Instant usability** — runners pre-authed; "ready" = ready to code, not "now SSH in."
- **Transparent** — `machine_quota`/cost surfaced through MCP.
- **No lock-in** — BYO (own Hetzner) stays first-class.
- **Honest failures** (`[[feedback_visible_failure_over_silent_retry]]`) — clear reason +
  fix, never a silent half-provisioned box.
- **Your data stays yours** — code/vault on the user's box (privacy contract), not Convex.
- **Clean exit** — `machine rm` = snapshot + delete + export; prepaid is refund-friendly.

## 7. Security tension (third-party AI in the loop)

Driving via Claude/ChatGPT mobile means Anthropic/OpenAI see every MCP call+result:
- **Never put secrets in MCP results** — runner auth is box-side (import over relay, or
  browser code the user completes); tokens never transit the MCP transcript.
- **Hosted MCP OAuth-scopes to the user's own boxes only** (relay cross-tenant scoping).
- Payment links / machine lists / quota through MCP are fine (public / own-data).
- Code+output through the AI provider is inherent to using it as the UI — the *user's*
  informed choice, stated plainly, not a Yaver leak.

## 8. Exists vs. build

| Piece | Status |
|---|---|
| LemonSqueezy checkout link | ✅ `http.ts:196` |
| `machine_create/up/down/rm` verbs | ✅ (BYO); managed via `cloudMachines.ts` |
| Runner auth: import + browser device-code | ✅ verbs exist |
| Managed provision + subscription gate | ✅ `cloudMachines.ts` fail-closed |
| Golden image (runners pre-installed) | ⏳ baking (arm-capacity blocked) |
| Device hosting tag (managed count) | ✅ 2026-07-11 |
| **Machine quota (per-plan cap, Convex-enforced)** | ✅ built |
| **OAuth-sync broker — box born authenticated (zero code)** | ✅ core built + deployed 2026-07-11 |
| **`machine_onboard` one-verb orchestration** | ❌ build |
| **Surface-aware runner-auth default** | ❌ build |
| **Hosted/remote MCP endpoint (OAuth, relay-proxy, quota-aware)** | ❌ build — linchpin for Claude/ChatGPT mobile |

The two real new builds are the **hosted MCP endpoint** and **machine quota**; the rest is
wiring existing verbs into `machine_onboard`.
