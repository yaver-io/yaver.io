import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const source = (relative: string) => readFileSync(join(here, relative), "utf8");

test("every TV account screen exposes the same two choices", () => {
  const screens = [
    source("../../app/tv-signin.tsx"),
    source("../../../tvos/YaverTV/Views/SignInView.swift"),
    source("../../../androidtv/app/src/main/kotlin/io/yaver/tv/ui/SignInScreen.kt"),
  ];
  for (const screen of screens) {
    assert.match(screen, /Email.{0,20}password/is);
    assert.match(screen, /Scan.{0,40}(Yaver app|QR|phone)/is);
    assert.doesNotMatch(screen, /Sign in with (Apple|Google|Microsoft|GitHub|GitLab)/i);
  }
});

test("tvOS cannot silently re-expose headset-only native provider implementations", () => {
  for (const file of ["AppleSignIn.swift", "OAuthSignIn.swift"]) {
    const implementation = source(`../../../tvos/YaverTV/${file}`);
    assert.match(implementation, /#if\s+!os\(tvOS\)/, `${file} is not compile-time excluded from tvOS`);
  }
  const signIn = source("../../../tvos/YaverTV/Views/SignInView.swift");
  assert.doesNotMatch(signIn, /AppleNativeAuth|OAuthSignIn|OAuthProvider/);
});

test("TV sign-in stays pinned above the scrollable Settings sections", () => {
  const settings = source("../../app/(tabs)/settings.tsx");
  const scanner = settings.indexOf("Sign in a TV");
  const scroll = settings.indexOf("<KeyboardAvoidingView");
  assert.ok(scanner >= 0 && scroll >= 0 && scanner < scroll);
  assert.match(settings, /params: \{ scan: "1" \}/);
});

test("Settings uses an Apple-style category level with a dedicated Coding Agent destination", () => {
  const settings = source("../../app/(tabs)/settings.tsx");
  assert.match(settings, /type SettingsPane[\s\S]*"coding-agent"[\s\S]*"advanced"/);
  assert.match(settings, /title: "Coding Agent", subtitle: "Runner, model, sign-in, and updates"/);
  assert.match(settings, /settingsPane === "coding-agent"[\s\S]*<CodingAgentsSection device=\{activeDevice\}/);
  assert.match(settings, /onBack=\{\(\) => settingsPane \? setSettingsPane\(null\) : router\.navigate/);

  const version = settings.indexOf("Yaver mobile v{APP_VERSION}");
  const tv = settings.indexOf("Sign in a TV");
  const categories = settings.indexOf('accessibilityLabel="Settings categories"');
  assert.ok(version >= 0 && tv > version && categories > tv, "version and TV sign-in must stay above categories");
});

test("the approval success action cannot shrink to its four-letter label", () => {
  const approval = source("../../app/approve-device.tsx");
  assert.match(approval, /styles\.primaryBtn,\s*styles\.successBtn/);
  assert.match(approval, /successBtn:\s*\{[^}]*width:\s*"100%"[^}]*minHeight:\s*52/s);
});

test("Settings keeps dense utility rows concise and separates Docker status cards", () => {
  const settings = source("../../app/(tabs)/settings.tsx");
  for (const removedWall of [
    "permission-justification videos & prose",
    "catch bugs (red box / crash / ANR)",
    "Agent leads each reply with a short spoken-style summary",
  ]) {
    assert.ok(!settings.includes(removedWall), `verbose helper copy returned: ${removedWall}`);
  }
  const imageSection = settings.slice(
    settings.indexOf("{/* Image status + build */}"),
    settings.indexOf("{/* Containerize Host toggle */}"),
  );
  assert.match(imageSection, /<View style=\{\{ height: 8 \}\} \/>/);
});

test("native and generated iOS camera disclosures name TV QR sign-in", () => {
  const expected =
    "Yaver uses your camera for private Silent Input lip reading, machine pairing and TV sign-in QR codes, secure handoff, task photos, and apps you run for testing. Silent Input records no audio and sends only mouth crops to your selected machine.";
  const appJson = JSON.parse(source("../../app.json"));
  assert.equal(appJson.expo.ios.infoPlist.NSCameraUsageDescription, expected);

  const cameraCopies = appJson.expo.plugins
    .filter((plugin: unknown) => Array.isArray(plugin) && (plugin[0] === "expo-camera" || plugin[0] === "expo-image-picker"))
    .map((plugin: [string, { cameraPermission?: string }]) => plugin[1]?.cameraPermission);
  assert.deepEqual(cameraCopies, [expected, expected]);
  assert.match(source("../../ios/Yaver/Info.plist"), new RegExp(expected.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
});
