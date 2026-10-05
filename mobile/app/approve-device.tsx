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
import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import {
  clearPendingDeviceCode,
  stashPendingDeviceCode,
} from "../src/lib/pendingDeviceApproval";
import DeviceCodeScanner from "../src/components/DeviceCodeScanner";
import SegmentedCodeInput from "../src/components/SegmentedCodeInput";
import {
  approveDeviceCode,
  extractUserCode,
  fetchDeviceCodeInfoResult,
  normalizeUserCode,
  type DeviceCodeInfo,
} from "../src/lib/deviceCodeApprove";

export default function ApproveDeviceScreen() {
  const c = useColors();
  const { token, user, isLoading: authLoading } = useAuth();
  const params = useLocalSearchParams<{ code?: string; url?: string; scan?: string }>();

  // Seed the code from either ?code= or a full ?url= (the deep-link
  // handler forwards the raw scanned URL).
  const initialCode = extractUserCode(
    (typeof params.url === "string" && params.url) ||
      (typeof params.code === "string" && params.code) ||
      "",
  );

  const [code, setCode] = useState(initialCode);
  const routedCodeRef = useRef(initialCode);

  // A second universal link can arrive while this route is already mounted.
  // Route params are not initial state: adopt the new code and discard every
  // verdict belonging to the previous TV before approval can be pressed.
  useEffect(() => {
    if (!initialCode || initialCode === routedCodeRef.current) return;
    routedCodeRef.current = initialCode;
    setCode(initialCode);
    setInfo(null);
    setUnknownCode(false);
    setLookupError(null);
    setError(null);
    setDone(false);
    setClaimedByDevice(false);
  }, [initialCode]);

  // Signed out but arrived with a code (scanned an Apple TV QR while logged
  // out)? Don't sit here and fail — approveDeviceCode would run with an empty
  // token and the TV would stay stuck "Waiting for approval". Stash the code and
  // send them to sign in; every sign-in path returns here with the code
  // afterward (resumePendingDeviceApproval + PendingDeviceApprovalHost) and the
  // approval completes.
  //
  // `authLoading` is the guard that matters as much as the stash: SecureStore
  // hydration takes a tick, so a SIGNED-IN phone opening this screen from a cold
  // universal link reads `user === null` on the first render. Acting then would
  // eject a signed-in user to /login for no reason.
  useEffect(() => {
    if (authLoading || user) return;
    const c9 = normalizeUserCode(code);
    if (c9.length !== 9) return;
    let cancelled = false;
    (async () => {
      await stashPendingDeviceCode(c9);
      if (!cancelled) router.replace("/login");
    })();
    return () => { cancelled = true; };
  }, [authLoading, user, code]);

  const [info, setInfo] = useState<DeviceCodeInfo | null>(null);
  const [loadingInfo, setLoadingInfo] = useState(false);
  // The lookup came back empty for a well-formed code: the code expired (15-min
  // server TTL) or was mistyped. Say so, instead of leaving an enabled Approve
  // button that fails with a backend string.
  const [unknownCode, setUnknownCode] = useState(false);
  const [lookupError, setLookupError] = useState<string | null>(null);
  const [lookupNonce, setLookupNonce] = useState(0);
  const [approving, setApproving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [claimedByDevice, setClaimedByDevice] = useState(false);
  const [scanning, setScanning] = useState(params.scan === "1" && !initialCode);

  useEffect(() => {
    if (params.scan === "1" && !initialCode) setScanning(true);
  }, [params.scan, initialCode]);

  // A normalized code is 9 chars (ABCD-1234). Look up the waiting
  // machine so we can name it in the prompt.
  useEffect(() => {
    const c9 = normalizeUserCode(code);
    if (c9.length !== 9) {
      setInfo(null);
      setUnknownCode(false);
      setLookupError(null);
      setLoadingInfo(false);
      return;
    }
    let cancelled = false;
    setLoadingInfo(true);
    setUnknownCode(false);
    setLookupError(null);
    fetchDeviceCodeInfoResult(c9)
      .then((res) => {
        if (cancelled) return;
        if (res.kind === "found") {
          setInfo(res.info);
          setUnknownCode(false);
        } else if (res.kind === "not_found") {
          setInfo(null);
          setUnknownCode(true);
        } else {
          setInfo(null);
          setUnknownCode(false);
          setLookupError(res.message);
        }
      })
      .finally(() => {
        if (!cancelled) setLoadingInfo(false);
      });
    return () => {
      cancelled = true;
    };
  }, [code, lookupNonce]);

  const onApprove = useCallback(async () => {
    if (approving) return;
    setApproving(true);
    setError(null);
    // Biometric gate: authorizing a remote machine is sensitive, so require a
    // fresh Face ID / Touch ID or the phone passcode before it goes through —
    // possession of an already-unlocked phone shouldn't be enough. If there is no
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
          disableDeviceFallback: false,
          fallbackLabel: "Use passcode",
          cancelLabel: "Cancel",
        });
        if (!r.success) {
          setApproving(false);
          setError("Device authentication is required to approve a sign-in.");
          return;
        }
      }
    } catch {
      // An authentication subsystem failure is not proof of user presence.
      setApproving(false);
      setError("Couldn't verify this approval with your device. Try again.");
      return;
    }
    const res = await approveDeviceCode(code, token ?? "");
    if (res.ok) {
      const c9 = normalizeUserCode(code);
      let claimed = false;
      const confirmDeadline = Date.now() + 12_000;
      while (Date.now() < confirmDeadline) {
        const remaining = confirmDeadline - Date.now();
        const latest = await fetchDeviceCodeInfoResult(c9, Math.min(2_500, remaining));
        if (latest.kind === "found") setInfo(latest.info);
        if (latest.kind === "found" && latest.info.claimed) {
          claimed = true;
          break;
        }
        const sleepMs = Math.min(1_000, Math.max(0, confirmDeadline - Date.now()));
        if (sleepMs > 0) await new Promise((resolve) => setTimeout(resolve, sleepMs));
      }
      // Only now is the stash spent. Clearing it earlier (the old finishLogin
      // did, before navigating) meant a navigation lost to another redirect
      // destroyed the code with it — unrecoverable, TV stuck forever.
      await clearPendingDeviceCode();
      setClaimedByDevice(claimed);
      setApproving(false);
      setDone(true);
    } else {
      setApproving(false);
      setError(res.error ?? "Couldn't authorize the machine.");
    }
  }, [approving, code, token, info?.machineName]);

  // "Not now" is an explicit decision — drop the stash so the approver never
  // re-opens itself on the next route change.
  const goHome = useCallback(() => {
    void clearPendingDeviceCode();
    router.replace("/(tabs)/ssh");
  }, []);

  if (done) {
    return (
      <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
        <View style={styles.centered}>
          <View style={[styles.checkCircle, { backgroundColor: c.success + "22", borderColor: c.success }]}>
            <Text style={[styles.checkMark, { color: c.success }]}>✓</Text>
          </View>
          <Text style={[styles.title, { color: c.textPrimary }]}>
            {claimedByDevice ? "Machine signed in" : "Sign-in approved"}
          </Text>
          <Text style={[styles.subtitle, { color: c.textSecondary }]}>
            {claimedByDevice && info?.machineName
              ? `${info.machineName} is authorized as ${user?.email ?? "your account"}. It'll come online in a few seconds.`
              : claimedByDevice
                ? "The machine is authorized. It'll come online in a few seconds."
                : `${info?.machineName || "The device"} is approved. Keep its sign-in screen open while it picks up the session.`}
          </Text>
          <Pressable
            style={({ pressed }) => [
              styles.primaryBtn,
              styles.successBtn,
              { backgroundColor: c.accent },
              pressed && { opacity: 0.85 },
            ]}
            onPress={goHome}
          >
            <Text style={[styles.primaryBtnText, { color: "#000" }]}>Done</Text>
          </Pressable>
        </View>
      </SafeAreaView>
    );
  }

  const codeReady = normalizeUserCode(code).length === 9;
  const canApprove = codeReady && !!token && !!info && info.status === "pending"
    && !loadingInfo && !unknownCode && !lookupError;
  const machineLabel = info?.machineName || "this machine";

  // Full-screen camera scanner — decodes the box's QR and drops the
  // code back into the form (never auto-approves; the user still taps).
  if (scanning) {
    return (
      <DeviceCodeScanner
        onScanned={(scanned) => {
          setCode(scanned);
          setInfo(null);
          setUnknownCode(false);
          setLookupError(null);
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
            ? `Sign ${machineLabel} into ${user?.email ?? "your account"}? Confirm this code also appears on the TV.`
            : "Scan the QR your machine printed, or type the code it shows after running yaver auth."}
        </Text>

        {loadingInfo ? (
          <View style={styles.infoRow}>
            <ActivityIndicator size="small" color={c.textMuted} />
            <Text style={[styles.infoText, { color: c.textMuted }]}>Looking up the machine…</Text>
          </View>
        ) : unknownCode ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.warn }]}>
            <Text style={[styles.cardName, { color: c.warn }]}>That code is no longer valid</Text>
            <Text style={[styles.cardMeta, { color: c.textSecondary }]}>
              Sign-in codes last 15 minutes. Your TV has already replaced it — open Yaver on the TV
              and scan the new code (or type it below).
            </Text>
          </View>
        ) : info ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: info.status === "pending" ? c.border : c.warn }]}>
            <Text style={[styles.cardName, { color: c.textPrimary }]}>{info.machineName || "Unknown machine"}</Text>
            {(info.platform || info.arch) ? (
              <Text style={[styles.cardMeta, { color: c.textMuted }]}>
                {[info.platform, info.arch].filter(Boolean).join(" · ")}
              </Text>
            ) : null}
            <Text style={[styles.confirmCode, { color: c.accent }]}>{normalizeUserCode(code)}</Text>
            <Text style={[styles.cardMeta, { color: info.status === "pending" ? c.textSecondary : c.warn }]}>
              {info.status === "pending"
                ? "Approve only if this code matches the TV."
                : "This code was already used or expired. Scan the current QR shown by the TV."}
            </Text>
          </View>
        ) : null}

        {lookupError ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.warn }]}>
            <Text style={[styles.cardName, { color: c.warn }]}>Couldn't verify the TV</Text>
            <Text style={[styles.cardMeta, { color: c.textSecondary }]}>{lookupError}</Text>
            <Pressable onPress={() => setLookupNonce((n) => n + 1)} style={styles.retryLookupBtn}>
              <Text style={{ color: c.accent, fontWeight: "700" }}>Try again</Text>
            </Pressable>
          </View>
        ) : null}

        {/* Manual code entry — shown when we arrived without a valid ?code=, and
            also when the code we arrived with is dead (so the user has somewhere
            to put the TV's new one instead of a dead end). */}
        {!initialCode || unknownCode ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardLabel, { color: c.textMuted }]}>CODE FROM YOUR MACHINE</Text>
            <SegmentedCodeInput
              testID="device-approval-segmented-code"
              value={code}
              onChangeText={(next) => {
                // A verdict belongs to one exact code. Clear it in the same
                // input event so the old TV cannot keep Approve enabled for a
                // newly typed 9-character code before its lookup begins.
                setCode(next);
                setInfo(null);
                setUnknownCode(false);
                setLookupError(null);
              }}
              length={8}
              groupEvery={4}
              accessibilityLabel="Eight-character device approval code"
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
          disabled={!canApprove || approving}
          style={({ pressed }) => [
            styles.primaryBtn,
            { backgroundColor: canApprove ? c.accent : c.border },
            pressed && { opacity: 0.85 },
          ]}
        >
          {approving ? (
            <ActivityIndicator size="small" color="#000" />
          ) : (
            <Text style={[styles.primaryBtnText, { color: canApprove ? "#000" : c.textMuted }]}>
              {!token
                ? "Sign in on this phone first"
                : unknownCode
                  ? "Enter the TV's current code"
                  : info && info.status !== "pending"
                    ? "Scan the TV's current QR"
                  : `Approve ${machineLabel}`}
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
  confirmCode: { fontSize: 28, fontWeight: "800", letterSpacing: 3, marginTop: 12, fontFamily: "Courier" },
  retryLookupBtn: { alignSelf: "flex-start", paddingTop: 12, paddingVertical: 6 },
  cardLabel: { fontSize: 11, fontWeight: "700", letterSpacing: 1, marginBottom: 10 },
  scanBtn: { marginTop: 12, borderWidth: 1, borderRadius: 10, paddingVertical: 11, alignItems: "center" },
  scanBtnText: { fontSize: 14, fontWeight: "600" },
  infoRow: { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 10, paddingVertical: 14 },
  infoText: { fontSize: 13 },
  error: { fontSize: 13, marginBottom: 12, textAlign: "center" },
  primaryBtn: { paddingVertical: 15, borderRadius: 12, alignItems: "center", justifyContent: "center", marginTop: 8 },
  // `centered` uses alignItems:center, so the shared button otherwise shrinks
  // to the four-letter label and renders as a tall, narrow pill on iPhone.
  successBtn: { width: "100%", maxWidth: 320, minHeight: 52, paddingHorizontal: 32 },
  primaryBtnText: { fontSize: 16, fontWeight: "700" },
  cancelBtn: { alignItems: "center", paddingVertical: 14, marginTop: 8 },
  cancelText: { fontSize: 15, fontWeight: "600" },
  checkCircle: { width: 72, height: 72, borderRadius: 36, borderWidth: 2, alignItems: "center", justifyContent: "center", marginBottom: 24 },
  checkMark: { fontSize: 36, fontWeight: "700" },
});
