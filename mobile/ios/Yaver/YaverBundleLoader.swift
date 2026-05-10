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
    YaverGuestCrashReporter.markGuestPhase("bundle_download_requested", moduleName: moduleName, sourceURL: urlString)

    guard let bundleURL = URL(string: urlString) else {
      NSLog("[YaverBundleLoader] INVALID_URL: %@", urlString)
      YaverGuestCrashReporter.recordGuestFailure(
        phase: "bundle_invalid_url",
        message: "Invalid guest bundle URL: \(urlString)",
        moduleName: moduleName,
        sourceURL: urlString
      )
      sendEvent(withName: "onBundleError", body: ["code": "INVALID_URL", "message": "Invalid bundle URL: \(urlString)"])
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
        YaverGuestCrashReporter.recordGuestFailure(
          phase: "bundle_download_failed",
          message: "Downloading the guest bundle failed: \(error.localizedDescription)",
          moduleName: moduleName,
          sourceURL: urlString
        )
        self.sendEvent(withName: "onBundleError", body: ["code": "DOWNLOAD_FAILED", "message": error.localizedDescription])
        reject("DOWNLOAD_FAILED", error.localizedDescription, error); return
      }
      guard let data = data, data.count > 0 else {
        NSLog("[YaverBundleLoader] EMPTY_BUNDLE from %@", urlString)
        YaverGuestCrashReporter.recordGuestFailure(
          phase: "bundle_download_empty",
          message: "Downloading the guest bundle returned no bytes.",
          moduleName: moduleName,
          sourceURL: urlString
        )
        self.sendEvent(withName: "onBundleError", body: ["code": "EMPTY_BUNDLE", "message": "Empty bundle"])
        reject("EMPTY_BUNDLE", "Empty bundle", nil); return
      }
      guard let http = response as? HTTPURLResponse, http.statusCode == 200 else {
        let code = (response as? HTTPURLResponse)?.statusCode ?? 0
        NSLog("[YaverBundleLoader] HTTP_ERROR: status=%d", code)
        YaverGuestCrashReporter.recordGuestFailure(
          phase: "bundle_download_http_error",
          message: "Downloading the guest bundle returned HTTP \(code).",
          moduleName: moduleName,
          sourceURL: urlString
        )
        self.sendEvent(withName: "onBundleError", body: ["code": "HTTP_ERROR", "message": "Status \(code)"])
        reject("HTTP_ERROR", "Status \(code)", nil); return
      }

      NSLog("[YaverBundleLoader] downloaded %d bytes", data.count)
      YaverGuestCrashReporter.markGuestPhase("bundle_downloaded", moduleName: moduleName, sourceURL: urlString)

      // Parse bundle metadata from response header (if agent sent it)
      let metaHeader = http.value(forHTTPHeaderField: "X-Yaver-Bundle-Metadata")
      var bundleMeta: BundleMetadata?

      if let metaStr = metaHeader, let metaData = metaStr.data(using: .utf8) {
        bundleMeta = try? JSONDecoder().decode(BundleMetadata.self, from: metaData)
        if let m = bundleMeta {
          NSLog("[YaverBundleLoader] metadata: size=%lld md5=%@ BC%d module=%@ format=%@",
                m.size, m.md5, m.hermesBCVersion, m.moduleName, m.format)
          if let family = m.runtimeFamilySelection?.selected {
            NSLog("[YaverBundleLoader] runtime family: id=%@ label=%@ exact=%@ supported=%@",
                  family.id, family.label,
                  (m.runtimeFamilySelection?.exactMatch ?? false) ? "YES" : "NO",
                  m.runtimeFamilySelection?.supportedHint ?? "")
          }

          // Pre-validate metadata (catches BC mismatch before we even look at bytes)
          if let metaErr = YaverBundleValidator.validateMetadata(m) {
            NSLog("[YaverBundleLoader] metadata rejected: %@", metaErr.localizedDescription)
            YaverGuestCrashReporter.recordGuestFailure(
              phase: "bundle_metadata_rejected",
              message: metaErr.localizedDescription,
              moduleName: moduleName,
              sourceURL: urlString
            )
            self.sendEvent(withName: "onBundleError", body: ["code": metaErr.code, "message": metaErr.localizedDescription])
            reject(metaErr.code, metaErr.localizedDescription, nil); return
          }

          // Full bundle validation (size + MD5 + magic + BC)
          if let bundleErr = YaverBundleValidator.validateBundle(data: data, metadata: m) {
            NSLog("[YaverBundleLoader] bundle validation FAILED: %@", bundleErr.localizedDescription)
            YaverGuestCrashReporter.recordGuestFailure(
              phase: "bundle_validation_failed",
              message: bundleErr.localizedDescription,
              moduleName: moduleName,
              sourceURL: urlString
            )
            self.sendEvent(withName: "onBundleError", body: ["code": bundleErr.code, "message": bundleErr.localizedDescription])
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
              YaverGuestCrashReporter.recordGuestFailure(
                phase: "bundle_bc_mismatch",
                message: "Hermes BC\(bcVersion) != expected BC\(expectedBC)",
                moduleName: moduleName,
                sourceURL: urlString
              )
              self.sendEvent(withName: "onBundleError", body: ["code": "BC_VERSION_MISMATCH", "message": "Hermes BC\(bcVersion) != expected BC\(expectedBC)"])
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
        YaverGuestCrashReporter.markGuestPhase(
          "bundle_saved",
          moduleName: moduleName,
          sourceURL: urlString,
          bundlePath: savePath.path
        )

        let localMeta = try JSONSerialization.data(withJSONObject: [
          "moduleName": moduleName, "sourceUrl": urlString, "size": data.count,
          "md5": bundleMeta?.md5 ?? "", "bcVersion": bundleMeta?.hermesBCVersion ?? 0,
          "runtimeFamilyId": bundleMeta?.runtimeFamilySelection?.selected.id ?? SDKManifest.shared.defaultRuntimeFamilyID,
          "runtimeFamilyLabel": bundleMeta?.runtimeFamilySelection?.selected.label ?? "",
          "runtimeFamilyExactMatch": bundleMeta?.runtimeFamilySelection?.exactMatch ?? true
        ] as [String: Any])
        try localMeta.write(to: dir.appendingPathComponent("metadata.json"), options: .atomic)

        UserDefaults.standard.set(moduleName, forKey: "yaverLoadedModuleName")
        UserDefaults.standard.set(
          bundleMeta?.runtimeFamilySelection?.selected.id ?? SDKManifest.shared.defaultRuntimeFamilyID,
          forKey: "yaverSelectedRuntimeFamilyID")
        UserDefaults.standard.set(
          bundleMeta?.runtimeFamilySelection?.selected.label ?? "",
          forKey: "yaverSelectedRuntimeFamilyLabel")
        // Persist md5 so JS-side loadAppIfChanged can short-circuit a
        // reload when the agent reports the new bundle has the same
        // hash as the one already running. Empty string when the agent
        // didn't send the metadata header (legacy fallback path) — the
        // JS guard treats empty as "don't skip".
        UserDefaults.standard.set(bundleMeta?.md5 ?? "", forKey: "yaverLoadedBundleMd5")

        // Store agent base URL + auth token so AppDelegate / native panes
        // (YaverFeedbackPane, YaverAgentsPane) can call agent endpoints.
        // PRESERVE the relay-routing path prefix `/d/<deviceId>` when the
        // bundle came from a relay-proxied URL — without it, calls to
        // `<base>/runner-auth/status` etc. land on the relay's root and
        // get back "subdomain 'public' not registered" because the relay
        // doesn't know which agent to forward to.
        //
        // Strip only the trailing `/yaver/main.jsbundle`-style path tail
        // (the bundle file path), keeping everything up to and including
        // the device-routing segment. For direct URLs (no /d/<id>),
        // scheme://host[:port] is what we want anyway — same outcome.
        if let parsed = URL(string: urlString),
           let scheme = parsed.scheme,
           let host = parsed.host {
          var baseURL = "\(scheme)://\(host)"
          if let port = parsed.port { baseURL += ":\(port)" }
          // Walk up the path one segment at a time, dropping the bundle
          // file but keeping the routing prefix. Bundle URLs look like:
          //   https://public.yaver.io/d/<deviceId>/yaver/main.jsbundle
          //   https://public.yaver.io/d/<deviceId>/dev/native-bundle
          //   http://192.168.1.42:18080/dev/native-bundle  (direct)
          // We want everything up to (and including) the last segment
          // BEFORE /yaver/, /dev/, etc. — for the relay case that's
          // `/d/<deviceId>`; for direct it's empty (root).
          let path = parsed.path
          if !path.isEmpty {
            // Find the LAST occurrence of "/yaver/" or "/dev/" or "/info"
            // in the path and trim from there.
            var trimmed = path
            for marker in ["/yaver/", "/dev/", "/info"] {
              if let r = trimmed.range(of: marker) {
                trimmed = String(trimmed[..<r.lowerBound])
                break
              }
            }
            // Defensive: don't end up with a trailing slash.
            while trimmed.hasSuffix("/") { trimmed.removeLast() }
            if !trimmed.isEmpty { baseURL += trimmed }

            // Also extract the deviceId from a /d/<deviceId> prefix
            // and persist it. yaverResolveAgentURL needs it to
            // recover when the persisted yaverAgentBaseURL turns out
            // to be a bare relay host (e.g. some upstream rewrote it,
            // or a non-relay direct URL was cached and we later
            // started routing via relay). Without this, deviceId
            // stayed empty in UserDefaults and the helper printed
            // requests to "https://public.yaver.io/<path>" instead of
            // "https://public.yaver.io/d/<deviceId>/<path>" — exactly
            // the case the user keeps hitting "subdomain 'public'
            // not registered" on.
            let trimmedNoLeading = trimmed.hasPrefix("/") ? String(trimmed.dropFirst()) : trimmed
            let segments = trimmedNoLeading.split(separator: "/", omittingEmptySubsequences: true)
            if segments.count >= 2 && segments[0] == "d" {
              let deviceId = String(segments[1])
              if !deviceId.isEmpty {
                UserDefaults.standard.set(deviceId, forKey: "yaverInheritedDeviceId")
                NSLog("[YaverBundleLoader] yaverInheritedDeviceId = \(deviceId)")
              }
            }
          }
          UserDefaults.standard.set(baseURL, forKey: "yaverAgentBaseURL")
          NSLog("[YaverBundleLoader] yaverAgentBaseURL = \(baseURL)")
        }
        if let headers = headers as? [String: String],
           let auth = headers["Authorization"] ?? headers["authorization"] {
          UserDefaults.standard.set(auth, forKey: "yaverAgentAuth")
        }

        resolve([
          "loaded": true,
          "url": urlString,
          "size": data.count,
          "runtimeFamilyId": bundleMeta?.runtimeFamilySelection?.selected.id ?? SDKManifest.shared.defaultRuntimeFamilyID,
        ])
        self.sendEvent(withName: "onBundleLoaded", body: [
          "url": urlString,
          "moduleName": moduleName,
          "size": data.count,
          "runtimeFamilyId": bundleMeta?.runtimeFamilySelection?.selected.id ?? SDKManifest.shared.defaultRuntimeFamilyID,
          "runtimeFamilyLabel": bundleMeta?.runtimeFamilySelection?.selected.label ?? "",
        ])

        NSLog("[YaverBundleLoader] posting reload notification: moduleName=%@", moduleName)
        DispatchQueue.main.async {
          NotificationCenter.default.post(name: YaverBundleLoader.reloadNotification, object: nil,
                                          userInfo: ["moduleName": moduleName])
        }
      } catch {
        NSLog("[YaverBundleLoader] SAVE_FAILED: %@", error.localizedDescription)
        YaverGuestCrashReporter.recordGuestFailure(
          phase: "bundle_save_failed",
          message: "Saving the guest bundle failed: \(error.localizedDescription)",
          moduleName: moduleName,
          sourceURL: urlString
        )
        self.sendEvent(withName: "onBundleError", body: ["code": "SAVE_FAILED", "message": error.localizedDescription])
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
    UserDefaults.standard.removeObject(forKey: "yaverSelectedRuntimeFamilyID")
    UserDefaults.standard.removeObject(forKey: "yaverSelectedRuntimeFamilyLabel")
    UserDefaults.standard.removeObject(forKey: "yaverLoadedBundleMd5")
    YaverGuestCrashReporter.clearGuestSession()
    resolve(["unloaded": true])
    sendEvent(withName: "onBundleUnloaded", body: ["unloaded": true])
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

  /// Returns the md5 of the currently-loaded bundle (as reported by
  /// the agent in X-Yaver-Bundle-Metadata at load time), or "" if no
  /// bundle is loaded / the agent didn't send a hash. JS uses this to
  /// short-circuit a reload when the freshly-built bundle has the same
  /// hash as the one already running.
  @objc func getLoadedBundleMd5(_ resolve: @escaping RCTPromiseResolveBlock,
                                rejecter reject: @escaping RCTPromiseRejectBlock) {
    resolve(UserDefaults.standard.string(forKey: "yaverLoadedBundleMd5") ?? "")
  }

  /// Set the tablet phone-frame flag. When `true`, the next guest
  /// bundle mount on iPad wraps the guest in an iPhone-shaped frame
  /// with a vibe dock alongside (right pane in landscape, bottom
  /// strip in portrait). Default false — phones always ignore this
  /// flag (see YaverFramedHost.applyIfNeeded for the device-class
  /// guard). Persisted via UserDefaults so the choice survives bundle
  /// reloads and app restarts.
  @objc func setPhoneFrame(_ enabled: Bool,
                           resolver resolve: @escaping RCTPromiseResolveBlock,
                           rejecter reject: @escaping RCTPromiseRejectBlock) {
    UserDefaults.standard.set(enabled, forKey: "yaverGuestPhoneFrame")
    NSLog("[YaverBundleLoader] yaverGuestPhoneFrame = \(enabled)")
    resolve(["enabled": enabled])
  }

  /// Read the current phone-frame flag. Returns `false` when the key
  /// has never been set, matching `UserDefaults.bool` semantics.
  @objc func getPhoneFrame(_ resolve: @escaping RCTPromiseResolveBlock,
                           rejecter reject: @escaping RCTPromiseRejectBlock) {
    resolve(["enabled": UserDefaults.standard.bool(forKey: "yaverGuestPhoneFrame")])
  }

  /// Static, instance-free, bridge-free counterpart to `loadBundle`.
  /// Callable from any Swift code (native panes, AppDelegate, …)
  /// without needing an `RCTBridge` reference or a `YaverBundleLoader`
  /// instance — solves the "no live bridge to swap" failure on
  /// Bridgeless / RCTHost (the New Architecture path Expo's
  /// `ReactAppDependencyProvider` uses) where there is no
  /// `RCTRootView` and `bridge.module(for:)` simply does not exist.
  ///
  /// Mirrors the same pipeline as the instance `loadBundle`:
  ///   1. URLSession download with the supplied headers.
  ///   2. HBC magic + BC version validation (legacy fallback path —
  ///      no metadata header is required from the caller).
  ///   3. Save to `<docs>/bundles/main.jsbundle`.
  ///   4. Persist `yaverLoadedModuleName` + `yaverAgentBaseURL` + auth
  ///      header in UserDefaults so the next pane / overlay flow has
  ///      what it needs.
  ///   5. Post `reloadNotification` — AppDelegate handles it the same
  ///      way it does for the JS-driven path, invalidating the
  ///      current host bridge and recreating it with the freshly
  ///      saved bundle.
  ///
  /// `completion` fires on the main queue with `nil` on success, an
  /// error message on failure. Callers use it to advance their UI
  /// state (e.g. the feedback overlay narrating "downloaded —
  /// swapping app").
  @objc static func swap(url urlString: String,
                         moduleName: String,
                         headers: [String: String]?,
                         completion: @escaping (String?) -> Void) {
    NSLog("[YaverBundleLoader] swap (static) called: url=%@ moduleName=%@", urlString, moduleName)
    YaverGuestCrashReporter.markGuestPhase("bundle_download_requested",
                                           moduleName: moduleName,
                                           sourceURL: urlString)
    guard let bundleURL = URL(string: urlString) else {
      DispatchQueue.main.async { completion("Invalid bundle URL: \(urlString)") }
      return
    }
    var request = URLRequest(url: bundleURL)
    request.timeoutInterval = 60
    if let headers = headers {
      for (k, v) in headers { request.setValue(v, forHTTPHeaderField: k) }
    }
    URLSession.shared.dataTask(with: request) { data, response, error in
      if let error = error {
        NSLog("[YaverBundleLoader] swap DOWNLOAD_FAILED: %@", error.localizedDescription)
        DispatchQueue.main.async { completion("Download failed: \(error.localizedDescription)") }
        return
      }
      guard let data = data, !data.isEmpty else {
        DispatchQueue.main.async { completion("Empty bundle response") }
        return
      }
      if let http = response as? HTTPURLResponse, http.statusCode != 200 {
        DispatchQueue.main.async { completion("HTTP \(http.statusCode)") }
        return
      }
      // Lightweight magic + BC validation (skips the X-Yaver-Bundle-Metadata
      // path the JS-driven loadBundle uses; the agent always sends
      // valid HBC, and the metadata header is mostly belt-and-braces).
      if data.count >= 12 {
        let magic: UInt32 = data.withUnsafeBytes { $0.load(fromByteOffset: 4, as: UInt32.self) }
        let bcVersion: UInt32 = data.withUnsafeBytes { $0.load(fromByteOffset: 8, as: UInt32.self) }
        let expectedBC = SDKManifest.shared.hermesBytecodeVersion
        if magic == 0x1F1903C1 {
          if expectedBC > 0 && bcVersion != expectedBC {
            let msg = "Hermes BC\(bcVersion) != expected BC\(expectedBC)"
            NSLog("[YaverBundleLoader] swap BC_MISMATCH: %@", msg)
            DispatchQueue.main.async { completion(msg) }
            return
          }
        } else {
          NSLog("[YaverBundleLoader] swap: plain JS bundle (not HBC) — proceeding anyway")
        }
      }
      do {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
        let dir = docs.appendingPathComponent("bundles", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let savePath = dir.appendingPathComponent("main.jsbundle")
        try data.write(to: savePath, options: .atomic)
        UserDefaults.standard.set(moduleName, forKey: "yaverLoadedModuleName")
        // swap() doesn't parse X-Yaver-Bundle-Metadata, so we don't
        // know the new md5. Clear any stale persisted hash so the
        // next loadAppIfChanged falls through to a full reload
        // instead of false-skipping against a different project's md5.
        UserDefaults.standard.removeObject(forKey: "yaverLoadedBundleMd5")
        // Persist agent base URL + auth header for any subsequent
        // pane that needs them. Same logic as the instance loadBundle
        // (lines ~196-251) — keeps the relay routing prefix.
        if let parsed = URL(string: urlString),
           let scheme = parsed.scheme,
           let host = parsed.host {
          var baseURL = "\(scheme)://\(host)"
          if let port = parsed.port { baseURL += ":\(port)" }
          var trimmed = parsed.path
          for marker in ["/yaver/", "/dev/", "/info"] {
            if let r = trimmed.range(of: marker) {
              trimmed = String(trimmed[..<r.lowerBound])
              break
            }
          }
          while trimmed.hasSuffix("/") { trimmed.removeLast() }
          if !trimmed.isEmpty { baseURL += trimmed }
          UserDefaults.standard.set(baseURL, forKey: "yaverAgentBaseURL")
          let trimmedNoLeading = trimmed.hasPrefix("/") ? String(trimmed.dropFirst()) : trimmed
          let segments = trimmedNoLeading.split(separator: "/", omittingEmptySubsequences: true)
          if segments.count >= 2 && segments[0] == "d" {
            let deviceId = String(segments[1])
            if !deviceId.isEmpty {
              UserDefaults.standard.set(deviceId, forKey: "yaverInheritedDeviceId")
            }
          }
        }
        if let auth = headers?["Authorization"] ?? headers?["authorization"] {
          UserDefaults.standard.set(auth, forKey: "yaverAgentAuth")
        }
        NSLog("[YaverBundleLoader] swap saved bundle (%d bytes); posting reloadNotification", data.count)
        DispatchQueue.main.async {
          NotificationCenter.default.post(name: YaverBundleLoader.reloadNotification, object: nil,
                                          userInfo: ["moduleName": moduleName])
          completion(nil)
        }
      } catch {
        DispatchQueue.main.async { completion("Save failed: \(error.localizedDescription)") }
      }
    }.resume()
  }
}
