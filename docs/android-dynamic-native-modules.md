# Android Dynamic Native Modules — Architecture Sketch

> **Status:** design only, no code shipped. This document captures the
> plan for shrinking the "module not in Yaver's super-host" wall on
> Android by leveraging a capability iOS doesn't have:
> `dlopen` of arbitrary `.so` files at runtime.

## Why Android-only

The structural limit explained in
[`docs/native-module-architecture.md`](native-module-architecture.md) and
the [Hermes vs WebView blog post](https://yaver.io/blog/hermes-vs-webview-yaver-architecture)
applies on iOS because Apple forbids loading unsigned executable code
at runtime (App Store Review Guideline 3.3.2). Hermes bytecode runs
fine — it's interpreted. Native code (a fresh `.dylib` or `.framework`
shipped over the wire) does not.

Android has no equivalent restriction. An app with the
`READ_EXTERNAL_STORAGE` (or app-private storage) capability can drop a
`.so` file on disk and call `System.load("/path/to/lib.so")` at runtime.
Google Play's policies discourage downloading executable code, but the
OS allows it, and there are well-established legitimate uses (game
engines streaming asset/code packs, dev tools, ML model hot-reload).

This means a Yaver-on-Android user could **add a missing native module
without rebuilding Yaver and shipping through Play Console**, by
streaming the module's compiled `.so` from the agent to the phone over
the existing relay path.

## Goal

When the agent runs `BuildNativeModuleCompatReport` and finds an
incompatible module on Android, instead of just warning the user it
should:

1. Build (or fetch) the module's compiled `.so` for the phone's ABI.
2. Stream the `.so` to the phone via the same relay path used for HBC bundles.
3. Yaver's super-host loads the `.so` via `System.load`, registers the
   module with the bridge, and proceeds with the bridge swap.

User experience: identical to today on iOS for matched modules — tap
"Open in Yaver," wait ~10s, app runs. The wall goes away on Android.

## Architecture

```
Developer machine                                       Android phone
┌──────────────────────────────────────┐               ┌──────────────────────┐
│ /dev/build-native called             │               │  Yaver Android       │
│  ↓ Metro bundle → HBC                │               │  super-host          │
│  ↓ ValidateHBC                       │               └──────────────────────┘
│  ↓ BuildNativeModuleCompatReport():  │                          ↓
│      missing: ["react-native-foo"]   │               for each missing module:
│  ↓ FOR each missing module:          │               ┌──────────────────────┐
│      • Resolve to gradle artifact    │               │ POST /native-modules │
│      • Cross-compile for ABI         │ ── stream ──→ │      /load-so        │
│        (arm64-v8a, armeabi-v7a)      │               │   { name, soBytes,   │
│      • Sign with Yaver's key         │               │     manifest }       │
│      • Stream .so back via relay     │               │ ↓                    │
│  ↓ Then HBC bundle as today          │               │ Verify signature     │
└──────────────────────────────────────┘               │ Save to app-private  │
                                                       │ System.load(path)    │
                                                       │ Register TurboModule │
                                                       │ via bridge.add(...)  │
                                                       └──────────────────────┘
                                                                  ↓
                                                       ┌──────────────────────┐
                                                       │ Bridge swap, run HBC │
                                                       │ bundle as today      │
                                                       └──────────────────────┘
```

## Open questions / risks

### How does the agent get the `.so`?

Three sources, in order of practicality:

1. **Cross-compile on the host.** Run `./gradlew :react-native-foo:assembleRelease`
   inside the third-party project. This produces an `.aar` containing
   `.so` files for each ABI. Slow on first run (~2-5 minutes), fast
   incremental.
2. **Fetch from Maven Central / a Yaver module CDN.** Pre-built `.so`
   files for popular modules cached centrally. Fastest but requires
   binary distribution infrastructure.
3. **Cross-compile on a CI runner.** For modules without a published
   `.aar`, build server-side and cache.

Phase 1 should be (1) — uses what's already on the developer's machine.

### How is the `.so` signed?

Without signing, anything could be loaded — same as the iOS reason for
forbidding this. Even though Android allows it at the OS level, Yaver's
trust model still demands authentication. Two approaches:

1. **Sign with Yaver's key.** The agent embeds a private key generated
   per-device-pair. The Android super-host verifies signatures before
   `System.load`. Simpler.
2. **Use Android Package Signing.** Reject any `.so` not in an `.aar`
   signed with Google Play's expected cert. Stricter but limits the
   long tail to published modules.

Phase 1 should be (1) — keeps the architecture self-contained.

### How is the TurboModule registered after `System.load`?

Autolinking happens at compile time via Gradle. To register a module
loaded at runtime, we need the dynamic bridge equivalent:

```kotlin
// hypothetical Kotlin runtime registration
val moduleClass = Class.forName("com.foo.RNFooModule")
val instance = moduleClass.getConstructor(ReactApplicationContext::class.java)
    .newInstance(reactContext)
val packageBuilder = TurboReactPackage.Builder()
packageBuilder.addModule(instance.name, instance)
reactInstanceManager.addPackage(packageBuilder.build())
reactInstanceManager.recreateReactContextInBackground()
```

