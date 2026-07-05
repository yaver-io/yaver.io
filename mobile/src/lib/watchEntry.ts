// watchEntry.ts — the JS hub that wires the native watch transport to the
// phone-side bridge (watchBridge.ts).
//
// The native module owns the wire: WCSession (watchOS) or the Wear Data
// Layer (Wear OS). It has exactly two responsibilities, and this file is the
// only contract it needs:
//
//   inbound:  when a JSON message arrives from the wrist, call
//             `watchBridgeBus.deliver(json)`.
//   outbound: register a `sender(json)` once at startup; the bridge calls it
//             for every reply (ack / working / summary / confirm-needed / …)
//             and the native side ships the JSON back to the watch.
//
// Everything substantive (guards, confirm handshake, dispatch, summarize)
// lives in watchBridge.ts and is unit-tested headless. This file is just the
// thin, app-mounted adapter that hands the bridge real deps (which box to
// dispatch to, the user's STT/TTS config) and pipes replies back out. It
// mirrors carVoiceEntry.ts's role for the car loop: keep the native gap to a
// single, well-typed seam.
//
// ─────────────────────────────────────────────────────────────────────────
//  NATIVE GAP (handed to whoever owns the native targets + plugins):
//   iOS  — mobile/native-watch/ios/YaverWatchBridge.swift: a WCSession
//          delegate on the PHONE. On didReceiveMessage → emit the JSON to JS
//          (DeviceEventEmitter "yaverWatchMessage"); expose sendToWatch(json).
//   Android — mobile/native-wear/android/YaverWearBridgeModule.kt: a
//          WearableListenerService on PATH_TURN → emit JSON to JS; expose
//          sendToWatch(json) via MessageClient on PATH_REPLY.
//   Both are copied into the prebuild by plugins/withWatchBridge.js (kept
//   UNREGISTERED in app.json until the native targets are built — same
//   posture as withMeshTunnel.js).
// ─────────────────────────────────────────────────────────────────────────

import { handleWatchTurn, type WatchReply, type WatchTurn } from "./watchBridge";
import type { CarVoiceConfig, CarVoiceDeps } from "./carVoiceCoding";
import type { CarSurfaceOps } from "./carSurfaceIntent";

/** What the app must provide so the bridge can actually do work: the deps for
 *  the chosen remote box and the user's speech config. Supplied by a factory
 *  so the box can be re-resolved per turn (the user may switch primary). */
export interface WatchBridgeWiring {
  /** Build the loop deps for the box this turn should run on. */
  makeDeps: () => CarVoiceDeps;
  /** STT/TTS + poll config (usually the same the car loop uses). */
  config?: () => CarVoiceConfig;
  /** Optional constrained-surface ops handler for mail/meetings/media/maps. */
  ops?: CarSurfaceOps;
  /** Ship a reply JSON string back to the wrist (the native sender). */
  sender: (json: string) => void;
}

type Wiring = WatchBridgeWiring | null;

let wiring: Wiring = null;

/** The contract the native transport (and app root) talk to. */
export interface WatchBridgeBus {
  /** App root calls this once with the box/deps/config + native sender. */
  configure(w: WatchBridgeWiring): void;
  /** Native transport calls this with each inbound JSON message from the
   *  wrist. Returns the final reply (also pushed via the sender) or null when
   *  the bus isn't configured / the message is unparseable. */
  deliver(json: string): Promise<WatchReply | null>;
  /** Tear down (screen unmount / sign-out). */
  reset(): void;
}

export const watchBridgeBus: WatchBridgeBus = {
  configure(w: WatchBridgeWiring) {
    wiring = w;
  },

  async deliver(json: string): Promise<WatchReply | null> {
    const w = wiring;
    if (!w) return null; // not wired yet — native side should retry after mount
    const msg = parseTurn(json);
    if (!msg) {
      const err: WatchReply = { v: 1, kind: "error", spoken: "I didn't understand that." };
      safeSend(w, err);
      return err;
    }
    const config = w.config?.() ?? {};
    try {
      return await handleWatchTurn(msg, w.makeDeps(), config, (r) => safeSend(w, r), w.ops);
    } catch (e) {
      const err: WatchReply = {
        v: 1,
        kind: "error",
        spoken: "Something went wrong on your phone.",
      };
      safeSend(w, err);
      return err;
    }
  },

  reset() {
    wiring = null;
  },
};

/** Parse + minimally validate an inbound wire message. Unknown/old versions
 *  and malformed kinds are rejected (returns null) rather than trusted. */
export function parseTurn(json: string): WatchTurn | null {
  let obj: unknown;
  try {
    obj = JSON.parse(json);
  } catch {
    return null;
  }
  if (!obj || typeof obj !== "object") return null;
  const m = obj as Record<string, unknown>;
  if (m.v !== 1) return null;
  switch (m.kind) {
    case "transcript":
      return typeof m.text === "string" ? { v: 1, kind: "transcript", text: m.text } : null;
    case "confirm":
      return typeof m.token === "string" && typeof m.reply === "string"
        ? { v: 1, kind: "confirm", token: m.token, reply: m.reply }
        : null;
    case "intent":
      return typeof m.intent === "string" ? { v: 1, kind: "intent", intent: m.intent } : null;
    default:
      return null;
  }
}

function safeSend(w: WatchBridgeWiring, r: WatchReply): void {
  try {
    w.sender(JSON.stringify(r));
  } catch {
    // A dead transport must not crash the turn — the wrist just won't update.
  }
}
