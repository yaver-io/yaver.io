import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  DeviceEventEmitter,
  Modal,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { YaverFeedback } from './YaverFeedback';
import {
  captureScreenshot,
  startVideoRecording,
  stopVideoRecording,
} from './capture';
import { uploadFeedback } from './upload';
import { DeviceInfo, FeedbackBundle } from './types';
import { AuthOverlay } from './AuthOverlay';

/**
 * Simplified feedback modal — 5 actions:
 *
 *  1. Hot Reload               — instant JS reload (most common use case)
 *  2. Screenshot + Fix         — capture the underlying app (modal hidden
 *                                during capture), attach errors, trigger
 *                                a fix task on the agent
 *  3. Vibing                   — open a vibing session on the agent
 *  4. Start / Stop Recording   — screen-recording toggle
 *  5. Send Video               — submit the last recorded video
 *
 * The header has an explicit X close icon on the right.
 * Live / Narrated / Batch modes, voice notes, and the streaming indicator
 * were removed in 0.7.0 — those flows never worked end-to-end against
 * the Go agent (see MISSINGS_FEEDBACK_SDK.md).
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
  | 'sending-video';

export const FeedbackModal: React.FC = () => {
  const [visible, setVisible] = useState(false);
  const [action, setAction] = useState<ActionState>('idle');
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [isRecordingVideo, setIsRecordingVideo] = useState(false);
  const [lastVideo, setLastVideo] = useState<LastVideo | null>(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    const sub = DeviceEventEmitter.addListener('yaverFeedback:startReport', () => {
      if (YaverFeedback.isEnabled()) {
        setVisible(true);
        setError(null);
        setToast(null);
        setAction('idle');
      }
    });
    // Agent streams build / compile progress through the BlackBox
    // SSE command channel as `command: "status"`; YaverFeedback re-emits
    // it as `yaverFeedback:status`. Show the most recent message in the
    // toast so a multi-second rebuild feels like "working" instead of
    // "stuck".
    const statusSub = DeviceEventEmitter.addListener(
      'yaverFeedback:status',
      (payload: { message?: string; phase?: string }) => {
        if (!mountedRef.current) return;
        const msg = payload?.message || payload?.phase || '';
        if (msg) setToast(msg);
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
    try {
      await runWithReconnect(async (client) => {
        await client.reloadApp('dev');
      });
      setToast('Reload sent');
      closeSoon(800);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (mountedRef.current) setAction('idle');
    }
  }, [closeSoon, runWithReconnect]);

  // ─── 2. Screenshot + Fix ───────────────────────────────────────────
  //
  // Hide the modal first so the screenshot captures the actual screen
  // (the bug) — not the modal card. Await a short animation delay,
  // snapshot, upload the feedback bundle with any buffered errors, then
  // kick `/feedback/{id}/fix` to create the repair task.
  const handleScreenshotAndFix = useCallback(async () => {
    const client = YaverFeedback.getP2PClient();
    const config = YaverFeedback.getConfig();
    if (!client || !config?.agentUrl) {
      setError('Not connected to the agent yet.');
      return;
    }
    setAction('capturing');
    setError(null);

    // Step 1: Hide the modal so the screenshot contains the real screen.
    setVisible(false);
    // Wait out the slide-down animation on both platforms.
    await new Promise((resolve) => setTimeout(resolve, 350));

    let path: string | null = null;
    try {
      path = await captureScreenshot();
    } catch (err: unknown) {
      setVisible(true);
      setError(err instanceof Error ? err.message : String(err));
      setAction('idle');
      return;
    }

    // Step 2: Re-show the modal for progress + ack.
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
      if (reportId) {
        try {
          await client.triggerFix(reportId);
          setToast('Fix task started');
        } catch (err: unknown) {
          setToast('Report uploaded — fix trigger failed');
          setError(err instanceof Error ? err.message : String(err));
        }
      } else {
        setToast('Report uploaded');
      }
      closeSoon(1400);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (mountedRef.current) setAction('idle');
    }
  }, [closeSoon]);

  // ─── 3. Vibing ─────────────────────────────────────────────────────
  const handleVibing = useCallback(async () => {
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
      const prompt =
        'The user opened the feedback modal on their phone and tapped Vibing. ' +
        'Investigate whatever they are likely to be asking about — pick the ' +
        'next small improvement or fix based on recent activity and the ' +
        'current screen.' +
        errNote;
      await client.vibing(prompt);
      setToast('Vibing task created');
      closeSoon(1200);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (mountedRef.current) setAction('idle');
    }
  }, [closeSoon]);

  // ─── 4. Toggle screen recording ────────────────────────────────────
  const handleToggleRecording = useCallback(async () => {
    setError(null);
    if (isRecordingVideo) {
      try {
        const result = await stopVideoRecording();
        if (mountedRef.current) {
          setIsRecordingVideo(false);
          setLastVideo(result);
          setToast(`Recording stopped — ${Math.round(result.duration)}s`);
        }
      } catch (err: unknown) {
        setIsRecordingVideo(false);
        setError(err instanceof Error ? err.message : String(err));
      }
    } else {
      try {
        await startVideoRecording();
        if (mountedRef.current) {
          setIsRecordingVideo(true);
          setToast('Recording…');
          setLastVideo(null);
        }
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : String(err));
      }
    }
  }, [isRecordingVideo]);

  // ─── 5. Send the last recorded video ───────────────────────────────
  const handleSendVideo = useCallback(async () => {
    const config = YaverFeedback.getConfig();
    if (!config?.agentUrl) {
      setError('Not connected to the agent yet.');
      return;
    }
    if (!lastVideo) {
      setError('No video recorded yet.');
      return;
    }
    setAction('sending-video');
    setError(null);
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
      setToast('Video sent');
      setLastVideo(null);
      closeSoon(1200);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (mountedRef.current) setAction('idle');
    }
  }, [lastVideo, closeSoon]);

  const busy = action !== 'idle';

  return (
    <>
      <AuthOverlay />
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

              {/* 2. Screenshot + Fix — for bug fixes */}
              <ActionRow
                label={
                  action === 'capturing'
                    ? 'Capturing…'
                    : 'Screenshot & Fix'
                }
                tint="#22c55e"
                onPress={handleScreenshotAndFix}
                disabled={busy}
                busy={action === 'capturing'}
              />

              {/* 3. Vibing */}
              <ActionRow
                label={action === 'vibing' ? 'Starting…' : 'Vibing'}
                tint="#818cf8"
                onPress={handleVibing}
                disabled={busy}
                busy={action === 'vibing'}
              />

              {/* 4. Start/Stop Recording */}
              <ActionRow
                label={isRecordingVideo ? 'Stop Recording' : 'Start Recording'}
                tint={isRecordingVideo ? '#ef4444' : '#60a5fa'}
                onPress={handleToggleRecording}
                disabled={busy && action !== 'idle' && !isRecordingVideo}
              />

              {/* 5. Send Video (only tappable when a clip is ready) */}
              <ActionRow
                label={
                  action === 'sending-video'
                    ? 'Sending…'
                    : lastVideo
                      ? `Send Video · ${Math.round(lastVideo.duration)}s`
                      : 'Send Video'
                }
                tint="#a78bfa"
                onPress={handleSendVideo}
                disabled={busy || !lastVideo}
                busy={action === 'sending-video'}
              />

              {toast && <Text style={styles.toast}>{toast}</Text>}
              {error && <Text style={styles.error}>{error}</Text>}
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
});
