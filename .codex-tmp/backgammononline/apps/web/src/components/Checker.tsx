import React from "react";
import { motion, AnimatePresence } from "framer-motion";
import type { Color } from "@game/types";

interface CheckerProps {
  color: Color;
  count?: number; // if stacked, show count badge
  isSelected?: boolean;
  isLegalTarget?: boolean;
  onClick?: () => void;
}

export default function Checker({
  color,
  count,
  isSelected,
  isLegalTarget,
  onClick,
}: CheckerProps) {
  const isWhite = color === "white";

  return (
    <motion.div
      layout
      onClick={onClick}
      whileHover={onClick ? { scale: 1.08, y: -2 } : undefined}
      whileTap={onClick ? { scale: 0.95 } : undefined}
      animate={
        isSelected
          ? { scale: 1.1, y: -4 }
          : { scale: 1, y: 0 }
      }
      transition={{ type: "spring", stiffness: 400, damping: 25 }}
      className={`
        relative flex items-center justify-center rounded-full no-select
        ${isWhite ? "checker-white" : "checker-black"}
        ${isSelected ? "checker-selected" : ""}
        ${onClick ? "cursor-pointer" : "cursor-default"}
      `}
      style={{
        width: "100%",
        aspectRatio: "1 / 1",
        flexShrink: 0,
        boxShadow: isWhite
          ? "inset 0 4px 6px -2px rgba(255,255,255,0.7), inset 0 -4px 6px -2px rgba(0,0,0,0.15), 0 3px 5px rgba(0,0,0,0.4)"
          : "inset 0 4px 6px -2px rgba(255,100,100,0.25), inset 0 -4px 6px -2px rgba(0,0,0,0.7), 0 3px 5px rgba(0,0,0,0.6)",
      }}
    >
      {/* Inner ring highlight */}
      <div
        className="absolute rounded-full border"
        style={{
          inset: "15%",
          borderColor: isWhite
            ? "rgba(255,255,255,0.5)"
            : "rgba(180,80,60,0.3)",
          borderWidth: 1,
        }}
      />

      {/* Legal target ring pulse */}
      <AnimatePresence>
        {isLegalTarget && !isSelected && (
          <motion.div
            key="legal-ring"
            initial={{ opacity: 0, scale: 0.8 }}
            animate={{ opacity: [0.5, 1, 0.5], scale: [1, 1.1, 1] }}
            exit={{ opacity: 0, scale: 0.8 }}
            transition={{ duration: 1.5, repeat: Infinity }}
            className="absolute rounded-full border-2 border-amber-400"
            style={{ inset: -4, pointerEvents: "none" }}
          />
        )}
      </AnimatePresence>

      {/* Stack count badge */}
      {count !== undefined && count > 1 && (
        <span
          className="absolute bottom-0 right-0 text-xs font-bold leading-none rounded-full flex items-center justify-center"
          style={{
            width: 16,
            height: 16,
            fontSize: 10,
            background: isWhite ? "#2a1f15" : "#fbbf24",
            color: isWhite ? "#fbbf24" : "#2a1f15",
            boxShadow: "0 2px 4px rgba(0,0,0,0.5)",
            transform: "translate(20%, 20%)",
          }}
        >
          {count}
        </span>
      )}
    </motion.div>
  );
}

/** An empty legal target indicator (for empty points) */
export function LegalTargetDot({ onClick }: { onClick?: (e: React.MouseEvent) => void }) {
  return (
    <motion.div
      onClick={onClick}
      initial={{ opacity: 0, scale: 0 }}
      animate={{ opacity: [0.5, 1, 0.5], scale: [0.9, 1.1, 0.9] }}
      exit={{ opacity: 0, scale: 0 }}
      transition={{ duration: 1.5, repeat: Infinity }}
      whileHover={{ scale: 1.3 }}
      whileTap={{ scale: 0.9 }}
      className="rounded-full border-2 border-amber-400 cursor-pointer"
      style={{
        width: 24,
        height: 24,
        background: "rgba(251,191,36,0.15)",
        boxShadow: "0 0 12px rgba(251,191,36,0.4)",
      }}
    />
  );
}

