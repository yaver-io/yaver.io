// tv-signin.tsx — TV-friendly device-code sign-in. Typing email + password on a
// TV remote is miserable, so the TV shows a QR + a short code: the user scans it
// with the already-signed-in Yaver phone app (app/approve-device.tsx) or visits
// yaver.io/auth/device, approves with one tap, and the TV signs itself in.
//
// RFC 8628 device flow over the existing Convex contract (src/lib/tvSignIn.ts) —
// the same flow `yaver auth` uses on a headless box. On a TV build, app/index.tsx
// routes unauthenticated users here instead of /login.
import QRCode from "react-native-qrcode-svg";
import { router } from "expo-router";
import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, StyleSheet, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Platform } from "react-native";

import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import { createTVDeviceCode, pollTVDeviceCode, type DeviceCodeStart } from "../src/lib/tvSignIn";

const POLL_MS = 5000;

export default function TVSignInScreen() {
  const c = useColors();
  const { login, isAuthenticated } = useAuth();
  const [start, setStart] = useState<DeviceCodeStart | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<"pending" | "authorized" | "expired">("pending");
  const liveRef = useRef(true);

  const machineName = Platform.OS === "ios" ? "Apple TV" : "Google TV";

  const begin = useCallback(async () => {
    setError(null);
    setStatus("pending");
    try {
      const s = await createTVDeviceCode(machineName, Platform.OS === "ios" ? "tvos" : "androidtv");
      if (liveRef.current) setStart(s);
    } catch (e: any) {
      if (liveRef.current) setError(e?.message || "Couldn't start sign-in. Check your connection.");
    }
  }, [machineName]);

  // Kick off a code on mount.
  useEffect(() => {
    liveRef.current = true;
    begin();
    return () => {
      liveRef.current = false;
    };
  }, [begin]);

  // Poll until approved (or expired → refresh).
  useEffect(() => {
    if (!start) return;
    const id = setInterval(async () => {
      try {
        const r = await pollTVDeviceCode(start.deviceCode);
        if (!liveRef.current) return;
        if (r.status === "authorized" && r.token) {
          clearInterval(id);
          await login(r.token);
          router.replace("/(tabs)/tasks");
        } else if (r.status === "expired") {
          clearInterval(id);
          setStatus("expired");
          begin(); // fetch a fresh code automatically
        }
      } catch {
        /* transient — keep polling */
      }
    }, POLL_MS);
    return () => clearInterval(id);
  }, [start, login, begin]);

  useEffect(() => {
    if (isAuthenticated) router.replace("/(tabs)/tasks");
  }, [isAuthenticated]);

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
      <View style={styles.row}>
        <View style={styles.left}>
          <Text style={[styles.title, { color: c.textPrimary }]}>Sign in to Yaver</Text>
          <Text style={[styles.step, { color: c.textSecondary }]}>1. Open the Yaver app on your phone</Text>
          <Text style={[styles.step, { color: c.textSecondary }]}>2. Scan this code (or visit yaver.io/auth/device)</Text>
          <Text style={[styles.step, { color: c.textSecondary }]}>3. Tap Approve — this TV signs in instantly</Text>

          {start ? (
            <View style={styles.codeBox}>
              <Text style={[styles.codeLabel, { color: c.textMuted }]}>OR ENTER THIS CODE</Text>
              <Text style={[styles.code, { color: c.accent }]}>{start.userCode}</Text>
            </View>
          ) : null}

          {error ? <Text style={[styles.error, { color: c.warn }]}>{error}</Text> : null}
          {status === "expired" ? (
            <Text style={[styles.hint, { color: c.textMuted }]}>Code expired — generating a new one…</Text>
          ) : null}
        </View>

        <View style={[styles.qrPane, { backgroundColor: "#fff" }]}>
          {start ? (
            <QRCode value={start.verifyUrl} size={260} backgroundColor="#fff" color="#000" />
          ) : (
            <ActivityIndicator size="large" color={c.accent} />
          )}
        </View>
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  row: { flex: 1, flexDirection: "row", alignItems: "center", justifyContent: "center", padding: 48, gap: 56 },
  left: { maxWidth: 520, flexShrink: 1 },
  title: { fontSize: 38, fontWeight: "800", letterSpacing: -0.6, marginBottom: 24 },
  step: { fontSize: 20, lineHeight: 30, marginBottom: 6 },
  codeBox: { marginTop: 28 },
  codeLabel: { fontSize: 13, fontWeight: "700", letterSpacing: 2, marginBottom: 6 },
  code: { fontSize: 44, fontWeight: "800", letterSpacing: 4, fontFamily: Platform.OS === "ios" ? "Courier" : "monospace" },
  error: { fontSize: 16, marginTop: 20 },
  hint: { fontSize: 15, marginTop: 16 },
  qrPane: { padding: 20, borderRadius: 20, alignItems: "center", justifyContent: "center", width: 300, height: 300 },
});
