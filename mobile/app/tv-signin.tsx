// tv-signin.tsx — exactly two TV account-login choices:
//   1. email + password on the TV;
//   2. a QR approved by an already-authenticated Yaver phone/remote device.
// OAuth providers stay on that approving device; the TV never grows a provider
// grid or embeds a browser.
//
// RFC 8628 device flow over the existing Convex contract (src/lib/tvSignIn.ts) —
// the same flow `yaver auth` uses on a headless box. On a TV build, app/index.tsx
// routes unauthenticated users here instead of /login.
import QRCode from "react-native-qrcode-svg";
import { router } from "expo-router";
import React, { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Platform, Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { YaverAppIcon } from "../src/components/YaverAppIcon";

import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import { loginWithEmail } from "../src/lib/auth";
import { decideTVDeviceCodeDelivery } from "../src/lib/tvDeviceCodeDelivery";
import {
  claimTVDeviceCode,
  createTVDeviceCode,
  pollTVDeviceCode,
  waitTVDeviceCodeEvent,
  type DeviceCodeStart,
  type PollResult,
} from "../src/lib/tvSignIn";

const POLL_MS = 5000;

export default function TVSignInScreen() {
  const c = useColors();
  const { login, isAuthenticated } = useAuth();
  const [start, setStart] = useState<DeviceCodeStart | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [unreachable, setUnreachable] = useState<string | null>(null);
  const [status, setStatus] = useState<"pending" | "authorized" | "expired">("pending");
  const [now, setNow] = useState(Date.now());
  const [mode, setMode] = useState<"email" | "qr">("email");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [emailBusy, setEmailBusy] = useState(false);
  const completedRef = useRef(false);
  const claimInFlightRef = useRef(false);
  const liveRef = useRef(true);

  const machineName = Platform.OS === "ios" ? "Apple TV" : "Google TV";

  const begin = useCallback(async () => {
    setError(null);
    setStatus("pending");
    try {
      const s = await createTVDeviceCode(machineName, Platform.OS === "ios" ? "tvos" : "androidtv");
      if (liveRef.current) {
        completedRef.current = false;
        claimInFlightRef.current = false;
        setStart(s);
        setUnreachable(null);
      }
    } catch (e: any) {
      if (liveRef.current) {
        completedRef.current = false;
        setError(e?.message || "Couldn't start sign-in. Check your connection.");
      }
    }
  }, [machineName]);

  // Mint a code only after the user chooses option two. Email-first TVs should
  // not create anonymous backend rows they never intend to use.
  useEffect(() => {
    liveRef.current = true;
    if (mode === "qr") {
      void begin();
    } else {
      // Leaving QR mode must stop both delivery lanes. A hidden code should
      // never claim a session while the user is typing email credentials.
      setStart(null);
      setStatus("pending");
      setUnreachable(null);
      completedRef.current = false;
      claimInFlightRef.current = false;
    }
    return () => {
      liveRef.current = false;
    };
  }, [begin, mode]);

  const finishIfAuthorized = useCallback(async (r: PollResult, deviceCode: string) => {
    if (!liveRef.current || completedRef.current) return;
    const decision = decideTVDeviceCodeDelivery(r, claimInFlightRef.current);
    if (r.status === "authorized") {
      setStatus("authorized");
      if (decision === "sign_in" && r.token) {
        completedRef.current = true;
        try {
          await login(r.token);
          router.replace("/tv-home");
        } catch (e: any) {
          completedRef.current = false;
          setError(e?.message || "The TV received its session but couldn't save it. Retrying...");
        }
        return;
      }
      if (decision !== "claim") return;
      claimInFlightRef.current = true;
      try {
        const claimed = await claimTVDeviceCode(deviceCode, r.claimHandle);
        if (completedRef.current) return;
        if (claimed.status === "authorized" && claimed.token) {
          completedRef.current = true;
          try {
            await login(claimed.token);
            router.replace("/tv-home");
          } catch (e: any) {
            completedRef.current = false;
            setError(e?.message || "The TV received its session but couldn't save it. Retrying...");
          }
          return;
        }
        setUnreachable("Approved, but this TV could not pick up the session yet. Retrying...");
      } finally {
        claimInFlightRef.current = false;
      }
      return;
    }
    if (decision === "rotate") {
      completedRef.current = true;
      setStatus("expired");
      await begin();
    }
  }, [begin, login]);

  // Event-listen first: the backend holds this request until the code changes,
  // so approval can land immediately. Polling below is still the fallback.
  useEffect(() => {
    if (!start) return;
    let cancelled = false;
    (async () => {
      while (!cancelled && liveRef.current && !completedRef.current) {
        try {
          const r = await waitTVDeviceCodeEvent(start.deviceCode);
          if (cancelled) return;
          setUnreachable(null);
          await finishIfAuthorized(r, start.deviceCode);
          if (r.status === "authorized" || r.status === "expired") return;
        } catch (e: any) {
          if (!cancelled) setUnreachable(e?.message || "Can't reach Yaver. Retrying...");
        }
      }
    })();
    return () => { cancelled = true; };
  }, [start, finishIfAuthorized]);

  // Poll until approved (or expired -> refresh). This is a backup for platforms
  // that suspend the long request or lose a network transition.
  useEffect(() => {
    if (!start) return;
    const id = setInterval(async () => {
      try {
        const r = await pollTVDeviceCode(start.deviceCode);
        if (!liveRef.current) return;
        setUnreachable(null);
        await finishIfAuthorized(r, start.deviceCode);
        if (r.status === "authorized" || r.status === "expired") {
          clearInterval(id);
        }
      } catch (e: any) {
        setUnreachable(e?.message || "Can't reach Yaver. Retrying...");
      }
    }, POLL_MS);
    return () => clearInterval(id);
  }, [start, finishIfAuthorized]);

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    if (isAuthenticated) router.replace("/tv-home");
  }, [isAuthenticated]);

  const signInWithEmail = useCallback(async () => {
    if (emailBusy || !email.trim() || !password) return;
    setEmailBusy(true);
    setError(null);
    try {
      const result = await loginWithEmail(email.trim(), password);
      if (result.kind === "2fa") {
        setError("Two-factor authentication is enabled. Choose QR login and approve from your signed-in phone.");
        return;
      }
      completedRef.current = true;
      await login(result.token);
      router.replace("/tv-home");
    } catch (e: any) {
      setError(e?.message || "Email sign-in failed.");
    } finally {
      setEmailBusy(false);
    }
  }, [email, emailBusy, login, password]);

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
      <YaverAppIcon size={76} style={styles.brandMark} />
      <View style={styles.modeRow}>
        <Pressable
          focusable
          hasTVPreferredFocus
          onPress={() => setMode("email")}
          style={({ focused }) => [styles.modeButton, mode === "email" && { borderColor: c.accent }, focused && styles.focused]}
        >
          <Text style={[styles.modeText, { color: c.textPrimary }]}>1 · Email & password</Text>
        </Pressable>
        <Pressable
          focusable
          onPress={() => setMode("qr")}
          style={({ focused }) => [styles.modeButton, mode === "qr" && { borderColor: c.accent }, focused && styles.focused]}
        >
          <Text style={[styles.modeText, { color: c.textPrimary }]}>2 · Scan QR from phone</Text>
        </Pressable>
      </View>

      {mode === "email" ? (
        <View style={styles.emailPane}>
          <Text style={[styles.title, { color: c.textPrimary }]}>Sign in with email</Text>
          <TextInput
            value={email}
            onChangeText={setEmail}
            placeholder="Email"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="email-address"
            style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgCard }]}
          />
          <TextInput
            value={password}
            onChangeText={setPassword}
            placeholder="Password"
            placeholderTextColor={c.textMuted}
            secureTextEntry
            style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgCard }]}
            onSubmitEditing={() => void signInWithEmail()}
          />
          <Pressable
            focusable
            disabled={emailBusy || !email.trim() || !password}
            onPress={() => void signInWithEmail()}
            style={({ focused }) => [styles.primaryButton, { backgroundColor: c.accent }, focused && styles.focused]}
          >
            {emailBusy ? <ActivityIndicator color="#000" /> : <Text style={styles.primaryButtonText}>Sign in</Text>}
          </Pressable>
          {error ? <Text style={[styles.error, { color: c.warn }]}>{error}</Text> : null}
        </View>
      ) : (
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
          {start ? (
            <Text style={[styles.hint, { color: unreachable ? c.warn : c.textMuted }]}>
              {unreachable || `Waiting for approval · ${formatClock(Math.max(0, now - (start.expiresAt - 15 * 60 * 1000)) / 1000)} elapsed · code expires in ${formatClock(Math.max(0, start.expiresAt - now) / 1000)}`}
            </Text>
          ) : null}
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
      )}
    </SafeAreaView>
  );
}

function formatClock(seconds: number): string {
  const total = Math.max(0, Math.round(seconds));
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  brandMark: { alignSelf: "center", marginTop: 28 },
  modeRow: { flexDirection: "row", justifyContent: "center", gap: 18, paddingTop: 34 },
  modeButton: { borderWidth: 2, borderColor: "transparent", borderRadius: 14, paddingHorizontal: 28, paddingVertical: 16 },
  modeText: { fontSize: 20, fontWeight: "700" },
  emailPane: { flex: 1, width: 620, alignSelf: "center", justifyContent: "center", gap: 18 },
  input: { borderWidth: 2, borderRadius: 14, paddingHorizontal: 20, paddingVertical: 16, fontSize: 22 },
  primaryButton: { borderRadius: 14, minHeight: 58, alignItems: "center", justifyContent: "center" },
  primaryButtonText: { color: "#000", fontSize: 21, fontWeight: "800" },
  focused: { transform: [{ scale: 1.04 }], opacity: 0.9 },
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
