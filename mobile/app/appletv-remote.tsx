// Apple TV Remote — control an Apple TV running behind a Yaver agent (e.g. a
// home Raspberry Pi), watch its now-playing card, and view the home capture
// card's video, from the phone / a car head unit / a glass HUD. All actions go
// over the SAME mesh transport as everything else (LAN-direct first, relay
// fallback) via appletvClient → ops verbs. Control + metadata is always-legal;
// capture video is the user's OWN non-protected source only (HDCP input is
// reported, never streamed).
//
// ?surface=glass renders a compact HUD layout (now-playing + a small D-pad) for
// Android glasses / WebView. The same screen, a tighter skin.
import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Image, Pressable, ScrollView, Text, View } from "react-native";
import { Platform } from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import {
  appletvClient,
  type AppleTVTarget,
  type CaptureStatus,
  type NowPlaying,
  type RemoteKey,
} from "../src/lib/appletvClient";

// A few common tvOS app bundle IDs for quick-launch shortcuts.
const APP_SHORTCUTS: { label: string; bundle: string }[] = [
  { label: "TV", bundle: "com.apple.TVAppLive" },
  { label: "Music", bundle: "com.apple.TVMusic" },
  { label: "Podcasts", bundle: "com.apple.podcasts" },
  { label: "Settings", bundle: "com.apple.TVSettings" },
];

