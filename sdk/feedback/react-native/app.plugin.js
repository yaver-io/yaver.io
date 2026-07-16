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
    // Camera + mic for screenshot/voice. The rest are required to
    // make `react-native-record-screen` actually start on modern
    // Android — without FOREGROUND_SERVICE_MEDIA_PROJECTION
    // (API 34+, mandatory) startRecording throws SecurityException,
    // and without POST_NOTIFICATIONS (API 33+) the recording
    // notification fails to post which some OEMs use as a signal
    // to kill the projection a few seconds in. Inject all five
    // unconditionally — listing them does NOT trigger any user
    // prompt; the actual runtime dialogs only fire if the app
    // calls startVideoRecording().
    const requiredPermissions = [
      "android.permission.CAMERA",
      "android.permission.RECORD_AUDIO",
      "android.permission.FOREGROUND_SERVICE",
      "android.permission.FOREGROUND_SERVICE_MEDIA_PROJECTION",
      "android.permission.POST_NOTIFICATIONS",
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

    // 2. Add reload notification handler and bridge recreation logic.
    //
    // Must be inserted into the AppDelegate class — NOT ReactNativeDelegate
    // (a separate class below it in the same file). The handler accesses
    // `self.window`, `self.reactNativeDelegate`, `self.reactNativeFactory`,
    // and `self.bindReactNativeFactory(...)` — all of which only exist
    // on AppDelegate. An earlier version of this plugin used
    // lastIndexOf("}"), which in modern Expo templates lands inside
    // ReactNativeDelegate, producing build errors like
    //   "cannot find 'setupYaverHotReload' in scope"
    //   "value of type 'ReactNativeDelegate' has no member 'window'"
    //
    // The anchor we want is the closing brace of the AppDelegate class
    // declaration itself. Find its opening, then walk braces to its
    // matching close.
    const classCloseIndex = findAppDelegateClassClose(patched);
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

      // No explicit override needed — the new delegate's bundleURL()
      // has a patched branch that calls YaverHotReload.bundleURL()
      // first, which returns the file we just saved. That avoids
      // relying on a non-existent overrideBundleURL property on
      // ExpoReactNativeFactoryDelegate (which is what errored out in
      // 0.7.14's build on Expo SDK 54+).
      _ = bundleURL  // silence "unused" warning; the file path was
                    // baked into YaverHotReload by loadBundle() above
      let delegate = ReactNativeDelegate()
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

// Locate the closing brace of `class AppDelegate: ...` in an Expo
// Swift AppDelegate file. Returns the index of the matching `}` for
// the AppDelegate class's opening `{`, or -1 if not found. We need
// this because Expo's modern template declares TWO classes in the
// same file (AppDelegate + ReactNativeDelegate), and the SDK's
// reload handler only makes sense on the AppDelegate one.
function findAppDelegateClassClose(contents) {
  // Match both `public class AppDelegate` and `class AppDelegate`,
  // with either inheritance colon or a plain body.
  const headerMatch = contents.match(/class\s+AppDelegate\s*[:{][^{]*\{/);
  if (!headerMatch) return -1;
  const bodyStart = headerMatch.index + headerMatch[0].length - 1; // index of the opening `{`
  // Walk braces to find the matching close. Minimal — we don't try
  // to parse strings/comments because the file is machine-generated
  // by Expo, so braces inside strings are not a realistic concern
  // at this layer.
  let depth = 0;
  for (let i = bodyStart; i < contents.length; i++) {
    const ch = contents[i];
    if (ch === "{") depth++;
    else if (ch === "}") {
      depth--;
      if (depth === 0) return i;
    }
  }
  return -1;
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

  // Patch MainApplication to register the package and use hot bundle.
  //
  // MainApplication is Kotlin on React Native 0.73+ (every current Expo
  // template) and Java before that. The two need genuinely different code —
  // `new Foo()`, `final`, and `@Override` are all syntax errors in Kotlin —
  // so branch on the language Expo reports rather than assuming.
  config = withMainApplication(config, (config) => {
    if (config.modResults.contents.includes("YaverHotReload")) {
      return config;
    }
    config.modResults.contents =
      config.modResults.language === "kt"
        ? patchMainApplicationKotlin(config.modResults.contents)
        : patchMainApplicationJava(config.modResults.contents);
    return config;
  });

  return config;
}

/**
 * Insert `snippet` immediately before the closing brace of the method whose
 * signature contains `anchor`, by matching braces from the method's opening
 * one.
 *
 * The boot guard has to run at the END of onCreate: it touches
 * reactNativeHost, and reaching that before super.onCreate() and
 * SoLoader.init() would initialise React before its native libraries are
 * loaded. Returns the contents unchanged if the anchor isn't found — a
 * missing safety net is survivable, a corrupted MainApplication is not.
 */
function insertAtEndOfMethod(contents, anchor, snippet) {
  const anchorIdx = contents.indexOf(anchor);
  if (anchorIdx === -1) return contents;
  const open = contents.indexOf("{", anchorIdx);
  if (open === -1) return contents;

  let depth = 0;
  for (let i = open; i < contents.length; i++) {
    const ch = contents[i];
    if (ch === "{") depth++;
    else if (ch === "}") {
      depth--;
      if (depth === 0) {
        return contents.slice(0, i) + snippet + contents.slice(i);
      }
    }
  }
  return contents;
}

/** Kotlin MainApplication (React Native 0.73+). */
function patchMainApplicationKotlin(contents) {
  // Imports. Kotlin takes no semicolons.
  contents = contents.replace(
    "import com.facebook.react.ReactApplication",
    "import com.facebook.react.ReactApplication\nimport io.yaver.feedback.YaverHotReloadModule\nimport io.yaver.feedback.YaverHotReloadPackage"
  );

  // Register the package. Anchor on `return packages` rather than on
  // `packages.add(` — the template's only occurrence of that is inside a
  // commented-out example line.
  contents = contents.replace(
    /(\n([ \t]*)return packages\n)/,
    "\n$2packages.add(YaverHotReloadPackage())\n$1"
  );

  // Load a hot-pushed bundle when one is present. Inserted before
  // getJSMainModuleName, which every Expo template defines.
  if (!contents.includes("getJSBundleFile")) {
    contents = contents.replace(
      /(\n([ \t]*)override fun getJSMainModuleName\(\))/,
      `
$2override fun getJSBundleFile(): String? {
$2  // Yaver Feedback SDK: load hot-reloaded bundle if available
$2  val hotBundle = YaverHotReloadModule.getSavedBundleFile(application.applicationContext)
$2  return hotBundle?.absolutePath ?: super.getJSBundleFile()
$2}
$1`
    );
  }

  // Crash-revert safety net: clear the boot-attempt counter once the React
  // context initialises (bundle loaded successfully), AND via a 10-s fallback
  // in case that listener never fires (e.g. an infinite loop in the root
  // component). If neither fires, YaverHotReloadModule.getSavedBundleFile()
  // reverts to the APK-bundled bundle after 3 failed cold starts. Parity with
  // YaverHotReload.swift on iOS.
  if (!contents.includes("yaverHotReloadBootListener")) {
    contents = insertAtEndOfMethod(
      contents,
      "override fun onCreate()",
      `
    // Yaver Feedback SDK hot-reload crash-revert safety net
    val yaverHotReloadCtx: android.content.Context = applicationContext
    android.os.Handler(android.os.Looper.getMainLooper()).postDelayed(
      { YaverHotReloadModule.markBootSuccessful(yaverHotReloadCtx) },
      10000
    )
    try {
      reactNativeHost.reactInstanceManager.addReactInstanceEventListener {
        YaverHotReloadModule.markBootSuccessful(yaverHotReloadCtx)
      }
    } catch (yaverHotReloadBootListener: Throwable) {
      // Bridgeless / New Architecture does not expose reactInstanceManager;
      // the 10-s fallback above still covers us.
    }
`
    );
  }

  return contents;
}

/** Java MainApplication (React Native < 0.73). */
function patchMainApplicationJava(contents) {
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

  return contents;
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
