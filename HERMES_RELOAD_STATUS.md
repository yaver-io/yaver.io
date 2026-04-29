# Hermes Reload — Status (2026-04-29)

> Snapshot of the Yaver mobile ↔ Go agent ↔ relay path that pushes a
> compiled Hermes bytecode bundle from a developer's machine into the
> Yaver mobile app's super-host bridge.
>
> Code is the source of truth. Re-grep before acting on anything below.

## Goal

Developer iterates on a third-party React Native app (e.g. SFMG, Talos,
Bento) on **any host OS** (Linux, macOS, WSL2). They tap "Open in Yaver"
in the Yaver mobile app on their iPhone. Yaver:

1. Calls `/dev/build-native` on the host's Go agent.
2. Agent runs Metro + `hermesc` to produce a Hermes bytecode bundle.
3. Mobile downloads the bundle (direct on LAN, via public relay otherwise).
4. iOS super-host invalidates its current bridge, creates a fresh one via
   `ExpoReactNativeFactory + RCTAppDependencyProvider`, and runs the
   guest bundle inside it. Full New Architecture (TurboModules / Fabric /
   JSI) — never WebView.

## Pipeline

```
host machine                                    relay                   iPhone
┌────────────────────────────┐         ┌──────────────────┐    ┌──────────────────┐
│ Metro + hermesc            │         │  QUIC tunnel     │    │ YaverBundleLoader│
│  ↓ HBC bundle              │ ─────► │  /d/{deviceId}/  │ ─► │  .loadBundle()   │
│  ↓ ValidateHBC(magic, BC,  │         │  /dev/native-bundle  │  └─ super-host     │
│      size, MD5)            │         │                  │    │     bridge swap   │
│  ↓ BundleMetadata.JSON()   │         │  streaming wire  │    └──────────────────┘
│  ↓ X-Yaver-Bundle-Metadata │         │  (0xFE magic)    │
└────────────────────────────┘         └──────────────────┘
```

## What we built / what's verified working

| Layer | Status | Evidence |
|---|---|---|
| Agent compiles Hermes BC bundle | ✅ | `/dev/build-native` returns 200 + `{size, md5, bcVersion}`. SFMG: 8.49 MB HBC. |
| `ValidateHBC` (magic + BC + size + MD5 at build time) | ✅ | `desktop/agent/bundlecheck.go:46`. Rejects raw JS, BC mismatch, oversize, undersized. |
| `BundleMetadata` JSON travels in `X-Yaver-Bundle-Metadata` header | ✅ | `desktop/agent/devserver_http.go:2572`. Includes platform / arch / RN / Expo SDK / hermesRef. |
| Streaming wire protocol over QUIC | ✅ | `desktop/agent/relay_stream_wire.go` writer + `relay/relay_stream_wire.go` reader. First-byte magic `0xFE`, 64 KiB chunks, `0x00000000` EOF. End-to-end 8.49 MB bundle in 1.0s @ 8.4 MB/s through `https://public.yaver.io`. Test: `mobile-headless/test/relay-bundle-truncation.test.ts`. |
| `localIPs` filter — agent never publishes public IPs to Convex | ✅ | `desktop/agent/main.go::firstPrivateIPv4`. Mobile no longer hits non-RFC1918 hosts → no iOS ATS -1022. |
| iPhone download via Yaver relay | ✅ | Confirmed end-to-end 2026-04-29 ~03:19, build 246. |
| iPhone download via direct LAN (when on same WiFi) | ✅ | Beacon + Convex IP path filters to RFC1918 only. |
| Hot Reload tab does NOT auto-jump to Projects mid-load | ✅ | `mobile/app/(tabs)/_layout.tsx`. State change but no `router.navigate`. |
| Settings runner picker shows only claude-code / codex / opencode | ✅ | `mobile/app/(tabs)/settings.tsx` `SUPPORTED_RUNNERS`. Lands in build 247+. |

## What's broken / regressed

### 1. iOS bundle validator is a stub

