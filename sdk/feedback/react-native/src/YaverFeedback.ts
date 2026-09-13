import { DeviceEventEmitter, NativeModules, Platform } from 'react-native';
import { FeedbackConfig, CapturedError } from './types';
import { YaverDiscovery } from './Discovery';
import { BlackBox } from './BlackBox';
import { P2PClient, resolveReportIdentity } from './P2PClient';
import { ShakeDetector } from './ShakeDetector';
import {
  captureStoreScreenshots,
  CaptureStoreScreenshotsOptions,
  CaptureStoreScreenshotsResult,
} from './storeShots';
import {
  configureAuthEndpoints,
  setStrictNativeAuth,
  getToken,
  getSelectedDeviceId,
  getDogfoodAccountAccess,
  setDogfoodControlPreference,
  listReachableDevices,
  clearToken,
  clearSelectedDeviceId,
  DEFAULT_CONVEX_SITE_URL,
} from './auth';
import {
  getQuickIconDisabled,
  getQuickIconColorPreset,
  setQuickIconDisabled,
  setQuickIconColorPreset,
  getDogfoodControlPresentation,
  setDogfoodControlPresentation as cacheDogfoodControlPresentation,
  getDogfoodControlOnboardingSeen,
  setDogfoodControlOnboardingSeen,
  getDogfoodEntryIconHidden,
  setDogfoodEntryIconHidden,
  getDogfoodModeActive,
  setDogfoodModeActive,
  getDogfoodUsageMode as getCachedDogfoodUsageMode,
  setDogfoodUsageMode as cacheDogfoodUsageMode,
  getDogfoodStartBehavior as getCachedDogfoodStartBehavior,
  setDogfoodStartBehavior as cacheDogfoodStartBehavior,
  getDogfoodRenderBehavior as getCachedDogfoodRenderBehavior,
  setDogfoodRenderBehavior as cacheDogfoodRenderBehavior,
  getDogfoodSessionBehavior as getCachedDogfoodSessionBehavior,
  setDogfoodSessionBehavior as cacheDogfoodSessionBehavior,
  getDogfoodRuntimeSelection as getCachedDogfoodRuntimeSelection,
  setDogfoodRuntimeSelection as cacheDogfoodRuntimeSelection,
  type DogfoodRuntimeSelection,
  type DogfoodControlPresentation,
  QuickIconColorPreset,
} from './preferences';
import {
  resolveSDKDogfood,
  resolveDogfoodUsageMode,
  resolveDogfoodStartBehavior,
  resolveDogfoodRenderBehavior,
  resolveDogfoodSessionBehavior,
  type SDKDogfoodStatus,
  type DogfoodAccessSnapshot,
  type DogfoodUsageMode,
  type DogfoodStartBehavior,
  type DogfoodRenderBehavior,
  type DogfoodSessionBehavior,
} from './dogfoodPolicy';
import { YaverDeviceDogfood, type DeviceDogfoodOptions, type DeviceDogfoodSession, type DeviceDogfoodState } from './deviceDogfood';
import {
  BrowserShortcutController,
  type BrowserShortcutRequest,
  type BrowserShortcutSnapshot,
} from './BrowserShortcut';

export interface DogfoodOnboardingOptions extends DeviceDogfoodOptions {
  /** Hint used to preselect the matching project returned by the owner machine. */
  projectName?: string;
  /** Framework fallback when the machine has not classified the project yet. */
  framework?: string;
}

export type DogfoodFlowPhase = 'idle' | 'denied' | 'auth-required' | 'machine-required' | 'opening' | 'error';
export interface DogfoodFlowState {
  phase: DogfoodFlowPhase;
  appId?: string;
  error?: string;
}

export interface DogfoodControlTriggerState {
  configured: boolean;
  authorized: boolean;
  appId?: string;
  installationId?: string;
  gestureSupported: boolean;
  gestureEnabled: boolean;
  fallbackVisible: boolean;
  presentation: DogfoodControlPresentation;
  onboardingSeen: boolean;
  reason: string;
  platform?: string;
}

interface DogfoodControlSyncOptions {
  presentation?: DogfoodControlPresentation;
  onboardingSeen?: boolean;
  /** User-initiated changes fail visibly when Convex cannot persist them. */
  requirePersistence?: boolean;
}

function unrefTimer(timer: ReturnType<typeof setTimeout>): void {
  const maybeNodeTimer = timer as unknown as { unref?: () => void };
  if (typeof maybeNodeTimer.unref === 'function') {
    maybeNodeTimer.unref();
  }
}

// Is this JS runtime the Yaver mobile app's super-host bridge? The
// YaverInfo native module is only registered inside Yaver's container
// (mobile/ios/Yaver/YaverInfo.{swift,m} + Android counterpart); a
// standalone app bundled by its own developer has no such module.
// When the SDK is loaded through Yaver's Hermes-push guest runtime we
// run in HOST MODE: dormant by default (no shake detector, no auto
// BlackBox, no QuickActionIcon — Yaver's host shell owns those), but
// we DO register a DeviceEventEmitter listener so Yaver's overlay can
// flip the SDK live at runtime. When the user shakes inside the guest
// app and taps "Feedback" on the Yaver overlay, AppDelegate dispatches
// `yaverFeedback:startReport` into this bridge; the listener wakes the
// SDK and opens the modal in-place over the running guest UI.
function isRunningInsideYaverHost(): boolean {
  try {
    return !!(NativeModules as any)?.YaverInfo;
  } catch {
    return false;
  }
}

// Two distinct compile-time modes for a guest app like sfmg / talos:
//
//   YAVER_HOST_MODE  — bundled by Yaver's agent (/dev/build-native) for
//                      loading inside Yaver mobile. SDK code is in the
//                      bundle but boots PASSIVE: no shake detector, no
//                      auto-BlackBox, no container UI. Yaver's host
//                      overlay owns the shake gesture; when the user
//                      taps "Feedback" on Yaver's overlay, AppDelegate
//                      dispatches yaverFeedback:hostActivate into this
//                      bridge and the SDK runtime-flips active for a
//                      single feedback session.
//
//   YAVER_SDK_MODE   — sfmg's own standalone TestFlight / Play Store
//                      build, with the Yaver SDK embedded. SDK boots
//                      active: shake → modal directly, no Yaver host
//                      involved. Default for normal `expo build`.
//
// Both can be forced at build time via process.env. When neither is
// set, fall back to runtime detection: if the YaverInfo native module
// exists (we're inside Yaver), assume HOST_MODE; else SDK_MODE. This
// keeps older bundles (built before the agent learned to set the env)
// working unchanged.
const YAVER_HOST_MODE_BUILD = (() => {
  try {
    const v = (process.env as any)?.YAVER_HOST_MODE;
    return v === 'true' || v === '1' || v === true || v === 1;
  } catch { return false; }
})();
const YAVER_SDK_MODE_BUILD = (() => {
  try {
    const v = (process.env as any)?.YAVER_SDK_MODE;
    return v === 'true' || v === '1' || v === true || v === 1;
  } catch { return false; }
})();

// Effective mode after considering build flags AND runtime detection.
const IS_HOST_MODE =
  YAVER_HOST_MODE_BUILD ||
  (!YAVER_SDK_MODE_BUILD && isRunningInsideYaverHost());

// Tracks whether we've been runtime-activated by the host (sfmg-in-Yaver
// case). Independent of `enabled` so we can tell "host turned us on for
// one shot" apart from "developer toggled enabled programmatically".
let hostActivated = false;

// Host-activation listener. Always registered when in HOST mode so a
// Yaver overlay tap can wake the SDK even before the guest's
// YaverFeedback.init() runs. AppDelegate (mobile/ios/Yaver/AppDelegate.
// swift::handleFeedbackTap) sends `yaverFeedback:startReport` into the
// guest bridge when the user picks Feedback on the shake overlay.
//
// Activation flow:
//   1. Try Yaver's existing bearer (NativeModules.YaverInfo.
//      inheritedAuthToken, populated by Yaver mobile's auth.ts on
//      sign-in). Validate against /auth/validate before trusting.
//   2. If valid: setAuthToken + open feedback modal in-place (the
//      modal already supports hot reload, screenshot, vibing chat).
//   3. If missing or invalid: open the SDK's own login screen.
if (IS_HOST_MODE) {
  try {
    const { DeviceEventEmitter } = require('react-native');
    DeviceEventEmitter.addListener('yaverFeedback:startReport', () => {
      hostActivated = true;
      enabled = true;
      void (async () => {
        const yi = (NativeModules as any)?.YaverInfo;
        const inheritedToken = String(yi?.inheritedAuthToken || '').trim();
        const inheritedAgent = String(yi?.inheritedAgentUrl || '').trim();
        const inheritedDevice = String(yi?.inheritedDeviceId || '').trim();
        if (inheritedToken) {
          // Lazy-import auth.ts so module load doesn't drag the auth
          // network code into the active set when the SDK is dormant.
          const { validateToken } = require('./auth');
          const user = await validateToken(inheritedToken).catch(() => null);
          if (user) {
            // Seed config + connect the SDK to the host's auth.
            if (!config) {
              YaverFeedback.init({
                authToken: inheritedToken,
                agentUrl: inheritedAgent || undefined,
                preferredDeviceId: inheritedDevice || undefined,
              } as FeedbackConfig);
            }
            await YaverFeedback.setAuthToken(inheritedToken);
            await YaverFeedback.startReport();
            return;
          }
        }
        // No token, or token invalid — fall through to the SDK's own
        // login screen. The user picks an OAuth provider; on success
        // the modal continues with the new token.
        if (!config) {
          YaverFeedback.init({} as FeedbackConfig);
        }
        YaverFeedback.showLogin();
      })();
    });
  } catch { /* react-native unavailable in jsdom unit tests */ }
}

let config: FeedbackConfig | null = null;
let enabled = false;
let p2pClient: P2PClient | null = null;
let renderP2PClient: P2PClient | null = null;
let shakeDetector: ShakeDetector | null = null;
/** Unsubscribe for the BlackBox command handler registered by init(). Held so
 *  a re-init (or destroy) can drop the old one instead of stacking. */
let commandUnsubscribe: (() => void) | null = null;
let p2pAuthToken: string | null = null;
let p2pRelayPassword: string = '';
/** Pending BlackBox auto-start retry. Held so destroy()/re-init can cancel it
 *  instead of leaving a timer chain running against a torn-down config. */
let autoStartTimer: ReturnType<typeof setTimeout> | null = null;
let reportLaunchInFlight = false;
let crashReportInFlight = false;
let dogfoodOnboarding: DogfoodOnboardingOptions | null = null;
let dogfoodRestoreInFlight: Promise<void> | null = null;
let dogfoodFlowState: DogfoodFlowState = { phase: 'idle' };
const dogfoodFlowListeners = new Set<(state: DogfoodFlowState) => void>();
let dogfoodShortcutAppStateSubscription: { remove: () => void } | null = null;
let dogfoodActivationSubscription: { remove: () => void } | null = null;
let dogfoodShortcutLaunchInFlight = false;
let lastDogfoodActivationUrl = '';

function dogfoodControlPreferenceScope(appId?: string, installationId?: string): string | undefined {
  if (!appId || !installationId) return undefined;
  return `${appId}:${installationId}`;
}

function publishDogfoodFlow(state: DogfoodFlowState): void {
  dogfoodFlowState = state;
  try { config?.dogfood?.onStateChange?.(state); } catch { /* host callback */ }
  dogfoodFlowListeners.forEach((listener) => {
    try { listener(state); } catch { /* host callbacks never break the SDK */ }
  });
}

async function consumeNativeDogfoodShortcut(): Promise<void> {
  if (dogfoodShortcutLaunchInFlight || !config?.dogfood?.appShortcut) return;
  const native = (NativeModules as any)?.YaverHotReload;
  if (typeof native?.consumeDogfoodShortcut !== 'function') return;
  try {
    if (!(await native.consumeDogfoodShortcut())) return;
    dogfoodShortcutLaunchInFlight = true;
    // Let FeedbackModal/AuthOverlay effects mount after a cold launch.
    const timer = setTimeout(() => {
      void YaverFeedback.openDogfood().finally(() => { dogfoodShortcutLaunchInFlight = false; });
    }, 350);
    unrefTimer(timer);
  } catch {
    dogfoodShortcutLaunchInFlight = false;
  }
}

function handleDogfoodActivationUrl(url?: string | null): void {
  if (!url || url === lastDogfoodActivationUrl || !config?.dogfood) return;
  try {
    const parsed = new URL(url);
    if (!parsed.protocol.startsWith('yaver-dogfood-') || parsed.hostname !== 'activate') return;
  } catch {
    return;
  }
  lastDogfoodActivationUrl = url;
  const timer = setTimeout(() => { void YaverFeedback.openDogfood(); }, 350);
  unrefTimer(timer);
}

/** Resolve the user's relay password by validating their auth token
 *  against Convex. Used whenever we (re)build the P2PClient so a
 *  relay-routed agentUrl carries a valid X-Relay-Password — without
 *  this, every relay-tunneled request rejects with HTTP 401
 *  "invalid relay password" (relay/server.go:957).
 *
 *  Cached on `p2pRelayPassword` so we only round-trip Convex when the
 *  user's auth token actually changes. Returns "" on any failure so
 *  direct LAN agentUrls (which need no password) keep working.
 */
async function resolveRelayPassword(authToken: string, convexUrl?: string): Promise<string> {
  const trimmed = (authToken || '').trim();
  if (!trimmed) {
    p2pRelayPassword = '';
    return '';
  }
  const url = (convexUrl || config?.convexUrl || DEFAULT_CONVEX_SITE_URL).replace(/\/+$/, '');
  try {
    // /settings returns {ok, settings: {relayPassword, relayUrl, ...}}
    // Older accounts may flatten relayPassword to the top — match the
    // tolerance the web shell already uses (route.ts:77).
    const res = await fetch(`${url}/settings`, {
      headers: { Authorization: `Bearer ${trimmed}` },
    });
    if (!res.ok) return p2pRelayPassword;
    const data = await res.json().catch(() => ({} as Record<string, unknown>));
    const settings = (data as { settings?: { relayPassword?: string } })?.settings;
    const pw =
      (typeof settings?.relayPassword === 'string' && settings.relayPassword) ||
      (typeof (data as { relayPassword?: string })?.relayPassword === 'string'
        ? (data as { relayPassword?: string }).relayPassword
        : '') ||
      '';
    p2pRelayPassword = pw;
    return pw;
  } catch {
    // Network failure on a passive Convex round-trip shouldn't break
    // direct-LAN flows. Fall through with whatever we already cached.
    return p2pRelayPassword;
  }
}

