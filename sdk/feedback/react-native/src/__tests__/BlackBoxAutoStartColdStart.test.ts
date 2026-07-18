// BlackBox auto-start on a COLD start — the case that decides whether Hermes
// hot reload works at all.
//
// The command channel that carries the agent's `reload_bundle` is BlackBox's
// SSE stream. If BlackBox never starts, a fix pushed from the dev machine has
// nowhere to land and the phone just sits there.
//
// Auto-start used to be a single 500ms timeout gated on `agentUrl && token`.
// A real app passes NEITHER to init() — they're restored from storage — so on
// a cold start the sequence is:
//
//     init() -> void hydrateSession()
//                 -> AsyncStorage read (token)
//                 -> AsyncStorage read (deviceId)
//                 -> await discoverAgent()   <- Convex round trip + LAN probe
//
// which does not complete in 500ms. The one-shot check found no agentUrl, gave
// up, and hot reload was silently dead until the user shook the device and
// drove discovery by hand. These tests pin the retry that fixes it, and the
// bounds that keep it from becoming a forever-poll or a 401 storm.

import { BlackBox } from '../BlackBox';
import { YaverFeedback } from '../YaverFeedback';

jest.mock('react-native', () => ({
  Platform: { OS: 'ios' },
  DeviceEventEmitter: { emit: jest.fn(), addListener: jest.fn() },
  NativeModules: {},
}));

jest.mock('../ShakeDetector', () => ({
  ShakeDetector: jest.fn().mockImplementation(() => ({
    start: jest.fn(),
    stop: jest.fn(),
  })),
}));

// Discovery is the slow step we're modelling. Resolves only after the test
// flips `discovered`, standing in for "the Convex round trip finally landed".
let discovered: string | null = null;
jest.mock('../Discovery', () => ({
  YaverDiscovery: {
    discover: jest.fn(() =>
      Promise.resolve(discovered ? { url: discovered } : null),
    ),
  },
}));

// The token cache hydrateSession() reads.
let storedToken: string | null = null;
jest.mock('../auth', () => ({
  ...jest.requireActual('../auth'),
  getToken: jest.fn(() => Promise.resolve(storedToken)),
  getSelectedDeviceId: jest.fn(() => Promise.resolve(null)),
  configureAuthEndpoints: jest.fn(),
  setStrictNativeAuth: jest.fn(),
}));

jest.useFakeTimers();

const mockFetch = jest.fn(() =>
  Promise.resolve({ ok: true, json: () => Promise.resolve({}), body: null }),
);

class MockAbortController {
  signal = { aborted: false };
  abort = jest.fn();
}

// fetch and AbortController are process-globals, not per-file module state:
// jest gives each test FILE its own module registry but they share one worker
// process. Assigning them at module scope (as the sibling auto-start suite
// does) leaves them mocked for every file that runs afterwards in the same
// worker. Save and restore instead, so this suite can't be the reason some
// unrelated file sees a stubbed fetch.
const realFetch = global.fetch;
const realAbortController = global.AbortController;

beforeAll(() => {
  global.fetch = mockFetch as any;
  global.AbortController = MockAbortController as any;
});

afterAll(() => {
  global.fetch = realFetch;
  global.AbortController = realAbortController;
  // Leave no timer chain behind for the next file in this worker.
  YaverFeedback.destroy();
  jest.useRealTimers();
});

beforeEach(() => {
  jest.clearAllMocks();
  discovered = null;
  storedToken = null;
  YaverFeedback.destroy();
});

afterEach(() => {
  BlackBox.stop();
  YaverFeedback.destroy();
});

describe('BlackBox auto-start on a cold start', () => {
  it('starts once discovery resolves well after the old 500ms window', async () => {
    const startSpy = jest.spyOn(BlackBox, 'start').mockImplementation(() => {});
    storedToken = 'tok-from-storage';

    // Nothing passed in — the real cold-start shape.
    YaverFeedback.init({ trigger: 'shake', blackBox: { appName: 'talos-mobile' } });

    // The window the old implementation checked, and only that window.
    await jest.advanceTimersByTimeAsync(600);
    expect(startSpy).not.toHaveBeenCalled();

    // Discovery lands late, as it does on a real cold, off-LAN start.
    discovered = 'http://10.0.0.5:18080';
    await jest.advanceTimersByTimeAsync(4000);

    expect(startSpy).toHaveBeenCalledWith(
      expect.objectContaining({ appName: 'talos-mobile' }),
    );
    startSpy.mockRestore();
  });

  it('never starts without a token, however long it waits', async () => {
    // The guard that matters: starting tokenless makes the SSE channel 401 and
    // retry with backoff — the string-concat + JSON-parse loop that SIGSEGV'd
    // Hermes on iOS 18.3.1. Retrying must not erode it.
    const startSpy = jest.spyOn(BlackBox, 'start').mockImplementation(() => {});
    storedToken = null;
    discovered = 'http://10.0.0.5:18080';

    YaverFeedback.init({ trigger: 'shake' });
    await jest.advanceTimersByTimeAsync(90_000);

    expect(startSpy).not.toHaveBeenCalled();
    startSpy.mockRestore();
  });

  it('gives up rather than polling forever', async () => {
    const startSpy = jest.spyOn(BlackBox, 'start').mockImplementation(() => {});
    storedToken = null;

    YaverFeedback.init({ trigger: 'shake' });
    // Past the ~60s bound.
    await jest.advanceTimersByTimeAsync(90_000);

    // Credentials arrive after the window closed — the poll is done.
    storedToken = 'late';
    discovered = 'http://10.0.0.5:18080';
    await jest.advanceTimersByTimeAsync(10_000);

    expect(startSpy).not.toHaveBeenCalled();
    startSpy.mockRestore();
  });

  it('signing in re-arms after the bound has passed', async () => {
    // Giving up is only acceptable because this path exists: a user who signs
    // in later gets the channel without restarting the app.
    const startSpy = jest.spyOn(BlackBox, 'start').mockImplementation(() => {});

    YaverFeedback.init({ trigger: 'shake' });
    await jest.advanceTimersByTimeAsync(90_000);
    expect(startSpy).not.toHaveBeenCalled();

    discovered = 'http://10.0.0.5:18080';
    await YaverFeedback.setAuthToken('tok-from-login');
    await jest.advanceTimersByTimeAsync(2000);

    expect(startSpy).toHaveBeenCalled();
    startSpy.mockRestore();
  });

  it('destroy() cancels a pending retry', async () => {
    // A retry chain firing against a torn-down config would start the recorder
    // for an SDK the host believes is off.
    const startSpy = jest.spyOn(BlackBox, 'start').mockImplementation(() => {});
    storedToken = 'tok';

    YaverFeedback.init({ trigger: 'shake' });
    await jest.advanceTimersByTimeAsync(600);

    YaverFeedback.destroy();
    discovered = 'http://10.0.0.5:18080';
    await jest.advanceTimersByTimeAsync(10_000);

    expect(startSpy).not.toHaveBeenCalled();
    startSpy.mockRestore();
  });
});
