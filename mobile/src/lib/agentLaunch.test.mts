// agentLaunch.test.mts — pin the exact runner launch flags.
// Run: npx tsx src/lib/agentLaunch.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { AGENT_LAUNCHERS, launchLine, closeLine } from "./agentLaunch.ts";

test("the three supported runners are present with exact dangerous flags", () => {
  const byId = Object.fromEntries(AGENT_LAUNCHERS.map((l) => [l.id, l.command]));
  assert.equal(byId.claude, "claude --dangerously-skip-permissions");
  assert.equal(byId.codex, "codex --dangerously-bypass-approvals-and-sandbox");
  assert.equal(byId.opencode, "opencode");
  assert.equal(AGENT_LAUNCHERS.length, 3);
});

test("launchLine appends exactly one newline (the Enter press)", () => {
  for (const l of AGENT_LAUNCHERS) {
    assert.equal(launchLine(l), `${l.command}\n`);
    assert.equal(launchLine(l).match(/\n/g)?.length, 1);
  }
});

test("closeLine sends /exit + Enter for every runner", () => {
  for (const l of AGENT_LAUNCHERS) {
    assert.equal(l.closeCommand, "/exit");
    assert.equal(closeLine(l), "/exit\n");
  }
});
