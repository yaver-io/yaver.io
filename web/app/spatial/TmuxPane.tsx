"use client";

/**
 * TmuxPane — xterm.js wired to the agent's /ws/terminal WebSocket, with
 * an "attach to your tmux session" first-input. This is what makes the
 * /spatial route feel like the user's actual terminal: their tmux
 * config (prefix Ctrl-b, splits, vi mode-keys, 1M scrollback) all live
 * on the remote agent — we just project the PTY bytes onto the glass.
 *
 * Wire protocol (mirrors mobile/app/(tabs)/terminal.tsx):
 *   WS GET /ws/terminal?token=…&cwd=… (auth via query param)
 *   - binary frames: stdin bytes <-> PTY stdout
 *   - text frames:   {"resize":{"cols":N,"rows":M}}
 *                    {"type":"terminal_session","sessionId":"…"} on attach
 *
 * Attach behavior:
 *   - On WS open, if `tmuxSession` is set, sends "tmux a -t <name>\r"
 *     as the very first stdin write. The user lands directly in their
 *     session — Ctrl-b h/v/c muscle memory works because it's their
 *     real tmux on the other side.
 *   - If `tmuxSession` is empty, just spawns the shell; user can type
 *     `tmux new -s scratch` or anything else.
 *
 * Focus contract:
 *   - Click into the pane → onFocus() fires → parent records the
 *     focused pane id → global /spatial shortcuts (Cmd-J/K/etc) still
 *     work but unmodified keys (Ctrl-b, j in vi-copy-mode) go to
 *     tmux uninterrupted.
 */

import { useEffect, useRef } from "react";

export interface TmuxPaneProps {
  agentUrl: string;
  token: string;
  /** Existing tmux session to attach to. Empty = open a fresh shell. */
  tmuxSession?: string;
  /** Working directory for the spawned shell. Defaults to $HOME. */
  cwd?: string;
  /** Initial shell binary; defaults to $SHELL on the agent host. */
  shell?: string;
  focused: boolean;
  onFocus: () => void;
  /** Render slot for the pane chrome — header bar, status dot, etc. */
  headerContent?: React.ReactNode;
}

export function TmuxPane({
  agentUrl,
  token,
  tmuxSession,
  cwd,
  shell,
  focused,
  onFocus,
  headerContent,
}: TmuxPaneProps): React.JSX.Element {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<any>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const sentInitRef = useRef<boolean>(false);

  // Mount: bring up xterm.js + open WS + wire bidirectional bytes.
  useEffect(() => {
    let cancelled = false;
    let resizeObserver: ResizeObserver | null = null;
    let term: any;
    let fit: any;

    (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
      ]);
      if (cancelled || !containerRef.current) return;

      term = new Terminal({
        fontFamily: "ui-monospace, 'JetBrains Mono', Menlo, monospace",
        fontSize: 12,
        theme: { background: "rgba(0,0,0,0.0)", foreground: "#e5e7eb" },
        allowTransparency: true,
        cursorBlink: true,
        // 100k scrollback — high enough to match the user's 1M tmux
        // history feel without burning megabytes per pane on the GPU.
        // Real backbuffer lives in tmux on the agent; this is just
        // the local render cache.
        scrollback: 100_000,
        convertEol: false, // PTY bytes already include CR/LF
        // Don't disable stdin — we forward to PTY over the WS.
        disableStdin: false,
      });
      termRef.current = term;
      fit = new FitAddon();
      term.loadAddon(fit);
      term.open(containerRef.current);
      try { fit.fit(); } catch {}

      // Open the WS with auth via query param (browsers can't set
      // Authorization on a WebSocket constructor).
      const url = new URL(agentUrl.replace(/^http/, "ws") + "/ws/terminal");
      url.searchParams.set("token", token);
      if (cwd) url.searchParams.set("cwd", cwd);
      if (shell) url.searchParams.set("shell", shell);

      const ws = new WebSocket(url.toString());
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      ws.onopen = () => {
        // On first open, send the tmux attach command if requested.
        // This lands the user inside their existing session so their
        // .tmux.conf bindings (prefix Ctrl-b, splits, vi mode-keys)
        // are immediately live.
        if (!sentInitRef.current && tmuxSession) {
          sentInitRef.current = true;
          // Send as UTF-8 bytes followed by CR. Shell receives it as
          // a normal typed command + Enter.
          const cmd = `tmux a -t ${shellEscape(tmuxSession)} || tmux new -s ${shellEscape(tmuxSession)}\r`;
          ws.send(new TextEncoder().encode(cmd).buffer);
        }
        // Sync initial size
        sendResize(ws, term);
      };

      ws.onmessage = (e) => {
        if (typeof e.data === "string") {
          // text frame — protocol meta, ignore for now
          return;
        }
        // binary frame = PTY output bytes
        term.write(new Uint8Array(e.data as ArrayBuffer));
      };

      ws.onerror = () => {
        term.write("\r\n\x1b[31m[ws error]\x1b[0m\r\n");
      };

      ws.onclose = () => {
        term.write("\r\n\x1b[33m[ws closed — refresh to reconnect]\x1b[0m\r\n");
      };

      // Forward keystrokes → PTY stdin
      term.onData((data: string) => {
        if (ws.readyState !== 1) return;
        ws.send(new TextEncoder().encode(data).buffer);
      });

      // Forward resize → PTY (so vim, less, htop etc. lay out right)
      term.onResize(() => sendResize(ws, term));

      resizeObserver = new ResizeObserver(() => {
        try { fit.fit(); } catch {}
      });
      resizeObserver.observe(containerRef.current);
    })();

    return () => {
      cancelled = true;
      try { wsRef.current?.close(); } catch {}
      try { termRef.current?.dispose(); } catch {}
      resizeObserver?.disconnect();
    };
    // Only depend on agentUrl/token/tmuxSession — re-mount on those
  }, [agentUrl, token, tmuxSession, cwd, shell]);

  // When the parent flips `focused=true`, snap xterm into focus so
  // keystrokes go to the PTY immediately (no click required).
  useEffect(() => {
    if (focused) {
      try { termRef.current?.focus(); } catch {}
    }
  }, [focused]);

  return (
    <div
      onClick={onFocus}
      style={{
        display: "flex",
        flexDirection: "column",
        background: "rgba(0,0,0,0.4)",
        border: `1px solid ${focused ? "#10b98166" : "rgba(255,255,255,0.08)"}`,
        boxShadow: focused ? "0 0 0 1px #10b98144" : "none",
        borderRadius: 8,
        overflow: "hidden",
        position: "relative",
        cursor: "text",
      }}
    >
      {headerContent}
      <div ref={containerRef} style={{ flex: 1, minHeight: 0, padding: 4 }} />
    </div>
  );
}

function sendResize(ws: WebSocket, term: any) {
  if (ws.readyState !== 1) return;
  try {
    ws.send(JSON.stringify({ resize: { cols: term.cols, rows: term.rows } }));
  } catch {
    // ignore — happens when ws is mid-close
  }
}

/** Single-quote a shell argument safely (handles inner quotes by
 *  closing + escaping + reopening). Sufficient for tmux session
 *  names which Yaver constrains to a safe alphabet anyway. */
function shellEscape(s: string): string {
  return "'" + s.replace(/'/g, `'\\''`) + "'";
}

export default TmuxPane;
