/**
 * agentStatus.test.ts — `npx tsx lib/agentStatus.test.ts` from web/.
 * Plain node, same tiny assert harness the mobile libs use.
 *
 * THE CONTRACT TABLE below is the cross-package agreement between this file and
 * mobile/src/lib/agentStatus.ts. web/ and mobile/ have no shared build, so the
 * only thing stopping the two from drifting apart again is that both sides
 * assert the same table: edit one mapping and that side's test fails.
 *
 * Drift is exactly how we got here — four palettes, three meanings for
 * `completed`, and nothing red anywhere to say so.
 */
import {
  agentIsTerminal,
  agentNeedsYou,
  agentSignalFromTaskStatus,
  agentStateCssColor,
  agentStateHex,
  agentStateVar,
  slotKeyForTask,
  type AgentState,
  type TaskStatus,
} from "./agentStatus";

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
function eq<T>(got: T, want: T, msg: string) {
  ok(got === want, `${msg} (got ${String(got)}, want ${String(want)})`);
}

/** THE CONTRACT TABLE. Mirrored in mobile/src/lib/agentStatus.test.ts. */
const CONTRACT: Record<TaskStatus, { state: AgentState; pulse: boolean; hollow: boolean }> = {
  queued: { state: "working", pulse: false, hollow: true },
  running: { state: "working", pulse: true, hollow: false },
  review: { state: "blocked", pulse: false, hollow: false },
  completed: { state: "verified", pulse: false, hollow: false },
  failed: { state: "failed", pulse: false, hollow: false },
  stopped: { state: "idle", pulse: false, hollow: false },
};

{
  for (const [status, want] of Object.entries(CONTRACT) as [TaskStatus, (typeof CONTRACT)[TaskStatus]][]) {
    const got = agentSignalFromTaskStatus(status);
    eq(got.state, want.state, `${status} -> ${want.state}`);
    eq(got.pulse, want.pulse, `${status} pulse`);
    eq(got.hollow, want.hollow, `${status} hollow`);
    ok(got.label.length > 0, `${status} has a label`);
  }
}

// ── The specific regression: completed was grey here, green on mobile ────────
{
  eq(agentStateVar("verified"), "--success", "completed/verified is the success token, not a grey literal");
  eq(agentStateVar("working"), "--info", "running/working is the info token, not emerald");
  eq(agentStateVar("blocked"), "--warning", "review/blocked is the warning token");
  eq(agentStateVar("failed"), "--danger", "failed is the danger token");
  ok(agentStateVar("working") !== agentStateVar("verified"), "working and verified are distinguishable");
}

// ── Colours resolve through globals.css, not literals ────────────────────────
{
  const states: AgentState[] = ["idle", "working", "blocked", "healing", "verified", "failed", "unknown"];
  for (const state of states) {
    ok(agentStateCssColor(state).startsWith("rgb(var(--"), `${state} styles through a CSS variable`);
  }
  // healing and blocked deliberately share the warning hue; pulse tells them
  // apart. If that ever stops being true, it should be a decision, not a slip.
  eq(agentStateVar("healing"), agentStateVar("blocked"), "healing and blocked share the warning hue by design");
}

// ── WebGL fallback: the VR scene can't read CSS vars ─────────────────────────
{
  // No document here, so this exercises the fallback path the server render and
  // the Three.js materials both hit.
  eq(agentStateHex("verified"), "#16A34A", "verified falls back to the --success value from globals.css");
  eq(agentStateHex("working"), "#2563EB", "working falls back to the --info value");
  eq(agentStateHex("failed"), "#DC2626", "failed falls back to the --danger value");
  ok(agentStateHex("healing") === agentStateHex("blocked"), "the fallback keeps the shared warning hue");
}

// ── "Does this need me?" ─────────────────────────────────────────────────────
{
  eq(agentNeedsYou("blocked"), true, "review/blocked needs you");
  eq(agentNeedsYou("failed"), true, "failed needs you");
  eq(agentNeedsYou("working"), false, "working does not need you");
  eq(agentNeedsYou("healing"), false, "healing is handling itself");
  eq(agentIsTerminal("verified"), true, "verified is terminal");
  eq(agentIsTerminal("healing"), false, "healing is not terminal");
}

// ── Slot key is an address, never a position ─────────────────────────────────
{
  eq(slotKeyForTask({ id: "t1" }), "task:t1", "a task's slot key is its stable id");
  ok(slotKeyForTask({ id: "t1" }) !== slotKeyForTask({ id: "t2" }), "two tasks never share a slot");
}

console.log(`\nagentStatus (web): ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