`mobile/ios/Yaver/YaverBundleValidator.swift::validateMetadata` and
`validateBundle` both return `nil`. CLAUDE.md flags this:

> Currently a stub — `validateMetadata` / `validateBundle` return nil
> (no-op). `SDKManifest.shared` reads `sdk-manifest.json` from
> `Bundle.main` and exposes `hermesBytecodeVersion` + `raw`.

The fallback path in `YaverBundleLoader.loadBundle` (no-header branch) does
a basic magic + BC check from the raw bytes, but **only when the agent
omits `X-Yaver-Bundle-Metadata`**. Modern agents always send the header,
so the bytes-level fallback is skipped, and the stubbed validator passes
everything. iOS effectively trusts the agent.

Impact: a BC-version mismatch from a non-Yaver-supplied hermesc would
crash silently inside Hermes instead of producing a clean
`BC_VERSION_MISMATCH` rejection. **Not the cause of the SFMG crash** — it
just means we have one fewer guard rail.

### 2. No native-module compatibility handshake

The agent → mobile handshake covers:

- ✅ HBC magic
- ✅ Hermes bytecode version (96)
- ✅ Size + MD5 integrity
- ✅ Module name (AppRegistry component)
- ✅ Builder platform / arch / RN version / Expo SDK / hermesRef (sent but
   **not compared**)
- ❌ **Native module list**

