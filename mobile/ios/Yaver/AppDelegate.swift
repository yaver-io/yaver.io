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
    // Clean up any stale guest bundle from previous sessions so Yaver's own UI loads
    let docsPath = NSSearchPathForDirectoriesInDomains(.documentDirectory, .userDomainMask, true).first!
    let staleBundlePath = (docsPath as NSString).appendingPathComponent("bundles/main.jsbundle")
    if FileManager.default.fileExists(atPath: staleBundlePath) {
      NSLog("[AppDelegate] Cleaning stale guest bundle at startup")
      try? FileManager.default.removeItem(atPath: staleBundlePath)
    }
    UserDefaults.standard.removeObject(forKey: "yaverLoadedModuleName")
    UserDefaults.standard.removeObject(forKey: "yaverCurrentModuleName")

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
    safeReloadBridge(moduleName: moduleName)
  }

  @objc func handleJSLoadFailure(_ notification: Notification) {
    let error = (notification.userInfo?["error"] as? Error)?.localizedDescription ?? "unknown"
    NSLog("[AppDelegate] JS LOAD FAILED: %@", error)

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

    NSLog("[AppDelegate] safeReloadBridge: bundleURL=%@ moduleName=%@", bundleURL.path, resolvedModule)

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

    // 1. Show spinner (stops JS rendering)
    let placeholder = UIView(frame: window?.bounds ?? .zero)
    placeholder.backgroundColor = .systemBackground
    let spinner = UIActivityIndicatorView(style: .large)
    spinner.center = placeholder.center
    spinner.startAnimating()
    placeholder.addSubview(spinner)
    window?.rootViewController?.view = placeholder

    // 2. Capture weak reference to old bridge before invalidating
    var oldBridgeWeak: RCTBridge? = nil
    if let rootView = window?.rootViewController?.view as? RCTRootView {
      oldBridgeWeak = rootView.bridge
      NSLog("[AppDelegate] invalidating old bridge...")
      rootView.bridge.invalidate()
    } else {
      NSLog("[AppDelegate] no existing RCTRootView — creating fresh bridge")
    }

    weak var weakOldBridge: RCTBridge? = oldBridgeWeak
    oldBridgeWeak = nil // release strong ref

    // 3. Wait for actual deallocation using polling (replaces fixed sleep)
    waitForBridgeDeallocation(weakRef: weakOldBridge, timeout: 3.0) { [weak self] in
      guard let self = self, let window = self.window else { return }
      self.initGuestBridge(bundleURL: bundleURL, moduleName: resolvedModule, window: window)
    }
  }

  /// Polls until the weak reference goes nil (bridge deallocated),
  /// then calls completion on the main thread. Falls back to timeout.
  private func waitForBridgeDeallocation(
    weakRef: AnyObject?,
    timeout: TimeInterval,
    completion: @escaping () -> Void
  ) {
    // If no old bridge, proceed immediately
    guard weakRef != nil else {
      NSLog("[AppDelegate] no old bridge to wait for — proceeding immediately")
      DispatchQueue.main.async { completion() }
      return
    }

    let deadline = Date().addingTimeInterval(timeout)
    let checkInterval: TimeInterval = 0.05

    func check() {
      if weakRef == nil {
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
  private func initGuestBridge(bundleURL: URL, moduleName: String, window: UIWindow) {
    NSLog("[AppDelegate] creating New Arch factory bridge: url=%@ module=%@", bundleURL.path, moduleName)

    let delegate = ReactNativeDelegate()
    delegate.overrideBundleURL = bundleURL
    delegate.dependencyProvider = RCTAppDependencyProvider()

    let factory = ExpoReactNativeFactory(delegate: delegate)
    self.reactNativeDelegate = delegate
    self.reactNativeFactory = factory
    self.bindReactNativeFactory(factory)

    UserDefaults.standard.set(moduleName, forKey: "yaverCurrentModuleName")

    factory.startReactNative(
      withModuleName: moduleName,
      in: window,
      launchOptions: nil)

    isReloading = false
    NSLog("[AppDelegate] guest app loaded (New Arch): module=%@", moduleName)

    // Guest app is running. Shake phone to reveal "Back to Yaver" overlay.
    isGuestAppRunning = true
  }

  private var isGuestAppRunning = false
  private var backOverlay: UIView?
  private var overlayDismissTimer: Timer?

  // MARK: - Shake to reveal Back to Yaver

  /// Called by ShakeDetectingWindow when device is shaken while guest app is running.
  func handleShakeGesture() {
    guard isGuestAppRunning, let window = self.window else { return }

    // If overlay is already visible, dismiss it
    if backOverlay != nil {
      dismissOverlay()
      return
    }

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

    let reloadBtn = makeButton(title: "Reload App", icon: "arrow.clockwise", color: greenColor,
                               action: #selector(handleReloadTap))
    let backBtn = makeButton(title: "Back to Yaver", icon: "chevron.left", color: accentColor,
                             action: #selector(handleBackTap))

    let stack = UIStackView(arrangedSubviews: [reloadBtn, backBtn])
    stack.axis = .horizontal
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
    NSLog("[AppDelegate] Reload App tapped — rebuilding Hermes bundle")
    dismissOverlay()

    // Re-trigger the Hermes build+push flow by reloading from the saved bundle URL
    // The guest bridge will reload with the latest JS
    if let rootView = window?.rootViewController?.view as? RCTRootView {
      rootView.bridge.reload()
      NSLog("[AppDelegate] Guest bridge reloaded")
    }
  }

  @objc private func handleBackTap() {
    NSLog("[AppDelegate] Back to Yaver tapped")
    dismissOverlay()
    isGuestAppRunning = false
    NotificationCenter.default.post(name: Notification.Name("YaverBundleLoaderRestore"), object: nil)
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

    // Invalidate loaded bridge
    if let rootView = window?.rootViewController?.view as? RCTRootView {
      rootView.bridge.invalidate()
    }

    // Recreate with original Yaver bundle
    DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) { [weak self] in
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
class ShakeDetectingWindow: UIWindow {
  override func motionEnded(_ motion: UIEvent.EventSubtype, with event: UIEvent?) {
    super.motionEnded(motion, with: event)
    if motion == .motionShake {
      if let appDelegate = UIApplication.shared.delegate as? AppDelegate {
        appDelegate.handleShakeGesture()
      }
    }
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
