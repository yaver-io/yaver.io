// Mobile SSH terminal — a full-VT interactive PTY on any of your devices,
// over the agent's /ws/terminal endpoint (so it rides direct LAN / Tailscale /
// Cloudflare Tunnel / relay, whichever the connection negotiated).
//
// Renders with XtermView (xterm.js in a WebView), so full-screen TUIs render
// faithfully — Claude Code's boxed UI, codex, opencode, tmux, vim. One-tap
// launchers type the supported coding agents into the PTY in yolo mode. An
// optional mic dictates a command via on-device whisper. A device picker
// switches which box you're shelled into (e.g. magara) without leaving here.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { useDevice, type Device } from "../src/context/DeviceContext";
import { useAuth } from "../src/context/AuthContext";
import { quicClient } from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";
import XtermView, { type XtermHandle } from "../src/components/XtermView";
import { isTerminalMetaFrame, resizeFrame } from "../src/lib/xtermBridge";
import { AGENT_LAUNCHERS, launchLine, closeLine, type AgentLaunch } from "../src/lib/agentLaunch";
import { isWhisperReady, startRealtimeTranscribe } from "../src/lib/speech";
import { OpenCodeConfigModal } from "../src/components/OpenCodeConfigModal";

function buildTerminalWsUrl(baseUrl: string, token: string): string {
  const ws = baseUrl.replace(/^http/, "ws").replace(/\/+$/, "");
  return `${ws}/ws/terminal?token=${encodeURIComponent(token)}`;
}

// UTF-8 encode for PTY stdin (TextEncoder is present in modern Hermes; the
// ASCII fallback covers the launch commands + typed text regardless).
function encodeUtf8(s: string): Uint8Array {
  try {
    return new TextEncoder().encode(s);
  } catch {
    const out = new Uint8Array(s.length);
    for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i) & 0xff;
    return out;
  }
}

