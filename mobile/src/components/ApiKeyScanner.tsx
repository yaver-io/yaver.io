import React from "react";
import { Linking, Pressable, StyleSheet, Text, View } from "react-native";
import { CameraView, useCameraPermissions, type BarcodeScanningResult } from "expo-camera";

import { useColors } from "../context/ThemeContext";
import {
  parseHetznerCloudTokenText,
  parseRecognizedAPIKeyText,
  parseScannedAPIKey,
  type ScannedAPIKey,
} from "../lib/apiKeyScan";

type ScanMode = "text" | "qr";

export default function ApiKeyScanner({
  provider,
  onScanned,
  onClose,
}: {
  provider?: string;
  onScanned: (value: ScannedAPIKey) => void;
  onClose: () => void;
}) {
  const c = useColors();
  const cameraRef = React.useRef<CameraView | null>(null);
  const [permission, requestPermission] = useCameraPermissions();
  const [mode, setMode] = React.useState<ScanMode>("text");
  const [busy, setBusy] = React.useState(false);
  const [hint, setHint] = React.useState("Place the API-key text inside the frame");
  const handled = React.useRef(false);

  const deliver = React.useCallback((parsed: ScannedAPIKey | null) => {
    if (!parsed) {
      setHint(mode === "text" ? "No API-key string found—move closer and try again" : "No API key found in that QR");
      return false;
    }
    if (parsed.provider && provider && parsed.provider !== provider.toLowerCase()) {
      setHint(`That key is for ${parsed.provider}, not ${provider}`);
      return false;
    }
    handled.current = true;
    onScanned(parsed);
    return true;
  }, [mode, onScanned, provider]);

  const parseForProvider = React.useCallback((value: string) => {
    return provider?.toLowerCase() === "hetzner"
      ? parseHetznerCloudTokenText(value)
      : parseRecognizedAPIKeyText(value);
  }, [provider]);

  const onBarcode = React.useCallback((result: BarcodeScanningResult) => {
    if (mode !== "qr" || handled.current) return;
    deliver(provider?.toLowerCase() === "hetzner"
      ? parseHetznerCloudTokenText(result?.data ?? "")
      : parseScannedAPIKey(result?.data ?? ""));
  }, [deliver, mode, provider]);

  const captureText = React.useCallback(async () => {
    if (!cameraRef.current || busy || handled.current) return;
    setBusy(true);
    setHint("Reading text on this phone…");
    try {
      // No base64 and no upload: ML Kit reads the temporary camera file on
      // device. The resulting candidate only fills the editable key field.
      const photo = await cameraRef.current.takePictureAsync({ quality: 0.9, skipProcessing: false });
      if (!photo?.uri) throw new Error("Camera did not return an image");
      const module = await import("@react-native-ml-kit/text-recognition");
      const result = await module.default.recognize(photo.uri);
      deliver(parseForProvider(result.text));
      // CameraView owns this temporary file. Do not copy it into the media
      // library or application storage; the OS clears the camera cache.
    } catch {
      setHint("Couldn’t read the string—try again or enter it manually");
    } finally {
      setBusy(false);
    }
  }, [busy, deliver, parseForProvider]);

  const choosePhoto = React.useCallback(async () => {
    if (busy || handled.current) return;
    setBusy(true);
    setHint("Reading this image on your phone…");
    try {
      const picker = await import("expo-image-picker");
      const selection = await picker.launchImageLibraryAsync({
        mediaTypes: ["images"],
        allowsEditing: false,
        quality: 1,
        base64: false,
        exif: false,
      });
      if (selection.canceled) {
        setHint("Place the API-key text inside the frame");
        return;
      }
      const uri = selection.assets[0]?.uri;
      if (!uri) throw new Error("Photo picker did not return an image");
      const module = await import("@react-native-ml-kit/text-recognition");
      const result = await module.default.recognize(uri);
      deliver(parseForProvider(result.text));
    } catch {
      setHint("Couldn’t read that image—try another or enter it manually");
    } finally {
      setBusy(false);
    }
  }, [busy, deliver, parseForProvider]);

  const switchMode = React.useCallback((next: ScanMode) => {
    handled.current = false;
    setMode(next);
    setHint(next === "text" ? "Place the API-key text inside the frame" : "Point at an API-key QR on your desktop");
  }, []);

  if (!permission) {
    return <View style={[styles.fill, styles.center, { backgroundColor: c.bg }]}><Text style={{ color: c.textMuted }}>Preparing camera…</Text></View>;
  }
  if (!permission.granted) {
    return (
      <View style={[styles.fill, styles.center, { backgroundColor: c.bg, padding: 32 }]}>
        <Text style={[styles.title, { color: c.textPrimary }]}>Scan API key</Text>
        <Text style={[styles.body, { color: c.textSecondary }]}>Camera text recognition runs on-device. The key goes only to the selected machine.</Text>
        <Pressable onPress={() => void (permission.canAskAgain ? requestPermission() : Linking.openSettings())} style={[styles.primary, { backgroundColor: c.accent }]}>
          <Text style={styles.primaryText}>{permission.canAskAgain ? "Allow camera" : "Open phone settings"}</Text>
        </Pressable>
        <Pressable onPress={onClose} style={styles.cancel}><Text style={{ color: c.textSecondary, fontWeight: "600" }}>Enter manually</Text></Pressable>
      </View>
    );
  }
  return (
    <View style={[styles.fill, { backgroundColor: "#000" }]}>
      <CameraView
        ref={cameraRef}
        style={StyleSheet.absoluteFill}
        facing="back"
        barcodeScannerSettings={mode === "qr" ? { barcodeTypes: ["qr"] } : undefined}
        onBarcodeScanned={mode === "qr" ? onBarcode : undefined}
      />
      <View style={styles.overlay} pointerEvents="box-none">
        <View style={styles.modeSwitch}>
          {(["text", "qr"] as const).map((item) => (
            <Pressable key={item} onPress={() => switchMode(item)} style={[styles.modeButton, mode === item && styles.modeButtonActive]}>
              <Text style={[styles.modeText, mode === item && styles.modeTextActive]}>{item === "text" ? "Text" : "QR"}</Text>
            </Pressable>
          ))}
        </View>
        <Text style={styles.hint}>{hint}</Text>
        {provider?.toLowerCase() === "hetzner" ? (
          <Text style={styles.formatHint}>Hetzner Cloud project token · 64 characters · S3 keys are not accepted</Text>
        ) : null}
        <View style={[styles.reticle, mode === "qr" && styles.reticleQR]} />
        <Text style={styles.privacy}>On-device scan · never uploaded or stored in Convex</Text>
        {mode === "text" ? (
          <>
            <Pressable disabled={busy} onPress={() => void captureText()} style={[styles.capture, busy && { opacity: 0.6 }]}>
              <View style={styles.captureInner} />
            </Pressable>
            <Pressable disabled={busy} onPress={() => void choosePhoto()} style={[styles.photo, busy && { opacity: 0.6 }]}>
              <Text style={styles.closeText}>Choose photo</Text>
            </Pressable>
          </>
        ) : null}
        <Pressable onPress={onClose} style={styles.close}><Text style={styles.closeText}>Cancel</Text></Pressable>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  fill: { flex: 1 },
  center: { alignItems: "center", justifyContent: "center" },
  title: { fontSize: 22, fontWeight: "700", marginBottom: 10 },
  body: { fontSize: 14, lineHeight: 20, textAlign: "center", marginBottom: 24 },
  primary: { paddingVertical: 14, paddingHorizontal: 26, borderRadius: 14 },
  primaryText: { color: "#fff", fontWeight: "700" },
  cancel: { marginTop: 18, padding: 10 },
  overlay: { ...StyleSheet.absoluteFillObject, alignItems: "center", justifyContent: "center" },
  modeSwitch: { position: "absolute", top: 52, flexDirection: "row", borderRadius: 22, padding: 3, backgroundColor: "rgba(0,0,0,0.62)" },
  modeButton: { minWidth: 74, paddingHorizontal: 16, paddingVertical: 8, borderRadius: 19, alignItems: "center" },
  modeButtonActive: { backgroundColor: "#fff" },
  modeText: { color: "rgba(255,255,255,0.76)", fontSize: 13, fontWeight: "700" },
  modeTextActive: { color: "#111827" },
  hint: { position: "absolute", top: 112, left: 24, right: 24, color: "#fff", fontSize: 16, fontWeight: "700", textAlign: "center" },
  formatHint: { position: "absolute", top: 142, left: 24, right: 24, color: "rgba(255,255,255,0.78)", fontSize: 11, textAlign: "center" },
  reticle: { width: "88%", height: 116, borderWidth: 3, borderColor: "#fff", borderRadius: 20 },
  reticleQR: { width: 238, height: 238, borderRadius: 24 },
  privacy: { position: "absolute", bottom: 150, color: "rgba(255,255,255,0.82)", fontSize: 12 },
  capture: { position: "absolute", bottom: 70, width: 68, height: 68, borderRadius: 34, padding: 5, borderWidth: 3, borderColor: "#fff" },
  captureInner: { flex: 1, borderRadius: 29, backgroundColor: "#fff" },
  photo: { position: "absolute", bottom: 76, left: 22, paddingHorizontal: 16, paddingVertical: 11, borderRadius: 22, backgroundColor: "rgba(0,0,0,0.65)" },
  close: { position: "absolute", bottom: 76, right: 28, paddingHorizontal: 20, paddingVertical: 11, borderRadius: 22, backgroundColor: "rgba(0,0,0,0.65)" },
  closeText: { color: "#fff", fontWeight: "700" },
});
