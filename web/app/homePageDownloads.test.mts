import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const source = readFileSync(new URL("./page.tsx", import.meta.url), "utf8");

test("homepage Android card exposes the direct signed APK and Google Play", () => {
  assert.match(source, /https:\/\/download\.yaver\.io\/latest\.apk/);
  assert.match(source, /Download APK/);
  assert.match(source, /https:\/\/play\.google\.com\/store\/apps\/details\?id=io\.yaver\.mobile/);
  assert.match(source, /Signed APK or Google Play/);
});
