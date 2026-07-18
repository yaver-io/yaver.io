/**
 * Fixed slots: an agent's position, held still.
 *
 * MIRROR of mobile/src/lib/agentSlots.ts — kept byte-identical below this
 * header, exactly like web/lib/agentStatus.ts mirrors its mobile twin. The
 * module is pure React (no React Native), but web cannot import across the
 * surface boundary: Turbopack resolves only within web/, so the direct
 * `../../../../mobile/src/lib/agentSlots` import typechecked under tsc and
 * then failed the Cloudflare build with "Module not found".
 *
 * Change the mobile copy and this one together; the tests live next to the
 * mobile source (`npx tsx mobile/src/lib/agentSlots.test.ts`).
 *
 * WHY: position was a function of time everywhere. The agent sorted sessions by
 * StartedAt (fixed now — see sortAutorunViewsBySlot), the VR arc picks its panes
 * with `sorted.slice(0, 3)` so a status change re-sorts and moves a pane through
 * space, and the Home strip drops a chip 120s after it finishes. In all three an
 * agent's home moves when something ELSE changes.
 *
 * That defeats the entire point. A colour-coded surface is only glanceable if
 * you know where to look before you look — the value is muscle memory, and
 * muscle memory cannot form against a list that renumbers. A macropad key does
 * not move because someone else's build went green.
 *
 * The rule here is: first come, first slot, and a slot is never renumbered
 * because something else changed.
 *
 * VANISHING and RELEASING are deliberately different events, and conflating
 * them is what breaks this:
 *
 *   - Vanished (missing from a poll — a dropped frame, a daemon restart, a
 *     machine that blinked) keeps its slot reserved. The agent comes back HOME.
 *     This is the case slot keys exist for: an autorun restart mints a new
 *     session id but the same task:seat, and a user who learned "the migration
 *     is key 4" must still find it on key 4.
 *   - Released (the caller says it's gone for good) frees the slot for reuse,
 *     which is the only way the deck gets dense again.
 *
 * A reservation still yields to a live agent that would otherwise be left off
 * the deck — a ghost never costs a real agent its place.
 */

import { useCallback, useMemo, useRef, useState } from "react";

/** A slot assignment: stable key -> fixed index, plus the display ordinal. */
export interface Slot<T> {
  /** Stable address — see slotKeyForAutorun / slotKeyForTask. */
  key: string;
  /** 0-based fixed position. Never changes while the agent lives. */
  index: number;
  /**
   * 1-based, what a human presses: key 1..6, Cmd-1..6, the first pane in the VR
   * arc. Exists so surfaces don't each re-derive it and drift by one.
   */
  ordinal: number;
  /** Null when the slot is held but the agent is currently absent. */
  item: T | null;
}

/** The macropad's six. Not a hard cap — the natural size of a glance. */
export const DEFAULT_SLOT_COUNT = 6;

/**
 * Assigns stable slots to keyed items.
 *
 * Pure so every surface — RN, the VR arc, a widget's timeline — can share the
 * assignment rule without dragging React in. Feed it the previous assignment and
 * it returns the next one, changing as little as it possibly can.
 *
 * @param keys    Present agents, in whatever order the caller has. Order is
 *                deliberately ignored: honouring it is what makes slots move.
 * @param previous Existing key -> index. Kept exactly, including for keys not
 *                in `keys` — those are reservations. Drop a key to release it.
 * @param max     Slots available. Extra agents get no slot (index -1) rather
 *                than evicting an agent the user is already watching.
 */
export function assignSlots(
  keys: string[],
  previous: ReadonlyMap<string, number>,
  max: number = DEFAULT_SLOT_COUNT,
): Map<string, number> {
  const next = new Map<string, number>();
  const taken = new Set<number>();
  const present = new Set(keys);

  // Incumbents keep their index — this is the whole contract.
  for (const key of keys) {
    const held = previous.get(key);
    if (held !== undefined && held >= 0 && held < max && !taken.has(held)) {
      next.set(key, held);
      taken.add(held);
    }
  }

  // An agent that is currently absent KEEPS its slot reserved, so that when it
  // comes back it comes back HOME. This is the case slot keys exist for: an
  // autorun restart mints a new session id but the same task:seat, and a user
  // who has learned "the migration is key 4" must still find it on key 4.
  //
  // Reservations yield to a live agent that would otherwise go unslotted —
  // a ghost must never cost a real agent its place on the deck.
  const reserved = new Map<number, string>();
  for (const [key, index] of previous) {
    if (present.has(key) || index < 0 || index >= max || taken.has(index)) continue;
    reserved.set(index, key);
  }

  for (const key of keys) {
    if (next.has(key)) continue;
    const free = lowestFree(max, taken, reserved);
    // No room at all: -1 means "real, but not on the deck". Never bump a
    // present agent — a slot changing owner under the user is worse than an
    // agent not being shown.
    next.set(key, free);
    if (free >= 0) {
      taken.add(free);
      reserved.delete(free);
    }
  }

  // Carry surviving reservations forward. Bounded by max, so this cannot grow.
  for (const [index, key] of reserved) next.set(key, index);

  return next;
}

