import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import * as Clipboard from "expo-clipboard";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import {
  beginTotpEnrollment,
  confirmTotpEnrollment,
  disableTotp,
  fetchTotpStatus,
} from "../src/lib/auth";

// two-factor-setup.tsx
//
// Opt-in 2FA enrollment + disable from the mobile app. 2FA is strictly
// optional; accounts that never visit this screen retain the exact same
// sign-in flow as today (OAuth/email only). Recovery codes are shown
// once at enrollment — the user must save them. If they lose the
// authenticator AND the recovery codes, they keep all existing recovery
// paths (device-code re-auth, password reset for email accounts, OAuth
// re-sign-in via a trusted device).

type Mode =
  | { kind: "loading" }
  | { kind: "disabled"; recoveryRemaining: number }
  | { kind: "enrolling"; secret: string; otpAuthUrl: string }
  | { kind: "enabled"; recoveryCodes: string[] }
  | { kind: "active"; recoveryRemaining: number };

export default function TwoFactorSetupScreen() {
  const c = useColors();
  const router = useRouter();
  const { token } = useAuth();
  const [mode, setMode] = useState<Mode>({ kind: "loading" });
  const [code, setCode] = useState("");
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refreshStatus = useCallback(async () => {
    if (!token) return;
    const status = await fetchTotpStatus(token);
    setMode(status.enabled
      ? { kind: "active", recoveryRemaining: status.recoveryCodesRemaining }
      : { kind: "disabled", recoveryRemaining: 0 }
    );
  }, [token]);

  useEffect(() => {
    refreshStatus();
  }, [refreshStatus]);

  const startEnrollment = async () => {
    if (!token) return;
    setWorking(true);
    setError(null);
    try {
      const setup = await beginTotpEnrollment(token);
      setMode({ kind: "enrolling", secret: setup.secret, otpAuthUrl: setup.otpAuthUrl });
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Could not start enrollment");
    } finally {
      setWorking(false);
    }
  };

  const confirm = async () => {
    if (!token || mode.kind !== "enrolling") return;
    setWorking(true);
    setError(null);
    try {
      const { recoveryCodes } = await confirmTotpEnrollment(token, code.trim());
      setMode({ kind: "enabled", recoveryCodes });
      setCode("");
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to enable 2FA");
    } finally {
      setWorking(false);
    }
  };

  const turnOff = async () => {
    if (!token) return;
    const entered = code.trim();
    if (entered.length < 6) {
      setError("Enter a current 6-digit code to disable 2FA.");
      return;
    }
    setWorking(true);
    setError(null);
    try {
      await disableTotp(token, entered);
      setCode("");
      await refreshStatus();
      Alert.alert("2FA disabled");
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to disable 2FA");
    } finally {
      setWorking(false);
    }
  };

  const copyRecovery = async (codes: string[]) => {
    await Clipboard.setStringAsync(codes.join("\n"));
    Alert.alert("Copied", "Recovery codes copied to clipboard. Paste them into your password manager.");
  };

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="Two-Factor Authentication" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={styles.container}>
        <Text style={[styles.subtitle, { color: c.textSecondary }]}>
          Optional. When enabled, new sign-ins require a 6-digit code from an
          authenticator app (Microsoft Authenticator, Google Authenticator,
          1Password, Authy, …). Existing relay sessions on your dev box keep
          working — this never interrupts the connection to your machine.
        </Text>

        {mode.kind === "loading" ? (
          <ActivityIndicator color={c.accent} />
        ) : null}

        {mode.kind === "disabled" ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary }]}>2FA is off</Text>
            <Pressable
              style={[styles.primary, { backgroundColor: c.accent, opacity: working ? 0.6 : 1 }]}
              disabled={working}
              onPress={startEnrollment}
            >
              {working ? <ActivityIndicator color="#fff" /> : <Text style={styles.primaryText}>Enable 2FA</Text>}
            </Pressable>
          </View>
        ) : null}

        {mode.kind === "enrolling" ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Scan with your authenticator</Text>
            <Text style={[styles.cardBody, { color: c.textSecondary }]}>
              Add a new entry in your authenticator app and enter this setup key:
            </Text>
            <Pressable
              onLongPress={() => Clipboard.setStringAsync(mode.secret)}
              style={[styles.secretBox, { borderColor: c.border }]}
            >
              <Text style={[styles.secret, { color: c.textPrimary }]}>{groupSecret(mode.secret)}</Text>
              <Text style={[styles.secretHint, { color: c.textMuted }]}>long-press to copy</Text>
            </Pressable>
            <Text style={[styles.cardBody, { color: c.textSecondary, marginTop: 12 }]}>
              Or tap the otpauth link to send it to your authenticator:
            </Text>
            <Pressable onPress={() => Clipboard.setStringAsync(mode.otpAuthUrl)}>
              <Text style={[styles.link, { color: c.accent }]} numberOfLines={2}>
                {mode.otpAuthUrl}
              </Text>
            </Pressable>

            <Text style={[styles.cardBody, { color: c.textSecondary, marginTop: 16 }]}>
              Enter the 6-digit code the app is showing:
            </Text>
            <TextInput
              style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
              value={code}
              onChangeText={setCode}
              placeholder="123456"
              placeholderTextColor={c.textMuted}
              keyboardType="number-pad"
              maxLength={6}
              autoFocus
            />
            <Pressable
              style={[styles.primary, { backgroundColor: c.accent, opacity: working ? 0.6 : 1 }]}
              disabled={working}
              onPress={confirm}
            >
              {working ? <ActivityIndicator color="#fff" /> : <Text style={styles.primaryText}>Verify and enable</Text>}
            </Pressable>
          </View>
        ) : null}

        {mode.kind === "enabled" ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Save your recovery codes</Text>
            <Text style={[styles.cardBody, { color: c.textSecondary }]}>
              Each code works once if you ever lose access to your
              authenticator. You will NOT see them again.
            </Text>
            <View style={styles.codes}>
              {mode.recoveryCodes.map((rc) => (
                <Text key={rc} style={[styles.codeLine, { color: c.textPrimary }]}>{rc}</Text>
              ))}
            </View>
            <Pressable
              style={[styles.primary, { backgroundColor: c.accent }]}
              onPress={() => copyRecovery(mode.recoveryCodes)}
            >
              <Text style={styles.primaryText}>Copy all</Text>
            </Pressable>
            <Pressable
              style={[styles.secondary, { borderColor: c.border }]}
              onPress={async () => {
                await refreshStatus();
              }}
            >
              <Text style={[styles.secondaryText, { color: c.textPrimary }]}>Done</Text>
            </Pressable>
          </View>
        ) : null}

        {mode.kind === "active" ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary }]}>2FA is on</Text>
            <Text style={[styles.cardBody, { color: c.textSecondary }]}>
              {mode.recoveryRemaining} recovery code{mode.recoveryRemaining === 1 ? "" : "s"} remaining.
            </Text>
            <Text style={[styles.cardBody, { color: c.textSecondary, marginTop: 12 }]}>
              To turn 2FA off, enter a current 6-digit code:
            </Text>
            <TextInput
              style={[styles.input, { color: c.textPrimary, borderColor: c.border }]}
              value={code}
              onChangeText={setCode}
              placeholder="123456"
              placeholderTextColor={c.textMuted}
              keyboardType="number-pad"
              maxLength={6}
            />
            <Pressable
              style={[styles.danger, { borderColor: "#ef4444", opacity: working ? 0.6 : 1 }]}
              disabled={working}
              onPress={turnOff}
            >
              {working ? <ActivityIndicator color="#ef4444" /> : <Text style={[styles.dangerText]}>Disable 2FA</Text>}
            </Pressable>
          </View>
        ) : null}

        {error ? <Text style={styles.errorText}>{error}</Text> : null}
      </ScrollView>
    </SafeAreaView>
  );
}

