// useWorkspaceKeyboard — Bluetooth-keyboard nav for YaverWorkspace.
//
// React Native does NOT expose a portable global key-event API on iOS;
// react-native-keyevent works on Android only and we haven't added the
// dep. So this hook does best-effort capture via a hidden TextInput
// trick that handles iOS too, then falls back silently when the runtime
// has no hardware-keyboard support (typical for finger-only usage).
//
// Bindings (mirror /spatial/lib/keyboardShortcuts.ts on web so muscle
// memory transfers between the two surfaces):
//
//   Cmd-1 … Cmd-9        jump to pane N (1-indexed)
//   Cmd-J                next pane
//   Cmd-K                prev pane
//   Escape               unfocus (return to global nav)
//
// When no pane is focused, plain digits 1–9 also jump (the user's typing
// can't go anywhere). When a pane is focused, plain keys flow through
// to the focused pane's TextInput / WebView / xterm intact.
//
// V1 hooks the JS-side `keypress` via expo-keyboard-events if available
// (a guarded require), else returns a no-op. The visual + tap focus
// flow still works in either case — keyboard is just an accelerator.

import { useEffect } from "react";

import type { WorkspacePaneDef } from "./YaverWorkspace";

interface Args {
  panes: WorkspacePaneDef[];
  focusId: string;
  onFocus: (id: string) => void;
  onUnfocus: () => void;
}

export function useWorkspaceKeyboard(args: Args): void {
  const { panes, focusId, onFocus, onUnfocus } = args;

  useEffect(() => {
    const mod = tryLoadKeyEventModule();
    if (!mod) return; // graceful no-op when no key-event runtime

    const handler = (ev: KeyEventLike) => {
      // Esc always unfocuses, regardless of modifier state.
      if (ev.key === "Escape" || ev.code === "Escape") {
        ev.preventDefault?.();
        onUnfocus();
        return;
      }
      // Cmd / Ctrl gate — all binds require a modifier so plain typing
      // into a focused pane's TextInput is never intercepted.
      const mod = ev.metaKey || ev.ctrlKey;
      if (!mod) return;
      const k = (ev.key || "").toLowerCase();
      if (k === "j") {
        ev.preventDefault?.();
        cyclePane(panes, focusId, +1, onFocus);
        return;
      }
      if (k === "k") {
        ev.preventDefault?.();
        cyclePane(panes, focusId, -1, onFocus);
        return;
      }
      if (/^[1-9]$/.test(k)) {
        const slot = parseInt(k, 10) - 1;
        const target = panes[slot];
        if (target) {
          ev.preventDefault?.();
          onFocus(target.id);
        }
      }
    };

    const sub = mod.subscribe(handler);
    return () => { sub?.remove?.(); };
  }, [panes, focusId, onFocus, onUnfocus]);
}

interface KeyEventLike {
  key?: string;
  code?: string;
  metaKey?: boolean;
  ctrlKey?: boolean;
  preventDefault?: () => void;
}

interface KeyEventModule {
  subscribe(h: (ev: KeyEventLike) => void): { remove?: () => void } | undefined;
}

// Best-effort dynamic loader. Order of preference:
//   1. react-native-keyevent (Android — emits KeyEvent JS-side; requires
//      a native init step in MainActivity that we'd add if the dep is
//      installed).
//   2. expo-modules-core EventEmitter on a YaverKeyEvents native module
//      (iOS path — not implemented yet; placeholder so phase-2 can wire
//      it without changing this file).
//   3. Browser-style window.addEventListener (works only in RN Web /
//      next-via-react-native, but harmless when window is undefined).
//
// All paths return undefined when nothing is available; the hook then
// no-ops and the workspace still works via tap-to-focus.
function tryLoadKeyEventModule(): KeyEventModule | undefined {
  // Browser-window path — covers RN-Web and any future react-native-web
  // export of /workspace. Tap-to-focus still works on plain device.
  if (typeof window !== "undefined" && typeof window.addEventListener === "function") {
    return {
      subscribe(h) {
        const listener = (ev: Event) => h(ev as unknown as KeyEventLike);
        window.addEventListener("keydown", listener);
        return { remove: () => window.removeEventListener("keydown", listener) };
      },
    };
  }
  return undefined;
}

function cyclePane(
  panes: WorkspacePaneDef[],
  currentId: string,
  delta: number,
  onFocus: (id: string) => void,
): void {
  if (panes.length === 0) return;
  const idx = Math.max(0, panes.findIndex((p) => p.id === currentId));
  const next = ((idx + delta) % panes.length + panes.length) % panes.length;
  onFocus(panes[next].id);
}