This requires:

- The module's Java/Kotlin code is included in the `.aar`'s `classes.jar`.
- The constructor signature is the standard one.
- React Native's bridge supports adding packages post-init (it does, via
  `recreateReactContextInBackground`, but with a ~1s pause).

### What about Hermes BC version drift?

The bytecode bundle is compiled against a specific Hermes version. The
runtime-loaded native module's `.so` doesn't change Hermes — but if the
module ships its own Hermes-aware code (rare, mostly RN internals do),
it must match BC 96. The compat handshake should reject mismatches.

### What about ABI mismatches?

Android ships builds for `arm64-v8a`, `armeabi-v7a`, `x86_64`, `x86`.
The agent must cross-compile for all the ABIs Yaver supports, or detect
the phone's ABI and ship just the matching one. The latter is faster
but requires the phone to advertise its ABI in the relay handshake.

### What does the user see?

UX:

- Compat dialog says "3 native modules will be installed dynamically: A,
  B, C." User confirms.
- Progress bar during cross-compile + transfer (each module ~5-30s).
- Bridge swap proceeds as today.

If any module fails to cross-compile or load, the dialog reports which
one and offers the same "Cancel / Load anyway (without it)" choice.

### Trust boundary

The dynamic load happens **only on the developer's own paired phone**,
loading **only modules from projects the developer is iterating on**. The
attack surface is: an attacker controls the agent, ships a malicious
`.so`, the phone loads it. Mitigations:

- Signing key pinned to the device pair (can't be reused across users).
- Yaver's super-host runs in the same process as the dynamic module —
  the module gets the same OS permissions Yaver has. No privilege
  escalation, but it does mean a malicious module can read Yaver's
  private storage (auth tokens, etc).
- For high-paranoia setups, the dynamic loading path can be disabled via
  a config flag and users fall back to the static-manifest behavior.

## Implementation phases

### Phase 1 — proof of concept (1-2 weeks)

- Hand-pick one module (`react-native-record-screen` is a good
  candidate) and prove the full pipeline: cross-compile → stream →
  load → register → call from JS.
- No automation; the developer manually triggers the build.
- Validates the architecture works.

### Phase 2 — automated agent integration (1 week)

- Wire `BuildNativeModuleCompatReport` to detect Android targets.
- Auto-trigger gradle cross-compile when a module is missing.
- Stream the `.so` over the existing relay path.
- Surface in the build response.

### Phase 3 — production polish (2-3 weeks)

- Caching (don't recompile the same module twice).
- Pre-built CDN for popular modules.
- Signing.
- ABI auto-detection.
- Mobile UI (progress bar, retry, fallback).

### Phase 4 — Play Store review (separate concern)

Loading dynamic native code at runtime is allowed by Android but flagged
by Google Play's pre-launch review. Yaver's existing distribution as a
developer tool likely qualifies — Yaver is a dev tool aimed at
developers, not a production app loading code on end users' phones. But
this needs explicit clearance with Google Play before shipping.

If Play Store review blocks the feature, fall back to:

- **Sideload-only Android build.** Yaver ships an extra
  `.apk` outside the Play Store with the dynamic loader enabled.
  Developers install via `adb install` or direct download.
- **Yaver Cloud Android.** A managed Yaver-on-Android instance with the
  feature pre-enabled.

## What this does NOT do for iOS

iOS users get nothing from this. The structural limit on iOS is Apple's
no-runtime-native-code rule, and that's not getting waived. The iOS
path still needs:

- The agent compat handshake (shipped).
- Auto-stub at build time (Tier 2 of roadmap, planned).
- Per-project Yaver build via TestFlight (Tier 4 of roadmap, planned).

This Android path is purely additive — it doesn't replace any iOS work.

## File map (when implementation begins)

Anticipated file additions / changes:

| File | Purpose |
|---|---|
| `desktop/agent/native_modules_dyna_android.go` | Cross-compile + sign + bundle `.so` |
| `desktop/agent/devserver_http.go::handleBuildNative` | Trigger dyna path when target=Android + missing modules |
| `mobile/android/.../YaverDynaLoader.kt` | Receive `.so`, verify signature, `System.load`, register |
| `mobile/android/.../YaverHTTPServer.kt` | Add `/native-modules/load-so` endpoint |
| `mobile/sdk-manifest.json` | New optional field `dynamicLoadable: true/false` per module |
| `docs/native-module-architecture.md` | Update with Android dyna path |

## References

- Android `System.load` documentation:
  https://developer.android.com/reference/java/lang/System#load(java.lang.String)
- React Native dynamic package loading discussion:
  https://github.com/facebook/react-native/issues/27567 (long-standing
  feature request, not yet upstream).
- Apple App Store Review Guideline 3.3.2 (the iOS counter-example):
  https://developer.apple.com/app-store/review/guidelines/#3.3.2
- Yaver's existing super-host bridge architecture:
  [`docs/native-module-architecture.md`](native-module-architecture.md)
