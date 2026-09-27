import Constants from "expo-constants";
import { Platform } from "react-native";
import { getBuildNumber, getInstallerPackageNameSync } from "react-native-device-info";

function nativeBuildNumber(): string {
  try {
    return String(getBuildNumber() || "").trim();
  } catch {
    return "";
  }
}

function firstNonEmpty(...values: unknown[]): string {
  for (const value of values) {
    const candidate = String(value ?? "").trim();
    if (candidate) return candidate;
  }
  return "";
}

const VERSION = firstNonEmpty(
  Constants.nativeAppVersion,
  Constants.expoConfig?.version,
) || "?";
const BUILD = firstNonEmpty(
  Constants.nativeBuildVersion,
  Constants.expoConfig?.ios?.buildNumber,
  Constants.expoConfig?.android?.versionCode,
  nativeBuildNumber(),
);

/** Human provenance for the installed mobile binary. On iOS the native
 * module inspects the provisioning profile and App Store receipt, so this is
 * operational truth rather than a build-number guess. */
export function mobileDistributionLabel(): string {
  if (Platform.OS === "web") return mobileRuntimeMode() === "dogfood" ? "Dogfood" : "Browser";
  if (__DEV__) return "Development";
  try {
    const installer = String(getInstallerPackageNameSync() || "").trim();
    if (Platform.OS === "ios") {
      if (installer === "TestFlight") return "TestFlight";
      if (installer === "AppStore") return "App Store";
      return "Local build";
    }
    if (/vending|google\.android\.feedback/i.test(installer)) return "Google Play";
    return installer ? "Installed build" : "Local build";
  } catch {
    return "Installed build";
  }
}

export type MobileRuntimeMode = "native" | "dogfood";
export type ClientSessionLane = "yaver-native" | "browser" | "hermes" | "webrtc";
export type ClientSessionUsageMode = "chat-only" | "reload-only" | "reload-and-chat";
export type ClientDeviceClass = "phone" | "tablet" | "desktop" | "tv" | "car" | "watch" | "xr" | "browser";

export type ClientSessionSettings = {
  appName: string;
  appVersion: string;
  buildNumber: string;
  surface: string;
  clientSurface: string;
  platform: string;
  deviceClass: ClientDeviceClass;
  lane: ClientSessionLane;
  runtimeMode: MobileRuntimeMode;
  dogfood: boolean;
  usageMode: ClientSessionUsageMode;
  chatEnabled: boolean;
  renderEnabled: boolean;
  renderModes: string[];
  codecs: string[];
  inputModes: string[];
};

function surfaceCapabilities(deviceClass: ClientDeviceClass) {
  switch (deviceClass) {
    case "tv":
      return { renderModes: ["webrtc", "frames"], codecs: ["h264", "jpeg"], inputModes: ["controller", "text", "voice"] };
    case "xr":
      return { renderModes: ["webrtc", "frames"], codecs: ["h264", "jpeg"], inputModes: ["gaze", "pointer", "voice", "text"] };
    case "watch":
    case "car":
      return { renderModes: [], codecs: [], inputModes: ["voice", "text"] };
    case "browser":
      return { renderModes: ["iframe", "webrtc", "frames"], codecs: ["h264", "jpeg"], inputModes: ["pointer", "keyboard", "touch", "text"] };
    default:
      return { renderModes: ["hermes", "webrtc", "iframe", "frames"], codecs: ["h264", "jpeg"], inputModes: ["touch", "keyboard", "text", "voice"] };
  }
}

/** Installed/native versus an attached Dogfood copy. The attached Yaver app
 * runs as RN-web and receives this sentinel from the native host. */
export function mobileRuntimeMode(scope: any = globalThis): MobileRuntimeMode {
  if (Platform.OS !== "web") return "native";
  try {
    const sentinel = scope?.localStorage?.getItem?.("yaver.attach.mode");
    return sentinel === "1" || sentinel === "true" ? "dogfood" : "native";
  } catch {
    return "native";
  }
}

export function mobileRuntimeIdentity(scope: any = globalThis) {
  return {
    appName: "Yaver mobile",
    appVersion: VERSION,
    buildNumber: BUILD,
    runtimeMode: mobileRuntimeMode(scope),
  } as const;
}

function detectedMobileSurface(): { surface: string; platform: string; deviceClass: ClientDeviceClass } {
  const constants = (Platform.constants ?? {}) as Record<string, unknown>;
  const uiMode = String(constants.uiMode ?? "").toLowerCase();
  if ((Platform as any).isVision) return { surface: "vision-pro", platform: "visionos", deviceClass: "xr" };
  if (Platform.isTV) {
    return Platform.OS === "ios"
      ? { surface: "apple-tv", platform: "tvos", deviceClass: "tv" }
      : { surface: "android-tv", platform: "android", deviceClass: "tv" };
  }
  if (uiMode === "car") return { surface: "android-auto", platform: "android", deviceClass: "car" };
  if (uiMode === "watch") return { surface: "wear-os", platform: "wearos", deviceClass: "watch" };
  if (uiMode === "vrheadset" || uiMode === "xr") return { surface: "android-xr", platform: "android-xr", deviceClass: "xr" };
  if (Platform.OS === "web") return { surface: "yaver-mobile-web", platform: "web", deviceClass: "browser" };
  if (Platform.OS === "macos" || Platform.OS === "windows") {
    return { surface: "yaver-native-desktop", platform: Platform.OS, deviceClass: "desktop" };
  }
  return {
    surface: "yaver-mobile-app",
    platform: Platform.OS,
    deviceClass: Platform.OS === "ios" && Platform.isPad ? "tablet" : "phone",
  };
}

export function mobileSessionSettings(options: {
  surface?: string;
  lane?: ClientSessionLane;
  dogfood?: boolean;
  usageMode?: ClientSessionUsageMode;
  platform?: string;
  deviceClass?: ClientDeviceClass;
} = {}): ClientSessionSettings {
  const identity = mobileRuntimeIdentity();
  const dogfood = options.dogfood ?? identity.runtimeMode === "dogfood";
  const usageMode = options.usageMode ?? "chat-only";
  const detected = detectedMobileSurface();
  const clientSurface = options.surface ?? detected.surface;
  const platform = options.platform ?? detected.platform;
  const deviceClass = options.deviceClass ?? detected.deviceClass;
  const capabilities = surfaceCapabilities(deviceClass);
  return {
    ...identity,
    surface: clientSurface,
    clientSurface,
    platform,
    deviceClass,
    lane: options.lane ?? (dogfood ? "browser" : "yaver-native"),
    runtimeMode: dogfood ? "dogfood" : identity.runtimeMode,
    dogfood,
    usageMode,
    chatEnabled: usageMode !== "reload-only",
    renderEnabled: usageMode !== "chat-only",
    ...capabilities,
  };
}

export const APP_VERSION = VERSION;
export const APP_BUILD = BUILD;

// Short tag for debug surfaces — alerts, error footers, copy-to-clipboard
// payloads. Keeps "1.18.36 (b304)" short so it doesn't dominate the modal.
export function appTag(): string {
  const mode = mobileRuntimeMode() === "dogfood" ? "Dogfood" : "Native";
  return BUILD ? `Yaver mobile ${VERSION} (b${BUILD}) · ${mode}` : `Yaver mobile ${VERSION} · ${mode}`;
}
