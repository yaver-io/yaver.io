// Stream camera — turn THIS phone into a live source (M10): capture the camera
// and push frames to a chosen box, which serves them through the stream plane.
// Viewers watch on their own account (web dashboard) or via a guest watch link.
// Low-fps JPEG push (snapshot cadence) — neutral tool, like OBS; what you stream
// and the right to it is yours.
import React, { useCallback, useEffect, useRef, useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { CameraView, useCameraPermissions } from "expo-camera";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { pushCameraFrame } from "../src/lib/cameraStreamClient";

const TICK_MS = 350; // ~3 fps; takePictureAsync is the bottleneck

export default function StreamCameraScreen() {
  const c = useColors();
  const router = useRouter();
  const deviceCtx = useDevice();
  const devices = (deviceCtx as any).devices as any[];

  const [permission, requestPermission] = useCameraPermissions();
  const [deviceId, setDeviceId] = useState("");
  const [facing, setFacing] = useState<"back" | "front">("back");
  const [streaming, setStreaming] = useState(false);
  const [pushed, setPushed] = useState(0);
  const [err, setErr] = useState<string | null>(null);

  const camRef = useRef<CameraView>(null);
  const aliveRef = useRef(false);
  const inFlight = useRef(false);

  const tick = useCallback(async () => {
    if (!aliveRef.current || inFlight.current || !camRef.current || !deviceId) return;
    inFlight.current = true;
    const started = Date.now();
    try {
      const shot = await camRef.current.takePictureAsync({ base64: true, quality: 0.3, skipProcessing: true });
      const b64 = (shot?.base64 || "").replace(/^data:[^,]+,/, "");
      if (b64 && aliveRef.current) {
        const ok = await pushCameraFrame(deviceId, "phone", b64);
        if (ok) setPushed((n) => n + 1);
        else setErr("push rejected (is the box reachable + signed in?)");
      }
    } catch (e: any) {
      setErr(e?.message || "capture failed");
    } finally {
      inFlight.current = false;
      if (aliveRef.current) {
        const wait = Math.max(0, TICK_MS - (Date.now() - started));
        setTimeout(tick, wait);
      }
    }
  }, [deviceId]);

  const start = useCallback(() => {
    if (!deviceId) return;
    setErr(null);
    setPushed(0);
    aliveRef.current = true;
    setStreaming(true);
    setTimeout(tick, 0);
  }, [deviceId, tick]);

  const stop = useCallback(() => {
    aliveRef.current = false;
    setStreaming(false);
  }, []);

  useEffect(() => () => { aliveRef.current = false; }, []);

  // permission gate
  if (!permission) {
    return <View style={{ flex: 1, backgroundColor: c.bg }} />;
  }
  if (!permission.granted) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader title="Stream camera" onBack={() => router.back()} />
        <View style={{ padding: 24 }}>
          <Text style={{ color: c.textPrimary, marginBottom: 12 }}>Camera permission is needed to stream this phone.</Text>
          <Pressable onPress={requestPermission} style={{ backgroundColor: c.accent, padding: 12, borderRadius: 10, alignItems: "center" }}>
            <Text style={{ color: c.textInverse, fontWeight: "700" }}>Grant camera access</Text>
          </Pressable>
        </View>
      </View>
    );
  }

  // device picker
  if (!deviceId) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader title="Stream camera" onBack={() => router.back()} />
        <ScrollView contentContainerStyle={{ padding: 16 }}>
          <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700", marginBottom: 12 }}>Pick the box to stream to</Text>
          <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 14 }}>Viewers (your account or a guest link) watch the camera through this box.</Text>
          {(devices || []).map((d) => (
            <Pressable key={d.id || d.deviceId} onPress={() => setDeviceId(d.id || d.deviceId)} style={{ flexDirection: "row", justifyContent: "space-between", backgroundColor: c.bgCard, borderColor: c.borderSubtle, borderWidth: 1, borderRadius: 12, padding: 14, marginBottom: 10 }}>
              <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{d.name || d.alias || d.id || d.deviceId}</Text>
              <Text style={{ color: c.textMuted }}>{d.online ? "online" : "offline"}</Text>
            </Pressable>
          ))}
          {(!devices || devices.length === 0) && <Text style={{ color: c.textMuted }}>No devices yet.</Text>}
        </ScrollView>
      </View>
    );
  }

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Stream camera" onBack={() => { stop(); router.back(); }} />
      <View style={{ flex: 1, padding: 16 }}>
        <View style={{ flex: 1, borderRadius: 14, overflow: "hidden", backgroundColor: "#000" }}>
          <CameraView ref={camRef} style={{ flex: 1 }} facing={facing} />
          {streaming && (
            <View style={{ position: "absolute", top: 10, left: 10, flexDirection: "row", alignItems: "center", gap: 6, backgroundColor: "rgba(0,0,0,0.5)", paddingHorizontal: 10, paddingVertical: 4, borderRadius: 20 }}>
              <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: "#ef4444" }} />
              <Text style={{ color: "#fff", fontSize: 12 }}>LIVE · {pushed}</Text>
            </View>
          )}
        </View>

        <View style={{ flexDirection: "row", gap: 10, marginTop: 12 }}>
          <Pressable onPress={() => setFacing((f) => (f === "back" ? "front" : "back"))} style={{ paddingVertical: 12, paddingHorizontal: 16, backgroundColor: c.bgCardElevated, borderRadius: 10 }}>
            <Text style={{ color: c.textPrimary }}>Flip</Text>
          </Pressable>
          <Pressable onPress={streaming ? stop : start} style={{ flex: 1, paddingVertical: 12, backgroundColor: streaming ? c.bgCardElevated : c.accent, borderRadius: 10, alignItems: "center" }}>
            <Text style={{ color: streaming ? c.textPrimary : c.textInverse, fontWeight: "700" }}>{streaming ? "Stop streaming" : "Start streaming"}</Text>
          </Pressable>
        </View>
        <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8 }}>
          Frames are pushed to the box and served as the "phone" source. Share a view-only link from the box's Apple TV / web dashboard.
        </Text>
        {!!err && <Text style={{ color: c.error || "#f55", fontSize: 12, marginTop: 6 }}>{err}</Text>}
        <Pressable onPress={() => { stop(); setDeviceId(""); }} style={{ marginTop: 8, alignItems: "center" }}>
          <Text style={{ color: c.textMuted, fontSize: 12 }}>switch box</Text>
        </Pressable>
      </View>
    </View>
  );
}
