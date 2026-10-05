// Mobile SSH terminal — a full-VT interactive PTY on any of your devices,
// over the agent's /ws/terminal and /ws/runner endpoints (so it rides direct
// LAN / Tailscale / Cloudflare Tunnel / relay, whichever the connection
// negotiated).
//
// Renders with XtermView (xterm.js in a WebView), so full-screen TUIs render
// faithfully — tmux, editors, and any TUI the user starts from their shell.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
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
import { useRouter, useLocalSearchParams } from "expo-router";
import * as ScreenOrientation from "expo-screen-orientation";
import * as WebBrowser from "expo-web-browser";
import { Ionicons } from "@expo/vector-icons";
import QRCode from "react-native-qrcode-svg";
import { useColors } from "../src/context/ThemeContext";
import { useDevice, type Device } from "../src/context/DeviceContext";
import { useAuth } from "../src/context/AuthContext";
import { quicClient, type RecoveryResult } from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";
import XtermView, { type XtermHandle } from "../src/components/XtermView";
import { isTerminalMetaFrame, resizeFrame } from "../src/lib/xtermBridge";
import { isWhisperReady, speakText, startRealtimeTranscribe, stopSpeaking } from "../src/lib/speech";
import NoMachineEmpty from "../src/components/NoMachineEmpty";
import { stripAnsi } from "../src/lib/taskPreview";
import { addTerminalSSHProfile, type TerminalSSHProfile } from "../src/lib/sshProfile";
import {
  getSSHSoftwareKeyboardMode,
  setSSHSoftwareKeyboardMode,
  type SSHSoftwareKeyboardMode,
} from "../src/lib/sshPreferences";

type PTYTarget =
  | { kind: "shell" }
  // An arbitrary tmux session — e.g. "observe this autorun", opened with the
  // autorun's tagged tmuxSession (Epic 7).
  | { kind: "tmux"; sessionName: string };

