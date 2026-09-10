// Metro config for the Yaver mobile app.
//
// The only customization: register `.bin` as a bundled asset extension so
// the on-device whisper STT model (assets/models/ggml-whisper-tiny.bin)
// can be loaded via `require()` and embedded into the app binary by Expo.
// Without this, metro treats `.bin` as source and the model never ships —
// whisper.rn then fails with "Failed to load the model" (the on-device
// voice path the Tasks tab mic relies on).
const { getDefaultConfig } = require("expo/metro-config");
const fs = require("fs");
const path = require("path");

const config = getDefaultConfig(__dirname);
const mobileNodeModules = path.resolve(__dirname, "node_modules");
const physicalMobileNodeModules = fs.realpathSync(mobileNodeModules);

// React Native's Gradle task forwards its own worker cap to the first Metro
// bundle, but expo-updates starts a second Metro instance directly while it
// creates app.manifest. That path never sees extraPackagerArgs and otherwise
// starts one jest-worker process per CPU, which can OOM an otherwise bounded
// Android release on a 4 GiB worker after native compilation has succeeded.
// Put the cap in Metro's shared config so every Metro entrypoint observes it.
const configuredMetroWorkers = process.env.YAVER_ANDROID_METRO_WORKERS;
if (configuredMetroWorkers !== undefined) {
  const metroWorkers = Number(configuredMetroWorkers);
  if (!Number.isInteger(metroWorkers) || metroWorkers < 1) {
    throw new Error(
      `YAVER_ANDROID_METRO_WORKERS must be a positive integer; got: ${configuredMetroWorkers}`
    );
  }
  config.maxWorkers = metroWorkers;
}

// Yaver mobile is the first real consumer of the published Dogfood runtime in
// sdk/feedback/react-native. Watch only that SDK package (not the monorepo root)
// so Metro can compile the exact source third-party apps receive without
// pulling unrelated workspaces into module discovery.
config.watchFolders = [
  ...(config.watchFolders || []),
  path.resolve(__dirname, "../sdk/feedback/react-native"),
  // When node_modules is a symlink to a mounted build volume, Metro can follow
  // the link only if its physical target is inside the file map. Otherwise the
  // browser receives a valid logical bundle URL and Metro still answers 404
  // "none of these files exist" for files that are plainly on disk.
  ...(physicalMobileNodeModules === mobileNodeModules ? [] : [physicalMobileNodeModules]),
];

// Files under the sibling SDK are outside `mobile/`, so Metro's normal
// hierarchical lookup starts beside that file. CI intentionally installs only
// mobile/node_modules; without this explicit workspace root, React/React Native
// resolve locally only when an unrelated sdk/node_modules happens to exist.
// That false green reached Xcode's expo-updates asset phase before failing.
config.resolver.nodeModulesPaths = [
  ...(config.resolver.nodeModulesPaths || []),
  mobileNodeModules,
];

// The sibling SDK imports React hooks. Pin only the core runtime entrypoints so
// the SDK and host share one React instance. Keep hierarchical lookup enabled:
// npm may install valid transitive dependencies under their owning package,
// and disabling that lookup makes Metro reject those nested modules.
const pinnedCoreModules = {
  react: path.join(mobileNodeModules, "react"),
  "react/jsx-runtime": path.join(mobileNodeModules, "react/jsx-runtime.js"),
  "react/jsx-dev-runtime": path.join(mobileNodeModules, "react/jsx-dev-runtime.js"),
  "react-native": path.join(mobileNodeModules, "react-native"),
};
config.resolver.disableHierarchicalLookup = false;
config.resolver.extraNodeModules = {
  ...(config.resolver.extraNodeModules || {}),
  ...pinnedCoreModules,
};
config.resolver.resolveRequest = (context, moduleName, platform) => {
  // Expo Web derives its HMR entry point from document.currentScript.src.
  // When Dogfood is mounted through the agent proxy, that URL contains the
  // transport prefix (`/dev/`, `/dev-web/`, or the device-scoped equivalent).
  // Metro interprets the prefix as a project directory and otherwise crashes
  // after the first successful paint trying to resolve
  // `./dev/node_modules/expo-router/entry`. The agent strips this on current
  // builds; keep the app-side resolver tolerant so Dogfood also recovers on a
  // box that is still running the previous agent binary.
  const resolvedModuleName = platform === "web"
    ? moduleName.replace(
        /^\.\/(?:(?:d|peer)\/[^/]+\/)?dev(?:-web)?\/(?=node_modules\/)/,
        "./"
      )
    : moduleName;
  // isomorphic-git exposes a Node-specific CommonJS condition that imports
  // the built-in `crypto` module. Expo's release asset pass can assert that
  // condition even for an iOS archive, which makes Metro select index.cjs and
  // fail after the native build has already spent minutes compiling. The ESM
  // entry is the package's portable implementation (sha.js + Web APIs), so
  // pin it for every React Native surface. Do the same for its web transport
  // subpath to keep both imports on one module format.
  const portableGitModule = resolvedModuleName === "isomorphic-git"
    ? path.join(mobileNodeModules, "isomorphic-git", "index.js")
    : resolvedModuleName === "isomorphic-git/http/web"
      ? path.join(mobileNodeModules, "isomorphic-git", "http", "web", "index.js")
      : null;
  // Expo normally aliases react-native to react-native-web. The shared-SDK
  // pin must preserve that platform decision; forcing the native package on
  // web imports ReactFabric and fails the entire RN-web bundle before #root
  // can mount.
  const pinned = resolvedModuleName === "react-native" && platform === "web"
    ? path.join(mobileNodeModules, "react-native-web")
    : pinnedCoreModules[resolvedModuleName];
  return context.resolveRequest(context, portableGitModule || pinned || resolvedModuleName, platform);
};

if (!config.resolver.assetExts.includes("bin")) {
  config.resolver.assetExts.push("bin");
}

module.exports = config;
