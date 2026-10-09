import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const source = readFileSync(new URL("../../app/studio-config.tsx", import.meta.url), "utf8");

test("Studio configuration turns a disconnected project probe into an in-place route", () => {
  assert.match(source, /Promise\.all\([\s\S]*?\)\.then\([\s\S]*?\)\.catch\(/);
  assert.match(source, /setLoadError\(/);
  assert.match(source, /Connect or re-select a machine to load its projects/);
});
