// carSurfaceIntent.test.mts
// Run: npx tsx src/lib/carSurfaceIntent.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  classifyCarSurfaceIntent,
  executeCarSurfaceIntent,
} from "./carSurfaceIntent.ts";

test("classifies join next Teams meeting as car meeting_join_next", () => {
  const intent = classifyCarSurfaceIntent("join my next Teams meeting");
  assert.equal(intent?.kind, "meeting_join_next");
  assert.equal(intent?.payload.provider, "teams");
  assert.equal(intent?.payload.open, true);
  assert.equal(intent?.payload.openMode, "browser");
  assert.equal(intent?.payload.surface, "car");
});

test("classifies next meeting lookup without unsupported surface payload", () => {
  const intent = classifyCarSurfaceIntent("when is my next meeting");
  assert.equal(intent?.kind, "meeting_next");
  assert.equal(intent?.payload.withinHours, 24);
  assert.equal("surface" in (intent?.payload || {}), false);
});

test("classifies incoming email check", () => {
  const intent = classifyCarSurfaceIntent("check my incoming Gmail");
  assert.equal(intent?.kind, "mail_unread");
  assert.equal(intent?.payload.provider, "gmail");
  assert.equal(intent?.payload.onlyPersonal, true);
});

test("execute calls ops and formats meeting response", async () => {
  const calls: Array<{ verb: string; payload: Record<string, unknown> }> = [];
  const result = await executeCarSurfaceIntent("join my next zoom call", async (verb, payload) => {
    calls.push({ verb, payload });
    return { title: "Customer sync", opened: true };
  });
  assert.equal(result.handled, true);
  assert.equal(calls[0].verb, "meeting_join_next");
  assert.match(result.spoken, /Opening Customer sync/);
});

test("send email without address is handled as phone handoff", async () => {
  const result = await executeCarSurfaceIntent("send an email that I am late", async () => {
    throw new Error("should not call ops");
  });
  assert.equal(result.handled, true);
  assert.match(result.spoken, /specific email address/i);
});
