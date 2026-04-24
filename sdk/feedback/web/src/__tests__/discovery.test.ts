import { YaverDiscovery } from '../discovery';

// Mock fetch
const mockFetch = jest.fn();
global.fetch = mockFetch as any;

// Mock localStorage
const mockStorage: Record<string, string> = {};
const localStorageMock = {
  getItem: jest.fn((key: string) => mockStorage[key] || null),
  setItem: jest.fn((key: string, value: string) => {
    mockStorage[key] = value;
  }),
  removeItem: jest.fn((key: string) => {
    delete mockStorage[key];
  }),
};
Object.defineProperty(window, 'localStorage', { value: localStorageMock });

beforeEach(() => {
  jest.clearAllMocks();
  mockFetch.mockReset();
  Object.keys(mockStorage).forEach((key) => delete mockStorage[key]);
});

describe('YaverDiscovery', () => {
  describe('probe()', () => {
    it('returns null for unreachable URL', async () => {
      mockFetch.mockRejectedValue(new Error('Network error'));

      const result = await YaverDiscovery.probe('http://192.168.1.99:18080');
      expect(result).toBeNull();
    });

    it('returns null for non-ok response', async () => {
      mockFetch.mockResolvedValue({ ok: false, status: 404 });

      const result = await YaverDiscovery.probe('http://192.168.1.1:18080');
      expect(result).toBeNull();
    });

    it('returns DiscoveryResult for reachable agent', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'MacBook-Pro', version: '1.44.0' }),
      });

      const result = await YaverDiscovery.probe('http://192.168.1.10:18080');
      expect(result).not.toBeNull();
      expect(result!.url).toBe('http://192.168.1.10:18080');
      expect(result!.hostname).toBe('MacBook-Pro');
      expect(result!.version).toBe('1.44.0');
      expect(typeof result!.latency).toBe('number');
      expect(result!.latency).toBeGreaterThanOrEqual(0);
    });

    it('calls /health endpoint with abort signal', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'test', version: '1.0' }),
      });

      await YaverDiscovery.probe('http://localhost:18080');

      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/health',
        expect.objectContaining({ signal: expect.anything() })
      );
    });
  });

  describe('getStored()', () => {
    it('returns null when no stored connection', () => {
      const result = YaverDiscovery.getStored();
      expect(result).toBeNull();
    });

    it('returns stored connection when available', () => {
      mockStorage['yaver_feedback_agent'] = JSON.stringify({
        url: 'http://192.168.1.10:18080',
        hostname: 'MacBook',
        timestamp: Date.now(),
      });

      const result = YaverDiscovery.getStored();
      expect(result).not.toBeNull();
      expect(result!.url).toBe('http://192.168.1.10:18080');
      expect(result!.hostname).toBe('MacBook');
    });

    it('returns null for expired stored connection (>24h)', () => {
      mockStorage['yaver_feedback_agent'] = JSON.stringify({
        url: 'http://192.168.1.10:18080',
        hostname: 'OldMachine',
        timestamp: Date.now() - 25 * 60 * 60 * 1000, // 25 hours ago
      });

      const result = YaverDiscovery.getStored();
      expect(result).toBeNull();
    });

    it('returns null for invalid JSON in storage', () => {
      mockStorage['yaver_feedback_agent'] = 'not-json{{{';

      const result = YaverDiscovery.getStored();
      expect(result).toBeNull();
    });
  });

  describe('store() + getStored() round-trip', () => {
    it('stores and retrieves a DiscoveryResult via localStorage', () => {
      const discovery = {
        url: 'http://10.0.0.50:18080',
        hostname: 'DevMachine',
        version: '1.44.0',
        latency: 5,
      };

      YaverDiscovery.store(discovery);

      expect(localStorageMock.setItem).toHaveBeenCalledWith(
        'yaver_feedback_agent',
        expect.any(String)
      );

      const stored = YaverDiscovery.getStored();
      expect(stored).not.toBeNull();
      expect(stored!.url).toBe('http://10.0.0.50:18080');
      expect(stored!.hostname).toBe('DevMachine');
    });
  });

  describe('clear()', () => {
    it('removes stored connection from localStorage', () => {
      mockStorage['yaver_feedback_agent'] = JSON.stringify({
        url: 'http://10.0.0.1:18080',
        hostname: 'Test',
        timestamp: Date.now(),
      });

      YaverDiscovery.clear();

      expect(localStorageMock.removeItem).toHaveBeenCalledWith('yaver_feedback_agent');
      const stored = YaverDiscovery.getStored();
      expect(stored).toBeNull();
    });
  });

  describe('connect()', () => {
    it('probes and stores on success', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'Agent', version: '2.0' }),
      });

      const result = await YaverDiscovery.connect('http://192.168.1.5:18080');
      expect(result).not.toBeNull();
      expect(result!.hostname).toBe('Agent');
      expect(localStorageMock.setItem).toHaveBeenCalled();
    });

    it('returns null on failure and does not store', async () => {
      mockFetch.mockRejectedValue(new Error('unreachable'));

      const result = await YaverDiscovery.connect('http://192.168.1.99:18080');
      expect(result).toBeNull();
      expect(localStorageMock.setItem).not.toHaveBeenCalled();
    });
  });

  describe('discover()', () => {
    it('returns stored connection if still reachable', async () => {
      // Store a connection
      mockStorage['yaver_feedback_agent'] = JSON.stringify({
        url: 'http://192.168.1.10:18080',
        hostname: 'StoredMachine',
        timestamp: Date.now(),
      });

      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'StoredMachine', version: '1.0' }),
      });

      const result = await YaverDiscovery.discover();
      expect(result).not.toBeNull();
      expect(result!.hostname).toBe('StoredMachine');
    });

    it('tries localhost if no stored connection', async () => {
      // First call (localhost) succeeds
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ hostname: 'LocalAgent', version: '1.0' }),
      });

      const result = await YaverDiscovery.discover();
      expect(result).not.toBeNull();
      expect(result!.hostname).toBe('LocalAgent');
      // Should store the result
      expect(localStorageMock.setItem).toHaveBeenCalled();
    });

    it('returns null when nothing is reachable', async () => {
      mockFetch.mockRejectedValue(new Error('unreachable'));

      const result = await YaverDiscovery.discover();
      expect(result).toBeNull();
    });

    it('prefers Convex-backed device discovery when auth is available', async () => {
      // URL-routed mocks: the new discovery path fetches /devices/list,
      // /config, /settings, and then races every candidate transport.
      // Only the direct LAN probe resolves so it's the unambiguous winner.
      mockFetch.mockImplementation(async (input: RequestInfo | URL) => {
        const url = typeof input === 'string' ? input : input.toString();
        if (url.endsWith('/devices/list')) {
          return {
            ok: true,
            json: async () => ({
              devices: [
                {
                  deviceId: 'dev-1',
                  name: 'MacBook',
                  platform: 'darwin',
                  isOnline: true,
                  needsAuth: false,
                  runnerDown: false,
                  lastHeartbeat: Date.now(),
                  isGuest: false,
                  accessScope: 'owner',
                  quicHost: '192.168.1.30',
                  quicPort: 18080,
                  localIps: ['10.0.0.20'],
                },
              ],
            }),
          };
        }
        if (url.endsWith('/config')) {
          return { ok: true, json: async () => ({ relayServers: [] }) };
        }
        if (url.endsWith('/settings')) {
          return { ok: true, json: async () => ({ settings: {} }) };
        }
        if (url === 'http://192.168.1.30:18080/health') {
          return {
            ok: true,
            json: async () => ({ hostname: 'MacBook', version: '1.0' }),
          };
        }
        if (url === 'http://10.0.0.20:18080/health') {
          // LocalIP health fails so the test has a single winner.
          throw new Error('other LAN ip unreachable');
        }
        throw new Error(`unexpected fetch: ${url}`);
      });

      const result = await YaverDiscovery.discover({
        authToken: 'tok',
        convexUrl: 'https://convex.example',
      });

      expect(result).not.toBeNull();
      expect(result!.url).toBe('http://192.168.1.30:18080');
      // The devices/list call still happens first with Bearer auth.
      expect(mockFetch).toHaveBeenCalledWith(
        'https://perceptive-minnow-557.eu-west-1.convex.site/devices/list',
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: 'Bearer tok',
          }),
        }),
      );
    });

    it('reaches the agent through the relay when no direct path works', async () => {
      mockFetch.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = typeof input === 'string' ? input : input.toString();
        if (url.endsWith('/devices/list')) {
          return {
            ok: true,
            json: async () => ({
              devices: [
                {
                  deviceId: 'dev-1',
                  name: 'MacBook',
                  platform: 'darwin',
                  isOnline: true,
                  needsAuth: false,
                  runnerDown: false,
                  lastHeartbeat: Date.now(),
                  isGuest: false,
                  accessScope: 'owner',
                  quicHost: '192.168.1.30',
                  quicPort: 18080,
                },
              ],
            }),
          };
        }
        if (url.endsWith('/config')) {
          return {
            ok: true,
            json: async () => ({
              relayServers: [{ httpUrl: 'https://relay.example', priority: 0 }],
            }),
          };
        }
        if (url.endsWith('/settings')) {
          return {
            ok: true,
            json: async () => ({ settings: { relayPassword: 'pw' } }),
          };
        }
        if (url === 'http://192.168.1.30:18080/health') {
          throw new Error('direct down');
        }
        if (url === 'https://relay.example/d/dev-1/health') {
          // Assert the probe carries both Bearer and X-Relay-Password.
          const headers = (init?.headers ?? {}) as Record<string, string>;
          if (headers.Authorization !== 'Bearer tok') {
            throw new Error('missing Bearer header on relay probe');
          }
          if (headers['X-Relay-Password'] !== 'pw') {
            throw new Error('missing X-Relay-Password on relay probe');
          }
          return {
            ok: true,
            json: async () => ({ hostname: 'RelayMac', version: '1.1' }),
          };
        }
        throw new Error(`unexpected fetch: ${url}`);
      });

      const result = await YaverDiscovery.discoverFromConvex(
        'https://convex.example',
        'tok',
      );

      expect(result).not.toBeNull();
      expect(result!.url).toBe('https://relay.example/d/dev-1');
      expect(result!.relayPassword).toBe('pw');
      expect(mockFetch).toHaveBeenCalledWith(
        'https://relay.example/d/dev-1/health',
        expect.objectContaining({
          headers: expect.objectContaining({
            'X-Relay-Password': 'pw',
            Authorization: 'Bearer tok',
          }),
        }),
      );
    });
  });
});
