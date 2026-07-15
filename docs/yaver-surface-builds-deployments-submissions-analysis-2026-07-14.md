# Yaver Surface Builds, Deployments, and Store Submissions

Deep analysis as of 2026-07-14. Markdown can drift; before shipping any lane,
re-grep the scripts/manifests named here and re-check the current Apple/Google
submission pages.

## Executive Summary

Yaver should not be treated as one mobile app with five marketing badges. The
repo has three different release shapes:

1. **Shared mobile artifact:** iOS/iPadOS + CarPlay + Apple Watch companion bits
   ride the iPhone archive; Android phone/tablet + Android TV + Android Auto +
   Android XR compatibility ride the Play AAB.
2. **Standalone native surface apps:** tvOS, standalone watchOS, and Wear OS are
   separate native projects with their own build scripts and store review gates.
3. **Web/spatial surface:** `web/app/spatial` is already a deployable WebXR /
   spatial web lane through Cloudflare, while native visionOS and dedicated
   immersive Android XR remain future lanes.

The strongest immediate submission path is:

1. Android phone + Android TV + Android Auto messaging + Android XR-compatible
   shared AAB to Play internal.
2. iOS/TestFlight with Apple Watch companion target + Live Activity/CarPlay
   scaffolding, but **do not claim CarPlay hardware readiness until the managed
   entitlement is in the provisioning profile**.
3. tvOS after a real device / Siri Remote pass.
4. Wear OS after the docs and pipeline are brought up to the deploy script.
5. Native visionOS / immersive Android XR only after dedicated projects exist.

The biggest repo mismatch: the marketing/README now says multi-surface, but the
store-listing helpers still say "from your phone" (`scripts/set-appstore-info.py`
and `scripts/set-playstore-info.py`). That is a submission metadata blocker if
the next release is meant to present Yaver as watch/TV/car/XR-capable.

## External Requirements Checked

- Apple submission page: as of 2026-04-28, uploads to App Store Connect need to
  meet the latest minimum SDK/toolchain requirements, with Apple pointing
  developers to Xcode 26 and latest platform SDKs:
  <https://developer.apple.com/app-store/submitting/>
- Apple App Store Connect screenshots/previews are managed per platform section:
  <https://developer.apple.com/help/app-store-connect/manage-app-information/upload-app-previews-and-screenshots/>
- Apple watchOS App Store metadata is attached through an iOS app record or a
  watchOS-only app path:
  <https://developer.apple.com/help/app-store-connect/create-an-app-record/add-watchos-app-information/>
- Apple Vision Pro submission uses normal App Store Connect review and TestFlight:
  <https://developer.apple.com/visionos/submit/>
- Apple App Review submissions can contain multiple items and support removing
  failed items:
  <https://developer.apple.com/distribute/app-review/>
- Google Wear OS packaging requires `android.hardware.type.watch`, a standalone
  metadata declaration, Wear screenshots/listing copy, form-factor opt-in, and
  Play quality review:
  <https://developer.android.com/training/wearables/packaging>
- Google Android TV quality now includes strict TV behavior checks; from
  2026-08-01, TV apps must support both 32-bit and 64-bit architectures and 16 KB
  page sizes:
  <https://developer.android.com/docs/quality-guidelines/tv-app-quality>
- Google Android Auto / Android Automotive distribution requires form-factor
  opt-in, car quality review, correct manifest metadata, and Automotive
  screenshots where applicable:
  <https://developer.android.com/training/cars/distribute>
- Google Android for Cars supported categories are limited. Messaging,
  navigation, POI, IoT, weather, media, and parked categories are not generic
  "show any app UI" surfaces:
  <https://developer.android.com/training/cars>
- Android XR packaging/distribution is now through Google Play for Android XR
  users, with quality guidelines:
  <https://developer.android.com/develop/xr/package-and-distribute>
- Android XR can show existing 2D Android apps as panels by default, while
  immersive/XR features require explicit XR work:
  <https://developer.android.com/develop/xr/jetpack-xr-sdk/add-xr-to-existing>

## Current Repo Surface Matrix

