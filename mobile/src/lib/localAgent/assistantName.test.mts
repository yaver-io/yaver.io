// assistantName.test.mts — spoken wake-name logic, mirror of the Go tests
// in desktop/agent/voice_assistant_name_test.go. Keep the two in sync so a
// rename behaves identically on phone and Mac.
// Run: npx tsx src/lib/localAgent/assistantName.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  DEFAULT_ASSISTANT_NAME,
  effectiveAssistantName,
  assistantWakeWords,
  stripWakeWord,
  assistantNameWarning,
} from "./assistantName.ts";

test("effectiveAssistantName: empty/blank → default, else lowercased+trimmed", () => {
  assert.equal(effectiveAssistantName(undefined), "yaver");
  assert.equal(effectiveAssistantName(""), "yaver");
  assert.equal(effectiveAssistantName("   "), "yaver");
  assert.equal(effectiveAssistantName(" Sam "), "sam");
  assert.equal(DEFAULT_ASSISTANT_NAME, "yaver");
});

test("assistantWakeWords matches the Go ordering", () => {
  assert.deepEqual(assistantWakeWords(""), ["hey yaver", "ok yaver", "okay yaver", "yaver", "please"]);
  assert.deepEqual(assistantWakeWords("sam"), ["hey sam", "ok sam", "okay sam", "sam", "please"]);
});

test("stripWakeWord: renamed assistant strips its own name, not the old one", () => {
  assert.equal(stripWakeWord("hey sam, status", "sam"), "status");
  assert.equal(stripWakeWord("sam deploy web", "sam"), "deploy web");
  assert.equal(stripWakeWord("okay sam cloud status", "sam"), "cloud status");
  assert.equal(stripWakeWord("sam", "sam"), "");
  assert.equal(stripWakeWord("status", "sam"), "status"); // bare command still works
  assert.equal(stripWakeWord("please status", "sam"), "status"); // universal filler
  assert.equal(stripWakeWord("hey yaver, status", "sam"), "hey yaver, status"); // old name not a wake word
});

test("assistantNameWarning: distinctive names ok, short/common warn", () => {
  for (const ok of ["sam", "feyi", "kole", "jarvis", "", "yaver"]) {
    assert.equal(assistantNameWarning(ok), "", `expected no warning for "${ok}"`);
  }
  for (const bad of ["jo", "x", "yes", "okay", "go"]) {
    assert.notEqual(assistantNameWarning(bad), "", `expected a warning for "${bad}"`);
  }
});
