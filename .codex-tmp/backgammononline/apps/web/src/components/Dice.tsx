import { motion, AnimatePresence } from "framer-motion";

interface DiceProps {
  initialValues?: number[];
  values: number[];
  rolling?: boolean;
  onRoll?: () => void;
  disabled?: boolean;
  color: "white" | "black";
}

const DOT_POSITIONS: Record<number, Array<[number, number]>> = {
  1: [[1, 1]],
  2: [[0, 0], [2, 2]],
  3: [[0, 0], [1, 1], [2, 2]],
  4: [[0, 0], [0, 2], [2, 0], [2, 2]],
  5: [[0, 0], [0, 2], [1, 1], [2, 0], [2, 2]],
  6: [[0, 0], [0, 1], [0, 2], [2, 0], [2, 1], [2, 2]],
};

function DieFace({ value, size = 52 }: { value: number; size?: number }) {
  const dotSize = Math.floor(size * 0.15);
  const positions = DOT_POSITIONS[value] || [];

  return (
    <div
      className="relative rounded-xl bg-amber-50 flex items-center justify-center"
      style={{
        width: size,
        height: size,
        boxShadow: "0 6px 20px rgba(0,0,0,0.5), inset 0 1px 0 rgba(255,255,255,0.9), inset 0 -2px 4px rgba(0,0,0,0.15)",
        border: "1px solid rgba(0,0,0,0.1)",
      }}
    >
      <div
        className="relative"
        style={{
          width: size * 0.65,
          height: size * 0.65,
          display: "grid",
          gridTemplateColumns: "repeat(3, 1fr)",
          gridTemplateRows: "repeat(3, 1fr)",
        }}
      >
        {positions.map(([row, col], i) => (
          <div
            key={i}
            style={{
              gridRow: row + 1,
              gridColumn: col + 1,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            <div
              style={{
                width: dotSize,
                height: dotSize,
                borderRadius: "50%",
                background: "#1a0f05",
                boxShadow: "inset 0 1px 2px rgba(0,0,0,0.4)",
              }}
            />
          </div>
        ))}
      </div>
    </div>
  );
}

export default function Dice({ initialValues = [], values, rolling, onRoll, disabled, color }: DiceProps) {
  const canRoll = onRoll && !disabled && !rolling && initialValues.length === 0;
  const isWhite = color === "white";

  // Determine what to render based on initialValues
  const isDoubles = initialValues.length === 4;
  
  // For rendering, we always want to show exactly 2 dice if dice have been rolled.
  // If not doubles, initialValues has 2 dice.
  // If doubles, initialValues has 4 identical dice, but we only want to show 2.
  const displayDice = initialValues.length > 0 
    ? [initialValues[0], initialValues[1]] 
    : [];

  // For regular rolls, track which dice are exhausted.
  // If we rolled 3 and 5, and values only has [5], then 3 is exhausted.
  // We need to match remaining values to display dice.
  const getExhaustedState = () => {
    if (isDoubles) {
      // For doubles, both dice appear fully opaque until ALL moves are done.
      // Wait, let's just make them dim if values.length === 0.
      return [values.length === 0, values.length === 0];
    }
    
    // Regular roll: match remaining values to the 2 display dice.
    const remaining = [...values];
    const exhausted = [true, true];
    
    for (let i = 0; i < 2; i++) {
      const idx = remaining.indexOf(displayDice[i]);
      if (idx !== -1) {
        exhausted[i] = false;
        remaining.splice(idx, 1);
      }
    }
    return exhausted;
  };

  const exhaustedState = getExhaustedState();

  return (
    <div className="flex flex-col items-center gap-3">
      <AnimatePresence mode="wait">
        {displayDice.length > 0 ? (
          <motion.div
            key="dice-values"
            initial={{ opacity: 0, scale: 0.5 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.5 }}
            className="flex flex-col items-center gap-2"
          >
            <div className="flex gap-2 flex-wrap justify-center relative">
              {displayDice.map((v, i) => (
                <motion.div
                  key={i}
                  initial={{ rotate: -30, scale: 0 }}
                  animate={{ rotate: 0, scale: 1 }}
                  transition={{
                    type: "spring",
                    stiffness: 300,
                    damping: 15,
                    delay: i * 0.08,
                  }}
                  style={{ opacity: exhaustedState[i] ? 0.3 : 1, transition: "opacity 0.3s" }}
                >
                  <DieFace value={v} />
                </motion.div>
              ))}

              {/* Doubles Badge */}
              {isDoubles && values.length > 0 && (
                <motion.div
                  initial={{ scale: 0 }}
                  animate={{ scale: 1 }}
                  className="absolute -top-3 -right-3 bg-amber-500 text-black text-xs font-black px-2 py-0.5 rounded-full shadow-lg z-10"
                  style={{ boxShadow: "0 2px 8px rgba(251,191,36,0.6)" }}
                >
                  x{values.length}
                </motion.div>
              )}
            </div>
          </motion.div>
        ) : (
          <motion.div
            key="roll-prompt"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            className="flex gap-2"
          >
            {/* Placeholder dice */}
            {[0, 1].map(i => (
              <div
                key={i}
                className="rounded-xl"
                style={{
                  width: 52,
                  height: 52,
                  background: "rgba(255,255,255,0.05)",
                  border: "2px dashed rgba(251,191,36,0.3)",
                }}
              />
            ))}
          </motion.div>
        )}
      </AnimatePresence>



      {rolling && (
        <motion.div
          animate={{ rotate: 360 }}
          transition={{ duration: 0.5, repeat: Infinity, ease: "linear" }}
          className="text-amber-400 text-xl"
        >
          ⟳
        </motion.div>
      )}
    </div>
  );
}
