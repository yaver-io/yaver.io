import { NativeModules, NativeEventEmitter, Platform } from "react-native";

const { YaverBundleLoader } = NativeModules;

const emitter = YaverBundleLoader
  ? new NativeEventEmitter(YaverBundleLoader)
  : null;

export interface BundleLoadResult {
  loaded: boolean;
  url?: string;
}

/**
 * Load an external React Native JS bundle and run it full-screen inside the Yaver app.
 * The loaded app has access to all native modules compiled into Yaver (camera, BLE, GPS, etc.).
 *
 * @param bundleUrl - Metro bundle URL, e.g. "http://192.168.1.10:18080/dev/index.bundle?platform=ios&dev=true"
 * @param moduleName - The registered app name (usually "main" for Expo apps)
 */
export async function loadApp(
  bundleUrl: string,
  moduleName: string = "main"
): Promise<BundleLoadResult> {
  if (!YaverBundleLoader) {
    throw new Error("YaverBundleLoader native module not available");
  }
  return YaverBundleLoader.loadBundle(bundleUrl, moduleName);
}

/**
 * Unload the current external app and return to the Yaver UI.
 */
export async function unloadApp(): Promise<{ unloaded: boolean }> {
  if (!YaverBundleLoader) {
    throw new Error("YaverBundleLoader native module not available");
  }
  return YaverBundleLoader.unloadBundle();
}

/**
 * Get the list of native modules available in this Yaver binary.
 * Used for compatibility checking before loading a bundle.
 */
export async function getAvailableModules(): Promise<string[]> {
  if (!YaverBundleLoader) return [];
  return YaverBundleLoader.getAvailableModules();
}

/**
 * Check if an external bundle is currently loaded.
 */
export async function isBundleLoaded(): Promise<boolean> {
  if (!YaverBundleLoader) return false;
  const result = await YaverBundleLoader.isLoaded();
  return result?.loaded ?? false;
}

/**
 * Build the Metro bundle URL for the current platform.
 * The URL goes through the agent's /dev/* proxy which works over relay.
 */
export function buildNativeBundleUrl(
  baseUrl: string,
  dev: boolean = true
): string {
  const platform = Platform.OS; // "ios" or "android"
  return `${baseUrl}/dev/index.bundle?platform=${platform}&dev=${dev}&minify=${!dev}`;
}

/**
 * Subscribe to bundle lifecycle events.
 */
export function onBundleEvent(
  event: "onBundleLoaded" | "onBundleError" | "onBundleUnloaded",
  callback: (data: any) => void
) {
  if (!emitter) return { remove: () => {} };
  return emitter.addListener(event, callback);
}
