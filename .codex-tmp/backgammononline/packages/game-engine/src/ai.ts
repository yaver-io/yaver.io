import type { Color, GameState, Move } from "./types.js";
import { applyMove, deepCloneState, pipCount } from "./backgammon.js";
import { getAllLegalMoves, canBearOff } from "./legalMoves.js";

// ─────────────────────────────────────────────────────────────────────────────
// Heuristic AI — evaluates positions and picks the best sequence of moves
//
// Heuristics (weighted):
//  1. Minimize pip count (primary)
//  2. Hit opponent blots (attack)
//  3. Build prime / block opponent (make points)
//  4. Protect blots (avoid leaving single checkers exposed)
//  5. Bear off aggressively when possible
// ─────────────────────────────────────────────────────────────────────────────

const WEIGHTS = {
  pipDiff: 1.0,       // pip count advantage
  hit: 8.0,           // reward for hitting a blot
  makePoint: 6.0,     // reward for making a point (stacking 2+)
  blotPenalty: 4.0,   // penalty for leaving a blot exposed
  primeBonus: 5.0,    // bonus per consecutive point in a prime
  bearOff: 10.0,      // reward for bearing off a checker
};

/** Evaluate a game state from the AI's perspective (higher = better for AI) */
function evaluateState(state: GameState, aiColor: Color): number {
  const opp: Color = aiColor === "white" ? "black" : "white";
  let score = 0;

  // 1. Pip count difference
  const aiPips = pipCount(state, aiColor);
  const oppPips = pipCount(state, opp);
  score += (oppPips - aiPips) * WEIGHTS.pipDiff;

  // 2. Blots — penalise AI blots, reward opponent blots (potential hits)
  for (let i = 0; i < 24; i++) {
    const pt = state.points[i];
    if (pt.checkers === 1) {
      if (pt.color === aiColor) score -= WEIGHTS.blotPenalty;
      else if (pt.color === opp) score += WEIGHTS.blotPenalty * 0.5; // opponent blot = potential hit
    }
  }

  // 3. Points made (2+ checkers on a point)
  let prime = 0;
  let maxPrime = 0;
  for (let i = 0; i < 24; i++) {
    const pt = state.points[i];
    if (pt.color === aiColor && pt.checkers >= 2) {
      score += WEIGHTS.makePoint;
      prime++;
      maxPrime = Math.max(maxPrime, prime);
    } else {
      prime = 0;
    }
  }
  // Bonus for consecutive primes
  score += maxPrime * WEIGHTS.primeBonus;

  // 4. Bear-off progress
  score += state.borneOff[aiColor] * WEIGHTS.bearOff;
  score -= state.borneOff[opp] * WEIGHTS.bearOff;

  // 5. Bar penalty — opponent on bar is huge advantage for AI
  score += state.bar[opp] * 15;
  score -= state.bar[aiColor] * 15;

  return score;
}

/** Try all legal move sequences (up to depth of remaining dice) and return the best */
function bestMoveSequence(
  state: GameState,
  aiColor: Color,
  depth: number = 0,
): { moves: Move[]; score: number } {
  const legalMoves = getAllLegalMoves(state);

  if (legalMoves.length === 0 || depth > 6) {
    return { moves: [], score: evaluateState(state, aiColor) };
  }

  let best: { moves: Move[]; score: number } = { moves: [], score: -Infinity };

  // Deduplicate moves by from-to pair
  const seen = new Set<string>();

  for (const move of legalMoves) {
    const key = `${move.from}-${move.to}`;
    if (seen.has(key)) continue;
    seen.add(key);

    const newState = applyMove(state, move);
    const sub = bestMoveSequence(newState, aiColor, depth + 1);
    const totalScore = sub.score;

    if (totalScore > best.score) {
      best = { moves: [move, ...sub.moves], score: totalScore };
    }
  }

  return best;
}

/**
 * Get the best move sequence for the AI.
 * Returns an ordered array of Move objects to be applied one by one.
 */
export function getBestMoves(state: GameState): Move[] {
  if (!state.aiColor) return [];
  const result = bestMoveSequence(deepCloneState(state), state.aiColor);
  return result.moves;
}

/** Check if the current turn belongs to the AI */
export function isAITurn(state: GameState): boolean {
  return state.mode === "vs-ai" && state.aiColor === state.currentTurn;
}
