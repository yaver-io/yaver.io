import React, { useEffect, useState } from 'react';
import {
  ActivityIndicator,
  Modal,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import type { RemoteDevice } from './auth';
import { getConvexSiteUrl, getToken } from './auth';

/**
 * In-SDK remote-pair modal. Shown when the user taps a device in the
 * machine picker that's in `needsAuth` state.
 *
 * Flow:
 *   1. User types the 6-char bootstrap passkey printed in the
 *      `yaver serve` terminal on the Mac (also shown in Yaver mobile
 *      app when pairing interactively).
 *   2. SDK POSTs to `http://<device.quicHost>:<device.httpPort>/auth/pair/submit?code=XXXXXX`
 *      with the user's Convex session token (same one the SDK already
 *      has after Apple / Google / email sign-in).
 *   3. Agent validates the token against Convex, persists it, flips
 *      out of bootstrap mode — within a couple of seconds it'll
 *      report `needsAuth=false` in /devices/list.
 *
 * This avoids making the user bounce to the Yaver mobile app just to
 * adopt a machine. Works for owners and shared-scope guests since the
 * pair endpoint accepts any valid Convex session that matches the
 * expected account type.
 */
export interface PairDeviceModalProps {
  device: RemoteDevice | null;
  onClose: () => void;
  onPaired?: (device: RemoteDevice) => void;
}

export const PairDeviceModal: React.FC<PairDeviceModalProps> = ({
  device,
  onClose,
  onPaired,
}) => {
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  useEffect(() => {
    if (device) {
      setCode('');
      setError(null);
      setSuccess(false);
    }
  }, [device?.deviceId]);

  const handleSubmit = async () => {
    if (!device) return;
    const trimmed = code.trim().toUpperCase();
    if (trimmed.length !== 6) {
      setError('Code must be 6 characters.');
      return;
    }
    const token = await getToken();
    if (!token) {
      setError('Not signed in.');
      return;
    }
    const host = (device.quicHost || '').trim();
    const port = device.httpPort || device.quicPort || 18080;
    if (!host) {
      setError('No reachable address for this machine.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const url = `http://${host}:${port}/auth/pair/submit?code=${encodeURIComponent(
        trimmed,
      )}`;
      const res = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          token,
          convexSiteUrl: getConvexSiteUrl(),
          // Backend reads userId from the session, but older agents
          // expect it in the body. Pass empty string if unknown.
          userId: '',
        }),
      });
      if (!res.ok) {
        let msg = `Agent rejected pair (HTTP ${res.status}).`;
        try {
          const body = await res.json();
          if (body?.error) msg = String(body.error);
        } catch {
          // body not JSON
        }
        throw new Error(msg);
      }
      setSuccess(true);
      onPaired?.(device);
      setTimeout(() => {
        onClose();
      }, 1200);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      visible={!!device}
      animationType="slide"
      transparent
      onRequestClose={onClose}
    >
      <Pressable style={styles.overlay} onPress={onClose}>
        <Pressable style={styles.card} onPress={(e) => e.stopPropagation()}>
          <View style={styles.header}>
            <Text style={styles.title}>Pair this Mac</Text>
            <Pressable onPress={onClose} hitSlop={12} style={styles.closeBtn}>
              <Text style={styles.closeIcon}>×</Text>
            </Pressable>
          </View>

          <Text style={styles.deviceName}>{device?.name || device?.deviceId}</Text>
          <Text style={styles.body}>
            On the Mac where `yaver serve` is running, look for the 6-character code in the terminal output. Enter it here to adopt this machine.
          </Text>

          <TextInput
            style={styles.codeInput}
            value={code}
            onChangeText={(v) => setCode(v.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 6))}
            placeholder="ABCDEF"
            placeholderTextColor="#555"
            autoCapitalize="characters"
            autoCorrect={false}
            maxLength={6}
            keyboardType={Platform.OS === 'ios' ? 'ascii-capable' : 'visible-password'}
          />

          {error && <Text style={styles.error}>{error}</Text>}
          {success && <Text style={styles.success}>Paired ✓</Text>}

          <Pressable
            onPress={handleSubmit}
            disabled={busy || success || code.length !== 6}
            style={({ pressed }) => [
              styles.submit,
              (busy || success || code.length !== 6) && styles.submitDisabled,
              pressed && { opacity: 0.7 },
            ]}
          >
            {busy ? (
              <ActivityIndicator color="#fff" />
            ) : (
              <Text style={styles.submitText}>{success ? 'Paired' : 'Pair'}</Text>
            )}
          </Pressable>
        </Pressable>
      </Pressable>
    </Modal>
  );
};

const styles = StyleSheet.create({
  overlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.55)',
    justifyContent: 'flex-end',
  },
  card: {
    backgroundColor: '#141422',
    borderTopLeftRadius: 22,
    borderTopRightRadius: 22,
    padding: 24,
    paddingBottom: 36,
    gap: 14,
  },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  title: { fontSize: 20, fontWeight: '700', color: '#fff' },
  closeBtn: {
    width: 36,
    height: 36,
    borderRadius: 18,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: 'rgba(255,255,255,0.08)',
  },
  closeIcon: { color: '#fff', fontSize: 22, lineHeight: 24 },
  deviceName: { fontSize: 15, fontWeight: '600', color: '#c7c8ff' },
  body: { fontSize: 13, color: '#9ca3af', lineHeight: 18 },
  codeInput: {
    backgroundColor: 'rgba(255,255,255,0.06)',
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,0.14)',
    borderRadius: 12,
    paddingHorizontal: 16,
    paddingVertical: 16,
    fontSize: 22,
    fontWeight: '700',
    letterSpacing: 4,
    textAlign: 'center',
    color: '#fff',
    fontFamily: Platform.OS === 'ios' ? 'Menlo' : 'monospace',
  },
  error: { color: '#ef4444', fontSize: 13 },
  success: { color: '#22c55e', fontSize: 14, fontWeight: '600' },
  submit: {
    backgroundColor: '#818cf8',
    borderRadius: 12,
    paddingVertical: 14,
    alignItems: 'center',
  },
  submitDisabled: { opacity: 0.35 },
  submitText: { color: '#fff', fontSize: 16, fontWeight: '700' },
});
