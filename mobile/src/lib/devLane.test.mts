// devLane.test.mts — locks the two dogfood reload-lane bugs shut.
// Run: npx tsx src/lib/devLane.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  browserLaneStartBody,
  hermesLaneStartBody,
  isWebServedStatus,
  mustUseNativePreview,
} from "./devLane.ts";

// Browser Reload must serve the web target, not a Hermes build. (The old
// devStartInstruction() helper + its tests were removed — rendering is never a
// coding task, so there is no /dev/start instruction string to test.)

test("browser lane body routes to the web target (caller web-ui, platform web)", () => {
  const b = browserLaneStartBody();
  assert.equal(b.caller, "web-ui");
  assert.equal(b.platform, "web");
});

test("hermes lane body stays caller mobile", () => {
  assert.equal(hermesLaneStartBody().caller, "mobile");
});

test("a web-served status is browser mode, never native — even for expo/RN", () => {
  assert.equal(isWebServedStatus({ devMode: "web" }), true);
  assert.equal(isWebServedStatus({ platform: "web" }), true);
  assert.equal(isWebServedStatus({ devMode: "ios" }), false);
  // The exact regression: expo project served as web → NOT native. The agent
  // signals this with devMode="web" (platform stays empty — the bug was keying
  // on platform).
  assert.equal(mustUseNativePreview({ framework: "expo", devMode: "web" }), false);
  assert.equal(mustUseNativePreview({ framework: "expo", platform: "web" }), false);
  assert.equal(mustUseNativePreview({ framework: "react-native", devMode: "web" }), false);
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
  assert.equal(mustUseNativePreview({ framework: "expo", devMode: "web" }), false);
});