| Surface | Current artifact | Build/deploy script | Store/review path | Readiness |
|---|---|---|---|---|
| iOS / iPadOS | Expo RN native app in `mobile/ios` | `scripts/deploy-testflight.sh`; CI `release-mobile.yml` manual iOS job | App Store Connect / TestFlight | Production lane exists; local path is canonical. |
| Android phone/tablet | Expo RN app in `mobile/android` | `scripts/deploy-playstore.sh` + `scripts/upload-playstore.py`; CI `release-mobile.yml` android | Play internal/closed/production | Build lane exists; upload is split local script + helper. |
| Apple Watch companion | Target injected into iOS project by `scripts/add-watch-ios-target.js`; native `watch/` also exists | iOS TestFlight script injects target; standalone `scripts/deploy-watchos.sh` | App Store Connect watchOS metadata | Mixed state: companion is in iOS path; standalone watchOS script exists. Needs one product decision. |
| Wear OS | Separate Compose app in `wear/` | `scripts/deploy-wear-os.sh` | Same Play listing or Wear form factor | Script exists and checks required manifest; docs still say source-only/not CI-wired. |
| Apple TV / tvOS | Separate SwiftUI app in `tvos/` | `scripts/deploy-tvos.sh` | App Store Connect tvOS platform | Staged but needs real-device 10-foot QA and metadata. |
| Android TV | Shared Android AAB with leanback manifest | `scripts/deploy-android-tv.sh` | Play Android TV form factor | Strong: shared AAB, manifest checks, banner check, runbook exists. |
| CarPlay | Shared iOS app with native CarPlay scene delegate | `scripts/deploy-carplay.sh` delegates to TestFlight after preflight | App Store Connect; managed entitlement/profile gate | Code scaffold exists; hardware submission blocked until profile carries entitlement. |
| Android Auto | Shared Android AAB, messaging notifications | `scripts/deploy-android-auto.sh` | Play Android Auto form factor / quality review | Strongest car lane: messaging/RemoteInput is built around allowed category. |
| visionOS | Compatible iPad-on-Vision Pro mode; no native `visionos/` project | `scripts/deploy-visionos.sh` | App Store Connect visionOS or compatible iOS lane | Compatible analysis exists; native upload not wired. |
| Android XR / VR | Shared AAB marked XR/headset compatible; web `/spatial` exists | `scripts/deploy-android-xr.sh`; web deploy for `/spatial` | Play Android XR for compatible 2D; dedicated immersive needs new artifact | Compatibility lane only unless `--immersive` and native OpenXR declarations pass. |
| WebXR / spatial web | Next.js route `web/app/spatial` | `scripts/deploy-web.sh` | Cloudflare production, no app store | Production web deploy path exists. |

## iOS / iPadOS

### What exists

`scripts/deploy-testflight.sh` is the main local release lane. It:

- injects/refreshes the Watch companion target via `scripts/add-watch-ios-target.js`;
- injects/refreshes the Live Activity target via `scripts/add-liveactivity-ios-target.js`;
- loads App Store Connect credentials from `yaver vault env --project mobile`,
  then falls back to `~/.appstoreconnect/yaver.env`;
- increments `CFBundleVersion`;
- archives `Yaver.xcworkspace` with automatic signing;
- exports with `method=app-store-connect` and `destination=upload`;
- uploads directly to App Store Connect / TestFlight.

The CI `release-mobile.yml` can do iOS on `workflow_dispatch`, but the project
guide still prefers local deploy first where possible. The local script is better
aligned with the current repo-specific watch/live-activity injection.

### Submission posture

Yaver's iOS story remains defensible if submitted as a developer tool and remote
runtime control app:

- user runs their own machine/agent;
- code and task output remain P2P/local;
- Hermes preview is for the developer's own paired devices;
- no public marketplace or arbitrary third-party app distribution.

The app metadata still needs to match the multi-surface positioning. Current
`scripts/set-appstore-info.py` default copy says "directly from their phone" and
does not mention watch, TV, car, or XR surfaces.

### Risks

