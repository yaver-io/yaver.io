# Apple stack native release gate — 2026-08-17

This is a dated execution record, not an evergreen source of truth. Re-read
the deploy scripts, XcodeGen specs, and app clients before reusing it. Code and
live toolchain inventory win when they disagree with this file.

## Decision

Chromium evidence is sufficient only for the browser/RN-web lane. A Chromium
window sized like a TV, watch, or headset is a **surrogate**, not that Apple
client. It cannot certify SwiftUI, the Apple focus engine, Siri Remote events,
watch navigation, CarPlay templates, visionOS windows, native entitlements,
signing, or packaging.

Every Apple surface therefore needs two different proofs:

1. Shared closed loop: the remote runner changes the project and the common
   browser render path changes pixels.
2. Native closed loop: the real app builds, installs, launches, connects, and
   renders in that platform's Apple simulator. Archive/export then proves the
   release artifact.

Neither proof substitutes for the other.

## Resource contract

The development Mac has 8 GB RAM. Run exactly one expensive lane at a time:

- one Xcode build;
- one booted simulator;
- one Metro/dev-server consumer;
- no simultaneous Chromium recording;
- shut down the current simulator before starting the next surface.

A named skip is not a pass. Missing runtimes, credentials, entitlements, or
automation hooks are release blockers with an explicit remedy.

## Live inventory at the start of this run

- iOS Simulator runtime: 18.3
- tvOS Simulator runtime: 18.2
- watchOS Simulator runtime: 11.2
- visionOS Simulator runtime: not installed
- tvOS native UI-test target: `YaverTVUITests`
- visionOS native UI-test target: `YaverVisionUITests`
- watchOS native UI-test target: absent at the start of the run
- CarPlay is a scene inside the iOS artifact, not a separate binary
- watchOS is embedded in the iOS/TestFlight artifact, not uploaded to a
  standalone App Store record

The absent visionOS runtime must be installed from Xcode Settings → Components
before native visionOS simulation can be called green. The absent watchOS UI
automation is a test-surface gap: build/install/launch/screenshot is useful,
but it does not prove task/render interaction until an actual UI arc exists.

## Remote runner and shared render evidence

Target machine: the owned device named `ubuntu-4gb-hel1-1`.
Target project: `sfmg`.
Runner: OpenCode.
Measured model: `deepseek/deepseek-flash`.

The task completed and the lightweight Chromium loop measured a transition to
`rgb(211, 47, 47)` on web and RN-web device contexts. The TV, watch, and vision
Chromium profiles also changed, but their manifest correctly labels them
`SURROGATE_PIXELS`; those rows are not native release evidence.

Artifacts for the shared loop live under:

`e2e/test-results/lightweight-color/2026-08-17T17-54-31-604Z/`

## Sequential native matrix

### 1. tvOS

Authoritative client: `tvos/YaverTV`.

Native gate:

1. Generate `tvos/YaverTV.xcodeproj` from `tvos/project.yml`.
2. Build the app and UI-test bundle for the available tvOS simulator.
3. Boot only that simulator.
4. Run `YaverTVUITests` against the real owned Ubuntu agent and `sfmg`.
5. Drive focus/remote navigation through named XCUITest elements.
6. Confirm the task terminal state and preview using simulator screenshots.
7. Require the native screenshot's pixel verdict, not server-side frame data.
8. Shut down the simulator.
9. Archive/upload only after the native arc and route-parity test pass.

The purpose-built orchestration is `e2e/tvos-sim-vibe-loop.mjs`. Its result is
invalid if Xcode reports skipped or zero tests.

### 2. watchOS

Authoritative client: `watch/YaverWatch`.

Native gate:

1. Generate `watch/YaverWatch.xcodeproj` from `watch/project.yml`.
2. Build for the installed watchOS simulator.
3. Boot one watch simulator, install, and launch `io.yaver.mobile.watch`.
4. Capture the watch's own screen and assert a named, non-placeholder state.
5. Exercise task/render controls through XCUITest when the target exists.
6. Shut down the simulator.
7. Build/export the watch app and later verify it exists inside the iOS
   archive at `Yaver.app/Watch/Yaver.app`.

At the start of this run, `watch/project.yml` has no UI-test target. A launch
screenshot alone is a smoke test, not the requested closed loop. The release
gate must stay explicit about that gap or add the native test target.

### 3. CarPlay and iOS mobile

Authoritative clients: the CarPlay scene delegate and the RN iOS app inside
the same `Yaver` workspace/artifact.

Native gate:

1. Run the CarPlay manifest/delegate/entitlement preflight.
2. Build the shared workspace for iOS Simulator without forcing every embedded
   target to the iOS SDK.
3. Boot one iPhone simulator, install, and launch the real app.
4. Enable/open the simulator's CarPlay scene and inspect its CPTemplate UI.
5. Validate the mobile task/render loop in the iPhone simulator.
6. Shut down the simulator.
7. Archive/upload iOS once. That single TestFlight artifact carries mobile,
   CarPlay, and the embedded watch app; do not upload duplicate iOS builds.

CarPlay upload remains blocked if Apple's managed entitlement is absent from
both the App ID/profile and the signed archive, even when simulator build works.

### 4. visionOS / AR-VR

Authoritative client: `visionos/YaverVision`.

Native gate:

1. Require an installed visionOS runtime; never replace it with a Chromium
   viewport.
2. Generate the project from `visionos/project.yml`.
3. Build the app and `YaverVisionUITests` for Apple Vision Pro Simulator.
4. Install/launch against the owned Ubuntu agent.
5. Assert dashboard, runner, prompt, and response from the headset simulator's
   own screenshots/text oracle.
6. Shut down the simulator.
7. Archive/upload the native visionOS artifact only after the native arc.

`e2e/visionos-sim-loop.mjs` is the purpose-built real-client arc. Until the
runtime is installed, visionOS is **blocked**, not green. Compatible iPad-on-
visionOS analysis is useful distribution evidence but does not test the native
visionOS client.

### 5. macOS

Build/archive/export/upload the macOS app only after all simulator consumers
are stopped and the repository is clean on `main`. TestFlight upload validates
packaging; launch/runtime smoke validation must still use the built macOS app.

## Release sequence

After code gates and native simulator gates:

1. Commit every intended tracked change on `main` and scan the diff for private
   material.
2. Push `main`; verify the remote ref.
3. `./deploy/deploy.sh tvos`
4. `./deploy/deploy.sh watchos` (build/export proof; delivery is through iOS)
5. CarPlay preflight/build, then `./deploy/deploy.sh ios` once
6. native visionOS upload only after its runtime/simulator gate
7. `./deploy/deploy.sh desktop-testflight`
8. `./deploy/deploy.sh npm`
9. `./deploy/deploy.sh mcp`

Inspect `git status` after every deploy because Apple scripts may update build
metadata. Commit and push intentional metadata before the next deploy that
requires a clean tree.

## Stop conditions

Do not call the stack deployed when any of these is true:

- an Apple simulator arc skipped or ran zero tests;
- a surface was represented only by Chromium;
- a native test checked server output instead of client pixels/state;
- a simulator/runtime is missing;
- the CarPlay entitlement is absent from the signed artifact;
- the watch app is absent from the iOS archive;
- App Store Connect accepted an upload but processing later failed;
- npm/MCP publication versions do not resolve from their public registries.
