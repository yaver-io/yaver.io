import CoreMotion
import Expo
import React
import ReactAppDependencyProvider

@UIApplicationMain
public class AppDelegate: ExpoAppDelegate {
  var window: UIWindow?

  var reactNativeDelegate: ReactNativeDelegate?
  var reactNativeFactory: RCTReactNativeFactory?
  private var isReloading = false
  private let carVoiceShortcutType = "io.yaver.mobile.carVoice"

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
    //
    // Phone-frame note: when the host has wrapped the guest in
    // YaverFramedHost, the rootViewController.view is the framed
    // chrome — the actual RCTRootView lives inside the phoneArea
    // child. Walk the hierarchy to find it. Phones / non-framed
    // tablets fall through to the existing direct cast.
    let existingRootView = AppDelegate.findRCTRootView(in: window?.rootViewController?.view)
    let existingBridge = existingRootView?.bridge

    // Phone-frame: tear the host off before the placeholder swap so
    // factory.startReactNative gets a clean rootViewController slot.
    // We re-apply the frame at the end of initGuestBridge, so the
    // user-visible UX is unchanged. Doing this before the placeholder
    // also keeps the placeholder centred on the *whole* window
    // instead of being trapped inside the framed phone area.
    if let win = window { YaverFramedHost.removeIfActive(window: win) }

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

    // Tablet phone-frame: re-parent the guest into a sized container
    // so on iPad it renders at iPhone dimensions with a vibe dock
    // alongside, instead of stretching across the tablet. Default
    // off — gated by UserDefaults.yaverGuestPhoneFrame.
    YaverFramedHost.applyIfNeeded(window: window)

    isReloading = false
    NSLog("[AppDelegate] guest app loaded (New Arch): module=%@", moduleName)
    YaverGuestCrashReporter.markGuestPhase(
      "bridge_started_guest",
      moduleName: moduleName,
      bundlePath: bundleURL.path
    )

    // Guest app is running. Shake phone to reveal "Back to Yaver" overlay.
    isGuestAppRunning = true

    // Restart shake detection. The UIKit responder-chain delivery of
    // motionShake to ShakeDetectingWindow stops working after the
    // SECONDARY bridge swap (the new RCTSurfaceHostingProxyRootView
    // mounted by factory.startReactNative consumes motionEnded
    // before it bubbles up to the window). The CoreMotion path
    // bypasses the responder chain entirely — accelerometer
    // sampling at the OS level — so it survives any number of
    // bridge invalidate/recreate cycles without re-wiring.
    startCoreMotionShakeDetector()

    // If the user opted into the floating-Y trigger, mount it now over
    // the freshly loaded guest UI. dismounted automatically when we
    // route back to the Yaver shell (isGuestAppRunning = false).
    refreshFeedbackTriggerMount()

