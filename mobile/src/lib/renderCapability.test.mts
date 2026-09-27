import test from "node:test";
import assert from "node:assert/strict";

import { REMOTE_RENDER_REQUIRED, remoteRenderRequiredFailure } from "./renderCapability.ts";

test("missing runner is a named render capability failure with an actionable route", () => {
  const failure = remoteRenderRequiredFailure("This TV");
  assert.equal(failure.legacyCode, REMOTE_RENDER_REQUIRED);
  assert.equal(failure.code, "remoteless.dev-server.unavailable");
  assert.match(failure.message, /own capable machine or VPS/i);
  assert.equal(failure.action.route, "/devices");
  assert.equal(failure.alternativeAction?.route, "/devices");
});

test("Flutter on iPhone names the exact missing remoteless capability", () => {
  const failure = remoteRenderRequiredFailure("This iPhone", "flutter-render", "ios");
  assert.equal(failure.code, "remoteless.flutter-render.unavailable");
  assert.match(failure.message, /Flutter SDK/);
  assert.match(failure.message, /already-built Flutter web artifact/);
});
