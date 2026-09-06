import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Modal, Pressable, StyleSheet, Text, View } from "react-native";
import { Camera, useCameraDevice, useCameraPermission } from "react-native-vision-camera";
import type { ThemeColors } from "../constants/colors";
import { RecordedMouthFrameSource } from "../lib/silentInput/nativeSource";
import { UserMachineVSRRecognizer } from "../lib/silentInput/client";
import { silentInputContextTerms } from "../lib/silentInput/context";
import type { MouthFrame } from "../lib/silentInput/types";

type Props = {
  visible: boolean;
  colors: ThemeColors;
  targetDeviceId: string;
  projectName?: string;
  onCancel(): void;
  onTranscription(text: string): void;
};

const MAX_CAPTURE_MS = 8_000;

export function SilentInputModal({ visible, colors: c, targetDeviceId, projectName, onCancel, onTranscription }: Props) {
  const camera = useRef<Camera>(null);
  const device = useCameraDevice("front");
  const permission = useCameraPermission();
  const [capturing, setCapturing] = useState(false);
  const [processing, setProcessing] = useState(false);
  const [backendReady, setBackendReady] = useState<boolean | null>(null);
  const [text, setText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [startedAt, setStartedAt] = useState(0);
  const stopTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (visible && !permission.hasPermission) void permission.requestPermission();
    if (!visible) {
      setText("");
      setError(null);
      setCapturing(false);
      setProcessing(false);
      setBackendReady(null);
    }
  }, [visible, permission.hasPermission]);

  useEffect(() => {
    if (!visible) return;
    let cancelled = false;
    setBackendReady(null);
    setError(null);
    const recognizer = new UserMachineVSRRecognizer(targetDeviceId);
    void recognizer.initialize()
      .then(() => { if (!cancelled) setBackendReady(true); })
      .catch((cause) => {
        if (cancelled) return;
        setBackendReady(false);
        setError(cause instanceof Error ? cause.message : String(cause));
      });
    return () => { cancelled = true; void recognizer.dispose(); };
  }, [targetDeviceId, visible]);

  useEffect(() => () => { if (stopTimer.current) clearTimeout(stopTimer.current); }, []);

  const transcribeVideo = useCallback(async (path: string) => {
    setCapturing(false);
    setProcessing(true);
    const source = new RecordedMouthFrameSource(path);
    try {
      const captureMs = Math.max(0, Date.now() - startedAt);
      await source.start();
      const frames: MouthFrame[] = [];
      for await (const frame of source.frames()) frames.push(frame);
      await source.stop();
      if (frames.length < 8) throw new Error("No stable mouth was visible. Face the camera and try again.");
      const recognizer = new UserMachineVSRRecognizer(targetDeviceId);
      await recognizer.initialize();
      const result = await recognizer.recognize(frames, {
        language: "en",
        contextualTerms: silentInputContextTerms(projectName),
        maxAlternatives: 3,
      });
      await recognizer.dispose();
      setText(result.text.trim());
      if (!result.text.trim()) throw new Error("No speech was recognized. Try a short command again.");
      // Metrics stay local; mouth pixels are never included in analytics.
      console.info("[silent-input] complete", { backend: "user-machine", captureMs, cropMs: source.cropDurationMs, ...result.metrics });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setProcessing(false);
      await source.dispose();
    }
  }, [projectName, startedAt, targetDeviceId]);

  const start = useCallback(() => {
    if (!camera.current || capturing || processing) return;
    setError(null);
    setText("");
    setCapturing(true);
    setStartedAt(Date.now());
    camera.current.startRecording({
      fileType: "mp4",
      videoCodec: "h264",
      onRecordingFinished: (video) => { void transcribeVideo(video.path); },
      onRecordingError: (cause) => { setCapturing(false); setError(cause.message); },
    });
    stopTimer.current = setTimeout(() => { void camera.current?.stopRecording(); }, MAX_CAPTURE_MS);
  }, [capturing, processing, transcribeVideo]);

  const stop = useCallback(() => {
    if (stopTimer.current) clearTimeout(stopTimer.current);
    stopTimer.current = null;
    if (capturing) void camera.current?.stopRecording();
  }, [capturing]);

  const close = useCallback(() => {
    if (capturing) void camera.current?.cancelRecording().catch(() => {});
    onCancel();
  }, [capturing, onCancel]);

  return (
    <Modal visible={visible} animationType="slide" presentationStyle="fullScreen" onRequestClose={close}>
      <View style={[styles.root, { backgroundColor: c.bg }]}>
        <View style={styles.cameraShell}>
          {permission.hasPermission && device ? (
            <Camera ref={camera} style={StyleSheet.absoluteFill} device={device} isActive={visible} video audio={false} />
          ) : (
            <View style={[StyleSheet.absoluteFill, styles.center]}><Text style={{ color: c.textSecondary }}>Front camera permission is required.</Text></View>
          )}
          <View pointerEvents="none" style={[styles.faceGuide, { borderColor: capturing ? c.accent : "rgba(255,255,255,0.65)" }]}>
            <View style={styles.mouthGuide} />
          </View>
        </View>
        <View style={[styles.panel, { backgroundColor: c.bgCard }]}>
          <Text style={[styles.status, { color: c.textPrimary }]}>{backendReady === null ? "Checking your Yaver machine…" : processing ? "Reading lips locally on your machine…" : capturing ? "Listening visually… release when finished" : "Hold to Lip Read"}</Text>
          {processing || backendReady === null ? <ActivityIndicator color={c.accent} style={{ marginVertical: 12 }} /> : null}
          {text ? <Text style={[styles.transcription, { color: c.textPrimary }]}>{text}</Text> : null}
          {error ? <Text accessibilityRole="alert" style={{ color: c.error, marginTop: 10 }}>{error}</Text> : null}
          {!text && !processing && backendReady ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Hold to lip read"
              onPressIn={start}
              onPressOut={stop}
              disabled={!permission.hasPermission || !device}
              style={[styles.hold, { backgroundColor: capturing ? c.error : c.accent }]}
            ><Text style={styles.holdText}>{capturing ? "Release" : "Hold"}</Text></Pressable>
          ) : null}
          <View style={styles.actions}>
            <Pressable onPress={close} style={styles.action}><Text style={{ color: c.textSecondary }}>Cancel</Text></Pressable>
            <Pressable
              disabled={!text}
              onPress={() => { onTranscription(text); onCancel(); }}
              style={[styles.action, { opacity: text ? 1 : 0.35 }]}
              accessibilityLabel="Use silent transcription in composer"
            ><Text style={{ color: c.accent, fontWeight: "700" }}>Use transcription</Text></Pressable>
          </View>
          <Text style={[styles.privacy, { color: c.textMuted }]}>No audio is captured. The temporary full video is deleted after on-phone mouth cropping; only 96×96 grayscale mouth crops cross your encrypted Yaver connection. Review the text, then tap Send.</Text>
        </View>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  cameraShell: { flex: 1, minHeight: 320, overflow: "hidden" },
  center: { alignItems: "center", justifyContent: "center", padding: 24 },
  faceGuide: { position: "absolute", width: 220, height: 285, borderWidth: 2, borderRadius: 110, left: "50%", top: "50%", marginLeft: -110, marginTop: -142 },
  mouthGuide: { position: "absolute", width: 96, height: 56, borderWidth: 1, borderColor: "rgba(255,255,255,0.75)", borderRadius: 28, left: 60, bottom: 52 },
  panel: { paddingHorizontal: 20, paddingTop: 18, paddingBottom: 28, minHeight: 260 },
  status: { fontSize: 16, fontWeight: "700" },
  transcription: { fontSize: 22, lineHeight: 30, marginTop: 14 },
  hold: { alignSelf: "center", width: 88, height: 88, borderRadius: 44, alignItems: "center", justifyContent: "center", marginTop: 18 },
  holdText: { color: "white", fontWeight: "800", fontSize: 16 },
  actions: { flexDirection: "row", justifyContent: "space-between", marginTop: 18 },
  action: { minHeight: 44, justifyContent: "center", paddingHorizontal: 8 },
  privacy: { fontSize: 11, lineHeight: 16, marginTop: 8 },
});
