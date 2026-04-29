# Native Module Architecture

> Reference for contributors who need to add a new native module to
> Yaver's super-host, debug a "module not registered" crash, or
> understand why iOS forces certain design choices.
>
> Code is the source of truth. Re-grep before acting on anything below.
> Companion docs: [`HERMES_RELOAD_STATUS.md`](../HERMES_RELOAD_STATUS.md),
> [`docs/android-dynamic-native-modules.md`](android-dynamic-native-modules.md).

## Why this matters

Yaver's mobile app is a **native container** for third-party React
Native apps. When you tap "Open in Yaver," your guest bundle's
JavaScript runs inside Yaver's bridge, calling Yaver's pre-registered
TurboModules and Fabric components. The bundle is interpreted code
(Hermes bytecode); the native side is whatever is signed into the Yaver
binary on disk.

The contract: a guest bundle can call any native module Yaver registers,
and only those. If the guest declares a native dep that Yaver doesn't
have, calling it at runtime throws `NSException` and crashes Hermes
during the JSError-conversion path. The only fix is adding the missing
module to Yaver's super-host, which means a new App Store / Play
Console release.

This doc covers:

1. The five tracked manifest copies.
2. The agent-side compat handshake.
3. The PR contract for adding a module.
4. Common failure modes.

## The five manifest copies

Yaver's "what native modules are registered" contract lives in a single
JSON file, mirrored to five locations. The mobile master is canonical;
all others must match.

