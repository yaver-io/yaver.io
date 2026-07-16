/**
 * agentStatus.test.ts — `npx tsx src/lib/agentStatus.test.ts`.
 * No RN, no jest — the tiny assert harness the voice libs use.
 *
 * These pin the properties that make a colour-coded surface trustworthy. The
 * bug this module exists to kill was silent: three palettes disagreed, so the
 * same task was green on Home and blue on Tasks and nothing failed.
 */
import {
  agentIsTerminal,
  agentNeedsYou,
  agentSignalFromAutorun,
  agentSignalFromTaskStatus,
  agentStateBg,
  agentStateColor,
  slotKeyForAutorun,
  type AgentState,
  type AutorunSession,
} from "./agentStatus";
import type { ThemeColors } from "../constants/colors";
import type { TaskStatus } from "./quic";

/**
 * Sentinel palette. The real ThemeColors can't be imported here — it reaches
 * theme/tokens.ts, which imports Platform from react-native, and these tests are
 * plain node like the voice ones.
 *
 * Sentinels test the thing that actually matters anyway: that a state binds to
 * the right SEMANTIC SLOT. Asserting `working -> colors.info` proves the
 * contract in a way that asserting `working -> "#3b82f6"` never could — a hex
 * assertion just restates the token file and passes even if someone swaps two
 * ramps. The real hexes are tokens.ts's job, in both themes.
 */
const C = {
  info: "INFO",
  infoBg: "INFO_BG",
  success: "SUCCESS",
  successBg: "SUCCESS_BG",
  warn: "WARN",
  warnBg: "WARN_BG",
  error: "ERROR",
  errorBg: "ERROR_BG",
  neutral: "NEUTRAL",
  neutralBg: "NEUTRAL_BG",
} as unknown as ThemeColors;

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

const ALL_STATES: AgentState[] = ["idle", "working", "blocked", "healing", "verified", "failed", "unknown"];
const ALL_TASK_STATUSES: TaskStatus[] = ["queued", "running", "review", "completed", "failed", "stopped"];

// ── The regression: one task, one colour, whatever screen you are on ─────────
{
  // Both surfaces now derive from the same function, so this is the property
  // that could not hold before: Tasks said blue, the strip said emerald.
  const running = agentSignalFromTaskStatus("running");
  eq(running.state, "working", "running is working");
  eq(agentStateColor(running.state, C), C.info, "running takes the info slot, not emerald");

  const completed = agentSignalFromTaskStatus("completed");
  eq(completed.state, "verified", "completed is verified");
  eq(agentStateColor(completed.state, C), C.success, "completed is the success slot, not blue");

  // The two the old maps swapped must never collide.
  ok(agentStateColor("working", C) !== agentStateColor("verified", C), "working and verified are distinguishable");
  ok(agentStateColor("failed", C) !== agentStateColor("verified", C), "failed and verified differ");
}

// ── Every colour comes from the token layer, never a literal ─────────────────
{
  const ramp = [C.info, C.success, C.warn, C.error, C.neutral];
  const softRamp = [C.infoBg, C.successBg, C.warnBg, C.errorBg, C.neutralBg];
  for (const state of ALL_STATES) {
    ok(ramp.includes(agentStateColor(state, C)), `${state} resolves to a token, not a literal`);
    ok(softRamp.includes(agentStateBg(state, C)), `${state} has a token soft variant`);
  }
}

/**
 * THE CONTRACT TABLE. Mirrored in web/lib/agentStatus.test.ts.
 *
 * web/ and mobile/ have no shared build, so this table is the only thing binding
 * the two implementations together: edit one mapping and that side's test fails
 * instead of the two quietly drifting apart, which is exactly how the product
 * ended up with four palettes and three meanings for `completed`.
 */
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
    eq(got.state, want.state, `contract: ${status} -> ${want.state}`);
    eq(got.pulse, want.pulse, `contract: ${status} pulse`);
    eq(got.hollow, want.hollow, `contract: ${status} hollow`);
  }
}

// ── Total coverage: no task status may fall through to a default ─────────────
{
  for (const status of ALL_TASK_STATUSES) {
    const signal = agentSignalFromTaskStatus(status);
    ok(ALL_STATES.includes(signal.state), `${status} maps to a real state`);
    ok(signal.label.length > 0, `${status} has a label`);
  }
  // Pulse means "spending right now" — queued has been accepted but is not.
  eq(agentSignalFromTaskStatus("queued").pulse, false, "queued does not pulse");
  eq(agentSignalFromTaskStatus("queued").hollow, true, "queued is hollow — accepted, not started");
  eq(agentSignalFromTaskStatus("running").pulse, true, "running pulses");
  eq(agentSignalFromTaskStatus("running").hollow, false, "running is filled");
}

