import { DeviceEventEmitter, NativeModules } from 'react-native';
import { YaverFeedback } from '../YaverFeedback';
import { getDogfoodAccountAccess, setDogfoodControlPreference } from '../auth';
import { YaverDeviceDogfood } from '../deviceDogfood';

// Mock react-native: DeviceEventEmitter for event dispatch + Platform so
// ShakeDetector.start() can branch on iOS without hitting a real RN runtime.
jest.mock('react-native', () => ({
  DeviceEventEmitter: {
    emit: jest.fn(),
    addListener: jest.fn(() => ({ remove: jest.fn() })),
  },
  Platform: { OS: 'ios' },
  NativeModules: {
    YaverHotReload: {
      setDogfoodShortcut: jest.fn(async () => true),
      consumeDogfoodShortcut: jest.fn(async () => false),
      clearBundleAndReload: jest.fn(async () => true),
    },
    YaverDogfoodGesture: {
      getCapability: jest.fn(async () => ({ supported: true, enabled: false, reason: 'supported', platform: 'ios' })),
      setEnabled: jest.fn(async (next: boolean) => ({ supported: true, enabled: next, reason: 'supported', platform: 'ios' })),
    },
  },
  AppState: { addEventListener: jest.fn(() => ({ remove: jest.fn() })) },
}));

// Mock Discovery
jest.mock('../Discovery', () => ({
  YaverDiscovery: {
    discover: jest.fn(),
  },
}));

jest.mock('../auth', () => ({
  configureAuthEndpoints: jest.fn(),
  setStrictNativeAuth: jest.fn(),
  getToken: jest.fn(async () => null),
  getSelectedDeviceId: jest.fn(async () => null),
  getDogfoodAccountAccess: jest.fn(async (_appId: string, token: string) => ({
    authenticated: token === 'owner-token',
    ownerAuthorized: token === 'owner-token',
    accountAuthorized: token === 'owner-token',
    installationAuthorized: token === 'owner-token',
  })),
  setDogfoodControlPreference: jest.fn(async () => true),
  saveSelectedDeviceId: jest.fn(async () => {}),
  clearToken: jest.fn(async () => {}),
  clearSelectedDeviceId: jest.fn(async () => {}),
  listReachableDevices: jest.fn(async () => ({
    owned: [
      {
        deviceId: 'device-1',
        name: 'Dev Mac',
        platform: 'darwin',
        isOnline: true,
        needsAuth: false,
        runnerDown: false,
        lastHeartbeat: Date.now(),
        quicHost: '127.0.0.1',
        quicPort: 18080,
      },
    ],
  })),
  DEFAULT_CONVEX_SITE_URL: 'https://example.convex.site',
}));

// Reset module-level state between tests by re-requiring
beforeEach(() => {
  // YaverFeedback uses module-level variables (config, enabled, p2pClient).
  // We reset them by calling init with a known state or relying on isInitialized checks.
  // For a clean slate, we re-init with enabled=false then verify.
  jest.restoreAllMocks();
  jest.clearAllMocks();
});

function activeDogfood(controlGesture: true | { durationMs: number } = true) {
  return {
    enabled: true,
    appId: 'io.example.app',
    installationId: 'phone-1',
    installationStatus: 'active' as const,
    controlGesture,
  };
}

function initActiveDogfood(options: { authToken?: string; controlGesture?: true | { durationMs: number } } = {}) {
  const controlGesture = options.controlGesture ?? true;
  YaverFeedback.init({
    authToken: options.authToken,
    bundleId: 'io.example.app',
    dogfood: { controlGesture },
  });
  Object.assign(YaverFeedback.getConfig()!.dogfood!, activeDogfood(controlGesture));
  jest.clearAllMocks();
}

