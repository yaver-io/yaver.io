# Audit Handoff: Second-Class Flutter / Swift / Kotlin Mobile Routing

## Scope

This change set was meant to add second-class mobile delivery flows for Flutter, Swift, and Kotlin while keeping Hermes as the only first-class mobile runtime path.

## Non-Negotiable Rule

Never ever break the existing Hermes reload / bundle-push / containerized runtime methodology used by the Yaver mobile app.

Hermes must remain:
- first-class
- React Native / Expo only
- loadable inside the Yaver mobile container
- valid over LAN, relay, and 4G
- untouched in semantics, copy, and execution path unless a bugfix is strictly isolated to Hermes itself

Do not regress:
- `Open in Yaver`
- `/dev/build-native`
- Hermes bundle compilation / validation
- Hermes runtime reload in the Yaver mobile container
- existing React Native / Expo compatibility flow
- existing containerization assumptions for Hermes

## What Was Implemented

### 1. Mobile UI branching for second-class frameworks

File:
- `mobile/app/(tabs)/apps.tsx`

Added:
- framework split between:
  - first-class Hermes: `expo`, `react-native`
  - second-class mobile: `flutter`, `swift`, `kotlin`
- second-class action labels:
  - `Flush to App (LAN)` for Flutter
  - `Flush Build to Phone (LAN)` for Swift/Kotlin
- second-class UI copy explaining:
  - these do not load inside Yaver
  - they are LAN-only
  - Hermes remains the only first-class remote path
- Flutter flush path:
  - starts/reuses Go-agent Flutter dev server
  - reloads through Yaver, not direct user CLI
- Swift flush path:
  - uses Go-agent `xcode-device-install`
- Kotlin flush path:
  - uses Go-agent `gradle-apk`
  - downloads APK to phone and starts install

Important:
- Hermes UI flow was kept in place.
- `Open in Yaver` is still Hermes-only.
- This file needs audit for coupling and any accidental regressions in `Apps` screen behavior.

### 2. Go-agent framework-aware Flutter targeting

File:
- `desktop/agent/devserver.go`

Implemented:
- `DevServerOpts` now carries `Target DevServerTarget`
- `DevServerManager.Start(...)` now passes selected Yaver target into dev server opts
- Flutter dev server logic now:
  - treats `ios` / `android` as preferred platform classes, not concrete Flutter device IDs
  - resolves actual Flutter mobile device IDs
  - prefers the selected Yaver target phone by name when possible
  - falls back to preferred platform match, then generic mobile detection

Added helper logic:
- `normalizeDeviceName`
- `flutterDeviceMatchesTarget`
- updated `detectFlutterMobileDevice(ctx, preferredPlatform, target)`

This is the main runtime bugfix:
before, the wrapper could run `flutter run -d ios`
now it resolves an actual device like `00008110-001A515426FB801E`

This area must be audited carefully.

### 3. Vibing / runner execution context

File:
- `desktop/agent/vibing.go`

Implemented:
- quick actions now include `Push To Phone` for:
  - Expo / RN / Flutter
  - Swift / Kotlin
- added `vibingExecutionContext(...)`
- `handleVibingExecute(...)` now prepends structured Yaver mobile routing context to the prompt:
  - project framework
  - selected target phone
  - direct vs relay context
  - Hermes-first rule
  - Flutter/Swift/Kotlin second-class LAN-only rule
  - explicit instruction not to suggest manual CLI flags like `--platform ios`

This is intended to keep Claude Code / Codex aligned with Yaver routing semantics.

Audit for:
- prompt safety
- prompt duplication
- accidental behavior changes in Vibing
- whether this context should instead live deeper in task context or contract generation

### 4. Architecture plan doc

File:
- `SECOND_CLASS_FLUTTER_SWIFT_KOTLIN.md`

Contains:
- target architecture
- Vibing / runner intent model
- Hermes-first rule
- second-class LAN-only rule
- device-awareness expectations
- execution plan / follow-up notes

## Runtime Validation Already Done

### Mobile TS validation

Command:
- `mobile/node_modules/.bin/tsc --noEmit -p mobile/tsconfig.json`

Result:
- passed

### Go-agent Flutter wrapper smoke test

Project used:
- `/Users/kivanccakmak/Workspace/elevathor/e_mobile_new`

Tested with rebuilt local repo agent binary:
- built local binary from `desktop/agent`
- ran Flutter through Yaver Go-agent wrapper in standalone mode

Observed before fix:
- wrapper used `ios` as if it were a concrete device ID
- failed device selection semantics

Observed after fix:
- wrapper resolved actual wireless iPhone device ID
- logs showed:
  - found preferred mobile device
  - started on actual device ID
  - launched Flutter on wireless iPhone
  - reached iOS signing/build phase

This confirms the framework-aware device resolution fix worked in the rebuilt repo binary.

### Important limitation of runtime test

I did not complete a full end-to-end production-quality test from the actual Yaver mobile Vibing surface across multiple devices.
This still needs validation.

## Things That Need Audit

### Highest priority

1. Confirm Hermes path is completely untouched in runtime behavior.
2. Review `mobile/app/(tabs)/apps.tsx` for regressions in:
   - action sheet behavior
   - running card behavior
   - quick action behavior
   - dependencies / hooks / callback ordering
3. Review `desktop/agent/devserver.go` for:
   - unintended side effects to Expo / RN / Vite / Next
   - target propagation correctness
   - Flutter detection edge cases
4. Review `desktop/agent/vibing.go` for:
   - prompt injection quality
   - whether `handleVibingExecute` is the right insertion point
   - whether mobile routing context belongs elsewhere

### Medium priority

5. Check whether Swift/Kotlin target selection is still too weak compared to Flutter.
   Current UI routes those through build/install paths, but Go-agent target-awareness is strongest for Flutter so far.
6. Check whether second-class flows should use a dedicated agent-side `mobile_flush` endpoint instead of UI-side orchestration.
7. Check whether LAN eligibility is being inferred too simplistically in mobile UI via connection mode.

### Lower priority

8. Review copy for consistency:
   - Hermes first-class
   - second-class LAN-only wording
   - no implication that Flutter/native runs inside Yaver container

## Files Changed

- `mobile/app/(tabs)/apps.tsx`
- `desktop/agent/devserver.go`
- `desktop/agent/vibing.go`
- `SECOND_CLASS_FLUTTER_SWIFT_KOTLIN.md`

## Explicit Instruction To Auditor

If you find any bug, fix it.
But do not "clean up" or refactor Hermes behavior unless a Hermes bug is proven.

Never ever break the existing Hermes reload inside the Yaver mobile app container methodology.
Never weaken:
- Hermes bundle push
- Hermes validation
- Hermes runtime reload
- React Native / Expo first-class path
- containerized Yaver mobile app semantics for Hermes

If there is any ambiguity, preserve Hermes behavior and isolate second-class changes.
