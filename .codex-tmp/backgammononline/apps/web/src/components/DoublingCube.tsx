import { motion } from "framer-motion";
import type { Color, GameState } from "@game/types";

interface DoublingCubeProps {
  value: number;
  owner: Color | null;
  currentTurn: Color;
  onDouble?: () => void;
}

export default function DoublingCube({ value, owner, currentTurn, onDouble }: DoublingCubeProps) {
  const canDouble = onDouble && (owner === null || owner === currentTurn);

  return (
    <motion.div
      whileHover={canDouble ? { scale: 1.08, rotate: 5 } : undefined}
      whileTap={canDouble ? { scale: 0.95 } : undefined}
      onClick={canDouble ? onDouble : undefined}
      className="flex flex-col items-center gap-1 select-none"
      title={canDouble ? "Offer Double" : undefined}
    >
      <motion.div
        animate={{ rotate: [0, 3, -3, 0] }}
        transition={{ duration: 4, repeat: Infinity, ease: "easeInOut" }}
        className="relative flex items-center justify-center rounded-xl font-black"
        style={{
          width: 52,
          height: 52,
          background: "linear-gradient(135deg, #f5f0e0 0%, #d4c090 50%, #b89040 100%)",
          boxShadow: "0 6px 20px rgba(0,0,0,0.5), inset 0 1px 0 rgba(255,255,255,0.9)",
          fontSize: value >= 32 ? 16 : 22,
          color: "#1a0f05",
          cursor: canDouble ? "pointer" : "default",
          border: "1px solid rgba(0,0,0,0.2)",
        }}
      >
        {value}
      </motion.div>
      <span className="text-xs font-mono" style={{ color: "rgba(251,191,36,0.5)" }}>
        {owner === null ? "CUBE" : `${owner.toUpperCase()[0]}'s`}
      </span>
    </motion.div>
  );
}
