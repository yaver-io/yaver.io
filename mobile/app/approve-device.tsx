// approve-device.tsx — one-tap approval of a remote box's `yaver auth`
// device code, from an already-signed-in phone.
//
// How the user gets here: a remote/off-LAN dev box (SSH, cloud, a
// laptop on another network) runs `yaver auth` and prints a QR + URL
// like https://yaver.io/auth/device?code=ABCD-1234. The user scans it
// with their phone camera; because the app claims applinks:yaver.io and
// pairLinkHandler routes /auth/device, the link opens HERE instead of a
// browser. The phone is already signed in, so approving is one tap — no
// browser, no re-auth, no code typed on the box.
//
// Falls back gracefully: with no/invalid code it shows a manual entry
// box so the user can type the ABCD-1234 the box printed. Success = the
// box's own `yaver auth` poller finishes within ~5s and it comes online.

import { router, useLocalSearchParams } from "expo-router";
import * as LocalAuthentication from "expo-local-authentication";
import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import DeviceCodeScanner from "../src/components/DeviceCodeScanner";
import {
  approveDeviceCode,
  extractUserCode,
  fetchDeviceCodeInfo,
  normalizeUserCode,
  type DeviceCodeInfo,
} from "../src/lib/deviceCodeApprove";

export default function ApproveDeviceScreen() {
  const c = useColors();
  const { token, user } = useAuth();
  const params = useLocalSearchParams<{ code?: string; url?: string }>();

  // Seed the code from either ?code= or a full ?url= (the deep-link
  // handler forwards the raw scanned URL).
  const initialCode = extractUserCode(
    (typeof params.url === "string" && params.url) ||
      (typeof params.code === "string" && params.code) ||
      "",
  );

  const [code, setCode] = useState(initialCode);
  const [info, setInfo] = useState<DeviceCodeInfo | null>(null);
  const [loadingInfo, setLoadingInfo] = useState(false);
  const [approving, setApproving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [scanning, setScanning] = useState(false);

  // A normalized code is 9 chars (ABCD-1234). Look up the waiting
  // machine so we can name it in the prompt.
  useEffect(() => {
    const c9 = normalizeUserCode(code);
    if (c9.length !== 9) {
      setInfo(null);
      return;
    }
    let cancelled = false;
    setLoadingInfo(true);
    fetchDeviceCodeInfo(c9)
      .then((res) => {
        if (!cancelled) setInfo(res);
      })
      .finally(() => {
        if (!cancelled) setLoadingInfo(false);
      });
    return () => {
      cancelled = true;
    };
  }, [code]);

  const onApprove = useCallback(async () => {
    if (approving) return;
    setApproving(true);
    setError(null);
    // Biometric gate: authorizing a remote machine is sensitive, so require a
    // fresh Face ID / Touch ID before it goes through — possession of an
    // already-unlocked phone shouldn't be enough. disableDeviceFallback keeps
    // it to biometrics (no passcode substitute). If the device has no
    // biometric hardware/enrollment we don't lock the user out: the signed-in
    // session token already proves account control.
    try {
      const [hasHw, enrolled] = await Promise.all([
        LocalAuthentication.hasHardwareAsync(),
        LocalAuthentication.isEnrolledAsync(),
      ]);
      if (hasHw && enrolled) {
        const r = await LocalAuthentication.authenticateAsync({
          promptMessage: `Approve sign-in for ${info?.machineName || "this machine"}`,
          disableDeviceFallback: true,
          cancelLabel: "Cancel",
        });
        if (!r.success) {
          setApproving(false);
          setError("Face ID / Touch ID is required to approve a sign-in.");
          return;
        }
      }
    } catch {
      // A biometric subsystem error shouldn't hard-block a valid session.
    }
    const res = await approveDeviceCode(code, token ?? "");
    setApproving(false);
    if (res.ok) setDone(true);
    else setError(res.error ?? "Couldn't authorize the machine.");
  }, [approving, code, token, info?.machineName]);

  const goHome = useCallback(() => {
    router.replace("/(tabs)/tasks");
  }, []);

  if (done) {
    return (
      <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
        <View style={styles.centered}>
          <View style={[styles.checkCircle, { backgroundColor: c.success + "22", borderColor: c.success }]}>
            <Text style={[styles.checkMark, { color: c.success }]}>✓</Text>
          </View>
          <Text style={[styles.title, { color: c.textPrimary }]}>Machine signed in</Text>
          <Text style={[styles.subtitle, { color: c.textSecondary }]}>
            {info?.machineName
              ? `${info.machineName} is authorized as ${user?.email ?? "your account"}. It'll come online in a few seconds.`
              : "The machine is authorized. It'll come online in a few seconds."}
          </Text>
          <Pressable
            style={({ pressed }) => [styles.primaryBtn, { backgroundColor: c.accent }, pressed && { opacity: 0.85 }]}
            onPress={goHome}
          >
            <Text style={[styles.primaryBtnText, { color: "#000" }]}>Done</Text>
          </Pressable>
        </View>
      </SafeAreaView>
    );
  }

  const codeReady = normalizeUserCode(code).length === 9;
  const machineLabel = info?.machineName || "this machine";

  // Full-screen camera scanner — decodes the box's QR and drops the
  // code back into the form (never auto-approves; the user still taps).
  if (scanning) {
    return (
      <DeviceCodeScanner
        onScanned={(scanned) => {
          setCode(scanned);
          setScanning(false);
          setError(null);
        }}
        onClose={() => setScanning(false)}
      />
    );
  }

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
      <View style={styles.content}>
        <Text style={[styles.title, { color: c.textPrimary }]}>Approve sign-in</Text>
        <Text style={[styles.subtitle, { color: c.textSecondary }]}>
          {codeReady
            ? `Sign ${machineLabel} into ${user?.email ?? "your account"}? It'll connect without a browser or password on the machine.`
            : "Scan the QR your machine printed, or type the code it shows after running yaver auth."}
        </Text>

        {loadingInfo ? (
          <View style={styles.infoRow}>
            <ActivityIndicator size="small" color={c.textMuted} />
            <Text style={[styles.infoText, { color: c.textMuted }]}>Looking up the machine…</Text>
          </View>
        ) : info ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardName, { color: c.textPrimary }]}>{info.machineName || "Unknown machine"}</Text>
            {(info.platform || info.arch) ? (
              <Text style={[styles.cardMeta, { color: c.textMuted }]}>
                {[info.platform, info.arch].filter(Boolean).join(" · ")}
              </Text>
            ) : null}
          </View>
        ) : null}

        {/* Manual code entry — used when arriving without a valid ?code= */}
        {!initialCode ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardLabel, { color: c.textMuted }]}>CODE FROM YOUR MACHINE</Text>
            <TextInput
              value={code}
              onChangeText={setCode}
              placeholder="ABCD-1234"
              placeholderTextColor={c.textMuted}
              autoCapitalize="characters"
              autoCorrect={false}
              style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
            />
            <Pressable
              onPress={() => setScanning(true)}
              style={({ pressed }) => [styles.scanBtn, { borderColor: c.accent }, pressed && { opacity: 0.75 }]}
            >
              <Text style={[styles.scanBtnText, { color: c.accent }]}>Scan QR instead</Text>
            </Pressable>
          </View>
        ) : null}

        {error ? <Text style={[styles.error, { color: c.warn }]}>{error}</Text> : null}

        <Pressable
          onPress={() => void onApprove()}
          disabled={!codeReady || approving || !token}
          style={({ pressed }) => [
            styles.primaryBtn,
            { backgroundColor: !codeReady || !token ? c.border : c.accent },
            pressed && { opacity: 0.85 },
          ]}
        >
          {approving ? (
            <ActivityIndicator size="small" color="#000" />
          ) : (
            <Text style={[styles.primaryBtnText, { color: !codeReady || !token ? c.textMuted : "#000" }]}>
              {!token ? "Sign in on this phone first" : `Approve ${machineLabel}`}
            </Text>
          )}
        </Pressable>

        <Pressable style={({ pressed }) => [styles.cancelBtn, pressed && { opacity: 0.6 }]} onPress={goHome}>
          <Text style={[styles.cancelText, { color: c.textSecondary }]}>Not now</Text>
        </Pressable>
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  content: { flex: 1, paddingHorizontal: 24, paddingTop: 48 },
  centered: { flex: 1, alignItems: "center", justifyContent: "center", paddingHorizontal: 32 },
  title: { fontSize: 24, fontWeight: "700", letterSpacing: -0.4, marginBottom: 10, textAlign: "center" },
  subtitle: { fontSize: 15, lineHeight: 21, marginBottom: 24, textAlign: "center" },
  card: { borderWidth: 1, borderRadius: 14, padding: 16, marginBottom: 16 },
  cardName: { fontSize: 16, fontWeight: "600" },
  cardMeta: { fontSize: 12, marginTop: 4 },
  cardLabel: { fontSize: 11, fontWeight: "700", letterSpacing: 1, marginBottom: 10 },
  input: { borderWidth: 1, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 12, fontSize: 16, fontFamily: "Courier" },
  scanBtn: { marginTop: 12, borderWidth: 1, borderRadius: 10, paddingVertical: 11, alignItems: "center" },
  scanBtnText: { fontSize: 14, fontWeight: "600" },
  infoRow: { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 10, paddingVertical: 14 },
  infoText: { fontSize: 13 },
  error: { fontSize: 13, marginBottom: 12, textAlign: "center" },
  primaryBtn: { paddingVertical: 15, borderRadius: 12, alignItems: "center", justifyContent: "center", marginTop: 8 },
  primaryBtnText: { fontSize: 16, fontWeight: "700" },
  cancelBtn: { alignItems: "center", paddingVertical: 14, marginTop: 8 },
  cancelText: { fontSize: 15, fontWeight: "600" },
  checkCircle: { width: 72, height: 72, borderRadius: 36, borderWidth: 2, alignItems: "center", justifyContent: "center", marginBottom: 24 },
  checkMark: { fontSize: 36, fontWeight: "700" },
});
