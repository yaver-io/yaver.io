import Foundation
import React
import UIKit

/**
 * Hot reload native module for the Yaver Feedback SDK.
 *
 * Downloads a Hermes bytecode bundle from the agent, saves it to Documents,
 * and posts a notification that the AppDelegate handler (injected by the Expo
 * config plugin) catches to recreate the RN bridge with the new bundle.
 *
 * Supports N reloads — each reload tears down the old bridge and creates
 * a fresh one pointing to the updated bundle file.
 */
@objc(YaverHotReload)
class YaverHotReload: NSObject {

  static let bundleDir = "yaver-hot-reload"
  static let bundleFile = "main.jsbundle"
  static let reloadNotification = Notification.Name("YaverHotReloadBundle")

  override static func requiresMainQueueSetup() -> Bool { return true }

  /// Download a Hermes bundle from the agent and trigger a bridge reload.
  @objc func loadBundle(_ urlString: String,
                        headers: NSDictionary?,
                        resolver resolve: @escaping RCTPromiseResolveBlock,
                        rejecter reject: @escaping RCTPromiseRejectBlock) {
    guard let bundleURL = URL(string: urlString) else {
      reject("INVALID_URL", "Invalid bundle URL", nil); return
    }

    var request = URLRequest(url: bundleURL)
    request.timeoutInterval = 60
    if let headers = headers as? [String: String] {
      for (key, value) in headers { request.setValue(value, forHTTPHeaderField: key) }
    }

    NSLog("[YaverHotReload] downloading bundle from %@...", urlString)
    URLSession.shared.dataTask(with: request) { data, response, error in
      if let error = error {
        reject("DOWNLOAD_FAILED", error.localizedDescription, error); return
      }
      guard let data = data, data.count > 0 else {
        reject("EMPTY_BUNDLE", "Empty bundle response", nil); return
      }
      guard let http = response as? HTTPURLResponse, http.statusCode == 200 else {
        let code = (response as? HTTPURLResponse)?.statusCode ?? 0
        reject("HTTP_ERROR", "Status \(code)", nil); return
      }

      // Basic Hermes bytecode validation
      if data.count >= 12 {
        let magic: UInt32 = data.withUnsafeBytes { $0.load(fromByteOffset: 4, as: UInt32.self) }
        if magic == 0x1F1903C1 {
          let bcVersion: UInt32 = data.withUnsafeBytes { $0.load(fromByteOffset: 8, as: UInt32.self) }
          NSLog("[YaverHotReload] Hermes bytecode BC%d, %d bytes", bcVersion, data.count)
        } else {
          NSLog("[YaverHotReload] WARNING: not Hermes bytecode (magic=0x%08X)", magic)
        }
      }

      do {
        let savePath = YaverHotReload.savedBundlePath()
        let dir = savePath.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try data.write(to: savePath, options: .atomic)

        NSLog("[YaverHotReload] saved %d bytes, posting reload notification", data.count)
        resolve(["loaded": true, "size": data.count])

        // Post notification — AppDelegate handler recreates the bridge
        DispatchQueue.main.async {
          NotificationCenter.default.post(
            name: YaverHotReload.reloadNotification,
            object: nil,
            userInfo: ["bundlePath": savePath.path]
          )
        }
      } catch {
        reject("SAVE_FAILED", error.localizedDescription, error)
      }
    }.resume()
  }

  @objc func hasBundle(_ resolve: @escaping RCTPromiseResolveBlock,
                       rejecter reject: @escaping RCTPromiseRejectBlock) {
    resolve(FileManager.default.fileExists(atPath: YaverHotReload.savedBundlePath().path))
  }

  @objc func clearBundle(_ resolve: @escaping RCTPromiseResolveBlock,
                         rejecter reject: @escaping RCTPromiseRejectBlock) {
    let dir = YaverHotReload.savedBundlePath().deletingLastPathComponent()
    try? FileManager.default.removeItem(at: dir)
    resolve(true)
  }

  // MARK: - Static helpers

  static func savedBundlePath() -> URL {
    let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
    return docs
      .appendingPathComponent(bundleDir, isDirectory: true)
      .appendingPathComponent(bundleFile)
  }

  /// Returns the hot-reloaded bundle URL if one exists on disk.
  @objc static func bundleURL() -> URL? {
    let path = savedBundlePath()
    return FileManager.default.fileExists(atPath: path.path) ? path : nil
  }
}
