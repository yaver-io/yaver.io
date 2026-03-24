import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Animated,
  Dimensions,
  Keyboard,
  PanResponder,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from "react-native";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useAuth } from "../context/AuthContext";
import { useDevice } from "../context/DeviceContext";
import { quicClient } from "../lib/quic";

const BUTTON_SIZE = 46;
const PANEL_WIDTH = 300;

/**
 * Global feedback overlay — draggable indigo "y" debug button.
 * Reads config from AsyncStorage. Appears when Feedback SDK is enabled.
 *
 * Panel auto-positions: opens left when button is near right edge,
 * opens right when near left edge.
 */
export function FeedbackOverlay() {
  const { user, token } = useAuth();
  const { activeDevice, connectionStatus } = useDevice();
  const [enabled, setEnabled] = useState(false);
  const [buttonColor, setButtonColor] = useState("#6366f1");
  const [chatOpen, setChatOpen] = useState(false);
  const [message, setMessage] = useState("");
  const [sending, setSending] = useState(false);
  const [output, setOutput] = useState<string[]>([]);
  const [reloading, setReloading] = useState(false);
  const [fullSize, setFullSize] = useState(false);
  const isDragging = useRef(false);
  const buttonPosX = useRef(0);

  const { width: screenWidth } = Dimensions.get("window");
  const startX = screenWidth - BUTTON_SIZE - 10;
  const pan = useRef(new Animated.ValueXY({ x: startX, y: 90 })).current;

  // Track button X position for panel alignment
  useEffect(() => {
    const id = pan.x.addListener(({ value }) => { buttonPosX.current = value; });
    return () => pan.x.removeListener(id);
  }, [pan.x]);

  const panResponder = useRef(
    PanResponder.create({
      onStartShouldSetPanResponder: () => false,
      onMoveShouldSetPanResponder: (_, gs) =>
        Math.abs(gs.dx) > 6 || Math.abs(gs.dy) > 6,
      onPanResponderGrant: () => {
        pan.extractOffset();
        isDragging.current = false;
      },
      onPanResponderMove: (_, gs) => {
        if (Math.abs(gs.dx) > 6 || Math.abs(gs.dy) > 6) isDragging.current = true;
        Animated.event([null, { dx: pan.x, dy: pan.y }], { useNativeDriver: false })(_, gs);
      },
      onPanResponderRelease: () => { pan.flattenOffset(); isDragging.current = false; },
    })
  ).current;

  // Load config — reset state on re-enable
  useEffect(() => {
    if (!user?.id) return;
    const key = `@yaver/u/${user.id}/feedback_config`;
    const load = async () => {
      try {
        const raw = await AsyncStorage.getItem(key);
        if (!raw) return;
        const cfg = JSON.parse(raw);
        const newEnabled = cfg.enabled === true;
        if (newEnabled && !enabled) {
          // Re-enable: reset chat state
          setChatOpen(false);
          setOutput([]);
          setMessage("");
          setSending(false);
        }
        setEnabled(newEnabled);
        if (cfg.buttonColor) setButtonColor(cfg.buttonColor);
      } catch {}
    };
    load();
    const interval = setInterval(load, 2000);
    return () => clearInterval(interval);
  }, [user?.id, enabled]);

  const agentUrl = connectionStatus === "connected" ? quicClient.baseUrl : null;
  const isConnected = connectionStatus === "connected" && !!agentUrl;

  const addOutput = useCallback((line: string) => {
    setOutput((prev) => [...prev.slice(-8), line]); // keep last 9 lines
  }, []);

  const handleTap = useCallback(() => {
    if (isDragging.current) return;
    setChatOpen((prev) => !prev);
  }, []);

  // Send message → create task → poll for output
  const handleSend = useCallback(async () => {
    if (!message.trim() || !agentUrl || !token) return;
    const msg = message.trim();
    setSending(true);
    setMessage("");
    Keyboard.dismiss();
    addOutput(`> ${msg}`);

    try {
      const resp = await fetch(`${agentUrl}/tasks`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ title: msg, source: "feedback-console" }),
      });
      if (!resp.ok) {
        addOutput(`err: ${resp.status}`);
        setSending(false);
        return;
      }
      const data = await resp.json();
      const taskId = data.taskId ?? data.id ?? data.task?.id;
      if (!taskId) {
        addOutput("task created (no id)");
        setSending(false);
        return;
      }
      addOutput(`task ${taskId} started...`);

      // Poll task output for up to 30s
      let attempts = 0;
      const poll = setInterval(async () => {
        attempts++;
        try {
          const statusResp = await fetch(`${agentUrl}/tasks/${taskId}`, {
            headers: { Authorization: `Bearer ${token}` },
          });
          if (!statusResp.ok) {
            clearInterval(poll);
            setSending(false);
            return;
          }
          const task = await statusResp.json();
          const t = task.task ?? task;

          if (t.status === "completed" || t.status === "failed" || t.status === "stopped") {
            // Get the last bit of output
            const out = t.output ?? t.rawOutput ?? "";
            if (out) {
              const lines = out.split("\n").filter((l: string) => l.trim());
              const last3 = lines.slice(-3);
              for (const l of last3) addOutput(l.slice(0, 60));
            }
            addOutput(t.status === "completed" ? "done." : `${t.status}.`);
            clearInterval(poll);
            setSending(false);
          } else if (attempts >= 15) {
            addOutput("running in background...");
            clearInterval(poll);
            setSending(false);
          }
        } catch {
          clearInterval(poll);
          setSending(false);
        }
      }, 2000);
    } catch (e) {
      addOutput(`fail: ${String(e).slice(0, 40)}`);
      setSending(false);
    }
  }, [message, agentUrl, token, addOutput]);

  // Generic: send a prefixed task to agent and poll output
  const runAgentAction = useCallback(async (label: string, prompt: string) => {
    if (!agentUrl || !token) return;
    addOutput(`> ${label}`);
    setSending(true);
    try {
      const resp = await fetch(`${agentUrl}/tasks`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({
          title: prompt,
          source: "feedback-sdk",
          description: `[Feedback SDK] User triggered "${label}" from the debug console.`,
        }),
      });
      if (!resp.ok) { addOutput(`err: ${resp.status}`); setSending(false); return; }
      const data = await resp.json();
      const taskId = data.taskId ?? data.id ?? data.task?.id;
      if (!taskId) { addOutput("started (no id)"); setSending(false); return; }
      addOutput(`${label}: task ${taskId}...`);

      // Poll output
      let attempts = 0;
      const poll = setInterval(async () => {
        attempts++;
        try {
          const sr = await fetch(`${agentUrl}/tasks/${taskId}`, {
            headers: { Authorization: `Bearer ${token}` },
          });
          if (!sr.ok) { clearInterval(poll); setSending(false); return; }
          const json = await sr.json(); const t = json.task ?? json;
          if (t.status === "completed" || t.status === "failed" || t.status === "stopped") {
            const out = t.output ?? t.rawOutput ?? "";
            if (out) {
              for (const l of out.split("\n").filter((l: string) => l.trim()).slice(-3)) {
                addOutput(l.slice(0, 60));
              }
            }
            addOutput(t.status === "completed" ? "done." : `${t.status}.`);
            clearInterval(poll); setSending(false);
          } else if (attempts >= 30) {
            addOutput("running in background...");
            clearInterval(poll); setSending(false);
          }
        } catch { clearInterval(poll); setSending(false); }
      }, 2000);
    } catch (e) {
      addOutput(`fail: ${String(e).slice(0, 40)}`);
      setSending(false);
    }
  }, [agentUrl, token, addOutput]);

  const isDevBuild = __DEV__;

  const handleReload = useCallback(() => {
    if (isDevBuild) {
      // Dev build: trigger metro hot reload
      runAgentAction("hot-reload", "Hot reload the app. Send the reload signal to the dev server to trigger a fast refresh on the connected device.");
    } else {
      // Release/TestFlight build: rebuild and redeploy
      runAgentAction(
        "rebuild",
        "This is a release build — hot reload is not available. " +
        "Rebuild the app and upload to TestFlight (iOS) and/or Play Store internal testing (Android). " +
        "Use xcodebuild for iOS and gradle for Android — no Expo, use native build tools. " +
        "Auto-increment the build number. Report progress.",
      );
    }
  }, [runAgentAction, isDevBuild]);

  const handleBuild = useCallback(() => {
    runAgentAction(
      "build-deploy",
      "Build and deploy the app using native tools (xcodebuild for iOS, gradle for Android — no Expo). " +
      "iOS: archive and upload to TestFlight, auto-increment CFBundleVersion. " +
      "Android: release AAB and upload to Google Play internal testing, auto-increment versionCode. " +
      "Report progress and result for both.",
    );
  }, [runAgentAction]);

  const handleBugReport = useCallback(async () => {
    if (!agentUrl || !token) return;
    addOutput("> bug report");
    setSending(true);
    try {
      const resp = await fetch(`${agentUrl}/tasks`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({
          title: "Bug report from device — investigate and fix any visible issues on the current screen.",
          source: "feedback-sdk",
          description: "[Feedback SDK] User tapped the bug report button from the debug console.",
        }),
      });
      if (resp.ok) {
        const data = await resp.json();
        const taskId = data.taskId ?? data.id ?? data.task?.id;
        addOutput(taskId ? `bug task ${taskId} created` : "bug report sent");
      } else {
        addOutput(`err: ${resp.status}`);
      }
    } catch (e) {
      addOutput(`fail: ${String(e).slice(0, 40)}`);
    } finally {
      setSending(false);
    }
  }, [agentUrl, token, addOutput]);

  const handleDisable = useCallback(async () => {
    if (!user?.id) return;
    const key = `@yaver/u/${user.id}/feedback_config`;
    const raw = await AsyncStorage.getItem(key);
    const cfg = raw ? JSON.parse(raw) : {};
    cfg.enabled = false;
    await AsyncStorage.setItem(key, JSON.stringify(cfg));
    setEnabled(false);
    setChatOpen(false);
  }, [user?.id]);

  if (!enabled) return null;

  // Panel alignment: if button is in right half, panel opens to the left
  const panelOnLeft = buttonPosX.current > screenWidth / 2;
  const btnBg = isConnected ? buttonColor : `${buttonColor}66`;

  return (
    <Animated.View
      style={[
        styles.root,
        { transform: [{ translateX: pan.x }, { translateY: pan.y }] },
        panelOnLeft ? { alignItems: "flex-end" } : { alignItems: "flex-start" },
      ]}
      {...panResponder.panHandlers}
    >
      {/* Panel */}
      {chatOpen && (
        <View style={[
          fullSize ? styles.panelFull : styles.panel,
          { borderColor: `${buttonColor}44`, shadowColor: buttonColor },
          fullSize && { width: screenWidth - 24, position: "absolute", right: -(screenWidth - BUTTON_SIZE - 24), top: BUTTON_SIZE + 8 },
        ]}>
          {/* Header */}
          <View style={styles.headerRow}>
            <Text style={[styles.headerTitle, { color: buttonColor }]}>yaver debug</Text>
            <View style={[styles.dot, isConnected ? styles.green : styles.red]} />
            <Text style={styles.headerStatus}>{isConnected ? "live" : "off"}</Text>
            <TouchableOpacity onPress={() => setFullSize(!fullSize)} style={styles.xBtn}>
              <Text style={styles.xBtnText}>{fullSize ? "\u25A1" : "\u2197"}</Text>
            </TouchableOpacity>
            <TouchableOpacity onPress={() => { setChatOpen(false); setFullSize(false); }} style={styles.xBtn}>
              <Text style={styles.xBtnText}>{"\u2715"}</Text>
            </TouchableOpacity>
          </View>

          {/* Output area */}
          <View style={[styles.outputArea, fullSize && styles.outputAreaFull]}>
            {output.length > 0 ? output.map((line, i) => (
              <Text key={i} style={[
                styles.outputLine,
                fullSize && styles.outputLineFull,
                line.startsWith(">") && { color: "#9ca3af" },
              ]}>
                {line}
              </Text>
            )) : (
              <Text style={[styles.outputLine, { color: "#333" }]}>
                {isConnected ? "connected. type a message or use actions below." : "not connected to agent."}
              </Text>
            )}
            {sending && <ActivityIndicator color={buttonColor} size="small" style={{ marginTop: 4 }} />}
          </View>

          {/* Input */}
          <View style={styles.inputRow}>
            <Text style={[styles.prompt, { color: buttonColor }]}>&gt;</Text>
            <TextInput
              style={[styles.input, fullSize && styles.inputFull]}
              placeholder="tell the agent..."
              placeholderTextColor="#444"
              value={message}
              onChangeText={setMessage}
              onSubmitEditing={handleSend}
              returnKeyType="send"
              multiline={fullSize}
            />
            <TouchableOpacity
              style={[styles.goBtn, { backgroundColor: buttonColor }, (sending || !message.trim()) && styles.dim]}
              onPress={handleSend}
              disabled={sending || !message.trim() || !isConnected}
            >
              <Text style={styles.goBtnText}>run</Text>
            </TouchableOpacity>
          </View>

          {/* Action cards — Reload | Build | Bug */}
          <View style={styles.cardRow}>
            <TouchableOpacity
              style={[styles.card, fullSize && styles.cardFull, !isConnected && styles.dim]}
              onPress={handleReload}
              disabled={sending || !isConnected}
            >
              <Text style={[styles.cardIcon, { color: "#fbbf24" }]}>{"\u21BB"}</Text>
              <Text style={styles.cardLabel}>Hot Reload</Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={[styles.card, fullSize && styles.cardFull, !isConnected && styles.dim]}
              onPress={handleBuild}
              disabled={sending || !isConnected}
            >
              <Text style={[styles.cardIcon, { color: "#60a5fa" }]}>{"\u2692"}</Text>
              <Text style={styles.cardLabel}>Build</Text>
              <Text style={[styles.cardLabel, { fontSize: 8, color: "#555" }]}>+ Deploy</Text>
            </TouchableOpacity>
            <TouchableOpacity
              style={[styles.card, fullSize && styles.cardFull, !isConnected && styles.dim]}
              onPress={handleBugReport}
              disabled={sending || !isConnected}
            >
              <Text style={[styles.cardIcon, { color: "#f87171" }]}>{"\uD83D\uDC1B"}</Text>
              <Text style={styles.cardLabel}>Report Bug</Text>
            </TouchableOpacity>
          </View>

          {/* Action cards row 2 — Test App */}
          <View style={styles.cardRow}>
            <TouchableOpacity
              style={[styles.card, fullSize && styles.cardFull, !isConnected && styles.dim]}
              onPress={() => {
                runAgentAction(
                  "test-app",
                  "Start an autonomous test session for the app. " +
                  "1. Read the codebase to understand the app structure, screens, and components. " +
                  "2. If this is a release build (not dev), first build a debug/test version using native tools " +
                  "(xcodebuild for iOS, gradle for Android — no Expo) and deploy it to the device/emulator. " +
                  "Name the test build 'test-<appname>' (e.g. test-Yaver). Report: 'preparing test-Yaver.app...' " +
                  "3. Navigate through every screen on the connected device or emulator. " +
                  "Try tapping buttons, filling forms with test data, submitting empty forms, etc. " +
                  "4. When you find errors/crashes, fix them in code. If dev build, hot reload. If release, rebuild and redeploy. " +
                  "5. Do NOT commit any changes — all fixes are staged only. " +
                  "6. After testing all screens, report a summary: screens tested, bugs found, fixes applied with file paths."
                );
              }}
              disabled={sending || !isConnected}
            >
              <Text style={[styles.cardIcon, { color: "#a78bfa" }]}>{"\u25B6"}</Text>
              <Text style={styles.cardLabel}>Test App</Text>
            </TouchableOpacity>
          </View>

          {/* Bottom row */}
          <View style={styles.actionsRow}>
            <TouchableOpacity style={styles.actionBtn} onPress={() => setOutput([])}>
              <Text style={styles.actionText}>clear</Text>
            </TouchableOpacity>
            <TouchableOpacity style={styles.actionBtn} onPress={handleDisable}>
              <Text style={[styles.actionText, { color: "#f87171" }]}>quit</Text>
            </TouchableOpacity>
          </View>
        </View>
      )}

      {/* Button — separate Pressable to avoid PanResponder stealing taps */}
      <Pressable
        style={[styles.button, { backgroundColor: btnBg }]}
        onPress={handleTap}
      >
        <Text style={styles.buttonIcon}>{chatOpen ? "\u2715" : "y"}</Text>
        <View style={[styles.statusDot, isConnected ? styles.green : styles.red]} />
      </Pressable>
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  root: {
    position: "absolute",
    zIndex: 99999,
  },
  button: {
    width: BUTTON_SIZE,
    height: BUTTON_SIZE,
    borderRadius: 12,
    alignItems: "center",
    justifyContent: "center",
    shadowColor: "#000",
    shadowOffset: { width: 0, height: 3 },
    shadowOpacity: 0.5,
    shadowRadius: 5,
    elevation: 10,
  },
  buttonIcon: {
    color: "#fff",
    fontSize: 24,
    fontWeight: "800",
    fontStyle: "italic",
  },
  statusDot: {
    position: "absolute",
    top: -2,
    right: -2,
    width: 10,
    height: 10,
    borderRadius: 5,
    borderWidth: 1.5,
    borderColor: "#000",
  },
  green: { backgroundColor: "#22c55e" },
  red: { backgroundColor: "#ef4444" },
  // Panel — mini
  panel: {
    width: PANEL_WIDTH,
    backgroundColor: "#0a0a0a",
    borderRadius: 12,
    padding: 10,
    marginBottom: 6,
    borderWidth: 1,
    shadowOffset: { width: 0, height: 0 },
    shadowOpacity: 0.2,
    shadowRadius: 12,
    elevation: 12,
  },
  // Panel — full size
  panelFull: {
    backgroundColor: "#0a0a0a",
    borderRadius: 14,
    padding: 14,
    borderWidth: 1,
    shadowOffset: { width: 0, height: 0 },
    shadowOpacity: 0.3,
    shadowRadius: 16,
    elevation: 15,
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    marginBottom: 6,
    gap: 5,
  },
  headerTitle: {
    flex: 1,
    fontSize: 13,
    fontWeight: "800",
    fontStyle: "italic",
  },
  dot: { width: 7, height: 7, borderRadius: 4 },
  headerStatus: { fontSize: 10, color: "#555", fontFamily: "Courier" },
  xBtn: { paddingHorizontal: 6, paddingVertical: 2 },
  xBtnText: { color: "#555", fontSize: 14 },
  // Output
  outputArea: {
    backgroundColor: "#111",
    borderRadius: 8,
    padding: 8,
    marginBottom: 6,
    maxHeight: 140,
  },
  outputLine: {
    fontSize: 11,
    color: "#22c55e",
    fontFamily: "Courier",
    lineHeight: 16,
  },
  // Input
  inputRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
    marginBottom: 6,
  },
  prompt: { fontSize: 16, fontWeight: "700", fontFamily: "Courier" },
  input: {
    flex: 1,
    backgroundColor: "#111",
    borderRadius: 6,
    paddingHorizontal: 8,
    paddingVertical: 7,
    color: "#e5e5e5",
    fontSize: 13,
    fontFamily: "Courier",
    borderWidth: 1,
    borderColor: "#222",
  },
  goBtn: { borderRadius: 6, paddingHorizontal: 12, paddingVertical: 7 },
  goBtnText: { color: "#fff", fontSize: 12, fontWeight: "700", fontFamily: "Courier" },
  dim: { opacity: 0.3 },
  // Actions
  cardRow: { flexDirection: "row", gap: 6, marginBottom: 6 },
  card: {
    flex: 1,
    backgroundColor: "#111",
    borderRadius: 8,
    paddingVertical: 10,
    alignItems: "center",
    borderWidth: 1,
    borderColor: "#1a1a1a",
  },
  cardIcon: { fontSize: 18, marginBottom: 2 },
  cardLabel: { fontSize: 10, color: "#999", fontWeight: "600", fontFamily: "Courier" },
  cardFull: { paddingVertical: 14 },
  // Full-size overrides
  outputAreaFull: { maxHeight: 300, minHeight: 160 },
  outputLineFull: { fontSize: 13, lineHeight: 20 },
  inputFull: { fontSize: 15, paddingVertical: 10 },
  actionsRow: { flexDirection: "row", gap: 4 },
  actionBtn: {
    flex: 1,
    paddingVertical: 6,
    borderRadius: 6,
    alignItems: "center",
    backgroundColor: "#111",
    borderWidth: 1,
    borderColor: "#1a1a1a",
  },
  actionText: { fontSize: 11, color: "#888", fontWeight: "600", fontFamily: "Courier" },
});
