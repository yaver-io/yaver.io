import { DeviceEventEmitter, NativeModules, Platform } from 'react-native';

// Detects that the surrounding runtime is Yaver's super-host bridge. The
// YaverInfo native module is only registered inside Yaver's container
// (see mobile/ios/Yaver/YaverInfo.{swift,m} and the Android equivalent),
// so it is undefined in a standalone third-party app. When the SDK runs
// inside Yaver we yield shake handling to Yaver's native
// ShakeDetectingWindow — the user should only ever see the 2-button
// "Reload / Back to Yaver" overlay, never a FeedbackModal popped from
// inside the guest bundle.
function isRunningInsideYaverHost(): boolean {
  try {
    return !!(NativeModules as any)?.YaverInfo;
  } catch {
    return false;
  }
}

const SHAKE_TIMEOUT_MS = 1000; // minimum time between shakes
const ACCEL_THRESHOLD_G = 1.8; // peak g-force that qualifies as a shake event
const ACCEL_MIN_HITS = 3; // peaks within the window before we fire
const ACCEL_WINDOW_MS = 800; // rolling window
const ACCEL_SAMPLE_INTERVAL_MS = 80; // ≈12Hz — cheap but catches shakes

/**
 * Detects device shake gestures.
 *
 * Two detection paths run in parallel so shake works in both Debug and
 * Release / TestFlight builds:
 *
 *   1. React Native's built-in `shakeEvent` (iOS) / `ShakeEvent` (Android)
 *      on `DeviceEventEmitter`. RN emits `shakeEvent` only in Debug mode
 *      on iOS, which is why TestFlight builds never saw a shake prior to
 *      SDK 0.5.1.
 *   2. Accelerometer-based fallback via `expo-sensors` (optional peer
 *      dep). When the host app has `expo-sensors` installed we subscribe
 *      to the accelerometer and fire on a burst of above-threshold peaks.
 *      This path works identically in Debug, Release, and TestFlight.
 *
 * Both paths share a 1-second debounce so a single shake never fires the
 * callback twice.
 */
export class ShakeDetector {
  private devMenuSub: { remove(): void } | null = null;
  private accelSub: { remove(): void } | null = null;
  private lastShakeTime = 0;
  private peakTimestamps: number[] = [];

  start(onShake: () => void): void {
    this.stop();
    // When the app is loaded inside Yaver's super-host (Hermes push),
    // Yaver owns the shake gesture and shows its own "Reload / Back to
    // Yaver" overlay. We must not also fire the guest-side callback,
    // otherwise the user gets both UIs at once.
    if (isRunningInsideYaverHost()) return;
    this.subscribeDevMenu(onShake);
    this.subscribeAccelerometer(onShake);
  }

  stop(): void {
    if (this.devMenuSub) {
      this.devMenuSub.remove();
      this.devMenuSub = null;
    }
    if (this.accelSub) {
      this.accelSub.remove();
      this.accelSub = null;
    }
    this.peakTimestamps = [];
  }

  private fire(onShake: () => void): void {
    const now = Date.now();
    if (now - this.lastShakeTime <= SHAKE_TIMEOUT_MS) return;
    this.lastShakeTime = now;
    this.peakTimestamps = [];
    onShake();
  }

  /**
   * Dev-menu / platform-native event listener. On iOS this is the
   * `shakeEvent` RN posts from RCTDevMenu (Debug only). On Android it is
   * the `ShakeEvent` name a handful of third-party shake libraries emit.
   */
  private subscribeDevMenu(onShake: () => void): void {
    if (typeof DeviceEventEmitter?.addListener !== 'function') return;
    const eventName = Platform.OS === 'ios' ? 'shakeEvent' : 'ShakeEvent';
    this.devMenuSub = DeviceEventEmitter.addListener(eventName, () => {
      this.fire(onShake);
    });
  }

  /**
   * Accelerometer-based detection. Uses `expo-sensors` when available —
   * the host app's own dependency, not the SDK's, so apps that don't
   * want the extra native surface get the Dev-menu path only.
   */
  private subscribeAccelerometer(onShake: () => void): void {
    let Accelerometer: {
      setUpdateInterval: (ms: number) => void;
      addListener: (cb: (d: { x: number; y: number; z: number }) => void) => {
        remove(): void;
      };
    } | null = null;
    try {
      // Optional peer dep — if it isn't installed, we simply skip this path.
      Accelerometer = require('expo-sensors').Accelerometer;
    } catch {
      return;
    }
    if (!Accelerometer) return;

    try {
      Accelerometer.setUpdateInterval(ACCEL_SAMPLE_INTERVAL_MS);
    } catch {
      // Some platforms reject zero-value intervals; fall through with defaults
    }

    this.accelSub = Accelerometer.addListener(({ x, y, z }) => {
      // Magnitude of acceleration vector (in g). Subtract 1 so a stationary
      // device reports ~0 rather than the 1g of gravity.
      const mag = Math.sqrt(x * x + y * y + z * z);
      if (mag - 1 < ACCEL_THRESHOLD_G - 1) return;

      const now = Date.now();
      this.peakTimestamps.push(now);
      // Drop peaks that fell out of the rolling window.
      while (
        this.peakTimestamps.length > 0 &&
        now - this.peakTimestamps[0] > ACCEL_WINDOW_MS
      ) {
        this.peakTimestamps.shift();
      }
      if (this.peakTimestamps.length >= ACCEL_MIN_HITS) {
        this.fire(onShake);
      }
    });
  }
}
