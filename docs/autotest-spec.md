# Yaver Auto Test — RN-Web/CDP Autonomous App Testing

> **Status:** architecture plan (2026-05-16). Supersedes the prior
> emulator/SDK-driven draft of this file — that version referenced the
> now-deleted `recording*.go` / `loop_autotest.go` infra and Aider, and
> predates the shipped WebRTC runtime. This rewrite is grounded in the
> code that exists today (file:line anchors throughout). Per CLAUDE.md:
> code is the source of truth; if an anchor below has drifted, fix the
> anchor in the same change.

---

## 1. What the user asked for

> User requests tests for their React Native app. A remote dev machine
> runs Chrome (wrapped with `--remote-debugging-port=9222
> --user-data-dir=…`) and traverses the app fully — every add / update /
> get (CRUD) path — while an AI agent reads the codebase to enumerate
> flows and find bugs. The session streams live to the end user over
> WebRTC if they opt in. Everything P2P. User can pick a tablet/phone
> viewport. Simple UI on mobile **and** web.

### Locked decisions (asked & answered 2026-05-16)

| Decision | Choice |
|---|---|
| Chrome automation stack | **CDP-direct (chromedp) is the default**, zero extra npm weight; **Selenium is lazily installed opt-in** per project. |
| v1 test surface | **RN-web via Chrome/CDP is the fast inner-loop pass.** The existing emulator/device WebRTC runtime is the **pluggable deep pass** (phase 2), not built in v1. |
| Bug handling in v1 | **Discover → test → propose, approval-gated.** Agent writes proposed fixes + regression tests on a branch; it does **not** auto-apply or self-loop in v1. |

---

## 2. Differentiation thesis (why this shape wins)

From the competitive scan (Maestro/mobile.dev, camelQA, QA Wolf,
testRigor, Waldo/Tricentis, Mabl, Octomind, Autonoma, Meticulous,
computer-use agents):

- **Codebase-derived CRUD enumeration for mobile is an empty quadrant.**
  Autonoma does "Planner reads the repo → derives flows" but **web only**.
  Every mobile player (camelQA, Waldo, testRigor) markets *no source
  access* as an enterprise-security feature precisely because they run in
  *their* cloud. Yaver reads the repo **because** nothing leaves the
  machine. That inversion is the wedge.
- **Zero marginal test cost.** Competitors meter cloud device-minutes
  (QA Wolf $40+/test/mo; BrowserStack/Sauce per-minute farms). Yaver runs
  on the developer's own box on the developer's existing Claude/Codex
  subscription → marginal cost ≈ $0. Never meter test runs; there is
  nothing to meter (see [[project_business_model.md]],
  [[feedback_no_api_keys_subscription_only.md]]).
- **P2P live test stream to the dev's browser is unique.** Incumbents
  stream *their* cloud devices with a datacenter round-trip. Yaver
  streams a test running on *your* hardware over WebRTC on rails that
  **already exist** (`remote_runtime_webrtc.go`).
- **Honest scoping is the trust play.** RN-web/CDP cannot exercise
  Hermes-specific behavior, native modules, gestures, biometrics, or
  `Platform.OS`-branched native paths. We **say so in the UI** and flag
  those code paths — and offer the emulator deep pass as the pluggable
  follow-up rather than pretending RN-web == device fidelity.

**Wedge one-liner:** *"Point Yaver at your RN repo. Your machine runs the
whole add/edit/delete surface in a real browser, your Claude subscription
finds the bugs, you watch it live. Zero cloud, zero per-test cost,
nothing leaves your laptop."*

**Do NOT build:** real-device matrix coverage, cloud test runners,
enterprise managed-service/compliance, or "smartest agent" — the agent is
commoditizing; the P2P harness + RN-aware enumeration + free execution is
the moat.

---

## 3. The feature sits on rails that already exist

