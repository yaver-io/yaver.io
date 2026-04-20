import React, { useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  SafeAreaView,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import {
  loginWithEmail,
  saveToken,
  saveUser,
  signInWithApple,
  signInWithOAuth,
  signupWithEmail,
  validateToken,
  type OAuthProvider,
} from './auth';

// ── ProviderLogo ─────────────────────────────────────────────────────
//
// Mirrors the Yaver mobile app's login screen — Apple, Google, GitHub,
// GitLab, Microsoft each get their brand mark next to the label.
// Mobile uses `Ionicons` from `@expo/vector-icons`. We soft-require
// the same package so apps that have it (every Expo app does) get
// real logos automatically; bare-RN apps without the package fall
// back to a single-letter monogram in a tinted circle.
//
// Each entry holds: Ionicons name, fallback letter, brand colour,
// optional Apple-on-light invert (Apple's wordmark needs the
// alternate "logo-apple-appstore" / dark variant on dark backgrounds).
type ProviderTheme = {
  ion: string;
  letter: string;
  fg: string;
  bg: string;
};

const PROVIDER_THEME: Record<OAuthProvider | 'apple', ProviderTheme> = {
  apple:     { ion: 'logo-apple',     letter: '', fg: '#FFFFFF', bg: 'transparent' },
  google:    { ion: 'logo-google',    letter: 'G', fg: '#EA4335', bg: 'transparent' },
  github:    { ion: 'logo-github',    letter: 'G', fg: '#FFFFFF', bg: 'transparent' },
  gitlab:    { ion: 'logo-gitlab',    letter: 'G', fg: '#FC6D26', bg: 'transparent' },
  microsoft: { ion: 'logo-microsoft', letter: 'M', fg: '#00A4EF', bg: 'transparent' },
};

const ProviderLogo: React.FC<{ provider: OAuthProvider | 'apple' }> = ({
  provider,
}) => {
  const theme = PROVIDER_THEME[provider] ?? PROVIDER_THEME.apple;
  // Soft-require @expo/vector-icons so the SDK works in bare-RN apps
  // that don't ship Expo. If present, render the brand glyph; if
  // not, render a coloured monogram fallback.
  let Ionicons: React.ComponentType<{
    name: string;
    size?: number;
    color?: string;
    style?: object;
  }> | null = null;
  try {
    const mod = require('@expo/vector-icons');
    Ionicons = mod?.Ionicons ?? null;
  } catch {
    // package not installed — use letter fallback
  }
  if (Ionicons) {
    return (
      <Ionicons
        name={theme.ion}
        size={18}
        color={theme.fg}
        style={iconStyles.icon}
      />
    );
  }
  if (!theme.letter) {
    return null;
  }
  return (
    <View style={[iconStyles.fallback, { backgroundColor: theme.fg + '22' }]}>
      <Text style={[iconStyles.fallbackText, { color: theme.fg }]}>{theme.letter}</Text>
    </View>
  );
};

const iconStyles = StyleSheet.create({
  icon: { marginRight: 12 },
  fallback: {
    width: 22,
    height: 22,
    borderRadius: 11,
    alignItems: 'center',
    justifyContent: 'center',
    marginRight: 12,
  },
  fallbackText: {
    fontSize: 12,
    fontWeight: '700',
  },
});

export interface YaverLoginScreenProps {
  /** Invoked once a session token is issued and the user is loaded. */
  onLoggedIn: (token: string) => void;
  /** Optional cancel button shown in header. */
  onCancel?: () => void;
}

/**
 * Full-screen in-SDK login. Mirrors the Yaver mobile app login UX: native
 * Apple Sign-In on iOS, in-app browser OAuth for Google/GitHub/GitLab/
 * Microsoft (no codes, no leaving the app), and inline email/password.
 */
export const YaverLoginScreen: React.FC<YaverLoginScreenProps> = ({
  onLoggedIn,
  onCancel,
}) => {
  const [busyProvider, setBusyProvider] = useState<OAuthProvider | 'apple' | null>(null);
  const [showEmailForm, setShowEmailForm] = useState(false);
  const [isSignUp, setIsSignUp] = useState(false);
  const [fullName, setFullName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [emailBusy, setEmailBusy] = useState(false);
  const [emailError, setEmailError] = useState('');

  const finish = async (token: string) => {
    const user = await validateToken(token);
    await saveToken(token);
    if (user) await saveUser(user);
    onLoggedIn(token);
  };

  const handleApple = async () => {
    setBusyProvider('apple');
    try {
      const { token } = await signInWithApple();
      await finish(token);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Apple Sign-In failed';
      if (msg !== 'cancelled') Alert.alert('Sign In Failed', msg);
    } finally {
      setBusyProvider(null);
    }
  };

  const handleOAuth = async (provider: OAuthProvider) => {
    setBusyProvider(provider);
    try {
      const { token } = await signInWithOAuth(provider);
      await finish(token);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'Sign-in failed';
      if (msg !== 'cancelled') Alert.alert('Sign In Failed', msg);
    } finally {
      setBusyProvider(null);
    }
  };

  const handleEmailSubmit = async () => {
    setEmailError('');
    if (isSignUp) {
      if (!fullName.trim()) return setEmailError('Full name is required');
      if (password !== confirmPassword)
        return setEmailError('Passwords do not match');
      if (password.length < 8)
        return setEmailError('Password must be at least 8 characters');
    }
    if (!email.trim() || !password)
      return setEmailError('Email and password are required');

    setEmailBusy(true);
    try {
      const result = isSignUp
        ? await signupWithEmail(fullName.trim(), email.trim(), password)
        : await loginWithEmail(email.trim(), password);
      await finish(result.token);
    } catch (e: unknown) {
      setEmailError(e instanceof Error ? e.message : 'Something went wrong');
    } finally {
      setEmailBusy(false);
    }
  };

  const renderProvider = (
    id: OAuthProvider | 'apple',
    label: string,
    onPress: () => void,
  ) => (
    <Pressable
      key={id}
      style={({ pressed }) => [
        styles.button,
        pressed && styles.buttonPressed,
        busyProvider === id && { opacity: 0.6 },
      ]}
      onPress={onPress}
      disabled={busyProvider !== null}
    >
      {busyProvider === id ? (
        <ActivityIndicator color="#e0e0e0" />
      ) : (
        <View style={styles.buttonContent}>
          <ProviderLogo provider={id} />
          <Text style={[styles.buttonText, styles.buttonTextWithIcon]}>{label}</Text>
        </View>
      )}
    </Pressable>
  );

  return (
    <SafeAreaView style={styles.safeArea}>
      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      >
        <ScrollView
          contentContainerStyle={styles.scrollContainer}
          keyboardShouldPersistTaps="handled"
        >
          <View style={styles.header}>
            <Text style={styles.logo}>Yaver</Text>
            <Text style={styles.subtitle}>Sign in to send feedback</Text>
            {onCancel && (
              <Pressable onPress={onCancel} style={styles.cancel}>
                <Text style={styles.cancelText}>Cancel</Text>
              </Pressable>
            )}
          </View>

          <View style={styles.buttons}>
            {Platform.OS === 'ios'
              ? renderProvider('apple', 'Continue with Apple', handleApple)
              : renderProvider('apple', 'Continue with Apple', () =>
                  handleOAuth('apple'),
                )}
            {renderProvider('google', 'Continue with Google', () =>
              handleOAuth('google'),
            )}
            {renderProvider('github', 'Continue with GitHub', () =>
              handleOAuth('github'),
            )}
            {renderProvider('gitlab', 'Continue with GitLab', () =>
              handleOAuth('gitlab'),
            )}
            {renderProvider('microsoft', 'Continue with Microsoft', () =>
              handleOAuth('microsoft'),
            )}

            {!showEmailForm ? (
              <Pressable
                style={({ pressed }) => [
                  styles.button,
                  pressed && styles.buttonPressed,
                ]}
                onPress={() => setShowEmailForm(true)}
                disabled={busyProvider !== null}
              >
                <Text style={styles.buttonText}>Continue with Email</Text>
              </Pressable>
            ) : (
              <>
                <View style={styles.divider}>
                  <View style={styles.dividerLine} />
                  <Text style={styles.dividerText}>email</Text>
                  <View style={styles.dividerLine} />
                </View>
                {isSignUp && (
                  <TextInput
                    style={styles.input}
                    placeholder="Full Name"
                    placeholderTextColor="#666"
                    value={fullName}
                    onChangeText={setFullName}
                    autoCapitalize="words"
                    autoCorrect={false}
                  />
                )}
                <TextInput
                  style={styles.input}
                  placeholder="Email"
                  placeholderTextColor="#666"
                  value={email}
                  onChangeText={setEmail}
                  keyboardType="email-address"
                  autoCapitalize="none"
                  autoCorrect={false}
                />
                <TextInput
                  style={styles.input}
                  placeholder="Password"
                  placeholderTextColor="#666"
                  value={password}
                  onChangeText={setPassword}
                  secureTextEntry
                  autoCapitalize="none"
                />
                {isSignUp && (
                  <TextInput
                    style={styles.input}
                    placeholder="Confirm Password"
                    placeholderTextColor="#666"
                    value={confirmPassword}
                    onChangeText={setConfirmPassword}
                    secureTextEntry
                  />
                )}

                {emailError ? (
                  <Text style={styles.errorText}>{emailError}</Text>
                ) : null}

                <Pressable
                  style={({ pressed }) => [
                    styles.submitButton,
                    pressed && styles.buttonPressed,
                    emailBusy && { opacity: 0.6 },
                  ]}
                  onPress={handleEmailSubmit}
                  disabled={emailBusy}
                >
                  {emailBusy ? (
                    <ActivityIndicator color="#fff" />
                  ) : (
                    <Text style={styles.submitButtonText}>
                      {isSignUp ? 'Create Account' : 'Sign In'}
                    </Text>
                  )}
                </Pressable>

                <Pressable
                  onPress={() => {
                    setIsSignUp(!isSignUp);
                    setEmailError('');
                  }}
                >
                  <Text style={styles.toggleText}>
                    {isSignUp
                      ? 'Already have an account? Sign In'
                      : "Don't have an account? Sign Up"}
                  </Text>
                </Pressable>
              </>
            )}
          </View>
        </ScrollView>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
};

const styles = StyleSheet.create({
  safeArea: { flex: 1, backgroundColor: '#1a1a2e' },
  scrollContainer: {
    flexGrow: 1,
    paddingHorizontal: 24,
    justifyContent: 'center',
  },
  header: { alignItems: 'center', marginBottom: 40 },
  logo: { fontSize: 44, fontWeight: '800', color: '#e0e0e0', letterSpacing: -1 },
  subtitle: { fontSize: 15, color: '#9ca3af', marginTop: 6 },
  cancel: { position: 'absolute', right: 0, top: 0, padding: 8 },
  cancelText: { color: '#9ca3af', fontSize: 14 },
  buttons: { gap: 12 },
  button: {
    backgroundColor: 'rgba(255,255,255,0.06)',
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,0.12)',
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: 'center',
    justifyContent: 'center',
  },
  buttonPressed: { opacity: 0.7 },
  buttonText: { color: '#e0e0e0', fontSize: 15, fontWeight: '600' },
  buttonContent: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
  },
  buttonTextWithIcon: {
    // No extra spacing — ProviderLogo provides its own marginRight.
  },
  divider: {
    flexDirection: 'row',
    alignItems: 'center',
    marginTop: 16,
    marginBottom: 8,
  },
  dividerLine: { flex: 1, height: 1, backgroundColor: 'rgba(255,255,255,0.12)' },
  dividerText: { marginHorizontal: 14, fontSize: 12, color: '#6b7280' },
  input: {
    backgroundColor: 'rgba(255,255,255,0.06)',
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,0.12)',
    borderRadius: 12,
    paddingHorizontal: 14,
    paddingVertical: 13,
    color: '#e0e0e0',
    fontSize: 15,
  },
  errorText: { color: '#ef4444', fontSize: 13, textAlign: 'center' },
  submitButton: {
    backgroundColor: '#6366f1',
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: 'center',
    justifyContent: 'center',
    marginTop: 4,
  },
  submitButtonText: { color: '#fff', fontSize: 15, fontWeight: '700' },
  toggleText: {
    color: '#818cf8',
    fontSize: 14,
    textAlign: 'center',
    marginTop: 4,
  },
});