/** Ring buffer of captured errors. */
let errorBuffer: CapturedError[] = [];
let maxErrors = 5;
let crashHandlerInstalled = false;

/** Track whether BlackBox was running before disable (to restart on enable). */
let blackBoxWasStreaming = false;

/**
 * Tracks whether the user has already shaken once in this process.
 * Consumed by QuickActionIcon's `'after-shake'` mode so the icon
 * appears the first time a user discovers shake and stays around
 * thereafter.
 */
let firstShakeFired = false;

/**
 * Flag evaluation cache — 30s TTL per `userId|key`. Prevents a
 * tight render loop from hammering /flags/eval when the dev calls
 * `YaverFeedback.getFlag()` every frame.
 */
const flagCache: Map<string, { value: unknown; at: number }> = new Map();

/**
 * Main entry point for the Yaver Feedback SDK.
 * Call `YaverFeedback.init()` once at app startup.
 */
export class YaverFeedback {
  private static async resolveP2PAuthToken(): Promise<string | null> {
    return config?.authToken ?? null;
  }

  private static async rebuildP2PClient(agentUrl?: string): Promise<void> {
    const currentConfig = config;
    if (!currentConfig) return;
    const effectiveUrl = agentUrl ?? currentConfig.agentUrl;
    if (!effectiveUrl) {
      p2pClient = null;
      renderP2PClient = null;
      p2pAuthToken = null;
      return;
    }
    const token = await YaverFeedback.resolveP2PAuthToken();
    // reset() can clear config while relay-password resolution is awaiting.
    // Do not resurrect a client from that stale init generation.
    if (config !== currentConfig) return;
    if (!token) {
      p2pClient = null;
      renderP2PClient = null;
      p2pAuthToken = null;
      return;
    }
    p2pAuthToken = token;
    const rp = await resolveRelayPassword(token);
    if (config !== currentConfig) return;
    p2pClient = new P2PClient(effectiveUrl, token, rp);
    const renderUrl = currentConfig.renderAgentUrl || effectiveUrl;
    renderP2PClient = renderUrl === effectiveUrl ? p2pClient : new P2PClient(renderUrl, token, rp);
  }

  /**
   * Initialize the feedback SDK with the given configuration.
   * Typically called in your app's root component or entry file.
   *
   * If no `agentUrl` is provided, the SDK will attempt auto-discovery
   * via `YaverDiscovery` on the first `startReport()` call.
   */
  /** The runtime lane, when running on web inside a Yaver preview WebView. */
  static detectWebLane(): 'browser' | 'webrtc' | null {
    try {
      if (Platform.OS !== 'web' || typeof window === 'undefined') return null;
      const l = String((window as unknown as { __yaverLane?: string }).__yaverLane || '').toLowerCase();
      if (l === 'browser' || l === 'webrtc') return l;
    } catch { /* noop */ }
    return null;
  }

  /**
   * Mount a draggable DOM floating "Y" icon for the BROWSER lane (RN-web). It
   * lives in the WebView's document.body (position:fixed, max z-index), so it
   * renders ON TOP of the page inside the WebView and is never occluded by the
   * fullScreen preview modal — the one occlusion-proof affordance on iOS.
   * Tap opens the SDK's existing report flow (the RN FeedbackModal renders in
   * the same WebView DOM, also un-occluded). No-op off-web or off-lane, and
   * idempotent (a re-init reuses the existing node).
   */
  static mountBrowserLaneIcon(): void {
    if (YaverFeedback.detectWebLane() !== 'browser') return;
    try {
      const doc = (globalThis as unknown as { document?: Document }).document;
      if (!doc || !doc.body) return;
      if (doc.getElementById('yaver-feedback-btn')) return; // idempotent
      const btn = doc.createElement('div');
      btn.id = 'yaver-feedback-btn';
      btn.textContent = 'Y';
      btn.title = 'Yaver Feedback — drag to move · tap to report';
      btn.style.cssText =
        'position:fixed;bottom:20px;right:20px;width:44px;height:44px;border-radius:50%;' +
        'background:#6366f1;color:#fff;display:flex;align-items:center;justify-content:center;' +
        'cursor:pointer;z-index:99999;font-size:18px;font-weight:bold;touch-action:none;' +
        'box-shadow:0 4px 12px rgba(99,102,241,0.4);transition:transform 0.2s;';
      const drag = { on: false, moved: false, sx: 0, sy: 0, ox: 0, oy: 0 };
      const w = window as unknown as { innerWidth: number; innerHeight: number };
      const applyPos = (x: number, y: number) => {
        const m = 8;
        const nx = Math.max(m, Math.min(w.innerWidth - 44 - m, x));
        const ny = Math.max(m, Math.min(w.innerHeight - 44 - m, y));
        btn.style.left = nx + 'px'; btn.style.top = ny + 'px';
        btn.style.right = 'auto'; btn.style.bottom = 'auto';
        try { localStorage.setItem('yaver-feedback-btn-pos', JSON.stringify({ x: nx, y: ny })); } catch { /* noop */ }
      };
      try { const s = localStorage.getItem('yaver-feedback-btn-pos'); if (s) { const p = JSON.parse(s); if (typeof p?.x === 'number') applyPos(p.x, p.y); } } catch { /* noop */ }
      btn.addEventListener('pointerdown', (e: PointerEvent) => {
        drag.on = true; drag.moved = false;
        const r = btn.getBoundingClientRect();
        drag.sx = e.clientX; drag.sy = e.clientY; drag.ox = r.left; drag.oy = r.top;
        btn.setPointerCapture(e.pointerId); btn.style.transition = 'none';
      });
      btn.addEventListener('pointermove', (e: PointerEvent) => {
        if (!drag.on) return;
        const dx = e.clientX - drag.sx, dy = e.clientY - drag.sy;
        if (Math.abs(dx) + Math.abs(dy) > 5) drag.moved = true;
        if (drag.moved) applyPos(drag.ox + dx, drag.oy + dy);
      });
      const end = (e: PointerEvent) => {
        if (!drag.on) return;
        drag.on = false; btn.style.transition = 'transform 0.2s';
        try { btn.releasePointerCapture(e.pointerId); } catch { /* noop */ }
        if (!drag.moved) { try { void YaverFeedback.startReport(); } catch { /* noop */ } }
      };
      btn.addEventListener('pointerup', end);
      btn.addEventListener('pointercancel', end);
      // Also answer the host's forwarded shake (Yaver injects yaver-feedback:launch).
      const launch = () => { try { void YaverFeedback.startReport(); } catch { /* noop */ } };
      window.addEventListener('yaver-feedback:launch', launch as EventListener);
      (window as unknown as { __yaverFeedbackLaunch?: () => void }).__yaverFeedbackLaunch = launch;
      doc.body.appendChild(btn);
    } catch { /* noop — never break the guest app over a feedback affordance */ }
  }

  static init(cfg: FeedbackConfig): void {
    config = {
      trigger: 'shake',
      maxRecordingDuration: 120,
      autoLogin: true,
      ...cfg,
      agentUrl: cfg.codingAgentUrl || cfg.agentUrl,
      codingDeviceId: cfg.codingDeviceId || cfg.preferredDeviceId,
      renderDeviceId: cfg.renderDeviceId || cfg.codingDeviceId || cfg.preferredDeviceId,
    };
    if (IS_HOST_MODE) {
      // sfmg / talos / etc. running inside Yaver mobile (compile-time
      // YAVER_HOST_MODE or runtime-detected). Store config so a later
      // host activation (yaverFeedback:startReport from AppDelegate)
      // can open the modal — but skip the side effects Yaver's host
      // shell owns: shake detector, auto-BlackBox, QuickActionIcon.
      enabled = false;
      // Configure auth endpoints + strict-native-auth even in passive
      // mode so a host-activated session uses the same login routing
      // the standalone path would.
      configureAuthEndpoints({
        convexSiteUrl: cfg.authConvexSiteUrl,
        webBaseUrl: cfg.authWebBaseUrl,
      });
      setStrictNativeAuth(cfg.strictNativeAuth === true);
      if (!config.convexUrl) {
        config.convexUrl = cfg.authConvexSiteUrl ?? DEFAULT_CONVEX_SITE_URL;
      }
      maxErrors = cfg.maxCapturedErrors ?? 5;
      errorBuffer = [];
      if (cfg.crashReporting?.enabled && cfg.crashReporting?.installGlobalHandler) {
        YaverFeedback.installCrashHandler();
      }
      return;
    }
    if (config.disableShakeGesture && (!config.quickIcon || config.quickIcon === 'auto')) {
      config.quickIcon = 'always';
    }
    firstShakeFired = false;

    // Route the in-SDK login screen to prod yaver.io by default; callers may
    // override for staging via authConvexSiteUrl / authWebBaseUrl.
    configureAuthEndpoints({
      convexSiteUrl: cfg.authConvexSiteUrl,
      webBaseUrl: cfg.authWebBaseUrl,
    });
    // Compile-time lockdown: refuse any browser-hop / device-code fallback
    // and force ASWebAuthenticationSession in ephemeral mode for OAuth.
    setStrictNativeAuth(cfg.strictNativeAuth === true);
    // If no explicit convexUrl was set but we have an auth site URL, use it
    // so Discovery.discoverFromConvex() has somewhere to look up the user's
    // machines (works for both LAN-direct and off-LAN relay paths).
    if (!config.convexUrl) {
      config.convexUrl = cfg.authConvexSiteUrl ?? DEFAULT_CONVEX_SITE_URL;
    }

    // Default: enabled. Pre-0.8.8 the SDK only enabled shake in dev
    // builds (`__DEV__`), but apps that bundle the SDK explicitly *want*
    // shake to work in TestFlight / Play Store builds — that's the
    // primary use case (a tester finds a bug in a release build and
    // shakes to report it). Dev builds get shake too. Apps that want
    // to disable shake pass `enabled: false` (or
    // `disableShakeGesture: true` for finer-grained control).
    if (cfg.enabled !== undefined) {
      enabled = cfg.enabled;
    } else {
      enabled = !cfg.disableShakeGesture;
    }

    // Hydrate cached auth token + preferred device from AsyncStorage so the
    // SDK reconnects silently on subsequent launches. If autoLogin is false
    // the caller is responsible for providing authToken themselves.
    if (config.autoLogin !== false && enabled) {
      void YaverFeedback.hydrateSession();
    }

    if (config.dogfood?.appShortcut || config.dogfood?.controlGesture) {
      if (config.dogfood?.appShortcut) {
        void YaverFeedback.syncDogfoodAppShortcut();
        void consumeNativeDogfoodShortcut();
      }
      if (config.dogfood?.controlGesture) {
        void YaverFeedback.syncDogfoodControlGesture();
      }
      try {
        const { AppState } = require('react-native');
        dogfoodShortcutAppStateSubscription?.remove();
        dogfoodShortcutAppStateSubscription = AppState.addEventListener('change', (state: string) => {
          if (state === 'active') {
            if (config?.dogfood?.appShortcut) {
              void YaverFeedback.syncDogfoodAppShortcut();
              void consumeNativeDogfoodShortcut();
            }
            if (config?.dogfood?.controlGesture) {
              void YaverFeedback.syncDogfoodControlGesture();
            }
          }
        });
      } catch { /* native quick controls unavailable on web/test runtimes */ }
    }
    if (config.dogfood) {
      try {
        const { Linking } = require('react-native');
        dogfoodActivationSubscription?.remove();
        dogfoodActivationSubscription = Linking.addEventListener('url', ({ url }: { url: string }) => {
          handleDogfoodActivationUrl(url);
        });
        void Linking.getInitialURL().then(handleDogfoodActivationUrl).catch(() => {});
      } catch { /* linking unavailable in non-native test runtimes */ }
    }

    // Create P2P client if we have a URL
    if (config.agentUrl) {
      p2pAuthToken = config.authToken ?? null;
      // Initial construction uses the cached p2pRelayPassword (empty on
      // first init). rebuildP2PClient below resolves the real password
      // from Convex and replaces this client — but only when authToken
      // is set, so set a placeholder header here that won't 401 a
      // direct-LAN url and will be overwritten before any relay hop.
      p2pClient = new P2PClient(config.agentUrl, config.authToken ?? '', p2pRelayPassword);
      const renderUrl = config.renderAgentUrl || config.agentUrl;
      renderP2PClient = renderUrl === config.agentUrl
        ? p2pClient
        : new P2PClient(renderUrl, config.authToken ?? '', p2pRelayPassword);
      if (config.authToken) {
        void YaverFeedback.rebuildP2PClient(config.agentUrl);
      }
    } else {
      p2pClient = null;
      renderP2PClient = null;
      // Auto-discover agent in the background when convexUrl or LAN is available
      if (enabled && (config.authToken || config.preferredDeviceId)) {
        YaverFeedback.discoverAgent();
      }
    }

    // Set up error capture buffer size
    maxErrors = cfg.maxCapturedErrors ?? 5;
    errorBuffer = [];
    if (cfg.crashReporting?.enabled && cfg.crashReporting?.installGlobalHandler) {
      YaverFeedback.installCrashHandler();
    }

    // Wire up shake detection when trigger is 'shake'. Rebuild from scratch:
    // config was just replaced, so the old detector may be closing over stale
    // settings.
    YaverFeedback.stopShakeDetector();
    YaverFeedback.syncShakeDetector();

    // Browser lane (RN-web inside Yaver's fullScreen WebView preview): the
    // native shake module doesn't exist on web, and Yaver's own container
    // overlay is OCCLUDED behind the WebView modal on iOS. So mount an
    // occlusion-proof DRAGGABLE DOM icon INSIDE the WebView — same pattern as
    // yaver-feedback-web. Lane is stamped by Yaver via
    // window.__yaverLane='browser' (mobile PREVIEW_LANE_SCRIPT). See
    // docs/audits/feedback-sdk-lanes-audit-2026-07-28.md.
    if (enabled) YaverFeedback.mountBrowserLaneIcon();

    // Wire up BlackBox command handlers for reload + status signals from
    // the agent. Opens an SSE channel the agent uses to:
    //   - broadcast reload_bundle so the phone picks up a fresh Hermes
    //     bundle after a vibe-coding edit;
    //   - stream build/compile progress ("Compiling bundle…", "Pushing
    //     assets…", "Done") so the SDK can surface it like a normal
    //     "Working…" spinner instead of a silent 60-second freeze.
    if (enabled) {
      // onCommand appends to a static array and hands back an unsubscribe.
      // Dropping that unsubscribe meant every re-init stacked another handler
      // on top of the last, so an app that re-initialised (say, a settings
      // toggle) reloaded twice per agent command, then three times, and so on.
      // destroy() never cleared them either. Drop the previous registration
      // first so init() is idempotent here.
      commandUnsubscribe?.();
      commandUnsubscribe = BlackBox.onCommand((cmd) => {
        if (cmd.command === 'reload') {
          if (cfg.onReload) {
            cfg.onReload();
          } else {
            YaverFeedback.defaultReload();
          }
        } else if (cmd.command === 'reload_bundle' && cmd.data) {
          const bundleUrl = cmd.data.bundleUrl as string;
          const assetsUrl = cmd.data.assetsUrl as string | undefined;
          if (cfg.onReloadBundle) {
            cfg.onReloadBundle(bundleUrl, assetsUrl);
          } else {
            YaverFeedback.defaultReloadBundle(bundleUrl, assetsUrl);
          }
        } else if (cmd.command === 'status') {
          // Pipe agent progress pings to the UI. The FeedbackModal
          // subscribes to this event and renders the message + a
          // progress bar while a reload / build is in flight.
          const message =
            typeof cmd.data?.message === 'string'
              ? (cmd.data.message as string)
              : '';
          const phase =
            typeof cmd.data?.phase === 'string'
              ? (cmd.data.phase as string)
              : '';
          const progress =
            typeof cmd.data?.progress === 'number'
              ? Math.max(0, Math.min(1, cmd.data.progress as number))
              : undefined;
          const { DeviceEventEmitter } = require('react-native');
          DeviceEventEmitter.emit('yaverFeedback:status', {
            message,
            phase,
            progress,
            at: Date.now(),
          });
        }
      });
      // BlackBox auto-start (0.8.8+).
      //
      // 0.7.6 auto-started BlackBox immediately, which produced a
      // Hermes rope-string SIGSEGV on iOS 18.3.1 when the agent was in
      // bootstrap / needs-auth mode: the SSE channel retried with
      // exponential backoff on 401s, generating a tight string-concat
      // + JSON-parse loop that collided with react-native-view-shot's
      // internal string handling during Screenshot & Fix. We rolled it
      // back to manual-start (host calls BlackBox.start() after auth).
      //
      // The fix that lets us auto-start safely now:
      //   1. Defer the start by 500ms so init() returns, the JS bridge
      //      settles, and any first-launch auth-token round trip on
      //      another thread completes before SSE opens.
      //   2. Only start when we have BOTH an agentUrl AND an authToken
      //      — without the token, the connect() call would 401 and we'd
      //      reproduce the original loop.
      //   3. Caller can opt out with cfg.autoStartBlackBox = false.
      //
      // SFMG used to call BlackBox.start() inside YaverFeedbackWidget
      // after auth — that path still works (start() is idempotent), so
      // upgrading SDK without removing the manual call is safe.
      //   4. RETRY, rather than checking once. This was a single 500ms
      //      timeout, and on a cold start it could not succeed: an app that
      //      passes neither agentUrl nor authToken to init() (the normal
      //      case — they're restored from storage) gets its session from
      //      `void hydrateSession()`, which reads two AsyncStorage keys and
      //      then awaits discoverAgent(), a Convex round trip plus LAN
      //      probing. That does not finish in 500ms, so the check found no
      //      agentUrl, gave up, and BlackBox never started — meaning no SSE
      //      channel, so the agent's `reload_bundle` command had nowhere to
      //      land and Hermes hot reload silently did nothing until the user
      //      shook the device and drove discovery by hand.
      //
      //      The both-conditions guard is what keeps the 401 retry storm
      //      from coming back, so it is preserved exactly; we only re-check
      //      it over time instead of once. Bounded so a device that never
      //      signs in doesn't poll forever.
      if (cfg.autoStartBlackBox !== false) {
        YaverFeedback.scheduleBlackBoxAutoStart();
      }
    }

    // NOTE: We intentionally do NOT hook ErrorUtils.setGlobalHandler().
    // Sentry, Crashlytics, Bugsnag, and other tools all compete for that
    // single slot. Hijacking it would break whichever tool the developer
    // already has installed, depending on init order.
    //
    // Instead, developers use:
    //   - YaverFeedback.attachError(err) in catch blocks
    //   - YaverFeedback.wrapErrorHandler(existingHandler) to create a
    //     pass-through wrapper they insert into their own error chain
  }

