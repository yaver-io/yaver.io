// ProvisionScanner — in-app camera QR scanner for a Yaver-powered box's
// provisioning label (a `yaver://provision/v1?...` QR). Mirrors
// DeviceCodeScanner's "detect, don't act" contract: it only DECODES and
// returns the parsed claim via onScanned; the parent screen (provision-add)
// shows the "Add <model>?" confirmation and performs the actual claim.

import React from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { CameraView, useCameraPermissions, type BarcodeScanningResult } from "expo-camera";

import { useColors } from "../context/ThemeContext";
import { parseProvisionQR, type ProvisionClaim } from "../lib/provisionClaim";

interface Props {
  /** Fires once with the parsed claim when a valid provision QR is decoded. */
  onScanned: (claim: ProvisionClaim) => void;
  /** Close the scanner without a result. */
  onClose: () => void;
}

export default function ProvisionScanner({ onScanned, onClose }: Props) {
  const c = useColors();
  const [permission, requestPermission] = useCameraPermissions();
  const handledRef = React.useRef(false);

  const onBarcode = React.useCallback(
    (result: BarcodeScanningResult) => {
      if (handledRef.current) return;
      const claim = parseProvisionQR(result?.data ?? "");
      if (!claim) return; // ignore any non-provision QR the camera catches
      handledRef.current = true;
      onScanned(claim);
    },
    [onScanned],
  );

  if (!permission) {
    return (
      <View style={[styles.fill, styles.center, { backgroundColor: c.bg }]}>
        <Text style={{ color: c.textMuted }}>Preparing camera…</Text>
      </View>
    );
  }

  if (!permission.granted) {
    return (
      <View style={[styles.fill, styles.center, { backgroundColor: c.bg, padding: 32 }]}>
        <Text style={[styles.title, { color: c.textPrimary }]}>Scan your device's QR</Text>
        <Text style={[styles.body, { color: c.textSecondary }]}>
          Allow camera access to scan the QR on your Yaver device's label and take ownership.
        </Text>
        <Pressable
          onPress={() => void requestPermission()}
          style={({ pressed }) => [styles.primaryBtn, { backgroundColor: c.accent }, pressed && { opacity: 0.85 }]}
        >
          <Text style={[styles.primaryBtnText, { color: "#000" }]}>Allow camera</Text>
        </Pressable>
        <Pressable onPress={onClose} style={({ pressed }) => [styles.linkBtn, pressed && { opacity: 0.6 }]}>
          <Text style={[styles.linkText, { color: c.textSecondary }]}>Cancel</Text>
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
      <View style={styles.overlay} pointerEvents="box-none">
        <Text style={styles.overlayText}>Point at the QR on your device's label</Text>
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
