import { Ionicons } from "@expo/vector-icons";
import { router } from "expo-router";
import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Keyboard,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import { loginWithEmail } from "../src/lib/auth";
import { resumePendingDeviceApproval } from "../src/lib/pendingDeviceApproval";
import { YaverAppIcon } from "../src/components/YaverAppIcon";

// One field plus the 12pt form gap and enough room to expose the next control.
// The real keyboard size still comes from the platform; this is only semantic
// clearance for the password field / submit button that follows focus.
const FOLLOWING_CONTROL_CLEARANCE = 112;

export default function EmailLoginScreen() {
  const c = useColors();
  const { login } = useAuth();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [keyboardVisible, setKeyboardVisible] = useState(false);
  const scrollRef = useRef<ScrollView>(null);
  const passwordRef = useRef<TextInput>(null);

  useEffect(() => {
    const show = Keyboard.addListener("keyboardDidShow", () => {
      setKeyboardVisible(true);
      // Samsung's landscape keyboard can overlay rather than resize the RN
      // root. Move the whole two-field card to the top immediately; relying
      // only on focused-field scrolling left Password under the keyboard.
      requestAnimationFrame(() => scrollRef.current?.scrollTo({ y: 0, animated: true }));
    });
    const hide = Keyboard.addListener("keyboardDidHide", () => setKeyboardVisible(false));
    return () => {
      show.remove();
      hide.remove();
    };
  }, []);

  const keepFollowingControlVisible = useCallback(
    (event: { nativeEvent: { target: number } }) => {
      if (Platform.OS === "web") return;
      scrollRef.current?.scrollResponderScrollNativeHandleToKeyboard(
        event.nativeEvent.target,
        FOLLOWING_CONTROL_CLEARANCE,
        true,
      );
    },
    [],
  );

  const submit = useCallback(async () => {
    if (busy) return;
    setError("");
    if (!email.trim() || !password) {
      setError("Email and password are required");
      return;
    }

    setBusy(true);
    try {
      const result = await loginWithEmail(email.trim(), password);
      if (result.kind === "2fa") {
        router.replace({
          pathname: "/two-factor-challenge",
          params: { pendingToken: result.pendingToken },
        });
        return;
      }
      await login(result.token);
      if (!(await resumePendingDeviceApproval())) router.replace("/");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Email sign-in failed");
    } finally {
      setBusy(false);
    }
  }, [busy, email, login, password]);

  return (
    <SafeAreaView style={[styles.safeArea, { backgroundColor: c.bg }]}>
      <ScrollView
        ref={scrollRef}
        style={styles.scroll}
        contentContainerStyle={[styles.content, keyboardVisible && styles.contentKeyboard]}
        keyboardShouldPersistTaps="handled"
        keyboardDismissMode={Platform.OS === "ios" ? "interactive" : "on-drag"}
        automaticallyAdjustKeyboardInsets
        contentInsetAdjustmentBehavior="automatic"
      >
        <View style={[styles.card, { backgroundColor: c.bgCardElevated, borderColor: c.borderSubtle }]}>
          <Pressable
            testID="email-login-back"
            accessibilityRole="button"
            accessibilityLabel="Back to sign-in options"
            onPress={() => router.back()}
            style={styles.back}
          >
            <Ionicons name="chevron-back" size={18} color={c.textMuted} />
            <Text style={[styles.backText, { color: c.textMuted }]}>Sign-in options</Text>
          </Pressable>

          <View style={styles.titleRow}>
            <YaverAppIcon size={44} />
            <Text style={[styles.title, { color: c.textPrimary }]}>Sign in with email</Text>
          </View>

          <TextInput
            testID="email-login-email"
            style={[styles.input, { backgroundColor: c.bgInput, borderColor: c.borderSubtle, color: c.textPrimary }]}
            placeholder="Email"
            placeholderTextColor={c.textMuted}
            value={email}
            onChangeText={setEmail}
            keyboardType="email-address"
            autoCapitalize="none"
            autoCorrect={false}
            autoComplete="email"
            textContentType="emailAddress"
            returnKeyType="next"
            submitBehavior="submit"
            onFocus={keepFollowingControlVisible}
            onSubmitEditing={() => passwordRef.current?.focus()}
          />

          <TextInput
            ref={passwordRef}
            testID="email-login-password"
            style={[styles.input, { backgroundColor: c.bgInput, borderColor: c.borderSubtle, color: c.textPrimary }]}
            placeholder="Password"
            placeholderTextColor={c.textMuted}
            value={password}
            onChangeText={setPassword}
            secureTextEntry
            autoComplete="current-password"
            textContentType="password"
            returnKeyType="go"
            onFocus={keepFollowingControlVisible}
            onSubmitEditing={() => void submit()}
          />

          {error ? <Text style={[styles.error, { color: c.error }]}>{error}</Text> : null}

          <Pressable
            testID="email-login-submit"
            accessibilityRole="button"
            onPress={() => void submit()}
            disabled={busy}
            style={({ pressed }) => [
              styles.submit,
              { backgroundColor: c.accent },
              (pressed || busy) && styles.dimmed,
            ]}
          >
            {busy ? (
              <ActivityIndicator color="#fff" size="small" />
            ) : (
              <Text style={styles.submitText}>Sign In</Text>
            )}
          </Pressable>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safeArea: { flex: 1 },
  scroll: { flex: 1 },
  content: {
    flexGrow: 1,
    justifyContent: "center",
    paddingHorizontal: 24,
    paddingVertical: 24,
  },
  contentKeyboard: {
    justifyContent: "flex-start",
    paddingTop: 8,
    paddingBottom: 8,
  },
  card: {
    width: "100%",
    maxWidth: 460,
    alignSelf: "center",
    borderWidth: 1,
    borderRadius: 20,
    padding: 24,
    gap: 12,
  },
  back: {
    minHeight: 44,
    alignSelf: "flex-start",
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
    paddingRight: 12,
  },
  backText: { fontSize: 14, fontWeight: "600" },
  titleRow: { flexDirection: "row", alignItems: "center", gap: 12, marginBottom: 8 },
  title: {
    fontSize: 22,
    lineHeight: 28,
    fontWeight: "700",
  },
  input: {
    minHeight: 50,
    borderWidth: 1,
    borderRadius: 10,
    paddingHorizontal: 16,
    paddingVertical: 13,
    fontSize: 16,
  },
  error: { fontSize: 13, lineHeight: 18, textAlign: "center" },
  submit: {
    minHeight: 50,
    borderRadius: 10,
    alignItems: "center",
    justifyContent: "center",
    marginTop: 4,
  },
  submitText: { color: "#fff", fontSize: 16, fontWeight: "700" },
  dimmed: { opacity: 0.68 },
});
