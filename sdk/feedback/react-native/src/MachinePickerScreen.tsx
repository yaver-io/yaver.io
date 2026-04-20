import React, { useCallback, useEffect, useState } from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  ActivityIndicator,
  SafeAreaView,
  ScrollView,
  RefreshControl,
} from 'react-native';
import {
  DeviceList,
  RemoteDevice,
  listReachableDevices,
  saveSelectedDeviceId,
} from './auth';

export interface YaverMachinePickerProps {
  token: string;
  /** Currently-selected deviceId (from config / cache) — highlighted. */
  currentDeviceId?: string;
  onPick: (device: RemoteDevice) => void;
  onCancel?: () => void;
}

/**
 * List of remote dev machines the signed-in user can reach. Split into
 *   - Owned machines (user is the host)
 *   - Shared machines (host invited them as a guest)
 *
 * Tapping a device persists it to AsyncStorage and invokes `onPick`. The
 * SDK then uses that device's deviceId for agent discovery (LAN probe +
 * relay fallback through Convex).
 */
export const YaverMachinePickerScreen: React.FC<YaverMachinePickerProps> = ({
  token,
  currentDeviceId,
  onPick,
  onCancel,
}) => {
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [list, setList] = useState<DeviceList>({ owned: [], shared: [] });

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    setError(null);
    try {
      const result = await listReachableDevices(token);
      setList(result);
      if (result.owned.length === 0 && result.shared.length === 0) {
        setError('Hiç makine bulunamadı — önce bir makinede `yaver auth` + `yaver serve` çalıştır.');
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [token]);

  useEffect(() => {
    void load();
  }, [load]);

  const handlePick = async (device: RemoteDevice) => {
    await saveSelectedDeviceId(device.deviceId);
    onPick(device);
  };

  const renderDevice = (device: RemoteDevice) => {
    const selected = device.deviceId === currentDeviceId;
    // Trust Convex's `isOnline` — the backend already gates it on a
    // fresh 90 s heartbeat (see backend/convex/devices.ts
    // deriveIsOnline). Re-checking on the client produced false
    // yellows from phone↔backend clock skew around the 89-90 s mark.
    //
    // `runnerDown` intentionally does NOT flip the dot. That flag
    // tracks whether the AI runner (claude-code, aider, ...) is
    // healthy — a separate concern from "can I reach this machine?"
    // Mobile app surfaces runner issues via a separate badge, not
    // this dot. Picker's job is reachability, nothing more.
    const healthColor = !device.isOnline
      ? '#ef4444'
      : device.needsAuth
        ? '#f59e0b'
        : '#22c55e';
    return (
      <TouchableOpacity
        key={device.deviceId}
        style={[styles.deviceRow, selected && styles.deviceSelected]}
        onPress={() => handlePick(device)}
      >
        <View style={[styles.health, { backgroundColor: healthColor }]} />
        <View style={{ flex: 1 }}>
          <Text style={styles.deviceName}>{device.name || device.deviceId}</Text>
          <Text style={styles.deviceMeta}>
            {device.platform}
            {device.isGuest && device.hostEmail ? `  •  ${device.hostEmail}` : ''}
            {device.accessScope === 'shared-scoped' ? '  •  paylaşılan' : ''}
          </Text>
        </View>
        {selected && <Text style={styles.selectedBadge}>seçili</Text>}
      </TouchableOpacity>
    );
  };

  return (
    <SafeAreaView style={styles.container}>
      <View style={styles.header}>
        <Text style={styles.title}>Makine Seç</Text>
        {onCancel && (
          <TouchableOpacity onPress={onCancel} style={styles.cancel}>
            <Text style={styles.cancelText}>Kapat</Text>
          </TouchableOpacity>
        )}
      </View>

      <ScrollView
        contentContainerStyle={styles.content}
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            onRefresh={() => {
              setRefreshing(true);
              void load(true);
            }}
            tintColor="#6366f1"
          />
        }
      >
        {loading ? (
          <ActivityIndicator color="#6366f1" style={{ marginTop: 60 }} />
        ) : (
          <>
            {list.owned.length > 0 && (
              <View style={styles.section}>
                <Text style={styles.sectionTitle}>Kendi makinelerim</Text>
                {list.owned.map(renderDevice)}
              </View>
            )}
            {list.shared.length > 0 && (
              <View style={styles.section}>
                <Text style={styles.sectionTitle}>Paylaşılan (guest)</Text>
                {list.shared.map(renderDevice)}
              </View>
            )}
            {error && <Text style={styles.error}>{error}</Text>}
          </>
        )}
      </ScrollView>
    </SafeAreaView>
  );
};

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: '#1a1a2e' },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 20,
    paddingVertical: 16,
  },
  title: { color: '#e0e0e0', fontSize: 20, fontWeight: '700' },
  cancel: { padding: 8 },
  cancelText: { color: '#9ca3af', fontSize: 14 },
  content: { padding: 20, paddingTop: 0 },
  section: { marginBottom: 24 },
  sectionTitle: {
    color: '#9ca3af',
    fontSize: 12,
    fontWeight: '600',
    textTransform: 'uppercase',
    letterSpacing: 1,
    marginBottom: 10,
  },
  deviceRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
    padding: 14,
    backgroundColor: 'rgba(255,255,255,0.05)',
    borderRadius: 12,
    marginBottom: 8,
    borderWidth: 1,
    borderColor: 'transparent',
  },
  deviceSelected: {
    borderColor: 'rgba(99,102,241,0.5)',
    backgroundColor: 'rgba(99,102,241,0.15)',
  },
  health: { width: 10, height: 10, borderRadius: 5 },
  deviceName: { color: '#e0e0e0', fontSize: 15, fontWeight: '600' },
  deviceMeta: { color: '#9ca3af', fontSize: 12, marginTop: 2 },
  selectedBadge: {
    color: '#a5b4fc',
    fontSize: 11,
    fontWeight: '700',
    textTransform: 'uppercase',
  },
  error: { color: '#ef4444', fontSize: 13, marginTop: 16, textAlign: 'center' },
});
