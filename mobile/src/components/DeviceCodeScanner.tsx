// DeviceCodeScanner — in-app camera QR scanner for a remote box's
// device-code URL.
//
// Today the iOS *system* camera already scans the box's
// https://yaver.io/auth/device?code=… QR and (via universal links)
// opens the in-app approver. This component is the in-app equivalent so
// the user never has to leave Yaver: open the approve screen → "Scan
// QR" → point at the terminal → we lift the ?code= and hand it back.
//
// Deliberately dumb: it only DECODES and returns a code string via
// onScanned. It never approves anything itself — the parent
// (approve-device.tsx) still shows "Approve sign-in on <machine>?" and
// requires an explicit tap. Mirrors pairLinkHandler's "detect, don't
// act" contract.

import React from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { CameraView, useCameraPermissions, type BarcodeScanningResult } from "expo-camera";

import { useColors } from "../context/ThemeContext";
import { extractUserCode } from "../lib/deviceCodeApprove";

interface Props {
  /** Fires once with the normalized ABCD-1234 code when a valid
   *  device-code QR is decoded. The scanner stops after the first hit
   *  so we don't spam the parent. */
  onScanned: (code: string) => void;
  /** Close the scanner without a result. */
  onClose: () => void;
}

export default function DeviceCodeScanner({ onScanned, onClose }: Props) {
  const c = useColors();
  const [permission, requestPermission] = useCameraPermissions();
  const handledRef = React.useRef(false);

  const onBarcode = React.useCallback(
    (result: BarcodeScanningResult) => {
      if (handledRef.current) return;
      const code = extractUserCode(result?.data ?? "");
      // A normalized device code is 9 chars (ABCD-1234). Ignore any
      // other QR the camera happens to catch.
      if (code.length !== 9) return;
      handledRef.current = true;
      onScanned(code);
    },
    [onScanned],
  );

  // Permission still loading.
  if (!permission) {
    return (
      <View style={[styles.fill, styles.center, { backgroundColor: c.bg }]}>
        <Text style={{ color: c.textMuted }}>Preparing camera…</Text>
      </View>
    );
  }

  // Permission not granted yet — ask, with a manual-entry escape.
  if (!permission.granted) {
    return (
      <View style={[styles.fill, styles.center, { backgroundColor: c.bg, padding: 32 }]}>
        <Text style={[styles.title, { color: c.textPrimary }]}>Scan the code on your machine</Text>
        <Text style={[styles.body, { color: c.textSecondary }]}>
          Allow camera access to scan the QR your machine printed after running yaver auth. You can
          also type the code instead.
        </Text>
        <Pressable
          onPress={() => void requestPermission()}
          style={({ pressed }) => [styles.primaryBtn, { backgroundColor: c.accent }, pressed && { opacity: 0.85 }]}
        >
          <Text style={[styles.primaryBtnText, { color: "#000" }]}>Allow camera</Text>
        </Pressable>
        <Pressable onPress={onClose} style={({ pressed }) => [styles.linkBtn, pressed && { opacity: 0.6 }]}>
          <Text style={[styles.linkText, { color: c.textSecondary }]}>Type the code instead</Text>
        </Pressable>
      </View>
    );
  }

  return (
    <View style={[styles.fill, { backgroundColor: "#000" }]}>
      <CameraView
        style={StyleSheet.absoluteFill}
        facing="back"
        barcodeScannerSettings={{ barcodeTypes: ["qr"] }}
        onBarcodeScanned={onBarcode}
      />
      {/* Framing hint + close. */}
      <View style={styles.overlay} pointerEvents="box-none">
        <Text style={styles.overlayText}>Point at the QR on your machine</Text>
        <View style={styles.reticle} />
        <Pressable
          onPress={onClose}
          style={({ pressed }) => [styles.closeBtn, pressed && { opacity: 0.7 }]}
        >
          <Text style={styles.closeText}>Cancel</Text>
        </Pressable>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  fill: { flex: 1 },
  center: { alignItems: "center", justifyContent: "center" },
  title: { fontSize: 20, fontWeight: "700", textAlign: "center", marginBottom: 10 },
  body: { fontSize: 14, lineHeight: 20, textAlign: "center", marginBottom: 24 },
  primaryBtn: { paddingVertical: 14, paddingHorizontal: 28, borderRadius: 12, alignItems: "center" },
  primaryBtnText: { fontSize: 16, fontWeight: "700" },
  linkBtn: { marginTop: 18, paddingVertical: 8 },
  linkText: { fontSize: 14, fontWeight: "600" },
  overlay: { ...StyleSheet.absoluteFillObject, alignItems: "center", justifyContent: "center" },
  overlayText: {
    position: "absolute",
    top: 80,
    color: "#fff",
    fontSize: 15,
    fontWeight: "600",
    textShadowColor: "rgba(0,0,0,0.6)",
    textShadowRadius: 6,
  },
  reticle: {
    width: 220,
    height: 220,
    borderWidth: 2,
    borderColor: "rgba(255,255,255,0.9)",
    borderRadius: 20,
  },
  closeBtn: {
    position: "absolute",
    bottom: 60,
    paddingHorizontal: 22,
    paddingVertical: 12,
    borderRadius: 24,
    backgroundColor: "rgba(0,0,0,0.55)",
  },
  closeText: { color: "#fff", fontSize: 15, fontWeight: "700" },
});
