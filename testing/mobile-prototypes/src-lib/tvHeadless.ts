// tvHeadless.ts — Headless Apple TV/Android TV surface for testing
//
// Provides the same interface as mobile/native-tv/TVModule.kt
// but without requiring actual Apple TV or Android TV hardware. Used in tests
// and CI environments to verify TV D-pad, Gateway, and Remote Desktop flows.
//
// The headless surface mocks the TV-specific transport:
//   - D-pad key events (up, down, left, right, select, menu, home, play/pause, etc.)
//   - Voice dictation simulation
//   - Screen rendering feedback (simulated)
//   - App lifecycle events (launch, suspend)
//
// The real TV SDK is only needed for:
//   - Actual Apple TV display rendering
//   - Real SiriKit/Voice interaction
//   - Physical remote button events
//
// Everything else (the D-pad flow, gateway operations, remote desktop)
// is shared with this headless implementation.

import { EventEmitter } from "events";

// ── Types mirroring native TV module ─────────────────────────────────

export interface TVFocusable {
  id: string;
  accessibleLabel: string;
  focused: boolean;
  role?: "button" | "text" | "image" | "tab";
}

export interface TVScreen {
  id: string;
  title: string;
  focusedElement: string | null;
  focusableElements: TVFocusable[];
}

export interface TVInput {
  type: "dpad" | "voice" | "keyboard" | "none";
  lastActivity: number;
}

export interface TVAppState {
  bundleId: string;
  name: string;
  state: "foreground" | "background" | "suspended";
}

export type TVPlatform = "apple" | "android";

export type TVDpadKey =
  | "up" | "down" | "left" | "right" | "select"
  | "menu" | "home" | "back"
  | "play_pause" | "play" | "pause" | "stop"
  | "next" | "previous" | "skip_forward" | "skip_back"
  | "volume_up" | "volume_down" | "volume_mute"
  | "power";

// ── Headless TV Surface ────────────────────────────────────────────────

export interface TVHeadless {
  // D-pad input simulation
  pressKey: (key: TVDpadKey, repeat?: number) => void;
  getKeyHistory: () => { key: TVDpadKey; timestamp: number }[];
  clearKeyHistory: () => void;
  
  // Voice dictation simulation
  startDictation: () => void;
  endDictation: (text?: string) => void;
  cancelDictation: () => void;
  getCurrentTranscript: () => string;
  
  // Focus management
  getCurrentScreen: () => TVScreen;
  setFocusableElements: (elements: TVFocusable[]) => void;
  setFocusedElement: (id: string) => void;
  moveFocus: (direction: "up" | "down" | "left" | "right") => void;
  
  // App lifecycle simulation
  launchApp: (bundleId: string) => void;
  suspendApp: (bundleId: string) => void;
  getForegroundApp: () => TVAppState | null;
  getInstalledApps: () => TVAppState[];
  
  // Input method
  setInputType: (input: TVInput) => void;
  getInputType: () => TVInput;
  
  // Screen info
  getPlatform: () => TVPlatform;
  getResolution: () => { width: number; height: number };
  
  // Event simulation
  simulateSiriCommand: (command: string) => void;
  simulateRemoteButton: (button: "menu" | "home" | "back" | "power") => void;
  
  // Lifecycle
  destroy: () => void;
}

// ── Implementation ─────────────────────────────────────────────────────

class HeadlessTV implements TVHeadless {
  private platform: TVPlatform;
  private keyHistory: { key: TVDpadKey; timestamp: number }[] = [];
  private transcript = "";
  private isDictating = false;
  private focusableElements: TVFocusable[] = [];
  private focusedElementId: string | null = null;
  private installedApps: TVAppState[] = [];
  private foregroundApp: TVAppState | null = null;
  private currentInput: TVInput = { type: "none", lastActivity: 0 };
  private emitter = new EventEmitter();
  
  constructor(platform: TVPlatform = "apple") {
    this.platform = platform;
  }
  
  pressKey(key: TVDpadKey, repeat = 1): void {
    for (let i = 0; i < repeat; i++) {
      const entry = { key, timestamp: Date.now() };
      this.keyHistory.push(entry);
      this.emitter.emit("dpad", entry);
      
      // Auto-move focus with D-pad
      if (!repeat || i === repeat - 1) {
        this.handleDpadNavigation(key);
      }
    }
  }
  
  getKeyHistory(): { key: TVDpadKey; timestamp: number }[] {
    return [...this.keyHistory];
  }
  
  clearKeyHistory(): void {
    this.keyHistory = [];
  }
  
  startDictation(): void {
    this.isDictating = true;
    this.transcript = "";
    this.emitter.emit("dictation", { active: true });
  }
  
