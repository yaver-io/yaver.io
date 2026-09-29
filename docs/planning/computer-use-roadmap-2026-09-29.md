# Roadmap — Yaver Operator: never open the laptop

**Status:** planning · 2026-09-29 · companion to
`docs/audits/computer-use-without-opening-the-computer-deep-audit-2026-09-29.md`.

> **Code is the source of truth.** Every "exists / missing" claim behind this
> plan is anchored in the companion audit, which was grepped the same day.
> Re-verify before building. Sizes are estimate ranges for someone fluent in
> this codebase, not commitments.

---

## North star

> **From any Yaver surface, hand an AI a sentence and have it do the work on my
> own machine — and show me what happened. I never open the computer.**

This is the generalization of what already ships: the dev loop works; the GUI
hands (`ghost_*`), the WebRTC desktop target, spoken desktop control, and the
browser toolset now exist. The missing product is the **loop** and the
**governed paths** around them.

### Product framing (2026-09-29, owner)

- **Yaver stays a dev tool.** The PC-operator is an *added capability of the
  same agent*, not a rebrand and not a second product. One agent, one relay, one
  set of surfaces: the thing that already runs tasks/preview/deploy gains hands.
- **The leverage is agent-side (Go).** Improve the one agent to *use the model's
  vision* to operate the machine. The model is the user's existing runner
  (Claude/Codex see pixels; opencode/deepseek get the text adaptation in
  `mcp_vision.go`). No per-surface reimplementation.
- **Do NOT pollute the UI.** Client surfaces already have chat, task output, and
  video recording of what was done. The operator's result is a **step trace**
  that rides those existing lanes. No new screens, no new chips, no walls of
  state — the output contract is "show the answer, not the inventory."
- **Every surface, one mechanism.** Expose it as `ops` verbs + named MCP tools;
  native surfaces reach it through the ops path they already speak. Desktop
  **video** stays web/mobile, but desktop **speech** (`desktop_voice`) already
  reaches watch/Wear/tvOS.
- **SINGLE MACHINE FIRST (owner, 2026-09-29).** The target is one PC used
  *perfectly* — as a development machine or for everyday computer use. Explicitly
  **out of scope for now:** multi-monitor, multi-machine/fleet orchestration,
  and cross-surface parity breadth. All effort goes into making the one machine's
  loop reliable and pleasant, with the browser as a first-class part of it.
- **macOS, Linux, Windows, and browser automation are all in scope** for that one
  machine; the platform with the least validation is Windows (see Tier 5).

### The wedge (why Yaver wins this and a pure remote-desktop tool doesn't)

- **Identity-bound, human-gated.** Consent + audit + approve/deny on a
  glanceable surface. RustDesk/AnyDesk have no policy layer; Browserbase/E2B
  have no "approve on your watch."
- **Breadth, on existing hardware and existing AI subscriptions.** One agent
  binary drives mac/Linux/WSL/Windows, phone, redroid, Pi, TVs, robots, and a
  browser — and never resells tokens.
- **Cost-honest.** Speech-only control and tree-based actuation cost no egress,
  which is what makes the free relay tier viable.

---

## Design principles (from `AGENTS.md` / `CLAUDE.md`, restated for this lane)

1. **Probe the operation, never the inventory.** If the only way to know is to
   attempt it, attempt it. (macOS input is the current false green — G5.)
2. **Every failure carries its route to fix** — detection → named code → visible
   cause → invocable fix, on every surface.
3. **Headless first, then closed loop, and both snowball.** Every item below
   lands a **headless verb** *and* an **arc** in the closed-loop suite that
   would have caught its defect.
4. **Additive only.** New lanes go in `.web.ts` siblings or ops verbs; the
   native phone connect path is untouchable.
5. **Less is more.** Show the answer, not the inventory; advisory never outranks
   the route.

---

## Status — landed on branch `operator-computer-use` (2026-09-29)

