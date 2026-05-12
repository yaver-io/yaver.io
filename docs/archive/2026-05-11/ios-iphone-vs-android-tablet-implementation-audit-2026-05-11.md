# iOS iPhone vs Android Tablet Implementation Audit

Date: 2026-05-11

Scope:
- Compare the actual code paths behind:
  - connecting to a remote box from `Devices`, `Tasks`, and the shared picker
  - Hermes reload / "Open in Yaver" / guest bundle loading
- Focus on why iPhone feels close to correct while Android tablet still shows breakage
- Separate shared JS logic from true native platform divergence

## Executive Summary

There are two different classes of differences in the codebase.

1. Remote-box connection is mostly a shared JavaScript implementation.
The `Devices`, `Tasks`, and `Hot Reload` surfaces are all routed through the same `DeviceContext`, `connectionManager`, and `QuicClient` code. That means "can't connect from Devices on Android tablet" is not explained by a separate Android-tablet connect flow in the UI layer. If Android tablet is failing while iPhone works, the likely fault is:
- Android-specific transport / network behavior below the shared UI
- lifecycle differences that leave the focused client or connection state stale
- a shared state bug that happens to reproduce more often on Android tablet

2. Hermes reload is not symmetric.
iPhone has a much more mature native guest-host path:
- in-place bridge reload
- phone-frame host on tablet (`YaverFramedHost`)
- more complete native guest-shell integration

Android currently uses a simpler strategy:
- download bundle
- save to disk
- broadcast reload
- `MainActivity.recreate()`
- React host boots again from saved `main.jsbundle`

That is a materially different implementation, not just a styling difference. The Android path is more brittle and less polished by construction.

## A. Shared Connection Stack

These files define the real connection behavior for both iPhone and Android tablet:

- `mobile/src/context/DeviceContext.tsx`
- `mobile/src/lib/connectionManager.ts`
- `mobile/src/lib/quic.ts`
- `mobile/src/lib/connectionState.ts`
- `mobile/src/components/RemoteBoxBanner.tsx`
- `mobile/src/components/RemoteBoxPickerModal.tsx`

### What is shared

`DeviceContext.selectDevice()` is the main connect path for both platforms.

Behavior:
- marks the selected device as focused in `connectionManager`
- updates React state (`activeDevice`, `connectionStatus`)
- either reuses an already-connected pooled client or calls `connectionManager.ensureConnected()`
- races connect against a 20s timeout
- on failure, disconnects only that device's pooled client and marks the device unreachable

`RemoteBoxPickerModal` does not implement its own platform-specific connect logic. It always routes through `selectDevice()`. That is true for `Tasks` and `Hot Reload`.

`RemoteBoxBanner` is also shared. It derives status from:
- focused-device `connectionStatus`
- pooled `connectedDeviceIds`

`connectionState.ts` exists specifically to stop `Devices`, `Tasks`, and `Reload` from disagreeing about whether the app is connected.

### Why this matters

If Android tablet cannot connect from `Devices`, the problem is not "Android tablet uses a different picker" or "Tasks uses a different transport path". The UI entry points converge into the same shared connection stack.

## B. Where Connection Behavior Can Diverge By Platform

Even though the connect flow is mostly shared JS, there are still a few platform-sensitive layers.

### 1. Android custom network stack

`mobile/android/app/src/main/java/io/yaver/mobile/YaverOkHttpFactory.kt`

Android overrides React Native's networking stack with:
- IPv4-first DNS ordering
- 6s connect timeout

This exists because Android devices on bad dual-stack Wi‑Fi were hitting broken IPv6 first and timing out before JS abort budgets expired.

This is an Android-only mitigation. iPhone does not go through this file.

Implication:
- Android connection failures can absolutely come from Android-specific transport behavior
- but they are below the `Devices` / `Tasks` UI layer

### 2. Active/focused client split-brain risk

`DeviceContext.tsx` contains several recovery effects to keep:
- `activeDevice`
- `connectionStatus`
- `connectedDeviceIds`
- `connectionManager.focusedId`

from drifting apart.

The code comments document prior symptoms such as:
- Devices showing connected pool state
- another tab still thinking nothing is connected
- user picks device A and gets bounced back to device B

This logic is shared, but lifecycle timing differences can make it reproduce more on Android tablet.

### 3. Auto-pair / auth-recovery complexity

`DeviceContext.tsx` also owns:
- encrypted auto-pair
- relay auto-pair
- bootstrap recovery
- manual-auth blocking
- retry gating

Again, this is shared logic, not a separate tablet path. If Android tablet fails more often here, the likely causes are:
- network timing
- app lifecycle timing
- stale device status
- relay/direct path differences

not a distinct `Devices` screen implementation.

## C. Devices / Tasks / Hot Reload UI Routing Comparison