- Apple policy around developer-tool downloaded code is sensitive. Keep the
  "own projects / own paired devices / not a marketplace" language in review
  notes and screenshots.
- The local script mutates `Info.plist` build number. If commit discipline is not
  strict, version bumps can leak into unrelated commits.
- Xcode/SDK minimums are now time-sensitive. Apple currently points uploads at
  Xcode 26-era requirements; older local Xcode will become a hard fail.

## Android Mobile / Play

### What exists

`scripts/deploy-playstore.sh` builds the shared release AAB from
`mobile/android`. It:

- forces Gradle heap up because the RN tree OOMs on smaller heaps;
- loads Play/keystore secrets from the vault and `~/.androidplay/yaver.env`;
- bumps `versionCode`, optionally looking up max remote bundle version;
- builds the on-device sandbox payload when Go is available;
- builds `bundleRelease`;
- prints manual upload instructions.

`scripts/upload-playstore.py` then uploads one or more AABs to the chosen Play
track. Defaults are `io.yaver.mobile`, `internal`, and `draft`.

### Submission posture

The Android artifact is doing more than phone/tablet:

- Android TV via leanback optional features and banner.
- Android Auto via notification/messaging metadata.
- Android XR/headset compatibility via optional XR/headset features.
- Wear OS is not inside this AAB; it is separate in `wear/`.

### Risks

- `scripts/set-playstore-info.py` metadata is stale and phone-only.
- `deploy-playstore.sh` does not upload; callers must remember
  `upload-playstore.py`. Some surface scripts do this; the base script does not.
- The AAB contains broad permissions and special-use foreground service. Store
  review notes must keep the developer-tool/on-device-agent justification tight.

## Apple Watch / watchOS

### What exists

There are two Apple Watch concepts in the repo:

1. The iOS TestFlight path injects an Apple Watch companion target into the
   committed iOS project.
2. `watch/` is a standalone Swift watchOS app with its own
   `scripts/deploy-watchos.sh`.

`scripts/deploy-watchos.sh` can:

- generate the Xcode project with XcodeGen;
- build unsigned locally;
- archive and export with App Store Connect API credentials;
- upload exported IPA via `xcrun altool` by default because the script notes
  Xcode 17 rejecting `app-store-connect` export for standalone watchOS archives.

### Product decision needed

Pick exactly one release model for the next Apple Watch milestone:

- **Companion-only:** watch app is bundled with iOS app. This is best if the watch
  is a thin approval/voice surface that assumes the phone as brain-of-record.
- **Standalone watchOS:** separate App Store watchOS binary. This is best if the
  watch can authenticate and work over LAN/relay without the phone.

Right now the repo can drift because both models exist. The docs and submission
plan need to state which one is product-authoritative.

### Submission posture

Apple's current watchOS submission path requires App Store Connect watchOS app
information and current SDK/toolchain compliance. The watch UX must be
minimal: voice/intent, one-line status, haptic result, confirm/cancel for risky
actions. Do not try to show terminal/log output on the wrist.

### Risks

- Standalone watchOS uploads are special enough that `altool` fallback may break
  as Apple tooling changes.
- If the product copy says "standalone" but the app still depends on phone state,
  App Review can reject for misleading functionality.
- Watch confirmation taps are risky; destructive/deploy actions need phone or
  second-factor confirmation if they materially change infrastructure.

## Wear OS

### What exists

`wear/` is a standalone Compose app. The manifest correctly declares:

- `android.hardware.type.watch` with `required=true`;
- `com.google.android.wearable.standalone=true`;
- microphone, internet, vibrate;
- Wear Data Layer listener for `/yaver/watch/reply`.

`scripts/deploy-wear-os.sh` builds an AAB and checks:

- the watch hardware feature exists;
- standalone metadata exists;
- the package name matches the chosen package.

It defaults `WEAR_PACKAGE` to `io.yaver.mobile`, aligning with Google's
recommendation to use the same package/listing when appropriate. Its default
version code is `mobile versionCode + 1`, which is fragile but intentional:
Google requires unique version codes across form factors in the same app.

### Submission posture

