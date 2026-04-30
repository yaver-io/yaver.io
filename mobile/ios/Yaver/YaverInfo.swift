import Foundation
import React

@objc(YaverInfo)
final class YaverInfo: NSObject {

  @objc static func requiresMainQueueSetup() -> Bool { false }

  @objc func constantsToExport() -> [AnyHashable: Any] {
    var availableModules: [String] = []
    if let modules = SDKManifest.shared.raw["nativeModules"] as? [String: String] {
      availableModules = Array(modules.keys).sorted()
    }
    let runtimeFamilies: [[String: Any]] = SDKManifest.shared.runtimeFamilies.map { family in
      var payload: [String: Any] = [
        "id": family.id,
        "label": family.label,
        "sdkVersion": family.sdkVersion ?? "",
        "expoVersion": family.expoVersion ?? "",
        "reactNativeVersion": family.reactNativeVersion ?? "",
        "reactVersion": family.reactVersion ?? "",
        "hermesVersion": family.hermesVersion ?? "",
        "hermesBCVersion": family.hermesBCVersion ?? 0,
        "supportedRNRange": family.supportedRNRange ?? "",
      ]
      payload["compiledIn"] = family.compiledIn ?? false
      payload["status"] = family.status ?? ""
      payload["manifestResource"] = family.manifestResource ?? ""
      payload["packageRoot"] = family.packageRoot ?? ""
      payload["preferredPackageNames"] = family.preferredPackageNames ?? []
      return payload
    }
    return [
      "isYaver": true,
      "version": (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "",
      "build": (Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String) ?? "",
      "sdkVersion": SDKManifest.shared.sdkVersion ?? "",
      "hermesBCVersion": Int(SDKManifest.shared.hermesBytecodeVersion),
      "runtimeFamilies": runtimeFamilies,
      "currentRuntimeFamilyId": UserDefaults.standard.string(forKey: "yaverSelectedRuntimeFamilyID")
        ?? SDKManifest.shared.defaultRuntimeFamilyID,
      "defaultRuntimeFamilyId": SDKManifest.shared.defaultRuntimeFamilyID,
      // Guest bundles run inside Yaver's own super-host, not their original app
      // container. Startup code that assumes project-specific entitlements or
      // push-token wiring should opt out when these flags are true.
      "guestSafeMode": true,
      "suppressPushNotifications": true,
      "suppressLocalizationProbe": true,
      "availableModules": availableModules,
      "lastGuestCrashReport": YaverGuestCrashReporter.loadLastCrashReport() ?? NSNull(),
    ]
  }

  @objc func getLastGuestCrashReport(
    _ resolve: RCTPromiseResolveBlock,
    rejecter reject: RCTPromiseRejectBlock
  ) {
    resolve(YaverGuestCrashReporter.loadLastCrashReport() ?? NSNull())
  }

  @objc func clearLastGuestCrashReport(
    _ resolve: RCTPromiseResolveBlock,
    rejecter reject: RCTPromiseRejectBlock
  ) {
    resolve(YaverGuestCrashReporter.clearLastCrashReport())
  }
}
