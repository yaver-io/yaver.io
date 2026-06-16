# Yaver Personal Agent Gateway

> Status: design (2026-06-17). Generalization of the EV-charging case
> (`docs/yaver-ev-charging-turkey.md`) into a universal pattern. Companion to the in-car
> voice surface (`docs/yaver-car-voice-coding.md`). **EV is one connector among many.**

## 1. The thesis in one line

**Yaver is an AI-driven CRUD layer over every credentialed app/service the user already uses,
executed on the user's own infrastructure.** It turns apps that have no API into a personal API
you query and command in natural language. The self-hosted runner (or Yaver-managed cloud)
holds your sessions and acts **as you** via `api` / `chromedp` / `redroid` / `mcp`.

"Is this charger free?", "Misli ratio on this match?", "EUR rate from my broker?", "my bank
balance?", "reorder groceries", "pay this bill", "start a charge" — all reduce to the **same
operation shape**.

## 2. The universal operation model (the core insight)

Every interaction with any service — API or not — reduces to a CRUD verb on a resource:

| Verb | Meaning | Risk | Examples |
|---|---|---|---|
| **GET** | read state | low | is-it-free, price, balance, FX rate, bet ratio, order status |
| **ADD/CREATE** | create | high (acting) | place bet, book slot, send, order, start charge |
| **UPDATE** | modify | high (acting) | change reservation, top up, cancel |
| **DELETE** | remove | high (acting) | cancel order, remove item |

The AI's job: map natural language → `(connector, verb, resource, params, risk)` → execute via
the right engine → **verify** → synthesize the answer. This is function-calling where the
"functions" are *your apps* and the "execution" is UI automation when no API exists.

## 3. Three layers

```
1. INTENT (AI router)     NL → {connector?, verb, resource, params, risk}; disambiguate; plan
                          multi-step ("compare EUR across my 2 brokers" = 2 GETs + compare).
2. CAPABILITY (connectors) registry of your services; each = { engine, authRef→vault,
                          surface, capability map (which get/add/update it supports) }.
3. EXECUTION (engines)    on your box: api | chromedp(playwright) | redroid | webview | mcp.
                          Runs the op AS YOU, returns raw result; AI extracts the answer.
```

### Connector schema (sketch)
```
Connector {
  id            "esarj" | "misli" | "broker-x" | "bank-y" | "tesla"
  engine        "api" | "playwright" | "redroid" | "webview" | "mcp"
  surface       url | androidPackage | apiBaseUrl
  authRef       vault ref (storageState / token / app-login) — NEVER inline
  capabilities  { get: [...], add: [...], update: [...] }   // each maps to a flow/endpoint
  riskDefault   "read" | "act"
  cadence       human/on-demand (NOT a polling swarm)
}
```

### Intent schema (sketch)
```
Intent { connector?, verb, resource, params, risk, needsConfirm, multiStepPlan? }
```

## 4. Why AI is load-bearing here (not decoration)

- **NL → intent** — genuine language understanding + disambiguation.
- **Reading arbitrary/unfamiliar UI/DOM → structured data** — the thing that makes "wrap *any*
  app cheaply" possible. The model finds the number; you don't hand-write the selector.
- **Self-healing** when UIs change (`testkit_self_heal_selector` exists).
- **Multi-step planning** across apps ("cheapest EUR of my 3 brokers, then alert me if < X").
- **Summarize noisy results to one answer** — essential for the voice surface.

Be honest about where it's *not* AI: a currency compare is arithmetic; an FX rate from an API
is a fetch. Use AI for understanding/perception/planning, not as a badge on a formula.

## 5. Engine ladder — always climb to the cheapest, most legit rung

1. **Official API** (`api`) — **always prefer** (Tesla Fleet API, broker REST, bank open-banking).
2. **chromedp / Playwright** (`playwright`) — web apps with no API; your authenticated session.
3. **redroid** (`redroid`) — mobile-only apps (many TR services: Eşarj/ZES/Misli are app-first).
4. **webview / mcp_external** — embedded or already-wrapped.

The router picks the highest available rung per connector.

## 6. Authoring a connector (the cost model)

Record-once → AI generalizes → reuse:
- Web: Playwright **codegen** (record your clicks) → AI turns it into a parameterized flow.
- Mobile: the redroid/testkit **recorder** captures the tap sequence; AI generalizes to selectors.
- Creds captured **interactively once** (you type the password, approve 2FA) → session serialized
  to **vault** → loaded on every future run. Re-auth only on expiry.

This keeps "wrap a new app" cheap — minutes, not days.

