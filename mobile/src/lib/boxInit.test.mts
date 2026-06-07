// boxInit.test.mts — box readiness computation.
// Run: npx tsx src/lib/boxInit.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  computeBoxReadiness,
  readinessSummary,
  type BoxReadinessInput,
  type CheckKey,
} from "./boxInit.ts";

const BASE: BoxReadinessInput = {
  agentOnline: true,
  agentVersion: "1.99.260",
  runners: [],
  providers: [],
};

function check(input: BoxReadinessInput, key: CheckKey) {
  const r = computeBoxReadiness(input);
  const c = r.checks.find((x) => x.key === key);
  assert.ok(c, `missing check ${key}`);
  return c!;
}

test("agent offline ⇒ not-ready and agent check wants wait", () => {
  const r = computeBoxReadiness({ ...BASE, agentOnline: false });
  assert.equal(r.overall, "not-ready");
  assert.equal(r.checks[0].key, "agent");
  assert.equal(r.checks[0].status, "missing");
  assert.equal(r.checks[0].action, "wait_agent");
  assert.equal(readinessSummary(r), "agent offline");
});

test("agent online but no runners ⇒ not-ready (can't code)", () => {
  const r = computeBoxReadiness(BASE);
  assert.equal(r.overall, "not-ready");
  // claude + codex both missing → setup actions offered.
  assert.equal(check(BASE, "claude").action, "setup_claude");
  assert.equal(check(BASE, "codex").action, "setup_codex");
});

test("claude authed ⇒ can code; missing git ⇒ partial (git non-blocking)", () => {
  const input: BoxReadinessInput = {
    ...BASE,
    runners: [{ id: "claude-code", installed: true, ready: true, authConfigured: true, version: "Claude Code 2.1.126" }],
  };
  const r = computeBoxReadiness(input);
  assert.equal(r.overall, "partial");
  assert.equal(check(input, "claude").status, "ok");
  assert.match(check(input, "claude").detail, /2\.1\.126/);
  // git still pending
  assert.equal(check(input, "git_github").status, "missing");
});

test("everything green ⇒ ready, no pending", () => {
  const input: BoxReadinessInput = {
    ...BASE,
    runners: [
      { id: "claude", installed: true, ready: true, authConfigured: true },
      { id: "codex", installed: true, ready: true, authConfigured: true },
    ],
    providers: [
      { id: "github", ready: true, configured: true },
      { id: "gitlab", ready: true, configured: true },
    ],
  };
  const r = computeBoxReadiness(input);
  assert.equal(r.overall, "ready");
  assert.equal(r.pending.length, 0);
  assert.equal(readinessSummary(r), "ready");
});

test("claude installed but not authed ⇒ warn + setup action (sign in on box)", () => {
  const input: BoxReadinessInput = {
    ...BASE,
    runners: [{ id: "claude", installed: true, ready: false, authConfigured: false }],
  };
  const c = check(input, "claude");
  assert.equal(c.status, "warn");
  assert.equal(c.action, "setup_claude");
  // can't code yet (claude not ok, codex missing) → not-ready
  assert.equal(computeBoxReadiness(input).overall, "not-ready");
});

test("local phone: subscription token ⇒ claude ok, codex + git n-a", () => {
  const input: BoxReadinessInput = {
    agentOnline: true,
    isLocalDevice: true,
    claudeSubscription: true,
    runners: [],
    providers: [],
  };
  const r = computeBoxReadiness(input);
  assert.equal(check(input, "agent").status, "ok");
  assert.equal(check(input, "claude").status, "ok");
  assert.equal(check(input, "codex").status, "n-a");
  assert.equal(check(input, "git_github").status, "n-a");
  assert.equal(r.overall, "ready");
});

test("local phone: no subscription ⇒ claude missing wants mirror", () => {
  const input: BoxReadinessInput = {
    agentOnline: true,
    isLocalDevice: true,
    claudeSubscription: false,
    runners: [],
    providers: [],
  };
  const c = check(input, "claude");
  assert.equal(c.status, "missing");
  assert.equal(c.action, "mirror_claude");
  assert.equal(computeBoxReadiness(input).overall, "not-ready");
});

test("pending excludes n-a and ok entries", () => {
  const input: BoxReadinessInput = {
    ...BASE,
    runners: [{ id: "claude", installed: true, ready: true, authConfigured: true }],
    providers: [{ id: "github", ready: true, configured: true }],
  };
  const r = computeBoxReadiness(input);
  // only codex (missing) + gitlab (missing) remain
  assert.deepEqual(r.pending.map((c) => c.key).sort(), ["codex", "git_gitlab"]);
});
