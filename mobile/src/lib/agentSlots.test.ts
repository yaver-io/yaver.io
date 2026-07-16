/**
 * agentSlots.test.ts — `npx tsx src/lib/agentSlots.test.ts`.
 * No RN, no jest — the tiny assert harness the voice libs use.
 *
 * assignSlots is pure precisely so the rule can be tested without React and
 * shared by every renderer (RN grid, VR arc, widget timeline). What's pinned
 * here is one property: an agent's position never moves for a reason that isn't
 * about that agent. Muscle memory is the entire product; a slot that shuffles is
 * a list with extra steps.
 */
import { DEFAULT_SLOT_COUNT, assignSlots, buildSlots, overflowItems } from "./agentSlots";

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

const empty = new Map<string, number>();

// ── First come, first slot ───────────────────────────────────────────────────
{
  const a = assignSlots(["alpha:claude", "beta:codex"], empty);
  eq(a.get("alpha:claude"), 0, "first agent takes slot 0");
  eq(a.get("beta:codex"), 1, "second agent takes slot 1");
}

// ── THE property: an incumbent never moves ───────────────────────────────────
{
  let assignment = assignSlots(["alpha:claude", "beta:codex", "gamma:glm"], empty);
  const before = new Map(assignment);

  // The agent list arrives in a different order — a poll, a re-sort, a re-render.
  // Order is deliberately ignored, so nothing may move.
  assignment = assignSlots(["gamma:glm", "alpha:claude", "beta:codex"], assignment);
  for (const key of before.keys()) {
    eq(assignment.get(key), before.get(key), `${key} did not move when the list reordered`);
  }

  // A new agent appears. It must take a free slot, not renumber the incumbents —
  // this is exactly what recency sorting used to do.
  assignment = assignSlots(["delta:codex", "gamma:glm", "alpha:claude", "beta:codex"], assignment);
  for (const key of before.keys()) {
    eq(assignment.get(key), before.get(key), `${key} did not move when a new agent appeared`);
  }
  eq(assignment.get("delta:codex"), 3, "the newcomer took the lowest free slot");
}

// ── Vanishing and releasing are different events ─────────────────────────────
{
  // VANISHED — beta is missing from this poll. A dropped frame, a daemon
  // restart, a machine that blinked. Nothing may move, and beta's slot stays
  // its own.
  let assignment = assignSlots(["alpha:claude", "beta:codex", "gamma:glm"], empty);
  const gamma = assignment.get("gamma:glm");
  assignment = assignSlots(["alpha:claude", "gamma:glm"], assignment);
  eq(assignment.get("alpha:claude"), 0, "alpha stayed put when beta vanished");
  eq(assignment.get("gamma:glm"), gamma, "gamma did NOT slide down into beta's slot");

  assignment = assignSlots(["alpha:claude", "gamma:glm", "delta:codex"], assignment);
  eq(assignment.get("delta:codex"), 3, "a newcomer does not squat on a vanished agent's slot while room remains");
  assignment = assignSlots(["alpha:claude", "beta:codex", "gamma:glm", "delta:codex"], assignment);
  eq(assignment.get("beta:codex"), 1, "beta came back to its own slot");

  // RELEASED — the caller says beta is gone for good (useAgentSlots.release
  // drops the key). Only then is the slot up for reuse, so the deck can stay
  // dense without any vanish ever shuffling it.
  const released = new Map(assignment);
  released.delete("beta:codex");
  const after = assignSlots(["alpha:claude", "gamma:glm", "delta:codex", "epsilon:x"], released);
  eq(after.get("epsilon:x"), 1, "a released slot is reused by the next newcomer");
  eq(after.get("alpha:claude"), 0, "releasing one agent moved nobody else");
  eq(after.get("gamma:glm"), gamma, "gamma still has not moved");
}

