# Yaver-from-Yaver: browser-lane rendering + E2E dogfood harness

Date: 2026-07-23. Author context: dogfooding build 460 on a physical iPhone
against a Mac agent, driving the reload lanes.

## Goal

Control the phone from the laptop (Claude Code / Codex) to render **every**
project — sfmg, e-mobile, yaver, carrotbet, talos — **from the browser lane**,
and turn that into an end-to-end test (phone + Mac mini) covering browser /
WebRTC / Hermes with the todo background-color closed loop.

## Why "browser lane for everything" is the right call

Each project fails the Hermes path for a different reason, and the browser
lane sidesteps all of them:

| Project | Stack | Hermes result | Browser lane |
|---|---|---|---|
| sfmg | expo | **FAILS** — `Load Failed: needs native modules Yaver does not include: expo-gl` | web target renders, no native module gate |
| e-mobile | flutter | N/A — Flutter has no Hermes | Flutter web dev server |
| yaver | self-dev | **BLOCKED** — recursion guard strips Hermes | chrome-webrtc (pixels) |
| carrotbet | expo | native-module gate risk | web target |
| talos | expo | native-module gate risk | web target |

Hermes is the one lane with a hard dependency on the guest app's native module
set matching the Yaver container. The browser lane (web target in a WebView, or
headless-Chrome pixels over WebRTC) has no such coupling — it is the universal
"see it run" path.

## Observed failures in build 460 (root-caused)

1. **Browser Reload silently becomes Hermes for expo/RN.**
   `mobile/src/components/DevPreview.tsx` computes
   `mustUseNativePreview = isHermesNativeFramework(status) || devMode==="dev-client" || building`.
   For any expo/RN project this is `true`, so `handleOpen`/`handleReload` force
   `handleRunInYaver()` (the `/dev/build-native` Hermes path) **regardless of
   which lane the user picked**. That is why "Browser Reload" on sfmg showed
   `mode · native install → Building native bundle → Load Failed (expo-gl)`.
   The lane the user chose is thrown away.

2. **Flutter told to "start Metro … Hermes push".**
   `mobile/app/(tabs)/apps.tsx` sent a hardcoded RN dev-start prompt for every
   framework. Flutter has no Metro and no Hermes. **Fixed** this session
   (`devStartInstruction(framework, targetPath)`), pending a build.

3. **Browser WebView shows `relay password missing — sign in again to fetch it`.**
   The web-mode WebView loads the `/dev/` proxy URL through the relay; the
   served URL returns Unauthorized because the dev-server proxy path did not
   self-heal the stale relay credential (the main connection self-heals, this
   sub-path does not).

4. **Preview chrome, not app-like.** The WebView modal carries a
   Back / Reload / Stop-Serving header. Wanted: full-screen, app-like, closer
   to the Hermes-in-container experience.

5. **Framework detection keys only on `pubspec.yaml` presence** — doesn't
   distinguish Flutter from a plain Dart package, and doesn't read the dart
   project markers.

## The two lanes that actually render on the phone

From the code maps:

### Browser lane (WebView → dev-server `/dev/` proxy)
- `POST /dev/start {framework, workDir, platform:"web", port}` → dev server up.
- Phone WebView points at the `/dev/` reverse proxy (`handleDevServerProxy`,
  no-auth catch-all). HMR via `POST /dev/reload {mode:"dev"}`.
