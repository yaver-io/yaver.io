import React from "react";
import { motion, AnimatePresence } from "framer-motion";
import type { GameState, Move, Color } from "@game/types";
import Checker, { LegalTargetDot } from "./Checker";

interface PointProps {
  pointNumber: number;       // 1–24
  point: GameState["points"][0];
  isBottom: boolean;         // true = bottom half of board (points 1-12)
  isSelected: boolean;
  isLegalSource: boolean;
  legalTargets: Array<number | "off">;
  selectedPoint: number | "bar" | null;
  onSelectPoint: (p: number) => void;
  onMakeMove: (move: Move) => void;
  legalMoves: Move[];
  currentTurn: Color;
  diceRolled: boolean;
}

export default function Point({
  pointNumber,
  point,
  isBottom,
  isSelected,
  isLegalSource,
  legalTargets,
  selectedPoint,
  onSelectPoint,
  onMakeMove,
  legalMoves,
  currentTurn,
  diceRolled,
}: PointProps) {
  // Only show this point as a legal target if it's reachable from the SELECTED checker specifically.
  // Using legalTargets (global) would show destinations that are valid for OTHER checkers too.
  const isLegalTarget = selectedPoint !== null &&
    legalMoves.some(m => m.from === selectedPoint && m.to === pointNumber);
  const isDark = pointNumber % 2 === 0; // Alternating point colors

  // Colors for the triangle
  const triangleColor = isDark
    ? "rgba(139, 30, 30, 0.85)"   // dark red triangles
    : "rgba(245, 158, 11, 0.75)"; // amber triangles

  const handleClick = (e: React.MouseEvent) => {
    if (isLegalTarget && selectedPoint !== null) {
      // Find the move from selectedPoint to this point
      const move = legalMoves.find(
        m => m.from === selectedPoint && m.to === pointNumber
      );
      if (move) {
        onMakeMove(move);
        return;
      }
    }
    if (point.color === currentTurn && point.checkers > 0 && diceRolled) {
      onSelectPoint(pointNumber);
    }
  };

  // Render all checkers, calculating overlap if > 5
  const checkers = Array.from({ length: point.checkers });
  
  // Calculate overlap negative margin if needed
  const getOverlapStyle = (index: number) => {
    if (index === 0 || point.checkers <= 5) return {};
    // We want N checkers to fit in the space of roughly 5.
    // Total space = 5 units. (N-1) gaps.
    // Overlap factor: (4.5 / (N - 1)) - 1
    const factor = (4.5 / (point.checkers - 1)) - 1; 
    // margin % is relative to the parent width. Since checker is 80% width, 
    // the height of a checker is 80% of parent width.
    const marginPct = `${factor * 80}%`;
    return { [isBottom ? "marginBottom" : "marginTop"]: marginPct };
  };

  return (
    <div
      className="relative flex flex-col items-center cursor-pointer group"
      style={{
        width: "100%",
        height: "100%",
        flexDirection: isBottom ? "column-reverse" : "column",
      }}
      onClick={handleClick}
    >
      {/* Triangle */}
      <div
        className="absolute inset-x-0"
        style={{
          top: isBottom ? "auto" : 0,
          bottom: isBottom ? 0 : "auto",
          height: "88%",
          clipPath: isBottom
            ? "polygon(50% 10%, 15% 100%, 85% 100%)"
            : "polygon(50% 90%, 15% 0%, 85% 0%)",
          background: triangleColor,
          transition: "opacity 0.2s, background-color 0.2s",
          opacity: isSelected ? 0.9 : isLegalSource ? 0.9 : 0.7,
          boxShadow: isLegalTarget ? "0 0 15px rgba(251,191,36,0.5)" : "none",
        }}
      />

      {/* Point number label (moved to extreme edges) */}
      <div
        className="absolute font-mono text-white/20 select-none z-0"
        style={{
          top: isBottom ? "auto" : "100%",
          bottom: isBottom ? "100%" : "auto",
          fontSize: 10,
          marginTop: isBottom ? 0 : 2,
          marginBottom: isBottom ? 2 : 0,
        }}
      >
        {pointNumber}
      </div>

      {/* Legal target highlight (empty point) */}
      <AnimatePresence>
        {isLegalTarget && point.checkers === 0 && (
          <motion.div
            key="target-glow"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            className="absolute inset-0 rounded"
            style={{
              background: "rgba(251,191,36,0.08)",
              border: "1px solid rgba(251,191,36,0.3)",
            }}
          />
        )}
      </AnimatePresence>

      {/* Checkers */}
      <div
        className={`relative flex flex-col items-center z-10 ${point.checkers <= 5 ? "gap-0.5" : ""}`}
        style={{
          flexDirection: isBottom ? "column-reverse" : "column",
          paddingTop: isBottom ? 0 : 4,
          paddingBottom: isBottom ? 4 : 0,
          width: "100%",
          alignItems: "center",
        }}
      >
        {checkers.map((_, i) => (
          <motion.div
            key={i}
            layout
            className="flex items-center justify-center relative"
            style={{ 
              width: "80%", 
              maxWidth: 48,
              zIndex: i, // Ensure overlapping checkers stack properly
              ...getOverlapStyle(i)
            }}
          >
            <Checker
              color={point.color!}
              isSelected={isSelected && i === point.checkers - 1}
              isLegalTarget={isLegalTarget && i === point.checkers - 1}
              onClick={undefined} // click handled by parent
            />
          </motion.div>
        ))}

        {/* Legal target dot (empty point or on top of opponent stack to hit) */}
        <AnimatePresence>
          {isLegalTarget && (
            <LegalTargetDot
              onClick={(e) => {
                e.stopPropagation(); // prevent click bubbling to parent div → double-move
                const move = legalMoves.find(
                  m => m.from === selectedPoint && m.to === pointNumber
                );
                if (move) onMakeMove(move);
              }}
            />
          )}
        </AnimatePresence>
      </div>
    </div>
  );
}
