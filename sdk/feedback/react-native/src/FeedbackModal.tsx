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
  useWindowDimensions,
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
import { resolveReportIdentity } from './P2PClient';
import {
  DeviceInfo,
  FeedbackBundle,
  OpenCodeConfigSummary,
  OpenCodeProviderSummary,
  RunnerAuthStatusRow,
} from './types';
import { AuthOverlay } from './AuthOverlay';
import { QuickActionIcon } from './QuickActionIcon';
import { VibeChatScreen } from './VibeChatScreen';
import { DeployPanel } from './DeployPanel';
import { listReachableDevices, RemoteDevice } from './auth';
import {
  QUICK_ICON_COLOR_PRESETS,
  QuickIconColorPreset,
  getPreferredModel,
  getPreferredRunner,
  setPreferredModel,
  setPreferredRunner,
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

type RunnerTone = 'ok' | 'warning' | 'error' | 'neutral';

type RunnerCardState = {
  id: string;
  name: string;
  installed: boolean;
  authConfigured: boolean;
  ready: boolean;
  version?: string;
  tone: RunnerTone;
  statusLine: string;
  detail?: string;
  actionLabel?: string;
  actionRunner?: string;
};

type ProviderEditorState = {
  mode: 'add' | 'edit';
  id: string;
  name: string;
  baseUrl: string;
  apiKey: string;
};

const PRIMARY_RUNNER_IDS = ['claude', 'codex', 'opencode'] as const;

function normalizeRunnerStatusRows(rows: RunnerAuthStatusRow[]): RunnerCardState[] {
  const byId = new Map<string, RunnerAuthStatusRow>();
  for (const row of rows) {
    const raw = String(row.id || '').trim().toLowerCase();
    if (!raw) continue;
    const normalized = raw === 'claude-code' ? 'claude' : raw;
    if (!PRIMARY_RUNNER_IDS.includes(normalized as (typeof PRIMARY_RUNNER_IDS)[number])) continue;
    byId.set(normalized, { ...row, id: normalized });
  }

  return PRIMARY_RUNNER_IDS.map((id) => {
    const baseName =
      id === 'claude' ? 'Claude Code' : id === 'codex' ? 'OpenAI Codex' : 'OpenCode';
    const row = byId.get(id);
    if (!row) {
      return {
        id,
        name: baseName,
        installed: false,
        authConfigured: false,
        ready: false,
        tone: 'warning',
        statusLine: 'Not installed on the selected machine',
      };
    }

    const versionPrefix = row.version?.trim() ? `${row.version.trim()} · ` : '';
    const detail = row.error?.trim() || row.warning?.trim() || row.detail?.trim() || undefined;

    if (!row.installed) {
      return {
        id,
        name: row.name || baseName,
        installed: false,
        authConfigured: false,
        ready: false,
        version: row.version,
        tone: 'warning',
        statusLine: 'Not installed on the selected machine',
        detail,
      };
    }

    if (id === 'opencode') {
      const configured = row.authConfigured || row.ready;
      return {
        id,
        name: row.name || baseName,
        installed: row.installed,
        authConfigured: row.authConfigured,
        ready: row.ready,
        version: row.version,
        tone: configured ? 'ok' : 'warning',
        statusLine: configured
          ? `${versionPrefix}Configured on the selected machine`
          : `${versionPrefix}Needs provider config on the selected machine`,
        detail,
      };
    }

    const authed = row.authConfigured || row.ready;
    return {
      id,
      name: row.name || baseName,
      installed: row.installed,
      authConfigured: row.authConfigured,
      ready: row.ready,
      version: row.version,
      tone: authed ? 'ok' : 'warning',
      statusLine: authed
        ? `${versionPrefix}Signed in on the selected machine`
        : `${versionPrefix}Not signed in on the selected machine`,
      detail,
      actionLabel: authed ? 'Re-auth' : 'Sign in',
      actionRunner: id,
    };
  });
}

export const FeedbackModal: React.FC = () => {
  const { width: winW, height: winH } = useWindowDimensions();
  const isTablet = Math.min(winW, winH) >= 600;
  // Tablet color/icon picker fans out to 5/6 cols — 31% (3-col)
  // looks empty on a 1024pt iPad. Mobile keeps 3-col.
  const iconOptionWidthOverride = isTablet ? '18%' : undefined;
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
  const [runnerAuthModal, setRunnerAuthModal] = useState<string | null>(null);
  // Vibing-input mode: same expand-on-tap pattern as email login.
  // Tap "Vibing" once → the button reveals an input + Send; that lets
  // the user say WHAT they want to vibe on instead of firing a canned
  // "pick something for me" prompt (which in 0.7.13 pointed Claude at
  // the wrong project because the matcher grepped the prompt itself).
  const [showVibeInput, setShowVibeInput] = useState(false);
  const [showDeploy, setShowDeploy] = useState(false);
  const [vibePrompt, setVibePrompt] = useState('');
  const [lastVibeTaskId, setLastVibeTaskId] = useState<string | null>(null);
  const [quickIconColorPreset, setQuickIconColorPreset] =
    useState<QuickIconColorPreset | null>(null);
  const [keyboardInset, setKeyboardInset] = useState(0);
  const [machineCard, setMachineCard] = useState<MachineCardState>({
    device: null,
    reachable: null,
    loading: false,
    status: 'none',
    title: 'No machine selected',
    detail: 'Pick a remote dev machine before using the feedback actions.',
  });
  const [runnerCards, setRunnerCards] = useState<RunnerCardState[]>(() =>
    normalizeRunnerStatusRows([]),
  );
  const [runnerStatusLoading, setRunnerStatusLoading] = useState(false);
  const [runnerStatusError, setRunnerStatusError] = useState<string | null>(null);
  const [preferredRunner, setPreferredRunnerState] = useState<string | null>(null);
  const [preferredModel, setPreferredModelState] = useState('');
  const [showOpenCodeConfig, setShowOpenCodeConfig] = useState(false);
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

  const loadRunnerStatuses = useCallback(async () => {
    const cfg = YaverFeedback.getConfig();
    if (!cfg?.authToken) {
      if (mountedRef.current) {
        setRunnerCards(normalizeRunnerStatusRows([]));
        setRunnerStatusError('Sign in to inspect coding-agent status.');
        setRunnerStatusLoading(false);
      }
      return;
    }
    if (!cfg.preferredDeviceId) {
      if (mountedRef.current) {
        setRunnerCards(normalizeRunnerStatusRows([]));
        setRunnerStatusError('Pick a machine to inspect coding-agent status.');
        setRunnerStatusLoading(false);
      }
      return;
    }

    if (mountedRef.current) {
      setRunnerStatusLoading(true);
      setRunnerStatusError(null);
    }

    try {
      let client = YaverFeedback.getP2PClient();
      if (!client) {
        const ok = await YaverFeedback.reconnect();
        if (ok) client = YaverFeedback.getP2PClient();
      }
      if (!client) {
        throw new Error('Not connected to the selected machine yet.');
      }
      const rows = await client.getRunnerAuthStatus();
      if (mountedRef.current) {
        setRunnerCards(normalizeRunnerStatusRows(rows));
      }
    } catch (err) {
      if (mountedRef.current) {
        setRunnerCards(normalizeRunnerStatusRows([]));
        setRunnerStatusError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      if (mountedRef.current) setRunnerStatusLoading(false);
    }
  }, []);

  const loadRoutingPrefs = useCallback(async () => {
    try {
      const [runner, model] = await Promise.all([
        getPreferredRunner(),
        getPreferredModel(),
      ]);
      if (!mountedRef.current) return;
      setPreferredRunnerState(runner);
      setPreferredModelState(model ?? '');
    } catch {
      if (!mountedRef.current) return;
      setPreferredRunnerState(null);
      setPreferredModelState('');
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
        void loadRunnerStatuses();
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
  }, [loadRunnerStatuses, loadSelectedMachine]);

  useEffect(() => {
    if (!visible) return;
    const interval = setInterval(() => {
      void loadSelectedMachine();
      void loadRunnerStatuses();
    }, 5000);
    return () => clearInterval(interval);
  }, [loadRunnerStatuses, loadSelectedMachine, visible]);

  useEffect(() => {
    if (!visible) {
      setKeyboardInset(0);
      return;
    }

    const showEvent = Platform.OS === 'ios' ? 'keyboardWillShow' : 'keyboardDidShow';
    const hideEvent = Platform.OS === 'ios' ? 'keyboardWillHide' : 'keyboardDidHide';
    const showSub = Keyboard.addListener(showEvent, (event) => {
      setKeyboardInset(event.endCoordinates?.height ?? 0);
    });
    const hideSub = Keyboard.addListener(hideEvent, () => {
      setKeyboardInset(0);
    });
    return () => {
      showSub.remove();
      hideSub.remove();
    };
  }, [visible]);

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
    setRunnerStatusError(null);
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
        YaverFeedback.getRelayPassword(),
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
      const cfg = YaverFeedback.getConfig();
      const identity = resolveReportIdentity({
        projectName: cfg?.projectName,
        bundleId: cfg?.bundleId,
      });
      const deviceInfo: DeviceInfo = {
        platform: Platform.OS,
        osVersion: String(Platform.Version),
        model: Platform.OS === 'ios' ? 'iOS Device' : 'Android Device',
        screenWidth: width,
        screenHeight: height,
        appName: identity.appName,
      };
      const capturedErrors = YaverFeedback.getCapturedErrors();
      const bundle: FeedbackBundle = {
        metadata: {
          timestamp: new Date().toISOString(),
          deviceInfo,
          app: identity.app,
          project: identity.project,
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
  const handleVibingButton = useCallback(async () => {
    if (!showVibeInput) {
      const client = YaverFeedback.getP2PClient();
      if (!client) {
        setError('Not connected to the agent yet.');
        return;
      }
      setError(null);
      try {
        const eligibility = await client.getVibingEligibility();
        if (!eligibility.canVibe) {
          const message =
            eligibility.guidance && eligibility.guidance.trim()
              ? `${eligibility.reason ?? 'Vibe coding is unavailable.'} ${eligibility.guidance}`
              : eligibility.reason ?? 'Vibe coding is unavailable.';
          setError(message);
          setToast('Vibe coding unavailable for this project.');
          return;
        }
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : String(err));
        return;
      }
      setShowVibeInput(true);
      return;
    }
    // collapse if tapped again with empty input
    if (!vibePrompt.trim()) {
      setShowVibeInput(false);
    }
  }, [showVibeInput, vibePrompt]);

  // Hold the active vibe-chat session — set when handleVibingSubmit
  // returns a fresh taskId. Renders <VibeChatScreen> which streams the
  // SSE transcript, supports multi-turn follow-ups via /tasks/{id}/
  // resume, and exposes a Reload button. Mirrors the in-Yaver native
  // pane's transcript-mode behaviour, just rendered in RN here.
  const [activeVibe, setActiveVibe] = useState<{
    taskId: string;
    initialPrompt: string;
    project?: string;
    runner?: string;
    model?: string;
  } | null>(null);
  const [includeScreenshot, setIncludeScreenshot] = useState<boolean>(true);

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
      const promptText = userPrompt
        ? userPrompt + errNote
        : 'Pick the next small improvement or fix for this app based on recent activity and the current screen.' +
          errNote;

      // Optional screenshot — captured from the host app's window.
      // captureScreenshotBase64 returns null when react-native-view-
      // shot isn't installed; we skip the screenshot rather than
      // abort the whole feedback in that case.
      let screenshotBase64: string | undefined;
      if (includeScreenshot) {
        const cap = await import('./capture');
        const captured = await cap.captureScreenshotBase64();
        if (captured?.base64) {
          screenshotBase64 = captured.base64;
        }
      }

      // Resolve project context the same way reloadApp / vibing did.
      const { resolveAppIdentity } = await import('./P2PClient');
      const identity = resolveAppIdentity();

      // Pull the user's preferred runner / model from local prefs.
      // Both are optional — the agent falls back to whatever runner
      // is signed in if neither is provided.
      const prefs = await import('./preferences');
      const preferredRunner = (await prefs.getPreferredRunner?.()) ?? null;
      const preferredModel = (await prefs.getPreferredModel?.()) ?? null;

      const result = await client.createFeedbackTask({
        userPrompt: promptText,
        projectName: identity.projectName,
        projectPath: identity.projectPath,
        runner: preferredRunner ?? undefined,
        model: preferredModel ?? undefined,
        screenshotBase64,
      });
      setLastVibeTaskId(result.taskId);
      // Hand off to VibeChatScreen — it streams the SSE transcript,
      // accepts follow-ups, and surfaces a Reload button.
      setActiveVibe({
        taskId: result.taskId,
        initialPrompt: promptText,
        project: identity.projectName,
        runner: preferredRunner ?? undefined,
        model: preferredModel ?? undefined,
      });
      setVibePrompt('');
      setShowVibeInput(false);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (mountedRef.current) setAction('idle');
    }
  }, [vibePrompt, includeScreenshot]);

  /*
  const handleScreenRecording = useCallback(async () => {
    ...
  }, [closeSoon, isRecordingVideo, lastVideo]);
  */

  const busy = action !== 'idle';
  const readyRunnerCount = runnerCards.filter((row) => row.ready || row.authConfigured).length;
  const missingRunnerCount = runnerCards.filter((row) => !row.installed).length;
  const needsAuthRunnerCount = runnerCards.filter(
    (row) => row.installed && !row.authConfigured && !row.ready,
  ).length;

  // Once the user fires off a vibe task, swap the entire modal body
  // for the live chat screen. The chat manages its own SSE
  // subscription, multi-turn follow-ups, and Reload button. Closing
  // the chat returns to idle and clears the active vibe.
  if (visible && activeVibe) {
    const client = YaverFeedback.getP2PClient();
    return (
      <>
        <AuthOverlay />
        <QuickActionIcon />
        <Modal
          visible={visible}
          animationType="slide"
          transparent
          onRequestClose={() => setActiveVibe(null)}
        >
          {client ? (
            <VibeChatScreen
              client={client}
              initialTaskId={activeVibe.taskId}
              initialUserPrompt={activeVibe.initialPrompt}
              project={activeVibe.project}
              runner={activeVibe.runner}
              model={activeVibe.model}
              onClose={() => setActiveVibe(null)}
              onReload={async () => {
                const c = YaverFeedback.getP2PClient();
                if (!c) throw new Error('Not connected');
                await c.reloadApp();
              }}
            />
          ) : null}
        </Modal>
      </>
    );
  }

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
              keyboardVerticalOffset={Platform.OS === 'ios' ? 12 : 0}
              style={styles.kbAvoider}
              pointerEvents="box-none"
            >
            <Pressable
              // Tablet: cap modal width and center as a card-style
              // sheet rather than a phone bottom sheet that stretches
              // across a 12.9" iPad. Phone behaviour unchanged.
              style={[
                styles.modal,
                isTablet
                  ? {
                      width: '100%',
                      maxWidth: 640,
                      alignSelf: 'center',
                      borderTopLeftRadius: 22,
                      borderTopRightRadius: 22,
                    }
                  : null,
              ]}
              onPress={(e) => {
                e.stopPropagation();
                Keyboard.dismiss();
              }}
            >
              <ScrollView
                style={styles.scroll}
                contentContainerStyle={[
                  styles.scrollContent,
                  showVibeInput && keyboardInset > 0
                    ? { paddingBottom: 8 + keyboardInset }
                    : null,
                ]}
                keyboardShouldPersistTaps="handled"
                keyboardDismissMode={Platform.OS === 'ios' ? 'interactive' : 'on-drag'}
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

              <View style={styles.runnerSection}>
                <View style={styles.runnerSectionHeader}>
                  <View style={{ flex: 1 }}>
                    <Text style={styles.runnerSectionTitle}>Coding Agents</Text>
                    <Text style={styles.runnerSectionSubtitle}>
                      {runnerStatusLoading
                        ? 'Refreshing runner status on the selected machine…'
                        : `${readyRunnerCount} ready · ${needsAuthRunnerCount} need sign-in · ${missingRunnerCount} missing`}
                    </Text>
                  </View>
                  <Pressable
                    onPress={() => void loadRunnerStatuses()}
                    style={({ pressed }) => [
                      styles.runnerRefreshBtn,
                      pressed && styles.buttonPressed,
                    ]}
                    accessibilityRole="button"
                    accessibilityLabel="Refresh coding-agent status"
                  >
                    <Text style={styles.runnerRefreshBtnText}>
                      {runnerStatusLoading ? 'Refreshing…' : 'Refresh'}
                    </Text>
                  </Pressable>
                </View>

                {runnerCards.map((row) => (
                  <View
                    key={row.id}
                    style={[
                      styles.runnerCard,
                      row.tone === 'ok' && styles.runnerCardOk,
                      row.tone === 'warning' && styles.runnerCardWarning,
                      row.tone === 'error' && styles.runnerCardError,
                    ]}
                  >
                    <View style={styles.runnerCardTop}>
                      <View style={{ flex: 1 }}>
                        <Text style={styles.runnerCardTitle}>{row.name}</Text>
                        <Text
                          style={[
                            styles.runnerCardStatus,
                            row.tone === 'ok' && styles.runnerCardStatusOk,
                            row.tone === 'warning' && styles.runnerCardStatusWarning,
                            row.tone === 'error' && styles.runnerCardStatusError,
                          ]}
                        >
                          {row.statusLine}
                        </Text>
                      </View>
                      {row.actionRunner ? (
                        <Pressable
                          onPress={() => setRunnerAuthModal(row.actionRunner ?? null)}
                          style={({ pressed }) => [
                            styles.runnerActionBtn,
                            pressed && styles.buttonPressed,
                          ]}
                          accessibilityRole="button"
                          accessibilityLabel={`${row.actionLabel} ${row.name}`}
                        >
                          <Text style={styles.runnerActionBtnText}>{row.actionLabel}</Text>
                        </Pressable>
                      ) : null}
                    </View>
                    {row.detail ? (
                      <Text style={styles.runnerCardDetail}>{row.detail}</Text>
                    ) : null}
                  </View>
                ))}

                {runnerStatusError ? (
                  <Text style={styles.runnerSectionError}>{runnerStatusError}</Text>
                ) : null}
              </View>

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
                          iconOptionWidthOverride ? { width: iconOptionWidthOverride } : null,
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

              {/* Deploy — opens an inline panel that talks to
                  /fleet/deploy-options on the agent and lets the user
                  pick TestFlight / Play / Both, then a machine to run
                  it on. Capabilities (e.g. "Linux can't TestFlight")
                  come from the agent's doctor probes — no client-side
                  platform smarts here. */}
              {!showDeploy ? (
                <ActionRow
                  label="Deploy"
                  tint="#7f8cf7"
                  onPress={() => setShowDeploy(true)}
                  disabled={busy}
                />
              ) : (
                <DeployPanel onClose={() => setShowDeploy(false)} />
              )}

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
      {runnerAuthModal ? (
        <RunnerAuthNativeModal
          runner={runnerAuthModal}
          onClose={() => {
            setRunnerAuthModal(null);
            void loadRunnerStatuses();
          }}
        />
      ) : null}
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
  runnerSection: {
    marginTop: 2,
    gap: 10,
  },
  runnerSectionHeader: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
  },
  runnerSectionTitle: {
    color: '#f8fafc',
    fontSize: 16,
    fontWeight: '700',
  },
  runnerSectionSubtitle: {
    marginTop: 2,
    color: '#94a3b8',
    fontSize: 12,
  },
  runnerRefreshBtn: {
    borderRadius: 10,
    borderWidth: 1,
    borderColor: 'rgba(148,163,184,0.22)',
    backgroundColor: 'rgba(15,23,42,0.65)',
    paddingHorizontal: 10,
    paddingVertical: 8,
  },
  runnerRefreshBtnText: {
    color: '#cbd5e1',
    fontSize: 12,
    fontWeight: '600',
  },
  runnerCard: {
    borderRadius: 12,
    borderWidth: 1,
    borderColor: 'rgba(148,163,184,0.14)',
    backgroundColor: 'rgba(15,23,42,0.45)',
    paddingHorizontal: 12,
    paddingVertical: 11,
    gap: 6,
  },
  runnerCardOk: {
    borderColor: 'rgba(34,197,94,0.28)',
    backgroundColor: 'rgba(20,83,45,0.20)',
  },
  runnerCardWarning: {
    borderColor: 'rgba(251,191,36,0.28)',
    backgroundColor: 'rgba(120,53,15,0.18)',
  },
  runnerCardError: {
    borderColor: 'rgba(248,113,113,0.28)',
    backgroundColor: 'rgba(127,29,29,0.18)',
  },
  runnerCardTop: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
  },
  runnerCardTitle: {
    color: '#f8fafc',
    fontSize: 14,
    fontWeight: '700',
  },
  runnerCardStatus: {
    marginTop: 2,
    fontSize: 12,
    color: '#cbd5e1',
  },
  runnerCardStatusOk: {
    color: '#86efac',
  },
  runnerCardStatusWarning: {
    color: '#fcd34d',
  },
  runnerCardStatusError: {
    color: '#fca5a5',
  },
  runnerCardDetail: {
    color: '#94a3b8',
    fontSize: 11,
    lineHeight: 16,
  },
  runnerActionBtn: {
    borderRadius: 10,
    borderWidth: 1,
    borderColor: 'rgba(129,140,248,0.35)',
    backgroundColor: 'rgba(67,56,202,0.22)',
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  runnerActionBtnText: {
    color: '#c7d2fe',
    fontSize: 12,
    fontWeight: '700',
  },
  runnerSectionError: {
    color: '#fca5a5',
    fontSize: 12,
    lineHeight: 18,
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

/**
 * Minimal native modal for the codex/claude remote sign-in flow. Opens
 * the device-auth session on the connected agent, surfaces the
 * verification URL + one-time code, polls every 1.5 s, and turns green
 * the moment the CLI writes its auth.json. No API keys, no SSH.
 */
const RunnerAuthNativeModal: React.FC<{
  runner: string;
  onClose: () => void;
}> = ({ runner, onClose }) => {
  const [session, setSession] = useState<import('./types').RunnerBrowserAuthSession | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [pasteCode, setPasteCode] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const startedRef = useRef(false);
  // Claude is the only runner that needs the user to paste a verifier
  // code back from platform.claude.com's callback page; Codex device-
  // auth and OpenCode (no OAuth at all) bypass this. Mirrors the
  // requiresPasteBack check in iOS YaverRunnerAuthFlowPane.swift.
  const needsPasteBack = runner === 'claude' || runner === 'claude-code';

  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;
    (async () => {
      try {
        const s = await YaverFeedback.startRunnerBrowserAuth(runner);
        setSession(s);
      } catch (err) {
        setStartError(err instanceof Error ? err.message : String(err));
      }
    })();
  }, [runner]);

  useEffect(() => {
    if (!session) return;
    if (['completed', 'failed', 'cancelled'].includes(session.status)) return;
    const iv = setInterval(async () => {
      try {
        const s = await YaverFeedback.getRunnerBrowserAuthStatus(session.id);
        setSession(s);
      } catch {
        // keep polling
      }
    }, 1500);
    return () => clearInterval(iv);
  }, [session?.id, session?.status]);

  const terminal = session && ['completed', 'failed', 'cancelled'].includes(session.status);
  const runnerLabel = runner === 'codex' ? 'OpenAI Codex' : runner === 'claude' ? 'Claude Code' : runner;

  const handleClose = () => {
    if (session && !terminal) {
      YaverFeedback.cancelRunnerBrowserAuth(session.id).catch(() => {});
    }
    onClose();
  };

  const copyCode = () => {
    if (!session?.code) return;
    try {
      // Avoid a hard Clipboard dep — host app can polyfill.
      const Clipboard = require('react-native').Clipboard;
      if (Clipboard?.setString) {
        Clipboard.setString(session.code);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }
    } catch {
      // best-effort — code is visible on screen regardless
    }
  };

  const openUrl = () => {
    if (!session?.openUrl) return;
    try {
      const { Linking } = require('react-native');
      Linking.openURL(session.openUrl).catch(() => {});
    } catch {
      /* ignore */
    }
  };

  return (
    <Modal visible={true} transparent animationType="fade" onRequestClose={handleClose}>
      <View style={runnerAuthModalStyles.overlay}>
        <View style={runnerAuthModalStyles.card}>
          <View style={runnerAuthModalStyles.header}>
            <View style={{ flex: 1 }}>
              <Text style={runnerAuthModalStyles.title}>Sign in to {runnerLabel}</Text>
              <Text style={runnerAuthModalStyles.subtitle}>
                Opens a one-time URL + code. Enter it in any browser.
              </Text>
            </View>
            <Pressable onPress={handleClose} hitSlop={10}>
              <Text style={runnerAuthModalStyles.close}>×</Text>
            </Pressable>
          </View>

          {startError ? (
            <View style={runnerAuthModalStyles.errorBox}>
              <Text style={runnerAuthModalStyles.errorTitle}>Couldn't start</Text>
              <Text style={runnerAuthModalStyles.errorBody}>{startError}</Text>
            </View>
          ) : !session ? (
            <Text style={runnerAuthModalStyles.dim}>
              Starting the sign-in flow on the remote machine…
            </Text>
          ) : session.status === 'completed' ? (
            <View style={runnerAuthModalStyles.successBox}>
              <Text style={runnerAuthModalStyles.successTitle}>✓ Signed in</Text>
              <Text style={runnerAuthModalStyles.successBody}>
                {session.detail || 'Auth stored on the remote machine.'}
              </Text>
            </View>
          ) : session.status === 'failed' || session.status === 'cancelled' ? (
            <View style={runnerAuthModalStyles.errorBox}>
              <Text style={runnerAuthModalStyles.errorTitle}>
                {session.status === 'cancelled' ? 'Cancelled' : 'Failed'}
              </Text>
              <Text style={runnerAuthModalStyles.errorBody}>
                {session.error || session.detail || 'The CLI exited before sign-in completed.'}
              </Text>
            </View>
          ) : (
            <View>
              {session.openUrl ? (
                <Pressable onPress={openUrl} style={runnerAuthModalStyles.urlBox}>
                  <Text style={runnerAuthModalStyles.urlText} numberOfLines={2}>
                    ↗ {session.openUrl}
                  </Text>
                </Pressable>
              ) : (
                <Text style={runnerAuthModalStyles.dim}>
                  Waiting for verification URL from the remote CLI…
                </Text>
              )}
              {session.code ? (
                <View style={{ marginTop: 12 }}>
                  <Text style={runnerAuthModalStyles.codeLabel}>ENTER THIS CODE</Text>
                  <Pressable onPress={copyCode} style={runnerAuthModalStyles.codeBox}>
                    <Text style={runnerAuthModalStyles.codeText}>{session.code}</Text>
                    <Text style={runnerAuthModalStyles.codeHint}>
                      {copied ? 'copied' : 'tap to copy'}
                    </Text>
                  </Pressable>
                </View>
              ) : null}
              {needsPasteBack ? (
                <View style={{ marginTop: 14 }}>
                  <Text style={runnerAuthModalStyles.codeLabel}>
                    PASTE CODE FROM CLAUDE.COM
                  </Text>
                  <View style={{ flexDirection: 'row', gap: 8, marginTop: 6 }}>
                    <View
                      style={{
                        flex: 1,
                        backgroundColor: 'rgba(148,163,184,0.10)',
                        borderRadius: 10,
                        paddingHorizontal: 10,
                      }}
                    >
                      {/* Lazy-import TextInput so the SDK doesn't pull
                          extra surface from react-native at module load. */}
                      {(() => {
                        const { TextInput } = require('react-native');
                        return (
                          <TextInput
                            value={pasteCode}
                            onChangeText={(t: string) => {
                              setPasteCode(t);
                              setSubmitError(null);
                            }}
                            placeholder="paste code here"
                            placeholderTextColor="#64748b"
                            autoCapitalize="none"
                            autoCorrect={false}
                            spellCheck={false}
                            style={{ color: '#f1f5f9', fontSize: 14, paddingVertical: 10 }}
                          />
                        );
                      })()}
                    </View>
                    <Pressable
                      disabled={!pasteCode.trim() || submitting}
                      onPress={async () => {
                        if (!session || !pasteCode.trim()) return;
                        setSubmitting(true);
                        setSubmitError(null);
                        try {
                          const next = await YaverFeedback.submitRunnerBrowserAuthCode(
                            session.id,
                            pasteCode.trim(),
                          );
                          setSession(next);
                          setPasteCode('');
                        } catch (err) {
                          setSubmitError(err instanceof Error ? err.message : String(err));
                        } finally {
                          setSubmitting(false);
                        }
                      }}
                      style={{
                        paddingHorizontal: 14,
                        justifyContent: 'center',
                        backgroundColor:
                          !pasteCode.trim() || submitting
                            ? 'rgba(124,58,237,0.4)'
                            : '#7c3aed',
                        borderRadius: 10,
                      }}
                    >
                      <Text style={{ color: 'white', fontWeight: '600' }}>
                        {submitting ? '…' : 'Submit'}
                      </Text>
                    </Pressable>
                  </View>
                  {submitError ? (
                    <Text
                      style={{
                        marginTop: 6,
                        color: '#fca5a5',
                        fontSize: 12,
                      }}
                    >
                      {submitError}
                    </Text>
                  ) : null}
                </View>
              ) : null}
              <Text style={runnerAuthModalStyles.phishingHint}>
                {needsPasteBack
                  ? 'After authorising on platform.claude.com, copy the code from the callback page and paste it above. Never share this code.'
                  : 'Device codes are a common phishing target. Never share this code. This dialog turns green automatically once sign-in completes.'}
              </Text>
            </View>
          )}
        </View>
      </View>
    </Modal>
  );
};