  /**
   * Opt-in global crash handler. This wraps the existing React Native
   * ErrorUtils handler and still calls it, so Sentry/Crashlytics/Bugsnag can
   * remain the system of record. The SDK only uploads a crash report and,
   * when configured, asks the agent to create a fix task.
   */
  static installCrashHandler(): void {
    if (crashHandlerInstalled) return;
    try {
      const errorUtils = (globalThis as any)?.ErrorUtils;
      if (!errorUtils?.getGlobalHandler || !errorUtils?.setGlobalHandler) {
        return;
      }
      const next = errorUtils.getGlobalHandler();
      errorUtils.setGlobalHandler((error: Error, isFatal?: boolean) => {
        YaverFeedback.attachError(error, {
          source: 'global-handler',
          crashAware: true,
        });
        if (errorBuffer.length > 0) {
          errorBuffer[errorBuffer.length - 1].isFatal = isFatal ?? true;
        }
        void YaverFeedback.reportCrash(error, {
          isFatal: isFatal ?? true,
          source: 'global-handler',
        });
        next?.(error, isFatal);
      });
      crashHandlerInstalled = true;
    } catch (err) {
      console.warn('[YaverFeedback] installCrashHandler failed:', err);
    }
  }

  /**
   * Run agent discovery in the background.
   * Called automatically from init() when no agentUrl is provided.
   * Sets config.agentUrl and creates P2PClient on success.
   */
  static async discoverAgent(): Promise<void> {
    if (!config || (!enabled && !dogfoodOnboarding)) return;
    if (config.agentUrl) return; // already have a URL
    if (!config.authToken) return; // need auth before discovery can succeed

    try {
      const result = await YaverDiscovery.discover({
        convexUrl: config.convexUrl,
        authToken: config.authToken,
        preferredDeviceId: config.preferredDeviceId,
      });
      if (result && config) {
        config.agentUrl = result.url;
        await YaverFeedback.rebuildP2PClient(result.url);
      }
    } catch {
      // Discovery failed — FloatingButton will show disconnected, user can retry
    }
  }

  /**
   * Force a fresh Convex lookup for the agent URL — ignoring any
   * cached URL. Callers use this after a P2P request fails
   * (connection refused / timeout) because the most common cause is
   * the Mac's LAN IP rotating. Convex has the fresh one, so we
   * re-query and probe `[quicHost, ...localIps]` in parallel.
   *
   * Returns true when a new URL was adopted.
   */
  static async reconnect(): Promise<boolean> {
    if (!config || (!enabled && !dogfoodOnboarding)) return false;
    if (!config.authToken || !config.convexUrl) return false;
    try {
      const result = await YaverDiscovery.refreshFromConvex({
        convexUrl: config.convexUrl,
        authToken: config.authToken,
        preferredDeviceId: config.preferredDeviceId,
      });
      if (!result) return false;
      config.agentUrl = result.url;
      await YaverFeedback.rebuildP2PClient(result.url);
      return true;
    } catch {
      return false;
    }
  }

  /**
   * Pull a cached session token + selected device from AsyncStorage (populated
   * by the in-SDK login + machine-picker screens). When present the SDK can
   * reconnect silently on launch without re-prompting the user. Safe to call
   * multiple times — it only overrides values the caller did not already set.
   */
  static async hydrateSession(): Promise<void> {
    if (!config) return;
    try {
      if (!config.authToken) {
        const cached = await getToken();
        if (cached) {
          config.authToken = cached;
        }
      }
      if (!config.preferredDeviceId) {
        const cachedDevice = await getSelectedDeviceId();
        if (cachedDevice) {
          config.preferredDeviceId = cachedDevice;
        }
      }
      if (config.authToken && !config.agentUrl) {
        await YaverFeedback.discoverAgent();
      }
      await YaverFeedback.restoreApprovedDogfoodMode();
    } catch {
      // hydration best-effort
    }
  }

  /** Re-enter an explicitly active Dogfood session after Hermes recreates the
   * React bridge. Backend approval and the installation private key are
   * re-proved; a cached boolean never grants access by itself. */
  private static async restoreApprovedDogfoodMode(): Promise<void> {
    if (dogfoodRestoreInFlight) return dogfoodRestoreInFlight;
    const currentConfig = config;
    const configured = currentConfig?.dogfood;
    const appId = configured?.appId || currentConfig?.bundleId;
    if (!currentConfig || !configured || !appId || !currentConfig.authToken || !enabled) return;
    if (YaverFeedback.getDogfoodStatus().active || !(await getDogfoodModeActive(appId))) return;
    const restore = (async () => {
      const access = await YaverFeedback.getDogfoodAccess();
      if (config !== currentConfig || !access.authorized || access.deviceState !== 'active') return;
      await YaverFeedback.enableDeviceDogfood({
        appId,
        label: configured.label || currentConfig.projectName || appId,
        backendUrl: configured.backendUrl,
        authToken: currentConfig.authToken,
      });
    })().catch(() => {});
    dogfoodRestoreInFlight = restore;
    try {
      await restore;
    } finally {
      if (dogfoodRestoreInFlight === restore) dogfoodRestoreInFlight = null;
    }
  }

  /**
   * Update the signed-in session token (e.g. after the in-SDK login screen
   * succeeds). Rebuilds the P2P client and kicks off agent discovery.
   */
  static async setAuthToken(token: string): Promise<void> {
    if (!config) return;
    config.authToken = token;
    if (config.agentUrl) {
      await YaverFeedback.rebuildP2PClient(config.agentUrl);
    } else {
      await YaverFeedback.discoverAgent();
    }
    // Signing in is the event the auto-start was waiting for. If its bounded
    // retry already gave up (device left signed out past the window), this is
    // what brings the command channel up without requiring an app restart.
    if (config.autoStartBlackBox !== false && !BlackBox.isStreaming) {
      YaverFeedback.scheduleBlackBoxAutoStart();
    }
    await YaverFeedback.restoreApprovedDogfoodMode();
    await YaverFeedback.syncDogfoodAppShortcut().catch(() => false);
    await YaverFeedback.syncDogfoodControlGesture().catch(() => undefined);
    DeviceEventEmitter.emit('yaverFeedback:authChanged', { authenticated: true });
  }

  /** Returns true once the SDK has a session token it can use. */
  static isAuthed(): boolean {
    return Boolean(config?.authToken);
  }

  /**
   * On-device App Store screenshot capture (Engine 2). Walks the routes
   * the host hands us, screenshots each, and uploads to the agent which
   * runs the App Store Connect backend. agentUrl / authToken / relay
   * password default to the SDK's resolved session; the host only has to
   * supply a navigation ref + the ordered route list.
   *
   *   YaverFeedback.captureStoreScreenshots({
   *     app: 'sfmg',
   *     navigationRef,
   *     routes: ['/(tabs)/dashboard', '/(tabs)/messages', '/(tabs)/clients'],
   *     submit: true,
   *   })
   */
  static async captureStoreScreenshots(
    opts: Omit<CaptureStoreScreenshotsOptions, 'agentUrl' | 'authToken' | 'relayPassword'> &
      Partial<Pick<CaptureStoreScreenshotsOptions, 'agentUrl' | 'authToken' | 'relayPassword'>>,
  ): Promise<CaptureStoreScreenshotsResult> {
    const agentUrl = opts.agentUrl ?? config?.agentUrl ?? '';
    const authToken = opts.authToken ?? config?.authToken ?? p2pAuthToken ?? '';
    const relayPassword = opts.relayPassword ?? p2pRelayPassword;
    if (!agentUrl || !authToken) {
      return {
        ok: false,
        captured: 0,
        uploaded: 0,
        message: 'YaverFeedback not connected to an agent — init() with agentUrl + authToken first.',
      };
    }
    return captureStoreScreenshots({ ...opts, agentUrl, authToken, relayPassword });
  }

  /**
   * Let the mobile app / CLI kick THIS device into self-capturing. Registers
   * a BlackBox command listener that fires captureStoreScreenshots when the
   * agent pushes a `capture_store_shots` command (its `data` may override
   * `submit` / `locale`). Returns an unsubscribe fn. Call once after init,
   * passing the app's navigation ref + the routes to walk.
   */
  static enableStoreShotsOnCommand(
    opts: Omit<CaptureStoreScreenshotsOptions, 'agentUrl' | 'authToken' | 'relayPassword'>,
  ): () => void {
    return BlackBox.onCommand((cmd) => {
      if (cmd?.command !== 'capture_store_shots') return;
      const data = (cmd as any).data ?? {};
      void YaverFeedback.captureStoreScreenshots({
        ...opts,
        submit: typeof data.submit === 'boolean' ? data.submit : opts.submit,
        locale: typeof data.locale === 'string' ? data.locale : opts.locale,
      });
    });
  }

  /**
   * Request the embedded FeedbackModal to show the login screen. Works by
   * emitting an event the modal listens for — avoids forcing the host app
   * to mount a second navigator.
   */
  static showLogin(): void {
    const { DeviceEventEmitter } = require('react-native');
    DeviceEventEmitter.emit('yaverFeedback:startLogin');
  }

  /**
   * Request the embedded FeedbackModal to show the machine picker. Requires
   * an active session; no-ops otherwise.
   */
  static showMachinePicker(): void {
    if (!YaverFeedback.isAuthed()) return;
    const { DeviceEventEmitter } = require('react-native');
    DeviceEventEmitter.emit('yaverFeedback:startMachinePicker');
  }

  /** Configure Dogfood once during host init. Hosts still decide whether and
   * where to render an affordance; `openDogfood()` owns all flow mechanics. */
  static configureDogfood(options: DogfoodOnboardingOptions): void {
    dogfoodOnboarding = options;
  }

