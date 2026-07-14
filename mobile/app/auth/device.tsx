/**
 * Device-code approver — the in-app "Approve this Apple TV?" screen.
 *
 * The Apple TV / Vision Pro / headless CLI sign-in shows a QR encoding
 * `https://yaver.io/auth/device?code=XXXX-YYYY`. On iOS that is a universal
 * link (apple-app-site-association maps `/auth/device*` to this app), so
 * scanning it with the phone opens Yaver here — NOT the web page.
 *
 * This route is what the association file has always promised ("opens the
 * in-app one-tap approver") and what was missing: with no `app/auth/device`
 * route, expo-router received the link, found nothing, and fell back to the
 * Tasks tab. The code was dropped and the TV sat forever on "waiting". This
 * file closes that gap.
 *
 * Flow:
 *   1. Read `?code=` (the userCode on the TV screen).
 *   2. GET /auth/device-code/info — show WHAT is being approved (machine name,
 *      platform) so the user isn't approving a blind code.
 *   3. On Approve: POST /auth/device-code/authorize with the phone's session
 *      token + userCode. The waiting device polls every 5s and signs in.
 *
 * Auth model: approval is a human-consent action taken by an already-signed-in
 * phone. The phone proves identity with its own bearer token; the TV never
 * sees a secret. Same posture as the web sign-in page, one tap instead of a
 * typed code.
 */

import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { SafeAreaView } from "react-native-safe-area-context";
import { AppBackButton } from "../../src/components/AppBackButton";
import { useColors } from "../../src/context/ThemeContext";
import { getToken, getConvexSiteUrl } from "../../src/lib/auth";

type Phase = "loading" | "ready" | "approving" | "success" | "error";

interface DeviceCodeInfo {
  userCode: string;
  status: string; // "pending" | "authorized" | "expired"
  machineName: string | null;
  platform: string | null;
  arch: string | null;
}

// The userCode is shown as "KKRL-2725" but the backend stores/looks up the
// canonical uppercased, trimmed form. Normalize before every call so a link
// that arrives lower-cased (some scanners do this) still matches.
function normalizeCode(raw: string | undefined): string {
  return (raw ?? "").toUpperCase().trim();
}

