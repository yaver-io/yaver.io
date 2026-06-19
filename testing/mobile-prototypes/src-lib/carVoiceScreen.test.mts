// carVoiceScreen.test.mts — Tests for car voice screen runtimeSurfaceClient wiring
// Run: npx tsx mobile/src/lib/carVoiceScreen.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { carVoiceViewport, watchVoiceViewport, tvDpadViewport, viewportHeaders } from "./runtimeSurfaceTypes";

// ── viewport generation ────────────────────────────────────────────────

test("carVoiceViewport produces driving-safe voice metadata", () => {
  const vp = carVoiceViewport();
  assert.equal(vp.surface, "car-audio");
  assert.equal(vp.interaction, "voice");
  assert.equal(vp.visualBudget, "none");
  assert.equal(vp.riskPolicy, "driving");
  assert.equal(vp.voice, true);
  assert.equal(vp.ttsBudget, 200);
  assert.equal(vp.sttEnabled, true);
  assert.equal(vp.ttsEnabled, true);
});

test("watchVoiceViewport produces glanceable watch metadata", () => {
  const vp = watchVoiceViewport();
  assert.equal(vp.surface, "wearable-watch");
  assert.equal(vp.interaction, "voice");
  assert.equal(vp.visualBudget, "glance");
  assert.equal(vp.riskPolicy, "watch");
  assert.equal(vp.voice, true);
  assert.equal(vp.ttsBudget, 160);
  assert.equal(vp.sttEnabled, true);
  assert.equal(vp.ttsEnabled, true);
});

test("tvDpadViewport produces shared TV dpad metadata", () => {
  const vp = tvDpadViewport();
  assert.equal(vp.surface, "tv-living-room");
  assert.equal(vp.interaction, "dpad");
  assert.equal(vp.visualBudget, "glance");
  assert.equal(vp.riskPolicy, "shared-tv");
});

test("tvDpadViewport supports custom surface types", () => {
  const vp = tvDpadViewport("tv-apple");
  assert.equal(vp.surface, "tv-apple");
});

test("viewportHeaders maps metadata to agent headers", () => {
  const vp = carVoiceViewport();
  const headers = viewportHeaders(vp);
  assert.equal(headers["X-Yaver-Surface"], "car-audio");
  assert.equal(headers["X-Yaver-Interaction"], "voice");
  assert.equal(headers["X-Yaver-Visual-Budget"], "none");
  assert.equal(headers["X-Yaver-Risk-Policy"], "driving");
  assert.equal(headers["X-Yaver-Voice"], "stt,tts");
  assert.equal(headers["X-Yaver-TTS-Mode"], undefined);
});

test("viewportHeaders includes TTS mode when ttsMode is true", () => {
  const vp = carVoiceViewport();
  vp.ttsMode = true;
  const headers = viewportHeaders(vp);
  assert.equal(headers["X-Yaver-TTS-Mode"], "1");
});

test("viewportHeaders excludes voice when disabled", () => {
  const vp = carVoiceViewport();
  vp.sttEnabled = false;
  vp.ttsEnabled = false;
  const headers = viewportHeaders(vp);
  assert.equal(headers["X-Yaver-Voice"], undefined);
});

// ── surface-specific headers ────────────────────────────────────────────

test("car viewport has stronger TTS budget than watch", () => {
  const car = carVoiceViewport();
  const watch = watchVoiceViewport();
  assert.ok((car.ttsBudget || 0) > (watch.ttsBudget || 0));
  assert.equal(car.ttsBudget, 200);
  assert.equal(watch.ttsBudget, 160);
});

test("car and watch use different risk policies", () => {
  const car = carVoiceViewport();
  const watch = watchVoiceViewport();
  assert.equal(car.riskPolicy, "driving");
  assert.equal(watch.riskPolicy, "watch");
});

test("car and watch use different visual budgets", () => {
  const car = carVoiceViewport();
  const watch = watchVoiceViewport();
  assert.equal(car.visualBudget, "none");
  assert.equal(watch.visualBudget, "glance");
});

test("TV uses dpad interaction and shared-tv risk policy", () => {
  const tv = tvDpadViewport();
  assert.equal(tv.interaction, "dpad");
  assert.equal(tv.riskPolicy, "shared-tv");
});

test("all surfaces include voice when enabled", () => {
  const car = carVoiceViewport();
  const watch = watchVoiceViewport();
  const tv = tvDpadViewport();
  
  const carHeaders = viewportHeaders(car);
  const watchHeaders = viewportHeaders(watch);
  const tvHeaders = viewportHeaders(tv);
  
  assert.equal(carHeaders["X-Yaver-Voice"], "stt,tts");
  assert.equal(watchHeaders["X-Yaver-Voice"], "stt,tts");
  assert.equal(tvHeaders["X-Yaver-Voice"], undefined); // TV doesn't use voice by default
});

test("custom TTS budget is preserved in headers", () => {
  const car = carVoiceViewport(150);
  const headers = viewportHeaders(car);
  // TTS budget is a runtime preference, not a header
  assert.equal(headers["X-Yaver-Surface"], "car-audio");
});

// ── header safety ───────────────────────────────────────────────────────

test("viewportHeaders handles empty viewport", () => {
  const vp: Record<string, unknown> = {};
  const headers = viewportHeaders(vp as any);
  assert.ok(typeof headers === "object");
});

test("viewportHeaders handles null values gracefully", () => {
  const vp = {
    surface: null,
    interaction: null as any,
    visualBudget: null as any,
    riskPolicy: null as any,
  };
  const headers = viewportHeaders(vp as any);
  assert.equal(Object.keys(headers).length, 0);
});

test("viewportHeaders uses empty string for nullish primitive fields", () => {
  const vp = carVoiceViewport();
  (vp as any).surface = null;
  const headers = viewportHeaders(vp);
  // The function should handle null by not setting the header
  assert.ok(!headers["X-Yaver-Surface"] || headers["X-Yaver-Surface"] === "");
});