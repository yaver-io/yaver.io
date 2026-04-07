/**
 * Expo config plugin for yaver-feedback-react-native.
 *
 * Adds:
 * - iOS/Android permissions for camera + microphone (feedback screenshots/voice)
 * - iOS: YaverHotReload native module for Hermes bundle hot reload
 * - AppDelegate hook to load hot-reloaded bundles on startup
 *
 * Usage in app.json:
 *   { "expo": { "plugins": ["yaver-feedback-react-native"] } }
 */
// Resolve @expo/config-plugins from the host project's node_modules
// (not from the SDK's directory, which may be symlinked)
const configPluginsPath = require.resolve("@expo/config-plugins", {
  paths: [process.cwd()],
});
const {
  withInfoPlist,
  withAndroidManifest,
  withXcodeProject,
  withAppDelegate,
  withMainApplication,
  withDangerousMod,
  createRunOncePlugin,
} = require(configPluginsPath);
const path = require("path");
const fs = require("fs");

const pkg = require("./package.json");

function withYaverFeedbackIOS(config) {
  return withInfoPlist(config, (config) => {
    if (!config.modResults.NSCameraUsageDescription) {
      config.modResults.NSCameraUsageDescription =
        "Used for visual feedback screenshots during development";
    }
    if (!config.modResults.NSMicrophoneUsageDescription) {
      config.modResults.NSMicrophoneUsageDescription =
        "Used for voice annotations in feedback reports during development";
    }
    return config;
  });
}

function withYaverFeedbackAndroid(config) {
  return withAndroidManifest(config, (config) => {
    const manifest = config.modResults.manifest;

    if (!manifest["uses-permission"]) {
      manifest["uses-permission"] = [];
    }

    const permissions = manifest["uses-permission"];
    const requiredPermissions = [
      "android.permission.CAMERA",
      "android.permission.RECORD_AUDIO",
    ];

    for (const perm of requiredPermissions) {
      const exists = permissions.some(
        (p) => p.$?.["android:name"] === perm
      );
      if (!exists) {
        permissions.push({ $: { "android:name": perm } });
      }
    }

    return config;
  });
}

/**
 * Copy YaverHotReload native module files into the iOS project directory.
 * Uses withDangerousMod to copy files during prebuild. The files are
 * automatically picked up by Xcode since they're in the project directory.
 */
function withYaverHotReloadNativeModule(config) {
  return withDangerousMod(config, [
    "ios",
    (config) => {
      const sdkIosDir = path.resolve(__dirname, "ios");
      const appName = config.modRequest.projectName || "SFMG";
      const targetDir = path.join(
        config.modRequest.platformProjectRoot,
        appName
      );

      const filesToCopy = ["YaverHotReload.swift", "YaverHotReload.m"];
      for (const fileName of filesToCopy) {
        const src = path.join(sdkIosDir, fileName);
        const dst = path.join(targetDir, fileName);
        if (fs.existsSync(src) && !fs.existsSync(dst)) {
          fs.copyFileSync(src, dst);
        }
      }

      return config;
    },
  ]);
}

/**
 * Patch AppDelegate to:
 * 1. Return hot-reloaded bundle URL on startup (so reloaded bundle persists)
 * 2. Handle YaverHotReloadBundle notification to recreate the RN bridge
 *    with the new bundle (enables N reloads without app restart)
 *
 * Uses the same pattern as Yaver's own AppDelegate: tear down old bridge,
 * create new ExpoReactNativeFactory with overrideBundleURL, startReactNative.
 */
