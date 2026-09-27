# Android TV — release runbook

> Status: live (updated 2026-09-27). Android TV is a standalone Compose app in
> `androidtv/`. It produces a distinct TV-only AAB with application ID
> `io.yaver.mobile` and ships from the existing Play listing on `tv:internal`.
> The Kotlin namespace remains `io.yaver.tv`. CI fallback wiring lives in
> `.github/workflows/release-android-tv.yml`.

## 1. Build and upload through the canonical wrapper

```bash
export PLAY_STORE_KEY_FILE=/path/to/google-play-service-account.json
./deploy/deploy.sh android-tv
```

The wrapper selects an unused versionCode from the live `io.yaver.mobile`
listing, builds `androidtv/`, checks the Leanback manifest and 320×180 banner,
verifies the upload certificate, probes `tv:internal`, then uploads. The GitHub
workflow is a fallback and invokes the same wrapper.

## 2. Confirm the AAB is actually TV-eligible (don't trust "should be")

Download the AAB artifact from the run and dump its manifest:

```bash
gh run download <run-id> -n <aab-artifact>          # or pull app-release.aab
# AAB manifests are protobuf — use bundletool, not grep:
bundletool dump manifest --bundle app-release.aab | grep -E "LEANBACK_LAUNCHER|leanback|banner"
```

Expect: `android.intent.category.LEANBACK_LAUNCHER`, a `<uses-feature
android:name="android.software.leanback" android:required="true">`, and
`android:banner="@drawable/tv_banner"`. The `mobile-variants.yml`
`build-android-variant` job does this assertion automatically on a debug APK via
`aapt2 dump xmltree`. The canonical deploy also checks the manifest and banner.

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
- **Leanback must be required and touchscreen optional** because this standalone
  AAB is TV-only. The phone AAB remains a separate artifact in the same listing.
- **No leanback ⇒ silently phone-only.** The app still builds and works on phones
  with leanback missing — the TV eligibility just vanishes. That's exactly the
  regression `mobile-variants.yml::verify-wiring` exists to catch.
- VersionCodes are shared across every AAB in the `io.yaver.mobile` listing and
  must strictly increase. The wrapper queries Play before it builds.
```
