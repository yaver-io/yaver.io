import Foundation
import React
import UIKit

/// Native module that loads external React Native JS bundles into a secondary RCTBridge.
/// This is the core of the "super host" feature — like Expo Go, but inside Yaver.
///
/// Flow:
/// 1. JS calls loadBundle(url, moduleName)
/// 2. We create a secondary RCTBridge pointed at the Metro bundle URL
/// 3. We present an RCTRootView full-screen on top of the Yaver UI
/// 4. The loaded app runs with access to all native modules compiled into Yaver
/// 5. HMR works through the same URL (Metro WebSocket at /hot)
/// 6. JS calls unloadBundle() to tear down and return to Yaver
@objc(YaverBundleLoader)
class YaverBundleLoader: RCTEventEmitter {

  private var loadedBridge: RCTBridge?
  private var loadedViewController: UIViewController?
  private var overlayWindow: UIWindow?

  override static func requiresMainQueueSetup() -> Bool { return true }

  override func supportedEvents() -> [String]! {
    return ["onBundleLoaded", "onBundleError", "onBundleUnloaded"]
  }

  /// Load an external JS bundle and present it full-screen.
  @objc func loadBundle(_ urlString: String,
                        moduleName: String,
                        resolver resolve: @escaping RCTPromiseResolveBlock,
                        rejecter reject: @escaping RCTPromiseRejectBlock) {
    DispatchQueue.main.async { [weak self] in
      guard let self = self else { return }

      // Tear down any existing loaded app
      self.tearDown()

      guard let bundleURL = URL(string: urlString) else {
        reject("INVALID_URL", "Invalid bundle URL: \(urlString)", nil)
        return
      }

      // Create a secondary RCTBridge with the remote bundle URL.
      // This bridge auto-discovers all native modules registered in the binary
      // (camera, BLE, GPS, etc.) — same as the primary Yaver bridge.
      let bridge = RCTBridge(
        bundleURL: bundleURL,
        moduleProvider: nil,
        launchOptions: nil
      )

      guard let bridge = bridge else {
        reject("BRIDGE_FAILED", "Failed to create RCTBridge", nil)
        return
      }

      self.loadedBridge = bridge

      // Create the root view for the loaded app
      let rootView = RCTRootView(
        bridge: bridge,
        moduleName: moduleName,
        initialProperties: nil
      )
      rootView.backgroundColor = UIColor.black

      // Create a view controller to present the loaded app
      let vc = UIViewController()
      vc.view = rootView
      vc.modalPresentationStyle = .fullScreen

      // Add a floating "Back to Yaver" button
      let backButton = self.createBackButton()
      vc.view.addSubview(backButton)

      // Present on top of the current UI
      guard let rootVC = self.topViewController() else {
        reject("NO_ROOT_VC", "Could not find root view controller", nil)
        return
      }

      self.loadedViewController = vc

      rootVC.present(vc, animated: true) {
        self.sendEvent(withName: "onBundleLoaded", body: [
          "url": urlString,
          "moduleName": moduleName
        ])
        resolve(["loaded": true, "url": urlString])
      }
    }
  }

  /// Unload the current external bundle and return to Yaver.
  @objc func unloadBundle(_ resolve: @escaping RCTPromiseResolveBlock,
                          rejecter reject: @escaping RCTPromiseRejectBlock) {
    DispatchQueue.main.async { [weak self] in
      self?.tearDown()
      self?.sendEvent(withName: "onBundleUnloaded", body: nil)
      resolve(["unloaded": true])
    }
  }

  /// Returns the list of native modules available in this binary.
  /// Used for compatibility checking before loading a bundle.
  @objc func getAvailableModules(_ resolve: @escaping RCTPromiseResolveBlock,
                                 rejecter reject: @escaping RCTPromiseRejectBlock) {
    // RCTModuleClasses() returns all registered native module classes
    var modules: [String] = []

    // Known modules we ship (hardcoded for reliability)
    let knownModules = [
      "expo-camera", "expo-location", "expo-sensors", "expo-haptics",
      "expo-brightness", "expo-battery", "expo-device", "expo-constants",
      "expo-barcode-scanner", "expo-notifications", "expo-file-system",
      "expo-asset", "expo-font", "expo-clipboard", "expo-linking",
      "expo-secure-store", "expo-av", "expo-image-picker", "expo-speech",
      "expo-web-browser", "expo-apple-authentication",
      "react-native-maps", "react-native-ble-plx",
      "react-native-reanimated", "react-native-gesture-handler",
      "react-native-screens", "react-native-safe-area-context",
      "react-native-webview", "@react-native-async-storage/async-storage",
      "@react-native-community/netinfo"
    ]
    modules.append(contentsOf: knownModules)

    resolve(modules)
  }

  /// Check if a bundle is currently loaded.
  @objc func isLoaded(_ resolve: @escaping RCTPromiseResolveBlock,
                      rejecter reject: @escaping RCTPromiseRejectBlock) {
    resolve(["loaded": self.loadedBridge != nil])
  }

  // MARK: - Private

  private func tearDown() {
    loadedViewController?.dismiss(animated: true)
    loadedViewController = nil
    loadedBridge?.invalidate()
    loadedBridge = nil
    overlayWindow?.isHidden = true
    overlayWindow = nil
  }

  private func createBackButton() -> UIButton {
    let button = UIButton(type: .system)
    button.setTitle("Back to Yaver", for: .normal)
    button.titleLabel?.font = UIFont.boldSystemFont(ofSize: 13)
    button.setTitleColor(.white, for: .normal)
    button.backgroundColor = UIColor(red: 0.1, green: 0.1, blue: 0.15, alpha: 0.9)
    button.layer.cornerRadius = 16
    button.contentEdgeInsets = UIEdgeInsets(top: 8, left: 16, bottom: 8, right: 16)
    button.addTarget(self, action: #selector(backButtonTapped), for: .touchUpInside)

    // Position at top-left with safe area
    button.translatesAutoresizingMaskIntoConstraints = false

    // We'll add constraints after it's added to the view
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
      if let superview = button.superview {
        NSLayoutConstraint.activate([
          button.topAnchor.constraint(equalTo: superview.safeAreaLayoutGuide.topAnchor, constant: 8),
          button.leadingAnchor.constraint(equalTo: superview.safeAreaLayoutGuide.leadingAnchor, constant: 12),
        ])
        superview.bringSubviewToFront(button)
      }
    }

    return button
  }

  @objc private func backButtonTapped() {
    tearDown()
    sendEvent(withName: "onBundleUnloaded", body: nil)
  }

  private func topViewController() -> UIViewController? {
    var vc = UIApplication.shared.connectedScenes
      .compactMap { $0 as? UIWindowScene }
      .flatMap { $0.windows }
      .first(where: { $0.isKeyWindow })?
      .rootViewController

    while let presented = vc?.presentedViewController {
      vc = presented
    }
    return vc
  }
}
