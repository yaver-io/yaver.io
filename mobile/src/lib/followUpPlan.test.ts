// Run: npx tsx mobile/src/lib/followUpPlan.test.ts
import { planFollowUp } from "./followUpPlan";

let failures = 0;
function check(name: string, cond: boolean, detail?: string) {
  if (cond) {
    console.log(`  ok   ${name}`);
  } else {
    failures++;
    console.error(`  FAIL ${name}${detail ? " — " + detail : ""}`);
  }
}

console.log("planFollowUp");

// The reported bug, in one case. A task that finished is the COMMON state by
// the time a user reads the answer and types a reply — so this path, not
// "continue", is what most follow-ups actually take. It must carry the
// conversation, or the user watches their chat get replaced by an empty one.
{
  const p = planFollowUp({ parentRunner: "codex", desiredRunner: "codex", status: "completed" });
  check("finished parent forks silently", p.action === "fork-silent", p.action);
  check("finished parent keeps the same runner", p.forkRunner === "codex", p.forkRunner);
  check("finished parent carries the conversation", p.carriesConversation);
}

// Every finished status forks — not just "completed". A user replying to a
// FAILED task is asking it to try again, which is the case most likely to be
// mistaken for "the app lost my message".
for (const status of ["completed", "review", "failed", "stopped"]) {
  const p = planFollowUp({ parentRunner: "claude", desiredRunner: "claude", status });
  check(`status ${status} forks`, p.action === "fork-silent", p.action);
  check(`status ${status} carries conversation`, p.carriesConversation);
}

// A live task continues in place — no new task, no fork.
for (const status of ["running", "queued", "streaming", ""]) {
  const p = planFollowUp({ parentRunner: "codex", desiredRunner: "codex", status });
  check(`live status ${status || "(empty)"} continues`, p.action === "continue", p.action);
}

// Changing the runner is the one fork the user should be ASKED about: the chat
// formats differ and it is a deliberate act, unlike replying to a done task.
{
  const p = planFollowUp({ parentRunner: "codex", desiredRunner: "claude", status: "running" });
  check("runner change asks first", p.action === "fork-confirm", p.action);
  check("runner change forks to the NEW runner", p.forkRunner === "claude", p.forkRunner);
}

// Runner change wins over finished: a confirm dialog must not be skipped just
// because the parent also happens to be done.
{
  const p = planFollowUp({ parentRunner: "codex", desiredRunner: "claude", status: "completed" });
  check("runner change beats finished (still confirms)", p.action === "fork-confirm", p.action);
}

// An unknown parent runner must NOT read as "changed". Legacy tasks have no
// recorded runnerId; treating that as a switch would pop a confirm dialog on
// the first follow-up to every old task.
{
  const p = planFollowUp({ parentRunner: "", desiredRunner: "codex", status: "running" });
  check("unknown parent runner does not count as a change", p.action === "continue", p.action);
}
{
  const p = planFollowUp({ parentRunner: "codex", desiredRunner: "", status: "running" });
  check("empty picker does not count as a change", p.action === "continue", p.action);
}

// Legacy finished task with no recorded runner still needs a non-empty runner,
// because the agent's fork endpoint rejects an empty one.
{
  const p = planFollowUp({ parentRunner: "", desiredRunner: "", status: "completed" });
  check("legacy finished task falls back to a real runner", p.forkRunner === "claude", p.forkRunner);
}

// Adopted tmux sessions bypass all of it — input goes to the pane.
{
  const p = planFollowUp({ isAdopted: true, parentRunner: "codex", status: "completed" });
  check("adopted tmux sends input directly", p.action === "tmux-input", p.action);
  check("adopted tmux never forks", p.forkRunner === "");
}

// Whitespace must not create a phantom runner change.
{
  const p = planFollowUp({ parentRunner: " codex ", desiredRunner: "codex", status: "running" });
  check("whitespace is not a runner change", p.action === "continue", p.action);
}

if (failures > 0) {
  console.error(`\n${failures} check(s) failed`);
  process.exit(1);
}
console.log("\nall checks passed");
