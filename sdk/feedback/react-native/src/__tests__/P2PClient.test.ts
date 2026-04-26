import { P2PClient } from '../P2PClient';

// Mock react-native Platform
jest.mock('react-native', () => ({
  Platform: { OS: 'ios' },
}));

// Mock fetch globally
const mockFetch = jest.fn();
global.fetch = mockFetch as any;

// Mock AbortController
class MockAbortController {
  signal = {};
  abort = jest.fn();
}
global.AbortController = MockAbortController as any;

beforeEach(() => {
  jest.clearAllMocks();
  mockFetch.mockReset();
});

describe('P2PClient', () => {
  describe('constructor', () => {
    it('sets baseUrl and authToken', () => {
      const client = new P2PClient('http://192.168.1.10:18080', 'my-token');
      // Verify via getArtifactUrl which uses baseUrl
      expect(client.getArtifactUrl('build-123')).toBe(
        'http://192.168.1.10:18080/builds/build-123/artifact'
      );
    });

    it('strips trailing slash from baseUrl', () => {
      const client = new P2PClient('http://192.168.1.10:18080/', 'tok');
      expect(client.getArtifactUrl('b1')).toBe(
        'http://192.168.1.10:18080/builds/b1/artifact'
      );
    });
  });

  describe('health()', () => {
    it('returns false for unreachable agent', async () => {
      mockFetch.mockRejectedValue(new Error('Network error'));

      const client = new P2PClient('http://192.168.1.99:18080', 'tok');
      const result = await client.health();
      expect(result).toBe(false);
    });

    it('returns true when agent is reachable', async () => {
      mockFetch.mockResolvedValue({ ok: true });

      const client = new P2PClient('http://192.168.1.10:18080', 'tok');
      const result = await client.health();
      expect(result).toBe(true);
    });

    it('returns false for non-ok response', async () => {
      mockFetch.mockResolvedValue({ ok: false, status: 503 });

      const client = new P2PClient('http://192.168.1.10:18080', 'tok');
      const result = await client.health();
      expect(result).toBe(false);
    });

    it('calls /health endpoint', async () => {
      mockFetch.mockResolvedValue({ ok: true });

      const client = new P2PClient('http://10.0.0.1:18080', 'tok');
      await client.health();

      expect(mockFetch).toHaveBeenCalledWith(
        'http://10.0.0.1:18080/health',
        expect.objectContaining({ method: 'GET' })
      );
    });
  });

  describe('getArtifactUrl()', () => {
    it('constructs correct URL for a build ID', () => {
      const client = new P2PClient('http://192.168.1.10:18080', 'tok');
      expect(client.getArtifactUrl('abc-123')).toBe(
        'http://192.168.1.10:18080/builds/abc-123/artifact'
      );
    });

    it('works with different base URLs', () => {
      const client = new P2PClient('http://10.0.0.50:9090', 'tok');
      expect(client.getArtifactUrl('xyz')).toBe(
        'http://10.0.0.50:9090/builds/xyz/artifact'
      );
    });
  });

  describe('setBaseUrl()', () => {
    it('updates the base URL used by subsequent calls', () => {
      const client = new P2PClient('http://old-host:18080', 'tok');
      client.setBaseUrl('http://new-host:18080');
      expect(client.getArtifactUrl('b1')).toBe(
        'http://new-host:18080/builds/b1/artifact'
      );
    });

    it('strips trailing slash from new URL', () => {
      const client = new P2PClient('http://old:18080', 'tok');
      client.setBaseUrl('http://new:18080/');
      expect(client.getArtifactUrl('b1')).toBe(
        'http://new:18080/builds/b1/artifact'
      );
    });
  });

  describe('info()', () => {
    it('returns agent info on success', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'MacBook', version: '1.44.0', platform: 'darwin' }),
        text: () => Promise.resolve(''),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const info = await client.info();
      expect(info.hostname).toBe('MacBook');
      expect(info.version).toBe('1.44.0');
      expect(info.platform).toBe('darwin');
    });

    it('sends Authorization header', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'test' }),
        text: () => Promise.resolve(''),
      });

      const client = new P2PClient('http://localhost:18080', 'my-secret-token');
      await client.info();

      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/health',
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: 'Bearer my-secret-token',
          }),
        })
      );
    });
  });

  describe('uploadFeedback()', () => {
    it('sends a POST request to /feedback', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ id: 'report-456' }),
        text: () => Promise.resolve(''),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const bundle = {
        metadata: {
          timestamp: '2026-03-24T00:00:00Z',
          device: {
            platform: 'ios',
            osVersion: '18.0',
            model: 'iPhone 16',
            screenWidth: 393,
            screenHeight: 852,
          },
          app: { bundleId: 'com.test', version: '1.0' },
          userNote: 'Button is broken',
        },
        screenshots: [],
      };

      const reportId = await client.uploadFeedback(bundle);
      expect(reportId).toBe('report-456');
      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/feedback',
        expect.objectContaining({ method: 'POST' })
      );
    });

    it('throws on non-ok response', async () => {
      mockFetch.mockResolvedValue({
        ok: false,
        status: 500,
        text: () => Promise.resolve('Internal Server Error'),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const bundle = {
        metadata: {
          timestamp: '2026-03-24T00:00:00Z',
          device: { platform: 'ios', osVersion: '18', model: 'iPhone', screenWidth: 393, screenHeight: 852 },
          app: {},
        },
        screenshots: [],
      };

      await expect(client.uploadFeedback(bundle)).rejects.toThrow('Upload failed');
    });
  });

  describe('listBuilds()', () => {
    it('returns builds array on success', async () => {
      const builds = [{ id: '1', platform: 'ios', status: 'complete' }];
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ builds }),
        text: () => Promise.resolve(''),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.listBuilds();
      expect(result).toEqual(builds);
    });
  });

  describe('reloadApp()', () => {
    it('returns an acknowledgement for dev reloads', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ ok: true, changeClass: 'js_only' }),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.reloadApp('dev');

      expect(result).toEqual(
        expect.objectContaining({
          ok: true,
          mode: 'dev',
          acknowledged: true,
          message: 'Hot reload request accepted.',
        }),
      );
    });

    it('returns an acknowledgement for bundle reloads', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ ok: true }),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.reloadApp('bundle');

      expect(result).toEqual(
        expect.objectContaining({
          ok: true,
          mode: 'bundle',
          acknowledged: true,
          message: 'Reload request acknowledged. Agent is rebuilding the bundle.',
        }),
      );
    });

    it('dev mode hits /dev/reload with bearer auth', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ ok: true }),
      });
      const client = new P2PClient('http://localhost:18080', 'tok');
      await client.reloadApp('dev');
      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/dev/reload',
        expect.objectContaining({
          method: 'POST',
          headers: expect.objectContaining({ Authorization: 'Bearer tok' }),
        }),
      );
    });

    it('bundle mode hits /dev/reload-app with mode + projectName in body', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ ok: true }),
      });
      const client = new P2PClient('http://localhost:18080', 'tok');
      await client.reloadApp('bundle', { projectName: 'sfmg' });
      const [url, init] = mockFetch.mock.calls[0];
      expect(url).toBe('http://localhost:18080/dev/reload-app');
      expect(init.method).toBe('POST');
      expect(JSON.parse(init.body as string)).toEqual(
        expect.objectContaining({ mode: 'bundle', projectName: 'sfmg' }),
      );
      expect(init.headers).toEqual(
        expect.objectContaining({
          Authorization: 'Bearer tok',
          'Content-Type': 'application/json',
        }),
      );
    });

    it('dev mode falls back to /dev/reload-app when /dev/reload 4xxs', async () => {
      // First call: /dev/reload 404. Second: /dev/reload-app 200.
      mockFetch
        .mockResolvedValueOnce({ ok: false, status: 404, json: () => Promise.resolve({}) })
        .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ ok: true }) });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.reloadApp('dev', { projectName: 'sfmg' });

      expect(mockFetch).toHaveBeenCalledTimes(2);
      expect(mockFetch.mock.calls[0][0]).toBe('http://localhost:18080/dev/reload');
      expect(mockFetch.mock.calls[1][0]).toBe('http://localhost:18080/dev/reload-app');
      expect(result.ok).toBe(true);
    });

    it('surfaces nativeChangesDetected so the host can prompt for rebuild', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () =>
          Promise.resolve({
            ok: true,
            nativeChangesDetected: true,
            changeClass: 'native_required',
          }),
      });
      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.reloadApp('dev');
      expect(result.nativeChangesDetected).toBe(true);
      expect(result.changeClass).toBe('native_required');
      // Caller-visible message must distinguish native-required from JS-only.
      expect(result.message).toMatch(/native|rebuild/i);
    });
  });
});