| Item | State | Evidence |
|---|---|---|
| **Tier 0.1** ship macOS ghost in the released agent | ✅ landed | `release-cli.yml` + `scripts/build-cli-native.sh` now build darwin with `CGO_ENABLED=1`; full agent verified building with cgo |
| **Tier 0.2** permission truth + honest macOS input | ✅ landed | `ghost/preflight_*.go`, `ghost.Preflight()`, `macInputPreflight()` in `input_darwin.go`; `/rd/status` `permissions`/`controlReady`/`screenReady`; desktop-target `Checks` |
| **Tier 0.3** no remote self-grant of control | ✅ landed | `rdControlPolicyUpdateEnforce` + `ControlConsentSet`; test proven by breaking it |
| **Tier 1.2** discoverable computer-use tools | ✅ landed | `mcp_desktop.go`: `desktop_screenshot` / `desktop_act` / `desktop_elements` / `desktop_operator`; `desktop` added to wedge allowlist + core denylist |
| **Tier 1.1** operator loop (first version) | ✅ landed | `ghost/loop.go` `RunLoop` (bounded observe→decide→act, stall detection, trace) + `ops_desktop_operator.go` verb + `desktop_operator` MCP tool |
| **Tier 1.1b** loop verification (tree-first) | ✅ landed | `LoopOptions.Observe`/`Verify`, `Engine.TreeText`, no-change stop; `visionLocator.VerifyGoal` in `ghost_vision.go`; grounded `verified` vs unverified `none` |
| **Tier 1.5** artifact / file return | ✅ landed | `ops_artifact.go` `artifact_fetch` + `mcp_artifact.go`; `file_fetch` reachable as `artifact_fetch`; roots = project roots + `~/.yaver/artifacts` + `YAVER_ARTIFACT_ROOTS`; symlink-escape test |
| **Tier 4** one vision config path | ✅ landed (partial) | `newVisionLocator` now consults the shared `vision_keys` config, so one setting enables both `vision_analyze_image` and grounding (OpenAI-compatible providers; Anthropic skipped by design) |
| **Tier 1.3** multi-monitor | ✅ macOS · 🅿️ Windows/Linux deferred | `screen_darwin.go` enumerates every active display (`CGGetActiveDisplayList`) with point-space bounds + main flag, and captures per display; verified by `TestDisplaysEnumeratesMac`. `desktop_screenshot`/`ghost_screenshot` take `display=N`; the desktop-screen (WebRTC) target validates the index and still streams display 0 only (ffmpeg region selection not implemented) |
| **Single-PC browser agent** (`browser_operator`) | ✅ landed | `ops_browser_operator.go` + `mcp_browser_operator.go`: a plain-language goal runs a bounded observe→decide→act loop in a REAL local browser (chrome CDP; firefox/safari WebDriver), one grounded action at a time, returning a step trace. Shares the model config with `desktop_operator`. Unit-tested parser + stall detection |
| **Browser engines** (Chromium + Safari + Firefox) | ✅ parity closed | `testkit` already ships geckodriver (Firefox) + `safaridriver` (real macOS Safari, never Playwright WebKit) W3C drivers; the `browser_*` tools routed most actions but `browser_extract_attribute` / `browser_get_dom` / `browser_evaluate` were Chrome/CDP-only — now routed to the WebDriver lane. `selenium_status.engines` reports chrome/firefox/safari readiness with install (`geckodriver`) + enable (`sudo safaridriver --enable`) routes. Guard: `TestBrowserToolsRouteBothEngines` (proven by breaking it) |
| **Tier 1.4** Wayland | ✅ loud refusal (partial) | `screen_linux.go` `Capture` refuses when `platformPreflight()` says capture is unavailable (Wayland-only), instead of returning a blank XWayland root. XWayland sessions still capture X11 clients; the partial-capture caveat is surfaced, not refused |
| Tier 2 governed browser (`web`/`playwright` flow type) | ⏳ next — **blocked on wiring** | needs the `BrowserManager` injected into `gatewayDeps`; today `newGatewayDeps()` is called by free functions (`mcpGatewayQuery`) with no server handle, so the seam must be threaded through first |
| Tier 3 surface parity | 🅿️ deferred by direction | multi-surface breadth is out of scope while we perfect the single PC |
| Tier 4b voice barge-in / stack unification · Tier 5 Windows CI | ⏳ pending (nice-to-have for one PC) | — |

