/**
 * endpointer.test.ts — run with `npx tsx src/lib/voice/endpointer.test.ts`.
 * No RN, no jest — a tiny assert harness like the other car-surface libs use.
 */
import {
  UtteranceEndpointer,
  DEFAULT_ENDPOINT_CONFIG,
  type EndpointDecision,
} from "./endpointer";

let passed = 0;
let failed = 0;
function ok(cond: boolean, msg: string) {
  if (cond) {
    passed++;
  } else {
    failed++;
    console.error("  ✗ " + msg);
  }
}
function eq(a: unknown, b: unknown, msg: string) {
  ok(JSON.stringify(a) === JSON.stringify(b), `${msg} (got ${JSON.stringify(a)})`);
}

// Drive the endpointer over a scripted timeline of partials, ticking every
// 200ms, and return the FIRST terminal decision (with the time it fired).
function runTimeline(
  partials: Array<{ at: number; text: string }>,
  endMs: number,
  cfg = DEFAULT_ENDPOINT_CONFIG,
): { decision: EndpointDecision; at: number } {
  const ep = new UtteranceEndpointer(cfg, 0);
  for (let t = 0; t <= endMs; t += 200) {
    for (const p of partials) if (p.at === t) ep.onPartial(p.text, t);
    const d = ep.tick(t);
    if (d.action !== "wait") return { decision: d, at: t };
  }
  return { decision: { action: "wait" }, at: endMs };
}

// 1) Normal utterance: words arrive, then silence → submit ~silenceMs later.
{
  const r = runTimeline(
    [
      { at: 400, text: "add" },
      { at: 800, text: "add a login" },
      { at: 1200, text: "add a login button" },
    ],
    6000,
  );
  eq(r.decision.action, "submit", "normal: submits");
  if (r.decision.action === "submit") {
    eq(r.decision.text, "add a login button", "normal: latest transcript");
    eq(r.decision.reason, "silence", "normal: silence reason");
  }
  // Last change at 1200, silence window 1200 → decision at 2400.
  ok(r.at >= 2400 && r.at <= 2600, `normal: fires after silence window (at ${r.at})`);
}

// 2) Driver says nothing → timeout, never submit.
{
  const r = runTimeline([], 9000);
  eq(r.decision.action, "timeout", "no-speech: times out");
  ok(r.at >= 8000 && r.at <= 8200, `no-speech: at noSpeechTimeout (at ${r.at})`);
}

// 3) A stray single-char blip must NOT submit (min-chars guard); it then
//    behaves like no real speech and eventually times out.
{
  const r = runTimeline([{ at: 400, text: "a" }], 9000);
  ok(r.decision.action !== "submit", "blip: does not submit a 1-char utterance");
}

// 4) Mid-sentence pauses shorter than silenceMs do NOT clip the driver.
{
  const r = runTimeline(
    [
      { at: 400, text: "fix the" }, // pause ~1s (< 1200)
      { at: 1400, text: "fix the bug in auth" },
      { at: 1800, text: "fix the bug in auth handler" },
    ],
    7000,
  );
  eq(r.decision.action, "submit", "pause: still submits");
  if (r.decision.action === "submit") {
    eq(r.decision.text, "fix the bug in auth handler", "pause: full sentence, not clipped");
  }
  ok(r.at >= 3000, `pause: waited for the real end (at ${r.at})`);
}

// 5) Endless monologue hits the hard cap and force-submits.
{
  const cfg = { ...DEFAULT_ENDPOINT_CONFIG, maxUtteranceMs: 3000 };
  const partials: Array<{ at: number; text: string }> = [];
  for (let t = 400; t <= 6000; t += 400) partials.push({ at: t, text: `word${t}` });
  const r = runTimeline(partials, 8000, cfg);
  eq(r.decision.action, "submit", "monologue: force-submits at cap");
  if (r.decision.action === "submit") eq(r.decision.reason, "max-length", "monologue: max-length reason");
  // firstSpeech at 400, cap 3000 → ~3400.
  ok(r.at >= 3200 && r.at <= 3800, `monologue: fires near cap (at ${r.at})`);
}

// 6) Terminal decision is emitted once; further ticks are inert (no double submit).
{
  const ep = new UtteranceEndpointer(DEFAULT_ENDPOINT_CONFIG, 0);
  ep.onPartial("ship it", 400);
  let submits = 0;
  for (let t = 0; t <= 5000; t += 200) {
    const d = ep.tick(t);
    if (d.action === "submit") submits++;
  }
  eq(submits, 1, "idempotent: exactly one submit across many ticks");
}

// 7) reset() re-arms for the next utterance in the loop.
{
  const ep = new UtteranceEndpointer(DEFAULT_ENDPOINT_CONFIG, 0);
  ep.onPartial("first turn", 400);
  ep.tick(2000); // submit
  ep.reset(3000);
  ok(!ep.hasSpeech(), "reset: speech cleared");
  ep.onPartial("second turn", 3400);
  const d = ep.tick(5000);
  eq(d.action, "submit", "reset: second utterance submits");
  if (d.action === "submit") eq(d.text, "second turn", "reset: fresh transcript");
}

console.log(`\ncarVoiceEndpoint: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
