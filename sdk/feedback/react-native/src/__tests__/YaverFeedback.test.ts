import { YaverFeedback } from '../YaverFeedback';

// Mock react-native: DeviceEventEmitter for event dispatch + Platform so
// ShakeDetector.start() can branch on iOS without hitting a real RN runtime.
jest.mock('react-native', () => ({
  DeviceEventEmitter: {
    emit: jest.fn(),
    addListener: jest.fn(() => ({ remove: jest.fn() })),
  },
  Platform: { OS: 'ios' },
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
        isGuest: false,
        accessScope: 'owner',
        quicHost: '127.0.0.1',
        quicPort: 18080,
      },
    ],
    shared: [],
  })),
  DEFAULT_CONVEX_SITE_URL: 'https://example.convex.site',
}));

// Reset module-level state between tests by re-requiring
beforeEach(() => {
  // YaverFeedback uses module-level variables (config, enabled, p2pClient).
  // We reset them by calling init with a known state or relying on isInitialized checks.
  // For a clean slate, we re-init with enabled=false then verify.
  jest.clearAllMocks();
});

describe('YaverFeedback', () => {
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
