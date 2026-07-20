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
