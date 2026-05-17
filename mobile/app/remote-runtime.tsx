import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, StyleSheet, Text, TextInput, View, useWindowDimensions } from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import { WebView } from "react-native-webview";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { quicClient, type RemoteRuntimeCapabilities, type RemoteRuntimeSession } from "../src/lib/quic";
import { setActiveRemoteRuntimeSession } from "../src/lib/feedbackTrigger";

export default function RemoteRuntimeScreen() {
  const c = useColors();
  const router = useRouter();
  const { width } = useWindowDimensions();
  const params = useLocalSearchParams<{ project?: string; path?: string; framework?: string }>();
  const project = typeof params.project === "string" ? params.project : "Project";
  const path = typeof params.path === "string" ? params.path : "";
  const framework = typeof params.framework === "string" ? params.framework : "";
  const [caps, setCaps] = useState<RemoteRuntimeCapabilities | null>(null);
  const [session, setSession] = useState<RemoteRuntimeSession | null>(null);
  const [loading, setLoading] = useState(true);
  const [busyTargetId, setBusyTargetId] = useState<string | null>(null);
  const [sendingFeedback, setSendingFeedback] = useState(false);
  const [controlText, setControlText] = useState("");
  const [viewerNote, setViewerNote] = useState<string>("Create a session to start remote viewing.");
  const [error, setError] = useState<string | null>(null);
  const [connectingTargetLabel, setConnectingTargetLabel] = useState<string | null>(null);
  const [connectingSince, setConnectingSince] = useState<number | null>(null);
  const [connectionLogs, setConnectionLogs] = useState<Array<{ id: string; text: string; tone: "neutral" | "success" | "error" }>>([]);
  const [connectionPhase, setConnectionPhase] = useState<string>("Preparing connection");
  const connectionTimers = useRef<ReturnType<typeof setTimeout>[]>([]);
  const isCompact = width < 430;
  const panelWidth = Math.min(width - (isCompact ? 28 : 48), 560);

  const clearConnectionTimers = useCallback(() => {
    for (const timer of connectionTimers.current) clearTimeout(timer);
    connectionTimers.current = [];
  }, []);

  const pushConnectionLog = useCallback((text: string, tone: "neutral" | "success" | "error" = "neutral") => {
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
    if (!path || !framework) {
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
    setActiveRemoteRuntimeSession(session?.id || null);
    return () => setActiveRemoteRuntimeSession(null);
  }, [session?.id]);

  const createSession = useCallback(async (target: { id: string; label: string }) => {
    setBusyTargetId(target.id);
    setConnectingTargetLabel(target.label);
    setConnectingSince(Date.now());
    setConnectionLogs([]);
    setConnectionPhase("Preparing connection");
    pushConnectionLog("Preparing connection");
    clearConnectionTimers();
    connectionTimers.current = [
      setTimeout(() => {
        setConnectionPhase("Resolving machine");
        pushConnectionLog("Resolving machine");
      }, 120),
      setTimeout(() => {
        setConnectionPhase(quicClient.activeRelayBaseUrl ? "Trying relay" : "Trying direct");
        pushConnectionLog(quicClient.activeRelayBaseUrl ? "Trying relay" : "Trying direct");
      }, 700),
      setTimeout(() => {
        setConnectionPhase("Authenticating");
        pushConnectionLog("Auth OK", "success");
      }, 1400),
      setTimeout(() => {
        setConnectionPhase("Starting remote view");
        pushConnectionLog("Starting remote view");
      }, 2100),
    ];
    try {
      const transportMode = quicClient.activeRelayBaseUrl ? "relay-jpeg-poll" : "direct-webrtc";
      const next = await quicClient.startRemoteRuntimeSession(path, framework, target.id, transportMode);
      setSession(next);
      setViewerNote(next.note || `Session ${next.id} created.`);
      setConnectionPhase("Session ready");
      pushConnectionLog(transportMode === "relay-jpeg-poll" ? "Relay ready" : "Direct ready", "success");
      pushConnectionLog("Session ready", "success");
      setTimeout(() => finishConnectionOverlay(), 500);
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

  const sendControl = useCallback(async (body: { action: "tap" | "text" | "back" | "home"; text?: string }) => {
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
      setViewerNote("Remote runtime session closed.");
    } catch (e) {
      Alert.alert("Could not close session", e instanceof Error ? e.message : String(e));
    }
  }, [session]);

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["top", "left", "right"]}>
      <AppScreenHeader title="Remote Runtime" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={styles.content}>
        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.title, { color: c.textPrimary }]}>{project}</Text>
          <Text style={[styles.meta, { color: c.textMuted }]}>{framework || "unknown"} · native WebRTC lane</Text>
          {path ? <Text style={[styles.path, { color: c.textMuted }]}>{path}</Text> : null}
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
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.section, { color: c.textPrimary }]}>Execution Mode</Text>
              <Text style={[styles.meta, { color: c.textMuted }]}>
                Primary surface: {caps?.primarySurface || "none"} · mode {caps?.executionMode || "unsupported"}
              </Text>
              {caps?.currentHostClass ? (
                <Text style={[styles.meta, { color: c.textMuted, marginTop: 6 }]}>
                  Current host class: {caps.currentHostClass}
                </Text>
              ) : null}
              {caps?.supportedTransports?.length ? (
                <Text style={[styles.meta, { color: c.textMuted, marginTop: 6 }]}>
                  Transports: {caps.supportedTransports.join(", ")}
                </Text>
              ) : null}
              {caps?.feedbackSdkCompatible ? (
                <Text style={[styles.meta, { color: c.textMuted, marginTop: 8 }]}>
                  Feedback SDK: {caps.feedbackSdkNote || "compatible"}
                  {caps.feedbackControlProtocol ? ` · protocol ${caps.feedbackControlProtocol}` : ""}
                </Text>
              ) : null}
            </View>

            {(caps?.targets || []).map((target) => (
              <View key={target.id} style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <Text style={[styles.section, { color: c.textPrimary }]}>{target.label}</Text>
                <Text style={[styles.meta, { color: c.textMuted }]}>
                  {target.requiredCli || "runtime tools"} · host {target.hostOs || "unknown"} · runtime class {target.runtimeHostClass || "generic"}
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
                  <Text style={styles.buttonText}>{busyTargetId === target.id ? "Creating..." : target.enabled ? "Create Session" : "Unavailable"}</Text>
                </Pressable>
              </View>
            ))}

            {session ? (
              <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                <Text style={[styles.section, { color: c.textPrimary }]}>Latest Session</Text>
                <Text style={[styles.meta, { color: c.textMuted }]}>
                  {session.id} · {session.status}{session.lastCommand ? ` · ${session.lastCommand}` : ""}{session.transportMode ? ` · ${session.transportMode}` : ""}
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
                      } catch {
                        setViewerNote(event.nativeEvent.data);
                      }
                    }}
                    style={styles.viewer}
                  />
                </View>
                <Text style={[styles.meta, { color: c.textMuted, marginTop: 10 }]}>{viewerNote}</Text>
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
                {session.targetId === "android-emulator" ? (
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
                {connectingSince ? `${Math.max(0, Math.round((Date.now() - connectingSince) / 1000))}s elapsed` : ""}
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
    transportMode: session.transportMode || "direct-webrtc",
  });
  return `<!doctype html>
<html>
  <head>
    <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no" />
    <style>
      html, body { margin: 0; padding: 0; background: #000; color: #fff; font-family: -apple-system, BlinkMacSystemFont, sans-serif; height: 100%; overflow: hidden; }
      #root { position: relative; width: 100vw; height: 100vh; display: flex; align-items: center; justify-content: center; }
      #frame { width: 100%; height: 100%; object-fit: contain; }
      #status { position: absolute; top: 10px; left: 10px; right: 10px; font-size: 12px; color: #d1d5db; background: rgba(0,0,0,0.55); padding: 8px 10px; border-radius: 10px; }
    </style>
  </head>
  <body>
    <div id="root">
      <img id="frame" alt="remote frame" />
      <div id="status">Negotiating WebRTC…</div>
    </div>
    <script>
      const cfg = ${payload};
      const statusEl = document.getElementById("status");
      const frameEl = document.getElementById("frame");
      let objectUrl = null;
      function post(payload) {
        if (window.ReactNativeWebView && window.ReactNativeWebView.postMessage) {
          window.ReactNativeWebView.postMessage(JSON.stringify(payload));
        }
      }
      function setStatus(note) {
        statusEl.textContent = note;
        post({ type: "status", note });
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
              if (objectUrl) URL.revokeObjectURL(objectUrl);
              objectUrl = URL.createObjectURL(blob);
              frameEl.src = objectUrl;
              setStatus("Relay frame polling active.");
            } catch (error) {
              setStatus(error.message || String(error));
            } finally {
              window.setTimeout(pump, 900);
            }
          };
          void pump();
          return;
        }
        const pc = new RTCPeerConnection();
        pc.createDataChannel("primer");
        pc.onconnectionstatechange = () => setStatus("Peer state: " + pc.connectionState);
        pc.ondatachannel = (event) => {
          if (event.channel.label === "frames") {
            event.channel.binaryType = "arraybuffer";
            event.channel.onmessage = (msg) => {
              if (objectUrl) URL.revokeObjectURL(objectUrl);
              objectUrl = URL.createObjectURL(new Blob([msg.data], { type: "image/jpeg" }));
              frameEl.src = objectUrl;
            };
          }
          if (event.channel.label === "events") {
            event.channel.onmessage = (msg) => {
              try {
                const payload = JSON.parse(String(msg.data));
                if (payload.session) post({ type: "session", session: payload.session });
                if (payload.error) setStatus(payload.error);
              } catch {}
            };
          }
        };
        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);
        const res = await fetch(cfg.baseUrl + "/remote-runtime/sessions/" + encodeURIComponent(cfg.sessionId) + "/webrtc/offer", {
          method: "POST",
          headers: { ...cfg.headers, "Content-Type": "application/json" },
          body: JSON.stringify({ type: offer.type, sdp: offer.sdp }),
        });
        const data = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(data.error || "WebRTC offer failed");
        if (data.session) post({ type: "session", session: data.session });
        if (data.note) setStatus(data.note);
        await pc.setRemoteDescription({ type: data.answer.type || "answer", sdp: data.answer.sdp || "" });
      }
      frameEl.addEventListener("click", async (event) => {
        if (!frameEl.naturalWidth || !frameEl.naturalHeight) return;
        const rect = frameEl.getBoundingClientRect();
        const x = Math.round(((event.clientX - rect.left) / rect.width) * frameEl.naturalWidth);
        const y = Math.round(((event.clientY - rect.top) / rect.height) * frameEl.naturalHeight);
        try {
          await sendControl({ action: "tap", x, y });
        } catch (error) {
          setStatus(error.message || String(error));
        }
      });
      start().catch((error) => setStatus(error.message || String(error)));
    </script>
  </body>
</html>`;
}
