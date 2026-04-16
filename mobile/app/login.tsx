import { Ionicons, FontAwesome } from "@expo/vector-icons";
import * as AppleAuthentication from "expo-apple-authentication";
import Constants from "expo-constants";
import * as Linking from "expo-linking";
import * as WebBrowser from "expo-web-browser";
import { router } from "expo-router";
import React, { useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
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
import { useColors, useTheme } from "../src/context/ThemeContext";
import {
  type OAuthProvider,
  getConvexSiteUrl,
  getOAuthUrl,
  signupWithEmail,
  loginWithEmail,
} from "../src/lib/auth";

WebBrowser.maybeCompleteAuthSession();

export default function LoginScreen() {
  const { login, surveyCompleted } = useAuth();
  const { isDark } = useTheme();
  const c = useColors();
  const [isLoading, setIsLoading] = useState(false);
  const [showEmailForm, setShowEmailForm] = useState(false);
  const [isSignUp, setIsSignUp] = useState(false);
  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [emailError, setEmailError] = useState("");

  useEffect(() => {
    const subscription = Linking.addEventListener("url", async (event) => {
      const url = event.url;
      if (!url.startsWith("yaver://oauth-callback")) return;

      const parsed = Linking.parse(url);
      const token = parsed.queryParams?.token as string | undefined;
      if (token) {
        try {
          await login(token);
          // Navigation handled by index.tsx based on survey status
          router.replace("/");
        } catch {
          // Token validation failed
        }
      }
    });

    return () => subscription.remove();
  }, [login]);

  const handleOAuth = async (provider: OAuthProvider) => {
    const url = getOAuthUrl(provider);
    await WebBrowser.openBrowserAsync(url, {
      showInRecents: true,
    });
  };

  const handleAppleSignIn = async () => {
    // Check if native Apple auth is available (requires Apple ID on simulator)
    const isAvailable = await AppleAuthentication.isAvailableAsync();
    if (!isAvailable) {
      // Fall back to web OAuth for Apple (works on simulator without Apple ID)
      await handleOAuth("apple");
      return;
    }

    setIsLoading(true);
    try {
      const credential = await AppleAuthentication.signInAsync({
        requestedScopes: [
          AppleAuthentication.AppleAuthenticationScope.FULL_NAME,
          AppleAuthentication.AppleAuthenticationScope.EMAIL,
        ],
      });

      if (!credential.identityToken) {
        throw new Error("No identity token");
      }

      const fullName = [credential.fullName?.givenName, credential.fullName?.familyName]
        .filter(Boolean)
        .join(" ") || undefined;

      const res = await fetch(`${getConvexSiteUrl()}/auth/apple-native`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          identityToken: credential.identityToken,
          fullName,
        }),
      });

      if (!res.ok) {
        const body = await res.text().catch(() => "");
        throw new Error(body || "Auth failed");
      }

      const { token } = await res.json();
      await login(token);
      router.replace("/");
    } catch (e: unknown) {
      if ((e as { code?: string }).code === "ERR_REQUEST_CANCELED") {
        // User cancelled — do nothing
      } else {
        const msg = e instanceof Error ? e.message : "Apple Sign In failed";
        Alert.alert("Sign In Failed", msg);
      }
    } finally {
      setIsLoading(false);
    }
  };

  const handleEmailSubmit = async () => {
    setEmailError("");
    if (isSignUp) {
      if (!fullName.trim()) {
        setEmailError("Full name is required");
        return;
      }
      if (password !== confirmPassword) {
        setEmailError("Passwords do not match");
        return;
      }
      if (password.length < 8) {
        setEmailError("Password must be at least 8 characters");
        return;
      }
    }
    if (!email.trim() || !password) {
      setEmailError("Email and password are required");
      return;
    }

    setIsLoading(true);
    try {
      if (isSignUp) {
        const result = await signupWithEmail(fullName.trim(), email.trim(), password);
        await login(result.token);
        router.replace("/");
        return;
      }
      const result = await loginWithEmail(email.trim(), password);
      if (result.kind === "2fa") {
        // 2FA is strictly optional; most users never see this branch. When
        // enabled, complete the challenge on a dedicated screen and return
        // once the pending token is exchanged for a session.
        router.replace({
          pathname: "/two-factor-challenge",
          params: { pendingToken: result.pendingToken },
        });
        return;
      }
      await login(result.token);
      router.replace("/");
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : "Something went wrong";
      setEmailError(message);
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <SafeAreaView style={[styles.safeArea, { backgroundColor: c.bg }]}>
      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
      <ScrollView
        contentContainerStyle={styles.scrollContainer}
        keyboardShouldPersistTaps="handled"
      >
        <View style={styles.header}>
          <Text style={[styles.logo, { color: c.textPrimary }]}>Yaver</Text>
          <Text style={[styles.subtitle, { color: c.textSecondary }]}>
            Your AI coding assistant, everywhere.
          </Text>
        </View>

        <View style={styles.buttons}>
          <Pressable
            style={({ pressed }) => [
              styles.button,
              { backgroundColor: c.bgCard, borderColor: c.border },
              pressed && styles.buttonPressed,
            ]}
            onPress={Platform.OS === "ios" ? handleAppleSignIn : () => handleOAuth("apple")}
          >
            <View style={styles.buttonContent}>
              <Ionicons name="logo-apple" size={18} color={c.textPrimary} style={styles.buttonIcon} />
              <Text style={[styles.buttonTextCentered, { color: c.textPrimary }]}>Continue with Apple</Text>
            </View>
          </Pressable>

          <Pressable
            style={({ pressed }) => [
              styles.button,
              { backgroundColor: c.bgCard, borderColor: c.border },
              pressed && styles.buttonPressed,
            ]}
            onPress={() => handleOAuth("google")}
          >
            <View style={styles.buttonContent}>
              <Ionicons name="logo-google" size={16} color={c.textPrimary} style={styles.buttonIcon} />
              <Text style={[styles.buttonTextCentered, { color: c.textPrimary }]}>Continue with Google</Text>
            </View>
          </Pressable>

          <Pressable
            style={({ pressed }) => [
              styles.button,
              { backgroundColor: c.bgCard, borderColor: c.border },
              pressed && styles.buttonPressed,
            ]}
            onPress={() => handleOAuth("microsoft")}
          >
            <View style={styles.buttonContent}>
              <FontAwesome name="windows" size={16} color={c.textPrimary} style={styles.buttonIcon} />
              <Text style={[styles.buttonTextCentered, { color: c.textPrimary }]}>Continue with Microsoft</Text>
            </View>
          </Pressable>

          {/* Continue with Email — collapsed button or expanded form */}
          {!showEmailForm ? (
            <Pressable
              style={({ pressed }) => [
                styles.button,
                { backgroundColor: c.bgCard, borderColor: c.border },
                pressed && styles.buttonPressed,
              ]}
              onPress={() => setShowEmailForm(true)}
            >
              <View style={styles.buttonContent}>
                <Ionicons name="mail-outline" size={17} color={c.textPrimary} style={styles.buttonIcon} />
                <Text style={[styles.buttonTextCentered, { color: c.textPrimary }]}>Continue with Email</Text>
              </View>
            </Pressable>
          ) : (
            <>
              <View style={styles.divider}>
                <View style={[styles.dividerLine, { backgroundColor: c.border }]} />
                <Text style={[styles.dividerText, { color: c.textMuted }]}>email</Text>
                <View style={[styles.dividerLine, { backgroundColor: c.border }]} />
              </View>
              <View style={styles.emailForm}>
                {isSignUp && (
                  <TextInput
                    style={[styles.input, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
                    placeholder="Full Name"
                    placeholderTextColor={c.textMuted}
                    value={fullName}
                    onChangeText={setFullName}
                    autoCapitalize="words"
                    autoCorrect={false}
                  />
                )}
                <TextInput
                  style={[styles.input, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
                  placeholder="Email"
                  placeholderTextColor={c.textMuted}
                  value={email}
                  onChangeText={setEmail}
                  keyboardType="email-address"
                  autoCapitalize="none"
                  autoCorrect={false}
                />
                <TextInput
                  style={[styles.input, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
                  placeholder="Password"
                  placeholderTextColor={c.textMuted}
                  value={password}
                  onChangeText={setPassword}
                  secureTextEntry
                />
                {isSignUp && (
                  <TextInput
                    style={[styles.input, { backgroundColor: c.bgCard, borderColor: c.border, color: c.textPrimary }]}
                    placeholder="Confirm Password"
                    placeholderTextColor={c.textMuted}
                    value={confirmPassword}
                    onChangeText={setConfirmPassword}
                    secureTextEntry
                  />
                )}

                {emailError ? (
                  <Text style={[styles.errorText, { color: c.error }]}>{emailError}</Text>
                ) : null}

                <Pressable
                  style={({ pressed }) => [
                    styles.submitButton,
                    { backgroundColor: c.accent },
                    pressed && styles.buttonPressed,
                    isLoading && { opacity: 0.6 },
                  ]}
                  onPress={handleEmailSubmit}
                  disabled={isLoading}
                >
                  {isLoading ? (
                    <ActivityIndicator size="small" color="#fff" />
                  ) : (
                    <Text style={styles.submitButtonText}>
                      {isSignUp ? "Create Account" : "Sign In"}
                    </Text>
                  )}
                </Pressable>

                {!isSignUp && (
                  <Pressable onPress={() => Linking.openURL("https://yaver.io/auth/reset-password")}>
                    <Text style={[styles.forgotText, { color: c.textMuted }]}>
                      Forgot password?
                    </Text>
                  </Pressable>
                )}

                <Pressable onPress={() => { setIsSignUp(!isSignUp); setEmailError(""); }}>
                  <Text style={[styles.toggleText, { color: c.accent }]}>
                    {isSignUp ? "Already have an account? Sign In" : "Don't have an account? Sign Up"}
                  </Text>
                </Pressable>
              </View>
            </>
          )}
        </View>

        <View style={styles.footerContainer}>
          <Text style={[styles.footer, { color: c.textMuted }]}>
            By signing in you agree to the{" "}
            <Text
              style={{ color: c.accent }}
              onPress={() => Linking.openURL("https://yaver.io/terms")}
            >
              Terms of Service
            </Text>{" "}
            and{" "}
            <Text
              style={{ color: c.accent }}
              onPress={() => Linking.openURL("https://yaver.io/privacy")}
            >
              Privacy Policy
            </Text>
            .
          </Text>
          <Text style={[styles.versionText, { color: c.textMuted }]}>
            v{Constants.expoConfig?.version ?? "1.0.0"}
          </Text>
        </View>
      </ScrollView>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safeArea: { flex: 1 },
  scrollContainer: {
    flexGrow: 1,
    paddingHorizontal: 24,
    justifyContent: "center",
  },
  header: {
    alignItems: "center",
    marginBottom: 64,
  },
  logo: {
    fontSize: 48,
    fontWeight: "800",
    letterSpacing: -1,
  },
  subtitle: {
    fontSize: 16,
    marginTop: 8,
  },
  buttons: { gap: 12 },
  button: {
    alignItems: "center",
    justifyContent: "center",
    paddingVertical: 14,
    paddingHorizontal: 16,
    borderRadius: 12,
    borderWidth: 1,
  },
  buttonPressed: { opacity: 0.7 },
  buttonContent: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
  },
  buttonIcon: {
    marginRight: 10,
  },
  buttonTextCentered: {
    fontSize: 15,
    fontWeight: "600",
    textAlign: "center",
  },
  footerContainer: {
    marginTop: 48,
    paddingBottom: 24,
    alignItems: "center",
  },
  footer: {
    fontSize: 12,
    textAlign: "center",
    lineHeight: 18,
  },
  versionText: {
    fontSize: 11,
    marginTop: 12,
    opacity: 0.6,
  },
  divider: {
    flexDirection: "row",
    alignItems: "center",
    marginTop: 24,
    marginBottom: 24,
  },
  dividerLine: {
    flex: 1,
    height: 1,
  },
  dividerText: {
    marginHorizontal: 16,
    fontSize: 13,
    fontWeight: "500",
  },
  emailForm: {
    gap: 12,
  },
  input: {
    borderWidth: 1,
    borderRadius: 12,
    paddingVertical: 14,
    paddingHorizontal: 16,
    fontSize: 15,
  },
  errorText: {
    fontSize: 13,
    textAlign: "center",
  },
  submitButton: {
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: "center",
    justifyContent: "center",
    marginTop: 4,
  },
  submitButtonText: {
    color: "#fff",
    fontSize: 15,
    fontWeight: "600",
  },
  toggleText: {
    fontSize: 14,
    textAlign: "center",
    marginTop: 4,
  },
  forgotText: {
    fontSize: 13,
    textAlign: "right",
    marginTop: 2,
    marginBottom: 4,
  },
});