/**
 * Lowest slot not held by a present agent, preferring one nobody has reserved.
 * Falls back to evicting the lowest reservation only when the alternative is
 * leaving a live agent off the deck entirely.
 */
function lowestFree(max: number, taken: ReadonlySet<number>, reserved: ReadonlyMap<number, string>): number {
  for (let i = 0; i < max; i++) {
    if (!taken.has(i) && !reserved.has(i)) return i;
  }
  for (let i = 0; i < max; i++) {
    if (!taken.has(i)) return i;
  }
  return -1;
}

/**
 * Builds the full slot list, including empty ones.
 *
 * Empty slots are RETURNED, not omitted. A macropad has six keys whether or not
 * six agents exist; the unlit key is what makes the lit one findable. Surfaces
 * that collapse empties reintroduce the moving-position bug for free.
 */
export function buildSlots<T>(
  items: T[],
  keyOf: (item: T) => string,
  assignment: ReadonlyMap<string, number>,
  max: number = DEFAULT_SLOT_COUNT,
): Slot<T>[] {
  const byIndex = new Map<number, T>();
  for (const item of items) {
    const index = assignment.get(keyOf(item));
    if (index !== undefined && index >= 0) byIndex.set(index, item);
  }
  const slots: Slot<T>[] = [];
  for (let i = 0; i < max; i++) {
    const item = byIndex.get(i) ?? null;
    slots.push({
      key: item ? keyOf(item) : `empty:${i}`,
      index: i,
      ordinal: i + 1,
      item,
    });
  }
  return slots;
}

/** Agents that are real but have no slot — the deck is full. */
export function overflowItems<T>(items: T[], keyOf: (item: T) => string, assignment: ReadonlyMap<string, number>): T[] {
  return items.filter((item) => (assignment.get(keyOf(item)) ?? -1) < 0);
}

/**
 * React binding. Holds the assignment across renders and polls.
 *
 * Deliberately does NOT drop an agent's slot when it finishes: a completed agent
 * keeps its key and goes green/idle in place, and even a vanished one keeps its
 * slot until `release` is called. The Home strip evicting a chip 120s after
 * completion is exactly the behaviour this replaces — a slot that empties itself
 * on a timer is not a slot.
 *
 * `release(key)` is the deliberate "this agent is gone" signal, and the only
 * thing that frees a slot for reuse.
 */
export function useAgentSlots<T>(
  items: T[],
  keyOf: (item: T) => string,
  max: number = DEFAULT_SLOT_COUNT,
): { slots: Slot<T>[]; overflow: T[]; release: (key: string) => void } {
  const [assignment, setAssignment] = useState<ReadonlyMap<string, number>>(() => new Map());
  // Read the live assignment during render without making it a dep of the memo
  // below — the memo must recompute on items, not on its own output.
  const ref = useRef(assignment);
  ref.current = assignment;

  const next = useMemo(() => assignSlots(items.map(keyOf), ref.current, max), [items, keyOf, max]);

  // Persist only on a real change; assignSlots is stable for stable input, so
  // this settles after one pass instead of looping.
  if (!sameAssignment(ref.current, next)) {
    ref.current = next;
    // Schedule out of render — setState during render of the same component is
    // allowed by React, but comparing first keeps it to one extra pass.
    queueMicrotask(() => setAssignment(next));
  }

  const release = useCallback((key: string) => {
    setAssignment((prev) => {
      if (!prev.has(key)) return prev;
      const copy = new Map(prev);
      copy.delete(key);
      return copy;
    });
  }, []);

  const slots = useMemo(() => buildSlots(items, keyOf, next, max), [items, keyOf, next, max]);
  const overflow = useMemo(() => overflowItems(items, keyOf, next), [items, keyOf, next]);
  return { slots, overflow, release };
}

function sameAssignment(a: ReadonlyMap<string, number>, b: ReadonlyMap<string, number>): boolean {
  if (a.size !== b.size) return false;
  for (const [key, index] of a) {
    if (b.get(key) !== index) return false;
  }
  return true;
}