  /**
   * Open Dogfood using config.dogfood + the normal app identity. Cached OAuth,
   * machine, runner and model choices are reused. The corresponding picker is
   * shown only when a required choice is missing.
   */
  static async openDogfood(overrides?: Partial<DogfoodOnboardingOptions>): Promise<DogfoodFlowState> {
    if (!config && overrides?.appId) {
      return YaverFeedback.beginDogfoodOnboarding(overrides as DogfoodOnboardingOptions);
    }
    const configured = config?.dogfood;
    const appId = overrides?.appId || configured?.appId || config?.bundleId;
    if (!appId) {
      const state: DogfoodFlowState = { phase: 'error', error: 'Dogfood requires an appId or FeedbackConfig.bundleId.' };
      publishDogfoodFlow(state);
      return state;
    }
    dogfoodOnboarding = {
      appId,
      label: overrides?.label || configured?.label || config?.projectName || appId,
      projectName: overrides?.projectName || configured?.projectName || config?.projectName,
      framework: overrides?.framework || configured?.framework,
      backendUrl: overrides?.backendUrl || configured?.backendUrl,
      secureStore: overrides?.secureStore,
    };
    if (configured?.canShow) {
      // This hook is presentation policy, but it still receives the complete
      // backend-authoritative snapshot. Passing an owner-only approximation
      // here made legitimate approved testers fail custom `access.authorized`
      // gates even though their exact phone key was active.
      const access = await YaverFeedback.getDogfoodAccess();
      try {
        if (!(await configured.canShow(access))) {
          const state: DogfoodFlowState = { phase: 'denied', appId };
          publishDogfoodFlow(state);
          return state;
        }
      } catch (cause) {
        const state: DogfoodFlowState = {
          phase: 'error',
          appId,
          error: cause instanceof Error ? cause.message : String(cause),
        };
        publishDogfoodFlow(state);
        return state;
      }
    }
    // Opening Dogfood is explicit user intent. Show the SDK-owned setup
    // surface before network/account verification so slow, denied, and failed
    // checks all have visible progress and a route to recovery. Previously the
    // modal opened only after a successful verification, turning every other
    // result into a silent no-op in embedded hosts.
    DeviceEventEmitter.emit('yaverFeedback:startReport');
    return YaverFeedback.continueDogfoodOnboarding();
  }

  /** Resolve the host-facing ACL snapshot without granting authority. This is
   * the one endpoint custom Settings screens need for visibility/status UI. */
  static async getDogfoodAccess(): Promise<DogfoodAccessSnapshot> {
    const configured = config?.dogfood;
    const appId = dogfoodOnboarding?.appId || configured?.appId || config?.bundleId;
    if (!appId) throw new Error('Dogfood requires an appId or FeedbackConfig.bundleId.');
    let installationId: string | undefined;
    let deviceState: DogfoodAccessSnapshot['deviceState'] = 'unknown';
    try {
      const device = new YaverDeviceDogfood({
        appId,
        label: dogfoodOnboarding?.label || configured?.label || config?.projectName,
        backendUrl: dogfoodOnboarding?.backendUrl || configured?.backendUrl,
        secureStore: dogfoodOnboarding?.secureStore,
      });
      installationId = (await device.enrollmentInfo()).installationId;
      deviceState = await device.status();
    } catch {
      // UI status must degrade to unknown; openDogfood still offers owner OAuth.
    }
    const token = config?.authToken || await getToken();
    const account = token
      ? await getDogfoodAccountAccess(appId, token, installationId)
      : { authenticated: false, ownerAuthorized: false, accountAuthorized: false, installationAuthorized: false };
    const yaverAuthenticated = account.authenticated;
    const ownerAuthorized = account.ownerAuthorized;
    return {
      appId,
      yaverAuthenticated,
      ownerAuthorized,
      accountAuthorized: account.accountAuthorized,
      installationId,
      deviceState,
      authorized: yaverAuthenticated && account.installationAuthorized && deviceState === 'active',
      controlPresentation: account.controlPresentation,
      gestureSupported: account.gestureSupported,
      gestureCapabilityReason: account.gestureCapabilityReason,
      controlOnboardingSeen: account.controlOnboardingSeen,
    };
  }

  /** Add/remove the platform Home Screen shortcut from backend-authoritative
   * owner/device ACL state. Static plist shortcuts are intentionally avoided:
   * an unauthorized install must never advertise a hidden developer action. */
  static async syncDogfoodAppShortcut(): Promise<boolean> {
    const shortcut = config?.dogfood?.appShortcut;
    const native = (NativeModules as any)?.YaverHotReload;
    if (!shortcut || typeof native?.setDogfoodShortcut !== 'function') return false;
    const access = await YaverFeedback.getDogfoodAccess();
    let visible = access.authorized;
    if (visible && config?.dogfood?.canShow) {
      try {
        visible = await config.dogfood.canShow(access);
      } catch {
        visible = false;
      }
    }
    const label = typeof shortcut === 'object' && shortcut.label
      ? shortcut.label
      : `Dogfood ${config?.dogfood?.label || config?.projectName || ''}`.trim();
    // Product contract: both a valid full Yaver account AND this phone's
    // backend-approved app installation are required. Neither factor alone
    // advertises the developer surface.
    await native.setDogfoodShortcut(visible, label || 'Dogfood');
    return visible;
  }

  /** Capability-gated in-app Dogfood controls. Every exact installation
   * starts with Y and keeps it after onboarding unless the user explicitly
   * hides it in Dogfood Settings. A supported standalone app may select a
   * passive three-finger hold; missing native support keeps Y. Yaver's
   * own container suppresses both because its split preview/chat UI owns them. */
  static async syncDogfoodControlGesture(
    syncOptions: DogfoodControlSyncOptions = {},
  ): Promise<DogfoodControlTriggerState> {
    const configured = config?.dogfood?.controlGesture;
    const native = (NativeModules as any)?.YaverDogfoodGesture;
    const base: DogfoodControlTriggerState = {
      configured: Boolean(configured),
      authorized: false,
      gestureSupported: false,
      gestureEnabled: false,
      fallbackVisible: false,
      presentation: syncOptions.presentation || 'minimized-y',
      onboardingSeen: syncOptions.onboardingSeen === true,
      reason: configured ? 'native-module-unavailable' : 'not-configured',
      platform: Platform.OS,
    };
    if (!configured) {
      if (typeof native?.setEnabled === 'function') {
        await native.setEnabled(false, 900).catch(() => undefined);
      }
      return base;
    }
    // Backend approval makes the installation eligible; it does not mean the
    // tester is currently in Dogfood. Keep all in-app controls absent until
    // enableDeviceDogfood() has minted a live scoped session, and make Exit
    // Dogfood actually remove both the gesture and fallback Y.
    if (!YaverFeedback.getDogfoodStatus().active) {
      if (typeof native?.setEnabled === 'function') {
        await native.setEnabled(false, 900).catch(() => undefined);
      }
      return { ...base, reason: 'dogfood-session-inactive' };
    }
    if (IS_HOST_MODE || isRunningInsideYaverHost()) {
      if (typeof native?.setEnabled === 'function') {
        await native.setEnabled(false, 900).catch(() => undefined);
      }
      return { ...base, reason: 'yaver-host-owns-controls' };
    }

    const access = await YaverFeedback.getDogfoodAccess();
    let authorized = access.authorized;
    if (authorized && config?.dogfood?.canShow) {
      try {
        authorized = await config.dogfood.canShow(access);
      } catch {
        authorized = false;
      }
    }
    const options = typeof configured === 'object' ? configured : {};
    const durationMs = Math.min(2000, Math.max(650, options.durationMs ?? 900));
    const allowFallback = options.fallback !== 'none';
    if (!authorized) {
      if (typeof native?.setEnabled === 'function') {
        await native.setEnabled(false, durationMs).catch(() => undefined);
      }
      return { ...base, appId: access.appId, installationId: access.installationId, reason: 'installation-not-authorized' };
    }

    const preferenceScope = dogfoodControlPreferenceScope(access.appId, access.installationId);
    const cachedPresentation = await getDogfoodControlPresentation(preferenceScope);
    const cachedOnboardingSeen = await getDogfoodControlOnboardingSeen(preferenceScope);
    const onboardingSeen = syncOptions.onboardingSeen
      ?? access.controlOnboardingSeen
      ?? cachedOnboardingSeen;
    // The edge-docked Y is the discoverable default both before and after
    // onboarding. `auto` is only used when an existing user explicitly saved
    // that presentation; capability detection must never silently hide Y.
    const preferredPresentation = syncOptions.presentation
      || access.controlPresentation
      || cachedPresentation
      || options.defaultPresentation
      || 'minimized-y';
    const presentation: DogfoodControlPresentation = onboardingSeen
      ? preferredPresentation
      : 'minimized-y';

    if (typeof native?.getCapability !== 'function' || typeof native?.setEnabled !== 'function') {
      const result: DogfoodControlTriggerState = {
        ...base,
        authorized: true,
        appId: access.appId,
        installationId: access.installationId,
        presentation: 'minimized-y',
        onboardingSeen,
        fallbackVisible: allowFallback,
      };
      if (syncOptions.requirePersistence) {
        const token = config?.authToken || await getToken();
        if (!token || !access.installationId) {
          throw new Error('A full Yaver session is required to save Dogfood settings.');
        }
        const persisted = await setDogfoodControlPreference({
          appId: access.appId,
          installationId: access.installationId,
          token,
          presentation: 'minimized-y',
          gestureSupported: false,
          gestureCapabilityReason: 'native-module-unavailable',
          gesturePlatform: Platform.OS,
          controlOnboardingSeen: onboardingSeen,
        });
        if (!persisted) throw new Error('Could not save this Dogfood control preference to Yaver.');
        await cacheDogfoodControlPresentation('minimized-y', preferenceScope);
        if (onboardingSeen) await setDogfoodControlOnboardingSeen(true, preferenceScope);
      }
      return result;
    }
    try {
      const capability = await native.getCapability();
      const gestureSupported = capability?.supported === true;
      const shouldEnableGesture = gestureSupported && onboardingSeen && presentation === 'auto';
      const applied = await native.setEnabled(shouldEnableGesture, durationMs);
      const gestureEnabled = shouldEnableGesture && applied?.enabled === true;
      const gestureEnableFailed = shouldEnableGesture && !gestureEnabled;
      const result: DogfoodControlTriggerState = {
        configured: true,
        authorized: true,
        appId: access.appId,
        installationId: access.installationId,
        gestureSupported,
        gestureEnabled,
        fallbackVisible: allowFallback && (!gestureSupported || presentation === 'minimized-y' || gestureEnableFailed),
        presentation,
        onboardingSeen,
        reason: gestureEnableFailed
          ? 'gesture-enable-failed'
          : String(applied?.reason || capability?.reason || (gestureSupported ? 'supported' : 'unsupported')),
        platform: String(applied?.platform || capability?.platform || Platform.OS),
      };
      const token = config?.authToken || await getToken();
      if (syncOptions.requirePersistence && (!token || !access.installationId)) {
        await native.setEnabled(false, durationMs).catch(() => undefined);
        throw new Error('A full Yaver session is required to save Dogfood settings.');
      }
      if (token && access.installationId) {
        const needsPersistence =
          access.controlPresentation !== presentation
          || access.gestureSupported !== gestureSupported
          || access.gestureCapabilityReason !== result.reason
          || (syncOptions.onboardingSeen === true && access.controlOnboardingSeen !== true);
        if (needsPersistence) {
          const persisted = await setDogfoodControlPreference({
            appId: access.appId,
            installationId: access.installationId,
            token,
            presentation,
            gestureSupported,
            gestureCapabilityReason: result.reason,
            gesturePlatform: result.platform || Platform.OS,
            controlOnboardingSeen: onboardingSeen,
          });
          if (!persisted && syncOptions.requirePersistence) {
            await native.setEnabled(false, durationMs).catch(() => undefined);
            throw new Error('Could not save this Dogfood control preference to Yaver.');
          }
          if (persisted) {
            await cacheDogfoodControlPresentation(presentation, preferenceScope);
            if (onboardingSeen) await setDogfoodControlOnboardingSeen(true, preferenceScope);
          }
        } else {
          await cacheDogfoodControlPresentation(presentation, preferenceScope);
          if (onboardingSeen) await setDogfoodControlOnboardingSeen(true, preferenceScope);
        }
      }
      return result;
    } catch (error) {
      await native.setEnabled(false, durationMs).catch(() => undefined);
      if (syncOptions.requirePersistence) {
        throw error instanceof Error
          ? error
          : new Error('Could not save this Dogfood control preference to Yaver.');
      }
      return {
        ...base,
        authorized: true,
        appId: access.appId,
        installationId: access.installationId,
        presentation: 'minimized-y',
        onboardingSeen,
        fallbackVisible: allowFallback,
        reason: 'native-capability-check-failed',
      };
    }
  }

  /** Complete first-run onboarding or change the later control preference.
   * Convex is authoritative and keyed by full Yaver user + app + installation;
   * the local value is only a cold-start cache. */
  static async setDogfoodControlPresentation(
    presentation: DogfoodControlPresentation,
  ): Promise<DogfoodControlTriggerState> {
    if (presentation !== 'auto' && presentation !== 'minimized-y') {
      throw new Error('Invalid Dogfood control presentation.');
    }
    return YaverFeedback.syncDogfoodControlGesture({
      presentation,
      onboardingSeen: true,
      requirePersistence: true,
    });
  }

  /** Resolve the approved installation's UI mode. The saved value is scoped to
   * app + installation; the host config is only its first-run default. */
  static async getDogfoodUsageMode(): Promise<DogfoodUsageMode> {
    const access = await YaverFeedback.getDogfoodAccess();
    const scope = dogfoodControlPreferenceScope(access.appId, access.installationId);
    const cached = await getCachedDogfoodUsageMode(scope);
    return cached || resolveDogfoodUsageMode(config?.dogfood?.usageMode);
  }

