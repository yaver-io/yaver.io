# Self-Improving MCP for Driving Mobile Apps (redroid)

> Status: architecture, design (2026-06-17). Specializes the Personal Agent Gateway
> (`docs/yaver-personal-agent-gateway.md` §18) to the **mobile-app / redroid** engine.
> Key reuse insight: **Yaver's AI app-*test* agent IS this machinery** — testing an app and
> *driving* an app are the same loop (observe → act → verify), pointed at different goals.

## 1. Thesis

Your **mobile apps become a self-improving MCP tool surface.** A redroid instance (logged into
your apps) is driven by an AI loop that learns each app's screens, compiles reliable flows,
heals them when the app changes, and exposes `get/add/update` per app as MCP tools. It runs
**remote-only** (managed cloud you never touch) and **improves from every use**.

EV is one connector ("is the Eşarj free?"); the architecture is general (Misli, bank, broker, …).

## 2. Why mobile ≠ web (the constraints that shape everything)

| | Web (Playwright) | Mobile (redroid) |
|---|---|---|
| Structure | DOM + CSS selectors | **accessibility tree** (uiautomator nodes) + **pixels** |
| Read a screen | `extract_text`/`get_dom` | `droid_ui_texts` (a11y) + `droid_frame` (vision) |
| Locate element | CSS/role | resource-id / content-desc / text / bounds / **vision** |
| Act | click/type | tap / type / swipe / back at node or coordinate |
| Persistence | `storageState` cookie | **golden snapshot** of the logged-in device |
| Drift source | layout / A-B test | **app version bumps** (UI changes wholesale) |

Implications: there is no stable selector language → **resilience must be multi-strategy +
vision-backed**; persistence is a device snapshot not a cookie; and app updates are a
first-class drift event the system must detect and adapt to.

## 3. The Screen abstraction (the core data model)

Unify the accessibility tree and vision into one model so the agent reasons about "where am I"
robustly:
```
Screen {
  nodes:      [{ role, text, contentDesc, resourceId, bounds, clickable }]   // from droid_ui_texts/uiautomator
  pixels:     frameRef                                                       // from droid_frame
  signature:  ScreenSignature   // robust fingerprint: salient resource-ids + text shape + layout hash + vision embedding
  appPkg, appVersion
}
```
- **ScreenSignature** is the key idea: a fuzzy fingerprint that recognizes "this is the
  station-detail screen" across minor changes. Flows are keyed to signatures, not pixel-exact
  layouts, so small UI shifts don't break replay — and a *big* shift (signature miss) is the
  trigger to self-heal.

## 4. Action + verify model
```
Action = tap(target) | type(target, text) | swipe(dir) | back | wait(cond)
target = byResourceId | byText | byContentDesc | byBounds | byVision(prompt)   // tried in resilience order
```
- **Every action is followed by a re-observe + verify**: did the screen advance to the expected
  next signature / did the expected state change appear? Trust the *observed* result, never the
  tap. Idempotency where the app allows.

## 5. Capabilities = MCP tools (per-user, dynamic)
Each app advertises `get/add/update` capabilities, each compiled to a **Flow** + an
**answerSchema** (what structured data to extract). These register as MCP tools on the user's box
(`gw_<app>_<capability>`) — the host AI (in-car voice, Claude Code) sees your apps as tools.
```
Flow { capabilityId, appPkg, steps:[{ expectSignature, action, fallbacks[] }], answerSchema, version }
```

## 6. The self-improvement loop (the heart)

```
        ┌──────────────────────────── per interaction ───────────────────────────┐
OBSERVE → ACT → VERIFY → LEARN → (GENERALIZE / SCORE) → next
   │        │       │        │
 Screen   Action  re-obs.  on success: record concrete trace
                            on failure: vision-LLM re-locate → heal → update Flow
```

What "learns" concretely:
1. **Trace → Flow compiler.** First successful run of a capability is recorded as a concrete
   trace (signatures + actions + which inputs varied). The curator parameterizes it into a
   reusable Flow with selector fallbacks. (Reuse the **vibe recorder** + record/codegen.)
2. **Screen-signature self-heal.** On replay, match current Screen to the expected signature; on
   a miss (app changed), the **vision-LLM re-locates** the target by intent, the Flow is updated,
   and the fix is recorded. (Reuse `testkit_self_heal_selector`.)
3. **Outcome reinforcement.** Success/fail + post-verify update a **reliability score** per
   capability and bias the resilience order toward the selector strategy that's been working.
4. **Correction learning.** When you correct it ("no, the *other* button"), record it; never
   repeat the mistake.
