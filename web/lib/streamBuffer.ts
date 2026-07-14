// streamBuffer — bounds for live agent-output accumulators on the web
// dashboard.
//
// Mobile caps every task's output at 8000 lines in state (see
// mobile/src/lib/taskPreview.ts). Web had no cap on ANY of its streaming
// accumulators — `streamedOutput`, `graphNodeOutput`, and the trailing
// assistant chat message each grew for the life of the session, and every
// SSE chunk re-ran stripAnsi + a full react-markdown parse over the whole
// transcript in a render body. That is the same defect that froze the mobile
// Tasks screen, without even an 8000-line ceiling to stop it.
//
// Rule: an accumulator fed by a stream must be capped WHERE IT IS WRITTEN,
// never merely where it is read.

export const MAX_STREAM_LINES = 8000;
export const STREAM_TRUNCATED_MARKER =
  "[… earlier output truncated to keep the browser responsive — agent has full log …]";

/**
 * Cap an accumulated output string to the last `maxLines` lines, keeping the
 * tail (the head is rarely useful by line 8000) and marking the truncation.
 */
export function capStreamText(text: string, maxLines: number = MAX_STREAM_LINES): string {
  if (!text) return text;
  // Fast path: counting newlines beats splitting a multi-MB string on every
  // chunk, and the overwhelming majority of chunks land under the cap.
  let newlines = 0;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10 /* \n */) newlines++;
  }
  if (newlines < maxLines) return text;

  const lines = text.split("\n");
  const tail = lines.slice(-(maxLines - 1));
  return [STREAM_TRUNCATED_MARKER, ...tail].join("\n");
}
