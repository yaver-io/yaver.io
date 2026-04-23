import type { Color, GameState, Move } from "@game/types";
import Point from "./Point";
import Bar from "./Bar";
import { motion, AnimatePresence } from "framer-motion";
import Checker from "./Checker";

interface BoardProps {
  state: GameState;
  legalMoves: Move[];
  legalTargets: Array<number | "off">;
  selectedPoint: number | "bar" | null;
  onSelectPoint: (p: number | "bar") => void;
  onClearSelection: () => void;
  onMakeMove: (move: Move) => void;
}

/**
 * Board Layout:
 *
 *  Top (Black's home ← Black moves right-to-left):
 *    Points 13-24 left to right (13 on left, 24 on right)
 *  Bottom (White's home → White moves left-to-right):
 *    Points 12-1 left to right (12 on left, 1 on right)
 *
 *  Visual:
 *    [24][23][22][21][20][19] | BAR | [18][17][16][15][14][13]   ← top row
 *    [  1][ 2][ 3][ 4][ 5][ 6] | BAR | [ 7][ 8][ 9][10][11][12]  ← bottom row
 */

// Point numbers displayed in each column position (top, left-to-right and bottom left-to-right)
const TOP_LEFT = [24, 23, 22, 21, 20, 19];
const TOP_RIGHT = [18, 17, 16, 15, 14, 13];
const BOT_LEFT = [1, 2, 3, 4, 5, 6];
const BOT_RIGHT = [7, 8, 9, 10, 11, 12];

