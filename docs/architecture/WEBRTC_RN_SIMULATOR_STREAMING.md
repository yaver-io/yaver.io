# WebRTC RN Simulator Streaming — design

Status: **foundation landed, end-to-end needs on-device validation** (2026-07-21).

The ask: let an RN/Expo app be developed **in a simulator on the remote box,
streamed to the phone/web viewer over WebRTC**, as an alternative to Hermes push
— with the app's own Yaver Feedback SDK working, triggered by a client-side
shake. This doc is the authoritative design; it answers the open questions raised
during design and records what is landed vs. what remains.

## The two surfaces are complementary, not competing

| | **Hermes push** (default today) | **WebRTC sim streaming** (this design) |
|---|---|---|
| Where the app runs | The **phone** (Yaver container) | A **simulator on the remote box** |
| Iteration speed | **Slow** — full Hermes bytecode rebundle + reload per change, no Fast Refresh (state lost) | **Instant** — app is on Metro with Fast Refresh; a patch hot-reloads the changed modules sub-second, state preserved. The normal RN sim dev loop. |
| Camera / GPS / sensors / biometrics / push | **Real** — actual phone hardware | **Simulated** — sim has no real camera/GPS; sensors are injected (shake is) |
| Device performance | **Real** | Simulator performance |
| Native-module coverage | Limited to the Yaver host (the compat gate — expo-gl class) | **Full** — the app builds with its own native modules in the sim |
| Feedback SDK | **Suppressed** — Yaver container owns shake | **Native** — the app runs standalone, its own SDK is live |
| Works without the Yaver mobile app | No (needs the container) | **Yes** — a web-dashboard viewer streams it too |

**Recommendation: expose BOTH, let the user choose, label the tradeoff.** Not
silently agnostic — they are genuinely different tools:

- **WebRTC = fast UI iteration + the feedback→patch→see loop.** Instant Fast
  Refresh is the whole point. This is where you live while building UI.
- **Hermes = real-device validation.** Camera, sensors, real touch, real perf —
  before you ship.

The picker should read like: **"⚡ Instant (simulator, streamed)"** vs.
**"📱 Real device — camera & sensors, slower reload"**. The exact words the user
used: *Hermes = full camera/peripheral but slow; WebRTC = instant but simulated.*

## The speed model (why WebRTC is instant)

The key insight, and the reason this is worth building. In WebRTC mode the app is
a **normal RN app on Metro** in the sim:

1. Remote runner patches a file.
2. Metro's file watcher sees it, computes the **HMR delta** (only changed modules).
3. Pushes the delta over the Metro HMR socket to the app.
4. **React Fast Refresh** applies it — component state preserved — in **well under
   a second**.
5. The next streamed video frame shows the new UI.

Compare Hermes: every change recompiles the **whole** HBC bundle, pushes it, and
does a full reload (state lost). So for the feedback→prompt→patch→see-result loop
the user described, WebRTC is dramatically faster — comparable to Flutter hot
reload, because it *is* the same class of mechanism (Metro Fast Refresh ≈ Dart VM
hot reload). The only added latency is network RTT on the **video** (frames), not
on the code update.

**Design requirement:** the WebRTC RN session must run the app in **`--dev` mode
against Metro with Fast Refresh ON**, reusing the existing dev-server (`/dev/start
framework=expo`). Never a release Hermes bundle — that would throw away the entire
speed advantage.

## Feedback + shake (client → server)

Design locked with the user: **shake is on the client, the simulator is on the
remote box.**

1. The viewer owns the shake trigger: the **phone's ShakeDetector** (physical
   shake) OR a **"Shake" button in the web dashboard** — so it works with or
   without the Yaver mobile app.
2. On trigger, the viewer sends the **`shake` session command** (landed).
3. The agent **injects a hardware shake into the sim** (`injectSimulatorShake`):
   iOS sim via the Simulator `Device ▸ Shake` menu (osascript — there is no
   `simctl shake`), Android emu via an `adb emu sensor set acceleration` burst
   that crosses the SDK's 1.8g threshold.
