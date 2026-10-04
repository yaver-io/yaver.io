import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

test("all mobile pre-auth entry points share the canonical app-icon component", async () => {
  const mobile = await readFile(new URL("../../app/login.tsx", import.meta.url), "utf8");
  const splash = await readFile(new URL("../components/YaverSplash.tsx", import.meta.url), "utf8");
  const callback = await readFile(new URL("../../app/oauth-callback.tsx", import.meta.url), "utf8");
  const twoFactor = await readFile(new URL("../../app/two-factor-challenge.tsx", import.meta.url), "utf8");
  const tv = await readFile(new URL("../../app/tv-signin.tsx", import.meta.url), "utf8");
  const gate = await readFile(new URL("../../app/index.tsx", import.meta.url), "utf8");
  const icon = await readFile(new URL("../components/YaverAppIcon.tsx", import.meta.url), "utf8");
  const web = await readFile(new URL("../../../web/app/auth/page.tsx", import.meta.url), "utf8");

  assert.match(icon, /require\("\.\.\/\.\.\/assets\/icon\.png"\)/);
  for (const source of [mobile, splash, callback, twoFactor, tv, gate]) {
    assert.match(source, /YaverAppIcon/);
  }
  assert.match(web, /src="\/icon-512\.png"/);
  assert.doesNotMatch(mobile, /M150 150 L256 288/, "the generic alphabet-Y glyph returned");
});

test("native splash uses the same black app icon on white", async () => {
  const config = await readFile(new URL("../../app.json", import.meta.url), "utf8");
  const background = await readFile(new URL("../../ios/Yaver/Images.xcassets/SplashScreenBackground.colorset/Contents.json", import.meta.url), "utf8");
  assert.match(config, /"backgroundColor": "#FFFFFF"/);
  assert.match(config, /"image": "\.\/assets\/icon\.png"/);
  assert.doesNotMatch(config, /splash-logo\.png/);
  assert.match(background, /"red": "0xFF"/);
  assert.match(background, /"green": "0xFF"/);
  assert.match(background, /"blue": "0xFF"/);
});

test("email sign-in lifts its form and keeps the next control above the keyboard", async () => {
  const mobile = await readFile(new URL("../../app/login.tsx", import.meta.url), "utf8");
  assert.match(mobile, /LOGIN_FOLLOWING_CONTROL_CLEARANCE = 112/);
  assert.match(mobile, /showEmailForm && !isTablet && styles\.headerEmailMode/);
  assert.match(mobile, /scrollTo\(\{ y: 54, animated: true \}\)/);
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
