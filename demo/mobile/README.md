# Yaver Feedback SDK — Mobile Showcase Apps

Each subdirectory here is a self-contained mobile demo whose only
purpose is to exercise the Yaver Feedback SDK from a real app. The
upcoming SDK videos shoot against `todo-rn` first (RN, both standalone
and inside Yaver hot-reload) — the others are alternate-stack
demonstrations of the same flow.

## Apps

| App | Stack | Hot Reload path | Standalone path | Build host |
|---|---|---|---|---|
| `bento` | Expo / React Native | Hermes (HBC) | TestFlight / Play | macOS or x86 Linux |
| `todo-rn` | Expo / React Native | Hermes (HBC) | TestFlight / Play | Any (Hermes builds run on any node Yaver agent supports) |
| `todo-kt` | Native Android (Kotlin) | WebRTC remote-runtime | APK install | macOS or x86 Linux; ARM64 Linux works via qemu-user-static + aapt2 emulation |
| `todo-swift` | Native iOS (SwiftUI) | WebRTC remote-runtime | TestFlight / .app sideload | **macOS only** — Apple SDK + xcodebuild, no Linux path |
| `todo-flutter` | Flutter / Dart | WebRTC remote-runtime | APK / .app | macOS or x86_64 Linux; Flutter SDK has no official linux-aarch64 release |
| `acme-store` | Expo / React Native | Hermes (HBC) | TestFlight / Play | (legacy landing-page demo, kept temporarily) |

## Why two RN-style entries (bento, todo-rn) and three native ones?

- `bento` is the existing meal-prep demo used in older SDK videos.
  The mock data is product-shaped and the screens look like a real
  consumer app, which is good for marketing but heavy for a SDK
  call-flow video.
- `todo-rn` is the deliberately-minimal counterpart: one screen,
  AsyncStorage, no auth, no backend. Built specifically so the SDK
  flow (shake → modal → reload → vibe → fix) reads as the only
  thing happening on screen.
- The native apps (`todo-kt`, `todo-swift`, `todo-flutter`) cover
  the WebRTC remote-runtime path for SDKs that don't have a
  Hermes-equivalent bundle format. Same Todo UX as `todo-rn` so the
  comparison videos can use one storyboard across stacks.

## Hot Reload vs WebRTC remote-runtime

- **Hermes path** (RN/Expo apps): the agent's `/dev/build-native`
  produces an HBC bundle, the Yaver mobile container loads it via
  `ExpoReactNativeFactory`. Bundle is loadable when the project's
  native deps match the host's runtime family — see the
  `runtime_family_test` suite for the version-drift gate. Today
  reanimated must be 4.1.x to match the host.
- **WebRTC path** (native apps): the agent dispatches a build to a
  host with the right toolchain (Mac for Swift, Mac/x86-Linux for
  Kotlin/Flutter), launches the resulting app on a sandboxed device
  or emulator, and streams the UI surface to the paired phone over
  the QUIC/relay session. The phone sends taps back the same way.
  Build host doesn't have to be the Yaver agent host.

## Build verification status (yaver-test-ephemeral, linux/arm64)

| App | Verified build | Verified hot-reload load |
|---|---|---|
| `todo-rn` | ✅ `npm install` + `expo prebuild --platform android` + `expo export --platform web` (2.61 MB web bundle) | ✅ `/dev/build-native` returns `status:ok`, 4.3 MB bundle, BC96 |
| `todo-web` | ✅ `npm install` + `npm run build` (4 pages prerendered, 92.8 KB JS) | n/a (web SDK uses floating-button trigger, no Hermes) |
| `todo-kt` | ✅ `./gradlew assembleDebug` produces `app/build/outputs/apk/debug/app-debug.apk` (qemu emulation for x86 aapt2) | n/a (WebRTC path; install + run on a device/emulator) |
| `todo-swift` | ⚠️ Linux box can't build (Apple SDK missing) — verified in scanner only; build on Mac with `xcodegen + xcodebuild` | n/a |
| `todo-flutter` | ⚠️ Flutter SDK ships no official linux-aarch64 stable build — verified in scanner only; build on Mac or x86 Linux | n/a |

The two ⚠ entries are not bugs in the apps — they're host-toolchain
limitations on yaver-test-ephemeral specifically. Both compile clean
on a Mac dev box with the Apple/Flutter toolchains installed.
