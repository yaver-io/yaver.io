// watchBridge.test.mts — phone-side smartwatch bridge: the transcript →
// guards → dispatch flow, the confirm round-trip for risky writes, the
// read-code handoff, complication-intent expansion, and the reply stream
// (ack → working → summary) pushed to the wrist.
// Run: npx tsx src/lib/watchBridge.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  handleWatchTurn,
  watchIntentToTranscript,
  type WatchReply,
  type WatchTurn,
} from "./watchBridge.ts";
import type { CarVoiceDeps, CarVoiceTaskRef } from "./carVoiceCoding.ts";

// ── tiny deps + capture harness ──────────────────────────────────────

function deps(over: Partial<CarVoiceDeps> = {}): CarVoiceDeps {
  return {
    transcribe: async () => "add a test",
    dispatch: async () => "task-1",
    getTask: async (): Promise<CarVoiceTaskRef> => ({ id: "task-1", status: "completed", resultText: "Tests pass." }),
    speak: async () => {},
    sleep: async () => {},
    now: () => 0,
    ...over,
  };
}

/** Capture every reply the bridge pushes to the wrist. */
function capture() {
  const sent: WatchReply[] = [];
  return { send: (r: WatchReply) => void sent.push(r), sent };
}

// ── plain transcript: ack → summary ──────────────────────────────────

test("a safe transcript dispatches and streams ack then summary", async () => {
  const { send, sent } = capture();
  let dispatchedPrompt = "";
  const final = await handleWatchTurn(
    { v: 1, kind: "transcript", text: "add a test" } as WatchTurn,
    deps({ dispatch: async (_title, prompt) => { dispatchedPrompt = prompt; return "task-1"; } }),
    {},
    send,
  );
  assert.equal(final.kind, "summary");
  assert.match(final.spoken!, /Done\./);
  assert.match(dispatchedPrompt, /smartwatch/i);
  assert.match(dispatchedPrompt, /Watch transcript: add a test/i);
  const kinds = sent.map((r) => r.kind);
  assert.deepEqual(kinds[0], "ack");          // "On it" fires first
  assert.equal(kinds.at(-1), "summary");      // summary last
});

test("walking idea is dispatched as idea capture, not blind code edit", async () => {
  let dispatchedPrompt = "";
  const final = await handleWatchTurn(
    { v: 1, kind: "transcript", text: "idea for sfmg owner mode sponsors" } as WatchTurn,
    deps({ dispatch: async (_title, prompt) => { dispatchedPrompt = prompt; return "task-1"; } }),
  );
  assert.equal(final.kind, "summary");
  assert.match(dispatchedPrompt, /Treat this as idea capture/i);
  assert.match(dispatchedPrompt, /Do not edit code/i);
});

// ── risky write: confirm round-trip ──────────────────────────────────

test("a risky transcript asks for confirmation instead of dispatching", async () => {
  const { send, sent } = capture();
  let dispatched = false;
  const final = await handleWatchTurn(
    { v: 1, kind: "transcript", text: "deploy to production" } as WatchTurn,
    deps({ dispatch: async () => { dispatched = true; return "x"; } }),
    {},
    send,
  );
  assert.equal(final.kind, "confirm-needed");
  assert.equal(dispatched, false, "must NOT dispatch before confirmation");
  assert.ok(final.token && final.token.includes("deploy"), "token carries the transcript");
  assert.match(final.prompt!, /deploy|production/);
  assert.deepEqual(sent.map((r) => r.kind), ["confirm-needed"]);
});

test("confirm:confirm dispatches the echoed transcript", async () => {
  const { send } = capture();
  let dispatchedText = "";
  const final = await handleWatchTurn(
    { v: 1, kind: "confirm", token: "deploy to production", reply: "confirm" } as WatchTurn,
    deps({ dispatch: async (_t, prompt) => { dispatchedText = prompt; return "task-9"; } }),
    {},
    send,
  );
  assert.equal(final.kind, "summary");
  assert.match(dispatchedText, /Watch transcript: deploy to production/i);
  assert.match(dispatchedText, /permission to work/i);
});

