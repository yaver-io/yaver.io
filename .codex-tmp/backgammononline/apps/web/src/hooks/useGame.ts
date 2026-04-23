import { useState, useCallback, useEffect, useRef } from "react";
import type { Color, GameState, Move } from "@game/types";
import {
  initGame,
  startTurnWithRoll,
  applyMove,
  endTurn,
  detectWin,
  deepCloneState,
  undoMove as undoMoveEngine,
} from "@game/backgammon";
import { getAllLegalMoves, getLegalMovesForDie } from "@game/legalMoves";
import { getBestMoves, isAITurn } from "@game/ai";

interface UseGameOptions {
  mode: GameState["mode"];
}

export interface GameHook {
  state: GameState;
  legalMoves: Move[];
  selectedPoint: number | "bar" | null;
  setSelectedPoint: (p: number | "bar" | null) => void;
  legalTargets: Array<number | "off">;
  rollDice: () => void;
  makeMove: (move: Move) => void;
  undoMove: () => void;
  newGame: () => void;
  endTurnManually: () => void;
  isAI: boolean;
}

export function useGame({ mode }: UseGameOptions): GameHook {
  const [state, setState] = useState<GameState>(() => {
    const s = initGame(mode, "black");
    // Start game immediately in playing status
    return { ...s, status: "playing" };
  });
  const [selectedPoint, setSelectedPoint] = useState<number | "bar" | null>(null);
  const aiTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // ── Derived: legal moves from selected source ─────────────────────────────
  const legalMoves: Move[] = (() => {
    if (!state.diceRolled || state.dice.length === 0) return [];
    const all = getAllLegalMoves(state);
    if (selectedPoint === null) return all;
    return all.filter(m => m.from === selectedPoint);
  })();

  const legalTargets: Array<number | "off"> = selectedPoint === null ? [] : legalMoves.map(m => m.to);

  // ── Roll dice ─────────────────────────────────────────────────────────────
  const rollDice = useCallback(() => {
    setState(prev => {
      if (prev.diceRolled) return prev;
      return startTurnWithRoll(prev);
    });
  }, []);

  // ── Make a move ───────────────────────────────────────────────────────────
  const makeMove = useCallback((move: Move) => {
    setState(prev => {
      const newState = applyMove(prev, move);
      const win = detectWin(newState);
      if (win) {
        return { ...newState, status: "finished", winner: win.winner, winType: win.type };
      }
      return newState;
    });
    setSelectedPoint(null);
  }, []);

  // ── Undo a move ───────────────────────────────────────────────────────────
  const undoMove = useCallback(() => {
    setState(prev => {
      // Cannot undo if no dice rolled, or if no moves were made this turn
      // We check if we have made moves by seeing if dice.length < initialDice.length
      if (!prev.diceRolled || prev.dice.length === prev.initialDice.length) return prev;
      return undoMoveEngine(prev);
    });
    setSelectedPoint(null);
  }, []);

  // ── End turn (called when no more dice or player clicks End Turn) ─────────
  const endTurnManually = useCallback(() => {
    setState(prev => {
      if (prev.status === "finished") return prev;
      return endTurn(prev);
    });
    setSelectedPoint(null);
  }, []);

  // ── Auto-end turn when dice are all used ─────────────────────────────────
  useEffect(() => {
    if (state.status !== "playing") return;
    if (!state.diceRolled) return;
    if (state.dice.length > 0) return;
    // All dice used — auto-end after brief delay
    const timer = setTimeout(() => {
      setState(prev => (prev.dice.length === 0 && prev.diceRolled ? endTurn(prev) : prev));
    }, 600);
    return () => clearTimeout(timer);
  }, [state.dice, state.diceRolled, state.status]);

  // ── AI turn logic ─────────────────────────────────────────────────────────
  useEffect(() => {
    if (state.status !== "playing") return;
    if (!isAITurn(state)) return;
    if (aiTimerRef.current) clearTimeout(aiTimerRef.current);

    if (!state.diceRolled) {
      // AI rolls dice after a short delay
      aiTimerRef.current = setTimeout(() => {
        setState(prev => {
          if (!isAITurn(prev) || prev.diceRolled) return prev;
          return startTurnWithRoll(prev);
        });
      }, 800);
      return;
    }

    if (state.dice.length > 0) {
      // AI picks moves after thinking
      aiTimerRef.current = setTimeout(() => {
        setState(prev => {
          if (!isAITurn(prev) || !prev.diceRolled || prev.dice.length === 0) return prev;
          const moves = getBestMoves(prev);
          if (moves.length === 0) return endTurn(prev);

          // Apply the first move
          const move = moves[0];
          const afterMove = applyMove(prev, move);
          const win = detectWin(afterMove);
          if (win) {
            return { ...afterMove, status: "finished", winner: win.winner, winType: win.type };
          }
          return afterMove;
        });
      }, 500);
    }

    return () => {
      if (aiTimerRef.current) clearTimeout(aiTimerRef.current);
    };
  }, [state.currentTurn, state.diceRolled, state.dice.length, state.status]);

  // ── New game ──────────────────────────────────────────────────────────────
  const newGame = useCallback(() => {
    if (aiTimerRef.current) clearTimeout(aiTimerRef.current);
    setState({ ...initGame(mode, "black"), status: "playing" });
    setSelectedPoint(null);
  }, [mode]);

  const isAI = isAITurn(state);

  return {
    state,
    legalMoves,
    selectedPoint,
    setSelectedPoint,
    legalTargets,
    rollDice,
    makeMove,
    undoMove,
    newGame,
    endTurnManually,
    isAI,
  };
}
