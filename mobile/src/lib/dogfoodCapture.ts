/**
 * dogfoodCapture — JS side of the YaverDogfood native screenshot auto-catch.
 *
 * The special part of the dogfood loop: when dogfood mode is on, every time the
 * user takes a screenshot anywhere in Yaver, the native module re-renders the
 * key window to a JPEG and emits `onDogfoodScreenshot`. The Dogfood thread
 * subscribes here to stage that shot for annotation + prompt + dispatch.
 *
 * Native module: mobile/ios/Yaver/YaverDogfood.swift (+ Android equivalent TODO).
 * Falls back to a no-op when the native module isn't present (Expo Go / stale
 * build before `yaver wire push` / web) — `isDogfoodCaptureAvailable()` is false
 * and the thread can offer the manual "＋ add screenshot" path instead.
 */

import { NativeEventEmitter, NativeModules } from "react-native";

const mod = (NativeModules as any).YaverDogfood;

export interface DogfoodShot {
  /** Absolute filesystem path to the captured JPEG (no file:// prefix). */
  path: string;
  /** ms epoch when the screenshot was taken. */
  takenAt: number;
  /** Active expo-router path, if setRoute was called. */
  route?: string;
}

type Handler = (shot: DogfoodShot) => void;

let sub: { remove: () => void } | null = null;
const handlers = new Set<Handler>();
let started = false;

/** True only on a native build where the YaverDogfood module is linked. */
export function isDogfoodCaptureAvailable(): boolean {
  return !!mod && typeof mod.start === "function";
}

export function isDogfoodCaptureStarted(): boolean {
  return started;
}

/**
 * Begin auto-catching screenshots. Idempotent. Returns false when the native
 * module isn't present so callers can degrade to manual add. Call this only
 * while dogfood mode is enabled AND the host (not a guest bundle) is running.
 */
export async function startDogfoodCapture(): Promise<boolean> {
  if (!isDogfoodCaptureAvailable()) return false;
  if (started) return true;
  try {
    const emitter = new NativeEventEmitter(mod);
    sub = emitter.addListener("onDogfoodScreenshot", (ev: any) => {
      const path = String(ev?.path ?? "");
      if (!path) return;
      const shot: DogfoodShot = {
        path,
        takenAt: Number(ev?.takenAt ?? Date.now()),
        route: ev?.route ? String(ev.route) : undefined,
      };
      handlers.forEach((h) => {
        try {
          h(shot);
        } catch {
          // a bad handler shouldn't drop the shot for others
        }
      });
    });
    await mod.start();
    started = true;
    return true;
  } catch {
    return false;
  }
}

export async function stopDogfoodCapture(): Promise<void> {
  try {
    sub?.remove();
  } catch {
    // ignore
  }
  sub = null;
  if (mod && typeof mod.stop === "function") {
    try {
      await mod.stop();
    } catch {
      // ignore
    }
  }
  started = false;
}

/** Subscribe to caught screenshots. Returns an unsubscribe fn. */
export function onDogfoodScreenshot(handler: Handler): () => void {
  handlers.add(handler);
  return () => {
    handlers.delete(handler);
  };
}

/**
 * Push the current expo-router path so the dispatched prompt can say which
 * screen the user was on. Best-effort label; safe to call often.
 */
export function setDogfoodRoute(route: string): void {
  if (mod && typeof mod.setRoute === "function") {
    try {
      mod.setRoute(route ?? "");
    } catch {
      // ignore
    }
  }
}
