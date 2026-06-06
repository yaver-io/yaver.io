// withMeshTunnel.js — Expo config plugin that wires the on-device Yaver Mesh
// tunnel (Phase 7) into the native projects that `expo prebuild --clean`
// regenerates. See docs/mesh-mobile-tunnel.md.
//
// ⚠️ DELIBERATELY NOT REGISTERED in app.json yet. Adding it to the `plugins`
// array is the ACTIVATION step, and it must happen TOGETHER with:
//   1. The Apple Network Extension entitlement granted on the App ID + a
//      regenerated provisioning profile (Apple-gated; the repo can't grant it).
//   2. A native rebuild (pod install / gradle) — ~30–60 min cold.
//   3. On-device verification (Simulator NEPacketTunnelProvider is unreliable).
// Until all three are done, leaving this plugin unregistered keeps every
// existing TestFlight/Play build green. The JS side (src/lib/yaverMesh.ts) is
// already safe to ship: with no native module present it no-ops.
//
// What this plugin does once registered:
//   iOS   — app entitlement (packet-tunnel-provider) + App Group, copies the
//           RN bridge (YaverMeshModule.swift/.m) into the app target, and the
//           extension target (PacketTunnelProvider.swift) + WireGuardKit dep.
//   Android — VpnService + BIND_VPN_SERVICE permission in the manifest and the
//           com.wireguard.android:tunnel dependency.

const {
  withEntitlementsPlist,
  withAndroidManifest,
  withAppBuildGradle,
  withDangerousMod,
} = require("@expo/config-plugins");
const fs = require("fs");
const path = require("path");

const APP_GROUP = "group.io.yaver.mesh";
const TUNNEL_BUNDLE_ID = "io.yaver.mobile.YaverMeshTunnel";
const WG_ANDROID_DEP = "com.wireguard.android:tunnel:1.0.20230706";

// ---- iOS: app-target entitlements + App Group -----------------------------
function withMeshIosEntitlements(config) {
  return withEntitlementsPlist(config, (cfg) => {
    cfg.modResults["com.apple.developer.networking.networkextension"] = ["packet-tunnel-provider"];
    cfg.modResults["com.apple.security.application-groups"] = [APP_GROUP];
    return cfg;
  });
}

// ---- iOS: copy the RN bridge sources next to the generated project --------
// The extension TARGET itself (a separate app-extension in the pbxproj) is the
// one piece a plain plugin can't fully express — creating PBX target/build
// phases is best done with @config-plugins/apple-target or a dedicated
// xcode-mod. This copies the reference sources into ios/ so that step has the
// files to reference, and leaves a marker for the human/EAS finisher.
function withMeshIosSources(config) {
  return withDangerousMod(config, [
    "ios",
    (cfg) => {
      const root = cfg.modRequest.projectRoot;
      const src = path.join(root, "native-mesh", "ios");
      const destApp = path.join(cfg.modRequest.platformProjectRoot, "Yaver");
      for (const f of ["YaverMeshModule.swift", "YaverMeshModule.m"]) {
        const from = path.join(src, f);
        if (fs.existsSync(from)) fs.copyFileSync(from, path.join(destApp, f));
      }
      // Extension target sources land in a sibling group the target owns.
      const extDir = path.join(cfg.modRequest.platformProjectRoot, "YaverMeshTunnel");
      fs.mkdirSync(extDir, { recursive: true });
      const ptp = path.join(src, "PacketTunnelProvider.swift");
      if (fs.existsSync(ptp)) fs.copyFileSync(ptp, path.join(extDir, "PacketTunnelProvider.swift"));
      // TODO(activation): register the YaverMeshTunnel app-extension target,
      // add its entitlement (packet-tunnel-provider + APP_GROUP), embed it in
      // the app, and add the WireGuardKit SwiftPM package. Provider bundle id
      // must equal TUNNEL_BUNDLE_ID (matches YaverMeshModule.swift).
      return cfg;
    },
  ]);
}

// ---- Android: VpnService + permission + WireGuard dep ----------------------
function withMeshAndroidManifest(config) {
  return withAndroidManifest(config, (cfg) => {
    const manifest = cfg.modResults.manifest;
    manifest["uses-permission"] = manifest["uses-permission"] || [];
    const has = manifest["uses-permission"].some(
      (p) => p.$["android:name"] === "android.permission.BIND_VPN_SERVICE"
    );
    if (!has) {
      manifest["uses-permission"].push({ $: { "android:name": "android.permission.BIND_VPN_SERVICE" } });
      manifest["uses-permission"].push({ $: { "android:name": "android.permission.FOREGROUND_SERVICE" } });
    }
    const app = manifest.application?.[0];
    if (app) {
      app.service = app.service || [];
      const named = app.service.some((s) => s.$["android:name"] === ".YaverMeshVpnService");
      if (!named) {
        app.service.push({
          $: {
            "android:name": ".YaverMeshVpnService",
            "android:permission": "android.permission.BIND_VPN_SERVICE",
            "android:exported": "false",
            "android:foregroundServiceType": "specialUse",
          },
          "intent-filter": [{ action: [{ $: { "android:name": "android.net.VpnService" } }] }],
        });
      }
    }
    return cfg;
  });
}

function withMeshAndroidGradle(config) {
  return withAppBuildGradle(config, (cfg) => {
    if (!cfg.modResults.contents.includes(WG_ANDROID_DEP)) {
      cfg.modResults.contents = cfg.modResults.contents.replace(
        /dependencies\s*\{/,
        `dependencies {\n    implementation "${WG_ANDROID_DEP}"`
      );
    }
    return cfg;
  });
}

module.exports = function withMeshTunnel(config) {
  config = withMeshIosEntitlements(config);
  config = withMeshIosSources(config);
  config = withMeshAndroidManifest(config);
  config = withMeshAndroidGradle(config);
  return config;
};

module.exports.APP_GROUP = APP_GROUP;
module.exports.TUNNEL_BUNDLE_ID = TUNNEL_BUNDLE_ID;
