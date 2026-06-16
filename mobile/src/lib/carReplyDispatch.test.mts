// carReplyDispatch.test.mts — Tier 1 car reply → dispatch gate.
// Covers risky-verb detection, the confirm/cancel state machine, and that a
// raw risky reply NEVER auto-dispatches (must be confirmed first).
// Run: npx tsx src/lib/carReplyDispatch.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  isRiskyReply,
  isConfirmReply,
  isCancelReply,
  CarReplyGate,
  handleCarReply,
} from "./carReplyDispatch.ts";
import type { CarVoiceDeps, CarVoiceTaskRef } from "./carVoiceCoding.ts";

// ── deps that record every dispatch ──────────────────────────────────
function recordingDeps() {
  const dispatched: string[] = [];
  const deps: CarVoiceDeps = {
    transcribe: async () => "",
    dispatch: async (_title: string, prompt: string) => {
      dispatched.push(prompt);
      return "task-1";
    },
    getTask: async (id: string): Promise<CarVoiceTaskRef> => ({
      id,
      status: "completed",
      resultText: "Done.",
    }),
    speak: async () => {},
    sleep: async () => {},
    now: () => 0,
  };
  return { deps, dispatched };
}

// ── risky-verb detection ─────────────────────────────────────────────
test("isRiskyReply flags destructive/irreversible verbs", () => {
  for (const t of [
    "deploy to production",
    "push the branch",
    "force push",
    "rm -rf node_modules",
    "delete the database",
    "git reset --hard",
    "drop table users",
    "terraform destroy",
    "merge into main",
    "shutdown the box",
  ]) {
    assert.equal(isRiskyReply(t), true, `expected risky: ${t}`);
  }
});

test("isRiskyReply lets normal coding commands through", () => {
  for (const t of [
    "add a test for the parser",
    "fix the build",
    "rename the function to handleReply",
    "explain the failing test",
    "format the file",
  ]) {
    assert.equal(isRiskyReply(t), false, `expected safe: ${t}`);
  }
});

test("confirm/cancel detection", () => {
  for (const t of ["confirm", "yes", "do it", "Go ahead.", "proceed"]) {
    assert.equal(isConfirmReply(t), true, t);
  }
  for (const t of ["cancel", "no", "stop", "never mind"]) {
    assert.equal(isCancelReply(t), true, t);
  }
  assert.equal(isConfirmReply("add a test"), false);
  assert.equal(isCancelReply("add a test"), false);
});

// ── confirm-gate state machine ───────────────────────────────────────
test("a safe reply dispatches immediately", async () => {
  const { deps, dispatched } = recordingDeps();
  const gate = new CarReplyGate();
  const d = await handleCarReply({ conversationId: "c", text: "add a test", gate, deps });
  assert.equal(d.outcome, "dispatched");
  assert.deepEqual(dispatched, ["add a test"]);
});

test("a risky reply does NOT dispatch — it asks to confirm", async () => {
  const { deps, dispatched } = recordingDeps();
  const gate = new CarReplyGate();
  const d = await handleCarReply({ conversationId: "c", text: "deploy to prod", gate, deps });
  assert.equal(d.outcome, "needs-confirm");
  assert.equal(dispatched.length, 0, "risky command must not fire unconfirmed");
  assert.ok(gate.hasPending("c"));
  assert.match(d.reply, /confirm/i);
});

test("confirm releases the stashed risky command", async () => {
  const { deps, dispatched } = recordingDeps();
  const gate = new CarReplyGate();
  await handleCarReply({ conversationId: "c", text: "push the release branch", gate, deps });
  assert.equal(dispatched.length, 0);
  const d = await handleCarReply({ conversationId: "c", text: "confirm", gate, deps });
  assert.equal(d.outcome, "confirmed");
  assert.deepEqual(dispatched, ["push the release branch"]);
  assert.equal(gate.hasPending("c"), false);
});

test("cancel discards the stashed risky command without dispatching", async () => {
  const { deps, dispatched } = recordingDeps();
  const gate = new CarReplyGate();
  await handleCarReply({ conversationId: "c", text: "rm -rf build", gate, deps });
  const d = await handleCarReply({ conversationId: "c", text: "cancel", gate, deps });
  assert.equal(d.outcome, "cancelled");
  assert.equal(dispatched.length, 0);
  assert.equal(gate.hasPending("c"), false);
});

test("a fresh command while a risky one is pending replaces it (and re-gates)", async () => {
  const { deps, dispatched } = recordingDeps();
  const gate = new CarReplyGate();
  await handleCarReply({ conversationId: "c", text: "deploy", gate, deps });
  // Driver changes their mind and says a safe command instead.
  const d = await handleCarReply({ conversationId: "c", text: "add a test", gate, deps });
  assert.equal(d.outcome, "dispatched");
  assert.deepEqual(dispatched, ["add a test"]);
  assert.equal(gate.hasPending("c"), false);
});

test("a fresh risky command while another is pending re-stashes (still gated)", async () => {
  const { deps, dispatched } = recordingDeps();
  const gate = new CarReplyGate();
  await handleCarReply({ conversationId: "c", text: "deploy", gate, deps });
  const d = await handleCarReply({ conversationId: "c", text: "drop table users", gate, deps });
  assert.equal(d.outcome, "needs-confirm");
  assert.equal(dispatched.length, 0);
  assert.ok(gate.hasPending("c"));
});

test("empty reply is ignored", async () => {
  const { deps, dispatched } = recordingDeps();
  const gate = new CarReplyGate();
  const d = await handleCarReply({ conversationId: "c", text: "   ", gate, deps });
  assert.equal(d.outcome, "ignored");
  assert.equal(dispatched.length, 0);
});

test("gate is per-conversation (confirm on one doesn't release another)", async () => {
  const { deps, dispatched } = recordingDeps();
  const gate = new CarReplyGate();
  await handleCarReply({ conversationId: "a", text: "deploy", gate, deps });
  // "confirm" on a DIFFERENT conversation with no pending → treated as a fresh
  // (safe) command, but it must NOT release conversation a's pending deploy.
  const d = await handleCarReply({ conversationId: "b", text: "confirm", gate, deps });
  assert.notEqual(d.outcome, "confirmed");
  assert.ok(gate.hasPending("a"), "conversation a's deploy must stay pending");
  assert.equal(dispatched.includes("deploy"), false, "deploy must never have fired");
});
