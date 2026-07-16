# Yaver n2n — Develop Anywhere, For Anywhere

> **Status:** design / deep-audit (2026-07-16). No feature code changed by this
> doc. Grounded in a 5-way read-only audit of the current tree; every claim
> carries a `file:line` anchor. Per CLAUDE.md: **the code is the source of
> truth — re-grep before acting on any line number here.**

## The thesis

Yaver should be the platform to **develop *for* every surface *from* every
surface** — full n2n. You open Yaver on your **tvOS** and build Talos's
**watchOS** target from it; you sit on your phone and drive the **AR/VR**
build on the Mac mini. Two independent axes, each spanning the same surface
set, **on both iOS and Android**:

```
              TARGET  (what you build/preview)
              car · watch · tv · ar-vr · mobile · tablet · web      ×  {iOS, Android}
CLIENT (drive-from)
  car · watch · tv · ar-vr · mobile · tablet · web
```

Four rendering/transport mechanisms carry a cell:

| Mechanism | What it is | Reaches |
|---|---|---|
| **Hermes push** | HBC bytecode → in-place RN bridge swap | iOS + Android **phone/tablet only** |
| **WebView / iframe** | `/dev/` proxy of a web dev server | web-framework projects, interactive |
| **WebRTC stream** | RTP-H.264 / JPEG-DataChannel of a booted runtime | any bootable sim/emulator/device |
| **JPEG / snapshot poll** | single-frame GETs (iOS-safe, relay-safe) | universal fallback |

**The load-bearing realization:** Hermes only reaches phone/tablet. For every
*other* target (watch, tv, car, AR/VR), the universal preview mechanism is
**WebRTC-stream-of-a-booted-runtime** — you boot the watchOS/tvOS/visionOS sim
(or Wear/AndroidTV/XR emulator) on the host and stream its pixels. So n2n's
target side is mostly a **streaming + enumeration** problem, not new push tech.

---

## Current state — 5 audits, grounded

### A. Remote-runtime streaming (`remote_runtime*.go`, `testkit/driver_iossim.go`)

- The simctl driver is **already runtime-agnostic**. `Boot/Screenshot/Tap/SendText`
  all take a UDID and shell to `xcrun simctl` (`testkit/driver_iossim.go:53-213`);
  `pickSimulatorFromList` already *scores* iPhone=40, iPad=35, **Vision=30, TV=20,
  Watch=15** (`driver_iossim.go:154-165`). Booting + screenshotting any Apple
  runtime works today.
- But enumeration emits **one static `ios-simulator` target**
  (`remote_runtime.go:285-311`); there is no per-runtime watch/tv/vision/iPad
  target. `DeviceType` (`driver_iossim.go:30`) is never threaded through
  `iosSimulatorTarget.Attach` (`remote_runtime_target.go:90-92`).
- Dispatch seam is clean: `runtimeTargetFor()` (`remote_runtime_target.go:62-84`)
  + the `runtimeTarget` interface (`:36-58`, tap/swipe/text/key/screenshot/
  dims/capture).
- **Transport blocker:** all Apple sims have `CanEncodeRTPH264()==false`
  (`remote_runtime_target.go:140-147`) — Xcode 26 dropped `simctl recordVideo`
  to stdout, so they fall back to JPEG-DataChannel (700 ms, ≤720 px, ≤60 KB;
  `remote_runtime_webrtc.go:407-451,518-551`). Android emu/device get real
  RTP-H.264 (`remote_runtime_video_track.go:263-288`).
- **Control model is tap/swipe/key only.** No Digital Crown (watch), no
  focus/directional remote (tv — `wda_client.go:184-194` only has home/volume),
  no pinch/gaze (vision). tvOS x,y taps are meaningless.

### B. Hermes push (`cli/src/*`, `mobile/ios|android/.../YaverBundleLoader*`)

- iOS: true in-place bridge swap via `ExpoReactNativeFactory` +
  `RCTAppDependencyProvider` (`AppDelegate.swift:139,286,309-325`). Android:
  best-effort `ReactHostImpl.loadBundle` reflection, else `recreate()` flash
  (`YaverBundleLoaderModule.kt:360,333`). **Android BundleLoader is present and
  at near-parity** — the "missing" note in project memory is **stale**.
