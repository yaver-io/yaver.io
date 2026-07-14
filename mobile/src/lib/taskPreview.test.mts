// taskPreview.test.mts — guards the Tasks-screen freeze.
// Run: npx tsx src/lib/taskPreview.test.mts
//
// History: buildTaskPreviewText used to run a task's ENTIRE output buffer
// (up to 8000 lines) through 12 chained regexes plus a per-line ANSI pass on
// every render of every card, just to read the last line. While output
// streamed, the list re-rendered per chunk and the JS thread pegged — and
// since React Native negotiates touch responders in JS, the whole screen
// stopped responding to taps and scrolling. These tests fail if that comes
// back: they assert correctness AND that the work stays bounded.

import test from "node:test";
import assert from "node:assert/strict";

import {
  buildTaskPreviewText,
  capOutput,
  collapseAdjacentDuplicateLines,
  stripAnsi,
  MAX_OUTPUT_LINES_PER_TASK,
  OUTPUT_TRUNCATED_MARKER,
  PREVIEW_SCAN_LINES,
  type TaskPreviewInput,
} from "./taskPreview.ts";

const ESC = String.fromCharCode(27);

/** A realistic streaming buffer: ANSI colour, markdown, code fences. */
function streamingOutput(lines: number, lastLine = "final line: done"): string[] {
  const out: string[] = [];
  for (let i = 0; i < lines; i++) {
    out.push(
      i % 7 === 0 ? `${ESC}[1m**$ npm run build**${ESC}[0m`
      : i % 11 === 0 ? "```ts"
      : `  compiling module_${i} \`src/x${i}.ts\` [ok]`,
    );
  }
  out.push(lastLine);
  return out;
}

const running = (output: string[]): TaskPreviewInput => ({ status: "running", output });

// ── correctness ──────────────────────────────────────────────────────

test("preview: returns the last non-empty line of a running task", () => {
  assert.equal(buildTaskPreviewText(running(["one", "two", "three"])), "three");
});

test("preview: strips ANSI and markdown from the live line", () => {
  const line = `${ESC}[32m**Compiling** \`src/app.ts\`${ESC}[0m`;
  assert.equal(buildTaskPreviewText(running([line])), "Compiling src/app.ts");
});

test("preview: a running task with no output yet says Working...", () => {
  assert.equal(buildTaskPreviewText(running([])), "Working...");
});

test("preview: trailing blank lines fall back to the last real line", () => {
  assert.equal(buildTaskPreviewText(running(["real line", "", "   ", ""])), "real line");
});

test("preview: resultText wins over output, truncated to 120 chars", () => {
  const long = "x".repeat(500);
  const preview = buildTaskPreviewText({ status: "completed", resultText: long, output: [] });
  assert.equal(preview?.length, 120);
});

test("preview: completed task without resultText has no preview", () => {
  assert.equal(buildTaskPreviewText({ status: "completed", output: ["noise"] }), null);
});

test("preview: adjacent duplicate lines collapse (the 'Hi. What do you need done?' twice bug)", () => {
  const dupes = "Hi. What do you need done?\nHi. What do you need done?";
  const preview = buildTaskPreviewText({ status: "review", resultText: dupes, output: [] });
  assert.equal(preview, "Hi. What do you need done?");
});

// ── the bound: this is the actual regression guard ───────────────────

test("preview: a full 8000-line buffer yields the SAME line as a short one", () => {
  // Bounding the scan must not change the answer — the last line is the last
  // line whether the buffer is 3 lines or at the cap.
  const big = buildTaskPreviewText(running(streamingOutput(MAX_OUTPUT_LINES_PER_TASK)));
  const small = buildTaskPreviewText(running(streamingOutput(5)));
  assert.equal(big, "final line: done");
  assert.equal(small, "final line: done");
});

