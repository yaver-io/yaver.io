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
 * Bindings — ALL global navigation uses Cmd/Meta so that bare keys
 * (including Ctrl-b, j/k/h/l in vi mode, Space, etc.) pass straight
 * through to the user's tmux on the other side of /ws/terminal.
 * The user's muscle memory from their actual .tmux.conf works
 * unmodified.
 *
 *   Cmd-J / Cmd-K        next / prev pane
 *   Cmd-1 .. Cmd-9       jump to pane N (1-indexed)
 *   Cmd-Shift-Space      toggle voice — start / stop recording
 *   Escape               unfocus current pane (return to global nav)
 *                        OR cancel voice / close any modal
 *   Cmd-/                toggle help overlay
 *   Cmd-V (with no       enter VR (when WebXR available)
 *     pane focused)
 *
 * When a terminal pane is focused (user clicked into xterm), ALL
 * non-Cmd shortcuts are passed through to the PTY — including
 * Ctrl-b prefix, vi navigation, Space, etc. Esc is the universal
 * "I want to use global nav again" key.
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
  /** Esc handler that returns "global nav" mode when a pane is focused. */
  onUnfocusPane?: () => void;
  /** When true, only Cmd/Meta-modified shortcuts intercept; bare keys
   *  flow through to the focused pane's PTY. Esc still unfocuses. */
  paneFocused?: boolean;
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

      // Esc is universal: always returns to global nav mode. When a
      // pane is focused this is the "I'm done typing into tmux" key.
      if (e.key === "Escape") {
        if (handlers.paneFocused) {
          e.preventDefault();
          handlers.onUnfocusPane?.();
          return;
        }
        e.preventDefault();
        handlers.onCancelVoice?.();
        return;
      }

      // When a pane is focused (xterm has keystrokes), only Cmd-
      // modified shortcuts intercept. Bare keys (j, k, Ctrl-b, Space,
      // gg, etc.) flow uninterrupted to the PTY so the user's
      // .tmux.conf works.
      const needsMeta = !!handlers.paneFocused;
      const hasMeta = e.metaKey;

      if (needsMeta && !hasMeta) return;
      // When NO pane is focused, both bare and Cmd-modified shortcuts
      // work for back-compat with desktop users who came in pre-tmux.

      // Cmd-1..9 (or bare 1..9 when global) — pane jumps
      if (e.key >= "1" && e.key <= "9" && (hasMeta || !needsMeta)) {
        const idx = parseInt(e.key, 10) - 1;
        e.preventDefault();
        handlers.onSelectPane?.(idx);
        return;
      }

      switch (e.key.toLowerCase()) {
        case "j":
          e.preventDefault();
          handlers.onNextPane?.();
          return;
        case "k":
          e.preventDefault();
          handlers.onPrevPane?.();
          return;
        case " ":
          // Cmd-Space (or Cmd-Shift-Space) for voice when pane focused;
          // bare Space when global nav.
          e.preventDefault();
          handlers.onToggleVoice?.();
          return;
        case "?":
        case "/":
        case "h":
          if (!needsMeta || hasMeta) {
            e.preventDefault();
            handlers.onToggleHelp?.();
            return;
          }
          return;
        case "v":
          if (!needsMeta || hasMeta) {
            e.preventDefault();
            handlers.onEnterVR?.();
            return;
          }
          return;
      }

      // gg / G only when global nav (no point in scrolling our local
      // render when the PTY owns scrollback).
      if (!needsMeta) {
        if (e.key === "g") {
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
        if (e.key === "G") {
          e.preventDefault();
          handlers.onScrollBottom?.();
          return;
        }
      }
    },
    [handlers],
  );

  useEffect(() => {
    window.addEventListener("keydown", handle);
    return () => window.removeEventListener("keydown", handle);
  }, [handle]);
}

/** Help-overlay rows. Single source of truth so the panel can't drift
 *  from the actual handler bindings. */
export const SHORTCUT_HELP_ROWS: { keys: string; what: string }[] = [
  { keys: "Esc", what: "unfocus pane (return to global nav)" },
  { keys: "Cmd-J / Cmd-K", what: "next / previous pane" },
  { keys: "Cmd-1..9", what: "jump to pane N" },
  { keys: "Cmd-Space", what: "toggle voice (when pane focused)" },
  { keys: "Cmd-/ or Cmd-H", what: "toggle this help overlay" },
  { keys: "Cmd-V", what: "enter VR (Quest 3 / Vision Pro)" },
  { keys: "(no pane focused)", what: "bare j/k/Space/?/v/gg also work as above" },
  { keys: "Ctrl-b (your tmux prefix)", what: "passes through to real tmux when pane focused" },
];
