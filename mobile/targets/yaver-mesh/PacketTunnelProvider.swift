// PacketTunnelProvider.swift — Yaver Mesh on-device WireGuard tunnel (iOS).
//
// REFERENCE IMPLEMENTATION. This is the NEPacketTunnelProvider that runs in the
// Network Extension target. It is NOT yet wired into the Xcode project — see
// docs/mesh-mobile-tunnel.md for the entitlement + config-plugin steps required
// before it compiles. It uses WireGuardKit (github.com/WireGuard/wireguard-apple),
// added as a SwiftPM/pod dependency by the config plugin.
//
// Lives under mobile/native-mesh/ (tracked) rather than mobile/ios/ because
// `expo prebuild --clean` regenerates the iOS project; the config plugin copies
// this file into the generated YaverMeshTunnel extension target.

import NetworkExtension
import WireGuardKit
import os

class PacketTunnelProvider: NEPacketTunnelProvider {
    private lazy var adapter: WireGuardAdapter = {
        WireGuardAdapter(with: self) { _, message in
            os_log("yaver-mesh: %{public}@", message)
        }
    }()

    override func startTunnel(options: [String: NSObject]?,
                              completionHandler: @escaping (Error?) -> Void) {
        // The app writes the wg-quick-style config into the provider protocol's
        // providerConfiguration["wgQuickConfig"] when starting the tunnel. That
        // config is built from GET /mesh/peers (one [Peer] per mesh node) plus
        // this device's [Interface] (private key from Keychain, overlay IP from
        // mesh:joinMeshWeb).
        guard
            let proto = protocolConfiguration as? NETunnelProviderProtocol,
            let providerConfig = proto.providerConfiguration,
            let wgQuickConfig = providerConfig["wgQuickConfig"] as? String,
            let tunnelConfiguration = try? TunnelConfiguration(fromWgQuickConfig: wgQuickConfig, called: "yaver-mesh")
        else {
            completionHandler(NEVPNError(.configurationInvalid))
            return
        }

        adapter.start(tunnelConfiguration: tunnelConfiguration) { adapterError in
            if let adapterError = adapterError {
                os_log("yaver-mesh: adapter start failed: %{public}@", "\(adapterError)")
                completionHandler(adapterError)
                return
            }
            completionHandler(nil)
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason,
                             completionHandler: @escaping () -> Void) {
        adapter.stop { _ in completionHandler() }
    }

    // Live reconfigure (peers joined/left, ACLs changed) without dropping the
    // tunnel: the app sends an updated wg-quick config as an app message.
    override func handleAppMessage(_ messageData: Data,
                                   completionHandler: ((Data?) -> Void)?) {
        guard
            let cfg = String(data: messageData, encoding: .utf8),
            let tunnelConfiguration = try? TunnelConfiguration(fromWgQuickConfig: cfg, called: "yaver-mesh")
        else {
            completionHandler?(nil)
            return
        }
        adapter.update(tunnelConfiguration: tunnelConfiguration) { _ in
            completionHandler?(Data([1]))
        }
    }
}
