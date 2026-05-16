# Physical-Device Remote Runtime — Audit + Plan

> **Status: Phases 1–3 implemented + merged to main (2026-05-16).
> Code is the source of truth — re-grep every path before acting;
> this drifts.**
> Parent: `docs/native-webrtc-web-streaming.md` (the emulator/simulator
> pipeline this extends). Trigger: an ARM Linux cloud box
> (linux/aarch64) can never run the Android emulator — Google ships no
> emulator binary for that arch (see memory
> `project_no_linux_arm64_android_emulator`), so streaming a third-party
> app for testing must come from a **physical device** attached to some
> agent host.
>
> **Implementation status:**
> - Phase 1 ✅ `android-device` — `remote_runtime_android_device.go`
>   (commit `2dd122e8`).
> - Phase 2 ✅ `runtimeTarget` adapter — `remote_runtime_target.go`,
>   9 switches collapsed (commit `38357c37`).
> - Phase 3 ✅ `ios-device` via WebDriverAgent — `wda_client.go` +
>   `remote_runtime_ios_device.go` (commit `ad5815f2`). WDA HTTP
>   contract + control are tested against an httptest fake WDA.
>   **Out-of-session remaining:** on-device pixel validation needs a
>   signed WDA on a wired iPhone + a Mac running ffmpeg against the
>   live MJPEG server. Until then `ios-device` capture is built but
>   unproven on real hardware; control/dims/screenshot are contract-
>   verified.

## 1. The "sync comm layer" already exists

It is the remote-runtime WebRTC session, two halves:

- **Video (host→browser):** capture subprocess → H.264 → Pion track.
  `remote_runtime_video_track.go:222` (`spawnCapture`). Today only
  `adb screenrecord` (`android-emulator`) / `xcrun simctl io
  recordVideo` (`ios-simulator`).
- **Control (browser→device):** WebRTC "events" data channel →
  `ExecuteControl` → per-target driver. `remote_runtime_webrtc.go:616`.
- **Device transport:** `adb` (USB serial *or* `ip:port` wireless) for
  Android; `usbmuxd`/`xcrun devicectl` for iOS.

Nothing new needs inventing. The blocker is that every dispatch is a
hardwired 2-case switch on `targetID ∈ {"android-emulator",
"ios-simulator"}`. No physical targetID exists anywhere
(`remote_runtime.go:24-53`, `remote_runtime_webrtc.go:102-111`,
`remote_runtime_dims.go:38-54`).

## 1a. Host → target capability matrix (emulator/simulator stay first-class)

Physical-device is an **addition, not a replacement**. The
emulator/simulator paths remain the default wherever the host can run
them; physical-device is the fallback for hosts that physically can't
(the ARM Linux box) or when the dev wants real-hardware fidelity.

| Host | iOS sim | Android emu | Physical (preferred where no local virtual target) |
|---|---|---|---|
| **macOS (Apple Silicon)** | ✅ default (`xcrun simctl`, HVF) | ✅ default (`emulator` darwin_arm64, HVF, fast) | optional (USB iPhone/Android) |
| **macOS (Intel)** | ✅ default | ✅ default (`emulator` darwin_x64) | optional |
| **Linux x86-64 + /dev/kvm ("capable Linux")** | ❌ (no iOS toolchain) | ✅ default (`emulator` linux_x64, KVM, fast) | optional Android via wire |
| **Linux x86-64, no KVM (cloud VPS)** | ❌ | ⚠️ emulator binary exists but TCG = minutes/boot, marginal | physical Android preferred |
| **Linux aarch64 (ARM cloud, e.g. test box)** | ❌ | ❌ no emulator binary at all (see §intro) | physical Android only path |
| iOS-native build anywhere | requires a Mac builder (existing dispatch) — unchanged | | |

Rules this plan must honor:

- On macOS and capable Linux, `probeIOSSimulatorTarget` /
  `probeAndroidEmulatorTarget` keep returning **enabled** and stay the
  recommended target — do not down-rank or hide them in favour of
  physical.
- Target selection precedence the dashboard/CLI should offer:
  **local virtual target (sim/emu) if the host supports it →
  physical device via wire → paired Mac/x86 builder**. Physical is
  surfaced first only when no local virtual target is available
  (capability-probed, never hardcoded per host name — see memory
  `feedback_yaver_is_for_everyone`).
- The Phase 2 `RuntimeTarget` refactor is **strictly
  behaviour-preserving** for `android-emulator` and `ios-simulator`:
  same commands (`xcrun simctl`, `emulator`/`adb`), same
  `kvmAvailable()`→TCG fallback for capable-Linux-without-KVM, all
  existing `remote_runtime_*_test.go` stay green unmodified. Physical
  targets are *new impls of the same interface*, not a rewrite of the
  virtual ones.

## 2. State on a physical device (per framework)

| Framework | Build | Install/Launch | Video | Control | Stream |
|---|---|---|---|---|---|
| React Native | ✅ | ✅ | ❌ | ❌ | ❌ |
| Flutter | ✅ | ✅ | ❌ | ❌ | ❌ |
| iOS-native | ✅ (Mac) | ✅ | ❌ | ❌ | ❌ |
| Kotlin-native | ✅ | ✅ | ❌ | ❌ | ❌ |

