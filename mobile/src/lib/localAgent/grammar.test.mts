// grammar.test.mts — GBNF constraints + model-JSON parsing.
// Run: npx tsx src/lib/localAgent/grammar.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { buildToolCallGrammar, buildChipsGrammar, parseModelJson } from "./grammar.ts";

test("tool-call grammar pins action to the allowed enum only", () => {
  const g = buildToolCallGrammar(["status", "device.recoverAuth"]);
  assert.match(g, /action ::= "status" \| "device\.recoverAuth"/);
  assert.match(g, /root ::= obj/);
  // a forbidden id is simply not expressible
  assert.doesNotMatch(g, /cloud\.destroy/);
});

test("empty allowed set forces an empty object", () => {
  assert.match(buildToolCallGrammar([]), /root ::= "\{\}"/);
});

test("chips grammar constrains actionId enum", () => {
  const g = buildChipsGrammar(["status", "reload"]);
  assert.match(g, /actionId ::= "status" \| "reload"/);
  assert.match(g, /chips ::=/); // the grammar defines a constrained chips array
});

test("parseModelJson extracts a clean object", () => {
  const v = parseModelJson<{ action: string }>('{"action":"status"}');
  assert.equal(v?.action, "status");
});

test("parseModelJson strips prose/fences around JSON", () => {
  const v = parseModelJson<{ action: string }>('Sure!\n```json\n{"action":"reload"}\n```');
  assert.equal(v?.action, "reload");
});

test("parseModelJson returns null on garbage", () => {
  assert.equal(parseModelJson("not json at all"), null);
  assert.equal(parseModelJson(""), null);
});
