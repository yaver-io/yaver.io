import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const source = readFileSync(new URL("./page.tsx", import.meta.url), "utf8");

test("download page leads with desktop, then mobile, then CLI", () => {
  const desktop = source.indexOf('aria-labelledby="desktop-downloads"');
  const mobile = source.indexOf('aria-labelledby="mobile-downloads"');
  const cli = source.indexOf('aria-labelledby="cli-install"');
  assert.ok(desktop > 0 && desktop < mobile && mobile < cli);
  assert.doesNotMatch(source, /Turn a Pi into a Yaver node/);
  assert.doesNotMatch(source, /aria-labelledby="raspberry-pi"/);
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

test("download page starts with the useful desktop section, not a repeated brand banner", () => {
  assert.match(source, /<h1 className="sr-only">Yaver downloads<\/h1>/);
  assert.doesNotMatch(source, /<header/);
  assert.doesNotMatch(source, /Choose your platform\./);
  assert.doesNotMatch(source, /Your development machine, in your pocket/);
  assert.doesNotMatch(source, /Install Yaver on your computer, add the phone app/);
});

test("desktop cards reserve an aligned CTA lane with space above every button", () => {
  assert.match(source, /className="mt-auto pt-6"/);
  assert.match(source, /className=\{`\$\{primaryButton\} w-full`\}/);
  assert.doesNotMatch(source, /\$\{primaryButton\} mt-auto pt-2/);
});

test("primary download buttons keep contrast when hovered in light mode", () => {
  assert.match(source, /hover:bg-surface-100/);
  assert.doesNotMatch(source, /hover:bg-white/);
});
