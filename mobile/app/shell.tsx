// Mobile browser-shell — minimal interactive PTY surface for the
// connected device. Talks to the agent's existing /ws/terminal
// endpoint via quicClient.baseUrl (so it transparently rides direct
// LAN, Tailscale, Cloudflare Tunnel, or the relay — whichever path
// the active connection negotiated).
//
// Why custom and not xterm.js: react-native-webview is reserved for
// web content per project policy, and the published RN terminal
// libraries (react-native-terminal-component, react-native-terminal)
// are unmaintained and don't support modern React Native. A simple
// scrollback + input field covers the "type a command, see output"
// case without dragging in a binary terminal emulator. Full TUI
// programs (vim, htop) won't render correctly here — for those, use
// the web dashboard's Shell button on the device card.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { useAuth } from "../src/context/AuthContext";
import { quicClient } from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";

// Minimal ANSI/CSI/OSC stripper — enough so vim's clear-screen
// sequences don't show as `^[[2J^[[H` in scrollback. Does not try to
// render colors or cursor moves; for that, use the web dashboard.
const ANSI_PATTERN =
  // eslint-disable-next-line no-control-regex
  /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[()][0AB]|\x1b[=>NOM78cDEHM]|\x07/g;

function stripAnsi(s: string): string {
  return s.replace(ANSI_PATTERN, "");
}

function buildTerminalWsUrl(baseUrl: string, token: string): string {
  // baseUrl already encodes the chosen transport (direct/tunnel/relay).
  // /ws/terminal is the agent's PTY endpoint; it accepts ?token= as a
  // bearer fallback because mobile can't set custom headers on a WS.
  const ws = baseUrl.replace(/^http/, "ws").replace(/\/+$/, "");
  return `${ws}/ws/terminal?token=${encodeURIComponent(token)}`;
}

