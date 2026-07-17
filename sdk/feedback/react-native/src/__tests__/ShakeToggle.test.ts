// Runtime shake toggling + init() idempotency.
//
// Host apps put the feedback SDK behind a settings switch, which means init /
// enable / disable run repeatedly in one process. Two things used to break
// there:
//
//   1. There was no way to turn the shake catcher off short of
//      setEnabled(false) (which also stops the flight recorder and the agent
//      command channel) or a full re-init. `trigger` is only read inside
//      init(). setShakeEnabled() fills that gap.
//
//   2. init() registered a BlackBox command handler and threw away the
//      unsubscribe, so each re-init stacked another. Two inits meant two
//      reloads per agent command; destroy() never cleared them either.

import { YaverFeedback } from '../YaverFeedback';
import { BlackBox } from '../BlackBox';

const mockShakeStart = jest.fn();
const mockShakeStop = jest.fn();

jest.mock('../ShakeDetector', () => ({
  ShakeDetector: jest.fn().mockImplementation(() => ({
    start: mockShakeStart,
    stop: mockShakeStop,
  })),
}));

jest.mock('react-native', () => ({
  Platform: { OS: 'ios' },
  DeviceEventEmitter: { emit: jest.fn(), addListener: jest.fn() },
  NativeModules: {},
}));

const mockFetch = jest.fn(() =>
  Promise.resolve({ ok: true, json: () => Promise.resolve({}) }),
);
global.fetch = mockFetch as any;

beforeEach(() => {
  jest.clearAllMocks();
  YaverFeedback.destroy();
});

afterEach(() => {
  YaverFeedback.destroy();
});

describe('setShakeEnabled', () => {
  it('arms the shake listener on init when trigger is shake', () => {
    YaverFeedback.init({ trigger: 'shake', agentUrl: 'http://x:18080', authToken: 't' });
    expect(YaverFeedback.isShakeEnabled()).toBe(true);
  });

  it('disarms the listener without disabling the SDK', () => {
    YaverFeedback.init({ trigger: 'shake', agentUrl: 'http://x:18080', authToken: 't' });

    YaverFeedback.setShakeEnabled(false);

    expect(YaverFeedback.isShakeEnabled()).toBe(false);
    expect(mockShakeStop).toHaveBeenCalled();
    // The point of a separate toggle: the rest of the SDK stays up.
    expect(YaverFeedback.isEnabled()).toBe(true);
  });

  it('re-arms the listener', () => {
    YaverFeedback.init({ trigger: 'shake', agentUrl: 'http://x:18080', authToken: 't' });
    YaverFeedback.setShakeEnabled(false);
    YaverFeedback.setShakeEnabled(true);
    expect(YaverFeedback.isShakeEnabled()).toBe(true);
  });

  it('is idempotent — repeated calls do not stack detectors', () => {
    YaverFeedback.init({ trigger: 'shake', agentUrl: 'http://x:18080', authToken: 't' });
    mockShakeStart.mockClear();

    YaverFeedback.setShakeEnabled(true);
    YaverFeedback.setShakeEnabled(true);

    expect(mockShakeStart).not.toHaveBeenCalled();
    expect(YaverFeedback.isShakeEnabled()).toBe(true);
  });

  it('persists onto config so a later enable does not resurrect the listener', () => {
    YaverFeedback.init({ trigger: 'shake', agentUrl: 'http://x:18080', authToken: 't' });
    YaverFeedback.setShakeEnabled(false);

    YaverFeedback.setEnabled(false);
    YaverFeedback.setEnabled(true);

    // The user said no shake. Cycling the master switch must not undo that.
    expect(YaverFeedback.isShakeEnabled()).toBe(false);
    expect(YaverFeedback.getConfig()?.disableShakeGesture).toBe(true);
  });

  it('does not arm the listener for a non-shake trigger', () => {
    YaverFeedback.init({ trigger: 'manual', agentUrl: 'http://x:18080', authToken: 't' });
    expect(YaverFeedback.isShakeEnabled()).toBe(false);

    YaverFeedback.setShakeEnabled(true);
    expect(YaverFeedback.isShakeEnabled()).toBe(false);
  });

  it('no-ops before init rather than throwing', () => {
    expect(() => YaverFeedback.setShakeEnabled(true)).not.toThrow();
    expect(YaverFeedback.isShakeEnabled()).toBe(false);
  });
});

describe('init() command-handler registration', () => {
  it('does not stack command handlers across re-inits', () => {
    const cfg = { trigger: 'shake' as const, agentUrl: 'http://x:18080', authToken: 't' };

    YaverFeedback.init(cfg);
    YaverFeedback.init(cfg);
    YaverFeedback.init(cfg);

    // Three inits, one live handler. Before the fix this array grew by one
    // each time, so a single agent 'reload' fired three reloads.
    expect((BlackBox as any).commandHandlers.length).toBe(1);
  });

  it('fires a reload exactly once per command after repeated inits', () => {
    // The user-visible symptom the count above stands in for.
    const onReload = jest.fn();
    const cfg = { trigger: 'shake' as const, agentUrl: 'http://x:18080', authToken: 't', onReload };

    YaverFeedback.init(cfg);
    YaverFeedback.init(cfg);

    for (const h of (BlackBox as any).commandHandlers) h({ command: 'reload' });

    expect(onReload).toHaveBeenCalledTimes(1);
  });

  it('destroy() unregisters the command handler', () => {
    YaverFeedback.init({ trigger: 'shake', agentUrl: 'http://x:18080', authToken: 't' });
    const before = (BlackBox as any).commandHandlers.length;
    expect(before).toBeGreaterThan(0);

    YaverFeedback.destroy();

    expect((BlackBox as any).commandHandlers.length).toBe(before - 1);
  });

  it('destroy() then init() leaves exactly one handler', () => {
    YaverFeedback.init({ trigger: 'shake', agentUrl: 'http://x:18080', authToken: 't' });
    YaverFeedback.destroy();
    YaverFeedback.init({ trigger: 'shake', agentUrl: 'http://x:18080', authToken: 't' });

    expect((BlackBox as any).commandHandlers.length).toBe(1);
  });
});
