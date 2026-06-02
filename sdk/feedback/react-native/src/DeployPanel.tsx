import React, { useCallback, useEffect, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { YaverFeedback } from './YaverFeedback';

/**
 * Inline Deploy panel — third-party RN apps using yaver-feedback can deploy
 * to TestFlight / Play Store from a phone shake without leaving their app.
 *
 * Flow:
 *   1. GET /fleet/deploy-options?app=<slug> on the SDK's selected machine.
 *      The agent fans out doctor probes to the user's other reachable
 *      devices (LAN > Tailscale > relay) and returns merged capabilities.
 *   2. User picks a target (TestFlight / Play / Both) — disables machines
 *      whose doctor reports a blocker for any picked target. Linux boxes
 *      grey out for TestFlight ("xcodebuild: only on darwin"). macOS
 *      machines without Xcode grey out for the same reason.
 *   3. Tap a machine row → POST /deploy/ship {app, target/targets, machine}.
 *      Toast + auto-collapse the panel. Live SSE log viewing is the
 *      desktop / web Deploy tab's job — this surface stays minimal.
 *
 * App slug resolution: prefer config.deployAppSlug → bundleId tail →
 * literal "main". Documented on FeedbackConfig.deployAppSlug.
 */

interface FleetDeployTargetCap {
  target: string;
  ok: boolean;
  reason?: string;
}

interface FleetDeployDevice {
  deviceId: string;
  name: string;
  alias?: string;
  platform: string;
  isLocal: boolean;
  isOnline: boolean;
  probed: boolean;
  probeError?: string;
  capabilities: FleetDeployTargetCap[];
}

interface FleetDeployOptions {
  app: string;
  stack?: string;
  targets: string[];
  devices: FleetDeployDevice[];
  warnings?: string[];
}

const TARGET_LABELS: Record<string, string> = {
  testflight: 'TestFlight',
  playstore: 'Play Store',
};

interface DeployPanelProps {
  /** Called when the user taps Cancel or after a successful deploy starts. */
  onClose: () => void;
}

type SelectedTarget = 'testflight' | 'playstore' | 'both';

export const DeployPanel: React.FC<DeployPanelProps> = ({ onClose }) => {
  const [options, setOptions] = useState<FleetDeployOptions | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [statusTone, setStatusTone] = useState<'progress' | 'success' | 'error'>('progress');
  const [selected, setSelected] = useState<SelectedTarget>('both');
  const [shipping, setShipping] = useState(false);

  const resolveAppSlug = useCallback((): string => {
    const cfg = YaverFeedback.getConfig();
    const explicit = (cfg as { deployAppSlug?: string } | null | undefined)?.deployAppSlug;
    if (explicit && explicit.trim().length > 0) return explicit.trim();
    // Best-effort fallback: bundleId's last dot-segment. iOS gives us
    // `io.yaver.sfmg`; Android gives the same shape. The agent's
    // workspace manifest typically names apps after the project basename
    // which is usually the same word, but the user can override via
    // config.deployAppSlug if it isn't.
    const bundleId = (cfg as { bundleId?: string } | null | undefined)?.bundleId;
    if (bundleId) {
      const tail = bundleId.split('.').pop();
      if (tail) return tail;
    }
    return 'main';
  }, []);

  const baseAuthHeaders = useCallback((): Record<string, string> => {
    const cfg = YaverFeedback.getConfig();
    const headers: Record<string, string> = {};
    if (cfg?.authToken) headers.Authorization = `Bearer ${cfg.authToken}`;
    const relay = YaverFeedback.getRelayPassword();
    if (relay) headers['X-Relay-Password'] = relay;
    return headers;
  }, []);

  const fetchOptions = useCallback(async () => {
    setLoading(true);
    setError(null);
    const cfg = YaverFeedback.getConfig();
    if (!cfg?.agentUrl) {
      setError('Not connected to a Yaver agent yet.');
      setLoading(false);
      return;
    }
    const app = resolveAppSlug();
    const url = `${cfg.agentUrl.replace(/\/$/, '')}/fleet/deploy-options?app=${encodeURIComponent(app)}`;
    try {
      const resp = await fetch(url, { headers: baseAuthHeaders() });
      if (!resp.ok) {
        const text = await resp.text().catch(() => '');
        throw new Error(`fetch failed (${resp.status}): ${text || resp.statusText}`);
      }
      const json = (await resp.json()) as FleetDeployOptions;
      setOptions(json);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [baseAuthHeaders, resolveAppSlug]);

  useEffect(() => {
    void fetchOptions();
  }, [fetchOptions]);

  const pickedTargets = (): string[] => {
    switch (selected) {
      case 'testflight':
        return ['testflight'];
      case 'playstore':
        return ['playstore'];
      default:
        return ['testflight', 'playstore'];
    }
  };

  const machineRow = (d: FleetDeployDevice) => {
    const targets = pickedTargets();
    const blockers: string[] = [];
    let allOK = true;
    for (const t of targets) {
      const cap = d.capabilities.find((c) => c.target === t);
      if (!cap) {
        allOK = false;
        blockers.push(`${TARGET_LABELS[t] ?? t}: no capability data`);
        continue;
      }
      if (!cap.ok) {
        allOK = false;
        if (cap.reason) blockers.push(`${TARGET_LABELS[t] ?? t}: ${cap.reason}`);
      }
    }
    if (!d.probed && allOK) {
      allOK = false;
      blockers.push(d.probeError || "couldn't reach this machine");
    }
    const label = (d.alias && d.alias.length > 0 ? d.alias : d.name) +
      (d.isLocal ? ' (this phone’s primary)' : '');
    return (
      <Pressable
        key={d.deviceId}
        disabled={!allOK || shipping}
        onPress={() => triggerDeploy(d.deviceId)}
        style={({ pressed }) => [
          styles.row,
          !allOK && styles.rowDisabled,
          pressed && allOK && styles.rowPressed,
        ]}
      >
        <Text style={styles.rowName}>{label}</Text>
        <Text style={[styles.rowMeta, !allOK && styles.rowMetaWarning]}>
          {d.platform} {'·'} {allOK ? 'ready' : blockers.join(' · ')}
        </Text>
      </Pressable>
    );
  };

  // First-class App Store screenshots: walk the app's routes on THIS
  // device, screenshot each, upload to the agent (which runs the ASC
  // backend). Closes the overlay first so the captures are clean (no
  // modal in frame), then reports via an alert.
  const runStoreShots = () => {
    const cfg = YaverFeedback.getConfig() as { storeShots?: any } | null;
    const ss = cfg?.storeShots;
    if (!ss?.routes?.length) {
      setStatus('Set config.storeShots.routes (and a navigationRef) to enable.');
      setStatusTone('error');
      return;
    }
    const app = resolveAppSlug();
    onClose();
    // Defer so the overlay is fully dismissed before the first capture.
    setTimeout(async () => {
      try {
        const res = await YaverFeedback.captureStoreScreenshots({
          app,
          routes: ss.routes,
          navigationRef: ss.navigationRef,
          screens: ss.screens,
          submit: ss.submit,
        });
        const msg = res.ok
          ? res.submitted
            ? 'Submitted for App Store review 🎉'
            : res.staged
              ? `Uploaded ${res.uploaded} screenshots — staged. One tap left in App Store Connect.`
              : `Uploaded ${res.uploaded} App Store screenshots.`
          : res.message || 'Capture failed.';
        Alert.alert('App Store screenshots', msg);
      } catch (e: any) {
        Alert.alert('App Store screenshots', e?.message ?? 'Capture failed.');
      }
    }, 500);
  };

  const storeShotsEnabled =
    ((YaverFeedback.getConfig() as { storeShots?: any } | null)?.storeShots?.routes?.length ?? 0) > 0;

  const triggerDeploy = async (machine: string) => {
    if (!options) return;
    setShipping(true);
    setStatus(`starting deploy on ${prettyMachineName(machine)}…`);
    setStatusTone('progress');
    const cfg = YaverFeedback.getConfig();
    if (!cfg?.agentUrl) {
      setStatus('Not connected to a Yaver agent yet.');
      setStatusTone('error');
      setShipping(false);
      return;
    }
    const targets = pickedTargets();
    const body: Record<string, unknown> = {
      app: options.app,
      machine,
    };
    if (targets.length === 1) {
      body.target = targets[0];
    } else {
      body.targets = targets;
    }
    try {
      const resp = await fetch(`${cfg.agentUrl.replace(/\/$/, '')}/deploy/ship`, {
        method: 'POST',
        headers: { ...baseAuthHeaders(), 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (!resp.ok) {
        const text = await resp.text().catch(() => '');
        throw new Error(`ship failed (${resp.status}): ${text || resp.statusText}`);
      }
      setStatus('deploy started — track progress in the desktop / web tab');
      setStatusTone('success');
      // Auto-close shortly so the user can keep using their app. Keep
      // this in sync with the iOS / Android pane delays.
      setTimeout(() => onClose(), 1600);
    } catch (err: unknown) {
      setStatus(err instanceof Error ? err.message : String(err));
      setStatusTone('error');
    } finally {
      setShipping(false);
    }
  };

  const prettyMachineName = (id: string): string => {
    const d = options?.devices.find((x) => x.deviceId === id);
    if (!d) return id;
    return d.alias && d.alias.length > 0 ? d.alias : d.name;
  };

  const targetButton = (value: SelectedTarget, label: string) => (
    <Pressable
      key={value}
      onPress={() => setSelected(value)}
      style={[styles.segBtn, selected === value && styles.segBtnSelected]}
    >
      <Text style={[styles.segText, selected === value && styles.segTextSelected]}>{label}</Text>
    </Pressable>
  );

  return (
    <View style={styles.container}>
      <View style={styles.headerRow}>
        <Text style={styles.title}>Deploy</Text>
        <Pressable onPress={onClose} hitSlop={10}>
          <Text style={styles.closeIcon}>✕</Text>
        </Pressable>
      </View>
      <Text style={styles.subtitle}>
        {loading
          ? 'loading machines…'
          : options
            ? `${options.devices.length} machine${options.devices.length === 1 ? '' : 's'} — pick a target, then tap to deploy`
            : 'no data yet'}
      </Text>

      <View style={styles.segment}>
        {targetButton('testflight', 'TestFlight')}
        {targetButton('playstore', 'Play Store')}
        {targetButton('both', 'Both')}
      </View>

      {loading ? (
        <View style={styles.loading}>
          <ActivityIndicator color="rgba(255,255,255,0.6)" />
        </View>
      ) : error ? (
        <Text style={styles.error}>{error}</Text>
      ) : options ? (
        <ScrollView style={styles.list}>{options.devices.map(machineRow)}</ScrollView>
      ) : null}

      {storeShotsEnabled && (
        <Pressable
          onPress={runStoreShots}
          disabled={shipping}
          style={({ pressed }) => [styles.shotsBtn, pressed && styles.rowPressed]}
        >
          <Text style={styles.shotsBtnText}>📸 App Store screenshots</Text>
          <Text style={styles.shotsBtnSub}>
            capture this app on-device + upload to App Store Connect
          </Text>
        </Pressable>
      )}

      {status && (
        <Text
          style={[
            styles.status,
            statusTone === 'success' && styles.statusSuccess,
            statusTone === 'error' && styles.statusError,
          ]}
        >
          {status}
        </Text>
      )}
    </View>
  );
};

const styles = StyleSheet.create({
  container: {
    backgroundColor: 'rgba(14,12,28,0.92)',
    borderRadius: 16,
    paddingHorizontal: 16,
    paddingTop: 14,
    paddingBottom: 18,
    marginVertical: 8,
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,0.08)',
  },
  headerRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  title: {
    color: '#fff',
    fontSize: 16,
    fontWeight: '600',
  },
  closeIcon: {
    color: 'rgba(255,255,255,0.55)',
    fontSize: 18,
    paddingHorizontal: 4,
  },
  subtitle: {
    color: 'rgba(255,255,255,0.55)',
    fontSize: 12,
    marginTop: 2,
  },
  segment: {
    flexDirection: 'row',
    backgroundColor: 'rgba(255,255,255,0.08)',
    borderRadius: 10,
    padding: 3,
    marginTop: 14,
  },
  segBtn: {
    flex: 1,
    alignItems: 'center',
    paddingVertical: 7,
    borderRadius: 8,
  },
  segBtnSelected: {
    backgroundColor: 'rgba(127,140,247,0.65)',
  },
  segText: {
    color: 'rgba(255,255,255,0.65)',
    fontSize: 13,
    fontWeight: '500',
  },
  segTextSelected: {
    color: '#fff',
    fontWeight: '600',
  },
  loading: {
    paddingVertical: 32,
    alignItems: 'center',
  },
  error: {
    color: 'rgb(255,115,115)',
    fontSize: 12,
    marginTop: 14,
  },
  list: {
    marginTop: 14,
    maxHeight: 260,
  },
  row: {
    backgroundColor: 'rgba(255,255,255,0.06)',
    borderRadius: 12,
    paddingHorizontal: 14,
    paddingVertical: 12,
    marginBottom: 8,
  },
  rowPressed: {
    backgroundColor: 'rgba(255,255,255,0.10)',
  },
  rowDisabled: {
    opacity: 0.55,
    backgroundColor: 'rgba(255,255,255,0.03)',
  },
  rowName: {
    color: '#fff',
    fontSize: 15,
    fontWeight: '600',
  },
  rowMeta: {
    color: 'rgba(255,255,255,0.55)',
    fontSize: 12,
    marginTop: 2,
  },
  rowMetaWarning: {
    color: 'rgb(255,178,115)',
  },
  shotsBtn: {
    marginTop: 12,
    paddingVertical: 12,
    paddingHorizontal: 14,
    borderRadius: 10,
    backgroundColor: 'rgba(124,109,255,0.16)',
    borderWidth: 1,
    borderColor: 'rgba(124,109,255,0.4)',
  },
  shotsBtnText: {
    color: '#fff',
    fontSize: 14,
    fontWeight: '600',
  },
  shotsBtnSub: {
    color: 'rgba(255,255,255,0.55)',
    fontSize: 12,
    marginTop: 2,
  },
  status: {
    color: 'rgba(255,255,255,0.55)',
    fontSize: 12,
    marginTop: 12,
    textAlign: 'center',
  },
  statusSuccess: {
    color: 'rgb(34,197,94)',
  },
  statusError: {
    color: 'rgb(255,115,115)',
  },
});
