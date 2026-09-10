import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

test("startup progress cannot restart the decorative splash deadline", () => {
  const source = readFileSync(new URL("../components/YaverSplash.tsx", import.meta.url), "utf8");
  assert.doesNotMatch(source, /\[screen, fade, rise, pulse, onDone\]/);
  assert.match(source, /onDoneRef\.current\?\.\(\)/);
});
