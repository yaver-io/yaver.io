// devLane.test.mts — locks the two dogfood reload-lane bugs shut.
// Run: npx tsx src/lib/devLane.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  devStartInstruction,
  browserLaneStartBody,
  hermesLaneStartBody,
  isWebServedStatus,
  mustUseNativePreview,
} from "./devLane.ts";

// ── Bug 1: Flutter must NOT be told to start Metro / push a Hermes bundle ──

test("Flutter dev-start says Flutter web server, never Metro/Hermes", () => {
  const s = devStartInstruction("flutter", "/p/e-mobile");
  assert.match(s, /Flutter web dev server/);
  // The bug was Flutter being INSTRUCTED to start Metro / push a Hermes bundle.
  // Saying "Flutter has no Metro and no Hermes" is correct, so assert on the
  // harmful directives, not the mere words.
  assert.ok(!/start Metro/.test(s), `Flutter told to start Metro: ${s}`);
  assert.ok(!/Hermes push/.test(s), `Flutter told to push a Hermes bundle: ${s}`);
});

test("expo/react-native keep the Metro + Hermes instruction", () => {
  for (const fw of ["expo", "react-native"]) {
    const s = devStartInstruction(fw, "/p/app");
    assert.match(s, /Metro/);
    assert.match(s, /Hermes push/);
  }
});

test("native swift/kotlin get the WebRTC-lane instruction, no Metro/Hermes", () => {
  for (const fw of ["swift", "kotlin"]) {
    const s = devStartInstruction(fw, "/p/native");
    assert.match(s, /WebRTC lane/);
    assert.ok(!/start Metro/.test(s));
    assert.ok(!/Hermes push/.test(s));
  }
});

test("unknown/web stack gets a generic web dev-server instruction", () => {
  const s = devStartInstruction("nextjs", "/p/web");
  assert.match(s, /web dev server/);
  assert.ok(!/Metro/.test(s));
});

// ── Bug 2: Browser Reload must serve the web target, not a Hermes build ──

test("browser lane body routes to the web target (caller web-ui, platform web)", () => {
  const b = browserLaneStartBody();
  assert.equal(b.caller, "web-ui");
  assert.equal(b.platform, "web");
});

test("hermes lane body stays caller mobile", () => {
  assert.equal(hermesLaneStartBody().caller, "mobile");
});

test("a web-served status is browser mode, never native — even for expo/RN", () => {
  assert.equal(isWebServedStatus("web"), true);
  // The exact regression: expo project, but served as web → NOT native.
  assert.equal(mustUseNativePreview({ framework: "expo", platform: "web" }), false);
  assert.equal(mustUseNativePreview({ framework: "react-native", platform: "web" }), false);
});

test("expo/RN WITHOUT web mode still takes the native Hermes path", () => {
  assert.equal(mustUseNativePreview({ framework: "expo", platform: "ios" }), true);
  assert.equal(mustUseNativePreview({ framework: "react-native" }), true);
  assert.equal(mustUseNativePreview({ framework: "expo", building: true }), true);
});

test("Flutter is never native-preview (renders in the browser lane)", () => {
  assert.equal(mustUseNativePreview({ framework: "flutter", platform: "ios" }), false);
  assert.equal(mustUseNativePreview({ framework: "flutter", platform: "web" }), false);
});

test("dev-client mode forces native regardless of framework", () => {
  assert.equal(mustUseNativePreview({ framework: "expo", devMode: "dev-client" }), true);
  // ...unless it is explicitly web-served.
  assert.equal(mustUseNativePreview({ framework: "expo", devMode: "dev-client", platform: "web" }), false);
});
