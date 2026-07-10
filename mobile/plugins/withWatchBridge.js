// withWatchBridge.js — Expo config plugin that wires the PHONE-side smartwatch
// bridge (WCSession on iOS, Wear Data Layer on Android) into the native
// projects that `expo prebuild --clean` regenerates. This is the phone half of
// the phone-paired smartwatch loop (docs/yaver-smartwatch-voice-terminal.md
// §3 mode A). The watch apps themselves are the standalone native projects in
// watch/ (watchOS) and wear/ (Wear OS).
//
// REGISTERED in mobile/app.json (activated 2026-07-10). Registration was the
// ACTIVATION step; it now runs on every `expo prebuild`:
//   iOS     — copies YaverWatchBridge.swift/.m into the app target. The
//             watchOS companion target itself (watch/) is a separate XcodeGen
//             project that must be embedded in the iOS app for WCSession to
//             have a peer. That embedding is a one-time Xcode step (see
//             watch/README.md §"Creating the Xcode target").
//   Android — copies the Wear bridge Kotlin sources into the app package,
//             registers YaverWearListenerService (intent-filter PATH_TURN) in
//             the manifest, adds the play-services-wearable dependency, and
//             registers YaverWearPackage in MainApplication.
// The JS side (src/lib/watchEntry.ts) no-ops safely when the native module is
// absent (e.g. on a fresh prebuild before pod install / gradle sync).

const {
  withAndroidManifest,
  withAppBuildGradle,
  withMainApplication,
  withDangerousMod,
} = require("@expo/config-plugins");
const fs = require("fs");
const path = require("path");

const WEAR_DEP = "com.google.android.gms:play-services-wearable:18.2.0";
const WEAR_PKG_DIR = path.join("io", "yaver", "mobile", "wear");
const LISTENER_PATH_TURN = "/yaver/watch/turn";

// ---- iOS: copy the WCSession bridge sources into the app target -----------
function withWatchIosSources(config) {
  return withDangerousMod(config, [
    "ios",
    (cfg) => {
      const root = cfg.modRequest.projectRoot;
      const src = path.join(root, "native-watch", "ios");
      const destApp = path.join(cfg.modRequest.platformProjectRoot, "Yaver");
      for (const f of ["YaverWatchBridge.swift", "YaverWatchBridge.m"]) {
        const from = path.join(src, f);
        if (fs.existsSync(from)) fs.copyFileSync(from, path.join(destApp, f));
      }
      // TODO(activation): add the watchOS companion app target (sources in
      // watch/YaverWatch) and embed it in the iOS app so WCSession has a peer.
      // Best done with @config-plugins/apple-target or in Xcode once.
      return cfg;
    },
  ]);
}

// ---- Android: copy the Wear bridge Kotlin sources -------------------------
function withWearAndroidSources(config) {
  return withDangerousMod(config, [
    "android",
    (cfg) => {
      const root = cfg.modRequest.projectRoot;
      const src = path.join(root, "native-wear", "android");
      const destDir = path.join(
        cfg.modRequest.platformProjectRoot,
        "app", "src", "main", "java", WEAR_PKG_DIR,
      );
      fs.mkdirSync(destDir, { recursive: true });
      for (const f of [
        "YaverWearBridgeModule.kt",
        "YaverWearListenerService.kt",
        "YaverWearPackage.kt",
      ]) {
        const from = path.join(src, f);
        if (fs.existsSync(from)) fs.copyFileSync(from, path.join(destDir, f));
      }
      return cfg;
    },
  ]);
}

// ---- Android: register the listener service in the manifest ---------------
function withWearAndroidManifest(config) {
  return withAndroidManifest(config, (cfg) => {
    const app = cfg.modResults.manifest.application?.[0];
    if (!app) return cfg;
    app.service = app.service || [];
    const name = "io.yaver.mobile.wear.YaverWearListenerService";
    if (!app.service.some((s) => s.$["android:name"] === name)) {
      app.service.push({
        $: { "android:name": name, "android:exported": "true" },
        "intent-filter": [
          {
            action: [{ $: { "android:name": "com.google.android.gms.wearable.MESSAGE_RECEIVED" } }],
            data: [{ $: { "android:scheme": "wear", "android:host": "*", "android:pathPrefix": LISTENER_PATH_TURN } }],
          },
        ],
      });
    }
    return cfg;
  });
}

function withWearAndroidGradle(config) {
  return withAppBuildGradle(config, (cfg) => {
    if (!cfg.modResults.contents.includes(WEAR_DEP)) {
      cfg.modResults.contents = cfg.modResults.contents.replace(
        /dependencies\s*\{/,
        `dependencies {\n    implementation "${WEAR_DEP}"`,
      );
    }
    return cfg;
  });
}

function withWearPackageRegistration(config) {
  return withMainApplication(config, (cfg) => {
    let src = cfg.modResults.contents;
    const importLine = "import io.yaver.mobile.wear.YaverWearPackage";
    if (!src.includes(importLine)) {
      // add import after the package declaration
      src = src.replace(/(^package .*$)/m, `$1\n${importLine}`);
    }
    // add to the packages list returned by getPackages()
    if (!src.includes("YaverWearPackage()")) {
      src = src.replace(
        /(val packages\s*=\s*PackageList\(this\)\.packages)/,
        "$1.apply { add(YaverWearPackage()) }",
      );
    }
    cfg.modResults.contents = src;
    return cfg;
  });
}

module.exports = function withWatchBridge(config) {
  config = withWatchIosSources(config);
  config = withWearAndroidSources(config);
  config = withWearAndroidManifest(config);
  config = withWearAndroidGradle(config);
  config = withWearPackageRegistration(config);
  return config;
};

module.exports.PATH_TURN = LISTENER_PATH_TURN;
module.exports.PATH_REPLY = "/yaver/watch/reply";
