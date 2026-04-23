import { useNavigate } from "react-router-dom";
import { motion, AnimatePresence } from "framer-motion";
import type { GameState } from "@game/types";
import { useGame } from "../hooks/useGame";
import Board from "../components/Board";
import Dice from "../components/Dice";
import DoublingCube from "../components/DoublingCube";
import GameControls from "../components/GameControls";
import WinModal from "../components/WinModal";

interface GamePageProps {
  mode: GameState["mode"];
}

export default function GamePage({ mode }: GamePageProps) {
  const navigate = useNavigate();
  const {
    state,
    legalMoves,
    legalTargets,
    selectedPoint,
    setSelectedPoint,
    rollDice,
    makeMove,
    undoMove,
    newGame,
    endTurnManually,
    isAI,
  } = useGame({ mode });

  // Jacoby Rule: gammons/backgammons only count as 1 point if the doubling cube was never turned.
  const cubeUsed = state.doublingCubeOwner !== null;
  const winMultiplier = cubeUsed ? (state.winType === "backgammon" ? 3 : state.winType === "gammon" ? 2 : 1) : 1;
  const winTypeMultiplier = winMultiplier;

  return (
    <div
      className="h-screen w-screen overflow-hidden grid"
      style={{
        background: "radial-gradient(ellipse at 50% 20%, #1a1a1a 0%, #050914 100%)",
        gridTemplateColumns: "250px 1fr 250px",
      }}
    >
      {/* ── Left Sidebar (Controls & Settings) ── */}
      <div className="flex flex-col justify-between p-6 border-r border-white/5 bg-black/40 z-10">
        <div className="flex flex-col gap-6">
          <button
            onClick={() => navigate("/")}
            className="self-start flex items-center gap-2 text-white/40 hover:text-white/70 transition-colors text-sm font-medium"
          >
            ← Menu
          </button>
          
          {/* Session Timer (Static Placeholder) */}
          <div className="flex flex-col gap-1">
            <span className="text-white/40 text-xs uppercase tracking-widest font-bold">Session Time</span>
            <div className="font-mono text-2xl text-amber-500/90 drop-shadow-md">00:00:00</div>
          </div>

          <div className="w-full h-px bg-white/5 my-2" />

          {/* Settings Toggles Placeholder */}
          <div className="flex flex-col gap-5">
            <span className="text-white/40 text-xs uppercase tracking-widest font-bold">Settings</span>
            
            <label className="flex items-center justify-between cursor-pointer group">
              <span className="text-sm text-white/70 group-hover:text-white transition-colors">Sound Effects</span>
              <div className="w-10 h-5 bg-amber-500/20 rounded-full border border-amber-500/50 flex items-center p-0.5 transition-colors">
                <div className="w-4 h-4 bg-amber-500 rounded-full translate-x-5 shadow-sm" />
              </div>
            </label>
            
            <label className="flex items-center justify-between cursor-pointer group">
              <span className="text-sm text-white/70 group-hover:text-white transition-colors">Show Hints</span>
              <div className="w-10 h-5 bg-amber-500/20 rounded-full border border-amber-500/50 flex items-center p-0.5 transition-colors">
                <div className="w-4 h-4 bg-amber-500 rounded-full translate-x-5 shadow-sm" />
              </div>
            </label>
            
            <label className="flex items-center justify-between cursor-pointer group">
              <span className="text-sm text-white/70 group-hover:text-white transition-colors">Animations</span>
              <div className="w-10 h-5 bg-amber-500/20 rounded-full border border-amber-500/50 flex items-center p-0.5 transition-colors">
                <div className="w-4 h-4 bg-amber-500 rounded-full translate-x-5 shadow-sm" />
              </div>
            </label>
          </div>
        </div>

        {/* Resign Button */}
        <button className="w-full py-3 rounded-lg bg-[#D32F2F] hover:bg-red-600 text-white font-bold tracking-wide transition-all shadow-lg shadow-red-900/20 active:scale-95">
          Resign Game
        </button>
      </div>

      {/* ── Center Column (Board Area) ── */}
      <div className="flex flex-col items-center justify-center p-4 relative overflow-hidden">
        {/* Title overlay */}
        <div className="absolute top-6 text-center w-full z-0 pointer-events-none">
          <h1 className="text-gradient-gold font-black text-2xl tracking-[0.2em] opacity-40">BACKGAMMON</h1>
          <p className="text-white/20 text-xs font-mono mt-1 tracking-widest">
            {mode === "vs-ai" ? "VS AI" : "LOCAL 2-PLAYER"} • {state.doublingCubeValue}×
          </p>
        </div>
        
        {/* Board wrapper enforcing aspect ratio */}
        <div 
          className="relative flex items-center justify-center z-10" 
          style={{ height: "100%", maxHeight: "90vh", aspectRatio: "4 / 3" }}
        >
          <Board
            state={state}
            legalMoves={legalMoves}
            legalTargets={legalTargets}
            selectedPoint={selectedPoint}
            onSelectPoint={setSelectedPoint}
            onClearSelection={() => setSelectedPoint(null)}
            onMakeMove={makeMove}
          />
        </div>
      </div>

      {/* ── Right Sidebar (Stats & Dice) ── */}
      <div className="flex flex-col justify-between p-6 border-l border-white/5 bg-black/40 z-10">
        
        {/* Top: Opponent Card (Black) */}
        <PlayerCard
          title={isAI ? "Opponent (AI)" : "Player 2"}
          color="black"
          isActive={state.currentTurn === "black"}
          borneOff={state.borneOff.black}
          barCount={state.bar.black}
        />

        {/* Middle: Dice & Controls */}
        <div className="flex flex-col items-center gap-8 py-8">
          {state.ruleVariant !== "casual" && (
            <DoublingCube
              value={state.doublingCubeValue}
              owner={state.doublingCubeOwner}
              currentTurn={state.currentTurn}
            />
          )}

          <div className="h-16 flex items-center justify-center">
            <Dice
              initialValues={state.initialDice}
              values={state.dice}
              color={state.currentTurn}
              onRoll={!state.diceRolled && !isAI ? rollDice : undefined}
              disabled={isAI}
            />
          </div>

          <GameControls
            state={state}
            isAI={isAI}
            onRoll={rollDice}
            onEndTurn={endTurnManually}
            onUndo={undoMove}
            onNewGame={newGame}
            vertical={true}
          />
        </div>

        {/* Bottom: User Card (White) */}
        <PlayerCard
          title="You"
          color="white"
          isActive={state.currentTurn === "white"}
          borneOff={state.borneOff.white}
          barCount={state.bar.white}
        />

      </div>

      {/* Win modal */}
      {state.status === "finished" && state.winner && state.winType && (
        <WinModal
          winner={state.winner}
          winType={state.winType}
          onNewGame={newGame}
          multiplier={winTypeMultiplier * state.doublingCubeValue}
        />
      )}
    </div>
  );
}

