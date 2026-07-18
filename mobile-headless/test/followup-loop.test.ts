// followup-loop.test.ts — closed loop for the mobile follow-up bug, against a
// REAL agent (no mocks, matching the house style in desktop/agent/*_test.go).
//
// What this covers that followUpPlan.test.ts cannot: the plan is computed from
// a task whose status came off a real runner on a real machine, not a literal
// in a fixture. The bug was never "the logic is wrong in the abstract" — it was
// that the state a task is ACTUALLY in when a human replies (finished) takes
// the fork path, and nobody had looked at what that does to the chat.
//
// Run:
//   YAVER_AGENT=http://<host>:18080 YAVER_TOKEN=<token> \
//     bun test test/followup-loop.test.ts
//
// Skips (does not fail) when those are unset, so the hermetic suite stays
// runnable offline.

import { expect, test } from "bun:test";
import { planFollowUp } from "../../mobile/src/lib/followUpPlan";

const AGENT = process.env.YAVER_AGENT;
const TOKEN = process.env.YAVER_TOKEN;
const live = Boolean(AGENT && TOKEN);
const RUNNER = process.env.YAVER_RUNNER || "codex";

async function api(path: string, init: RequestInit = {}) {
  const res = await fetch(`${AGENT}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${TOKEN}`,
      "Content-Type": "application/json",
      ...(init.headers || {}),
    },
  });
  const text = await res.text();
  let body: any = text;
  try {
    body = JSON.parse(text);
  } catch {
    /* leave as text */
  }
  return { status: res.status, body };
}

test.if(live)(
  "a finished task takes the fork path, and the plan says to carry the conversation",
  async () => {
    // 1. Create a real task on a real runner.
    const created = await api("/tasks", {
      method: "POST",
      body: JSON.stringify({
        title: "followup-loop probe: reply with the word pong",
        input: "Reply with exactly the word: pong",
        runner: RUNNER,
      }),
    });
    expect(created.status).toBeLessThan(400);
    const taskId = created.body?.id || created.body?.taskId || created.body?.task?.id;
    expect(taskId).toBeTruthy();

    // 2. Wait for it to reach a terminal state — which is the whole point.
    //    By the time a human reads an answer and types a reply, the task is
    //    finished. That is the NORMAL case, not an edge case.
    const terminal = new Set(["completed", "review", "failed", "stopped"]);
    let status = "";
    const deadline = Date.now() + 240_000;
    while (Date.now() < deadline) {
      const got = await api(`/tasks/${taskId}`);
      status = got.body?.task?.status || got.body?.status || "";
      if (terminal.has(status)) break;
      await new Promise((r) => setTimeout(r, 5000));
    }
    expect(terminal.has(status)).toBe(true);

    // 3. The app's own decision function, fed the REAL status.
    const plan = planFollowUp({
      isAdopted: false,
      parentRunner: RUNNER,
      desiredRunner: RUNNER,
      status,
    });

    // This is the assertion that matters. A real, just-finished task forks —
    // so the UI MUST carry the conversation across, or the user watches their
    // chat get replaced by an empty one.
    expect(plan.action).toBe("fork-silent");
    expect(plan.carriesConversation).toBe(true);
    expect(plan.forkRunner).toBe(RUNNER);

    // 4. Drive the fork the way the screen does, and prove the child is real
    //    and distinct — the "it shows a new task" half of the report.
    const forked = await api(`/tasks/${taskId}/fork`, {
      method: "POST",
      body: JSON.stringify({ runner: plan.forkRunner, input: "FOLLOWUP-PROBE" }),
    });
    expect(forked.status).toBeLessThan(400);
    const childId = forked.body?.taskId || forked.body?.id;
    expect(childId).toBeTruthy();
    expect(childId).not.toBe(taskId);

    // 5. The parent must survive. Forking is non-destructive; if the parent
    //    were consumed, "carry the conversation" would be unimplementable.
    const parentAfter = await api(`/tasks/${taskId}`);
    expect(parentAfter.status).toBeLessThan(400);
  },
  300_000,
);

// A live task that is still running must NOT fork — it continues in place.
// Without this, a passing fork test could just mean "everything forks".
test.if(live)("a running task continues in place rather than forking", async () => {
  const created = await api("/tasks", {
    method: "POST",
    body: JSON.stringify({
      title: "followup-loop probe: slow count",
      input: "Count slowly from 1 to 20, one number per line.",
      runner: RUNNER,
    }),
  });
  expect(created.status).toBeLessThan(400);
  const taskId = created.body?.id || created.body?.taskId || created.body?.task?.id;

  // Catch it while it is still working.
  let status = "";
  for (let i = 0; i < 10; i++) {
    const got = await api(`/tasks/${taskId}`);
    status = got.body?.task?.status || got.body?.status || "";
    if (status && !["completed", "review", "failed", "stopped"].includes(status)) break;
    await new Promise((r) => setTimeout(r, 1000));
  }

  if (["completed", "review", "failed", "stopped"].includes(status)) {
    // It finished faster than we could observe it running. Not a failure of
    // the code under test, and saying so is better than a bogus green.
    console.warn(`[skip] task reached ${status} before it could be observed running`);
    return;
  }

  const plan = planFollowUp({
    isAdopted: false,
    parentRunner: RUNNER,
    desiredRunner: RUNNER,
    status,
  });
  expect(plan.action).toBe("continue");
  expect(plan.carriesConversation).toBe(false);

  await api(`/tasks/${taskId}/stop`, { method: "POST" }).catch(() => {});
}, 120_000);

test("plan logic is exercised even without a live agent", () => {
  expect(planFollowUp({ status: "completed", parentRunner: "codex", desiredRunner: "codex" }).action)
    .toBe("fork-silent");
  expect(planFollowUp({ status: "running", parentRunner: "codex", desiredRunner: "codex" }).action)
    .toBe("continue");
});
