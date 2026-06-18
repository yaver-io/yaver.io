# Yaver Personal Assistant — MVP Spec

> **Source-of-truth note (read CLAUDE.md first).** This doc references real code
> as of 2026-06-17. Where it says *exists*, grep confirmed it; where it says
> *proposed* / *net-new*, it does not exist yet. When code and this doc disagree,
> the doc is the bug — fix it in the same change.

## What this is

The Personal Assistant is an **AI-driven CRUD gateway** over the user's own
credentialed apps/services. It acts *as the user* — official API > web
(playwright/chromedp) > redroid (mobile UI) > — to **get / add / update / delete**
across apps that often have no usable API. Creds live in the vault; every
mutating action is confirm-gated, dry-run-previewed, and audited locally.

It is **B2C / personal**, not a B2B mission-critical integration bus. The value
is "automate *my* stuff that nothing else can touch," safely — not
"never-breaks at enterprise scale." The two design risks below are exactly why.

## Status: the spine already exists (~10,900 lines, tested)

| Capability | File(s) | State |
|---|---|---|
| Intent router (NL → connector/verb/params), tiered keyword→model | `gateway_intent.go`, `gateway_intent_model.go`, `gateway_intent_mcp.go` | **Works** |
| Connector registry (local JSON manifests, never Convex) | `gateway_registry.go` | **Works** |
| ACT pipeline: policy guard → velocity cap → dry-run → two-key confirm → audit | `gateway_act.go` | **Works** |
| Human gate (`awaitHuman`, `GateApprovePush`, resumable, phone push) | `gateway_gate.go` | **Works** |
| OAuth broker + PKCE + refresh-on-401 | `gateway_oauth.go`, `gateway_broker.go` | **Works** |
| Redroid 2FA + seamless OTP relay | `gateway_redroid.go`, `gateway_redroid_invoke.go` | **Works** |
| Vault-backed per-connector creds | `gateway_creds.go`, `vault.go` | **Works** |
| Append-only local audit ledger | `gateway_audit.go` | **Works** |
| Phone-inventory → clone bridge | `gateway_phone_inventory.go` | **Works** |

**So the MVP is not greenfield — it is "close four stubs":**

1. **AI extraction / vision** — `projectAnswer()` is deterministic dotted-path;
   the AI hook is a placeholder. *(→ §3 Vision-heal)*
2. **Web / playwright session** — `SessionStorageState` is a placeholder. *(→ §3)*
3. **Connector catalog** — none; all user-authored. *(→ §3 Demo recorder)*
4. **Self-heal curator** — stubbed in `gateway_selfheal.go`. *(→ §3 Vision-heal)*

## The two risks this MVP must close (and how)

1. **UI-automation fragility.** Selector flows (`FlowStep.target` /
   `expectSignature`) rot on every app update. **Fix: vision-grounded
   self-heal** (§3) — the cached selector runs first; on mismatch a vision model
   re-grounds the action and rewrites the cache.
2. **Financial / irreversible-action liability.** "An AI moved my money" is the
   highest trust-bar in consumer software. **Fix: WebRTC last-mile** (§4) —
   Yaver drives to the doorstep; the user types sensitive fields and makes the
   final tap on the *real* streamed screen.

A third concern — **margin** — is protected by **tiered inference** (§2):
assistant turns burn resold tokens, so route the 90% cheap and spend frontier
only where it pays.

---

## §1 Architecture (one engine, three moods)

The same gateway engine is packaged three ways. This doc is the **assistant**
(reactive) mood; automations (proactive/scheduled) and sandbox (build the
missing connector) reuse the identical registry + ACT + gate + audit spine.

```
utterance ──► routeIntent (tiered: keyword → model)
                 │
                 ├─ gateway_read  ─► gatewayInvoke ─► engine ladder ─► projectAnswer ─► answer
                 │                                         (API | web | redroid)
                 └─ gateway_act   ─► buildActPreview (dry-run) ─► GATE ─► execute ─► audit
                                                                   │
                                              ┌────────────────────┼─────────────────────┐
                                         riskLow              riskHigh             riskFinancial
                                      GateSimpleConfirm      GateApprovePush        GateLiveTap (§4)
                                      (voice "yes" OK)       (tap in Yaver)      (WebRTC, user types
                                                                                  fields + taps real btn)
```

## §2 Inference — tiered, cheap OSS *and* frontier by job

