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

      // Parse bundle metadata from response header (if agent sent it)
      let metaHeader = http.value(forHTTPHeaderField: "X-Yaver-Bundle-Metadata")
      var bundleMeta: BundleMetadata?

      if let metaStr = metaHeader, let metaData = metaStr.data(using: .utf8) {
        bundleMeta = try? JSONDecoder().decode(BundleMetadata.self, from: metaData)
        if let m = bundleMeta {
          NSLog("[YaverBundleLoader] metadata: size=%lld md5=%@ BC%d module=%@ format=%@",
                m.size, m.md5, m.hermesBCVersion, m.moduleName, m.format)

          // Pre-validate metadata (catches BC mismatch before we even look at bytes)
          if let metaErr = YaverBundleValidator.validateMetadata(m) {
            NSLog("[YaverBundleLoader] metadata rejected: %@", metaErr.localizedDescription)
            reject(metaErr.code, metaErr.localizedDescription, nil); return
          }

          // Full bundle validation (size + MD5 + magic + BC)
          if let bundleErr = YaverBundleValidator.validateBundle(data: data, metadata: m) {
            NSLog("[YaverBundleLoader] bundle validation FAILED: %@", bundleErr.localizedDescription)
            reject(bundleErr.code, bundleErr.localizedDescription, nil); return
          }
          NSLog("[YaverBundleLoader] bundle validated: size match, MD5 match, BC%d", m.hermesBCVersion)
        }
      } else {
        NSLog("[YaverBundleLoader] no X-Yaver-Bundle-Metadata header — agent may be outdated, skipping integrity checks")
        // Legacy fallback: basic magic + BC check
        if data.count >= 12 {
          let magic: UInt32 = data.withUnsafeBytes { $0.load(fromByteOffset: 4, as: UInt32.self) }
          let bcVersion: UInt32 = data.withUnsafeBytes { $0.load(fromByteOffset: 8, as: UInt32.self) }
          let expectedBC = SDKManifest.shared.hermesBytecodeVersion
          if magic == 0x1F1903C1 {
            NSLog("[YaverBundleLoader] Hermes BC=%d expectedBC=%d", bcVersion, expectedBC)
            if expectedBC > 0 && bcVersion != expectedBC {
              reject("BC_VERSION_MISMATCH",
                     "Hermes BC\(bcVersion) != expected BC\(expectedBC)", nil); return
            }
          } else {
            NSLog("[YaverBundleLoader] plain JS bundle (not HBC) — may crash on Release bridge")
          }
        }
      }

      do {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
        let dir = docs.appendingPathComponent("bundles", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let savePath = dir.appendingPathComponent("main.jsbundle")
        try data.write(to: savePath, options: .atomic)
        NSLog("[YaverBundleLoader] saved bundle to %@, size=%d", savePath.path, data.count)

        let localMeta = try JSONSerialization.data(withJSONObject: [
          "moduleName": moduleName, "sourceUrl": urlString, "size": data.count,
          "md5": bundleMeta?.md5 ?? "", "bcVersion": bundleMeta?.hermesBCVersion ?? 0
        ] as [String: Any])
        try localMeta.write(to: dir.appendingPathComponent("metadata.json"), options: .atomic)

        UserDefaults.standard.set(moduleName, forKey: "yaverLoadedModuleName")

        // Store agent base URL + auth token so AppDelegate can call /dev/stop
        // when user taps "Back to Yaver" from the shake overlay.
        if let parsed = URL(string: urlString),
           let scheme = parsed.scheme,
           let host = parsed.host {
          var baseURL = "\(scheme)://\(host)"
          if let port = parsed.port { baseURL += ":\(port)" }
          UserDefaults.standard.set(baseURL, forKey: "yaverAgentBaseURL")
        }
        if let headers = headers as? [String: String],
           let auth = headers["Authorization"] ?? headers["authorization"] {
          UserDefaults.standard.set(auth, forKey: "yaverAgentAuth")
        }

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
    let manifest = SDKManifest.shared.raw
    if let modules = manifest["nativeModules"] as? [String: String] {
      resolve(Array(modules.keys).sorted())
    } else {
      resolve([])
    }
  }

  @objc func isLoaded(_ resolve: @escaping RCTPromiseResolveBlock,
                      rejecter reject: @escaping RCTPromiseRejectBlock) {
    let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
    resolve(["loaded": FileManager.default.fileExists(atPath: docs.appendingPathComponent("bundles/main.jsbundle").path)])
  }
}
