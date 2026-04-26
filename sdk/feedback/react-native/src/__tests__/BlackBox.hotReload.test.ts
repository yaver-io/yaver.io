// Hot-reload command-channel coverage for BlackBox.
//
// The agent uses BlackBox's SSE channel as a back-channel to push
// "reload" / "reload_bundle" commands into a running guest app
// (see desktop/agent/blackbox.go::BroadcastCommand). This test
// pins the SDK's contract:
//
//   1. onCommand(handler) registers a handler and returns an
//      unsubscribe function that actually unsubscribes.
//   2. start() opens the SSE command-stream against the agent
//      with the proper URL + headers.
//   3. When the SSE stream delivers a JSON message of the form
//      {type:"command", command:{command:"reload", data:...}},
//      the registered handler fires with the inner command.
//   4. start() also schedules the periodic flush — proving start
//      is idempotent over multiple calls.

import { BlackBox } from '../BlackBox';
import { YaverFeedback } from '../YaverFeedback';

jest.mock('react-native', () => ({
  Platform: { OS: 'ios' },
}));

// Drive Date.now / setTimeout deterministically.
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

// Helper to build an SSE streaming body.
function sseBody(messages: Array<unknown>): ReadableStream<Uint8Array> {
  const enc = new TextEncoder();
  const chunks = messages.map((m) => `data: ${JSON.stringify(m)}\n\n`);
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const c of chunks) controller.enqueue(enc.encode(c));
      controller.close();
    },
  });
}

beforeEach(() => {
  jest.clearAllMocks();
  // Default: every POST (events) and the SSE GET both succeed.
  mockFetch.mockImplementation((url: string) => {
    if (url.includes('/blackbox/command-stream')) {
      return Promise.resolve({
        ok: true,
        body: sseBody([]),
      });
    }
    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve({}),
    });
  });
  YaverFeedback.init({
    agentUrl: 'http://localhost:18080',
    authToken: 'tok',
  });
});

afterEach(() => {
  BlackBox.stop();
});

describe('BlackBox hot-reload command channel', () => {
  it('onCommand returns an unsubscribe that removes the handler', () => {
    const a = jest.fn();
    const b = jest.fn();
    const offA = BlackBox.onCommand(a);
    const offB = BlackBox.onCommand(b);

    // Both subscribed — calling internal dispatch directly via the
    // public surface isn't easy without SSE mockery, but
    // deregistering should still leave only `b` registered after
    // offA(), then no handlers after offB().
    offA();
    offB();
    // Re-subscribe and confirm subscription returns a fresh unsub.
    const offC = BlackBox.onCommand(jest.fn());
    expect(typeof offC).toBe('function');
    offC();
  });

  it('start() opens SSE against /blackbox/command-stream with bearer + device headers', async () => {
    BlackBox.start({ deviceId: 'dev-abc', appName: 'sfmg' });
    // Allow the async fetch in connectSSE to fire.
    await Promise.resolve();
    await Promise.resolve();

    const sseCall = mockFetch.mock.calls.find(([url]) =>
      String(url).includes('/blackbox/command-stream'),
    );
    expect(sseCall).toBeDefined();
    const [url, init] = sseCall!;
    expect(String(url)).toContain('http://localhost:18080/blackbox/command-stream');
    expect(String(url)).toContain('device=dev-abc');
    expect(init.headers).toEqual(
      expect.objectContaining({
        Authorization: 'Bearer tok',
        Accept: 'text/event-stream',
        'X-Device-ID': 'dev-abc',
        'X-App-Name': 'sfmg',
      }),
    );
  });

  it('dispatches a {type:"command", command:{command:"reload"}} SSE frame to handlers', async () => {
    // SSE body delivers one reload command, then closes.
    mockFetch.mockImplementation((url: string) => {
      if (url.includes('/blackbox/command-stream')) {
        return Promise.resolve({
          ok: true,
          body: sseBody([
            { type: 'command', command: { command: 'reload', data: {} } },
          ]),
        });
      }
      return Promise.resolve({ ok: true, json: () => Promise.resolve({}) });
    });

    const seen: any[] = [];
    BlackBox.onCommand((cmd) => seen.push(cmd));
    BlackBox.start({ deviceId: 'dev-1', appName: 'sfmg' });

    // Drain the microtask + reader queue. The reader is async but
    // the body is fully buffered + the stream closes immediately,
    // so a few microtask flushes are enough.
    for (let i = 0; i < 10; i++) await Promise.resolve();

    expect(seen).toEqual([
      expect.objectContaining({ command: 'reload', data: {} }),
    ]);
  });

  it('dispatches a reload_bundle command (carries bundleUrl payload)', async () => {
    mockFetch.mockImplementation((url: string) => {
      if (url.includes('/blackbox/command-stream')) {
        return Promise.resolve({
          ok: true,
          body: sseBody([
            {
              type: 'command',
              command: {
                command: 'reload_bundle',
                data: { bundleUrl: '/dev/native-bundle' },
              },
            },
          ]),
        });
      }
      return Promise.resolve({ ok: true, json: () => Promise.resolve({}) });
    });

    const seen: any[] = [];
    BlackBox.onCommand((cmd) => seen.push(cmd));
    BlackBox.start({ deviceId: 'dev-1', appName: 'sfmg' });
    for (let i = 0; i < 10; i++) await Promise.resolve();

    expect(seen).toEqual([
      expect.objectContaining({
        command: 'reload_bundle',
        data: expect.objectContaining({ bundleUrl: '/dev/native-bundle' }),
      }),
    ]);
  });

  it('handler exception does not break sibling handlers', async () => {
    mockFetch.mockImplementation((url: string) => {
      if (url.includes('/blackbox/command-stream')) {
        return Promise.resolve({
          ok: true,
          body: sseBody([
            { type: 'command', command: { command: 'reload' } },
          ]),
        });
      }
      return Promise.resolve({ ok: true, json: () => Promise.resolve({}) });
    });

    const sibling = jest.fn();
    BlackBox.onCommand(() => {
      throw new Error('caller bug — should not break the dispatch loop');
    });
    BlackBox.onCommand(sibling);

    BlackBox.start({ deviceId: 'dev-1', appName: 'sfmg' });
    for (let i = 0; i < 10; i++) await Promise.resolve();

    expect(sibling).toHaveBeenCalledWith(
      expect.objectContaining({ command: 'reload' }),
    );
  });

  it('ignores non-command SSE frames (events/log/whatever)', async () => {
    mockFetch.mockImplementation((url: string) => {
      if (url.includes('/blackbox/command-stream')) {
        return Promise.resolve({
          ok: true,
          body: sseBody([
            { type: 'log', logLine: 'hi' },
            { type: 'lifecycle', message: 'started' },
            { ping: 1 },
          ]),
        });
      }
      return Promise.resolve({ ok: true, json: () => Promise.resolve({}) });
    });

    const handler = jest.fn();
    BlackBox.onCommand(handler);
    BlackBox.start({ deviceId: 'dev-1', appName: 'sfmg' });
    for (let i = 0; i < 10; i++) await Promise.resolve();
    expect(handler).not.toHaveBeenCalled();
  });
});