function withYaverAppDelegateHook(config) {
  return withAppDelegate(config, (config) => {
    const contents = config.modResults.contents;

    // Only patch if not already patched
    if (contents.includes("YaverHotReload")) {
      return config;
    }

    // For Swift AppDelegate (Expo SDK 50+)
    let patched = contents;

    // 1. Hook bundleURL() to return hot bundle on startup
    if (patched.includes("func bundleURL()")) {
      patched = patched.replace(
        /func bundleURL\(\) -> URL\? \{/,
        `func bundleURL() -> URL? {
    // Yaver Feedback SDK: load hot-reloaded bundle if available
    if let yaverBundle = YaverHotReload.bundleURL() { return yaverBundle }`
      );
    }

    // 2. Add reload notification handler and bridge recreation logic
    // Insert before the closing brace of the class
    const classCloseIndex = patched.lastIndexOf("}");
    if (classCloseIndex > 0) {
      const reloadHandler = `
  // MARK: - Yaver Feedback SDK Hot Reload

  private var yaverIsReloading = false

  private func setupYaverHotReload() {
    NotificationCenter.default.addObserver(
      self,
      selector: #selector(yaverHandleHotReload(_:)),
      name: Notification.Name("YaverHotReloadBundle"),
      object: nil
    )
  }

  @objc private func yaverHandleHotReload(_ notification: Notification) {
    guard !yaverIsReloading else { return }
    yaverIsReloading = true

    guard let bundlePath = notification.userInfo?["bundlePath"] as? String else {
      yaverIsReloading = false
      return
    }

    let bundleURL = URL(fileURLWithPath: bundlePath)
    guard FileManager.default.fileExists(atPath: bundlePath) else {
      NSLog("[YaverHotReload] bundle not found at %@", bundlePath)
      yaverIsReloading = false
      return
    }

    NSLog("[YaverHotReload] reloading bridge with %@", bundlePath)

    guard let window = self.window else {
      yaverIsReloading = false
      return
    }

    // Show loading placeholder
    let placeholder = UIView(frame: window.bounds)
    placeholder.backgroundColor = .black
    let spinner = UIActivityIndicatorView(style: .large)
    spinner.color = .white
    spinner.center = placeholder.center
    spinner.startAnimating()
    placeholder.addSubview(spinner)
    window.rootViewController?.view = placeholder

    // Brief delay for old bridge to tear down
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) { [weak self] in
      guard let self = self else { return }

      let delegate = ReactNativeDelegate()
      delegate.overrideBundleURL = bundleURL
      delegate.dependencyProvider = RCTAppDependencyProvider()

      let factory = ExpoReactNativeFactory(delegate: delegate)
      self.reactNativeDelegate = delegate
      self.reactNativeFactory = factory
      self.bindReactNativeFactory(factory)

      factory.startReactNative(
        withModuleName: "main",
        in: window,
        launchOptions: nil
      )
      self.yaverIsReloading = false
      NSLog("[YaverHotReload] bridge recreated successfully")
    }
  }
`;

      patched =
        patched.slice(0, classCloseIndex) +
        reloadHandler +
        patched.slice(classCloseIndex);
    }

    // 3. Call setupYaverHotReload() in didFinishLaunchingWithOptions
    if (patched.includes("super.application(application, didFinishLaunchingWithOptions:")) {
      patched = patched.replace(
        "super.application(application, didFinishLaunchingWithOptions:",
        "setupYaverHotReload()\n    return super.application(application, didFinishLaunchingWithOptions:"
      );
      // Remove the duplicate "return" if the original already had one
      patched = patched.replace("return setupYaverHotReload()", "setupYaverHotReload()");
    }

    config.modResults.contents = patched;
    return config;
  });
}

/**
 * Copy Android native module source files and register the package.
 * Also patches MainApplication to use hot-reloaded bundle on startup.
 */
function withYaverAndroidHotReload(config) {
  // Copy Java source files
  config = withDangerousMod(config, [
    "android",
    (config) => {
      const sdkAndroidDir = path.resolve(__dirname, "android", "src", "main", "java", "io", "yaver", "feedback");
      const targetDir = path.join(
        config.modRequest.platformProjectRoot,
        "app", "src", "main", "java", "io", "yaver", "feedback"
      );

      if (fs.existsSync(sdkAndroidDir)) {
        fs.mkdirSync(targetDir, { recursive: true });
        for (const file of fs.readdirSync(sdkAndroidDir)) {
          fs.copyFileSync(
            path.join(sdkAndroidDir, file),
            path.join(targetDir, file)
          );
        }
      }
      return config;
    },
  ]);

  // Patch MainApplication to register the package and use hot bundle
  config = withMainApplication(config, (config) => {
    let contents = config.modResults.contents;

    if (contents.includes("YaverHotReload")) {
      return config;
    }

    // Add import
    contents = contents.replace(
      "import com.facebook.react.ReactApplication",
      "import com.facebook.react.ReactApplication;\nimport io.yaver.feedback.YaverHotReloadPackage;\nimport io.yaver.feedback.YaverHotReloadModule;"
    );

    // Register package in getPackages()
    if (contents.includes("packages.add(")) {
      // Find the last packages.add() and add ours after
      const lastAdd = contents.lastIndexOf("packages.add(");
      const lineEnd = contents.indexOf("\n", lastAdd);
      contents =
        contents.slice(0, lineEnd + 1) +
        "      packages.add(new YaverHotReloadPackage());\n" +
        contents.slice(lineEnd + 1);
    }

    // Override getJSBundleFile to check for hot bundle
    if (contents.includes("getJSMainModuleName()") && !contents.includes("getJSBundleFile")) {
      const mainModuleIdx = contents.indexOf("getJSMainModuleName()");
      const methodStart = contents.lastIndexOf("@Override", mainModuleIdx);
      contents =
        contents.slice(0, methodStart) +
        `@Override
    protected String getJSBundleFile() {
      // Yaver Feedback SDK: load hot-reloaded bundle if available
      java.io.File hotBundle = YaverHotReloadModule.getSavedBundleFile(getApplicationContext());
      if (hotBundle != null) return hotBundle.getAbsolutePath();
      return super.getJSBundleFile();
    }

    ` +
        contents.slice(methodStart);
    }

    config.modResults.contents = contents;
    return config;
  });

  return config;
}

function withYaverFeedback(config, props) {
  config = withYaverFeedbackIOS(config);
  config = withYaverFeedbackAndroid(config);

  // Hot reload native module is opt-in via enableHotReload: true
  // Skip by default — apps running inside Yaver container get hot reload
  // via YaverBundleLoader, standalone dev builds use DevSettings.reload()
  const enableHotReload = props?.enableHotReload === true;
  if (enableHotReload) {
    config = withYaverHotReloadNativeModule(config);
    config = withYaverAppDelegateHook(config);
    config = withYaverAndroidHotReload(config);
  }

  return config;
}

module.exports = createRunOncePlugin(
  withYaverFeedback,
  pkg.name,
  pkg.version
);
