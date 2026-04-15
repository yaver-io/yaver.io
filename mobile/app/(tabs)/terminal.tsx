import React, { useEffect, useRef, useState } from "react";
import { Keyboard, KeyboardAvoidingView, Platform, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";

// Native mobile terminal — no WebView. WebSocket to /ws/terminal carries
// PTY bytes both ways. We strip the most common ANSI escape sequences and
// render output as a scrollable monospace feed with a stdin input at the
// bottom. Not a full xterm emulator, but usable for: ls, git, cat, npm,
// curl, docker ps, tail -f, interactive prompts, REPLs (node, python).

export default function TerminalScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();

  const [buf, setBuf] = useState<string>("");
  const [input, setInput] = useState("");
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState("");
  const scrollRef = useRef<ScrollView | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const appendTimer = useRef<any>(null);
  const pending = useRef<string>("");

  useEffect(() => {
    const url = terminalWsUrl();
    try {
      const ws = new WebSocket(url);
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;
      ws.onopen = () => {
        setConnected(true);
        setBuf("— connected —\n");
      };
      ws.onmessage = (e) => {
        let text: string;
        if (typeof e.data === "string") text = e.data;
        else text = new TextDecoder().decode(new Uint8Array(e.data as ArrayBuffer));
        pending.current += stripAnsi(text);
        // Throttle state updates to ~10 fps to avoid jank during `tail -f` floods.
        if (!appendTimer.current) {
          appendTimer.current = setTimeout(() => {
            appendTimer.current = null;
            setBuf((b) => capToTail(b + pending.current));
            pending.current = "";
            requestAnimationFrame(() => scrollRef.current?.scrollToEnd({ animated: false }));
          }, 80);
        }
      };
      ws.onclose = () => { setConnected(false); setBuf((b) => b + "\n— disconnected —"); };
      ws.onerror = () => { setError("WebSocket error"); setConnected(false); };
    } catch (e: any) {
      setError(e.message);
    }
    return () => { wsRef.current?.close(); };
  }, []);

  function send(raw: string) {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    // Server expects binary for stdin.
    ws.send(new TextEncoder().encode(raw));
    // Optimistic echo so the user sees their own keystrokes immediately.
    pending.current += raw;
  }

  function onSubmit() {
    send(input + "\n");
    setInput("");
  }

  function keyBtn(label: string, raw: string) {
    return (
      <Pressable key={label} onPress={() => send(raw)} style={[styles.keyBtn, { borderColor: c.border, backgroundColor: c.bgCard }]}>
        <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 11 }}>{label}</Text>
      </Pressable>
    );
  }

  return (
    <View style={[styles.container, { backgroundColor: "#0b0d10" }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
          <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: connected ? "#10b981" : "#ef4444" }} />
          <Text style={{ fontSize: 14, fontWeight: "600", color: "#d1d5db" }}>Terminal</Text>
        </View>
        <View style={{ width: 50 }} />
      </View>

      {error && <Text style={{ color: "#ef4444", fontSize: 11, padding: 8 }}>{error}</Text>}

      <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === "ios" ? "padding" : undefined} keyboardVerticalOffset={insets.bottom}>
        <ScrollView ref={scrollRef} style={{ flex: 1 }} contentContainerStyle={{ padding: 10 }}
          keyboardShouldPersistTaps="handled">
          <Text selectable style={{ color: "#d1d5db", fontFamily: "Menlo", fontSize: 12, lineHeight: 16 }}>{buf}</Text>
        </ScrollView>

        <View style={[styles.macroRow, { borderTopColor: c.border }]}>
          <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ paddingHorizontal: 8 }}>
            {keyBtn("Esc", "\x1b")}
            {keyBtn("Tab", "\t")}
            {keyBtn("^C", "\x03")}
            {keyBtn("^D", "\x04")}
            {keyBtn("↑", "\x1b[A")}
            {keyBtn("↓", "\x1b[B")}
            {keyBtn("←", "\x1b[D")}
            {keyBtn("→", "\x1b[C")}
            {keyBtn("/", "/")}
            {keyBtn("~", "~")}
            {keyBtn("|", "|")}
            {keyBtn("clear", "\x0cclear\n")}
          </ScrollView>
        </View>

        <View style={[styles.inputRow, { backgroundColor: "#1c1f25", borderTopColor: c.border, paddingBottom: insets.bottom || 8 }]}>
          <Text style={{ color: "#818cf8", fontFamily: "Menlo", fontSize: 13, marginRight: 6 }}>$</Text>
          <TextInput
            value={input}
            onChangeText={setInput}
            onSubmitEditing={onSubmit}
            placeholder={connected ? "type a command…" : "connecting…"}
            placeholderTextColor="#555"
            autoCorrect={false}
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            style={{ flex: 1, color: "#e5e7eb", fontFamily: "Menlo", fontSize: 13, padding: 4 }}
            editable={connected}
            returnKeyType="send"
            blurOnSubmit={false}
          />
        </View>
      </KeyboardAvoidingView>
    </View>
  );
}

/** Build the WebSocket URL to the agent's /ws/terminal. Auth token goes in
 *  the query string since browsers + RN WebSockets can't set headers. */
function terminalWsUrl(): string {
  const base = quicClient.baseUrl.replace(/^http/, "ws");
  const h = quicClient.getAuthHeaders();
  const token = encodeURIComponent((h.Authorization || "").replace("Bearer ", ""));
  return `${base}/ws/terminal?token=${token}`;
}

/** Strip the most common ANSI escape sequences. We keep newlines + tabs so
 *  output remains readable; color codes and cursor-movement sequences are
 *  dropped (we don't have real cursor tracking). */
function stripAnsi(s: string): string {
  // CSI sequences: ESC [ ... final char in @-~
  s = s.replace(/\x1b\[[0-9;?]*[@-~]/g, "");
  // OSC sequences: ESC ] ... BEL or ESC \
  s = s.replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, "");
  // Remaining stray escapes.
  s = s.replace(/\x1b[@-_]/g, "");
  // Carriage returns alone → drop (many TUIs use \r to overwrite line).
  s = s.replace(/\r(?!\n)/g, "");
  return s;
}

/** Keep the buffer bounded so the ScrollView doesn't grow unbounded. */
function capToTail(s: string, maxChars = 60000): string {
  if (s.length <= maxChars) return s;
  const trimmed = s.slice(s.length - maxChars);
  const firstNL = trimmed.indexOf("\n");
  return firstNL > 0 ? trimmed.slice(firstNL + 1) : trimmed;
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
  inputRow: { flexDirection: "row", alignItems: "center", padding: 8, borderTopWidth: 1 },
  macroRow: { borderTopWidth: 1, paddingVertical: 6, backgroundColor: "#12141a" },
  keyBtn: { paddingHorizontal: 10, paddingVertical: 6, marginHorizontal: 4, borderRadius: 4, borderWidth: 1, minWidth: 36, alignItems: "center" },
});
