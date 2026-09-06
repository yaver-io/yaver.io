/** `npx tsx src/lib/inflightAttemptRegistry.test.ts` */
import assert from "node:assert/strict";
import test from "node:test";
import { InflightAttemptRegistry } from "./inflightAttemptRegistry.ts";

test("disconnect invalidation lets a fresh device attempt start", () => {
  const attempts = new InflightAttemptRegistry<Promise<void>>();
  const retired = new Promise<void>(() => {});
  attempts.set("ubuntu", retired);

  attempts.invalidate("ubuntu");

  assert.equal(attempts.get("ubuntu"), undefined);
});

test("a retired promise cannot clear its replacement when it settles late", () => {
  const attempts = new InflightAttemptRegistry<Promise<void>>();
  const retired = Promise.resolve();
  const replacement = Promise.resolve();

  attempts.set("ubuntu", retired);
  attempts.invalidate("ubuntu");
  attempts.set("ubuntu", replacement);

  assert.equal(attempts.release("ubuntu", retired), false);
  assert.equal(attempts.get("ubuntu"), replacement);
  assert.equal(attempts.release("ubuntu", replacement), true);
  assert.equal(attempts.get("ubuntu"), undefined);
});

test("negative control: unconditional late cleanup deletes the live retry", () => {
  const oldRegistry = new Map<string, Promise<void>>();
  const retired = Promise.resolve();
  const replacement = Promise.resolve();

  oldRegistry.set("ubuntu", retired);
  oldRegistry.delete("ubuntu");
  oldRegistry.set("ubuntu", replacement);
  // The old `.finally(() => map.delete(id))` did this when `retired` settled.
  oldRegistry.delete("ubuntu");

  assert.equal(oldRegistry.has("ubuntu"), false);
});
