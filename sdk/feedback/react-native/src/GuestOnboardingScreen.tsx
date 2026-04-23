import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ActivityIndicator,
  Pressable,
  SafeAreaView,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import {
  acceptGuestByCode,
  acceptGuestInvitation,
  fetchGuestHosts,
  findInviteByCode,
  type GuestHostsResponse,
  type InvitationPreview,
} from './auth';

export interface YaverGuestOnboardingScreenProps {
  token: string;
  initialInviteCode?: string;
  onContinue: () => void;
  onCancel?: () => void;
}

export const YaverGuestOnboardingScreen: React.FC<YaverGuestOnboardingScreenProps> = ({
  token,
  initialInviteCode,
  onContinue,
  onCancel,
}) => {
  const [code, setCode] = useState((initialInviteCode ?? '').toUpperCase());
  const [preview, setPreview] = useState<InvitationPreview | null>(null);
  const [hosts, setHosts] = useState<GuestHostsResponse>({ pending: [], active: [] });
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const cleanedCode = useMemo(() => code.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 6), [code]);

  const loadHosts = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await fetchGuestHosts(token);
      setHosts(result);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    void loadHosts();
  }, [loadHosts]);

  useEffect(() => {
    let cancelled = false;
    if (cleanedCode.length !== 6) {
      setPreview(null);
      return;
    }
    (async () => {
      try {
        const result = await findInviteByCode(token, cleanedCode);
        if (!cancelled) {
          setPreview(result);
          if (!result) {
            setError('Invite code not found or expired.');
          } else {
            setError(null);
          }
        }
      } catch (err) {
        if (!cancelled) {
          setPreview(null);
          setError(err instanceof Error ? err.message : String(err));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [cleanedCode, token]);

  const handleAcceptCode = useCallback(async () => {
    if (cleanedCode.length !== 6) {
      setError('Enter the 6-character invite code from the host.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await acceptGuestByCode(token, cleanedCode, preview?.proposedDeviceIds);
      await loadHosts();
      onContinue();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, [cleanedCode, loadHosts, onContinue, preview?.proposedDeviceIds, token]);

  const handleAcceptPending = useCallback(async (hostUserId: string) => {
    setBusy(true);
    setError(null);
    try {
      await acceptGuestInvitation(token, hostUserId);
      await loadHosts();
      onContinue();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, [loadHosts, onContinue, token]);

  return (
    <SafeAreaView style={styles.container}>
      <ScrollView contentContainerStyle={styles.content} keyboardShouldPersistTaps="handled">
        <View style={styles.header}>
          <View>
            <Text style={styles.title}>Guest Access</Text>
            <Text style={styles.subtitle}>
              Join a host&apos;s repo-scoped Feedback SDK access without leaving the app.
            </Text>
          </View>
          {onCancel && (
            <Pressable onPress={onCancel} style={styles.skipBtn}>
              <Text style={styles.skipText}>Skip</Text>
            </Pressable>
          )}
        </View>

        <View style={styles.card}>
          <Text style={styles.cardTitle}>Have an invite code?</Text>
          <Text style={styles.cardText}>
            Enter the 6-character code from the host. After redemption you can pick the shared machine directly here.
          </Text>
          <TextInput
            style={styles.input}
            value={cleanedCode}
            onChangeText={setCode}
            placeholder="ABC123"
            placeholderTextColor="#667085"
            autoCapitalize="characters"
            autoCorrect={false}
            maxLength={6}
          />
          {preview && (
            <View style={styles.previewBox}>
              <Text style={styles.previewTitle}>{preview.hostName}</Text>
              <Text style={styles.previewMeta}>{preview.hostEmail}</Text>
              <Text style={styles.previewMeta}>
                {preview.hostDevices.length > 0
                  ? `${preview.hostDevices.length} host machine${preview.hostDevices.length === 1 ? '' : 's'} shared`
                  : 'Host machine list will appear after acceptance'}
              </Text>
            </View>
          )}
          <Pressable
            onPress={() => void handleAcceptCode()}
            disabled={busy || cleanedCode.length !== 6}
            style={({ pressed }) => [
              styles.primaryBtn,
              (pressed || busy || cleanedCode.length !== 6) && styles.primaryBtnPressed,
            ]}
          >
            {busy ? <ActivityIndicator color="#fff" /> : <Text style={styles.primaryBtnText}>Redeem Code</Text>}
          </Pressable>
        </View>

        <View style={styles.card}>
          <Text style={styles.cardTitle}>Pending host invites</Text>
          <Text style={styles.cardText}>
            If a host invited your email directly, it should appear here after you sign up.
          </Text>
          {loading ? (
            <ActivityIndicator color="#98a2b3" style={{ marginTop: 12 }} />
          ) : hosts.pending.length > 0 ? (
            hosts.pending.map((invite) => (
              <View key={`${invite.hostUserId}:${invite.createdAt}`} style={styles.inviteRow}>
                <View style={{ flex: 1 }}>
                  <Text style={styles.inviteName}>{invite.hostName}</Text>
                  <Text style={styles.inviteMeta}>{invite.hostEmail}</Text>
                </View>
                <Pressable
                  onPress={() => void handleAcceptPending(invite.hostUserId)}
                  disabled={busy}
                  style={({ pressed }) => [
                    styles.secondaryBtn,
                    (pressed || busy) && styles.secondaryBtnPressed,
                  ]}
                >
                  <Text style={styles.secondaryBtnText}>Accept</Text>
                </Pressable>
              </View>
            ))
          ) : (
            <Text style={styles.emptyText}>No pending host invites on this account yet.</Text>
          )}
        </View>

        <View style={styles.cardMuted}>
          <Text style={styles.cardTitle}>No machine yet?</Text>
          <Text style={styles.cardText}>
            That is fine. You can still sign up here and redeem the host&apos;s guest access. Shared machines will appear once the host has one running. Later you can install Yaver on your own machine too.
          </Text>
        </View>

        {error ? <Text style={styles.error}>{error}</Text> : null}

        <Pressable
          onPress={onContinue}
          style={({ pressed }) => [styles.linkBtn, pressed && { opacity: 0.7 }]}
        >
          <Text style={styles.linkBtnText}>Continue to machine picker</Text>
        </Pressable>
      </ScrollView>
    </SafeAreaView>
  );
};

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#0f172a' },
  content: { padding: 20, gap: 16 },
  header: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'flex-start',
    marginBottom: 8,
  },
  title: { color: '#f8fafc', fontSize: 24, fontWeight: '700' },
  subtitle: { color: '#94a3b8', fontSize: 14, marginTop: 6, maxWidth: 280 },
  skipBtn: { paddingVertical: 6, paddingHorizontal: 10 },
  skipText: { color: '#cbd5e1', fontSize: 13, fontWeight: '600' },
  card: {
    backgroundColor: 'rgba(15,23,42,0.82)',
    borderWidth: 1,
    borderColor: 'rgba(148,163,184,0.18)',
    borderRadius: 16,
    padding: 16,
  },
  cardMuted: {
    backgroundColor: 'rgba(30,41,59,0.9)',
    borderRadius: 16,
    padding: 16,
  },
  cardTitle: { color: '#e2e8f0', fontSize: 16, fontWeight: '700' },
  cardText: { color: '#94a3b8', fontSize: 13, lineHeight: 20, marginTop: 8 },
  input: {
    marginTop: 14,
    borderWidth: 1,
    borderColor: 'rgba(148,163,184,0.28)',
    borderRadius: 12,
    paddingHorizontal: 14,
    paddingVertical: 12,
    backgroundColor: 'rgba(2,6,23,0.88)',
    color: '#f8fafc',
    fontSize: 18,
    letterSpacing: 3,
    textAlign: 'center',
  },
  previewBox: {
    marginTop: 12,
    padding: 12,
    borderRadius: 12,
    backgroundColor: 'rgba(30,41,59,0.75)',
  },
  previewTitle: { color: '#f8fafc', fontSize: 15, fontWeight: '600' },
  previewMeta: { color: '#94a3b8', fontSize: 12, marginTop: 4 },
  primaryBtn: {
    marginTop: 14,
    backgroundColor: '#2563eb',
    borderRadius: 12,
    minHeight: 46,
    alignItems: 'center',
    justifyContent: 'center',
  },
  primaryBtnPressed: { opacity: 0.6 },
  primaryBtnText: { color: '#fff', fontSize: 15, fontWeight: '700' },
  inviteRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
    marginTop: 12,
    paddingTop: 12,
    borderTopWidth: 1,
    borderTopColor: 'rgba(148,163,184,0.12)',
  },
  inviteName: { color: '#f8fafc', fontSize: 14, fontWeight: '600' },
  inviteMeta: { color: '#94a3b8', fontSize: 12, marginTop: 4 },
  secondaryBtn: {
    paddingHorizontal: 12,
    paddingVertical: 10,
    borderRadius: 10,
    backgroundColor: 'rgba(37,99,235,0.16)',
  },
  secondaryBtnPressed: { opacity: 0.6 },
  secondaryBtnText: { color: '#bfdbfe', fontSize: 13, fontWeight: '700' },
  emptyText: { color: '#64748b', fontSize: 13, marginTop: 12 },
  error: { color: '#f87171', fontSize: 13, marginTop: 4 },
  linkBtn: { alignItems: 'center', paddingVertical: 8 },
  linkBtnText: { color: '#cbd5e1', fontSize: 14, fontWeight: '600' },
});
