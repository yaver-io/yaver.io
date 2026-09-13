import React, { useCallback, useEffect, useRef, useState } from "react";
import { describeLaneProgress } from "../src/lib/laneProgress";
import { ActivityIndicator, Alert, Pressable, ScrollView, StyleSheet, Text, TextInput, View, useWindowDimensions } from "react-native";
import { usePathname, useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import { WebView } from "react-native-webview";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { BrowserVibeBubble } from "../src/components/BrowserVibeBubble";
import { useColors } from "../src/context/ThemeContext";
import { devReloadReachedTarget, quicClient, type RemoteRuntimeCapabilities, type RemoteRuntimeSession } from "../src/lib/quic";
import { setActiveRemoteRuntimeSession, triggerFeedbackLaunch } from "../src/lib/feedbackTrigger";
import { useRouteParamsCompat } from "../src/lib/useRouteParamsCompat";
import { useDogfoodOverlay } from "../src/context/DogfoodOverlayContext";
import { initialRemoteRuntimeTransport, shouldFallbackToRelayFrames } from "../src/lib/remoteRuntimeTransport";

export default function RemoteRuntimeScreen() {
  const c = useColors();
  const router = useRouter();
  const pathname = usePathname();
  const { end: endDogfoodOverlay, goHome } = useDogfoodOverlay();
  const { width } = useWindowDimensions();
  const params = useRouteParamsCompat<{ project?: string; path?: string; framework?: string; usageMode?: string; renderBehavior?: string; sessionBehavior?: string }>();
  const project = typeof params.project === "string" ? params.project : "Project";
  const path = typeof params.path === "string" ? params.path : "";
  const framework = typeof params.framework === "string" ? params.framework : "";
  const usageMode = params.usageMode === "chat-only" || params.usageMode === "reload-only" || params.usageMode === "reload-and-chat"
    ? params.usageMode
    : undefined;
  const [caps, setCaps] = useState<RemoteRuntimeCapabilities | null>(null);
  const [session, setSession] = useState<RemoteRuntimeSession | null>(null);
  const [loading, setLoading] = useState(true);
  const [busyTargetId, setBusyTargetId] = useState<string | null>(null);
  const [sendingFeedback, setSendingFeedback] = useState(false);
  const [controlText, setControlText] = useState("");
  const [viewerNote, setViewerNote] = useState<string>("Create a session to start remote viewing.");
  const [vibingProtocolVersion, setVibingProtocolVersion] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [connectingTargetLabel, setConnectingTargetLabel] = useState<string | null>(null);
  const [connectingSince, setConnectingSince] = useState<number | null>(null);
  const [connectionLogs, setConnectionLogs] = useState<Array<{ id: string; text: string; tone: "neutral" | "success" | "error" }>>([]);
  // The elapsed counter below used to read Date.now() inline during render, so
  // it only advanced when some OTHER state change re-rendered the panel. During
  // a quiet stretch (simulator booting, ICE gathering) it froze — the one piece
  // of UI whose whole job is to prove time is passing stopped moving, which is
  // exactly what makes a slow connect feel hung. Own the clock explicitly.
  const [connectNowTick, setConnectNowTick] = useState(Date.now());
  const [connectLastOutputAt, setConnectLastOutputAt] = useState<number | null>(null);
  const [connectionPhase, setConnectionPhase] = useState<string>("Preparing connection");
  const connectionTimers = useRef<ReturnType<typeof setTimeout>[]>([]);
  const relayFallbackStarted = useRef(false);
  const isCompact = width < 430;
  const panelWidth = Math.min(width - (isCompact ? 28 : 48), 560);
  const isDesktop = framework === "desktop";
  const isReactNativeBrowserLane = framework === "expo" || framework === "react-native";

  const clearConnectionTimers = useCallback(() => {
    for (const timer of connectionTimers.current) clearTimeout(timer);
    connectionTimers.current = [];
  }, []);

  // 1s tick only while the connect panel is up.
  useEffect(() => {
    if (!connectingSince) return;
    const id = setInterval(() => setConnectNowTick(Date.now()), 1000);
    return () => clearInterval(id);
  }, [connectingSince]);

  const pushConnectionLog = useCallback((text: string, tone: "neutral" | "success" | "error" = "neutral") => {
    setConnectLastOutputAt(Date.now());
    setConnectionLogs((prev) => [
      ...prev.slice(-3),
      { id: `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, text, tone },
    ]);
  }, []);

  const finishConnectionOverlay = useCallback(() => {
    clearConnectionTimers();
    setConnectingTargetLabel(null);
    setConnectingSince(null);
  }, [clearConnectionTimers]);

  const load = useCallback(async () => {
    // framework="desktop" streams the MACHINE, not a project, so it has no
    // workDir. Every other framework still requires one — a missing path there
    // is a navigation bug we want surfaced, not defaulted away. The agent
    // enforces the same rule (remote_runtime.go handleRemoteRuntimeCapabilities).
    const isDesktop = framework === "desktop";
    if (!framework || (!path && !isDesktop)) {
      setError("Missing project path or framework.");
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      setCaps(await quicClient.getRemoteRuntimeCapabilities(path, framework));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [path, framework]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => () => clearConnectionTimers(), [clearConnectionTimers]);

  useEffect(() => {
    setActiveRemoteRuntimeSession(session);
    return () => setActiveRemoteRuntimeSession(null);
  }, [session]);

  const createSession = useCallback(async (target: { id: string; label: string }) => {
    relayFallbackStarted.current = false;
    setVibingProtocolVersion(null);
    setBusyTargetId(target.id);
    setConnectingTargetLabel(target.label);
    const connectStartedAt = Date.now();
    setConnectingSince(connectStartedAt);
    setConnectNowTick(connectStartedAt);
    setConnectLastOutputAt(null);
    setConnectionLogs([]);
    setConnectionPhase("Preparing connection");
    pushConnectionLog("Preparing connection");
    clearConnectionTimers();

    // Every line below reports something that ACTUALLY happened.
    //
    // This used to be four setTimeouts at 120/700/1400/2100 ms that ran
    // regardless of the network: "Auth OK ✓" was printed by a timer, not by an
    // auth result, and "Session ready ✓" fired 500 ms after the create call
    // returned — before the WebView loaded, before ICE, before a single frame.
    // The user read "Session ready ✓" over a black screen. That is the same
    // false green we spent this week removing from the agent, and it costs
    // more trust here than a missing feature would.
    //
    // The overlay now closes when the STREAM is up (first frame), not when the
    // HTTP call returns. finishConnectionOverlay is still called on failure so
    // it can never trap the user.
    const usingRelay = !!quicClient.activeRelayBaseUrl;
    try {
      if (target.id === "browser-window") {
        setConnectionPhase("Starting project web runtime");
        pushConnectionLog("Starting and verifying the project web runtime");
        await quicClient.prepareRemoteRuntimeBrowserLane(path, framework);
        pushConnectionLog("Project web runtime is serving", "success");
      }
      setConnectionPhase(usingRelay ? "Connecting through your home computer" : "Connecting directly");
      pushConnectionLog(usingRelay ? "Reached the computer; negotiating live media" : "Connecting directly");

      // Signaling and media are separate. A relay URL for the HTTP request must
      // not pre-demote the session to snapshot polling: ICE/TURN gets the first
      // attempt, with relay frames only as a measured failure fallback below.
      const transportMode = initialRemoteRuntimeTransport();

      const next = await quicClient.startRemoteRuntimeSession(path, framework, target.id, transportMode);
      setSession(next);
      setViewerNote(next.note || `Session ${next.id} created.`);
      // The session EXISTS. It is not yet showing anything — the viewer
      // reports first paint via onFirstFrame below.
      setConnectionPhase(usingRelay ? "Waiting for first frame" : "Negotiating WebRTC");
      pushConnectionLog("Session created on the box", "success");

      // A frame that never arrives must not hang the overlay forever. The old
      // code could not stall here because it closed on a timer regardless;
      // closing on a real event means we owe the user a bound.
      connectionTimers.current = [
        setTimeout(() => {
          setConnectionPhase("No frames yet");
          pushConnectionLog(
            target.id === "browser-window"
              ? "The app is serving, but no preview frames arrived in 20s — live media may be blocked on this network."
              : "The session started but no frames arrived in 20s — the device may still be booting, or media is blocked on this network.",
            "error",
          );
          finishConnectionOverlay();
        }, 20000),
      ];
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      setConnectionPhase("Connection failed");
      pushConnectionLog(message, "error");
      setTimeout(() => finishConnectionOverlay(), 900);
      Alert.alert("Could not create session", message);
    } finally {
      setBusyTargetId(null);
    }
  }, [path, framework, clearConnectionTimers, finishConnectionOverlay, pushConnectionLog]);

  const fallbackToRelayFrames = useCallback(async (failed: RemoteRuntimeSession, reason?: string) => {
    if (!shouldFallbackToRelayFrames({
      relayAvailable: !!quicClient.activeRelayBaseUrl,
      currentMode: failed.transportMode,
      failureReason: reason,
      alreadyAttempted: relayFallbackStarted.current,
    })) return false;
    relayFallbackStarted.current = true;
    clearConnectionTimers();
    setConnectionPhase("Switching connection mode");
    pushConnectionLog("Live media is blocked on this network; switching to compatibility frames");
    try {
      await quicClient.closeRemoteRuntimeSession(failed.id).catch(() => undefined);
      const next = await quicClient.startRemoteRuntimeSession(path, framework, failed.targetId, "relay-jpeg-poll");
      setSession(next);
      setViewerNote(next.note || "Compatibility frames started.");
      setConnectionPhase("Waiting for first frame");
      pushConnectionLog("Compatibility connection started", "success");
      connectionTimers.current = [setTimeout(() => {
        setConnectionPhase("No frames yet");
        pushConnectionLog("The computer is connected, but no app frame arrived in 20 seconds.", "error");
        finishConnectionOverlay();
      }, 20000)];
      return true;
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      pushConnectionLog(message, "error");
      finishConnectionOverlay();
      return false;
    }
  }, [clearConnectionTimers, finishConnectionOverlay, framework, path, pushConnectionLog]);

  const sendControl = useCallback(async (body: { action: "tap" | "swipe" | "text" | "back" | "home" | "key"; x?: number; y?: number; x2?: number; y2?: number; durationMs?: number; text?: string; key?: string }) => {
    if (!session) return;
    try {
      const next = await quicClient.sendRemoteRuntimeControl(session.id, body);
      setSession(next);
      setViewerNote(next.note || viewerNote);
    } catch (e) {
      Alert.alert("Control failed", e instanceof Error ? e.message : String(e));
    }
  }, [session, viewerNote]);

  const launchFeedback = useCallback(async () => {
    if (!session) return;
    setSendingFeedback(true);
    try {
      const result = await quicClient.sendRemoteRuntimeCommand(session.id, "launch-feedback", "mobile");
      triggerFeedbackLaunch("remote-runtime");
      setSession((prev) => prev ? {
        ...prev,
        status: "feedback-pending",
        lastCommand: "launch-feedback",
        note: result.note || prev.note,
      } : prev);
      Alert.alert("Feedback Requested", result.note || "Remote runtime feedback launch requested.");
    } catch (e) {
      Alert.alert("Could not launch feedback", e instanceof Error ? e.message : String(e));
    } finally {
      setSendingFeedback(false);
    }
  }, [session]);

  const closeSession = useCallback(async () => {
    if (!session) return;
    try {
      await quicClient.closeRemoteRuntimeSession(session.id);
      setSession(null);
      relayFallbackStarted.current = false;
      setVibingProtocolVersion(null);
      setViewerNote("Remote runtime session closed.");
    } catch (e) {
      Alert.alert("Could not close session", e instanceof Error ? e.message : String(e));
    }
  }, [session]);

  const exitRuntime = useCallback(() => {
    void (async () => {
      if (session) await closeSession();
      await endDogfoodOverlay();
      router.replace("/(tabs)/tasks" as any);
    })();
  }, [closeSession, endDogfoodOverlay, router, session]);

  const reloadRuntime = useCallback(async (kind: "fast" | "full") => {
    const result = await quicClient.reloadDevServerDetailed({
      mode: kind,
      allowBundleFallback: false,
      projectName: project,
      projectPath: path,
    });
    return devReloadReachedTarget(result);
  }, [path, project]);

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["top", "left", "right"]}>
      <AppScreenHeader title={isDesktop ? "Use Computer" : "App Preview"} onBack={() => router.back()} />
      <ScrollView contentContainerStyle={styles.content}>
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.title, { color: c.textPrimary }]}>{project}</Text>
          <Text style={[styles.meta, { color: c.textMuted }]}>
            {isDesktop
              ? "See and control apps already open on this computer."
              : isReactNativeBrowserLane
                ? "Browser preview first · no simulator needed"
                : "Live preview from your computer"}
          </Text>
        </View>

        {loading ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border, alignItems: "center" }]}>
            <ActivityIndicator color={c.accent} />
            <Text style={[styles.meta, { color: c.textMuted, marginTop: 10 }]}>Loading remote runtime capabilities...</Text>
          </View>
        ) : error ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.error, { color: "#fca5a5" }]}>{error}</Text>
          </View>
        ) : (
          <>
            {(caps?.targets || []).map((target) => (
              <View key={target.id} style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <Text style={[styles.section, { color: c.textPrimary }]}>{target.label}</Text>
                <Text style={[styles.meta, { color: c.textMuted }]}>
                  {target.id === "browser-window"
                    ? "Opens the app's web version on this computer."
                    : target.id === "desktop"
                      ? "Uses the screen and apps already open on this computer."
                      : target.displaySurface || target.surface || "Optional device preview"}
                </Text>
                {target.reason ? <Text style={[styles.reason, { color: "#fca5a5" }]}>{target.reason}</Text> : null}
                <Pressable
                  disabled={!target.enabled || busyTargetId === target.id}
                  onPress={() => createSession({ id: target.id, label: target.label })}
                  style={[
                    styles.button,
                    { backgroundColor: target.enabled ? c.accent : c.border, opacity: busyTargetId === target.id ? 0.7 : 1 },
                  ]}
                >
                  <Text style={styles.buttonText}>
                    {busyTargetId === target.id
                      ? "Opening..."
                      : target.enabled
                        ? (target.id === "desktop" ? "Connect" : "Open App")
                        : "Unavailable"}
                  </Text>
                </Pressable>
              </View>
            ))}

            {session ? (
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <Text style={[styles.section, { color: c.textPrimary }]}>Live Preview</Text>
                <Text style={[styles.meta, { color: c.textMuted }]}>
                  {session.status === "streaming" ? "Connected" : session.status}
                </Text>
                {session.note ? <Text style={[styles.meta, { color: c.textMuted, marginTop: 8 }]}>{session.note}</Text> : null}
                <View style={[styles.viewerShell, { borderColor: c.border }]}>
                  <WebView
                    source={{ html: buildRemoteRuntimeViewerHtml(quicClient.baseUrl, quicClient.getAuthHeaders(), session) }}
                    originWhitelist={["*"]}
                    javaScriptEnabled
                    scrollEnabled={false}
                    onMessage={(event) => {
                      try {
                        const payload = JSON.parse(event.nativeEvent.data);
                        if (payload?.type === "session" && payload.session) {
                          setSession(payload.session as RemoteRuntimeSession);
                        }
                        if (typeof payload?.note === "string") setViewerNote(payload.note);
                        if (typeof payload?.error === "string") setViewerNote(payload.error);
                        // First real pixels — THIS is when the connection is
                        // done, not when the create call returned.
                        if (payload?.type === "first-frame") {
                          pushConnectionLog(
                            payload.how === "relay-jpeg" ? "First frame (relay stills)" : "First frame decoded",
                            "success",
                          );
                          setConnectionPhase("Streaming");
                          finishConnectionOverlay();
                        }
                        if (payload?.type === "stream-failed") {
                          if (payload.reason === "ice-failed" && session) {
                            void fallbackToRelayFrames(session, payload.reason).then((started) => {
                              if (started) return;
                              setConnectionPhase("Connection failed");
                              pushConnectionLog(payload.message || "Live media is blocked and no compatibility relay is available.", "error");
                              finishConnectionOverlay();
                            });
                            return;
                          }
                          setConnectionPhase("Connection failed");
                          pushConnectionLog(
                            payload.message || (payload.reason === "ice-failed"
                              ? "Direct WebRTC blocked on this network — a relay is required"
                              : String(payload.reason || "stream failed")),
                            "error",
                          );
                          finishConnectionOverlay();
                        }
                        if (payload?.type === "stream-stalled") {
                          setViewerNote("Stream stalled — the box stopped sending frames.");
                        }
                        if (payload?.type === "vibing-protocol" && payload.protocol?.v === 1) {
                          setVibingProtocolVersion(1);
                        }
                        if (payload?.type === "vibing-ack" && payload.ack?.ok === false) {
                          const message = payload.ack?.error?.message;
                          if (typeof message === "string") setViewerNote(message);
                        }
                        if (payload?.type === "feedback-launch-request") triggerFeedbackLaunch("remote-runtime");
                      } catch {
                        setViewerNote(event.nativeEvent.data);
                      }
                    }}
                    style={styles.viewer}
                  />
                </View>
                <Text style={[styles.meta, { color: c.textMuted, marginTop: 10 }]}>
                  {viewerNote}{vibingProtocolVersion ? ` · DOM control v${vibingProtocolVersion}` : ""}
                </Text>
                <View style={styles.row}>
                  <TextInput
                    value={controlText}
                    onChangeText={setControlText}
                    placeholder="Send text to focused field"
                    placeholderTextColor={c.textMuted}
                    style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
                  />
                  <Pressable
                    onPress={() => {
                      const text = controlText.trim();
                      if (!text) return;
                      void sendControl({ action: "text", text });
                      setControlText("");
                    }}
                    style={[styles.inlineButton, { backgroundColor: c.accent }]}
                  >
                    <Text style={styles.buttonText}>Type</Text>
                  </Pressable>
                </View>
                {session.platform === "android" ? (
                  <View style={styles.row}>
                    <Pressable onPress={() => void sendControl({ action: "back" })} style={[styles.inlineButton, { backgroundColor: c.border }]}>
                      <Text style={styles.buttonText}>Back</Text>
                    </Pressable>
                    <Pressable onPress={() => void sendControl({ action: "home" })} style={[styles.inlineButton, { backgroundColor: c.border }]}>
                      <Text style={styles.buttonText}>Home</Text>
                    </Pressable>
                  </View>
                ) : null}
                <Pressable
                  disabled={sendingFeedback}
                  onPress={() => void launchFeedback()}
                  style={[styles.button, { backgroundColor: c.accent, opacity: sendingFeedback ? 0.7 : 1 }]}
                >
                  <Text style={styles.buttonText}>{sendingFeedback ? "Requesting..." : "Trigger Feedback"}</Text>
                </Pressable>
                <Pressable onPress={() => void closeSession()} style={[styles.button, { backgroundColor: "#7f1d1d" }]}>
                  <Text style={styles.buttonText}>Close Session</Text>
                </Pressable>
              </View>
            ) : null}
          </>
        )}
      </ScrollView>
      {connectingTargetLabel ? (
        <View style={styles.connectOverlay}>
          <View style={styles.connectScrim} />
          <View style={styles.connectPanelWrap}>
            <View style={[styles.connectPanel, { width: panelWidth, paddingHorizontal: isCompact ? 20 : 26, paddingVertical: isCompact ? 22 : 28 }]}>
              <View style={[styles.connectIconWrap, { width: isCompact ? 62 : 72, height: isCompact ? 62 : 72, borderRadius: isCompact ? 31 : 36, marginBottom: isCompact ? 14 : 18 }]}>
                <Text style={[styles.connectIcon, { fontSize: isCompact ? 28 : 32 }]}>◌</Text>
              </View>
              <Text style={[styles.connectTitle, { fontSize: isCompact ? 22 : 28, marginBottom: isCompact ? 6 : 8 }]}>Connecting to Yaver</Text>
              <Text style={[styles.connectTarget, { fontSize: isCompact ? 18 : 22, marginBottom: isCompact ? 10 : 12 }]} numberOfLines={2}>{connectingTargetLabel}</Text>
              <Text style={[styles.connectPhase, { fontSize: isCompact ? 17 : 20, lineHeight: isCompact ? 22 : 26 }]}>{connectionPhase}</Text>
              <Text style={[styles.connectElapsed, { fontSize: isCompact ? 13 : 15, marginTop: isCompact ? 6 : 8, marginBottom: isCompact ? 14 : 18 }]}>
                {describeLaneProgress({ startedAt: connectingSince, lastOutputAt: connectLastOutputAt, now: connectNowTick })?.text ?? ""}
              </Text>
              <View style={[styles.connectLogStack, { minHeight: isCompact ? 104 : 122 }]}>
                {connectionLogs.map((entry, index) => (
                  <View key={entry.id} style={styles.connectLogRow}>
                    <Text
                      style={[
                        styles.connectLogIcon,
                        { width: isCompact ? 18 : 22, fontSize: isCompact ? 16 : 18 },
                        entry.tone === "success"
                          ? styles.connectLogIconSuccess
                          : entry.tone === "error"
                            ? styles.connectLogIconError
                            : styles.connectLogIconNeutral,
                      ]}
                    >
                      {entry.tone === "success" ? "✓" : entry.tone === "error" ? "!" : "•"}
                    </Text>
                    <Text
                      style={[
                        styles.connectLogText,
                        { fontSize: isCompact ? 15 : 18, lineHeight: isCompact ? 20 : 24 },
                        entry.tone === "success"
                          ? styles.connectLogTextSuccess
                          : entry.tone === "error"
                            ? styles.connectLogTextError
                            : styles.connectLogTextNeutral,
                        index === connectionLogs.length - 1 ? styles.connectLogTextCurrent : null,
                      ]}
                    >
                      {entry.text}
                    </Text>
                  </View>
                ))}
              </View>
              <ActivityIndicator color="#22c55e" size={isCompact ? "small" : "large"} style={[styles.connectSpinner, { marginTop: isCompact ? 14 : 18 }]} />
            </View>
          </View>
        </View>
      ) : null}
      {pathname === "/remote-runtime" ? <BrowserVibeBubble
        projectPath={path}
        projectName={project}
        usageMode={usageMode}
        renderBehavior={params.renderBehavior === "auto-on-request" ? "auto-on-request" : "manual"}
        sessionBehavior={params.sessionBehavior === "new-session" ? "new-session" : "resume-last"}
        exitLabel="Open Dogfood"
        onGoHome={goHome}
        onExitPreview={exitRuntime}
        onReload={reloadRuntime}
      /> : null}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  content: { padding: 16, gap: 12 },
  card: { borderWidth: 1, borderRadius: 18, padding: 16 },
  viewerShell: { marginTop: 14, borderWidth: 1, borderRadius: 16, overflow: "hidden", height: 460 },
  viewer: { flex: 1, backgroundColor: "#000" },
  title: { fontSize: 20, fontWeight: "700" },
  section: { fontSize: 15, fontWeight: "700" },
  meta: { fontSize: 13, lineHeight: 18 },
  path: { marginTop: 8, fontSize: 12 },
  reason: { marginTop: 10, fontSize: 13, lineHeight: 18 },
  error: { fontSize: 14, lineHeight: 20 },
  row: { flexDirection: "row", alignItems: "center", gap: 10, marginTop: 12 },
  input: { flex: 1, borderWidth: 1, borderRadius: 12, paddingHorizontal: 12, paddingVertical: 10, fontSize: 13 },
  inlineButton: { borderRadius: 12, paddingHorizontal: 14, paddingVertical: 10, alignItems: "center", justifyContent: "center" },
  button: { marginTop: 14, borderRadius: 12, paddingVertical: 12, alignItems: "center" },
  buttonText: { color: "#fff", fontSize: 13, fontWeight: "700" },
  connectOverlay: {
    ...StyleSheet.absoluteFillObject,
    justifyContent: "center",
    alignItems: "center",
  },
  connectScrim: {
    ...StyleSheet.absoluteFillObject,
    backgroundColor: "rgba(3, 7, 18, 0.84)",
  },
  connectPanelWrap: {
    width: "100%",
    paddingHorizontal: 24,
  },
  connectPanel: {
    borderRadius: 28,
    backgroundColor: "#0a0f18",
    borderWidth: 1,
    borderColor: "rgba(34,197,94,0.28)",
    paddingHorizontal: 26,
    paddingVertical: 28,
    shadowColor: "#000",
    shadowOpacity: 0.4,
    shadowRadius: 18,
    shadowOffset: { width: 0, height: 12 },
    elevation: 18,
  },
  connectIconWrap: {
    width: 72,
    height: 72,
    borderRadius: 36,
    alignSelf: "center",
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "rgba(34,197,94,0.12)",
    borderWidth: 1,
    borderColor: "rgba(34,197,94,0.24)",
    marginBottom: 18,
  },
  connectIcon: {
    color: "#22c55e",
    fontSize: 32,
    fontWeight: "700",
  },
  connectTitle: {
    color: "#ffffff",
    fontSize: 28,
    fontWeight: "800",
    textAlign: "center",
    marginBottom: 8,
  },
  connectTarget: {
    color: "#22c55e",
    fontSize: 22,
    fontWeight: "700",
    textAlign: "center",
    marginBottom: 12,
  },
  connectPhase: {
    color: "#ffffff",
    fontSize: 20,
    lineHeight: 26,
    fontWeight: "600",
    textAlign: "center",
  },
  connectElapsed: {
    color: "#94a3b8",
    fontSize: 15,
    textAlign: "center",
    marginTop: 8,
    marginBottom: 18,
  },
  connectLogStack: {
    gap: 10,
    minHeight: 122,
    justifyContent: "center",
  },
  connectLogRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
  connectLogIcon: {
    width: 22,
    textAlign: "center",
    fontSize: 18,
    fontWeight: "800",
  },
  connectLogIconNeutral: { color: "#cbd5e1" },
  connectLogIconSuccess: { color: "#22c55e" },
  connectLogIconError: { color: "#ef4444" },
  connectLogText: {
    flex: 1,
    fontSize: 18,
    lineHeight: 24,
    fontWeight: "500",
  },
  connectLogTextNeutral: { color: "#ffffff" },
  connectLogTextSuccess: { color: "#22c55e" },
  connectLogTextError: { color: "#ef4444" },
  connectLogTextCurrent: { fontWeight: "700" },
  connectSpinner: {
    marginTop: 18,
  },
});

function buildRemoteRuntimeViewerHtml(baseUrl: string, headers: Record<string, string>, session: RemoteRuntimeSession) {
  const payload = JSON.stringify({
    baseUrl,
    headers,
    sessionId: session.id,
    deviceDims: session.deviceDims || null,
    transportMode: session.transportMode || "direct-webrtc",
  });
  return `<!doctype html>
<html>
  <head>
    <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no" />
    <style>
      html, body { margin: 0; padding: 0; background: #000; color: #fff; font-family: -apple-system, BlinkMacSystemFont, sans-serif; height: 100%; overflow: hidden; }
      #root { position: relative; width: 100vw; height: 100vh; display: flex; align-items: center; justify-content: center; touch-action: none; }
      #frame, #video { width: 100%; height: 100%; object-fit: contain; }
      /* Exactly one surface is live at a time — the RTP <video> when the
         agent attaches an H.264 track, the JPEG <img> otherwise. The idle
         one is hidden so a stale frame can't bleed through. */
      .hidden { display: none; }
      #status { position: absolute; top: 10px; left: 10px; right: 10px; font-size: 12px; color: #d1d5db; background: rgba(0,0,0,0.55); padding: 8px 10px; border-radius: 10px; }
    </style>
  </head>
  <body>
    <div id="root">
      <video id="video" class="hidden" autoplay playsinline muted></video>
      <img id="frame" alt="remote frame" />
      <div id="status">Negotiating WebRTC…</div>
    </div>
    <script>
      const cfg = ${payload};
      const statusEl = document.getElementById("status");
      const frameEl = document.getElementById("frame");
      const videoEl = document.getElementById("video");
      const rootEl = document.getElementById("root");
      let objectUrl = null;
      let imagePending = false;
      let blankSince = 0;
      let blankFailureReported = false;

      // The live render surface. RTP paints into <video> (intrinsic size is
      // videoWidth/videoHeight); JPEG paints into <img> (naturalWidth/Height).
      // Everything downstream — hit-testing, swipe mapping — goes through here
      // so it never has to know which transport won.
      function activeSurface() {
        if (videoEl.srcObject && videoEl.videoWidth > 0) {
          return { el: videoEl, w: videoEl.videoWidth, h: videoEl.videoHeight };
        }
        if (frameEl.naturalWidth > 0) {
          return { el: frameEl, w: frameEl.naturalWidth, h: frameEl.naturalHeight };
        }
        return null;
      }
      function showVideoSurface() {
        frameEl.classList.add("hidden");
        videoEl.classList.remove("hidden");
      }
      function post(payload) {
        if (window.ReactNativeWebView && window.ReactNativeWebView.postMessage) {
          window.ReactNativeWebView.postMessage(JSON.stringify(payload));
        }
      }
      // firstFrame fires ONCE, on the first frame the user can actually see.
      // ontrack only means a track object arrived - it can arrive and never
      // decode, which is how "H.264 stream active." ended up over a black
      // screen. The JPEG path never reported anything at all, so it sat on
      // "Negotiating WebRTC…" while frames were painting.
      var reportedFirstFrame = false;
      function reportFirstFrame(how) {
        if (reportedFirstFrame) return;
        reportedFirstFrame = true;
        post({ type: "first-frame", how: how });
      }
      function setStatus(note) {
        statusEl.textContent = note;
        post({ type: "status", note });
      }
      function displayFrameBlob(blob, how) {
        if (imagePending) return;
        imagePending = true;
        const previousUrl = objectUrl;
        const nextUrl = URL.createObjectURL(blob);
        frameEl.onload = function () {
          imagePending = false;
          objectUrl = nextUrl;
          if (previousUrl) URL.revokeObjectURL(previousUrl);
          try {
            const sample = document.createElement("canvas");
            sample.width = 12;
            sample.height = 12;
            const ctx = sample.getContext("2d", { willReadFrequently: true });
            ctx.drawImage(frameEl, 0, 0, 12, 12);
            const pixels = ctx.getImageData(0, 0, 12, 12).data;
            let min = 255, max = 0, sum = 0, count = 0;
            for (let i = 0; i < pixels.length; i += 4) {
              const y = Math.round(pixels[i] * 0.299 + pixels[i + 1] * 0.587 + pixels[i + 2] * 0.114);
              min = Math.min(min, y); max = Math.max(max, y); sum += y; count++;
            }
            const avg = count ? sum / count : 0;
            const blank = max - min <= 6 && (avg <= 12 || avg >= 243);
            if (blank) {
              if (!blankSince) blankSince = Date.now();
              setStatus("Connected, waiting for usable app pixels…");
              if (!blankFailureReported && Date.now() - blankSince >= 8000) {
                blankFailureReported = true;
                post({ type: "stream-failed", reason: "blank-frames", message: "The box is sending blank frames. The project web runtime did not render usable pixels." });
              }
              return;
            }
            blankSince = 0;
          } catch (_) {
            // Blob-backed images in WKWebView are same-origin. If a platform
            // still refuses canvas reads, keep rendering instead of creating
            // a false failure from the diagnostic itself.
          }
          if (how === "relay-jpeg") setStatus("Relay frame polling active (still frames, ~1 fps).");
          reportFirstFrame(how);
        };
        frameEl.onerror = function () {
          imagePending = false;
          URL.revokeObjectURL(nextUrl);
          setStatus("JPEG frame arrived but this surface could not decode it.");
        };
        frameEl.src = nextUrl;
      }
      async function sendControl(body) {
        const res = await fetch(cfg.baseUrl + "/remote-runtime/sessions/" + encodeURIComponent(cfg.sessionId) + "/control", {
          method: "POST",
          headers: { ...cfg.headers, "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        const data = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(data.error || "Control failed");
        if (data.session) post({ type: "session", session: data.session, note: data.session.note || "" });
      }
      function contentPointFromClient(clientX, clientY) {
        const surface = activeSurface();
        if (!surface) return null;
        const rect = surface.el.getBoundingClientRect();
        const scale = Math.min(rect.width / surface.w, rect.height / surface.h);
        const drawW = surface.w * scale;
        const drawH = surface.h * scale;
        const left = rect.left + (rect.width - drawW) / 2;
        const top = rect.top + (rect.height - drawH) / 2;
        const nx = (clientX - left) / Math.max(1, drawW);
        const ny = (clientY - top) / Math.max(1, drawH);
        if (nx < 0 || ny < 0 || nx > 1 || ny > 1) return null;
        const targetW = cfg.deviceDims && cfg.deviceDims.width ? cfg.deviceDims.width : surface.w;
        const targetH = cfg.deviceDims && cfg.deviceDims.height ? cfg.deviceDims.height : surface.h;
        return {
          x: Math.round(nx * targetW),
          y: Math.round(ny * targetH),
        };
      }
      function contentPoint(event) {
        return contentPointFromClient(event.clientX, event.clientY);
      }
      function waitForIce(pc) {
        return new Promise((resolve) => {
          if (pc.iceGatheringState === "complete") return resolve();
          const check = () => {
            if (pc.iceGatheringState === "complete") {
              pc.removeEventListener("icegatheringstatechange", check);
              resolve();
            }
          };
          pc.addEventListener("icegatheringstatechange", check);
          setTimeout(resolve, 2000);
        });
      }
      async function start() {
        if (cfg.transportMode === "relay-jpeg-poll") {
          setStatus("Starting relay frame polling…");
          const pump = async () => {
            try {
              const res = await fetch(cfg.baseUrl + "/remote-runtime/sessions/" + encodeURIComponent(cfg.sessionId) + "/frame?ts=" + Date.now(), {
                headers: cfg.headers,
                cache: "no-store",
              });
              const data = !res.ok ? await res.json().catch(() => ({})) : null;
              if (!res.ok) throw new Error(data && data.error ? data.error : "Frame fetch failed");
              const blob = await res.blob();
              displayFrameBlob(blob, "relay-jpeg");
            } catch (error) {
              setStatus(error.message || String(error));
            } finally {
              window.setTimeout(pump, 900);
            }
          };
          void pump();
          return;
        }
        // ICE servers from the agent: STUN always, plus the relay's colocated
        // TURN when the operator set YAVER_TURN_URL + --turn-port. Without
        // this the PC has no STUN at all and only connects by peer-reflexive
        // discovery — which is why direct-webrtc was flaky off the local link.
        let iceServers = [{ urls: "stun:stun.l.google.com:19302" }];
        try {
          const iceRes = await fetch(cfg.baseUrl + "/stream/webrtc/ice", { headers: cfg.headers, cache: "no-store" });
          const iceData = await iceRes.json().catch(() => ({}));
          if (iceRes.ok && iceData.iceServers && iceData.iceServers.length > 0) iceServers = iceData.iceServers;
        } catch {}
        const pc = new RTCPeerConnection({ iceServers });
        let eventsChannel = null;
        // Native DOM-selection surfaces can call this through injected JS once
        // the versioned hello arrives. It is deliberately bounded to the
        // server's 16 KiB protocol limit and returns false when HTTP fallback
        // should be used instead.
        window.yaverVibingControl = function (message) {
          if (!eventsChannel || eventsChannel.readyState !== "open" || !message || typeof message !== "object") return false;
          try {
            const wire = JSON.stringify(message);
            if (!wire || wire.length > 16384) return false;
            eventsChannel.send(wire);
            return true;
          } catch { return false; }
        };
        const jpegChunks = new Map();
        function jpegBlobFromMessage(data) {
          if (typeof data !== "string") return new Blob([data], { type: "image/jpeg" });
          let payload = null;
          try { payload = JSON.parse(data); } catch {
            const bytes = new Uint8Array(data.length);
            for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 255;
            return new Blob([bytes], { type: "image/jpeg" });
          }
          if (!payload || payload.type !== "jpeg-chunk" || !payload.id ||
              typeof payload.index !== "number" || typeof payload.total !== "number" ||
              typeof payload.data !== "string") return null;
          const entry = jpegChunks.get(payload.id) || { total: payload.total, parts: [] };
          entry.total = payload.total;
          entry.parts[payload.index] = payload.data;
          jpegChunks.set(payload.id, entry);
          if (entry.parts.filter(Boolean).length < entry.total) return null;
          jpegChunks.delete(payload.id);
          const raw = atob(entry.parts.join(""));
          const bytes = new Uint8Array(raw.length);
          for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
          return new Blob([bytes], { type: "image/jpeg" });
        }
        // SCTP m-line: the agent creates the "frames"/"events" channels from
        // its side, so a video-only offer would leave them un-negotiable.
        pc.createDataChannel("primer");
        // Offer a video transceiver. The agent's selectRemoteRuntimeStreamer()
        // greps the offer SDP for m=video and only then attaches the Pion
        // H.264 track; without this it silently picks JPEG-over-DataChannel
        // every time and the RTP path is unreachable. If the agent can't
        // encode for this target it still falls back to JPEG-DC below.
        pc.addTransceiver("video", { direction: "recvonly" });
        pc.ontrack = (event) => {
          const stream = event.streams[0];
          if (!stream) return;
          videoEl.srcObject = stream;
          videoEl.muted = true;
          showVideoSurface();
          // Autoplay: muted inline playback is allowed without a gesture.
          videoEl.play().catch(() => {});
          setStatus("H.264 stream active.");
          // Wait for real pixels: loadeddata is the first decoded frame.
          const verifyVideoPixels = function () {
            // A decoded frame is necessary but not sufficient: H.264 can
            // faithfully decode a black/white about:blank stream. Sample the
            // same pixels as the JPEG path before declaring first paint.
            try {
              const sample = document.createElement("canvas");
              sample.width = 12; sample.height = 12;
              const ctx = sample.getContext("2d", { willReadFrequently: true });
              ctx.drawImage(videoEl, 0, 0, 12, 12);
              const pixels = ctx.getImageData(0, 0, 12, 12).data;
              let min = 255, max = 0, sum = 0;
              for (let i = 0; i < pixels.length; i += 4) {
                const y = Math.round(pixels[i] * 0.299 + pixels[i + 1] * 0.587 + pixels[i + 2] * 0.114);
                min = Math.min(min, y); max = Math.max(max, y); sum += y;
              }
              const avg = sum / (pixels.length / 4);
              if (max - min <= 6 && (avg <= 12 || avg >= 243)) {
                if (!blankSince) blankSince = Date.now();
                setStatus("Connected, waiting for usable app pixels…");
                if (!blankFailureReported && Date.now() - blankSince >= 8000) {
                  blankFailureReported = true;
                  post({ type: "stream-failed", reason: "blank-frames", message: "The box is sending blank H.264 frames. The project runtime did not render usable pixels." });
                }
                return;
              }
            } catch (_) {}
            blankSince = 0;
            videoEl.removeEventListener("loadeddata", verifyVideoPixels);
            videoEl.removeEventListener("timeupdate", verifyVideoPixels);
            reportFirstFrame("video");
          };
          videoEl.addEventListener("loadeddata", verifyVideoPixels);
          videoEl.addEventListener("timeupdate", verifyVideoPixels);
        };
        pc.onconnectionstatechange = function () {
          // "Peer state: failed" is an implementation detail leaking into the
          // UI. Say what it means for the user and what to do.
          var st = pc.connectionState;
          if (st === "connected") { setStatus("Connected — waiting for frames…"); }
          else if (st === "failed") {
            setStatus("Could not reach the box's media ports. Direct WebRTC was blocked — a relay is needed on this network.");
            post({ type: "stream-failed", reason: "ice-failed" });
          }
          else if (st === "disconnected") { setStatus("Stream dropped — the box stopped sending."); post({ type: "stream-stalled" }); }
          else { setStatus("Connecting…"); }
        };
        pc.ondatachannel = (event) => {
          if (event.channel.label === "frames") {
            event.channel.binaryType = "arraybuffer";
            event.channel.onmessage = (msg) => {
              const blob = jpegBlobFromMessage(msg.data);
              if (!blob) return;
              displayFrameBlob(blob, "jpeg-datachannel");
            };
          }
          if (event.channel.label === "events") {
            eventsChannel = event.channel;
            event.channel.onmessage = (msg) => {
              try {
                const payload = JSON.parse(String(msg.data));
                if (payload.session) post({ type: "session", session: payload.session });
                if (payload.type === "dims" && payload.width && payload.height) {
                  cfg.deviceDims = { width: payload.width, height: payload.height, scale: payload.scale, rotation: payload.rotation };
                }
                if (payload.type === "feedback-launch-request") {
                  post({ type: "feedback-launch-request", source: payload.source || "remote-runtime" });
                  setStatus("Feedback overlay requested.");
                }
                if (payload.type === "vibing.protocol") {
                  post({ type: "vibing-protocol", protocol: payload });
                }
                if (payload.type === "vibing.ack") {
                  post({ type: "vibing-ack", ack: payload });
                }
                if (payload.type === "frame-error" && payload.error) setStatus("Frame capture failed: " + String(payload.error));
                else if (payload.error) setStatus(payload.error);
              } catch {}
            };
          }
        };
        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);
        // Signaling is non-trickle (agent answers once over HTTP), so the
        // offer must carry its candidates. Bounded — a host-only offer still
        // connects on-LAN, and waiting forever beats nothing.
        await waitForIce(pc);
        const local = pc.localDescription || offer;
        const res = await fetch(cfg.baseUrl + "/remote-runtime/sessions/" + encodeURIComponent(cfg.sessionId) + "/webrtc/offer", {
          method: "POST",
          headers: { ...cfg.headers, "Content-Type": "application/json" },
          body: JSON.stringify({ type: local.type, sdp: local.sdp }),
        });
        const data = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(data.error || "WebRTC offer failed");
        if (data.session) post({ type: "session", session: data.session });
        if (data.note) setStatus(data.note);
        await pc.setRemoteDescription({ type: data.answer.type || "answer", sdp: data.answer.sdp || "" });
      }
      let suppressClickUntil = 0;
      rootEl.addEventListener("click", async (event) => {
        if (Date.now() < suppressClickUntil) return;
        const point = contentPoint(event);
        if (!point) return;
        try {
          await sendControl({ action: "tap", x: point.x, y: point.y });
        } catch (error) {
          setStatus(error.message || String(error));
        }
      });
      let startPoint = null;
      let startClient = null;
      let moved = false;
      rootEl.addEventListener("touchstart", (event) => {
        if (event.touches.length !== 1) return;
        const t = event.touches[0];
        startClient = { x: t.clientX, y: t.clientY };
        startPoint = contentPointFromClient(t.clientX, t.clientY);
        moved = false;
        event.preventDefault();
      }, { passive: false });
      rootEl.addEventListener("touchmove", (event) => {
        if (!startClient || event.touches.length !== 1) return;
        const t = event.touches[0];
        if (Math.abs(t.clientX - startClient.x) > 12 || Math.abs(t.clientY - startClient.y) > 12) moved = true;
        event.preventDefault();
      }, { passive: false });
      rootEl.addEventListener("touchend", async (event) => {
        if (!startPoint) return;
        const t = event.changedTouches[0];
        const endPoint = contentPointFromClient(t.clientX, t.clientY);
        const beginPoint = startPoint;
        const shouldSwipe = moved && endPoint;
        startPoint = null;
        startClient = null;
        moved = false;
        if (!shouldSwipe) return;
        suppressClickUntil = Date.now() + 600;
        try {
          await sendControl({ action: "swipe", x: beginPoint.x, y: beginPoint.y, x2: endPoint.x, y2: endPoint.y, durationMs: 250 });
        } catch (error) {
          setStatus(error.message || String(error));
        }
        event.preventDefault();
      }, { passive: false });
      start().catch((error) => setStatus(error.message || String(error)));
    </script>
  </body>
</html>`;
}
