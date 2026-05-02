import { listReachableDevices } from '../auth';

const mockFetch = jest.fn();
global.fetch = mockFetch as any;

describe('auth device listing', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockFetch.mockReset();
  });

  it('keeps guest-shared devices in the shared bucket', async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      json: () =>
        Promise.resolve({
          devices: [
            {
              deviceId: 'own-1',
              name: 'My Mac',
              platform: 'macos',
              isOnline: true,
              isGuest: false,
              quicHost: '10.0.0.10',
              quicPort: 18080,
              lastHeartbeat: 123,
            },
            {
              deviceId: 'guest-1',
              name: 'yaver-test-ephemeral',
              platform: 'linux',
              isOnline: true,
              isGuest: true,
              hostName: 'Host User',
              hostEmail: 'host@example.com',
              accessScope: 'shared-scoped',
              quicHost: '198.51.100.20',
              quicPort: 18080,
              lastHeartbeat: 456,
            },
          ],
        }),
    });

    const result = await listReachableDevices('sdk-user-token');

    expect(mockFetch).toHaveBeenCalledWith(
      expect.stringContaining('/devices/list'),
      expect.objectContaining({
        headers: { Authorization: 'Bearer sdk-user-token' },
      }),
    );
    expect(result.owned).toHaveLength(1);
    expect(result.shared).toHaveLength(1);
    expect(result.owned[0].deviceId).toBe('own-1');
    expect(result.shared[0]).toMatchObject({
      deviceId: 'guest-1',
      isGuest: true,
      hostEmail: 'host@example.com',
      accessScope: 'shared-scoped',
    });
  });

  it('shows shared devices even when the guest owns no machines', async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      json: () =>
        Promise.resolve({
          devices: [
            {
              deviceId: 'guest-only',
              name: 'yaver-test-ephemeral',
              platform: 'linux',
              isOnline: true,
              isGuest: true,
              hostName: 'Host User',
              hostEmail: 'host@example.com',
              accessScope: 'shared-scoped',
              quicHost: '198.51.100.20',
              quicPort: 18080,
              lastHeartbeat: 789,
            },
          ],
        }),
    });

    const result = await listReachableDevices('guest-only-token');

    expect(result.owned).toEqual([]);
    expect(result.shared).toHaveLength(1);
    expect(result.shared[0].deviceId).toBe('guest-only');
  });
});
