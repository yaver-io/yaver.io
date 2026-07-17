// BlackBox auto-start config passthrough.
//
// The natural way to configure the flight recorder — and the one the README's
// own snippet shows — is:
//
//     YaverFeedback.init({ trigger: 'shake' });
//     BlackBox.start({ appName: 'my-app' });
//
// That start() early-returns: a zero-config init has no agentUrl yet, because
// discovery resolves it asynchronously. The SDK's autoStartBlackBox then fires
// ~500ms later once the agent and token exist — and used to call start() with
// no arguments, quietly replacing the host's config with defaults and leaving
// appName as ''. Hosts had no way to tell: the recorder streamed, just
// anonymously.
//
// config.blackBox is now the supported channel, and auto-start honours it.

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

jest.useFakeTimers();

const mockFetch = jest.fn(() =>
  Promise.resolve({ ok: true, json: () => Promise.resolve({}), body: null }),
);
global.fetch = mockFetch as any;

class MockAbortController {
  signal = { aborted: false };
  abort = jest.fn();
}
global.AbortController = MockAbortController as any;

beforeEach(() => {
  jest.clearAllMocks();
  YaverFeedback.destroy();
});

afterEach(() => {
  BlackBox.stop();
  YaverFeedback.destroy();
});

describe('autoStartBlackBox config passthrough', () => {
  it('starts BlackBox with the config passed on init', async () => {
    const startSpy = jest.spyOn(BlackBox, 'start').mockImplementation(() => {});

    YaverFeedback.init({
      trigger: 'shake',
      agentUrl: 'http://localhost:18080',
      authToken: 'tok',
      blackBox: { appName: 'talos-mobile', flushInterval: 3000, maxBufferSize: 25 },
    });

    await jest.advanceTimersByTimeAsync(600);

    expect(startSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        appName: 'talos-mobile',
        flushInterval: 3000,
        maxBufferSize: 25,
      }),
    );
    startSpy.mockRestore();
  });

  it('still auto-starts when no blackBox config is given', async () => {
    const startSpy = jest.spyOn(BlackBox, 'start').mockImplementation(() => {});

    YaverFeedback.init({
      trigger: 'shake',
      agentUrl: 'http://localhost:18080',
      authToken: 'tok',
    });

    await jest.advanceTimersByTimeAsync(600);

    expect(startSpy).toHaveBeenCalled();
    startSpy.mockRestore();
  });

  it('does not auto-start when autoStartBlackBox is false', async () => {
    const startSpy = jest.spyOn(BlackBox, 'start').mockImplementation(() => {});

    YaverFeedback.init({
      trigger: 'shake',
      agentUrl: 'http://localhost:18080',
      authToken: 'tok',
      autoStartBlackBox: false,
      blackBox: { appName: 'talos-mobile' },
    });

    await jest.advanceTimersByTimeAsync(600);

    expect(startSpy).not.toHaveBeenCalled();
    startSpy.mockRestore();
  });
});