5. **App-version awareness.** Read the package version; keep per-version Flow variants; on a
   version bump, **proactively re-validate** READ flows before they're needed.
6. **Cross-app transfer.** Patterns learned once (login screens, OTP entry, list extraction,
   pull-to-refresh) bootstrap *new* apps faster — the system gets better at wrapping apps it has
   never seen.

## 7. Exploration agent (capability discovery, READ-ONLY)
A bounded agent that **safely explores** an app's read-only surface (lists, detail screens,
settings *views*) to propose new capabilities ("this app also shows session history — add as
`get`?"). **Never triggers an ACT during exploration** (no buttons that spend/submit/delete). It
expands the tool surface without risk; proposals go to the curator → you.

## 8. The curator (bounded self-improvement)
A periodic agent (the completeness-critic pattern) that reviews usage and:
- **auto-applies (low blast radius):** selector heals, READ-flow refinements, reliability
  re-scoring, preference learning, screen-signature updates.
- **requires your confirm:** any **new ACT/financial capability**, **auth changes**, **policy/
  spend-cap changes**. A self-rewriting system must never silently grant itself "transfer money".
- All changes **versioned, reversible, audited**.

## 9. The big reuse: the AI app-test agent *is* this
Yaver already built (per `docs/yaver-ai-app-test-agent.md`): redroid + the test brain
(T1 + oracle bank), `droid_*` UI driving, `testkit` + self-heal selectors, the `yaver-base`
warm golden snapshot, the catch→fix→reload→re-verify loop, `ops_qa`. **"Catch a regression on a
screen" and "reliably accomplish a task on a screen" are the same observe→act→verify machinery.**
So this architecture is largely a *re-aiming* of existing organs:

| Need here | Existing organ |
|---|---|
| Drive UI | `droid_frame` / `droid_input` / `droid_ui_texts` (`droid_interactive.go`) |
| Logged-in persistence | `yaver-base` golden snapshot |
| Self-heal selectors | `testkit_self_heal_selector` |
| Record → flow | vibe recorder / codegen |
| Observe + oracle | TestBrain T1 + oracle bank |
| Cheap/vision inference | `models_*` + multimodal (`robot_camera` image-tool pattern) |
| Remote-only operation | `rd/stream`+`/rd/input`, `device_broadcast_command` |

## 10. Remote-only, safety, privacy, open-core
- **Remote-only:** all interaction via `droid_frame`/`droid_input`; human steps (2FA, captcha,
  first login) surface via remote-view on your *own* phone — never a physical tap on the
  managed-cloud device (parent §19).
- **Policy Guard:** your apps/accounts only; human cadence; back off + record on
  anti-automation/captcha-storm; **no evasion, no auto-captcha-solve** (the human solves it live).
- **Privacy:** Screens, signatures, flows, traces, reliability stats, sessions — your most
  personal data → **local-first / vault, never Convex.** The personalized MCP runs on your box.
- **Open-core:** the *engine + learning framework* is generic/open Yaver; your *specific* app
  flows, credentials, and learned preferences are private.

## 11. Build path
1. **Screen model + signature** over `droid_ui_texts`+`droid_frame` (the perception layer).
2. **Flow store + replay** with multi-strategy locate + post-verify.
3. **Trace→Flow recorder** (one READ capability on one app, e.g. an EV "is-it-free").
4. **Self-heal on signature miss** (vision re-locate) — the durability proof.
5. **MCP registration** of compiled capabilities (per-user dynamic tools).
6. **Exploration agent** (READ-only discovery) + **curator** (gated improvement).
7. ACT capabilities (parent §16 consent model) — last, confirm-gated.

Each step reuses an existing organ; the net-new is the **Screen-signature self-heal loop** and
the **Flow store/curator** that turn one-off app-test runs into durable, improving tools.

## 12. Real-device engine — passing Play Integrity honestly (second-hand phones)

redroid is detected and blocked by **hardened backends** (banks, betting-KYC, DRM, wallet) via
**Play Integrity / SafetyNet** hardware attestation — and that must NOT be defeated (evasion +
near-impossible). The legitimate answer for integrity-gated apps: a **real, Play-certified
second-hand phone**, which produces genuine hardware attestation because it *is* a real device.
Not evasion — satisfaction.

### Engine
A third connector engine alongside `redroid`/`api`:
```
Connector { engine: "device", deviceRef: <real phone enrolled in the user's account> }
```
**Reuses the phone-as-box agent** (`libyaver.so` on `127.0.0.1:18080`, relay-reachable) +
`droid_frame`/`droid_input`/`UiTexts` — so the invoke engine, auth broker, human-gates, and
self-heal all work unchanged. Only provisioning + persistence differ.

### Integrity ≠ the whole story (honest boundary)
A real device clears **attestation**, but NOT: datacenter-IP fraud flags (→ use **residential
WiFi / cellular SIM**), **anti-automation-framework** detection (ADB/accessibility/instrumentation —
top-tier banks block automation *regardless* of device → stay **API-only**), or **behavioral/ToS**
(human cadence, own account, occasional). Real phone = big step, not a skeleton key.

### Device selection (a real gatekeeper)
Must be: **stock ROM, bootloader locked, verified boot, NOT rooted** (rooting breaks integrity +
is evasion), **Play-certified**, **recent enough** (Google deprecates old devices from integrity
over time), and have **StrongBox** if an app needs `STRONG`. **Verify a Play Integrity pass
before enrolling.**

### Provisioning + sync flow ("trigger downloads + keep synced")
1. Acquire + **verify integrity passes**; reject unlocked/rooted/too-old.
2. Enroll: Yaver agent → `yaver auth` → device in account, on **residential/cellular IP**.
3. **App provisioning:** trigger installs **from the Play Store** (drive Store UI / `market://`
   intents — Play install source, never sideload) for the target apps → first launch.
4. Login via the **auth broker** (OAuth/TOTP/SMS/push); irreducible human steps surface via
   **remote-view to the user's OWN phone** (no physical access to this device either).
5. Persistence: no container snapshot — the phone **stays logged in** (real apps keep sessions);
   Play auto-update + session keepalive + re-auth-on-expiry via human-gate.
6. Operate: connector invoke runs here, passes integrity because the device is real.

### Fleet + cost
Multiple second-hand phones = a personal real-device farm at residential vantages (home /
friend-roster runner). Real ops cost (power, network, manual touch, integrity aging) — the
tradeoff vs redroid's zero-marginal-cost-but-Tier-B-cap.

### Block detection (complements self-heal)
An integrity / "device not supported / can't verify it's you" screen is a **block**, NOT a
UI-drift heal target. The block detector must distinguish them → **stop, don't retry, don't
evade, surface to the user, suggest API or a real-device engine.** (Small extension of
`detectBlockSignal`.)

### Engine ladder (refined)
`official API → real second-hand phone (integrity-gated apps, residential IP) → redroid (Tier
B/C reads) → blocked: surface, never evade`. Honest lines: no root/hide/spoof, residential IP,
own accounts, human cadence, Play install source; most-hardened apps stay API-only; the user's
real account is at stake → confirm-gate sensitive ops.

## 13. The appliance product — "plug an old phone in and forget it"

**Consumer crystallization:** a €50 second-hand phone plugged into a charger at the user's home
(always-on, residential WiFi, Yaver agent) becomes the user's personal automation node — the
*hands*. The user talks to their AI (phone/web/car/glasses) via STT/TTS; the AI runs their
**personal MCP** (`gw_*` tools = their apps on the forgotten phone) to do **things they already
do manually** — zero behavior change, zero server, zero cloud setup.

### Economics (honest)
The relay is the only cloud piece and is **cheap + self-hostable** → almost a non-business alone.
The REAL cost is **AI inference** (STT→agent→TTS). Two shapes:
- **Near-free/open:** user BYO phone + **BYO AI key** (or local models) + self-hosted relay →
  Yaver ≈ free open-source software.
- **Managed:** Yaver hosts relay (reachability) + **meters AI inference** (per-token resale) +
  monitoring/support → small monthly + metered AI.
Marginal infra cost ≈ 0 because compute = the user's phone + their electricity (~1–3 W). The
product sells **reachability + the AI brain + integration breadth**, NOT compute. (Corrects
"just charge for relay" — relay is trivial; inference is the meter.)

### Moat
Siri/Alexa/Google are walled to *integrated* services; they can't drive the arbitrary
third-party apps you're personally logged into. Yaver's screen-automation + real-device-integrity
+ personal-MCP can — legitimately, because it's your real device + real login. Breadth +
appliance simplicity + self-heal durability = the defensible part.

### Make-or-break constraints (NOT footnotes)
1. **Security blast radius (#1):** a forgotten phone holds ALL the user's logins. Demands device
   encryption + screen lock, **network-jail (relay-only, RFC1918-blocked, operator-fleet jail)**,
   passkey on the controlling account, full audit, **per-app scoping** (don't casually add the
   primary bank). "Forget it" ≠ forget the risk.
2. **Battery/fire safety:** an old lithium phone at 100% charge 24/7 for months **swells/fails
   dangerously.** Use charge-limiting/battery-bypass, or accept degradation + fire-safe placement.
   Must be in-product, not glossed.
3. **Integrity aging:** Google deprecates old devices from Play Integrity over time → phone gets
   blocked by hardened apps eventually → plan periodic phone refresh. "Forget it" has a shelf life.
4. **Reliability:** consumer phone 24/7 = crashes, session expiry, OS updates, reboots, WiFi drops.
   Needs **node health monitoring + remote recovery + "node is down" alerts.** Self-heal fixes UI
   drift, NOT a dead node.
5. **ToS/account risk:** always-on node invites behavioral scrutiny → personal cadence, own
   account, human-ish timing; most-hardened apps stay API-only.

### Reusable vs net-new
Reusable: phone-as-box agent, relay (self-hostable), `droid_*`, auth broker, connector model,
self-heal, STT/TTS loop, multi-surface clients, managed-metering spine. Net-new: **appliance
onboarding** (old phone → node in minutes), **node health/recovery + down-alerts**, **relay/
inference billing meter**, **battery-safety handling**, consumer voice-first wrapper.

### App sync / provisioning (no one-by-one manual install)
The user lists the apps once; Yaver installs + logs them all in. **No silent "install any app"
API exists for normal apps** (Google forbids it = malware) — only two legit ways:
- **Path 1 — Play UI automation (any phone, no reset):** on-node agent opens `market://details?id=<pkg>`
  → drives the Store UI (`droid_*` tap Install / accept) → **Play install source** (trusted, not
  sideload). Triggered remotely from the user's Yaver app → relay → node agent installs locally.
  Reuses `adb_*`/`droid_*`/`device_install`. Irreducible prompts → human-gate to the user's phone.
- **Path 2 — Device-owner managed mode (appliance gold path):** factory-reset + enroll Yaver as
  **device owner** (`adb dpm set-device-owner` / QR / zero-touch) → **silent install/update/remove**
  via managed Play + `PackageInstaller`, declaratively ("node should have [apps]" → reconcile).
  Cost: reset+enroll at onboarding; managed-device signal visible (most apps ignore it).
- **Sync model:** desired app-set per node → reconcile (install missing → update → remove) + Play
  auto-update. **Install ≠ logged in** → auth-broker login runs per app after install
  (OAuth/TOTP/SMS/push → human-gate). Play UI drift → reuse self-heal. Device-owner's silent-install
  power → controlling account MUST be locked down (passkey + audit).

## 14. Inference layer — a 3-way privacy/capability/cost dial

The personal MCP reads SENSITIVE screens (bank/health/messages). The real decision isn't "which
model" but **where sensitive screen content is processed.** Three tiers, by privacy:

| Tier | Runs on | Sensitive data goes to | Capability | Cost |
|---|---|---|---|---|
| Most private | node / user's home GPU (local models) | nobody | limited (small models) | user electricity |
| Middle ("security") | **Yaver self-hosted GPU** (open models: Qwen-VL/Llama) | Yaver boundary only | good, not frontier | GPU → **metered** |
| Most capable | frontier API (Claude), BYO key | the AI vendor | best agentic+vision | per-token (user) |

- **Honest:** Yaver-GPU is more private than a third-party API but is a **trust-shift to Yaver,
  not zero-trust**. Max privacy = the user's own hardware. Don't oversell the middle tier.
- **Only part of the loop is sensitive+expensive:** STT (whisper) + TTS run local/cheap; the
  **agentic LLM + vision screen-reading** is the costly, sensitive part the GPU serves.
- **Sensitivity-aware routing (smart default):** sensitive screen content → in-boundary
  (local/Yaver-GPU); high-level planning on **redacted/abstracted** state → frontier. Gets
  frontier reasoning while raw sensitive pixels stay in-boundary + minimizes GPU cost.
- **Architecture:** an inference-provider abstraction `{ byo_api | local | yaver_gpu }`,
  user-selectable + **per-connector override** (primary bank → local-only or API-only-no-screen).
  Reuses `models_*` + GPU-rental-orchestration + managed-metering. Net-new = provider router +
  sensitivity policy + GPU inference endpoint.
- **Tradeoff triangle:** capability ↔ privacy ↔ cost — open-on-Yaver-GPU trades some agentic
  reliability for privacy; the hybrid softens it. Metering must be honest/auditable (GPU-seconds).
