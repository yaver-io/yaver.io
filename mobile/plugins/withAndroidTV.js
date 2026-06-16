// withAndroidTV.js — Expo config plugin that makes the Android APK TV-eligible
// (Android TV / Google TV) so it surfaces on the TV launcher and runs without a
// touchscreen. See docs/yaver-appletv-remote-control.md Part C (M12) + Part D
// and docs/yaver-tv-car-deployment-roadmap.md §2.1 (M-AT1).
//
// SELF-CONTAINED as of 2026-06-17: this plugin now also COPIES the TV banner
// PNG (assets/tv_banner.png, 320×180) into res/drawable-xhdpi/tv_banner.png via
// a dangerous mod, so registering it never fails a build on a missing
// @drawable/tv_banner. Two of the three old activation prerequisites (banner +
// focus screens — see app/tv-home.tsx) are now in-tree; the remaining step is a
// native rebuild + TV-emulator verify before a Play TV submission.
//
// IMPORTANT — actual shipping path: `scripts/deploy-playstore.sh` runs
// `gradlew bundleRelease` on the EXISTING (git-tracked) android project, it does
// NOT run `expo prebuild`. So the leanback launcher + banner that actually ship
// live in the force-tracked overlay `mobile/android/app/src/main/AndroidManifest.xml`
// (+ the tracked `res/drawable-xhdpi/tv_banner.png`). This plugin only takes
// effect on a from-clean `expo prebuild`; keep it in sync with that overlay.
//
// Activation = add "./plugins/withAndroidTV" to app.json `plugins`, then:
//   expo prebuild --platform android --clean → restore force-tracked overlays →
//   gradle bundleRelease, then verify on an Android TV emulator (or a real
//   Google TV) before any Play TV submission. The plugin is Android-only
//   (withAndroidManifest / android dangerous mod) so iOS/TestFlight builds are
//   completely unaffected whether or not it is registered.
//
// What this plugin does (Android only):
//   - Marks leanback + touchscreen as NOT required (uses-feature required=false),
//     so Play lists the same APK on TV form factors.
//   - Adds <category android:name="android.intent.category.LEANBACK_LAUNCHER"/>
//     to the main activity's launcher intent-filter, and android:banner +
//     android:isGame="false" to the <application>, so the TV home screen shows
//     the app with its banner.
//   - Copies assets/tv_banner.png → res/drawable-xhdpi/tv_banner.png so the
//     android:banner reference always resolves.

const {
  withAndroidManifest,
  withDangerousMod,
  AndroidConfig,
} = require("@expo/config-plugins");
const fs = require("fs");
const path = require("path");

const LEANBACK_LAUNCHER = "android.intent.category.LEANBACK_LAUNCHER";
const TV_BANNER = "@drawable/tv_banner";
// Source asset (repo-relative) and the drawable bucket it lands in. xhdpi is
// the density Android TV uses for the 320×180 leanback banner.
const BANNER_ASSET = path.join("assets", "tv_banner.png");
const BANNER_DRAWABLE_DIR = "drawable-xhdpi";
const BANNER_DRAWABLE_NAME = "tv_banner.png";

function ensureUsesFeature(manifest, name) {
  manifest["uses-feature"] = manifest["uses-feature"] || [];
  const exists = manifest["uses-feature"].some((f) => f.$ && f.$["android:name"] === name);
  if (!exists) {
    manifest["uses-feature"].push({ $: { "android:name": name, "android:required": "false" } });
  }
}

// ---- Android manifest: leanback launcher + banner -------------------------
function withTVManifest(config) {
  return withAndroidManifest(config, (cfg) => {
    const manifest = cfg.modResults.manifest;

    // Non-touch / leanback are optional → same APK serves phone + TV.
    ensureUsesFeature(manifest, "android.software.leanback");
    ensureUsesFeature(manifest, "android.hardware.touchscreen");

    const app = AndroidConfig.Manifest.getMainApplicationOrThrow(cfg.modResults);
    app.$ = app.$ || {};
    // TV banner on the application (shown on the leanback home row).
    if (!app.$["android:banner"]) {
      app.$["android:banner"] = TV_BANNER;
    }
    // Leanback launcher requires an explicit non-game flag so the app sorts
    // under "Apps" rather than the games row.
    if (!app.$["android:isGame"]) {
      app.$["android:isGame"] = "false";
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
}

// ---- Android: copy the banner PNG into res/drawable-xhdpi ------------------
function withTVBannerDrawable(config) {
  return withDangerousMod(config, [
    "android",
    (cfg) => {
      const src = path.join(cfg.modRequest.projectRoot, BANNER_ASSET);
      const drawableDir = path.join(
        cfg.modRequest.platformProjectRoot,
        "app",
        "src",
        "main",
        "res",
        BANNER_DRAWABLE_DIR,
      );
      if (!fs.existsSync(src)) {
        throw new Error(
          `withAndroidTV: TV banner asset missing at ${BANNER_ASSET}. ` +
            "Generate a 320×180 PNG before building a TV APK.",
        );
      }
      fs.mkdirSync(drawableDir, { recursive: true });
      fs.copyFileSync(src, path.join(drawableDir, BANNER_DRAWABLE_NAME));
      return cfg;
    },
  ]);
}

module.exports = function withAndroidTV(config) {
  config = withTVManifest(config);
  config = withTVBannerDrawable(config);
  return config;
};

module.exports.LEANBACK_LAUNCHER = LEANBACK_LAUNCHER;
module.exports.TV_BANNER = TV_BANNER;
module.exports.BANNER_ASSET = BANNER_ASSET;
module.exports.withTVManifest = withTVManifest;
