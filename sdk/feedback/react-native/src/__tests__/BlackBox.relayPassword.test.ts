// BlackBox relay-password sourcing.
//
// This exists because BlackBox.start() used to read the relay password off
// the feedback config:
//
//     BlackBox.relayPassword =
//       (feedbackConfig as { relayPassword?: string }).relayPassword ?? '';
//
// `relayPassword` is not a key on FeedbackConfig. The cast is what stopped
// the compiler from saying so, and the field was therefore permanently ''.
// Since every X-Relay-Password header is written behind an
// `if (BlackBox.relayPassword)` guard, the header was never sent, and the
// relay 401s an empty password (relay/server.go). Net effect: on cellular
// (the only time the relay is used at all) the SSE command channel died,
// so the reload that lands after an agent fix never fired. On-LAN the
// password is unused, so this was invisible in local dev — it only ever
// broke remote users.
//
// The contract pinned here: BlackBox takes its relay password from
// YaverFeedback.getRelayPassword(), the same source P2PClient uses.

import { BlackBox } from '../BlackBox';
import { YaverFeedback } from '../YaverFeedback';

jest.mock('react-native', () => ({
  Platform: { OS: 'ios' },
}));

jest.useFakeTimers();

const mockFetch = jest.fn();
global.fetch = mockFetch as any;

class MockAbortController {
  signal = { aborted: false };
  abort = jest.fn(() => {
    this.signal.aborted = true;
  });
}
global.AbortController = MockAbortController as any;

function emptySse(): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      controller.close();
    },
  });
}

beforeEach(() => {
  jest.clearAllMocks();
  mockFetch.mockImplementation((url: string) => {
    if (String(url).includes('/blackbox/command-stream')) {
      return Promise.resolve({ ok: true, body: emptySse() });
    }
    return Promise.resolve({ ok: true, json: () => Promise.resolve({}) });
  });
  // A relay-routed agent URL — the shape discovery produces off-LAN.
  YaverFeedback.init({
    agentUrl: 'https://public.yaver.io/d/macmini-1',
    authToken: 'tok',
  });
});

afterEach(() => {
  BlackBox.stop();
  jest.restoreAllMocks();
});

describe('BlackBox relay password', () => {
  it('sends X-Relay-Password on the SSE command stream, sourced from getRelayPassword()', async () => {
    jest.spyOn(YaverFeedback, 'getRelayPassword').mockReturnValue('s3cret-relay-pw');

    BlackBox.start({ deviceId: 'dev-abc', appName: 'talos-mobile' });
    await Promise.resolve();
    await Promise.resolve();

    const sseCall = mockFetch.mock.calls.find(([url]) =>
      String(url).includes('/blackbox/command-stream'),
    );
    expect(sseCall).toBeDefined();
    const [, init] = sseCall!;
    expect(init.headers).toEqual(
      expect.objectContaining({ 'X-Relay-Password': 's3cret-relay-pw' }),
    );
  });

  it('sends X-Relay-Password on the event flush', async () => {
    jest.spyOn(YaverFeedback, 'getRelayPassword').mockReturnValue('s3cret-relay-pw');

    // maxBufferSize:1 makes push() flush synchronously on the first event,
    // so this doesn't hang on interval semantics.
    BlackBox.start({ deviceId: 'dev-abc', maxBufferSize: 1 });
    BlackBox.log('hello');
    await Promise.resolve();

    const flush = mockFetch.mock.calls.find(([url]) =>
      String(url).includes('/blackbox/events'),
    );
    expect(flush).toBeDefined();
    const [, init] = flush!;
    expect(init.headers).toEqual(
      expect.objectContaining({ 'X-Relay-Password': 's3cret-relay-pw' }),
    );
  });

  it('omits the header entirely when there is no relay password (direct LAN)', async () => {
    // An empty header value is itself a 401 at the relay, so "no password"
    // must mean "no header", not "empty header".
    jest.spyOn(YaverFeedback, 'getRelayPassword').mockReturnValue('');

    BlackBox.start({ deviceId: 'dev-abc' });
    await Promise.resolve();
    await Promise.resolve();

    const sseCall = mockFetch.mock.calls.find(([url]) =>
      String(url).includes('/blackbox/command-stream'),
    );
    expect(sseCall).toBeDefined();
    const [, init] = sseCall!;
    expect(init.headers).not.toHaveProperty('X-Relay-Password');
  });

  it('does not read a relayPassword key off the feedback config', () => {
    // The original bug in one assertion: FeedbackConfig has no such key, so
    // anyone reintroducing that read gets '' and silently breaks the relay.
    expect(YaverFeedback.getConfig()).not.toHaveProperty('relayPassword');
  });
});