**Nothing is committed.** The branch is a working tree for review; per repo
rules, commit/push happens only on explicit request.

---

## Tier 0 — Safety & correctness (small, independent, land regardless)

These are owed even if the operator product is never built.

| # | Item | Where | Size |
|---|---|---|---|
| 0.1 | **macOS input must report real errors.** Add `AXIsProcessTrusted` / `CGPreflightScreenCaptureAccess` preflight and surface it in `/rd/status` + the desktop target probe. Return non-nil when TCC denies injection. | `ghost/input_darwin.go`, `ghost/screen_darwin.go`, `remotedesktop_http.go`, `remote_runtime_desktop.go:340` | 1–2 d |
| 0.2 | **Gate control on locality.** A remote caller may not raise `ControlEnabled`; only an on-box (or physically-confirmed) caller may. Mirror `rdViewPolicyUpdateEnforce` for control. | `remotedesktop_http.go:126` | 0.5 d |
| 0.3 | **Scope the browser-session token per route+method** (not prefix). Verify `/rd/input` and `/rd/policy` are not reachable with a GET-view token. | `browser_session.go:112` | 1 d |
| 0.4 | **Give legacy `/rd/input` the same lease** as `desktop-screen`, or make `desktop-screen` the only path and deprecate `/rd/input`. | `remotedesktop_http.go`, `remote_runtime_lease.go` | 1–2 d |
| 0.5 | **Re-check consent inside long-lived streams**; add a teardown when `ViewEnabled` flips off. | `remotedesktop_http.go` | 0.5 d |
| 0.6 | **Re-verify the July `gateway_act_confirm` finding** (second key = same key) and fix if still open. | `gateway_act_mcp.go:154`, `gateway_act.go:243` | 1 d |
| 0.7 | **Doc drift sweep** (audit §6). | several | 0.5 d |

**Acceptance:** a test that disables each guard and watches it fail (Snowball),
plus a doctor probe that reports the macOS permission state as a named code.

---

## Tier 1 — The computer-use loop (the spine) ★

This is the product. Everything else is packaging.

### 1.1 One operator loop: observe → decide → act → verify → retry
Generalize the existing single-shot `ghost.Engine.Act` into a bounded,
model-driven loop that uses **tree assertions before pixel diffs** and re-reads
state after every action. The `expect` mechanism already in
`ghost_click_element` is the seed — promote it to the loop.

- **Surface it as one thing.** Options (decide in the open questions):
  an MCP tool `operator_run{goal, machine, maxSteps, allowBrowser}` **and** an
  ops verb `desktop_operator` (so every surface, incl. watch/TV, reaches it via
  `machine=`), plus a `create_task` "operator mode".
- **Verification:** tree state first, screenshot/`vision_analyze_image` second,
  pixel-diff last. Named failure codes on step-N failure with the last good
  screenshot attached.
- **Bounded:** wall-clock + step budget + a kill switch (reuse the existing
  lease and the `access_policy.go` guard).
- Size: **4–7 d** (the primitives all exist).

### 1.2 Make the desktop reachable in one hop for any model
Expose the desktop primitives as **named MCP tools** in the `runtime_frame` /
`robot_camera` image-first pattern rather than only through generic `ops`:

- `desktop_screenshot` → **first-class MCP image block** (so a vision model
  sees pixels; a text-only model gets the `mcp_vision.go` text adaptation).
- `desktop_act` (click/type/key/scroll/launch/focus) and
  `desktop_elements` (tree query).
- Keep `ops` as the universal fallback for surfaces that only speak ops.
- Size: **2–3 d**.