4. The **third-party app's own Yaver Feedback SDK** — running **standalone** in
   the real sim (NOT suppressed, because it is not inside the Yaver container;
   `isYaver` is false) — fires its overlay **inside the sim**.
5. That overlay **streams back** to the viewer over the same WebRTC video.
6. The user annotates / writes a prompt in the SDK's overlay; the remote runner
   patches; **Metro Fast Refresh** renders it instantly (see speed model).

Robustness: the `shake` command also emits a `feedback-launch-request` on the
events channel, so if hardware-shake injection is a no-op on a given host, a
viewer-side overlay (or an SDK subscribed to that channel) still triggers.

**SDK context-awareness (already correct, worth stating):** the RN feedback SDK
yields to Yaver only when `isYaver` (Hermes container). In a streamed sim it is
standalone, so its normal shake→overlay path runs — exactly what we want. A
future `YaverFeedback.trigger()` programmatic entry point would make step 4
deterministic instead of relying on accelerometer injection.

## Device type & screen size (iPhone 14 viewer, iPhone 17 Pro sim)

**Viewing does NOT require matching device types.** The WebRTC stream is just
pixels: the viewer scales the sim's video **aspect-fit** to its own screen, and
touch coordinates map **proportionally** back to sim coordinates. An iPhone 17 Pro
sim streamed to an iPhone 14 viewer just renders scaled — no device match needed.

