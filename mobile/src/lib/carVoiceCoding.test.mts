// carVoiceCoding.test.mts — Tier 0 car voice loop: summarization guards,
// the read-code refusal, dispatch/poll/timeout, and the never-speak-code rule.
// Run: npx tsx src/lib/carVoiceCoding.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  isReadCodeRequest,
  summarizeForReadback,
  titleFromTranscript,
  dispatchAndSummarize,
  runCarVoiceTurn,
  READBACK_MAX_CHARS,
  type CarVoiceDeps,
  type CarVoiceTaskRef,
} from "./carVoiceCoding.ts";

// ── tiny deps builder ────────────────────────────────────────────────

function deps(over: Partial<CarVoiceDeps> = {}): CarVoiceDeps {
  return {
    transcribe: async () => "add a test",
    dispatch: async () => "task-1",
    getTask: async () => ({ id: "task-1", status: "completed", resultText: "Tests pass." }),
    speak: async () => {},
    sleep: async () => {},
    now: () => 0,
    ...over,
  };
}

// ── read-code refusal ────────────────────────────────────────────────

test("isReadCodeRequest catches read-the-code asks", () => {
  assert.equal(isReadCodeRequest("read me the diff"), true);
  assert.equal(isReadCodeRequest("show me the code"), true);
  assert.equal(isReadCodeRequest("tell me what's in the file"), true);
  assert.equal(isReadCodeRequest("recite the stack trace"), true);
});

test("isReadCodeRequest does NOT block normal coding commands", () => {
  assert.equal(isReadCodeRequest("add a test for the auth handler"), false);
  assert.equal(isReadCodeRequest("fix the build on magara"), false);
  assert.equal(isReadCodeRequest("deploy the web app"), false);
});

test("dispatchAndSummarize declines read-code requests without dispatching", async () => {
  let dispatched = false;
  const r = await dispatchAndSummarize("read me the diff", deps({
    dispatch: async () => { dispatched = true; return "x"; },
  }));
  assert.equal(r.declined, true);
  assert.equal(dispatched, false);
  assert.match(r.spoken, /parked/i);
});

// ── summarization never speaks code ──────────────────────────────────

test("summarizeForReadback leads with status and stays one short sentence", () => {
  assert.match(summarizeForReadback({ id: "t", status: "completed", resultText: "Tests pass on magara." }), /^Done\./);
  assert.match(summarizeForReadback({ id: "t", status: "failed", resultText: "Build error." }), /^That failed\./);
  assert.match(summarizeForReadback({ id: "t", status: "review" }), /review/i);
});

test("summarizeForReadback refuses to read code-shaped output", () => {
  const codey: CarVoiceTaskRef = {
    id: "t",
    status: "completed",
    resultText: "```go\nfunc main() { return }\n```\nconst x = 1;",
  };
  const spoken = summarizeForReadback(codey);
  // Only the status lead survives — no code tokens leak into speech.
  assert.equal(spoken, "Done.");
  assert.ok(!/func|const|```|{|}/.test(spoken));
});

test("summarizeForReadback clamps long bodies hard", () => {
  const long = "Done stuff. " + "x".repeat(500);
  const spoken = summarizeForReadback({ id: "t", status: "completed", resultText: long });
  assert.ok(spoken.length <= READBACK_MAX_CHARS);
});

// ── title ────────────────────────────────────────────────────────────

test("titleFromTranscript truncates politely", () => {
  assert.equal(titleFromTranscript("short one"), "short one");
  const long = "please add a comprehensive integration test, then refactor the dispatcher and re-run everything";
  const out = titleFromTranscript(long);
  assert.ok(out.length <= 62);
});

// ── dispatch / poll / terminal ───────────────────────────────────────

test("dispatchAndSummarize polls until terminal then summarizes", async () => {
  let calls = 0;
  const r = await dispatchAndSummarize("add a test", deps({
    getTask: async () => {
      calls++;
      return calls < 3
        ? { id: "task-1", status: "running" }
        : { id: "task-1", status: "completed", resultText: "All green." };
    },
  }));
  assert.equal(r.status, "completed");
  assert.match(r.spoken, /^Done\./);
  assert.ok(calls >= 3);
});

test("dispatchAndSummarize times out gracefully", async () => {
  let t = 0;
  const r = await dispatchAndSummarize("add a test", deps({
    getTask: async () => ({ id: "task-1", status: "running" }),
    now: () => { t += 10 * 60 * 1000; return t; }, // jump past maxWait each call
  }), { maxWaitMs: 1 });
  assert.equal(r.timedOut, true);
  assert.match(r.spoken, /phone/i);
});

test("dispatchAndSummarize reports an unreachable box", async () => {
  const r = await dispatchAndSummarize("add a test", deps({
    dispatch: async () => { throw new Error("ECONNREFUSED"); },
  }));
  assert.equal(r.error, "ECONNREFUSED");
  assert.match(r.spoken, /box/i);
});

// ── full turn ────────────────────────────────────────────────────────

test("runCarVoiceTurn speaks ack then summary, never the code", async () => {
  const spoken: string[] = [];
  const r = await runCarVoiceTurn("file://clip.m4a", deps({
    transcribe: async () => "fix the build",
    getTask: async () => ({ id: "task-1", status: "completed", resultText: "function foo() {}" }),
    speak: async (s) => { spoken.push(s); },
  }));
  assert.equal(r.transcript, "fix the build");
  assert.ok(spoken.includes("On it."));
  assert.ok(spoken.some((s) => s.startsWith("Done.")));
  assert.ok(!spoken.some((s) => /function|{|}/.test(s)));
});

test("runCarVoiceTurn handles empty transcript", async () => {
  const spoken: string[] = [];
  const r = await runCarVoiceTurn("file://clip.m4a", deps({
    transcribe: async () => "   ",
    speak: async (s) => { spoken.push(s); },
  }));
  assert.equal(r.transcript, "");
  assert.match(spoken[0], /didn't catch/i);
});