  /** Change visible Dogfood controls only after full Yaver OAuth and exact
   * installation approval. The Go agent independently authenticates every
   * reload/chat request; this preference grants no capability. */
  static async setDogfoodUsageMode(mode: DogfoodUsageMode): Promise<DogfoodUsageMode> {
    const resolved = resolveDogfoodUsageMode(mode);
    await YaverFeedback.hydrateSession();
    const access = await YaverFeedback.getDogfoodAccess();
    if (!access.yaverAuthenticated) {
      YaverFeedback.showLogin();
      throw new Error('Sign in to Yaver before changing Dogfood mode.');
    }
    if (!access.authorized || !access.installationId) {
      throw new Error('This app installation must be approved for Dogfood first.');
    }
    const scope = dogfoodControlPreferenceScope(access.appId, access.installationId);
    await cacheDogfoodUsageMode(resolved, scope);
    if (config?.dogfood) config.dogfood.usageMode = resolved;
    try {
      const { DeviceEventEmitter } = require('react-native');
      DeviceEventEmitter.emit('yaverFeedback:dogfoodUsageModeChanged', { mode: resolved });
    } catch { /* non-native test runtime */ }
    return resolved;
  }

  private static async dogfoodPreferenceScope(): Promise<string> {
    await YaverFeedback.hydrateSession();
    const access = await YaverFeedback.getDogfoodAccess();
    if (!access.yaverAuthenticated) {
      YaverFeedback.showLogin();
      throw new Error('Sign in to Yaver before changing Dogfood settings.');
    }
    if (!access.authorized || !access.installationId) {
      throw new Error('This app installation must be approved for Dogfood first.');
    }
    return dogfoodControlPreferenceScope(access.appId, access.installationId)
      || `${access.appId}:${access.installationId}`;
  }

  static async getDogfoodStartBehavior(): Promise<DogfoodStartBehavior> {
    const scope = await YaverFeedback.dogfoodPreferenceScope();
    return (await getCachedDogfoodStartBehavior(scope))
      || resolveDogfoodStartBehavior(config?.dogfood?.startBehavior);
  }

  static async setDogfoodStartBehavior(value: DogfoodStartBehavior): Promise<DogfoodStartBehavior> {
    const resolved = resolveDogfoodStartBehavior(value);
    const scope = await YaverFeedback.dogfoodPreferenceScope();
    await cacheDogfoodStartBehavior(resolved, scope);
    if (config?.dogfood) config.dogfood.startBehavior = resolved;
    DeviceEventEmitter.emit('yaverFeedback:dogfoodExperienceChanged', { startBehavior: resolved });
    return resolved;
  }

  static async getDogfoodRenderBehavior(): Promise<DogfoodRenderBehavior> {
    const scope = await YaverFeedback.dogfoodPreferenceScope();
    return (await getCachedDogfoodRenderBehavior(scope))
      || resolveDogfoodRenderBehavior(config?.dogfood?.renderBehavior);
  }

  static async setDogfoodRenderBehavior(value: DogfoodRenderBehavior): Promise<DogfoodRenderBehavior> {
    const resolved = resolveDogfoodRenderBehavior(value);
    const scope = await YaverFeedback.dogfoodPreferenceScope();
    await cacheDogfoodRenderBehavior(resolved, scope);
    if (config?.dogfood) config.dogfood.renderBehavior = resolved;
    DeviceEventEmitter.emit('yaverFeedback:dogfoodExperienceChanged', { renderBehavior: resolved });
    return resolved;
  }

  static async getDogfoodSessionBehavior(): Promise<DogfoodSessionBehavior> {
    const scope = await YaverFeedback.dogfoodPreferenceScope();
    return (await getCachedDogfoodSessionBehavior(scope))
      || resolveDogfoodSessionBehavior(config?.dogfood?.sessionBehavior);
  }

  static async setDogfoodSessionBehavior(value: DogfoodSessionBehavior): Promise<DogfoodSessionBehavior> {
    const resolved = resolveDogfoodSessionBehavior(value);
    const scope = await YaverFeedback.dogfoodPreferenceScope();
    await cacheDogfoodSessionBehavior(resolved, scope);
    if (config?.dogfood) config.dogfood.sessionBehavior = resolved;
    DeviceEventEmitter.emit('yaverFeedback:dogfoodExperienceChanged', { sessionBehavior: resolved });
    return resolved;
  }

  static async getDogfoodSessions() {
    await YaverFeedback.dogfoodPreferenceScope();
    const client = await YaverFeedback.dogfoodCodingClient();
    const selection = await YaverFeedback.getDogfoodRuntimeSelection();
    return client.listVibeThreads({
      projectName: selection?.projectName || config?.dogfood?.projectName || config?.projectName,
      projectPath: selection?.projectPath || config?.dogfood?.projectPath,
    });
  }

  private static async dogfoodCodingClient(): Promise<P2PClient> {
    let client = YaverFeedback.getP2PClient();
    if (!client && await YaverFeedback.reconnect()) client = YaverFeedback.getP2PClient();
    if (!client) throw new Error('The selected coding machine is not connected yet. Reconnect it and retry.');
    return client;
  }

  static async getDogfoodSession(taskId: string) {
    await YaverFeedback.dogfoodPreferenceScope();
    return (await YaverFeedback.dogfoodCodingClient()).getVibeThread(taskId);
  }

  static async openDogfoodSession(taskId: string): Promise<void> {
    await YaverFeedback.dogfoodPreferenceScope();
    const clean = String(taskId || '').trim();
    if (!clean) throw new Error('Choose a Dogfood session first.');
    DeviceEventEmitter.emit('yaverFeedback:dogfoodSessionRequested', { taskId: clean });
  }

  static async completeDogfoodSession(taskId: string): Promise<void> {
    await YaverFeedback.dogfoodPreferenceScope();
    await (await YaverFeedback.dogfoodCodingClient()).completeVibeThread(taskId);
  }

  static async deleteDogfoodSession(taskId: string): Promise<void> {
    await YaverFeedback.dogfoodPreferenceScope();
    await (await YaverFeedback.dogfoodCodingClient()).deleteVibeThread(taskId);
  }

  /** Chat never starts rendering. It either restores the newest durable
   * runner/tmux-backed topic or opens a clean composer. */
  static async openDogfoodChat(): Promise<DogfoodFlowState> {
    await YaverFeedback.hydrateSession();
    const access = await YaverFeedback.getDogfoodAccess();
    if (!access.yaverAuthenticated || !access.authorized) return YaverFeedback.openDogfood();
    const selection = await YaverFeedback.getDogfoodRuntimeSelection();
    if (!config?.preferredDeviceId || !selection?.projectPath) return YaverFeedback.openDogfood();
    if (await YaverFeedback.getDogfoodSessionBehavior() === 'resume-last') {
      const sessions = await YaverFeedback.getDogfoodSessions();
      if (sessions[0]) {
        await YaverFeedback.openDogfoodSession(sessions[0].id);
        return { phase: 'opening', appId: access.appId };
      }
    }
    DeviceEventEmitter.emit('yaverFeedback:dogfoodNewChatRequested');
    return { phase: 'opening', appId: access.appId };
  }

  /** One-tap fast reload for the compact Dogfood card. It preserves the same
   * selected render machine and bearer-authenticated P2P route as the full
   * Feedback modal, and names missing auth/machine state instead of no-oping. */
  static async requestDogfoodFastReload(): Promise<string> {
    await YaverFeedback.hydrateSession();
    if (!config?.authToken) {
      YaverFeedback.showLogin();
      throw new Error('Sign in to Yaver before requesting Fast Reload.');
    }
    if (!config.preferredDeviceId) {
      YaverFeedback.showMachinePicker();
      throw new Error('Choose a render machine before requesting Fast Reload.');
    }
    const selected = await YaverFeedback.getSelectedRemoteDevice();
    if (!selected) {
      YaverFeedback.showMachinePicker();
      throw new Error('The selected render machine is no longer available.');
    }
    if (selected.needsAuth) {
      YaverFeedback.showMachinePicker();
      throw new Error('The selected render machine needs pairing again.');
    }
    if (!selected.isOnline) {
      throw new Error('The selected render machine is offline.');
    }
    let client = YaverFeedback.getRenderP2PClient();
    if (!client && await YaverFeedback.reconnect()) {
      client = YaverFeedback.getRenderP2PClient();
    }
    if (!client) throw new Error('The render machine is not connected yet.');
    const access = await YaverFeedback.getDogfoodAccess();
    if (!access.authorized || !access.installationId) {
      throw new Error('This app installation must be approved for Dogfood before it can reload.');
    }
    const appId = access.appId || config.dogfood?.appId || config.bundleId || '';
    const saved = appId ? await getCachedDogfoodRuntimeSelection(appId) : null;
    const selection: DogfoodRuntimeSelection = saved || {
      lane: 'browser',
      projectName: config.dogfood?.projectName || config.projectName,
      projectPath: config.dogfood?.projectPath,
      targetDeviceId: config.dogfood?.targetDeviceId,
      runtimeSessionId: config.dogfood?.runtimeSessionId,
    };
    if (!selection.projectPath?.trim()) {
      throw new Error('Choose an exact Git checkout in Dogfood Settings before reloading.');
    }
    // Browser Dogfood reload is an authenticated request to the selected
    // render box; it refreshes the browser dev server directly and has no
    // native BlackBox recipient. Requiring that channel made Reload Only
    // silently unusable for browser-lane apps such as SFMG. Hermes is the
    // lane that actually broadcasts to the installed app, so it keeps the
    // recipient check and target pin.
    if (selection.lane === 'hermes' && (!BlackBox.isStreaming || !BlackBox.isCommandChannelConnected || !BlackBox.currentDeviceId)) {
      throw new Error('The app reload channel is not connected. Reopen Dogfood Settings after the app reconnects.');
    }
    const reloadSelection = selection.lane === 'hermes'
      ? { ...selection, targetDeviceId: BlackBox.currentDeviceId }
      : selection;
    try {
      const ack = await client.reloadDogfood({
        ...reloadSelection, mode: 'fast', bundleId: config.bundleId,
      });
      return ack.message;
    } catch (firstError) {
      if (await YaverFeedback.reconnect()) {
        const retry = YaverFeedback.getRenderP2PClient();
        if (retry) return (await retry.reloadDogfood({ ...reloadSelection, mode: 'fast', bundleId: config.bundleId })).message;
      }
      throw firstError;
    }
  }

  /** Explicit repair for a selected render box whose agent predates Dogfood
   * Reload. Authentication and reachability are rechecked at tap time. */
  static async updateDogfoodRenderAgent(): Promise<string> {
    await YaverFeedback.hydrateSession();
    const access = await YaverFeedback.getDogfoodAccess();
    if (!access.yaverAuthenticated || !access.authorized) {
      throw new Error('Yaver OAuth and an approved Dogfood installation are required.');
    }
    let client = YaverFeedback.getRenderP2PClient();
    if (!client && await YaverFeedback.reconnect()) client = YaverFeedback.getRenderP2PClient();
    if (!client) throw new Error('The render machine is not connected yet.');
    return client.updateAgentForDogfood();
  }

  /** Open the compact Dogfood Usage surface without routing through chat or
   * setup. Missing OAuth/approval is routed to the normal onboarding flow so
   * the user always has an in-place fix instead of a dead button. */
  static async openDogfoodUsage(): Promise<DogfoodFlowState> {
    await YaverFeedback.hydrateSession();
    const access = await YaverFeedback.getDogfoodAccess();
    if (!access.yaverAuthenticated || !access.authorized || !YaverFeedback.getDogfoodStatus().active) {
      return YaverFeedback.openDogfood();
    }
    const selection = await YaverFeedback.getDogfoodRuntimeSelection();
    let runtimeActive = false;
    if (selection?.lane === 'browser' && selection.projectPath) {
      const client = YaverFeedback.getP2PClient();
      const status = client ? await client.getDogfoodDevServerStatus() : null;
      runtimeActive = status?.running === true
        && status.serving === true
        && status.workDir === selection.projectPath;
    } else if (selection?.lane === 'hermes') {
      runtimeActive = BlackBox.isStreaming
        && BlackBox.isCommandChannelConnected
        && Boolean(BlackBox.currentDeviceId);
    } else if (selection?.lane === 'webrtc') {
      runtimeActive = Boolean(selection.runtimeSessionId);
    }
    // Approval, a cached checkout, and a past successful launch are inventory.
    // The compact controls are useful only while their real runtime is alive;
    // otherwise the host entry must reopen setup and auto-launch it.
    if (!runtimeActive) return YaverFeedback.openDogfood();
    try {
      const { DeviceEventEmitter } = require('react-native');
      DeviceEventEmitter.emit('yaverFeedback:dogfoodUsageRequested');
    } catch {
      return { phase: 'error', appId: access.appId, error: 'Dogfood Usage is unavailable on this platform.' };
    }
    const state: DogfoodFlowState = { phase: 'opening', appId: access.appId };
    publishDogfoodFlow(state);
    return state;
  }

  /** Persist the Settings selection used by the compact Dogfood Usage card. */
  static async setDogfoodRuntimeSelection(selection: DogfoodRuntimeSelection): Promise<void> {
    const access = await YaverFeedback.getDogfoodAccess();
    if (!access.yaverAuthenticated || !access.authorized) {
      throw new Error('Yaver OAuth and an approved Dogfood installation are required.');
    }
    const appId = access.appId || config?.dogfood?.appId || config?.bundleId || '';
    if (!appId) throw new Error('Dogfood app identity is missing.');
    await cacheDogfoodRuntimeSelection(appId, selection);
    if (config?.dogfood) Object.assign(config.dogfood, selection);
  }

  static async getDogfoodRuntimeSelection(): Promise<DogfoodRuntimeSelection | null> {
    const access = await YaverFeedback.getDogfoodAccess();
    if (!access.yaverAuthenticated || !access.authorized) return null;
    const appId = access.appId || config?.dogfood?.appId || config?.bundleId || '';
    return appId ? getCachedDogfoodRuntimeSelection(appId) : null;
  }

  /** Backwards-compatible entry point for existing integrations. */
  static async beginDogfoodOnboarding(options: DogfoodOnboardingOptions): Promise<DogfoodFlowState> {
    YaverFeedback.configureDogfood(options);
    if (!config) {
      YaverFeedback.init({
        autoLogin: true,
        enabled: true,
        projectName: options.projectName || options.label,
        bundleId: options.appId,
      } as FeedbackConfig);
    }
    return YaverFeedback.continueDogfoodOnboarding();
  }

