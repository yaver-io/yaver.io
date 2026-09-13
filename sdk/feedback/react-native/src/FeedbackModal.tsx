import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  DeviceEventEmitter,
  KeyboardAvoidingView,
  Linking,
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
  OpenCodeConfigSummary,
  OpenCodeProviderSummary,
  RunnerAuthStatusRow,
} from './types';
import { AuthOverlay } from './AuthOverlay';
import { QuickActionIcon } from './QuickActionIcon';
import { YaverModeBadge } from './YaverModeBadge';
import { VibeChatScreen, type VibeTurn } from './VibeChatScreen';
import { resolveDogfoodClientSessionSettings } from './P2PClient';
import { DogfoodQuickControls } from './DogfoodQuickControls';
import { listReachableDevices, RemoteDevice } from './auth';
import { reloadActions } from './reloadActions';
import type { DevServerSnapshot, ReloadAction } from './reloadActions';
import {
  QUICK_ICON_COLOR_PRESETS,
  QuickIconColorPreset,
  getPreferredModel,
  getPreferredRunner,
  getPreferredDogfoodLane,
  setPreferredModel,
  setPreferredRunner,
  setPreferredDogfoodLane,
} from './preferences';
import {
  DogfoodController,
  defaultDogfoodLane,
  dogfoodLanePlan,
  dogfoodLaneOptions,
  type DogfoodLane,
  type DogfoodSnapshot,
} from './DogfoodRuntime';
import { createP2PDogfoodDriver } from './P2PDogfoodDriver';
import { DogfoodLanePicker, DogfoodLaunchingWidget, DogfoodLiveConsole, DogfoodStatusRail } from './DogfoodSessionUi';
import type { DogfoodRemoteRuntimeTarget } from './P2PClient';
import type { DogfoodRenderBehavior, DogfoodUsageMode } from './dogfoodPolicy';
import {
  FEEDBACK_DOGFOOD_CONSOLE_COLORS,
  FEEDBACK_DOGFOOD_LIGHT_COLORS,
} from './FeedbackModalTheme';
import { firstClassTaskConversationTurns } from './_core/taskConversation';

/**
 * Feedback modal with one conversational control surface. Authenticated users
 * land directly in Chat; screenshots, fixes, and deploy intent are expressed
 * in that conversation and handled by the connected agent/MCP toolchain.
 */

type ActionState =
  | 'idle'
  | 'hot-reloading'
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
  models?: Array<{ id: string; name?: string; provider?: string; isDefault?: boolean }>;
};

type ProviderEditorState = {
  mode: 'add' | 'edit';
  id: string;
  name: string;
  baseUrl: string;
  apiKey: string;
};

type DogfoodProjectChoice = {
  name: string;
  path: string;
  framework?: string;
  frameworks?: string[];
  surfaces?: string[];
  branch?: string;
  gitRemote?: string;
};

type DogfoodSetupStage = 'setup' | 'runtime';
type DogfoodExpandedStep = 'runner' | 'checkout' | null;

function dogfoodCheckoutDetail(project: DogfoodProjectChoice | null): string {
  if (!project) return 'Choose a Git checkout on the selected machine';
  return [project.path, project.branch ? `branch ${project.branch}` : project.gitRemote ? 'Git checkout' : 'checkout']
    .filter(Boolean)
    .join(' · ');
}

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
        models: row.models,
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
        models: row.models,
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
      models: row.models,
      detail,
      actionLabel: authed ? 'Re-auth' : 'Sign in',
      actionRunner: id,
    };
  });
}


/**
 * Renders the "inside Yaver" mark unless the app opted out.
 *
 * Lives here rather than in YaverModeBadge so the component stays a dumb view
 * and the DEFAULT (`modeBadge !== false` → on) is expressed in exactly one
 * place. Mounted from FeedbackModal because that is the component integrating
 * apps already render — a default that required a new mount point would not be
 * a default, it would be a feature request.
 */
const YaverModeBadgeGate: React.FC = () => {
  const [, refresh] = useState(0);
  useEffect(() => {
    const sub = DeviceEventEmitter.addListener('yaverFeedback:dogfoodChanged', () => refresh((n) => n + 1));
    return () => sub.remove();
  }, []);
  const cfg = YaverFeedback.getConfig();
  if (cfg?.modeBadge === false) return null;
  const dogfood = YaverFeedback.getDogfoodStatus();
  // DogfoodQuickControls is the single over-app affordance in standalone
  // Dogfood, while the Yaver container owns its guest Y. Rendering this badge
  // too creates two competing Y buttons with different menus.
  if (dogfood.active) return null;
  return (
    <YaverModeBadge
      position={cfg?.modeBadgePosition ?? 'bottom-left'}
    />
  );
};

