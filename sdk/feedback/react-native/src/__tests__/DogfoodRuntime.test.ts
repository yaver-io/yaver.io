import {
  DogfoodController,
  DogfoodRuntimeError,
  defaultDogfoodLane,
  dogfoodLanePlan,
  dogfoodLaneOptions,
  dogfoodLogLinesFromDevEvent,
  validateDogfoodProject,
  type DogfoodDriver,
  type DogfoodProject,
} from '../DogfoodRuntime';

const expo: DogfoodProject = {
  name: 'Example', workDir: '/workspace/example', framework: 'expo', lane: 'browser',
};

describe('DogfoodController', () => {
  test('stopping immediately prevents a queued launch from starting', async () => {
    const start = jest.fn(async () => ({ lane: 'browser' as const }));
    const controller = new DogfoodController(expo, { start });
    const run = controller.trigger();
    await controller.stop();
    await expect(run).rejects.toMatchObject({ failure: { code: 'DOGFOOD_ATTEMPT_REPLACED' } });
    expect(start).not.toHaveBeenCalled();
    expect(controller.snapshot().phase).toBe('stopped');
  });

  test('a slow Stop cannot overwrite a successful Retry', async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    let starts = 0;
    const controller = new DogfoodController(expo, {
      async start(ctx) {
        starts += 1;
        if (starts === 1) ctx.registerCleanup(() => gate);
        return { lane: 'browser', sessionId: `session-${starts}` };
      },
    });
    await controller.trigger();
    const stopping = controller.stop();
    await controller.retry();
    release();
    await stopping;
    expect(controller.snapshot()).toMatchObject({ phase: 'ready', result: { sessionId: 'session-2' } });
  });

  test('a delayed handoff cannot discard the replacement runtime cleanup', async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const stopNew = jest.fn();
    let starts = 0;
    const controller = new DogfoodController(expo, {
      async start(ctx) {
        starts += 1;
        if (starts === 1) ctx.registerCleanup(() => gate, 'transient');
        else ctx.registerCleanup(stopNew);
        return { lane: 'browser', sessionId: `session-${starts}` };
      },
    });
    await controller.trigger();
    const handingOff = controller.handoff();
    await controller.stop();
    await controller.retry();
    release();
    expect(await handingOff).toBeUndefined();
    await controller.stop();
    expect(stopNew).toHaveBeenCalledTimes(1);
  });

  test('does no work before the explicit trigger', () => {
    const driver: DogfoodDriver = { start: jest.fn(async () => ({ lane: 'browser' as const })) };
    const controller = new DogfoodController(expo, driver);
    expect(driver.start).not.toHaveBeenCalled();
    expect(controller.snapshot().phase).toBe('idle');
  });

  test('preserves raw npm output and hands a live session to the host', async () => {
    const stop = jest.fn();
    const closeLogs = jest.fn();
    const controller = new DogfoodController(expo, {
      async start(ctx) {
        ctx.registerCleanup(stop, 'session');
        ctx.registerCleanup(closeLogs, 'transient');
        ctx.log('$ npm install --legacy-peer-deps');
        ctx.log('npm warn deprecated example@1.0.0');
        return { lane: 'browser', sessionId: 's1', url: 'http://agent/dev/' };
      },
    });

    await controller.trigger();
    expect(controller.snapshot().logs.map((line) => line.text)).toEqual([
      '$ npm install --legacy-peer-deps',
      'npm warn deprecated example@1.0.0',
    ]);
    expect(await controller.handoff()).toMatchObject({ sessionId: 's1' });
    expect(closeLogs).toHaveBeenCalledTimes(1);
    expect(stop).not.toHaveBeenCalled();
  });

  test('cleans a partial session before exposing a structured failure', async () => {
    const stop = jest.fn();
    const controller = new DogfoodController(expo, {
      async start(ctx) {
        ctx.registerCleanup(stop);
        throw new DogfoodRuntimeError({
          code: 'DOGFOOD_RENDER_FAILED', error: 'Metro exited', remedy: 'Fix Metro and retry.', retryable: true,
        });
      },
    });
    await expect(controller.trigger()).rejects.toThrow('Metro exited');
    expect(stop).toHaveBeenCalledTimes(1);
    expect(controller.snapshot()).toMatchObject({ phase: 'failed', failure: { code: 'DOGFOOD_RENDER_FAILED' } });
  });

  test('an obsolete attempt cannot clean up the replacement attempt', async () => {
    let releaseFirst!: () => void;
    const firstGate = new Promise<void>((resolve) => { releaseFirst = resolve; });
    let firstStarted!: () => void;
    const firstStartedGate = new Promise<void>((resolve) => { firstStarted = resolve; });
    const stopFirst = jest.fn();
    const stopSecond = jest.fn();
    let starts = 0;
    const controller = new DogfoodController(expo, {
      async start(ctx) {
        starts += 1;
        if (starts === 1) {
          ctx.registerCleanup(stopFirst);
          firstStarted();
          await firstGate;
          return { lane: 'browser', sessionId: 'old' };
        }
        ctx.registerCleanup(stopSecond);
        return { lane: 'browser', sessionId: 'new' };
      },
    });

    const old = controller.trigger();
    await firstStartedGate;
    await controller.stop();
    await expect(controller.trigger()).resolves.toMatchObject({ sessionId: 'new' });
    releaseFirst();
    await expect(old).rejects.toMatchObject({ failure: { code: 'DOGFOOD_ATTEMPT_REPLACED' } });
    expect(stopFirst).toHaveBeenCalledTimes(1);
    expect(stopSecond).not.toHaveBeenCalled();
    await controller.stop();
    expect(stopSecond).toHaveBeenCalledTimes(1);
  });

  test('Stop aborts the active driver and remains a clean stopped state', async () => {
    let started!: () => void;
    const startedGate = new Promise<void>((resolve) => { started = resolve; });
    const stopRemote = jest.fn();
    const controller = new DogfoodController(expo, {
      async start(ctx) {
        ctx.registerCleanup(stopRemote);
        started();
        await new Promise<void>((_resolve, reject) => {
          ctx.signal.addEventListener('abort', () => {
            const error = new Error('Aborted');
            error.name = 'AbortError';
            reject(error);
          }, { once: true });
        });
        return { lane: 'browser' };
      },
    });

    const run = controller.trigger();
    await startedGate;
    await controller.stop();
    await expect(run).rejects.toMatchObject({ failure: { code: 'DOGFOOD_REQUEST_TIMEOUT' } });
    expect(stopRemote).toHaveBeenCalledTimes(1);
    expect(controller.snapshot().phase).toBe('stopped');
    expect(controller.snapshot().failure).toBeUndefined();
  });

  test('keeps a failed preferred lane in the console and automatically recovers through browser', async () => {
    const stopPreferred = jest.fn();
    const lanes: string[] = [];
    const controller = new DogfoodController({
      ...expo,
      lane: 'hermes',
      fallbackLane: 'browser',
    }, {
      async start(ctx) {
        lanes.push(ctx.project.lane);
        if (ctx.project.lane === 'hermes') {
          ctx.registerCleanup(stopPreferred);
          throw new DogfoodRuntimeError({
            code: 'DOGFOOD_HERMES_BUILD_FAILED',
            error: 'Hermes build failed',
            remedy: 'Use the browser build.',
            retryable: true,
          });
        }
        return { lane: 'browser', url: 'http://agent/dev/', metadata: { recovered: true } };
      },
    });

    await expect(controller.trigger()).resolves.toMatchObject({
      lane: 'browser',
      metadata: { fallbackFrom: 'hermes', fallbackReason: 'DOGFOOD_HERMES_BUILD_FAILED' },
    });
    expect(lanes).toEqual(['hermes', 'browser']);
    expect(stopPreferred).toHaveBeenCalledTimes(1);
    expect(controller.snapshot()).toMatchObject({ phase: 'ready', project: { lane: 'browser' } });
    expect(controller.snapshot().logs.map((line) => line.text)).toContain(
      '[fallback] hermes failed (DOGFOOD_HERMES_BUILD_FAILED); trying browser',
    );
  });
});

