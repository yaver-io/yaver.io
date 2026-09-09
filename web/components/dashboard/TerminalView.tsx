"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";
import "@xterm/xterm/css/xterm.css";

type ConnState = "connecting" | "open" | "closed" | "error";

// One-tap coding-agent launchers — kept in sync with the mobile app's
// src/lib/agentLaunch.ts. Typed straight into the remote PTY in yolo mode.
const AGENT_LAUNCHERS: ReadonlyArray<{ id: string; label: string; command: string; hint: string }> = [
  {
    id: "claude",
    label: "Claude",
    command: "if command -v tmux >/dev/null 2>&1; then exec tmux new-session -A -s yaver-claude 'claude --dangerously-skip-permissions'; else exec claude --dangerously-skip-permissions; fi",
    hint: "Launch Claude Code in a persistent Yaver session with permission prompts skipped",
  },
  {
    id: "codex",
    label: "Codex",
    command: "if command -v tmux >/dev/null 2>&1; then exec tmux new-session -A -s yaver-codex 'codex --dangerously-bypass-approvals-and-sandbox'; else exec codex --dangerously-bypass-approvals-and-sandbox; fi",
    hint: "Launch Codex in a persistent Yaver session with approvals + sandbox bypassed",
  },
  {
    id: "opencode",
    label: "OpenCode",
    command: "if command -v tmux >/dev/null 2>&1; then exec tmux new-session -A -s yaver-opencode 'opencode --auto'; else exec opencode --auto; fi",
    hint: "Launch OpenCode in a persistent Yaver session with auto approvals",
  },
];

