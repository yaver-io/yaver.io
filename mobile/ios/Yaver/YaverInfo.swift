import Foundation

@objc(YaverInfo)
final class YaverInfo: NSObject {

  @objc static func requiresMainQueueSetup() -> Bool { false }

  @objc func constantsToExport() -> [AnyHashable: Any] {
    var availableModules: [String] = []
    if let modules = SDKManifest.shared.raw["nativeModules"] as? [String: String] {
      availableModules = Array(modules.keys).sorted()
    }
    return [
      "isYaver": true,
      "version": (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "",
      "build": (Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String) ?? "",
      "sdkVersion": SDKManifest.shared.sdkVersion ?? "",
      "hermesBCVersion": Int(SDKManifest.shared.hermesBytecodeVersion),
      // Guest bundles run inside Yaver's own super-host, not their original app
      // container. Startup code that assumes project-specific entitlements or
      // push-token wiring should opt out when these flags are true.
      "guestSafeMode": true,
      "suppressPushNotifications": true,
      "suppressLocalizationProbe": true,
      "availableModules": availableModules,
    ]
  }
}