### Devices

`mobile/app/(tabs)/devices.tsx`

This screen:
- reads `activeDevice`, `connectionStatus`, and `connectedDeviceIds` from `useDevice()`
- uses `selectDevice(item)` on tap
- shows pooled-connected state as connected even if the focused client is not the same one

This is already written with the pooled multi-device model in mind.

### Tasks

`mobile/app/(tabs)/tasks.tsx`

This screen:
- also reads from `useDevice()`
- computes effective connectivity from focused state plus pool state
- embeds `RemoteBoxBanner`
- can call `selectDevice()` during reconnect / recovery flows
- shows `DevPreview` when effectively connected

This means Tasks is not using a second-class Android-specific connect path either.

### Hot Reload

`mobile/app/(tabs)/hotreload.tsx`

This screen:
- uses `RemoteBoxBanner`
- uses `RemoteBoxPickerModal`
- gates content on `isEffectivelyConnected(connectionStatus, connectedDeviceIds)`
- depends on `activeDevice` for project scan, dev server status, capability snapshot, and preview targeting

So the three main user-facing surfaces are already converged architecturally on the same connection model.

## D. Hermes Reload: iPhone vs Android Is Genuinely Different

This is where the codebase diverges most sharply.

### Shared JS contract

`mobile/src/lib/bundleLoader.ts`

JS expects the same native surface on both platforms:
- `loadBundle`
- `unloadBundle`
- `getAvailableModules`
- `isLoaded`
- `getLoadedBundleMd5`
- `setPhoneFrame`
- `getPhoneFrame`

But the native implementations are not equivalent.

### iPhone native implementation

Main files:
- `mobile/ios/Yaver/YaverBundleLoader.swift`
- `mobile/ios/Yaver/AppDelegate.swift`

Important characteristics:

1. Full validation and persistence flow
- download bundle
- validate metadata
- validate Hermes bytecode
- persist module/runtime family/md5
- persist inherited agent base URL and auth

2. In-place native reload path
`YaverBundleLoader` posts a reload notification.
`AppDelegate.safeReloadBridge()`:
- finds the current `RCTRootView`
- invalidates the old bridge
- waits for bridge deallocation
- creates a fresh guest bridge
- restores the guest shell cleanly

This is much closer to a first-class guest runtime than Android's recreate approach.

3. iPad-only framed host
`AppDelegate.swift` contains `YaverFramedHost`.
This is the native "phone in a tablet shell" path:
- guest can be wrapped in phone chrome
- vibe dock can sit beside or below it
- the host is actively removed and re-applied during reload

This is a real iOS-only experience layer.

### Android native implementation

Main files:
- `mobile/android/app/src/main/java/io/yaver/mobile/YaverBundleLoaderModule.kt`
- `mobile/android/app/src/main/java/io/yaver/mobile/MainActivity.kt`
- `mobile/android/app/src/main/java/io/yaver/mobile/MainApplication.kt`

Important characteristics:

1. Bundle save + recreate strategy
Android downloads, validates, and saves the bundle similarly, but after success it:
- broadcasts `io.yaver.mobile.BUNDLE_RELOAD`
- `MainActivity` receives it
- calls `recreate()`
- `MainApplication.getJSBundleFile()` points React Native to the saved guest bundle on next boot

This is explicitly documented in the code as Strategy A / MVP.

2. No Android framed-host equivalent
`YaverBundleLoaderModule.setPhoneFrame()` and `getPhoneFrame()` are stubs that always resolve false / disabled.

So JS can ask for "Phone view", but Android has no equivalent of iOS `YaverFramedHost`.

3. Guest shell is activity-level, not bridge-level
That means Android guest reload is more like "reboot the surface into the saved bundle" than "swap the guest runtime in place".

Expected consequences:
- more visible flash / disruption
- more chances for state mismatch after reload
- more brittle behavior around rapid successive reloads or stop/open cycles

## E. Hot Reload Screen Differences That Matter

### View-mode picker is conceptually cross-platform, functionally iOS-first

`mobile/app/(tabs)/hotreload.tsx`

The screen shows an "Open as…" prompt:
- `Phone view`
- `Tablet view`

It always calls `setPhoneFrame(mode === "phone")`.

On iPhone/iPad:
- this maps to real native state

On Android:
- the bundle-loader module exposes the method
- but it is stubbed
- the promise resolves, but no native framing behavior exists

So this part of the product is visually cross-platform but functionally iOS-first.

### Hermes build/reload control is shared, mount behavior is not

`hotreload.tsx` and `DevPreview.tsx` both call:
- agent-side Hermes bundle build
- `loadAppIfChanged(...)`
- native reload via `bundleLoader`