export default function AppleTVRemoteScreen() {
  const c = useColors();
  const router = useRouter();
  const params = useLocalSearchParams<{ surface?: string }>();
  const glass = params.surface === "glass";
  const deviceCtx = useDevice();
  const devices = (deviceCtx as any).devices as any[];

  const [deviceId, setDeviceId] = useState("");
  const [np, setNp] = useState<NowPlaying | null>(null);
  const [cap, setCap] = useState<CaptureStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [tick, setTick] = useState(0); // cache-buster for the capture frame
  const liveRef = useRef(true);

  const target = useCallback((): AppleTVTarget | undefined => {
    const d = devices?.find((x) => x.id === deviceId || x.deviceId === deviceId);
    if (!deviceId) return undefined;
    return { id: deviceId, lanIps: d?.lanIps, host: d?.host, port: 18080 };
  }, [deviceId, devices]);

  const refreshNowPlaying = useCallback(async () => {
    const t = target();
    if (!t) return;
    try {
      const r = await appletvClient.nowPlaying(t);
      if (liveRef.current && r && !(r as any).code) setNp(r);
    } catch {
      /* transient — keep last */
    }
  }, [target]);

  const refreshCapture = useCallback(async () => {
    const t = target();
    if (!t) return;
    try {
      const r = await appletvClient.captureStatus(t);
      if (liveRef.current) setCap(r);
    } catch {
      /* ignore */
    }
  }, [target]);

  // Poll now-playing while mounted.
  useEffect(() => {
    liveRef.current = true;
    if (!deviceId) return;
    refreshNowPlaying();
    refreshCapture();
    const np = setInterval(refreshNowPlaying, 2500);
    const cp = setInterval(() => setTick((x) => x + 1), 1000);
    return () => {
      liveRef.current = false;
      clearInterval(np);
      clearInterval(cp);
    };
  }, [deviceId, refreshNowPlaying, refreshCapture]);

  const send = useCallback(
    async (fn: () => Promise<any>) => {
      setBusy(true);
      setMsg(null);
      try {
        const r = await fn();
        if (r?.ok === false || r?.code) setMsg(r?.error || "command failed");
        refreshNowPlaying();
      } catch (e: any) {
        setMsg(e?.message || "command failed");
      } finally {
        setBusy(false);
      }
    },
    [refreshNowPlaying],
  );

  const key = (k: RemoteKey) => () => send(() => appletvClient.key(target()!, k));

  const toggleCapture = useCallback(async () => {
    const t = target();
    if (!t) return;
    setBusy(true);
    try {
      if (cap?.running) await appletvClient.captureStop(t);
      else await appletvClient.captureStart(t, { fps: 6 });
      await refreshCapture();
    } finally {
      setBusy(false);
    }
  }, [cap, target, refreshCapture]);

  // ---------- styles ----------
  const card = { backgroundColor: c.bgCard, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 12, padding: 14, marginBottom: 12 };
  const h = { color: c.textPrimary, fontSize: 15, fontWeight: "700" as const, marginBottom: 10 };
  const padBtn = (label: string, onPress: () => void, big = false) => (
    <Pressable
      onPress={onPress}
      disabled={busy}
      style={{
        backgroundColor: c.bgCardElevated,
        width: big ? 84 : 64,
        height: big ? 84 : 64,
        borderRadius: big ? 42 : 14,
        alignItems: "center",
        justifyContent: "center",
        opacity: busy ? 0.5 : 1,
      }}
    >
      <Text style={{ color: c.textPrimary, fontSize: big ? 18 : 22, fontWeight: "700" }}>{label}</Text>
    </Pressable>
  );

  // ---------- device picker ----------
  if (!deviceId) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader title="Apple TV Remote" onBack={() => router.back()} />
        <ScrollView contentContainerStyle={{ padding: 16 }}>
          <Pressable
            onPress={() => router.push("/stream-camera")}
            style={[card, { flexDirection: "row", justifyContent: "space-between", alignItems: "center", borderColor: c.accent }]}
          >
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>📷  Stream this phone's camera</Text>
            <Text style={{ color: c.textMuted, fontSize: 12 }}>→</Text>
          </Pressable>
          <Text style={[h, { marginBottom: 14 }]}>Pick the device running the Apple TV engine (your home Pi)</Text>
          {(devices || []).map((d) => (
            <Pressable
              key={d.id || d.deviceId}
              onPress={() => setDeviceId(d.id || d.deviceId)}
              style={[card, { flexDirection: "row", justifyContent: "space-between" }]}
            >
              <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{d.name || d.alias || d.id || d.deviceId}</Text>
              <Text style={{ color: c.textMuted }}>{d.online ? "online" : "offline"}</Text>
            </Pressable>
          ))}
          {(!devices || devices.length === 0) && <Text style={{ color: c.textMuted }}>No devices yet. Sign a box in first.</Text>}
        </ScrollView>
      </View>
    );
  }

  const artworkUri =
    np?.artwork_b64 ? `data:${np.mimetype || "image/jpeg"};base64,${np.artwork_b64}` : null;

  // ---------- now-playing card (shared by full + glass) ----------
  const nowPlayingCard = (
    <View style={[card, { flexDirection: "row", alignItems: "center", gap: 12 }]}>
      {artworkUri ? (
        <Image source={{ uri: artworkUri }} style={{ width: glass ? 48 : 72, height: glass ? 48 : 72, borderRadius: 8 }} />
      ) : (
        <View style={{ width: glass ? 48 : 72, height: glass ? 48 : 72, borderRadius: 8, backgroundColor: c.bgCardElevated }} />
      )}
      <View style={{ flex: 1 }}>
        <Text style={{ color: c.textPrimary, fontWeight: "700" }} numberOfLines={1}>
          {np?.title || "Nothing playing"}
        </Text>
        <Text style={{ color: c.textMuted, fontSize: 12 }} numberOfLines={1}>
          {[np?.artist, np?.app].filter(Boolean).join(" · ") || "—"}
        </Text>
        {!!np?.state && <Text style={{ color: c.textSecondary, fontSize: 11 }}>{np.state}</Text>}
      </View>
    </View>
  );

  const transportRow = (
    <View style={{ flexDirection: "row", justifyContent: "space-around", marginBottom: 12 }}>
      {padBtn("⏮", key("previous"))}
      {padBtn("⏯", key("play_pause"), true)}
      {padBtn("⏭", key("next"))}
    </View>
  );

  const dpad = (
    <View style={{ alignItems: "center", marginBottom: 12 }}>
      {padBtn("▲", key("up"))}
      <View style={{ flexDirection: "row", alignItems: "center", gap: 10, marginVertical: 10 }}>
        {padBtn("◀", key("left"))}
        {padBtn("OK", key("select"), true)}
        {padBtn("▶", key("right"))}
      </View>
      {padBtn("▼", key("down"))}
    </View>
  );

  // ---------- glass HUD (compact) ----------
  if (glass) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <ScrollView contentContainerStyle={{ padding: 10 }}>
          {nowPlayingCard}
          {transportRow}
          {!!msg && <Text style={{ color: c.error || "#f55", fontSize: 11 }}>{msg}</Text>}
        </ScrollView>
      </View>
    );
  }

  // ---------- full surface (phone / head unit) ----------
  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Apple TV Remote" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16 }}>
        {nowPlayingCard}

        <View style={card}>
          <Text style={h}>Remote</Text>
          {dpad}
          <View style={{ flexDirection: "row", justifyContent: "space-around", marginBottom: 8 }}>
            <Pressable onPress={key("menu")} disabled={busy} style={{ paddingVertical: 8, paddingHorizontal: 16, backgroundColor: c.bgCardElevated, borderRadius: 10 }}>
              <Text style={{ color: c.textPrimary }}>Menu</Text>
            </Pressable>
            <Pressable onPress={key("home")} disabled={busy} style={{ paddingVertical: 8, paddingHorizontal: 16, backgroundColor: c.bgCardElevated, borderRadius: 10 }}>
              <Text style={{ color: c.textPrimary }}>Home</Text>
            </Pressable>
            <Pressable onPress={() => send(() => appletvClient.power(target()!, "off"))} disabled={busy} style={{ paddingVertical: 8, paddingHorizontal: 16, backgroundColor: c.bgCardElevated, borderRadius: 10 }}>
              <Text style={{ color: c.textPrimary }}>Power</Text>
            </Pressable>
          </View>
          {transportRow}
        </View>

        <View style={card}>
          <Text style={h}>Apps</Text>
          <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
            {APP_SHORTCUTS.map((a) => (
              <Pressable key={a.bundle} onPress={() => send(() => appletvClient.launchApp(target()!, a.bundle))} disabled={busy} style={{ paddingVertical: 8, paddingHorizontal: 14, backgroundColor: c.bgCardElevated, borderRadius: 10 }}>
                <Text style={{ color: c.textPrimary }}>{a.label}</Text>
              </Pressable>
            ))}
          </View>
        </View>

        {/* Home capture card — own non-protected sources only */}
        <View style={card}>
          <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginBottom: 10 }}>
            <Text style={h}>Home camera / capture</Text>
            <Pressable onPress={toggleCapture} disabled={busy} style={{ paddingVertical: 6, paddingHorizontal: 12, backgroundColor: cap?.running ? c.bgCardElevated : c.accent, borderRadius: 10 }}>
              <Text style={{ color: cap?.running ? c.textPrimary : c.textInverse }}>{cap?.running ? "Stop" : "Start"}</Text>
            </Pressable>
          </View>
          {cap?.running ? (
            // Agnostic: stream whatever the card provides, including black.
            // Android renders MJPEG directly; iOS polls the single-frame URL.
            <>
              <Image
                source={{
                  uri: Platform.OS === "android" ? quicClient.captureStreamUrl() : `${quicClient.captureFrameUrl()}&t=${tick}`,
                }}
                style={{ width: "100%", aspectRatio: 16 / 9, borderRadius: 8, backgroundColor: "#000" }}
                resizeMode="contain"
              />
              {cap?.blackHint ? (
                <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>{cap.blackHint}</Text>
              ) : null}
            </>
          ) : (
            <Text style={{ color: c.textMuted, fontSize: 12 }}>
              {cap?.ffmpeg === false ? "ffmpeg not installed on this box." : "Stopped. Start to stream a capture card on this box (satellite box, console, camera, PC…)."}
            </Text>
          )}
        </View>

        {busy && <ActivityIndicator color={c.accent} />}
        {!!msg && <Text style={{ color: c.error || "#f55", fontSize: 12, marginTop: 6 }}>{msg}</Text>}
        <Pressable onPress={() => setDeviceId("")} style={{ marginTop: 8, alignItems: "center" }}>
          <Text style={{ color: c.textMuted, fontSize: 12 }}>switch device</Text>
        </Pressable>
      </ScrollView>
    </View>
  );
}