export default function ShellScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { activeDevice, connectionStatus } = useDevice();
  const { token } = useAuth();

  const [output, setOutput] = useState("");
  const [input, setInput] = useState("");
  const [status, setStatus] = useState<"idle" | "connecting" | "open" | "closed" | "error">("idle");
  const [error, setError] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const scrollRef = useRef<ScrollView | null>(null);

  const append = useCallback((chunk: string) => {
    setOutput((prev) => {
      // Cap scrollback at ~64 KB so the ScrollView doesn't bloat.
      const next = prev + chunk;
      return next.length > 65536 ? next.slice(next.length - 65536) : next;
    });
    requestAnimationFrame(() => {
      scrollRef.current?.scrollToEnd({ animated: true });
    });
  }, []);

  useEffect(() => {
    if (!activeDevice || !token || connectionStatus !== "connected") {
      setStatus("idle");
      return;
    }
    let cancelled = false;
    setStatus("connecting");
    setError(null);

    const url = buildTerminalWsUrl(quicClient.baseUrl, token);
    const ws = new WebSocket(url);
    wsRef.current = ws;
    // RN's WebSocket supports binaryType="arraybuffer".
    try { (ws as any).binaryType = "arraybuffer"; } catch {}

    ws.onopen = () => {
      if (cancelled) return;
      setStatus("open");
      append("— connected —\n");
      try {
        ws.send(JSON.stringify({ resize: { cols: 80, rows: 24 } }));
      } catch {}
    };
    ws.onmessage = (e: WebSocketMessageEvent) => {
      if (cancelled) return;
      const data = e.data as any;
      if (typeof data === "string") {
        append(stripAnsi(data));
      } else if (data instanceof ArrayBuffer) {
        const text = new TextDecoder("utf-8").decode(new Uint8Array(data));
        append(stripAnsi(text));
      }
    };
    ws.onerror = () => {
      if (cancelled) return;
      setStatus("error");
      setError("connection error");
    };
    ws.onclose = () => {
      if (cancelled) return;
      setStatus("closed");
      append("\n— disconnected —\n");
    };

    return () => {
      cancelled = true;
      try { ws.close(); } catch {}
      wsRef.current = null;
    };
  }, [activeDevice, token, connectionStatus, append]);

  const send = useCallback(() => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const line = input + "\n";
    try {
      ws.send(line);
    } catch (e: any) {
      setError(e?.message || String(e));
    }
    setInput("");
  }, [input]);

  const sendCtrl = useCallback((byte: number) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    try {
      ws.send(new Uint8Array([byte]));
    } catch {}
  }, []);

  if (!activeDevice) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top + 16, paddingHorizontal: 16 }}>
        <AppBackButton onPress={() => router.back()} />
        <View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}>
          <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "600", marginBottom: 6 }}>
            No device connected
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center" }}>
            Open a device from the home screen, then come back to start a shell.
          </Text>
        </View>
      </View>
    );
  }

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: "#0b0d10" }}
      keyboardVerticalOffset={insets.top}
    >
      <View style={{ paddingTop: insets.top + 8, paddingHorizontal: 12, paddingBottom: 8, borderBottomWidth: 1, borderBottomColor: "#1f2937", flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flex: 1 }}>
          <AppBackButton onPress={() => router.back()} />
          <View style={{ flex: 1 }}>
            <Text style={{ color: "#e5e7eb", fontSize: 14, fontWeight: "700" }}>
              Shell · {activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name}
            </Text>
            <Text style={{ color: "#6b7280", fontSize: 11 }}>
              {status === "open" ? "PTY · connected" : status === "connecting" ? "connecting…" : status === "closed" ? "disconnected" : status === "error" ? (error || "error") : "idle"}
            </Text>
          </View>
        </View>
        {status === "connecting" ? <ActivityIndicator color="#6ee7b7" /> : null}
      </View>

      <ScrollView
        ref={scrollRef}
        style={{ flex: 1 }}
        contentContainerStyle={{ padding: 10 }}
        keyboardShouldPersistTaps="handled"
      >
        <Text
          selectable
          style={{
            color: "#d1d5db",
            fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
            fontSize: 12,
            lineHeight: 16,
          }}
        >
          {output || "(no output yet)"}
        </Text>
      </ScrollView>

      <View style={{ flexDirection: "row", alignItems: "center", gap: 6, paddingHorizontal: 8, paddingVertical: 6, borderTopWidth: 1, borderTopColor: "#1f2937" }}>
        <Pressable
          onPress={() => sendCtrl(3)}
          style={styles.ctrlBtn}
          accessibilityLabel="Send Ctrl-C"
        >
          <Text style={styles.ctrlText}>^C</Text>
        </Pressable>
        <Pressable
          onPress={() => sendCtrl(4)}
          style={styles.ctrlBtn}
          accessibilityLabel="Send Ctrl-D"
        >
          <Text style={styles.ctrlText}>^D</Text>
        </Pressable>
      </View>

      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, padding: 10, paddingBottom: insets.bottom + 10, borderTopWidth: 1, borderTopColor: "#1f2937" }}>
        <Text style={{ color: "#6ee7b7", fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", fontSize: 13 }}>$</Text>
        <TextInput
          value={input}
          onChangeText={setInput}
          onSubmitEditing={send}
          placeholder="type a command…"
          placeholderTextColor="#4b5563"
          autoCapitalize="none"
          autoCorrect={false}
          spellCheck={false}
          editable={status === "open"}
          returnKeyType="send"
          style={{
            flex: 1,
            color: "#e5e7eb",
            fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
            fontSize: 13,
            backgroundColor: "#111827",
            borderWidth: 1,
            borderColor: "#1f2937",
            borderRadius: 6,
            paddingHorizontal: 10,
            paddingVertical: 8,
          }}
        />
        <Pressable
          onPress={send}
          disabled={status !== "open" || !input.trim()}
          style={[
            styles.sendBtn,
            (status !== "open" || !input.trim()) && { opacity: 0.4 },
          ]}
        >
          <Text style={styles.sendText}>Send</Text>
        </Pressable>
      </View>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  ctrlBtn: {
    paddingHorizontal: 10,
    paddingVertical: 5,
    borderRadius: 6,
    backgroundColor: "#111827",
    borderWidth: 1,
    borderColor: "#1f2937",
  },
  ctrlText: {
    color: "#9ca3af",
    fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
    fontSize: 12,
    fontWeight: "600",
  },
  sendBtn: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 6,
    backgroundColor: "rgba(16,185,129,0.18)",
    borderWidth: 1,
    borderColor: "rgba(16,185,129,0.45)",
  },
  sendText: {
    color: "#a7f3d0",
    fontSize: 12,
    fontWeight: "700",
  },
});
