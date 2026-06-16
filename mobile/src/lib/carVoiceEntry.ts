// carVoiceEntry.ts — hands-free entry point for the Car Voice Coding loop.
//
// Goal: let the driver start a voice turn WITHOUT deep navigation — ideally a
// single physical/voice gesture (a Siri Shortcut, an Android app-shortcut /
// quick-action, a steering-wheel Bluetooth AVRCP button, or a CarPlay/Android
// Auto template tap) that wakes the phone straight into "listening".
//
// Reality of managed Expo (this repo): we do NOT own the native dirs
// (mobile/ios, mobile/android, mobile/plugins are off-limits — see CLAUDE.md
// file-ownership), so we cannot register an App Intent / SiriKit donation, a
// `UIApplicationShortcutItem`, or an Android `<shortcut>` from here. Those
// require native config another agent owns. What we CAN do — and do here — is:
//
//   1. Define the clean interface a native trigger should call. When the
//      native quick-action / Siri intent / Android-Auto action lands, it only
//      needs to (a) deep-link into `/car-voice-coding?autostart=1` OR (b) call
//      `carVoiceEntryBus.requestTurn()`. Both routes converge on the same PTT.
//   2. Ship a WORKING in-app fallback: a tiny pub-sub bus the screen
//      subscribes to, plus a `shouldAutostart()` reader for the deep-link
//      query param. Until the native trigger exists, the user taps the big
//      push-to-talk button; the moment it exists, it fires `requestTurn()` and
//      the loop starts with zero taps.
//
// ─────────────────────────────────────────────────────────────────────────
//  NATIVE GAP (handed to whoever owns mobile/ios + mobile/android + plugins):
//
//  iOS (SiriKit / App Intents): add an App Intent "Start Yaver Car Coding"
//    whose perform() opens URL `yaver://car-voice-coding?autostart=1` (or
//    posts to the JS bridge). Donate it so "Hey Siri, start car coding" works.
//    Add a Home-Screen quick action (UIApplicationShortcutItem) with the same
//    target for a long-press launch.
//
//  Android (app shortcut / Assistant): add a static `<shortcut>` in
//    res/xml/shortcuts.xml with an intent that deep-links the same URL, and
//    (optionally) an Android-Auto / Assistant App Action mapping the
//    "START_EXERCISE"-style BII to the same deep link.
//
//  Steering-wheel button: AVRCP media-button capture needs a native media
//    session; out of scope for managed Expo. The Bluetooth car-AUDIO path
//    (TTS readback over the car speakers) already works with no entitlement —
//    only the INPUT trigger needs native help.
// ─────────────────────────────────────────────────────────────────────────

/** The contract a native hands-free trigger fulfils. Implemented in JS by
 *  `carVoiceEntryBus`; a native module/App-Intent only needs to call
 *  `requestTurn()` (or deep-link with `?autostart=1`, which the screen turns
 *  into the same call). */
export interface CarVoiceEntryTrigger {
  /** Ask the (mounted) Car Voice screen to begin a push-to-talk turn now. */
  requestTurn(): void;
  /** Subscribe to turn requests. Returns an unsubscribe fn. */
  subscribe(cb: () => void): () => void;
}

type Listener = () => void;

const listeners = new Set<Listener>();
let pending = false;

/**
 * In-app trigger bus. The screen subscribes on mount; any source (a native
 * quick action that reached JS, a deep link, or an in-app shortcut) calls
 * `requestTurn()` to kick off a hands-free turn. If no screen is mounted yet
 * the request is held and replayed on the next subscribe, so a cold-start
 * deep-link still lands.
 */
export const carVoiceEntryBus: CarVoiceEntryTrigger = {
  requestTurn() {
    if (listeners.size === 0) {
      pending = true;
      return;
    }
    listeners.forEach((cb) => {
      try {
        cb();
      } catch {
        // one bad listener mustn't block the others
      }
    });
  },

  subscribe(cb: Listener): () => void {
    listeners.add(cb);
    if (pending) {
      pending = false;
      setTimeout(() => cb(), 0);
    }
    return () => {
      listeners.delete(cb);
    };
  },
};

/**
 * Read whether the screen was opened by a hands-free deep link
 * (`/car-voice-coding?autostart=1`). Pure helper so it's testable without the
 * router. Accepts the raw search-param value expo-router hands us (string,
 * string[], or undefined).
 */
export function shouldAutostart(param: string | string[] | undefined): boolean {
  const v = Array.isArray(param) ? param[0] : param;
  if (!v) return false;
  const t = String(v).toLowerCase();
  return t === "1" || t === "true" || t === "yes" || t === "on";
}
