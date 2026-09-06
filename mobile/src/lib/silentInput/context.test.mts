import test from "node:test";
import assert from "node:assert/strict";
import { silentInputContextTerms } from "./context.ts";
import { DEFAULT_SILENT_INPUT_CONFIG } from "./types.ts";

test("silent input vocabulary is dynamic, bounded, and de-duplicated", () => {
  const terms = silentInputContextTerms("SFMG", ["pytest", "Custom command"]);
  assert.ok(terms.includes("run tests"));
  assert.ok(terms.includes("sfmg"));
  assert.ok(terms.includes("custom command"));
  assert.equal(terms.filter((term) => term === "pytest").length, 1);
  assert.ok(terms.length <= 80);
});

test("experimental silent input defaults to explicit-send local privacy", () => {
  assert.equal(DEFAULT_SILENT_INPUT_CONFIG.enabled, true);
  assert.equal(DEFAULT_SILENT_INPUT_CONFIG.backend, "user-machine");
  assert.equal(DEFAULT_SILENT_INPUT_CONFIG.autoSend, false);
  assert.equal(DEFAULT_SILENT_INPUT_CONFIG.sendFullVideo, false);
  assert.equal(DEFAULT_SILENT_INPUT_CONFIG.mouthCropOnly, true);
});