describe('YaverFeedback', () => {
  it('coalesces concurrent Dogfood activation so one-time session challenges cannot overwrite each other', async () => {
    YaverFeedback.init({ enabled: true, authToken: 'owner-token', bundleId: 'io.example.concurrent' });
    jest.spyOn(YaverDeviceDogfood.prototype, 'status').mockResolvedValue('active');
    jest.spyOn(YaverDeviceDogfood.prototype, 'enrollmentInfo').mockResolvedValue({
      appId: 'io.example.concurrent',
      installationId: 'installation-concurrent',
      registrationSlot: 'slot-concurrent-value',
      publicKey: 'public-key',
    });
    let release!: (value: any) => void;
    const session = jest.spyOn(YaverDeviceDogfood.prototype, 'session').mockImplementation(() => new Promise((resolve) => { release = resolve; }));
    jest.spyOn(YaverFeedback, 'syncDogfoodAppShortcut').mockResolvedValue(false);
    jest.spyOn(YaverFeedback, 'syncDogfoodControlGesture').mockResolvedValue({} as any);
    const secureStore = {
      getItemAsync: jest.fn(async () => null),
      setItemAsync: jest.fn(async () => undefined),
      deleteItemAsync: jest.fn(async () => undefined),
    };

    const first = YaverFeedback.enableDeviceDogfood({ appId: 'io.example.concurrent', secureStore });
    const second = YaverFeedback.enableDeviceDogfood({ appId: 'io.example.concurrent', secureStore });
    await new Promise((resolve) => setImmediate(resolve));
    expect(session).toHaveBeenCalledTimes(1);
    release({
      active: true,
      appId: 'io.example.concurrent',
      installationId: 'installation-concurrent',
      token: 'scoped-token',
      expiresAt: Date.now() + 1000,
      scopes: [],
    });
    await expect(Promise.all([first, second])).resolves.toHaveLength(2);
    expect(session).toHaveBeenCalledTimes(1);
  });

  describe('Dogfood onboarding', () => {
    it('routes compact Usage through onboarding when the cached browser runtime is no longer serving', async () => {
      YaverFeedback.init({
        enabled: true,
        authToken: 'owner-token',
        bundleId: 'io.example.app',
        preferredDeviceId: 'device-1',
        dogfood: { framework: 'expo' },
      });
      Object.assign(YaverFeedback.getConfig()!.dogfood!, activeDogfood());
      jest.spyOn(YaverFeedback, 'getDogfoodAccess').mockResolvedValueOnce({
        appId: 'io.example.app',
        yaverAuthenticated: true,
        ownerAuthorized: true,
        accountAuthorized: true,
        installationId: 'phone-1',
        deviceState: 'active',
        authorized: true,
      });
      jest.spyOn(YaverFeedback, 'getDogfoodRuntimeSelection').mockResolvedValueOnce({
        lane: 'browser',
        projectName: 'Example',
        projectPath: '/workspace/example',
      });
      jest.spyOn(YaverFeedback, 'getP2PClient').mockReturnValueOnce({
        getDogfoodDevServerStatus: jest.fn(async () => ({
          running: false,
          serving: false,
          workDir: '/workspace/example',
        })),
      } as any);
      const open = jest.spyOn(YaverFeedback, 'openDogfood').mockResolvedValueOnce({
        phase: 'opening',
        appId: 'io.example.app',
      });

      await expect(YaverFeedback.openDogfoodUsage()).resolves.toMatchObject({ phase: 'opening' });
      expect(open).toHaveBeenCalledTimes(1);
      expect(DeviceEventEmitter.emit).not.toHaveBeenCalledWith('yaverFeedback:dogfoodUsageRequested');
    });

    it('opens compact Usage only when the selected browser checkout is actually serving', async () => {
      YaverFeedback.init({
        enabled: true,
        authToken: 'owner-token',
        bundleId: 'io.example.app',
        preferredDeviceId: 'device-1',
        dogfood: { framework: 'expo' },
      });
      Object.assign(YaverFeedback.getConfig()!.dogfood!, activeDogfood());
      jest.spyOn(YaverFeedback, 'getDogfoodAccess').mockResolvedValueOnce({
        appId: 'io.example.app',
        yaverAuthenticated: true,
        ownerAuthorized: true,
        accountAuthorized: true,
        installationId: 'phone-1',
        deviceState: 'active',
        authorized: true,
      });
      jest.spyOn(YaverFeedback, 'getDogfoodRuntimeSelection').mockResolvedValueOnce({
        lane: 'browser',
        projectName: 'Example',
        projectPath: '/workspace/example',
      });
      jest.spyOn(YaverFeedback, 'getP2PClient').mockReturnValueOnce({
        getDogfoodDevServerStatus: jest.fn(async () => ({
          running: true,
          serving: true,
          workDir: '/workspace/example',
        })),
      } as any);

      await expect(YaverFeedback.openDogfoodUsage()).resolves.toMatchObject({ phase: 'opening' });
      expect(DeviceEventEmitter.emit).toHaveBeenCalledWith('yaverFeedback:dogfoodUsageRequested');
    });

    it('starts with Yaver OAuth when the host has no session', async () => {
      YaverFeedback.init({ enabled: true });
      await YaverFeedback.beginDogfoodOnboarding({ appId: 'io.example.app', label: 'Example' });
      expect(DeviceEventEmitter.emit).toHaveBeenCalledWith('yaverFeedback:startLogin');
    });

    it('automatically uses the sole reachable machine after an existing OAuth session', async () => {
      YaverFeedback.init({ enabled: true, authToken: 'owner-token' });
      await YaverFeedback.beginDogfoodOnboarding({ appId: 'io.example.app', label: 'Example' });
      expect(YaverFeedback.getConfig()?.preferredDeviceId).toBe('device-1');
      expect(DeviceEventEmitter.emit).toHaveBeenCalledWith('yaverFeedback:startReport');
      expect(DeviceEventEmitter.emit).not.toHaveBeenCalledWith('yaverFeedback:startMachinePicker');
    });

    it('reuses the configured app identity and selected machine', async () => {
      YaverFeedback.init({
        enabled: true,
        authToken: 'owner-token',
        preferredDeviceId: 'device-1',
        projectName: 'Example',
        bundleId: 'io.example.app',
        dogfood: { framework: 'expo' },
      });
      const state = await YaverFeedback.openDogfood();
      expect(state).toEqual({ phase: 'opening', appId: 'io.example.app' });
      expect(DeviceEventEmitter.emit).toHaveBeenCalledWith('yaverFeedback:startReport');
      expect(DeviceEventEmitter.emit).not.toHaveBeenCalledWith('yaverFeedback:startMachinePicker');
    });

    it('publishes flow state for custom host UI', async () => {
      YaverFeedback.init({ enabled: true });
      const states: string[] = [];
      const unsubscribe = YaverFeedback.onDogfoodFlowState((state) => states.push(state.phase));
      await YaverFeedback.beginDogfoodOnboarding({ appId: 'io.example.app' });
      unsubscribe();
      expect(states[states.length - 1]).toBe('auth-required');
    });

    it('returns a visible structured error when access verification fails', async () => {
      YaverFeedback.init({
        enabled: true,
        authToken: 'owner-token',
        bundleId: 'io.example.app',
        dogfood: {},
      });
      (getDogfoodAccountAccess as jest.Mock).mockRejectedValueOnce(new Error('Access service unavailable'));
      const state = await YaverFeedback.openDogfood();
      expect(state).toEqual({
        phase: 'error',
        appId: 'io.example.app',
        error: 'Access service unavailable',
      });
      expect(DeviceEventEmitter.emit).toHaveBeenCalledWith('yaverFeedback:startReport');
    });

    it('lets a host ACL hide its affordance without opening auth UI', async () => {
      YaverFeedback.init({
        enabled: true,
        bundleId: 'io.example.app',
        dogfood: { canShow: () => false },
      });
      const state = await YaverFeedback.openDogfood();
      expect(state).toEqual({ phase: 'denied', appId: 'io.example.app' });
      expect(DeviceEventEmitter.emit).not.toHaveBeenCalledWith('yaverFeedback:startLogin');
    });

    it('adds the app shortcut only for a signed-in and approved phone', async () => {
      YaverFeedback.init({ bundleId: 'io.example.app', dogfood: {} });
      YaverFeedback.getConfig()!.dogfood!.appShortcut = { label: 'Dogfood Example' };
      jest.spyOn(YaverFeedback, 'getDogfoodAccess').mockResolvedValueOnce({
        appId: 'io.example.app',
        yaverAuthenticated: true,
        ownerAuthorized: false,
        accountAuthorized: true,
        installationId: 'phone-1',
        deviceState: 'active',
        authorized: true,
      });
      await YaverFeedback.syncDogfoodAppShortcut();
      expect((NativeModules as any).YaverHotReload.setDogfoodShortcut)
        .toHaveBeenCalledWith(true, 'Dogfood Example');
    });

    it('removes the shortcut when the phone is approved but Yaver is signed out', async () => {
      YaverFeedback.init({ bundleId: 'io.example.app', dogfood: {} });
      YaverFeedback.getConfig()!.dogfood!.appShortcut = true;
      jest.spyOn(YaverFeedback, 'getDogfoodAccess').mockResolvedValueOnce({
        appId: 'io.example.app',
        yaverAuthenticated: false,
        ownerAuthorized: false,
        accountAuthorized: true,
        installationId: 'phone-1',
        deviceState: 'active',
        authorized: false,
      });
      await YaverFeedback.syncDogfoodAppShortcut();
      expect((NativeModules as any).YaverHotReload.setDogfoodShortcut)
        .toHaveBeenCalledWith(false, 'Dogfood');
    });

    it('applies the host presentation ACL after backend device authorization', async () => {
      YaverFeedback.init({
        bundleId: 'io.example.app',
        dogfood: { appShortcut: true, canShow: async () => false },
      });
      jest.spyOn(YaverFeedback, 'getDogfoodAccess').mockResolvedValueOnce({
        appId: 'io.example.app',
        yaverAuthenticated: true,
        ownerAuthorized: false,
        accountAuthorized: true,
        installationId: 'phone-1',
        deviceState: 'active',
        authorized: true,
      });
      await YaverFeedback.syncDogfoodAppShortcut();
      expect((NativeModules as any).YaverHotReload.setDogfoodShortcut)
        .toHaveBeenCalledWith(false, 'Dogfood');
    });

    it('keeps the Y visible until the authorized phone has completed onboarding', async () => {
      initActiveDogfood();
      jest.spyOn(YaverFeedback, 'getDogfoodAccess').mockResolvedValueOnce({
        appId: 'io.example.app',
        yaverAuthenticated: true,
        ownerAuthorized: false,
        accountAuthorized: true,
        installationId: 'phone-1',
        deviceState: 'active',
        authorized: true,
      });
      const state = await YaverFeedback.syncDogfoodControlGesture();
      expect(state).toMatchObject({
        onboardingSeen: false,
        presentation: 'minimized-y',
        gestureSupported: true,
        gestureEnabled: false,
        fallbackVisible: true,
      });
      expect((NativeModules as any).YaverDogfoodGesture.setEnabled).toHaveBeenCalledWith(false, 900);
    });

    it('uses the invisible three-finger hold only after onboarding selects it', async () => {
      initActiveDogfood();
      jest.spyOn(YaverFeedback, 'getDogfoodAccess').mockResolvedValueOnce({
        appId: 'io.example.app',
        yaverAuthenticated: true,
        ownerAuthorized: false,
        accountAuthorized: true,
        installationId: 'phone-1',
        deviceState: 'active',
        authorized: true,
        controlPresentation: 'auto',
        controlOnboardingSeen: true,
      });
      const state = await YaverFeedback.syncDogfoodControlGesture();
      expect(state).toMatchObject({
        onboardingSeen: true,
        presentation: 'auto',
        gestureSupported: true,
        gestureEnabled: true,
        fallbackVisible: false,
      });
      expect((NativeModules as any).YaverDogfoodGesture.setEnabled).toHaveBeenCalledWith(true, 900);
    });

    it('keeps the Y as the default after onboarding when no hidden presentation was chosen', async () => {
      initActiveDogfood();
      jest.spyOn(YaverFeedback, 'getDogfoodAccess').mockResolvedValueOnce({
        appId: 'io.example.app',
        yaverAuthenticated: true,
        ownerAuthorized: true,
        accountAuthorized: true,
        installationId: 'phone-1',
        deviceState: 'active',
        authorized: true,
        controlOnboardingSeen: true,
      });
      const state = await YaverFeedback.syncDogfoodControlGesture();
      expect(state).toMatchObject({
        onboardingSeen: true,
        presentation: 'minimized-y',
        gestureEnabled: false,
        fallbackVisible: true,
      });
    });

    it('keeps the Y when native capability reports support but enabling the gesture fails', async () => {
      initActiveDogfood();
      jest.spyOn(YaverFeedback, 'getDogfoodAccess').mockResolvedValueOnce({
        appId: 'io.example.app',
        yaverAuthenticated: true,
        ownerAuthorized: false,
        accountAuthorized: true,
        installationId: 'phone-1',
        deviceState: 'active',
        authorized: true,
        controlPresentation: 'auto',
        controlOnboardingSeen: true,
      });
      (NativeModules as any).YaverDogfoodGesture.setEnabled.mockResolvedValueOnce({
        supported: true,
        enabled: false,
        reason: 'supported',
        platform: 'ios',
      });
      const state = await YaverFeedback.syncDogfoodControlGesture();
      expect(state).toMatchObject({
        gestureSupported: true,
        gestureEnabled: false,
        fallbackVisible: true,
        reason: 'gesture-enable-failed',
      });
    });

    it('persists first-run completion for the exact account app installation', async () => {
      initActiveDogfood({ authToken: 'owner-token' });
      jest.spyOn(YaverFeedback, 'getDogfoodAccess').mockResolvedValueOnce({
        appId: 'io.example.app',
        yaverAuthenticated: true,
        ownerAuthorized: true,
        accountAuthorized: true,
        installationId: 'phone-1',
        deviceState: 'active',
        authorized: true,
        controlOnboardingSeen: false,
      });
      const state = await YaverFeedback.setDogfoodControlPresentation('minimized-y');
      expect(state).toMatchObject({ onboardingSeen: true, presentation: 'minimized-y', fallbackVisible: true });
      expect(setDogfoodControlPreference).toHaveBeenCalledWith(expect.objectContaining({
        appId: 'io.example.app',
        installationId: 'phone-1',
        token: 'owner-token',
        presentation: 'minimized-y',
        controlOnboardingSeen: true,
      }));
    });

    it('falls back to the minimized Y when accessibility owns multi-touch', async () => {
      initActiveDogfood({ controlGesture: { durationMs: 1200 } });
      jest.spyOn(YaverFeedback, 'getDogfoodAccess').mockResolvedValueOnce({
        appId: 'io.example.app',
        yaverAuthenticated: true,
        ownerAuthorized: false,
        accountAuthorized: true,
        installationId: 'phone-1',
        deviceState: 'active',
        authorized: true,
      });
      (NativeModules as any).YaverDogfoodGesture.getCapability.mockResolvedValueOnce({
        supported: false,
        enabled: false,
        reason: 'accessibility-touch-exploration',
        platform: 'ios',
      });
      const state = await YaverFeedback.syncDogfoodControlGesture();
      expect(state).toMatchObject({ gestureSupported: false, gestureEnabled: false, fallbackVisible: true });
      expect((NativeModules as any).YaverDogfoodGesture.setEnabled).toHaveBeenCalledWith(false, 1200);
    });

    it('suppresses guest controls inside the Yaver split-view container', async () => {
      initActiveDogfood();
      (NativeModules as any).YaverInfo = { isYaver: true };
      const state = await YaverFeedback.syncDogfoodControlGesture();
      delete (NativeModules as any).YaverInfo;
      expect(state).toMatchObject({ gestureEnabled: false, fallbackVisible: false, reason: 'yaver-host-owns-controls' });
    });

    it('removes the gesture and fallback Y when Dogfood exits', async () => {
      initActiveDogfood();
      await YaverFeedback.exitDogfoodMode();
      const state = await YaverFeedback.syncDogfoodControlGesture();
      expect(state).toMatchObject({
        authorized: false,
        gestureEnabled: false,
        fallbackVisible: false,
        reason: 'dogfood-session-inactive',
      });
      expect((NativeModules as any).YaverDogfoodGesture.setEnabled).toHaveBeenCalledWith(false, 900);
      expect((NativeModules as any).YaverHotReload.clearBundleAndReload).toHaveBeenCalledTimes(1);
    });

    it('keeps Dogfood reachable when an old native plugin cannot restore the installed app', async () => {
      initActiveDogfood();
      const current = (NativeModules as any).YaverHotReload;
      (NativeModules as any).YaverHotReload = {
        hasBundle: jest.fn(async () => true),
        clearBundle: jest.fn(async () => true),
      };
      await expect(YaverFeedback.exitDogfoodMode()).rejects.toThrow(/rebuilt with the current Yaver config plugin/);
      expect(YaverFeedback.getDogfoodStatus().active).toBe(true);
      (NativeModules as any).YaverHotReload = current;
    });
  });

  describe('init()', () => {
    it('sets config correctly with defaults', () => {
      YaverFeedback.init({
        authToken: 'test-token',
        agentUrl: 'http://localhost:18080',
      });

      const cfg = YaverFeedback.getConfig();
      expect(cfg).not.toBeNull();
      expect(cfg!.authToken).toBe('test-token');
      expect(cfg!.agentUrl).toBe('http://localhost:18080');
      expect(cfg!.trigger).toBe('shake');
      expect(cfg!.maxRecordingDuration).toBe(120);
    });

    it('respects user-provided values over defaults', () => {
      YaverFeedback.init({
        authToken: 'tok',
        trigger: 'floating-button',
        maxRecordingDuration: 60,
        strictNativeAuth: true,
      });

      const cfg = YaverFeedback.getConfig();
      expect(cfg!.trigger).toBe('floating-button');
      expect(cfg!.maxRecordingDuration).toBe(60);
      expect(cfg!.strictNativeAuth).toBe(true);
    });

    it('promotes quick icon to always when shake is disabled', () => {
      YaverFeedback.init({
        authToken: 'tok',
        disableShakeGesture: true,
      });

      const cfg = YaverFeedback.getConfig();
      expect(cfg!.disableShakeGesture).toBe(true);
      expect(cfg!.quickIcon).toBe('always');
    });

    it('with enabled=false sets enabled to false', () => {
      YaverFeedback.init({
        authToken: 'tok',
        enabled: false,
      });

      expect(YaverFeedback.isInitialized()).toBe(true);
      expect(YaverFeedback.isEnabled()).toBe(false);
    });

    it('with enabled=true sets enabled to true', () => {
      YaverFeedback.init({
        authToken: 'tok',
        enabled: true,
      });

      expect(YaverFeedback.isEnabled()).toBe(true);
    });

    it('creates P2PClient when agentUrl is provided', () => {
      YaverFeedback.init({
        authToken: 'tok',
        agentUrl: 'http://192.168.1.10:18080',
      });

      expect(YaverFeedback.getP2PClient()).not.toBeNull();
    });

    it('does not create P2PClient when agentUrl is omitted', () => {
      YaverFeedback.init({
        authToken: 'tok',
      });

      expect(YaverFeedback.getP2PClient()).toBeNull();
    });
  });

  describe('isInitialized()', () => {
    it('returns true after init()', () => {
      YaverFeedback.init({ authToken: 'tok' });
      expect(YaverFeedback.isInitialized()).toBe(true);
    });
  });

  describe('setEnabled()', () => {
    it('toggles enabled state', () => {
      YaverFeedback.init({ authToken: 'tok', enabled: true });
      expect(YaverFeedback.isEnabled()).toBe(true);

      YaverFeedback.setEnabled(false);
      expect(YaverFeedback.isEnabled()).toBe(false);

      YaverFeedback.setEnabled(true);
      expect(YaverFeedback.isEnabled()).toBe(true);
    });
  });

  describe('getConfig()', () => {
    it('returns the config after init', () => {
      YaverFeedback.init({
        authToken: 'my-token',
        agentUrl: 'http://10.0.0.1:18080',
      });

      const cfg = YaverFeedback.getConfig();
      expect(cfg).toBeDefined();
      expect(cfg!.authToken).toBe('my-token');
      expect(cfg!.agentUrl).toBe('http://10.0.0.1:18080');
    });
  });

  describe('getSelectedRemoteDevice()', () => {
    it('returns the selected device from the reachable device list', async () => {
      YaverFeedback.init({
        authToken: 'tok',
        preferredDeviceId: 'device-1',
        enabled: true,
      });

      const device = await YaverFeedback.getSelectedRemoteDevice();
      expect(device?.deviceId).toBe('device-1');
      expect(device?.name).toBe('Dev Mac');
    });
  });

  describe('startReport()', () => {
    it('does nothing when not enabled', async () => {
      YaverFeedback.init({ authToken: 'tok', enabled: false });

      // Should not throw
      await YaverFeedback.startReport();

      const { DeviceEventEmitter } = require('react-native');
      expect(DeviceEventEmitter.emit).not.toHaveBeenCalled();
    });

    it('emits event when enabled and agentUrl is set', async () => {
      YaverFeedback.init({
        authToken: 'tok',
        enabled: true,
        agentUrl: 'http://localhost:18080',
      });

      await YaverFeedback.startReport();

      const { DeviceEventEmitter } = require('react-native');
      expect(DeviceEventEmitter.emit).toHaveBeenCalledWith('yaverFeedback:startReport');
    });
  });
});
