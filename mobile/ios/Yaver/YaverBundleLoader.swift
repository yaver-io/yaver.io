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
    NSLog("[YaverBundleLoader] loadBundle called: url=%@ moduleName=%@", urlString, moduleName)

    guard let bundleURL = URL(string: urlString) else {
      NSLog("[YaverBundleLoader] INVALID_URL: %@", urlString)
      reject("INVALID_URL", "Invalid bundle URL: \(urlString)", nil)
      return
    }

    var request = URLRequest(url: bundleURL)
    request.timeoutInterval = 60
    if let headers = headers as? [String: String] {
      for (key, value) in headers { request.setValue(value, forHTTPHeaderField: key) }
    }

    NSLog("[YaverBundleLoader] downloading bundle from %@...", urlString)
    URLSession.shared.dataTask(with: request) { data, response, error in
      if let error = error {
        NSLog("[YaverBundleLoader] DOWNLOAD_FAILED: %@", error.localizedDescription)
        reject("DOWNLOAD_FAILED", error.localizedDescription, error); return
      }
      guard let data = data, data.count > 0 else {
        NSLog("[YaverBundleLoader] EMPTY_BUNDLE from %@", urlString)
        reject("EMPTY_BUNDLE", "Empty bundle", nil); return
      }
      guard let http = response as? HTTPURLResponse, http.statusCode == 200 else {
        let code = (response as? HTTPURLResponse)?.statusCode ?? 0
        NSLog("[YaverBundleLoader] HTTP_ERROR: status=%d", code)
        reject("HTTP_ERROR", "Status \(code)", nil); return
      }

      NSLog("[YaverBundleLoader] downloaded %d bytes", data.count)

      // Validate Hermes bytecode
      // HBC format: magic at offset 4, BC version at offset 8
      if data.count >= 12 {
        let magic: UInt32 = data.withUnsafeBytes { $0.load(fromByteOffset: 4, as: UInt32.self) }
        let bcVersion: UInt32 = data.withUnsafeBytes { $0.load(fromByteOffset: 8, as: UInt32.self) }
        let expectedBC = SDKManifest.shared.hermesBytecodeVersion

        if magic == 0x1F1903C1 {
          NSLog("[YaverBundleLoader] Hermes bytecode: magic=0x%08X BC=%d expectedBC=%d match=%@",
                magic, bcVersion, expectedBC, bcVersion == expectedBC ? "YES" : "NO")

          if expectedBC > 0 && bcVersion != expectedBC {
            let msg = "Hermes BC\(bcVersion) != expected BC\(expectedBC). Update yaver agent or app."
            NSLog("[YaverBundleLoader] BC_MISMATCH: %@", msg)
            reject("BC_MISMATCH", msg, nil)
            return
          }
        } else {
          NSLog("[YaverBundleLoader] plain JS bundle (magic=0x%08X, not Hermes)", magic)
        }
      }

      do {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
        let dir = docs.appendingPathComponent("bundles", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let savePath = dir.appendingPathComponent("main.jsbundle")
        try data.write(to: savePath, options: .atomic)
        NSLog("[YaverBundleLoader] saved bundle to %@, size=%d", savePath.path, data.count)

        let meta = try JSONSerialization.data(withJSONObject: [
          "moduleName": moduleName, "sourceUrl": urlString, "size": data.count
        ] as [String: Any])
        try meta.write(to: dir.appendingPathComponent("metadata.json"), options: .atomic)

        UserDefaults.standard.set(moduleName, forKey: "yaverLoadedModuleName")
        resolve(["loaded": true, "url": urlString, "size": data.count])

        NSLog("[YaverBundleLoader] posting reload notification: moduleName=%@", moduleName)
        DispatchQueue.main.async {
          NotificationCenter.default.post(name: YaverBundleLoader.reloadNotification, object: nil,
                                          userInfo: ["moduleName": moduleName])
        }
      } catch {
        NSLog("[YaverBundleLoader] SAVE_FAILED: %@", error.localizedDescription)
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
