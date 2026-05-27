"use client";

/**
 * Keyboard shortcut layer for /spatial — designed for the "Yaver trio":
 *
 *   phone + smart glasses + Bluetooth keyboard
 *
 * Use case: user pairs a foldable BT keyboard to their phone, plugs
 * XReal Air / Mentra / Quest 3 into the same phone via USB-C, opens
 * yaver.io/spatial in the phone's browser. The phone's screen renders
 * onto the glasses; the keyboard is the input device. No laptop, no
 * desk, beach-style.
 *
 * Bindings (vim-flavored — power users feel at home):
 *
 *   j / k         next / prev pane
 *   1..9          jump to pane N (1-indexed)
 *   Space         toggle voice — start/stop recording
 *   Escape        cancel voice OR exit any modal
 *   ? or h        toggle the help overlay
 *   v             enter VR (when WebXR available)
 *   gg            scroll terminal pane to top
 *   G             scroll terminal pane to bottom
 *
 * Shortcuts are SUSPENDED when an <input>, <textarea>, or
 * contenteditable element is focused — so paste-back voice prompts
 * in the orb still work. Hold-to-record is also suspended in editable
 * targets (Space is just a space character there).
 */

import { useCallback, useEffect, useRef } from "react";

export interface ShortcutHandlers {
  onNextPane?: () => void;
  onPrevPane?: () => void;
  onSelectPane?: (index: number) => void;
  onToggleVoice?: () => void;
  onCancelVoice?: () => void;
  onToggleHelp?: () => void;
  onEnterVR?: () => void;
  onScrollTop?: () => void;
  onScrollBottom?: () => void;
}

function isEditableTarget(target: EventTarget | null): boolean {
  if (!target || !(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  if (target.isContentEditable) return true;
  return false;
}

export function useSpatialShortcuts(handlers: ShortcutHandlers): void {
  // gg sequence tracker — gg = scroll to top (matches less/vim).
  const lastGPressRef = useRef<number>(0);

  const handle = useCallback(
    (e: KeyboardEvent) => {
      if (isEditableTarget(e.target)) return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;

      // Numeric pane jumps (1..9)
      if (e.key >= "1" && e.key <= "9") {
        const idx = parseInt(e.key, 10) - 1;
        e.preventDefault();
        handlers.onSelectPane?.(idx);
        return;
      }

      switch (e.key) {
        case "j":
          e.preventDefault();
          handlers.onNextPane?.();
          return;
        case "k":
          e.preventDefault();
          handlers.onPrevPane?.();
          return;
        case " ":
          // Space toggles voice. Use keydown so hold-to-talk is also
          // possible in the future via keyup detection.
          e.preventDefault();
          handlers.onToggleVoice?.();
          return;
        case "Escape":
          e.preventDefault();
          handlers.onCancelVoice?.();
          return;
        case "?":
        case "h":
          e.preventDefault();
          handlers.onToggleHelp?.();
          return;
        case "v":
          e.preventDefault();
          handlers.onEnterVR?.();
          return;
        case "g": {
          const now = performance.now();
          if (now - lastGPressRef.current < 400) {
            e.preventDefault();
            handlers.onScrollTop?.();
            lastGPressRef.current = 0;
            return;
          }
          lastGPressRef.current = now;
          return;
        }
        case "G":
          e.preventDefault();
          handlers.onScrollBottom?.();
          return;
      }
    },
    [handlers],
  );

  useEffect(() => {
    window.addEventListener("keydown", handle);
    return () => window.removeEventListener("keydown", handle);
  }, [handle]);
}

/** The shortcuts to render in the help overlay. Single source of truth
 *  so the panel can't drift from the actual bindings. */
export const SHORTCUT_HELP_ROWS: { keys: string; what: string }[] = [
  { keys: "j / k", what: "next / previous pane" },
  { keys: "1 .. 9", what: "jump to pane N" },
  { keys: "Space", what: "toggle voice — record / send" },
  { keys: "Esc", what: "cancel voice or close modal" },
  { keys: "v", what: "enter VR (Quest 3 / Vision Pro)" },
  { keys: "gg / G", what: "scroll terminal to top / bottom" },
  { keys: "? or h", what: "toggle this help overlay" },
];
