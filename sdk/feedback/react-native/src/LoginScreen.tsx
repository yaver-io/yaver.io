import React, { useEffect, useRef, useState } from 'react';
import {
  View,
  Text,
  TextInput,
  TouchableOpacity,
  StyleSheet,
  ActivityIndicator,
  SafeAreaView,
  ScrollView,
  Linking,
  Platform,
} from 'react-native';
import {
  loginWithEmail,
  signupWithEmail,
  pollDeviceCode,
  startDeviceCode,
  validateToken,
  saveToken,
  saveUser,
  OAuthProvider,
  DeviceCodeStart,
} from './auth';

export interface YaverLoginScreenProps {
  /** Invoked once a session token is issued and the user is loaded. */
  onLoggedIn: (token: string) => void;
  /** Optional cancel button shown in header. */
  onCancel?: () => void;
}

type Mode = 'device' | 'email';

const PROVIDERS: { id: OAuthProvider; label: string; emoji: string }[] = [
  { id: 'apple', label: 'Apple', emoji: '' },
  { id: 'google', label: 'Google', emoji: 'G' },
  { id: 'github', label: 'GitHub', emoji: '' },
  { id: 'gitlab', label: 'GitLab', emoji: '' },
  { id: 'microsoft', label: 'Microsoft', emoji: 'M' },
];

/**
 * Full-screen in-SDK login. Device-code is the default flow — users sign in
 * with any OAuth provider (Apple/Google/GitHub/GitLab/Microsoft) or email on
 * yaver.io and the SDK polls for the issued session token. Email/password is
 * available as an inline fallback for headless environments.
 */