function buildPTYWsUrl(target: PTYTarget, bearerToken: string, sshProfile?: TerminalSSHProfile, browserSession?: string, launch?: string): string {
  const q = new URLSearchParams({ term: "xterm-256color" });
  if (browserSession) q.set("browser_session", browserSession);
  if (target.kind === "tmux") {
    q.set("name", target.sessionName);
    return quicClient.agentWebSocketUrl("/ws/runner", q, bearerToken);
  }
  addTerminalSSHProfile(q, sshProfile);
  if (launch && launch !== "raw") q.set("launch", launch);
  return quicClient.agentWebSocketUrl("/ws/terminal", q, bearerToken);
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

function decodeUtf8(bytes: Uint8Array): string {
  try {
    return new TextDecoder().decode(bytes);
  } catch {
    return Array.from(bytes, (byte) => String.fromCharCode(byte)).join("");
  }
}

function runnerTmuxSession(launch: string): string {
  switch (launch) {
    case "claude": return "yaver-claude";
    case "codex": return "yaver-codex";
    case "opencode": return "yaver-opencode";
    default: return "";
  }
}

export default function ShellScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const {
    activeDevice,
    devices,
    selectDevice,
    connectionStatus,
    refreshDevices,
    recoverDeviceAuth,
  } = useDevice();
  const { token } = useAuth();

  const [status, setStatus] = useState<"idle" | "connecting" | "open" | "closed" | "error">("idle");
  const [error, setError] = useState<string | null>(null);
  const [reconnectNonce, setReconnectNonce] = useState(0);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [dictating, setDictating] = useState(false);
  const [voiceDraft, setVoiceDraft] = useState("");
  const [speaking, setSpeaking] = useState(false);
  const [sttAvailable] = useState(() => isWhisperReady());
  const [sshSettingsOpen, setSSHSettingsOpen] = useState(false);
  const [softwareKeyboardMode, setSoftwareKeyboardModeState] = useState<SSHSoftwareKeyboardMode>("auto");
  const [fullscreen, setFullscreen] = useState(false);
  const [target, setTarget] = useState<PTYTarget>({ kind: "shell" });
  const [closingSessions, setClosingSessions] = useState(false);
  const [taskFollowUpOnly, setTaskFollowUpOnly] = useState(false);
  const [inputReason, setInputReason] = useState("");
  const [machineAuthRequired, setMachineAuthRequired] = useState(false);
  const [authorizingMachine, setAuthorizingMachine] = useState(false);
  const [authFlow, setAuthFlow] = useState<RecoveryResult | null>(null);
  const [authRecoveryError, setAuthRecoveryError] = useState<string | null>(null);
  const [connectionTrace, setConnectionTrace] = useState<string[]>([]);
  const [tmuxPickerOpen, setTmuxPickerOpen] = useState(false);
  const [tmuxSessions, setTmuxSessions] = useState<Array<{ name: string; attached?: boolean }>>([]);
  const [tmuxBusy, setTmuxBusy] = useState(false);
  const [activeTmuxSession, setActiveTmuxSession] = useState("");

  useEffect(() => {
    getSSHSoftwareKeyboardMode().then(setSoftwareKeyboardModeState).catch(() => {});
  }, []);

  const chooseSoftwareKeyboardMode = useCallback((mode: SSHSoftwareKeyboardMode) => {
    setSoftwareKeyboardModeState(mode);
    void setSSHSoftwareKeyboardMode(mode);
  }, []);

  // Deep-links can pin both the session and the machine. `deviceId` is
  // load-bearing for Tasks: opening an older VPS task must not attach the PTY
  // to whichever pooled client was focused last.
  const routeParams = useLocalSearchParams<{ session?: string; deviceId?: string; source?: string; launch?: string }>();
  const requestedDeviceId = typeof routeParams.deviceId === "string" ? routeParams.deviceId.trim() : "";
  const requestedLaunch = typeof routeParams.launch === "string" ? routeParams.launch.trim().toLowerCase() : "raw";
  const routeSelectAttemptRef = useRef<string | null>(null);
  useEffect(() => {
    const s = typeof routeParams.session === "string" ? routeParams.session.trim() : "";
    if (s) setTarget({ kind: "tmux", sessionName: s });
  }, [routeParams.session]);
  useEffect(() => {
    setActiveTmuxSession(target.kind === "tmux" ? target.sessionName : runnerTmuxSession(requestedLaunch));
  }, [requestedLaunch, target]);

  // Initial open is itself connection intent. Select/connect the exact routed
  // endpoint before the PTY effect is allowed to build a WebSocket. This also
  // makes a cold Tasks → SSH navigation work when no machine was focused yet.
  useEffect(() => {
    if (!requestedDeviceId) return;
    if (activeDevice?.id === requestedDeviceId && connectionStatus === "connected") {
      routeSelectAttemptRef.current = null;
      return;
    }
    const requested = devices.find((device) => device.id === requestedDeviceId);
    if (!requested) {
      if (devices.length > 0) setError("This session's machine is no longer in your Yaver device list.");
      return;
    }
    if (routeSelectAttemptRef.current === requestedDeviceId) return;
    routeSelectAttemptRef.current = requestedDeviceId;
    setStatus("connecting");
    setError(null);
    setMachineAuthRequired(false);
    void selectDevice(requested).catch((e: any) => {
      routeSelectAttemptRef.current = null;
      setStatus("error");
      setError(e?.message ?? `Could not connect to ${requested.name}.`);
    });
  }, [activeDevice?.id, connectionStatus, devices, reconnectNonce, requestedDeviceId, selectDevice]);

  const wsRef = useRef<WebSocket | null>(null);
  const xtermRef = useRef<XtermHandle | null>(null);
  const sizeRef = useRef<{ cols: number; rows: number }>({ cols: 80, rows: 24 });
  const recRef = useRef<{ stop: () => Promise<string> } | null>(null);
  const recentOutputRef = useRef("");

  // A swipe-back/navigation unmount must stop dictation; otherwise the hidden
  // recorder keeps the process on Bluetooth HFP with no visible mic state.
  useEffect(() => () => {
    const recording = recRef.current;
    recRef.current = null;
    if (recording) void recording.stop().catch(() => {});
    stopSpeaking();
  }, []);

  // Fullscreen = landscape + hide chrome, so the VT grid gets the whole screen.
  const toggleFullscreen = useCallback(async () => {
    try {
      if (!fullscreen) {
        await ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.LANDSCAPE);
        setFullscreen(true);
      } else {
        await ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.PORTRAIT_UP);
        setFullscreen(false);
      }
    } catch {
      setFullscreen((f) => !f);
    }
    setTimeout(() => xtermRef.current?.fit(), 350);
  }, [fullscreen]);

  // Always restore portrait when leaving the shell.
  useEffect(() => {
    return () => { ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.PORTRAIT_UP).catch(() => {}); };
  }, []);

  // ── PTY WebSocket ────────────────────────────────────────────────
  useEffect(() => {
    // Do not briefly open a shell on the previously focused machine while a
    // task-owned route is still selecting its endpoint.
    const routeTargetMatches = !requestedDeviceId || activeDevice?.id === requestedDeviceId;
    if (!activeDevice || !routeTargetMatches || !token) {
      setStatus("idle");
      return;
    }
    if (connectionStatus !== "connected") {
      setStatus(connectionStatus === "connecting" ? "connecting" : "error");
      return;
    }
    let cancelled = false;
    let ws: WebSocket | null = null;
    // This is deliberately local to one connection attempt. A 403 from the
    // WebSocket upgrader after this flips true is a transport/origin failure,
    // not an account-auth failure: the same agent already accepted the bearer
    // over REST and minted a path-scoped PTY session.
    let agentAuthorizationAccepted = false;
    setStatus("connecting");
    setError(null);
    setMachineAuthRequired(false);
    setTaskFollowUpOnly(false);
    setInputReason("");

    const authPosture = [
      `transport=${quicClient.connectionMode}`,
      `bearer=${token ? "present" : "missing"}`,
      `relay=${quicClient.activeRelayHttpUrl ? "active" : "not-used"}`,
      `relay-auth=${quicClient.activeRelayHttpUrl ? (quicClient.activeRelayPasswordValue ? "present" : "missing") : "n/a"}`,
    ];
    setConnectionTrace([`preflight · ${authPosture.join(" · ")}`]);
    const openWebSocket = async () => {
      const wsPath = target.kind === "tmux" ? "/ws/runner" : "/ws/terminal";
      const preflight = await quicClient.issueWebSocketSession(wsPath);
      if (cancelled) return;
      if (!preflight.ok || !preflight.token) {
        const detail = [preflight.status ? `HTTP ${preflight.status}` : "transport", preflight.code, preflight.error]
          .filter(Boolean).join(" · ");
        setConnectionTrace((lines) => [...lines.slice(-3), `authorization · rejected · ${detail}`]);
        setStatus("closed");
        setMachineAuthRequired(preflight.status === 401 || preflight.status === 403);
        setError(preflight.error || `Could not authorize SSH on ${activeDevice.name}.`);
        return;
      }
      agentAuthorizationAccepted = true;
      setConnectionTrace((lines) => [...lines.slice(-3), "authorization · accepted · scoped PTY session minted"]);
      const wsUrl = buildPTYWsUrl(target, token || "", activeDevice.sshProfile, preflight.token, requestedLaunch);
      if (__DEV__) {
        // Credential values never enter logs. This posture line makes the
        // otherwise opaque RN WebSocket handshake diagnosable in seconds.
        console.info("[ssh-ws-auth]", {
          transport: quicClient.connectionMode,
          relayPath: Boolean(quicClient.activeRelayHttpUrl),
          relayCredential: Boolean(quicClient.activeRelayPasswordValue),
          bearer: Boolean(token),
          bearerQuery: /[?&]token=[^&]+/.test(wsUrl),
          relayQuery: /[?&]__rp=[^&]+/.test(wsUrl),
        });
      }
      const openedWs: WebSocket = new (WebSocket as any)(
        wsUrl,
        undefined,
        { headers: quicClient.agentWebSocketHeaders(token || "") },
      );
      ws = openedWs;
      wsRef.current = openedWs;
      try { (openedWs as any).binaryType = "arraybuffer"; } catch {}

      openedWs.onopen = () => {
      if (cancelled) return;
      setConnectionTrace((lines) => [...lines.slice(-3), "upgrade · accepted (101)"]);
      setStatus("open");
      xtermRef.current?.focus();
      try {
        openedWs.send(resizeFrame(sizeRef.current.cols, sizeRef.current.rows));
      } catch {}
    };
      openedWs.onmessage = (e: WebSocketMessageEvent) => {
      if (cancelled) return;
      const data = e.data as any;
      if (data instanceof ArrayBuffer) {
        const bytes = new Uint8Array(data);
        xtermRef.current?.write(bytes);
        recentOutputRef.current = (recentOutputRef.current + decodeUtf8(bytes)).slice(-12_000);
      } else if (typeof data === "string") {
        // Text frames are control/meta (session id, sudo prompt) — don't paint
        // them into the grid. Anything else is treated as output bytes.
        if (isTerminalMetaFrame(data)) {
          try {
            const frame = JSON.parse(data);
            if (typeof frame?.error === "string") setError(frame.error);
            if (frame?.type === "runner_pty" && frame?.inputMode === "task-followup") {
              setTaskFollowUpOnly(true);
              setInputReason(typeof frame?.inputReason === "string"
                ? frame.inputReason
                : "This tmux session is view-only here. Continue it from its original session.");
            }
          } catch {}
        } else {
          const bytes = encodeUtf8(data);
          xtermRef.current?.write(bytes);
          recentOutputRef.current = (recentOutputRef.current + data).slice(-12_000);
        }
      }
    };
      openedWs.onerror = () => {
      if (cancelled) return;
      setConnectionTrace((lines) => [...lines.slice(-3), "upgrade · network/handshake error"]);
      setStatus("error");
      setError(
        agentAuthorizationAccepted
          ? `Yaver authorization succeeded, but the PTY connection through ${quicClient.connectionMode} was rejected.`
          : `Couldn't reach the shell on ${activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name}. Make sure yaver is running on it.`,
      );
    };
      openedWs.onclose = (e: WebSocketCloseEvent) => {
      if (cancelled) return;
      const safeReason = String(e.reason || "no server reason").replace(/[\r\n]+/g, " ").slice(0, 120);
      setConnectionTrace((lines) => [...lines.slice(-3), `closed · code=${e.code || 0} · ${safeReason}`]);
      setStatus("closed");
      if (e?.code && e.code !== 1000) {
        const authRejected = !agentAuthorizationAccepted && /\b(?:401|403)\b|unauthori[sz]ed|invalid token/i.test(e.reason || "");
        setMachineAuthRequired(authRejected);
        setError(authRejected
          ? `${activeDevice.name} rejected this Yaver session. Authorize the machine from this phone.`
          : agentAuthorizationAccepted
            ? `Yaver authorization succeeded, but the ${quicClient.connectionMode} PTY upgrade failed${e.reason ? ` (${e.reason})` : ` (code ${e.code})`}.`
          : `Shell closed${e.reason ? ` (${e.reason})` : ` (code ${e.code})`}. Tap Reconnect.`);
      } else {
        setError(null);
      }
    };
    };
    void openWebSocket();

    return () => {
      cancelled = true;
      try { ws?.close(); } catch {}
      wsRef.current = null;
    };
  }, [activeDevice, token, connectionStatus, reconnectNonce, requestedDeviceId, requestedLaunch, target]);

  // Give the VT grid keyboard focus as soon as it is live. On a tablet with a
  // hardware keyboard (the primary SSH-over-tablet shape) the WebView's hidden
  // textarea must hold focus or keystrokes land nowhere, and a device-context
  // guard cannot catch "the grid is rendered but not focused". Re-focus when a
  // session opens and on ready; tapping the grid re-focuses it natively.
  useEffect(() => {
    if (status !== "open") return;
    const t = setTimeout(() => xtermRef.current?.focus(), 50);
    return () => clearTimeout(t);
  }, [status, reconnectNonce, target]);

  const reconnect = useCallback(() => {
    setError(null);
    setStatus("connecting");
    // A routed endpoint connect can fail before a PTY WebSocket exists.
    // Re-arm endpoint selection as well as the socket attempt.
    routeSelectAttemptRef.current = null;
    setReconnectNonce((n) => n + 1);
  }, []);

  const authorizeMachine = useCallback(async () => {
    if (!activeDevice || authorizingMachine) return;
    setAuthorizingMachine(true);
    setAuthRecoveryError(null);
    try {
      // The VPS returns its verification URL over the authenticated REST lane.
      // Keep the OAuth-style approval inside Yaver; once the sheet closes the
      // recovery poll continues and this screen retries the PTY.
      const result = await recoverDeviceAuth(activeDevice, { openBrowser: true });
      if (result?.ok && result.deviceCodeUrl) {
        setAuthFlow(result);
        return;
      }
      if (!result?.ok && !result?.alreadyHealthy) {
        setMachineAuthRequired(true);
        setAuthRecoveryError(result?.error || `Could not authorize ${activeDevice.name}.`);
        return;
      }
      setMachineAuthRequired(false);
      await refreshDevices().catch(() => {});
      setReconnectNonce((n) => n + 1);
    } catch (e: any) {
      setMachineAuthRequired(true);
      setAuthRecoveryError(e?.message || `Could not authorize ${activeDevice.name}.`);
    } finally {
      setAuthorizingMachine(false);
    }
  }, [activeDevice, authorizingMachine, recoverDeviceAuth, refreshDevices]);

  const openAuthorizationPage = useCallback(async () => {
    if (!authFlow?.deviceCodeUrl) return;
    await WebBrowser.openBrowserAsync(authFlow.deviceCodeUrl, {
      dismissButtonStyle: "done",
      controlsColor: "#8b5cf6",
      presentationStyle: WebBrowser.WebBrowserPresentationStyle.PAGE_SHEET,
    });
  }, [authFlow?.deviceCodeUrl]);

  const sendBytes = useCallback((bytes: Uint8Array) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    try { ws.send(bytes); } catch {}
  }, []);

  const sendText = useCallback((text: string) => sendBytes(encodeUtf8(text)), [sendBytes]);

  const exitForegroundTui = useCallback(() => {
    // Always reachable in the header, even when Android's IME covers the
    // bottom controls. Ctrl-C twice exits OpenCode and other foreground TUIs
    // without killing the tmux session or the remote shell beneath it.
    sendBytes(new Uint8Array([3]));
    setTimeout(() => sendBytes(new Uint8Array([3])), 140);
    xtermRef.current?.focus();
  }, [sendBytes]);

  const attachTmuxSession = useCallback((sessionName: string) => {
    if (!/^[A-Za-z0-9_-]{1,48}$/.test(sessionName)) return;
    setTmuxPickerOpen(false);
    setActiveTmuxSession(sessionName);
    sendText(`command -v tmux >/dev/null 2>&1 && exec tmux new-session -A -s ${sessionName}\r`);
  }, [sendText]);

  const openTmuxPicker = useCallback(async () => {
    if (tmuxBusy) return;
    setTmuxBusy(true);
    try {
      const sessions = await quicClient.listTmuxSessions();
      setTmuxSessions(sessions.filter((session) => /^[A-Za-z0-9_-]{1,48}$/.test(session.name)));
      setTmuxPickerOpen(true);
    } catch (e: any) {
      setError(e?.message || "Could not list tmux sessions on this machine.");
    } finally {
      setTmuxBusy(false);
    }
  }, [tmuxBusy]);

  const detachPersistentSession = useCallback(async () => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN || taskFollowUpOnly) return;
    const sessionName = activeTmuxSession || runnerTmuxSession(requestedLaunch);
    if (!sessionName) {
      setError("Attach to a tmux session before detaching it.");
      return;
    }
    setTmuxBusy(true);
    try {
      await quicClient.controlTmuxClient(sessionName, "detach");
      router.replace("/(tabs)/ssh" as any);
    } catch {
      // Compatibility with older agents: send the chord as separate input,
      // and leave enough time for a relayed PTY to process it.
      try { ws.send(new Uint8Array([2])); } catch {}
      setTimeout(() => { try { if (ws.readyState === WebSocket.OPEN) ws.send(encodeUtf8("d")); } catch {} }, 120);
      setTimeout(() => router.replace("/(tabs)/ssh" as any), 1_000);
    } finally {
      setTmuxBusy(false);
    }
  }, [activeTmuxSession, requestedLaunch, router, taskFollowUpOnly]);

  const killActiveTmuxPane = useCallback(() => {
    const sessionName = activeTmuxSession || runnerTmuxSession(requestedLaunch);
    if (!sessionName) {
      setError("Attach to a tmux session before closing its active pane.");
      return;
    }
    Alert.alert("Close active tmux pane?", `This is the same as Ctrl-B, X, then Y in ${sessionName}.`, [
      { text: "Cancel", style: "cancel" },
      { text: "Close pane", style: "destructive", onPress: () => {
        setTmuxBusy(true);
        void quicClient.controlTmuxClient(sessionName, "kill-pane")
          .then(() => router.replace("/(tabs)/ssh" as any))
          .catch((e: any) => setError(e?.message || "Could not close the tmux pane."))
          .finally(() => setTmuxBusy(false));
      } },
    ]);
  }, [activeTmuxSession, requestedLaunch, router]);

  const openShell = useCallback(() => {
    if (target.kind === "shell") return;
    setTarget({ kind: "shell" });
    setReconnectNonce((n) => n + 1);
  }, [target.kind]);

  const summarizeClosed = useCallback((label: string, killed: Array<{ name: string; runner?: string; error?: string }>) => {
    if (killed.length === 0) return `${label}: no Yaver sessions found`;
    return `${label}: ${killed.map((s) => s.runner ? `${s.name} (${s.runner})` : s.name).join(", ")}`;
  }, []);

  const closeSelectedSessions = useCallback(() => {
    if (!activeDevice || closingSessions) return;
    const label = activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name;
    Alert.alert("Close Yaver sessions?", `This ends all Yaver runner and shell sessions on ${label}.`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Close sessions",
        style: "destructive",
        onPress: async () => {
          setClosingSessions(true);
          try {
            const res = await quicClient.closeTmuxSessions(activeDevice.id);
            Alert.alert(res.ok ? "Sessions closed" : "Close failed", res.error || summarizeClosed(label, res.killed));
            setTarget({ kind: "shell" });
            setReconnectNonce((n) => n + 1);
          } catch (e: any) {
            Alert.alert("Close failed", e?.message ?? "Could not close sessions.");
          } finally {
            setClosingSessions(false);
          }
        },
      },
    ]);
  }, [activeDevice, closingSessions, summarizeClosed]);

  const closeAllMachineSessions = useCallback(() => {
    if (closingSessions) return;
    const targets = devices.filter((d) => d.online && !d.needsAuth);
    if (targets.length === 0) {
      Alert.alert("No online machines", "No online owned machines are available.");
      return;
    }
    Alert.alert("Close sessions on all machines?", `This ends Yaver sessions on ${targets.length} online owned machine${targets.length === 1 ? "" : "s"}.`, [
      { text: "Cancel", style: "cancel" },
      {
        text: "Close all",
        style: "destructive",
        onPress: async () => {
          setClosingSessions(true);
          try {
            const lines: string[] = [];
            for (const d of targets) {
              const label = d.alias ? `@${d.alias}` : d.name;
              try {
                const res = await quicClient.closeTmuxSessions(d.id);
                lines.push(res.error ? `${label}: ${res.error}` : summarizeClosed(label, res.killed));
              } catch (e: any) {
                lines.push(`${label}: ${e?.message ?? "failed"}`);
              }
            }
            Alert.alert("Close sessions", lines.join("\n"));
            setTarget({ kind: "shell" });
            setReconnectNonce((n) => n + 1);
          } finally {
            setClosingSessions(false);
          }
        },
      },
    ]);
  }, [closingSessions, devices, summarizeClosed]);

  // ── XtermView wiring ─────────────────────────────────────────────
  const onTermData = useCallback((bytes: Uint8Array) => {
    if (!taskFollowUpOnly) sendBytes(bytes);
  }, [sendBytes, taskFollowUpOnly]);
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
    if (!sttAvailable) {
      setError("STT is not ready on this tablet. Download the on-device speech model in Settings → Voice.");
      return;
    }
    setDictating(true);
    setVoiceDraft("");
    try {
      recRef.current = await startRealtimeTranscribe((partial) => setVoiceDraft(partial));
    } catch {
      setDictating(false);
      recRef.current = null;
    }
  }, [dictating, sttAvailable]);

  const stopDictation = useCallback(async () => {
    const rec = recRef.current;
    recRef.current = null;
    setDictating(false);
    if (!rec) return;
    let text = "";
    try { text = await rec.stop(); } catch {}
    if (text.trim()) setVoiceDraft(text.trim());
  }, [sendText]);

  const sendVoiceDraft = useCallback(() => {
    const command = voiceDraft.trim();
    if (!command || status !== "open" || taskFollowUpOnly) return;
    sendText(`${command}\r`);
    setVoiceDraft("");
    xtermRef.current?.focus();
  }, [sendText, status, taskFollowUpOnly, voiceDraft]);

  const readRecentOutput = useCallback(async () => {
    if (speaking) {
      stopSpeaking();
      setSpeaking(false);
      return;
    }
    const readable = stripAnsi(recentOutputRef.current)
      .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g, "")
      .trim()
      .slice(-1_500);
    if (!readable) {
      setError("There is no terminal output to read yet.");
      return;
    }
    setSpeaking(true);
    setError(null);
    try {
      await speakText(readable, { provider: "device" });
    } catch (e: any) {
      setError(e?.message || "Text-to-speech is unavailable on this tablet.");
    } finally {
      setSpeaking(false);
    }
  }, [speaking]);

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

  // No auto-connected machine (yet). Do NOT dead-end: the same auto-connect
  // sweep Tasks relies on is already running globally, so narrate it and offer
  // the same inline machine picker. Picking one sets activeDevice, which the
  // PTY effect above observes and opens the shell automatically — the shell
  // rides the Tasks auto-connect exactly as before.
  if (!activeDevice) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top + 12, paddingHorizontal: 12 }}>
        <AppBackButton onPress={() => router.back()} />
        <View style={{ flex: 1, paddingTop: 8 }}>
          <NoMachineEmpty noun="shells" />
        </View>
      </View>
    );
  }

  // A task-owned deep link must wait for its exact machine instead of briefly
  // opening the currently focused box. The ordinary no-machine state above
  // keeps the shared auto-connect/picker experience from upstream.
  if (requestedDeviceId && activeDevice.id !== requestedDeviceId) {
    const requested = devices.find((device) => device.id === requestedDeviceId);
    return (
      <View style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top + 16, paddingHorizontal: 16 }}>
        <AppBackButton label="Exit SSH" onPress={() => router.replace("/(tabs)/ssh" as any)} />
        <View style={{ flex: 1, alignItems: "center", justifyContent: "center", width: "100%" }}>
          <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "600", marginBottom: 6 }}>
            {requested ? `Connecting to ${requested.alias ? `@${requested.alias}` : requested.name}…` : "Choose a machine for SSH"}
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center" }}>
            {error || (requested
              ? "Opening its encrypted interactive PTY."
              : "This session's machine is no longer in your Yaver device list.")}
          </Text>
        </View>
      </View>
    );
  }

  if (activeDevice.needsAuth) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg, paddingTop: insets.top + 16, paddingHorizontal: 16 }}>
        <AppBackButton label="Exit SSH" onPress={() => router.replace("/(tabs)/ssh" as any)} />
        <View style={{ flex: 1, alignItems: "center", justifyContent: "center", gap: 8 }}>
          <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700", textAlign: "center" }}>
            {activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name}'s agent needs to sign back in
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 13, lineHeight: 19, textAlign: "center" }}>
            The agent is reachable but its Yaver session expired. Go back and tap “Reauth this device”
            on the attention banner, then return here.
          </Text>
          <Pressable onPress={() => router.replace("/(tabs)/ssh" as any)} style={[styles.reconnectBtn, { marginTop: 8 }]}>
            <Text style={styles.reconnectText}>Exit SSH</Text>
          </Pressable>
        </View>
      </View>
    );
  }

  const deviceLabel = activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name;
  const surfaceLabel =
    target.kind === "tmux"
        ? `${target.sessionName} · Yaver session`
        : "PTY";

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : "height"}
      style={{ flex: 1, backgroundColor: "#0b0d10" }}
      keyboardVerticalOffset={insets.top}
    >
      {/* Header — tap the title to switch device */}
      {!fullscreen ? (
      <View style={[styles.header, { paddingTop: insets.top + 8 }]}>
        <AppBackButton label="Exit SSH" onPress={() => router.replace("/(tabs)/ssh" as any)} />
        <Pressable style={{ flex: 1, marginLeft: 8 }} onPress={() => setPickerOpen(true)}>
          <Text style={styles.headerTitle}>
            Shell · {deviceLabel} {"▾"}
          </Text>
          <Text
            style={{ color: status === "error" ? "#fca5a5" : "#6b7280", fontSize: 11 }}
            numberOfLines={1}
          >
            {status === "open"
              ? `${surfaceLabel} · connected — tap title to switch device`
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
        ) : (status === "error" || status === "closed") && !machineAuthRequired ? (
          <Pressable onPress={reconnect} style={styles.reconnectBtn}>
            <Text style={styles.reconnectText}>Reconnect</Text>
          </Pressable>
        ) : null}
        {status === "open" ? (
          <Pressable onPress={exitForegroundTui} style={styles.headerExit} accessibilityLabel="Exit the foreground terminal app">
            <Text style={styles.headerExitText}>Exit TUI</Text>
          </Pressable>
        ) : null}
        <Pressable onPress={() => setSSHSettingsOpen(true)} style={styles.fsToggle} accessibilityLabel="SSH settings">
          <Ionicons name="options-outline" size={19} color="#9ca3af" />
        </Pressable>
        <Pressable onPress={toggleFullscreen} style={styles.fsToggle} accessibilityLabel="Fullscreen (landscape)">
          <Ionicons name="expand-outline" size={19} color="#9ca3af" />
        </Pressable>
      </View>
      ) : null}

      {/* The VT grid */}
      <View style={{ flex: 1 }}>
        <XtermView
          ref={xtermRef}
          onData={onTermData}
          onResize={onTermResize}
          onReady={() => {
            xtermRef.current?.fit();
            // Capture a paired/hardware keyboard immediately. The WebView
            // bridge focuses xterm on boot too; this native-side call covers
            // delayed iPad layout and route transitions.
            xtermRef.current?.focus();
          }}
          background="#0b0d10"
          foreground="#d1d5db"
          fontSize={13}
          softwareKeyboardMode={softwareKeyboardMode}
        />
        {machineAuthRequired && !fullscreen ? (
          <View style={styles.authWidgetWrap} pointerEvents="box-none">
            <View style={styles.authWidget}>
              <View style={styles.authWidgetIcon}>
                <Ionicons name="key-outline" size={22} color="#7dd3fc" />
              </View>
              <View style={{ flex: 1 }}>
                <Text style={styles.authWidgetTitle}>Yaver authorization · {deviceLabel}</Text>
                <Text style={styles.authWidgetBody}>
                  {authFlow?.deviceCodeUrl
                    ? "Scan with your phone, or open the Yaver authorization page on this device. SSH reconnects after approval."
                    : "This is Yaver account authorization for the remote machine—not Linux or SSH credentials."}
                </Text>
                {authFlow?.deviceCodeUrl ? (
                  <View style={styles.authFlowRow}>
                    <View style={styles.authQr}>
                      <QRCode value={authFlow.deviceCodeUrl} size={132} backgroundColor="#fff" color="#000" />
                    </View>
                    <View style={{ flex: 1 }}>
                      <Text style={styles.authCodeLabel}>YAVER CODE</Text>
                      <Text selectable style={styles.authCode}>{authFlow.userCode || "Open link"}</Text>
                      <Pressable onPress={() => { void openAuthorizationPage(); }} style={styles.authLinkAction}>
                        <Text style={styles.authLinkText}>Open authorization page</Text>
                      </Pressable>
                    </View>
                  </View>
                ) : null}
                {authRecoveryError ? <Text style={styles.authWidgetError}>{authRecoveryError}</Text> : null}
                {connectionTrace.length ? (
                  <View style={styles.authTrace}>
                    {connectionTrace.map((line, index) => (
                      <Text key={`${index}:${line}`} selectable style={styles.authTraceLine}>{line}</Text>
                    ))}
                  </View>
                ) : null}
                {!authFlow?.deviceCodeUrl ? <Pressable
                  accessibilityRole="button"
                  accessibilityLabel={`Authorize ${deviceLabel} with Yaver`}
                  onPress={() => { void authorizeMachine(); }}
                  disabled={authorizingMachine}
                  style={({ pressed }) => [
                    styles.authWidgetAction,
                    { opacity: authorizingMachine ? 0.55 : pressed ? 0.8 : 1 },
                  ]}
                >
                  {authorizingMachine ? <ActivityIndicator size="small" color="#071018" /> : null}
                  <Text style={styles.authWidgetActionText}>
                    {authorizingMachine ? "Starting Yaver authorization…" : "Start Yaver authorization"}
                  </Text>
                </Pressable> : null}
              </View>
            </View>
          </View>
        ) : null}
        {!machineAuthRequired && (status === "error" || status === "closed") && connectionTrace.length && !fullscreen ? (
          <View style={styles.authWidgetWrap} pointerEvents="box-none">
            <View style={[styles.authWidget, styles.transportWidget]}>
              <View style={[styles.authWidgetIcon, styles.transportWidgetIcon]}>
                <Ionicons name="git-network-outline" size={22} color="#fbbf24" />
              </View>
              <View style={{ flex: 1 }}>
                <Text style={styles.authWidgetTitle}>PTY connection · {deviceLabel}</Text>
                <Text style={styles.authWidgetBody}>{error || "The terminal transport closed."}</Text>
                <View style={styles.authTrace}>
                  {connectionTrace.map((line, index) => (
                    <Text key={`${index}:${line}`} selectable style={styles.authTraceLine}>{line}</Text>
                  ))}
                </View>
                <Pressable onPress={reconnect} style={styles.transportRetryAction}>
                  <Text style={styles.transportRetryText}>Retry PTY connection</Text>
                </Pressable>
              </View>
            </View>
          </View>
        ) : null}
        {taskFollowUpOnly && !fullscreen ? (
          <View style={styles.observeBanner}>
            <Text style={styles.observeText} numberOfLines={2}>
              View only · {inputReason || "Continue this tmux session from its original terminal."}
            </Text>
          </View>
        ) : null}
        {fullscreen ? (
          <Pressable onPress={toggleFullscreen} style={[styles.fsExit, { top: insets.top + 6 }]} accessibilityLabel="Exit fullscreen">
            <Text style={{ color: "#fff", fontSize: 14 }}>✕</Text>
          </Pressable>
        ) : null}
      </View>

      {/* Voice is a transient texting layer over the current PTY/tmux pane.
          It appears only while dictating or reviewing the transcript; Send
          writes the command + Enter to the same WebSocket as hardware keys. */}
      {!fullscreen && (dictating || voiceDraft.trim()) ? (
        <View style={styles.voiceComposer}>
          <Text style={styles.voiceDraft} numberOfLines={2}>
            {voiceDraft.trim() || "Listening…"}
          </Text>
          {!dictating ? (
            <>
              <Pressable
                onPress={() => setVoiceDraft("")}
                style={styles.voiceComposerButton}
                accessibilityLabel="Discard dictated command"
              >
                <Text style={styles.voiceComposerSecondary}>Cancel</Text>
              </Pressable>
              <Pressable
                onPress={sendVoiceDraft}
                style={[styles.voiceComposerButton, styles.voiceComposerSend]}
                accessibilityLabel="Send dictated command to the current terminal pane"
              >
                <Text style={styles.voiceComposerSendText}>Send</Text>
              </Pressable>
            </>
          ) : null}
        </View>
      ) : null}

      {/* Agent launchers */}
      {!fullscreen ? (
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        keyboardShouldPersistTaps="handled"
        style={styles.launchBar}
        contentContainerStyle={{ gap: 8, paddingHorizontal: 10, alignItems: "center" }}
      >
        <Pressable
          onPress={openShell}
          disabled={target.kind === "shell"}
          style={[styles.ctrlBtn, target.kind === "shell" && styles.ctrlBtnActive]}
          accessibilityLabel="Open a regular shell"
        >
          <Text style={[styles.ctrlText, target.kind === "shell" && styles.ctrlTextActive]}>Shell</Text>
        </Pressable>
        <Pressable
          onPressIn={startDictation}
          onPressOut={stopDictation}
          disabled={status !== "open" || taskFollowUpOnly}
          style={[styles.micBtn, dictating && styles.micBtnActive, (status !== "open" || taskFollowUpOnly) && { opacity: 0.4 }]}
          accessibilityLabel={sttAvailable ? "Hold to dictate a terminal command" : "Set up speech-to-text"}
        >
          <Ionicons name={dictating ? "mic" : "mic-outline"} size={18} color={dictating ? "#0b0d10" : "#6ee7b7"} />
        </Pressable>
        <Pressable
          onPress={() => { void readRecentOutput(); }}
          disabled={status !== "open"}
          style={[styles.ctrlBtn, speaking && styles.ctrlBtnActive, status !== "open" && { opacity: 0.4 }]}
          accessibilityLabel={speaking ? "Stop reading terminal output" : "Read recent terminal output aloud"}
        >
          <Ionicons name={speaking ? "stop-circle-outline" : "volume-medium-outline"} size={18} color={speaking ? "#ffffff" : "#9ca3af"} />
        </Pressable>
        <View style={styles.launchDivider} />
        <Pressable onPress={() => sendBytes(new Uint8Array([2]))} disabled={status !== "open" || taskFollowUpOnly} style={[styles.ctrlBtn, (status !== "open" || taskFollowUpOnly) && { opacity: 0.4 }]} accessibilityLabel="Send the tmux Ctrl-B prefix">
          <Text style={styles.ctrlText}>^B</Text>
        </Pressable>
        <Pressable onPress={() => { void openTmuxPicker(); }} disabled={status !== "open" || taskFollowUpOnly || tmuxBusy} style={[styles.ctrlBtn, (status !== "open" || taskFollowUpOnly || tmuxBusy) && { opacity: 0.4 }]} accessibilityLabel="List and attach to a tmux session">
          <Text style={styles.ctrlText}>{tmuxBusy ? "…" : "Attach"}</Text>
        </Pressable>
        <Pressable onPress={() => { void detachPersistentSession(); }} disabled={status !== "open" || taskFollowUpOnly || tmuxBusy} style={[styles.ctrlBtn, (status !== "open" || taskFollowUpOnly || tmuxBusy) && { opacity: 0.4 }]} accessibilityLabel="Detach from tmux and keep the session running">
          <Text style={styles.ctrlText}>Detach</Text>
        </Pressable>
        <Pressable onPress={killActiveTmuxPane} disabled={status !== "open" || taskFollowUpOnly || tmuxBusy} style={[styles.ctrlBtn, (status !== "open" || taskFollowUpOnly || tmuxBusy) && { opacity: 0.4 }]} accessibilityLabel="Close active tmux pane with confirmation">
          <Text style={styles.ctrlText}>Kill pane</Text>
        </Pressable>
        <Pressable onPress={() => sendBytes(new Uint8Array([3]))} disabled={status !== "open" || taskFollowUpOnly} style={[styles.ctrlBtn, (status !== "open" || taskFollowUpOnly) && { opacity: 0.4 }]}>
          <Text style={styles.ctrlText}>^C</Text>
        </Pressable>
        <Pressable onPress={() => sendBytes(new Uint8Array([4]))} disabled={status !== "open" || taskFollowUpOnly} style={[styles.ctrlBtn, (status !== "open" || taskFollowUpOnly) && { opacity: 0.4 }]}>
          <Text style={styles.ctrlText}>^D</Text>
        </Pressable>
        <Pressable onPress={() => sendBytes(new Uint8Array([0x1b]))} disabled={status !== "open" || taskFollowUpOnly} style={[styles.ctrlBtn, (status !== "open" || taskFollowUpOnly) && { opacity: 0.4 }]}>
          <Text style={styles.ctrlText}>Esc</Text>
        </Pressable>
      </ScrollView>
      ) : null}

      {!fullscreen ? <View style={{ height: insets.bottom, backgroundColor: "#0b0d10" }} /> : null}

      <Modal visible={tmuxPickerOpen} transparent animationType="fade" onRequestClose={() => setTmuxPickerOpen(false)}>
        <Pressable style={styles.modalBackdrop} onPress={() => setTmuxPickerOpen(false)}>
          <Pressable style={[styles.modalCard, { backgroundColor: c.bgCard, borderColor: c.border }]} onPress={(event) => event.stopPropagation()}>
            <Text style={[styles.modalTitle, { color: c.textPrimary }]}>Tmux sessions</Text>
            <Pressable onPress={() => attachTmuxSession("yaver-shell")} style={[styles.deviceRow, { borderColor: c.border }]}>
              <Text style={{ color: c.textPrimary, fontWeight: "600" }}>New or resume yaver-shell</Text>
            </Pressable>
            <ScrollView style={{ maxHeight: 320 }}>
              {tmuxSessions.filter((session) => session.name !== "yaver-shell").map((session) => (
                <Pressable key={session.name} onPress={() => attachTmuxSession(session.name)} style={[styles.deviceRow, { borderColor: c.border }]}>
                  <Text style={{ flex: 1, color: c.textPrimary, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" }}>{session.name}</Text>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>{session.attached ? "attached" : "ready"}</Text>
                </Pressable>
              ))}
            </ScrollView>
          </Pressable>
        </Pressable>
      </Modal>

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

      <Modal visible={sshSettingsOpen} transparent animationType="fade" onRequestClose={() => setSSHSettingsOpen(false)}>
        <Pressable style={styles.modalBackdrop} onPress={() => setSSHSettingsOpen(false)}>
          <Pressable style={[styles.modalCard, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.modalTitle, { color: c.textPrimary }]}>SSH settings</Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 10 }}>
              Software keyboard
            </Text>
            {([
              { mode: "auto" as const, label: "Auto", detail: "Hide when a USB or Bluetooth keyboard is attached" },
              { mode: "never" as const, label: "Never show", detail: "Voice or hardware keyboard only" },
              { mode: "always" as const, label: "Always show", detail: "Keep Samsung's touch keyboard available" },
            ]).map((option) => {
              const selected = softwareKeyboardMode === option.mode;
              return (
                <Pressable
                  key={option.mode}
                  onPress={() => chooseSoftwareKeyboardMode(option.mode)}
                  accessibilityRole="radio"
                  accessibilityState={{ selected }}
                  style={[styles.deviceRow, { borderColor: selected ? c.accent : c.border }]}
                >
                  <View style={{ flex: 1 }}>
                    <Text style={{ color: selected ? c.accent : c.textPrimary, fontWeight: "700" }}>{option.label}</Text>
                    <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>{option.detail}</Text>
                  </View>
                  <Text style={{ color: selected ? c.accent : c.textMuted }}>{selected ? "●" : "○"}</Text>
                </Pressable>
              );
            })}
            <View style={{ height: 1, backgroundColor: c.border, marginVertical: 10 }} />
            <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 8 }}>Session cleanup</Text>
            <View style={{ flexDirection: "row", gap: 8 }}>
              <Pressable
                onPress={closeSelectedSessions}
                disabled={closingSessions}
                style={[styles.dangerBtn, { flex: 1 }, closingSessions && { opacity: 0.45 }]}
              >
                <Text style={styles.dangerText}>Close on this machine</Text>
              </Pressable>
              <Pressable
                onPress={closeAllMachineSessions}
                disabled={closingSessions}
                style={[styles.dangerBtn, { flex: 1 }, closingSessions && { opacity: 0.45 }]}
              >
                <Text style={styles.dangerText}>Close on all machines</Text>
              </Pressable>
            </View>
          </Pressable>
        </Pressable>
      </Modal>
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
  headerExit: {
    borderWidth: 1,
    borderColor: "#374151",
    borderRadius: 8,
    paddingHorizontal: 9,
    paddingVertical: 6,
    marginLeft: 8,
  },
  headerExitText: { color: "#d1d5db", fontSize: 11, fontWeight: "700" },
  voiceComposer: {
    minHeight: 48,
    paddingHorizontal: 10,
    paddingVertical: 7,
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: "#29313d",
    backgroundColor: "#11151b",
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  voiceDraft: { flex: 1, color: "#d1d5db", fontSize: 13, lineHeight: 18 },
  voiceComposerButton: { paddingHorizontal: 10, paddingVertical: 7, borderRadius: 8 },
  voiceComposerSecondary: { color: "#9ca3af", fontSize: 12, fontWeight: "600" },
  voiceComposerSend: { backgroundColor: "#6ee7b7" },
  voiceComposerSendText: { color: "#0b0d10", fontSize: 12, fontWeight: "800" },
  headerTitle: { color: "#e5e7eb", fontSize: 14, fontWeight: "700" },
  fsToggle: { paddingHorizontal: 8, paddingVertical: 4, marginLeft: 4 },
  fsExit: { position: "absolute", right: 10, backgroundColor: "rgba(0,0,0,0.5)", borderRadius: 8, paddingHorizontal: 12, paddingVertical: 8 },
  observeBanner: { position: "absolute", left: 10, right: 10, bottom: 10, borderRadius: 8, borderWidth: 1, borderColor: "rgba(251,191,36,0.45)", backgroundColor: "rgba(69,43,8,0.94)", paddingHorizontal: 10, paddingVertical: 8 },
  observeText: { color: "#fde68a", fontSize: 12, lineHeight: 17 },
  authWidgetWrap: { position: "absolute", inset: 0, alignItems: "center", justifyContent: "center", padding: 20 },
  authWidget: { width: "100%", maxWidth: 520, flexDirection: "row", gap: 13, borderWidth: 1, borderColor: "rgba(56,189,248,0.38)", borderRadius: 16, backgroundColor: "rgba(15,23,34,0.97)", padding: 17 },
  transportWidget: { borderColor: "rgba(251,191,36,0.34)" },
  authWidgetIcon: { width: 42, height: 42, borderRadius: 12, alignItems: "center", justifyContent: "center", backgroundColor: "rgba(56,189,248,0.12)" },
  transportWidgetIcon: { backgroundColor: "rgba(251,191,36,0.10)" },
  authWidgetTitle: { color: "#e5e7eb", fontSize: 16, fontWeight: "800" },
  authWidgetBody: { color: "#9ca3af", fontSize: 12, lineHeight: 18, marginTop: 5 },
  authWidgetError: { color: "#fca5a5", fontSize: 11, lineHeight: 16, marginTop: 8 },
  authTrace: { marginTop: 9, padding: 9, borderRadius: 8, backgroundColor: "rgba(2,6,23,0.72)", gap: 3 },
  authTraceLine: { color: "#94a3b8", fontFamily: "monospace", fontSize: 10, lineHeight: 14 },
  authWidgetAction: { minHeight: 42, marginTop: 13, borderRadius: 10, backgroundColor: "#7dd3fc", flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 8, paddingHorizontal: 14 },
  authWidgetActionText: { color: "#071018", fontSize: 13, fontWeight: "800" },
  authFlowRow: { flexDirection: "row", alignItems: "center", gap: 16, marginTop: 14 },
  authQr: { padding: 8, borderRadius: 10, backgroundColor: "#fff" },
  authCodeLabel: { color: "#6b7280", fontSize: 10, fontWeight: "800", letterSpacing: 1 },
  authCode: { color: "#e5e7eb", fontSize: 24, fontWeight: "800", letterSpacing: 2, marginTop: 4 },
  authLinkAction: { marginTop: 12, borderRadius: 9, borderWidth: 1, borderColor: "rgba(125,211,252,0.5)", paddingHorizontal: 12, paddingVertical: 9, alignItems: "center" },
  authLinkText: { color: "#7dd3fc", fontSize: 12, fontWeight: "800" },
  transportRetryAction: { minHeight: 42, marginTop: 13, borderRadius: 10, borderWidth: 1, borderColor: "rgba(251,191,36,0.45)", alignItems: "center", justifyContent: "center", paddingHorizontal: 14 },
  transportRetryText: { color: "#fbbf24", fontSize: 13, fontWeight: "800" },
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
  ctrlBtnActive: { backgroundColor: "#1f2937", borderColor: "#374151" },
  ctrlText: { color: "#9ca3af", fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", fontSize: 12, fontWeight: "600" },
  ctrlTextActive: { color: "#e5e7eb" },
  dangerBtn: {
    paddingHorizontal: 10,
    paddingVertical: 8,
    borderRadius: 8,
    backgroundColor: "rgba(239,68,68,0.12)",
    borderWidth: 1,
    borderColor: "rgba(239,68,68,0.45)",
  },
  dangerText: { color: "#fca5a5", fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace", fontSize: 12, fontWeight: "700" },
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
