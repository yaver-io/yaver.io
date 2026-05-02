import Expo
import React
import ReactAppDependencyProvider

@UIApplicationMain
public class AppDelegate: ExpoAppDelegate {
  var window: UIWindow?

  var reactNativeDelegate: ReactNativeDelegate?
  var reactNativeFactory: RCTReactNativeFactory?
  private var isReloading = false

  public override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
  ) -> Bool {
    YaverGuestCrashReporter.recoverCrashIfNeeded()

    // Clean up any stale guest bundle from previous sessions so Yaver's own UI loads
    let docsPath = NSSearchPathForDirectoriesInDomains(.documentDirectory, .userDomainMask, true).first!
    let staleBundlePath = (docsPath as NSString).appendingPathComponent("bundles/main.jsbundle")
    if FileManager.default.fileExists(atPath: staleBundlePath) {
      NSLog("[AppDelegate] Cleaning stale guest bundle at startup")
      try? FileManager.default.removeItem(atPath: staleBundlePath)
    }
    UserDefaults.standard.removeObject(forKey: "yaverLoadedModuleName")
    UserDefaults.standard.removeObject(forKey: "yaverCurrentModuleName")
    UserDefaults.standard.removeObject(forKey: "yaverSelectedRuntimeFamilyID")
    UserDefaults.standard.removeObject(forKey: "yaverSelectedRuntimeFamilyLabel")

    let delegate = ReactNativeDelegate()
    let factory = ExpoReactNativeFactory(delegate: delegate)
    delegate.dependencyProvider = RCTAppDependencyProvider()

    reactNativeDelegate = delegate
    reactNativeFactory = factory
    bindReactNativeFactory(factory)

    // Listen for bundle reload/restore notifications from YaverBundleLoader
    NotificationCenter.default.addObserver(
      self, selector: #selector(handleBundleReload(_:)),
      name: Notification.Name("YaverBundleLoaderReload"), object: nil)
    NotificationCenter.default.addObserver(
      self, selector: #selector(handleBundleRestore(_:)),
      name: Notification.Name("YaverBundleLoaderRestore"), object: nil)

    // Listen for JS load success/failure on ANY bridge (guest or Yaver's own)
    NotificationCenter.default.addObserver(
      forName: NSNotification.Name("RCTJavaScriptDidLoad"),
      object: nil, queue: .main
    ) { _ in
      NSLog("[AppDelegate] JS loaded successfully")
      if UserDefaults.standard.string(forKey: "yaverCurrentModuleName") != nil
        || UserDefaults.standard.string(forKey: "yaverLoadedModuleName") != nil
      {
        YaverGuestCrashReporter.markGuestPhase("javascript_loaded")
      }
    }
    NotificationCenter.default.addObserver(
      self, selector: #selector(handleJSLoadFailure(_:)),
      name: NSNotification.Name("RCTJavaScriptDidFailToLoad"), object: nil)

    // Start the on-device HTTP server for yaver push
    YaverHTTPServer.shared.onBundleReceived = { [weak self] in
      self?.safeReloadBridge()
    }
    YaverHTTPServer.shared.start()

#if os(iOS) || os(tvOS)
    window = ShakeDetectingWindow(frame: UIScreen.main.bounds)
    factory.startReactNative(
      withModuleName: "main",
      in: window,
      launchOptions: launchOptions)
#endif

    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  @objc func handleBundleReload(_ notification: Notification) {
    let moduleName = (notification.userInfo?["moduleName"] as? String) ?? "main"
    NSLog("[AppDelegate] handleBundleReload: moduleName=%@", moduleName)
    YaverGuestCrashReporter.markGuestPhase("reload_requested", moduleName: moduleName)
    safeReloadBridge(moduleName: moduleName)
  }

  @objc func handleJSLoadFailure(_ notification: Notification) {
    let error = (notification.userInfo?["error"] as? Error)?.localizedDescription ?? "unknown"
    NSLog("[AppDelegate] JS LOAD FAILED: %@", error)
    YaverGuestCrashReporter.recordGuestFailure(
      phase: "javascript_failed_to_load",
      message: "JavaScript failed to load: \(error)",
      moduleName: UserDefaults.standard.string(forKey: "yaverCurrentModuleName")
    )

    // Only show error screen if we're loading a guest app (not Yaver's own bundle)
    let isGuestBundle = UserDefaults.standard.string(forKey: "yaverCurrentModuleName") != nil
      || UserDefaults.standard.string(forKey: "yaverLoadedModuleName") != nil
    if isGuestBundle, let window = self.window {
      showGuestErrorScreen(message: "JavaScript failed to load: \(error)", window: window)
    }
  }

  /// Safely tears down the old bridge and creates a new one with the downloaded bundle.
  /// Uses weak-reference polling to wait for actual bridge deallocation instead of a fixed sleep.
  private func safeReloadBridge(moduleName: String? = nil) {
    guard Thread.isMainThread else {
      DispatchQueue.main.async { self.safeReloadBridge(moduleName: moduleName) }
      return
    }
    guard !isReloading else {
      NSLog("[AppDelegate] safeReloadBridge: already reloading, skipping")
      return
    }
    isReloading = true
    YaverGuestCrashReporter.markGuestPhase("bridge_reload_preparing", moduleName: moduleName)

    let docsPath = NSSearchPathForDirectoriesInDomains(.documentDirectory, .userDomainMask, true).first!
    let downloadedBundle = (docsPath as NSString).appendingPathComponent("bundles/main.jsbundle")

    guard FileManager.default.fileExists(atPath: downloadedBundle) else {
      NSLog("[AppDelegate] ERROR: Downloaded bundle not found at %@", downloadedBundle)
      isReloading = false
      return
    }

    let resolvedModule = moduleName
      ?? UserDefaults.standard.string(forKey: "yaverCurrentModuleName")
      ?? "main"
    let bundleURL = URL(fileURLWithPath: downloadedBundle)
    let selectedFamily = resolveSelectedRuntimeFamily()

    NSLog("[AppDelegate] safeReloadBridge: bundleURL=%@ moduleName=%@", bundleURL.path, resolvedModule)
    NSLog("[AppDelegate] safeReloadBridge: runtimeFamily=%@ label=%@",
          selectedFamily.id, selectedFamily.label)
    YaverGuestCrashReporter.markGuestPhase(
      "bridge_reload_ready",
      moduleName: resolvedModule,
      bundlePath: bundleURL.path
    )

    // Log bundle info for debugging
    // HBC format: magic at offset 4, BC version at offset 8
    if let data = try? Data(contentsOf: bundleURL) {
      NSLog("[AppDelegate] Bundle size: %d bytes", data.count)
      if data.count >= 12 {
        let magic: UInt32 = data.withUnsafeBytes { $0.load(fromByteOffset: 4, as: UInt32.self) }
        let bcVersion: UInt32 = data.withUnsafeBytes { $0.load(fromByteOffset: 8, as: UInt32.self) }
        let expectedBC = SDKManifest.shared.hermesBytecodeVersion
        if magic == 0x1F1903C1 {
          NSLog("[AppDelegate] Hermes BC=%d expectedBC=%d match=%@", bcVersion, expectedBC,
                bcVersion == expectedBC ? "YES" : "NO")
        } else {
          NSLog("[AppDelegate] Plain JS bundle (magic=0x%08X at offset 4, not Hermes)", magic)
        }
      }
    }

    // 1. Capture the existing bridge BEFORE swapping the root view.
    // The previous code replaced the root view with the placeholder first,
    // then tried to cast that placeholder back to RCTRootView, which meant
    // the old bridge was never invalidated at all.
    let existingRootView = window?.rootViewController?.view as? RCTRootView
    let existingBridge = existingRootView?.bridge

    // 2. Show spinner (stops JS rendering)
    let placeholder = UIView(frame: window?.bounds ?? .zero)
    placeholder.backgroundColor = .systemBackground
    let spinner = UIActivityIndicatorView(style: .large)
    spinner.center = placeholder.center
    spinner.startAnimating()
    placeholder.addSubview(spinner)
    window?.rootViewController?.view = placeholder

    // 3. Capture weak reference to old bridge before invalidating
    var oldBridgeWeak: RCTBridge? = nil
    if let bridge = existingBridge {
      oldBridgeWeak = bridge
      NSLog("[AppDelegate] invalidating old bridge...")
      YaverGuestCrashReporter.markGuestPhase(
        "bridge_invalidating_old",
        moduleName: resolvedModule,
        bundlePath: bundleURL.path
      )
      bridge.invalidate()
    } else {
      NSLog("[AppDelegate] no existing RCTRootView — creating fresh bridge")
    }

    // 4. Wait for actual deallocation using polling (replaces fixed sleep)
    waitForBridgeDeallocation(bridge: oldBridgeWeak, timeout: 3.0) { [weak self] in
      guard let self = self, let window = self.window else { return }
      self.initGuestBridge(bundleURL: bundleURL, moduleName: resolvedModule, runtimeFamily: selectedFamily, window: window)
    }
    oldBridgeWeak = nil // release the last strong ref after scheduling the weak poll
  }

  /// Polls until the weak reference goes nil (bridge deallocated),
  /// then calls completion on the main thread. Falls back to timeout.
  private func waitForBridgeDeallocation(
    bridge: RCTBridge?,
    timeout: TimeInterval,
    completion: @escaping () -> Void
  ) {
    weak var weakBridge = bridge
    // If no old bridge, proceed immediately
    guard weakBridge != nil else {
      NSLog("[AppDelegate] no old bridge to wait for — proceeding immediately")
      DispatchQueue.main.async { completion() }
      return
    }

    let deadline = Date().addingTimeInterval(timeout)
    let checkInterval: TimeInterval = 0.05

    func check() {
      if weakBridge == nil {
        NSLog("[AppDelegate] old bridge deallocated — creating new bridge")
        DispatchQueue.main.async { completion() }
        return
      }
      if Date() > deadline {
        NSLog("[AppDelegate] WARNING: bridge deallocation timeout after %.1fs — proceeding anyway", timeout)
        DispatchQueue.main.async { completion() }
        return
      }
      DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + checkInterval) {
        check()
      }
    }

    DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + checkInterval) {
      check()
    }
  }

  /// Creates the guest bridge using ExpoReactNativeFactory (New Architecture) so
  /// TurboModules like PlatformConstants are available to the guest app.
  private func initGuestBridge(bundleURL: URL, moduleName: String, runtimeFamily: RuntimeFamily, window: UIWindow) {
    NSLog("[AppDelegate] creating guest bridge: family=%@ label=%@ url=%@ module=%@",
          runtimeFamily.id, runtimeFamily.label, bundleURL.path, moduleName)
    YaverGuestCrashReporter.markGuestPhase(
      "bridge_starting_guest",
      moduleName: moduleName,
      bundlePath: bundleURL.path
    )

    guard SDKManifest.shared.supportsRuntimeFamily(id: runtimeFamily.id) else {
      let message = "Runtime family \(runtimeFamily.id) is not compiled into this Yaver build."
      NSLog("[AppDelegate] %@", message)
      YaverGuestCrashReporter.recordGuestFailure(
        phase: "runtime_family_unsupported",
        message: message,
        moduleName: moduleName,
        bundlePath: bundleURL.path
      )
      showGuestErrorScreen(message: message, window: window)
      isReloading = false
      return
    }

    let delegate = ReactNativeDelegate()
    delegate.overrideBundleURL = bundleURL
    delegate.dependencyProvider = RCTAppDependencyProvider()

    let factory = ExpoReactNativeFactory(delegate: delegate)
    self.reactNativeDelegate = delegate
    self.reactNativeFactory = factory
    self.bindReactNativeFactory(factory)

    UserDefaults.standard.set(moduleName, forKey: "yaverCurrentModuleName")
    UserDefaults.standard.set(runtimeFamily.id, forKey: "yaverSelectedRuntimeFamilyID")
    UserDefaults.standard.set(runtimeFamily.label, forKey: "yaverSelectedRuntimeFamilyLabel")

    factory.startReactNative(
      withModuleName: moduleName,
      in: window,
      launchOptions: nil)

    isReloading = false
    NSLog("[AppDelegate] guest app loaded (New Arch): module=%@", moduleName)
    YaverGuestCrashReporter.markGuestPhase(
      "bridge_started_guest",
      moduleName: moduleName,
      bundlePath: bundleURL.path
    )

    // Guest app is running. Shake phone to reveal "Back to Yaver" overlay.
    isGuestAppRunning = true
  }

  private var isGuestAppRunning = false
  private var backOverlay: UIView?
  private var overlayDismissTimer: Timer?

  /// Exposed so ShakeDetectingWindow can decide whether to swallow the
  /// motionShake event instead of letting RN / the guest JS also react.
  func isGuestModeActive() -> Bool {
    return isGuestAppRunning
  }

  private func resolveSelectedRuntimeFamily() -> RuntimeFamily {
    if let metadata = currentGuestBundleMetadata(),
       let familyID = metadata["runtimeFamilyId"] as? String,
       let family = SDKManifest.shared.runtimeFamily(id: familyID) {
      return family
    }
    if let familyID = UserDefaults.standard.string(forKey: "yaverSelectedRuntimeFamilyID"),
       let family = SDKManifest.shared.runtimeFamily(id: familyID) {
      return family
    }
    return SDKManifest.shared.runtimeFamilies.first
      ?? RuntimeFamily(
        id: SDKManifest.shared.defaultRuntimeFamilyID,
        label: UserDefaults.standard.string(forKey: "yaverSelectedRuntimeFamilyLabel") ?? "Default runtime family",
        sdkVersion: SDKManifest.shared.sdkVersion,
        expoVersion: nil,
        reactNativeVersion: SDKManifest.shared.reactNativeVersion,
        reactVersion: nil,
        hermesVersion: nil,
        hermesBCVersion: Int(SDKManifest.shared.hermesBytecodeVersion),
        supportedRNRange: SDKManifest.shared.supportedRNRange,
        compiledIn: true,
        status: "active",
        manifestResource: "sdk-manifest.json",
        packageRoot: "mobile",
        preferredPackageNames: nil
      )
  }

  private func currentGuestBundleMetadata() -> [String: Any]? {
    let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
    let metadataURL = docs.appendingPathComponent("bundles/metadata.json")
    guard let data = try? Data(contentsOf: metadataURL),
          let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
      return nil
    }
    return obj
  }

  // MARK: - Shake to reveal Back to Yaver

  /// Called by ShakeDetectingWindow when device is shaken while guest app is running.
  /// Shows the 2-button overlay (Feedback / Back to Yaver). Feedback opens
  /// the guest's bundled SDK in-place (it owns the loaded app's feedback
  /// flow); Back to Yaver restores Yaver's bundle.
  func handleShakeGesture() {
    guard isGuestAppRunning else { return }
    guard let window = self.window else { return }
    showShakeOverlay(in: window)
  }

  private func showShakeOverlay(in window: UIWindow) {
    backOverlay?.removeFromSuperview()
    overlayDismissTimer?.invalidate()

    let accentColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 1.0)
    let greenColor = UIColor(red: 0.13, green: 0.77, blue: 0.37, alpha: 1.0)

    // Container card
    let card = UIView()
    card.backgroundColor = UIColor(red: 0.05, green: 0.05, blue: 0.08, alpha: 0.95)
    card.layer.cornerRadius = 16
    card.layer.borderWidth = 1
    card.layer.borderColor = UIColor(white: 0.2, alpha: 1.0).cgColor
    card.layer.shadowColor = UIColor.black.cgColor
    card.layer.shadowOffset = CGSize(width: 0, height: 6)
    card.layer.shadowRadius = 16
    card.layer.shadowOpacity = 0.6
    card.translatesAutoresizingMaskIntoConstraints = false

    // Button helper
    func makeButton(title: String, icon: String, color: UIColor, action: Selector) -> UIButton {
      let btn = UIButton(type: .system)
      let config = UIImage.SymbolConfiguration(pointSize: 15, weight: .semibold)
      btn.setImage(UIImage(systemName: icon, withConfiguration: config), for: .normal)
      btn.setTitle("  \(title)", for: .normal)
      btn.titleLabel?.font = .boldSystemFont(ofSize: 15)
      btn.tintColor = color
      btn.setTitleColor(color, for: .normal)
      btn.backgroundColor = color.withAlphaComponent(0.12)
      btn.layer.cornerRadius = 12
      btn.contentEdgeInsets = UIEdgeInsets(top: 12, left: 18, bottom: 12, right: 18)
      btn.addTarget(self, action: action, for: .touchUpInside)
      btn.translatesAutoresizingMaskIntoConstraints = false
      btn.heightAnchor.constraint(equalToConstant: 46).isActive = true
      return btn
    }

    // Two options: stay in the guest app and open its bundled feedback
    // SDK in-place ("Feedback"), or exit back to the Yaver shell
    // ("Back to Yaver"). Reload was previously a 3rd option but it
    // belongs inside the guest SDK, not the host overlay — when the
    // guest's feedback modal is open it owns the reload flow.
    let feedbackBtn = makeButton(title: "Feedback", icon: "bubble.left.and.bubble.right", color: accentColor,
                                 action: #selector(handleFeedbackTap))
    let backBtn = makeButton(title: "Back to Yaver", icon: "chevron.left", color: accentColor,
                             action: #selector(handleBackTap))

    let stack = UIStackView(arrangedSubviews: [feedbackBtn, backBtn])
    stack.axis = .vertical
    stack.spacing = 10
    stack.distribution = .fillEqually
    stack.translatesAutoresizingMaskIntoConstraints = false

    card.addSubview(stack)
    NSLayoutConstraint.activate([
      stack.topAnchor.constraint(equalTo: card.topAnchor, constant: 12),
      stack.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 12),
      stack.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -12),
      stack.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -12),
    ])

    window.addSubview(card)

    let topOffset: CGFloat = (window.safeAreaInsets.top > 0) ? window.safeAreaInsets.top + 8 : 32
    NSLayoutConstraint.activate([
      card.leadingAnchor.constraint(equalTo: window.leadingAnchor, constant: 16),
      card.trailingAnchor.constraint(equalTo: window.trailingAnchor, constant: -16),
      card.topAnchor.constraint(equalTo: window.topAnchor, constant: topOffset),
    ])

    backOverlay = card

    // Slide down + fade in
    card.alpha = 0
    card.transform = CGAffineTransform(translationX: 0, y: -30)
    UIView.animate(withDuration: 0.35, delay: 0, usingSpringWithDamping: 0.75,
                   initialSpringVelocity: 0.5) {
      card.alpha = 1.0
      card.transform = .identity
    }

    // Haptic
    UIImpactFeedbackGenerator(style: .medium).impactOccurred()

    // Auto-hide after 5 seconds
    overlayDismissTimer = Timer.scheduledTimer(withTimeInterval: 5.0, repeats: false) { [weak self] _ in
      self?.dismissOverlay()
    }
  }

  private func dismissOverlay() {
    overlayDismissTimer?.invalidate()
    overlayDismissTimer = nil
    guard let overlay = backOverlay else { return }
    UIView.animate(withDuration: 0.25, animations: {
      overlay.alpha = 0
      overlay.transform = CGAffineTransform(translationX: 0, y: -20)
    }) { _ in
      overlay.removeFromSuperview()
      self.backOverlay = nil
    }
  }

  @objc private func handleReloadTap() {
    NSLog("[AppDelegate] Reload App tapped — fetching fresh Hermes bundle")
    dismissOverlay()
    rebuildAndReloadGuestBundle()
  }

  @objc private func handleFeedbackTap() {
    NSLog("[AppDelegate] Feedback tapped — dispatching to guest SDK")
    dismissOverlay()
    // Stay in the guest bundle. Send a DeviceEventEmitter event into the
    // current bridge so the guest's bundled YaverFeedback SDK opens its
    // own feedback modal in-place. The bounce-back-to-Yaver behaviour
    // (UserDefaults flag + YaverBundleLoaderRestore notification) was
    // wrong for this model — Yaver is the runtime, sfmg / talos / etc.
    // own their own feedback flow.
    if let rootView = window?.rootViewController?.view as? RCTRootView {
      rootView.bridge.eventDispatcher().sendDeviceEvent(withName: "yaverFeedback:startReport", body: nil)
    } else {
      NSLog("[AppDelegate] Feedback: no guest bridge available — overlay just dismisses")
    }
  }

  /// POST /dev/build-native to the agent (Metro bundles + hermesc compiles),
  /// download the fresh bundle over the existing URL, then reload the guest bridge.
  /// This is what the JS side does in handleRunInYaver — but we do it natively
  /// so it works even while the guest bundle is running (no Yaver JS available).
  private func rebuildAndReloadGuestBundle() {
    guard let baseURL = UserDefaults.standard.string(forKey: "yaverAgentBaseURL"),
          let auth = UserDefaults.standard.string(forKey: "yaverAgentAuth"),
          let buildURL = URL(string: "\(baseURL)/dev/build-native") else {
      NSLog("[AppDelegate] reload: missing baseURL/auth — falling back to cached reload")
      if let rootView = window?.rootViewController?.view as? RCTRootView {
        rootView.bridge.reload()
      }
      return
    }

    // Show a quick loading indicator
    if let window = self.window {
      showReloadSpinner(in: window)
    }

    var req = URLRequest(url: buildURL)
    req.httpMethod = "POST"
    req.setValue(auth, forHTTPHeaderField: "Authorization")
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    let runtimeFamiliesPayload: [[String: Any]] = SDKManifest.shared.runtimeFamilies.map { family in
      [
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
    }
    req.httpBody = try? JSONSerialization.data(withJSONObject: [
      "platform": "ios",
      "consumerVersion": (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "",
      "consumerBuild": (Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String) ?? "",
      "consumerSdkVersion": SDKManifest.shared.sdkVersion ?? "",
      "consumerHermesBCVersion": Int(SDKManifest.shared.hermesBytecodeVersion),
      "consumerCurrentRuntimeFamilyId": UserDefaults.standard.string(forKey: "yaverSelectedRuntimeFamilyID")
        ?? SDKManifest.shared.defaultRuntimeFamilyID,
      "consumerDefaultRuntimeFamilyId": SDKManifest.shared.defaultRuntimeFamilyID,
      "consumerRuntimeFamilies": runtimeFamiliesPayload,
    ])
    req.timeoutInterval = 120

    URLSession.shared.dataTask(with: req) { [weak self] data, resp, err in
      if let err = err {
        NSLog("[AppDelegate] reload build failed: %@", err.localizedDescription)
        YaverGuestCrashReporter.recordGuestFailure(
          phase: "native_rebuild_failed",
          message: "Native rebuild request failed: \(err.localizedDescription)",
          moduleName: UserDefaults.standard.string(forKey: "yaverCurrentModuleName")
        )
        DispatchQueue.main.async { self?.hideReloadSpinner(); self?.fallbackBridgeReload() }
        return
      }
      guard let data = data,
            let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
            let bundleURLPath = obj["bundleUrl"] as? String,
            let fullBundleURL = URL(string: "\(baseURL)\(bundleURLPath)") else {
        NSLog("[AppDelegate] reload: invalid build response")
        YaverGuestCrashReporter.recordGuestFailure(
          phase: "native_rebuild_invalid_response",
          message: "Native rebuild returned an invalid response.",
          moduleName: UserDefaults.standard.string(forKey: "yaverCurrentModuleName")
        )
        DispatchQueue.main.async { self?.hideReloadSpinner(); self?.fallbackBridgeReload() }
        return
      }

      // Download fresh bundle
      var dlReq = URLRequest(url: fullBundleURL)
      dlReq.setValue(auth, forHTTPHeaderField: "Authorization")
      dlReq.timeoutInterval = 60
      URLSession.shared.dataTask(with: dlReq) { [weak self] bundleData, _, dlErr in
        if let dlErr = dlErr {
          NSLog("[AppDelegate] reload download failed: %@", dlErr.localizedDescription)
          YaverGuestCrashReporter.recordGuestFailure(
            phase: "native_rebuild_download_failed",
            message: "Downloading the rebuilt guest bundle failed: \(dlErr.localizedDescription)",
            moduleName: UserDefaults.standard.string(forKey: "yaverCurrentModuleName"),
            sourceURL: fullBundleURL.absoluteString
          )
          DispatchQueue.main.async { self?.hideReloadSpinner(); self?.fallbackBridgeReload() }
          return
        }
        guard let bundleData = bundleData, bundleData.count > 0 else {
          YaverGuestCrashReporter.recordGuestFailure(
            phase: "native_rebuild_empty_bundle",
            message: "Downloading the rebuilt guest bundle returned no bytes.",
            moduleName: UserDefaults.standard.string(forKey: "yaverCurrentModuleName"),
            sourceURL: fullBundleURL.absoluteString
          )
          DispatchQueue.main.async { self?.hideReloadSpinner(); self?.fallbackBridgeReload() }
          return
        }

        // Overwrite the cached bundle
        do {
          let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first!
          let bundlePath = docs.appendingPathComponent("bundles/main.jsbundle")
          try bundleData.write(to: bundlePath, options: .atomic)
          NSLog("[AppDelegate] reload: wrote %d bytes to %@", bundleData.count, bundlePath.path)
          YaverGuestCrashReporter.markGuestPhase(
            "native_rebuild_downloaded",
            moduleName: UserDefaults.standard.string(forKey: "yaverCurrentModuleName"),
            sourceURL: fullBundleURL.absoluteString,
            bundlePath: bundlePath.path
          )
        } catch {
          NSLog("[AppDelegate] reload write failed: %@", error.localizedDescription)
          YaverGuestCrashReporter.recordGuestFailure(
            phase: "native_rebuild_write_failed",
            message: "Writing the rebuilt guest bundle failed: \(error.localizedDescription)",
            moduleName: UserDefaults.standard.string(forKey: "yaverCurrentModuleName"),
            sourceURL: fullBundleURL.absoluteString
          )
          DispatchQueue.main.async { self?.hideReloadSpinner(); self?.fallbackBridgeReload() }
          return
        }

        // Fire reload notification — AppDelegate.safeReloadBridge reads the fresh bundle
        DispatchQueue.main.async {
          self?.hideReloadSpinner()
          let moduleName = UserDefaults.standard.string(forKey: "yaverCurrentModuleName") ?? "main"
          NotificationCenter.default.post(
            name: Notification.Name("YaverBundleLoaderReload"),
            object: nil,
            userInfo: ["moduleName": moduleName]
          )
        }
      }.resume()
    }.resume()
  }

  private func fallbackBridgeReload() {
    if let rootView = window?.rootViewController?.view as? RCTRootView {
      rootView.bridge.reload()
    }
  }

  private var reloadSpinner: UIView?
  private func showReloadSpinner(in window: UIWindow) {
    reloadSpinner?.removeFromSuperview()
    let bg = UIView()
    bg.backgroundColor = UIColor.black.withAlphaComponent(0.4)
    bg.translatesAutoresizingMaskIntoConstraints = false

    let card = UIView()
    card.backgroundColor = UIColor(red: 0.05, green: 0.05, blue: 0.08, alpha: 0.95)
    card.layer.cornerRadius = 14
    card.translatesAutoresizingMaskIntoConstraints = false

    let spinner = UIActivityIndicatorView(style: .large)
    spinner.color = UIColor(red: 0.13, green: 0.77, blue: 0.37, alpha: 1.0)
    spinner.startAnimating()
    spinner.translatesAutoresizingMaskIntoConstraints = false

    let label = UILabel()
    label.text = "Reloading…"
    label.font = .boldSystemFont(ofSize: 14)
    label.textColor = .white
    label.translatesAutoresizingMaskIntoConstraints = false

    card.addSubview(spinner)
    card.addSubview(label)
    bg.addSubview(card)
    window.addSubview(bg)

    NSLayoutConstraint.activate([
      bg.leadingAnchor.constraint(equalTo: window.leadingAnchor),
      bg.trailingAnchor.constraint(equalTo: window.trailingAnchor),
      bg.topAnchor.constraint(equalTo: window.topAnchor),
      bg.bottomAnchor.constraint(equalTo: window.bottomAnchor),
      card.centerXAnchor.constraint(equalTo: bg.centerXAnchor),
      card.centerYAnchor.constraint(equalTo: bg.centerYAnchor),
      card.widthAnchor.constraint(equalToConstant: 160),
      card.heightAnchor.constraint(equalToConstant: 120),
      spinner.centerXAnchor.constraint(equalTo: card.centerXAnchor),
      spinner.centerYAnchor.constraint(equalTo: card.centerYAnchor, constant: -14),
      label.centerXAnchor.constraint(equalTo: card.centerXAnchor),
      label.topAnchor.constraint(equalTo: spinner.bottomAnchor, constant: 12),
    ])
    reloadSpinner = bg
  }

  private func hideReloadSpinner() {
    reloadSpinner?.removeFromSuperview()
    reloadSpinner = nil
  }

  @objc private func handleBackTap() {
    NSLog("[AppDelegate] Back to Yaver tapped")
    dismissOverlay()
    isGuestAppRunning = false
    // Kill the dev server so next "Open App" starts from a clean initial state
    stopDevServerOnAgent()
    NotificationCenter.default.post(name: Notification.Name("YaverBundleLoaderRestore"), object: nil)
  }

  /// POST /dev/stop to the agent so the next Open App starts fresh.
  /// Uses the baseURL + auth token stored by YaverBundleLoader when the bundle was loaded.
  private func stopDevServerOnAgent() {
    guard let baseURL = UserDefaults.standard.string(forKey: "yaverAgentBaseURL"),
          let auth = UserDefaults.standard.string(forKey: "yaverAgentAuth"),
          let url = URL(string: "\(baseURL)/dev/stop") else {
      NSLog("[AppDelegate] stopDevServerOnAgent: missing baseURL or auth")
      return
    }
    var req = URLRequest(url: url)
    req.httpMethod = "POST"
    req.setValue(auth, forHTTPHeaderField: "Authorization")
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.timeoutInterval = 5
    URLSession.shared.dataTask(with: req) { _, resp, err in
      if let err = err {
        NSLog("[AppDelegate] /dev/stop failed: %@", err.localizedDescription)
      } else if let http = resp as? HTTPURLResponse {
        NSLog("[AppDelegate] /dev/stop → %d", http.statusCode)
      }
    }.resume()
  }

  /// Shows an error screen instead of a white screen when the guest app fails to load.
  private func showGuestErrorScreen(message: String, window: UIWindow) {
    NSLog("[AppDelegate] showing error screen: %@", message)

    let errorVC = UIViewController()
    let view = UIView(frame: window.bounds)
    view.backgroundColor = UIColor(red: 0.02, green: 0.02, blue: 0.03, alpha: 1.0)

    let stack = UIStackView()
    stack.axis = .vertical
    stack.alignment = .center
    stack.spacing = 16
    stack.translatesAutoresizingMaskIntoConstraints = false

    let icon = UILabel()
    icon.text = "\u{26A0}\u{FE0F}"
    icon.font = .systemFont(ofSize: 48)
    stack.addArrangedSubview(icon)

    let title = UILabel()
    title.text = "App Load Failed"
    title.font = .boldSystemFont(ofSize: 20)
    title.textColor = .white
    stack.addArrangedSubview(title)

    let detail = UILabel()
    detail.text = message
    detail.font = .systemFont(ofSize: 14)
    detail.textColor = UIColor(white: 0.6, alpha: 1.0)
    detail.textAlignment = .center
    detail.numberOfLines = 0
    detail.lineBreakMode = .byWordWrapping
    stack.addArrangedSubview(detail)

    let backBtn = UIButton(type: .system)
    backBtn.setTitle("Back to Yaver", for: .normal)
    backBtn.titleLabel?.font = .boldSystemFont(ofSize: 16)
    backBtn.setTitleColor(UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 1.0), for: .normal)
    backBtn.addTarget(self, action: #selector(handleErrorScreenBack), for: .touchUpInside)
    stack.addArrangedSubview(backBtn)

    view.addSubview(stack)
    NSLayoutConstraint.activate([
      stack.centerXAnchor.constraint(equalTo: view.centerXAnchor),
      stack.centerYAnchor.constraint(equalTo: view.centerYAnchor),
      stack.leadingAnchor.constraint(greaterThanOrEqualTo: view.leadingAnchor, constant: 32),
      stack.trailingAnchor.constraint(lessThanOrEqualTo: view.trailingAnchor, constant: -32),
    ])

    errorVC.view = view
    window.rootViewController = errorVC
  }

  @objc private func handleErrorScreenBack() {
    NSLog("[AppDelegate] error screen: user tapped Back to Yaver")
    NotificationCenter.default.post(name: Notification.Name("YaverBundleLoaderRestore"), object: nil)
  }

  @objc func handleBundleRestore(_ notification: Notification) {
    NSLog("[AppDelegate] Restoring original Yaver bundle...")
    YaverGuestCrashReporter.clearGuestSession()
    isGuestAppRunning = false
    overlayDismissTimer?.invalidate()
    overlayDismissTimer = nil
    backOverlay?.removeFromSuperview()
    backOverlay = nil

    let docsPath = NSSearchPathForDirectoriesInDomains(.documentDirectory, .userDomainMask, true).first!
    let downloadedBundle = (docsPath as NSString).appendingPathComponent("bundles/main.jsbundle")
    try? FileManager.default.removeItem(atPath: downloadedBundle)
    UserDefaults.standard.removeObject(forKey: "yaverLoadedModuleName")
    UserDefaults.standard.removeObject(forKey: "yaverCurrentModuleName")

    let existingBridge = (window?.rootViewController?.view as? RCTRootView)?.bridge
    existingBridge?.invalidate()

    waitForBridgeDeallocation(bridge: existingBridge, timeout: 3.0) { [weak self] in
      guard let self = self, let window = self.window else { return }

      let delegate = ReactNativeDelegate()
      delegate.overrideBundleURL = nil
      delegate.dependencyProvider = RCTAppDependencyProvider()

      let factory = ExpoReactNativeFactory(delegate: delegate)
      self.reactNativeDelegate = delegate
      self.reactNativeFactory = factory
      self.bindReactNativeFactory(factory)

      factory.startReactNative(
        withModuleName: "main",
        in: window,
        launchOptions: nil)

      print("[AppDelegate] Yaver restored")
    }
  }

  public override func application(
    _ app: UIApplication,
    open url: URL,
    options: [UIApplication.OpenURLOptionsKey: Any] = [:]
  ) -> Bool {
    return super.application(app, open: url, options: options) || RCTLinkingManager.application(app, open: url, options: options)
  }

  public override func application(
    _ application: UIApplication,
    continue userActivity: NSUserActivity,
    restorationHandler: @escaping ([UIUserActivityRestoring]?) -> Void
  ) -> Bool {
    let result = RCTLinkingManager.application(application, continue: userActivity, restorationHandler: restorationHandler)
    return super.application(application, continue: userActivity, restorationHandler: restorationHandler) || result
  }
}

// MARK: - Shake-detecting window
// Intercepts device shake at the UIWindow level (before any responder chain).
// Works even when a guest RN bridge is running — the guest can't block this.
//
// When a guest app is running we deliberately DO NOT forward motionShake up
// the responder chain. That blocks:
//   • RN's built-in dev menu (RCTDevMenu in Debug) from opening over our
//     2-button "Reload / Back to Yaver" overlay.
//   • DeviceEventEmitter 'shakeEvent' from firing inside the guest's JS
//     context (yaver-feedback-react-native's ShakeDetector, guest-side
//     dev helpers, etc.). The only thing a shake can do inside a third-
//     party app loaded through Yaver is show our two buttons.
// Other motion events (rotation, etc.) still flow normally so we do not
// break unrelated guest behaviour.
class ShakeDetectingWindow: UIWindow {
  override func motionEnded(_ motion: UIEvent.EventSubtype, with event: UIEvent?) {
    if motion == .motionShake {
      if let appDelegate = UIApplication.shared.delegate as? AppDelegate {
        appDelegate.handleShakeGesture()
        if appDelegate.isGuestModeActive() { return }
      }
    }
    super.motionEnded(motion, with: event)
  }
}

class ReactNativeDelegate: ExpoReactNativeFactoryDelegate {
  var overrideBundleURL: URL?

  override func sourceURL(for bridge: RCTBridge) -> URL? {
    bridge.bundleURL ?? bundleURL()
  }

  override func bundleURL() -> URL? {
    if let override = overrideBundleURL {
      return override
    }

    // Only use a downloaded guest bundle if it was explicitly loaded via YaverBundleLoader
    // (indicated by the yaverLoadedModuleName UserDefaults key).
    // This prevents stale bundles from previous tests from hijacking app startup.
    if UserDefaults.standard.string(forKey: "yaverLoadedModuleName") != nil {
      if let docsPath = NSSearchPathForDirectoriesInDomains(.documentDirectory, .userDomainMask, true).first {
        let downloaded = (docsPath as NSString).appendingPathComponent("bundles/main.jsbundle")
        if FileManager.default.fileExists(atPath: downloaded) {
          NSLog("[ReactNativeDelegate] Using downloaded guest bundle: %@", downloaded)
          return URL(fileURLWithPath: downloaded)
        }
      }
    }

#if DEBUG
    return RCTBundleURLProvider.sharedSettings().jsBundleURL(forBundleRoot: ".expo/.virtual-metro-entry")
#else
    return Bundle.main.url(forResource: "main", withExtension: "jsbundle")
#endif
  }
}
