import { test } from "node:test";
import assert from "node:assert/strict";
import { nextTurnsCacheIndex } from "./storage";

// The turns cache is LRU-bounded so a phone with thousands of tasks can't grow
// AsyncStorage without limit. These lock the bounding + recency invariants that
// keep "instant re-open" from becoming "unbounded disk".

test("moves an opened task to the front (most-recent-first)", () => {
  const { index, evicted } = nextTurnsCacheIndex(["a", "b", "c"], "c");
  assert.deepEqual(index, ["c", "a", "b"]);
  assert.deepEqual(evicted, []);
});

test("a brand-new task is prepended without duplicating", () => {
  const { index } = nextTurnsCacheIndex(["a", "b"], "z");
  assert.deepEqual(index, ["z", "a", "b"]);
});

test("re-opening never creates a duplicate id", () => {
  const { index } = nextTurnsCacheIndex(["a", "b", "a"], "a");
  assert.equal(index.filter((x) => x === "a").length, 1);
  assert.equal(index[0], "a");
});

test("evicts the oldest beyond the cap", () => {
  const prev = ["t4", "t3", "t2", "t1"]; // t1 is oldest
  const { index, evicted } = nextTurnsCacheIndex(prev, "t5", 3);
  assert.deepEqual(index, ["t5", "t4", "t3"]);
  assert.deepEqual(evicted, ["t2", "t1"]); // both evicted blobs get removed
});

test("promoting an existing task at cap evicts the tail, not the promoted one", () => {
  const prev = ["a", "b", "c"]; // cap 3, full
  const { index, evicted } = nextTurnsCacheIndex(prev, "c", 3);
  assert.deepEqual(index, ["c", "a", "b"]);
  assert.deepEqual(evicted, []); // no growth, so nothing evicted
});
