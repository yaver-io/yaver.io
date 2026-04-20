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
 * Copy YaverHotReload native module files into the iOS project directory
 * AND register them in project.pbxproj so Xcode actually compiles them.
 *
 * The file-copy step uses withDangerousMod; the project registration
 * uses withXcodeProject. Both are needed — if we only copy the files
 * they sit in the filesystem unreferenced, Xcode ignores them, the
 * final .ipa has no `YaverHotReload` class, and the agent's
 * `reload_bundle` broadcast silently fails to load a new Hermes bundle.
 * This is a bug we've hit repeatedly; keep both steps wired.
 */
function withYaverHotReloadNativeModule(config) {
  // Step 1 — copy source files onto disk.
  config = withDangerousMod(config, [
    "ios",
    (config) => {
      const sdkIosDir = path.resolve(__dirname, "ios");
      const appName = config.modRequest.projectName;
      if (!appName) return config;
      const targetDir = path.join(
        config.modRequest.platformProjectRoot,
        appName
      );

      const filesToCopy = ["YaverHotReload.swift", "YaverHotReload.m"];
      for (const fileName of filesToCopy) {
        const src = path.join(sdkIosDir, fileName);
        const dst = path.join(targetDir, fileName);
        if (fs.existsSync(src)) {
          // Always overwrite so bumping the SDK version actually
          // picks up the new native code on the next prebuild
          // instead of silently keeping a stale copy.
          fs.copyFileSync(src, dst);
        }
      }

      return config;
    },
  ]);

  // Step 2 — register the files in project.pbxproj so Xcode compiles
  // them into the app target. Without this, the files exist on disk
  // but are invisible to the build system.
  config = withXcodeProject(config, (config) => {
    const proj = config.modResults;
    const appName = config.modRequest.projectName;
    if (!appName) return config;

    // Look up the group key for the app's source folder (same group
    // that holds AppDelegate.swift). Expo's naming is consistent —
    // projectName === group name in the tree.
    const groupKey =
      proj.findPBXGroupKey({ name: appName }) ||
      proj.findPBXGroupKey({ path: appName });
    if (!groupKey) return config;

    // Target UUID — getFirstTarget is the app target in a standard
    // Expo project. For multi-target projects, users can opt out via
    // enableHotReload: false.
    const target = proj.getFirstTarget();
    if (!target || !target.uuid) return config;

    const filesToAdd = ["YaverHotReload.swift", "YaverHotReload.m"];
    for (const fileName of filesToAdd) {
      const relPath = `${appName}/${fileName}`;
      // addSourceFile registers PBXFileReference + PBXBuildFile, adds
      // the file to the group, and wires it into the target's
      // Sources build phase — which is exactly what we need. It's
      // idempotent in practice because the sdk-level copyFileSync
      // step always writes the file, and addSourceFile is a no-op if
      // the reference already exists.
      if (!proj.hasFile || !proj.hasFile(relPath)) {
        try {
          proj.addSourceFile(relPath, { target: target.uuid }, groupKey);
        } catch (e) {
          // If a duplicate slips through, xcode-lib throws; safe to
          // ignore since the file already being registered is the
          // desired state.
        }
      }
    }

    return config;
  });

  return config;
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
    // Crash-revert safety net: clear the boot-attempt counter once
    // RN renders its first frame, OR after 10 s of uptime — whichever
    // fires first. If neither fires (bundle crashes before render),
    // YaverHotReload.bundleURL() will eventually revert to the
    // TestFlight-installed bundle after 3 failed boots. See
    // YaverHotReload.swift for the full state machine.
    NotificationCenter.default.addObserver(
      forName: NSNotification.Name(rawValue: "RCTContentDidAppearNotification"),
      object: nil,
      queue: .main
    ) { _ in YaverHotReload.markBootSuccessful() }
    DispatchQueue.main.asyncAfter(deadline: .now() + 10) {
      YaverHotReload.markBootSuccessful()
    }
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

    // Crash-revert safety net: clear the boot-attempt counter once
    // the React context initializes (bundle loaded successfully), AND
    // via a 10-s fallback Handler in case that listener never fires
    // (e.g. infinite loop in root component). If neither fires,
    // YaverHotReloadModule.getSavedBundleFile() reverts to the
    // APK-bundled bundle after 3 failed cold starts. Parity with
    // YaverHotReload.swift on iOS.
    if (contents.includes("onCreate()") && !contents.includes("yaverHotReloadBootListener")) {
      const onCreateIdx = contents.indexOf("onCreate()");
      const braceIdx = contents.indexOf("{", onCreateIdx);
      const insertionPoint = braceIdx + 1;
      const bootGuard = `
    // Yaver Feedback SDK hot-reload crash-revert safety net
    final android.content.Context yaverHotReloadCtx = getApplicationContext();
    new android.os.Handler(android.os.Looper.getMainLooper()).postDelayed(
        new Runnable() {
          @Override public void run() {
            io.yaver.feedback.YaverHotReloadModule.markBootSuccessful(yaverHotReloadCtx);
          }
        },
        10000
    );
    try {
      com.facebook.react.ReactInstanceManager yaverRim = getReactNativeHost().getReactInstanceManager();
      yaverRim.addReactInstanceEventListener(new com.facebook.react.ReactInstanceEventListener() {
        @Override public void onReactContextInitialized(com.facebook.react.bridge.ReactContext ctx) {
          io.yaver.feedback.YaverHotReloadModule.markBootSuccessful(yaverHotReloadCtx);
        }
      });
    } catch (Throwable yaverHotReloadBootListener) {
      // Older/newer RN versions may not expose ReactInstanceEventListener
      // exactly like this; the 10-s fallback above still covers us.
    }
`;
      contents =
        contents.slice(0, insertionPoint) + bootGuard + contents.slice(insertionPoint);
    }

    config.modResults.contents = contents;
    return config;
  });

  return config;
}

function withYaverFeedback(config, props) {
  config = withYaverFeedbackIOS(config);
  config = withYaverFeedbackAndroid(config);

  // Hot reload native module is ON by default — it's the SDK's whole
  // point. TestFlight / Play Store standalone builds have no Metro
  // dev server, so without the YaverHotReload native module the
  // agent's `reload_bundle` broadcast silently no-ops: the SDK falls
  // through to DevSettings.reload() which does nothing in Release
  // builds. Apps that specifically don't want the plugin mutating
  // their AppDelegate / MainApplication can opt out with
  //   ["yaver-feedback-react-native", { "enableHotReload": false }]
  const enableHotReload = props?.enableHotReload !== false;
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