| Path | Role |
|---|---|
| [`mobile/sdk-manifest.json`](../mobile/sdk-manifest.json) | **Canonical master.** Every change starts here. |
| [`mobile/android/app/src/main/assets/sdk-manifest.json`](../mobile/android/app/src/main/assets/sdk-manifest.json) | Bundled into Android APK. Read by `YaverInfoModule.kt` to expose `isYaver` + manifest to guest bundles. |
| [`mobile/ios/Yaver/sdk-manifest.json`](../mobile/ios/Yaver/sdk-manifest.json) | Force-tracked into the iOS Yaver target (Xcode's Copy Bundle Resources phase). Read by `SDKManifest.shared` in Swift. |
| [`cli/sdk-manifest.json`](../cli/sdk-manifest.json) | Shipped with the `yaver-cli` npm package. The push-to-device CLI uses it to validate compatibility before bundling. |
| [`desktop/agent/sdk-manifest.json`](../desktop/agent/sdk-manifest.json) | Embedded into the Go agent via `//go:embed`. Read by `BuildNativeModuleCompatReport` to do the build-time handshake check. |

The `TestSDKManifestInSync` Go test in `desktop/agent/native_modules_compat_test.go`
fails the build if the agent's copy drifts from the mobile master. There
is no equivalent test for the other three copies yet — they're checked
manually. Future TODO: extend the sync test to cover all five.

## How the handshake works (cli/v1.99.94+)

```
Developer machine                                       iPhone
┌──────────────────────────────────────┐               ┌──────────────────────┐
│ /dev/build-native called             │               │  POST /dev/build-    │
│  ↓ Metro bundle                      │ ─── HTTP ──── │  native              │
│  ↓ hermesc → HBC                     │               └──────────────────────┘
│  ↓ ValidateHBC (BC version 96)       │                          ↓
│  ↓ BuildNativeModuleCompatReport():  │               ┌──────────────────────┐
│      • read project package.json     │               │ Reads response:      │
│      • diff vs embedded manifest     │ ── response ─→│ incompatibleNative-  │
│      • return matched + missing      │               │ Modules: [...]       │
│                                      │               └──────────────────────┘
│  Response includes:                  │                          ↓
│   incompatibleNativeModules: [...]   │               ┌──────────────────────┐
│   matchedNativeModules: [...]        │               │ If non-empty:        │
└──────────────────────────────────────┘               │  show "Incompatible  │
                                                       │  native modules"     │
                                                       │  dialog before       │
                                                       │  bridge swap.        │
                                                       │  Cancel / Load       │
                                                       │  anyway.             │
                                                       └──────────────────────┘
```

### The native-module heuristic

`desktop/agent/native_modules_compat.go::isLikelyNativeModule(name)` is
deliberately liberal. A package is treated as native if any of:

- `react-native-…`
- `@scope/…react-native…`
- `@react-native-…`
- `expo-…`
- `@expo/…`

Minus a `jsOnlyExact` deny-list of build/dev tools that match the
heuristic but ship no runtime native code (`react-native-web`,
`@expo/metro-runtime`, `expo` umbrella, `react-native-svg-transformer`,
etc.).

False positives are acceptable. False negatives are not — a missed
native module silently crashes. If you find one, add it to the
heuristic.

### Match logic

After extraction, each candidate is checked against the host manifest's
`nativeModules` keys. Three buckets:

- **Matched** (in manifest): registered in Yaver, will work.
- **Incompatible** (not in manifest, passed heuristic): suspect — flag.
- **Ignored** (didn't pass heuristic): assumed pure-JS, no check.

The mobile dialog only surfaces the **Incompatible** bucket.

## Adding a native module — full PR contract

Follow this exactly. Each step matters.

### 1. Add to `mobile/package.json`

```bash
cd mobile
npm install --legacy-peer-deps <package-name>
```

`--legacy-peer-deps` is required (not optional) — Yaver has chronic peer
dep conflicts because of the wide RN/Expo surface area. See CLAUDE.md.

### 2. Update all five manifest copies

```bash
# Add the entry to mobile/sdk-manifest.json under nativeModules:
#   "<package-name>": "<installed-version>"
# Then mirror it to the other four:
cp mobile/sdk-manifest.json mobile/android/app/src/main/assets/sdk-manifest.json
cp mobile/sdk-manifest.json mobile/ios/Yaver/sdk-manifest.json
cp mobile/sdk-manifest.json cli/sdk-manifest.json
cp mobile/sdk-manifest.json desktop/agent/sdk-manifest.json
```

Then `cd desktop/agent && go test -run TestSDKManifestInSync` must pass.

### 3. iOS — pod install + verify autolinking

```bash
cd mobile/ios && pod install
```

This is slow (~5-30 minutes the first time, ~1-2 minutes incremental).
For most modules, autolinking handles registration via the Pods build
phase. Check `mobile/ios/Pods/Pods.xcodeproj/` after `pod install` to
confirm the module's pod is present. If autolinking didn't pick it up,
the module's `podspec` may need an explicit `pod` entry in `Podfile`.

For modules with custom native code (rare — Apple-platform-specific
APIs not exposed by autolinking), follow the module's iOS install guide
and ensure the bridging header at
[`mobile/ios/Yaver/Yaver-Bridging-Header.h`](../mobile/ios/Yaver/Yaver-Bridging-Header.h)
imports anything required.

### 4. Android — gradle clean + verify autolinking

```bash
cd mobile/android && ./gradlew clean
```

Then a regular Yaver build runs `./gradlew bundleRelease` and the
autolinker registers the module. Verify in
`mobile/android/app/build/generated/autolinking/...` after build.

### 5. Smoke test inside Yaver

The single most important step. Build a small RN app that uses the
module, push it to Yaver via "Open in Yaver," and verify
`NativeModules.<ModuleName>` resolves and at least one method returns
without throwing.

Example for `react-native-record-screen`:

```js
import RecordScreen from "react-native-record-screen";

console.log("RecordScreen module:", RecordScreen);
const { status } = await RecordScreen.startRecording();
console.log("Recording status:", status);
```

If this throws `NSException` or "native module is null," the manifest
is lying about Yaver's capability — back the change out.

### 6. Open the PR

Include in the description:

- Manifest diff (which key was added, with version).
- Smoke-test scenario (the JS snippet you ran and what it returned).
- Hermes BC version you tested against (currently 96 — RN 0.81.5).
- Any iOS/Android quirks discovered (e.g. needs Info.plist permission,
  needs a specific Android permission in `AndroidManifest.xml`).

The reviewer will:

- Pull, run `pod install` + `bundleRelease` locally.
- Verify the smoke-test scenario.
- Run `go test -run TestSDKManifestInSync` to verify manifest sync.
- Sanity-check `mobile/sdk-manifest.json` is the only authoritative
  edit and other four are byte-identical copies.

## Common failure modes

### "Native module 'X' is null" thrown immediately after bundle load

The guest bundle imports the module at top level and a side effect calls
`TurboModuleRegistry.getEnforcing('X')`. The module isn't registered.

**Fix**: Add the module via the PR contract, OR have the guest app gate
the call:

```js
const X = NativeModules.X || null;
if (X) X.someMethod();
```

### `EXC_BAD_ACCESS` in `hermes::vm::HiddenClass::isDictionary()` after `convertNSExceptionToJSError`

A registered module's selector threw `NSException`. RN tried to convert
it to a JSError via `TurboModuleConvertUtils::convertNSExceptionToJSError`,
which crashed Hermes. This is a known Hermes bug class triggered by
async TurboModule throws.

**Diagnosis**: Run the same app outside Yaver (build with Xcode and
deploy directly to device). If it still crashes, the bug is in the
module, not Yaver. If it doesn't crash, Yaver's super-host bridge is
mishandling the exception path — file an issue with the crash log.

**Mitigation**: Either guard the calling JS with try/catch, or wait for
Yaver's defensive TurboModule wrapper (Tier 2 of the roadmap).

### "BC_VERSION_MISMATCH" rejection

Project compiled against a different Hermes than Yaver ships.

**Fix**: Make sure `hermesc` from your RN install isn't being substituted.
`yaver-cli` embeds the canonical hermesc for BC 96; if you're using
non-yaver-cli tooling, check `node_modules/react-native/sdks/hermesc/`
matches.

### Bundle downloads then silently does nothing

App is bundleless — the user's bundle is plain JS, not Hermes bytecode.

**Diagnosis**: `xxd /tmp/<bundle>.jsbundle | head -1` — first 12 bytes
should show the HBC magic `0x1F1903C1` at offset 4. If they show
`#!/usr/bin/env node` or actual JS source, hermesc never ran. Check
the build pipeline.

### Compat report has a false negative (missing module didn't show up)

The native-module heuristic in `isLikelyNativeModule` failed to match.
Add the package to the heuristic by name OR refine the regex.

PRs to broaden the heuristic are welcome — false positives (extra
warnings) are much better than false negatives (silent crashes).

## File map

| File | What it owns |
|---|---|
| [`desktop/agent/sdk-manifest.json`](../desktop/agent/sdk-manifest.json) | Embedded copy of mobile master. |
| [`desktop/agent/native_modules_compat.go`](../desktop/agent/native_modules_compat.go) | Heuristic + report builder. |
| [`desktop/agent/native_modules_compat_test.go`](../desktop/agent/native_modules_compat_test.go) | Sync test + heuristic tests + SFMG case. |
| [`desktop/agent/devserver_http.go`](../desktop/agent/devserver_http.go) (`handleBuildNative`) | Calls `BuildNativeModuleCompatReport`, returns result in JSON. |
| [`mobile/app/(tabs)/apps.tsx`](../mobile/app/(tabs)/apps.tsx) (`buildHermesBundle`) | Reads `incompatibleNativeModules`, shows native dialog before bridge swap. |
| [`mobile/sdk-manifest.json`](../mobile/sdk-manifest.json) | Master manifest. |
| [`mobile/ios/Yaver/SDKManifest.shared`](../mobile/ios/Yaver/YaverBundleValidator.swift) | Reads bundled iOS copy at runtime. |
| [`mobile/android/.../YaverInfoModule.kt`](../mobile/android/) | Exposes `isYaver` + manifest to guest bundles. |
