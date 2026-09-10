// Regression guard for the iOS Follow Up keyboard overlap seen on 2026-09-10.
// Run: node --experimental-strip-types --test src/lib/followUpKeyboardLayout.test.mts

import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const mobileRoot = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const tasks = readFileSync(join(mobileRoot, "app/(tabs)/tasks.tsx"), "utf8");

const expandedStart = tasks.indexOf("{followUpExpanded ? (");
assert.notEqual(expandedStart, -1, "expanded Follow Up branch not found");
const compactStart = tasks.indexOf("s.chatInputBar", expandedStart);
assert.notEqual(compactStart, -1, "compact Follow Up branch not found");
const expanded = tasks.slice(expandedStart, compactStart);

test("Follow Up is a keyboard-safe modal sheet instead of a chat flex child", () => {
  assert.match(expanded, /style=\{s\.followUpModalOverlay\}/);
  assert.match(
    tasks,
    /followUpModalOverlay:\s*\{[\s\S]{0,180}\.\.\.StyleSheet\.absoluteFillObject[\s\S]{0,180}justifyContent:\s*"flex-end"/,
    "the sheet must overlay the chat and anchor to the KeyboardAvoidingView's resized bottom edge",
  );
  assert.match(expanded, /maxHeight:\s*"92%"/);
  assert.match(expanded, /contentContainerStyle=\{\{ paddingBottom: Math\.max\(insets\.bottom, 16\) \}\}/);
  assert.doesNotMatch(expanded, /followUpComposerMaxHeight/,
    "a static window-height cap ignores the keyboard-sized viewport");
});

test("Follow Up keeps the New Task composer controls above the keyboard", () => {
  assert.match(expanded, /testID="close-followup"/);
  assert.match(expanded, /s\.composerInput/);
  assert.match(expanded, /s\.composerFooter/);
  assert.match(expanded, /s\.sendButtonLarge/);
  assert.match(expanded, /testID="followup-send"/);
});