Google's Wear OS distribution requires the Play Console Wear OS form factor,
Wear screenshots, listing text that mentions Wear OS, and quality review. The
app's current architecture fits: voice-first, glanceable, no dense UI.

### Repo mismatch

`wear/README.md` still says the directory is source-only and not wired into any
build pipeline. That is now stale relative to `scripts/deploy-wear-os.sh`.
Update the README before treating the Wear OS lane as release-ready.

### Risks

- `compileSdk=34` / `targetSdk=34` may lag current Play requirements in the
  2026 window.
- The same-package strategy requires strict version-code management. A naive
  phone version bump can collide with Wear.
- Google commonly rejects Wear apps for missing Wear mention in listing, missing
  screenshots, broken basic functionality, or poor round-display layout.

## tvOS / Apple TV

### What exists

`tvos/` is a standalone SwiftUI app, generated with XcodeGen from
`tvos/project.yml`. The project uses:

- bundle id `io.yaver.mobile` for Universal Purchase alignment;
- product name `Yaver`;
- tvOS deployment target 17.0;
- `RunsAsCurrentUser=true`;
- local-network usage text;
- App Icon & Top Shelf assets.

`scripts/deploy-tvos.sh` can build unsigned, or archive and upload to App Store
Connect with the same App Store Connect credentials as iOS. `tvos/README.md`
claims Apple TV App Store is staged with build 4 and screenshots uploaded.

### Submission posture

tvOS is a good fit for:

- runtime status wallboard;
- runner/session status;
- reload controls;
- Apple TV remote/capture surfaces;
- QR/device-code sign-in.

It is not a good fit for editing code, raw logs, or text-heavy terminal work.
The app must be fully Siri Remote / D-pad navigable.

### Risks

- The script's default manual profile name (`Yaver TVOS_APP_STORE profile`) is
  brittle. If the profile name changes, upload fails.
- Off-LAN relay fallback is still listed as a follow-up in `tvos/README.md`.
  If screenshots/review imply remote-anywhere TV control, this is a review risk.
- tvOS ratings/reviews behavior differs from iOS; plan platform-specific store
  assets and support copy.

## Android TV

### What exists

Android TV is the cleanest Android non-phone surface because it ships inside the
same AAB. The tracked manifest has:

- optional `android.software.leanback`;
- optional `android.hardware.touchscreen`;
- `LEANBACK_LAUNCHER`;
- `android:banner="@drawable/tv_banner"`;
- native runtime surface metadata including `android-tv`.

`scripts/deploy-android-tv.sh` runs the Play build unless `--skip-build`, checks
the release manifest, verifies the TV banner is PNG `320x180`, then optionally
uploads through `upload-playstore.py`.

`docs/yaver-android-tv-release-runbook.md` is accurate in spirit: Play Console
form-factor opt-in and TV screenshots are manual.

### Submission posture

Use the shared AAB, then opt into Android TV in Play Console. The review should
be routine if:

- launch lands on a TV-friendly route;
- all flows are D-pad navigable;
- sign-in uses QR/device-code;
- no touch-only screens are reachable as the primary TV path;
- screenshots are 10-foot UI, not phone screenshots.

### Risks

- Google TV quality rules now include a future-dated architecture/page-size
  requirement from 2026-08-01. The shared RN native build must be checked for
  32-bit + 64-bit and 16 KB page size before that date.
- The CI path uses `expo prebuild --clean`, so the Expo plugin must keep TV
  manifest entries in sync with the tracked native overlay.

## CarPlay

### What exists

`scripts/deploy-carplay.sh` preflights:

- iOS `Info.plist`;
- entitlements file;
- `YaverCarPlaySceneDelegate.swift`;
- CarPlay scene manifest;
- delegate implements `CPTemplateApplicationSceneDelegate`.

Without `--upload`, it builds an iOS simulator release with signing disabled.
With `--upload`, it requires the CarPlay entitlement in `Yaver.entitlements`,
then delegates to `scripts/deploy-testflight.sh`.

`docs/yaver-car-surface.md` says Apple has granted the account-level
CarPlay Voice Based Conversation capability, but the App ID and provisioning
profile must still be configured. The script correctly hard-fails upload if the
entitlement key is absent.