| Capability the feature needs | Already in the codebase | Anchor |
|---|---|---|
| WebRTC H.264-RTP + JPEG-DC fan-out, control channel (tap/swipe/text/key) | `RemoteRuntimeManager` / `ApplyWebRTCOffer` / `ExecuteControl` | `desktop/agent/remote_runtime_webrtc.go:224,616`; session model `remote_runtime.go:70,254,282,462` |
| Browser viewer component (WebRTC `<video>` + JPEG fallback + events DC) | `RemoteRuntimeViewer.tsx` | `web/components/dashboard/RemoteRuntimeViewer.tsx` |
| CDP automation (chromedp speaks Chrome CDP directly) | testkit already imports `chromedp` (a11y, firefox driver note) | `desktop/agent/testkit/a11y.go:11`, `testkit/driver_firefox.go:18` |
| Driver pattern (Boot/Install/Launch/Screenshot/Tap/Text/Swipe/Key) | `IOSSimDriver`, `AndroidEmuDriver` | `testkit/driver_iossim.go:28`, `testkit/driver_androidemu.go:17` |
| RN-web surface (Expo web sibling already produced by dev server) | `DevServer` Expo web, `WebPort` | `desktop/agent/devserver.go:99,838,886` |
| Streaming verb pattern (`{ok,streamId,initial}`, `/streams/<id>`) | `registerOpsVerb` / ops dispatcher | `desktop/agent/ops.go:79,98,100`; example `ops_testrun.go:26` ("test" verb) |
| Long-running CLI → daemon stream + publish helpers | `runner_stream.go` (`AutodevPublishRunner*`) | `desktop/agent/runner_stream.go` |
| Agent runner spawn (claude/codex/opencode, interactive, subscription) | runner infra | see [[feedback_no_headless_p_mode.md]], [[feedback_no_api_keys_subscription_only.md]] |
| In-repo versioned test store concept | prior spec's `.yaver/tests/` (kept) | this doc §7 |
| Chrome install recipe (brew/apt/dnf) | `install_cmd.go` chrome entry | `desktop/agent/install_cmd.go:18` |
| npm postinstall runner bootstrap hook | `cli/src/postinstall.js` | `cli/src/postinstall.js` |

**Net new code is small and surgical:** one testkit CDP driver, one
`web-chrome` target branch in the runtime manager, one `autotest` ops
verb + orchestrator, the CRUD-enumeration prompt/agent glue, the
`.yaver/tests` codifier, the npm Chrome/Selenium bootstrap, and two simple
UI surfaces. Everything streaming/transport is **reuse**.

---

## 4. End-to-end architecture

```
 END USER (mobile app OR web dashboard)
        │  POST /ops {verb:"autotest", payload:{workDir,scope,viewport,stream,target:"web-chrome"}}
        ▼
 ┌─────────────────────────── Yaver Agent (remote dev machine) ───────────────────────────┐
 │                                                                                        │
 │  ops_autotest.go ── returns streamId ──►  client subscribes /streams/<id> (progress)    │
 │      │                                                                                  │
 │      ▼                                                                                  │
 │  autotest orchestrator (autotest.go)                                                    │
 │   1. DISCOVER  spawn interactive runner (claude/codex) → enumerate CRUD flows from repo │
 │   2. SERVE     ensure RN-web: DevServerManager Expo-web sibling (devserver.go WebPort)  │
 │   3. DRIVE     ChromeCDPDriver: launch Chrome --remote-debugging-port=9222              │
 │                --user-data-dir=<tmp>  --window-size=<viewport>  → chromedp traversal    │
 │   4. OBSERVE   per flow: assert add/update/get; capture DOM+console+network+screenshot  │
 │   5. PROPOSE   bugs → runner writes fix + regression test on branch autotest/<ts>       │
 │                (NO auto-apply, NO self-loop in v1 — approval-gated)                      │
 │   6. CODIFY    findings → .yaver/tests/ manifest (ciEnabled:false)                      │
 │      │                                                                                  │
 │      ▼ (only if user opted into live view)                                              │
 │  RemoteRuntimeManager target "web-chrome"  ── WebRTC ──►  viewer (web <video> / mobile) │
 │      (reuses remote_runtime_webrtc.go: H.264 RTP fan-out, JPEG-DC fallback, events DC)  │
 └────────────────────────────────────────────────────────────────────────────────────────┘
        ▲                                                                     │
        └── P2P: direct LAN / QUIC / relay (web = relay-only, per CLAUDE.md) ──┘
```