  /** Continue after OAuth or machine selection. Public so custom host UI can
   * hand control back without recreating the SDK state machine. */
  static async continueDogfoodOnboarding(): Promise<DogfoodFlowState> {
    const appId = dogfoodOnboarding?.appId;
    if (!dogfoodOnboarding || !appId) {
      const state: DogfoodFlowState = { phase: 'error', error: 'Dogfood is not configured.' };
      publishDogfoodFlow(state);
      return state;
    }
    let token: string | null;
    let account: Awaited<ReturnType<typeof getDogfoodAccountAccess>> | {
      authenticated: false;
      ownerAuthorized: false;
      accountAuthorized: false;
      installationAuthorized: false;
    };
    try {
      await YaverFeedback.hydrateSession();
      token = config?.authToken || await getToken();
      account = token
        ? await getDogfoodAccountAccess(appId, token)
        : { authenticated: false, ownerAuthorized: false, accountAuthorized: false, installationAuthorized: false };
    } catch (cause) {
      const state: DogfoodFlowState = {
        phase: 'error',
        appId,
        error: cause instanceof Error ? cause.message : 'Yaver could not verify Dogfood access.',
      };
      publishDogfoodFlow(state);
      return state;
    }
    // A device key is a second factor for this installation, never a
    // replacement for a real Yaver account. Narrow installation sessions and
    // stale/invalid cached tokens both return authenticated=false here.
    if (!account.authenticated) {
      const state: DogfoodFlowState = { phase: 'auth-required', appId };
      publishDogfoodFlow(state);
      YaverFeedback.showLogin();
      return state;
    }
    if (!account.accountAuthorized) {
      const state: DogfoodFlowState = {
        phase: 'denied',
        appId,
        error: 'The app owner has not enabled Dogfood for this Yaver account.',
      };
      publishDogfoodFlow(state);
      return state;
    }
    if (!config?.preferredDeviceId && token) {
      const devices = await listReachableDevices(token);
      const ready = devices.owned.filter((device) => device.isOnline && !device.needsAuth);
      if (ready.length === 1) await YaverFeedback.setPreferredDevice(ready[0].deviceId);
    }
    if (!config?.preferredDeviceId) {
      const state: DogfoodFlowState = { phase: 'machine-required', appId };
      publishDogfoodFlow(state);
      YaverFeedback.showMachinePicker();
      return state;
    }
    const state: DogfoodFlowState = { phase: 'opening', appId };
    publishDogfoodFlow(state);
    const { DeviceEventEmitter } = require('react-native');
    DeviceEventEmitter.emit('yaverFeedback:startReport');
    return state;
  }

  static getDogfoodFlowState(): DogfoodFlowState {
    return dogfoodFlowState;
  }

  static onDogfoodFlowState(listener: (state: DogfoodFlowState) => void): () => void {
    dogfoodFlowListeners.add(listener);
    listener(dogfoodFlowState);
    return () => dogfoodFlowListeners.delete(listener);
  }

  static getDogfoodOnboarding(): DogfoodOnboardingOptions | null {
    return dogfoodOnboarding;
  }

  static clearDogfoodOnboarding(): void {
    dogfoodOnboarding = null;
    publishDogfoodFlow({ phase: 'idle' });
  }

  /**
   * Update the selected remote device. Resets the cached agent URL so the
   * next `startReport()` (or FloatingButton press) rediscovers against the
   * newly-selected machine.
   */
  static async setPreferredDevice(deviceId: string): Promise<void> {
    if (!config) return;
    const wasUnified = !config.renderDeviceId
      || config.renderDeviceId === config.codingDeviceId
      || config.renderDeviceId === config.preferredDeviceId;
    config.preferredDeviceId = deviceId;
    config.codingDeviceId = deviceId;
    if (wasUnified) config.renderDeviceId = deviceId;
    config.agentUrl = undefined;
    config.codingAgentUrl = undefined;
    if (wasUnified) config.renderAgentUrl = undefined;
    p2pClient = null;
    renderP2PClient = null;
    p2pAuthToken = null;
    await import('./auth').then(({ saveSelectedDeviceId }) => saveSelectedDeviceId(deviceId));
    await YaverFeedback.discoverAgent();
  }

  /** Resolve the currently selected remote machine from the authenticated device list. */
  static async getSelectedRemoteDevice() {
    if (!config?.authToken || !config.preferredDeviceId) return null;
    const preferredDeviceId = config.preferredDeviceId;
    const devices = await listReachableDevices(config.authToken);
    return devices.owned.find((device) => device.deviceId === preferredDeviceId) ?? null;
  }

  /**
   * Trigger remote device-auth for a CLI runner on the selected agent
   * (codex login --device-auth / claude auth login --console). Returns
   * the session so the host UI can render the verification URL + code.
   *
   * RN UI layer owns the modal (see FeedbackModal's runner sign-in
   * buttons). This method just proxies into P2PClient — no browser
   * launch, no API keys, works through the relay with an SDK token
   * that carries the runner-auth scope.
   */
  static async startRunnerBrowserAuth(
    runner: string,
  ): Promise<import('./types').RunnerBrowserAuthSession> {
    if (!p2pClient) {
      throw new Error('Not connected to any agent. Select a machine first.');
    }
    return p2pClient.startRunnerBrowserAuth(runner);
  }

  static async getRunnerBrowserAuthStatus(
    sessionId: string,
  ): Promise<import('./types').RunnerBrowserAuthSession> {
    if (!p2pClient) throw new Error('Not connected to any agent.');
    return p2pClient.getRunnerBrowserAuthStatus(sessionId);
  }

  static async cancelRunnerBrowserAuth(sessionId: string): Promise<void> {
    if (!p2pClient) return;
    await p2pClient.cancelRunnerBrowserAuth(sessionId);
  }

  /** Submit the Claude paste-back verifier so the agent can finalise the
   *  OAuth handshake. RunnerAuthModal calls this after the user copies
   *  the code from platform.claude.com's callback page. */
  static async submitRunnerBrowserAuthCode(
    sessionId: string,
    code: string,
  ): Promise<import('./types').RunnerBrowserAuthSession> {
    if (!p2pClient) {
      throw new Error('Not connected to any agent.');
    }
    return p2pClient.submitRunnerBrowserAuthCode(sessionId, code);
  }

  /**
   * Sign out: clear cached token + device, tear down the P2P client. The
   * SDK stays enabled; the next feedback trigger will re-prompt for login.
   */
  static async signOut(): Promise<void> {
    await setDogfoodModeActive(false, config?.dogfood?.appId || config?.bundleId);
    if (config?.dogfood) config.dogfood.enabled = false;
    await clearToken();
    await clearSelectedDeviceId();
    if (config) {
      config.authToken = undefined;
      config.preferredDeviceId = undefined;
      config.agentUrl = undefined;
    }
    p2pClient = null;
    renderP2PClient = null;
    p2pAuthToken = null;
    await YaverFeedback.syncDogfoodAppShortcut().catch(() => false);
    await YaverFeedback.syncDogfoodControlGesture().catch(() => undefined);
    DeviceEventEmitter.emit('yaverFeedback:authChanged', { authenticated: false });
  }

  /**
   * Manually trigger the feedback collection flow.
   * Opens the feedback modal if the SDK is initialized and enabled.
   *
   * If no agentUrl was configured, runs auto-discovery first.
   */
  static async startReport(): Promise<void> {
    if (!config) {
      console.warn('[YaverFeedback] SDK not initialized. Call YaverFeedback.init() first.');
      return;
    }
    if (!enabled) {
      return;
    }
    if (reportLaunchInFlight) {
      return;
    }

    reportLaunchInFlight = true;
    const { DeviceEventEmitter } = require('react-native');
    DeviceEventEmitter.emit('yaverFeedback:reportLaunch', {
      state: 'starting',
      at: Date.now(),
    });
    try {

      // If the caller has autoLogin enabled and we have no session yet, show
      // the in-SDK login flow instead of a failing discovery + warning spam.
      if (!config.authToken) {
        if (config.autoLogin !== false) {
          await YaverFeedback.hydrateSession();
        }
        if (!config.authToken) {
          YaverFeedback.showLogin();
          return;
        }
      }

      // Auto-discover if no agent URL was provided
      if (!config.agentUrl) {
        try {
          const result = await YaverDiscovery.discover({
            convexUrl: config.convexUrl,
            authToken: config.authToken,
            preferredDeviceId: config.preferredDeviceId,
          });
          if (result) {
            config.agentUrl = result.url;
            await YaverFeedback.rebuildP2PClient(result.url);
          } else if (config.autoLogin !== false && !config.preferredDeviceId) {
            // No agent discovered and no device picked yet — prompt the user
            // to pick one of their machines (handles the non-LAN case where
            // relay discovery requires knowing which deviceId to target).
            YaverFeedback.showMachinePicker();
            return;
          } else {
            console.warn('[YaverFeedback] No agent found. Check that `yaver serve` is running on the selected machine.');
          }
        } catch (err) {
          console.warn('[YaverFeedback] Auto-discovery failed:', err);
        }
      }

      // Emit event that the FeedbackModal listens for
      DeviceEventEmitter.emit('yaverFeedback:startReport');
    } finally {
      reportLaunchInFlight = false;
      DeviceEventEmitter.emit('yaverFeedback:reportLaunch', {
        state: 'settled',
        at: Date.now(),
      });
    }
  }

  /** Returns true if the SDK has been initialized. */
  static isInitialized(): boolean {
    return config !== null;
  }

  /**
   * Enable or disable the entire feedback SDK at runtime.
   *
   * **Disable (false):**
   * - Stops BlackBox streaming (flushes remaining events first)
   * - Restores console.log/warn/error if wrapped
   * - Clears error buffer
   * - All methods become no-ops (attachError, wrapErrorHandler still safe to call but do nothing)
   * - P2P client is kept alive (no reconnection cost on re-enable)
   *
   * **Enable (true):**
   * - Restarts BlackBox streaming if it was running before disable
   * - Error buffer starts collecting again
   * - All methods become active
   */
  static setEnabled(value: boolean): void {
    if (enabled === value) return; // No-op if already in desired state

    if (!value) {
      // === DISABLE ===
      blackBoxWasStreaming = BlackBox.isStreaming;
      if (BlackBox.isStreaming) {
        BlackBox.stop(); // flush + stop timer + unwrap console
      }
      BlackBox.unwrapConsole(); // ensure console is restored even if BlackBox wasn't started
      errorBuffer = [];
      YaverFeedback.stopShakeDetector();
    } else {
      // === ENABLE ===
      if (blackBoxWasStreaming) {
        BlackBox.start(); // restart with previous config
      }
      enabled = true; // syncShakeDetector reads this
      YaverFeedback.syncShakeDetector();
    }

    enabled = value;
  }

  /**
   * Turn shake-to-report on or off without tearing the SDK down.
   *
   * `trigger` is read only inside init(), so before this the only ways to
   * stop listening for a shake were setEnabled(false) — which also kills the
   * flight recorder and the agent command channel — or a re-init, which used
   * to stack a duplicate command handler. Neither is what "turn the shake
   * catcher off" should cost.
   *
   * Persists onto config.disableShakeGesture, so a later setEnabled(true)
   * honours it rather than resurrecting the listener.
   */
  static setShakeEnabled(value: boolean): void {
    if (!config) return;
    config.disableShakeGesture = !value;
    YaverFeedback.syncShakeDetector();
  }

  /** Whether the shake listener is currently armed. */
  static isShakeEnabled(): boolean {
    return shakeDetector !== null;
  }

  /**
   * Bring the shake listener in line with the current config. Idempotent —
   * safe to call whenever `enabled`, `trigger`, or `disableShakeGesture`
   * moves.
   */
  private static syncShakeDetector(): void {
    const want =
      enabled && config?.trigger === 'shake' && !config?.disableShakeGesture;
    if (want && !shakeDetector) {
      shakeDetector = new ShakeDetector();
      shakeDetector.start(() => {
        YaverFeedback.notifyShake();
        if (config?.reportingOnly) {
          YaverFeedback.sendAutoReport();
        } else {
          YaverFeedback.startReport();
        }
      });
    } else if (!want && shakeDetector) {
      YaverFeedback.stopShakeDetector();
    }
  }

  private static stopShakeDetector(): void {
    if (shakeDetector) {
      shakeDetector.stop();
      shakeDetector = null;
    }
  }

  /** Cancel a pending BlackBox auto-start retry chain. */
  private static cancelBlackBoxAutoStart(): void {
    if (autoStartTimer) {
      clearTimeout(autoStartTimer);
      autoStartTimer = null;
    }
  }

  /**
   * Start BlackBox as soon as we have BOTH an agentUrl and a token, polling
   * until then.
   *
   * Both conditions are mandatory and deliberate: starting without a token
   * makes the SSE channel 401 and retry with backoff, which is the tight
   * string-concat + JSON-parse loop that used to SIGSEGV Hermes on iOS 18.3.1
   * during Screenshot & Fix. This only re-checks that same guard over time.
   *
   * Bounded at ~60s: long enough for AsyncStorage hydration plus a Convex
   * discovery round trip on a cold, off-LAN start, short enough that a device
   * whose user never signs in stops polling. Giving up here costs nothing —
   * setAuthToken() and startReport() both reschedule, so signing in later
   * still brings the channel up.
   */
  private static scheduleBlackBoxAutoStart(attempt = 0): void {
    const MAX_ATTEMPTS = 60;
    const FIRST_DELAY_MS = 500;
    const RETRY_DELAY_MS = 1000;
    // Re-run discovery every Nth tick rather than every tick: it is a Convex
    // round trip plus LAN probing, so once a second would hammer both. Every
    // 3s is responsive enough that waking the dev machine reconnects the phone
    // on its own, without the user wondering whether to restart the app.
    const REDISCOVER_EVERY = 3;

    YaverFeedback.cancelBlackBoxAutoStart();
    if (!enabled || attempt >= MAX_ATTEMPTS) return;

    const timer = setTimeout(() => {
      autoStartTimer = null;
      // A destroy() or setEnabled(false) between scheduling and firing.
      if (!enabled || !config) return;
      if (BlackBox.isStreaming) return;

      if (config.agentUrl && (config.authToken || p2pAuthToken)) {
        try {
          // Pass the host's BlackBox config through. This used to call
          // start() bare, so a host that configured the flight recorder
          // — as the README itself shows — had it silently replaced by
          // defaults, and appName came out ''.
          BlackBox.start(config.blackBox);
        } catch (err) {
          console.warn('[YaverFeedback] BlackBox auto-start failed:', err);
        }
        return;
      }

      // No agent yet. Re-attempt discovery instead of only re-reading the
      // flag: hydrateSession() runs discoverAgent() exactly once at init, so
      // if the dev machine was unreachable at that moment — asleep, phone
      // still on cellular before the relay came up, agent not yet serving —
      // agentUrl stayed null for the whole process lifetime and only an app
      // restart could recover it. discoverAgent() self-guards on "already
      // have a URL" and "no token", so calling it again is cheap and safe.
      if (!config.agentUrl && attempt > 0 && attempt % REDISCOVER_EVERY === 0) {
        void YaverFeedback.discoverAgent();
      }

      YaverFeedback.scheduleBlackBoxAutoStart(attempt + 1);
    }, attempt === 0 ? FIRST_DELAY_MS : RETRY_DELAY_MS);

    autoStartTimer = timer;
    unrefTimer(timer);
  }

