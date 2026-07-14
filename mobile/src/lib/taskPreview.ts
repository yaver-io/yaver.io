// taskPreview — pure derivations over a task's streaming output buffer.
//
// Extracted from app/(tabs)/tasks.tsx so they can be unit-tested in Node
// (a .tsx screen drags in react-native and can't be imported by the test
// runner) — and because these are exactly the functions that froze the
// Tasks screen when they went unbounded.
//
// THE INVARIANT THIS MODULE EXISTS TO PROTECT:
//   A task's `output` is a streaming buffer of up to MAX_OUTPUT_LINES_PER_TASK
//   (8000) lines. Anything derived from it on a render path MUST be bounded —
//   it must touch a fixed-size slice, never the whole buffer. It used to run
//   all 8000 lines through 12 chained regexes plus a per-line ANSI pass on
//   every render of every card, purely to read the LAST line. With output
//   streaming, the list re-renders per chunk, so this pegged the JS thread —
//   and because React Native negotiates touch responders in JS, the entire
//   screen stopped responding to taps and scrolling while tasks were running.
//   taskPreview.test.mts holds the line with an explicit budget.

// Keep every task's output bounded in JS heap: a long-running agent can emit
// 100k+ lines. Cap at 8000 and keep the tail; the head is rarely useful by
// then. The agent retains the full transcript on disk.
export const MAX_OUTPUT_LINES_PER_TASK = 8000;
export const OUTPUT_TRUNCATED_MARKER =
  "[… earlier output truncated to keep memory bounded — agent has full log …]";

// The preview is a single line capped at 120 chars, so it only ever needs the
// tail of the output / the head of a result. These bounds are what make the
// derivation O(1) in the size of the buffer instead of O(n).
export const PREVIEW_SCAN_LINES = 200;
export const PREVIEW_SCAN_CHARS = 4000;

/** Minimal structural shape — avoids importing the RN-bound Task type. */
export type TaskPreviewInput = {
  status: string;
  resultText?: string;
  output: string[];
};

export function capOutput(lines: string[]): string[] {
  if (lines.length <= MAX_OUTPUT_LINES_PER_TASK) return lines;
  const tail = lines.slice(-(MAX_OUTPUT_LINES_PER_TASK - 1));
  return [OUTPUT_TRUNCATED_MARKER, ...tail];
}

const ANSI_PATTERN =
  // eslint-disable-next-line no-control-regex
  /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[()][0AB]|\x1b[=>NOM78cDEHM]|\x07/g;

export function stripAnsi(s: string): string {
  if (!s) return s;
  // Some agents emit the terminal-detection CSI without the leading ESC
  // (raw `[1m...[0m` after the agent's own pre-processing strips ESC
  // from JSON-escaped strings). Catch those bare CSI runs too — only
  // when they look exactly like an SGR (digits + 'm') so we don't eat
  // legitimate `[1ms ago]` style brackets.
  return s
    .replace(ANSI_PATTERN, "")
    .replace(/\[\d+(?:;\d+)*m/g, "");
}

export function stripMarkdownForPreview(text: string): string {
  return stripAnsi(text)
    .replace(/```[\s\S]*?```/g, " code block ")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, "$1")
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/^\s*>\s?/gm, "")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/\*([^*]+)\*/g, "$1")
    .replace(/_/g, "")
    .replace(/\r/g, "")
    .replace(/[ \t]+\n/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

export function collapseAdjacentDuplicateLines(text: string): string {
  const out: string[] = [];
  let lastNonEmpty = "";
  for (const line of String(text || "").replace(/\r/g, "").split("\n")) {
    const normalized = stripAnsi(line).trim();
    if (normalized && normalized === lastNonEmpty) continue;
    out.push(line);
    if (normalized) lastNonEmpty = normalized;
  }
  return out.join("\n").replace(/\n{3,}/g, "\n\n").trim();
}

/**
 * One-line preview for a task card. BOUNDED BY CONTRACT: reads at most
 * PREVIEW_SCAN_LINES from the tail of `output` (running) or
 * PREVIEW_SCAN_CHARS from the head of `resultText`. Never scan the whole
 * buffer here — see the note at the top of this file.
 */
export function buildTaskPreviewText(task: TaskPreviewInput): string | null {
  if (task.resultText) {
    // Head slice: we want the FIRST 120 chars, and adjacent-duplicate
    // collapsing only ever compares neighbouring lines.
    return collapseAdjacentDuplicateLines(
      stripMarkdownForPreview(task.resultText.slice(0, PREVIEW_SCAN_CHARS)),
    ).slice(0, 120);
  }
  if (task.status === "running" || task.status === "queued") {
    const tail = task.output.length > PREVIEW_SCAN_LINES
      ? task.output.slice(-PREVIEW_SCAN_LINES)
      : task.output;
    const live = collapseAdjacentDuplicateLines(stripMarkdownForPreview(tail.join("\n")))
      .split("\n").map((line) => line.trim()).filter(Boolean);
    if (live.length > 0) return live[live.length - 1].slice(0, 120);
    return "Working...";
  }
  return null;
}
