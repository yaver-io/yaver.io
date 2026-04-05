/**
 * Expo config plugin for yaver-feedback-react-native.
 *
 * Adds required native permissions for the feedback SDK:
 * - iOS: Camera + Microphone usage descriptions
 * - Android: CAMERA + RECORD_AUDIO permissions
 *
 * Usage in app.json:
 *   { "expo": { "plugins": ["yaver-feedback-react-native"] } }
 */
const {
  withInfoPlist,
  withAndroidManifest,
  createRunOncePlugin,
} = require("@expo/config-plugins");

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

function withYaverFeedback(config) {
  config = withYaverFeedbackIOS(config);
  config = withYaverFeedbackAndroid(config);
  return config;
}

module.exports = createRunOncePlugin(
  withYaverFeedback,
  pkg.name,
  pkg.version
);
