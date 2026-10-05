import React, { useCallback, useEffect, useRef, useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { useAuth } from "../../context/AuthContext";
import { useColors } from "../../context/ThemeContext";
import { useDevice } from "../../context/DeviceContext";
import { quicClient } from "../../lib/quic";
import type { StudioRunner } from "../../lib/studioWorkspace";
import XtermView, { type XtermHandle } from "../XtermView";
import { isTerminalMetaFrame, resizeFrame } from "../../lib/xtermBridge";

function encodeUtf8(value: string): Uint8Array {
  try { return new TextEncoder().encode(value); } catch {
    return Uint8Array.from(value, (character) => character.charCodeAt(0) & 0xff);
  }
}

function terminalUrl(baseUrl: string, token: string, cwd: string | undefined, runner: StudioRunner, tmuxSession: string): string {
  const params = new URLSearchParams({ token, term: "xterm-256color" });
  if (cwd) params.set("cwd", cwd);
  if (runner !== "shell") params.set("launch", runner);
  else {
    params.set("profile_tmux", tmuxSession);
    params.set("profile_shell", "default");
  }
  return `${baseUrl.replace(/^http/, "ws").replace(/\/+$/, "")}/ws/terminal?${params.toString()}`;
}

export function StudioTerminalPane({ cwd, runner, tmuxSession }: { cwd?: string; runner: StudioRunner; tmuxSession: string }) {
  const colors = useColors();
  const { token } = useAuth();
  const { activeDevice, connectionStatus } = useDevice();
  const terminalRef = useRef<XtermHandle | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState<"connecting" | "open" | "closed" | "error">("connecting");
  const [failure, setFailure] = useState<string | null>(null);

  useEffect(() => {
    if (!activeDevice || !token || connectionStatus !== "connected") {
      setStatus("closed");
      setFailure("SSH route unavailable. Connect the selected machine, then retry.");
      return;
    }
    let disposed = false;
    setStatus("connecting");
    setFailure(null);
    const socket = new WebSocket(terminalUrl(quicClient.baseUrl, token, cwd, runner, tmuxSession));
    socketRef.current = socket;
    try { (socket as any).binaryType = "arraybuffer"; } catch {}
    socket.onopen = () => {
      if (disposed) return;
      setStatus("open");
      socket.send(resizeFrame(100, 32));
    };
    socket.onmessage = (event: WebSocketMessageEvent) => {
      if (disposed) return;
      if (event.data instanceof ArrayBuffer) terminalRef.current?.write(new Uint8Array(event.data));
      else if (typeof event.data === "string" && !isTerminalMetaFrame(event.data)) terminalRef.current?.write(encodeUtf8(event.data));
      else if (typeof event.data === "string") {
        try {
          const frame = JSON.parse(event.data);
          if (typeof frame?.error === "string") setFailure(frame.error);
        } catch {}
      }
    };
    socket.onerror = () => {
      if (disposed) return;
      setStatus("error");
      setFailure("SSH terminal could not reach the selected machine.");
    };
    socket.onclose = (event: WebSocketCloseEvent) => {
      if (disposed) return;
      setStatus("closed");
      if (event.code !== 1000) setFailure(event.reason || `SSH session closed (code ${event.code}).`);
    };
    return () => {
      disposed = true;
      try { socket.close(); } catch {}
      socketRef.current = null;
    };
  }, [activeDevice, attempt, connectionStatus, cwd, runner, tmuxSession, token]);

  const send = useCallback((bytes: Uint8Array) => {
    if (socketRef.current?.readyState === WebSocket.OPEN) socketRef.current.send(bytes);
  }, []);

  return (
    <View style={[styles.root, { backgroundColor: "#0b0e14" }]}>
      <View style={[styles.bar, { borderBottomColor: colors.border }]}>
        <Text style={[styles.title, { color: colors.textSecondary }]} numberOfLines={1}>
          SSH · {activeDevice?.alias ? `@${activeDevice.alias}` : activeDevice?.name || "machine"} · {runner === "shell" ? tmuxSession : runner}
        </Text>
        <Text style={{ color: status === "open" ? colors.success : colors.textMuted, fontSize: 10 }}>{status}</Text>
      </View>
      <XtermView
        ref={terminalRef}
        onData={send}
        onResize={(cols, rows) => {
          if (socketRef.current?.readyState === WebSocket.OPEN) socketRef.current.send(resizeFrame(cols, rows));
        }}
        onReady={() => terminalRef.current?.fit()}
        style={styles.terminal}
      />
      {failure ? (
        <View style={[styles.failure, { backgroundColor: colors.bgCard, borderColor: colors.border }]}>
          <Text style={{ color: colors.error, fontSize: 12, flex: 1 }}>{failure}</Text>
          <Pressable onPress={() => setAttempt((value) => value + 1)} style={[styles.retry, { backgroundColor: colors.accent }]}>
            <Text style={styles.retryText}>Retry</Text>
          </Pressable>
        </View>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, minHeight: 0 },
  bar: { height: 34, paddingHorizontal: 10, borderBottomWidth: StyleSheet.hairlineWidth, flexDirection: "row", alignItems: "center", gap: 8 },
  title: { flex: 1, fontSize: 11, fontWeight: "700" },
  terminal: { flex: 1 },
  failure: { position: "absolute", left: 12, right: 12, bottom: 12, borderWidth: 1, borderRadius: 10, padding: 10, flexDirection: "row", alignItems: "center", gap: 10 },
  retry: { borderRadius: 8, paddingHorizontal: 12, paddingVertical: 7 },
  retryText: { color: "#fff", fontSize: 11, fontWeight: "800" },
});
