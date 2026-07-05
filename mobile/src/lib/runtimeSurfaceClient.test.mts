import test from "node:test";
import assert from "node:assert/strict";

import {
  carVoiceViewport,
  headsetViewport,
  normalizeOpsInitial,
  tvDpadViewport,
  viewportHeaders,
  watchVoiceViewport,
} from "./runtimeSurfaceTypes.ts";

test("carVoiceViewport produces driving-safe voice metadata", () => {
  const vp = carVoiceViewport(180);
  assert.equal(vp.surface, "car-audio");
  assert.equal(vp.interaction, "voice");
  assert.equal(vp.visualBudget, "none");
  assert.equal(vp.riskPolicy, "driving");
  assert.equal(vp.ttsBudget, 180);
  assert.equal(vp.voice, true);
});

test("watchVoiceViewport produces glanceable watch metadata", () => {
  const vp = watchVoiceViewport();
  assert.equal(vp.surface, "wearable-watch");
  assert.equal(vp.visualBudget, "glance");
  assert.equal(vp.riskPolicy, "watch");
  assert.equal(vp.ttsBudget, 160);
});

test("tvDpadViewport produces shared TV dpad metadata", () => {
  const vp = tvDpadViewport("tv-apple");
  assert.equal(vp.surface, "tv-apple");
  assert.equal(vp.interaction, "dpad");
  assert.equal(vp.riskPolicy, "shared-tv");
});

test("headsetViewport produces spatial headset metadata", () => {
  const vp = headsetViewport("headset-android-xr");
  assert.equal(vp.surface, "headset-android-xr");
  assert.equal(vp.interaction, "touch");
  assert.equal(vp.visualBudget, "panel");
  assert.equal(vp.riskPolicy, "spatial");
});

test("viewportHeaders maps metadata to agent headers", () => {
  const headers = viewportHeaders({
    surface: "car-audio",
    interaction: "voice",
    visualBudget: "none",
    riskPolicy: "driving",
    voice: true,
    ttsEnabled: true,
    ttsMode: true,
  });
  assert.equal(headers["X-Yaver-Surface"], "car-audio");
  assert.equal(headers["X-Yaver-Interaction"], "voice");
  assert.equal(headers["X-Yaver-Visual-Budget"], "none");
  assert.equal(headers["X-Yaver-Risk-Policy"], "driving");
  assert.equal(headers["X-Yaver-Voice"], "stt,tts");
  assert.equal(headers["X-Yaver-TTS-Mode"], "1");
});

test("normalizeOpsInitial unwraps initial and throws typed failures", () => {
  assert.deepEqual(normalizeOpsInitial({ ok: true, initial: { value: 1 } }), { value: 1 });
  assert.deepEqual(normalizeOpsInitial({ ok: true, value: 2 } as any), { ok: true, value: 2 });
  assert.throws(() => normalizeOpsInitial({ ok: false, code: "bad_payload", error: "nope" }), /nope/);
});
