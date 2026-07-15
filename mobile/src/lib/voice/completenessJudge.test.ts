/**
 * completenessJudge.test.ts — `npx tsx src/lib/voice/completenessJudge.test.ts`.
 */
import {
  createCompletenessJudge,
  heuristicVerdict,
  parseVerdict,
  buildCompletenessGrammar,
  type ModelComplete,
} from "./completenessJudge";
import type { JudgeInput } from "./types";

let passed = 0;
let failed = 0;
function ok(cond: boolean, msg: string) {
  if (cond) passed++;
  else {
    failed++;
    console.error("  ✗ " + msg);
  }
}

function inp(transcript: string, over: Partial<JudgeInput> = {}): JudgeInput {
  return {
    transcript,
    trailingSilenceMs: 1200,
    surface: "car",
    pendingChoice: false,
    ...over,
  };
}

// ── Heuristic fast-path ──────────────────────────────────────────────────

// Trailing conjunction/filler → "still thinking", keep listening.
ok(heuristicVerdict(inp("add a login button and"))?.complete === false, "trailing 'and' → incomplete");
ok(heuristicVerdict(inp("fix the auth bug because"))?.complete === false, "trailing 'because' → incomplete");
ok(heuristicVerdict(inp("deploy to prod so"))?.complete === false, "trailing 'so' → incomplete");
ok(heuristicVerdict(inp("i want to"))?.complete === false, "trailing 'to' → incomplete");

// Terminal punctuation → complete.
ok(heuristicVerdict(inp("ship it."))?.complete === true, "period → complete");
ok(heuristicVerdict(inp("what's failing?"))?.complete === true, "question mark → complete");

// Clear imperative / question of length → complete.
ok(heuristicVerdict(inp("add a login button"))?.complete === true, "imperative → complete");
ok(heuristicVerdict(inp("run the tests"))?.complete === true, "imperative run → complete");
ok(heuristicVerdict(inp("how many tests are failing"))?.complete === true, "question word → complete");

// Menu answer is always complete.
ok(heuristicVerdict(inp("yes", { pendingChoice: true }))?.complete === true, "pendingChoice → complete");

// Genuinely ambiguous → null (defer to model).
ok(heuristicVerdict(inp("the auth handler")) === null, "ambiguous noun phrase → defer");

// ── Grammar shape ────────────────────────────────────────────────────────
{
  const g = buildCompletenessGrammar();
  ok(g.includes("complete") && g.includes("wantsAnswer") && g.includes("bool"), "grammar names both fields");
}

// ── parseVerdict ─────────────────────────────────────────────────────────
ok(parseVerdict('{"complete": true, "wantsAnswer": false}')?.complete === true, "parses well-formed");
ok(parseVerdict('garbage')?.complete === undefined || parseVerdict("garbage") === null, "rejects garbage");
{
  const v = parseVerdict('prefix {"complete": false, "wantsAnswer": true} suffix');
  ok(v?.complete === false && v?.wantsAnswer === true && v?.source === "model", "extracts embedded json, tags source=model");
}

// ── Judge: model consulted only on ambiguity ─────────────────────────────
(async () => {
  // Model should NOT be called when the heuristic is confident.
  let modelCalls = 0;
  const model: ModelComplete = async () => {
    modelCalls++;
    return { text: '{"complete": false, "wantsAnswer": false}' };
  };
  const j1 = createCompletenessJudge({ complete: model });
  const v1 = await j1.judge(inp("add a login button")); // heuristic-confident complete
  ok(v1.complete === true && v1.source === "heuristic", "confident case skips model");
  ok(modelCalls === 0, "model not called on confident heuristic");

  // Ambiguous → model IS consulted and its verdict wins.
  const v2 = await j1.judge(inp("the auth handler"));
  ok(modelCalls === 1, "model called on ambiguous case");
  ok(v2.complete === false && v2.source === "model", "model verdict used");

  // No model + ambiguous + short pause → incomplete (still thinking).
  const j2 = createCompletenessJudge({ complete: null, fallbackCompleteSilenceMs: 1800 });
  const v3 = await j2.judge(inp("the auth handler", { trailingSilenceMs: 1200 }));
  ok(v3.complete === false && v3.source === "fallback", "no-model short pause → incomplete fallback");

  // No model + ambiguous + long pause → complete.
  const v4 = await j2.judge(inp("the auth handler", { trailingSilenceMs: 2000 }));
  ok(v4.complete === true && v4.source === "fallback", "no-model long pause → complete fallback");

  // Model throws → falls back to silence heuristic, never rejects.
  const boom: ModelComplete = async () => {
    throw new Error("model oom");
  };
  const j3 = createCompletenessJudge({ complete: boom, fallbackCompleteSilenceMs: 1800 });
  const v5 = await j3.judge(inp("the auth handler", { trailingSilenceMs: 2000 }));
  ok(v5.source === "fallback" && v5.complete === true, "model throw → silence fallback");

  console.log(`\ncompletenessJudge: ${passed} passed, ${failed} failed`);
  if (failed > 0) process.exit(1);
})();
