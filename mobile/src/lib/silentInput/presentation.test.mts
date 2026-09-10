import test from "node:test";
import assert from "node:assert/strict";
import { silentInputSetupPresentation } from "./presentation.ts";

test("lip reading names the enable step before camera testing", () => {
  const state = silentInputSetupPresentation({
    enabled: false,
    platform: "ios",
    targetDeviceId: "box-1",
    probing: false,
  });

  assert.equal(state.canTest, false);
  assert.equal(state.status, "Lip reading is off");
  assert.match(state.guidance, /Turn it on/);
});

test("lip reading exposes the camera test only after the selected machine proves readiness", () => {
  const state = silentInputSetupPresentation({
    enabled: true,
    platform: "ios",
    targetDeviceId: "box-1",
    probing: false,
    capabilityAvailable: true,
  });

  assert.equal(state.canTest, true);
  assert.equal(state.status, "Ready to test");
  assert.match(state.guidance, /Test with front camera/);
});

test("lip reading explains native iPhone and connected-machine requirements", () => {
  const browser = silentInputSetupPresentation({
    enabled: true,
    platform: "web",
    targetDeviceId: "box-1",
    probing: false,
  });
  const noMachine = silentInputSetupPresentation({
    enabled: true,
    platform: "ios",
    probing: false,
  });

  assert.equal(browser.canTest, false);
  assert.match(browser.status, /iPhone/);
  assert.equal(noMachine.canTest, false);
  assert.match(noMachine.status, /Connect a Yaver machine/);
});
