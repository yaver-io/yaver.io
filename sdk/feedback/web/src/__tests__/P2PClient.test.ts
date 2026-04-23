import { P2PClient } from '../P2PClient';

// Mock fetch
const mockFetch = jest.fn();
global.fetch = mockFetch as any;

// Polyfill AbortSignal.timeout if not available in test env
if (!AbortSignal.timeout) {
  (AbortSignal as any).timeout = (ms: number) => {
    const controller = new AbortController();
    setTimeout(() => controller.abort(), ms);
    return controller.signal;
  };
}

beforeEach(() => {
  jest.clearAllMocks();
  mockFetch.mockReset();
});

describe('P2PClient', () => {
  describe('health()', () => {
    it('returns false for unreachable agent', async () => {
      mockFetch.mockRejectedValue(new Error('Network error'));

      const client = new P2PClient('http://192.168.1.99:18080');
      const result = await client.health();
      expect(result).toBe(false);
    });

    it('returns true when fetch succeeds with ok response', async () => {
      mockFetch.mockResolvedValue({ ok: true });

      const client = new P2PClient('http://localhost:18080');
      const result = await client.health();
      expect(result).toBe(true);
    });

    it('returns false for non-ok response', async () => {
      mockFetch.mockResolvedValue({ ok: false, status: 503 });

      const client = new P2PClient('http://localhost:18080');
      const result = await client.health();
      expect(result).toBe(false);
    });

    it('calls the /health endpoint', async () => {
      mockFetch.mockResolvedValue({ ok: true });

      const client = new P2PClient('http://10.0.0.1:18080');
      await client.health();

      expect(mockFetch).toHaveBeenCalledWith(
        'http://10.0.0.1:18080/health',
        expect.objectContaining({ signal: expect.anything() })
      );
    });
  });

  describe('getArtifactUrl()', () => {
    it('constructs correct URL', () => {
      const client = new P2PClient('http://192.168.1.10:18080', 'tok');
      expect(client.getArtifactUrl('build-abc')).toBe(
        'http://192.168.1.10:18080/builds/build-abc/artifact'
      );
    });

    it('works with different base URLs and build IDs', () => {
      const client = new P2PClient('http://10.0.0.50:9090', 'tok');
      expect(client.getArtifactUrl('xyz-789')).toBe(
        'http://10.0.0.50:9090/builds/xyz-789/artifact'
      );
    });
  });

  describe('headers', () => {
    it('includes X-Client-Platform: web in requests', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'test' }),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      await client.info();

      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/info',
        expect.objectContaining({
          headers: expect.objectContaining({
            'X-Client-Platform': 'web',
          }),
        })
      );
    });

    it('includes Authorization header when token is set', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'test' }),
      });

      const client = new P2PClient('http://localhost:18080', 'my-secret');
      await client.info();

      expect(mockFetch).toHaveBeenCalledWith(
        expect.any(String),
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: 'Bearer my-secret',
          }),
        })
      );
    });

    it('does not include Authorization header when token is empty', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'test' }),
      });

      const client = new P2PClient('http://localhost:18080');
      await client.info();

      const callHeaders = mockFetch.mock.calls[0][1].headers;
      expect(callHeaders['Authorization']).toBeUndefined();
    });
  });

  describe('uploadFeedback()', () => {
    it('sends FormData via POST to /feedback', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ id: 'report-123' }),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const bundle = {
        metadata: {
          source: 'in-app-sdk' as const,
          deviceInfo: {
            platform: 'web' as const,
            browser: 'Chrome',
            browserVersion: '120.0',
            os: 'MacIntel',
            screenSize: '1920x1080',
            userAgent: 'Mozilla/5.0...',
          },
          url: 'http://localhost:3000/dashboard',
          timeline: [],
        },
        screenshots: [],
      };

      const result = await client.uploadFeedback(bundle);
      expect(result).toEqual({ id: 'report-123' });

      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/feedback',
        expect.objectContaining({
          method: 'POST',
          body: expect.any(FormData),
        })
      );
    });

    it('returns null on failure', async () => {
      mockFetch.mockResolvedValue({ ok: false, status: 500 });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const bundle = {
        metadata: {
          source: 'in-app-sdk' as const,
          deviceInfo: {
            platform: 'web' as const,
            browser: 'Chrome',
            browserVersion: '120',
            os: 'MacIntel',
            screenSize: '1920x1080',
            userAgent: 'test',
          },
          url: 'http://localhost:3000',
          timeline: [],
        },
        screenshots: [],
      };

      const result = await client.uploadFeedback(bundle);
      expect(result).toBeNull();
    });

    it('includes auth header in upload request', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ id: 'r1' }),
      });

      const client = new P2PClient('http://localhost:18080', 'upload-token');
      await client.uploadFeedback({
        metadata: {
          source: 'in-app-sdk' as const,
          deviceInfo: {
            platform: 'web' as const,
            browser: 'Chrome',
            browserVersion: '120',
            os: 'MacIntel',
            screenSize: '1920x1080',
            userAgent: 'test',
          },
          url: 'http://localhost:3000',
          timeline: [],
        },
        screenshots: [],
      });

      const callHeaders = mockFetch.mock.calls[0][1].headers;
      expect(callHeaders['Authorization']).toBe('Bearer upload-token');
    });
  });

  describe('info()', () => {
    it('returns agent info on success', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'MacBook', version: '1.44.0', platform: 'darwin' }),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const info = await client.info();
      expect(info).not.toBeNull();
      expect(info!.hostname).toBe('MacBook');
      expect(info!.version).toBe('1.44.0');
    });

    it('returns null on failure', async () => {
      mockFetch.mockResolvedValue({ ok: false, status: 401 });

      const client = new P2PClient('http://localhost:18080', 'bad-tok');
      const info = await client.info();
      expect(info).toBeNull();
    });

    it('returns null on network error', async () => {
      mockFetch.mockRejectedValue(new Error('Network error'));

      const client = new P2PClient('http://unreachable:18080');
      const info = await client.info();
      expect(info).toBeNull();
    });
  });

  describe('listBuilds()', () => {
    it('returns builds array on success', async () => {
      const builds = [{ id: '1', platform: 'ios', status: 'complete' }];
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(builds),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.listBuilds();
      expect(result).toEqual(builds);
    });

    it('returns empty array on failure', async () => {
      mockFetch.mockResolvedValue({ ok: false, status: 500 });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.listBuilds();
      expect(result).toEqual([]);
    });
  });

  describe('createTask()', () => {
    it('sends POST to /tasks with prompt as title', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ id: 'task-1' }),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.createTask('Fix the login button');

      expect(result).toEqual({ id: 'task-1' });
      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/tasks',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ title: 'Fix the login button' }),
        })
      );
    });
  });

  describe('reloadApp()', () => {
    it('uses /dev/reload for dev reloads', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({}),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.reloadApp('dev');

      expect(result.mode).toBe('dev');
      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/dev/reload',
        expect.objectContaining({
          method: 'POST',
          headers: expect.objectContaining({
            Authorization: 'Bearer tok',
          }),
        }),
      );
    });

    it('falls back to /dev/reload-app when dev reload fails', async () => {
      mockFetch
        .mockResolvedValueOnce({ ok: false, status: 500, text: () => Promise.resolve('bad') })
        .mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({ message: 'Bundle rebuild queued.' }),
        });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.reloadApp('dev', { projectName: 'web-app' });

      expect(result.mode).toBe('bundle');
      expect(result.message).toBe('Bundle rebuild queued.');
      expect(mockFetch).toHaveBeenLastCalledWith(
        'http://localhost:18080/dev/reload-app',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({
            mode: 'bundle',
            projectName: 'web-app',
            projectPath: undefined,
            bundleId: undefined,
          }),
        }),
      );
    });
  });

  describe('vibing()', () => {
    it('posts vibing tasks to the agent', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ taskId: 'task-123' }),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.vibing('fix the checkout bug', {
        projectName: 'shop-web',
        projectPath: '/repo/web',
      });

      expect(result.taskId).toBe('task-123');
      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/vibing/execute',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({
            prompt: 'fix the checkout bug',
            projectName: 'shop-web',
            projectPath: '/repo/web',
            bundleId: undefined,
          }),
        }),
      );
    });
  });

  describe('getVibingEligibility()', () => {
    it('queries vibing eligibility with project hints', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ canVibe: true }),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const result = await client.getVibingEligibility({
        projectName: 'shop-web',
        projectPath: '/repo/web',
      });

      expect(result.canVibe).toBe(true);
      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/vibing/eligibility?projectName=shop-web&projectPath=%2Frepo%2Fweb',
        expect.objectContaining({
          method: 'GET',
        }),
      );
    });
  });
});
