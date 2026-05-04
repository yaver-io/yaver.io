import { NativeModules, NativeEventEmitter, Platform } from "react-native";

const { YaverBundleLoader } = NativeModules;
const { YaverInfo } = NativeModules;

const emitter = YaverBundleLoader
  ? new NativeEventEmitter(YaverBundleLoader)
  : null;

export interface BundleLoadResult {
  loaded: boolean;
  url?: string;
  /** True when loadAppIfChanged short-circuited because the agent reported
   * the new bundle has the same md5 as the one already running. */
  skipped?: boolean;
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
  moduleName: string = "main",
  headers?: Record<string, string>
): Promise<BundleLoadResult> {
  if (!YaverBundleLoader) {
    throw new Error("YaverBundleLoader native module not available");
  }
  return YaverBundleLoader.loadBundle(bundleUrl, moduleName, headers || {});
}

/**
 * Iteration optimization: like loadApp, but skips download + bridge
 * reload entirely when the freshly-built bundle's md5 matches what's
 * already loaded. Use this from in-app reload flows (Hot Reload tab,
 * DevPreview banner) — NOT from the Apps-tab "Open in Yaver" entry,
 * because a backed-out user expects "Open" to re-enter the guest even
 * when the bytes haven't changed.
 *
 * Falls through to full loadApp on:
 *   - empty / missing expectedMd5 (initial load — agent may also omit it)
 *   - native module without getLoadedBundleMd5 (Android, older Yaver)
 *   - any error reading the persisted md5
 *   - md5 mismatch (the normal "user changed JS" path)
 */
export async function loadAppIfChanged(
  bundleUrl: string,
  moduleName: string = "main",
  expectedMd5: string | undefined | null,
  headers?: Record<string, string>
): Promise<BundleLoadResult> {
  if (!YaverBundleLoader) {
    throw new Error("YaverBundleLoader native module not available");
  }
  if (expectedMd5 && YaverBundleLoader.getLoadedBundleMd5) {
    try {
      const loadedMd5: string = await YaverBundleLoader.getLoadedBundleMd5();
      if (loadedMd5 && loadedMd5 === expectedMd5) {
        const stillThere = await isBundleLoaded();
        if (stillThere) {
          return { loaded: true, skipped: true, url: bundleUrl };
        }
      }
    } catch {
      // Fall through to full reload on any check failure.
    }
  }
  return YaverBundleLoader.loadBundle(bundleUrl, moduleName, headers || {});
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
  if (YaverBundleLoader?.getAvailableModules) {
    return YaverBundleLoader.getAvailableModules();
  }
  if (YaverInfo?.getAvailableModules) {
    return YaverInfo.getAvailableModules();
  }
  return [];
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