  endDictation(text?: string): void {
    this.isDictating = false;
    if (text !== undefined) {
      this.transcript = text;
    }
    this.emitter.emit("dictation", { active: false, transcript: this.transcript });
    this.transcript = "";
  }
  
  cancelDictation(): void {
    this.isDictating = false;
    this.transcript = "";
    this.emitter.emit("dictation", { active: false, cancelled: true });
  }
  
  getCurrentTranscript(): string {
    return this.transcript;
  }
  
  getCurrentScreen(): TVScreen {
    return {
      id: "main-screen",
      title: this.foregroundApp?.name || "Home",
      focusedElement: this.focusedElementId,
      focusableElements: [...this.focusableElements],
    };
  }
  
  setFocusableElements(elements: TVFocusable[]): void {
    this.focusableElements = elements;
    // Auto-focus the first element if nothing is focused
    if (!this.focusedElementId && elements.length > 0) {
      this.setFocusedElement(elements[0].id);
    }
    this.emitter.emit("focus", this.getCurrentScreen());
  }
  
  setFocusedElement(id: string): void {
    this.focusedElementId = id;
    this.emitter.emit("focus", this.getCurrentScreen());
  }
  
  moveFocus(direction: "up" | "down" | "left" | "right"): void {
    if (this.focusableElements.length === 0) return;
    
    const currentIndex = this.focusedElementId
      ? this.focusableElements.findIndex(e => e.id === this.focusedElementId)
      : -1;
    
    let newIndex = currentIndex;
    switch (direction) {
      case "up":
        newIndex = currentIndex > 0 ? currentIndex - 1 : this.focusableElements.length - 1;
        break;
      case "down":
        newIndex = currentIndex < this.focusableElements.length - 1 ? currentIndex + 1 : 0;
        break;
      case "left":
        newIndex = currentIndex > 0 ? currentIndex - 1 : this.focusableElements.length - 1;
        break;
      case "right":
        newIndex = currentIndex < this.focusableElements.length - 1 ? currentIndex + 1 : 0;
        break;
    }
    
    this.setFocusedElement(this.focusableElements[newIndex].id);
  }
  
  launchApp(bundleId: string): void {
    const app = this.installedApps.find(a => a.bundleId === bundleId);
    if (!app) {
      this.installedApps.push({
        bundleId,
        name: `App ${bundleId}`,
        state: "foreground",
      });
    } else {
      app.state = "foreground";
    }
    this.foregroundApp = this.installedApps.find(a => a.bundleId === bundleId) || null;
    this.emitter.emit("appLaunch", { bundleId });
    this.emitter.emit("appFocus", this.getForegroundApp());
  }
  
  suspendApp(bundleId: string): void {
    const app = this.installedApps.find(a => a.bundleId === bundleId);
    if (app) {
      app.state = "suspended";
      if (this.foregroundApp?.bundleId === bundleId) {
        this.foregroundApp = null;
      }
    }
    this.emitter.emit("appSuspend", { bundleId });
    this.emitter.emit("appFocus", this.getForegroundApp());
  }
  
  getForegroundApp(): TVAppState | null {
    return this.foregroundApp;
  }
  
  getInstalledApps(): TVAppState[] {
    return [...this.installedApps];
  }
  
  setInputType(input: TVInput): void {
    this.currentInput = { ...input, lastActivity: Date.now() };
    this.emitter.emit("inputChange", this.currentInput);
  }
  
  getInputType(): TVInput {
    return this.currentInput;
  }
  
  getPlatform(): TVPlatform {
    return this.platform;
  }
  
  getResolution(): { width: number; height: number } {
    // Standard TV resolutions
    return this.platform === "apple" ? { width: 3840, height: 2160 }
                                         : { width: 3840, height: 2160 };
  }
  
  simulateSiriCommand(command: string): void {
    this.emitter.emit("siri", { command });
  }
  
  simulateRemoteButton(button: "menu" | "home" | "back" | "power"): void {
    this.emitter.emit("remoteButton", { button });
  }
  
  // Event registration (mirrors DeviceEventEmitter)
  addListener(event: string, callback: (...args: unknown[]) => void): () => void {
    this.emitter.on(event, callback);
    return () => this.emitter.removeListener(event, callback);
  }
  
  destroy(): void {
    this.emitter.removeAllListeners();
    this.keyHistory = [];
    this.transcript = "";
    this.focusableElements = [];
    this.installedApps = [];
  }
  
