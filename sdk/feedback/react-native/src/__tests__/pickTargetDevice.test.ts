// Target-device selection contract.
//
// This exists because pickTargetDevice used to silently reroute: when the
// user's `preferredDeviceId` was absent from the list — or present but
// carrying an empty `quicHost` — the preference block fell through to the
// generic "first fresh machine" search. The user picked their Mac mini and
// the fix landed on their laptop, with no error on either end.
//
// The contract now: an explicit preference is honoured by id alone, or the
// pick fails. Not connecting is a better failure than connecting to the
// wrong host.

import { pickTargetDevice, isDeviceFresh } from '../_core/device';
import { HEARTBEAT_STALE_MS } from '../_core/constants';
import type { CoreDevice } from '../_core/device';

const NOW = 1_700_000_000_000;

function device(over: Partial<CoreDevice> & { deviceId: string }): CoreDevice {
  return {
    name: over.deviceId,
    platform: 'darwin',
    isOnline: true,
    lastHeartbeat: NOW,
    quicHost: '100.64.0.1',
    quicPort: 18080,
    ...over,
  } as CoreDevice;
}

beforeEach(() => {
  jest.spyOn(Date, 'now').mockReturnValue(NOW);
});

afterEach(() => {
  jest.restoreAllMocks();
});

describe('pickTargetDevice — explicit preference', () => {
  it('returns the preferred device when it is fresh', () => {
    const macmini = device({ deviceId: 'macmini' });
    const laptop = device({ deviceId: 'laptop' });
    expect(pickTargetDevice([laptop, macmini], 'macmini')).toBe(macmini);
  });

  it('returns the preferred device even when it is STALE, rather than rerouting', () => {
    const macmini = device({
      deviceId: 'macmini',
      lastHeartbeat: NOW - (HEARTBEAT_STALE_MS + 1),
    });
    const laptop = device({ deviceId: 'laptop' });
    expect(isDeviceFresh(macmini)).toBe(false);
    expect(pickTargetDevice([laptop, macmini], 'macmini')).toBe(macmini);
  });

  it('returns the preferred device even with NO quicHost — relay addresses it by id', () => {
    // This is the off-LAN shape: no LAN address is published, and the relay
    // route is `<relay>/d/<deviceId>`. An empty quicHost is not a reason to
    // reroute; it is the normal remote case.
    const macmini = device({ deviceId: 'macmini', quicHost: '' });
    const laptop = device({ deviceId: 'laptop' });
    expect(pickTargetDevice([laptop, macmini], 'macmini')).toBe(macmini);
  });

  it('returns null when the preferred id is absent — never falls through to another machine', () => {
    const laptop = device({ deviceId: 'laptop' });
    const desktop = device({ deviceId: 'desktop' });
    expect(pickTargetDevice([laptop, desktop], 'macmini')).toBeNull();
  });

  it('returns null for an unknown preference even when exactly one healthy machine exists', () => {
    // The tempting "well, there's only one, just use it" case. Still wrong:
    // the user asked for a specific host.
    expect(pickTargetDevice([device({ deviceId: 'laptop' })], 'macmini')).toBeNull();
  });
});

describe('pickTargetDevice — no preference', () => {
  it('prefers a fresh device with a quicHost over a stale one', () => {
    const stale = device({
      deviceId: 'stale',
      lastHeartbeat: NOW - (HEARTBEAT_STALE_MS + 1),
    });
    const fresh = device({ deviceId: 'fresh' });
    expect(pickTargetDevice([stale, fresh])).toBe(fresh);
  });

  it('falls back to an online device with a quicHost when none are fresh', () => {
    const offline = device({ deviceId: 'offline', isOnline: false });
    const online = device({
      deviceId: 'online',
      lastHeartbeat: NOW - (HEARTBEAT_STALE_MS + 1),
    });
    expect(pickTargetDevice([offline, online])).toBe(online);
  });

  it('returns null for an empty list', () => {
    expect(pickTargetDevice([], 'macmini')).toBeNull();
    expect(pickTargetDevice([])).toBeNull();
  });
});
