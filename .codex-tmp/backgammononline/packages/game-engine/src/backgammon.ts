import type { Color, GameState, Move, Point, WinResult } from "./types.js";

// ─────────────────────────────────────────────────────────────────────────────
// Standard backgammon starting position
// Points are 1-indexed (index 0 = point 1, index 23 = point 24)
// White moves from high points → low points (24→1), bears off below point 1
// Black moves from low points → high points (1→24), bears off above point 24
// ─────────────────────────────────────────────────────────────────────────────
const INITIAL_POINTS: Point[] = (() => {
  const pts: Point[] = Array.from({ length: 24 }, () => ({ checkers: 0, color: null }));

  // Standard backgammon starting position
  // White checkers
  pts[23] = { checkers: 2, color: "white" }; // point 24
  pts[12] = { checkers: 5, color: "white" }; // point 13
  pts[7]  = { checkers: 3, color: "white" }; // point 8
  pts[5]  = { checkers: 5, color: "white" }; // point 6

  // Black checkers
  pts[0]  = { checkers: 2, color: "black" }; // point 1
  pts[11] = { checkers: 5, color: "black" }; // point 12
  pts[16] = { checkers: 3, color: "black" }; // point 17
  pts[18] = { checkers: 5, color: "black" }; // point 19

  return pts;
})();

/** Create a fresh game state */
export function initGame(mode: GameState["mode"] = "local", aiColor: Color | null = "black"): GameState {
  return {
    points: INITIAL_POINTS.map(p => ({ ...p })),
    bar: { white: 0, black: 0 },
    borneOff: { white: 0, black: 0 },
    currentTurn: "white", // white goes first conventionally
    dice: [],
    initialDice: [],
    diceRolled: false,
    doublingCubeValue: 1,
    doublingCubeOwner: null,
    status: "pregame",
    winner: null,
    winType: null,
    moveHistory: [],
    mode,
    ruleVariant: "standard",
    aiColor: mode === "vs-ai" ? aiColor : null,
  };
}

/** Roll two dice and expand doubles into 4 identical values */
export function rollDice(): number[] {
  const d1 = Math.ceil(Math.random() * 6);
  const d2 = Math.ceil(Math.random() * 6);
  return d1 === d2 ? [d1, d1, d1, d1] : [d1, d2];
}

/** Get point index (0-based) from point number (1-based) */
export function ptIdx(n: number): number {
  return n - 1;
}

/** Get point number (1-based) from index (0-based) */
export function ptNum(idx: number): number {
  return idx + 1;
}

/** Direction a color moves: white moves -1 (24→1), black moves +1 (1→24) */
export function direction(color: Color): 1 | -1 {
  return color === "white" ? -1 : 1;
}

/**
 * Apply a move to the game state immutably.
 * This handles:
 *  - Moving from bar
 *  - Regular moves
 *  - Hitting a blot (opponent single checker → bar)
 *  - Bearing off
 *  - Consuming the used die value
 */
export function applyMove(state: GameState, move: Move): GameState {
  const newState: GameState = deepCloneState(state);
  const color = newState.currentTurn;
  const opp: Color = color === "white" ? "black" : "white";

  // ── Remove checker from source ────────────────────────────────────────────
  if (move.from === "bar") {
    newState.bar[color] -= 1;
  } else {
    const fromPt = newState.points[ptIdx(move.from)];
    fromPt.checkers -= 1;
    if (fromPt.checkers === 0) fromPt.color = null;
  }

  // ── Place checker at destination ──────────────────────────────────────────
  if (move.to === "off") {
    newState.borneOff[color] += 1;
  } else {
    const toPt = newState.points[ptIdx(move.to)];

    // Hit opponent blot
    if (toPt.color === opp && toPt.checkers === 1) {
      toPt.checkers = 0;
      toPt.color = null;
      newState.bar[opp] += 1;
      move.hitOpponent = true;
    }

    toPt.checkers += 1;
    toPt.color = color;
  }

  // ── Consume the die ───────────────────────────────────────────────────────
  const dieIdx = newState.dice.indexOf(move.die);
  if (dieIdx !== -1) newState.dice.splice(dieIdx, 1);

  // ── Record in history ─────────────────────────────────────────────────────
  newState.moveHistory.push(move);

  return newState;
}

/**
 * Undo the last move in the game state.
 * Returns the state as it was before the last move was applied.
 */