Do not pick one model. Extend the router's existing keyword→model tiering to
every inference call. **The result is cached so frontier is paid once per
screen layout, then cheap forever.**

| Job | Tier | Rationale |
|---|---|---|
| Intent routing (≈90% of turns) | keyword / tiny local (**exists**) | Free, offline, instant. Never pay a model on a keyword hit. |
| Ambiguous intent escalation | cheap OSS / Haiku-class | "Which connector + verb" is easy. |
| **Vision grounding of a novel screen** | **frontier vision (Opus/Sonnet computer-use)** | The hard part; cheap vision misses buttons / hallucinates taps. Spend here, cache the result. |
| Extraction from a *known* screen | cheap OSS vision / local OCR | Layout known → cheap is fine. |
| Planning a novel multi-step flow | frontier | Rare, high-value. |

**Honest take:** cheap OSS genuinely helps for routing, extraction, OCR, and
re-runs of known flows. Frontier vision earns its cost only for *grounding a
screen never seen before* — which is exactly what makes redroid/playwright
robust. The margin lever is the **cache**: pay frontier once per layout.

`intentCompleteFn` is already an injectable seam (`gateway_intent_model.go`);
add a parallel `visionGroundFn` seam so the model tier is swappable per
deployment (managed vs BYOK vs self-host-local).

## §3 Perception & flow-learning

Three mechanisms, in priority order. The first two are MVP; the third is R&D.

### Vision-heal (MUST — closes fragility)
- Today: `FlowStep{action, target, expectSignature}` selectors break on UI change;
  `projectAnswer()` reads a fixed dotted path.
- Proposed: **selector-fast-path → vision-heal-on-miss → re-cache.**
  1. Run the cached selector/path (fast, cheap, offline).
  2. On `expectSignature` mismatch (or extraction failure), call `visionGroundFn`
     over `deviceDriver.Frame()` → "where is the *Transfer* button / the
     *balance* field on *this* screen."
  3. Execute the re-grounded action; **rewrite the cached step** (the curator
     slot already stubbed in `gateway_selfheal.go`).
- Applies symmetrically to **get** (extraction), **add/update** (form fill),
  **delete** (locate + confirm). This is the single highest-value piece.

### Demonstration recorder (MUST — seeds the catalog)
- "We can train it manually" is the honest answer to the empty-catalog problem.
- Proposed: a **record-a-flow-once** tool. A human performs the flow on
  redroid/playwright; Yaver records `FlowStep`s + a vision `Snapshot` of each
  screen → connector flow written to `~/.yaver/connectors/<id>.json`.
- **Hand-author the head** (top ~50: bank transfer, EV topup, order placement,
  bill pay). **Long tail is user-recorded.** No 6,000-connector army needed.
- Reuses existing `FlowStep` / `Snapshot` / `RestoreSnapshot` machinery.

### YouTube flow-bootstrapper (R&D — after the above)
- Mine "how to <do X> in <app>" tutorials: a vision model watches and emits a
  **draft** step list to accelerate authoring.
- **Honest constraints:** tutorials are noisy / outdated / region-specific →
  output is always a *human-confirmed draft*, **never auto-trusted, never for
  financial steps**. Respect guardrails — official API / `yt-dlp` politely,
  back off on blocks, no scrape-swarm (CLAUDE.md). Priority: *after* vision-heal
  + recorder ship. A catalog accelerator, not a foundation.

## §4 Trust & liability

### Approvals inbox (first-class surface)
- Backend exists: `PendingGate`, `GET /gateway/gate`,
  `POST /gateway/gate/{id}/resolve`, `blackboxGateNotifier` → phone push.
- **Net-new = the surface.** A dedicated **Approvals inbox** in mobile + web
  rendering `ActPreview` richly (app, action, diff, risk tier, idempotency) with
  tap-to-approve / decline / edit. The confirm-queue is the *main character* of
  the assistant UI, not a buried modal.

### WebRTC last-mile — `GateLiveTap` (the marquee feature)
For `riskHigh` / `riskFinancial`, the gate escalates from "approve in Yaver" to
**"here's the real app screen — you finish it."**

- Yaver does the navigation (open app, reach the transfer screen).
- **For first release (chosen model): the user types the sensitive fields
  (payee, amount) themselves** on the streamed surface, and makes the final
  irreversible tap on the *real* button. Yaver never types the amount/payee.
