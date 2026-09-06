import test from "node:test";
import assert from "node:assert/strict";
import { buildTaskHumanSummary, humanizeTaskCommand } from "./taskHumanSummary";

test("humanizes common coding actions instead of exposing shell syntax", () => {
  assert.equal(humanizeTaskCommand("pnpm test -- --runInBand"), "Run tests");
  assert.equal(humanizeTaskCommand("git diff --stat"), "Review changes");
  assert.equal(humanizeTaskCommand("npx tsc --noEmit"), "Check types");
  assert.equal(
    humanizeTaskCommand("custom-tool --opaque=value && another-tool --flag"),
    "Work in the project",
  );
});

test("running summary names the latest action and explicit outcomes", () => {
  const summary = buildTaskHumanSummary(
    {
      title: "Fix login",
      status: "running",
      output: [],
      progressLine: "10:51 elapsed · last update 4s ago",
      presentationDetail: "Checking BrowserVibeBubble reload flow.",
      agentVersion: "1.99.397",
      latestAgentVersion: "1.99.435",
      agentVersionDistance: 38,
    },
    {
      inspect: {
        id: "inspect", command: "rg -n auth src", args: [], cwd: "", runner: "codex", startedAt: 1,
        stdout: "", stderr: "", status: "ok", exitCode: 0, durationMs: 100, truncated: false,
      },
      test: {
        id: "test", command: "pnpm test", args: [], cwd: "", runner: "codex", startedAt: 2,
        stdout: "", stderr: "", status: "error", exitCode: 1, durationMs: 1200, truncated: false,
      },
      build: {
        id: "build", command: "pnpm build", args: [], cwd: "", runner: "codex", startedAt: 3,
        stdout: "", stderr: "", status: "running", truncated: false,
      },
    },
  );
  assert.equal(summary.title, "Work in progress");
  assert.match(summary.detail, /Checking BrowserVibeBubble reload flow/);
  assert.match(summary.detail, /Build the project is running now/);
  assert.match(summary.detail, /10:51 elapsed · last update 4s ago/);
  assert.deepEqual(summary.steps.map((step) => step.state), ["succeeded", "failed", "running"]);
  assert.ok(summary.facts.includes("1 command succeeded"));
  assert.ok(summary.facts.includes("1 command failed"));
  assert.ok(summary.facts.includes("Yaver 1.99.397 -> 1.99.435 (38 behind)"));
  assert.match(summary.nextAction || "", /Update the box from Yaver 1\.99\.397 to 1\.99\.435 after this task/);
});

test("structured failure carries cause and recovery route", () => {
  const summary = buildTaskHumanSummary({
    title: "Ship app",
    status: "review",
    output: [],
    failure: {
      title: "Tests failed",
      reason: "Two checkout tests did not pass.",
      remedy: "Open the failed tests, fix them, then run the suite again.",
    },
  });
  assert.equal(summary.title, "Tests failed");
  assert.equal(summary.detail, "Two checkout tests did not pass.");
  assert.equal(summary.nextAction, "Open the failed tests, fix them, then run the suite again.");
  assert.equal(summary.tone, "error");
});

test("completed task surfaces semantic result and delivery evidence", () => {
  const summary = buildTaskHumanSummary({
    title: "Fix login",
    status: "completed",
    output: [],
    resultText: "$ npm test\nPASS\ndiff --git a/login.ts b/login.ts",
    presentationDetail: "Login now keeps the session after reload.",
    diffShortstat: "3 files changed, 18 insertions(+), 4 deletions(-)",
    commitSha: "abcdef1234567890",
  });
  assert.equal(summary.detail, "Login now keeps the session after reload.");
  assert.doesNotMatch(summary.detail, /npm test|diff --git/);
  assert.ok(summary.facts.includes("3 files changed, 18 insertions(+), 4 deletions(-)"));
  assert.ok(summary.facts.includes("Commit abcdef12"));
});

test("reopened task recovers activity without inventing command success", () => {
  const summary = buildTaskHumanSummary({
    title: "Audit",
    status: "review",
    output: ["**$ rg -n auth src**", "matches", "**$ pnpm test**", "all tests passed"],
  });
  assert.equal(summary.steps.length, 2);
  assert.ok(summary.steps.every((step) => step.state === "seen"));
  assert.ok(!summary.facts.some((fact) => fact.includes("succeeded")));
});

test("review summary ignores truncated placeholder replies and falls back to real activity", () => {
  const summary = buildTaskHumanSummary({
    title: "Audit",
    status: "review",
    resultText: "…[truncated — open the task for the full text]\nReady for review",
    presentationDetail: "Updated the model preference selector.",
    output: ["**$ pnpm test mobile/src/lib/taskHumanSummary.test.mts**"],
  });
  assert.equal(summary.title, "Ready for review");
  assert.equal(summary.detail, "Updated the model preference selector. Run tests is the latest recorded action.");
});

test("queued summary prefers explicit presentation and progress over generic filler", () => {
  const summary = buildTaskHumanSummary({
    title: "Audit mobile task UI",
    status: "queued",
    output: [],
    presentationDetail: "Waiting for the current task to finish on ubuntu-4gb.",
    progressLine: "0:05 elapsed · waiting for the first output from the box",
  });
  assert.equal(summary.title, "Waiting to start");
  assert.match(summary.detail, /Waiting for the current task to finish on ubuntu-4gb/);
  assert.match(summary.detail, /0:05 elapsed · waiting for the first output from the box/);
});
