// carVoiceConfirm.test.mts — the screen-layer safety gate: which commands need
// an explicit confirm, the prompt text, and how spoken replies are interpreted.
// Run: npx tsx src/lib/carVoiceConfirm.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  assessRisk,
  needsConfirm,
  confirmPrompt,
  interpretConfirmReply,
} from "./carVoiceConfirm.ts";

// ── risky commands are gated ──────────────────────────────────────────

test("gates deploy / push / delete / force / reset / prod", () => {
  for (const cmd of [
    "deploy the web app",
    "redeploy to production",
    "git push the branch",
    "force push to main",
    "delete the feature branch",
    "rm -rf the build dir",
    "reset hard to origin",
    "roll back the last release",
    "ship it to prod",
  ]) {
    assert.equal(needsConfirm(cmd), true, `expected risky: ${cmd}`);
  }
});

test("does NOT gate routine coding commands", () => {
  for (const cmd of [
    "add a test for the auth handler",
    "fix the build on magara",
    "rename the dispatcher function",
    "refactor the parser",
    "write a docstring for foo",
    "explain what this module does",
  ]) {
    assert.equal(needsConfirm(cmd), false, `expected safe: ${cmd}`);
  }
});

// ── storage_reclaim / proc_kill (destructive ops verbs) ───────────────
// These map to verbs that delete files and terminate processes on the box.
// The generic 'delete' pattern matches none of the natural phrasings.

test("gates disk reclaim and process kill", () => {
  for (const cmd of [
    "clean up my disk",
    "free up some space on the mac mini",
    "reclaim the build artifacts",
    "purge the caches",
    "prune docker",
    "clear out the storage",
    "empty the trash",
    "kill that process",
    "terminate pid 4123",
    "force quit chrome",
  ]) {
    assert.equal(needsConfirm(cmd), true, `expected risky: ${cmd}`);
  }
});

test("read-only monitoring stays ungated", () => {
  // Looking is free; only destruction stops and asks.
  for (const cmd of [
    "how much disk is left",
    "show me the top processes",
    "what's using all the memory",
    "clean up the naming in that function",
  ]) {
    assert.equal(needsConfirm(cmd), false, `expected safe: ${cmd}`);
  }
});

test("does not false-positive on lookalike words", () => {
  // 'deltas' must not trip 'delete'; 'redemption' must not trip 'reset'.
  assert.equal(needsConfirm("compute the deltas between runs"), false);
  assert.equal(needsConfirm("add a redemption code field"), false);
});

// ── assessment detail ─────────────────────────────────────────────────

test("assessRisk reports the matched kinds and a prompt", () => {
  const r = assessRisk("force push to production");
  assert.equal(r.risky, true);
  assert.ok(r.kinds.includes("force"));
  assert.ok(r.kinds.includes("prod"));
  assert.match(r.prompt, /say "confirm"|tap Confirm/i);
});

test("confirmPrompt is empty for no risk", () => {
  assert.equal(confirmPrompt("add a test", []), "");
});

// ── spoken reply interpretation ───────────────────────────────────────

test("interpretConfirmReply: clear affirmatives confirm", () => {
  for (const r of ["confirm", "confirmed", "yes", "yeah do it", "go ahead", "proceed"]) {
    assert.equal(interpretConfirmReply(r), "confirm", r);
  }
});

test("interpretConfirmReply: negation wins even with a stray yes", () => {
  assert.equal(interpretConfirmReply("no"), "cancel");
  assert.equal(interpretConfirmReply("cancel that"), "cancel");
  assert.equal(interpretConfirmReply("no, don't, I said yes earlier but cancel"), "cancel");
});

test("interpretConfirmReply: ambiguous is unclear, not a confirm", () => {
  assert.equal(interpretConfirmReply("uh, maybe"), "unclear");
  assert.equal(interpretConfirmReply(""), "unclear");
});
