import { Ionicons, FontAwesome } from "@expo/vector-icons";
import * as AppleAuthentication from "expo-apple-authentication";
import Constants from "expo-constants";
import * as Crypto from "expo-crypto";
import * as Linking from "expo-linking";
import * as ExpoLinking from "expo-linking";
import * as WebBrowser from "expo-web-browser";
import { router } from "expo-router";
import React, { useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Image,
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
import { appLog } from "../src/lib/logger";
import { useAuth } from "../src/context/AuthContext";
import { useColors, useTheme } from "../src/context/ThemeContext";
import { useResponsiveLayout } from "../src/hooks/useResponsiveLayout";
import {
  formatUserCode,
  startDeviceCodeSignIn,
  waitForDeviceCodeToken,
} from "../src/lib/deviceCodeSignIn";
import {
  type OAuthProvider,
  getConvexSiteUrl,
  getOAuthUrl,
  getAuthConfig,
  signupWithEmail,
  loginWithEmail,
  saveToken,
  saveUser,
  validateTokenDetailed,
} from "../src/lib/auth";
import {
  PasskeyCancelled,
  PasskeyError,
  PasskeyMisconfigured,
  PasskeyNoCredential,
  isPasskeySupported,
  passkeyHasCredential,
  passkeySignin,
  passkeySignup,
} from "../src/lib/passkey";

import { resumePendingDeviceApproval } from "../src/lib/pendingDeviceApproval";
import { SESSION_EXPIRED_NOTICE } from "../src/lib/sessionExpiredNotice";

WebBrowser.maybeCompleteAuthSession();

// After a successful sign-in, resume a pending Apple TV / device-code approval
// if the user scanned a QR while signed out (the code was stashed before the
// login round-trip). Otherwise go home. This is what stops the TV getting stuck
// "Waiting for approval" after sign-in.
//
// Every `await login(...)` in the app must go through this (or call
// resumePendingDeviceApproval itself) — src/lib/pendingDeviceCodeLoginPaths.test.ts
// fails the build otherwise. That test exists because this screen was fixed on
// 2026-07-15 while app/oauth-callback.tsx kept dropping the code, which is the
// path a browser OAuth sign-in actually takes.
async function finishLogin() {
  if (await resumePendingDeviceApproval()) return;
  router.replace("/");
}

const LEGACY_OAUTH_REDIRECT = "yaver:///oauth-callback";
const YAVER_LOGIN_WORDMARK_DARK = require("../assets/branding/yaver-login-wordmark-dark.png");
const YAVER_LOGIN_WORDMARK_LIGHT = require("../assets/branding/yaver-login-wordmark-light.png");

// One 48pt control + the email form's 12pt gap + breathing room. This is not a
// device/keyboard offset: it tells the native ScrollView to reveal the control
// immediately after the focused field. Without it, UIKit correctly revealed a
// focused Email input while leaving Password completely behind the keyboard.
const LOGIN_FOLLOWING_CONTROL_CLEARANCE = 72;

function randomHex(bytes: Uint8Array): string {
  return Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

function isOAuthCallbackUrl(url: string): boolean {
  return url.startsWith(OAUTH_REDIRECT) || url.startsWith(LEGACY_OAUTH_REDIRECT);
}

export default function LoginScreen() {
  const { login, surveyCompleted, sessionExpired } = useAuth();
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
  // Code sign-in. The whole flow is three bits of state: the short code to show,
  // a line saying what is happening, and a cancel flag the poll loop reads.
  const [deviceCode, setDeviceCode] = useState<{ userCode: string; secret: string } | null>(null);
  const [deviceCodeNote, setDeviceCodeNote] = useState("");
  const deviceCodeCancelled = useRef(false);

  // Sign in with a short code — no browser, no keyboard on this device.
  //
  // backend/convex/deviceCode.ts has shipped this flow the whole time; this
  // screen simply had no entry point to it, so a new phone or a simulator could
  // not be signed in without a human driving a browser. That is what blocked
  // every automated iOS/Android arc at its first step.
  //
  // THIS DEVICE CREATES THE CODE. poll/claim key off the 40-hex SECRET, not the
  // short code the human reads — so a screen that merely accepts a typed code
  // could never complete. The short code is for the approver to recognise.
  const startCodeSignIn = React.useCallback(async () => {
    deviceCodeCancelled.current = false;
    setDeviceCodeNote("");
    setIsLoading(true);
    let started: { userCode: string; deviceCode: string };
    try {
      started = await startDeviceCodeSignIn({ machineName: "Yaver mobile", platform: "mobile", environment: "mobile" });
    } catch (err) {
      // Say why. A tap that appears to do nothing is the failure mode this
      // whole seam exists to remove.
      setDeviceCodeNote(err instanceof Error ? err.message : String(err));
      setIsLoading(false);
      return;
    }
    setDeviceCode({ userCode: started.userCode, secret: started.deviceCode });
    setDeviceCodeNote("Waiting for approval…");
    setIsLoading(false);

    const result = await waitForDeviceCodeToken(started.deviceCode, {
      isCancelled: () => deviceCodeCancelled.current,
      onTick: ({ elapsedMs, unreachableReason }) => {
        // Narrate the wait, and NEVER let an unreachable server render as
        // "waiting for you" — they look identical and mean opposite things.
        const secs = Math.round(elapsedMs / 1000);
        setDeviceCodeNote(
          unreachableReason
            ? `Can't reach Yaver right now (${unreachableReason}) — still trying · ${secs}s`
            : `Waiting for approval… ${secs}s`,
        );
      },
    });

    if (result.kind === "cancelled") return;
    if (result.kind === "token") {
      await saveToken(result.token);
      const validated = await validateTokenDetailed(result.token);
      if (validated.kind === "valid") {
        await saveUser(validated.user);
        setDeviceCode(null);
        await finishLogin();
        return;
      }
      // The token minted but did not validate. Do not strand the user on a
      // screen that says "approved" — name it and let them retry.
      setDeviceCodeNote(
        validated.kind === "networkError"
          ? "Signed in, but Yaver could not be reached to confirm it. Check your connection and try again."
          : "That approval did not produce a usable session. Start a new code.",
      );
      return;
    }
    setDeviceCodeNote(
      result.kind === "expired"
        ? "That code expired. Tap to get a new one."
        : `No approval arrived${result.lastReason ? ` (${result.lastReason})` : ""}. Tap to get a new one.`,
    );
    setDeviceCode(null);
  }, []);

  const cancelCodeSignIn = React.useCallback(() => {
    deviceCodeCancelled.current = true;
    setDeviceCode(null);
    setDeviceCodeNote("");
  }, []);
  const [isSignUp, setIsSignUp] = useState(false);
  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [emailError, setEmailError] = useState("");
  const [passkeyLoading, setPasskeyLoading] = useState(false);
  const [emailPasswordEnabled, setEmailPasswordEnabled] = useState(false);
  const [wordmarkFailed, setWordmarkFailed] = useState(false);
  const loginScrollRef = useRef<ScrollView>(null);
  const passwordInputRef = useRef<TextInput>(null);
  const confirmPasswordInputRef = useRef<TextInput>(null);
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

  // automaticallyAdjustKeyboardInsets keeps the focused input visible. The
  // login task needs one stronger guarantee: while Email is focused, Password
  // must also remain tappable; while Password is focused, Sign In must remain
  // visible. React Native's own scroll responder measures the real keyboard
  // frame, including iOS accessory/floating keyboards and Android resize mode.
  const keepNextLoginControlVisible = React.useCallback(
    (event: { nativeEvent: { target: number } }) => {
      // RN-web delegates focus visibility to the browser and its ScrollView
      // ref does not implement React Native's native scroll responder.
      if (Platform.OS === "web") return;
      loginScrollRef.current?.scrollResponderScrollNativeHandleToKeyboard(
        event.nativeEvent.target,
        LOGIN_FOLLOWING_CONTROL_CLEARANCE,
        true,
      );
    },
    [],
  );
  const focusPassword = React.useCallback(() => passwordInputRef.current?.focus(), []);
  const focusConfirmPassword = React.useCallback(
    () => confirmPasswordInputRef.current?.focus(),
    [],
  );

  // Belt-and-braces fallback: if the OAuth deep link arrives while
  // LoginScreen is still mounted (cold-start race winner), consume
  // the token here. The canonical handler is app/oauth-callback.tsx,
  // which expo-router routes to whether or not this listener fires.
  useEffect(() => {
    void getAuthConfig().then((config) => {
      setEmailPasswordEnabled(config.emailPasswordEnabled);
      if (!config.emailPasswordEnabled) {
        setShowEmailForm(false);
      }
    });
  }, []);

  useEffect(() => {
    if (!emailPasswordEnabled) {
      setShowEmailForm(false);
    }
  }, [emailPasswordEnabled]);

  useEffect(() => {
    const subscription = Linking.addEventListener("url", async (event) => {
      const url = event.url;
      // Log EVERY inbound url, matched or not. A sign-in deep link that
      // silently does nothing is indistinguishable from one that never
      // arrived, and telling those two apart is the whole diagnosis when
      // someone reports "I tapped sign in and nothing happened".
      appLog("info", `[auth] deep link received: ${url.split("?")[0]}`);
      if (!isOAuthCallbackUrl(url)) {
        appLog("warn", `[auth] deep link ignored — does not match ${OAUTH_REDIRECT}`);
        return;
      }

      const parsed = Linking.parse(url);
      const token = parsed.queryParams?.token as string | undefined;
      if (!token) {
        appLog("warn", "[auth] callback deep link carried no token");
        return;
      }
      try {
        await login(token);
        void finishLogin();
      } catch (e) {
        // Previously swallowed entirely. A rejected or unreachable-server
        // token left the user on the sign-in screen with no error and no
        // log — the failure mode this whole screen exists to report.
        const msg = e instanceof Error ? e.message : String(e);
        appLog("error", `[auth] deep-link sign-in failed: ${msg}`);
        Alert.alert("Sign-in failed", msg);
      }
    });

    return () => subscription.remove();
  }, [login]);

  // Passkey sign-in: works for any user who has previously enrolled
  // a passkey on web or mobile, regardless of how they originally
  // signed up (Apple OAuth, Google OAuth, email/password). Discoverable
  // credentials let the platform passkey picker show without needing
  // an email field first.
  //
  // No-credential pre-flight: when the email field is filled, ping the
  // backend to ask whether that account has any passkey enrolled. iOS
  // returns the same error code for "user cancelled" and "no credentials
  // for this rpId" — without this preflight the iOS sheet auto-dismisses
  // silently and the user sees the button revert with no feedback.
  const handlePasskeySignin = async () => {
    setEmailError("");
    setPasskeyLoading(true);
    try {
      if (email.trim()) {
        const probe = await passkeyHasCredential(getConvexSiteUrl(), email.trim());
        if (probe && probe.emailRegistered && !probe.hasPasskey) {
          setEmailError(
            "No passkey on this account yet. Sign in with Apple / Google / Email below, then enable passkey from Settings.",
          );
          return;
        }
        if (probe && !probe.emailRegistered) {
          setEmailError(
            "No account for that email. Use 'Continue with Email' or another provider to sign up first.",
          );
          return;
        }
      }
      const result = await passkeySignin(getConvexSiteUrl());
      if (!("token" in result)) {
        router.replace({
          pathname: "/two-factor-challenge",
          params: { pendingToken: result.pendingToken },
        });
        return;
      }
      await login(result.token);
      void finishLogin();
    } catch (e: unknown) {
      if (e instanceof PasskeyCancelled) {
        // User dismissed the platform sheet — silent.
      } else if (e instanceof PasskeyNoCredential) {
        setEmailError(
          "No passkey found on this device. Sign in with Apple / Google / Email below, then enable passkey from Settings.",
        );
      } else if (e instanceof PasskeyMisconfigured) {
        Alert.alert("Passkey setup issue", e.message);
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
          // Route the user to the most useful next step:
          //   • Already enrolled passkey → "Sign in with passkey".
          //   • OAuth providers present → name them explicitly so the
          //     user can tap "Continue with Apple" below and have the
          //     backend auto-link their new identity (verified-by-IdP).
          //   • Email-only → tell them to sign in then enroll passkey.
          const oauthProviders =
            outcome.providers?.filter((p) =>
              ["google", "microsoft", "apple", "github", "gitlab"].includes(p),
            ) ?? [];
          if (outcome.hasPasskey) {
            setEmailError(
              "An account with that email already exists. Use 'Sign in with passkey' instead.",
            );
          } else if (oauthProviders.length > 0) {
            const labels = oauthProviders
              .map((p) => p.charAt(0).toUpperCase() + p.slice(1))
              .join(" / ");
            setEmailError(
              `An account with that email already exists. Sign in with ${labels} below — your passkey will be added to that account automatically.`,
            );
          } else {
            setEmailError(
              "An account with that email already exists. Sign in with your existing method, then add a passkey from settings.",
            );
          }
        } else if (outcome.error === "INVALID_EMAIL") {
          setEmailError("Email looks invalid.");
        }
        return;
      }
      if (!("token" in outcome.result)) {
        router.replace({
          pathname: "/two-factor-challenge",
          params: { pendingToken: outcome.result.pendingToken },
        });
        return;
      }
      await login(outcome.result.token);
      void finishLogin();
    } catch (e: unknown) {
      if (e instanceof PasskeyCancelled) {
        // Silent
      } else if (e instanceof PasskeyMisconfigured) {
        Alert.alert("Passkey setup issue", e.message);
      } else if (e instanceof PasskeyError) {
        setEmailError(e.message || "Passkey sign-up failed.");
      } else {
        setEmailError(e instanceof Error ? e.message : "Passkey sign-up failed.");
      }
    } finally {
      setPasskeyLoading(false);
    }
  };

  // (Post-OAuth enrollment prompt removed in 1.18.118 — racing the
  // freshly-minted session token against the immediate register/start
  // call surfaced sporadic 401s before storage hydration completed.
  // Settings → Add a passkey runs after the session has fully settled
  // and works reliably; that's the canonical enrollment path now.)

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
      void finishLogin();
    } catch (e: unknown) {
      // Keep the raw reason for logs/debugging, but lead with friendly copy —
      // never surface a bare e.message as the primary line.
      if (__DEV__) console.warn("OAuth sign-in failed:", e);
      Alert.alert(
        "Sign In Failed",
        "Couldn't sign in — check your connection and try again.",
      );
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
      const rawNonce = randomHex(Crypto.getRandomBytes(16));
      const nonce = await Crypto.digestStringAsync(
        Crypto.CryptoDigestAlgorithm.SHA256,
        rawNonce,
      );
      const credential = await AppleAuthentication.signInAsync({
        requestedScopes: [
          AppleAuthentication.AppleAuthenticationScope.FULL_NAME,
          AppleAuthentication.AppleAuthenticationScope.EMAIL,
        ],
        nonce,
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
          nonce,
        }),
      });

      if (!res.ok) {
        // Don't surface the raw HTTP body to the user. Log it for debugging
        // and throw a plain message the catch below turns into friendly copy.
        const body = await res.text().catch(() => "");
        if (__DEV__ && body) console.warn("apple-native auth failed:", body);
        throw new Error(`Apple sign-in failed (HTTP ${res.status})`);
      }

      const { token } = await res.json();
      await login(token);
      void finishLogin();
    } catch (e: unknown) {
      if ((e as { code?: string }).code === "ERR_REQUEST_CANCELED") {
        // User cancelled — do nothing
      } else {
        // Friendly copy first; raw reason stays in the dev log only.
        if (__DEV__) console.warn("Apple sign-in failed:", e);
        Alert.alert(
          "Sign In Failed",
          "Couldn't sign in with Apple — check your connection and try again.",
        );
      }
    } finally {
      setIsLoading(false);
    }
  };

  const handleEmailSubmit = async () => {
    setEmailError("");
    if (!emailPasswordEnabled) {
      setEmailError("Email/password sign-in is disabled on this deployment.");
      return;
    }
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
        void finishLogin();
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
      void finishLogin();
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : "Something went wrong";
      setEmailError(message);
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <SafeAreaView style={[styles.safeArea, { backgroundColor: c.bg }]}>
      {/* Keyboard handling: let the PLATFORM do it.
        *
        * This was a KeyboardAvoidingView with behavior="padding" wrapping a
        * ScrollView — a combination that reliably hides the very field you are
        * typing into. "padding" only adds space at the BOTTOM of the container;
        * it never scrolls the focused input into view. On a form already taller
        * than the screen (five OAuth buttons above the email field) the padding
        * lands below the fold and the input stays behind the keyboard. Reported
        * on TestFlight 2026-08-01: the email field was invisible while typing.
        *
        * automaticallyAdjustKeyboardInsets is the base answer on iOS — UIKit
        * adjusts the scroll view's contentInset for the real keyboard frame. The
        * email fields additionally use the ScrollView's native focus responder
        * to reserve one following-control slot: Email reveals Password, and
        * Password reveals Sign In. This is semantic clearance, not a guessed
        * keyboard height or device-specific vertical offset.
        *
        * Android is handled by the manifest (windowSoftInputMode=adjustResize),
        * which is why no wrapper is needed there either. */}
      <View style={{ flex: 1 }}>
        <ScrollView
          ref={loginScrollRef}
          contentContainerStyle={[
            styles.scrollContainer,
            isTabletLandscape && styles.scrollContainerLandscape,
            isTabletPortrait && styles.scrollContainerTabletPortrait,
          ]}
          keyboardShouldPersistTaps="handled"
          automaticallyAdjustKeyboardInsets
          keyboardDismissMode="interactive"
          contentInsetAdjustmentBehavior="automatic"
        >
          <View style={[styles.shell, isTabletLandscape && styles.shellLandscape]}>
            <View
              style={[
                styles.header,
                isTabletPortrait && styles.headerTabletPortrait,
                isTabletLandscape && styles.headerLandscape,
              ]}
            >
              {Platform.OS === "web" || wordmarkFailed ? (
                <Text
                  accessibilityRole="header"
                  style={[
                    styles.webWordmark,
                    { color: c.textPrimary },
                    isTabletPortrait && styles.webWordmarkTabletPortrait,
                    isTabletLandscape && styles.webWordmarkTabletLandscape,
                  ]}
                >
                  Yaver
                </Text>
              ) : (
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
                  onError={() => setWordmarkFailed(true)}
                />
              )}
              <Text
                style={[
                  styles.subtitle,
                  { color: c.textSecondary },
                  isTabletLandscape && styles.subtitleTabletLandscape,
                  isTabletPortrait && styles.subtitleTablet,
                ]}
              >
                {"Remote AI Runtime"}
              </Text>
              {isTabletLandscape && (
                <Text style={[styles.tertiaryTagline, { color: c.textMuted }]}>
                  Sign in to drive your coding agent from anywhere.
                </Text>
              )}
            </View>

            {sessionExpired ? (
              // Confirmed session revoke (audit gap T6): the user did not
              // choose to be here — say why, exactly like web's dashboard
              // does, instead of a silent dump onto the sign-in gate.
              <View
                style={{
                  alignSelf: "stretch",
                  borderRadius: 10,
                  borderWidth: 1,
                  borderColor: c.warnBorder,
                  backgroundColor: c.warnBg,
                  paddingHorizontal: 12,
                  paddingVertical: 10,
                  marginBottom: 12,
                }}
              >
                <Text style={{ color: c.warn, fontSize: 13, fontWeight: "600" }}>
                  {SESSION_EXPIRED_NOTICE}
                </Text>
              </View>
            ) : null}
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

                {!showEmailForm && <View style={[styles.providerGroup, { gap: providerGap }]}>
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
                </View>}

                {/* Sign in with a code. One row when idle, a panel while a code
                    is live — the flow every other surface already had and this
                    one did not. Placed AFTER the providers because it is the
                    fallback path (no browser here, or signing in a simulator),
                    not the thing most people want first. */}
                {!showEmailForm && (
                  <View style={{ marginTop: providerGap }}>
                  {deviceCode ? (
                    <View style={[styles.deviceCodePanel, { backgroundColor: c.bgCard, borderColor: providerBorderColor }]}>
                      <Text style={[styles.deviceCodeLabel, { color: c.textMuted }]}>
                        Approve this code from a signed-in device
                      </Text>
                      <Text
                        selectable
                        accessibilityLabel={`Sign-in code ${formatUserCode(deviceCode.userCode).split("").join(" ")}`}
                        style={[styles.deviceCodeValue, { color: c.textPrimary }]}
                      >
                        {formatUserCode(deviceCode.userCode)}
                      </Text>
                      {deviceCodeNote ? (
                        <Text style={[styles.deviceCodeNote, { color: c.textMuted }]}>{deviceCodeNote}</Text>
                      ) : null}
                      <Pressable onPress={cancelCodeSignIn} hitSlop={8}>
                        <Text style={[styles.deviceCodeCancel, { color: c.textMuted }]}>Cancel</Text>
                      </Pressable>
                    </View>
                  ) : (
                    <>
                      <Pressable
                        style={({ pressed }) => [
                          styles.button,
                          { backgroundColor: c.bgCard, borderColor: providerBorderColor },
                          pressed && styles.buttonPressed,
                        ]}
                        disabled={isLoading}
                        onPress={() => void startCodeSignIn()}
                      >
                        <View style={styles.buttonContent}>
                          <Ionicons name="keypad-outline" size={17} color={c.textPrimary} style={styles.buttonIcon} />
                          <Text style={[styles.buttonTextCentered, { color: c.textPrimary }]}>Sign in with a code</Text>
                        </View>
                      </Pressable>
                      {deviceCodeNote ? (
                        <Text style={[styles.deviceCodeNote, { color: c.textMuted }]}>{deviceCodeNote}</Text>
                      ) : null}
                    </>
                  )}
                  </View>
                )}

                {emailPasswordEnabled ? (
                  !showEmailForm ? (
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
                      <Pressable
                        testID="login-back-to-options"
                        accessibilityRole="button"
                        accessibilityLabel="Back to sign-in options"
                        onPress={() => {
                          setShowEmailForm(false);
                          setEmailError("");
                        }}
                        style={styles.emailBackButton}
                      >
                        <Ionicons name="chevron-back" size={17} color={c.textMuted} />
                        <Text style={[styles.emailBackText, { color: c.textMuted }]}>Sign-in options</Text>
                      </Pressable>
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
                          testID="login-email-input"
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
                          returnKeyType="next"
                          submitBehavior="submit"
                          onFocus={keepNextLoginControlVisible}
                          onSubmitEditing={focusPassword}
                        />
                        <TextInput
                          ref={passwordInputRef}
                          testID="login-password-input"
                          style={[
                            styles.input,
                            { backgroundColor: c.bgInput, borderColor: c.borderSubtle, color: c.textPrimary },
                          ]}
                          placeholder="Password"
                          placeholderTextColor={c.textMuted}
                          value={password}
                          onChangeText={setPassword}
                          secureTextEntry
                          returnKeyType={isSignUp ? "next" : "go"}
                          onFocus={keepNextLoginControlVisible}
                          onSubmitEditing={isSignUp ? focusConfirmPassword : handleEmailSubmit}
                        />
                        {isSignUp && (
                          <TextInput
                            ref={confirmPasswordInputRef}
                            style={[
                              styles.input,
                              { backgroundColor: c.bgInput, borderColor: c.borderSubtle, color: c.textPrimary },
                            ]}
                            placeholder="Confirm Password"
                            placeholderTextColor={c.textMuted}
                            value={confirmPassword}
                            onChangeText={setConfirmPassword}
                            secureTextEntry
                            returnKeyType="go"
                            onFocus={keepNextLoginControlVisible}
                            onSubmitEditing={handleEmailSubmit}
                          />
                        )}

                        {emailError ? (
                          <Text style={[styles.errorText, { color: c.error }]}>{emailError}</Text>
                        ) : null}

                        <Pressable
                          testID="login-email-submit"
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
                  )
                ) : null}
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
      </View>
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
  webWordmark: {
    fontSize: 64,
    lineHeight: 76,
    fontWeight: "900",
    marginBottom: 10,
    textAlign: "center",
  },
  webWordmarkTabletPortrait: {
    fontSize: 82,
    lineHeight: 94,
    marginBottom: 12,
  },
  webWordmarkTabletLandscape: {
    fontSize: 92,
    lineHeight: 104,
    marginBottom: 14,
    textAlign: "left",
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
  deviceCodePanel: {
    borderWidth: 1,
    borderRadius: 12,
    paddingVertical: 14,
    paddingHorizontal: 16,
    alignItems: "center",
    gap: 6,
  },
  deviceCodeLabel: {
    fontSize: 12,
  },
  deviceCodeValue: {
    fontSize: 30,
    fontWeight: "700",
    letterSpacing: 4,
    fontVariant: ["tabular-nums"],
  },
  deviceCodeNote: {
    fontSize: 12,
    textAlign: "center",
  },
  deviceCodeCancel: {
    fontSize: 13,
    marginTop: 2,
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
  emailBackButton: {
    alignSelf: "flex-start",
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
    minHeight: 44,
    paddingRight: 12,
  },
  emailBackText: {
    fontSize: 14,
    fontWeight: "600",
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
