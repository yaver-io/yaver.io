# visionOS — TestFlight & App Store submission

> Status as of **2026-07-14**: the native app **builds and archives clean**
> (first successful compile — see "What was broken" below). The upload is
> **blocked on one Console-only click**: the `io.yaver.mobile` app record does
> not yet carry the `VISION_OS` platform.

`visionos/README.md` covers *why* the app is native SwiftUI, what it shares with
tvOS, and how to build it. This file covers *shipping* it — the part that is not
a code problem.

## TL;DR

```bash
# 1. YOU do this once, in App Store Connect (no API exists for it):
#    Yaver IO → ＋ beside the platforms in the sidebar → Add Platform → visionOS

# 2. then:
source ~/.appstoreconnect/yaver.env      # or: $(yaver vault env --project mobile)
./scripts/deploy-visionos.sh --upload
```

## The iOS TestFlight lane does NOT ship visionOS

A recurring assumption worth killing: **`deploy-testflight.sh` will never
produce a visionOS build.** It archives `mobile/ios/Yaver.xcworkspace`, scheme
`Yaver` — the iPhone app, and nothing else. It does not read `visionos/`.

They are two separate archives from two separate Xcode projects:

| Surface | Project | Scheme | Script |
|---|---|---|---|
| iPhone / iPad | `mobile/ios/Yaver.xcworkspace` | `Yaver` | `scripts/deploy-testflight.sh` |
| Apple TV | `tvos/YaverTV.xcodeproj` | `YaverTV` | `scripts/deploy-tvos.sh` |
| Vision Pro | `visionos/YaverVision.xcodeproj` | `YaverVision` | `scripts/deploy-visionos.sh` |

Shipping the headset is a deliberate second command. It is not a side effect of
shipping iOS.

## The blocker: the platform must exist on the app record

All three surfaces share **one** bundle ID — `io.yaver.mobile` — on purpose, for
Universal Purchase. But a shared bundle ID does **not** imply a shared platform:
the App Store Connect *app record* enumerates its platforms explicitly, and an
upload for a platform the record doesn't list has nowhere to land.

Current state, queried from the API (`/v1/apps?filter[bundleId]=io.yaver.mobile`,
app id `6760467669`):

- platforms with versions: **`IOS`, `TV_OS`**
- platforms with TestFlight builds: **`IOS`**

No `VISION_OS`. That is what `deploy-visionos.sh` means when it prints
*"visionOS native upload is gated until a visionOS platform/project exists in
App Store Connect."*

### This cannot be automated

The App Store Connect API has **no endpoint to add a platform to an existing
app**. App creation and platform addition are Console-only. It is the same class
of gap as per-email Play testers (Console-only) — do not go looking for an
`ops` verb or an `hcloud`-style CLI for it; there isn't one, and a script that
appears to do it is lying.

So, in the browser:

> **App Store Connect → Yaver IO → the `＋` beside the platforms in the left
> sidebar → Add Platform → visionOS**

### Do NOT "re-enable the identifier" in the Developer portal

The instinct is to go to Certificates, Identifiers & Profiles and enable
something on `io.yaver.mobile`. **Don't.** The App ID already exists and is
already shared across all three platforms — nothing there is missing.

Worse, it is actively dangerous: **turning on any capability on an App ID marks
every existing provisioning profile INVALID, on every platform.** That is not
theoretical — it is exactly what broke the tvOS lane when CarPlay was enabled
(2026-07-07), and it is why `deploy-visionos.sh` uses automatic signing with
`-allowProvisioningUpdates` rather than pinning a profile by name the way
`deploy-tvos.sh` does. A stray toggle there costs you the iOS and tvOS profiles
too.

Adding a *platform* in App Store Connect touches none of that. Take that path.

## What was broken (fixed 2026-07-14)

The visionOS app shipped in `9f5eba538` had **never been compiled** — its commit
message says so plainly ("the visionOS platform component (~7GB) isn't installed
on this Mac, so this is unverified against a compiler"). Two real bugs were
hiding behind that:

**1. `deploy-visionos.sh` could not run at all.** macOS ships bash 3.2, where
expanding an *empty* array under `set -u` is an "unbound variable" error rather
than nothing. `VERSION_ARGS` is empty unless `VISIONOS_MARKETING_VERSION` /
`VISIONOS_BUILD_NUMBER` are set, so the default invocation died at `xcodebuild`
on every Mac. Both call sites now expand through the `${arr[@]+"${arr[@]}"}`
guard, which yields zero words when the array is empty.

**2. The headset's empty state was the terminal state of the app.** With no box
registered, `VisionDashboardView` rendered "No machine selected" and the advice
*"Pick a box in the Yaver phone app — it syncs here."* Nothing syncs: boxes live
in `@AppStorage("yaver.tv.boxes")`, a per-app-container `UserDefaults` on a
different physical device. There is no CloudKit, no App Group, no backend box
roster. And the only caller of `store.addBox()` in the repo was tvOS's
`AddBoxView`, which lives in a tvOS-only view file that the visionOS target does
not compile — so the headset had no picker, no sheet, and no route out. A fresh
install could never reach the dashboard.

`AddBoxView` is now its own file in the shared client layer
(`tvos/YaverTV/AddBoxView.swift`, pulled in by `visionos/project.yml`), and the
Vision empty state offers **Add machine** like tvOS's does.

## Preflight

- **visionOS platform component** must be installed (`XROS.platform` in
  `/Applications/Xcode.app/Contents/Developer/Platforms/`). Having the SDK in
  `xcodebuild -showsdks` is not enough. Install with
  `xcodebuild -downloadPlatform visionOS` (~7 GB).
- **Disk.** `mobile-cache-cleanup.sh preflight` fails hard under 20 GB. The
  visionOS archive is small (3 Swift files, no CocoaPods, no Hermes bundle), so
  it survives a tighter budget than the iOS lane — but don't chain it after an
  iOS archive on a full disk.
- **Credentials.** `APP_STORE_KEY_PATH`, `APP_STORE_KEY_ID`,
  `APP_STORE_KEY_ISSUER`, `APPLE_TEAM_ID` — from the vault
  (`yaver vault env --project mobile`) or the gitignored
  `~/.appstoreconnect/yaver.env` when the vault is locked.

## Verify the build without touching Apple

```bash
./scripts/deploy-visionos.sh          # no --upload: builds + runs the
                                      # compatible-iPad-on-visionOS plist checks
```

This is safe to run any time and consumes no TestFlight upload slot.

## Versioning

`MARKETING_VERSION` / `CURRENT_PROJECT_VERSION` come from
`visionos/project.yml` (currently `1.0.0` / `1`). Override per-run without
editing the spec:

```bash
VISIONOS_MARKETING_VERSION=1.0.0 VISIONOS_BUILD_NUMBER=2 \
  ./scripts/deploy-visionos.sh --upload
```

Unlike the iOS script, this one does **not** auto-bump the build number. A
second upload at the same `CURRENT_PROJECT_VERSION` will be rejected by Apple —
bump it yourself.

## Rate limit

TestFlight accepts roughly **15–20 uploads per app per day**, counted across
platforms on the same app record. A day spent iterating on iOS eats the budget
the headset needs. If you see "Upload limit reached", wait 24h — re-running
does not help.

## Known gap

The app **compiles and archives; it has never been run.** No simulator boot, no
device install. The "Add machine" flow is verified against the type checker, not
against a headset. Boot the visionOS simulator before putting a build in front
of testers.