- `/dev/build-native` platform gate accepts **only `ios`/`android`**
  (`devserver_http.go:2673-2675`).
- **tvOS/watchOS/visionOS/wear/CarPlay are native Swift/Kotlin clients, not RN
  hosts** — no `RCTBridge`/`jsbundle` anywhere in `tvos/ watch/ visionos/ wear/`.
  tvOS itself says "no Hermes push to a device — the TV just streams pixels"
  (`tvos/YaverTV/Views/ProjectsView.swift:6`). So those targets **cannot be
  Hermes-pushed**; they need a native rebuild *or* a streamed pixel preview.

### C. WebView / web-framework preview (`devserver*.go`, `vibe_preview*`, mobile/web/glass)

- Web dev servers (Vite `devserver.go:2431`, Next `:2476`, Flutter-web `:2158`,
  Expo-web `:1878`) are proxied at `/dev/` and shown as an **iframe** on web
  dashboard (`WebPreviewFrame.tsx:310`), a **full-screen WebView** on mobile
  (`DevPreview.tsx:719`, web frameworks only), and a WebView pane on glass
  workspace (`glass-workspace.tsx:406`).
- The **RN-never-WebView** rule is enforced everywhere (`DevPreview.tsx:35-38,
  260-263`; `PreviewPane.tsx:88-92`).
- **Two parallel preview transports with no unifying fallback:** `/dev/` iframe
  vs a pixel/snapshot stream (`vibe_preview.go`, tvOS `WebPreviewStreamView.swift`).
  Non-WebView clients (tvOS, immersive visionOS, watch) can only ever get the
  snapshot stream, never interactive web.

### D. Client surfaces — the drive-from matrix

| Client | List devices | View stream | Send control | Drive runner |
|---|---|---|---|---|
| **Mobile / tablet / car / glass** (RN, shared) | ✅ | ✅ WebRTC-direct + JPEG-poll + snapshot | ✅ | ✅ PTY + session-turn + tasks |
| **Web dashboard** | ✅ | ✅ RTP-H.264 + JPEG-DC + relay-poll + MJPEG | ✅ | ✅ PTY + tasks |
| **tvOS** | ✅ boxes | ⚠️ snapshot-poll frames only (no WebRTC, not the remote-runtime lane) | ❌ **disabled by design** (`DroidStreamView.swift:8-10`) | ✅ session-turn (text/voice, needs named session) |
| **watchOS / Wear** | ⚠️ single standalone box | ❌ | ❌ | ✅ voice/choice session-turn only |

- Full n2n clients today = **mobile + web only**. Remote-runtime WebRTC-direct +
  relay-poll parity exists only there (`mobile/app/remote-runtime.tsx:108,556-668`;
  web `RemoteRuntimeViewer.tsx:187-353`).
- tvOS is a **watch-and-steer** client: it polls `/droid/frame`,
  `/capture/frame.jpg`, `/vibing/preview/*` (`AgentClient.swift:180-344`) and
  drives the runner turn-by-turn (`SessionClient.swift:53-58`), but has **no**
  remote-runtime viewer and **declines** to send input.
- watch/Wear are **voice terminals by explicit product stance**
  (`WatchStore.swift:6-8`).

### E. MCP verbs + agent routes

- **The WebRTC remote-runtime lane is NOT exposed via MCP — it is HTTP-only**
  (`/remote-runtime/sessions` create/attach/offer/control/frame,
  `remote_runtime.go:471-523`, `remote_runtime_webrtc.go:770-879`). Claude-on-
  phone cannot create+stream+control a runtime through MCP.
- Live runtime targets are **phone/desktop-only** — no watch/tv/car/vision
  `probe*Target` (`remote_runtime.go:200-238`).
- **No composed orchestration verb.** `mobile_platform_deploy` (`mcp_tools.go:2103`)
  is the one place all surfaces are named (ios/tvos/watchos/visionos/carplay/
  wear-os/android-tv/android-auto/android-xr) — but it is **build/upload only**,
  no boot/launch/stream. `remote_dev_prepare`, `mobile_project_build`,
  `code_attach`, `droid_launch` each do one step.
