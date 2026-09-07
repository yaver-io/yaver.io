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
// isomorphic-git publishes a Node-specific CommonJS entry behind the `node`
// export condition. Expo's production iOS asset export includes that condition,
// even though the resulting bundle runs in Hermes, so Metro otherwise selects
// index.cjs and then fails to resolve Node's built-in `crypto` module. Pin the
// package root to its browser ESM build for every client platform. Subpaths such
// as isomorphic-git/http/web keep using their own package exports.
const browserOnlyModules = {
  "isomorphic-git": path.join(mobileNodeModules, "isomorphic-git", "index.js"),
};
config.resolver.disableHierarchicalLookup = false;
config.resolver.extraNodeModules = {
  ...(config.resolver.extraNodeModules || {}),
  ...pinnedCoreModules,
};
config.resolver.resolveRequest = (context, moduleName, platform) => {
  // Expo normally aliases react-native to react-native-web. The shared-SDK
  // pin must preserve that platform decision; forcing the native package on
  // web imports ReactFabric and fails the entire RN-web bundle before #root
  // can mount.
  const pinned = moduleName === "react-native" && platform === "web"
    ? path.join(mobileNodeModules, "react-native-web")
    : browserOnlyModules[moduleName] || pinnedCoreModules[moduleName];
  return context.resolveRequest(context, pinned || moduleName, platform);
};

if (!config.resolver.assetExts.includes("bin")) {
  config.resolver.assetExts.push("bin");
}

module.exports = config;
