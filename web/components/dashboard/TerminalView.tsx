"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";
import "@xterm/xterm/css/xterm.css";

type ConnState = "connecting" | "open" | "closed" | "error";

// One-tap coding-agent launchers — kept in sync with the mobile app's
// src/lib/agentLaunch.ts. Typed straight into the remote PTY in yolo mode.
const AGENT_LAUNCHERS: ReadonlyArray<{ id: string; label: string; command: string; hint: string }> = [
  { id: "claude", label: "Claude", command: "claude --dangerously-skip-permissions", hint: "Launch Claude Code with permission prompts skipped" },
  { id: "codex", label: "Codex", command: "codex --dangerously-bypass-approvals-and-sandbox", hint: "Launch Codex with approvals + sandbox bypassed" },
  { id: "opencode", label: "OpenCode", command: "opencode", hint: "Launch OpenCode (bring-your-own-provider TUI)" },
];

export default function TerminalView({ cwd }: { cwd?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const termRef = useRef<any>(null);
  const fitRef = useRef<any>(null);
  const recognitionRef = useRef<any>(null);
  const [status, setStatus] = useState<ConnState>("connecting");
  const [closeReason, setCloseReason] = useState<string>("");
  const [attempt, setAttempt] = useState(0);
  const [dictating, setDictating] = useState(false);
  const [runningRunner, setRunningRunner] = useState<string | null>(null);
  const [sttAvailable] = useState<boolean>(
    () => typeof window !== "undefined" && !!((window as any).SpeechRecognition || (window as any).webkitSpeechRecognition),
  );

  // Manual reconnect — clears closed state and bumps the attempt counter
  // so the effect below re-runs and rebuilds the WebSocket.
  const reconnect = useCallback(() => {
    setStatus("connecting");
    setCloseReason("");
    setAttempt((n) => n + 1);
  }, []);

  // Type bytes into the PTY (binary stdin frame), then refocus the grid.
  const sendToPty = useCallback((text: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(new TextEncoder().encode(text));
    try { termRef.current?.focus(); } catch {}
  }, []);

  // Open/close toggle: tap an idle runner to launch it, tap the active one to
  // send `/exit`. Best-effort state — reset on (re)connect since the PTY is new.
  const toggleRunner = useCallback(
    (l: { id: string; command: string }) => {
      if (status !== "open") return;
      if (runningRunner === l.id) {
        sendToPty("/exit\n");
        setRunningRunner(null);
      } else {
        sendToPty(`${l.command}\n`);
        setRunningRunner(l.id);
      }
    },
    [status, runningRunner, sendToPty],
  );

  // Optional browser dictation → typed at the prompt (no auto-Enter).
  const toggleDictation = useCallback(() => {
    if (!sttAvailable) return;
    if (recognitionRef.current) {
      try { recognitionRef.current.stop(); } catch {}
      recognitionRef.current = null;
      setDictating(false);
      return;
    }
    const Ctor = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition;
    const rec = new Ctor();
    rec.lang = "en-US";
    rec.interimResults = false;
    rec.maxAlternatives = 1;
    rec.onresult = (ev: any) => {
      const text = ev.results?.[0]?.[0]?.transcript ?? "";
      if (text.trim()) sendToPty(text.trim());
    };
    rec.onend = () => { recognitionRef.current = null; setDictating(false); };
    rec.onerror = () => { recognitionRef.current = null; setDictating(false); };
    recognitionRef.current = rec;
    setDictating(true);
    try { rec.start(); } catch { recognitionRef.current = null; setDictating(false); }
  }, [sttAvailable, sendToPty]);

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
        setRunningRunner(null); // fresh PTY
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
        setRunningRunner(null);
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
    <div className="flex h-full w-full flex-col bg-[#0b0d10] overflow-hidden">
      {/* One-tap agent launchers + optional dictation */}
      <div className="flex items-center gap-2 border-b border-white/10 px-2 py-1.5 overflow-x-auto">
        {AGENT_LAUNCHERS.map((l) => {
          const active = runningRunner === l.id;
          return (
            <button
              key={l.id}
              title={active ? `Exit ${l.label} (sends /exit)` : l.hint}
              disabled={status !== "open"}
              onClick={() => toggleRunner(l)}
              className={`shrink-0 rounded border px-2.5 py-1 text-xs font-semibold disabled:opacity-40 ${
                active
                  ? "border-violet-400 bg-violet-500 text-white hover:bg-violet-600"
                  : "border-violet-400/50 bg-violet-500/15 text-violet-200 hover:bg-violet-500/25"
              }`}
            >
              {active ? `■ ${l.label}` : `▷ ${l.label}`}
            </button>
          );
        })}
        <span className="mx-1 h-4 w-px shrink-0 bg-white/10" />
        <button
          disabled={status !== "open"}
          onClick={() => sendToPty("\x03")}
          className="shrink-0 rounded border border-white/10 bg-white/5 px-2 py-1 font-mono text-xs text-gray-300 hover:bg-white/10 disabled:opacity-40"
        >
          ^C
        </button>
        {sttAvailable ? (
          <button
            onClick={toggleDictation}
            disabled={status !== "open"}
            title="Dictate a command"
            className={`shrink-0 rounded border px-2 py-1 text-xs font-semibold disabled:opacity-40 ${
              dictating
                ? "border-emerald-400 bg-emerald-400 text-black"
                : "border-emerald-400/50 bg-white/5 text-emerald-300 hover:bg-emerald-500/15"
            }`}
          >
            {dictating ? "● rec" : "🎙"}
          </button>
        ) : null}
      </div>
      <div className="relative flex-1 overflow-hidden">
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
    </div>
  );
}