- **Chat-triggered launch+render works for Android** (`droid_launch` →
  `droid_frame` returns a first-class MCP image, `httpserver.go:8771`); **iOS is
  weaker** (`simulator_screenshot` returns JSON, not an image content block,
  `httpserver.go:9513`); **live WebRTC is not reachable from MCP at all**.
- First-class image tools exist (`robot_camera`, `appletv_now_playing`,
  `droid_frame`, `screenshot`, `circuit_plot`) — the pattern to copy for a
  per-surface "launch and show me a live frame".

---

## The n2n target matrix (build-for side)

Per target surface × platform: today's preview mechanism and the gap.

| Target | iOS mechanism | Android mechanism | Streamable now? | Controllable now? |
|---|---|---|---|---|
| **mobile** | Hermes push ✅ | Hermes push ✅ | ✅ (sim/emu WebRTC+JPEG) | ✅ |
| **tablet** | Hermes push ✅ (iPad, no dedicated target) | Hermes push ✅ | ✅ | ✅ |
| **watch** | boot watchOS sim → JPEG stream | boot Wear emu → RTP/JPEG | ⚠️ boots+screenshots, **no target entry** | ❌ no crown |
| **tv** | boot tvOS sim → JPEG stream | boot AndroidTV emu → RTP/JPEG | ⚠️ **no target entry** | ❌ no directional remote |
| **ar-vr** | boot visionOS sim → JPEG stream | Android XR emu | ⚠️ **no target entry** | ❌ no pinch/gaze |
| **car** | CarPlay window inside iOS sim | Android Auto (native templates) | ❌ no addressable window | ❌ |
| **web** | `/dev/` iframe / pixel stream | same | ✅ (iframe or snapshot) | ✅ (iframe) / ❌ (pixel) |

The `⚠️` rows are the sweet spot: the driver *already boots and screenshots*
them; they just aren't enumerated, addressed, or given the right control verbs.

---

## Design — how to close it

Decision (2026-07-16): **dedicated per-runtime target IDs** (not a parameterized
`ios-simulator:<sel>`), so the picker shows each as its own labeled surface.

### Phase 0 — Apple-runtime fan-out (stream-first) — *smallest, unblocks tomorrow*
- New probes → dedicated targets: `ios-simulator` (iPhone), `ipados-simulator`,
  `watchos-simulator`, `tvos-simulator`, `visionos-simulator`. Each probes its
  installed runtime via `simctl list runtimes` and is `Enabled` iff present
  (`remote_runtime.go:285`).
- Thread `DeviceType`/UDID through `iosSimulatorTarget.Attach`
  (`remote_runtime_target.go:90`) → the driver already handles the rest.
- These stream immediately via the existing JPEG-DC lane. **Delivers "see my
  AR/VR + tvOS + watch build from the phone/web."** No new control needed.

### Phase 1 — per-surface control primitives + Android surface targets
- Extend `runtimeTarget` (or enrich `Key`): tvOS directional/select/menu/
  play-pause, watch Digital Crown, vision pinch-at-coordinate. iOS bottleneck is
  `wda_client.go:184-194`; Android is already extensible (`androidKeycodeForName`).
- Add Android emulator targets for **Wear OS / Android TV / Android XR / Android
  Auto** — all adb-based, largely cloning `androidEmulatorTarget`.

### Phase 2 — MCP + task-level orchestration (the "let's develop Talos for Android Watch" ask)
- **MCP wrapper over remote-runtime**: `runtime_create / runtime_attach /
  runtime_frame / runtime_control / runtime_targets` verbs over the existing
  HTTP lane (`remote_runtime.go` / `_webrtc.go`).
- **One composed verb** — `develop_for <project> <surface> [platform]` — that:
  resolves the target per `{surface × iOS/Android}` → builds the right artifact
  (**Hermes** for phone/tablet, **native rebuild** for tv/watch/car/vision,
  **web** for web) → boots → launches → returns a **first-class live frame**
  (robot_camera pattern) + a session handle + a runner attach. Building blocks
  exist (`mobile_platform_deploy`, `remote_dev_prepare`, `droid_launch`/
  `droid_frame`); this stitches them.
