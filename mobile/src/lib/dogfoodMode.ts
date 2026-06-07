/**
 * dogfoodMode — the sticky "improve Yaver with Yaver" toggle + its wiring.
 *
 * While ON:
 *  - the native YaverDogfood module auto-catches screenshots (dogfoodCapture),
 *  - a lightweight breadcrumb buffer records Yaver-context (dogfoodBreadcrumbs),
 *  - both pause when the app backgrounds (foreground-only),
 *  - both are host-only (a guest bundle runs its own JS context, so this host
 *    code simply doesn't execute inside guests; on host we also stop capture
 *    while a guest is loaded via setGuestActive).
 *
 * State is persisted per-user so it survives app restarts.
 */

import AsyncStorage from "@react-native-async-storage/async-storage";
import { AppState, type AppStateStatus } from "react-native";
import {
  startDogfoodCapture,
  stopDogfoodCapture,
  setDogfoodRoute,
  isDogfoodCaptureAvailable,
} from "./dogfoodCapture";
import { pushBreadcrumb, clearBreadcrumbs } from "./dogfoodBreadcrumbs";

function storageKey(userId?: string | null): string {
  return userId
    ? `@yaver/u/${userId}/dogfood_mode_enabled`
    : "@yaver/dogfood_mode_enabled";
}

let enabled = false;
let userId: string | null = null;
let guestActive = false;
let appStateSub: { remove: () => void } | null = null;

type Listener = (on: boolean) => void;
const listeners = new Set<Listener>();

export function isDogfoodModeEnabled(): boolean {
  return enabled;
}

export function subscribeDogfoodMode(cb: Listener): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

function notify(): void {
  listeners.forEach((l) => {
    try {
      l(enabled);
    } catch {
      // ignore
    }
  });
}

/**
 * Load persisted state on app boot / sign-in. If it was on, re-arms capture +
 * recording. Pass the current user id so the flag is per-account.
 */
export async function loadDogfoodMode(uid?: string | null): Promise<boolean> {
  userId = uid ?? null;
  try {
    const raw = await AsyncStorage.getItem(storageKey(userId));
    const on = raw === "1" || raw === "true";
    if (on && !enabled) {
      await arm();
    } else if (!on && enabled) {
      await disarm();
    }
    enabled = on;
    notify();
    return on;
  } catch {
    return enabled;
  }
}

export async function setDogfoodModeEnabled(
  on: boolean,
  uid?: string | null,
): Promise<void> {
  if (uid !== undefined) userId = uid;
  try {
    await AsyncStorage.setItem(storageKey(userId), on ? "1" : "0");
  } catch {
    // best-effort persist
  }
  if (on && !enabled) {
    enabled = true;
    await arm();
  } else if (!on && enabled) {
    enabled = false;
    await disarm();
  }
  notify();
}

/** Called when a guest bundle is (un)loaded so we pause capture on host. */
export function setGuestActive(active: boolean): void {
  guestActive = active;
  if (!enabled) return;
  if (active) {
    void stopDogfoodCapture();
  } else if (AppState.currentState === "active") {
    void startDogfoodCapture();
  }
}

/** Record a route change into both the native payload + the breadcrumb trail. */
export function recordDogfoodRoute(path: string): void {
  if (!enabled) return;
  setDogfoodRoute(path);
  pushBreadcrumb("route", path);
}

/** Record a coarse interaction breadcrumb (tab switch, primary button, etc.). */
export function recordDogfoodTap(label: string): void {
  if (!enabled) return;
  pushBreadcrumb("tap", label);
}

/** Record a recent in-app error/toast so the agent sees what broke. */
export function recordDogfoodError(label: string): void {
  if (!enabled) return;
  pushBreadcrumb("error", label);
}

// MARK: - internal

async function arm(): Promise<void> {
  if (!appStateSub) {
    appStateSub = AppState.addEventListener("change", handleAppState);
  }
  if (AppState.currentState === "active" && !guestActive) {
    if (isDogfoodCaptureAvailable()) {
      await startDogfoodCapture();
    }
  }
}

async function disarm(): Promise<void> {
  await stopDogfoodCapture();
  if (appStateSub) {
    appStateSub.remove();
    appStateSub = null;
  }
  clearBreadcrumbs();
}

function handleAppState(state: AppStateStatus): void {
  if (!enabled) return;
  if (state === "active") {
    if (!guestActive && isDogfoodCaptureAvailable()) {
      void startDogfoodCapture();
    }
  } else {
    // background / inactive → pause; nothing recorded off-screen
    void stopDogfoodCapture();
  }
}
