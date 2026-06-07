"use client";

// RemoteDesktopView — live remote-desktop for your own machines, rendered in
// the dashboard. The agent captures its screen and serves an MJPEG stream
// (/rd/stream), which we render straight into an <img> (authed via a path-scoped
// browser-session token + relay password baked into the URL). An overlay on top
// captures mouse + keyboard and POSTs them to /rd/input, where the agent injects
// them via the cross-OS ghost engine.
//
// Control (input injection) is OFF by default on the box — the owner flips it on
// from here (POST /rd/policy), which is the runtime consent gate. View works
// without control; you can always just watch.

import { useCallback, useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";

type RdStatus = {
  supported: boolean;
  viewEnabled: boolean;
  controlEnabled: boolean;
  allowRemoteControl: boolean;
  streaming: boolean;
  engineError?: string;
  displaysError?: string;
};

type InputEvent = {
  type: "move" | "click" | "double" | "drag" | "scroll" | "text" | "key";
  nx?: number;
  ny?: number;
  tonx?: number;
  tony?: number;
  button?: "left" | "right" | "middle";
  dx?: number;
  dy?: number;
  text?: string;
  keys?: string[];
};

// JS KeyboardEvent.key → ghost key name (see ghost/input_*.go keymaps).
const NAMED_KEYS: Record<string, string> = {
  Enter: "enter",
  Backspace: "backspace",
  Tab: "tab",
  " ": "space",
  Escape: "esc",
  Delete: "del",
  ArrowUp: "up",
  ArrowDown: "down",
  ArrowLeft: "left",
  ArrowRight: "right",
  Home: "home",
  End: "end",
  PageUp: "pageup",
  PageDown: "pagedown",
  F1: "f1", F2: "f2", F3: "f3", F4: "f4", F5: "f5", F6: "f6",
  F7: "f7", F8: "f8", F9: "f9", F10: "f10", F11: "f11", F12: "f12",
};

export default function RemoteDesktopView() {
  const [streamUrl, setStreamUrl] = useState<string | null>(null);
  const [status, setStatus] = useState<RdStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const imgRef = useRef<HTMLImageElement>(null);
  const overlayRef = useRef<HTMLDivElement>(null);
  // Input batching: hover/move are throttled; clicks/keys flush immediately.
  const queue = useRef<InputEvent[]>([]);
  const flushTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const lastMove = useRef(0);
  const dragStart = useRef<{ nx: number; ny: number; button: "left" | "right" | "middle" } | null>(null);
  const dragged = useRef(false);

  const refreshStatus = useCallback(async () => {
    try {
      const res = await agentClient.agentFetch("/rd/status");
      if (!res.ok) throw new Error(`status ${res.status}`);
      setStatus(await res.json());
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to load status");
    }
  }, []);

  // Mint the authed MJPEG URL once connected, and load status.
  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const url = await agentClient.remoteDesktopStreamUrl("/rd/stream");
        if (alive) setStreamUrl(url);
      } catch (e) {
        if (alive) setError(e instanceof Error ? e.message : "failed to open stream");
      }
    })();
    void refreshStatus();
    return () => { alive = false; };
  }, [refreshStatus]);

  const flush = useCallback(async () => {
    flushTimer.current = null;
    if (queue.current.length === 0) return;
    const events = queue.current;
    queue.current = [];
    try {
      const res = await agentClient.agentFetch("/rd/input", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ events }),
      });
      if (res.status === 403) {
        // Control got disabled (or never enabled) — reflect it in the UI.
        setStatus((s) => (s ? { ...s, controlEnabled: false } : s));
      }
    } catch {
      /* transient; next event re-tries */
    }
  }, []);

  const enqueue = useCallback((ev: InputEvent, immediate = false) => {
    queue.current.push(ev);
    if (immediate) {
      if (flushTimer.current) { clearTimeout(flushTimer.current); flushTimer.current = null; }
      void flush();
      return;
    }
    if (!flushTimer.current) flushTimer.current = setTimeout(() => void flush(), 40);
  }, [flush]);

  // Normalize a pointer event to [0,1] fractions of the displayed frame.
  const norm = useCallback((e: { clientX: number; clientY: number }) => {
    const el = overlayRef.current;
    if (!el) return { nx: 0, ny: 0 };
    const r = el.getBoundingClientRect();
    return {
      nx: Math.min(1, Math.max(0, (e.clientX - r.left) / Math.max(1, r.width))),
      ny: Math.min(1, Math.max(0, (e.clientY - r.top) / Math.max(1, r.height))),
    };
  }, []);

  const controlActive = Boolean(status?.controlEnabled);

  const onPointerMove = useCallback((e: React.PointerEvent) => {
    if (!controlActive) return;
    const now = Date.now();
    if (now - lastMove.current < 45) return; // ~22/s
    lastMove.current = now;
    const { nx, ny } = norm(e);
    if (dragStart.current) dragged.current = true;
    enqueue({ type: "move", nx, ny });
  }, [controlActive, norm, enqueue]);

  const onPointerDown = useCallback((e: React.PointerEvent) => {
    if (!controlActive) return;
    overlayRef.current?.focus();
    const { nx, ny } = norm(e);
    const button = e.button === 2 ? "right" : e.button === 1 ? "middle" : "left";
    dragStart.current = { nx, ny, button };
    dragged.current = false;
    try { (e.target as HTMLElement).setPointerCapture(e.pointerId); } catch { /* ignore */ }
  }, [controlActive, norm]);

  const onPointerUp = useCallback((e: React.PointerEvent) => {
    if (!controlActive) return;
    const { nx, ny } = norm(e);
    const start = dragStart.current;
    dragStart.current = null;
    if (start && dragged.current) {
      enqueue({ type: "drag", nx: start.nx, ny: start.ny, tonx: nx, tony: ny, button: start.button }, true);
    } else {
      const button = e.button === 2 ? "right" : e.button === 1 ? "middle" : "left";
      enqueue({ type: "click", nx, ny, button }, true);
    }
    dragged.current = false;
  }, [controlActive, norm, enqueue]);

  const onDoubleClick = useCallback((e: React.MouseEvent) => {
    if (!controlActive) return;
    const { nx, ny } = norm(e);
    enqueue({ type: "double", nx, ny, button: "left" }, true);
  }, [controlActive, norm, enqueue]);

  const onWheel = useCallback((e: React.WheelEvent) => {
    if (!controlActive) return;
    // Wheel notches: positive deltaY scrolls content down; ghost positive dy
    // scrolls UP, so invert.
    const dy = e.deltaY > 0 ? -1 : e.deltaY < 0 ? 1 : 0;
    const dx = e.deltaX > 0 ? -1 : e.deltaX < 0 ? 1 : 0;
    if (dx || dy) enqueue({ type: "scroll", dx, dy }, true);
  }, [controlActive, enqueue]);

  const onKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (!controlActive) return;
    const mods: string[] = [];
    if (e.ctrlKey) mods.push("ctrl");
    if (e.altKey) mods.push("alt");
    if (e.metaKey) mods.push("cmd");

    const named = NAMED_KEYS[e.key];
    if (named) {
      e.preventDefault();
      if (e.shiftKey) mods.push("shift");
      enqueue({ type: "key", keys: [...mods, named] }, true);
      return;
    }
    if (e.key.length === 1) {
      e.preventDefault();
      if (mods.length > 0) {
        if (e.shiftKey) mods.push("shift");
        enqueue({ type: "key", keys: [...mods, e.key.toLowerCase()] }, true);
      } else {
        enqueue({ type: "text", text: e.key }, true);
      }
    }
  }, [controlActive, enqueue]);

  const toggleControl = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const next = !controlActive;
      const res = await agentClient.agentFetch("/rd/policy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ controlEnabled: next }),
      });
      if (!res.ok) throw new Error(`policy ${res.status}`);
      await refreshStatus();
      if (next) overlayRef.current?.focus();
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to toggle control");
    } finally {
      setBusy(false);
    }
  }, [controlActive, refreshStatus]);

  const enterFullscreen = useCallback(() => {
    const el = overlayRef.current?.parentElement;
    if (!el) return;
    if (document.fullscreenElement) void document.exitFullscreen();
    else void el.requestFullscreen?.();
  }, []);

  return (
    <div className="relative flex h-full w-full flex-col bg-[#0b0d10]">
      {/* Toolbar */}
      <div className="flex items-center justify-between gap-2 border-b border-surface-800 bg-surface-900/80 px-3 py-1.5">
        <div className="flex items-center gap-2 text-[11px] text-surface-300">
          <span className={`inline-flex h-2 w-2 rounded-full ${streamUrl ? "bg-emerald-400" : "bg-slate-500"}`} />
          <span>{controlActive ? "Control ON — click & type to drive" : "View only"}</span>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={toggleControl}
            disabled={busy}
            className={`rounded-md border px-2.5 py-1 text-[11px] font-semibold disabled:opacity-50 ${
              controlActive
                ? "border-amber-500/40 bg-amber-500/10 text-amber-200 hover:bg-amber-500/15"
                : "border-emerald-500/30 bg-emerald-500/10 text-emerald-200 hover:bg-emerald-500/15"
            }`}
            title={controlActive ? "Stop controlling (back to view-only)" : "Enable mouse + keyboard control on this machine"}
          >
            {controlActive ? "Stop control" : "Take control"}
          </button>
          <button
            onClick={enterFullscreen}
            className="rounded-md border border-surface-700 bg-surface-950 px-2.5 py-1 text-[11px] text-surface-300 hover:border-surface-600 hover:text-surface-100"
            title="Fullscreen"
          >
            ⛶ Fullscreen
          </button>
        </div>
      </div>

      {/* Screen + input overlay */}
      <div className="relative flex-1 overflow-hidden bg-black">
        {streamUrl ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            ref={imgRef}
            src={streamUrl}
            alt="remote desktop"
            className="absolute inset-0 h-full w-full object-contain select-none"
            draggable={false}
            onError={() => setError("Stream interrupted — the box may need Screen Recording permission, or the agent is too old.")}
          />
        ) : (
          <div className="flex h-full items-center justify-center text-[12px] text-surface-500">
            {error ? "Couldn't open the screen stream." : "Connecting to screen…"}
          </div>
        )}
        {/* Transparent capture layer. Pointer events only when control is on so
            view-only mode lets the user scroll the page / select normally. */}
        <div
          ref={overlayRef}
          tabIndex={controlActive ? 0 : -1}
          onPointerMove={onPointerMove}
          onPointerDown={onPointerDown}
          onPointerUp={onPointerUp}
          onDoubleClick={onDoubleClick}
          onWheel={onWheel}
          onKeyDown={onKeyDown}
          onContextMenu={(e) => { if (controlActive) e.preventDefault(); }}
          className={`absolute inset-0 outline-none ${controlActive ? "cursor-crosshair" : "pointer-events-none"}`}
          style={{ touchAction: "none" }}
        />
      </div>

      {(error || status?.engineError) ? (
        <div className="border-t border-amber-500/30 bg-amber-500/10 px-3 py-1.5 text-[11px] text-amber-200">
          {error || status?.engineError}
        </div>
      ) : null}
    </div>
  );
}
