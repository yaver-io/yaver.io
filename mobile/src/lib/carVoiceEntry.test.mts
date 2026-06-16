// carVoiceEntry.test.mts — hands-free entry: the deep-link autostart reader
// and the in-app trigger bus (subscribe / requestTurn / cold-start replay).
// Run: npx tsx src/lib/carVoiceEntry.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { carVoiceEntryBus, shouldAutostart } from "./carVoiceEntry.ts";

test("shouldAutostart accepts the truthy query-param forms", () => {
  for (const v of ["1", "true", "yes", "on", "TRUE", "Yes"]) {
    assert.equal(shouldAutostart(v), true, v);
  }
  assert.equal(shouldAutostart(["1"]), true); // expo-router can hand an array
});

test("shouldAutostart rejects falsey / missing", () => {
  for (const v of [undefined, "", "0", "false", "no", "off"] as (string | undefined)[]) {
    assert.equal(shouldAutostart(v), false, String(v));
  }
});

test("entry bus delivers requestTurn to a subscriber", () => {
  let fired = 0;
  const unsub = carVoiceEntryBus.subscribe(() => { fired++; });
  carVoiceEntryBus.requestTurn();
  assert.equal(fired, 1);
  unsub();
  carVoiceEntryBus.requestTurn(); // no subscriber now → held as pending
  assert.equal(fired, 1);
});

test("entry bus replays a pending request to the next subscriber", async () => {
  // Drain any pending from the previous test by subscribing+unsubscribing.
  let drained = 0;
  carVoiceEntryBus.subscribe(() => { drained++; })();
  // Now request with no listeners → pending.
  carVoiceEntryBus.requestTurn();
  let replayed = 0;
  await new Promise<void>((resolve) => {
    carVoiceEntryBus.subscribe(() => { replayed++; resolve(); });
  });
  assert.ok(replayed >= 1);
});