**Yaver manages the sim device pool** (the user's instinct is right):
- The sim device is chosen by **what you're testing for**, not by the viewer's
  phone model — pick iPhone SE / iPad / Pixel etc. per the target.
- Yaver **creates/boots on demand** and **shuts down idle sims** to reclaim disk
  + RAM — the same scale-to-zero discipline as Hetzner boxes (a booted sim is
  cheap but disk/RAM-heavy; don't leave a pool running). `simctl create/delete`,
  `adb`/`avdmanager` for Android.
- Never tie the sim to the viewer's model; let the user pick, default to a common
  device (iPhone 15/17), and clean up after the session.

## Surfaces: any-to-any, mobile→mobile is the default

Yaver's reach is broad — AR/VR, watch, car, tablet, tvOS, phone — as BOTH the
streamed simulator and the client viewer. RN/Expo now offers the full sim
fan-out (iPhone/iPad/watchOS/tvOS/visionOS + Android emulator/wear/TV/XR/auto +
real devices), capability-probed so unavailable ones are shown disabled with a
reason.

**UX rule:** default to the common case — **mobile sim → mobile client** — shown
prominently. Every other surface (tvOS, watch, tablet, …) lives behind an
"other surfaces" disclosure so the picker stays clean but ANY surface is
one tap away. A user who only wants tvOS can pick just tvOS.

## Per-client-surface input mapping

The **client** surface decides how feedback is triggered and how "clicking"
works, because a phone, a TV and a watch have different input hardware. The
control channel is the same; only the viewer-side mapping differs:

| Client surface | Feedback trigger | "Click" / control mapping |
|---|---|---|
| **Phone / tablet** | Physical **shake** (ShakeDetector) → `shake` command | Touch → x/y tap/drag sent to the sim |
| **Web dashboard** | A **"Shake" button** | Mouse click → x/y; keyboard passthrough |
| **tvOS** | **No shake** — voice: **STT** ("feedback"/"shake") triggers it, **TTS** reads results back | The tvOS **focus engine**: Siri-Remote D-pad = directional focus moves, Select = activate; swipes on the remote's touch surface map to scroll/drag. NOT raw x/y — send focus-move + select control events. |
| **watch** | Wrist raise / a tap-and-hold, or voice | Digital-crown scroll + tap → scroll + select control events |
| **car** | Voice only (hands-free) — STT/TTS | Restricted; voice-driven, no fine pointing |
| **AR/VR** | Voice or a controller button | Gaze/controller ray → mapped to sim coordinates |

Design consequence: the `shake` command is the phone/web path; other surfaces
send their native trigger (voice intent, focus-select) over the same session
control channel. The viewer owns the translation; the agent injects the
platform-appropriate event into the sim.

## Mature remote-side simulator management

Yaver manages the sim/emulator lifecycle on the remote box (the driver methods
exist in `testkit/`: `Boot`, `Install`, `Launch`, `Shutdown`, `list devices`,
`list runtimes`, plus `DeviceType` selection). The mature layer to build on top:

- **List** available device types + installed runtimes (iOS: `simctl list`;
  Android: `avdmanager list` / `adb devices`).
- **Pick / create** the device type you're testing for (`simctl create`,
  `avdmanager create avd`), not tied to the viewer's model.
- **Boot on demand**, **shut down / delete when idle** — a booted sim is cheap
  but disk/RAM-heavy, so apply the same scale-to-zero discipline as Hetzner
  boxes: don't leave a pool running.
- Expose it as `ops` verbs + MCP so every surface (CLI/web/mobile) can manage
  the pool.

## PROVEN end-to-end on a real app (2026-07-21)

The full native flow was validated on the mac mini against **yaver-todo-rn**
(Expo 54 / RN 0.81.5):

```
** BUILD SUCCEEDED **        # xcodebuild, generic iOS Simulator dest, ARCHS=arm64, Debug
LAUNCHED io.yaver.todorn     # simctl install + launch onto the booted device
in sim (1=yes): 2            # app confirmed running in the simulator
```

So the expo-free pipeline — `xcodebuild` (generic destination + host arch, Debug)
→ `simctl install` → `simctl launch` — works on a real RN app, and the existing
frame-pump (`startFramePump`/`captureJPEGFrame`) streams whatever is on that
booted device. The architecture is confirmed; what remains is per-app build
robustness and the client UIs.

**Per-app build reality (confirmed across apps):** a MINIMAL app (yaver-todo-rn,
clean pods) builds through; heavier apps hit their OWN native-module snags under
Xcode 26.4 — talos = `fmt` 11 consteval (Podfile patched), Yaver mobile =
`NitroIap` arm64-sim linker. These are guest-project/toolchain issues, and they
are exactly what the "Try to Fix" AI-runner loop resolves. Setup friction also
seen and handled: fresh clones need `npm install` before `pod install`, and a
stale `Podfile.lock` needs a reset — the Yaver flow should run these preflights.

## FULL LOOP PROVEN WORKING (2026-07-21)

Validated visually end-to-end on the mac mini with yaver-todo-rn: the **live Todo
app renders in the simulator and streams its real UI** — the "What needs doing?"
composer, All/Active/Completed filters, empty state, and the RN dev-mode
"Open debugger to view warnings" banner confirming **Metro Fast Refresh is
connected**. The one gap the first capture exposed — the RN red screen "No script
URL provided" — was Metro not running; wiring `ensureDevServerForProject` before
launch fixed it, and a fresh `simctl terminate` + `launch` loaded the bundle. So:

    build (xcodebuild generic dest + arm64, Debug)
      → ensure Metro (Fast Refresh)
      → simctl install + launch
      → simctl io screenshot  →  the live app, streamed

Every stage is proven on real hardware. Gotcha to encode: `simctl launch` on an
already-running instance returns the old PID and does NOT reconnect to Metro —
terminate first, then launch, so the app picks Metro up.

## Fast vibing — PROVEN (2026-07-21)

The landing-demo loop works. Edited one line in the running todo app
(`backgroundColor: "#0f172a"` → `"#6E56F6"`); the streamed sim repainted **purple
with app state preserved** (still "All clear" / "Nothing here" / "All" selected)
— no rebuild, no restart. That is React Fast Refresh over Metro HMR.

**Latency breakdown (why it's instant):**

    save → Metro file-watch (<50ms)
         → HMR delta, changed modules only (~50–200ms for a style edit)
         → push over HMR WebSocket to the app (localhost, <10ms)
         → React Fast Refresh re-render, state kept (~16–50ms)
         → sim repaints (1 frame)
         → WebRTC stream delivers the new frame (network RTT + frame interval)

Code→sim-update is **~100–300ms (sub-second)** — the Flutter-hot-reload class.
The ONLY viewer-added latency is frame delivery (RTT + the JPEG-DC rate today,
60 fps with a baguette-style encoder). Contrast Hermes: every edit rebundles the
whole HBC (seconds) and full-reloads (state lost). This is the entire reason to
offer the WebRTC surface for the iterate-on-UI loop.

## CRITICAL: capture method, not Fast Refresh, is the latency — use H.264 video

Measured on the mini, and it reframes the streaming architecture:

| Stage | Time | Note |
|---|---|---|
| Metro HMR (edit → bundle ready) | **~764 ms** | Fast Refresh is FAST — the vibe loop itself is fine |
| `simctl io screenshot` (one frame) | **~18 s** | pathologically slow on Xcode 26.4, headless OR with Simulator.app open |
| `simctl io recordVideo --codec=h264` | **real-time** | continuous hardware-encoded H.264, no per-frame penalty |
| ffmpeg extract 1 frame from the H.264 | **0.03 s** | decoding is trivial |

So the 18 s "vibe latency" was **entirely the frame-pump using `simctl io
screenshot`** — a broken capture path. Fast Refresh is sub-second. The fix, which
is also the premium **Relay Pro** feature, is a real **H.264 video stream**:

- **iOS sim:** `simctl io recordVideo --codec=h264` → pipe the H.264 elementary
  stream into the Pion WebRTC video track (`webrtc-rtp-h264-v1`, which the
  RemoteRuntimeViewer ALREADY negotiates). Never screenshot-poll iOS.
- **Android emulator / redroid:** `scrcpy` (or the emulator's `-gpu`/`adb`
  H.264) → same RTP track. redroid on Linux is a strong fit here — cheap,
  containerized, and streams fast.

**Backend capture-tool selection (speed-first):** the agent should choose the
capture per target — H.264 recordVideo (iOS sim), scrcpy/H.264 (Android/redroid),
and fall back to JPEG-DC screenshot ONLY where neither exists. The frame-pump's
current unconditional `Screenshot()` path is the thing to replace; the H.264
track turns the ~18 s screenshot loop into a live 30–60 fps stream and is what
makes end-to-end edit→see land in the ~1–2 s range (Fast Refresh 764 ms + a
video frame), hitting the ≤3 s target. This is the seam that sells Relay Pro.

Native surfaces (Swift/Kotlin/Flutter) run in the SAME sim/emulator and stream
the SAME way — they need a real simulator/emulator on the remote (macOS for iOS),
which is exactly what the sim-management layer + Cloud Workspace provide.

## Deep analysis — streaming EACH platform, including the Apple sims we can't avoid

Two facts force the design:
1. **Xcode 26 removed `simctl io recordVideo` to stdout** ("rendering to standard
   out is no longer supported") — so the stdout→NAL→RTP pump is dead for iOS
   (`iosSimulatorTarget.CanEncodeRTPH264()` returns false).
2. **This mini's `simctl io screenshot` is degraded (~17s/frame)** — so the JPEG
   data-channel fallback is unusable here too.
3. But **we cannot avoid Xcode/Apple sims** for watchOS, CarPlay, tvOS, visionOS,
   and iOS-specific testing — those only exist as Apple simulators on macOS.

So Apple-platform streaming must be solved, not sidestepped. Three capture
sources, in preference order:

**A. ScreenCaptureKit — capture the Simulator.app WINDOW (best, universal).**
Every Apple sim (iPhone/watch/tv/vision/CarPlay) renders into a Simulator.app
window. macOS **ScreenCaptureKit** (`SCStream`) captures that window as a
hardware-encoded video feed at up to 60 fps, independent of `simctl` — so it
sidesteps BOTH the recordVideo-stdout removal AND the degraded-screenshot bug.
This is the robust path for all Apple platforms. Wrap it via a tiny Swift helper
(`SCStream` → H.264 → stdout → our existing `AnnexBReader`/RTP track) or
`ScreenCaptureKit`-based `ffmpeg`. It also gives CarPlay/tvOS/watch, which have no
`simctl screenshot` story at all.

**B. recordVideo-to-FILE + fragment tailer (fallback where SCK is unavailable).**
`simctl io recordVideo` to a FILE still works on Xcode 26 (only stdout was
removed); it writes a FRAGMENTED MP4, which is readable WHILE growing. Tail the
file, feed each new `moof`/`mdat` fragment to `MP4ToAnnexB` (already in
`h264_extract.go`) → NAL → RTP. The code comment at
`remote_runtime_target.go:207` explicitly names this as the planned replacement.

**C. JPEG data-channel screenshot (last resort).** Only where A and B fail AND
the box's `simctl screenshot` is healthy (<1s). Never on this degraded mini.

**Android/redroid needs NONE of this** — `adb exec-out screenrecord
--output-format=h264 -` streams H.264 to stdout directly into the existing RTP
track (`androidTarget.CanEncodeRTPH264()` = true), plus `adb` gives tap/swipe/
text/key for free. So:

**Platform routing (speed + lightweight first):**

| Guest | Preferred host | Capture | Why |
|---|---|---|---|
| RN / Expo | **Android emulator / redroid** | adb screenrecord H.264 | cross-platform; Linux; fast; no Xcode |
| Flutter | **Android emulator / redroid** | adb screenrecord H.264 | cross-platform; Linux; fast |
| Kotlin | Android emulator / redroid | adb screenrecord H.264 | Android-native |
| Swift | iOS sim (required) | **ScreenCaptureKit window** → RTP | iOS-native, Xcode mandatory |
| watchOS/tvOS/CarPlay/visionOS | Apple sim (required) | **ScreenCaptureKit window** → RTP | Apple-only, Xcode mandatory |

The lesson the user pushed to: **default cross-platform guests to Android/redroid
(lightweight, Linux, works today), and reserve the heavier Apple-sim path — with
ScreenCaptureKit as the capture that makes it actually stream — for what genuinely
requires Xcode.** iOS being un-streamable on THIS mini is not a wall for the
product: the Android path ships now, and ScreenCaptureKit unblocks the Apple
sims on a healthy Mac.

## Input / gesture control — wrap first-party + permissive OSS

The web viewer already forwards pointer input over the control DataChannel (the
signaling layer exists). Simulator-side injection: `simctl` only does a basic
tap, so richer gestures (swipe, pinch-zoom, multi-finger) wrap existing tools:

- **facebook/idb** — HID tap/swipe on iOS sims (`idb ui tap/swipe`). **MIT.**
- **tddworks/baguette** — headless iOS-26 sim farm + host-side input injection
  (taps, swipes, **multi-finger gestures**) + **60 fps streaming**. Closest fit
  to this whole feature; verify its license before bundling.
- **Genymobile/scrcpy** — Android mirror + control (tap/swipe/pinch). **Apache-2.0.**
- **whitesmith/ios-simulator-mcp** — MCP wrapping simctl+idb (tap/type/swipe/
  a11y tree). **MIT.**

## Licensing — clean to wrap

Everything in this pipeline is either a first-party Apple/Google tool we merely
INVOKE (no redistribution) or permissive OSS:

| Component | License | How we use it |
|---|---|---|
| xcodebuild / simctl | Apple (Xcode) | Invoke on the user's own Mac to build the user's own app |
| Metro | MIT | Orchestrated from the **user's** node_modules — not bundled by Yaver |
| Expo / React Native | MIT | The guest's own dependency |
| gradle | Apache-2.0 | Invoke |
| adb / Android SDK | Apache-2.0 | Invoke |
| redroid | Apache-2.0 | Container image, run as-is |
| idb | MIT | Wrap for iOS gesture injection (preserve notice) |
| scrcpy | Apache-2.0 | Wrap for Android control (preserve NOTICE) |

Key point: Yaver does not REDISTRIBUTE Metro/Expo/RN — they run from the guest
project's own install; Yaver is the conductor. Apple tools are only invoked, never
shipped. MIT/Apache wraps just need their notices preserved. No copyleft in the
path. `baguette` is the one to license-check before vendoring.

## What is landed (this change)

- **Agent capabilities** (`remote_runtime.go`): RN/Expo is now
  `RemoteRuntimeEligible` as a **secondary** surface (Hermes stays
  `PrimarySurface`), with iOS-sim + Android-emulator + device targets.
  `FeedbackSurface = "client-shake-remote-sim"`, and `FeedbackSDKNote` describes
  the client→server shake flow.
- **`shake` session command** (`remote_runtime.go`): injects a hardware shake
  (`injectSimulatorShake`, iOS/Android best-effort) and emits a
  `feedback-launch-request`. Tested for dispatch + command construction.

## What remains (needs a sim + a device to validate)

1. **The RN→sim build+boot+stream flow.** `native_build.go` has no `expo run:ios`
   / `run:android` path yet; a WebRTC RN session must build the app in `--dev`
   mode against Metro, install into the booted sim, launch, and stream. This is
   the core runtime piece and needs simulator hardware to build against.
2. **Metro Fast Refresh wiring** end-to-end (reuse `/dev/start framework=expo`).
3. **Sim lifecycle management** (create/boot/delete, device-type picker).
4. **Viewer UI** — surface "⚡ Instant (WebRTC)" vs "📱 Real device (Hermes)" on
   mobile + web, aspect-fit scaling, coordinate mapping, the shake trigger.
5. **`YaverFeedback.trigger()`** deterministic SDK entry point.

These are a focused follow-up: they need a booted simulator to exercise, which the
build box can provide once free.

## Real-hardware finding (2026-07-21) — the expo→sim destination issue

Running the actual flow against the mac mini (Xcode 26.4, a `simctl`-booted
iOS 26.4 iPhone, talos target 15.5 — a compatible runtime) surfaced a concrete
integration bug that only real testing catches:

```
xcodebuild: error: Unable to find a destination matching the provided
destination specifier: { id:3D94A65E-… }
Available destinations for the "Talos" scheme:
  { platform:macOS … My Mac }, { … Any iOS Device }, { … Any iOS Simulator Device }
```

xcodebuild listed **only placeholders — no concrete simulator** — even after
`open -a Simulator` and with the device booted and runtime-compatible. So
`expo run:ios --device <udid>` (which shells to `xcodebuild -destination
id=<udid>`) can't resolve the booted sim on this box. This is a
CoreSimulator/xcodebuild enumeration issue (Xcode 26.4), not a code bug in the
flow — the flow ran correctly up to xcodebuild.

**Resolved 2026-07-21 — the flow is now expo-free and first-party:** dropped the
expo CLI entirely for `xcodebuild` (iOS) + `simctl`, and `gradlew` + `adb`
(Android). Two findings from iterating on the mini fixed the exact invocation:

1. **Generic destination, not `id=<udid>`.** `-destination
   'generic/platform=iOS Simulator'` resolves cleanly (bundle id + build dir),
   then `simctl install` puts the .app on the exact booted device. The
   udid-specific destination form is what failed to enumerate.
2. **Single host arch.** The generic destination builds both slices; the x86_64
   slice fails to compile on Apple Silicon (`fmt` etc.), so `ARCHS=<host arch>`.

Both are encoded in `iosSimBuildArgs` and proven on the box (generic destination
+ arm64 compiles the arm64 slice; earlier it died on x86_64).

**Remaining proof blocker is the GUEST project, not the flow:** talos/mobile's
`fmt` pod hits a `consteval … is not a constant expression` compile error under
Xcode 26.4's clang — a stale-pod / new-toolchain mismatch in talos itself, which
any Xcode-26.4 build of that project hits regardless of Yaver. The flow is
correct; a guest whose pods compile under the host Xcode (or talos with an
updated `fmt`) builds through. This is guest maintenance, tracked separately, not
a Yaver bug. `iosSimBuildArgs` / `buildAndLaunchRNiOS` need no further change for
the happy path.