test("preview: cost does NOT grow with buffer size (no whole-buffer scan)", () => {
  // The regression this file exists for. If someone reintroduces a whole-buffer
  // derivation, an 8000-line buffer costs ~15x a 200-line one and this fails.
  // A bounded implementation is flat, so we allow a generous 4x for noise.
  const small = running(streamingOutput(PREVIEW_SCAN_LINES));
  const huge = running(streamingOutput(MAX_OUTPUT_LINES_PER_TASK));

  const timeOf = (task: TaskPreviewInput) => {
    for (let i = 0; i < 20; i++) buildTaskPreviewText(task); // warm the JIT
    const t0 = performance.now();
    for (let i = 0; i < 50; i++) buildTaskPreviewText(task);
    return (performance.now() - t0) / 50;
  };

  const smallMs = timeOf(small);
  const hugeMs = timeOf(huge);
  const ratio = hugeMs / Math.max(smallMs, 0.001);

  assert.ok(
    ratio < 4,
    `preview cost scales with buffer size (${hugeMs.toFixed(2)}ms at ${MAX_OUTPUT_LINES_PER_TASK} lines vs ` +
      `${smallMs.toFixed(2)}ms at ${PREVIEW_SCAN_LINES} lines, ${ratio.toFixed(1)}x). ` +
      `Something is scanning the whole output buffer again — that freezes the Tasks screen while tasks stream.`,
  );
});

test("preview: stays under a per-render frame budget at the cap", () => {
  // Node/V8 is far faster than Hermes on a phone, so keep this strict: the
  // card renders many times per second while output streams.
  const task = running(streamingOutput(MAX_OUTPUT_LINES_PER_TASK));
  for (let i = 0; i < 20; i++) buildTaskPreviewText(task);
  const t0 = performance.now();
  for (let i = 0; i < 50; i++) buildTaskPreviewText(task);
  const ms = (performance.now() - t0) / 50;
  assert.ok(ms < 2, `preview took ${ms.toFixed(2)}ms/render at the output cap (budget 2ms)`);
});

// ── capOutput: the reason memo keys can't rely on output.length ──────

test("capOutput: trims from the head and keeps the tail, pinning length at the cap", () => {
  const capped = capOutput(streamingOutput(MAX_OUTPUT_LINES_PER_TASK + 500));
  assert.equal(capped.length, MAX_OUTPUT_LINES_PER_TASK);
  assert.equal(capped[0], OUTPUT_TRUNCATED_MARKER);
  assert.equal(capped[capped.length - 1], "final line: done");
});

test("capOutput: at the cap, appending changes the LAST line but NOT the length", () => {
  // This is why the render memos key on the last line, not just output.length:
  // a long-running task pins length at the cap while still streaming, so a
  // length-only key would freeze the UI on exactly the tasks that stream most.
  const a = capOutput(streamingOutput(MAX_OUTPUT_LINES_PER_TASK));
  const b = capOutput([...a, "newest line"]);
  assert.equal(a.length, b.length);
  assert.notEqual(a[a.length - 1], b[b.length - 1]);
  assert.equal(buildTaskPreviewText(running(b)), "newest line");
});

// ── helpers ──────────────────────────────────────────────────────────

test("stripAnsi: removes escape sequences and bare SGR runs", () => {
  assert.equal(stripAnsi(`${ESC}[31mred${ESC}[0m`), "red");
  assert.equal(stripAnsi("[1mbold[0m"), "bold");
});

test("stripAnsi: KNOWN LIMITATION — the bare-SGR fallback also eats '[1m' inside '[1ms ago]'", () => {
  // taskPreview.ts claims this case is safe ("so we don't eat legitimate
  // `[1ms ago]` style brackets") — it is NOT. /\[\d+(?:;\d+)*m/ matches the
  // "[1m" of "[1ms ago]". Pinned here as the current, real behaviour so the
  // claim can't quietly drift further; fixing it means disambiguating a bare
  // SGR run from prose (e.g. only strip when the line also carries a reset),
  // which is a separate change from the freeze fix.
  assert.equal(stripAnsi("[1ms ago] done"), "s ago] done");
});

test("collapseAdjacentDuplicateLines: only collapses ADJACENT repeats", () => {
  assert.equal(collapseAdjacentDuplicateLines("a\na\nb\na"), "a\nb\na");
});
