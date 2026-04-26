// VibePreviewModal.tsx — full-screen live-preview modal.
//
// Renders the latest captured frame as <Image> (URL keyed by content hash so
// RN's image cache dedupes), a scrubable summary timeline at the bottom, and
// inline <Video> playback for clips. Subscribes to /vibing/preview/events
// for live updates and falls back to polling /status if the SSE drops.

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  View,
  Text,
  Image,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  ActivityIndicator,
  Alert,
} from "react-native";
import { Video, ResizeMode } from "expo-av";
import {
  clipPosterUrl,
  clipUrl,
  frameUrl,
  listClips,
  listSessions,
  recordAndUploadPhoneClip,
  startClip,
  startPreview,
  stopClip,
  stopPreview,
  subscribeEvents,
  VibeClipRecord,
  VibeClipSource,
  VibePreviewEvent,
  VibePreviewSession,
} from "../lib/vibePreview";
import { isNativeScreenRecorderAvailable } from "../lib/screenRecorder";
import { quicClient } from "../lib/quic";

interface Props {
  visible: boolean;
  project: string;
  targetUrl?: string;
  onClose: () => void;
}

export function VibePreviewModal({ visible, project, targetUrl, onClose }: Props) {
  const [session, setSession] = useState<VibePreviewSession | null>(null);
  const [latestHash, setLatestHash] = useState<string | null>(null);
  const [eventLog, setEventLog] = useState<VibePreviewEvent[]>([]);
  const [clips, setClips] = useState<VibeClipRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [activeClipId, setActiveClipId] = useState<string | null>(null);
  const unsubscribeRef = useRef<null | (() => void)>(null);

  // Establish or reuse a session when the modal opens.
  useEffect(() => {
    if (!visible) return;
    let cancelled = false;
    (async () => {
      setLoading(true);
      const sessions = await listSessions();
      let s = sessions.find((x) => x.project === project) || null;
      if (!s && targetUrl) {
        s = await startPreview({ project, targetUrl, mode: "live" });
      }
      if (!cancelled) {
        setSession(s);
        setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [visible, project, targetUrl]);

  // Refresh clip list when the modal opens (and after every clip_ready).
  const refreshClips = useCallback(async () => {
    setClips(await listClips(project));
  }, [project]);

  useEffect(() => {
    if (visible) refreshClips();
  }, [visible, refreshClips]);

  // Live event subscription. Re-establishes when project changes.
  useEffect(() => {
    if (!visible) return;
    if (unsubscribeRef.current) {
      unsubscribeRef.current();
      unsubscribeRef.current = null;
    }
    const unsub = subscribeEvents(project, {
      onEvent: (ev) => {
        setEventLog((prev) => {
          const next = [...prev, ev];
          // Keep the last 200 events in memory — same order as on-agent.
          return next.length > 200 ? next.slice(next.length - 200) : next;
        });
        if (ev.type === "frame" && ev.hash) setLatestHash(ev.hash);
        if (ev.type === "clip_ready") refreshClips();
      },
      onError: () => {
        // Silently let the modal show "no signal" until the next reconnect.
      },
    });
    unsubscribeRef.current = unsub;
    return () => {
      if (unsubscribeRef.current) unsubscribeRef.current();
      unsubscribeRef.current = null;
    };
  }, [visible, project, refreshClips]);

  // Stop the live capture but keep clips visible when the user dismisses
  // the modal. Closing the connection is preferable to leaving Chrome
  // running on the agent indefinitely.
  const handleClose = useCallback(() => {
    if (session) {
      // Best-effort — don't block the close on a slow stop.
      void stopPreview(session.project);
    }
    onClose();
  }, [session, onClose]);

  // Recording controls. Phone source uses the native ReplayKit /
  // MediaProjection bridge end-to-end (record locally → upload MP4 to
  // agent), all other sources go through the agent-side recorder.
  const handleRecord = useCallback(
    async (source?: VibeClipSource) => {
      if (source === "phone") {
        if (!isNativeScreenRecorderAvailable()) {
          Alert.alert("Phone recording unavailable", "Native screen recorder not loaded on this build.");
          return;
        }
        const result = await recordAndUploadPhoneClip({ project, durationSec: 12 });
        if (!result.ok) {
          Alert.alert("Phone recording failed", result.error ?? "Unknown error");
          return;
        }
        if (result.clip) {
          setClips((prev) => [result.clip!, ...prev.filter((c) => c.id !== result.clip!.id)]);
        }
        return;
      }
      const rec = await startClip({ project, source, durationMaxSec: 12 });
      if (!rec) {
        Alert.alert("Couldn't start recording", "Make sure a simulator is booted (or pass a custom source).");
        return;
      }
      setClips((prev) => [rec, ...prev.filter((c) => c.id !== rec.id)]);
    },
    [project],
  );

  const handleClipTap = useCallback(
    async (clip: VibeClipRecord) => {
      if (clip.status === "recording") {
        // Tap-to-stop while recording.
        const ok = await stopClip(clip.id);
        if (!ok) return;
        setActiveClipId(clip.id);
      } else if (clip.status === "ready") {
        setActiveClipId(clip.id);
      }
    },
    [],
  );

  const frameSrc = useMemo(() => {
    if (!latestHash) return null;
    const u = frameUrl(project, latestHash);
    if (!u) return null;
    return { uri: u, headers: quicClient.getAuthHeaders() };
  }, [project, latestHash]);

  const playingClip = activeClipId ? clips.find((c) => c.id === activeClipId) : null;
  const playingClipUri = playingClip && playingClip.status === "ready" ? clipUrl(playingClip.id) : null;

  return (
    <Modal visible={visible} animationType="slide" onRequestClose={handleClose}>
      <View style={styles.container}>
        <View style={styles.topBar}>
          <Pressable onPress={handleClose} style={styles.closeBtn}>
            <Text style={styles.closeBtnText}>Close</Text>
          </Pressable>
          <Text style={styles.title} numberOfLines={1}>
            Vibe Preview · {project}
          </Text>
          <Text style={styles.fps}>
            {session ? `${session.profile.fps} FPS · ${session.profile.name}` : ""}
          </Text>
        </View>

        <View style={styles.frameArea}>
          {loading && <ActivityIndicator color="#fff" size="large" />}
          {!loading && playingClipUri ? (
            <Video
              key={playingClipUri}
              source={{ uri: playingClipUri, headers: quicClient.getAuthHeaders() } as any}
              style={styles.video}
              useNativeControls
              resizeMode={ResizeMode.CONTAIN}
              shouldPlay
              isLooping={false}
              onPlaybackStatusUpdate={(st: any) => {
                if (st?.didJustFinish) setActiveClipId(null);
              }}
            />
          ) : null}
          {!loading && !playingClipUri && frameSrc ? (
            <Image source={frameSrc as any} style={styles.frame} resizeMode="contain" />
          ) : null}
          {!loading && !playingClipUri && !frameSrc ? (
            <Text style={styles.empty}>Waiting for first frame…</Text>
          ) : null}
        </View>

        <View style={styles.controls}>
          <Pressable style={styles.recordBtn} onPress={() => handleRecord()}>
            <Text style={styles.recordBtnText}>● Record clip</Text>
          </Pressable>
          <Pressable style={styles.recordBtnSecondary} onPress={() => handleRecord("sim-android")}>
            <Text style={styles.recordBtnSecondaryText}>Android</Text>
          </Pressable>
          <Pressable style={styles.recordBtnSecondary} onPress={() => handleRecord("phone")}>
            <Text style={styles.recordBtnSecondaryText}>Phone</Text>
          </Pressable>
        </View>

        <Text style={styles.sectionLabel}>Clips ({clips.length})</Text>
        <ScrollView horizontal style={styles.clipStrip} contentContainerStyle={styles.clipStripContent}>
          {clips.map((clip) => (
            <Pressable key={clip.id} style={styles.clipCard} onPress={() => handleClipTap(clip)}>
              {clip.status === "ready" && clipPosterUrl(clip.id) ? (
                <Image
                  source={{ uri: clipPosterUrl(clip.id)!, headers: quicClient.getAuthHeaders() } as any}
                  style={styles.poster}
                  resizeMode="cover"
                />
              ) : (
                <View style={[styles.poster, styles.posterFallback]}>
                  <Text style={styles.posterFallbackText}>
                    {clip.status === "recording" ? "● REC" : clip.status}
                  </Text>
                </View>
              )}
              <Text style={styles.clipMeta} numberOfLines={1}>
                {clip.source} · {clip.durationSec ? `${clip.durationSec.toFixed(0)}s` : "—"}
              </Text>
            </Pressable>
          ))}
          {clips.length === 0 ? <Text style={styles.empty}>No clips yet. Hit Record.</Text> : null}
        </ScrollView>

        <Text style={styles.sectionLabel}>Events ({eventLog.length})</Text>
        <ScrollView style={styles.events} contentContainerStyle={styles.eventsContent}>
          {eventLog.slice().reverse().slice(0, 30).map((ev, i) => (
            <Text key={i} style={styles.eventLine} numberOfLines={1}>
              {ev.ts.slice(11, 19)} · {ev.type}
              {ev.hash ? ` · ${ev.hash.slice(0, 6)}` : ""}
              {ev.clipId ? ` · ${ev.clipId}` : ""}
              {ev.message ? ` · ${ev.message}` : ""}
            </Text>
          ))}
        </ScrollView>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: "#000" },
  topBar: {
    flexDirection: "row",
    alignItems: "center",
    paddingTop: 48,
    paddingHorizontal: 12,
    paddingBottom: 8,
    backgroundColor: "#111",
  },
  closeBtn: { paddingVertical: 4, paddingHorizontal: 10, marginRight: 12 },
  closeBtnText: { color: "#0af", fontSize: 14 },
  title: { color: "#fff", fontSize: 16, fontWeight: "600", flex: 1 },
  fps: { color: "#888", fontSize: 11, fontVariant: ["tabular-nums"] },
  frameArea: {
    flex: 1,
    backgroundColor: "#000",
    alignItems: "center",
    justifyContent: "center",
  },
  frame: { width: "100%", height: "100%" },
  video: { width: "100%", height: "100%" },
  empty: { color: "#666", fontSize: 14 },
  controls: { flexDirection: "row", padding: 8, backgroundColor: "#111", gap: 8 },
  recordBtn: {
    backgroundColor: "#c0392b",
    flex: 2,
    paddingVertical: 10,
    borderRadius: 6,
    alignItems: "center",
  },
  recordBtnText: { color: "#fff", fontWeight: "700" },
  recordBtnSecondary: {
    backgroundColor: "#333",
    flex: 1,
    paddingVertical: 10,
    borderRadius: 6,
    alignItems: "center",
  },
  recordBtnSecondaryText: { color: "#ddd" },
  sectionLabel: {
    color: "#888",
    fontSize: 11,
    paddingHorizontal: 12,
    paddingTop: 8,
    textTransform: "uppercase",
    letterSpacing: 0.6,
  },
  clipStrip: { maxHeight: 110, backgroundColor: "#111" },
  clipStripContent: { padding: 8, gap: 8 },
  clipCard: { width: 120, marginRight: 8 },
  poster: { width: 120, height: 80, backgroundColor: "#222", borderRadius: 4 },
  posterFallback: { alignItems: "center", justifyContent: "center" },
  posterFallbackText: { color: "#bbb", fontSize: 11 },
  clipMeta: { color: "#aaa", fontSize: 10, marginTop: 4 },
  events: { maxHeight: 140, backgroundColor: "#111" },
  eventsContent: { padding: 8 },
  eventLine: { color: "#9c9", fontSize: 10, fontFamily: "Menlo" },
});
