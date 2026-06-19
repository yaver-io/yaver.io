// watchHeadless.ts — Headless watchOS/Wear OS surface for testing
//
// Provides the same interface as mobile/native-watch/WatchBridgeModule.kt
// but without requiring actual watch hardware. Used in tests and CI environments
// to verify watch bridge, voice terminal, and complication flows.
//
// The headless surface mocks the watch-specific transport:
//   - WCSession (watchOS) or Wear Data Layer (Wear OS) message delivery
//   - Complication taps and intents
//   - Voice dictation input
//   - Haptic feedback simulation
//   - Watch battery and state
//
// The real watch SDK is only needed for:
//   - Actual watch display rendering
//   - Real Siri integration
//   - Physical watch button events
//   - Real complication lifecycle
//
// Everything else (the watch bridge, protocol handling, voice terminal)
// is shared with this headless implementation.

import { EventEmitter } from "events";

// ── Types mirroring native watch module ─────────────────────────

export interface WatchMessage {
  v: 1;
  kind: "ack" | "confirm-needed" | "working" | "summary" | "error" | "handoff";
  spoken?: string;
  token?: string;
  prompt?: string;
  taskId?: string;
  status?: string;
  target?: string;
}

export interface WatchState {
  connected: boolean;
  deviceType: "watchos" | "wearos";
  batteryLevel: number;
  isCharging: boolean;
  appInstalled: boolean;
}

export interface ComplicationIntent {
  type: "run-tests" | "deploy" | "status";
  timestamp: number;
}

export type WatchPlatform = "watchos" | "wearos";

// ── Headless Watch Surface ────────────────────────────────────────────

export interface WatchHeadless {
  // Message delivery (mirrors WCSession / Data Layer)
  sendMessage: (json: string) => void;
  getIncomingMessages: () => string[];
  getOutgoingMessages: () => WatchMessage[];
  clearOutgoingMessages: () => void;
  
  // Complication intents
  tapComplication: (intent: string) => void;
  getComplicationIntents: () => ComplicationIntent[];
  
  // Voice dictation
  startDictation: () => void;
  endDictation: (text?: string) => void;
  getCurrentTranscript: () => string;
  
  // State
  getState: () => WatchState;
  setBattery: (level: number, charging: boolean) => void;
  setConnected: (connected: boolean) => void;
  
  // Haptics simulation
  triggerHaptic: (pattern: "success" | "warning" | "error" | "notification") => void;
  
  // Event simulation
  simulateButtonPress: (button: "crown" | "side") => void;
  
  // Lifecycle
  destroy: () => void;
  
  // Event registration
  addListener: (event: string, callback: (...args: unknown[]) => void) => () => void;
}

// ── Implementation ─────────────────────────────────────────────────

class HeadlessWatch implements WatchHeadless {
  private platform: WatchPlatform;
  private incomingMessages: string[] = [];
  private outgoingMessages: WatchMessage[] = [];
  private complicationIntents: ComplicationIntent[] = [];
  private transcript = "";
  private state: WatchState = {
    connected: true,
    deviceType: "watchos",
    batteryLevel: 100,
    isCharging: false,
    appInstalled: true,
  };
  private emitter = new EventEmitter();
  
  constructor(platform: WatchPlatform = "watchos") {
    this.platform = platform;
    this.state.deviceType = platform;
  }
  
  sendMessage(json: string): void {
    // Simulate sending to watch - in reality this goes over WCSession/Data Layer
    // In headless mode, we just track it for testing
    this.emitter.emit("messageSent", json);
  }
  
  getIncomingMessages(): string[] {
    return [...this.incomingMessages];
  }
  
  getOutgoingMessages(): WatchMessage[] {
    return [...this.outgoingMessages];
  }
  
  clearOutgoingMessages(): void {
    this.outgoingMessages = [];
  }
  
  receiveMessage(json: string): void {
    // Simulate receiving from watch
    this.incomingMessages.push(json);
    this.emitter.emit("messageReceived", json);
  }
  
  tapComplication(intent: string): void {
    this.complicationIntents.push({
      type: intent as "run-tests" | "deploy" | "status",
      timestamp: Date.now(),
    });
    this.emitter.emit("complicationTap", { intent });
  }
  
  getComplicationIntents(): ComplicationIntent[] {
    return [...this.complicationIntents];
  }
  
  startDictation(): void {
    this.transcript = "";
    this.emitter.emit("dictation", { active: true });
  }
  
  endDictation(text?: string): void {
    if (text !== undefined) {
      this.transcript = text;
    }
    this.emitter.emit("dictation", { active: false, transcript: this.transcript });
  }
  
  getCurrentTranscript(): string {
    return this.transcript;
  }
  
