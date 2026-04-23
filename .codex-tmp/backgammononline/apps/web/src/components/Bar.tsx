import { motion, AnimatePresence } from "framer-motion";
import type { Color, GameState, Move } from "@game/types";
import Checker, { LegalTargetDot } from "./Checker";

interface BarProps {
  bar: GameState["bar"];
  currentTurn: Color;
  legalTargets: Array<number | "off">;
  legalMoves: Move[];
  selectedPoint: number | "bar" | null;
  onSelectBar: () => void;
  onMakeMove: (move: Move) => void;
  diceRolled: boolean;
}

export default function Bar({
  bar,
  currentTurn,
  legalTargets,
  legalMoves,
  selectedPoint,
  onSelectBar,
  onMakeMove,
  diceRolled,
}: BarProps) {
  const hasWhite = bar.white > 0;
  const hasBlack = bar.black > 0;
  const isSelected = selectedPoint === "bar";

  // Legal targets from bar
  const barLegalTargets = selectedPoint === "bar" ? legalTargets : [];

  return (
    <div
      className="flex flex-col items-center justify-between h-full py-4 gap-2"
      style={{ width: "100%", background: "rgba(10, 5, 2, 0.8)" }}
    >
      {/* Black checkers on bar (top half) */}
      <div className="flex flex-col items-center gap-1">
        {hasBlack && Array.from({ length: Math.min(bar.black, 4) }).map((_, i) => (
          <motion.div
            key={`bar-black-${i}`}
            layout
            initial={{ scale: 0, rotate: -20 }}
            animate={{ scale: 1, rotate: 0 }}
            exit={{ scale: 0 }}
            transition={{ type: "spring", stiffness: 300, damping: 20, delay: i * 0.05 }}
          >
            <Checker
              color="black"
              count={i === 0 && bar.black > 4 ? bar.black : undefined}
              isSelected={isSelected && currentTurn === "black"}
              onClick={
                currentTurn === "black" && diceRolled
                  ? onSelectBar
                  : undefined
              }
              size={38}
            />
          </motion.div>
        ))}
        {hasBlack && currentTurn === "black" && isSelected && (
          <AnimatePresence>
            {barLegalTargets.map((t, i) => (
              <LegalTargetDot
                key={i}
                onClick={() => {
                  const move = legalMoves.find(m => m.from === "bar" && m.to === t);
                  if (move) onMakeMove(move);
                }}
              />
            ))}
          </AnimatePresence>
        )}
      </div>

      {/* Bar divider label */}
      <div className="text-center select-none">
        <span
          className="text-xs font-mono tracking-widest"
          style={{ color: "rgba(251,191,36,0.3)", writingMode: "vertical-rl" }}
        >
          BAR
        </span>
      </div>

      {/* White checkers on bar (bottom half) */}
      <div className="flex flex-col-reverse items-center gap-1">
        {hasWhite && Array.from({ length: Math.min(bar.white, 4) }).map((_, i) => (
          <motion.div
            key={`bar-white-${i}`}
            layout
            initial={{ scale: 0, rotate: 20 }}
            animate={{ scale: 1, rotate: 0 }}
            exit={{ scale: 0 }}
            transition={{ type: "spring", stiffness: 300, damping: 20, delay: i * 0.05 }}
          >
            <Checker
              color="white"
              count={i === 0 && bar.white > 4 ? bar.white : undefined}
              isSelected={isSelected && currentTurn === "white"}
              onClick={
                currentTurn === "white" && diceRolled
                  ? onSelectBar
                  : undefined
              }
              size={38}
            />
          </motion.div>
        ))}
        {hasWhite && currentTurn === "white" && isSelected && (
          <AnimatePresence>
            {barLegalTargets.map((t, i) => (
              <LegalTargetDot
                key={i}
                onClick={() => {
                  const move = legalMoves.find(m => m.from === "bar" && m.to === t);
                  if (move) onMakeMove(move);
                }}
              />
            ))}
          </AnimatePresence>
        )}
      </div>
    </div>
  );
}
