# Android TV — release runbook

> Status: live (2026-06-17). Android TV ships in the **same AAB** as the phone app
> (leanback is a manifest addition via `mobile/plugins/withAndroidTV.js` + the tracked
> `mobile/android/app/src/main/AndroidManifest.xml` overlay). This is the last-mile
> checklist for getting that AAB onto a **TV release**. CI wiring:
> `.github/workflows/release-mobile.yml` (signed → Play internal) and
> `.github/workflows/mobile-variants.yml` (wiring guard + debug-APK verify).

## 1. Build (GitHub CI — no local disk/keychain needed)

```bash
# signed AAB → Play internal testing track (auto versionCode bump)
gh workflow run "Release Mobile" --ref main -f upload_testflight=false -f upload_playstore=true
gh run watch <run-id> --exit-status    # or: gh run view <run-id> --json status,conclusion
```

The android job: `npm ci → expo prebuild --clean → restore splash drawable →
decode keystore → bump versionCode → bundleRelease → upload to internal track`.
Because `expo prebuild --clean` regenerates the manifest from `app.json` plugins,
the leanback entries come from the **registered** `withAndroidTV` plugin (this is
why app.json registration matters on CI — the tracked overlay only governs the
*local* `scripts/deploy-playstore.sh` path).

## 2. Confirm the AAB is actually TV-eligible (don't trust "should be")

Download the AAB artifact from the run and dump its manifest:

```bash
gh run download <run-id> -n <aab-artifact>          # or pull app-release.aab
# AAB manifests are protobuf — use bundletool, not grep:
bundletool dump manifest --bundle app-release.aab | grep -E "LEANBACK_LAUNCHER|leanback|banner"
```

Expect: `android.intent.category.LEANBACK_LAUNCHER`, a `<uses-feature
android:name="android.software.leanback" android:required="false">`, and
`android:banner="@drawable/tv_banner"`. The `mobile-variants.yml`
`build-android-variant` job does this assertion automatically on a debug APK via
`aapt2 dump xmltree`.

## 3. Device-verify BEFORE submitting (Google rejects un-navigable TV apps)

The one thing that gets a TV submission rejected: screens that a D-pad can't
drive. Verify on an **Android TV emulator** (Android Studio → Device Manager →
Television profile, e.g. *Android TV (1080p)*) or a real **Google TV**:

```bash
# install the AAB's universal APK (or the debug APK from mobile-variants.yml)
adb install app-debug.apk
adb shell monkey -p io.yaver.mobile -c android.intent.category.LEANBACK_LAUNCHER 1
```

Check: app shows on the **leanback home row** with the banner; launching lands on
`/tv-home` (focus-driven launcher); every tile + the sign-out button is reachable
and actuatable with **D-pad only** (arrows + center select), no touch assumed.
The QR sign-in (`/tv-signin`) is reachable. If focus gets trapped anywhere, fix
focus before submitting.

## 4. Promote to a TV release (Play Console — browser, manual)

This is the step that cannot be scripted from here (no Console API for form-factor
opt-in / store-listing review):

1. **Play Console → your app → Release → Setup → Advanced settings → Form
   factors → Android TV → Add form factor.** Accept the TV declaration.
2. Provide **TV-specific store assets**: TV banner (already in the APK), at least
   one **TV screenshot** (1920×1080), and the TV description.
3. Move the internal build up the tracks: **internal → closed (TV testers) →
   production**, or submit directly to the **Android TV** track for review.
4. Submit for review. Google runs a **TV quality** pass (D-pad nav, no
   touch-only flows, banner present, no crash on launch). Turnaround is usually a
   few days.

## 5. Gotchas

- **Banner is mandatory and must be exactly 320×180** (`@drawable/tv_banner`).
  Missing/!=size → instant TV rejection. Guarded in `mobile-variants.yml`.
- **`leanback` + `touchscreen` must be `required="false"`** or Play won't let the
  same APK serve both phone and TV.
- **No leanback ⇒ silently phone-only.** The app still builds and works on phones
  with leanback missing — the TV eligibility just vanishes. That's exactly the
  regression `mobile-variants.yml::verify-wiring` exists to catch.
- TestFlight-style upload rate limits don't apply to Play, but internal-track
  versionCodes must strictly increase (CI handles this: `100 + run_number`).
```
