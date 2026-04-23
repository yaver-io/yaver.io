import { motion, AnimatePresence } from "framer-motion";
import type { Color } from "@game/types";
import { useNavigate } from "react-router-dom";

interface WinModalProps {
  winner: Color;
  winType: "normal" | "gammon" | "backgammon";
  onNewGame: () => void;
  multiplier: number;
}

const WIN_LABELS = {
  normal: { label: "Winner!", subtitle: "Normal win — 1 point" },
  gammon: { label: "Gammon!", subtitle: "Double win — 2 points" },
  backgammon: { label: "Backgammon!", subtitle: "Triple win — 3 points" },
};

export default function WinModal({ winner, winType, onNewGame, multiplier }: WinModalProps) {
  const navigate = useNavigate();
  const { label, subtitle } = WIN_LABELS[winType];
  const isWhite = winner === "white";

  return (
    <AnimatePresence>
      <motion.div
        key="win-overlay"
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        exit={{ opacity: 0 }}
        className="fixed inset-0 flex items-center justify-center z-50"
        style={{ background: "rgba(5, 9, 20, 0.85)", backdropFilter: "blur(8px)" }}
        onClick={e => e.stopPropagation()}
      >
        <motion.div
          initial={{ scale: 0.7, opacity: 0, y: 40 }}
          animate={{ scale: 1, opacity: 1, y: 0 }}
          exit={{ scale: 0.7, opacity: 0, y: 40 }}
          transition={{ type: "spring", stiffness: 260, damping: 20 }}
          className="relative flex flex-col items-center gap-6 px-12 py-10 rounded-3xl glass-heavy text-center max-w-md mx-4"
          style={{
            border: isWhite
              ? "1px solid rgba(200, 180, 100, 0.4)"
              : "1px solid rgba(200, 60, 60, 0.4)",
            boxShadow: isWhite
              ? "0 0 60px rgba(251,191,36,0.2), 0 25px 50px rgba(0,0,0,0.7)"
              : "0 0 60px rgba(239,68,68,0.2), 0 25px 50px rgba(0,0,0,0.7)",
          }}
        >
          {/* Confetti particles */}
          {Array.from({ length: 12 }).map((_, i) => (
            <motion.div
              key={i}
              className="absolute rounded-full pointer-events-none"
              initial={{
                x: 0,
                y: 0,
                opacity: 1,
                scale: 1,
              }}
              animate={{
                x: (Math.random() - 0.5) * 300,
                y: (Math.random() - 0.5) * 300,
                opacity: 0,
                scale: 0,
              }}
              transition={{ duration: 1.5, delay: i * 0.08, ease: "easeOut" }}
              style={{
                width: 8,
                height: 8,
                background: i % 2 === 0 ? "#fbbf24" : "#ef4444",
                top: "50%",
                left: "50%",
              }}
            />
          ))}

          {/* Winner checker */}
          <motion.div
            animate={{ rotate: [0, 10, -10, 0], scale: [1, 1.1, 1] }}
            transition={{ duration: 2, repeat: Infinity }}
            className={isWhite ? "checker-white" : "checker-black"}
            style={{ width: 64, height: 64, borderRadius: "50%" }}
          />

          {/* Win type */}
          <div>
            <motion.h2
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: 0.2 }}
              className="text-4xl font-black mb-2"
              style={{
                background: isWhite
                  ? "linear-gradient(135deg, #fbbf24, #f5f0e0)"
                  : "linear-gradient(135deg, #ef4444, #fca5a5)",
                WebkitBackgroundClip: "text",
                WebkitTextFillColor: "transparent",
              }}
            >
              {label}
            </motion.h2>
            <p className="text-white/50 text-sm">{subtitle}</p>
          </div>

          {/* Winner name */}
          <div
            className="px-4 py-2 rounded-xl text-sm font-semibold"
            style={{
              background: isWhite ? "rgba(251,191,36,0.1)" : "rgba(239,68,68,0.1)",
              color: isWhite ? "#fbbf24" : "#ef4444",
              border: isWhite ? "1px solid rgba(251,191,36,0.2)" : "1px solid rgba(239,68,68,0.2)",
            }}
          >
            {isWhite ? "⚪ White" : "🔴 Black"} wins {multiplier} point{multiplier !== 1 ? "s" : ""}
          </div>

          {/* Buttons */}
          <div className="flex gap-3 w-full">
            <motion.button
              onClick={onNewGame}
              whileHover={{ scale: 1.03, y: -2 }}
              whileTap={{ scale: 0.97 }}
              className="flex-1 py-3 rounded-2xl font-bold text-sm"
              style={{
                background: "linear-gradient(135deg, #fbbf24 0%, #f59e0b 100%)",
                color: "#1a0f05",
                boxShadow: "0 4px 20px rgba(251,191,36,0.4)",
              }}
            >
              Play Again
            </motion.button>
            <motion.button
              onClick={() => navigate("/")}
              whileHover={{ scale: 1.03, y: -2 }}
              whileTap={{ scale: 0.97 }}
              className="flex-1 py-3 rounded-2xl font-semibold text-sm glass border border-white/10"
              style={{ color: "rgba(255,255,255,0.6)" }}
            >
              Main Menu
            </motion.button>
          </div>
        </motion.div>
      </motion.div>
    </AnimatePresence>
  );
}
