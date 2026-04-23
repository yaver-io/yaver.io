// ─────────────────────────────────────────────────────────────────────────────
// Core Types for the Backgammon Game Engine
// ─────────────────────────────────────────────────────────────────────────────

/** The two player colors */
export type Color = "white" | "black";

/** Represents a single point (triangle) on the board.
 *  Points are numbered 1–24 from White's perspective:
 *    - White bears off past point 0 (below point 1)
 *    - Black bears off past point 25 (above point 24)
 *    - White's home board: points 1–6
 *    - Black's home board: points 19–24
 */
export interface Point {
  checkers: number; // count of checkers on this point
  color: Color | null; // which color occupies it (null if empty)
}

/** Represents checkers held on the bar */
export interface Bar {
  white: number;
  black: number;
}

/** Represents checkers that have borne off */
export interface BorneOff {
  white: number;
  black: number;
}

/** A dice roll — 2 dice, doubles get 4 moves */
export type DiceValues = [number, number] | [number, number, number, number];

/** A single legal move a player can make */
export interface Move {
  from: number | "bar"; // 1–24 or "bar"
  to: number | "off";  // 1–24 or "off" (bearing off)
  die: number;         // which die value was used
  hitOpponent?: boolean; // true if this move hit an opponent's blot
}

/** The complete game state — everything needed to reconstruct the board */
export interface GameState {
  /** Points 1–24 (index 0 = point 1, index 23 = point 24) */
  points: Point[];

  /** Checkers on the bar */
  bar: Bar;

  /** Checkers that have borne off */
  borneOff: BorneOff;

  /** Whose turn it is */
  currentTurn: Color;

  /** Current dice values available for moves (consumed as moves are made) */
  dice: number[];

  /** The dice originally rolled at the start of the turn (used for UI rendering of exhausted dice) */
  initialDice: number[];

  /** Whether dice have been rolled this turn */
  diceRolled: boolean;

  /** Doubling cube value: 1, 2, 4, 8, 16, 32, 64 */
  doublingCubeValue: number;

  /** Who owns the doubling cube (null = centered, available to both) */
  doublingCubeOwner: Color | null;

  /** Game status */
  status: "pregame" | "playing" | "finished";

  /** Winner (if status === 'finished') */
  winner: Color | null;

  /** Win type */
  winType: "normal" | "gammon" | "backgammon" | null;

  /** Move history for undo */
  moveHistory: Move[];

  /** Game mode */
  mode: "local" | "vs-ai" | "online";

  /** Rule variant */
  ruleVariant: "standard" | "hardcore" | "casual";

  /** If vs-ai, which color does the AI play */
  aiColor: Color | null;
}

/** Result of a win check */
export interface WinResult {
  winner: Color;
  type: "normal" | "gammon" | "backgammon";
}
