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
  DeviceReachability,
  RemoteDevice,
  listReachableDevices,
  probeDeviceReachability,
  saveSelectedDeviceId,
} from './auth';
import { PairDeviceModal } from './PairDeviceModal';

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
  const [pairingDevice, setPairingDevice] = useState<RemoteDevice | null>(null);
  const [reachability, setReachability] = useState<Record<string, DeviceReachability | undefined>>({});

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    setError(null);
    try {
      const result = await listReachableDevices(token);
      setList(result);
      setReachability({});
      void (async () => {
        const devices = [...result.owned, ...result.shared];
        const settled = await Promise.allSettled(
          devices.map(async (device) => ({
            deviceId: device.deviceId,
            result: await probeDeviceReachability(device),
          })),
        );
        setReachability((prev) => {
          const next = { ...prev };
          for (const entry of settled) {
            if (entry.status === 'fulfilled') {
              next[entry.value.deviceId] = entry.value.result;
            }
          }
          return next;
        });
      })();
      if (result.owned.length === 0 && result.shared.length === 0) {
        setError(
          'No machines found yet. If you do not have your own computer, redeem a host invite code first. Otherwise run `yaver auth` + `yaver serve` on your machine.',
        );
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
    // Needs-auth device — show the in-SDK pair modal instead of
    // treating the tap as a "pick". The user enters the 6-char code
    // from their Mac terminal; the SDK POSTs it to /auth/pair/submit
    // on the agent directly. Once the device flips out of bootstrap
    // mode, the next load() picks up the fresh state.
    if (device.isOnline && device.needsAuth) {
      setPairingDevice(device);
      return;
    }
    const direct = await probeDeviceReachability(device);
    if (!direct.reachable && !device.needsAuth) {
      setError('Selected machine is not responding. Start `yaver serve` on it and try again.');
      setReachability((prev) => ({ ...prev, [device.deviceId]: direct }));
      return;
    }
    await saveSelectedDeviceId(device.deviceId);
    onPick(device);
  };

  const renderDevice = (device: RemoteDevice) => {
    const selected = device.deviceId === currentDeviceId;
    const probe = reachability[device.deviceId];
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
    const effectivelyReachable = probe?.reachable === true;
    const explicitlyOffline = probe?.reachable === false;
    const healthColor = device.needsAuth
      ? '#f59e0b'
      : effectivelyReachable
        ? '#22c55e'
        : explicitlyOffline || !device.isOnline
          ? '#ef4444'
          : '#22c55e';
    // Derive a single short status phrase the user can act on.
    let statusLine = device.platform;
    if (probe === undefined) {
      statusLine = 'Checking connection…';
    } else if (!device.isOnline && effectivelyReachable) {
      statusLine = 'Reachable now — waiting for cloud status to refresh';
    } else if (!device.isOnline) {
      statusLine = 'Offline — start `yaver serve` on the Mac';
    } else if (device.needsAuth) {
      statusLine =
        'Needs pairing — open the Yaver app to adopt this machine';
    } else if (explicitlyOffline) {
      statusLine = 'Agent not responding on this machine';
    } else if (device.runnerDown) {
      statusLine = 'Runner down — restart the coding agent on the Mac';
    } else {
      // Happy-path subtitle: platform + optional host/share hint.
      statusLine = device.platform;
      if (device.isGuest && device.hostEmail) {
        statusLine = `${statusLine} • ${device.hostEmail}`;
      } else if (device.accessScope === 'shared-scoped') {
        statusLine = `${statusLine} • paylaşılan`;
      }
    }
    return (
      <TouchableOpacity
        key={device.deviceId}
        style={[styles.deviceRow, selected && styles.deviceSelected]}
        onPress={() => handlePick(device)}
      >
        <View style={[styles.health, { backgroundColor: healthColor }]} />
        <View style={{ flex: 1 }}>
          <Text style={styles.deviceName}>{device.name || device.deviceId}</Text>
          <Text style={styles.deviceMeta}>{statusLine}</Text>
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

      <PairDeviceModal
        device={pairingDevice}
        onClose={() => setPairingDevice(null)}
        onPaired={() => {
          // Give the agent a moment to flip bootstrap → owner mode,
          // then reload the list so the now-authenticated device shows
          // up with a green dot and can be selected normally.
          setTimeout(() => void load(true), 1500);
        }}
      />
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
