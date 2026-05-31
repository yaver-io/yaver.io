// interpreter.test.mts — response-message → action-chips interpretation.
// Run: npx tsx src/lib/localAgent/interpreter.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { interpretMessage, buildInterpretPrompt } from "./interpreter.ts";

test("crash/restart in progress → Debug + Logs + Restart (the screenshot)", () => {
  const r = interpretMessage("Agent process crashed — restarting (attempt 1/4)…", { deviceRef: "mac" });
  assert.equal(r.needsLlm, false);
  const labels = r.chips.map((c) => c.label);
  assert.ok(labels.includes("Debug"));
  assert.ok(labels.includes("Logs"));
  assert.ok(labels.includes("Restart agent"));
  // chips carry the device + valid disposition
  assert.ok(r.chips.every((c) => c.disposition === "auto" || c.disposition === "confirm"));
});

test("retries exhausted → Reconnect first", () => {
  const r = interpretMessage("restarting (attempt 4/4)… all retries exhausted", { deviceRef: "box1" });
  assert.equal(r.chips[0].label, "Reconnect");
  assert.equal(r.chips[0].actionId, "device.recoverAuth");
});

test("runner needs OAuth → Sign in Codex (subscription)", () => {
  const r = interpretMessage("Codex is not signed in. Please sign in to continue.", { deviceRef: "mac" });
  const signin = r.chips.find((c) => c.actionId === "runner.install");
  assert.ok(signin);
  assert.match(signin!.label, /Codex/);
  assert.equal((signin!.args as any).op, "browser_start");
  assert.equal((signin!.args as any).runner, "codex");
});

test("runner OAuth uses ctx.runner when message is generic", () => {
  const r = interpretMessage("This runner requires authentication.", { runner: "opencode", deviceRef: "x" });
  const signin = r.chips.find((c) => c.actionId === "runner.install");
  assert.match(signin!.label, /OpenCode/);
});

test("auth expired → Reconnect", () => {
  const r = interpretMessage("Agent session expired — please re-auth.", { deviceRef: "mac" });
  assert.equal(r.chips[0].actionId, "device.recoverAuth");
});

test("device offline → Try again + status", () => {
  const r = interpretMessage("No recent heartbeat; host is down.", { deviceRef: "box1" });
  assert.ok(r.chips.some((c) => c.ui === "retry"));
  assert.ok(r.chips.some((c) => c.actionId === "status"));
});

test("build failed → Rebuild (confirm tier)", () => {
  const r = interpretMessage("Build failed: tsc error TS2322", { deviceRef: "mac" });
  const rebuild = r.chips.find((c) => c.actionId === "build");
  assert.ok(rebuild);
  assert.equal(rebuild!.disposition, "confirm");
});

test("deploy failed → Retry deploy (confirm)", () => {
  const r = interpretMessage("Deployment failed: push rejected", { deviceRef: "mac" });
  const dep = r.chips.find((c) => c.actionId === "deploy");
  assert.equal(dep?.disposition, "confirm");
});

test("rate limited → status only, no hammering", () => {
  const r = interpretMessage("429 Too Many Requests — usage limit", { deviceRef: "mac" });
  assert.deepEqual(r.chips.map((c) => c.actionId), ["status"]);
});

test("approval prompt → no auto chips, surfaced to user", () => {
  const r = interpretMessage("Do you want to proceed? (y/n)", {});
  assert.equal(r.chips.length, 0);
  assert.equal(r.needsLlm, false);
  assert.match(r.summary ?? "", /approval/i);
});

test("unrecognized free-form → escalate to LLM", () => {
  const r = interpretMessage("Refactored the auth module and added 3 tests.", {});
  assert.equal(r.needsLlm, true);
  assert.equal(r.chips.length, 0);
});

test("no chip ever references a BLOCKED action", () => {
  // A message mentioning 'destroy' must not yield a cloud.destroy chip.
  const msgs = [
    "Agent process crashed — restarting (attempt 1/4)…",
    "Codex needs sign-in",
    "Build failed",
    "destroy the server now",
  ];
  for (const m of msgs) {
    for (const c of interpretMessage(m, { deviceRef: "x" }).chips) {
      assert.notEqual(c.actionId, "cloud.destroy");
      assert.notEqual(c.actionId, "device.remove");
      assert.notEqual(c.disposition as string, "blocked");
    }
  }
});

test("buildInterpretPrompt constrains to allowed action ids", () => {
  const p = buildInterpretPrompt("something happened", ["status", "device.recoverAuth"]);
  assert.match(p, /status, device\.recoverAuth/);
  assert.match(p, /Never invent an actionId/);
});
