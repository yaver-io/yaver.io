import { YaverDiscovery, DiscoveryResult } from '../Discovery';

// Mock fetch globally
const mockFetch = jest.fn();
global.fetch = mockFetch as any;

// Mock AsyncStorage. Discovery.ts requires it via `.default`, so the mock
// must expose its API on a `default` property too.
const mockStorage: Record<string, string> = {};
const mockAsyncStorage = {
  getItem: jest.fn((key: string) => Promise.resolve(mockStorage[key] || null)),
  setItem: jest.fn((key: string, value: string) => {
    mockStorage[key] = value;
    return Promise.resolve();
  }),
  removeItem: jest.fn((key: string) => {
    delete mockStorage[key];
    return Promise.resolve();
  }),
};
jest.mock('@react-native-async-storage/async-storage', () => ({
  __esModule: true,
  default: mockAsyncStorage,
  ...mockAsyncStorage,
}));

// Mock AbortController
class MockAbortController {
  signal = {};
  abort = jest.fn();
}
global.AbortController = MockAbortController as any;

beforeEach(() => {
  jest.clearAllMocks();
  mockFetch.mockReset();
  // Clear mock storage
  Object.keys(mockStorage).forEach((key) => delete mockStorage[key]);
});

describe('YaverDiscovery', () => {
  describe('probe()', () => {
    it('returns null for unreachable URLs', async () => {
      mockFetch.mockRejectedValue(new Error('Network error'));

      const result = await YaverDiscovery.probe('http://192.168.1.99:18080');
      expect(result).toBeNull();
    });

    it('returns null for non-ok responses', async () => {
      mockFetch.mockResolvedValue({
        ok: false,
        status: 500,
      });

      const result = await YaverDiscovery.probe('http://192.168.1.1:18080');
      expect(result).toBeNull();
    });

    it('returns DiscoveryResult for a reachable agent', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'MacBook-Air', version: '1.44.0' }),
      });

      const result = await YaverDiscovery.probe('http://192.168.1.10:18080');
      expect(result).not.toBeNull();
      expect(result!.url).toBe('http://192.168.1.10:18080');
      expect(result!.hostname).toBe('MacBook-Air');
      expect(result!.version).toBe('1.44.0');
      expect(typeof result!.latency).toBe('number');
    });

    it('strips trailing slash from URL', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'test', version: '1.0' }),
      });

      const result = await YaverDiscovery.probe('http://192.168.1.10:18080/');
      expect(result!.url).toBe('http://192.168.1.10:18080');
    });

    it('handles health endpoint returning non-JSON gracefully', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.reject(new Error('not JSON')),
      });

      const result = await YaverDiscovery.probe('http://192.168.1.10:18080');
      expect(result).not.toBeNull();
      expect(result!.hostname).toBe('Unknown');
      expect(result!.version).toBe('unknown');
    });
  });

  describe('getStored()', () => {
    it('returns null when no stored connection', async () => {
      const result = await YaverDiscovery.getStored();
      expect(result).toBeNull();
    });

    it('returns stored connection when available', async () => {
      const AsyncStorage = require('@react-native-async-storage/async-storage');
      mockStorage['yaver_feedback_agent'] = JSON.stringify({
        url: 'http://192.168.1.10:18080',
        hostname: 'MacBook',
      });

      const result = await YaverDiscovery.getStored();
      expect(result).not.toBeNull();
      expect(result!.url).toBe('http://192.168.1.10:18080');
      expect(result!.hostname).toBe('MacBook');
    });

    it('returns null for invalid stored JSON', async () => {
      mockStorage['yaver_feedback_agent'] = 'not-json';

      const result = await YaverDiscovery.getStored();
      expect(result).toBeNull();
    });

    it('returns null for stored data without url field', async () => {
      mockStorage['yaver_feedback_agent'] = JSON.stringify({ hostname: 'test' });

      const result = await YaverDiscovery.getStored();
      expect(result).toBeNull();
    });
  });

  describe('store() and getStored() round-trip', () => {
    it('stores and retrieves a DiscoveryResult', async () => {
      const discovery: DiscoveryResult = {
        url: 'http://10.0.0.50:18080',
        hostname: 'DevMachine',
        version: '1.44.0',
        latency: 5,
      };

      await YaverDiscovery.store(discovery);

      const stored = await YaverDiscovery.getStored();
      expect(stored).not.toBeNull();
      expect(stored!.url).toBe('http://10.0.0.50:18080');
      expect(stored!.hostname).toBe('DevMachine');
    });
  });

  describe('clear()', () => {
    it('removes stored connection', async () => {
      const discovery: DiscoveryResult = {
        url: 'http://10.0.0.1:18080',
        hostname: 'Test',
        version: '1.0',
        latency: 3,
      };

      await YaverDiscovery.store(discovery);
      const beforeClear = await YaverDiscovery.getStored();
      expect(beforeClear).not.toBeNull();

      await YaverDiscovery.clear();

      const afterClear = await YaverDiscovery.getStored();
      expect(afterClear).toBeNull();
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

      // Should be stored
      const stored = await YaverDiscovery.getStored();
      expect(stored).not.toBeNull();
      expect(stored!.url).toBe('http://192.168.1.5:18080');
    });

    it('returns null and does not store on failure', async () => {
      mockFetch.mockRejectedValue(new Error('unreachable'));

      const result = await YaverDiscovery.connect('http://192.168.1.99:18080');
      expect(result).toBeNull();
    });
  });
});