- Make `simulator_screenshot` and remote-runtime `/frame` return **first-class
  MCP images** so a chat turn can "launch + show me" any surface, not just
  Android.
- Extend the session `command` verb (today only `launch-feedback`,
  `remote_runtime.go:553`) with `boot` / `launch-app` / `build`.

### Phase 3 — client parity (drive-from) + clean UI on every surface
- Port the remote-runtime **viewer + control** to `tvos/YaverTV` (add a
  Pion-answer or JPEG-poll viewer + a focus-engine→coordinate control channel;
  server already exposes `/remote-runtime/.../control` and `/droid/input`). This
  is what enables **"develop watchOS from my tvOS."**
- Unify the two web-preview transports behind one abstraction that **auto-falls
  back** iframe ↔ pixel-stream per client capability.
- Clean, parity picker+stream+control UI on **all** clients (RN surfaces share
  code; tvOS/watchOS/web/Wear are native ports — honor the cross-surface parity
  rule in CLAUDE.md).

### Phase 4 — streaming quality
- Replace the dead simctl-stdout RTP path with a **file-backed MP4-fragment
  tailer** to restore RTP-H.264 for Apple sims (the single biggest quality
  unlock; `remote_runtime_target.go:140-147`). In-process x264 for
  `browser-window` (`remote_runtime_browser.go:278`).

---

## Cross-cutting notes

- **iOS + Android duality** holds per surface: watch = watchOS **+** Wear OS,
  tv = tvOS **+** Android TV, car = CarPlay **+** Android Auto, ar-vr = visionOS
  **+** Android XR. The orchestration verb must pick per `{surface × platform}`.
- **CarPlay/Android Auto are the hardest targets** — Apple/Google forbid
  arbitrary RN UI in-car and there's no addressable CarPlay window in the sim.
  Realistic path: native template preview + streamed pixel view of the sim's
  external-display window; not interactive control.
- **watch/Wear as *clients*** are intentionally capped at voice-driven session
  steering (`WatchStore.swift:6-8`). n2n "drive from watch" should respect that:
  voice-launch + spoken status, not a frame viewer.
- Memory fix pending: `project_android_bundleloader_missing` is **stale** —
  Android BundleLoader is present (`YaverBundleLoaderModule.kt`).

---

# Part II — Voice, Feedback, Concurrency, and the all-MCP keystone

Part I covered the target/stream/preview side. The lived UX the user is after
adds three more axes and a voice-first loop:

```
Axis 1  TARGET      — what you build for     (car/watch/tv/ar-vr/mobile/tablet/web × iOS/Android)
Axis 2  COMMAND     — where you issue it     (voice from car, tap from phone, D-pad+dictation from TV…)
Axis 3  RENDER SINK — where it displays      (cast the stream to phone / TV / glasses…)
```

Concrete story to satisfy: *developer opens Apple TV → says "open Talos mobile
app here" → Yaver launches+streams it → they **vibe by speech** → the runner
edits code **and operates the app** (navigates/browses) → results read back by
TTS → and they use **phone + TV at once** (phone = mic + control, TV = big
render), or **"from my car, render on my phone."*

## The keystone: MCP-over-remote-runtime (runner-driven app operation)

The single most important missing piece. Today the WebRTC remote-runtime lane
(create / attach / offer / **control** / frame) is **HTTP-only** — no MCP verbs
(`remote_runtime.go:471-523`, `remote_runtime_webrtc.go:770-879`; MCP audit).
So a runner can drive the *code* but cannot drive the *app*. The contract:

```
voice/text intent → runner (on the runner-authed remote machine, via MCP)
   ├─ edits code, rebuilds/reloads  (mechanism auto-resolved — see below)
   ├─ operates the app:  runtime_control  tap/swipe/text/navigate   ← MISSING as MCP
   ├─ observes:          runtime_frame → first-class MCP image       ← MISSING for sim/iOS
   └─ narrates:          TTS spoken summary                          (runner_turn already does this)
```

New MCP verbs required: `runtime_targets`, `runtime_create`, `runtime_attach`,
`runtime_control`, `runtime_frame` (image), plus session `command` extended
with `boot`/`launch-app`/`build` (today only `launch-feedback`,
`remote_runtime.go:553`). This makes **runner-driven app usage** real and is the
prerequisite for "browse the app by voice."

