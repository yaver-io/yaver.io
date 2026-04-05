import Foundation
import React
import UIKit

@objc(YaverBundleLoader)
class YaverBundleLoader: RCTEventEmitter {

  static let reloadNotification = Notification.Name("YaverBundleLoaderReload")
  static let restoreNotification = Notification.Name("YaverBundleLoaderRestore")

  override static func requiresMainQueueSetup() -> Bool { return true }
  override func supportedEvents() -> [String]! {
    return ["onBundleLoaded", "onBundleError", "onBundleUnloaded"]
  }
  override func startObserving() {}
  override func stopObserving() {}

  @objc func loadBundle(_ urlString: String,
                        moduleName: String,
                        headers: NSDictionary?,
                        resolver resolve: @escaping RCTPromiseResolveBlock,
                        rejecter reject: @escaping RCTPromiseRejectBlock) {
    guard let bundleURL = URL(string: urlString) else {
      reject("INVALID_URL", "Invalid bundle URL: \(urlString)", nil)
      return
    }

    var request = URLRequest(url: bundleURL)
    request.timeoutInterval = 60
    if let headers = headers as? [String: String] {
      for (key, value) in headers { request.setValue(value, forHTTPHeaderField: key) }
    }

    URLSession.shared.dataTask(with: request) { data, response, error in
      if let error = error {
        reject("DOWNLOAD_FAILED", error.localizedDescription, error); return
      }
      guard let data = data, data.count > 0 else {
        reject("EMPTY_BUNDLE", "Empty bundle", nil); return
      }
      guard let http = response as? HTTPURLResponse, http.statusCode == 200 else {
        reject("HTTP_ERROR", "Status \((response as? HTTPURLResponse)?.statusCode ?? 0)", nil); return
      }

      do {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
        let dir = docs.appendingPathComponent("bundles", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try data.write(to: dir.appendingPathComponent("main.jsbundle"), options: .atomic)

        let meta = try JSONSerialization.data(withJSONObject: [
          "moduleName": moduleName, "sourceUrl": urlString, "size": data.count
        ] as [String: Any])
        try meta.write(to: dir.appendingPathComponent("metadata.json"), options: .atomic)

        UserDefaults.standard.set(moduleName, forKey: "yaverLoadedModuleName")
        resolve(["loaded": true, "url": urlString, "size": data.count])

        DispatchQueue.main.async {
          NotificationCenter.default.post(name: YaverBundleLoader.reloadNotification, object: nil,
                                          userInfo: ["moduleName": moduleName])
        }
      } catch {
        reject("SAVE_FAILED", error.localizedDescription, error)
      }
    }.resume()
  }

  @objc func unloadBundle(_ resolve: @escaping RCTPromiseResolveBlock,
                          rejecter reject: @escaping RCTPromiseRejectBlock) {
    let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
    let dir = docs.appendingPathComponent("bundles", isDirectory: true)
    try? FileManager.default.removeItem(at: dir.appendingPathComponent("main.jsbundle"))
    try? FileManager.default.removeItem(at: dir.appendingPathComponent("metadata.json"))
    UserDefaults.standard.removeObject(forKey: "yaverLoadedModuleName")
    resolve(["unloaded": true])
    DispatchQueue.main.async {
      NotificationCenter.default.post(name: YaverBundleLoader.restoreNotification, object: nil)
    }
  }

  @objc func getAvailableModules(_ resolve: @escaping RCTPromiseResolveBlock,
                                 rejecter reject: @escaping RCTPromiseRejectBlock) {
    resolve(["expo-camera","expo-location","expo-sensors","expo-haptics","expo-device",
             "expo-constants","expo-notifications","expo-file-system","expo-asset","expo-font",
             "expo-clipboard","expo-linking","expo-secure-store","expo-av","expo-image-picker",
             "expo-speech","expo-web-browser","expo-apple-authentication",
             "react-native-reanimated","react-native-gesture-handler","react-native-screens",
             "react-native-safe-area-context","react-native-webview",
             "@react-native-async-storage/async-storage","@react-native-community/netinfo"])
  }

  @objc func isLoaded(_ resolve: @escaping RCTPromiseResolveBlock,
                      rejecter reject: @escaping RCTPromiseRejectBlock) {
    let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
    resolve(["loaded": FileManager.default.fileExists(atPath: docs.appendingPathComponent("bundles/main.jsbundle").path)])
  }
}
