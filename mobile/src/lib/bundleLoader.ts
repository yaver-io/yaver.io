import { NativeModules, NativeEventEmitter, Platform } from "react-native";

const { YaverBundleLoader } = NativeModules;
const { YaverInfo } = NativeModules;

const emitter = YaverBundleLoader
  ? new NativeEventEmitter(YaverBundleLoader)
  : null;

// The native bundle loader ships on iOS and Android (YaverBundleLoaderModule,
// registered in MainApplication). It's absent only on web, or on an Android
// build predating the module. Callers should prefer isBundleLoaderAvailable()
// to gate UI; this message is the fallback when a load is attempted anyway.
function bundleLoaderUnavailableMessage(): string {
  return Platform.OS === "web"
    ? "Loading apps inside Yaver isn't available in the web preview — use the iOS or Android app."
    : "Yaver's bundle loader isn't available in this build. Update Yaver to the latest version and try again.";
}

/**
 * True when the native YaverBundleLoader module is present, i.e. this build
 * can mount a guest Hermes bundle. iOS + Android (≥ the build that shipped
 * YaverBundleLoaderModule) → true; web / older builds → false. Use this to
 * gate "Open in Yaver" / Hermes-reload UI instead of checking Platform.OS,
 * so the capability — not the platform name — drives the decision.
 */
export function isBundleLoaderAvailable(): boolean {
  return !!YaverBundleLoader;
}

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
    throw new Error(bundleLoaderUnavailableMessage());
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
    throw new Error(bundleLoaderUnavailableMessage());
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
    throw new Error(bundleLoaderUnavailableMessage());
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

/**
 * Tablet phone-frame toggle. When enabled, the next guest bundle
 * mount on iPad wraps the guest in an iPhone-shaped frame with a
 * vibe dock alongside (right pane in landscape, bottom strip in
 * portrait). Default false. The native side gates on iPad-only —
 * setting `true` from a phone is a no-op visually but still
 * persists, so toggling on a phone and rotating an iPad later
 * picks up the user's preference.
 *
 * iOS-only in v1; on Android the native methods are stubbed to
 * `{ enabled: false }` (no tablet phone-frame chrome yet).
 */
export async function setPhoneFrame(enabled: boolean): Promise<{ enabled: boolean }> {
  if (!YaverBundleLoader?.setPhoneFrame) return { enabled: false };
  return YaverBundleLoader.setPhoneFrame(enabled);
}

export async function getPhoneFrame(): Promise<{ enabled: boolean }> {
  if (!YaverBundleLoader?.getPhoneFrame) return { enabled: false };
  return YaverBundleLoader.getPhoneFrame();
}
