# Android Tablet Reload UI Audit

Date: 2026-05-11
Scope: `mobile` app reload UX across iPhone/iPad vs Android tablet, with special attention to the Tasks tab serving banner, Hot Reload tab, and native guest-open path.

## Executive Summary

The Android tablet experience is not merely "styled worse" than iPhone/iPad. There are two distinct problems:

1. The shared React Native serving controls were tuned for phone and looked poor on tablet.
2. The deeper iPad experience depends on a native framed-host implementation that Android does not appear to have in the main mobile app.

Those are different classes of problem:

- The Tasks / DevPreview banner issues are a shared JS layout problem and are fixable in React Native.
- The "same sense" as iPad when opening a guest app is a native-platform gap, not just a layout bug.

I updated the shared `DevPreview` banner to improve the serving state presentation on tablet. That does not close the iPad-vs-Android native parity gap.

## What I Changed

I updated `mobile/src/components/DevPreview.tsx` to:

- show the served project name as the primary label instead of generic "`framework` dev server"
- show serving state as an explicit pill (`SERVING` / `BUILDING`)
- show framework, port, and target on one clear metadata line
- show the full `workDir` as a low-emphasis mono line
- use a tablet-aware action layout so `Open in Yaver` and `Stop Serving` no longer stretch awkwardly

Relevant code:

- `mobile/src/components/DevPreview.tsx:52`
- `mobile/src/components/DevPreview.tsx:425`
- `mobile/src/components/DevPreview.tsx:740`

## Verified Findings

### 1. iPad has a native framed-host path; Android tablet does not in the main app

Verified in iOS:

- Guest bundle load ends with `YaverFramedHost.applyIfNeeded(window:)` in `mobile/ios/Yaver/AppDelegate.swift:295`.
- `YaverFramedHost` is a real native container that wraps the guest into a phone-sized surface and leaves room for a vibe dock in `mobile/ios/Yaver/AppDelegate.swift:1070`.
- The layout policy is explicit:
  - landscape: phone left, vibe dock right
  - portrait: phone top, vibe dock below

Verified in JS:

- The Hot Reload tab prompts tablet users to choose `Tablet view` vs `Phone view` in `mobile/app/(tabs)/hotreload.tsx:156`.
- That prompt always writes via `setPhoneFrame(...)` in `mobile/app/(tabs)/hotreload.tsx:161`.

Verified in the shared bundle-loader bridge:

- `setPhoneFrame()` is documented as iOS-only in `mobile/src/lib/bundleLoader.ts:132`.
- On Android it silently falls back to `{ enabled: false }` in `mobile/src/lib/bundleLoader.ts:143`.

Verified in Android app wiring:

- The mobile Android app registers `YaverInfoPackage()` in `mobile/android/app/src/main/java/io/yaver/mobile/MainApplication.kt:24`.
- There is no corresponding `YaverBundleLoader` package registered in the main app code under `mobile/android/app/src/main/java`.

Conclusion:

- The iPad "phone view plus vibe dock" experience is backed by native iOS code.
- The Android tablet path currently receives the same JS prompt but does not have the same verified native framing implementation in the main mobile app.

### 2. The Tasks tab serving controls come from `DevPreview`, not from a Tasks-specific implementation

Verified:

- Tasks renders the shared preview banner near the top of the screen in `mobile/app/(tabs)/tasks.tsx:3408`.
- Tasks also renders the same shared preview inside task detail in `mobile/app/(tabs)/tasks.tsx:4815`.

Implication:

- The "green dot", large `Open in Yaver` button, and poor `Stop Serving` alignment on tablet were not unique to Tasks.
- They were coming from one shared component used in two places.

### 3. The old shared serving banner was structurally weak on tablet

Before the patch, `DevPreview` rendered:

- a generic title: ``${status.framework} dev server`` in `mobile/src/components/DevPreview.tsx:432`
- only the basename of `workDir` in `mobile/src/components/DevPreview.tsx:435`
- target info as a separate low-emphasis line in `mobile/src/components/DevPreview.tsx:440`
- a horizontal action row with a flexed primary CTA in `mobile/src/components/DevPreview.tsx:455`

Style choices that made tablet look bad:

- `bannerRight` was always a simple horizontal row in `mobile/src/components/DevPreview.tsx:717`
- `bannerPrimaryBtn` had `flex: 1` in `mobile/src/components/DevPreview.tsx:723`
- `bannerStopBtn` did not match that sizing model in the original implementation

Result:

- the green primary action visually dominated the whole banner
- the stop action looked secondary in a bad way, not in an intentional hierarchy way
- project identity was too weak, especially in Tasks where the user is already context-switching

### 4. Tasks shows "current project" from agent info, not from the active dev server

Verified:

- Tasks pulls project info from `quicClient.agentInfo()` in `mobile/app/(tabs)/tasks.tsx:3166`.
- The project chip shown under the banner uses that data in `mobile/app/(tabs)/tasks.tsx:3411`.
- `agentInfo()` returns both:
  - `project`
  - optional `devServer`
  in `mobile/src/lib/quic.ts:2843`