  /** Returns whether the SDK is currently enabled. */
  static isEnabled(): boolean {
    return enabled;
  }

  /** Returns the current config, or null if not initialized. */
  static getConfig(): FeedbackConfig | null {
    return config;
  }

  /** Current third-party SDK mode. Fails closed unless enabled + account match. */
  static getDogfoodStatus(): SDKDogfoodStatus {
    return resolveSDKDogfood(config?.dogfood);
  }

  /** One-call account-bound Dogfood bootstrap for third-party apps. The host
   * app needs no auth backend of its own: SDK OAuth supplies the full Yaver
   * account, then this creates/proves the installation key. Owner approval
   * binds that account + appId + phone key before a scoped session is minted. */
  static async enableDeviceDogfood(options: DeviceDogfoodOptions): Promise<{
    status: DeviceDogfoodState;
    installationId: string;
    session: DeviceDogfoodSession | null;
  }> {
    const client = new YaverDeviceDogfood({ ...options, authToken: options.authToken || config?.authToken });
    let status = await client.status();
    if (status === 'unregistered' || status === 'cancelled' || status === 'revoked' || status === 'superseded') {
      const enrolled = status === 'unregistered'
        ? await client.enroll(Platform.OS)
        : await client.reRegister(Platform.OS);
      status = enrolled.status;
    }
    const info = await client.enrollmentInfo();
    const session = status === 'active' ? await client.session() : null;
    if (!config) YaverFeedback.init({ autoLogin: false } as FeedbackConfig);
    if (config) {
      config.dogfood = {
        ...config.dogfood,
        enabled: status === 'active' && !!session,
        appId: options.appId,
        installationId: info.installationId,
        installationStatus: status === 'active' && session ? 'active' : status === 'unregistered' ? 'pending' : status,
        label: options.label,
      };
      // A normal full Yaver OAuth session remains authoritative for the
      // runtime wizard. The narrow installation token is intentionally never
      // promoted into account auth.
    }
    if (session) await setDogfoodModeActive(true, options.appId);
    try {
      const { DeviceEventEmitter } = require('react-native');
      DeviceEventEmitter.emit('yaverFeedback:dogfoodChanged', { active: !!session, status });
    } catch { /* noop */ }
    await YaverFeedback.syncDogfoodAppShortcut().catch(() => false);
    await YaverFeedback.syncDogfoodControlGesture().catch(() => undefined);
    return { status, installationId: info.installationId, session };
  }

  /** Confirmed by the Y badge/UI before this is called. Reverts this SDK to
   * normal Feedback mode for the remainder of the app run. */
  static async exitDogfoodMode(): Promise<void> {
    const cfg = config?.dogfood;
    if (!cfg) return;

    // Resolve the restoration route before hiding the only controls that can
    // retry it. An older native plugin may know how to delete a bundle but not
    // recreate the release bridge; failing after disabling Y strands the user
    // in the hot app with no visible recovery action.
    let restoreInstalledApp: (() => Promise<void>) | null = null;
    const containerLoader = (NativeModules as any)?.YaverBundleLoader;
    if (typeof containerLoader?.unloadBundle === 'function') {
      const loaded = typeof containerLoader.isLoaded === 'function'
        ? await containerLoader.isLoaded().catch(() => ({ loaded: true }))
        : { loaded: true };
      if (loaded === true || loaded?.loaded === true) {
        restoreInstalledApp = async () => { await containerLoader.unloadBundle(); };
      }
    } else {
      const hotLoader = (NativeModules as any)?.YaverHotReload;
      const hasHotBundle = typeof hotLoader?.hasBundle === 'function'
        ? await hotLoader.hasBundle().catch(() => true)
        : typeof hotLoader?.clearBundleAndReload === 'function';
      if (hasHotBundle) {
        if (typeof hotLoader?.clearBundleAndReload === 'function') {
          restoreInstalledApp = async () => { await hotLoader.clearBundleAndReload(); };
        } else if (typeof hotLoader?.clearBundle === 'function') {
          const { DevSettings } = require('react-native');
          if (typeof DevSettings?.reload !== 'function') {
            throw new Error('Exit Dogfood requires this app to be rebuilt with the current Yaver config plugin before it can return without a cold launch.');
          }
          restoreInstalledApp = async () => {
            await hotLoader.clearBundle();
            DevSettings.reload();
          };
        }
      }
    }

    await setDogfoodModeActive(false, cfg.appId || config?.bundleId);
    cfg.enabled = false;
    await cfg.onExit?.();
    try {
      const { DeviceEventEmitter } = require('react-native');
      DeviceEventEmitter.emit('yaverFeedback:dogfoodChanged', { active: false, exited: true });
    } catch { /* noop */ }
    await YaverFeedback.syncDogfoodControlGesture().catch(() => undefined);
    await restoreInstalledApp?.();
  }

  /** Returns the resolved relay password the SDK is currently using.
   *  Empty string when no relay routing is in play (direct LAN agent
   *  URLs need no password). Callers attaching it to relay-routed
   *  HTTP requests must check for empty before setting the header,
   *  since "X-Relay-Password: " is treated as invalid by the relay
   *  and would 401 the request. */
  static getRelayPassword(): string {
    return p2pRelayPassword;
  }

  /**
   * Manually attach an error with optional metadata.
   * Use this in catch blocks to give the agent extra context.
   */
  static attachError(error: Error, metadata?: Record<string, unknown>): void {
    if (!enabled) return; // No-op when disabled
    const captured: CapturedError = {
      message: error.message,
      stack: (error.stack ?? '').split('\n').filter((l: string) => l.trim()),
      isFatal: false,
      timestamp: Date.now(),
      metadata,
    };
    errorBuffer.push(captured);
    if (errorBuffer.length > maxErrors) {
      errorBuffer.shift();
    }
  }

  /**
   * Returns the current captured errors buffer.
   * Called internally when building a FeedbackBundle.
   */
  static getCapturedErrors(): CapturedError[] {
    return [...errorBuffer];
  }

  /** Clears the captured errors buffer. */
  static clearCapturedErrors(): void {
    errorBuffer = [];
  }

  /**
   * Returns a pass-through error handler that records the error in Yaver's
   * buffer and then calls `next`. Use this to insert Yaver into your
   * existing error handler chain without replacing anything.
   *
   * @example
   * // Works alongside Sentry, Crashlytics, or any other tool:
   * const originalHandler = ErrorUtils.getGlobalHandler();
   * ErrorUtils.setGlobalHandler(
   *   YaverFeedback.wrapErrorHandler(originalHandler)
   * );
   * // Sentry can still be initialized after this — it will wrap our
   * // wrapper, and the chain stays intact.
   */
  static wrapErrorHandler(
    next?: ((error: Error, isFatal?: boolean) => void) | null,
  ): (error: Error, isFatal?: boolean) => void {
    return (error: Error, isFatal?: boolean) => {
      YaverFeedback.attachError(error);
      if (errorBuffer.length > 0) {
        errorBuffer[errorBuffer.length - 1].isFatal = isFatal ?? false;
      }
      next?.(error, isFatal);
    };
  }

  /**
   * Upload a crash-aware feedback bundle. Apps can call this from their own
   * error boundary/global handler. If crashReporting.autoFix is true, the SDK
   * also triggers the agent's feedback-fix task; reload delivery still happens
   * through the existing BlackBox command stream.
   */
  static async reportCrash(
    error: Error,
    opts?: {
      isFatal?: boolean;
      source?: 'manual' | 'global-handler';
      metadata?: Record<string, unknown>;
      autoFix?: boolean;
    },
  ): Promise<{ reportId?: string; taskId?: string }> {
    if (!config || !enabled || !config.crashReporting?.enabled) return {};
    if (crashReportInFlight) return {};
    crashReportInFlight = true;
    try {
      YaverFeedback.attachError(error, {
        ...(config.crashReporting.metadata || {}),
        ...(opts?.metadata || {}),
        crashAware: true,
      });
      if (errorBuffer.length > 0) {
        errorBuffer[errorBuffer.length - 1].isFatal = opts?.isFatal ?? true;
      }

      if (!config.agentUrl) {
        await YaverFeedback.discoverAgent();
      }
      if (!config.agentUrl) {
        console.warn('[YaverFeedback] No agent URL — cannot send crash report.');
        return {};
      }

      const { Platform, Dimensions } = require('react-native');
      const { uploadFeedback } = require('./upload');
      const { width, height } = Dimensions.get('window');
      let screenshotPath: string | undefined;
      if (config.crashReporting.captureScreenshot !== false) {
        try {
          const { captureScreenshot } = require('./capture');
          screenshotPath = await captureScreenshot();
        } catch {}
      }

      const autoFix = opts?.autoFix ?? config.crashReporting.autoFix === true;
      const identity = resolveReportIdentity({
        projectName: config?.projectName,
        bundleId: config?.bundleId,
        surface: config?.surface,
        surfaces: config?.surfaces,
        stack: config?.stack,
        stacks: config?.stacks,
        testSurfaces: config?.testSurfaces,
        feedbackSdk: config?.feedbackSdk,
        feedbackTransport: config?.feedbackTransport,
        voiceCapabilities: config?.voiceCapabilities,
        sttProvider: config?.sttProvider,
        ttsProvider: config?.ttsProvider,
        appVersion: config?.appVersion,
        buildNumber: config?.buildNumber,
        runtimeMode: IS_HOST_MODE
          ? 'yaver-hosted-dogfood'
          : (YaverFeedback.getDogfoodStatus().active ? 'dogfood' : 'native'),
      });
      const bundle = {
        metadata: {
          timestamp: new Date().toISOString(),
          reportKind: 'crash',
          deviceInfo: {
            platform: Platform.OS,
            osVersion: String(Platform.Version),
            model: Platform.OS === 'ios' ? 'iOS Device' : 'Android Device',
            screenWidth: width,
            screenHeight: height,
            appName: identity.appName,
          },
          app: identity.app,
          project: identity.project,
          userNote: '[Crash report]',
          crash: {
            message: error.message,
            isFatal: opts?.isFatal ?? true,
            source: opts?.source ?? 'manual',
            autoFixRequested: autoFix,
          },
        },
        screenshots: screenshotPath ? [screenshotPath] : [],
        errors: errorBuffer.length > 0 ? [...errorBuffer] : undefined,
      };

      const uploaded = await uploadFeedback(config.agentUrl, config.authToken ?? '', bundle, p2pRelayPassword);
      const reportId =
        (uploaded as { id?: string; reportId?: string } | null | undefined)?.id ??
        (uploaded as { reportId?: string } | null | undefined)?.reportId;
      let taskId: string | undefined;
      if (autoFix && reportId) {
        const client = p2pClient ?? new P2PClient(config.agentUrl, config.authToken ?? '', p2pRelayPassword);
        const fix = await client.triggerFix(reportId);
        taskId = fix?.taskId;
      }
      return { reportId, taskId };
    } catch (err) {
      console.warn('[YaverFeedback] Crash report failed:', err);
      return {};
    } finally {
      crashReportInFlight = false;
    }
  }

  /**
   * Returns the P2P client instance.
   * Available after init if agentUrl is set, or after first successful discovery.
   */
  static getP2PClient(): P2PClient | null {
    return p2pClient;
  }

  /**
   * Export this app's verified checkout as an installable browser shortcut.
   * Yaver Mobile calls the same BrowserShortcutController directly through its
   * selected-device adapter; third-party apps such as SFMG get this one-call
   * facade after YaverFeedback.init(). The SDK token must explicitly include
   * `browser-shortcut` and be pinned to this checkout's project slug; ordinary
   * feedback tokens intentionally cannot compile or publish source.
   */
  static async exportBrowserShortcut(
    request: BrowserShortcutRequest,
    onSnapshot?: (snapshot: BrowserShortcutSnapshot) => void,
  ): Promise<BrowserShortcutSnapshot> {
    await YaverFeedback.hydrateSession();
    let client = YaverFeedback.getRenderP2PClient();
    if (!client && await YaverFeedback.reconnect()) client = YaverFeedback.getRenderP2PClient();
    if (!client) {
      const blocked: BrowserShortcutSnapshot = {
        phase: 'blocked', progress: 0, code: 'BROWSER_SHORTCUT_BOX_OFFLINE',
        message: 'The selected Yaver machine is not connected.',
        remedy: 'Reconnect or choose a reachable machine before exporting.',
      };
      onSnapshot?.(blocked);
      return blocked;
    }
    return new BrowserShortcutController().run(client.browserShortcutDriver(), request, onSnapshot);
  }

  /** Pending native-shortcut pairing codes for this exported app. */
  static async listBrowserShortcutEnrollments(appId: string): Promise<Array<{ id: string; code: string; createdAt: string }>> {
    await YaverFeedback.hydrateSession();
    let client = YaverFeedback.getRenderP2PClient();
    if (!client && await YaverFeedback.reconnect()) client = YaverFeedback.getRenderP2PClient();
    return client ? client.listBrowserShortcutEnrollments(appId) : [];
  }

  /** Approve one displayed pairing code using the app's project-scoped SDK token. */
  static async approveBrowserShortcutEnrollment(appId: string, code: string): Promise<{ ok: boolean; error?: string }> {
    await YaverFeedback.hydrateSession();
    let client = YaverFeedback.getRenderP2PClient();
    if (!client && await YaverFeedback.reconnect()) client = YaverFeedback.getRenderP2PClient();
    if (!client) return { ok: false, error: 'The selected Yaver machine is not connected.' };
    return client.approveBrowserShortcutEnrollment(appId, code);
  }