// ── An agent returning under the same key comes home ─────────────────────────
{
  // This is why the key is (task, seat) and not the run id: autorun restarts
  // mint a new session id every time, and the agent must land where it was.
  //
  // The home slot here is deliberately NOT the lowest free one. An earlier
  // version of this test used slot 0 and passed against an implementation that
  // had no memory at all — the agent "came home" only because home happened to
  // be the first empty chair. Freeing a LOWER slot before the agent returns is
  // what actually distinguishes remembering from guessing.
  let assignment = assignSlots(["a:x", "b:x", "c:x", "widget:codex"], empty);
  const home = assignment.get("widget:codex");
  eq(home, 3, "widget starts at slot 3");

  // widget leaves, and so does b — freeing slot 1, which is lower than home.
  assignment = assignSlots(["a:x", "c:x"], assignment);
  assignment = assignSlots(["a:x", "c:x", "widget:codex"], assignment);
  eq(assignment.get("widget:codex"), home, "an agent that restarted returned to its OWN slot, not the first free one");

  // The reservation is not a permanent squat: a live agent that would otherwise
  // be left off the deck may claim a ghost's slot.
  let full = assignSlots(["p:x", "q:x", "r:x", "s:x", "t:x", "ghost:x"], empty);
  eq(full.get("ghost:x"), 5, "ghost holds the last slot");
  full = assignSlots(["p:x", "q:x", "r:x", "s:x", "t:x"], full); // ghost leaves, still reserved
  full = assignSlots(["p:x", "q:x", "r:x", "s:x", "t:x", "newcomer:x"], full);
  eq(full.get("newcomer:x"), 5, "a live agent takes the ghost's slot rather than going unslotted");
  ok((full.get("ghost:x") ?? -1) < 0, "the evicted ghost no longer holds a slot");
}

// ── A full deck never evicts someone you are watching ────────────────────────
{
  const keys = ["a:x", "b:x", "c:x", "d:x", "e:x", "f:x"];
  let assignment = assignSlots(keys, empty);
  const before = new Map(assignment);
  assignment = assignSlots([...keys, "overflow:x"], assignment);
  for (const key of keys) {
    eq(assignment.get(key), before.get(key), `${key} kept its slot when the deck overflowed`);
  }
  eq(assignment.get("overflow:x"), -1, "the extra agent gets no slot rather than bumping an incumbent");

  const items = [...keys, "overflow:x"].map((key) => ({ key }));
  const over = overflowItems(items, (i) => i.key, assignment);
  eq(over.length, 1, "the overflowing agent is still reported, not dropped");
  eq(over[0].key, "overflow:x", "overflow names the right agent");
}

// ── Empty slots are rendered, not omitted ────────────────────────────────────
{
  // A macropad has six keys whether or not six agents exist — the unlit key is
  // what makes the lit one findable. Collapsing empties reintroduces moving
  // positions for free.
  const items = [{ key: "alpha:claude" }, { key: "beta:codex" }];
  const assignment = assignSlots(
    items.map((i) => i.key),
    empty,
  );
  const slots = buildSlots(items, (i) => i.key, assignment);
  eq(slots.length, DEFAULT_SLOT_COUNT, "the deck is always its full size");
  eq(slots[0].item?.key, "alpha:claude", "slot 0 holds alpha");
  eq(slots[2].item, null, "slot 2 is empty, and present");
  eq(slots[0].ordinal, 1, "ordinal is 1-based — what a human presses");
  eq(slots[5].ordinal, 6, "the last key is 6");
  ok(
    slots.every((s, i) => s.index === i),
    "index always equals position",
  );
}

// ── An agent with no slot is not rendered into one ───────────────────────────
{
  const items = [{ key: "a:x" }, { key: "b:x" }];
  const assignment = new Map([
    ["a:x", 0],
    ["b:x", -1],
  ]);
  const slots = buildSlots(items, (i) => i.key, assignment);
  eq(slots[0].item?.key, "a:x", "the slotted agent renders");
  ok(
    slots.slice(1).every((s) => s.item === null),
    "the unslotted agent does not leak into another slot",
  );
}

console.log(`\nagentSlots: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