- The screen is streamed over **WebRTC**, reusing shipped infra:
  `remotedesktop*.go` (`/rd/stream` MJPEG + `/rd/input` forward), `ghost_stream.go`,
  `capture.go`, and the WebRTC Opus stack from commit `85876105e`. The
  remote-desktop engine *is* the last-mile viewer; input is forwarded back to the
  redroid/playwright session.
- **Gate wiring:** extend `confirmPlanFor(tier)` with a third plan `GateLiveTap`.
  Instead of preview+button, it opens the stream and waits for the user's
  field-entry + tap to land as real input. Audit records `confirmed: live_tap`.

Product line: **"Yaver drives to the doorstep; for anything irreversible, you
cross the threshold yourself."** Liability rests on the user's actual finger on
the actual button — which defuses the #1 risk to the whole product.

## §5 UI — Cockpit by default, mobile-first

- **Mobile = simple, always.** "Ask Yaver to…", **connected accounts**
  (add/remove), the **Approvals inbox** (`N actions waiting`), an **activity log**
  ("here's what I did"), a **wallet/usage** card. Zero verbs, zero logs.
- **Web** holds the Cockpit↔Workbench toggle. Pro reveals connector internals,
  ACT dry-run diffs, audit detail, vault, the demo recorder, raw `ops`.
- Never call it "Developer Mode." Default new accounts to Cockpit; detect-and-
  *offer* Workbench on power signals.

## §6 Build order (the real MVP = 1–4)

1. **Vision-heal in the flow engine** — selector-fast-path → frontier vision
   re-ground on `expectSignature` miss → re-cache. Fills `gateway_selfheal.go` +
   the `projectAnswer()` AI hook. *Unblocks reliable get/add/update/delete.*
2. **Demonstration recorder** — record-a-flow-once → connector catalog; hand-
   author top ~50 apps. *Seeds the catalog.*
3. **Approvals inbox** (mobile + web) over the existing gate backend. *Cheap, huge for trust.*
4. **`GateLiveTap` WebRTC last-mile** (user types sensitive fields + taps real
   button), reusing `remotedesktop*.go` + Opus/WebRTC. *Kills financial liability.*
5. **Tiered-inference wiring** + per-layout vision cache (§2). *Protects margin.*
6. *(R&D)* YouTube flow-bootstrapper → human-confirmed draft flows.

## §7 Non-goals / honest lines

- **Not** a B2B reliable iPaaS. Personal/prosumer only; reliability expectations
  set per-engine (API reliable, redroid "may need your tap").
- **No** captcha-solve, 2FA-bypass, bot-detection evasion, IP rotation, or
  24/7 datacenter scrape loops (CLAUDE.md Policy Guard). Engine ladder favors
  official API; redroid is last resort, human-cadence.
- **No** autonomous financial execution — irreversible actions always end in a
  human's live tap (§4). Financial/high always confirm, never autonomous.
- Connectors, inventory, consent, audit ledger: **local-only, never Convex**
  (privacy contract).
- BYOK respected at cost; managed-key is the default (margin). Free tier =
  self-host-your-own-key; no subsidized managed inference.

## §8 Hardware — there is none (phone is the hub) + optional accessories

The honest conclusion after weighing a Pi/CM compute box: **don't ship compute
hardware.** A second-hand flagship the user already owns *is* the hub — local
compute + NPU (cheap inference tier), real Android (real apps, no redroid),
a SIM (real SMS-2FA), camera, BLE/WiFi. **Compute COGS = zero; the user brings
it.** The business is software + credit-metered cloud fallback (§2): routine
turns run free on the phone, only the hard frontier tail is resold.

Optional accessories — **commodity-first, bundle-not-manufacture, sold only
after the software has customers:**

- **Home control radios** — integrate commodity Broadlink / Sonoff / Flipper
  over WiFi/BLE (the kumanda plan: SmartIR / Flipper-IRDB / `python-broadlink`,
  wired to the in-flight `ops_home.go` / `home_store.go`). No BOM, works today.