/* ── Player Card (Sidebar) ────────────────────────────────────────────────── */
function PlayerCard({
  title,
  color,
  isActive,
  borneOff,
  barCount,
}: {
  title: string;
  color: "white" | "black";
  isActive: boolean;
  borneOff: number;
  barCount: number;
}) {
  const isWhite = color === "white";
  return (
    <motion.div
      animate={isActive ? { scale: 1.02 } : { scale: 1 }}
      transition={{ type: "spring", stiffness: 300, damping: 25 }}
      className="flex flex-col gap-3 p-4 rounded-xl"
      style={{
        background: "rgba(255, 255, 255, 0.03)",
        border: isActive
          ? `1px solid ${isWhite ? "rgba(251,191,36,0.3)" : "rgba(239,68,68,0.3)"}`
          : "1px solid rgba(255,255,255,0.05)",
        boxShadow: isActive
          ? `0 0 20px ${isWhite ? "rgba(251,191,36,0.1)" : "rgba(239,68,68,0.1)"}`
          : "none",
      }}
    >
      <div className="flex items-center gap-3">
        <div
          className={isWhite ? "checker-white" : "checker-black"}
          style={{ width: 28, height: 28, borderRadius: "50%", flexShrink: 0, boxShadow: "inset 0 2px 4px rgba(255,255,255,0.2), inset 0 -4px 8px rgba(0,0,0,0.5), 0 2px 4px rgba(0,0,0,0.5)" }}
        />
        <div className="flex-1 flex flex-col">
          <span className="font-bold text-sm" style={{ color: isWhite ? "#e8d9bc" : "#ef4444" }}>
            {title}
          </span>
          {isActive ? (
            <span className="text-[10px] text-amber-500 font-mono tracking-wider animate-pulse">● YOUR TURN</span>
          ) : (
            <span className="text-[10px] text-white/20 font-mono tracking-wider">WAITING</span>
          )}
        </div>
      </div>

      <div className="flex justify-between items-end mt-2">
        <div className="flex flex-col gap-1">
          <span className="text-[10px] text-white/30 uppercase tracking-widest">Borne Off</span>
          <span className="font-mono text-sm text-white/80">{borneOff} / 15</span>
        </div>
        {barCount > 0 && (
          <div className="flex flex-col gap-1 text-right">
            <span className="text-[10px] text-red-400/50 uppercase tracking-widest">On Bar</span>
            <span className="font-mono text-sm text-red-400 font-bold">{barCount}</span>
          </div>
        )}
      </div>

      <div className="w-full h-1.5 rounded-full mt-1 overflow-hidden" style={{ background: "rgba(0,0,0,0.5)" }}>
        <motion.div
          animate={{ width: `${(borneOff / 15) * 100}%` }}
          transition={{ type: "spring", stiffness: 100 }}
          className="h-full rounded-full"
          style={{ background: isWhite ? "#fbbf24" : "#ef4444" }}
        />
      </div>
    </motion.div>
  );
}
