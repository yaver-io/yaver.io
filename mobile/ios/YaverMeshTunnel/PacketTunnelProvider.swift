// PacketTunnelProvider.swift — Yaver Mesh on-device tunnel (iOS extension).
//
// STAGE-A STUB (no WireGuardKit). Lives in the COMMITTED iOS project (force-
// tracked like the other mobile/ios/Yaver*.swift overlays) so it survives
// `expo prebuild --clean` + `git checkout -- mobile/ios/`. The YaverMeshTunnel
// app-extension target is added to the committed project.pbxproj by
// scripts/add-mesh-ios-target.js (mesh can't ride @bacons/apple-targets here —
// that regenerates the pbxproj, which the checkout discards; see
// docs/mesh-mobile-tunnel.md).
//
// Stage A validates the target builds + signs + the Network Extension
// capability auto-registers (GATE 1). Stage B swaps this for the real
// WireGuardKit implementation in mobile/native-mesh/ios/PacketTunnelProvider.swift.

import NetworkExtension
import os

class PacketTunnelProvider: NEPacketTunnelProvider {
    override func startTunnel(options: [String: NSObject]?,
                             completionHandler: @escaping (Error?) -> Void) {
        os_log("yaver-mesh: stub startTunnel (WireGuardKit not yet wired)")
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
        setTunnelNetworkSettings(settings) { error in
            completionHandler(error)
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason,
                            completionHandler: @escaping () -> Void) {
        completionHandler()
    }
}
