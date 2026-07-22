// runtimeTurnAnnouncer.test.mts — completion announcements for watch/car.
// Covers transition-only firing, the no-replay-on-launch rule, and that a car
// line never carries a stack trace or an absolute path.
// Run: npx tsx src/lib/runtimeTurnAnnouncer.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  RuntimeTurnAnnouncer,
  announcementFor,
  carSafeLine,
} from "./runtimeTurnAnnouncer.ts";
import type { RuntimeTurnQueueItem } from "./runtimeSurfaceTypes.ts";

function item(over: Partial<RuntimeTurnQueueItem> = {}): RuntimeTurnQueueItem {
  return {
    itemId: "rq_1",
    state: "running",
    utterance: "fix the startup flicker",
    ...over,
  } as RuntimeTurnQueueItem;
}

test("first observation never announces — launching must not replay old work", () => {
  const a = new RuntimeTurnAnnouncer();
  const out = a.observe([
    item({ itemId: "rq_1", state: "done" }),
    item({ itemId: "rq_2", state: "failed" }),
  ]);
  assert.equal(out.length, 0);
});

test("announces only on entering an announceable state", () => {
  const a = new RuntimeTurnAnnouncer();
  a.observe([item({ state: "running" })]); // prime

  assert.equal(a.observe([item({ state: "running" })]).length, 0, "no change → silence");

  const fired = a.observe([item({ state: "ready_to_test" })]);
  assert.equal(fired.length, 1);
  assert.match(fired[0].spoken, /coded/);

  assert.equal(a.observe([item({ state: "ready_to_test" })]).length, 0, "must not repeat");
});

test("running → done announces once, not on every poll", () => {
  const a = new RuntimeTurnAnnouncer();
  a.observe([item({ state: "running" })]);
  assert.equal(a.observe([item({ state: "done" })]).length, 1);
  assert.equal(a.observe([item({ state: "done" })]).length, 0);
});

test("intermediate states are not worth interrupting anyone for", () => {
  const a = new RuntimeTurnAnnouncer();
  a.observe([item({ state: "captured" })]);
  assert.equal(a.observe([item({ state: "queued" })]).length, 0);
  assert.equal(a.observe([item({ state: "running" })]).length, 0);
});

test("needs_input and failed are urgent; ready_to_test is not", () => {
  assert.equal(announcementFor(item({ state: "needs_input" }))!.urgent, true);
  assert.equal(announcementFor(item({ state: "failed" }))!.urgent, true);
  assert.equal(announcementFor(item({ state: "ready_to_test" }))!.urgent, false);
});

test("ready_to_test only claims 'live' once a DEVICE verified it", () => {
  const unverified = announcementFor(item({ state: "ready_to_test" }))!;
  assert.ok(!/live/.test(unverified.spoken), `must not claim live: ${unverified.spoken}`);

  const verified = announcementFor(
    item({ state: "ready_to_test", testTarget: { state: "verified" } }),
  )!;
  assert.match(verified.spoken, /live on your phone/);
});

test("a failure never reads the stack trace aloud", () => {
  const a = announcementFor(
    item({
      state: "failed",
      error: "TypeError: undefined is not a function\n  at /Users/someone/proj/src/x.ts:12",
    }),
  )!;
  assert.ok(!a.spoken.includes("TypeError"), "spoken line leaked the error text");
  assert.ok(!a.spoken.includes("/Users/"), "spoken line leaked a home path");
  assert.match(a.spoken, /Details are on your phone/);
});

test("carSafeLine strips home paths and collapses to one short line", () => {
  const out = carSafeLine("boom at /Users/kivanc/Workspace/yaver.io/x.ts\nsecond line", 200);
  assert.ok(!out.includes("/Users/"), out);
  assert.ok(!out.includes("second line"), "must not carry extra lines");
  assert.match(out, /the project/);
});

test("carSafeLine strips Windows paths too", () => {
  const out = carSafeLine("failed at C:\\Users\\someone\\proj\\x.ts");
  assert.ok(!out.includes("C:\\Users"), out);
});

test("carSafeLine truncates long text with an ellipsis", () => {
  const out = carSafeLine("x".repeat(400), 50);
  assert.ok(out.length <= 50, `length ${out.length}`);
  assert.ok(out.endsWith("…"));
});

test("work spoken from another surface after priming still announces", () => {
  const a = new RuntimeTurnAnnouncer();
  a.observe([item({ itemId: "rq_1", state: "running" })]);
  // rq_2 appears for the first time already finished — spoken on the watch
  // while the phone was polling.
  const out = a.observe([
    item({ itemId: "rq_1", state: "running" }),
    item({ itemId: "rq_2", state: "needs_input" }),
  ]);
  assert.equal(out.length, 1);
  assert.equal(out[0].itemId, "rq_2");
});

test("forget lets a turn announce again", () => {
  const a = new RuntimeTurnAnnouncer();
  a.observe([item({ state: "running" })]);
  assert.equal(a.observe([item({ state: "done" })]).length, 1);
  a.forget("rq_1");
  assert.equal(a.observe([item({ state: "done" })]).length, 1);
});
