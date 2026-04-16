"use client";

import { useEffect, useRef } from "react";
import { agentClient } from "@/lib/agent-client";
import "@xterm/xterm/css/xterm.css";

export default function TerminalView({ cwd }: { cwd?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    let disposed = false;
    let term: any;
    let fit: any;

    (async () => {
      const { Terminal } = await import("@xterm/xterm");
      const { FitAddon } = await import("@xterm/addon-fit");
      if (disposed || !ref.current) return;

      term = new Terminal({
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
        fontSize: 13,
        cursorBlink: true,
        theme: { background: "#0b0d10", foreground: "#d1d5db" },
      });
      fit = new FitAddon();
      term.loadAddon(fit);
      term.open(ref.current);
      fit.fit();

      const url = await agentClient.terminalWsUrl(cwd);
      if (disposed) return;
      const ws = new WebSocket(url);
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      ws.onopen = () => {
        term.writeln("\x1b[90m— connected —\x1b[0m");
        ws.send(JSON.stringify({ resize: { cols: term.cols, rows: term.rows } }));
      };
      ws.onmessage = (e) => {
        const data = typeof e.data === "string" ? e.data : new Uint8Array(e.data);
        term.write(data as any);
      };
      ws.onclose = () => term.writeln("\r\n\x1b[90m— disconnected —\x1b[0m");
      ws.onerror = () => term.writeln("\r\n\x1b[31mconnection error\x1b[0m");

      term.onData((d: string) => {
        if (ws.readyState === WebSocket.OPEN) ws.send(new TextEncoder().encode(d));
      });

      const onResize = () => {
        if (disposed) return;
        fit.fit();
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ resize: { cols: term.cols, rows: term.rows } }));
        }
      };
      window.addEventListener("resize", onResize);
      // Observe container resize too (tab switches).
      const ro = new ResizeObserver(onResize);
      ro.observe(ref.current);

      return () => {
        window.removeEventListener("resize", onResize);
        ro.disconnect();
      };
    })();

    return () => {
      disposed = true;
      wsRef.current?.close();
      term?.dispose();
    };
  }, [cwd]);

  return (
    <div className="bg-[#0b0d10] border border-surface-800 rounded-lg overflow-hidden">
      <div ref={ref} className="h-[500px] p-2" />
    </div>
  );
}
