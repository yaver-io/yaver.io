import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
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
import Markdown from "react-native-markdown-display";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useAuth } from "../context/AuthContext";
import { useDevice } from "../context/DeviceContext";
import { quicClient } from "../lib/quic";
import { subscribeFeedbackLaunch } from "../lib/feedbackTrigger";
import { getLocalSecret, getUserSettings, LOCAL_KEYS, type SpeechProvider, type TtsProvider } from "../lib/auth";
import { transcribe, initWhisper, speakText as speakConfiguredText } from "../lib/speech";

const BUTTON_SIZE = 46;
const PANEL_WIDTH = 300;

const ANSI_PATTERN =
  // eslint-disable-next-line no-control-regex
  /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[()][0AB]|\x1b[=>NOM78cDEHM]|\x07/g;

function stripAnsi(text: string): string {
  return String(text || "")
    .replace(ANSI_PATTERN, "")
    .replace(/\[\d+(?:;\d+)*m/g, "");
}

function stripPromptEcho(content: string): string {
  return stripAnsi(content)
    .replace(/^[\s\S]*?OpenAI Codex v[^\n]*\n(?:[\s\S]*?\n)?\s*\n/, "")
    .replace(/^Reading additional input from stdin[.…]*\s*\n?/, "")
    .replace(/\n*\s*tokens used\s*\n?\s*[\d,]+\s*/gi, "\n\n")
    .trim();
}

function extractAssistantActivity(text: string, maxItems = 3): string[] {
  const seen = new Set<string>();
  const items: string[] = [];
  for (const rawLine of text.split("\n").map((line) => line.trim()).filter(Boolean)) {
    const command = rawLine.match(/^\*\*\$\s+(.+?)\*\*$/);
    const normalized = command?.[1]
      ? `$ ${command[1].trim()}`
      : (/^[-*]\s+/.test(rawLine) || /^\d+\.\s+/.test(rawLine)
        ? rawLine.replace(/^[-*]\s+/, "").replace(/^\d+\.\s+/, "").trim()
        : "");
    if (!normalized || normalized.length < 4 || seen.has(normalized)) continue;
    seen.add(normalized);
    items.push(normalized);
  }
  return items.slice(-maxItems);
}

function buildLiveAssistantMarkdown(content: string): string {
  const cleaned = stripPromptEcho(content).replace(/```[\s\S]*?```/g, "\n_Code/details hidden while work continues._\n");
  const visible: string[] = [];
  let hidden = false;
  let chars = 0;
  for (const rawLine of cleaned.split("\n")) {
    const line = rawLine.trim();
    if (!line) {
      if (visible.length > 0 && visible[visible.length - 1] !== "") visible.push("");
      continue;
    }
    if (/^\*\*\$\s+.+\*\*$/.test(line) || /^(workdir|model|provider|approval|sandbox|reasoning effort):/i.test(line)) {
      hidden = true;
      continue;
    }
    if (/^(diff --git|index [0-9a-f]+\.\.[0-9a-f]+|@@ |--- |\+\+\+ )/.test(line)) {
      hidden = true;
      continue;
    }
    if (/^[{}[\];(),.=><:+\-/*\\|'"`_]+$/.test(line)) {
      hidden = true;
      continue;
    }
    visible.push(rawLine);
    chars += rawLine.length;
    if (visible.length >= 10 || chars >= 1200) {
      hidden = true;
      break;
    }
  }
  const body = visible.join("\n").replace(/\n{3,}/g, "\n\n").trim();
  const activity = extractAssistantActivity(cleaned);
  if (!body) {
    return "_Working… implementation details hidden while the task runs._";
  }
  if (!hidden && activity.length === 0) return body;
  return `${body}${activity.length ? `\n\n${activity.map((item) => `- ${item}`).join("\n")}` : ""}\n\n_Working through implementation details…_`.trim();
}

function feedbackMarkdownStyles() {
  return {
    body: {
      color: "#111827",
      fontSize: 13,
      lineHeight: 19,
    },
    paragraph: {
      marginTop: 0,
      marginBottom: 8,
    },
    bullet_list: {
      marginTop: 0,
      marginBottom: 8,
    },
    list_item: {
      color: "#111827",
    },
    code_inline: {
      backgroundColor: "#eef2ff",
      color: "#4338ca",
      borderRadius: 4,
      paddingHorizontal: 4,
      paddingVertical: 1,
    },
    fence: {
      backgroundColor: "#f3f4f6",
      borderRadius: 8,
      padding: 10,
      color: "#374151",
    },
  } as const;
}

/**
 * Global feedback overlay — draggable indigo "y" debug button.
 * Reads config from AsyncStorage. Appears when Feedback SDK is enabled.
 *
 * Panel auto-positions: opens left when button is near right edge,
 * opens right when near left edge.
 */
export function FeedbackOverlay() {
  const { user, token } = useAuth();
  const { activeDevice, connectionStatus, connectedDeviceIds } = useDevice();
  const [enabled, setEnabled] = useState(false);
  const [buttonColor, setButtonColor] = useState("#6366f1");
  const [voiceEnabled, setVoiceEnabled] = useState(true);
  const [speechProvider, setSpeechProvider] = useState<SpeechProvider | null>("on-device");
  const [speechApiKey, setSpeechApiKey] = useState<string | undefined>();
  const [ttsEnabled, setTtsEnabled] = useState(false);
  const [ttsProvider, setTtsProvider] = useState<TtsProvider>("device");
  const [recordingVoice, setRecordingVoice] = useState(false);
  const [transcribingVoice, setTranscribingVoice] = useState(false);
  const [chatOpen, setChatOpen] = useState(false);
  const [message, setMessage] = useState("");
  const [sending, setSending] = useState(false);
  const [output, setOutput] = useState<string[]>([]);
  const [assistantReply, setAssistantReply] = useState("");
  const [taskStatusLine, setTaskStatusLine] = useState("");
  const [reloading, setReloading] = useState(false);
  const [fullSize, setFullSize] = useState(false);
  const isDragging = useRef(false);
  const buttonPosX = useRef(0);
  const voiceRecordingRef = useRef<any>(null);
  // Source of the latest subscribeFeedbackLaunch event. When this is
  // "native-guest-shake" we route handleSend to /vibing/execute (with
  // bundleId + projectName from the loaded guest) instead of the
  // generic /tasks path. Reset on chat close.
  const lastLaunchSource = useRef<string | null>(null);

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
          setAssistantReply("");
          setTaskStatusLine("");
          setSending(false);
        }
        setEnabled(newEnabled);
        if (cfg.buttonColor) setButtonColor(cfg.buttonColor);
        setVoiceEnabled(cfg.voiceEnabled !== false);
        if (cfg.speechProvider === "on-device" || cfg.speechProvider === "openai" || cfg.speechProvider === "deepgram" || cfg.speechProvider === "assemblyai") {
          setSpeechProvider(cfg.speechProvider);
        }
        if (cfg.ttsProvider === "openai" || cfg.ttsProvider === "device") {
          setTtsProvider(cfg.ttsProvider);
        }
      } catch {}
    };
    load();
    const interval = setInterval(load, 2000);
    return () => clearInterval(interval);
  }, [user?.id, enabled]);

  const addOutput = useCallback((line: string) => {
    setOutput((prev) => [...prev.slice(-8), line]); // keep last 9 lines
  }, []);

  useEffect(() => {
    return subscribeFeedbackLaunch(({ source }) => {
      // Implicit opt-in sources bypass the Settings toggle: a shake while a
      // guest bundle is loaded (Hermes push from the agent) or a remote-
      // runtime session is the user telling us they want feedback right
      // now, regardless of whether the floating button is on.
      const isImplicitOptIn = source === "native-guest-shake" || source === "remote-runtime";
      if (!enabled && !isImplicitOptIn) return;
      setChatOpen(true);
      setFullSize(false);
      setAssistantReply("");
      setTaskStatusLine("");
      // Remember the source so handleSend can route to /vibing/execute
      // (with project context) instead of the generic /tasks path. Reset
      // on close.
      lastLaunchSource.current = source;
      addOutput("> feedback opened" + (isImplicitOptIn ? ` (${source})` : ""));
    });
  }, [enabled, addOutput]);

  // Pool-aware gate: feedback sends through the focused client's
  // baseUrl, which is correct as long as ANY pool client is live —
  // when the user has multiple devices pooled, they're probably
  // submitting feedback against whichever is in focus, and a
  // momentary "connected → disconnected" flip on the focused client
  // doesn't actually mean nothing's reachable.
  const hasAnyConnection = connectionStatus === "connected" || connectedDeviceIds.length > 0;
  const agentUrl = hasAnyConnection ? quicClient.baseUrl : null;
  const isConnected = hasAnyConnection && !!agentUrl;

  useEffect(() => {
    if (!token) return;
    getUserSettings(token).then(async (s) => {
      if (s.speechProvider) setSpeechProvider(s.speechProvider);
      if (s.ttsEnabled !== undefined) setTtsEnabled(s.ttsEnabled);
      if (s.ttsProvider === "openai" || s.ttsProvider === "device") setTtsProvider(s.ttsProvider);
      const localSpeechKey = await getLocalSecret(LOCAL_KEYS.speechApiKey);
      if (localSpeechKey) setSpeechApiKey(localSpeechKey);
      else if (s.speechApiKey) setSpeechApiKey(s.speechApiKey);
      else if (s.ttsProvider === "openai") {
        const localOpenAi = await getLocalSecret(LOCAL_KEYS.openAiApiKey);
        if (localOpenAi) setSpeechApiKey(localOpenAi);
        else if (s.openAiApiKey) setSpeechApiKey(s.openAiApiKey);
      }
    }).catch(() => {});
  }, [token]);

  const handleTap = useCallback(() => {
    if (isDragging.current) return;
    setChatOpen((prev) => !prev);
  }, []);

  const toggleVoiceInput = useCallback(async () => {
    if (recordingVoice) {
      setRecordingVoice(false);
      setTranscribingVoice(true);
      try {
        const recording = voiceRecordingRef.current;
        voiceRecordingRef.current = null;
        if (!recording) return;
        await recording.stopAndUnloadAsync();
        const uri = recording.getURI();
        if (!uri) throw new Error("No recording URI");
        if (!speechProvider) throw new Error("Voice input is disabled in Settings.");
        const result = await transcribe(uri, { provider: speechProvider, apiKey: speechApiKey });
        if (result.text) setMessage((prev) => (prev ? `${prev} ${result.text}` : result.text));
      } catch (err) {
        Alert.alert("Transcription failed", err instanceof Error ? err.message : String(err));
      } finally {
        setTranscribingVoice(false);
      }
      return;
    }

    try {
      if (!speechProvider) throw new Error("Voice input is disabled in Settings.");
      if (speechProvider === "on-device") await initWhisper();
      const { Audio } = require("expo-av");
      const perm = await Audio.requestPermissionsAsync();
      if (perm.status !== "granted") throw new Error("Microphone permission is required.");
      await Audio.setAudioModeAsync({ allowsRecordingIOS: true, playsInSilentModeIOS: true });
      const { recording } = await Audio.Recording.createAsync(Audio.RecordingOptionsPresets.HIGH_QUALITY);
      voiceRecordingRef.current = recording;
      setRecordingVoice(true);
    } catch (err) {
      Alert.alert("Voice unavailable", err instanceof Error ? err.message : String(err));
    }
  }, [recordingVoice, speechProvider, speechApiKey]);

  const speakFeedbackResult = useCallback((text: string) => {
    if (!ttsEnabled || !text.trim()) return;
    speakConfiguredText(text, { provider: ttsProvider, apiKey: speechApiKey }).catch(() => {});
  }, [ttsEnabled, ttsProvider, speechApiKey]);

  // Send message → create task → poll for output. When the chat was
  // launched from `native-guest-shake` (user shook while a guest
  // bundle was loaded inside Yaver host), route to /vibing/execute
  // directly with bundleId / projectName context. /vibing/execute
  // wraps the prompt with project info, picks a ready runner, and
  // OnTaskDone auto-fires reload_bundle when the fix commits — none
  // of which the generic /tasks path does without the new
  // feedback_to_vibe.go reshape (agent ≥ 1.99.129).
  const handleSend = useCallback(async () => {
    if (!message.trim() || !agentUrl || !token) return;
    const msg = message.trim();
    setSending(true);
    setMessage("");
    setAssistantReply("");
    setTaskStatusLine("Sending…");
    Keyboard.dismiss();
    addOutput(`Asked: ${msg}`);

    const isGuestShake = lastLaunchSource.current === "native-guest-shake";
    const url = isGuestShake ? `${agentUrl}/vibing/execute` : `${agentUrl}/tasks`;
    const payload = isGuestShake
      ? { prompt: msg }
      : { title: msg, source: "feedback-console" };

    try {
      const resp = await fetch(url, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(payload),
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
      setTaskStatusLine("Working on it…");

      // Poll task output for up to 30s. Vibing tasks live under
      // /vibing/task/{id} (SDK-token-accessible), generic tasks under
      // /tasks/{id} (owner-only).
      const pollPath = isGuestShake ? `${agentUrl}/vibing/task/${taskId}` : `${agentUrl}/tasks/${taskId}`;
      let attempts = 0;
      const poll = setInterval(async () => {
        attempts++;
        try {
          const statusResp = await fetch(pollPath, {
            headers: { Authorization: `Bearer ${token}` },
          });
          if (!statusResp.ok) {
            clearInterval(poll);
            setSending(false);
            return;
          }
          const task = await statusResp.json();
          const t = task.task ?? task;
          const combined = Array.isArray(t.output) ? t.output.join("\n") : String(t.output || t.rawOutput || t.resultText || "");
          if (combined.trim()) {
            setAssistantReply(buildLiveAssistantMarkdown(combined));
          }

          if (t.status === "completed" || t.status === "failed" || t.status === "stopped") {
            setTaskStatusLine(t.status === "completed" ? "Done." : t.status === "failed" ? "Could not finish." : "Stopped.");
            if (t.status === "completed") speakFeedbackResult(String(t.resultText || combined || ""));
            clearInterval(poll);
            setSending(false);
          } else if (attempts >= 15) {
            setTaskStatusLine("Still working…");
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
  }, [message, agentUrl, token, addOutput, speakFeedbackResult]);

  // Generic: send a prefixed task to agent and poll output
  const runAgentAction = useCallback(async (label: string, prompt: string) => {
    if (!agentUrl || !token) return;
    addOutput(label);
    setAssistantReply("");
    setTaskStatusLine("Working on it…");
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
      setTaskStatusLine(`${label} started…`);

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
          const combined = Array.isArray(t.output) ? t.output.join("\n") : String(t.output || t.rawOutput || t.resultText || "");
          if (combined.trim()) {
            setAssistantReply(buildLiveAssistantMarkdown(combined));
          }
          if (t.status === "completed" || t.status === "failed" || t.status === "stopped") {
            setTaskStatusLine(t.status === "completed" ? "Done." : t.status === "failed" ? "Could not finish." : "Stopped.");
            if (t.status === "completed") speakFeedbackResult(String(t.resultText || combined || ""));
            clearInterval(poll); setSending(false);
          } else if (attempts >= 30) {
            setTaskStatusLine("Still working…");
            clearInterval(poll); setSending(false);
          }
        } catch { clearInterval(poll); setSending(false); }
      }, 2000);
    } catch (e) {
      addOutput(`fail: ${String(e).slice(0, 40)}`);
      setSending(false);
    }
  }, [agentUrl, token, addOutput, speakFeedbackResult]);

  const isDevBuild = __DEV__;

  // Hot Reload: directly call /dev/reload-app on the agent (which
  // recompiles a fresh Hermes bundle + broadcasts reload_bundle to the
  // loaded guest via BlackBox SSE). One round trip, no Claude in the
  // loop. Replaces the previous behaviour of POSTing /tasks with a
  // "please hot reload" prompt that asked an LLM to do something a
  // single HTTP call already does.
  const handleReload = useCallback(async () => {
    if (!isConnected) return;
    addOutput("> hot reload");
    setTaskStatusLine("Sending reload…");
    setReloading(true);
    try {
      const ok = await quicClient.reloadDevServer({ mode: "bundle" });
      setTaskStatusLine(ok ? "Reload sent. Waiting for bundle…" : "Reload failed.");
    } catch (e) {
      setTaskStatusLine(`Reload error: ${String(e).slice(0, 40)}`);
    } finally {
      setReloading(false);
    }
  }, [isConnected, addOutput]);

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
    addOutput("Bug report");
    setAssistantReply("");
    setTaskStatusLine("Sending bug report…");
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
        setTaskStatusLine(taskId ? "Bug fix task started…" : "Bug report sent.");
      } else {
        setTaskStatusLine(`Error: ${resp.status}`);
      }
    } catch (e) {
      setTaskStatusLine(`Failed: ${String(e).slice(0, 40)}`);
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

  // Render the overlay any time the chat is open, even if `enabled` is
  // false. The Settings toggle gates the floating draggable button (see
  // below where the button is conditional on `enabled`); when feedback
  // is launched implicitly via a guest-shake event, we open the chat
  // panel directly without showing the persistent button.
  if (!enabled && !chatOpen) return null;

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
            <TouchableOpacity onPress={() => { setChatOpen(false); setFullSize(false); setAssistantReply(""); setTaskStatusLine(""); }} style={styles.xBtn}>
              <Text style={styles.xBtnText}>{"\u2715"}</Text>
            </TouchableOpacity>
          </View>

          {/* Output area */}
          <View style={[styles.outputArea, fullSize && styles.outputAreaFull]}>
            {assistantReply ? (
              <Markdown style={feedbackMarkdownStyles()}>
                {assistantReply}
              </Markdown>
            ) : (
              <Text style={[styles.outputLine, { color: "#333" }]}>
                {isConnected ? "connected. type a message or use actions below." : "not connected to agent."}
              </Text>
            )}
            {taskStatusLine ? (
              <Text style={[styles.outputLine, { color: buttonColor, marginTop: 8 }]}>
                {taskStatusLine}
              </Text>
            ) : null}
            {output.length > 0 ? (
              <Text style={[styles.outputLine, { color: "#6b7280", marginTop: 8 }]}>
                {output[output.length - 1]}
              </Text>
            ) : null}
            {sending && <ActivityIndicator color={buttonColor} size="small" style={{ marginTop: 4 }} />}
          </View>

          {/* Input */}
          <View style={styles.inputRow}>
            <TextInput
              style={[styles.input, fullSize && styles.inputFull]}
              placeholder="Describe the issue or what to change"
              placeholderTextColor="#6b7280"
              value={message}
              onChangeText={setMessage}
              onSubmitEditing={handleSend}
              returnKeyType="send"
              multiline={fullSize}
            />
            {voiceEnabled ? (
              <TouchableOpacity
                style={[
                  styles.micBtn,
                  { borderColor: recordingVoice ? "#ef4444" : "#222" },
                  (sending || transcribingVoice) && styles.dim,
                ]}
                onPress={toggleVoiceInput}
                disabled={sending || transcribingVoice}
              >
                <Text style={[styles.micBtnText, { color: recordingVoice ? "#ef4444" : buttonColor }]}>
                  {transcribingVoice ? "..." : recordingVoice ? "Stop" : "Mic"}
                </Text>
              </TouchableOpacity>
            ) : null}
            <TouchableOpacity
              style={[styles.goBtn, { backgroundColor: buttonColor }, (sending || !message.trim()) && styles.dim]}
              onPress={handleSend}
              disabled={sending || !message.trim() || !isConnected}
            >
              <Text style={styles.goBtnText}>Send</Text>
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
            <TouchableOpacity style={styles.actionBtn} onPress={() => { setOutput([]); setAssistantReply(""); setTaskStatusLine(""); }}>
              <Text style={styles.actionText}>clear</Text>
            </TouchableOpacity>
            <TouchableOpacity style={styles.actionBtn} onPress={handleDisable}>
              <Text style={[styles.actionText, { color: "#f87171" }]}>quit</Text>
            </TouchableOpacity>
          </View>
        </View>
      )}

      {/* Button — separate Pressable to avoid PanResponder stealing taps */}
      {/* Floating draggable button \u2014 only when feedback is toggled on
          in Settings. When chat was opened via a guest-shake (implicit
          opt-in), we skip the persistent button so we don't surprise
          the user with a permanent UI element they didn't ask for. */}
      {enabled ? (
        <Pressable
          style={[styles.button, { backgroundColor: btnBg }]}
          onPress={handleTap}
        >
          <Text style={styles.buttonIcon}>{chatOpen ? "\u2715" : "y"}</Text>
          <View style={[styles.statusDot, isConnected ? styles.green : styles.red]} />
        </Pressable>
      ) : null}
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
    gap: 8,
    marginBottom: 8,
  },
  input: {
    flex: 1,
    backgroundColor: "#111",
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 10,
    color: "#e5e5e5",
    fontSize: 13,
    borderWidth: 1,
    borderColor: "#222",
  },
  goBtn: { borderRadius: 10, paddingHorizontal: 16, paddingVertical: 10 },
  goBtnText: { color: "#fff", fontSize: 13, fontWeight: "700" },
  micBtn: {
    borderRadius: 10,
    paddingHorizontal: 10,
    paddingVertical: 10,
    backgroundColor: "#111",
    borderWidth: 1,
  },
  micBtnText: { fontSize: 12, fontWeight: "700" },
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
