// watchEntry.test.mts — the native-transport adapter: message parsing/
// validation and the configure → deliver → sender wiring.
// Run: npx tsx src/lib/watchEntry.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { watchBridgeBus, parseTurn } from "./watchEntry.ts";
import type { WatchReply } from "./watchBridge.ts";
import type { CarVoiceDeps, CarVoiceTaskRef } from "./carVoiceCoding.ts";

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

test("parseTurn accepts valid v1 messages", () => {
  assert.deepEqual(parseTurn('{"v":1,"kind":"transcript","text":"hi"}'), {
    v: 1, kind: "transcript", text: "hi",
  });
  assert.deepEqual(parseTurn('{"v":1,"kind":"confirm","token":"x","reply":"confirm"}'), {
    v: 1, kind: "confirm", token: "x", reply: "confirm",
  });
  assert.deepEqual(parseTurn('{"v":1,"kind":"intent","intent":"run-tests"}'), {
    v: 1, kind: "intent", intent: "run-tests",
  });
});

test("parseTurn rejects malformed / wrong-version / unknown-kind input", () => {
  assert.equal(parseTurn("not json"), null);
  assert.equal(parseTurn("null"), null);
  assert.equal(parseTurn('{"v":2,"kind":"transcript","text":"hi"}'), null); // wrong version
  assert.equal(parseTurn('{"v":1,"kind":"bogus"}'), null);                  // unknown kind
  assert.equal(parseTurn('{"v":1,"kind":"transcript"}'), null);            // missing text
  assert.equal(parseTurn('{"v":1,"kind":"confirm","token":"x"}'), null);   // missing reply
});

test("deliver returns null until configured", async () => {
  watchBridgeBus.reset();
  const r = await watchBridgeBus.deliver('{"v":1,"kind":"transcript","text":"hi"}');
  assert.equal(r, null);
});

test("configure → deliver runs the bridge and pipes replies to the sender", async () => {
  const sent: WatchReply[] = [];
  watchBridgeBus.configure({
    makeDeps: () => deps(),
    sender: (json) => void sent.push(JSON.parse(json) as WatchReply),
  });
  const final = await watchBridgeBus.deliver('{"v":1,"kind":"transcript","text":"add a test"}');
  assert.equal(final?.kind, "summary");
  assert.ok(sent.some((r) => r.kind === "ack"));
  assert.equal(sent.at(-1)?.kind, "summary");
  watchBridgeBus.reset();
});

test("a bad inbound message sends a spoken error", async () => {
  const sent: WatchReply[] = [];
  watchBridgeBus.configure({ makeDeps: () => deps(), sender: (json) => void sent.push(JSON.parse(json) as WatchReply) });
  const final = await watchBridgeBus.deliver("garbage");
  assert.equal(final?.kind, "error");
  assert.equal(sent.at(-1)?.kind, "error");
  watchBridgeBus.reset();
});