test("confirm:cancel (and anything unclear) fails safe without dispatching", async () => {
  for (const replyText of ["cancel", "no", "uh maybe"]) {
    let dispatched = false;
    const final = await handleWatchTurn(
      { v: 1, kind: "confirm", token: "deploy to production", reply: replyText } as WatchTurn,
      deps({ dispatch: async () => { dispatched = true; return "x"; } }),
    );
    assert.equal(final.kind, "ack");
    assert.match(final.spoken!, /Cancelled/);
    assert.equal(dispatched, false, `"${replyText}" must not dispatch`);
  }
});

// ── read-code handoff ────────────────────────────────────────────────

test("read-the-code asks hand off to the phone, never dispatch", async () => {
  let dispatched = false;
  const final = await handleWatchTurn(
    { v: 1, kind: "transcript", text: "read me the diff" } as WatchTurn,
    deps({ dispatch: async () => { dispatched = true; return "x"; } }),
  );
  assert.equal(final.kind, "handoff");
  assert.equal(final.target, "phone");
  assert.equal(dispatched, false);
});

test("surface media transcript is handled before coding dispatch", async () => {
  let dispatched = false;
  const calls: Array<{ verb: string; payload: Record<string, unknown> }> = [];
  const final = await handleWatchTurn(
    { v: 1, kind: "transcript", text: "open Twitch hasanabi" } as WatchTurn,
    deps({ dispatch: async () => { dispatched = true; return "x"; } }),
    {},
    undefined,
    async (verb, payload) => {
      calls.push({ verb, payload });
      return { spoken: "Opening Twitch for hasanabi." };
    },
  );
  assert.equal(final.kind, "summary");
  assert.match(final.spoken!, /Twitch/);
  assert.equal(calls[0].verb, "media_open");
  assert.equal(dispatched, false);
});

test("surface maps transcript is handled before coding dispatch", async () => {
  let dispatched = false;
  const calls: Array<{ verb: string; payload: Record<string, unknown> }> = [];
  const final = await handleWatchTurn(
    { v: 1, kind: "transcript", text: "check Google Maps traffic to Taksim" } as WatchTurn,
    deps({ dispatch: async () => { dispatched = true; return "x"; } }),
    {},
    undefined,
    async (verb, payload) => {
      calls.push({ verb, payload });
      return { spoken: "Opening Google Maps traffic for Taksim." };
    },
  );
  assert.equal(final.kind, "summary");
  assert.match(final.spoken!, /Google Maps/);
  assert.equal(calls[0].verb, "maps_open");
  assert.equal(dispatched, false);
});

// ── complication intents ─────────────────────────────────────────────

test("run-tests intent expands and dispatches", async () => {
  const final = await handleWatchTurn({ v: 1, kind: "intent", intent: "run-tests" } as WatchTurn, deps());
  assert.equal(final.kind, "summary");
});

test("deploy intent still hits the confirm gate", async () => {
  const final = await handleWatchTurn({ v: 1, kind: "intent", intent: "deploy" } as WatchTurn, deps());
  assert.equal(final.kind, "confirm-needed");
});

test("unknown intent errors cleanly", async () => {
  const final = await handleWatchTurn({ v: 1, kind: "intent", intent: "nonsense" } as WatchTurn, deps());
  assert.equal(final.kind, "error");
});

test("watchIntentToTranscript matches the Go mirror's known set", () => {
  assert.ok(watchIntentToTranscript("run-tests").length > 0);
  assert.equal(watchIntentToTranscript("deploy"), "deploy");
  assert.equal(watchIntentToTranscript("nonsense"), "");
});

// ── failure surfaces as a spoken error ───────────────────────────────

test("an unreachable box surfaces a spoken error, not a crash", async () => {
  const final = await handleWatchTurn(
    { v: 1, kind: "transcript", text: "add a test" } as WatchTurn,
    deps({ dispatch: async () => { throw new Error("no box"); } }),
  );
  assert.equal(final.kind, "error");
  assert.ok(final.spoken && final.spoken.length > 0);
});

// ── empty input ──────────────────────────────────────────────────────

test("empty transcript is handled, not dispatched", async () => {
  let dispatched = false;
  const final = await handleWatchTurn(
    { v: 1, kind: "transcript", text: "   " } as WatchTurn,
    deps({ dispatch: async () => { dispatched = true; return "x"; } }),
  );
  assert.equal(final.kind, "error");
  assert.equal(dispatched, false);
});