### Submission posture

CarPlay must remain voice/template-only:

- no terminal;
- no custom UI;
- no code panes;
- no dangerous write action without explicit confirmation;
- short voice summaries and list-template choices only.

The best category fit is voice-based conversation / communication around the
existing runner session, not "developer tool UI in the car."

### Hard blocker

The managed entitlement must appear in the provisioning profile before the
source entitlement is restored. Otherwise signing fails and the iOS release lane
breaks. This is an Apple Developer portal/profile task, not a code task.

## Android Auto

### What exists

Android Auto is implemented as notification/messaging, not an Android for Cars
App Library `CarAppService`. The manifest declares:

- `com.google.android.gms.car.application` metadata pointing at
  `@xml/automotive_app_desc`;
- native reply receiver;
- native module/plugin wiring through `withAndroidAutoMessaging`.

`scripts/deploy-android-auto.sh` verifies:

- manifest metadata;
- automotive descriptor uses notification;
- reply receiver is registered;
- MainApplication imports/registers the native package;
- native module uses `NotificationCompat.MessagingStyle`;
- native module uses `RemoteInput`.

It can then build or upload the shared Play AAB.

### Submission posture

This is the most realistic car path today because Android Auto messaging is an
allowed, narrow surface. It should be positioned as:

- speak a command/reply;
- continue an existing live runner session;
- receive one-sentence status;
- answer menu choices safely.

Do not describe this as a general car UI or remote desktop.

### Risks

- Google car review can take longer and applies category-specific quality
  guidelines.
- Android Auto and Android Automotive OS are not the same. This lane is Android
  Auto notification/messaging unless and until an AAOS parked/IoT/native app
  exists.
- If the same submission includes a non-compliant car artifact in production,
  Google can reject the submission.

## visionOS / Apple Vision Pro

### What exists

There is no native `visionos/` project in the repo. `scripts/deploy-visionos.sh`
therefore runs compatible iOS/iPad analysis:

- checks the visionOS SDK exists;
- checks `UIRequiredDeviceCapabilities` does not require ARKit;
- checks iPad orientations include landscape;
- checks `UIRequiresFullScreen` is not true.

With `--upload` and no native project, it uploads the compatible artifact through
the iOS/TestFlight lane. Native upload is explicitly not wired.

The web route `web/app/spatial` is the real spatial implementation today and is
deployed through Cloudflare, not App Store Connect.

### Submission posture

Two valid paths:

- **Compatible iPad app on Vision Pro:** quickest, if the iPad UI is acceptable
  in a spatial window.
- **WebXR/spatial web:** already production-deployable; good for demos and
  standalone headset browser use.

Native visionOS should wait until there is a real product need beyond compatible
iPad panels.

### Risks

- Do not claim native visionOS until there is a `visionos/` project and App Store
  Connect platform record.
- App Store featuring/submission for Apple Vision Pro expects screenshots and UX
  tuned for the platform.

## Android XR / VR

### What exists

The Android manifest makes the shared AAB headset-visible but not immersive:

- optional `android.software.xr.api.openxr`;
- optional `android.hardware.vr.headtracking`;
- optional camera/bluetooth features;
- optional touchscreen;
- `com.oculus.intent.category.VR`.

`scripts/deploy-android-xr.sh` checks the AAB and manifest. In default mode it
reports compatibility mode. In `--immersive` mode it requires hard OpenXR /
headtracking and XR start-mode declarations; the current shared AAB should fail
that mode by design.

### Submission posture

For Android XR:

- Existing 2D Android app can be available as a panel-style app.
- Dedicated immersive OpenXR / Quest / Android XR release needs a separate
  native artifact and stronger manifest requirements.
- The current `/spatial` web route is more mature than native Android XR.

### Risks

- Google Android XR docs are actively changing; re-check before submission.
- Optional XR feature declarations can make the app visible, but visibility is
  not a quality spatial experience.
- A true immersive app must not be a phone UI stretched into a headset.

## Build and Deploy Order

### Immediate internal-track matrix