Build/install/launch on real devices is **complete** for all four via
`yaver wire push` / `wireless push` (`wire_cmd.go`, `wireless_cmd.go`,
`device_install.go`, `native_build.go`); USB + wifi/mDNS detection done
(`listAndroidWireDevices`, `listIOSWireDevices`, `adbPair`/`adbConnect`).

**Single architectural gap:** `yaver wire` (build/install) and
remote-runtime (stream/control) are two siloed subsystems that never
connect. `wire push` stops after launch; remote-runtime rejects any
non-emulator/simulator target.

Asymmetry that drives phasing:

- **Physical Android ≈ 1 day.** `adb screenrecord` and `adb shell
  input` work identically against a real serial — the code just isn't
  in the switch. `testkit/driver_device.go` already has a `USBDevice`
  driver ~60% built (Install/Launch/Screenshot/Verify).
- **Physical iOS = a project.** No `simctl` on a real iPhone. Needs
  WebDriverAgent (control + MJPEG) or a CoreMediaIO/AVFoundation
  capture daemon over the usbmuxd tunnel. `driver_device.go:269`
  already notes "iOS real-device taps need WebDriverAgent (planned)".

## 3. RN nuance

`wire push` on a physical device builds RN JS **baked into a standalone
native APK/IPA** — the Yaver container / Hermes-push path is *not*
involved (that is the iOS-only in-container `YaverBundleLoader`; Android
has none, per memory `project_android_bundleloader_missing`). So for
physical-device streaming, RN is just "an app" and shares the
Flutter/native capture+control path. No RN-special stream work. This
respects the parent doc's hard rule: the Hermes flow is untouched.

## 4. Design (shared foundation)

1. **Add physical targetIDs** `android-device`, `ios-device` with
   probes that resolve a real serial/UDID via the existing
   `pickWireDevice` (`wire_cmd.go:850`).
2. **Collapse the per-target ad-hoc switches into one `RuntimeTarget`
   interface** (`Capture/Tap/Swipe/Text/Key/Dims`), 4 impls
   (emu/sim/android-device/ios-device). Today these are copy-pasted
   2-case switches in 5–7 places; adding physical as more branches
   quadruples the duplication. Do the refactor before iOS-physical.
3. **Bridge wire→remote-runtime:** a session with a physical targetID
   resolves the serial via existing detection, optionally
   builds+installs via the existing `native_build` path, then attaches
   capture/control.

## 5. Phased plan

### Phase 1 — Physical Android (RN / Flutter / Kotlin-native). Small. Do first.
- targetID `android-device`; resolve serial from `adb devices` (USB) /
  `ip:port` (wireless), exclude `emulator-*`.
- Capture: add the case to `spawnCapture` calling the already
  serial-generic `spawnAdbScreenrecord(ctx, serial)`.
- Control: `adb -s <serial> shell input tap/swipe/text/keyevent` —
  `AndroidEmuDriver` already does this; parameterize by serial.
- Dims: `adb -s <serial> shell wm size`.
- Test (repo convention — real procs, stub `adb` on PATH like
  `android_sdk_install_test.go`): assert capture+control cmds carry
  `-s <serial>` and never `emulator-`.
- **Deliverable:** `wire push` an RN/Flutter/Kotlin app to a real
  Android phone, then stream+control it in web-headless — the deferred
  Flutter-todo loop, unblocked without an emulator.

### Phase 2 — `RuntimeTarget` adapter refactor
- Land the interface, migrate emu/sim/android-device (behavior-
  preserving; covered by existing `remote_runtime_*_test.go`).
- Table test: every targetID implements every method.

### Phase 3 — Physical iOS (Swift / RN / Flutter). Hard.
- Launch WebDriverAgent over the usbmuxd tunnel.
- Control via WDA HTTP; video via WDA `/mjpeg` → transcode to H.264
  into the Pion track (≈10–15 fps). Evaluate CoreMediaIO (Mac-only,
  higher quality) as a follow-up.
- Gated on macOS + Xcode + signing — reuses `resolveAppStoreConnectKey`
  (`wire_cmd.go:1143`); no new secret surface.

## 6. Risks / open decisions

- **iOS physical capture quality:** WDA MJPEG ≈10–15 fps. WDA-first;
  CoreMediaIO later if the demo loop needs it.
- **Wireless Android latency:** `screenrecord` over `adb tcpip` adds
  lag — fine for functional testing; surface it in the UX copy.
- **Effort:** Phase 1 ≈1d, Phase 2 ≈1d, Phase 3 ≈3–5d (WDA is the bulk).

## 7. Precise anchors (re-grep before use)

- Detection: `wire_cmd.go:39-625`, `wireless_cmd.go:37-756`
- Build: `native_build.go:222-258`, `wire_cmd.go:891-1074`
- Install/launch: `device_install.go:76-158`, `native_build.go:135-169`
- Capture (emu-only): `remote_runtime_video_track.go:222-292`
- Control (emu-only): `remote_runtime_webrtc.go:616-762`
- Dims (emu-only): `remote_runtime_dims.go:38-54`
- Target enum: `remote_runtime.go:24-53`, `_webrtc.go:102-111`
- Physical driver seed (~60%): `testkit/driver_device.go:1-250`