### 1.3 Multi-monitor
Enumerate all displays in `ghost` (macOS `CGGetActiveDisplayList`, Windows
`EnumDisplayMonitors`, Linux Xinerama/RandR), let `displays[]` flow through
`/rd/status`, the runtime target, and the client pickers.
- Size: **3–5 d**.

### 1.4 Wayland
Either a real backend (grim/`xdg-desktop-portal` for capture; `ydotool`/libei
for input) or an explicit, loud "Wayland unsupported — switch to X11 session"
on **every** path, not just `desktop-screen`. Refusing explicitly beats a
silently blank capture.
- Size: **3–8 d** (portal capture is easy; input is the hard half).

### 1.5 Artifact / file return
A general artifact channel: an MCP `file_fetch` that can return bytes (bounded,
typed) or a signed local URL, generalizing `cad_get` beyond `~/.yaver/cad/`, and
an "attachment" affordance on web/mobile so a PDF/`.dwg`/screenshot lands where
the user is.
- Size: **2–3 d**.

**Acceptance for Tier 1:** *"From the phone, say 'open Safari, log into
<app>, and download the invoice' — watch a model do it step by step, and get the
file back."* Prove it on macOS **or** Linux first (see critical path).

---

## Tier 2 — Governed browser + data

### 2.1 A `web` flow type in the gateway
Add a `web` (and/or `playwright`) flow type to `CapabilityFlow`, routed to the
browser manager, reusing `projectAnswer`. This makes the browser lane a
**governed data path** — answer-schema projection, audit, consent, policy guard
— instead of bare verbs. The extractor contract already exists; only the wiring
is missing.
- Size: **3–5 d**.

### 2.2 One real connector, end to end
Ship **one** connector (pick a vendor) end-to-end before claiming any CRM/ERP
story: manifest → vault auth → GET read → ACT with tap gate → answer. Then the
pattern is proven.
- Size: **2–3 d** once a vendor is picked.

### 2.3 Governed "browse as me" task
A task shape — *"on <site> as me: do X, verify Y, report Z"* — that uses the
persistent profile, the vision adaptation for text-only models, and the Tier-1
loop, under the gateway's consent/audit.
- Size: **3–5 d** on top of 1.1 + 2.1.

### 2.4 Clipboard bridge on surfaces
Surface `clipboard_read`/`clipboard_write` (already MCP tools) on web/mobile,
and bridge to the browser session and remote desktop clipboard.
- Size: **2–3 d**.

---

## Tier 3 — Cross-surface parity

Per the repo's hard rule, a capability is not shipped until it reaches every
surface. Today desktop **video** is web+mobile only; speech reaches watch/TV.

| # | Item | Size |
|---|---|---|
| 3.1 | Point tvOS/watch/Wear at `desktop_voice` for full spoken control (they have `DesktopVoiceClient` — verify end-to-end and expand intents). | 2–3 d |
| 3.2 | Bring **desktop video** (the `desktop-screen` WebRTC target) to tvOS/watch/Wear, or decide speech-only is the TV/watch contract and document it. | 5–10 d |
| 3.3 | Make `desktop-screen` the surface default (replace `/rd/frame.jpg` polling in mobile and `/rd/stream` in web) so users get H.264 + lease by default. | 2–3 d |
| 3.4 | CarPlay + glass: wire `desktop_voice` if not already, with the same disambiguation ("two matches: Save, Save As — which one?"). | 2–3 d |

---

## Tier 4 — Voice + vision polish

| # | Item | Size |
|---|---|---|
| 4.1 | **One vision config path.** Make `ghost_locate` able to reuse the runner's configured provider (opencode/deepseek BYOK included) so setting the runner once enables both `vision_analyze_image` and grounding. | 2–3 d |
| 4.2 | Pick **one** voice stack; delete the dead stubs (`voice_listen_start`/`voice_speak` if they still broadcast to nobody). | 1–2 d |
| 4.3 | Echo-cancelling capture adapter for true barge-in. | 3–5 d |
| 4.4 | Expose scripting bridges as verbs (`osascript_run`, PowerShell/COM, Fusion Python) — far more reliable than GUI-driving for apps that have an API. | 2–4 d |