    // Phase-3 signal in the reload protocol. The feedback overlay
    // listens for this notification to advance the in-flight reload
    // status from "🔄 Swapping app…" → "✓ Reloaded — changes are
    // live" and clear its in-flight latch. The previous flow showed
    // ✓ Reloaded the moment the agent emitted reload_done, which
    // was wrong — the bundle hadn't even downloaded yet, let alone
    // swapped. Now ✓ only fires after the new bridge actually
    // started rendering.
    NotificationCenter.default.post(name: AppDelegate.guestReloadCompleteNotification,
                                    object: nil,
                                    userInfo: ["moduleName": moduleName])
  }

  /// Posted from initGuestBridge when the new guest bundle has
  /// finished mounting. The feedback overlay subscribes to clear its
  /// reload spinner. See YaverFeedbackPane.swift::kickOverlayReload.
  static let guestReloadCompleteNotification = Notification.Name("YaverGuestReloadComplete")

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

  // MARK: - CoreMotion-based shake detector
  //
  // UIKit's responder-chain motionEnded delivery breaks after the
  // SECONDARY bridge swap because the new RCTSurfaceHostingProxyRootView
  // mounted by factory.startReactNative consumes motionEnded before
  // it bubbles to ShakeDetectingWindow. Accelerometer-driven detection
  // ignores the responder chain entirely and survives every bridge
  // recreate. CMMotionManager.deviceMotion runs at the OS level — no
  // UIKit involvement — so it works regardless of which view is the
  // first responder, which window is key, or whether the guest bundle
  // intercepts touch/motion events.
  private static let shakeAccelerationThreshold: Double = 2.5    // g
  private static let shakeMinSpikes: Int = 3                     // need this many in window
  private static let shakeWindowSeconds: Double = 0.7
  private static let shakeCooldownSeconds: Double = 1.2          // ignore re-fires
  private var motionManager: CMMotionManager?
  private var shakeSpikeTimestamps: [Date] = []
  private var lastShakeFiredAt: Date = .distantPast

  private func startCoreMotionShakeDetector() {
    if motionManager?.isDeviceMotionActive == true { return }
    let mgr = CMMotionManager()
    guard mgr.isDeviceMotionAvailable else {
      NSLog("[AppDelegate] CoreMotion deviceMotion unavailable on this device")
      return
    }
    mgr.deviceMotionUpdateInterval = 1.0 / 30.0    // 30 Hz — plenty for shake detection
    self.motionManager = mgr
    mgr.startDeviceMotionUpdates(to: .main) { [weak self] motion, _ in
      guard let self = self, let m = motion else { return }
      // userAcceleration is acceleration with gravity already subtracted.
      let a = m.userAcceleration
      let magnitude = sqrt(a.x * a.x + a.y * a.y + a.z * a.z)
      if magnitude < AppDelegate.shakeAccelerationThreshold { return }
      let now = Date()
      // Cooldown — one shake gesture is one event, not 30 at 30 Hz.
      if now.timeIntervalSince(self.lastShakeFiredAt) < AppDelegate.shakeCooldownSeconds {
        return
      }
      self.shakeSpikeTimestamps.append(now)
      // Drop samples outside the rolling window.
      let windowStart = now.addingTimeInterval(-AppDelegate.shakeWindowSeconds)
      self.shakeSpikeTimestamps.removeAll { $0 < windowStart }
      if self.shakeSpikeTimestamps.count >= AppDelegate.shakeMinSpikes {
        NSLog("[AppDelegate] CoreMotion shake detected (mag=%.2f, spikes=%d)",
              magnitude, self.shakeSpikeTimestamps.count)
        self.lastShakeFiredAt = now
        self.shakeSpikeTimestamps.removeAll()
        self.handleShakeGesture()
      }
    }
    NSLog("[AppDelegate] CoreMotion shake detector started")
  }

  private func showShakeOverlay(in window: UIWindow) {
    // Stateful guard: if the overlay is already up (e.g. user
    // tapped the Y bubble twice in a row), bail. Otherwise we'd
    // stack a second card on top and the user has to dismiss
    // both, layered, before they can interact with anything.
    if backOverlay != nil { return }
    overlayDismissTimer?.invalidate()

    let accentColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 1.0)
    _ = UIColor(red: 0.13, green: 0.77, blue: 0.37, alpha: 1.0) // greenColor unused after removing Reload

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

    // Close X — sits in the card's top-right so the user can dismiss the
    // shake overlay without picking any of the four actions. Also tapping
    // outside the card area auto-dismisses (see overlayDismissTimer auto-
    // hide), but an explicit X is what users expect.
    let closeBtn = UIButton(type: .system)
    closeBtn.translatesAutoresizingMaskIntoConstraints = false
    let xCfg = UIImage.SymbolConfiguration(pointSize: 14, weight: .semibold)
    closeBtn.setImage(UIImage(systemName: "xmark", withConfiguration: xCfg), for: .normal)
    closeBtn.tintColor = UIColor(white: 1, alpha: 0.55)
    closeBtn.addTarget(self, action: #selector(handleOverlayCloseTap), for: .touchUpInside)

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

    // Four options: in-place feedback (vibe + reload + screenshot),
    // coding-agent setup (claude/codex/opencode auth + opencode config),
    // settings (trigger mode: shake vs floating Y button — same trigger
    // values as the standalone feedback SDK's `trigger?: 'shake' |
    // 'floating-button' | 'manual'`), and exit back to the Yaver shell.
    let feedbackBtn = makeButton(title: "Feedback", icon: "bubble.left.and.bubble.right", color: accentColor,
                                 action: #selector(handleFeedbackTap))
    let agentsBtn = makeButton(title: "Agents", icon: "person.crop.circle.badge.checkmark", color: accentColor,
                               action: #selector(handleAgentsTap))
    let settingsBtn = makeButton(title: "Settings", icon: "gearshape", color: accentColor,
                                 action: #selector(handleSettingsTap))
    let deployBtn = makeButton(title: "Deploy", icon: "paperplane.fill", color: accentColor,
                               action: #selector(handleDeployTap))
    let backBtn = makeButton(title: "Back to Yaver", icon: "chevron.left", color: accentColor,
                             action: #selector(handleBackTap))

    let stack = UIStackView(arrangedSubviews: [feedbackBtn, agentsBtn, settingsBtn, deployBtn, backBtn])
    stack.axis = .vertical
    stack.spacing = 10
    stack.distribution = .fillEqually
    stack.translatesAutoresizingMaskIntoConstraints = false

    card.addSubview(stack)
    card.addSubview(closeBtn)
    NSLayoutConstraint.activate([
      // Reserve a strip at the top of the card for the close button so
      // it doesn't sit on top of the first action button.
      stack.topAnchor.constraint(equalTo: card.topAnchor, constant: 36),
      stack.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 12),
      stack.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -12),
      stack.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -12),
      closeBtn.topAnchor.constraint(equalTo: card.topAnchor, constant: 6),
      closeBtn.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -8),
      closeBtn.widthAnchor.constraint(equalToConstant: 32),
      closeBtn.heightAnchor.constraint(equalToConstant: 28),
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
    NSLog("[AppDelegate] Feedback tapped — presenting native pane")
    dismissOverlay()
    // Present Yaver's native feedback pane over the guest bundle. Works
    // for ANY guest app regardless of which version of yaver-feedback-
    // react-native it ships with — even apps without the SDK at all.
    // The pane talks directly to the agent's HTTP API (/tasks +
    // /dev/reload) using the user's bearer + agent URL stored in
    // UserDefaults by YaverBundleLoader / auth.ts.
    guard let win = self.window else { return }
    YaverFeedbackPane.shared.present(in: win)
  }

  @objc private func handleAgentsTap() {
    NSLog("[AppDelegate] Agents tapped — presenting native agents pane")
    dismissOverlay()
    guard let win = self.window else { return }
    YaverAgentsPane.shared.present(in: win)
  }

  @objc private func handleSettingsTap() {
    NSLog("[AppDelegate] Settings tapped — presenting trigger-mode pane")
    dismissOverlay()
    guard let win = self.window else { return }
    // Pane lets the user pick how the shake overlay is triggered: shake
    // gesture (default) or a draggable floating Y button. Same value
    // space as the standalone feedback SDK's `trigger?:` field, persisted
    // under UserDefaults("yaverFeedbackTrigger") so anything else in the
    // host that wants to read the user's preference (e.g. an SDK module
    // bridged via YaverInfo) gets a single source of truth.
    YaverSettingsPane.shared.present(in: win, applyTrigger: { [weak self] in
      self?.refreshFeedbackTriggerMount()
    })
  }

  @objc private func handleDeployTap() {
    NSLog("[AppDelegate] Deploy tapped — presenting native deploy pane")
    dismissOverlay()
    guard let win = self.window else { return }
    // Pane lets the user pick a target (TestFlight / Play / Both) and a
    // machine from their fleet to run the deploy on. Capabilities come
    // from /fleet/deploy-options on the agent — Linux machines greyed
    // out for TestFlight, macOS without Xcode greyed out the same way.
    YaverDeployPane.shared.present(in: win)
  }

  @objc private func handleOverlayCloseTap() {
    // Explicit X on the shake overlay — same effect as the auto-hide
    // timer firing. Users want a tap target rather than waiting 5s.
    dismissOverlay()
  }

  /// Reads the persisted trigger mode and mounts/dismounts the floating
  /// Y button overlay accordingly. Called after Settings save AND once
  /// at app activation when a guest bundle is running. Shake detection
  /// is left in place either way — the floating button is additive, not
  /// a replacement, so power users get both.
  func refreshFeedbackTriggerMount() {
    let mode = UserDefaults.standard.string(forKey: "yaverFeedbackTrigger") ?? "shake"
    NSLog("[AppDelegate] refreshFeedbackTriggerMount mode=%@ window=%@", mode,
          self.window != nil ? "present" : "nil")
    guard let win = self.window else {
      NSLog("[AppDelegate] refreshFeedbackTriggerMount: no window — skipping")
      return
    }
    if mode == "floating-button" {
      NSLog("[AppDelegate] mounting floating Y trigger over window bounds=%@",
            NSCoder.string(for: win.bounds))
      YaverFloatingTrigger.shared.mount(in: win) { [weak self] in
        // Tap on Y bubble goes STRAIGHT to the Feedback pane —
        // that's the action the user does ~95% of the time. The
        // shake-overlay menu (Feedback / Agents / Settings / Back)
        // is still reachable from inside the Feedback pane via the
        // small links at the top, and via long-press on the Y bubble.
        self?.handleFeedbackTap()
      }
    } else {
      YaverFloatingTrigger.shared.dismount()
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
    if let rootView = AppDelegate.findRCTRootView(in: window?.rootViewController?.view) {
      rootView.bridge.reload()
    }
  }

  /// Walks the view hierarchy looking for an RCTRootView. Used so the
  /// bridge-reload path keeps working when YaverFramedHost has put a
  /// chrome wrapper between the rootViewController and the guest's
  /// RCTRootView. Falls back to a direct cast on the non-framed path.
  static func findRCTRootView(in view: UIView?) -> RCTRootView? {
    guard let view = view else { return nil }
    if let rct = view as? RCTRootView { return rct }
    for sub in view.subviews {
      if let found = findRCTRootView(in: sub) { return found }
    }
    return nil
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
    // Floating-Y trigger only makes sense over a guest app; pull it
    // back when we route to the Yaver shell.
    YaverFloatingTrigger.shared.dismount()
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

    let existingBridge = AppDelegate.findRCTRootView(in: window?.rootViewController?.view)?.bridge
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

      // Restore: pull the guest out of the framed host before
      // mounting Yaver's own bundle, so Yaver's UI renders full-screen
      // (it has its own internal tablet layout — no need to frame).
      YaverFramedHost.removeIfActive(window: window)

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
    let handledByRN = RCTLinkingManager.application(app, open: url, options: options)
    if isCarVoiceLaunchURL(url) {
      UserDefaults.standard.set(true, forKey: "yaverPendingCarVoiceLaunch")
      return true
    }
    return super.application(app, open: url, options: options) || handledByRN
  }

  public override func application(
    _ application: UIApplication,
    continue userActivity: NSUserActivity,
    restorationHandler: @escaping ([UIUserActivityRestoring]?) -> Void
  ) -> Bool {
    let result = RCTLinkingManager.application(application, continue: userActivity, restorationHandler: restorationHandler)
    return super.application(application, continue: userActivity, restorationHandler: restorationHandler) || result
  }

  public override func application(
    _ application: UIApplication,
    performActionFor shortcutItem: UIApplicationShortcutItem,
    completionHandler: @escaping (Bool) -> Void
  ) {
    guard shortcutItem.type == carVoiceShortcutType,
          let url = URL(string: "yaver://car-voice-coding?autostart=1")
    else {
      completionHandler(false)
      return
    }
    UserDefaults.standard.set(true, forKey: "yaverPendingCarVoiceLaunch")
    _ = RCTLinkingManager.application(application, open: url, options: [:])
    completionHandler(true)
  }

  private func isCarVoiceLaunchURL(_ url: URL) -> Bool {
    guard url.scheme?.lowercased() == "yaver" else { return false }
    if url.host?.lowercased() == "car-voice-coding" { return true }
    return url.path.trimmingCharacters(in: CharacterSet(charactersIn: "/")).lowercased() == "car-voice-coding"
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

// MARK: - YaverFramedHost
//
// Wraps a guest RCTRootView in an iPhone-shaped frame on iPad,
// leaving the rest of the screen free for a vibe dock (a small
// UIKit panel that surfaces the Y feedback trigger). Strict opt-in
// via UserDefaults("yaverGuestPhoneFrame") — when the flag is
// false (the default), `applyIfNeeded` returns immediately and the
// guest mount path stays byte-identical to the pre-tablet flow.
//
// Layout policy:
//   • Phone (UIDevice.userInterfaceIdiom != .pad): never frames.
//     The picker that drives the flag is hidden on phones, but the
//     guard here is the load-bearing one — anyone toggling the
//     flag manually on a phone gets normal behaviour.
//   • Tablet landscape: phone column on the left, vibe dock right.
//   • Tablet portrait: phone strip on top, vibe dock below.
//
// The vibe dock is intentionally minimal in v1: a centered Y bubble
// that taps through to YaverFeedbackPane. The pane is the same one
// that the floating-Y trigger / shake overlay routes to, so the
// whole feedback experience reuses existing surfaces.
final class YaverFramedHost: UIViewController {

  private weak var guestVC: UIViewController?
  private let phoneArea = UIView()
  private let vibeDock = UIView()
  private var dockOnRight: Bool = true
  // Phone preset — iPhone 15 Pro Max footprint. Future versions can
  // expose a per-guest preset (iPhone SE 375x667, Pixel 6 412x915).
  private static let phoneWidth: CGFloat = 414
  private static let phoneHeight: CGFloat = 896

  // Public toggle — what the JS layer reads/writes.
  static let userDefaultKey = "yaverGuestPhoneFrame"

  static var isEnabled: Bool {
    return UserDefaults.standard.bool(forKey: userDefaultKey)
  }

  /// Wrap the current rootViewController if all preconditions hold.
  /// Idempotent — safe to call multiple times during the bridge
  /// reload pipeline.
  static func applyIfNeeded(window: UIWindow) {
    guard UIDevice.current.userInterfaceIdiom == .pad else { return }
    guard isEnabled else { return }
    guard let guest = window.rootViewController, !(guest is YaverFramedHost) else { return }
    let host = YaverFramedHost()
    host.embed(guest: guest, in: window)
    window.rootViewController = host
    NSLog("[YaverFramedHost] applied; guest=\(type(of: guest))")
  }

  /// Reverse of `applyIfNeeded` — used by handleBundleRestore so
  /// Yaver's own bundle goes back to fullscreen when the user exits
  /// the guest app.
  static func removeIfActive(window: UIWindow) {
    guard let host = window.rootViewController as? YaverFramedHost else { return }
    let detached = host.detachGuest()
    window.rootViewController = detached
    NSLog("[YaverFramedHost] removed")
  }

  override func viewDidLoad() {
    super.viewDidLoad()
    view.backgroundColor = UIColor(red: 0.04, green: 0.04, blue: 0.06, alpha: 1.0)

    phoneArea.translatesAutoresizingMaskIntoConstraints = false
    phoneArea.backgroundColor = .black
    phoneArea.layer.cornerRadius = 22
    phoneArea.layer.borderWidth = 1.5
    phoneArea.layer.borderColor = UIColor(white: 0.18, alpha: 1.0).cgColor
    phoneArea.clipsToBounds = true
    view.addSubview(phoneArea)

    vibeDock.translatesAutoresizingMaskIntoConstraints = false
    vibeDock.backgroundColor = UIColor(red: 0.06, green: 0.06, blue: 0.08, alpha: 1.0)
    view.addSubview(vibeDock)
    setupVibeDockContent()
  }

  override func viewDidLayoutSubviews() {
    super.viewDidLayoutSubviews()
    layoutPhoneAndDock()
  }

  override func viewWillTransition(to size: CGSize, with coordinator: UIViewControllerTransitionCoordinator) {
    super.viewWillTransition(to: size, with: coordinator)
    coordinator.animate { _ in
      self.layoutPhoneAndDock()
    }
  }

  private func embed(guest: UIViewController, in window: UIWindow) {
    self.guestVC = guest
    self.view.frame = window.bounds
    addChild(guest)
    guest.view.translatesAutoresizingMaskIntoConstraints = false
    phoneArea.addSubview(guest.view)
    NSLayoutConstraint.activate([
      guest.view.leadingAnchor.constraint(equalTo: phoneArea.leadingAnchor),
      guest.view.trailingAnchor.constraint(equalTo: phoneArea.trailingAnchor),
      guest.view.topAnchor.constraint(equalTo: phoneArea.topAnchor),
      guest.view.bottomAnchor.constraint(equalTo: phoneArea.bottomAnchor),
    ])
    guest.didMove(toParent: self)
  }

  /// Pull the guest VC back out of this host so it can be re-installed
  /// as window.rootViewController directly. Returns the detached VC.
  fileprivate func detachGuest() -> UIViewController {
    guard let guest = guestVC else {
      return UIViewController() // shouldn't happen; caller will swap to a fresh main
    }
    guest.willMove(toParent: nil)
    guest.view.removeFromSuperview()
    guest.removeFromParent()
    self.guestVC = nil
    return guest
  }

  private func layoutPhoneAndDock() {
    phoneArea.translatesAutoresizingMaskIntoConstraints = true
    vibeDock.translatesAutoresizingMaskIntoConstraints = true

    let bounds = view.bounds
    let safeTop = view.safeAreaInsets.top
    let safeBottom = view.safeAreaInsets.bottom
    let isLandscape = bounds.width > bounds.height

    if isLandscape {
      // Phone pinned left, vibe dock right.
      let phoneW = min(YaverFramedHost.phoneWidth, bounds.width * 0.5)
      let phoneH = min(YaverFramedHost.phoneHeight, bounds.height - safeTop - safeBottom - 16)
      let phoneY = safeTop + (bounds.height - safeTop - safeBottom - phoneH) / 2
      phoneArea.frame = CGRect(x: 16, y: phoneY, width: phoneW, height: phoneH)
      let dockX = phoneArea.frame.maxX + 12
      vibeDock.frame = CGRect(
        x: dockX,
        y: safeTop + 8,
        width: max(0, bounds.width - dockX - 16),
        height: bounds.height - safeTop - safeBottom - 16
      )
    } else {
      // Phone on top, vibe dock at bottom (so vibing reads as
      // "below the device", matching the simulator UX).
      let phoneW = min(YaverFramedHost.phoneWidth, bounds.width - 32)
      let phoneH = min(YaverFramedHost.phoneHeight, bounds.height * 0.62)
      let phoneX = (bounds.width - phoneW) / 2
      phoneArea.frame = CGRect(x: phoneX, y: safeTop + 12, width: phoneW, height: phoneH)
      let dockY = phoneArea.frame.maxY + 12
      vibeDock.frame = CGRect(
        x: 16,
        y: dockY,
        width: bounds.width - 32,
        height: max(0, bounds.height - dockY - safeBottom - 12)
      )
    }
  }

  // MARK: vibe dock content

  private func setupVibeDockContent() {
    let title = UILabel()
    title.text = "Vibing"
    title.font = .boldSystemFont(ofSize: 13)
    title.textColor = UIColor(white: 0.85, alpha: 1.0)
    title.translatesAutoresizingMaskIntoConstraints = false

    let subtitle = UILabel()
    subtitle.text = "Tap the Y to chat with your agent — the framed app on the side stays live."
    subtitle.numberOfLines = 0
    subtitle.font = .systemFont(ofSize: 12)
    subtitle.textColor = UIColor(white: 0.55, alpha: 1.0)
    subtitle.translatesAutoresizingMaskIntoConstraints = false

    let yButton = UIButton(type: .system)
    yButton.setTitle("Y", for: .normal)
    yButton.titleLabel?.font = .boldSystemFont(ofSize: 22)
    yButton.setTitleColor(.white, for: .normal)
    yButton.backgroundColor = UIColor(red: 0.5, green: 0.55, blue: 0.97, alpha: 1.0)
    yButton.layer.cornerRadius = 14
    yButton.translatesAutoresizingMaskIntoConstraints = false
    yButton.addTarget(self, action: #selector(handleYTap), for: .touchUpInside)

    vibeDock.addSubview(title)
    vibeDock.addSubview(subtitle)
    vibeDock.addSubview(yButton)
    NSLayoutConstraint.activate([
      title.topAnchor.constraint(equalTo: vibeDock.topAnchor, constant: 16),
      title.leadingAnchor.constraint(equalTo: vibeDock.leadingAnchor, constant: 14),
      subtitle.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 6),
      subtitle.leadingAnchor.constraint(equalTo: vibeDock.leadingAnchor, constant: 14),
      subtitle.trailingAnchor.constraint(equalTo: vibeDock.trailingAnchor, constant: -14),
      yButton.topAnchor.constraint(equalTo: subtitle.bottomAnchor, constant: 18),
      yButton.leadingAnchor.constraint(equalTo: vibeDock.leadingAnchor, constant: 14),
      yButton.widthAnchor.constraint(equalToConstant: 56),
      yButton.heightAnchor.constraint(equalToConstant: 56),
    ])
  }

  @objc private func handleYTap() {
    // Same path as the floating Y trigger / shake overlay → routes
    // to the existing YaverFeedbackPane. Reuses all the auth +
    // black-box plumbing already wired for those entry points.
    guard let win = view.window else { return }
    YaverFeedbackPane.shared.present(in: win)
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