export default function ShellScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { activeDevice, devices, selectDevice, connectionStatus } = useDevice();
  const { token } = useAuth();

  const [status, setStatus] = useState<"idle" | "connecting" | "open" | "closed" | "error">("idle");
  const [error, setError] = useState<string | null>(null);
  const [reconnectNonce, setReconnectNonce] = useState(0);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [dictating, setDictating] = useState(false);
  const [sttAvailable] = useState(() => isWhisperReady());
  // Which runner we believe is open in the PTY (best-effort: tracks what the
  // toggle launched). Reset on disconnect since the PTY is gone.
  const [runningId, setRunningId] = useState<string | null>(null);
  const [opencodeCfgOpen, setOpencodeCfgOpen] = useState(false);

  const wsRef = useRef<WebSocket | null>(null);
  const xtermRef = useRef<XtermHandle | null>(null);
  const sizeRef = useRef<{ cols: number; rows: number }>({ cols: 80, rows: 24 });
  const recRef = useRef<{ stop: () => Promise<string> } | null>(null);

  // ── PTY WebSocket ────────────────────────────────────────────────
  useEffect(() => {
    if (!activeDevice || !token || connectionStatus !== "connected") {
      setStatus("idle");
      return;
    }
    let cancelled = false;
    setStatus("connecting");
    setError(null);

    const ws = new WebSocket(buildTerminalWsUrl(quicClient.baseUrl, token));
    wsRef.current = ws;
    try { (ws as any).binaryType = "arraybuffer"; } catch {}

    ws.onopen = () => {
      if (cancelled) return;
      setStatus("open");
      setRunningId(null); // fresh PTY — nothing running yet
      try {
        ws.send(resizeFrame(sizeRef.current.cols, sizeRef.current.rows));
      } catch {}
    };
    ws.onmessage = (e: WebSocketMessageEvent) => {
      if (cancelled) return;
      const data = e.data as any;
      if (data instanceof ArrayBuffer) {
        xtermRef.current?.write(new Uint8Array(data));
      } else if (typeof data === "string") {
        // Text frames are control/meta (session id, sudo prompt) — don't paint
        // them into the grid. Anything else is treated as output bytes.
        if (!isTerminalMetaFrame(data)) xtermRef.current?.write(encodeUtf8(data));
      }
    };
    ws.onerror = () => {
      if (cancelled) return;
      setStatus("error");
      setRunningId(null);
      setError(
        `Couldn't reach the shell on ${activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name}. Make sure yaver is running on it.`,
      );
    };
    ws.onclose = (e: WebSocketCloseEvent) => {
      if (cancelled) return;
      setStatus("closed");
      setRunningId(null);
      if (e?.code && e.code !== 1000) {
        setError(
          `Shell closed${e.reason ? ` (${e.reason})` : ` (code ${e.code})`}. The agent may have restarted — tap Reconnect.`,
        );
      } else {
        setError(null);
      }
    };

    return () => {
      cancelled = true;
      try { ws.close(); } catch {}
      wsRef.current = null;
    };
  }, [activeDevice, token, connectionStatus, reconnectNonce]);

  const reconnect = useCallback(() => {
    setError(null);
    setStatus("connecting");
    setReconnectNonce((n) => n + 1);
  }, []);

  const sendBytes = useCallback((bytes: Uint8Array) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    try { ws.send(bytes); } catch {}
  }, []);

  const sendText = useCallback((text: string) => sendBytes(encodeUtf8(text)), [sendBytes]);

  // Open/close toggle for a runner. Best-effort state: tapping an idle chip
  // types the launch command; tapping the active chip types its `/exit`.
  const toggleRunner = useCallback(
    (l: AgentLaunch) => {
      if (status !== "open") return;
      if (runningId === l.id) {
        sendText(closeLine(l));
        setRunningId(null);
      } else {
        sendText(launchLine(l));
        setRunningId(l.id);
      }
    },
    [status, runningId, sendText],
  );

  // ── XtermView wiring ─────────────────────────────────────────────
  const onTermData = useCallback((bytes: Uint8Array) => sendBytes(bytes), [sendBytes]);
  const onTermResize = useCallback((cols: number, rows: number) => {
    sizeRef.current = { cols, rows };
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      try { ws.send(resizeFrame(cols, rows)); } catch {}
    }
  }, []);

  // ── Optional dictation (on-device whisper) ───────────────────────
  const startDictation = useCallback(async () => {
    if (dictating) return;
    setDictating(true);
    try {
      recRef.current = await startRealtimeTranscribe(() => {});
    } catch {
      setDictating(false);
      recRef.current = null;
    }
  }, [dictating]);

  const stopDictation = useCallback(async () => {
    const rec = recRef.current;
    recRef.current = null;
    setDictating(false);
    if (!rec) return;
    let text = "";
    try { text = await rec.stop(); } catch {}
    if (text.trim()) sendText(text.trim()); // typed at the prompt; user presses Enter
  }, [sendText]);

  const pickDevice = useCallback(
    async (d: Device) => {
      setPickerOpen(false);
      if (d.id === activeDevice?.id) return;
      try {
        await selectDevice(d); // the connect effect re-runs on activeDevice change
      } catch (e: any) {
        setError(e?.message ?? "device switch failed");
      }
    },
    [activeDevice?.id, selectDevice],
  );

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

  if (activeDevice.needsAuth && !activeDevice.isGuest) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top + 16, paddingHorizontal: 16 }}>
        <AppBackButton onPress={() => router.back()} />
        <View style={{ flex: 1, alignItems: "center", justifyContent: "center", gap: 8 }}>
          <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700", textAlign: "center" }}>
            {activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name}'s agent needs to sign back in
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 13, lineHeight: 19, textAlign: "center" }}>
            The agent is reachable but its Yaver session expired. Go back and tap “Reauth this device”
            on the attention banner, then return here.
          </Text>
          <Pressable onPress={() => router.back()} style={[styles.reconnectBtn, { marginTop: 8 }]}>
            <Text style={styles.reconnectText}>← Back to devices</Text>
          </Pressable>
        </View>
      </View>
    );
  }

  const deviceLabel = activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name;

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: "#0b0d10" }}
      keyboardVerticalOffset={insets.top}
    >
      {/* Header — tap the title to switch device */}
      <View style={[styles.header, { paddingTop: insets.top + 8 }]}>
        <AppBackButton onPress={() => router.back()} />
        <Pressable style={{ flex: 1, marginLeft: 8 }} onPress={() => setPickerOpen(true)}>
          <Text style={styles.headerTitle}>
            Shell · {deviceLabel} {"▾"}
          </Text>
          <Text
            style={{ color: status === "error" ? "#fca5a5" : "#6b7280", fontSize: 11 }}
            numberOfLines={1}
          >
            {status === "open"
              ? "PTY · connected — tap title to switch device"
              : status === "connecting"
                ? "connecting…"
                : status === "closed"
                  ? (error ?? "disconnected")
                  : status === "error"
                    ? (error ?? "connection error")
                    : "idle"}
          </Text>
        </Pressable>
        {status === "connecting" ? (
          <ActivityIndicator color="#6ee7b7" />
        ) : status === "error" || status === "closed" ? (
          <Pressable onPress={reconnect} style={styles.reconnectBtn}>
            <Text style={styles.reconnectText}>Reconnect</Text>
          </Pressable>
        ) : null}
      </View>

      {/* The VT grid */}
      <View style={{ flex: 1 }}>
        <XtermView
          ref={xtermRef}
          onData={onTermData}
          onResize={onTermResize}
          onReady={() => xtermRef.current?.fit()}
          background="#0b0d10"
          foreground="#d1d5db"
          fontSize={13}
        />
      </View>

      {/* Agent launchers */}
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        keyboardShouldPersistTaps="handled"
        style={styles.launchBar}
        contentContainerStyle={{ gap: 8, paddingHorizontal: 10, alignItems: "center" }}
      >
        {AGENT_LAUNCHERS.map((l) => {
          const active = runningId === l.id;
          return (
            <View key={l.id} style={styles.launchGroup}>
              <Pressable
                onPress={() => toggleRunner(l)}
                disabled={status !== "open"}
                style={[
                  styles.launchBtn,
                  active && styles.launchBtnActive,
                  l.id === "opencode" && styles.launchBtnGrouped,
                  status !== "open" && { opacity: 0.4 },
                ]}
                accessibilityLabel={active ? `Exit ${l.label} (sends /exit)` : l.hint}
              >
                <Text style={[styles.launchText, active && styles.launchTextActive]}>
                  {active ? `■ ${l.label}` : `▷ ${l.label}`}
                </Text>
              </Pressable>
              {l.id === "opencode" ? (
                <Pressable
                  onPress={() => setOpencodeCfgOpen(true)}
                  style={styles.gearBtn}
                  accessibilityLabel="OpenCode provider + model settings (GLM key, thinking/doing model)"
                >
                  <Text style={styles.gearText}>⚙</Text>
                </Pressable>
              ) : null}
            </View>
          );
        })}
        <View style={styles.launchDivider} />
        <Pressable onPress={() => sendBytes(new Uint8Array([3]))} disabled={status !== "open"} style={[styles.ctrlBtn, status !== "open" && { opacity: 0.4 }]}>
          <Text style={styles.ctrlText}>^C</Text>
        </Pressable>
        <Pressable onPress={() => sendBytes(new Uint8Array([4]))} disabled={status !== "open"} style={[styles.ctrlBtn, status !== "open" && { opacity: 0.4 }]}>
          <Text style={styles.ctrlText}>^D</Text>
        </Pressable>
        <Pressable onPress={() => sendBytes(new Uint8Array([0x1b]))} disabled={status !== "open"} style={[styles.ctrlBtn, status !== "open" && { opacity: 0.4 }]}>
          <Text style={styles.ctrlText}>Esc</Text>
        </Pressable>
        {sttAvailable ? (
          <Pressable
            onPressIn={startDictation}
            onPressOut={stopDictation}
            disabled={status !== "open"}
            style={[styles.micBtn, dictating && styles.micBtnActive, status !== "open" && { opacity: 0.4 }]}
            accessibilityLabel="Hold to dictate a command"
          >
            <Text style={[styles.micText, dictating && { color: "#0b0d10" }]}>
              {dictating ? "● rec" : "🎙"}
            </Text>
          </Pressable>
        ) : null}
      </ScrollView>

      <View style={{ height: insets.bottom, backgroundColor: "#0b0d10" }} />

      {/* Device picker */}
      <Modal visible={pickerOpen} transparent animationType="fade" onRequestClose={() => setPickerOpen(false)}>
        <Pressable style={styles.modalBackdrop} onPress={() => setPickerOpen(false)}>
          <Pressable style={[styles.modalCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.modalTitle, { color: c.textPrimary }]}>Connect a device</Text>
            <ScrollView style={{ maxHeight: 360 }}>
              {devices.map((d) => {
                const isActive = d.id === activeDevice.id;
                return (
                  <Pressable
                    key={d.id}
                    onPress={() => pickDevice(d)}
                    style={[styles.deviceRow, { borderColor: isActive ? c.accent : c.border }]}
                  >
                    <View style={{ flex: 1 }}>
                      <Text style={{ color: c.textPrimary, fontWeight: "600" }}>
                        {d.alias ? `@${d.alias}` : d.name}
                      </Text>
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>
                        {d.os}{d.online ? " · online" : " · offline"}{isActive ? " · current" : ""}
                      </Text>
                    </View>
                    <View
                      style={{
                        width: 8, height: 8, borderRadius: 4,
                        backgroundColor: d.online ? "#4ade80" : "#6b7280",
                      }}
                    />
                  </Pressable>
                );
              })}
            </ScrollView>
          </Pressable>
        </Pressable>
      </Modal>

      {/* First-class OpenCode config: set a provider (GLM / OpenRouter / Gemini)
          + API key + thinking/doing models, persisted to the box's opencode.json
          via /runner/opencode/config — then launch OpenCode from the toolbar. */}
      <OpenCodeConfigModal
        visible={opencodeCfgOpen}
        onClose={() => setOpencodeCfgOpen(false)}
        startInAddProvider
      />
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  header: {
    paddingHorizontal: 12,
    paddingBottom: 8,
    borderBottomWidth: 1,
    borderBottomColor: "#1f2937",
    flexDirection: "row",
    alignItems: "center",
  },
  headerTitle: { color: "#e5e7eb", fontSize: 14, fontWeight: "700" },
  launchBar: {
    maxHeight: 52,
    borderTopWidth: 1,
    borderTopColor: "#1f2937",
    backgroundColor: "#0e1117",
  },
  launchGroup: { flexDirection: "row", alignItems: "center" },
  launchBtn: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
    backgroundColor: "rgba(124,92,255,0.18)",
    borderWidth: 1,
    borderColor: "rgba(124,92,255,0.5)",
  },
  // When grouped with the gear (OpenCode), square off the right edge so the
  // chip + gear read as one control.
  launchBtnGrouped: { borderTopRightRadius: 0, borderBottomRightRadius: 0 },
  launchBtnActive: { backgroundColor: "#7c5cff", borderColor: "#7c5cff" },
  launchText: { color: "#c4b5fd", fontSize: 13, fontWeight: "700" },
  launchTextActive: { color: "#ffffff" },
  gearBtn: {
    paddingHorizontal: 9,
    paddingVertical: 8,
    borderTopRightRadius: 8,
    borderBottomRightRadius: 8,
    backgroundColor: "rgba(124,92,255,0.10)",
    borderWidth: 1,
    borderLeftWidth: 0,
    borderColor: "rgba(124,92,255,0.5)",
  },
  gearText: { color: "#c4b5fd", fontSize: 13 },
  launchDivider: { width: 1, height: 24, backgroundColor: "#1f2937", marginHorizontal: 2 },
  ctrlBtn: {
    paddingHorizontal: 10,
    paddingVertical: 8,
    borderRadius: 8,
    backgroundColor: "#111827",
    borderWidth: 1,
    borderColor: "#1f2937",
  },
  ctrlText: { color: "#9ca3af", fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", fontSize: 12, fontWeight: "600" },
  micBtn: {
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 8,
    backgroundColor: "#111827",
    borderWidth: 1,
    borderColor: "rgba(16,185,129,0.45)",
  },
  micBtnActive: { backgroundColor: "#6ee7b7", borderColor: "#6ee7b7" },
  micText: { color: "#6ee7b7", fontSize: 13, fontWeight: "700" },
  reconnectBtn: {
    paddingHorizontal: 12,
    paddingVertical: 6,
    borderRadius: 6,
    backgroundColor: "rgba(56,189,248,0.14)",
    borderWidth: 1,
    borderColor: "rgba(56,189,248,0.45)",
  },
  reconnectText: { color: "#7dd3fc", fontSize: 12, fontWeight: "700" },
  modalBackdrop: { flex: 1, backgroundColor: "rgba(0,0,0,0.6)", justifyContent: "center", padding: 24 },
  modalCard: { borderRadius: 14, borderWidth: 1, padding: 16 },
  modalTitle: { fontSize: 15, fontWeight: "700", marginBottom: 12 },
  deviceRow: {
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 1,
    borderRadius: 10,
    padding: 12,
    marginBottom: 8,
  },
});