The JS orchestration is mostly shared.
The actual mount/reload behavior is controlled by the native module underneath, which differs sharply across platforms.

## F. Areas Where iPhone Is Ahead Today

### 1. Guest presentation

iPhone / iPad path:
- in-place host control
- optional phone frame on tablet
- explicit native host lifecycle management

Android tablet path:
- no frame host
- no true phone-view mode
- activity recreation only

### 2. Reload smoothness

iPhone:
- bridge invalidation + re-init

Android:
- save bundle + activity recreate

The Android path is inherently coarser.

### 3. Product-level polish around Hermes guesting

iOS native code contains more guest-specific product behavior:
- framed host
- more mature bridge lifecycle handling
- guest-focused reload/restore control

Android has the core loader and validator, but not the equivalent shell sophistication.

## G. Areas That Are Not Actually iPhone-vs-Android Differences

These are important because they can mislead debugging.

### 1. `Devices` connect UI

This is not implemented separately for iPhone and Android tablet.
The tap path is shared through `selectDevice()`.

### 2. `Tasks` remote-box selection

Also shared through `RemoteBoxPickerModal` and `DeviceContext`.

### 3. Effective connected-state derivation

Shared via `connectionState.ts`.

So if Android tablet shows "can't connect from Devices" while iPhone works, it is likely:
- lower-level transport behavior
- lifecycle/state timing
- stale pool/focus state
- auth/bootstrap edge case

not a completely separate screen implementation.

## H. Most Likely Fault Boundaries For The Reported Bugs

### Bug family 1: "Can't connect from Devices on Android tablet"

Most likely boundaries:

1. Android transport/network behavior
- `YaverOkHttpFactory.kt`
- any Android-specific DNS / timeout / dual-stack behavior

2. Shared state split between:
- `activeDevice`
- `connectionStatus`
- `connectedDeviceIds`
- `connectionManager.focusedId`

3. Auto-pair / auth-recovery path
- `DeviceContext.tsx`
- especially bootstrap / `needsAuth` / relay recovery cases

Less likely:
- `Devices.tsx` itself

### Bug family 2: "Hermes reload for Todo RN is not working on Android tablet"

Most likely boundaries:

1. Android native guest reload strategy
- `YaverBundleLoaderModule.kt`
- `MainActivity.kt`
- `MainApplication.kt`

2. Android bundle validation / persistence path
- `YaverBundleValidator.kt`
- `YaverSDKManifest.kt`
- `YaverInfoModule.kt`

3. Missing Android frame/host parity
- no `YaverFramedHost` equivalent
- `setPhoneFrame` stubbed

Less likely:
- the high-level JS build orchestration alone

## I. Practical Comparison Table

| Area | iOS iPhone / iPad | Android tablet |
|---|---|---|
| Remote connect entrypoint | Shared `DeviceContext.selectDevice()` | Shared `DeviceContext.selectDevice()` |
| Pool / focus model | Shared JS | Shared JS |
| Effective connected-state logic | Shared JS | Shared JS |
| Network stack specialization | iOS default RN/native stack | Android custom OkHttp IPv4-first factory |
| Hermes load API surface | Full native module | Matching module surface |
| Hermes reload implementation | Bridge invalidation + guest bridge re-init | Save bundle + broadcast + `Activity.recreate()` |
| Phone-view-on-tablet | Real native framed host | Stubbed `setPhoneFrame()` |
| Tablet guest chrome | `YaverFramedHost` | none |
| Visual polish baseline | mature guest host path | JS UI improving, native host still simpler |

## J. Bottom-Line Assessment

The codebase does not currently have "iPhone implementation" and "Android tablet implementation" for connection in the same way it does for Hermes reload.

Connection:
- mostly shared
- Android tablet issues likely come from platform transport/lifecycle differences or shared state bugs

Hermes reload:
- genuinely different native implementations
- iOS path is substantially more advanced
- Android path is still MVP-level compared with iOS

So the right mental model is:

- `Devices` / `Tasks` connection bugs on Android tablet are probably not fixable by only redesigning those screens
- Hermes reload parity on Android requires native work, not just JS/UI tuning
- tablet UI polish is worth doing, but it will not close the deepest implementation gap by itself

## K. Suggested Next Debug Order

1. Reproduce Android-tablet device-connect failure with logs around `DeviceContext.selectDevice()` and `QuicClient.connect()`.
2. Verify whether failure happens before transport establishment, after transport establishment, or after focus/state promotion.
3. Reproduce Android Hermes reload on a known-good Expo/RN app and log:
- build result
- metadata header
- bundle validation result
- `BUNDLE_RELOAD` broadcast
- `MainActivity.recreate()`
- `getJSBundleFile()` returning guest bundle path
4. Treat Android framed-host / phone-view parity as a separate native feature, not part of the connection bug.
