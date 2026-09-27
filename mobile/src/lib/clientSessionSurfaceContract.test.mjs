import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const mobile = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const repo = join(mobile, "..");
const read = (...parts) => readFileSync(join(repo, ...parts), "utf8");

test("shared mobile identity distinguishes surface, platform, and device class", () => {
  const identity = read("mobile", "src", "lib", "appVersion.ts");
  for (const value of [
    "clientSurface", "platform", "deviceClass", "vision-pro", "apple-tv",
    "android-tv", "android-auto", "wear-os", "android-xr", "yaver-mobile-web",
  ]) {
    assert.match(identity, new RegExp(value));
  }
});

test("car, watch, and TV entry points override their shared mobile identity", () => {
  const senders = [
    ["mobile/app/car-voice-coding.tsx", "carplay", "android-auto"],
    ["mobile/app/tv-coding.tsx", "apple-tv", "android-tv"],
    ["mobile/src/components/WatchBridgeHost.tsx", "apple-watch", "wear-os"],
  ];
  for (const [path, ...markers] of senders) {
    const source = read(...path.split("/"));
    assert.match(source, /mobileSessionSettings/);
    for (const marker of markers) assert.match(source, new RegExp(marker));
  }
});

test("Dogfood chat, WebRTC, and car lanes avoid the TestFlight route hook crash", () => {
  for (const path of [
    "mobile/app/(tabs)/tasks.tsx",
    "mobile/app/remote-runtime.tsx",
    "mobile/app/car-voice-coding.tsx",
  ]) {
    const source = read(...path.split("/"));
    assert.match(source, /useRouteParamsCompat/);
    assert.doesNotMatch(source, /useLocalSearchParams/);
  }
});

test("standalone TV, XR, and desktop clients send identity on create and continue", () => {
  const apple = read("tvos", "YaverTV", "AgentClient.swift");
  assert.match(apple, /CFBundleShortVersionString/);
  assert.match(apple, /#if os\(visionOS\)/);
  assert.match(apple, /"renderModes": \["webrtc", "frames"\]/);
  assert.match(apple, /"inputModes": \["controller", "text", "voice"\]/);
  assert.match(apple, /"sessionSettings": clientSessionSettings\(\)/);
  assert.match(apple, /body\["sessionSettings"\] = clientSessionSettings\(\)/);

  const androidTV = read("androidtv", "app", "src", "main", "kotlin", "io", "yaver", "tv", "OpsClient.kt");
  assert.match(androidTV, /BuildConfig\.VERSION_NAME/);
  assert.match(androidTV, /\.put\("renderModes", JSONArray\(\)\)/);
  assert.equal((androidTV.match(/\.put\("sessionSettings", clientSessionSettings\(\)\)/g) ?? []).length, 2);

  for (const path of ["desktop/app/src/main/preload.js", "desktop/installer/src/preload.js"]) {
    const source = read(...path.split("/"));
    assert.match(source, /desktopSessionSettings/);
    assert.match(source, /renderModes/);
    assert.match(source, /inputModes/);
    assert.match(source, /createTask/);
    assert.match(source, /continueTask/);
  }
});