### 4.1 The Chrome wrapper (honoring the user's exact ask)

`testkit/driver_chromecdp.go` (new) launches:

```
google-chrome \
  --remote-debugging-port=<ephemeral, default 9222 if free else allocated> \
  --user-data-dir=<os.MkdirTemp "yaver-autotest-chrome-*"> \
  --window-size=<viewport.w,viewport.h> \
  --force-device-scale-factor=<viewport.dpr> \
  --headless=new            # unless live-stream opted in → headful for the WebRTC capture
  <RN-web URL from DevServer WebPort>
```

- **CDP default:** the driver attaches to `:9222` and drives via
  `chromedp` — already a testkit dependency (`testkit/a11y.go:11`), zero
  new npm weight, no Java, no ChromeDriver. CDP `Page.captureScreenshot`
  feeds the existing JPEG/RTP pump.
- **Selenium opt-in:** if `.yaver/autotest.json` sets `"driver":
  "selenium"`, the agent lazily ensures ChromeDriver + Selenium server
  (downloaded on first opt-in use, **not** at `npm install`). Same
  `--remote-debugging-port` Chrome; Selenium just becomes the command
  layer. One interface, two backends:

```go
// testkit/driver_chromecdp.go
type WebDriver interface {            // satisfied by cdpBackend & seleniumBackend
    Launch(ctx, ChromeOpts) (Session, error)
    Navigate(ctx, url string) error
    Snapshot(ctx) (DOMTree, error)    // a11y tree + visible interactables
    Click(ctx, sel string) error
    Fill(ctx, sel, val string) error
    Screenshot(ctx) ([]byte, error)   // PNG → reused by JPEG/RTP pump
    Console(ctx) ([]ConsoleMsg, error)
    Network(ctx) ([]NetEvent, error)
    Close() error
}
```

### 4.2 New target on the existing WebRTC runtime — not a new transport

`remote_runtime.go` builds targets at `:254` (`ios-simulator`) and `:282`
(`android-emulator`); `Create()` at `:462`; `Attach()` switches on
`session.TargetID` at `remote_runtime_webrtc.go:102`; capture switches at
`:536`; control switches at `:690`.

Add a third arm **`web-chrome`** to those three switches:

- `Attach`: instead of `IOSSimDriver.Boot`, start/locate the
  `ChromeCDPDriver` session and record a synthetic `deviceID` =
  CDP target id; dims = the chosen viewport (no `ProbeDeviceDims`).
- `captureJPEGFrame`: `case "web-chrome":` → `chromedp` full-page
  screenshot instead of `testkit.IOSSimDriver{}.Screenshot`. The H.264
  RTP pump, JPEG-DC fallback, multi-viewer fan-out, events channel — **all
  unchanged** (`remote_runtime_webrtc.go:273-408`).
- `ExecuteControl` (`:616`): `case "web-chrome":` maps tap→CDP
  `Input.dispatchMouseEvent`, text→`Input.insertText`, swipe→wheel/scroll.
  Lets a human grab the wheel mid-run from the live viewer — same UX the
  emulator target already gives.

Result: the web dashboard's `RemoteRuntimeViewer.tsx` and the mobile
viewer render an Auto Test session **with zero viewer changes** — it is
just another remote-runtime session whose `targetId` is `web-chrome`.

### 4.3 Streaming is opt-in and free when off

If the user does **not** opt into live view, **no WebRTC session is
created** — the orchestrator runs headless Chrome and only emits
text/progress events on `/streams/<id>`. Opting in flips Chrome to
`--headless=new` off (headful) and creates the remote-runtime session so
the existing pump captures frames. This keeps the default path cheap and
matches "stream … if end user prefers".