export default function TerminalView({
  cwd,
  launch,
  tmuxSession,
  tmuxTaskId,
  sshProfile,
  onCloseTerminal,
  onTmuxClosed,
}: {
  cwd?: string;
  launch?: "claude" | "codex" | "opencode";
  tmuxSession?: string;
  tmuxTaskId?: string;
  sshProfile?: { shell: "default" | "bash" | "zsh" | "fish"; tmux: boolean; tmuxSession?: string };
  onRunnerNeedsAuth?: (runner: "claude" | "codex") => void;
  onCloseTerminal?: () => void;
  onTmuxClosed?: () => void;
}) {
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
  const [closeBusy, setCloseBusy] = useState(false);
  const [closeError, setCloseError] = useState<string>("");
  const [taskFollowUpOnly, setTaskFollowUpOnly] = useState(false);
  const [inputReason, setInputReason] = useState("");
  const [sttAvailable] = useState<boolean>(
    () => typeof window !== "undefined" && !!((window as any).SpeechRecognition || (window as any).webkitSpeechRecognition),
  );

  // Manual reconnect — clears closed state and bumps the attempt counter
  // so the effect below re-runs and rebuilds the WebSocket.
  const reconnect = useCallback(() => {
    setStatus("connecting");
    setCloseReason("");
    setTaskFollowUpOnly(false);
    setInputReason("");
    setAttempt((n) => n + 1);
  }, []);

  // Type bytes into the PTY (binary stdin frame), then refocus the grid.
  const sendToPty = useCallback((text: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(new TextEncoder().encode(text));
    try { termRef.current?.focus(); } catch {}
  }, []);

  const disconnectPty = useCallback(() => {
    try { wsRef.current?.close(1000, "closed by user"); } catch {}
    if (onCloseTerminal) onCloseTerminal();
  }, [onCloseTerminal]);

  const closeTmuxTask = useCallback(async () => {
    if (!tmuxTaskId || closeBusy) return;
    const ok = window.confirm(`Close Yaver session ${tmuxSession || tmuxTaskId}? This stops the adopted terminal on the connected machine.`);
    if (!ok) return;
    setCloseBusy(true);
    setCloseError("");
    try {
      await agentClient.closeTmuxTask(tmuxTaskId);
      try { wsRef.current?.close(1000, "Yaver session closed"); } catch {}
      if (onTmuxClosed) onTmuxClosed();
    } catch (err: any) {
      setCloseError(err?.message || "Failed to close Yaver session");
    } finally {
      setCloseBusy(false);
    }
  }, [closeBusy, onTmuxClosed, tmuxSession, tmuxTaskId]);

  // Open/close toggle: tap an idle runner to launch it, tap the active one to
  // send `/exit`. Best-effort state — reset on (re)connect since the PTY is new.
  const toggleRunner = useCallback(
    async (l: { id: string; label: string; command: string }) => {
      if (status !== "open") return;
      if (runningRunner === l.id) {
        sendToPty("/exit\n");
        setRunningRunner(null);
        return;
      }
      // This tap already expresses the one permitted action: launch this
      // runner. Do not spend a second generation on a hidden test first; the
      // runner's own terminal output is the operation-level readiness signal.
      sendToPty(`${l.command}\n`);
      setRunningRunner(l.id);
    },
    [runningRunner, sendToPty, status],
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

      const url = await agentClient.terminalWsUrl(cwd, { launch, tmuxSession, sshProfile });
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
        setTaskFollowUpOnly(false);
        setInputReason("");
        term.options.disableStdin = false;
        if (attempt > 0) {
          term.writeln("\r\n\x1b[90m— reconnected —\x1b[0m");
        } else {
          term.writeln("\x1b[90m— connected —\x1b[0m");
        }
        ws.send(JSON.stringify({ resize: { cols: term.cols, rows: term.rows } }));
      };
      // /ws/terminal carries two unrelated things on ONE socket, and the only
      // thing separating them is the frame opcode:
      //
      //   binary = PTY bytes — the ONLY thing xterm is ever given
      //   text   = control plane — session id, sudo prompt, errors, pong
      //
      // Before 2026-07-27 this handler wrote BOTH into the grid. So the agent's
      // reply to our own 30s keepalive below was painted at the cursor, and an
      // idle terminal on the user's Linux box showed
      //
      //   $ {"pong":1}{"pong":1}
      //
      // on the prompt line — Yaver's heartbeat rendered as if the user had
      // typed it. The fix is to decide by FRAME TYPE, never by string-matching
      // the payload: any content test ("does it look like JSON?", "does it say
      // pong?") is a test the user can type into their own shell.
      ws.onmessage = (e) => {
        // Liveness FIRST, before any filtering. The 60s force-close below
        // keys off this, and the keepalive's answer is a control frame — if
        // marking activity moved below the filter, answering a ping would
        // stop counting as inbound data and we would resurrect the
        // idle-but-healthy self-disconnect the pong exists to prevent.
        markActivity();

        if (typeof e.data !== "string") {
          term.write(new Uint8Array(e.data));
          return;
        }

        const text = e.data;
        let frame: Record<string, unknown> | null = null;
        if (text.trimStart().startsWith("{")) {
          try {
            const parsed = JSON.parse(text);
            if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
              frame = parsed as Record<string, unknown>;
            }
          } catch {
            frame = null;
          }
        }

        if (!frame) {
          // Older agents emit a few diagnostics as bare prose on this channel
          // ("pty read err: …"). Show them DELIBERATELY, in Yaver's own voice,
          // rather than smuggling them in as if the shell had printed them.
          term.writeln(`\r\n\x1b[33m${text}\x1b[0m`);
          return;
        }

        const type = typeof frame.type === "string" ? frame.type : "";
        const detail = typeof frame.error === "string" ? frame.error : "";
        if (type === "error" || detail) {
          const msg = detail || "terminal error";
          setCloseReason((prev) => prev || msg);
          term.writeln(`\r\n\x1b[31m${msg}\x1b[0m`);
          return;
        }
        if (type === "runner_pty" && frame.inputMode === "task-followup") {
          const reason = typeof frame.inputReason === "string"
            ? frame.inputReason
            : "This task seat is observable here; send prompts with Reply on the task.";
          setTaskFollowUpOnly(true);
          setInputReason(reason);
          term.options.disableStdin = true;
          return;
        }
        // Every other control frame — pong, terminal_session, sudo_prompt,
        // runner_auth_invalid, and anything a newer agent adds — is consumed
        // here and never rendered. Unknown-but-structured is control by
        // definition; painting it is what caused this bug.
      };
      ws.onclose = (ev) => {
        setStatus("closed");
        setRunningRunner(null);
        setTaskFollowUpOnly(false);
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
  }, [cwd, launch, tmuxSession, sshProfile?.shell, sshProfile?.tmux, sshProfile?.tmuxSession, attempt]);

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
        {tmuxSession ? (
          <span className="shrink-0 rounded border border-sky-400/40 bg-sky-500/15 px-2.5 py-1 font-mono text-xs text-sky-700 dark:text-sky-200">
            Yaver session · {tmuxSession}
          </span>
        ) : AGENT_LAUNCHERS.map((l) => {
          const active = runningRunner === l.id;
          return (
            <button
              key={l.id}
              title={active ? `Exit ${l.label} (sends /exit)` : l.hint}
              disabled={status !== "open"}
              onClick={() => { void toggleRunner(l); }}
              className={`shrink-0 rounded border px-2.5 py-1 text-xs font-semibold disabled:opacity-40 ${
                active
                  ? "border-violet-400 bg-violet-500 text-white hover:bg-violet-600"
                  : "border-violet-400/50 bg-violet-500/15 text-violet-700 dark:text-violet-200 hover:bg-violet-500/25"
              }`}
            >
              {active ? `■ ${l.label}` : `▷ ${l.label}`}
            </button>
          );
        })}
        <span className="mx-1 h-4 w-px shrink-0 bg-white/10" />
        {taskFollowUpOnly ? (
          <span className="shrink-0 text-xs text-amber-300" title={inputReason}>
            Observe only · reply from the task
          </span>
        ) : null}
        <button
          disabled={status !== "open" || taskFollowUpOnly}
          onClick={() => sendToPty("\x03")}
          className="shrink-0 rounded border border-white/10 bg-white/5 px-2 py-1 font-mono text-xs text-gray-300 hover:bg-white/10 disabled:opacity-40"
        >
          ^C
        </button>
        {tmuxSession ? (
          <button
            disabled={!tmuxTaskId || closeBusy}
            onClick={closeTmuxTask}
            title={tmuxTaskId ? "Stop the adopted terminal on the connected machine" : "This Yaver session is not attached to a task, so the dashboard will not guess which terminal to close"}
            className="shrink-0 rounded border border-rose-400/40 bg-rose-500/10 px-2 py-1 text-xs font-semibold text-rose-700 hover:bg-rose-500/15 disabled:opacity-40 dark:text-rose-200"
          >
            {closeBusy ? "Closing..." : "Close session"}
          </button>
        ) : null}
        <button
          onClick={disconnectPty}
          title="Close this browser PTY connection"
          className="shrink-0 rounded border border-white/10 bg-white/5 px-2 py-1 text-xs text-gray-300 hover:bg-white/10"
        >
          Close PTY
        </button>
        {sttAvailable ? (
          <button
            onClick={toggleDictation}
            disabled={status !== "open" || taskFollowUpOnly}
            title="Dictate a command"
            className={`shrink-0 rounded border px-2 py-1 text-xs font-semibold disabled:opacity-40 ${
              dictating
                ? "border-emerald-400 bg-emerald-400 text-black"
                : "border-emerald-400/50 bg-white/5 text-emerald-700 dark:text-emerald-300 hover:bg-emerald-500/15"
            }`}
          >
            {dictating ? "● rec" : "🎙"}
          </button>
        ) : null}
        {closeError ? (
          <span className="shrink-0 text-xs text-rose-300">{closeError}</span>
        ) : null}
      </div>
      <div className="relative flex-1 overflow-hidden">
        <div ref={ref} className="h-full w-full p-2" />
      {(status === "closed" || status === "error") ? (
        <div className="pointer-events-none absolute inset-0 flex items-end justify-center pb-3">
          <div className="pointer-events-auto rounded border border-amber-500/40 bg-black/80 px-3 py-2 text-xs text-amber-700 dark:text-amber-200 shadow-lg backdrop-blur">
            <span className="mr-2">
              Terminal disconnected{closeReason ? ` — ${closeReason}` : ""}.
            </span>
            <button
              onClick={reconnect}
              className="rounded border border-amber-400 bg-amber-500/20 px-2 py-0.5 font-semibold text-amber-800 dark:text-amber-100 hover:bg-amber-500/30"
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
