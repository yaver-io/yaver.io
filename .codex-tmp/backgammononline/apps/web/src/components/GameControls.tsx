import { motion } from "framer-motion";
import type { Color, GameState } from "@game/types";
import { getAllLegalMoves } from "@game/legalMoves";

interface GameControlsProps {
  state: GameState;
  isAI: boolean;
  onRoll: () => void;
  onEndTurn: () => void;
  onUndo: () => void;
  onNewGame: () => void;
  vertical?: boolean;
}

export default function GameControls({
  state,
  isAI,
  onRoll,
  onEndTurn,
  onUndo,
  onNewGame,
  vertical = false,
}: GameControlsProps) {
  const { diceRolled, dice, initialDice, status } = state;
  const canRoll = !diceRolled && status === "playing" && !isAI;
  const hasNoMovesLeft = diceRolled && getAllLegalMoves(state).length === 0 && dice.length > 0;
  const canEndTurn = diceRolled && (dice.length === 0 || hasNoMovesLeft) && status === "playing" && !isAI;
  // Undo is available when player has made at least one move this turn
  const canUndo = diceRolled && dice.length < initialDice.length && status === "playing" && !isAI;

  return (
    <div className={`flex gap-3 w-full justify-center ${vertical ? "flex-col items-stretch" : "flex-wrap items-center"}`}>
      {canRoll && (
        <motion.button
          onClick={onRoll}
          whileHover={{ scale: 1.05, y: -2 }}
          whileTap={{ scale: 0.95 }}
          initial={{ opacity: 0, scale: 0.8 }}
          animate={{ opacity: 1, scale: 1 }}
          className="px-6 py-4 rounded-xl font-bold text-sm tracking-widest uppercase w-full shadow-lg"
          style={{
            background: "linear-gradient(135deg, #fbbf24 0%, #f59e0b 100%)",
            color: "#1a0f05",
            boxShadow: "0 4px 20px rgba(251,191,36,0.3)",
          }}
        >
          Roll Dice
        </motion.button>
      )}

      {canUndo && (
        <motion.button
          onClick={onUndo}
          whileHover={{ scale: 1.05, y: -2 }}
          whileTap={{ scale: 0.95 }}
          initial={{ opacity: 0, scale: 0.8 }}
          animate={{ opacity: 1, scale: 1 }}
          className="px-6 py-4 rounded-xl font-bold text-sm tracking-widest uppercase w-full"
          style={{
            background: "rgba(255,255,255,0.06)",
            color: "rgba(232,217,188,0.7)",
            border: "1px solid rgba(255,255,255,0.12)",
          }}
        >
          ↩ Undo Move
        </motion.button>
      )}

      {canEndTurn && (
        <motion.button
          onClick={onEndTurn}
          whileHover={{ scale: 1.05, y: -2 }}
          whileTap={{ scale: 0.95 }}
          initial={{ opacity: 0, scale: 0.8 }}
          animate={{ opacity: 1, scale: 1 }}
          className="px-6 py-4 rounded-xl font-bold text-sm tracking-widest uppercase w-full glass border border-white/10"
          style={{ color: "#e8d9bc" }}
        >
          End Turn
        </motion.button>
      )}

      <motion.button
        onClick={onNewGame}
        whileHover={{ scale: 1.05, y: -2 }}
        whileTap={{ scale: 0.95 }}
        className="px-4 py-3 rounded-xl text-xs font-semibold tracking-widest uppercase mt-4 w-full"
        style={{
          background: "rgba(255,255,255,0.05)",
          color: "rgba(255,255,255,0.4)",
          border: "1px solid rgba(255,255,255,0.08)",
        }}
      >
        New Game
      </motion.button>
    </div>
  );
}
