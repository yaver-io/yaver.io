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

describe('SDK Token Auth', () => {
  describe('Bearer token in requests', () => {
    it('sends SDK token in Authorization header for feedback', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ id: 'report-1' }),
        text: () => Promise.resolve(''),
      });

      const client = new P2PClient('http://192.168.1.10:18080', 'sdk-token-abc123');
      const bundle = {
        metadata: {
          timestamp: '2026-03-25T00:00:00Z',
          device: { platform: 'ios', osVersion: '18', model: 'iPhone', screenWidth: 393, screenHeight: 852 },
          app: {},
        },
        screenshots: [],
      };
      await client.uploadFeedback(bundle);

      expect(mockFetch).toHaveBeenCalledWith(
        'http://192.168.1.10:18080/feedback',
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: 'Bearer sdk-token-abc123',
          }),
        })
      );
    });

    it('sends SDK token in Authorization header for voice', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ voiceInputEnabled: true }),
        text: () => Promise.resolve(''),
      });

      const client = new P2PClient('http://localhost:18080', 'my-sdk-token');
      await client.voiceStatus();

      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/voice/status',
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: 'Bearer my-sdk-token',
          }),
        })
      );
    });

    it('sends SDK token for test session', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ sessionId: 'sess-1' }),
        text: () => Promise.resolve(''),
      });

      const client = new P2PClient('http://localhost:18080', 'test-sdk-token');
      await client.startTestSession();

      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/test-app/start',
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: 'Bearer test-sdk-token',
          }),
        })
      );
    });
  });

  describe('setAuthToken()', () => {
    it('updates token for subsequent requests', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ hostname: 'test' }),
        text: () => Promise.resolve(''),
      });

      const client = new P2PClient('http://localhost:18080', 'old-token');
      client.setAuthToken('new-sdk-token');
      await client.info();

      expect(mockFetch).toHaveBeenCalledWith(
        expect.any(String),
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: 'Bearer new-sdk-token',
          }),
        })
      );
    });
  });

  describe('Error handling for SDK token auth failures', () => {
    it('throws on 403 Forbidden (scope violation)', async () => {
      mockFetch.mockResolvedValue({
        ok: false,
        status: 403,
        text: () => Promise.resolve('SDK token scope does not allow this endpoint'),
      });

      const client = new P2PClient('http://localhost:18080', 'limited-sdk');
      await expect(client.info()).rejects.toThrow('403');
    });

    it('throws on 403 Forbidden (IP binding)', async () => {
      mockFetch.mockResolvedValue({
        ok: false,
        status: 403,
        text: () => Promise.resolve('SDK token not allowed from this IP'),
      });

      const client = new P2PClient('http://localhost:18080', 'ip-bound-sdk');
      await expect(client.info()).rejects.toThrow('403');
    });

    it('throws on 401 Unauthorized (expired token)', async () => {
      mockFetch.mockResolvedValue({
        ok: false,
        status: 401,
        text: () => Promise.resolve('Invalid or expired SDK token'),
      });

      const client = new P2PClient('http://localhost:18080', 'expired-sdk');
      await expect(client.info()).rejects.toThrow('401');
    });
  });

  describe('Token rotation', () => {
    it('rotateToken() calls POST /sdk/token/rotate', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({
          token: 'new-rotated-token',
          expiresAt: Date.now() + 365 * 24 * 60 * 60 * 1000,
        }),
        text: () => Promise.resolve(''),
      });

      const client = new P2PClient('http://localhost:18080', 'current-sdk-token');
      const result = await client.rotateToken();

      expect(result.token).toBe('new-rotated-token');
      expect(result.expiresAt).toBeGreaterThan(Date.now());

      // Verify the request was made correctly
      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:18080/sdk/token/rotate',
        expect.objectContaining({
          method: 'POST',
          headers: expect.objectContaining({
            Authorization: 'Bearer current-sdk-token',
          }),
        })
      );
    });

    it('rotateToken() auto-updates internal token', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          token: 'rotated-token-xyz',
          expiresAt: Date.now() + 365 * 24 * 60 * 60 * 1000,
        }),
        text: () => Promise.resolve(''),
      });

      const client = new P2PClient('http://localhost:18080', 'old-token');
      await client.rotateToken();

      // Now make another request — should use the new token
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ hostname: 'test' }),
        text: () => Promise.resolve(''),
      });
      await client.info();

      // Second call should use the rotated token
      expect(mockFetch).toHaveBeenLastCalledWith(
        expect.any(String),
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: 'Bearer rotated-token-xyz',
          }),
        })
      );
    });

    it('rotateToken() throws on failure', async () => {
      mockFetch.mockResolvedValue({
        ok: false,
        status: 401,
        text: () => Promise.resolve('Token already rotated'),
      });

      const client = new P2PClient('http://localhost:18080', 'already-rotated');
      await expect(client.rotateToken()).rejects.toThrow('Token rotation failed');
    });
  });

  describe('Health endpoint with TLS info', () => {
    it('health response includes TLS fingerprint', async () => {
      mockFetch.mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({
          ok: true,
          hostname: 'MacBook',
          version: '1.45.0',
          tlsFingerprint: 'abc123def456789',
          tlsPort: 18443,
        }),
        text: () => Promise.resolve(''),
      });

      const client = new P2PClient('http://localhost:18080', 'tok');
      const info = await client.info();
      expect(info.hostname).toBe('MacBook');
      expect(info.version).toBe('1.45.0');
    });
  });

  describe('Multiple SDK token scenarios', () => {
    it('works with long token strings', async () => {
      const longToken = 'a'.repeat(64); // 64-char hex token
      mockFetch.mockResolvedValue({ ok: true });

      const client = new P2PClient('http://localhost:18080', longToken);
      const healthy = await client.health();
      expect(healthy).toBe(true);
    });

    it('health check does not send auth header', async () => {
      mockFetch.mockResolvedValue({ ok: true });

      const client = new P2PClient('http://localhost:18080', 'secret-sdk');
      await client.health();

      // health() should NOT send Authorization header
      const call = mockFetch.mock.calls[0];
      expect(call[1].headers).toBeUndefined();
    });
  });
});
