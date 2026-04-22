import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  DeviceEventEmitter,
  Modal,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { YaverFeedback } from './YaverFeedback';
import {
  captureScreenshot,
  pickFeedbackFile,
  startVideoRecording,
  stopVideoRecording,
} from './capture';
import { uploadFeedback } from './upload';
import { DeviceInfo, FeedbackBundle } from './types';
import { AuthOverlay } from './AuthOverlay';
import { QuickActionIcon } from './QuickActionIcon';

/**
 * Simplified feedback modal — 4 actions:
 *
 *  1. Hot Reload               — instant JS reload (most common use case)
 *  2. Vibing                   — open a vibing session on the agent
 *  3. Screenshot / Upload      — capture the underlying app (modal hidden
 *                                during capture) or upload an existing
 *                                media file through the Go agent
 *  4. Screen Recording         — start recording, then stop + upload
 *
 * The footer also has an explicit Cancel button so the icon tap path
 * feels like a standard action sheet rather than a hidden modal.
 */

interface LastVideo {
  path: string;
  duration: number;
}

type ActionState =
  | 'idle'
  | 'hot-reloading'
  | 'capturing'
  | 'vibing'
  | 'uploading-video';

export const FeedbackModal: React.FC = () => {
  const [visible, setVisible] = useState(false);
  const [action, setAction] = useState<ActionState>('idle');
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [isRecordingVideo, setIsRecordingVideo] = useState(false);
  const [lastVideo, setLastVideo] = useState<LastVideo | null>(null);
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
  const [showCaptureChoices, setShowCaptureChoices] = useState(false);
  const [lastVibeTaskId, setLastVibeTaskId] = useState<string | null>(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    const sub = DeviceEventEmitter.addListener('yaverFeedback:startReport', () => {
      if (YaverFeedback.isEnabled()) {
        setVisible(true);
        setError(null);
        setToast(null);
        setAction('idle');
        setShowCaptureChoices(false);
        // Re-read the "user hid the quick icon" flag on every open so
        // the re-enable row reflects the latest preference (the user
        // might have hidden or shown it between opens).
        YaverFeedback.isQuickIconHidden()
          .then((v) => {
            if (mountedRef.current) setQuickIconHidden(v);
          })
          .catch(() => {});
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
  }, []);

  const closeSoon = useCallback((delayMs = 1200) => {
    setTimeout(() => {
      if (mountedRef.current) setVisible(false);
    }, delayMs);
  }, []);

  const handleClose = useCallback(() => {
    setVisible(false);
    setError(null);
    setToast(null);
    setAction('idle');
    setShowCaptureChoices(false);
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
    setToast('Sending…');
    try {
      // Default mode: bundle. Always rebuilds via the agent regardless
      // of Metro state. P2PClient.reloadApp auto-resolves projectName +
      // bundleId from expo-constants / NativeModules so the agent can
      // map this app to its MobileProject scan entry without needing
      // `yaver dev start` to have been run.
      await runWithReconnect(async (client) => {
        await client.reloadApp('bundle');
      });
      // We don't auto-close here — the agent's BlackBox status pings
      // will keep the modal updated, and the on-device YaverBundleLoader
      // will reload the JS once the fresh bundle arrives. Modal stays
      // up for a beat so the user sees the final progress state.
      closeSoon(2500);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
      setProgress(null);
    } finally {
      if (mountedRef.current) setAction('idle');
    }
  }, [closeSoon, runWithReconnect]);

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

  // ─── 3. Screenshot / Upload ───────────────────────────────────────
  const handleCaptureChoiceToggle = useCallback(() => {
    setShowCaptureChoices((v) => !v);
  }, []);

  const handleScreenshotAndFix = useCallback(async () => {
    setAction('capturing');
    setError(null);
    setShowCaptureChoices(false);

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

  const handleFileUpload = useCallback(async () => {
    setAction('capturing');
    setError(null);
    setShowCaptureChoices(false);
    try {
      const picked = await pickFeedbackFile();
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
          userNote: `[Uploaded file] ${picked.name}`,
        },
        screenshots: picked.kind === 'image' ? [picked.path] : [],
        video: picked.kind === 'video' ? picked.path : undefined,
        audio: picked.kind === 'audio' ? picked.path : undefined,
        errors: capturedErrors.length > 0 ? capturedErrors : undefined,
      };
      if (picked.kind === 'unknown') {
        throw new Error('Pick an image, video, or audio file.');
      }
      await uploadBundleWithOptionalFix(
        bundle,
        picked.kind === 'image',
        picked.kind === 'image' ? 'Fix task started' : 'File uploaded',
      );
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      if (message !== 'File selection canceled.') {
        setError(message);
      }
    } finally {
      if (mountedRef.current) setAction('idle');
    }
  }, [uploadBundleWithOptionalFix]);

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

  // ─── 4. Screen recording ───────────────────────────────────────────
  const handleScreenRecording = useCallback(async () => {
    setError(null);
    if (!isRecordingVideo && lastVideo) {
      const config = YaverFeedback.getConfig();
      if (!config?.agentUrl) {
        setError('Not connected to the agent yet.');
        return;
      }
      setAction('uploading-video');
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
        const bundle: FeedbackBundle = {
          metadata: {
            timestamp: new Date().toISOString(),
            device: deviceInfo,
            app: {},
            userNote: '[Screen recording]',
          },
          screenshots: [],
          video: lastVideo.path,
          errors: YaverFeedback.getCapturedErrors().length
            ? YaverFeedback.getCapturedErrors()
            : undefined,
        };
        await uploadFeedback(config.agentUrl, config.authToken ?? '', bundle);
        if (mountedRef.current) {
          setToast(`Recording uploaded — ${Math.round(lastVideo.duration)}s`);
          setLastVideo(null);
        }
        closeSoon(1200);
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (mountedRef.current) setAction('idle');
      }
      return;
    }

    if (isRecordingVideo) {
      try {
        const result = await stopVideoRecording();
        if (mountedRef.current) {
          setIsRecordingVideo(false);
          setLastVideo(result);
          setAction('uploading-video');
          setToast('Uploading recording…');
        }
        const config = YaverFeedback.getConfig();
        if (!config?.agentUrl) {
          throw new Error('Not connected to the agent yet.');
        }
        const { Dimensions } = require('react-native');
        const { width, height } = Dimensions.get('window');
        const deviceInfo: DeviceInfo = {
          platform: Platform.OS,
          osVersion: String(Platform.Version),
          model: Platform.OS === 'ios' ? 'iOS Device' : 'Android Device',
          screenWidth: width,
          screenHeight: height,
        };
        const bundle: FeedbackBundle = {
          metadata: {
            timestamp: new Date().toISOString(),
            device: deviceInfo,
            app: {},
            userNote: '[Screen recording]',
          },
          screenshots: [],
          video: result.path,
          errors: YaverFeedback.getCapturedErrors().length
            ? YaverFeedback.getCapturedErrors()
            : undefined,
        };
        await uploadFeedback(config.agentUrl, config.authToken ?? '', bundle);
        if (mountedRef.current) {
          setToast(`Recording uploaded — ${Math.round(result.duration)}s`);
          setLastVideo(null);
        }
        closeSoon(1200);
      } catch (err: unknown) {
        setIsRecordingVideo(false);
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (mountedRef.current) setAction('idle');
      }
    } else {
      try {
        await startVideoRecording();
        if (mountedRef.current) {
          setIsRecordingVideo(true);
          setToast('Recording… tap again to stop and upload');
          setLastVideo(null);
        }
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : String(err));
      }
    }
  }, [closeSoon, isRecordingVideo, lastVideo]);

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
            <Pressable style={styles.modal} onPress={(e) => e.stopPropagation()}>
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

              {/* Screenshot / Upload */}
              {!showCaptureChoices ? (
                <ActionRow
                  label={
                    action === 'capturing'
                      ? 'Working…'
                      : 'Screenshot / Upload'
                  }
                  tint="#22c55e"
                  onPress={handleCaptureChoiceToggle}
                  disabled={busy}
                  busy={action === 'capturing'}
                />
              ) : (
                <View style={styles.captureChoices}>
                  <ActionRow
                    label="Take Screenshot"
                    tint="#22c55e"
                    onPress={handleScreenshotAndFix}
                    disabled={busy}
                  />
                  <ActionRow
                    label="Upload File"
                    tint="#34d399"
                    onPress={handleFileUpload}
                    disabled={busy}
                  />
                </View>
              )}

              {/* 4. Screen recording */}
              <ActionRow
                label={
                  action === 'uploading-video'
                    ? 'Uploading…'
                    : isRecordingVideo
                      ? 'Stop & Upload Recording'
                      : lastVideo
                        ? `Retry Upload Recording · ${Math.round(lastVideo.duration)}s`
                        : 'Screen Recording'
                }
                tint={isRecordingVideo ? '#ef4444' : '#60a5fa'}
                onPress={handleScreenRecording}
                disabled={busy && action !== 'uploading-video' && !isRecordingVideo}
                busy={action === 'uploading-video'}
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

              {/* Quick-icon toggle. The user's three ways to control
                  the floating icon are: (1) long-press the icon →
                  Hide, (2) tap this row to toggle it on/off, (3) shake
                  → this modal → tap this row. Shake is the unkillable
                  back-door when the icon is hidden and the dev hasn't
                  exposed their own settings UI. */}
              <Pressable
                onPress={async () => {
                  const next = !quickIconHidden;
                  setQuickIconHidden(next);
                  await YaverFeedback.setQuickIconVisible(!next);
                }}
                style={({ pressed }) => [
                  styles.quickIconToggle,
                  pressed && { opacity: 0.7 },
                ]}
                accessibilityRole="button"
                accessibilityLabel={
                  quickIconHidden ? 'Show quick icon' : 'Hide quick icon'
                }
              >
                <Text style={styles.quickIconToggleText}>
                  {quickIconHidden
                    ? '◯  Show quick-access icon'
                    : '●  Hide quick-access icon'}
                </Text>
              </Pressable>

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
            </Pressable>
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
  modal: {
    backgroundColor: '#141422',
    borderTopLeftRadius: 22,
    borderTopRightRadius: 22,
    padding: 22,
    paddingBottom: 36,
    gap: 12,
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
    marginTop: 4,
    alignSelf: 'center',
    paddingVertical: 6,
    paddingHorizontal: 12,
  },
  quickIconToggleText: {
    color: '#9ca3af',
    fontSize: 12,
    fontWeight: '500',
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
