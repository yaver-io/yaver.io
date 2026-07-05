/**
 * watchBridge.ts — the PHONE side of the smartwatch loop.
 *
 * See docs/yaver-smartwatch-voice-terminal.md. In the DEFAULT transport
 * (phone-paired, §3 mode A) the watch owns nothing: it ships a spoken
 * transcript to the phone over WCSession (watchOS) / the Wear Data Layer
 * (Wear OS), and THIS module runs the exact same loop the car uses
 * (carVoiceCoding.ts::dispatchAndSummarize) against the chosen remote box,
 * then sends one short sentence back to the wrist.
 *
 * Why a separate bridge instead of calling dispatchAndSummarize directly:
 *   1. The watch input is a discrete message protocol, not a screen — we
 *      translate WatchTurn → loop → WatchReply(s).
 *   2. Risky writes must be confirm-GATED before dispatch (carVoiceConfirm),
 *      and the confirm round-trips through the watch. That handshake is the
 *      bridge's job; the car screen did it inline.
 *   3. The watch needs INTERMEDIATE replies (ack, working) pushed as they
 *      happen, plus a final summary — a message stream, not a return value.
 *
 * Everything is dependency-injected (CarVoiceDeps) and the wire protocol is
 * plain data, so this runs headless under `npx tsx` — see watchBridge.test.mts.
 * The native WCSession / Data-Layer module only has to (a) hand each inbound
 * JSON message to `handleWatchTurn` and (b) forward each `send(reply)` back to
 * the watch. That native glue + its Expo plugin is mobile/native-watch +
 * mobile/native-wear + plugins/withWatchBridge.js (unregistered until wired).
 */

import {
  dispatchAndSummarize,
  isReadCodeRequest,
  type CarVoiceConfig,
  type CarVoiceDeps,
} from "./carVoiceCoding";
import { assessRisk, interpretConfirmReply } from "./carVoiceConfirm";
import {
  executeCarSurfaceIntent,
  type CarSurfaceOps,
} from "./carSurfaceIntent";

// ── Wire protocol v1 ─────────────────────────────────────────────────
// The single TS source of truth. Mirrored byte-for-byte by:
//   watch/YaverWatch/WatchProtocol.swift  (Apple Watch)
//   wear/app/.../WatchProtocol.kt          (Wear OS)
//   desktop/agent/watch_http.go            (standalone, no-phone path)
// `token` is OPAQUE to the watch — each server defines it however it likes
// (this bridge uses the transcript text itself, so confirm is stateless).

export const WATCH_PROTOCOL_VERSION = 1 as const;

/** Watch → phone. */
export type WatchTurn =
  | { v: 1; kind: "transcript"; text: string }
  | { v: 1; kind: "confirm"; token: string; reply: string }
  | { v: 1; kind: "intent"; intent: string };

/** Phone → watch. Only the fields relevant to `kind` are set. */
export interface WatchReply {
  v: 1;
  kind: "ack" | "confirm-needed" | "working" | "summary" | "error" | "handoff";
  /** The short line the wrist speaks / shows. */
  spoken?: string;
  /** confirm-needed: opaque echo token + the prompt to display. */
  token?: string;
  prompt?: string;
  /** working / summary: the task being tracked. */
  taskId?: string;
  status?: string;
  /** handoff: where the detail went ("phone"). */
  target?: string;
}

function reply(kind: WatchReply["kind"], extra: Partial<WatchReply> = {}): WatchReply {
  return { v: WATCH_PROTOCOL_VERSION, kind, ...extra };
}

// ── Complication intents ─────────────────────────────────────────────
// A watch-face complication sends a fixed intent instead of a transcript.
// Expand the small fixed set into the transcript the loop understands.
// Mirrors watchIntentToTranscript in watch_risk.go — keep them in sync.

export function watchIntentToTranscript(intent: string): string {
  switch (intent.trim().toLowerCase()) {
    case "run-tests":
    case "tests":
    case "test":
      return "run the tests on the primary device and tell me if they pass";
    case "deploy":
      return "deploy"; // still routed through the risk gate below
    case "status":
      return "give me a one-line status of the current work";
    default:
      return "";
  }
}

