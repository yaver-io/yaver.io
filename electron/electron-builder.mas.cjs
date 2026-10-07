"use strict";

const path = require("node:path");
const pkg = require("./package.json");

// The Store build belongs to the existing multi-platform Yaver IO record.
// Apple requires every platform in a universal-purchase record to use the
// same bundle ID; the direct Developer ID desktop lane intentionally retains
// io.yaver.gui in package.json.
const bundleId = "io.yaver.mobile";
const target = process.env.YAVER_MAS_TARGET === "mas-dev" ? "mas-dev" : "mas";
const buildNumber = process.env.YAVER_MAC_BUILD_NUMBER || "1";
if (!/^[0-9]+(?:\.[0-9]+){0,2}$/.test(buildNumber)) {
  throw new Error("YAVER_MAC_BUILD_NUMBER must contain one to three dot-separated integer components");
}

const profileEnv = target === "mas-dev"
  ? process.env.YAVER_MAS_DEV_PROVISIONING_PROFILE
  : process.env.YAVER_MAS_PROVISIONING_PROFILE;
const provisioningProfile = profileEnv ? path.resolve(profileEnv) : undefined;

module.exports = {
  appId: bundleId,
  productName: pkg.productName,
  buildVersion: buildNumber,
  files: ["src/**/*", "assets/**/*", "package.json"],
  extraResources: [{from:"resources/plain-ssh",to:"plain-ssh"}],
  directories: {
    output: "dist-mas",
    buildResources: "assets",
  },
  protocols: pkg.build.protocols,
  // Deliberately NO extraResources/bin/yaver. App Sandbox cannot honestly run
  // Yaver's arbitrary local CLI/repo/capture workload. The MAS/TestFlight app
  // is the client surface; the Developer ID DMG is the full local node.
  mac: {
    category: "public.app-category.developer-tools",
    icon: "assets/icon.icns",
    target: [target],
    notarize: false,
    hardenedRuntime: true,
    bundleVersion: buildNumber,
    extendInfo: {
      ITSAppUsesNonExemptEncryption: false,
      NSHumanReadableCopyright: "Copyright © Simkab. All rights reserved.",
    },
  },
  mas: {
    type: "distribution",
    entitlements: "assets/entitlements.mas.plist",
    entitlementsInherit: "assets/entitlements.mas.inherit.plist",
    entitlementsLoginHelper: "assets/entitlements.mas.plist",
    provisioningProfile,
    artifactName: "yaver-gui-${version}-mac-testflight-${arch}.${ext}",
  },
  masDev: {
    type: "development",
    entitlements: "assets/entitlements.mas.plist",
    entitlementsInherit: "assets/entitlements.mas.inherit.plist",
    entitlementsLoginHelper: "assets/entitlements.mas.plist",
    provisioningProfile,
    artifactName: "yaver-gui-${version}-mac-mas-dev-${arch}.${ext}",
  },
};
