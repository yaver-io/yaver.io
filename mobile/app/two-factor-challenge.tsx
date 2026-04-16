import React, { useState } from "react";
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import { verifyTotpChallenge } from "../src/lib/auth";

// two-factor-challenge.tsx
//
// Shown only when a signed-in user has 2FA enabled. The login screen
// detects `requires2fa` and routes here with a `pendingToken`. Accepts
// a 6-digit TOTP code OR a one-time recovery code — both succeed via
// the same /auth/verify-totp endpoint, so a user who lost their
// authenticator can still sign in. This screen never bypasses 2FA; it
// just keeps recovery auth reachable from the phone.

export default function TwoFactorChallengeScreen() {
  const c = useColors();
  const router = useRouter();
  const { login } = useAuth();
  const { pendingToken } = useLocalSearchParams<{ pendingToken?: string }>();

  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const submit = async () => {
    if (!pendingToken) {
      setError("Session expired — sign in again.");
      return;
    }
    const entered = code.trim();
    if (entered.length < 6) {
      setError("Enter the 6-digit code from your authenticator or a recovery code.");
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const result = await verifyTotpChallenge(pendingToken, entered);
      await login(result.token);
      router.replace("/");
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Two-factor verification failed");
    } finally {
      setLoading(false);
    }
  };

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <View style={styles.container}>
          <Text style={[styles.title, { color: c.textPrimary }]}>Two-Factor Authentication</Text>
          <Text style={[styles.subtitle, { color: c.textSecondary }]}>
            Enter the 6-digit code from your authenticator app, or a recovery code if you lost access.
          </Text>

          <TextInput
            style={[
              styles.input,
              { color: c.textPrimary, backgroundColor: c.bgCard, borderColor: c.border },
            ]}
            value={code}
            onChangeText={(v) => {
              setCode(v.replace(/\s/g, ""));
              setError(null);
            }}
            placeholder="123 456"
            placeholderTextColor={c.textMuted}
            keyboardType="number-pad"
            autoFocus
            autoCapitalize="characters"
            maxLength={16}
          />

          {error ? (
            <Text style={[styles.error, { color: "#ef4444" }]}>{error}</Text>
          ) : null}

          <Pressable
            style={[styles.submit, { backgroundColor: c.accent, opacity: loading ? 0.6 : 1 }]}
            disabled={loading}
            onPress={submit}
          >
            {loading ? (
              <ActivityIndicator color="#fff" />
            ) : (
              <Text style={styles.submitText}>Verify</Text>
            )}
          </Pressable>

          <Pressable onPress={() => router.replace("/login")} style={styles.back}>
            <Text style={[styles.backText, { color: c.textMuted }]}>Back to sign in</Text>
          </Pressable>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  container: { flex: 1, padding: 24, justifyContent: "center" },
  title: { fontSize: 22, fontWeight: "700", marginBottom: 8 },
  subtitle: { fontSize: 14, lineHeight: 20, marginBottom: 24 },
  input: {
    borderWidth: 1,
    borderRadius: 10,
    padding: 14,
    fontSize: 20,
    letterSpacing: 4,
    textAlign: "center",
    fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
  },
  error: { marginTop: 12, fontSize: 13 },
  submit: {
    marginTop: 24,
    paddingVertical: 14,
    borderRadius: 10,
    alignItems: "center",
  },
  submitText: { color: "#fff", fontSize: 16, fontWeight: "600" },
  back: { marginTop: 16, alignItems: "center" },
  backText: { fontSize: 13 },
});
