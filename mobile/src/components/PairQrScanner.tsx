// PairQrScanner — camera-first entry for `yaver auth pair`.
//
// The scanner accepts only URLs understood by parsePairUrl. It never submits
// the phone's token: the parent first renders the decoded machine summary and
// requires an explicit Pair tap. This keeps scanning a locator separate from
// authorizing credential transfer.

import React from "react";
import { Linking, Pressable, StyleSheet, Text, View } from "react-native";
import { CameraView, useCameraPermissions, type BarcodeScanningResult } from "expo-camera";

import { useColors } from "../context/ThemeContext";
import { parsePairUrl } from "../lib/pairDevice";

interface Props {
  onScanned: (pairUrl: string) => void;
  onClose: () => void;
  accept?: (value: string) => boolean;
  title?: string;
  instruction?: string;
  invalidHint?: string;
  manualLabel?: string;
}

export default function PairQrScanner({
  onScanned,
  onClose,
  accept = (value) => !!parsePairUrl(value),
  title = "Scan the QR on your machine",
  instruction = "Run yaver auth pair on the machine, then allow camera access to scan the QR it prints.",
  invalidHint = "That isn't a Yaver pairing QR",
  manualLabel = "Enter a code or link instead",
}: Props) {
  const c = useColors();
  const [permission, requestPermission] = useCameraPermissions();
  const [scanHint, setScanHint] = React.useState("Point at the QR shown by yaver auth pair");
  const handledRef = React.useRef(false);

  const onBarcode = React.useCallback(
    (result: BarcodeScanningResult) => {
      if (handledRef.current) return;
      const raw = (result?.data ?? "").trim();
      if (!accept(raw)) {
        setScanHint(invalidHint);
        return;
      }
      handledRef.current = true;
      onScanned(raw);
    },
    [accept, invalidHint, onScanned],
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
        <Text style={[styles.title, { color: c.textPrimary }]}>{title}</Text>
        <Text style={[styles.body, { color: c.textSecondary }]}>{instruction}</Text>
        <Pressable
          onPress={() => void (permission.canAskAgain ? requestPermission() : Linking.openSettings())}
          style={({ pressed }) => [styles.primaryBtn, { backgroundColor: c.accent }, pressed && { opacity: 0.85 }]}
        >
          <Text style={styles.primaryBtnText}>{permission.canAskAgain ? "Allow camera" : "Open phone settings"}</Text>
        </Pressable>
        <Pressable onPress={onClose} style={({ pressed }) => [styles.linkBtn, pressed && { opacity: 0.6 }]}>
          <Text style={[styles.linkText, { color: c.textSecondary }]}>{manualLabel}</Text>
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
        <Text style={styles.overlayText}>{scanHint}</Text>
        <View style={styles.reticle} />
        <Pressable onPress={onClose} style={({ pressed }) => [styles.closeBtn, pressed && { opacity: 0.7 }]}>
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
  primaryBtnText: { color: "#fff", fontSize: 16, fontWeight: "700" },
  linkBtn: { marginTop: 18, paddingVertical: 8 },
  linkText: { fontSize: 14, fontWeight: "600" },
  overlay: { ...StyleSheet.absoluteFillObject, alignItems: "center", justifyContent: "center" },
  overlayText: {
    position: "absolute",
    top: 80,
    left: 24,
    right: 24,
    color: "#fff",
    fontSize: 15,
    fontWeight: "600",
    textAlign: "center",
    textShadowColor: "rgba(0,0,0,0.8)",
    textShadowRadius: 6,
  },
  reticle: { width: 236, height: 236, borderWidth: 3, borderColor: "#fff", borderRadius: 24 },
  closeBtn: {
    position: "absolute",
    bottom: 52,
    paddingHorizontal: 22,
    paddingVertical: 12,
    borderRadius: 24,
    backgroundColor: "rgba(0,0,0,0.6)",
  },
  closeText: { color: "#fff", fontSize: 15, fontWeight: "700" },
});