  private handleDpadNavigation(key: TVDpadKey): void {
    switch (key) {
      case "up":
        this.moveFocus("up");
        break;
      case "down":
        this.moveFocus("down");
        break;
      case "left":
        this.moveFocus("left");
        break;
      case "right":
        this.moveFocus("right");
        break;
      case "select":
        this.emitter.emit("select", { elementId: this.focusedElementId });
        break;
    }
  }
}

// ── Factory ─────────────────────────────────────────────────────────

let currentHeadless: HeadlessTV | null = null;

export function createTVHeadless(platform: TVPlatform = "apple"): TVHeadless {
  if (currentHeadless) {
    currentHeadless.destroy();
  }
  currentHeadless = new HeadlessTV(platform);
  return currentHeadless;
}

export function getTVHeadless(): TVHeadless | null {
  return currentHeadless;
}

export function destroyTVHeadless(): void {
  if (currentHeadless) {
    currentHeadless.destroy();
    currentHeadless = null;
  }
}

// ── Export for React Native module compatibility ───────────────────────

export const YaverTVHeadless = {
  pressKey: (key: string, repeat = 1) => {
    const headless = getTVHeadless();
    if (!headless) throw new Error("Headless TV not created. Call createTVHeadless() first.");
    headless.pressKey(key as TVDpadKey, repeat);
  },
  
  getKeyHistory: () => {
    const headless = getTVHeadless();
    if (!headless) return [];
    return headless.getKeyHistory();
  },
  
  clearKeyHistory: () => {
    const headless = getTVHeadless();
    if (!headless) return;
    headless.clearKeyHistory();
  },
  
  startDictation: () => {
    const headless = getTVHeadless();
    if (!headless) return;
    headless.startDictation();
  },
  
  endDictation: (text?: string) => {
    const headless = getTVHeadless();
    if (!headless) return;
    headless.endDictation(text);
  },
  
  cancelDictation: () => {
    const headless = getTVHeadless();
    if (!headless) return;
    headless.cancelDictation();
  },
  
  getCurrentTranscript: () => {
    const headless = getTVHeadless();
    if (!headless) return "";
    return headless.getCurrentTranscript();
  },
  
  getCurrentScreen: () => {
    const headless = getTVHeadless();
    if (!headless) {
      return {
        id: "main-screen",
        title: "Home",
        focusedElement: null,
        focusableElements: [],
      };
    }
    return headless.getCurrentScreen();
  },
  
  setFocusableElements: (elements: TVFocusable[]) => {
    const headless = getTVHeadless();
    if (!headless) return;
    headless.setFocusableElements(elements);
  },
  
  setFocusedElement: (id: string) => {
    const headless = getTVHeadless();
    if (!headless) return;
    headless.setFocusedElement(id);
  },
  
  launchApp: (bundleId: string) => {
    const headless = getTVHeadless();
    if (!headless) return;
    headless.launchApp(bundleId);
  },
  
  suspendApp: (bundleId: string) => {
    const headless = getTVHeadless();
    if (!headless) return;
    headless.suspendApp(bundleId);
  },
  
  getForegroundApp: () => {
    const headless = getTVHeadless();
    if (!headless) return null;
    return headless.getForegroundApp();
  },
  
  getInstalledApps: () => {
    const headless = getTVHeadless();
    if (!headless) return [];
    return headless.getInstalledApps();
  },
  
  setInputType: (input: TVInput) => {
    const headless = getTVHeadless();
    if (!headless) return;
    headless.setInputType(input);
  },
  
  getInputType: () => {
    const headless = getTVHeadless();
    if (!headless) return null;
    return headless ? headless.getInputType() : { type: "none", lastActivity: 0 };
  },
  
  getPlatform: () => {
    const headless = getTVHeadless();
    if (!headless) return "apple" as TVPlatform;
    return headless.getPlatform();
  },
  
  getResolution: () => {
    const headless = getTVHeadless();
    if (!headless) {
      return { width: 1920, height: 1080 };
    }
    return headless.getResolution();
  },
  
  simulateSiriCommand: (command: string) => {
    const headless = getTVHeadless();
    if (!headless) return;
    headless.simulateSiriCommand(command);
  },
  
  simulateRemoteButton: (button: "menu" | "home" | "back" | "power") => {
    const headless = getTVHeadless();
    if (!headless) return;
    headless.simulateRemoteButton(button);
  },
  
  addListener: (event: string, callback: (...args: unknown[]) => void) => {
    const headless = getTVHeadless();
    if (!headless) {
      // Return no-op for safety when headless isn't created
      return () => {};
    }
    return (headless as any).addListener(event, callback);
  },
  
  destroy: () => {
    destroyTVHeadless();
  },
};

export default YaverTVHeadless;