function groupSecret(secret: string): string {
  const s = secret.toUpperCase().replace(/\s+/g, "");
  const out: string[] = [];
  for (let i = 0; i < s.length; i += 4) {
    out.push(s.slice(i, i + 4));
  }
  return out.join(" ");
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  container: { padding: 20, gap: 16 },
  title: { fontSize: 22, fontWeight: "700" },
  subtitle: { fontSize: 14, lineHeight: 20 },
  card: { borderWidth: 1, borderRadius: 12, padding: 16, gap: 12 },
  cardTitle: { fontSize: 16, fontWeight: "700" },
  cardBody: { fontSize: 13, lineHeight: 18 },
  secretBox: { borderWidth: 1, borderRadius: 8, padding: 12, alignItems: "center" },
  secret: { fontFamily: "Menlo", fontSize: 16, letterSpacing: 2 },
  secretHint: { fontSize: 10, marginTop: 4 },
  link: { fontSize: 12, fontFamily: "Menlo", marginTop: 4 },
  input: {
    borderWidth: 1,
    borderRadius: 10,
    padding: 12,
    fontSize: 18,
    letterSpacing: 4,
    textAlign: "center",
    fontFamily: "Menlo",
  },
  primary: { paddingVertical: 12, borderRadius: 10, alignItems: "center" },
  primaryText: { color: "#fff", fontWeight: "600", fontSize: 15 },
  secondary: { paddingVertical: 12, borderRadius: 10, alignItems: "center", borderWidth: 1 },
  secondaryText: { fontWeight: "600", fontSize: 15 },
  danger: { paddingVertical: 12, borderRadius: 10, alignItems: "center", borderWidth: 1 },
  dangerText: { color: "#ef4444", fontWeight: "600", fontSize: 15 },
  codes: { gap: 4 },
  codeLine: { fontFamily: "Menlo", fontSize: 14 },
  errorText: { color: "#ef4444", fontSize: 13 },
});