- **Optional branded wireless ESP32 IR-learn puck** — justified *only* by
  learn-capable IR (phone IR is blast-only/can't-learn) + 433MHz. **BLE/WiFi,
  charger-powered — not USB-OTG** (don't tie up the always-on phone's charge
  port; dodge Android USB-serial permission friction). Late accessory SKU, not
  a product.

## §9 Place-shift TV / satellite kit (normie wedge) — and its hard boundary

A concrete, demoable wedge for the orphaned **Slingbox-refugee / diaspora**
market ("watch my own home TV/satellite on my phone from abroad"). Reuses
`capture.go` (ffmpeg → MJPEG/WebRTC + HDCP-black detection), `ops_stream.go`,
the WebRTC Opus stack, the kumanda IR stack, and **direct-first relay** (P2P
home stream; relay only as metered fallback). Shares the *same* WebRTC
stream-and-control pipe as the assistant last-mile (§4) — building one
strengthens the other.

**Kit = commodity bundle:** USB HDMI capture card + commodity/ESP IR + the
Yaver app. Bundle and integrate; never tool.

**Money:** kit (thin-margin wedge) → **metered remote-stream relay
subscription** (the recurring line; video egress has real cost, so metered is
honest not gouging) → assistant credits (same installed household, one-tap
upsell). The TV kit is the cheapest acquisition channel for the assistant.

### The boundary (non-negotiable — this is what makes the kit shippable)

Yaver is **content-agnostic, like OBS.** It passes through exactly what the
hardware gives and does **not** inspect, classify, or police the signal.

- ✅ **Place-shift non-protected sources:** FTA satellite, DVB-T antenna, the
  receiver's own menu/EPG/UI, consoles, your own cameras, your own media
  (Plex/Jellyfin/NAS). Much of diaspora *uydu* content is FTA — this alone is a
  real product.
- ❌ **The code adds NO HDCP/DRM circumvention (no stripper)** and **NO
  geo/IP-block bypass.** HDCP-protected HDMI streams as a black frame
  (`capture.go` detects it) — Yaver streams it as-is, never strips it. Geo-locked
  OTT (TOD/beIN/etc.) is a *licensing "no"*, not a bug to route around.
- ❌ **No inducement.** Yaver must never bundle, recommend, link, or instruct an
  HDCP stripper, nor explain how to defeat a geo-block. Neutral pass-through with
  *zero assistance* is the legal line (content-agnostic tool vs. circumvention
  device / inducer).

**Agnostic by design — to content AND to upstream hardware.** The kit ships
**no splitter / no stripper.** Yaver does not detect, require, or care what
additional hardware a user places upstream of the capture card. If a user
supplies their own gear and feeds Yaver a signal, Yaver streams what arrives —
it neither knows nor asks what produced it. **Whatever the user plugs in, and
the right to capture/stream it, is the user's responsibility, by design.** This
is the OBS/VCR position.

**Legal basis — the Betamax/dual-use doctrine (why neutral pass-through is
load-bearing).** A capture card + Yaver has *substantial non-infringing uses*
(FTA satellite, antenna, EPG/menu, consoles, own cameras, own media, OBS-style
production), so the tool is protected from secondary liability (*Sony v.
Universal*) **precisely because it stays neutral and non-inducing.** Note the
two distinct legal tracks, so no future contributor over-reads this:
- *Secondary liability (dual-use)* protects **Yaver and the capture card.** ✅
- *Anti-circumvention (DMCA §1201 / EU InfoSoc)* still reaches **the user's act**
  of defeating a TPM, and specifically the **stripper device** (no substantial
  non-infringing use → no dual-use cover). That act is the user's, with the
  user's own gear — **not Yaver's**, because Yaver does not provide, bundle,
  recommend, or instruct it. "Technically possible with the user's own splitter"
  ≠ "Yaver helps."

**Why the hard line holds:** DRM circumvention + geo-evasion are illegal,
expose the founder personally, are existential for a public repo (lawsuit +
store ban), and harm a third-party rightsholder — every one a CLAUDE.md hard
rule. "It's my own subscription" does not create an exception. The kit is
shippable *only* while Yaver stays the neutral, dual-use, non-inducing tool.

### Policy surface (one acknowledgment, not littered warnings)

Per CLAUDE.md (*"a terse diagnostic hint is fine; do not litter the code/UI with
warnings"*):

- **One** first-run / kit-setup **acceptable-use acknowledgment** (logged):
  *"You're responsible for what you capture and stream, for any additional
  hardware you connect, and for your right to do so. Yaver is content-agnostic,
  passes through what your hardware provides, and adds no DRM/HDCP
  circumvention."* One-time, not a per-stream nag.
- Keep only the **terse HDCP-black diagnostic** inline ("source appears
  protected"). No moralizing in the UI.
- Marketing: **"your own TV, anywhere — what your hardware gives."** Never
  "unlock TOD/beIN abroad."