export const YaverLoginScreen: React.FC<YaverLoginScreenProps> = ({
  onLoggedIn,
  onCancel,
}) => {
  const [mode, setMode] = useState<Mode>('device');

  const [code, setCode] = useState<DeviceCodeStart | null>(null);
  const [codeError, setCodeError] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const expiredTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const [emailMode, setEmailMode] = useState<'login' | 'signup'>('login');
  const [fullName, setFullName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [emailBusy, setEmailBusy] = useState(false);
  const [emailError, setEmailError] = useState<string | null>(null);

  useEffect(() => {
    if (mode === 'device' && !code && !starting) {
      void beginDeviceCode(undefined);
    }
    return () => {
      stopPolling();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode]);

  const stopPolling = () => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
    if (expiredTimerRef.current) {
      clearTimeout(expiredTimerRef.current);
      expiredTimerRef.current = null;
    }
  };

  const beginDeviceCode = async (preferredProvider?: OAuthProvider) => {
    setStarting(true);
    setCodeError(null);
    stopPolling();
    try {
      const result = await startDeviceCode({
        platform: Platform.OS,
        machineName: `feedback-sdk-${Platform.OS}`,
        preferredProvider,
      });
      setCode(result);

      pollRef.current = setInterval(async () => {
        const poll = await pollDeviceCode(result.deviceCode);
        if (poll.status === 'authorized') {
          stopPolling();
          const user = await validateToken(poll.token);
          await saveToken(poll.token);
          if (user) await saveUser(user);
          onLoggedIn(poll.token);
        } else if (poll.status === 'expired') {
          stopPolling();
          setCodeError('Kod doldu — tekrar başlat.');
          setCode(null);
        }
      }, 3_000);

      expiredTimerRef.current = setTimeout(() => {
        stopPolling();
        setCode(null);
        setCodeError('Kod süresi doldu.');
      }, Math.max(0, result.expiresAt - Date.now()));
    } catch (err) {
      setCodeError(err instanceof Error ? err.message : String(err));
    } finally {
      setStarting(false);
    }
  };

  const openVerification = () => {
    if (!code) return;
    Linking.openURL(code.verificationUrl).catch(() => {
      setCodeError('Tarayıcı açılamadı — URL’yi elle aç.');
    });
  };

  const handleEmailSubmit = async () => {
    setEmailError(null);
    if (!email.trim() || !password) {
      setEmailError('E-posta ve parola zorunlu.');
      return;
    }
    setEmailBusy(true);
    try {
      const result =
        emailMode === 'signup'
          ? await signupWithEmail(fullName.trim() || email.trim(), email.trim(), password)
          : await loginWithEmail(email.trim(), password);
      const user = await validateToken(result.token);
      await saveToken(result.token);
      if (user) await saveUser(user);
      onLoggedIn(result.token);
    } catch (err) {
      setEmailError(err instanceof Error ? err.message : String(err));
    } finally {
      setEmailBusy(false);
    }
  };

  return (
    <SafeAreaView style={styles.container}>
      <ScrollView
        contentContainerStyle={styles.content}
        keyboardShouldPersistTaps="handled"
      >
        <View style={styles.header}>
          <Text style={styles.title}>Yaver Girişi</Text>
          {onCancel && (
            <TouchableOpacity onPress={onCancel} style={styles.cancel}>
              <Text style={styles.cancelText}>İptal</Text>
            </TouchableOpacity>
          )}
        </View>

        <View style={styles.tabRow}>
          <TouchableOpacity
            style={[styles.tab, mode === 'device' && styles.tabActive]}
            onPress={() => setMode('device')}
          >
            <Text
              style={[styles.tabText, mode === 'device' && styles.tabTextActive]}
            >
              Hızlı Giriş (OAuth)
            </Text>
          </TouchableOpacity>
          <TouchableOpacity
            style={[styles.tab, mode === 'email' && styles.tabActive]}
            onPress={() => setMode('email')}
          >
            <Text
              style={[styles.tabText, mode === 'email' && styles.tabTextActive]}
            >
              E-posta
            </Text>
          </TouchableOpacity>
        </View>

        {mode === 'device' && (
          <View style={styles.section}>
            {starting ? (
              <ActivityIndicator color="#6366f1" style={{ marginVertical: 40 }} />
            ) : code ? (
              <>
                <Text style={styles.hint}>
                  Tarayıcıda yaver.io/auth/device aç ve aşağıdaki kodu gir.
                  Sağlayıcıyla giriş yaptığında buraya otomatik dönecek.
                </Text>
                <View style={styles.codeBox}>
                  <Text style={styles.codeText}>{code.userCode}</Text>
                </View>
                <TouchableOpacity style={styles.primaryButton} onPress={openVerification}>
                  <Text style={styles.primaryButtonText}>Tarayıcıda Aç</Text>
                </TouchableOpacity>

                <Text style={[styles.hint, { marginTop: 20 }]}>
                  Sağlayıcıyı önceden seçmek istersen:
                </Text>
                <View style={styles.providerRow}>
                  {PROVIDERS.map((p) => (
                    <TouchableOpacity
                      key={p.id}
                      style={styles.providerButton}
                      onPress={() => beginDeviceCode(p.id)}
                    >
                      <Text style={styles.providerButtonText}>{p.label}</Text>
                    </TouchableOpacity>
                  ))}
                </View>
              </>
            ) : (
              <TouchableOpacity
                style={styles.primaryButton}
                onPress={() => beginDeviceCode(undefined)}
              >
                <Text style={styles.primaryButtonText}>Tekrar Başlat</Text>
              </TouchableOpacity>
            )}
            {codeError && <Text style={styles.error}>{codeError}</Text>}
          </View>
        )}

        {mode === 'email' && (
          <View style={styles.section}>
            <View style={styles.tabRow}>
              <TouchableOpacity
                style={[styles.subTab, emailMode === 'login' && styles.subTabActive]}
                onPress={() => setEmailMode('login')}
              >
                <Text style={styles.subTabText}>Giriş</Text>
              </TouchableOpacity>
              <TouchableOpacity
                style={[styles.subTab, emailMode === 'signup' && styles.subTabActive]}
                onPress={() => setEmailMode('signup')}
              >
                <Text style={styles.subTabText}>Kayıt</Text>
              </TouchableOpacity>
            </View>

            {emailMode === 'signup' && (
              <>
                <Text style={styles.label}>Ad Soyad</Text>
                <TextInput
                  style={styles.input}
                  value={fullName}
                  onChangeText={setFullName}
                  placeholder="Adın Soyadın"
                  placeholderTextColor="#666"
                  autoCapitalize="words"
                />
              </>
            )}

            <Text style={styles.label}>E-posta</Text>
            <TextInput
              style={styles.input}
              value={email}
              onChangeText={setEmail}
              placeholder="you@example.com"
              placeholderTextColor="#666"
              keyboardType="email-address"
              autoCapitalize="none"
              autoCorrect={false}
            />

            <Text style={styles.label}>Parola</Text>
            <TextInput
              style={styles.input}
              value={password}
              onChangeText={setPassword}
              placeholder="••••••••"
              placeholderTextColor="#666"
              secureTextEntry
              autoCapitalize="none"
            />

            <TouchableOpacity
              style={styles.primaryButton}
              onPress={handleEmailSubmit}
              disabled={emailBusy}
            >
              {emailBusy ? (
                <ActivityIndicator color="#fff" />
              ) : (
                <Text style={styles.primaryButtonText}>
                  {emailMode === 'signup' ? 'Kayıt Ol' : 'Giriş Yap'}
                </Text>
              )}
            </TouchableOpacity>

            {emailError && <Text style={styles.error}>{emailError}</Text>}
          </View>
        )}
      </ScrollView>
    </SafeAreaView>
  );
};

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#1a1a2e' },
  content: { padding: 24, paddingTop: 16 },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: 20,
  },
  title: { fontSize: 22, fontWeight: '700', color: '#e0e0e0' },
  cancel: { padding: 8 },
  cancelText: { color: '#9ca3af', fontSize: 14 },
  tabRow: { flexDirection: 'row', gap: 8, marginBottom: 16 },
  tab: {
    flex: 1,
    paddingVertical: 12,
    borderRadius: 10,
    backgroundColor: 'rgba(255,255,255,0.05)',
    alignItems: 'center',
  },
  tabActive: { backgroundColor: 'rgba(99,102,241,0.25)' },
  tabText: { color: '#9ca3af', fontSize: 14, fontWeight: '600' },
  tabTextActive: { color: '#e0e0e0' },
  subTab: {
    flex: 1,
    paddingVertical: 8,
    borderRadius: 8,
    alignItems: 'center',
    backgroundColor: 'rgba(255,255,255,0.05)',
  },
  subTabActive: { backgroundColor: 'rgba(99,102,241,0.2)' },
  subTabText: { color: '#e0e0e0', fontSize: 13 },
  section: { marginTop: 4 },
  hint: { color: '#9ca3af', fontSize: 13, lineHeight: 18 },
  codeBox: {
    backgroundColor: 'rgba(99,102,241,0.15)',
    borderWidth: 1,
    borderColor: 'rgba(99,102,241,0.35)',
    borderRadius: 14,
    paddingVertical: 24,
    alignItems: 'center',
    marginTop: 16,
    marginBottom: 16,
  },
  codeText: {
    color: '#e0e7ff',
    fontSize: 36,
    fontWeight: '800',
    letterSpacing: 6,
    fontVariant: ['tabular-nums'],
  },
  primaryButton: {
    backgroundColor: '#6366f1',
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: 'center',
    marginTop: 8,
  },
  primaryButtonText: { color: '#fff', fontWeight: '700', fontSize: 15 },
  providerRow: { flexDirection: 'row', flexWrap: 'wrap', gap: 8, marginTop: 10 },
  providerButton: {
    backgroundColor: 'rgba(255,255,255,0.08)',
    borderRadius: 10,
    paddingVertical: 10,
    paddingHorizontal: 14,
  },
  providerButtonText: { color: '#e0e0e0', fontSize: 13, fontWeight: '600' },
  label: { color: '#9ca3af', fontSize: 12, marginTop: 14, marginBottom: 6 },
  input: {
    backgroundColor: 'rgba(255,255,255,0.08)',
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,0.15)',
    borderRadius: 10,
    paddingHorizontal: 14,
    paddingVertical: 12,
    color: '#e0e0e0',
    fontSize: 15,
  },
  error: { color: '#ef4444', fontSize: 13, marginTop: 12 },
});
