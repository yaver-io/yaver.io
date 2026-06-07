/**
 * @bacons/apple-targets config for the Yaver Mesh on-device WireGuard tunnel.
 *
 * STAGED — NOT yet activated. `@bacons/apple-targets` is installed but
 * deliberately NOT in app.json `plugins`, so `expo prebuild` ignores this
 * directory and the current iOS build stays unchanged. Activating it is the
 * finish step (see docs/mesh-mobile-tunnel.md "Activation"), and it has TWO
 * remaining hard gates that no plugin can satisfy automatically:
 *
 *   1. Apple Developer portal — enable the Network Extension capability
 *      (packet-tunnel-provider) + the App Group `group.io.yaver.mesh` on the
 *      App ID `io.yaver.mobile`, and regenerate the provisioning profile.
 *   2. WireGuardKit (Swift Package, github.com/WireGuard/wireguard-apple) +
 *      its wireguard-go bridge. @bacons/apple-targets only links SYSTEM
 *      frameworks (its `frameworks` field), so the SPM dependency must be added
 *      to the generated `YaverMeshTunnel` target — either via Xcode
 *      "Add Package Dependency" after prebuild, or a withXcodeProject SPM mod.
 *      Without it, PacketTunnelProvider.swift's `import WireGuardKit` won't
 *      compile.
 *
 * @type {import('@bacons/apple-targets/app.plugin').ConfigFunction}
 */
module.exports = (config) => ({
  type: "network-packet-tunnel",
  name: "YaverMeshTunnel",
  displayName: "Yaver Mesh",
  // Dot-prefixed → appended to the app id → io.yaver.mobile.YaverMeshTunnel.
  // Must match `tunnelBundleId` in native-mesh/ios/YaverMeshModule.swift.
  bundleIdentifier: ".YaverMeshTunnel",
  // System frameworks the extension links directly. WireGuardKit is an SPM
  // package, added separately (see header note) — it is NOT a system framework.
  frameworks: ["NetworkExtension"],
  entitlements: {
    "com.apple.developer.networking.networkextension": ["packet-tunnel-provider"],
    "com.apple.security.application-groups": ["group.io.yaver.mesh"],
  },
});