## Mechanism resolver + runner-auth gate ("launch this app in Yaver")

The user names an app, never a mechanism. On any client: **select machine →
gate on authenticated runner → speak/issue command →** a resolver picks the path:

```
resolve(project.framework, target.surface, target.platform, host.caps):
   RN + phone/tablet        → Hermes push (in-place bridge swap)          devserver_http.go:2561
   RN + watch/tv/car/ar-vr  → native rebuild + WebRTC/JPEG stream of sim  (no Hermes host on those)
   web framework            → WebView iframe  |  pixel-stream on non-WebView clients
   any bootable runtime     → WebRTC stream (universal fallback)
```

This is the composed `develop_for <project> <surface> [platform] [renderOn]`
verb from Part I Phase 2, now with two hard contracts: **(a) runner-auth gate**
on the selected machine, **(b) transparent mechanism resolution**.

## Voice-first loop (STT in / TTS out)

- One surface-agnostic engine exists — `mobile/src/lib/voice/conversationCore.ts`
  (streaming STT → `endpointer.ts` timing → `completenessJudge.ts` semantic → runner
  dispatch → TTS → barge-in), on-device-first (whisper.rn STT `adapters/whisperCapture.ts:53`,
  expo-speech TTS `adapters/deviceTts.ts:17`, llama.rn judge `adapters/localJudge.ts:38`).
- **Adoption is 2 of 7 surfaces** — only car (`car-voice-coding.tsx:326`) and
  glass (`glass-terminal.tsx:378`) consume it. Phone, web, tvOS, watch, Wear
  each re-implement or lack it (`types.ts:12-18` says the core exists to stop
  exactly this duplication).
- Runner drive already voice-aware: `/runner/session/turn`
  (`runner_session_turn.go:178`) + MCP `runner_turn` (`ops_runner_turn.go:34`,
  `surface=voice` → code-free spoken summary `:185`).
- **tvOS has no streaming mic** — only Siri-remote press-to-dictate into a
  TextField (`tvos/YaverTV/SessionView.swift:20-22,276`); TTS works
  (`Speech.swift`). So the tvOS story is **phone-as-mic + TV-as-render** — which
  is exactly Axis 2≠Axis 3. There is **no phone→TV voice/render bridge today.**
- **Two missing MCP verbs**: `voice_listen_start(device)` and `voice_speak(device,text)`
  — nothing can start STT or play TTS *on a remote surface* from an agent
  (voice audit §5). Without them a runner can't make the TV/car start listening,
  and can't cast a spoken result to a chosen surface.
- **No surface can voice-navigate a streamed app** yet — all streamed views are
  tap/D-pad. "Browse the app by voice" = voice intent → `runtime_control` (the
  keystone verb) driven by the runner.

## Feedback SDK — n2n compliance

- SDK exists for **RN / web / Flutter / Unity / browser-ext** — **zero
  keyboard-less surfaces** (no tvOS/watch/car/AR-VR); capture is gated on shake +
  touch modal + view-shot/record-screen, none meaningful on TV/car/HMD
  (feedback audit §1). `surface` is hardcoded `'feedback-sdk'`
  (`VibeChatScreen.tsx:325`) and never emits the richer surfaces the agent
  already understands (`voice_http.go:62-64`).
- **Reusable and n2n-ready**: transport is P2P/agent-HTTP with the privacy
  contract enforced (no feedback content in Convex, `convex_privacy_test.go`);
  MCP `feedback_list/show/fix/delete` exist (`mcp_tools.go:3228-3274`); the fix
  loop is already hands-free once a report exists (`feedback_to_vibe.go:250`).
- **Missing at the capture edge**: no `feedback_create` verb (feedback can only
  be born from the shake/overlay UI); no **voice→FeedbackReport** authoring path
  (voice currently drives task creation, not feedback); no **TTS readback** of a
  feedback queue. These three are what make the feedback loop work hands-free on
  a keyboard-less surface.

## Concurrency + render routing (phone + TV at once; "render on my phone")

