// withAndroidTV.js — Expo config plugin that makes the Android APK TV-eligible
// (Android TV / Google TV) so it surfaces on the TV launcher and runs without a
// touchscreen. See docs/yaver-appletv-remote-control.md Part C (M12) + Part D.
//
// ⚠️ DELIBERATELY NOT REGISTERED in app.json yet (mirrors withAndroidAutoMessaging.js
// / withMeshTunnel.js). Registering it in the `plugins` array is the ACTIVATION
// step, and it must happen TOGETHER with:
//   1. A TV banner asset at android:banner (320×180 xhdpi) — referenced below as
//      @drawable/tv_banner; drop the PNG in before activating or the build fails.
//   2. D-pad FOCUS handling on the screens you want usable on TV (RN gives basic
//      focus for free, but lists/buttons should set hasTVPreferredFocus / use
//      TVFocusGuideView for a good remote experience). The Apple TV screen has a
//      compact `?surface=glass` layout to base a `tv` variant on.
//   3. A native rebuild: `expo prebuild --platform android --clean` → restore
//      force-tracked overlays → gradle bundleRelease, then verify on an Android
//      TV emulator (or a real Google TV) before any Play TV submission.
// Until those are done, leaving this plugin unregistered keeps every existing
// Play/TestFlight build green (it's phone-only, unchanged).
//
// What this plugin does once registered (Android only):
//   - Marks leanback + touchscreen as NOT required (uses-feature required=false),
//     so Play lists the same APK on TV form factors.
//   - Adds <category android:name="android.intent.category.LEANBACK_LAUNCHER"/>
//     to the main activity's launcher intent-filter, and android:banner to the
//     <application>, so the TV home screen shows the app.

const { withAndroidManifest, AndroidConfig } = require("@expo/config-plugins");

const LEANBACK_LAUNCHER = "android.intent.category.LEANBACK_LAUNCHER";
const TV_BANNER = "@drawable/tv_banner";

function ensureUsesFeature(manifest, name) {
  manifest["uses-feature"] = manifest["uses-feature"] || [];
  const exists = manifest["uses-feature"].some((f) => f.$ && f.$["android:name"] === name);
  if (!exists) {
    manifest["uses-feature"].push({ $: { "android:name": name, "android:required": "false" } });
  }
}

module.exports = function withAndroidTV(config) {
  return withAndroidManifest(config, (cfg) => {
    const manifest = cfg.modResults.manifest;

    // Non-touch / leanback are optional → same APK serves phone + TV.
    ensureUsesFeature(manifest, "android.software.leanback");
    ensureUsesFeature(manifest, "android.hardware.touchscreen");

    const app = AndroidConfig.Manifest.getMainApplicationOrThrow(cfg.modResults);
    // TV banner on the application (shown on the leanback home row).
    app.$ = app.$ || {};
    if (!app.$["android:banner"]) {
      app.$["android:banner"] = TV_BANNER;
    }

    // Add LEANBACK_LAUNCHER to the main activity's MAIN/LAUNCHER intent-filter.
    const activity = AndroidConfig.Manifest.getMainActivityOrThrow(cfg.modResults);
    for (const filter of activity["intent-filter"] || []) {
      const isLauncher = (filter.category || []).some(
        (c) => c.$ && c.$["android:name"] === "android.intent.category.LAUNCHER",
      );
      if (!isLauncher) continue;
      filter.category = filter.category || [];
      const hasLeanback = filter.category.some((c) => c.$ && c.$["android:name"] === LEANBACK_LAUNCHER);
      if (!hasLeanback) {
        filter.category.push({ $: { "android:name": LEANBACK_LAUNCHER } });
      }
    }
    return cfg;
  });
};

module.exports.LEANBACK_LAUNCHER = LEANBACK_LAUNCHER;
module.exports.TV_BANNER = TV_BANNER;
