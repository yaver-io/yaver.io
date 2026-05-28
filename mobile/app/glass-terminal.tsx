// Glass terminal — a TUI-style screen optimised for AR-glasses viewing
// (Xreal One via USB-C from iPhone or Beam Pro). High contrast, big mono
// font, minimal chrome. Hosts EITHER:
//
//   • "agent" mode  — drives the existing on-phone runYaverAgent() loop
//                     (BYOK / subscription token, no remote box needed).
//                     This is the Path B in BEAM_PRO_DEV.md — full Claude-
//                     Code-like experience running natively on the phone.
//
//   • "shell" mode  — connects to the connected Yaver agent's /ws/terminal
//                     PTY endpoint (Path A in BEAM_PRO_DEV.md when the
//                     agent is on a remote dev box you SSH'd into; works
//                     the same whether the agent is local or remote).
//
// The screen toggles between the two with a single tap. No third-party SSH
// app needed — Yaver mobile owns the whole surface.
//
// Why a separate route instead of extending (tabs)/terminal.tsx:
//   - Glasses use case wants FULL-SCREEN, no tab bar / header chrome.
//   - We render larger fonts + tmux-aware controls that would clash with
//     the existing terminal tab's "type a command, see output" shape.
//   - Easier to ship a focused TUI without breaking the existing tab UX.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  Keyboard,
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
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import {
  runYaverAgent,
  type YaverAgentProgressEvent,
  type YaverAgentHistoryTurn,
} from "../src/lib/yaverAgentRunner";
import type { YaverAgentToolContext } from "../src/lib/yaverAgentTools";

type Mode = "agent" | "shell";

type Line = {
  kind: "user" | "model" | "tool" | "sys" | "err";
  text: string;
};

// AR-glasses readability palette — pure dark background, high-contrast
// foreground, plus a few semantic accents.
const PAL = {
  bg: "#000000",
  fg: "#e5e7eb",
  muted: "#9ca3af",
  prompt: "#60a5fa",
  user: "#fbbf24",
  model: "#e5e7eb",
  tool: "#34d399",
  toolDim: "#10b981",
  err: "#f87171",
  sys: "#94a3b8",
  border: "#1f2937",
  chip: "#111827",
  chipText: "#d1d5db",
  accent: "#a78bfa",
};

// Bigger than the default terminal tab — readable through 50° FoV glasses.
const FONT_SIZE = 14;
const LINE_HEIGHT = 20;