---

## 5. CRUD / add-update-get enumeration (the differentiator)

The DISCOVER phase spawns an **interactive** runner (claude-code / codex /
opencode — never `-p`/headless, subscription auth only;
[[feedback_no_headless_p_mode.md]], [[feedback_no_api_keys_subscription_only.md]])
with a structured prompt that makes it emit a machine-readable flow plan:

```
Read this React Native repo. Enumerate every user-facing CRUD flow.
For each: { id, screen, navPath[], kind: create|read|update|delete,
            entrypointSelector, inputs[], successAssertion,
            apiHooks[] (the data hooks/mutations it exercises),
            nativeOnly: bool (true if it depends on a native module,
            Hermes-specific API, biometric, camera, push, or a
            Platform.OS-branched path CDP on RN-web cannot exercise) }
Emit as JSON to .yaver/tests/plan.json. Do not modify code.
```

- `nativeOnly: true` flows are **listed but skipped** on the web-chrome
  pass and surfaced in the UI as *"3 native-only flows need the device
  deep pass"* — honest scoping, and the hook for the phase-2 emulator
  pass to pick up.
- Plan is cached and **diffed against `git diff`** on subsequent runs so
  re-runs prioritize changed/new flows (cheap incremental runs).
- Sources of flows (kept from prior design): AI-from-code (every run),
  AI-from-git-diff (new features), discovered-from-bugs (regression),
  user-written stories in `.yaver/tests/stories/user/`.

---

## 6. Orchestrator state machine (`autotest.go`)

```
 DISCOVER ─► SERVE ─► DRIVE ─► OBSERVE ─┐
    ▲                                   │  per flow
    └──────────  next flow  ◄───────────┘
                                        │ all flows done
                                        ▼
                       PROPOSE (bugs → branch, approval-gated)
                                        ▼
                       CODIFY (.yaver/tests manifest, ciEnabled:false)
                                        ▼
                       REPORT (push summary to user; await approve)
```

- **No FIX self-loop in v1.** PROPOSE writes patches + regression tests
  to branch `autotest/<repo>-<ts>` and stops. User reviews via existing
  approve flow; this matches [[project_autotest.md]] / [[project_todolist_queue.md]]
  ("user approves before push") and the existing
  `/autotest/approve` shape kept from the prior spec (§9).
- Budget guard: max flows, max wall-clock, AC-power check (mirrors
  `testkit` `ac_power_only`). Visible failure over silent retry
  ([[feedback_visible_failure_over_silent_retry.md]]).
- Progress events reuse `runner_stream.go` publish helpers so the same
  envelope the rest of Yaver streams shows up on `/streams/<id>`.

---

## 7. In-repo test store (kept from prior design, unchanged shape)

```
.yaver/
  autotest.json            # driver: cdp|selenium, budgets, viewport presets
  tests/
    plan.json              # latest enumerated CRUD flow plan (git-diffable)
    manifest.json          # master index (cases, sources, ciEnabled)
    stories/{user,generated,discovered}/*.md
    snapshots/*.png         # visual baseline (phase 3)
  results/                  # gitignored — local only
    runs/<ts>/{results.json,results.md,screenshots/,fixes/}
```

`manifest.json` case shape and the test-case lifecycle
(DRAFT→VALIDATED→IN-CI) are unchanged from the prior revision of this
doc; CI-sync (`yaver autotest sync-ci`) stays a **phase-2** concern and
keeps the user-owned-runner-first rule
([[feedback_no_github_ci_executor.md]]).

---

## 8. Privacy contract compliance (hard requirement)

Per CLAUDE.md privacy contract + `convex_privacy_test.go`:

- Flow plans, DOM snapshots, console/network logs, screenshots, fixes →
  **device-local only** (`.yaver/results/` gitignored) and P2P frames.
  **Never** to Convex.
- Convex may only see an activity audit summary: `action:"autotest.run"`,
  target = repo slug, outcome, timestamp. No paths, no stdout, no frame
  bytes. Add the autotest payload fields to
  `fieldsWeForbidInAnyConvexPayload` + a `convex_privacy_test.go` case in
  Phase 1 ([[feedback_p2p_only.md]]).
