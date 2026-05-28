"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";
import "@xterm/xterm/css/xterm.css";

type ConnState = "connecting" | "open" | "closed" | "error";

export default function TerminalView({ cwd }: { cwd?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const termRef = useRef<any>(null);
  const fitRef = useRef<any>(null);
  const [status, setStatus] = useState<ConnState>("connecting");
  const [closeReason, setCloseReason] = useState<string>("");
  const [attempt, setAttempt] = useState(0);

  // Manual reconnect — clears closed state and bumps the attempt counter
  // so the effect below re-runs and rebuilds the WebSocket.
  const reconnect = useCallback(() => {
    setStatus("connecting");
    setCloseReason("");
    setAttempt((n) => n + 1);
  }, []);

  useEffect(() => {
    let disposed = false;
    let heartbeatTimer: ReturnType<typeof setInterval> | null = null;
    let resizeCleanup: (() => void) | null = null;

    (async () => {
      const { Terminal } = await import("@xterm/xterm");
      const { FitAddon } = await import("@xterm/addon-fit");
      if (disposed || !ref.current) return;

      // Lazy-init the terminal once; on reconnect we keep the existing
      // term so scrollback survives.
      if (!termRef.current) {
        const term = new Terminal({
          fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
          fontSize: 13,
          cursorBlink: true,
          theme: { background: "#0b0d10", foreground: "#d1d5db" },
        });
        const fit = new FitAddon();
        term.loadAddon(fit);
        term.open(ref.current);
        fit.fit();
        termRef.current = term;
        fitRef.current = fit;
        term.onData((d: string) => {
          const ws = wsRef.current;
          if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(new TextEncoder().encode(d));
          }
        });
      }
      const term = termRef.current;
      const fit = fitRef.current;

      const url = await agentClient.terminalWsUrl(cwd);
      if (disposed) return;
      const ws = new WebSocket(url);
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      // The agent may close gracefully on shutdown, or the TCP path
      // (relay → tunnel → agent) may drop silently. Browser WebSocket
      // surfaces both via onclose eventually, but only after the OS's
      // TCP keepalive fires (minutes). Send a JSON ping every 30s so
      // the agent can answer (and so the path stays warm against
      // intermediate NATs); if we get no data of any kind for 60s,
      // force-close so the user sees the disconnect promptly.
      let lastActivityAt = Date.now();
      const markActivity = () => { lastActivityAt = Date.now(); };

      ws.onopen = () => {
        setStatus("open");
        setCloseReason("");
        if (attempt > 0) {
          term.writeln("\r\n\x1b[90m— reconnected —\x1b[0m");
        } else {
          term.writeln("\x1b[90m— connected —\x1b[0m");
        }
        ws.send(JSON.stringify({ resize: { cols: term.cols, rows: term.rows } }));
      };
      ws.onmessage = (e) => {
        markActivity();
        const data = typeof e.data === "string" ? e.data : new Uint8Array(e.data);
        term.write(data as any);
      };
      ws.onclose = (ev) => {
        setStatus("closed");
        const reason = ev.reason
          ? `${ev.reason} (code ${ev.code})`
          : ev.code
            ? `close code ${ev.code}`
            : "connection closed";
        setCloseReason(reason);
        term.writeln(`\r\n\x1b[90m— disconnected: ${reason} —\x1b[0m`);
      };
      ws.onerror = () => {
        setStatus("error");
        setCloseReason((prev) => prev || "websocket error");
        term.writeln("\r\n\x1b[31mconnection error\x1b[0m");
      };

      heartbeatTimer = setInterval(() => {
        if (ws.readyState !== WebSocket.OPEN) return;
        const idleMs = Date.now() - lastActivityAt;
        if (idleMs > 60_000) {
          // Force-close so onclose fires and the user sees the
          // disconnect + reconnect affordance. Without this, a wedged
          // relay would leave the terminal looking "open" for many
          // minutes while the OS TCP keepalive grinds.
          try { ws.close(4000, "idle timeout"); } catch {}
          return;
        }
        try {
          ws.send(JSON.stringify({ ping: 1, t: Date.now() }));
        } catch {}
      }, 30_000);

      const onResize = () => {
        if (disposed) return;
        fit.fit();
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ resize: { cols: term.cols, rows: term.rows } }));
        }
      };
      window.addEventListener("resize", onResize);
      const ro = new ResizeObserver(onResize);
      if (ref.current) ro.observe(ref.current);
      resizeCleanup = () => {
        window.removeEventListener("resize", onResize);
        ro.disconnect();
      };
    })();

    return () => {
      disposed = true;
      if (heartbeatTimer) clearInterval(heartbeatTimer);
      if (resizeCleanup) resizeCleanup();
      wsRef.current?.close();
      // termRef intentionally NOT disposed here — we keep it across
      // reconnect attempts so scrollback survives. Component unmount
      // disposes via the second effect below.
    };
  }, [cwd, attempt]);

  // Dispose the terminal only on full component unmount.
  useEffect(() => {
    return () => {
      try { termRef.current?.dispose(); } catch {}
      termRef.current = null;
      fitRef.current = null;
    };
  }, []);

  return (
    <div className="relative h-full w-full bg-[#0b0d10] overflow-hidden">
      <div ref={ref} className="h-full w-full p-2" />
      {(status === "closed" || status === "error") ? (
        <div className="pointer-events-none absolute inset-0 flex items-end justify-center pb-3">
          <div className="pointer-events-auto rounded border border-amber-500/40 bg-black/80 px-3 py-2 text-xs text-amber-200 shadow-lg backdrop-blur">
            <span className="mr-2">
              Terminal disconnected{closeReason ? ` — ${closeReason}` : ""}.
            </span>
            <button
              onClick={reconnect}
              className="rounded border border-amber-400 bg-amber-500/20 px-2 py-0.5 font-semibold text-amber-100 hover:bg-amber-500/30"
            >
              Reconnect
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