---

## Tier 5 — Windows (only if Windows is the operator target)

| # | Item | Size |
|---|---|---|
| 5.1 | Validate the now-implemented ConPTY seat end-to-end on a real Windows box. | 3–5 d |
| 5.2 | First-ever execution of the GDI/`SendInput`/UIAutomation GUI code: add a Windows job to CI; fix `TestTreeStubUnsupported`. | 5–10 d, unbounded downside |
| 5.3 | Unattended/session caveats: document that UIA needs an unlocked interactive session; add a named probe. | 1–2 d |

> Windows has the best accessibility tree (`AutomationId`) and honest input
> errors, but the least validation. macOS-or-Linux-first is materially cheaper.

---

## Sizing, critical path, smallest honest slice

**Critical path to "talk to my machine from my phone and have an AI reliably do
a multi-step task across an app and the browser":**

| Order | Item | Size |
|---|---|---|
| 1 | Tier 0.1–0.2 (macOS truth + control gate) | 2–4 d |
| 2 | 1.1 operator loop | 4–7 d |
| 3 | 1.2 named desktop tools + image-first screenshot | 2–3 d |
| 4 | 3.3 make `desktop-screen` the default surface | 2–3 d |
| 5 | 2.1 `web` flow type + 2.3 governed browse task | 6–10 d |
| 6 | 1.5 artifact return | 2–3 d |

**Critical path total: ~18–30 engineering-days** on **one OS** for the core
operator experience (browser included), before parity/Windows breadth.

**Smallest honest slice (the decisive experiment):**

> **One OS (macOS or Linux). One app + the browser. Voice → operator loop →
> verify → speak/return the result.** No AR/VR, no CRM connector, no Windows, no
> second voice stack, no multi-monitor.

That is items 0.1–0.2, 1.1, 1.2, and 3.3 (~10–17 d) and it answers the only
question that matters: **does tree- and vision-driven computer use hit a
reliability bar a human will tolerate?** `docs/yaver-personal-assistant-audit.md`
already flags UI-driving at 10–30 s/task and "<95% reliability = trust death."
If the loop can't beat that, no amount of surface breadth saves it — and
everything deferred here was correctly deferred.

**Do not start with breadth.** The failure mode this codebase already exhibits
is many strong components and no spine; adding a ninth component makes it worse,
not better.

---

## Acceptance: every item ships a verb AND an arc

Per the Snowball Principle, each item above is "done" only when:

1. **Headless verb** — it can be asked in one call (`ops` / MCP tool / CLI),
   not by hand.
2. **Closed loop** — an assertion in the Playwright/RN matrix that would have
   caught its absence, on the surface where it broke (web + mobile today; add
   native arcs as Tier 3 lands). **PIXELS / NAMED / SILENT**, where SILENT is
   the only failing verdict.
3. **Proof by breaking** — disable the guard, watch the test fail, restore it.
4. **Named failure codes** on every new failure, consumed by at least one
   surface, with a route-to-fix button.
5. **Parity note** in the handoff/commit: which surfaces got it, which didn't,
   and why.

---

## What to deliberately NOT do

- **Do not build a second GUI engine.** Reuse `ghost`; extend it.
- **Do not make MJPEG better.** Ship on the WebRTC `desktop-screen` target and
  retire `/rd/stream` as the default.
- **Do not build a bespoke browser use-agent from scratch.** The CDP/WebDriver +
  Selenium + co-browse + persistent-profile stack is already broader than most
  competitors; wire it into the loop and the gateway.
- **Do not unify the two voice stacks** before the operator loop proves the
  interaction is worth having.
- **Do not start with Windows** unless the user says Windows is the target; the
  validation cost dominates.
- **Do not add a ninth component.** Build the spine.
