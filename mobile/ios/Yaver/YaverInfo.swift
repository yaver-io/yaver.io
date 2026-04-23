import Foundation

@objc(YaverInfo)
final class YaverInfo: NSObject {

  @objc static func requiresMainQueueSetup() -> Bool { false }

  @objc func constantsToExport() -> [AnyHashable: Any] {
    return [
      "isYaver": true,
      "version": (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "",
      "build": (Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String) ?? "",
    ]
  }
}