Implication:

- The project chip in Tasks is not guaranteed to mean "the project currently being served".
- It can represent the agent's current project context while serving state comes from `DevPreview` and `dev/status`.

This explains the user complaint that in Tasks it is hard to tell which project is actually serving.

### 5. Hot Reload already has better serving identity than Tasks used to have

Verified in `mobile/app/(tabs)/hotreload.tsx`:

- Running card title resolves back to the real project name when possible in `mobile/app/(tabs)/hotreload.tsx:737`.
- Running metadata includes framework, port, and hot-reload state in `mobile/app/(tabs)/hotreload.tsx:842`.
- It also shows target device and operation state in `mobile/app/(tabs)/hotreload.tsx:848` and `mobile/app/(tabs)/hotreload.tsx:861`.

Implication:

- The Hot Reload tab already carried more state and more intentional hierarchy.
- Tasks felt worse partly because its serving surface was the simpler `DevPreview` banner.

## Strong Inference

The following is strongly suggested by the code, but I did not verify it by running an Android tablet build in this turn.

### Android bundle-loader integration may be split or incomplete

Verified:

- `mobile/src/lib/bundleLoader.ts` expects a native `YaverBundleLoader` module.
- The main mobile Android app does not visibly register such a package under `mobile/android/app/src/main/java`.
- There is an Android `YaverHotReloadModule` and `YaverHotReloadPackage`, but they live under the SDK package:
  - `sdk/feedback/react-native/android/src/main/java/io/yaver/feedback/YaverHotReloadModule.java:37`
  - `sdk/feedback/react-native/android/src/main/java/io/yaver/feedback/YaverHotReloadPackage.java:18`
- I found no references to `YaverHotReload` from the mobile app's Android sources or its package manifest in this repo.

Inference:

- Unless another build-time integration path wires that SDK package into the mobile app outside the lines inspected here, the Android app's native guest-open path is at best different from iOS and at worst partially disconnected from the `bundleLoader.ts` surface the JS expects.

This matters because "tablet view feel" is not only about banner styling. If the native guest-open architecture differs, the overall experience will keep feeling less coherent.

## Root Causes

### Root Cause A: product model mismatch

The Hot Reload tablet chooser presents a unified concept:

- `Phone view`
- `Tablet view`

But the underlying implementation is not unified:

- iOS has native framing support.
- Android in the main app does not show the same verified native support.

That creates a UX promise the platform implementation does not equally honor.

### Root Cause B: serving identity is split across two data sources

Tasks currently mixes:

- serving status from `DevPreview` / `dev/status`
- "current project" from `agentInfo()`

Those are related but not identical concepts.

So the user can see:

- a green serving banner
- a separate project chip

without being certain those refer to the same project.

### Root Cause C: shared control layout was phone-first

The old `DevPreview` banner used a simple stacked phone pattern with a wide filled CTA. On tablet, that reads as oversized and unbalanced rather than primary-and-secondary.

## Recommendations

### Immediate

1. Keep the `DevPreview` banner changes.
2. In Tasks, explicitly label the active served project from `dev/status.workDir`, not only from `agentInfo().project`.
3. If both are shown, distinguish them:
   - `serving`
   - `workspace`

### Next

1. Remove the platform-agnostic tablet chooser copy unless Android can actually support the same conceptual modes.
2. Gate the `Phone view / Tablet view` chooser by platform capability, not only by `layout.isTablet`.

Practical rule:

- iPad: show both options
- Android tablet: either
  - hide the chooser entirely, or
  - show Android-specific copy that does not imply iPad-style framing exists

### Strategic

Implement Android-native parity for the framed guest host if parity is truly a product requirement.

That would mean Android needs equivalents for:

- phone-framed guest container
- reserved side/bottom "vibe dock" space
- persisted frame mode preference
- clean restore back to the full Yaver shell

Without that, Android tablet can be improved, but it will still not feel like the iPad experience because the architecture is different.

## Suggested Follow-Up Work

1. Add `servedProjectLabel` and `servedWorkDir` directly to the `DevServerStatus` contract if the basename/path logic should be server-authoritative.
2. Add a tiny "Serving `<project>`" chip to the Tasks project bar so it is impossible to confuse workspace context with served app context.
3. Run an actual Android tablet smoke test for:
   - `Open in Yaver`
   - reload
   - stop serving
   - tablet chooser behavior
4. If Android native bundle-loading is supposed to exist, document exactly which native module is authoritative:
   - `YaverBundleLoader`
   - `YaverHotReload`
   - or another bridge

## Validation Notes

I ran `cd mobile && npx tsc --noEmit` after the `DevPreview` edit.

The repo still has unrelated existing TypeScript failures in:

- `app/phone-project/code/[slug].tsx`
- `src/lib/phoneSandboxFsExpo.ts`

I did not change those files in this turn.
