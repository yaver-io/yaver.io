# Yaver TV & Car Deployment Roadmap

> Status: design + active scaffold (2026-06-17). Companion docs:
> `docs/yaver-car-voice-coding.md` (voice remote-coding), `docs/yaver-ev-charging-turkey.md`
> (EV data layer). This file is the deployment plan across TV + Car surfaces.
>
> **Source-of-truth caveat (see CLAUDE.md):** the EV verbs are currently *advertised*
> (`mcp_tools.go`) and *wired* (`httpserver.go:8494`) but the handlers are **stubs**
> (`mcp_dropped_stubs.go:40` → `droppedMCPStub`). Treat "Yaver has EV" as false until
> M-EV1 lands. Re-grep before trusting any line below.

---

## 0. TL;DR — priority ladder

Ranked by (real fit × low effort × approval realism × you-can-test-it):

| # | Surface | Category Yaver legitimately fits | Effort | Approval | Ship order |
|---|---------|----------------------------------|--------|----------|-----------|
| 1 | **Android TV** | (its own app, no category gate) | Low | Routine | **First** |
| 2 | **Android Auto — Messaging** | Messaging (voice coding channel) | Low | No entitlement | **First** |
| 3 | **Android Auto — EV Charging** | `CHARGING` | Med | No entitlement | Second |
| 4 | **CarPlay — EV Charging** | `carplay-charging` entitlement | Med | Form, grantable | Second |
| 5 | **Apple TV (tvOS)** | (its own app) | High (RN fork) | Routine after fork | Third |
| 6 | **CarPlay — Communication** | `carplay-communication` | Med | Strict review | Optional |
| 7 | **Android Auto — IoT** | `IOT` | Med | No entitlement | Optional |
| ✗ | CarPlay/AA general dev UI | none | — | **Impossible** | Never |

The two car *products* that justify all the car plumbing: **EV charging (Turkey-first)**
and **voice remote-coding**. Everything else rides on the same binary.

---

## 1. The two constraints that shape everything

### Car: the screen never renders your UI
CarPlay and Android Auto do **not** run React Native screens. They render only
Apple/Google **templates** (list / grid / map / point-of-interest / now-playing /
messaging) that you fill with data. Custom rendering is hard-blocked for driver
distraction. **Consequence:** you are not "putting Yaver in the car." You ship a
narrow, template-only companion *in the same binary* that exposes one allowed
capability. The dev-tools brain stays on phone/web.

### TV: focus, not touch
Android TV and tvOS are 10-foot, D-pad/remote, focus-driven. Every interactive
element needs explicit focus handling. Your touch-first screens are unusable as-is
on TV — but TV *can* render real UI (unlike car), so it's a re-layout problem, not a
category problem.

---

### 1.5 Open-core boundary — Talos → Yaver, never the reverse
- **Dependency law:** Yaver (this repo, public) must compile and be fully useful with **zero
  knowledge of Talos or OCPP**. Talos *consumes* Yaver via public surfaces (MCP verbs, SDK
  tokens, HTTP API, generic driver interfaces). **Yaver never imports Talos.**
- **Generic → open Yaver:** EV station discovery (keyless public data), connector/vehicle
  taxonomy, the `ChargeController` interface (the empty seam), navigate/deep-link, all car/TV
  templates, the voice-coding loop, STT/TTS, the remote-runner job/update plumbing, the
  Policy-Guarded collection *framework*.
- **Private IP → never in this repo:** the **OCPP** protocol implementation, charge
  start/stop control, charger fleet mgmt, Talos integration, domain configs, production data,
  network-specific private adapters.
- **Seam (preferred): out-of-process.** The OCPP/Talos controller runs as a separate service;
  Yaver calls it as a registered external/peer tool (`mcp_external` / `acl_call_peer_tool`).
  Zero proprietary code in the open binary. Fallback: in-process private overlay package that
  registers a `ChargeController` against a discovery-only default.