export default function Board({
  state,
  legalMoves,
  legalTargets,
  selectedPoint,
  onSelectPoint,
  onClearSelection,
  onMakeMove,
}: BoardProps) {
  const { points, bar, borneOff, currentTurn, diceRolled } = state;

  const legalSources = new Set(legalMoves.map(m => m.from));

  const renderPoint = (pointNum: number, isBottom: boolean) => (
    <Point
      key={pointNum}
      pointNumber={pointNum}
      point={points[pointNum - 1]}
      isBottom={isBottom}
      isSelected={selectedPoint === pointNum}
      isLegalSource={legalSources.has(pointNum)}
      legalTargets={legalTargets}
      selectedPoint={selectedPoint}
      onSelectPoint={n => onSelectPoint(n)}
      onMakeMove={onMakeMove}
      legalMoves={legalMoves}
      currentTurn={currentTurn}
      diceRolled={diceRolled}
    />
  );

  return (
    <div
      className="relative no-select"
      style={{
        width: "min(95vw, 850px)",
        aspectRatio: "4 / 3",
        borderRadius: 16,
        overflow: "hidden",
        boxShadow: "0 25px 60px rgba(0,0,0,0.8), 0 0 0 2px rgba(255,200,100,0.12)",
        background: "url(\"data:image/svg+xml,%3Csvg viewBox='0 0 200 200' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='noise'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.8' numOctaves='4' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23noise)' opacity='0.15'/%3E%3C/svg%3E\"), linear-gradient(180deg, #180f08 0%, #0a0603 100%)",
        border: "3px solid #3d2a10",
      }}
      onClick={e => {
        if (e.target === e.currentTarget) onClearSelection();
      }}
    >
      {/* Outer frame gradient */}
      <div
        className="absolute inset-0 pointer-events-none"
        style={{
          background:
            "linear-gradient(180deg, rgba(255,200,80,0.04) 0%, transparent 30%, transparent 70%, rgba(255,200,80,0.04) 100%)",
          zIndex: 10,
        }}
      />

      {/* Board felt surface */}
      <div
        className="absolute inset-2 rounded-xl"
        style={{
          background: "url(\"data:image/svg+xml,%3Csvg viewBox='0 0 200 200' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='felt'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='1.2' numOctaves='3' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23felt)' opacity='0.08'/%3E%3C/svg%3E\"), radial-gradient(ellipse at center, #26160d 0%, #0d0804 100%)",
        }}
      />

      {/* ── Main layout: [left 6 cols] [bar] [right 6 cols] ── */}
      <div
        className="absolute inset-2 rounded-xl overflow-hidden"
        style={{ display: "flex", flexDirection: "row" }}
      >
        {/* Left quadrant */}
        <div
          style={{
            flex: 6,
            display: "grid",
            gridTemplateColumns: "repeat(6, 1fr)",
            gridTemplateRows: "1fr 1fr",
          }}
        >
          {/* Top left points (24-19) */}
          {TOP_LEFT.map(n => (
            <div key={n} style={{ height: "100%" }}>
              {renderPoint(n, false)}
            </div>
          ))}
          {/* Bottom left points (1-6) */}
          {BOT_LEFT.map(n => (
            <div key={n} style={{ height: "100%" }}>
              {renderPoint(n, true)}
            </div>
          ))}
        </div>

        {/* Bar */}
        <div
          style={{
            width: 52,
            flexShrink: 0,
            background: "rgba(6, 3, 1, 0.9)",
            borderLeft: "1px solid rgba(255,200,100,0.08)",
            borderRight: "1px solid rgba(255,200,100,0.08)",
          }}
        >
          <Bar
            bar={bar}
            currentTurn={currentTurn}
            legalTargets={legalTargets}
            legalMoves={legalMoves}
            selectedPoint={selectedPoint}
            onSelectBar={() => onSelectPoint("bar")}
            onMakeMove={onMakeMove}
            diceRolled={diceRolled}
          />
        </div>

        {/* Right quadrant */}
        <div
          style={{
            flex: 6,
            display: "grid",
            gridTemplateColumns: "repeat(6, 1fr)",
            gridTemplateRows: "1fr 1fr",
          }}
        >
          {/* Top right points (18-13) */}
          {TOP_RIGHT.map(n => (
            <div key={n} style={{ height: "100%" }}>
              {renderPoint(n, false)}
            </div>
          ))}
          {/* Bottom right points (7-12) */}
          {BOT_RIGHT.map(n => (
            <div key={n} style={{ height: "100%" }}>
              {renderPoint(n, true)}
            </div>
          ))}
        </div>

        {/* Center divider bar */}
        <div
          className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2"
          style={{
            width: "calc(100% - 60px)",
            height: 2,
            background: "linear-gradient(90deg, transparent, rgba(255,200,100,0.15) 20%, rgba(255,200,100,0.25) 50%, rgba(255,200,100,0.15) 80%, transparent)",
            pointerEvents: "none",
            zIndex: 5,
          }}
        />
      </div>

      {/* ── Borne-off piles ── */}
      <BorneOffIndicator color="white" count={borneOff.white} />
      <BorneOffIndicator color="black" count={borneOff.black} />

      {/* Bear-off zone indicator when in bear-off mode */}
      <AnimatePresence>
        {selectedPoint !== null && legalTargets.includes("off") && (
          <motion.div
            key="bear-off-zone"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={() => {
              const move = legalMoves.find(m => m.from === selectedPoint && m.to === "off");
              if (move) onMakeMove(move);
            }}
            className="absolute right-0 inset-y-0 flex flex-col items-center justify-center cursor-pointer z-20"
            style={{
              width: 36,
              background: "rgba(251,191,36,0.15)",
              borderLeft: "2px solid rgba(251,191,36,0.5)",
            }}
          >
            <motion.span
              animate={{ opacity: [0.5, 1, 0.5] }}
              transition={{ duration: 1.5, repeat: Infinity }}
              className="text-amber-400 font-bold"
              style={{ writingMode: "vertical-rl", fontSize: 11, letterSpacing: 2 }}
            >
              BEAR OFF
            </motion.span>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

function BorneOffIndicator({ color, count }: { color: "white" | "black"; count: number }) {
  if (count === 0) return null;
  const isWhite = color === "white";
  return (
    <div
      className="absolute flex flex-col gap-0.5 items-center"
      style={{
        right: -40,
        top: isWhite ? "auto" : 8,
        bottom: isWhite ? 8 : "auto",
        zIndex: 20,
      }}
    >
      <span className="text-xs text-amber-400/60 font-mono">{count}</span>
      {Array.from({ length: Math.min(count, 5) }).map((_, i) => (
        <div
          key={i}
          className={isWhite ? "checker-white" : "checker-black"}
          style={{ width: 20, height: 20, borderRadius: "50%" }}
        />
      ))}
    </div>
  );
}
