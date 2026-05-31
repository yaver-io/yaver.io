// Shim for `react-native` core used by mobile/src/lib/*.
//
// The mobile lib layer only touches a small slice of RN's public API
// — Platform, Alert, AppState, NativeModules. Anything rendering-
// related (View, Text, Pressable, StyleSheet) never reaches lib/ and
// doesn't need a shim. If a future lib file does need one, export
// it here rather than forking the mobile code.

type Listener<T = any> = (ev: T) => void;

function readPlatform(): "ios" | "android" | "web" {
  const env = (globalThis as any)?.process?.env?.YMH_PLATFORM;
  if (env === "ios" || env === "android" || env === "web") return env;
  return "ios";
}

export const Platform = {
  OS: readPlatform(),
  Version: 18 as number | string,
  select<T>(specifics: { ios?: T; android?: T; web?: T; default?: T; native?: T }): T | undefined {
    return (
      specifics[readPlatform()] ??
      specifics.native ??
      specifics.default
    );
  },
};

export const Alert = {
  alert(title: string, message?: string, buttons?: any[], _options?: any) {
    // Harness prints alerts as structured JSONL so test runners can
    // assert against them. Never blocks.
    const line = JSON.stringify({ __alert: { title, message, buttons: buttons?.map((b) => b?.text) } });
    // stdout so it ends up in CI logs.
    process.stdout.write(line + "\n");
  },
  prompt(..._args: any[]) {
    // prompts are rare and unused in lib/; keep a stub so imports
    // don't throw.
  },
};

class SimpleEmitter<T = any> {
  private listeners = new Set<Listener<T>>();
  addEventListener(_type: string, cb: Listener<T>) {
    this.listeners.add(cb);
    return { remove: () => this.listeners.delete(cb) };
  }
  removeEventListener(_type: string, cb: Listener<T>) { this.listeners.delete(cb); }
  emit(ev: T) { for (const cb of this.listeners) cb(ev); }
}

export const AppState = Object.assign(new SimpleEmitter<string>(), {
  currentState: "active" as "active" | "background" | "inactive",
});

export const NativeModules: Record<string, any> = new Proxy({}, {
  get() {
    // Return an object of callable stubs — anything the lib queries
    // gets a sensible empty answer.
    return new Proxy(() => undefined, {
      get: () => () => undefined,
    });
  },
});

export const NativeEventEmitter = class {
  addListener(_type: string, _cb: Listener) { return { remove() {} }; }
  removeAllListeners(_type?: string) {}
};

// DeviceEventEmitter — used by a few beacon callsites.
export const DeviceEventEmitter = new SimpleEmitter();

// NetInfo is commonly imported via @react-native-community/netinfo,
// but some code paths inspect `Platform.OS`-gated fallbacks. Leaving
// here as a trivial export in case a lib file reaches for it.
export const NetInfo = {
  addEventListener(_cb: Listener) { return () => {}; },
  async fetch() { return { isConnected: true, type: "wifi" as const }; },
};

// Linking — used by mobile/src/lib/builds.ts to open URLs. In headless
// there's no OS browser to hand off to; openURL is a no-op that resolves
// so call sites don't crash.
export const Linking = {
  async openURL(_url: string): Promise<void> {},
  async canOpenURL(_url: string): Promise<boolean> { return false; },
  async getInitialURL(): Promise<string | null> { return null; },
  addEventListener(_type: string, _cb: (...args: any[]) => void) { return { remove() {} }; },
};

export default {
  Platform,
  Alert,
  AppState,
  NativeModules,
  NativeEventEmitter,
  DeviceEventEmitter,
  NetInfo,
  Linking,
};
