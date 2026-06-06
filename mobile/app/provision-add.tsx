// provision-add.tsx — "Add a Yaver device" via its label QR (zero-touch /
// DPP-style claiming). The buyer points the camera at the QR printed on a
// Yaver-powered box (Talos edge node, blackbox Pi, any third-party hardware)
// — even before powering it on — and becomes the owner. The box then
// self-credentials to this account on its next boot.
//
// Reachable at /provision-add (Expo Router file route) and as a deep link.
// Flow: scan -> confirm "Add <model>?" -> claim -> done. Pure claim logic
// lives in src/lib/provisionClaim.ts; the camera in src/components/
// ProvisionScanner.tsx.

import { router } from "expo-router";
import React, { useCallback, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, TextInput, View } from "react-native";

import ProvisionScanner from "../src/components/ProvisionScanner";
import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import {
  claimProvisionedDevice,
  type ClaimResult,
  type ProvisionClaim,
} from "../src/lib/provisionClaim";

type Phase = "scan" | "confirm" | "claiming" | "done" | "error";

export default function ProvisionAddScreen() {
  const c = useColors();
  const { token } = useAuth();
  const [phase, setPhase] = useState<Phase>("scan");
  const [claim, setClaim] = useState<ProvisionClaim | null>(null);
  const [name, setName] = useState("");
  const [result, setResult] = useState<ClaimResult | null>(null);

  const onScanned = useCallback((parsed: ProvisionClaim) => {
    setClaim(parsed);
    setName(parsed.model ?? "");
    setPhase("confirm");
  }, []);

  const doClaim = useCallback(async () => {
    if (!claim || !token) {
      setResult({ ok: false, deviceId: claim?.deviceId ?? "", error: "Not signed in" });
      setPhase("error");
      return;
    }
    setPhase("claiming");
    const r = await claimProvisionedDevice(token, claim, name);
    setResult(r);
    setPhase(r.ok ? "done" : "error");
  }, [claim, token, name]);

  const close = useCallback(() => router.back(), []);

  if (phase === "scan") {
    return <ProvisionScanner onScanned={onScanned} onClose={close} />;
  }

  return (
    <View style={[styles.fill, styles.pad, { backgroundColor: c.bg }]}>
      {phase === "confirm" && claim && (
        <View style={styles.center}>
          <Text style={[styles.title, { color: c.textPrimary }]}>
            Add {claim.model || "this device"}?
          </Text>
          <Text style={[styles.body, { color: c.textSecondary }]}>
            You'll become the owner of this device. It connects to your account automatically the
            next time it powers on — no setup on the device.
          </Text>
          <Text style={[styles.meta, { color: c.textMuted }]}>Device {claim.deviceId.slice(0, 8)}…</Text>
          <TextInput
            value={name}
            onChangeText={setName}
            placeholder="Name this device (optional)"
            placeholderTextColor={c.textMuted}
            style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
          />
          <Pressable
            onPress={doClaim}
            style={({ pressed }) => [styles.primaryBtn, { backgroundColor: c.accent }, pressed && { opacity: 0.85 }]}
          >
            <Text style={[styles.primaryBtnText, { color: "#000" }]}>Claim device</Text>
          </Pressable>
          <Pressable onPress={close} style={({ pressed }) => [styles.linkBtn, pressed && { opacity: 0.6 }]}>
            <Text style={[styles.linkText, { color: c.textSecondary }]}>Cancel</Text>
          </Pressable>
        </View>
      )}

      {phase === "claiming" && (
        <View style={styles.center}>
          <ActivityIndicator color={c.accent} />
          <Text style={[styles.body, { color: c.textSecondary, marginTop: 16 }]}>Claiming…</Text>
        </View>
      )}

      {phase === "done" && result && (
        <View style={styles.center}>
          <Text style={[styles.title, { color: c.textPrimary }]}>✓ You own this device</Text>
          <Text style={[styles.body, { color: c.textSecondary }]}>
            {result.alreadyActive
              ? "It's already online and now linked to your account."
              : "It will appear in your devices and come online automatically on its next boot."}
          </Text>
          <Pressable
            onPress={close}
            style={({ pressed }) => [styles.primaryBtn, { backgroundColor: c.accent }, pressed && { opacity: 0.85 }]}
          >
            <Text style={[styles.primaryBtnText, { color: "#000" }]}>Done</Text>
          </Pressable>
        </View>
      )}

      {phase === "error" && result && (
        <View style={styles.center}>
          <Text style={[styles.title, { color: c.textPrimary }]}>Couldn't add device</Text>
          <Text style={[styles.body, { color: c.textSecondary }]}>{result.error}</Text>
          <Pressable
            onPress={() => setPhase("scan")}
            style={({ pressed }) => [styles.primaryBtn, { backgroundColor: c.accent }, pressed && { opacity: 0.85 }]}
          >
            <Text style={[styles.primaryBtnText, { color: "#000" }]}>Scan again</Text>
          </Pressable>
          <Pressable onPress={close} style={({ pressed }) => [styles.linkBtn, pressed && { opacity: 0.6 }]}>
            <Text style={[styles.linkText, { color: c.textSecondary }]}>Cancel</Text>
          </Pressable>
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  fill: { flex: 1 },
  pad: { padding: 28 },
  center: { flex: 1, alignItems: "center", justifyContent: "center" },
  title: { fontSize: 22, fontWeight: "700", textAlign: "center", marginBottom: 12 },
  body: { fontSize: 15, lineHeight: 21, textAlign: "center", marginBottom: 16 },
  meta: { fontSize: 13, marginBottom: 20 },
  input: {
    width: "100%",
    borderWidth: 1,
    borderRadius: 12,
    paddingHorizontal: 14,
    paddingVertical: 12,
    fontSize: 16,
    marginBottom: 20,
  },
  primaryBtn: { paddingVertical: 14, paddingHorizontal: 32, borderRadius: 12, alignItems: "center", alignSelf: "stretch" },
  primaryBtnText: { fontSize: 16, fontWeight: "700" },
  linkBtn: { marginTop: 16, paddingVertical: 8 },
  linkText: { fontSize: 14, fontWeight: "600" },
});