// ── The bridge ───────────────────────────────────────────────────────

/**
 * Handle one inbound WatchTurn. Pushes every reply (ack / working / summary /
 * confirm-needed / handoff / error) to the wrist via `send`, and also returns
 * the FINAL reply for callers/tests that want it directly.
 *
 * Flow:
 *   intent      → expand to a transcript, then fall into the transcript path
 *   transcript  → read-code? → handoff
 *                 risky?     → confirm-needed (token = the transcript)
 *                 else       → ack "On it" → dispatchAndSummarize → summary
 *   confirm     → "confirm" → dispatch the echoed transcript (gate satisfied)
 *                 anything else (incl. "unclear") → ack "Cancelled" (fail-safe)
 */
export async function handleWatchTurn(
  msg: WatchTurn,
  deps: CarVoiceDeps,
  config: CarVoiceConfig = {},
  send: (r: WatchReply) => void = () => {},
  ops?: CarSurfaceOps,
): Promise<WatchReply> {
  switch (msg.kind) {
    case "intent": {
      const text = watchIntentToTranscript(msg.intent);
      if (!text) return emit(send, reply("error", { spoken: "I don't know that shortcut." }));
      return runTranscript(text, deps, config, send, ops);
    }
    case "confirm": {
      // Negation / ambiguity fails safe — only an explicit confirm proceeds.
      if (interpretConfirmReply(msg.reply) !== "confirm") {
        return emit(send, reply("ack", { spoken: "Cancelled." }));
      }
      const text = (msg.token || "").trim();
      if (!text) return emit(send, reply("error", { spoken: "I lost what you were confirming." }));
      // Risk gate already satisfied by the explicit confirm — dispatch.
      return dispatch(text, deps, config, send);
    }
    case "transcript":
      return runTranscript(msg.text, deps, config, send, ops);
    default:
      return emit(send, reply("error", { spoken: "I didn't understand that." }));
  }
}

/** Apply the read-code + risk guards, then dispatch. */
async function runTranscript(
  text: string,
  deps: CarVoiceDeps,
  config: CarVoiceConfig,
  send: (r: WatchReply) => void,
  ops?: CarSurfaceOps,
): Promise<WatchReply> {
  const clean = (text || "").trim();
  if (!clean) return emit(send, reply("error", { spoken: "I didn't catch that." }));

  if (isReadCodeRequest(clean)) {
    return emit(send, reply("handoff", {
      target: "phone",
      spoken: "I won't read code on your wrist — it'll be on your phone.",
    }));
  }

  if (ops) {
    const surface = await executeCarSurfaceIntent(clean, ops);
    if (surface.handled) {
      return emit(send, reply("summary", { spoken: surface.spoken }));
    }
  }

  const risk = assessRisk(clean);
  if (risk.risky) {
    // token = the transcript itself; the watch echoes it back on confirm so
    // the phone needs no pending-state map.
    return emit(send, reply("confirm-needed", { token: clean, prompt: risk.prompt }));
  }

  return dispatch(clean, deps, config, send);
}

/** Run the shared car loop and stream replies to the wrist. */
async function dispatch(
  text: string,
  deps: CarVoiceDeps,
  config: CarVoiceConfig,
  send: (r: WatchReply) => void,
): Promise<WatchReply> {
  let acked = false;
  const result = await dispatchAndSummarize(text, deps, config, (s) => {
    if (s.stage === "dispatched") {
      acked = true;
      send(reply("ack", { taskId: s.taskId, spoken: s.text || "On it." }));
    } else if (s.stage === "working") {
      send(reply("working", { taskId: s.taskId, status: s.status }));
    }
  });

  if (result.error && !acked) {
    return emit(send, reply("error", { spoken: result.spoken || "Something went wrong." }));
  }
  return emit(send, reply("summary", {
    taskId: result.taskId,
    status: result.status,
    spoken: result.spoken,
  }));
}

function emit(send: (r: WatchReply) => void, r: WatchReply): WatchReply {
  send(r);
  return r;
}