describe('Dogfood lanes and console events', () => {
  test('Flutter is first-class on browser but cannot be mislabeled Hermes', () => {
    expect(validateDogfoodProject({ ...expo, framework: 'flutter', lane: 'browser' })).toBeNull();
    expect(validateDogfoodProject({ ...expo, framework: 'flutter', lane: 'hermes' })?.code)
      .toBe('DOGFOOD_HERMES_FRAMEWORK_UNSUPPORTED');
  });

  test('uses the browser lane by default for React Native and Flutter', () => {
    const rn = dogfoodLaneOptions('expo', { nativeRuntimeAvailable: true });
    expect(defaultDogfoodLane('expo')).toBe('browser');
    expect(rn.map((option) => [option.lane, option.supported])).toEqual([
      ['browser', true], ['hermes', true], ['webrtc', true],
    ]);
    const flutter = dogfoodLaneOptions('flutter', { nativeRuntimeAvailable: true });
    expect(defaultDogfoodLane('flutter')).toBe('browser');
    expect(flutter.find((option) => option.lane === 'hermes')?.supported).toBe(false);
  });

  test('never turns a failed React Native phone reload into a browser-only false success', () => {
    expect(dogfoodLanePlan('flutter', { nativeRuntimeAvailable: true })).toMatchObject({
      preferred: 'browser', fallback: undefined,
    });
    expect(dogfoodLanePlan('expo', { nativeRuntimeAvailable: true }, 'hermes')).toMatchObject({
      preferred: 'hermes', fallback: undefined,
    });
    expect(dogfoodLanePlan('flutter', { nativeRuntimeAvailable: true }, 'webrtc')).toMatchObject({
      preferred: 'webrtc', fallback: 'browser',
    });
    expect(dogfoodLanePlan('swift', { nativeRuntimeAvailable: true }, 'webrtc')).toMatchObject({
      preferred: 'webrtc', fallback: undefined,
    });
  });

  test('keeps Yaver self-development on the same RN three-lane contract', () => {
    const options = dogfoodLaneOptions('expo', { nativeRuntimeAvailable: true, selfDevelopment: true });
    expect(options).toHaveLength(3);
    expect(options.find((option) => option.lane === 'hermes')).toMatchObject({ supported: true });
    expect(options.find((option) => option.lane === 'webrtc')).toMatchObject({ supported: true });
  });

  test('native-only projects use WebRTC rather than fake browser or Hermes lanes', () => {
    const swift = dogfoodLaneOptions('swift', { nativeRuntimeAvailable: true });
    expect(swift.map((option) => [option.lane, option.supported])).toEqual([
      ['browser', false], ['hermes', false], ['webrtc', true],
    ]);
    expect(dogfoodLaneOptions('kotlin', { nativeRuntimeAvailable: true })
      .map((option) => [option.lane, option.supported])).toEqual([
      ['browser', false], ['hermes', false], ['webrtc', true],
    ]);
    expect(defaultDogfoodLane('swift', { nativeRuntimeAvailable: true })).toBe('webrtc');
  });

  test('a detected browser target adds real framework exceptions such as SwiftWasm', () => {
    const swiftWasm = dogfoodLaneOptions('swift', {
      nativeRuntimeAvailable: false,
      browserRuntimeAvailable: true,
    });
    expect(swiftWasm.map((option) => [option.lane, option.supported])).toEqual([
      ['browser', true], ['hermes', false], ['webrtc', false],
    ]);
  });

  test('reads raw and replayed package-manager logs from /dev/events', () => {
    expect(dogfoodLogLinesFromDevEvent({ type: 'log', logLine: '$ npm ci\u001b[0m' }))
      .toEqual(['$ npm ci\u001b[0m']);
    expect(dogfoodLogLinesFromDevEvent({ type: 'snapshot', snapshot: { recentLogs: ['npm warn x', 'Metro ready'] } }))
      .toEqual(['npm warn x', 'Metro ready']);
  });
});