- **Multi-viewer fan-out IS built for RTP mode** — `remoteRuntimeLiveState.peers`
  is the subscriber source of truth, every RTP offer *appends* (no cap), shared
  capture pipeline, events broadcast to all peers
  (`remote_runtime_webrtc.go:42-62,240-288,453-481`). **TV + phone can both view
  one session live today.** A `GET /frame` poller coexists safely.
- **But**: JPEG-DC mode is single-viewer and a JPEG-DC offer **tears down** RTP
  peers (`:251-255`) — a mixed fleet collides.
- **No control arbitration anywhere** — `/remote-runtime/.../control` and
  `/rd/input` are free-for-all, last-writer-wins (`remote_runtime_webrtc.go:584-648`;
  `remotedesktop_http.go:104-148,219-296`). Phone + TV would fight over input.
  **Biggest concurrency gap.** Needs a single-writer lease / role split (phone
  controls, TV views).
- **No unified reactive session registry** all a user's surfaces join —
  remote-runtime sessions live in agent memory, poll-discovered
  (`remote_runtime.go:97-100,342`); live state pushes only to attached peers.
  The **relay per-user event bus** (`relay/bus.go:34-130`, "every subscriber
  gets every event") is the natural substrate but isn't wired to session state.
- **session/transfer/multiuser are hand-off / isolation, not co-use**
  (`ops_session.go:18-62` move/baton; `multiuser.go` different-user). There is no
  same-user "join this live session" primitive.
- **Render routing / cast (Axis 3)** builds on the above: a command from surface
  A carrying `renderOn: B` opens/attaches the session's stream on B (another of
  the user's authenticated surfaces). Requires (i) the shared session registry,
  (ii) a `voice_speak`/stream-cast verb to push media to a named sibling, (iii)
  presence to resolve B. None exist as a unit today; the RTP fan-out + relay bus
  + Convex device presence are the pieces to compose.
- **Relay caveat**: the relay does **no media fan-out** — all fan-out is
  agent-side Pion; TURN is not wired (`remote_runtime_webrtc.go:854`). So
  concurrent multi-viewer relies on the agent (e.g. the mac mini) being directly
  reachable, or TURN must be added for NAT'd fleets.

## Revised phased plan (supersedes Part I's phases where they overlap)

- **P0 — Apple-runtime fan-out (stream-first).** Per-runtime dedicated targets
  (iPhone/iPad/watchOS/tvOS/visionOS); thread `DeviceType`; stream via existing
  JPEG lane. Unblocks "see my AR/VR + tvOS + watch build."
- **P1 — MCP keystone.** `runtime_*` MCP verbs over the remote-runtime lane
  (create/attach/**control**/frame-as-image) + session `command` boot/launch/build.
  Enables runner-driven app operation.
- **P2 — Orchestration verb.** `develop_for <project> <surface> [platform] [renderOn]`
  with runner-auth gate + mechanism resolver + first-class live-frame return.
- **P3 — Voice everywhere.** Bind `AudioCaptureAdapter`+`TtsAdapter` on the 5
  un-wired surfaces; add `voice_listen_start`/`voice_speak` MCP verbs; **phone-as-mic
  → TV-render bridge**; voice-navigate-the-app via the keystone `runtime_control`.
- **P4 — Feedback n2n.** `feedback_create` + voice→FeedbackReport authoring +
  TTS readback; keyboard-less capture surfaces.
- **P5 — Concurrency + cast.** Control-arbitration lease (role split), a
  relay-bus-backed **shared reactive session registry** all surfaces join, unify
  JPEG-DC/RTP so a mixed fleet co-views, and `renderOn` cast routing.
- **P6 — Control primitives + Android surfaces + transport quality.** tvOS
  directional/watch crown/vision pinch; Wear/AndroidTV/XR/Auto emulator targets;
  file-backed MP4-fragment tailer to restore RTP-H.264 for Apple sims; TURN for
  NAT'd multi-viewer.

## The n2n contract, one line

`{command surface} × {render sink} × {target surface × iOS|Android} × {mechanism:
auto} × {N concurrent clients}` — one spoken intent, runner-driven code **and**
app operation, gated only on *machine selected + runner authenticated*, every
step reachable over **MCP**.
