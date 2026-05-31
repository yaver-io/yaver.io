// localAgent/grammar.ts — GBNF grammar builder so the on-device model can ONLY
// emit a valid tool-call / chip JSON. PURE + RN-free (tsx-tested).
//
// llama.cpp supports GBNF grammars to constrain decoding. We generate a grammar
// that forces the model's output to be a JSON object whose `action` is one of a
// FIXED enum of allowed action ids — it literally cannot hallucinate a verb
// outside the catalog. This is the structural safety net under brain/catalog:
// even a confused tiny model can't propose `cloud.destroy` if it's not in the
// allowed set we pass here.

function quote(s: string): string {
  // GBNF string literals are double-quoted; escape backslash + quote.
  return '"' + s.replace(/\\/g, "\\\\").replace(/"/g, '\\"') + '"';
}

/**
 * Build a GBNF grammar that constrains output to:
 *   { "action": <one-of allowedActionIds>, "deviceRef"?: string, "args"?: object }
 *
 * allowedActionIds MUST be the BLOCKED-filtered list (voiceInvokableActions /
 * the chip catalog) so the grammar can't even express a forbidden action.
 */
export function buildToolCallGrammar(allowedActionIds: string[]): string {
  const ids = allowedActionIds.filter(Boolean);
  if (ids.length === 0) {
    // Degenerate: no actions allowed → force an empty object.
    return [
      "root ::= \"{}\"",
    ].join("\n");
  }
  const actionAlt = ids.map(quote).join(" | ");
  return [
    "root ::= obj",
    'obj ::= "{" ws "\\"action\\"" ws ":" ws action ( ws "," ws field )* ws "}"',
    `action ::= ${actionAlt}`,
    'field ::= devicefield | argsfield',
    'devicefield ::= "\\"deviceRef\\"" ws ":" ws string',
    'argsfield ::= "\\"args\\"" ws ":" ws object',
    'object ::= "{" ws ( pair ( ws "," ws pair )* )? ws "}"',
    'pair ::= string ws ":" ws value',
    'value ::= string | number | "true" | "false" | "null" | object',
    'string ::= "\\"" ( [^"\\\\] | "\\\\" . )* "\\""',
    "number ::= \"-\"? [0-9]+ ( \".\" [0-9]+ )?",
    'ws ::= [ \\t\\n]*',
  ].join("\n");
}

/**
 * Build a GBNF grammar for the response→chips interpreter LLM path:
 *   { "summary": string, "chips": [ { "label": string, "actionId": <enum> } ] }
 * Limits chips to the allowed action ids; an empty allowed set forces no chips.
 */
export function buildChipsGrammar(allowedActionIds: string[]): string {
  const ids = allowedActionIds.filter(Boolean);
  const actionAlt = ids.length ? ids.map(quote).join(" | ") : '"__none__"';
  return [
    "root ::= obj",
    'obj ::= "{" ws "\\"summary\\"" ws ":" ws string ws "," ws "\\"chips\\"" ws ":" ws chips ws "}"',
    'chips ::= "[" ws ( chip ( ws "," ws chip )* )? ws "]"',
    'chip ::= "{" ws "\\"label\\"" ws ":" ws string ws "," ws "\\"actionId\\"" ws ":" ws actionId ws "}"',
    `actionId ::= ${actionAlt}`,
    'string ::= "\\"" ( [^"\\\\] | "\\\\" . )* "\\""',
    'ws ::= [ \\t\\n]*',
  ].join("\n");
}

/** Safe-parse a model's JSON output; returns null on any error. */
export function parseModelJson<T = unknown>(text: string): T | null {
  if (!text) return null;
  // Models sometimes wrap JSON in prose/fences; extract the first {...} block.
  const start = text.indexOf("{");
  const end = text.lastIndexOf("}");
  if (start < 0 || end <= start) return null;
  try {
    return JSON.parse(text.slice(start, end + 1)) as T;
  } catch {
    return null;
  }
}