- **Leak guard:** repo is public — committing OCPP/Talos code publishes it (cf. the betting
  cell moved to private `../yaver-bet` + history rewrite). Never let it enter this tree; a CI
  grep-guard (like `convex_privacy_test`) is cheap insurance.

---

## 2. Surface-by-surface

### 2.1 Android TV — the easy win (do first)
- Same Play Console, same AAB pipeline (`scripts/deploy-playstore.sh`).
- Add leanback launcher entry + `<uses-feature android:name="android.software.leanback" android:required="false"/>` + TV banner (320×180).
- Real work = focus-based navigation + 10-foot layout of a **subset** of screens
  (device list, agent status, remote-desktop view, capture/Apple-TV dashboard — these
  are "lean-back watch" surfaces that map well to TV).
- No entitlement. Routine review if it runs on a remote without crashing.
- **Effort:** days–2 weeks. **Gate:** none.

### 2.2 Apple TV (tvOS) — heaviest engineering (do third)
- Separate Xcode target. Stock RN has no tvOS support → need the **`react-native-tvos`**
  fork (your own notes flag this needs an ADR). Fork swap touches the whole native build.
- Implement the focus engine (parallax, focus guides).
- Then normal App Store review; a legit remote-control/dev app is accepted with Siri Remote support + HIG.
- **Effort:** weeks. **Gate:** ADR on the RN fork *before* any code.
- Natural tvOS feature set: the existing Apple TV control + capture-card dashboard
  (`appletv.go`/`capture.go`) is literally a TV-native experience — ship that slice first.

### 2.3 Android Auto — cheapest car door (do first, alongside Android TV)
- **Messaging path = no Car App Library, no entitlement.** `MessagingStyle`
  notifications + `RemoteInput` reply + mark-as-read. This is the **voice remote-coding
  channel** (§3.2). Nearly free from RN. Passes the car-quality review.
- **CHARGING path** = Car App Library `CarAppService` + `PlaceListMapTemplate` (Kotlin,
  native). Category generally available, no entitlement.
- **IOT path** = same library, `GridTemplate`/`ListTemplate` for device control. Optional.
- Native Android Auto code must be injected via an **Expo config plugin** — `expo prebuild
  --clean` regenerates `mobile/android` and would wipe hand-edited files.

### 2.4 Apple CarPlay — entitlement-gated (do second)
- **EV charging:** request `com.apple.developer.carplay-charging`. Templated
  (`CPListTemplate`/`CPPointOfInterestTemplate`/`CPMapTemplate`). Real charging apps get
  it. **Without the granted entitlement the CarPlay scene never loads** — this gate is the
  schedule risk, not the code.
- **Communication** (`carplay-communication`): the voice-coding channel as a CarPlay
  messaging app via SiriKit intents — strictest review; optional.
- **Driving Task** (`carplay-driving-task`, iOS 16+): the most permissive door, closest
  CarPlay analog to Android's IOT — viable for a *narrow* device-control glance.
- RN doesn't render in CarPlay; bridge templates from JS with **`react-native-carplay`**
  (big effort-saver for this stack).

---

## 3. The two car products