1. `npm run build` in `web/` and `./scripts/deploy-web.sh` for landing/spatial.
2. `./scripts/deploy-playstore.sh`, then:
   - `./scripts/deploy-android-tv.sh --skip-build`
   - `./scripts/deploy-android-auto.sh --skip-source-preflight`
   - `./scripts/deploy-android-xr.sh --skip-build`
   - `PLAY_STORE_KEY_FILE=... python3 scripts/upload-playstore.py`
3. `./scripts/deploy-testflight.sh` for iOS/iPadOS + companion targets.
4. `./scripts/deploy-tvos.sh` locally, then `--upload` after real device QA.
5. `./scripts/deploy-wear-os.sh`, then `--upload` after listing/screenshot
   readiness.
6. `./scripts/deploy-watchos.sh` only after choosing standalone vs companion.

### Manual console steps that cannot be fully scripted

- Play Console form-factor opt-ins:
  - Android TV
  - Android Auto
  - Android Automotive OS only if an AAOS artifact/category exists
  - Wear OS
  - Android XR when using that distribution path
- Play Console screenshots and listing copy per form factor.
- App Store Connect platform screenshots/previews:
  - iOS/iPadOS
  - watchOS
  - tvOS
  - visionOS if native or compatible listing is enabled
- Apple Developer portal managed capability/profile configuration for CarPlay.

## Required Fixes Before Next Multi-Surface Submission

1. **Update store metadata helpers.**
   - `scripts/set-appstore-info.py`
   - `scripts/set-playstore-info.py`
   Replace phone-only copy with multi-surface wording while keeping the
   mobile-first actual workflow honest.

2. **Resolve Apple Watch release model.**
   Decide companion-only vs standalone watchOS. Update `watch/README.md`,
   `scripts/deploy-watchos.sh` usage, App Store Connect metadata, and landing
   copy to match.

3. **Refresh Wear OS docs.**
   `wear/README.md` says not wired, while `scripts/deploy-wear-os.sh` exists.
   The doc should become a release runbook with Play form-factor checklist.

4. **Add a surface preflight aggregator.**
   A single script should run all non-upload checks:
   - Android TV manifest/banner
   - Android Auto metadata/native module
   - Android XR compatibility
   - Wear OS manifest/AAB
   - tvOS project generation/build
   - visionOS compatible analysis
   - CarPlay entitlement/scene preflight

5. **Add store asset inventory.**
   Track required screenshots and listing copy per surface in a checked-in doc
   or JSON file. This prevents "code ready, Console blocked" surprises.

6. **Clarify Android XR and visionOS claims.**
   Marketing can say AR/VR/spatial support, but technical docs should say:
   - web `/spatial` exists;
   - compatible iPad-on-Vision Pro exists;
   - Android XR compatibility exists;
   - native immersive artifacts are not yet release-ready.

7. **CarPlay portal/profile task.**
   Do not restore the CarPlay entitlement key until a regenerated profile carries
   it. After that, run `scripts/deploy-carplay.sh --upload`.

8. **TV real-device QA.**
   Android TV and tvOS must pass D-pad/Siri Remote navigation before review.
   Store screenshots must show the TV UI, not phone screens.

9. **Architecture/page-size audit for Android TV.**
   Before 2026-08-01, verify shared native libraries support both 32-bit and
   64-bit where required and satisfy 16 KB page-size constraints.

## Recommended Release Naming

Avoid one broad "Yaver on everything" release. Use a staged naming model:

- **Yaver Mobile Runtime:** iOS/Android developer control and Hermes preview.
- **Yaver TV Runtime:** Android TV + Apple TV wallboard/control room.
- **Yaver Car Voice Runtime:** Android Auto messaging now; CarPlay after
  entitlement/profile is active.
- **Yaver Wrist Runtime:** Apple Watch/Wear OS approval and voice surface.
- **Yaver Spatial Runtime:** web `/spatial`, Vision Pro compatible mode, Android
  XR-compatible mode; native immersive later.

This lets each store reviewer see a narrow, platform-appropriate purpose instead
of a generic developer tool trying to occupy restricted surfaces.