## 7. Trust & consent for ACT (add/update/delete)

- **Read = low friction.** Act = **dry-run preview + explicit confirm + audit + sandbox**.
  ("About to place a 50 TL bet on X — confirm?") Never fire a write from an ambiguous voice
  transcript.
- **Idempotency + post-verify** — after an act, re-read to confirm it happened. Don't trust the
  click.
- **Financial / betting / spending** ops are *always* explicit-confirm, *never* autonomous loops.

## 8. State, memory, proactivity

- Cache recent reads (balance, last order, last FX) locally for instant answers.
- **Proactive agents** (Task Packages runner + routines): "EUR < your threshold", "charger now
  free", "bet line moved" — push notifications / TTS. On-demand cadence, your vantage.
- Local-first store (`collection_store`), vantage-keyed; **never to Convex**; never creds.

## 9. Guardrails — the line that keeps your accounts alive

This is **personal automation of your own accounts** — doing by robot what you may do by hand.
Legitimate, but one inch from abuse. Bright lines (also Yaver's `access_policy.go` Policy Guard):

- ✅ **Your credentials, your accounts, your data.** Vault-stored, never committed, never Convex.
- ✅ **Official API first**; UI automation only when there's no API.
- ✅ **Human cadence, on-demand.** You ask → it answers. Not a polling swarm.
- ✅ **Your own device / residential vantage** for reads — *not* a 24/7 datacenter scrape loop
  (that gets the whole cloud account suspended and is wrong regardless).
- ✅ **Consent + audit + sandbox** for every ACT; confirm-gate spends/bets/charges.
- ❌ **No bot-detection evasion, no captcha-solving, no UA-spoof, no WAF/rate-limit bypass, no IP
  rotation.** Back off on 403/429/451 — a block is a "no", recorded as a finding, never routed
  around. **2FA = human-in-the-loop** (approve the push); never automate it away.
- ⚠️ **The ban risk is now YOUR account**, not just a datacenter IP. Respectful cadence is
  self-interest.
- ⚠️ **ToS:** personal, occasional, own-account use is defensible; high-frequency or resale-grade
  is not. Stay on the personal-assistant side.

## 10. Open-core split

- **Generic → open Yaver:** the gateway *framework* (connector registry schema, intent router,
  consent/audit/sandbox, self-healing), and the engines (`browser_*`, `droid_*`/`adb_*`, `api`).
- **Private → yours, never in the public repo:** your *specific* connectors, your
  credentials/sessions (vault), and any proprietary domain logic — e.g. the betting model
  (`../yaver-bet`) and the OCPP charge driver. The framework is open; *your wired-up life* is private.

## 11. What exists vs net-new

| Piece | State |
|---|---|
| Web engine (`browser_navigate/click/type/extract_text/get_dom/...`, chromedp) | **exists** |
| Android engine (`droid_*`, `adb_*`) + AI app-test agent + golden logged-in snapshot | **exists** |
| Self-healing selectors, record/codegen | **exists** (`testkit_self_heal_selector`, recorder) |
| Vault for session creds | **exists** |
| Policy Guard, vantage selection, `collection_store` | **exists** |
| Task Packages READ-ONLY/ACTING tiers, consent, sandbox | **exists (designed)** |
| `models_*` (local/cheap inference for extraction/scoring) | **exists** |
| **Connector registry + per-app auth-session manager** | **net-new** |
| **Intent router** (NL → connector+verb+params) | **net-new** |
| **storageState ↔ vault lifecycle + 2FA human-loop** | **net-new** |
| **Dry-run/confirm/audit wrapper for ACT** | **partly (Task Packages) — needs gateway wiring** |

## 12. EV as the reference connector

EV is *one* connector that proves the pattern end-to-end:
- `get`: "is it free / price" → `ev_charging` (api/OpenChargeMap) or the network app (redroid).
- `add`: "start a charge" → ACT → private OCPP driver behind the `ChargeController` seam (confirm-gated).
The same shape generalizes to Misli (get ratio / add bet — bet *model* stays private), FX (get
rate via broker API), bank (get balance), commerce (add order), etc.

## 13. Build order

1. **Connector framework** — registry + schema + vault auth-session manager (generic, open).
2. **Intent router** — NL → connector+verb (start read-only).
3. **Reference connector, read-only** — one web (chromedp + vault `storageState`) and one mobile
   (redroid, e.g. an EV/Misli "get") to validate both engines.
4. **ACT wrapper** — dry-run + confirm + audit, then enable one write end-to-end.
5. Voice surface: route gateway queries through the in-car STT/TTS assistant.

---

# Deep design (design-only, 2026-06-17)

## 14. The Intent Router

**Core insight: connector capabilities ARE tool schemas; routing is LLM tool-selection over
the registry.** Each connector publishes its get/add/update capabilities as function
descriptors; the router is an LLM doing function-calling over the union of *your* connectors —
exactly like MCP tool selection, but the "tools" are your apps.

### Pipeline
1. Normalize utterance + context (location, time, recent turns, device).
2. **Stage-1 cheap/local classifier** (`models_*`, on-device): is this a gateway request?
   which connector candidates? which verb? Fast + private.
3. **Stage-2 planner** (large model, only when needed): resolve ambiguity, extract params,
   build a multi-step plan (DAG of capability calls), tag risk.
4. **Ground** every step to a concrete capability descriptor in the registry; reject any
   capability that doesn't exist (no hallucinated actions).
5. **Confidence gate:** low confidence or multiple plausible connectors → clarify
   (`yaver_ask_user` / voice), don't guess.

### Schemas
```
Capability {            // what a connector advertises — the "tool"
  id, connectorId, verb("get"|"add"|"update"|"delete"), title, description
  paramsSchema:  JSONSchema   // param extraction + validation
  answerSchema:  JSONSchema   // structured extraction target (the KEY artifact for GET)
  risk("read"|"low-act"|"high-act"), reversible: bool, costBearing: bool
  flowRef                     // api endpoint | recorded UI flow id
}
Step  { connectorId, capabilityId, verb, params, risk, dependsOn?[], answerSchema?, condition? }
Intent{ utterance, steps: Step[], needsClarify?{question, options} }
```

### Key behaviors
- **Grounded, not generative** — can only select capabilities that exist; unknown request →
  "no connector for that — add one?" (→ authoring funnel), never an invented action.
- **Defaults learning** — "my broker" resolves via a per-user alias store; persist resolutions.
- **Determinism cache** — memoize (utterance pattern → plan); invalidate on registry change.
- **Cost tiering** — local model routes; escalate to large model only for hard planning.

## 15. Connector Authoring UX (wrap an app in minutes, by demonstration)

Funnel: **discover → bind-auth-once → record → AI-generalize → declare → self-test → heal.**
1. **Discover** — pick/auto-detect engine (API? → `api`; web URL → `playwright`; Android
   package → `redroid`).
2. **Bind auth once** — wizard opens a headful browser / redroid screen; you log in (approve
   2FA); session → **vault**; 2FA steps marked human-loop.
3. **Record** — do the task once (Playwright codegen / redroid recorder captures the trace).
4. **AI generalize** — convert the concrete trace into: a parameterized flow (which fields are
   inputs), a capability descriptor, an **answer schema** (what structured data to extract),
   and candidate self-healing selectors (role/text/testid + fallbacks).
5. **Declare** — confirm/edit the proposed get/add/update map + answer schema; name them
   (`esarj.station_status`, `broker.fx_rate`).
6. **Self-test** — run the GET → show the extracted structured answer; run ACT as dry-run.
7. **Version + heal** — store flow+selectors with versions; on drift the AI re-derives + re-tests.

Output = a connector manifest (generic schema, **open**) + creds in vault (**private**). The
**answer schema** is the crucial artifact — it's what turns messy DOM into
`{available:true, price:9.5}` reliably. Cost: first wrap = minutes; maintenance ≈ 0 until a UI
redesign, then self-heal covers most drift.

## 16. The ACT Consent Model (the safety architecture for writes)

| Tier | Examples | Consent required |
|---|---|---|
| **READ** | balance, price, status | none |
| **LOW-ACT** (reversible, non-financial) | toggle setting, save draft, add-to-cart | one-tap / one-word confirm |
| **HIGH-ACT** (financial / irreversible / destructive) | place bet, buy FX, pay bill, start charge, cancel | dry-run preview + explicit confirm + (over threshold) 2nd factor |

Mechanisms:
- **Dry-run preview (most important):** drive the flow to the **final confirm screen and STOP**;
  render exactly what will happen (amount, counterparty, side effects) extracted from that
  screen. Nothing commits until you approve the *actual* pending action — not the AI's intent.
- **Two-key for the worst ops:** irreversible + financial over a per-connector threshold needs
  confirm on a **second surface** (phone push), not just voice. A misheard voice command can't
  move real money alone.
- **Policy / spend caps:** per-connector limits, capability allowlist, velocity limits
  (max N acts/hr), time windows — enforced before execution (mirrors `access_policy.go`).
- **Idempotency + post-verify:** idempotency key per act; re-read state after; report *verified*
  outcome, not assumed.
- **Sandbox + audit:** acts run in the consented sandbox; append-only audit
  (action+target+outcome+timestamp). Convex = summaries only (no params/amounts/secrets);
  detailed log local.
- **Reversibility wiring:** where the app supports cancel/undo, capture how, and offer it
  immediately ("Done — say 'undo' within 60s").
- **Voice-specific:** explicit confirm phrase ("say 'confirm bet fifty lira'"); negation always
  beats a stray affirmative (already implemented in `carVoiceConfirm.ts`); never accept an
  ambiguous "yeah" for HIGH-ACT.
- **Betting/financial special case:** always HIGH-ACT, never autonomous. The gateway places
  *your explicit* bet; it does not run a strategy loop (honest prior verdict: single-book bots
  bleed to the vig). Betting *model* stays private (`../yaver-bet`).

## 17. The Auth Broker — and redroid as a *device*

**Insight: redroid is not just an automation engine — it's a persistent, trusted device
identity.** A cloud Android instance can install a service's app, hold an authenticator, become
a "remembered device", and receive push approvals. That collapses most 2FA into either
*auto-satisfiable* or *one-tap-on-your-real-phone*.

### Auth-method ladder (prefer top)
| Method | Path | Human? |
|---|---|---|
| OAuth code + PKCE / refresh token | proper API (Google / MS / Tesla Fleet) | once, at consent |
| OAuth device grant | code + approve (Yaver already does this: `deviceCode.ts`) | once |
| Open Banking (PSD2 / BDDK) | regulated bank API + consent | once (re-consent ~90d) |
| Password + session cookie | `storageState` → vault | + a 2FA row below |
| **TOTP** | seed in vault OR authenticator app on **redroid** → auto-fill | none (you own it) |
| **Push approval** | app on redroid / your phone → approve | one tap |
| SMS OTP / SMS-only login | redroid reads its own SMS inbox, or your real phone forwards the code → broker waits for the matching OTP and fills it | auto (if sourced) / read-once |
| **Biometric (Face ID / Touch ID)** | almost always a *local unlock* over a stored token/session — you unlock **once** (human), the session persists; if it gates nothing deeper, fall back to the password/token path | one-time |
| Passkey / WebAuthn | device-bound, phishing-resistant **by design** | usually **NOT relayable / NOT bypassable** → official API or manual |

### Components
- **AuthMethod handlers** — pluggable, one per row above.
- **Device registry** — redroid instances *and* your real phone as devices, each tagged with
  capabilities (receives-sms, has-app-X, holds-TOTP-Y, trusted-for-Z). The broker picks the
  device that can satisfy a given challenge.
- **Resumable flow with human gates** — a flow is a state machine; on an irreducible human step
  it **suspends, snapshots the screen, pushes to your real phone/voice** ("approve in your bank
  app" / "enter the code I just texted you"), waits (timeout), resumes on your action. Async,
  minimal-friction.
- **Vault** — tokens, refresh tokens, TOTP seeds, sessions. Encrypted; never Convex; never committed.
- **Network jail + isolation** — session-holding devices are high-value: relay-only,
  RFC1918-blocked, same-user-only, audited (the operator-fleet jail pattern). Self-hosted
  strongly preferred for financial/sensitive connectors.

### Honest lines
- This makes 2FA **convenient, it does not bypass or weaken it** — you still approve; the broker
  only reduces friction for factors you legitimately control. Never defeat 2FA's purpose.
- **Bank data → Open Banking APIs**, not redroid scraping (ToS/legal/lockout risk).
- **SMS-receive services are fraud-flagged** — prefer your own number or app-push/TOTP.
- **Passkeys are intentionally non-relayable** — that's the security feature; fall back to the
  official API or manual.
- **Biometrics (Face ID / Touch ID) cannot and must not be "bypassed"** — they're a local unlock
  over a stored credential. The honest handling: you satisfy the biometric **once** on your real
  device (a human gate), and the **session it unlocked persists** (golden snapshot / storageState);
  later calls reuse that session. We never defeat a biometric; we persist what it unlocked. If the
  biometric *is* a passkey/WebAuthn, it's non-relayable → official API or manual.
- **SMS-only login** is supported by *sourcing* the code (redroid's own inbox, or your real phone
  forwarding to the broker) — needs **your** number; SMS-receive services are fraud-flagged, don't
  use them.
- Holding TOTP seeds + live bank sessions on a cloud box is a real risk surface — sensitive
  services should be **self-hosted**; managed cloud requires explicit trust + strong isolation.

## 18. Self-improving personalized MCP

The gateway exposes **your** connectors as a **per-user MCP server** — your apps become a
personal tool surface that any host AI (the in-car voice assistant, Claude Code) connects to.
It **self-improves from usage**:

1. Selector/flow **self-healing** on drift (`testkit_self_heal_selector`).
2. **Capability discovery** — explore an app, propose new capabilities ("also has usage history
   — add?").
3. **Preference/alias learning** — "my broker"=X, usual charge=80%, your phrasings → faster
   routing, fewer clarifies.
4. **Answer-schema refinement** — fix wrong/incomplete extraction.
5. **Routing improvement** — cache (utterance→plan); learn from your corrections.
6. **Flow optimization** — shorter paths (an API became available; drop a step); lower latency/cost.
7. **Reliability scoring** — track flaky capabilities, prioritize healing, warn you.

**The loop:** every interaction emits signal (success/fail, correction, new capability) →
updates the registry/preferences/routing → next time is better. A periodic **curator agent**
(completeness-critic pattern) reviews usage and proposes improvements.

**Safety gates (critical) — self-improvement is bounded:**
- *Auto-allowed:* selector healing, READ-capability refinement, preference learning, routing cache.
- *Requires your confirm:* any **new ACT/financial capability**, any **auth change**, any
  **spend-cap/policy change**. A self-improving system must never silently grant itself
  "transfer money."
- All changes **versioned + reversible + audited**.

**Privacy:** this is your most personal data (apps, habits, sessions). The personalized MCP +
learning store run on **your** box, local-first/vault; the privacy contract forbids it in
Convex. It's yours, it improves for you, it never leaves.

## 19. Remote-only operation, human-loop captcha, authenticator handling

**Constraint that reshapes the broker: assume the user has ZERO physical access to the remote
device** (managed-cloud redroid). Every human step — first login, 2FA, captcha, authenticator
setup — must be doable *remotely*. The redroid is "the user's device" but operated entirely
through a remote window.

### 19.1 Human-in-the-loop captcha (legitimate — with a bright line)
- ❌ **Forbidden:** auto-solving (OCR/ML solvers, 2captcha-style human farms, fingerprint
  spoofing to suppress the challenge). That's evasion.
- ✅ **Legitimate:** stream the live challenge to the **actual account owner**, who solves it
  themselves, interactively. The human *is* present — which is exactly what a captcha verifies.
  And it's rare: solve once at the login/challenge, the session persists, you don't see it again.
- **Mechanism — the human-gate goes *interactive*:** a live remote view of the browser/redroid
  (`rd/` MJPEG stream + `/rd/input`, or `droid_frame` + `droid_input`) renders on the user's real
  phone; their clicks/drags relay back; the flow resumes when solved.
- **Bound:** the user's own account, own occasional challenge, low frequency. If captchas appear
  *constantly*, the service is saying "stop" → back off. A signal, not a step to grind.

### 19.2 Remote operation primitives (works without ever touching the box)
- **Remote view + input:** the remote-desktop engine (`rd/status|stream|input`) for web,
  `droid_frame`/`droid_input` for redroid. A "human gate" renders as a live window on the user's
  phone — never a physical tap on the cloud device.
- **Session persistence is doubly critical** when you can't touch the device — golden snapshots
  must capture device-trust so the human steps recur rarely.

### 19.3 Authenticator handling (Yaver-as-authenticator first; apps optional)
Per connector, the broker picks a mode — **always a remote-operable one**:
1. **Yaver-as-authenticator (default, fully remote):** at enrollment capture the **TOTP secret**
   ("can't scan the QR? enter this code") → store in **vault** → Yaver generates the rotating
   code itself. No app, nothing to tap, works headless in managed cloud.
2. **Authenticator app on the remote device (optional):** Microsoft Authenticator / Google
   Authenticator / the service's own app on the redroid, for **push-only** 2FA with no TOTP
   alternative. Operated via remote-view (approve the push through the remote window) — or, when
   the account allows multi-device enrollment, the push **also** lands on the user's **own** phone
   where they approve physically (preferred).
- **Connector declares its 2FA mechanism:** `{ totp_seed | authenticator_app(device) |
  push_to_app(device) | sms | passkey }`; the broker routes accordingly, escalating to a
  human-gate only when irreducible.
- **Passkey-only with no fallback** stays the one dead-end (non-relayable) → official API / manual.


