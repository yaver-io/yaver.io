import { buildProbeCandidates } from '../_core/device';
import { urlHost } from '../_core/urlHost';
import type { CoreDevice } from '../_core/device';

const target = (over: Partial<CoreDevice>): CoreDevice => ({
  deviceId: 'ipv6-host',
  name: 'IPv6 host',
  platform: 'linux',
  isOnline: true,
  lastHeartbeat: Date.now(),
  quicHost: '2001:db8::10',
  quicPort: 18080,
  ...over,
} as CoreDevice);

describe('IPv6 probe candidates', () => {
  it('brackets IPv6 literals and preserves IPv4 hosts', () => {
    expect(buildProbeCandidates(target({ localIps: ['192.0.2.10', 'fd00::20'] }))).toEqual([
      'http://[2001:db8::10]:18080',
      'http://192.0.2.10:18080',
      'http://[fd00::20]:18080',
    ]);
  });

  it('does not double-bracket an already formatted IPv6 host', () => {
    expect(urlHost('[2001:db8::10]')).toBe('[2001:db8::10]');
  });
});
