"use client";

// useWorkspaceKeyboard — keyboard nav for the flat /workspace route.
// Adapted from /spatial/lib/keyboardShortcuts.ts so the same muscle
// memory transfers between the VR / 3D / 2D spatial surfaces and the
// flat dashboard workspace.
//
// Binds:
//
//   Cmd-J / Cmd-K            next / prev pane
//   Cmd-1 .. Cmd-9           jump to pane N (1-indexed)
//   Escape                   unfocus pane (return to global nav)
//   Cmd-/                    toggle help overlay (handled by caller)
//
// When no pane is focused, bare digits 1..9 also jump panes — this
// covers the desktop user who hasn't yet learned the Cmd modifier.
// When a pane IS focused (xterm has the cursor, or an iframe is
// receiving keystrokes) only Cmd-modified shortcuts intercept;
// everything else flows through to the focused content, so Vim / tmux
// keybindings still work for users with their own .tmux.conf.

import { useCallback, useEffect } from "react";

export interface WorkspaceKeyboardHandlers {
  onSelectPane?: (index: number) => void;
  onNextPane?: () => void;
  onPrevPane?: () => void;
  onUnfocusPane?: () => void;
  onToggleHelp?: () => void;
  /** True when a pane is currently focused (PTY / iframe absorbs keys).
   *  Forces every shortcut to require Cmd / Meta. */
  paneFocused?: boolean;
}

function isEditableTarget(target: EventTarget | null): boolean {
  if (!target || !(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  if (target.isContentEditable) return true;
  return false;
}

export function useWorkspaceKeyboard(handlers: WorkspaceKeyboardHandlers): void {
  const handle = useCallback(
    (e: KeyboardEvent) => {
      // Don't fight a real text input — only the workspace's own
      // controls should swallow modifier combos.
      if (isEditableTarget(e.target) && !e.metaKey && !e.ctrlKey) return;

      if (e.key === "Escape") {
        e.preventDefault();
        handlers.onUnfocusPane?.();
        return;
      }

      const needsMeta = !!handlers.paneFocused;
      const hasMeta = e.metaKey || e.ctrlKey;
      if (needsMeta && !hasMeta) return;

      if (e.key >= "1" && e.key <= "9" && (hasMeta || !needsMeta)) {
        const idx = parseInt(e.key, 10) - 1;
        e.preventDefault();
        handlers.onSelectPane?.(idx);
        return;
      }
      const k = e.key.toLowerCase();
      if (k === "j") { e.preventDefault(); handlers.onNextPane?.(); return; }
      if (k === "k") { e.preventDefault(); handlers.onPrevPane?.(); return; }
      if (k === "/" || k === "?" || k === "h") {
        if (hasMeta || !needsMeta) {
          e.preventDefault();
          handlers.onToggleHelp?.();
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

export const WORKSPACE_SHORTCUT_ROWS: { keys: string; what: string }[] = [
  { keys: "Esc", what: "unfocus pane (return to global nav)" },
  { keys: "Cmd-J / Cmd-K", what: "next / previous pane" },
  { keys: "Cmd-1..9", what: "jump to pane N" },
  { keys: "Cmd-/", what: "toggle this help overlay" },
  { keys: "(no pane focused)", what: "bare j/k/1..9 also work" },
  { keys: "anything else", what: "passes through to the focused pane" },
];