const runnerAuthModalStyles = StyleSheet.create({
  overlay: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    backgroundColor: 'rgba(2,6,23,0.75)',
    padding: 16,
  },
  card: {
    width: '100%',
    maxWidth: 420,
    backgroundColor: '#0f172a',
    borderRadius: 14,
    borderWidth: 1,
    borderColor: 'rgba(148,163,184,0.18)',
    padding: 18,
  },
  header: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    marginBottom: 12,
  },
  title: { color: '#f1f5f9', fontSize: 16, fontWeight: '600' },
  subtitle: { color: '#94a3b8', fontSize: 11, marginTop: 2 },
  close: { color: '#94a3b8', fontSize: 22, lineHeight: 22, paddingHorizontal: 4 },
  dim: {
    color: '#94a3b8',
    fontSize: 12,
    padding: 12,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: 'rgba(148,163,184,0.2)',
    backgroundColor: 'rgba(15,23,42,0.6)',
  },
  errorBox: {
    padding: 12,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: 'rgba(248,113,113,0.35)',
    backgroundColor: 'rgba(248,113,113,0.1)',
  },
  errorTitle: { color: '#fca5a5', fontWeight: '600', marginBottom: 4, fontSize: 13 },
  errorBody: { color: '#fca5a5', fontSize: 12 },
  successBox: {
    padding: 14,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: 'rgba(34,197,94,0.35)',
    backgroundColor: 'rgba(34,197,94,0.1)',
  },
  successTitle: { color: '#4ade80', fontSize: 14, fontWeight: '600', marginBottom: 4 },
  successBody: { color: '#86efac', fontSize: 12 },
  urlBox: {
    padding: 12,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: 'rgba(99,102,241,0.35)',
    backgroundColor: 'rgba(99,102,241,0.1)',
  },
  urlText: { color: '#c7d2fe', fontSize: 13 },
  codeLabel: {
    fontSize: 10,
    fontWeight: '600',
    color: '#94a3b8',
    letterSpacing: 0.8,
    marginBottom: 4,
  },
  codeBox: {
    padding: 14,
    borderRadius: 10,
    borderWidth: 1,
    borderColor: 'rgba(148,163,184,0.22)',
    backgroundColor: 'rgba(15,23,42,0.8)',
    alignItems: 'center',
  },
  codeText: {
    color: '#f1f5f9',
    fontSize: 22,
    letterSpacing: 6,
    fontFamily: 'Menlo',
  },
  codeHint: { color: '#64748b', fontSize: 10, marginTop: 4, textTransform: 'uppercase' },
  phishingHint: {
    color: '#475569',
    fontSize: 10,
    marginTop: 12,
    lineHeight: 14,
  },
});
