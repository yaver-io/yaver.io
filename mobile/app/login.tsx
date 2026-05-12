import { Ionicons, FontAwesome } from "@expo/vector-icons";
import * as AppleAuthentication from "expo-apple-authentication";
import Constants from "expo-constants";
import * as Linking from "expo-linking";
import * as ExpoLinking from "expo-linking";
import * as WebBrowser from "expo-web-browser";
import { router } from "expo-router";
import React, { useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Image,
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
import { OAUTH_REDIRECT } from "../src/_core/constants";
import { useAuth } from "../src/context/AuthContext";
import { useColors, useTheme } from "../src/context/ThemeContext";
import { useResponsiveLayout } from "../src/hooks/useResponsiveLayout";
import {
  type OAuthProvider,
  getConvexSiteUrl,
  getOAuthUrl,
  signupWithEmail,
  loginWithEmail,
} from "../src/lib/auth";
import {
  PasskeyCancelled,
  PasskeyError,
  isPasskeySupported,
  passkeySignin,
  passkeySignup,
} from "../src/lib/passkey";

WebBrowser.maybeCompleteAuthSession();

const LEGACY_OAUTH_REDIRECT = "yaver:///oauth-callback";
const YAVER_LOGIN_WORDMARK_DARK = require("../assets/branding/yaver-login-wordmark-dark.png");
const YAVER_LOGIN_WORDMARK_LIGHT = require("../assets/branding/yaver-login-wordmark-light.png");

function isOAuthCallbackUrl(url: string): boolean {
  return url.startsWith(OAUTH_REDIRECT) || url.startsWith(LEGACY_OAUTH_REDIRECT);
}

export default function LoginScreen() {
  const { login, surveyCompleted } = useAuth();
  const { isDark } = useTheme();
  const c = useColors();
  const layout = useResponsiveLayout();
  // Tablet polish:
  //   • portrait: cap content width ~440pt, centered. The phone-shaped
  //     login page on a 10" tablet stretched buttons across the whole
  //     screen and read as a scaled-up phone UI.
  //   • landscape: split-pane — brand on the left half, buttons on the
  //     right. Reads as a real tablet sign-in screen, not a phone with
  //     a giant header above the buttons.
  //   • phone: unchanged (everything below uses the original styles).
  const isTabletLandscape = layout.layoutClass === "tablet-landscape";
  const isTablet = layout.isTablet;
  const [isLoading, setIsLoading] = useState(false);
  const [showEmailForm, setShowEmailForm] = useState(false);
  const [isSignUp, setIsSignUp] = useState(false);
  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [emailError, setEmailError] = useState("");
  const [passkeyLoading, setPasskeyLoading] = useState(false);
  const passkeySupported = isPasskeySupported();
  const isTabletPortrait = isTablet && !isTabletLandscape;
  const loginWordmark = isDark ? YAVER_LOGIN_WORDMARK_LIGHT : YAVER_LOGIN_WORDMARK_DARK;
  const providerGap = isTablet ? 10 : 8;
  const providerBorderColor = isDark ? c.borderSubtle : c.border;
  const heroCardShadow = !isDark
    ? {
        shadowColor: c.shadowSm,
        shadowOffset: { width: 0, height: 10 },
        shadowOpacity: 0.22,
        shadowRadius: 24,
        elevation: 4,
      }
    : null;
  const elevatedCardShadow = !isDark
    ? {
        shadowColor: c.shadowSm,
        shadowOffset: { width: 0, height: 12 },
        shadowOpacity: 0.28,
        shadowRadius: 28,
        elevation: 6,
      }
    : null;
  const darkHeroGlow = isDark
    ? {
        shadowColor: c.shadowMd,
        shadowOffset: { width: 0, height: 8 },
        shadowOpacity: 0.3,
        shadowRadius: 20,
        elevation: 6,
      }
    : null;

  // Belt-and-braces fallback: if the OAuth deep link arrives while
  // LoginScreen is still mounted (cold-start race winner), consume
  // the token here. The canonical handler is app/oauth-callback.tsx,
  // which expo-router routes to whether or not this listener fires.
  useEffect(() => {
    const subscription = Linking.addEventListener("url", async (event) => {
      const url = event.url;
      if (!isOAuthCallbackUrl(url)) return;

      const parsed = Linking.parse(url);
      const token = parsed.queryParams?.token as string | undefined;
      if (token) {
        try {
          await login(token);
          router.replace("/");
        } catch {
          // Token validation failed
        }
      }
    });

    return () => subscription.remove();
  }, [login]);

  // Passkey sign-in: works for any user who has previously enrolled
  // a passkey on web or mobile, regardless of how they originally
  // signed up (Apple OAuth, Google OAuth, email/password). Discoverable
  // credentials let the platform passkey picker show without needing
  // an email field first.
  const handlePasskeySignin = async () => {
    setEmailError("");
    setPasskeyLoading(true);
    try {
      const result = await passkeySignin(getConvexSiteUrl());
      await login(result.token);
      router.replace("/");
    } catch (e: unknown) {
      if (e instanceof PasskeyCancelled) {
        // User dismissed the platform sheet — silent.
      } else if (e instanceof PasskeyError) {
        setEmailError(e.message || "Passkey sign-in failed.");
      } else {
        setEmailError(e instanceof Error ? e.message : "Passkey sign-in failed.");
      }
    } finally {
      setPasskeyLoading(false);
    }
  };

  // Passkey sign-up: brand-new account. Email + full name come from
  // the email-form fields above; we surface a clear hint when the
  // email is already registered (route the user to sign-in instead).
  const handlePasskeySignup = async () => {
    setEmailError("");
    if (!email.trim() || !email.includes("@")) {
      setEmailError("Enter your email first.");
      setShowEmailForm(true);
      setIsSignUp(true);
      return;
    }
    setPasskeyLoading(true);
    try {
      const outcome = await passkeySignup(getConvexSiteUrl(), email.trim(), fullName.trim());
      if (!outcome.ok) {
        if (outcome.error === "EMAIL_EXISTS") {
          setEmailError(
            outcome.hasPasskey
              ? "An account with that email already exists. Use 'Sign in with passkey' instead."
              : "An account with that email already exists. Sign in with your existing method, then add a passkey from settings.",
          );
        } else if (outcome.error === "INVALID_EMAIL") {
          setEmailError("Email looks invalid.");
        }
        return;
      }
      await login(outcome.result.token);
      router.replace("/");
    } catch (e: unknown) {
      if (e instanceof PasskeyCancelled) {
        // Silent
      } else if (e instanceof PasskeyError) {
        setEmailError(e.message || "Passkey sign-up failed.");
      } else {
        setEmailError(e instanceof Error ? e.message : "Passkey sign-up failed.");
      }
    } finally {
      setPasskeyLoading(false);
    }
  };

  // openAuthSessionAsync returns the final redirect URL via the
  // awaited promise, so the OAuth token can't be lost to a deep-link
  // / route-mount race the way openBrowserAsync allowed. (Settings
  // already uses the same API for the link flow.)
  const handleOAuth = async (provider: OAuthProvider) => {
    const url = getOAuthUrl(provider);
    const returnUrl = OAUTH_REDIRECT;
    setIsLoading(true);
    try {
      const result = await WebBrowser.openAuthSessionAsync(url, returnUrl);
      if (result.type !== "success" || !result.url) return;
      const parsed = ExpoLinking.parse(result.url);
      const token = parsed.queryParams?.token as string | undefined;
      if (!token) return;
      await login(token);
      router.replace("/");
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : "Sign-in failed";
      Alert.alert("Sign In Failed", message);
    } finally {
      setIsLoading(false);
    }
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
          contentContainerStyle={[
            styles.scrollContainer,
            isTabletLandscape && styles.scrollContainerLandscape,
            isTabletPortrait && styles.scrollContainerTabletPortrait,
          ]}
          keyboardShouldPersistTaps="handled"
        >
          <View style={[styles.shell, isTabletLandscape && styles.shellLandscape]}>
            <View
              style={[
                styles.header,
                isTabletPortrait && styles.headerTabletPortrait,
                isTabletLandscape && styles.headerLandscape,
              ]}
            >
              <Image
                source={loginWordmark}
                style={[
                  styles.wordmark,
                  isTabletPortrait && styles.wordmarkTabletPortrait,
                  isTabletLandscape && styles.wordmarkTabletLandscape,
                ]}
                resizeMode="contain"
                accessibilityRole="image"
                accessibilityLabel="Yaver"
              />
              <Text
                style={[
                  styles.subtitle,
                  { color: c.textSecondary },
                  isTabletLandscape && styles.subtitleTabletLandscape,
                  isTabletPortrait && styles.subtitleTablet,
                ]}
              >
                {isTablet ? "AI coding assistant for your machines, from anywhere." : "AI coding assistant for your machines."}
              </Text>
              {isTabletLandscape && (
                <Text style={[styles.tertiaryTagline, { color: c.textMuted }]}>
                  Sign in to start coding from anywhere.
                </Text>
              )}
            </View>

            <View
              style={[
                styles.formCard,
                { backgroundColor: isTablet ? c.bgCardElevated : "transparent" },
                isTabletPortrait && styles.formCardTabletPortrait,
                isTabletLandscape && styles.formCardTabletLandscape,
                isTablet && { borderColor: c.borderSubtle },
                isTablet && elevatedCardShadow,
              ]}
            >
              <View style={styles.buttons}>
                {passkeySupported && !showEmailForm && (
                  <Pressable
                    style={({ pressed }) => [
                      styles.passkeyButton,
                      {
                        backgroundColor: isDark ? c.accent + "1F" : c.accentSoft,
                        borderColor: c.accent + "55",
                      },
                      heroCardShadow,
                      darkHeroGlow,
                      pressed && styles.buttonPressed,
                      passkeyLoading && { opacity: 0.6 },
                    ]}
                    onPress={handlePasskeySignin}
                    disabled={passkeyLoading}
                  >
                    <View style={styles.buttonContent}>
                      {passkeyLoading ? (
                        <ActivityIndicator
                          size="small"
                          color={c.accent}
                          style={styles.loadingIcon}
                        />
                      ) : (
                        <Ionicons
                          name="key-outline"
                          size={20}
                          color={c.accent}
                          style={styles.buttonIcon}
                        />
                      )}
                      <Text style={[styles.passkeyText, { color: c.accent }]}>
                        {passkeyLoading ? "Waiting for passkey..." : "Sign in with passkey"}
                      </Text>
                    </View>
                  </Pressable>
                )}

                <View style={[styles.providerGroup, { gap: providerGap }]}>
                  <Pressable
                    style={({ pressed }) => [
                      styles.button,
                      { backgroundColor: c.bgCard, borderColor: providerBorderColor },
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
                      { backgroundColor: c.bgCard, borderColor: providerBorderColor },
                      pressed && styles.buttonPressed,
                    ]}
                    onPress={() => handleOAuth("google")}
                  >
                    <View style={styles.buttonContent}>
                      <Ionicons name="logo-google" size={16} color="#4285F4" style={styles.buttonIcon} />
                      <Text style={[styles.buttonTextCentered, { color: c.textPrimary }]}>Continue with Google</Text>
                    </View>
                  </Pressable>

                  <Pressable
                    style={({ pressed }) => [
                      styles.button,
                      { backgroundColor: c.bgCard, borderColor: providerBorderColor },
                      pressed && styles.buttonPressed,
                    ]}
                    onPress={() => handleOAuth("github")}
                  >
                    <View style={styles.buttonContent}>
                      <Ionicons name="logo-github" size={17} color={c.textPrimary} style={styles.buttonIcon} />
                      <Text style={[styles.buttonTextCentered, { color: c.textPrimary }]}>Continue with GitHub</Text>
                    </View>
                  </Pressable>

                  <Pressable
                    style={({ pressed }) => [
                      styles.button,
                      { backgroundColor: c.bgCard, borderColor: providerBorderColor },
                      pressed && styles.buttonPressed,
                    ]}
                    onPress={() => handleOAuth("gitlab")}
                  >
                    <View style={styles.buttonContent}>
                      <FontAwesome name="gitlab" size={16} color="#FC6D26" style={styles.buttonIcon} />
                      <Text style={[styles.buttonTextCentered, { color: c.textPrimary }]}>Continue with GitLab</Text>
                    </View>
                  </Pressable>

                  <Pressable
                    style={({ pressed }) => [
                      styles.button,
                      { backgroundColor: c.bgCard, borderColor: providerBorderColor },
                      pressed && styles.buttonPressed,
                    ]}
                    onPress={() => handleOAuth("microsoft")}
                  >
                    <View style={styles.buttonContent}>
                      <FontAwesome name="windows" size={16} color="#0078D4" style={styles.buttonIcon} />
                      <Text style={[styles.buttonTextCentered, { color: c.textPrimary }]}>Continue with Microsoft</Text>
                    </View>
                  </Pressable>
                </View>

                {!showEmailForm ? (
                  <>
                    <View style={styles.divider}>
                      <View style={[styles.dividerLine, { backgroundColor: c.borderSubtle }]} />
                      <Text style={[styles.dividerText, { color: c.textMuted }]}>email</Text>
                      <View style={[styles.dividerLine, { backgroundColor: c.borderSubtle }]} />
                    </View>
                    <Pressable
                      style={({ pressed }) => [
                        styles.button,
                        { backgroundColor: c.bgCard, borderColor: providerBorderColor },
                        pressed && styles.buttonPressed,
                      ]}
                      onPress={() => setShowEmailForm(true)}
                    >
                      <View style={styles.buttonContent}>
                        <Ionicons name="mail-outline" size={17} color={c.textPrimary} style={styles.buttonIcon} />
                        <Text style={[styles.buttonTextCentered, { color: c.textPrimary }]}>Continue with Email</Text>
                      </View>
                    </Pressable>
                  </>
                ) : (
                  <>
                    <View style={styles.divider}>
                      <View style={[styles.dividerLine, { backgroundColor: c.borderSubtle }]} />
                      <Text style={[styles.dividerText, { color: c.textMuted }]}>email</Text>
                      <View style={[styles.dividerLine, { backgroundColor: c.borderSubtle }]} />
                    </View>
                    <View style={styles.emailForm}>
                      {isSignUp && (
                        <TextInput
                          style={[
                            styles.input,
                            { backgroundColor: c.bgInput, borderColor: c.borderSubtle, color: c.textPrimary },
                          ]}
                          placeholder="Full Name"
                          placeholderTextColor={c.textMuted}
                          value={fullName}
                          onChangeText={setFullName}
                          autoCapitalize="words"
                          autoCorrect={false}
                        />
                      )}
                      <TextInput
                        style={[
                          styles.input,
                          { backgroundColor: c.bgInput, borderColor: c.borderSubtle, color: c.textPrimary },
                        ]}
                        placeholder="Email"
                        placeholderTextColor={c.textMuted}
                        value={email}
                        onChangeText={setEmail}
                        keyboardType="email-address"
                        autoCapitalize="none"
                        autoCorrect={false}
                      />
                      <TextInput
                        style={[
                          styles.input,
                          { backgroundColor: c.bgInput, borderColor: c.borderSubtle, color: c.textPrimary },
                        ]}
                        placeholder="Password"
                        placeholderTextColor={c.textMuted}
                        value={password}
                        onChangeText={setPassword}
                        secureTextEntry
                      />
                      {isSignUp && (
                        <TextInput
                          style={[
                            styles.input,
                            { backgroundColor: c.bgInput, borderColor: c.borderSubtle, color: c.textPrimary },
                          ]}
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

                      {isSignUp && passkeySupported && (
                        <Pressable
                          style={({ pressed }) => [
                            styles.passkeySignupButton,
                            {
                              backgroundColor: isDark ? c.accent + "1F" : c.accentSoft,
                              borderColor: c.accent + "55",
                            },
                            pressed && styles.buttonPressed,
                            passkeyLoading && { opacity: 0.6 },
                          ]}
                          onPress={handlePasskeySignup}
                          disabled={passkeyLoading || !email.trim() || !fullName.trim()}
                        >
                          {passkeyLoading ? (
                            <ActivityIndicator size="small" color={c.accent} />
                          ) : (
                            <View style={styles.buttonContent}>
                              <Ionicons name="key-outline" size={18} color={c.accent} style={styles.buttonIcon} />
                              <Text style={[styles.passkeySignupText, { color: c.accent }]}>Sign up with passkey</Text>
                            </View>
                          )}
                        </Pressable>
                      )}

                      {!isSignUp && (
                        <Pressable onPress={() => Linking.openURL("https://yaver.io/auth/reset-password")}>
                          <Text style={[styles.forgotText, { color: c.textMuted }]}>
                            Forgot password?
                          </Text>
                        </Pressable>
                      )}

                      <Pressable onPress={() => { setIsSignUp(!isSignUp); setEmailError(""); }}>
                        <Text style={[styles.toggleText, { color: c.textMuted }]}>
                          {isSignUp ? "Already have an account? " : "Don't have an account? "}
                          <Text style={{ color: c.accent }}>
                            {isSignUp ? "Sign In" : "Sign Up"}
                          </Text>
                        </Text>
                      </Pressable>
                    </View>
                  </>
                )}
              </View>
            </View>
          </View>

          <View
            style={[
              styles.footerContainer,
              isTabletLandscape && styles.footerContainerLandscape,
              isTabletPortrait && styles.footerContainerTabletPortrait,
            ]}
          >
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
    paddingTop: 32,
    paddingBottom: 24,
    justifyContent: "space-between",
  },
  scrollContainerTabletPortrait: {
    paddingHorizontal: 32,
    paddingTop: 56,
    paddingBottom: 28,
  },
  scrollContainerLandscape: {
    paddingHorizontal: 48,
    paddingTop: 40,
    paddingBottom: 32,
  },
  shell: {
    width: "100%",
    alignSelf: "center",
    justifyContent: "center",
    flex: 1,
  },
  shellLandscape: {
    flexDirection: "row",
    alignItems: "center",
    gap: 72,
    maxWidth: 1240,
  },
  header: {
    alignItems: "center",
    marginBottom: 40,
  },
  headerTabletPortrait: {
    maxWidth: 480,
    width: "100%",
    alignSelf: "center",
    marginBottom: 0,
    paddingBottom: 24,
  },
  headerLandscape: {
    flex: 1,
    alignItems: "flex-start",
    justifyContent: "center",
    maxWidth: 520,
    marginBottom: 0,
    paddingLeft: 8,
  },
  wordmark: {
    width: 240,
    height: 98,
    marginBottom: 8,
  },
  wordmarkTabletPortrait: {
    width: 320,
    height: 128,
    marginBottom: 10,
  },
  wordmarkTabletLandscape: {
    width: 360,
    height: 140,
    marginBottom: 12,
  },
  subtitle: {
    fontSize: 15,
    lineHeight: 22,
    marginTop: 6,
    textAlign: "center",
  },
  subtitleTablet: {
    fontSize: 17,
  },
  subtitleTabletLandscape: {
    fontSize: 17,
    textAlign: "left",
    maxWidth: 360,
  },
  tertiaryTagline: {
    fontSize: 15,
    lineHeight: 22,
    marginTop: 18,
    maxWidth: 320,
  },
  formCard: {
    width: "100%",
  },
  formCardTabletPortrait: {
    maxWidth: 480,
    alignSelf: "center",
    padding: 32,
    borderRadius: 24,
    borderWidth: 1,
  },
  formCardTabletLandscape: {
    flex: 1,
    maxWidth: 420,
    alignSelf: "center",
    padding: 32,
    borderRadius: 24,
    borderWidth: 1,
  },
  buttons: {
    gap: 0,
  },
  providerGroup: {
    marginTop: 18,
  },
  button: {
    alignItems: "center",
    justifyContent: "center",
    minHeight: 48,
    paddingVertical: 12,
    paddingHorizontal: 16,
    borderRadius: 10,
    borderWidth: 1,
  },
  passkeyButton: {
    minHeight: 56,
    paddingVertical: 15,
    paddingHorizontal: 18,
    borderRadius: 16,
    borderWidth: 1,
    alignItems: "center",
    justifyContent: "center",
  },
  passkeySignupButton: {
    minHeight: 48,
    borderRadius: 10,
    borderWidth: 1,
    alignItems: "center",
    justifyContent: "center",
    paddingVertical: 12,
    marginTop: 2,
  },
  buttonPressed: {
    opacity: 0.85,
    transform: [{ scale: 0.985 }],
  },
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
    fontWeight: "500",
    textAlign: "center",
  },
  passkeyText: {
    fontSize: 16,
    fontWeight: "600",
    textAlign: "center",
  },
  passkeySignupText: {
    fontSize: 15,
    fontWeight: "600",
  },
  footerContainer: {
    marginTop: 40,
    paddingBottom: 24,
    alignItems: "center",
  },
  footerContainerTabletPortrait: {
    maxWidth: 480,
    width: "100%",
    alignSelf: "center",
    marginTop: 28,
  },
  footerContainerLandscape: {
    position: "absolute",
    left: 0,
    right: 0,
    bottom: 16,
    marginTop: 0,
    paddingHorizontal: 32,
  },
  footer: {
    fontSize: 12,
    textAlign: "center",
    lineHeight: 17,
  },
  versionText: {
    fontSize: 11,
    marginTop: 8,
  },
  divider: {
    flexDirection: "row",
    alignItems: "center",
    marginTop: 22,
    marginBottom: 18,
  },
  dividerLine: {
    flex: 1,
    height: 1,
  },
  dividerText: {
    marginHorizontal: 14,
    fontSize: 12,
    fontWeight: "500",
    letterSpacing: 0.4,
    textTransform: "uppercase",
  },
  emailForm: {
    gap: 12,
  },
  input: {
    borderWidth: 1,
    borderRadius: 10,
    paddingVertical: 13,
    paddingHorizontal: 16,
    fontSize: 15,
  },
  errorText: {
    fontSize: 13,
    textAlign: "center",
    lineHeight: 18,
  },
  submitButton: {
    minHeight: 48,
    borderRadius: 10,
    paddingVertical: 12,
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
  loadingIcon: {
    marginRight: 10,
  },
});