- Works for expo web target, Flutter web, Next/Vite. **No native module gate.**
- Current blocker: the relay-auth on the served URL (#3).

### WebRTC lane (headless Chrome on the box → streamed pixels)
- `GET /remote-runtime/capabilities?framework=&workDir=` → target `browser-window`.
- `POST /remote-runtime/sessions {workDir, framework:"browser", targetId:"browser-window", transportMode:"direct-webrtc"}`.
- `POST /remote-runtime/sessions/{id}/control {action:"navigate", url}`.
- `POST /remote-runtime/sessions/{id}/webrtc/offer {type,sdp}` (viewer MUST
  `addTransceiver("video")` or it silently drops to ~1.1fps JPEG).
- `GET /remote-runtime/sessions/{id}/frame` — JPEG poll fallback.
- Works for **any** stack including native (simulator/redroid targets) and
  self-dev Yaver. This is what the color-vibe smokes already exercise on the box.

## E2E dogfood harness — what exists, what's missing

The "laptop orchestrates phone + mini, todo color change, verify" loop:

| Step | Primitive (physical-phone path) | Status |
|---|---|---|
| Change code (bg color) | `runtime_turn {text,run:true}` → coding task | **exists** |
| Trigger reload on phone | `runtime_turn_verify` / `device_broadcast_command{command:"reload"}` → `BroadcastCommand` | **exists** |
| Confirm bundle loaded | device acks `preview_worker_bundle_loaded{turnId}` → `verified` | **exists** (the ack loop) |
| **Capture the phone's screen** | — | **MISSING** |
| **Verify the color changed** | — | **MISSING** |

Every screenshot primitive (`vibe_preview snapshot`, sim clips, `robot_camera`)
targets a browser/simulator/host-camera **on the box, not the physical phone**.
The phone receives a `capture_screenshot` command but replies
`supported:false, reason:"screenshot-capture-not-wired-yet"` — a dead stub on
both ends. The only phone→agent visual path is a ≥1s MP4 clip a user must tap to
start (`recordAndUploadPhoneClip` → `/vibing/preview/clip/upload`).

**The single highest-value primitive to build is an agent-triggerable
phone-screenshot:** wire the `capture_screenshot` command → a native single-frame
capture → upload to `/blackbox/frame` (or reuse the clip-upload path) → a
retrieval verb mirroring `vibe_preview_snapshot`. That closes the last half of
every phone E2E test.

## The test app for the color loop

The existing color-vibe smokes do NOT use a real todo project — they serve a
synthetic `body{background:rgb(220,20,20)}` → `.done{background:rgb(20,180,60)}`
HTML page from a throwaway `python3 http.server`, and verify a center-crop avg
RGB red→green. The **real** todo fixtures are:
- in-tree `demo/mobile/todo-rn` (expo, `io.yaver.todorn`) — the browser/Hermes lane.
- external `yaver-todo-{rn,web,flutter,kt,swift}` — "same UX five ways" so the app
  is the control and any diff is the transport.

A real-hardware harness clones `yaver-todo-rn` (Hermes + expo web) and
`yaver-todo-web` (Next HMR), edits a background color, and drives the phone
through each lane, verifying the phone frame flips color.

## Plan (prioritized)

**P0 — make Browser Reload actually render in the browser (unblocks all 5):**
1. DevPreview: when the chosen lane is browser, do NOT force `mustUseNativePreview`.
   Start the dev server in web mode and render the web target (WebView or WebRTC),
   even for expo/RN. Hermes stays a separate explicit lane.
2. Fix the relay-password self-heal on the `/dev/` served path (reuse the
   `repair-relay` re-pull the main connection already does).
3. Full-screen the preview (drop/auto-hide the header chrome).

**P0 — framework-correct dev-start (done, pending build):** `devStartInstruction`.

**P1 — the phone-screenshot primitive** (the E2E enabler): agent-triggerable
`capture_screenshot` → native single-frame → upload → retrieval verb.

**P1 — the E2E harness** (`scripts/dogfood-phone-lanes-e2e.sh` or a Go
integration test driven from the laptop): for each of {browser, webrtc, hermes},
clone/point at a todo project, edit bg color, drive the phone, pull a phone
frame, assert the color changed.

**P2 — robust Flutter detection** (agent): `pubspec.yaml` + a `flutter:` key /
`lib/*.dart`, distinguishing Flutter from a plain Dart package.
