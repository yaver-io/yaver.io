import type { Color, GameState, Move } from "./types.js";
import { applyMove, direction, ptIdx } from "./backgammon.js";

// ─────────────────────────────────────────────────────────────────────────────
// Legal Move Generation
//
// Backgammon rules enforced:
//  1. If you have checkers on the bar you MUST enter them first
//  2. You must use as many dice as possible
//  3. If only one die can be played, must use the higher one if possible
//  4. You can only bear off when ALL your checkers are in your home board
//  5. Bearing off: exact die or higher die if no checker on that exact point
// ─────────────────────────────────────────────────────────────────────────────

/** Check if a color has checkers on the bar */
export function hasBarCheckers(state: GameState, color: Color): boolean {
  return state.bar[color] > 0;
}

/** Get the entry point for a color entering from the bar with a given die */
export function barEntryPoint(color: Color, die: number): number {
  // White enters black's home board (points 19-24): die=1→24, die=6→19
  // Black enters white's home board (points 1-6):   die=1→1,  die=6→6
  return color === "white" ? 25 - die : die;
}

/** Check if a point is open for a color to land on */
export function isOpenFor(state: GameState, pointNum: number, color: Color): boolean {
  const pt = state.points[ptIdx(pointNum)];
  if (pt.color === null || pt.checkers === 0) return true;
  if (pt.color === color) return true;
  // Opponent's blot (single checker) — can hit
  if (pt.color !== color && pt.checkers === 1) return true;
  return false;
}

/** Check if all of a color's checkers are in their home board (for bearing off) */
export function canBearOff(state: GameState, color: Color): boolean {
  if (state.bar[color] > 0) return false;
  // White home board: points 1-6 (indices 0-5)
  // Black home board: points 19-24 (indices 18-23)
  const nonHomeIndices = color === "white"
    ? Array.from({ length: 18 }, (_, i) => i + 6)   // indices 6-23 = points 7-24
    : Array.from({ length: 18 }, (_, i) => i);        // indices 0-17 = points 1-18

  for (const idx of nonHomeIndices) {
    const pt = state.points[idx];
    if (pt.color === color && pt.checkers > 0) return false;
  }
  return true;
}

/**
 * Get the furthest-back occupied home-board point for a color.
 * White: highest numbered point in 1-6 that has a checker
 * Black: lowest numbered point in 19-24 that has a checker
 */
function furthestCheckerInHome(state: GameState, color: Color): number | null {
  if (color === "white") {
    for (let p = 6; p >= 1; p--) {
      const pt = state.points[ptIdx(p)];
      if (pt.color === color && pt.checkers > 0) return p;
    }
  } else {
    for (let p = 19; p <= 24; p++) {
      const pt = state.points[ptIdx(p)];
      if (pt.color === color && pt.checkers > 0) return p;
    }
  }
  return null;
}

/**
 * Get all legal moves for the current player using a specific die value.
 */
export function getLegalMovesForDie(state: GameState, die: number): Move[] {
  const color = state.currentTurn;
  const moves: Move[] = [];

  // ── Rule 1: Must enter from bar first ─────────────────────────────────────
  if (hasBarCheckers(state, color)) {
    const to = barEntryPoint(color, die);
    if (to >= 1 && to <= 24 && isOpenFor(state, to, color)) {
      moves.push({ from: "bar", to, die });
    }
    return moves;
  }

  // ── Bear off ──────────────────────────────────────────────────────────────
  if (canBearOff(state, color)) {
    if (color === "white") {
      // White: point 1=die 1, point 2=die 2, ... point 6=die 6
      const exactPoint = die;
      if (exactPoint <= 6) {
        const pt = state.points[ptIdx(exactPoint)];
        if (pt.color === color && pt.checkers > 0) {
          moves.push({ from: exactPoint, to: "off", die });
        } else {
          // No exact match — can bear off from furthest checker if die is higher
          const furthest = furthestCheckerInHome(state, color);
          if (furthest !== null && die > furthest) {
            moves.push({ from: furthest, to: "off", die });
          }
        }
      }
    } else {
      // Black: point 24=die 1, point 23=die 2, ... point 19=die 6
      const exactPoint = 25 - die;
      if (exactPoint >= 19) {
        const pt = state.points[ptIdx(exactPoint)];
        if (pt.color === color && pt.checkers > 0) {
          moves.push({ from: exactPoint, to: "off", die });
        } else {
          // No exact match — can bear off from furthest if die is higher
          const furthest = furthestCheckerInHome(state, color);
          if (furthest !== null && die > 25 - furthest) {
            moves.push({ from: furthest, to: "off", die });
          }
        }
      }
    }
  }

  // ── Regular board moves ───────────────────────────────────────────────────
  const dir = direction(color);
  for (let i = 0; i < 24; i++) {
    const pt = state.points[i];
    if (pt.color !== color || pt.checkers === 0) continue;

    const fromPoint = i + 1;
    const toPoint = fromPoint + dir * die;

    if (toPoint < 1 || toPoint > 24) continue;
    if (!isOpenFor(state, toPoint, color)) continue;

    moves.push({ from: fromPoint, to: toPoint, die });
  }

  return moves;
}

/**
 * Recursively determine the maximum value of a dice sequence that can be used.
 * Sequence value = (moves_made * 1000) + sum(dice_values)
 * This enforces playing the maximum number of dice possible, and tie-breaking
 * by forcing the play of the higher die value if only one can be played.
 */
function getMaxSequenceValue(state: GameState): number {
  if (state.dice.length === 0) return 0;

  let maxVal = 0;
  const seen = new Set<number>();

  for (const die of state.dice) {
    if (seen.has(die)) continue;
    seen.add(die);

    const moves = getLegalMovesForDie(state, die);
    for (const move of moves) {
      const newState = applyMove(state, move);
      const subVal = getMaxSequenceValue(newState);
      const pathVal = 1000 + die + subVal;
      if (pathVal > maxVal) {
        maxVal = pathVal;
      }
    }
  }
  return maxVal;
}

export function getAllLegalMoves(state: GameState): Move[] {
  if (state.dice.length === 0) return [];

  const maxVal = getMaxSequenceValue(state);
  if (maxVal === 0) return [];

  const resultSet = new Map<string, Move>();

  for (const die of [...new Set(state.dice)]) {
    const movesForDie = getLegalMovesForDie(state, die);
    for (const move of movesForDie) {
      const newState = applyMove(state, move);
      const furtherVal = getMaxSequenceValue(newState);
      if (1000 + move.die + furtherVal === maxVal) {
        const key = `${move.from}-${move.to}-${move.die}`;
        if (!resultSet.has(key)) {
          resultSet.set(key, move);
        }
      }
    }
  }

  return Array.from(resultSet.values());
}

/** Check if a given color is completely stuck (no legal moves on any die) */
export function isStuck(state: GameState): boolean {
  return getAllLegalMoves(state).length === 0;
}
