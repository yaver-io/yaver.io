import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

test("mobile and web sign-in use the canonical app-icon asset", async () => {
  const mobile = await readFile(new URL("../../app/login.tsx", import.meta.url), "utf8");
  const web = await readFile(new URL("../../../web/app/auth/page.tsx", import.meta.url), "utf8");

  assert.match(mobile, /source=\{require\("\.\.\/assets\/icon\.png"\)\}/);
  assert.match(web, /src="\/icon-512\.png"/);
  assert.doesNotMatch(mobile, /M150 150 L256 288/, "the generic alphabet-Y glyph returned");
});

test("BYO cloud is the first section in Advanced settings", async () => {
  const settings = await readFile(new URL("../../app/(tabs)/settings.tsx", import.meta.url), "utf8");
  const advanced = settings.indexOf('{settingsPane === "advanced"');
  const byo = settings.indexOf("<CloudProvidersSection", advanced);
  const testApp = settings.indexOf("{/* Test App */}", advanced);
  const password = settings.indexOf("{/* Change Password", advanced);

  assert.ok(advanced >= 0 && byo > advanced, "Advanced must render BYO cloud");
  assert.ok(byo < testApp, "BYO cloud must be above Advanced diagnostics");
  assert.ok(byo < password, "BYO cloud must be above Change Password");
  assert.equal(settings.indexOf("<CloudProvidersSection", byo + 1), -1, "BYO cloud must render exactly once");
});
