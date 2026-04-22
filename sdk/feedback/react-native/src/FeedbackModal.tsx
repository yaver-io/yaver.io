import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  DeviceEventEmitter,
  Keyboard,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { YaverFeedback } from './YaverFeedback';
import {
  captureScreenshot,
  // Launch scope for the feedback test SDK is intentionally smaller for now.
  // Keep the dormant file-upload and screen-recording helpers nearby, but
  // comment them out until we bring them back with stronger test coverage.
  // pickFeedbackFile,
  // startVideoRecording,
  // stopVideoRecording,
} from './capture';
import { uploadFeedback } from './upload';
import { DeviceInfo, FeedbackBundle } from './types';
import { AuthOverlay } from './AuthOverlay';
import { QuickActionIcon } from './QuickActionIcon';
import { listReachableDevices, RemoteDevice } from './auth';
import {
  QUICK_ICON_COLOR_PRESETS,
  QuickIconColorPreset,
} from './preferences';

/**
 * Simplified feedback modal — launch scope is 3 actions:
 *
 *  1. Hot Reload               — instant JS reload (most common use case)
 *  2. Vibing                   — open a vibing session on the agent
 *  3. Screenshot & Fix         — capture the underlying app (modal hidden
 *                                during capture), upload it, and trigger
 *                                the fix loop
 *
 * The footer also has an explicit Cancel button so the icon tap path
 * feels like a standard action sheet rather than a hidden modal.
 */

type ActionState =
  | 'idle'
  | 'hot-reloading'
  | 'capturing'
  | 'vibing';

type MachineCardState = {
  device: RemoteDevice | null;
  reachable: boolean | null;
  loading: boolean;
  status: 'none' | 'live' | 'attention' | 'offline';
  title: string;
  detail: string;
};