- Web dashboard reaches the agent **relay-only** (browser CORS); the
  WebRTC offer/answer rides the existing `/remote-runtime/sessions/*`
  routes already proxied through relay.

---

## 9. Agent surface

**New ops verb** (`desktop/agent/ops_autotest.go`, mirrors
`ops_testrun.go:26`):

```
verb "autotest"  (Streaming: true)
  payload: { workDir, scope: "full"|"changed"|"screen:<name>",
             viewport: "<presetId>", driver?: "cdp"|"selenium",
             stream: bool, propose: bool }
  → { ok, streamId }      # subscribe /streams/<streamId>
```

**HTTP routes** (kept from prior spec, thin wrappers over the verb so
mobile/web/CLI share one path):

```
POST /autotest/start     {workDir,scope,viewport,stream,propose} → {runId,streamId}
GET  /autotest/status?runId=    SSE: {phase,flow,progress,bugsFound,proposed,nativeSkipped}
POST /autotest/stop      {runId}
GET  /autotest/results[/:runId|/latest]
POST /autotest/approve   {runId, fixes:[], tests:[]}     # phase-1: approve = merge branch
POST /autotest/sync-ci   {runId, testIds|"all"}          # phase-2
GET  /autotest/suite[/coverage|/growth]                  # phase-2/3
```

**CLI** (`yaver autotest …`) wraps the same verb: `start`,
`results`, `approve`, `suite`, `sync-ci` (phase-2), `--viewport`,
`--driver cdp|selenium`, `--no-stream`, `--scope changed`.

---

## 10. UI — deliberately minimal

Design system to match (audited): mobile = `Pressable` + `useColors()` +
`AppScreenHeader` (`mobile/src/components/AppScreenHeader.tsx:14`),
tokens `mobile/src/theme/tokens.ts` (tablet bp 600 / 900); web =
`web/components/ui/Button.tsx` + `Card.tsx`, `surface-*`/`brand` tokens.
Audience = solo founders, keep it one screen ([[user_target_audience.md]]).

### 10.1 Viewport selection (one sticky picker, presets only)

Reuse the existing `VIEW_MODE_KEY = "@yaver/tablet/view_mode"` pattern
(`mobile/app/(tabs)/hotreload.tsx:135`). Presets (drive Chrome
`--window-size` / `--force-device-scale-factor`):

