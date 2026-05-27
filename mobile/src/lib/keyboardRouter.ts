/**
 * keyboardRouter — phone-side multiplexer for a paired BT keyboard.
 *
 * Routes keystrokes from one physical keyboard across many remote
 * sinks: a terminal pty on the agent, a remote browser window in
 * the spatial scene, the voice dictation pipe, or "phone" (default
 * OS handling — typing into a phone text field).
 *
 * Two ingestion paths:
 *
 *   1. NATIVE — `NativeModules.YaverKeyboardRouter.grab()` hooks
 *      iOS GCKeyboard / Android InputManager + activity dispatch.
 *      Hardware keystrokes arrive as `YaverKey` JS events. This is
 *      the path you get after `expo prebuild` rebuilds the native
 *      side (or via `yaver wire push`).
 *
 *   2. FALLBACK — when the native module isn't installed (Expo Go,
 *      stale build), the TS router still handles keystrokes the OS
 *      forwards to RN via standard TextInput onKeyPress. The host
 *      screen wires those calls explicitly.
 *
 * Sinks are addressed as opaque strings ("terminal:<paneId>",
 * "browser:<sessionId>", "voice:dictate", "phone:native"). The
 * router does not interpret the suffix — it just dispatches.
 */

import { AppState, NativeEventEmitter, NativeModules } from "react-native";

export type KeyboardSink =
  | { kind: "terminal"; paneId: string }
  | { kind: "browser"; sessionId: string }
  | { kind: "voice" }
  | { kind: "phone" };

export interface KeyDispatchOptions {
  agentUrl: string;
  token: string;
}

interface KeyEvent {
  key: string;
  modifiers?: {
    shift?: boolean;
    ctrl?: boolean;
    alt?: boolean;
    meta?: boolean;
  };
}

// Single named keys we pass through unchanged. Plain printable
// characters (length === 1) become a "text" dispatch; anything in
// this list becomes a "key" dispatch.
const NAMED_KEYS = new Set([
  "Enter",
  "Tab",
  "Backspace",
  "Escape",
  "ArrowLeft",
  "ArrowRight",
  "ArrowUp",
  "ArrowDown",
  "Home",
  "End",
  "PageUp",
  "PageDown",
  "Delete",
]);

type Listener = (sink: KeyboardSink) => void;

class KeyboardRouter {
  private currentSink: KeyboardSink = { kind: "phone" };
  private listeners: Set<Listener> = new Set();
  private opts: KeyDispatchOptions | null = null;
  private nativeSub: { remove: () => void } | null = null;
  private nativeGrabbed = false;

  configure(opts: KeyDispatchOptions): void {
    this.opts = opts;
  }

  /**
   * Acquire the native HID grab (iOS GCKeyboard / Android
   * dispatchKeyEvent intercept). Idempotent — calling twice keeps
   * a single subscription. Returns `false` when the native module
   * isn't present (Expo Go / stale build), the caller can still
   * push events into `handleKey` manually from RN keyboard events.
   */
  async grabNative(): Promise<boolean> {
    const mod = (NativeModules as any).YaverKeyboardRouter;
    if (!mod || typeof mod.grab !== "function") return false;
    if (this.nativeGrabbed) return true;
    try {
      await mod.grab({ exclusive: false });
      const emitter = new NativeEventEmitter(mod);
      this.nativeSub = emitter.addListener("YaverKey", (ev: any) => {
        void this.handleKey({
          key: String(ev?.key ?? ""),
          modifiers: ev?.modifiers,
        });
      });
      this.nativeGrabbed = true;
      return true;
    } catch {
      return false;
    }
  }

  async releaseNative(): Promise<void> {
    const mod = (NativeModules as any).YaverKeyboardRouter;
    try {
      this.nativeSub?.remove();
    } catch {}
    this.nativeSub = null;
    if (mod && typeof mod.release === "function") {
      try {
        await mod.release();
      } catch {}
    }
    this.nativeGrabbed = false;
  }

  isNativeGrabbed(): boolean {
    return this.nativeGrabbed;
  }

  setSink(sink: KeyboardSink): void {
    this.currentSink = sink;
    for (const l of this.listeners) {
      try {
        l(sink);
      } catch {}
    }
  }

  getSink(): KeyboardSink {
    return this.currentSink;
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  /**
   * Handle a single keystroke. Returns true when the router
   * consumed the event (caller should preventDefault if possible).
   * False when the sink is "phone" — the OS keeps the event.
   */
  async handleKey(ev: KeyEvent): Promise<boolean> {
    if (AppState.currentState !== "active") return false;
    const sink = this.currentSink;
    if (sink.kind === "phone") return false;
    if (!this.opts) return false;
    const named = NAMED_KEYS.has(ev.key);
    try {
      switch (sink.kind) {
        case "browser": {
          await fetch(
            `${this.opts.agentUrl}/remote-runtime/sessions/${encodeURIComponent(
              sink.sessionId,
            )}/control`,
            {
              method: "POST",
              headers: {
                "Content-Type": "application/json",
                Authorization: `Bearer ${this.opts.token}`,
              },
              body: JSON.stringify(
                named
                  ? { action: "key", key: ev.key }
                  : ev.key.length === 1
                  ? { action: "text", text: ev.key }
                  : { action: "key", key: ev.key },
              ),
            },
          );
          return true;
        }
        case "terminal": {
          await fetch(
            `${this.opts.agentUrl}/tasks/${encodeURIComponent(sink.paneId)}/stdin`,
            {
              method: "POST",
              headers: {
                "Content-Type": "application/json",
                Authorization: `Bearer ${this.opts.token}`,
              },
              body: JSON.stringify({
                bytes: named ? namedKeyToBytes(ev.key) : ev.key,
              }),
            },
          );
          return true;
        }
        case "voice": {
          // Voice sink owns its own audio stream; the keyboard's
          // role here is to send control characters that bracket
          // start/stop. Anything else is a no-op.
          if (ev.key === "Enter" || ev.key === "Escape") {
            await fetch(`${this.opts.agentUrl}/voice/control`, {
              method: "POST",
              headers: {
                "Content-Type": "application/json",
                Authorization: `Bearer ${this.opts.token}`,
              },
              body: JSON.stringify({
                action: ev.key === "Enter" ? "commit" : "cancel",
              }),
            });
            return true;
          }
          return false;
        }
      }
    } catch {
      // Best-effort: a one-off failure shouldn't lock the keyboard.
      return false;
    }
    return false;
  }
}

function namedKeyToBytes(key: string): string {
  switch (key) {
    case "Enter":
      return "\r";
    case "Tab":
      return "\t";
    case "Backspace":
      return "\x7f";
    case "Escape":
      return "\x1b";
    case "ArrowLeft":
      return "\x1b[D";
    case "ArrowRight":
      return "\x1b[C";
    case "ArrowUp":
      return "\x1b[A";
    case "ArrowDown":
      return "\x1b[B";
    case "Delete":
      return "\x1b[3~";
    default:
      return "";
  }
}

export const keyboardRouter = new KeyboardRouter();