export function undoMove(state: GameState): GameState {
  if (state.moveHistory.length === 0) return state;

  const newState = deepCloneState(state);
  const move = newState.moveHistory.pop()!;
  const color = newState.currentTurn;
  const opp: Color = color === "white" ? "black" : "white";

  // 1. Restore the die
  newState.dice.push(move.die);
  newState.dice.sort((a, b) => b - a);

  // 2. Remove checker from destination
  if (move.to === "off") {
    newState.borneOff[color] -= 1;
  } else {
    const toPt = newState.points[ptIdx(move.to)];
    toPt.checkers -= 1;
    if (toPt.checkers === 0) toPt.color = null;

    // Restore hit opponent
    if (move.hitOpponent) {
      toPt.checkers = 1;
      toPt.color = opp;
      newState.bar[opp] -= 1;
    }
  }

  // 3. Place checker back at source
  if (move.from === "bar") {
    newState.bar[color] += 1;
  } else {
    const fromPt = newState.points[ptIdx(move.from)];
    fromPt.checkers += 1;
    fromPt.color = color;
  }

  // 4. Restore playing status in case this move had triggered a win
  newState.status = "playing";
  newState.winner = null;
  newState.winType = null;

  return newState;
}

/**
 * End the current player's turn (switch sides, reset dice).
 */
export function endTurn(state: GameState): GameState {
  const newState = deepCloneState(state);
  newState.currentTurn = newState.currentTurn === "white" ? "black" : "white";
  newState.dice = [];
  newState.initialDice = [];
  newState.diceRolled = false;
  newState.status = "playing";
  return newState;
}

/**
 * Start the turn with a roll of the dice.
 */
export function startTurnWithRoll(state: GameState): GameState {
  const newState = deepCloneState(state);
  const rolled = rollDice();
  newState.dice = rolled;
  newState.initialDice = [...rolled];
  newState.diceRolled = true;
  newState.status = "playing";
  return newState;
}

/**
 * Check if the game has been won. Returns WinResult or null.
 *
 * Win types:
 *  - Normal: opponent has borne off ≥ 1 checker (or started bearing off)
 *  - Gammon: opponent has borne off 0 checkers
 *  - Backgammon: opponent has borne off 0 AND has checkers on bar or in winner's home board
 */
export function detectWin(state: GameState): WinResult | null {
  for (const color of ["white", "black"] as Color[]) {
    if (state.borneOff[color] === 15) {
      const opp: Color = color === "white" ? "black" : "white";
      const oppBorneOff = state.borneOff[opp];

      if (oppBorneOff > 0) {
        return { winner: color, type: "normal" };
      }

      // Gammon / backgammon check
      const oppOnBar = state.bar[opp] > 0;
      const oppInWinnersHome = isInWinnersHome(state, opp, color);

      if (oppOnBar || oppInWinnersHome) {
        return { winner: color, type: "backgammon" };
      }

      return { winner: color, type: "gammon" };
    }
  }
  return null;
}

/** Check if any of opp's checkers are in winner's home board (points 1-6 for white winner, 19-24 for black winner) */
function isInWinnersHome(state: GameState, opp: Color, winner: Color): boolean {
  // White's home board is points 1-6 (indices 0-5)
  // Black's home board is points 19-24 (indices 18-23)
  const homeIndices = winner === "white"
    ? [0, 1, 2, 3, 4, 5]
    : [18, 19, 20, 21, 22, 23];

  return homeIndices.some(i => state.points[i].color === opp && state.points[i].checkers > 0);
}

/** Deep clone game state (fast, structured) */
export function deepCloneState(state: GameState): GameState {
  return {
    ...state,
    points: state.points.map(p => ({ ...p })),
    bar: { ...state.bar },
    borneOff: { ...state.borneOff },
    dice: [...state.dice],
    initialDice: [...state.initialDice],
    moveHistory: [...state.moveHistory],
  };
}

/** Calculate pip count for a color (lower = better position) */
export function pipCount(state: GameState, color: Color): number {
  let count = 0;
  const dir = color === "white" ? 1 : -1; // distance to bear-off

  state.points.forEach((pt, idx) => {
    if (pt.color === color && pt.checkers > 0) {
      // Distance for white = point number (1-24, 1 is closest)
      // Distance for black = 25 - point number
      const pointNum = idx + 1;
      const dist = color === "white" ? pointNum : 25 - pointNum;
      count += dist * pt.checkers;
    }
  });

  // Bar checkers have maximum distance
  count += state.bar[color] * (color === "white" ? 25 : 25);

  return count;
}
