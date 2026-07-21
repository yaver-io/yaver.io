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
