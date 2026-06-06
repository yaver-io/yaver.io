// YaverMeshModule.swift — RN bridge for the on-device Yaver Mesh tunnel (iOS).
//
// REFERENCE IMPLEMENTATION. Backs `NativeModules.YaverMesh` consumed by
// src/lib/yaverMesh.ts. NOT yet wired into the Xcode project — the
// `mobile/plugins/withMeshTunnel.js` config plugin adds this file to the app
// target and the PacketTunnelProvider to the extension target. Requires the
// Network Extension entitlement + WireGuardKit (see docs/mesh-mobile-tunnel.md).
//
// Identity contract: the Curve25519 PRIVATE key is generated here and stored in
// the Keychain — it never crosses the RN bridge. JS only ever sees the public
// key (and a deviceId derived from it). When starting the tunnel, JS passes a
// wg-quick config with `PrivateKey = __KEYCHAIN__`; this module substitutes the
// real key before handing the config to NETunnelProviderManager.

import Foundation
import NetworkExtension
import WireGuardKit

@objc(YaverMesh)
class YaverMeshModule: NSObject {

  private let keychainTag = "io.yaver.mesh.privatekey"
  private let tunnelBundleId = "io.yaver.mobile.YaverMeshTunnel"

  @objc static func requiresMainQueueSetup() -> Bool { false }

  // MARK: keypair (private key stays in Keychain)

  @objc(ensureKeyPair:rejecter:)
  func ensureKeyPair(_ resolve: @escaping RCTPromiseResolveBlock,
                     rejecter reject: @escaping RCTPromiseRejectBlock) {
    let priv = loadOrCreatePrivateKey()
    resolve(priv.publicKey.base64Key)
  }

  private func loadOrCreatePrivateKey() -> PrivateKey {
    if let data = keychainRead(keychainTag), let key = PrivateKey(rawValue: data) {
      return key
    }
    let key = PrivateKey()
    keychainWrite(keychainTag, key.rawValue)
    return key
  }

  // MARK: tunnel lifecycle

  @objc(up:resolver:rejecter:)
  func up(_ wgQuickConfig: String,
          resolver resolve: @escaping RCTPromiseResolveBlock,
          rejecter reject: @escaping RCTPromiseRejectBlock) {
    let priv = loadOrCreatePrivateKey()
    let resolved = wgQuickConfig.replacingOccurrences(of: "__KEYCHAIN__", with: priv.base64Key)

    NETunnelProviderManager.loadAllFromPreferences { managers, error in
      if let error = error { reject("load", error.localizedDescription, error); return }
      let manager = managers?.first ?? NETunnelProviderManager()
      let proto = NETunnelProviderProtocol()
      proto.providerBundleIdentifier = self.tunnelBundleId
      proto.serverAddress = "Yaver Mesh"
      proto.providerConfiguration = ["wgQuickConfig": resolved]
      manager.protocolConfiguration = proto
      manager.localizedDescription = "Yaver Mesh"
      manager.isEnabled = true
      manager.saveToPreferences { saveErr in
        if let saveErr = saveErr { reject("save", saveErr.localizedDescription, saveErr); return }
        manager.loadFromPreferences { _ in
          do {
            try manager.connection.startVPNTunnel()
            resolve(nil)
          } catch {
            reject("start", error.localizedDescription, error)
          }
        }
      }
    }
  }

  @objc(reconfigure:resolver:rejecter:)
  func reconfigure(_ wgQuickConfig: String,
                   resolver resolve: @escaping RCTPromiseResolveBlock,
                   rejecter reject: @escaping RCTPromiseRejectBlock) {
    let priv = loadOrCreatePrivateKey()
    let resolved = wgQuickConfig.replacingOccurrences(of: "__KEYCHAIN__", with: priv.base64Key)
    NETunnelProviderManager.loadAllFromPreferences { managers, _ in
      guard let session = managers?.first?.connection as? NETunnelProviderSession else {
        resolve(nil); return
      }
      try? session.sendProviderMessage(Data(resolved.utf8)) { _ in resolve(nil) }
    }
  }

  @objc(down:rejecter:)
  func down(_ resolve: @escaping RCTPromiseResolveBlock,
            rejecter reject: @escaping RCTPromiseRejectBlock) {
    NETunnelProviderManager.loadAllFromPreferences { managers, _ in
      managers?.first?.connection.stopVPNTunnel()
      resolve(nil)
    }
  }

  @objc(status:rejecter:)
  func status(_ resolve: @escaping RCTPromiseResolveBlock,
              rejecter reject: @escaping RCTPromiseRejectBlock) {
    NETunnelProviderManager.loadAllFromPreferences { managers, _ in
      let state: String
      switch managers?.first?.connection.status {
      case .some(.connected): state = "connected"
      case .some(.connecting), .some(.reasserting): state = "connecting"
      case .some(.disconnecting), .some(.disconnected), .some(.invalid), .none: state = "disconnected"
      @unknown default: state = "disconnected"
      }
      resolve(["state": state])
    }
  }

  // MARK: Keychain helpers

  private func keychainWrite(_ tag: String, _ data: Data) {
    let query: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrAccount as String: tag,
    ]
    SecItemDelete(query as CFDictionary)
    var add = query
    add[kSecValueData as String] = data
    add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
    SecItemAdd(add as CFDictionary, nil)
  }

  private func keychainRead(_ tag: String) -> Data? {
    let query: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrAccount as String: tag,
      kSecReturnData as String: true,
      kSecMatchLimit as String: kSecMatchLimitOne,
    ]
    var out: AnyObject?
    guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess else { return nil }
    return out as? Data
  }
}
