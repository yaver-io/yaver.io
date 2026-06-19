// carHeadless.ts — Headless CarPlay/Cast Audio surface for testing
//
// Provides the same interface as mobile/native-car/CarPlayModule.kt
// but without requiring actual CarPlay hardware. Used in tests and
// CI environments to verify car voice coding and car messaging flows.
//
// The headless surface mocks the CarPlay-specific transport:
//   - MessagingStyle notifications (T1+) are captured in memory
//   - HMI buttons are exposed via programmatic interface
//   - Audio routes are simulated with no-op callbacks
//
// The real car SDK is only needed for:
//   - Actual CarPlay display rendering
//   - Real audio route switching
//   - Physical steering wheel button events
//
// Everything else (the dispatch loop, summarization, viewport shaping)
// is shared with this headless implementation.

import { EventEmitter } from "events";

// ── Types mirroring native car module ─────────────────────────────────

export interface CarMessage {
  id: string;
  title: string;
  body: string;
  timestamp: number;
  imageUrl?: string;
  action?: string;
}

export interface CarButton {
  id: string;
  title: string;
  type: "primary" | "secondary" | "action";
}

export interface CarAudioRoute {
  name: string;
  active: boolean;
}

// ── Headless Car Surface ───────────────────────────────────────────────

export interface CarHeadless {
  // MessagingStyle (T1+) - CarPlay audio message queue
  sendMessage: (message: string) => void;
  getMessages: () => CarMessage[];
  clearMessages: () => void;
  
  // HMI buttons - CarPlay navigation bar buttons
  setButtons: (buttons: CarButton[]) => void;
  getButtons: () => CarButton[];
  pressButton: (id: string) => void;
  
  // Audio routes - Simulate Bluetooth/Audio output switching
  setAudioRoutes: (routes: CarAudioRoute[]) => void;
  getAudioRoutes: () => CarAudioRoute[];
  setActiveRoute: (name: string) => void;
  
  // Event simulation - For testing event handlers
  simulateSteeringButton: (button: "left" | "right" | "select" | "back") => void;
  simulateVoiceCommand: (transcript: string) => void;
  
  // Lifecycle
  destroy: () => void;
}

// ── Implementation ─────────────────────────────────────────────────────

class HeadlessCar implements CarHeadless {
  private messages: CarMessage[] = [];
  private buttons: CarButton[] = [];
  private audioRoutes: CarAudioRoute[] = [];
  private activeRoute = "";
  private emitter = new EventEmitter();
  
  sendMessage(message: string): void {
    const carMessage: CarMessage = {
      id: `msg-${Date.now()}-${Math.random().toString(36).slice(2, 11)}`,
      title: "Yaver Assistant",
      body: message,
      timestamp: Date.now(),
    };
    this.messages.push(carMessage);
    this.emitter.emit("message", carMessage);
  }
  
  getMessages(): CarMessage[] {
    return [...this.messages];
  }
  
  clearMessages(): void {
    this.messages = [];
  }
  
  setButtons(buttons: CarButton[]): void {
    this.buttons = buttons;
    this.emitter.emit("buttons", buttons);
  }
  
  getButtons(): CarButton[] {
    return [...this.buttons];
  }
  
  pressButton(id: string): void {
    this.emitter.emit("buttonPress", id);
  }
  
  setAudioRoutes(routes: CarAudioRoute[]): void {
    this.audioRoutes = routes.map((r, i) => ({ ...r, active: i === 0 }));
    this.activeRoute = this.audioRoutes[0]?.name || "";
    this.emitter.emit("audioRoutes", this.audioRoutes);
  }
  
  getAudioRoutes(): CarAudioRoute[] {
    return [...this.audioRoutes];
  }
  
  setActiveRoute(name: string): void {
    this.activeRoute = name;
    this.audioRoutes = this.audioRoutes.map(r => ({ ...r, active: r.name === name }));
    this.emitter.emit("audioRoutes", this.audioRoutes);
  }
  
  simulateSteeringButton(button: "left" | "right" | "select" | "back"): void {
    this.emitter.emit("steeringButton", button);
  }
  
  simulateVoiceCommand(transcript: string): void {
    this.emitter.emit("voiceCommand", transcript);
  }
  
  // Event registration (mirrors DeviceEventEmitter)
  addListener(event: string, callback: (...args: unknown[]) => void): () => void {
    this.emitter.on(event, callback);
    return () => this.emitter.removeListener(event, callback);
  }
  
  destroy(): void {
    this.emitter.removeAllListeners();
    this.messages = [];
    this.buttons = [];
    this.audioRoutes = [];
  }
}

// ── Factory ─────────────────────────────────────────────────────────

let currentHeadless: HeadlessCar | null = null;

export function createCarHeadless(): CarHeadless {
  if (currentHeadless) {
    currentHeadless.destroy();
  }
  currentHeadless = new HeadlessCar();
  return currentHeadless;
}

export function getCarHeadless(): CarHeadless | null {
  return currentHeadless;
}

export function destroyCarHeadless(): void {
  if (currentHeadless) {
    currentHeadless.destroy();
    currentHeadless = null;
  }
}

// ── Export for React Native module compatibility ───────────────────────

// When used as a mock for the native module, expose the same interface
export const YaverCarHeadless = {
  sendMessage: (message: string) => {
    const headless = getCarHeadless();
    if (!headless) throw new Error("Headless car not created. Call createCarHeadless() first.");
    headless.sendMessage(message);
  },
  
  getMessages: () => {
    const headless = getCarHeadless();
    if (!headless) return [];
    return headless.getMessages();
  },
  
  clearMessages: () => {
    const headless = getCarHeadless();
    if (headless) return;
    headless.clearMessages();
  },
  
  setButtons: (buttons: CarButton[]) => {
    const headless = getCarHeadless();
    if (!headless) return;
    headless.setButtons(buttons);
  },
  
  getButtons: () => {
    const headless = getCarHeadless();
    if (!headless) return [];
    return headless.getButtons();
  },
  
  pressButton: (id: string) => {
    const headless = getCarHeadless();
    if (!headless) return;
    headless.pressButton(id);
  },
  
  setAudioRoutes: (routes: CarAudioRoute[]) => {
    const headless = getCarHeadless();
    if (!headless) return;
    headless.setAudioRoutes(routes);
  },
  
  getAudioRoutes: () => {
    const headless = getCarHeadless();
    if (!headless) return [];
    return headless.getAudioRoutes();
  },
  
  setActiveRoute: (name: string) => {
    const headless = getCarHeadless();
    if (!headless) return;
    headless.setActiveRoute(name);
  },
  
  addListener: (event: string, callback: (...args: unknown[]) => void) => {
    const headless = getCarHeadless();
    if (!headless) {
      // Return no-op for safety when headless isn't created
      return () => {};
    }
    return (headless as any).addListener(event, callback);
  },
  
  destroy: () => {
    destroyCarHeadless();
  },
};

export default YaverCarHeadless;