import Constants from "expo-constants";

const VERSION = Constants.expoConfig?.version ?? "?";
const BUILD =
  Constants.expoConfig?.ios?.buildNumber ??
  Constants.expoConfig?.android?.versionCode?.toString() ??
  "";

export const APP_VERSION = VERSION;
export const APP_BUILD = BUILD;

// Short tag for debug surfaces — alerts, error footers, copy-to-clipboard
// payloads. Keeps "1.18.36 (b304)" short so it doesn't dominate the modal.
export function appTag(): string {
  return BUILD ? `Yaver mobile ${VERSION} (b${BUILD})` : `Yaver mobile ${VERSION}`;
}