export const FeedbackModal: React.FC = () => {
  const [visible, setVisible] = useState(false);
  const [action, setAction] = useState<ActionState>('idle');
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [progress, setProgress] = useState<number | null>(null);
  // Tracks whether the user has hidden the QuickActionIcon via its
  // long-press menu. Shake is always available, so the feedback modal
  // is our guaranteed UI for bringing the icon back — we surface a
  // small "Show quick icon" row when this is true.
  const [quickIconHidden, setQuickIconHidden] = useState(false);
  // Vibing-input mode: same expand-on-tap pattern as email login.
  // Tap "Vibing" once → the button reveals an input + Send; that lets
  // the user say WHAT they want to vibe on instead of firing a canned
  // "pick something for me" prompt (which in 0.7.13 pointed Claude at
  // the wrong project because the matcher grepped the prompt itself).
  const [showVibeInput, setShowVibeInput] = useState(false);
  const [vibePrompt, setVibePrompt] = useState('');
  const [lastVibeTaskId, setLastVibeTaskId] = useState<string | null>(null);
  const [quickIconColorPreset, setQuickIconColorPreset] =
    useState<QuickIconColorPreset | null>(null);
  const [machineCard, setMachineCard] = useState<MachineCardState>({
    device: null,
    reachable: null,
    loading: false,
    status: 'none',
    title: 'No machine selected',
    detail: 'Pick a remote dev machine before using the feedback actions.',
  });
  const mountedRef = useRef(true);

  const loadSelectedMachine = useCallback(async () => {
    const cfg = YaverFeedback.getConfig();
    if (!cfg?.authToken) {
      if (mountedRef.current) {
        setMachineCard({
          device: null,
          reachable: null,
          loading: false,
          status: 'none',
          title: 'Not signed in',
          detail: 'Sign in to pick and monitor a remote dev machine.',
        });
      }
      return;
    }
    if (!cfg.preferredDeviceId) {
      if (mountedRef.current) {
        setMachineCard({
          device: null,
          reachable: null,
          loading: false,
          status: 'none',
          title: 'No machine selected',
          detail: 'Choose which machine this SDK should talk to.',
        });
      }
      return;
    }

    if (mountedRef.current) {
      setMachineCard((prev) => ({ ...prev, loading: true }));
    }

    try {
      const devices = await listReachableDevices(cfg.authToken);
      const all = [...devices.owned, ...devices.shared];
      const device =
        all.find((candidate) => candidate.deviceId === cfg.preferredDeviceId) ?? null;

      if (!device) {
        if (mountedRef.current) {
          setMachineCard({
            device: null,
            reachable: null,
            loading: false,
            status: 'offline',
            title: 'Selected machine missing',
            detail: 'The saved machine was not returned by the device list. Re-select it.',
          });
        }
        return;
      }

      let reachable: boolean | null = null;
      const client = YaverFeedback.getP2PClient();
      if (device.isOnline && !device.needsAuth && client) {
        reachable = await client.health();
      }

      const hostHint = device.hostEmail ? ` via ${device.hostEmail}` : '';
      let status: MachineCardState['status'] = 'live';
      let detail = `${device.platform}${hostHint}`;

      if (!device.isOnline) {
        status = 'offline';
        detail = 'Machine offline. Start `yaver serve` on the selected machine.';
      } else if (device.needsAuth) {
        status = 'attention';
        detail = 'Machine needs pairing again before feedback actions can run.';
      } else if (device.runnerDown) {
        status = 'attention';
        detail = 'Machine is online but the coding agent is down.';
      } else if (reachable === false) {
        status = 'offline';
        detail = 'Machine selected, but the agent is not responding.';
      }

      if (mountedRef.current) {
        setMachineCard({
          device,
          reachable,
          loading: false,
          status,
          title: device.name || device.deviceId,
          detail,
        });
      }
    } catch (err) {
      if (mountedRef.current) {
        setMachineCard({
          device: null,
          reachable: null,
          loading: false,
          status: 'offline',
          title: 'Machine status unavailable',
          detail: err instanceof Error ? err.message : String(err),
        });
      }
    }
  }, []);

  useEffect(() => {
    mountedRef.current = true;
    const sub = DeviceEventEmitter.addListener('yaverFeedback:startReport', () => {
      if (YaverFeedback.isEnabled()) {
        setVisible(true);
        setError(null);
        setToast(null);
        setProgress(null);
        setAction('idle');
        setShowVibeInput(false);
        setVibePrompt('');
        // Re-read the "user hid the quick icon" flag on every open so
        // the re-enable row reflects the latest preference (the user
        // might have hidden or shown it between opens).
        YaverFeedback.isQuickIconHidden()
          .then((v) => {
            if (mountedRef.current) setQuickIconHidden(v);
          })
          .catch(() => {});
        YaverFeedback.getQuickIconColorPreset()
          .then((preset) => {
            if (mountedRef.current) setQuickIconColorPreset(preset);
          })
          .catch(() => {});
        void loadSelectedMachine();
      }
    });
    // Agent streams build / compile progress through the BlackBox
    // SSE command channel as `command: "status"`; YaverFeedback re-emits
    // it as `yaverFeedback:status`. Show the most recent message in the
    // toast so a multi-second rebuild feels like "working" instead of
    // "stuck".
    const statusSub = DeviceEventEmitter.addListener(
      'yaverFeedback:status',
      (payload: { message?: string; phase?: string; progress?: number }) => {
        if (!mountedRef.current) return;
        const msg = payload?.message || payload?.phase || '';
        if (msg) setToast(msg);
        if (typeof payload?.progress === 'number') {
          setProgress(payload.progress);
        }
        // On final phases, fade the bar to 100% so the user sees
        // completion before the modal auto-dismisses.
        if (payload?.phase === 'done' || payload?.phase === 'error') {
          setProgress(1);
        }
      },
    );
    return () => {
      mountedRef.current = false;
      sub.remove();
      statusSub.remove();
    };
  }, [loadSelectedMachine]);

  useEffect(() => {
    if (!visible) return;
    const interval = setInterval(() => {
      void loadSelectedMachine();
    }, 5000);
    return () => clearInterval(interval);
  }, [loadSelectedMachine, visible]);

  const closeSoon = useCallback((delayMs = 1200) => {
    setTimeout(() => {
      if (mountedRef.current) setVisible(false);
    }, delayMs);
  }, []);

  const handleClose = useCallback(() => {
    setVisible(false);
    setError(null);
    setToast(null);
    setProgress(null);
    setAction('idle');
    setShowVibeInput(false);
    setVibePrompt('');
  }, []);

  // Helper: run a P2P call; on network failure, ask YaverFeedback to
  // re-query Convex for the fresh IP and retry once. Solves the common
  // case where the Mac's LAN IP rotated while the SDK held a stale URL.
  const runWithReconnect = useCallback(
    async (fn: (client: NonNullable<ReturnType<typeof YaverFeedback.getP2PClient>>) => Promise<void>) => {
      let client = YaverFeedback.getP2PClient();
      if (!client) {
        const ok = await YaverFeedback.reconnect();
        if (ok) client = YaverFeedback.getP2PClient();
      }
      if (!client) {
        throw new Error('Not connected to the agent yet.');
      }
      try {
        await fn(client);
      } catch (err) {
        const msg = (err instanceof Error ? err.message : String(err)) || '';
        // Avoid unbounded `.*` in regex — on RN 0.81 / Hermes rope
        // strings plus a background SSE reconnect, that pattern has
        // reliably SIGSEGV'd Hermes's string-view flattening path.
        // Split into short, literal-only alternations.
        const lower = msg.toLowerCase();
        const authFailed =
          lower.indexOf('invalid token') >= 0 ||
          lower.indexOf('unauthor') >= 0 ||
          lower.indexOf(' 401') >= 0 ||
          lower.indexOf(' 403') >= 0;
        if (authFailed) {
          await YaverFeedback.signOut();
          YaverFeedback.showLogin();
          throw new Error('Session expired — please sign in again.');
        }
        const transient =
          lower.indexOf('network request failed') >= 0 ||
          lower.indexOf('econnrefused') >= 0 ||
          lower.indexOf('failed to fetch') >= 0 ||
          lower.indexOf('fetch failed') >= 0 ||
          lower.indexOf('aborted') >= 0 ||
          lower.indexOf('timeout') >= 0;
        if (!transient) throw err;
        const ok = await YaverFeedback.reconnect();
        if (!ok) throw err;
        const fresh = YaverFeedback.getP2PClient();
        if (!fresh) throw err;
        await fn(fresh);
      }
    },
    [],
  );

  // ─── 1. Hot reload ─────────────────────────────────────────────────
  const handleHotReload = useCallback(async () => {
    setAction('hot-reloading');
    setError(null);
    setProgress(0);
    setToast('Contacting selected machine…');
    try {
      await loadSelectedMachine();
      const selected = await YaverFeedback.getSelectedRemoteDevice();
      if (!selected) {
        YaverFeedback.showMachinePicker();
        throw new Error('No machine selected. Pick a machine and try again.');
      }
      if (selected.needsAuth) {
        YaverFeedback.showMachinePicker();
        throw new Error('Selected machine needs pairing again.');
      }
      if (!selected.isOnline) {
        throw new Error('Selected machine is offline. Start `yaver serve` on it first.');
      }

      // Default mode: bundle. Always rebuilds via the agent regardless
      // of Metro state. P2PClient.reloadApp auto-resolves projectName +
      // bundleId from expo-constants / NativeModules so the agent can
      // map this app to its MobileProject scan entry without needing
      // `yaver dev start` to have been run.
      let ackMessage = 'Reload request acknowledged.';
      await runWithReconnect(async (client) => {
        const ack = await client.reloadApp('bundle');
        ackMessage = ack.message;
        setToast(ack.message);
        setProgress(0.2);
      });
      // We don't auto-close here — the agent's BlackBox status pings
      // will keep the modal updated, and the on-device YaverBundleLoader
      // will reload the JS once the fresh bundle arrives. Modal stays
      // up for a beat so the user sees the final progress state.
      setToast(ackMessage);
      closeSoon(2500);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
      setToast(
        message.toLowerCase().indexOf('session expired') >= 0
          ? 'Session expired. Sign in again.'
          : 'Hot reload did not start.',
      );
      await loadSelectedMachine();
      setProgress(null);
    } finally {
      if (mountedRef.current) setAction('idle');
    }
  }, [closeSoon, loadSelectedMachine, runWithReconnect]);

  const uploadBundleWithOptionalFix = useCallback(async (
    bundle: FeedbackBundle,
    fixOnUpload: boolean,
    successToast: string,
    failureToast?: string,
  ) => {
    const client = YaverFeedback.getP2PClient();
    const config = YaverFeedback.getConfig();
    if (!client || !config?.agentUrl) {
      setError('Not connected to the agent yet.');
      return;
    }
    try {
      const uploaded = await uploadFeedback(
        config.agentUrl,
        config.authToken ?? '',
        bundle,
      );
      // The agent returns the new report id as `id` (see
      // feedback_http.go::ReceiveFeedback). Trigger the fix loop if we got
      // one back; otherwise just ack the upload.
      const reportId =
        (uploaded as { id?: string; reportId?: string } | null | undefined)?.id ??
        (uploaded as { reportId?: string } | null | undefined)?.reportId;
      if (reportId && fixOnUpload) {
        try {
          await client.triggerFix(reportId);
          setToast(successToast);
        } catch (err: unknown) {
          setToast(failureToast ?? 'Report uploaded — fix trigger failed');
          setError(err instanceof Error ? err.message : String(err));
        }
      } else {
        setToast(successToast);
      }
      closeSoon(1400);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [closeSoon]);

  const handleScreenshotAndFix = useCallback(async () => {
    setAction('capturing');
    setError(null);

    setVisible(false);
    await new Promise((resolve) => setTimeout(resolve, 350));

    let path: string;
    try {
      path = await captureScreenshot();
    } catch (err: unknown) {
      setVisible(true);
      setError(err instanceof Error ? err.message : String(err));
      setAction('idle');
      return;
    }

    setVisible(true);
    await new Promise((resolve) => setTimeout(resolve, 150));

    try {
      const { Dimensions } = require('react-native');
      const { width, height } = Dimensions.get('window');
      const deviceInfo: DeviceInfo = {
        platform: Platform.OS,
        osVersion: String(Platform.Version),
        model: Platform.OS === 'ios' ? 'iOS Device' : 'Android Device',
        screenWidth: width,
        screenHeight: height,
      };
      const capturedErrors = YaverFeedback.getCapturedErrors();
      const bundle: FeedbackBundle = {
        metadata: {
          timestamp: new Date().toISOString(),
          device: deviceInfo,
          app: {},
          userNote: '[Screenshot + Fix]',
        },
        screenshots: [path],
        errors: capturedErrors.length > 0 ? capturedErrors : undefined,
      };
      await uploadBundleWithOptionalFix(
        bundle,
        true,
        'Fix task started',
      );
    } finally {
      if (mountedRef.current) setAction('idle');
    }
  }, [uploadBundleWithOptionalFix]);

  /*
  const handleFileUpload = useCallback(async () => {
    ...
  }, [uploadBundleWithOptionalFix]);
  */

  // ─── 3. Vibing ─────────────────────────────────────────────────────
  // First tap expands the input; second submit fires the actual
  // /vibing/execute. Mirrors the Yaver mobile app's Vibing tab —
  // user types what they want, hits Send, sees the task id back. If
  // left blank, we default to "pick the next small improvement"
  // so a one-tap workflow still works for lazy days.
  const handleVibingButton = useCallback(() => {
    if (!showVibeInput) {
      setShowVibeInput(true);
      return;
    }
    // collapse if tapped again with empty input
    if (!vibePrompt.trim()) {
      setShowVibeInput(false);
    }
  }, [showVibeInput, vibePrompt]);

  const handleVibingSubmit = useCallback(async () => {
    const client = YaverFeedback.getP2PClient();
    if (!client) {
      setError('Not connected to the agent yet.');
      return;
    }
    setAction('vibing');
    setError(null);
    try {
      const capturedErrors = YaverFeedback.getCapturedErrors();
      const errNote =
        capturedErrors.length > 0
          ? `\n\nRecent captured errors:\n` +
            capturedErrors
              .slice(-3)
              .map((e) => `- ${e.message}`)
              .join('\n')
          : '';
      const userPrompt = vibePrompt.trim();
      const prompt = userPrompt
        ? userPrompt + errNote
        : 'Pick the next small improvement or fix for this app based on recent activity and the current screen.' +
          errNote;
      const result = await client.vibing(prompt);
      setLastVibeTaskId(result.taskId);
      setToast(`Vibing task ${result.taskId.slice(0, 8)} created`);
      setVibePrompt('');
      setShowVibeInput(false);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (mountedRef.current) setAction('idle');
    }
  }, [vibePrompt]);

  /*
  const handleScreenRecording = useCallback(async () => {
    ...
  }, [closeSoon, isRecordingVideo, lastVideo]);
  */

  const busy = action !== 'idle';

  return (
    <>
      <AuthOverlay />
      <QuickActionIcon />
      {visible && (
        <Modal
          visible={visible}
          animationType="slide"
          transparent
          onRequestClose={handleClose}
        >
          <Pressable style={styles.overlay} onPress={handleClose}>
            <KeyboardAvoidingView
              behavior={Platform.OS === 'ios' ? 'padding' : 'height'}
              style={styles.kbAvoider}
              pointerEvents="box-none"
            >
            <Pressable
              style={styles.modal}
              onPress={(e) => {
                e.stopPropagation();
                Keyboard.dismiss();
              }}
            >
              <ScrollView
                style={styles.scroll}
                contentContainerStyle={styles.scrollContent}
                keyboardShouldPersistTaps="handled"
              >
              <View style={styles.header}>
                <Text style={styles.title}>Send Feedback</Text>
                <Pressable
                  onPress={handleClose}
                  hitSlop={12}
                  style={styles.closeBtn}
                  accessibilityRole="button"
                  accessibilityLabel="Close"
                >
                  <Text style={styles.closeIcon}>×</Text>
                </Pressable>
              </View>

              <Pressable
                onPress={() => {
                  if (!YaverFeedback.isAuthed()) {
                    YaverFeedback.showLogin();
                    return;
                  }
                  YaverFeedback.showMachinePicker();
                }}
                style={[
                  styles.machineCard,
                  machineCard.status === 'live' && styles.machineCardLive,
                  machineCard.status === 'attention' && styles.machineCardAttention,
                  machineCard.status === 'offline' && styles.machineCardOffline,
                ]}
              >
                <View style={styles.machineHeader}>
                  <View style={styles.machineTitleWrap}>
                    <View
                      style={[
                        styles.machineDot,
                        machineCard.status === 'live' && styles.machineDotLive,
                        machineCard.status === 'attention' && styles.machineDotAttention,
                        machineCard.status === 'offline' && styles.machineDotOffline,
                      ]}
                    />
                    <Text style={styles.machineLabel}>Selected Machine</Text>
                  </View>
                  <Text style={styles.machineAction}>
                    {machineCard.loading ? 'Refreshing…' : 'Change'}
                  </Text>
                </View>
                <Text style={styles.machineName}>
                  {machineCard.loading ? 'Checking machine…' : machineCard.title}
                </Text>
                <Text style={styles.machineMeta}>{machineCard.detail}</Text>
              </Pressable>

              {quickIconHidden && (
                <View style={styles.quickIconNote}>
                  <Text style={styles.quickIconNoteText}>
                    Quick access icon is hidden. Shake the phone if you want feedback back fast.
                  </Text>
                  <Pressable
                    onPress={() => {
                      void YaverFeedback.setQuickIconVisible(true);
                      setQuickIconHidden(false);
                    }}
                    style={({ pressed }) => [
                      styles.quickIconToggle,
                      pressed && styles.buttonPressed,
                    ]}
                  >
                    <Text style={styles.quickIconToggleText}>Show quick icon again</Text>
                  </Pressable>
                </View>
              )}

              <View style={styles.iconSelector}>
                <Text style={styles.iconSelectorTitle}>Quick Icon Color</Text>
                <Text style={styles.iconSelectorText}>
                  Pick a runtime color so the floating y icon does not overlap with your app UI.
                </Text>
                <View style={styles.iconSelectorGrid}>
                  {(Object.entries(QUICK_ICON_COLOR_PRESETS) as Array<
                    [QuickIconColorPreset, (typeof QUICK_ICON_COLOR_PRESETS)[QuickIconColorPreset]]
                  >).map(([preset, colors]) => {
                    const selected = quickIconColorPreset === preset;
                    return (
                      <Pressable
                        key={preset}
                        onPress={() => {
                          setQuickIconColorPreset(preset);
                          void YaverFeedback.setQuickIconColorPreset(preset);
                        }}
                        style={[
                          styles.iconOption,
                          selected && styles.iconOptionSelected,
                        ]}
                      >
                        <View
                          style={[
                            styles.iconOptionCircle,
                            {
                              backgroundColor: colors.backgroundColor,
                              borderColor: colors.borderColor,
                              shadowColor: colors.shadowColor,
                            },
                          ]}
                        >
                          <Text
                            style={[
                              styles.iconOptionLabel,
                              { color: colors.foregroundColor },
                            ]}
                          >
                            y
                          </Text>
                        </View>
                        <Text style={styles.iconOptionText}>{colors.label}</Text>
                      </Pressable>
                    );
                  })}
                </View>
              </View>

              {/* 1. Hot Reload — the common path */}
              <ActionRow
                label={
                  action === 'hot-reloading' ? 'Reloading…' : 'Hot Reload'
                }
                tint="#fbbf24"
                onPress={handleHotReload}
                disabled={busy}
                busy={action === 'hot-reloading'}
              />

              {/* 3. Vibing — expands to an input box on first tap
                   so the user says WHAT they want to vibe on, just
                   like the Yaver mobile app's Vibing tab. Second
                   tap (Send) fires /vibing/execute with the typed
                   prompt + resolved bundle id so the agent routes
                   to the right repo. */}
              {!showVibeInput ? (
                <ActionRow
                  label={action === 'vibing' ? 'Starting…' : 'Vibing'}
                  tint="#818cf8"
                  onPress={handleVibingButton}
                  disabled={busy}
                  busy={action === 'vibing'}
                />
              ) : (
                <View style={styles.vibeInputRow}>
                  <TextInput
                    style={styles.vibeInput}
                    placeholder="What do you want to vibe on?"
                    placeholderTextColor="#666"
                    value={vibePrompt}
                    onChangeText={setVibePrompt}
                    multiline
                    autoFocus
                    editable={action !== 'vibing'}
                    blurOnSubmit={false}
                  />
                  <View style={styles.vibeInputButtons}>
                    <Pressable
                      onPress={() => { setShowVibeInput(false); setVibePrompt(''); }}
                      style={({ pressed }) => [styles.vibeCancelBtn, pressed && styles.buttonPressed]}
                      disabled={action === 'vibing'}
                    >
                      <Text style={styles.vibeCancelBtnText}>Cancel</Text>
                    </Pressable>
                    <Pressable
                      onPress={handleVibingSubmit}
                      style={({ pressed }) => [
                        styles.vibeSendBtn,
                        pressed && styles.buttonPressed,
                        action === 'vibing' && { opacity: 0.6 },
                      ]}
                      disabled={action === 'vibing'}
                    >
                      {action === 'vibing' ? (
                        <ActivityIndicator color="#fff" />
                      ) : (
                        <Text style={styles.vibeSendBtnText}>Send</Text>
                      )}
                    </Pressable>
                  </View>
                </View>
              )}
              {lastVibeTaskId && action !== 'vibing' && (
                <Text style={styles.vibeTaskLine} numberOfLines={1}>
                  Last vibing task: {lastVibeTaskId.slice(0, 12)}…
                </Text>
              )}

              {/* Screenshot & Fix */}
              <ActionRow
                label={action === 'capturing' ? 'Working…' : 'Screenshot & Fix'}
                tint="#22c55e"
                onPress={handleScreenshotAndFix}
                disabled={busy}
                busy={action === 'capturing'}
              />

              {progress !== null && (
                <View style={styles.progressTrack}>
                  <View
                    style={[
                      styles.progressFill,
                      { width: `${Math.round(progress * 100)}%` },
                    ]}
                  />
                </View>
              )}
              {toast && <Text style={styles.toast}>{toast}</Text>}
              {error && <Text style={styles.error}>{error}</Text>}

              <Pressable
                onPress={handleClose}
                style={({ pressed }) => [
                  styles.cancelBtn,
                  pressed && styles.buttonPressed,
                ]}
                accessibilityRole="button"
                accessibilityLabel="Cancel"
              >
                <Text style={styles.cancelBtnText}>Cancel</Text>
              </Pressable>
              </ScrollView>
            </Pressable>
            </KeyboardAvoidingView>
          </Pressable>
        </Modal>
      )}
    </>
  );
};

interface ActionRowProps {
  label: string;
  tint: string;
  onPress: () => void;
  disabled?: boolean;
  busy?: boolean;
}

const ActionRow: React.FC<ActionRowProps> = ({
  label,
  tint,
  onPress,
  disabled,
  busy,
}) => (
  <Pressable
    onPress={onPress}
    disabled={disabled}
    style={({ pressed }) => [
      styles.actionBtn,
      {
        borderColor: tint + '66',
        backgroundColor: tint + '1f',
      },
      disabled && styles.actionBtnDisabled,
      pressed && !disabled && { opacity: 0.7 },
    ]}
    accessibilityRole="button"
    accessibilityLabel={label}
  >
    {busy ? (
      <ActivityIndicator color={tint} size="small" />
    ) : (
      <Text style={[styles.actionText, { color: tint }]}>{label}</Text>
    )}
  </Pressable>
);

const styles = StyleSheet.create({
  vibeInputRow: {
    backgroundColor: 'rgba(129,140,248,0.08)',
    borderColor: 'rgba(129,140,248,0.4)',
    borderWidth: 1,
    borderRadius: 12,
    padding: 12,
    gap: 10,
  },
  vibeInput: {
    color: '#fff',
    fontSize: 15,
    minHeight: 64,
    textAlignVertical: 'top',
    padding: 0,
  },
  vibeInputButtons: {
    flexDirection: 'row',
    justifyContent: 'flex-end',
    gap: 10,
  },
  vibeCancelBtn: {
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 8,
    backgroundColor: 'transparent',
  },
  vibeCancelBtnText: {
    color: '#999',
    fontSize: 14,
    fontWeight: '600',
  },
  vibeSendBtn: {
    paddingHorizontal: 16,
    paddingVertical: 8,
    borderRadius: 8,
    backgroundColor: '#818cf8',
    minWidth: 72,
    alignItems: 'center',
  },
  vibeSendBtnText: {
    color: '#fff',
    fontSize: 14,
    fontWeight: '700',
  },
  vibeTaskLine: {
    color: '#818cf8',
    fontSize: 12,
    marginTop: -4,
    fontFamily: Platform.select({ ios: 'Menlo', android: 'monospace', default: 'monospace' }),
  },
  overlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.55)',
    justifyContent: 'flex-end',
  },
  kbAvoider: {
    width: '100%',
    justifyContent: 'flex-end',
  },
  modal: {
    backgroundColor: '#141422',
    borderTopLeftRadius: 22,
    borderTopRightRadius: 22,
    padding: 22,
    paddingBottom: 36,
    gap: 12,
    maxHeight: '92%',
  },
  scroll: {
    maxHeight: '100%',
  },
  scrollContent: {
    gap: 12,
    paddingBottom: 8,
  },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: 6,
  },
  title: {
    fontSize: 20,
    fontWeight: '700',
    color: '#fff',
  },
  closeBtn: {
    width: 36,
    height: 36,
    borderRadius: 18,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: 'rgba(255,255,255,0.08)',
  },
  closeIcon: {
    color: '#fff',
    fontSize: 22,
    lineHeight: 24,
    fontWeight: '400',
  },
  actionBtn: {
    paddingVertical: 16,
    borderRadius: 14,
    alignItems: 'center',
    justifyContent: 'center',
    borderWidth: 1,
  },
  actionBtnDisabled: {
    opacity: 0.35,
  },
  actionText: {
    fontSize: 15,
    fontWeight: '700',
  },
  machineCard: {
    borderRadius: 14,
    borderWidth: 1,
    padding: 14,
    backgroundColor: 'rgba(255,255,255,0.04)',
    borderColor: 'rgba(255,255,255,0.12)',
  },
  machineCardLive: {
    backgroundColor: 'rgba(34,197,94,0.10)',
    borderColor: 'rgba(34,197,94,0.35)',
  },
  machineCardAttention: {
    backgroundColor: 'rgba(245,158,11,0.10)',
    borderColor: 'rgba(245,158,11,0.35)',
  },
  machineCardOffline: {
    backgroundColor: 'rgba(239,68,68,0.10)',
    borderColor: 'rgba(239,68,68,0.35)',
  },
  machineHeader: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: 6,
  },
  machineTitleWrap: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
  },
  machineDot: {
    width: 10,
    height: 10,
    borderRadius: 5,
    backgroundColor: '#6b7280',
  },
  machineDotLive: {
    backgroundColor: '#22c55e',
  },
  machineDotAttention: {
    backgroundColor: '#f59e0b',
  },
  machineDotOffline: {
    backgroundColor: '#ef4444',
  },
  machineLabel: {
    color: '#cbd5e1',
    fontSize: 12,
    fontWeight: '700',
    textTransform: 'uppercase',
    letterSpacing: 0.8,
  },
  machineAction: {
    color: '#a5b4fc',
    fontSize: 12,
    fontWeight: '700',
  },
  machineName: {
    color: '#fff',
    fontSize: 16,
    fontWeight: '700',
  },
  machineMeta: {
    color: '#cbd5e1',
    fontSize: 12,
    marginTop: 4,
    lineHeight: 17,
  },
  captureChoices: {
    gap: 10,
  },
  progressTrack: {
    height: 6,
    borderRadius: 3,
    backgroundColor: 'rgba(255,255,255,0.08)',
    overflow: 'hidden',
    marginTop: 4,
  },
  progressFill: {
    height: '100%',
    backgroundColor: '#818cf8',
    borderRadius: 3,
  },
  toast: {
    color: '#22c55e',
    fontSize: 13,
    textAlign: 'center',
    marginTop: 4,
  },
  error: {
    color: '#ef4444',
    fontSize: 12,
    textAlign: 'center',
    marginTop: 4,
  },
  quickIconToggle: {
    marginTop: 6,
    alignSelf: 'flex-start',
    paddingVertical: 6,
    paddingHorizontal: 10,
    borderRadius: 10,
    backgroundColor: 'rgba(255,255,255,0.05)',
  },
  quickIconToggleText: {
    color: '#cbd5e1',
    fontSize: 12,
    fontWeight: '700',
  },
  quickIconNote: {
    borderRadius: 12,
    borderWidth: 1,
    borderColor: 'rgba(251,191,36,0.28)',
    backgroundColor: 'rgba(251,191,36,0.08)',
    padding: 12,
  },
  quickIconNoteText: {
    color: '#fde68a',
    fontSize: 12,
    lineHeight: 17,
  },
  iconSelector: {
    borderRadius: 12,
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,0.09)',
    backgroundColor: 'rgba(255,255,255,0.03)',
    padding: 12,
    gap: 10,
  },
  iconSelectorTitle: {
    color: '#f8fafc',
    fontSize: 13,
    fontWeight: '700',
  },
  iconSelectorText: {
    color: '#cbd5e1',
    fontSize: 12,
    lineHeight: 17,
  },
  iconSelectorGrid: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 10,
  },
  iconOption: {
    width: '31%',
    minWidth: 84,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,0.08)',
    backgroundColor: 'rgba(255,255,255,0.02)',
    paddingVertical: 10,
    paddingHorizontal: 8,
    alignItems: 'center',
    gap: 8,
  },
  iconOptionSelected: {
    borderColor: 'rgba(129,140,248,0.72)',
    backgroundColor: 'rgba(129,140,248,0.12)',
  },
  iconOptionCircle: {
    width: 38,
    height: 38,
    borderRadius: 19,
    alignItems: 'center',
    justifyContent: 'center',
    borderWidth: 2,
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.28,
    shadowRadius: 5,
    elevation: 5,
  },
  iconOptionLabel: {
    fontSize: 18,
    fontWeight: '700',
  },
  iconOptionText: {
    color: '#e2e8f0',
    fontSize: 11,
    fontWeight: '600',
  },
  cancelBtn: {
    marginTop: 4,
    borderRadius: 14,
    borderWidth: 1,
    borderColor: 'rgba(255,255,255,0.1)',
    paddingVertical: 15,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: 'rgba(255,255,255,0.04)',
  },
  cancelBtnText: {
    color: '#e5e7eb',
    fontSize: 15,
    fontWeight: '700',
  },
  buttonPressed: {
    opacity: 0.7,
  },
});
