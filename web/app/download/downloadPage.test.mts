import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const source = readFileSync(new URL("./page.tsx", import.meta.url), "utf8");

test("download page leads with desktop, then mobile, then CLI, then Raspberry Pi", () => {
  const desktop = source.indexOf('aria-labelledby="desktop-downloads"');
  const mobile = source.indexOf('aria-labelledby="mobile-downloads"');
  const cli = source.indexOf('aria-labelledby="cli-install"');
  const pi = source.indexOf('aria-labelledby="raspberry-pi"');
  assert.ok(desktop > 0 && desktop < mobile && mobile < cli && cli < pi);
});

test("download page exposes the friend path without the old installation wall", () => {
  assert.match(source, /Download APK/);
  assert.match(source, /Download \.deb/);
  assert.match(source, /Open Microsoft Store/);
  assert.match(source, /Signed x64 installer \(\.exe\)/);
  assert.match(source, /Already use OpenCode with DeepSeek/);
  assert.doesNotMatch(source, /One install path\. npm\./);
  assert.doesNotMatch(source, /Why one path:/);
});

test("Windows offers the released Microsoft Store app and the signed installer", () => {
  assert.match(source, /href=\{WINDOWS_STORE_URL\}/);
  assert.match(source, /href=\{GUI_DOWNLOADS\.winX64\}/);
});

test("download page uses a compact, non-redundant heading", () => {
  assert.match(source, />Downloads<\/h1>/);
  assert.match(source, /Choose your platform\./);
  assert.doesNotMatch(source, /Your development machine, in your pocket/);
  assert.doesNotMatch(source, /Install Yaver on your computer, add the phone app/);
});