SFMG's `package.json` can declare TurboModules that are NOT registered in
`mobile/sdk-manifest.json`. The agent will happily compile + ship the
bundle, iOS will happily load it, and at runtime SFMG's JS will call a
TurboModule that resolves to nil (or a partial wrapper), which throws
`NSException`, which crashes Hermes (see #3).

**This is the real handshake gap.** We pin the engine version but not the
ABI surface of the host container.

### 3. SFMG TestFlight crash (build 246, iPhone14,7, iOS 18.3.1)

Crash log: `EXC_BAD_ACCESS (SIGSEGV)` at address `0x0c` (null+12) on
Thread 10. Stack:

```
0   hermes  HiddenClass::isDictionary() const
1   hermes  HiddenClass::addProperty(...)
6   hermes  HermesRuntimeImpl::setPropertyValue(...)
10  React   TurboModuleConvertUtils::convertNSExceptionToJSError    ← ENTRY
11  React   ObjCTurboModule::performVoidMethodInvocation
17  libdispatch  _dispatch_call_block_and_release
```

What happened, frame by frame:

1. Bundle downloaded and saved to `/Documents/bundles/main.jsbundle`. ✅
2. Super-host swapped the bridge, started executing SFMG's main module.
3. SFMG called a void TurboModule method via `dispatch_async`.
4. The Objective-C selector threw `NSException`.
5. RN called `convertNSExceptionToJSError(...)` to wrap it.
6. Hermes tried to set a property on the new error object — but the
   error's `HiddenClass*` slot was null. Crashed reading
   `propertyMap_` at offset `0x0c`.

This is a Hermes bug class triggered by an NSException thrown during
JSI method invocation when JS is running on a different queue. Yaver's
super-host bridge passes ObjC exceptions through unsafely.

**Crash is independent of the relay/streaming work**: bundle loaded
fine. The crash is inside SFMG's runtime, post-load, calling something
that isn't wired right in Yaver's container.

### 4. "Was working before" regression

User reports the same SFMG flow worked from both Linux and macOS hosts
through the same iPhone in earlier sessions. Plausible regression
sources, in order of likelihood:

1. **SFMG added a native dependency** that Yaver's super-host doesn't
   register. Compare `~/Workspace/sfmg/package.json` deps vs
   `mobile/sdk-manifest.json::nativeModules` — any module in the former
   not in the latter is the suspect.
2. **Yaver mobile dropped or version-bumped a module** between 1.18.20
   and 1.18.22 that SFMG implicitly relied on.
3. **Expo SDK 52 ↔ RN 0.81.5 surface change** — the
   `TurboModuleRegistry.getEnforcing()` path can throw differently
   between minor versions.
4. **A specific iOS 18.3.x behaviour change** in `NSException` →
   `RCTTurboModule` propagation. Less likely; would affect every guest
   bundle, not just SFMG.

We can't tell from the crash log alone which TurboModule threw —
`convertNSExceptionToJSError` doesn't preserve the selector name on the
JS-side error object until after the property-set step that crashed.

## Path forward

In order of cost vs payoff:

### A. Implement `YaverBundleValidator.validateMetadata` properly *(small)*

Wire the stubs to compare:

- `metadata.hermesBCVersion` vs `SDKManifest.shared.hermesBytecodeVersion`
- `metadata.size` vs actual `data.count`
- MD5 of `data` vs `metadata.md5`
- Hermes magic at offset 4 of `data`

Reject with structured error codes (`BC_VERSION_MISMATCH`,
`SIZE_MISMATCH`, `MD5_MISMATCH`, `MAGIC_MISSING`). Defense in depth, no
behavioural change for valid bundles.

### B. Native-module compat preflight in `/dev/build-native` *(medium)*

Before running Metro:

1. Read `package.json` of the project being built.
2. Read `mobile/sdk-manifest.json::nativeModules` (already shipped in app).
3. Diff. For each missing module, decide:
   - **Hard fail** (return 4xx with structured warning) — recommended
     for first-class modules SFMG can't function without.
   - **Warn + ship** (proceed but include `incompatibleModules: [...]`
     in the response so mobile shows a banner).

This is the actual fix for the SFMG crash class. Once a guest bundle
can't reach the super-host without a green compat check, the
NSException path stops being exercised.

### C. Defensive TurboModule wrapper in super-host *(medium-large)*

Patch `mobile/ios/Yaver/AppDelegate.swift` (or a new `YaverSafeTurboModule`
proxy) to wrap `RCTTurboModule.performVoidMethodInvocation` in
`@try/@catch`, and on catch, build the JSError directly via
`runtime.makeError(message)` rather than calling
`convertNSExceptionToJSError`. Avoids the Hermes HiddenClass null
deref permanently.

This requires a TestFlight build to validate.

### D. Identify the specific SFMG TurboModule throw *(investigative)*

```bash
# From SFMG
jq -r '.dependencies | keys[]' ~/Workspace/sfmg/package.json | sort > /tmp/sfmg-deps.txt
jq -r '.nativeModules | keys[]' /Users/kivanccakmak/Workspace/yaver.io/mobile/sdk-manifest.json | sort > /tmp/yaver-deps.txt
comm -23 /tmp/sfmg-deps.txt /tmp/yaver-deps.txt
```

Anything in SFMG-only that ends in `react-native-*` or
`@*-react-native-*` is a candidate. Streaming the iPhone Console.app
output during a fresh "Open in Yaver" attempt would also pin the
selector — but that needs a wired iPhone + macOS Console.app, not the
TestFlight crash report alone.

## Verified versus stale

| Claim in this doc | Verified by | Last verified |
|---|---|---|
| Streaming relay 8.49 MB SFMG bundle in 1.0s | `mobile-headless/test/relay-bundle-truncation.test.ts` against `public.yaver.io` | 2026-04-29 |
| `firstPrivateIPv4` filters non-RFC1918 | `desktop/agent/main.go::firstPrivateIPv4` | 2026-04-29 (commit `cc1f0990`) |
| iOS `YaverBundleValidator` is a stub | `mobile/ios/Yaver/YaverBundleValidator.swift` | 2026-04-29 |
| Hermes BC version 96 | `mobile/sdk-manifest.json::hermes.bytecodeVersion` | 2026-04-29 |
| SFMG crash on TurboModule conversion | `testflight_feedback-15.zip` Thread 10 | 2026-04-29 03:27:37 +0300 |
| Settings runner filter shipped | `mobile/app/(tabs)/settings.tsx::SUPPORTED_RUNNERS`, build 247+ | 2026-04-29 (commit `a2a12751`) |

When code changes, edit this doc in the same commit.