  /** Renderer/reload route; intentionally distinct from the coding client. */
  static getRenderP2PClient(): P2PClient | null {
    return renderP2PClient ?? p2pClient;
  }

  static getMachineRouting(): { codingDeviceId?: string; renderDeviceId?: string } {
    return {
      codingDeviceId: config?.codingDeviceId || config?.preferredDeviceId,
      renderDeviceId: config?.renderDeviceId || config?.codingDeviceId || config?.preferredDeviceId,
    };
  }

  // ─── One-stop SaaS replacement methods ─────────────────────────
  //
  // These are the three solo-dev SaaS-replacement entry points
  // wired into YaverFeedback so there's exactly one import path
  // for the dev's app code: track / getFlag / checkUpdate.

  /**
   * Record a business event. Routes through BlackBox so the agent
   * persists it to the analytics ledger (no dashboards — export
   * via CSV or webhook into PostHog).
   *
   * @example
   * ```ts
   * YaverFeedback.track('purchase_completed', { amount: '9.99' });
   * ```
   */
  static track(name: string, props?: Record<string, unknown>, route?: string): void {
    if (!enabled) return;
    BlackBox.track(name, props, route);
  }

  /**
   * Evaluate a single feature flag for a user. Results are cached
   * for 30 seconds inside YaverFeedback so a tight loop evaluating
   * the same key doesn't hammer the agent.
   *
   * @param key — flag key (must exist on the agent)
   * @param defaultValue — returned if the flag is missing / offline
   * @param userId — stable user identifier for rollout bucketing
   */
  static async getFlag<T = boolean | string>(
    key: string,
    defaultValue: T,
    userId: string = 'anonymous',
  ): Promise<T> {
    if (!enabled || !p2pClient) return defaultValue;
    const cacheKey = `${userId}|${key}`;
    const now = Date.now();
    const cached = flagCache.get(cacheKey);
    if (cached && now - cached.at < 30_000) {
      return (cached.value as T) ?? defaultValue;
    }
    try {
      const val = await p2pClient.flagsEvaluateOne<T>(key, userId);
      flagCache.set(cacheKey, { value: val ?? defaultValue, at: now });
      return (val as T) ?? defaultValue;
    } catch {
      return defaultValue;
    }
  }

  /**
   * Bulk evaluate every flag for a user. Cached on the same 30s
   * window as getFlag — use this when boot needs a handful of
   * flags in one roundtrip.
   */
  static async getFlags(
    userId: string = 'anonymous',
  ): Promise<Record<string, unknown>> {
    if (!enabled || !p2pClient) return {};
    const cacheKey = `all|${userId}`;
    const now = Date.now();
    const cached = flagCache.get(cacheKey);
    if (cached && now - cached.at < 30_000) {
      return (cached.value as Record<string, unknown>) ?? {};
    }
    try {
      const flags = await p2pClient.flagsEvaluate(userId);
      flagCache.set(cacheKey, { value: flags, at: now });
      return flags;
    } catch {
      return {};
    }
  }

  /**
   * Ask what bundle this device should run. Returns the latest
   * release manifest in the configured channel plus a rollout
   * gate. The dev can then compare against what's currently
   * running and prompt the user to reload.
   *
   * On-disk bundle swap is platform-specific — see
   * `YaverFeedback.onUpdateAvailable` if you want a hook.
   */
  static async checkUpdate(
    channel: string = 'production',
    deviceId?: string,
  ): Promise<Awaited<ReturnType<P2PClient['releasesLatest']>>> {
    if (!enabled || !p2pClient) return null;
    return p2pClient.releasesLatest(channel, deviceId);
  }

  /** Clear the in-memory flag cache. Useful for tests or after sign-out. */
  static clearFlagCache(): void {
    flagCache.clear();
  }

  /**
   * Reporting-only mode: auto-capture screenshot + errors and send
   * to the agent's /feedback endpoint. No modal UI — just shake and go.
   *
   * This is triggered by shake when `reportingOnly: true` is set.
   * The agent receives the report via the same P2P channel and logs it.
   */
  static async sendAutoReport(): Promise<void> {
    if (!config || !enabled) return;

    // Resolve agent URL if needed
    if (!config.agentUrl) {
      try {
        const result = await YaverDiscovery.discover({
          convexUrl: config.convexUrl,
          authToken: config.authToken,
          preferredDeviceId: config.preferredDeviceId,
        });
        if (result) {
          config.agentUrl = result.url;
          const rp = await resolveRelayPassword(config.authToken ?? '');
          p2pClient = new P2PClient(result.url, config.authToken ?? '', rp);
          renderP2PClient = p2pClient;
        }
      } catch {}
    }

    if (!config.agentUrl) {
      console.warn('[YaverFeedback] No agent URL — cannot send auto report.');
      return;
    }

    try {
      const { Platform, Dimensions } = require('react-native');
      const { captureScreenshot } = require('./capture');
      const { uploadFeedback } = require('./upload');
      const { width, height } = Dimensions.get('window');

      // Auto-capture screenshot
      let screenshotPath: string | undefined;
      try {
        screenshotPath = await captureScreenshot();
      } catch {
        // Screenshot capture may fail (e.g. no view ref) — continue without it
      }

      const identity = resolveReportIdentity({
        projectName: config?.projectName,
        bundleId: config?.bundleId,
        surface: config?.surface,
        surfaces: config?.surfaces,
        stack: config?.stack,
        stacks: config?.stacks,
        testSurfaces: config?.testSurfaces,
        feedbackSdk: config?.feedbackSdk,
        feedbackTransport: config?.feedbackTransport,
        voiceCapabilities: config?.voiceCapabilities,
        sttProvider: config?.sttProvider,
        ttsProvider: config?.ttsProvider,
        appVersion: config?.appVersion,
        buildNumber: config?.buildNumber,
        runtimeMode: IS_HOST_MODE
          ? 'yaver-hosted-dogfood'
          : (YaverFeedback.getDogfoodStatus().active ? 'dogfood' : 'native'),
      });
      const bundle = {
        metadata: {
          timestamp: new Date().toISOString(),
          deviceInfo: {
            platform: Platform.OS,
            osVersion: String(Platform.Version),
            model: Platform.OS === 'ios' ? 'iOS Device' : 'Android Device',
            screenWidth: width,
            screenHeight: height,
            appName: identity.appName,
          },
          app: identity.app,
          project: identity.project,
          userNote: '[Auto-report via shake]',
        },
        screenshots: screenshotPath ? [screenshotPath] : [],
        errors: errorBuffer.length > 0 ? [...errorBuffer] : undefined,
      };

      await uploadFeedback(config.agentUrl, config.authToken ?? '', bundle, p2pRelayPassword);
      console.log('[YaverFeedback] Auto-report sent');
    } catch (err) {
      console.warn('[YaverFeedback] Auto-report failed:', err);
    }
  }

  /**
   * Default reload handler. Tries three strategies in order:
   *
   * 1. **YaverBundleLoader** — running inside Yaver's native container.
   *    Pulls fresh Hermes bundle from agent and reloads the RN bridge.
   *
   * 2. **YaverHotReload** — standalone app with feedback SDK's native module
   *    (added via Expo config plugin). Downloads Hermes bundle from agent,
   *    saves to Documents, and reloads the RN bridge.
   *
   * 3. **DevSettings.reload()** — standalone dev build connected to Metro.
   */
  private static defaultReload(): void {
    if (!config?.agentUrl) return;
    const bundleUrl = `${config.agentUrl}/dev/native-bundle`;
    const headers = { Authorization: `Bearer ${config.authToken ?? ''}` };
    YaverFeedback.loadBundleAndReload(bundleUrl, headers);
  }

  /**
   * Default reload_bundle handler. Receives a compiled Hermes bundle URL
   * from the agent and loads it via the best available native mechanism.
   */
  private static defaultReloadBundle(bundleUrl: string, _assetsUrl?: string): void {
    if (!config?.agentUrl) return;

    const fullUrl = bundleUrl.startsWith('http')
      ? bundleUrl
      : `${config.agentUrl}${bundleUrl}`;
    const headers = { Authorization: `Bearer ${config.authToken ?? ''}` };
    YaverFeedback.loadBundleAndReload(fullUrl, headers);
  }

  /**
   * Core bundle reload logic. Tries native loaders in order:
   *
   * 1. YaverBundleLoader (Yaver container — full validation + bridge reload)
   * 2. YaverHotReload (SDK's own native module — download + bridge reload)
   * 3. DevSettings.reload() (Metro dev server fallback)
   */
  private static loadBundleAndReload(
    bundleUrl: string,
    headers: Record<string, string>,
  ): void {
    const { NativeModules } = require('react-native');

    // Strategy 1: YaverBundleLoader (running inside Yaver container)
    if (NativeModules.YaverBundleLoader) {
      NativeModules.YaverBundleLoader.loadBundle(bundleUrl, 'main', headers)
        .catch((err: Error) => {
          console.warn('[YaverFeedback] YaverBundleLoader reload failed:', err);
        });
      return;
    }

    // Strategy 2: YaverHotReload (SDK's native module, added by Expo config plugin)
    if (NativeModules.YaverHotReload) {
      NativeModules.YaverHotReload.loadBundle(bundleUrl, headers)
        .catch((err: Error) => {
          console.warn('[YaverFeedback] YaverHotReload reload failed:', err);
        });
      return;
    }

    // Strategy 3: DevSettings.reload() for Metro dev builds
    console.warn(
      '[YaverFeedback] No native bundle loader available. ' +
      'Add "yaver-feedback-react-native" to your app.json plugins to enable hot reload.',
    );
    try {
      const { DevSettings } = require('react-native');
      if (typeof DevSettings?.reload === 'function') {
        DevSettings.reload();
      }
    } catch {
      // Not in dev mode
    }
  }

  /**
   * Internal: fired from every shake path (dev-menu + accelerometer)
   * before the feedback modal opens. Emits `yaverFeedback:firstShake`
   * exactly once per process so QuickActionIcon's `'after-shake'` mode
   * can surface itself on first shake and stay visible for the rest of
   * the session.
   */
  static notifyShake(): void {
    if (firstShakeFired) return;
    firstShakeFired = true;
    try {
      const { DeviceEventEmitter } = require('react-native');
      DeviceEventEmitter.emit('yaverFeedback:firstShake');
    } catch {
      // emitter unavailable (e.g. jsdom unit test) — safe to ignore
    }
  }

  /**
   * Show / hide the QuickActionIcon programmatically and persist the
   * choice across launches. Host apps can call this from a settings
   * screen so the user has a second way to re-enable the icon after
   * hiding it via the icon's own long-press menu — shake is always the
   * third back-door because it never depends on a visible control.
   */
  static async setQuickIconVisible(visible: boolean): Promise<void> {
    await setQuickIconDisabled(!visible);
    try {
      const { DeviceEventEmitter } = require('react-native');
      DeviceEventEmitter.emit(
        visible ? 'yaverFeedback:quickIconShow' : 'yaverFeedback:quickIconHide',
      );
    } catch {
      // emitter unavailable — preference is still persisted
    }
  }

  /**
   * Returns `true` when the user has chosen to hide the QuickActionIcon
   * (via its long-press menu or `setQuickIconVisible(false)`).
   * FeedbackModal uses this to surface a one-tap "Show quick icon"
   * control so the user can bring the icon back without having to know
   * about the programmatic API.
   */
  static async isQuickIconHidden(): Promise<boolean> {
    return getQuickIconDisabled();
  }

  /** Dogfood's sole over-app chrome is visible by default and can be managed
   * from the native Dogfood Settings surface. */
  static async setDogfoodEntryIconVisible(visible: boolean): Promise<void> {
    const scope = await YaverFeedback.dogfoodPreferenceScope().catch(() => 'legacy');
    await setDogfoodEntryIconHidden(!visible, scope);
    DeviceEventEmitter.emit('yaverFeedback:dogfoodEntryIconChanged', { visible });
  }

  static async isDogfoodEntryIconHidden(): Promise<boolean> {
    const scope = await YaverFeedback.dogfoodPreferenceScope().catch(() => 'legacy');
    return getDogfoodEntryIconHidden(scope);
  }

  static async setQuickIconColorPreset(
    preset: QuickIconColorPreset | null,
  ): Promise<void> {
    await setQuickIconColorPreset(preset);
    try {
      const { DeviceEventEmitter } = require('react-native');
      DeviceEventEmitter.emit('yaverFeedback:quickIconColorChange', { preset });
    } catch {
      // emitter unavailable — preference is still persisted
    }
  }

  static async getQuickIconColorPreset(): Promise<QuickIconColorPreset | null> {
    return getQuickIconColorPreset();
  }

  /** Clear the persisted "user hid the icon" flag. */
  static async resetQuickIconPreference(): Promise<void> {
    await YaverFeedback.setQuickIconVisible(true);
  }

  /** Tear down the SDK (stop shake detector, clear state). */
  static destroy(): void {
    if (shakeDetector) {
      shakeDetector.stop();
      shakeDetector = null;
    }
    // Drop the agent command handler and un-monkey-patch console. Without
    // these, destroy() left the SDK half-alive: console stayed wrapped, and
    // the reload handler kept firing on a "destroyed" SDK — then a later
    // init() stacked a second one on top.
    commandUnsubscribe?.();
    commandUnsubscribe = null;
    dogfoodShortcutAppStateSubscription?.remove();
    dogfoodShortcutAppStateSubscription = null;
    dogfoodActivationSubscription?.remove();
    dogfoodActivationSubscription = null;
    try {
      const native = (NativeModules as any)?.YaverDogfoodGesture;
      if (typeof native?.setEnabled === 'function') {
        void native.setEnabled(false, 900).catch(() => undefined);
      }
    } catch { /* native trigger teardown is best-effort */ }
    lastDogfoodActivationUrl = '';
    // Before `enabled = false` / `config = null` below, so an in-flight retry
    // can't fire against a torn-down config.
    YaverFeedback.cancelBlackBoxAutoStart();
    BlackBox.stop();
    BlackBox.unwrapConsole();
    firstShakeFired = false;
    enabled = false;
    config = null;
    p2pClient = null;
    renderP2PClient = null;
    errorBuffer = [];
  }
}
