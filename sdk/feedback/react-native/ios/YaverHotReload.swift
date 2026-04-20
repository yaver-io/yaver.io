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

  // `requiresMainQueueSetup` is an RCTBridgeModule protocol method,
  // not an NSObject method — so it must not be marked `override`.
  // Modern React Native discovers it via the Objective-C runtime
  // (the Swift-generated ObjC interface plus the .m file's
  // RCT_EXPORT_MODULE macro). Marking it `override` errors with
  // "does not override any method from its superclass" on Swift 5+.
  @objc static func requiresMainQueueSetup() -> Bool { return true }

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

  /// Returns the hot-reloaded bundle URL if one exists AND hasn't
  /// crashed on boot N times in a row.
  ///
  /// Vibe-coding loop: a developer pushes dozens of Hermes bundles over
  /// the course of a session; eventually one of them crashes on boot
  /// (missing native module, syntax error, TurboModule assert). Without
  /// this guard, the crashing bundle persists across cold starts and
  /// bricks the app — user has to delete + reinstall from TestFlight.
  ///
  /// Guard: on each cold-start bundleURL() call, increment a boot
  /// counter. Reset it on first successful render (RCTContentDidAppear)
  /// or after 10 s of uptime. If the counter hits kMaxBootAttempts
  /// without being reset, the bundle has crashed every boot so far —
  /// delete it and return nil so the app falls back to the
  /// TestFlight-installed bundle.
  ///
  /// Bundle mtime is tracked separately: pushing a NEW bundle resets
  /// the counter so each pushed bundle gets its own 3 attempts.
  @objc static func bundleURL() -> URL? {
    let path = savedBundlePath()
    guard FileManager.default.fileExists(atPath: path.path) else { return nil }

    let defaults = UserDefaults.standard
    let attrs = try? FileManager.default.attributesOfItem(atPath: path.path)
    let currentMtime = (attrs?[.modificationDate] as? Date)?.timeIntervalSince1970 ?? 0
    let lastMtime = defaults.double(forKey: kKeyBundleMtime)

    // Fresh bundle pushed since the counter was last reset → start over.
    if currentMtime != lastMtime {
      defaults.set(0, forKey: kKeyBootAttempts)
      defaults.set(currentMtime, forKey: kKeyBundleMtime)
    }

    let attempts = defaults.integer(forKey: kKeyBootAttempts)
    if attempts >= kMaxBootAttempts {
      NSLog("[YaverHotReload] hot bundle failed %d consecutive boot attempts — reverting to app-bundled TestFlight bundle.", attempts)
      let dir = path.deletingLastPathComponent()
      try? FileManager.default.removeItem(at: dir)
      defaults.removeObject(forKey: kKeyBootAttempts)
      defaults.removeObject(forKey: kKeyBundleMtime)
      return nil
    }

    // Pre-increment: this boot will count as a failure unless the JS
    // side reaches first render and calls markBootSuccessful().
    defaults.set(attempts + 1, forKey: kKeyBootAttempts)
    NSLog("[YaverHotReload] loading hot bundle (boot attempt %d/%d)", attempts + 1, kMaxBootAttempts)
    return path
  }

  /// Clear the boot-attempt counter. Call this from AppDelegate after
  /// first successful RN render (RCTContentDidAppearNotification) or
  /// after a short uptime safety timer — whichever fires first.
  @objc static func markBootSuccessful() {
    let defaults = UserDefaults.standard
    if defaults.integer(forKey: kKeyBootAttempts) > 0 {
      NSLog("[YaverHotReload] boot confirmed successful — reset boot-attempt counter.")
    }
    defaults.set(0, forKey: kKeyBootAttempts)
  }

  static let kKeyBootAttempts = "yaverHotReloadBootAttempts"
  static let kKeyBundleMtime = "yaverHotReloadBundleMtime"
  static let kMaxBootAttempts = 3
}
