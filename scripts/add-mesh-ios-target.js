#!/usr/bin/env node
/**
 * add-mesh-ios-target.js — add the YaverMeshTunnel packet-tunnel app-extension
 * target to the COMMITTED mobile/ios/Yaver.xcodeproj/project.pbxproj.
 *
 * Why a script instead of @bacons/apple-targets: this repo git-tracks a
 * hand-maintained pbxproj (the cold-rebuild flow restores it via
 * `git checkout -- mobile/ios/`). @bacons regenerates the pbxproj during
 * prebuild, so its target is discarded by that checkout. The durable home for
 * the target is the committed pbxproj — which is what this script edits, so it
 * must be COMMITTED after running (then it survives prebuild like the custom
 * Yaver*.swift panes do). See docs/mesh-mobile-tunnel.md.
 *
 * Idempotent: re-running is a no-op once the target exists.
 *
 * Run from repo root:  node scripts/add-mesh-ios-target.js
 */
const path = require("path");
const fs = require("fs");
const xcode = require(path.join(__dirname, "..", "mobile", "node_modules", "xcode"));

const PROJ = path.join(__dirname, "..", "mobile", "ios", "Yaver.xcodeproj", "project.pbxproj");
const TARGET = "YaverMeshTunnel";
const BUNDLE = "io.yaver.mobile.YaverMeshTunnel";
const TEAM = "5SJZ4KA39A";
const DEPLOY = "15.5";

const proj = xcode.project(PROJ);
proj.parseSync();

// --- idempotency ---------------------------------------------------------
const native = proj.pbxNativeTargetSection();
for (const k of Object.keys(native)) {
  const t = native[k];
  if (t && typeof t === "object" && t.name === TARGET) {
    console.log(`✓ ${TARGET} target already present — no-op.`);
    process.exit(0);
  }
}

// --- create the app-extension target ------------------------------------
// addTarget(name, type, subfolder, bundleId) — node-xcode creates the
// PBXNativeTarget (productType app-extension), product .appex ref, and a
// build-config list with Debug/Release configs.
const target = proj.addTarget(TARGET, "app_extension", TARGET, BUNDLE);

// Group holding the extension's source files (path = mobile/ios/YaverMeshTunnel).
proj.addPbxGroup(
  ["PacketTunnelProvider.swift", "Info.plist", "YaverMeshTunnel.entitlements"],
  TARGET,
  TARGET
);

// Build phases for the new target.
proj.addBuildPhase(["PacketTunnelProvider.swift"], "PBXSourcesBuildPhase", "Sources", target.uuid);
proj.addBuildPhase([], "PBXFrameworksBuildPhase", "Frameworks", target.uuid);
proj.addBuildPhase([], "PBXResourcesBuildPhase", "Resources", target.uuid);

// Embed the .appex into the main app + add a target dependency so it builds first.
const mainUuid = proj.getFirstTarget().uuid;
proj.addBuildPhase([], "PBXCopyFilesBuildPhase", "Embed App Extensions", mainUuid, "plugins");
// Add the appex product to that Embed phase.
proj.addToPbxCopyfilesBuildPhase({
  basename: `${TARGET}.appex`,
  group: "Embed App Extensions",
  target: mainUuid,
});
proj.addTargetDependency(mainUuid, [target.uuid]);

// --- build settings on BOTH configs of the new target -------------------
const cfgList = native[target.uuid].buildConfigurationList;
const xcConfigSection = proj.pbxXCConfigurationList();
const buildConfigs = proj.pbxXCBuildConfigurationSection();
const configRefs = xcConfigSection[cfgList].buildConfigurations.map((c) => c.value);

const SETTINGS = {
  PRODUCT_NAME: `"${TARGET}"`,
  PRODUCT_BUNDLE_IDENTIFIER: BUNDLE,
  DEVELOPMENT_TEAM: TEAM,
  CODE_SIGN_STYLE: "Automatic",
  CODE_SIGN_ENTITLEMENTS: `${TARGET}/${TARGET}.entitlements`,
  INFOPLIST_FILE: `${TARGET}/Info.plist`,
  GENERATE_INFOPLIST_FILE: "NO",
  SWIFT_VERSION: "5.0",
  IPHONEOS_DEPLOYMENT_TARGET: DEPLOY,
  TARGETED_DEVICE_FAMILY: '"1,2"',
  CURRENT_PROJECT_VERSION: "1",
  MARKETING_VERSION: "1.0",
  SKIP_INSTALL: "YES",
  LD_RUNPATH_SEARCH_PATHS: '"$(inherited) @executable_path/Frameworks @executable_path/../../Frameworks"',
};

for (const ref of configRefs) {
  const bs = buildConfigs[ref].buildSettings;
  for (const [key, val] of Object.entries(SETTINGS)) bs[key] = val;
}

fs.writeFileSync(PROJ, proj.writeSync());
console.log(`✓ added ${TARGET} app-extension target → ${path.relative(process.cwd(), PROJ)}`);
console.log("  Next: pod install (if needed) + build. COMMIT the pbxproj so it survives prebuild.");
