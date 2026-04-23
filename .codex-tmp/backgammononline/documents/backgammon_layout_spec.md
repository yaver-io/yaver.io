import os

markdown_content = """# Backgammon Game Layout Specification

This document defines the structural and visual layout for the React-based Backgammon browser game. The primary goal is to create a professional, single-screen interface that requires zero scrolling.

## 1. Global Layout Constraints
* **Viewport Management:** The application must use `height: 100vh` and `width: 100vw`.
* **No Scroll:** `overflow: hidden` must be applied to the body. All components must scale to fit the available screen height.
* **Layout Engine:** Use CSS Grid or Flexbox to divide the screen into three distinct vertical columns.

## 2. Column 1: Control & Settings Sidebar (Left)
**Width:** Approximately 15-20% of screen width.
**Background:** Subtle dark neutral or matching the board's secondary tone.

* **Top Section (Session Timer):** * Display a "Session Duration" timer (HH:MM:SS) clearly at the very top.
    * Font should be monospaced for stability during counting.
* **Middle Section (Settings):**
    * Interactive toggles for "Sound On/Off," "Show Hints," and "Animations."
    * Use clean, minimalist UI components.
* **Bottom Section (Action):**
    * **Resign Button:** High visibility. 
    * **Style:** Background color `#D32F2F` (Red), white bold text.
    * Placement: Fixed at the bottom of the sidebar.

## 3. Column 2: Game Board (Center)
**Width:** Approximately 60-70% of screen width.
**Layout:** Primary focus area.

* **Board UI:** Must implement the "Vertical Stretch" previously specified. 
* **Geometry:** The board must occupy the full height of the viewport minus any necessary padding. 
* **Scaling:** The board should maintain its aspect ratio while expanding to fill the vertical space.

## 4. Column 3: Player Stats & Dice Sidebar (Right)
**Width:** Approximately 15-20% of screen width.
**Layout:** Vertical Stack (Opponent -> Game Actions -> Player).

### Top Section: Opponent Information
* **Checker Indicator:** A small disc showing the opponent's color (Brown).
* **Name:** "Opponent" or User Name.
* **Turn Timer:** Total time spent on current turn.

### Middle Section: Dice & Controls
* **Dice Display:** Visual representation of two dice.
* **Roll Dice Button:** A prominent button centered between the player cards.
* **Action Logic:** Ensure the button is only active during the user's turn and before the dice are rolled.

### Bottom Section: Player Information
* **Checker Indicator:** A small disc showing the player's color (White).
* **Name:** "You" or User Name.
* **Turn Timer:** Total time spent on the player's turn.

## 5. Visual Hierarchy & CSS Structure
```css
.game-container {
  display: grid;
  grid-template-columns: 250px 1fr 250px;
  height: 100vh;
  width: 100vw;
  background-color: #1a1a1a; /* Dark background to prevent eye strain */
  color: #ffffff;
}

.sidebar {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  padding: 20px;
  border-right: 1px solid #333;
}

.board-area {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 10px;
}

.stats-sidebar {
  display: flex;
  flex-direction: column;
  justify-content: space-around;
  padding: 20px;
  border-left: 1px solid #333;
}

.player-card {
  padding: 15px;
  background: rgba(255, 255, 255, 0.05);
  border-radius: 8px;
  text-align: center;
}