export const FeedbackModal: React.FC = () => {
  const { width: winW, height: winH } = useWindowDimensions();
  const isTablet = Math.min(winW, winH) >= 600;
  // Tablet color/icon picker fans out to 5/6 cols — 31% (3-col)
  // looks empty on a 1024pt iPad. Mobile keeps 3-col.
  const iconOptionWidthOverride = isTablet ? '18%' : undefined;
  const [visible, setVisible] = useState(false);
  const [dogfoodActive, setDogfoodActive] = useState(() => YaverFeedback.getDogfoodStatus().active);
  const [dogfoodUsageMode, setDogfoodUsageMode] = useState<DogfoodUsageMode>('reload-and-chat');
  const [action, setAction] = useState<ActionState>('idle');
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [progress, setProgress] = useState<number | null>(null);
  // Tracks whether the user has hidden the QuickActionIcon via its
  // long-press menu. Shake is always available, so the feedback modal
  // is our guaranteed UI for bringing the icon back — we surface a
  // small "Show quick icon" row when this is true.
  const [quickIconHidden, setQuickIconHidden] = useState(false);
  // Dev-server snapshot behind the reload buttons. `null` means "we have not
  // been able to ask" — rendered as "not connected", never as "no dev server".
  const [devSnapshot, setDevSnapshot] = useState<DevServerSnapshot | null>(null);
  // Which reload action is in flight, so only that button spins.
  const [reloadingId, setReloadingId] = useState<string | null>(null);
  const [runnerAuthModal, setRunnerAuthModal] = useState<string | null>(null);
  // Signed-in users get the composer immediately. Signed-out users still get
  // an explicit entry action so auth/setup failures remain named and visible.
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
  const [runnerCards, setRunnerCards] = useState<RunnerCardState[]>(() =>
    normalizeRunnerStatusRows([]),
  );
  const [runnerStatusLoading, setRunnerStatusLoading] = useState(false);
  const [runnerStatusError, setRunnerStatusError] = useState<string | null>(null);
  const [preferredRunner, setPreferredRunnerState] = useState<string | null>(null);
  const [preferredModel, setPreferredModelState] = useState('');
  const [activeTab, setActiveTab] = useState<'chat' | 'settings'>('chat');
  const [showOpenCodeConfig, setShowOpenCodeConfig] = useState(false);
  const scrollRef = useRef<ScrollView>(null);
  const [dogfoodEnrollment, setDogfoodEnrollment] = useState<{
    status: string;
    installationId?: string;
    error?: string;
  } | null>(null);
  const [dogfoodProjects, setDogfoodProjects] = useState<DogfoodProjectChoice[]>([]);
  const [dogfoodProject, setDogfoodProject] = useState<DogfoodProjectChoice | null>(null);
  const [dogfoodLane, setDogfoodLane] = useState<DogfoodLane>('browser');
  const [dogfoodNativeAvailable, setDogfoodNativeAvailable] = useState(false);
  const [dogfoodBrowserAvailable, setDogfoodBrowserAvailable] = useState(false);
  const [dogfoodNativeTargets, setDogfoodNativeTargets] = useState<DogfoodRemoteRuntimeTarget[]>([]);
  const [dogfoodNativeTargetId, setDogfoodNativeTargetId] = useState('');
  const [dogfoodSetupStage, setDogfoodSetupStage] = useState<DogfoodSetupStage>('setup');
  const [dogfoodExpandedStep, setDogfoodExpandedStep] = useState<DogfoodExpandedStep>(null);
  const [dogfoodRuntime, setDogfoodRuntime] = useState<DogfoodSnapshot | null>(null);
  const [dogfoodSetupLoading, setDogfoodSetupLoading] = useState(false);
  const dogfoodControllerRef = useRef<DogfoodController | null>(null);
  const dogfoodAutoStartRef = useRef(false);
  const dogfoodAutoCompleteRef = useRef(false);
  const mountedRef = useRef(true);

  const loadDogfoodOnboarding = useCallback(async (silent = false) => {
    const onboarding = YaverFeedback.getDogfoodOnboarding();
    if (!onboarding) return;
    if (!silent) {
      setDogfoodSetupLoading(true);
      setDogfoodEnrollment({ status: 'checking' });
    }
    try {
      const enrolled = await YaverFeedback.enableDeviceDogfood(onboarding);
      if (!mountedRef.current) return;
      setDogfoodEnrollment({ status: enrolled.status, installationId: enrolled.installationId });
      if (enrolled.status !== 'active') return;
      let client = YaverFeedback.getP2PClient();
      if (!client && await YaverFeedback.reconnect()) client = YaverFeedback.getP2PClient();
      if (!client) throw new Error('The selected Yaver machine is not reachable yet.');
      const projects = await client.listDogfoodProjects();
      if (!mountedRef.current) return;
      setDogfoodProjects(projects);
      const hint = String(onboarding.projectName || '').trim().toLowerCase();
      const preferred = projects.find((item) => item.name.toLowerCase() === hint)
        || projects.find((item) => hint && item.name.toLowerCase().includes(hint))
        || projects[0]
        || null;
      setDogfoodProject((current) => current && projects.some((item) => item.path === current.path) ? current : preferred);
      if (preferred) {
        const framework = preferred.framework || onboarding.framework || 'expo';
        const capabilities = await client.getDogfoodRemoteRuntimeCapabilities(preferred.path, framework).catch(() => null);
        const targets = capabilities?.targets || [];
        const nativeTargets = targets.filter((target) => target.id !== 'browser-window');
        const enabledNativeTargets = nativeTargets.filter((target) => target.enabled);
        const nativeRuntimeAvailable = enabledNativeTargets.length > 0;
        const browserRuntimeAvailable = targets.some((target) => target.enabled && target.id === 'browser-window');
        if (mountedRef.current) {
          setDogfoodNativeAvailable(nativeRuntimeAvailable);
          setDogfoodBrowserAvailable(browserRuntimeAvailable);
          setDogfoodNativeTargets(nativeTargets);
          setDogfoodNativeTargetId((current) => enabledNativeTargets.some((target) => target.id === current)
            ? current
            : enabledNativeTargets[0]?.id || '');
        }
        const savedLane = await getPreferredDogfoodLane(onboarding.appId);
        const savedSupported = dogfoodLaneOptions(framework, { nativeRuntimeAvailable, browserRuntimeAvailable })
          .some((option) => option.lane === savedLane && option.supported);
        setDogfoodLane(savedLane && savedSupported
          ? savedLane
          : defaultDogfoodLane(framework, { nativeRuntimeAvailable, browserRuntimeAvailable }));
      }
    } catch (cause) {
      if (mountedRef.current) setDogfoodEnrollment({ status: 'failed', error: cause instanceof Error ? cause.message : String(cause) });
    } finally {
      if (mountedRef.current && !silent) setDogfoodSetupLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!visible || dogfoodEnrollment?.status !== 'pending') return;
    const timer = setInterval(() => { void loadDogfoodOnboarding(true); }, 2500);
    return () => clearInterval(timer);
  }, [dogfoodEnrollment?.status, loadDogfoodOnboarding, visible]);

  useEffect(() => {
    let cancelled = false;
    setDogfoodNativeAvailable(false);
    setDogfoodBrowserAvailable(false);
    setDogfoodNativeTargets([]);
    setDogfoodNativeTargetId('');
    const client = YaverFeedback.getP2PClient();
    if (!client || !dogfoodProject) return () => { cancelled = true; };
    const framework = dogfoodProject.framework || YaverFeedback.getDogfoodOnboarding()?.framework || 'expo';
    void client.getDogfoodRemoteRuntimeCapabilities(dogfoodProject.path, framework)
      .then((value) => {
        if (cancelled) return;
        const nativeTargets = value.targets.filter((target) => target.id !== 'browser-window');
        const enabledNativeTargets = nativeTargets.filter((target) => target.enabled);
        setDogfoodNativeAvailable(enabledNativeTargets.length > 0);
        setDogfoodBrowserAvailable(value.targets.some((target) => target.enabled && target.id === 'browser-window'));
        setDogfoodNativeTargets(nativeTargets);
        setDogfoodNativeTargetId((current) => enabledNativeTargets.some((target) => target.id === current)
          ? current
          : enabledNativeTargets[0]?.id || '');
        const laneCapabilities = {
          nativeRuntimeAvailable: enabledNativeTargets.length > 0,
          browserRuntimeAvailable: value.targets.some((target) => target.enabled && target.id === 'browser-window'),
        };
        setDogfoodLane((current) => dogfoodLaneOptions(framework, laneCapabilities)
          .some((option) => option.lane === current && option.supported)
          ? current
          : defaultDogfoodLane(framework, laneCapabilities));
      })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [dogfoodProject]);

  const startDogfoodRuntime = useCallback(async () => {
    const client = YaverFeedback.getP2PClient();
    const onboarding = YaverFeedback.getDogfoodOnboarding();
    if (!client || !dogfoodProject || !onboarding) return;
    await dogfoodControllerRef.current?.stop().catch(() => {});
    const framework = dogfoodProject.framework || onboarding.framework || 'expo';
    const lanePlan = dogfoodLanePlan(framework, {
      nativeRuntimeAvailable: dogfoodNativeAvailable,
      browserRuntimeAvailable: dogfoodBrowserAvailable,
    }, dogfoodLane);
    const controller = new DogfoodController({
      name: dogfoodProject.name,
      workDir: dogfoodProject.path,
      framework,
      lane: lanePlan.preferred,
      fallbackLane: lanePlan.fallback,
      nativeTargetId: lanePlan.preferred === 'webrtc' ? dogfoodNativeTargetId : undefined,
    }, createP2PDogfoodDriver(client), {
      onChange: (snapshot) => { if (mountedRef.current) setDogfoodRuntime(snapshot); },
    });
    dogfoodControllerRef.current = controller;
    setDogfoodRuntime(controller.snapshot());
    setDogfoodSetupStage('runtime');
    // Commit the exact reload target before starting. Hermes delivery may
    // recreate the React bridge before this promise resolves; the next bundle
    // must still know which checkout and lane it is running.
    await YaverFeedback.setDogfoodRuntimeSelection({
      projectName: dogfoodProject.name,
      projectPath: dogfoodProject.path,
      lane: lanePlan.preferred,
      targetDeviceId: lanePlan.preferred === 'webrtc' ? dogfoodNativeTargetId || undefined : undefined,
    }).catch(() => {});
    await YaverFeedback.setDogfoodControlPresentation('minimized-y').catch(() => {});
    const result = await controller.trigger().catch(() => null);
    if (result) {
      await YaverFeedback.setDogfoodRuntimeSelection({
        projectName: dogfoodProject.name,
        projectPath: dogfoodProject.path,
        lane: result.lane,
        runtimeSessionId: result.sessionId,
      }).catch(() => {});
    }
  }, [dogfoodBrowserAvailable, dogfoodLane, dogfoodNativeAvailable, dogfoodNativeTargetId, dogfoodProject]);

  /**
   * Ask the machine what its dev server is doing, so the reload actions can
   * be enabled/disabled against reality rather than against a guess.
   *
   * Best-effort and deliberately null-on-failure: null means "we could not
   * ask", which the seam renders as "not connected to a machine yet" — a
   * different sentence from "no dev server is running", because they have
   * different fixes.
   */
  const refreshDevSnapshot = useCallback(async () => {
    try {
      const client = YaverFeedback.getP2PClient();
      if (!client) {
        setDevSnapshot(null);
        return;
      }
      setDevSnapshot(await client.getDevServerStatus());
    } catch {
      setDevSnapshot(null);
    }
  }, []);

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
      const device =
        devices.owned.find((candidate) => candidate.deviceId === cfg.preferredDeviceId) ?? null;

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

      let status: MachineCardState['status'] = 'live';
      let detail = device.platform;

      if (!device.isOnline) {
        status = 'offline';
        detail = 'Machine offline. Start `yaver serve` on the selected machine.';
      } else if (device.needsAuth) {
        status = 'attention';
        detail = 'Machine needs pairing again before feedback actions can run.';
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
      const rows = await client.getAvailableRunners();
      if (mountedRef.current) {
        const normalized = normalizeRunnerStatusRows(rows);
        setRunnerCards(normalized);
        const savedRunner = await getPreferredRunner();
        if (!savedRunner) {
          const ready = normalized.filter((row) => row.ready || row.authConfigured);
          const preferred = rows.find((row) => row.isDefault && (row.ready || row.authConfigured));
          const automatic = preferred
            ? normalized.find((row) => row.id === (preferred.id === 'claude-code' ? 'claude' : preferred.id))
            : ready.length === 1 ? ready[0] : null;
          if (automatic) {
            const automaticModel = automatic.models?.find((model) => model.isDefault)?.id || automatic.models?.[0]?.id || '';
            setPreferredRunnerState(automatic.id);
            setPreferredModelState(automaticModel);
            await setPreferredRunner(automatic.id);
            await setPreferredModel(automaticModel || null);
          }
        }
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
      const onboarding = YaverFeedback.getDogfoodOnboarding();
      // Explicit Dogfood remains usable when passive capture/shake is off.
      // The host still owns visibility; server OAuth/device signatures own
      // authority. Keeping this event path independent avoids toggling the
      // user's feedback preference merely to open Developer Mode.
      if (YaverFeedback.isEnabled() || onboarding) {
        const directDogfood = YaverFeedback.getDogfoodStatus().active;
        const authenticated = YaverFeedback.isAuthed();
        setDogfoodActive(directDogfood);
        setVisible(true);
        setError(null);
        setToast(null);
        setProgress(null);
        setAction('idle');
        setShowVibeInput(authenticated || directDogfood);
        setVibePrompt('');
        if (onboarding) {
          // A Dogfood shortcut is an explicit setup/runtime intent. Opening on
          // Chat hid the machine/runner/checkout gate for signed-in SFMG users;
          // keep the SDK-owned Dogfood surface visible, then show its live logs
          // immediately when the saved setup becomes ready.
          setActiveTab('settings');
          dogfoodAutoStartRef.current = false;
          dogfoodAutoCompleteRef.current = false;
          setDogfoodSetupStage('setup');
          setDogfoodExpandedStep(null);
          setDogfoodRuntime(null);
          void loadDogfoodOnboarding();
        }
        if (directDogfood || onboarding) {
          void YaverFeedback.getDogfoodUsageMode().then((mode) => {
            if (!mountedRef.current) return;
            setDogfoodUsageMode(mode);
            if (mode === 'reload-only') {
              setActiveTab('settings');
              setShowVibeInput(false);
            }
          }).catch(() => {});
        }
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
        void loadRoutingPrefs();
      }
    });
    const dogfoodSub = DeviceEventEmitter.addListener(
      'yaverFeedback:dogfoodChanged',
      (payload: { active?: boolean; exited?: boolean }) => {
        if (!mountedRef.current) return;
        setDogfoodActive(payload?.active === true);
        if (payload?.exited === true) setVisible(false);
      },
    );
    const dogfoodUsageSub = DeviceEventEmitter.addListener(
      'yaverFeedback:dogfoodUsageModeChanged',
      (payload: { mode?: DogfoodUsageMode }) => {
        if (!mountedRef.current || !payload?.mode) return;
        setDogfoodUsageMode(payload.mode);
        if (payload.mode === 'reload-only') {
          setActiveTab('settings');
          setShowVibeInput(false);
        }
      },
    );
    const dogfoodSessionSub = DeviceEventEmitter.addListener(
      'yaverFeedback:dogfoodSessionRequested',
      (payload: { taskId?: string }) => {
        const taskId = String(payload?.taskId || '').trim();
        if (!mountedRef.current || !taskId) return;
        void (async () => {
          const [task, renderBehavior] = await Promise.all([
            YaverFeedback.getDogfoodSession(taskId),
            YaverFeedback.getDogfoodRenderBehavior(),
          ]);
          const taskTurns: VibeTurn[] = firstClassTaskConversationTurns(task.turns, task.presentation).map((turn, index) => ({
            id: `${task.id}-${index}`,
            role: turn.role as VibeTurn['role'],
            text: turn.content,
            timestamp: Date.now() + index,
          }));
          setActiveVibe({
            taskId: task.id,
            initialPrompt: taskTurns.find((turn) => turn.role === 'user')?.text || task.title || 'Dogfood session',
            project: task.projectName,
            runner: task.runnerId || task.executionSession?.runnerId,
            model: task.model,
            initialStatus: task.status,
            initialTurns: taskTurns,
            renderBehavior,
          });
          setVisible(true);
        })().catch((cause) => {
          setError(cause instanceof Error ? cause.message : String(cause));
          setVisible(true);
        });
      },
    );
    const dogfoodNewChatSub = DeviceEventEmitter.addListener('yaverFeedback:dogfoodNewChatRequested', () => {
      if (!mountedRef.current) return;
      void (async () => {
        const [selection, runner, model, renderBehavior] = await Promise.all([
          YaverFeedback.getDogfoodRuntimeSelection(),
          getPreferredRunner(),
          getPreferredModel(),
          YaverFeedback.getDogfoodRenderBehavior(),
        ]);
        setActiveVibe({
          initialPrompt: '',
          project: selection?.projectName,
          projectPath: selection?.projectPath,
          runner: runner || undefined,
          model: model || undefined,
          renderBehavior,
        });
        setVisible(true);
      })().catch((cause) => {
        setError(cause instanceof Error ? cause.message : String(cause));
        setVisible(true);
      });
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
      dogfoodSub.remove();
      dogfoodUsageSub.remove();
      dogfoodSessionSub.remove();
      dogfoodNewChatSub.remove();
      statusSub.remove();
    };
  }, [loadDogfoodOnboarding, loadRoutingPrefs, loadRunnerStatuses, loadSelectedMachine]);

  useEffect(() => {
    if (!visible) return;
    // Poll the dev server alongside the machine + runners. A reload button
    // whose enabled state was decided once, when the sheet opened, goes
    // stale the moment the user starts Metro from another surface — and a
    // stale "no dev server is running" reads as the product being broken.
    void refreshDevSnapshot();
    const interval = setInterval(() => {
      void loadSelectedMachine();
      void loadRunnerStatuses();
      void refreshDevSnapshot();
    }, 5000);
    return () => clearInterval(interval);
  }, [loadRunnerStatuses, loadSelectedMachine, refreshDevSnapshot, visible]);

  useEffect(() => {
    if (!visible || !showVibeInput) return;
    // UIKit owns keyboard insets on the ScrollView. This one bounded scroll only
    // reveals the newly mounted composer; it does not add a second inset.
    const timer = setTimeout(() => scrollRef.current?.scrollToEnd({ animated: true }), 80);
    return () => clearTimeout(timer);
  }, [showVibeInput, visible]);

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
    setDogfoodSetupStage('setup');
    setDogfoodExpandedStep(null);
    setDogfoodRuntime(null);
    dogfoodAutoStartRef.current = false;
    dogfoodAutoCompleteRef.current = false;
    void dogfoodControllerRef.current?.stop().catch(() => {});
    dogfoodControllerRef.current = null;
    YaverFeedback.clearDogfoodOnboarding();
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

  // ─── 1. Reload ─────────────────────────────────────────────────────
  //
  // Three actions now, not one: Hot Reload (mode=fast), Full Reload
  // (mode=full — Flutter's hot RESTART), and Rebuild Bundle
  // (/dev/reload-app, the only one that works with Metro down).
  //
  // WHICH of them render, and which are disabled with what reason, is
  // decided by the pure `reloadActions()` seam — never inline here, so the
  // same policy holds on web, Flutter, Unity, Swift and Kotlin. In
  // particular: a production build (`__DEV__ === false`) gets NONE of them.

  const availableReloadActions = reloadActions(devSnapshot, {
    // __DEV__ is React Native's own build flag. A release bundle sets it
    // false, so a shipped app renders no reload UI at all — which is the
    // point, and is what reloadActions.test.ts pins.
    isDevBuild: typeof __DEV__ !== 'undefined' && __DEV__ === true,
    connected: devSnapshot !== null,
    machineLabel: machineCard.device?.name || undefined,
    includeRebuild: true,
  });

  const handleReloadAction = useCallback(
    async (reloadAction: ReloadAction) => {
      if (!reloadAction.enabled) {
        // Pressing a disabled action must SAY why. A row that does nothing
        // is the same defect as a spinner that never resolves.
        setToast(reloadAction.disabledReason || 'Reload is unavailable right now.');
        setError(reloadAction.disabledReason || null);
        return;
      }
      setReloadingId(reloadAction.id);
      setAction('hot-reloading');
      setError(null);
      setProgress(0);
      setToast(`${reloadAction.label}…`);
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

        let ackMessage = `${reloadAction.label} requested.`;
        await runWithReconnect(async () => {
          const renderClient = YaverFeedback.getRenderP2PClient();
          if (!renderClient) throw new Error('Render machine is not connected yet.');
          const ack = await renderClient.reloadWithMode(reloadAction.mode, devSnapshot);
          ackMessage = ack.message;
          setToast(ack.message);
          setProgress(0.2);
        });
        setToast(ackMessage);
        if (reloadAction.mode === 'bundle') closeSoon(2500);
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : String(err);
        setError(message);
        setToast(
          message.toLowerCase().indexOf('session expired') >= 0
            ? 'Session expired. Sign in again.'
            : message,
        );
        await loadSelectedMachine();
        setProgress(null);
      } finally {
        if (mountedRef.current) {
          setAction('idle');
          setReloadingId(null);
          void refreshDevSnapshot();
        }
      }
    },
    [closeSoon, devSnapshot, loadSelectedMachine, refreshDevSnapshot, runWithReconnect],
  );

  /** Kept for the legacy one-tap path (BlackBox command, chat Reload button). */
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
      await runWithReconnect(async () => {
        const renderClient = YaverFeedback.getRenderP2PClient();
        if (!renderClient) throw new Error('Render machine is not connected yet.');
        const ack = await renderClient.reloadApp('bundle');
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

  // ─── Chat ──────────────────────────────────────────────────────────
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
    taskId?: string;
    initialPrompt?: string;
    project?: string;
    projectPath?: string;
    runner?: string;
    model?: string;
    initialStatus?: string;
    initialTurns?: VibeTurn[];
    renderBehavior?: DogfoodRenderBehavior;
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

      const renderBehavior = await YaverFeedback.getDogfoodRenderBehavior().catch(() => 'manual' as const);
      const config = YaverFeedback.getConfig();
      const sessionSettings = resolveDogfoodClientSessionSettings({
        lane: dogfoodLane,
        usageMode: dogfoodUsageMode,
        projectName: identity.projectName,
        appVersion: config?.appVersion,
        buildNumber: config?.buildNumber,
      });
      const result = await client.createFeedbackTask({
        userPrompt: promptText,
        projectName: identity.projectName,
        projectPath: identity.projectPath,
        runner: preferredRunner ?? undefined,
        model: preferredModel ?? undefined,
        screenshotBase64,
        sessionSettings,
      });
      setLastVibeTaskId(result.taskId);
      // Hand off to VibeChatScreen — it streams the SSE transcript,
      // accepts follow-ups, and surfaces a Reload button.
      setActiveVibe({
        taskId: result.taskId,
        initialPrompt: promptText,
        project: identity.projectName,
        projectPath: identity.projectPath,
        runner: preferredRunner ?? undefined,
        model: preferredModel ?? undefined,
        renderBehavior,
      });
      setVibePrompt('');
      setShowVibeInput(false);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (mountedRef.current) setAction('idle');
    }
  }, [dogfoodLane, dogfoodUsageMode, vibePrompt, includeScreenshot]);

  /*
  const handleScreenRecording = useCallback(async () => {
    ...
  }, [closeSoon, isRecordingVideo, lastVideo]);
  */

  const busy = action !== 'idle';
  const machineRouting = YaverFeedback.getMachineRouting();
  const readyRunnerCount = runnerCards.filter((row) => row.ready || row.authConfigured).length;
  const missingRunnerCount = runnerCards.filter((row) => !row.installed).length;
  const needsAuthRunnerCount = runnerCards.filter(
    (row) => row.installed && !row.authConfigured && !row.ready,
  ).length;
  const selectedDogfoodRunner = preferredRunner
    ? runnerCards.find((row) => row.id === preferredRunner) ?? null
    : null;
  const dogfoodRunnerReady = !!selectedDogfoodRunner
    && (selectedDogfoodRunner.ready || selectedDogfoodRunner.authConfigured);
  const dogfoodModelReady = !selectedDogfoodRunner?.models?.length
    || !!preferredModel && selectedDogfoodRunner.models.some((model) => model.id === preferredModel);
  const dogfoodFramework = dogfoodProject?.framework || YaverFeedback.getDogfoodOnboarding()?.framework || 'expo';
  const dogfoodOnboarding = YaverFeedback.getDogfoodOnboarding();
  const dogfoodLaneChoices = dogfoodLaneOptions(dogfoodFramework, {
    nativeRuntimeAvailable: dogfoodNativeAvailable,
    browserRuntimeAvailable: dogfoodBrowserAvailable,
  });
  const dogfoodLanePolicy = dogfoodLanePlan(dogfoodFramework, {
    nativeRuntimeAvailable: dogfoodNativeAvailable,
    browserRuntimeAvailable: dogfoodBrowserAvailable,
  }, dogfoodLane);
  const selectedDogfoodNativeTarget = dogfoodNativeTargets.find((target) => target.id === dogfoodNativeTargetId) || null;
  const dogfoodLaneReady = dogfoodLaneChoices.some((option) => option.lane === dogfoodLane && option.supported)
    && (dogfoodLane !== 'webrtc' || !!selectedDogfoodNativeTarget?.enabled);
  const dogfoodMachineReady = !!machineCard.device && machineCard.status === 'live';
  const dogfoodSetupReady = dogfoodMachineReady && !!dogfoodProject && dogfoodRunnerReady && dogfoodModelReady;
  const dogfoodSetupSteps = [
    {
      key: 'box',
      label: 'Remote box',
      detail: dogfoodMachineReady ? machineCard.title : 'Choose a reachable development machine',
      tone: dogfoodMachineReady ? 'ready' as const : 'attention' as const,
      actionLabel: machineCard.device ? 'Change' : 'Pick',
      onAction: () => YaverFeedback.showMachinePicker(),
    },
    {
      key: 'runner',
      label: 'Runner',
      detail: dogfoodRunnerReady
        ? [selectedDogfoodRunner?.name || preferredRunner || 'Ready', preferredModel || 'default model'].join(' · ')
        : 'Choose or configure a coding runner',
      tone: dogfoodRunnerReady && dogfoodModelReady ? 'ready' as const : 'attention' as const,
      actionLabel: dogfoodExpandedStep === 'runner' ? 'Done' : dogfoodRunnerReady ? 'Change' : 'Choose',
      expanded: dogfoodExpandedStep === 'runner',
      onAction: () => setDogfoodExpandedStep((current) => current === 'runner' ? null : 'runner'),
    },
    {
      key: 'checkout',
      label: 'Checkout',
      detail: dogfoodCheckoutDetail(dogfoodProject),
      tone: dogfoodProject ? 'ready' as const : 'attention' as const,
      actionLabel: dogfoodExpandedStep === 'checkout' ? 'Done' : dogfoodProject ? 'Change' : 'Choose',
      expanded: dogfoodExpandedStep === 'checkout',
      onAction: () => setDogfoodExpandedStep((current) => current === 'checkout' ? null : 'checkout'),
    },
  ];
  const dogfoodStartBlocked = !dogfoodSetupReady || !dogfoodLaneReady;
  const activeDogfoodLane = dogfoodRuntime?.project.lane || dogfoodLane;
  const dogfoodSourceLabel = activeDogfoodLane === 'webrtc'
    ? selectedDogfoodNativeTarget
      ? [selectedDogfoodNativeTarget.label, selectedDogfoodNativeTarget.platform].filter(Boolean).join(' · ')
      : 'Native simulator, emulator, or device'
    : activeDogfoodLane === 'hermes'
      ? `Hermes build · ${machineCard.title}`
      : `${dogfoodFramework === 'flutter' ? 'Flutter web compiler' : 'Metro / browser build'} · ${machineCard.title}`;
  const dogfoodLaunchHint = dogfoodLane === 'browser'
    ? 'Browser Logs open first, then the React Native web app opens automatically.'
    : dogfoodLane === 'hermes'
      ? 'Hermes Logs open first, then the installed app reloads with a validated bundle.'
      : 'WebRTC Logs open first, then the selected native runtime opens automatically.';
  const completeDogfoodRuntime = useCallback(async () => {
    if (dogfoodRuntime?.phase !== 'ready') return;
    try {
      // Browser Dogfood is an external surface for an SDK host. Opening the
      // proven URL is part of the handoff; merely closing this sheet would
      // report success while leaving the user in the unchanged host app.
      if (dogfoodRuntime.result?.url) await Linking.openURL(dogfoodRuntime.result.url);
      await YaverFeedback.setDogfoodControlPresentation('minimized-y').catch(() => {});
      YaverFeedback.clearDogfoodOnboarding();
      setVisible(false);
    } catch (cause) {
      dogfoodAutoCompleteRef.current = false;
      setError(`Dogfood did not open: ${cause instanceof Error ? cause.message : String(cause)}`);
    }
  }, [dogfoodRuntime?.phase, dogfoodRuntime?.result?.url]);

  useEffect(() => {
    if (!visible || !dogfoodOnboarding || dogfoodSetupStage !== 'setup' || dogfoodStartBlocked || dogfoodAutoStartRef.current) return;
    dogfoodAutoStartRef.current = true;
    void startDogfoodRuntime().finally(() => {
      if (mountedRef.current && !dogfoodControllerRef.current) dogfoodAutoStartRef.current = false;
    });
  }, [dogfoodOnboarding, dogfoodSetupStage, dogfoodStartBlocked, startDogfoodRuntime, visible]);

  useEffect(() => {
    if (!visible || dogfoodRuntime?.phase !== 'ready' || dogfoodAutoCompleteRef.current) return;
    dogfoodAutoCompleteRef.current = true;
    void completeDogfoodRuntime();
  }, [completeDogfoodRuntime, dogfoodRuntime?.phase, visible]);

  // Once the user fires off a vibe task, swap the entire modal body
  // for the live chat screen. The chat manages its own SSE
  // subscription, multi-turn follow-ups, and Reload button. Closing
  // the chat returns to idle and clears the active vibe.
  if (activeVibe) {
    const client = YaverFeedback.getP2PClient();
    const config = YaverFeedback.getConfig();
    const sessionSettings = resolveDogfoodClientSessionSettings({
      lane: activeDogfoodLane,
      usageMode: dogfoodUsageMode,
      projectName: activeVibe.project,
      appVersion: config?.appVersion,
      buildNumber: config?.buildNumber,
    });
    return (
      <>
        <AuthOverlay />
        <DogfoodQuickControls suppressed={visible} />
        <QuickActionIcon />
        <YaverModeBadgeGate />
        <Modal
          visible={visible}
          animationType="slide"
          transparent
          onRequestClose={handleClose}
        >
          <KeyboardAvoidingView
            style={styles.dogfoodChatOverlay}
            behavior={Platform.OS === 'ios' ? 'padding' : 'height'}
            keyboardVerticalOffset={0}
          >
            <Pressable style={styles.dogfoodChatBackdrop} onPress={handleClose} accessibilityLabel="Minimize Dogfood chat" />
            <View style={styles.dogfoodChatCard}>
            {client ? <VibeChatScreen
              key={activeVibe.taskId || 'new-chat'}
              client={client}
              initialTaskId={activeVibe.taskId}
              initialUserPrompt={activeVibe.initialPrompt}
              project={activeVibe.project}
              projectPath={activeVibe.projectPath}
              runner={activeVibe.runner}
              model={activeVibe.model}
              initialStatus={activeVibe.initialStatus}
              initialTurns={activeVibe.initialTurns}
              renderBehavior={activeVibe.renderBehavior || 'manual'}
              sessionSettings={sessionSettings}
              voiceInputEnabled={YaverFeedback.getConfig()?.voiceInputEnabled === true}
              // Closing this sheet must also clear the chat route.  Previously
              // `handleClose` only hid the native modal; reopening Feedback
              // therefore mounted the old chat again and made the normal
              // composer unreachable.
              onClose={() => {
                setActiveVibe(null);
                handleClose();
              }}
              onNewTopic={() => {
                setActiveVibe((current) => current ? {
                  ...current,
                  taskId: undefined,
                  initialPrompt: '',
                  initialStatus: undefined,
                  initialTurns: undefined,
                } : current);
              }}
              onMinimize={handleClose}
              onOpenSettings={() => {
                setActiveVibe(null);
                void YaverFeedback.openDogfood();
              }}
              onSignOut={async () => {
                await YaverFeedback.signOut();
                setActiveVibe(null);
                handleClose();
                YaverFeedback.showLogin();
              }}
              codingMachine={YaverFeedback.getMachineRouting().codingDeviceId}
              renderMachine={YaverFeedback.getMachineRouting().renderDeviceId}
              onReload={dogfoodUsageMode === 'chat-only' ? undefined : async () => {
                // The compact Reload Only control and this Vibing surface must
                // take the identical lane-aware path. `reloadApp()` has no
                // selected checkout/lane, so it cannot refresh browser
                // Dogfood reliably.
                await YaverFeedback.requestDogfoodFastReload();
              }}
            /> : null}
            </View>
          </KeyboardAvoidingView>
        </Modal>
      </>
    );
  }

  return (
    <>
      <AuthOverlay />
      <DogfoodQuickControls suppressed={visible} />
      <QuickActionIcon />
      <YaverModeBadgeGate />
      {visible && (
        <Modal
          visible={visible}
          animationType="slide"
          transparent
          onRequestClose={handleClose}
        >
          <KeyboardAvoidingView
            style={styles.overlay}
            behavior={Platform.OS === 'ios' ? 'padding' : 'height'}
          >
            <Pressable style={styles.backdrop} onPress={handleClose} accessibilityLabel="Close feedback" />
            <View
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
            >
              <ScrollView
                ref={scrollRef}
                style={styles.scroll}
                contentContainerStyle={styles.scrollContent}
                keyboardShouldPersistTaps="handled"
                keyboardDismissMode={Platform.OS === 'ios' ? 'interactive' : 'on-drag'}
                contentInsetAdjustmentBehavior="always"
                // KeyboardAvoidingView is the single inset owner. Letting the
                // ScrollView add a second UIKit inset lifts the composer twice
                // on short phones and still leaves its action row clipped.
                automaticallyAdjustKeyboardInsets={false}
              >
              <View style={styles.header}>
                <Text style={styles.title}>
                  {dogfoodOnboarding
                    ? `Set up ${dogfoodOnboarding.projectName || dogfoodOnboarding.label || 'this app'} Dogfood`
                    : dogfoodActive
                      ? `${YaverFeedback.getDogfoodStatus().label || 'App'} Developer Mode`
                      : 'Send Feedback'}
                </Text>
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

              {(!dogfoodOnboarding || dogfoodSetupStage === 'runtime') ? (
              <View style={styles.tabs} accessibilityRole="tablist">
                {(dogfoodActive && dogfoodUsageMode === 'reload-only'
                  ? (['settings'] as const)
                  : (['chat', 'settings'] as const)).map((tab) => (
                  <Pressable
                    key={tab}
                    onPress={() => setActiveTab(tab)}
                    style={[styles.tab, activeTab === tab && styles.tabSelected]}
                    accessibilityRole="tab"
                    accessibilityState={{ selected: activeTab === tab }}
                  >
                    <Text style={[styles.tabText, activeTab === tab && styles.tabTextSelected]}>{tab === 'chat' ? 'Chat' : 'Settings'}</Text>
                  </Pressable>
                ))}
              </View>
              ) : null}

              <View style={[styles.tabContent, activeTab !== 'settings' && styles.hidden]}>
              <>
              {dogfoodOnboarding ? (
                <View style={styles.dogfoodWizard}>
                  <Text style={styles.dogfoodWizardTitle}>Connect the app to its checkout</Text>
                  <Text style={styles.dogfoodWizardHint}>
                    {dogfoodEnrollment?.status === 'active'
                      ? 'Choose one development machine, coding runner, and checkout. Chat opens without starting a renderer.'
                      : `Signed in · installation ${dogfoodEnrollment?.status || 'checking'}`}
                  </Text>
                  {dogfoodEnrollment?.status === 'active' && dogfoodSetupStage === 'setup' ? (
                    <DogfoodStatusRail
                      steps={dogfoodSetupSteps}
                      colors={FEEDBACK_DOGFOOD_LIGHT_COLORS}
                    />
                  ) : null}
                  {dogfoodEnrollment?.status !== 'active' && dogfoodEnrollment?.installationId ? (
                    <Text selectable style={styles.dogfoodInstallationId}>
                      This device · {dogfoodEnrollment.installationId}
                    </Text>
                  ) : null}
                  {dogfoodEnrollment?.status !== 'active' ? (
                    <View style={styles.dogfoodPendingBox}>
                      <Text style={styles.dogfoodPendingText}>
                        {dogfoodEnrollment?.error
                          || 'Approve this installation from Yaver → Settings → Third-party app testing. A UUID alone never grants access.'}
                      </Text>
                      <Pressable
                        onPress={() => void loadDogfoodOnboarding()}
                        style={({ pressed }) => [styles.runnerRefreshBtn, pressed && styles.buttonPressed]}
                      >
                        <Text style={styles.runnerRefreshBtnText}>{dogfoodSetupLoading ? 'Checking…' : 'Check approval'}</Text>
                      </Pressable>
                    </View>
                  ) : (
                    <>
                      {dogfoodSetupStage === 'setup' ? (
                        <>
                          {dogfoodExpandedStep === 'runner' ? (
                            <View style={styles.dogfoodExpandedPanel}>
                              <Text style={styles.dogfoodStepLabel}>Coding runner</Text>
                              {runnerCards.map((row) => {
                                const selectable = row.ready || row.authConfigured;
                                const selected = preferredRunner === row.id;
                                return (
                                  <View key={row.id} style={[
                                    styles.runnerCard,
                                    row.tone === 'ok' && styles.runnerCardOk,
                                    row.tone === 'warning' && styles.runnerCardWarning,
                                    selected && styles.runnerCardSelected,
                                  ]}>
                                    <View style={styles.runnerCardTop}>
                                      <View style={{ flex: 1 }}>
                                        <Text style={styles.runnerCardTitle}>{row.name}{selected ? ' · selected' : ''}</Text>
                                        <Text style={[
                                          styles.runnerCardStatus,
                                          row.tone === 'ok' && styles.runnerCardStatusOk,
                                          row.tone === 'warning' && styles.runnerCardStatusWarning,
                                        ]}>{row.statusLine}</Text>
                                      </View>
                                      {selectable ? <Pressable
                                        onPress={() => {
                                          const nextModel = row.models?.find((model) => model.isDefault)?.id || row.models?.[0]?.id || '';
                                          setPreferredRunnerState(row.id);
                                          setPreferredModelState(nextModel);
                                          void setPreferredRunner(row.id);
                                          void setPreferredModel(nextModel || null);
                                        }}
                                        style={[styles.runnerActionBtn, selected && styles.runnerActionBtnSelected]}
                                        accessibilityRole="button"
                                        accessibilityLabel={`${selected ? 'Using' : 'Use'} ${row.name}`}
                                      ><Text style={styles.runnerActionBtnText}>{selected ? 'Using' : 'Use'}</Text></Pressable> : row.actionRunner ? <Pressable
                                        onPress={() => setRunnerAuthModal(row.actionRunner || null)}
                                        style={styles.runnerActionBtn}
                                        accessibilityRole="button"
                                        accessibilityLabel={`${row.actionLabel} ${row.name}`}
                                      ><Text style={styles.runnerActionBtnText}>{row.actionLabel}</Text></Pressable> : null}
                                    </View>
                                  </View>
                                );
                              })}
                              {readyRunnerCount === 0 ? (
                                <Text style={styles.dogfoodWizardHint}>No runner is ready on this box yet. Sign in above or choose another remote box.</Text>
                              ) : null}
                            </View>
                          ) : null}
                          {dogfoodExpandedStep === 'checkout' ? (
                            <View style={styles.dogfoodExpandedPanel}>
                              <Text style={styles.dogfoodStepLabel}>Git checkout on remote box</Text>
                              {dogfoodProjects.map((project) => (
                                <Pressable
                                  key={project.path}
                                  onPress={() => {
                                    setDogfoodProject(project);
                                    setDogfoodLane(defaultDogfoodLane(project.framework || YaverFeedback.getDogfoodOnboarding()?.framework || 'expo'));
                                    setDogfoodRuntime(null);
                                  }}
                                  style={[styles.dogfoodProjectChoice, dogfoodProject?.path === project.path && styles.dogfoodChoiceSelected]}
                                >
                                  <Text style={[styles.dogfoodChoiceText, dogfoodProject?.path === project.path && styles.dogfoodChoiceTextSelected]}>{project.name}</Text>
                                  <Text style={styles.dogfoodProjectDetail}>{dogfoodCheckoutDetail(project)}</Text>
                                </Pressable>
                              ))}
                            </View>
                          ) : null}
                          <View style={styles.dogfoodStageHeader}>
                            <View style={styles.dogfoodStageCopy}>
                              <Text style={styles.dogfoodStepLabel}>Reload target</Text>
                              <Text style={styles.dogfoodStageTitle}>{dogfoodProject?.name} · {dogfoodFramework}</Text>
                            </View>
                          </View>
                          <DogfoodLanePicker
                            options={dogfoodLaneChoices}
                            selected={dogfoodLane}
                            fallbackLane={dogfoodLanePolicy.fallback}
                            colors={FEEDBACK_DOGFOOD_LIGHT_COLORS}
                            onSelect={(lane) => {
                              setDogfoodLane(lane);
                              const appId = YaverFeedback.getDogfoodOnboarding()?.appId;
                              if (appId) void setPreferredDogfoodLane(appId, lane);
                            }}
                          />
                          {dogfoodLane === 'webrtc' ? (
                            <View style={styles.dogfoodExpandedPanel}>
                              <Text style={styles.dogfoodStepLabel}>Simulator, emulator, or device</Text>
                              {dogfoodNativeTargets.map((target) => (
                                <Pressable
                                  key={target.id}
                                  disabled={!target.enabled}
                                  onPress={() => setDogfoodNativeTargetId(target.id)}
                                  style={[
                                    styles.dogfoodProjectChoice,
                                    dogfoodNativeTargetId === target.id && styles.dogfoodChoiceSelected,
                                    !target.enabled && styles.dogfoodChoiceDisabled,
                                  ]}
                                >
                                  <Text style={[styles.dogfoodChoiceText, dogfoodNativeTargetId === target.id && styles.dogfoodChoiceTextSelected]}>{target.label}</Text>
                                  <Text style={styles.dogfoodProjectDetail}>{target.enabled
                                    ? [target.platform, target.displaySurface || target.surface, 'WebRTC'].filter(Boolean).join(' · ')
                                    : target.reason || 'Unavailable on this box'}</Text>
                                </Pressable>
                              ))}
                            </View>
                          ) : null}
                          <Text style={styles.dogfoodWizardHint}>{dogfoodLaunchHint}</Text>
                          {dogfoodSetupReady && dogfoodLaneReady ? (
                            <DogfoodLaunchingWidget
                              message="Launching Dogfood…"
                              detail="Your saved choices are ready. No second tap is needed."
                              colors={FEEDBACK_DOGFOOD_LIGHT_COLORS}
                            />
                          ) : null}
                        </>
                      ) : null}

                      {dogfoodSetupStage === 'runtime' && dogfoodRuntime ? (
                        <>
                          <View style={styles.dogfoodStageHeader}>
                            <View style={styles.dogfoodStageCopy}>
                              <Text style={styles.dogfoodStepLabel}>Dogfooding</Text>
                              <Text style={styles.dogfoodStageTitle}>{dogfoodSourceLabel}</Text>
                            </View>
                            <Pressable
                              onPress={() => {
                                void dogfoodControllerRef.current?.stop().catch(() => {});
                                dogfoodControllerRef.current = null;
                                dogfoodAutoStartRef.current = false;
                                dogfoodAutoCompleteRef.current = false;
                                setDogfoodRuntime(null);
                                setDogfoodSetupStage('setup');
                              }}
                              style={styles.dogfoodSmallAction}
                            >
                              <Text style={styles.dogfoodSmallActionText}>{['preparing', 'starting', 'compiling'].includes(dogfoodRuntime.phase) ? 'Stop' : 'Change'}</Text>
                            </Pressable>
                          </View>
                          <DogfoodLiveConsole
                            lane={dogfoodRuntime.project.lane}
                            sourceLabel={dogfoodSourceLabel}
                            phase={dogfoodRuntime.phase}
                            message={dogfoodRuntime.message}
                            logs={dogfoodRuntime.logs}
                            failure={dogfoodRuntime.failure}
                            colors={FEEDBACK_DOGFOOD_CONSOLE_COLORS}
                          />
                          {dogfoodRuntime.phase === 'ready' ? (
                            <DogfoodLaunchingWidget
                              message="Opening dogfooded app…"
                              detail="The verified runtime is returning to the app automatically."
                              colors={FEEDBACK_DOGFOOD_LIGHT_COLORS}
                            />
                          ) : null}
                        </>
                      ) : null}
                    </>
                  )}
                </View>
              ) : null}
              {!dogfoodOnboarding ? <>
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
                    <Text style={styles.machineLabel}>Coding Machine</Text>
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

              <View style={styles.machineRoutes}>
                <View style={styles.machineRouteCard}>
                  <Text style={styles.machineRouteLabel}>Coding machine</Text>
                  <Text style={styles.machineRouteValue} numberOfLines={1}>{machineRouting.codingDeviceId || machineCard.title}</Text>
                </View>
                <View style={styles.machineRouteCard}>
                  <Text style={styles.machineRouteLabel}>Render machine</Text>
                  <Text style={styles.machineRouteValue} numberOfLines={1}>{machineRouting.renderDeviceId || machineRouting.codingDeviceId || machineCard.title}</Text>
                </View>
              </View>

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

                <View style={styles.routingSummary}>
                  <Text style={styles.routingSummaryLabel}>Vibing uses</Text>
                  <Text style={styles.routingSummaryValue} numberOfLines={1}>
                    {[preferredRunner || 'automatic runner', preferredModel].filter(Boolean).join(' · ')}
                  </Text>
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
                      <View style={styles.runnerCardActions}>
                        {row.ready ? (
                          <Pressable
                            onPress={() => {
                              const nextModel = row.models?.find((model) => model.isDefault)?.id || row.models?.[0]?.id || '';
                              setPreferredRunnerState(row.id);
                              setPreferredModelState(nextModel);
                              void setPreferredRunner(row.id);
                              void setPreferredModel(nextModel || null);
                            }}
                            style={({ pressed }) => [styles.runnerActionBtn, preferredRunner === row.id && styles.runnerActionBtnSelected, pressed && styles.buttonPressed]}
                            accessibilityRole="button"
                            accessibilityLabel={`Use ${row.name} for Vibing`}
                          >
                            <Text style={styles.runnerActionBtnText}>{preferredRunner === row.id ? 'Using' : 'Use'}</Text>
                          </Pressable>
                        ) : null}
                        {row.actionRunner ? (
                          <Pressable
                            onPress={() => setRunnerAuthModal(row.actionRunner ?? null)}
                            style={({ pressed }) => [styles.runnerActionBtn, pressed && styles.buttonPressed]}
                            accessibilityRole="button"
                            accessibilityLabel={`${row.actionLabel} ${row.name}`}
                          >
                            <Text style={styles.runnerActionBtnText}>{row.actionLabel}</Text>
                          </Pressable>
                        ) : null}
                      </View>
                    </View>
                    {preferredRunner === row.id && (row.models?.length || 0) > 0 ? (
                      <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.modelChoiceRow}>
                        {row.models!.map((model) => (
                          <Pressable
                            key={model.id}
                            onPress={() => {
                              setPreferredModelState(model.id);
                              void setPreferredModel(model.id);
                            }}
                            style={[styles.modelChoice, preferredModel === model.id && styles.modelChoiceSelected]}
                            accessibilityRole="button"
                            accessibilityLabel={`Use ${model.name || model.id} model`}
                          >
                            <Text style={[styles.modelChoiceText, preferredModel === model.id && styles.modelChoiceTextSelected]}>{model.name || model.id}</Text>
                          </Pressable>
                        ))}
                      </ScrollView>
                    ) : null}
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

              {YaverFeedback.isAuthed() ? (
                <Pressable
                  onPress={() => {
                    void YaverFeedback.signOut().then(() => {
                      handleClose();
                      YaverFeedback.showLogin();
                    });
                  }}
                  style={({ pressed }) => [styles.yaverSignOutBtn, pressed && styles.buttonPressed]}
                  accessibilityRole="button"
                  accessibilityLabel="Sign out of Yaver"
                >
                  <Text style={styles.yaverSignOutText}>Sign out of Yaver</Text>
                </Pressable>
              ) : null}

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

              {/* 1. Reload — Hot / Full / Rebuild Bundle.
                   Rendered from the shared decision seam, so a production
                   build (__DEV__ false) renders nothing here at all, and a
                   blocked action shows greyed WITH its reason underneath
                   rather than vanishing. */}
              {availableReloadActions.map((reloadAction) => (
                <View key={reloadAction.id} style={styles.reloadRow}>
                  <ActionRow
                    label={
                      reloadingId === reloadAction.id
                        ? `${reloadAction.label}…`
                        : reloadAction.label
                    }
                    tint={reloadAction.id === 'rebuild' ? '#0369a1' : '#9a5700'}
                    onPress={() => {
                      void handleReloadAction(reloadAction);
                    }}
                    // Never `disabled` at the Pressable level for a blocked
                    // action: we WANT the tap so we can say why. Only a
                    // genuinely busy modal blocks the press.
                    disabled={busy && reloadingId !== reloadAction.id}
                    busy={reloadingId === reloadAction.id}
                  />
                  <Text style={styles.reloadHint}>
                    {reloadAction.enabled
                      ? reloadAction.hint
                      : reloadAction.disabledReason}
                  </Text>
                </View>
              ))}
              </> : null}
              </>
              </View>

              {!(dogfoodActive && dogfoodUsageMode === 'reload-only') ? (
              <View style={[styles.tabContent, activeTab !== 'chat' && styles.hidden]}>
              {/* Chat creates the first task, then VibeChatScreen owns the
                  transcript and every MCP-backed follow-up. */}
              {!showVibeInput ? (
                <ActionRow
                  label={action === 'vibing' ? 'Starting…' : 'Vibing'}
                  tint="#5645d8"
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

              </View>
              ) : null}

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
            </View>
          </KeyboardAvoidingView>
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
  dogfoodChatOverlay: {
    flex: 1,
    justifyContent: 'flex-end',
    alignItems: 'center',
  },
  dogfoodChatBackdrop: {
    ...StyleSheet.absoluteFillObject,
    backgroundColor: 'rgba(2,6,23,0.28)',
  },
  dogfoodChatCard: {
    width: '100%',
    maxWidth: 680,
    height: '78%',
    minHeight: 300,
    maxHeight: 720,
    overflow: 'hidden',
    borderTopLeftRadius: 22,
    borderTopRightRadius: 22,
    backgroundColor: '#f8f8fb',
  },
  dogfoodWizard: { gap: 10, borderRadius: 14, borderWidth: 1, borderColor: 'rgba(129,140,248,0.38)', backgroundColor: 'rgba(129,140,248,0.08)', padding: 13 },
  dogfoodWizardTitle: { color: '#222229', fontSize: 17, fontWeight: '800' },
  dogfoodWizardHint: { color: '#6f6f7b', fontSize: 12, lineHeight: 17 },
  dogfoodInstallationId: { color: '#6555df', fontSize: 11, fontFamily: Platform.select({ ios: 'Menlo', android: 'monospace', default: 'monospace' }) },
  dogfoodPendingBox: { gap: 9, borderRadius: 11, padding: 10, backgroundColor: 'rgba(245,158,11,0.10)' },
  dogfoodPendingText: { color: '#8a5a10', fontSize: 12, lineHeight: 17 },
  dogfoodStepLabel: { color: '#555561', fontSize: 11, fontWeight: '800', textTransform: 'uppercase', letterSpacing: 0.7 },
  dogfoodChoiceRow: { flexDirection: 'row', gap: 7 },
  dogfoodChoice: { borderRadius: 9, borderWidth: 1, borderColor: '#d8d8e3', backgroundColor: '#fff', paddingHorizontal: 10, paddingVertical: 8 },
  dogfoodChoiceSelected: { borderColor: '#818cf8', backgroundColor: 'rgba(129,140,248,0.18)' },
  dogfoodChoiceDisabled: { opacity: 0.48 },
  dogfoodChoiceText: { color: '#666671', fontSize: 12, fontWeight: '700' },
  dogfoodChoiceTextSelected: { color: '#5645d8' },
  dogfoodExpandedPanel: { gap: 8, borderRadius: 11, borderWidth: 1, borderColor: '#d8d8e3', backgroundColor: 'rgba(255,255,255,0.72)', padding: 10 },
  dogfoodProjectChoice: { gap: 3, borderRadius: 9, borderWidth: 1, borderColor: '#d8d8e3', backgroundColor: '#fff', paddingHorizontal: 10, paddingVertical: 9 },
  dogfoodProjectDetail: { color: '#777783', fontSize: 10, lineHeight: 14 },
  dogfoodStageHeader: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: 10 },
  dogfoodStageCopy: { flex: 1, gap: 2 },
  dogfoodStageTitle: { color: '#222229', fontSize: 13, fontWeight: '700' },
  dogfoodSmallAction: { borderRadius: 8, borderWidth: 1, borderColor: '#d8d8e3', backgroundColor: '#fff', paddingHorizontal: 10, paddingVertical: 7 },
  dogfoodSmallActionText: { color: '#5645d8', fontSize: 11, fontWeight: '800' },
  dogfoodConsole: { maxHeight: 260, gap: 4, borderRadius: 11, padding: 10, backgroundColor: '#15151b' },
  dogfoodConsoleStatus: { color: '#a5b4fc', fontSize: 12, fontWeight: '800' },
  dogfoodConsoleLine: { color: '#d1d5db', fontSize: 10, lineHeight: 14, fontFamily: Platform.select({ ios: 'Menlo', android: 'monospace', default: 'monospace' }) },
  dogfoodConsoleError: { color: '#fca5a5', fontSize: 11, lineHeight: 16, marginTop: 5 },
  dogfoodOpenPreview: { alignSelf: 'flex-start', borderRadius: 9, paddingHorizontal: 11, paddingVertical: 8, marginTop: 6, backgroundColor: '#6555df' },
  dogfoodOpenPreviewText: { color: '#fff', fontSize: 12, fontWeight: '800' },
  yaverSignOutBtn: { alignSelf: 'flex-start', paddingHorizontal: 4, paddingVertical: 8 },
  yaverSignOutText: { color: '#b42318', fontSize: 13, fontWeight: '700' },
  reloadRow: {
    gap: 4,
  },
  reloadHint: {
    color: '#666671',
    fontSize: 11,
    lineHeight: 15,
    paddingHorizontal: 4,
  },
  vibeInputRow: {
    backgroundColor: 'rgba(129,140,248,0.08)',
    borderColor: 'rgba(129,140,248,0.4)',
    borderWidth: 1,
    borderRadius: 12,
    padding: 12,
    gap: 10,
  },
  vibeInput: {
    color: '#24242b',
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
    color: '#5f5f69',
    fontSize: 14,
    fontWeight: '600',
  },
  vibeSendBtn: {
    paddingHorizontal: 16,
    paddingVertical: 8,
    borderRadius: 8,
    backgroundColor: '#5645d8',
    minWidth: 72,
    alignItems: 'center',
  },
  vibeSendBtnText: {
    color: '#fff',
    fontSize: 14,
    fontWeight: '700',
  },
  vibeTaskLine: {
    color: '#5645d8',
    fontSize: 12,
    marginTop: -4,
    fontFamily: Platform.select({ ios: 'Menlo', android: 'monospace', default: 'monospace' }),
  },
  overlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.55)',
    justifyContent: 'flex-end',
  },
  backdrop: StyleSheet.absoluteFillObject,
  modal: {
    backgroundColor: '#f8f8fb',
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
    color: '#17171d',
  },
  closeBtn: {
    width: 36,
    height: 36,
    borderRadius: 18,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: '#ededf3',
  },
  closeIcon: {
    color: '#656570',
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
  tabs: { flexDirection: 'row', gap: 6, padding: 3, borderRadius: 12, backgroundColor: '#ededf3' },
  tab: { flex: 1, minHeight: 36, borderRadius: 9, alignItems: 'center', justifyContent: 'center' },
  tabSelected: { backgroundColor: '#fff' },
  tabText: { color: '#666671', fontSize: 12, fontWeight: '700' },
  tabTextSelected: { color: '#6252e8' },
  tabContent: { gap: 12 },
  hidden: { display: 'none' },
  machineCard: {
    borderRadius: 14,
    borderWidth: 1,
    padding: 14,
    backgroundColor: '#fff',
    borderColor: '#e1e1e8',
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
  machineRoutes: { flexDirection: 'row', gap: 8 },
  machineRouteCard: { flex: 1, minWidth: 0, borderRadius: 11, padding: 10, gap: 3, backgroundColor: '#fff', borderWidth: 1, borderColor: '#e1e1e8' },
  machineRouteLabel: { color: '#666671', fontSize: 10, fontWeight: '700', textTransform: 'uppercase' },
  machineRouteValue: { color: '#222229', fontSize: 12, fontWeight: '700' },
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
    color: '#73737e',
    fontSize: 12,
    fontWeight: '700',
    textTransform: 'uppercase',
    letterSpacing: 0.8,
  },
  machineAction: {
    color: '#5645d8',
    fontSize: 12,
    fontWeight: '700',
  },
  machineName: {
    color: '#222229',
    fontSize: 16,
    fontWeight: '700',
  },
  machineMeta: {
    color: '#73737e',
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
    color: '#222229',
    fontSize: 16,
    fontWeight: '700',
  },
  runnerSectionSubtitle: {
    marginTop: 2,
    color: '#666671',
    fontSize: 12,
  },
  runnerRefreshBtn: {
    borderRadius: 10,
    borderWidth: 1,
    borderColor: '#dedee7',
    backgroundColor: '#fff',
    paddingHorizontal: 10,
    paddingVertical: 8,
  },
  runnerRefreshBtnText: {
    color: '#5f5f69',
    fontSize: 12,
    fontWeight: '600',
  },
  runnerCard: {
    borderRadius: 12,
    borderWidth: 1,
    borderColor: '#e2e2e9',
    backgroundColor: '#fff',
    paddingHorizontal: 12,
    paddingVertical: 11,
    gap: 6,
  },
  runnerCardOk: {
    borderColor: 'rgba(34,197,94,0.28)',
    backgroundColor: 'rgba(34,197,94,0.08)',
  },
  runnerCardSelected: {
    borderWidth: 2,
    borderColor: '#6555df',
  },
  runnerCardWarning: {
    borderColor: 'rgba(251,191,36,0.28)',
    backgroundColor: 'rgba(245,158,11,0.08)',
  },
  runnerCardError: {
    borderColor: 'rgba(248,113,113,0.28)',
    backgroundColor: 'rgba(239,68,68,0.07)',
  },
  runnerCardTop: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
  },
  runnerCardActions: { flexDirection: 'row', alignItems: 'center', gap: 6 },
  runnerCardTitle: {
    color: '#222229',
    fontSize: 14,
    fontWeight: '700',
  },
  runnerCardStatus: {
    marginTop: 2,
    fontSize: 12,
    color: '#666671',
  },
  runnerCardStatusOk: {
    color: '#137a3f',
  },
  runnerCardStatusWarning: {
    color: '#9a5700',
  },
  runnerCardStatusError: {
    color: '#b42318',
  },
  runnerCardDetail: {
    color: '#858590',
    fontSize: 11,
    lineHeight: 16,
  },
  runnerActionBtn: {
    borderRadius: 10,
    borderWidth: 1,
    borderColor: 'rgba(129,140,248,0.35)',
    backgroundColor: 'rgba(86,69,216,0.10)',
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  runnerActionBtnText: {
    color: '#5645d8',
    fontSize: 12,
    fontWeight: '700',
  },
  runnerActionBtnSelected: { borderColor: '#6555df', backgroundColor: 'rgba(86,69,216,0.18)' },
  routingSummary: { flexDirection: 'row', alignItems: 'center', gap: 8, paddingHorizontal: 2 },
  routingSummaryLabel: { color: '#7c7c87', fontSize: 11, fontWeight: '600' },
  routingSummaryValue: { flex: 1, color: '#6555df', fontSize: 12, fontWeight: '700', textAlign: 'right' },
  modelChoiceRow: { gap: 6, paddingTop: 2 },
  modelChoice: { borderRadius: 9, borderWidth: 1, borderColor: 'rgba(148,163,184,0.18)', paddingHorizontal: 9, paddingVertical: 6 },
  modelChoiceSelected: { borderColor: '#6555df', backgroundColor: 'rgba(86,69,216,0.12)' },
  modelChoiceText: { color: '#73737e', fontSize: 11 },
  modelChoiceTextSelected: { color: '#5e4ce6', fontWeight: '700' },
  runnerSectionError: {
    color: '#b42318',
    fontSize: 12,
    lineHeight: 18,
  },
  captureChoices: {
    gap: 10,
  },
  progressTrack: {
    height: 6,
    borderRadius: 3,
    backgroundColor: '#e5e5eb',
    overflow: 'hidden',
    marginTop: 4,
  },
  progressFill: {
    height: '100%',
    backgroundColor: '#818cf8',
    borderRadius: 3,
  },
  toast: {
    color: '#137a3f',
    fontSize: 13,
    textAlign: 'center',
    marginTop: 4,
  },
  error: {
    color: '#b42318',
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
    backgroundColor: '#ededf3',
  },
  quickIconToggleText: {
    color: '#5f5f69',
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
    color: '#8a5a12',
    fontSize: 12,
    lineHeight: 17,
  },
  iconSelector: {
    borderRadius: 12,
    borderWidth: 1,
    borderColor: '#e1e1e8',
    backgroundColor: '#fff',
    padding: 12,
    gap: 10,
  },
  iconSelectorTitle: {
    color: '#222229',
    fontSize: 13,
    fontWeight: '700',
  },
  iconSelectorText: {
    color: '#73737e',
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
    borderColor: '#e4e4ea',
    backgroundColor: '#f8f8fb',
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
    color: '#555561',
    fontSize: 11,
    fontWeight: '600',
  },
  cancelBtn: {
    marginTop: 4,
    borderRadius: 14,
    borderWidth: 1,
    borderColor: '#e1e1e8',
    paddingVertical: 15,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: '#fff',
  },
  cancelBtnText: {
    color: '#555561',
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