| Preset | px (CSS) | DPR |
|---|---|---|
| Phone (iPhone 15) | 393 × 852 | 3 |
| Phone (Pixel 7) | 412 × 915 | 2.6 |
| Tablet (iPad 11") | 834 × 1194 | 2 |
| Tablet (landscape) | 1194 × 834 | 2 |

### 10.2 Mobile entry — Devices tab device card (no new tab)

```
┌─────────────────────────────┐   tap "Auto Test" on the device card
│  MacBook-Air  ●  online     │
│  [Tasks] [Terminal] [Test]  │
└─────────────────────────────┘
        ▼  bottom sheet (simple)
┌─────────────────────────────┐
│  Auto Test                  │
│  Scope   ● Full  ○ Changed  │
│  Viewport ▾ Phone (iPhone)  │
│  ☐ Watch live (uses WebRTC) │
│  ☑ Propose fixes on a branch│
│  [ ▶ Run Auto Test ]        │
└─────────────────────────────┘
        ▼  live (text by default; video only if "Watch live")
┌─────────────────────────────┐
│  Auto Test ◉ running        │
│  ███████░░░ 12/18 flows     │
│  ✓ Create Todo      0.6s    │
│  ✓ Edit Todo        0.9s    │
│  ✗ Delete Todo  bug found   │
│  ⤳ 3 native-only skipped    │
│  Found 1 · Proposed 1       │
│  [Stop]  [Open results]     │
└─────────────────────────────┘
```

Live video, when opted in, embeds the **existing** mobile remote-runtime
viewer pointed at the `web-chrome` session — no new streaming UI.

### 10.3 Web entry — `web/components/dashboard/AutoTestView.tsx` (new)

`<UICard>` with the same three controls; live pane **renders the existing
`RemoteRuntimeViewer`** with `targetId="web-chrome"` (H.264 `<video>` when
LAN/Tailscale, JPEG-DC fallback over relay — already implemented). Results
list + "Approve branch" / "View diff" buttons using `<Button>`.

---

## 11. npm install bootstrap (`cli/src/postinstall.js` + `install_cmd.go`)

- **Chrome:** on global install, detect Chrome; if missing, *offer* the
  existing `install_cmd.go:18` recipe (brew cask / apt / dnf). Do not
  hard-fail install — Auto Test is opt-in; surface a one-line hint.
- **CDP path: nothing to install** — `chromedp` is already compiled into
  the agent binary. This is the default and keeps `npm install -g
  yaver-cli` lean (CLAUDE.md lean-stack rule).
- **Selenium path: lazy.** Only when a project sets `"driver":"selenium"`
  does the agent download ChromeDriver (+ Selenium server) into
  `~/.yaver/autotest/selenium/` at first use. Never at `npm install`, no
  JVM on the default path.

---

## 12. Phased delivery

**Phase 1 (v1 — this plan):**
1. `testkit/driver_chromecdp.go` — `WebDriver` iface, CDP backend
   (chromedp), Selenium backend stub behind opt-in flag.
2. `web-chrome` arm in `remote_runtime.go` Create + the three switches in
   `remote_runtime_webrtc.go` (Attach/capture/control).
3. `autotest.go` orchestrator (DISCOVER→SERVE→DRIVE→OBSERVE→PROPOSE→
   CODIFY→REPORT, no self-loop) + `ops_autotest.go` verb + `/autotest/*`
   routes + `yaver autotest` CLI.
4. `.yaver/tests` codifier + `convex_privacy_test.go` forbidden-fields
   case.
5. Mobile bottom-sheet entry + web `AutoTestView.tsx`, both reusing the
   existing remote-runtime viewer for opt-in live stream.
6. npm Chrome detect/offer; CDP zero-dep; Selenium lazy.
7. Tests: real CDP against a throwaway static RN-web build on a random
   port, real `httptest` servers, no mocks (convention:
   `desktop/agent/*_test.go`, e.g. `ops_testrun.go` peers). Per
   [[feedback_no_full_test_suite.md]] run focused `-run TestAutotest…`.

**Phase 2 (pluggable deep pass):** wire the existing
`ios-simulator`/`android-emulator` runtime as the device pass for
`nativeOnly` flows; consider orchestrating local Maestro via its MCP
rather than reimplementing device drivers (Yaver = orchestrator,
[[project_yaver_is_orchestrator.md]]). CI-sync (`sync-ci`,
user-owned-runner-first).

**Phase 3:** visual + perf + API-contract regression, crash replay,
self-healing/pruning (kept from prior spec, unchanged intent).

---

## 13. Risks & mitigations

| Risk | Mitigation |
|---|---|
| RN-web ≠ shipped artifact (Hermes/native gaps) | `nativeOnly` flagging + explicit UI copy + phase-2 device pass. Position as fast inner-loop, not certification. |
| Chrome `:9222` port already in use | Allocate ephemeral port if 9222 busy; never assume. |
| Headful Chrome needed for live capture but heavier | Headless by default; headful only when "Watch live" opted in. |
| Selenium bloating `npm install` | Lazy, opt-in, post-install, off the default path entirely. |
| Privacy leak via results/frames | Device-local + P2P only; Convex audit-summary only; privacy test gate in Phase 1. |
| Self-recursion / runner billing | Interactive runners only, subscription auth, no `-p`; sha-compare not version-exec ([[feedback_yaver_self_recursion_macos.md]]). |
| Doc drift | Anchors are file:line; fix in same change when they move (CLAUDE.md). |