  getState(): WatchState {
    return { ...this.state };
  }
  
  setBattery(level: number, charging: boolean): void {
    this.state.batteryLevel = Math.max(0, Math.min(100, level));
    this.state.isCharging = charging;
    this.emitter.emit("stateChange", this.getState());
  }
  
  setConnected(connected: boolean): void {
    this.state.connected = connected;
    this.emitter.emit("stateChange", this.getState());
  }
  
  triggerHaptic(pattern: "success" | "warning" | "error" | "notification"): void {
    this.emitter.emit("haptic", { pattern });
  }
  
  simulateButtonPress(button: "crown" | "side"): void {
    this.emitter.emit("buttonPress", { button });
  }
  
  addListener(event: string, callback: (...args: unknown[]) => void): () => void {
    this.emitter.on(event, callback);
    return () => this.emitter.removeListener(event, callback);
  }
  
  destroy(): void {
    this.emitter.removeAllListeners();
    this.incomingMessages = [];
    this.outgoingMessages = [];
    this.complicationIntents = [];
  }
}

// ── Factory ─────────────────────────────────────────────────────────

let currentHeadless: HeadlessWatch | null = null;

export function createWatchHeadless(platform: WatchPlatform = "watchos"): WatchHeadless {
  if (currentHeadless) {
    currentHeadless.destroy();
  }
  currentHeadless = new HeadlessWatch(platform);
  return currentHeadless;
}

export function getWatchHeadless(): WatchHeadless | null {
  return currentHeadless;
}

export function destroyWatchHeadless(): void {
  if (currentHeadless) {
    currentHeadless.destroy();
    currentHeadless = null;
  }
}

// ── Export for React Native module compatibility ───────────────────────

export const YaverWatchHeadless = {
  sendMessage: (json: string) => {
    const headless = getWatchHeadless();
    if (!headless) throw new Error("Headless watch not created. Call createWatchHeadless() first.");
    headless.sendMessage(json);
  },
  
  getIncomingMessages: () => {
    const headless = getWatchHeadless();
    if (!headless) return [];
    return headless.getIncomingMessages();
  },
  
  getOutgoingMessages: () => {
    const headless = getWatchHeadless();
    if (!headless) return [];
    return headless.getOutgoingMessages();
  },
  
  clearOutgoingMessages: () => {
    const headless = getWatchHeadless();
    if (!headless) return;
    headless.clearOutgoingMessages();
  },
  
  receiveMessage: (json: string) => {
    const headless = getWatchHeadless();
    if (!headless) throw new Error("Headless watch not created. Call createWatchHeadless() first.");
    headless.receiveMessage(json);
  },
  
  tapComplication: (intent: string) => {
    const headless = getWatchHeadless();
    if (!headless) return;
    headless.tapComplication(intent);
  },
  
  getComplicationIntents: () => {
    const headless = getWatchHeadless();
    if (!headless) return [];
    return headless.getComplicationIntents();
  },
  
  startDictation: () => {
    const headless = getWatchHeadless();
    if (!headless) return;
    headless.startDictation();
  },
  
  endDictation: (text?: string) => {
    const headless = getWatchHeadless();
    if (!headless) return;
    headless.endDictation(text);
  },
  
  getCurrentTranscript: () => {
    const headless = getWatchHeadless();
    if (!headless) return "";
    return headless.getCurrentTranscript();
  },
  
  getState: () => {
    const headless = getWatchHeadless();
    if (!headless) {
      return {
        connected: false,
        deviceType: "watchos",
        batteryLevel: 0,
        isCharging: false,
        appInstalled: false,
      };
    }
    return headless.getState();
  },
  
  setBattery: (level: number, charging: boolean) => {
    const headless = getWatchHeadless();
    if (!headless) return;
    headless.setBattery(level, charging);
  },
  
  setConnected: (connected: boolean) => {
    const headless = getWatchHeadless();
    if (!headless) return;
    headless.setConnected(connected);
  },
  
  triggerHaptic: (pattern: "success" | "warning" | "error" | "notification") => {
    const headless = getWatchHeadless();
    if (!headless) return;
    headless.triggerHaptic(pattern);
  },
  
  simulateButtonPress: (button: "crown" | "side") => {
    const headless = getWatchHeadless();
    if (!headless) return;
    headless.simulateButtonPress(button);
  },
  
  addListener: (event: string, callback: (...args: unknown[]) => void) => {
    const headless = getWatchHeadless();
    if (!headless) {
      // Return no-op for safety when headless isn't created
      return () => {};
    }
    return (headless as any).addListener(event, callback);
  },
  
  destroy: () => {
    destroyWatchHeadless();
  },
};

export default YaverWatchHeadless;