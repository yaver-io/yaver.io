# Backgammon Gameplay Features & Roadmap

This document outlines the current gameplay features of our Backgammon game and proposes future enhancements inspired by the extensible open-source engine `quasoft/backgammonjs`.

## Current Gameplay (Standard Rules)
Our engine currently strictly enforces standard international Backgammon rules:
*   **Movement Restrictions:** Checkers can only move to open points (points with 1 or fewer opponent checkers).
*   **Hitting:** Landing on a point with exactly 1 opponent checker sends it to the bar.
*   **The Bar:** Players with checkers on the bar must re-enter them into the opponent's home board before making any other moves.
*   **Bearing Off:** Players can only bear off checkers once all 15 of their checkers are securely within their own home board.
*   **Scoring & Doubling Cube:** Includes standard scoring (Single, Gammon, Backgammon) and a fully functional Doubling Cube to raise the stakes.
*   **Visual Assist (Current):** The UI currently highlights playable checkers and possible target destinations to assist newer players.

## UI & UX Improvements (Pending)
The following user experience improvements are planned or required for the core gameplay interface:
*   **Contextual Highlighting:** The player should only see a checker's possible move target locations when that checker is actively selected. If no checker is selected, there should be never be any target indications shown on the board.
*   **Doubles Representation:** When a player rolls doubles, displaying 4 identical dice on screen is confusing. We need to implement a clearer, less cluttered approach to represent that the player has 4 moves available.
*   **Played Dice Visibility:** Played dice should not completely disappear from the UI. The player must be able to see which dice have already been played during their turn, but they should be visually distinct (e.g., dimmed, crossed out, or visually exhausted) from the unplayed dice.

---

## Proposed Gameplay Modes (Inspired by `quasoft/backgammonjs`)

The `quasoft/backgammonjs` repository demonstrates that a truly robust backgammon engine should be extensible and support multiple regional rule variants. We can implement the following modes to improve replayability:

### 1. Hardcore Mode (Standard)
*   **No Visual Hints:** Disable the glowing amber target highlights and playable checker indicators.
*   **No Pip Counter:** Force players to manually count pips, simulating a real-life physical board experience.

### 2. "Casual" Variant
*   **Rules:** Standard rules apply.
*   **Difference:** The Doubling Cube is entirely disabled. Games are played strictly for 1 point each, perfect for relaxed, low-stakes matches.

### 3. "Tapa" (Тапа) Variant
A popular variant in Bulgaria and the Middle East.
*   **No Hitting:** You cannot hit opponent checkers.
*   **Pinning (Tapa):** Landing on a single opponent checker "pins" it. The opponent cannot move the pinned checker until you move your checker off of it.
*   **Movement:** Since there is no hitting, there is no "Bar". The strategy shifts entirely to building blockades and pinning opponent stragglers.

### 4. "Gul Bara" (Гюлбара / Rosespring) Variant
Also known as "Crazy Narde".
*   **Starting Position:** All 15 checkers start on the player's 24-point (the opponent's 1-point).
*   **No Hitting:** Like Tapa, there is no hitting.
*   **Doubles Bonus:** If a player rolls doubles, they play the roll normally (e.g., four 5s). Then, they get to play *every subsequent double* up to 6. (e.g., after playing four 5s, they get to play four 6s!). This creates massive, game-swinging turns.

## Technical Implementation Notes for New Variants
To support these variants, we will need to refactor our current `game-engine` package:
1.  **Rule Interface:** Create a generic `GameRules` interface that the engine consumes.
2.  **Pluggable Validation:** Extract the current standard `isLegalMove` and `getLegalMoves` functions into a `StandardRules` class.
3.  **State Modifications:** Ensure the `GameState` type can support pinned checkers (for Tapa) and extended dice rolls (for Gul Bara).
