import assert from "node:assert/strict";
import test from "node:test";

import { appendTerminalSpeechText, terminalSpeechExcerpt } from "./terminalSpeech.ts";

test("terminal speech mirror strips ANSI and control bytes", () => {
  assert.equal(appendTerminalSpeechText("", "\u001b[31mfailed\u001b[0m\r\n$ \u0003"), "failed\n$ ");
});

test("terminal speech excerpt is bounded to recent useful lines", () => {
  const text = ["old", "$", "building", "tests passed"].join("\n");
  assert.equal(terminalSpeechExcerpt(text), "old. building. tests passed");
  assert.ok(terminalSpeechExcerpt("x".repeat(2_000)).length <= 900);
});