### 3.1 EV charging — Turkey-first (your testbed)
You own a **Togg** and your sister has an **MG ZS EV** — both **CCS2** (DC fast) + **Type 2**
(AC). That makes Turkey the testbed and **Trugo** (Togg's own network) the natural primary.
Deep dive in §4.

### 3.2 Voice remote-coding from the car
The killer reframe: **your coding agent is a "contact you talk to."** Speak a command →
STT → dispatch to the agent on a remote box (`code_dev`/`remote_exec`/agentic loop) →
summarize result to one sentence → TTS over car audio. To Apple/Google it's a
messaging/voice app; to you it's hands-free remote coding. Same surface as §2.3 messaging.
**Safety rule:** async command + high-level *status* readback only — never read diffs/code
aloud while driving. Full design in `docs/yaver-car-voice-coding.md`. Tiering:
- **Tier 0 (ships today, no entitlement):** phone app + Bluetooth car audio + push-to-talk /
  Siri Shortcut voice loop.
- **Tier 1 (cheap):** Android Auto MessagingStyle channel.
- **Tier 2:** CarPlay `carplay-communication` (strict review).

---

## 4. EV charging — Turkey deep dive

### 4.1 Vehicles → connector defaults
| Vehicle | AC | DC fast | Default filter |
|---|---|---|---|
| Togg T10X (yours) | Type 2 | **CCS2** | CCS2, ≥120 kW |
| MG ZS EV (sister) | Type 2 | **CCS2** | CCS2, ≥50 kW |

Ship vehicle presets so "find a charger" defaults to CCS2 in TR. CHAdeMO is legacy in TR —
keep it selectable, not default.

### 4.2 Turkish networks (seed list for `ev_networks`)
**Trugo** (Togg, 180–300 kW, nationwide — primary), **ZES** (Zorlu), **Eşarj** (Enerjisa),
**Sharz.net**, **Voltrun**, **Beefull**, **Astor Şarj**, **On Şarj**, **Otowatt**,
**PowerCity**, **Assan Şarj**. Public chargers in TR are overwhelmingly CCS2/Type 2.

### 4.3 Data source — keyless & respectful (CLAUDE.md Policy Guard)
- **Primary: OpenChargeMap** — community DB, good Turkey coverage, free API key (register;
  not a scrape). Returns location, connectors, power, network/operator, status hints.
  **Honor rate limits; back off on 429/403; never spoof UA.**
- Do **not** scrape each network's app/API to defeat bot detection. If a network has an
  official/keyless POI endpoint, prefer it; otherwise OpenChargeMap is the lawful baseline.
- Cache station metadata locally; never push customer location/IP to Convex (privacy contract).

### 4.4 What's realistically in-car (honest scope)
- ✅ **Find** stations (nearby / along route), filter by connector/power/network.
- ✅ **Availability/status** where the source exposes it (best-effort; OCM status is sparse).
- ✅ **Navigate to** (hand off to the car's nav).
- ⚠️ **Remote start/stop a charge:** **not a public-Yaver capability.** Open Yaver ships only
  the generic `ChargeController` *interface* (the empty seam) + a "control unavailable" default
  and a deep-link-to-network-app fallback. The actual start/stop runs behind a **proprietary
  OCPP driver** that plugs into that seam from a **private** overlay/service (your IP — see §1.5).
  Public Trugo/ZES/Eşarj remote-start would anyway need *their* account/partnership; don't promise it.

### 4.5 Testability with your hardware
- Drive to a **Trugo** site with the Togg → verify "find + status + navigate" end-to-end.
- MG ZS EV gives a second CCS2 profile to validate vehicle presets.
- Home/AC charger (if any) → start/stop demo **via the private OCPP driver behind the seam**
  (the only legit start/stop path; not part of the open binary — see §1.5).

---

### 4.7 Richer EV data via self-hosted collection (Policy-Guarded)
Open Yaver's default EV source is keyless **OpenChargeMap** (find/status). **Richer real-time
availability** (live ZES/Eşarj status, **Lixhium**-style aggregators, your own session history)
is a **self-hosted/managed collection task — NOT a public-binary default** — and rides the
Policy Guard (`access_policy.go`) hard:
- Prefer official/keyless APIs first.
- Else the **user's own authenticated session** (you're a paying customer — it's *your* data),
  run on the **user's own device / residential vantage** (`collection_plan` runtime), at human
  cadence. **Never** a 24/7 datacenter scrape loop.
- **No UA spoofing, no bypassing robots/rate-limits/WAF, no IP rotation.** Back off on
  403/429/451 and **stop** — a block is a "no". Blocks recorded as findings, never routed around.
- Local-first (`collection_store`, vantage-keyed); never to Convex; never customer IPs.

This is the legitimate "self-hosted gets real data" path — *your account, your vantage,
respectfully* — distinct from abusively scraping a third party. Slots into the existing Task
Packages + collection-runtimes infra. The collection *framework* is generic/open; any
network-specific private adapter is IP and stays out of this repo (§1.5).

---

## 4A. System shape — thin car, heavy remote

The car is **I/O only** (voice in, voice/notification out). All real work runs on a **remote
runner** — the user's self-hosted box or Yaver-managed cloud. Relay carries it.

```
CAR (CarPlay/AA: STT mic + TTS speaker + messaging/list template)
  │ voice command                      ▲ spoken status update
  ▼                                    │
YAVER RELAY (QUIC, password, pass-through — never stores task data)
  │
REMOTE RUNNER (self-hosted OR managed cloud):
  • coding agent      (code_dev / agentic loop)      → edits, builds, fixes
  • redroid app-test  (ops_qa + testkit)             → runs the app, catches regressions
  • chromedp e2e      (collection/browser engine)    → web flows
  • EV collection     (Policy-Guarded, own account)  → real availability
  └──> async UPDATES → summarize-to-one-sentence → TTS / MessagingStyle → CAR
```

- **Async, not interactive.** Speak a task; the runner works for minutes; updates arrive as
  events ("tests green, 142/142"; "redroid caught a crash on login"). Never wait at a light.
- **Confirmation gate for risky verbs.** deploy/push/rm get a spoken confirm — never
  auto-fired from a transcript (STT mishears; a misheard "deploy" is a disaster).
- **Reuses:** relay, `code_dev`/`remote_exec`, `ops_qa`+`testkit`+redroid, chromedp engine,
  managed cloud, Task Packages runner. **Net-new = voice loop + summarize-to-one-sentence +
  car templates** (voice pipeline itself is ~90% pre-existing — see §8).

---

## 5. Milestones

Dependency-ordered. ⛔ = blocked on an external gate (entitlement/ADR).

| ID | Milestone | Surface | Depends | Effort | Gate |
|---|---|---|---|---|---|
| **M-EV1** | Implement real `ev_charging`/`ev_networks`/`ev_connector_types` (OpenChargeMap-backed, TR networks, CCS2 presets) — replace the stub | agent | — | M | — |
| **M-V0** | Tier-0 phone voice loop (push-to-talk → STT → dispatch → TTS) | mobile | — | M | — |
| **M-AT1** | Android TV leanback target + focus nav for device/agent/AppleTV-dashboard subset | mobile | — | M | — |
| **M-AA1** | Android Auto **Messaging** channel (voice-coding) via Expo config plugin | mobile | M-V0 | M | — |
| **M-AA2** | Android Auto **CHARGING** `CarAppService` (Kotlin templates) | mobile | M-EV1 | M | — |
| **M-CP1** | CarPlay **EV charging** scene via `react-native-carplay` | mobile | M-EV1 | M | ⛔ `carplay-charging` |
| **M-CP0** | CarPlay entitlement request + justification text | ops | M-EV1 | S | ⛔ Apple form |
| **M-TV1** | tvOS ADR (react-native-tvos fork decision) | doc | — | S | — |
| **M-TV2** | tvOS target + focus engine + appletv/capture dashboard slice | mobile | M-TV1 | XL | ⛔ ADR |
| **M-AA3** | Android Auto **IOT** device-control templates (optional) | mobile | — | M | — |
| **M-CP2** | CarPlay **Communication** voice-coding (optional) | mobile | M-V0 | M | ⛔ `carplay-communication` (strict) |

**Critical path to "EV charging in both cars + voice coding":**
M-EV1 → (M-AA1 ∥ M-AA2 ∥ M-V0) → M-CP0/M-CP1. Android side has **no external gate** and is
fully shippable before Apple grants anything.

---

## 6. Entitlement & review realism

| Item | Reality |
|---|---|
| Android TV | Routine Play review. No gate. |
| Android Auto (any category) | No entitlement; declare category in manifest, pass car-quality review (tested on a head unit for distraction/crashes). |
| `carplay-charging` | Request form, justify you're a real charging app → granted to legit third parties. **Scene won't load without it.** |
| `carplay-communication` | Strictest CarPlay review; Apple wants evidence you're a genuine messaging platform. Treat as optional. |
| `carplay-driving-task` | Most permissive door, but Apple still judges "appropriate while driving." |
| tvOS | Reviewed like iOS; legit app accepted. Gate is *engineering* (RN fork), not policy. |
| ✗ general dev UI in car | Not a bureaucracy you grind through — a closed category list Yaver doesn't fit. Don't build it. |

**Do-no-harm note:** EV data collection rides the Policy Guard — keyless/official APIs,
honor 429/403, no UA spoofing, no IP rotation. The collection layer records blocks as
findings; it must never route around them.

---

## 7. What you can test locally (no review needed)
1. **M-EV1** — `ev_charging` against OpenChargeMap for your home coordinates; verify Trugo
   stations + CCS2 filter return real data.
2. **M-V0** — phone mounted, Bluetooth to car audio, push-to-talk: "on magara, run the
   tests" → spoken status back. Zero entitlement.
3. **M-AT1** — Android TV emulator (or a physical Android TV / Chromecast-with-GoogleTV).
4. **M-AA1/AA2** — **Desktop Head Unit (DHU)** emulator for Android Auto; no car needed.
5. Real-world: drive the Togg to a Trugo site for the end-to-end EV demo.

---

## 8. Build-status snapshot (re-grep before trusting)
| Piece | State |
|---|---|
| `ev_charging`/`ev_networks`/`ev_connector_types` | **IMPLEMENTED** (`desktop/agent/ev_charging.go`) — live OpenChargeMap discovery (keyless, Policy-Guarded), TR networks, CCS2 + Togg/MG presets; `ChargeController` seam in `charge_controller.go` (discovery-only default). M-EV1 ✓ |
| OCPP charge control | **NOT in public Yaver** — private IP behind generic `ChargeController` seam, out-of-process preferred (§1.5) |
| STT/TTS voice pipeline | **~90% exists** — `speech.ts` (whisper.rn+cloud STT, expo-speech TTS), `agentVoice.ts` (WS loop), `voice_dispatch.go` (transcript→remote-task→readback), `quic.ts sendTask(codeMode)` |
| Car voice loop (record→STT→dispatch→summarize→TTS) | **scaffolded** — `mobile/src/lib/carVoiceCoding.ts` + tests (12/12), DI-based, one-sentence summarizer + no-read-code-while-driving guard |
| Remote test runner (redroid `ops_qa`/`testkit`, chromedp) | exists — wire as the async update source |
| Android Auto messaging | `withAndroidAutoMessaging.js` plugin **REGISTERED** in app.json (2026-06-17) + `carMessagingNotification.ts` data layer; **gap = native MessagingStyle module** (degrades to a phone notification until then) |
| **Android TV (M-AT1)** | **READY** (2026-06-17) — `withAndroidTV.js` self-contained (copies `assets/tv_banner.png` 320×180 → `res/drawable-xhdpi`) + **REGISTERED** in app.json; focus-driven `app/tv-home.tsx` lean-back launcher + index/tv-signin routing; plugin unit test 4/4; `expo config --type introspect` confirms leanback+banner+uses-feature land; **remaining = gradle bundleRelease + Android-TV-emulator verify + Play TV submission** |
| tvOS support | **ADR decided → Option B native SwiftUI** (`docs/yaver-tvos-fork-adr.md`, no RN fork); **scaffolded** `tvos/YaverTV/*.swift` (device-code auth + `/ops` LAN client + sign-in/dashboard/remote SwiftUI views) — source-only, needs a one-time Xcode tvOS target (see `tvos/README.md`) |