export default function GlassTerminalScreen() {
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { connectionStatus, devices, primaryDeviceId, selectDevice } = useDevice();

  const [mode, setMode] = useState<Mode>("agent");
  const [lines, setLines] = useState<Line[]>([
    { kind: "sys", text: "— glass terminal — switch with the chip top-right —" },
  ]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const scrollRef = useRef<ScrollView | null>(null);

  // Persist conversation across turns in agent mode so follow-ups continue
  // the same context.
  const historyRef = useRef<YaverAgentHistoryTurn[]>([]);

  // Shell mode: WebSocket + pending output buffer (throttled paints).
  const wsRef = useRef<WebSocket | null>(null);
  const pendingShell = useRef<string>("");
  const flushTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Abort signal for the in-flight agent run.
  const abortRef = useRef<AbortController | null>(null);

  // ── Output helpers ─────────────────────────────────────────────────────
  const appendLine = useCallback((kind: Line["kind"], text: string) => {
    setLines((prev) => {
      const next = [...prev, { kind, text }];
      return next.length > 1000 ? next.slice(-800) : next;
    });
    requestAnimationFrame(() => scrollRef.current?.scrollToEnd({ animated: false }));
  }, []);

  // ── Shell mode wiring ──────────────────────────────────────────────────
  useEffect(() => {
    if (mode !== "shell") {
      wsRef.current?.close();
      wsRef.current = null;
      return;
    }
    try {
      const url = buildTerminalWsUrl();
      const ws = new WebSocket(url);
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;
      ws.onopen = () => appendLine("sys", "— shell connected —");
      ws.onmessage = (e) => {
        const raw = typeof e.data === "string"
          ? e.data
          : new TextDecoder().decode(new Uint8Array(e.data as ArrayBuffer));
        pendingShell.current += stripAnsi(raw);
        if (!flushTimer.current) {
          flushTimer.current = setTimeout(() => {
            flushTimer.current = null;
            const chunk = pendingShell.current;
            pendingShell.current = "";
            if (chunk.length) appendLine("model", chunk);
          }, 80);
        }
      };
      ws.onclose = () => appendLine("sys", "— shell disconnected —");
      ws.onerror = () => appendLine("err", "shell websocket error");
    } catch (e: unknown) {
      appendLine("err", e instanceof Error ? e.message : "shell connect failed");
    }
    return () => {
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [mode, appendLine]);

  // ── Submit handler ─────────────────────────────────────────────────────
  const submit = useCallback(async () => {
    const text = input.trim();
    if (!text || busy) return;
    setInput("");
    Keyboard.dismiss();

    if (mode === "shell") {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        appendLine("err", "shell not connected — switch to agent mode or reconnect");
        return;
      }
      appendLine("user", `$ ${text}`);
      ws.send(new TextEncoder().encode(text + "\n"));
      return;
    }

    // Agent mode: drive runYaverAgent with current history.
    appendLine("user", `▶ ${text}`);
    setBusy(true);
    abortRef.current = new AbortController();

    try {
      const ctx: YaverAgentToolContext = {
        devices: () => devices,
        primaryDeviceId: () => primaryDeviceId,
        secondaryDeviceId: () => null,
        selectDevice: async (deviceId) => {
          const d = devices.find((x) => x.id === deviceId);
          if (d) await selectDevice(d);
        },
      };
      const result = await runYaverAgent({
        prompt: text,
        history: historyRef.current,
        ctx,
        signal: abortRef.current.signal,
        onProgress: (ev: YaverAgentProgressEvent) => {
          if (ev.kind === "model_text") {
            if (ev.text.trim()) appendLine("model", ev.text);
          } else if (ev.kind === "tool_call") {
            const name = ev.call.name;
            const args = safeStringify(ev.call.args);
            const result = ev.call.error
              ? `❌ ${ev.call.error}`
              : safeStringify(ev.call.result);
            appendLine("tool", `⏺ ${name}(${args})`);
            if (result) appendLine("tool", `  ${result}`);
          }
        },
      });

      historyRef.current = [
        ...historyRef.current,
        { role: "user", text },
        { role: "assistant", text: result.finalText },
      ];
      if (result.finalText && !lastLineMatches(lines, result.finalText)) {
        // The progress callback already streamed `model_text` events,
        // but some providers return the final text without intermediate
        // streaming. Render it here as a safety net.
        appendLine("model", result.finalText);
      }
      appendLine("sys", `— done · ${result.steps} step(s)${result.outputTokens ? ` · ${result.outputTokens} out tokens` : ""} —`);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "agent run failed";
      appendLine("err", msg);
    } finally {
      abortRef.current = null;
      setBusy(false);
    }
  }, [input, busy, mode, appendLine, lines]);

  const cancel = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  // ── Render ─────────────────────────────────────────────────────────────
  return (
    <View style={[styles.root, { backgroundColor: PAL.bg, paddingTop: insets.top }]}>
      {/* Header — minimal so the glasses see content, not chrome. */}
      <View style={styles.header}>
        <Pressable hitSlop={12} onPress={() => router.back()}>
          <Text style={[styles.headerBtn, { color: PAL.muted }]}>‹</Text>
        </Pressable>
        <Text style={[styles.headerTitle, { color: PAL.fg }]}>
          {mode === "agent" ? "agent" : "shell"}
          <Text style={{ color: connectionStatus === "connected" ? PAL.tool : PAL.muted, fontSize: 11 }}>
            {"  "}{mode === "shell" ? `· ${connectionStatus}` : ""}
          </Text>
        </Text>
        <Pressable
          hitSlop={12}
          onPress={() => setMode((m) => (m === "agent" ? "shell" : "agent"))}
          style={[styles.modeChip, { backgroundColor: PAL.chip, borderColor: PAL.border }]}
        >
          <Text style={[styles.modeChipText, { color: PAL.accent }]}>
            {mode === "agent" ? "→ shell" : "→ agent"}
          </Text>
        </Pressable>
      </View>

      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        {/* Output area */}
        <ScrollView
          ref={scrollRef}
          style={styles.body}
          contentContainerStyle={{ padding: 12, paddingBottom: 24 }}
        >
          {lines.map((line, i) => (
            <Text
              key={i}
              selectable
              style={{
                color: colorFor(line.kind),
                fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
                fontSize: FONT_SIZE,
                lineHeight: LINE_HEIGHT,
                marginBottom: 2,
              }}
            >
              {line.text}
            </Text>
          ))}
          {busy ? (
            <Text style={{ color: PAL.accent, fontFamily: "Menlo", fontSize: FONT_SIZE, marginTop: 4 }}>
              …
            </Text>
          ) : null}
        </ScrollView>

        {/* Macro keys for paired BT keyboard users — quick send useful
         *  control sequences. Hidden when on-screen keyboard is irrelevant
         *  (foldable BT keyboard does most of this natively). */}
        {mode === "shell" ? (
          <ScrollView
            horizontal
            showsHorizontalScrollIndicator={false}
            style={[styles.macroRow, { borderTopColor: PAL.border }]}
            contentContainerStyle={{ paddingHorizontal: 8 }}
          >
            {[
              ["Esc", "\x1b"],
              ["Tab", "\t"],
              ["^C", "\x03"],
              ["^D", "\x04"],
              ["↑", "\x1b[A"],
              ["↓", "\x1b[B"],
              ["←", "\x1b[D"],
              ["→", "\x1b[C"],
              ["tmux ^B", "\x02"],
              ["clear", "\x0cclear\n"],
            ].map(([label, seq]) => (
              <Pressable
                key={label}
                onPress={() => wsRef.current?.send(new TextEncoder().encode(seq))}
                style={[styles.macroKey, { borderColor: PAL.border, backgroundColor: PAL.chip }]}
              >
                <Text style={{ color: PAL.chipText, fontFamily: "Menlo", fontSize: 11 }}>{label}</Text>
              </Pressable>
            ))}
          </ScrollView>
        ) : null}

        {/* Prompt input */}
        <View style={[styles.inputRow, { borderTopColor: PAL.border, paddingBottom: insets.bottom || 8 }]}>
          <Text style={{ color: PAL.prompt, fontFamily: "Menlo", fontSize: FONT_SIZE + 1, marginRight: 6 }}>
            {mode === "agent" ? "▶" : "$"}
          </Text>
          <TextInput
            value={input}
            onChangeText={setInput}
            onSubmitEditing={() => void submit()}
            placeholder={busy ? "running…" : mode === "agent" ? "ask the agent…" : "type a command…"}
            placeholderTextColor={PAL.muted}
            autoCorrect={false}
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            multiline={mode === "agent"}
            style={{
              flex: 1,
              color: PAL.fg,
              fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
              fontSize: FONT_SIZE,
              padding: 6,
              maxHeight: 120,
            }}
            editable={!busy || mode === "shell"}
            returnKeyType="send"
            blurOnSubmit={false}
          />
          {busy ? (
            <Pressable onPress={cancel} hitSlop={12}>
              <Text style={[styles.cancelBtn, { color: PAL.err }]}>stop</Text>
            </Pressable>
          ) : null}
        </View>
      </KeyboardAvoidingView>
    </View>
  );
}

// ── Helpers ──────────────────────────────────────────────────────────────

function colorFor(kind: Line["kind"]): string {
  switch (kind) {
    case "user": return PAL.user;
    case "model": return PAL.model;
    case "tool": return PAL.tool;
    case "sys": return PAL.sys;
    case "err": return PAL.err;
  }
}

function buildTerminalWsUrl(): string {
  const base = quicClient.baseUrl.replace(/^http/, "ws");
  const h = quicClient.getAuthHeaders();
  const token = encodeURIComponent((h.Authorization || "").replace("Bearer ", ""));
  return `${base}/ws/terminal?token=${token}`;
}

// eslint-disable-next-line no-control-regex
const ANSI = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[()][0AB]|\x1b[=>NOM78cDEHM]|\x07/g;
function stripAnsi(s: string): string {
  return s.replace(ANSI, "").replace(/\r(?!\n)/g, "");
}

function safeStringify(v: unknown): string {
  if (v == null) return "";
  if (typeof v === "string") return v.length > 200 ? v.slice(0, 200) + "…" : v;
  try {
    const s = JSON.stringify(v);
    return s.length > 200 ? s.slice(0, 200) + "…" : s;
  } catch {
    return String(v);
  }
}

function lastLineMatches(lines: Line[], text: string): boolean {
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i].kind === "model") return lines[i].text.includes(text.slice(0, 80));
  }
  return false;
}

// ── Styles ───────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  root: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: PAL.border,
  },
  headerBtn: { fontSize: 26, fontWeight: "300", paddingHorizontal: 4 },
  headerTitle: { fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }), fontSize: 13, fontWeight: "600" },
  modeChip: {
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 999,
    borderWidth: 1,
  },
  modeChipText: { fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }), fontSize: 11, fontWeight: "600" },
  body: { flex: 1 },
  macroRow: { borderTopWidth: 1, paddingVertical: 6, backgroundColor: "#0a0a0a" },
  macroKey: {
    paddingHorizontal: 10,
    paddingVertical: 6,
    marginHorizontal: 4,
    borderRadius: 6,
    borderWidth: 1,
    minWidth: 38,
    alignItems: "center",
  },
  inputRow: {
    flexDirection: "row",
    alignItems: "center",
    padding: 10,
    borderTopWidth: 1,
    backgroundColor: "#0a0a0a",
  },
  cancelBtn: { fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }), fontSize: 12, paddingHorizontal: 10 },
});