// ── "Does this need me?" is the question a status light answers ──────────────
{
  eq(agentNeedsYou("blocked"), true, "review/blocked needs you");
  eq(agentNeedsYou("failed"), true, "failed needs you");
  eq(agentNeedsYou("working"), false, "working does not need you");
  eq(agentNeedsYou("healing"), false, "healing is handling itself");
  eq(agentNeedsYou("unknown"), false, "unknown is not a request for action");
  eq(agentIsTerminal("verified"), true, "verified is terminal");
  eq(agentIsTerminal("working"), false, "working is not terminal");
  eq(agentIsTerminal("healing"), false, "healing is not terminal");
}

// ── Autorun: the light must not lie ──────────────────────────────────────────
const NOW = 1_700_000_000_000;
function session(over: Partial<AutorunSession> = {}): AutorunSession {
  return { id: "autorun-1", slot: "widget:codex", task: "/repo/tasks/widget.md", status: "running", ...over };
}

{
  // THE state OpenAI's macropad never needs: a finished loop with no final
  // commit did not finish. The agent's own contract says so.
  const quiet = agentSignalFromAutorun(session({ status: "completed", finalCommit: "" }), NOW);
  eq(quiet.state, "unknown", "completed without a final commit is unknown, never green");
  eq(quiet.hollow, true, "unknown renders hollow — we are not claiming it");

  const real = agentSignalFromAutorun(
    session({ status: "completed", finalCommit: "abc123", finishReason: "converged" }),
    NOW,
  );
  eq(real.state, "verified", "completed WITH a final commit is verified");

  // Lost contact beats an optimistic "running".
  const stale = agentSignalFromAutorun(session({ status: "running" }), NOW, NOW - 60 * 60 * 1000);
  eq(stale.state, "unknown", "a running loop with no contact for an hour is unknown");
  const fresh = agentSignalFromAutorun(session({ status: "running" }), NOW, NOW - 60 * 1000);
  eq(fresh.state, "working", "a loop seen a minute ago is working");
  // A long runner turn is normal — 30min kick timeout — and must not read as death.
  const thinking = agentSignalFromAutorun(session({ status: "running" }), NOW, NOW - 20 * 60 * 1000);
  eq(thinking.state, "working", "a 20-minute runner turn is thinking, not lost");

  const failed = agentSignalFromAutorun(session({ status: "failed", finishReason: "gate failed" }), NOW);
  eq(failed.state, "failed", "failed is failed");
  eq(failed.label, "gate failed", "the finish reason is the label");
}

// ── Healing is current-only, or a loop goes amber forever ────────────────────
{
  const healingNow = agentSignalFromAutorun(
    session({ iterations: 4, heals: [{ iteration: 4, kind: "disk_reclaim", detail: "freed 5GB" }] }),
    NOW,
  );
  eq(healingNow.state, "healing", "a heal in the current iteration is healing");
  eq(healingNow.label, "reclaiming disk", "the heal kind is spelled out for a human");
  eq(agentStateColor("healing", C), C.warn, "healing is amber");

  // Heals accumulate for the life of the run. Treating the array as "is
  // healing" would leave the loop amber long after it recovered.
  const recovered = agentSignalFromAutorun(
    session({ iterations: 9, heals: [{ iteration: 2, kind: "cpu_backoff", detail: "load" }] }),
    NOW,
  );
  eq(recovered.state, "working", "a heal from 7 iterations ago is history, not a state");

  const failover = agentSignalFromAutorun(
    session({ iterations: 3, heals: [{ iteration: 3, kind: "runner_failover", detail: "claude -> codex" }] }),
    NOW,
  );
  eq(failover.label, "switching runner", "failover reads as switching runner");
}

// ── The label names the seats — the whole point of the split ─────────────────
{
  const twoSeat = agentSignalFromAutorun(session({ iterations: 2, master: "claude", activeRunner: "codex" }), NOW);
  ok(twoSeat.label.includes("claude") && twoSeat.label.includes("codex"), "a two-seat loop names both seats");
  const solo = agentSignalFromAutorun(session({ iterations: 2, activeRunner: "codex" }), NOW);
  ok(!solo.label.includes("→"), "a single-runner loop does not invent a master");
}

// ── Slot key: stable address, never position ─────────────────────────────────
{
  eq(slotKeyForAutorun(session()), "widget:codex", "the agent's own slot key wins");
  // Older agents predate the field; fall back rather than collapsing every
  // session onto one slot.
  const legacy = slotKeyForAutorun(session({ slot: "", activeRunner: "codex" }));
  ok(legacy.includes("codex"), "a slotless session still gets a distinct address");
  ok(
    slotKeyForAutorun(session({ slot: "" })) !== slotKeyForAutorun(session({ slot: "", task: "/repo/other.md" })),
    "two tasks never collapse onto one slot",
  );
}

console.log(`\nagentStatus: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
