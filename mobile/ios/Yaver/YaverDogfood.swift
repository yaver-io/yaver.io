import Foundation
import React
import UIKit

/// YaverDogfood — host-side screenshot auto-catch for the "improve Yaver with
/// Yaver" dogfood loop. When dogfood mode is on, JS calls `start()`; from then
/// on every time the user takes a screenshot anywhere in the Yaver app we
/// re-render the key window to a JPEG and emit `onDogfoodScreenshot` so the
/// Dogfood thread can stage it for annotation + prompt + dispatch.
///
/// Why re-render the window instead of reading the OS screenshot file: the OS
/// never hands the app the screenshot bitmap, and reading it back from Photos
/// would need a Photos permission (expo-media-library isn't even installed).
/// Re-rendering the window needs no permission, is instant, captures the exact
/// app UI, and lets the JS side run a blur/redact pass before anything leaves
/// the device (screenshots are P2P-only, never Convex — privacy contract).
///
/// JS side (mobile/src/lib/dogfoodCapture.ts):
///
///   const emitter = new NativeEventEmitter(NativeModules.YaverDogfood)
///   emitter.addListener("onDogfoodScreenshot", (ev) => stage(ev))
///   await NativeModules.YaverDogfood.start()
///   …
///   await NativeModules.YaverDogfood.stop()
@objc(YaverDogfood)
class YaverDogfood: RCTEventEmitter {

  private var observing = false
  private var screenshotObserver: NSObjectProtocol?
  /// Current expo-router path, pushed from JS via setRoute so the dispatched
  /// prompt can say which screen the user was on. Best-effort label only.
  private var currentRoute: String = ""

  override static func requiresMainQueueSetup() -> Bool { return true }

  override func supportedEvents() -> [String]! {
    return ["onDogfoodScreenshot"]
  }

  // RCTEventEmitter lifecycle — JS (de)registers listeners. We key the OS
  // observer off explicit start()/stop() instead so the listener only runs
  // while dogfood mode is actually enabled, not merely while a listener exists.
  override func startObserving() {}
  override func stopObserving() {}

  @objc func start(_ resolve: @escaping RCTPromiseResolveBlock,
                   rejecter reject: @escaping RCTPromiseRejectBlock) {
    DispatchQueue.main.async {
      if self.observing {
        resolve(["alreadyObserving": true])
        return
      }
      self.observing = true
      self.screenshotObserver = NotificationCenter.default.addObserver(
        forName: UIApplication.userDidTakeScreenshotNotification,
        object: nil,
        queue: .main
      ) { [weak self] _ in
        self?.handleScreenshotTaken()
      }
      resolve(["alreadyObserving": false])
    }
  }

  @objc func stop(_ resolve: @escaping RCTPromiseResolveBlock,
                  rejecter reject: @escaping RCTPromiseRejectBlock) {
    DispatchQueue.main.async {
      if let obs = self.screenshotObserver {
        NotificationCenter.default.removeObserver(obs)
        self.screenshotObserver = nil
      }
      self.observing = false
      resolve(nil)
    }
  }

  @objc func isObserving(_ resolve: @escaping RCTPromiseResolveBlock,
                         rejecter reject: @escaping RCTPromiseRejectBlock) {
    resolve(observing)
  }

  /// JS pushes the active route on navigation so the screenshot payload can
  /// carry "which screen". Empty string clears it.
  @objc func setRoute(_ route: String) {
    currentRoute = route.trimmingCharacters(in: .whitespacesAndNewlines)
  }

  // MARK: - Capture

  private func handleScreenshotTaken() {
    guard observing else { return }
    // Already on main (queue: .main above), but be defensive — UIKit only.
    guard let image = captureKeyWindow() else { return }
    guard let data = image.jpegData(compressionQuality: 0.85) else { return }

    let takenAt = Date().timeIntervalSince1970 * 1000
    let path = dogfoodImagePath(stamp: Int(takenAt))
    do {
      try data.write(to: URL(fileURLWithPath: path), options: .atomic)
    } catch {
      return
    }

    var body: [String: Any] = [
      "path": path,
      "takenAt": takenAt,
    ]
    if !currentRoute.isEmpty { body["route"] = currentRoute }
    sendEvent(withName: "onDogfoodScreenshot", body: body)
  }

  private func captureKeyWindow() -> UIImage? {
    guard let window = activeKeyWindow() else { return nil }
    let renderer = UIGraphicsImageRenderer(bounds: window.bounds)
    return renderer.image { _ in
      // afterScreenUpdates:false — capture the frame as it was when the user
      // pressed the buttons, not after a relayout flash.
      window.drawHierarchy(in: window.bounds, afterScreenUpdates: false)
    }
  }

  private func activeKeyWindow() -> UIWindow? {
    let scenes = UIApplication.shared.connectedScenes
      .compactMap { $0 as? UIWindowScene }
      .filter { $0.activationState == .foregroundActive }
    let windows = scenes.flatMap { $0.windows }
    return windows.first { $0.isKeyWindow } ?? windows.first
  }

  private func dogfoodImagePath(stamp: Int) -> String {
    let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first!
    let dir = caches.appendingPathComponent("dogfood", isDirectory: true)
    if !FileManager.default.fileExists(atPath: dir.path) {
      try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }
    return dir.appendingPathComponent("shot-\(stamp).jpg").path
  }
}
