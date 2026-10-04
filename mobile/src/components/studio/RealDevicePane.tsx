import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { WebView } from "react-native-webview";
import { buildRemoteRuntimeViewerHtml } from "../../../app/remote-runtime";
import { useColors } from "../../context/ThemeContext";
import { initialRemoteRuntimeTransport, shouldFallbackToRelayFrames } from "../../lib/remoteRuntimeTransport";
import { quicClient, type RegisteredRealDevice, type RemoteRuntimeSession } from "../../lib/quic";

type Props = {
  projectPath: string;
  framework: string;
};

/** Live, interactive physical-device source for Vibe Studio's left pane. */
export function RealDevicePane({ projectPath, framework }: Props) {
  const c = useColors();
  const [devices, setDevices] = useState<RegisteredRealDevice[]>([]);
  const [selectedID, setSelectedID] = useState<string>("");
  const [session, setSession] = useState<RemoteRuntimeSession | null>(null);
  const [phase, setPhase] = useState("Looking for devices…");
  const [busy, setBusy] = useState(true);
  const ownedSession = useRef<string | null>(null);
  const relayFallbackStarted = useRef(false);

  const load = useCallback(async () => {
    setBusy(true);
    setPhase("Looking for devices…");
    try {
      const next = await quicClient.listRealDevices();
      setDevices(next);
      setSelectedID((current) => next.some((d) => d.id === current) ? current : next[0]?.id || "");
      setPhase(next.length ? "Ready to stream" : "No authorized Android device is attached to this box.");
    } catch (error) {
      setPhase(error instanceof Error ? error.message : "Could not inspect attached devices.");
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    const previous = ownedSession.current;
    ownedSession.current = null;
    setSession(null);
    relayFallbackStarted.current = false;
    if (previous) void quicClient.closeRemoteRuntimeSession(previous).catch(() => undefined);
    void load();
  }, [framework, load, projectPath]);

  useEffect(() => () => {
    if (ownedSession.current) void quicClient.closeRemoteRuntimeSession(ownedSession.current).catch(() => undefined);
  }, []);

  const open = useCallback(async () => {
    if (!selectedID || busy) return;
    setBusy(true);
    relayFallbackStarted.current = false;
    setPhase("Connecting live device stream…");
    try {
      if (ownedSession.current) {
        await quicClient.closeRemoteRuntimeSession(ownedSession.current).catch(() => undefined);
      }
      const next = await quicClient.startRemoteRuntimeSession(
        projectPath,
        framework,
        "android-device",
        initialRemoteRuntimeTransport(),
        selectedID,
      );
      ownedSession.current = next.id;
      setSession(next);
      setPhase("Negotiating WebRTC…");
    } catch (error) {
      setSession(null);
      setPhase(error instanceof Error ? error.message : "Could not open the real device.");
    } finally {
      setBusy(false);
    }
  }, [busy, framework, projectPath, selectedID]);

  const fallbackToRelayFrames = useCallback(async (failed: RemoteRuntimeSession, reason?: string) => {
    if (!shouldFallbackToRelayFrames({
      relayAvailable: !!quicClient.activeRelayBaseUrl,
      currentMode: failed.transportMode,
      failureReason: reason,
      alreadyAttempted: relayFallbackStarted.current,
    })) return false;
    relayFallbackStarted.current = true;
    setPhase("Live media is blocked; switching to compatibility frames…");
    try {
      await quicClient.closeRemoteRuntimeSession(failed.id).catch(() => undefined);
      const next = await quicClient.startRemoteRuntimeSession(
        projectPath, framework, "android-device", "relay-jpeg-poll", selectedID,
      );
      ownedSession.current = next.id;
      setSession(next);
      setPhase("Waiting for the first relay frame…");
      return true;
    } catch (error) {
      setPhase(error instanceof Error ? error.message : "Compatibility frames could not connect.");
      return false;
    }
  }, [framework, projectPath, selectedID]);

  if (session) {
    return (
      <View style={styles.fill} testID="studio-real-device-stream">
        <WebView
          source={{ html: buildRemoteRuntimeViewerHtml(quicClient.baseUrl, quicClient.getAuthHeaders(), session) }}
          originWhitelist={["*"]}
          javaScriptEnabled
          scrollEnabled={false}
          style={styles.viewer}
          onMessage={(event) => {
            try {
              const message = JSON.parse(event.nativeEvent.data);
              if (message?.type === "first-frame") setPhase("Live");
              else if (message?.type === "stream-stalled") setPhase("Stream stalled — reconnect the device or box.");
              else if (message?.type === "stream-failed") {
                if (message.reason === "ice-failed") {
                  void fallbackToRelayFrames(session, message.reason).then((started) => {
                    if (!started) setPhase(message.message || "Live media could not connect.");
                  });
                } else {
                  setPhase(message.message || "Live media could not connect.");
                }
              }
              else if (message?.session) setSession(message.session as RemoteRuntimeSession);
            } catch { /* viewer status is advisory */ }
          }}
        />
        <View style={styles.liveBadge} pointerEvents="none">
          <View style={styles.liveDot} />
          <Text style={styles.liveText}>{phase}</Text>
        </View>
      </View>
    );
  }

  return (
    <View style={[styles.empty, { backgroundColor: c.bgCard }]} testID="studio-real-device-picker">
      <Text style={[styles.title, { color: c.textPrimary }]}>Real device</Text>
      <Text style={[styles.note, { color: c.textMuted }]}>{phase}</Text>
      {devices.map((device) => (
        <Pressable
          key={device.id}
          onPress={() => setSelectedID(device.id)}
          style={[styles.device, { borderColor: selectedID === device.id ? c.accent : c.border }]}
          accessibilityRole="radio"
          accessibilityState={{ selected: selectedID === device.id }}
          accessibilityLabel={`${device.name}, ${device.kind}, ${device.transport}`}
        >
          <Text style={[styles.deviceName, { color: c.textPrimary }]}>{device.name}</Text>
          <Text style={[styles.deviceMeta, { color: c.textMuted }]}>Android {device.osVersion || ""} · {device.kind} · {device.transport}</Text>
        </Pressable>
      ))}
      <View style={styles.actions}>
        <Pressable onPress={() => void load()} disabled={busy} style={[styles.secondary, { borderColor: c.border }]}>
          <Text style={{ color: c.textSecondary, fontWeight: "700" }}>Refresh</Text>
        </Pressable>
        {selectedID ? (
          <Pressable onPress={() => void open()} disabled={busy} style={[styles.primary, { backgroundColor: c.accent }]}>
            {busy ? <ActivityIndicator size="small" color="#fff" /> : null}
            <Text style={styles.primaryText}>Stream device</Text>
          </Pressable>
        ) : null}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  fill: { flex: 1, backgroundColor: "#000" },
  viewer: { flex: 1, backgroundColor: "#000" },
  empty: { flex: 1, alignItems: "center", justifyContent: "center", padding: 18, gap: 10 },
  title: { fontSize: 17, fontWeight: "800" },
  note: { fontSize: 13, textAlign: "center", maxWidth: 360 },
  device: { width: "100%", maxWidth: 360, borderWidth: 1, borderRadius: 12, padding: 12 },
  deviceName: { fontSize: 14, fontWeight: "700" },
  deviceMeta: { fontSize: 12, marginTop: 3, textTransform: "capitalize" },
  actions: { flexDirection: "row", gap: 10, marginTop: 4 },
  secondary: { borderWidth: 1, borderRadius: 10, paddingHorizontal: 14, paddingVertical: 10 },
  primary: { borderRadius: 10, paddingHorizontal: 14, paddingVertical: 10, flexDirection: "row", gap: 7 },
  primaryText: { color: "#fff", fontWeight: "800" },
  liveBadge: { position: "absolute", top: 8, left: 8, flexDirection: "row", alignItems: "center", gap: 6, backgroundColor: "rgba(0,0,0,0.68)", borderRadius: 10, paddingHorizontal: 8, paddingVertical: 5 },
  liveDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: "#22c55e" },
  liveText: { color: "#fff", fontSize: 11, fontWeight: "700" },
});
