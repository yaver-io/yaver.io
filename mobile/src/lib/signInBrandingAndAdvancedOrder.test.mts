import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

test("all mobile pre-auth entry points share the canonical app-icon component", async () => {
  const mobile = await readFile(new URL("../../app/login.tsx", import.meta.url), "utf8");
  const email = await readFile(new URL("../../app/email-login.tsx", import.meta.url), "utf8");
  const splash = await readFile(new URL("../components/YaverSplash.tsx", import.meta.url), "utf8");
  const callback = await readFile(new URL("../../app/oauth-callback.tsx", import.meta.url), "utf8");
  const twoFactor = await readFile(new URL("../../app/two-factor-challenge.tsx", import.meta.url), "utf8");
  const tv = await readFile(new URL("../../app/tv-signin.tsx", import.meta.url), "utf8");
  const gate = await readFile(new URL("../../app/index.tsx", import.meta.url), "utf8");
  const icon = await readFile(new URL("../components/YaverAppIcon.tsx", import.meta.url), "utf8");
  const web = await readFile(new URL("../../../web/app/auth/page.tsx", import.meta.url), "utf8");

  assert.match(icon, /require\("\.\.\/\.\.\/assets\/adaptive-icon\.png"\)/);
  for (const source of [mobile, email, splash, callback, twoFactor, tv, gate]) {
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
  const email = await readFile(new URL("../../app/email-login.tsx", import.meta.url), "utf8");
  assert.match(email, /FOLLOWING_CONTROL_CLEARANCE = 112/);
  assert.match(email, /Keyboard\.addListener\("keyboardDidShow"/);
  assert.match(email, /keyboardVisible && styles\.contentKeyboard/);
  assert.match(email, /contentKeyboard:[\s\S]{0,100}justifyContent: "flex-start"/);
  assert.match(email, /scrollTo\(\{ y: 0, animated: true \}\)/);
});

test("BYO cloud is a More destination and no longer crowds Advanced settings", async () => {
  const settings = await readFile(new URL("../../app/(tabs)/settings.tsx", import.meta.url), "utf8");
  const more = await readFile(new URL("../../app/(tabs)/more.tsx", import.meta.url), "utf8");
  const cloud = await readFile(new URL("../../app/cloud.tsx", import.meta.url), "utf8");
  assert.doesNotMatch(settings, /CloudProvidersSection/);
  assert.doesNotMatch(more, /<CloudProvidersSection/);
  assert.match(more, /router\.push\("\/cloud"/);
  assert.match(cloud, /<CloudProvidersSection c=\{c\} token=\{token\} initialOpen hideHeader \/>/);
});

test("the cloud deep link always has a working route back to More", async () => {
  const cloud = await readFile(new URL("../../app/cloud.tsx", import.meta.url), "utf8");
  assert.match(cloud, /router\.canGoBack\(\)/);
  assert.match(cloud, /router\.replace\("\/\(tabs\)\/more"/);
  assert.match(cloud, /onBack=\{leaveCloud\}/);
});