export default function DeviceApproveScreen() {
  const c = useColors();
  const router = useRouter();
  const params = useLocalSearchParams<{ code?: string }>();
  const code = normalizeCode(typeof params.code === "string" ? params.code : undefined);

  const [phase, setPhase] = useState<Phase>("loading");
  const [info, setInfo] = useState<DeviceCodeInfo | null>(null);
  const [error, setError] = useState<string>("");

  const loadInfo = useCallback(async () => {
    if (!code) {
      setPhase("error");
      setError("No device code in the link. Reopen the QR on your Apple TV and scan again.");
      return;
    }
    setPhase("loading");
    setError("");
    try {
      const res = await fetch(
        `${getConvexSiteUrl()}/auth/device-code/info?user_code=${encodeURIComponent(code)}`,
      );
      if (res.status === 404) {
        setPhase("error");
        setError(`Code ${code} was not found — it may have expired. Reopen sign-in on the Apple TV for a fresh code.`);
        return;
      }
      if (!res.ok) throw new Error(`info request failed (${res.status})`);
      const data = (await res.json()) as DeviceCodeInfo;
      if (data.status === "authorized") {
        setPhase("success"); // already approved (e.g. re-scan) — don't error
        setInfo(data);
        return;
      }
      if (data.status === "expired") {
        setPhase("error");
        setError("This code has expired. Reopen sign-in on the Apple TV for a fresh code.");
        return;
      }
      setInfo(data);
      setPhase("ready");
    } catch (e) {
      setPhase("error");
      setError(e instanceof Error ? e.message : "Couldn't reach Yaver to check the code.");
    }
  }, [code]);

  useEffect(() => {
    void loadInfo();
  }, [loadInfo]);

  const approve = useCallback(async () => {
    setPhase("approving");
    setError("");
    try {
      const token = await getToken();
      if (!token) {
        setPhase("error");
        setError("You're not signed in on this phone. Sign in first, then scan the code again.");
        return;
      }
      const res = await fetch(`${getConvexSiteUrl()}/auth/device-code/authorize`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ userCode: code }),
      });
      if (res.status === 401 || res.status === 403) {
        setPhase("error");
        setError("Your phone session isn't valid anymore. Sign in again, then re-scan.");
        return;
      }
      if (res.status === 410) {
        setPhase("error");
        setError("The code expired before you approved it. Reopen sign-in on the Apple TV.");
        return;
      }
      if (res.status === 409) {
        setPhase("success"); // already used — treat as done, not a failure
        return;
      }
      if (!res.ok) throw new Error(`approve failed (${res.status})`);
      setPhase("success");
    } catch (e) {
      setPhase("error");
      setError(e instanceof Error ? e.message : "Approval failed. Try again.");
    }
  }, [code]);

  const machineLabel = info?.machineName || "a new device";
  const platformLabel = [info?.platform, info?.arch].filter(Boolean).join(" · ");

  return (
    <SafeAreaView style={[styles.container, { backgroundColor: c.bg }]}>
      <AppBackButton onPress={() => router.back()} />
      <View style={styles.body}>
        {phase === "loading" && (
          <>
            <ActivityIndicator size="large" color={c.accent} />
            <Text style={[styles.sub, { color: c.textMuted }]}>Checking code {code}…</Text>
          </>
        )}

        {(phase === "ready" || phase === "approving") && (
          <>
            <Ionicons name="tv-outline" size={64} color={c.accent} />
            <Text style={[styles.title, { color: c.textPrimary }]}>Sign in {machineLabel}?</Text>
            {platformLabel ? (
              <Text style={[styles.sub, { color: c.textMuted }]}>{platformLabel}</Text>
            ) : null}
            <Text style={[styles.sub, { color: c.textMuted }]}>
              Approving lets this device use your Yaver account. Only approve a code you can see on your
              own screen.
            </Text>
            <Pressable
              accessibilityRole="button"
              onPress={approve}
              disabled={phase === "approving"}
              style={[styles.primaryBtn, { backgroundColor: c.accent, opacity: phase === "approving" ? 0.6 : 1 }]}
            >
              {phase === "approving" ? (
                <ActivityIndicator color={c.bg} />
              ) : (
                <Text style={[styles.primaryBtnText, { color: c.bg }]}>Approve</Text>
              )}
            </Pressable>
          </>
        )}

        {phase === "success" && (
          <>
            <Ionicons name="checkmark-circle" size={72} color="#34C759" />
            <Text style={[styles.title, { color: c.textPrimary }]}>Approved</Text>
            <Text style={[styles.sub, { color: c.textMuted }]}>
              {machineLabel} will sign in within a few seconds. You can close this.
            </Text>
            <Pressable
              accessibilityRole="button"
              onPress={() => router.back()}
              style={[styles.primaryBtn, { backgroundColor: c.accent }]}
            >
              <Text style={[styles.primaryBtnText, { color: c.bg }]}>Done</Text>
            </Pressable>
          </>
        )}

        {phase === "error" && (
          <>
            <Ionicons name="alert-circle-outline" size={64} color="#FF9F0A" />
            <Text style={[styles.title, { color: c.textPrimary }]}>Couldn't approve</Text>
            <Text style={[styles.sub, { color: c.textMuted }]}>{error}</Text>
            <Pressable
              accessibilityRole="button"
              onPress={loadInfo}
              style={[styles.primaryBtn, { backgroundColor: c.accent }]}
            >
              <Text style={[styles.primaryBtnText, { color: c.bg }]}>Try again</Text>
            </Pressable>
          </>
        )}
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  body: { flex: 1, alignItems: "center", justifyContent: "center", padding: 32, gap: 16 },
  title: { fontSize: 28, fontWeight: "700", textAlign: "center" },
  sub: { fontSize: 16, textAlign: "center", lineHeight: 22, maxWidth: 340 },
  primaryBtn: {
    marginTop: 12,
    paddingHorizontal: 40,
    paddingVertical: 16,
    borderRadius: 14,
    minWidth: 200,
    alignItems: "center",
  },
  primaryBtnText: { fontSize: 18, fontWeight: "600" },
});
